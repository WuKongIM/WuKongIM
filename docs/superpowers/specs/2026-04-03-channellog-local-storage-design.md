# `pkg/storage/channellog` Local Storage Design

**Date:** 2026-04-03
**Status:** Proposed

## Goal

Add a real local message-storage implementation into `pkg/storage/channellog` without splitting a new top-level storage package and without turning `channellog` back into a single large mixed-responsibility file.

The V1 scope is intentionally narrow:

- add local persisted message-log storage
- keep the package root as `pkg/storage/channellog`
- keep `channellog` as the channel-oriented facade
- add V1 local read APIs:
  - single-message load by `messageSeq`
  - forward range load by `messageSeq`
  - backward range load by `messageSeq`
  - truncate-to-seq
- do not implement idempotency state storage in this task
- do not implement search or secondary indexes in this task

## Non-Goals

- creating a new `pkg/storage/msgdb` package
- introducing subpackages under `pkg/storage/channellog`
- making `messageSeq` the storage-layer source of truth
- reintroducing old `wkdb/message.go` style mixed message-table and secondary-index design
- implementing payload search, sender indexes, timestamp indexes, or channel-level last-seq caches

## Problem

Current `pkg/storage/channellog` already owns the channel-oriented semantics:

- `ChannelKey -> GroupKey` translation
- metadata compatibility checks
- delete fencing
- `messageSeq = committed offset + 1`
- channel-facing send/fetch/status APIs

But it does not yet own a concrete local persistent store. Existing tests use fakes for:

- `MessageLog`
- `ChannelStateStore`
- runtime group handles

That leaves a gap between the intended architecture and a real deployable storage path.

At the same time, introducing a separate `msgdb` package would add another public boundary and another naming layer even though the current decision is to keep storage in `channellog`.

## Decision

The repository will keep local message-log storage inside `pkg/storage/channellog` and organize it as a single package with strict file-level responsibility boundaries.

The package will contain three kinds of code:

1. channel-facing semantic facade code
2. local Pebble-backed storage code
3. ISR storage bridge code

These remain in the same Go package, but they are split by file and object boundaries.

## Package Shape

`pkg/storage/channellog` remains the only package that knows both:

- business channel identity
- local message-log storage layout

The package does **not** split into subpackages such as `local`, `bridge`, or `msgdb`.

Instead, the internal organization is:

- semantic facade files
  - `meta.go`
  - `send.go`
  - `fetch.go`
  - `apply.go`
- local storage files
  - `db.go`
  - `store.go`
  - `log_store.go`
  - `seq_read.go`
  - `checkpoint_store.go`
  - `history_store.go`
  - `snapshot_store.go`
- key and codec files
  - `store_keys.go`
  - `store_codec.go`
  - existing `codec.go` remains the message-record codec
- ISR bridge files
  - `isr_bridge.go`

This is a code-organization rule, not a new package boundary.

## Object Model

The local storage entry point follows the same general shape as `pkg/storage/metadb`:

```go
type DB struct { ... }

func Open(path string) (*DB, error)
func (db *DB) Close() error
func (db *DB) ForChannel(key ChannelKey) *Store
```

```go
type Store struct { ... }
```

`Store` is channel-scoped. It binds:

- `ChannelKey`
- derived `GroupKey`
- the underlying `*pebble.DB`

That means upper layers do not repeatedly derive storage prefixes or runtime keys for every operation.

## Source Of Truth

The storage-layer source of truth is:

```text
groupKey + offset
```

Not:

```text
channel + messageSeq
```

Rules:

- the persisted message log is ordered by ISR log offset
- `messageSeq` is interpreted as `offset + 1`
- committed visibility is derived from checkpoint `HW`
- no separate `lastMessageSeq` key is stored in V1

This keeps the new storage aligned with ISR semantics instead of drifting back toward the old `wkdb` model where message sequence became a duplicated storage truth.

## Storage Responsibilities

### `DB`

`DB` owns:

- Pebble lifecycle
- top-level helpers such as `Open`, `Close`, and basic batch helpers
- creation of channel-scoped `Store` handles

`DB` does not own channel semantic checks.

### `Store`

`Store` owns channel-local persisted data operations.

It will provide two categories of behavior:

1. local read and maintenance APIs
2. ISR bridge adapters

Recommended V1 local APIs:

```go
func (s *Store) LoadMsg(seq uint64) (ChannelMessage, error)
func (s *Store) LoadNextRangeMsgs(startSeq, endSeq uint64, limit int) ([]ChannelMessage, error)
func (s *Store) LoadPrevRangeMsgs(startSeq, endSeq uint64, limit int) ([]ChannelMessage, error)
func (s *Store) TruncateLogTo(seq uint64) error
```

These methods are intentionally on `Store`, not on `Cluster`, so the channel-facing facade does not become a mixed storage and replication object.

## File Responsibilities

### `db.go`

Defines:

- `DB`
- `Open`
- `Close`
- `ForChannel`

### `store.go`

Defines:

- `Store`
- common constructor and validation helpers
- shared access to channel key and derived group key

### `store_keys.go`

Defines all Pebble keyspaces for V1:

- message log entries
- checkpoint state
- epoch history
- snapshot payload

No semantic logic belongs here.

### `store_codec.go`

Defines value codecs for non-message-record storage:

- checkpoint payload
- epoch history payload
- snapshot metadata or wrapped payload values if needed

Existing `codec.go` remains focused on message-record encoding and payload hashing.

### `log_store.go`

Implements the offset-based local log:

- `Append`
- `Read`
- `LEO`
- `Truncate`
- `Sync`

This is the storage truth layer.

### `seq_read.go`

Implements the V1 `messageSeq` read surface on top of the offset log:

- `LoadMsg`
- `LoadNextRangeMsgs`
- `LoadPrevRangeMsgs`
- conversion between `seq` and `offset`
- committed-range fencing using checkpoint `HW`

### `checkpoint_store.go`

Implements checkpoint persistence:

- `Load`
- `Store`

Checkpoint fields must at least include:

- `Epoch`
- `LogStartOffset`
- `HW`

### `history_store.go`

Implements epoch history persistence:

- `Load`
- `Append`

### `snapshot_store.go`

Implements snapshot payload persistence and replacement.

V1 treats snapshot payload as opaque bytes.

### `isr_bridge.go`

Builds ISR-facing adapters from `Store` so one concrete store can satisfy:

- log store behavior
- checkpoint store behavior
- epoch history behavior
- snapshot applier behavior

The bridge file may import `pkg/replication/isr`. The Pebble storage files should stay focused on local storage semantics.

## Read Semantics

V1 local read APIs keep channel-facing `messageSeq` semantics but do not store `messageSeq` separately.

### Single Load

`LoadMsg(seq)` rules:

- `seq` must be greater than zero
- `offset = seq - 1`
- the message is visible only when `offset < checkpoint.HW`

### Forward Range Load

`LoadNextRangeMsgs(startSeq, endSeq, limit)` rules:

- `startSeq` is inclusive
- `endSeq == 0` means open-ended upper bound
- the effective readable upper bound is still capped by committed `HW`

### Backward Range Load

`LoadPrevRangeMsgs(startSeq, endSeq, limit)` rules:

- preserve the old semantic shape expected by upper layers
- compute the backward window in seq-space
- translate that to offset-space
- cap visibility by committed `HW`

### Truncate

`TruncateLogTo(seq)` keeps the old channel-facing meaning:

> keep messages with `messageSeq <= seq`, remove all later messages

Internal equivalent:

- truncate the offset log to `to = seq`
- clamp checkpoint `HW` to `<= seq`
- trim any epoch-history tail that now points beyond the new local end

## Checkpoint And Visibility Model

V1 does not store a separate `lastMessageSeq`.

Instead:

- last committed visible message sequence is `checkpoint.HW`
- next readable `messageSeq` after the committed range is `checkpoint.HW + 1`
- empty channel means `HW == 0`

This removes duplicated state and keeps the local store consistent with ISR recovery behavior.

## ISR Integration

The same channel-scoped `Store` instance should be usable both:

- by `channellog` local read paths
- by ISR replica construction

The intended wiring is:

1. open `DB`
2. call `db.ForChannel(key)` to get a `Store`
3. derive ISR adapters from that `Store`
4. pass those adapters into replica construction
5. continue using the same `Store` for channel-local read and truncate helpers

`Cluster` itself should not build Pebble keys and should not own raw Pebble dependencies.

## What Stays Out Of V1

The following stay out of scope:

- idempotency state persistence
- sender secondary indexes
- client message number secondary indexes
- timestamp indexes
- payload search
- global message-id lookup tables
- dedicated channel last-seq cache

Those can be added later if a real caller needs them.

## Rejected Alternatives

### Introduce `pkg/storage/msgdb`

Rejected for this task because the current choice is to keep the local message store inside `pkg/storage/channellog` and avoid another public storage package boundary.

### Add subpackages under `pkg/storage/channellog`

Rejected because the chosen direction is to keep one package and enforce boundaries by file and object responsibilities instead of additional package visibility rules.

### Make `messageSeq` the persisted primary ordering key

Rejected because it duplicates ISR log truth and weakens the invariant that `messageSeq` is derived from committed offset.

### Rebuild old `wkdb/message.go` semantics wholesale

Rejected because V1 only needs local log storage plus seq-based reads and truncate. Pulling search and index behavior into this task would widen scope without a current caller.

## Testing Requirements

V1 must add three layers of tests.

### 1. Local Pebble Storage Tests

Lock:

- `Open/Close/ForChannel`
- append then single load by seq
- append then forward range load by seq
- append then backward range load by seq
- truncate-to-seq
- checkpoint load/store
- epoch history load/append
- snapshot payload replace/read behavior

### 2. ISR Bridge Integration Tests

Lock:

- one `Store` instance can back replica log/checkpoint/history/snapshot interfaces
- append then checkpoint advancement survives reopen
- follower apply and recovery keep local log and checkpoint consistent
- snapshot install updates visible range and payload correctly

### 3. `channellog` Semantic Regression Tests

Keep current semantic tests and add real-store coverage for:

- committed messages only
- `messageSeq == offset + 1`
- seq-based loads still match checkpoint `HW`

## Migration Strategy

Recommended implementation order:

1. add `DB`, `Store`, keyspace definitions, and codecs
2. add local offset-log persistence
3. add checkpoint, history, and snapshot persistence
4. add ISR bridge adapters
5. add seq-based read and truncate helpers
6. wire real-store integration tests

This order keeps the storage truth layer stable before adding channel-facing seq helpers.
