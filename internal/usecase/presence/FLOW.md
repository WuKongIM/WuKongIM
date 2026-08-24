---
scope: package
summary: Coordinates owner-local connection lifecycle with UID authority registration, actions, touch batching, and route lookup.
---

# Presence Use Case Flow

## Responsibility

This package owns entry-independent presence orchestration between the
owner-local online registry and the current UID authority client.
It does not own gateway frames, concrete cluster routing, or session storage.

## Boundaries

- It does not import gateway or protocol frames, cluster runtimes, access, or
  app composition.
- Concrete sessions remain local; authority calls use route projections and
  exact targets.
- The app touch worker drains dirty routes in bounded batches, avoiding one
  authority RPC per client ping.

## Main Flows

1. Activate resolves the UID Hash Slot, registers the local session pending,
   registers authority, executes returned owner actions, commits a pending
   token, performs the final local active recheck, then observes online status.
2. Deactivate snapshots active state, removes local indexes first, observes
   offline only for the last active owner-local session, then queues the exact
   authority unregister tombstone.
3. Lookup delegates one UID, a batch, or aligned exact-target groups to optional
   authority batch ports; group errors do not abort successful siblings.

## Invariants and Failure Semantics

- Failed authority registration removes the pending local session. Failed final
  activation queues exact unregister and removes local state.
- Status observation is best effort, never adds authority traffic, counts only
  active owner-local sessions, and skips offline when the prior session is unknown.
- Touch records owner-observed activity locally only.
- Production target-aware lookup preserves the complete fence and returns
  aligned results. The per-UID fallback exists only for limited compatibility
  implementations and cannot provide that target fence.
- Import-boundary tests reject concrete entry, protocol, cluster, and app imports.

## Read First

- [Activation](activate.go)
- [Deactivation](deactivate.go)
- [Lookup](lookup.go)
- [Touch](touch.go)
- [Ports](ports.go)

## Update Triggers

Update this file when activation rollback, conflict actions, status observation,
deactivation ordering, touch ownership, lookup alignment, or imports change.
