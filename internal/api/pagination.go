package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
)

// HolaspiritResponse is the standard paginated API response envelope.
type HolaspiritResponse struct {
	Data json.RawMessage `json:"data"`
	Meta struct {
		Pagination struct {
			CurrentPage int `json:"current_page"`
			TotalPages  int `json:"total_pages"`
		} `json:"pagination"`
	} `json:"meta"`
}

const (
	// maxPages is a hard cap on the number of pages fetched per endpoint,
	// preventing unbounded loops if the API returns inconsistent pagination
	// metadata.
	maxPages = 500

	// maxItemsPerEndpoint bounds the total number of accumulated items per
	// endpoint (issue #19). FetchAllPages holds all items in memory before
	// writing, so without this cap a misbehaving or hostile API could drive
	// memory use unbounded (500 pages × 100 MiB). 1,000,000 items is far above
	// any realistic Holaspirit org while keeping worst-case memory bounded.
	maxItemsPerEndpoint = 1_000_000

	// perPage is the page size requested from the API.
	perPage = 100
)

// FetchAllPages retrieves all pages for a given endpoint and returns
// the combined data items as raw JSON messages.
func FetchAllPages(ctx context.Context, client *Client, path string) ([]json.RawMessage, error) {
	var allItems []json.RawMessage
	page := 1

	for {
		if page > maxPages {
			return nil, fmt.Errorf("pagination safety limit reached (%d pages) for %s", maxPages, path)
		}

		// Build the query safely so a path with existing query characters or
		// reserved bytes cannot corrupt the request URL (issue #30).
		q := url.Values{}
		q.Set("page", strconv.Itoa(page))
		q.Set("per_page", strconv.Itoa(perPage))
		reqPath := path + "?" + q.Encode()
		body, err := client.Get(ctx, reqPath)
		if err != nil {
			return nil, fmt.Errorf("page %d of %s: %w", page, path, err)
		}

		var resp HolaspiritResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("parse page %d of %s: %w", page, path, err)
		}

		var items []json.RawMessage
		if err := json.Unmarshal(resp.Data, &items); err != nil {
			// data is a single object, not an array — wrap it
			allItems = append(allItems, resp.Data)
			break
		}
		allItems = append(allItems, items...)

		if len(allItems) > maxItemsPerEndpoint {
			return nil, fmt.Errorf("item safety limit reached (%d items) for %s — possible runaway pagination", maxItemsPerEndpoint, path)
		}

		if page >= resp.Meta.Pagination.TotalPages || resp.Meta.Pagination.TotalPages == 0 {
			break
		}
		page++
	}

	return allItems, nil
}
