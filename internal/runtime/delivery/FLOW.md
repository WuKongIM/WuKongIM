# internal/runtime/delivery Flow

`internal/runtime/delivery` owns online delivery fanout primitives and recipient-owner recvack state.

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
concerns. The separately configured optional `AckBatchObserver` adds one
aggregate callback per batch bind or finish stage with numeric
shape/rejection/rollback fields; it does not add callbacks inside tracker item
or shard loops, and an ordinary `AckObserver` never enables that extra work.
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
2. Owner-local multi-route delivery validates exact active sessions first, then
   `Manager.BindPendingAcks` calls `AckTracker.BindBatch`. The tracker preserves
   item alignment and per-session limits while grouping entries by internal
   shard, so each affected shard and the Manager mutation/observation lock are
   acquired once per push batch. The result is one item-aligned opaque token
   slice: a zero token means rejection, while every accepted attempt receives a
   distinct in-flight reservation even when it refreshes an existing key. The
   single-result surface retains `Bound` and `Added` for compatibility. Its
   `BindPendingAck` compatibility wrapper immediately finishes the reservation;
   direct result and batch callers must finish or roll back every accepted token
   unless identity cleanup wins first.
3. The owner writes only routes whose aligned bind result succeeded, after one
   final exact-session revalidation closes the wider batch-bind/write race. A
   route write, packet-build, or revalidation failure rolls back only that
   attempt's token. A successful write finishes its token and marks the key
   committed. The internal entry keeps the committed `PendingRecvAck` snapshot
   separate from in-flight refresh metadata. A first uncommitted bind uses one
   inline primary token and the entry snapshot without another allocation;
   overlapping or committed refreshes use a lazily allocated attempt slice that
   carries both token and candidate snapshot. Finishing a refresh promotes only
   that attempt's snapshot, while rolling it back preserves the previous
   committed snapshot. When an overlapping fresh attempt finishes before the
   inline primary attempt, the finishing attempt's existing slice slot retains
   the primary candidate so a later successful primary finish can still promote
   its own metadata without another allocation. Finish and rollback nil the
   extra-attempt storage when it drains. Consequently the typical unique bind uses no extra-attempt
   allocation, while each simultaneously retained committed-key refresh pays
   for its own tentative metadata until finish or rollback. Rollback removes
   the key only when it has neither a committed write nor another in-flight
   attempt. Multi-route success finishes are
   grouped by tracker shard without another per-item result allocation. A later
   duplicate item first cancels its original batch token, then rebinds immediately
   before writing so a fast earlier Recvack cannot consume the reservation it is
   about to use. Mixed limit rejections remain item-aligned and are classified by
   the owner pusher.
4. Client sends Recvack.
5. `Manager.Recvack` calls `AckTracker.Ack` and clears the owner-local pending
   state for matched entries.
6. `Manager.SessionClosed` or `Manager.ExpirePendingAcks` cleans pending
   entries that no longer have a live client ack path.
7. Each single mutation emits one bounded `AckEvent`; a batch bind emits one
   aggregate event with the batch's added count and final owner-local pending
   count. Token rollback emits one bounded event and changes the gauge only when
   it removes the last uncommitted reservation. Finish emits no event because it
   cannot change the pending identity count. The event remains a gauge
   projection, while per-route mixed bind rejections stay visible in owner-push
   classification and bounded logs.
   Separately, an optional `AckBatchObserver` emits one post-operation stage for
   each multi-item bind and finish. The event reports bounded phase/outcome plus
   item, touched-shard, rejection, rollback, and duration values. The
   owner-push caller passes the number of bind reservations it actually
   canceled; omitted indexes remain in-flight and are not inferred to be
   rollbacks. The observer does not scan tracker state or time individual shard
   locks.
8. `Manager` serializes each ack mutation with its `AckEvent` emission, so
   observers do not apply an older pending count after a newer state change.
9. App top and Prometheus observers consume the same ack event path, so
   `ack_bindings` and `wukongim_delivery_ack_bindings` reflect `AckTracker`
   transitions instead of adapter-local estimates.
