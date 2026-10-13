package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/kAYd9iN/holaspirit-backup/internal/api"
)

// pagedServer serves `pages` pages; page(n) builds the response body for page n.
func pagedServer(t *testing.T, page func(n int) any) (*httptest.Server, *int32) {
	t.Helper()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		n, err := strconv.Atoi(r.URL.Query().Get("page"))
		if err != nil {
			n = 1
		}
		json.NewEncoder(w).Encode(page(n))
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func fetch(t *testing.T, srv *httptest.Server) (*api.Collection, error) {
	t.Helper()
	c := api.NewClient(srv.URL, "api:test")
	c.MaxRetries = 0
	return api.FetchAllPages(context.Background(), c, "/items")
}

func ids(t *testing.T, items []json.RawMessage) string {
	t.Helper()
	var out []string
	for _, it := range items {
		var v struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(it, &v); err != nil {
			t.Fatalf("item %s: %v", it, err)
		}
		out = append(out, v.ID)
	}
	return strings.Join(out, ",")
}

// The format documented in Holaspirit's published OpenAPI spec: a top-level
// `pagination` object with camelCase fields. The old code ignored it and
// stopped after page 1.
func TestFetchAllPages_SpecFormat_AllPages(t *testing.T) {
	srv, calls := pagedServer(t, func(n int) any {
		return map[string]any{
			"data": []map[string]string{{"id": fmt.Sprintf("a%d", n)}, {"id": fmt.Sprintf("b%d", n)}},
			"pagination": map[string]any{
				"totalItems": 6, "currentPage": n, "nextPage": n + 1, "countPerPage": 2, "pagesCount": 3,
			},
		}
	})
	col, err := fetch(t, srv)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(t, col.Items); got != "a1,b1,a2,b2,a3,b3" {
		t.Errorf("items = %s", got)
	}
	if *calls != 3 {
		t.Errorf("requests = %d, want 3", *calls)
	}
	if col.Reported != 6 || col.Missing() != 0 {
		t.Errorf("reported=%d missing=%d, want 6/0", col.Reported, col.Missing())
	}
}

// The format earlier versions of this tool assumed stays supported.
func TestFetchAllPages_LegacyMetaFormat(t *testing.T) {
	srv, _ := pagedServer(t, func(n int) any {
		return map[string]any{
			"data": []map[string]string{{"id": fmt.Sprintf("i%d", n)}},
			"meta": map[string]any{"pagination": map[string]any{"current_page": n, "total_pages": 3}},
		}
	})
	col, err := fetch(t, srv)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(t, col.Items); got != "i1,i2,i3" {
		t.Errorf("items = %s", got)
	}
}

// Documented format without pagesCount: follow nextPage until it is null.
func TestFetchAllPages_FollowsNextPage(t *testing.T) {
	srv, _ := pagedServer(t, func(n int) any {
		var next any = n + 1
		if n == 2 {
			next = nil
		}
		return map[string]any{
			"data":       []map[string]string{{"id": fmt.Sprintf("i%d", n)}},
			"pagination": map[string]any{"currentPage": n, "nextPage": next},
		}
	})
	col, err := fetch(t, srv)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(t, col.Items); got != "i1,i2" {
		t.Errorf("items = %s", got)
	}
}

// No pagination metadata at all: keep paging until an empty page.
func TestFetchAllPages_NoMetadata_StopsAtEmptyPage(t *testing.T) {
	srv, calls := pagedServer(t, func(n int) any {
		data := []map[string]string{}
		if n <= 2 {
			data = append(data, map[string]string{"id": fmt.Sprintf("i%d", n)})
		}
		return map[string]any{"data": data}
	})
	col, err := fetch(t, srv)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(t, col.Items); got != "i1,i2" {
		t.Errorf("items = %s", got)
	}
	if *calls != 3 {
		t.Errorf("requests = %d, want 3", *calls)
	}
}

// No metadata and the page parameter is ignored: the endpoint isn't paginated,
// page 1 holds everything, and the repeat must not be stored twice.
func TestFetchAllPages_NoMetadata_PageIgnored_NoDuplicates(t *testing.T) {
	srv, _ := pagedServer(t, func(n int) any {
		return map[string]any{"data": []map[string]string{{"id": "x"}, {"id": "y"}}}
	})
	col, err := fetch(t, srv)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(t, col.Items); got != "x,y" {
		t.Errorf("items = %s, want x,y", got)
	}
}

// Metadata promises more pages, but every page repeats page 1 (reordered):
// that must fail loudly instead of producing a silently wrong backup.
func TestFetchAllPages_PageIgnoredDespiteMetadata_Errors(t *testing.T) {
	srv, _ := pagedServer(t, func(n int) any {
		data := []map[string]string{{"id": "x"}, {"id": "y"}}
		if n%2 == 0 {
			data = []map[string]string{{"id": "y"}, {"id": "x"}}
		}
		return map[string]any{"data": data, "pagination": map[string]any{"pagesCount": 5, "totalItems": 10}}
	})
	if _, err := fetch(t, srv); err == nil || !strings.Contains(err.Error(), "page parameter seems to be ignored") {
		t.Fatalf("expected ignored-page error, got %v", err)
	}
}

// The API reports more items than it delivered.
func TestFetchAllPages_ReportsMissingItems(t *testing.T) {
	srv, _ := pagedServer(t, func(n int) any {
		return map[string]any{
			"data":       []map[string]string{{"id": "only"}},
			"pagination": map[string]any{"totalItems": 3, "pagesCount": 1},
		}
	})
	col, err := fetch(t, srv)
	if err != nil {
		t.Fatal(err)
	}
	if col.Missing() != 2 {
		t.Errorf("Missing() = %d, want 2", col.Missing())
	}
}

// linked entities are kept, per type, de-duplicated across pages.
func TestFetchAllPages_KeepsLinkedDeduplicated(t *testing.T) {
	srv, _ := pagedServer(t, func(n int) any {
		return map[string]any{
			"data": []map[string]any{{"id": fmt.Sprintf("r%d", n), "accountabilities": []string{"acc1"}}},
			"linked": map[string]any{
				"accountabilities": []map[string]string{
					{"id": "acc1", "description": "shared on both pages"},
					{"id": fmt.Sprintf("only%d", n), "description": "page-specific"},
				},
				"members": map[string]string{"id": "m1"}, // single object instead of a list
			},
			"pagination": map[string]any{"pagesCount": 2},
		}
	})
	col, err := fetch(t, srv)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(t, col.Linked["accountabilities"]); got != "acc1,only1,only2" {
		t.Errorf("linked accountabilities = %s", got)
	}
	if got := ids(t, col.Linked["members"]); got != "m1" {
		t.Errorf("linked members = %s", got)
	}
}

// PHP serializes empty objects as []; numbers may come as strings.
func TestFetchAllPages_TolerantDecoding(t *testing.T) {
	srv, _ := pagedServer(t, func(n int) any {
		return map[string]any{
			"data":       []map[string]string{{"id": fmt.Sprintf("i%d", n)}},
			"linked":     []any{},
			"meta":       []any{},
			"pagination": map[string]any{"totalItems": "2", "pagesCount": "2"},
		}
	})
	col, err := fetch(t, srv)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(t, col.Items); got != "i1,i2" {
		t.Errorf("items = %s", got)
	}
	if len(col.Linked) != 0 {
		t.Errorf("linked = %v, want empty", col.Linked)
	}
}

func TestFetchAllPages_SingleObjectData(t *testing.T) {
	srv, calls := pagedServer(t, func(n int) any {
		return map[string]any{"data": map[string]string{"id": "org"}}
	})
	col, err := fetch(t, srv)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(t, col.Items); got != "org" || *calls != 1 {
		t.Errorf("items = %s after %d requests", got, *calls)
	}
}

func TestFetchAllPages_EmptyResponse(t *testing.T) {
	srv, _ := pagedServer(t, func(n int) any {
		return map[string]any{"data": []any{}, "pagination": map[string]any{"totalItems": 0, "pagesCount": 0}}
	})
	col, err := fetch(t, srv)
	if err != nil {
		t.Fatal(err)
	}
	if len(col.Items) != 0 || col.Missing() != 0 {
		t.Errorf("items=%d missing=%d", len(col.Items), col.Missing())
	}
	// An empty collection must serialize as [] (not null) in the backup file.
	if b, _ := json.Marshal(col.Items); string(b) != "[]" {
		t.Errorf("empty items marshal to %s", b)
	}
}

func TestFetchAllPages_MissingDataFieldErrors(t *testing.T) {
	srv, _ := pagedServer(t, func(n int) any { return map[string]any{"error": "nope"} })
	if _, err := fetch(t, srv); err == nil {
		t.Fatal("expected an error for a response without data")
	}
}

// The documented page-size parameter is `count` (not `per_page`).
func TestFetchAllPages_EncodesQueryParams(t *testing.T) {
	var gotQueries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQueries = append(gotQueries, r.URL.RawQuery)
		json.NewEncoder(w).Encode(map[string]any{
			"data":       []map[string]any{{"id": "1"}},
			"pagination": map[string]any{"pagesCount": 1},
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
	// url.Values.Encode sorts keys alphabetically: count before page.
	if gotQueries[0] != "count=100&page=1" {
		t.Errorf("unexpected encoded query: %q", gotQueries[0])
	}
}
