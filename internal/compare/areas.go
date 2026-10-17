package compare

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	areaUsers      = "Personen"
	areaCircles    = "Kreise"
	areaRoles      = "Rollen"
	areaAssign     = "Rollenbesetzungen"
	areaPolicies   = "Richtlinien"
	areaCustom     = "Benutzerdefinierte Felder"
	areaGoals      = "Ziele (OKR)"
	areaTimeframes = "Zeiträume"
	areaProjects   = "Projekte (Boards)"
	areaSections   = "Spalten"
	areaLabels     = "Labels"
	areaTasks      = "Aufgaben"
	areaOpen       = "Offene Spannungen"
	areaMeetings   = "Meetings"
	areaTensions   = "Traktanden & Notizen"
)

// Priority levels, top-down: the organisation's structure first, its
// history last.
const (
	PrioHigh   = "Hoch"
	PrioMedium = "Mittel"
	PrioLow    = "Niedrig"
)

// areaOrder is the report order; areaPrio its priority level.
var areaOrder = []string{areaUsers, areaCircles, areaRoles, areaAssign, areaPolicies,
	areaCustom, areaGoals, areaTimeframes, areaOpen, areaProjects, areaSections, areaLabels, areaTasks,
	areaMeetings, areaTensions}

var areaPrio = map[string]string{
	areaUsers: PrioHigh, areaCircles: PrioHigh, areaRoles: PrioHigh, areaAssign: PrioHigh, areaPolicies: PrioHigh,
	areaCustom: PrioMedium, areaGoals: PrioMedium, areaTimeframes: PrioMedium, areaProjects: PrioMedium,
	areaSections: PrioMedium, areaLabels: PrioMedium, areaTasks: PrioMedium, areaOpen: PrioMedium,
	areaMeetings: PrioLow, areaTensions: PrioLow,
}

// AreaPriority returns the priority level of an area.
func AreaPriority(area string) string { return areaPrio[area] }

// AreaRank returns the position of an area in the report (0 = most important).
func AreaRank(area string) int {
	for i, a := range areaOrder {
		if a == area {
			return i
		}
	}
	return len(areaOrder)
}

// Run compares a Holaspirit backup directory with a Talkspirit one.
// migratedAt is the end of the migration (the cut-off date); zero estimates
// it from the data.
func Run(hsDir, tsDir string, migratedAt time.Time) (*Report, error) {
	hs, err := loadHS(hsDir)
	if err != nil {
		return nil, err
	}
	ts, err := loadTS(tsDir)
	if err != nil {
		return nil, err
	}
	r := &Report{HSDir: hsDir, TSDir: tsDir, HSTime: hs.time, TSTime: ts.time, MigratedAt: migratedAt.UTC()}
	for _, a := range areaOrder {
		r.area(a)
	}
	if migratedAt.IsZero() {
		t, evidence, err := guessMigration(ts)
		if err != nil {
			return nil, err
		}
		r.MigratedAt, r.MigratedAtGuessed, r.MigratedAtEvidence = t, true, evidence
	}
	if !hs.time.IsZero() && hs.time.After(r.MigratedAt) {
		r.note("Das Holaspirit-Backup (%s UTC) ist jünger als der Stichtag. Was in Holaspirit nach dem Stichtag noch geändert wurde, ist als „in Holaspirit nach Stichtag geändert“ eingeordnet und nicht als Verlust.",
			hs.time.Format("02.01.2006 15:04"))
	}
	r.note("Die Talkspirit-API kennt die Holaspirit-IDs nicht. Zugeordnet wird über fachliche Merkmale: E-Mail, Kreispfad + Name, Titel + Zeitpunkt.")
	r.note("Wurde etwas in Talkspirit nach dem Stichtag gelöscht, sieht das aus wie ein Migrationsverlust (die API liefert gelöschte Objekte nicht, das Audit-Log nennt keine Namen). Solche Zeilen in der Arbeitsliste auf „Kein Fehler“ setzen.")
	r.note("Der API-Schlüssel sieht nur, was sein technischer Benutzer sehen darf; private Aufgaben anderer Personen und gesperrte Konten fehlen deshalb auch ohne Migrationsfehler.")

	c := &cmp{r: r, hs: hs, ts: ts, emailOfTS: map[string]string{}, emailOfHS: map[string]string{}, nameOfHS: map[string]string{}}
	c.users()
	c.circles()
	c.roles()
	c.policies()
	c.customFields()
	c.goals()
	c.projects()
	c.tasks()
	c.meetings()
	c.tensions()
	c.notVerifiable()
	c.audit()
	r.sortFindings()
	return r, nil
}

// guessMigration estimates the end of the migration. Talkspirit keeps the
// Holaspirit timestamps on migrated objects, but writes every migrated
// membership with the import time: a single updated_at shared by (nearly) all
// memberships is that time.
func guessMigration(ts *tsSet) (time.Time, string, error) {
	perMinute := map[string]time.Time{}
	counts := map[string]int{}
	for _, m := range ts.memberships {
		if t := tm(m, "updated_at"); !t.IsZero() {
			k := minuteKey(t)
			counts[k]++
			if t.After(perMinute[k]) {
				perMinute[k] = t
			}
		}
	}
	best := ""
	for k, n := range counts {
		if best == "" || n > counts[best] {
			best = k
		}
	}
	if best == "" || counts[best]*10 < len(ts.memberships)*9 {
		return time.Time{}, "", fmt.Errorf("cannot estimate the migration time from the Talkspirit data — pass --migrated-at YYYY-MM-DD[THH:MM]")
	}
	return perMinute[best].Add(time.Minute).Truncate(time.Minute),
		fmt.Sprintf("%d von %d Mitgliedschaften tragen den Import-Zeitpunkt %s UTC", counts[best], len(ts.memberships), perMinute[best].Format("02.01.2006 15:04")), nil
}

type cmp struct {
	r                    *Report
	hs                   *hsSet
	ts                   *tsSet
	emailOfTS, emailOfHS map[string]string // user/member id → normalized e-mail
	nameOfHS             map[string]string // normalized e-mail → display name
	hsCirclePath         map[string]string
	tsCirclePath         map[string]string
	circlePair           map[string]string // HS circle id → TS circle id
	rolePair             map[string]string // HS role id → TS role id
	meetingPair          map[string]string
	boardPair            map[string]string
	tsMembershipsOf      map[string][]obj // TS role id → memberships
	tsRoleless           map[string]bool  // e-mails of active TS users without any role
	tsActive             map[string]bool  // e-mails of active TS users
}

func email(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// person renders an e-mail as "Name <e-mail>" when the name is known.
func (c *cmp) person(e string) string {
	if n := c.nameOfHS[e]; n != "" {
		return n + " <" + e + ">"
	}
	return e
}

// --- people -----------------------------------------------------------------

func (c *cmp) users() {
	a := c.r.area(areaUsers)
	a.HS, a.TS = len(c.hs.members), len(c.ts.users)
	var hs, ts []*rec
	for _, m := range c.hs.members {
		e := email(str(m, "email"))
		c.emailOfHS[str(m, "id")] = e
		c.nameOfHS[e] = str(m, "displayName")
		hs = append(hs, &rec{o: m, id: str(m, "id"), keys: []string{e}})
	}
	for _, u := range c.ts.users {
		e := email(str(u, "email"))
		c.emailOfTS[str(u, "id")] = e
		ts = append(ts, &rec{o: u, id: str(u, "id"), keys: []string{e}})
	}
	pairs, hsLeft, tsLeft := match(hs, ts, 1)
	a.Matched = len(pairs)
	for _, p := range pairs {
		kind := c.r.classify(tm(p.hs.o, "updatedAt"), tm(p.ts.o, "last_activity_at"))
		if hn, tn := str(p.hs.o, "displayName"), str(p.ts.o, "display_name"); norm(hn) != norm(tn) {
			c.r.add(Finding{Area: areaUsers, Kind: kind, Key: personKey(p.hs.o), What: "Name abweichend",
				Original: hn, Current: tn, HSID: p.hs.id, TSID: p.ts.id})
		}
		if hj, tj := str(p.hs.o, "jobTitle"), str(p.ts.o, "job_title"); hj != "" && norm(hj) != norm(tj) {
			c.r.add(Finding{Area: areaUsers, Kind: kind, Key: personKey(p.hs.o), What: "Funktion abweichend",
				Original: hj, Current: tj, HSID: p.hs.id, TSID: p.ts.id})
		}
	}
	for _, h := range hsLeft {
		c.r.add(Finding{Area: areaUsers, Kind: Unverified, Key: personKey(h.o), What: "kein aktives Konto",
			Original: personKey(h.o), HSID: h.id,
			Detail: "kein aktives Talkspirit-Konto mit dieser E-Mail – gesperrt oder nicht migriert (gesperrte/anonymisierte Konten liefert die API nie)"})
	}
	for _, t := range tsLeft {
		key := fmt.Sprintf("%s <%s>", str(t.o, "display_name"), str(t.o, "email"))
		c.r.add(Finding{Area: areaUsers, Kind: c.r.extra(tm(t.o, "created_at")), Key: key, What: "Person nur in Talkspirit",
			Current: key, TSID: t.id})
	}
}

func personKey(m obj) string {
	return fmt.Sprintf("%s <%s>", str(m, "displayName"), str(m, "email"))
}

// --- governance --------------------------------------------------------------

// hsTexts resolves linked accountability/domain ids to their descriptions.
func hsTexts(ids []string, linked map[string]obj) []string {
	var out []string
	for _, id := range ids {
		if o, ok := linked[id]; ok {
			out = append(out, str(o, "description"))
		}
	}
	return out
}

// hsRoleContent returns purpose, accountabilities and domains of a role. A
// transversal (invited) role takes them from its source role.
func (c *cmp) hsRoleContent(role obj) (purpose string, accs, doms []string) {
	purpose = str(role, "purpose")
	accIDs, domIDs := strs(role, "accountabilities"), strs(role, "domains")
	if boolean(role, "isTransversalRole") {
		if v := strs(role, "transversalRoleAccountabilities"); len(v) > 0 {
			accIDs = v
		}
		if v := strs(role, "transversalRoleDomains"); len(v) > 0 {
			domIDs = v
		}
		if src, ok := c.hs.roleByID[str(role, "transversalRole")]; ok && norm(purpose) == "" {
			purpose = str(src, "purpose")
		}
	}
	return purpose, hsTexts(accIDs, c.hs.acc), hsTexts(domIDs, c.hs.dom)
}

// holders returns the e-mails of the current holders of a Holaspirit role.
func (c *cmp) holders(role obj) []string {
	var out []string
	for _, aid := range strs(role, "assignedMembers") {
		am, ok := c.hs.assigned[aid]
		if !ok {
			continue
		}
		if until := tm(am, "assignedUntil"); !until.IsZero() && !c.hs.time.IsZero() && until.Before(c.hs.time) {
			continue // assignment had already ended
		}
		if e := c.emailOfHS[str(am, "member")]; e != "" {
			out = append(out, e)
		}
	}
	return out
}

// roleSheet renders a role's full Holaspirit content, so a missing role can
// be re-created from the report.
func (c *cmp) roleSheet(role obj) string {
	purpose, accs, doms := c.hsRoleContent(role)
	var b strings.Builder
	if p := plain(purpose); p != "" {
		b.WriteString("Purpose: " + p + "\n")
	}
	list := func(title string, items []string) {
		if len(items) == 0 {
			return
		}
		b.WriteString(title + ":\n")
		for _, s := range items {
			b.WriteString("• " + plain(s) + "\n")
		}
	}
	list("Accountabilities", accs)
	list("Domänen", doms)
	var people []string
	for _, e := range c.holders(role) {
		people = append(people, c.person(e))
	}
	list("Besetzt mit", people)
	return strings.TrimSpace(b.String())
}

// part is one content difference of a circle or role.
type part struct{ what, orig, cur string }

// contentDiff compares purpose/accountabilities/domains. missing are texts
// present in Holaspirit and absent in Talkspirit; diffs everything else.
func contentDiff(hsPurpose, tsPurpose string, hsAccs, tsAccs, hsDoms, tsDoms []string) (diffs, missing []part) {
	if norm(hsPurpose) != "" && !sameText(hsPurpose, tsPurpose) {
		if norm(tsPurpose) == "" {
			missing = append(missing, part{"Purpose fehlt", plain(hsPurpose), ""})
		} else {
			diffs = append(diffs, part{"Purpose geändert", plain(hsPurpose), plain(tsPurpose)})
		}
	}
	for _, l := range []struct {
		name   string
		hs, ts []string
	}{{"Accountability", hsAccs, tsAccs}, {"Domäne", hsDoms, tsDoms}} {
		onlyHS, onlyTS := textDiff(l.hs, l.ts)
		for _, s := range onlyHS {
			missing = append(missing, part{l.name + " fehlt", plain(s), ""})
		}
		for _, s := range onlyTS {
			diffs = append(diffs, part{l.name + " nur in Talkspirit", "", plain(s)})
		}
	}
	return diffs, missing
}

// reportContent files the content differences of a matched circle or role.
// Missing parts of an object untouched since the cut-off are migration
// losses; if the object was edited afterwards they may be deliberate.
func (c *cmp) reportContent(f Finding, hsUpd, tsUpd time.Time, diffs, missing []part) {
	kind := c.r.classify(hsUpd, tsUpd)
	for _, m := range missing {
		g := f
		g.Kind, g.What, g.Original, g.Current = MissingPart, m.what, m.orig, m.cur
		if kind != DiffUntouched {
			g.Kind = kind
		}
		c.r.add(g)
	}
	for _, d := range diffs {
		g := f
		g.Kind, g.What, g.Original, g.Current = kind, d.what, d.orig, d.cur
		c.r.add(g)
	}
}

func parentPath(path string) string {
	if i := strings.LastIndex(path, " › "); i >= 0 {
		return path[:i]
	}
	return ""
}

func (c *cmp) circles() {
	a := c.r.area(areaCircles)
	a.HS, a.TS = len(c.hs.circles), len(c.ts.circles)
	c.hsCirclePath = paths(c.hs.circleByID, "parentCircle", func(o obj) string { return str(o, "name") })
	c.tsCirclePath = paths(c.ts.circleByID, "parent_circle_id", func(o obj) string { return str(o, "name") })
	c.circlePair = map[string]string{}

	var hs, ts []*rec
	for _, o := range c.hs.circles {
		id := str(o, "id")
		hs = append(hs, &rec{o: o, id: id, keys: []string{normPath(c.hsCirclePath[id]), norm(str(o, "name"))}})
	}
	for _, o := range c.ts.circles {
		id := str(o, "id")
		ts = append(ts, &rec{o: o, id: id, keys: []string{normPath(c.tsCirclePath[id]), norm(str(o, "name"))}})
	}
	pairs, hsLeft, tsLeft := match(hs, ts, 2)
	a.Matched = len(pairs)
	for _, p := range pairs {
		c.circlePair[p.hs.id] = p.ts.id
		path := c.hsCirclePath[p.hs.id]
		base := Finding{Area: areaCircles, Circle: parentPath(path), Key: str(p.hs.o, "name"), HSID: p.hs.id, TSID: p.ts.id}
		if p.pass == 1 {
			g := base
			g.Kind, g.What, g.Original, g.Current = c.r.moved(tm(p.hs.o, "updatedAt"), tm(p.ts.o, "updated_at")), "Kreis an anderer Stelle", path, c.tsCirclePath[p.ts.id]
			c.r.add(g)
		}
		// A Holaspirit circle's purpose, accountabilities and domains live on
		// its own role.
		role := c.hs.roleByID[str(p.hs.o, "role")]
		purpose, accs, doms := c.hsRoleContent(role)
		diffs, missing := contentDiff(purpose, str(p.ts.o, "purpose"), accs, strs(p.ts.o, "accountabilities"), doms, strs(p.ts.o, "domains"))
		hsUpd := tm(p.hs.o, "updatedAt")
		if ru := tm(role, "updatedAt"); ru.After(hsUpd) {
			hsUpd = ru
		}
		c.reportContent(base, hsUpd, tm(p.ts.o, "updated_at"), diffs, missing)
	}
	for _, h := range hsLeft {
		path := c.hsCirclePath[h.id]
		c.r.add(Finding{Area: areaCircles, Kind: Missing, Circle: parentPath(path), Key: str(h.o, "name"), What: "Kreis fehlt",
			Original: c.roleSheet(c.hs.roleByID[str(h.o, "role")]), HSID: h.id,
			Detail: fmt.Sprintf("%s Rollen", str(h.o, "rolesCount"))})
	}
	for _, t := range tsLeft {
		path := c.tsCirclePath[t.id]
		c.r.add(Finding{Area: areaCircles, Kind: c.r.extra(tm(t.o, "created_at")), Circle: parentPath(path), Key: str(t.o, "name"),
			What: "Kreis nur in Talkspirit", Current: path, TSID: t.id})
	}
}

func (c *cmp) roles() {
	a := c.r.area(areaRoles)
	c.rolePair = map[string]string{}
	c.indexMemberships()
	tsCircleKeys := map[string]bool{}
	for id := range c.ts.circleByID {
		tsCircleKeys[normPath(c.tsCirclePath[id])] = true
	}
	hsPath := func(o obj) string {
		return c.hsCirclePath[str(o, "parentCircle")] + " › " + str(o, "name")
	}
	tsPath := func(o obj) string {
		return c.tsCirclePath[str(o, "parent_circle_id")] + " › " + str(o, "name")
	}

	var hs, ts []*rec
	for _, o := range c.hs.roles {
		if str(o, "circle") != "" {
			continue // the role of a circle: compared under Kreise
		}
		hs = append(hs, &rec{o: o, id: str(o, "id"), keys: []string{normPath(hsPath(o)), norm(str(o, "name"))}})
	}
	for _, o := range c.ts.roles {
		if tsCircleKeys[normPath(tsPath(o))] {
			continue // a circle represented as a role
		}
		ts = append(ts, &rec{o: o, id: str(o, "id"), keys: []string{normPath(tsPath(o)), norm(str(o, "name"))}})
	}
	a.HS, a.TS = len(hs), len(ts)
	pairs, hsLeft, tsLeft := match(hs, ts, 2)
	a.Matched = len(pairs)
	for _, p := range pairs {
		c.rolePair[p.hs.id] = p.ts.id
		base := Finding{Area: areaRoles, Circle: c.hsCirclePath[str(p.hs.o, "parentCircle")], Key: str(p.hs.o, "name"),
			HSID: p.hs.id, TSID: p.ts.id}
		if p.pass == 1 {
			g := base
			g.Kind, g.What, g.Original, g.Current = c.r.moved(tm(p.hs.o, "updatedAt"), tm(p.ts.o, "updated_at")), "Rolle in anderem Kreis", hsPath(p.hs.o), tsPath(p.ts.o)
			c.r.add(g)
		}
		purpose, accs, doms := c.hsRoleContent(p.hs.o)
		diffs, missing := contentDiff(purpose, str(p.ts.o, "purpose"), accs, strs(p.ts.o, "accountabilities"), doms, strs(p.ts.o, "domains"))
		c.reportContent(base, tm(p.hs.o, "updatedAt"), tm(p.ts.o, "updated_at"), diffs, missing)
		c.assignments(base, p.hs.o, p.ts.o)
	}
	for _, h := range hsLeft {
		c.r.add(Finding{Area: areaRoles, Kind: Missing, Circle: c.hsCirclePath[str(h.o, "parentCircle")], Key: str(h.o, "name"),
			What: "Rolle fehlt", Original: c.roleSheet(h.o), HSID: h.id})
	}
	for _, t := range tsLeft {
		c.r.add(Finding{Area: areaRoles, Kind: c.r.extra(tm(t.o, "created_at")), Circle: c.tsCirclePath[str(t.o, "parent_circle_id")],
			Key: str(t.o, "name"), What: "Rolle nur in Talkspirit", Current: plain(str(t.o, "purpose")), TSID: t.id})
	}
}

// indexMemberships groups the Talkspirit memberships by role and reports
// people who held roles in Holaspirit but hold none at all in Talkspirit —
// a migration loss per person rather than per role.
func (c *cmp) indexMemberships() {
	c.tsMembershipsOf = map[string][]obj{}
	c.tsRoleless = map[string]bool{}
	hasRole := map[string]bool{}
	for _, m := range c.ts.memberships {
		if id := str(m, "role_id"); id != "" {
			c.tsMembershipsOf[id] = append(c.tsMembershipsOf[id], m)
		}
		hasRole[c.emailOfTS[str(m, "user_id")]] = true
	}
	for _, r := range c.ts.roles {
		for _, m := range objs(r, "members") {
			hasRole[c.emailOfTS[str(m, "user_id")]] = true
		}
	}
	tsActive := map[string]bool{}
	for _, e := range c.emailOfTS {
		tsActive[e] = true
	}
	c.tsActive = tsActive
	held := map[string][]string{}
	for _, r := range c.hs.roles {
		for _, e := range c.holders(r) {
			held[e] = append(held[e], c.hsCirclePath[str(r, "parentCircle")]+" › "+str(r, "name"))
		}
	}
	var people []string
	for e := range held {
		if tsActive[e] && !hasRole[e] {
			people = append(people, e)
		}
	}
	sort.Strings(people)
	for _, e := range people {
		c.tsRoleless[e] = true
		roles := held[e]
		sort.Strings(roles)
		what := fmt.Sprintf("Alle %d Rollen fehlen", len(roles))
		if len(roles) == 1 {
			what = "Die einzige Rolle fehlt"
		}
		c.r.add(Finding{Area: areaUsers, Kind: MissingPart, Key: c.person(e), What: what,
			Original: strings.Join(roles, "\n"), Current: "aktives Konto, aber keine einzige Rolle oder Mitgliedschaft",
			Detail: "Systematischer Verlust pro Person: die Rollenzuweisungen dieser Person wurden nicht migriert"})
	}
}

// assignments compares who holds a matched role.
func (c *cmp) assignments(base Finding, hsRole, tsRole obj) {
	a := c.r.area(areaAssign)
	base.Area = areaAssign
	hsEmails := c.holders(hsRole)
	tsEmails := map[string]obj{}
	var current []string
	// Role members (?include=members) and the tenant-wide memberships list
	// describe the same assignments; take both.
	members := append(objs(tsRole, "members"), c.tsMembershipsOf[str(tsRole, "id")]...)
	for _, m := range members {
		// An ended mandate (expired election) counts on neither side.
		if exp := tm(m, "expires_at"); !exp.IsZero() && !c.hs.time.IsZero() && exp.Before(c.hs.time) {
			continue
		}
		if e := c.emailOfTS[str(m, "user_id")]; e != "" {
			if _, dup := tsEmails[e]; !dup {
				current = append(current, c.person(e))
			}
			tsEmails[e] = m
		}
	}
	sort.Strings(current)
	if len(current) > 8 {
		current = append(current[:8], fmt.Sprintf("… und %d weitere", len(current)-8))
	}
	a.HS += len(hsEmails)
	a.TS += len(tsEmails)
	seen := map[string]bool{}
	for _, e := range hsEmails {
		seen[e] = true
		if _, ok := tsEmails[e]; ok {
			a.Matched++
			continue
		}
		g := base
		g.Kind, g.What, g.Original, g.Current = MissingPart, "Person fehlt in der Rolle", c.person(e), strings.Join(current, "\n")
		if !c.tsActive[e] {
			g.Kind, g.Detail = Unverified, "Person hat kein aktives Talkspirit-Konto (siehe Personen)"
		} else if c.tsRoleless[e] {
			g.Detail = "Diese Person hat in Talkspirit keine einzige Rolle mehr (siehe Personen)"
		} else if tm(tsRole, "updated_at").After(c.r.MigratedAt) {
			g.Detail = "Rolle wurde nach dem Stichtag geändert"
		}
		c.r.add(g)
	}
	for e, m := range tsEmails {
		if seen[e] {
			continue
		}
		g := base
		g.Kind, g.What, g.Current = c.r.extra(tm(m, "assigned_at")), "zusätzlich besetzt", c.person(e)
		g.HSID = ""
		c.r.add(g)
	}
}

func (c *cmp) policies() {
	a := c.r.area(areaPolicies)
	a.HS, a.TS = len(c.hs.policies), len(c.ts.documents)
	if !c.ts.have["documents"] {
		a.Note = "Talkspirit-Backup enthält keine Dokumente (Scope documents:read fehlt?)"
	} else {
		a.Note = "Talkspirit-Spalte = alle Dokumente; Richtlinien werden über den Titel gesucht"
	}
	var hs, ts []*rec
	for _, o := range c.hs.policies {
		hs = append(hs, &rec{o: o, id: str(o, "id"), keys: []string{norm(str(o, "title"))}})
	}
	for _, o := range c.ts.documents {
		ts = append(ts, &rec{o: o, id: str(o, "id"), keys: []string{norm(str(o, "title"))}})
	}
	pairs, hsLeft, _ := match(hs, ts, 1)
	a.Matched = len(pairs)
	for _, p := range pairs {
		content := c.ts.docContent[p.ts.id]
		if len(content) == 0 {
			continue
		}
		if !sameText(str(p.hs.o, "body"), str(content[0], "content")) {
			c.r.add(Finding{Area: areaPolicies, Kind: c.r.classify(time.Time{}, tm(p.ts.o, "updated_at")),
				Circle: c.hsCirclePath[str(p.hs.o, "circle")], Key: str(p.hs.o, "title"), What: "Text weicht ab",
				Original: plain(str(p.hs.o, "body")), Current: plain(str(content[0], "content")), HSID: p.hs.id, TSID: p.ts.id,
				Detail: fmt.Sprintf("Talkspirit zeigt die veröffentlichte Version %s vom %s", str(content[0], "version_number"),
					tm(content[0], "published_at").Format("02.01.2006"))})
		}
	}
	for _, h := range hsLeft {
		c.r.add(Finding{Area: areaPolicies, Kind: Missing, Circle: c.hsCirclePath[str(h.o, "circle")], Key: str(h.o, "title"),
			What: "Richtlinie fehlt", Original: plain(str(h.o, "body")), HSID: h.id,
			Detail: "kein Talkspirit-Dokument mit diesem Titel"})
	}
}

func (c *cmp) customFields() {
	a := c.r.area(areaCustom)
	tsDefs := map[string]string{} // normalized name → Talkspirit entity type
	for typ, list := range map[string][]obj{"Kreis": c.ts.cfCircle, "Rolle": c.ts.cfRole, "Person": c.ts.cfUser} {
		for _, d := range list {
			tsDefs[norm(str(d, "name"))] = typ
		}
	}
	a.TS = len(tsDefs)
	for _, f := range c.hs.customFields {
		if _, ok := tsDefs[norm(str(f, "name"))]; ok {
			a.HS++
			a.Matched++
			continue
		}
		a.HS++
		c.r.add(Finding{Area: areaCustom, Kind: Missing, Key: str(f, "name"), What: "Felddefinition fehlt",
			Original: fmt.Sprintf("%s (%s)", str(f, "name"), str(f, "applyTo")), HSID: str(f, "id")})
	}

	// Values. Holaspirit keys them by apiName as {apiName, name, value};
	// Talkspirit lists {key, name, value | values}. A circle's field lives on
	// the circle's own role in Holaspirit and on the circle in Talkspirit.
	tsRoles, tsUsers := byID(c.ts.roles), byID(c.ts.users)
	for _, hr := range c.hs.rolesCF {
		var target obj
		var key, circle string
		if circleID := str(hr, "circle"); circleID != "" {
			target = c.ts.circleByID[c.circlePair[circleID]]
			key, circle = str(hr, "name"), parentPath(c.hsCirclePath[circleID])
		} else {
			target = tsRoles[c.rolePair[str(hr, "id")]]
			key, circle = str(hr, "name"), c.hsCirclePath[str(hr, "parentCircle")]
		}
		if target != nil {
			c.fieldValues(hr, target, key, circle, tm(hr, "updatedAt"))
		}
	}
	for _, m := range c.hs.members {
		var tu obj
		for id, e := range c.emailOfTS {
			if e == email(str(m, "email")) {
				tu = tsUsers[id]
			}
		}
		if tu != nil {
			c.fieldValues(m, tu, personKey(m), "", tm(m, "updatedAt"))
		}
	}
}

// fieldValue renders a custom-field value of either side as text.
func fieldValue(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return plain(x)
	case []any:
		var parts []string
		for _, e := range x {
			if s := fieldValue(e); s != "" {
				parts = append(parts, s)
			}
		}
		sort.Strings(parts)
		return strings.Join(parts, ", ")
	case map[string]any:
		if val, ok := x["value"]; ok {
			return fieldValue(val)
		}
		for _, k := range []string{"label", "name", "title"} {
			if s, ok := x[k].(string); ok {
				return s
			}
		}
	}
	return fmt.Sprint(v)
}

// fieldValues compares the filled custom fields of a Holaspirit object with
// its Talkspirit counterpart.
func (c *cmp) fieldValues(hs, ts obj, key, circle string, hsUpd time.Time) {
	a := c.r.area(areaCustom)
	vals, _ := hs["customFields"].(map[string]any)
	for apiName, raw := range vals {
		hv := fieldValue(raw)
		if norm(hv) == "" {
			continue
		}
		name := apiName
		if m, ok := raw.(map[string]any); ok {
			if n, ok := m["name"].(string); ok && n != "" {
				name = n
			}
		}
		tv := ""
		for _, f := range objs(ts, "custom_fields") {
			if norm(str(f, "name")) == norm(name) || str(f, "key") == apiName || str(f, "key") == "custom_"+apiName {
				tv = fieldValue(f["value"])
				if tv == "" {
					tv = fieldValue(f["values"])
				}
			}
		}
		a.HS++
		if sameText(hv, tv) {
			a.Matched++
			continue
		}
		kind := MissingPart
		if norm(tv) != "" {
			kind = c.r.classify(hsUpd, tm(ts, "updated_at"))
		}
		c.r.add(Finding{Area: areaCustom, Kind: kind, Circle: circle, Key: key, What: fmt.Sprintf("Feld „%s“ %s", name, yesNo(norm(tv) == "", "leer", "abweichend")),
			Original: hv, Current: tv, HSID: str(hs, "id"), TSID: str(ts, "id")})
	}
}

func (c *cmp) goals() {
	a := c.r.area(areaGoals)
	a.HS, a.TS = len(c.hs.okrs), len(c.ts.goals)
	var hs, ts []*rec
	for _, o := range c.hs.okrs {
		hs = append(hs, &rec{o: o, id: str(o, "id"), keys: []string{norm(str(o, "title"))}})
	}
	for _, o := range c.ts.goals {
		ts = append(ts, &rec{o: o, id: str(o, "id"), keys: []string{norm(str(o, "title"))}})
	}
	krTitles := func(o obj) []string {
		var out []string
		for _, id := range strs(o, "results") {
			if kr, ok := c.hs.krByID[id]; ok {
				out = append(out, str(kr, "title"))
			}
		}
		return out
	}
	pairs, hsLeft, tsLeft := match(hs, ts, 1)
	a.Matched = len(pairs)
	for _, p := range pairs {
		var tsKR []string
		for _, kr := range c.ts.keyResults[p.ts.id] {
			tsKR = append(tsKR, str(kr, "title"))
		}
		onlyHS, _ := textDiff(krTitles(p.hs.o), tsKR)
		for _, s := range onlyHS {
			k := MissingPart
			if tm(p.ts.o, "updated_at").After(c.r.MigratedAt) {
				k = DiffEdited
			}
			c.r.add(Finding{Area: areaGoals, Kind: k, Circle: c.hsCirclePath[str(p.hs.o, "circle")], Key: str(p.hs.o, "title"),
				What: "Key Result fehlt", Original: plain(s), Current: strings.Join(tsKR, "\n"), HSID: p.hs.id, TSID: p.ts.id})
		}
	}
	for _, h := range hsLeft {
		orig := plain(str(h.o, "description"))
		if krs := krTitles(h.o); len(krs) > 0 {
			orig = strings.TrimSpace(orig + "\nKey Results:\n• " + strings.Join(krs, "\n• "))
		}
		c.r.add(Finding{Area: areaGoals, Kind: Missing, Circle: c.hsCirclePath[str(h.o, "circle")], Key: str(h.o, "title"),
			What: "Ziel fehlt", Original: orig, HSID: h.id, Detail: "Status " + str(h.o, "status")})
	}
	for _, t := range tsLeft {
		c.r.add(Finding{Area: areaGoals, Kind: c.r.extra(tm(t.o, "created_at")), Key: str(t.o, "title"), What: "Ziel nur in Talkspirit", TSID: t.id})
	}

	// Timeframes: Holaspirit has recurring cadences ("Monatlich"), Talkspirit
	// concrete periods ("2026-05"), so the lists cannot be compared; what
	// must survive is that every goal keeps a period.
	b := c.r.area(areaTimeframes)
	b.Note = "Holaspirit: wiederkehrende Rhythmen, Talkspirit: konkrete Perioden – geprüft wird, ob jedes Ziel einen Zeitraum behalten hat"
	for _, p := range pairs {
		if str(p.hs.o, "timeframe") == "" {
			continue
		}
		b.HS++
		if len(strs(p.ts.o, "time_period_ids")) > 0 {
			b.TS++
			b.Matched++
			continue
		}
		c.r.add(Finding{Area: areaTimeframes, Kind: MissingPart, Circle: c.hsCirclePath[str(p.hs.o, "circle")], Key: str(p.hs.o, "title"),
			What: "Zeitraum am Ziel fehlt", Original: c.hs.timeframeName[str(p.hs.o, "timeframe")], HSID: p.hs.id, TSID: p.ts.id})
	}
}

// --- operations ---------------------------------------------------------------

func (c *cmp) projects() {
	a := c.r.area(areaProjects)
	a.HS, a.TS = len(c.hs.boards), len(c.ts.projects)
	c.boardPair = map[string]string{}
	// Several boards share a name (copies of templates); their archive state
	// and column set tell them apart.
	hsCols := map[string][]string{}
	for _, col := range c.hs.columns {
		hsCols[str(col, "board")] = append(hsCols[str(col, "board")], norm(str(col, "name")))
	}
	sig := func(names []string) string {
		sort.Strings(names)
		return strings.Join(names, "|")
	}
	var hs, ts []*rec
	for _, o := range c.hs.boards {
		n, arch := norm(str(o, "title")), yesNo(str(o, "archivedAt") != "", "a", "o")
		hs = append(hs, &rec{o: o, id: str(o, "id"), keys: []string{n + "|" + arch + "|" + sig(hsCols[str(o, "id")]), n + "|" + arch, n}})
	}
	for _, o := range c.ts.projects {
		var cols []string
		for _, s := range c.ts.sections[str(o, "id")] {
			cols = append(cols, norm(str(s, "name")))
		}
		n, arch := norm(str(o, "name")), yesNo(boolean(o, "archived"), "a", "o")
		ts = append(ts, &rec{o: o, id: str(o, "id"), keys: []string{n + "|" + arch + "|" + sig(cols), n + "|" + arch, n}})
	}
	pairs, hsLeft, tsLeft := match(hs, ts, 3)
	a.Matched = len(pairs)
	for _, p := range pairs {
		c.boardPair[p.hs.id] = p.ts.id
		if hsArch, tsArch := str(p.hs.o, "archivedAt") != "", boolean(p.ts.o, "archived"); hsArch != tsArch {
			c.r.add(Finding{Area: areaProjects, Kind: c.r.classify(time.Time{}, tm(p.ts.o, "updated_at")), Key: str(p.hs.o, "title"),
				What: "Archiv-Status abweichend", Original: yesNo(hsArch, "archiviert", "aktiv"), Current: yesNo(tsArch, "archiviert", "aktiv"),
				HSID: p.hs.id, TSID: p.ts.id})
		}
	}
	for _, h := range hsLeft {
		c.r.add(Finding{Area: areaProjects, Kind: Missing, Key: str(h.o, "title"), What: "Projekt fehlt", HSID: h.id,
			Original: str(h.o, "title"),
			Detail:   fmt.Sprintf("%s Aufgaben%s", str(h.o, "tasksCount"), yesNo(boolean(h.o, "private"), ", privat", ""))})
	}
	for _, t := range tsLeft {
		c.r.add(Finding{Area: areaProjects, Kind: c.r.extra(tm(t.o, "created_at")), Key: str(t.o, "name"), What: "Projekt nur in Talkspirit", TSID: t.id})
	}

	// columns ↔ sections and labels ↔ labels, within matched projects
	boards := byID(c.hs.boards)
	for _, sub := range []struct {
		area, what string
		hsList     []obj
		ts         map[string][]obj
		file       string
	}{{areaSections, "Spalte fehlt", c.hs.columns, c.ts.sections, "sections"}, {areaLabels, "Label fehlt", c.hs.labels, c.ts.projLabels, "labels"}} {
		s := c.r.area(sub.area)
		if !c.ts.have[sub.file] {
			s.Note = "nicht im Talkspirit-Backup"
		}
		for _, o := range sub.hsList {
			pid, ok := c.boardPair[str(o, "board")]
			if !ok {
				continue // board itself missing: reported there
			}
			s.HS++
			var names []string
			found := false
			for _, t := range sub.ts[pid] {
				names = append(names, str(t, "name"))
				if norm(str(t, "name")) == norm(str(o, "name")) {
					found = true
				}
			}
			if found {
				s.Matched++
				continue
			}
			c.r.add(Finding{Area: sub.area, Kind: MissingPart, Circle: str(boards[str(o, "board")], "title"), Key: str(o, "name"),
				What: sub.what, Original: str(o, "name"), Current: strings.Join(names, ", "), HSID: str(o, "id"), TSID: pid})
		}
		for _, pid := range c.boardPair {
			s.TS += len(sub.ts[pid])
		}
	}
}

func yesNo(b bool, yes, no string) string {
	if b {
		return yes
	}
	return no
}

func (c *cmp) tasks() {
	a := c.r.area(areaTasks)
	a.HS, a.TS = len(c.hs.tasks), len(c.ts.tasks)
	if !c.ts.have["subtasks"] {
		a.Note = "Unteraufgaben fehlen im Talkspirit-Backup"
	}
	// Holaspirit marks a task done by its status or by checking it off.
	hsDone := func(o obj) bool {
		return str(o, "status") == "done" || boolean(o, "checked") || str(o, "completedAt") != ""
	}
	tsDone := func(o obj) bool {
		st := strings.ToLower(str(o, "status"))
		return str(o, "completed_at") != "" || st == "done" || st == "completed" || st == "closed"
	}
	state := func(done, archived bool) string { return yesNo(done, "d", "o") + yesNo(archived, "a", "-") }
	// Passes: 0 title+minute+state (duplicates created at the same moment),
	// 1 title+minute, 2 title+day, 3 project+title, 4 title only.
	var hs, ts []*rec
	for _, o := range c.hs.tasks {
		title, t := norm(str(o, "title")), tm(o, "createdAt")
		pk := ""
		if p := c.boardPair[str(o, "board")]; p != "" {
			pk = key2(p, title)
		}
		st := state(hsDone(o), str(o, "archivedAt") != "")
		hs = append(hs, &rec{o: o, id: str(o, "id"), keys: []string{key2(key2(title, minuteKey(t)), st), key2(title, minuteKey(t)), key2(title, dayKey(t)), pk, title}})
	}
	for _, o := range c.ts.tasks {
		title, t := norm(str(o, "title")), tm(o, "created_at")
		st := state(tsDone(o), boolean(o, "archived"))
		ts = append(ts, &rec{o: o, id: str(o, "id"), keys: []string{key2(key2(title, minuteKey(t)), st), key2(title, minuteKey(t)), key2(title, dayKey(t)), key2(str(o, "project_id"), title), title}})
	}
	pairs, hsLeft, tsLeft := match(hs, ts, 5)
	a.Matched = len(pairs)
	boards := byID(c.hs.boards)
	board := func(o obj) string {
		if b := str(o, "boardTitle"); b != "" {
			return b
		}
		return str(boards[str(o, "board")], "title")
	}
	taskPair := map[string]string{}
	hsTasks := byID(c.hs.tasks)
	for _, p := range pairs {
		taskPair[p.hs.id] = p.ts.id
		base := Finding{Area: areaTasks, Circle: board(p.hs.o), Key: taskKey(p.hs.o), HSID: p.hs.id, TSID: p.ts.id}
		if p.pass == 4 {
			g := base
			g.Kind, g.What, g.Detail = Uncertain, "nur über den Titel zugeordnet", "Talkspirit-Aufgabe vom "+str(p.ts.o, "created_at")
			c.r.add(g)
		}
		kind := c.r.classify(tm(p.hs.o, "updatedAt"), tm(p.ts.o, "updated_at"))
		// Talkspirit archives the sub-tasks of an archived task and archived
		// some done tasks on import; neither loses anything.
		hsArch, tsArch := str(p.hs.o, "archivedAt") != "", boolean(p.ts.o, "archived")
		parentArchived := str(hsTasks[str(p.hs.o, "parent")], "archivedAt") != ""
		if hsArch != tsArch && !(tsArch && (hsDone(p.hs.o) || parentArchived)) {
			g := base
			g.Kind, g.What, g.Original, g.Current = kind, "Archiv-Status abweichend", yesNo(hsArch, "archiviert", "aktiv"), yesNo(tsArch, "archiviert", "aktiv")
			c.r.add(g)
		}
		if hd, td := hsDone(p.hs.o), tsDone(p.ts.o); hd != td {
			g := base
			g.Kind, g.What, g.Original, g.Current = kind, "Erledigt-Status abweichend", yesNo(hd, "erledigt", "offen"), yesNo(td, "erledigt", "offen")+" ("+str(p.ts.o, "status")+")"
			c.r.add(g)
		}
	}
	// unverifiable reports why a task absent from the Talkspirit backup may
	// still exist in Talkspirit, or "" when its absence is proven.
	var unverifiable func(o obj, depth int) string
	unverifiable = func(o obj, depth int) string {
		parent := str(o, "parent")
		if parent == "" {
			if str(o, "board") == "" {
				return "Aufgabe ohne Projekt: die API zeigt sie nur, wenn der technische Benutzer an ihr beteiligt ist"
			}
			return ""
		}
		if tp, ok := taskPair[parent]; ok {
			if !c.ts.subtaskParents[tp] {
				return "Unteraufgabe: ihre Unteraufgaben wurden im Backup nicht abgefragt (v0.6.0 fragte nur bei subtask_count > 0, und dieser Zähler übergeht archivierte Unteraufgaben) – mit v0.6.1 erneut sichern"
			}
			return ""
		}
		if p, ok := hsTasks[parent]; ok && depth < 10 {
			if why := unverifiable(p, depth+1); why != "" {
				return "übergeordnete Aufgabe nicht bestätigbar: " + why
			}
		}
		return ""
	}
	for _, h := range hsLeft {
		detail := fmt.Sprintf("Status %s%s", yesNo(hsDone(h.o), "erledigt", "offen"), yesNo(str(h.o, "archivedAt") != "", ", archiviert", ""))
		if boolean(h.o, "private") {
			detail += ", privat"
		}
		if str(h.o, "parent") != "" {
			detail += ", Unteraufgabe"
		}
		kind, what := Missing, "Aufgabe fehlt"
		if why := unverifiable(h.o, 0); why != "" {
			kind, what, detail = Unverified, "Aufgabe nicht im Backup", why+" ("+detail+")"
		}
		c.r.add(Finding{Area: areaTasks, Kind: kind, Circle: board(h.o), Key: taskKey(h.o), What: what,
			Original: strings.TrimSpace(str(h.o, "title") + "\n" + clip(plain(str(h.o, "body")), 2000)), HSID: h.id, Detail: detail})
	}
	for _, t := range tsLeft {
		c.r.add(Finding{Area: areaTasks, Kind: c.r.extra(tm(t.o, "created_at")), Key: str(t.o, "title"), What: "Aufgabe nur in Talkspirit", TSID: t.id})
	}
}

func taskKey(o obj) string {
	return fmt.Sprintf("%s (%s)", str(o, "title"), tm(o, "createdAt").Format("02.01.2006"))
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func (c *cmp) meetings() {
	a := c.r.area(areaMeetings)
	a.HS, a.TS = len(c.hs.meetings), len(c.ts.meetings)
	c.meetingPair = map[string]string{}
	// Most Holaspirit meetings have no title (the template names them), so
	// the circle is the second identity. Passes: 0 title+minute,
	// 1 circle+minute, 2 title+day, 3 circle+day, 4 minute only, 5 title only.
	var hs, ts []*rec
	for _, o := range c.hs.meetings {
		t, title := hsMeetingTime(o), norm(str(o, "title"))
		circle := c.circlePair[str(o, "circle")]
		hs = append(hs, &rec{o: o, id: str(o, "id"), keys: []string{key2(title, minuteKey(t)), key2(circle, minuteKey(t)),
			key2(title, dayKey(t)), key2(circle, dayKey(t)), minuteKey(t), title}})
	}
	for _, o := range c.ts.meetings {
		t, title := tm(o, "start_time"), norm(str(o, "title"))
		if t.IsZero() {
			t = tm(o, "created_at")
		}
		circle := str(o, "circle_id")
		ts = append(ts, &rec{o: o, id: str(o, "id"), keys: []string{key2(title, minuteKey(t)), key2(circle, minuteKey(t)),
			key2(title, dayKey(t)), key2(circle, dayKey(t)), minuteKey(t), title}})
	}
	pairs, hsLeft, tsLeft := match(hs, ts, 6)
	a.Matched = len(pairs)
	for _, p := range pairs {
		c.meetingPair[p.hs.id] = p.ts.id
		if p.pass >= 4 {
			c.r.add(Finding{Area: areaMeetings, Kind: Uncertain, Circle: c.hsCirclePath[str(p.hs.o, "circle")], Key: c.meetingKey(p.hs.o),
				What: "nur über Zeitpunkt oder Titel zugeordnet", Current: fmt.Sprintf("%s (%s)", str(p.ts.o, "title"), str(p.ts.o, "start_time")),
				HSID: p.hs.id, TSID: p.ts.id})
		}
	}
	for _, h := range hsLeft {
		when := "vergangen"
		if t := tm(h.o, "scheduledAt"); t.After(c.r.MigratedAt) {
			when = "nach dem Stichtag geplant (Serie evtl. neu angelegt)"
		}
		if str(h.o, "scheduledAt") == "" {
			when = "Governance-Änderung ausserhalb eines Meetings (Holaspirit-eigenes Konstrukt)"
		}
		c.r.add(Finding{Area: areaMeetings, Kind: Missing, Circle: c.hsCirclePath[str(h.o, "circle")], Key: c.meetingKey(h.o),
			What: "Meeting fehlt", HSID: h.id,
			Detail: fmt.Sprintf("%s; Status %s; %s Traktanden", when, str(h.o, "status"), str(h.o, "tensionsCount"))})
	}
	for _, t := range tsLeft {
		c.r.add(Finding{Area: areaMeetings, Kind: c.r.extra(tm(t.o, "created_at")), Circle: c.tsCirclePath[str(t.o, "circle_id")],
			Key: fmt.Sprintf("%s (%s)", str(t.o, "title"), str(t.o, "start_time")), What: "Meeting nur in Talkspirit", TSID: t.id})
	}
}

// hsMeetingTime is when a Holaspirit meeting took place. Meetings without a
// date ("Evolution outside meeting") start, in Talkspirit, at the moment they
// were opened; failing that, their creation.
func hsMeetingTime(o obj) time.Time {
	for _, k := range []string{"scheduledAt", "openedAt", "createdAt"} {
		if t := tm(o, k); !t.IsZero() {
			return t
		}
	}
	return time.Time{}
}

func (c *cmp) meetingKey(o obj) string {
	title := strings.TrimSpace(str(o, "title"))
	if title == "" {
		title = c.hs.meetingTemplates[str(o, "meetingTemplate")]
	}
	if title == "" {
		title = "Meeting"
	}
	return fmt.Sprintf("%s, %s UTC", title, hsMeetingTime(o).Format("02.01.2006 15:04"))
}

func key2(a, b string) string {
	if a == "" || b == "" {
		return ""
	}
	return a + "|" + b
}

// tensions matches Holaspirit tensions (agenda items, notes and proposals of
// meetings) against Talkspirit tensions and meeting agenda items.
func (c *cmp) tensions() {
	a := c.r.area(areaTensions)
	agendaCount := 0
	for _, l := range c.ts.agenda {
		agendaCount += len(l)
	}
	a.HS, a.TS = len(c.hs.tensions), len(c.ts.tensions)+agendaCount
	a.Note = fmt.Sprintf("Talkspirit = %d Spannungen + %d Traktanden", len(c.ts.tensions), agendaCount)
	if !c.ts.have["meeting-agenda-items"] {
		a.Note += "; Traktanden fehlen im Talkspirit-Backup (mit --quick erstellt?)"
	}

	// keys: 0 meeting+title, 1 tension title+minute, 2 agenda title+minute,
	// 3 tension title+day, 4 agenda title+day, 5 title only
	var hs, ts []*rec
	for _, o := range c.hs.tensions {
		title, t := norm(str(o, "name")), tm(o, "createdAt")
		mk := ""
		if m := c.meetingPair[str(o, "meeting")]; m != "" {
			mk = key2(m, title)
		}
		hs = append(hs, &rec{o: o, id: str(o, "id"), keys: []string{mk,
			key2(title, minuteKey(t)), key2(title, minuteKey(t)), key2(title, dayKey(t)), key2(title, dayKey(t)), title}})
	}
	for _, o := range c.ts.tensions {
		title, t := norm(str(o, "title")), tm(o, "created_at")
		ts = append(ts, &rec{o: o, id: str(o, "id"), keys: []string{"", key2(title, minuteKey(t)), "", key2(title, dayKey(t)), "", title}})
	}
	for mid, list := range c.ts.agenda {
		for _, o := range list {
			title, t := norm(str(o, "title")), tm(o, "created_at")
			ts = append(ts, &rec{o: o, id: str(o, "id"), keys: []string{key2(mid, title), "", key2(title, minuteKey(t)), "", key2(title, dayKey(t)), title}})
		}
	}
	pairs, hsLeft, tsLeft := match(hs, ts, 6)
	a.Matched = len(pairs)
	hsMeetings := byID(c.hs.meetings)
	// Open tensions (not attached to a meeting) are current work: own area.
	open := c.r.area(areaOpen)
	open.Note = "Holaspirit-Spannungen ohne Meeting (Backlog); Talkspirit-Spalte = offene Talkspirit-Spannungen"
	for _, t := range c.ts.tensions {
		if st := strings.ToUpper(str(t, "status")); st != "RESOLVED" && st != "CLOSED" {
			open.TS++
		}
	}
	for _, h := range hs {
		if str(h.o, "meeting") == "" {
			open.HS++
			a.HS--
		}
	}
	for _, p := range pairs {
		if str(p.hs.o, "meeting") == "" {
			open.Matched++
			a.Matched--
		}
	}
	meetingOf := func(o obj) string {
		if m, ok := hsMeetings[str(o, "meeting")]; ok {
			return c.meetingKey(m)
		}
		return ""
	}
	for _, p := range pairs {
		if p.pass == 5 {
			c.r.add(Finding{Area: areaTensions, Kind: Uncertain, Circle: c.hsCirclePath[str(p.hs.o, "circle")], Key: tensionKey(p.hs.o),
				What: "nur über den Titel zugeordnet", Detail: meetingOf(p.hs.o), HSID: p.hs.id, TSID: p.ts.id})
		}
	}
	// Holaspirit carries an unresolved item from meeting to meeting and keeps
	// backlog copies; Talkspirit has it once. A title that exists somewhere
	// in Talkspirit is therefore not counted as lost.
	tsTitles := map[string]bool{}
	var tsWords []map[string]bool
	var tsNames []string
	for _, t := range ts {
		if !tsTitles[t.keys[5]] {
			tsWords = append(tsWords, words(t.keys[5]))
			tsNames = append(tsNames, str(t.o, "title"))
		}
		tsTitles[t.keys[5]] = true
	}
	// Every other Talkspirit text, to find an item that survived only inside
	// another one (a description, a task, a comment).
	var corpus strings.Builder
	for _, t := range c.ts.tensions {
		corpus.WriteString(strings.ToLower(plain(str(t, "description")) + " " + plain(str(t, "context")) + " " + plain(str(t, "resolution_note")) + "\n"))
	}
	for _, list := range c.ts.agenda {
		for _, a := range list {
			corpus.WriteString(strings.ToLower(plain(str(a, "description")) + "\n"))
		}
	}
	for _, t := range c.ts.tasks {
		corpus.WriteString(strings.ToLower(str(t, "title") + " " + plain(str(t, "description")) + "\n"))
	}
	text := corpus.String()
	// similarTitle returns the most similar Talkspirit title (word Jaccard
	// ≥ 0.6), or "".
	similarTitle := func(name string) string {
		w := words(norm(name))
		best, bestName := 0.0, ""
		for i, tw := range tsWords {
			inter := 0
			for x := range w {
				if tw[x] {
					inter++
				}
			}
			if union := len(w) + len(tw) - inter; union > 0 {
				if s := float64(inter) / float64(union); s > best {
					best, bestName = s, tsNames[i]
				}
			}
		}
		if best >= 0.6 {
			return bestName
		}
		return ""
	}
	for _, h := range hsLeft {
		mid := str(h.o, "meeting")
		if tsTitles[norm(str(h.o, "name"))] {
			area := areaTensions
			if mid == "" {
				area = areaOpen
			}
			c.r.add(Finding{Area: area, Kind: Uncertain, Circle: c.hsCirclePath[str(h.o, "circle")], Key: tensionKey(h.o),
				What: "Titel in Talkspirit vorhanden", Original: str(h.o, "name"), HSID: h.id,
				Detail: "in Holaspirit mehrfach geführt (Backlog oder über mehrere Meetings), in Talkspirit einmal vorhanden"})
			continue
		}
		if _, ok := hsMeetings[mid]; mid != "" && !ok {
			c.r.add(Finding{Area: areaTensions, Kind: Orphan, Circle: c.hsCirclePath[str(h.o, "circle")], Key: tensionKey(h.o),
				What: "Traktandum eines gelöschten Meetings", Original: str(h.o, "name"), HSID: h.id,
				Detail: "das Meeting fehlt schon im Holaspirit-Backup (in Holaspirit gelöscht)"})
			continue
		}
		sim := similarTitle(str(h.o, "name"))
		if name := strings.ToLower(strings.TrimSpace(str(h.o, "name"))); sim == "" && len([]rune(name)) >= 12 && strings.Contains(text, name) {
			sim = "Titel kommt in einem anderen Talkspirit-Text vor (Beschreibung, Aufgabe oder Kommentar)"
		}
		if sim != "" {
			area := areaTensions
			if mid == "" {
				area = areaOpen
			}
			c.r.add(Finding{Area: area, Kind: Uncertain, Circle: c.hsCirclePath[str(h.o, "circle")], Key: tensionKey(h.o),
				What: "ähnlicher Titel in Talkspirit", Original: str(h.o, "name"), Current: sim, HSID: h.id,
				Detail: "vermutlich umbenannt übernommen – stichprobenartig prüfen"})
			continue
		}
		if mid == "" {
			c.r.add(Finding{Area: areaOpen, Kind: Missing, Circle: c.hsCirclePath[str(h.o, "circle")], Key: tensionKey(h.o),
				What:     yesNo(str(h.o, "tensionBehavior") == "proposal", "Offener Governance-Vorschlag fehlt", "Offene Spannung fehlt"),
				Original: strings.TrimSpace(str(h.o, "name") + "\n" + clip(plain(str(h.o, "body")), 2000)), HSID: h.id,
				Detail: "Status " + str(h.o, "status") + ", keinem Meeting zugeordnet"})
			continue
		}
		what := "Notiz fehlt"
		if str(h.o, "tensionBehavior") == "proposal" {
			what = "Governance-Vorschlag fehlt"
		}
		detail := "Status " + str(h.o, "status")
		if m := meetingOf(h.o); m != "" {
			detail += "; Meeting " + m
			if c.meetingPair[str(h.o, "meeting")] == "" {
				detail += " (Meeting selbst fehlt)"
			}
		}
		c.r.add(Finding{Area: areaTensions, Kind: Missing, Circle: c.hsCirclePath[str(h.o, "circle")], Key: tensionKey(h.o),
			What: what, Original: strings.TrimSpace(str(h.o, "name") + "\n" + clip(plain(str(h.o, "body")), 2000)), HSID: h.id, Detail: detail})
	}
	for _, t := range tsLeft {
		c.r.add(Finding{Area: areaTensions, Kind: c.r.extra(tm(t.o, "created_at")), Key: str(t.o, "title"), What: "nur in Talkspirit", TSID: t.id})
	}
}

func tensionKey(o obj) string {
	return fmt.Sprintf("%s (%s)", str(o, "name"), tm(o, "createdAt").Format("02.01.2006"))
}

// --- context -----------------------------------------------------------------

func (c *cmp) notVerifiable() {
	reasons := map[string]string{
		"checklists":            "Checklisten: kein Endpunkt in der Partner API",
		"metrics":               "Metriken: kein Endpunkt in der Partner API",
		"publications":          "Publikationen: kein Endpunkt (evtl. als Dokument migriert)",
		"publications-revision": "Publikations-Versionen: kein Endpunkt",
		"attachments":           "Anhänge/Links an Spannungen, Rollen, Checklisten: nur Aufgaben-Anhänge sind lesbar",
		"calendars":             "Kalender-Anbindungen: kein Endpunkt",
		"highlights":            "Highlights: kein Endpunkt",
		"meetingtemplates":      "Meeting-Vorlagen: kein Endpunkt (Meetings tragen nur eine template_id)",
		"meetings-recurrences":  "Meeting-Serien: kein Endpunkt (Meetings tragen nur eine series_id)",
		"webhooks":              "Webhooks: in Talkspirit (noch) nicht vorhanden",
		"templates":             "Rollen-Vorlagen: IDs wurden bei der Migration geändert; Inhalt nicht 1:1 vergleichbar",
		"comment-feeds":         "Kommentar-Feeds: kein Gegenstück",
	}
	names := make([]string, 0, len(c.hs.counts))
	for n := range c.hs.counts {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if c.hs.counts[n] == 0 {
			continue
		}
		c.r.NotVerifiable = append(c.r.NotVerifiable, NotVerifiable{What: n, HS: c.hs.counts[n], Reason: reasons[n]})
	}
	c.r.NotVerifiable = append(c.r.NotVerifiable, NotVerifiable{What: "proposals", HS: countBehavior(c.hs.tensions, "proposal"),
		Reason: "Inhalt von Governance-Vorschlägen (rawProposal, Abstimmungen): die API liefert nur Titel/Beschreibung"})
}

func countBehavior(list []obj, b string) int {
	n := 0
	for _, o := range list {
		if str(o, "tensionBehavior") == b {
			n++
		}
	}
	return n
}

func (c *cmp) audit() {
	if !c.ts.have["audit-logs"] {
		return
	}
	c.r.AuditAvailable = true
	c.r.AuditAfter = map[string]int{}
	for _, e := range c.ts.auditLogs {
		if tm(e, "occurred_at").After(c.r.MigratedAt) {
			c.r.AuditAfter[str(e, "event_type")]++
		}
	}
}
