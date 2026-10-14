package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kAYd9iN/holaspirit-backup/internal/api"
)

// capServer imitates a Holaspirit list endpoint backed by a search index:
// at most `limit` items per query (and totalItems capped the same way),
// createdAfter/createdBefore as inclusive YYYY-MM-DD dates unless disabled.
type capServer struct {
	limit        int
	items        []time.Time // createdAt of item i
	ignoreFilter bool        // pretend the date filters do not exist
	ignoreAfter  bool        // apply createdBefore only (a "wrong" filter)
}

func (s *capServer) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		var after, before time.Time
		if v := q.Get("createdAfter"); v != "" && !s.ignoreFilter && !s.ignoreAfter {
			after, _ = time.Parse("2006-01-02", v)
		}
		if v := q.Get("createdBefore"); v != "" && !s.ignoreFilter {
			if before, _ = time.Parse("2006-01-02", v); before.IsZero() {
				// Not YYYY-MM-DD (e.g. the RFC 3339 probe): reject like an API would.
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			before = before.Add(24*time.Hour - time.Nanosecond) // inclusive day
		}
		var match []map[string]any
		for i, c := range s.items {
			if (!after.IsZero() && c.Before(after)) || (!before.IsZero() && c.After(before)) {
				continue
			}
			match = append(match, map[string]any{"id": fmt.Sprintf("t%03d", i), "createdAt": c.Format(time.RFC3339)})
		}
		total := len(match)
		if total > s.limit {
			total = s.limit // the index caps the reported total as well
			match = match[:s.limit]
		}
		page, _ := strconv.Atoi(q.Get("page"))
		count, _ := strconv.Atoi(q.Get("count"))
		if page < 1 {
			page = 1
		}
		if count < 1 {
			count = 100
		}
		from, to := (page-1)*count, page*count
		if from > len(match) {
			from = len(match)
		}
		if to > len(match) {
			to = len(match)
		}
		pages := (total + count - 1) / count
		json.NewEncoder(w).Encode(map[string]any{
			"data":       match[from:to],
			"pagination": map[string]any{"totalItems": total, "currentPage": page, "pagesCount": pages},
		})
	})
}

func withCap(t *testing.T, n int) {
	t.Helper()
	old := api.ResultCap
	api.ResultCap = n
	t.Cleanup(func() { api.ResultCap = old })
}

func days(start time.Time, n, perDay int) []time.Time {
	var out []time.Time
	for i := 0; i < n; i++ {
		out = append(out, start.Add(time.Duration(i/perDay)*24*time.Hour+time.Duration(i%perDay)*time.Minute))
	}
	return out
}

var now = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

func TestFetchByCreatedWindows_GetsPastTheCap(t *testing.T) {
	withCap(t, 10)
	s := &capServer{limit: 10, items: days(time.Date(2019, 3, 1, 8, 0, 0, 0, time.UTC), 37, 2)}
	srv := httptest.NewServer(s.handler(t))
	defer srv.Close()

	col, err := api.FetchByCreatedWindows(context.Background(), newClient(srv), "/api/organizations/o/tensions", now)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, it := range col.Items {
		var v struct {
			ID string `json:"id"`
		}
		json.Unmarshal(it, &v)
		if seen[v.ID] {
			t.Errorf("duplicate %s", v.ID)
		}
		seen[v.ID] = true
	}
	if len(seen) != 37 {
		t.Errorf("got %d distinct items, want all 37 (a plain query stops at 10)", len(seen))
	}
}

func TestFetchByCreatedWindows_FilterIgnored(t *testing.T) {
	withCap(t, 10)
	s := &capServer{limit: 10, items: days(time.Date(2019, 3, 1, 8, 0, 0, 0, time.UTC), 30, 1), ignoreFilter: true}
	srv := httptest.NewServer(s.handler(t))
	defer srv.Close()

	if _, err := api.FetchByCreatedWindows(context.Background(), newClient(srv), "/x", now); err != api.ErrNoDateFilter {
		t.Fatalf("expected ErrNoDateFilter, got %v", err)
	}
}

func TestFetchByCreatedWindows_TooManyOnOneDay(t *testing.T) {
	withCap(t, 10)
	s := &capServer{limit: 10, items: days(time.Date(2019, 3, 1, 8, 0, 0, 0, time.UTC), 15, 100)} // all on one day
	srv := httptest.NewServer(s.handler(t))
	defer srv.Close()

	_, err := api.FetchByCreatedWindows(context.Background(), newClient(srv), "/x", now)
	if err == nil || !strings.Contains(err.Error(), "cannot be split further") {
		t.Fatalf("expected a cannot-split error, got %v", err)
	}
}

// A filter that does not behave as assumed must not yield a silently wrong result.
func TestFetchByCreatedWindows_WrongFilterIsDetected(t *testing.T) {
	withCap(t, 10)
	s := &capServer{limit: 10, items: days(time.Date(2019, 3, 1, 8, 0, 0, 0, time.UTC), 30, 1), ignoreAfter: true}
	srv := httptest.NewServer(s.handler(t))
	defer srv.Close()

	_, err := api.FetchByCreatedWindows(context.Background(), newClient(srv), "/x", now)
	if err == nil || !strings.Contains(err.Error(), "outside the window") {
		t.Fatalf("expected an outside-the-window error, got %v", err)
	}
}

func newClient(srv *httptest.Server) *api.Client {
	c := api.NewClient(srv.URL, "api:test")
	c.MaxRetries = 0
	return api.Unthrottled(c)
}
