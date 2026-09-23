---
scope: package
summary: Coordinates MQTT Session lifecycle, subscription preparation and bounded shared replay copy/recovery turns.
---

# MQTT Session Usecase Flow

## Responsibility

This package connects authenticated connection intent to authoritative Session
metadata and the node-local Owners execution gate. Subscriptions coordinates
intent with an injected durable projection capability. This package owns no
packet, concrete cluster/gateway adapter, worker or shared replay storage.

## Boundaries

- Metadata uses narrow shared contracts implemented by the foreground-
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
7. Subscription establishment commits Preparing before projection, checks exact
   receipt/current child and permission, then commits Active. Removal commits
   Removing before closing matching work, and preserves outstanding exchanges.
   Same-lifetime owner resume reconciles stable intent; option replacement keeps
   its generation and operation. See the [failure inventory](../../../docs/specs/mqtt-subscription-orchestration.md).
8. Group source preparation installs an unknown-boundary source binding before
   confirming and saving its initial cursor boundary. Exact Session cursor CAS
   precedes binding activation; lost replies and owner resume preserve that
   boundary. It returns preparation evidence, never a subscription completion receipt.
9. Replay alternates bounded copy/anchor admission with replica recovery, using
   fresh placement and accepted progress. Targets pin anchors and retain scan/donor
   hints across continued turns; changed source/placement resets hints. Cold pass
   seeds rotate targets/phases/donors; results grant no release, readiness or SUBACK.

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
- Subscription counts use bounded pages at one parent revision; its CAS rejects
  concurrent admissions. Pending/removing rows consume quota; tombstones consume
  scan budget. Parent cancellation is checked synchronously at effect boundaries.
- Projection receipts are trusted-port assertions, not independent source proof.
  Product wiring requires replicated protection, initialized cursors, inbox future
  source admission and safe removal. Membership version changes cannot replace
  an active subscription silently; delivery/revocation ordering remains required.
- Group preparation performs bounded point reads and at most a two-row cursor
  check; it cannot create subscriptions, release content or authorize SUBACK.
- These usecases are not yet wired into the product listener; full process-level
  MQTT recovery and acceptance remain separate required implementation work.

## Read First
- [Contracts](types.go), [Acquisition](connect.go)
- [Lifecycle](lifecycle.go)
- [Subscription orchestration](subscriptions.go), [Replay coordination](replay.go)
## Update Triggers

Update when authentication/isolation ordering, lifecycle policy, lease derivation,
commit evidence, subscription projection, cleanup ownership or product composition changes.
