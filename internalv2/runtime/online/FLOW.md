# internalv2/runtime/online Flow

## Responsibility

`internalv2/runtime/online` owns node-local connection route projections for internalv2 connection routing. It is not a distributed directory, and authority/touch flows consume only `OwnerRoute` values. Concrete gateway session handles are stored separately in `LocalSession` for owner-local close actions.

## Lifecycle

`RegisterPending` stores a `LocalSession` before CONNACK succeeds, while keeping the route projection separate from the concrete session handle. `MarkActive` promotes a session after authority registration and local active re-check. `MarkClosingAndUnregister` removes local indexes before authority unregister tombstones are queued and returns only the `OwnerRoute` projection.

## Touch Batching

`MarkTouched` records owner-observed activity on active routes only and marks the session dirty. `DrainTouched` returns bounded `OwnerRoute` dirty batches for authority touch writes and clears their dirty markers. `RequeueTouched` re-marks drained routes after a failed batch only when the same active owner route is still current, so removed or superseded sessions are skipped.
