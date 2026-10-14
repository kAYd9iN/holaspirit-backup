package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/kAYd9iN/holaspirit-backup/internal/api"
	"github.com/kAYd9iN/holaspirit-backup/internal/backup"
	"github.com/kAYd9iN/holaspirit-backup/internal/storage"
)

// auditInfo collects host and user identity for the optional --audit trail.
// CI=true (set by GitHub Actions and most CI systems) marks the run automated.
func auditInfo() *backup.AuditInfo {
	a := &backup.AuditInfo{Automated: os.Getenv("CI") != ""}
	if h, err := os.Hostname(); err == nil {
		a.Hostname = h
	}
	if u, err := user.Current(); err == nil {
		a.User = u.Username
	}
	return a
}

// version is set at build time via -ldflags "-X main.version=vX.Y.Z"
var version = "dev"

const holaspiritBaseURL = "https://app.holaspirit.com"

func main() {
	// Subcommand dispatch
	if len(os.Args) > 1 && os.Args[1] == "verify" {
		fs := flag.NewFlagSet("verify", flag.ExitOnError)
		dir := fs.String("dir", "", "Backup directory to verify (required)")
		fs.Parse(os.Args[2:]) // #nosec G104 -- FlagSet uses ExitOnError; return value is unreachable
		if *dir == "" {
			fmt.Fprintln(os.Stderr, "usage: backup verify --dir <path>")
			os.Exit(2)
		}
		os.Exit(runVerify(*dir))
	}

	// Main backup command
	outputDir := flag.String("output", "./backup", "Backup destination directory")
	orgID := flag.String("org-id", os.Getenv("HOLASPIRIT_ORG_ID"), "Organization ID (default $HOLASPIRIT_ORG_ID; auto-detected via /api/me if empty)")
	dryRun := flag.Bool("dry-run", false, "Test connection without writing files")
	showVer := flag.Bool("version", false, "Show version and exit")
	timeoutMin := flag.Int("timeout", 120, "Overall timeout in minutes (0 = no timeout)")
	audit := flag.Bool("audit", false, "Record hostname and username in the manifest (audit trail)")
	flag.Parse()

	if *showVer {
		// Releases set version to the tag ("v0.4.0"); avoid printing "vv0.4.0".
		fmt.Printf("holaspirit-backup v%s\n", strings.TrimPrefix(version, "v"))
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
			var se *api.StatusError
			if errors.As(err, &se) && (se.Code == 401 || se.Code == 403) {
				// Organization API tokens may not read /api/me.
				slog.Error("this token cannot read /api/me, so the organization ID cannot be detected — pass --org-id <id> or set HOLASPIRIT_ORG_ID (the ID is in the Holaspirit URL: app.holaspirit.com/o/<id>/…)",
					"error", err)
			} else {
				slog.Error("discovering organization ID", "error", err)
			}
			os.Exit(2)
		}
		*orgID = id
	}

	// Validate BEFORE logging the org ID (issue #18): the value may come from
	// an API response, so it is sanitized and format-checked before it ever
	// reaches a log line.
	if err := api.ValidateOrgID(*orgID); err != nil {
		slog.Error("invalid organization ID", "error", err)
		os.Exit(2)
	}
	slog.Info("organization confirmed", "id", sanitizeLog(*orgID)) // #nosec G706 -- passed ValidateOrgID ([A-Za-z0-9_-] only) and sanitizeLog

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
	slog.Info("fetching endpoints", "count", len(endpoints)) // #nosec G706 -- an integer count, no attacker-controlled text

	manifest := backup.NewManifest(*orgID, version, ts)
	manifest.Root = w.Dir() // record file names relative to the backup root (verify re-hashes them)
	if *audit {
		manifest.SetAudit(auditInfo())
	}
	results := backup.RunFetchers(ctx, client, w, endpoints)

	exitCode := 0
	for _, r := range results {
		// Endpoint names are compile-time constants, but they are sanitized
		// before logging as defense-in-depth against log injection (issue #18).
		safeName := sanitizeLog(r.Name)
		if r.Unavailable != nil {
			// Optional feature not enabled for this organization: nothing to back up.
			slog.Warn("endpoint not available", "name", safeName, "reason", sanitizeLog(r.Unavailable.Error()))
			manifest.AddUnavailable(r.Name, r.Unavailable)
			continue
		}
		if r.Err != nil {
			slog.Warn("endpoint failed", "name", safeName, "error", sanitizeLog(r.Err.Error()))
			manifest.AddFailedFile(r.Name, r.Err)
			exitCode = 1
			continue
		}
		for _, f := range r.Files {
			filePath := filepath.Join(w.Dir(), f)
			add := manifest.AddFile
			if r.Incomplete != nil {
				add = func(p string) error { return manifest.AddIncompleteFile(p, r.Incomplete) }
			}
			if err := add(filePath); err != nil {
				slog.Warn("manifest hash failed", "file", sanitizeLog(f), "error", sanitizeLog(err.Error()))
			}
		}
		if r.Incomplete != nil {
			// Data was written, but the API announced more items than it
			// delivered — never let a partial backup pass as complete.
			slog.Warn("endpoint incomplete", "name", safeName, "records", r.Records,
				"error", sanitizeLog(r.Incomplete.Error()))
			exitCode = 1
		} else {
			slog.Info("endpoint ok", "name", safeName, "records", r.Records, "files", len(r.Files))
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
