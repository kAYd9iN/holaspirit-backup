package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"time"
)

// ResultCap is the most items a Holaspirit list endpoint returns, however many
// pages are requested. A live run stopped tensions and node-tensions at exactly
// 10,000 — the usual search-index result window — with totalItems capped at
// the same value, so the completeness check could not notice. A variable so
// tests can lower it.
var ResultCap = 10_000

// ErrNoDateFilter means the createdAfter/createdBefore filter could not be
// shown to work, so an endpoint cannot be split into time windows.
var ErrNoDateFilter = errors.New("the createdAfter/createdBefore filter could not be verified to work")

// dateLayouts are tried in order; the spec documents the filter values only as
// "string".
var dateLayouts = []string{"2006-01-02", time.RFC3339}

// windowStart is earlier than any Holaspirit content.
var windowStart = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

const day = 24 * time.Hour

// withQuery returns path with the given query parameters set.
func withQuery(path string, kv ...string) string {
	u, err := url.Parse(path)
	if err != nil {
		return path
	}
	q := u.Query()
	for i := 0; i+1 < len(kv); i += 2 {
		q.Set(kv[i], kv[i+1])
	}
	return u.Path + "?" + q.Encode()
}

// countItems returns how many items the API reports for path (one request
// with count=1): totalItems when present, otherwise the number of items on
// that single page (0 or 1 — enough to tell "none" from "some").
func countItems(ctx context.Context, client *Client, path string) (int, error) {
	body, err := client.Get(ctx, withQuery(path, "page", "1", "count", "1"))
	if err != nil {
		return 0, err
	}
	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return 0, fmt.Errorf("parse %s: %w", path, err)
	}
	if info := parsePageInfo(env); info.total.ok {
		return info.total.v, nil
	}
	var items []json.RawMessage
	if json.Unmarshal(env.Data, &items) == nil {
		return len(items), nil
	}
	return 0, nil
}

// detectDateLayout finds a date format the endpoint's createdBefore filter
// really applies: "created before 1971" must match nothing while "created
// before the day after tomorrow" must match something. A filter that is
// ignored, or a format it does not understand, fails one of the two.
func detectDateLayout(ctx context.Context, client *Client, path string, now time.Time) (string, error) {
	past := time.Date(1971, 1, 1, 0, 0, 0, 0, time.UTC)
	future := now.Add(2 * day)
	for _, layout := range dateLayouts {
		none, err := countItems(ctx, client, withQuery(path, "createdBefore", past.Format(layout)))
		if err != nil {
			continue // e.g. HTTP 400 for a format the API rejects
		}
		some, err := countItems(ctx, client, withQuery(path, "createdBefore", future.Format(layout)))
		if err != nil {
			continue
		}
		if none == 0 && some > 0 {
			return layout, nil
		}
	}
	return "", ErrNoDateFilter
}

// FetchByCreatedWindows retrieves every item of an endpoint that supports the
// createdAfter/createdBefore filters, even beyond ResultCap: the creation-time
// range is halved until each window holds fewer than ResultCap items.
// Adjacent windows overlap by a day (the filters' inclusiveness is not
// documented); duplicates are removed by item ID. Every returned item's
// createdAt must lie inside its window, otherwise the filter does not do what
// it seems to and an error is returned instead of a possibly wrong result.
func FetchByCreatedWindows(ctx context.Context, client *Client, path string, now time.Time) (*Collection, error) {
	layout, err := detectDateLayout(ctx, client, path, now)
	if err != nil {
		return nil, err
	}

	out := &Collection{Items: []json.RawMessage{}, Linked: map[string][]json.RawMessage{}, Reported: -1}
	itemSeen := map[string]bool{}
	linkedSeen := map[string]bool{}

	var walk func(after, before time.Time) error
	walk = func(after, before time.Time) error {
		wpath := withQuery(path, "createdAfter", after.Format(layout), "createdBefore", before.Format(layout))
		n, err := countItems(ctx, client, wpath)
		if err != nil {
			return err
		}
		if n < ResultCap {
			col, err := FetchAllPages(ctx, client, wpath)
			if err != nil {
				return err
			}
			if len(col.Items) < ResultCap {
				if err := checkWindow(col.Items, after, before); err != nil {
					return err
				}
				for _, it := range col.Items {
					if k := entityKey(it); !itemSeen[k] {
						itemSeen[k] = true
						out.Items = append(out.Items, it)
					}
				}
				for typ, ents := range col.Linked {
					raw, _ := json.Marshal(map[string][]json.RawMessage{typ: ents})
					out.addLinked(raw, linkedSeen)
				}
				return nil
			}
		}
		if before.Sub(after) <= 2*day {
			return fmt.Errorf("%s: %d or more items were created between %s and %s — more than the API returns for one query, and the window cannot be split further",
				path, ResultCap, after.Format("2006-01-02"), before.Format("2006-01-02"))
		}
		mid := after.Add(before.Sub(after) / 2).Truncate(day)
		if err := walk(after, mid.Add(day)); err != nil {
			return err
		}
		return walk(mid, before)
	}
	if err := walk(windowStart, now.Add(2*day).Truncate(day)); err != nil {
		return nil, err
	}
	return out, nil
}

// checkWindow verifies that items carry a createdAt inside [after, before]
// (with a day of tolerance for time zones and inclusive/exclusive bounds).
func checkWindow(items []json.RawMessage, after, before time.Time) error {
	for _, it := range items {
		var v struct {
			CreatedAt string `json:"createdAt"`
		}
		if json.Unmarshal(it, &v) != nil || v.CreatedAt == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339, v.CreatedAt)
		if err != nil {
			continue
		}
		if t.Before(after.Add(-day)) || t.After(before.Add(day)) {
			return fmt.Errorf("the createdAfter/createdBefore filter returned an item created %s outside the window %s–%s — not applied as expected",
				v.CreatedAt, after.Format("2006-01-02"), before.Format("2006-01-02"))
		}
	}
	return nil
}
