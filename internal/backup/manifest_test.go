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
