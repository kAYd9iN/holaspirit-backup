package compare

import (
	"fmt"
	"sort"
	"time"
)

// Kind classifies a finding.
type Kind string

const (
	// Missing: in Holaspirit, not found in Talkspirit.
	Missing Kind = "FEHLT"
	// MissingPart: the object exists, but part of it is missing (an
	// accountability, a role assignment, a key result).
	MissingPart Kind = "FEHLT_TEIL"
	// DiffUntouched: differs, and the Talkspirit object was not changed since
	// the migration — a migration error is likely.
	DiffUntouched Kind = "ABWEICHUNG_UNVERAENDERT"
	// Moved: found, but under another parent (circle, project).
	Moved Kind = "VERSCHOBEN"
	// OnlyInTS: in Talkspirit, created before the migration ended, but not in
	// the Holaspirit backup — created in Talkspirit itself; not a loss.
	OnlyInTS Kind = "NUR_IN_TALKSPIRIT"
	// DiffEdited: differs, but the Talkspirit object was changed after the
	// migration — probably a deliberate edit.
	DiffEdited Kind = "ABWEICHUNG_BEARBEITET"
	// DiffHSLater: differs, but the Holaspirit object changed after the
	// migration, so the difference is expected.
	DiffHSLater Kind = "ABWEICHUNG_HS_NACH_MIGRATION"
	// NewInTS: created in Talkspirit after the migration — legitimate work.
	NewInTS Kind = "NEU_IN_TALKSPIRIT"
	// Gap: changed or created between the Holaspirit backup and the end of
	// the migration — the backup cannot show that state, so the difference
	// is expected.
	Gap Kind = "ZWISCHEN_BACKUP_UND_MIGRATION"
	// Unverified: absent from the Talkspirit backup, but the Partner API or
	// the backup cannot show it (visibility, a request not made), so the
	// loss is not proven.
	Unverified Kind = "NICHT_BESTAETIGBAR"
	// Orphan: Holaspirit data that already pointed at something deleted in
	// Holaspirit (an agenda item of a deleted meeting) — not a migration loss.
	Orphan Kind = "VERWAIST_IN_HOLASPIRIT"
	// Uncertain: matched on the title alone (no timestamp agreed), so the
	// pairing may be wrong; counts are still right.
	Uncertain Kind = "UNSICHER_ZUGEORDNET"
)

// kindOrder sorts findings from "act on this" to "for information".
var kindOrder = []Kind{Missing, MissingPart, DiffUntouched, Moved, OnlyInTS, Unverified, Uncertain, Orphan, Gap, DiffHSLater, DiffEdited, NewInTS}

// Suspicious reports whether a kind points at the migration.
func (k Kind) Suspicious() bool {
	return k == Missing || k == MissingPart || k == DiffUntouched || k == Moved
}

// Finding is one reported difference.
type Finding struct {
	Area     string
	Kind     Kind
	Circle   string // circle path (or project) the object belongs to, for grouping
	Key      string // human-readable identity (name, title, e-mail)
	What     string // what is wrong, short ("Accountability fehlt")
	Original string // the Holaspirit content (target state, to copy back)
	Current  string // the Talkspirit content (actual state)
	Detail   string // context
	HSID     string
	TSID     string
}

// AreaStats summarizes one area.
type AreaStats struct {
	Area    string
	HS, TS  int
	Matched int
	Kinds   map[Kind]int
	Note    string
}

// NotVerifiable is Holaspirit data the Partner API has no counterpart for.
type NotVerifiable struct {
	What   string
	HS     int
	Reason string
}

// Report is the result of a comparison.
type Report struct {
	HSDir, TSDir       string
	HSTime, TSTime     time.Time
	MigratedAt         time.Time
	MigratedAtGuessed  bool
	MigratedAtEvidence string
	Areas              []*AreaStats
	Findings           []Finding
	NotVerifiable      []NotVerifiable
	Notes              []string
	AuditAfter         map[string]int // audit-log event types after the migration
	AuditAvailable     bool
}

func (r *Report) area(name string) *AreaStats {
	for _, a := range r.Areas {
		if a.Area == name {
			return a
		}
	}
	a := &AreaStats{Area: name, Kinds: map[Kind]int{}}
	r.Areas = append(r.Areas, a)
	return a
}

func (r *Report) add(f Finding) {
	r.Findings = append(r.Findings, f)
	r.area(f.Area).Kinds[f.Kind]++
}

func (r *Report) note(format string, a ...any) {
	r.Notes = append(r.Notes, fmt.Sprintf(format, a...))
}

// KindRank returns the position of a kind from "act on this" to "for information".
func KindRank(k Kind) int {
	for i, x := range kindOrder {
		if x == k {
			return i
		}
	}
	return len(kindOrder)
}

// sortFindings orders findings top-down: area priority, kind, circle, key.
func (r *Report) sortFindings() {
	sort.SliceStable(r.Areas, func(i, j int) bool { return AreaRank(r.Areas[i].Area) < AreaRank(r.Areas[j].Area) })
	sort.SliceStable(r.Findings, func(i, j int) bool {
		a, b := r.Findings[i], r.Findings[j]
		if ra, rb := AreaRank(a.Area), AreaRank(b.Area); ra != rb {
			return ra < rb
		}
		if ka, kb := KindRank(a.Kind), KindRank(b.Kind); ka != kb {
			return ka < kb
		}
		if a.Circle != b.Circle {
			return a.Circle < b.Circle
		}
		return a.Key < b.Key
	})
}

// moved classifies an object found under another parent: suspicious, unless
// the Talkspirit object was changed after the cut-off.
func (r *Report) moved(hsUpdated, tsUpdated time.Time) Kind {
	if k := r.classify(hsUpdated, tsUpdated); k != DiffUntouched {
		return k
	}
	return Moved
}

// classify decides how to read a difference between a matched pair.
// Talkspirit keeps Holaspirit's created_at/updated_at on migrated objects,
// so a later time on the Talkspirit side means a later edit.
func (r *Report) classify(hsUpdated, tsUpdated time.Time) Kind {
	if !hsUpdated.IsZero() && hsUpdated.After(r.MigratedAt) {
		return DiffHSLater
	}
	if !tsUpdated.IsZero() && tsUpdated.After(r.MigratedAt) {
		return DiffEdited
	}
	if r.inGap(tsUpdated) {
		return Gap
	}
	return DiffUntouched
}

// inGap reports whether t lies between the Holaspirit backup and the end of
// the migration.
func (r *Report) inGap(t time.Time) bool {
	return !t.IsZero() && !r.HSTime.IsZero() && t.After(r.HSTime) && !t.After(r.MigratedAt)
}

// extra classifies a Talkspirit object without Holaspirit counterpart.
func (r *Report) extra(tsCreated time.Time) Kind {
	if !tsCreated.IsZero() && tsCreated.After(r.MigratedAt) {
		return NewInTS
	}
	if r.inGap(tsCreated) {
		return Gap
	}
	return OnlyInTS
}

// rec is one object prepared for matching.
type rec struct {
	o    obj
	id   string
	keys []string // one key per matching pass; "" = not matchable in that pass
}

// pair is a matched Holaspirit/Talkspirit couple and the pass that matched it.
type pair struct {
	hs, ts *rec
	pass   int
}

// match pairs hs and ts records pass by pass: in pass i, records with equal
// non-empty keys[i] are paired in input order. Unpaired records move on to the
// next pass. Duplicate keys pair as multisets, so counts stay right even when
// the individual pairing is ambiguous.
func match(hs, ts []*rec, passes int) (pairs []pair, hsLeft, tsLeft []*rec) {
	hsLeft, tsLeft = hs, ts
	for p := 0; p < passes; p++ {
		pool := map[string][]*rec{}
		for _, t := range tsLeft {
			if p < len(t.keys) && t.keys[p] != "" {
				pool[t.keys[p]] = append(pool[t.keys[p]], t)
			}
		}
		used := map[*rec]bool{}
		var rest []*rec
		for _, h := range hsLeft {
			k := ""
			if p < len(h.keys) {
				k = h.keys[p]
			}
			if c := pool[k]; k != "" && len(c) > 0 {
				pairs = append(pairs, pair{hs: h, ts: c[0], pass: p})
				used[c[0]] = true
				pool[k] = c[1:]
				continue
			}
			rest = append(rest, h)
		}
		hsLeft = rest
		var tsRest []*rec
		for _, t := range tsLeft {
			if !used[t] {
				tsRest = append(tsRest, t)
			}
		}
		tsLeft = tsRest
	}
	return pairs, hsLeft, tsLeft
}
