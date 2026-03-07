package main

import (
	"context"
	"flag"
	"fmt"
	"log"
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

	log.SetFlags(log.Ldate | log.Ltime)

	token, err := getToken()
	if err != nil {
		log.Fatalf("ERROR loading token: %v\n\nOn Windows, store credentials with:\n  cmdkey /generic:holaspirit-backup /user:api /pass:api:YOUR_TOKEN", err)
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
			log.Fatalf("ERROR discovering organization ID: %v", err)
		}
		*orgID = id
		log.Printf("Organization ID: %s", *orgID)
	}

	if *dryRun {
		log.Println("Dry run successful — connection OK")
		os.Exit(0)
	}

	ts := time.Now()
	w, err := storage.NewWriter(*outputDir, ts)
	if err != nil {
		log.Fatalf("ERROR creating backup directory: %v", err)
	}
	log.Printf("Backup directory: %s", w.Dir())

	endpoints := api.AllEndpoints(*orgID)
	log.Printf("Fetching %d endpoints concurrently...", len(endpoints))

	manifest := backup.NewManifest(*orgID, version, ts)
	results := backup.RunFetchers(ctx, client, w, endpoints)

	for _, r := range results {
		if r.Err != nil {
			log.Printf("WARN: %s failed: %v", r.Name, r.Err)
			manifest.AddFailedFile(r.Name, r.Err)
		} else {
			log.Printf("OK: %s (%d records)", r.Name, r.Records)
			filePath := filepath.Join(w.Dir(), r.Name+".json")
			if err := manifest.AddFile(filePath); err != nil {
				log.Printf("WARN: manifest hash for %s failed: %v", r.Name, err)
			}
		}
	}

	manifestPath := filepath.Join(w.Dir(), "backup-manifest.json")
	if err := manifest.Write(manifestPath); err != nil {
		log.Fatalf("ERROR writing manifest: %v", err)
	}

	log.Printf("Manifest written: %s", manifestPath)
	log.Printf("Backup complete: %s", w.Dir())
}
