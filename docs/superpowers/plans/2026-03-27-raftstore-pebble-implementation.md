# Raftstore Pebble Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a shared Pebble-backed `raftstore` backend that durably persists `multiraft.Storage` state across process restarts while keeping Raft storage physically separate from `wkdb`.

**Architecture:** Introduce `raftstore.DB` as the lifecycle owner of one Pebble instance per process and expose `ForGroup(group)` views that implement `multiraft.Storage` through stable binary key prefixes. Keep `multiraft` runtime ordering unchanged, preserve the existing in-memory backend, and add integration tests that prove clean reopen works while explicitly documenting the current “no business-state rebuild without snapshot” limitation.

**Tech Stack:** Go 1.23, Pebble (`github.com/cockroachdb/pebble`), `go.etcd.io/raft/v3/raftpb`, `github.com/WuKongIM/WuKongIM/pkg/consensus/multiraft`, `github.com/WuKongIM/WuKongIM/pkg/wkfsm`, Go testing

---

## File Structure

### Production files

- Create: `raftstore/pebble.go`
  Responsibility: define `raftstore.DB`, manage Pebble lifecycle, implement the group-scoped `multiraft.Storage`, and persist `HardState`, `Entries`, `Snapshot`, and `AppliedIndex`.
- Create: `raftstore/pebble_codec.go`
  Responsibility: encode/decode stable binary keys for group metadata and entry index ranges, plus shared scan/range helpers.
- Modify: `AGENTS.md`
  Responsibility: update the `raftstore` package description so it no longer claims the package is memory-only.

### Test files

- Create: `raftstore/pebble_test.go`
  Responsibility: cover DB open/close, key ordering, group isolation, `Save` round-trip, log replacement, snapshot trimming, `MarkApplied`, reopen recovery, and deep-copy semantics.
- Modify: `wkfsm/testutil_test.go`
  Responsibility: add helpers for persistent `wkdb` and `raftstore` paths so integration tests can close and reopen both databases.
- Modify: `wkfsm/integration_test.go`
  Responsibility: add persistent-backend runtime coverage for clean reopen and for the current limitation where deleting business data is not repaired by Raft alone without a snapshot.

### Existing specs and support docs

- Reference: `docs/superpowers/specs/2026-03-27-raftstore-pebble-design.md`
  Responsibility: approved architecture, key layout, persistence semantics, and crash-recovery model.
- Reference: `raftstore/memory.go`
  Responsibility: semantic baseline for `Entries`, `Term`, `FirstIndex`, `LastIndex`, and `ConfState` derivation.
- Reference: `wkfsm/integration_test.go`
  Responsibility: existing runtime test style and helper patterns.

## Implementation Notes

- Follow `@superpowers:test-driven-development` on every code-bearing task: write the failing test, run it red, implement the minimum code, run it green.
- Do not change the `multiraft.Storage` interface.
- Do not make `raftstore` depend on `wkdb`; physical separation is part of the design.
- Keep `raftstore.NewMemory()` unchanged and fully supported.
- `raftstore.Open(path)` must own exactly one Pebble instance for all groups in the process. Do not create one DB per group.
- Use big-endian binary encoding for `group` and `index` so entry keys sort naturally.
- `Save()` and `MarkApplied()` must sync to disk with `pebble.Sync`.
- `InitialState()` must derive `ConfState` the same way `raftstore.NewMemory()` does: from snapshot metadata when present, otherwise from committed conf-change entries.
- Do not add `TruncatedState` behavior yet; only reserve the key type and keep the implementation focused on phase-1 persistence.
- Keep `wkdb.DeleteSlotData()` business-only. The integration plan should explicitly prove that deleting business slot data after a clean close is not recovered by the Raft DB alone until snapshot generation exists.

## Task 1: Add the Pebble DB surface and key codec

**Files:**
- Create: `raftstore/pebble.go`
- Create: `raftstore/pebble_codec.go`
- Create: `raftstore/pebble_test.go`

- [ ] **Step 1: Write the failing DB-surface and key-ordering tests**

```go
func TestPebbleOpenForGroupReturnsStorage(t *testing.T) {
    db, err := raftstore.Open(filepath.Join(t.TempDir(), "raft"))
    if err != nil {
        t.Fatalf("Open() error = %v", err)
    }
    t.Cleanup(func() { _ = db.Close() })

    if db.ForGroup(7) == nil {
        t.Fatal("ForGroup(7) returned nil storage")
    }
}

func TestPebbleEntryKeysSortByGroupAndIndex(t *testing.T) {
    a := encodeEntryKey(7, 5)
    b := encodeEntryKey(7, 6)
    c := encodeEntryKey(8, 1)

    if bytes.Compare(a, b) >= 0 {
        t.Fatalf("entry keys for one group did not sort by index")
    }
    if bytes.Compare(b, c) >= 0 {
        t.Fatalf("entry keys did not sort by group before index")
    }
}
```

- [ ] **Step 2: Run the targeted tests and confirm they fail**

Run: `go test ./raftstore -run 'TestPebble(OpenForGroupReturnsStorage|EntryKeysSortByGroupAndIndex)' -count=1`

Expected: FAIL because `Open`, `ForGroup`, and the Pebble key helpers do not exist yet.

- [ ] **Step 3: Implement the Pebble DB skeleton and key helpers**

```go
type DB struct {
    db *pebble.DB
}

func Open(path string) (*DB, error) {
    pdb, err := pebble.Open(path, &pebble.Options{})
    if err != nil {
        return nil, err
    }
    return &DB{db: pdb}, nil
}

func (db *DB) ForGroup(group uint64) multiraft.Storage {
    return &pebbleStore{db: db, group: group}
}
```

Implementation details:
- add fixed key-type bytes for `HardState`, `AppliedIndex`, `Snapshot`, `Entry`, and reserved `TruncatedState`
- encode `group` and `index` using `binary.BigEndian`
- add helper functions for the group metadata prefix, entry key, and entry prefix span
- keep the storage method bodies minimal for now if later tasks will flesh them out

- [ ] **Step 4: Re-run the targeted tests and confirm they pass**

Run: `go test ./raftstore -run 'TestPebble(OpenForGroupReturnsStorage|EntryKeysSortByGroupAndIndex)' -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add raftstore/pebble.go raftstore/pebble_codec.go raftstore/pebble_test.go
git commit -m "feat: add pebble raftstore skeleton"
```

## Task 2: Persist `HardState`, `Snapshot`, and `AppliedIndex`

**Files:**
- Modify: `raftstore/pebble.go`
- Modify: `raftstore/pebble_test.go`

- [ ] **Step 1: Write the failing round-trip tests for metadata persistence**

```go
func TestPebbleStateRoundTripAcrossReopen(t *testing.T) {
    path := filepath.Join(t.TempDir(), "raft")

    db, err := raftstore.Open(path)
    if err != nil {
        t.Fatalf("Open() error = %v", err)
    }
    store := db.ForGroup(9)

    hs := raftpb.HardState{Term: 2, Vote: 1, Commit: 7}
    snap := raftpb.Snapshot{
        Data: []byte("snap"),
        Metadata: raftpb.SnapshotMetadata{
            Index: 7,
            Term:  2,
            ConfState: raftpb.ConfState{
                Voters: []uint64{1, 2},
            },
        },
    }
    if err := store.Save(context.Background(), multiraft.PersistentState{
        HardState: &hs,
        Snapshot:  &snap,
    }); err != nil {
        t.Fatalf("Save() error = %v", err)
    }
    if err := store.MarkApplied(context.Background(), 7); err != nil {
        t.Fatalf("MarkApplied() error = %v", err)
    }
    if err := db.Close(); err != nil {
        t.Fatalf("Close() error = %v", err)
    }

    reopened, err := raftstore.Open(path)
    if err != nil {
        t.Fatalf("reopen Open() error = %v", err)
    }
    t.Cleanup(func() { _ = reopened.Close() })

    state, err := reopened.ForGroup(9).InitialState(context.Background())
    if err != nil {
        t.Fatalf("InitialState() error = %v", err)
    }
    if !reflect.DeepEqual(state.HardState, hs) {
        t.Fatalf("HardState = %#v, want %#v", state.HardState, hs)
    }
    if state.AppliedIndex != 7 {
        t.Fatalf("AppliedIndex = %d, want 7", state.AppliedIndex)
    }
}
```

Also add:
- snapshot round-trip through `Snapshot()`
- `ConfState` coming from `snapshot.Metadata.ConfState`
- group isolation, proving group `9` state does not leak into group `10`

- [ ] **Step 2: Run the targeted tests and confirm they fail**

Run: `go test ./raftstore -run 'TestPebble(StateRoundTripAcrossReopen|SnapshotRoundTrip|GroupsAreIndependent)' -count=1`

Expected: FAIL because the Pebble backend does not yet persist or reload Raft metadata.

- [ ] **Step 3: Implement metadata persistence and reload**

```go
func (s *pebbleStore) InitialState(ctx context.Context) (multiraft.BootstrapState, error) { ... }
func (s *pebbleStore) Snapshot(ctx context.Context) (raftpb.Snapshot, error) { ... }
func (s *pebbleStore) MarkApplied(ctx context.Context, index uint64) error { ... }
```

Implementation details:
- store `HardState` and `Snapshot` as protobuf values
- store `AppliedIndex` as an 8-byte big-endian integer
- in `InitialState()`, load snapshot first and derive `ConfState` from `snapshot.Metadata.ConfState` when it exists
- keep all returned protobuf/message data deep-copied
- `MarkApplied()` must use `Commit(pebble.Sync)`

- [ ] **Step 4: Re-run the targeted tests and confirm they pass**

Run: `go test ./raftstore -run 'TestPebble(StateRoundTripAcrossReopen|SnapshotRoundTrip|GroupsAreIndependent)' -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add raftstore/pebble.go raftstore/pebble_test.go
git commit -m "feat: persist raft metadata in pebble"
```

## Task 3: Persist log entries and match the in-memory storage semantics

**Files:**
- Modify: `raftstore/pebble.go`
- Modify: `raftstore/pebble_codec.go`
- Modify: `raftstore/pebble_test.go`

- [ ] **Step 1: Write the failing log-windowing and replacement tests**

```go
func TestPebbleSaveEntriesReplacesTailFromFirstIndex(t *testing.T) {
    ctx := context.Background()
    store := openTestPebbleStore(t, 11)

    initial := []raftpb.Entry{
        {Index: 5, Term: 1, Data: []byte("a")},
        {Index: 6, Term: 2, Data: []byte("b")},
        {Index: 7, Term: 2, Data: []byte("c")},
    }
    require.NoError(t, store.Save(ctx, multiraft.PersistentState{Entries: initial}))

    replacement := []raftpb.Entry{
        {Index: 6, Term: 3, Data: []byte("bb")},
        {Index: 7, Term: 3, Data: []byte("cc")},
        {Index: 8, Term: 3, Data: []byte("dd")},
    }
    require.NoError(t, store.Save(ctx, multiraft.PersistentState{Entries: replacement}))

    got, err := store.Entries(ctx, 5, 9, 0)
    require.NoError(t, err)
    require.Equal(t, []raftpb.Entry{
        initial[0], replacement[0], replacement[1], replacement[2],
    }, got)
}
```

Also add:
- `Save(snapshot)` deleting all `index <= snapshot.Metadata.Index`
- `Entries(lo, hi, maxSize)` respecting `maxSize` the same way `raftstore.NewMemory()` does
- `Term(index)` returning the snapshot term exactly at `snapshot.Metadata.Index`
- `FirstIndex()` / `LastIndex()` matching the current memory-store contract
- reopen round-trip after writing entries, proving data survives a fresh `Open()`
- mutating slices returned by `Entries()` or `Snapshot()` does not corrupt stored data

- [ ] **Step 2: Run the targeted tests and confirm they fail**

Run: `go test ./raftstore -run 'TestPebble(SaveEntriesReplacesTailFromFirstIndex|SaveSnapshotTrimsCoveredEntries|EntriesWindowingAndTerm|ReturnsClonedData)' -count=1`

Expected: FAIL because entry persistence, scans, and deletion semantics are not implemented yet.

- [ ] **Step 3: Implement the Pebble log storage methods**

```go
func (s *pebbleStore) Entries(ctx context.Context, lo, hi, maxSize uint64) ([]raftpb.Entry, error) { ... }
func (s *pebbleStore) Term(ctx context.Context, index uint64) (uint64, error) { ... }
func (s *pebbleStore) FirstIndex(ctx context.Context) (uint64, error) { ... }
func (s *pebbleStore) LastIndex(ctx context.Context) (uint64, error) { ... }
func (s *pebbleStore) Save(ctx context.Context, st multiraft.PersistentState) error { ... }
```

Implementation details:
- write `Entries` and `HardState` in one Pebble batch and `Commit(pebble.Sync)`
- when `st.Snapshot != nil`, write the snapshot and delete the entry range `<= snapshot.Metadata.Index`
- when `len(st.Entries) > 0`, delete the entry range starting at `st.Entries[0].Index` before writing the new suffix
- derive `ConfState` from committed conf-change entries when there is no snapshot, using the same helper logic as the memory store
- keep entry iteration contiguous and byte-copy all returned `Entry.Data`

- [ ] **Step 4: Re-run the targeted tests and confirm they pass**

Run: `go test ./raftstore -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add raftstore/pebble.go raftstore/pebble_codec.go raftstore/pebble_test.go
git commit -m "feat: persist raft log entries in pebble"
```

## Task 4: Add persistent runtime integration coverage in `wkfsm`

**Files:**
- Modify: `wkfsm/testutil_test.go`
- Modify: `wkfsm/integration_test.go`

- [ ] **Step 1: Write the failing persistent-backend integration tests**

```go
func TestPebbleBackedGroupReopensAndAcceptsNewProposal(t *testing.T) {
    ctx := context.Background()
    groupID := multiraft.GroupID(61)
    bizPath := filepath.Join(t.TempDir(), "biz")
    raftPath := filepath.Join(t.TempDir(), "raft")

    bizDB := openTestDBAt(t, bizPath)
    raftDB := openTestRaftDBAt(t, raftPath)

    rt := newStartedRuntime(t)
    require.NoError(t, rt.BootstrapGroup(ctx, multiraft.BootstrapGroupRequest{
        Group: multiraft.GroupOptions{
            ID:           groupID,
            Storage:      raftDB.ForGroup(uint64(groupID)),
            StateMachine: New(bizDB, uint64(groupID)),
        },
        Voters: []multiraft.NodeID{1},
    }))

    // propose once, close runtime and both DBs, reopen both DBs and runtime,
    // open the same group, propose again, assert the second token wins.
}

func TestPebbleBackedGroupDoesNotRecoverDeletedBusinessStateWithoutSnapshot(t *testing.T) {
    // bootstrap a persistent group, apply one command, close cleanly,
    // delete the business slot data, reopen with the same raft DB,
    // and assert the row is still missing because no snapshot replay exists yet.
}
```

- [ ] **Step 2: Run the targeted tests and confirm they fail**

Run: `go test ./wkfsm -run 'TestPebbleBackedGroup' -count=1`

Expected: FAIL because there are no persistent-backend helpers or tests yet.

- [ ] **Step 3: Add persistent test helpers and wire the new backend into the runtime tests**

```go
func openTestDBAt(t *testing.T, path string) *wkdb.DB { ... }

func openTestRaftDBAt(t *testing.T, path string) *raftstore.DB { ... }
```

Implementation details:
- keep the existing memory-backed integration tests intact
- for the reopen test, close the runtime and both DBs before reopening to mimic a real process restart
- assert the reopened group becomes leader again before the second proposal
- in the limitation test, call `db.DeleteSlotData(ctx, slot)` after the clean close and assert the reopened group does not rebuild the row
- do not try to add snapshot generation to make the limitation test pass; the point is to document current behavior

- [ ] **Step 4: Re-run the targeted tests and confirm they pass**

Run: `go test ./wkfsm -run 'Test(MemoryBackedGroup|PebbleBackedGroup)' -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add wkfsm/testutil_test.go wkfsm/integration_test.go
git commit -m "test: cover pebble-backed runtime reopen"
```

## Task 5: Update package docs and run full verification

**Files:**
- Modify: `AGENTS.md`
- Reference: `docs/superpowers/specs/2026-03-27-raftstore-pebble-design.md`

- [ ] **Step 1: Update the live package description**

```md
- `raftstore` — `multiraft.Storage` implementations, with in-memory and Pebble-backed backends
```

Also keep the existing rule that Raft keyspace remains separate from business keyspace.

- [ ] **Step 2: Run package-focused verification**

Run: `go test ./raftstore ./wkfsm ./multiraft -count=1`

Expected: PASS

- [ ] **Step 3: Run the full repository test suite**

Run: `go test ./... -count=1`

Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add AGENTS.md
git commit -m "docs: update raftstore package description"
```

- [ ] **Step 5: Prepare execution handoff notes**

Record:
- `raftstore.DB` is shared across groups, not per-group
- clean process restart is supported when both Raft DB and business DB persist
- deleting business slot data is still unrecoverable without snapshot generation
- the next follow-up after this plan is snapshot production and `TruncatedState`, not sideloading or Cockroach-style footer metadata
