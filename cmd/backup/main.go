package main

import (
	"context"
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

func main() {
	// Subcommand dispatch
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "verify":
			fs := flag.NewFlagSet("verify", flag.ExitOnError)
			dir := fs.String("dir", "", "Backup directory to verify (required)")
			fs.Parse(os.Args[2:]) // #nosec G104 -- FlagSet uses ExitOnError; return value is unreachable
			if *dir == "" {
				fmt.Fprintln(os.Stderr, "usage: backup verify --dir <path>")
				os.Exit(2)
			}
			os.Exit(runVerify(*dir))
		case "compare":
			os.Exit(runCompare(os.Args[2:]))
		}
	}

	// Main backup command
	outputDir := flag.String("output", "./backup", "Backup destination directory")
	baseURL := flag.String("base-url", api.BaseURL, "Partner API base URL (staging: https://partner-api.ghostspirit.net)")
	dryRun := flag.Bool("dry-run", false, "Test the API key (GET /v1/whoami) without writing files")
	quick := flag.Bool("quick", false, "Skip per-item comments and meeting agenda items (one request per item each)")
	showVer := flag.Bool("version", false, "Show version and exit")
	timeoutMin := flag.Int("timeout", 480, "Overall timeout in minutes (0 = no timeout)")
	audit := flag.Bool("audit", false, "Record hostname and username in the manifest (audit trail)")
	flag.Parse()

	if *showVer {
		// Releases set version to the tag ("v0.4.0"); avoid printing "vv0.4.0".
		fmt.Printf("talkspirit-backup v%s\n", strings.TrimPrefix(version, "v"))
		os.Exit(0)
	}
	if !strings.HasPrefix(*baseURL, "https://") {
		slog.Error("--base-url must use https")
		os.Exit(2)
	}

	token, err := getToken()
	if err != nil {
		slog.Error("loading API key", "error", err,
			"hint", "set TALKSPIRIT_API_KEY, or on Windows: cmdkey /generic:talkspirit-backup /user:api /pass:ts_partner_…")
		os.Exit(2)
	}

	client := api.NewClient(*baseURL, token)
	client.SetUserAgent("talkspirit-backup/" + strings.TrimPrefix(version, "v"))

	ctx := context.Background()
	if *timeoutMin > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(*timeoutMin)*time.Minute)
		defer cancel()
	}

	body, err := client.Get(ctx, "/v1/whoami")
	if err != nil {
		slog.Error("checking the API key (GET /v1/whoami)", "error", sanitizeLog(err.Error()),
			"hint", "401: key wrong/revoked, or the API module is not enabled for the organization")
		os.Exit(2)
	}
	who, err := api.ParseWhoAmI(body)
	if err != nil {
		slog.Error("checking the API key", "error", err)
		os.Exit(2)
	}
	// Validate BEFORE logging the tenant ID (issue #18): it comes from an API
	// response, so it is format-checked before it reaches a log line.
	if err := api.ValidateTenantID(who.TenantID); err != nil {
		slog.Error("invalid tenant ID", "error", err)
		os.Exit(2)
	}
	slog.Info("API key OK", "tenant", sanitizeLog(who.TenantID), "partner", sanitizeLog(who.PartnerName)) // #nosec G706 -- sanitized
	if missing := missingScopes(who.Scopes); len(missing) > 0 {
		slog.Warn("the key lacks read scopes — these parts of the backup will fail or be skipped",
			"missing", sanitizeLog(strings.Join(missing, ",")))
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

	endpoints := api.AllEndpoints()
	slog.Info("fetching endpoints", "count", len(endpoints), "quick", *quick) // #nosec G706 -- an integer count, no attacker-controlled text

	manifest := backup.NewManifest(who.TenantID, version, ts)
	manifest.Root = w.Dir() // record file names relative to the backup root (verify re-hashes them)
	if *audit {
		manifest.SetAudit(auditInfo())
	}
	results := backup.RunFetchers(ctx, client, w, endpoints, backup.Options{Quick: *quick})

	exitCode := 0
	for _, r := range results {
		// Endpoint names are compile-time constants, but they are sanitized
		// before logging as defense-in-depth against log injection (issue #18).
		safeName := sanitizeLog(r.Name)
		if r.Unavailable != nil {
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
			// Data was written, but not everything could be retrieved — never
			// let a partial backup pass as complete.
			slog.Warn("endpoint incomplete", "name", safeName, "records", r.Records,
				"error", sanitizeLog(r.Incomplete.Error()))
			exitCode = 1
		} else {
			slog.Info("endpoint ok", "name", safeName, "records", r.Records, "sub-resources", fmt.Sprint(r.Children))
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

// missingScopes returns the read scopes a complete backup needs that the key
// does not have.
func missingScopes(have []string) []string {
	got := map[string]bool{}
	for _, s := range have {
		got[s] = true
	}
	var missing []string
	for _, s := range api.RequiredScopes() {
		if !got[s] {
			missing = append(missing, s)
		}
	}
	return missing
}
