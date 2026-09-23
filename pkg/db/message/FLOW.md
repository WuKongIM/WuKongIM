---
scope: package
summary: Stores Channel message logs, indexes, checkpoints, retention state, snapshots, and compatibility leases on the shared DB engine.
---

# Message Database Flow

## Responsibility

`pkg/db/message` persists node-local Channel logs on `pkg/db/internal`, with canonical leases, atomic append
and follower apply, secondary indexes, checkpoints and history, logical and
physical retention, inspection, and portable backup/restore snapshots.

Compatibility maps Channel records/offsets to this core without transferring engine ownership.

## Boundaries

- Pebble-specific code stays under `pkg/db/internal`; direct Pebble imports are forbidden.
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
   idempotency and newest-message lookup. Newest-first primary reads iterate
   natively in reverse and stop while scanning at `Limit` or `MaxBytes`. A
   single-row bounded read first uses the exact durable sequence when supplied;
   a missing row retains predecessor scanning, while corruption and I/O errors
   fail immediately. Unresolved or maximum sequence bounds keep the range scan. Reads
   must never materialize the complete Channel history before truncation.
   Catalog pages follow encoded key order; skip only the exact cursor key.
   Remote client-number lookups additionally cap inspected index entries and
   payload bytes, failing explicitly rather than returning partial matches.
3. Snapshot, backup, restore, truncation, retention, and close stream or mutate
   bounded batches while keeping rows, indexes, catalog, system state, leases,
   and physical engine ownership consistent.

## Invariants and Failure Semantics

- Offline helpers preserve optional publication column 21 and verify proposal
  formats 1–5. Format 2 binds Expire; format 3 also binds publication metadata;
  format 4 exclusively binds one canonical internal source activation record.
  Metadata uses compatibility record codec 2; native codec-1 bytes stay unchanged.
  Matched runtimes, tooling and full-generation rollback are required. Import adds no empty-key exception, uniqueness relaxation,
  or recovery path and rejects values the native runtime cannot represent.
  Inspection includes independently owned publication bytes only when present.
- A sparse SyncOnce ordinal index (ID 7, complete marker system ID 11) excludes
  internal records from badge rank queries. Existing primary rows are rebuilt in
  bounded batches before the marker is published; channel append ownership
  serializes writers and rank reads. A range count shares one bounded iterator
  across both ranks and proves an empty index once, preserving the retained
  ordinal baseline without caching unread results. Once the complete index is
  proven empty, a constant-size canonical/warm proof avoids repeated storage
  reads while preserving cancellation and engine lifecycle checks. SyncOnce
  staging invalidates it before commit; restore discard and backup-import
  generations fence active and retired entries. All append, replacement, truncate
  and retention paths maintain the index. Portable backups omit the marker and
  rebuild derived entries during import; raw snapshots preserve both together.
  Matched runtimes are required after publication; older writers cannot maintain
  this derived keyspace.
- Sequences are contiguous and monotonic. A durable append updates its primary
  row, global message-ID index, idempotency/client index, sender index, and
  catalog as one atomic unit where applicable.
- A 32,768-entry bounded warm cache retains LEO, idempotency membership, and the
  last committed exact proposal/entry identity across Channel lease reclamation.
  It also shares immutable encoded keys across matching identity generations;
  retained key backing arrays have a separate 16 MiB LRU bound. Reacquisition
  always creates a fresh mutable entry and independently closable lease.
  Fresh exact extensions with node-scoped message-ID allocation proof may
  validate that immutable tail in memory; restart, eviction, replay, recovery,
  suffix mutation, or an invalidated LEO falls back to durable validation.
- Server-allocation proof may skip only existing message-ID reads. In-batch
  duplicate IDs and durable sender/client idempotency remain mandatory.
- Exact retries return only durable, already-durable, definitely-not-written,
  conflict, or outcome-unknown. Durable indexes remain authoritative across
  cache eviction, prefix retention, and reopen; incomplete manifests, chains,
  overlaps, or checkpoints above LEO are corruption.
- Idempotency filter negatives may avoid a read; possible hits always verify
  durable index and message data. Saturation can increase reads, never admit a
  duplicate.
- Keyed Wills derive unique index 8 from publication metadata and UID; client
  index 3 preserves history lookup while native index 4 stays separate. Will
  uniqueness uses durable point proofs without the native negative filter.
- Caller cancellation stops waiting but cannot release commit-owned locks or
  pins before build, physical commit, publish, or terminal shutdown.
- Retention and truncation remove primary and secondary rows together. Logical
  retention preserves canonical lookup state until physical deletion.
  MQTT System 12 clamps physical trim at copied-through without holding logical
  visibility back. Its bounded original-source reader fences incarnation/HW and
  fails on gaps; local CAS applies a decision but proves no distributed authority.
  Format 4's System 13 protects the pending prefix; HW atomically creates System
  12. Duplicates keep the first boundary; only uncommitted suffixes can replace it.
  Backups skip pending controls and validate committed source/manifest pairs.
  Source/checkpoint reads pin activation/source/HW; admission requires a covered control.
  Replay table 2 atomically stores canonical content/counters; index 2 meters ranges.
  Preparation returns covered pages before extending, preserving short-page retries.
  Transfer requires committed log proofs plus an independent full-content digest;
  native hashes omit fields. Local copy/import never advances System 12 or proves quorum.
  Format-5 anchors journal controls in System 14; pinned source/latest reads optionally
  include exact command lookup, with one reverse seek and bounded proofs. Trims retain them; suffix replacement
  removes pending entries. Backups require matching journals and format versions.
  Anchor repair verifies local journals under append/checkpoint ownership; pinned exports must reach the exact endpoint.
  Repair planning verifies coverage/cursors, scans at most 64 journals and returns one bounded interval or explicit continuation.
  Trim/restart preserve exact retries; neither repair nor planning changes source release or HW.
  Suffix cuts never split proposals; recovery replacement is fenced by the
  inspected frontier and atomically replaces complete verified proposal pages.
- Queue-depth publication is monotonic through grouped collection and terminal
  zero. Backup includes committed proposal/entry identities and excludes the
  uncommitted suffix above the selected HW.
- Monotonic checkpoint updates preserve epoch/log-start fields. Protected sources
  require an intact explicit checkpoint on every load; missing or inconsistent
  evidence cannot be recreated by a writer. Raw setters also reject protected HW
  regression. Suffix cuts hold append then checkpoint locks through commit.
- Retention reads reuse immutable state in the bounded canonical/warm registry.
  A database-wide generation disables hits/fills during overlapping retention
  mutations, truncation, recovery replacement, discard and either backup import
  path, and invalidates before and after every attempted mutation. Ordinary
  append does not invalidate unchanged retention state; cancellation, close and
  durable decoding errors remain visible.
- Close rejects new work, drains admitted operations and pins, reclaims entries,
  and closes the physical engine exactly once. One lease cannot close another.
- Backup count and content come from one pinned view; restore is exact-retry
  idempotent, conflicts with different state, and cleans partial rows in bounded
  batches before retry.
  Replay-bearing backups use version 2; native-only streams stay version 1.
  Preflight validates replay chains and target coverage before writes; restore
  rebuilds metering without inserting shared copies into the global message index.

## Read First

- [Database lifecycle](db.go), [Channel lease](channel_log.go)
- [Atomic append](append.go), [Secondary indexes](indexes.go), [Snapshot state](snapshot.go)

## Update Triggers

Update this file when durable rows or indexes change, lease/registry ownership
changes, commit locking changes, checkpoint or retention semantics change,
backup/restore coverage changes, or the Channel compatibility contract changes.
