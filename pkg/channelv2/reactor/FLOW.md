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
  -> apply PullRequest.AckOffset when present
  -> complete quorum append waiters when HW advances
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
remote follower stopped Ack RPC
  -> Group.Submit(EventAck)
  -> handleLeaderAck
  -> ApplyFollowerAck
  -> record stopped follower state for stopped ACKs
  -> schedule leader lifecycle
```

Ordinary follower progress is accepted from `PullRequest.AckOffset` before the
leader serves the requested range. Stopped ACKs must stay on the standalone ACK
RPC and must match the current activity version and leader LEO.

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
  -> retry pending stopped or compatibility ACK before new pulls
  -> apply a pending pull before new pulls
  -> checkpoint and send stopped ACK after accepted stop control
  -> honor retry backoff and leader-provided park delay
  -> submit RPC Pull with AckOffset when eligible
```

The follower keeps at most one pull RPC, one pending pull response, one store
apply, and one stopped or compatibility ACK RPC in flight. Ordinary durable
progress after store apply schedules the next pull immediately so the follower's
latest local LEO is piggybacked as `AckOffset`.

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
