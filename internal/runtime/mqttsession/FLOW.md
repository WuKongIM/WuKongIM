---
scope: package
summary: Bounds MQTT owners, quiescence, consumer maintenance and delivery/connection/deadline/replay/Will scheduling.
---

# MQTT Session Runtime Flow

## Responsibility

This package bounds local execution/quiescence and schedules consumer, deadline, replay, Will and delivery work through injected usecases.
It does not authenticate users, acquire durable ownership, derive distributed
leases, publish messages, or interpret MQTT packets.

## Boundaries

- Activation/renewal callers prove the committed identity/revision and supply
  a conservative deadline from the same owner-local monotonic time base.
- The injected close callback seals writes and closes the physical transport;
  it must honor cancellation and cannot recursively wait for business cleanup.
- App composition owns the joined OwnerSweeper and durable deadline worker's
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
   per-session goroutine or full-registry scan. The managed sweeper defaults to 256 visits/250ms, with a 250ms turn deadline; Stop joins even slow callbacks.
5. Shutdown closes admission and cancels scopes before bounded cleanup pages;
   timeouts retain unfinished owners and permit a later exact retry.
6. One managed deadline loop rotates Session/Will index pages over currently led
   hash Slots, with bounded reads/visits and per-call/turn deadlines. Only Waiting
   Wills reach lifecycle reconciliation; detached publication work remains intact.
   Stop joins the exact run; restart after Stop gets fresh process cursors.
   A separate [Will scanner](will_worker.go) selects detached due keys; four workers call the authority-rereading executor. Bodies never enter its queue, pressure preserves cursors, and Stop joins the cohort before dependencies close.
7. Connections keeps one indexed schedule per registered owner and one bounded
   worker cohort. Renew requires a newer installed lease; disconnect keeps its
   original monotonic observation and first intent through exact cleanup retries.
8. One replay loop uses read kind 17 across led hash Slots, retaining tombstone sources for cleanup. It retains at most
   one finite recovery or retirement journal-scan continuation per Slot; work/errors yield to later sources.
   Cold passes rotate phases, targets and donor hints; durable storage owns progress.
   Reverse scans pin source/authority/capture/floor and strictly decrease; partial budgets preserve unstarted entries. Invalid/late pages dispatch nothing; commit counts never prove cleanup.
9. Deliveries retains one body-free task per exact Owner with a fixed cohort and
   indexed due heap. Progress yields to other due Owners; wakes coalesce during
   queued/executing work; idle polling recovers missed hints and failure backoff survives wakes. Fencing cannot discard cleanup.
10. Consumer maintenance rotates source-binding, pending-subscription and Session-reclamation streams over led Slots, with at most three cursors and one coverage hint per Slot and one fixed cohort of body-free keys. It accounts offline/full-window debt, projects ACK progress and retires proved closed obligations through usecases. Complete-page validation accepts terminal request cursors; pressure preserves unadmitted rows, subscription hints exclude timestamps, Preparing and Removing use the same bounded cohort and Stop joins all work. Timely successful establishment/removal confirmations feed separate fixed aggregate events, including retries; late/failed turns clear both outcomes and subscription revocation confirmation. Session discovery resumes one 64-row backfill page before strict kind-23 reads. Reclamation shares admission/Stop, retries durable work on wrap and counts only timely completion; callback panic cannot strand its key.

## Invariants and Failure Semantics

- Pending/closing owners count toward capacity; admitted operations are bounded.
- Lease expiry is an admission fence, not proof of drained in-flight work.
- Exact receipt retries cannot extend deadlines; stale/expired owners never reopen.
- Allocation/publication and retirement share one short lock. An absent issued
  ID from this exact boot is inactive; terminal drained registries mint retirement. Isolation delegates foreign boots
  of this same node only to explicit persisted proof. Reconstruction changes boot.
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
  Replay has the same joined lifecycle; source removal or lost Slot ownership
  discards hints. App must join it before stopping or restoring cluster dependencies.
- Connections Stop fences owner admission and makes one heap pass to expedite
  live cleanup without starving behind failed retries. Timeout retains that run;
  successful Stop joins registered work. App separately closes unregistered Owners.
  Both Connections and its owner registry are terminal after Stop, including restore.
- Deliveries Stop is terminal: cancel turns, skip queued business calls, join the
  scheduler/cohort, then release records. Timeout retains the run. App fences
  admission first and keeps dependencies alive until join; this is no isolation proof.

## Read First
- [Owner execution](owner.go)
- [Deadline worker](deadline_worker.go)
- [Connection supervision](connections.go), [Delivery scheduling](deliveries.go)
- [Replay worker](replay_worker.go)

## Update Triggers

Update when admission, deadline derivation, quiescence proof, resource bounds,
identity retirement, sweep fairness or shutdown changes.
