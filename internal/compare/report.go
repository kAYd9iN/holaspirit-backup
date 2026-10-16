package compare

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kAYd9iN/holaspirit-backup/internal/xlsx"
)

// kindLabel is how a kind reads in the workbook.
var kindLabel = map[Kind]string{
	Missing:       "Fehlt",
	MissingPart:   "Teil fehlt",
	DiffUntouched: "Abweichend, seit Stichtag unverändert",
	Moved:         "An anderer Stelle",
	OnlyInTS:      "Nur in Talkspirit, vor Stichtag angelegt",
	Uncertain:     "Unsicher zugeordnet",
	DiffHSLater:   "In Holaspirit nach Stichtag geändert",
	DiffEdited:    "In Talkspirit nach Stichtag bearbeitet",
	NewInTS:       "Neu nach Stichtag",
}

var kindExplain = map[Kind]string{
	Missing:       "Objekt aus Holaspirit ist in Talkspirit nicht auffindbar.",
	MissingPart:   "Objekt ist da, aber ein Teil fehlt (Accountability, Domäne, Purpose, Besetzung, Key Result …) – und das Objekt wurde nach dem Stichtag nicht bearbeitet.",
	DiffUntouched: "Inhalt weicht ab, obwohl niemand das Talkspirit-Objekt nach dem Stichtag geändert hat.",
	Moved:         "Nur unter einem anderen Kreis gefunden; seit dem Stichtag nicht bearbeitet.",
	OnlyInTS:      "In Talkspirit vor dem Stichtag vorhanden, im Holaspirit-Backup nicht – unerwartet.",
	Uncertain:     "Nur über Titel oder Uhrzeit zugeordnet. Die Anzahl stimmt, die einzelne Paarung kann falsch sein.",
	DiffHSLater:   "Das Holaspirit-Objekt wurde nach dem Stichtag noch geändert – Abweichung erwartbar.",
	DiffEdited:    "Das Talkspirit-Objekt wurde nach dem Stichtag bearbeitet – vermutlich gewollt.",
	NewInTS:       "Nach dem Stichtag in Talkspirit angelegt – legitime neue Arbeit.",
}

// statusList is the drop-down of the work list.
var statusList = []string{"Offen", "In Arbeit", "Erledigt", "Kein Fehler", "An Talkspirit gemeldet"}

// WriteFiles writes vergleich.xlsx into dir (0600: it holds names and e-mail
// addresses) and returns its path.
func (r *Report) WriteFiles(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0750); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "vergleich.xlsx")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600) // #nosec G304 -- operator-chosen output dir + fixed name
	if err != nil {
		return "", err
	}
	if err := r.WriteXLSX(f); err != nil {
		return "", errors.Join(err, f.Close())
	}
	return path, f.Close()
}

func prioStyle(p string) xlsx.Style {
	switch p {
	case PrioHigh:
		return xlsx.PrioHigh
	case PrioMedium:
		return xlsx.PrioMedium
	}
	return xlsx.PrioLow
}

func prioColor(p string) string {
	switch p {
	case PrioHigh:
		return xlsx.ColorRed
	case PrioMedium:
		return xlsx.ColorAmber
	}
	return xlsx.ColorGrey
}

func s(v any, st xlsx.Style) xlsx.Cell { return xlsx.Cell{Value: v, Style: st} }

// count renders a number, red when it counts suspicious findings.
func count(n int, suspicious bool) xlsx.Cell {
	if suspicious && n > 0 {
		return s(n, xlsx.Bad)
	}
	return s(n, xlsx.Number)
}

func (r *Report) suspicious() []Finding {
	var out []Finding
	for _, f := range r.Findings {
		if f.Kind.Suspicious() {
			out = append(out, f)
		}
	}
	return out
}

// WriteXLSX writes the workbook: overview, work list, defect list for
// Talkspirit, one sheet per area, not verifiable data, audit log, notes.
func (r *Report) WriteXLSX(w io.Writer) error {
	wb := &xlsx.Workbook{Title: "Migrationsabgleich Holaspirit → Talkspirit", Creator: "talkspirit-backup compare"}
	sus := r.suspicious()
	wb.Sheets = append(wb.Sheets, r.overviewSheet(len(sus)), r.workSheet(sus), r.defectSheet(sus))
	for i, a := range r.Areas {
		if n := r.areaCount(a.Area); n > 0 {
			wb.Sheets = append(wb.Sheets, r.areaSheet(i+1, a))
		}
	}
	wb.Sheets = append(wb.Sheets, r.notVerifiableSheet())
	if r.AuditAvailable {
		wb.Sheets = append(wb.Sheets, r.auditSheet())
	}
	return wb.Write(w)
}

func (r *Report) areaCount(area string) int {
	n := 0
	for _, f := range r.Findings {
		if f.Area == area {
			n++
		}
	}
	return n
}

func (r *Report) cutoff() string {
	t := r.MigratedAt.Format("02.01.2006 15:04") + " UTC"
	if r.MigratedAtGuessed {
		t += " (geschätzt: " + r.MigratedAtEvidence + ")"
	}
	return t
}

func (r *Report) overviewSheet(suspicious int) *xlsx.Sheet {
	sh := &xlsx.Sheet{Name: "Übersicht", TabColor: xlsx.ColorBlue, Landscape: true, Heights: map[int]float64{1: 30}}
	sh.Widths = []float64{11, 26, 12, 12, 12, 10, 10, 13, 11, 13, 13, 13, 12, 12, 44}
	const cols = 15
	merge := func(row int) { sh.Merges = append(sh.Merges, fmt.Sprintf("A%d:%s", row, xlsx.Ref(cols, row))) }

	merge(sh.Add(s("Migrationsabgleich Holaspirit → Talkspirit", xlsx.Title)))
	merge(sh.Add(s("Von oben nach unten nach Wichtigkeit: Struktur (Personen, Kreise, Rollen) zuerst, Meeting-Notizen zuletzt.", xlsx.Muted)))
	sh.Add()

	info := [][2]string{
		{"Stichtag Migration", r.cutoff()},
		{"Holaspirit-Backup", fmt.Sprintf("%s (%s)", r.HSDir, fmtTime(r.HSTime))},
		{"Talkspirit-Backup", fmt.Sprintf("%s (%s)", r.TSDir, fmtTime(r.TSTime))},
		{"Verdachtsfälle", fmt.Sprintf("%d – Blatt „Arbeitsliste“ (Team) und „Mängelliste“ (an Talkspirit)", suspicious)},
	}
	for _, kv := range info {
		row := sh.Add(s(kv[0], xlsx.Label), s(nil, xlsx.Label), s(kv[1], xlsx.Wrap))
		sh.Merges = append(sh.Merges, fmt.Sprintf("A%d:B%d", row, row), fmt.Sprintf("C%d:%s", row, xlsx.Ref(cols, row)))
		for c := 4; c <= cols; c++ {
			sh.Rows[row-1] = append(sh.Rows[row-1], s(nil, xlsx.Wrap))
		}
	}
	sh.Add()
	merge(sh.Add(s("Ergebnis pro Bereich", xlsx.Subtitle)))

	head := sh.Add(s("Priorität", xlsx.Header), s("Bereich", xlsx.Header), s("Holaspirit", xlsx.Header), s("Talkspirit", xlsx.Header),
		s("zugeordnet", xlsx.Header), s("Fehlt", xlsx.Header), s("Teil fehlt", xlsx.Header), s("Abweichend (unverändert)", xlsx.Header),
		s("An anderer Stelle", xlsx.Header), s("Nur in Talkspirit (vor Stichtag)", xlsx.Header), s("Verdachtsfälle total", xlsx.Header),
		s("Nach Stichtag bearbeitet", xlsx.Header), s("Neu nach Stichtag", xlsx.Header), s("Unsicher zugeordnet", xlsx.Header),
		s("Hinweis", xlsx.Header))
	sh.Heights[head] = 42
	var tot [12]int
	for _, a := range r.Areas {
		k := a.Kinds
		susp := k[Missing] + k[MissingPart] + k[DiffUntouched] + k[Moved] + k[OnlyInTS]
		vals := []int{a.HS, a.TS, a.Matched, k[Missing], k[MissingPart], k[DiffUntouched], k[Moved], k[OnlyInTS], susp,
			k[DiffEdited] + k[DiffHSLater], k[NewInTS], k[Uncertain]}
		p := AreaPriority(a.Area)
		row := []xlsx.Cell{s(p, prioStyle(p)), s(a.Area, xlsx.Bold)}
		for i, v := range vals {
			tot[i] += v
			susCol := i >= 3 && i <= 8
			c := count(v, susCol)
			if i == 8 && v > 0 {
				c = s(v, xlsx.Bad)
			}
			row = append(row, c)
		}
		row = append(row, s(a.Note, xlsx.Wrap))
		sh.Add(row...)
	}
	totRow := []xlsx.Cell{s("Total", xlsx.TotalLabel), s("", xlsx.TotalLabel)}
	for _, v := range tot {
		totRow = append(totRow, s(v, xlsx.Total))
	}
	totRow = append(totRow, s("", xlsx.TotalLabel))
	sh.Add(totRow...)

	sh.Add()
	merge(sh.Add(s("Einordnung der Befunde", xlsx.Subtitle)))
	sh.Add(s("Verdacht", xlsx.Header), s("Befund", xlsx.Header), s("Bedeutung", xlsx.Header))
	legendRow := len(sh.Rows)
	sh.Merges = append(sh.Merges, fmt.Sprintf("C%d:%s", legendRow, xlsx.Ref(cols, legendRow)))
	for _, k := range kindOrder {
		v, st := "nein", xlsx.Good
		if k.Suspicious() {
			v, st = "ja", xlsx.BadText
		}
		row := sh.Add(s(v, st), s(kindLabel[k], xlsx.Bold), s(kindExplain[k], xlsx.Wrap))
		sh.Merges = append(sh.Merges, fmt.Sprintf("C%d:%s", row, xlsx.Ref(cols, row)))
	}

	sh.Add()
	merge(sh.Add(s("Wichtig zu wissen", xlsx.Subtitle)))
	for _, n := range r.Notes {
		row := sh.Add(s("• "+n, xlsx.Muted))
		merge(row)
		sh.Heights[row] = 30
	}
	merge(sh.Add(s("• Arbeitsliste: Status pro Zeile setzen. Vor dem Versand der Mängelliste die Zeilen entfernen, die im Team als „Kein Fehler“ markiert wurden (gleiche Nr.).", xlsx.Muted)))
	return sh
}

func fmtTime(t time.Time) string {
	if t.IsZero() {
		return "Zeitpunkt unbekannt"
	}
	return t.Format("02.01.2006 15:04") + " UTC"
}

// workSheet is the team's list: every suspicious finding, top-down, with
// the original to copy back and status/comment columns.
func (r *Report) workSheet(sus []Finding) *xlsx.Sheet {
	sh := &xlsx.Sheet{Name: "Arbeitsliste", TabColor: xlsx.ColorRed, Landscape: true, FreezeRows: 1, FreezeCols: 0, FilterRow: 1, FilterCols: 14,
		Heights: map[int]float64{1: 30}}
	sh.Widths = []float64{7, 10, 18, 30, 30, 24, 50, 40, 30, 14, 16, 30, 26, 26}
	sh.Add(hdr("Nr", "Priorität", "Bereich", "Kreis / Projekt", "Objekt", "Befund", "Holaspirit (Soll – zum Übernehmen)",
		"Talkspirit (Ist)", "Hinweis", "Status", "Verantwortlich", "Kommentar", "Holaspirit-ID", "Talkspirit-ID")...)
	for i, f := range sus {
		p := AreaPriority(f.Area)
		sh.Add(s(i+1, xlsx.Number), s(p, prioStyle(p)), s(f.Area, xlsx.Text), s(f.Circle, xlsx.Wrap), s(f.Key, xlsx.Bold),
			s(what(f), xlsx.BadText), s(f.Original, xlsx.Wrap), s(f.Current, xlsx.Wrap), s(f.Detail, xlsx.Wrap),
			s("Offen", xlsx.Input), s(nil, xlsx.Input), s(nil, xlsx.Input), s(f.HSID, xlsx.Text), s(f.TSID, xlsx.Text))
	}
	if len(sus) > 0 {
		sh.Validations = []xlsx.Validation{{Col: 10, FromRow: 2, ToRow: len(sus) + 1, List: statusList}}
	}
	return sh
}

// defectSheet is the list for Talkspirit: same rows and numbers, no internal
// columns, target vs. actual as evidence.
func (r *Report) defectSheet(sus []Finding) *xlsx.Sheet {
	sh := &xlsx.Sheet{Name: "Mängelliste Talkspirit", TabColor: xlsx.ColorRed, Landscape: true, FreezeRows: 1, FilterRow: 1, FilterCols: 10,
		Heights: map[int]float64{1: 30}}
	sh.Widths = []float64{7, 10, 18, 30, 30, 26, 55, 45, 26, 26}
	sh.Add(hdr("Nr", "Priorität", "Bereich", "Kreis / Projekt", "Objekt", "Mangel", "Soll (Stand Holaspirit vor Migration)",
		"Ist (Stand Talkspirit)", "Holaspirit-ID", "Talkspirit-ID")...)
	for i, f := range sus {
		p := AreaPriority(f.Area)
		ist := f.Current
		if ist == "" {
			ist = "– nicht vorhanden –"
		}
		sh.Add(s(i+1, xlsx.Number), s(p, prioStyle(p)), s(f.Area, xlsx.Text), s(f.Circle, xlsx.Wrap), s(f.Key, xlsx.Bold),
			s(what(f), xlsx.BadText), s(f.Original, xlsx.Wrap), s(ist, xlsx.Wrap), s(f.HSID, xlsx.Text), s(f.TSID, xlsx.Text))
	}
	return sh
}

// areaSheet lists every finding of one area, suspicious or not.
func (r *Report) areaSheet(n int, a *AreaStats) *xlsx.Sheet {
	p := AreaPriority(a.Area)
	sh := &xlsx.Sheet{Name: fmt.Sprintf("%02d %s", n, a.Area), TabColor: prioColor(p), Landscape: true, FreezeRows: 1, FilterRow: 1, FilterCols: 10,
		Heights: map[int]float64{1: 30}}
	sh.Widths = []float64{9, 30, 30, 30, 26, 50, 40, 30, 26, 26}
	sh.Add(hdr("Verdacht", "Einordnung", "Kreis / Projekt", "Objekt", "Befund", "Holaspirit", "Talkspirit", "Hinweis", "Holaspirit-ID", "Talkspirit-ID")...)
	for _, f := range r.Findings {
		if f.Area != a.Area {
			continue
		}
		v, vs, ks := "nein", xlsx.Good, xlsx.Good
		if f.Kind.Suspicious() {
			v, vs, ks = "ja", xlsx.BadText, xlsx.BadText
		} else if f.Kind == Uncertain {
			v, vs, ks = "prüfen", xlsx.Wrap, xlsx.Wrap
		}
		sh.Add(s(v, vs), s(kindLabel[f.Kind], ks), s(f.Circle, xlsx.Wrap), s(f.Key, xlsx.Bold), s(what(f), xlsx.Wrap),
			s(f.Original, xlsx.Wrap), s(f.Current, xlsx.Wrap), s(f.Detail, xlsx.Wrap), s(f.HSID, xlsx.Text), s(f.TSID, xlsx.Text))
	}
	return sh
}

func (r *Report) notVerifiableSheet() *xlsx.Sheet {
	sh := &xlsx.Sheet{Name: "Nicht prüfbar", TabColor: xlsx.ColorGrey, FreezeRows: 3}
	sh.Widths = []float64{26, 12, 90}
	sh.Add(s("Holaspirit-Daten ohne Gegenstück in der Talkspirit-API – bitte stichprobenartig in der Oberfläche prüfen", xlsx.Subtitle))
	sh.Merges = append(sh.Merges, "A1:C1")
	sh.Add()
	sh.Add(hdr("Daten", "Anzahl in Holaspirit", "Grund")...)
	for _, n := range r.NotVerifiable {
		sh.Add(s(n.What, xlsx.Bold), s(n.HS, xlsx.Number), s(n.Reason, xlsx.Wrap))
	}
	return sh
}

func (r *Report) auditSheet() *xlsx.Sheet {
	sh := &xlsx.Sheet{Name: "Audit-Log nach Stichtag", TabColor: xlsx.ColorGrey, FreezeRows: 3}
	sh.Widths = []float64{50, 14}
	sh.Add(s("Ereignisse in Talkspirit nach dem Stichtag (Kontext für fehlende Objekte)", xlsx.Subtitle))
	sh.Merges = append(sh.Merges, "A1:B1")
	sh.Add()
	sh.Add(hdr("Ereignis", "Anzahl")...)
	types := make([]string, 0, len(r.AuditAfter))
	for t := range r.AuditAfter {
		types = append(types, t)
	}
	sort.Slice(types, func(i, j int) bool {
		if r.AuditAfter[types[i]] != r.AuditAfter[types[j]] {
			return r.AuditAfter[types[i]] > r.AuditAfter[types[j]]
		}
		return types[i] < types[j]
	})
	for _, t := range types {
		st := xlsx.Text
		if strings.Contains(strings.ToLower(t), "delet") || strings.Contains(strings.ToLower(t), "remov") {
			st = xlsx.BadText
		}
		sh.Add(s(t, st), s(r.AuditAfter[t], xlsx.Number))
	}
	if len(types) == 0 {
		sh.Add(s("keine Ereignisse nach dem Stichtag", xlsx.Muted))
	}
	return sh
}

func hdr(names ...string) []xlsx.Cell {
	out := make([]xlsx.Cell, len(names))
	for i, n := range names {
		out[i] = s(n, xlsx.Header)
	}
	return out
}

func what(f Finding) string {
	if f.What != "" {
		return f.What
	}
	return kindLabel[f.Kind]
}
