# internalv2/runtime/delivery Flow

`internalv2/runtime/delivery` owns online delivery fanout primitives and recipient-owner recvack state.

The package is independent from gateway, app, and concrete cluster runtimes. It only consumes small ports for subscriber paging, presence resolution, partition discovery, and pushing, so planner and fanout behavior can be unit tested and benchmarked in isolation.

`AckTracker` keeps owner-local recvack state, enforces a per UID/session pending
limit, and maintains an O(1) derived pending count so the recvack hot path never
scans all shards for observability. `Manager`, `Planner`, and `FanoutWorker`
form the runtime facade used by app adapters. `Manager` owns bounded async
admission through `pkg/workqueue.BoundedWorkerQueue` when fanout ports are
configured. Runtime `Observer`, `ManagerObserver`, and `AckObserver` events
describe fanout routing, UID route resolution, owner push attempts, manager
admission, terminal async outcomes, and owner-local ack state changes with
bounded result and error-class labels; concrete metrics and logging remain app
concerns.
`RetryScheduler` can wrap any `FanoutTaskRunner` with a bounded in-memory
retry queue. It executes the first attempt inline; retryable failures are
queued for background workers, while non-retryable failures and queue overflow
are returned to the caller. `ChannelSubscriberPlanner` adapts an
optional durable subscriber source into partition/cursor-based fanout pages; a
nil source returns a terminal empty page so app tests can enable delivery
without wiring a subscriber store. `FanoutTaskRouter` can sit between
`Manager` and `FanoutWorker` to run local authority partitions in-process and
forward remote authority partitions through a small node RPC port.

Committed-message fanout flow:

1. A committed message event enters `Manager.SubmitCommitted`.
2. `Manager` converts the event into an independent `Envelope`.
3. `Manager` admits the envelope into a bounded queue only while started. A
   full queue applies backpressure until a queue slot opens, the caller context
   expires, or the manager closes.
4. Workers consume accepted envelopes, call `Manager.runEnvelope` with an
   execution context independent from admission, and emit terminal observations
   with bounded result, error-class, and queue depth labels.
5. `Planner.Plan` creates one `FanoutTask` per authority `Partition`, or a
   single default task when no partitioner is wired.
6. `Manager` runs planned tasks sequentially through its configured `FanoutTaskRunner`.
   App wiring may use `FanoutTaskRouter`, which dispatches by
   `Partition.LeaderNodeID`, wrapped by `RetryScheduler` for retryable failures.
7. When `Envelope.MessageScopedUIDs` is non-empty, `Planner` creates a single default scoped task and `FanoutWorker` uses those UIDs directly without scanning subscribers.
8. Otherwise `FanoutWorker` pages recipients through `SubscriberPlanner.NextPartitionPage`.
9. Each UID page is resolved through `PresenceResolver.EndpointsByUIDs`.
10. `FanoutWorker` emits a resolve observation for each UID page.
11. Online routes are grouped by `OwnerNodeID`, split by push batch size, and sent through `Pusher.Push`.
12. `FanoutWorker` emits a push observation for each owner-node batch, continues after retryable push results, and returns aggregated retryable routes as `ErrRetryablePushRoutes` after all remaining owner batches are attempted.
13. `FanoutTaskRouter` emits a task observation for local or remote task execution. Remote forwarding failures are wrapped with `ErrRetryableFanoutTask`.
14. `RetryScheduler` enqueues retryable task failures until `MaxAttempts` is reached. Push-route retries are narrowed to the retryable route UIDs before re-enqueueing.
15. If a non-terminal subscriber page cannot advance its cursor, `FanoutWorker` returns `ErrInvalidSubscriberCursor` instead of silently ending the scan.
16. `FanoutWorker` skips routes without an owner node and skips same-session sender echo only when `Envelope.SenderNodeID` is known and the route matches sender UID, sender owner node, and sender session.

Async manager flow:

1. `Manager.Start` opens a bounded queue and launches a small worker set.
2. `Manager.SubmitCommitted` clones the committed event and uses bounded
   admission; it does not fall back to synchronous fanout.
3. Accepted work is later planned and run through the existing runner.
4. A full queue waits for capacity; if that admission wait is interrupted by
   caller context expiry, the manager emits an overflow admission observation
   and returns the context error.
5. Closed admission returns `ErrManagerClosed` to the caller.
6. Every accepted command emits exactly one terminal observation.
7. `Manager.Stop` rejects new admission and drains accepted work until the
   caller context expires.
8. Stop is terminal for the manager lifecycle; app composition should create a
   new `Manager` for a new lifecycle instead of restarting a stopped instance.

Retry scheduler lifecycle:

1. `Start` launches a small fixed worker set.
2. `RunTask` performs the first attempt inline.
3. Retryable errors enqueue a cloned task with an incremented attempt number.
4. Background workers retry queued tasks after the configured backoff.
5. `Stop` cancels waiting backoff, drains queued tasks, and exits when the queue is empty or the caller context expires.

Recvack flow:

1. Push accepted by recipient owner.
2. `Manager.BindPendingAck` calls `AckTracker.BindResult` and records the
   pending recvack when the per-session limit allows it.
3. Client sends Recvack.
4. `Manager.Recvack` calls `AckTracker.Ack` and clears the owner-local pending
   state for matched entries.
5. `Manager.SessionClosed` or `Manager.ExpirePendingAcks` cleans pending
   entries that no longer have a live client ack path.
6. Each bind, ack, session-close, and expire mutation emits a bounded
   `AckEvent` from `Manager` with action, result, changed count, and the
   owner-local pending count after the mutation.
7. `Manager` serializes each ack mutation with its `AckEvent` emission, so
   observers do not apply an older pending count after a newer state change.
8. App top and Prometheus observers consume the same ack event path, so
   `ack_bindings` and `wukongim_delivery_ack_bindings` reflect `AckTracker`
   transitions instead of adapter-local estimates.
