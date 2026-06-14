# Betrieb & Installation

## Voraussetzungen

- Windows 10/11 oder Windows Server 2019+ (für Windows-Betrieb)
- Linux: amd64 oder arm64 (alternativ)
- Netzwerkzugang zu `app.holaspirit.com` (HTTPS/443)
- Holaspirit Read-Only API Token
- Veeam (für Weiterverarbeitung des Backup-Verzeichnisses)

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
C:\Tools\holaspirit-backup\backup.exe
```

**Linux:**

```bash
chmod +x backup-linux-amd64
sudo mv backup-linux-amd64 /usr/local/bin/holaspirit-backup
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

### 3. API-Token hinterlegen

**Windows — PowerShell als Administrator:**

```powershell
cmdkey /generic:holaspirit-backup /user:api /pass:api:DEIN_TOKEN_HIER
```

Token-Verwaltung:

```powershell
# Token aktualisieren
cmdkey /generic:holaspirit-backup /user:api /pass:api:NEUER_TOKEN

# Token entfernen
cmdkey /delete:holaspirit-backup

# Token prüfen (ob vorhanden)
cmdkey /list:holaspirit-backup
```

**Linux:**

```bash
export HOLASPIRIT_TOKEN="api:DEIN_TOKEN_HIER"
```

### 4. Backup-Verzeichnis festlegen

```powershell
mkdir C:\Backups\holaspirit
```

### 5. Ersten Testlauf durchführen

```powershell
C:\Tools\holaspirit-backup\backup.exe --dry-run
```

Erwartete Ausgabe:

```
time=2026-03-06T02:00:00Z level=INFO msg="organization confirmed" id=org_xxx
time=2026-03-06T02:00:00Z level=INFO msg="dry run successful — connection OK"
```

Echtes Backup:

```powershell
C:\Tools\holaspirit-backup\backup.exe --output C:\Backups\holaspirit
```

Erwartete Ausgabe:

```
time=2026-03-06T02:00:00Z level=INFO msg="organization confirmed" id=org_xxx
time=2026-03-06T02:00:00Z level=INFO msg="backup directory created" path=C:\Backups\holaspirit\2026-03-06T02-00-00
time=2026-03-06T02:00:00Z level=INFO msg="fetching endpoints" count=21
time=2026-03-06T02:00:45Z level=INFO msg="endpoint ok" name=circles records=42
...
time=2026-03-06T02:00:46Z level=INFO msg="backup complete" dir=C:\Backups\holaspirit\2026-03-06T02-00-00
```

## Integrität prüfen

Nach jedem Backup oder vor der Verwendung der Daten kann die Integrität geprüft werden:

```powershell
backup.exe verify --dir C:\Backups\holaspirit\2026-03-06T02-00-00
```

| Exit-Code | Bedeutung |
|-----------|-----------|
| 0 | Manifest-Signatur und alle Datei-Hashes korrekt |
| 1 | Signatur ungültig oder Hash-Abweichung — Backup kompromittiert |
| 2 | Ungültige Argumente |

## Windows Task Scheduler (Cronjob)

### Automatisch einrichten (PowerShell als Admin)

```powershell
$action = New-ScheduledTaskAction `
    -Execute "C:\Tools\holaspirit-backup\backup.exe" `
    -Argument "--output C:\Backups\holaspirit"

$trigger = New-ScheduledTaskTrigger -Daily -At "02:00"

$settings = New-ScheduledTaskSettingsSet `
    -ExecutionTimeLimit (New-TimeSpan -Hours 1) `
    -RestartCount 3 `
    -RestartInterval (New-TimeSpan -Minutes 5)

Register-ScheduledTask `
    -TaskName "Holaspirit Backup" `
    -Action $action `
    -Trigger $trigger `
    -Settings $settings `
    -RunLevel Highest
```

### Manuell ausführen

```powershell
Start-ScheduledTask -TaskName "Holaspirit Backup"
```

## Veeam-Integration

Veeam-Job so konfigurieren, dass er das Backup-Verzeichnis einschließt:

- **Pfad:** `C:\Backups\holaspirit`
- **Empfehlung:** Veeam-Job nach dem Holaspirit-Backup-Job einplanen (z.B. 03:00 Uhr)
- Veeam übernimmt: Verschlüsselung, Komprimierung, Off-site-Speicherung

## CLI-Referenz

```
backup [Optionen]
backup verify --dir <path>

Hauptbefehl:
  --output PATH     Backup-Zielverzeichnis (Standard: ./backup)
  --org-id ID       Organisations-ID (optional, wird automatisch ermittelt)
  --dry-run         Verbindung testen ohne Daten zu schreiben
  --timeout MIN     Gesamt-Timeout in Minuten (Standard: 120, 0 = kein Timeout)
  --audit           Hostname + Benutzer im Manifest erfassen (Audit-Trail)
  --version         Version anzeigen
  --help            Hilfe anzeigen

Subcommand verify:
  --dir PATH        Backup-Verzeichnis prüfen (Pflichtfeld)
```

## Fehlerbehandlung

Das Tool gibt Exit-Code 1 zurück wenn mindestens ein Endpunkt fehlgeschlagen ist.  
Windows Task Scheduler protokolliert diesen.

Fehlgeschlagene Endpunkte werden im Manifest als `"status": "failed"` markiert —  
der Rest des Backups bleibt verwendbar.

## Backup-Aufbewahrung

Das Tool selbst löscht **keine** alten Backups.  
Aufbewahrungsstrategie wird vollständig durch Veeam verwaltet.
