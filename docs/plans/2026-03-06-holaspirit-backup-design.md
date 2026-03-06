# Holaspirit Backup Tool — Design Document

**Datum:** 2026-03-06
**Status:** Genehmigt
**Confluence:** https://ewigepluseins.atlassian.net/wiki/spaces/HB

---

## 1. Uebersicht

Automatisches Backup-Tool fuer Holaspirit-Organisationsdaten, geschrieben in Go.
Sichert alle Daten der Holaspirit-REST-API als JSON-Dateien in ein lokales Verzeichnis,
das von Veeam weiterverarbeitet wird.

**Scope:** Nur Holaspirit (Talkspirit hat keine oeffentliche REST API)

---

## 2. Entscheidungen

| Thema | Entscheidung | Begruendung |
|---|---|---|
| Tech-Stack | Go | Statisches Binary, minimale Deps, Windows-kompatibel, CISO-konform |
| Architektur | Concurrent Fetcher + Token-Bucket Rate-Limiter | Schnell, Go-nativ, Rate-Limit praezise |
| Secrets | Windows Credential Manager (DPAPI) | Kein Klartext, OS-Schutz, Cronjob-tauglich |
| Format | JSON pro Ressource + Manifest (SHA256) | Granulare Integritaetspruefung, einfacher Restore |
| Backup-Ziel | Lokales Verzeichnis | Veeam uebernimmt Verschluesselung & Storage |
| Exports | JSON + PDF + Excel (async API) | Mehrschichtige Sicherung |
| Restore | Stufe 2 (optional, spaeter) | Holaspirit hat keinen Import-Endpoint |

---

## 3. Projektstruktur

```
holaspirit-backup/
├── cmd/
│   └── backup/
│       └── main.go          # Einstiegspunkt, CLI-Flags
├── internal/
│   ├── api/
│   │   ├── client.go        # HTTP-Client, Rate-Limiter, Retry
│   │   └── endpoints.go     # alle Holaspirit-Endpoints
│   ├── backup/
│   │   ├── runner.go        # orchestriert alle Fetcher (concurrent)
│   │   └── manifest.go      # SHA256-Hashes, backup-manifest.json
│   ├── credentials/
│   │   └── wincred.go       # Windows Credential Manager
│   └── storage/
│       └── writer.go        # Dateien schreiben, Ordnerstruktur
├── tests/                   # Unit- und Integrationstests
├── docs/plans/
├── go.mod
├── go.sum
└── .github/workflows/ci.yml
```

---

## 4. Holaspirit API

- **Base URL:** `https://app.holaspirit.com`
- **Auth:** Read-Only Token (`api:...`), laeuft nie ab
- **Rate Limit:** 250 req / 5 min

### Endpoints (vollstaendig)

| Kategorie | Endpoints |
|---|---|
| Org & Nutzer | `me`, `organization` |
| Struktur | `circles`, `circles-timespent`, `roles`, `members`, `authority` |
| Governance | `tensions`, `policies`, `meetings` |
| OKRs | `objectives`, `keyresults` |
| Projekte | `tasks`, `boards`, `columns` |
| Checklists | `checklists`, `metrics` |
| Publikationen | `publications`, `categories` |
| Sonstiges | `attachments`, `chartviews`, `calendars`, `backups` |
| Async Exports | PDF, Spreadsheet |

---

## 5. Backup-Ablauf

```
1. Token aus Windows Credential Manager laden
2. Output-Verzeichnis anlegen: backup/YYYY-MM-DDTHH-MM-SS/
3. Organisation-ID ermitteln (GET /api/me)
4. Concurrent Fetcher starten (alle Endpoints parallel, Rate-Limiter)
5. Paginierung: alle Pages zu einer JSON-Datei zusammenfuehren
6. Async Exports (PDF + XLSX): ausloesen, pollen, herunterladen
7. Manifest schreiben (SHA256 + Timestamp + Records)
8. Exit 0 oder Exit 1 mit Log
```

**Fehlerbehandlung:**
- 3 Retries mit Exponential Backoff pro Endpoint
- HTTP 429: warten bis Fenster ablaeuft
- Fehlgeschlagener Endpoint bricht Lauf nicht ab, wird als `failed` im Manifest markiert

---

## 6. Output-Struktur

```
backup/2026-03-06T02-00-00/
  circles.json, roles.json, members.json, policies.json,
  meetings.json, objectives.json, keyresults.json, tasks.json,
  boards.json, columns.json, checklists.json, metrics.json,
  publications.json, categories.json, tensions.json,
  attachments.json, chartviews.json, calendars.json, backups.json,
  export.xlsx, export.pdf,
  backup-manifest.json, backup.log
```

### backup-manifest.json

```json
{
  "timestamp": "2026-03-06T02:00:00Z",
  "tool_version": "1.0.0",
  "organization_id": "org_xxx",
  "files": [
    {"name": "circles.json", "sha256": "abc...", "records": 42, "status": "ok"}
  ],
  "summary": {"total_files": 21, "successful": 21, "failed": 0}
}
```

---

## 7. Sicherheit

- **Least Privilege:** Read-Only Token
- **Secrets:** Windows Credential Manager (DPAPI, nie Klartext auf Disk)
- **Supply Chain:** `go mod vendor` eingecheckt
- **SAST:** `gosec` + `govulncheck` im CI

---

## 8. CI/CD (GitHub Actions)

```yaml
jobs:
  - govulncheck   # CVE-Pruefung
  - gosec         # SAST
  - go test       # Unit-Tests
  - go build      # Windows-Binary (GOOS=windows GOARCH=amd64)
```

---

## 9. Betrieb

```powershell
# Token einrichten (einmalig)
cmdkey /generic:holaspirit-backup /user:api /pass:api:TOKEN

# Backup-Verzeichnis
mkdir C:\Backups\holaspirit

# Task Scheduler einrichten (taegliich 02:00)
Register-ScheduledTask -TaskName "Holaspirit Backup" ...

# Veeam-Job: C:\Backups\holaspirit einschliessen (03:00 Uhr)
```

---

## 10. Roadmap: Stufe 2 — Automatischer Restore (optional)

Holaspirit bietet keinen Import-Endpoint. Ein Restore via POST/PATCH-Endpoints
erfordert ID-Mapping und Abhaengigkeitsaufloesung:

**Reihenfolge:** Circles > Roles > Members > Policies > OKRs > Tasks > Meetings

**Nicht restorebar:** Meeting-History, Feeds, binaere Anhaenge, historische Zeiterfassung

**Subkommando (wenn beauftragt):** `backup.exe restore --from PATH --dry-run`

---

## 11. GitHub

- **Repo:** `github.com/kAYd9iN/holaspirit-backup`
- **Releases:** `backup.exe` als Artifact
- **Versioning:** Semantic Versioning (`vMAJOR.MINOR.PATCH`)
