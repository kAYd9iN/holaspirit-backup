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
	areaGoals      = "Ziele (OKR)"
	areaTimeframes = "Zeiträume"
	areaCustom     = "Benutzerdefinierte Felder"
	areaProjects   = "Projekte (Boards)"
	areaSections   = "Spalten"
	areaLabels     = "Labels"
	areaMeetings   = "Meetings"
	areaTensions   = "Spannungen / Traktanden"
	areaTasks      = "Aufgaben"
)

// Run compares a Holaspirit backup directory with a Talkspirit one.
// migratedAt is the end of the migration; zero estimates it from the data.
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
	if migratedAt.IsZero() {
		t, evidence, err := guessMigration(ts)
		if err != nil {
			return nil, err
		}
		r.MigratedAt, r.MigratedAtGuessed, r.MigratedAtEvidence = t, true, evidence
	}
	if !hs.time.IsZero() && hs.time.After(r.MigratedAt) {
		r.note("Das Holaspirit-Backup (%s) ist jünger als der Migrationszeitpunkt: was in Holaspirit nach der Migration noch geändert wurde, erscheint als %s und ist erwartbar.",
			hs.time.Format("02.01.2006 15:04"), DiffHSLater)
	}

	c := &cmp{r: r, hs: hs, ts: ts, emailOfTS: map[string]string{}, emailOfHS: map[string]string{}}
	c.users()
	c.circles()
	c.roles()
	c.policies()
	c.goals()
	c.customFields()
	c.projects()
	c.meetings()
	c.tensions()
	c.tasks()
	c.notVerifiable()
	c.audit()
	r.sortFindings()
	return r, nil
}

// guessMigration estimates the end of the migration: most Talkspirit circles,
// roles and users are created by the import, so the busiest creation day
// marks it; the last creation that day is taken as its end.
func guessMigration(ts *tsSet) (time.Time, string, error) {
	perDay := map[string][]time.Time{}
	total := 0
	for _, list := range [][]obj{ts.circles, ts.roles, ts.users} {
		for _, o := range list {
			if t := tm(o, "created_at"); !t.IsZero() {
				perDay[dayKey(t)] = append(perDay[dayKey(t)], t)
				total++
			}
		}
	}
	best := ""
	for d, l := range perDay {
		if best == "" || len(l) > len(perDay[best]) || (len(l) == len(perDay[best]) && d < best) {
			best = d
		}
	}
	if total == 0 || len(perDay[best])*2 < total {
		return time.Time{}, "", fmt.Errorf("cannot estimate the migration time from the Talkspirit data (no single import day) — pass --migrated-at YYYY-MM-DD[THH:MM]")
	}
	var last time.Time
	for _, t := range perDay[best] {
		if t.After(last) {
			last = t
		}
	}
	return last.Add(time.Minute), fmt.Sprintf("%d von %d Kreisen/Rollen/Personen wurden am %s angelegt", len(perDay[best]), total, best), nil
}

type cmp struct {
	r                    *Report
	hs                   *hsSet
	ts                   *tsSet
	emailOfTS, emailOfHS map[string]string // user/member id → normalized e-mail
	hsCirclePath         map[string]string
	tsCirclePath         map[string]string
	circlePair           map[string]string // HS circle id → TS circle id
	rolePair             map[string]string // HS role id → TS role id
	meetingPair          map[string]string
	boardPair            map[string]string
}

func email(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// --- people -----------------------------------------------------------------

func (c *cmp) users() {
	a := c.r.area(areaUsers)
	a.HS, a.TS = len(c.hs.members), len(c.ts.users)
	var hs, ts []*rec
	for _, m := range c.hs.members {
		e := email(str(m, "email"))
		c.emailOfHS[str(m, "id")] = e
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
		var diffs []string
		if hn, tn := str(p.hs.o, "displayName"), str(p.ts.o, "display_name"); norm(hn) != norm(tn) {
			diffs = append(diffs, fmt.Sprintf("Name: %q → %q", hn, tn))
		}
		if hj, tj := str(p.hs.o, "jobTitle"), str(p.ts.o, "job_title"); hj != "" && norm(hj) != norm(tj) {
			diffs = append(diffs, fmt.Sprintf("Funktion: %q → %q", hj, tj))
		}
		if len(diffs) > 0 {
			c.r.add(Finding{Area: areaUsers, Kind: c.r.classify(tm(p.hs.o, "updatedAt"), tm(p.ts.o, "last_activity_at")),
				Key: personKey(p.hs.o), Detail: strings.Join(diffs, "; "), HSID: p.hs.id, TSID: p.ts.id})
		}
	}
	for _, h := range hsLeft {
		c.r.add(Finding{Area: areaUsers, Kind: Missing, Key: personKey(h.o), HSID: h.id,
			Detail: "kein aktives Talkspirit-Konto mit dieser E-Mail (gesperrte/anonymisierte Konten liefert die API nicht)"})
	}
	for _, t := range tsLeft {
		c.r.add(Finding{Area: areaUsers, Kind: c.r.extra(tm(t.o, "created_at")),
			Key: fmt.Sprintf("%s <%s>", str(t.o, "display_name"), str(t.o, "email")), TSID: t.id})
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

// contentDiff describes the differences of purpose/accountabilities/domains.
// missingParts are texts present in Holaspirit and absent in Talkspirit.
func contentDiff(hsPurpose, tsPurpose string, hsAccs, tsAccs, hsDoms, tsDoms []string) (diffs []string, missingParts []string) {
	if norm(hsPurpose) != "" && !sameText(hsPurpose, tsPurpose) {
		if norm(tsPurpose) == "" {
			missingParts = append(missingParts, "Purpose fehlt: "+short(hsPurpose, 120))
		} else {
			diffs = append(diffs, fmt.Sprintf("Purpose geändert: %q → %q", short(hsPurpose, 80), short(tsPurpose, 80)))
		}
	}
	onlyHS, onlyTS := textDiff(hsAccs, tsAccs)
	for _, s := range onlyHS {
		missingParts = append(missingParts, "Accountability fehlt: "+short(s, 120))
	}
	for _, s := range onlyTS {
		diffs = append(diffs, "Accountability nur in Talkspirit: "+short(s, 120))
	}
	onlyHS, onlyTS = textDiff(hsDoms, tsDoms)
	for _, s := range onlyHS {
		missingParts = append(missingParts, "Domäne fehlt: "+short(s, 120))
	}
	for _, s := range onlyTS {
		diffs = append(diffs, "Domäne nur in Talkspirit: "+short(s, 120))
	}
	return diffs, missingParts
}

// reportContent files the content differences of a matched circle or role.
// Missing parts of an object untouched since the migration are migration
// losses; if the object was edited afterwards they may be deliberate.
func (c *cmp) reportContent(area, key, hsID, tsID string, hsUpd, tsUpd time.Time, diffs, missing []string) {
	kind := c.r.classify(hsUpd, tsUpd)
	for _, m := range missing {
		k := MissingPart
		if kind != DiffUntouched {
			k = kind
			m += " (Objekt nach der Migration geändert)"
		}
		c.r.add(Finding{Area: area, Kind: k, Key: key, Detail: m, HSID: hsID, TSID: tsID})
	}
	if len(diffs) > 0 {
		c.r.add(Finding{Area: area, Kind: kind, Key: key, Detail: strings.Join(diffs, " | "), HSID: hsID, TSID: tsID})
	}
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
		key := c.hsCirclePath[p.hs.id]
		if p.pass == 1 {
			c.r.add(Finding{Area: areaCircles, Kind: Moved, Key: key, HSID: p.hs.id, TSID: p.ts.id,
				Detail: "in Talkspirit unter: " + c.tsCirclePath[p.ts.id]})
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
		c.reportContent(areaCircles, key, p.hs.id, p.ts.id, hsUpd, tm(p.ts.o, "updated_at"), diffs, missing)
	}
	for _, h := range hsLeft {
		c.r.add(Finding{Area: areaCircles, Kind: Missing, Key: c.hsCirclePath[h.id], HSID: h.id})
	}
	for _, t := range tsLeft {
		c.r.add(Finding{Area: areaCircles, Kind: c.r.extra(tm(t.o, "created_at")), Key: c.tsCirclePath[t.id], TSID: t.id})
	}
}

func (c *cmp) roles() {
	a := c.r.area(areaRoles)
	c.rolePair = map[string]string{}
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
		key := hsPath(p.hs.o)
		if p.pass == 1 {
			c.r.add(Finding{Area: areaRoles, Kind: Moved, Key: key, HSID: p.hs.id, TSID: p.ts.id,
				Detail: "in Talkspirit unter: " + tsPath(p.ts.o)})
		}
		purpose, accs, doms := c.hsRoleContent(p.hs.o)
		diffs, missing := contentDiff(purpose, str(p.ts.o, "purpose"), accs, strs(p.ts.o, "accountabilities"), doms, strs(p.ts.o, "domains"))
		c.reportContent(areaRoles, key, p.hs.id, p.ts.id, tm(p.hs.o, "updatedAt"), tm(p.ts.o, "updated_at"), diffs, missing)
		c.assignments(key, p.hs.o, p.ts.o)
	}
	for _, h := range hsLeft {
		_, accs, _ := c.hsRoleContent(h.o)
		c.r.add(Finding{Area: areaRoles, Kind: Missing, Key: hsPath(h.o), HSID: h.id,
			Detail: fmt.Sprintf("%d Accountabilities, %d Personen zugewiesen", len(accs), len(strs(h.o, "assignedMembers")))})
	}
	for _, t := range tsLeft {
		c.r.add(Finding{Area: areaRoles, Kind: c.r.extra(tm(t.o, "created_at")), Key: tsPath(t.o), TSID: t.id})
	}
}

// assignments compares who holds a matched role.
func (c *cmp) assignments(key string, hsRole, tsRole obj) {
	a := c.r.area(areaAssign)
	var hsEmails []string
	for _, aid := range strs(hsRole, "assignedMembers") {
		am, ok := c.hs.assigned[aid]
		if !ok {
			continue
		}
		if until := tm(am, "assignedUntil"); !until.IsZero() && !c.hs.time.IsZero() && until.Before(c.hs.time) {
			continue // assignment had already ended
		}
		if e := c.emailOfHS[str(am, "member")]; e != "" {
			hsEmails = append(hsEmails, e)
		}
	}
	tsEmails := map[string]obj{}
	for _, m := range objs(tsRole, "members") {
		if e := c.emailOfTS[str(m, "user_id")]; e != "" {
			tsEmails[e] = m
		}
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
		detail := "Person fehlt in der Rolle"
		if tm(tsRole, "updated_at").After(c.r.MigratedAt) {
			detail += " (Rolle nach der Migration geändert)"
		}
		c.r.add(Finding{Area: areaAssign, Kind: MissingPart, Key: key, Detail: detail + ": " + e,
			HSID: str(hsRole, "id"), TSID: str(tsRole, "id")})
	}
	for e, m := range tsEmails {
		if seen[e] {
			continue
		}
		c.r.add(Finding{Area: areaAssign, Kind: c.r.extra(tm(m, "assigned_at")), Key: key,
			Detail: "zusätzlich besetzt mit: " + e, TSID: str(tsRole, "id")})
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
				Key: str(p.hs.o, "title"), HSID: p.hs.id, TSID: p.ts.id,
				Detail: fmt.Sprintf("Text weicht ab: %q → %q", short(str(p.hs.o, "body"), 80), short(str(content[0], "content"), 80))})
		}
	}
	for _, h := range hsLeft {
		where := c.hsCirclePath[str(h.o, "circle")]
		c.r.add(Finding{Area: areaPolicies, Kind: Missing, Key: str(h.o, "title"), HSID: h.id,
			Detail: "kein Talkspirit-Dokument mit diesem Titel; Kreis: " + where})
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
	pairs, hsLeft, tsLeft := match(hs, ts, 1)
	a.Matched = len(pairs)
	for _, p := range pairs {
		var hsKR, tsKR []string
		for _, id := range strs(p.hs.o, "results") {
			if kr, ok := c.hs.krByID[id]; ok {
				hsKR = append(hsKR, str(kr, "title"))
			}
		}
		for _, kr := range c.ts.keyResults[p.ts.id] {
			tsKR = append(tsKR, str(kr, "title"))
		}
		onlyHS, _ := textDiff(hsKR, tsKR)
		for _, s := range onlyHS {
			k := MissingPart
			if tm(p.ts.o, "updated_at").After(c.r.MigratedAt) {
				k = DiffEdited
			}
			c.r.add(Finding{Area: areaGoals, Kind: k, Key: str(p.hs.o, "title"), Detail: "Key Result fehlt: " + short(s, 120),
				HSID: p.hs.id, TSID: p.ts.id})
		}
	}
	for _, h := range hsLeft {
		c.r.add(Finding{Area: areaGoals, Kind: Missing, Key: str(h.o, "title"), HSID: h.id,
			Detail: fmt.Sprintf("Status %s, %d Key Results", str(h.o, "status"), len(strs(h.o, "results")))})
	}
	for _, t := range tsLeft {
		c.r.add(Finding{Area: areaGoals, Kind: c.r.extra(tm(t.o, "created_at")), Key: str(t.o, "title"), TSID: t.id})
	}

	// timeframes ↔ time periods
	b := c.r.area(areaTimeframes)
	b.HS, b.TS = len(c.hs.timeframes), len(c.ts.timePeriods)
	hs, ts = nil, nil
	for _, o := range c.hs.timeframes {
		hs = append(hs, &rec{o: o, id: str(o, "id"), keys: []string{norm(str(o, "name"))}})
	}
	for _, o := range c.ts.timePeriods {
		ts = append(ts, &rec{o: o, id: str(o, "id"), keys: []string{norm(str(o, "title"))}})
	}
	pairs, hsLeft, tsLeft = match(hs, ts, 1)
	b.Matched = len(pairs)
	for _, h := range hsLeft {
		c.r.add(Finding{Area: areaTimeframes, Kind: Missing, Key: str(h.o, "name"), HSID: h.id})
	}
	for _, t := range tsLeft {
		c.r.add(Finding{Area: areaTimeframes, Kind: c.r.extra(tm(t.o, "created_at")), Key: str(t.o, "title"), TSID: t.id})
	}
}

func (c *cmp) customFields() {
	a := c.r.area(areaCustom)
	tsDefs := map[string][]obj{"role": c.ts.cfRole, "circle": c.ts.cfCircle, "member": c.ts.cfUser, "user": c.ts.cfUser}
	a.HS = len(c.hs.customFields)
	a.TS = len(c.ts.cfRole) + len(c.ts.cfCircle) + len(c.ts.cfUser)
	for _, f := range c.hs.customFields {
		applyTo := str(f, "applyTo")
		found := false
		for _, d := range tsDefs[applyTo] {
			if norm(str(d, "name")) == norm(str(f, "name")) {
				found = true
				break
			}
		}
		if found {
			a.Matched++
			continue
		}
		c.r.add(Finding{Area: areaCustom, Kind: Missing, Key: fmt.Sprintf("%s (%s)", str(f, "name"), applyTo), HSID: str(f, "id"),
			Detail: "Felddefinition fehlt"})
	}

	// Values on roles: Holaspirit keys them by apiName, Talkspirit by name.
	nameOf := map[string]string{}
	for _, f := range c.hs.customFields {
		nameOf[str(f, "apiName")] = str(f, "name")
		nameOf[str(f, "id")] = str(f, "name")
	}
	tsRoles := map[string]obj{}
	for _, r := range c.ts.roles {
		tsRoles[str(r, "id")] = r
	}
	for _, hr := range c.hs.rolesCF {
		tr, ok := tsRoles[c.rolePair[str(hr, "id")]]
		if !ok {
			continue
		}
		vals, _ := hr["customFields"].(map[string]any)
		for k, v := range vals {
			hv := fmt.Sprint(v)
			if s, ok := v.(string); ok {
				hv = s
			}
			if norm(hv) == "" || hv == "<nil>" {
				continue
			}
			name := nameOf[k]
			if name == "" {
				name = k
			}
			tv := ""
			for _, f := range objs(tr, "custom_fields") {
				if norm(str(f, "name")) == norm(name) || str(f, "key") == k {
					tv = str(f, "value")
					if tv == "" {
						tv = strings.Join(strs(f, "values"), ", ")
					}
				}
			}
			if sameText(hv, tv) {
				continue
			}
			key := c.hsCirclePath[str(hr, "parentCircle")] + " › " + str(hr, "name")
			kind := MissingPart
			if norm(tv) != "" || tm(tr, "updated_at").After(c.r.MigratedAt) {
				kind = c.r.classify(tm(hr, "updatedAt"), tm(tr, "updated_at"))
			}
			c.r.add(Finding{Area: areaCustom, Kind: kind, Key: key, HSID: str(hr, "id"), TSID: str(tr, "id"),
				Detail: fmt.Sprintf("Feld %q: %q → %q", name, short(hv, 80), short(tv, 80))})
		}
	}
}

// --- operations ---------------------------------------------------------------

func (c *cmp) projects() {
	a := c.r.area(areaProjects)
	a.HS, a.TS = len(c.hs.boards), len(c.ts.projects)
	c.boardPair = map[string]string{}
	var hs, ts []*rec
	for _, o := range c.hs.boards {
		hs = append(hs, &rec{o: o, id: str(o, "id"), keys: []string{norm(str(o, "title"))}})
	}
	for _, o := range c.ts.projects {
		ts = append(ts, &rec{o: o, id: str(o, "id"), keys: []string{norm(str(o, "name"))}})
	}
	pairs, hsLeft, tsLeft := match(hs, ts, 1)
	a.Matched = len(pairs)
	for _, p := range pairs {
		c.boardPair[p.hs.id] = p.ts.id
		if hsArch, tsArch := str(p.hs.o, "archivedAt") != "", boolean(p.ts.o, "archived"); hsArch != tsArch {
			c.r.add(Finding{Area: areaProjects, Kind: c.r.classify(time.Time{}, tm(p.ts.o, "updated_at")), Key: str(p.hs.o, "title"),
				Detail: fmt.Sprintf("archiviert: Holaspirit %v, Talkspirit %v", hsArch, tsArch), HSID: p.hs.id, TSID: p.ts.id})
		}
	}
	for _, h := range hsLeft {
		c.r.add(Finding{Area: areaProjects, Kind: Missing, Key: str(h.o, "title"), HSID: h.id,
			Detail: fmt.Sprintf("%s Aufgaben, privat: %v", str(h.o, "tasksCount"), boolean(h.o, "private"))})
	}
	for _, t := range tsLeft {
		c.r.add(Finding{Area: areaProjects, Kind: c.r.extra(tm(t.o, "created_at")), Key: str(t.o, "name"), TSID: t.id})
	}

	// columns ↔ sections and labels ↔ labels, within matched projects
	for _, sub := range []struct {
		area   string
		hsList []obj
		ts     map[string][]obj
		file   string
	}{{areaSections, c.hs.columns, c.ts.sections, "sections"}, {areaLabels, c.hs.labels, c.ts.projLabels, "labels"}} {
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
			found := false
			for _, t := range sub.ts[pid] {
				if norm(str(t, "name")) == norm(str(o, "name")) {
					found = true
					break
				}
			}
			if found {
				s.Matched++
				continue
			}
			board := ""
			if b, ok := byID(c.hs.boards)[str(o, "board")]; ok {
				board = str(b, "title")
			}
			c.r.add(Finding{Area: sub.area, Kind: MissingPart, Key: board + " › " + str(o, "name"), HSID: str(o, "id"), TSID: pid})
		}
		for pid := range c.boardPairReverse() {
			s.TS += len(sub.ts[pid])
		}
	}
}

func (c *cmp) boardPairReverse() map[string]string {
	out := map[string]string{}
	for h, t := range c.boardPair {
		out[t] = h
	}
	return out
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
			c.r.add(Finding{Area: areaMeetings, Kind: Uncertain, Key: meetingKey(p.hs.o), HSID: p.hs.id, TSID: p.ts.id,
				Detail: fmt.Sprintf("nur über Zeitpunkt oder Titel zugeordnet: %q, %s", str(p.ts.o, "title"), str(p.ts.o, "start_time"))})
		}
	}
	for _, h := range hsLeft {
		when := "vergangen"
		if t := tm(h.o, "scheduledAt"); t.After(c.r.MigratedAt) {
			when = "nach der Migration geplant (Serie evtl. neu angelegt)"
		}
		if tpl := c.hs.meetingTemplates[str(h.o, "meetingTemplate")]; tpl != "" {
			when = tpl + ", " + when
			if str(h.o, "scheduledAt") == "" {
				when += " (Governance-Änderung ausserhalb eines Meetings, Holaspirit-eigenes Konstrukt)"
			}
		}
		c.r.add(Finding{Area: areaMeetings, Kind: Missing, Key: meetingKey(h.o), HSID: h.id,
			Detail: fmt.Sprintf("%s, Status %s, %s Traktanden, Kreis %s", when, str(h.o, "status"), str(h.o, "tensionsCount"), str(h.o, "circleName"))})
	}
	for _, t := range tsLeft {
		c.r.add(Finding{Area: areaMeetings, Kind: c.r.extra(tm(t.o, "created_at")),
			Key: fmt.Sprintf("%s (%s)", str(t.o, "title"), str(t.o, "start_time")), TSID: t.id})
	}
}

// hsMeetingTime is when a Holaspirit meeting took place; meetings without a
// date ("Evolution outside meeting") fall back to their creation.
func hsMeetingTime(o obj) time.Time {
	if t := tm(o, "scheduledAt"); !t.IsZero() {
		return t
	}
	return tm(o, "createdAt")
}

func meetingKey(o obj) string {
	title := str(o, "title")
	if title == "" {
		title = "[ohne Titel] " + str(o, "circleName")
	}
	return fmt.Sprintf("%s (%s UTC)", strings.TrimSpace(title), hsMeetingTime(o).Format("2006-01-02 15:04"))
}

func key2(a, b string) string {
	if a == "" || b == "" {
		return ""
	}
	return a + "|" + b
}

// tensions matches Holaspirit tensions (agenda items and proposals of
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
	for _, p := range pairs {
		if p.pass == 5 {
			c.r.add(Finding{Area: areaTensions, Kind: Uncertain, Key: c.tensionKey(p.hs.o), HSID: p.hs.id, TSID: p.ts.id,
				Detail: "nur über den Titel zugeordnet"})
		}
	}
	hsMeetings := byID(c.hs.meetings)
	for _, h := range hsLeft {
		detail := fmt.Sprintf("%s, Status %s", str(h.o, "tensionBehavior"), str(h.o, "status"))
		if m, ok := hsMeetings[str(h.o, "meeting")]; ok {
			detail += ", Meeting " + meetingKey(m)
			if c.meetingPair[str(m, "id")] == "" {
				detail += " (Meeting selbst fehlt)"
			}
		}
		c.r.add(Finding{Area: areaTensions, Kind: Missing, Key: c.tensionKey(h.o), HSID: h.id, Detail: detail})
	}
	for _, t := range tsLeft {
		c.r.add(Finding{Area: areaTensions, Kind: c.r.extra(tm(t.o, "created_at")), Key: str(t.o, "title"), TSID: t.id})
	}
}

func (c *cmp) tensionKey(o obj) string {
	return fmt.Sprintf("%s (%s, %s)", str(o, "name"), tm(o, "createdAt").Format("2006-01-02"), c.hsCirclePath[str(o, "circle")])
}

func (c *cmp) tasks() {
	a := c.r.area(areaTasks)
	a.HS, a.TS = len(c.hs.tasks), len(c.ts.tasks)
	if !c.ts.have["subtasks"] {
		a.Note = "Unteraufgaben fehlen im Talkspirit-Backup"
	}
	var hs, ts []*rec
	for _, o := range c.hs.tasks {
		title, t := norm(str(o, "title")), tm(o, "createdAt")
		pk := ""
		if p := c.boardPair[str(o, "board")]; p != "" {
			pk = key2(p, title)
		}
		hs = append(hs, &rec{o: o, id: str(o, "id"), keys: []string{key2(title, minuteKey(t)), key2(title, dayKey(t)), pk, title}})
	}
	for _, o := range c.ts.tasks {
		title, t := norm(str(o, "title")), tm(o, "created_at")
		ts = append(ts, &rec{o: o, id: str(o, "id"), keys: []string{key2(title, minuteKey(t)), key2(title, dayKey(t)), key2(str(o, "project_id"), title), title}})
	}
	pairs, hsLeft, tsLeft := match(hs, ts, 4)
	a.Matched = len(pairs)
	boards := byID(c.hs.boards)
	for _, p := range pairs {
		if p.pass == 3 {
			c.r.add(Finding{Area: areaTasks, Kind: Uncertain, Key: taskKey(p.hs.o, boards), HSID: p.hs.id, TSID: p.ts.id,
				Detail: "nur über den Titel zugeordnet"})
		}
		var diffs []string
		if hsArch, tsArch := str(p.hs.o, "archivedAt") != "", boolean(p.ts.o, "archived"); hsArch != tsArch {
			diffs = append(diffs, fmt.Sprintf("archiviert: %v → %v", hsArch, tsArch))
		}
		hsDone := str(p.hs.o, "status") == "done"
		st := strings.ToLower(str(p.ts.o, "status"))
		tsDone := str(p.ts.o, "completed_at") != "" || st == "done" || st == "completed" || st == "closed"
		if hsDone != tsDone {
			diffs = append(diffs, fmt.Sprintf("erledigt: %v → %v (Status %s)", hsDone, tsDone, str(p.ts.o, "status")))
		}
		if len(diffs) > 0 {
			c.r.add(Finding{Area: areaTasks, Kind: c.r.classify(tm(p.hs.o, "updatedAt"), tm(p.ts.o, "updated_at")),
				Key: taskKey(p.hs.o, boards), Detail: strings.Join(diffs, "; "), HSID: p.hs.id, TSID: p.ts.id})
		}
	}
	for _, h := range hsLeft {
		detail := fmt.Sprintf("Status %s, archiviert %v", str(h.o, "status"), str(h.o, "archivedAt") != "")
		if boolean(h.o, "private") {
			detail += ", privat (für den API-Benutzer evtl. unsichtbar)"
		}
		if str(h.o, "parent") != "" {
			detail += ", Unteraufgabe"
		}
		c.r.add(Finding{Area: areaTasks, Kind: Missing, Key: taskKey(h.o, boards), HSID: h.id, Detail: detail})
	}
	for _, t := range tsLeft {
		c.r.add(Finding{Area: areaTasks, Kind: c.r.extra(tm(t.o, "created_at")), Key: str(t.o, "title"), TSID: t.id})
	}
}

func taskKey(o obj, boards map[string]obj) string {
	b := str(o, "boardTitle")
	if b == "" {
		b = str(boards[str(o, "board")], "title")
	}
	return fmt.Sprintf("%s › %s (%s)", b, str(o, "title"), tm(o, "createdAt").Format("2006-01-02"))
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
