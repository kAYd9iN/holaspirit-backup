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
	"regexp"
	"strings"
	"time"
)

// maxManifestFileBytes caps how much of a backup file is read for hashing,
// so an unexpectedly large file cannot exhaust memory (issue #14). It matches
// the client's per-response body limit.
const maxManifestFileBytes = 100 * 1024 * 1024 // 100 MiB

// errorSanitizer strips ASCII control characters and ANSI escapes from error
// strings before they are stored in the manifest, preventing log/JSON injection
// and reducing the chance of leaking unexpected response content (issue #15).
var errorSanitizer = regexp.MustCompile(`[\x00-\x1f\x7f]|\x1b\[[0-9;]*[a-zA-Z]`)

// sanitizeError makes an error string safe to persist: control characters are
// replaced and the message is truncated to a sane length.
func sanitizeError(err error) string {
	if err == nil {
		return ""
	}
	s := errorSanitizer.ReplaceAllString(err.Error(), "?")
	const maxLen = 512
	if len(s) > maxLen {
		s = s[:maxLen] + "…(truncated)"
	}
	return s
}

// FileEntry records integrity data for one backup file.
type FileEntry struct {
	Name    string `json:"name"`
	SHA256  string `json:"sha256"`
	Records int    `json:"records,omitempty"`
	Status  string `json:"status"` // "ok" or "failed"
	Error   string `json:"error,omitempty"`
}

// Summary aggregates the backup run outcome.
type Summary struct {
	TotalFiles int `json:"total_files"`
	Successful int `json:"successful"`
	Failed     int `json:"failed"`
}

// AuditInfo records who ran a backup and from where. It is populated only when
// the operator opts in via --audit (issue #33); it is omitted by default to
// avoid embedding host/user identifiers in every backup (issue #28).
type AuditInfo struct {
	Hostname string `json:"hostname,omitempty"`
	User     string `json:"user,omitempty"`
	Automated bool  `json:"automated,omitempty"`
}

// Manifest records the full backup run metadata and per-file integrity hashes.
type Manifest struct {
	Timestamp      time.Time   `json:"timestamp"`
	ToolVersion    string      `json:"tool_version"`
	OrganizationID string      `json:"organization_id"`
	Audit          *AuditInfo  `json:"audit,omitempty"`
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

// SetAudit attaches optional audit metadata (issue #33). No-op for nil.
func (m *Manifest) SetAudit(a *AuditInfo) {
	m.Audit = a
}

// AddFile hashes the file at path and records it as a successful entry.
func (m *Manifest) AddFile(path string) error {
	f, err := os.Open(path) // #nosec G304 — path is always an internally constructed backup path
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, io.LimitReader(f, maxManifestFileBytes)); err != nil {
		return fmt.Errorf("hash %s: %w", path, err)
	}

	m.Files = append(m.Files, FileEntry{
		Name:   filepath.Base(path),
		SHA256: hex.EncodeToString(h.Sum(nil)),
		Status: "ok",
	})
	return nil
}

// AddFailedFile records an endpoint that could not be fetched.
func (m *Manifest) AddFailedFile(name string, err error) {
	m.Files = append(m.Files, FileEntry{
		Name:   name + ".json",
		Status: "failed",
		Error:  sanitizeError(err),
	})
}

// Write serializes the manifest to path and writes an HMAC-SHA-256 signature
// to the corresponding .sig file. The token is never written to disk.
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

	if err := os.WriteFile(path, data, 0600); err != nil {
		return err
	}

	sig := computeHMAC(data, token)
	sigPath := strings.TrimSuffix(path, ".json") + ".sig"
	return os.WriteFile(sigPath, []byte(sig), 0600)
}

// VerifyManifest checks the HMAC-SHA-256 signature of a manifest.
// Returns nil if valid, error if tampered or wrong token.
func VerifyManifest(manifestPath, token string) error {
	data, err := os.ReadFile(manifestPath) // #nosec G304 — path comes from CLI flag, not raw user input
	if err != nil {
		return fmt.Errorf("read manifest: %w", err)
	}

	sigPath := strings.TrimSuffix(manifestPath, ".json") + ".sig"
	sigBytes, err := os.ReadFile(sigPath) // #nosec G304
	if err != nil {
		return fmt.Errorf("read sig: %w", err)
	}

	// Compare the raw HMAC bytes, not the hex strings (issue #13). Decoding
	// first means a stray trailing newline or whitespace in the .sig file
	// yields a clear "malformed signature" error instead of a misleading
	// "tampered" verdict, and the constant-time compare operates on the
	// actual MAC bytes.
	expectedBytes, err := hex.DecodeString(computeHMAC(data, token))
	if err != nil {
		return fmt.Errorf("compute expected signature: %w", err)
	}
	storedBytes, err := hex.DecodeString(strings.TrimSpace(string(sigBytes)))
	if err != nil {
		return fmt.Errorf("malformed signature file (%s): not valid hex", sigPath)
	}
	if !hmac.Equal(expectedBytes, storedBytes) {
		return fmt.Errorf("manifest signature mismatch — backup may have been tampered with")
	}
	return nil
}

// computeHMAC derives a domain-separated key from the token and computes
// HMAC-SHA-256 over data. The token itself is never stored or returned.
func computeHMAC(data []byte, token string) string {
	keyHash := sha256.Sum256([]byte("holaspirit-backup-manifest\x00" + token))
	mac := hmac.New(sha256.New, keyHash[:])
	mac.Write(data)
	return hex.EncodeToString(mac.Sum(nil))
}
