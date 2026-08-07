# internal/runtime/delivery Flow

## Responsibility

`internal/runtime/delivery` owns canonical Online Delivery plan execution and
recipient-owner RECVACK state. `Runtime` is the only delivery execution module
in this package. The retired committed-event `Manager`, partition planner,
fanout worker/router, retry scheduler, and subscriber planner have been
removed; channelappend is the sole producer of recipient delivery plans.

The runtime depends only on narrow plan, presence, remote owner-push, local
session-write, offline-recipient, ACK, and observation ports. It does not
import app, gateway, Prometheus, concrete cluster runtimes, or protocol packet
builders.

The exported `Envelope`, `Route`, `PushCommand`, and `PushResult` DTOs remain
only as the stable version-one `WKVD1`/`WKVd1` owner-push wire representation.
Canonical runtime logic uses `internal/contracts/onlinedelivery` DTOs and the
node RPC adapter performs the compatibility conversion.

## Canonical Plan Flow

1. Channelappend submits a bounded durable or transient
   `RecipientDeliveryPlan` through `Runtime.EnqueueRecipientDeliveryPlan`.
2. `Runtime` validates the mode, exact-target groups, and total recipient
   bound, takes ownership of the immutable plan storage without cloning it,
   and admits it only while the bounded runtime is started.
3. A preallocated, globally bounded queue hashes canonical Channel type and
   ID onto a stable worker shard. One shard drains FIFO, so the complete
   presence/offline/owner-push execution for an accepted plan finishes before
   the next plan for the same Channel starts; different Channel shards retain
   the configured worker parallelism. A worker resolves all exact
   authority-target groups through one aligned presence call. A failed group
   does not discard successful siblings.
4. For durable plans, the runtime emits one de-duplicated batch for recipients
   that have no online route. Transient plans do not produce offline effects.
5. Online routes are coalesced by owner node and split by the configured owner
   push batch bound. Different owner batches execute under bounded concurrency.
6. Local batches enter `PushOwner`; remote batches use `RemoteOwnerPusher`.
   Retryable results are narrowed to their exact routes before the bounded
   retry loop. Terminal and exhausted results remain local to their plan.
7. Every plan admission, terminal result, execution-pressure change, and owner
   push attempt emits a bounded observation. Identity values remain diagnostic
   samples and are never metric labels.
8. `Runtime.Stop` closes admission, waits for in-flight enqueue senders, and
   drains every accepted plan from every worker shard within the caller's
   context. A successful stop leaves the runtime restartable for maintenance
   restore.

`RuntimeOptions.QueueSize` remains the node-wide accepted-plan capacity.
`RuntimeOptions.Workers` is both the maximum plan-processing concurrency and
the stable Channel-order shard count; it must never be implemented as multiple
independent consumers racing on one shared FIFO because that can deliver a
later `message_seq` before an earlier one for the same Channel.

## Owner-local Push and ACK Flow

1. `PushOwner` rejects commands for another node and validates exact active
   UID/session/owner fences before reserving ACK state.
2. `AckTracker.BindBatch` returns item-aligned opaque tokens while enforcing
   the per-session pending limit. A zero token is a rejection; accepted
   refreshes retain independent tentative metadata until finish or rollback.
3. `LocalSessionWriter` performs final exact-session validation, constructs
   the recipient packet, and writes it to the gateway session.
4. Successful writes finish only their matching reservations. Retryable,
   terminal, stale, build, and panic-isolated failures roll back only the
   current attempt's token, preserving any earlier committed reservation.
5. Duplicate recipient rows intentionally keep duplicate writes. A later
   duplicate refreshes its reservation before its own write so an earlier fast
   RECVACK cannot consume the next attempt's state.
6. Gateway `Recvack` removes the matching owner-local identity. `SessionClosed`
   removes all matching session identities.
7. Activity-throttled expiry removes stale committed and tentative identities;
   ordinary pushes and feedback do not scan the full tracker.
8. Serialized `AckEvent` callbacks project the exact pending identity count.
   Optional `AckBatchEvent` callbacks report one aggregate bind or finish stage
   with item, shard, rejection, rollback, and duration values; they never add
   callbacks inside tracker item or shard loops.

`AckTracker` maintains an O(1) derived pending count, shards mutation locks, and
keeps committed snapshots separate from in-flight refresh attempts. The common
unique bind uses the inline primary token path; extra-attempt storage is
allocated only when overlapping or committed-key refreshes require it.
