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
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create backup dir: %w", err)
	}
	return &Writer{dir: dir}, nil
}

func (w *Writer) Dir() string { return w.dir }

// WriteJSON pretty-prints data and writes it to <name>.json
func (w *Writer) WriteJSON(name string, data []byte) error {
	var v interface{}
	if err := json.Unmarshal(data, &v); err != nil {
		// not JSON, write as-is
		return os.WriteFile(filepath.Join(w.dir, name+".json"), data, 0600)
	}
	pretty, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(w.dir, name+".json"), pretty, 0600)
}

// WriteFile writes raw bytes to a file.
func (w *Writer) WriteFile(name string, data []byte) error {
	return os.WriteFile(filepath.Join(w.dir, name), data, 0600)
}
