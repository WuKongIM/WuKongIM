# internal/runtime/online Flow

## Responsibility

`internal/runtime/online` owns node-local connection route projections for internal connection routing. It is not a distributed directory, and authority/touch flows consume only `OwnerRoute` values. Concrete gateway session handles are stored separately in `LocalSession` for owner-local close actions.

## Lifecycle

`RegisterPending` stores a `LocalSession` before CONNACK succeeds, while keeping the route projection separate from the concrete session handle. `MarkActive` promotes a session after authority registration and local active re-check. `MarkClosingAndUnregister` removes local indexes before authority unregister tombstones are queued and returns only the `OwnerRoute` projection.

## Local Session Handle

`SessionHandle` is the entry-agnostic write and close surface stored with a
`LocalSession`. Runtime code may call `WriteDelivery(any)` for owner-local
server push and `CloseSession(reason)` for conflict actions, but the concrete
gateway/frame validation remains in the access adapter that created the handle.
`LocalSessionsByUID` returns copies of concrete local session records for
owner-local maintenance flows such as compatible user token replacement and
device quit handling. `LocalSessions` returns copies of all locally indexed
session records for owner-local manager connection inventory. Authority-facing
flows still consume only `OwnerRoute`.

## Touch Batching

`MarkTouched` records owner-observed activity on active routes only and marks the
session dirty. Each `DrainTouched(limit)` call returns at most `limit`
`OwnerRoute` values for one authority touch chunk and clears their dirty
markers. The app worker, rather than the registry, owns repeated drains and the
65,536-route default total flush budget. `RequeueTouched` re-marks drained
routes only when the same active owner route is still current, so removed or
superseded sessions are skipped. The worker defers failed-route requeue until
the complete bounded flush ends, preventing one failing route from being
drained repeatedly in the same flush.

## Diagnostics

`Snapshot` counts pending routes, active routes, and dirty touched routes across
all shards. It is intended for benchmark diagnostics and does not expose
concrete `LocalSession` handles.
