# Backup Contracts Flow

`internal/contracts/backup` contains the entry-agnostic DTOs shared by backup
use cases, runtime, node RPC, infrastructure adapters, and app wiring. It does
not schedule work, access repositories, mutate Controller state, or contain
business policy.

## Scheduled State

`ScheduledState` is the bounded Controller-owned coordination record. It
contains:

- one revisioned `Plan`;
- at most one active full-backup job and one active restore job;
- bounded completed job history;
- the Manager session epoch used to invalidate pre-restore JWTs.

The plan selects one file or S3-compatible repository, one Cron or `@every`
schedule and time zone, retention count, per-node rate limit, workers per node,
and maximum duration. Repository credentials cross this package only as
encrypted values. Public projections use a credential-present boolean and
never expose ciphertext or plaintext secrets.

Each backup job contains exactly one bounded `SlotBackupProgress` entry for
every logical Hash Slot. Jobs are resumable and Controller-Leader-fenced by
owner node and term. A complete archive is independent; jobs never carry prior
archive watermarks or incremental cursors.

Each restore job records the selected complete archive, maintenance/switch
phase, and one bounded `RestoreSlotProgress` entry per logical Hash Slot.
Cancellation is valid only before switching begins.

## Node RPC

`SlotExportCommand` asks the current physical Slot leader to export one full
logical Hash Slot directly to the configured repository. The receipt returns
only bounded byte and record totals.

`MessageExportCommand` carries a bounded set of Channel authority fences for
one source node. The receiver exports complete committed message snapshots and
writes a bounded chunk index to the repository. Its fixed-size receipt returns
that index's key and digest plus count, total, and maximum-message-ID evidence;
message payloads and variable-length chunk lists do not traverse the
coordinator.

Repository probes exchange only a generated marker and its digest. Restore
RPCs carry exact archive and Slot identities, expected topology, repository
configuration, target activation, and bounded per-replica evidence. They do
not carry repository credentials back through Manager responses.

## Boundary Rules

- Repository artifacts and checksum formats belong to `pkg/backup`.
- Scheduling, overlap, retry, retention, and restore state transitions belong
  to `internal/usecase/backup`.
- Controller persistence belongs to the infrastructure state adapter.
- Node-local snapshot/export and staged storage switching belong to runtime,
  cluster, and infrastructure adapters.
- Manager request validation, permissions, reauthentication, and confirmation
  remain in `internal/access/manager`.
