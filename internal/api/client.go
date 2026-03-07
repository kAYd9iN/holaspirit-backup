package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"golang.org/x/time/rate"
)

// 250 requests / 5 minutes = 1 request per 1.2 seconds, burst of 20
var rateLimit = rate.Every(1200 * time.Millisecond)

const (
	rateBurst  = 20
	maxRetries = 3
)

// Client is an authenticated HTTP client for the Holaspirit API.
// It only supports GET requests — no Post(), Patch(), or Delete() methods exist.
type Client struct {
	httpClient *http.Client
	baseURL    string
	token      string
	limiter    *rate.Limiter
	MaxRetries int
	RetryDelay time.Duration
}

func NewClient(baseURL, token string) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		baseURL:    baseURL,
		token:      token,
		limiter:    rate.NewLimiter(rateLimit, rateBurst),
		MaxRetries: maxRetries,
		RetryDelay: 2 * time.Second,
	}
}

// Get performs a GET request with rate limiting and retry.
// Only retries on 429 (rate limited) and 5xx (server errors).
// 4xx responses (except 429) fail immediately — no retry.
func (c *Client) Get(ctx context.Context, path string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt <= c.MaxRetries; attempt++ {
		if attempt > 0 {
			delay := c.RetryDelay * time.Duration(1<<uint(attempt-1))
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		if err := c.limiter.Wait(ctx); err != nil {
			return nil, fmt.Errorf("rate limiter: %w", err)
		}

		body, retryable, err := c.doGet(ctx, path)
		if err == nil {
			return body, nil
		}
		if !retryable {
			return nil, err
		}
		lastErr = err
	}
	return nil, fmt.Errorf("after %d retries: %w", c.MaxRetries, lastErr)
}

// doGet performs a single GET request. Returns (body, retryable, error).
// retryable=true for 429, 5xx, and network errors.
// retryable=false for 4xx (except 429) — these are not transient.
func (c *Client) doGet(ctx context.Context, path string) ([]byte, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, true, err // network errors are retryable
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, true, fmt.Errorf("rate limited (429)")
	case resp.StatusCode >= 500:
		return nil, true, fmt.Errorf("server error HTTP %d", resp.StatusCode)
	case resp.StatusCode >= 400:
		return nil, false, fmt.Errorf("client error HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	return body, false, err
}
