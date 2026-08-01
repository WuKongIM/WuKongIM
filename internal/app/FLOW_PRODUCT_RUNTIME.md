# internal/app Product Runtime Details

This companion to `FLOW.md` records the detailed product-runtime composition
and messaging flows. `FLOW.md` remains the package's canonical index and may
temporarily duplicate this detail during the review-bounded extraction; once
linked, this file owns the detailed product-runtime flow.

Operations MCP has no independent process, listener, port, or config enable
switch. `internal/app` creates and closes its local call-control audit writer,
passes low-cardinality metrics when metrics are enabled, and registers the same
forward/profile/profile-lease/audit RPC handler on every cluster node. The
owner composes existing management readers, server-owned Prometheus queries,
backup reads, and bounded profile analysis into the closed-world `opsobserve`
service. Target profilers route one-time lease verification back to the
configured owner before starting active observation.

The DB Inspect reader is app-owned because only the composition root derives
the node-local storage locations for `pkg/db/inspect`. It is exposed to manager
usecases as a read-only diagnostics port and never accepts filesystem paths
from HTTP, web, or node RPC callers. The manager page can inspect the local
manager node by omitting `node_id`; selecting another node uses the manager DB
inspect RPC path to that node and does not combine rows from multiple nodes.

The ordinary application log reader is also app-owned because only the
composition root owns `Log.Dir` and the concrete node-local logger layout. It is
separate from the distributed Controller/Slot Raft log reader: ordinary app log
requests list fixed local log sources and parse application log entries, while
Raft log requests read cluster log storage metadata and decoded Raft payloads.
Remote ordinary app log requests use the manager app-log RPC path for the
selected node and still return only reader-owned source names and file labels,
never absolute paths.

The node-config snapshot provider is app-owned because only startup config
loading has the fully merged TOML/env effective values. `internal/config`
builds the bounded allowlist once during startup, redacts manager credentials,
cluster join tokens, static manager users, local filesystem paths, and
similarly sensitive values, then attaches that snapshot to `app.Config`.
`internal/app` only serves the supplied snapshot for the local node and returns
`ErrNodeConfigUnavailable` when the startup loader did not provide one. It is
read-only and does not watch or mutate live runtime config.

The diagnostics store is app-owned because only the composition root knows
whether `Observability.Diagnostics.Enabled` installed the bounded event store,
tracking sampler, and process-wide sendtrace sink. Manager diagnostics routes
use that same store for local reads and tracking-rule mutations; non-local
node-scoped reads and mutations route through the manager diagnostics RPC path
without falling back to legacy `internal` diagnostics state.
PreferredLeader details remain node-local diagnostics rather than Prometheus
labels: one bounded signature per observed physical Slot preserves state changes
immediately and resamples an unchanged non-match decision at most once every 30
seconds. A non-match to `match` recovery is retained once, while initial or
repeated steady `match` decisions remain available only through aggregate
metrics and later cluster snapshots so they cannot churn the diagnostics ring.
Diagnostic event counts are transition evidence, not reconcile-rate evidence;
aggregate Prometheus counters remain the frequency source.

Controller Raft status and manual compaction use a cluster-routed management
operator created in the app composition root. Local reads and compaction call
the local cluster node facade directly; non-local node-scoped operations use
the manager Controller Raft node RPC path. The cluster-wide manager compact
action fans out above the RPC layer by targeting every Controller voter in the
current control snapshot.

`Delivery.Enabled` remains false for app-level zero-value configs, while the
`wukongim` executable config enables `WK_DELIVERY_ENABLE` by default. With
delivery disabled, committed message effects still run inside the channel
authority writer so recent conversation state is updated, but no online
delivery is submitted. With delivery enabled, gateway RECVACK and session close
feedback flows to the delivery usecase, while channelappend post-commit effects
enqueue bounded multi-target recipient delivery plans into the recipient
delivery worker. Each plan retains every exact Slot authority fence. The app
presence adapter converts all plan groups in one call; the cluster adapter then
batches local groups in the presence directory, acquiring one read lock per
touched directory shard while preserving group-aligned partial errors, and
sends at most one batch RPC to each remote Slot leader. Results remain aligned
per exact target so a failed
leader group does not discard successful groups from the same plan.
`Config.ChannelAppend.AuthorityShardCount` defaults to a CPU-aware lookup-shard
count with a minimum of four. `ChannelAppend.AdvancePoolSize` is the direct ants
pool capacity used to activate channelappend writer state machines.
`ChannelAppend.EffectPoolSize` is the direct ants pool capacity used separately
by foreground channelappend append effects and post-append recipient effects.
The post-append pool uses non-blocking saturated admission and retains an
already-durable envelope behind a de-duplicated fair retry FIFO instead of
blocking a channel writer advance worker. A group-wide handoff reservation is
acquired before durable append; when that bound is full only the not-yet-appended
item returns `ErrChannelBusy`. The foreground append pool keeps its blocking
worker admission semantics.
Prepare runs inline on the writer advance path; append remains the foreground
durable path that determines SEND/SENDACK throughput.
`ChannelAppend.RecipientAuthorityDispatchConcurrency` defaults to a bounded
recipient-authority target fanout for legacy batch-only enqueuers. The
production plan-capable worker admits exact-target groups together instead of
using this target fanout.
`Delivery.RecipientWorkerConcurrency` independently defaults to 100 and controls
only the goroutines draining the bounded recipient delivery queue. The legacy
target fanout and production plan execution capacities therefore remain
independent. The lookup-shard count controls writer map sharding; effect workers run only blocking effects and never write channel
state concurrently with another advance for the same channel. The delivery
observer maps aggregate writer pressure and effect pool observations into
Prometheus. Post-commit pressure uses four fixed per-node gauges:
`wukongim_channelappend_post_commit_handoff_depth`,
`wukongim_channelappend_post_commit_handoff_capacity`,
`wukongim_channelappend_post_commit_retry_queue_depth`, and
`wukongim_channelappend_post_commit_retry_contended`. They have only the
registry's constant node labels; no channel, UID, Slot, or authority-target
label is added. Retry queue depth excludes a writer that already owns the retry
turn, so a zero queue with contended equal to one is valid. The observer also
records direct ants/v2 occupancy for the channelappend
advance/append_effect/post_commit pools in the generic ants pool metrics. The three-node bench
script summarizes these in `channelappend_metrics_summary.tsv` and
`ants_pool_usage_summary.tsv`. Per-channel append ordering remains capped
by the single-writer invariant even when different channels run through
different shards or workers.
The foreground SEND path waits only for channel-authority durable append;
subscriber scan, recipient authority grouping, delivery enqueue, and the
independent conversation active projection all run after SENDACK from the
authority writer's best-effort post-commit pipeline. The recipient delivery worker later
drains accepted plans, resolves all exact-target groups through the batched
presence seam, coalesces successful routes by owner across each whole plan,
splits each owner group by `Delivery.PushBatchSize`, and pushes those bounded
commands in first-seen order. The owner-push adapter records every actual local
or remote attempt in the delivery push count, route-count, and duration metrics.
Post-commit persistence
and restart replay are not part of
channelappend. Post-commit enqueue failures are logged with the failing phase and
route/dispatch context, counted through effect metrics, and dropped after the
routed helper's bounded retry window; they do not change channel durability or
the already-successful SENDACK decision. Conversation active-batch admission
performs only a short bounded fresh-route retry in the routed client. Delivery
is enqueued first; active projection failures surface independently as the
`conversation_active` post-commit phase and do not stop online delivery or later
large-channel pages.
Runtime fanout failures are counted with normalized delivery error classes.
Retryable fanout failures enter a bounded in-memory retry scheduler with a small
fixed attempt cap; retry queue overflow is surfaced as `queue_full`. The
composition root supplies `infra/delivery.LocalOwnerPusher` to that runtime and
installs the delivery Manager before workers start. Exact owner-local session
revalidation, recipient-specific `RecvPacket` construction, item-aligned pending
RECVACK bind/finish/rollback, duplicate reservation refresh, write-error
classification, and activity-throttled expiry remain inside the adapter; see
`internal/infra/delivery/FLOW.md` for that state machine. The same append observer records
per-message append success/error latency and classifies append failures with
low-cardinality labels for benchmark triage, including typed Channel runtime/cluster
errors and short append results.

The channel append commit pipeline scopes unscoped person-channel events to the
two channel participants. For non-person unscoped channels it pages durable
subscribers through the app delivery metadata source, an explicitly supplied
subscriber source, or the cluster Slot metadata source. The composition root
constructs `infra/cluster.RecipientAuthorityResolver` and supplies its optional
aggregate metrics observer; cluster capability probing, route-error mapping,
aligned route DTO conversion, and physical hash-slot target counting remain
inside that adapter. After each recipient set is formed,
channelappend groups recipients by exact physical hash-slot and logical Slot
Raft Group authority target including leader term and config epoch, then packs
the groups into a bounded delivery plan. The worker preserves those fences
while the presence usecase groups target lookups by actual leader and returns
partial per-target results.
It next admits an independent kind-aware `conversationactive.ActiveBatch`
through the shared `ConversationAuthorityClient`; its first attempt consumes
the already-grouped aligned snapshot, while exceptional sender or recipient
route items use the legacy active-admission compatibility path. Channelappend
chooses normal versus CMD kind from the committed envelope, and active admission
still runs when online delivery is disabled. When delivery is enabled, the app wires a bounded
recipient delivery worker that drains those plans and runs the delivery-only
channelappend recipient processor outside the authority writer. `/bench/v1/channels`,
`/bench/v1/channels/subscribers`, and `/bench/v1/channels/subscribers/remove`
write real channel metadata or add/remove subscriber rows through Slot proposals.
The benchmark data writer uses bounded concurrency for independent
channel/subscriber mutations while preserving subscriber mutation order within
the same channel. Scoped UID delivery bypasses subscriber scan and
flows through recipient authority grouping, presence resolution, and the local
or RPC owner pusher after the recipient delivery worker accepts the plan.
The app maps the worker's serialized execution-pressure observation into
Prometheus worker capacity and in-flight gauges. These metrics do not include
UID, channel, slot, or per-target labels.

When the cluster runtime exposes route snapshots, delivery planning uses the
cluster UID hash-slot table to create authority partitions. A fanout task
router runs local partitions through the in-process fanout worker and forwards
remote partitions through access/node Delivery Fanout RPC. The remote node then
uses its own subscriber source and still pushes resolved online routes by
owner node. Runtime fanout task, resolve, and push observations are translated
by app-level metrics/logging adapters; retry enqueue, attempt, drop, and
queue-depth observations use the same adapter. The delivery runtime itself stays
independent from Prometheus and concrete logging backends.

The Channel runtime metrics observer also logs rare admitted-append cancellation
snapshots emitted by the append runtime. These lines include the channel key,
op id, commit mode, LEO/HW/target offset, queue and in-flight counts, and
quorum progress flags plus a compact leader-visible follower summary so
benchmark timeout triage can identify the stuck append phase without adding
high-cardinality Prometheus labels.
Leader-side Pull stage metrics sample one in every sixteen operation IDs. When
multiple optional observers request different sample intervals, the composite
observer admits the greatest-common-divisor envelope and filters callbacks by
operation ID for each child, preserving every child's requested rate without
forcing the metrics child to inherit a more expensive observer's rate.

Message append observations record low-cardinality metrics for every durable
append attempt and log rare append failures, including gateway deadline
timeouts, with path, error class, duration, and raw error. These diagnostics do
not change append admission, durable write, or quorum ACK rules.
When metrics are enabled, app observability also adapts cluster message event
observations into Prometheus counters, histograms, and stream-cache gauges.
The adapter preserves the cluster-provided bounded labels only; it does not add
UID, channel, slot, or per-message labels.

If a test or harness supplies `WithCluster` and that runtime implements the
cluster append surface, `New` still wires a `ChannelAppender` to keep the real
send path available.
If that runtime also implements the committed channel message read surface,
`New` wires a `ChannelMessageReader` so `/channel/messagesync` can use the same
message usecase as the gateway send path.
If that runtime also implements the message event projection surface, `New`
wires `MessageEventStore` so `/message/event` appends and `/channel/messagesync`
event summaries share the same Slot/meta reducer as other cluster-owned message
metadata. `/message/eventsync` remains outside the app surface in this phase.

If the runtime also exposes unified conversation projection writes and committed
Channel runtime reads, `New` wires `internal/usecase/cmdsync` through
`CMDSyncStore`. `/message/sync` scans only `ConversationKindCMD` rows from the
UID-owned projection, reads the corresponding command/source SyncOnce channel
logs, and returns legacy message arrays through the API adapter.
`/message/syncack` advances CMD-kind read cursors in the same kind-aware
conversation table, so CMD sync does not introduce a second metadata branch or
pending-state updater. Ordinary conversation hydration stays on
`ConversationKindNormal` rows and skips `SyncOnce`/command-channel log entries
instead of relying on suffix filtering in conversation storage or list logic.

Bench runtime controls flow from internal HTTP through `internal/infra/cluster`, `pkg/cluster.Node`, `pkg/cluster/channels.Service`, and finally the hosted Channel runtime runtime. These routes are benchmark-only observation/cleanup controls and do not replace the gateway SEND activation path.

Legacy channel management requests flow from internal HTTP through
`internal/usecase/channel` and the `internal/infra/cluster`
`ChannelMetadataStore` adapter to `pkg/cluster.Node` Slot metadata facades.
Mutations are proposed through Slot ownership; reads use the current routed Slot
metadata store. Ordinary subscriber mutations also project `(uid, channel)` rows
through the UID-owned membership facade for compatible metadata reads; the
conversation list itself pages UID-owned active conversation rows instead. When
the channelappend group is available, the app-level subscriber mutation observer
forwards the final large-group flag and subscriber mutation version to
`channelappend.Group.ApplySubscriberMutation` so non-large channel subscriber
snapshots cached in `channelState` stay aligned with API mutations.

Conversation list reads flow from entry adapters through
`internal/usecase/conversation`. When the cluster exposes the conversation
authority surface, the list Store is the routed
`internal/infra/cluster.ConversationAuthorityClient`, which resolves the UID
hash-slot authority and reads the target-owned active view from the local or
remote authority cache. The Messages port remains the `ConversationStore`
adapter so last-message hydration reads committed Channel runtime tails with
`Config.Conversation.MaxLastMessageConcurrency` as a bounded tail-read limit;
the same adapter remains the StateStore, StateMutationStore, and DeleteStore so
legacy conversation read/delete mutations still write through UID-owned Slot
metadata instead of the authority list client.
If a test or limited harness exposes conversation reads but not the authority
surface, the usecase uses `ConversationStore` for both Store and Messages as a
DB-only compatibility path. Conversation rows do not store the last message.
When metrics are enabled, the app maps API conversation-list observations to
Prometheus metrics for latency, returned items, sparse items, last-message
loads, last-message errors, active-index stale skips, and whether another active
page exists using only low-cardinality labels. It also maps conversation active
cache observations to Prometheus gauges for cached rows, dirty rows, fair
dirty-queue rows, bounded dirty-age buckets, oldest dirty age, fixed normal/CMD
row and dirty-row counts, accepted/rejected admission cache-lock latency, and
flush result/row/stage-duration metrics.

Conversation list with authority enabled:

```text
/conversation/list
  -> access/api parses the UID page request
  -> internal/usecase/conversation asks Store for the UID active view
  -> ConversationAuthorityClient resolves the UID hash-slot authority
  -> local authority:
       validate the exact RouteTarget
       delegate cache and UID-owned DB active-view merge to runtime/conversationactive.Manager
  -> remote authority:
       call access/node Conversation Authority List RPC for the target-owned view
  -> usecase hydrates only the returned page with channel-owned last-visible messages
  -> access/api shapes the legacy-compatible response
```

Conversation active-batch admission with authority enabled:

```text
channelappend active producer
  -> emits conversationactive.ActiveBatch with explicit normal or CMD kind
  -> ConversationAuthorityClient.AdmitActiveBatch
       -> cluster resolves SenderUID and recipient UIDs from one route snapshot
       -> groups rows by exact UID authority and packs same-leader groups together
  -> local authority bulk entry:
       validate all exact RouteTargets and reserve each unique valid exact
       target under one short authority lock
       lazily activate only stale groups that still match the current local route
       finish lazy activation with one unified revalidation-and-reservation pass
       keep stale/not-leader results aligned while valid siblings continue;
       fragments for the same exact target share one in-flight reservation
       release the authority lock before delegating all valid groups to one
       runtime/conversationactive.Manager.AdmitRoutedActiveBatches cache transaction
       release exact-target reservations after the cache mutation returns
  -> remote authority:
       access/node Conversation Authority bulk ActiveBatch RPC
       remote local authority applies the same per-target validation and one
       routed cache transaction, returning one aligned status per exact group
```

The app authority does not regroup or reinterpret active batches and does not
normalize zero conversation kinds. It trusts the cluster-routed client to send
`SenderUID` only to the sender-owned authority target; non-sender recipient
targets arrive with an empty sender field. The hard local fence remains
`(HashSlot, SlotID, LeaderNodeID, LeaderTerm, ConfigEpoch)`; `RouteRevision` and
`AuthorityEpoch` remain observation-order and diagnostic fields. One stale
group cannot reject valid siblings, while cache pressure or a conflicting
cache-address hash slot rejects the complete set of otherwise-valid sibling
groups before any cache mutation. Legacy single-batch and ActivePatch entries
use the same exact-target reservation fence, including rolling-upgrade fallback
traffic that does not use the bulk RPC.

Conversation delete with authority enabled:

```text
DeleteConversation
  -> ConversationAuthorityClient groups deletes by UID and resolves a fresh exact target
  -> local or access/node RPC authority HideConversationsForTarget
  -> durable HideConversationsBatch advances deleted_to_seq and clears active_at
  -> runtime/conversationactive.Manager reconciles cached MessageSeq against the barrier
  -> authority revalidates the exact target before returning success
```

Rows at or below the delete barrier are removed from cache. A concurrently
observed newer message stays visible and becomes dirty against the cleared
durable baseline. A successful store call applies those confirmed barriers to
cache. A multi-proposal store error can have an unknown committed prefix, so it
only invalidates every requested durable baseline and forces present rows dirty;
it never removes an unconfirmed tail. Later durable hydration fences the
committed prefix, while the routed client retries the monotonic barrier when the
error is retryable.

Legacy user management requests flow from internal HTTP through
`internal/usecase/user` and the `internal/infra/cluster`
`UserMetadataStore` adapter to `pkg/cluster.Node` Slot metadata facades.
Token and device mutations are proposed through UID Slot ownership. Online
status reads use the v2 presence usecase when available, while device close
side effects are limited to owner-local sessions from `online.Registry`.
System UID persistence reuses the compatible channel metadata store's internal
subscriber-list model.

Legacy message send and channel message sync requests flow from internal HTTP
through the app message facade. Sends delegate to `channelappend.Router`, which
resolves the canonical channel's append authority. Local authority sends are
admitted to the local `channelappend.Group`; remote authority sends are forwarded
through access/node Channel Append RPC to the target node, where they enter only
that node's authority writer group. Channel message sync uses the
`internal/infra/cluster` ChannelMessageReader, which reads committed Channel runtime
messages through the cluster Node facade and keeps legacy person-channel
response IDs in the HTTP adapter.

Conversation active rows remain working-set hints: delayed or dropped
post-commit work does not change message durability or SENDACK success. The
runtime/conversationactive.Manager coalesces active rows, serves list reads by
merging cached rows with UID-owned DB active rows, and flushes durable active
touch patches through the conversation active flush worker or handoff drain.
Cache pressure only sends a nonblocking wakeup to that worker; admission never
performs durable I/O. The app conversation authority keeps route target fencing,
lifecycle handoff, observer mapping, and usecase/RPC type adaptation.
Authority activation and final drain keep the exact clean-slot purge atomic
with target publish/fencing under the authority mutex. The purge mutates only
cache state in that critical section; aggregate cache observers run after the
target state is published and the authority mutex is released, so observer
callbacks may safely re-enter authority reads.
Foreground cache admissions reserve the full hard target identity under that
same mutex before calling the runtime Manager and release it after the cache
mutation returns. The authority mutex is not held across Manager calls, so
synchronous runtime observers can re-enter authority reads. Route-lifecycle
handoff is initiated outside those admission observer callbacks.
Aggregate admission cache snapshots are coalesced to a 100ms interval in the
production authority wiring; pressure transitions and flush completion still
publish immediate snapshots, while mutation counters remain unsampled.

SEND with channel authority routing enabled:

```text
gateway/API send
  -> message.App delegates to channelappend.Router
  -> Router resolves channel append authority
  -> local channel authority:
       channelappend.Group admits the batch to the channel writer
  -> remote channel authority:
       access/node Channel Append RPC forwards the batch
       remote node admits it to its local channel writer
  -> authority writer prepares commands, allocates IDs, and calls cluster ChannelAppender
     only after reserving one slot from the group-wide post-commit handoff bound
  -> Channel runtime persists messages and returns append result
  -> SENDACK returns to sender
  -> authority writer post-commit effect:
       scope person recipients or page subscribers
       group recipients by UID authority target, including Slot leader term and config epoch, for delivery
       enqueue recipient delivery batch when delivery is enabled
       ConversationAuthorityClient.AdmitActiveBatch as an independent projection
       when the nonblocking effect pool is full, retain the committed envelope
       and enter the fair retry FIFO instead of dropping already-durable work
       after one terminal recipient/conversation attempt, release the handoff
       reservation and prune the in-memory committed envelope
```

The bench presence snapshot controller aggregates `online.Registry.Snapshot`
and `runtime/presence.Directory.Snapshot`. It is read-only and exists so
wkbench can validate owner-route and authority-route counts after connection
runs.

The effective cluster node ID is also the message ID seed. `Config.Cluster.NodeID`
wins when set; top-level `Config.NodeID` is only the fallback.
