package api

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"golang.org/x/time/rate"
)

// The Partner API allows about 100 requests per sliding minute per key (the
// exact figure is not part of the contract). Pace slightly below it; the
// X-RateLimit-* headers and Retry-After correct the pace at run time.
var rateLimit = rate.Every(650 * time.Millisecond)

const (
	rateBurst    = 3
	maxRetries   = 3
	maxBodyBytes = 100 * 1024 * 1024 // 100 MiB per response — prevents memory exhaustion

	// max429Retries: a refused request is not counted against the quota and
	// nothing was executed, so throttling is retried more patiently than errors.
	max429Retries = 10
	// maxWait caps any server-requested pause (Retry-After, X-RateLimit-Reset).
	maxWait = 2 * time.Minute
	// lowRemaining: below this many requests left in the window, wait for the
	// window to slide instead of running into a 429.
	lowRemaining = 3
)

// problemCode accepts only the documented shape of RFC 7807 `code` values, so
// nothing else from an error body ever reaches an error message or a log.
var problemCode = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

// StatusError is a non-retryable 4xx response. Callers use errors.As to tell
// "not found" or "forbidden" apart from other failures. Problem is the stable
// machine-readable `code` of the RFC 7807 body (e.g. FORBIDDEN_SCOPE,
// INVALID_CURSOR), when the body carried a well-formed one.
type StatusError struct {
	Code    int
	Problem string
}

func (e *StatusError) Error() string {
	if e.Problem != "" {
		return fmt.Sprintf("client error HTTP %d (%s)", e.Code, e.Problem)
	}
	return fmt.Sprintf("client error HTTP %d", e.Code)
}

// Client is an authenticated HTTP client for the Talkspirit Partner API.
// It only supports GET requests — no Post(), Patch(), or Delete() methods exist.
type Client struct {
	httpClient *http.Client
	baseURL    string
	token      string
	limiter    *rate.Limiter
	userAgent  string
	MaxRetries int
	RetryDelay time.Duration
	// sleep is replaceable in tests.
	sleep func(ctx context.Context, d time.Duration) error
}

func NewClient(baseURL, token string) *Client {
	// Explicit TLS policy (issues #12, #29): pin a minimum of TLS 1.2 and make
	// it impossible to silently ship with certificate verification disabled.
	// InsecureSkipVerify is hard-set to false so a future custom transport edit
	// can't accidentally turn it off; the Bearer token must never traverse an
	// unverified connection.
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: false,
		},
	}
	return &Client{
		httpClient: &http.Client{
			Timeout:   60 * time.Second,
			Transport: transport,
			// Never follow redirects: a redirect could carry the Bearer token
			// to another host.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		baseURL:    baseURL,
		token:      token,
		limiter:    rate.NewLimiter(rateLimit, rateBurst),
		userAgent:  "talkspirit-backup",
		MaxRetries: maxRetries,
		RetryDelay: 2 * time.Second,
		sleep:      sleepCtx,
	}
}

// SetPace replaces the client-side pacing: one request per `every`, with
// bursts of up to `burst`. every <= 0 removes it (tests against a local fake
// server; the server-sent rate-limit headers are still honoured).
func (c *Client) SetPace(every time.Duration, burst int) {
	if every <= 0 {
		c.limiter = rate.NewLimiter(rate.Inf, 1)
		return
	}
	c.limiter = rate.NewLimiter(rate.Every(every), burst)
}

// SetUserAgent sets the User-Agent sent with every request (tool + version).
func (c *Client) SetUserAgent(ua string) { c.userAgent = ua }

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Get performs a GET request with rate limiting and retry.
// Retries 5xx and network errors up to MaxRetries times (exponential backoff),
// and 429 (throttled) up to max429Retries times, honouring Retry-After.
// Other 4xx responses fail immediately — no retry.
func (c *Client) Get(ctx context.Context, path string) ([]byte, error) {
	var lastErr error
	errRetries, throttled := 0, 0
	for {
		if err := c.limiter.Wait(ctx); err != nil {
			return nil, fmt.Errorf("rate limiter: %w", err)
		}

		r := c.doGet(ctx, path)
		if r.err == nil {
			// Slow down before the window is exhausted rather than after.
			if r.remaining >= 0 && r.remaining < lowRemaining && r.wait > 0 {
				if err := c.sleep(ctx, r.wait); err != nil {
					return nil, err
				}
			}
			return r.body, nil
		}
		lastErr = r.err

		switch {
		case r.throttled:
			throttled++
			if throttled > max429Retries {
				return nil, fmt.Errorf("after %d throttled attempts: %w", throttled, lastErr)
			}
			wait := r.wait
			if wait <= 0 {
				wait = c.RetryDelay * time.Duration(throttled)
			}
			if err := c.sleep(ctx, wait); err != nil {
				return nil, err
			}
		case r.retryable:
			errRetries++
			if errRetries > c.MaxRetries {
				return nil, fmt.Errorf("after %d retries: %w", c.MaxRetries, lastErr)
			}
			// Exponential backoff: 2s, 4s, 8s for attempts 1, 2, 3 — or what
			// the server asked for, when it said.
			delay := c.RetryDelay * time.Duration(1<<uint(errRetries-1))
			if r.wait > delay {
				delay = r.wait
			}
			if err := c.sleep(ctx, delay); err != nil {
				return nil, err
			}
		default:
			return nil, lastErr
		}
	}
}

type getResult struct {
	body      []byte
	err       error
	retryable bool
	throttled bool
	// wait is the pause the server asked for (Retry-After, or time until the
	// rate-limit window frees a slot); remaining is X-RateLimit-Remaining or -1.
	wait      time.Duration
	remaining int
}

// doGet performs a single GET request.
func (c *Client) doGet(ctx context.Context, path string) getResult {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return getResult{err: err}
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return getResult{err: err, retryable: true} // network errors are retryable
	}
	defer resp.Body.Close()

	res := getResult{remaining: -1, wait: serverWait(resp.Header, time.Now())}
	if v, err := strconv.Atoi(resp.Header.Get("X-RateLimit-Remaining")); err == nil {
		res.remaining = v
	}

	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		res.err, res.throttled = fmt.Errorf("rate limited (429)"), true
		return res
	case resp.StatusCode >= 500:
		res.err, res.retryable = fmt.Errorf("server error HTTP %d", resp.StatusCode), true
		return res
	case resp.StatusCode >= 300:
		res.err = &StatusError{Code: resp.StatusCode, Problem: readProblemCode(resp.Body)}
		return res
	}

	res.body, res.err = io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	return res
}

// serverWait returns the pause the response asks for: Retry-After (seconds)
// or, failing that, the time until X-RateLimit-Reset (Unix seconds). Capped.
func serverWait(h http.Header, now time.Time) time.Duration {
	var d time.Duration
	if s, err := strconv.Atoi(h.Get("Retry-After")); err == nil && s > 0 {
		d = time.Duration(s) * time.Second
	} else if r, err := strconv.ParseInt(h.Get("X-RateLimit-Reset"), 10, 64); err == nil {
		d = time.Unix(r, 0).Sub(now)
	}
	if d < 0 {
		return 0
	}
	if d > maxWait {
		return maxWait
	}
	return d
}

// readProblemCode extracts the RFC 7807 `code` from an error body. Only a
// value matching problemCode is returned; the rest of the body is discarded.
func readProblemCode(body io.Reader) string {
	b, err := io.ReadAll(io.LimitReader(body, 16*1024))
	if err != nil {
		return ""
	}
	var top map[string]json.RawMessage
	if json.Unmarshal(b, &top) != nil {
		return ""
	}
	var cands []string
	// The documented problem body carries `code` at the top level; FastAPI
	// wraps it in `detail` on some routes, the edge limiter in `error`.
	for _, k := range []string{"detail", "error"} {
		var inner struct {
			Code string `json:"code"`
		}
		if raw, ok := top[k]; ok && json.Unmarshal(raw, &inner) == nil {
			cands = append(cands, inner.Code)
		}
	}
	var code string
	if raw, ok := top["code"]; ok && json.Unmarshal(raw, &code) == nil {
		cands = append(cands, code)
	}
	for _, c := range cands {
		if problemCode.MatchString(c) {
			return c
		}
	}
	return ""
}
