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
	// #nosec G304 -- path is constructed internally from the backup output directory,
	// never from user input. All callers use filepath.Join(w.Dir(), filename).
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
	return os.WriteFile(path, data, 0600)
}
