---
scope: package
summary: Coordinates MQTT Session lifecycle, subscription preparation, consumer progress and bounded replay copy/recovery/retirement turns.
---

# MQTT Session Usecase Flow

## Responsibility

This package connects authenticated intent to Session metadata and node-local
Owners gates. Subscriptions coordinates intent with durable projection. It owns
no packet, concrete cluster/gateway adapter, worker or shared replay storage.

## Boundaries

- Metadata uses narrow shared contracts on the foreground-gated Node; product reads never fall back to local storage.
- Device-token verification reuses user policy without WK master-device kicks.
- Will permission is checked at setup and execution; explicit denial differs from authority or infrastructure failure.
- Isolation requires exact owner quiescence or another valid proof supplied by
  its port. A stored state, lease expiry, foreign boot or RPC error is not proof.
- App owns composition, bounded contexts, renewal/sweep scheduling and unavailable-owner/restore fencing before product admission.

## Main Flows

1. CONNECT validates bounded owned input and credentials, reads the binding,
   isolates the observed old owner and rereads authority. A changed owner fails;
   the usecase does not loop through evicting new successors.
2. Expired owners record abnormal disconnect at their execution deadline; recovery never restarts Will/offline clocks.
3. Recheck credentials/Will permission, reserve a bounded local candidate, capture
   its monotonic deadline before proposal, atomically commit Session/Will and
   then activate. Any failed candidate is fenced and given bounded close cleanup.
4. Renew inside an admitted scope, preserve delivery/Will state, commit exact
   revision and install the original deadline. Confirmed loss/clock failure
   fences immediately; unconfirmed writes cannot extend local execution.
5. Disconnect captures observation before isolation outside the caller's scope,
   then rereads ownership and commits Will/expiry without restarting clocks or changing normal intent.
   Late disconnect never changes a successor; original zero expiry cannot extend.
   Queued entry cleanup supplies an optional trusted local monotonic observation;
   wall-only or future values fail before isolation and are never client input.
6. Reconcile one complete-owner deadline candidate against current authority.
   Active expiry still requires exact isolated disconnect; offline Will Delay and
   expiry use one coherent Session/Will read and at most one lifecycle commit.
   Ready work detaches before lifetime expiry and survives later Session ending.
7. Subscription establishment commits Preparing, checks projection/current child
   and permission, then Active. Removal commits Removing before SourceDrain fixes
   the accounting end and releases only unadmitted backlog; exchanges survive.
   Group removal discovers its original cursor or registers missing preparation before protected cancellation Init.
   Resume preserves intent; replacement keeps generation/operation. See the [failure inventory](../../../docs/specs/mqtt-subscription-orchestration.md).
8. Group preparation registers an unknown binding before fresh protection confirmation,
   fixes one start, initializes the cursor and activates the binding. Retries preserve that start; this is no subscription completion receipt.
9. Replay maintenance rotates bounded copy/anchor admission, recovery and retirement under fresh placement.
   Targets pin anchors and scan/donor hints; placement resets hints and cold passes rotate work.
   Fenced turns only recover existing anchors; absent anchors yield without copying.
   Recovery applies committed retirement and releases sources; pending cleanup yields without granting new GC, readiness or SUBACK authority.
10. Consumer progress reads one binding and a pinned Session/cursor, projecting
    only contiguous completion with one CAS. Unchanged floors produce no write.
    Explicit lifetime end retains Removing without fabricating source release;
    offline/absent state cannot discharge responsibility or authorize content GC.
    SourceRemoval revalidates ended-lifetime or closed-subscription/drained-cursor
    proof, acknowledges one exact binding revision on its source Slot, then
    revalidates before a separate Removed commit. Tombstones remain; aggregate
    Channel protection and shared-content retirement are unaffected.
11. Retention captures an accepted anchor before the strict minimum-consumer read.
    Retirement reruns this plan per turn, selects whole anchors and submits a routed decision; unknown/removing obligations limit the floor.
    Finite continuations retain capture/floor while fresh permission covers it; changed authority/lower floors yield. Maintenance keeps phase hints separate and preserves replica rotation; no local GC is granted.

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
- Group preparation bounds point/cursor reads; it cannot create subscriptions, release content or authorize SUBACK.
- These usecases are not yet wired into the product listener; full process-level
  MQTT recovery and acceptance remain separate required implementation work.

## Read First
- [Contracts](types.go), [Acquisition](connect.go), [Lifecycle](lifecycle.go), [Subscriptions](subscriptions.go), [Replay](replay.go)
## Update Triggers

Update when authentication/isolation ordering, lifecycle policy, lease derivation,
commit evidence, subscription projection, cleanup ownership or product composition changes.
