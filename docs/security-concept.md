# Sicherheitskonzept

## Prinzipien

- **Least Privilege:** Read-Only API Token — kein Schreibzugriff auf Holaspirit
- **GET-only HTTP-Client:** Der interne HTTP-Client exponiert ausschließlich eine `Get()`-Methode; POST/PATCH/DELETE sind strukturell nicht aufrufbar
- **No Secrets on Disk:** Token wird ausschließlich im Windows Credential Manager gespeichert
- **Token-Leak Prevention:** Logging via `slog` — Token wird nie geloggt oder in Fehlermeldungen exponiert; kein `Token()`-Accessor nach außen
- **Supply Chain Security:** Alle Go-Abhängigkeiten via `go mod vendor` eingecheckt; alle CI-Actions auf Commit-SHA gepinnt
- **SAST im CI:** `gosec` und `govulncheck` bei jedem Push (pinned versions)
- **Path Traversal Prevention:** Dateinamen werden vor dem Schreiben sanitiert (`[^a-zA-Z0-9_-]` → `_`) und per `filepath.Rel` gegen das Zielverzeichnis geprüft (beide Methoden: WriteJSON + WriteFile)
- **Input Validation:** OrgID wird per Regexp validiert bevor sie in URLs injiziert wird
- **Dateiberechtigungen:** Backup-Dateien `0600`, Verzeichnisse `0750`
- **Response-Limit:** HTTP-Responses auf 100 MiB begrenzt (Schutz vor Memory-Exhaustion)
- **Pagination-Guard:** Max. 500 Seiten pro Endpunkt (Schutz vor endlosen Loops)
- **SLSA Level 2:** Jedes Release-Binary erhält eine signierte Provenance-Attestation
- **cosign Keyless Signing:** Release-Binaries werden via Sigstore OIDC signiert
- **OpenSSF Scorecard:** Wöchentliches automatisches Security-Scoring des Repos
- **Dependency Review:** PRs werden auf neue hochschwere CVEs geprüft (CVSS ≥ 7.0)

## Secrets Management: Windows Credential Manager

Der API-Token wird **nie** in einer Datei, Umgebungsvariable oder Konfigurationsdatei gespeichert.  
Er wird einmalig im Windows Credential Manager hinterlegt und dort durch DPAPI (Data Protection API)  
an den Windows-Benutzer gebunden geschützt.

### Einmalige Einrichtung (PowerShell als Admin)

```powershell
cmdkey /generic:holaspirit-backup /user:api /pass:api:DEIN_TOKEN_HIER
```

### Vergleich: .env-Datei vs. Windows Credential Manager

| Kriterium | .env-Datei | Windows Credential Manager |
|-----------|------------|----------------------------|
| Klartext auf Disk | Ja (Risiko) | Nein |
| OS-Schutz | Nein | Ja (DPAPI) |
| Backup enthält Token | Ja (Risiko) | Nein |
| Audit-Trail | Nein | Ja |
| Cronjob-tauglich | Ja | Ja |

### Linux-Betrieb

Auf Linux wird `HOLASPIRIT_TOKEN` als Umgebungsvariable erwartet.  
Der Wert wird automatisch von führenden/abschließenden Leerzeichen bereinigt (TrimSpace),  
sodass Tokens aus Dateien mit Zeilenumbruch (`TOKEN\n`) korrekt funktionieren.

## API-Token Verwaltung

- Token-Typ: Read-Only (Admin > API > Token erstellen)
- Token beginnt mit `api:`
- Läuft nie ab (kein Rotation-Zwang, aber empfohlen: jährlich rotieren)
- Minimale Berechtigungen: nur Lesezugriff

## GET-only HTTP-Client

Der API-Client hat bewusst nur eine einzige öffentliche Methode:

```go
func (c *Client) Get(ctx context.Context, path string) ([]byte, error)
```

POST, PATCH, DELETE sind nicht implementiert. Dadurch ist es strukturell ausgeschlossen,  
dass ein Bug oder eine fehlerhafte Erweiterung Holaspirit-Daten verändert.

**Rate-Limiter:** 250 Requests / 5 Minuten (Token Bucket, Burst 20).  
**Retry:** Nur bei HTTP 429 und 5xx (Exponential Backoff: 2s, 4s, 8s).  
**Timeout:** 30s pro Request, 2h gesamt (konfigurierbar via `--timeout`).

## CI/CD Security

### GitHub Actions Workflow-Struktur

| Workflow | Trigger | Zweck |
|----------|---------|-------|
| `security.yml` | Push, PR | govulncheck + gosec + Tests (race detector) |
| `build.yml` | Push main, Tags, PR | Matrix-Build (linux/amd64, linux/arm64, windows/amd64) |
| `release.yml` | `v*` Tags | GitHub Release + SLSA L2 Attestation + cosign Signing + SHA256 |
| `cbom.yml` | Push, PR | CycloneDX CBOM generieren + optional signieren |
| `commit-signature.yml` | Push main, PR | GPG-Signatur aller Commits prüfen |
| `scorecard.yml` | Push main, wöchentlich | OpenSSF Scorecard Security-Scoring |
| `dependency-review.yml` | PRs auf main | Neue Abhängigkeiten auf CVEs prüfen |

Alle `actions/*`-Referenzen sind auf Commit-SHA gepinnt (Schutz gegen Tag-Manipulation und Typosquatting).

### Security-Tools

| Tool | Zweck |
|------|-------|
| `govulncheck` | Bekannte CVEs in Go-Abhängigkeiten prüfen |
| `gosec` | SAST: unsichere Code-Muster erkennen |
| `go mod verify` | Integrität der vendored Dependencies prüfen |
| OpenSSF Scorecard | Automatisches Security-Scoring |
| Dependency Review | PRs blockieren wenn neue Abhängigkeiten CVEs (CVSS ≥ 7.0) enthalten |

## Integritätsprüfung der Backups

### Manifest-Signatur (HMAC-SHA-256)

Jede Backup-Session erzeugt:

- `backup-manifest.json` mit SHA256-Hash pro Datei, Timestamp, Org-ID und Record-Anzahl
- `backup-manifest.sig` — HMAC-SHA-256-Signatur des Manifests (domain-separated Key, hex-encoded)

**Verifikation via CLI:**

```powershell
# Windows
backup.exe verify --dir C:\Backups\holaspirit\2026-03-06T02-00-00

# Linux
holaspirit-backup verify --dir /mnt/backups/holaspirit/2026-03-06T02-00-00

# Exit 0 = Manifest und Signaturen OK
# Exit 1 = Manipulation oder falscher Token erkannt
```

## CBOM (Cryptography Bill of Materials)

Bei jedem CI-Lauf wird via [CycloneDX cdxgen](https://github.com/CycloneDX/cdxgen) ein  
Cryptography Bill of Materials generiert. Es dokumentiert alle kryptografischen Primitive  
und Bibliotheken im Projekt.

- **Format:** CycloneDX JSON (`cbom.cdx.json`)
- **Speicherung:** GitHub Actions Artifact `cbom-cyclonedx` (365 Tage Aufbewahrung)
- **Optionale Signierung:** Wenn Secret `CBOM_SIGNING_KEY_PEM` konfiguriert ist, wird das CBOM mit OpenSSL SHA256 signiert (`cbom.cdx.json.sig`)

## Netzwerk

- Ausgehende HTTPS-Verbindung zu `app.holaspirit.com` (Port 443)
- Keine eingehenden Verbindungen nötig
- HTTP-Timeouts: 30s pro API-Request, 2h gesamt (konfigurierbar via `--timeout`)

## Pentest-Ergebnisse

Eine vollständige interne Sicherheitsprüfung (OWASP Top 10 + toolspezifische Analyse) wurde durchgeführt.  
Alle Findings sind als GitHub Issues erfasst (#1–#8) und behoben:

| ID | Schwere | Finding | Status |
|----|---------|---------|--------|
| SEC-01 | Medium | Unbounded pagination loop | Behoben: `maxPages=500` |
| SEC-02 | Medium | `io.ReadAll` ohne `LimitReader` | Behoben: 100 MiB Limit |
| SEC-03 | Medium | `govulncheck`/`gosec` `@latest` | Behoben: versionsgepinnt |
| SEC-04 | Low | String-Concat statt `filepath.Join` | Behoben |
| SEC-05 | Low | `WriteFile` ohne `filepath.Rel`-Check | Behoben |
| SEC-06 | Low | `orgID` ohne Validierung in URL | Behoben: `ValidateOrgID()` |
| SEC-07 | Low | Token ohne `TrimSpace` | Behoben |
| SEC-08 | Low | `codeql-action` nicht SHA-gepinnt | Behoben |
