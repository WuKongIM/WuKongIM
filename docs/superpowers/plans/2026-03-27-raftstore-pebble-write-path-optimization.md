# Raftstore Pebble Write Path Optimization Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Improve `raftstore` Pebble write-path throughput and reopen latency by adding a durable internal write aggregator and persisted per-group raft metadata, without weakening acknowledged-write durability.

**Architecture:** Keep the public `multiraft.Storage` API unchanged. Extend `raftstore.DB` with a single background write worker that batches concurrent `Save` and `MarkApplied` requests into one `pebble.Sync` commit while preserving synchronous completion semantics. Persist group metadata in the same write batch so `InitialState`, `FirstIndex`, and `LastIndex` can avoid full entry scans after reopen, with a backward-compatible fallback for older stores.

**Tech Stack:** Go 1.23, Pebble, `go.etcd.io/raft/v3/raftpb`, Go testing/benchmark framework, existing `raftstore` benchmark/stress suite

---

## File Structure

### Production files

- Modify: `raftstore/pebble.go`
  Responsibility: add DB write-worker lifecycle, queueing, request/result types, metadata-backed state loading, backward-compatible fallback, and shutdown behavior.
- Modify: `raftstore/pebble_codec.go`
  Responsibility: add key encoding for the new per-group metadata record.

### Test files

- Modify: `raftstore/pebble_test.go`
  Responsibility: correctness tests for metadata persistence, legacy fallback, close/drain semantics, and durable visibility across reopen.
- Modify: `raftstore/pebble_test_helpers_test.go`
  Responsibility: add helpers for constructing legacy-format stores, metadata assertions, and synchronized concurrent write test scaffolding.
- Modify: `raftstore/pebble_benchmark_test.go`
  Responsibility: keep existing benchmark names and capture pre/post optimization effects on write and reopen paths.
- Modify: `raftstore/pebble_stress_test.go`
  Responsibility: extend stress coverage with acknowledged-write visibility across periodic close/reopen.

### Reference files

- Reference: `docs/superpowers/specs/2026-03-27-raftstore-pebble-write-path-optimization-design.md`
  Responsibility: approved scope, invariants, and validation requirements.
- Reference: `raftstore/pebble_test.go`
  Responsibility: current storage semantics and existing correctness patterns.
- Reference: `docs/raftstore-stress.md`
  Responsibility: benchmark/stress commands already used for validation.

## Implementation Notes

- Follow `@superpowers:test-driven-development`: write the failing test first, run it red, then write the minimum production code to get it green.
- Do not change `multiraft.Storage` or any other package interface.
- Preserve slot-prefixed raft keys and keep raft metadata/log data isolated from business keyspace.
- The write worker must not return success to any caller before the corresponding `pebble.Sync` commit completes.
- Same-group request ordering is mandatory; do not reorder `Save` and `MarkApplied` calls for one group when coalescing.
- Reopen must remain backward compatible for stores that do not yet have metadata keys.

### Task 1: Add persisted group metadata and codec coverage

**Files:**
- Modify: `raftstore/pebble_codec.go`
- Modify: `raftstore/pebble.go`
- Modify: `raftstore/pebble_test.go`
- Modify: `raftstore/pebble_test_helpers_test.go`
- Test: `raftstore/pebble_test.go`

- [ ] **Step 1: Write the failing metadata tests**

```go
func TestPebbleInitialStateReopenUsesPersistedMetadata(t *testing.T) {
	ctx := context.Background()
	store := openTestPebbleStore(t, 21)

	hs := raftpb.HardState{Term: 2, Commit: 3}
	entries := []raftpb.Entry{
		{
			Index: 1,
			Term:  1,
			Type:  raftpb.EntryConfChange,
			Data:  mustMarshalConfChange(t, raftpb.ConfChange{Type: raftpb.ConfChangeAddNode, NodeID: 1}),
		},
		{
			Index: 2,
			Term:  1,
			Type:  raftpb.EntryConfChange,
			Data:  mustMarshalConfChange(t, raftpb.ConfChange{Type: raftpb.ConfChangeAddNode, NodeID: 2}),
		},
		{
			Index: 3,
			Term:  2,
			Type:  raftpb.EntryConfChange,
			Data:  mustMarshalConfChange(t, raftpb.ConfChange{Type: raftpb.ConfChangeAddNode, NodeID: 3}),
		},
	}

	if err := store.Save(ctx, multiraft.PersistentState{HardState: &hs, Entries: entries}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := store.MarkApplied(ctx, 3); err != nil {
		t.Fatalf("MarkApplied() error = %v", err)
	}

	reopened := reopenTestPebbleDB(t, store.(*pebbleStore).db)
	state, err := reopened.ForGroup(21).InitialState(ctx)
	if err != nil {
		t.Fatalf("InitialState() error = %v", err)
	}

	want := raftpb.ConfState{Voters: []uint64{1, 2, 3}}
	if !reflect.DeepEqual(state.ConfState, want) {
		t.Fatalf("ConfState = %#v, want %#v", state.ConfState, want)
	}
}

func TestPebbleFirstAndLastIndexUsePersistedMetadataAfterSnapshotTrim(t *testing.T) {
	ctx := context.Background()
	store := openTestPebbleStore(t, 22)

	if err := store.Save(ctx, multiraft.PersistentState{
		Entries: benchEntries(5, 4, 1, 8),
	}); err != nil {
		t.Fatalf("Save(entries) error = %v", err)
	}
	snap := raftpb.Snapshot{
		Metadata: raftpb.SnapshotMetadata{
			Index: 6,
			Term:  1,
			ConfState: raftpb.ConfState{
				Voters: []uint64{1, 2},
			},
		},
	}
	if err := store.Save(ctx, multiraft.PersistentState{Snapshot: &snap}); err != nil {
		t.Fatalf("Save(snapshot) error = %v", err)
	}

	first, _ := store.FirstIndex(ctx)
	last, _ := store.LastIndex(ctx)
	if first != 7 || last != 8 {
		t.Fatalf("first/last = %d/%d, want 7/8", first, last)
	}
}
```

- [ ] **Step 2: Run the new metadata tests and confirm they fail**

Run: `go test ./raftstore -run 'TestPebble(InitialStateReopenUsesPersistedMetadata|FirstAndLastIndexUsePersistedMetadataAfterSnapshotTrim)$' -count=1`

Expected: FAIL because there is no persisted metadata record and read paths still reconstruct state by scanning entries.

- [ ] **Step 3: Implement metadata encoding and persistence**

```go
type groupMeta struct {
	FirstIndex   uint64
	LastIndex    uint64
	AppliedIndex uint64
	SnapshotIndex uint64
	SnapshotTerm  uint64
	ConfState     raftpb.ConfState
}

func encodeGroupStateKey(group uint64) []byte {
	return encodeGroupMetaKey(group, keyTypeGroupState)
}
```

Implementation details:
- add a new metadata key type in `raftstore/pebble_codec.go`
- add marshal/unmarshal helpers for the per-group metadata record in `raftstore/pebble.go`
- update direct-write code paths temporarily to persist metadata in the same batch as `Save` and `MarkApplied`
- teach `InitialState`, `FirstIndex`, and `LastIndex` to prefer metadata when present
- keep the old reconstruction path available for missing metadata

- [ ] **Step 4: Re-run the metadata tests and confirm they pass**

Run: `go test ./raftstore -run 'TestPebble(InitialStateReopenUsesPersistedMetadata|FirstAndLastIndexUsePersistedMetadataAfterSnapshotTrim)$' -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add raftstore/pebble.go raftstore/pebble_codec.go raftstore/pebble_test.go raftstore/pebble_test_helpers_test.go
git commit -m "feat: persist raftstore pebble group metadata"
```

### Task 2: Add backward-compatible metadata backfill on reopen

**Files:**
- Modify: `raftstore/pebble.go`
- Modify: `raftstore/pebble_test.go`
- Modify: `raftstore/pebble_test_helpers_test.go`
- Test: `raftstore/pebble_test.go`

- [ ] **Step 1: Write the failing legacy-reopen tests**

```go
func TestPebbleInitialStateReopenBackfillsLegacyStoreMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "raft")
	mustWriteLegacyPebbleState(t, path, 31, legacyPebbleState{
		hardState: raftpb.HardState{Term: 2, Commit: 3},
		applied:   3,
		entries: []raftpb.Entry{
			{
				Index: 1,
				Term:  1,
				Type:  raftpb.EntryConfChange,
				Data:  mustMarshalConfChange(t, raftpb.ConfChange{Type: raftpb.ConfChangeAddNode, NodeID: 1}),
			},
			{
				Index: 2,
				Term:  1,
				Type:  raftpb.EntryConfChange,
				Data:  mustMarshalConfChange(t, raftpb.ConfChange{Type: raftpb.ConfChangeAddNode, NodeID: 2}),
			},
			{
				Index: 3,
				Term:  2,
				Type:  raftpb.EntryConfChange,
				Data:  mustMarshalConfChange(t, raftpb.ConfChange{Type: raftpb.ConfChangeAddNode, NodeID: 3}),
			},
		},
	})

	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	state, err := db.ForGroup(31).InitialState(context.Background())
	if err != nil {
		t.Fatalf("InitialState() error = %v", err)
	}
	want := raftpb.ConfState{Voters: []uint64{1, 2, 3}}
	if !reflect.DeepEqual(state.ConfState, want) {
		t.Fatalf("ConfState = %#v, want %#v", state.ConfState, want)
	}

	if !hasGroupMetadata(t, db, 31) {
		t.Fatal("metadata was not backfilled after legacy reopen")
	}
}
```

- [ ] **Step 2: Run the legacy-reopen test and confirm it fails**

Run: `go test ./raftstore -run '^TestPebbleInitialStateReopenBackfillsLegacyStoreMetadata$' -count=1`

Expected: FAIL because no backfill occurs after falling back to the old reconstruction path.

- [ ] **Step 3: Implement legacy fallback and metadata backfill**

Implementation details:
- add a helper like `loadOrRebuildGroupMeta()` in `raftstore/pebble.go`
- when metadata is missing, reconstruct from snapshot, hard state, applied index, and entries exactly as the current code does
- persist the reconstructed metadata before returning so the second reopen takes the fast path
- add test helpers in `raftstore/pebble_test_helpers_test.go` that write legacy stores directly through raw Pebble keys without the new metadata key

- [ ] **Step 4: Re-run the legacy-reopen test and confirm it passes**

Run: `go test ./raftstore -run '^TestPebbleInitialStateReopenBackfillsLegacyStoreMetadata$' -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add raftstore/pebble.go raftstore/pebble_test.go raftstore/pebble_test_helpers_test.go
git commit -m "feat: backfill raftstore pebble metadata on reopen"
```

### Task 3: Add the durable DB write worker and same-group coalescing

**Files:**
- Modify: `raftstore/pebble.go`
- Modify: `raftstore/pebble_test.go`
- Modify: `raftstore/pebble_test_helpers_test.go`
- Test: `raftstore/pebble_test.go`

- [ ] **Step 1: Write the failing write-worker tests**

```go
func TestPebbleConcurrentSaveAndMarkAppliedAreDurableAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "raft")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	const writers = 8
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(group uint64) {
			defer wg.Done()
			store := db.ForGroup(group)
			state := multiraft.PersistentState{
				HardState: ptr(raftpb.HardState{Term: 1, Commit: 4}),
				Entries:   benchEntries(1, 4, 1, 32),
			}
			if err := store.Save(context.Background(), state); err != nil {
				t.Errorf("Save(group=%d) error = %v", group, err)
				return
			}
			if err := store.MarkApplied(context.Background(), 4); err != nil {
				t.Errorf("MarkApplied(group=%d) error = %v", group, err)
			}
		}(uint64(i + 1))
	}
	wg.Wait()

	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen Open() error = %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })

	for group := uint64(1); group <= writers; group++ {
		state, err := reopened.ForGroup(group).InitialState(context.Background())
		if err != nil {
			t.Fatalf("group %d InitialState() error = %v", group, err)
		}
		if state.AppliedIndex != 4 {
			t.Fatalf("group %d AppliedIndex = %d, want 4", group, state.AppliedIndex)
		}
	}
}

func TestPebbleCloseDrainsQueuedWritesBeforeClosingDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "raft")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	done := make(chan error, 1)
	go func() {
		store := db.ForGroup(41)
		done <- store.Save(context.Background(), multiraft.PersistentState{
			HardState: ptr(raftpb.HardState{Term: 1, Commit: 2}),
			Entries:   benchEntries(1, 2, 1, 16),
		})
	}()

	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("Save() error = %v", err)
	}
}
```

- [ ] **Step 2: Run the write-worker tests and confirm they fail**

Run: `go test ./raftstore -run 'TestPebble(ConcurrentSaveAndMarkAppliedAreDurableAcrossReopen|CloseDrainsQueuedWritesBeforeClosingDB)$' -count=1`

Expected: FAIL because writes still commit independently and `Close()` does not coordinate with an internal worker.

- [ ] **Step 3: Implement the write worker**

```go
type writeRequest struct {
	group uint64
	save  *multiraft.PersistentState
	apply *uint64
	done  chan error
}

type DB struct {
	db *pebble.DB

	writeCh chan writeRequest
	closeCh chan struct{}
	workerWG sync.WaitGroup
}
```

Implementation details:
- start one background worker from `Open()`
- route `Save()` and `MarkApplied()` through a shared `submitWrite()` helper
- flush multiple queued requests into a single Pebble batch and commit with `pebble.Sync`
- fold same-group `Save` + `MarkApplied` requests into one in-memory mutation before writing keys
- reject new submissions after shutdown starts
- make `Close()` stop acceptance, flush/drain pending work, wait for the worker, then close Pebble

- [ ] **Step 4: Re-run the write-worker tests and confirm they pass**

Run: `go test ./raftstore -run 'TestPebble(ConcurrentSaveAndMarkAppliedAreDurableAcrossReopen|CloseDrainsQueuedWritesBeforeClosingDB)$' -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add raftstore/pebble.go raftstore/pebble_test.go raftstore/pebble_test_helpers_test.go
git commit -m "feat: batch raftstore pebble writes durably"
```

### Task 4: Preserve same-group ordering and metadata correctness across mixed mutations

**Files:**
- Modify: `raftstore/pebble.go`
- Modify: `raftstore/pebble_test.go`
- Modify: `raftstore/pebble_test_helpers_test.go`
- Test: `raftstore/pebble_test.go`

- [ ] **Step 1: Write the failing ordering and mixed-mutation tests**

```go
func TestPebbleSameGroupSaveThenMarkAppliedPreservesOrder(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "raft")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	store := db.ForGroup(51)

	if err := store.Save(ctx, multiraft.PersistentState{
		HardState: ptr(raftpb.HardState{Term: 2, Commit: 3}),
		Entries:   benchEntries(1, 3, 2, 16),
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := store.MarkApplied(ctx, 3); err != nil {
		t.Fatalf("MarkApplied() error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen Open() error = %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })

	state, err := reopened.ForGroup(51).InitialState(ctx)
	if err != nil {
		t.Fatalf("InitialState() error = %v", err)
	}
	if state.HardState.Commit != 3 || state.AppliedIndex != 3 {
		t.Fatalf("commit/applied = %d/%d, want 3/3", state.HardState.Commit, state.AppliedIndex)
	}
}

func TestPebbleMetadataTracksTailReplaceAndSnapshotTrim(t *testing.T) {
	ctx := context.Background()
	store := openTestPebbleStore(t, 52)

	if err := store.Save(ctx, multiraft.PersistentState{Entries: benchEntries(5, 4, 1, 16)}); err != nil {
		t.Fatalf("Save(initial) error = %v", err)
	}
	if err := store.Save(ctx, multiraft.PersistentState{Entries: benchEntries(7, 3, 2, 16)}); err != nil {
		t.Fatalf("Save(replace) error = %v", err)
	}
	snap := raftpb.Snapshot{
		Metadata: raftpb.SnapshotMetadata{
			Index: 8,
			Term:  2,
			ConfState: raftpb.ConfState{
				Voters: []uint64{1, 2},
			},
		},
	}
	if err := store.Save(ctx, multiraft.PersistentState{Snapshot: &snap}); err != nil {
		t.Fatalf("Save(snapshot) error = %v", err)
	}

	first, _ := store.FirstIndex(ctx)
	last, _ := store.LastIndex(ctx)
	if first != 9 || last != 9 {
		t.Fatalf("first/last = %d/%d, want 9/9", first, last)
	}
}
```

- [ ] **Step 2: Run the ordering tests and confirm they fail**

Run: `go test ./raftstore -run 'TestPebble(SameGroupSaveThenMarkAppliedPreservesOrder|MetadataTracksTailReplaceAndSnapshotTrim)$' -count=1`

Expected: FAIL until mixed same-group mutations and metadata updates are folded correctly.

- [ ] **Step 3: Implement same-group fold and metadata update rules**

Implementation details:
- make the worker build a per-group mutation object within one flush
- preserve queue order while combining adjacent or co-batched same-group writes
- apply metadata updates for append, tail replace, snapshot save, and `MarkApplied` in the same batch as the underlying mutation
- keep `Term()` and `Entries()` behavior unchanged

- [ ] **Step 4: Re-run the ordering tests and confirm they pass**

Run: `go test ./raftstore -run 'TestPebble(SameGroupSaveThenMarkAppliedPreservesOrder|MetadataTracksTailReplaceAndSnapshotTrim)$' -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add raftstore/pebble.go raftstore/pebble_test.go raftstore/pebble_test_helpers_test.go
git commit -m "fix: preserve raftstore pebble ordering and metadata"
```

### Task 5: Refresh benchmarks and add acknowledged-write stress coverage

**Files:**
- Modify: `raftstore/pebble_benchmark_test.go`
- Modify: `raftstore/pebble_stress_test.go`
- Modify: `raftstore/pebble_test_helpers_test.go`
- Test: `raftstore/pebble_benchmark_test.go`
- Test: `raftstore/pebble_stress_test.go`

- [ ] **Step 1: Write the failing benchmark/stress additions**

```go
func TestPebbleStressAckedWritesSurvivePeriodicReopen(t *testing.T) {
	cfg, err := loadPebbleStressConfig()
	if err != nil {
		t.Fatalf("loadPebbleStressConfig() error = %v", err)
	}
	if !cfg.enabled {
		t.Skip("set WRAFT_RAFTSTORE_STRESS=1 to enable")
	}
	t.Fatal("not implemented")
}
```

Benchmark update expectations:
- keep existing benchmark names unchanged
- add sub-benchmark comments or post-checks if needed for the new metadata/write-worker paths

- [ ] **Step 2: Run the targeted stress test and benchmark suite and confirm the new test fails**

Run: `WRAFT_RAFTSTORE_STRESS=1 WRAFT_RAFTSTORE_STRESS_DURATION=3s go test ./raftstore -run '^TestPebbleStressAckedWritesSurvivePeriodicReopen$' -count=1 -v`

Expected: FAIL because the new stress shell intentionally calls `t.Fatal`.

- [ ] **Step 3: Implement the validation updates**

Implementation details:
- add a stress loop that records only successfully acknowledged writes, periodically closes/reopens the DB, and verifies those writes remain visible
- reuse existing helpers and model-state verification from `raftstore/pebble_test_helpers_test.go`
- keep existing benchmark names and run them against the optimized production path without changing their command surface

- [ ] **Step 4: Re-run the focused benchmark and stress commands**

Run: `go test ./raftstore -run '^$' -bench 'BenchmarkPebble(SaveEntries|Entries|MarkApplied|InitialStateAndReopen)$' -count=1`

Expected: PASS with benchmark output and no post-check failures.

Run: `WRAFT_RAFTSTORE_BENCH_SCALE=heavy go test ./raftstore -run '^$' -bench 'BenchmarkPebble(SaveEntries|Entries|MarkApplied|InitialStateAndReopen)$' -count=1`

Expected: PASS with heavy-scale benchmark output.

Run: `WRAFT_RAFTSTORE_STRESS=1 WRAFT_RAFTSTORE_STRESS_DURATION=3s go test ./raftstore -run 'TestPebbleStress(ConcurrentWriters|MixedReadWriteReopen|SnapshotAndRecovery|AckedWritesSurvivePeriodicReopen)$' -count=1 -v`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add raftstore/pebble_benchmark_test.go raftstore/pebble_stress_test.go raftstore/pebble_test_helpers_test.go
git commit -m "test: verify raftstore pebble write path optimization"
```

### Task 6: Run final verification before handoff

**Files:**
- Verify only: `raftstore/pebble.go`
- Verify only: `raftstore/pebble_codec.go`
- Verify only: `raftstore/pebble_test.go`
- Verify only: `raftstore/pebble_test_helpers_test.go`
- Verify only: `raftstore/pebble_benchmark_test.go`
- Verify only: `raftstore/pebble_stress_test.go`

- [ ] **Step 1: Run the targeted correctness suite**

Run: `go test ./raftstore -count=1`

Expected: PASS

- [ ] **Step 2: Run the repository suite**

Run: `go test ./...`

Expected: PASS

- [ ] **Step 3: Run the race detector on raftstore at minimum**

Run: `go test -race ./raftstore -count=1`

Expected: PASS

- [ ] **Step 4: Record benchmark/stress evidence for the handoff**

Required commands:

```bash
go test ./raftstore -run '^$' -bench 'BenchmarkPebble(SaveEntries|Entries|MarkApplied|InitialStateAndReopen)$' -count=1
WRAFT_RAFTSTORE_BENCH_SCALE=heavy go test ./raftstore -run '^$' -bench 'BenchmarkPebble(SaveEntries|Entries|MarkApplied|InitialStateAndReopen)$' -count=1
WRAFT_RAFTSTORE_STRESS=1 WRAFT_RAFTSTORE_STRESS_DURATION=5m go test ./raftstore -run '^TestPebbleStress' -count=1 -v
```

Expected: all PASS; benchmark output shows improved `SaveEntries`, `MarkApplied`, and `InitialStateAndReopen` trends relative to the documented baseline.

- [ ] **Step 5: Commit**

```bash
git add raftstore/pebble.go raftstore/pebble_codec.go raftstore/pebble_test.go raftstore/pebble_test_helpers_test.go raftstore/pebble_benchmark_test.go raftstore/pebble_stress_test.go
git commit -m "feat: optimize raftstore pebble write path"
```
