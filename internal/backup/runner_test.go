package backup_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kAYd9iN/holaspirit-backup/internal/api"
	"github.com/kAYd9iN/holaspirit-backup/internal/backup"
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

	results := backup.RunFetchers(context.Background(), client, w, endpoints)

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
