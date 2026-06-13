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

// unsafeChars matches any character that is not alphanumeric, underscore, hyphen, or dot.
// Dots are allowed for file extensions (e.g. "export.pdf"); path traversal via ".."
// is caught by the filepath.Rel containment check in each write method.
var unsafeChars = regexp.MustCompile(`[^a-zA-Z0-9_\-\.]`)

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
	// Symlink hardening (issue #17): resolve the operator-supplied baseDir
	// through any symlinks before writing into it. If baseDir is a symlink
	// to a sensitive location (e.g. /etc), the backup must not follow it.
	// A not-yet-existing baseDir is fine — only an existing symlink target is
	// resolved and re-checked.
	if resolved, err := filepath.EvalSymlinks(baseDir); err == nil {
		baseDir = resolved
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("resolve output dir %q: %w", baseDir, err)
	}

	dir := filepath.Join(baseDir, ts.UTC().Format("2006-01-02T15-04-05"))
	if err := os.MkdirAll(dir, 0750); err != nil {
		return nil, fmt.Errorf("create backup dir: %w", err)
	}

	// Re-resolve the created directory and confirm it still sits under the
	// intended base — guards against a symlink swapped in between the checks.
	resolvedDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve backup dir: %w", err)
	}
	resolvedBase, err := filepath.EvalSymlinks(baseDir)
	if err != nil {
		return nil, fmt.Errorf("resolve base dir: %w", err)
	}
	rel, err := filepath.Rel(resolvedBase, resolvedDir)
	if err != nil || isOutsideDir(rel) {
		return nil, fmt.Errorf("backup dir %q escapes base %q after symlink resolution", resolvedDir, resolvedBase)
	}

	return &Writer{dir: resolvedDir}, nil
}

func (w *Writer) Dir() string { return w.dir }

// isOutsideDir returns true when a filepath.Rel result indicates the path
// escapes the base directory. It checks for the path component ".." rather
// than a simple string prefix, so sanitized names like ".._.._foo" are not
// falsely flagged.
func isOutsideDir(rel string) bool {
	return rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// WriteJSON sanitizes name, pretty-prints data, and writes it to <name>.json
// within the backup directory. Path traversal attempts are blocked by sanitization
// and a belt-and-suspenders containment check.
func (w *Writer) WriteJSON(name string, data []byte) error {
	safe := sanitizeName(name)
	dest := filepath.Join(w.dir, safe+".json")

	// Belt-and-suspenders: verify destination is inside the backup directory.
	rel, err := filepath.Rel(w.dir, dest)
	if err != nil || isOutsideDir(rel) {
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
	dest := filepath.Join(w.dir, safe)

	// Belt-and-suspenders: verify destination is inside the backup directory.
	rel, err := filepath.Rel(w.dir, dest)
	if err != nil || isOutsideDir(rel) {
		return fmt.Errorf("path traversal detected for name %q", name)
	}

	return os.WriteFile(dest, data, 0600)
}
