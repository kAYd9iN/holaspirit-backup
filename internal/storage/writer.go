package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// unsafeChars matches any character that is not alphanumeric, underscore, or hyphen.
var unsafeChars = regexp.MustCompile(`[^a-zA-Z0-9_\-]`)

// sanitizeName replaces all unsafe characters with underscores, preventing
// path traversal via endpoint names.
func sanitizeName(name string) string {
	return unsafeChars.ReplaceAllString(name, "_")
}

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

// WriteJSON sanitizes name, pretty-prints data, and writes it to <name>.json
// within the backup directory. Path traversal attempts are blocked by sanitization
// and a belt-and-suspenders containment check.
func (w *Writer) WriteJSON(name string, data []byte) error {
	safe := sanitizeName(name)
	dest := filepath.Join(w.dir, safe+".json")

	// Belt-and-suspenders: verify destination is inside the backup directory.
	rel, err := filepath.Rel(w.dir, dest)
	if err != nil || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("path traversal detected for name %q", name)
	}

	var v interface{}
	if err := json.Unmarshal(data, &v); err != nil {
		// Not JSON — write as-is
		return os.WriteFile(dest, data, 0600)
	}
	pretty, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(dest, pretty, 0600)
}

// WriteFile sanitizes name and writes raw bytes to a file within the backup directory.
func (w *Writer) WriteFile(name string, data []byte) error {
	safe := sanitizeName(name)
	return os.WriteFile(filepath.Join(w.dir, safe), data, 0600)
}
