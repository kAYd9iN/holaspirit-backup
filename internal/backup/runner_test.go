package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kAYd9iN/holaspirit-backup/internal/api"
	"github.com/kAYd9iN/holaspirit-backup/internal/storage"
)

func TestRunner_FetchesAllEndpoints(t *testing.T) {
	fetched := make(map[string]bool)

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
	client.RetryDelay = 1 * time.Millisecond

	tmpDir := t.TempDir()
	w, _ := storage.NewWriter(tmpDir, time.Now())

	endpoints := []api.Endpoint{
		{Name: "circles", Path: "/api/organizations/org1/circles", Paginated: true},
		{Name: "roles", Path: "/api/organizations/org1/roles", Paginated: true},
	}

	results := RunFetchers(context.Background(), client, w, endpoints)

	for _, r := range results {
		fetched[r.Name] = r.Err == nil
	}

	if !fetched["circles"] || !fetched["roles"] {
		t.Errorf("not all endpoints fetched: %v", fetched)
	}

	if _, err := os.Stat(filepath.Join(w.Dir(), "circles.json")); os.IsNotExist(err) {
		t.Error("circles.json not created")
	}
}

// A paginated endpoint writes <name>.json plus <name>-linked.json, and a
// shortfall against the API's reported total is surfaced, not swallowed.
func TestRunner_WritesLinkedAndFlagsIncomplete(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"data":       []map[string]any{{"id": "r1", "accountabilities": []string{"a1"}}},
			"linked":     map[string]any{"accountabilities": []map[string]string{{"id": "a1", "description": "text"}}},
			"pagination": map[string]any{"totalItems": 2, "pagesCount": 1},
		})
	}))
	defer srv.Close()

	client := api.NewClient(srv.URL, "api:test")
	client.MaxRetries = 0
	w, err := storage.NewWriter(t.TempDir(), time.Now())
	if err != nil {
		t.Fatal(err)
	}

	res := RunFetchers(context.Background(), client, w,
		[]api.Endpoint{{Name: "roles", Path: "/api/organizations/org1/roles", Paginated: true}})[0]

	if res.Err != nil {
		t.Fatalf("unexpected error: %v", res.Err)
	}
	if got := fmt.Sprint(res.Files); got != "[roles.json roles-linked.json]" {
		t.Errorf("files = %s", got)
	}
	if res.Incomplete == nil {
		t.Error("expected Incomplete: API reported 2 items, 1 was delivered")
	}
	linked, err := os.ReadFile(filepath.Join(w.Dir(), "roles-linked.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(linked), `"description": "text"`) {
		t.Errorf("roles-linked.json lacks the linked accountability: %s", linked)
	}
}

func TestRunFetchersMaxConcurrency(t *testing.T) {
	var mu sync.Mutex
	var peak, current int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		current++
		if current > peak {
			peak = current
		}
		mu.Unlock()

		time.Sleep(20 * time.Millisecond)

		mu.Lock()
		current--
		mu.Unlock()

		w.Write([]byte(`{"data":[],"meta":{}}`))
	}))
	defer srv.Close()

	// 20 endpoints to saturate the pool
	endpoints := make([]api.Endpoint, 20)
	for i := range endpoints {
		endpoints[i] = api.Endpoint{
			Name:      fmt.Sprintf("ep%d", i),
			Path:      "/api/test",
			Paginated: false,
		}
	}

	client := api.NewClient(srv.URL, "tok")
	client.MaxRetries = 0
	client.RetryDelay = 0

	dir := t.TempDir()
	w, err := storage.NewWriter(dir, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	RunFetchers(context.Background(), client, w, endpoints)

	if peak > workerCount {
		t.Errorf("peak concurrency %d exceeded workerCount %d", peak, workerCount)
	}
	if peak == 0 {
		t.Error("no requests were made")
	}
}
