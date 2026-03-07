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

// Manifest records the full backup run metadata and per-file integrity hashes.
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

// AddFile hashes the file at path and records it as a successful entry.
func (m *Manifest) AddFile(path string) error {
	f, err := os.Open(path) // #nosec G304 — path is always an internally constructed backup path
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

// AddFailedFile records an endpoint that could not be fetched.
func (m *Manifest) AddFailedFile(name string, err error) {
	m.Files = append(m.Files, FileEntry{
		Name:   name + ".json",
		Status: "failed",
		Error:  err.Error(),
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

	expected := computeHMAC(data, token)
	if !hmac.Equal([]byte(expected), sigBytes) {
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
