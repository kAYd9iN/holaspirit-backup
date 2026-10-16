package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/kAYd9iN/holaspirit-backup/internal/compare"
)

// runCompare implements:
//
//	backup compare --holaspirit <dir> --talkspirit <dir> [--out <dir>] [--migrated-at <time>]
//
// Returns an exit code (0=report written, 2=fatal error).
func runCompare(args []string) int {
	fs := flag.NewFlagSet("compare", flag.ExitOnError)
	hsDir := fs.String("holaspirit", "", "Holaspirit backup directory (made by holaspirit-backup v0.5.0) (required)")
	tsDir := fs.String("talkspirit", "", "Talkspirit backup directory (required)")
	out := fs.String("out", "./vergleich", "Directory for vergleich.md and befunde.csv")
	mig := fs.String("migrated-at", "", "End of the migration, YYYY-MM-DD or YYYY-MM-DDTHH:MM (UTC); estimated from the data if empty")
	fs.Parse(args) // #nosec G104 -- FlagSet uses ExitOnError
	if *hsDir == "" || *tsDir == "" {
		fmt.Fprintln(os.Stderr, "usage: backup compare --holaspirit <dir> --talkspirit <dir> [--out <dir>] [--migrated-at YYYY-MM-DD[THH:MM]]")
		return 2
	}
	var migratedAt time.Time
	if *mig != "" {
		var err error
		for _, l := range []string{"2006-01-02T15:04", "2006-01-02T15:04:05Z07:00", "2006-01-02"} {
			if migratedAt, err = time.Parse(l, *mig); err == nil {
				break
			}
		}
		if err != nil {
			slog.Error("invalid --migrated-at", "value", sanitizeLog(*mig))
			return 2
		}
	}

	r, err := compare.Run(*hsDir, *tsDir, migratedAt)
	if err != nil {
		slog.Error("comparing backups", "error", sanitizeLog(err.Error()))
		return 2
	}
	md, csv, err := r.WriteFiles(*out)
	if err != nil {
		slog.Error("writing report", "error", err)
		return 2
	}
	suspicious := 0
	for _, f := range r.Findings {
		if f.Kind.Suspicious() {
			suspicious++
		}
	}
	slog.Info("comparison written", "report", md, "findings", csv, "suspicious", suspicious, "total", len(r.Findings),
		"migrated_at", r.MigratedAt.Format(time.RFC3339), "estimated", r.MigratedAtGuessed)
	return 0
}
