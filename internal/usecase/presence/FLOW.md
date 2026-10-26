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
   collection timer. Legacy providers retain the sixteen-original-call result
   guard. Fully bounded local/remote/old-protocol providers instead prepay exact
   decoded backing and cloned strings against a separate 128 MiB / 196,608 row
   capacity ledger. Each original call still permits at most 4,096 routes.
   Page, scratch and old/new accumulator backing coexist under that same ledger;
   resource denial fails the physical round without waiting for more capacity.
   Credits remain owned through provider completion, fenced installation and
   reference release. Neither ledger is a process RSS or transport/Directory
   heap limit; fixed input selector maps and allocator overhead remain excluded.
   Newly owned proofs publish before callers join older flights, preventing
   overlapping-call worker cycles. Legacy owner ports run each original call
   separately. Proven lookups bypass admission; failed groups preserve siblings.
4. Lookup delegates one UID, a batch, or aligned exact-target groups to optional
   authority batch ports; group errors do not abort successful siblings.

## Invariants and Failure Semantics

- Activation failures retain their original error family through fixed phase
  wrappers. Diagnostic extraction returns the innermost known phase, never request data.
  Registration failure may carry an anonymous owner-route digest; malformed or
  absent owner identity stays unknown. Shared provider failures can reference one
  actual terminal owner RPC without claiming an exclusive logical caller.
  Recovery distinguishes admission, provider, validation, installation and
  Directory readback; execution cancellation keeps its existing not-ready mapping.
  Immutable provider evidence survives normalization; a later physical-context
  override keeps only the same global or aligned group failure capture, never
  another group's cause. Masks distinguish unobserved stages from measured zero.
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
  If proof appears between the initial lookup and missing-UID selection, an empty
  missing set requires a fresh read under the same target fence. It never implies
  offline. Only groups joining flights reread after work completes; a cold sibling
  cannot retry or erase another group's failed readback. No RPC or retry is added.
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
- The optional bounded provider capability is fixed at construction only after
  local registry projection, remote decoding and old-protocol fallback all use
  prepaid allocation. Local pages reuse small growing scratch; remote replies
  validate shape and count exact backing before decoding. No wire changes,
  automatic RPC retries, per-page timeout reset or partial offline publication
  are introduced. Published Directory data owns its own lifetime.
- Always-on fixed budget gauges report charged row capacity and bytes, peaks
  and rejected reservations even when diagnostic sampling is disabled.
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
