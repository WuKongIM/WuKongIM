# Native first-CI failure follow-up

Original head d79bff5219f5a69872bb1720cbbaa224b1c7b3eb, run 39578472435 attempt 1: candidate step failed at preflight with AssertionError, zero cases/commands. Keep original receipt/log. Linux exception detail was intentionally not emitted, so the exact Linux cause is not yet established.

The same long factory home (91 bytes) on macOS GnuPG 2.4.8 causes gpg-agent exit 2 with `socket name ... is too long`. The same agent options and only shorter temporary prefix yield agent ready. No production keys read or used. This supports shortening only the disposable harness path, followed by fresh-head Linux CI.

Failures to cover before editing:
- The real temporary prefix plus factory's longest home/socket path exceeds portable Unix-domain pathname budget.
- Source-fixture or test-key generation fails before invoke(), losing its non-sensitive phase.
- Changing diagnostics accidentally serializes child output, passphrase or private key content.

Keep exact 31 cases, strict negative reasons, permissions, tools, workload, production core and control bytes unchanged. Do not rerun the old head.
