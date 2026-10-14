package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

const (
	// maxPages is a hard cap on the number of pages fetched per endpoint,
	// preventing unbounded loops if the API returns inconsistent pagination
	// metadata.
	maxPages = 500

	// maxItemsPerEndpoint bounds the total number of accumulated items (data
	// plus linked entities) per endpoint (issue #19). FetchAllPages holds all
	// items in memory before writing, so without this cap a misbehaving or
	// hostile API could drive memory use unbounded (500 pages × 100 MiB).
	// 1,000,000 items is far above any realistic Holaspirit org while keeping
	// worst-case memory bounded.
	maxItemsPerEndpoint = 1_000_000

	// pageSize is requested with the `count` query parameter, the page-size
	// parameter documented in Holaspirit's published OpenAPI spec.
	pageSize = 100
)

// Collection is the complete content of a paginated list endpoint.
type Collection struct {
	// Items are the `data` entries of all pages, in API order.
	Items []json.RawMessage
	// Linked holds the side-loaded entities from the `linked` blocks of all
	// pages, keyed by entity type and de-duplicated. Holaspirit returns
	// referenced records here — e.g. role accountabilities, domains and
	// policies — while `data` often carries only their IDs, so dropping
	// `linked` would lose content.
	Linked map[string][]json.RawMessage
	// Reported is the total item count announced by the API, or -1 when the
	// response carried none.
	Reported int
}

// Missing returns how many items the API announced but FetchAllPages did not
// retrieve. Zero means the collection is complete (or the API reported no total).
func (c *Collection) Missing() int {
	if c.Reported < 0 || len(c.Items) >= c.Reported {
		return 0
	}
	return c.Reported - len(c.Items)
}

// envelope is one page of a Holaspirit list response. Every block except
// `data` is kept raw and decoded leniently: PHP backends serialize an empty
// object as `[]`, and a strict decode of a shape we did not expect must not
// abort the whole backup.
type envelope struct {
	Data       json.RawMessage `json:"data"`
	Linked     json.RawMessage `json:"linked"`
	Pagination json.RawMessage `json:"pagination"`
	Meta       json.RawMessage `json:"meta"`
}

// pageInfo is the pagination metadata of one page, from whichever of the two
// known formats the response used.
type pageInfo struct {
	total, pages, next optInt
	// spec is true when the response carried the documented top-level
	// `pagination` object.
	spec bool
}

// parsePageInfo reads pagination metadata in the documented format
//
//	{"pagination": {"totalItems", "currentPage", "nextPage", "pagesCount", ...}}
//
// and, as a fallback, the format earlier versions of this tool assumed
//
//	{"meta": {"pagination": {"total", "total_pages", ...}}}.
//
// The old code only understood the second one; against an API that sends the
// first, it saw no page count and stopped after page 1.
func parsePageInfo(env envelope) pageInfo {
	var spec struct {
		TotalItems optInt `json:"totalItems"`
		NextPage   optInt `json:"nextPage"`
		PagesCount optInt `json:"pagesCount"`
	}
	if len(env.Pagination) > 0 && json.Unmarshal(env.Pagination, &spec) == nil &&
		bytes.HasPrefix(bytes.TrimSpace(env.Pagination), []byte("{")) {
		return pageInfo{total: spec.TotalItems, pages: spec.PagesCount, next: spec.NextPage, spec: true}
	}
	var meta struct {
		Pagination struct {
			Total      optInt `json:"total"`
			TotalPages optInt `json:"total_pages"`
		} `json:"pagination"`
	}
	if len(env.Meta) > 0 && json.Unmarshal(env.Meta, &meta) == nil {
		return pageInfo{total: meta.Pagination.Total, pages: meta.Pagination.TotalPages}
	}
	return pageInfo{}
}

// FetchAllPages retrieves every page of a list endpoint and returns the
// combined `data` items plus the de-duplicated `linked` entities.
//
// Termination, in order of preference: the page count the API reports; the
// documented `nextPage` pointer; and, when the response carries no pagination
// metadata at all, the first empty page.
func FetchAllPages(ctx context.Context, client *Client, path string) (*Collection, error) {
	c := &Collection{Items: []json.RawMessage{}, Linked: map[string][]json.RawMessage{}, Reported: -1}
	seen := map[string]bool{}     // linked entities, by type + id
	itemSeen := map[string]bool{} // data items, by id
	linkedCount := 0

	for page := 1; ; page++ {
		if page > maxPages {
			return nil, fmt.Errorf("pagination safety limit reached (%d pages) for %s", maxPages, path)
		}

		// Build the query safely so a path with existing query characters or
		// reserved bytes cannot corrupt the request URL (issue #30).
		q := url.Values{}
		q.Set("page", strconv.Itoa(page))
		q.Set("count", strconv.Itoa(pageSize))
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&" // path already carries filters (e.g. time windows)
		}
		body, err := client.Get(ctx, path+sep+q.Encode())
		if err != nil {
			return nil, fmt.Errorf("page %d of %s: %w", page, path, err)
		}

		var env envelope
		if err := json.Unmarshal(body, &env); err != nil {
			return nil, fmt.Errorf("parse page %d of %s: %w", page, path, err)
		}
		if len(env.Data) == 0 {
			return nil, fmt.Errorf("page %d of %s: response has no data field", page, path)
		}
		info := parsePageInfo(env)
		hasMeta := info.spec || info.pages.ok

		var items []json.RawMessage
		if err := json.Unmarshal(env.Data, &items); err != nil {
			// data is a single object, not a list: nothing to page through.
			c.Items = append(c.Items, env.Data)
			c.addLinked(env.Linked, seen)
			return c, nil
		}

		// An API that ignores the page parameter hands back the same items on
		// every page. Compare by item identity, not bytes, so a reordered or
		// re-timestamped repeat is caught too.
		if page > 1 && len(items) > 0 && allSeen(items, itemSeen) {
			if hasMeta {
				return nil, fmt.Errorf("%s: page %d only repeats items from earlier pages although the API reports more pages — the page parameter seems to be ignored", path, page)
			}
			// No metadata and a repeated page: the endpoint is not paginated
			// and page 1 already held everything. Drop the duplicate.
			return c, nil
		}
		for _, it := range items {
			itemSeen[entityKey(it)] = true
		}

		c.Items = append(c.Items, items...)
		linkedCount += c.addLinked(env.Linked, seen)
		if len(c.Items)+linkedCount > maxItemsPerEndpoint {
			return nil, fmt.Errorf("item safety limit reached (%d items) for %s — possible runaway pagination", maxItemsPerEndpoint, path)
		}
		if info.total.ok {
			c.Reported = info.total.v
		}

		switch {
		case info.pages.ok:
			if page >= info.pages.v {
				return c, nil
			}
		case info.spec:
			// Documented format without a page count: follow nextPage.
			if !info.next.ok || info.next.v <= page {
				return c, nil
			}
		default:
			// No pagination metadata: keep going until a page comes back empty.
			if len(items) == 0 {
				return c, nil
			}
		}
	}
}

// addLinked merges one page's `linked` block into c.Linked, skipping entities
// already seen on earlier pages. It returns the number of entities added.
func (c *Collection) addLinked(raw json.RawMessage, seen map[string]bool) int {
	byType := map[string][]json.RawMessage{}
	var obj map[string]json.RawMessage
	var arr []json.RawMessage
	switch {
	case json.Unmarshal(raw, &obj) == nil:
		for typ, v := range obj {
			var ents []json.RawMessage
			if json.Unmarshal(v, &ents) != nil {
				ents = []json.RawMessage{v} // a single entity instead of a list
			}
			byType[typ] = ents
		}
	case json.Unmarshal(raw, &arr) == nil && len(arr) > 0:
		// Unexpected shape (a flat list). Keep it rather than lose it.
		byType["_untyped"] = arr
	default:
		return 0 // absent, null, or an empty PHP array
	}

	added := 0
	for typ, ents := range byType {
		for _, e := range ents {
			key := typ + "\x00" + entityKey(e)
			if seen[key] {
				continue
			}
			seen[key] = true
			c.Linked[typ] = append(c.Linked[typ], e)
			added++
		}
	}
	return added
}

// allSeen reports whether every item is already in seen.
func allSeen(items []json.RawMessage, seen map[string]bool) bool {
	for _, it := range items {
		if !seen[entityKey(it)] {
			return false
		}
	}
	return true
}

// entityKey identifies an entity by its `id`, or by a hash of its raw JSON
// when it has none.
func entityKey(e json.RawMessage) string {
	var v struct {
		ID json.RawMessage `json:"id"`
	}
	if json.Unmarshal(e, &v) == nil && len(v.ID) > 0 && string(v.ID) != "null" {
		return "id:" + string(v.ID)
	}
	sum := sha256.Sum256(e)
	return "raw:" + hex.EncodeToString(sum[:])
}

// optInt is an integer that may be absent, null, or sent as a numeric string.
type optInt struct {
	v  int
	ok bool
}

func (o *optInt) UnmarshalJSON(b []byte) error {
	s := string(bytes.Trim(bytes.TrimSpace(b), `"`))
	if s == "" || s == "null" {
		*o = optInt{}
		return nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		// Not a number: treat as unknown rather than failing the page.
		*o = optInt{}
		return nil
	}
	*o = optInt{v: n, ok: true}
	return nil
}
