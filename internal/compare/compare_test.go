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
	})
	writeJSON(t, hs, "circles", []m{
		{"id": "hc0", "name": "Org", "parentCircle": nil, "role": "hcr0", "updatedAt": old},
		{"id": "hc1", "name": "Sales", "parentCircle": "hc0", "role": "hcr1", "updatedAt": old},
	})
	writeJSON(t, hs, "roles", []m{
		{"id": "hcr0", "name": "Org", "circle": "hc0", "parentCircle": nil, "purpose": "<p>Gutes tun</p>", "accountabilities": []string{}, "domains": []string{}, "updatedAt": old},
		{"id": "hcr1", "name": "Sales", "circle": "hc1", "parentCircle": "hc0", "purpose": "Verkaufen", "accountabilities": []string{}, "domains": []string{}, "updatedAt": old},
		{"id": "hr1", "name": "Key Account", "circle": nil, "parentCircle": "hc1", "purpose": "<p>Kunden&nbsp;pflegen</p>",
			"accountabilities": []string{"a1", "a2"}, "domains": []string{"d1"}, "assignedMembers": []string{"as1", "as2"}, "updatedAt": old},
		{"id": "hr2", "name": "Lost Role", "circle": nil, "parentCircle": "hc1", "purpose": "x", "accountabilities": []string{}, "domains": []string{}, "updatedAt": old},
		{"id": "hr3", "name": "Edited Role", "circle": nil, "parentCircle": "hc1", "purpose": "alt", "accountabilities": []string{"a3"}, "domains": []string{}, "updatedAt": old},
	})
	writeJSON(t, hs, "roles-linked", m{
		"accountabilities": []m{{"id": "a1", "description": "<p>Angebote erstellen</p>"}, {"id": "a2", "description": "Verträge verhandeln"},
			{"id": "a3", "description": "Alte Aufgabe"}},
		"domains":         []m{{"id": "d1", "description": "CRM"}},
		"assignedmembers": []m{{"id": "as1", "member": "hm1"}, {"id": "as2", "member": "hm2"}},
	})
	writeJSON(t, hs, "meetings", []m{{"id": "hmt1", "title": "Tactical Sales", "scheduledAt": "2026-03-02T09:00:00+01:00", "status": "closed", "tensionsCount": 2}})
	writeJSON(t, hs, "tensions", []m{
		{"id": "ht1", "name": "Pipeline", "meeting": "hmt1", "createdAt": "2026-03-01T08:00:00+01:00", "tensionBehavior": "note", "status": "accepted"},
		{"id": "ht2", "name": "Lost item", "meeting": "hmt1", "createdAt": "2026-03-01T08:05:00+01:00", "tensionBehavior": "note", "status": "accepted"},
	})
	writeJSON(t, hs, "policies", []m{{"id": "p1", "title": "Spesenregel", "body": "<p>Max 50 CHF</p>", "circle": "hc0"}})
	writeJSON(t, hs, "checklists", []m{{"id": "c1"}})

	mig := "2026-10-10T08:00:00Z"
	later := "2026-10-12T08:00:00Z"
	writeJSON(t, ts, "backup-manifest", m{"timestamp": "2026-10-16T12:00:00Z"})
	writeJSON(t, ts, "whoami", []m{{"tenant_id": "t"}})
	writeJSON(t, ts, "users", []m{{"id": "u1", "email": "anna@x.ch", "display_name": "Anna A", "created_at": mig}})
	writeJSON(t, ts, "circles", []m{
		{"id": "c0", "name": "Org", "parent_circle_id": nil, "purpose": "Gutes tun", "accountabilities": []string{}, "domains": []string{}, "created_at": mig, "updated_at": mig},
		{"id": "c1", "name": "Sales", "parent_circle_id": "c0", "purpose": "Verkaufen", "accountabilities": []string{}, "domains": []string{}, "created_at": mig, "updated_at": mig},
	})
	writeJSON(t, ts, "roles", []m{
		{"id": "r1", "name": "Key Account", "parent_circle_id": "c1", "purpose": "Kunden pflegen", "accountabilities": []string{"Angebote erstellen"},
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
		{areaUsers, Missing, "ben@x.ch", ""},
		{areaRoles, Missing, "Lost Role", "Rolle fehlt"},
		{areaRoles, MissingPart, "Key Account", "Verträge verhandeln"},
		{areaAssign, MissingPart, "Key Account", "ben@x.ch"},
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
	// Formatting differences (HTML vs plain, &nbsp;) are not findings.
	for _, f := range r.Findings {
		if f.Area == areaCircles || (f.Area == areaRoles && strings.Contains(f.Key, "Key Account") && strings.Contains(f.Detail, "Purpose")) {
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
	if !r.MigratedAtGuessed || r.MigratedAt.Format("2006-01-02") != "2026-10-10" {
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
