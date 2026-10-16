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

func newRunnerClient(srv *httptest.Server) *api.Client {
	c := api.NewClient(srv.URL, "api:test")
	c.MaxRetries = 0
	c.SetPace(0, 0)
	return c
}

func page(w http.ResponseWriter, items ...map[string]any) {
	if items == nil {
		items = []map[string]any{}
	}
	json.NewEncoder(w).Encode(map[string]any{"data": items, "pagination": map[string]any{"next_cursor": nil, "has_more": false}})
}

func TestRunner_FetchesAllEndpoints(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page(w, map[string]any{"id": "x"})
	}))
	defer srv.Close()

	w, _ := storage.NewWriter(t.TempDir(), time.Now())
	results := RunFetchers(context.Background(), newRunnerClient(srv), w, []api.Endpoint{
		{Name: "circles", Path: "/v1/circles"},
		{Name: "roles", Path: "/v1/roles"},
	}, Options{})
	for _, r := range results {
		if r.Err != nil || r.Records != 1 {
			t.Errorf("%s: %+v", r.Name, r)
		}
	}
	if _, err := os.Stat(filepath.Join(w.Dir(), "circles.json")); err != nil {
		t.Error("circles.json not created")
	}
}

// Sub-resources are fetched per parent and written as [{parent_id, data}];
// OnlyIf skips parents without children, Recursive follows sub-tasks down.
func TestRunner_ChildrenRecursiveAndOnlyIf(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path+"?"+r.URL.Query().Get("parent_task_id"))
		mu.Unlock()
		switch r.URL.Query().Get("parent_task_id") {
		case "":
			page(w, map[string]any{"id": "t1", "subtask_count": 1}, map[string]any{"id": "t2", "subtask_count": 0})
		case "t1":
			page(w, map[string]any{"id": "s1", "subtask_count": 1})
		case "s1":
			page(w, map[string]any{"id": "s2", "subtask_count": 0})
		default:
			t.Errorf("unexpected request for %s", r.URL.RawQuery)
			page(w)
		}
	}))
	defer srv.Close()
	w, _ := storage.NewWriter(t.TempDir(), time.Now())
	res := RunFetchers(context.Background(), newRunnerClient(srv), w, []api.Endpoint{{
		Name: "tasks", Path: "/v1/tasks?archived_status=all",
		Children: []api.Child{{Name: "subtasks", Path: "/v1/tasks?archived_status=all&parent_task_id=%s", OnlyIf: "subtask_count", Recursive: true}},
	}}, Options{})[0]
	if res.Err != nil || res.Incomplete != nil {
		t.Fatalf("%+v", res)
	}
	if res.Children["subtasks"] != 2 {
		t.Errorf("subtasks = %d, want 2 (s1, s2)", res.Children["subtasks"])
	}
	var groups []ChildGroup
	data, _ := os.ReadFile(filepath.Join(w.Dir(), "subtasks.json"))
	if err := json.Unmarshal(data, &groups); err != nil || len(groups) != 2 || groups[0].ParentID != "t1" || groups[1].ParentID != "s1" {
		t.Errorf("subtasks.json = %s", data)
	}
}

func TestRunner_QuickSkipsDetailChildren(t *testing.T) {
	var calls int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		page(w, map[string]any{"id": "m1"})
	}))
	defer srv.Close()
	w, _ := storage.NewWriter(t.TempDir(), time.Now())
	res := RunFetchers(context.Background(), newRunnerClient(srv), w, []api.Endpoint{{
		Name: "meetings", Path: "/v1/meetings",
		Children: []api.Child{{Name: "meeting-agenda-items", Path: "/v1/meetings/%s/agenda-items", Detail: true}},
	}}, Options{Quick: true})[0]
	if calls != 1 || len(res.Files) != 1 {
		t.Errorf("calls=%d files=%v, want only the meeting list", calls, res.Files)
	}
}

// A failing sub-resource request keeps the rest and marks the endpoint incomplete.
func TestRunner_ChildFailureMarksIncomplete(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/goals":
			page(w, map[string]any{"id": "g1"}, map[string]any{"id": "g2"})
		case "/v1/goals/g1/key-results":
			page(w, map[string]any{"id": "k1"})
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()
	w, _ := storage.NewWriter(t.TempDir(), time.Now())
	res := RunFetchers(context.Background(), newRunnerClient(srv), w, []api.Endpoint{{
		Name: "goals", Path: "/v1/goals",
		Children: []api.Child{{Name: "goal-key-results", Path: "/v1/goals/%s/key-results"}},
	}}, Options{})[0]
	if res.Err != nil || res.Incomplete == nil || !strings.Contains(res.Incomplete.Error(), "g2") {
		t.Fatalf("%+v", res)
	}
	if res.Children["goal-key-results"] != 1 {
		t.Errorf("children = %v", res.Children)
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
		page(w)
	}))
	defer srv.Close()

	endpoints := make([]api.Endpoint, 12)
	for i := range endpoints {
		endpoints[i] = api.Endpoint{Name: fmt.Sprintf("ep%d", i), Path: "/v1/test"}
	}
	w, err := storage.NewWriter(t.TempDir(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	RunFetchers(context.Background(), newRunnerClient(srv), w, endpoints, Options{})

	if peak > workerCount {
		t.Errorf("peak concurrency %d exceeded workerCount %d", peak, workerCount)
	}
	if peak == 0 {
		t.Error("no requests were made")
	}
}

// Admin-only data refused for this key is "unavailable", not a failure — but
// a regular endpoint with the same answer still fails, naming the scope.
func TestRunner_OptionalEndpointRefusedIsUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"code":"FORBIDDEN_SCOPE"}`))
	}))
	defer srv.Close()
	w, _ := storage.NewWriter(t.TempDir(), time.Now())
	res := RunFetchers(context.Background(), newRunnerClient(srv), w, []api.Endpoint{
		{Name: "audit-logs", Path: "/v1/audit-logs", Scope: "audit_log:read", Optional: "admin only"},
		{Name: "circles", Path: "/v1/circles", Scope: "circles:read"},
	}, Options{})
	if res[0].Unavailable == nil || res[0].Err != nil {
		t.Errorf("optional 403: %+v", res[0])
	}
	if res[1].Err == nil || !strings.Contains(res[1].Err.Error(), "circles:read") {
		t.Errorf("regular 403 must fail and name the scope: %+v", res[1])
	}
}

// The traversal cap keeps the items read and marks the endpoint incomplete.
func TestRunner_TraversalCapMarksIncomplete(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("cursor") != "" {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"code":"INVALID_CURSOR"}`))
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"id": "a"}},
			"pagination": map[string]any{"next_cursor": "c", "has_more": true}})
	}))
	defer srv.Close()
	w, _ := storage.NewWriter(t.TempDir(), time.Now())
	res := RunFetchers(context.Background(), newRunnerClient(srv), w,
		[]api.Endpoint{{Name: "tensions", Path: "/v1/tensions"}}, Options{})[0]
	if res.Err != nil || res.Incomplete == nil || res.Records != 1 {
		t.Fatalf("%+v", res)
	}
}
