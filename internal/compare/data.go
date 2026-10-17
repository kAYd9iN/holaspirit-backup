// Package compare matches a Holaspirit backup (made by v0.5.0 of this tool)
// against a Talkspirit backup and reports what the migration lost.
//
// The Partner API carries no Holaspirit IDs, so objects are matched on
// business keys (e-mail, circle path + name, title + timestamp). Every
// difference is classified against the migration time: a Talkspirit object
// changed after the migration was probably edited on purpose; one untouched
// since then points at the migration itself.
package compare

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// obj is one decoded JSON record.
type obj = map[string]any

// maxFileBytes bounds what is read from one backup file.
const maxFileBytes = 512 * 1024 * 1024

func readJSON(dir, name string, v any) (bool, error) {
	p := filepath.Join(dir, name+".json")
	f, err := os.Open(p) // #nosec G304 -- operator-supplied backup directory + fixed file names
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return false, err
	}
	if st.Size() > maxFileBytes {
		return false, fmt.Errorf("%s: file larger than %d bytes", p, maxFileBytes)
	}
	dec := json.NewDecoder(f)
	if err := dec.Decode(v); err != nil {
		return false, fmt.Errorf("%s: %w", p, err)
	}
	return true, nil
}

// loadArr reads <name>.json as a list of records. A missing file yields nil.
func loadArr(dir, name string) ([]obj, bool, error) {
	var out []obj
	ok, err := readJSON(dir, name, &out)
	return out, ok, err
}

// loadLinked reads a Holaspirit <name>-linked.json (type → entities).
func loadLinked(dir, name string) (map[string][]obj, error) {
	out := map[string][]obj{}
	_, err := readJSON(dir, name+"-linked", &out)
	return out, err
}

// loadChildren reads a Talkspirit sub-resource file ([{parent_id, data}]).
func loadChildren(dir, name string) (map[string][]obj, bool, error) {
	var groups []struct {
		ParentID string          `json:"parent_id"`
		Data     json.RawMessage `json:"data"`
	}
	ok, err := readJSON(dir, name, &groups)
	if err != nil || !ok {
		return nil, ok, err
	}
	out := map[string][]obj{}
	for _, g := range groups {
		var list []obj
		if json.Unmarshal(g.Data, &list) != nil {
			// single object (document content)
			var one obj
			if json.Unmarshal(g.Data, &one) == nil {
				list = []obj{one}
			}
		}
		out[g.ParentID] = append(out[g.ParentID], list...)
	}
	return out, true, nil
}

func byID(list []obj) map[string]obj {
	m := make(map[string]obj, len(list))
	for _, o := range list {
		if id := str(o, "id"); id != "" {
			m[id] = o
		}
	}
	return m
}

func str(o obj, k string) string {
	if o == nil {
		return ""
	}
	switch v := o[k].(type) {
	case string:
		return v
	case float64:
		return fmt.Sprint(v)
	case bool:
		return fmt.Sprint(v)
	}
	return ""
}

func strs(o obj, k string) []string {
	arr, _ := o[k].([]any)
	out := make([]string, 0, len(arr))
	for _, v := range arr {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func objs(o obj, k string) []obj {
	arr, _ := o[k].([]any)
	out := make([]obj, 0, len(arr))
	for _, v := range arr {
		if m, ok := v.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func boolean(o obj, k string) bool {
	b, _ := o[k].(bool)
	return b
}

var timeLayouts = []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999", "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02"}

// parseTime reads the timestamp formats of both APIs; zero when absent.
func parseTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, l := range timeLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func tm(o obj, k string) time.Time { return parseTime(str(o, k)) }

func minuteKey(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02T15:04")
}

func dayKey(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02")
}

var (
	tagRe     = regexp.MustCompile(`<[^>]*>`)
	mdLinkRe  = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	mdCharsRe = regexp.MustCompile("[*_#>`~\\\\]")
	bulletRe  = regexp.MustCompile(`(^|\s)[-•·–]\s`)
	wordRe    = regexp.MustCompile(`[\p{L}\p{N}]+`)
	// Holaspirit mentions: <a data-mention="id|type">Name</a>
	hsMentionRe = regexp.MustCompile(`(?s)<a[^>]*data-mention[^>]*>.*?</a>`)
	// mentions Talkspirit kept as plain text: "@Name"
	atMentionRe = regexp.MustCompile(`@[\p{L}\p{N}][^\s,;.)]*`)
)

// blockText extracts the text of a Talkspirit rich-text value: a JSON array of
// BlockNote blocks ({type, content: [{type: "text", text}], children}) stored
// as a string. Blocks become lines; ok is false when s is not such a value.
func blockText(s string, mentions bool) (string, bool) {
	t := strings.TrimSpace(s)
	if !strings.HasPrefix(t, "[") || !strings.Contains(t, `"type"`) {
		return "", false
	}
	var blocks []any
	if json.Unmarshal([]byte(t), &blocks) != nil {
		return "", false
	}
	var lines []string
	var inline func(v any, b *strings.Builder)
	inline = func(v any, b *strings.Builder) {
		switch x := v.(type) {
		case []any:
			for _, e := range x {
				inline(e, b)
			}
		case map[string]any:
			if txt, ok := x["text"].(string); ok {
				b.WriteString(txt)
			}
			// Mentions of circles, roles and people carry their name in props.
			if x["type"] == "mention" && mentions {
				if p, ok := x["props"].(map[string]any); ok {
					for _, k := range []string{"userName", "name", "label", "title"} {
						if n, ok := p[k].(string); ok && n != "" {
							b.WriteString(" " + n + " ")
							break
						}
					}
				}
			}
			for _, k := range []string{"content", "rows", "cells"} {
				if c, ok := x[k]; ok {
					inline(c, b)
					if k == "cells" {
						b.WriteString(" ")
					}
				}
			}
		case string:
			b.WriteString(x)
		}
	}
	var walk func(list []any)
	walk = func(list []any) {
		for _, e := range list {
			m, ok := e.(map[string]any)
			if !ok {
				continue
			}
			var b strings.Builder
			inline(m["content"], &b)
			if line := strings.TrimSpace(b.String()); line != "" {
				lines = append(lines, line)
			}
			if ch, ok := m["children"].([]any); ok {
				walk(ch)
			}
		}
	}
	walk(blocks)
	return strings.Join(lines, "\n"), true
}

// plain strips markup (HTML from Holaspirit; BlockNote JSON or Markdown from
// Talkspirit) and collapses whitespace, keeping the wording and case.
func plain(s string) string { return plainText(s, true) }

// plainText is plain; mentions=false drops the names of mentioned circles,
// roles and people (Talkspirit shows their current, possibly renamed name).
func plainText(s string, mentions bool) string {
	if t, ok := blockText(s, mentions); ok {
		s = t
	} else if !mentions {
		s = hsMentionRe.ReplaceAllString(s, " ")
		s = atMentionRe.ReplaceAllString(s, " ")
	}
	s = strings.ReplaceAll(s, "<br", " <br")
	s = strings.ReplaceAll(s, "</p>", " </p>")
	s = tagRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	s = mdLinkRe.ReplaceAllString(s, "$1")
	s = mdCharsRe.ReplaceAllString(s, " ")
	s = bulletRe.ReplaceAllString(s, " ")
	s = strings.ReplaceAll(s, "\u00a0", " ") // non-breaking space
	s = strings.Join(strings.Fields(s), " ")
	return strings.Trim(s, " .;:,-–•")
}

// norm reduces a text to a comparable form: plain, lower case.
func norm(s string) string {
	return strings.ToLower(plain(s))
}

// words returns the word set of a normalized text.
func words(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range wordRe.FindAllString(s, -1) {
		out[w] = true
	}
	return out
}

// sameText reports whether two texts say the same. Tolerated: formatting,
// small wording differences, lost spaces, and mentions — Talkspirit shows a
// mentioned role or circle under its current name, which may have been
// renamed, and sometimes fills a mention Holaspirit had left empty.
func sameText(a, b string) bool {
	na, nb := norm(a), norm(b)
	if similar(na, nb) || squash(na) == squash(nb) {
		return true
	}
	ma, mb := strings.ToLower(plainText(a, false)), strings.ToLower(plainText(b, false))
	return similar(ma, mb) || squash(ma) == squash(mb)
}

// squash keeps only letters and digits, so lost or extra spaces do not count.
func squash(s string) string {
	return strings.Join(wordRe.FindAllString(s, -1), "")
}

// similar compares two normalized texts by their word sets: Jaccard ≥ 0.9,
// a (the Holaspirit side) fully contained in b, or one set almost contained
// in the other.
func similar(a, b string) bool {
	if a == b {
		return true
	}
	wa, wb := words(a), words(b)
	if len(wa) == 0 || len(wb) == 0 {
		return len(wa) == len(wb)
	}
	inter := 0
	for w := range wa {
		if wb[w] {
			inter++
		}
	}
	// Every word of a (the Holaspirit text) is still there: nothing was lost,
	// Talkspirit only added (e.g. a mention name Holaspirit had left empty).
	if inter == len(wa) && len(wa) >= 3 {
		return true
	}
	small, large := len(wa), len(wb)
	if small > large {
		small, large = large, small
	}
	union := len(wa) + len(wb) - inter
	if float64(inter)/float64(union) >= 0.9 {
		return true
	}
	return float64(inter)/float64(small) >= 0.95 && float64(inter)/float64(large) >= 0.75
}

// short shortens a text for a report line.
func short(s string, n int) string {
	s = plain(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// textDiff compares two lists of texts as multisets, tolerating formatting.
// It returns the texts only in a and only in b.
func textDiff(a, b []string) (onlyA, onlyB []string) {
	used := make([]bool, len(b))
	for _, x := range a {
		found := false
		for i, y := range b { // exact first
			if !used[i] && norm(x) == norm(y) {
				used[i], found = true, true
				break
			}
		}
		if !found {
			for i, y := range b {
				if !used[i] && sameText(x, y) {
					used[i], found = true, true
					break
				}
			}
		}
		if !found && norm(x) != "" {
			onlyA = append(onlyA, x)
		}
	}
	for i, y := range b {
		if !used[i] && norm(y) != "" {
			onlyB = append(onlyB, y)
		}
	}
	return onlyA, onlyB
}
