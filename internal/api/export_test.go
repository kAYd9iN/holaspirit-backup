package api

import (
	"context"
	"time"

	"golang.org/x/time/rate"
)

// Unthrottled removes the client's rate limit and server-requested pauses —
// for tests against a local fake server only (compiled into test binaries,
// not into the tool).
func Unthrottled(c *Client) *Client {
	c.limiter = rate.NewLimiter(rate.Inf, 1)
	c.sleep = func(context.Context, time.Duration) error { return nil }
	return c
}

// RecordSleeps makes the client record instead of perform its pauses.
func RecordSleeps(c *Client, into *[]time.Duration) *Client {
	c.limiter = rate.NewLimiter(rate.Inf, 1)
	c.sleep = func(_ context.Context, d time.Duration) error {
		*into = append(*into, d)
		return nil
	}
	return c
}
