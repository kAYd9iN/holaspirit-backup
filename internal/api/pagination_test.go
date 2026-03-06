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
