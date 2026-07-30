# internal/runtime/delivery Flow

## Responsibility

`internal/runtime/delivery` is the single node-local Online Delivery runtime.
It owns bounded plan admission, exact-target presence resolution, durable-only
offline observation, sender-session suppression, owner grouping, bounded owner
push and narrowed retry, final owner-local writes, and the complete pending
RECVACK lifecycle.

The package depends only on canonical Online Delivery and channelappend
contracts plus narrow ports. Gateway session lookup and packet construction
remain in `internal/infra/delivery`; node transport remains in
`internal/access/node`; composition and lifecycle ordering remain in
`internal/app`.

Online Delivery is bounded best effort. Accepted plans are not durable
checkpoints and are not replayed after process restart.

## Plan Flow

```text
channelappend RecipientDeliveryPlan
  -> Runtime.EnqueueRecipientDeliveryPlan
     -> validate explicit Durable/Transient mode, exact targets, and size bound
     -> bounded queue ownership transfer
  -> fixed Runtime workers
     -> PlanPresenceResolver.EndpointsByTargets
     -> preserve aligned partial target errors
     -> Durable only: publish one deduplicated offline UID batch
     -> suppress only the sender's exact UID/node/session route
     -> coalesce routes by first-seen OwnerNodeID
     -> split each owner group by OwnerPushBatchSize
     -> run distinct owners under OwnerConcurrency
     -> retry only routes returned as Retryable
        -> local owner: Runtime.pushOwnerLocal
        -> remote owner: RemoteOwnerPusher.PushOwner
  -> one terminal observation per accepted plan
```

`RecipientDeliveryPlan.Event`, target slices, and recipient slices become
shared immutable storage after successful admission. A failed admission retains
nothing. Plan recipient count, queue capacity, worker count, owner batch size,
owner concurrency, retry attempts/backoff, and total processing time are all
bounded.

The resolver result is position-aligned with the exact input targets. A missing
or failed result is terminal for that target but does not discard successful
sibling targets. Owner push chunks for one owner remain ordered; independent
owners may overlap. There is no separate retry queue: retry happens inside the
accepted plan's deadline and narrows to the exact retryable route subset.

## Owner-local Push and ACK Flow

```text
local plan or RPC OwnerPush
  -> validate owner identity and route shape
  -> AckTracker.BindBatch, preserving input alignment
  -> for each successfully reserved route
     -> later duplicate key: cancel its early token and rebind immediately
     -> LocalSessionWriter.WriteSession
        -> Accepted: retain and finish the token
        -> Retryable: token-scoped rollback and retryable classification
        -> Dropped: token-scoped rollback and terminal classification
  -> AckTracker.FinishBindBatch for accepted indexes
  -> aggregate bind/finish observations
```

The runtime, not the session adapter, owns every ACK token. The adapter receives
only the immutable event and exact route, validates the live session, builds the
recipient packet, performs the write, and returns a disposition.

ACK identity is `(UID, SessionID, MessageID)`. A fast `Recvack`, concurrent
`SessionClosed`, expiry, duplicate route, refresh, or failed write can win
between bind and finish without allowing one attempt to roll back another
attempt's state. A duplicate row intentionally remains a duplicate write; it
rebinds just before its write so an earlier fast ACK cannot consume its pending
identity. Per-session limits and invalid rows remain item-aligned. Successful
writes leave pending state until exact feedback, session close, expiry, or
runtime shutdown.

`AckTracker` shards state by session ID, maintains an O(1) pending count, and
groups batch bind/finish locking by touched shard. `AckObserver` reports
serialized identity-count mutations. Optional `AckBatchObserver` reports one
aggregate bind stage and one finish stage without adding callbacks inside item
or shard loops.

## Lifecycle

`Start` opens one runtime generation and launches the fixed plan workers.
`Stop` first closes admission, cancels the generation context, waits for
admission senders and external owner-push RPC calls to leave, then lets workers
terminally drain every accepted queue item. A successful stop clears transient
ACK state and makes the same runtime restartable.

A caller timeout bounds only that caller's wait. The runtime remains closing
until the old generation has fully exited; `Start` rejects during that window,
so old and new queues or ACK generations never overlap. A later `Stop` or
`Start` observes the completed generation and finalizes the closed state.

Queue, in-flight worker, admission, terminal-plan, owner-push, ACK, and ACK-batch
observations use bounded low-cardinality labels. No UID, channel, Slot, or
authority target becomes a metric label. Terminal failures expose one
non-metric `PlanFailureSample` with a representative recipient and exact
authority target for bounded structured logging. Retry exhaustion has its own
`retry_exhausted` observation result instead of collapsing into generic error.
Observer panics are isolated from admission, delivery, ACK mutation, and
lifecycle outcomes.
