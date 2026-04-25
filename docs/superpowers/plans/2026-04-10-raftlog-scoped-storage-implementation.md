# Raftlog Scoped Storage Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rebuild `pkg/raftlog` as a scope-based raft storage backend so slot raft and controller raft are independent first-class storage spaces with a versioned Pebble key schema.

**Architecture:** Introduce an explicit `Scope` model and make `DB.For(scope)` the fundamental lookup API, with `ForSlot` and `ForController` as convenience wrappers. Replace the current `group-only` key layout with a versioned schema plus a manifest, then split the Pebble implementation into focused files for DB lifecycle, scope-local storage views, read helpers, write batching, and metadata derivation.

**Tech Stack:** Go 1.23, `github.com/cockroachdb/pebble/v2`, `go.etcd.io/raft/v3/raftpb`, `github.com/WuKongIM/WuKongIM/pkg/slot/multiraft`, `testing`, `testify/require`, `gofmt`.

---

## File Structure Map

### New files

- Create: `pkg/raftlog/scope.go`
- Create: `pkg/raftlog/types.go`
- Create: `pkg/raftlog/codec.go`
- Create: `pkg/raftlog/meta.go`
- Create: `pkg/raftlog/pebble_db.go`
- Create: `pkg/raftlog/pebble_store.go`
- Create: `pkg/raftlog/pebble_reader.go`
- Create: `pkg/raftlog/pebble_writer.go`

### Existing files to keep and update

- Modify: `pkg/raftlog/memory.go`
- Modify: `pkg/raftlog/memory_test.go`
- Modify: `pkg/raftlog/pebble_test.go`
- Modify: `pkg/raftlog/pebble_test_helpers_test.go`
- Modify: `pkg/raftlog/pebble_benchmark_test.go`
- Modify: `pkg/raftlog/pebble_stress_test.go`
- Modify: `pkg/controller/raft/service.go`
- Modify: `pkg/controller/raft/service_test.go`

### Legacy files to remove after the split

- Delete: `pkg/raftlog/helpers.go`
- Delete: `pkg/raftlog/pebble.go`
- Delete: `pkg/raftlog/pebble_codec.go`

### Responsibility targets

- `pkg/raftlog/scope.go`: `ScopeKind`, `Scope`, constructors, validation, `String()`
- `pkg/raftlog/types.go`: `logMeta`, manifest payload types, fixed-size encoding constants
- `pkg/raftlog/codec.go`: key prefixes, manifest key, metadata keys, entry keys, prefix ranges
- `pkg/raftlog/meta.go`: clone helpers, entry replacement/snapshot trimming, derived conf-state and metadata logic
- `pkg/raftlog/pebble_db.go`: open/close, manifest validation, write worker lifecycle, store factory methods
- `pkg/raftlog/pebble_store.go`: `multiraft.Storage` implementation for one scope
- `pkg/raftlog/pebble_reader.go`: low-level Pebble reads for one scope
- `pkg/raftlog/pebble_writer.go`: batched write requests, scope write state, apply logic

### Compatibility rule

- Do **not** preserve the old `groupID`-only Pebble layout
- Do **not** leave `controllerGroupStorageID` or any fake controller-slot constant in public use
- Do **not** keep a single oversized `pkg/raftlog/pebble.go`
- Do **not** add a temporary compatibility shim that reinterprets controller raft as a slot

## Task 1: Introduce `Scope` and the versioned key codec

**Files:**
- Create: `pkg/raftlog/scope.go`
- Create: `pkg/raftlog/types.go`
- Create: `pkg/raftlog/codec.go`
- Modify: `pkg/raftlog/pebble_test.go`
- Modify: `pkg/raftlog/pebble_test_helpers_test.go`
- Test: `pkg/raftlog/pebble_test.go`

- [ ] **Step 1: Write the failing scope and codec tests**

Add or update tests with these target behaviors:

```go
func TestPebbleForControllerReturnsStorage(t *testing.T) {
    db, _ := Open(t.TempDir())
    t.Cleanup(func() { _ = db.Close() })

    if db.ForController() == nil {
        t.Fatal("ForController() returned nil storage")
    }
}

func TestScopeEntryKeysSortByVersionScopeAndIndex(t *testing.T) {
    a := encodeEntryKey(SlotScope(7), 5)
    b := encodeEntryKey(SlotScope(7), 6)
    c := encodeEntryKey(SlotScope(8), 1)
    d := encodeEntryKey(ControllerScope(), 1)

    require.Less(t, bytes.Compare(a, b), 0)
    require.Less(t, bytes.Compare(a, c), 0)
    require.NotZero(t, bytes.Compare(a, d))
}

func TestScopeString(t *testing.T) {
    require.Equal(t, "slot/7", SlotScope(7).String())
    require.Equal(t, "controller/1", ControllerScope().String())
}
```

- [ ] **Step 2: Run the focused tests to verify RED**

Run: `go test ./pkg/raftlog -run 'Test(PebbleForControllerReturnsStorage|ScopeEntryKeysSortByVersionScopeAndIndex|ScopeString)'`
Expected: FAIL because `ForController`, `Scope`, `SlotScope`, `ControllerScope`, and the new `encodeEntryKey` shape do not exist yet.

- [ ] **Step 3: Implement `Scope`, `logMeta`, and the new codec surface**

Create these target shapes:

```go
type ScopeKind uint8

const (
    ScopeSlot ScopeKind = 1
    ScopeController ScopeKind = 2
)

type Scope struct {
    Kind ScopeKind
    ID   uint64
}

func SlotScope(slotID uint64) Scope { return Scope{Kind: ScopeSlot, ID: slotID} }
func ControllerScope() Scope { return Scope{Kind: ScopeController, ID: 1} }
```

And start the new key schema in `codec.go`:

```go
const currentFormatVersion byte = 0x01

func encodeScopePrefix(scope Scope) []byte
func encodeMetaKey(scope Scope, recordType byte) []byte
func encodeEntryKey(scope Scope, index uint64) []byte
func encodeEntryPrefix(scope Scope) []byte
func encodeEntryPrefixEnd(scope Scope) []byte
func encodeManifestKey() []byte
```

Use the on-disk layout from the spec exactly:

```text
[formatVersion:1][scopeKind:1][scopeID:8][recordType:1][optional index:8]
```

- [ ] **Step 4: Add the new DB lookup helpers without changing call sites yet**

Target shape in the DB surface:

```go
func (db *DB) For(scope Scope) multiraft.Storage
func (db *DB) ForSlot(slotID uint64) multiraft.Storage { return db.For(SlotScope(slotID)) }
func (db *DB) ForController() multiraft.Storage { return db.For(ControllerScope()) }
```

Wire these methods to the existing store implementation temporarily if needed, but the lookup key must already be `Scope`, not raw `uint64`.

- [ ] **Step 5: Update the test helpers to write new-style keys**

Rewrite the helper code in `pkg/raftlog/pebble_test_helpers_test.go` so legacy state injection and metadata existence checks use `Scope`-aware helpers:

```go
func mustWriteLegacyPebbleState(tb testing.TB, path string, scope Scope, state legacyPebbleState)
func hasScopeMetadata(tb testing.TB, db *DB, scope Scope) bool
```

Use `SlotScope(group)` for the old slot-oriented tests while keeping the helper names accurate.

- [ ] **Step 6: Run the focused tests again**

Run: `go test ./pkg/raftlog -run 'Test(PebbleForControllerReturnsStorage|ScopeEntryKeysSortByVersionScopeAndIndex|ScopeString)'`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add pkg/raftlog/scope.go pkg/raftlog/types.go pkg/raftlog/codec.go pkg/raftlog/pebble_test.go pkg/raftlog/pebble_test_helpers_test.go
git commit -m "refactor: add scoped raftlog identity and key codec"
```

## Task 2: Move metadata logic to scope-neutral helpers and update the in-memory store

**Files:**
- Create: `pkg/raftlog/meta.go`
- Modify: `pkg/raftlog/memory.go`
- Modify: `pkg/raftlog/memory_test.go`
- Delete: `pkg/raftlog/helpers.go`
- Test: `pkg/raftlog/memory_test.go`

- [ ] **Step 1: Write the failing memory tests for controller-neutral metadata semantics**

Keep the existing behavior coverage, but rename/add tests so they describe log behavior rather than group behavior. At minimum preserve:

```go
func TestMemoryInitialStateDerivesConfStateWithoutSnapshot(t *testing.T)
func TestMemoryInitialStateAppliesPostSnapshotConfChanges(t *testing.T)
func TestMemoryMarkAppliedUpdatesBootstrapState(t *testing.T)
```

Add a small pure-helper test in `memory_test.go` or a new `meta_test.go` block:

```go
func TestUpdateLogMetaUsesSnapshotWhenEntriesAreTrimmed(t *testing.T) {
    meta := logMeta{AppliedIndex: 8}
    snap := raftpb.Snapshot{Metadata: raftpb.SnapshotMetadata{Index: 8, Term: 2}}

    require.NoError(t, updateLogMeta(&meta, snap, nil, 8))
    require.Equal(t, uint64(9), meta.FirstIndex)
    require.Equal(t, uint64(8), meta.LastIndex)
}
```

- [ ] **Step 2: Run the memory tests to verify RED**

Run: `go test ./pkg/raftlog -run 'TestMemory|TestUpdateLogMetaUsesSnapshotWhenEntriesAreTrimmed'`
Expected: FAIL because `logMeta` and `updateLogMeta` do not exist yet.

- [ ] **Step 3: Move clone and derivation helpers into `meta.go`**

Port the logic from `helpers.go` and the metadata section of `pebble.go` into focused helpers:

```go
func cloneEntry(entry raftpb.Entry) raftpb.Entry
func cloneSnapshot(snapshot raftpb.Snapshot) raftpb.Snapshot
func cloneConfState(state raftpb.ConfState) raftpb.ConfState
func replaceEntriesFromIndex(existing []raftpb.Entry, first uint64, incoming []raftpb.Entry) []raftpb.Entry
func trimEntriesAfterSnapshot(existing []raftpb.Entry, snapshotIndex uint64) []raftpb.Entry
func deriveConfState(snapshot raftpb.Snapshot, entries []raftpb.Entry, committed uint64) (raftpb.ConfState, error)
func updateLogMeta(meta *logMeta, snapshot raftpb.Snapshot, entries []raftpb.Entry, committed uint64) error
```

Rename everything away from `group` while keeping the behavior identical.

- [ ] **Step 4: Update the memory store to use `logMeta`-aligned helper names**

Do not change the `NewMemory() multiraft.Storage` API. Just point `memory.go` at the new helper names and keep the behavior identical:

```go
confState, err := deriveConfState(m.snapshot, m.entries, m.hardState.Commit)
```

The memory implementation should remain one-scope storage and does not need a DB wrapper.

- [ ] **Step 5: Delete the legacy helper file**

Remove `pkg/raftlog/helpers.go` once all helper references move to `meta.go`.

- [ ] **Step 6: Run the memory test subset again**

Run: `go test ./pkg/raftlog -run 'TestMemory|TestUpdateLogMetaUsesSnapshotWhenEntriesAreTrimmed'`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add pkg/raftlog/meta.go pkg/raftlog/memory.go pkg/raftlog/memory_test.go
git rm pkg/raftlog/helpers.go
git commit -m "refactor: extract scope-neutral raftlog metadata helpers"
```

## Task 3: Split the Pebble DB and read path, and add manifest validation

**Files:**
- Create: `pkg/raftlog/pebble_db.go`
- Create: `pkg/raftlog/pebble_store.go`
- Create: `pkg/raftlog/pebble_reader.go`
- Modify: `pkg/raftlog/pebble_test.go`
- Modify: `pkg/raftlog/pebble_test_helpers_test.go`
- Test: `pkg/raftlog/pebble_test.go`

- [ ] **Step 1: Write the failing Pebble tests for manifest initialization and controller reopen**

Add tests like:

```go
func TestPebbleOpenInitializesManifest(t *testing.T) {
    db := mustOpenPebbleDB(t, t.TempDir())
    defer db.Close()

    if _, err := db.db.Get(encodeManifestKey()); err != nil {
        t.Fatalf("manifest key missing: %v", err)
    }
}

func TestPebbleControllerStateRoundTripAcrossReopen(t *testing.T) {
    db, path := openBenchDB(t)
    store := db.ForController()
    mustSave(t, store, benchPersistentState(1, 3, 2, 16))
    closeBenchDB(t, db, path)

    reopened := mustOpenPebbleDB(t, path)
    defer closeBenchDB(t, reopened, path)

    state := mustInitialState(t, reopened.ForController())
    require.Equal(t, uint64(3), state.HardState.Commit)
}
```

- [ ] **Step 2: Run the focused Pebble tests to verify RED**

Run: `go test ./pkg/raftlog -run 'TestPebble(OpenInitializesManifest|ControllerStateRoundTripAcrossReopen)'`
Expected: FAIL because the manifest is not persisted and `ForController()` is not yet fully wired through Pebble open/reopen paths.

- [ ] **Step 3: Create `pebble_db.go` and move DB lifecycle there**

Move these responsibilities out of `pebble.go`:

```go
type DB struct {
    db *pebble.DB

    mu      sync.Mutex
    closing bool

    writeCh  chan *writeRequest
    workerWG sync.WaitGroup

    stateCache map[Scope]scopeWriteState
}

func Open(path string) (*DB, error)
func (db *DB) Close() error
func (db *DB) ensureManifest() error
func (db *DB) For(scope Scope) multiraft.Storage
```

On open:
- create/load the Pebble DB
- write the manifest if missing
- reject unexpected manifest versions
- start the write worker

- [ ] **Step 4: Create `pebble_store.go` and `pebble_reader.go` for scope-local reads**

Target store shape:

```go
type pebbleStore struct {
    db    *DB
    scope Scope
}
```

Move these read APIs onto the store:

```go
func (s *pebbleStore) InitialState(ctx context.Context) (multiraft.BootstrapState, error)
func (s *pebbleStore) Entries(ctx context.Context, lo, hi, maxSize uint64) ([]raftpb.Entry, error)
func (s *pebbleStore) Term(ctx context.Context, index uint64) (uint64, error)
func (s *pebbleStore) FirstIndex(ctx context.Context) (uint64, error)
func (s *pebbleStore) LastIndex(ctx context.Context) (uint64, error)
func (s *pebbleStore) Snapshot(ctx context.Context) (raftpb.Snapshot, error)
```

And move the low-level helpers to the reader file:

```go
func (s *pebbleStore) loadHardState() (raftpb.HardState, error)
func (s *pebbleStore) loadSnapshot() (raftpb.Snapshot, error)
func (s *pebbleStore) loadAppliedIndex() (uint64, error)
func (s *pebbleStore) loadMeta() (logMeta, bool, error)
func (s *pebbleStore) currentMeta() (logMeta, bool, error)
func (s *pebbleStore) ensureMeta() (logMeta, bool, error)
func (s *pebbleStore) loadEntries(lo, hi uint64) ([]raftpb.Entry, error)
```

- [ ] **Step 5: Update the tests and helpers to query manifest and scope metadata**

Rename or rewrite assertions that currently depend on `encodeGroupStateKey(group)` so they use the new helpers:

```go
if _, err := store.(*pebbleStore).getValue(encodeMetaKey(SlotScope(21), recordTypeMeta)); err != nil {
    t.Fatalf("scope metadata missing: %v", err)
}
```

Keep the legacy-backfill test behavior, but make the naming accurate: it is backfilling metadata for a scope, not a group.

- [ ] **Step 6: Run the focused Pebble test subset again**

Run: `go test ./pkg/raftlog -run 'TestPebble(OpenInitializesManifest|ControllerStateRoundTripAcrossReopen|InitialStateReopenUsesPersistedMetadata|InitialStateReopenBackfillsLegacyStoreMetadata)'`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add pkg/raftlog/pebble_db.go pkg/raftlog/pebble_store.go pkg/raftlog/pebble_reader.go pkg/raftlog/pebble_test.go pkg/raftlog/pebble_test_helpers_test.go
git commit -m "refactor: split raftlog pebble db and read path"
```

## Task 4: Rebuild the Pebble write path around scope-aware write operations

**Files:**
- Create: `pkg/raftlog/pebble_writer.go`
- Modify: `pkg/raftlog/pebble_test.go`
- Modify: `pkg/raftlog/pebble_benchmark_test.go`
- Modify: `pkg/raftlog/pebble_stress_test.go`
- Delete: `pkg/raftlog/pebble.go`
- Delete: `pkg/raftlog/pebble_codec.go`
- Test: `pkg/raftlog/pebble_test.go`, `pkg/raftlog/pebble_stress_test.go`

- [ ] **Step 1: Write the failing tests for slot/controller isolation and concurrent scoped writes**

Add explicit coverage:

```go
func TestPebbleSlotAndControllerAreIndependent(t *testing.T) {
    db := mustOpenPebbleDB(t, t.TempDir())
    defer db.Close()

    mustSave(t, db.ForSlot(9), benchPersistentState(1, 2, 1, 8))
    mustSave(t, db.ForController(), benchPersistentState(1, 3, 2, 8))

    slotState := mustInitialState(t, db.ForSlot(9))
    controllerState := mustInitialState(t, db.ForController())

    require.Equal(t, uint64(2), slotState.HardState.Commit)
    require.Equal(t, uint64(3), controllerState.HardState.Commit)
}
```

Keep and retarget the existing concurrent write tests so at least one writer targets `ControllerScope()` and the others target slot scopes.

- [ ] **Step 2: Run the focused write-path tests to verify RED**

Run: `go test ./pkg/raftlog -run 'TestPebble(SlotAndControllerAreIndependent|CloseDrainsConcurrentWritesBeforeClosingDB|ConcurrentSaveAndMarkAppliedAreDurableAcrossReopen|SameGroupSaveThenMarkAppliedPreservesOrder)'`
Expected: FAIL because the write cache and request model still key state by raw `uint64` and are not fully scope-aware.

- [ ] **Step 3: Replace the optional-field request struct with explicit write operations**

Target shapes:

```go
type writeRequest struct {
    scope Scope
    op    writeOp
    done  chan error
}

type writeOp interface {
    apply(batch *pebble.Batch, state *scopeWriteState, store *pebbleStore) error
}

type saveOp struct {
    state multiraft.PersistentState
}

type markAppliedOp struct {
    index uint64
}
```

And:

```go
type scopeWriteState struct {
    hardState raftpb.HardState
    snapshot  raftpb.Snapshot
    entries   []raftpb.Entry
    meta      logMeta
}
```

- [ ] **Step 4: Move the worker and apply logic into `pebble_writer.go`**

Port and rename:

```go
func (db *DB) submitWrite(req *writeRequest) error
func (db *DB) runWriteWorker()
func (db *DB) flushWriteRequests(reqs []*writeRequest) error
func (db *DB) loadScopeWriteState(cache map[Scope]*scopeWriteState, scope Scope) (*scopeWriteState, error)
func (db *DB) cloneScopeWriteState(state scopeWriteState) scopeWriteState
```

Implement `saveOp.apply` and `markAppliedOp.apply` so they:
- mutate one `scopeWriteState`
- write new-format keys
- update `logMeta`
- persist metadata alongside the state change

- [ ] **Step 5: Update `Save` and `MarkApplied` to submit scope operations**

Target shape:

```go
func (s *pebbleStore) Save(ctx context.Context, st multiraft.PersistentState) error {
    return s.db.submitWrite(&writeRequest{
        scope: s.scope,
        op:    saveOp{state: st},
        done:  make(chan error, 1),
    })
}
```

Do the same for `MarkApplied`.

- [ ] **Step 6: Remove the legacy monolith files**

Once all exported and internal symbols are moved:

```bash
git rm pkg/raftlog/pebble.go pkg/raftlog/pebble_codec.go
```

- [ ] **Step 7: Run `gofmt`, the Pebble package tests, and the stress subset**

Run: `gofmt -w pkg/raftlog`
Run: `go test ./pkg/raftlog -run 'TestPebble|TestMemory'`
Run: `go test ./pkg/raftlog -run 'TestPebbleStress(ConcurrentWriters|MixedReadWriteReopen|SnapshotAndRecovery|AckedWritesSurvivePeriodicReopen)'`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add pkg/raftlog
git commit -m "refactor: rebuild scoped raftlog pebble write path"
```

## Task 5: Migrate controller callers and finish the repo-level verification

**Files:**
- Modify: `pkg/controller/raft/service.go`
- Modify: `pkg/controller/raft/service_test.go`
- Modify: `pkg/raftlog/pebble_benchmark_test.go`
- Modify: `pkg/raftlog/pebble_stress_test.go`
- Modify: `pkg/raftlog/pebble_test.go`
- Test: `pkg/controller/raft/service_test.go`, `pkg/raftlog/*.go`

- [ ] **Step 1: Write the failing controller tests for the new API**

Update the controller tests so they stop referencing the fake slot constant and instead assert the controller-specific API:

```go
func TestServiceUsesControllerStorageScope(t *testing.T) {
    state, err := node.logDB.ForController().InitialState(context.Background())
    require.NoError(t, err)
    require.Equal(t, expectedCommit, state.HardState.Commit)
}
```

If a dedicated test name is clearer, add one in `pkg/controller/raft/service_test.go` and remove direct assertions on `controllerGroupStorageID`.

- [ ] **Step 2: Run the controller and raftlog tests to verify RED**

Run: `go test ./pkg/controller/raft ./pkg/raftlog -run 'TestService|TestPebble|TestMemory'`
Expected: FAIL because controller callers still use `ForSlot(controllerGroupStorageID)` or because the tests still reference removed symbols.

- [ ] **Step 3: Switch controller raft to `ForController()` and delete the fake slot constant**

In `pkg/controller/raft/service.go`, replace:

```go
const controllerGroupStorageID uint64 = 1
store := s.cfg.LogDB.ForSlot(controllerGroupStorageID)
```

with:

```go
store := s.cfg.LogDB.ForController()
```

Remove the now-unused constant and update any helper tests that still expect it.

- [ ] **Step 4: Sweep remaining `group`-identity names in raftlog tests and helpers**

Use:

```bash
rg -n '\bgroup\b|GroupMeta|groupWriteState|controllerGroupStorageID' pkg/raftlog pkg/controller/raft
```

Rename only the storage-identity terms that still refer to the old fake model. Do not rename unrelated Raft or cluster-domain uses outside this scope.

- [ ] **Step 5: Run formatting and the final verification suite**

Run: `gofmt -w pkg/raftlog pkg/controller/raft`
Run: `go test ./pkg/raftlog`
Run: `go test ./pkg/controller/raft`
Run: `go test ./pkg/cluster -run 'TestCluster|TestObservationPeersForSlot|TestAssignedNodesForSlot'`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/raftlog pkg/controller/raft
git commit -m "refactor: use dedicated controller raftlog scope"
```

## Execution Notes

- Follow @superpowers:test-driven-development for each task: test first, confirm RED, implement minimally, confirm GREEN, then refactor.
- Before claiming success, follow @superpowers:verification-before-completion and include the exact test commands that passed.
- If the existing dirty worktree has unrelated changes in touched files, read and preserve them; do not revert user work.
