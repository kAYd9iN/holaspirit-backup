# Betrieb & Installation

## Voraussetzungen

- Windows 10/11 oder Windows Server 2019+ (für Windows-Betrieb)
- Linux: amd64 oder arm64 (alternativ)
- Netzwerkzugang zu `partner-api.talkspirit.com` (HTTPS/443)
- Talkspirit-API-Schlüssel (`ts_partner_…`) mit allen Lese-Scopes, siehe unten
- Veeam (für Weiterverarbeitung des Backup-Verzeichnisses)

> Holaspirit-Backups (bis v0.5.0) bleiben mit dem v0.5.0-Binary prüfbar
> (`verify`, Holaspirit-Token nötig). Die Holaspirit-API wird am 30.10.2026 abgeschaltet.

## Installation

### 1. Binary herunterladen

Die fertigen Binaries von [GitHub Releases](https://github.com/kAYd9iN/holaspirit-backup/releases) herunterladen:

| Plattform | Dateiname |
|-----------|----------|
| Windows (64-bit) | `backup-windows-amd64.exe` |
| Linux (x86_64) | `backup-linux-amd64` |
| Linux (ARM64) | `backup-linux-arm64` |

**Windows:**

```powershell
# Binary speichern unter:
C:\Tools\talkspirit-backup\backup.exe
```

**Linux:**

```bash
chmod +x backup-linux-amd64
sudo mv backup-linux-amd64 /usr/local/bin/talkspirit-backup
```

### 2. Integrität prüfen

```bash
# SHA256 gegen SHA256SUMS prüfen
sha256sum -c SHA256SUMS

# SLSA L2 Provenance verifizieren
gh attestation verify backup-linux-amd64 --repo kAYd9iN/holaspirit-backup

# cosign Bundle verifizieren
cosign verify-blob \
  --bundle backup-linux-amd64.bundle \
  backup-linux-amd64
```

Siehe [docs/supply-chain.md](supply-chain.md) für Details zur Verifikation.

### 3. API-Schlüssel anlegen und hinterlegen

Ein Talkspirit-Administrator:

1. aktiviert das **API-Modul** der Organisation (ohne Modul antwortet jede Anfrage mit 401),
2. erstellt unter `https://<subdomain>.talkspirit.com/app/admin/api-keys` einen Schlüssel mit
   den Scopes `users:read`, `circles:read`, `roles:read`, `tasks:read`, `projects:read`,
   `goals:read`, `documents:read`, `meetings:read`, `tensions:read`, `audit_log:read` —
   **keine** Schreib-Scopes,
3. gibt den Schlüssel (wird nur einmal angezeigt) sicher weiter.

Der Schlüssel arbeitet als eigener technischer Benutzer und liest nur, was dieser sehen darf.
Für ein vollständiges Backup braucht er Admin-Rechte (das Audit-Log ist nur für Admins lesbar).

**Windows — Credential Manager:**

```powershell
cmdkey /generic:talkspirit-backup /user:api /pass:ts_partner_DEIN_SCHLUESSEL
```

Schlüssel-Verwaltung:

```powershell
cmdkey /generic:talkspirit-backup /user:api /pass:ts_partner_NEUER_SCHLUESSEL   # ersetzen
cmdkey /delete:talkspirit-backup                                                 # entfernen
cmdkey /list:talkspirit-backup                                                   # vorhanden?
```

**Alternativ (alle Plattformen) — Umgebungsvariable, hat Vorrang:**

```bash
export TALKSPIRIT_API_KEY="ts_partner_DEIN_SCHLUESSEL"
```

### 4. Backup-Verzeichnis festlegen

```powershell
mkdir C:\Backups\talkspirit
```

### 5. Ersten Testlauf durchführen

```powershell
C:\Tools\talkspirit-backup\backup.exe --dry-run
```

Erwartete Ausgabe:

```
level=INFO msg="API key OK" tenant=… partner=…
level=INFO msg="dry run successful — connection OK"
```

Fehlen Scopes, meldet der Testlauf `the key lacks read scopes` mit der Liste.

Echtes Backup:

```powershell
C:\Tools\talkspirit-backup\backup.exe --output C:\Backups\talkspirit
```

Der erste vollständige Lauf dauert bei grossen Organisationen **mehrere Stunden**: pro Meeting,
Aufgabe, Projekt, Ziel und Dokument werden Traktanden bzw. Kommentare einzeln abgerufen, und die
API erlaubt rund 100 Anfragen pro Minute. `--quick` lässt diese Einzelabrufe weg (Minuten statt
Stunden), die Traktanden fehlen dann aber auch im Vergleich.

## Integrität prüfen

```powershell
backup.exe verify --dir C:\Backups\talkspirit\2026-10-16T18-00-00
```

| Exit-Code | Bedeutung |
|-----------|-----------|
| 0 | Manifest-Signatur und alle Datei-Hashes korrekt |
| 1 | Signatur ungültig oder Hash-Abweichung — Backup kompromittiert |
| 2 | Ungültige Argumente |

## Vergleich mit dem Holaspirit-Backup

```powershell
backup.exe compare `
  --holaspirit C:\Temp\holaspirit-backup-data `
  --talkspirit C:\Backups\talkspirit\2026-10-16T18-00-00 `
  --migrated-at 2026-10-15T06:00 `
  --out C:\Backups\vergleich
```

- `--migrated-at`: Ende der Migration in UTC. Ohne Angabe wird es geschätzt (der Tag, an dem die
  meisten Kreise/Rollen/Personen in Talkspirit angelegt wurden) und im Bericht als „geschätzt“
  ausgewiesen. Davon hängt ab, ob eine Abweichung als vermutlich gewollt oder als Verdacht auf
  einen Migrationsfehler eingeordnet wird.
- Ergebnis: `vergleich.xlsx`. Enthält Namen und E-Mail-Adressen: nicht ins Repository, nicht in
  Tickets kopieren.
- Vorgehen beim Auswerten: Blatt „Übersicht“ zeigt, wo die Verdachtsfälle liegen. Das Team
  arbeitet die „Arbeitsliste“ von oben nach unten ab (Priorität Hoch = Personen, Kreise, Rollen,
  Besetzungen, Richtlinien) und setzt pro Zeile den Status. Vor dem Versand an Talkspirit in der
  „Mängelliste“ die Nummern entfernen, die als „Kein Fehler“ markiert wurden.

## Windows Task Scheduler (Cronjob)

### Automatisch einrichten (PowerShell als Admin)

```powershell
$action = New-ScheduledTaskAction `
    -Execute "C:\Tools\talkspirit-backup\backup.exe" `
    -Argument "--output C:\Backups\talkspirit"

$trigger = New-ScheduledTaskTrigger -Daily -At "01:00"

$settings = New-ScheduledTaskSettingsSet `
    -ExecutionTimeLimit (New-TimeSpan -Hours 8) `
    -RestartCount 3 `
    -RestartInterval (New-TimeSpan -Minutes 5)

Register-ScheduledTask `
    -TaskName "Talkspirit Backup" `
    -Action $action `
    -Trigger $trigger `
    -Settings $settings `
    -RunLevel Highest
```

### Manuell ausführen

```powershell
Start-ScheduledTask -TaskName "Talkspirit Backup"
```

## Veeam-Integration

Veeam-Job so konfigurieren, dass er das Backup-Verzeichnis einschließt:

- **Pfad:** `C:\Backups\talkspirit`
- **Empfehlung:** Veeam-Job nach dem Backup-Job einplanen (Laufzeit des Backups beachten)
- Veeam übernimmt: Verschlüsselung, Komprimierung, Off-site-Speicherung

## CLI-Referenz

```
backup [Optionen]
backup verify --dir <path>
backup compare --holaspirit <dir> --talkspirit <dir> [--out <dir>] [--migrated-at <zeit>]

Hauptbefehl:
  --output PATH     Backup-Zielverzeichnis (Standard: ./backup)
  --quick           Kommentare und Traktanden nicht einzeln abrufen
  --dry-run         Schlüssel prüfen (GET /v1/whoami), nichts schreiben
  --timeout MIN     Gesamt-Timeout in Minuten (Standard: 480, 0 = kein Timeout)
  --base-url URL    API-Host (Standard: https://partner-api.talkspirit.com;
                    Staging: https://partner-api.ghostspirit.net)
  --audit           Hostname + Benutzer im Manifest erfassen (Audit-Trail)
  --version         Version anzeigen
  --help            Hilfe anzeigen

Subcommand verify:
  --dir PATH        Backup-Verzeichnis prüfen (Pflichtfeld)

Subcommand compare:
  --holaspirit DIR  Holaspirit-Backup (v0.5.0)
  --talkspirit DIR  Talkspirit-Backup
  --out DIR         Zielordner für vergleich.xlsx (Standard: ./vergleich)
  --migrated-at T   Ende der Migration, YYYY-MM-DD oder YYYY-MM-DDTHH:MM (UTC)
```

## Fehlerbehandlung

Das Tool gibt Exit-Code 1 zurück, wenn mindestens ein Endpunkt fehlgeschlagen oder
unvollständig ist. Windows Task Scheduler protokolliert diesen.

Fehlgeschlagene Endpunkte werden im Manifest als `"status": "failed"` markiert —
der Rest des Backups bleibt verwendbar.

### Erster Lauf: Ergebnis prüfen

1. **Schlüssel:** `--dry-run` muss `API key OK` melden und darf keine fehlenden Scopes nennen.
   HTTP 401 heisst: Schlüssel falsch/widerrufen oder API-Modul nicht aktiviert.
2. **Log durchsehen**, die Meldungen bedeuten:

   | Meldung | Bedeutung |
   |---------|-----------|
   | `endpoint ok` | vollständig gesichert; `records` = Anzahl Einträge, `sub-resources` = Einträge pro Unter-Ressource |
   | `endpoint incomplete` | Daten geschrieben, aber nicht alles abrufbar: 10'000er-Grenze der API oder einzelne Unter-Ressourcen fehlgeschlagen (Details in der Meldung) |
   | `endpoint not available` | Audit-Log für diesen Schlüssel nicht lesbar (kein Admin) — kein Fehler |
   | `endpoint failed` | nichts gesichert; Grund in der Meldung (z. B. „lacks the … scope“) |

3. **Plausibilität:** `records` von `users`, `roles`, `circles` mit der Talkspirit-Oberfläche
   vergleichen.
4. Bei `incomplete` oder `failed`: die Log-Zeilen (sie enthalten keine Organisationsdaten) an die
   Maintainer weitergeben. Den Backup-Ordner selbst nicht weitergeben, er enthält Organisationsdaten.

## Backup-Aufbewahrung

Das Tool selbst löscht **keine** alten Backups.
Aufbewahrungsstrategie wird vollständig durch Veeam verwaltet.
