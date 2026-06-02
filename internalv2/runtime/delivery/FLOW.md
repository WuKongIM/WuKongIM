# internalv2/runtime/delivery Flow

`internalv2/runtime/delivery` owns online delivery fanout primitives and recipient-owner recvack state.

The package is independent from gateway, app, and concrete cluster runtimes, so it can be benchmarked in isolation.

Task 3 only implements `AckTracker`. Task 4 will add planner, fanout, and manager runtime primitives.

Flow:

1. Push accepted by recipient owner.
2. `AckTracker.Bind` records the pending recvack.
3. Client sends Recvack.
4. `AckTracker.Ack` clears the owner-local pending state.
5. `SessionClosed` or `Expire` cleans pending entries that no longer have a live client ack path.
