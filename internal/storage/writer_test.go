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
