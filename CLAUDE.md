# holaspirit-backup

Backup tool for Holaspirit organisational data. Fetches 21 GET-only endpoints → timestamped JSON files + HMAC-SHA-256 signed manifest.

## Commands

```bash
# Run tests
GOTOOLCHAIN=go1.25.8 go test ./...

# Build
go build -o backup ./cmd/backup

# Run backup
./backup --org-id <org> --output ./out

# Verify manifest
./backup verify --dir ./out/2026-03-07_120000
```

## Key Files

| Path | Purpose |
|------|---------|
| `internal/api/endpoints.go` | All 21 endpoints (edit here to add/remove) |
| `internal/api/client.go` | HTTP client — GET-only, rate limiter 250/5min, retry 429+5xx |
| `internal/backup/backup.go` | Worker pool (5), fetches all endpoints |
| `internal/backup/manifest.go` | SHA256 per file + HMAC-SHA-256 .sig |
| `internal/storage/writer.go` | Timestamped dirs, 0600 files, path sanitising |
| `cmd/backup/main.go` | CLI entry point |
| `docs/api-snapshot.json` | Baseline of Holaspirit API field structure (for drift detection) |

## Architecture

- **GET-only**: client has only `Get()` — no Post/Patch/Delete possible
- **No token exposure**: no `Token()` accessor, token never in logs/errors
- **File perms**: 0600 files, 0750 dirs
- **HMAC key**: domain-separated (`holaspirit-backup-manifest-v1` prefix)
- **Worker pool**: 5 bounded goroutines
- **vendor/** checked in for supply-chain safety

## CI/CD Workflows

| Workflow | Trigger | Purpose |
|----------|---------|---------|
| `security-and-quality.yml` | push + PR | govulncheck, gosec, go test -race |
| `build.yml` | push main + tags + PR | 3 platform binaries |
| `release.yml` | `v*` tags | GitHub Release, SLSA provenance, cosign signing |
| `cbom.yml` | push + PR | CycloneDX SBoM (`--type go`) |
| `scorecard.yml` | push main + weekly | OpenSSF Scorecard (needs `SCORECARD_ENABLED=true` var + `SCORECARD_TOKEN` secret) |
| `commit-signature.yml` | push + PR | GPG commit check (needs `COMMIT_SIGNING_ENABLED=true` var + `COMMIT_SIGNING_PUBLIC_KEY` secret) |
| `dependency-review.yml` | PR only | Block high-severity CVEs |
| `api-update-check.yml` | weekly Monday | Detect Holaspirit API field changes, open issue if drift |

## Repo

- GitHub: `kAYd9iN/holaspirit-backup` (public)
- Versioning: 0ver — major stays 0, e.g. `v0.2.0` (see https://0ver.org/)
- go.mod: `go 1.25.8`, but CI uses `go-version: '1.26'` — do not change

## Pending Manual Steps

- Set `SCORECARD_TOKEN` secret (PAT with `repo` + `read:org`)
- Set `COMMIT_SIGNING_PUBLIC_KEY` secret (GPG public key)
- Set `HOLASPIRIT_TOKEN` + `HOLASPIRIT_ORG_ID` secrets for api-update-check workflow

## Confluence (HB Space, cloudId: 78b5b3f6-a4c9-4f9d-856e-56eca016288c)

- Sicherheitskonzept (ID: 2326555)
- Design & Architektur (ID: 2326535)
- Betrieb & Installation (ID: 2490371)

## Extending: Adding a New Endpoint

1. Add entry to `AllEndpoints()` in `internal/api/endpoints.go`
2. Run `GOTOOLCHAIN=go1.25.8 go test ./internal/api/` to verify
3. Update `docs/api-snapshot.json` baseline: run `scripts/check-api-schema.sh` locally
4. Commit + push → CI validates
