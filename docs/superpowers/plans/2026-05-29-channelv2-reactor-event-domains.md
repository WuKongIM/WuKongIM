# ChannelV2 Reactor Event Domains Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Refactor `pkg/channelv2/reactor` internal leader/follower event handling into clear event-domain files without changing behavior or transport protocol types.

**Architecture:** Keep the existing concrete `Event` mailbox envelope, priority queues, worker pools, and due scheduler. Make the local node's role explicit through handler names and file boundaries: leader replication, follower replication, leader lifecycle runtime, and worker completion routing. Preserve the existing lifecycle and replication state models; this is a readability refactor with compile and behavior guard tests.

**Tech Stack:** Go, `go test`, existing `pkg/channelv2/reactor` unit tests, existing in-package test helpers.

---

## File Map

- Create: `pkg/channelv2/reactor/FLOW.md`
  - Package-local map of event domains and leader/follower flows.
- Create: `pkg/channelv2/reactor/event_domain_test.go`
  - Compile guard for the new local-role handler names.
- Create: `pkg/channelv2/reactor/leader_replication.go`
  - Leader-side inbound follower pull and ACK handling.
- Create: `pkg/channelv2/reactor/follower_replication.go`
  - Follower-side pull hint, legacy notify, pull/apply/ACK scheduling, and stopped follower eviction.
- Create: `pkg/channelv2/reactor/leader_lifecycle_runtime.go`
  - Leader idle lifecycle ticking, final eviction recheck, pull-hint send and retry completion.
- Create: `pkg/channelv2/reactor/worker_completion.go`
  - `EventWorkerResult` routing by `worker.TaskKind`.
- Create: `pkg/channelv2/reactor/runtime_helpers.go`
  - Small reactor helpers that do not belong to a specific event domain.
- Modify: `pkg/channelv2/reactor/event.go`
  - Add event-domain comments without changing enum values.
- Modify: `pkg/channelv2/reactor/reactor.go`
  - Keep loop, `Submit`, top-level `handle`, control, append, and metadata handlers. Remove moved leader/follower handlers.
- Modify: `pkg/channelv2/reactor/replication_runtime.go`
  - Drain this file by moving functions into the new domain files, then delete it when empty.
- Modify: `pkg/channelv2/reactor/lifecycle.go`
  - Keep lifecycle data structures and shared helpers. Move leader runtime lifecycle helpers to `leader_lifecycle_runtime.go`.
- Modify: `pkg/channelv2/reactor/scheduler.go`
  - Call renamed `tickFollowerReplication` and `tickLeaderLifecycle`.
- Test: `pkg/channelv2/reactor/*_test.go`
  - Existing tests remain the behavior contract. Add one compile guard test only.

Implementation must stage only files touched by this plan. Existing unrelated working tree changes must stay unstaged.

When running Go commands from this isolated worktree, prefix them with
`GOWORK=off`. The parent directory has a `go.work` file that does not include
this linked worktree, so unprefixed `go test` commands can resolve the wrong
workspace.

---

### Task 1: Add Reactor Flow Documentation

**Files:**
- Create: `pkg/channelv2/reactor/FLOW.md`
- Test: `pkg/channelv2/reactor/FLOW.md`

- [ ] **Step 1: Write the package flow document**

Create `pkg/channelv2/reactor/FLOW.md` with this content:

````markdown
# pkg/channelv2/reactor Flow

## Purpose

`pkg/channelv2/reactor` owns channel-keyed runtime state for channelv2. A single
reactor goroutine is the writer for each loaded channel runtime. Blocking store
and RPC work leaves the reactor through bounded worker pools and returns as
fenced worker completions.

The package keeps single-node deployments under the same cluster semantics: a
single node is a single-node cluster, not a bypass around replication logic.

## Event Domains

The mailbox uses one concrete `Event` envelope for performance. Handlers are
organized by the local node's role in the interaction.

```text
Control
  EventApplyMeta
  EventCheckState
  EventCancelWaiter
  EventClose

ClientWrite
  EventAppend

LeaderReplication
  EventPull
  EventAck
  EventLeaderEvictReady

FollowerReplication
  EventPullHint
  EventNotify
  tickFollowerReplication, usually driven by dueReplication

WorkerCompletion
  EventWorkerResult

Maintenance
  EventTick
  dueAppendFlush
  dueReplication
  dueLifecycle
```

## Leader-Side Replication

```text
remote follower Pull RPC
  -> Group.Submit(EventPull)
  -> handleLeaderPull
  -> recent leader cache hit: completeLeaderPull
  -> cache miss: submitStoreReadLog
  -> handleStoreReadLogResult
  -> completeLeaderPull
```

`handleLeaderPull` serves follower pull requests on the local leader. It
validates the current role, channel id, epoch, leader epoch, follower replica,
and range. Empty caught-up responses may pace the follower or offer
`PullControlStop` when idle eviction guards pass.

```text
remote follower Ack RPC
  -> Group.Submit(EventAck)
  -> handleLeaderAck
  -> ApplyFollowerAck
  -> complete quorum append waiters when HW advances
  -> record stopped follower state for stopped ACKs
  -> schedule leader lifecycle
```

Stopped ACKs must match the current activity version and leader LEO.

## Follower-Side Replication

```text
leader PullHint or legacy Notify
  -> Group.Submit(EventPullHint or EventNotify)
  -> handleFollowerPullHint or handleLegacyFollowerNotify
  -> mark follower dirty
  -> tickFollowerReplication
```

```text
tickFollowerReplication
  -> retry pending ACK before new pulls
  -> apply a pending pull before new pulls
  -> checkpoint and send stopped ACK after accepted stop control
  -> honor retry backoff and leader-provided park delay
  -> submit RPC Pull when eligible
```

The follower keeps at most one pull RPC, one pending pull response, one store
apply, and one ACK RPC in flight.

## Worker Completion Routing

```text
TaskStoreAppend      -> append completion
TaskStoreReadLog     -> leader pull completion
TaskStoreCheckpoint  -> leader checkpoint or follower stop checkpoint
TaskRPCPull          -> follower pull completion
TaskStoreApply       -> follower apply completion
TaskRPCAck           -> follower ACK completion
TaskRPCPullHint      -> leader pull-hint completion
```

Worker completions are accepted only when their channel key, generation, epoch,
leader epoch, and operation id match the current runtime state.
````

- [ ] **Step 2: Verify the document exists**

Run:

```bash
test -s pkg/channelv2/reactor/FLOW.md
```

Expected: exit code 0.

- [ ] **Step 3: Commit**

Run:

```bash
git add pkg/channelv2/reactor/FLOW.md
git commit -m "docs: map channelv2 reactor event flow"
```

Expected: commit succeeds and stages no unrelated files.

---

### Task 2: Add Handler Name Guard And Rename Top-Level Dispatch

**Files:**
- Create: `pkg/channelv2/reactor/event_domain_test.go`
- Modify: `pkg/channelv2/reactor/reactor.go`
- Modify: `pkg/channelv2/reactor/replication_runtime.go`
- Modify: `pkg/channelv2/reactor/lifecycle.go`
- Modify: `pkg/channelv2/reactor/scheduler.go`
- Test: `pkg/channelv2/reactor/event_domain_test.go`

- [ ] **Step 1: Write the failing compile guard**

Create `pkg/channelv2/reactor/event_domain_test.go`:

```go
package reactor

import "testing"

func TestEventDomainHandlerNames(t *testing.T) {
	var r *Reactor

	_ = r.handleLeaderPull
	_ = r.handleLeaderAck
	_ = r.handleFollowerPullHint
	_ = r.handleLegacyFollowerNotify
	_ = r.tickFollowerReplication
	_ = r.tickLeaderLifecycle
}
```

- [ ] **Step 2: Run the guard and verify it fails**

Run:

```bash
go test ./pkg/channelv2/reactor -run TestEventDomainHandlerNames -count=1
```

Expected before implementation: compile failure mentioning undefined methods
such as `r.handleLeaderPull`.

- [ ] **Step 3: Rename handlers without moving bodies**

Use `apply_patch` to rename these function declarations:

```text
handlePull             -> handleLeaderPull
handleAck              -> handleLeaderAck
handlePullHint         -> handleFollowerPullHint
handleNotify           -> handleLegacyFollowerNotify
tickReplication        -> tickFollowerReplication
tickLifecycle          -> tickLeaderLifecycle
```

Update the top-level dispatch in `pkg/channelv2/reactor/reactor.go` to call the
new names:

```go
case EventPull:
	r.handleLeaderPull(event)
case EventAck:
	r.handleLeaderAck(event)
case EventNotify:
	r.handleLegacyFollowerNotify(event)
case EventPullHint:
	r.handleFollowerPullHint(event)
```

Update `handleTick` in `pkg/channelv2/reactor/reactor.go`:

```go
r.tryFlushAppend(rc, now)
r.tickFollowerReplication(rc, now)
r.tickLeaderLifecycle(rc, now)
```

Update `processDueItem` in `pkg/channelv2/reactor/scheduler.go`:

```go
case dueReplication:
	if item.version != rc.replicationDueVersion {
		return
	}
	r.tickFollowerReplication(rc, now)
case dueLifecycle:
	if item.version != rc.lifecycleDueVersion {
		return
	}
	r.tickLeaderLifecycle(rc, now)
```

Update internal calls in follower code:

```go
r.tickFollowerReplication(rc, now)
```

Update internal calls in leader lifecycle code:

```go
r.tickLeaderLifecycle(rc, now)
```

- [ ] **Step 4: Format and run the guard**

Run:

```bash
gofmt -w pkg/channelv2/reactor/event_domain_test.go pkg/channelv2/reactor/reactor.go pkg/channelv2/reactor/replication_runtime.go pkg/channelv2/reactor/lifecycle.go pkg/channelv2/reactor/scheduler.go
go test ./pkg/channelv2/reactor -run TestEventDomainHandlerNames -count=1
```

Expected: PASS.

- [ ] **Step 5: Run focused existing behavior tests**

Run:

```bash
go test ./pkg/channelv2/reactor -run 'Test(LeaderPullCoveredByRecentCacheCompletesWithoutStoreRead|LeaderStoppedAckRequiresCurrentActivityVersionAndMatchAtLEO|FollowerPullHintInterruptsParked|StoppedAckStaleMetaCancelsStopAndPullsImmediately)$' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

Run:

```bash
git add pkg/channelv2/reactor/event_domain_test.go pkg/channelv2/reactor/reactor.go pkg/channelv2/reactor/replication_runtime.go pkg/channelv2/reactor/lifecycle.go pkg/channelv2/reactor/scheduler.go
git commit -m "refactor: name reactor event domains"
```

Expected: commit succeeds and stages no unrelated files.

---

### Task 3: Move Leader Replication Handlers

**Files:**
- Create: `pkg/channelv2/reactor/leader_replication.go`
- Modify: `pkg/channelv2/reactor/reactor.go`
- Modify: `pkg/channelv2/reactor/replication_runtime.go`
- Test: `pkg/channelv2/reactor/replication_state_test.go`
- Test: `pkg/channelv2/reactor/append_batch_test.go`

- [ ] **Step 1: Create the leader replication file**

Create `pkg/channelv2/reactor/leader_replication.go` with this package and
import block:

```go
package reactor

import (
	"context"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/machine"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/worker"
)
```

- [ ] **Step 2: Move leader pull and ACK functions unchanged**

Move the complete function bodies for these functions into
`leader_replication.go`:

```text
handleLeaderPull
tryCompletePullFromLeaderCache
leaderPullReadRange
handleLeaderAck
mergeLeaderPullCacheSuffix
strictRecentRecordCacheSlice
handleStoreReadLogResult
completeLeaderPull
updateLeaderPullFollowerState
leaderPullDelay
```

Do not change branch conditions, error values, future completion behavior, or
store-read submission behavior while moving the functions.

- [ ] **Step 3: Remove unused imports**

After the move, run:

```bash
gofmt -w pkg/channelv2/reactor/leader_replication.go pkg/channelv2/reactor/reactor.go pkg/channelv2/reactor/replication_runtime.go
```

Expected: gofmt completes. Remove any imports reported by the compiler in the
next step.

- [ ] **Step 4: Run leader-side behavior tests**

Run:

```bash
go test ./pkg/channelv2/reactor -run 'Test(LeaderPullCoveredByRecentCacheCompletesWithoutStoreRead|LeaderPullCacheHitDoesNotOfferStopWithRecords|LeaderPullCaughtUpCompletesEmptyWithoutStoreRead|LeaderPullBeforeRecentCacheBaseReadsStorePrefixOnly|LeaderPullStorePrefixResultFencedAfterLeaderEpochChange|LeaderPullReadsStorePrefixAndMergesRecentCacheSuffix|LeaderStoppedAckRequiresCurrentActivityVersionAndMatchAtLEO|LeaderStoppedAckRejectsMatchAboveLEO|LeaderStoppedAckRejectsMismatchedOfferedVersion|LeaderEvictsOnlyAfterAllFollowersStoppedAck)$' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

Run:

```bash
git add pkg/channelv2/reactor/leader_replication.go pkg/channelv2/reactor/reactor.go pkg/channelv2/reactor/replication_runtime.go
git commit -m "refactor: split leader reactor replication handlers"
```

Expected: commit succeeds and stages no unrelated files.

---

### Task 4: Move Follower Replication Handlers

**Files:**
- Create: `pkg/channelv2/reactor/follower_replication.go`
- Modify: `pkg/channelv2/reactor/reactor.go`
- Modify: `pkg/channelv2/reactor/replication_runtime.go`
- Test: `pkg/channelv2/reactor/replication_state_test.go`

- [ ] **Step 1: Create the follower replication file**

Create `pkg/channelv2/reactor/follower_replication.go` with this package and
import block:

```go
package reactor

import (
	"context"
	"errors"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/worker"
)
```

- [ ] **Step 2: Move follower scheduling functions unchanged**

Move the complete function bodies for these functions into
`follower_replication.go`:

```text
tickFollowerReplication
trySubmitPull
trySubmitPendingApply
trySubmitPendingAck
submitAck
submitAckPayload
trySubmitStopCheckpoint
tryEvictStoppedFollower
handleLegacyFollowerNotify
handleRPCPullResult
scheduleEmptyLaggingPullRetry
handleFollowerStopControl
handleStoreApplyResult
handleRPCAckResult
backoffPull
canAcceptFollowerStop
```

Do not change the ordering inside `tickFollowerReplication`. The required order
is pending ACK, ACK inflight guard, stop path, pending pull apply, pull inflight
or park delay, then new pull.

- [ ] **Step 3: Update comments that mention old names**

In moved comments, keep the meaning but use the new names:

```go
// handleLegacyFollowerNotify accepts the legacy transport compatibility nudge
// and maps it to the current PullHint-driven follower resume path.
```

```go
// tryEvictStoppedFollower retries runtime deletion after the stopped ACK has already reached the leader.
```

- [ ] **Step 4: Format and run follower-side behavior tests**

Run:

```bash
gofmt -w pkg/channelv2/reactor/follower_replication.go pkg/channelv2/reactor/reactor.go pkg/channelv2/reactor/replication_runtime.go
go test ./pkg/channelv2/reactor -run 'Test(FollowerTickPullsFromLocalLEOPlusOne|FollowerPullInflightSuppressesDuplicatePull|FollowerMetaFenceResetsPullInflightAndStaleCompletionDoesNotClearNewPull|FollowerMetaFenceDropsPendingPullBeforeSchedulingNewEpoch|FollowerMetaFenceClearsAckState|FollowerPullErrorBacksOff|FollowerStopCheckpointsThenSendsStoppedAckBeforeEvicting|FollowerStopEmptyChannelSendsStoppedAckAndEvicts|StoppedAckRetryKeepsStoppedPayloadAndRuntime|StoppedAckStaleMetaCancelsStopAndPullsImmediately|FollowerPullHintInterruptsParked|FollowerStoreApplyResultSendsAck|FollowerAckResultResetsBackoff|StoreApplyPoolFullKeepsOnePendingPullAndRetries|AckPoolFullKeepsPendingAckAndRetriesOnTick)$' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

Run:

```bash
git add pkg/channelv2/reactor/follower_replication.go pkg/channelv2/reactor/reactor.go pkg/channelv2/reactor/replication_runtime.go
git commit -m "refactor: split follower reactor replication handlers"
```

Expected: commit succeeds and stages no unrelated files.

---

### Task 5: Move Leader Lifecycle Runtime Helpers

**Files:**
- Create: `pkg/channelv2/reactor/leader_lifecycle_runtime.go`
- Modify: `pkg/channelv2/reactor/lifecycle.go`
- Modify: `pkg/channelv2/reactor/replication_runtime.go`
- Test: `pkg/channelv2/reactor/replication_state_test.go`
- Test: `pkg/channelv2/reactor/append_batch_test.go`
- Test: `pkg/channelv2/reactor/group_test.go`

- [ ] **Step 1: Create the leader lifecycle runtime file**

Create `pkg/channelv2/reactor/leader_lifecycle_runtime.go` with this package
and import block:

```go
package reactor

import (
	"context"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/worker"
)
```

- [ ] **Step 2: Move leader lifecycle runtime functions unchanged**

Move the complete function bodies for these functions into
`leader_lifecycle_runtime.go`:

```text
sendPullHintsForAppend
tickLeaderLifecycle
resetPullHintLifecycle
followerNeedsImmediateProgress
leaderCanOfferStop
leaderIdleExpired
allFollowersCaughtUp
allFollowersStopped
hasPendingRuntimeWork
tryEvictLeader
submitLeaderEvictReady
handleLeaderEvictReady
trySubmitPullHint
handleRPCPullHintResult
sendCurrentPullHintIfNeeded
handleStoreCheckpointResult
handleLeaderCheckpointResult
```

Keep these shared lifecycle helpers in `lifecycle.go`:

```text
channelLifecycle
followerLifecycle
markAppendActivity
cancelLeaderEvictionForAppend
resetLeaderCheckpointLifecycle
retireFollowerPullHints
syncLeaderFollowers
syncFollowerMatches
leaderIdleSince
currentAppendSubmitSeq
bumpAppendSubmitSeqLocked
clearAppendSubmitStateLocked
clearAppendSubmitState
reserveAppend
```

- [ ] **Step 3: Move neutral runtime helpers**

Create `pkg/channelv2/reactor/runtime_helpers.go`:

```go
package reactor

import ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"

func (r *Reactor) nextOpID() ch.OpID {
	if r.cfg.NextOpID != nil {
		return r.cfg.NextOpID()
	}
	return ch.OpID(r.nextOp.Add(1))
}

func minUint64(left, right uint64) uint64 {
	if left < right {
		return left
	}
	return right
}
```

Remove the moved `nextOpID` and `minUint64` definitions from
`replication_runtime.go`.

- [ ] **Step 4: Delete the drained replication runtime file**

After all functions have moved out of `pkg/channelv2/reactor/replication_runtime.go`,
delete the file:

```bash
git rm pkg/channelv2/reactor/replication_runtime.go
```

Expected: the file is removed only when it contains no remaining functions.

- [ ] **Step 5: Format and run leader lifecycle tests**

Run:

```bash
gofmt -w pkg/channelv2/reactor/leader_lifecycle_runtime.go pkg/channelv2/reactor/lifecycle.go pkg/channelv2/reactor/runtime_helpers.go
go test ./pkg/channelv2/reactor -run 'Test(AppendSendsPullHintToInactiveFollowersOncePerActivityVersion|AppendSendsPullHintRetryAfterDrop|PullHintInflightAppendSendsCurrentVersionAfterOldSuccess|PullHintInflightAppendSchedulesCurrentVersionRetryAfterOldError|MetadataRefreshIgnoresStalePullHintResult|AppendAfterStopOfferSendsPullHintWithoutWaitingForParkDelay|AppendAfterZeroVersionStopOfferSendsPullHintImmediately|LeaderCheckpointCompletionStaleAfterNewAppendDoesNotEvict|LeaderCheckpointCompletionDefersEvictionBehindQueuedAppend|LeaderReadyEvictionDefersWhenAppendSubmitsAfterReadyEventQueued|LeaderReadyEvictionDefersWhileAppendReservedBeforeSubmit|LeaderReadyEvictionReservationRetryWaitsForInterval|LeaderReadyEvictionIgnoresUnrelatedAppendReservation|SingleNodeClusterLeaderEvictsAfterIdleCheckpoint|ObserverSeesPullHintSentAndDropped|ObserverSeesFollowerStopped|ObserverSeesChannelRuntimeEvicted)$' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

Run:

```bash
git add pkg/channelv2/reactor/leader_lifecycle_runtime.go pkg/channelv2/reactor/lifecycle.go pkg/channelv2/reactor/runtime_helpers.go
git add -u pkg/channelv2/reactor/replication_runtime.go
git commit -m "refactor: split leader reactor lifecycle runtime"
```

Expected: commit succeeds and stages no unrelated files.

---

### Task 6: Move Worker Completion Routing And Comment Event Domains

**Files:**
- Create: `pkg/channelv2/reactor/worker_completion.go`
- Modify: `pkg/channelv2/reactor/reactor.go`
- Modify: `pkg/channelv2/reactor/event.go`
- Test: `pkg/channelv2/reactor/event_domain_test.go`

- [ ] **Step 1: Create the worker completion routing file**

Create `pkg/channelv2/reactor/worker_completion.go`:

```go
package reactor

import "github.com/WuKongIM/WuKongIM/pkg/channelv2/worker"

func (r *Reactor) handleWorkerResult(event Event) {
	switch event.Worker.Kind {
	case worker.TaskStoreAppend:
		r.handleStoreAppendResult(event.Worker)
	case worker.TaskStoreReadLog:
		r.handleStoreReadLogResult(event.Worker)
	case worker.TaskStoreCheckpoint:
		r.handleStoreCheckpointResult(event.Worker)
	case worker.TaskRPCPull:
		r.handleRPCPullResult(event.Worker)
	case worker.TaskStoreApply:
		r.handleStoreApplyResult(event.Worker)
	case worker.TaskRPCAck:
		r.handleRPCAckResult(event.Worker)
	case worker.TaskRPCPullHint:
		r.handleRPCPullHintResult(event.Worker)
	}
}
```

Remove the old `handleWorkerResult` body from `reactor.go`.

- [ ] **Step 2: Extend the compile guard**

Modify `pkg/channelv2/reactor/event_domain_test.go`:

```go
func TestEventDomainHandlerNames(t *testing.T) {
	var r *Reactor

	_ = r.handleLeaderPull
	_ = r.handleLeaderAck
	_ = r.handleFollowerPullHint
	_ = r.handleLegacyFollowerNotify
	_ = r.tickFollowerReplication
	_ = r.tickLeaderLifecycle
	_ = r.handleWorkerResult
}
```

- [ ] **Step 3: Add domain comments to EventKind**

Modify `pkg/channelv2/reactor/event.go` so the constant block reads:

```go
const (
	// Control events manage metadata, runtime lookup, cancellation, and close.
	EventApplyMeta EventKind = iota + 1
	// EventCheckState asks the owning reactor whether it has channel state loaded.
	EventCheckState
	// EventAppend is the client write event handled by the local leader.
	EventAppend
	// EventWorkerResult carries a blocking worker completion back to its reactor.
	EventWorkerResult
	// EventTick asks a reactor to perform low-priority maintenance work.
	EventTick
	// EventCancelWaiter cooperatively cancels a previously admitted waiter.
	EventCancelWaiter
	// EventPull is an inbound follower pull handled by the local leader.
	EventPull
	// EventAck is an inbound follower progress report handled by the local leader.
	EventAck
	// EventNotify accepts legacy transport compatibility nudges.
	EventNotify
	// EventPullHint wakes a local follower after leader progress.
	EventPullHint
	// EventLeaderEvictReady performs the final normal-priority leader eviction recheck.
	EventLeaderEvictReady
	EventClose
)
```

- [ ] **Step 4: Format and run routing guard**

Run:

```bash
gofmt -w pkg/channelv2/reactor/worker_completion.go pkg/channelv2/reactor/reactor.go pkg/channelv2/reactor/event.go pkg/channelv2/reactor/event_domain_test.go
go test ./pkg/channelv2/reactor -run TestEventDomainHandlerNames -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

Run:

```bash
git add pkg/channelv2/reactor/worker_completion.go pkg/channelv2/reactor/reactor.go pkg/channelv2/reactor/event.go pkg/channelv2/reactor/event_domain_test.go
git commit -m "refactor: isolate reactor worker completion routing"
```

Expected: commit succeeds and stages no unrelated files.

---

### Task 7: Full Reactor Verification

**Files:**
- Verify: `pkg/channelv2/reactor`
- Verify: `docs/superpowers/specs/2026-05-29-channelv2-reactor-event-domains-design.md`

- [ ] **Step 1: Run the reactor package tests**

Run:

```bash
go test ./pkg/channelv2/reactor -count=1
```

Expected: PASS.

- [ ] **Step 2: Check for stale handler names**

Run:

```bash
rg -n "handlePull\\(|handleAck\\(|handlePullHint\\(|handleNotify\\(|tickReplication\\(|tickLifecycle\\(" pkg/channelv2/reactor
```

Expected: no output. The renamed methods should be:

```text
handleLeaderPull
handleLeaderAck
handleFollowerPullHint
handleLegacyFollowerNotify
tickFollowerReplication
tickLeaderLifecycle
```

- [ ] **Step 3: Check event-domain file layout**

Run:

```bash
rg -n "^func \\(r \\*Reactor\\) (handleLeaderPull|handleLeaderAck|handleFollowerPullHint|handleLegacyFollowerNotify|tickFollowerReplication|tickLeaderLifecycle|handleWorkerResult)" pkg/channelv2/reactor
```

Expected output includes these files:

```text
pkg/channelv2/reactor/leader_replication.go:... handleLeaderPull
pkg/channelv2/reactor/leader_replication.go:... handleLeaderAck
pkg/channelv2/reactor/follower_replication.go:... handleFollowerPullHint
pkg/channelv2/reactor/follower_replication.go:... handleLegacyFollowerNotify
pkg/channelv2/reactor/follower_replication.go:... tickFollowerReplication
pkg/channelv2/reactor/leader_lifecycle_runtime.go:... tickLeaderLifecycle
pkg/channelv2/reactor/worker_completion.go:... handleWorkerResult
```

- [ ] **Step 4: Check formatting and staged scope**

Run:

```bash
gofmt -w pkg/channelv2/reactor
git diff --check -- pkg/channelv2/reactor
git status --short
```

Expected:

- `git diff --check` exits 0.
- `git status --short` shows only files intentionally changed by this plan plus any pre-existing unrelated unstaged files.

- [ ] **Step 5: Commit final verification cleanup**

When Step 4 shows reactor changes that were not committed in earlier tasks, run:

```bash
git add pkg/channelv2/reactor
git commit -m "test: verify channelv2 reactor event domains"
```

Expected: commit succeeds when there are staged reactor changes. When there are
no reactor changes, skip this commit and record that Task 7 was verification
only.

---

## Self-Review Checklist

- Spec coverage:
  - `FLOW.md`: Task 1.
  - Event-domain comments: Task 6.
  - Local-role handler names: Task 2.
  - Leader replication split: Task 3.
  - Follower replication split: Task 4.
  - Leader lifecycle runtime split: Task 5.
  - Worker completion routing: Task 6.
  - Performance constraints: Tasks 2-6 move concrete functions without new interfaces, channels, goroutines, or event payload allocations.
  - Verification: Task 7.
- Placeholder scan:
  - This plan contains no unresolved placeholder sections.
- Type consistency:
  - All renamed methods use the exact names from the approved design.
  - All test commands target existing reactor package tests or the new compile guard.
