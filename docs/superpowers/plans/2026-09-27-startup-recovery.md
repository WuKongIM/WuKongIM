# Bounded Slot startup recovery

This is the historical first-stage plan and measurement. The completed PR also
implements certified fast restart; see the [final follow-up plan](2026-09-27-startup-recovery-followup.md). Statements below about a separate, unimplemented
checkpoint describe the first stage only.

## Evidence and scope

At source `64f73d99b3b0cb8960d40825f76053f5a0000dbd`, a synthetic clean reopen
of three million User rows across 256 hash slots in one physical Slot loaded
a 226 MiB snapshot, sampled 1,379 MiB peak Go heap, allocated 2,950 MiB and
rewrote about 229 MiB into the metadata WAL. It took 875 ms on a 32 GiB macOS
machine. This confirms amplification, not the reported indefinite production
startup: the incident has no exact build, configuration, logs or profiles.

The current change bounds snapshot-install memory and makes recovery observable.
It does not skip snapshot recovery, change network snapshot wire formats, or
claim that full startup is independent of retained Raft-log size.

## Installation and admission

The durable Raft storage implements a narrow optional startup snapshot reader.
It pins the exact manifest against snapshot GC, authenticates each chunk and
the whole payload with a 64 KiB buffer, and opens at most one chunk file for
seekable reads. Existing byte-slice snapshots remain available for transport
and adapters that do not support streaming.

Only a compatible startup restorer under explicit node-wide startup admission
uses this reader. Live opens and maintenance reloads retain atomic restoration.
The portable snapshot is fully checksum/order/ownership validated before destructive writes. In one
synced metadata batch, installation deletes the owned ranges, stores a global
physical-Slot pending marker and resets the FSM applied watermark to zero.
Rows are installed in synced batches capped at 8 MiB or 65,536 records. One
record exceeding the batch target is allowed only within the existing format
limits. The final synced batch publishes the snapshot watermark and deletes
the pending marker atomically. An interruption restarts the whole installation;
there is no partial-batch resume protocol.

The runtime reserves the Slot identity before constructing/restoring it and
registers it only after successful installation. Duplicate opens cannot mutate
a registered FSM. Runtime close joins in-flight constructors. Product startup
keeps foreground admission closed; live placement and maintenance reload never
opt into streamed installation. Ordinary runtime snapshot installation remains
one atomic metadata batch. Reads of the durable watermark fail while a marker exists.

The original snapshot plus committed suffix remains authoritative. A completed
install still replays that suffix. System key 3 is outside hash-slot payloads;
old complete databases and snapshot encodings are unchanged. A rollback to an
older binary still follows its full-snapshot recovery path. Never manually
remove the pending marker to make an incomplete installation appear ready.

## Progress logs

INFO event `slot.recovery.progress` carries `nodeID`, `slotID`, `snapshotIndex`,
`stage`, `bytes`, `totalBytes`, `entries`, `totalEntries`, `elapsed`, and `percent`
when a denominator is known. Stage transitions are immediate; progress within
a stage is emitted at most once every five seconds. There is no ticker per
Slot and no record identifiers, credentials or bodies. A blocked operation
leaves its last stage visible; the logger does not pretend that bytes advanced.

Stages include `open`, `snapshot_verify`, `log_load`, `snapshot_restore`,
`snapshot_checksum`, `snapshot_validate`, `snapshot_prepare`, `snapshot_install`,
`snapshot_installed`, `log_replay`, and `complete`. Some stages are absent for
fresh Slots or compatibility adapters. Percent is stage-local, not overall
startup percent. Completion means the original committed suffix was durably
applied, not that Gateway admission is already open. Failures use ERROR with
the last progress counters.

## Verification

- Process-level E2E creates users/devices through Product HTTP, requests a
  snapshot through Manager compaction, writes credentials into the committed
  suffix, and authenticates both generations after repeated product restarts.
  The scenario exports a body-free JSON progress report.
- Subprocess integration abruptly exits after range deletion, an intermediate
  batch, and the final data batch before publication. Reopen rejects the pending
  watermark; full retry must reproduce the complete snapshot SHA-256.
- Focused contracts reject corruption before mutation, preserve unowned data,
  exercise chunk seeks, reject duplicate-open restoration, and verify progress
  throttling without sleeps.
- Compare the original three-million-row probe after changes, distinguishing
  sampled Go heap, cumulative allocations, elapsed time, and WAL input. A lower
  memory peak does not by itself prove lower latency on an unconstrained SSD.
- Run `flow-doc-contracts` through the canonical Review Agent policy catalog.

The managed app worktree is used instead of `.worktrees`: the tool owns
registration and recovery and does not expose a destination override.

## Measured result and completed checks

The same synthetic three-million-User-row probe used 256 hash slots, one
physical Slot, 14-character UIDs and 32-character tokens. It saved a real Raft
snapshot and cleanly reopened the actual metadata and Slot runtimes. There
were no Device rows or retained suffix in this memory experiment.

| Measurement | Original | Bounded startup recovery |
| --- | ---: | ---: |
| Snapshot payload | 226.02 MiB | 226.02 MiB |
| Sampled peak Go heap | 1,378.78 MiB | 137.61 MiB |
| Cumulative Go allocations during Slot open | 2,950.06 MiB | 493.50 MiB |
| Slot open duration | 875 ms | 1,471 ms |
| Metadata WAL input | 240,010,036 bytes | 240,010,663 bytes |

Peak heap fell about 90% and allocation volume about 83%. This is not an RSS
measurement, a cold-disk benchmark, or a full-server three-million-user E2E.
The additional verification passes and synced batches traded latency for
bounded memory on this 32 GiB macOS host: Slot open was about 68% slower.
The change still rewrites the snapshot, so it does not remove WAL traffic or
prove the reported production stall is resolved.

Completed validation:

```sh
GOWORK=off go test ./pkg/db/... ./pkg/raftlog/... ./pkg/slot/... ./pkg/cluster/... ./internal/app -count=1
GOWORK=off go test -race -tags=integration ./pkg/db/meta ./pkg/slot/multiraft ./pkg/raftlog -run 'Startup|RecoveryProgress|OpenSlotDoesNotStream|DuplicateOpen' -count=1 -timeout=3m
GOWORK=off WK_E2E_STARTUP_RECOVERY_REPORT=/tmp/startup-recovery.json go test -tags=e2e ./test/e2e/cluster/startup_recovery -count=1 -timeout=4m
```

All passed. The E2E covered one and twelve physical Slots, 256 hash slots,
two restarts each, snapshot/suffix credential authentication and progress
events. It emits `/tmp/startup-recovery.json` and
`/tmp/startup-recovery-12.json` with stage observations. The named
`flow-doc-contracts` check passed with existing repository length warnings.

## Separate follow-up: durable FSM checkpoint fast restart

Not implemented by this change. Reusing an existing FSM could remove the full
snapshot rewrite, but the current repository contract deliberately requires
snapshot restoration before suffix replay. A future proposal must first define
and prove a checkpoint certificate binding cluster/Slot identity, database
incarnation, applied index and term, membership/configuration boundary, and
completed-install state. Business mutations and their checkpoint watermark must
remain atomic; snapshot/log replacement and offline restore must invalidate old
certificates durably. Recovery must verify that the retained suffix is contiguous
through the committed boundary and that its membership transition history agrees.

A missing, mismatched or incomplete certificate falls back to the existing
verified snapshot path. Testing must cover crashes around each certificate
publication/invalidation, stale copied directories, imported generations,
configuration changes, log truncation and unsupported older metadata. This is
a separate recovery-protocol decision; comparing two applied indexes is not a
valid fast-path proof.
