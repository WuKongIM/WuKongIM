# ChannelV2 Lifecycle Controller Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Refactor `pkg/channelv2/reactor` so channel runtime stop, checkpoint, stopped ACK, pull-hint retry, and eviction state live in one per-channel lifecycle controller while preserving current transport behavior.

**Architecture:** Keep the reactor as the single writer and keep blocking I/O in existing worker pools. Split hot follower replication from cold lifecycle work: `replicationState` owns pull/apply/ordinary ACK/park/retry, while `channelRuntimeLifecycle` owns stop/checkpoint/stopped ACK/pull-hint/final eviction effects.

**Tech Stack:** Go, `pkg/channelv2/reactor`, existing `testify/require` tests, existing store/transport worker pools.

---

## Execution Notes

The current workspace may already contain uncommitted channelv2 pull-ACK piggyback changes. Before executing this plan, run:

```bash
git status --short
```

If `pkg/channelv2/reactor/follower_replication.go`, `pkg/channelv2/reactor/leader_replication.go`, `pkg/channelv2/reactor/replication_state_test.go`, `pkg/channelv2/transport/types.go`, or `pkg/channelv2/FLOW.md` are dirty, either finish and commit that work first or create an isolated worktree from the current branch. Do not overwrite unrelated user changes.

Run focused tests after each task:

```bash
go test ./pkg/channelv2/reactor -count=1
```

Run package tests after the final task:

```bash
go test ./pkg/channelv2/... -count=1
```

## File Structure

- Create `pkg/channelv2/reactor/lifecycle_controller.go`: lifecycle stage, effect, follower state, controller state, lifecycle events, and small state mutators.
- Create `pkg/channelv2/reactor/lifecycle_controller_test.go`: pure controller and guard tests.
- Modify `pkg/channelv2/reactor/reactor.go`: replace old lifecycle fields on `runtimeChannel`, initialize controller state, route metadata/appends/ticks through lifecycle helpers.
- Modify `pkg/channelv2/reactor/lifecycle.go`: keep append reservation helpers, replace old `channelLifecycle` and `followerLifecycle` helpers with controller-backed helpers or move them to `lifecycle_controller.go`.
- Modify `pkg/channelv2/reactor/runtime_snapshot.go`: build lifecycle views from controller-owned state.
- Modify `pkg/channelv2/reactor/follower_replication.go`: remove stop/checkpoint/stopped ACK state from `replicationState`; call lifecycle controller for follower stop control, stopped ACK result, and stopped runtime eviction.
- Modify `pkg/channelv2/reactor/leader_replication.go`: route stopped ACK handling, stop offers, follower pull state, and pull-hint retirement through controller-owned follower state.
- Modify `pkg/channelv2/reactor/leader_lifecycle_runtime.go`: make leader checkpoint/final recheck/pull-hint effects controller-backed, then shrink this file to effect submission helpers.
- Modify `pkg/channelv2/reactor/replication_state.go`: remove follower stop fields and stopped ACK payload fields, leaving hot replication state only.
- Modify `pkg/channelv2/reactor/lifecycle_model.go`, `pkg/channelv2/reactor/lifecycle_leader.go`, `pkg/channelv2/reactor/lifecycle_follower.go`, `pkg/channelv2/reactor/lifecycle_apply.go`: delete or reduce old dual-phase model after controller replacement.
- Modify `pkg/channelv2/reactor/*_test.go`: update internal assertions from old booleans and phases to controller stage/effect fields.
- Modify `pkg/channelv2/FLOW.md` and `pkg/channelv2/reactor/FLOW.md`: document the final lifecycle controller.

### Task 1: Add Controller Types and Pure Guard Tests

**Files:**
- Create: `pkg/channelv2/reactor/lifecycle_controller.go`
- Create: `pkg/channelv2/reactor/lifecycle_controller_test.go`

- [ ] **Step 1: Write failing controller tests**

Create `pkg/channelv2/reactor/lifecycle_controller_test.go` with these tests:

```go
package reactor

import (
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/stretchr/testify/require"
)

func TestLifecycleControllerIdleSinceUsesAppendThenLoad(t *testing.T) {
	loadedAt := time.Unix(100, 0)
	appendAt := time.Unix(200, 0)
	lc := newChannelRuntimeLifecycle(loadedAt, 3)

	require.Equal(t, loadedAt, lc.idleSince())

	lc.markAppend(appendAt, 4)

	require.Equal(t, appendAt, lc.idleSince())
	require.Equal(t, uint64(4), lc.version)
	require.Equal(t, lifecycleLive, lc.stage)
}

func TestLifecycleControllerResetForMetaClearsEffects(t *testing.T) {
	now := time.Unix(100, 0)
	lc := newChannelRuntimeLifecycle(now, 3)
	lc.stage = lifecycleLeaderReadyToEvict
	lc.checkpoint = lifecycleEffect{inflight: true, opID: 11, version: 3}
	lc.stoppedAck = lifecycleEffect{inflight: true, opID: 12, version: 3}
	lc.finalCheck = lifecycleEffect{queued: true, version: 3}
	lc.followers = map[ch.NodeID]*lifecycleFollower{
		2: {match: 3, stopOfferedVersion: 3, stoppedVersion: 3, hint: lifecycleEffect{inflight: true, opID: 13, version: 3}},
	}

	lc.resetForMeta(now.Add(time.Second), 5)

	require.Equal(t, lifecycleLive, lc.stage)
	require.Equal(t, uint64(5), lc.version)
	require.False(t, lc.checkpoint.active())
	require.False(t, lc.stoppedAck.active())
	require.False(t, lc.finalCheck.active())
	require.Zero(t, lc.followers[2].stopOfferedVersion)
	require.Zero(t, lc.followers[2].stoppedVersion)
	require.False(t, lc.followers[2].hint.active())
}

func TestLifecycleFollowerVersionPredicates(t *testing.T) {
	follower := lifecycleFollower{match: 7, stopOfferedVersion: 9, stoppedVersion: 9}

	require.True(t, follower.stopOffered(9))
	require.True(t, follower.stopped(9))
	require.False(t, follower.stopOffered(8))
	require.False(t, follower.stopped(8))
	require.True(t, follower.caughtUp(7))
	require.False(t, follower.caughtUp(8))
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run:

```bash
go test ./pkg/channelv2/reactor -run 'TestLifecycleController|TestLifecycleFollowerVersionPredicates' -count=1
```

Expected: compile failure because `newChannelRuntimeLifecycle`, `lifecycleStage`, `lifecycleEffect`, and `lifecycleFollower` are not defined.

- [ ] **Step 3: Add controller types**

Create `pkg/channelv2/reactor/lifecycle_controller.go`:

```go
package reactor

import (
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
)

// lifecycleStage describes stop and eviction progress for the local runtime role.
type lifecycleStage uint8

const (
	lifecycleLive lifecycleStage = iota + 1
	lifecycleLeaderStoppingFollowers
	lifecycleLeaderCheckpointing
	lifecycleLeaderReadyToEvict
	lifecycleFollowerCheckpointing
	lifecycleFollowerStoppedAcking
	lifecycleFollowerReadyToEvict
)

// lifecycleEffect fences one asynchronous lifecycle side effect.
type lifecycleEffect struct {
	inflight bool
	opID     ch.OpID
	version  uint64
	retryAt  time.Time
	queued   bool
}

func (e lifecycleEffect) active() bool {
	return e.inflight || e.opID != 0 || e.queued || !e.retryAt.IsZero()
}

func (e *lifecycleEffect) reset() {
	*e = lifecycleEffect{}
}

// lifecycleFollower is leader-visible lifecycle state for a remote replica.
type lifecycleFollower struct {
	match              uint64
	lastPullAt         time.Time
	nextExpectedPullAt time.Time
	lastHintVersion    uint64
	pendingHintVersion uint64
	hint               lifecycleEffect
	parked             bool
	stopOfferedVersion uint64
	stoppedVersion     uint64
}

func (f lifecycleFollower) caughtUp(leo uint64) bool {
	return f.match >= leo
}

func (f lifecycleFollower) stopOffered(version uint64) bool {
	return version != 0 && f.stopOfferedVersion == version
}

func (f lifecycleFollower) stopped(version uint64) bool {
	return version != 0 && f.stoppedVersion == version
}

func (f *lifecycleFollower) resetStop() {
	f.stopOfferedVersion = 0
	f.stoppedVersion = 0
}

func (f *lifecycleFollower) resetHint() {
	f.lastHintVersion = 0
	f.pendingHintVersion = 0
	f.hint.reset()
}

// followerStopState is follower-local state for an accepted PullControlStop.
type followerStopState struct {
	accepted bool
	version  uint64
	leaderLEO uint64
	leaderHW  uint64
}

// lifecyclePullHintInflight records enough information to finish a PullHint worker result.
type lifecyclePullHintInflight struct {
	follower ch.NodeID
	version  uint64
	reason   transport.PullHintReason
}

// channelRuntimeLifecycle owns stop, checkpoint, stopped ACK, hint, and eviction state.
type channelRuntimeLifecycle struct {
	stage lifecycleStage

	loadedAt     time.Time
	lastAppendAt time.Time
	version      uint64

	checkpoint lifecycleEffect
	stoppedAck  lifecycleEffect
	finalCheck  lifecycleEffect

	followerStop followerStopState

	followers        map[ch.NodeID]*lifecycleFollower
	pullHintInflight map[ch.OpID]lifecyclePullHintInflight
	nextDue          time.Time
}

func newChannelRuntimeLifecycle(now time.Time, version uint64) channelRuntimeLifecycle {
	return channelRuntimeLifecycle{
		stage:     lifecycleLive,
		loadedAt:  now,
		version:   version,
		followers: make(map[ch.NodeID]*lifecycleFollower),
	}
}

func (lc *channelRuntimeLifecycle) idleSince() time.Time {
	if lc == nil {
		return time.Time{}
	}
	if !lc.lastAppendAt.IsZero() {
		return lc.lastAppendAt
	}
	return lc.loadedAt
}

func (lc *channelRuntimeLifecycle) markAppend(now time.Time, version uint64) {
	if lc == nil {
		return
	}
	lc.stage = lifecycleLive
	lc.lastAppendAt = now
	if version > lc.version {
		lc.version = version
	}
	lc.checkpoint.reset()
	lc.finalCheck.reset()
	for _, follower := range lc.followers {
		if follower != nil && follower.match < version {
			follower.resetStop()
		}
	}
}

func (lc *channelRuntimeLifecycle) resetForMeta(now time.Time, version uint64) {
	if lc == nil {
		return
	}
	lc.stage = lifecycleLive
	lc.loadedAt = now
	lc.lastAppendAt = time.Time{}
	lc.version = version
	lc.checkpoint.reset()
	lc.stoppedAck.reset()
	lc.finalCheck.reset()
	lc.followerStop = followerStopState{}
	lc.pullHintInflight = nil
	lc.nextDue = time.Time{}
	for _, follower := range lc.followers {
		if follower == nil {
			continue
		}
		follower.resetStop()
		follower.resetHint()
		follower.parked = false
		follower.nextExpectedPullAt = time.Time{}
	}
}
```

- [ ] **Step 4: Run tests and verify they pass**

Run:

```bash
go test ./pkg/channelv2/reactor -run 'TestLifecycleController|TestLifecycleFollowerVersionPredicates' -count=1
```

Expected: pass.

- [ ] **Step 5: Commit Task 1**

```bash
git add pkg/channelv2/reactor/lifecycle_controller.go pkg/channelv2/reactor/lifecycle_controller_test.go
git commit -m "refactor: add channelv2 lifecycle controller skeleton"
```

### Task 2: Wire Controller State Into runtimeChannel Without Behavior Changes

**Files:**
- Modify: `pkg/channelv2/reactor/reactor.go`
- Modify: `pkg/channelv2/reactor/lifecycle.go`
- Modify: `pkg/channelv2/reactor/runtime_snapshot.go`
- Modify: `pkg/channelv2/reactor/lifecycle_controller.go`
- Modify: `pkg/channelv2/reactor/lifecycle_controller_test.go`

- [ ] **Step 1: Add tests for leader follower sync**

Append to `pkg/channelv2/reactor/lifecycle_controller_test.go`:

```go
func TestLifecycleSyncLeaderFollowersAddsUpdatesAndRemovesReplicas(t *testing.T) {
	meta := ch.Meta{
		Key:         "1:lifecycle-sync",
		ID:          ch.ChannelID{ID: "lifecycle-sync", Type: 1},
		Epoch:       1,
		LeaderEpoch: 1,
		Leader:      1,
		Replicas:    []ch.NodeID{1, 2, 3},
		ISR:         []ch.NodeID{1, 2, 3},
		MinISR:      2,
		Status:      ch.StatusActive,
	}
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: store.NewMemoryFactory(), MailboxSize: 16})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]

	require.NotNil(t, rc.lifecycle.followers[2])
	require.NotNil(t, rc.lifecycle.followers[3])

	rc.state.Progress[2] = machine.ReplicaProgress{Match: 4}
	r.syncLeaderFollowers(rc)

	require.Equal(t, uint64(4), rc.lifecycle.followers[2].match)

	rc.state.Replicas = []ch.NodeID{1, 2}
	r.syncLeaderFollowers(rc)

	require.NotNil(t, rc.lifecycle.followers[2])
	require.Nil(t, rc.lifecycle.followers[3])
}
```

Add these imports to the test file:

```go
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/machine"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
```

- [ ] **Step 2: Run the new sync test and verify it fails**

Run:

```bash
go test ./pkg/channelv2/reactor -run TestLifecycleSyncLeaderFollowersAddsUpdatesAndRemovesReplicas -count=1
```

Expected: fail because `runtimeChannel.lifecycle` still uses the old `channelLifecycle` type.

- [ ] **Step 3: Replace runtimeChannel lifecycle fields**

In `pkg/channelv2/reactor/reactor.go`, change the `runtimeChannel` lifecycle fields:

```go
	// replication owns follower pull, apply, and ordinary ACK scheduling state.
	replication replicationState
	// lifecycle owns runtime stop, checkpoint, hint, and eviction state.
	lifecycle channelRuntimeLifecycle
```

Remove these fields from `runtimeChannel`:

```go
	runtimeLifecycle runtimeLifecycle
	followers map[ch.NodeID]*followerLifecycle
	pullHintInflight map[ch.OpID]lifecyclePullHintInflight
```

In `ensureChannel`, replace old initialization:

```go
lifecycle: newChannelRuntimeLifecycle(time.Now(), initial.LEO),
```

Remove `runtimeLifecycle: runtimeLifecycle{...}` from the literal.

- [ ] **Step 4: Update follower map helpers**

In `pkg/channelv2/reactor/lifecycle.go`, replace `syncLeaderFollowers` with:

```go
func (r *Reactor) syncLeaderFollowers(rc *runtimeChannel) {
	if rc == nil {
		return
	}
	if rc.state == nil || rc.state.Role != ch.RoleLeader {
		rc.lifecycle.followers = nil
		rc.lifecycle.pullHintInflight = nil
		return
	}
	if rc.lifecycle.followers == nil {
		rc.lifecycle.followers = make(map[ch.NodeID]*lifecycleFollower)
	}
	current := make(map[ch.NodeID]struct{}, len(rc.state.Replicas))
	for _, replica := range rc.state.Replicas {
		if replica == r.cfg.LocalNode {
			continue
		}
		current[replica] = struct{}{}
		progress := rc.state.Progress[replica]
		follower := rc.lifecycle.followers[replica]
		if follower == nil {
			follower = &lifecycleFollower{match: progress.Match}
			rc.lifecycle.followers[replica] = follower
			continue
		}
		if progress.Match > follower.match {
			follower.match = progress.Match
		}
	}
	for node := range rc.lifecycle.followers {
		if _, ok := current[node]; !ok {
			delete(rc.lifecycle.followers, node)
		}
	}
}
```

Replace `syncFollowerMatches` with:

```go
func (r *Reactor) syncFollowerMatches(rc *runtimeChannel) {
	if rc == nil || rc.state == nil || rc.state.Role != ch.RoleLeader {
		return
	}
	r.syncLeaderFollowers(rc)
	for node, follower := range rc.lifecycle.followers {
		progress := rc.state.Progress[node]
		if follower != nil && progress.Match > follower.match {
			follower.match = progress.Match
		}
	}
}
```

Replace `leaderIdleSince` with:

```go
func leaderIdleSince(rc *runtimeChannel) time.Time {
	if rc == nil {
		return time.Time{}
	}
	return rc.lifecycle.idleSince()
}
```

- [ ] **Step 5: Update snapshot follower fields**

In `pkg/channelv2/reactor/runtime_snapshot.go`, change the follower loop:

```go
	for node, follower := range rc.lifecycle.followers {
		if follower == nil {
			continue
		}
		view.Followers = append(view.Followers, FollowerView{
			Node:           node,
			Match:          follower.match,
			Stopped:        follower.stopped(rc.lifecycle.version),
			StopAckVersion: follower.stoppedVersion,
			StopOffered:    follower.stopOffered(rc.lifecycle.version),
		})
	}
```

Change activity view:

```go
	view.ActivityVersion = rc.lifecycle.version
```

Change pending lifecycle work:

```go
		LifecycleCheckpoint: rc.lifecycle.checkpoint.inflight || rc.lifecycle.checkpoint.opID != 0,
		LifecycleRetry:      !rc.lifecycle.checkpoint.retryAt.IsZero() || !rc.lifecycle.finalCheck.retryAt.IsZero(),
```

- [ ] **Step 6: Replace old lifecycle field references mechanically**

Use `rg` to find old names:

```bash
rg -n 'rc\.lifecycle\.(LoadedAt|LastAppendAt|ActivityVersion|Checkpoint|followers|pullHintInflight|runtimeLifecycle)' pkg/channelv2/reactor
```

Apply these exact replacements in code:

```text
rc.lifecycle.LoadedAt -> rc.lifecycle.loadedAt
rc.lifecycle.LastAppendAt -> rc.lifecycle.lastAppendAt
rc.lifecycle.ActivityVersion -> rc.lifecycle.version
rc.lifecycle.CheckpointInflight -> rc.lifecycle.checkpoint.inflight
rc.lifecycle.CheckpointOpID -> rc.lifecycle.checkpoint.opID
rc.lifecycle.CheckpointActivityVersion -> rc.lifecycle.checkpoint.version
rc.lifecycle.CheckpointReady -> rc.lifecycle.finalCheck.inflight
rc.lifecycle.CheckpointReadyActivityVersion -> rc.lifecycle.finalCheck.version
rc.lifecycle.CheckpointReadyQueued -> rc.lifecycle.finalCheck.queued
rc.lifecycle.CheckpointRetryAt -> rc.lifecycle.checkpoint.retryAt
rc.followers -> rc.lifecycle.followers
rc.pullHintInflight -> rc.lifecycle.pullHintInflight
```

For old phase writes, use:

```text
rc.runtimeLifecycle.LeaderPhase = LeaderLifecycleServing -> rc.lifecycle.stage = lifecycleLive
rc.runtimeLifecycle.LeaderPhase = LeaderLifecycleCheckpointing -> rc.lifecycle.stage = lifecycleLeaderCheckpointing
rc.runtimeLifecycle.LeaderPhase = LeaderLifecycleFinalRecheck -> rc.lifecycle.stage = lifecycleLeaderReadyToEvict
rc.runtimeLifecycle.FollowerPhase = FollowerLifecycleReplicating -> rc.lifecycle.stage = lifecycleLive
rc.runtimeLifecycle.FollowerPhase = FollowerLifecycleStopCheckpointing -> rc.lifecycle.stage = lifecycleFollowerCheckpointing
rc.runtimeLifecycle.FollowerPhase = FollowerLifecycleStopAcking -> rc.lifecycle.stage = lifecycleFollowerStoppedAcking
```

- [ ] **Step 7: Remove old type definitions that now conflict**

Delete these old definitions from `pkg/channelv2/reactor/lifecycle.go`:

```go
type channelLifecycle struct { ... }
type followerLifecycle struct { ... }
```

Keep the old `pullHintInflight` type in `pkg/channelv2/reactor/reactor.go` until all old references are gone. The controller uses `lifecyclePullHintInflight` during the transition to avoid a duplicate type definition.

- [ ] **Step 8: Run focused tests**

Run:

```bash
go test ./pkg/channelv2/reactor -run 'TestLifecycleSyncLeaderFollowersAddsUpdatesAndRemovesReplicas|TestRuntimeViewFromChannelCapturesPendingWork|TestLeaderReturnsStopWhenAllReplicasCaughtUpAndIdle' -count=1
```

Expected: pass.

- [ ] **Step 9: Run reactor tests**

Run:

```bash
go test ./pkg/channelv2/reactor -count=1
```

Expected: pass.

- [ ] **Step 10: Commit Task 2**

```bash
git add pkg/channelv2/reactor
git commit -m "refactor: wire channelv2 lifecycle controller state"
```

### Task 3: Move Follower Stop State Out of replicationState

**Files:**
- Modify: `pkg/channelv2/reactor/replication_state.go`
- Modify: `pkg/channelv2/reactor/follower_replication.go`
- Modify: `pkg/channelv2/reactor/leader_lifecycle_runtime.go`
- Modify: `pkg/channelv2/reactor/runtime_snapshot.go`
- Modify: `pkg/channelv2/reactor/replication_state_test.go`

- [ ] **Step 1: Add failing test for follower stop state ownership**

In `pkg/channelv2/reactor/replication_state_test.go`, add this assertion to `TestFollowerStopCheckpointsThenSendsStoppedAckBeforeEvicting` immediately after the stop control is handled:

```go
	require.Equal(t, lifecycleFollowerCheckpointing, rc.lifecycle.stage)
	require.True(t, rc.lifecycle.followerStop.accepted)
	require.Equal(t, uint64(3), rc.lifecycle.followerStop.version)
	require.True(t, rc.lifecycle.checkpoint.inflight)
	require.False(t, rc.replication.hasStopLifecycle())
```

Add this helper to `pkg/channelv2/reactor/replication_state.go` temporarily so the failing test compiles before fields are removed:

```go
func (s replicationState) hasStopLifecycle() bool {
	return s.stopping ||
		s.stopActivityVersion != 0 ||
		s.checkpointInflight ||
		s.checkpointOpID != 0 ||
		!s.nextCheckpointAt.IsZero() ||
		s.deleteAfterStoppedAck ||
		s.stopAcked ||
		!s.nextStopEvictAt.IsZero() ||
		s.pendingAckStopped ||
		s.pendingAckActivityVersion != 0 ||
		s.ackStopped ||
		s.ackActivityVersion != 0
}
```

- [ ] **Step 2: Run the ownership test and verify it fails**

Run:

```bash
go test ./pkg/channelv2/reactor -run TestFollowerStopCheckpointsThenSendsStoppedAckBeforeEvicting -count=1
```

Expected: fail because follower stop state is still stored in `replicationState`.

- [ ] **Step 3: Add follower stop lifecycle helpers**

Add to `pkg/channelv2/reactor/lifecycle_controller.go`:

```go
func (lc *channelRuntimeLifecycle) acceptFollowerStop(version, leaderLEO, leaderHW uint64) {
	lc.stage = lifecycleFollowerCheckpointing
	lc.followerStop = followerStopState{
		accepted:  true,
		version:   version,
		leaderLEO: leaderLEO,
		leaderHW:  leaderHW,
	}
	lc.stoppedAck.reset()
}

func (lc *channelRuntimeLifecycle) cancelFollowerStop() {
	if lc == nil {
		return
	}
	lc.stage = lifecycleLive
	lc.followerStop = followerStopState{}
	lc.stoppedAck.reset()
	if lc.checkpoint.version != lc.version {
		lc.checkpoint.reset()
	}
}

func (lc *channelRuntimeLifecycle) followerStopVersion() uint64 {
	if lc == nil || !lc.followerStop.accepted {
		return 0
	}
	return lc.followerStop.version
}
```

- [ ] **Step 4: Move stop checkpoint submission to lifecycle**

In `pkg/channelv2/reactor/follower_replication.go`, replace `trySubmitStopCheckpoint` with:

```go
func (r *Reactor) trySubmitStopCheckpoint(rc *runtimeChannel, now time.Time) {
	if rc == nil || rc.state == nil || !rc.lifecycle.followerStop.accepted || rc.lifecycle.checkpoint.inflight {
		return
	}
	if !rc.lifecycle.checkpoint.retryAt.IsZero() && now.Before(rc.lifecycle.checkpoint.retryAt) {
		return
	}
	opID := r.nextOpID()
	fence := ch.Fence{ChannelKey: rc.state.Key, Generation: rc.state.Generation, Epoch: rc.state.Epoch, LeaderEpoch: rc.state.LeaderEpoch, OpID: opID}
	if err := r.submitStoreCheckpoint(context.Background(), rc.state.ID, fence, ch.Checkpoint{HW: rc.state.HW}); err != nil {
		rc.replication.backoff = nextReplicationBackoff(rc.replication.backoff, r.cfg.ReplicationMinBackoff, r.cfg.ReplicationMaxBackoff)
		rc.lifecycle.checkpoint.retryAt = now.Add(rc.replication.backoff)
		rc.lifecycle.checkpoint.version = rc.lifecycle.followerStop.version
		rc.replication.lastError = err
		return
	}
	rc.lifecycle.stage = lifecycleFollowerCheckpointing
	rc.lifecycle.checkpoint.inflight = true
	rc.lifecycle.checkpoint.opID = opID
	rc.lifecycle.checkpoint.version = rc.lifecycle.followerStop.version
	rc.lifecycle.checkpoint.retryAt = time.Time{}
}
```

- [ ] **Step 5: Update follower replication tick**

In `tickFollowerReplication`, replace the stop block:

```go
	if rc.lifecycle.stage == lifecycleFollowerReadyToEvict {
		r.tryEvictStoppedFollower(rc, now)
		return
	}
	if rc.lifecycle.followerStop.accepted {
		if rc.lifecycle.checkpoint.inflight || rc.lifecycle.stoppedAck.inflight {
			return
		}
		if rc.lifecycle.stage == lifecycleFollowerCheckpointing {
			r.trySubmitStopCheckpoint(rc, now)
			return
		}
		if rc.lifecycle.stage == lifecycleFollowerStoppedAcking {
			r.trySubmitStoppedAck(rc, now)
			return
		}
	}
```

Keep this block after ordinary pending ACK handling and before pending pull/apply work.

- [ ] **Step 6: Split stopped ACK submission from ordinary ACK**

Add to `pkg/channelv2/reactor/follower_replication.go`:

```go
func (r *Reactor) trySubmitStoppedAck(rc *runtimeChannel, now time.Time) bool {
	if rc == nil || rc.state == nil || !rc.lifecycle.followerStop.accepted || rc.lifecycle.stoppedAck.inflight {
		return false
	}
	if !rc.lifecycle.stoppedAck.retryAt.IsZero() && now.Before(rc.lifecycle.stoppedAck.retryAt) {
		return false
	}
	opID := r.nextOpID()
	version := rc.lifecycle.followerStop.version
	fence := ch.Fence{ChannelKey: rc.state.Key, Generation: rc.state.Generation, Epoch: rc.state.Epoch, LeaderEpoch: rc.state.LeaderEpoch, OpID: opID}
	req := transport.AckRequest{
		ChannelKey:      rc.state.Key,
		Epoch:           rc.state.Epoch,
		LeaderEpoch:     rc.state.LeaderEpoch,
		Follower:        r.cfg.LocalNode,
		MatchOffset:     rc.state.LEO,
		ActivityVersion: version,
		Stopped:         true,
	}
	if err := r.submitRPCAck(context.Background(), rc.state.Leader, fence, req); err != nil {
		rc.replication.backoff = nextReplicationBackoff(rc.replication.backoff, r.cfg.ReplicationMinBackoff, r.cfg.ReplicationMaxBackoff)
		rc.lifecycle.stoppedAck.retryAt = now.Add(rc.replication.backoff)
		rc.lifecycle.stoppedAck.version = version
		rc.replication.lastError = err
		return false
	}
	rc.lifecycle.stage = lifecycleFollowerStoppedAcking
	rc.lifecycle.stoppedAck.inflight = true
	rc.lifecycle.stoppedAck.opID = opID
	rc.lifecycle.stoppedAck.version = version
	rc.lifecycle.stoppedAck.retryAt = time.Time{}
	return true
}
```

Change `trySubmitPendingAck` and `submitAckPayload` so they only deal with ordinary ACK:

```go
func (r *Reactor) trySubmitPendingAck(rc *runtimeChannel, now time.Time) {
	if rc.replication.ackInflight || !rc.replication.pendingAck {
		return
	}
	if !rc.replication.nextAckAt.IsZero() && now.Before(rc.replication.nextAckAt) {
		return
	}
	r.submitAck(rc, rc.replication.pendingAckMatch, now)
}
```

Replace `submitAckPayload` with a `submitAck` implementation that sends `Stopped: false` and removes activity-version parameters.

- [ ] **Step 7: Update follower stop control**

In `handleFollowerStopControl`, replace the state mutation block with:

```go
	rc.state.HW = minUint64(rc.state.LEO, resp.LeaderHW)
	rc.lifecycle.acceptFollowerStop(resp.ActivityVersion, resp.LeaderLEO, resp.LeaderHW)
	rc.replication.parked = false
	rc.replication.dirty = false
	rc.replication.nextPullAt = time.Time{}
	rc.replication.nextPullAfter = 0
	r.trySubmitStopCheckpoint(rc, now)
```

When stop is rejected, replace `rc.replication.markDirty(now)` and recursive tick with:

```go
	rc.lifecycle.cancelFollowerStop()
	rc.replication.markDirty(now)
	r.scheduleReplicationFromState(rc, now)
```

- [ ] **Step 8: Route follower checkpoint completion through lifecycle**

In `handleStoreCheckpointResult`, replace the follower checkpoint branch with:

```go
	if rc.lifecycle.followerStop.accepted &&
		rc.lifecycle.checkpoint.inflight &&
		result.Fence.OpID == rc.lifecycle.checkpoint.opID &&
		result.Fence.Generation == rc.state.Generation &&
		result.Fence.Epoch == rc.state.Epoch &&
		result.Fence.LeaderEpoch == rc.state.LeaderEpoch {
		r.handleFollowerStopCheckpointResult(rc, result)
		return
	}
```

Add this function:

```go
func (r *Reactor) handleFollowerStopCheckpointResult(rc *runtimeChannel, result worker.Result) {
	now := time.Now()
	rc.lifecycle.checkpoint.inflight = false
	rc.lifecycle.checkpoint.opID = 0
	err := result.Err
	if err == nil && result.StoreCheckpoint == nil {
		err = ch.ErrInvalidConfig
	}
	if err != nil {
		rc.replication.backoff = nextReplicationBackoff(rc.replication.backoff, r.cfg.ReplicationMinBackoff, r.cfg.ReplicationMaxBackoff)
		rc.lifecycle.checkpoint.retryAt = now.Add(rc.replication.backoff)
		rc.replication.lastError = err
		r.scheduleReplicationFromState(rc, now)
		return
	}
	rc.replication.backoff = 0
	rc.replication.lastError = nil
	rc.lifecycle.checkpoint.retryAt = time.Time{}
	rc.lifecycle.stage = lifecycleFollowerStoppedAcking
	r.trySubmitStoppedAck(rc, now)
	r.scheduleReplicationFromState(rc, now)
}
```

- [ ] **Step 9: Route stopped ACK result through lifecycle**

In `handleRPCAckResult`, before ordinary ACK handling, add:

```go
	if rc.lifecycle.stoppedAck.inflight && result.Fence.OpID == rc.lifecycle.stoppedAck.opID {
		r.handleStoppedAckResult(rc, result)
		return
	}
```

Add:

```go
func (r *Reactor) handleStoppedAckResult(rc *runtimeChannel, result worker.Result) {
	now := time.Now()
	version := rc.lifecycle.stoppedAck.version
	rc.lifecycle.stoppedAck.inflight = false
	rc.lifecycle.stoppedAck.opID = 0
	if result.Err != nil {
		if errors.Is(result.Err, ch.ErrStaleMeta) {
			rc.lifecycle.cancelFollowerStop()
			rc.replication.backoff = 0
			rc.replication.lastError = result.Err
			rc.replication.markDirty(now)
			r.scheduleReplicationFromState(rc, now)
			return
		}
		rc.replication.backoff = nextReplicationBackoff(rc.replication.backoff, r.cfg.ReplicationMinBackoff, r.cfg.ReplicationMaxBackoff)
		rc.lifecycle.stoppedAck.retryAt = now.Add(rc.replication.backoff)
		rc.lifecycle.stoppedAck.version = version
		rc.replication.lastError = result.Err
		r.scheduleReplicationFromState(rc, now)
		return
	}
	rc.replication.backoff = 0
	rc.replication.lastError = nil
	rc.lifecycle.stoppedAck.retryAt = time.Time{}
	rc.lifecycle.stage = lifecycleFollowerReadyToEvict
	if !r.evictRuntimeChannel(rc.state.Key, rc, "stopped ack") {
		rc.lifecycle.finalCheck.retryAt = now.Add(r.cfg.IdleEvictCheckInterval)
		r.scheduleReplicationFromState(rc, now)
		return
	}
	r.clearAppendSubmitState(rc.state.Key)
}
```

- [ ] **Step 10: Remove stop fields from replicationState**

Delete these fields from `replicationState`:

```go
ackStopped
ackActivityVersion
pendingAckStopped
pendingAckActivityVersion
stopping
stopActivityVersion
checkpointInflight
checkpointOpID
nextCheckpointAt
deleteAfterStoppedAck
stopAcked
nextStopEvictAt
```

Delete `cancelStopping` and the temporary `hasStopLifecycle` helper.

Update `applyAckResult` so it only stores `match` and ordinary `pendingAckMatch`.

- [ ] **Step 11: Update pending work snapshot**

In `pendingWorkViewFromChannel`, change checkpoint and ACK state:

```go
		AckInflight:          replication.ackInflight || rc.lifecycle.stoppedAck.inflight,
		PendingAck:           replication.pendingAck,
		CheckpointInflight:   rc.lifecycle.stage == lifecycleFollowerCheckpointing && rc.lifecycle.checkpoint.inflight,
		CheckpointRetry:      rc.lifecycle.stage == lifecycleFollowerCheckpointing && !rc.lifecycle.checkpoint.retryAt.IsZero(),
```

- [ ] **Step 12: Run follower stop tests**

Run:

```bash
go test ./pkg/channelv2/reactor -run 'TestFollowerStop|TestStoppedAck|TestStaleStopCompletion|TestPullHintDuringInflightPullPreventsOldEmptyResponseParking' -count=1
```

Expected: pass.

- [ ] **Step 13: Run reactor tests**

Run:

```bash
go test ./pkg/channelv2/reactor -count=1
```

Expected: pass.

- [ ] **Step 14: Commit Task 3**

```bash
git add pkg/channelv2/reactor
git commit -m "refactor: move follower stop lifecycle into controller"
```

### Task 4: Move Leader Stop, Pull-Hint, and Follower State Into Controller APIs

**Files:**
- Modify: `pkg/channelv2/reactor/lifecycle_controller.go`
- Modify: `pkg/channelv2/reactor/lifecycle.go`
- Modify: `pkg/channelv2/reactor/leader_replication.go`
- Modify: `pkg/channelv2/reactor/leader_lifecycle_runtime.go`
- Modify: `pkg/channelv2/reactor/append_batch_test.go`
- Modify: `pkg/channelv2/reactor/replication_state_test.go`

- [ ] **Step 1: Add controller tests for leader followers**

Append to `pkg/channelv2/reactor/lifecycle_controller_test.go`:

```go
func TestLifecycleControllerLeaderFollowerStopVersioning(t *testing.T) {
	lc := newChannelRuntimeLifecycle(time.Unix(100, 0), 3)
	lc.followers[2] = &lifecycleFollower{match: 3}

	lc.offerStop(2)
	require.True(t, lc.followers[2].stopOffered(3))
	require.False(t, lc.followers[2].stopped(3))

	lc.markFollowerStopped(2, 3, 3)
	require.True(t, lc.followers[2].stopped(3))

	lc.markAppend(time.Unix(200, 0), 4)
	require.False(t, lc.followers[2].stopOffered(4))
	require.False(t, lc.followers[2].stopped(4))
}

func TestLifecycleControllerPullHintLifecycle(t *testing.T) {
	lc := newChannelRuntimeLifecycle(time.Unix(100, 0), 3)
	lc.followers[2] = &lifecycleFollower{match: 2, parked: true}

	require.True(t, lc.followerNeedsImmediateProgress(2, 3))

	lc.beginPullHint(2, 10, 3, transport.PullHintReasonAppend)
	require.True(t, lc.followers[2].hint.inflight)
	require.Equal(t, ch.OpID(10), lc.followers[2].hint.opID)
	require.Equal(t, uint64(3), lc.followers[2].lastHintVersion)

	lc.finishPullHint(10)
	require.False(t, lc.followers[2].hint.inflight)
}
```

- [ ] **Step 2: Run controller tests and verify they fail**

Run:

```bash
go test ./pkg/channelv2/reactor -run 'TestLifecycleControllerLeaderFollowerStopVersioning|TestLifecycleControllerPullHintLifecycle' -count=1
```

Expected: fail because controller follower methods are not defined.

- [ ] **Step 3: Add leader follower controller methods**

Add to `pkg/channelv2/reactor/lifecycle_controller.go`:

```go
func (lc *channelRuntimeLifecycle) offerStop(node ch.NodeID) {
	if lc == nil {
		return
	}
	if lc.followers == nil {
		lc.followers = make(map[ch.NodeID]*lifecycleFollower)
	}
	follower := lc.followers[node]
	if follower == nil {
		follower = &lifecycleFollower{}
		lc.followers[node] = follower
	}
	follower.stopOfferedVersion = lc.version
	follower.parked = false
	follower.nextExpectedPullAt = time.Time{}
}

func (lc *channelRuntimeLifecycle) markFollowerStopped(node ch.NodeID, version, match uint64) bool {
	if lc == nil || version == 0 || version != lc.version {
		return false
	}
	follower := lc.followers[node]
	if follower == nil {
		return false
	}
	already := follower.stopped(version)
	follower.stoppedVersion = version
	follower.parked = false
	if match > follower.match {
		follower.match = match
	}
	follower.resetHint()
	return !already
}

func (lc *channelRuntimeLifecycle) recordFollowerPull(node ch.NodeID, nextOffset, leo uint64, now time.Time) {
	if lc == nil {
		return
	}
	follower := lc.followers[node]
	if follower == nil {
		return
	}
	follower.lastPullAt = now
	follower.parked = false
	follower.nextExpectedPullAt = time.Time{}
	follower.stoppedVersion = 0
	match := nextOffset - 1
	if nextOffset != 0 && nextOffset <= leo+1 && match > follower.match {
		follower.match = match
	}
}

func (lc *channelRuntimeLifecycle) followerNeedsImmediateProgress(node ch.NodeID, leaderLEO uint64) bool {
	if lc == nil {
		return false
	}
	follower := lc.followers[node]
	if follower == nil || follower.match >= leaderLEO {
		return false
	}
	return follower.parked ||
		follower.stoppedVersion != 0 ||
		follower.stopOfferedVersion != 0 ||
		follower.lastPullAt.IsZero()
}

func (lc *channelRuntimeLifecycle) beginPullHint(node ch.NodeID, opID ch.OpID, version uint64, reason transport.PullHintReason) {
	if lc == nil {
		return
	}
	follower := lc.followers[node]
	if follower == nil {
		return
	}
	follower.hint = lifecycleEffect{inflight: true, opID: opID, version: version}
	follower.lastHintVersion = version
	follower.pendingHintVersion = 0
	if lc.pullHintInflight == nil {
		lc.pullHintInflight = make(map[ch.OpID]lifecyclePullHintInflight)
	}
	lc.pullHintInflight[opID] = lifecyclePullHintInflight{follower: node, version: version, reason: reason}
}

func (lc *channelRuntimeLifecycle) finishPullHint(opID ch.OpID) (lifecyclePullHintInflight, bool) {
	if lc == nil {
		return lifecyclePullHintInflight{}, false
	}
	inflight, ok := lc.pullHintInflight[opID]
	if !ok {
		return lifecyclePullHintInflight{}, false
	}
	delete(lc.pullHintInflight, opID)
	follower := lc.followers[inflight.follower]
	if follower == nil || !follower.hint.inflight || follower.hint.opID != opID {
		return lifecyclePullHintInflight{}, false
	}
	follower.hint.inflight = false
	follower.hint.opID = 0
	return inflight, true
}

func (lc *channelRuntimeLifecycle) retirePullHints(node ch.NodeID) {
	if lc == nil {
		return
	}
	for opID, inflight := range lc.pullHintInflight {
		if inflight.follower == node {
			delete(lc.pullHintInflight, opID)
		}
	}
	if follower := lc.followers[node]; follower != nil {
		follower.resetHint()
	}
}
```

- [ ] **Step 4: Update leader pull state**

In `handleLeaderPull`, replace direct follower mutation with:

```go
	rc.lifecycle.recordFollowerPull(event.Pull.Follower, event.Pull.NextOffset, rc.state.LEO, now)
	retireFollowerPullHints(rc, event.Pull.Follower)
```

In `completeLeaderPull`, replace stop offer mutation with:

```go
					control = transport.PullControlStop
					rc.lifecycle.offerStop(waiter.follower)
```

In `updateLeaderPullFollowerState`, replace `follower.Parked` and `NextExpectedPullAt` references with:

```go
	if follower := rc.lifecycle.followers[waiter.follower]; follower != nil {
		if len(records) == 0 && control != transport.PullControlStop {
			follower.parked = delay > 0
			follower.nextExpectedPullAt = now.Add(delay)
		} else {
			follower.parked = false
			follower.nextExpectedPullAt = time.Time{}
		}
	}
```

- [ ] **Step 5: Update stopped ACK handling**

In `handleLeaderAck`, replace stopped ACK follower mutation with:

```go
			if follower := rc.lifecycle.followers[event.Ack.Follower]; follower != nil {
				if follower.stopOfferedVersion != 0 && follower.stopOfferedVersion != event.Ack.ActivityVersion {
					event.Future.Complete(Result{Err: ch.ErrStaleMeta})
					return
				}
				wasNew := rc.lifecycle.markFollowerStopped(event.Ack.Follower, event.Ack.ActivityVersion, event.Ack.MatchOffset)
				if wasNew {
					r.observeFollowerStopped(rc.state.Key, event.Ack.Follower, event.Ack.ActivityVersion)
				}
			}
```

Replace ordinary ACK follower progress update with lowercase fields:

```go
	if follower := rc.lifecycle.followers[event.Ack.Follower]; follower != nil && event.Ack.MatchOffset > follower.match {
		follower.match = event.Ack.MatchOffset
	}
	if follower := rc.lifecycle.followers[event.Ack.Follower]; follower != nil && follower.match >= rc.state.LEO {
		retireFollowerPullHints(rc, event.Ack.Follower)
	}
```

Update `applyLeaderPullAckOffset` the same way.

- [ ] **Step 6: Update pull hint helpers**

In `leader_lifecycle_runtime.go`, change `followerNeedsImmediateProgress`:

```go
func (r *Reactor) followerNeedsImmediateProgress(rc *runtimeChannel, follower *lifecycleFollower) bool {
	if rc == nil || rc.state == nil || follower == nil || follower.match >= rc.state.LEO {
		return false
	}
	return follower.parked || follower.stoppedVersion != 0 || follower.stopOfferedVersion != 0 || follower.lastPullAt.IsZero()
}
```

Change `trySubmitPullHint` signature to use `*lifecycleFollower`.

Inside `trySubmitPullHint`, replace hint fields:

```go
	if follower.hint.inflight {
		if follower.lastHintVersion < version {
			follower.pendingHintVersion = version
		}
		return
	}
	if follower.lastHintVersion == version && !follower.hint.retryAt.IsZero() && now.Before(follower.hint.retryAt) {
		return
	}
	if follower.lastHintVersion == version && follower.hint.retryAt.IsZero() {
		return
	}
```

After successful submit:

```go
	rc.lifecycle.beginPullHint(node, opID, version, reason)
```

After submit failure:

```go
	follower.hint.inflight = false
	follower.hint.retryAt = now.Add(r.cfg.PullHintRetryInterval)
```

In `handleRPCPullHintResult`, replace map handling:

```go
	inflight, ok := rc.lifecycle.finishPullHint(result.Fence.OpID)
	if !ok {
		return
	}
	follower := rc.lifecycle.followers[inflight.follower]
	if follower == nil {
		return
	}
```

Use `inflight.version` instead of `inflight.activityVersion`.

- [ ] **Step 7: Update append tests and runtime tests to new fields**

Use `rg`:

```bash
rg -n 'StopOffered|StopAckVersion|Stopped|HintInflight|HintRetryAt|LastHintVersion|PendingHintVersion|Parked|Match' pkg/channelv2/reactor/*_test.go
```

Replace old field assertions:

```text
rc.followers[2].Stopped -> rc.lifecycle.followers[2].stopped(rc.lifecycle.version)
rc.followers[2].StopAckVersion -> rc.lifecycle.followers[2].stoppedVersion
rc.followers[2].StopOffered -> rc.lifecycle.followers[2].stopOffered(rc.lifecycle.version)
rc.followers[2].StopOfferedVersion -> rc.lifecycle.followers[2].stopOfferedVersion
rc.followers[2].HintInflight -> rc.lifecycle.followers[2].hint.inflight
rc.followers[2].HintRetryAt -> rc.lifecycle.followers[2].hint.retryAt
rc.followers[2].LastHintVersion -> rc.lifecycle.followers[2].lastHintVersion
rc.followers[2].PendingHintVersion -> rc.lifecycle.followers[2].pendingHintVersion
rc.followers[2].Parked -> rc.lifecycle.followers[2].parked
rc.followers[2].Match -> rc.lifecycle.followers[2].match
```

- [ ] **Step 8: Run leader lifecycle tests**

Run:

```bash
go test ./pkg/channelv2/reactor -run 'TestAppendSendsPullHint|TestPullHint|TestLeaderStoppedAck|TestLeaderReturnsStop|TestLeaderEvicts|TestSingleNodeClusterLeaderEvicts' -count=1
```

Expected: pass.

- [ ] **Step 9: Run reactor tests**

Run:

```bash
go test ./pkg/channelv2/reactor -count=1
```

Expected: pass.

- [ ] **Step 10: Commit Task 4**

```bash
git add pkg/channelv2/reactor
git commit -m "refactor: move leader follower lifecycle state into controller"
```

### Task 5: Centralize Lifecycle Driving and Leader Eviction Effects

**Files:**
- Modify: `pkg/channelv2/reactor/lifecycle_controller.go`
- Modify: `pkg/channelv2/reactor/leader_lifecycle_runtime.go`
- Modify: `pkg/channelv2/reactor/lifecycle_apply.go`
- Modify: `pkg/channelv2/reactor/lifecycle_model.go`
- Modify: `pkg/channelv2/reactor/lifecycle_leader.go`
- Modify: `pkg/channelv2/reactor/lifecycle_follower.go`
- Modify: `pkg/channelv2/reactor/scheduler.go`
- Modify: `pkg/channelv2/reactor/event_domain_test.go`

- [ ] **Step 1: Add lifecycle event and action types**

Add to `pkg/channelv2/reactor/lifecycle_controller.go`:

```go
type lifecycleEventKind uint8

const (
	lifecycleEventMetaFence lifecycleEventKind = iota + 1
	lifecycleEventAppendAdmitted
	lifecycleEventAppendStored
	lifecycleEventIdleTick
	lifecycleEventLeaderStoppedAck
	lifecycleEventFollowerStopControl
	lifecycleEventStoreCheckpointDone
	lifecycleEventStoppedAckDone
	lifecycleEventFinalEvictReady
	lifecycleEventPullHintDone
)

type lifecycleEvent struct {
	kind              lifecycleEventKind
	now               time.Time
	err               error
	activityVersion   uint64
	appendSeqObserved uint64
	follower          ch.NodeID
	matchOffset       uint64
	leaderLEO         uint64
	leaderHW          uint64
}

type lifecycleActionKind uint8

const (
	lifecycleActionScheduleLifecycle lifecycleActionKind = iota + 1
	lifecycleActionScheduleReplication
	lifecycleActionStartFollowerStopCheckpoint
	lifecycleActionSendStoppedAck
	lifecycleActionStartLeaderCheckpoint
	lifecycleActionQueueLeaderFinalRecheck
	lifecycleActionEvictRuntime
)

type lifecycleAction struct {
	kind lifecycleActionKind
	due  time.Time
}
```

- [ ] **Step 2: Add pure planning helpers**

Add:

```go
func planLeaderLifecycle(lc channelRuntimeLifecycle, view RuntimeView, now time.Time, idleAfter time.Duration) []lifecycleAction {
	if view.Role != ch.RoleLeader || view.Status != ch.StatusActive {
		return nil
	}
	if lc.stage == lifecycleLive && view.CanOfferFollowerStop(now, idleAfter) {
		return []lifecycleAction{{kind: lifecycleActionScheduleLifecycle, due: now}}
	}
	if view.HW >= view.LEO && view.AllFollowersStopped() && !view.HasPendingWork() {
		return []lifecycleAction{{kind: lifecycleActionStartLeaderCheckpoint}}
	}
	return nil
}

func planFinalLeaderEviction(view RuntimeView, now time.Time, retryInterval time.Duration) []lifecycleAction {
	if view.AppendFence.Reservations > 0 {
		return []lifecycleAction{{kind: lifecycleActionScheduleLifecycle, due: retryDue(now, retryInterval)}}
	}
	if view.SafeToEvict() && view.HW >= view.LEO && view.AllFollowersStopped() {
		return []lifecycleAction{{kind: lifecycleActionEvictRuntime}}
	}
	return []lifecycleAction{{kind: lifecycleActionScheduleLifecycle, due: retryDue(now, retryInterval)}}
}
```

- [ ] **Step 3: Add driver shell**

Add to `pkg/channelv2/reactor/lifecycle_apply.go` or move it to `lifecycle_controller.go`:

```go
func (r *Reactor) driveLifecycle(rc *runtimeChannel, ev lifecycleEvent, now time.Time) {
	if r == nil || rc == nil || rc.state == nil {
		return
	}
	if now.IsZero() {
		now = time.Now()
	}
	switch ev.kind {
	case lifecycleEventMetaFence:
		rc.lifecycle.resetForMeta(now, rc.state.LEO)
	case lifecycleEventAppendAdmitted:
		rc.lifecycle.markAppend(now, rc.state.LEO)
	case lifecycleEventAppendStored:
		rc.lifecycle.markAppend(now, rc.state.LEO)
	case lifecycleEventIdleTick:
		r.applyLifecycleActions(rc, planLeaderLifecycle(rc.lifecycle, runtimeViewFromChannel(rc, now, AppendFenceView{}), now, r.cfg.IdleEvictAfter), now)
	case lifecycleEventFinalEvictReady:
		r.applyFinalLeaderEviction(rc, ev.appendSeqObserved, now)
	}
	r.scheduleLifecycleFromState(rc, now)
}

func (r *Reactor) applyLifecycleActions(rc *runtimeChannel, actions []lifecycleAction, now time.Time) {
	for _, action := range actions {
		switch action.kind {
		case lifecycleActionScheduleLifecycle:
			r.scheduleLifecycleDue(rc, action.due)
		case lifecycleActionScheduleReplication:
			r.scheduleReplicationFromState(rc, now)
		case lifecycleActionStartFollowerStopCheckpoint:
			r.trySubmitStopCheckpoint(rc, now)
		case lifecycleActionSendStoppedAck:
			r.trySubmitStoppedAck(rc, now)
		case lifecycleActionStartLeaderCheckpoint:
			r.startLeaderCheckpoint(rc, now)
		case lifecycleActionQueueLeaderFinalRecheck:
			r.submitLeaderEvictReady(rc, now, r.currentAppendSubmitSeq(rc.state.Key))
		case lifecycleActionEvictRuntime:
			key := rc.state.Key
			if r.evictRuntimeChannel(key, rc, "lifecycle controller") {
				r.clearAppendSubmitState(key)
			}
		}
	}
}

func (r *Reactor) applyFinalLeaderEviction(rc *runtimeChannel, appendSeqObserved uint64, now time.Time) {
	if r == nil || rc == nil || rc.state == nil {
		return
	}
	key := rc.state.Key
	var requeue bool
	var retry bool
	var nextSeq uint64
	evicted := false

	r.submitMu.Lock()
	view := runtimeViewFromChannel(rc, now, AppendFenceView{
		Reservations: uint64(r.appendReservations[key]),
		SubmitSeq:    r.appendSubmitSeqs[key],
	})
	if view.AppendFence.Reservations > 0 {
		retry = true
	} else if appendSeqObserved != view.AppendFence.SubmitSeq {
		requeue = true
		nextSeq = view.AppendFence.SubmitSeq
	} else if view.SafeToEvict() && view.HW >= view.LEO && view.AllFollowersStopped() {
		evicted = r.evictRuntimeChannel(key, rc, "lifecycle controller final recheck")
		if evicted {
			r.clearAppendSubmitStateLocked(key)
		}
	} else {
		retry = true
	}
	r.submitMu.Unlock()

	if requeue {
		r.submitLeaderEvictReady(rc, now, nextSeq)
		return
	}
	if retry || !evicted {
		rc.lifecycle.finalCheck.retryAt = retryDue(now, r.cfg.IdleEvictCheckInterval)
		r.scheduleLifecycleFromState(rc, now)
	}
}
```

- [ ] **Step 4: Route ticks through driver**

In `tickLeaderLifecycle`, keep pull-hint retry scanning, then replace `r.tryEvictLeader(rc, now)` with:

```go
	r.driveLifecycle(rc, lifecycleEvent{kind: lifecycleEventIdleTick, now: now}, now)
```

In `handleLeaderEvictReady`, replace direct final recheck body with:

```go
	rc.lifecycle.finalCheck.queued = false
	now := time.Now()
	r.driveLifecycle(rc, lifecycleEvent{
		kind:              lifecycleEventFinalEvictReady,
		now:               now,
		appendSeqObserved: event.LeaderEvictAppendSeq,
	}, now)
```

`applyFinalLeaderEviction` is responsible for holding `submitMu` while it checks append reservations, compares append submit sequence, evicts, and calls `clearAppendSubmitStateLocked`. Do not call `clearAppendSubmitState` while `submitMu` is held.

- [ ] **Step 5: Remove old dual-phase model files**

Delete old pure model files after all references are gone:

```bash
git rm pkg/channelv2/reactor/lifecycle_model.go pkg/channelv2/reactor/lifecycle_leader.go pkg/channelv2/reactor/lifecycle_follower.go
```

Keep `lifecycle_apply.go` only if it contains `driveLifecycle`, `applyLifecycleActions`, `scheduleLifecycleDue`, and `startLeaderCheckpoint`.

- [ ] **Step 6: Update event domain compile guard**

In `pkg/channelv2/reactor/event_domain_test.go`, keep:

```go
_ = r.tickLeaderLifecycle
```

Add:

```go
_ = r.driveLifecycle
```

- [ ] **Step 7: Run lifecycle model tests and remove obsolete test file**

Run:

```bash
go test ./pkg/channelv2/reactor -run Lifecycle -count=1
```

Expected: tests in new `lifecycle_controller_test.go` pass. If `lifecycle_model_test.go` still references removed types, delete it and move any still-useful guard tests into `lifecycle_controller_test.go` using controller stages and effects.

- [ ] **Step 8: Run reactor tests**

Run:

```bash
go test ./pkg/channelv2/reactor -count=1
```

Expected: pass.

- [ ] **Step 9: Commit Task 5**

```bash
git add pkg/channelv2/reactor
git commit -m "refactor: centralize channelv2 lifecycle driving"
```

### Task 6: Clean Up Names, Docs, and Full Package Tests

**Files:**
- Modify: `pkg/channelv2/FLOW.md`
- Modify: `pkg/channelv2/reactor/FLOW.md`
- Modify: `pkg/channelv2/reactor/replication_state.go`
- Modify: `pkg/channelv2/reactor/lifecycle_controller.go`
- Modify: `pkg/channelv2/reactor/leader_lifecycle_runtime.go`
- Modify: `pkg/channelv2/reactor/follower_replication.go`

- [ ] **Step 1: Verify old lifecycle names are gone**

Run:

```bash
rg -n 'runtimeLifecycle|LeaderLifecycle|FollowerLifecycle|channelLifecycle|followerLifecycle|CheckpointReady|CheckpointActivityVersion|stopActivityVersion|deleteAfterStoppedAck|stopAcked|nextStopEvictAt|ackStopped|pendingAckStopped' pkg/channelv2/reactor
```

Expected: no output except comments in migration notes if any remain. Remove remaining references from code and tests.

- [ ] **Step 2: Verify replicationState is hot-path only**

Open `pkg/channelv2/reactor/replication_state.go` and confirm the struct contains only:

```go
pullInflight
pullOpID
ackInflight
ackOpID
ackMatch
pendingAck
pendingAckMatch
nextAckAt
dirty
parked
nextPullAt
nextPullAfter
lastActivityVersion
hintedLeaderLEO
backoff
lastLeaderHW
lastError
pendingPull
applyBlocked
applyRetryAt
applyOpID
```

If other stop/checkpoint fields remain, move them to `channelRuntimeLifecycle` or delete them if they are stale.

- [ ] **Step 3: Update package flow document**

In `pkg/channelv2/FLOW.md`, replace the "Channel Runtime Lifecycle Model" section with:

```markdown
## Channel Runtime Lifecycle Model

`Unloaded` is represented by absence from the owning reactor's `channels` map.
Loaded runtimes hold `machine.ChannelState`, append queue state, hot follower
replication state, leader recent-record cache, and one
`channelRuntimeLifecycle` controller.

The lifecycle controller is the only owner for stop and eviction state:

- `lifecycleLive`: runtime is serving its current leader or follower role.
- `lifecycleLeaderStoppingFollowers`: idle leader can offer `PullControlStop`
  to caught-up followers.
- `lifecycleLeaderCheckpointing`: leader checkpoint is in flight or retrying.
- `lifecycleLeaderReadyToEvict`: checkpoint finished and final normal-priority
  eviction recheck is queued or retrying.
- `lifecycleFollowerCheckpointing`: follower accepted stop control and is
  checkpointing local progress.
- `lifecycleFollowerStoppedAcking`: follower checkpoint finished and stopped
  ACK is in flight or retrying.
- `lifecycleFollowerReadyToEvict`: stopped ACK reached the leader and local
  runtime deletion is retrying if pending work blocks it.

`replicationState` is limited to hot follower pull, apply, ordinary ACK, park,
and retry state. Stopped ACKs remain on the standalone ACK RPC; ordinary durable
progress remains piggybacked on Pull requests.
```

- [ ] **Step 4: Update reactor flow document**

In `pkg/channelv2/reactor/FLOW.md`, replace lifecycle-specific prose with:

```markdown
## Lifecycle Controller

Each loaded `runtimeChannel` owns one `channelRuntimeLifecycle`. The controller
tracks lifecycle stage, activity version, effect fences, leader-visible
follower stop state, pull-hint effects, follower-local stop state, checkpoint
effects, stopped ACK effects, and final leader eviction recheck state.

Follower replication remains hot-path only: Pull, Apply, ordinary ACK, park,
and retry. When a follower receives `PullControlStop`, the lifecycle controller
takes over checkpoint, stopped ACK, and runtime eviction. When a leader is idle,
the lifecycle controller decides when to offer stop, start leader checkpoint,
queue final recheck, and evict.

All lifecycle worker completions are fenced by channel key, generation, epoch,
leader epoch, op id, and activity version.
```

- [ ] **Step 5: Run formatting**

Run:

```bash
gofmt -w pkg/channelv2/reactor
```

Expected: no output.

- [ ] **Step 6: Run reactor tests**

Run:

```bash
go test ./pkg/channelv2/reactor -count=1
```

Expected: pass.

- [ ] **Step 7: Run package tests**

Run:

```bash
go test ./pkg/channelv2/... -count=1
```

Expected: pass.

- [ ] **Step 8: Inspect final diff**

Run:

```bash
git diff --stat
git diff --check
```

Expected: `git diff --check` exits 0. The stat should show lifecycle/controller, reactor handler, tests, and flow docs only.

- [ ] **Step 9: Commit Task 6**

```bash
git add pkg/channelv2 docs/superpowers/plans/2026-05-29-channelv2-lifecycle-controller.md
git commit -m "docs: update channelv2 lifecycle controller flow"
```

## Final Verification

After all tasks are complete, run:

```bash
go test ./pkg/channelv2/reactor -count=1
go test ./pkg/channelv2/... -count=1
git status --short
```

Expected:

- reactor tests pass
- channelv2 package tests pass
- `git status --short` shows no unintended unstaged changes

## Handoff Notes

Keep the protocol stable throughout implementation. If a test failure suggests changing `transport.PullResponse.Control`, stopped ACK shape, or append reservation behavior, stop and re-check the design before changing protocol or service-facing behavior.
