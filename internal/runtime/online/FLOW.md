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

1. `RegisterPending` stores a local session before CONNACK. Full activation uses
   `MarkActiveWithReceipt`, comparing the exact committed generation/owner Cut
   and promoting under the same Slot lock as `BeginSlot`. Legacy `MarkActive`
   is unavailable once a full barrier exists.
2. `MarkClosingAndUnregister` removes local indexes before the authority
   tombstone is queued and returns only the owner-route projection.
3. An active UID index serves bounded owner reconstruction pages; pending,
   closing, superseded, and output-saturated snapshots cannot prove online state.
   Its optional fill-buffer port reuses prepaid caller storage, borrows immutable
   strings and clears written references on saturation; retained strings need
   separate caller-owned copies.
4. Full reconstruction traverses an ordered active-by-Hash-Slot index through
   Begin/Page/End. Pending never delays a scan; activity/close and promotion use
   Slot-before-session lock order. Exact last-page replay retains immutable
   backing; skipped pages, old generations and forged End fail closed.
5. `MarkTouched` marks an active route dirty; bounded drains produce one touch
   chunk, and failed routes are requeued only if the same active owner remains.

## Invariants and Failure Semantics

- Pending and active indexes remain separate from concrete session handles.
- Local inventory methods return copies and are restricted to owner-local
  maintenance and diagnostics. Bulk traversal uses a callback so large
  registries do not require an inventory-sized allocation.
- Requeue skips removed or superseded sessions and occurs after a bounded flush
  so one failing route cannot be redrained indefinitely.
- Snapshot reports only aggregate pending, active, and dirty counts.

- Owner scan backing and each retained reply stay charged independently of
  live indexes: default 196,608 rows / 128 MiB, with nonwaiting atomic admission.
  End/Abort release cache ownership; consumers release their own references.
  These limits exclude wire buffers, live sessions, allocator overhead and RSS.
- Message-update EVENT capability is stored only on the exact active local session. Reconnecting requires negotiation again; unrelated sessions cannot enable it by UID alone.

## Read First

- [Registry](registry.go), [Slot barrier and pages](slot_generation.go)
- [Online types](types.go)

## Update Triggers

Update this file when connection lifecycle, owner-route projection, session
handles, touch batching, flush ownership, or diagnostics change.
