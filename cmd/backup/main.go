package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/kAYd9iN/holaspirit-backup/internal/api"
	"github.com/kAYd9iN/holaspirit-backup/internal/backup"
	"github.com/kAYd9iN/holaspirit-backup/internal/storage"
)

const (
	version           = "1.0.0"
	holaspiritBaseURL = "https://app.holaspirit.com"
)

func main() {
	// Subcommand dispatch
	if len(os.Args) > 1 && os.Args[1] == "verify" {
		fs := flag.NewFlagSet("verify", flag.ExitOnError)
		dir := fs.String("dir", "", "Backup directory to verify (required)")
		fs.Parse(os.Args[2:]) //nolint:errcheck
		if *dir == "" {
			fmt.Fprintln(os.Stderr, "usage: backup verify --dir <path>")
			os.Exit(2)
		}
		os.Exit(runVerify(*dir))
	}

	// Main backup command
	outputDir := flag.String("output", "./backup", "Backup destination directory")
	orgID := flag.String("org-id", "", "Organization ID (auto-detected if empty)")
	dryRun := flag.Bool("dry-run", false, "Test connection without writing files")
	showVer := flag.Bool("version", false, "Show version and exit")
	timeoutMin := flag.Int("timeout", 120, "Overall timeout in minutes (0 = no timeout)")
	flag.Parse()

	if *showVer {
		fmt.Printf("holaspirit-backup v%s\n", version)
		os.Exit(0)
	}

	token, err := getToken()
	if err != nil {
		slog.Error("loading token", "error", err,
			"hint", "On Windows: cmdkey /generic:holaspirit-backup /user:api /pass:api:TOKEN")
		os.Exit(2)
	}

	client := api.NewClient(holaspiritBaseURL, token)

	ctx := context.Background()
	if *timeoutMin > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(*timeoutMin)*time.Minute)
		defer cancel()
	}

	if *orgID == "" {
		id, err := api.DiscoverOrgID(ctx, client)
		if err != nil {
			slog.Error("discovering organization ID", "error", err)
			os.Exit(2)
		}
		*orgID = id
		slog.Info("organization discovered", "id", *orgID)
	}

	if *dryRun {
		slog.Info("dry run successful — connection OK")
		os.Exit(0)
	}

	ts := time.Now()
	w, err := storage.NewWriter(*outputDir, ts)
	if err != nil {
		slog.Error("creating backup directory", "error", err)
		os.Exit(2)
	}
	slog.Info("backup directory created", "path", w.Dir())

	endpoints := api.AllEndpoints(*orgID)
	slog.Info("fetching endpoints", "count", len(endpoints))

	manifest := backup.NewManifest(*orgID, version, ts)
	results := backup.RunFetchers(ctx, client, w, endpoints)

	exitCode := 0
	for _, r := range results {
		if r.Err != nil {
			slog.Warn("endpoint failed", "name", r.Name, "error", r.Err)
			manifest.AddFailedFile(r.Name, r.Err)
			exitCode = 1
		} else {
			slog.Info("endpoint ok", "name", r.Name, "records", r.Records)
			filePath := filepath.Join(w.Dir(), r.Name+".json")
			if err := manifest.AddFile(filePath); err != nil {
				slog.Warn("manifest hash failed", "name", r.Name, "error", err)
			}
		}
	}

	manifestPath := filepath.Join(w.Dir(), "backup-manifest.json")
	if err := manifest.Write(manifestPath, token); err != nil {
		slog.Error("writing manifest", "error", err)
		os.Exit(2)
	}

	slog.Info("backup complete", "dir", w.Dir())
	os.Exit(exitCode)
}
