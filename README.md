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
  --version         Version anzeigen
```

## Security & Trust

| Maßnahme | Details |
|----------|---------|
| SLSA Level 2 | Provenance-Attestation via `actions/attest-build-provenance`, verifikbar mit `gh attestation verify` |
| cosign | Keyless-Signing aller Release-Binaries via Sigstore OIDC |
| HMAC-SHA-256 | Manifest-Signatur jedes Backups (`backup-manifest.sig`) |
| GET-only API | HTTP-Client exponiert nur `Get()` — kein Schreibzugriff möglich |
| SHA-gepinnte Actions | Alle CI-Actions auf Commit-SHA gepinnt (Supply-Chain-Schutz) |
| govulncheck + gosec | SAST bei jedem Push (versionsgepinnt) |
| OpenSSF Scorecard | Wöchentliches Security-Scoring (GitHub Security tab) |
| vendor/ committed | Supply-Chain: alle Abhängigkeiten eingecheckt |

Sicherheitslücken bitte per [GitHub Private Vulnerability Reporting](https://github.com/kAYd9iN/holaspirit-backup/security/advisories/new) melden — siehe [SECURITY.md](SECURITY.md).

## Dokumentation

- [Architektur](docs/architecture.md) — Projektstruktur, Ablauf, Manifest-Format
- [Sicherheitskonzept](docs/security-concept.md) — Prinzipien, Token-Verwaltung, CI-Security, Pentest-Ergebnisse
- [Betrieb & Installation](docs/operations.md) — Installation, Windows Task Scheduler, Veeam-Integration
- [Supply Chain & Vertrauenskette](docs/supply-chain.md) — SLSA L2, cosign, Scorecard, Verifikationsanleitung
- [Security Policy](SECURITY.md) — Sicherheitslücken melden

## Entwicklung

```bash
# Tests
go test -race -cover ./...

# Build
go build -mod=vendor -ldflags="-X main.version=dev" -o backup ./cmd/backup/

# Security-Scan
go run golang.org/x/vuln/cmd/govulncheck@v1.1.3 ./...
go run github.com/securego/gosec/v2/cmd/gosec@v2.21.4 ./...
```
