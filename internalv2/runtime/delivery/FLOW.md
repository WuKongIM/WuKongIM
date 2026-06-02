# internalv2/runtime/delivery Flow

`internalv2/runtime/delivery` owns online delivery fanout primitives and recipient-owner recvack state.

The package is independent from gateway, app, and concrete cluster runtimes. It only consumes small ports for subscriber paging, presence resolution, partition discovery, and pushing, so planner and fanout behavior can be unit tested and benchmarked in isolation.

`AckTracker` keeps owner-local recvack state and can enforce a per UID/session
pending limit. `Manager`, `Planner`, and `FanoutWorker` form the runtime facade
used by app adapters. `Manager` supports a sync unit-test mode and a bounded
async mode for app wiring. Runtime `Observer` and
`ManagerObserver` events describe fanout routing, UID route resolution, owner
push attempts, manager admission, and terminal async outcomes with bounded
result and error-class labels; concrete metrics remain an app concern.
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
3. When async manager mode is enabled, `Manager` admits the envelope into a
   bounded queue only while started; overflow and closed states return explicit
   admission errors and observer events.
4. In async manager mode, workers consume accepted envelopes, call
   `Manager.runEnvelope` with an execution context independent from admission,
   and emit terminal observations with bounded result, error-class, and queue
   depth labels.
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
12. `FanoutWorker` emits a push observation for each owner-node batch and classifies retryable push results as `ErrRetryablePushRoutes`.
13. `FanoutTaskRouter` emits a task observation for local or remote task execution. Remote forwarding failures are wrapped with `ErrRetryableFanoutTask`.
14. `RetryScheduler` enqueues retryable task failures until `MaxAttempts` is reached. Push-route retries are narrowed to the retryable route UIDs before re-enqueueing.
15. If a non-terminal subscriber page cannot advance its cursor, `FanoutWorker` returns `ErrInvalidSubscriberCursor` instead of silently ending the scan.
16. `FanoutWorker` skips routes without an owner node and skips same-session sender echo only when `Envelope.SenderNodeID` is known and the route matches sender UID, sender owner node, and sender session.

Async manager flow:

1. `Manager.Start` opens a bounded queue and launches a small worker set.
2. `Manager.SubmitCommitted` clones the committed event and tries bounded
   admission.
3. Accepted work is later planned and run through the existing runner.
4. Queue overflow and closed admission return typed errors to the caller.
5. Every accepted command emits exactly one terminal observation.
6. `Manager.Stop` rejects new admission and drains accepted work until the
   caller context expires.

Retry scheduler lifecycle:

1. `Start` launches a small fixed worker set.
2. `RunTask` performs the first attempt inline.
3. Retryable errors enqueue a cloned task with an incremented attempt number.
4. Background workers retry queued tasks after the configured backoff.
5. `Stop` cancels waiting backoff, drains queued tasks, and exits when the queue is empty or the caller context expires.

Recvack flow:

1. Push accepted by recipient owner.
2. `AckTracker.Bind` records the pending recvack when the per-session limit
   allows it.
3. Client sends Recvack.
4. `AckTracker.Ack` clears the owner-local pending state.
5. `SessionClosed` or `Expire` cleans pending entries that no longer have a live client ack path.
