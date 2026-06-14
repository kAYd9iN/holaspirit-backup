# holaspirit-backup

Automatisiertes Backup-Werkzeug für alle Holaspirit-Organisationsdaten als JSON-Dateien  
mit SHA256-Integritätsmanifest und HMAC-SHA-256-Signatur.

## Features

- Sichert 21 Holaspirit-Endpunkte (GET-only, kein Schreibzugriff)
- SHA256-Hashes pro Datei + HMAC-SHA-256-Manifest-Signatur
- Bounded Worker Pool (5 Goroutinen), Rate-Limiter (250 req / 5 min)
- Plattform-Binaries: Linux amd64/arm64, Windows amd64
- Token via Windows Credential Manager (DPAPI-geschützt) oder Umgebungsvariable
- `backup verify --dir <path>` — Integritätsprüfung nach dem Backup

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

**API-Token hinterlegen (Windows):**

```powershell
cmdkey /generic:holaspirit-backup /user:api /pass:api:DEIN_TOKEN_HIER
```

**Backup ausführen:**

```powershell
backup.exe --output C:\Backups\holaspirit
```

**Integrität prüfen:**

```powershell
backup.exe verify --dir C:\Backups\holaspirit\2026-03-06T02-00-00
```

## CLI-Referenz

```
backup [Optionen]
backup verify --dir <path>

Optionen:
  --output PATH     Backup-Zielverzeichnis (Standard: ./backup)
  --org-id ID       Organisations-ID (auto-detected)
  --dry-run         Verbindung testen ohne Daten zu schreiben
  --timeout MIN     Gesamt-Timeout in Minuten (Standard: 120)
  --audit           Hostname + Benutzer im Manifest erfassen (Audit-Trail)
  --version         Version anzeigen
```

## Security & Trust

| Maßnahme | Details |
|----------|---------|
| SLSA Level 2 | Provenance-Attestation via `actions/attest-build-provenance`, verifizierbar mit `gh attestation verify` |
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

Ein täglicher Workflow (`api-update-check`) vergleicht Holaspirits publizierte
OpenAPI-Spec (aus der öffentlichen `/api/doc/`-Seite) gegen die committete
Baseline (`docs/api-snapshot.json`) — ohne Credentials. Bei Drift passt Claude
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
