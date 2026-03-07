# CI Rebuild & Confluence Migration Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Replace the monolithic `ci.yml` with 5 purpose-built workflows (security, build, release, cbom, commit-signature), produce Linux + Windows binaries, and consolidate all documentation into the HB Confluence space (deleting the wrong AIAI pages).

**Architecture:** Separate GitHub Actions workflow files per concern (mirroring the `kAYd9iN/Aiai` repo pattern). Pinned SHA hashes on all `actions/*` references. Build matrix produces `linux/amd64`, `linux/arm64`, `windows/amd64` binaries. `version` injected at build time via ldflags from the git tag.

**Tech Stack:** Go 1.26, GitHub Actions, `gh` CLI (built into runners), cdxgen (Node.js), Confluence MCP, GitHub MCP.

**Note on execution:** All file operations via `mcp__plugin_github_github__*` tools. No local git. CI validates on every push to `main`.

**Pinned SHA references (from kAYd9iN/Aiai — already vetted):**
- `actions/checkout` → `34e114876b0b11c390a56381ad16ebd13914f8d5`
- `actions/upload-artifact` → `ea165f8d65b6e75b540449e92b4886f43607fa02`
- `actions/setup-go@v5` → use `v5` tag and pin SHA after verifying: `ghcr.io/actions/setup-go` latest SHA

---

## Task 1: Make `version` injectable via ldflags

**Files:**
- Modify: `cmd/backup/main.go`

`version` is currently a `const`. `const` cannot be overridden by `-ldflags`. Change to `var`.

**Step 1: Read current main.go** (get SHA).

**Step 2: Replace the const block**

Change:
```go
const (
	version           = "1.0.0"
	holaspiritBaseURL = "https://app.holaspirit.com"
)
```

To:
```go
// version is set at build time via -ldflags "-X main.version=vX.Y.Z"
var version = "dev"

const holaspiritBaseURL = "https://app.holaspirit.com"
```

**Step 3: Commit**
Message: `build: make version injectable via ldflags`

---

## Task 2: Delete old `ci.yml`

**Files:**
- Delete: `.github/workflows/ci.yml` (blob SHA `7c73d169e2348b4c127e317cd47c936a75994da3` — verify before deleting, may have changed)

**Step 1: Read `.github/workflows/ci.yml`** to get current blob SHA.

**Step 2: Delete** via `mcp__plugin_github_github__delete_file`.

Message: `ci: remove monolithic ci.yml — replacing with focused workflows`

---

## Task 3: Create `security.yml`

**Files:**
- Create: `.github/workflows/security.yml`

Security workflow: module verification + vulnerability scan + SAST + tests. Runs on every push and PR. Mirrors Aiai's `security.yml` structure.

```yaml
name: security-and-quality

on:
  push:
  pull_request:

permissions:
  contents: read

jobs:
  checks:
    runs-on: ubuntu-latest
    steps:
      - name: Checkout
        uses: actions/checkout@34e114876b0b11c390a56381ad16ebd13914f8d5

      - name: Setup Go
        uses: actions/setup-go@v5
        with:
          go-version: '1.26'
          cache: true

      - name: Verify dependencies
        run: go mod verify

      - name: govulncheck
        run: go run golang.org/x/vuln/cmd/govulncheck@latest ./...

      - name: gosec
        run: go run github.com/securego/gosec/v2/cmd/gosec@latest -quiet ./...

      - name: Unit tests (race detector)
        run: go test -race -cover ./...
```

Commit message: `ci: add security-and-quality workflow`

---

## Task 4: Create `build.yml`

**Files:**
- Create: `.github/workflows/build.yml`

Matrix build for Linux and Windows. Runs on every push to `main` and on tags. Artifacts uploaded per platform.

```yaml
name: build

on:
  push:
    branches: [main]
    tags: ['v*']
  pull_request:
    branches: [main]

permissions:
  contents: read

jobs:
  build:
    strategy:
      matrix:
        include:
          - goos: linux
            goarch: amd64
            artifact: backup-linux-amd64
          - goos: linux
            goarch: arm64
            artifact: backup-linux-arm64
          - goos: windows
            goarch: amd64
            artifact: backup-windows-amd64.exe
    runs-on: ubuntu-latest
    steps:
      - name: Checkout
        uses: actions/checkout@34e114876b0b11c390a56381ad16ebd13914f8d5

      - name: Setup Go
        uses: actions/setup-go@v5
        with:
          go-version: '1.26'
          cache: true

      - name: Build
        env:
          GOOS: ${{ matrix.goos }}
          GOARCH: ${{ matrix.goarch }}
        run: |
          VERSION="${GITHUB_REF_NAME:-dev}"
          go build \
            -mod=vendor \
            -ldflags="-s -w -X main.version=${VERSION}" \
            -o ${{ matrix.artifact }} \
            ./cmd/backup/

      - name: Upload artifact
        uses: actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02
        with:
          name: ${{ matrix.artifact }}
          path: ${{ matrix.artifact }}
          retention-days: 30
```

Commit message: `ci: add matrix build workflow (linux/amd64, linux/arm64, windows/amd64)`

---

## Task 5: Create `release.yml`

**Files:**
- Create: `.github/workflows/release.yml`

Triggered on signed `v*` tags. Builds all binaries, generates SHA256 checksums, creates a GitHub Release with all assets. Includes private-repo SLSA provenance (mirroring Aiai's fallback approach).

```yaml
name: release

on:
  push:
    tags:
      - 'v*'

permissions:
  contents: write
  actions: read
  id-token: write
  attestations: write

jobs:
  build:
    name: Build ${{ matrix.artifact }}
    strategy:
      matrix:
        include:
          - goos: linux
            goarch: amd64
            artifact: backup-linux-amd64
          - goos: linux
            goarch: arm64
            artifact: backup-linux-arm64
          - goos: windows
            goarch: amd64
            artifact: backup-windows-amd64.exe
    runs-on: ubuntu-latest
    steps:
      - name: Checkout
        uses: actions/checkout@34e114876b0b11c390a56381ad16ebd13914f8d5

      - name: Setup Go
        uses: actions/setup-go@v5
        with:
          go-version: '1.26'
          cache: true

      - name: Build
        env:
          GOOS: ${{ matrix.goos }}
          GOARCH: ${{ matrix.goarch }}
        run: |
          go build \
            -mod=vendor \
            -ldflags="-s -w -X main.version=${{ github.ref_name }}" \
            -o ${{ matrix.artifact }} \
            ./cmd/backup/

      - name: Upload artifact
        uses: actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02
        with:
          name: ${{ matrix.artifact }}
          path: ${{ matrix.artifact }}

  release:
    name: Create GitHub Release
    needs: build
    runs-on: ubuntu-latest
    steps:
      - name: Checkout
        uses: actions/checkout@34e114876b0b11c390a56381ad16ebd13914f8d5

      - name: Download all artifacts
        uses: actions/download-artifact@v4
        with:
          merge-multiple: true
          path: dist/

      - name: Generate SHA256 checksums
        run: |
          cd dist/
          sha256sum backup-linux-amd64 backup-linux-arm64 backup-windows-amd64.exe > SHA256SUMS
          cat SHA256SUMS

      - name: Generate provenance metadata (private-repo fallback)
        run: |
          cat > dist/provenance.json <<EOF
          {
            "version": "${{ github.ref_name }}",
            "commit": "${{ github.sha }}",
            "workflow": "${{ github.workflow }}",
            "repository": "${{ github.repository }}",
            "run_id": "${{ github.run_id }}",
            "built_at": "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
          }
          EOF

      - name: Create GitHub Release
        env:
          GH_TOKEN: ${{ github.token }}
        run: |
          gh release create "${{ github.ref_name }}" \
            --generate-notes \
            --title "holaspirit-backup ${{ github.ref_name }}" \
            dist/backup-linux-amd64 \
            dist/backup-linux-arm64 \
            dist/backup-windows-amd64.exe \
            dist/SHA256SUMS \
            dist/provenance.json
```

Commit message: `ci: add release workflow with multi-platform binaries and SHA256 checksums`

---

## Task 6: Create `cbom.yml`

**Files:**
- Create: `.github/workflows/cbom.yml`

Separate CBOM workflow mirroring Aiai. Uses cdxgen with optional OpenSSL signing via secret.

```yaml
name: cbom

on:
  push:
  pull_request:
  workflow_dispatch:

permissions:
  contents: read

jobs:
  generate-cbom:
    runs-on: ubuntu-latest
    steps:
      - name: Checkout
        uses: actions/checkout@34e114876b0b11c390a56381ad16ebd13914f8d5

      - name: Setup Go
        uses: actions/setup-go@v5
        with:
          go-version: '1.26'
          cache: true

      - name: Generate CBOM
        run: |
          npx --yes @cyclonedx/cdxgen@latest \
            --type cryptography \
            --output cbom.cdx.json \
            .

      - name: Optional sign CBOM
        env:
          CBOM_SIGNING_KEY_PEM: ${{ secrets.CBOM_SIGNING_KEY_PEM }}
        run: |
          set -euo pipefail
          if [ -z "${CBOM_SIGNING_KEY_PEM:-}" ]; then
            echo "No CBOM_SIGNING_KEY_PEM configured; unsigned CBOM artifact only."
            exit 0
          fi
          printf '%s\n' "${CBOM_SIGNING_KEY_PEM}" > /tmp/cbom-key.pem
          if ! openssl dgst -sha256 -sign /tmp/cbom-key.pem -out cbom.cdx.json.sig cbom.cdx.json; then
            echo "Signing failed. Continuing with unsigned artifact."
            rm -f cbom.cdx.json.sig
          fi
          rm -f /tmp/cbom-key.pem

      - name: Upload CBOM artifacts
        uses: actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02
        with:
          name: cbom
          path: |
            cbom.cdx.json
            cbom.cdx.json.sig
          retention-days: 365
```

Commit message: `ci: add separate cbom workflow with optional signing (mirrors Aiai)`

---

## Task 7: Create `commit-signature.yml` and helper script

**Files:**
- Create: `.github/workflows/commit-signature.yml`
- Create: `scripts/verify_signed_commits.sh`

Port directly from Aiai. Verifies that commits are GPG-signed. Gracefully degrades if `COMMIT_SIGNING_PUBLIC_KEY` secret is not configured.

**Step 1: Create `scripts/verify_signed_commits.sh`**

```bash
#!/usr/bin/env bash
set -euo pipefail

BASE="${1:-}"
HEAD="${2:-HEAD}"

if [ -z "$BASE" ] || [ "$BASE" = "0000000000000000000000000000000000000000" ]; then
  COMMITS=$(git rev-list "$HEAD")
else
  COMMITS=$(git rev-list "${BASE}..${HEAD}")
fi

if [ -z "$COMMITS" ]; then
  echo "No commits to verify."
  exit 0
fi

FAILED=0
for c in $COMMITS; do
  STATUS=$(git log --format="%G?" -1 "$c")
  case "$STATUS" in
    G|U)
      echo "OK: $c (signed)"
      ;;
    N)
      echo "FAIL: $c has no signature"
      FAILED=1
      ;;
    B)
      echo "FAIL: $c has a bad signature"
      FAILED=1
      ;;
    *)
      echo "WARN: $c signature status unknown ($STATUS)"
      ;;
  esac
done

if [ "$FAILED" -eq 1 ]; then
  echo ""
  echo "One or more commits are unsigned. All commits to main must be GPG-signed."
  exit 1
fi
```

**Step 2: Create `.github/workflows/commit-signature.yml`** (port from Aiai):

```yaml
name: commit-signature-verification

on:
  push:
    branches: [main]
  pull_request:
  workflow_dispatch:
    inputs:
      base:
        description: "Base commit SHA (optional)"
        required: false
        type: string
      head:
        description: "Head commit SHA (optional)"
        required: false
        type: string

permissions:
  contents: read

jobs:
  verify-commit-signatures:
    runs-on: ubuntu-latest
    steps:
      - name: Checkout
        uses: actions/checkout@34e114876b0b11c390a56381ad16ebd13914f8d5
        with:
          fetch-depth: 0

      - name: Determine commit range
        id: range
        run: |
          set -euo pipefail
          if [ "${GITHUB_EVENT_NAME}" = "pull_request" ]; then
            BASE="$(jq -r '.pull_request.base.sha' "$GITHUB_EVENT_PATH")"
            HEAD="$(jq -r '.pull_request.head.sha' "$GITHUB_EVENT_PATH")"
          elif [ "${GITHUB_EVENT_NAME}" = "workflow_dispatch" ]; then
            BASE="${{ github.event.inputs.base }}"
            HEAD="${{ github.event.inputs.head }}"
            if [ -z "$HEAD" ]; then
              HEAD="$(git rev-parse HEAD)"
            fi
            if [ -z "$BASE" ]; then
              BASE="$(git rev-parse "${HEAD}^" 2>/dev/null || echo 0000000000000000000000000000000000000000)"
            fi
          else
            BASE="$(jq -r '.before' "$GITHUB_EVENT_PATH")"
            HEAD="$(jq -r '.after' "$GITHUB_EVENT_PATH")"
          fi
          echo "base=$BASE" >> "$GITHUB_OUTPUT"
          echo "head=$HEAD" >> "$GITHUB_OUTPUT"

      - name: Verify commit signatures
        run: |
          chmod +x ./scripts/verify_signed_commits.sh
          ./scripts/verify_signed_commits.sh \
            "${{ steps.range.outputs.base }}" \
            "${{ steps.range.outputs.head }}"

      - name: Optional cryptographic verification with trusted key
        env:
          COMMIT_SIGNING_PUBLIC_KEY: ${{ secrets.COMMIT_SIGNING_PUBLIC_KEY }}
        run: |
          set -euo pipefail
          if [ -z "${COMMIT_SIGNING_PUBLIC_KEY:-}" ]; then
            echo "No COMMIT_SIGNING_PUBLIC_KEY configured; structure-only verification active."
            exit 0
          fi
          printf '%s\n' "${COMMIT_SIGNING_PUBLIC_KEY}" | gpg --batch --import
          BASE="${{ steps.range.outputs.base }}"
          HEAD="${{ steps.range.outputs.head }}"
          if [ "$BASE" = "0000000000000000000000000000000000000000" ]; then
            COMMITS=$(git rev-list "$HEAD")
          else
            COMMITS=$(git rev-list "${BASE}..${HEAD}")
          fi
          for c in $COMMITS; do
            git verify-commit "$c"
          done
```

Commit messages (2 separate commits):
1. `scripts/verify_signed_commits.sh`: `ci: add commit signature verification helper script`
2. `commit-signature.yml`: `ci: add commit signature verification workflow (mirrors Aiai)`

---

## Task 8: Update HB Confluence — Sicherheitskonzept

**Page:** ID `2326555` in HB space.

Update to reflect security hardening additions:
- Add HMAC-SHA-256 manifest signing section
- Add GET-only constraint explanation
- Add path traversal prevention
- Add token-leak prevention (slog)
- Add CBOM section (CycloneDX, GitHub Actions artifact, optional OpenSSL signing)
- Update CI/CD snippet to show new workflow structure
- Keep all existing content that is still accurate

Read current page first, then update with `contentFormat: "markdown"`.
Version message: `Security hardening: HMAC, GET-only, CBOM, token-leak prevention`

---

## Task 9: Update HB Confluence — Design & Architektur + Betrieb & Installation

**Pages:** `2326535` (Design & Architektur), `2490371` (Betrieb & Installation)

**Design & Architektur:**
- Update worker pool from "concurrent goroutines" to "bounded pool 5 workers"
- Add `cmd/backup/verify.go` to project structure
- Remove any mention of async exports
- Add verify subcommand documentation

**Betrieb & Installation:**
- Add "Integrität prüfen" section: `backup.exe verify --dir <path>`, exit codes
- Update binary download section to reference `backup-linux-amd64`, `backup-linux-arm64`, `backup-windows-amd64.exe` from GitHub Releases
- Remove any async export mentions

Read each page first, then update.
Version message: `Update: multi-platform binaries, verify subcommand, remove async exports`

---

## Task 10: Delete AIAI Confluence pages

**Note:** The Confluence MCP does not expose a delete page API. The 5 AIAI pages must be deleted manually via the Confluence UI.

**Pages to delete (AIAI space — wrong location):**
1. "Holaspirit Backup Tool" (ID: 2949121) — and all children:
2. "Architektur" (ID: 2883588)
3. "Betrieb & Monitoring" (ID: 2785282)
4. "Disaster Recovery" (ID: 3014657)
5. "Setup & Installation" (ID: 2981889)

**Manual steps:**
1. Open https://ewigepluseins.atlassian.net/wiki/spaces/AIAI/pages/2949121
2. Delete the page tree (Holaspirit Backup Tool + all children)
3. Confirm in Confluence trash

---

## Summary

| Task | What | Status |
|---|---|---|
| 1 | `version` → var (ldflags) | pending |
| 2 | Delete old ci.yml | pending |
| 3 | Create security.yml | pending |
| 4 | Create build.yml (matrix) | pending |
| 5 | Create release.yml | pending |
| 6 | Create cbom.yml | pending |
| 7 | Create commit-signature.yml + script | pending |
| 8 | Update HB: Sicherheitskonzept | pending |
| 9 | Update HB: Design & Betrieb | pending |
| 10 | Delete AIAI pages (manual UI) | manual |
