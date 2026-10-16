package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/kAYd9iN/holaspirit-backup/internal/api"
)

// pagedServer serves `total` items with ids i0..i(n-1), `limit` per page,
// using the documented {data, pagination{next_cursor, has_more}} envelope.
func pagedServer(total int, seen *[]string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if seen != nil {
			*seen = append(*seen, r.URL.RawQuery)
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		start, _ := strconv.Atoi(r.URL.Query().Get("cursor"))
		var data []map[string]string
		for i := start; i < total && i < start+limit; i++ {
			data = append(data, map[string]string{"id": fmt.Sprintf("i%d", i)})
		}
		next := start + limit
		var cursor any
		if next < total {
			cursor = strconv.Itoa(next)
		}
		json.NewEncoder(w).Encode(map[string]any{"data": data,
			"pagination": map[string]any{"next_cursor": cursor, "has_more": next < total}})
	}))
}

func TestFetchAll_FollowsCursorUntilHasMoreFalse(t *testing.T) {
	var seen []string
	srv := pagedServer(7, &seen)
	defer srv.Close()

	col, err := api.FetchAll(context.Background(), api.Unthrottled(api.NewClient(srv.URL, "k")), "/v1/roles?include=members", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(col.Items) != 7 {
		t.Fatalf("got %d items, want 7", len(col.Items))
	}
	if len(seen) != 3 {
		t.Fatalf("requests = %v, want 3 pages", seen)
	}
	// existing query kept, limit + cursor added
	if !strings.Contains(seen[0], "include=members") || !strings.Contains(seen[0], "limit=3") || strings.Contains(seen[0], "cursor") {
		t.Errorf("first request query = %q", seen[0])
	}
	if !strings.Contains(seen[1], "cursor=3") {
		t.Errorf("second request query = %q", seen[1])
	}
}

func TestFetchAll_SingleObjectResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"tenant_id":"t1","scopes":["roles:read"]}`))
	}))
	defer srv.Close()
	col, err := api.FetchAll(context.Background(), api.Unthrottled(api.NewClient(srv.URL, "k")), "/v1/whoami", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(col.Items) != 1 || !strings.Contains(string(col.Items[0]), "t1") {
		t.Errorf("items = %s", col.Items)
	}
}

func TestFetchAll_RepeatedCursorFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"data":[{"id":"x%s"}],"pagination":{"next_cursor":"same","has_more":true}}`, r.URL.Query().Get("cursor"))
	}))
	defer srv.Close()
	_, err := api.FetchAll(context.Background(), api.Unthrottled(api.NewClient(srv.URL, "k")), "/v1/x", 1)
	if err == nil || !strings.Contains(err.Error(), "repeats an earlier cursor") {
		t.Fatalf("err = %v", err)
	}
}

func TestFetchAll_IgnoredCursorFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"data":[{"id":"a"}],"pagination":{"next_cursor":%q,"has_more":true}}`, r.URL.Query().Get("cursor")+"x")
	}))
	defer srv.Close()
	_, err := api.FetchAll(context.Background(), api.Unthrottled(api.NewClient(srv.URL, "k")), "/v1/x", 1)
	if err == nil || !strings.Contains(err.Error(), "cursor seems to be ignored") {
		t.Fatalf("err = %v", err)
	}
}

func TestFetchAll_HasMoreWithoutCursorFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[{"id":"a"}],"pagination":{"next_cursor":null,"has_more":true}}`))
	}))
	defer srv.Close()
	_, err := api.FetchAll(context.Background(), api.Unthrottled(api.NewClient(srv.URL, "k")), "/v1/x", 1)
	if err == nil {
		t.Fatal("expected an error")
	}
}

// Sorted lists stop at 10,000 rows with 400 INVALID_CURSOR: keep what was
// read and report the cap instead of failing.
func TestFetchAll_TraversalCapKeepsItems(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("cursor") != "" {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"type":"about:blank","title":"Bad Request","status":400,"code":"INVALID_CURSOR","detail":"cursor beyond traversal cap"}`))
			return
		}
		w.Write([]byte(`{"data":[{"id":"a"},{"id":"b"}],"pagination":{"next_cursor":"c1","has_more":true}}`))
	}))
	defer srv.Close()
	col, err := api.FetchAll(context.Background(), api.Unthrottled(api.NewClient(srv.URL, "k")), "/v1/tensions", 2)
	if !errors.Is(err, api.ErrTraversalCap) {
		t.Fatalf("err = %v, want ErrTraversalCap", err)
	}
	if col == nil || len(col.Items) != 2 {
		t.Fatalf("items kept = %v", col)
	}
}

func TestFetchAll_OtherErrorsFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"detail":{"code":"FORBIDDEN_SCOPE","message":"missing roles:read"}}`))
	}))
	defer srv.Close()
	col, err := api.FetchAll(context.Background(), api.Unthrottled(api.NewClient(srv.URL, "k")), "/v1/roles", 0)
	var se *api.StatusError
	if col != nil || !errors.As(err, &se) || se.Code != 403 || se.Problem != "FORBIDDEN_SCOPE" {
		t.Fatalf("col=%v err=%v", col, err)
	}
}

// Nothing but a well-formed code from an error body reaches an error message.
func TestStatusError_IgnoresMalformedProblemCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"code":"bad\ncode <script>"}`))
	}))
	defer srv.Close()
	_, err := api.Unthrottled(api.NewClient(srv.URL, "k")).Get(context.Background(), "/x")
	var se *api.StatusError
	if !errors.As(err, &se) || se.Problem != "" {
		t.Fatalf("err = %v", err)
	}
}
