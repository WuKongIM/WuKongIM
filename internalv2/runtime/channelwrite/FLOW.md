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

Accepted submit events dispatch preparation to bounded effect workers. The
reactor bounds accepted prepare/append admission with the same capacity as its
mailbox, so new submissions return `ErrBackpressured` instead of growing
unbounded in memory when workers or appenders are saturated. The reserved slot
is released only when the item-aligned future completes. Each accepted prepare
effect receives a monotonic sequence for the submitted authority target;
completion events may arrive out of order, but the reactor drains them only in
sequence so same-channel pending order matches submission order even with
multiple workers.

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

Batch-level `ErrRouteNotReady`, `ErrNotLeader`, and `ErrStaleRoute` are retried
with bounded backoff while at least one active item deadline remains. Short
append results complete missing items with `ErrAppendResultMissing`; per-item
append errors map to SENDACK reasons; successful append results complete
`SENDACK` futures immediately with `ReasonSuccess`, message id, and channel
sequence. Successful append items also enqueue `CommittedEnvelope` values in the
same `channelState` as the handoff point for post-commit recipient work.
When a durable cursor store is configured, the state first loads the last
completed post-commit cursor and reads committed messages from
`lastCompletedSeq + 1` in bounded pages before allowing live post-commit
effects to run. Replay messages are inserted ahead of live committed backlog
so authority restart repair preserves channel sequence order. Cursor load/read
errors retry with the same bounded in-memory attempt cap as commit effects; a
channel with exhausted replay attempts remains blocked by its backlog rather
than running live recipient effects ahead of unreplayed durable messages.

Post-commit work is scheduled from the authority `channelState` after durable
append succeeds and is independent from `SENDACK` completion. A committed
envelope remains pending until its recipient dispatch succeeds or reaches the
bounded in-memory retry cap. Each channel keeps only one committed envelope in
flight, so cursor checkpoint order remains message-sequential; concurrency is
limited to recipient authority targets inside that envelope. Success then
checkpoints the committed channel sequence through the cursor store after
recipient authority dispatch is accepted, and prunes the payload-bearing
envelope from the backlog. Cursor
checkpoint errors retry the same committed envelope without changing the
already-completed `SENDACK`; if the process restarts before checkpoint success,
durable replay starts again from the previous cursor and duplicate replay must
be tolerated by idempotent recipient-side projections. Terminal max-attempt
failure explicitly drops the in-memory envelope for now so the reactor can
advance, while a later restart can still replay uncheckpointed durable log
entries. Unprocessed and in-flight commit backlog participates in the channel
high-watermark so a lagging post-commit path can return `ErrChannelBusy`
instead of retaining unbounded payloads.

Scoped `MessageScopedUIDs` dispatch directly without scanning subscribers.
Person channels derive exactly the two canonical participants from the
committed channel id. Other channels page subscribers with the configured page
size and dispatch each page before requesting the next one, so the runtime does
not load all subscribers before recipient dispatch. A non-terminal subscriber
page must return a non-empty cursor different from the previous cursor;
otherwise dispatch fails before that page's recipients are dispatched, avoiding
duplicate effects on retry. Recipient batches are grouped by the full fenced
recipient authority target. UID authority resolution is performed once per
unique trimmed UID and uses the optional batch resolver when available; invalid
or missing targets map to route-not-ready before dispatch. Different recipient
authority targets may dispatch concurrently up to the effect worker count;
batches for the same target are dispatched sequentially.

Recipient-authority processing applies conversation patches before resolving
online delivery routes. Delivery pushes are grouped by owner node. The sender's
own echo is skipped only for the exact owner node and session that accepted the
original SEND; other sender sessions remain eligible. Delivery cannot be
configured without the conversation projector. Retryable owner push routes are
retried with bounded backoff and an attempt cap; routes still retryable after
the final attempt return an explicit retry-exhausted error. Owner-local
concrete session writes remain outside `channelState`.

`Stop` cancels the runtime context passed to prepare, append, replay, and
post-commit effects before waiting for reactors to drain. Once cancellation is
observed, the reactor does not schedule more replay or post-commit backlog,
avoiding a one-by-one walk of canceled work during shutdown. Ports must
respect their context promptly for Stop to complete without waiting for the
caller's timeout.
