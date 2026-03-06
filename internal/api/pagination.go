package api

import (
	"context"
	"encoding/json"
	"fmt"
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

// FetchAllPages retrieves all pages for a given endpoint and returns
// the combined data items as raw JSON messages.
func FetchAllPages(ctx context.Context, client *Client, path string) ([]json.RawMessage, error) {
	var allItems []json.RawMessage
	page := 1

	for {
		url := fmt.Sprintf("%s?page=%d&per_page=100", path, page)
		body, err := client.Get(ctx, url)
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

		if page >= resp.Meta.Pagination.TotalPages || resp.Meta.Pagination.TotalPages == 0 {
			break
		}
		page++
	}

	return allItems, nil
}
