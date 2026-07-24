# internal/runtime/channelappend Flow

## Responsibility

`internal/runtime/channelappend` owns local channel append-authority admission.
It is entered only after routing has resolved that the local node is the current
channel append authority. The package validates SEND commands, allocates
message IDs, admits durable append work, completes item-aligned futures, and
runs best-effort post-commit recipient/conversation effects. It also handles
legacy command-style `NoPersist` sends as transient realtime delivery without
writing the channel log or conversation active state.

## Router Flow

`Router` is the channel-authority routing front door for SEND batches. Before it
resolves authority, it performs only side-effect-safe command checks and
canonical channel derivation: malformed terminal commands fail item-locally
without resolving or creating channel metadata, request-scoped sends derive the
stable temporary command channel, and person-channel sends with
`NormalizePersonChannel` derive the canonical person channel. The original send
command is still submitted unchanged so the local append authority remains the
authoritative validation, idempotency, message-id allocation, and canonical
command mutation point.
Plain non-command `NoPersist` sends are terminal successes at pre-route time
and intentionally do not resolve authority, append, or dispatch realtime
delivery. Command-style `NoPersist` sends (`SyncOnce` or an already command
channel id) route by the command-channel id so the resolved authority owns the
transient realtime enqueue.

```text
Router.SendBatch
  -> side-effect-safe pre-route validation / canonical channel derivation
  -> AuthorityResolver.ResolveAppendAuthority(canonical channel)
  -> bounded concurrent submission of independent canonical-channel groups
     -> local target: LocalSubmitter.SubmitLocal(target, original items)
     -> remote target: RemoteForwarder.ForwardSendBatch(target, original items)
  -> fold group results in original group/item order
  -> item-aligned results
```

The local path is the only path that may enter `SubmitLocal`; remote targets are
forwarded and must not create local `channelWriter` state. Route movement errors
(`ErrStaleRoute`, `ErrNotChannelAuthority`, `ErrNotLeader`,
`ErrRouteNotReady`) are retried with bounded backoff while item deadlines allow
it. Before that retry, an optional authority invalidator removes only the exact
failed `(channel, leader, epoch, leader_epoch, route_generation)` cache version, forcing the next
resolve to refresh while preserving any concurrently installed newer route.
This invalidation also happens when the current item has exhausted its own
retry budget, so later requests do not inherit a proven stale entry. Successful
results, non-route terminal errors, and caller cancellation do not invalidate.
A downstream `context.Canceled` is also retried when the item itself is
still active, covering authority-node shutdown or transport cancellation during
leader movement without hiding caller/session cancellation. Retry sleeps wake
when pending item cancellation or deadlines arrive, so expired work does not
wait for the whole backoff. Remote outbound admission is bounded per
`LeaderNodeID`, not per channel, so different channels to the same remote
authority share the same pressure limit. Resolved remote groups in one batch
are assigned to at most `MaxOutboundPerNode` batch-local lanes for that leader.
Groups in one lane submit serially, so they do not reject one another merely
because the router made independent groups concurrent. The existing global
outbound admission remains fail-fast, so occupancy from another `SendBatch`
still returns real cross-batch backpressure. When no leader exceeds the bound,
the hot path submits group indexes directly and does not allocate lane maps or
per-group lane slices.

Authority resolution is side-effect safe and uses its own fixed per-batch
concurrency bound for independent canonical channels. This prevents first-use
channel runtime metadata proposals from serially multiplying Slot round trips.
Resolved targets are folded in original channel order before submission.
After resolution, different canonical-channel groups use the separate fixed
submission concurrency bound. The caller participates as one worker in both
phases, so the router creates at most `bound-1` helper goroutines per phase. A
single canonical channel still forms one ordered group, and completed group
results are folded in original group and item order before retry selection.
This removes cross-channel head-of-line waiting without adding an unbounded
goroutine or a second cross-request queue.

Router submit contexts are neutral batch transport contexts. Per-item contexts
and deadlines are checked before route lookup, before submission, and while
waiting for retry wakeups. The single-item path derives its submit context
directly from that item context/deadline. Multi-item batches use a shared
neutral context so one item cannot cancel unrelated items; the shared context is
canceled only when every submitted item has reached its own terminal signal.
Runtime watchers use `context.AfterFunc` and timers instead of per-item goroutine
waiters. Once local or remote authority returns item-aligned results, those
results are preserved so a late deadline cannot erase a durable append success
or its committed handoff. This prevents one item context or deadline from
canceling other items that happen to share the same authority batch. When a
shared transport context is canceled only after an item's own deadline has
expired, the item result is reported as deadline-exceeded rather than a generic
cancellation so metrics keep send-timeout failures distinct from caller/session
cancellation.

## Authority-Only State

`SubmitLocal` accepts an `AuthorityTarget` that carries the channel authority
identity plus recipient fanout metadata (`Large` and
`SubscriberMutationVersion`). The group rejects targets whose `LeaderNodeID`
does not match `Options.LocalNodeID` before choosing a shard. Remote channels
are handled by `Router` through the `RemoteForwarder` port and never create
proxy `channelWriter` state inside this package.

`channelWriter` is created lazily by the owning shard after local authority
validation. State is keyed by the channel routing key derived from
`AuthorityTarget.ChannelKey` or from `ChannelID` when the target omits an
explicit key. Shard selection uses the same channel key, not the sender UID.
When later submissions carry a newer subscriber mutation version or a changed
large-channel flag, the writer invalidates only its recipient snapshot cache;
when they carry a newer authority target for the same channel key, the writer
also advances the cached leader and epoch fences without downgrading older
observations. This keeps long-lived local writers aligned with Channel
membership or leader-epoch changes after migration while preserving the same
single-writer ordering for the channel key.

## Lifecycle

`Group.Start` opens local admission and prepares isolated worker pools. Writers
are created lazily on accepted local submissions and reclaimed opportunistically
after they have stayed fully idle past `WriterIdleRetention`. `Group.Stop`
closes admission and starts one background graceful drain. The drain preserves
accepted futures, durable append state, global post-commit handoff reservations,
and fair retry ownership until they finish; only then does it release worker
pools and cancel the runtime context. A caller context bounds only that caller's
wait. A timed-out Stop does not cancel or clear work, and a later Stop continues
waiting for the same drain. A stopped group is not restarted.

The advance, append, and post-commit ants pools register actual created worker
and maintenance goroutines under `channelappend/worker_pool`. The scheduler,
retry loop, delivery workers, short fan-out helpers, pool release, and
background stop drain use fixed `pkg/goroutine` task IDs, without changing the
single-writer or shutdown ordering described below.
Pool panic policy is applied through that owner, and pool ownership remains
registered after release until every executing worker has returned.

## Writer Execution

The runtime implements local authority validation, hash-sharded writer lookup,
lifecycle, pre-append preparation, channel-level append flow control, durable
append scheduling, committed-message handoff, and item-aligned futures.
Submission checks caller cancellation before admission. After that check,
bounded local admission is shard-local and non-blocking. It returns
`ErrRouteNotReady` when the group is not accepting writes because it has not
started, is stopping, or has stopped, and returns `ErrBackpressured` only when
the target shard's outstanding accepted work is at capacity. Once a submit event is accepted into a writer,
caller cancellation no longer turns the accepted event into a rejected submit.

Each active channel has one `channelWriter`. The writer owns one
`channelState`, a lightweight inbox, and an atomic scheduled flag. The scheduled
flag guarantees that at most one goroutine advances that channel at a time. The
group uses shards only for writer lookup and creation; shard locks are not held
while preparing, appending, or committing messages.

Writer activation first enters one group-local dispatcher queue. Only that
dispatcher may perform a blocking submit to the bounded advance worker pool;
`SubmitLocal`, append completions, and post-commit completions never wait for an
advance worker. The scheduled flag de-duplicates each writer to at most one
queued or running activation, so dispatcher memory is bounded by active writer
state rather than SEND rate. This breaks the cross-pool cycle where every
advance worker could wait for the append pool while every append completion
waited to resubmit into the advance pool. The configured `AdvancePoolSize`
still bounds concurrent writer state-machine execution.

Accepted submit events enter a lightweight per-writer inbox. A scheduled writer
may wait for the tiny runtime-only `InboxCoalesceWindow` before draining a
small inbox, stopping earlier when the inbox reaches `InboxCoalesceMaxItems`
logical send items. This wait runs only in the already scheduled writer
goroutine, never in `SubmitLocal`, and never while holding `channelWriter.mu`.
Its purpose is to merge near-simultaneous same-channel submissions into larger
append batches without changing local authority ownership or durable ordering.

After the wait, the writer briefly locks to detach the current inbox, prepares
those accepted batches outside `channelWriter.mu`, and re-locks only to admit
the prepared outcomes into `channelState`. Rejected and idempotent items
complete their item-aligned future slots immediately with their
reason/error/result. Valid prepared items receive one message id and one server
timestamp. Before a prepared item can enter the pending queue, its canonical
prepared channel must still match the submitted `AuthorityTarget`;
request-scoped derivation or person-channel normalization that changes the
channel away from the target returns `ErrStaleRoute` for that item and creates
no state. Idempotency hits use the same canonical target validation before
their stored result can complete successfully, so stale routes cannot bypass
authority ownership by returning an older successful result. Matching prepared
items enter the writer state's pending queue in input order only when the
state's pending plus in-flight item count is below
`ChannelBacklogHighWatermark`; saturated channels complete those items with
`ErrChannelBusy` before they reach the append port. When any post-commit port is
configured, every append-bound prepared item must also reserve one slot from
the group-wide `PostCommitHandoffCapacity` before append. The reservation spans
pending append, append execution, and durable post-commit work. An item that
cannot reserve a slot completes with `ErrChannelBusy` and is never passed to
`Appender`; append failure or terminal post-commit completion returns the slot.
The default is the
larger of `ChannelBacklogHighWatermark` and
`EffectPoolSize * commitBatchMaxEvents`.
Both worker-pool sizes are capped at the maximum positive int32 capacity used by
ants/v2, preventing oversized configuration values from wrapping into an
unbounded or permanently full pool.
Prepared command-style `NoPersist` items also receive one message id and server
timestamp, but they do not enter the durable pending queue. The writer schedules
them as realtime effects and completes their future only after the recipient
delivery queue accepts the transient envelope or returns an error. If no
recipient delivery enqueuer is configured, the transient send fails instead of
reporting a success that cannot be delivered.

The writer builds channel-aligned append batches from prepared pending items
and keeps up to `AppendInflightBatchesPerChannel` append batches in flight per channel.
Blocking `Appender.AppendBatch` calls run on the foreground append pool and wake the
same writer when they complete. Completion events are drained by append sequence
before mutating `channelState`, so SENDACK and post-commit handoff remain in
submission order even when same-channel append calls finish out of order. A
later completion waiting on an earlier sequence gap remains pending but does
not reactivate the writer, because only the missing completion callback can
make that ordered drain runnable. The appender port must preserve durable
per-channel append order for concurrent same-channel requests or serialize the
requests internally. Append requests
borrow immutable send-path payloads and carry the resolved authority epoch and
leader epoch as append fences; concrete storage adapters clone payloads when
they cross into durable ownership.

Batch-level append errors are returned to all active items from that single
append attempt without retry. When an unexpected append failure races with a
previous durable commit for the same sender/client/channel key, the writer may
perform one payload-hash-checked idempotency lookup and complete that item from
the existing committed result. Recovered idempotency hits do not enqueue
post-commit side effects because they are not new commits from this append
attempt. Short append results complete missing items with
`ErrAppendResultMissing`; per-item append errors map to SENDACK reasons;
successful append results complete `SENDACK` futures immediately with
`ReasonSuccess`, message id, and channel sequence. Newly successful append items also
enqueue `CommittedEnvelope` values in the same `channelState` as the handoff
point for best-effort post-commit recipient work. Post-commit side effects are
not checkpointed and not replayed after authority restart.
The append payload carries the `SyncOnce` command marker through the durable
channel appender port so CMD sync readers and ordinary conversation readers can
separate command messages from normal channel messages without guessing from
channel names or client message numbers.

Realtime `NoPersist` effects reuse the same recipient authority grouping and
delivery enqueue machinery as post-commit work, but they explicitly skip
`conversationactive.ActiveBatch` admission. Request-scoped realtime sends keep
using `MessageScopedUIDs`, so they bypass subscriber scans just like durable
request-scoped commits.

Post-commit work is scheduled from the writer state after durable append
succeeds and is independent from `SENDACK` completion. A committed envelope
remains pending only until one recipient delivery enqueue attempt completes.
The committed envelope owns one payload copy when it enters the async
post-commit backlog; later state and recipient dispatch steps pass that
immutable envelope by reference through the delivery worker and owner-push
planning. Concrete owner push adapters copy or serialize at their boundary.
Success prunes the payload-bearing envelope from the backlog. Recipient route or
delivery enqueue failure is logged through `PostCommitFailureObserver`, counted
through effect metrics, and then the envelope reaches its terminal attempt
without retry so one bad recipient side effect cannot block later messages on
the same channel.
Conversation-active projection failures are recorded independently and do not
turn a successful delivery enqueue into a failed item completion. Failure
observations carry a precise post-commit phase plus
sampled recipient, target, and dispatch context so route-resolution failures
can be distinguished from conversation active admission and delivery enqueue
failures in logs. Each channel keeps only one committed envelope in flight at a
time, and concurrency inside that envelope is limited to recipient authority
targets. The global pre-append reservation is the post-commit backlog bound, so
every newly acknowledged durable envelope already owns capacity for its FIFO
handoff. A full handoff rejects only the not-yet-appended item with
`ErrChannelBusy`; an already-durable envelope is never dropped at scheduler
admission.

Post-commit pool admission remains non-blocking. If the pool is full, the writer
cancels only transient dispatch ownership, keeps the committed envelopes in
`channelState`, and enters a de-duplicated global FIFO retry scheduler. While
that FIFO is contended, newer writers join behind it instead of probing the pool
directly. A queued writer may prepare newly received inbox work, but it parks
that append work until its older durable commit owns the retry turn. The selected
writer's pass admits only the old commit; normal append-first ordering resumes
on its next pass. The scheduler consumes a turn only when the pool reports free
logical task capacity. Task completion releases that logical reservation before
publishing the capacity wakeup, so coalesced wakeups refill every free worker
without depending on ants' idle-worker count. If ants is still returning the
just-completed worker to its idle queue, the logical capacity owner completes
that short handoff rather than reporting a false overload. A real overload
returns the writer to the FIFO tail and waits for the next capacity signal or
bounded timer tick. Queue compaction clears truncated writer pointers from the
backing array. This preserves fair retry without a busy scheduler spin, stale
writer retention, or terminal scheduler drop, while each admitted effect still
processes at most `commitBatchMaxEvents` envelopes.
Realtime work shares the effect pool but does not overtake a contended durable
retry FIFO; it fails fast with backpressure while an older durable writer owns
or awaits the retry turn.

When a PersistAfter enqueuer is configured, each successful durable committed
envelope is cloned into the configured side-effect sinks from the same
authority-local post-commit point. The enqueuer may represent plugin hooks,
webhook delivery, or both. It receives only committed envelopes after durable
append succeeds, remains best-effort, and does not affect SENDACK, append
success, recipient delivery, or conversation active projection. Transient
NoPersist realtime sends skip PersistAfter because they do not create durable
committed envelopes.

When an offline recipient observer is configured, recipient delivery resolves
presence first, accumulates the unique recipient UIDs with no online route for
one durable ordinary envelope, and reports them through the batch observer when
available. It falls back to the legacy per-UID observer only when the batch path
is not configured. This observer runs before sender echo suppression so a
sender's other online sessions do not hide unrelated offline recipients. It is
limited to durable ordinary commits: zero-sequence realtime envelopes, SyncOnce
command messages, and request-scoped `MessageScopedUIDs` batches are skipped.
Batch offline observation avoids per-recipient webhook or plugin-worker queue
admission for large fanout. The observer receives one shared committed payload
plus the ordered UID slice; an asynchronous adapter takes ownership once for the
whole batch instead of cloning the payload for every UID. Callers should chunk
large batches at the observer boundary if needed.
The observer is a best-effort side-effect boundary and must not influence
SENDACK, append success, conversation active admission, or owner push delivery.

Scoped `MessageScopedUIDs` dispatch directly without scanning subscribers.
Person channels derive exactly the two canonical participants from the
committed channel id. Large channels page subscribers with the configured page
size and dispatch each page before requesting the next one, so a large-channel
effect never loads the full subscriber set into memory. Non-large channels load
the full subscriber snapshot once for the current `SubscriberMutationVersion`
and cache it in `channelState`; later committed messages reuse that cache until
a new target version invalidates it. External subscriber metadata mutations may
also call `Group.ApplySubscriberMutation`; the group applies that mutation
directly to the owning writer state so non-large cached snapshots stay aligned
with API mutations. Large updates clear the cached snapshot, reset updates
replace the cached non-large snapshot, and add/remove updates patch an
already-ready non-large snapshot while advancing the cached mutation version. A
non-terminal subscriber page in the large-channel path must return a non-empty
cursor different from the previous cursor; otherwise dispatch fails before that
page's recipients are admitted or dispatched, avoiding partial side effects for
the invalid page before the envelope is dropped.

After each recipient set is formed, channelappend resolves one item-aligned
authority snapshot for the sender plus every unique trimmed recipient. Delivery
rows retain integer indexes into that snapshot, while the conversation-active
first attempt reuses the same exact-target groups; neither path rebuilds a
`map[string]Target` or performs a second route lookup. Group identity includes
the complete physical hash-slot fence, so the 256 hash slots remain distinct
even when they map onto only 10 logical Slot Raft Groups. Channelappend then
enqueues bounded recipient delivery plans and admits one independent
`conversationactive.ActiveBatch` projection.
The normal bounded page uses an allocation-free inline UID index to coalesce the
sender and duplicate recipients while preserving duplicate delivery rows. It
borrows an already-normalized recipient slice and copies only when empty or
whitespace-normalized UIDs require compaction. Exact-target grouping uses a
fixed 256-entry physical-hash-slot index with exact-target comparison; custom
slot counts or transition collisions fall back to a bounded exact scan rather
than weakening leader-term, config-epoch, or route-revision fences.
The batch carries an explicit `metadb.ConversationKind`: normal for ordinary
channel commits, CMD for one-shot sync commits or command-channel ids. It also
carries `SenderUID` from the committed event, channel identity, message
sequence, activity timestamp, and the expanded recipient UIDs. Receiver entries
leave `IsSender` unset; the active worker advances the sender read sequence
from `SenderUID` semantics. Active admission still runs when online
delivery enqueueing is disabled or no `RecipientDeliveryEnqueuer` is
configured. If active admission fails, the post-commit failure phase is
`conversation_active`, while accepted delivery, later large-channel pages, and
a successfully loaded non-large subscriber snapshot continue independently. A
sender-only aligned route failure does not suppress valid recipient delivery;
active projection falls back to the legacy admission surface so it can obtain a
fresh compatible route. A recipient item failure keeps delivery all-or-nothing
for that set and uses the same active compatibility fallback.

Recipient delivery is an enqueue contract. When delivery enqueueing is
configured, recipients are grouped by the full fenced recipient authority
target, including Slot leader term and Slot config epoch. UID authority
resolution is performed once per aligned sender-and-recipient snapshot and uses
the optional batch resolver when available; invalid or missing recipient targets
map to route-not-ready before enqueueing. The production plan-capable enqueuer packs those exact-target
groups into commands whose total recipient count is bounded by the existing
recipient batch size. This preserves the complete target fence across the queue
boundary while allowing one subscriber page to be admitted as one plan when it
fits that bound. Each target carries a capacity-limited view into the
grouping-owned normalized recipient storage, so asynchronous admission does not
repeat the recipient copy and cannot overwrite a sibling target window. A
legacy batch-only enqueuer remains supported; only that
compatibility path dispatches different targets concurrently up to
`RecipientAuthorityDispatchConcurrency`, while batches for the same target stay
sequential.

The dedicated delivery worker drains accepted plans. A target-aware presence
resolver receives all target groups from one plan together and returns aligned
per-group results, so one failed group is observed without suppressing the
other groups. A legacy presence resolver remains supported by resolving the
groups individually. A panic while processing one resolved group is converted
to that group's terminal error and does not prevent later sibling groups from
running. Successfully resolved groups first contribute their deduplicated
offline UIDs to one plan-level observer event, so plugin and webhook work scales
with bounded delivery plans rather than 256 physical authority targets. They
then skip only the sender's exact accepted session before routes are coalesced
by owner across the whole plan. Owner groups retain first-appearance order for
stable error folding, route order follows target and resolver order, and each
owner group is split into bounded push chunks. Different owners execute
concurrently under a fixed bound, while chunks for the same owner remain
sequential.
Retryable results narrow retries to only their returned routes; terminal
push failures map back only to the exact target groups that contributed those
routes, while unrelated owners and targets continue. Owner-local concrete
session writes remain outside `channelState`.

`RecipientDeliveryWorker` owns the bounded async queue for those delivery
plans. The buffered queue is the admission backpressure primitive; there is
no second slot semaphore. Admission is open only between `Start` and `Stop`;
closed admission returns `ErrRecipientDeliveryWorkerClosed`, and a full queue
waits for capacity until the caller context expires. `Stop` closes admission
first and cancels the worker lifecycle context. Enqueue calls that crossed the
open-state gate are counted until their queue send returns; workers may exit
only after that sender barrier closes, then terminally drain every accepted
plan with the canceled lifecycle context. Each plan also has a bounded
processing deadline, so a transport RPC that never responds cannot occupy one
worker indefinitely. The caller's Stop context bounds only how long Stop waits
for this asynchronous shutdown protocol to finish.
Processing failures are terminal best-effort delivery failures: they are
observed through the same post-commit failure surface with the recipient
authority target attached, and they are not returned to channelappend after the
plan has been accepted. The worker also emits low-cardinality queue, admission,
process, and execution-pressure observations: queue depth/capacity, configured
worker capacity/current in-flight commands, enqueue result/wait time,
processing result/duration, and attempted recipients per plan. Recipient totals
describe planned processing work rather than proven successful online delivery.
Queue and worker gauge
samples are serialized and read from current state so concurrent worker starts,
finishes, and queue changes cannot leave a stale terminal gauge; every accepted
command increments in-flight for the complete `runCommand` execution and the
final command returns it to zero. These observations do not include UID,
channel, or target labels.

## Pressure Observability

Writer pressure observations are produced from aggregate counters maintained on
state transitions; they do not scan shard writer maps. Admission depth is the
sum of shard-local accepted items that have not yet completed their futures.
Pending append, append in-flight, and post-commit backlog counters are updated
by the writer as items move between queues. Append effects run on a foreground
append pool, while post-commit and realtime recipient effects run on an
isolated post-commit pool so best-effort side effects cannot occupy durable
append workers. Post-commit pool admission is bounded and non-blocking: a full
pool retains the committed envelope and sends its writer through the fair retry
FIFO, so saturated side effects cannot pin writer-advance workers or lose an
acknowledged durable handoff. Exhausting the global pre-append reservation
returns item-local `ErrChannelBusy` and emits a post-commit backpressure effect
observation. The writer pressure observer reports the local
writer group aggregate rather than a per-channel event loop. It also reports
the global post-commit handoff depth/capacity and the fair-retry FIFO depth plus
its contended bit. Handoff depth counts reservations spanning pending append,
append execution, and durable post-commit work; retry depth counts de-duplicated
writers still waiting in the FIFO and excludes the writer that already owns the
selected retry turn. Consequently retry depth
may be zero while contended remains true. These snapshots read only atomics and
never acquire the retry scheduler mutex on the append hot path. Concurrent
publication requests use a non-blocking, coalesced wakeup for one group-local
publisher goroutine. It invokes callbacks serially, so a delayed old callback
cannot overwrite a newer snapshot, while a slow observer never pins an append or
commit goroutine. Group shutdown performs one final serialized publication after
writer and retry state drain, making the terminal zero state observable without
requiring later traffic or a hot-path global mutex. Reservation release, FIFO
enqueue/pop, and retry-turn completion request pressure publication only after
their state transition. None of these observations carries a channel, UID, Slot,
or authority-target label. When the observer supports pool
samples, the group also emits running/capacity/waiting observations for the
advance, append_effect, and post_commit pools. Advance waiting includes both
the dispatcher queue and a dispatcher currently blocked on the underlying
ants/v2 pool; append_effect reports direct ants state. Post_commit reports
logical admitted tasks so retained idle ants workers are not mistaken for
active work. Benchmark scripts use these samples for the per-node peak
`used/cap` pool summary.

Before a post-commit effect enters the asynchronous pool, it copies the
committed-envelope slice into effect-owned storage. The envelopes themselves
remain immutable values, while the independent slice ownership lets the writer
retain and retry its queued backing store without racing an already-running
effect.

`Stop` first closes admission and waits for shard admission ownership, append
state, handoff reservations, committed backlog, and retry FIFO ownership to
drain. This includes accepted realtime sends, whose futures retain shard
admission until their effect completes. The runtime context is canceled only
after the drain and worker-pool release complete. A Stop deadline therefore
returns only a waiting error; it does not alter the ongoing graceful drain.
