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

// BaseURL returns the base URL of the client.
func (c *Client) BaseURL() string { return c.baseURL }

// Token returns the API token.
func (c *Client) Token() string { return c.token }

// Get performs a GET request with rate limiting and retry.
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

		body, err := c.doGet(ctx, path)
		if err == nil {
			return body, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("after %d retries: %w", c.MaxRetries, lastErr)
}

func (c *Client) doGet(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("rate limited (429)")
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}
