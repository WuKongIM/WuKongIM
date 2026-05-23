# channelv2 Idle Eviction Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add leader-driven idle eviction to `pkg/channelv2` using PullHint wakeups, leader-controlled pull pacing, stopped follower acknowledgements, and safe leader-last runtime removal.

**Architecture:** Keep `service/` as the synchronous facade and lazy metadata activation point, keep `reactor/` as the single-writer owner of per-channel lifecycle, and keep blocking store/RPC work in bounded `worker` pools. Implement this in phases so existing replication behavior stays green while new idle pacing and eviction behavior is added behind conservative defaults.

**Tech Stack:** Go, `pkg/channelv2` reactor/service/transport/worker packages, existing memory store/testkit, `github.com/stretchr/testify/require`, `go test`.

---

## Spec

- Design spec: `docs/superpowers/specs/2026-05-24-channelv2-idle-eviction-design.md`
- Project rules: read `pkg/channelv2/FLOW.md` before modifying package flow; update it when flow changes.
- Required language: key structs/methods and all config fields need English comments.
- Terminology: use “single-node cluster”, not standalone/single-node bypass.

## File Map

- `pkg/channelv2/types.go`: public `Config` additions for idle eviction knobs.
- `pkg/channelv2/transport/types.go`: rename Notify protocol to PullHint; add pull pacing/stop fields and stopped ACK fields.
- `pkg/channelv2/transport/local.go`: in-memory PullHint transport support and drop/counter helpers for tests.
- `pkg/channelv2/worker/task.go`: `TaskRPCPullHint`, `TaskStoreCheckpoint`, task runners.
- `pkg/channelv2/worker/result.go`: checkpoint and PullHint result types.
- `pkg/channelv2/worker/pools.go`: route checkpoint and PullHint tasks to bounded pools.
- `pkg/channelv2/reactor/event.go`: `EventPullHint`, worker result kind coverage, possible lifecycle event kinds.
- `pkg/channelv2/reactor/group.go`: config propagation, default idle values, compatibility entry points.
- `pkg/channelv2/reactor/reactor.go`: runtime lifecycle fields, event handling, safe runtime deletion.
- `pkg/channelv2/reactor/lifecycle.go` (new): leader-side per-follower lifecycle/progress state and PullHint retry helpers.
- `pkg/channelv2/reactor/replication_state.go`: follower pull pacing, parking, stopping, stopped ACK retry state.
- `pkg/channelv2/reactor/replication_runtime.go`: PullHint handling, PullResponse pacing, follower stop flow, leader ACK/stop handling.
- `pkg/channelv2/reactor/effect.go`: submit PullHint/checkpoint tasks, activity version after append store, safe eviction helpers.
- `pkg/channelv2/reactor/metrics.go`: observer hooks for lifecycle/PullHint events.
- `pkg/channelv2/reactor/scheduler.go` (new): per-reactor due scheduler/min-heap for append flush, replication pull, lifecycle checks.
- `pkg/channelv2/service/service.go`: config propagation and MetaResolver retention.
- `pkg/channelv2/service/replication.go`: `HandlePullHint` service lazy activation and transport server method rename.
- `pkg/channelv2/service/append.go`: reuse lazy metadata helper; no fetch activation.
- `pkg/channelv2/testkit/cluster.go`: update local transport/server registrations and helper names.
- `pkg/channelv2/FLOW.md`: document PullHint, pull pacing, follower stop, leader-last eviction, and scheduler behavior.
- Tests: `pkg/channelv2/transport/local_test.go`, `pkg/channelv2/worker/task_test.go`, `pkg/channelv2/service/service_test.go`, `pkg/channelv2/reactor/replication_state_test.go`, `pkg/channelv2/reactor/group_test.go`, `pkg/channelv2/testkit/cluster_test.go`.

## Implementation Notes

- Use TDD for each task: write or adjust focused tests first, run them to confirm failure, implement minimal code, rerun.
- Keep backward compatibility inside the package during refactor by using temporary aliases only if needed, but final public names in channelv2 should be PullHint.
- Do not introduce bypass logic for single-node cluster. Treat it as a replica set with only the local node.
- Use durable leader LEO as `ActivityVersion`; do not add a process-local counter that resets on eviction.
- Checkpoint writes must go through a worker task, not synchronously in the reactor loop.
- A follower must not delete runtime state until checkpoint succeeds and stopped ACK succeeds.
- Commit after each task. If a worker owns a task, it must list changed files and not revert other workers’ changes.

---

### Task 1: Protocol And Worker Plumbing

**Files:**
- Modify: `pkg/channelv2/transport/types.go`
- Modify: `pkg/channelv2/transport/local.go`
- Modify: `pkg/channelv2/worker/task.go`
- Modify: `pkg/channelv2/worker/result.go`
- Modify: `pkg/channelv2/worker/pools.go`
- Test: `pkg/channelv2/transport/local_test.go`
- Test: `pkg/channelv2/worker/task_test.go`

- [ ] **Step 1: Write failing transport protocol tests**

Add tests that prove local transport routes PullHint and can drop PullHint independently:

```go
func TestLocalNetworkPullHintRoutesToServer(t *testing.T) {
    net := NewLocalNetwork()
    srv := &captureServer{}
    net.Register(2, srv)

    req := PullHintRequest{ChannelKey: ch.ChannelKey("1:a"), ChannelID: ch.ChannelID{ID: "a", Type: 1}, Epoch: 1, LeaderEpoch: 2, Leader: 1, LeaderLEO: 7, ActivityVersion: 7, Reason: PullHintReasonAppend}
    require.NoError(t, net.PullHint(context.Background(), 2, req))
    require.Equal(t, req, srv.pullHint)
}

func TestLocalNetworkDropPullHint(t *testing.T) {
    net := NewLocalNetwork()
    net.Register(2, &captureServer{})
    net.SetDropPullHint(2, true)

    err := net.PullHint(context.Background(), 2, PullHintRequest{ChannelKey: ch.ChannelKey("1:a")})
    require.ErrorIs(t, err, ch.ErrNotReady)
    require.Equal(t, 1, net.DroppedPullHints(2))
}
```

- [ ] **Step 2: Run transport tests to verify failure**

Run: `go test ./pkg/channelv2/transport -run 'TestLocalNetworkPullHint|TestLocalNetworkDropPullHint' -count=1`

Expected: FAIL because `PullHintRequest`, `Client.PullHint`, and local drop helpers do not exist.

- [ ] **Step 3: Add PullHint protocol types and local transport routing**

In `pkg/channelv2/transport/types.go`:

```go
// PullHintReason explains why a leader is asking a follower to pull promptly.
type PullHintReason uint8

const (
    // PullHintReasonAppend means leader append progress should interrupt follower parking.
    PullHintReasonAppend PullHintReason = iota + 1
    // PullHintReasonResume means the leader is retrying or resuming follower pull work.
    PullHintReasonResume
)

// PullHintRequest nudges a follower to pull current leader progress without carrying records.
type PullHintRequest struct {
    ChannelKey      ch.ChannelKey
    ChannelID       ch.ChannelID
    Epoch           uint64
    LeaderEpoch     uint64
    Leader          ch.NodeID
    LeaderLEO       uint64
    ActivityVersion uint64
    Reason          PullHintReason
}
```

Add `PullHint(ctx, node, req)` to `Client` and `HandlePullHint(ctx, req)` to `Server`. Keep temporary `Notify` aliases only if required by intermediate code; remove or mark them as compatibility wrappers by the end of Task 2.

Extend existing types:

```go
// PullControl lets the leader control follower pull lifecycle.
type PullControl uint8

const (
    // PullControlContinue keeps the follower runtime loaded and pulling later.
    PullControlContinue PullControl = iota + 1
    // PullControlStop allows a caught-up follower to checkpoint and unload runtime state.
    PullControlStop
)
```

Add `ActivityVersion`, `NextPullAfter`, and `Control` to `PullResponse`; add `ActivityVersion` and `Stopped` to `AckRequest`.

In `local.go`, rename `DropNotify` to `DropPullHint`, add `SetDropPullHint`, `DroppedPullHints`, and route `PullHint` to `server.HandlePullHint`.

- [ ] **Step 4: Run transport tests to verify pass**

Run: `go test ./pkg/channelv2/transport -count=1`

Expected: PASS.

- [ ] **Step 5: Write failing worker tests for PullHint and checkpoint tasks**

Add tests to `pkg/channelv2/worker/task_test.go`:

```go
func TestTaskRunRPCPullHint(t *testing.T) {
    net := transport.NewLocalNetwork()
    srv := &workerCaptureServer{}
    net.Register(2, srv)

    req := transport.PullHintRequest{ChannelKey: ch.ChannelKey("1:a"), ChannelID: ch.ChannelID{ID: "a", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, LeaderLEO: 3, ActivityVersion: 3, Reason: transport.PullHintReasonAppend}
    task := Task{Kind: TaskRPCPullHint, Fence: ch.Fence{ChannelKey: req.ChannelKey, OpID: 1}, RPCPullHint: &RPCPullHintTask{Node: 2, Request: req}}

    res := task.Run(context.Background(), Deps{Transport: net})
    require.NoError(t, res.Err)
    require.NotNil(t, res.RPCPullHint)
    require.Equal(t, req, srv.pullHint)
}

func TestTaskRunStoreCheckpoint(t *testing.T) {
    factory := store.NewMemoryFactory()
    id := ch.ChannelID{ID: "checkpoint", Type: 1}
    key := ch.ChannelKeyForID(id)
    task := Task{Kind: TaskStoreCheckpoint, Fence: ch.Fence{ChannelKey: key, OpID: 1}, StoreCheckpoint: &StoreCheckpointTask{ChannelID: id, Checkpoint: ch.Checkpoint{HW: 4}}}

    res := task.Run(context.Background(), Deps{Stores: factory})
    require.NoError(t, res.Err)
    cs, err := factory.ChannelStore(key, id)
    require.NoError(t, err)
    loaded, err := cs.Load(context.Background())
    require.NoError(t, err)
    require.Equal(t, uint64(4), loaded.CheckpointHW)
}
```

- [ ] **Step 6: Run worker tests to verify failure**

Run: `go test ./pkg/channelv2/worker -run 'TestTaskRunRPCPullHint|TestTaskRunStoreCheckpoint' -count=1`

Expected: FAIL because task kinds and payload/result types do not exist.

- [ ] **Step 7: Add worker task/result plumbing**

In `task.go`, add:

```go
TaskStoreCheckpoint
TaskRPCPullHint
```

Add payloads:

```go
// StoreCheckpointTask asks a worker to persist a channel checkpoint before eviction.
type StoreCheckpointTask struct {
    ChannelID  ch.ChannelID
    Checkpoint ch.Checkpoint
}

// RPCPullHintTask asks a remote follower to pull leader progress promptly.
type RPCPullHintTask struct {
    Node    ch.NodeID
    Request transport.PullHintRequest
}
```

Implement `runStoreCheckpoint` using `cs.StoreCheckpoint(ctx, checkpoint)` and `runRPCPullHint` using `deps.Transport.PullHint`.

In `result.go`, add `StoreCheckpoint *StoreCheckpointResult` and `RPCPullHint *RPCPullHintResult` with English comments.

In `pools.go`, route `TaskStoreCheckpoint` to `StoreApply` or `StoreRead`? Use `StoreApply` for follower-side checkpoint and leader eviction checkpoint because it is store mutation but not append. Route `TaskRPCPullHint` to `RPC`.

- [ ] **Step 8: Run worker tests to verify pass**

Run: `go test ./pkg/channelv2/worker -count=1`

Expected: PASS.

- [ ] **Step 9: Commit Task 1**

```bash
git add pkg/channelv2/transport pkg/channelv2/worker
git commit -m "feat(channelv2): add pull hint protocol plumbing"
```

---

### Task 2: Service PullHint Lazy Activation

**Files:**
- Modify: `pkg/channelv2/reactor/event.go`
- Modify: `pkg/channelv2/reactor/group.go`
- Modify: `pkg/channelv2/reactor/reactor.go`
- Modify: `pkg/channelv2/service/replication.go`
- Modify: `pkg/channelv2/service/service.go`
- Modify: `pkg/channelv2/service/append.go`
- Modify: `pkg/channelv2/testkit/cluster.go`
- Test: `pkg/channelv2/service/service_test.go`
- Test: `pkg/channelv2/testkit/cluster_test.go`

- [ ] **Step 1: Write failing service test for PullHint loading an unloaded follower**

Add a test in `service_test.go`:

```go
func TestHandlePullHintLazyLoadsFollowerMeta(t *testing.T) {
    factory := store.NewMemoryFactory()
    meta := ch.Meta{Key: ch.ChannelKey("1:hint-lazy"), ID: ch.ChannelID{ID: "hint-lazy", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1, 2}, ISR: []ch.NodeID{1, 2}, MinISR: 1, Status: ch.StatusActive}
    resolver := &countingMetaResolver{meta: meta}
    clusterAPI, err := New(Config{LocalNode: 2, Store: factory, ReactorCount: 1, MetaResolver: resolver})
    require.NoError(t, err)
    defer clusterAPI.Close()
    svc := clusterAPI.(*cluster)

    err = svc.HandlePullHint(context.Background(), transport.PullHintRequest{ChannelKey: meta.Key, ChannelID: meta.ID, Epoch: 1, LeaderEpoch: 1, Leader: 1, LeaderLEO: 1, ActivityVersion: 1, Reason: transport.PullHintReasonAppend})
    require.NoError(t, err)
    require.Equal(t, int32(1), resolver.calls.Load())

    loaded, err := svc.group.HasChannelState(context.Background(), meta.Key)
    require.NoError(t, err)
    require.True(t, loaded)
}
```

- [ ] **Step 2: Write failing service test for PullHint resolver failure**

Use a resolver that returns `ch.ErrChannelNotFound` or stale meta and assert `HandlePullHint` returns an error and does not load runtime state.

- [ ] **Step 3: Run tests to verify failure**

Run: `go test ./pkg/channelv2/service -run 'TestHandlePullHintLazyLoadsFollowerMeta|TestHandlePullHint' -count=1`

Expected: FAIL because `HandlePullHint` does not exist or does not lazy load.

- [ ] **Step 4: Implement `HandlePullHint` lazy activation**

In `reactor/event.go`, introduce `EventPullHint` in the same task before service compilation. In `reactor/group.go`, treat `EventPullHint` with the same high priority as the old notify path so parked followers are interrupted promptly. In `reactor/reactor.go`, add a temporary handler that validates metadata and marks follower replication dirty; Task 4 will add parking interruption semantics. In `service/replication.go`, rename `HandleNotify` to `HandlePullHint` and route `reactor.EventPullHint`.

Add helper in service package:

```go
func (c *cluster) ensurePullHintChannelState(ctx context.Context, req transport.PullHintRequest) error {
    loaded, err := c.group.HasChannelState(ctx, req.ChannelKey)
    if err != nil {
        return err
    }
    if loaded {
        return nil
    }
    if c.metaResolver == nil {
        return ch.ErrChannelNotFound
    }
    meta, err := c.metaResolver.ResolveChannelMeta(ctx, req.ChannelID)
    if err != nil {
        return err
    }
    if meta.ID == (ch.ChannelID{}) {
        meta.ID = req.ChannelID
    }
    if meta.Key == "" {
        meta.Key = ch.ChannelKeyForID(meta.ID)
    }
    local := c.localNode
    if meta.Key != req.ChannelKey || meta.Epoch != req.Epoch || meta.LeaderEpoch != req.LeaderEpoch || meta.Leader != req.Leader || meta.Leader == local || meta.Status != ch.StatusActive || !metaContainsReplica(meta.Replicas, local) {
        return ch.ErrStaleMeta
    }
    return c.applyMeta(ctx, meta)
}
```

Store `localNode ch.NodeID` on `cluster` during `New` and compare against it. PullHint activation must require `meta.Leader != localNode`; the local leader must not accept a leader-originated PullHint as a follower.

- [ ] **Step 5: Preserve append lazy load helper**

Keep `ensureAppendChannelState` Append-specific. Do not make Fetch lazy-load.

- [ ] **Step 6: Update testkit to register PullHint server method**

Update local network registration and any helper names from Notify to PullHint.

- [ ] **Step 7: Run service and testkit tests**

Run:

```bash
go test ./pkg/channelv2/service -count=1
go test ./pkg/channelv2/testkit -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit Task 2**

```bash
git add pkg/channelv2/reactor pkg/channelv2/service pkg/channelv2/testkit
git commit -m "feat(channelv2): lazy activate followers on pull hint"
```

---

### Task 3: Reactor Lifecycle Config, Activity Version, And PullHint Sending

**Files:**
- Modify: `pkg/channelv2/channel.go`
- Modify: `pkg/channelv2/types.go`
- Modify: `pkg/channelv2/reactor/group.go`
- Modify: `pkg/channelv2/reactor/reactor.go`
- Create: `pkg/channelv2/reactor/lifecycle.go`
- Modify: `pkg/channelv2/reactor/effect.go`
- Modify: `pkg/channelv2/reactor/metrics.go`
- Test: `pkg/channelv2/reactor/append_batch_test.go`
- Test: `pkg/channelv2/reactor/group_test.go`

- [ ] **Step 1: Write failing test for activity version derived from durable LEO**

Add a direct reactor test after local commit-mode append completes:

```go
func TestLeaderActivityVersionTracksDurableLEO(t *testing.T) {
    factory := newCountingStoreFactory()
    meta := testMeta("activity-version", 1, 1)
    meta.Replicas = []ch.NodeID{1, 2}
    meta.ISR = []ch.NodeID{1}
    g := newAppendBatchTestGroup(t, factory, Config{AppendBatchMaxRecords: 1})
    defer g.Close()
    require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))

    future, err := g.Submit(context.Background(), meta.Key, appendEvent(meta, 1, "a"))
    require.NoError(t, err)
    require.NoError(t, awaitFutureResult(t, future).Err)

    rc := g.reactors[g.router.PickIndex(meta.Key)].channels[meta.Key]
    require.Equal(t, rc.state.LEO, rc.lifecycle.ActivityVersion)
    require.NotZero(t, rc.lifecycle.LastAppendAt)
}
```

- [ ] **Step 2: Write failing test for PullHint sent only to parked/stopped/not-started follower**

Use a capturing transport. Set a leader channel with follower progress states: one parked, one active, one stopped. Append and assert PullHint only targets parked/stopped/not-started followers and only once per activity version.

- [ ] **Step 3: Run tests to verify failure**

Run: `go test ./pkg/channelv2/reactor -run 'TestLeaderActivityVersionTracksDurableLEO|TestAppendSendsPullHint' -count=1`

Expected: FAIL due missing lifecycle state and PullHint send behavior.

- [ ] **Step 4: Add idle config fields with English comments**

In public `channelv2.Config` in `pkg/channelv2/channel.go`, service `Config`, reactor `Config`, and `ReactorConfig`, add:

```go
// IdleSlowdownAfter is the idle duration after the last Append before follower pull intervals begin increasing.
IdleSlowdownAfter time.Duration
// IdleEvictAfter is the idle duration after the last Append before a leader may ask caught-up followers to stop.
IdleEvictAfter time.Duration
// IdlePullMinInterval is the shortest no-record follower pull delay returned by a leader.
IdlePullMinInterval time.Duration
// IdlePullMaxInterval is the longest parked follower pull delay returned by a leader.
IdlePullMaxInterval time.Duration
// IdleEvictCheckInterval is the retry interval for lifecycle checks while eviction is blocked.
IdleEvictCheckInterval time.Duration
// PullHintRetryInterval is the retry interval for best-effort PullHint while a follower still needs progress.
PullHintRetryInterval time.Duration
```

Defaults:

```go
IdleSlowdownAfter = 30 * time.Second
IdleEvictAfter = 5 * time.Minute
IdlePullMinInterval = 10 * time.Millisecond
IdlePullMaxInterval = 5 * time.Second
IdleEvictCheckInterval = time.Second
PullHintRetryInterval = time.Second
```

- [ ] **Step 5: Add leader lifecycle and per-follower runtime structs**

Create `pkg/channelv2/reactor/lifecycle.go`:

```go
type lifecyclePhase uint8

const (
    lifecycleHot lifecyclePhase = iota + 1
    lifecycleCooling
    lifecycleStoppingFollowers
    lifecycleEvictingLeader
)

// channelLifecycle tracks leader-owned activity and idle eviction state for one runtime channel.
type channelLifecycle struct {
    LastAppendAt    time.Time
    ActivityVersion uint64
    Phase           lifecyclePhase
}

// followerLifecycle tracks leader-visible follower runtime state that is not part of the pure machine progress.
type followerLifecycle struct {
    Match              uint64
    LastPullAt         time.Time
    NextExpectedPullAt time.Time
    LastHintVersion    uint64
    HintInflight       bool
    HintRetryAt        time.Time
    Parked             bool
    Stopped            bool
    StopAckVersion     uint64
}
```

Add `lifecycle channelLifecycle` and `followers map[ch.NodeID]*followerLifecycle` to `runtimeChannel`. Initialize it from `state.Replicas` when metadata is applied on a leader. Keep `machine.ReplicaProgress.Match` as the commit/HW source of truth, but mirror updates into `followerLifecycle.Match` so PullHint targeting, parking, stopped ACKs, and retry scheduling have a leader-owned place to store state.

In `ensureChannel`, initialize `ActivityVersion = initial.LEO`. Do not count ApplyMeta-only load as Append activity; use `markAppendActivity(rc, now)` only from Append path.

- [ ] **Step 6: Update append completion to set activity version**

In `handleStoreAppendResult`, after `ApplyAppendStored` succeeds and LEO advances, set `rc.lifecycle.ActivityVersion = rc.state.LEO`, update/create `rc.followers` for all non-local replicas, clear `Stopped` for replicas whose `Match < rc.state.LEO`, and call `sendPullHintsForAppend(rc, now)`.

Do not send PullHint before durable append success because `LeaderLEO` and activity version need the stored LEO.

- [ ] **Step 7: Implement PullHint submit helper**

Add:

```go
func (r *Reactor) submitPullHint(ctx context.Context, node ch.NodeID, fence ch.Fence, req transport.PullHintRequest) error
```

Send via `TaskRPCPullHint`. Add coalescing by `LastHintVersion`; set `HintInflight=true` only after submit succeeds. On submit backpressure/error, set `HintRetryAt = now.Add(PullHintRetryInterval)` and schedule lifecycle retry.


- [ ] **Step 8: Handle PullHint worker results and retry state**

In `handleWorkerResult`, add `TaskRPCPullHint`. On success, clear `HintInflight` and leave `LastHintVersion` recorded. On error, clear `HintInflight`, set `HintRetryAt = now.Add(PullHintRetryInterval)`, observe the drop, and schedule lifecycle retry if the follower still needs immediate progress.

- [ ] **Step 9: Add dropped PullHint retry test**

Extend the Task 3 PullHint test with transport drop/backpressure: drop the first PullHint, assert `HintRetryAt` is set and no runtime state is lost; clear the drop and advance lifecycle time, then assert a new PullHint is sent for the same activity version while the follower remains behind.

- [ ] **Step 10: Add observer hooks as no-op-compatible methods**

Avoid breaking existing observers by adding an optional extended interface:

```go
type LifecycleObserver interface {
    ObserveChannelRuntimeLoaded(key ch.ChannelKey)
    ObserveChannelRuntimeEvicted(key ch.ChannelKey, role ch.Role)
    ObservePullHintSent(key ch.ChannelKey, follower ch.NodeID, reason transport.PullHintReason)
    ObservePullHintDropped(key ch.ChannelKey, follower ch.NodeID, err error)
}
```

Call only after type assertion so existing `Observer` implementations still compile.

- [ ] **Step 11: Run reactor tests**

Run: `go test ./pkg/channelv2/reactor -count=1`

Expected: PASS.

- [ ] **Step 12: Commit Task 3**

```bash
git add pkg/channelv2/types.go pkg/channelv2/reactor
git commit -m "feat(channelv2): track leader idle lifecycle"
```

---

### Task 4: PullResponse Pacing And Follower Parking

**Files:**
- Modify: `pkg/channelv2/reactor/replication_state.go`
- Modify: `pkg/channelv2/reactor/replication_runtime.go`
- Modify: `pkg/channelv2/reactor/event.go`
- Test: `pkg/channelv2/reactor/replication_state_test.go`

- [ ] **Step 1: Write failing tests for leader pacing fields**

Add tests around leader `HandlePull` result:

```go
func TestLeaderPullResponsePacesCaughtUpIdleFollower(t *testing.T) {
    factory := store.NewMemoryFactory()
    meta := testMeta("pace-idle", 1, 1)
    meta.Replicas = []ch.NodeID{1, 2}
    meta.ISR = []ch.NodeID{1}
    r := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: factory, MailboxSize: 16, IdleSlowdownAfter: time.Millisecond, IdlePullMinInterval: 10 * time.Millisecond, IdlePullMaxInterval: time.Second})
    require.NoError(t, applyMetaDirect(t, r, meta))
    rc := r.channels[meta.Key]
    rc.state.LEO = 3
    rc.state.HW = 3
    rc.lifecycle.LastAppendAt = time.Now().Add(-time.Hour)
    rc.lifecycle.ActivityVersion = 3
    rc.state.Progress[2] = machine.ReplicaProgress{Match: 3}

    future := NewFuture()
    r.handlePull(Event{Kind: EventPull, Key: meta.Key, Future: future, Context: context.Background(), Pull: transport.PullRequest{ChannelKey: meta.Key, ChannelID: meta.ID, Epoch: 1, LeaderEpoch: 1, Follower: 2, NextOffset: 4, MaxBytes: 1024}, OpID: 10})
    result := awaitFutureResult(t, future)
    require.Equal(t, transport.PullControlContinue, result.Pull.Control)
    require.Equal(t, uint64(3), result.Pull.ActivityVersion)
    require.GreaterOrEqual(t, result.Pull.NextPullAfter, 10*time.Millisecond)
}
```

Use actual helper return types. Include a second test that lagging follower gets records and `NextPullAfter == 0`.

- [ ] **Step 2: Write failing follower parking test**

After an empty PullResponse with `NextPullAfter=time.Hour`, assert the follower does not submit another pull on earlier ticks, then an `EventPullHint` interrupts and submits immediately.

- [ ] **Step 3: Run tests to verify failure**

Run: `go test ./pkg/channelv2/reactor -run 'TestLeaderPullResponsePaces|TestFollowerPullHintInterruptsParked' -count=1`

Expected: FAIL.

- [ ] **Step 4: Add follower pacing fields**

Extend `replicationState`:

```go
// parked records whether the follower is intentionally waiting for the leader-provided pull delay.
parked bool
// nextPullAfter is the leader-provided delay from the latest empty pull response.
nextPullAfter time.Duration
// lastActivityVersion is the latest accepted leader activity version.
lastActivityVersion uint64
```

Reuse existing `nextPullAt` for due time.

- [ ] **Step 5: Set leader PullResponse pacing**

In `handleStoreReadLogResult`, when returning `PullResponse`, populate `ActivityVersion`, `NextPullAfter`, and `Control`. Add helper:

```go
func (r *Reactor) leaderPullDelay(rc *runtimeChannel, now time.Time) time.Duration
```

Rules:
- lagging/read returns records -> `0`.
- before `IdleSlowdownAfter` -> `IdlePullMinInterval`.
- after slowdown -> double or step up toward `IdlePullMaxInterval` based on current leader-side `followerLifecycle.NextExpectedPullAt` or idle age.

When an empty response is returned, update leader-side `followerLifecycle.Parked=true` and `NextExpectedPullAt=now.Add(delay)`.

- [ ] **Step 6: Apply follower PullResponse pacing**

In `handleRPCPullResult`, when no records:
- set `HW = min(LEO, resp.LeaderHW)` as today,
- set `replication.lastActivityVersion = resp.ActivityVersion`,
- set `replication.parked = resp.NextPullAfter > 0`,
- set `nextPullAt = now.Add(resp.NextPullAfter)`.

- [ ] **Step 7: Enhance `EventPullHint` handling**

`EventPullHint` was introduced in Task 2. Extend its handler:
- validate metadata fence,
- ignore stale activity version if lower than `replication.lastActivityVersion`,
- clear `parked`, `nextPullAt`, and mark dirty,
- call `tickReplication(rc, now)`.

- [ ] **Step 8: Run focused and package tests**

Run:

```bash
go test ./pkg/channelv2/reactor -run 'TestLeaderPullResponsePaces|TestFollowerPullHintInterruptsParked|TestFollower' -count=1
go test ./pkg/channelv2/reactor -count=1
```

Expected: PASS.

- [ ] **Step 9: Commit Task 4**

```bash
git add pkg/channelv2/reactor
git commit -m "feat(channelv2): pace follower pulls from leader responses"
```

---

### Task 5: Follower Stop, Checkpoint, And Stopped ACK Retry

**Files:**
- Modify: `pkg/channelv2/reactor/replication_state.go`
- Modify: `pkg/channelv2/reactor/replication_runtime.go`
- Modify: `pkg/channelv2/reactor/effect.go`
- Modify: `pkg/channelv2/worker/task.go` if Task 1 needs adjustments
- Test: `pkg/channelv2/reactor/replication_state_test.go`
- Test: `pkg/channelv2/worker/task_test.go`

- [ ] **Step 1: Write failing follower stop test**

Add a test that feeds a follower an empty `PullResponse{Control: PullControlStop, LeaderLEO: 3, LeaderHW: 3, ActivityVersion: 3}`, with local `LEO/HW=3`, and asserts:
- checkpoint task is submitted,
- stopped ACK is sent after checkpoint result,
- runtime channel remains until ACK success,
- runtime channel is deleted after ACK success.

- [ ] **Step 2: Write failing stopped ACK retry test**

Use capturing transport with `SetDropAck(leader, true)` for first ACK. Assert follower keeps runtime and retry state; after drop clears and a later tick occurs, stopped ACK succeeds and runtime deletes.

- [ ] **Step 3: Run tests to verify failure**

Run: `go test ./pkg/channelv2/reactor -run 'TestFollowerStop|TestStoppedAckRetry' -count=1`

Expected: FAIL.

- [ ] **Step 4: Add stop state fields**

Extend `replicationState`:

```go
// stopping records a follower that has accepted leader stop control but has not unloaded yet.
stopping bool
// stopActivityVersion fences the stop control and stopped ACK.
stopActivityVersion uint64
// checkpointInflight records a pending checkpoint before stopped ACK.
checkpointInflight bool
// checkpointOpID fences the checkpoint worker result.
checkpointOpID ch.OpID
// deleteAfterStoppedAck records that runtime deletion is allowed after stopped ACK success.
deleteAfterStoppedAck bool
```

- [ ] **Step 5: Submit checkpoint on PullControlStop**

In `handleRPCPullResult`, if `resp.Control == PullControlStop`:
- reject/ignore if stale metadata or activity version,
- require `state.LEO >= resp.LeaderLEO` and `state.HW >= resp.LeaderHW`, otherwise continue pulling,
- require no pending pull/apply/ack except the current completed pull,
- submit `TaskStoreCheckpoint` with `HW=state.HW`,
- set stopping state.

- [ ] **Step 6: Handle checkpoint worker result**

In `handleWorkerResult`, add `TaskStoreCheckpoint` branch. On follower checkpoint success, submit stopped ACK:

```go
transport.AckRequest{Stopped: true, MatchOffset: rc.state.LEO, ActivityVersion: rc.replication.stopActivityVersion}
```

On checkpoint failure/backpressure, schedule retry via lifecycle/replication next due time.

- [ ] **Step 7: Reuse ACK retry path for stopped ACK**

Extend ACK state so `pendingAck` can carry a stopped flag and activity version:

```go
pendingAckStopped bool
pendingAckActivityVersion uint64
ackStopped bool
ackActivityVersion uint64
```

When ACK success for stopped ACK returns, delete runtime channel.

- [ ] **Step 8: Delete runtime safely**

Add reactor helper:

```go
func (r *Reactor) evictRuntimeChannel(key ch.ChannelKey, rc *runtimeChannel, reason string) bool
```

It must assert no waiters, no append queue/inflight, no pull waiters, and no replication inflight. It clears cancel indexes, closes store if needed, deletes `r.channels[key]`, and observes eviction.

- [ ] **Step 9: Run focused tests**

Run: `go test ./pkg/channelv2/reactor -run 'TestFollowerStop|TestStoppedAckRetry|TestFollower' -count=1`

Expected: PASS.

- [ ] **Step 10: Commit Task 5**

```bash
git add pkg/channelv2/reactor pkg/channelv2/worker
git commit -m "feat(channelv2): stop followers after checkpointed ack"
```

---

### Task 6: Leader Stop Control And Leader-Last Eviction

**Files:**
- Modify: `pkg/channelv2/reactor/replication_runtime.go`
- Modify: `pkg/channelv2/reactor/effect.go`
- Modify: `pkg/channelv2/reactor/lifecycle.go`
- Modify: `pkg/channelv2/reactor/reactor.go`
- Modify: `pkg/channelv2/reactor/group.go`
- Test: `pkg/channelv2/reactor/replication_state_test.go`
- Test: `pkg/channelv2/testkit/cluster_test.go`

- [ ] **Step 1: Write failing test that leader does not stop lagging non-ISR follower**

Set meta replicas `[1,2,3]`, ISR `[1,2]`, follower 3 match below LEO. Assert a Pull from follower 2 does not get `PullControlStop` even if follower 2 is caught up and channel is idle.

- [ ] **Step 2: Write failing test that leader returns stop only when all followers caught up**

Set all follower progress to `Match == LEO`, channel idle beyond `IdleEvictAfter`, and assert empty Pull response returns `PullControlStop` with current `ActivityVersion`.

- [ ] **Step 3: Write failing test that leader evicts only after all stopped ACKs**

Apply leader meta with two followers. Set caught-up progress, feed stopped ACK for one follower, assert channel remains. Feed stopped ACK for all followers with current activity version, assert leader submits checkpoint and deletes runtime after checkpoint success.

- [ ] **Step 4: Write failing single-node cluster eviction test**

For meta replicas `[1]`, append a message, advance time beyond `IdleEvictAfter`, run lifecycle tick, assert leader checkpoints and deletes runtime without follower ACKs.

- [ ] **Step 5: Write failing stale leader checkpoint completion test**

Set up a leader eligible for eviction, start leader checkpoint, then accept a new Append before the checkpoint worker result returns. Complete the old checkpoint result and assert the runtime channel is not deleted because the checkpoint activity version is stale.

- [ ] **Step 6: Run tests to verify failure**

Run: `go test ./pkg/channelv2/reactor -run 'TestLeader.*Evict|TestLeader.*Stop|TestSingleNodeCluster.*Evict' -count=1`

Expected: FAIL.

- [ ] **Step 7: Add leader stop eligibility helpers**

Implement helpers:

```go
func (r *Reactor) leaderCanOfferStop(rc *runtimeChannel, now time.Time) bool
func (r *Reactor) allFollowersCaughtUp(rc *runtimeChannel) bool
func (r *Reactor) allFollowersStopped(rc *runtimeChannel) bool
func (r *Reactor) hasPendingRuntimeWork(rc *runtimeChannel) bool
```

Use all `state.Replicas` except local node, not just ISR. Read stopped/parked/hint retry data from `rc.followers`, not from follower-local `replicationState`.

- [ ] **Step 8: Return PullControlStop from leader pull path**

In leader empty Pull handling, if `leaderCanOfferStop` and follower is caught up, return `Control=PullControlStop`. Mark that follower as stop-offered for current activity version if a field is needed to inspect/debug.

- [ ] **Step 9: Handle stopped ACK on leader**

In `handleAck`, when `event.Ack.Stopped`:
- validate role/fence/follower as today,
- require `Ack.ActivityVersion == rc.lifecycle.ActivityVersion`,
- require `Ack.MatchOffset >= rc.state.LEO`,
- update `rc.followers[ack.Follower]` with `Stopped=true`, `StopAckVersion=Ack.ActivityVersion`, `Parked=false`, `HintInflight=false`, and `Match=max(Match, Ack.MatchOffset)`,
- call `tryEvictLeader(rc, now)`.

Do not advance HW from stale stopped ACK.

- [ ] **Step 10: Add fenced leader checkpoint and delete**

Add leader-side checkpoint fields to `channelLifecycle` or a dedicated leader eviction struct:

```go
// CheckpointInflight records a leader eviction checkpoint that must complete before runtime deletion.
CheckpointInflight bool
// CheckpointOpID fences the leader eviction checkpoint worker result.
CheckpointOpID ch.OpID
// CheckpointActivityVersion is the activity version that requested the checkpoint.
CheckpointActivityVersion uint64
// CheckpointRetryAt is the next time to retry a failed or backpressured leader checkpoint.
CheckpointRetryAt time.Time
```

When all followers are stopped or this is a single-node cluster, require `state.HW >= state.LEO`, require no existing leader checkpoint in flight, allocate a new op id, record `CheckpointActivityVersion=rc.lifecycle.ActivityVersion`, and submit `TaskStoreCheckpoint` with `HW=state.LEO` because the whole log is committed.

On checkpoint completion, evict only if all fences still match: channel key, generation, epoch, leader epoch, checkpoint op id, `CheckpointActivityVersion == rc.lifecycle.ActivityVersion`, leader role, and `state.HW >= state.LEO`. On checkpoint failure or worker-pool backpressure, clear the inflight flag, set `CheckpointRetryAt = now.Add(IdleEvictCheckInterval)`, keep the channel loaded, and schedule lifecycle retry. This prevents duplicate checkpoint submissions and prevents stale checkpoint completions from deleting a hot channel after a new Append.

- [ ] **Step 11: New Append cancels leader eviction**

Ensure `handleAppend` or append-store success clears stopping/evicting phase and follower stopped state for replicas behind the new LEO.

- [ ] **Step 12: Add testkit reactivation test**

Add a three-node testkit scenario where followers stop/unload, a new Append arrives on the leader, PullHint lazy-loads the stopped follower through resolver-backed nodes, and the new append commits. Ensure the harness wires a `MetaResolver` for each follower node; otherwise PullHint lazy activation should fail by design. This covers end-to-end reactivation after follower eviction.

- [ ] **Step 13: Run focused tests and testkit**

Run:

```bash
go test ./pkg/channelv2/reactor -run 'TestLeader.*Evict|TestLeader.*Stop|TestSingleNodeCluster.*Evict|TestAck' -count=1
go test ./pkg/channelv2/testkit -count=1
```

Expected: PASS.

- [ ] **Step 14: Commit Task 6**

```bash
git add pkg/channelv2/reactor pkg/channelv2/testkit
git commit -m "feat(channelv2): evict leaders after followers stop"
```

---

### Task 7: Due Scheduler To Avoid Full Idle Scans

**Files:**
- Create: `pkg/channelv2/reactor/scheduler.go`
- Modify: `pkg/channelv2/reactor/reactor.go`
- Modify: `pkg/channelv2/reactor/effect.go`
- Modify: `pkg/channelv2/reactor/replication_runtime.go`
- Test: `pkg/channelv2/reactor/group_test.go`
- Test: `pkg/channelv2/reactor/scheduler_test.go`

- [ ] **Step 1: Write scheduler unit tests**

Create tests for a min-heap scheduler:

```go
func TestDueSchedulerPopsDueItemsInOrder(t *testing.T) {
    var s dueScheduler
    now := time.Unix(10, 0)
    s.push(dueItem{key: ch.ChannelKey("b"), kind: dueReplication, due: now.Add(2 * time.Second), version: 1})
    s.push(dueItem{key: ch.ChannelKey("a"), kind: dueAppendFlush, due: now.Add(time.Second), version: 1})

    item, ok := s.popDue(now.Add(time.Second))
    require.True(t, ok)
    require.Equal(t, ch.ChannelKey("a"), item.key)
    _, ok = s.popDue(now.Add(time.Second))
    require.False(t, ok)
}

func TestDueSchedulerNextWait(t *testing.T) {
    var s dueScheduler
    now := time.Unix(10, 0)
    s.push(dueItem{key: ch.ChannelKey("a"), kind: dueAppendFlush, due: now.Add(2 * time.Second), version: 1})
    require.Equal(t, 2*time.Second, s.nextWait(now))
    require.Equal(t, time.Duration(0), s.nextWait(now.Add(3*time.Second)))
}
```

Keep scheduler generic over `(kind, key, due, version)`.

- [ ] **Step 2: Run scheduler tests to verify failure**

Run: `go test ./pkg/channelv2/reactor -run 'TestDueScheduler' -count=1`

Expected: FAIL because scheduler does not exist.

- [ ] **Step 3: Implement scheduler**

Create `scheduler.go`:

```go
type dueKind uint8

const (
    dueAppendFlush dueKind = iota + 1
    dueReplication
    dueLifecycle
)

type dueItem struct {
    key     ch.ChannelKey
    kind    dueKind
    due     time.Time
    version uint64
    index   int
}

type dueScheduler struct { items []dueItem }
```

Implement `push`, `popDue(now)`, `nextWait(now)`, using `container/heap`. Stale entries are ignored by callers after comparing current runtime state.

- [ ] **Step 4: Integrate append flush due scheduling**

When append queue receives its first pending item or restore-front after backpressure, schedule `dueAppendFlush` at `appendQ.flushDue` or retry time. Replace `flushDueAppends(now)` full map scan on idle with popping due append items.

- [ ] **Step 5: Integrate follower replication due scheduling**

Whenever follower `nextPullAt`, ACK retry, apply retry, checkpoint retry, or stopped ACK retry changes, schedule `dueReplication` for that channel. Replace `tickAllReplication(now)` full map scan with due item processing. Keep explicit `Group.Tick` as a compatibility maintenance nudge, but it should schedule/check due work instead of always scanning every loaded channel.

- [ ] **Step 6: Integrate lifecycle due scheduling**

Schedule lifecycle checks after Append, after PullHint result failures, and after eviction-blocking conditions. Use `IdleSlowdownAfter`, `IdleEvictAfter`, `IdleEvictCheckInterval`, and `PullHintRetryInterval`. Lifecycle due processing must call a helper such as `retryDuePullHints(rc, now)` before eviction checks.

- [ ] **Step 7: Write non-scan behavior test**

Add a test with many loaded idle leader channels and one due channel. Use a test observer or reactor hook to assert only the due channel attempts replication/lifecycle work. If hook instrumentation is too invasive, assert via transport call counts that no PullHint/checkpoint work is emitted for non-due channels.

- [ ] **Step 8: Run reactor tests**

Run: `go test ./pkg/channelv2/reactor -count=1`

Expected: PASS.

- [ ] **Step 9: Commit Task 7**

```bash
git add pkg/channelv2/reactor
git commit -m "feat(channelv2): schedule channelv2 maintenance by due time"
```

---

### Task 8: Observability, Docs, And Compatibility Cleanup

**Files:**
- Modify: `pkg/channelv2/reactor/metrics.go`
- Modify: `pkg/channelv2/FLOW.md`
- Modify: `pkg/channelv2/doc.go` if package summary needs lifecycle mention
- Modify: tests that still use Notify names
- Test: `pkg/channelv2/reactor/group_test.go`

- [ ] **Step 1: Write or update observer tests**

Add tests that observer sees:
- PullHint sent/dropped,
- follower stopped,
- channel runtime evicted.

- [ ] **Step 2: Run observer tests to verify failure if hooks missing**

Run: `go test ./pkg/channelv2/reactor -run 'TestObserver.*PullHint|TestObserver.*Evict|TestObserver.*Stopped' -count=1`

Expected: FAIL until hooks are wired.

- [ ] **Step 3: Wire optional observer hooks**

Use optional interfaces to avoid breaking existing observer implementations. Add small helper methods in `metrics.go` such as `observePullHintSent`, `observeFollowerStopped`, `observeRuntimeEvicted`.

- [ ] **Step 4: Remove stale Notify names**

Run: `rg -n 'Notify|notify' pkg/channelv2`

Update remaining channelv2 replication notification names to PullHint unless the name belongs to unrelated comments in historical docs. Do not leave mixed terminology in active code.

- [ ] **Step 5: Update `FLOW.md`**

Document:
- PullHint replaces notify semantics,
- leader returns `NextPullAfter` and `PullControlStop`,
- follower checkpoint + stopped ACK before local eviction,
- leader waits for all followers then checkpoints and evicts,
- due scheduler replaces broad idle scans.

- [ ] **Step 6: Run docs grep**

Run: `rg -n 'standalone|single node|Notify|notify' pkg/channelv2 docs/superpowers/specs/2026-05-24-channelv2-idle-eviction-design.md pkg/channelv2/FLOW.md`

Expected: no incorrect deployment wording; no active channelv2 Notify terminology except compatibility notes if any.

- [ ] **Step 7: Run channelv2 tests**

Run: `go test ./pkg/channelv2/... -count=1`

Expected: PASS.

- [ ] **Step 8: Commit Task 8**

```bash
git add pkg/channelv2 docs/superpowers/specs/2026-05-24-channelv2-idle-eviction-design.md
git commit -m "docs(channelv2): document idle eviction flow"
```

---

### Task 9: Final Verification

**Files:**
- No planned source changes unless verification finds a defect.

- [ ] **Step 1: Run targeted package tests**

Run: `go test ./pkg/channelv2/... -count=1`

Expected: PASS.

- [ ] **Step 2: Run broader related tests**

Run: `go test ./internal/... ./pkg/... -count=1`

Expected: PASS. If this is too slow, run at least `go test ./pkg/channelv2/... ./pkg/channel/... ./internal/app/... -count=1` and record skipped scope.

- [ ] **Step 3: Inspect git status**

Run: `git status --short`

Expected: clean or only intentional uncommitted notes. Do not revert unrelated user changes.

- [ ] **Step 4: Request final code review**

Use `superpowers:requesting-code-review` with a concise summary and ask a reviewer to focus on concurrency races, stale fences, and eviction safety.

- [ ] **Step 5: Address review findings**

Apply accepted fixes one at a time with focused tests.

- [ ] **Step 6: Final commit if needed**

```bash
git add <changed-files>
git commit -m "fix(channelv2): address idle eviction review"
```

