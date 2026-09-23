---
scope: package
summary: Bounds owner-local MQTT reservations, monotonic execution admission, exact quiescence, deadline cleanup and shutdown.
---

# MQTT Owner Execution Flow

## Responsibility

This package tracks local connection execution and exact-owner quiescence.
It does not authenticate users, acquire durable ownership, derive distributed
leases, publish messages, or interpret MQTT packets.

## Boundaries

- Activation/renewal callers prove the committed identity/revision and supply
  a conservative deadline from the same owner-local monotonic time base.
- The injected close callback seals writes and closes the physical transport;
  it must honor cancellation and cannot recursively wait for business cleanup.
- App composition owns periodic bounded sweeps and joined shutdown.

## Main Flows

1. Reserve publishes a bounded pending identity with a new connection ID;
   activation opens execution only for its exact identity and committed receipt.
2. Begin checks the active local lease and per-owner capacity synchronously.
   A scope retains ownership through all effects; explicit Done releases it.
3. Quiesce permanently fences admission, cancels scopes and coalesces transport
   close. It returns success only after transport closure and all scopes drain.
4. One indexed heap entry per retained owner schedules pending expiry, lease
   expiry or cleanup retry. A sweep visits at most 256 due owners, without a
   per-session goroutine or full-registry scan.
5. Shutdown closes admission and cancels scopes before bounded cleanup pages;
   timeouts retain unfinished owners and permit a later exact retry.

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

## Read First

- [Owner execution](owner.go)
- [Deadline and shutdown ownership](owner_deadlines.go)
- [Failure inventory](../../../docs/specs/mqtt-owner-execution.md)

## Update Triggers

Update when admission, deadline derivation, quiescence proof, resource bounds,
identity retirement, sweep fairness or shutdown changes.
