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
│   │   ├── endpoints.go         # alle 47 Organisations-Endpunkte + ValidateOrgID
│   │   └── pagination.go        # FetchAllPages: Paginierung, linked, Vollständigkeit
│   ├── backup/
│   │   ├── runner.go            # Worker Pool (5 bounded Goroutinen)
│   │   └── manifest.go          # SHA256-Hashes + HMAC-SHA-256-Signatur
│   ├── credentials/
│   │   ├── credentials.go       # Interface + Mock
│   │   └── wincred.go           # Windows Credential Manager (build tag windows)
│   └── storage/
│       └── writer.go            # Dateien schreiben, Pfad-Sanitizing + Symlink-Auflösung, 0600/0750
├── scripts/
│   ├── check-api-schema.sh      # credential-freier API-Drift-Check (publizierte Spec)
│   └── check-cbom.sh            # CBOM-Konsistenz (Krypto-Import muss im CBOM stehen)
├── policy/
│   └── nist-crypto.rego         # OPA/conftest NIST-Krypto-Policy (gatet Release)
├── vendor/                      # eingecheckte Abhängigkeiten
├── docs/
│   ├── architecture.md          # diese Datei
│   ├── security-concept.md      # Sicherheitskonzept
│   ├── trust-model.md           # Vertrauensgrenzen
│   ├── operations.md            # Betrieb & Installation
│   ├── supply-chain.md          # SLSA, cosign, Scorecard, CBOM
│   ├── api-snapshot.json        # Drift-Check-Baseline
│   ├── cbom.cdx.json            # CycloneDX 1.6 Cryptography BoM
│   └── plans/                   # Implementierungspläne
├── go.mod
├── go.sum
├── SECURITY.md                  # GitHub Security Policy
├── .github/
│   ├── dependabot.yml           # täglich, 7-Tage-Cooldown
│   └── workflows/
│       ├── security.yml         # govulncheck + gosec + Tests
│       ├── build.yml            # Matrix-Build (3 Plattformen)
│       ├── release.yml          # Security-Gate → SLSA L2 + L3 + cosign + SHA256
│       ├── cbom.yml             # Dependency-SBoM + CBOM + NIST-conftest
│       ├── scorecard.yml        # OpenSSF Scorecard (wöchentlich)
│       ├── dependency-review.yml # CVE- + Lizenz-Prüfung bei PRs
│       ├── dependabot-auto-merge.yml # Auto-Merge reifer Non-Major-Bumps
│       ├── api-update-check.yml # täglicher Spec-Drift-Check → Claude → PR
│       └── auto-release.yml     # api-drift-Merge → Tag → Release
└── (Self-Update-Schlaufe: siehe CLAUDE.md)
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
       ├─ worker: ... (alle 47 Endpunkte, je nach Verfügbarkeit)
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
5. Worker Pool starten (5 Goroutinen, alle 47 Endpunkte)
6. Paginierung: alle Seiten pro Endpunkt zu `<name>.json` zusammenführen, die mitgelieferten
   verknüpften Objekte zu `<name>-linked.json` (Berechtigungen: `0600`)
7. Manifest schreiben: SHA256 pro Datei + HMAC-SHA-256-Signatur + Timestamp + Status
8. Exit 0 (alles vollständig) oder Exit 1 (mindestens ein Endpunkt `failed` oder `incomplete`)

## Was gesichert wird

Alle Organisations-Sammlungen der veröffentlichten Holaspirit-API, die Organisationsdaten
enthalten (Liste in `internal/api/endpoints.go`):

- **Struktur:** Organisation, Kreise, Rollen, Mitglieder, Richtlinien, Hierarchie, Rollen-Beziehungen, Diagramme
- **Arbeit:** Tensions, Meetings (inkl. Wiederholungen und Vorlagen), OKRs, Tasks, Boards, Spalten,
  Checklisten, Metriken, Kalender, Zeitaufwand
- **Inhalte:** Publikationen inkl. Revisionen und Kommentar-Threads, Kategorien, Anhänge (Metadaten),
  Highlights, Labels, Custom Fields, Zeiträume, Vorlagen, Übersetzungen, Chart-Ansichten
- **Sichten:** die `node-*`-Baumansichten
- **Konfiguration (Admin-Token nötig):** Webhooks, SAML-Konfiguration
- **`*-linked.json`:** Holaspirit liefert verknüpfte Datensätze in einem `linked`-Block statt im
  Datensatz selbst. Bei Rollen stehen z. B. die Texte der Verantwortlichkeiten, Domains und
  Richtlinien nur dort; `roles.json` enthält davon nur die IDs.

**Bewusst nicht gesichert:**

| Endpunkt | Grund |
|----------|-------|
| `tokens` | API-Geheimnisse |
| `stripe/current` | Abrechnung, keine Organisationsdaten |
| `favorites` | persönliche Daten des Token-Inhabers |
| `search-global`, `search/mention`, `migration/domain-availability` | Abfragen, keine gespeicherten Daten |
| `jobs`, `record` | flüchtiger Job-/Aufzeichnungs-Status |
| `domains` | braucht einen Rollen-Parameter; die Inhalte stehen in `roles-linked.json` |
| PDF-, öffentliche und Statistik-Endpunkte | abgeleitete Darstellungen bzw. Duplikate der gesicherten Daten |
| Verläufe pro Objekt (`/{id}/feed`), Dateiinhalte der Anhänge | nicht im Umfang (Datensicherung für die Migration) |

## Paginierung

Angefordert wird mit `page` und `count` (Seitengrösse 100), wie in der veröffentlichten Spec
dokumentiert. Das Ende einer Liste ergibt sich, in dieser Reihenfolge, aus `pagination.pagesCount`,
aus `pagination.nextPage` oder, wenn die Antwort keinerlei Paginierungs-Metadaten hat, aus der
ersten leeren Seite. Das ältere Format `meta.pagination.total_pages` wird weiterhin verstanden.

Schutzmechanismen:

- **Wiederholte Seite:** liefert die API trotz gemeldeter weiterer Seiten nur bereits bekannte
  Einträge (der `page`-Parameter wird ignoriert), schlägt der Endpunkt fehl, statt eine Liste
  mit Duplikaten zu speichern.
- **Vollständigkeit:** meldet die API in `pagination.totalItems` mehr Einträge als geliefert
  wurden, werden die Daten geschrieben, aber im Manifest als `incomplete` markiert (Exit-Code 1).
- **API-Obergrenze 10'000:** Holaspirit liefert pro Abfrage höchstens 10'000 Einträge und meldet
  auch `totalItems` höchstens als 10'000 (im Live-Lauf bei `tensions`/`node-tensions` gesehen).
  `tensions` und `node-tensions` werden deshalb in **Zeitfenstern** über `createdAfter`/`createdBefore`
  geholt: Das Tool prüft zuerst, in welchem Datumsformat der (undokumentierte) Filter wirklich greift,
  halbiert den Zeitraum, bis jedes Fenster unter 10'000 liegt, entfernt Doppelte über die ID und
  prüft, dass jedes Ergebnis im angefragten Fenster liegt. Lässt sich das nicht nachweisen, oder
  erreicht ein anderer Endpunkt genau 10'000, wird er als `incomplete` markiert — nie still als vollständig.
- **Optionale Endpunkte:** `translations`, `node-translations`, `chartviews` und
  `authentication-saml` hängen von Funktionen der Organisation ab. HTTP 400/403/404 markiert sie als
  `unavailable` (kein Fehler, Exit-Code 0). Lehnt eine Liste die Paging-Parameter ab (HTTP 400),
  wird sie einmal ohne Paging abgerufen.
- **Obergrenzen:** max. 500 Seiten und 1 Mio. Einträge pro Endpunkt.

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
   **SLSA L3 Build** — parallel baut der slsa-github-generator `backup-linux-amd64-slsa3` isoliert, mit signierter `.intoto.jsonl`-Provenance
3. **cosign Signing** — Keyless-Signatur via GitHub OIDC, Bundle als Release-Asset
4. **SHA256SUMS** — Checksummen-Datei für schnelle Integritätsprüfung
5. **GitHub Release** — alle Artefakte + Bundles + SHA256SUMS veröffentlicht

Siehe [docs/supply-chain.md](supply-chain.md) für die vollständige Verifikationsanleitung.

## Fehlerbehandlung

- Pro Endpunkt: max. 3 Retries mit Exponential Backoff (2s, 4s, 8s)
- HTTP 429 und 5xx: Retry mit Backoff; HTTP 4xx (außer 429): kein Retry
- Fehlgeschlagener Endpunkt bricht den Lauf **nicht** ab
- Fehlschläge werden im Manifest als `"status": "failed"` markiert, unvollständige Listen als
  `"status": "incomplete"` (Datei geschrieben und signiert, aber weniger Einträge als gemeldet)
- Logging via `slog` (strukturiertes Logging, kein separates Log-File)
- Response-Body begrenzt auf 100 MiB (Schutz vor Memory-Exhaustion)

## Output-Struktur

```
backup/
  2026-03-06T02-00-00/
    organization.json
    circles.json
    circles-linked.json        # mitgelieferte verknüpfte Objekte, sofern vorhanden
    roles.json
    roles-linked.json          # u. a. Verantwortlichkeiten, Domains, Richtlinien
    members.json
    ...                        # eine Datei pro Endpunkt (47), je nach Antwort plus -linked
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
      "status": "ok"
    },
    {
      "name": "roles.json",
      "sha256": "def456...",
      "status": "incomplete",
      "error": "retrieved 100 of 140 items the API reported (40 missing)"
    }
  ],
  "summary": {
    "total_files": 2,
    "successful": 1,
    "incomplete": 1,
    "failed": 0
  }
}
```

Die zugehörige `backup-manifest.sig` enthält die HMAC-SHA-256-Signatur des Manifests (hex-encoded).
