# ChannelV2 Lifecycle Simplification Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Refactor `pkg/channelv2` runtime lifecycle handling into an explicit, testable lifecycle model without changing channelv2 behavior.

**Architecture:** Keep the reactor as the single writer and keep worker pools for blocking effects. Add a pure lifecycle model that consumes `RuntimeView` snapshots and returns lifecycle decisions/actions; the reactor builds snapshots and executes actions. Ordinary append/fetch and pull/apply/ack replication semantics remain in `machine.ChannelState`, `appendQueue`, and `replicationState`.

**Tech Stack:** Go, package `pkg/channelv2/reactor`, existing channelv2 worker/store/transport contracts, `testify/require`, `go test`.

---

## References

- Spec: `docs/superpowers/specs/2026-05-24-channelv2-lifecycle-simplification-design.md`
- Existing flow doc: `pkg/channelv2/FLOW.md`
- Existing lifecycle code: `pkg/channelv2/reactor/lifecycle.go`
- Existing replication runtime code: `pkg/channelv2/reactor/replication_runtime.go`
- Existing reactor event handlers: `pkg/channelv2/reactor/reactor.go`
- Existing scheduler: `pkg/channelv2/reactor/scheduler.go`
- Existing lifecycle-heavy tests: `pkg/channelv2/reactor/replication_state_test.go`, `pkg/channelv2/reactor/append_batch_test.go`, `pkg/channelv2/reactor/scheduler_test.go`, `pkg/channelv2/reactor/group_test.go`

## Repository Constraints

- Follow `AGENTS.md` terminology: use "single-node cluster" in docs/tests when describing deployment shape.
- If `pkg/channelv2/FLOW.md` has unrelated pre-existing edits, do not overwrite them. Read and merge carefully before the final doc update.
- Add English comments for key new types and fields.
- Keep tests fast. Do not use `-tags=integration` for this refactor.
- Use TDD for each behavior slice: failing test, minimal implementation, passing test, commit.

## File Structure

Create:

- `pkg/channelv2/reactor/lifecycle_model.go`
  - Pure lifecycle phases, events, actions, decisions, and view structs.
  - Guard methods such as `AllFollowersCaughtUp`, `AllFollowersStopped`, `CanOfferFollowerStop`, and `SafeToEvict`.
- `pkg/channelv2/reactor/lifecycle_leader.go`
  - Pure leader lifecycle transition functions.
  - No store, transport, mailbox, or future calls.
- `pkg/channelv2/reactor/lifecycle_follower.go`
  - Pure follower stop lifecycle transition functions.
  - Ordinary pull/apply/ack retry remains in `replication_runtime.go`.
- `pkg/channelv2/reactor/lifecycle_apply.go`
  - Reactor-side action execution helpers.
  - Calls existing submit checkpoint, submit ACK, submit pull hint, queue final recheck, and evict helpers.
- `pkg/channelv2/reactor/runtime_snapshot.go`
  - Converts `runtimeChannel` into `RuntimeView` and compatibility helpers.
- `pkg/channelv2/reactor/lifecycle_model_test.go`
  - Pure guard and phase-transition tests.

Modify:

- `pkg/channelv2/reactor/reactor.go`
  - Initialize `runtimeLifecycle` in `ensureChannel`.
  - Replace scattered lifecycle mutation at event boundaries with calls into lifecycle decisions where planned.
- `pkg/channelv2/reactor/lifecycle.go`
  - Gradually reduce to follower tracking helpers and wrappers, or delete after all logic moves.
- `pkg/channelv2/reactor/replication_runtime.go`
  - Delegate follower stop/checkpoint/stopped-ACK and leader pull stop decisions to lifecycle model/actions.
- `pkg/channelv2/reactor/scheduler.go`
  - Use decision `NextDue`/snapshot guards rather than re-encoding lifecycle semantics.
- `pkg/channelv2/reactor/replication_state_test.go`
  - Adjust direct field references from old lifecycle phase/checkpoint fields to new lifecycle fields.
- `pkg/channelv2/reactor/append_batch_test.go`
  - Adjust activity/version field references if names move.
- `pkg/channelv2/reactor/group_test.go`
  - Adjust observer lifecycle expectations if helper names move.
- `pkg/channelv2/FLOW.md`
  - Update after implementation to describe explicit lifecycle model.

---

### Task 0: Baseline And Worktree Safety

**Files:**
- Read: `git status --short`
- Read: `pkg/channelv2/FLOW.md`
- No code changes

- [ ] **Step 1: Check current tree state**

Run:

```bash
git status --short
```

Expected: note any pre-existing modified files. At the time this plan was written, `pkg/channelv2/FLOW.md` was already modified. Do not discard or overwrite user changes.

- [ ] **Step 2: Run targeted baseline tests**

Run:

```bash
go test ./pkg/channelv2/... -count=1
```

Expected: PASS before refactor. If it fails, stop and diagnose before editing.

- [ ] **Step 3: Commit nothing**

This is a safety checkpoint only. Do not commit.

---

### Task 1: Add Pure Lifecycle Model And Guard Tests

**Files:**
- Create: `pkg/channelv2/reactor/lifecycle_model.go`
- Create: `pkg/channelv2/reactor/lifecycle_model_test.go`

- [ ] **Step 1: Write failing guard tests**

Add tests similar to:

```go
func TestLifecycleViewCanOfferFollowerStopRequiresIdleCaughtUpAndNoWork(t *testing.T) {
    now := time.Unix(100, 0)
    view := RuntimeView{
        Role:            ch.RoleLeader,
        Status:          ch.StatusActive,
        LEO:             3,
        HW:              3,
        ActivityVersion: 3,
        IdleSince:       now.Add(-time.Minute),
        Followers: []FollowerView{
            {Node: 2, Match: 3},
            {Node: 3, Match: 3},
        },
    }

    require.True(t, view.CanOfferFollowerStop(now, time.Second))

    view.PendingWork.Waiters = 1
    require.False(t, view.CanOfferFollowerStop(now, time.Second))

    view.PendingWork = PendingWorkView{}
    view.Followers[1].Match = 2
    require.False(t, view.CanOfferFollowerStop(now, time.Second))
}

func TestLifecycleViewSafeToEvictRejectsPendingWork(t *testing.T) {
    view := RuntimeView{}
    require.True(t, view.SafeToEvict())

    view.PendingWork.AppendInflight = true
    require.False(t, view.SafeToEvict())
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
go test ./pkg/channelv2/reactor -run 'TestLifecycleView' -count=1
```

Expected: FAIL because `RuntimeView`, `FollowerView`, and `PendingWorkView` do not exist.

- [ ] **Step 3: Implement minimal lifecycle model types and guards**

Create `lifecycle_model.go` with this structure:

```go
package reactor

import (
    "time"

    ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
)

// LeaderLifecyclePhase identifies the leader runtime eviction phase.
type LeaderLifecyclePhase uint8

const (
    LeaderLifecycleServing LeaderLifecyclePhase = iota + 1
    LeaderLifecycleStoppingFollowers
    LeaderLifecycleCheckpointing
    LeaderLifecycleFinalRecheck
)

// FollowerLifecyclePhase identifies the follower stop-and-evict phase.
type FollowerLifecyclePhase uint8

const (
    FollowerLifecycleReplicating FollowerLifecyclePhase = iota + 1
    FollowerLifecycleStopCheckpointing
    FollowerLifecycleStopAcking
)

// runtimeLifecycle owns runtime eviction state that is separate from log progress.
type runtimeLifecycle struct {
    LeaderPhase   LeaderLifecyclePhase
    FollowerPhase FollowerLifecyclePhase
}

// RuntimeView is an immutable snapshot used by pure lifecycle guards.
type RuntimeView struct {
    Key             ch.ChannelKey
    Role            ch.Role
    Status          ch.Status
    LEO             uint64
    HW              uint64
    ActivityVersion uint64
    IdleSince       time.Time
    PendingWork     PendingWorkView
    Followers       []FollowerView
    AppendFence     AppendFenceView
}

// FollowerView is the leader-visible lifecycle state for one follower.
type FollowerView struct {
    Node           ch.NodeID
    Match          uint64
    Stopped        bool
    StopAckVersion uint64
    StopOffered    bool
}

// PendingWorkView summarizes transient runtime work that blocks eviction.
type PendingWorkView struct {
    Waiters              int
    FetchWaiters         int
    PullWaiters          int
    AppendQueued         int
    AppendQueueBlocked   bool
    AppendInflight       bool
    AppendStoreBlocked   bool
    AppendRetryScheduled bool
    AppendCancelContexts int
    AppendTimings        int
    MachineAppendPending bool
    PullInflight         bool
    AckInflight          bool
    PendingAck           bool
    PendingPull          bool
    ApplyBlocked         bool
    ApplyInflight        bool
    CheckpointInflight   bool
    CheckpointRetry      bool
    AckRetry             bool
    LifecycleCheckpoint  bool
    LifecycleRetry       bool
}

// AppendFenceView summarizes append submission state used by final recheck.
type AppendFenceView struct {
    Reservations uint64
    SubmitSeq    uint64
}
```

Implement methods:

```go
func (v RuntimeView) AllFollowersCaughtUp() bool
func (v RuntimeView) AllFollowersStopped() bool
func (v RuntimeView) IdleExpired(now time.Time, idleAfter time.Duration) bool
func (v RuntimeView) CanOfferFollowerStop(now time.Time, idleAfter time.Duration) bool
func (v RuntimeView) HasPendingWork() bool
func (v RuntimeView) SafeToEvict() bool
```

Preserve existing semantics from `leaderCanOfferStop`, `allFollowersCaughtUp`, `allFollowersStopped`, `hasPendingRuntimeWork`, and `safeToEvictRuntime`.

- [ ] **Step 4: Run tests to verify they pass**

Run:

```bash
go test ./pkg/channelv2/reactor -run 'TestLifecycleView' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/channelv2/reactor/lifecycle_model.go pkg/channelv2/reactor/lifecycle_model_test.go
git commit -m "refactor(channelv2): add runtime lifecycle model"
```

---

### Task 2: Add Runtime Snapshot And Compatibility Wrappers

**Files:**
- Create: `pkg/channelv2/reactor/runtime_snapshot.go`
- Modify: `pkg/channelv2/reactor/lifecycle.go`
- Modify: `pkg/channelv2/reactor/effect.go`
- Test: `pkg/channelv2/reactor/lifecycle_model_test.go`

- [ ] **Step 1: Write failing snapshot tests**

Add tests that construct a `runtimeChannel` and assert snapshot fields capture all blockers:

```go
func TestRuntimeViewFromChannelCapturesPendingWork(t *testing.T) {
    rc := &runtimeChannel{
        waiters:               map[ch.OpID]*Future{1: NewFuture()},
        fetchWaiters:          map[ch.OpID]struct{}{},
        pullWaiters:           map[ch.OpID]*pullWaiter{},
        appendCancelContexts:  map[ch.OpID]context.Context{},
        appendTimings:         map[ch.OpID]appendTiming{},
        lifecycle:             channelLifecycle{CheckpointInflight: true},
    }
    view := runtimeViewFromChannel(rc, time.Now(), AppendFenceView{})
    require.Equal(t, 1, view.PendingWork.Waiters)
    require.True(t, view.PendingWork.LifecycleCheckpoint)
    require.True(t, view.HasPendingWork())
}
```

Import `context` if needed.

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
go test ./pkg/channelv2/reactor -run 'TestRuntimeViewFromChannel' -count=1
```

Expected: FAIL because `runtimeViewFromChannel` does not exist.

- [ ] **Step 3: Implement snapshot construction**

Create `runtime_snapshot.go`:

```go
package reactor

import "time"

func runtimeViewFromChannel(rc *runtimeChannel, now time.Time, appendFence AppendFenceView) RuntimeView {
    if rc == nil || rc.state == nil {
        return RuntimeView{AppendFence: appendFence}
    }
    view := RuntimeView{
        Key:             rc.state.Key,
        Role:            rc.state.Role,
        Status:          rc.state.Status,
        LEO:             rc.state.LEO,
        HW:              rc.state.HW,
        ActivityVersion: rc.lifecycle.ActivityVersion,
        IdleSince:       leaderIdleSince(rc),
        AppendFence:     appendFence,
        PendingWork:     pendingWorkViewFromChannel(rc),
    }
    for node, follower := range rc.followers {
        if follower == nil {
            continue
        }
        view.Followers = append(view.Followers, FollowerView{
            Node:           node,
            Match:          follower.Match,
            Stopped:        follower.Stopped,
            StopAckVersion: follower.StopAckVersion,
            StopOffered:    follower.StopOffered,
        })
    }
    return view
}
```

Implement `pendingWorkViewFromChannel` by mirroring every current condition in `hasPendingRuntimeWork` and `safeToEvictRuntime`.

- [ ] **Step 4: Convert compatibility wrappers**

Update existing functions to call the view, without changing call sites yet:

```go
func (r *Reactor) leaderCanOfferStop(rc *runtimeChannel, now time.Time) bool {
    return runtimeViewFromChannel(rc, now, AppendFenceView{}).CanOfferFollowerStop(now, r.cfg.IdleEvictAfter)
}

func (r *Reactor) allFollowersCaughtUp(rc *runtimeChannel) bool {
    r.syncLeaderFollowers(rc)
    return runtimeViewFromChannel(rc, time.Now(), AppendFenceView{}).AllFollowersCaughtUp()
}

func (r *Reactor) allFollowersStopped(rc *runtimeChannel) bool {
    r.syncLeaderFollowers(rc)
    return runtimeViewFromChannel(rc, time.Now(), AppendFenceView{}).AllFollowersStopped()
}

func (r *Reactor) hasPendingRuntimeWork(rc *runtimeChannel) bool {
    return runtimeViewFromChannel(rc, time.Now(), AppendFenceView{}).HasPendingWork()
}

func (rc *runtimeChannel) safeToEvictRuntime() bool {
    return runtimeViewFromChannel(rc, time.Now(), AppendFenceView{}).SafeToEvict()
}
```

If wrapper placement causes import cycles or duplicate names, keep wrappers in their current files and move only common logic to `runtime_snapshot.go`.

- [ ] **Step 5: Run focused tests**

Run:

```bash
go test ./pkg/channelv2/reactor -run 'TestRuntimeViewFromChannel|TestLeader|TestFollowerStop|TestAppendSendsPullHint|TestObserver|TestScheduler' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/channelv2/reactor/runtime_snapshot.go pkg/channelv2/reactor/lifecycle_model_test.go pkg/channelv2/reactor/lifecycle.go pkg/channelv2/reactor/effect.go
git commit -m "refactor(channelv2): snapshot runtime lifecycle state"
```

---

### Task 3: Model Leader Lifecycle Decisions

**Files:**
- Create: `pkg/channelv2/reactor/lifecycle_leader.go`
- Modify: `pkg/channelv2/reactor/lifecycle_model.go`
- Test: `pkg/channelv2/reactor/lifecycle_model_test.go`

- [ ] **Step 1: Write failing leader decision tests**

Add pure tests:

```go
func TestLeaderLifecycleTickStartsCheckpointAfterFollowersStopped(t *testing.T) {
    now := time.Unix(100, 0)
    lc := runtimeLifecycle{LeaderPhase: LeaderLifecycleStoppingFollowers}
    view := RuntimeView{
        Role:            ch.RoleLeader,
        Status:          ch.StatusActive,
        LEO:             3,
        HW:              3,
        ActivityVersion: 3,
        IdleSince:       now.Add(-time.Minute),
        Followers: []FollowerView{
            {Node: 2, Match: 3, Stopped: true, StopAckVersion: 3},
        },
    }

    decision := lc.OnLeaderLifecycleEvent(LeaderLifecycleEvent{Kind: LeaderLifecycleTick, Now: now}, view, LifecycleConfig{IdleEvictAfter: time.Second})
    require.Equal(t, LeaderLifecycleCheckpointing, decision.LeaderPhase)
    require.Contains(t, decision.ActionKinds(), LifecycleActionStartLeaderCheckpoint)
}

func TestLeaderLifecycleAppendCancelsFinalRecheck(t *testing.T) {
    lc := runtimeLifecycle{LeaderPhase: LeaderLifecycleFinalRecheck}
    decision := lc.OnLeaderLifecycleEvent(LeaderLifecycleEvent{Kind: LeaderLifecycleAppendAdmitted}, RuntimeView{}, LifecycleConfig{})
    require.Equal(t, LeaderLifecycleServing, decision.LeaderPhase)
    require.Contains(t, decision.ActionKinds(), LifecycleActionResetEviction)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
go test ./pkg/channelv2/reactor -run 'TestLeaderLifecycle' -count=1
```

Expected: FAIL because leader lifecycle events/actions do not exist.

- [ ] **Step 3: Add event, action, config, and decision types**

In `lifecycle_model.go`, add:

```go
// LifecycleConfig carries timing values needed by pure lifecycle decisions.
type LifecycleConfig struct {
    IdleEvictAfter          time.Duration
    IdleEvictCheckInterval  time.Duration
    PullHintRetryInterval   time.Duration
}

type LifecycleActionKind uint8

const (
    LifecycleActionScheduleLifecycle LifecycleActionKind = iota + 1
    LifecycleActionScheduleReplication
    LifecycleActionSendPullHint
    LifecycleActionStartFollowerStopCheckpoint
    LifecycleActionSendStoppedAck
    LifecycleActionStartLeaderCheckpoint
    LifecycleActionQueueLeaderFinalRecheck
    LifecycleActionEvictRuntime
    LifecycleActionResetEviction
    LifecycleActionFailStaleWaiters
)

type LifecycleAction struct {
    Kind LifecycleActionKind
    Due  time.Time
}

type LifecycleDecision struct {
    LeaderPhase   LeaderLifecyclePhase
    FollowerPhase FollowerLifecyclePhase
    Actions       []LifecycleAction
    NextDue       time.Time
}

func (d LifecycleDecision) ActionKinds() []LifecycleActionKind
```

- [ ] **Step 4: Implement leader decision function**

Create `lifecycle_leader.go` with pure transitions:

```go
type LeaderLifecycleEventKind uint8

const (
    LeaderLifecycleTick LeaderLifecycleEventKind = iota + 1
    LeaderLifecycleAppendAdmitted
    LeaderLifecycleAppendStored
    LeaderLifecycleFollowerAcked
    LeaderLifecycleCheckpointDone
    LeaderLifecycleFinalRecheck
    LeaderLifecycleMetaFence
)

type LeaderLifecycleEvent struct {
    Kind              LeaderLifecycleEventKind
    Now               time.Time
    CheckpointErr     error
    ActivityVersion   uint64
    AppendSeqObserved uint64
}

func (lc runtimeLifecycle) OnLeaderLifecycleEvent(event LeaderLifecycleEvent, view RuntimeView, cfg LifecycleConfig) LifecycleDecision
```

Keep the first implementation conservative:

- append or meta fence returns `LeaderLifecycleServing` and `ResetEviction`.
- tick/follower ACK moves to `Checkpointing` and emits `StartLeaderCheckpoint` only when `view.AllFollowersStopped()`, `view.HW >= view.LEO`, and `!view.HasPendingWork()`.
- checkpoint done emits `QueueLeaderFinalRecheck` only if activity version still matches and guards still pass.
- final recheck emits `EvictRuntime` only if `view.SafeToEvict()`, no append reservations, and submit sequence did not change.

- [ ] **Step 5: Run pure leader tests**

Run:

```bash
go test ./pkg/channelv2/reactor -run 'TestLeaderLifecycle' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/channelv2/reactor/lifecycle_model.go pkg/channelv2/reactor/lifecycle_leader.go pkg/channelv2/reactor/lifecycle_model_test.go
git commit -m "refactor(channelv2): model leader runtime lifecycle"
```

---

### Task 4: Execute Leader Lifecycle Actions In Reactor

**Files:**
- Create: `pkg/channelv2/reactor/lifecycle_apply.go`
- Modify: `pkg/channelv2/reactor/reactor.go`
- Modify: `pkg/channelv2/reactor/lifecycle.go`
- Modify: `pkg/channelv2/reactor/replication_runtime.go`
- Modify: `pkg/channelv2/reactor/scheduler.go`
- Test: existing leader lifecycle tests

- [ ] **Step 1: Write a failing integration assertion for action routing**

Add or extend a test in `pkg/channelv2/reactor/replication_state_test.go` to assert that leader checkpoint is started through the new lifecycle path. Use existing helpers around `TestLeaderEvictsOnlyAfterAllFollowersStoppedAck`.

The assertion should check the observable behavior, not internal action names:

```go
require.Equal(t, LeaderLifecycleCheckpointing, rc.lifecycleV2.LeaderPhase)
checkpoint := sink.awaitResultKind(t, worker.TaskStoreCheckpoint)
require.Equal(t, meta.Key, checkpoint.Fence.ChannelKey)
```

Use the actual new field name chosen in Task 1. If the field remains `lifecycle`, assert the new phase field there.

- [ ] **Step 2: Run the specific test to verify it fails**

Run:

```bash
go test ./pkg/channelv2/reactor -run 'TestLeaderEvictsOnlyAfterAllFollowersStoppedAck' -count=1
```

Expected: FAIL because reactor still mutates old lifecycle state directly.

- [ ] **Step 3: Implement leader action application helper**

In `lifecycle_apply.go`, add:

```go
func (r *Reactor) applyLifecycleDecision(rc *runtimeChannel, decision LifecycleDecision, now time.Time) {
    if rc == nil {
        return
    }
    if decision.LeaderPhase != 0 {
        rc.lifecycleV2.LeaderPhase = decision.LeaderPhase
    }
    if decision.FollowerPhase != 0 {
        rc.lifecycleV2.FollowerPhase = decision.FollowerPhase
    }
    for _, action := range decision.Actions {
        r.applyLifecycleAction(rc, action, now)
    }
    if !decision.NextDue.IsZero() {
        r.scheduleLifecycleDue(rc, decision.NextDue)
    }
}
```

If the implementation keeps the field name `lifecycle`, use named subfields rather than introducing `lifecycleV2`. The plan prefers a distinct field during migration only if that reduces risk.

Implement `applyLifecycleAction` by delegating to existing helpers:

- `LifecycleActionStartLeaderCheckpoint` -> existing leader checkpoint submission code currently in `tryEvictLeader`.
- `LifecycleActionQueueLeaderFinalRecheck` -> `submitLeaderEvictReady`.
- `LifecycleActionEvictRuntime` -> `evictRuntimeChannel` with append fence checks already done by decision.
- `LifecycleActionResetEviction` -> `resetLeaderCheckpointLifecycle` and follower stop reset helpers.
- `LifecycleActionScheduleLifecycle` -> existing scheduler push helper.

Do not duplicate store checkpoint submission logic. Extract the existing code into a helper such as:

```go
func (r *Reactor) startLeaderCheckpoint(rc *runtimeChannel, now time.Time) bool
```

- [ ] **Step 4: Wire leader event sites**

Replace direct lifecycle mutation at these sites with decisions/actions:

- `cancelLeaderEvictionForAppend`: emit `LeaderLifecycleAppendAdmitted`.
- successful leader append in `handleStoreAppendResult`: emit `LeaderLifecycleAppendStored` after LEO/activity update.
- stopped ACK handling in `handleAck`: emit `LeaderLifecycleFollowerAcked` after progress update.
- `handleLeaderCheckpointResult`: emit `LeaderLifecycleCheckpointDone`.
- `handleLeaderEvictReady`: emit `LeaderLifecycleFinalRecheck`.
- `tickLifecycle`: emit `LeaderLifecycleTick`.

Keep wrappers where needed so behavior remains incremental.

- [ ] **Step 5: Run focused leader tests**

Run:

```bash
go test ./pkg/channelv2/reactor -run 'TestLeader|TestAppendSendsPullHint|TestLeaderEvicts|TestLeaderCheckpoint|TestLeaderReadyEvict|TestObserverSeesRuntimeEvicted|TestScheduler' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/channelv2/reactor/lifecycle_apply.go pkg/channelv2/reactor/reactor.go pkg/channelv2/reactor/lifecycle.go pkg/channelv2/reactor/replication_runtime.go pkg/channelv2/reactor/scheduler.go pkg/channelv2/reactor/replication_state_test.go
git commit -m "refactor(channelv2): route leader lifecycle through decisions"
```

---

### Task 5: Model Follower Stop Lifecycle Decisions

**Files:**
- Create: `pkg/channelv2/reactor/lifecycle_follower.go`
- Modify: `pkg/channelv2/reactor/lifecycle_model.go`
- Test: `pkg/channelv2/reactor/lifecycle_model_test.go`

- [ ] **Step 1: Write failing follower lifecycle tests**

Add pure tests:

```go
func TestFollowerLifecycleStopControlStartsCheckpointWhenCaughtUp(t *testing.T) {
    lc := runtimeLifecycle{FollowerPhase: FollowerLifecycleReplicating}
    view := RuntimeView{
        Role:   ch.RoleFollower,
        Status: ch.StatusActive,
        LEO:    3,
        HW:     3,
    }

    decision := lc.OnFollowerLifecycleEvent(FollowerLifecycleEvent{
        Kind:            FollowerLifecycleStopOffered,
        ActivityVersion: 7,
        LeaderLEO:       3,
        LeaderHW:        3,
    }, view, LifecycleConfig{})

    require.Equal(t, FollowerLifecycleStopCheckpointing, decision.FollowerPhase)
    require.Contains(t, decision.ActionKinds(), LifecycleActionStartFollowerStopCheckpoint)
}

func TestFollowerLifecycleRejectsStopWhenBehindLeader(t *testing.T) {
    lc := runtimeLifecycle{FollowerPhase: FollowerLifecycleReplicating}
    view := RuntimeView{Role: ch.RoleFollower, Status: ch.StatusActive, LEO: 2, HW: 2}

    decision := lc.OnFollowerLifecycleEvent(FollowerLifecycleEvent{
        Kind:      FollowerLifecycleStopOffered,
        LeaderLEO: 3,
        LeaderHW:  3,
    }, view, LifecycleConfig{})

    require.Equal(t, FollowerLifecycleReplicating, decision.FollowerPhase)
    require.NotContains(t, decision.ActionKinds(), LifecycleActionStartFollowerStopCheckpoint)
    require.Contains(t, decision.ActionKinds(), LifecycleActionScheduleReplication)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
go test ./pkg/channelv2/reactor -run 'TestFollowerLifecycle' -count=1
```

Expected: FAIL because follower lifecycle event API does not exist.

- [ ] **Step 3: Implement follower transition function**

Create `lifecycle_follower.go`:

```go
type FollowerLifecycleEventKind uint8

const (
    FollowerLifecycleStopOffered FollowerLifecycleEventKind = iota + 1
    FollowerLifecycleStopCheckpointDone
    FollowerLifecycleStoppedAckDone
    FollowerLifecyclePullHintReceived
    FollowerLifecycleMetaFence
)

type FollowerLifecycleEvent struct {
    Kind            FollowerLifecycleEventKind
    Now             time.Time
    LeaderLEO       uint64
    LeaderHW        uint64
    ActivityVersion uint64
    Err             error
}

func (lc runtimeLifecycle) OnFollowerLifecycleEvent(event FollowerLifecycleEvent, view RuntimeView, cfg LifecycleConfig) LifecycleDecision
```

Semantics:

- `StopOffered`: if follower is active, no pending replication work blocks stop, and `view.LEO >= LeaderLEO && view.HW >= LeaderHW`, move to `StopCheckpointing` and emit `StartFollowerStopCheckpoint`; otherwise emit `ScheduleReplication`.
- `StopCheckpointDone`: on success move to `StopAcking` and emit `SendStoppedAck`; on error stay `StopCheckpointing` and schedule retry.
- `StoppedAckDone`: on success emit `EvictRuntime`; on stale metadata emit reset to `Replicating` and schedule replication; on retryable error stay `StopAcking` and schedule retry.
- `PullHintReceived` or `MetaFence`: reset to `Replicating` and emit `ScheduleReplication`/`ResetEviction` as needed.

- [ ] **Step 4: Run pure follower tests**

Run:

```bash
go test ./pkg/channelv2/reactor -run 'TestFollowerLifecycle' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/channelv2/reactor/lifecycle_model.go pkg/channelv2/reactor/lifecycle_follower.go pkg/channelv2/reactor/lifecycle_model_test.go
git commit -m "refactor(channelv2): model follower stop lifecycle"
```

---

### Task 6: Execute Follower Stop Lifecycle In Reactor

**Files:**
- Modify: `pkg/channelv2/reactor/lifecycle_apply.go`
- Modify: `pkg/channelv2/reactor/replication_runtime.go`
- Modify: `pkg/channelv2/reactor/reactor.go`
- Test: `pkg/channelv2/reactor/replication_state_test.go`

- [ ] **Step 1: Write or adjust failing integration tests**

Use existing tests as acceptance coverage:

- `TestFollowerStopCheckpointsThenSendsStoppedAckBeforeEvicting`
- `TestFollowerStopRejectedWhenLocalLEOBelowLeaderLEO`
- `TestFollowerStopRejectedWhenLocalHWBelowLeaderHW`
- `TestStaleStopCompletionDoesNotDeleteAfterNewerPullHint`

Add assertions for the new follower phase where helpful:

```go
require.Equal(t, FollowerLifecycleStopCheckpointing, rc.lifecycleV2.FollowerPhase)
```

- [ ] **Step 2: Run focused tests to verify current gap**

Run:

```bash
go test ./pkg/channelv2/reactor -run 'TestFollowerStop|TestStaleStopCompletion|TestPullHint' -count=1
```

Expected: FAIL until follower action execution is wired, or PASS if compatibility wrappers still cover behavior. If PASS, continue and preserve PASS after rewiring.

- [ ] **Step 3: Wire stop offered handling**

In `handleFollowerStopControl`, replace direct mutation with:

```go
view := runtimeViewFromChannel(rc, now, AppendFenceView{})
decision := rc.lifecycleV2.OnFollowerLifecycleEvent(FollowerLifecycleEvent{
    Kind:            FollowerLifecycleStopOffered,
    Now:             now,
    LeaderLEO:       resp.LeaderLEO,
    LeaderHW:        resp.LeaderHW,
    ActivityVersion: resp.ActivityVersion,
}, view, r.lifecycleConfig())
r.applyLifecycleDecision(rc, decision, now)
```

Preserve current updates to `rc.state.HW`, `rc.replication.stopActivityVersion`, and `deleteAfterStoppedAck` either inside action application or immediately before it. Do not move ordinary pull/apply state into lifecycle.

- [ ] **Step 4: Wire checkpoint result handling**

In `handleStoreCheckpointResult`, for follower stop checkpoint completions, emit `FollowerLifecycleStopCheckpointDone` and let action application call stopped ACK submission.

- [ ] **Step 5: Wire stopped ACK result handling**

In `handleRPCAckResult`, for stopped ACK completions, emit `FollowerLifecycleStoppedAckDone` and let action application evict runtime or schedule retry.

- [ ] **Step 6: Wire pull hint/meta reset**

In `handlePullHint` and metadata fence handling, emit follower lifecycle reset events so stale stop state is cancelled consistently.

- [ ] **Step 7: Run follower lifecycle tests**

Run:

```bash
go test ./pkg/channelv2/reactor -run 'TestFollowerStop|TestStaleStopCompletion|TestPullHint|TestNotify' -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add pkg/channelv2/reactor/lifecycle_apply.go pkg/channelv2/reactor/replication_runtime.go pkg/channelv2/reactor/reactor.go pkg/channelv2/reactor/replication_state_test.go
git commit -m "refactor(channelv2): route follower stop lifecycle through decisions"
```

---

### Task 7: Simplify Scheduler And Remove Misleading Old Phase State

**Files:**
- Modify: `pkg/channelv2/reactor/lifecycle.go`
- Modify: `pkg/channelv2/reactor/scheduler.go`
- Modify: `pkg/channelv2/reactor/reactor.go`
- Modify: tests that reference old lifecycle fields

- [ ] **Step 1: Write failing assertion for derived cooling**

Add a pure test confirming cooling is derived by pull delay, not stored phase:

```go
func TestLeaderPullDelayUsesIdleAgeWithoutCoolingPhase(t *testing.T) {
    r := NewReactor(ReactorConfig{
        LocalNode:                   1,
        Store:                       store.NewMemoryFactory(),
        IdleSlowdownAfter:           time.Second,
        IdlePullMinInterval:         time.Millisecond,
        IdlePullMaxInterval:         8 * time.Millisecond,
        ReplicationIdlePollInterval: time.Millisecond,
    })
    rc := &runtimeChannel{lifecycle: channelLifecycle{LoadedAt: time.Unix(0, 0)}}

    delay := r.leaderPullDelay(rc, time.Unix(3, 0))
    require.GreaterOrEqual(t, delay, 2*time.Millisecond)
}
```

If an equivalent test already exists, adjust it to assert there is no stored `lifecycleCooling` dependency.

- [ ] **Step 2: Run focused test**

Run:

```bash
go test ./pkg/channelv2/reactor -run 'TestLeaderPullDelay|TestScheduler' -count=1
```

Expected: PASS before removal, or FAIL if the new assertion references removed symbols.

- [ ] **Step 3: Remove or alias old phase constants**

Remove unused old phase constants if all callers have migrated:

```go
// remove lifecycleCooling and lifecycleStoppingFollowers when no longer used
```

If tests still need compatibility during migration, keep aliases briefly:

```go
const lifecycleHot = LeaderLifecycleServing
```

Prefer final code with one phase enum source.

- [ ] **Step 4: Simplify scheduler due calculation**

Keep `scheduler.go` responsible only for next due times:

- append flush due
- follower replication due
- lifecycle decision due

Do not let scheduler duplicate stop/evict semantics. It should call a helper such as:

```go
func (r *Reactor) nextLifecycleDue(rc *runtimeChannel, now time.Time) (time.Time, bool)
```

where the helper uses `RuntimeView` and lifecycle decision `NextDue`.

- [ ] **Step 5: Run broad reactor tests**

Run:

```bash
go test ./pkg/channelv2/reactor -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/channelv2/reactor/lifecycle.go pkg/channelv2/reactor/scheduler.go pkg/channelv2/reactor/reactor.go pkg/channelv2/reactor/*_test.go
git commit -m "refactor(channelv2): simplify lifecycle scheduling state"
```

---

### Task 8: Update FLOW.md And Documentation

**Files:**
- Modify: `pkg/channelv2/FLOW.md`
- Modify if needed: `docs/superpowers/specs/2026-05-24-channelv2-lifecycle-simplification-design.md`

- [ ] **Step 1: Inspect existing FLOW.md edits**

Run:

```bash
git status --short pkg/channelv2/FLOW.md
git diff -- pkg/channelv2/FLOW.md
```

Expected: understand any pre-existing user edits before applying doc changes. Do not revert unrelated edits.

- [ ] **Step 2: Update lifecycle section**

Replace the old lifecycle section with an explicit model summary:

```markdown
## Channel Runtime Lifecycle Model

`Unloaded` is represented by absence from the owning reactor's `channels` map.
Loaded runtimes hold `machine.ChannelState`, `appendQueue`, `replicationState`,
and `runtimeLifecycle`.

Leader phases:
- `Serving`
- `StoppingFollowers`
- `Checkpointing`
- `FinalRecheck`

Follower phases:
- `Replicating`
- `StopCheckpointing`
- `StopAcking`

Cooling is derived from idle age and pull delay; it is not stored as a phase.
```

Include a short mermaid state diagram matching the implemented names.

- [ ] **Step 3: Run doc diff check**

Run:

```bash
git diff --check -- pkg/channelv2/FLOW.md
```

Expected: no whitespace errors.

- [ ] **Step 4: Commit**

```bash
git add pkg/channelv2/FLOW.md
git commit -m "docs(channelv2): document explicit runtime lifecycle"
```

---

### Task 9: Final Verification

**Files:**
- No code changes unless verification finds a bug

- [ ] **Step 1: Run package tests**

Run:

```bash
go test ./pkg/channelv2/... -count=1
```

Expected: PASS.

- [ ] **Step 2: Run broader targeted tests**

Run:

```bash
go test ./internal/... ./pkg/... -count=1
```

Expected: PASS. If this is too slow in the local environment, at minimum run `go test ./pkg/... -count=1` and document why `./internal/...` was skipped.

- [ ] **Step 3: Inspect final diff**

Run:

```bash
git status --short
git log --oneline -8
```

Expected: only intended files are modified or committed. No unrelated user changes are included.

- [ ] **Step 4: Summarize implementation**

Prepare a final summary with:

- lifecycle model files added
- old scattered logic replaced
- tests run and results
- any skipped broad tests and why
- note whether `pkg/channelv2/FLOW.md` had pre-existing edits that were preserved
