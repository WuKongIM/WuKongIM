---
scope: package
summary: Coordinates owner-local connection lifecycle with UID authority registration, actions, touch batching, and route lookup.
---

# Presence Use Case Flow

## Responsibility
Entry-independent orchestration connects the online registry and UID authority; frames, cluster routing and session storage belong elsewhere.

## Boundaries
- It does not import gateway/protocol frames, cluster runtimes, access or app.
- Concrete sessions remain local; authority calls use route projections/exact targets.
- The app touch worker batches dirty routes instead of one RPC per client ping.

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
   At most sixteen actual owner batches execute. Legacy batches take queued
   work up to sixteen independent calls, 256 targets and 512 UIDs without a timer. Legacy providers retain the sixteen-original-call result
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
5. Full-mode wiring uses `NewSlotRecovery(directory, source, owners)` as both
   ports of `NewAuthorityRecovery(slots, slots)`. This preserves the original
   caller/input admission and shares the same decoded result ledger across
   staging, owner pages and original-call output. Sixteen physical page turns
   execute; after each page a continuation returns to the original FIFO tail.
   Same-slot followers share one scan; paused scans retain prepaid staging and
   original caller/key credits. No unbounded waiter queue is added. Each scan
   has one fixed two-second deadline and each owner one fixed one-second budget
   across Begin/pages/End. Every owner End and current applied claim must validate
   before publication. A failed attempt needs a new G.
6. `SlotRecovery` owns strict registration, commit, abort, touch and unregister
   orchestration. Infrastructure forwards these DTOs; it must not rebuild an
   activation receipt or pending claim from a later route resolution. Strict
   activation atomically uses `MarkActiveWithReceipt`; absent/stale receipts fail
   closed. Only a typed, pre-mutation owner mismatch permits one shared rebuild
   under the original context; retry compares actual cuts and cannot revoke again.
7. `OwnerGeneration` validates Begin freshness before the local atomic cut.
   `NewOwnerGenerationWithChecks` separates that freshness check from local
   current-gate checks around pages, End and bounded suspect reads. Repeated
   pages must not issue repeated Controller quorum probes. Page handles retain
   the registry's real replay backing until `Release`; all-owner rechecks keep
   actual activity timestamps and cannot skip unavailable owners.

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
- Flights store no routes. Successful callers reread current Directory routes.
  Only the exact built-in Directory captures a caller-local known/missing mask;
  known proof survives residency eviction within that request, while original
  misses need current proof and the same local authority instance. Wrappers keep
  their original read contract. Capture metadata is charged to caller admission.
  Shared execution has its own cancel root: caller cancellation cannot cancel
  live UID followers or other
  batch members, and every caller still returns on its own original context.
  Only all-member cancellation or Close cancels actual execution. UID proofs
  with no live owner are not installed; exact authority fences still govern all
  installation. Old and new peer compatibility never merges per-call route caps.
  If proof appears between the initial lookup and missing-UID selection, an empty
  missing set requires a fresh read under the same target fence. It never implies
  offline. Only groups joining flights reread after work completes; a cold sibling
  cannot retry or erase another group's failed readback. No RPC or retry is added.
- Canceled queued work releases its ledger only after actual ownership ends.
  Full continuations retain cleanup ownership through the same bounded executors.
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
  validate shape and prepay exact backing before decoding. Legacy wire operations
  remain unchanged; Full uses separate explicit generation operations. There is
  no page retry, deadline reset or partial offline publication. Published
  Directory data owns its own lifetime.
- Always-on result-budget gauges report charged rows/bytes, peaks and rejections.
  Full turns explicitly mark the unsupported legacy structure census unobserved.
- A fixed aggregate census separates FIFO, physical batches, original-call
  result credits and UID admission bytes. When existing RPC diagnostics are
  enabled, partial owner accumulators and transient decoded pages remain
  observed until real ownership returns; an explicit enabled gauge distinguishes
  unobserved zeros when this additional accounting is disabled.
  Structural decoded bytes count visible slice capacity and live route strings,
  charging aliases per reference; they exclude wire buffers, maps, errors,
  hidden backing prefixes, allocator overhead and RSS and cannot set a heap limit.
- Import-boundary tests reject concrete entry, protocol, cluster, and app imports.
- Full mode requires every obligated owner to implement its explicit operations.
  Unsupported/malformed/timeout responses never create a Full certificate and
  are not interpreted as empty legacy pages. Negotiating a separate legacy
  generation is outside `SlotRecovery`; no implicit same-generation downgrade
  is implemented. Membership/boot changes must invalidate the old serving gate.
- Shutdown closes the outer `AuthorityRecovery` and then `SlotRecovery`, joining
  actual provider ownership before releasing its shared result credits. A caller
  returning on cancellation alone is not that join.

## Read First
- [Activation](activate.go)
- [Recovery admission](recovery_flights.go)
- [Full-slot recovery and strict mutations](slot_recovery.go)
- [Owner generation validation](owner_generation.go)
- [Generation ports and result lifetimes](generation_ports.go)

## Update Triggers
Update when activation, conflicts, observation, deactivation, touch, lookup or imports change.
