# Channel Replica State Machine Refactor Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking. This refactor is sequential by default: do not parallelize implementation tasks unless a task explicitly assigns disjoint file ownership.

**Goal:** Refactor `pkg/channel/replica` into a single-writer state-machine implementation with shared pure safety rules, explicit durable transactions/effects, safe legacy ACK compatibility, and package-level FLOW documentation.

**Architecture:** Preserve the public `Replica` facade while moving mutable state ownership into an internal event loop. Extract epoch/quorum/reconcile safety decisions into pure functions; serialize log-mutating durable effects; fence stale results by epoch/role generation/effect id; and migrate ownership by mutable field rather than by method name.

**Tech Stack:** Go, `context`, channels, atomic snapshots, existing `pkg/channel` contracts, `testify/require`, Go race detector.

---

## Migration Ownership Matrix

Update this matrix as each migration task moves ownership. A task is not complete if a mutable field has two active production writers without an explicit temporary compatibility guard.

| Mutable field | Current writers | Target owner | Migration task | Old writer removal |
|---------------|-----------------|--------------|----------------|--------------------|
| `meta` | loop lifecycle commands (`ApplyMeta`, `BecomeLeader`, `BecomeFollower`) | loop | Task 8 completed | Task 17 cleanup |
| `state` | lifecycle/meta/recovery/snapshot, leader progress/HW, checkpoint result publication, append publication/fencing, fetch read fencing, follower apply result publication, and leader reconcile result publication via loop | loop | Task 8 partial; Tasks 9-10 completed; Task 12 append completed; Task 13 fetch completed; Task 14 follower apply completed; Task 15 reconcile completed | Task 17 |
| `progress` | loop progress pipeline (`Fetch`, `ApplyFollowerCursor`, append publish, reconcile proof) | loop | Task 9 completed | Task 17 cleanup |
| `waiters` | loop append pipeline, HW advance, cancel, close/follower/tombstone | loop | Task 12 completed | Task 17 cleanup |
| `appendPending` | loop append pipeline | loop | Task 12 completed | Task 17 cleanup |
| `checkpointQueued/InFlight` | loop checkpoint effect queue and fenced checkpoint writer results | loop | Task 10 completed | Task 17 cleanup |
| `reconcilePending` | become leader, apply proof, complete reconcile, and fenced reconcile durable result via loop | loop | Task 15 completed | Task 17 |
| `epochHistory` | recovery before workers plus begin-leader epoch result, snapshot, follower apply/truncate result, and reconcile truncate result handling via loop | loop + durable adapter | Tasks 7, 8 partial; Task 14 follower apply completed; Task 15 reconcile completed | Task 17 |
| `closed/tombstoned` | loop lifecycle commands with queued-command fencing | loop | Task 8 completed | Task 17 cleanup |

---

### Task 0: Preflight And Safety Check

**Files:**
- Read: `AGENTS.md`
- Read: `pkg/channel/FLOW.md`
- Read: `docs/superpowers/specs/2026-04-25-channel-replica-state-machine-refactor-design.md`
- Read: `docs/superpowers/plans/2026-04-25-channel-replica-state-machine-refactor.md`

- [x] **Step 1: Record git state**

Run: `git status --short`

Expected: identify all modified/untracked files. Do not overwrite user changes. If files outside this plan are modified, leave them alone.

Note for project-local `.worktrees/` under the parent `go.work`: prefix Go commands with `GOWORK=off` unless the worktree has been added to the workspace.

- [x] **Step 2: Confirm package docs**

Run: `test ! -f pkg/channel/replica/FLOW.md || sed -n '1,220p' pkg/channel/replica/FLOW.md`

Expected: if package FLOW exists, read it before edits. If it does not exist, final FLOW will be created in Task 18.

- [x] **Step 3: Establish sequential execution**

Record in implementation notes: Tasks 1-19 are sequential unless explicitly split by file ownership. Subagents may review, write isolated pure-function tests, or inspect code; they must not concurrently edit shared files such as `replica.go`, `progress.go`, `append.go`, `replication.go`, or `loop.go`.

---

### Task 1: Add OffsetEpoch Safety Regression Tests

**Files:**
- Modify: `pkg/channel/replica/progress_ack_test.go`

- [x] **Step 1: Write failing test for divergent cursor**

Add `TestCursorDeltaWithDivergentOffsetEpochDoesNotAdvanceHW`:

```go
func TestCursorDeltaWithDivergentOffsetEpochDoesNotAdvanceHW(t *testing.T) {
    env := newFetchEnvWithHistory(t)
    r := env.replica

    r.mu.Lock()
    r.state.HW = 4
    r.state.CheckpointHW = 4
    r.state.LEO = 6
    r.progress[r.localNode] = 6
    r.progress[2] = 4
    r.publishStateLocked()
    r.mu.Unlock()

    err := r.ApplyFollowerCursor(context.Background(), channel.ReplicaFollowerCursorUpdate{
        ChannelKey:  "group-10",
        Epoch:       7,
        ReplicaID:   2,
        MatchOffset: 6,
        OffsetEpoch: 4,
    })
    require.NoError(t, err)

    st := r.Status()
    require.Equal(t, uint64(4), st.HW)
    r.mu.RLock()
    require.Equal(t, uint64(4), r.progress[2])
    r.mu.RUnlock()
}
```

- [x] **Step 2: Write failing test for future epoch**

Add `TestCursorDeltaRejectsFutureOffsetEpoch` using a leader with latest epoch `7`, `OffsetEpoch: 99`, and `MatchOffset > HW`. Assert `ErrStaleMeta`, unchanged progress, unchanged HW.

- [x] **Step 3: Write failing test for legacy progress ACK**

Add `TestLegacyProgressAckWithHistoryCannotAdvanceHW`. Use `ApplyProgressAck` with `MatchOffset > HW` when epoch history exists. Assert nil error for compatibility, unchanged HW, and progress capped at or below current HW.

- [x] **Step 4: Run RED tests**

Run: `go test ./pkg/channel/replica -run 'TestCursorDeltaWithDivergentOffsetEpochDoesNotAdvanceHW|TestCursorDeltaRejectsFutureOffsetEpoch|TestLegacyProgressAckWithHistoryCannotAdvanceHW' -count=1`

Expected: FAIL because current `ApplyFollowerCursor` ignores `OffsetEpoch` and `ApplyProgressAck` uses zero epoch unsafely.

- [x] **Step 5: Implement minimal safety fix**

Update current `ApplyFollowerCursor` and `ApplyProgressAck` enough to satisfy the tests using existing helper logic. This is a safety patch before the larger loop migration.

- [x] **Step 6: Run GREEN tests**

Run: `go test ./pkg/channel/replica -run 'TestCursorDeltaWithDivergentOffsetEpochDoesNotAdvanceHW|TestCursorDeltaRejectsFutureOffsetEpoch|TestLegacyProgressAckWithHistoryCannotAdvanceHW' -count=1`

Expected: PASS.

---

### Task 2: Extract Epoch Lineage Pure Functions

**Files:**
- Create: `pkg/channel/replica/epoch_lineage.go`
- Create: `pkg/channel/replica/epoch_lineage_test.go`
- Modify: `pkg/channel/replica/progress.go`
- Modify: `pkg/channel/replica/promotion_evaluator.go`
- Modify: `pkg/channel/replica/history.go`

- [x] **Step 1: Write table-driven tests**

Add tests covering the spec truth table: known epoch, next epoch truncation, future epoch, unknown epoch, zero epoch with empty history, zero epoch with non-empty history, offset below log start, and cap at leader LEO.

- [x] **Step 2: Run RED**

Run: `go test ./pkg/channel/replica -run '^TestEpochLineage' -count=1`

Expected: FAIL or compile failure until extracted helpers exist.

- [x] **Step 3: Implement pure helpers**

Create helpers similar to:

```go
type lineageDecisionKind uint8

type lineageDecision struct {
    MatchOffset uint64
    TruncateTo  *uint64
    Snapshot    bool
    Err         error
}

func offsetEpochForLEO(history []channel.EpochPoint, leo uint64) uint64
func decideLineage(history []channel.EpochPoint, logStartOffset, currentHW, leaderLEO, remoteOffset, offsetEpoch uint64) lineageDecision
func matchOffsetForProof(history []channel.EpochPoint, currentHW, leaderLEO, logEndOffset, offsetEpoch uint64) (uint64, error)
```

- [x] **Step 4: Route current code through helpers**

Replace duplicated lineage logic in `progress.go` and `promotion_evaluator.go` with `epoch_lineage.go` helpers.

- [x] **Step 5: Run focused tests**

Run: `go test ./pkg/channel/replica -run 'TestEpochLineage|TestFetch|TestPromotionEvaluator|TestCursorDelta|TestLegacyProgressAck' -count=1`

Expected: PASS.

---

### Task 3: Extract Quorum Progress Pure Functions

**Files:**
- Create: `pkg/channel/replica/progress_tracker.go`
- Create: `pkg/channel/replica/progress_tracker_test.go`
- Modify: `pkg/channel/replica/progress.go`

- [x] **Step 1: Write failing tests**

Add `TestQuorumProgressCandidate` table cases for MinISR 1/2/3, missing progress entries defaulting to zero, candidate not beyond HW, invalid MinISR, and candidate beyond LEO.

- [x] **Step 2: Run RED**

Run: `go test ./pkg/channel/replica -run '^TestQuorumProgress' -count=1`

Expected: FAIL or compile failure until helper exists.

- [x] **Step 3: Implement helper**

Add a helper returning `(candidate uint64, ok bool, err error)` from ISR, progress, MinISR, HW, LEO.

- [x] **Step 4: Use helper from current HW advancement**

Keep behavior compatible, but delegate candidate selection to pure helper.

- [x] **Step 5: Run progress tests**

Run: `go test ./pkg/channel/replica -run 'TestQuorumProgress|TestAppend|TestApplyProgressAck|TestCursorDelta|TestStatus' -count=1`

Expected: PASS.

---

### Task 4: Add Durable Adapter Contract And Tests

**Files:**
- Create: `pkg/channel/replica/durable_store.go`
- Create: `pkg/channel/replica/durable_store_test.go`
- Modify: `pkg/channel/replica/replica.go`
- Modify: `pkg/channel/replica/testenv_test.go`

- [x] **Step 1: Write contract tests for fake adapter**

Add tests for `BeginEpoch`, `AppendLeaderBatch`, `ApplyFollowerBatch` with optional epoch point, `TruncateLogAndHistory`, `StoreCheckpointMonotonic`, and `InstallSnapshotAtomically` using fake stores. Cover checkpoint > HW/LEO rejection, truncate history/log consistency, follower apply records+epoch consistency, and snapshot payload+checkpoint+history consistency.

- [x] **Step 2: Run RED**

Run: `go test ./pkg/channel/replica -run '^TestDurableStore' -count=1`

Expected: FAIL or compile failure until adapter exists.

- [x] **Step 3: Implement adapter wrapping existing split interfaces**

Create package-private adapter that wraps current `LogStore`, `CheckpointStore`, `ApplyFetchStore`, `EpochHistoryStore`, and `SnapshotApplier`. Keep `ReplicaConfig` compatible. If `ApplyFetchStore` is nil, preserve the current fallback through `LogStore.Append` + checkpoint store for compatibility, but mark it as split-store fallback and require recovery validation for partial records/checkpoint/history states. Production `pkg/channel/store.ChannelStore` should implement the combined durable path so follower records, optional checkpoint, and optional epoch point are one Pebble batch.

- [x] **Step 4: Add recovery validation for all partial durable states**

If atomic truncate/snapshot/epoch apply cannot be guaranteed by wrapped stores, make recovery validate log/history/snapshot/checkpoint consistency and return `ErrCorruptState` for mismatched unsafe state. Add a note that split-store fallback is compatibility-only and not the preferred production path.

- [x] **Step 5: Run durable tests**

Run: `go test ./pkg/channel/replica -run '^TestDurableStore|TestNewReplica|TestRecover|TestInstallSnapshot' -count=1`

Expected: PASS.

---

### Task 5: Add Invariant Checker

**Files:**
- Create: `pkg/channel/replica/invariant.go`
- Create: `pkg/channel/replica/invariant_test.go`

- [x] **Step 1: Write failing invariant tests**

Add tests for invalid `LogStartOffset > CheckpointHW`, `CheckpointHW > HW`, `HW > LEO`, offset epoch mismatch, tail truncation below HW, and snapshot regression.

- [x] **Step 2: Run RED**

Run: `go test ./pkg/channel/replica -run '^TestReplicaInvariant' -count=1`

Expected: FAIL or compile failure until invariant checker exists.

- [x] **Step 3: Implement invariant checker**

Add package-private validation used by tests and later machine transitions.

- [x] **Step 4: Run invariant tests**

Run: `go test ./pkg/channel/replica -run '^TestReplicaInvariant' -count=1`

Expected: PASS.

---

### Task 6: Add Command, Result, Effect, And Machine Types

**Files:**
- Create: `pkg/channel/replica/commands.go`
- Create: `pkg/channel/replica/machine.go`
- Create: `pkg/channel/replica/state.go`
- Create: `pkg/channel/replica/machine_test.go`

- [x] **Step 1: Write machine tests first**

Add tests for append validation, cursor progress update, HW advance completion, checkpoint result handling, stale effect discard, tombstone, and close.

- [x] **Step 2: Run RED**

Run: `go test ./pkg/channel/replica -run '^TestMachine' -count=1`

Expected: FAIL or compile failure until machine types exist.

- [x] **Step 3: Implement minimal machine model**

Create internal state, event, effect, and completion types and implement enough transitions for tests. Machine transitions must call invariant checks. `state.go` is introduced here so later loop/snapshot work compiles against a stable internal state type.

- [x] **Step 4: Run machine tests**

Run: `go test ./pkg/channel/replica -run '^TestMachine' -count=1`

Expected: PASS.

---

### Task 7: Add Loop Skeleton Without Moving Ownership

**Files:**
- Create: `pkg/channel/replica/loop.go`
- Modify: `pkg/channel/replica/replica.go`
- Modify: `pkg/channel/replica/lifecycle_test.go`

- [x] **Step 1: Write loop lifecycle tests**

Add tests that loop starts/stops, command submission after close returns a close/not-leader error, and `Status` remains non-blocking.

- [x] **Step 2: Run RED**

Run: `go test ./pkg/channel/replica -run '^TestReplicaLoop' -count=1`

Expected: FAIL or compile failure until loop skeleton exists.

- [x] **Step 3: Implement loop skeleton**

Add command channel, result handling, stop channel, and worker lifecycle. At this stage, old mutable fields are not yet owned by the loop; do not claim single ownership yet.

- [x] **Step 4: Run loop lifecycle tests**

Run: `go test ./pkg/channel/replica -run '^TestReplicaLoop|TestCloseStopsCollectorGoroutine' -count=1`

Expected: PASS.

---

### Task 8: Migrate Lifecycle, Meta, Recovery, And Snapshot To Loop Ownership

**Files:**
- Modify: `pkg/channel/replica/replica.go`
- Modify: `pkg/channel/replica/meta.go`
- Modify: `pkg/channel/replica/recovery.go`
- Create: `pkg/channel/replica/snapshot_pipeline.go`
- Modify: `pkg/channel/replica/snapshot_test.go`
- Modify: `pkg/channel/replica/meta_test.go`
- Modify: `pkg/channel/replica/recovery_test.go`

- [x] **Step 1: Add characterization tests**

Ensure existing meta/recovery/snapshot tests cover current behavior. Add missing tests for snapshot stale effect fencing and recovery failing before workers start.

- [x] **Step 2: Run characterization tests**

Run: `go test ./pkg/channel/replica -run 'TestApplyMeta|TestBecomeFollower|TestTombstone|TestNewReplica|TestRecover|TestInstallSnapshot' -count=1`

Expected: PASS before refactor.

- [x] **Step 3: Route lifecycle/meta commands through loop**

Move `ApplyMeta`, `BecomeFollower`, `Tombstone`, `Close`, and state publication into loop commands. Increment role generation on role/meta/tombstone/close transitions.

- [x] **Step 4: Route recovery and snapshot through durable adapter/effects**

Run recovery before loop workers start. Route `InstallSnapshot` through command/effect/result with fencing.

- [x] **Step 5: Run tests**

Run: `go test ./pkg/channel/replica -run 'TestApplyMeta|TestBecomeFollower|TestTombstone|TestNewReplica|TestRecover|TestInstallSnapshot|TestReplicaLoop' -count=1`

Expected: PASS.

- [x] **Step 6: Update ownership matrix**

Mark `meta`, `closed/tombstoned`, snapshot mutation, and recovery publication as loop-owned.

---

### Task 9: Migrate All Progress And HW Writers Together

**Files:**
- Modify: `pkg/channel/replica/progress.go`
- Modify: `pkg/channel/replica/fetch.go`
- Modify: `pkg/channel/replica/append.go`
- Modify: `pkg/channel/replica/reconcile.go`
- Modify: `pkg/channel/replica/loop.go`
- Modify: `pkg/channel/replica/machine.go`
- Modify: `pkg/channel/replica/progress_ack_test.go`
- Modify: `pkg/channel/replica/progress_test.go`

- [x] **Step 1: Add tests for every current progress writer**

Cover cursor, fetch ACK progress, append local progress publish, reconcile proof progress, HW advance, stale progress effect/result, and legacy `ApplyProgressAck` cap.

- [x] **Step 2: Run RED/characterization mix**

Run: `go test ./pkg/channel/replica -run 'TestCursorDelta|TestProgressCursor|TestStatusIsUpdatedAfterFollowerAckAdvancesHW|TestAppendQuorum|TestBecomeLeader' -count=1`

Expected: new stale/cap tests fail before migration; existing characterization tests pass.

- [x] **Step 3: Move progress/HW ownership to loop in one slice**

Ensure no production path outside loop mutates `progress`, runtime `HW`, or waiter completion from HW advance.

- [x] **Step 4: Run focused tests**

Run: `go test ./pkg/channel/replica -run 'TestCursorDelta|TestProgressCursor|TestStatus|TestAppendQuorum|TestBecomeLeader|TestLegacyProgressAck' -count=1`

Expected: PASS.

- [x] **Step 5: Update ownership matrix**

Mark `progress` and runtime `HW` loop-owned. Note any remaining temporary compatibility guards.

Task 9 note: leader-side `progress`, leader runtime `HW`, and waiter completion caused by HW advance now run through loop events. Follower `ApplyFetch` HW publication, checkpoint `CheckpointHW`/`CommitReady` publication, and append lease-fence compatibility guards remain intentionally transitional for later tasks.

---

### Task 10: Migrate Checkpoint Effects

**Files:**
- Create: `pkg/channel/replica/checkpoint_writer.go`
- Modify: `pkg/channel/replica/progress.go`
- Modify: `pkg/channel/replica/loop.go`
- Modify: `pkg/channel/replica/progress_async_test.go`

- [x] **Step 1: Add new failing stale-result tests**

Add tests for checkpoint result arriving after a newer checkpoint is queued and checkpoint result arriving after tombstone/close. Existing checkpoint retry tests are characterization and should pass before/after.

- [x] **Step 2: Run RED/characterization mix**

Run: `go test ./pkg/channel/replica -run 'TestCheckpointWriter|TestApplyProgressAckReturnsBeforeCheckpointStoreCompletes|TestAdvanceCommitHWCompletesWaitersBeforeCheckpointStoreReturns' -count=1`

Expected: new stale-result tests fail before migration; existing characterization tests pass.

- [x] **Step 3: Implement checkpoint effects**

Loop emits `storeCheckpointEffect`; writer stores checkpoint and returns fenced result event. Loop coalesces latest pending checkpoint and never lets `CheckpointHW` exceed runtime HW/LEO.

- [x] **Step 4: Run async checkpoint tests**

Run: `go test ./pkg/channel/replica -run 'TestCheckpointWriter|TestApplyProgressAckReturnsBeforeCheckpointStoreCompletes|TestAdvanceCommitHWCompletesWaitersBeforeCheckpointStoreReturns' -count=1`

Expected: PASS.

- [x] **Step 5: Update ownership matrix**

Mark checkpoint queue/in-flight state loop-owned.

Task 10 note: checkpoint queue/in-flight state is loop-owned. The checkpoint writer only executes `storeCheckpointEffect` and returns fenced `machineCheckpointStoredEvent` results; stale/closed results do not publish `CheckpointHW`, and newer queued checkpoints are emitted after older results complete.

---

### Task 11: Harden Immutable Snapshot Usage

**Files:**
- Modify: `pkg/channel/replica/state.go`
- Modify: `pkg/channel/replica/append.go`
- Modify: `pkg/channel/replica/fetch.go`
- Modify: `pkg/channel/replica/progress.go`
- Modify: `pkg/channel/replica/replication.go`
- Modify: `pkg/channel/replica/reconcile.go`

- [x] **Step 1: Audit unlock-then-read usage**

Run: `rg -n 'r\.mu\.Unlock\(\)|r\.state\.|r\.meta\.' pkg/channel/replica/{append.go,fetch.go,progress.go,replication.go,reconcile.go,replica.go,recovery.go}`

Expected: identify every path that logs, traces, or builds store requests from mutable state after unlock.

- [x] **Step 2: Add snapshot helper tests if needed**

Add tests only for behavior not already covered by race tests.

- [x] **Step 3: Replace mutable state reads with snapshots**

Use loop-owned snapshots or local copies captured before unlock. Logging and notification effects must use snapshot fields, not mutable `r.state`.

- [x] **Step 4: Run race detector focused set**

Run: `go test -race ./pkg/channel/replica -run 'TestAppend|TestFetch|TestApplyFetch|TestStatus|TestCursorDelta|TestCheckpointWriter' -count=1`

Expected: PASS.

Task 11 note: audit found the Task 9/10 progress, fetch, append, and checkpoint paths already log/notify using captured snapshots. `ApplyFetch` builds durable requests and trace fields from local copies before unlock; remaining direct state publication is intentionally migrated in later tasks. Focused race verification passed, so no extra helper tests were needed.

---

### Task 12: Migrate Append Pipeline In Smaller Slices

**Files:**
- Create: `pkg/channel/replica/append_pipeline.go`
- Create or modify: `pkg/channel/replica/waiters.go`
- Modify: `pkg/channel/replica/append.go`
- Modify: `pkg/channel/replica/pool.go`
- Modify: `pkg/channel/replica/append_test.go`

- [x] **Step 1: Add failing ownership tests**

Add tests for queued cancel, durable in-flight cancel, quorum-wait cancel, close with queued append, leadership loss while append waits, and durable append result after lease expiry. Assert each append returns exactly once.

- [x] **Step 2: Run RED**

Run: `go test ./pkg/channel/replica -run 'TestAppend.*Cancel|TestAppend.*Lease|TestCloseFailsPendingAppendRequests|TestBecomeFollowerFailsOutstandingAppendWaiters|TestTombstoneFailsOutstandingAppendWaiters' -count=1`

Expected: new ownership/fencing tests fail before migration; existing tests pass.

- [x] **Step 3: Move waiter registry to loop**

Implement request ids and states: queued, durable in-flight, waiting quorum, completed. Context cancellation submits cancel command; loop completes exactly once.

- [x] **Step 4: Move group commit queue to loop/effect**

Preserve default `1ms / 64 / 64KB` batching and existing `CommitModeLocal`/quorum commit modes.

- [x] **Step 5: Move leader durable append effect**

Use `AppendLeaderBatch` durable adapter and effect fencing. Re-validate appendability before publishing LEO.

- [x] **Step 6: Remove request/waiter pooling unless ownership is trivial**

Prefer no pooling over subtle lifecycle bugs. If pooling remains, add tests proving objects are released exactly once and channel completions cannot leak between requests.

- [x] **Step 7: Run append tests**

Run: `go test ./pkg/channel/replica -run 'TestAppend|TestLeaderLease|TestBecomeFollowerFailsOutstandingAppendWaiters|TestTombstoneFailsOutstandingAppendWaiters|TestCloseFailsPendingAppendRequests' -count=1`

Expected: PASS.

- [x] **Step 8: Update ownership matrix**

Mark `appendPending` and `waiters` loop-owned.

Task 12 note: append facade now submits loop commands for validation, queueing, cancellation, and zero-record results. The loop owns queued/durable/waiting/completed request stages, group commit batching, durable append effect fencing, waiter completion, and append LEO/local progress publication. Durable append effects/results use effect IDs plus request IDs, revalidate appendability before pending durable writes and before LEO publication, validate durable `newLEO`, and use close-cancelable append effect contexts when the durable store supports context-aware sync. Request/waiter pooling was removed to keep exact-once ownership simple. Spec compliance and code-quality subagent reviews approved Task 12 after the close-cancellation, stale queued write, durable `newLEO`, and request-ID fencing fixes.

---

### Task 13: Migrate Leader Fetch/Read-log Path

**Files:**
- Create: `pkg/channel/replica/fetch_pipeline.go`
- Modify: `pkg/channel/replica/fetch.go`
- Modify: `pkg/channel/replica/fetch_test.go`

- [x] **Step 1: Add fetch state-machine tests**

Cover invalid budget, stale epoch/key, snapshot required, truncate response, max visible records, fetch progress update through lineage, and stale read-log result fencing.

- [x] **Step 2: Run RED/characterization mix**

Run: `go test ./pkg/channel/replica -run 'TestFetch' -count=1`

Expected: new stale/fenced tests fail before migration; existing fetch tests pass.

- [x] **Step 3: Route `Fetch` through loop**

Loop computes safe response and emits read-log effect when records are needed. Read-log result must be clipped to captured leader LEO.

- [x] **Step 4: Run fetch tests**

Run: `go test ./pkg/channel/replica -run 'TestFetch' -count=1`

Expected: PASS.

Task 13 note: `Fetch` now uses a loop-computed progress/read snapshot and a fenced read-log result command before records are returned. The read-log effect carries effect ID, channel key, epoch, role generation, captured leader LEO, fetch offset, max bytes, and the base result; result handling rejects stale role/meta/generation changes, same-generation LEO regressions, and log-start advancement before clipping records to the captured leader LEO. Added blocking-read leadership-loss and same-generation LEO-regression tests that failed before the migration and now return fenced errors without records. Spec compliance and code-quality subagent reviews approved Task 13.

---

### Task 14: Migrate Follower Apply And Snapshot Durable Paths

**Files:**
- Create: `pkg/channel/replica/follower_apply.go`
- Modify: `pkg/channel/replica/replication.go`
- Modify: `pkg/channel/replica/recovery.go`
- Modify: `pkg/channel/replica/replication_test.go`
- Modify: `pkg/channel/replica/snapshot_test.go`

- [x] **Step 1: Add apply/truncate stale-result tests**

Cover stale apply result after role/meta change, truncate result racing with append/apply result, follower apply across a new epoch with records+history consistency, and snapshot result after tombstone/close.

- [x] **Step 2: Run RED/characterization mix**

Run: `go test ./pkg/channel/replica -run 'TestApplyFetch|TestInstallSnapshot' -count=1`

Expected: new stale-result tests fail before migration; existing tests pass.

- [x] **Step 3: Route `ApplyFetch` through loop/effect**

Loop validates request, emits durable apply/truncate effects, and applies result event only if fencing matches.

- [x] **Step 4: Ensure snapshot path uses `InstallSnapshotAtomically`**

Snapshot install must update snapshot payload, checkpoint, log start, and epoch history consistently. Prefer a real `ChannelStore`-backed combined durable operation; if a split-store fallback remains for tests or non-production adapters, recovery must detect partial snapshot/checkpoint/history publication and reject or repair it before the loop starts.

- [x] **Step 5: Run apply/snapshot tests**

Run: `go test ./pkg/channel/replica -run 'TestApplyFetch|TestInstallSnapshot|TestNewReplica' -count=1`

Expected: PASS.

Task 14 note: follower `ApplyFetch` now validates through the loop and emits fenced durable effects for truncate, follower batch apply, and heartbeat checkpoint storage. Durable results return to the loop with effect ID, channel key, epoch, leader, and role-generation fences before state, HW/checkpoint, LEO, `CommitReady`, and epoch history are published. Snapshot install continues to use `InstallSnapshotAtomically` with fenced result handling, rejects concurrent pending snapshots, clears stale pre-durable pending slots, and no longer blocks `Close()` while durable snapshot I/O is in flight. Added stale record-apply, stale truncate, snapshot-after-tombstone, snapshot-after-close, concurrent snapshot, and close-during-snapshot regressions; Task 14 spec and code-quality reviews approved after durable-lane serialization/cancellation fixes.

---

### Task 15: Migrate Leader Reconcile And Promotion Shared Rules

**Files:**
- Create: `pkg/channel/replica/reconcile_coordinator.go`
- Modify: `pkg/channel/replica/reconcile.go`
- Modify: `pkg/channel/replica/promotion_evaluator.go`
- Modify: `pkg/channel/replica/lifecycle_test.go`
- Modify: `pkg/channel/replica/recovery_test.go`
- Modify: `pkg/channel/replica/promotion_evaluator_test.go`

- [x] **Step 1: Add missing-proof and stale-result tests**

Add tests proving missing/offline ISR cannot count above HW, reconcile effects are discarded after lease expiry/newer meta/tombstone, and reconcile HW notification fires exactly once. Existing `TestBecomeLeaderReconcileNotifiesHWAdvance` is a characterization test if already present.

- [x] **Step 2: Run RED/characterization mix**

Run: `go test ./pkg/channel/replica -run 'TestBecomeLeader|TestNewReplica|TestPromotionEvaluator|TestRecover' -count=1`

Expected: new missing-proof/stale-result tests fail before migration; existing tests pass.

- [x] **Step 3: Route reconcile through loop/effects**

Leader promotion seeds progress, emits probe effects as needed, applies proofs through shared epoch/quorum helpers, truncates unsafe suffix, stores checkpoint, and marks commit ready.

- [x] **Step 4: Keep promotion evaluator aligned**

Ensure dry-run promotion uses the same match-offset and quorum helpers as runtime reconcile.

- [x] **Step 5: Run reconcile/promotion/recovery tests**

Run: `go test ./pkg/channel/replica -run 'TestBecomeLeader|TestNewReplica|TestPromotionEvaluator|TestRecover' -count=1`

Expected: PASS.

- [x] **Step 6: Update ownership matrix**

Mark `reconcilePending` and reconcile-related `epochHistory` mutation loop-owned.

Task 15 note: leader reconcile now uses shared reconcile quorum/match helpers in `reconcile_coordinator.go`; missing ISR members default to HW and never prove local LEO. `BecomeLeader` validates lease before durable epoch publication, emits a fenced begin-epoch effect when a new epoch point is needed, and only publishes leader state after the durable result returns through the loop. Reconcile proof application fences channel epoch and leader epoch, then durable reconcile truncation/checkpoint publication runs outside the loop through `leaderReconcileDurableEffect` and returns via an effect-id/channel/epoch/leader-epoch/role-generation fenced result. Promotion dry-run uses the same helpers and the durable log-start boundary. Added offline-ISR HW-only, lease-expired reconcile, stale leader-epoch proof, stale durable result, snapshot-boundary promotion, and exactly-once HW notification regressions; targeted replica tests, full replica package tests, and related runtime/transport/app reconcile tests passed. A full replica race run hit the pre-existing `TestAppendQuorumModeWaitsForHWAdvance` timeout once, then the focused race test for that case passed; rerun full race in Task 17/19.

---

### Task 16: Add Real Store Durability Characterization

**Files:**
- Modify: `pkg/channel/store/*.go`
- Create or modify: `pkg/channel/store/*_test.go`
- Create or modify: `pkg/channel/replica/*_test.go` only if package-level fake cannot cover contract

- [x] **Step 1: Add real-store tests where fast enough**

Cover begin-epoch reopen, append reopen, apply-fetch-with-checkpoint-and-epoch reopen, truncate+history consistency, snapshot+checkpoint/history consistency, monotonic checkpoint, and injected partial history/log/snapshot/checkpoint recovery behavior using `pkg/channel/store` test utilities. The implementation must add or verify real `ChannelStore` combined durable operations for begin epoch, apply-fetch+checkpoint+epoch, truncate+history, snapshot+checkpoint/history, and monotonic checkpoint; tests alone are not sufficient if production store paths remain split unsafely.

- [x] **Step 2: Run store/replica durability tests**

Run: `go test ./pkg/channel/store ./pkg/channel/replica -run 'Test.*(Recover|Reopen|Durable|Checkpoint|Snapshot|Truncate|Epoch)' -count=1`

Expected: PASS. If any test is too slow or needs crash process isolation, move it behind `integration` tag and document why.

Task 16 note: `ChannelStore` now has production combined durable operations for begin epoch, truncate+history, monotonic checkpoint, and snapshot+checkpoint/history publication, while `StoreApplyFetchWithEpoch` validates checkpoint monotonicity and writes records/checkpoint/history in one Pebble batch. The replica durable adapter prefers these combined production paths and validates checkpoint regressions before any split-store fallback can mutate log/history/snapshot state. Added real Pebble-backed reopen tests for append, begin epoch, apply-fetch+checkpoint+epoch, truncate+history, snapshot+checkpoint/history, monotonic checkpoint validation, and injected partial-state recovery rejection. Added regression tests for checkpoint coordinator independence and no-mutation-on-checkpoint-regression. Task 16 spec review and code-quality re-review approved; `GOWORK=off go test ./pkg/channel/store ./pkg/channel/replica -run 'Test.*(Recover|Reopen|Durable|Checkpoint|Snapshot|Truncate|Epoch)' -count=1` and package store/replica tests passed.

---

### Task 17: Remove Obsolete Locks, Publishers, And Transitional Paths

**Files:**
- Modify: `pkg/channel/replica/replica.go`
- Modify: `pkg/channel/replica/append.go`
- Modify: `pkg/channel/replica/progress.go`
- Modify: `pkg/channel/replica/pool.go`
- Modify: any transitional files introduced earlier

- [x] **Step 1: Remove obsolete lock ownership**

Remove direct production mutation paths protected by `appendMu`/`advanceMu` once loop owns all mutable state. Keep a small mutex only if it protects external submission/lifecycle and is not a state owner.

- [x] **Step 2: Remove obsolete background publishers**

Remove old append collector, advance publisher, and checkpoint publisher if replaced by loop/effect workers.

- [x] **Step 3: Remove transitional dual paths**

Search for direct mutable ownership of `meta`, `state`, `progress`, `waiters`, `appendPending`, `pendingCheckpoint`, `checkpointQueued`, `checkpointInFlight`, `reconcilePending`, `epochHistory`, and `closed` outside machine/loop-owned files.

Run: `rg -n 'r\.(meta|state|progress|waiters|appendPending|pendingCheckpoint|checkpointQueued|checkpointInFlight|reconcilePending|epochHistory|closed)\b|delete\(r\.progress' pkg/channel/replica`

Expected: direct mutable field ownership is only in an explicit allowlist of loop/machine/durable recovery files. Any remaining usage in legacy files such as `append.go`, `fetch.go`, `progress.go`, `replication.go`, or `reconcile.go` must be read-only snapshot access or removed. Document the allowlist in the task notes, including any remaining `closed` or `pendingCheckpoint` access.

- [x] **Step 4: Run package race detector**

Run: `go test -race ./pkg/channel/replica -count=1`

Expected: PASS.

Task 17 note: removed `appendMu`, old collector/checkpoint publisher names, and the deleted pool/transitional request lifecycle. `append.go`, `progress.go`, `fetch.go`, `replication.go`, and `reconcile.go` are now facade/read-only helpers; append/progress/reconcile mutable ownership lives in `append_pipeline.go`, `progress_pipeline.go`, `reconcile_coordinator.go`, `follower_apply.go`, `snapshot_pipeline.go`, `checkpoint_writer.go`, and `lifecycle_pipeline.go`, with startup-only recovery ownership in `recovery.go` before workers start. Remaining `closed` access is loop-owned lifecycle/fencing (`loop.go`, `lifecycle_pipeline.go`, effect validation in pipeline files). Remaining `pendingCheckpoint`/`checkpointQueued`/`checkpointInFlight` access is checkpoint pipeline ownership in `progress_pipeline.go` scheduling and `checkpoint_writer.go` effect/result handling. Removed unreferenced transitional `appendEpochPointLocked` and `truncateLogToLocked` durable helpers. The direct-field search now only returns the loop/pipeline/recovery allowlist plus `append.go` timeout snapshot logging and `replica.go` atomic snapshot publication. `GOWORK=off go test ./pkg/channel/replica -count=1 -timeout=60s` and `GOWORK=off go test -race ./pkg/channel/replica -count=1 -timeout=180s` passed after the cleanup.

---

### Task 18: Write Final FLOW Documentation

**Files:**
- Create: `pkg/channel/replica/FLOW.md`
- Modify: `pkg/channel/FLOW.md`

- [x] **Step 1: Write final package FLOW**

Create `pkg/channel/replica/FLOW.md` describing actual final code, not future target-only behavior. Include responsibilities, public/optional APIs, state fields, event loop ownership, durable effects, append/fetch/apply/reconcile/recovery/snapshot flows, invariants, error semantics, and troubleshooting.

- [x] **Step 2: Update parent FLOW**

Update the `replica/` row or key flow notes in `pkg/channel/FLOW.md` only where the final implementation changed terminology or behavior.

- [x] **Step 3: Verify docs**

Run: `sed -n '1,260p' pkg/channel/replica/FLOW.md && sed -n '1,100p' pkg/channel/FLOW.md`

Expected: docs match final code and use “single-node cluster” terminology for deployment semantics.

Task 18 note: created `pkg/channel/replica/FLOW.md` with the final package responsibilities, file map, public/optional APIs, state ownership matrix, loop/effect model, append/fetch/apply/reconcile/recovery/snapshot flows, invariants, error semantics, and troubleshooting notes. Updated `pkg/channel/FLOW.md` to replace obsolete replica references (`collectAppendBatch`, `appendEpochPointLocked`, `progress.go:advanceHW`, checkpoint publisher) with the loop-owned append/progress/checkpoint/reconcile implementation. Verified with `sed -n '1,260p' pkg/channel/replica/FLOW.md && sed -n '1,100p' pkg/channel/FLOW.md`.

---

### Task 19: Package And Cross-layer Verification

**Files:**
- Potentially modify: `pkg/channel/runtime/*_test.go`
- Potentially modify: `internal/app/*_test.go`

- [x] **Step 1: Run package stress tests**

Run: `go test ./pkg/channel/replica -count=10`

Expected: PASS.

- [x] **Step 2: Run replica race tests**

Run: `go test -race ./pkg/channel/replica -count=1`

Expected: PASS.

- [x] **Step 3: Run public channel facade tests**

Run: `go test ./pkg/channel -count=1`

Expected: PASS.

- [x] **Step 4: Run related channel subsystem tests**

Run: `go test ./pkg/channel/runtime ./pkg/channel/transport ./pkg/channel/store -count=1`

Expected: PASS.

- [x] **Step 5: Run targeted runtime long-poll/session tests**

Run: `go test ./pkg/channel/runtime -run 'Test.*LongPoll|Test.*Lane|Test.*Session|Test.*ServeFetch|Test.*Leader' -count=1`

Expected: PASS.

- [x] **Step 6: Run app-level smoke tests**

Run: `go test ./internal/app -run 'Test(Channel|ApplyMeta|Leader|Runtime|Replica|Send|Ack|Repair)' -count=1`

Expected: PASS or no matching tests. If failures reveal integration contract changes, fix before proceeding.

- [x] **Step 7: Optional runtime race coverage**

Run if duration is acceptable: `go test -race ./pkg/channel/runtime -run 'Test.*LongPoll|Test.*Lane|Test.*Session' -count=1`

Expected: PASS.

- [x] **Step 8: Final doc check**

Run: `git diff --check`

Expected: no whitespace errors.

Task 19 note: final verification completed with `GOWORK=off`; reran after final review fixes for durable-effect revalidation and same-leader `ApplyMeta` reconcile. Passed: `go test ./pkg/channel/replica -count=10`, `go test -race ./pkg/channel/replica -count=1`, `go test ./pkg/channel -count=1`, `go test ./pkg/channel/runtime ./pkg/channel/transport ./pkg/channel/store -count=1`, `go test ./pkg/channel/runtime -run 'Test.*LongPoll|Test.*Lane|Test.*Session|Test.*ServeFetch|Test.*Leader' -count=1`, `go test ./internal/app -run 'Test(Channel|ApplyMeta|Leader|Runtime|Replica|Send|Ack|Repair)' -count=1`, optional `go test -race ./pkg/channel/runtime -run 'Test.*LongPoll|Test.*Lane|Test.*Session' -count=1`, and `git diff --check`.
