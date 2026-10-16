package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kAYd9iN/holaspirit-backup/internal/api"
)

func TestClientGet_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer api:test" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()

	client := api.NewClient(srv.URL, "api:test")
	body, err := client.Get(context.Background(), "/test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(body) != `{"data":[]}` {
		t.Errorf("got %q", string(body))
	}
}

func TestClientGet_Retries_On_500(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if calls.Load() < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()

	client := api.NewClient(srv.URL, "api:test")
	client.RetryDelay = 1 * time.Millisecond
	_, err := client.Get(context.Background(), "/test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls.Load() != 3 {
		t.Errorf("expected 3 calls, got %d", calls.Load())
	}
}

func TestClientGet_Fails_After_MaxRetries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := api.NewClient(srv.URL, "api:test")
	client.RetryDelay = 1 * time.Millisecond
	_, err := client.Get(context.Background(), "/test")
	if err == nil {
		t.Fatal("expected error after max retries")
	}
}

func TestClientDoesNotRetryOn4xx(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := api.NewClient(srv.URL, "test-token")
	c.MaxRetries = 3
	c.RetryDelay = 0

	_, err := c.Get(context.Background(), "/api/test")
	if err == nil {
		t.Fatal("expected error")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("expected 1 call (no retry on 404), got %d", got)
	}
}

func TestClientRetriesOn5xx(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := api.NewClient(srv.URL, "test-token")
	c.MaxRetries = 2
	c.RetryDelay = 0

	_, err := c.Get(context.Background(), "/api/test")
	if err == nil {
		t.Fatal("expected error")
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("expected 3 calls (1 initial + 2 retries), got %d", got)
	}
}

func TestClientRetriesOn429(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if calls.Load() <= 4 {
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()

	var sleeps []time.Duration
	c := api.RecordSleeps(api.NewClient(srv.URL, "test-token"), &sleeps)
	c.MaxRetries = 0 // throttling has its own, larger budget

	if _, err := c.Get(context.Background(), "/api/test"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := calls.Load(); got != 5 {
		t.Fatalf("expected 5 calls, got %d", got)
	}
	for _, d := range sleeps {
		if d != 7*time.Second {
			t.Errorf("slept %v, want the Retry-After of 7s", d)
		}
	}
}

func TestClientGivesUpOnEndless429(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	var sleeps []time.Duration
	c := api.RecordSleeps(api.NewClient(srv.URL, "test-token"), &sleeps)
	if _, err := c.Get(context.Background(), "/api/test"); err == nil {
		t.Fatal("expected an error")
	}
}

// Before the window runs dry, the client waits for X-RateLimit-Reset.
func TestClientPacesOnLowRemaining(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "1")
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(30*time.Second).Unix(), 10))
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	var sleeps []time.Duration
	c := api.RecordSleeps(api.NewClient(srv.URL, "k"), &sleeps)
	if _, err := c.Get(context.Background(), "/x"); err != nil {
		t.Fatal(err)
	}
	if len(sleeps) != 1 || sleeps[0] < 25*time.Second || sleeps[0] > 31*time.Second {
		t.Errorf("sleeps = %v, want one ~30s pause", sleeps)
	}
}

// Redirects are never followed: they could carry the Bearer token elsewhere.
func TestClientDoesNotFollowRedirects(t *testing.T) {
	var other atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { other.Store(true) }))
	defer target.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer srv.Close()
	c := api.Unthrottled(api.NewClient(srv.URL, "k"))
	if _, err := c.Get(context.Background(), "/x"); err == nil {
		t.Error("expected an error for a redirect")
	}
	if other.Load() {
		t.Error("redirect was followed")
	}
}

func TestClientOnlyMakesGETRequests(t *testing.T) {
	var nonGetSeen atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			nonGetSeen.Store(true)
		}
		w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()

	c := api.NewClient(srv.URL, "test-token")
	c.MaxRetries = 0
	for i := 0; i < 5; i++ {
		c.Get(context.Background(), "/api/test")
	}
	if nonGetSeen.Load() {
		t.Error("non-GET request was made by client")
	}
}
