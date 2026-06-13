# Architektur

## Projektstruktur

```
holaspirit-backup/
├── cmd/
│   └── backup/
│       ├── main.go              # Einstiegspunkt, CLI-Flags
│       ├── verify.go            # verify-Subcommand (Manifest-Signatur prüfen)
│       ├── token_windows.go     # Token via Windows Credential Manager
│       └── token_other.go       # Token-Fallback für Nicht-Windows
├── internal/
│   ├── api/
│   │   ├── client.go            # HTTP-Client (GET-only), Rate-Limiter, Retry
│   │   ├── endpoints.go         # alle 21 Holaspirit-Endpunkte + ValidateOrgID
│   │   └── pagination.go        # FetchAllPages mit maxPages=500 Guard
│   ├── backup/
│   │   ├── runner.go            # Worker Pool (5 bounded Goroutinen)
│   │   └── manifest.go          # SHA256-Hashes + HMAC-SHA-256-Signatur
│   ├── credentials/
│   │   ├── credentials.go       # Interface + Mock
│   │   └── wincred.go           # Windows Credential Manager (build tag windows)
│   └── storage/
│       └── writer.go            # Dateien schreiben, Pfad-Sanitizing, 0600/0750
├── scripts/
│   └── verify_signed_commits.sh # GPG-Commit-Signatur-Prüfung
├── vendor/                      # eingecheckte Abhängigkeiten
├── docs/
│   ├── architecture.md          # diese Datei
│   ├── security-concept.md      # Sicherheitskonzept
│   ├── operations.md            # Betrieb & Installation
│   ├── supply-chain.md          # SLSA, cosign, Scorecard
│   └── plans/                   # Implementierungspläne
├── go.mod
├── go.sum
├── SECURITY.md                  # GitHub Security Policy
└── .github/
    └── workflows/
        ├── security.yml         # govulncheck + gosec + Tests
        ├── build.yml            # Matrix-Build (3 Plattformen)
        ├── release.yml          # GitHub Release + SLSA L2 + cosign + SHA256
        ├── cbom.yml             # CycloneDX CBOM
        ├── commit-signature.yml # GPG-Commit-Prüfung
        ├── scorecard.yml        # OpenSSF Scorecard (wöchentlich)
        └── dependency-review.yml # CVE-Prüfung bei PRs
```

## Architekturentscheidung: Bounded Worker Pool mit Rate-Limiter

Alle Endpunkte werden über einen **bounded Pool von 5 Worker-Goroutinen** abgefragt.  
Ein zentraler Token-Bucket-Limiter stellt sicher, dass das Rate-Limit  
(250 Requests / 5 Minuten) nicht überschritten wird.

```
main.go
  └─ runner.go (Worker Pool: 5 Goroutinen)
       ├─ worker: circles
       ├─ worker: roles
       ├─ worker: members
       ├─ worker: ... (alle 21 Endpunkte, je nach Verfügbarkeit)
       │       ↕ koordiniert über Rate-Limiter + bounded channel
       └─ manifest.go (nach Abschluss aller Worker)
```

Der Pool begrenzt die Parallelität auf 5 gleichzeitige Requests, verhindert  
Resource-Exhaustion und respektiert das API-Rate-Limit zuverlässig.

## Backup-Ablauf

1. Token aus Windows Credential Manager laden (oder `HOLASPIRIT_TOKEN` Env-Variable)
2. Organisations-ID via `ValidateOrgID` validieren
3. Output-Verzeichnis anlegen: `backup/YYYY-MM-DDTHH-MM-SS/` (Berechtigungen: `0750`)
4. Organisation-ID ermitteln via `GET /api/me` (wenn nicht per `--org-id` angegeben)
5. Worker Pool starten (5 Goroutinen, alle 21 Endpunkte)
6. Paginierung: alle Pages pro Endpunkt zu einer JSON-Datei zusammenführen (Berechtigungen: `0600`)
7. Manifest schreiben: SHA256 pro Datei + HMAC-SHA-256-Signatur + Timestamp + Record-Anzahl
8. Exit 0 (Erfolg) oder Exit 1 (Teilerfolg mit Fehler-Log)

## verify-Subcommand

```
backup verify --dir <path>
```

Prüft die HMAC-SHA-256-Signatur des Manifests und verifiziert die SHA256-Hashes aller Backup-Dateien.

| Exit-Code | Bedeutung |
|-----------|-----------|
| 0 | Manifest und alle Datei-Hashes stimmen überein |
| 1 | Signatur ungültig oder Datei-Hash-Abweichung erkannt |
| 2 | Ungültige Argumente |

## Release-Pipeline & Artefakt-Vertrauenskette

Bei jedem `v*`-Tag läuft die folgende Kette:

1. **Build** — 3 Binaries (linux/amd64, linux/arm64, windows/amd64) mit `ldflags -X main.version`
2. **SLSA L2 Attestation** — `actions/attest-build-provenance` erzeugt signierte Provenance pro Binary
3. **cosign Signing** — Keyless-Signatur via GitHub OIDC, Bundle als Release-Asset
4. **SHA256SUMS** — Checksummen-Datei für schnelle Integritätsprüfung
5. **GitHub Release** — alle Artefakte + Bundles + SHA256SUMS veröffentlicht

Siehe [docs/supply-chain.md](supply-chain.md) für die vollständige Verifikationsanleitung.

## Fehlerbehandlung

- Pro Endpunkt: max. 3 Retries mit Exponential Backoff (2s, 4s, 8s)
- HTTP 429 und 5xx: Retry mit Backoff; HTTP 4xx (außer 429): kein Retry
- Fehlgeschlagener Endpunkt bricht den Lauf **nicht** ab
- Fehlschläge werden im Manifest als `"status": "failed"` markiert
- Logging via `slog` (strukturiertes Logging, kein separates Log-File)
- Response-Body begrenzt auf 100 MiB (Schutz vor Memory-Exhaustion)

## Output-Struktur

```
backup/
  2026-03-06T02-00-00/
    circles.json
    roles.json
    members.json
    policies.json
    meetings.json
    okrs.json
    node-okrs.json
    tasks.json
    boards.json
    columns.json
    checklists.json
    metrics.json
    publications.json
    categories.json
    tensions.json
    attachments.json
    chartviews.json
    calendars.json
    backups.json
    backup-manifest.json
    backup-manifest.sig
```

## backup-manifest.json Format

```json
{
  "timestamp": "2026-03-06T02:00:00Z",
  "tool_version": "1.0.0",
  "organization_id": "org_xxx",
  "files": [
    {
      "name": "circles.json",
      "sha256": "abc123...",
      "records": 42,
      "status": "ok"
    }
  ],
  "summary": {
    "total_files": 21,
    "successful": 21,
    "failed": 0
  }
}
```

Die zugehörige `backup-manifest.sig` enthält die HMAC-SHA-256-Signatur des Manifests (hex-encoded).
