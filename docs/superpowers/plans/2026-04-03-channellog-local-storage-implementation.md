# Channellog Local Storage Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a real Pebble-backed local message-log store into `pkg/storage/channellog`, keep the package as a single root package, and deliver V1 seq-based read and truncate APIs without introducing a separate storage package or rebuilding the old `wkdb` indexing model.

**Architecture:** The implementation keeps `pkg/storage/channellog` as the channel-oriented facade and adds a concrete local storage layer beside it in the same package. The storage truth is `groupKey + offset`; `messageSeq` stays a facade-level interpretation of committed offset via checkpoint `HW`. A channel-scoped `Store` exposes seq-based local read helpers, while package-local bridge adapters let the same persisted store back ISR `LogStore`, `CheckpointStore`, `EpochHistoryStore`, and `SnapshotApplier`.

**Tech Stack:** Go 1.23, `github.com/cockroachdb/pebble/v2`, existing `pkg/storage/channellog`, existing `pkg/replication/isr`, standard library `bytes/context/encoding/binary/errors/math/path/filepath/testing`

---

## Scope Check

This plan covers one subsystem only: local message-log persistence inside `pkg/storage/channellog`.

It includes:

- root-package `DB` and channel-scoped `Store`
- Pebble keyspace and codec definitions
- local offset-log persistence
- checkpoint, epoch history, and snapshot payload persistence
- seq-based local load and truncate helpers
- package-local ISR bridge adapters
- real-store integration tests for `pkg/storage/channellog`

It does not include:

- idempotency state persistence
- search or secondary indexes
- protocol changes
- controller or `internal/app` composition changes outside the test seam needed to exercise the store

## File Structure

### Production files

- Create: `pkg/storage/channellog/db.go`
  Responsibility: define `DB`, open/close Pebble, and create channel-scoped `Store` handles.
- Create: `pkg/storage/channellog/store.go`
  Responsibility: define `Store`, bind `ChannelKey` plus derived `GroupKey`, and expose local APIs plus bridge constructors.
- Create: `pkg/storage/channellog/store_keys.go`
  Responsibility: define Pebble keyspaces for message log entries, checkpoint state, epoch history, and snapshot payload.
- Create: `pkg/storage/channellog/store_codec.go`
  Responsibility: encode and decode checkpoint, epoch history, and snapshot payload values.
- Create: `pkg/storage/channellog/log_store.go`
  Responsibility: persist the offset-based message log, implement append/read/LEO/truncate/sync helpers, and make `DB` satisfy `MessageLog`.
- Create: `pkg/storage/channellog/checkpoint_store.go`
  Responsibility: load and store per-channel checkpoint state.
- Create: `pkg/storage/channellog/history_store.go`
  Responsibility: load and append per-channel epoch history.
- Create: `pkg/storage/channellog/snapshot_store.go`
  Responsibility: persist opaque snapshot payload bytes per channel.
- Create: `pkg/storage/channellog/seq_read.go`
  Responsibility: implement `LoadMsg`, `LoadNextRangeMsgs`, `LoadPrevRangeMsgs`, and `TruncateLogTo` on top of the offset log plus checkpoint `HW`.
- Create: `pkg/storage/channellog/isr_bridge.go`
  Responsibility: adapt a channel-scoped `Store` to ISR `LogStore`, `CheckpointStore`, `EpochHistoryStore`, and `SnapshotApplier`.
- Modify: `pkg/storage/channellog/errors.go`
  Responsibility: add store-facing sentinel errors such as `ErrInvalidArgument`, `ErrMessageNotFound`, and `ErrCorruptValue`.
- Modify: `pkg/storage/channellog/doc.go`
  Responsibility: update package docs to mention the new local-storage role alongside the channel facade.

### Test files

- Create: `pkg/storage/channellog/storage_testenv_test.go`
  Responsibility: temp-dir Pebble helpers, encoded message fixtures, and checkpoint/history test helpers.
- Create: `pkg/storage/channellog/db_test.go`
  Responsibility: `Open/Close/ForChannel` behavior and `DB` compile-surface checks.
- Create: `pkg/storage/channellog/log_store_test.go`
  Responsibility: offset append/read/LEO/truncate/sync behavior and `MessageLog.Read` behavior.
- Create: `pkg/storage/channellog/checkpoint_store_test.go`
  Responsibility: checkpoint load/store round trips.
- Create: `pkg/storage/channellog/history_store_test.go`
  Responsibility: epoch history load/append round trips and ordering.
- Create: `pkg/storage/channellog/snapshot_store_test.go`
  Responsibility: snapshot payload persist/replace/read behavior.
- Create: `pkg/storage/channellog/seq_read_test.go`
  Responsibility: seq-based single load, forward/backward range load, committed fencing, and truncate semantics.
- Create: `pkg/storage/channellog/isr_bridge_test.go`
  Responsibility: real `isr.Replica` integration against one `Store`.
- Create: `pkg/storage/channellog/storage_integration_test.go`
  Responsibility: `Cluster`-level semantic coverage using the real Pebble-backed store instead of fakes.

## Implementation Notes

- Follow `@superpowers:test-driven-development` task-by-task.
- Finish with `@superpowers:verification-before-completion`.
- Use `github.com/cockroachdb/pebble/v2` to match the rest of `pkg/storage`.
- Keep the package root as `pkg/storage/channellog`; do not add subpackages.
- Keep `codec.go` focused on message-record encoding and payload hashing. Put non-message storage codecs in `store_codec.go`.
- Use length-delimited or otherwise unambiguous `groupKey` encoding in keys; do not rely on string separators alone.
- Always copy bytes out of Pebble before returning them from storage APIs.
- The persisted log truth is offset-ordered. `messageSeq` is interpreted as `offset + 1` only in seq-read helpers and facade code.
- V1 does not add any new search/index surfaces. Do not add from-sender, client-message-number, timestamp, or message-id indexes.
- `DB` should satisfy `MessageLog` so existing `Cluster.Config.Log` wiring can stay narrow.
- `Store` should expose bridge constructors or package-local adapters so a single concrete store instance can back ISR and local seq reads.

## Task 1: Add the local-storage shell and package-facing errors

**Files:**
- Create: `pkg/storage/channellog/db.go`
- Create: `pkg/storage/channellog/store.go`
- Create: `pkg/storage/channellog/store_keys.go`
- Create: `pkg/storage/channellog/store_codec.go`
- Create: `pkg/storage/channellog/storage_testenv_test.go`
- Create: `pkg/storage/channellog/db_test.go`
- Modify: `pkg/storage/channellog/errors.go`
- Modify: `pkg/storage/channellog/doc.go`

- [ ] **Step 1: Write the failing storage-shell tests**

```go
func TestOpenCreatesDBAndForChannelBindsDerivedGroupKey(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "store"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()

	key := ChannelKey{ChannelID: "c1", ChannelType: 1}
	store := db.ForChannel(key)
	if store == nil {
		t.Fatal("expected Store")
	}
	if store.key != key {
		t.Fatalf("Store.key = %+v, want %+v", store.key, key)
	}
	if store.groupKey != channelGroupKey(key) {
		t.Fatalf("Store.groupKey = %q, want %q", store.groupKey, channelGroupKey(key))
	}
}

func TestDBImplementsMessageLogSurface(t *testing.T) {
	var _ MessageLog = (*DB)(nil)
}
```

- [ ] **Step 2: Run the targeted tests and confirm they fail**

Run: `go test ./pkg/storage/channellog -run 'TestOpenCreatesDBAndForChannelBindsDerivedGroupKey|TestDBImplementsMessageLogSurface' -v`

Expected: FAIL because `DB`, `Store`, and the new store-facing errors do not exist yet.

- [ ] **Step 3: Add the minimal `DB` and `Store` shell**

```go
type DB struct {
	db *pebble.DB
}

func Open(path string) (*DB, error) { ... }
func (db *DB) Close() error { ... }
func (db *DB) ForChannel(key ChannelKey) *Store { ... }

type Store struct {
	db       *DB
	key      ChannelKey
	groupKey isr.GroupKey
}
```

Implementation details:
- put only lifecycle and handle construction in `db.go`
- put only channel-scoped state and validation helpers in `store.go`
- add store-facing sentinel errors in `errors.go`
- update package docs in `doc.go` to describe the local-storage role

- [ ] **Step 4: Add the keyspace and codec placeholders**

```go
const (
	keyspaceLog        byte = 0x10
	keyspaceCheckpoint byte = 0x11
	keyspaceHistory    byte = 0x12
	keyspaceSnapshot   byte = 0x13
)
```

Implementation details:
- define one prefix helper per keyspace in `store_keys.go`
- define placeholder encode/decode helpers for checkpoint/history/snapshot values in `store_codec.go`
- do not implement log persistence here yet

- [ ] **Step 5: Re-run the targeted tests**

Run: `go test ./pkg/storage/channellog -run 'TestOpenCreatesDBAndForChannelBindsDerivedGroupKey|TestDBImplementsMessageLogSurface' -v`

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add pkg/storage/channellog/db.go pkg/storage/channellog/store.go pkg/storage/channellog/store_keys.go pkg/storage/channellog/store_codec.go pkg/storage/channellog/storage_testenv_test.go pkg/storage/channellog/db_test.go pkg/storage/channellog/errors.go pkg/storage/channellog/doc.go
git commit -m "feat: add channellog storage shell"
```

## Task 2: Implement the offset-ordered local message log

**Files:**
- Create: `pkg/storage/channellog/log_store.go`
- Create: `pkg/storage/channellog/log_store_test.go`
- Modify: `pkg/storage/channellog/storage_testenv_test.go`

- [ ] **Step 1: Write the failing log-store tests**

```go
func TestStoreAppendAndReadByOffset(t *testing.T) {
	store := openTestStore(t, ChannelKey{ChannelID: "c1", ChannelType: 1})

	base, err := store.appendPayloads([][]byte{[]byte("one"), []byte("two")})
	if err != nil {
		t.Fatalf("appendPayloads() error = %v", err)
	}
	if base != 0 {
		t.Fatalf("base = %d, want 0", base)
	}

	records, err := store.readOffsets(0, 2, 1024)
	if err != nil {
		t.Fatalf("readOffsets() error = %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("len(records) = %d, want 2", len(records))
	}
	if records[1].Offset != 1 {
		t.Fatalf("records[1].Offset = %d, want 1", records[1].Offset)
	}
}

func TestDBReadScopesByGroupKeyAndBudget(t *testing.T) {
	db := openTestDB(t)
	first := db.ForChannel(ChannelKey{ChannelID: "c1", ChannelType: 1})
	second := db.ForChannel(ChannelKey{ChannelID: "c2", ChannelType: 1})
	mustAppendPayloads(t, first, []string{"one", "two"})
	mustAppendPayloads(t, second, []string{"zzz"})

	records, err := db.Read(channelGroupKey(first.key), 0, 10, len("one")+16)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("len(records) = %d, want 1", len(records))
	}
}
```

- [ ] **Step 2: Run the targeted tests and confirm they fail**

Run: `go test ./pkg/storage/channellog -run 'TestStoreAppendAndReadByOffset|TestDBReadScopesByGroupKeyAndBudget' -v`

Expected: FAIL because the offset-log helpers and `DB.Read` are not implemented.

- [ ] **Step 3: Implement the raw offset log helpers**

```go
func (s *Store) appendPayloads(payloads [][]byte) (uint64, error) { ... }
func (s *Store) readOffsets(fromOffset uint64, limit int, maxBytes int) ([]LogRecord, error) { ... }
func (s *Store) leo() (uint64, error) { ... }
func (s *Store) truncateOffsets(to uint64) error { ... }
func (s *Store) sync() error { ... }
```

Implementation details:
- persist one record per `(groupKey, offset)`
- derive `baseOffset` from current `LEO`
- copy stored payload bytes before returning
- keep `leo()` as the exclusive end offset

- [ ] **Step 4: Make `DB` satisfy `MessageLog.Read`**

```go
func (db *DB) Read(groupKey isr.GroupKey, fromOffset uint64, limit int, maxBytes int) ([]LogRecord, error) {
	...
}
```

Implementation details:
- route directly by `groupKey`-prefixed keys
- enforce `limit`
- enforce `maxBytes` the same way current fake tests do: always allow the first record, then stop if the next record would exceed the budget

- [ ] **Step 5: Re-run the targeted tests**

Run: `go test ./pkg/storage/channellog -run 'TestStoreAppendAndReadByOffset|TestDBReadScopesByGroupKeyAndBudget' -v`

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add pkg/storage/channellog/log_store.go pkg/storage/channellog/log_store_test.go pkg/storage/channellog/storage_testenv_test.go
git commit -m "feat: add channellog local offset log"
```

## Task 3: Persist checkpoint, epoch history, and snapshot payload state

**Files:**
- Create: `pkg/storage/channellog/checkpoint_store.go`
- Create: `pkg/storage/channellog/checkpoint_store_test.go`
- Create: `pkg/storage/channellog/history_store.go`
- Create: `pkg/storage/channellog/history_store_test.go`
- Create: `pkg/storage/channellog/snapshot_store.go`
- Create: `pkg/storage/channellog/snapshot_store_test.go`
- Modify: `pkg/storage/channellog/store_codec.go`
- Modify: `pkg/storage/channellog/storage_testenv_test.go`

- [ ] **Step 1: Write the failing persistence-state tests**

```go
func TestStoreCheckpointRoundTrip(t *testing.T) {
	store := openTestStore(t, ChannelKey{ChannelID: "c1", ChannelType: 1})
	want := isr.Checkpoint{Epoch: 3, LogStartOffset: 4, HW: 9}

	if err := store.storeCheckpoint(want); err != nil {
		t.Fatalf("storeCheckpoint() error = %v", err)
	}
	got, err := store.loadCheckpoint()
	if err != nil {
		t.Fatalf("loadCheckpoint() error = %v", err)
	}
	if got != want {
		t.Fatalf("checkpoint = %+v, want %+v", got, want)
	}
}

func TestStoreEpochHistoryAppendAndLoad(t *testing.T) {
	store := openTestStore(t, ChannelKey{ChannelID: "c1", ChannelType: 1})
	if err := store.appendEpochPoint(isr.EpochPoint{Epoch: 7, StartOffset: 10}); err != nil {
		t.Fatalf("appendEpochPoint() error = %v", err)
	}
	points, err := store.loadEpochHistory()
	if err != nil {
		t.Fatalf("loadEpochHistory() error = %v", err)
	}
	if len(points) != 1 || points[0].Epoch != 7 {
		t.Fatalf("points = %+v", points)
	}
}
```

- [ ] **Step 2: Run the targeted tests and confirm they fail**

Run: `go test ./pkg/storage/channellog -run 'TestStoreCheckpointRoundTrip|TestStoreEpochHistoryAppendAndLoad|TestStoreReplaceSnapshotPayload' -v`

Expected: FAIL because checkpoint, history, and snapshot persistence are not implemented.

- [ ] **Step 3: Implement checkpoint persistence**

```go
func (s *Store) loadCheckpoint() (isr.Checkpoint, error) { ... }
func (s *Store) storeCheckpoint(checkpoint isr.Checkpoint) error { ... }
```

Implementation details:
- use one value per channel-scoped checkpoint key
- return `isr.ErrEmptyState` when no checkpoint exists yet
- keep value encoding in `store_codec.go`

- [ ] **Step 4: Implement epoch-history and snapshot persistence**

```go
func (s *Store) loadEpochHistory() ([]isr.EpochPoint, error) { ... }
func (s *Store) appendEpochPoint(point isr.EpochPoint) error { ... }
func (s *Store) loadSnapshotPayload() ([]byte, error) { ... }
func (s *Store) storeSnapshotPayload(payload []byte) error { ... }
```

Implementation details:
- keep history append-only in V1
- store snapshot payload as opaque bytes
- snapshot replacement must overwrite the prior payload cleanly

- [ ] **Step 5: Re-run the targeted tests**

Run: `go test ./pkg/storage/channellog -run 'TestStoreCheckpointRoundTrip|TestStoreEpochHistoryAppendAndLoad|TestStoreReplaceSnapshotPayload' -v`

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add pkg/storage/channellog/checkpoint_store.go pkg/storage/channellog/checkpoint_store_test.go pkg/storage/channellog/history_store.go pkg/storage/channellog/history_store_test.go pkg/storage/channellog/snapshot_store.go pkg/storage/channellog/snapshot_store_test.go pkg/storage/channellog/store_codec.go pkg/storage/channellog/storage_testenv_test.go
git commit -m "feat: persist channellog checkpoint and snapshot state"
```

## Task 4: Add seq-based local reads and truncate semantics

**Files:**
- Create: `pkg/storage/channellog/seq_read.go`
- Create: `pkg/storage/channellog/seq_read_test.go`
- Modify: `pkg/storage/channellog/errors.go`
- Modify: `pkg/storage/channellog/storage_testenv_test.go`

- [ ] **Step 1: Write the failing seq-read tests**

```go
func TestLoadMsgReturnsCommittedMessageOnly(t *testing.T) {
	store := openTestStore(t, ChannelKey{ChannelID: "c1", ChannelType: 1})
	mustAppendStoredMessages(t, store, "one", "two", "three")
	mustStoreCheckpoint(t, store, isr.Checkpoint{Epoch: 1, HW: 2})

	msg, err := store.LoadMsg(2)
	if err != nil {
		t.Fatalf("LoadMsg(2) error = %v", err)
	}
	if string(msg.Payload) != "two" {
		t.Fatalf("Payload = %q, want %q", msg.Payload, "two")
	}

	_, err = store.LoadMsg(3)
	if !errors.Is(err, ErrMessageNotFound) {
		t.Fatalf("expected ErrMessageNotFound, got %v", err)
	}
}

func TestTruncateLogToRemovesTailAndClampsCheckpoint(t *testing.T) {
	store := openTestStore(t, ChannelKey{ChannelID: "c1", ChannelType: 1})
	mustAppendStoredMessages(t, store, "one", "two", "three")
	mustStoreCheckpoint(t, store, isr.Checkpoint{Epoch: 1, HW: 3})

	if err := store.TruncateLogTo(2); err != nil {
		t.Fatalf("TruncateLogTo() error = %v", err)
	}

	got, err := store.LoadNextRangeMsgs(1, 0, 10)
	if err != nil {
		t.Fatalf("LoadNextRangeMsgs() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
}
```

- [ ] **Step 2: Run the targeted tests and confirm they fail**

Run: `go test ./pkg/storage/channellog -run 'TestLoadMsgReturnsCommittedMessageOnly|TestLoadNextRangeMsgsCapsByCommittedHW|TestLoadPrevRangeMsgsMatchesLegacyWindowSemantics|TestTruncateLogToRemovesTailAndClampsCheckpoint' -v`

Expected: FAIL because `Store` does not expose seq-based local APIs yet.

- [ ] **Step 3: Implement single and range seq reads**

```go
func (s *Store) LoadMsg(seq uint64) (ChannelMessage, error) { ... }
func (s *Store) LoadNextRangeMsgs(startSeq, endSeq uint64, limit int) ([]ChannelMessage, error) { ... }
func (s *Store) LoadPrevRangeMsgs(startSeq, endSeq uint64, limit int) ([]ChannelMessage, error) { ... }
```

Implementation details:
- convert `seq` to `offset = seq - 1`
- fence visibility with `checkpoint.HW`
- preserve old backward-window semantics from `wkdb`
- decode records with existing `decodeStoredMessage`

- [ ] **Step 4: Implement `TruncateLogTo`**

```go
func (s *Store) TruncateLogTo(seq uint64) error { ... }
```

Implementation details:
- keep messages where `messageSeq <= seq`
- call the underlying offset truncate with `to = seq`
- clamp `checkpoint.HW` if it is greater than `seq`
- trim any epoch-history tail that starts beyond the new local end

- [ ] **Step 5: Re-run the targeted tests**

Run: `go test ./pkg/storage/channellog -run 'TestLoadMsgReturnsCommittedMessageOnly|TestLoadNextRangeMsgsCapsByCommittedHW|TestLoadPrevRangeMsgsMatchesLegacyWindowSemantics|TestTruncateLogToRemovesTailAndClampsCheckpoint' -v`

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add pkg/storage/channellog/seq_read.go pkg/storage/channellog/seq_read_test.go pkg/storage/channellog/errors.go pkg/storage/channellog/storage_testenv_test.go
git commit -m "feat: add channellog seq reads and truncate"
```

## Task 5: Adapt one `Store` to ISR persistence interfaces

**Files:**
- Create: `pkg/storage/channellog/isr_bridge.go`
- Create: `pkg/storage/channellog/isr_bridge_test.go`
- Modify: `pkg/storage/channellog/store.go`
- Modify: `pkg/storage/channellog/storage_testenv_test.go`

- [ ] **Step 1: Write the failing ISR-bridge integration tests**

```go
func TestStoreAdaptersBackReplicaRecovery(t *testing.T) {
	store := openTestStore(t, ChannelKey{ChannelID: "c1", ChannelType: 1})
	replica, err := isr.NewReplica(isr.ReplicaConfig{
		LocalNode:         1,
		LogStore:          store.LogStore(),
		CheckpointStore:   store.CheckpointStore(),
		EpochHistoryStore: store.EpochHistoryStore(),
		SnapshotApplier:   store.SnapshotApplier(),
	})
	if err != nil {
		t.Fatalf("NewReplica() error = %v", err)
	}
	if replica.Status().HW != 0 {
		t.Fatalf("HW = %d, want 0", replica.Status().HW)
	}
}

func TestStoreSnapshotAdapterPersistsOpaquePayload(t *testing.T) {
	store := openTestStore(t, ChannelKey{ChannelID: "c1", ChannelType: 1})
	err := store.SnapshotApplier().InstallSnapshot(context.Background(), isr.Snapshot{
		GroupKey:  channelGroupKey(store.key),
		Epoch:     3,
		EndOffset: 8,
		Payload:   []byte("snap"),
	})
	if err != nil {
		t.Fatalf("InstallSnapshot() error = %v", err)
	}
	payload, err := store.loadSnapshotPayload()
	if err != nil {
		t.Fatalf("loadSnapshotPayload() error = %v", err)
	}
	if string(payload) != "snap" {
		t.Fatalf("payload = %q, want snap", payload)
	}
}
```

- [ ] **Step 2: Run the targeted tests and confirm they fail**

Run: `go test ./pkg/storage/channellog -run 'TestStoreAdaptersBackReplicaRecovery|TestStoreSnapshotAdapterPersistsOpaquePayload' -v`

Expected: FAIL because the `Store` bridge adapters do not exist.

- [ ] **Step 3: Implement bridge constructors on `Store`**

```go
func (s *Store) LogStore() isr.LogStore { ... }
func (s *Store) CheckpointStore() isr.CheckpointStore { ... }
func (s *Store) EpochHistoryStore() isr.EpochHistoryStore { ... }
func (s *Store) SnapshotApplier() isr.SnapshotApplier { ... }
```

Implementation details:
- keep the adapter structs private in `isr_bridge.go`
- do not move Pebble logic into the bridge
- bridge types should delegate to the raw store helpers from earlier tasks

- [ ] **Step 4: Add a real recovery-path test**

```go
func TestStoreAdaptersRecoverCheckpointAndLogAfterReopen(t *testing.T) { ... }
```

Implementation details:
- use one temp directory
- write through the first `Store`
- close and reopen `DB`
- recreate `Store`
- rebuild `isr.Replica`
- assert the recovered `HW` and local visible range match the stored checkpoint

- [ ] **Step 5: Re-run the targeted tests**

Run: `go test ./pkg/storage/channellog -run 'TestStoreAdaptersBackReplicaRecovery|TestStoreSnapshotAdapterPersistsOpaquePayload|TestStoreAdaptersRecoverCheckpointAndLogAfterReopen' -v`

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add pkg/storage/channellog/isr_bridge.go pkg/storage/channellog/isr_bridge_test.go pkg/storage/channellog/store.go pkg/storage/channellog/storage_testenv_test.go
git commit -m "feat: bridge channellog store into isr"
```

## Task 6: Add real-store `channellog` semantic integration coverage

**Files:**
- Create: `pkg/storage/channellog/storage_integration_test.go`
- Modify: `pkg/storage/channellog/fetch_test.go`
- Modify: `pkg/storage/channellog/send_test.go`
- Modify: `pkg/storage/channellog/storage_testenv_test.go`

- [ ] **Step 1: Write the failing real-store semantic tests**

```go
func TestFetchWithRealStoreReturnsCommittedMessagesOnly(t *testing.T) {
	env := newRealStoreFetchEnv(t)

	result, err := env.cluster.Fetch(context.Background(), FetchRequest{
		Key:      env.key,
		FromSeq:  1,
		Limit:    10,
		MaxBytes: 1024,
	})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if len(result.Messages) != 2 {
		t.Fatalf("len(Messages) = %d, want 2", len(result.Messages))
	}
	if result.Messages[0].MessageSeq != 1 || result.Messages[1].MessageSeq != 2 {
		t.Fatalf("seqs = %+v", result.Messages)
	}
}

func TestRealStoreSeqLoadsUseOffsetPlusOneSemantics(t *testing.T) {
	store := openTestStore(t, ChannelKey{ChannelID: "c1", ChannelType: 1})
	mustAppendStoredMessages(t, store, "one", "two")
	mustStoreCheckpoint(t, store, isr.Checkpoint{Epoch: 1, HW: 2})

	first, err := store.LoadMsg(1)
	if err != nil {
		t.Fatalf("LoadMsg(1) error = %v", err)
	}
	if first.MessageSeq != 1 {
		t.Fatalf("MessageSeq = %d, want 1", first.MessageSeq)
	}
}
```

- [ ] **Step 2: Run the targeted tests and confirm they fail**

Run: `go test ./pkg/storage/channellog -run 'TestFetchWithRealStoreReturnsCommittedMessagesOnly|TestRealStoreSeqLoadsUseOffsetPlusOneSemantics' -v`

Expected: FAIL because the real Pebble-backed store is not yet wired into semantic tests.

- [ ] **Step 3: Add real-store test environments**

Implementation details:
- keep the existing fake-based tests intact
- add separate helpers in `storage_testenv_test.go` for a real `DB`, real `Store`, and a fake runtime that reports committed `HW`
- configure `Cluster.Config.Log` with the real `DB`

- [ ] **Step 4: Make the real-store tests pass**

Implementation details:
- do not widen `Cluster`’s public API
- keep semantic assertions focused on committed visibility and `messageSeq = offset + 1`
- ensure `send.go` and `fetch.go` continue to work unchanged against the `MessageLog` abstraction

- [ ] **Step 5: Re-run the full `pkg/storage/channellog` test suite**

Run: `go test ./pkg/storage/channellog -count=1`

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add pkg/storage/channellog/storage_integration_test.go pkg/storage/channellog/fetch_test.go pkg/storage/channellog/send_test.go pkg/storage/channellog/storage_testenv_test.go
git commit -m "test: cover channellog semantics with real local storage"
```

## Final Verification

- [ ] Run: `go test ./pkg/storage/channellog -count=1`
- [ ] Run: `go test ./pkg/replication/isr -count=1`
- [ ] Review `git diff --stat` for unexpected scope
- [ ] Update any stale package docs if implementation drifted from the spec

If all checks pass, the feature is complete at the package level.
