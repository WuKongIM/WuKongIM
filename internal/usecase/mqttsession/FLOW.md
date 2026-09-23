---
scope: package
summary: Coordinates authenticated MQTT Session acquisition, exact-owner isolation, atomic Will decisions, leases, disconnect and deadline reconciliation.
---

# MQTT Session Usecase Flow

## Responsibility

This package connects authenticated connection intent to authoritative Session
metadata and the node-local Owners execution gate. It owns no packet, concrete
cluster/gateway adapter, background worker or shared replay implementation.

## Boundaries

- Metadata uses three narrow shared contracts implemented by the foreground-
  gated cluster Node; product reads never fall back to local storage.
- Device-token verification reuses user policy without WK master-device kicks.
- Will permission is checked at setup and must be checked again on execution;
  explicit denial remains distinct from authority or infrastructure failure.
- Isolation requires exact owner quiescence or another valid proof supplied by
  its port. A stored state, lease expiry, foreign boot or RPC error is not proof.
- App owns composition, bounded request contexts, renewal/sweep scheduling and
  unavailable-owner/restore fencing before enabling product MQTT admission.

## Main Flows

1. CONNECT validates bounded owned input and credentials, reads the binding,
   isolates the observed old owner and rereads authority. A changed owner fails;
   the usecase does not loop through evicting new successors.
2. An expired active owner first records abnormal disconnect at its recorded
   execution deadline. Will due time and offline lifetime do not restart at recovery.
3. Recheck credentials/Will permission, reserve a bounded local candidate, capture
   its monotonic deadline before proposal, atomically commit Session/Will and
   then activate. Any failed candidate is fenced and given bounded close cleanup.
4. Renew inside an admitted scope, preserve delivery/Will state, commit exact
   revision and install the original deadline. Confirmed loss/clock failure
   fences immediately; unconfirmed writes cannot extend local execution.
5. Disconnect captures observation before blocking isolation outside the caller's
   scope, rereads exact ownership and commits Will/expiry without restarting their
   clock or changing normal intent because isolation took time.
   Late disconnect never changes a successor; original zero expiry cannot extend.
   Queued entry cleanup supplies an optional trusted local monotonic observation;
   wall-only or future values fail before isolation and are never client input.
6. Reconcile one complete-owner deadline candidate against current authority.
   Active expiry still requires exact isolated disconnect; offline Will Delay and
   expiry use one coherent Session/Will read and at most one lifecycle commit.
   Ready work detaches before lifetime expiry and survives later Session ending.

## Invariants and Failure Semantics

- ClientID stays UID-bound through expiry/Clean Start. Different IDs coexist.
- Resume preserves counters/allocators and lifetime quotas; a new lifetime
  resets them. A lower Receive Maximum does not delete old inflight exchanges.
- Local deadlines retain monotonic time. Stored milliseconds round upward so
  the local gate does not outlive its recorded upper bound; neither is remote
  isolation evidence. Clock regression, overflow and late installation fail.
- Committed replies must match the expected revision and Will decision. A
  timeout/conflict never activates a candidate or triggers an unbounded retry.
- Cancellation alone does not release scopes; renewal releases its scope on all
  exits, including dependency panic, before local fencing/cleanup may join it.
- Close errors retain runtime capacity for bounded cleanup. No token is stored
  in durable rows, local owner claims or the returned connection.
- Deadline scans must page both Session deadlines and Waiting Will deadlines.
  Stale candidates, missing referenced work, changed authority and uncertain
  commits cannot erase an obligation; publication scheduling remains separate.
- These usecases are not yet wired into the product listener; full process-level
  MQTT recovery and acceptance remain separate required implementation work.

## Read First

- [Contracts](types.go), [Acquisition](connect.go)
- [Lifecycle](lifecycle.go), [Deadline reconciliation](deadlines.go)
- [Failure inventory](../../../docs/specs/mqtt-session-acquisition.md)

## Update Triggers

Update when authentication/isolation ordering, lifecycle policy, lease derivation,
commit evidence, cleanup ownership or product composition changes.
