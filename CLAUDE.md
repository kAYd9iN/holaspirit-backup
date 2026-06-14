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
| `internal/api/client.go` | HTTP client — GET-only, explicit TLS≥1.2, rate limiter 250/5min, retry 429+5xx |
| `internal/api/pagination.go` | Page accumulation with url.Values query + per-endpoint item cap |
| `internal/backup/backup.go` | Worker pool (5), fetches all endpoints |
| `internal/backup/manifest.go` | SHA256 per file + HMAC-SHA-256 .sig, error sanitization, hex-decoded verify |
| `internal/storage/writer.go` | Timestamped dirs, 0600 files, path sanitising + symlink resolution |
| `docs/api-snapshot.json` | Baseline of Holaspirit API field structure (for drift detection) |
| `scripts/check-api-schema.sh` | Credential-free API drift check vs published OpenAPI spec |
| `scripts/check-cbom.sh` | Anti-staleness: every crypto import must be in the CBOM |
| `docs/cbom.cdx.json` | CycloneDX 1.6 Cryptography BoM (hand-authored) |
| `policy/nist-crypto.rego` | OPA/conftest NIST crypto policy (gates the release) |

## Architecture

- **GET-only**: client has only `Get()` — no Post/Patch/Delete possible
- **No token exposure**: no `Token()` accessor, token never in logs/errors
- **TLS**: explicit minimum TLS 1.2, `InsecureSkipVerify` hard-false
- **File perms**: 0600 files, 0750 dirs
- **HMAC key**: domain-separated (`holaspirit-backup-manifest-v1` prefix)
- **Log-injection guard**: API/operator values sanitized before logging
- **Symlink guard**: output dir resolved + containment-checked before writing
- **Worker pool**: 5 bounded goroutines; per-endpoint item cap bounds memory
- **CBOM**: real crypto surface in `docs/cbom.cdx.json`, checked against NIST
  SP 800-131A via OPA/conftest; non-approved algorithm or TLS<1.2 gates the release
- **vendor/** checked in for supply-chain safety

## CI/CD Workflows

| Workflow | Trigger | Purpose |
|----------|---------|---------|
| `security-and-quality.yml` | push + PR | govulncheck, gosec, go test -race |
| `build.yml` | push main + tags + PR | 3 platform binaries |
| `release.yml` | `v*` tags | GitHub Release, SLSA provenance, cosign signing |
| `cbom.yml` | push + PR | Dependency SBoM (`sbom.cdx.json`) + CBOM validation + conftest NIST check (informational) |
| `scorecard.yml` | push main + weekly | OpenSSF Scorecard (`SCORECARD_ENABLED=true`; `SCORECARD_TOKEN` optional) |
| `commit-signature.yml` | push + PR | GPG commit check (needs `COMMIT_SIGNING_ENABLED=true` var + `COMMIT_SIGNING_PUBLIC_KEY` secret) |
| `dependency-review.yml` | PR + merge_group | Block high-severity CVEs + license allowlist |
| `dependabot-auto-merge.yml` | Dependabot PR | Auto-merge non-major bumps once CI is green |
| `api-update-check.yml` | daily 06:00 UTC | Spec-based drift detection → Claude adapts code → api-drift PR |
| `auto-release.yml` | api-drift PR merged | Bump 0ver minor, push tag → triggers release |

## Self-Update Loop (API drift)

```
api-update-check (daily 06:00 UTC — no credentials needed)
  → compares Holaspirit's PUBLISHED OpenAPI spec (embedded in the public
    /api/doc/ page) against docs/api-snapshot.json
  → drift detected → Claude adapts internal/api/endpoints.go
    (needs ANTHROPIC_API_KEY or CLAUDE_CODE_OAUTH_TOKEN)
  → PR with label api-drift (auto-merge only if snapshot-only;
    Go/script changes always require human review — prompt-injection guard)
  → merge → auto-release bumps 0ver minor + pushes tag
  → release workflow: security-gate (govulncheck, gosec, race tests, CBOM NIST
    policy, blocks while issues labeled `security` are open) → build → signed release
```

- Baseline: `docs/api-snapshot.json` (committed); sentinel `__ENDPOINT_MISSING__`
  marks endpoints the tool calls that are no longer documented
- The original security review findings (issues #9–#33) are all resolved; the
  security-gate now passes (release v0.2.0 shipped). It will block again if any
  new `security`-labeled issue is opened — by design
- Exit 2 (spec download failed) fails the job; no snapshot is written

## Repo

- GitHub: `kAYd9iN/holaspirit-backup` (public)
- Versioning: 0ver — major stays 0, e.g. `v0.2.0` (see https://0ver.org/)
- go.mod: `go 1.25.8`, but CI uses `go-version: '1.26'` — do not change

## Dependency Updates (Dependabot)

- `dependabot.yml`: daily, with a 7-day `cooldown` (supply-chain maturity window
  — a release is only proposed a week after publication; security advisories bypass it)
- `dependabot-auto-merge.yml`: auto-merges non-major bumps once required CI is green
  (build + security-and-quality + dependency-review); major bumps stay open for review
- Branch ruleset `main-protection` enforces PR + required checks before any merge

## Configured (was "pending")

- ✅ Security findings #9–#33 resolved; release v0.2.0 shipped (gate passes)
- ✅ Secrets CLAUDE_CODE_OAUTH_TOKEN + REPO_PAT set; SCORECARD_ENABLED=true
- ✅ "Allow auto-merge" enabled; `main-protection` ruleset active; Actions cannot approve PRs
- ✅ Secret scanning + push protection + private vulnerability reporting on
- HOLASPIRIT_TOKEN / HOLASPIRIT_ORG_ID are **no longer needed** (drift check reads the public spec)
- Optional, still open: SCORECARD_TOKEN (improves Branch-Protection check only),
  COMMIT_SIGNING_PUBLIC_KEY + COMMIT_SIGNING_ENABLED (to enforce signed commits)

## Confluence (HB Space, cloudId: 78b5b3f6-a4c9-4f9d-856e-56eca016288c)

- Sicherheitskonzept (ID: 2326555)
- Design & Architektur (ID: 2326535)
- Betrieb & Installation (ID: 2490371)

## Extending: Adding a New Endpoint

1. Add entry to `AllEndpoints()` in `internal/api/endpoints.go`
2. Run `GOTOOLCHAIN=go1.25.8 go test ./internal/api/` to verify
3. Update `docs/api-snapshot.json` baseline: run `scripts/check-api-schema.sh` locally
4. Commit + push → CI validates
