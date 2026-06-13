package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/kAYd9iN/holaspirit-backup/internal/backup"
)

// runVerify implements: backup.exe verify --dir <path>
// Returns an exit code (0=OK, 1=tampered/wrong token, 2=fatal error).
func runVerify(dir string) int {
	token, err := getToken()
	if err != nil {
		slog.Error("loading token", "error", err)
		return 2
	}

	// Resolve --dir to an absolute, symlink-free path and require it to be a
	// real directory before reading from it (issue #21). This prevents the
	// verify path from being redirected through a symlink to a sensitive
	// location and gives a clear error instead of a confusing read failure.
	cleanDir, err := filepath.Abs(filepath.Clean(dir))
	if err != nil {
		slog.Error("invalid --dir", "dir", sanitizeLog(dir), "error", err)
		return 2
	}
	resolved, err := filepath.EvalSymlinks(cleanDir)
	if err != nil {
		slog.Error("cannot resolve --dir", "dir", sanitizeLog(dir), "error", err)
		return 2
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		slog.Error("--dir is not a directory", "dir", sanitizeLog(dir))
		return 2
	}
	dir = resolved

	manifestPath := filepath.Join(dir, "backup-manifest.json")
	if err := backup.VerifyManifest(manifestPath, token); err != nil {
		slog.Error("verification FAILED", "dir", dir, "error", err)
		fmt.Fprintln(os.Stderr, "TAMPERED or invalid token — do not trust this backup.")
		return 1
	}

	slog.Info("manifest OK — backup integrity verified", "dir", dir)
	return 0
}
