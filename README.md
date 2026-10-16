# holaspirit-backup → talkspirit-backup

Automatisiertes Backup-Werkzeug für die Organisationsdaten von **Talkspirit** (über die
offizielle [Partner API](https://partner-api.talkspirit.com/v1/docs)) als JSON-Dateien mit
SHA256-Integritätsmanifest und HMAC-SHA-256-Signatur, plus ein Vergleich gegen ein altes
Holaspirit-Backup, der zeigt, was bei der Migration verloren ging.

> Holaspirit wurde durch Talkspirit ersetzt; die Holaspirit-API wird am 30.10.2026 abgeschaltet.
> **v0.5.0 ist der letzte Holaspirit-Release** — Holaspirit-Backups damit prüfen (`verify`)
> und als Referenz für `compare` verwenden. Ab v0.6.0 sichert das Tool Talkspirit.

## Features

- Sichert alle lesbaren Ressourcen der Partner API (GET-only, kein Schreibzugriff): Kreise,
  Rollen (inkl. Purpose, Domänen, Accountabilities, Besetzung), Rollen-Vorlagen, Personen,
  Mitgliedschaften, Spannungen, Meetings mit Traktanden, Aufgaben mit Unteraufgaben und
  Kommentaren, Projekte mit Spalten und Labels, Ziele mit Key Results, Zeiträume, Dokumente
  mit Inhalt und Kommentaren, benutzerdefinierte Felder, Audit-Log
- Cursor-Paginierung über alle Seiten; Abbruch am 10'000er-Limit der API wird als
  `incomplete` markiert (Exit-Code 1), nie als vollständig
- Rate-Limit-bewusst: richtet sich nach `X-RateLimit-*` und `Retry-After` (Standard ca.
  100 Anfragen/Minute pro Schlüssel)
- `backup compare` — Abgleich Holaspirit-Backup ↔ Talkspirit-Backup mit Einordnung, ob eine
  Abweichung nach der Migration bewusst bearbeitet wurde oder auf die Migration zurückgeht
- SHA256-Hashes pro Datei + HMAC-SHA-256-Manifest-Signatur, `backup verify --dir <path>`
- Plattform-Binaries: Linux amd64/arm64, Windows amd64
- API-Schlüssel via `TALKSPIRIT_API_KEY` oder Windows Credential Manager (DPAPI-geschützt)

## Installation

Binary von [GitHub Releases](https://github.com/kAYd9iN/holaspirit-backup/releases) herunterladen.

| Plattform | Datei |
|-----------|-------|
| Windows (64-bit) | `backup-windows-amd64.exe` |
| Linux (x86_64) | `backup-linux-amd64` |
| Linux (ARM64) | `backup-linux-arm64` |

**Integrität prüfen:**

```bash
# SHA256 gegen SHA256SUMS prüfen
sha256sum -c SHA256SUMS

# SLSA L2 Provenance verifizieren (erfordert gh CLI)
gh attestation verify backup-linux-amd64 --repo kAYd9iN/holaspirit-backup

# cosign Bundle verifizieren
cosign verify-blob \
  --bundle backup-linux-amd64.bundle \
  backup-linux-amd64
```

## Konfiguration

**API-Schlüssel anlegen:** Ein Talkspirit-Administrator aktiviert das API-Modul und erstellt
unter `https://<subdomain>.talkspirit.com/app/admin/api-keys` einen Schlüssel
(`ts_partner_…`, wird nur einmal angezeigt) mit **allen Lese-Scopes**: `users:read`,
`circles:read`, `roles:read`, `tasks:read`, `projects:read`, `goals:read`, `documents:read`,
`meetings:read`, `tensions:read`, `audit_log:read`. Keine Schreib-Scopes vergeben.

> Der Schlüssel liest nur, was sein technischer Benutzer sehen darf. Für ein vollständiges
> Backup muss er Admin-Rechte haben (Audit-Log nur für Admins); private Inhalte anderer
> Personen bleiben unsichtbar.

```powershell
# Variante A: Umgebungsvariable (hat Vorrang)
$env:TALKSPIRIT_API_KEY = "ts_partner_…"
# Variante B: Windows Credential Manager
cmdkey /generic:talkspirit-backup /user:api /pass:ts_partner_…
```

**Backup ausführen:**

```powershell
backup.exe --dry-run                          # Schlüssel + Scopes prüfen
backup.exe --output C:\Backups\talkspirit
```

Ein vollständiger Lauf macht pro Meeting, Aufgabe, Ziel, Projekt und Dokument eigene
Anfragen (Traktanden, Kommentare) und dauert bei grossen Organisationen mehrere Stunden;
`--quick` lässt diese Einzelabrufe weg.

**Mit dem Holaspirit-Backup vergleichen:**

```powershell
backup.exe compare --holaspirit C:\Backups\holaspirit\2026-10-14T15-04-21 `
                   --talkspirit C:\Backups\talkspirit\2026-10-16T18-00-00 `
                   --migrated-at 2026-10-15 --out C:\Backups\vergleich
```

Ergebnis: `vergleich.md` (Bericht) und `befunde.csv` (alle Befunde, Excel-tauglich). Beide
enthalten Namen und E-Mail-Adressen — vertraulich behandeln. Wie der Abgleich arbeitet, steht
in [docs/architecture.md](docs/architecture.md#vergleich-holaspirit--talkspirit).

**Integrität prüfen:**

```powershell
backup.exe verify --dir C:\Backups\talkspirit\2026-10-16T18-00-00
```

## CLI-Referenz

```
backup [Optionen]
backup verify --dir <path>
backup compare --holaspirit <dir> --talkspirit <dir> [--out <dir>] [--migrated-at YYYY-MM-DD[THH:MM]]

Optionen:
  --output PATH     Backup-Zielverzeichnis (Standard: ./backup)
  --quick           Kommentare und Traktanden nicht einzeln abrufen
  --dry-run         Schlüssel prüfen (GET /v1/whoami), nichts schreiben
  --timeout MIN     Gesamt-Timeout in Minuten (Standard: 480)
  --base-url URL    API-Host (Standard: https://partner-api.talkspirit.com)
  --audit           Hostname + Benutzer im Manifest erfassen (Audit-Trail)
  --version         Version anzeigen

compare:
  --migrated-at     Ende der Migration (UTC); leer = aus den Talkspirit-Daten geschätzt
  --out             Zielordner für vergleich.md + befunde.csv (Standard: ./vergleich)
```

## Security & Trust

| Maßnahme | Details |
|----------|---------|
| SLSA Level 2 | Provenance-Attestation via `actions/attest-build-provenance`, verifizierbar mit `gh attestation verify` |
| SLSA Level 3 | Linux-amd64-Binary isoliert gebaut via slsa-github-generator (`backup-linux-amd64-slsa3` + `.intoto.jsonl`), verifizierbar mit `slsa-verifier` |
| cosign | Keyless-Signing aller Release-Binaries via Sigstore OIDC |
| HMAC-SHA-256 | Manifest-Signatur jedes Backups (`backup-manifest.sig`) |
| GET-only API | HTTP-Client exponiert nur `Get()` — kein Schreibzugriff möglich |
| TLS ≥ 1.2 | Explizit erzwungen, Zertifikatsprüfung nicht abschaltbar |
| Release-Security-Gate | Release blockiert bei offenen `security`-Issues, fehlgeschlagenem govulncheck/gosec/Race-Test oder nicht-NIST-konformer Krypto |
| CBOM + NIST-Policy | `docs/cbom.cdx.json` (CycloneDX 1.6) gegen NIST SP 800-131A via OPA/conftest geprüft |
| SHA-gepinnte Actions | Alle CI-Actions auf Commit-SHA gepinnt (Supply-Chain-Schutz) |
| govulncheck + gosec | SAST bei jedem Push (versionsgepinnt) |
| OpenSSF Scorecard | Wöchentliches Security-Scoring (GitHub Security tab) |
| Dependabot | 7-Tage-Cooldown + Auto-Merge reifer Minor/Patch-Bumps; Major manuell |
| Branch-Protection | `main`-Ruleset: PR + Pflicht-Checks erzwungen, kein Force-Push |
| vendor/ committed | Supply-Chain: alle Abhängigkeiten eingecheckt |

Sicherheitslücken bitte per [GitHub Private Vulnerability Reporting](https://github.com/kAYd9iN/holaspirit-backup/security/advisories/new) melden — siehe [SECURITY.md](SECURITY.md).

## Self-Update-Schlaufe

Ein täglicher Workflow (`api-update-check`) vergleicht den publizierten OpenAPI-Vertrag
der Talkspirit Partner API (`/v1/openapi.json`, öffentlich) gegen die committete
Baseline (`docs/api-snapshot.json`) — ohne Credentials. Neue Lese-Routen, die das Tool
noch nicht sichert, erscheinen dort unter `__untracked_get_routes__`. Bei Drift passt Claude
den Code automatisch an und öffnet einen PR; nach dem Merge erzeugt `auto-release`
einen versionierten, signierten Release (sofern das Security-Gate frei ist).

## Dokumentation

- [Architektur](docs/architecture.md) — Projektstruktur, Ablauf, Manifest-Format
- [Sicherheitskonzept](docs/security-concept.md) — Prinzipien, Token-Verwaltung, CI-Security, Pentest-Ergebnisse
- [Trust-Model](docs/trust-model.md) — Vertrauensgrenzen, was die HMAC-Signatur garantiert (und was nicht)
- [Betrieb & Installation](docs/operations.md) — Installation, Windows Task Scheduler, Veeam-Integration
- [Supply Chain & Vertrauenskette](docs/supply-chain.md) — SLSA L2, cosign, Scorecard, CBOM, Verifikationsanleitung
- [Security Policy](SECURITY.md) — Sicherheitslücken melden, Krypto-/Daten-Sicherheit

## Entwicklung

```bash
# Tests
go test -race -cover ./...

# Build
go build -mod=vendor -ldflags="-X main.version=dev" -o backup ./cmd/backup/

# Security-Scan (gleiche Versionen wie CI)
go run golang.org/x/vuln/cmd/govulncheck@v1.1.4 ./...
go run github.com/securego/gosec/v2/cmd/gosec@v2.24.7 ./...

# CBOM gegen NIST-Policy prüfen
go run github.com/open-policy-agent/conftest@v0.68.2 test docs/cbom.cdx.json --policy policy
```

## Lizenz

Lizenziert unter der **GNU General Public License v3.0** — siehe [LICENSE](LICENSE).

Freie Software: nutzen, studieren, anpassen und weitergeben ist erlaubt (auch
kommerziell). Copyleft: jede Weitergabe oder modifizierte Version muss ebenfalls
unter der GPL-3.0 frei bleiben — es kann keine geschlossene, proprietäre Version
daraus gemacht werden.
