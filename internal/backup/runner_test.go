package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
