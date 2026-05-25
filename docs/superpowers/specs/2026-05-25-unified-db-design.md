# Unified DB Design

Date: 2026-05-25
Status: Draft for review
Owner: Codex

## 1. Context

WuKongIM currently has two Pebble-backed storage packages that solve similar
problems with separate implementations:

- `pkg/channel/store` stores channel-scoped message logs, checkpoints, epoch
  history, snapshots, retention state, and idempotency indexes.
- `pkg/slot/meta` stores hash-slot-scoped metadata tables such as users,
  channels, subscribers, channel runtime metadata, conversations, migration
  tasks, plugin bindings, and hash-slot migration rows.

Both packages already implement a small table layer on top of Pebble:

- Stable table, family, column, and index IDs.
- Ordered key encodings.
- Family value encodings.
- Secondary indexes.
- Atomic Pebble batches.
- Prefix scans and range deletes.
- Snapshot export/import.
- Hot-path caches.

The implementations differ in key layout, value checksums, batching, lock
scope, schema validation, and durability coordination. That duplication makes
the storage layer harder to read and harder to extend.

This design introduces a new `pkg/db` library as the single local storage
foundation for message data and slot metadata. The project has not shipped yet,
so this is a breaking refactor. Old APIs and old on-disk data do not need to be
preserved.

## 2. Goals

- Provide one storage library for channel messages and slot metadata.
- Keep Pebble as the fixed physical engine for the first version.
- Hide Pebble types behind `pkg/db/internal/*`.
- Preserve high-throughput message append and scan paths.
- Improve metadata write scalability by replacing the current global lock with
  hash-slot scoped locking.
- Make key layout, value envelopes, schema descriptors, batches, snapshots, and
  commit coordination consistent across domains.
- Expose strong typed APIs for message and metadata callers.
- Keep domain invariants close to the domain package, not in a generic ORM.
- Leave room for future storage domains, such as controller metadata or
  raftlog, without implementing them in the first version.

## 3. Non-Goals

- Compatibility with existing `pkg/channel/store` or `pkg/slot/meta` public
  APIs.
- Compatibility with existing Pebble data files.
- A generic SQL engine, ORM, or reflection-driven row mapper.
- A pluggable physical KV engine.
- Moving business rules into `internal/app` or a broad service layer.
- Covering `pkg/raftlog`, controller metadata, or channelv2-native storage in
  the first version.

## 4. Chosen Approach

Use a typed domain DB design.

`pkg/db` owns the shared storage foundation:

- Pebble engine wrapper.
- Key codec.
- Row codec and checksums.
- Schema descriptors and validation.
- Transactions and iterators.
- Commit coordinator.
- Shared errors and options.
- Small typed cache primitives.

`pkg/db/message` owns message-log semantics:

- Channel log append and apply-fetch.
- Message row indexes.
- LEO recovery and publication.
- Checkpoint, epoch history, snapshot, idempotency, and retention state.

`pkg/db/meta` owns metadata semantics:

- Hash-slot scoped typed table APIs.
- Meta batch and guard execution.
- Runtime metadata monotonic updates.
- Channel migration task guards.
- Hash-slot snapshots and range deletion.

This avoids turning all storage into a generic table framework while still
removing duplicated low-level storage code.

## 5. Package Layout

```text
pkg/db/
  db.go
  errors.go
  options.go
  FLOW.md

  internal/
    engine/
    keycodec/
    rowcodec/
    schema/
    commit/
    cache/

  message/
    db.go
    channel_log.go
    table_message.go
    checkpoint.go
    history.go
    snapshot.go
    retention.go
    idempotency.go
    FLOW.md

  meta/
    db.go
    shard.go
    batch.go
    table_user.go
    table_device.go
    table_channel.go
    table_subscriber.go
    table_runtime_meta.go
    table_conversation.go
    table_cmd_conversation.go
    table_plugin_binding.go
    table_channel_migration.go
    table_hashslot_migration.go
    snapshot.go
    FLOW.md
```

`pkg/db/internal/*` is not imported outside `pkg/db`.

The composition root can open both physical stores through one root helper:

```go
store, err := db.OpenNodeStore(db.NodeStoreOptions{
    MessagePath: messagePath,
    MetaPath:    metaPath,
})
msgDB := store.Messages()
metaDB := store.Meta()
```

The implementation should use two physical Pebble directories:

```text
data/
  message/
  meta/
```

Message and metadata workloads have different write amplification, cache,
compaction, scan, and fsync patterns. Keeping separate Pebble instances allows
domain-specific tuning without losing the benefit of one storage library.

## 6. Keyspace Design

All new keys use one ordered skeleton:

```text
[domain:1][partition-kind:1][partition-id:N][space:1][table-id:4][rest...]
```

Domains:

```text
0x01 message
0x02 meta
```

Partition kinds:

```text
0x00 global
0x01 channel
0x02 hash-slot
```

Spaces:

```text
0x10 row state
0x11 secondary index
0x12 system state
0x13 catalog
```

Message examples:

```text
message row:
[message][channel][channelKey][row][message_table][message_seq][family]

message index:
[message][channel][channelKey][index][message_table][index_id][index_columns...]

message system:
[message][channel][channelKey][system][message_table][system_id...]

message catalog:
[message][global][catalog][channelKey]
```

Meta examples:

```text
meta row:
[meta][hash-slot][uint16][row][user_table][uid][family]

meta index:
[meta][hash-slot][uint16][index][channel_table][index_id][channel_id][channel_type]

meta system:
[meta][hash-slot][uint16][system][system_id...]
```

Only `internal/keycodec` can build or decode keys. Domain packages call typed
helpers instead of constructing raw byte slices directly.

Key encoding rules:

- Strings use `uint16 length + bytes`.
- `uint64` uses big endian.
- Ordered `int64` uses sign-bit flip plus big endian.
- Descending integers are supported for recent-first active indexes.
- Family IDs use `uint16` big endian.
- Prefix spans use one shared `Span{Start, End}` type.
- Hot paths can append to caller-provided scratch buffers.

## 7. Value and Schema Design

All row and system values use one envelope:

```text
[version:1][codec:1][flags:1][checksum:4][payload...]
```

Codec values:

```text
0x01 column family delta codec
0x02 raw bytes
0x03 fixed binary
```

The checksum is CRC32C over the key, header with zero checksum, and payload.
All row and system values enable checksums. Index values also enable checksums
in the first version unless a later benchmark proves a specific exception is
needed.

The column family codec uses ordered column IDs:

```text
[column-delta + type tag][value]
```

Supported types:

- String
- Bytes
- Int64
- Uint64
- Bool
- Uint8

Rules:

- Encoders write columns in ascending ID order.
- Decoders skip unknown optional columns.
- Missing required columns return `ErrCorruptValue`.
- Type mismatches return `ErrCorruptValue`.
- Family layouts keep large payloads separate from frequently read headers.

Schema descriptors are not a runtime ORM. They validate storage declarations,
support debug tooling, and prevent ID collisions:

```go
type Table struct {
    ID       uint32
    Name     string
    Columns  []Column
    Families []Family
    Primary  Index
    Indexes   []Index
}

type Column struct {
    ID       uint16
    Name     string
    Type     Type
    Required bool
}

type Family struct {
    ID      uint16
    Name    string
    Columns []uint16
}

type Index struct {
    ID       uint16
    Name     string
    Unique   bool
    Columns  []uint16
    Covering []uint16
}
```

Each table still has a typed codec. For example:

```go
func encodeMessageHeader(row MessageRow, dst []byte) ([]byte, error)
func decodeMessageHeader(key, value []byte, row *MessageRow) error
```

## 8. Engine, Tx, and Durability

`internal/engine` wraps Pebble and exposes only storage primitives needed by
the domain packages:

```go
type Tx struct {
    // unexported
}

func (tx *Tx) Get(key Key) ([]byte, bool, error)
func (tx *Tx) Set(key Key, value []byte) error
func (tx *Tx) SetDeferred(keyLen, valueLen int, fill func(k, v []byte) error) error
func (tx *Tx) Delete(key Key) error
func (tx *Tx) DeleteRange(span Span) error
func (tx *Tx) NewIter(span Span, opts IterOptions) (*Iter, error)
```

`SetDeferred` is required for message append hot paths so the new store can
retain Pebble allocation optimizations.

Durability modes:

```go
type Durability uint8

const (
    NoSync Durability = iota
    Sync
    CoordinatedSync
)
```

Message defaults:

- Append: `CoordinatedSync`.
- ApplyFetch: `CoordinatedSync`.
- Checkpoint monotonic: coordinated checkpoint lane or `Sync`.
- Retention trim: `Sync` or a low-priority coordinator lane.
- Snapshot install: `Sync`.

Meta defaults:

- Raft apply batch: `Sync`.
- Snapshot import: `Sync`.
- Management writes: `Sync`.

## 9. Commit Coordinator

The current cross-channel fsync batching moves into `internal/commit` and
becomes domain-aware:

```go
type Lane struct {
    Domain   Domain
    Priority Priority
}

type Request struct {
    Lane      Lane
    Partition PartitionID
    Records   int
    Bytes     int
    Build     func(tx *Tx) error
    Publish   func() error
}
```

Initial lanes:

- `message-append`
- `message-checkpoint`
- `meta-apply` is optional and should remain direct `Sync` at first unless a
  benchmark proves batching is needed.

Rules:

- Same-channel micro-batching happens before cross-channel group commit.
- `Publish` runs only after the Pebble commit succeeds.
- Failed commits fail every request in the collected batch.
- Close first stops accepting new work, then drains or fails pending work.
- Batch limits include flush window, request count, record count, and byte
  count.

Recommended initial settings:

```text
FlushWindow: 200us
QueueSize: 1024
MaxRequests: configurable
MaxRecords: configurable
MaxBytes: configurable
```

## 10. Concurrency Model

Root DB:

- The Pebble wrapper is safe for concurrent operations.
- There is no global root write lock.
- Domain packages lock only where domain invariants require it.

MessageDB:

- Each `ChannelLog` owns its own append lock.
- Same channel append, truncate, retention trim, and snapshot install are
  serialized.
- Different channels can append concurrently.
- LEO and write-in-progress flags use atomics plus a small state lock.
- Checkpoint monotonic writes have their own lock.
- Committed cursor writes have their own monotonic lock.

MetaDB:

- Replace the current all-DB lock with hash-slot striped locks.
- Single hash-slot reads use that slot's read lock.
- Single hash-slot writes use that slot's write lock.
- Multi hash-slot batches lock hash slots in ascending order.
- Global migration rows that are not scoped to a hash slot use a small global
  meta lock.

This keeps single hash-slot Raft apply safe while allowing independent hash
slots to proceed concurrently.

## 11. MessageDB Design

Public shape:

```go
type MessageDB struct {
    // unexported
}

func (db *MessageDB) Channel(key ChannelKey, id ChannelID) *ChannelLog
func (db *MessageDB) ListChannels(ctx context.Context) ([]ChannelKey, error)
```

`ChannelLog` API:

```go
func (l *ChannelLog) Append(ctx context.Context, records []Record, opts AppendOptions) (AppendResult, error)
func (l *ChannelLog) ApplyFetch(ctx context.Context, req ApplyFetchRequest) (ApplyFetchResult, error)
func (l *ChannelLog) Read(ctx context.Context, fromSeq uint64, opts ReadOptions) ([]Message, error)
func (l *ChannelLog) ReadReverse(ctx context.Context, fromSeq uint64, opts ReadOptions) ([]Message, error)
func (l *ChannelLog) GetBySeq(ctx context.Context, seq uint64) (Message, bool, error)
func (l *ChannelLog) GetByMessageID(ctx context.Context, messageID uint64) (Message, bool, error)
func (l *ChannelLog) ListByClientMsgNo(ctx context.Context, clientMsgNo string, beforeSeq uint64, limit int) (MessagePage, error)
func (l *ChannelLog) LEO(ctx context.Context) (uint64, error)
func (l *ChannelLog) Truncate(ctx context.Context, toSeq uint64) error
```

The new API uses sequence terminology only:

- The first message has `seq=1`.
- An empty log has `LEO=0`.
- Offset compatibility is removed from the storage layer.
- Replication layers that need offsets convert at their boundary.

Message table:

```text
table id: 1
primary key: channelKey, message_seq

family 0: header
  message_id
  framer_flags
  setting
  stream_flag
  msg_key
  expire
  client_seq
  client_msg_no
  stream_no
  stream_id
  timestamp
  channel_id
  channel_type
  topic
  from_uid
  payload_hash
  payload_size

family 1: payload
  payload
```

Indexes:

```text
uidx_message_id:
  key   = message_id
  value = message_seq

idx_client_msg_no:
  key   = client_msg_no, message_seq
  value = message_seq

uidx_from_uid_client_msg_no:
  key   = from_uid, client_msg_no
  value = message_seq, message_id, payload_hash
```

Append flow:

1. Lock the channel append path.
2. Recover or read cached LEO.
3. Decode records into rows and assign contiguous sequences.
4. In strict mode, check existing unique indexes and in-batch duplicates.
5. In trusted mode, skip existing index reads but still check in-batch
   duplicates.
6. Stage header family, payload family, and secondary indexes.
7. Submit to the message append coordinator.
8. Publish LEO only after commit success.

Apply-fetch:

```go
type ApplyFetchRequest struct {
    BaseSeq             uint64
    Records             []Record
    Checkpoint          *Checkpoint
    EpochPoint          *EpochPoint
    PreviousCommittedHW uint64
    Mode                AppendMode
}
```

`BaseSeq` is explicit. If it does not match the local LEO, storage returns
`ErrConflict` and the replication layer decides whether to truncate or fetch.

Channel system state is stored under message system keys:

- Checkpoint.
- Epoch history.
- Snapshot payload.
- Committed dispatch cursor.
- Retention state.

Retention range deletion scans header rows to delete secondary indexes, then
range-deletes row families and updates retention state in the same batch.

`ListChannels` uses a message catalog key written on the first durable message
append or channel-system mutation for a channel. Empty in-memory channel
handles are not listed.

## 12. MetaDB Design

Public shape:

```go
type MetaDB struct {
    // unexported
}

func (db *MetaDB) HashSlot(hashSlot uint16) *Shard
func (db *MetaDB) HashSlots(hashSlots []uint16) []*Shard
```

Initial table IDs:

```text
1  user
2  channel
3  channel_runtime_meta
4  device
5  subscriber
6  user_conversation_state
7  reserved_conversation_projection
8  channel_migration_task
9  cmd_conversation_state
10 plugin_user_binding
11 hashslot_migration_state
12 hashslot_migration_delta
13 hashslot_migration_outbox
```

Primary and secondary keys:

```text
user:
  pk = uid

device:
  pk = uid, device_flag

channel:
  pk = channel_id, channel_type
  idx_channel_id = channel_id, channel_type

subscriber:
  pk = channel_id, channel_type, uid

channel_runtime_meta:
  pk = channel_id, channel_type

user_conversation_state:
  pk = uid, channel_id, channel_type
  idx_active = uid, active_at desc, channel_id, channel_type

cmd_conversation_state:
  pk = uid, channel_id, channel_type
  idx_active = uid, active_at desc, channel_id, channel_type

channel_migration_task:
  pk = channel_id, channel_type, task_id
  idx_active = channel_id, channel_type
  idx_terminal = completed_at_ms, channel_id, channel_type, task_id

plugin_user_binding:
  pk = uid, plugin_no
  idx_plugin = plugin_no, uid

hashslot_migration_state:
  pk = empty within the hash-slot partition

hashslot_migration_delta:
  pk = source_slot, source_index within the hash-slot partition

hashslot_migration_outbox:
  pk = source_slot, target_slot, source_index within the hash-slot partition
```

Conversation active indexes use descending time so recent-first scans are
forward scans.

For the hash-slot migration tables, the hash slot is the partition ID. It is
also kept in the typed Go value for self-description, but it is not duplicated
as a key column.

`MetaBatch` replaces the current write batch:

```go
type MetaBatch struct {
    // unexported
}

func (db *MetaDB) NewBatch() *MetaBatch
func (b *MetaBatch) UpsertUser(hashSlot uint16, u User) error
func (b *MetaBatch) CreateUser(hashSlot uint16, u User) error
func (b *MetaBatch) UpsertDevice(hashSlot uint16, d Device) error
func (b *MetaBatch) UpsertChannel(hashSlot uint16, ch Channel) error
func (b *MetaBatch) AddSubscribers(hashSlot uint16, channelID string, channelType int64, uids []string, version uint64) error
func (b *MetaBatch) Commit(ctx context.Context) error
```

Internally, batch methods append typed operations. Commit:

1. Collects touched hash slots.
2. Locks them in ascending order.
3. Creates one tx.
4. Builds a batch-local overlay for read-your-writes.
5. Validates and applies each op.
6. Checks guards.
7. Commits with `Sync`.
8. Publishes cache updates.

Guards make migration and runtime metadata invariants explicit:

```go
type RuntimeMetaVersionGuard struct {
    HashSlot      uint16
    ChannelID     string
    ChannelType   int64
    ChannelEpoch  uint64
    LeaderEpoch   uint64
    FenceVersion  uint64
}

type MigrationActiveSlotGuard struct {
    HashSlot       uint16
    ChannelID      string
    ChannelType    int64
    ExpectedTaskID string
    MustBeEmpty    bool
}
```

Channel runtime metadata monotonic updates return an explicit result:

```go
type MonotonicResult uint8

const (
    MonotonicApplied MonotonicResult = iota
    MonotonicIgnoredStale
    MonotonicConflict
)
```

Subscriber mutations sort and de-duplicate UIDs before writing. They also
advance `channel.subscriber_mutation_version` when the request version is
greater than the current version.

## 13. Snapshot and Range Operations

MetaDB supports hash-slot snapshots:

```go
func (db *MetaDB) ExportHashSlotSnapshot(ctx context.Context, hashSlots []uint16) (HashSlotSnapshot, error)
func (db *MetaDB) ImportHashSlotSnapshot(ctx context.Context, snap HashSlotSnapshot, opts ImportOptions) error
func (db *MetaDB) DeleteHashSlotData(ctx context.Context, hashSlot uint16) error
```

Export scans row, index, and system spans for each requested hash slot.

The first version uses a stable logical format:

```text
[snapshot version]
repeated:
  [key len][key bytes][value len][value bytes]
```

Import:

1. Validates the snapshot header and hash-slot set.
2. Locks hash slots in ascending order.
3. Deletes existing row, index, and system spans.
4. Imports entries.
5. Optionally preserves migration metadata.
6. Commits with `Sync`.
7. Clears affected caches.

MessageDB supports channel-scoped snapshot payloads and retention range deletes
through channel system keys and message-row spans.

## 14. Caching

The first version uses explicit typed caches only.

MessageDB caches:

- Channel LEO.
- Channel retention floor.
- Durable commit counters for tests and diagnostics.

MetaDB caches:

- Channel metadata by hash slot, channel ID, and channel type.

No general row cache is added in the first version.

Cache rules:

- Publish cache changes only after commit success.
- Batch-local overlays do not update global caches before commit.
- Range delete and snapshot import clear affected partition caches.
- Subscriber changes update the channel cache because they advance the
  subscriber mutation version.

## 15. Observability

The shared commit coordinator should expose low-cardinality events compatible
with existing send trace usage:

- Batch collect duration.
- Request count.
- Record count.
- Byte count.
- Queue depth.
- Commit duration.
- Result.

MessageDB should keep message append and retention metrics domain-specific.
MetaDB should expose batch op count, touched hash-slot count, guard conflicts,
and snapshot import/export sizes.

## 16. Testing Plan

Internal tests:

- Key encoding order, prefix end, and span boundaries.
- Row codec required fields, nullable fields, unknown column skip, type
  mismatch, truncated payload, and checksum mismatch.
- Schema descriptor duplicate IDs and bad references.
- Commit coordinator batching, publish-after-commit, failed commit fanout, and
  close behavior.

MessageDB tests:

- Append assigns contiguous sequences.
- Strict append rejects conflicting unique indexes.
- Trusted append skips existing index reads but writes indexes.
- Forward and reverse reads respect limit and max bytes.
- Message ID, client message number, and idempotency lookups work.
- LEO recovers after reopen.
- Checkpoint monotonic rules reject stale or unsafe checkpoints.
- Apply-fetch validates `BaseSeq`.
- Snapshot install atomically writes snapshot, checkpoint, and history.
- Truncate and retention delete rows and secondary indexes.
- Same channel appends serialize.
- Different channels append concurrently.

MetaDB tests:

- Create, upsert, get, delete, and page scans for basic tables.
- Channel ID secondary index.
- Subscriber add, remove, contains, page, and mutation version advance.
- Batch read-your-writes.
- Create unique guard.
- Migration active uniqueness.
- Runtime metadata monotonic applied, ignored, and conflict outcomes.
- Multi hash-slot batch lock ordering.
- Snapshot export, import, delete, and cache clearing.
- Conversation active descending order.
- Terminal migration task GC.
- Plugin binding primary and plugin index.

Targeted verification command after implementation:

```bash
go test ./pkg/db/... ./pkg/channel/... ./pkg/slot/... ./internal/app/... -count=1
```

Run `go test ./...` before final integration if the refactor changes many
callers. Do not run integration-tag tests by default.

## 17. Benchmarks

Benchmark command:

```bash
go test -run '^$' -bench . -benchmem ./pkg/db/message ./pkg/db/meta
```

Message benchmarks:

- Append with 1, 32, and 256 records.
- Parallel append on 1, 4, 16, and 64 channels.
- Forward and reverse read with 1, 32, and 256 rows.
- Get by message ID.
- Retention trim in chunks.

Meta benchmarks:

- User create, get, update.
- Channel create, list by channel ID, cached get.
- Subscriber add and page.
- Runtime meta upsert.
- Mixed `MetaBatch` apply.
- Snapshot export and import.

Track:

- `ns/op`
- `allocs/op`
- `bytes/op`
- commit batch size
- write stalls
- append p95 and p99 latency in later performance runs

## 18. Migration Plan

Phase 1: Build `pkg/db` internals.

- Engine wrapper.
- Key codec.
- Row codec.
- Schema validation.
- Tx, iterator, span.
- Commit coordinator.
- Shared errors and options.

Phase 2: Build `pkg/db/message`.

- Message schema and codecs.
- Append and apply-fetch.
- Checkpoint, history, snapshot, retention, idempotency.
- Message tests and benchmarks.

Phase 3: Build `pkg/db/meta`.

- User, device, channel, and subscriber tables.
- Channel runtime metadata.
- Conversation and CMD conversation state.
- Plugin bindings.
- Channel migration tasks and operations.
- Hash-slot migration state, delta, and outbox.
- Snapshot import, export, and delete.
- Meta tests and benchmarks.

Phase 4: Replace callers.

- Update `internal/app` composition root.
- Update `pkg/slot/fsm` and `pkg/slot/proxy`.
- Update `internal/usecase/*`, `internal/runtime/*`, and `internal/access/*`.
- Update `pkg/channel/handler` and `pkg/channelv2/store`.
- Delete old `pkg/channel/store` and `pkg/slot/meta` once callers compile.

Phase 5: Documentation and configuration.

- Update `wukongim.conf.example` if storage paths or config keys change.
- Update `AGENTS.md` when package layout changes.
- Add `FLOW.md` files for `pkg/db`, `pkg/db/message`, and `pkg/db/meta`.
- Record concise durable storage rules in
  `docs/development/PROJECT_KNOWLEDGE.md`.

## 19. Open Questions

- Use CRC32C or add xxhash for message payload hashes.
- Keep or remove `MsgKey` after confirming protocol-level requirements.
- Enable `ChannelRuntimeMeta` cache by default or leave it disabled until a
  benchmark proves it is needed.
- Keep MetaDB Raft apply as direct `Sync` in the first implementation, unless
  benchmarks show clear benefit from a coordinator lane.
- Finalize concrete config keys and default paths during implementation.
