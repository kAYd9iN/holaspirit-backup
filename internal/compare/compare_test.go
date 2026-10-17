package compare

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeJSON(t *testing.T, dir, name string, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".json"), b, 0600); err != nil {
		t.Fatal(err)
	}
}

type m = map[string]any

// fixture: a Holaspirit org with one sub-circle and two roles, and its
// Talkspirit migration, which lost an accountability, an assignment, a role
// and a meeting item — plus one deliberate edit and one new role.
func fixture(t *testing.T) (hs, ts string) {
	hs, ts = t.TempDir(), t.TempDir()
	old := "2026-01-01T10:00:00+01:00"
	writeJSON(t, hs, "backup-manifest", m{"timestamp": "2026-10-14T15:00:00Z"})
	writeJSON(t, hs, "members", []m{
		{"id": "hm1", "email": "Anna@x.ch", "displayName": "Anna A", "updatedAt": old},
		{"id": "hm2", "email": "ben@x.ch", "displayName": "Ben B", "updatedAt": old},
		{"id": "hm3", "email": "cara@x.ch", "displayName": "Cara C", "updatedAt": old},
	})
	writeJSON(t, hs, "circles", []m{
		{"id": "hc0", "name": "Org", "parentCircle": nil, "role": "hcr0", "updatedAt": old},
		{"id": "hc1", "name": "Sales", "parentCircle": "hc0", "role": "hcr1", "updatedAt": old},
	})
	writeJSON(t, hs, "roles", []m{
		{"id": "hcr0", "name": "Org", "circle": "hc0", "parentCircle": nil, "purpose": "<p>Gutes tun</p>", "accountabilities": []string{}, "domains": []string{}, "updatedAt": old},
		{"id": "hcr1", "name": "Sales", "circle": "hc1", "parentCircle": "hc0", "purpose": "Verkaufen", "accountabilities": []string{}, "domains": []string{}, "updatedAt": old},
		{"id": "hr1", "name": "Key Account", "circle": nil, "parentCircle": "hc1", "purpose": "<p>Kunden&nbsp;pflegen</p>",
			"accountabilities": []string{"a1", "a2", "a4"}, "domains": []string{"d1"}, "assignedMembers": []string{"as1", "as2", "as3"}, "updatedAt": old},
		{"id": "hr2", "name": "Lost Role", "circle": nil, "parentCircle": "hc1", "purpose": "x", "accountabilities": []string{}, "domains": []string{}, "updatedAt": old},
		{"id": "hr3", "name": "Edited Role", "circle": nil, "parentCircle": "hc1", "purpose": "alt", "accountabilities": []string{"a3"}, "domains": []string{}, "updatedAt": old},
	})
	writeJSON(t, hs, "roles-linked", m{
		"accountabilities": []m{{"id": "a1", "description": "<p>Angebote erstellen</p>"}, {"id": "a2", "description": "Verträge verhandeln"},
			{"id": "a3", "description": "Alte Aufgabe"},
			{"id": "a4", "description": `<p>Abstimmen mit <a data-mention="hc0|circle">Org</a> und Partnern</p>`}},
		"domains":         []m{{"id": "d1", "description": "CRM"}},
		"assignedmembers": []m{{"id": "as1", "member": "hm1"}, {"id": "as2", "member": "hm2"}, {"id": "as3", "member": "hm3"}},
	})
	writeJSON(t, hs, "meetings", []m{{"id": "hmt1", "title": "Tactical Sales", "scheduledAt": "2026-03-02T09:00:00+01:00", "status": "closed", "tensionsCount": 2}})
	writeJSON(t, hs, "tensions", []m{
		{"id": "ht1", "name": "Pipeline", "meeting": "hmt1", "createdAt": "2026-03-01T08:00:00+01:00", "tensionBehavior": "note", "status": "accepted"},
		{"id": "ht2", "name": "Lost item", "meeting": "hmt1", "createdAt": "2026-03-01T08:05:00+01:00", "tensionBehavior": "note", "status": "accepted"},
		{"id": "ht3", "name": "Item of a deleted meeting", "meeting": "gone", "createdAt": "2025-03-01T08:05:00+01:00", "tensionBehavior": "note", "status": "accepted"},
		{"id": "ht4", "name": "Offenes Backlog Thema", "createdAt": "2026-05-01T08:05:00+01:00", "tensionBehavior": "note", "status": "ready"},
	})
	writeJSON(t, hs, "tasks", []m{
		{"id": "hk1", "title": "Ohne Projekt", "createdAt": "2025-01-01T10:00:00+01:00", "status": "current"},
		{"id": "hk2", "title": "Erledigt per Haken", "createdAt": "2025-01-02T10:00:00+01:00", "status": "current", "checked": true, "board": "hb1"},
	})
	writeJSON(t, hs, "boards", []m{{"id": "hb1", "title": "Board"}})
	writeJSON(t, hs, "policies", []m{{"id": "p1", "title": "Spesenregel", "body": "<p>Max 50 CHF</p>", "circle": "hc0"}})
	writeJSON(t, hs, "checklists", []m{{"id": "c1"}})

	mig := "2026-10-10T08:00:00Z"
	later := "2026-10-12T08:00:00Z"
	writeJSON(t, ts, "backup-manifest", m{"timestamp": "2026-10-16T12:00:00Z"})
	writeJSON(t, ts, "whoami", []m{{"tenant_id": "t"}})
	writeJSON(t, ts, "users", []m{{"id": "u1", "email": "anna@x.ch", "display_name": "Anna A", "created_at": mig},
		{"id": "u3", "email": "cara@x.ch", "display_name": "Cara C", "created_at": old}})
	writeJSON(t, ts, "memberships", []m{{"user_id": "u1", "role_id": "r1", "updated_at": "2026-10-10T07:59:12Z"}})
	writeJSON(t, ts, "projects", []m{{"id": "p1", "name": "Board", "archived": false}})
	writeJSON(t, ts, "tasks", []m{{"id": "k2", "title": "Erledigt per Haken", "created_at": "2025-01-02T09:00:00Z", "status": "DONE", "project_id": "p1", "archived": false}})
	writeJSON(t, ts, "subtasks", []m{{"parent_id": "k2", "data": []m{}}})
	writeJSON(t, ts, "circles", []m{
		{"id": "c0", "name": "Org", "parent_circle_id": nil, "purpose": "Gutes tun", "accountabilities": []string{}, "domains": []string{}, "created_at": mig, "updated_at": mig},
		{"id": "c1", "name": "Sales", "parent_circle_id": "c0", "purpose": "Verkaufen", "accountabilities": []string{}, "domains": []string{}, "created_at": mig, "updated_at": mig},
	})
	writeJSON(t, ts, "roles", []m{
		{"id": "r1", "name": "Key Account", "parent_circle_id": "c1", "purpose": "Kunden pflegen", "accountabilities": []string{"Angebote erstellen",
			// BlockNote, mentioning the circle under a new name, without spaces
			`[{"type": "paragraph", "content": [{"type": "text", "text": "Abstimmen mit"}, {"type": "mention", "props": {"userName": "Organisation neu"}}, {"type": "text", "text": "und Partnern"}], "children": []}]`},
			"domains": []string{"CRM"}, "members": []m{{"user_id": "u1"}}, "created_at": mig, "updated_at": mig},
		{"id": "r3", "name": "Edited Role", "parent_circle_id": "c1", "purpose": "neu", "accountabilities": []string{}, "domains": []string{},
			"created_at": mig, "updated_at": later},
		{"id": "r4", "name": "New Role", "parent_circle_id": "c1", "purpose": "", "created_at": later, "updated_at": later},
	})
	writeJSON(t, ts, "meetings", []m{{"id": "mt1", "title": "Tactical Sales", "start_time": "2026-03-02T08:00:00Z", "created_at": mig}})
	writeJSON(t, ts, "meeting-agenda-items", []m{{"parent_id": "mt1", "data": []m{{"id": "ag1", "title": "Pipeline", "created_at": mig}}}})
	writeJSON(t, ts, "documents", []m{})
	return hs, ts
}

func find(r *Report, area string, kind Kind, keyPart, detailPart string) bool {
	for _, f := range r.Findings {
		text := f.What + "|" + f.Original + "|" + f.Current + "|" + f.Detail
		if f.Area == area && f.Kind == kind && strings.Contains(f.Key+"|"+f.Circle, keyPart) && strings.Contains(text, detailPart) {
			return true
		}
	}
	return false
}

func TestRun_Fixture(t *testing.T) {
	hs, ts := fixture(t)
	r, err := Run(hs, ts, time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	checks := []struct {
		area   string
		kind   Kind
		key    string
		detail string
	}{
		{areaUsers, Unverified, "ben@x.ch", "kein aktives Konto"},
		{areaUsers, MissingPart, "cara@x.ch", "Die einzige Rolle fehlt"},
		{areaAssign, MissingPart, "Key Account", "keine einzige Rolle"},
		{areaAssign, Unverified, "Key Account", "ben@x.ch"},
		{areaTensions, Orphan, "Item of a deleted meeting", ""},
		{areaOpen, Missing, "Offenes Backlog Thema", ""},
		{areaTasks, Unverified, "Ohne Projekt", "ohne Projekt"},
		{areaRoles, Missing, "Lost Role", "Rolle fehlt"},
		{areaRoles, MissingPart, "Key Account", "Verträge verhandeln"},
		{areaRoles, DiffEdited, "Edited Role", "Alte Aufgabe"},
		{areaRoles, DiffEdited, "Edited Role", "Purpose geändert"},
		{areaRoles, NewInTS, "New Role", ""},
		{areaTensions, Missing, "Lost item", ""},
		{areaPolicies, Missing, "Spesenregel", ""},
	}
	for _, c := range checks {
		if !find(r, c.area, c.kind, c.key, c.detail) {
			t.Errorf("missing finding %s/%s %q %q", c.area, c.kind, c.key, c.detail)
		}
	}
	// Formatting differences (HTML vs plain vs BlockNote, &nbsp;, renamed
	// mentions, lost spaces) are not findings; neither is a task checked off
	// in Holaspirit and DONE in Talkspirit.
	for _, f := range r.Findings {
		if f.Area == areaCircles || (f.Area == areaRoles && f.Key == "Key Account" && f.Kind != MissingPart) ||
			strings.Contains(f.Original, "Abstimmen") || f.Key == "Erledigt per Haken (02.01.2025)" {
			t.Errorf("unexpected finding: %+v", f)
		}
	}
	if a := r.area(areaTensions); a.Matched != 1 {
		t.Errorf("tensions matched = %d, want 1 (via the meeting)", a.Matched)
	}
	if len(r.NotVerifiable) == 0 {
		t.Error("checklists should be listed as not verifiable")
	}

	// The Holaspirit original is carried for copying back.
	if !find(r, areaRoles, MissingPart, "Key Account", "Verträge verhandeln") {
		t.Error("missing accountability must carry the original text")
	}
	for _, f := range r.Findings {
		if f.Area == areaRoles && f.Key == "Lost Role" && f.Circle != "Org › Sales" {
			t.Errorf("circle of the lost role = %q", f.Circle)
		}
	}

	var buf bytes.Buffer
	if err := r.WriteXLSX(&buf); err != nil {
		t.Fatal(err)
	}
	parts := unzip(t, buf.Bytes())
	for _, want := range []string{"Arbeitsliste", "Mängelliste Talkspirit", "Übersicht", "Nicht prüfbar"} {
		if !strings.Contains(parts["xl/workbook.xml"], want) {
			t.Errorf("workbook lacks sheet %q", want)
		}
	}
	work := parts["xl/worksheets/sheet2.xml"]
	if !strings.Contains(work, "Lost Role") || !strings.Contains(work, "Verträge verhandeln") || !strings.Contains(work, "Kein Fehler") {
		t.Error("work list lacks the expected rows or the status drop-down")
	}
	if strings.Contains(work, "New Role") {
		t.Error("legitimate new roles must not be on the work list")
	}
}

func TestRun_GuessesMigrationTime(t *testing.T) {
	hs, ts := fixture(t)
	r, err := Run(hs, ts, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if !r.MigratedAtGuessed || r.MigratedAt.Format("2006-01-02T15:04") != "2026-10-10T08:00" {
		t.Errorf("migrated at %v (guessed %v)", r.MigratedAt, r.MigratedAtGuessed)
	}
}

func TestNormAndSameText(t *testing.T) {
	if norm("<p>Kunden&nbsp;pflegen</p>") != "kunden pflegen" {
		t.Errorf("norm = %q", norm("<p>Kunden&nbsp;pflegen</p>"))
	}
	if !sameText("<ul><li>**Wartung** &amp; Support</li></ul>", "- Wartung & Support") {
		t.Error("markup differences must not count")
	}
	if sameText("Angebote erstellen", "Verträge verhandeln") {
		t.Error("different texts must differ")
	}
}

func TestMatch_MultisetPairing(t *testing.T) {
	hs := []*rec{{id: "h1", keys: []string{"a"}}, {id: "h2", keys: []string{"a"}}, {id: "h3", keys: []string{"a"}}}
	ts := []*rec{{id: "t1", keys: []string{"a"}}, {id: "t2", keys: []string{"a"}}}
	pairs, hsLeft, tsLeft := match(hs, ts, 1)
	if len(pairs) != 2 || len(hsLeft) != 1 || len(tsLeft) != 0 {
		t.Errorf("pairs=%d hsLeft=%d tsLeft=%d", len(pairs), len(hsLeft), len(tsLeft))
	}
}

// unzip returns the parts of an .xlsx and checks each is well-formed XML.
func unzip(t *testing.T, b []byte) map[string]string {
	t.Helper()
	z, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, f := range z.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(rc)
		rc.Close()
		dec := xml.NewDecoder(bytes.NewReader(data))
		for {
			if _, err := dec.Token(); err == io.EOF {
				break
			} else if err != nil {
				t.Fatalf("%s is not well-formed XML: %v", f.Name, err)
			}
		}
		out[f.Name] = string(data)
	}
	return out
}

func TestBlockText(t *testing.T) {
	in := `[{"type":"bulletListItem","content":[{"type":"text","text":"Eins"},{"type":"link","href":"x","content":[{"type":"text","text":"Link"}]}],"children":[{"type":"paragraph","content":[{"type":"mention","props":{"userName":"Kreis A"}}],"children":[]}]}]`
	got, ok := blockText(in, true)
	if !ok || got != "EinsLink\nKreis A" {
		t.Errorf("blockText = %q, %v", got, ok)
	}
	if got, _ := blockText(in, false); strings.Contains(got, "Kreis A") {
		t.Errorf("mentions not dropped: %q", got)
	}
	if _, ok := blockText("plain text", true); ok {
		t.Error("plain text is not BlockNote")
	}
}

func TestSameText_RealDifferencesStay(t *testing.T) {
	if sameText("Verträge verhandeln und abschliessen", "Rechnungen prüfen") {
		t.Error("different accountabilities must differ")
	}
	if !sameText("Information der Stellen wie , Account Manager", "Information der Stellen wie Applikationsverantwortliche, Account Manager") {
		t.Error("a mention filled in by Talkspirit is no loss")
	}
}
