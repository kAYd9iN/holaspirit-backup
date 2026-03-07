# Holaspirit Backup — Security Hardening Design

**Datum:** 2026-03-07
**Status:** Genehmigt
**Basis:** `docs/plans/2026-03-06-holaspirit-backup-design.md`

---

## Kontext

Nach Review des bestehenden Codes (Stand 2026-03-06) wurden folgende Sicherheitsprobleme
identifiziert und deren Behebung in dieser Session entworfen und genehmigt.

---

## 1. GET-only Constraint (neu)

**Problem:** `internal/backup/exports.go` macht POST-Requests — widerspricht dem
Prinzip dass das Tool ausschliesslich lesend auf die API zugreift.

**Loesung:**
- `internal/backup/exports.go` und `internal/backup/exports_test.go` werden entfernt
- `client.Token()` und `client.BaseURL()` werden entfernt (wurden nur von exports.go genutzt)
- Der HTTP-Client exponiert ausschliesslich `Get()` — kein `Post()`, `Patch()`, `Delete()`
- Zusaetzlich: Test mit `httptest.Server` der verifiziert dass jeder eingehende Request
  `Method == GET` ist, sonst schlaegt der Test fehl

**Konsequenz fuer Async Exports:** Werden ersatzlos entfernt. Bei Bedarf spaeter als
explizites Modul mit eigenem Flag `--with-exports` und expliziter Nutzerbestaetigung.

---

## 2. HMAC-Signatur auf dem Manifest (neu)

**Problem:** Das `backup-manifest.json` enthaelt SHA256-Hashes der Backup-Dateien,
ist selbst aber nicht integritaetsgesichert und kann unbemerkt veraendert werden.

**Loesung:** HMAC-SHA-256 Signatur des Manifests.

```
Key   = HMAC-SHA-256(api-token, "holaspirit-backup-manifest")
Input = vollstaendiger JSON-Inhalt von backup-manifest.json (kanonisch, ohne Signatur)
Output = backup-manifest.sig (Hex-String)
```

Der HMAC-Key wird vom API-Token abgeleitet — kein zusaetzliches Secret noetig.

**Verifikation via Subcommand:**
```
backup.exe verify --dir C:\Backups\holaspirit\2026-03-07T02-00-00
```
- Liest `backup-manifest.json` und `backup-manifest.sig`
- Errechnet HMAC mit dem Token aus dem Windows Credential Manager
- Exit 0 = OK, Exit 1 = Manipuliert oder Token falsch

---

## 3. Path Traversal Prevention (neu)

**Problem:** Endpoint-Namen werden direkt als Dateinamen verwendet. Ein praeparierter
Endpoint-Name wie `../../etc/passwd` koennte ausserhalb des Backup-Verzeichnisses schreiben.

**Loesung:** In `storage.Writer.WriteJSON()` wird der Name vor Verwendung sanitized:

```go
func sanitizeName(name string) string {
    // Ersetze alle Zeichen ausser [a-zA-Z0-9_-] durch "_"
    // Verhindert: path separators, null bytes, unicode tricks
}
```

Nach Sanitizing wird zusaetzlich geprueft dass der resultierende Pfad ein Child
des Backup-Verzeichnisses ist (`filepath.Rel` + check for `..`).

---

## 4. Token-Leak Prevention (neu)

**Problem:** Der API-Token darf niemals in Logs, Fehlermeldungen, Dateinamen
oder dem Manifest erscheinen.

**Loesung:**
- `client.Token()` wird entfernt — der Token ist nach Konstruktion des Clients
  nicht mehr abrufbar
- `slog` (Go 1.21 stdlib) ersetzt das `log`-Package — strukturiertes Logging
  ohne versehentliche Token-Ausgabe
- Explizite Tests pruefen nach jedem Codepfad dass der Token-String nicht
  im Log-Output, in Fehlermeldungen oder im Manifest auftaucht

---

## 5. CBOM — Cryptography Bill of Materials (neu)

**Standard:** OWASP CycloneDX 1.6 (CBOM)

**Crypto-Assets des Tools:**

| Zweck | Algorithmus | Go-Paket | Staerke |
|---|---|---|---|
| Datei-Integritaet | SHA-256 | `crypto/sha256` (stdlib) | stark |
| Manifest-Signatur | HMAC-SHA-256 | `crypto/hmac` (stdlib) | stark |
| Transport-Sicherheit | TLS 1.2/1.3 | `crypto/tls` (stdlib) | stark |
| Secret Storage | Windows DPAPI | `wincred` (extern) | OS-verwaltet |

**Generierung:** `cdxgen --type cryptography -o cbom.cdx.json` in CI.
`cbom.cdx.json` wird als CI-Artefakt hochgeladen und im Repo committed.

---

## 6. Aenderungen am CI (neu)

```yaml
- name: CBOM generieren
  run: npx --yes @cyclonedx/cdxgen --type cryptography -o cbom.cdx.json .

- name: CBOM als Artefakt hochladen
  uses: actions/upload-artifact@v4
  with:
    name: cbom
    path: cbom.cdx.json
    retention-days: 365
```

---

## 7. Weitere Code-Korrekturen

| Problem | Loesung |
|---|---|
| Unbegrenzte Goroutinen (eine pro Endpoint) | Bounded Worker Pool: 5 Worker-Goroutinen |
| Retry auch bei 4xx (ausser 429) | Retry nur bei 429 + 5xx; bei 4xx sofort Fehler |
| `log` statt `slog` | `slog` mit strukturierten Feldern, kein Token in Output |

---

## 8. Zusammenfassung der Aenderungen

**Entfernen:**
- `internal/backup/exports.go`
- `internal/backup/exports_test.go`
- `client.Token()`, `client.BaseURL()` aus `internal/api/client.go`

**Aendern:**
- `internal/api/client.go`: Retry-Logik, kein `Token()`
- `internal/backup/runner.go`: Bounded Worker Pool (5)
- `internal/backup/manifest.go`: HMAC-SHA-256 Signatur
- `internal/storage/writer.go`: Path Sanitizing
- `cmd/backup/main.go`: `slog`, kein Exporter, `verify`-Subcommand
- `.github/workflows/ci.yml`: cdxgen CBOM

**Hinzufuegen:**
- `cmd/backup/verify.go`: `verify`-Subcommand
- Security-Tests: Token-Leak, GET-only, Path-Traversal, HMAC
- `cbom.cdx.json`: committed im Repo (initial leer, durch CI befallt)
