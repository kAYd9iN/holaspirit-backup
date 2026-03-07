# Supply Chain & Vertrauenskette

Dieses Dokument beschreibt alle Maßnahmen zur Sicherung der Software-Lieferkette  
und wie jede Schutzschicht verifiziert werden kann.

## Überblick

```
Source Code (GitHub)
    │
    ├─ Commit-Signatur (GPG) — optional, --strict
    │
    ▼
CI-Build (GitHub Actions — gehosteter Runner)
    │
    ├─ govulncheck (CVE-Scan, @v1.1.3)
    ├─ gosec (SAST, @v2.21.4)
    ├─ go mod verify (vendor-Integrität)
    ├─ go test -race (Tests)
    └─ Alle Actions auf Commit-SHA gepinnt
    │
    ▼
Release-Artefakte
    │
    ├─ SLSA L2 Provenance-Attestation (GitHub Attestation Registry)
    ├─ cosign Bundle (Keyless-Signatur via Sigstore/OIDC)
    ├─ SHA256SUMS (Checksummen)
    └─ CycloneDX CBOM (Cryptography Bill of Materials)
    │
    ▼
OpenSSF Scorecard (wöchentlich) + Dependency Review (PRs)
```

## SLSA Level 2

### Was SLSA Level 2 garantiert

- Der Build lief auf einem **verwalteten, gehosteten Build-Service** (GitHub Actions)
- Die Provenance ist **signiert und unveränderlich** im GitHub Attestation Registry gespeichert
- Nachweis: welcher Commit, welcher Workflow, welcher Runner → welches Binary

> **Hinweis:** SLSA Level 2 erfordert **kein** `SLSA.md`-Dokument.  
> Die Anforderungen sind ausschließlich prozessualer Natur (Build-Umgebung + signierte Provenance).
> Dieses Dokument dient der Dokumentation der Verifikation.

### Provenance verifizieren

```bash
# gh CLI installieren: https://cli.github.com
gh attestation verify backup-linux-amd64 --repo kAYd9iN/holaspirit-backup

# Erwartete Ausgabe:
# ✓ Loaded digest sha256:...
# ✓ Verified attestation: 1 verified
# ✓ Build provenance:
#   - Workflow: .github/workflows/release.yml
#   - Repository: kAYd9iN/holaspirit-backup
#   - Commit: <SHA>
```

Für alle Release-Binaries:

```bash
for f in backup-linux-amd64 backup-linux-arm64 backup-windows-amd64.exe; do
  echo "=== $f ==="
  gh attestation verify "$f" --repo kAYd9iN/holaspirit-backup
done
```

## cosign Keyless Signing

Zusätzlich zur SLSA-Attestation werden alle Release-Binaries mit **cosign** signiert.  
Die Signatur erfolgt keyless via GitHub OIDC-Token — kein privater Schlüssel wird verwaltet.

Das Bundle (`*.bundle`) enthält:
- Signatur
- Sigstore-Zertifikat (mit GitHub Actions OIDC-Claims)
- Transparency-Log-Eintrag (Rekor)

### Bundle verifizieren

```bash
# cosign installieren: https://docs.sigstore.dev/cosign/system_config/installation/
cosign verify-blob \
  --bundle backup-linux-amd64.bundle \
  backup-linux-amd64

# Erwartete Ausgabe:
# Verified OK
```

## SHA256 Checksums

Schnelle Integritätsprüfung ohne externe Tools:

```bash
# Linux
sha256sum -c SHA256SUMS

# Windows (PowerShell)
Get-Content SHA256SUMS | ForEach-Object {
    $hash, $file = $_ -split '  '
    $actual = (Get-FileHash $file -Algorithm SHA256).Hash.ToLower()
    if ($actual -eq $hash) { "OK: $file" } else { "FAILED: $file" }
}
```

## SHA-gepinnte GitHub Actions

Alle CI-Actions sind auf unveränderliche Commit-SHAs gepinnt, nicht auf Tags.  
Das verhindert Supply-Chain-Angriffe via force-pushed Tags.

```yaml
# Korrekt: SHA-gepinnt
uses: actions/checkout@34e114876b0b11c390a56381ad16ebd13914f8d5  # v4

# Falsch: Tag kann überschrieben werden
uses: actions/checkout@v4  # NICHT verwenden
```

## OpenSSF Scorecard

Das Repository wird automatisch bewertet:

- **Trigger:** Jeden Montag 01:30 UTC + bei jedem Push auf `main`
- **Ergebnisse:** GitHub Security tab → Code scanning alerts

Scorecard prüft u.a.:
- Branch Protection
- SHA-gepinnte CI-Actions (Supply-Chain-Schutz)
- CI-Tests vorhanden und laufend
- Vulnerability-Scanning
- Signed Releases
- Security Policy vorhanden

Details: [https://securityscorecards.dev](https://securityscorecards.dev)

## Dependency Review

Bei jedem Pull Request auf `main` wird automatisch geprüft, ob neue oder geänderte  
Abhängigkeiten bekannte Schwachstellen (CVSS ≥ 7.0) enthalten.  
Der PR wird blockiert und ein Kommentar mit den Details gepostet.

## vendor/ — eingecheckte Abhängigkeiten

Alle Go-Abhängigkeiten sind im `vendor/`-Verzeichnis eingecheckt.  
Der Build verwendet ausschließlich diese lokalen Kopien:

```bash
go build -mod=vendor ./...
```

Integrität prüfen:

```bash
go mod verify
```

## CycloneDX CBOM

Bei jedem CI-Lauf wird ein Cryptography Bill of Materials (CBOM) generiert:

- Dokumentiert alle kryptografischen Primitive und Bibliotheken
- Format: CycloneDX JSON
- Artefakt: `cbom-cyclonedx` (365 Tage in GitHub Actions)
- Optionale Signierung via `CBOM_SIGNING_KEY_PEM` Secret
