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
    ├─ govulncheck (CVE-Scan, @v1.1.4)
    ├─ gosec (SAST, @v2.24.7)
    ├─ go mod verify (vendor-Integrität)
    ├─ go test -race (Tests)
    └─ Alle Actions auf Commit-SHA gepinnt
    │
    ▼
Release-Security-Gate (release.yml)
    │
    ├─ govulncheck + gosec + Race-Tests
    ├─ CBOM-Konsistenz (check-cbom.sh) + NIST-Policy (conftest)
    └─ Block bei offenen `security`-Issues
    │  (alle Build-Jobs starten erst nach grünem Gate)
    ▼
Release-Artefakte
    │
    ├─ SLSA L2 Provenance-Attestation (GitHub Attestation Registry)
    ├─ cosign Bundle (Keyless-Signatur via Sigstore/OIDC)
    ├─ SHA256SUMS (Checksummen)
    └─ SBoM + CBOM (CycloneDX, als Artifact)
    │
    ▼
OpenSSF Scorecard (wöchentlich) + Dependency Review (PRs)
    + Dependabot (täglich, 7-Tage-Cooldown, Auto-Merge reifer Bumps)
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

Bei jedem Pull Request auf `main` (und in der Merge-Queue) wird automatisch geprüft, ob neue oder geänderte  
Abhängigkeiten bekannte Schwachstellen (CVSS ≥ 7.0) enthalten **oder** eine nicht-permissive Lizenz tragen  
(Allowlist: MIT, Apache-2.0, BSD-2-Clause, BSD-3-Clause, ISC). Der PR wird blockiert und ein Kommentar mit den Details gepostet.  
Direkte Pushes auf `main` sind durch das `main-protection`-Ruleset ausgeschlossen, sodass diese Prüfung nicht umgangen werden kann.

## Dependabot — Reifezeit & Auto-Merge

Abhängigkeits-Updates durchlaufen eine **Supply-Chain-Reifezeit**, bevor sie übernommen werden:

- **Cooldown:** Ein Release wird erst **7 Tage nach Veröffentlichung** als PR vorgeschlagen (`cooldown: default-days: 7` in `dependabot.yml`). So werden kompromittierte oder zurückgezogene Versionen meist erkannt, bevor sie gemerged werden. **Security-Advisories umgehen den Cooldown** (sofort).
- **Täglich:** Nach Ablauf der Reifezeit wird der PR beim nächsten täglichen Check geöffnet.
- **Auto-Merge:** `dependabot-auto-merge.yml` merged **Minor/Patch automatisch**, sobald die Pflicht-CI (Build + security-and-quality + dependency-review) grün ist. **Major-Bumps bleiben für manuelle Review offen.**

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

## SBoM und CBOM

Bei jedem CI-Lauf werden zwei getrennte Stücklisten erzeugt (Artefakt `bom-cyclonedx`, 365 Tage):

- **Dependency-SBoM** (`sbom.cdx.json`) — via cdxgen `--type go`: alle Modul-Abhängigkeiten mit PURLs, Hashes, Lizenzen. **Kein** CBOM (Go-Stdlib-Krypto erscheint hier nicht).
- **CBOM** (`docs/cbom.cdx.json`) — die tatsächliche Krypto-Oberfläche als CycloneDX 1.6 mit `cryptographic-asset`-Komponenten (SHA-256, HMAC-SHA-256, TLS ≥ 1.2, ECDSA P-256). Hand-gepflegt, da automatisierte Scanner Go-Stdlib-Krypto nicht erkennen; `scripts/check-cbom.sh` hält ihn ehrlich.

**NIST-Compliance-Gate:** Der CBOM wird via OPA/conftest gegen `policy/nist-crypto.rego` (NIST SP 800-131A) geprüft. Ein nicht-zugelassener Algorithmus oder TLS < 1.2 lässt das Release-Security-Gate fehlschlagen. Quantensicherheit ist eine nicht-blockierende Warnung (ECDSA P-256). Lokal: `go run github.com/open-policy-agent/conftest@v0.68.2 test docs/cbom.cdx.json --policy policy`.

- Optionale Signierung beider BoMs via `CBOM_SIGNING_KEY_PEM` Secret
