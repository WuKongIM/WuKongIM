# Project hardening: implementation and acceptance

This work follows the approved project assessment plan. The starting source is
`0ca3eafafe2c404f1cae106a08bba29a28945bd9`. The task branch is
`codex/project-hardening`; development uses `.worktrees/project-hardening`.
The source AGENTS/FLOW SHA-256 inventory was frozen before edits in the local
`/tmp/wukongim-hardening-context.json` audit. This report records evidence, not
release authority or production-capacity qualification.

## Scope and completion conditions

| Work | Required evidence | Current status |
| --- | --- | --- |
| Slot close/apply test | Original failure, repeated race regression, complete package race suite, final unit gate | Complete; all required local gates passed |
| Backup RPC credential isolation | No ciphertext on all four request wires, exact target-local resolution, rotation/refusal, actual object-store I/O and process-level backup/restore | Complete locally; protocol, S3 protocol integration, race and both process scenarios passed |
| Faster migration feedback | Same corruption/topology assertions, bounded fixture copies, comparable measurements, complete migration suite | Complete package passed in 126.192 seconds versus 173.030 seconds before |
| Current-source performance | Fixed workloads, scheduled-arrival latency/error/drop/resource evidence and profiles before tuning | Local preflight rejected insufficient disk reserve; multi-host environment not yet supplied |
| Upgrade/recovery and aged-data stability | Exact source/binary/config identity, fault/restart/restore/rollback and bounded sustained workload receipts | Backup fault/rejoin/restore and all five original-v2 migration topologies passed; long workload unavailable |
| Maintainability and issue closure | Narrow ownership, updated navigation and proven status changes | Backup resolution isolated behind one target-local seam; stale delivery symbol note verified obsolete; unrelated findings left open |

## Slot test

The isolated command was
`GOWORK=off go test ./pkg/slot/multiraft -run '^TestCloseSlotDuringApplyEnqueueHandoffRejectsTaskAndRetiresQueue$' -count=100`.
It reproduced `slotFor() = nil` once. `CloseSlot` legitimately removes the Slot
from the runtime map before waiting for apply completion. The test captured its
reference only after launching close, so the apply hook did not protect that
lookup. Capturing the exact Slot before close preserves the original rejected
proposal, retired queue, and reopen assertions.

Commit `312692915` passed 100 repetitions with `-race` (5.164 seconds) and the
complete `pkg/slot/multiraft` race suite (9.046 seconds). No product scheduling,
shutdown, or timeout policy changed.

## Backup repository references

Commit `2fd58e9a1` replaces all four request encodings with explicit version-2
DTOs. Durable `StoreConfig` remains unchanged; `StoreReference` contains only
repository identity and credential revision. The app injects its existing
Controller state adapter as the resolver. Each receiving node rejects missing,
rotated or mismatched state before effects and opens storage with its own detached
encrypted credential. It does not fetch credentials over RPC.

Version-1 requests, injected ciphertext fields and credential-bearing endpoint
URLs fail closed. Response formats and service IDs are unchanged. Upgrade all
backup participants together; no fallback transmits old credentials.

The wire-capture regression failed for Slot export, message export, repository
probe and restore before implementation. Tests now cover all four transport
paths, exact local credentials, sender ownership, absent resolution, cancellation,
identity drift, credential rotation, legacy requests and endpoint secrets.
The app integration uses real RPC codecs, Controller-store resolution,
credential decryption, the production S3 client and an HTTP S3 protocol fixture.
It proves signed marker reads, receipt writes, rejection before object I/O after
rotation, and success with the matching new revision. This fixture does not
claim a live Alibaba OSS or external S3 service qualification.

Relevant node, backup and app unit tests passed. Focused race validation with the
integration build tag passed for all three packages. `flow-doc-contracts` passed
with navigation length warnings and no invalid FLOW files.

## Backup process acceptance and readiness repair

The first process run used clean binary source `2fd58e9a1` (VCS modified=false).
The single-node scenario failed before backup at an unauthenticated WKProto
readiness handshake (`ReasonAuthFail`). The three-node scenario waited on the
same old readiness implementation; its exact three child processes were stopped
with TERM rather than waiting out the 14-minute scenario timeout. Both outcomes
remain non-passing evidence.

Commit `fe7712be3` makes managed-process readiness register a dedicated device
token through Product HTTP, then perform an authenticated WKProto handshake.
A real 256-Hash-Slot single-node cluster passes the regression; a wrong token is
still rejected. Child-exit detection also passes. Generic address-only probes
retain their original no-credential contract. No product authentication default
was weakened.

The complete E2E suite helper package passed in 6.142 seconds with the clean
`2de1e9e2c` binary, including process lifecycle and authenticated readiness.

The real single-node cluster then passed full backup and exact point-in-time
business restoration in 149.013 seconds. The initial three-node run returned
`backup_repository_node_unreachable` for node 2 during repository testing,
before the backup job. The receiver intentionally requires target-visible plan
state; a coordinator-side save alone is not that visibility proof. The scenario
now checks the exact saved plan revision through every node's public Manager
dashboard, with a 30-second bound, before probing. This is an explicit setup
barrier, not a relaxed repository-success assertion or credential fallback.
The first failure alone does not establish whether mirror lag or another
transient node condition caused it; the failed run is retained separately.

With the explicit setup barrier in `d08ec51b6`, the three-node scenario passed
in 414.112 seconds. It stopped Controller Leader node 1 during the active
backup, published the resumed archive, rejoined that same node, restored every
current replica and verified exact point-in-time messages through Product HTTP.
The intermediate public dashboard showed all 256 Hash Slots verified before
finalization. Its binary remained clean source `2de1e9e2c`.

## Migration fixture cost

A fresh whole-package run with JSON timing and CPU profiling passed in 173.030
seconds. The slowest matrix,
`TestPrepareRebuildsTransitionProofFromRawArchiveCommands`, took 46.840 seconds.
A separate block-profile run took 39.494 seconds and attributed about 30.50
seconds to test fixture copying through one synchronous `Spool.Put` per row.
Idle background-goroutine wait totals are not interpreted as foreground cost.

Commit `2de1e9e2c` copies those exact fixture rows in batches of at most 256 rows
and 1 MiB, cloning iterator-owned bytes. An individually oversized row retains
the existing Spool byte guard. Every batch remains synchronously durable.
All four corruption modes, both historical layouts, and single-node/three-node
migration targets remain in the default test suite.

The same targeted block-profile command passed in 8.648 seconds, about 78%
less wall time than its 39.494-second baseline. Total foreground Spool.Put wait
fell from about 31.48 to 1.18 seconds. These local test measurements are not
server throughput or production latency claims.

The complete post-change migration package, using the same JSON/CPU-profile
command, passed in 126.192 seconds, down from 173.030 seconds (about 27%).

## Performance environment preflight

The committed local-baseline helper ran from a clean checkout at `2de1e9e2c`:
`GOWORK=off bash scripts/run-wukongim-three-node-chat-lifecycle-local-baseline.sh --run-dir /tmp/wukongim-hardening-local-baseline`.
It exited 2 before building binaries or launching workload processes. Its
`local-baseline.json` records `outcome=storage_confounded` and
`reason=filesystem_free_below_10_percent`. The measured free-space percentage
was 2, below the fixed 10-percent baseline gate. This is an infrastructure
preflight rejection, not a failed product capacity result or a passing baseline.

The full source-specific workload, 24/72-hour aged-data qualification and
post-aging capacity search remain unverified. No alternate threshold, existing
user data deletion or paid cloud procurement was used to bypass this condition.

## Repository validation

Before the additional migration checkpoint repair, the complete default Go gate
passed: 212 packages succeeded and 13 had no test files. It used the required
explicit roots (`cmd`, `internal`, `pkg`, `scripts`,
`docker`), `GOWORK=off`, and `-count=1`. The migration package took 209.862
seconds in this loaded full-suite run; that duration is not used for the
isolated before/after optimization comparison.

## Migration workspace reuse repair

The original v2 ordinary-history process matrix initially failed all five
topologies during import with `migration spool durable key conflict`. A
minimized CLI regression reproduced the same failure in 0.55 seconds of test
execution. Source preparation writes an export-sealed `workflow/PREPARED`
receipt; archive reconstruction wrote a different, unsealed result to the same
immutable key. The durable conflict guard correctly refused the overwrite.

Commit `3c18c52bc` stores the independently rebuilt archive receipt under
`workflow/ARCHIVE_PREPARED`, retaining the original sealed source checkpoint.
The CLI regression now covers fresh and reused workspaces, removed source
directories, repeated import, immutable creator identity, separate exact
checkpoint contents, independent verification and modified-target refusal.
The focused CLI/archive tests passed (66.284 seconds). The unchanged original
process matrix then passed all five topologies (1→1, 1→3, 3→1, 3→3, 3→5) in
161.073 seconds. It exercised source prepare/export, two imports, independent
verification, original history and credentials, appends, idempotency, full
restart and applicable Channel Leader failover. Both the CLI and service were
built in a clean checkout of `3c18c52bc`; the service VCS stamp is modified=false.
Archive semantic validation and the Spool's conflict guard were not bypassed.

The subsequent explicit-root default run passed 211 packages and failed only
the migration package: the opaque-identity regression still read the old source
checkpoint name from an archive-only workspace. Commit `35d0bdd5e` changes that
lookup to `workflow/ARCHIVE_PREPARED`; the field-integrity, altered-identity and
native-recovery assertions remain. The complete migration package subsequently
passed in 121.313 seconds. Together with the other 211 passing packages, this
completes default-package coverage after the product fix. The failed all-root
invocation remains recorded as failed, rather than being relabeled after a
focused retry.

The explicit-root `go vet` gate passed again after the checkpoint fix. All 18
changed Go files are formatted, and `git diff --check` passed. The navigation
check passed with 81 valid FLOW files and nine length warnings. No protected
Agent policy, automation or release workflow was changed.

## Pending delivery checks

- Local implementation, related race checks, default-package coverage, Vet,
  format, navigation, backup restore and the migration process matrix are
  complete. All acceptance service child processes exited.
- Performance, long stability and aged-data capacity still require a suitable
  identified environment. The current machine failed the fixed disk reserve
  preflight, and no existing test cluster has been supplied.
- The full approved plan remains incomplete while that acceptance evidence is
  missing. No release, deployment or production-capacity claim has been made.

Machine-readable outcomes, exact binary identities and local log checksums are
in [the evidence receipt](2026-09-21-project-hardening.json).
