# Trust Model

This document defines the trust boundaries for holaspirit-backup so that future
maintainers and anyone building restore/audit tooling on top of the backups have
a clear baseline.

## Data flow

```
Holaspirit API  ──TLS──▶  HTTP client (GET-only)  ──▶  JSON writer  ──▶  backup dir
                                                                            │
                                                              manifest signer (HMAC)
                                                                            │
                                                                  backup-manifest.{json,sig}
```

## Trust levels

| Input / component | Trusted? | Notes |
|-------------------|----------|-------|
| API token | Trusted secret | Loaded from OS credential store or env; never logged, never written to disk. |
| TLS connection to `app.holaspirit.com` | Trusted after verification | Min TLS 1.2, certificate verification enforced (cannot be disabled). |
| **API response content** | **Untrusted** | The JSON bodies are stored verbatim. A compromised or misbehaving API could return adversarial field values. The tool does not interpret or execute them. |
| `--output` path | Operator-controlled | Sanitized; symlinks resolved; writes are contained to the timestamped backup directory. |
| `--dir` path (verify) | Operator-controlled | Resolved to an absolute, symlink-free directory and required to exist before reading. |
| `.sig` file content | Untrusted | Decoded from hex and compared in constant time; malformed input yields a clear error, not a silent mismatch. |

## What the HMAC signature guarantees — and what it does NOT

The `backup-manifest.sig` HMAC proves:

- ✅ The manifest was produced by a party that possessed the API token at backup
  time.
- ✅ The manifest (and therefore the recorded file hashes) has not been altered
  since signing.

It does **not** prove:

- ❌ That the backup files contain benign content. The signature covers integrity
  and origin, not safety. Field values originate from the API and are untrusted.
- ❌ Authenticity relative to a separate signing identity. The key is derived from
  the API token, so anyone with the token can produce a valid signature (see
  [Token Rotation](../SECURITY.md#token-rotation)).

## Guidance for restore / consumer tooling

- **Do not** treat HMAC-verified data as safe to interpret. Verification proves
  integrity, not that a field is free of injection payloads.
- **Do not** pass backed-up field values unescaped into shells, SQL queries,
  template engines, or HTML.
- **Do** re-validate and escape every value at the point of use, exactly as you
  would for any external/untrusted input.
- **Do** verify the manifest signature before consuming a backup, and surface a
  clear failure if it does not verify.

## At-rest confidentiality

Confidentiality of the backup directory is **out of scope** for the tool and is
delegated to the storage layer (encrypted volumes, filesystem ACLs). See
[Backup Data Security](../SECURITY.md#backup-data-security-encryption-at-rest).
