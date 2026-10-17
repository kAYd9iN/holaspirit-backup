# holaspirit-backup (now: talkspirit-backup)

Backup tool for **Talkspirit** organisational data via the official Partner API
(`https://partner-api.talkspirit.com/v1`, key `ts_partner_…`). Fetches every read route
(cursor pagination, plus per-item sub-resources) → timestamped JSON files + HMAC-SHA-256
signed manifest. `backup compare` matches a Holaspirit backup (v0.5.0) against a Talkspirit
backup to find what the Holaspirit → Talkspirit migration lost.

Holaspirit was replaced by Talkspirit; the Holaspirit API is shut down on 2026-10-30.
**v0.5.0 is the last Holaspirit release.** The repo/module name stays `holaspirit-backup`.

## Commands

```bash
# Run tests
go test ./...

# Build
go build -o backup ./cmd/backup

# Run backup (key from $TALKSPIRIT_API_KEY or Windows Credential Manager "talkspirit-backup")
./backup --dry-run
./backup --output ./out            # --quick skips per-item comments/agenda items

# Compare with a Holaspirit backup (writes vergleich.xlsx — contains PII)
./backup compare --holaspirit <hs-dir> --talkspirit <ts-dir> --migrated-at 2026-10-15 --out ./vergleich

# Verify manifest
./backup verify --dir ./out/2026-03-07_120000
```

## Key Files

| Path | Purpose |
|------|---------|
| `internal/api/endpoints.go` | All Partner API routes + `Children` (per-item sub-resources) + deliberate exclusions |
| `internal/api/client.go` | HTTP client — GET-only, TLS≥1.2, no redirects, pacing 1/650ms, honours `X-RateLimit-*` + `Retry-After`, RFC 7807 `code` → `StatusError.Problem` |
| `internal/api/pagination.go` | `FetchAll`: cursor pagination (`next_cursor`/`has_more`), cursor-loop + repeated-page detection, 10k traversal cap (`400 INVALID_CURSOR`) → `ErrTraversalCap` with partial items |
| `internal/backup/runner.go` | Worker pool (3), writes `<name>.json` and `<child>.json` (`[{parent_id, data}]`); `--quick` skips `Detail` children |
| `internal/compare/` | Holaspirit ↔ Talkspirit matching on business keys (no shared IDs), classification against the cut-off date, prioritized German Excel report (overview, work list, defect list, sheet per area) |
| `internal/xlsx/` | Dependency-free .xlsx writer (inline strings, fixed style palette, filters, validations) — no third-party lib, keeps vendor/CBOM untouched |
| `internal/backup/manifest.go` | SHA256 per file + HMAC-SHA-256 .sig, error sanitization, hex-decoded verify |
| `internal/storage/writer.go` | Timestamped dirs, 0600 files, path sanitising + symlink resolution |
| `docs/api-snapshot.json` | Baseline of the Partner API contract (fields + query params per route, untracked routes) |
| `scripts/check-api-schema.sh` | Credential-free drift check vs `/v1/openapi.json` |
| `scripts/check-cbom.sh` | Anti-staleness: every crypto import must be in the CBOM |
| `docs/cbom.cdx.json` | CycloneDX 1.6 Cryptography BoM (hand-authored) |
| `policy/nist-crypto.rego` | OPA/conftest NIST crypto policy (gates the release) |

## Architecture

- **GET-only**: client has only `Get()` — no Post/Patch/Delete possible
- **No token exposure**: no `Token()` accessor, token never in logs/errors
- **TLS**: explicit minimum TLS 1.2, `InsecureSkipVerify` hard-false
- **File perms**: 0600 files, 0750 dirs
- **HMAC key**: domain-separated (`holaspirit-backup-manifest` prefix, kept unchanged so `verify` still checks old Holaspirit backups given the Holaspirit token)
- **Log-injection guard**: API/operator values sanitized before logging
- **Symlink guard**: output dir resolved + containment-checked before writing
- **Worker pool**: 3 bounded goroutines (the quota is per key); per-endpoint item cap bounds memory
- **CBOM**: real crypto surface in `docs/cbom.cdx.json`, checked against NIST
  SP 800-131A via OPA/conftest; non-approved algorithm or TLS<1.2 gates the release
- **vendor/** checked in for supply-chain safety

## CI/CD Workflows

| Workflow | Trigger | Purpose |
|----------|---------|---------|
| `security-and-quality.yml` | push + PR | govulncheck, gosec, go test -race |
| `build.yml` | push main + tags + PR | 3 platform binaries |
| `release.yml` | `v*` tags | Security gate → 3 builds (SLSA L2 attestation) + SLSA L3 build of linux/amd64 → cosign → GitHub Release |
| `go-ossf-slsa3-publish.yml` | manual | Test run of the SLSA L3 builder. Keep it on the `@vX.Y.Z` tag — a SHA ref fails ("Invalid ref") |
| `cbom.yml` | push + PR | Dependency SBoM (`sbom.cdx.json`) + CBOM validation + conftest NIST check (informational) |
| `scorecard.yml` | push main + weekly | OpenSSF Scorecard (`SCORECARD_ENABLED=true`; `SCORECARD_TOKEN` optional) |
| `dependency-review.yml` | PR + merge_group | Block high-severity CVEs + license allowlist |
| `dependabot-auto-merge.yml` | Dependabot PR | Auto-merge non-major bumps once CI is green |
| `api-update-check.yml` | daily 06:00 UTC | Spec-based drift detection → Claude adapts code → api-drift PR |
| `auto-release.yml` | api-drift PR merged | Bump 0ver minor, push tag → triggers release |

## Self-Update Loop (API drift)

```
api-update-check (daily 06:00 UTC — no credentials needed)
  → compares the Talkspirit Partner API's PUBLISHED contract
    (/v1/openapi.json) against docs/api-snapshot.json
  → drift detected → job `check`: Claude adapts internal/api/endpoints.go
    (needs ANTHROPIC_API_KEY or CLAUDE_CODE_OAUTH_TOKEN) → patch artifact
  → job `verify`: build + race tests on the patch (read-only, no secrets)
  → job `pr`: PR with label api-drift (auto-merge only if snapshot-only;
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
- Trust split: `drift.diff` comes from Talkspirit's public contract (untrusted). The `check`
  job has no persisted git credentials, and Claude is limited to Read/Edit/Write/Glob/
  Grep + `go build`/`go vet` (no `go test`, it executes code). Tests run in `verify`
  (no secrets, no OIDC, no Go cache). Only `pr` sees REPO_PAT and it runs no patch code.
  Only `docs/api-snapshot.json`, `internal/` and `scripts/check-api-schema.sh` enter the patch
- One drift PR at a time: while an `api-drift` PR is open, the check only reports
  (avoids a duplicate PR every day)

## Repo

- GitHub: `kAYd9iN/holaspirit-backup` (public)
- Versioning: 0ver — major stays 0, e.g. `v0.2.0` (see https://0ver.org/)
- go.mod: `go 1.26.9` (minimum patched toolchain; the SLSA builder reads it via `go-version-file`).
  CI uses `go-version: '1.26'` + `check-latest: true` so every run gets the newest 1.26 patch —
  without it setup-go uses the runner's cached Go, which made govulncheck fail (GO-2026-6617)

## Dependency Updates (Dependabot)

- `dependabot.yml`: daily, with a 7-day `cooldown` (supply-chain maturity window
  — a release is only proposed a week after publication; security advisories bypass it)
- `dependabot-auto-merge.yml`: auto-merges non-major bumps once required CI is green
  (build + security-and-quality + dependency-review); major bumps stay open for review
- Branch ruleset `main-protection` enforces PR + required checks before any merge

## Configured (was "pending")

- ✅ Security findings #9–#33 resolved; release v0.2.0 shipped (gate passes)
- ✅ Secrets CLAUDE_CODE_OAUTH_TOKEN + REPO_PAT set; SCORECARD_ENABLED=true
- ✅ "Allow auto-merge" enabled; `main-protection` ruleset active (PR + checks + squash-only); Actions cannot approve PRs
- ✅ Secret scanning + push protection + private vulnerability reporting on
- No API key is needed in CI (drift check reads the public contract)
- Optional, still open: SCORECARD_TOKEN (improves Branch-Protection check only)

## Commit signing — not enforced (by design)

Not enforced at the branch level. GitHub's `required_signatures` rejects merging
any PR with unsigned commits (it inspects the PR's commits, not just the squash
result), which would break the api-drift loop (its `github-actions[bot]` commits
are unsigned) and block unsigned local commits. The high-value signature — the
release artifacts — is already provided by cosign + SLSA. The old
`commit-signature.yml` workflow was removed (inconsistent, verified nothing
without a key, strict mode incompatible with bot commits).

## Confluence (HB Space, cloudId: 78b5b3f6-a4c9-4f9d-856e-56eca016288c)

- Sicherheitskonzept (ID: 2326555)
- Design & Architektur (ID: 2326535)
- Betrieb & Installation (ID: 2490371)

## Talkspirit data facts (real backups, 2026-10-17 — drive internal/compare)

- Rich text (purpose, accountabilities, domains, descriptions, documents, field values) is BlockNote JSON in a string; mentions are `{"type":"mention","props":{"userName":…}}` with the *current* (possibly renamed) name, often without surrounding spaces
- created_at/updated_at are Holaspirit's; every membership carries the import time as updated_at → default cut-off
- `subtask_count` excludes archived sub-tasks → backup fetches sub-tasks for every task (v0.6.1)
- API hides project-less tasks the technical user is not involved in and suspended users → `NICHT_BESTAETIGBAR`, never `FEHLT`
- Expired elections stay with `expires_at`; Holaspirit `checked`/`completedAt` = done; `/v1/audit-logs` → 409 `ACTIVITY_LOG_DISABLED` when the activity log is off
- Cross-checks used: accountabilities 2579 = 2579, domains 160 = 160 (HS vs TS)

## Holaspirit live-run findings (2026-10-14, v0.5.0 — historic)

- Pagination per spec works (533 roles, 4081 meetings, 5490 tasks).
- **10,000 result cap**: tensions/node-tensions stopped at exactly 10,000 with totalItems capped
  too → `api.ResultCap`; `ByCreated` endpoints use `FetchByCreatedWindows` (self-verifying date
  format, halving windows, dedupe, createdAt-in-window check); any other endpoint at the cap → incomplete
- `/api/me` → 403 for organization tokens → `--org-id` / `HOLASPIRIT_ORG_ID`, clear error
- translations/node-translations 404, chartviews 400 → `Endpoint.Optional` → status `unavailable`;
  HTTP 400 on paging → one plain retry
- Tests: `api.Unthrottled` (export_test.go) removes the rate limiter for fake-server tests

## Extending: Adding a New Endpoint

1. Add an entry to `endpoints` in `internal/api/endpoints.go` (`Limit` when the route's maximum
   is below 199; a per-item sub-resource goes into `Children`, `Detail: true` if `--quick` may skip it)
2. Add the same name → OpenAPI route to `ENDPOINTS` in `scripts/check-api-schema.sh`
   (`TestDriftCheckCoversAllEndpoints` fails otherwise)
3. Run `go test ./internal/...`
4. Regenerate `docs/api-snapshot.json`: delete it and run `scripts/check-api-schema.sh`
5. If `internal/compare` should use the new data, extend `loadTS` and the matching area
