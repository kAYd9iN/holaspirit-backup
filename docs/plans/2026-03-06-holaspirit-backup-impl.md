# Holaspirit Backup Tool — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Build a Go CLI tool that backs up all Holaspirit organization data as JSON files with SHA256 integrity manifest, runnable via Windows Task Scheduler.

**Architecture:** Concurrent goroutines fetch all API endpoints in parallel, coordinated by a token-bucket rate limiter. Results are written as individual JSON files plus a manifest. Secrets are read from Windows Credential Manager via an interface (mockable for tests).

**Tech Stack:** Go 1.22+, `golang.org/x/time/rate` (rate limiter), `github.com/danieljoos/wincred` (Windows Credential Manager), `net/http/httptest` (mock server in tests), GitHub Actions CI.

---

## Task 1: GitHub Repository erstellen & Projekt bootstrappen

**Files:**
- Create: `holaspirit-backup/go.mod`
- Create: `holaspirit-backup/.gitignore`
- Create: `holaspirit-backup/cmd/backup/main.go` (Stub)

**Step 1: GitHub Repo via MCP erstellen**

Im Claude-MCP-GitHub-Tool:
```
create_repository(
  name: "holaspirit-backup",
  description: "Automated backup tool for Holaspirit organization data",
  private: true,
  auto_init: false
)
```

**Step 2: Lokales Verzeichnis anlegen**

```bash
mkdir -p ~/holaspirit-backup
cd ~/holaspirit-backup
git init
git remote add origin https://github.com/kAYd9iN/holaspirit-backup.git
```

**Step 3: Go-Modul initialisieren**

```bash
go mod init github.com/kAYd9iN/holaspirit-backup
```

**Step 4: Verzeichnisstruktur anlegen**

```bash
mkdir -p cmd/backup internal/api internal/backup internal/credentials internal/storage docs/plans
```

**Step 5: .gitignore erstellen**

```
backup/
*.exe
vendor/
```

**Step 6: Stub main.go**

```go
// cmd/backup/main.go
package main

import "fmt"

func main() {
    fmt.Println("holaspirit-backup v0.0.1")
}
```

**Step 7: Kompilieren testen**

```bash
go build ./cmd/backup/
```
Expected: keine Fehler, Binary erstellt.

**Step 8: Commit**

```bash
git add .
git commit -m "chore: initialize Go module and project structure"
git push -u origin main
```

---

## Task 2: Credential Manager Interface

**Files:**
- Create: `internal/credentials/credentials.go`
- Create: `internal/credentials/wincred.go`
- Create: `internal/credentials/mock.go`
- Create: `internal/credentials/credentials_test.go`

**Step 1: Interface und Mock definieren**

```go
// internal/credentials/credentials.go
package credentials

// Manager abstracts secret retrieval for testability.
type Manager interface {
    GetToken() (string, error)
}
```

```go
// internal/credentials/mock.go
package credentials

// Mock is a test double for Manager.
type Mock struct {
    Token string
    Err   error
}

func (m *Mock) GetToken() (string, error) {
    return m.Token, m.Err
}
```

**Step 2: Failing test schreiben**

```go
// internal/credentials/credentials_test.go
package credentials_test

import (
    "testing"
    "github.com/kAYd9iN/holaspirit-backup/internal/credentials"
)

func TestMockReturnsToken(t *testing.T) {
    mock := &credentials.Mock{Token: "api:test123"}
    token, err := mock.GetToken()
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if token != "api:test123" {
        t.Errorf("got %q, want %q", token, "api:test123")
    }
}

func TestMockReturnsError(t *testing.T) {
    mock := &credentials.Mock{Err: fmt.Errorf("not found")}
    _, err := mock.GetToken()
    if err == nil {
        t.Fatal("expected error, got nil")
    }
}
```

**Step 3: Test ausfuehren — muss FAIL**

```bash
go test ./internal/credentials/...
```
Expected: FAIL (fmt nicht importiert)

**Step 4: Test korrigieren und ausfuehren**

```go
// credentials_test.go — import hinzufuegen
import (
    "fmt"
    "testing"
    "github.com/kAYd9iN/holaspirit-backup/internal/credentials"
)
```

```bash
go test ./internal/credentials/...
```
Expected: PASS

**Step 5: Windows-Implementierung (build-tag: nur auf Windows kompiliert)**

```go
// internal/credentials/wincred.go
//go:build windows

package credentials

import (
    "fmt"
    "github.com/danieljoos/wincred"
)

const CredentialName = "holaspirit-backup"

type WinCredManager struct {
    name string
}

func NewWinCredManager() *WinCredManager {
    return &WinCredManager{name: CredentialName}
}

func (w *WinCredManager) GetToken() (string, error) {
    cred, err := wincred.GetGenericCredential(w.name)
    if err != nil {
        return "", fmt.Errorf("credential %q not found in Windows Credential Manager: %w", w.name, err)
    }
    return string(cred.CredentialBlob), nil
}
```

**Step 6: Abhaengigkeit hinzufuegen**

```bash
go get github.com/danieljoos/wincred
go mod tidy
```

**Step 7: Commit**

```bash
git add internal/credentials/ go.mod go.sum
git commit -m "feat: add credential manager interface with Windows Credential Manager impl"
```

---

## Task 3: HTTP-Client mit Rate-Limiter und Retry

**Files:**
- Create: `internal/api/client.go`
- Create: `internal/api/client_test.go`

**Step 1: Failing tests schreiben**

```go
// internal/api/client_test.go
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
    client.RetryDelay = 1 * time.Millisecond // Test-Beschleunigung
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
```

**Step 2: Test ausfuehren — muss FAIL**

```bash
go test ./internal/api/...
```
Expected: FAIL (package api nicht vorhanden)

**Step 3: Client implementieren**

```go
// internal/api/client.go
package api

import (
    "context"
    "fmt"
    "io"
    "net/http"
    "time"

    "golang.org/x/time/rate"
)

const (
    // 250 requests / 5 minutes = 1 request per 1.2 seconds, burst of 20
    rateLimit  = rate.Every(1200 * time.Millisecond)
    rateBurst  = 20
    maxRetries = 3
)

// Client is an authenticated HTTP client for the Holaspirit API.
type Client struct {
    httpClient  *http.Client
    baseURL     string
    token       string
    limiter     *rate.Limiter
    MaxRetries  int
    RetryDelay  time.Duration
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
```

**Step 4: Abhaengigkeit hinzufuegen**

```bash
go get golang.org/x/time/rate
go mod tidy
```

**Step 5: Tests ausfuehren — muss PASS**

```bash
go test -v ./internal/api/...
```
Expected: PASS alle 3 Tests

**Step 6: Commit**

```bash
git add internal/api/ go.mod go.sum
git commit -m "feat: add HTTP client with rate limiting and retry"
```

---

## Task 4: Pagination

**Files:**
- Create: `internal/api/pagination.go`
- Create: `internal/api/pagination_test.go`

**Step 1: Failing test**

```go
// internal/api/pagination_test.go
package api_test

import (
    "context"
    "encoding/json"
    "fmt"
    "net/http"
    "net/http/httptest"
    "testing"

    "github.com/kAYd9iN/holaspirit-backup/internal/api"
)

func TestFetchAllPages(t *testing.T) {
    page := 1
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        resp := map[string]interface{}{
            "data": []map[string]string{{"id": fmt.Sprintf("item%d", page)}},
            "meta": map[string]interface{}{
                "pagination": map[string]interface{}{
                    "current_page": page,
                    "total_pages":  3,
                },
            },
        }
        json.NewEncoder(w).Encode(resp)
        page++
    }))
    defer srv.Close()

    client := api.NewClient(srv.URL, "api:test")
    items, err := api.FetchAllPages(context.Background(), client, "/items")
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if len(items) != 3 {
        t.Errorf("expected 3 items, got %d", len(items))
    }
}
```

**Step 2: Test ausfuehren — FAIL**

```bash
go test ./internal/api/... -run TestFetchAllPages
```

**Step 3: Pagination implementieren**

```go
// internal/api/pagination.go
package api

import (
    "context"
    "encoding/json"
    "fmt"
)

// HolaspiritResponse is the standard paginated API response.
type HolaspiritResponse struct {
    Data json.RawMessage `json:"data"`
    Meta struct {
        Pagination struct {
            CurrentPage int `json:"current_page"`
            TotalPages  int `json:"total_pages"`
        } `json:"pagination"`
    } `json:"meta"`
}

// FetchAllPages retrieves all pages for a given endpoint and returns
// the combined data array as raw JSON messages.
func FetchAllPages(ctx context.Context, client *Client, path string) ([]json.RawMessage, error) {
    var allItems []json.RawMessage
    page := 1

    for {
        url := fmt.Sprintf("%s?page=%d&per_page=100", path, page)
        body, err := client.Get(ctx, url)
        if err != nil {
            return nil, fmt.Errorf("page %d: %w", page, err)
        }

        var resp HolaspiritResponse
        if err := json.Unmarshal(body, &resp); err != nil {
            return nil, fmt.Errorf("parse page %d: %w", page, err)
        }

        var items []json.RawMessage
        if err := json.Unmarshal(resp.Data, &items); err != nil {
            // data might be a single object, not array
            allItems = append(allItems, resp.Data)
            break
        }
        allItems = append(allItems, items...)

        if page >= resp.Meta.Pagination.TotalPages || resp.Meta.Pagination.TotalPages == 0 {
            break
        }
        page++
    }

    return allItems, nil
}
```

**Step 4: Tests — PASS**

```bash
go test -v ./internal/api/... -run TestFetchAllPages
```

**Step 5: Commit**

```bash
git add internal/api/pagination.go internal/api/pagination_test.go
git commit -m "feat: add pagination support for Holaspirit API"
```

---

## Task 5: Endpoint-Definitionen

**Files:**
- Create: `internal/api/endpoints.go`
- Create: `internal/api/endpoints_test.go`

**Step 1: Endpoints definieren**

```go
// internal/api/endpoints.go
package api

import "fmt"

// Endpoint describes a single API endpoint to back up.
type Endpoint struct {
    Name       string // used as filename (e.g. "circles" -> circles.json)
    Path       string // API path template (use %s for org ID)
    Paginated  bool
}

// AllEndpoints returns all endpoints to back up for a given organization ID.
func AllEndpoints(orgID string) []Endpoint {
    base := fmt.Sprintf("/api/organizations/%s", orgID)
    return []Endpoint{
        {Name: "organization", Path: fmt.Sprintf("/api/organizations/%s", orgID), Paginated: false},
        {Name: "circles", Path: base + "/circles", Paginated: true},
        {Name: "circles-timespent", Path: base + "/circles-timespent", Paginated: true},
        {Name: "roles", Path: base + "/roles", Paginated: true},
        {Name: "members", Path: base + "/members", Paginated: true},
        {Name: "tensions", Path: base + "/tensions", Paginated: true},
        {Name: "policies", Path: base + "/policies", Paginated: true},
        {Name: "meetings", Path: base + "/meetings", Paginated: true},
        {Name: "objectives", Path: base + "/objectives", Paginated: true},
        {Name: "keyresults", Path: base + "/keyresults", Paginated: true},
        {Name: "tasks", Path: base + "/tasks", Paginated: true},
        {Name: "boards", Path: base + "/boards", Paginated: true},
        {Name: "columns", Path: base + "/columns", Paginated: true},
        {Name: "checklists", Path: base + "/checklists", Paginated: true},
        {Name: "metrics", Path: base + "/metrics", Paginated: true},
        {Name: "publications", Path: base + "/publications", Paginated: true},
        {Name: "categories", Path: base + "/categories", Paginated: true},
        {Name: "attachments", Path: base + "/attachments", Paginated: true},
        {Name: "chartviews", Path: base + "/chartviews", Paginated: true},
        {Name: "calendars", Path: base + "/calendars", Paginated: true},
        {Name: "backups", Path: base + "/backups", Paginated: true},
    }
}

// MeResponse is the response from GET /api/me
type MeResponse struct {
    Data struct {
        Relationships struct {
            Organization struct {
                Data struct {
                    ID string `json:"id"`
                } `json:"data"`
            } `json:"organization"`
        } `json:"relationships"`
    } `json:"data"`
}
```

**Step 2: Test**

```go
// internal/api/endpoints_test.go
package api_test

import (
    "testing"
    "github.com/kAYd9iN/holaspirit-backup/internal/api"
)

func TestAllEndpoints_Count(t *testing.T) {
    endpoints := api.AllEndpoints("org123")
    if len(endpoints) != 21 {
        t.Errorf("expected 21 endpoints, got %d", len(endpoints))
    }
}

func TestAllEndpoints_ContainsOrgID(t *testing.T) {
    endpoints := api.AllEndpoints("myorg")
    for _, ep := range endpoints {
        if ep.Name == "circles" {
            if ep.Path != "/api/organizations/myorg/circles" {
                t.Errorf("unexpected path: %s", ep.Path)
            }
            return
        }
    }
    t.Error("circles endpoint not found")
}
```

**Step 3: Tests — PASS**

```bash
go test -v ./internal/api/... -run TestAllEndpoints
```

**Step 4: Commit**

```bash
git add internal/api/endpoints.go internal/api/endpoints_test.go
git commit -m "feat: define all Holaspirit backup endpoints"
```

---

## Task 6: Storage (Dateien schreiben)

**Files:**
- Create: `internal/storage/writer.go`
- Create: `internal/storage/writer_test.go`

**Step 1: Failing test**

```go
// internal/storage/writer_test.go
package storage_test

import (
    "encoding/json"
    "os"
    "path/filepath"
    "testing"
    "time"

    "github.com/kAYd9iN/holaspirit-backup/internal/storage"
)

func TestWriter_CreatesDirAndFile(t *testing.T) {
    tmpDir := t.TempDir()
    ts := time.Date(2026, 3, 6, 2, 0, 0, 0, time.UTC)

    w, err := storage.NewWriter(tmpDir, ts)
    if err != nil {
        t.Fatalf("NewWriter: %v", err)
    }

    data := []byte(`[{"id":"1"}]`)
    if err := w.WriteJSON("circles", data); err != nil {
        t.Fatalf("WriteJSON: %v", err)
    }

    expected := filepath.Join(tmpDir, "2026-03-06T02-00-00", "circles.json")
    content, err := os.ReadFile(expected)
    if err != nil {
        t.Fatalf("file not found: %v", err)
    }

    var out interface{}
    if err := json.Unmarshal(content, &out); err != nil {
        t.Errorf("output is not valid JSON: %v", err)
    }
}
```

**Step 2: Test — FAIL**

```bash
go test ./internal/storage/...
```

**Step 3: Writer implementieren**

```go
// internal/storage/writer.go
package storage

import (
    "encoding/json"
    "fmt"
    "os"
    "path/filepath"
    "time"
)

// Writer writes backup files to a timestamped directory.
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

// WriteJSON pretty-prints data and writes it to <name>.json
func (w *Writer) WriteJSON(name string, data []byte) error {
    // pretty-print for readability
    var v interface{}
    if err := json.Unmarshal(data, &v); err != nil {
        // not JSON, write as-is
        return os.WriteFile(filepath.Join(w.dir, name+".json"), data, 0640)
    }
    pretty, err := json.MarshalIndent(v, "", "  ")
    if err != nil {
        return err
    }
    return os.WriteFile(filepath.Join(w.dir, name+".json"), pretty, 0640)
}

// WriteFile writes raw bytes to a file.
func (w *Writer) WriteFile(name string, data []byte) error {
    return os.WriteFile(filepath.Join(w.dir, name), data, 0640)
}
```

**Step 4: Tests — PASS**

```bash
go test -v ./internal/storage/...
```

**Step 5: Commit**

```bash
git add internal/storage/
git commit -m "feat: add storage writer for timestamped backup directories"
```

---

## Task 7: Manifest (SHA256 + Integritaet)

**Files:**
- Create: `internal/backup/manifest.go`
- Create: `internal/backup/manifest_test.go`

**Step 1: Failing test**

```go
// internal/backup/manifest_test.go
package backup_test

import (
    "encoding/json"
    "os"
    "path/filepath"
    "testing"
    "time"

    "github.com/kAYd9iN/holaspirit-backup/internal/backup"
)

func TestManifest_HashAndWrite(t *testing.T) {
    dir := t.TempDir()

    // Testdatei anlegen
    testFile := filepath.Join(dir, "circles.json")
    os.WriteFile(testFile, []byte(`[{"id":"1"}]`), 0640)

    m := backup.NewManifest("org123", "1.0.0", time.Now())
    if err := m.AddFile(testFile); err != nil {
        t.Fatalf("AddFile: %v", err)
    }

    outPath := filepath.Join(dir, "backup-manifest.json")
    if err := m.Write(outPath); err != nil {
        t.Fatalf("Write: %v", err)
    }

    data, _ := os.ReadFile(outPath)
    var result map[string]interface{}
    if err := json.Unmarshal(data, &result); err != nil {
        t.Fatalf("invalid JSON: %v", err)
    }

    files := result["files"].([]interface{})
    if len(files) != 1 {
        t.Errorf("expected 1 file, got %d", len(files))
    }

    file := files[0].(map[string]interface{})
    if file["sha256"] == "" || file["sha256"] == nil {
        t.Error("sha256 is empty")
    }
    if file["status"] != "ok" {
        t.Errorf("expected status ok, got %v", file["status"])
    }
}
```

**Step 2: Test — FAIL**

```bash
go test ./internal/backup/...
```

**Step 3: Manifest implementieren**

```go
// internal/backup/manifest.go
package backup

import (
    "crypto/sha256"
    "encoding/hex"
    "encoding/json"
    "fmt"
    "io"
    "os"
    "path/filepath"
    "time"
)

type FileEntry struct {
    Name    string `json:"name"`
    SHA256  string `json:"sha256"`
    Records int    `json:"records,omitempty"`
    Status  string `json:"status"` // "ok" or "failed"
    Error   string `json:"error,omitempty"`
}

type Summary struct {
    TotalFiles  int `json:"total_files"`
    Successful  int `json:"successful"`
    Failed      int `json:"failed"`
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
    return m.addEntry(path, "ok", "", 0)
}

func (m *Manifest) AddFailedFile(name string, err error) {
    m.Files = append(m.Files, FileEntry{
        Name:   name + ".json",
        Status: "failed",
        Error:  err.Error(),
    })
}

func (m *Manifest) addEntry(path, status, errMsg string, records int) error {
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
        Name:    filepath.Base(path),
        SHA256:  hex.EncodeToString(h.Sum(nil)),
        Status:  status,
        Error:   errMsg,
        Records: records,
    })
    return nil
}

func (m *Manifest) Write(path string) error {
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
    return os.WriteFile(path, data, 0640)
}
```

**Step 4: Tests — PASS**

```bash
go test -v ./internal/backup/...
```

**Step 5: Commit**

```bash
git add internal/backup/manifest.go internal/backup/manifest_test.go
git commit -m "feat: add SHA256 manifest writer"
```

---

## Task 8: Backup Runner (Concurrent Fetcher)

**Files:**
- Create: `internal/backup/runner.go`
- Create: `internal/backup/runner_test.go`

**Step 1: Failing test**

```go
// internal/backup/runner_test.go
package backup_test

import (
    "context"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "os"
    "path/filepath"
    "testing"
    "time"

    "github.com/kAYd9iN/holaspirit-backup/internal/api"
    "github.com/kAYd9iN/holaspirit-backup/internal/backup"
    "github.com/kAYd9iN/holaspirit-backup/internal/storage"
)

func TestRunner_FetchesAllEndpoints(t *testing.T) {
    fetched := make(map[string]bool)

    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        resp := map[string]interface{}{
            "data": []interface{}{},
            "meta": map[string]interface{}{
                "pagination": map[string]interface{}{
                    "current_page": 1,
                    "total_pages":  1,
                },
            },
        }
        json.NewEncoder(w).Encode(resp)
    }))
    defer srv.Close()

    client := api.NewClient(srv.URL, "api:test")
    client.RetryDelay = 1 * time.Millisecond

    tmpDir := t.TempDir()
    w, _ := storage.NewWriter(tmpDir, time.Now())

    endpoints := []api.Endpoint{
        {Name: "circles", Path: "/api/organizations/org1/circles", Paginated: true},
        {Name: "roles", Path: "/api/organizations/org1/roles", Paginated: true},
    }

    results := backup.RunFetchers(context.Background(), client, w, endpoints)

    for _, r := range results {
        fetched[r.Name] = r.Err == nil
    }

    if !fetched["circles"] || !fetched["roles"] {
        t.Errorf("not all endpoints fetched: %v", fetched)
    }

    if _, err := os.Stat(filepath.Join(w.Dir(), "circles.json")); os.IsNotExist(err) {
        t.Error("circles.json not created")
    }
}
```

**Step 2: Test — FAIL**

```bash
go test ./internal/backup/... -run TestRunner
```

**Step 3: Runner implementieren**

```go
// internal/backup/runner.go
package backup

import (
    "context"
    "encoding/json"
    "fmt"
    "sync"

    "github.com/kAYd9iN/holaspirit-backup/internal/api"
    "github.com/kAYd9iN/holaspirit-backup/internal/storage"
)

// Result holds the outcome of fetching one endpoint.
type Result struct {
    Name    string
    Records int
    Err     error
}

// RunFetchers concurrently fetches all endpoints and writes JSON files.
func RunFetchers(ctx context.Context, client *api.Client, w *storage.Writer, endpoints []api.Endpoint) []Result {
    results := make([]Result, len(endpoints))
    var wg sync.WaitGroup

    for i, ep := range endpoints {
        wg.Add(1)
        go func(idx int, endpoint api.Endpoint) {
            defer wg.Done()
            results[idx] = fetch(ctx, client, w, endpoint)
        }(i, ep)
    }

    wg.Wait()
    return results
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

**Step 4: Tests — PASS**

```bash
go test -v ./internal/backup/... -run TestRunner
```

**Step 5: Commit**

```bash
git add internal/backup/runner.go internal/backup/runner_test.go
git commit -m "feat: add concurrent backup runner"
```

---

## Task 9: Async Exports (PDF + XLSX)

**Files:**
- Create: `internal/backup/exports.go`
- Create: `internal/backup/exports_test.go`

**Step 1: Failing test**

```go
// internal/backup/exports_test.go
package backup_test

import (
    "context"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "testing"
    "time"

    "github.com/kAYd9iN/holaspirit-backup/internal/api"
    "github.com/kAYd9iN/holaspirit-backup/internal/backup"
    "github.com/kAYd9iN/holaspirit-backup/internal/storage"
)

func TestExports_PDFAndXLSX(t *testing.T) {
    callCount := 0
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        switch r.Method + " " + r.URL.Path {
        case "POST /api/async/organizations/org1/pdf",
             "POST /api/async/organizations/org1/spreadsheet":
            json.NewEncoder(w).Encode(map[string]interface{}{
                "data": map[string]string{"id": "job123"},
            })
        case "GET /api/export/organizations/org1/jobs/job123/download":
            callCount++
            if callCount < 2 {
                // Simulate pending
                w.WriteHeader(http.StatusAccepted)
                return
            }
            w.Header().Set("Content-Type", "application/octet-stream")
            w.Write([]byte("FAKE_FILE_CONTENT"))
        default:
            w.WriteHeader(http.StatusNotFound)
        }
    }))
    defer srv.Close()

    client := api.NewClient(srv.URL, "api:test")
    tmpDir := t.TempDir()
    writer, _ := storage.NewWriter(tmpDir, time.Now())

    exporter := backup.NewExporter(client, "org1")
    exporter.PollInterval = 1 * time.Millisecond

    if err := exporter.Run(context.Background(), writer); err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
}
```

**Step 2: Test — FAIL**

```bash
go test ./internal/backup/... -run TestExports
```

**Step 3: Exports implementieren**

```go
// internal/backup/exports.go
package backup

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "time"

    "github.com/kAYd9iN/holaspirit-backup/internal/api"
    "github.com/kAYd9iN/holaspirit-backup/internal/storage"
)

type Exporter struct {
    client       *api.Client
    orgID        string
    PollInterval time.Duration
}

func NewExporter(client *api.Client, orgID string) *Exporter {
    return &Exporter{
        client:       client,
        orgID:        orgID,
        PollInterval: 5 * time.Second,
    }
}

func (e *Exporter) Run(ctx context.Context, w *storage.Writer) error {
    types := []struct{ name, endpoint, filename string }{
        {"pdf", fmt.Sprintf("/api/async/organizations/%s/pdf", e.orgID), "export.pdf"},
        {"spreadsheet", fmt.Sprintf("/api/async/organizations/%s/spreadsheet", e.orgID), "export.xlsx"},
    }

    for _, t := range types {
        jobID, err := e.triggerExport(ctx, t.endpoint)
        if err != nil {
            return fmt.Errorf("trigger %s export: %w", t.name, err)
        }

        data, err := e.pollAndDownload(ctx, jobID)
        if err != nil {
            return fmt.Errorf("download %s: %w", t.name, err)
        }

        if err := w.WriteFile(t.filename, data); err != nil {
            return fmt.Errorf("write %s: %w", t.filename, err)
        }
    }
    return nil
}

func (e *Exporter) triggerExport(ctx context.Context, path string) (string, error) {
    req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.client.BaseURL()+path, bytes.NewReader([]byte("{}")))
    if err != nil {
        return "", err
    }
    req.Header.Set("Authorization", "Bearer "+e.client.Token())
    req.Header.Set("Content-Type", "application/json")

    resp, err := http.DefaultClient.Do(req)
    if err != nil {
        return "", err
    }
    defer resp.Body.Close()

    var result struct {
        Data struct {
            ID string `json:"id"`
        } `json:"data"`
    }
    if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
        return "", err
    }
    return result.Data.ID, nil
}

func (e *Exporter) pollAndDownload(ctx context.Context, jobID string) ([]byte, error) {
    downloadPath := fmt.Sprintf("/api/export/organizations/%s/jobs/%s/download", e.orgID, jobID)

    for {
        select {
        case <-ctx.Done():
            return nil, ctx.Err()
        case <-time.After(e.PollInterval):
        }

        body, err := e.client.Get(ctx, downloadPath)
        if err != nil {
            // 202 Accepted means still processing — retry
            continue
        }
        return body, nil
    }
}
```

**Step 4: client.go um BaseURL() und Token() Accessors erweitern**

```go
// In internal/api/client.go hinzufuegen:
func (c *Client) BaseURL() string { return c.baseURL }
func (c *Client) Token() string   { return c.token }
```

**Step 5: Tests — PASS**

```bash
go test -v ./internal/backup/... -run TestExports
```

**Step 6: Commit**

```bash
git add internal/backup/exports.go internal/backup/exports_test.go internal/api/client.go
git commit -m "feat: add async PDF and XLSX export support"
```

---

## Task 10: CLI (main.go)

**Files:**
- Modify: `cmd/backup/main.go`

**Step 1: main.go komplett schreiben**

```go
// cmd/backup/main.go
package main

import (
    "context"
    "flag"
    "fmt"
    "log"
    "os"
    "time"

    "github.com/kAYd9iN/holaspirit-backup/internal/api"
    "github.com/kAYd9iN/holaspirit-backup/internal/backup"
    "github.com/kAYd9iN/holaspirit-backup/internal/storage"
)

const version = "1.0.0"
const holaspiritBaseURL = "https://app.holaspirit.com"

func main() {
    outputDir := flag.String("output", "./backup", "Backup destination directory")
    orgID     := flag.String("org-id", "", "Organization ID (auto-detected if empty)")
    dryRun    := flag.Bool("dry-run", false, "Test connection without writing files")
    showVer   := flag.Bool("version", false, "Show version")
    flag.Parse()

    if *showVer {
        fmt.Printf("holaspirit-backup v%s\n", version)
        os.Exit(0)
    }

    log.SetFlags(log.Ldate | log.Ltime)

    // Load token
    token, err := getToken()
    if err != nil {
        log.Fatalf("ERROR: %v\n\nRun: cmdkey /generic:holaspirit-backup /user:api /pass:api:YOUR_TOKEN", err)
    }

    client := api.NewClient(holaspiritBaseURL, token)
    ctx := context.Background()

    // Discover org ID if not provided
    if *orgID == "" {
        id, err := api.DiscoverOrgID(ctx, client)
        if err != nil {
            log.Fatalf("ERROR discovering organization ID: %v", err)
        }
        *orgID = id
        log.Printf("Organization ID: %s", *orgID)
    }

    if *dryRun {
        log.Println("Dry run successful — connection OK")
        os.Exit(0)
    }

    ts := time.Now()
    w, err := storage.NewWriter(*outputDir, ts)
    if err != nil {
        log.Fatalf("ERROR creating backup directory: %v", err)
    }
    log.Printf("Backup directory: %s", w.Dir())

    endpoints := api.AllEndpoints(*orgID)
    log.Printf("Fetching %d endpoints concurrently...", len(endpoints))

    manifest := backup.NewManifest(*orgID, version, ts)
    results := backup.RunFetchers(ctx, client, w, endpoints)

    for _, r := range results {
        if r.Err != nil {
            log.Printf("WARN: %s failed: %v", r.Name, r.Err)
            manifest.AddFailedFile(r.Name, r.Err)
        } else {
            log.Printf("OK: %s (%d records)", r.Name, r.Records)
            manifest.AddFile(w.Dir() + "/" + r.Name + ".json")
        }
    }

    log.Println("Running async exports (PDF + XLSX)...")
    exporter := backup.NewExporter(client, *orgID)
    if err := exporter.Run(ctx, w); err != nil {
        log.Printf("WARN: async exports failed: %v", err)
    }

    manifestPath := w.Dir() + "/backup-manifest.json"
    if err := manifest.Write(manifestPath); err != nil {
        log.Fatalf("ERROR writing manifest: %v", err)
    }

    log.Printf("Manifest written: %s", manifestPath)
    log.Printf("Backup complete: %s", w.Dir())
}
```

**Step 2: DiscoverOrgID in api/endpoints.go hinzufuegen**

```go
// In internal/api/endpoints.go hinzufuegen:
func DiscoverOrgID(ctx context.Context, client *Client) (string, error) {
    body, err := client.Get(ctx, "/api/me")
    if err != nil {
        return "", fmt.Errorf("GET /api/me: %w", err)
    }
    var me MeResponse
    if err := json.Unmarshal(body, &me); err != nil {
        return "", fmt.Errorf("parse /api/me: %w", err)
    }
    orgID := me.Data.Relationships.Organization.Data.ID
    if orgID == "" {
        return "", fmt.Errorf("organization ID not found in /api/me response")
    }
    return orgID, nil
}
```

Außerdem `context` und `encoding/json` in den Imports von endpoints.go ergaenzen.

**Step 3: getToken() — plattformspezifisch**

```go
// cmd/backup/token_windows.go
//go:build windows

package main

import "github.com/kAYd9iN/holaspirit-backup/internal/credentials"

func getToken() (string, error) {
    return credentials.NewWinCredManager().GetToken()
}
```

```go
// cmd/backup/token_other.go
//go:build !windows

package main

import (
    "fmt"
    "os"
)

func getToken() (string, error) {
    token := os.Getenv("HOLASPIRIT_TOKEN")
    if token == "" {
        return "", fmt.Errorf("HOLASPIRIT_TOKEN environment variable not set (Windows Credential Manager not available on this platform)")
    }
    return token, nil
}
```

**Step 4: Kompilieren testen (Linux)**

```bash
go build ./cmd/backup/
```
Expected: kein Fehler

**Step 5: Windows-Cross-Compile testen**

```bash
GOOS=windows GOARCH=amd64 go build -o backup.exe ./cmd/backup/
```
Expected: `backup.exe` erstellt, kein Fehler

**Step 6: Commit**

```bash
git add cmd/backup/ internal/api/endpoints.go
git commit -m "feat: add CLI with flag parsing and platform-aware token loading"
```

---

## Task 11: GitHub Actions CI

**Files:**
- Create: `.github/workflows/ci.yml`

**Step 1: CI-Workflow schreiben**

```yaml
# .github/workflows/ci.yml
name: CI

on:
  push:
    branches: [main]
  pull_request:
    branches: [main]

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version: '1.22'

      - name: Verify dependencies
        run: go mod verify

      - name: govulncheck
        run: go run golang.org/x/vuln/cmd/govulncheck@latest ./...

      - name: gosec
        run: go run github.com/securego/gosec/v2/cmd/gosec@latest -quiet ./...

      - name: Run tests
        run: go test -race -cover ./...

      - name: Build Windows binary
        run: GOOS=windows GOARCH=amd64 go build -o backup.exe ./cmd/backup/

      - name: Upload artifact
        uses: actions/upload-artifact@v4
        if: github.ref == 'refs/heads/main'
        with:
          name: backup-windows-amd64
          path: backup.exe
```

**Step 2: Commit & Push**

```bash
git add .github/
git commit -m "ci: add GitHub Actions workflow with security scanning"
git push
```

**Step 3: CI-Status pruefen**

Auf GitHub: Actions-Tab oeffnen, sicherstellen dass alle Jobs gruен sind.

---

## Task 12: Vendor-Modus aktivieren & finaler Push

**Step 1: Vendor-Verzeichnis erstellen**

```bash
go mod vendor
```

**Step 2: .gitignore anpassen**

```
backup/
*.exe
# vendor/ NICHT ignorieren — absichtlich eingecheckt fuer Supply-Chain-Sicherheit
```

**Step 3: Alle Tests nochmals ausfuehren**

```bash
go test ./...
```
Expected: alle PASS

**Step 4: govulncheck lokal**

```bash
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
```

**Step 5: Finaler Commit**

```bash
git add vendor/ go.mod go.sum .gitignore
git commit -m "chore: vendor all dependencies for supply chain security"
git push
```

---

## Task 13: README schreiben

**Files:**
- Create: `README.md`

**Step 1: README inhalt**

```markdown
# holaspirit-backup

Automated backup tool for Holaspirit organization data. Fetches all API endpoints concurrently, writes JSON files with SHA256 integrity manifest.

## Requirements

- Windows 10/11 or Windows Server 2019+
- Network access to `app.holaspirit.com` (HTTPS/443)
- Holaspirit Read-Only API Token

## Setup

### 1. Store API token (run once as Administrator)

```powershell
cmdkey /generic:holaspirit-backup /user:api /pass:api:YOUR_TOKEN_HERE
```

### 2. Run backup

```powershell
.\backup.exe --output C:\Backups\holaspirit
```

### 3. Schedule daily (PowerShell as Administrator)

```powershell
$action = New-ScheduledTaskAction -Execute "C:\Tools\holaspirit-backup\backup.exe" -Argument "--output C:\Backups\holaspirit"
$trigger = New-ScheduledTaskTrigger -Daily -At "02:00"
Register-ScheduledTask -TaskName "Holaspirit Backup" -Action $action -Trigger $trigger -RunLevel Highest
```

## Options

```
--output PATH   Backup destination (default: ./backup)
--org-id ID     Organization ID (auto-detected)
--dry-run       Test connection only
--version       Show version
```

## Output

```
backup/2026-03-06T02-00-00/
  circles.json, roles.json, members.json, ...
  export.xlsx, export.pdf
  backup-manifest.json  (SHA256 hashes)
  backup.log
```

## Development

```bash
go test ./...
GOOS=windows GOARCH=amd64 go build -o backup.exe ./cmd/backup/
```

## Documentation

Full documentation: https://ewigepluseins.atlassian.net/wiki/spaces/HB
```

**Step 2: Commit**

```bash
git add README.md
git commit -m "docs: add README with setup and usage instructions"
git push
```

---

## Abschlussuebersicht

| Task | Inhalt | Status |
|---|---|---|
| 1 | Projekt-Bootstrap, GitHub Repo | - |
| 2 | Credential Manager Interface + wincred | - |
| 3 | HTTP-Client, Rate-Limiter, Retry | - |
| 4 | Paginierung | - |
| 5 | Endpoint-Definitionen (21 Endpoints) | - |
| 6 | Storage Writer | - |
| 7 | SHA256 Manifest | - |
| 8 | Concurrent Runner | - |
| 9 | Async Exports (PDF + XLSX) | - |
| 10 | CLI (main.go + platform token) | - |
| 11 | GitHub Actions CI | - |
| 12 | Vendor-Modus | - |
| 13 | README | - |
