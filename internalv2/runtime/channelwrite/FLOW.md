# internalv2/runtime/channelwrite Flow

## Responsibility

`internalv2/runtime/channelwrite` owns local channel-authority write admission.
It is the in-memory reactor group entered only after routing has resolved that
the local node is the current channel authority.

## Router Flow

`Router` is the channel-authority routing front door for SEND batches. Before
it resolves authority, it performs only side-effect-safe command checks and
canonical channel derivation: malformed terminal commands fail item-locally
without resolving or creating channel metadata, request-scoped sends derive the
stable temporary command channel, and person-channel sends with
`NormalizePersonChannel` derive the canonical person channel. The original send
command is still submitted unchanged so reactor prepare remains the
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
forwarded and must not create local `channelState`. Route movement errors
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
identity. The group rejects targets whose `LeaderNodeID` does not match
`Options.LocalNodeID` before choosing a reactor. Remote channels are handled by
`Router` through the `RemoteForwarder` port and never create proxy
`channelState` inside this package.

`channelState` is created lazily by the owning reactor after local authority
validation. State is keyed by the channel routing key derived from
`AuthorityTarget.ChannelKey` or from `ChannelID` when the target omits an
explicit key. Reactor selection uses the same channel key, not the sender UID.

## Lifecycle

`Group.Start` starts all reactors and opens local admission. `Group.Stop` closes
admission, closes reactor mailboxes, and waits for already accepted mailbox
events to drain until the caller context expires. A stopped group is not
restarted because reactor mailboxes are closed as part of shutdown.

## Current Reactor Scope

The runtime currently implements local authority validation, reactor routing,
lifecycle, pre-append preparation, lazy state creation, channel-level append
flow control, durable append scheduling, committed-message handoff, and
item-aligned futures. Submission checks caller cancellation before mailbox
enqueue; after that check, bounded mailbox enqueue is non-blocking and returns
`ErrBackpressured` when full. Once a submit event is accepted into a reactor
mailbox, caller cancellation no longer turns the accepted event into a rejected
submit. The group lock is held only through closed-state validation, reactor
selection, and mailbox enqueue, then released while waiting for reactor
admission ack so `Stop` can close admission promptly.

Accepted submit events dispatch preparation through the shared ants-backed
effect scheduler. The reactor remains the only writer of `channelState`; ants
workers only execute blocking prepare, append, and post-commit effects, then
return completion events to the owning reactor. The reactor bounds accepted
prepare/append admission with the same capacity as its mailbox, so new
submissions return `ErrBackpressured` instead of growing unbounded in memory
when workers or appenders are saturated. The reserved slot is released only
when the item-aligned future completes. Each accepted prepare effect receives a
monotonic sequence for the submitted authority target; completion events may
arrive out of order, but the reactor drains them only in sequence so
same-channel pending order matches submission order even with multiple workers.
Effect worker pressure metrics report busy worker count, configured worker
capacity, effect queue depth, and effect queue capacity per reactor and stage
(`prepare`, `append`, and `post_commit`) so saturation can be distinguished
from downstream append or post-commit latency. Shared ants pool observations
also report pool submit results (`submitted`, `full`, and `error`) plus
in-flight/capacity/saturation gauges per stage, so benchmark evidence can show
when dispatch is delayed because a stage pool is full. Prepare, append, and
post-commit worker budgets are configured independently. Recipient authority
dispatch fanout inside one `post_commit` effect is configured separately from
the post-commit worker count so increasing post-commit workers does not also
multiply downstream recipient-authority concurrency.

The reactor loop applies prepare completion events, and only those completion
events mutate `channelState`. Rejected and idempotent items complete their
item-aligned future slots immediately with their reason/error/result. Valid
prepared items receive one message id and one server timestamp. Before a
prepared item can enter the pending queue, its canonical prepared channel must
still match the submitted `AuthorityTarget`; request-scoped derivation or
person-channel normalization that changes the channel away from the target
returns `ErrStaleRoute` for that item and creates no state. Idempotency hits
use the same canonical target validation before their stored result can
complete successfully, so stale routes cannot bypass authority ownership by
returning an older successful result. Matching prepared items enter the owning
channel state's pending queue in input order only when the state's pending plus
in-flight item count is below `PendingItemHighWatermark`; saturated channels
complete those items with `ErrChannelBusy` before they reach the append port.

The owning reactor builds channel-aligned append batches from prepared pending
items and keeps up to `AppendInflightLimit` append batches in flight per
channel. Blocking `Appender.AppendBatch` calls run in worker goroutines and
return completion events to the same authority reactor. Completion events are
drained by append sequence before mutating `channelState`, so SENDACK and
post-commit handoff remain in submission order even when same-channel append
workers finish out of order. The appender port must preserve durable per-channel
append order for concurrent same-channel requests or serialize the requests
internally. Append requests clone payloads at the appender boundary and carry
the resolved authority epoch and leader epoch as append fences.

Batch-level append errors are returned to all active items from that single
append attempt without retry. Short append results complete missing items with
`ErrAppendResultMissing`; per-item append errors map to SENDACK reasons;
successful append results complete `SENDACK` futures immediately with
`ReasonSuccess`, message id, and channel sequence. Successful append items also
enqueue `CommittedEnvelope` values in the same `channelState` as the handoff
point for best-effort post-commit recipient work. Post-commit side effects are
not checkpointed and not replayed after authority restart.

Post-commit work is scheduled from the authority `channelState` after durable
append succeeds and is independent from `SENDACK` completion. A committed
envelope remains pending only until one recipient dispatch attempt completes.
Success prunes the payload-bearing envelope from the backlog. Failure is logged
through `PostCommitFailureObserver`, counted through effect metrics, and then
the envelope is dropped without retry so one bad active-admission, presence,
route, or push side effect cannot block later messages on the same channel. The
failure observation carries a precise post-commit phase plus sampled recipient,
target, and dispatch context so route-resolution failures can be distinguished
from conversation active admission, presence lookup, recipient dispatch, and
owner push failures in logs. Each channel keeps only one committed envelope in
flight
at a time, and concurrency inside that envelope is limited to recipient
authority targets. Unprocessed and in-flight commit backlog still participates
in the channel high-watermark, but failed best-effort work is dropped promptly
to avoid retaining unbounded payloads.

Scoped `MessageScopedUIDs` dispatch directly without scanning subscribers.
Person channels derive exactly the two canonical participants from the
committed channel id. Other channels page subscribers with the configured page
size and dispatch each page before requesting the next one, so the runtime does
not load all subscribers before recipient dispatch. A non-terminal subscriber
page must return a non-empty cursor different from the previous cursor;
otherwise dispatch fails before that page's recipients are admitted or
dispatched, avoiding partial side effects for the invalid page before the
envelope is dropped.

After each recipient set is formed, channelwrite admits one
`conversationactive.ActiveBatch` before invoking the message delivery router.
The batch carries `SenderUID` from the committed event, channel identity,
message sequence, activity timestamp, and the expanded recipient UIDs. Receiver
entries leave `IsSender` unset; the active worker advances the sender read
sequence from `SenderUID` semantics. This active admission still runs when
online delivery is disabled or no `RecipientRouter` is configured. If active
admission fails, the post-commit failure phase is `conversation_active` and
message delivery is not attempted for that recipient set.

Recipient-authority processing is now delivery-only: it resolves online routes
and pushes delivery commands, but does not maintain recent-conversation
projection rows. When delivery is configured, recipient batches are grouped by
the full fenced recipient authority target after active admission succeeds. UID
authority resolution is performed once per unique trimmed UID and uses the
optional batch resolver when available; invalid or missing targets map to
route-not-ready before dispatch. Different recipient authority targets may
dispatch concurrently up to `RecipientDispatchConcurrency`; batches for the
same target are dispatched sequentially. Delivery pushes are grouped by owner
node. The sender's own echo is skipped only for the exact owner node and
session that accepted the original SEND; other sender sessions remain eligible.
Retryable owner push routes are retried with bounded backoff and an attempt cap;
routes still retryable after the final attempt return an explicit
retry-exhausted error. Owner-local concrete session writes remain outside
`channelState`.

`Stop` cancels the runtime context passed to prepare, append, and post-commit
effects before waiting for reactors to drain. Once cancellation is observed,
the reactor does not schedule more queued post-commit backlog, avoiding a
one-by-one walk of canceled work during shutdown. Ports must respect their
context promptly for Stop to complete without waiting for the caller's timeout.
