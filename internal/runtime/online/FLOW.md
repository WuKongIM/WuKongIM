---
scope: package
summary: Maintains node-local pending and active connection routes, concrete session handles, and bounded touch batching.
---

# Online Registry Flow

## Responsibility

This package owns the node-local connection-route projection and concrete
owner-local session handles. It is not a distributed presence directory.

## Boundaries

- Authority registration, lookup, and touch use only copied `OwnerRoute`
  values; concrete `SessionHandle` values never leave owner-local flows.
- Gateway and frame validation remain in the adapter that creates the handle.
- The app worker owns repeated touch drains and the total flush budget.

## Main Flows

1. `RegisterPending` stores a local session before CONNACK; `MarkActive`
   promotes it after authority registration and an active recheck.
2. `MarkClosingAndUnregister` removes local indexes before the authority
   tombstone is queued and returns only the owner-route projection.
3. `MarkTouched` marks an active route dirty; bounded drains produce one touch
   chunk, and failed routes are requeued only if the same active owner remains.

## Invariants and Failure Semantics

- Pending and active indexes remain separate from concrete session handles.
- Local inventory methods return copies and are restricted to owner-local
  maintenance and diagnostics. Bulk traversal uses a callback so large
  registries do not require an inventory-sized allocation.
- Requeue skips removed or superseded sessions and occurs after a bounded flush
  so one failing route cannot be redrained indefinitely.
- Snapshot reports only aggregate pending, active, and dirty counts.

## Read First

- [Registry](registry.go)
- [Online types](types.go)

## Update Triggers

Update this file when connection lifecycle, owner-route projection, session
handles, touch batching, flush ownership, or diagnostics change.
