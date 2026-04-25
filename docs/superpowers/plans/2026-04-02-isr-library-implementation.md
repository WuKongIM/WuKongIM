# ISR Library Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the first working `pkg/isr` library that provides single-group ISR replication with durable recovery, exclusive `HW`, lease fencing, divergence handling, and snapshot installation.

**Architecture:** The implementation lives in `pkg/isr` and follows the same focused-file style as `pkg/multiraft`: public contracts in `types.go`, sentinel errors in `errors.go`, and behavior split across `meta`, `recovery`, `fetch`, `replication`, and `append` files. Tests use in-memory fake stores plus a manual clock so recovery order, checkpoint durability, epoch history, divergence, and append blocking can be verified without any transport or external services.

**Tech Stack:** Go 1.23, standard library `context/errors/slices/sync/testing/time`

---

## File Structure

### Production files

- Create: `pkg/isr/doc.go`
  Responsibility: package overview and contract notes for control-plane-driven ISR replication.
- Create: `pkg/isr/types.go`
  Responsibility: public IDs, enums, config, metadata, records, checkpoints, fetch/apply requests, results, store interfaces, and the public `Replica` interface.
- Create: `pkg/isr/errors.go`
  Responsibility: exported sentinel errors and small internal helpers.
- Create: `pkg/isr/replica.go`
  Responsibility: concrete replica struct, constructor, shared locking/state fields, and top-level method wiring.
- Create: `pkg/isr/meta.go`
  Responsibility: `GroupMeta` normalization, stale-meta checks, role transitions, and status snapshots.
- Create: `pkg/isr/recovery.go`
  Responsibility: startup recovery, `BecomeLeader`, `BecomeFollower`, `InstallSnapshot`, checkpoint/history validation, and durable ordering.
- Create: `pkg/isr/progress.go`
  Responsibility: follower progress tracking, ISR commit calculation, waiter notification, and lease-fence helpers.
- Create: `pkg/isr/fetch.go`
  Responsibility: leader-side fetch serving, divergence detection, `ErrSnapshotRequired`, and read budgeting.
- Create: `pkg/isr/replication.go`
  Responsibility: follower-side application of fetch results, local truncate/append/checkpoint updates, and local `HW` advancement.
- Create: `pkg/isr/append.go`
  Responsibility: leader append critical section, MinISR gate, commit wait path, and `CommitResult`.

### Test files

- Create: `pkg/isr/testenv_test.go`
  Responsibility: fake log store, checkpoint store, epoch history store, snapshot applier, manual clock, and reusable assertions.
- Create: `pkg/isr/api_test.go`
  Responsibility: constructor validation and public-surface compile tests.
- Create: `pkg/isr/meta_test.go`
  Responsibility: metadata normalization, stale-meta handling, role transitions, and tombstone fencing.
- Create: `pkg/isr/recovery_test.go`
  Responsibility: startup recovery, dirty-tail truncation, checkpoint/history corruption, and leader-promotion ordering.
- Create: `pkg/isr/fetch_test.go`
  Responsibility: fetch budgeting, divergence, snapshot-required, and leader progress updates from follower fetches.
- Create: `pkg/isr/replication_test.go`
  Responsibility: follower-side `ApplyFetch` behavior, truncate safety, local checkpoint advancement, and stale-epoch rejection.
- Create: `pkg/isr/append_test.go`
  Responsibility: append gating, MinISR blocking, exclusive `HW`, checkpoint-before-success, and lease fencing.
- Create: `pkg/isr/snapshot_test.go`
  Responsibility: snapshot installation ordering and recovery after installed snapshots.

## Implementation Notes

- Follow `@superpowers:test-driven-development` task-by-task.
- Mirror the existing `pkg/multiraft` layout: keep public contracts in `types.go`, keep runtime internals unexported, and avoid a catch-all `utils.go`.
- No new module dependency is expected for V1; stay inside the standard library.
- The approved spec leaves one transport-facing detail implicit: leader-side fetches need follower identity and follower-side fetch responses need a library-owned apply path. Task 1 therefore locks these two executable completions:
  - `FetchRequest.ReplicaID` and `FetchRequest.OffsetEpoch` are required so leader-side progress tracking and divergence decisions are implementable.
  - `ApplyFetchRequest` plus `Replica.ApplyFetch(...)` are required so follower persistence, truncate safety, and local `HW` advancement stay inside `pkg/isr` rather than leaking into `pkg/multiisr`.
- `CheckpointStore.Store(...)` is treated as durable on successful return. `LogStore.Sync()` is the explicit durability boundary for log mutations.
- `BecomeLeader` seeds peer progress conservatively:
  - local leader progress starts at `LEO`
  - every other replica in `ISR` starts at `HW`
  This matches the safety guarantee that committed data `< HW` is already durable on the elected ISR set.

## Task 1: Lock the public API and executable contract

**Files:**
- Create: `pkg/isr/doc.go`
- Create: `pkg/isr/types.go`
- Create: `pkg/isr/errors.go`
- Create: `pkg/isr/replica.go`
- Test: `pkg/isr/api_test.go`

- [ ] **Step 1: Write the failing public-surface tests**

```go
func TestNewReplicaValidatesRequiredDependencies(t *testing.T) {
	_, err := isr.NewReplica(isr.ReplicaConfig{})
	if !errors.Is(err, isr.ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig, got %v", err)
	}
}

func TestReplicaSurfaceIncludesFetchAndApplyHooks(t *testing.T) {
	var req isr.FetchRequest
	req.ReplicaID = 2
	req.OffsetEpoch = 5

	var apply isr.ApplyFetchRequest
	apply.Leader = 1

	var r isr.Replica
	_, _ = r.Fetch(context.Background(), req)
	_ = r.ApplyFetch(context.Background(), apply)
}
```

- [ ] **Step 2: Run the targeted tests and confirm they fail**

Run: `go test ./pkg/isr -run 'TestNewReplicaValidatesRequiredDependencies|TestReplicaSurfaceIncludesFetchAndApplyHooks' -v`

Expected: FAIL because `pkg/isr` and its exported symbols do not exist yet.

- [ ] **Step 3: Add the package shell and public type definitions**

```go
package isr

type NodeID uint64

type FetchRequest struct {
	GroupID     uint64
	Epoch       uint64
	ReplicaID   NodeID
	FetchOffset uint64
	OffsetEpoch uint64
	MaxBytes    int
}

type ApplyFetchRequest struct {
	GroupID    uint64
	Epoch      uint64
	Leader     NodeID
	TruncateTo *uint64
	Records    []Record
	LeaderHW   uint64
}
```

Implementation details:
- add package comment in `pkg/isr/doc.go`
- define `GroupMeta`, `ReplicaState`, `Record`, `Checkpoint`, `Snapshot`, `EpochPoint`, `CommitResult`, `FetchResult`, `Role`
- define `LogStore`, `CheckpointStore`, `EpochHistoryStore`, `SnapshotApplier`
- define `ReplicaConfig` with `LocalNode`, stores, `SnapshotApplier`, and optional `Now func() time.Time`
- define the public `Replica` interface and a concrete `replica` shell
- add constructor signature `func NewReplica(cfg ReplicaConfig) (Replica, error)`

- [ ] **Step 4: Add constructor validation and sensible defaults**

Validation rules for V1:
- `LocalNode != 0`
- `LogStore != nil`
- `CheckpointStore != nil`
- `EpochHistoryStore != nil`
- `SnapshotApplier != nil`
- `Now` defaults to `time.Now` when nil

- [ ] **Step 5: Re-run the targeted tests**

Run: `go test ./pkg/isr -run 'TestNewReplicaValidatesRequiredDependencies|TestReplicaSurfaceIncludesFetchAndApplyHooks' -v`

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add pkg/isr/doc.go pkg/isr/types.go pkg/isr/errors.go pkg/isr/replica.go pkg/isr/api_test.go
git commit -m "feat: add isr public api shell"
```

## Task 2: Implement metadata normalization and lifecycle fences

**Files:**
- Modify: `pkg/isr/replica.go`
- Create: `pkg/isr/meta.go`
- Create: `pkg/isr/testenv_test.go`
- Create: `pkg/isr/meta_test.go`

- [ ] **Step 1: Write failing metadata and role-transition tests**

```go
func TestApplyMetaRejectsInvalidISRSubset(t *testing.T) {
	r := newTestReplica(t)
	err := r.ApplyMeta(isr.GroupMeta{
		GroupID:  10,
		Epoch:    1,
		Leader:   1,
		Replicas: []isr.NodeID{1, 2},
		ISR:      []isr.NodeID{1, 3},
		MinISR:   2,
	})
	if !errors.Is(err, isr.ErrInvalidMeta) {
		t.Fatalf("expected ErrInvalidMeta, got %v", err)
	}
}

func TestTombstoneFencesFutureOperations(t *testing.T) {
	r := newTestReplica(t)
	if err := r.Tombstone(); err != nil {
		t.Fatalf("Tombstone() error = %v", err)
	}
	_, err := r.Append(context.Background(), []isr.Record{{Payload: []byte("x"), SizeBytes: 1}})
	if !errors.Is(err, isr.ErrTombstoned) {
		t.Fatalf("expected ErrTombstoned, got %v", err)
	}
}
```

- [ ] **Step 2: Run the metadata tests and confirm they fail**

Run: `go test ./pkg/isr -run 'TestApplyMetaRejectsInvalidISRSubset|TestTombstoneFencesFutureOperations' -v`

Expected: FAIL because metadata validation and tombstone fencing are not implemented.

- [ ] **Step 3: Build the test doubles**

```go
type fakeLogStore struct {
	records        []isr.Record
	leo            uint64
	syncCount      int
	truncateCalls  []uint64
}

type fakeCheckpointStore struct {
	checkpoint isr.Checkpoint
	loadErr    error
	stored     []isr.Checkpoint
}

type fakeEpochHistoryStore struct {
	points   []isr.EpochPoint
	loadErr  error
	appended []isr.EpochPoint
}
```

Requirements:
- fake stores must be concurrency-safe because later append tests will block in goroutines
- fake clock must let tests advance `Now()` without sleeping
- `newTestReplica(t)` must create a replica with empty-state stores and a stable clock

- [ ] **Step 4: Implement `GroupMeta` normalization and role state changes**

Implementation details:
- add ordered de-dup normalization for `Replicas` and `ISR`
- reject invalid `Leader`, empty `Replicas`, or out-of-range `MinISR`
- enforce same-epoch update rules from the spec
- keep `ApplyMeta` non-destructive
- implement `BecomeFollower`, `Tombstone`, and `Status`
- make top-level mutating methods fail fast with `ErrTombstoned` once tombstoned

- [ ] **Step 5: Re-run the metadata tests plus API tests**

Run: `go test ./pkg/isr -run 'TestApplyMetaRejectsInvalidISRSubset|TestTombstoneFencesFutureOperations|TestNewReplicaValidatesRequiredDependencies' -v`

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add pkg/isr/replica.go pkg/isr/meta.go pkg/isr/testenv_test.go pkg/isr/meta_test.go
git commit -m "feat: add isr metadata lifecycle guards"
```

## Task 3: Implement startup recovery and checkpoint/history validation

**Files:**
- Modify: `pkg/isr/replica.go`
- Create: `pkg/isr/recovery.go`
- Modify: `pkg/isr/testenv_test.go`
- Create: `pkg/isr/recovery_test.go`

- [ ] **Step 1: Write failing recovery tests**

```go
func TestNewReplicaTruncatesDirtyTailToCheckpointHW(t *testing.T) {
	env := newTestEnv(t)
	env.log.leo = 5
	env.checkpoints.checkpoint = isr.Checkpoint{
		Epoch:          3,
		LogStartOffset: 0,
		HW:             3,
	}

	r := newReplicaFromEnv(t, env)
	st := r.Status()
	if st.LEO != 3 || st.HW != 3 {
		t.Fatalf("status = %+v", st)
	}
	if got := env.log.truncateCalls; !reflect.DeepEqual(got, []uint64{3}) {
		t.Fatalf("truncate calls = %v", got)
	}
}

func TestNewReplicaRejectsCheckpointBeyondLEO(t *testing.T) {
	env := newTestEnv(t)
	env.log.leo = 2
	env.checkpoints.checkpoint = isr.Checkpoint{
		LogStartOffset: 0,
		HW:             3,
	}
	_, err := isr.NewReplica(env.config())
	if !errors.Is(err, isr.ErrCorruptState) {
		t.Fatalf("expected ErrCorruptState, got %v", err)
	}
}
```

- [ ] **Step 2: Run the recovery tests and confirm they fail**

Run: `go test ./pkg/isr -run 'TestNewReplicaTruncatesDirtyTailToCheckpointHW|TestNewReplicaRejectsCheckpointBeyondLEO' -v`

Expected: FAIL because constructor recovery is still a stub.

- [ ] **Step 3: Implement constructor-time recovery**

```go
func (r *replica) recoverFromStores() error {
	checkpoint, err := r.checkpoints.Load()
	if errors.Is(err, ErrEmptyState) {
		checkpoint = Checkpoint{}
	} else if err != nil {
		return err
	}
	history, err := r.history.Load()
	if errors.Is(err, ErrEmptyState) {
		history = nil
	} else if err != nil {
		return err
	}
}
```

Implementation details:
- load checkpoint then epoch history, honoring `ErrEmptyState`
- validate `LogStartOffset <= HW`
- read `LEO()` from the log store
- if `LEO > HW`, truncate to `HW` then `Sync()`
- set in-memory `LogStartOffset`, `HW`, `LEO`, and `epochHistory`
- finish in non-writable follower state

- [ ] **Step 4: Add coverage for empty-state bootstrapping**

Add at least one test that starts with:
- `CheckpointStore.Load() -> ErrEmptyState`
- `EpochHistoryStore.Load() -> ErrEmptyState`
- `LogStore.LEO() == 0`

Expected behavior:
- constructor succeeds
- `Status()` returns `LogStartOffset=0`, `HW=0`, `LEO=0`

- [ ] **Step 5: Re-run recovery, metadata, and API tests**

Run: `go test ./pkg/isr -run 'TestNewReplicaTruncatesDirtyTailToCheckpointHW|TestNewReplicaRejectsCheckpointBeyondLEO|TestApplyMetaRejectsInvalidISRSubset|TestNewReplicaValidatesRequiredDependencies' -v`

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add pkg/isr/replica.go pkg/isr/recovery.go pkg/isr/testenv_test.go pkg/isr/recovery_test.go
git commit -m "feat: add isr startup recovery"
```

## Task 4: Implement `BecomeLeader` durable sequencing

**Files:**
- Modify: `pkg/isr/recovery.go`
- Modify: `pkg/isr/meta.go`
- Modify: `pkg/isr/testenv_test.go`
- Modify: `pkg/isr/recovery_test.go`

- [ ] **Step 1: Write failing leader-promotion ordering tests**

```go
func TestBecomeLeaderTruncatesThenSyncsThenAppendsEpochPoint(t *testing.T) {
	env := newRecoveredLeaderEnv(t)
	env.log.leo = 6
	env.replica.mustApplyMeta(t, activeMeta(7, 1))

	if err := env.replica.BecomeLeader(activeMeta(7, 1)); err != nil {
		t.Fatalf("BecomeLeader() error = %v", err)
	}

	if got := env.calls.snapshot(); !reflect.DeepEqual(got, []string{
		"log.truncate:4",
		"log.sync",
		"history.append:7@4",
	}) {
		t.Fatalf("call order = %v", got)
	}
}

func TestBecomeLeaderReplaysIdenticalEpochPointIdempotently(t *testing.T) {
	env := newRecoveredLeaderEnv(t)
	env.history.points = []isr.EpochPoint{{Epoch: 7, StartOffset: 4}}
	env.replica.mustApplyMeta(t, activeMeta(7, 1))

	if err := env.replica.BecomeLeader(activeMeta(7, 1)); err != nil {
		t.Fatalf("BecomeLeader() error = %v", err)
	}
}
```

- [ ] **Step 2: Run the targeted recovery-order tests and confirm they fail**

Run: `go test ./pkg/isr -run 'TestBecomeLeaderTruncatesThenSyncsThenAppendsEpochPoint|TestBecomeLeaderReplaysIdenticalEpochPointIdempotently' -v`

Expected: FAIL because `BecomeLeader` does not yet enforce durable ordering.

- [ ] **Step 3: Implement `BecomeLeader` exactly in the approved order**

Implementation details:
- reject stale or invalid metadata before mutating state
- require startup recovery to have completed
- if `LEO > HW`, truncate to `HW` then `Sync()`
- append `EpochPoint{Epoch: meta.Epoch, StartOffset: LEO}` durably
- publish the updated history in memory only after successful append
- clear old peer progress and seed new progress map
- set role to `Leader` only after durable steps complete and lease is valid

- [ ] **Step 4: Add a crash-retry test**

Write one test where:
- the fake history store already contains the exact epoch point
- `BecomeLeader` is called again for the same epoch

Expected:
- the method succeeds
- no duplicate progress state is created
- the replica becomes writable exactly once

- [ ] **Step 5: Re-run all recovery tests**

Run: `go test ./pkg/isr -run 'TestNewReplica|TestBecomeLeader' -v`

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add pkg/isr/recovery.go pkg/isr/meta.go pkg/isr/testenv_test.go pkg/isr/recovery_test.go
git commit -m "feat: add isr leader promotion sequencing"
```

## Task 5: Implement leader fetch serving and divergence detection

**Files:**
- Create: `pkg/isr/progress.go`
- Create: `pkg/isr/fetch.go`
- Modify: `pkg/isr/replica.go`
- Modify: `pkg/isr/testenv_test.go`
- Create: `pkg/isr/fetch_test.go`

- [ ] **Step 1: Write failing fetch tests**

```go
func TestFetchRejectsInvalidBudget(t *testing.T) {
	r := newLeaderReplica(t)
	_, err := r.Fetch(context.Background(), isr.FetchRequest{
		GroupID:     10,
		Epoch:       3,
		ReplicaID:   2,
		FetchOffset: 0,
		OffsetEpoch: 3,
		MaxBytes:    0,
	})
	if !errors.Is(err, isr.ErrInvalidFetchBudget) {
		t.Fatalf("expected ErrInvalidFetchBudget, got %v", err)
	}
}

func TestFetchReturnsTruncateToWhenOffsetEpochDiverges(t *testing.T) {
	env := newFetchEnvWithHistory(t)
	result, err := env.replica.Fetch(context.Background(), isr.FetchRequest{
		GroupID:     10,
		Epoch:       7,
		ReplicaID:   2,
		FetchOffset: 5,
		OffsetEpoch: 4,
		MaxBytes:    1024,
	})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if result.TruncateTo == nil || *result.TruncateTo != 4 {
		t.Fatalf("result.TruncateTo = %v", result.TruncateTo)
	}
}
```

- [ ] **Step 2: Run the fetch tests and confirm they fail**

Run: `go test ./pkg/isr -run 'TestFetchRejectsInvalidBudget|TestFetchReturnsTruncateToWhenOffsetEpochDiverges' -v`

Expected: FAIL because fetch validation and divergence logic are not implemented.

- [ ] **Step 3: Implement leader-side fetch validation and read path**

Implementation details:
- require `ReplicaID != 0`
- reject non-leader, stale-epoch, and tombstoned fetches
- return `ErrSnapshotRequired` when `FetchOffset < LogStartOffset`
- read whole-record prefixes from `LogStore.Read(from, maxBytes)`
- return the current `HW` and leader `Epoch`

- [ ] **Step 4: Implement divergence detection and leader progress updates**

Implementation details:
- use `OffsetEpoch` plus local `epochHistory` to detect when a follower tail belongs to an older branch
- compute `TruncateTo` as the highest safe offset on the leader branch
- treat `FetchOffset` from `ReplicaID` as an acknowledgement that offsets `< FetchOffset` are durable on that follower
- update the leader’s per-replica progress before serving new records

- [ ] **Step 5: Re-run the fetch tests and add one snapshot-required test**

Run: `go test ./pkg/isr -run 'TestFetchRejectsInvalidBudget|TestFetchReturnsTruncateToWhenOffsetEpochDiverges|TestFetchReturnsSnapshotRequiredWhenFollowerFallsBehindLogStart' -v`

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add pkg/isr/progress.go pkg/isr/fetch.go pkg/isr/replica.go pkg/isr/testenv_test.go pkg/isr/fetch_test.go
git commit -m "feat: add isr fetch and divergence handling"
```

## Task 6: Implement follower-side `ApplyFetch`

**Files:**
- Create: `pkg/isr/replication.go`
- Modify: `pkg/isr/replica.go`
- Modify: `pkg/isr/testenv_test.go`
- Create: `pkg/isr/replication_test.go`

- [ ] **Step 1: Write failing follower-apply tests**

```go
func TestApplyFetchTruncatesUncommittedTailBeforeAppending(t *testing.T) {
	env := newFollowerEnv(t)
	env.status.HW = 4
	env.log.leo = 6
	truncateTo := uint64(4)

	err := env.replica.ApplyFetch(context.Background(), isr.ApplyFetchRequest{
		GroupID:    10,
		Epoch:      7,
		Leader:     1,
		TruncateTo: &truncateTo,
		Records:    []isr.Record{{Payload: []byte("a"), SizeBytes: 1}},
		LeaderHW:   5,
	})
	if err != nil {
		t.Fatalf("ApplyFetch() error = %v", err)
	}
	if got := env.log.truncateCalls; !reflect.DeepEqual(got, []uint64{4}) {
		t.Fatalf("truncate calls = %v", got)
	}
}

func TestApplyFetchAdvancesCheckpointToMinLeaderHWAndLEO(t *testing.T) {
	env := newFollowerEnv(t)
	err := env.replica.ApplyFetch(context.Background(), isr.ApplyFetchRequest{
		GroupID:  10,
		Epoch:    7,
		Leader:   1,
		Records:  []isr.Record{{Payload: []byte("a"), SizeBytes: 1}},
		LeaderHW: 10,
	})
	if err != nil {
		t.Fatalf("ApplyFetch() error = %v", err)
	}
	if got := env.checkpoints.lastStored(); got.HW != 1 {
		t.Fatalf("stored checkpoint = %+v", got)
	}
}
```

- [ ] **Step 2: Run the follower-apply tests and confirm they fail**

Run: `go test ./pkg/isr -run 'TestApplyFetchTruncatesUncommittedTailBeforeAppending|TestApplyFetchAdvancesCheckpointToMinLeaderHWAndLEO' -v`

Expected: FAIL because `ApplyFetch` is still a stub.

- [ ] **Step 3: Implement follower-side apply semantics**

Implementation details:
- reject stale epoch or tombstoned state
- allow follower and fenced-leader roles to consume leader data
- if `TruncateTo != nil`, enforce `TruncateTo >= HW`, then truncate and `Sync()`
- append records, `Sync()` the log, update `LEO`
- advance local `HW = min(LeaderHW, LEO)`
- store `Checkpoint{Epoch, LogStartOffset, HW}` only after local log durability

- [ ] **Step 4: Add one stale-epoch rejection test and one truncate-safety test**

Required coverage:
- `ApplyFetch` with older `Epoch` returns `ErrStaleMeta`
- `ApplyFetch` refuses to truncate below current `HW` and returns `ErrCorruptState`

- [ ] **Step 5: Re-run the follower-apply and fetch suites**

Run: `go test ./pkg/isr -run 'TestApplyFetch|TestFetch' -v`

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add pkg/isr/replication.go pkg/isr/replica.go pkg/isr/testenv_test.go pkg/isr/replication_test.go pkg/isr/fetch_test.go
git commit -m "feat: add isr follower fetch application"
```

## Task 7: Implement append, MinISR commit gating, and lease fencing

**Files:**
- Create: `pkg/isr/append.go`
- Modify: `pkg/isr/progress.go`
- Modify: `pkg/isr/replica.go`
- Modify: `pkg/isr/testenv_test.go`
- Create: `pkg/isr/append_test.go`

- [ ] **Step 1: Write failing append tests**

```go
func TestAppendRejectsReplicaThatIsNotLeader(t *testing.T) {
	r := newFollowerReplica(t)
	_, err := r.Append(context.Background(), []isr.Record{{Payload: []byte("x"), SizeBytes: 1}})
	if !errors.Is(err, isr.ErrNotLeader) {
		t.Fatalf("expected ErrNotLeader, got %v", err)
	}
}

func TestAppendWaitsUntilMinISRReplicasAcknowledgeViaFetch(t *testing.T) {
	env := newThreeReplicaCluster(t)
	done := make(chan isr.CommitResult, 1)

	go func() {
		res, err := env.leader.Append(context.Background(), []isr.Record{{Payload: []byte("x"), SizeBytes: 1}})
		if err != nil {
			t.Errorf("Append() error = %v", err)
			return
		}
		done <- res
	}()

	env.replicateOnce(t, env.follower2)
	select {
	case <-done:
		t.Fatal("append returned before MinISR was satisfied")
	default:
	}

	env.replicateOnce(t, env.follower3)
	res := <-done
	if res.NextCommitHW != 1 {
		t.Fatalf("NextCommitHW = %d", res.NextCommitHW)
	}
}
```

- [ ] **Step 2: Run the append tests and confirm they fail**

Run: `go test ./pkg/isr -run 'TestAppendRejectsReplicaThatIsNotLeader|TestAppendWaitsUntilMinISRReplicasAcknowledgeViaFetch' -v`

Expected: FAIL because append waiting and commit progression are not implemented.

- [ ] **Step 3: Implement leader append and waiter management**

Implementation details:
- gate on `Leader` role, current epoch, `len(ISR) >= MinISR`, and non-expired lease
- append batches under one critical section
- `LogStore.Append` then `LogStore.Sync` before the write becomes eligible for commit
- create a waiter keyed by the appended end offset
- seed local leader progress to the new `LEO`
- wake waiters only after `HW` advances past their requested end offset

- [ ] **Step 4: Implement `HW` advancement and checkpoint-before-success**

Implementation details:
- compute `HW` as the `MinISR`-th largest `MatchOffsetExclusive`
- store `Checkpoint{Epoch, LogStartOffset, HW}` before resolving append waiters
- on lease expiry, move from `Leader` to `FencedLeader` before returning `ErrLeaseExpired`
- make subsequent `Append` calls from `FencedLeader` fail without touching the log

- [ ] **Step 5: Re-run the append suite and add one lease-expiry test**

Run: `go test ./pkg/isr -run 'TestAppend|TestLeaderLeaseExpiryFencesAppend' -v`

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add pkg/isr/append.go pkg/isr/progress.go pkg/isr/replica.go pkg/isr/testenv_test.go pkg/isr/append_test.go
git commit -m "feat: add isr append commit flow"
```

## Task 8: Implement snapshot installation and run the full regression suite

**Files:**
- Modify: `pkg/isr/recovery.go`
- Modify: `pkg/isr/replication.go`
- Modify: `pkg/isr/testenv_test.go`
- Create: `pkg/isr/snapshot_test.go`

- [ ] **Step 1: Write failing snapshot tests**

```go
func TestInstallSnapshotPersistsPayloadBeforeCheckpoint(t *testing.T) {
	env := newFollowerEnv(t)
	snap := isr.Snapshot{
		GroupID:   10,
		Epoch:     7,
		EndOffset: 8,
		Payload:   []byte("snap"),
	}

	if err := env.replica.InstallSnapshot(context.Background(), snap); err != nil {
		t.Fatalf("InstallSnapshot() error = %v", err)
	}
	if got := env.calls.snapshot(); !reflect.DeepEqual(got, []string{
		"snapshot.install:8",
		"checkpoint.store:8",
	}) {
		t.Fatalf("call order = %v", got)
	}
}

func TestInstallSnapshotRejectsLogStoreBehindSnapshotEndOffset(t *testing.T) {
	env := newFollowerEnv(t)
	env.log.leo = 5
	err := env.replica.InstallSnapshot(context.Background(), isr.Snapshot{
		GroupID:   10,
		Epoch:     7,
		EndOffset: 8,
	})
	if !errors.Is(err, isr.ErrCorruptState) {
		t.Fatalf("expected ErrCorruptState, got %v", err)
	}
}
```

- [ ] **Step 2: Run the snapshot tests and confirm they fail**

Run: `go test ./pkg/isr -run 'TestInstallSnapshotPersistsPayloadBeforeCheckpoint|TestInstallSnapshotRejectsLogStoreBehindSnapshotEndOffset' -v`

Expected: FAIL because snapshot installation ordering is not implemented.

- [ ] **Step 3: Implement `InstallSnapshot` exactly as specified**

Implementation details:
- call `SnapshotApplier.InstallSnapshot(...)` first
- then store `Checkpoint{Epoch: snap.Epoch, LogStartOffset: snap.EndOffset, HW: snap.EndOffset}`
- then update in-memory `LogStartOffset`, `HW`, and `LEO`
- verify `LEO >= snap.EndOffset`
- keep the replica non-writable until a later `BecomeLeader`

- [ ] **Step 4: Run the full `pkg/isr` test suite**

Run: `go test ./pkg/isr -v`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/isr/recovery.go pkg/isr/replication.go pkg/isr/testenv_test.go pkg/isr/snapshot_test.go
git commit -m "feat: add isr snapshot recovery"
```

## Final Verification

- [ ] Run: `go test ./pkg/isr -v`
  Expected: PASS
- [ ] Run: `go test ./pkg/...`
  Expected: PASS for the affected package set, or any unrelated failures are documented before handoff.
- [ ] Run: `git status --short`
  Expected: only the intended `pkg/isr` files and any deliberately updated docs are modified.

