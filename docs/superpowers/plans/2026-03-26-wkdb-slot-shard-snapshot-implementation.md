# wkdb Slot Shard Snapshot Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Refactor `wkdb` into a slot-scoped Pebble state store and add slot snapshot export/import primitives that can back `multiraft` state-machine snapshot and restore.

**Architecture:** Keep a single Pebble DB, but move all business data into slot-prefixed keyspaces so one slot maps to a small set of contiguous spans. Build snapshot support in `wkdb` first (`ExportSlotSnapshot`, `DeleteSlotData`, `ImportSlotSnapshot`), then layer `wkdbStateMachine` and `wkdbRaftStorage` on top for `multiraft` integration without mixing business snapshot bytes with Raft persistence.

**Tech Stack:** Go 1.23, Pebble (`github.com/cockroachdb/pebble`), `go.etcd.io/raft/v3`, Go testing

---

## File Structure

### Production files

- Modify: `wkdb/db.go`
  Responsibility: retain Pebble lifecycle, add `ForSlot(slot)` and internal helpers shared by slot-scoped operations.
- Create: `wkdb/shard.go`
  Responsibility: define `ShardStore` and shared slot validation helpers.
- Modify: `wkdb/codec.go`
  Responsibility: introduce slot-aware business key encoding and common top-level keyspace prefixes.
- Modify: `wkdb/user.go`
  Responsibility: move user CRUD onto `ShardStore` and write only slot-prefixed keys.
- Modify: `wkdb/channel.go`
  Responsibility: move channel CRUD and secondary-index scans onto `ShardStore`.
- Create: `wkdb/shard_spans.go`
  Responsibility: compute stable state/index/meta spans for one slot.
- Create: `wkdb/snapshot_codec.go`
  Responsibility: encode/decode `SlotSnapshot.Data` payload with versioning and checksum.
- Create: `wkdb/snapshot.go`
  Responsibility: export one slot from a Pebble snapshot, delete one slot, and import one slot payload.
- Create: `wkdb/raft_state_machine.go`
  Responsibility: implement `multiraft.StateMachine` using `wkdb` slot operations.
- Create: `wkdb/raft_storage.go`
  Responsibility: implement `multiraft.Storage` using dedicated `/wk/raft/<group>/...` keys.

### Test files

- Modify: `wkdb/codec_test.go`
  Responsibility: slot-aware key ordering and prefix encoding coverage.
- Modify: `wkdb/user_test.go`
  Responsibility: verify user CRUD is isolated by slot.
- Modify: `wkdb/channel_test.go`
  Responsibility: verify channel CRUD and index scans are isolated by slot.
- Create: `wkdb/snapshot_test.go`
  Responsibility: slot deletion, export/import round-trip, checksum/slot mismatch failures, idempotent restore retry semantics.
- Create: `wkdb/raft_state_machine_test.go`
  Responsibility: snapshot/restore behavior through the `multiraft.StateMachine` interface.
- Create: `wkdb/raft_storage_test.go`
  Responsibility: persistent `HardState`, entries, snapshot metadata, and `MarkApplied` behavior.
- Create: `multiraft/wkdb_integration_test.go`
  Responsibility: end-to-end recovery/open-group coverage using `wkdbStateMachine` + `wkdbRaftStorage`.

### Existing spec and support docs

- Reference: `docs/superpowers/specs/2026-03-26-wkdb-slot-shard-snapshot-design.md`
  Responsibility: approved scope, architecture, keyspace boundaries, snapshot semantics, and non-goals.

## Implementation Notes

- Follow `@superpowers:test-driven-development` task by task: write the failing test, run it red, implement the minimum code, run it green.
- Do not preserve compatibility with the old global-key `wkdb` layout.
- Remove or stop exporting DB-level business CRUD entry points; slot-scoped access should be the only business write path.
- Treat `multiraft.GroupID == slotID` as a hard assumption throughout this implementation. Do not add translation tables or indirection.
- `ExportSlotSnapshot` must only include `/wk/state`, `/wk/index`, and `/wk/meta` data for the target slot.
- `wkdbRaftStorage` must never store data under business spans and must never read business snapshot payloads as a substitute for raft storage.
- `ImportSlotSnapshot` must be retry-safe: delete the target slot’s data first on every import attempt, then fully rewrite the snapshot contents.
- First implementation may build snapshot payloads fully in memory and restore via Pebble batch writes. Do not add SST-ingest optimization unless tests show the basic design is correct first.

## Task 1: Introduce slot-scoped surface area and slot-aware key prefixes

**Files:**
- Modify: `wkdb/db.go`
- Modify: `wkdb/codec.go`
- Create: `wkdb/shard.go`
- Modify: `wkdb/codec_test.go`

- [ ] **Step 1: Write the failing slot-surface and prefix-ordering tests**

```go
func TestForSlotReturnsShardStore(t *testing.T) {
    db := openTestDB(t)
    shard := db.ForSlot(7)
    if shard == nil || shard.slot != 7 {
        t.Fatalf("ForSlot(7) = %#v", shard)
    }
}

func TestStateAndIndexPrefixesIncludeSlotAndSortStably(t *testing.T) {
    aState := encodeStatePrefix(1, TableIDUser)
    bState := encodeStatePrefix(2, TableIDUser)
    if bytes.Compare(aState, bState) >= 0 {
        t.Fatalf("slot-prefixed keys did not sort by slot")
    }
}
```

- [ ] **Step 2: Run the targeted tests and confirm they fail**

Run: `go test ./wkdb -run 'Test(ForSlotReturnsShardStore|StateAndIndexPrefixesIncludeSlotAndSortStably)' -count=1`

Expected: FAIL because `ShardStore`, `ForSlot`, and the slot-aware prefix helpers do not exist yet.

- [ ] **Step 3: Add `ShardStore`, slot validation, and slot-prefixed top-level key encoders**

```go
type ShardStore struct {
    db   *DB
    slot uint64
}

func (db *DB) ForSlot(slot uint64) *ShardStore {
    return &ShardStore{db: db, slot: slot}
}
```

Implementation details:
- define stable top-level prefix constants for `/wk/state`, `/wk/index`, `/wk/meta`, and `/wk/raft`
- add `encodeStatePrefix`, `encodeIndexPrefix`, and `encodeMetaPrefix`
- keep encoding fully byte-sortable

- [ ] **Step 4: Re-run the targeted tests and confirm they pass**

Run: `go test ./wkdb -run 'Test(ForSlotReturnsShardStore|StateAndIndexPrefixesIncludeSlotAndSortStably)' -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add wkdb/db.go wkdb/shard.go wkdb/codec.go wkdb/codec_test.go
git commit -m "refactor: add wkdb slot-scoped keyspace"
```

## Task 2: Move user CRUD onto `ShardStore` and make it slot-isolated

**Files:**
- Modify: `wkdb/user.go`
- Modify: `wkdb/user_test.go`
- Modify: `wkdb/codec.go`

- [ ] **Step 1: Write the failing slot-isolation tests for user CRUD**

```go
func TestUserCRUDIsSlotScoped(t *testing.T) {
    db := openTestDB(t)
    left := db.ForSlot(1)
    right := db.ForSlot(2)

    require.NoError(t, left.CreateUser(ctx, User{UID: "u1", Token: "left"}))
    _, err := right.GetUser(ctx, "u1")
    require.ErrorIs(t, err, ErrNotFound)
}
```

Also add:
- create/get/update/delete through `ShardStore`
- duplicate create inside the same slot still returns `ErrAlreadyExists`
- same `uid` in different slots is allowed

- [ ] **Step 2: Run the targeted tests and confirm they fail**

Run: `go test ./wkdb -run 'Test(UserCRUDIsSlotScoped|CreateUserSameUIDAllowedAcrossSlots)' -count=1`

Expected: FAIL because user CRUD still uses the old DB-wide key layout.

- [ ] **Step 3: Rewrite user CRUD to operate on slot-prefixed primary keys**

```go
func (s *ShardStore) CreateUser(ctx context.Context, u User) error { ... }
func (s *ShardStore) GetUser(ctx context.Context, uid string) (User, error) { ... }
```

Implementation details:
- change user primary-key encoding to include `slot`
- remove or stop routing through DB-wide user CRUD methods
- keep existing validation/error semantics unless the spec explicitly changes them

- [ ] **Step 4: Re-run the targeted tests and confirm they pass**

Run: `go test ./wkdb -run 'Test(UserCRUDIsSlotScoped|CreateUserSameUIDAllowedAcrossSlots)' -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add wkdb/user.go wkdb/user_test.go wkdb/codec.go
git commit -m "refactor: scope wkdb user records by slot"
```

## Task 3: Move channel CRUD and index scans onto `ShardStore`

**Files:**
- Modify: `wkdb/channel.go`
- Modify: `wkdb/channel_test.go`
- Modify: `wkdb/codec.go`

- [ ] **Step 1: Write the failing slot-isolation and slot-index tests**

```go
func TestChannelIndexScanIsSlotScoped(t *testing.T) {
    db := openTestDB(t)
    left := db.ForSlot(1)
    right := db.ForSlot(2)

    require.NoError(t, left.CreateChannel(ctx, Channel{ChannelID: "c1", ChannelType: 1}))
    channels, err := right.ListChannelsByChannelID(ctx, "c1")
    require.NoError(t, err)
    require.Len(t, channels, 0)
}
```

Also add:
- duplicate create in one slot still fails
- same `(channel_id, channel_type)` across slots is allowed
- delete removes the slot-local secondary index entry

- [ ] **Step 2: Run the targeted tests and confirm they fail**

Run: `go test ./wkdb -run 'Test(ChannelIndexScanIsSlotScoped|DeleteChannelRemovesOnlySlotLocalIndex)' -count=1`

Expected: FAIL because channel records and indexes are still globally encoded.

- [ ] **Step 3: Rewrite channel CRUD and index scanning to use slot-prefixed keys**

```go
func (s *ShardStore) CreateChannel(ctx context.Context, ch Channel) error { ... }
func (s *ShardStore) ListChannelsByChannelID(ctx context.Context, channelID string) ([]Channel, error) { ... }
```

Implementation details:
- include `slot` in both primary and secondary index encodings
- keep index scanning prefix-based so the result remains sorted by Pebble key order
- ensure delete only removes keys under the shard’s slot prefixes

- [ ] **Step 4: Re-run the targeted tests and confirm they pass**

Run: `go test ./wkdb -run 'Test(ChannelIndexScanIsSlotScoped|DeleteChannelRemovesOnlySlotLocalIndex)' -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add wkdb/channel.go wkdb/channel_test.go wkdb/codec.go
git commit -m "refactor: scope wkdb channel records by slot"
```

## Task 4: Add slot-span helpers and destructive slot clearing

**Files:**
- Create: `wkdb/shard_spans.go`
- Create: `wkdb/snapshot_test.go`
- Modify: `wkdb/db.go`

- [ ] **Step 1: Write the failing span and delete tests**

```go
func TestDeleteSlotDataRemovesOnlyTargetSlot(t *testing.T) {
    db := openTestDB(t)
    left := db.ForSlot(1)
    right := db.ForSlot(2)

    require.NoError(t, left.CreateUser(ctx, User{UID: "u1"}))
    require.NoError(t, right.CreateUser(ctx, User{UID: "u1"}))

    require.NoError(t, db.DeleteSlotData(ctx, 1))

    _, err := left.GetUser(ctx, "u1")
    require.ErrorIs(t, err, ErrNotFound)
    _, err = right.GetUser(ctx, "u1")
    require.NoError(t, err)
}
```

Also add a span-coverage test that asserts state/index/meta spans are disjoint and ordered.

- [ ] **Step 2: Run the targeted tests and confirm they fail**

Run: `go test ./wkdb -run 'Test(DeleteSlotDataRemovesOnlyTargetSlot|SlotAllDataSpansAreOrderedAndDisjoint)' -count=1`

Expected: FAIL because slot span helpers and delete support do not exist yet.

- [ ] **Step 3: Implement slot-span helpers and `DeleteSlotData`**

```go
func slotAllDataSpans(slot uint64) []Span { ... }
func (db *DB) DeleteSlotData(ctx context.Context, slotID uint64) error { ... }
```

Implementation details:
- define a small internal span type compatible with prefix deletes / iterator bounds
- ensure `DeleteSlotData` only clears `/wk/state`, `/wk/index`, and `/wk/meta`
- do not touch `/wk/raft/<group>/...`

- [ ] **Step 4: Re-run the targeted tests and confirm they pass**

Run: `go test ./wkdb -run 'Test(DeleteSlotDataRemovesOnlyTargetSlot|SlotAllDataSpansAreOrderedAndDisjoint)' -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add wkdb/shard_spans.go wkdb/snapshot_test.go wkdb/db.go
git commit -m "feat: add wkdb slot span helpers"
```

## Task 5: Implement slot snapshot payload encoding and export/import round-trip

**Files:**
- Create: `wkdb/snapshot_codec.go`
- Create: `wkdb/snapshot.go`
- Modify: `wkdb/snapshot_test.go`
- Modify: `wkdb/db.go`

- [ ] **Step 1: Write the failing snapshot round-trip tests**

```go
func TestSlotSnapshotRoundTrip(t *testing.T) {
    db := openTestDB(t)
    shard := db.ForSlot(9)
    require.NoError(t, shard.CreateUser(ctx, User{UID: "u1", Token: "t"}))
    require.NoError(t, shard.CreateChannel(ctx, Channel{ChannelID: "c1", ChannelType: 1, Ban: 1}))

    snap, err := db.ExportSlotSnapshot(ctx, 9)
    require.NoError(t, err)

    require.NoError(t, db.DeleteSlotData(ctx, 9))
    require.NoError(t, db.ImportSlotSnapshot(ctx, snap))

    gotUser, err := shard.GetUser(ctx, "u1")
    require.NoError(t, err)
    require.Equal(t, "t", gotUser.Token)
}
```

Also add:
- checksum mismatch test
- slot mismatch rejection test
- import retry test: failed import can be retried with the same snapshot and eventually succeeds

- [ ] **Step 2: Run the targeted tests and confirm they fail**

Run: `go test ./wkdb -run 'Test(SlotSnapshotRoundTrip|ImportSlotSnapshotRejectsWrongSlot|ImportSlotSnapshotRejectsChecksumMismatch|ImportSlotSnapshotCanBeRetried)' -count=1`

Expected: FAIL because snapshot payload encoding and export/import APIs do not exist yet.

- [ ] **Step 3: Implement snapshot payload codec plus export/import APIs**

```go
type SlotSnapshot struct {
    SlotID uint64
    Data   []byte
    Stats  SnapshotStats
}

func (db *DB) ExportSlotSnapshot(ctx context.Context, slotID uint64) (SlotSnapshot, error) { ... }
func (db *DB) ImportSlotSnapshot(ctx context.Context, snap SlotSnapshot) error { ... }
```

Implementation details:
- use a Pebble snapshot while exporting
- iterate the slot spans in order and write key/value pairs in stable order
- validate checksum and slot ID during import
- clear target slot data before every import attempt

- [ ] **Step 4: Re-run the targeted tests and confirm they pass**

Run: `go test ./wkdb -run 'Test(SlotSnapshotRoundTrip|ImportSlotSnapshotRejectsWrongSlot|ImportSlotSnapshotRejectsChecksumMismatch|ImportSlotSnapshotCanBeRetried)' -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add wkdb/snapshot_codec.go wkdb/snapshot.go wkdb/snapshot_test.go wkdb/db.go
git commit -m "feat: add wkdb slot snapshot export and import"
```

## Task 6: Implement `wkdbStateMachine` on top of slot snapshot APIs

**Files:**
- Create: `wkdb/raft_state_machine.go`
- Create: `wkdb/raft_state_machine_test.go`

- [ ] **Step 1: Write the failing state-machine tests**

```go
func TestWKDBStateMachineSnapshotRestoreRoundTrip(t *testing.T) {
    db := openTestDB(t)
    sm := NewStateMachine(db, 11)

    // Apply one encoded business command, then snapshot and restore into a new DB.
}
```

At minimum cover:
- `Snapshot()` returns non-empty bytes after data is written
- `Restore()` rehydrates the same slot into a fresh DB
- `Snapshot()` only includes the configured slot

- [ ] **Step 2: Run the targeted tests and confirm they fail**

Run: `go test ./wkdb -run 'TestWKDBStateMachine(SnapshotRestoreRoundTrip|SnapshotIsSlotScoped)' -count=1`

Expected: FAIL because the state-machine adapter does not exist yet.

- [ ] **Step 3: Implement `multiraft.StateMachine` using `wkdb` slot operations**

```go
type wkdbStateMachine struct {
    db   *DB
    slot uint64
}

func NewStateMachine(db *DB, slot uint64) multiraft.StateMachine
func (m *wkdbStateMachine) Snapshot(ctx context.Context) (multiraft.Snapshot, error) { ... }
func (m *wkdbStateMachine) Restore(ctx context.Context, snap multiraft.Snapshot) error { ... }
```

Implementation details:
- keep `Apply` minimal and explicit; do not invent a generic command DSL beyond what is necessary for tests
- `Snapshot().Data` should directly carry the slot snapshot payload
- `Restore()` must call the same import path used by direct `wkdb` tests

- [ ] **Step 4: Re-run the targeted tests and confirm they pass**

Run: `go test ./wkdb -run 'TestWKDBStateMachine(SnapshotRestoreRoundTrip|SnapshotIsSlotScoped)' -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add wkdb/raft_state_machine.go wkdb/raft_state_machine_test.go
git commit -m "feat: add wkdb multiraft state machine"
```

## Task 7: Implement `wkdbRaftStorage` for `multiraft.Storage`

**Files:**
- Create: `wkdb/raft_storage.go`
- Create: `wkdb/raft_storage_test.go`

- [ ] **Step 1: Write the failing raft-storage tests**

```go
func TestWKDBRaftStorageSaveAndLoadRoundTrip(t *testing.T) {
    db := openTestDB(t)
    store := NewRaftStorage(db, 13)

    // Save HardState, Entries, and Snapshot; then assert InitialState/Entries/Snapshot reload them.
}
```

Also cover:
- `MarkApplied` persistence
- `Entries(lo, hi)` window semantics
- snapshot metadata remains separate from business snapshot spans

- [ ] **Step 2: Run the targeted tests and confirm they fail**

Run: `go test ./wkdb -run 'TestWKDBRaftStorage(SaveAndLoadRoundTrip|MarkAppliedPersists|EntriesWindowing)' -count=1`

Expected: FAIL because the raft storage adapter does not exist yet.

- [ ] **Step 3: Implement `multiraft.Storage` using `/wk/raft/<group>/...` keys**

```go
type wkdbRaftStorage struct {
    db    *DB
    group uint64
}

func NewRaftStorage(db *DB, group uint64) multiraft.Storage
```

Implementation details:
- implement `InitialState`, `Entries`, `Term`, `FirstIndex`, `LastIndex`, `Snapshot`, `Save`, and `MarkApplied`
- store raft keys under the dedicated raft prefix only
- do not reuse business snapshot payload as the storage-layer snapshot record

- [ ] **Step 4: Re-run the targeted tests and confirm they pass**

Run: `go test ./wkdb -run 'TestWKDBRaftStorage(SaveAndLoadRoundTrip|MarkAppliedPersists|EntriesWindowing)' -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add wkdb/raft_storage.go wkdb/raft_storage_test.go
git commit -m "feat: add wkdb multiraft storage"
```

## Task 8: Add end-to-end `multiraft` integration coverage using `wkdb`

**Files:**
- Create: `multiraft/wkdb_integration_test.go`
- Reference: `wkdb/raft_state_machine.go`
- Reference: `wkdb/raft_storage.go`

- [ ] **Step 1: Write the failing end-to-end recovery test**

```go
func TestWKDBBackedGroupRestoresStateFromSnapshotOnReopen(t *testing.T) {
    // Bootstrap a group with wkdb-backed storage + state machine,
    // apply data, persist a snapshot, reopen, and assert the state is restored.
}
```

At minimum cover:
- bootstrap one local group
- write business data through the state machine
- persist raft state and snapshot
- reopen group
- verify restored data is present

- [ ] **Step 2: Run the targeted test and confirm it fails**

Run: `go test ./multiraft -run TestWKDBBackedGroupRestoresStateFromSnapshotOnReopen -count=1`

Expected: FAIL because the wkdb-backed adapters are not wired into an end-to-end path yet.

- [ ] **Step 3: Wire the test to use the new `wkdb` adapters without changing `multiraft` public APIs**

Implementation details:
- use `wkdb.NewRaftStorage(...)` and `wkdb.NewStateMachine(...)` directly from the test
- prefer a single-node cluster bootstrap to keep the scenario deterministic
- verify both snapshot restore and post-restore reads

- [ ] **Step 4: Re-run the targeted test and confirm it passes**

Run: `go test ./multiraft -run TestWKDBBackedGroupRestoresStateFromSnapshotOnReopen -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add multiraft/wkdb_integration_test.go wkdb/raft_state_machine.go wkdb/raft_storage.go
git commit -m "test: cover wkdb-backed multiraft recovery"
```

## Task 9: Full verification and final cleanup

**Files:**
- Modify: `wkdb/*.go`
- Modify: `multiraft/wkdb_integration_test.go`
- Reference: `docs/superpowers/specs/2026-03-26-wkdb-slot-shard-snapshot-design.md`

- [ ] **Step 1: Run the full `wkdb` package tests**

Run: `go test ./wkdb -count=1`

Expected: PASS

- [ ] **Step 2: Run the focused `multiraft` integration tests**

Run: `go test ./multiraft -run 'TestWKDBBackedGroupRestoresStateFromSnapshotOnReopen|TestOpenGroupRestoresSnapshotIntoStateMachine' -count=1`

Expected: PASS

- [ ] **Step 3: Run broader repository verification**

Run: `go test ./... -count=1`

Expected: PASS, or only clearly unrelated pre-existing failures that are documented before finishing.

- [ ] **Step 4: Re-check the spec against the implementation**

Confirm all of the following:
- all business keys are slot-prefixed
- no DB-level global business CRUD path remains in active use
- slot snapshot only covers state/index/meta spans
- raft persistence is stored separately under raft prefixes
- export/import round-trip is tested
- wkdb-backed multiraft recovery is tested

- [ ] **Step 5: Commit**

```bash
git add wkdb multiraft/wkdb_integration_test.go docs/superpowers/plans/2026-03-26-wkdb-slot-shard-snapshot-implementation.md
git commit -m "feat: add wkdb slot shard snapshot support"
```
