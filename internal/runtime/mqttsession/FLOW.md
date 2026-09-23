---
scope: package
summary: Bounds MQTT owner-local execution, exact quiescence, cleanup and fair durable-deadline scheduling with joined shutdown.
---

# MQTT Session Runtime Flow

## Responsibility

This package tracks local connection execution and exact-owner quiescence,
and schedules bounded durable deadline candidates through an injected usecase.
It does not authenticate users, acquire durable ownership, derive distributed
leases, publish messages, or interpret MQTT packets.

## Boundaries

- Activation/renewal callers prove the committed identity/revision and supply
  a conservative deadline from the same owner-local monotonic time base.
- The injected close callback seals writes and closes the physical transport;
  it must honor cancellation and cannot recursively wait for business cleanup.
- App composition owns owner-registry sweeps and the durable deadline worker's
  start/stop ordering. The worker does not renew live owners or publish Wills.
  Connections separately owns bounded live renewal and queued disconnect through
  app-adapted usecases; entry callbacks only register or accept immutable intent.

## Main Flows

1. Reserve publishes a bounded pending identity with a new connection ID;
   activation opens execution only for its exact identity and committed receipt.
2. Begin checks the active local lease and per-owner capacity synchronously.
   A scope retains ownership through all effects; explicit Done releases it.
   UID reads its immutable authenticated principal; Check revalidates the live
   lease before another effect without consuming a second operation slot.
3. Fence closes admission/cancels scopes without waiting; it supplies no isolation
   proof. Quiesce also coalesces transport close and succeeds only after physical
   closure and every admitted scope drain.
4. One indexed heap entry per retained owner schedules pending expiry, lease
   expiry or cleanup retry. A sweep visits at most 256 due owners, without a
   per-session goroutine or full-registry scan.
5. Shutdown closes admission and cancels scopes before bounded cleanup pages;
   timeouts retain unfinished owners and permit a later exact retry.
6. One managed deadline loop rotates Session/Will index pages over currently led
   hash Slots, with bounded reads/visits and per-call/turn deadlines. Only Waiting
   Wills reach lifecycle reconciliation; detached publication work remains intact.
   Stop joins the exact run; restart after Stop gets fresh process cursors.
7. Connections keeps one indexed schedule per registered owner and one bounded
   worker cohort. Renew requires a newer installed lease; disconnect keeps its
   original monotonic observation and first intent through exact cleanup retries.

## Invariants and Failure Semantics

- Pending/closing owners count toward capacity; admitted operations are bounded.
- Lease expiry is an admission fence, not proof of drained in-flight work.
- Exact receipt retries cannot extend deadlines; stale/expired owners never reopen.
- Allocation/publication and retirement share one short lock. An absent issued
  ID from this exact registry boot is inactive; future IDs or foreign boots fail
  closed. Registry reconstruction must change boot identity.
- Callback errors/panics retain fenced state and capacity; no transport callback or wait
  runs under the registry lock. Panic/error diagnostics contain no callback text.
- Diagnostics use constant-time aggregate counters. Timer renewal fixes the
  existing heap entry rather than appending stale deadline records.
- A scope must not escape into untracked effects; cancellation alone does not
  end it. Gateway adaptation and distributed takeover remain separate work.
- MarkUncertain before Done retains an unresolved-effect barrier when a dependency
  may still execute. Physical closure and local drain wake quiescence waiters with
  isolation-unproved, never success. The bounded owner remains retained; no time,
  sweep, shutdown or lease refresh clears it. Recovery needs separate proof.
- Scan hints advance only past visited candidates, including failures; durable
  rows retry after wrap. Future boundaries reset the stream, lost Slots lose
  cursors, and invalid pages or late discovery results authorize no new effects.
- A stopping deadline loop cannot overlap a restart. No per-session task or
  unbounded queue is added; observation contains only aggregate counts/duration.
- Connections Stop fences owner admission and makes one heap pass to expedite
  live cleanup without starving behind failed retries. Timeout retains that run;
  successful Stop joins registered work. App separately closes unregistered Owners.
  Both Connections and its owner registry are terminal after Stop, including restore.

## Read First

- [Owner execution](owner.go)
- [Deadline and shutdown ownership](owner_deadlines.go)
- [Failure inventory](../../../docs/specs/mqtt-owner-execution.md)
- [Deadline worker](deadline_worker.go)
- [Connection supervision](connections.go)

## Update Triggers

Update when admission, deadline derivation, quiescence proof, resource bounds,
identity retirement, sweep fairness or shutdown changes.
