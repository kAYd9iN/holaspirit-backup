package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kAYd9iN/holaspirit-backup/internal/api"
)

func TestFetchAllPages_SinglePage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"data": []map[string]string{{"id": "item1"}, {"id": "item2"}},
			"meta": map[string]interface{}{
				"pagination": map[string]interface{}{
					"current_page": 1,
					"total_pages":  1,
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := api.NewClient(srv.URL, "api:test")
	items, err := api.FetchAllPages(context.Background(), client, "/items")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 2 {
		t.Errorf("expected 2 items, got %d", len(items))
	}
}

func TestFetchAllPages_MultiplePages(t *testing.T) {
	page := 1
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		currentPage := page
		page++
		resp := map[string]interface{}{
			"data": []map[string]string{{"id": fmt.Sprintf("item%d", currentPage)}},
			"meta": map[string]interface{}{
				"pagination": map[string]interface{}{
					"current_page": currentPage,
					"total_pages":  3,
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := api.NewClient(srv.URL, "api:test")
	items, err := api.FetchAllPages(context.Background(), client, "/items")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 3 {
		t.Errorf("expected 3 items, got %d", len(items))
	}
}

func TestFetchAllPages_EmptyResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"data": []interface{}{},
			"meta": map[string]interface{}{
				"pagination": map[string]interface{}{
					"current_page": 1,
					"total_pages":  1,
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := api.NewClient(srv.URL, "api:test")
	items, err := api.FetchAllPages(context.Background(), client, "/items")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected 0 items, got %d", len(items))
	}
}

func TestFetchAllPages_EncodesQueryParams(t *testing.T) {
	var gotQueries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQueries = append(gotQueries, r.URL.RawQuery)
		json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"id": "1"}},
			"meta": map[string]any{"pagination": map[string]any{"current_page": 1, "total_pages": 1}},
		})
	}))
	defer srv.Close()

	c := api.NewClient(srv.URL, "tok")
	if _, err := api.FetchAllPages(context.Background(), c, "/circles"); err != nil {
		t.Fatal(err)
	}
	if len(gotQueries) != 1 {
		t.Fatalf("expected 1 request, got %d", len(gotQueries))
	}
	// url.Values.Encode sorts keys alphabetically: page before per_page.
	if gotQueries[0] != "page=1&per_page=100" {
		t.Errorf("unexpected encoded query: %q", gotQueries[0])
	}
}
