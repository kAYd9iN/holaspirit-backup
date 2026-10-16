package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

const (
	// maxPages is a hard cap on the number of pages fetched per collection,
	// preventing unbounded loops if the API returns inconsistent cursors.
	maxPages = 5000

	// maxItemsPerEndpoint bounds the total number of accumulated items per
	// collection (issue #19). FetchAll holds all items in memory before
	// writing, so without this cap a misbehaving or hostile API could drive
	// memory use unbounded.
	maxItemsPerEndpoint = 1_000_000

	// DefaultLimit is the page size requested when an endpoint does not set
	// one: the documented maximum of most Partner API list routes.
	DefaultLimit = 199
)

// ErrTraversalCap is returned (wrapped, together with the items read so far)
// when the API refuses to page further: some sorted Partner API lists stop at
// 10,000 rows with 400 INVALID_CURSOR.
var ErrTraversalCap = errors.New("the API refused to page further (traversal cap)")

// Collection is the content of a paginated list endpoint.
type Collection struct {
	// Items are the `data` entries of all pages, in API order. A response
	// whose `data` is a single object yields one item.
	Items []json.RawMessage
}

// envelope is one page of a Partner API list response:
//
//	{"data": [...], "pagination": {"next_cursor": "...", "has_more": true}}
type envelope struct {
	Data       json.RawMessage `json:"data"`
	Pagination *struct {
		NextCursor *string `json:"next_cursor"`
		HasMore    bool    `json:"has_more"`
	} `json:"pagination"`
}

// FetchAll walks a cursor-paginated list endpoint and returns every item.
// path may already carry query parameters (filters, include); cursor and
// limit are added. A response without a `data` key is stored as one item, so
// single-object endpoints can be fetched the same way.
//
// When the API stops the walk with ErrTraversalCap, the items read so far are
// returned together with the (wrapped) error.
func FetchAll(ctx context.Context, client *Client, path string, limit int) (*Collection, error) {
	if limit <= 0 {
		limit = DefaultLimit
	}
	c := &Collection{Items: []json.RawMessage{}}
	seenCursor := map[string]bool{}
	itemSeen := map[string]bool{}
	cursor := ""

	for page := 1; ; page++ {
		if page > maxPages {
			return nil, fmt.Errorf("pagination safety limit reached (%d pages) for %s", maxPages, path)
		}

		// Build the query safely so reserved bytes in a cursor cannot corrupt
		// the request URL (issue #30).
		q := url.Values{}
		q.Set("limit", strconv.Itoa(limit))
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		body, err := client.Get(ctx, path+sep+q.Encode())
		if err != nil {
			var se *StatusError
			if page > 1 && errors.As(err, &se) && se.Code == 400 && se.Problem == "INVALID_CURSOR" {
				return c, fmt.Errorf("%s after %d items: %w", path, len(c.Items), ErrTraversalCap)
			}
			return nil, fmt.Errorf("page %d of %s: %w", page, path, err)
		}

		var env envelope
		if err := json.Unmarshal(body, &env); err != nil {
			return nil, fmt.Errorf("parse page %d of %s: %w", page, path, err)
		}
		if len(env.Data) == 0 {
			// Not a list envelope (e.g. /v1/whoami): keep the whole response.
			if page == 1 {
				c.Items = append(c.Items, json.RawMessage(bytes.TrimSpace(body)))
				return c, nil
			}
			return nil, fmt.Errorf("page %d of %s: response has no data field", page, path)
		}

		var items []json.RawMessage
		if err := json.Unmarshal(env.Data, &items); err != nil {
			// data is a single object, not a list: nothing to page through.
			c.Items = append(c.Items, env.Data)
			return c, nil
		}

		// A cursor that is ignored hands back the same items on every page.
		// Compare by item identity, not bytes.
		if page > 1 && len(items) > 0 && allSeen(items, itemSeen) {
			return nil, fmt.Errorf("%s: page %d only repeats items from earlier pages — the cursor seems to be ignored", path, page)
		}
		for _, it := range items {
			itemSeen[entityKey(it)] = true
		}
		c.Items = append(c.Items, items...)
		if len(c.Items) > maxItemsPerEndpoint {
			return nil, fmt.Errorf("item safety limit reached (%d items) for %s — possible runaway pagination", maxItemsPerEndpoint, path)
		}

		if env.Pagination == nil || !env.Pagination.HasMore {
			return c, nil
		}
		if env.Pagination.NextCursor == nil || *env.Pagination.NextCursor == "" {
			return nil, fmt.Errorf("%s: page %d reports more items but no next_cursor", path, page)
		}
		next := *env.Pagination.NextCursor
		if seenCursor[next] {
			return nil, fmt.Errorf("%s: page %d repeats an earlier cursor", path, page)
		}
		seenCursor[next] = true
		cursor = next
	}
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
// when it has none (memberships, for example, carry no id).
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

// ItemID returns the string `id` of an item, or "" when it has none.
func ItemID(e json.RawMessage) string {
	var v struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(e, &v) != nil {
		return ""
	}
	return v.ID
}
