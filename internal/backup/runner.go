package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/kAYd9iN/holaspirit-backup/internal/api"
	"github.com/kAYd9iN/holaspirit-backup/internal/storage"
)

// workerCount is the number of concurrent endpoint fetchers.
const workerCount = 5

// Result holds the outcome of fetching one endpoint.
type Result struct {
	Name    string
	Records int
	// Files are the backup files written for this endpoint, relative to the
	// backup directory: <name>.json and, when the API side-loaded entities,
	// <name>-linked.json.
	Files []string
	// Err is set when the endpoint could not be backed up; no file was written.
	Err error
	// Incomplete is set when the files were written but the API announced
	// more items than were retrieved. Partial data is kept — it beats none —
	// but the run must not count as a full success.
	Incomplete error
}

// RunFetchers fetches all endpoints concurrently using a bounded worker pool.
func RunFetchers(ctx context.Context, client *api.Client, w *storage.Writer, endpoints []api.Endpoint) []Result {
	type indexedJob struct {
		idx int
		ep  api.Endpoint
	}

	jobs := make(chan indexedJob, len(endpoints))
	results := make([]Result, len(endpoints))
	var wg sync.WaitGroup

	// Concurrency safety (issue #16): each job carries a unique index and every
	// worker writes only to results[job.idx]; no two goroutines ever touch the
	// same element, so the writes do not race under the Go memory model. The
	// slice is read only after wg.Wait() establishes happens-before ordering.
	// The -race detector in CI guards this invariant against future refactors.
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				results[job.idx] = fetch(ctx, client, w, job.ep)
			}
		}()
	}

	for i, ep := range endpoints {
		jobs <- indexedJob{idx: i, ep: ep}
	}
	close(jobs)
	wg.Wait()
	return results
}

func fetch(ctx context.Context, client *api.Client, w *storage.Writer, ep api.Endpoint) Result {
	if !ep.Paginated {
		// Stored as the complete response (data, linked, meta), so nothing
		// the endpoint returns is dropped.
		body, err := client.Get(ctx, ep.Path)
		if err != nil {
			return Result{Name: ep.Name, Err: err}
		}
		data, err := json.Marshal([]json.RawMessage{body})
		if err != nil {
			return Result{Name: ep.Name, Err: err}
		}
		if err := w.WriteJSON(ep.Name, data); err != nil {
			return Result{Name: ep.Name, Err: err}
		}
		return Result{Name: ep.Name, Records: 1, Files: []string{ep.Name + ".json"}}
	}

	col, err := api.FetchAllPages(ctx, client, ep.Path)
	if err != nil {
		return Result{Name: ep.Name, Err: fmt.Errorf("fetch %s: %w", ep.Name, err)}
	}

	data, err := json.Marshal(col.Items)
	if err != nil {
		return Result{Name: ep.Name, Err: err}
	}
	if err := w.WriteJSON(ep.Name, data); err != nil {
		return Result{Name: ep.Name, Err: err}
	}
	res := Result{Name: ep.Name, Records: len(col.Items), Files: []string{ep.Name + ".json"}}

	if len(col.Linked) > 0 {
		linked, err := json.Marshal(col.Linked)
		if err != nil {
			return Result{Name: ep.Name, Err: err}
		}
		if err := w.WriteJSON(ep.Name+"-linked", linked); err != nil {
			return Result{Name: ep.Name, Err: err}
		}
		res.Files = append(res.Files, ep.Name+"-linked.json")
	}

	if missing := col.Missing(); missing > 0 {
		res.Incomplete = fmt.Errorf("retrieved %d of %d items the API reported (%d missing)",
			len(col.Items), col.Reported, missing)
	}
	return res
}
