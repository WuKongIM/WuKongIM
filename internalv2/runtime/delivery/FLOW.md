# internalv2/runtime/delivery Flow

`internalv2/runtime/delivery` owns online delivery fanout primitives and recipient-owner recvack state.

The package is independent from gateway, app, and concrete cluster runtimes. It only consumes small ports for subscriber paging, presence resolution, partition discovery, and pushing, so planner and fanout behavior can be unit tested and benchmarked in isolation.

`AckTracker` keeps owner-local recvack state. `Manager`, `Planner`, and `FanoutWorker` form the synchronous runtime facade used by future app adapters.

Committed-message fanout flow:

1. A committed message event enters `Manager.SubmitCommitted`.
2. `Manager` converts the event into an independent `Envelope`.
3. `Planner.Plan` creates one `FanoutTask` per authority `Partition`, or a single default task when no partitioner is wired.
4. `Manager` runs tasks sequentially through `FanoutWorker.RunTask`.
5. When `Envelope.MessageScopedUIDs` is non-empty, `Planner` creates a single default scoped task and `FanoutWorker` uses those UIDs directly without scanning subscribers.
6. Otherwise `FanoutWorker` pages recipients through `SubscriberPlanner.NextPartitionPage`.
7. Each UID page is resolved through `PresenceResolver.EndpointsByUIDs`.
8. Online routes are grouped by `OwnerNodeID`, split by push batch size, and sent through `Pusher.Push`.
9. If a non-terminal subscriber page cannot advance its cursor, `FanoutWorker` returns `ErrInvalidSubscriberCursor` instead of silently ending the scan.
10. `FanoutWorker` skips routes without an owner node and skips same-session sender echo only when `Envelope.SenderNodeID` is known and the route matches sender UID, sender owner node, and sender session.

Recvack flow:

1. Push accepted by recipient owner.
2. `AckTracker.Bind` records the pending recvack.
3. Client sends Recvack.
4. `AckTracker.Ack` clears the owner-local pending state.
5. `SessionClosed` or `Expire` cleans pending entries that no longer have a live client ack path.
