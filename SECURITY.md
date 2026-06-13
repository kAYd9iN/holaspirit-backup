# Security Policy

## Supported Versions

Only the latest release receives security updates.

| Version | Security updates |
|---------|-----------------|
| Latest release | ✓ |
| Older releases | — |

## Reporting a Vulnerability

**Please do not report security vulnerabilities as public GitHub Issues.**

Use [GitHub Private Vulnerability Reporting](https://github.com/kAYd9iN/holaspirit-backup/security/advisories/new)
instead. This allows confidential coordination before public disclosure.

### Report contents

- Description of the vulnerability
- Steps to reproduce
- Potential impact
- Optional: suggested fix

### Response times

| Phase | Target |
|-------|--------|
| Acknowledgement | 5 business days |
| Initial assessment | 10 business days |
| Fix for critical issues | 30 days |
| Fix for medium/low issues | 90 days |

## Scope

**In scope:**
- Vulnerabilities in tool code (`cmd/`, `internal/`)
- Weaknesses in CI/CD workflows (`.github/workflows/`)
- Token / authentication vulnerabilities
- Supply-chain risks (dependencies, Actions)

**Out of scope:**
- Holaspirit API vulnerabilities → report directly to Holaspirit
- GitHub Actions platform vulnerabilities → report to GitHub
- Issues requiring physical access to the backup system

## Security Architecture

- **GET-only HTTP client** — no write access to Holaspirit possible (`Get()` only)
- **Explicit TLS policy** — minimum TLS 1.2, certificate verification cannot be
  silently disabled (`InsecureSkipVerify` hard-set to `false`)
- **Bearer token** — never logged or included in error messages
- **HMAC-SHA-256 manifest signature** (`backup-manifest.sig`) — detects tampering
- **File permissions** 0600 (files) / 0750 (directories)
- **Path-traversal & symlink protection** in the storage writer — output paths
  are sanitized, symlinks resolved, and containment is re-checked
- **Log-injection protection** — API-supplied values are sanitized before logging
- **Bounded memory** — per-response (100 MiB) and per-endpoint item caps
- **vendor/ checked in** — reproducible builds, no network required
- **License allowlist** — only permissive licenses (MIT, Apache-2.0, BSD, ISC)
  are accepted for dependencies, enforced in CI

For the full design see [docs/security-concept.md](docs/security-concept.md),
[docs/supply-chain.md](docs/supply-chain.md), and the trust boundary description
in [docs/trust-model.md](docs/trust-model.md).

## Trust Model (summary)

The backup output is **HMAC-verified, not sanitized**. The signature proves the
manifest was produced by someone who possessed the API token at backup time — it
does **not** guarantee that the backed-up JSON is free of adversarial content.
Tools that consume backups must treat field values as untrusted data (never pass
them unescaped to shells, SQL, or template engines). See
[docs/trust-model.md](docs/trust-model.md) for the full data-flow and trust
boundaries.

## Backup Data Security (encryption at rest)

Backup output is **not encrypted at rest**. The backup directory contains
plaintext JSON of all organizational data, which may include PII (member names,
emails, roles). The HMAC signature provides tamper detection only — **zero
confidentiality**.

**Operators are responsible for confidentiality.** Recommended mitigations:

1. Store backups on an encrypted volume (LUKS, FileVault, BitLocker).
2. Set restrictive filesystem permissions on the output directory (`chmod 700`).
3. Restrict access via OS-level access controls.
4. Encrypt with `age` or `gpg` before transferring backups off-site.

This is an accepted design decision: the tool keeps a single responsibility
(faithful backup + integrity), and delegates confidentiality to the storage
layer, which is the appropriate trust boundary for at-rest protection.

## Token Rotation

The HMAC-SHA-256 manifest signature key is derived from the Holaspirit API
token. **If the token is rotated, signatures of existing backups become
unverifiable** (a false-positive "tampered" result).

The token itself is never stored — only its derived HMAC key is used. To verify
an old backup, supply the token that was in use when it was created. If you
rotate tokens regularly and need long-term verifiability, re-sign archived
manifests after rotation, or record which token generation signed each backup.

## Backup Retention & Disposal

No retention policy is enforced by the tool — timestamped backup directories
accumulate indefinitely. Operators should:

1. Define a retention period appropriate for their compliance requirements
   (e.g. 90 days).
2. Securely delete backups beyond the retention period (`shred`/`wipe` on
   unencrypted media, or destroy the key for encrypted volumes).
3. Audit who has access to the backup storage location.

This satisfies GDPR storage-limitation expectations (Art. 5(1)(e)) at the
operational layer.

## Audit Trail

By default the manifest records only the timestamp, tool version, and
organization ID — host and user identifiers are **omitted** to minimize
information exposure. Operators who require an audit trail (who ran a backup,
from where, automated vs manual) can opt in with `--audit`, which records the
hostname, username, and a CI-detected automation flag in the manifest.

## Incident Response — compromised backup

If a backup containing organizational data is exposed (e.g. storage breach,
stolen device, misconfigured share), treat it as a **data breach**, not merely a
tool issue:

1. **Contain** — revoke access to the affected storage; identify which backup
   directories and which endpoints (`members`, `tensions`, `policies`, etc.)
   were exposed.
2. **Rotate credentials** — rotate the Holaspirit API token, and any secrets
   that may appear inside backed-up content (e.g. tokens written into tasks or
   policies).
3. **Assess scope** — the manifest lists every file and its hash; use it to
   enumerate exposed data. PII (member names, emails) is the highest-sensitivity
   class.
4. **Notify** — if PII was exposed and you are subject to GDPR, the supervisory
   authority must be notified within **72 hours** (Art. 33); affected data
   subjects may need to be informed (Art. 34). Notify Holaspirit if their data
   is involved.
5. **Review** — confirm the at-rest mitigations above were in place; adjust
   retention and access controls to reduce the exposed corpus next time.

## Organization ID disclosure

The organization ID is stored in the manifest and (after validation) logged.
It is **not a secret** — it grants no access without a valid API token — but it
is a stable identifier. It is intentionally not redacted, because it is required
to identify which organization a backup belongs to. Treat manifests with the
same care as the backup directory they describe.
