# pkg/channel/reactor Flow

## Purpose

`pkg/channel/reactor` owns channel-keyed runtime state for the channel runtime. A single
reactor goroutine is the writer for each loaded channel runtime. Blocking store
and RPC work leaves the reactor through bounded worker pools and returns as
fenced worker completions.
Started reactors also open/load channel stores and close detached store handles
through worker tasks, so metadata activation and runtime eviction do not wait on
store I/O inside the reactor loop.
Store append and follower apply pools default to twice the reactor count, capped
at 128 workers, because quorum traffic produces both leader appends and follower
apply work while the hosted message DB still has one shared commit coordinator.
Read and RPC pools default to the reactor count unless explicitly configured.
Production composition roots may still cap store append/apply worker counts to
reduce pressure on the shared message DB commit coordinator; this only limits
blocking task concurrency and does not relax store sync, quorum progress, or
waiter fencing.

The package keeps single-node deployments under the same cluster semantics: a
single node is a single-node cluster, not a bypass around replication logic.
Append admission converts client-visible `Message` values into durable
`Record` values while preserving conversation display fields:
`FromUID`, `ClientMsgNo`, payload, `ServerTimestampMS`, and the legacy setting
bitset. Direct channel append callers that omit `ServerTimestampMS` receive the
reactor admission time.

## Event Domains

The mailbox uses one concrete `Event` envelope for performance. Handlers are
organized by the local node's role in the interaction.

```text
Control
  EventApplyMeta
  EventCheckState
  EventLookupCommittedMessage
  EventRetentionView
  EventApplyRetentionBoundary
  EventRuntimeProbe
  EventDrainChannel
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
  duePendingMeta
  dueColdActivation
```

The loop drains mailbox events in priority order and runs ready due work after
each drained batch as well as while idle. This keeps append flushes, follower
replication retries, lifecycle checks, pending-meta deadlines, and fixed cold
activation deadlines from waiting for the mailbox to become completely empty
during sustained load.

`EventRuntimeProbe` and `EventDrainChannel` are read-only migration/diagnostic
control events. Probe copies only bounded scalar proof fields from
`ChannelState`; drain checks the expected leader epoch and durable write-fence
version, then reports drained only when no machine append is in flight, no
pending append waiters remain, and HW covers LEO. Neither event mutates channel
state or performs store/RPC work.

Append queues flush by record count, payload bytes, or the oldest request's
wait deadline. `AppendBatchAdaptiveFlush` keeps the default behavior off, but
when enabled a cold queue's first request uses `AppendBatchColdMaxWait` if it is
shorter than `AppendBatchMaxWait`, so low-volume channels can flush without
waiting for the full batch window. After a reactor flushes, store-append worker
coalescing can still add a short cross-channel batch wait; `StoreAppendBatchMaxWait`
overrides that worker-side wait while zero keeps the worker default.

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
Leader-side `EventPull` uses the high-priority mailbox so quorum follower pulls
can still reach the leader while foreground append submissions are pressuring
the normal-priority queue.
With the optional leader Pull observer enabled, `Group.Submit` captures the
EventPull admission timestamp in the event's existing time slot. The reactor
reports admission-to-handler `mailbox_wait`, synchronous `handler`, AckOffset
`ack_apply`, and the number of append caller futures completed by the AckOffset.
Mailbox wait also includes earlier high-priority handlers, per-event cancellation
sweeps, per-drain due work, and observer callbacks; it is not a mailbox-capacity
or rejection metric. A cache-miss handler ends after it submits StoreReadLog, so
the synchronous handler stage excludes the later worker wait, store read, worker
completion, and Pull Future publication. Observer implementations may request a
deterministic sampling interval; the app metrics adapter uses one of every
sixteen Pull op IDs while tests and observers without a sampling capability see
every event.

```text
remote follower Ack RPC
  -> Group.Submit(EventAck)
  -> handleLeaderAck
  -> ApplyFollowerAck
  -> legacy ordinary progress ACK: complete quorum append waiters when HW advances
  -> stopped ACK: record stopped follower state and schedule leader lifecycle
```

Ordinary follower progress is accepted from `PullRequest.AckOffset` before the
leader serves the requested range. The leader still accepts standalone ordinary
progress ACKs for transport compatibility when `MatchOffset` does not exceed
the leader LEO, but the follower hot path no longer sends them. Stopped ACKs
must stay on the standalone ACK RPC and must match the current activity version
and leader LEO. Accepted stopped ACK state, including zero-version stopped ACKs,
is recorded in `channelRuntimeLifecycle`.

## Follower-Side Replication

```text
leader PullHint or legacy Notify
  -> Group.Submit(EventPullHint or EventNotify)
  -> handleFollowerPullHint or handleLegacyFollowerNotify
  -> loaded matching follower: mark dirty and tickFollowerReplication
  -> unloaded: bounded ColdActivation authority resolve, then isolated store load
  -> loaded newer fence: submit isolated authoritative MetaResolve
```

```text
tickFollowerReplication
  -> apply a pending pull before new pulls
  -> persist a due coalesced committed-HW checkpoint
  -> checkpoint and send stopped ACK after accepted stop control
  -> honor retry backoff and leader-provided park delay
  -> submit RPC Pull with AckOffset when eligible
```

The follower keeps at most one pull RPC, one pending pull response, one store
apply, and one stopped ACK RPC in flight. Ordinary durable progress after store
apply directly drives the next pull instead of waiting for the due scheduler.
Record-bearing applies advance the covered leader HW atomically with the rows
while preserving existing checkpoint epoch/log-start fields, and the worker
result returns the durable frontier covered by that apply to the reactor. If a later empty
pull advances HW after those rows were applied, the follower schedules a fenced
monotonic checkpoint after `CommittedCheckpointInterval` (one second by
default). Successive empty-pull advances replace that pending frontier, while a
later record-bearing apply can satisfy it atomically. The final learned frontier
is therefore persisted after the bounded idle window without issuing one
standalone checkpoint commit per message on high-cardinality traffic.
The pull piggybacks the follower's latest local LEO as `AckOffset`, so hot-path
progress return does not depend on standalone ACK RPC delivery.
RPC worker dispatchers may batch same-target `TaskRPCPull` and
`TaskRPCPullHint` items across different channels before an ants-backed worker
executes the transport call. If one collected window contains multiple target
nodes, those target-specific subgroups may execute independently; a pool-wide
slot budget still caps actual Pull/PullHint calls at the configured RPC worker
count. The reactor submits and receives one fenced worker task per channel, so
batching and subgroup scheduling do not change per-channel lifecycle or
replication state.
Store worker dispatchers may batch queued `TaskStoreAppend`, `TaskStoreApply`,
or `TaskStoreCheckpoint` items across different channels when the store factory
supports the corresponding leader-append, follower-apply, or checkpoint
batching contract. Checkpoint tasks use a dedicated worker pool so checkpoint
fsync latency cannot consume follower-apply capacity. The ants executor only
runs prepared blocking groups; it is not the source of worker backpressure.
Retention apply uses the store-apply worker pool shared with follower apply.
The reactor publishes the logical `RetentionThroughSeq` before
submitting the worker task, but physical deletion is allowed only when the
requested boundary is locally covered by HW, checkpoint HW, LEO, and leader
minimum ISR match offset. If checkpoint HW is the only missing physical trim
gate, the retention path submits a bounded retention-owned checkpoint and lets a
later GC retry perform the trim after the checkpoint result updates the runtime
view.
When the leader serves records to a follower that is still behind, it schedules a
bounded `PullHintReasonResume` retry. Normal lifecycle ticks can send that
resume hint, and the append hot path opportunistically sends it when the retry is
already due, so hot mailboxes do not starve follower wakeups. If a quorum append
waiter remains pending after the leader's durable append and a follower is still
behind without an inflight or scheduled hint, the leader schedules the same
bounded resume retry even when that follower recently pulled. If a pending
quorum waiter is still blocked and the follower has not pulled for at least the
hint retry interval, the append path moves any future resume retry to immediate
due so stale follower silence is retried without waiting for the caller timeout.
The due check is still rate-limited by the most recent Pull or PullHint time, so
successful wakeup delivery cannot be bypassed by every subsequent append while
the follower is still silent.
A later `AckOffset` or leader-observed follower progress that catches up to the
leader LEO retires the hint state; merely observing a follower Pull does not
retire it while the follower remains behind. The append-cancellation sweep also
nudges overdue resume hints for channels with pending quorum waiters, so a
delayed due tick cannot leave an already scheduled follower wakeup dormant until
the caller timeout. This cancellation-driven wakeup is rate-limited per channel,
and global append-cancellation scans are rate-limited during hot mailboxes; the
direct per-channel sweep before append flush is still immediate so canceled
queued appends are not flushed because of the global scan throttle.
If a caught-up follower receives another matching PullHint while it still has a
locally durable `ackReturnOffset` that the leader has not observed, it submits
one Pull carrying that AckOffset instead of treating the hint as a pure no-op.
This wakeup only returns already-applied follower progress; HW advancement and
append completion still go through the leader's normal quorum checks.

Follower replication stage metrics use `wukongim_channelv2_replication_stage_duration_seconds`
with low-cardinality `stage/result` labels. `follower_pull_hint_to_submit`
measures accepted PullHint wakeup through pull RPC submission,
`follower_pull_rpc` measures pull RPC submission through accepted result,
`follower_need_meta_pull_rpc` measures compatibility `Pull{NeedMeta=true}` paths
without mixing them into ordinary follower pull latency,
`follower_store_apply` measures follower store-apply submission through result,
and `follower_apply_to_ack_return` measures successful apply through the first
successful progress return, either the standalone progress ACK or the fallback
pull carrying the new `AckOffset`.

Leader PullHint result counters use `wukongim_channelv2_pull_hint_total` with
low-cardinality `reason/result/error` labels. `submitted` counts accepted worker
submissions, `ok` counts completed PullHint RPCs, and `err` is classified by
stable error classes such as `stale_meta`, `channel_not_found`, `not_ready`,
`canceled`, `timeout`, `remote_error`, and `other`.
Successful PullHint RPC completion means the follower wakeup was delivered, not
that the follower has durably pulled the leader LEO. While the follower match is
still behind, the leader keeps a bounded resume retry scheduled; ordinary
follower pull progress or an `AckOffset` that reaches the leader LEO retires the
hint. This only repeats wakeups and never advances HW or completes append
futures without the existing quorum progress checks.
Append waiter cancellation snapshots include leader-visible follower state,
including `last_hint_ms`, to distinguish stale follower pulls from recently
delivered wakeups that are still waiting for the next bounded retry.

Leader PullHint requests carry only the channel key, channel ID, epoch,
leader epoch, leader, leader LEO, activity version, and reason. If the follower
does not already have runtime state, the owning reactor creates a lightweight
ColdActivation shell and submits `TaskColdMetaResolve` to a dedicated bounded
pool. The authoritative result, not the hint envelope, must prove key, ID,
active topology, leader membership, and local replica membership before
`TaskColdStoreLoad` opens storage in that same pool. Successful activation uses
ordinary follower Pull from the resolved leader; no NeedMeta RPC is required.

A loaded runtime treats a strictly newer PullHint only as a refresh trigger.
It keeps the existing runtime and submits one `TaskMetaResolve` to a dedicated
small bounded pool backed by the authoritative read-only MetaResolver. All
valid newer hints coalesce within one fixed, non-renewable admission lease;
their claimed target fence and leader are never trusted. A fenced completion
may apply only an active, structurally valid, local-replica metadata record with
the same identity and a fence strictly newer than the exact base runtime. The
result may be an intermediate committed version when the local Slot read lags,
but metadata application remains monotonic. Success schedules normal
`NeedMeta=false` replication from the resolved leader; resolver failure,
timeout, invalid topology, or backpressure leaves the runtime and waiters
unchanged. An explicit ApplyMeta that supersedes the captured base, eviction,
and close cancel and fence stale resolver completions; a compatible same-fence
authority-field refresh keeps the fixed admission lease intact.

ColdActivation is not a loaded runtime. `EventCheckState` reports it as missing,
and runtime eviction or close cancels its shared resolve/load context. PullHints
for the same channel coalesce without extending the fixed activation deadline.
The CPU-aware pool defaults to 4-64 workers and 64 queue slots per worker,
bounded to 256-4096 queued tasks. Its complete deadline is five configured
PullHint retry intervals clamped to 100ms-5s. It stays separate from both the
loaded-runtime MetaResolve pool and hot StoreRead/RPC pools. Low-cardinality
worker pool, wait, task, and admission metrics expose both cold stages;
activation-rejected metrics classify asynchronous failures with bounded
`cold_*` reason labels.

When a follower observes an empty pull response and both `LeaderLEO` and the
latest hinted leader LEO are covered by local LEO, it enters parked state.
Parked followers do not schedule ordinary idle pulls. A valid PullHint clears the
parked state and schedules immediate pull when the hint exposes leader progress
or the follower still needs to return a durable AckOffset; otherwise a
deterministic jittered recovery probe runs at the configured
send-timeout-bounded interval. The runtime default is 2s plus up to 1s jitter.

## Worker Completion Routing

```text
TaskStoreAppend        -> append completion
TaskStoreLoad          -> explicit ApplyMeta activation
TaskColdMetaResolve    -> unloaded authoritative metadata completion
TaskColdStoreLoad      -> authority-proven unloaded store completion
TaskStoreReadLog       -> leader pull completion
TaskStoreLookupMessage -> committed message lookup completion
TaskStoreCheckpoint    -> isolated pool for leader, follower stop, retention, or committed-HW checkpoint
TaskStoreClose         -> detached store close completion (no state mutation)
TaskStoreRetention     -> retention adoption and optional physical trim completion
TaskRPCPull            -> follower pull completion
TaskStoreApply         -> follower apply completion
TaskRPCAck             -> follower stopped ACK completion
TaskRPCPullHint        -> leader pull-hint completion
TaskMetaResolve        -> loaded newer-fence authoritative metadata completion
```

Worker completions are accepted only when their channel key, generation, epoch,
leader epoch, and operation id match the current runtime state. Store-load
results are fenced by the temporary loading shell; stale load results close the
opened store handle outside the reactor state machine.

ApplyMeta activation builds and applies metadata to a local `ChannelState`
before publishing either the state or loaded store into the runtime shell. A
failed metadata decision deletes the loading shell, releases the loaded store,
and fails all waiting futures, so it cannot consume `MaxChannels`. Synchronous
store loading owns the acquired handle until both initial and retention state
load successfully; either failure closes it and returns a nil handle.

## Shutdown Ownership

`Group.Close` first rejects new group admission. Each reactor then stops its
single-writer loop, fails loading futures and pending waiters, detaches every
published store, and drains queued events while holding the final submission
gate. A queued successful StoreLoad is detached from its worker result and its
event future still completes with `ErrClosed`. The gate is released before any
store close is submitted or executed.

Detached stores normally transfer to accepted `TaskStoreClose` work. The worker
pool owns accepted close tasks through execution or queued cancellation; a
submission failure instead enters the Group-owned fallback-close tracker, so
ordinary eviction remains non-blocking. Shutdown joins all reactors, closes the
worker pools to finish running work and finalize queued closes, then seals and
waits the fallback tracker. Late StoreLoad delivery that cannot enter a reactor
closes its store exactly once. None of these paths closes the shared store
factory or underlying Pebble database.

Append waiter metrics split the admitted future after service submission:
`store_append_wait` measures append flush submission through fenced durable store
completion, and `post_store_commit_wait` measures durable store completion
through local/quorum waiter completion. For quorum appends, additional low-cardinality
sub-stages split the post-store wait: `quorum_follower_pull_wait` observes when
the leader first serves records covering the append target to a follower,
`quorum_ack_offset_wait` observes when the leader receives an `AckOffset`
covering the target, `quorum_hw_advance_wait` observes HW covering the target,
and `quorum_final_complete_wait` observes HW coverage through future completion.
Caller cancellation after append admission emits `AppendWaitCancelSnapshot`
before removing the waiter from reactor and machine state. This diagnostic is
not a high-cardinality metric; app-level observers can log it on rare timeout
paths to preserve the exact LEO/HW/target, queue, in-flight, and quorum progress
state that normal latency histograms cannot show.

Mailbox and append queue pressure observations stay low-cardinality. Mailbox
samples use only reactor id and priority. Append queue pressure is maintained as
a per-reactor aggregate with per-channel deltas, so hot-path queue mutations do
not scan the full runtime map and do not expose channel labels.

`EventLookupCommittedMessage` is a read-only path for timeout recovery and
diagnostics. The owning reactor submits the optional store message-id index read
to the store read worker pool, then accepts the result only through the current
runtime fence. A message is returned only if its sequence is positive and
`sequence <= HW`; the path never mutates progress and cannot mark locally durable
but uncommitted rows as successful.

## Deep Sendtrace Sidecars

Reactor append tracing uses transient `appendTraceBatch` sidecars selected by
`pkg/observability/sendtrace` detail sampling. Disabled or unselected appends do
not allocate sidecars. Queue and local durable stages may lazily scan the
in-flight batch for error or slow-stage traces, bounded by `MaxItemsPerBatch`.
Quorum wait emits only for preselected sidecars so pending quorum waiters do not
retain unsampled high-cardinality candidates.

## Bench Runtime Events

Runtime snapshot, probe, and evict requests enter reactors as mailbox events.
The owning reactor reads or evicts its local `channels` map, preserving the
single-writer rule. Evict only removes loaded runtime state when
`safeToEvictRuntime()` is true; it never deletes durable channel metadata or
messages.
