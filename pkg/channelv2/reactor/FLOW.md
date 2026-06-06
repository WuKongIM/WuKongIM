# pkg/channelv2/reactor Flow

## Purpose

`pkg/channelv2/reactor` owns channel-keyed runtime state for channelv2. A single
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

## Event Domains

The mailbox uses one concrete `Event` envelope for performance. Handlers are
organized by the local node's role in the interaction.

```text
Control
  EventApplyMeta
  EventCheckState
  EventLookupCommittedMessage
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
```

The loop drains mailbox events in priority order and runs ready due work after
each drained batch as well as while idle. This keeps append flushes, follower
replication retries, lifecycle checks, and pending-meta deadlines from waiting
for the mailbox to become completely empty during sustained load.

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
  -> unloaded or newer fence: create PendingMeta and submit NeedMeta Pull
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
apply directly drives the next pull instead of waiting for the due scheduler.
The pull piggybacks the follower's latest local LEO as `AckOffset`, so hot-path
progress return does not depend on standalone ACK RPC delivery.
RPC worker pools may batch same-target `TaskRPCPull` and `TaskRPCPullHint`
items across different channels before handing them to transport. The reactor
still submits and receives one fenced worker task per channel, so batching does
not change per-channel lifecycle or replication state.
Store worker pools may batch queued `TaskStoreAppend` or `TaskStoreApply` items
across different channels when the store factory supports leader-append or
follower-apply batching. The reactor still observes one fenced completion per
channel; only the blocking storage boundary changes from multiple worker/store
calls into one store-level batch request.
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
`follower_need_meta_pull_rpc` measures the bootstrap `Pull{NeedMeta=true}` path
without mixing it into ordinary follower pull latency,
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
does not already have matching loaded runtime state, the owning reactor creates
a `PendingMeta` shell and submits a bounded `Pull{NeedMeta=true}` to the channel
leader. A successful NeedMeta Pull must return cloned active metadata; the
follower validates key, ID, epochs, leader, active status, and local replica
membership, applies metadata, and only then processes any records in the same
response.

`PendingMeta` is not a loaded runtime. `EventCheckState` reports it as missing,
append, leader pull, ACK, notify, snapshots, and active runtime counts do not
use it, and runtime eviction or close may release it directly. Same-fence
PullHints coalesce without extending the activation deadline; newer fences for
the same channel identity replace the pending shell; stale, invalid,
not-replica, not-ready, and retry-exhausted attempts release it so later leader
PullHints can start a fresh activation.

PendingMeta metrics stay low-cardinality and do not label channel identity:
`wukongim_channelv2_pending_meta_current{reactor_id}` reports outstanding
bootstrap shells, `wukongim_channelv2_pending_meta_total{event,error}` reports
`created`, `converted`, and `released`, and
`wukongim_channelv2_need_meta_pull_total{result,error}` reports submitted,
successful, retried, and failed NeedMeta pulls.

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
TaskStoreLoad          -> ApplyMeta activation or PendingMeta bootstrap
TaskStoreReadLog       -> leader pull completion
TaskStoreLookupMessage -> committed message lookup completion
TaskStoreCheckpoint    -> leader checkpoint or follower stop checkpoint
TaskStoreClose         -> detached store close completion (no state mutation)
TaskRPCPull            -> follower pull completion
TaskStoreApply         -> follower apply completion
TaskRPCAck             -> follower stopped ACK completion
TaskRPCPullHint        -> leader pull-hint completion
```

Worker completions are accepted only when their channel key, generation, epoch,
leader epoch, and operation id match the current runtime state. Store-load
results are fenced by the temporary loading shell; stale load results close the
opened store handle outside the reactor state machine.

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
