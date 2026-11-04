# Format-1 startup upgrade acceptance

Implemented against `origin/main` commit
`e753f52fafd38437d21c45f949c9765ec25f3bf3`. The unchanged beta.21 fixture was built
from `eff9b4d1ef0d072016796e9fa5d20849e93a0bdd` in a clean detached worktree.
No production host was accessed or modified for implementation or validation.

The server preserves a verified independent cold backup before opening a
writable database, converts User/Channel metadata in crash-atomic bounded batches,
retains legacy UID bans through snapshot and TLV log recovery, and publishes
format 2 only after conversion verification and close. Current metadata readers
remain strict. See the [operator procedure](../development/DATA_FORMAT_UPGRADE.md).

## Process-level evidence

All cases use real beta.21 and candidate server processes on macOS/arm64, with
three nodes, 256 Hash Slots, 12 physical Slots and three Channel replicas.
128 extra users ensure the first committed conversion batch is only a prefix.
Both original source binaries and candidate binaries are identified by SHA-256
in the receipts. Test-owned processes are joined before receipts are emitted.

| Candidate/case | Result | Full-history observations | Old snapshot installations | Observed Channel Leader |
| --- | --- | --- | --- | --- |
| Fault candidate, normal startup | Pass | 15 | 36 | 2 → 1 |
| Kill while copying backup | Pass | 15 | 36 | 2 → 1 |
| Kill after a durable metadata batch | Pass | 15 | 36 | 2 → 1 |
| Kill before format publication | Pass | 15 | 36 | 2 → 1 |
| Ordinary binary without gofail | Pass | 15 | 36 | See receipt |

The four-case fault run took 351.43 seconds; the separate ordinary binary run
took 96.05 seconds. Every case verifies all 105 original regular backup files,
full history/IDs/sequences/payloads at each ingress, original credential login,
legacy active bans including a policy-only user, credential initialization
without losing that ban, group bans, HTTP idempotency, cross-node WKProto
SEND/RECV, whole-cluster restart, subsequent appends, actual Channel Leader loss,
follower catch-up, and full-generation rollback booted with beta.21.
Fault cases also prove competing startup is refused. The pre-publication case
corrupts a backup file, observes startup refusal with the marker still at 1,
restores the original backup bytes and resumes successfully.

- [Four-case receipt](assets/2026-11-04-format-upgrade-faults.json)
- [Ordinary-server receipt](assets/2026-11-04-format-upgrade-server.json)
- [Scenario and repeatable command](../../test/e2e/migration/format_upgrade/AGENTS.md)

## Related validation

Passed the complete default tests for `pkg/db/...`, `pkg/slot/fsm`,
`internal/app`, `cmd/wukongim`; the integration tier for `pkg/db/meta`,
`pkg/slot/fsm`, `pkg/slot/multiraft`; and the named `flow-doc-contracts`,
`go-format`, `go-vet`, `go-mod-tidy` and `docs` checks. FLOW validation reports
only existing advisory size warnings. The full repository-wide E2E matrix was
not run; this receipt qualifies the explicit upgrade scenario above.

The E2E suite first reproduced the original three-node unsupported-format error
with the unchanged candidate before implementing migration. A subsequent
failure caught legacy Raft replay dropping the projected UID ban; the current
recovery translation fixes that observed regression.

Only coordinated cold upgrades are supported. Rollback restores the complete
stopped generation and loses later accepted writes. Historical binaries must
not open partially converted live directories. Linux/amd64 cross-compilation also passed; these receipts are not Linux
runtime or production-data qualification. This change does not publish a release or deploy a server.
