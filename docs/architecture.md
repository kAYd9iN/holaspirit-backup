# Architektur

## Projektstruktur

```
holaspirit-backup/
├── cmd/
│   └── backup/
│       ├── main.go              # Einstiegspunkt, CLI-Flags
│       ├── verify.go            # verify-Subcommand (Manifest-Signatur prüfen)
│       ├── compare.go           # compare-Subcommand (Holaspirit ↔ Talkspirit)
│       ├── token.go             # Schlüssel aus TALKSPIRIT_API_KEY
│       ├── token_windows.go     # Fallback: Windows Credential Manager
│       └── token_other.go       # Fallback für Nicht-Windows (keiner)
├── internal/
│   ├── api/
│   │   ├── client.go            # HTTP-Client (GET-only), Rate-Limit-Header, Retry-After
│   │   ├── endpoints.go         # alle Partner-API-Routen + Unter-Ressourcen + whoami
│   │   └── pagination.go        # FetchAll: Cursor-Paginierung, Schleifen-/Cap-Erkennung
│   ├── backup/
│   │   ├── runner.go            # Worker Pool (3 bounded Goroutinen) + Unter-Ressourcen
│   │   └── manifest.go          # SHA256-Hashes + HMAC-SHA-256-Signatur
│   ├── compare/                 # Abgleich Holaspirit-Backup ↔ Talkspirit-Backup, Bericht
│   ├── credentials/
│   │   ├── credentials.go       # Interface + Mock
│   │   └── wincred.go           # Windows Credential Manager (build tag windows)
│   └── storage/
│       └── writer.go            # Dateien schreiben, Pfad-Sanitizing + Symlink-Auflösung, 0600/0750
├── scripts/
│   ├── check-api-schema.sh      # credential-freier API-Drift-Check (/v1/openapi.json)
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

Die Endpunkte werden über einen **bounded Pool von 3 Worker-Goroutinen** abgefragt.
Das Kontingent der Partner API gilt **pro Schlüssel** (Standard ca. 100 Anfragen pro
gleitender Minute, nicht vertraglich zugesichert) und wird von allen Workern geteilt — mehr
Worker machen das Backup nicht schneller, drei verdecken nur die Latenz.

- Ein zentraler Token-Bucket-Limiter taktet auf eine Anfrage pro 650 ms (Burst 3).
- Sinkt `X-RateLimit-Remaining` unter 3, wartet der Client bis `X-RateLimit-Reset`.
- HTTP 429 wird bis zu 10-mal wiederholt und respektiert `Retry-After` (eine abgewiesene
  Anfrage zählt nicht gegen das Kontingent und wurde nicht ausgeführt). 5xx/Netzwerkfehler:
  3 Wiederholungen mit exponentiellem Backoff. Wartezeiten sind auf 2 Minuten begrenzt.
- Redirects werden nie verfolgt (der Bearer-Token darf keinen anderen Host erreichen).

```
main.go  ── GET /v1/whoami (Schlüssel, Tenant, Scopes prüfen)
  └─ runner.go (Worker Pool: 3 Goroutinen)
       ├─ worker: circles ──► circle-members (pro Kreis)
       ├─ worker: meetings ──► meeting-agenda-items (pro Meeting)
       ├─ worker: tasks ──► subtasks (rekursiv) + task-comments (pro Aufgabe)
       ├─ worker: ... (alle Endpunkte aus internal/api/endpoints.go)
       │       ↕ koordiniert über Rate-Limiter + bounded channel
       └─ manifest.go (nach Abschluss aller Worker)
```

## Backup-Ablauf

1. API-Schlüssel aus `TALKSPIRIT_API_KEY` laden, sonst aus dem Windows Credential Manager
   (`talkspirit-backup`)
2. `GET /v1/whoami`: Schlüssel gültig? Tenant-ID (validiert, ins Manifest) und fehlende
   Lese-Scopes melden
3. Output-Verzeichnis anlegen: `backup/YYYY-MM-DDTHH-MM-SS/` (Berechtigungen: `0750`)
4. Worker Pool starten; jede Liste per Cursor vollständig lesen → `<name>.json`
5. Unter-Ressourcen pro Eintrag lesen → `<unterressource>.json` als
   `[{"parent_id": "…", "data": [...]}]` (Berechtigungen: `0600`)
6. Manifest schreiben: SHA256 pro Datei + HMAC-SHA-256-Signatur + Timestamp + Status
7. Exit 0 (alles vollständig) oder Exit 1 (mindestens ein Endpunkt `failed` oder `incomplete`)

## Was gesichert wird

Alle Lese-Routen des veröffentlichten Partner-API-Vertrags (`/v1/openapi.json`), die
Organisationsdaten enthalten (Liste in `internal/api/endpoints.go`):

| Datei | Route | Unter-Ressourcen |
|-------|-------|------------------|
| `whoami.json` | `/v1/whoami` | |
| `custom-fields-{circle,role,user}.json` | `/v1/custom-fields?entity_type=…` | |
| `circles.json` | `/v1/circles?include=custom_fields` | `circle-members` |
| `roles.json` | `/v1/roles?include=custom_fields,members` | |
| `role-templates.json` | `/v1/role-templates` | |
| `users.json` | `/v1/users?include=custom_fields` | |
| `memberships.json` | `/v1/memberships` | |
| `tensions.json` | `/v1/tensions?include=links` | |
| `meetings.json` | `/v1/meetings?status=` alle (sonst fehlen abgesagte) | `meeting-agenda-items`¹ |
| `tasks.json` | `/v1/tasks?archived_status=all&include=…` | `subtasks` (rekursiv), `task-comments`¹ |
| `projects.json` | `/v1/projects?archived_status=all` | `sections`, `labels`, `project-comments`¹ |
| `goals.json` | `/v1/goals?archived_status=all` | `goal-key-results`, `goal-comments`¹ |
| `time-periods.json` | `/v1/time-periods` | |
| `documents.json` | `/v1/documents?archived_status=all` | `document-content`, `document-comments`¹ |
| `audit-logs.json` | `/v1/audit-logs` (Admin, optional) | |

¹ eine Anfrage pro Eintrag; `--quick` lässt sie weg.

**Bewusst nicht gesichert:**

| Route | Grund |
|-------|-------|
| `/v1/structure-exports` | Start braucht POST — der Client ist absichtlich GET-only; Inhalt = Kreise/Rollen/Mitgliedschaften |
| `/v1/*/{id}` (Detail) | gleiche Felder wie die Listen |
| `/v1/roles/{id}/members`, `/v1/users/{id}/memberships` | bereits in `roles.json` (`include=members`) bzw. `memberships.json` |
| `/healthz` | Diagnose |

**Grenzen der API:** Der Schlüssel sieht nur, was sein technischer Benutzer sehen darf
(Ausnahme: Spannungen sind mandantenweit). Gesperrte/anonymisierte Personen liefert
`/v1/users` nicht. Checklisten, Metriken, Publikationen, Kalender und Webhooks haben in der
Partner API kein Gegenstück.

## Paginierung

`{"data": [...], "pagination": {"next_cursor": "…", "has_more": true}}` — das Tool folgt
`next_cursor`, bis `has_more` falsch ist. Seitengrösse: 199 (Maximum der meisten Routen),
`users` 100, `meetings` 50.

Schutzmechanismen:

- **Cursor-Schleife:** ein bereits gesehener Cursor, oder eine Seite, die nur bekannte Einträge
  wiederholt, bricht den Endpunkt mit Fehler ab, statt Duplikate zu speichern.
- **`has_more` ohne Cursor:** Fehler.
- **10'000er-Grenze:** sortierte Listen brechen bei 10'000 Zeilen mit `400 INVALID_CURSOR` ab.
  Das Gelesene wird geschrieben, der Endpunkt aber als `incomplete` markiert (Exit-Code 1).
- **Unter-Ressourcen:** schlägt eine Anfrage für einen einzelnen Eintrag fehl, wird der Rest
  trotzdem gesichert und der Endpunkt als `incomplete` markiert.
- **Optionale Endpunkte:** `audit-logs` (Admin-Recht + Scope) wird bei 401/403/404 als
  `unavailable` geführt. Fehlt einem anderen Endpunkt der Scope (`403 FORBIDDEN_SCOPE`),
  schlägt er fehl und die Meldung nennt den fehlenden Scope.
- **Fehlercodes:** aus RFC-7807-Fehlern wird nur der `code` (Format `[A-Z][A-Z0-9_]*`)
  übernommen, nie der restliche Antworttext.
- **Obergrenzen:** max. 5'000 Seiten und 1 Mio. Einträge pro Liste, Unteraufgaben max. 10 Ebenen.

## Vergleich Holaspirit ↔ Talkspirit

```
backup compare --holaspirit <dir> --talkspirit <dir> [--migrated-at …] [--out <dir>]
```

Liest ein Holaspirit-Backup von v0.5.0 und ein Talkspirit-Backup und schreibt
`vergleich.xlsx` (`0600`, enthält Namen/E-Mails). Keine Netzwerkzugriffe. Die Arbeitsmappe
schreibt `internal/xlsx` ohne Fremdbibliothek (Text immer als Inline-String, nie als Formel).

**Aufbau der Arbeitsmappe** (von oben nach unten nach Wichtigkeit): Übersicht pro Bereich mit
Priorität Hoch (Personen, Kreise, Rollen, Besetzungen, Richtlinien), Mittel (Felder, Ziele,
Zeiträume, Projekte, Spalten, Labels, Aufgaben), Niedrig (Meetings, Traktanden/Notizen) →
Arbeitsliste (Verdachtsfälle, Status-Auswahl, Verantwortlich, Kommentar) → Mängelliste Talkspirit
(gleiche Nummern, Soll/Ist) → ein Blatt pro Bereich (alle Befunde) → Nicht prüfbar → Audit-Log.

**Zuordnung.** Die Partner API kennt die Holaspirit-IDs nicht. Zugeordnet wird deshalb in
mehreren Durchgängen, vom strengsten Merkmal zum lockersten; was in einem Durchgang ein
Gegenstück findet, ist für die weiteren vergeben. Gleiche Merkmale werden als Multimenge
gepaart, damit Anzahlen auch bei Mehrdeutigkeit stimmen.

| Bereich | Merkmale (in Reihenfolge) |
|---------|---------------------------|
| Personen | E-Mail |
| Kreise | Namenspfad, dann Name (→ `VERSCHOBEN`) |
| Rollen | Kreispfad + Name, dann Name (→ `VERSCHOBEN`); Kreis-Rollen zählen zu den Kreisen |
| Inhalte von Kreisen/Rollen | Purpose, Accountabilities, Domänen als Textmengen; HTML/Markdown/Leerraum zählen nicht, ≥ 90 % Wortübereinstimmung gilt als gleich; bei eingeladenen Rollen die Texte der Ursprungsrolle |
| Rollenbesetzungen | E-Mail der Personen je Rolle; abgelaufene Holaspirit-Zuweisungen zählen nicht |
| Richtlinien | Titel ↔ Talkspirit-Dokumente |
| Ziele / Key Results | Titel |
| Meetings | Titel + Minute, Kreis + Minute, Titel + Tag, Kreis + Tag, Minute, Titel; Meetings ohne Datum („Evolution outside meeting“) über den Erstellzeitpunkt |
| Spannungen / Traktanden | Titel im zugeordneten Meeting, dann Titel + Minute/Tag gegen Spannungen und Traktanden, dann Titel |
| Aufgaben | Titel + Minute, Titel + Tag, Titel im zugeordneten Projekt, Titel |
| Projekte, Spalten, Labels | Name (Spalten/Labels innerhalb des zugeordneten Projekts) |

Zuordnungen nur über den Titel oder nur über die Uhrzeit werden als `UNSICHER_ZUGEORDNET`
ausgewiesen.

**Einordnung gegen den Migrationszeitpunkt** (`--migrated-at`, sonst geschätzt: der Tag, an dem
die meisten Talkspirit-Kreise/-Rollen/-Personen angelegt wurden):

| Befund | Bedeutung |
|--------|-----------|
| `FEHLT` | in Holaspirit, in Talkspirit nicht gefunden |
| `FEHLT_TEIL` | Objekt da, aber Accountability/Domäne/Purpose/Besetzung/Key Result fehlt, und das Objekt wurde seit der Migration nicht geändert |
| `ABWEICHUNG_UNVERAENDERT` | abweichend, Talkspirit-Objekt seit der Migration unverändert → Verdacht Migrationsfehler |
| `VERSCHOBEN` | nur unter einem anderen Kreis gefunden |
| `NUR_IN_TALKSPIRIT` | in Talkspirit vor Migrationsende angelegt, im Holaspirit-Backup nicht vorhanden |
| `ABWEICHUNG_BEARBEITET` | abweichend, Talkspirit-Objekt nach der Migration geändert → vermutlich gewollt |
| `ABWEICHUNG_HS_NACH_MIGRATION` | Holaspirit-Objekt wurde nach der Migration noch geändert → Abweichung erwartbar |
| `NEU_IN_TALKSPIRIT` | nach der Migration angelegt → legitime Arbeit |

Die ersten fünf gelten als Verdachtsfälle (Arbeitsliste/Mängelliste; Spalte „Verdacht“ in den Bereichsblättern). Verschobene Objekte, die nach dem Stichtag bearbeitet wurden, gelten als gewollt. Ein
fehlendes Objekt kann auch nach der Migration absichtlich gelöscht worden sein; der Bericht
listet dafür die Audit-Log-Ereignisse nach Migrationsende.

**Nicht prüfbar** (kein Gegenstück in der Partner API) und nur mit Anzahl gelistet:
Checklisten, Metriken, Publikationen samt Revisionen, Anhänge/Links ausserhalb von Aufgaben,
Kalender, Highlights, Meeting-Vorlagen und -Serien, Webhooks, Inhalt von Governance-Vorschlägen.

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

- 5xx/Netzwerkfehler: max. 3 Retries mit Exponential Backoff (2s, 4s, 8s)
- HTTP 429: bis zu 10 Retries nach `Retry-After`; HTTP 4xx (außer 429): kein Retry
- Fehlgeschlagener Endpunkt bricht den Lauf **nicht** ab
- Fehlschläge werden im Manifest als `"status": "failed"` markiert, unvollständige Listen als
  `"status": "incomplete"` (Datei geschrieben und signiert, aber nicht alles abrufbar)
- Logging via `slog` (strukturiertes Logging, kein separates Log-File)
- Response-Body begrenzt auf 100 MiB (Schutz vor Memory-Exhaustion)

## Output-Struktur

```
backup/
  2026-03-06T02-00-00/
    whoami.json
    circles.json
    circle-members.json        # [{"parent_id": "<Kreis>", "data": [...]}]
    roles.json                 # inkl. Purpose, Domänen, Accountabilities, Besetzung
    users.json
    meetings.json
    meeting-agenda-items.json
    ...                        # eine Datei pro Endpunkt und Unter-Ressource
    backup-manifest.json
    backup-manifest.sig
```

## backup-manifest.json Format

```json
{
  "timestamp": "2026-03-06T02:00:00Z",
  "tool_version": "1.0.0",
  "organization_id": "<tenant_id aus /v1/whoami>",
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
      "error": "/v1/tensions after 10000 items: the API refused to page further (traversal cap)"
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
