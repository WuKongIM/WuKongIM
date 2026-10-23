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
3. Authority recovery owns a FIFO of at most 512 waiting-plus-active caller
   entries. Joint admission bounds 8,192 distinct fenced UID keys, 128 MiB of
   cloned UIDs plus scheduler/caller metadata, and 32 MiB per new UID subset;
   any full dimension fails closed without retaining a partial reservation.
   At most sixteen actual owner batches execute. Each takes already queued
   work, up to sixteen independent calls, 256 targets and 512 UIDs, without a
   collection timer. A separate sixteen-original-call result ledger preserves
   each call's independent 4,096-route availability and remains owned through
   actual provider completion, fenced installation and result-reference release.
   These ledgers are not a process RSS or transport/Directory heap limit.
   Newly owned proofs publish before callers join older flights, preventing
   overlapping-call worker cycles. Legacy owner ports run each original call
   separately. Proven lookups bypass admission; failed groups preserve siblings.
4. Lookup delegates one UID, a batch, or aligned exact-target groups to optional
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
- Flights store no routes. Successful callers reread current Directory proof,
  including after eviction. Shared execution has its own cancel root: one
  caller's cancellation/deadline does not cancel live UID followers or other
  batch members, and every caller still returns on its own original context.
  Only all-member cancellation or Close cancels actual execution. UID proofs
  with no live owner are not installed; exact authority fences still govern all
  installation. Old and new peer compatibility never merges per-call route caps.
- Canceled queued work leaves the physical FIFO before releasing its ledger.
  Active ownership and result credits survive caller cancellation until the
  real provider returns. Close stops admission, cancels queued/active work and
  joins actual ownership; timeout reports incomplete cleanup and permits a
  later Close to join again. Workers and dispatchers start on demand and exit
  when their currently ready work ends.
- Caller/index metadata remains charged while a caller waits on old flights.
  Completed flights drop keys, user maps and execution/creator references;
  their minimal retired status/channel/work is covered by caller reference charges.
  Remaining user maps shrink with live ownership rather than retaining historic
  follower peaks. Cancellation checks each affected execution once.
- Optional RPC phase tracing measures actual FIFO/capacity admission waiting,
  unfinished shared-flight waits and proof installation. Idle dispatch turns and
  Directory reads are not labeled quota waits. Shared multi-call execution
  invents no first-caller trace parent or cancellation root.
- A fixed aggregate census separates FIFO, physical batches, original-call
  result credits and UID admission bytes. When existing RPC diagnostics are
  enabled, partial owner accumulators and transient decoded pages remain
  observed until real ownership returns; an explicit enabled gauge distinguishes
  unobserved zeros when this additional accounting is disabled.
  Structural decoded bytes count visible slice capacity and live route strings,
  charging aliases per reference; they exclude wire buffers, maps, errors,
  hidden backing prefixes, allocator overhead and RSS and cannot set a heap limit.
- Import-boundary tests reject concrete entry, protocol, cluster, and app imports.

## Read First

- [Activation](activate.go)
- [Lookup](lookup.go)
- [Recovery admission](recovery_flights.go)
- [Independent owner call pages](owner_recovery_calls.go)
- [Ports](ports.go)

## Update Triggers

Update this file when activation rollback, conflict actions, status observation,
deactivation ordering, touch ownership, lookup alignment, or imports change.
