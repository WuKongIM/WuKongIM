# internalv2/runtime/channelappend Flow

## Responsibility

`internalv2/runtime/channelappend` owns local channel append-authority admission.
It is entered only after routing has resolved that the local node is the current
channel append authority. The package validates SEND commands, allocates
message IDs, admits durable append work, completes item-aligned futures, and
runs best-effort post-commit recipient/conversation effects.

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

```text
Router.SendBatch
  -> side-effect-safe pre-route validation / canonical channel derivation
  -> AuthorityResolver.ResolveAppendAuthority(canonical channel)
  -> local target: LocalSubmitter.SubmitLocal(target, original items)
  -> remote target: RemoteForwarder.ForwardSendBatch(target, original items)
  -> item-aligned results
```

The local path is the only path that may enter `SubmitLocal`; remote targets are
forwarded and must not create local `channelAppendr` state. Route movement errors
(`ErrStaleRoute`, `ErrNotChannelAuthority`, `ErrNotLeader`,
`ErrRouteNotReady`) are retried with bounded backoff while item deadlines allow
it. Retry sleeps wake when pending item cancellation or deadlines arrive, so
expired work does not wait for the whole backoff. Remote outbound admission is
bounded per `LeaderNodeID`, not per channel, so different channels to the same
remote authority share the same pressure limit.

Router submit contexts are neutral batch transport contexts. Per-item contexts
and deadlines are checked before route lookup, before submission, and while
waiting for retry wakeups. Once local or remote authority returns item-aligned
results, those results are preserved so a late deadline cannot erase a durable
append success or its committed handoff. This prevents one item context or
deadline from canceling other items that happen to share the same authority
batch. When a shared transport context is canceled only after an item's own
deadline has expired, the item result is reported as deadline-exceeded rather
than a generic cancellation so metrics keep send-timeout failures distinct from
caller/session cancellation.

## Authority-Only State

`SubmitLocal` accepts an `AuthorityTarget` that carries the channel authority
identity plus recipient fanout metadata (`Large` and
`SubscriberMutationVersion`). The group rejects targets whose `LeaderNodeID`
does not match `Options.LocalNodeID` before choosing a shard. Remote channels
are handled by `Router` through the `RemoteForwarder` port and never create
proxy `channelAppendr` state inside this package.

`channelAppendr` is created lazily by the owning shard after local authority
validation. State is keyed by the channel routing key derived from
`AuthorityTarget.ChannelKey` or from `ChannelID` when the target omits an
explicit key. Shard selection uses the same channel key, not the sender UID.
When later submissions carry a newer subscriber mutation version or a changed
large-channel flag, the writer invalidates only its recipient snapshot cache;
append authority fences remain owned by the state created for that channel key.

## Lifecycle

`Group.Start` opens local admission and prepares shared worker pools. Writers
are created lazily on accepted local submissions. `Group.Stop` closes admission,
cancels the runtime context used by append and post-commit work, and waits for
already accepted writers to drain until the caller context expires. A stopped
group is not restarted.

## Writer Execution

The runtime implements local authority validation, hash-sharded writer lookup,
lifecycle, pre-append preparation, channel-level append flow control, durable
append scheduling, committed-message handoff, and item-aligned futures.
Submission checks caller cancellation before admission. After that check,
bounded local admission is non-blocking and returns `ErrBackpressured` when the
group is closed or its outstanding accepted work is at capacity. Once a submit
event is accepted into a writer, caller cancellation no longer turns the
accepted event into a rejected submit.

Each active channel has one `channelAppendr`. The writer owns one
`channelState`, a lightweight inbox, and an atomic scheduled flag. The scheduled
flag guarantees that at most one goroutine advances that channel at a time. The
group uses shards only for writer lookup and creation; shard locks are not held
while preparing, appending, or committing messages.

Accepted submit events are prepared inline on the writer advance path. Rejected
and idempotent items complete their item-aligned future slots immediately with
their reason/error/result. Valid prepared items receive one message id and one
server timestamp. Before a prepared item can enter the pending queue, its
canonical prepared channel must still match the submitted `AuthorityTarget`;
request-scoped derivation or person-channel normalization that changes the
channel away from the target returns `ErrStaleRoute` for that item and creates
no state. Idempotency hits use the same canonical target validation before
their stored result can complete successfully, so stale routes cannot bypass
authority ownership by returning an older successful result. Matching prepared
items enter the writer state's pending queue in input order only when the
state's pending plus in-flight item count is below
`ChannelBacklogHighWatermark`; saturated channels complete those items with
`ErrChannelBusy` before they reach the append port.

The writer builds channel-aligned append batches from prepared pending items
and keeps up to `AppendInflightBatchesPerChannel` append batches in flight per channel.
Blocking `Appender.AppendBatch` calls run on the shared worker pool and wake the
same writer when they complete. Completion events are drained by append sequence
before mutating `channelState`, so SENDACK and post-commit handoff remain in
submission order even when same-channel append calls finish out of order. The
appender port must preserve durable per-channel append order for concurrent
same-channel requests or serialize the requests internally. Append requests
clone payloads at the appender boundary and carry the resolved authority epoch
and leader epoch as append fences.

Batch-level append errors are returned to all active items from that single
append attempt without retry. Short append results complete missing items with
`ErrAppendResultMissing`; per-item append errors map to SENDACK reasons;
successful append results complete `SENDACK` futures immediately with
`ReasonSuccess`, message id, and channel sequence. Successful append items also
enqueue `CommittedEnvelope` values in the same `channelState` as the handoff
point for best-effort post-commit recipient work. Post-commit side effects are
not checkpointed and not replayed after authority restart.

Post-commit work is scheduled from the writer state after durable append
succeeds and is independent from `SENDACK` completion. A committed envelope
remains pending only until one recipient delivery enqueue attempt completes.
Success prunes the payload-bearing envelope from the backlog. Failure is logged
through `PostCommitFailureObserver`, counted through effect metrics, and then
the envelope is dropped without retry so one bad active-admission, recipient
route, or delivery enqueue side effect cannot block later messages on the same
channel. The failure observation carries a precise post-commit phase plus
sampled recipient, target, and dispatch context so route-resolution failures
can be distinguished from conversation active admission and delivery enqueue
failures in logs. Each channel keeps only one committed envelope in flight at a
time, and concurrency inside that envelope is limited to recipient authority
targets. Unprocessed and in-flight commit backlog still participates in the
channel high-watermark, but failed best-effort work is dropped promptly to
avoid retaining unbounded payloads.

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

After each recipient set is formed, channelappend admits one
`conversationactive.ActiveBatch` before enqueueing recipient delivery batches.
The batch carries `SenderUID` from the committed event, channel identity,
message sequence, activity timestamp, and the expanded recipient UIDs. Receiver
entries leave `IsSender` unset; the active worker advances the sender read
sequence from `SenderUID` semantics. This active admission still runs when
online delivery enqueueing is disabled or no `RecipientDeliveryEnqueuer` is
configured. If active admission fails, the post-commit failure phase is
`conversation_active` and recipient delivery is not enqueued for that recipient
set.

Recipient delivery is an enqueue contract. When delivery enqueueing is
configured, recipient batches are grouped by the full fenced recipient
authority target after active admission succeeds. UID authority resolution is
performed once per unique trimmed UID and uses the optional batch resolver when
available; invalid or missing targets map to route-not-ready before enqueueing.
Different recipient authority targets may enqueue concurrently up to
`RecipientAuthorityDispatchConcurrency`; batches for the same target are enqueued
sequentially. The dedicated delivery worker drains the accepted batches,
resolves presence, groups routes by owner node, skips only the sender's exact
accepted session for echo suppression, retries retryable owner pushes, and
performs owner-local concrete session writes outside `channelState`.

`RecipientDeliveryWorker` owns the bounded async queue for those delivery
batches. Admission is open only between `Start` and `Stop`; closed admission
returns `ErrRecipientDeliveryWorkerClosed`, and a full queue waits for capacity
until the caller context expires. `Stop` closes admission first, then drains
already accepted batches until the caller context expires. Processing failures
are terminal best-effort delivery failures: they are observed through the same
post-commit failure surface with the recipient authority target attached, and
they are not returned to channelappend after the batch has been accepted. The
worker also emits low-cardinality queue, admission, and process observations:
queue depth/capacity, enqueue result/wait time, processing result/duration, and
recipient batch size. These observations do not include UID, channel, or target
labels.

## Pressure Observability

Writer pressure observations are produced from aggregate counters maintained on
state transitions; they do not scan shard writer maps. Admission depth tracks
accepted items that have not yet completed their futures. Pending append,
append in-flight, and post-commit backlog counters are updated by the writer as
items move between queues. Shared pool observations report submit results and
current running/capacity gauges for append and post-commit tasks. The writer
pressure observer reports the local writer group aggregate rather than a
per-channel event loop.

`Stop` cancels the runtime context passed to append and post-commit effects
before waiting for writers to drain. Once cancellation is observed, writers do
not schedule more queued post-commit backlog, avoiding a one-by-one walk of
canceled work during shutdown. Ports must respect their context promptly for
Stop to complete without waiting for the caller's timeout.
