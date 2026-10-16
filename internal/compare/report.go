package compare

import (
	"bufio"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// detailLimit caps how many findings per area and kind the Markdown report
// lists; the CSV always holds all of them.
const detailLimit = 60

var kindLabel = map[Kind]string{
	Missing:       "Fehlt in Talkspirit",
	MissingPart:   "Teil fehlt (Accountability, Besetzung, Key Result …)",
	DiffUntouched: "Abweichung, Talkspirit-Objekt seit Migration unverändert",
	Moved:         "An anderer Stelle (Kreis/Projekt) gefunden",
	OnlyInTS:      "Nur in Talkspirit, aber vor Migrationsende angelegt",
	Uncertain:     "Nur über den Titel zugeordnet (Paarung unsicher, Anzahl stimmt)",
	DiffHSLater:   "Abweichung, aber in Holaspirit nach der Migration geändert",
	DiffEdited:    "Abweichung, in Talkspirit nach der Migration bearbeitet",
	NewInTS:       "Neu in Talkspirit (nach der Migration angelegt)",
}

// WriteFiles writes vergleich.md and befunde.csv into dir (0600 files: the
// report holds names and e-mail addresses).
func (r *Report) WriteFiles(dir string) (md, csvPath string, err error) {
	if err := os.MkdirAll(dir, 0750); err != nil {
		return "", "", err
	}
	md = filepath.Join(dir, "vergleich.md")
	csvPath = filepath.Join(dir, "befunde.csv")
	if err := writeFile(md, r.WriteMarkdown); err != nil {
		return "", "", err
	}
	if err := writeFile(csvPath, r.WriteCSV); err != nil {
		return "", "", err
	}
	return md, csvPath, nil
}

func writeFile(path string, fn func(io.Writer) error) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600) // #nosec G304 -- operator-chosen output dir + fixed names
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	if err := fn(w); err != nil {
		f.Close()
		return err
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// WriteCSV writes every finding; semicolon-separated with a UTF-8 BOM so
// Excel with German/Swiss settings opens it directly.
func (r *Report) WriteCSV(w io.Writer) error {
	if _, err := io.WriteString(w, "ï»¿"); err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	cw.Comma = ';'
	if err := cw.Write([]string{"Bereich", "Befund", "Verdacht Migrationsfehler", "Objekt", "Details", "Holaspirit-ID", "Talkspirit-ID"}); err != nil {
		return err
	}
	for _, f := range r.Findings {
		sus := "nein"
		if f.Kind.Suspicious() {
			sus = "ja"
		}
		if err := cw.Write([]string{f.Area, string(f.Kind), sus, csvSafe(f.Key), csvSafe(f.Detail), f.HSID, f.TSID}); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// csvSafe neutralizes values a spreadsheet would run as a formula.
func csvSafe(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

// mdSafe keeps a value on one table/list line.
func mdSafe(s string) string {
	s = strings.NewReplacer("|", "\\|", "\n", " ", "\r", " ").Replace(s)
	return s
}

// WriteMarkdown writes the human-readable report (German).
func (r *Report) WriteMarkdown(w io.Writer) error {
	p := func(format string, a ...any) { fmt.Fprintf(w, format, a...) }

	p("# Vergleich Holaspirit → Talkspirit\n\n")
	p("| | |\n|---|---|\n")
	p("| Holaspirit-Backup | `%s` (%s) |\n", r.HSDir, fmtTime(r))
	p("| Talkspirit-Backup | `%s` (%s) |\n", r.TSDir, r.TSTime.Format("02.01.2006 15:04 UTC"))
	mig := r.MigratedAt.Format("02.01.2006 15:04 UTC")
	if r.MigratedAtGuessed {
		mig += " — **geschätzt**: " + r.MigratedAtEvidence + ". Mit `--migrated-at` korrigieren, falls falsch."
	}
	p("| Migrationsende | %s |\n\n", mig)

	p("**So ist der Bericht zu lesen.** Die Talkspirit-API kennt die alten Holaspirit-IDs nicht. Objekte werden deshalb über fachliche Merkmale zugeordnet: E-Mail, Kreispfad + Name, Titel + Zeitpunkt. ")
	p("Eine Abweichung an einem Talkspirit-Objekt, das seit der Migration **nicht** geändert wurde, deutet auf die Migration hin. ")
	p("Wurde es danach bearbeitet, ist die Abweichung wahrscheinlich gewollt. ")
	p("Fehlende Objekte können auch nach der Migration absichtlich gelöscht worden sein — das Audit-Log am Ende hilft bei der Einordnung.\n\n")
	for _, n := range r.Notes {
		p("> %s\n\n", n)
	}

	p("## Übersicht\n\n")
	p("| Bereich | Holaspirit | Talkspirit | zugeordnet | fehlt | Teil fehlt | Abw. unverändert | verschoben | nur TS (alt) | unsicher | Abw. bearbeitet | neu in TS | Hinweis |\n")
	p("|---|--:|--:|--:|--:|--:|--:|--:|--:|--:|--:|--:|---|\n")
	for _, a := range r.Areas {
		k := a.Kinds
		p("| %s | %d | %d | %d | %s | %s | %s | %s | %s | %d | %d | %d | %s |\n", a.Area, a.HS, a.TS, a.Matched,
			bold(k[Missing]), bold(k[MissingPart]), bold(k[DiffUntouched]), bold(k[Moved]), bold(k[OnlyInTS]),
			k[Uncertain], k[DiffEdited]+k[DiffHSLater], k[NewInTS], mdSafe(a.Note))
	}
	p("\nFettgedruckte Zahlen sind Verdachtsfälle für Migrationsfehler.\n\n")

	p("## Verdacht auf Migrationsfehler\n\n")
	r.writeKinds(w, func(k Kind) bool { return k.Suspicious() })

	p("## Wahrscheinlich gewollte Änderungen und Unsicherheiten\n\n")
	r.writeKinds(w, func(k Kind) bool { return !k.Suspicious() })

	p("## Nicht über die API prüfbar\n\n")
	p("Für diese Holaspirit-Daten gibt es in der Talkspirit Partner API kein Gegenstück. Bitte stichprobenartig in der Oberfläche prüfen.\n\n")
	p("| Holaspirit-Daten | Anzahl | Grund |\n|---|--:|---|\n")
	for _, n := range r.NotVerifiable {
		p("| %s | %d | %s |\n", n.What, n.HS, mdSafe(n.Reason))
	}
	p("\n")

	p("## Audit-Log nach der Migration\n\n")
	if !r.AuditAvailable {
		p("Nicht im Talkspirit-Backup (Scope `audit_log:read` und ein Admin-Schlüssel sind nötig). Ohne Audit-Log lässt sich ein absichtliches Löschen nicht von einem Migrationsverlust unterscheiden.\n")
	} else if len(r.AuditAfter) == 0 {
		p("Keine Ereignisse nach dem Migrationsende.\n")
	} else {
		p("| Ereignis | Anzahl |\n|---|--:|\n")
		types := make([]string, 0, len(r.AuditAfter))
		for t := range r.AuditAfter {
			types = append(types, t)
		}
		sort.Slice(types, func(i, j int) bool { return r.AuditAfter[types[i]] > r.AuditAfter[types[j]] })
		for _, t := range types {
			p("| %s | %d |\n", mdSafe(t), r.AuditAfter[t])
		}
	}
	p("\nAlle Befunde mit IDs stehen in `befunde.csv`.\n")
	return nil
}

func (r *Report) writeKinds(w io.Writer, want func(Kind) bool) {
	any := false
	for _, a := range r.Areas {
		for _, k := range kindOrder {
			if !want(k) || a.Kinds[k] == 0 {
				continue
			}
			any = true
			fmt.Fprintf(w, "### %s — %s (%d)\n\n", a.Area, kindLabel[k], a.Kinds[k])
			n := 0
			for _, f := range r.Findings {
				if f.Area != a.Area || f.Kind != k {
					continue
				}
				if n == detailLimit {
					fmt.Fprintf(w, "- … %d weitere in `befunde.csv`\n", a.Kinds[k]-detailLimit)
					break
				}
				line := "- " + mdSafe(f.Key)
				if f.Detail != "" {
					line += " — " + mdSafe(f.Detail)
				}
				fmt.Fprintln(w, line)
				n++
			}
			fmt.Fprintln(w)
		}
	}
	if !any {
		fmt.Fprintln(w, "Keine.")
		fmt.Fprintln(w)
	}
}

func bold(n int) string {
	if n == 0 {
		return "0"
	}
	return fmt.Sprintf("**%d**", n)
}

func fmtTime(r *Report) string {
	if r.HSTime.IsZero() {
		return "Zeitpunkt unbekannt"
	}
	return r.HSTime.Format("02.01.2006 15:04 UTC")
}
