package api

import "golang.org/x/time/rate"

// Unthrottled removes the client's rate limit — for tests against a local
// fake server only (compiled into test binaries, not into the tool).
func Unthrottled(c *Client) *Client {
	c.limiter = rate.NewLimiter(rate.Inf, 1)
	return c
}
