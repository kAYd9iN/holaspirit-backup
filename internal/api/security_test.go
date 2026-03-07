package api

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testSecretToken = "api:super-secret-token-do-not-leak"

func TestTokenNotInErrorMessages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, testSecretToken)
	c.MaxRetries = 0
	c.RetryDelay = 0

	_, err := c.Get(context.Background(), "/api/test")
	if err == nil {
		t.Fatal("expected error from 500 response")
	}

	if strings.Contains(err.Error(), testSecretToken) {
		t.Errorf("token leaked in error message: %v", err)
	}
	// Check for any identifiable substring
	if strings.Contains(err.Error(), "super-secret") {
		t.Errorf("token substring leaked in error message: %v", err)
	}
}

func TestTokenNotInSlogOutput(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	prev := slog.Default()
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(prev) })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway) // 502 — retryable 5xx
	}))
	defer srv.Close()

	c := NewClient(srv.URL, testSecretToken)
	c.MaxRetries = 0
	c.RetryDelay = 0

	c.Get(context.Background(), "/api/test")

	output := buf.String()
	if strings.Contains(output, testSecretToken) {
		t.Errorf("token leaked in slog output:\n%s", output)
	}
	if strings.Contains(output, "super-secret") {
		t.Errorf("token substring leaked in slog output:\n%s", output)
	}
}

func TestAuthorizationHeaderNotInErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return a 400 to trigger the client error path. Verify the Authorization
		// header value (token) is not included in the returned error string.
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, testSecretToken)
	c.MaxRetries = 0
	c.RetryDelay = 0

	_, err := c.Get(context.Background(), "/api/test")
	if err == nil {
		t.Fatal("expected error")
	}

	// The token must not appear in any error path
	errStr := err.Error()
	if strings.Contains(errStr, "Bearer") {
		t.Errorf("Authorization header value leaked in error: %v", err)
	}
	if strings.Contains(errStr, testSecretToken) {
		t.Errorf("token leaked in error: %v", err)
	}
}
