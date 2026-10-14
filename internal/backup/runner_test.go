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

func newRunnerClient(srv *httptest.Server) *api.Client {
	c := api.NewClient(srv.URL, "api:test")
	c.MaxRetries = 0
	return c
}

// Live run: translations answered 404 for an organization without that
// feature. An optional endpoint is then "unavailable", not a failure — but a
// regular endpoint with the same answer still fails.
func TestRunner_OptionalEndpointNotFoundIsUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	w, _ := storage.NewWriter(t.TempDir(), time.Now())
	res := RunFetchers(context.Background(), newRunnerClient(srv), w, []api.Endpoint{
		{Name: "translations", Path: "/api/organizations/o/translations", Paginated: true, Optional: "feature off"},
		{Name: "circles", Path: "/api/organizations/o/circles", Paginated: true},
	})
	if res[0].Unavailable == nil || res[0].Err != nil {
		t.Errorf("optional 404: %+v", res[0])
	}
	if res[1].Err == nil || res[1].Unavailable != nil {
		t.Errorf("regular 404 must fail: %+v", res[1])
	}
}

// Live run: chartviews answered 400 to the paging parameters. Retry as a
// plain request.
func TestRunner_PagingRejectedFallsBackToPlainRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") != "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Write([]byte(`{"data":[{"id":"v1"}]}`))
	}))
	defer srv.Close()
	w, _ := storage.NewWriter(t.TempDir(), time.Now())
	res := RunFetchers(context.Background(), newRunnerClient(srv), w,
		[]api.Endpoint{{Name: "chartviews", Path: "/api/organizations/o/chartviews", Paginated: true}})[0]
	if res.Err != nil || len(res.Files) != 1 {
		t.Fatalf("fallback failed: %+v", res)
	}
	data, _ := os.ReadFile(filepath.Join(w.Dir(), "chartviews.json"))
	if !strings.Contains(string(data), `"v1"`) {
		t.Errorf("chartviews.json = %s", data)
	}
}

// Live run: tensions stopped at exactly 10,000 with totalItems capped too.
// Hitting the cap must never pass as complete.
func TestRunner_ResultCapMarksIncomplete(t *testing.T) {
	old := api.ResultCap
	api.ResultCap = 3
	defer func() { api.ResultCap = old }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Ignores any date filter, always the capped first three items.
		w.Write([]byte(`{"data":[{"id":"a"},{"id":"b"},{"id":"c"}],"pagination":{"totalItems":3,"pagesCount":1}}`))
	}))
	defer srv.Close()
	w, _ := storage.NewWriter(t.TempDir(), time.Now())
	res := RunFetchers(context.Background(), newRunnerClient(srv), w, []api.Endpoint{
		{Name: "meetings", Path: "/api/organizations/o/meetings", Paginated: true},
		{Name: "tensions", Path: "/api/organizations/o/tensions", Paginated: true, ByCreated: true},
	})
	if res[0].Incomplete == nil || !strings.Contains(res[0].Incomplete.Error(), "API limit") {
		t.Errorf("plain endpoint at the cap: %+v", res[0])
	}
	// ByCreated, but the date filter does not work here: falls back, and says so.
	if res[1].Err != nil || res[1].Incomplete == nil || !strings.Contains(res[1].Incomplete.Error(), "time-window paging not possible") {
		t.Errorf("tensions without a working date filter: %+v", res[1])
	}
}
