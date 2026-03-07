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
	Err     error
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
	var items []json.RawMessage
	var err error

	if ep.Paginated {
		items, err = api.FetchAllPages(ctx, client, ep.Path)
	} else {
		body, e := client.Get(ctx, ep.Path)
		if e != nil {
			return Result{Name: ep.Name, Err: e}
		}
		items = []json.RawMessage{body}
	}

	if err != nil {
		return Result{Name: ep.Name, Err: fmt.Errorf("fetch %s: %w", ep.Name, err)}
	}

	data, err := json.Marshal(items)
	if err != nil {
		return Result{Name: ep.Name, Err: err}
	}

	if err := w.WriteJSON(ep.Name, data); err != nil {
		return Result{Name: ep.Name, Err: err}
	}

	return Result{Name: ep.Name, Records: len(items)}
}
