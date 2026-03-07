package backup_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kAYd9iN/holaspirit-backup/internal/api"
	"github.com/kAYd9iN/holaspirit-backup/internal/backup"
	"github.com/kAYd9iN/holaspirit-backup/internal/storage"
)

func TestExports_PDFAndXLSX(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /api/async/organizations/org1/pdf",
			"POST /api/async/organizations/org1/spreadsheet":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"data": map[string]string{"id": "job123"},
			})
		case "GET /api/export/organizations/org1/jobs/job123/download":
			callCount++
			if callCount < 2 {
				// Simulate pending: return 202 with no body
				w.WriteHeader(http.StatusAccepted)
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Write([]byte("FAKE_FILE_CONTENT"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	client := api.NewClient(srv.URL, "api:test")
	tmpDir := t.TempDir()
	writer, _ := storage.NewWriter(tmpDir, time.Now())

	exporter := backup.NewExporter(client, "org1")
	exporter.PollInterval = 1 * time.Millisecond

	if err := exporter.Run(context.Background(), writer); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify both files were written
	for _, name := range []string{"export.pdf", "export.xlsx"} {
		path := filepath.Join(writer.Dir(), name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("%s not written: %v", name, err)
			continue
		}
		if len(data) == 0 {
			t.Errorf("%s is empty", name)
		}
	}
}

func TestExports_ContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /api/async/organizations/org1/pdf":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"data": map[string]string{"id": "job123"},
			})
		default:
			// Always return 202 to simulate stuck job
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	defer srv.Close()

	client := api.NewClient(srv.URL, "api:test")
	tmpDir := t.TempDir()
	writer, _ := storage.NewWriter(tmpDir, time.Now())

	exporter := backup.NewExporter(client, "org1")
	exporter.PollInterval = 1 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := exporter.Run(ctx, writer)
	if err == nil {
		t.Fatal("expected error on context cancellation, got nil")
	}
}
