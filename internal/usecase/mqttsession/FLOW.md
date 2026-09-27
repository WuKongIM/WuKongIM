---
scope: package
summary: Coordinates MQTT Session lifecycle, subscription preparation, consumer progress and bounded replay copy/recovery/retirement turns.
---

# MQTT Session Usecase Flow

## Responsibility

This package coordinates Session lifecycle, Owners, subscriptions and delivery; it owns no packet, concrete adapter, worker or replay storage.

## Boundaries

- Metadata uses narrow shared contracts on the foreground-gated Node; product reads never fall back to local storage.
- Device-token verification reuses user policy without WK master-device kicks.
- Will permission is checked at setup and execution; explicit denial differs from authority or infrastructure failure.
- WillExecutor rereads detached work and claims one exact revision. Applied claims resume Preparing/Prepared work through current policy and freeze hook output before a definite Started CAS grants dispatch. Started/legacy work only recovers positive receipts without reauthorization, redispatch or inferred rejection. Four turns run concurrently without a waiting queue; local deadlines precede CAS and gate subsequent effects.
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
5. Disconnect captures observation before isolation outside every owner scope, preserving Will/expiry clocks and normal intent.
   Original zero expiry cannot extend. Queued cleanup may supply a trusted monotonic observation;
   wall-only or future values fail before isolation and are never client input.
   [End](end.go) requires exact isolation even for ended rows, then atomically ends Session/Will while retaining delivery/source debt.
   Neither follows a successor. End retains the first reason; expired active owners record the original disconnect first.
6. Reconcile one complete-owner deadline candidate against current authority.
   Active expiry still requires exact isolated disconnect; offline Will Delay and
   expiry use one coherent Session/Will read and at most one lifecycle commit.
   Ready work detaches before lifetime expiry and survives later Session ending.
7. Subscription establishment commits Preparing, checks projection/current child
   and permission, then Active. Removal commits Removing before SourceDrain fixes
   the accounting end and releases only unadmitted backlog; exchanges survive. Removing intent permits at most three writes after definite CAS rejection, only while fresh same-Owner reads retain the identical child and advance the parent revision.
   Qualified cursors release one range per turn; group removal recovers its cursor or registers missing preparation before protected cancellation Init. SubscriptionRemoval completes frozen Removing intent without live admission; foreground completion accepts only the identical already-Removed child.
   Resume preserves intent; replacement keeps generation/operation. See the [failure inventory](../../../docs/specs/mqtt-subscription-orchestration.md).
   SubscriptionRequests waits only for explicit pending work within attempts/deadline. Possible writes and pending intent mark errors Unconfirmed; entry cannot return a definitive negative ACK. Comment-only failpoints bracket committed Removing intent and the final completion CAS in temporary instrumented builds.
8. Group preparation registers an unknown binding before fresh protection confirmation,
   fixes one start, initializes the cursor and activates the binding. GroupProjection adds all-replica replay confirmation and current intent/permission checks before a receipt.
   InboxSources prepares one canonical person source from fresh qualification, including offline Sessions; closed intent retains cleanup debt. InboxAdmission waits for pinned native directory readiness, scans one bounded participant page, commits each prepared candidate and rechecks its incarnation. InboxAppender drives bounded preparation before ordinary person writes and pins its exact fresh checkpoint/authority into durable append; pending work survives timeout. InboxEstablishment commits UID qualification before one bounded initial-directory page; each protected cursor needs all-replica confirmation before discovery advances, and exact intent/permission precede its receipt. InboxRemoval closes UID qualification before bounded cursor draining, persists each completed source independently and retains exchanges and cursorless cleanup debt. RemoveClosed shares these stages offline and passes the captured Owner into nested source cleanup.
9. Replay maintenance rotates bounded copy/anchor admission, recovery and retirement under fresh placement.
   Targets pin anchors and scan/donor hints; placement resets hints and cold passes rotate work.
   Fenced turns only recover existing anchors; absent anchors yield without copying.
   Confirmation captures one anchor, checks every replica and rechecks placement; partial recovery remains pending. Pre-anchor copy and replica-recovery readiness/pressure yield explicit pending within the existing request bound; cancellation, unknown errors and anchor failures are not retried. Maintenance grants no consumer GC or SUBACK authority.
10. Accounting reads bounded anchored originals for online/offline debt, preserving QoS/No Local/expiry and exact revisions; quota ending proves no owner isolation.
    WindowAdmission checks originals/options and exact charges; original QoS 0 preclaims once. ExchangeRecovery reads begun exchanges across unsubscribe with original authorization/content identity.
    Sender serializes old recovery before new admission and checks final receive permission; ambiguity closes without retry. Only its private proved-enqueued QoS-0 token may rebase completion across unrelated revisions.
    [DeliveryCoordinator](delivery_coordinator.go) rotates one subscription/source per turn, then accounts and sends; bounded body-free hints survive source failures. Revocation/quota cleanup runs after scopes release, including lost quota replies.
    ConsumerMaintenance accounts Channel debt independently of online/window state, drains closed sources through owner-independent ReconcileClosed, then projects or removes. Quota/revocation cleanup targets the captured Owner, including lost replies. UID work projects only explicit ended/new lifetimes; separate removal retains discovery/drain evidence and grants no Channel release.
    Consumer progress reads one binding and a pinned Session/cursor, projecting only contiguous completion with one CAS. Acknowledgements checks current owner and exact PacketID/order before one command-70 commit; absent exchanges cause no writes.
    Explicit lifetime end retains Removing without fabricating source release; offline/absent state cannot discharge responsibility or authorize content GC.
    SourceRemoval revalidates ended-lifetime or closed-subscription/drained-cursor
    proof, acknowledges one exact binding revision on its source Slot, then
    revalidates before a separate Removed commit. UID termination needs no Channel acknowledgement. Tombstones remain; aggregate
    Channel protection and shared-content retirement are unaffected.
11. Retention captures an accepted anchor before the strict minimum-consumer read.
    Retirement reruns this plan per turn, selects whole anchors and submits a routed decision; unknown/removing obligations limit the floor.
    Finite continuations retain capture/floor while fresh permission covers it; changed authority/lower floors yield. Maintenance keeps phase hints separate and preserves replica rotation; no local GC is granted.

## Invariants and Failure Semantics

- ClientID stays UID-bound through expiry/Clean Start. Different IDs coexist. Resume preserves counters/allocators and lifetime quotas; a new lifetime
  resets them. A lower Receive Maximum does not delete old inflight exchanges.
- Local deadlines retain monotonic time. Stored milliseconds round upward so
  the local gate does not outlive its recorded upper bound; neither is remote
  isolation evidence. Clock regression, overflow and late installation fail.
- Committed replies must match the expected revision and Will decision. A
  timeout/conflict never activates a candidate or triggers an unbounded retry.
- Cancellation alone does not release scopes; renewal releases its scope on all exits, including dependency panic, before local fencing/cleanup may join it.
- Close errors retain runtime capacity for bounded cleanup. No token is stored
  in durable rows, local owner claims or the returned connection.
- Deadline scans must page both Session deadlines and Waiting Will deadlines.
  Stale candidates, missing referenced work, changed authority and uncertain
  commits cannot erase an obligation; Will publication scheduling and fenced uncertain-dispatch recovery remain separate.
- Subscription counts use bounded pages at one parent revision; its CAS rejects
  concurrent admissions. Pending/removing rows consume quota; tombstones consume
  scan budget. Parent cancellation is checked synchronously at effect boundaries.
- Projection receipts are trusted-port assertions, not independent source proof.
  Product wiring requires replicated protection, initialized cursors, inbox future
  source admission and safe removal. Membership version changes cannot replace
  an active subscription silently; delivery/revocation ordering remains required.
- Group preparation bounds reads and grants no subscription, release or SUBACK authority. ACK survives unsubscribe; entry binds sent exchanges first.
- ReceiveAuthorization reads a fresh coherent Slot channel/member/sequence view; group grants use join incarnation, self inbox uses admitted UID. Send mutes do not deny receiving; ambiguous evidence cannot revoke.
- App now composes an opt-in product listener with process-level interop coverage. Will and consumer scheduling invoke existing policy; pending-removal recovery now shares the consumer cohort; ended-record reclamation and full failure/scale acceptance remain required.

## Read First
- [Contracts](types.go), [Acquisition](connect.go), [Lifecycle](lifecycle.go), [Subscriptions](subscriptions.go), [Replay](replay.go)
## Update Triggers
Update when lifecycle, isolation, accounting, commit evidence, subscription projection, cleanup or composition changes.
