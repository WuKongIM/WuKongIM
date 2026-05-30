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

## Lifecycle Controller

Each loaded runtime has exactly one `channelRuntimeLifecycle`, written by the
owning reactor. It owns lifecycle stage, activity version, idle time, leader
checkpoint effect, follower stop checkpoint effect, stopped ACK effect, leader
final eviction recheck, leader-visible follower stop state, and pull-hint
inflight bookkeeping. The controller receives small lifecycle events and returns
reactor-owned actions; store and RPC work still goes through worker pools.

Follower hot-path replication remains separate in `replicationState`: pull,
apply, park delay, backoff, and leader hints are not stored
as lifecycle phases. Lifecycle guards read these transient fields through
`RuntimeView.PendingWork` so stop control and eviction are blocked while hot-path
work is pending.

Leader idle eviction flows as:

```text
idle expired and no pending work
  -> offer PullControlStop to caught-up followers
  -> accept stopped ACKs for the current activity version
  -> checkpoint leader store state
  -> queue normal-priority final recheck
  -> evict only after append reservations, submit sequence, pending work, HW,
     LEO, and stopped-follower guards still pass
```

Follower stop flows as:

```text
PullControlStop accepted after local LEO/HW caught up
  -> checkpoint local store state
  -> send stopped ACK for the accepted activity version
  -> evict local runtime after the ACK succeeds
```

Every asynchronous lifecycle completion is fenced by channel key, generation,
epoch, leader epoch, and op id before it can advance controller state. Metadata
fences and newer activity reset stale lifecycle effects, so delayed worker
results cannot stop or evict a newer runtime incarnation.

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
`PullControlStop` when idle eviction guards pass. Leader-visible follower state
used by these guards lives under `channelRuntimeLifecycle`.

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
RPC and must match the current activity version and leader LEO. Accepted stopped
ACK state, including zero-version stopped ACKs, is recorded in
`channelRuntimeLifecycle`.

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
  -> apply a pending pull before new pulls
  -> checkpoint and send stopped ACK after accepted stop control
  -> honor retry backoff and leader-provided park delay
  -> submit RPC Pull with AckOffset when eligible
```

The follower keeps at most one pull RPC, one pending pull response, one store
apply, and one stopped ACK RPC in flight. Ordinary durable progress after store
apply schedules the next pull immediately so the follower's latest local LEO is
piggybacked as `AckOffset`.

Follower replication stage metrics use `wukongim_channelv2_replication_stage_duration_seconds`
with low-cardinality `stage/result` labels. `follower_pull_hint_to_submit`
measures accepted PullHint wakeup through pull RPC submission,
`follower_pull_rpc` measures pull RPC submission through accepted result,
`follower_store_apply` measures follower store-apply submission through result,
and `follower_apply_to_ack_return` measures successful apply through the return
of the next pull carrying the new `AckOffset`.

Leader PullHint result counters use `wukongim_channelv2_pull_hint_total` with
low-cardinality `reason/result/error` labels. `submitted` counts accepted worker
submissions, `ok` counts completed PullHint RPCs, and `err` is classified by
stable error classes such as `stale_meta`, `channel_not_found`, `not_ready`,
`canceled`, `timeout`, and `other`.

When a follower observes an empty pull response and both `LeaderLEO` and the
latest hinted leader LEO are covered by local LEO, it enters parked state.
Parked followers do not schedule ordinary idle pulls. A valid PullHint clears the
parked state and schedules immediate pull; otherwise a deterministic jittered
recovery probe runs at the configured low-frequency interval.

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

Append waiter metrics split the admitted future after service submission:
`store_append_wait` measures append flush submission through fenced durable store
completion, and `post_store_commit_wait` measures durable store completion
through local/quorum waiter completion. For quorum appends, additional low-cardinality
sub-stages split the post-store wait: `quorum_follower_pull_wait` observes when
the leader first serves records covering the append target to a follower,
`quorum_ack_offset_wait` observes when the leader receives an `AckOffset`
covering the target, `quorum_hw_advance_wait` observes HW covering the target,
and `quorum_final_complete_wait` observes HW coverage through future completion.

## Bench Runtime Events

Runtime snapshot, probe, and evict requests enter reactors as mailbox events.
The owning reactor reads or evicts its local `channels` map, preserving the
single-writer rule. Evict only removes loaded runtime state when
`safeToEvictRuntime()` is true; it never deletes durable channel metadata or
messages.
