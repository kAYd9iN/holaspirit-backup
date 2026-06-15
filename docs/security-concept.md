# Sicherheitskonzept

## Prinzipien

- **Least Privilege:** Read-Only API Token — kein Schreibzugriff auf Holaspirit
- **GET-only HTTP-Client:** Der interne HTTP-Client exponiert ausschließlich eine `Get()`-Methode; POST/PATCH/DELETE sind strukturell nicht aufrufbar
- **No Secrets on Disk:** Token wird ausschließlich im Windows Credential Manager gespeichert
- **Token-Leak Prevention:** Logging via `slog` — Token wird nie geloggt oder in Fehlermeldungen exponiert; kein `Token()`-Accessor nach außen
- **Supply Chain Security:** Alle Go-Abhängigkeiten via `go mod vendor` eingecheckt; alle CI-Actions auf Commit-SHA gepinnt
- **SAST im CI:** `gosec` und `govulncheck` bei jedem Push (pinned versions)
- **Path Traversal Prevention:** Dateinamen werden vor dem Schreiben sanitiert (`[^a-zA-Z0-9_-]` → `_`) und per `filepath.Rel` gegen das Zielverzeichnis geprüft (beide Methoden: WriteJSON + WriteFile)
- **Symlink-Schutz:** Das `--output`-Verzeichnis wird via `EvalSymlinks` aufgelöst und nach dem Anlegen erneut auf Containment geprüft (kein Schreiben durch einen Symlink hindurch)
- **Input Validation:** OrgID wird per Regexp validiert bevor sie in URLs injiziert wird
- **Log-Injection-Schutz:** API-/Operator-Werte werden vor dem Logging von Steuerzeichen/ANSI-Sequenzen bereinigt
- **Fehler-Sanitisierung:** Im Manifest gespeicherte Fehlerstrings werden bereinigt und gekürzt (kein Roh-API-Fehler auf Disk)
- **TLS-Policy:** Mindestens TLS 1.2 explizit erzwungen, `InsecureSkipVerify` hart auf `false`
- **Dateiberechtigungen:** Backup-Dateien `0600`, Verzeichnisse `0750`
- **Response-Limit:** HTTP-Responses auf 100 MiB begrenzt (Schutz vor Memory-Exhaustion)
- **Pagination-Guard:** Max. 500 Seiten **und** max. 1 Mio. Items pro Endpunkt (Schutz vor endlosen Loops und Memory-Exhaustion)
- **SLSA Level 2:** Jedes Release-Binary erhält eine signierte Provenance-Attestation
- **cosign Keyless Signing:** Release-Binaries werden via Sigstore OIDC signiert
- **OpenSSF Scorecard:** Wöchentliches automatisches Security-Scoring des Repos
- **Dependency Review:** PRs werden auf neue hochschwere CVEs **und** auf nicht-permissive Lizenzen geprüft (Allowlist: MIT/Apache-2.0/BSD/ISC)
- **Release-Security-Gate:** Ein Release wird blockiert, solange `security`-Issues offen sind, govulncheck/gosec/Race-Tests fehlschlagen oder der CBOM nicht NIST-konform ist
- **CBOM + NIST-Policy:** Krypto-Oberfläche als CycloneDX-1.6-CBOM, gegen NIST SP 800-131A via OPA/conftest geprüft (siehe unten)

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
| `release.yml` | `v*` Tags | Security-Gate → SLSA L2 Attestation + cosign Signing + SHA256 |
| `cbom.yml` | Push, PR | Dependency-SBoM + CBOM-Validierung + conftest-NIST-Check (informativ) |
| `scorecard.yml` | Push main, wöchentlich | OpenSSF Scorecard Security-Scoring |
| `dependency-review.yml` | PRs, merge_group | Neue Abhängigkeiten auf CVEs + Lizenz-Allowlist prüfen |
| `dependabot-auto-merge.yml` | Dependabot-PR | Auto-Merge reifer Minor/Patch-Bumps nach grüner CI |
| `api-update-check.yml` | täglich 06:00 UTC | Spec-basierte Drift-Erkennung → Claude-Anpassung → api-drift-PR |
| `auto-release.yml` | api-drift-PR gemerged | 0ver-Minor-Bump + Tag → triggert Release |

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

## SBoM und CBOM

Zwei getrennte Stücklisten:

**Dependency-SBoM** (`sbom.cdx.json`) — via [CycloneDX cdxgen](https://github.com/CycloneDX/cdxgen)
`--type go`: alle Go-Modul-Abhängigkeiten mit PURLs, Hashes und Lizenzen. Das ist
**kein** CBOM — Go-Stdlib-Krypto ist keine go.mod-Abhängigkeit und taucht hier nicht auf.

**CBOM** (`docs/cbom.cdx.json`) — die tatsächliche Krypto-Oberfläche als CycloneDX-1.6
mit `cryptographic-asset`-Komponenten. Da automatisierte Scanner (cdxgen, cbomkit-theia)
Go-Stdlib-Krypto nicht erkennen, wird der CBOM **hand-gepflegt** und durch
`scripts/check-cbom.sh` ehrlich gehalten (CI schlägt fehl, wenn ein Krypto-Import in
`internal/`/`cmd/` nicht im CBOM deklariert ist).

Erfasste Assets: **SHA-256** (Hashing), **HMAC-SHA-256** (Manifest-Signatur),
**TLS ≥ 1.2** (Transport), **ECDSA P-256** (cosign Release-Signing).

**NIST-Compliance-Gate:** Der CBOM wird gegen eine NIST-SP-800-131A-Policy
([`policy/nist-crypto.rego`](../policy/nist-crypto.rego), OPA/conftest) geprüft. Ein
nicht-zugelassener Algorithmus (z.B. MD5, SHA-1, RC4 oder TLS < 1.2) lässt das
**Release-Security-Gate fehlschlagen**. Quantensicherheit wird als nicht-blockierende
Warnung ausgewiesen: ECDSA P-256 ist NIST-konform, aber nicht quantensicher
(für spätere PQC-Migration vermerkt).

- **Aufbewahrung:** GitHub Actions Artifact `bom-cyclonedx` (SBoM + CBOM, 365 Tage)
- **Optionale Signierung:** mit Secret `CBOM_SIGNING_KEY_PEM` werden beide BoMs via OpenSSL SHA256 signiert

## Netzwerk

- Ausgehende HTTPS-Verbindung zu `app.holaspirit.com` (Port 443)
- Keine eingehenden Verbindungen nötig
- HTTP-Timeouts: 30s pro API-Request, 2h gesamt (konfigurierbar via `--timeout`)

## Security-Reviews

### Erster Pentest (#1–#8)

Eine erste interne Sicherheitsprüfung (OWASP Top 10 + toolspezifische Analyse) wurde durchgeführt.  
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

### Umfassendes Security-Review (#9–#33)

Ein zweites, umfassenderes Review (Personas: Security Architect/Engineer, Pentester, SSDM)
brachte 25 weitere Findings (#9–#33, davon 5× `severity:high`). **Alle behoben** (PR #42);
das Release-Security-Gate gibt seither frei (Release v0.2.0 erstellt). Auflösungen:

- **Code-Fixes:** TLS-Policy (#12/#29), Hex-decodierter HMAC-Vergleich (#13),
  `LimitReader` beim Manifest-Hashing (#14), Fehler-Sanitisierung (#15),
  Symlink-Auflösung (#17), Log-Injection-Schutz (#18), Item-Cap (#19),
  `verify`-Pfad-Auflösung (#21), URL-Encoding (#30), Opt-in-Audit-Trail (#33)
- **Workflow:** Least-Privilege-Permissions (#25), Lizenz-Allowlist (#24), Branch-Ruleset (#26)
- **Dokumentierte Design-Entscheidungen** (SECURITY.md, [trust-model.md](trust-model.md)):
  Verschlüsselung-at-Rest (#9), Token-Rotation/HMAC (#10), Retention (#11),
  Incident-Response (#23), Trust-Model (#27), OrgID-Disclosure (#28)
- **Durch credential-freien Drift-Check obsolet:** Token im Workflow (#22), Shell-Injection (#20)
