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

	manifestPath := filepath.Join(dir, "backup-manifest.json")
	if err := backup.VerifyManifest(manifestPath, token); err != nil {
		slog.Error("verification FAILED", "dir", dir, "error", err)
		fmt.Fprintln(os.Stderr, "TAMPERED or invalid token — do not trust this backup.")
		return 1
	}

	slog.Info("manifest OK — backup integrity verified", "dir", dir)
	return 0
}
