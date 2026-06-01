# internalv2/runtime/online Flow

## Responsibility

`internalv2/runtime/online` owns node-local real gateway sessions for internalv2 connection routing. It is not a distributed directory.

## Lifecycle

`RegisterPending` is used before CONNACK succeeds. `MarkActive` promotes a session after authority registration and local active re-check. `MarkClosingAndUnregister` removes local indexes before authority unregister tombstones are queued.

## Touch Batching

`MarkTouched` records owner-observed activity on active routes only and marks the session dirty. `DrainTouched` returns bounded dirty batches for authority touch writes and clears their dirty markers. `RequeueTouched` re-marks drained routes after a failed batch only when the same active owner route is still current, so removed or superseded sessions are skipped.
