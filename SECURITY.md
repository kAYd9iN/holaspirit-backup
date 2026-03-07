# Security Policy

## Unterstützte Versionen

| Version | Sicherheitsupdates |
|---------|-------------------|
| Neuestes Release | ✓ |
| Ältere Releases | — |

Nur das jeweils neueste Release erhält Sicherheitsupdates.

## Sicherheitslücke melden

**Bitte keine Sicherheitslücken als öffentliche GitHub Issues melden.**

Stattdessen bitte [GitHub Private Vulnerability Reporting](https://github.com/kAYd9iN/holaspirit-backup/security/advisories/new) nutzen. Das ermöglicht vertrauliche Koordination vor einer öffentlichen Offenlegung.

### Inhalt der Meldung

- Beschreibung der Sicherheitslücke
- Schritte zur Reproduktion
- Mögliche Auswirkungen
- Optional: vorgeschlagener Fix

### Reaktionszeit

| Phase | Ziel |
|-------|------|
| Eingangsbestätigung | 5 Werktage |
| Erstbewertung | 10 Werktage |
| Fix für kritische Lücken | 30 Tage |
| Fix für mittlere/niedrige Lücken | 90 Tage |

## Scope

**In Scope:**
- Sicherheitslücken im Tool-Code (`cmd/`, `internal/`)
- Schwachstellen in CI/CD-Workflows (`.github/workflows/`)
- Token-/Authentifizierungs-Schwachstellen
- Supply-Chain-Risiken (Abhängigkeiten, Actions)

**Out of Scope:**
- Holaspirit-API-Schwachstellen → direkt an Holaspirit melden
- GitHub-Actions-Plattform-Schwachstellen → an GitHub melden
- Probleme, die physischen Zugriff auf das Backup-System erfordern

## Sicherheitsarchitektur

Für eine vollständige Beschreibung aller Sicherheitsmaßnahmen siehe:

- [Sicherheitskonzept](docs/security-concept.md) — Prinzipien, Token-Verwaltung, CI-Security
- [Supply Chain & Vertrauenskette](docs/supply-chain.md) — SLSA L2, cosign, Scorecard, Verifikation
