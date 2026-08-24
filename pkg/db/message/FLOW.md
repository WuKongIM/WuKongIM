---
scope: package
summary: Stores Channel message logs, indexes, checkpoints, retention state, snapshots, and compatibility leases on the shared DB engine.
---

# Message Database Flow

## Responsibility

`pkg/db/message` owns node-local Channel log persistence on the shared
`pkg/db/internal` engine. It provides canonical Channel leases, atomic append
and follower apply, secondary indexes, checkpoints and history, logical and
physical retention, inspection, and portable backup/restore snapshots.

The compatibility surface maps `pkg/channel` records and offsets to this typed
storage core without transferring shared-engine ownership.

## Boundaries

- Pebble-specific code stays under `pkg/db/internal`; this package must not
  import Pebble directly.
- `MessageDB` owns one registry and physical engine. Each `Channel` or
  `ForChannel` call returns an independently closable lease over a shared
  canonical entry.
- Channel quorum and visibility policy remain in `pkg/channel`; this package
  persists caller-supplied records, progress, and retention state atomically.
- Schema changes follow the parent storage compatibility contract.

## Main Flows

1. Acquiring a Channel reuses one canonical entry; append and follower apply
   transfer its locks/pins to terminal commit ownership, validate sequences and
   duplicates, synchronously commit compatible batches, then publish all rows,
   indexes, checkpoints, history, and frontiers atomically.
   Exact quorum proposals persist versioned authority, command, range,
   predecessor, entry identities, and paired command/range indexes in that same
   synchronous commit; replica HW may advance atomically with its proposal.
2. Reads scan complete primary rows or verified typed indexes, recover LEO
   lazily after reopen/reclamation, and use bounded durable verification for
   idempotency and newest-message lookup.
3. Snapshot, backup, restore, truncation, retention, and close stream or mutate
   bounded batches while keeping rows, indexes, catalog, system state, leases,
   and physical engine ownership consistent.

## Invariants and Failure Semantics

- Sequences are contiguous and monotonic. A durable append updates its primary
  row, global message-ID index, idempotency/client index, sender index, and
  catalog as one atomic unit where applicable.
- Server-allocation proof may skip only existing message-ID reads. In-batch
  duplicate IDs and durable sender/client idempotency remain mandatory.
- Exact retries return only durable, already-durable, definitely-not-written,
  conflict, or outcome-unknown. Durable indexes remain authoritative across
  cache eviction, prefix retention, and reopen; incomplete manifests, chains,
  overlaps, or checkpoints above LEO are corruption.
- Idempotency filter negatives may avoid a read; possible hits always verify
  durable index and message data. Saturation can increase reads, never admit a
  duplicate.
- Caller cancellation stops waiting but cannot release commit-owned locks or
  pins before build, physical commit, publish, or terminal shutdown.
- Retention and truncation remove primary and secondary rows together. Logical
  retention preserves canonical lookup state until physical deletion.
  Suffix cuts never split proposals; recovery replacement is fenced by the
  inspected frontier and atomically replaces complete verified proposal pages.
- Queue-depth publication is monotonic through grouped collection and terminal
  zero. Backup includes committed proposal/entry identities and excludes the
  uncommitted suffix above the selected HW.
- Checkpoint updates are serialized, initialize an explicit zero, never regress
  HW, and preserve epoch and log-start fields.
- Close rejects new work, drains admitted operations and pins, reclaims entries,
  and closes the physical engine exactly once. One lease cannot close another.
- Backup count and content come from one pinned view; restore is exact-retry
  idempotent, conflicts with different state, and cleans partial rows in bounded
  batches before retry.

## Read First

- [Database lifecycle](db.go)
- [Channel lease](channel_log.go)
- [Atomic append](append.go)
- [Secondary indexes](indexes.go)
- [Snapshot state](snapshot.go)

## Update Triggers

Update this file when durable rows or indexes change, lease/registry ownership
changes, commit locking changes, checkpoint or retention semantics change,
backup/restore coverage changes, or the Channel compatibility contract changes.
