# Security Hardening Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Harden the Holaspirit Backup Tool against write-access abuse, path traversal,
token leakage, and manifest tampering — and add CBOM for cryptographic transparency.

**Architecture:** All changes are additive security controls layered onto the existing
concurrent-fetcher architecture. No feature additions, only security fixes and removals.
All commits go directly to `main` via GitHub MCP (`mcp__plugin_github_github__create_or_update_file`).

**Tech Stack:** Go 1.21+ (slog, crypto/hmac, crypto/sha256), CycloneDX cdxgen (Node.js/npx),
GitHub Actions, Windows Credential Manager (wincred)

**Note on TDD:** Since we use the GitHub MCP (no local shell), tests are written together
with the implementation in each commit. CI (GitHub Actions) is the test runner — check
`https://github.com/kAYd9iN/holaspirit-backup/actions` after each push.

---

## Task 1: Remove exports.go (POST requests)

**Why:** `exports.go` contains `http.MethodPost` requests — violates GET-only contract.

**Files:**
- Delete: `internal/backup/exports.go`
- Delete: `internal/backup/exports_test.go`
- Modify: `cmd/backup/main.go` (remove exporter references)

**Step 1: Delete exports.go**

Use `mcp__plugin_github_github__delete_file` with SHA `bf76e766eb5a4580043d11c3491623aef0ac845f`.

**Step 2: Delete exports_test.go**

Use `mcp__plugin_github_github__delete_file` with SHA `e8d4d45f755be567569ca94fdcc3071555855a25`.

**Step 3: Remove exporter from main.go**

Remove the `NewExporter` / `exporter.Run()` block (lines ~60-66 in current main.go).
Also remove the `"log"` import and replace with `"log/slog"` (see Task 6).

**Step 4: Verify CI passes**

Check `https://github.com/kAYd9iN/holaspirit-backup/actions` — green = OK.

---

## Task 2: Fix client.go — remove Token()/BaseURL(), fix retry logic

**Why:**
- `Token()` exposes the raw secret — remove it
- `BaseURL()` was only used by exports.go — remove it
- Current retry retries all errors including 4xx — should only retry 429 + 5xx

**Files:**
- Modify: `internal/api/client.go`
- Modify: `internal/api/client_test.go`

**Step 1: Write failing test for retry behaviour**

Add to `client_test.go`:

```go
func TestClientDoesNotRetryOn4xx(t *testing.T) {
    calls := 0
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        calls++
        w.WriteHeader(http.StatusNotFound) // 404
    }))
    defer srv.Close()

    c := NewClient(srv.URL, "test-token")
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

    c := NewClient(srv.URL, "test-token")
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
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.Method != http.MethodGet {
            t.Errorf("expected GET, got %s", r.Method)
            w.WriteHeader(http.StatusMethodNotAllowed)
            return
        }
        w.Write([]byte(`{"data":[]}`))
    }))
    defer srv.Close()

    c := NewClient(srv.URL, "test-token")
    c.MaxRetries = 0
    for i := 0; i < 5; i++ {
        c.Get(context.Background(), "/api/test")
    }
}
```

**Step 2: Update client.go**

```go
package api

import (
    "context"
    "fmt"
    "io"
    "net/http"
    "time"

    "golang.org/x/time/rate"
)

var rateLimit = rate.Every(1200 * time.Millisecond)

const (
    rateBurst  = 20
    maxRetries = 3
)

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
// Only retries on 429 and 5xx responses. Never retries on 4xx.
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

// doGet returns (body, retryable, error).
// retryable is true for 429 and 5xx; false for all other errors.
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
        return nil, false, fmt.Errorf("client error HTTP %d (not retrying)", resp.StatusCode)
    }

    body, err := io.ReadAll(resp.Body)
    return body, false, err
}
```

**Step 3: Verify CI passes**

---

## Task 3: Fix runner.go — bounded worker pool (5 workers)

**Why:** Current code launches one goroutine per endpoint (unbounded). With ~20 endpoints
this is fine today, but a bounded pool is more principled and easier to reason about.

**Files:**
- Modify: `internal/backup/runner.go`
- Modify: `internal/backup/runner_test.go`

**Step 1: Write test for concurrency limit**

Add to `runner_test.go`:

```go
func TestRunFetchersMaxConcurrency(t *testing.T) {
    const maxWorkers = 5
    var mu sync.Mutex
    var peak, current int

    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        mu.Lock()
        current++
        if current > peak {
            peak = current
        }
        mu.Unlock()
        time.Sleep(20 * time.Millisecond)
        mu.Lock()
        current--
        mu.Unlock()
        w.Write([]byte(`{"data":[],"meta":{}}`))
    }))
    defer srv.Close()

    // Create 20 endpoints to saturate the pool
    endpoints := make([]api.Endpoint, 20)
    for i := range endpoints {
        endpoints[i] = api.Endpoint{Name: fmt.Sprintf("ep%d", i), Path: "/api/test", Paginated: false}
    }

    client := api.NewClient(srv.URL, "tok")
    client.MaxRetries = 0
    w, _ := storage.NewWriter(t.TempDir(), time.Now())
    RunFetchers(context.Background(), client, w, endpoints)

    if peak > maxWorkers {
        t.Errorf("peak concurrency %d exceeded max %d", peak, maxWorkers)
    }
}
```

**Step 2: Update runner.go**

```go
package backup

import (
    "context"
    "encoding/json"
    "fmt"
    "sync"

    "github.com/kAYd9iN/holaspirit-backup/internal/api"
    "github.com/kAYd9iN/holaspirit-backup/internal/storage"
)

const workerCount = 5

type Result struct {
    Name    string
    Records int
    Err     error
}

// RunFetchers fetches all endpoints concurrently with a bounded worker pool.
func RunFetchers(ctx context.Context, client *api.Client, w *storage.Writer, endpoints []api.Endpoint) []Result {
    jobs := make(chan indexedJob, len(endpoints))
    results := make([]Result, len(endpoints))
    var wg sync.WaitGroup

    for i := 0; i < workerCount; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            for job := range jobs {
                results[job.idx] = fetch(ctx, client, w, job.ep)
            }
        }()
    }

    for i, ep := range endpoints {
        jobs <- indexedJob{idx: i, ep: ep}
    }
    close(jobs)
    wg.Wait()
    return results
}

type indexedJob struct {
    idx int
    ep  api.Endpoint
}

func fetch(ctx context.Context, client *api.Client, w *storage.Writer, ep api.Endpoint) Result {
    var items []json.RawMessage
    var err error

    if ep.Paginated {
        items, err = api.FetchAllPages(ctx, client, ep.Path)
    } else {
        body, e := client.Get(ctx, ep.Path)
        if e != nil {
            return Result{Name: ep.Name, Err: e}
        }
        items = []json.RawMessage{body}
    }

    if err != nil {
        return Result{Name: ep.Name, Err: fmt.Errorf("fetch %s: %w", ep.Name, err)}
    }

    data, err := json.Marshal(items)
    if err != nil {
        return Result{Name: ep.Name, Err: err}
    }

    if err := w.WriteJSON(ep.Name, data); err != nil {
        return Result{Name: ep.Name, Err: err}
    }

    return Result{Name: ep.Name, Records: len(items)}
}
```

**Step 3: Verify CI passes**

---

## Task 4: Fix storage/writer.go — path traversal prevention

**Why:** Endpoint names are used directly as filenames. A malicious name like
`../../etc/passwd` would write outside the backup directory.

**Files:**
- Modify: `internal/storage/writer.go`
- Modify: `internal/storage/writer_test.go`

**Step 1: Write failing test**

```go
func TestWriteJSONPreventPathTraversal(t *testing.T) {
    w, err := NewWriter(t.TempDir(), time.Now())
    if err != nil {
        t.Fatal(err)
    }

    // These names must NOT escape the backup dir
    dangerousNames := []string{
        "../../etc/passwd",
        "../secret",
        "/absolute/path",
        "foo/../../bar",
        "null\x00byte",
    }

    for _, name := range dangerousNames {
        err := w.WriteJSON(name, []byte(`[]`))
        if err != nil {
            continue // error is fine
        }
        // If no error, verify file is inside backup dir
        entries, _ := os.ReadDir(w.Dir())
        for _, e := range entries {
            fullPath := filepath.Join(w.Dir(), e.Name())
            rel, err := filepath.Rel(w.Dir(), fullPath)
            if err != nil || strings.HasPrefix(rel, "..") {
                t.Errorf("file escaped backup dir: %s", fullPath)
            }
        }
    }
}

func TestSanitizeName(t *testing.T) {
    cases := []struct{ input, want string }{
        {"circles", "circles"},
        {"../../etc/passwd", "______etc_passwd"},
        {"foo/bar", "foo_bar"},
        {"normal-name_123", "normal-name_123"},
    }
    for _, c := range cases {
        got := sanitizeName(c.input)
        if got != c.want {
            t.Errorf("sanitizeName(%q) = %q, want %q", c.input, got, c.want)
        }
    }
}
```

**Step 2: Update writer.go**

```go
package storage

import (
    "encoding/json"
    "fmt"
    "os"
    "path/filepath"
    "regexp"
    "time"
)

var unsafeChars = regexp.MustCompile(`[^a-zA-Z0-9_\-]`)

// sanitizeName replaces all characters outside [a-zA-Z0-9_-] with underscores.
func sanitizeName(name string) string {
    return unsafeChars.ReplaceAllString(name, "_")
}

type Writer struct {
    dir string
}

func NewWriter(baseDir string, ts time.Time) (*Writer, error) {
    dir := filepath.Join(baseDir, ts.UTC().Format("2006-01-02T15-04-05"))
    if err := os.MkdirAll(dir, 0750); err != nil {
        return nil, fmt.Errorf("create backup dir: %w", err)
    }
    return &Writer{dir: dir}, nil
}

func (w *Writer) Dir() string { return w.dir }

// WriteJSON sanitizes the name, then pretty-prints and writes data to <name>.json.
func (w *Writer) WriteJSON(name string, data []byte) error {
    safe := sanitizeName(name)
    dest := filepath.Join(w.dir, safe+".json")

    // Verify the destination is inside the backup directory (belt-and-suspenders)
    rel, err := filepath.Rel(w.dir, dest)
    if err != nil || len(rel) >= 2 && rel[:2] == ".." {
        return fmt.Errorf("path traversal detected: %q", name)
    }

    var v interface{}
    if err := json.Unmarshal(data, &v); err != nil {
        return os.WriteFile(dest, data, 0640)
    }
    pretty, err := json.MarshalIndent(v, "", "  ")
    if err != nil {
        return err
    }
    return os.WriteFile(dest, pretty, 0640)
}

// WriteFile writes raw bytes. Name is sanitized before use.
func (w *Writer) WriteFile(name string, data []byte) error {
    safe := sanitizeName(name)
    return os.WriteFile(filepath.Join(w.dir, safe), data, 0640)
}
```

**Step 3: Verify CI passes**

---

## Task 5: Fix manifest.go — HMAC-SHA-256 signature

**Why:** manifest.json is integrity-proof for backup files, but is itself unsigned.
A tampered manifest could hide corrupted backups.

**Files:**
- Modify: `internal/backup/manifest.go`
- Modify: `internal/backup/manifest_test.go`

**Step 1: Write failing tests**

```go
func TestManifestHMACRoundTrip(t *testing.T) {
    dir := t.TempDir()
    m := NewManifest("org123", "1.0.0", time.Now())

    // Write a dummy file to hash
    fpath := filepath.Join(dir, "circles.json")
    os.WriteFile(fpath, []byte(`[]`), 0640)
    m.AddFile(fpath)

    token := "api:test-token-xyz"
    manifestPath := filepath.Join(dir, "backup-manifest.json")
    sigPath := filepath.Join(dir, "backup-manifest.sig")

    if err := m.Write(manifestPath, token); err != nil {
        t.Fatalf("Write: %v", err)
    }

    // sig file must exist
    if _, err := os.Stat(sigPath); err != nil {
        t.Fatalf("sig file missing: %v", err)
    }

    // Verify must pass with correct token
    if err := VerifyManifest(manifestPath, token); err != nil {
        t.Errorf("VerifyManifest failed: %v", err)
    }

    // Tamper with manifest — verify must fail
    data, _ := os.ReadFile(manifestPath)
    data = append(data[:len(data)-1], []byte(` }`)...) // corrupt last byte
    os.WriteFile(manifestPath, data, 0640)

    if err := VerifyManifest(manifestPath, token); err == nil {
        t.Error("expected verification failure after tampering")
    }
}

func TestManifestTokenNotInOutput(t *testing.T) {
    dir := t.TempDir()
    m := NewManifest("org123", "1.0.0", time.Now())
    token := "api:super-secret-token"

    manifestPath := filepath.Join(dir, "backup-manifest.json")
    m.Write(manifestPath, token)

    // Token must not appear in manifest.json
    data, _ := os.ReadFile(manifestPath)
    if strings.Contains(string(data), token) {
        t.Error("token found in manifest.json")
    }
    if strings.Contains(string(data), "super-secret") {
        t.Error("token substring found in manifest.json")
    }

    // Token must not appear in manifest.sig
    sig, _ := os.ReadFile(dir + "/backup-manifest.sig")
    if strings.Contains(string(sig), token) {
        t.Error("token found in manifest.sig")
    }
}
```

**Step 2: Update manifest.go**

```go
package backup

import (
    "crypto/hmac"
    "crypto/sha256"
    "encoding/hex"
    "encoding/json"
    "fmt"
    "io"
    "os"
    "path/filepath"
    "strings"
    "time"
)

type FileEntry struct {
    Name    string `json:"name"`
    SHA256  string `json:"sha256"`
    Records int    `json:"records,omitempty"`
    Status  string `json:"status"`
    Error   string `json:"error,omitempty"`
}

type Summary struct {
    TotalFiles int `json:"total_files"`
    Successful int `json:"successful"`
    Failed     int `json:"failed"`
}

type Manifest struct {
    Timestamp      time.Time   `json:"timestamp"`
    ToolVersion    string      `json:"tool_version"`
    OrganizationID string      `json:"organization_id"`
    Files          []FileEntry `json:"files"`
    Summary        Summary     `json:"summary"`
}

func NewManifest(orgID, version string, ts time.Time) *Manifest {
    return &Manifest{
        Timestamp:      ts.UTC(),
        ToolVersion:    version,
        OrganizationID: orgID,
    }
}

func (m *Manifest) AddFile(path string) error {
    f, err := os.Open(path)
    if err != nil {
        return fmt.Errorf("open %s: %w", path, err)
    }
    defer f.Close()

    h := sha256.New()
    if _, err := io.Copy(h, f); err != nil {
        return fmt.Errorf("hash %s: %w", path, err)
    }

    m.Files = append(m.Files, FileEntry{
        Name:   filepath.Base(path),
        SHA256: hex.EncodeToString(h.Sum(nil)),
        Status: "ok",
    })
    return nil
}

func (m *Manifest) AddFailedFile(name string, err error) {
    m.Files = append(m.Files, FileEntry{
        Name:   name + ".json",
        Status: "failed",
        Error:  err.Error(),
    })
}

// Write serializes the manifest to path and writes an HMAC-SHA-256 signature
// to path + ".sig" (replacing ".json" suffix with ".sig").
// The token is never written to disk.
func (m *Manifest) Write(path, token string) error {
    m.Summary = Summary{TotalFiles: len(m.Files)}
    for _, f := range m.Files {
        if f.Status == "ok" {
            m.Summary.Successful++
        } else {
            m.Summary.Failed++
        }
    }

    data, err := json.MarshalIndent(m, "", "  ")
    if err != nil {
        return err
    }

    if err := os.WriteFile(path, data, 0640); err != nil {
        return err
    }

    sig := computeHMAC(data, token)
    sigPath := strings.TrimSuffix(path, ".json") + ".sig"
    return os.WriteFile(sigPath, []byte(sig), 0640)
}

// VerifyManifest checks the HMAC signature of a manifest file.
// Returns nil if valid, error if tampered or wrong token.
func VerifyManifest(manifestPath, token string) error {
    data, err := os.ReadFile(manifestPath)
    if err != nil {
        return fmt.Errorf("read manifest: %w", err)
    }

    sigPath := strings.TrimSuffix(manifestPath, ".json") + ".sig"
    sigBytes, err := os.ReadFile(sigPath)
    if err != nil {
        return fmt.Errorf("read sig: %w", err)
    }

    expected := computeHMAC(data, token)
    if !hmac.Equal([]byte(expected), sigBytes) {
        return fmt.Errorf("manifest signature mismatch — file may have been tampered with")
    }
    return nil
}

// computeHMAC derives a key from the token and computes HMAC-SHA-256 over data.
func computeHMAC(data []byte, token string) string {
    // Derive a domain-separated key so the raw token is never used as-is
    keyHash := sha256.Sum256([]byte("holaspirit-backup-manifest\x00" + token))
    mac := hmac.New(sha256.New, keyHash[:])
    mac.Write(data)
    return hex.EncodeToString(mac.Sum(nil))
}
```

**Step 3: Update all callers of `manifest.Write()`**

In `cmd/backup/main.go`, change:
```go
manifest.Write(manifestPath)
```
to:
```go
manifest.Write(manifestPath, token)
```

**Step 4: Verify CI passes**

---

## Task 6: Add verify subcommand + slog in main.go

**Why:** Users need a way to verify backup integrity. Also switch from `log` to `slog`
for structured logging (token-safe output).

**Files:**
- Modify: `cmd/backup/main.go`
- Create: `cmd/backup/verify.go`

**Step 1: Create verify.go**

```go
package main

import (
    "fmt"
    "log/slog"
    "os"

    "github.com/kAYd9iN/holaspirit-backup/internal/backup"
)

// runVerify implements: backup.exe verify --dir <path>
func runVerify(dir string) int {
    token, err := getToken()
    if err != nil {
        slog.Error("loading token", "error", err)
        return 2
    }

    manifestPath := dir + "/backup-manifest.json"
    if err := backup.VerifyManifest(manifestPath, token); err != nil {
        slog.Error("verification FAILED", "error", err)
        fmt.Fprintln(os.Stderr, "TAMPERED or invalid token — do not trust this backup.")
        return 1
    }

    slog.Info("manifest OK — backup integrity verified", "dir", dir)
    return 0
}
```

**Step 2: Update main.go**

Replace the `log` package with `log/slog`. Add subcommand dispatch at the top of `main()`:

```go
package main

import (
    "context"
    "flag"
    "fmt"
    "log/slog"
    "os"
    "path/filepath"
    "time"

    "github.com/kAYd9iN/holaspirit-backup/internal/api"
    "github.com/kAYd9iN/holaspirit-backup/internal/backup"
    "github.com/kAYd9iN/holaspirit-backup/internal/storage"
)

const (
    version           = "1.0.0"
    holaspiritBaseURL = "https://app.holaspirit.com"
)

func main() {
    // Subcommand dispatch
    if len(os.Args) > 1 && os.Args[1] == "verify" {
        fs := flag.NewFlagSet("verify", flag.ExitOnError)
        dir := fs.String("dir", "", "Backup directory to verify (required)")
        fs.Parse(os.Args[2:])
        if *dir == "" {
            fmt.Fprintln(os.Stderr, "usage: backup verify --dir <path>")
            os.Exit(2)
        }
        os.Exit(runVerify(*dir))
    }

    // Main backup command
    outputDir := flag.String("output", "./backup", "Backup destination directory")
    orgID := flag.String("org-id", "", "Organization ID (auto-detected if empty)")
    dryRun := flag.Bool("dry-run", false, "Test connection without writing files")
    showVer := flag.Bool("version", false, "Show version and exit")
    flag.Parse()

    if *showVer {
        fmt.Printf("holaspirit-backup v%s\n", version)
        os.Exit(0)
    }

    token, err := getToken()
    if err != nil {
        slog.Error("loading token", "error", err,
            "hint", "On Windows: cmdkey /generic:holaspirit-backup /user:api /pass:api:TOKEN")
        os.Exit(2)
    }

    client := api.NewClient(holaspiritBaseURL, token)
    ctx := context.Background()

    if *orgID == "" {
        id, err := api.DiscoverOrgID(ctx, client)
        if err != nil {
            slog.Error("discovering organization ID", "error", err)
            os.Exit(2)
        }
        *orgID = id
        slog.Info("organization discovered", "id", *orgID)
    }

    if *dryRun {
        slog.Info("dry run successful — connection OK")
        os.Exit(0)
    }

    ts := time.Now()
    w, err := storage.NewWriter(*outputDir, ts)
    if err != nil {
        slog.Error("creating backup directory", "error", err)
        os.Exit(2)
    }
    slog.Info("backup directory created", "path", w.Dir())

    endpoints := api.AllEndpoints(*orgID)
    slog.Info("fetching endpoints", "count", len(endpoints))

    manifest := backup.NewManifest(*orgID, version, ts)
    results := backup.RunFetchers(ctx, client, w, endpoints)

    exitCode := 0
    for _, r := range results {
        if r.Err != nil {
            slog.Warn("endpoint failed", "name", r.Name, "error", r.Err)
            manifest.AddFailedFile(r.Name, r.Err)
            exitCode = 1
        } else {
            slog.Info("endpoint ok", "name", r.Name, "records", r.Records)
            filePath := filepath.Join(w.Dir(), r.Name+".json")
            if err := manifest.AddFile(filePath); err != nil {
                slog.Warn("manifest hash failed", "name", r.Name, "error", err)
            }
        }
    }

    manifestPath := filepath.Join(w.Dir(), "backup-manifest.json")
    if err := manifest.Write(manifestPath, token); err != nil {
        slog.Error("writing manifest", "error", err)
        os.Exit(2)
    }

    slog.Info("backup complete", "dir", w.Dir(), "manifest", manifestPath)
    os.Exit(exitCode)
}
```

**Step 3: Verify CI passes**

---

## Task 7: Add token-leak security tests

**Why:** Prove the token never surfaces in logs or error messages.

**Files:**
- Create: `internal/api/security_test.go`

**Step 1: Create security_test.go**

```go
package api_test

import (
    "bytes"
    "context"
    "log/slog"
    "net/http"
    "net/http/httptest"
    "strings"
    "testing"
)

const testToken = "api:super-secret-token-do-not-leak"

func TestTokenNotInErrorMessages(t *testing.T) {
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusInternalServerError)
    }))
    defer srv.Close()

    c := NewClient(srv.URL, testToken)
    c.MaxRetries = 0
    c.RetryDelay = 0

    _, err := c.Get(context.Background(), "/api/test")
    if err == nil {
        t.Fatal("expected error")
    }

    if strings.Contains(err.Error(), testToken) {
        t.Errorf("token leaked in error message: %v", err)
    }
}

func TestTokenNotInSlogOutput(t *testing.T) {
    var buf bytes.Buffer
    logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
    slog.SetDefault(logger)
    t.Cleanup(func() { slog.SetDefault(slog.Default()) })

    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusBadGateway)
    }))
    defer srv.Close()

    c := NewClient(srv.URL, testToken)
    c.MaxRetries = 0
    c.Get(context.Background(), "/api/test")

    if strings.Contains(buf.String(), testToken) {
        t.Errorf("token leaked in slog output:\n%s", buf.String())
    }
    if strings.Contains(buf.String(), "super-secret") {
        t.Errorf("token substring leaked in slog output:\n%s", buf.String())
    }
}
```

**Step 2: Verify CI passes**

---

## Task 8: Update CI — add CBOM generation

**Why:** Document cryptographic assets in CycloneDX CBOM format per OWASP standard.

**Files:**
- Modify: `.github/workflows/ci.yml`

**Step 1: Update ci.yml**

```yaml
name: CI

on:
  push:
    branches: [main]
  pull_request:
    branches: [main]

permissions:
  contents: read

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version: '1.21'
          cache: true

      - name: Verify dependencies
        run: go mod verify

      - name: govulncheck
        run: go run golang.org/x/vuln/cmd/govulncheck@latest ./...

      - name: gosec
        run: go run github.com/securego/gosec/v2/cmd/gosec@latest -quiet ./...

      - name: Run tests (with race detector)
        run: go test -race -cover ./...

      - name: Build Windows binary
        run: GOOS=windows GOARCH=amd64 go build -o backup.exe ./cmd/backup/

      - name: Generate CBOM (CycloneDX cryptography BOM)
        run: npx --yes @cyclonedx/cdxgen@latest --type cryptography --output cbom.cdx.json .

      - name: Upload Windows binary
        uses: actions/upload-artifact@v4
        if: github.ref == 'refs/heads/main'
        with:
          name: backup-windows-amd64
          path: backup.exe
          retention-days: 90

      - name: Upload CBOM
        uses: actions/upload-artifact@v4
        if: github.ref == 'refs/heads/main'
        with:
          name: cbom-cyclonedx
          path: cbom.cdx.json
          retention-days: 365
```

**Step 2: Verify CI passes and CBOM artefact appears in GitHub Actions**

---

## Task 9: Update Confluence documentation

**Why:** Confluence must stay in sync with code changes.

**Space:** https://ewigepluseins.atlassian.net/wiki/spaces/HB

**Updates needed:**
1. Add section "Security" with: GET-only constraint, HMAC manifest signing,
   path traversal prevention, token-leak prevention
2. Add section "CBOM" with link to CI artefacts
3. Update "Backup-Ablauf" — remove async exports (PDF/XLSX)
4. Add section "Integritaet pruefen" with `backup.exe verify --dir <path>` usage

Use `mcp__plugin_atlassian_atlassian__searchConfluenceUsingCql` to find the existing page,
then `mcp__plugin_atlassian_atlassian__updateConfluencePage` to update it.

---

## Summary

| Task | Status |
|---|---|
| 1. Remove exports.go | pending |
| 2. Fix client.go | pending |
| 3. Fix runner.go (worker pool) | pending |
| 4. Fix writer.go (path traversal) | pending |
| 5. Fix manifest.go (HMAC) | pending |
| 6. Add verify subcommand + slog | pending |
| 7. Token-leak security tests | pending |
| 8. Update CI (CBOM) | pending |
| 9. Update Confluence | pending |
