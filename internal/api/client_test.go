package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
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
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusNotFound) // 404
	}))
	defer srv.Close()

	c := api.NewClient(srv.URL, "test-token")
	c.MaxRetries = 3
	c.RetryDelay = 0

	_, err := c.Get(context.Background(), "/api/test")
	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Fatalf("expected 1 call (no retry on 404), got %d", calls)
	}
}

func TestClientRetriesOn5xx(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
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
	if calls != 3 { // 1 initial + 2 retries
		t.Fatalf("expected 3 calls, got %d", calls)
	}
}

func TestClientOnlyMakesGETRequests(t *testing.T) {
	nonGetSeen := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			nonGetSeen = true
		}
		w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()

	c := api.NewClient(srv.URL, "test-token")
	c.MaxRetries = 0
	for i := 0; i < 5; i++ {
		c.Get(context.Background(), "/api/test")
	}
	if nonGetSeen {
		t.Error("non-GET request was made by client")
	}
}
