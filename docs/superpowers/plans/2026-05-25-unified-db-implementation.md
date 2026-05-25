# Unified DB Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace `pkg/channel/store` and `pkg/slot/meta` with a new Pebble-backed `pkg/db` library that exposes typed MessageDB and MetaDB APIs.

**Architecture:** Build shared storage internals first, then implement `pkg/db/message` and `pkg/db/meta` on top of them, then migrate callers and delete the old storage packages. Keep Pebble isolated under `pkg/db/internal/*`, keep domain invariants in `message` and `meta`, and use frequent focused commits.

**Tech Stack:** Go 1.23, Pebble v2, existing WuKongIM channel/slot/domain types, Go unit tests and benchmarks.

---

## Preconditions and Guardrails

- Read `docs/superpowers/specs/2026-05-25-unified-db-design.md` before starting.
- Use @superpowers:test-driven-development for implementation tasks.
- Use @superpowers:verification-before-completion before each completion claim.
- Do not run integration-tag tests unless a task explicitly asks for them.
- Do not touch unrelated dirty files. At the time this plan was written, unrelated changes existed under `pkg/clusterv2/*` and `AGENTS.md`.
- Keep comments in English for exported structs, exported methods, and important storage invariants.
- If a package has a `FLOW.md`, read it before modifying that package and update it when behavior changes.

## Target File Structure

Create these new files:

```text
pkg/db/
  FLOW.md
  db.go
  errors.go
  options.go
  types.go

pkg/db/internal/engine/
  db.go
  batch.go
  iter.go
  span.go
  engine_test.go

pkg/db/internal/keycodec/
  codec.go
  builder.go
  span.go
  codec_test.go

pkg/db/internal/rowcodec/
  codec.go
  envelope.go
  value.go
  codec_test.go

pkg/db/internal/schema/
  schema.go
  validate.go
  validate_test.go

pkg/db/internal/commit/
  coordinator.go
  batch.go
  coordinator_test.go

pkg/db/internal/cache/
  tiny.go
  tiny_test.go

pkg/db/message/
  FLOW.md
  db.go
  types.go
  keys.go
  schema.go
  row.go
  codec.go
  channel_log.go
  append.go
  read.go
  indexes.go
  checkpoint.go
  history.go
  snapshot.go
  retention.go
  idempotency.go
  catalog.go
  testutil_test.go
  *_test.go
  *_benchmark_test.go

pkg/db/meta/
  FLOW.md
  db.go
  types.go
  keys.go
  schema.go
  shard.go
  tx_helpers.go
  batch.go
  cache_channel.go
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
  testutil_test.go
  *_test.go
  *_benchmark_test.go
```

Modify these existing areas during migration:

```text
internal/app/*.go
internal/runtime/**/*.go
internal/usecase/**/*.go
internal/access/**/*.go
pkg/channel/**/*.go
pkg/channelv2/store/channel_adapter.go
pkg/slot/fsm/**/*.go
pkg/slot/proxy/**/*.go
pkg/cluster/**/*.go
wukongim.conf.example
AGENTS.md
```

Delete after callers compile:

```text
pkg/channel/store/
pkg/slot/meta/
```

---

### Task 1: Root Package Skeleton

**Files:**
- Create: `pkg/db/errors.go`
- Create: `pkg/db/options.go`
- Create: `pkg/db/types.go`
- Create: `pkg/db/db.go`
- Create: `pkg/db/FLOW.md`
- Test: `pkg/db/db_test.go`

- [ ] **Step 1: Write root package tests**

Create `pkg/db/db_test.go` with tests for shared error identity and options defaults:

```go
func TestDefaultOptionsFillPathsAndDurability(t *testing.T) {
    opts := db.DefaultNodeStoreOptions(t.TempDir())
    if opts.MessagePath == "" || opts.MetaPath == "" {
        t.Fatalf("paths not filled: %#v", opts)
    }
    if opts.Commit.FlushWindow <= 0 {
        t.Fatalf("flush window not filled: %#v", opts.Commit)
    }
}

func TestErrorsSupportErrorsIs(t *testing.T) {
    if !errors.Is(fmt.Errorf("wrap: %w", db.ErrNotFound), db.ErrNotFound) {
        t.Fatal("ErrNotFound does not support errors.Is")
    }
}
```

- [ ] **Step 2: Run root tests and verify failure**

Run: `go test ./pkg/db -run 'TestDefaultOptions|TestErrorsSupport' -count=1`

Expected: FAIL because `pkg/db` does not exist or exported names are missing.

- [ ] **Step 3: Implement root package skeleton**

Add shared errors:

```go
var (
    ErrInvalidArgument  = errors.New("db: invalid argument")
    ErrNotFound         = errors.New("db: not found")
    ErrAlreadyExists    = errors.New("db: already exists")
    ErrConflict         = errors.New("db: conflict")
    ErrCorruptValue     = errors.New("db: corrupt value")
    ErrCorruptState     = errors.New("db: corrupt state")
    ErrChecksumMismatch = errors.New("db: checksum mismatch")
    ErrClosed           = errors.New("db: closed")
)
```

Add options and placeholder root types. Do not open Pebble yet.

- [ ] **Step 4: Run root tests and verify pass**

Run: `go test ./pkg/db -run 'TestDefaultOptions|TestErrorsSupport' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/db
git commit -m "feat(db): add root storage package skeleton"
```

### Task 2: Key Codec

**Files:**
- Create: `pkg/db/internal/keycodec/codec.go`
- Create: `pkg/db/internal/keycodec/builder.go`
- Create: `pkg/db/internal/keycodec/span.go`
- Test: `pkg/db/internal/keycodec/codec_test.go`

- [ ] **Step 1: Write key codec tests**

Cover string, uint64, ordered int64, descending int64, prefix end, and key skeleton:

```go
func TestOrderedInt64SortsNumerically(t *testing.T) {
    values := []int64{-2, -1, 0, 1, 2}
    keys := make([][]byte, 0, len(values))
    for _, v := range values {
        keys = append(keys, keycodec.AppendInt64Ordered(nil, v))
    }
    if !sort.SliceIsSorted(keys, func(i, j int) bool { return bytes.Compare(keys[i], keys[j]) < 0 }) {
        t.Fatalf("ordered int64 keys are not sorted")
    }
}
```

- [ ] **Step 2: Run key codec tests and verify failure**

Run: `go test ./pkg/db/internal/keycodec -count=1`

Expected: FAIL with missing package or functions.

- [ ] **Step 3: Implement key codec**

Implement:

- `Domain`, `PartitionKind`, `Space` constants.
- `Builder` with `Reset`, `Domain`, `Partition`, `Row`, `Index`, `System`, `Catalog`, `String`, `Uint64`, `Int64Ordered`, `Int64Desc`, `Family`, `Key`.
- `AppendString`, `ReadString`, `AppendUint64`, `AppendInt64Ordered`, `AppendInt64Desc`.
- `Span` and `PrefixEnd`.

- [ ] **Step 4: Run key codec tests and verify pass**

Run: `go test ./pkg/db/internal/keycodec -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/db/internal/keycodec
git commit -m "feat(db): add ordered key codec"
```

### Task 3: Row Codec and Value Envelope

**Files:**
- Create: `pkg/db/internal/rowcodec/envelope.go`
- Create: `pkg/db/internal/rowcodec/value.go`
- Create: `pkg/db/internal/rowcodec/codec.go`
- Test: `pkg/db/internal/rowcodec/codec_test.go`

- [ ] **Step 1: Write row codec tests**

Cover value envelope checksum, checksum mismatch, required columns, unknown optional column skip, and type mismatch.

```go
func TestEnvelopeDetectsChecksumMismatch(t *testing.T) {
    key := []byte("k")
    value := rowcodec.Wrap(key, 1, rowcodec.CodecColumns, rowcodec.FlagChecksum, []byte("payload"))
    value[len(value)-1] ^= 0xff
    _, err := rowcodec.Unwrap(key, value)
    if !errors.Is(err, db.ErrChecksumMismatch) {
        t.Fatalf("err = %v, want checksum mismatch", err)
    }
}
```

- [ ] **Step 2: Run row codec tests and verify failure**

Run: `go test ./pkg/db/internal/rowcodec -count=1`

Expected: FAIL.

- [ ] **Step 3: Implement row codec**

Implement:

- Envelope `Wrap` and `Unwrap` using CRC32C.
- Column writer with ascending ID enforcement.
- Column scanner that returns `(columnID, type, raw)`.
- Helpers for `String`, `Bytes`, `Int64`, `Uint64`, `Bool`, `Uint8`.
- Conversion helpers for zig-zag int64.

- [ ] **Step 4: Run row codec tests and verify pass**

Run: `go test ./pkg/db/internal/rowcodec -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/db/internal/rowcodec
git commit -m "feat(db): add row value codec"
```

### Task 4: Schema Descriptors

**Files:**
- Create: `pkg/db/internal/schema/schema.go`
- Create: `pkg/db/internal/schema/validate.go`
- Test: `pkg/db/internal/schema/validate_test.go`

- [ ] **Step 1: Write schema validation tests**

Cover duplicate column IDs, duplicate family IDs, index references to unknown columns, and family references to unknown columns.

- [ ] **Step 2: Run schema tests and verify failure**

Run: `go test ./pkg/db/internal/schema -count=1`

Expected: FAIL.

- [ ] **Step 3: Implement schema descriptors and validation**

Implement the `Table`, `Column`, `Family`, `Index`, `Type` structs from the spec and a `ValidateTable(table Table) error` function.

- [ ] **Step 4: Run schema tests and verify pass**

Run: `go test ./pkg/db/internal/schema -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/db/internal/schema
git commit -m "feat(db): add schema descriptors"
```

### Task 5: Pebble Engine Wrapper

**Files:**
- Create: `pkg/db/internal/engine/db.go`
- Create: `pkg/db/internal/engine/batch.go`
- Create: `pkg/db/internal/engine/iter.go`
- Create: `pkg/db/internal/engine/span.go`
- Test: `pkg/db/internal/engine/engine_test.go`
- Modify: `pkg/db/options.go`
- Modify: `pkg/db/db.go`

- [ ] **Step 1: Write engine tests**

Cover open/close, get/set, range delete, iterator bounds, and `SetDeferred`.

- [ ] **Step 2: Run engine tests and verify failure**

Run: `go test ./pkg/db/internal/engine -count=1`

Expected: FAIL.

- [ ] **Step 3: Implement engine wrapper**

Wrap Pebble with:

- `Open(path string, opts Options) (*DB, error)`.
- `Close() error`.
- `Get`, `NewBatch`, `NewIter`.
- `Batch.Set`, `SetDeferred`, `Delete`, `DeleteRange`, `Commit(sync bool)`, `Close`.
- Iterator wrapper that copies keys when exposing them beyond iterator movement.

- [ ] **Step 4: Run engine tests and verify pass**

Run: `go test ./pkg/db/internal/engine -count=1`

Expected: PASS.

- [ ] **Step 5: Wire root open/close smoke**

Add a root `NodeStore` that can open message and meta engine instances but does not expose domain DBs yet.

- [ ] **Step 6: Run root and engine tests**

Run: `go test ./pkg/db ./pkg/db/internal/engine -count=1`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add pkg/db pkg/db/internal/engine
git commit -m "feat(db): wrap pebble engine"
```

### Task 6: Commit Coordinator

**Files:**
- Create: `pkg/db/internal/commit/coordinator.go`
- Create: `pkg/db/internal/commit/batch.go`
- Test: `pkg/db/internal/commit/coordinator_test.go`
- Modify: `pkg/db/options.go`

- [ ] **Step 1: Write coordinator tests**

Cover flush window batching, max request/record/byte limits, publish after commit, failed commit fanout, and close behavior.

- [ ] **Step 2: Run coordinator tests and verify failure**

Run: `go test ./pkg/db/internal/commit -count=1`

Expected: FAIL.

- [ ] **Step 3: Port and generalize coordinator**

Use the current `pkg/channel/store/commit.go` behavior as the baseline, but replace channel-specific fields with `Lane`, `Partition`, `Records`, and `Bytes`.

- [ ] **Step 4: Run coordinator tests and verify pass**

Run: `go test ./pkg/db/internal/commit -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/db/internal/commit pkg/db/options.go
git commit -m "feat(db): add commit coordinator"
```

### Task 7: Typed Tiny Cache

**Files:**
- Create: `pkg/db/internal/cache/tiny.go`
- Test: `pkg/db/internal/cache/tiny_test.go`

- [ ] **Step 1: Write cache tests**

Cover get/set/delete, capacity bound, arbitrary eviction, and clear.

- [ ] **Step 2: Run cache tests and verify failure**

Run: `go test ./pkg/db/internal/cache -count=1`

Expected: FAIL.

- [ ] **Step 3: Implement cache**

Implement a small generic cache with a mutex and bounded map. Keep eviction simple and deterministic enough for tests.

- [ ] **Step 4: Run cache tests and verify pass**

Run: `go test ./pkg/db/internal/cache -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/db/internal/cache
git commit -m "feat(db): add bounded typed cache"
```

### Task 8: MessageDB Types, Keys, and Schema

**Files:**
- Create: `pkg/db/message/FLOW.md`
- Create: `pkg/db/message/types.go`
- Create: `pkg/db/message/keys.go`
- Create: `pkg/db/message/schema.go`
- Create: `pkg/db/message/db.go`
- Test: `pkg/db/message/schema_test.go`
- Test: `pkg/db/message/keys_test.go`
- Modify: `pkg/db/db.go`

- [ ] **Step 1: Write message schema and key tests**

Cover table validation, channel row prefix, index prefix, system key prefix, and global catalog key ordering.

- [ ] **Step 2: Run message schema tests and verify failure**

Run: `go test ./pkg/db/message -run 'TestMessageSchema|TestMessageKeys' -count=1`

Expected: FAIL.

- [ ] **Step 3: Implement message types, schema, and key helpers**

Define `MessageDB`, `ChannelLog`, `ChannelKey`, `ChannelID`, `Record`, `Message`, `Checkpoint`, `EpochPoint`, `Snapshot`, `AppendOptions`, `ReadOptions`, and table/index/system IDs.

- [ ] **Step 4: Wire `NodeStore.Messages()`**

Return a `*message.MessageDB` backed by the message engine and commit coordinator.

- [ ] **Step 5: Run message schema tests and verify pass**

Run: `go test ./pkg/db/message ./pkg/db -run 'TestMessageSchema|TestMessageKeys|TestOpen' -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/db pkg/db/message
git commit -m "feat(db): add message db schema"
```

### Task 9: Message Row Codec

**Files:**
- Create: `pkg/db/message/row.go`
- Create: `pkg/db/message/codec.go`
- Test: `pkg/db/message/codec_test.go`

- [ ] **Step 1: Write message codec tests**

Use the old `pkg/channel/store/message_row_test.go` and `table_codec_test.go` as behavior references. Cover header family, payload family, required message ID, payload hash, and corrupt checksum.

- [ ] **Step 2: Run message codec tests and verify failure**

Run: `go test ./pkg/db/message -run 'TestMessage.*Codec|TestMessage.*Row' -count=1`

Expected: FAIL.

- [ ] **Step 3: Implement message row conversion and codec**

Implement typed row encode/decode. Keep protocol conversion at the boundary; do not import Pebble.

- [ ] **Step 4: Run message codec tests and verify pass**

Run: `go test ./pkg/db/message -run 'TestMessage.*Codec|TestMessage.*Row' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/db/message/row.go pkg/db/message/codec.go pkg/db/message/*codec*_test.go
git commit -m "feat(db): encode message rows"
```

### Task 10: Message Append and Read

**Files:**
- Create: `pkg/db/message/channel_log.go`
- Create: `pkg/db/message/append.go`
- Create: `pkg/db/message/read.go`
- Create: `pkg/db/message/testutil_test.go`
- Test: `pkg/db/message/append_test.go`
- Test: `pkg/db/message/read_test.go`

- [ ] **Step 1: Write failing append/read tests**

Cover empty append, contiguous seq assignment, LEO recovery after reopen, forward reads, reverse reads, max bytes, and same-channel append serialization.

- [ ] **Step 2: Run focused tests and verify failure**

Run: `go test ./pkg/db/message -run 'TestChannelLogAppend|TestChannelLogRead|TestChannelLogLEO' -count=1`

Expected: FAIL.

- [ ] **Step 3: Implement append/read minimal path**

Implement primary row writes and primary seq scans. Do not implement secondary indexes yet.

- [ ] **Step 4: Run focused tests and verify pass**

Run: `go test ./pkg/db/message -run 'TestChannelLogAppend|TestChannelLogRead|TestChannelLogLEO' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/db/message
git commit -m "feat(db): append and read channel messages"
```

### Task 11: Message Indexes and Idempotency

**Files:**
- Create: `pkg/db/message/indexes.go`
- Create: `pkg/db/message/idempotency.go`
- Test: `pkg/db/message/indexes_test.go`
- Test: `pkg/db/message/idempotency_test.go`
- Modify: `pkg/db/message/append.go`
- Modify: `pkg/db/message/read.go`

- [ ] **Step 1: Write failing index tests**

Cover message ID lookup, client message number page, idempotency lookup, strict duplicate detection, trusted append behavior, and in-batch duplicate detection.

- [ ] **Step 2: Run focused tests and verify failure**

Run: `go test ./pkg/db/message -run 'TestMessageIndex|TestIdempotency|TestAppendStrict|TestAppendTrusted' -count=1`

Expected: FAIL.

- [ ] **Step 3: Implement secondary indexes**

Write and read `uidx_message_id`, `idx_client_msg_no`, and `uidx_from_uid_client_msg_no`. Strict mode reads existing unique indexes; trusted mode skips existing index reads.

- [ ] **Step 4: Run focused tests and verify pass**

Run: `go test ./pkg/db/message -run 'TestMessageIndex|TestIdempotency|TestAppendStrict|TestAppendTrusted' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/db/message
git commit -m "feat(db): add message secondary indexes"
```

### Task 12: Message System State

**Files:**
- Create: `pkg/db/message/checkpoint.go`
- Create: `pkg/db/message/history.go`
- Create: `pkg/db/message/snapshot.go`
- Test: `pkg/db/message/checkpoint_test.go`
- Test: `pkg/db/message/history_test.go`
- Test: `pkg/db/message/snapshot_test.go`

- [ ] **Step 1: Write failing system-state tests**

Port behavior from old checkpoint/history/snapshot tests. Cover checkpoint load/store, monotonic checkpoint rejection, epoch append/truncate, and atomic snapshot install.

- [ ] **Step 2: Run focused tests and verify failure**

Run: `go test ./pkg/db/message -run 'TestCheckpoint|TestHistory|TestSnapshot' -count=1`

Expected: FAIL.

- [ ] **Step 3: Implement system state**

Store channel system keys under message system space. Keep checkpoint/history/snapshot in the same commit when required.

- [ ] **Step 4: Run focused tests and verify pass**

Run: `go test ./pkg/db/message -run 'TestCheckpoint|TestHistory|TestSnapshot' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/db/message
git commit -m "feat(db): add message system state"
```

### Task 13: Message ApplyFetch, Truncate, Retention, and Catalog

**Files:**
- Create: `pkg/db/message/retention.go`
- Create: `pkg/db/message/catalog.go`
- Test: `pkg/db/message/apply_fetch_test.go`
- Test: `pkg/db/message/truncate_test.go`
- Test: `pkg/db/message/retention_test.go`
- Test: `pkg/db/message/catalog_test.go`
- Modify: `pkg/db/message/append.go`
- Modify: `pkg/db/message/channel_log.go`

- [ ] **Step 1: Write failing tests**

Cover explicit `BaseSeq`, `ErrConflict` on mismatch, apply-fetch with checkpoint and epoch point, truncate deleting indexes, retention trim deleting indexes, and `ListChannels` catalog behavior.

- [ ] **Step 2: Run focused tests and verify failure**

Run: `go test ./pkg/db/message -run 'TestApplyFetch|TestTruncate|TestRetention|TestCatalog' -count=1`

Expected: FAIL.

- [ ] **Step 3: Implement apply-fetch/truncate/retention/catalog**

Chunk large retention deletes and ensure catalog keys are written on first durable append or system-state mutation.

- [ ] **Step 4: Run focused tests and verify pass**

Run: `go test ./pkg/db/message -run 'TestApplyFetch|TestTruncate|TestRetention|TestCatalog' -count=1`

Expected: PASS.

- [ ] **Step 5: Add message benchmarks**

Create `pkg/db/message/message_benchmark_test.go` covering append, parallel append, read, message ID lookup, and retention trim.

- [ ] **Step 6: Run message package tests and benchmarks smoke**

Run: `go test ./pkg/db/message -count=1`

Run: `go test -run '^$' -bench BenchmarkChannelLogAppend -benchmem ./pkg/db/message`

Expected: tests PASS and benchmark runs.

- [ ] **Step 7: Commit**

```bash
git add pkg/db/message
git commit -m "feat(db): complete message log storage"
```

### Task 14: MetaDB Types, Keys, Schema, and Shard Locks

**Files:**
- Create: `pkg/db/meta/FLOW.md`
- Create: `pkg/db/meta/types.go`
- Create: `pkg/db/meta/keys.go`
- Create: `pkg/db/meta/schema.go`
- Create: `pkg/db/meta/db.go`
- Create: `pkg/db/meta/shard.go`
- Create: `pkg/db/meta/tx_helpers.go`
- Test: `pkg/db/meta/schema_test.go`
- Test: `pkg/db/meta/keys_test.go`
- Test: `pkg/db/meta/shard_lock_test.go`
- Modify: `pkg/db/db.go`

- [ ] **Step 1: Write meta schema/key/lock tests**

Cover table validation, hash-slot row/index/system spans, active descending key ordering, and multi-hash-slot lock ordering helper.

- [ ] **Step 2: Run focused tests and verify failure**

Run: `go test ./pkg/db/meta -run 'TestMetaSchema|TestMetaKeys|TestShardLocks' -count=1`

Expected: FAIL.

- [ ] **Step 3: Implement meta types, schema, keys, and locks**

Define all table IDs and key helpers. Wire `NodeStore.Meta()`.

- [ ] **Step 4: Run focused tests and verify pass**

Run: `go test ./pkg/db/meta ./pkg/db -run 'TestMetaSchema|TestMetaKeys|TestShardLocks|TestOpen' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/db pkg/db/meta
git commit -m "feat(db): add meta db schema"
```

### Task 15: Meta User, Device, Channel, and Channel Cache

**Files:**
- Create: `pkg/db/meta/table_user.go`
- Create: `pkg/db/meta/table_device.go`
- Create: `pkg/db/meta/table_channel.go`
- Create: `pkg/db/meta/cache_channel.go`
- Test: `pkg/db/meta/user_test.go`
- Test: `pkg/db/meta/device_test.go`
- Test: `pkg/db/meta/channel_test.go`
- Test: `pkg/db/meta/channel_cache_test.go`

- [ ] **Step 1: Write failing table tests**

Port basic behavior from old `user_test.go`, `device_test.go`, `channel_test.go`, and channel cache tests. Cover create, duplicate create, upsert, update missing, delete, page scans, channel ID index, and warm cache read behavior.

- [ ] **Step 2: Run focused tests and verify failure**

Run: `go test ./pkg/db/meta -run 'TestUser|TestDevice|TestChannel' -count=1`

Expected: FAIL.

- [ ] **Step 3: Implement user/device/channel tables**

Use shared table helpers for primary rows and secondary indexes. Update channel cache only after commit success.

- [ ] **Step 4: Run focused tests and verify pass**

Run: `go test ./pkg/db/meta -run 'TestUser|TestDevice|TestChannel' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/db/meta
git commit -m "feat(db): add core meta tables"
```

### Task 16: Meta Subscribers

**Files:**
- Create: `pkg/db/meta/table_subscriber.go`
- Test: `pkg/db/meta/subscriber_test.go`
- Modify: `pkg/db/meta/table_channel.go`
- Modify: `pkg/db/meta/cache_channel.go`

- [ ] **Step 1: Write failing subscriber tests**

Cover add, remove, list page, snapshot list, contains, has subscribers, UID de-duplication, stable ordering, mutation version advance, and channel cache update.

- [ ] **Step 2: Run focused tests and verify failure**

Run: `go test ./pkg/db/meta -run 'TestSubscriber|TestChannelCache' -count=1`

Expected: FAIL.

- [ ] **Step 3: Implement subscriber table and channel version updates**

Sort and de-duplicate UIDs before staging writes. Advance subscriber mutation version with `max(existing, requestVersion)`.

- [ ] **Step 4: Run focused tests and verify pass**

Run: `go test ./pkg/db/meta -run 'TestSubscriber|TestChannelCache' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/db/meta
git commit -m "feat(db): add subscriber metadata storage"
```

### Task 17: Meta Channel Runtime Metadata

**Files:**
- Create: `pkg/db/meta/table_runtime_meta.go`
- Test: `pkg/db/meta/channel_runtime_meta_test.go`
- Test: `pkg/db/meta/channel_runtime_meta_page_test.go`

- [ ] **Step 1: Write failing runtime meta tests**

Port existing tests and add explicit `MonotonicApplied`, `MonotonicIgnoredStale`, and `MonotonicConflict` expectations. Cover page scans and retention advancement.

- [ ] **Step 2: Run focused tests and verify failure**

Run: `go test ./pkg/db/meta -run 'TestChannelRuntimeMeta' -count=1`

Expected: FAIL.

- [ ] **Step 3: Implement runtime meta storage**

Implement normalization, validation, monotonic resolution, page scans, retention advancement, and delete.

- [ ] **Step 4: Run focused tests and verify pass**

Run: `go test ./pkg/db/meta -run 'TestChannelRuntimeMeta' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/db/meta
git commit -m "feat(db): add channel runtime metadata storage"
```

### Task 18: Meta Conversation State Tables

**Files:**
- Create: `pkg/db/meta/table_conversation.go`
- Create: `pkg/db/meta/table_cmd_conversation.go`
- Test: `pkg/db/meta/user_conversation_state_test.go`
- Test: `pkg/db/meta/cmd_conversation_state_test.go`

- [ ] **Step 1: Write failing conversation tests**

Port existing tests and add active index recent-first assertions using descending time keys.

- [ ] **Step 2: Run focused tests and verify failure**

Run: `go test ./pkg/db/meta -run 'TestUserConversation|TestCMDConversation' -count=1`

Expected: FAIL.

- [ ] **Step 3: Implement conversation tables**

Implement upsert, get, active listing, state page, touch active, clear active, hide/delete semantics, and CMD read advancement.

- [ ] **Step 4: Run focused tests and verify pass**

Run: `go test ./pkg/db/meta -run 'TestUserConversation|TestCMDConversation' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/db/meta
git commit -m "feat(db): add conversation metadata storage"
```

### Task 19: Meta Plugin Binding and Hash-Slot Migration Tables

**Files:**
- Create: `pkg/db/meta/table_plugin_binding.go`
- Create: `pkg/db/meta/table_hashslot_migration.go`
- Test: `pkg/db/meta/plugin_binding_test.go`
- Test: `pkg/db/meta/hashslot_migration_test.go`

- [ ] **Step 1: Write failing tests**

Port plugin binding and hash-slot migration tests. Cover binding by UID, plugin index scan, migration state, applied delta, outbox scan/delete/ack, and hash-slot partition key behavior.

- [ ] **Step 2: Run focused tests and verify failure**

Run: `go test ./pkg/db/meta -run 'TestPlugin|TestHashSlotMigration' -count=1`

Expected: FAIL.

- [ ] **Step 3: Implement plugin binding and hash-slot migration tables**

Keep hash slot as the key partition, not duplicated as a table key column. Keep it in typed values for self-description.

- [ ] **Step 4: Run focused tests and verify pass**

Run: `go test ./pkg/db/meta -run 'TestPlugin|TestHashSlotMigration' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/db/meta
git commit -m "feat(db): add plugin and hashslot migration storage"
```

### Task 20: Meta Batch and Guards

**Files:**
- Create: `pkg/db/meta/batch.go`
- Test: `pkg/db/meta/batch_test.go`
- Modify: `pkg/db/meta/table_*.go`

- [ ] **Step 1: Write failing batch tests**

Cover batch read-your-writes, create unique guard, multi hash-slot lock ordering, channel runtime meta guards, migration active uniqueness, cache publish after commit, and rollback on failed guard.

- [ ] **Step 2: Run focused tests and verify failure**

Run: `go test ./pkg/db/meta -run 'TestMetaBatch|Test.*Guard' -count=1`

Expected: FAIL.

- [ ] **Step 3: Implement MetaBatch pipeline**

Store typed ops first, then on commit collect hash slots, lock in order, build overlay, validate, apply, commit, and publish caches.

- [ ] **Step 4: Run focused tests and verify pass**

Run: `go test ./pkg/db/meta -run 'TestMetaBatch|Test.*Guard' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/db/meta
git commit -m "feat(db): add guarded meta batches"
```

### Task 21: Meta Channel Migration Tasks and Operations

**Files:**
- Create: `pkg/db/meta/table_channel_migration.go`
- Test: `pkg/db/meta/channel_migration_task_test.go`
- Test: `pkg/db/meta/channel_migration_ops_test.go`
- Modify: `pkg/db/meta/batch.go`
- Modify: `pkg/db/meta/table_runtime_meta.go`

- [ ] **Step 1: Write failing migration task tests**

Port existing channel migration task and ops tests. Cover create, claim, advance, active index, terminal index, terminal GC, fence set/reset/clear, learner add/promote, leader transfer, abort, and runtime guards.

- [ ] **Step 2: Run focused tests and verify failure**

Run: `go test ./pkg/db/meta -run 'TestChannelMigration' -count=1`

Expected: FAIL.

- [ ] **Step 3: Implement migration tasks and batch operations**

Use explicit guard structs. Keep task rows, active index, terminal index, and runtime meta updates in one tx.

- [ ] **Step 4: Run focused tests and verify pass**

Run: `go test ./pkg/db/meta -run 'TestChannelMigration' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/db/meta
git commit -m "feat(db): add channel migration metadata"
```

### Task 22: Meta Snapshot, DeleteHashSlotData, and Benchmarks

**Files:**
- Create: `pkg/db/meta/snapshot.go`
- Test: `pkg/db/meta/snapshot_test.go`
- Test: `pkg/db/meta/benchmark_test.go`
- Modify: `pkg/db/meta/db.go`
- Modify: `pkg/db/meta/cache_channel.go`

- [ ] **Step 1: Write failing snapshot/delete tests**

Port snapshot tests. Cover export selected hash slots, import, preserve migration meta, delete hash slot data, cache clearing, and corrupt snapshot rejection.

- [ ] **Step 2: Run focused tests and verify failure**

Run: `go test ./pkg/db/meta -run 'TestSnapshot|TestDeleteHashSlotData' -count=1`

Expected: FAIL.

- [ ] **Step 3: Implement snapshot and range operations**

Use row, index, and system spans for each hash slot. Import should lock slots in order, delete existing spans, import entries, commit sync, and clear caches.

- [ ] **Step 4: Run focused tests and verify pass**

Run: `go test ./pkg/db/meta -run 'TestSnapshot|TestDeleteHashSlotData' -count=1`

Expected: PASS.

- [ ] **Step 5: Add meta benchmarks**

Benchmark user get/create/update, channel cached get, subscriber add/page, runtime meta upsert, mixed batch, snapshot export/import.

- [ ] **Step 6: Run meta package tests and benchmark smoke**

Run: `go test ./pkg/db/meta -count=1`

Run: `go test -run '^$' -bench BenchmarkUserGet -benchmem ./pkg/db/meta`

Expected: tests PASS and benchmark runs.

- [ ] **Step 7: Commit**

```bash
git add pkg/db/meta
git commit -m "feat(db): add meta snapshots and benchmarks"
```

### Task 23: Replace Slot FSM and Proxy Callers

**Files:**
- Modify: `pkg/slot/fsm/*.go`
- Modify: `pkg/slot/fsm/*_test.go`
- Modify: `pkg/slot/proxy/*.go`
- Modify: `pkg/slot/proxy/*_test.go`
- Modify: `pkg/cluster/**/*.go` where it imports `pkg/slot/meta`

- [ ] **Step 1: Inspect old meta import surface**

Run: `rg 'pkg/slot/meta|metadb' pkg/slot pkg/cluster -g'*.go'`

Record import clusters before editing.

- [ ] **Step 2: Update imports and type names**

Replace `metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"` with `metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"` first. Keep alias `metadb` during migration to reduce churn.

- [ ] **Step 3: Adjust API differences**

Update callers for explicit monotonic results, explicit hash-slot APIs, new batch behavior, and new snapshot option names.

- [ ] **Step 4: Run focused slot tests**

Run: `go test ./pkg/slot/fsm ./pkg/slot/proxy -count=1`

Expected: PASS before committing.

- [ ] **Step 5: Commit**

```bash
git add pkg/slot pkg/cluster
git commit -m "refactor: migrate slot metadata callers to db meta"
```

### Task 24: Replace Channel Message Callers

**Files:**
- Modify: `pkg/channel/**/*.go`
- Modify: `pkg/channelv2/store/channel_adapter.go`
- Modify: `internal/app/channelcluster.go`
- Modify: `internal/app/committed_replay.go`
- Modify: `internal/app/channelretention.go`
- Modify: `internal/app/manager_messages.go`
- Modify: `internal/app/manager_message_retention.go`
- Modify: related tests under `pkg/channel/**` and `internal/app/**`

- [ ] **Step 1: Inspect old channel store import surface**

Run: `rg 'pkg/channel/store|channelstore' pkg/channel pkg/channelv2 internal/app -g'*.go'`

Record import clusters before editing.

- [ ] **Step 2: Update imports and constructor wiring**

Replace old store usage with `github.com/WuKongIM/WuKongIM/pkg/db/message`. Keep aliases concise and local.

- [ ] **Step 3: Adjust seq/offset boundaries**

Storage now uses seq only. Convert old offset semantics at channel replication/handler boundaries, not in `pkg/db/message`.

- [ ] **Step 4: Run focused channel and app tests**

Run: `go test ./pkg/channel/... ./pkg/channelv2/... ./internal/app -count=1`

Expected: PASS before committing.

- [ ] **Step 5: Commit**

```bash
git add pkg/channel pkg/channelv2 internal/app
git commit -m "refactor: migrate channel message callers to db message"
```

### Task 25: Replace Remaining Meta Callers

**Files:**
- Modify: `internal/usecase/**/*.go`
- Modify: `internal/runtime/**/*.go`
- Modify: `internal/access/**/*.go`
- Modify: `internal/bench/**/*.go` if imports old meta
- Modify: remaining tests that import `pkg/slot/meta`

- [ ] **Step 1: Find remaining old imports**

Run: `rg 'pkg/slot/meta|pkg/channel/store' -g'*.go'`

Expected: only old packages themselves should remain before this task starts.

- [ ] **Step 2: Update remaining imports**

Replace remaining meta and channel store imports with new packages.

- [ ] **Step 3: Fix API call sites**

Adjust constructor calls, snapshot types, batch behavior, errors, monotonic results, and seq/offset conversions.

- [ ] **Step 4: Run broad focused tests**

Run: `go test ./internal/... ./pkg/... -count=1`

Expected: PASS before committing.

- [ ] **Step 5: Commit**

```bash
git add internal pkg
git commit -m "refactor: migrate remaining storage callers"
```

### Task 26: Delete Old Packages and Update Docs

**Files:**
- Delete: `pkg/channel/store/`
- Delete: `pkg/slot/meta/`
- Modify: `internal/app/build.go`
- Modify: `wukongim.conf.example` if storage path keys changed
- Modify: `AGENTS.md`
- Modify: `docs/development/PROJECT_KNOWLEDGE.md`
- Test: any affected docs/catalog tests

- [ ] **Step 1: Verify no callers import old packages**

Run: `rg 'pkg/channel/store|pkg/slot/meta' -g'*.go'`

Expected: no Go caller imports old packages.

- [ ] **Step 2: Delete old packages**

Delete `pkg/channel/store` and `pkg/slot/meta` after imports are gone.

- [ ] **Step 3: Update project docs**

Update `AGENTS.md` directory structure. Add a concise PROJECT_KNOWLEDGE entry that `pkg/db` is the single local storage library and single-node deployment remains a single-node cluster.

- [ ] **Step 4: Run compile/test sweep for deletion fallout**

Run: `go test ./pkg/db/... ./pkg/channel/... ./pkg/slot/... ./internal/app -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add -A pkg/channel/store pkg/slot/meta pkg/db internal/app wukongim.conf.example AGENTS.md docs/development/PROJECT_KNOWLEDGE.md
git commit -m "refactor: remove legacy storage packages"
```

### Task 27: Final Verification and Performance Baseline

**Files:**
- All modified files
- Create: `docs/superpowers/reports/2026-05-25-unified-db-verification.md`

- [ ] **Step 1: Run formatting and whitespace checks**

Run: `files=$(git diff --name-only -- '*.go'); if [ -n "$files" ]; then gofmt -w $files; fi`

Run: `git diff --check`

Expected: no whitespace errors.

- [ ] **Step 2: Run targeted storage tests**

Run: `go test ./pkg/db/... -count=1`

Expected: PASS.

- [ ] **Step 3: Run migrated package tests**

Run: `go test ./pkg/channel/... ./pkg/channelv2/... ./pkg/slot/... ./internal/app -count=1`

Expected: PASS.

- [ ] **Step 4: Run broad unit tests if time allows**

Run: `go test ./... -count=1`

Expected: PASS. If this is too slow or exposes unrelated failures, record exact package and failure in the verification report.

- [ ] **Step 5: Run benchmark smoke**

Run: `go test -run '^$' -bench . -benchmem ./pkg/db/message ./pkg/db/meta`

Expected: benchmarks complete and report `allocs/op`.

- [ ] **Step 6: Write verification report**

Create `docs/superpowers/reports/2026-05-25-unified-db-verification.md` with commands, results, failures, and follow-up risks.

- [ ] **Step 7: Commit verification report**

```bash
git add docs/superpowers/reports/2026-05-25-unified-db-verification.md
git commit -m "docs: record unified db verification"
```

## Review Checklist

Before considering implementation complete, verify:

- [ ] No Go files import `pkg/channel/store` or `pkg/slot/meta`.
- [ ] Pebble types are not exposed outside `pkg/db/internal/*`.
- [ ] Message storage uses seq terminology internally.
- [ ] Offset conversions happen only at channel protocol/replication boundaries.
- [ ] Meta writes no longer use a single package-wide global lock.
- [ ] Hash-slot snapshot import/export uses row, index, and system spans.
- [ ] Channel cache publishes only after commit success.
- [ ] `FLOW.md` files reflect actual code flow.
- [ ] `AGENTS.md` directory structure is current.
- [ ] Relevant tests and benchmark smoke have fresh evidence.
