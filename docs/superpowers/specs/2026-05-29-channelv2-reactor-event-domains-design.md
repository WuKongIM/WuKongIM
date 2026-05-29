# ChannelV2 Reactor Event Domains Design

**Date:** 2026-05-29
**Status:** Design approved by user, pending implementation plan

## Overview

Refactor `pkg/channelv2/reactor` internal event handling so leader and follower
interactions are easier to read without changing runtime behavior or public
transport protocol DTOs.

The reactor already has a performance-friendly execution model: a single
reactor goroutine owns each channel runtime, events are routed through a
priority mailbox, blocking store and RPC work goes through bounded worker
pools, and scheduled maintenance uses a due heap instead of scanning all loaded
channels. This design keeps those choices intact.

The current clarity problem is that the mailbox `Event` envelope, transport
requests, worker completions, follower replication state, and leader/follower
lifecycle decisions are spread across broad handlers. A reader must jump across
`reactor.go`, `replication_runtime.go`, `lifecycle.go`, and
`lifecycle_*.go` files to understand whether a path is running with the local
node acting as leader or follower.

The target shape makes those roles explicit by grouping handlers into internal
event domains while preserving the existing event envelope and hot-path
allocation profile.

## Goals

- Keep all changes internal to `pkg/channelv2/reactor`.
- Preserve `Group.Submit`, `Reactor.Submit`, mailbox priority, worker pools,
  and due scheduler behavior.
- Preserve `pkg/channelv2/transport` request and response types and wire
  behavior.
- Make local-node role explicit in handler names and file layout.
- Separate leader-side replication handling from follower-side replication
  scheduling.
- Keep lifecycle decisions pure and continue applying side effects in the
  reactor.
- Add a concise `pkg/channelv2/reactor/FLOW.md` before code movement so the
  package has a local map for future readers.

## Non-Goals

- No protocol redesign for `Pull`, `Ack`, `PullHint`, or `Notify`.
- No additional goroutines, per-domain channels, interface payloads, or
  polymorphic event dispatch.
- No replacement of `machine.ChannelState`, `appendQueue`, `replicationState`,
  `runtimeLifecycle`, or worker task types.
- No behavior change for append batching, pull caching, pull hints, stopped
  ACKs, idle eviction, checkpointing, or final leader eviction fencing.
- No change outside `pkg/channelv2/reactor` except the design document and the
  package-local `FLOW.md`.
- No single-node bypass branch. Single-node deployment remains a single-node
  cluster.

## Current Shape

`Event` is a single mailbox envelope:

```text
Event
  Kind
  Key
  Meta
  Append
  Pull
  Ack
  Notify
  PullHint
  Worker
  lifecycle and cancellation fields
```

That is efficient and should remain. The issue is semantic density:

- `EventPull` is handled by the local leader on behalf of a remote follower.
- `EventAck` is handled by the local leader when a remote follower reports
  progress or stopped state.
- `EventPullHint` and `EventNotify` are handled by the local follower when a
  leader asks it to resume pulling.
- `EventWorkerResult` can complete append store work, leader pull reads,
  leader checkpoints, follower pull RPCs, follower apply, follower ACKs, or
  leader pull-hint sends.
- `EventTick` and due items drive append flushes, follower replication, and
  leader lifecycle work through one generic maintenance path.

The current implementation is correct but forces readers to derive the role
from the payload and surrounding state checks.

## Target Event Domains

Keep the existing `EventKind` constants and numeric values, but document and
route them by internal domain:

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

The important naming rule is that handler names should describe the local
node's role:

- `handleLeaderPull` for inbound follower pull RPCs served by a leader.
- `handleLeaderAck` for inbound follower ACKs served by a leader.
- `handleFollowerPullHint` for leader wakeups received by a follower.
- `handleLegacyFollowerNotify` for compatibility nudges mapped to follower
  pull-hint behavior.
- `tickFollowerReplication` for follower pull/apply/ack scheduling.
- `tickLeaderLifecycle` for leader idle slowdown, pull hint retry, stop offer,
  checkpoint, and eviction work.

## Target File Layout

The refactor should prefer moving and renaming functions over changing logic.

```text
pkg/channelv2/reactor/
  FLOW.md
    Package-local map of event domains and leader/follower interaction flows.

  event.go
    Event and EventKind.
    Domain comments for mailbox events.

  reactor.go
    Reactor loop, Submit, handle top-level dispatch, control handlers.

  leader_replication.go
    handleLeaderPull
    handleLeaderAck
    handleStoreReadLogResult
    completeLeaderPull
    updateLeaderPullFollowerState
    leader pull cache helpers

  follower_replication.go
    tickFollowerReplication
    handleFollowerPullHint
    handleLegacyFollowerNotify
    handleRPCPullResult
    handleStoreApplyResult
    handleRPCAckResult
    follower pull/apply/ack/stop scheduling helpers

  leader_lifecycle_runtime.go
    tickLeaderLifecycle
    submitLeaderEvictReady
    handleLeaderEvictReady
    trySubmitPullHint
    handleRPCPullHintResult
    leader idle eviction and pull-hint retry helpers

  worker_completion.go
    handleWorkerResult and worker task kind routing.
```

Existing model files remain focused:

```text
lifecycle_model.go
lifecycle_leader.go
lifecycle_follower.go
lifecycle_apply.go
replication_state.go
runtime_snapshot.go
scheduler.go
```

## Leader-Side Flow

### Follower Pull RPC

```text
transport.HandlePull
  -> Group.Submit(EventPull)
  -> handleLeaderPull
    -> validate leader role, channel id, epoch, leader epoch, follower
    -> update leader-visible follower pull state
    -> recent cache hit: completeLeaderPull
    -> cache miss: submitStoreReadLog
  -> handleStoreReadLogResult
  -> completeLeaderPull
```

`completeLeaderPull` remains responsible for:

- returning records or an empty response,
- setting leader HW and LEO in the response,
- pacing caught-up followers with `NextPullAfter`,
- returning `PullControlStop` only when idle eviction guards pass,
- marking `StopOffered` for the follower when stop control is returned,
- scheduling leader lifecycle work after the response.

### Follower ACK RPC

```text
transport.HandleAck
  -> Group.Submit(EventAck)
  -> handleLeaderAck
    -> validate leader role and metadata fence
    -> normal ACK: ApplyFollowerAck and maybe complete quorum waiters
    -> stopped ACK: validate activity version and LEO match
    -> mark follower stopped
    -> maybe schedule or continue leader eviction
```

Stopped ACK validation stays strict:

- channel key, epoch, and leader epoch must match,
- follower must be a current replica,
- activity version must match current leader activity,
- stopped match offset must equal leader LEO,
- offered stop version must match if a stop was explicitly offered.

### Leader Idle Lifecycle

```text
dueLifecycle / EventLeaderEvictReady
  -> tickLeaderLifecycle
    -> retry pull hints for lagging inactive followers
    -> maybe offer stop through future empty pull responses
    -> after all followers stopped: submit checkpoint
    -> checkpoint completion queues normal-priority final recheck
    -> final recheck fences concurrent append submission
    -> evict runtime when still safe
```

Append submission fencing remains unchanged. The final eviction recheck still
uses append reservation count and append submit sequence to avoid evicting a
leader runtime while an append is between state verification and mailbox
submission.

## Follower-Side Flow

### Pull Hint Or Legacy Notify

```text
transport.HandlePullHint / HandleNotify
  -> Group.Submit(EventPullHint or EventNotify)
  -> handleFollowerPullHint / handleLegacyFollowerNotify
    -> validate follower role and metadata fence
    -> ignore stale activity versions
    -> cancel obsolete stop state if newer activity arrived
    -> record hinted leader LEO
    -> mark follower dirty
    -> tickFollowerReplication
```

`Notify` remains a compatibility path and should be described as a legacy form
of follower wakeup, not as a separate replication protocol.

### Follower Replication Tick

```text
tickFollowerReplication
  -> pending stopped ACK retry first
  -> pending ACK retry before new pull
  -> pending pull apply before new pull
  -> stop checkpoint path if stop was accepted
  -> honor pull/apply/ack/checkpoint backoff
  -> submit RPC pull when eligible
```

The tick order is part of the contract. It prevents pulling newer data while an
older ACK or pulled response is still unresolved.

### Pull Result

```text
handleRPCPullResult
  -> ignore stale fence
  -> error: pull backoff
  -> empty response:
       update HW up to local LEO
       retry quickly if leader LEO or hinted LEO is ahead
       otherwise park by leader-provided delay
  -> records:
       retain one pending pull
       submit store apply when ACK lane is clear
  -> stop control:
       ask follower lifecycle model
       checkpoint then send stopped ACK if caught up
       otherwise continue ordinary replication
```

### Apply And ACK Results

```text
handleStoreApplyResult
  -> ignore stale fence
  -> error or missing result: keep pending pull and back off
  -> success:
       update local LEO/HW
       clear pending pull
       submit ACK for applied match

handleRPCAckResult
  -> ignore stale fence
  -> stopped ACK stale meta: cancel stop and resume pulling
  -> error: keep exact pending ACK payload and back off
  -> stopped ACK success: evict follower runtime when safe
  -> normal ACK success: continue pending apply or next pull
```

## Worker Completion Routing

`EventWorkerResult` should be routed in one place by worker task kind:

```text
TaskStoreAppend      -> append completion
TaskStoreReadLog     -> leader pull completion
TaskStoreCheckpoint  -> leader checkpoint or follower stop checkpoint
TaskRPCPull          -> follower pull completion
TaskStoreApply       -> follower apply completion
TaskRPCAck           -> follower ACK completion
TaskRPCPullHint      -> leader pull-hint completion
```

The routing function should not contain flow logic. It only names the
completion domain and calls the appropriate handler.

## Performance Constraints

The implementation must not add runtime cost on the hot paths:

- Keep `Event` as a concrete mailbox envelope.
- Do not replace `Event` payloads with `interface{}` or heap-allocated command
  objects.
- Do not add channels between event domains.
- Do not add goroutines for leader or follower subreactors.
- Do not change mailbox priorities.
- Do not scan all loaded channels for lifecycle or replication work.
- Keep worker-pool submissions and fenced completions as they are.

Most changes should be function moves, function renames, comments, and local
dispatch reshaping. Any helper added to clarify a guard must avoid allocation
and stay near the existing logic.

## Error Handling

Error behavior should remain unchanged:

- Stale metadata fences continue returning or recording `ErrStaleMeta` where
  they do today.
- Missing worker payloads continue mapping to `ErrInvalidConfig`.
- Backpressure keeps the current retry behavior for follower pull, apply, ACK,
  checkpoint, append flush, and pull hints.
- Canceled append and pull waiters continue to be swept by the reactor.
- Late worker completions continue to be ignored by generation, epoch,
  leader-epoch, and operation-id fences.

## Testing Strategy

The first implementation pass should rely heavily on existing tests because the
intended behavior is unchanged:

```text
go test ./pkg/channelv2/reactor
```

Add focused tests only where the new boundaries need direct protection:

- `handleLeaderPull` makes the local leader role explicit and still serves
  cache hits without store reads.
- `handleLeaderAck` keeps stopped ACK validation unchanged.
- `handleFollowerPullHint` interrupts a parked follower and cancels obsolete
  stop state on newer activity.
- `tickFollowerReplication` preserves ACK-before-apply-before-pull ordering.
- Worker completion routing sends each task kind to the expected domain
  handler.

Avoid broad test rewrites. Existing tests should keep proving the behavior; new
tests should document the new names and boundaries.

## Implementation Notes

Suggested order:

1. Add `pkg/channelv2/reactor/FLOW.md` with the event-domain map and the
   leader/follower flows from this design.
2. Add event-domain comments to `event.go`.
3. Move leader pull, leader ACK, and store read-log completion helpers into
   `leader_replication.go`.
4. Move follower pull-hint, notify, replication tick, pull result, apply
   result, ACK result, and follower stop helpers into `follower_replication.go`.
5. Move leader lifecycle runtime and pull-hint retry helpers into
   `leader_lifecycle_runtime.go`.
6. Move worker result routing into `worker_completion.go`.
7. Rename top-level handlers to local-role names and update tests.
8. Run `go test ./pkg/channelv2/reactor`.

The implementation should be reviewed as a readability refactor. Any behavior
change found during the move should be isolated, explained, and tested instead
of hidden inside file movement.
