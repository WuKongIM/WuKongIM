# MQTT source binding tombstone retirement

Removed table-26 rows currently serve two duties: they fence delayed
Preparing/Removed inserts from resurrecting a binding, and they keep replay
cleanup discoverable after the last consumer leaves
([tombstone source discovery](mqtt-tombstone-source-discovery.md)). Retaining
every row forever grows metadata with subscription churn. This splits both
duties into bounded durable records, then deletes completed rows.

## Why a subscription watermark is unsafe

Subscription generation equals the Session revision at creation. It is
monotonic per Session, not per topic: an older, still-unprepared subscription
on another topic may have a lower generation than a removed one. A per-client
subscription watermark would reject its legitimate first prepare. Only an
**ended Session lifetime** is irreversible for all of its subscriptions.

## Records (table 26, source Slot, no new table)

1. **Closed-lifetime fence**, System 2, key `(owner kind, owner id, owner
   generation, namespace, client id)`, value `ClosedThrough` Session generation
   plus revision/updated time. Monotonic; never regresses. Any binding insert
   with `SessionGeneration <= ClosedThrough` is a CAS conflict, exactly as an
   existing Removed tombstone is today.
2. **Replay source marker**, System 3, key = Channel owner prefix. Written in the
   same batch that deletes the last primary row of that owner. Kind 17 merges
   primary owners and markers in owner order, still at most limit+1 witnesses.
   Markers are hints; replay retirement removes a marker only through its
   existing fresh minimum-consumer/anchor proof when the source is fully retired.

## Retirement operation

`RetireMQTTSourceBinding(slot, key, expectedRevision, closedThrough)` in one
source-Slot batch:

- requires the row to be Removed at `expectedRevision` with
  `ProtectionRevision` acknowledged, and `key.SessionGeneration <=
  closedThrough`;
- raises the fence to `max(old, closedThrough)`;
- writes the replay marker when the owner is a Channel;
- deletes the primary row (Removed rows already carry no secondary indexes).

The usecase supplies `closedThrough` only from a fresh pinned Session-Slot read
showing that generation Ended or superseded by a newer generation. UID
qualification rows use the fence only; they have no replay marker.

Live Session (unsubscribe) tombstones retire through a second fence field,
`(LiveSessionGeneration, SubscriptionThrough)`, stored in the same System-2
record. The usecase must prove from fresh reads that the Session is still the
same generation and every subscription of it with generation <= G is Removed
(bounded to 16 pages of 64). New subscriptions take `Session.Revision + 1`, so
all live or Preparing subscriptions are above G and are never fenced. The fence
rejects first inserts with `SessionGeneration == S && SubscriptionGeneration <= G`
and only moves forward; a lower live Session generation is ignored. A single
command carries exactly one of `closed_through` or `live_subscription_through`.
An old subscription that is never unsubscribed holds the watermark below it.

Rows are discovered by the existing bounded maintenance scans; retirement runs
at most one CAS per bounded turn and yields under Slot pressure. Snapshot,
restore and inspect cover Systems 2/3 as registered system spans.

## Failure inventory before implementation

1. Retirement of a Removing, unacknowledged or live-Session row; a stale or
   missing Session read treated as ended; clock/lease used as proof.
2. Delayed Preparing or Removed insert for a retired generation succeeds, or
   the fence regresses on retry, snapshot, restore or lost response.
3. A subscription with lower generation on a live Session is fenced.
4. Last-row deletion loses replay discovery; markers duplicate owners, break
   encoded order, exceed witness bounds, or are treated as consumer evidence.
5. Partial batch (row deleted, fence/marker not written) after crash.
6. Snapshot/restore/inspect omits Systems 2/3; old peers accept kind 17 without
   marker semantics (all binaries must match, as for kind 17 today).
7. Unbounded scan or retirement storm under 100k-member group churn.

## Replay marker clearing (deferred)

`ClearMQTTReplayMarker` (Slot command 78) deletes a System-3 marker only while
the owner has no binding rows. No caller invokes it yet. Local replay copies may
run ahead of any committed anchor (`CopyMQTTReplaySource` persists replay state
before anchoring), so `!HasAnchor` does not prove there is nothing to clean.
Clearing requires either a replicated "source never started" proof or a
Channel-level proof that replica copies were reclaimed. Until then markers
stay, bounded to one per Channel owner and paged by replay discovery. The
`wukongim_mqtt_consumer_events_total{event="retired"}` counter shows how often
retirement creates or refreshes markers.
