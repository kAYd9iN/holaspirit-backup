package backup_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
	if err := m.Write(outPath, "api:test-token"); err != nil {
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

func TestManifestHMACRoundTrip(t *testing.T) {
	dir := t.TempDir()
	m := backup.NewManifest("org123", "1.0.0", time.Now())

	fpath := filepath.Join(dir, "circles.json")
	if err := os.WriteFile(fpath, []byte(`[]`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.AddFile(fpath); err != nil {
		t.Fatal(err)
	}

	token := "api:test-token-xyz"
	manifestPath := filepath.Join(dir, "backup-manifest.json")

	if err := m.Write(manifestPath, token); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// sig file must exist
	sigPath := filepath.Join(dir, "backup-manifest.sig")
	if _, err := os.Stat(sigPath); err != nil {
		t.Fatalf("sig file missing: %v", err)
	}

	// Verify must pass with correct token
	if err := backup.VerifyManifest(manifestPath, token); err != nil {
		t.Errorf("VerifyManifest with correct token failed: %v", err)
	}

	// Wrong token must fail
	if err := backup.VerifyManifest(manifestPath, "wrong-token"); err == nil {
		t.Error("expected failure with wrong token")
	}

	// Tamper with manifest — verify must fail
	data, _ := os.ReadFile(manifestPath)
	tampered := append(data, []byte("\n// tampered")...)
	if err := os.WriteFile(manifestPath, tampered, 0600); err != nil {
		t.Fatal(err)
	}
	if err := backup.VerifyManifest(manifestPath, token); err == nil {
		t.Error("expected failure after tampering")
	}
}

func TestManifestTokenNotInOutput(t *testing.T) {
	dir := t.TempDir()
	m := backup.NewManifest("org123", "1.0.0", time.Now())
	token := "api:super-secret-token"

	manifestPath := filepath.Join(dir, "backup-manifest.json")
	if err := m.Write(manifestPath, token); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(manifestPath)
	if strings.Contains(string(data), token) {
		t.Error("token found in manifest.json")
	}
	if strings.Contains(string(data), "super-secret") {
		t.Error("token substring found in manifest.json")
	}

	sigPath := filepath.Join(dir, "backup-manifest.sig")
	sig, _ := os.ReadFile(sigPath)
	if strings.Contains(string(sig), token) {
		t.Error("token found in manifest.sig")
	}
	if strings.Contains(string(sig), "super-secret") {
		t.Error("token substring found in manifest.sig")
	}
}

func TestVerifyManifest_RejectsMalformedSignature(t *testing.T) {
	dir := t.TempDir()
	mPath := filepath.Join(dir, "backup-manifest.json")
	if err := os.WriteFile(mPath, []byte(`{"files":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	// .sig with non-hex content must yield a clear "malformed" error, not a
	// silent "tampered" verdict (issue #13).
	if err := os.WriteFile(filepath.Join(dir, "backup-manifest.sig"), []byte("not-hex-!!"), 0600); err != nil {
		t.Fatal(err)
	}
	err := backup.VerifyManifest(mPath, "tok")
	if err == nil || !strings.Contains(err.Error(), "malformed signature") {
		t.Errorf("expected malformed-signature error, got: %v", err)
	}
}

func TestVerifyManifest_ToleratesTrailingNewlineInSig(t *testing.T) {
	dir := t.TempDir()
	m := backup.NewManifest("org", "test", time.Now())
	mPath := filepath.Join(dir, "backup-manifest.json")
	if err := m.Write(mPath, "tok"); err != nil {
		t.Fatal(err)
	}
	// Append a trailing newline to the .sig — a common artifact of text tools.
	sigPath := filepath.Join(dir, "backup-manifest.sig")
	sig, _ := os.ReadFile(sigPath)
	if err := os.WriteFile(sigPath, append(sig, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	if err := backup.VerifyManifest(mPath, "tok"); err != nil {
		t.Errorf("trailing newline in sig should still verify, got: %v", err)
	}
}

func TestAddFailedFile_SanitizesControlChars(t *testing.T) {
	dir := t.TempDir()
	m := backup.NewManifest("org", "test", time.Now())
	m.AddFailedFile("evil", fmtErr("line1\nFAKE: injected\x1b[31m"))
	mPath := filepath.Join(dir, "backup-manifest.json")
	if err := m.Write(mPath, "tok"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(mPath)
	// The stored error must not contain raw newlines or escape bytes (issue #15/#18).
	var parsed struct {
		Files []struct {
			Error string `json:"error"`
		} `json:"files"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}
	if len(parsed.Files) != 1 {
		t.Fatalf("expected 1 file entry, got %d", len(parsed.Files))
	}
	if strings.ContainsAny(parsed.Files[0].Error, "\n\x1b\x00") {
		t.Errorf("error string not sanitized: %q", parsed.Files[0].Error)
	}
}

type fmtErr string

func (e fmtErr) Error() string { return string(e) }
