package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/kAYd9iN/holaspirit-backup/internal/api"
	"github.com/kAYd9iN/holaspirit-backup/internal/storage"
)

// Exporter triggers async PDF/XLSX exports and downloads the results.
type Exporter struct {
	client       *api.Client
	orgID        string
	PollInterval time.Duration
}

func NewExporter(client *api.Client, orgID string) *Exporter {
	return &Exporter{
		client:       client,
		orgID:        orgID,
		PollInterval: 5 * time.Second,
	}
}

// Run triggers PDF and XLSX exports and writes them to the storage writer.
func (e *Exporter) Run(ctx context.Context, w *storage.Writer) error {
	exports := []struct{ name, endpoint, filename string }{
		{"pdf", fmt.Sprintf("/api/async/organizations/%s/pdf", e.orgID), "export.pdf"},
		{"spreadsheet", fmt.Sprintf("/api/async/organizations/%s/spreadsheet", e.orgID), "export.xlsx"},
	}

	for _, ex := range exports {
		jobID, err := e.triggerExport(ctx, ex.endpoint)
		if err != nil {
			return fmt.Errorf("trigger %s export: %w", ex.name, err)
		}

		data, err := e.pollAndDownload(ctx, jobID)
		if err != nil {
			return fmt.Errorf("download %s: %w", ex.name, err)
		}

		if err := w.WriteFile(ex.filename, data); err != nil {
			return fmt.Errorf("write %s: %w", ex.filename, err)
		}
	}
	return nil
}

func (e *Exporter) triggerExport(ctx context.Context, path string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.client.BaseURL()+path, bytes.NewReader([]byte("{}")))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+e.client.Token())
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("trigger export: HTTP %d", resp.StatusCode)
	}

	var result struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode trigger response: %w", err)
	}
	if result.Data.ID == "" {
		return "", fmt.Errorf("trigger response missing job ID")
	}
	return result.Data.ID, nil
}

// pollAndDownload polls the download URL until the file is ready (200 OK with body).
// A 202 Accepted response means the job is still processing.
func (e *Exporter) pollAndDownload(ctx context.Context, jobID string) ([]byte, error) {
	downloadURL := fmt.Sprintf("%s/api/export/organizations/%s/jobs/%s/download",
		e.client.BaseURL(), e.orgID, jobID)

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(e.PollInterval):
		}

		data, done, err := e.tryDownload(ctx, downloadURL)
		if err != nil {
			return nil, err
		}
		if done {
			return data, nil
		}
		// 202: still processing, poll again
	}
}

// tryDownload performs one download attempt. Returns (data, true, nil) on success,
// (nil, false, nil) when still pending (202), or (nil, false, err) on error.
func (e *Exporter) tryDownload(ctx context.Context, url string) ([]byte, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Authorization", "Bearer "+e.client.Token())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusAccepted {
		return nil, false, nil
	}
	if resp.StatusCode >= 400 {
		return nil, false, fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, false, fmt.Errorf("read download body: %w", err)
	}
	return data, true, nil
}
