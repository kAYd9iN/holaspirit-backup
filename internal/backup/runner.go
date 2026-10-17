package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/kAYd9iN/holaspirit-backup/internal/api"
	"github.com/kAYd9iN/holaspirit-backup/internal/storage"
)

// workerCount is the number of concurrent endpoint fetchers. The Partner API
// quota is per key, shared by all workers, so more workers do not make the
// backup faster — a few only hide request latency.
const workerCount = 3

// maxChildDepth bounds recursive sub-resources (sub-tasks of sub-tasks).
const maxChildDepth = 10

// Options tune a backup run.
type Options struct {
	// Quick skips the per-item Detail sub-resources (comments, agenda items),
	// which cost one request per item.
	Quick bool
}

// Result holds the outcome of fetching one endpoint.
type Result struct {
	Name    string
	Records int
	// Files are the backup files written for this endpoint, relative to the
	// backup directory: <name>.json and one file per sub-resource.
	Files []string
	// Children counts the records written per sub-resource file.
	Children map[string]int
	// Err is set when the endpoint could not be backed up; no file was written.
	Err error
	// Incomplete is set when the files were written but not everything could
	// be retrieved (traversal cap, failed sub-resource requests). Partial data
	// is kept — it beats none — but the run must not count as a full success.
	Incomplete error
	// Unavailable is set when an optional endpoint is refused for this key
	// (admin-only data). Nothing was written, and that is not a failure.
	Unavailable error
}

// ChildGroup is one parent's sub-resource in a <child>.json file.
type ChildGroup struct {
	ParentID string            `json:"parent_id"`
	Data     []json.RawMessage `json:"data"`
}

// RunFetchers fetches all endpoints concurrently using a bounded worker pool.
func RunFetchers(ctx context.Context, client *api.Client, w *storage.Writer, endpoints []api.Endpoint, opt Options) []Result {
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
				results[job.idx] = fetch(ctx, client, w, job.ep, opt)
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

// refused reports whether err is an HTTP answer that means "not for this key"
// rather than a fault.
func refused(err error) (*api.StatusError, bool) {
	var se *api.StatusError
	if errors.As(err, &se) && (se.Code == 400 || se.Code == 401 || se.Code == 403 || se.Code == 404) {
		return se, true
	}
	return nil, false
}

func fetch(ctx context.Context, client *api.Client, w *storage.Writer, ep api.Endpoint, opt Options) Result {
	res := fetchEndpoint(ctx, client, w, ep, opt)
	if res.Err != nil && ep.Optional != "" {
		var se *api.StatusError
		// 409: the feature is switched off for the organization
		// (audit log: ACTIVITY_LOG_DISABLED).
		if errors.As(res.Err, &se) && (se.Code == 401 || se.Code == 403 || se.Code == 404 || se.Code == 409) {
			return Result{Name: ep.Name, Unavailable: fmt.Errorf("%s (%s)", ep.Optional, se.Error())}
		}
	}
	if res.Err != nil {
		var se *api.StatusError
		if errors.As(res.Err, &se) && se.Problem == "FORBIDDEN_SCOPE" && ep.Scope != "" {
			res.Err = fmt.Errorf("the API key lacks the %s scope: %w", ep.Scope, res.Err)
		}
	}
	return res
}

func fetchEndpoint(ctx context.Context, client *api.Client, w *storage.Writer, ep api.Endpoint, opt Options) Result {
	col, err := api.FetchAll(ctx, client, ep.Path, ep.Limit)
	var problems []string
	if err != nil {
		if col == nil || !errors.Is(err, api.ErrTraversalCap) {
			return Result{Name: ep.Name, Err: fmt.Errorf("fetch %s: %w", ep.Name, err)}
		}
		problems = append(problems, err.Error())
	}

	data, err := json.Marshal(col.Items)
	if err != nil {
		return Result{Name: ep.Name, Err: err}
	}
	if err := w.WriteJSON(ep.Name, data); err != nil {
		return Result{Name: ep.Name, Err: err}
	}
	res := Result{Name: ep.Name, Records: len(col.Items), Files: []string{ep.Name + ".json"}, Children: map[string]int{}}

	for _, ch := range ep.Children {
		if ch.Detail && opt.Quick {
			continue
		}
		groups, n, chProblems, err := fetchChildren(ctx, client, ch, col.Items)
		if err != nil {
			// Context cancelled or timed out: keep what is already written.
			problems = append(problems, fmt.Sprintf("%s: %v", ch.Name, err))
		}
		problems = append(problems, chProblems...)
		out, merr := json.Marshal(groups)
		if merr != nil {
			return Result{Name: ep.Name, Err: merr}
		}
		if werr := w.WriteJSON(ch.Name, out); werr != nil {
			return Result{Name: ep.Name, Err: werr}
		}
		res.Files = append(res.Files, ch.Name+".json")
		res.Children[ch.Name] = n
		if err != nil {
			break
		}
	}

	if len(problems) > 0 {
		res.Incomplete = errors.New(summarize(problems))
	}
	return res
}

// fetchChildren fetches ch for every parent item. Requests refused for a
// single parent are collected as problems instead of aborting the endpoint;
// only a cancelled context stops the walk.
func fetchChildren(ctx context.Context, client *api.Client, ch api.Child, parents []json.RawMessage) ([]ChildGroup, int, []string, error) {
	groups := []ChildGroup{}
	var problems []string
	total, failed := 0, 0
	queue := parents
	for depth := 0; len(queue) > 0 && depth < maxChildDepth; depth++ {
		var next []json.RawMessage
		for _, p := range queue {
			if err := ctx.Err(); err != nil {
				return groups, total, problems, err
			}
			id := api.ItemID(p)
			if id == "" || (ch.OnlyIf != "" && !positive(p, ch.OnlyIf)) {
				continue
			}
			col, err := api.FetchAll(ctx, client, fmt.Sprintf(ch.Path, id), ch.Limit)
			if err != nil {
				if ctx.Err() != nil {
					return groups, total, problems, ctx.Err()
				}
				if _, ok := refused(err); ok && ch.Optional != "" {
					continue
				}
				if col == nil {
					failed++
					if failed <= 3 {
						problems = append(problems, fmt.Sprintf("%s for %s: %v", ch.Name, id, err))
					}
					continue
				}
				problems = append(problems, fmt.Sprintf("%s for %s: %v", ch.Name, id, err))
			}
			groups = append(groups, ChildGroup{ParentID: id, Data: col.Items})
			total += len(col.Items)
			if ch.Recursive {
				next = append(next, col.Items...)
			}
		}
		if !ch.Recursive {
			break
		}
		queue = next
	}
	if ch.Recursive && anyPositive(queue, ch.OnlyIf) {
		problems = append(problems, fmt.Sprintf("%s: nesting deeper than %d levels not fetched", ch.Name, maxChildDepth))
	}
	if failed > 3 {
		problems = append(problems, fmt.Sprintf("%s: %d requests failed in total", ch.Name, failed))
	}
	return groups, total, problems, nil
}

// positive reports whether the numeric field of item is > 0.
func positive(item json.RawMessage, field string) bool {
	var m map[string]json.RawMessage
	if json.Unmarshal(item, &m) != nil {
		return false
	}
	var n float64
	return json.Unmarshal(m[field], &n) == nil && n > 0
}

func anyPositive(items []json.RawMessage, field string) bool {
	for _, it := range items {
		if field == "" || positive(it, field) {
			return true
		}
	}
	return false
}

func summarize(problems []string) string {
	const max = 5
	if len(problems) <= max {
		return strings.Join(problems, "; ")
	}
	return strings.Join(problems[:max], "; ") + fmt.Sprintf("; … and %d more", len(problems)-max)
}
