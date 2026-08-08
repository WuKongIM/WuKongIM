# internal/app Product Runtime Details

This companion to `FLOW.md` records the detailed product-runtime composition
and messaging flows. `FLOW.md` remains the package's canonical index; this file
owns the linked product-runtime detail.

App wiring starts `internal/runtime/delivery.Runtime`, injects its canonical
plan-admission port into channelappend, and registers only owner-push RPC. The
retired manager/retry/fanout execution stack and fanout RPC have been removed.
Gateway feedback temporarily crosses the existing delivery-usecase facade,
which performs only command-type conversion.

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
`wukongim` executable config enables `WK_DELIVERY_ENABLE` by default. With delivery disabled, committed messages still append and update their
channel-local sender-sequence index, but no online delivery is submitted. With delivery enabled, gateway RECVACK and session close
feedback flows through the temporary delivery usecase facade, while
channelappend post-commit effects enqueue bounded multi-target recipient
delivery plans into the canonical Online Delivery runtime. Each plan retains
every exact Slot authority fence. The app
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
`ChannelAppend.RecipientAuthorityDispatchConcurrency` remains accepted for
configuration compatibility but does not affect the canonical plan path, which
admits exact-target groups together.
`Delivery.RecipientWorkerConcurrency` independently defaults to 100 and is both
the Online Delivery plan worker count and stable Channel-order shard count.
The plan queue remains globally bounded; complete plans for one Channel drain
FIFO on one shard while different Channel shards can run concurrently. The
lookup-shard count controls writer map sharding; effect workers run only
blocking effects and never write channel state concurrently with another
advance for the same channel. The delivery observer maps aggregate writer
pressure and effect pool observations into Prometheus. Post-commit pressure
uses four fixed per-node gauges:
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
The foreground SEND path waits only for channel-authority durable append.
Ordinary message storage writes the message row and sender-sequence index in the
same storage batch. Subscriber scan, recipient authority grouping, and delivery
enqueue run after SENDACK in the authority writer's bounded post-commit
pipeline. No conversation-active or recipient-membership work exists in that
pipeline. The Online Delivery runtime later drains accepted plans, resolves
exact-target groups through the batched presence seam, coalesces routes by
owner, and pushes bounded commands. Failures do not change Channel durability
or the already successful SENDACK.

Runtime owner-push failures are counted with normalized delivery error classes.
Retryable results are narrowed to their exact routes and retried within a small
fixed attempt cap; terminal or exhausted results remain plan-local. The
composition root supplies `infra/delivery.LocalSessionWriter` to the runtime
and registers only the owner-push node RPC before workers start. Exact
owner-local session revalidation and recipient-specific `RecvPacket`
construction remain inside that adapter. The runtime owns item-aligned pending
RECVACK bind/finish/rollback, duplicate reservation refresh, write-error
classification, and activity-throttled expiry; see
`internal/infra/delivery/FLOW.md` and `internal/runtime/delivery/FLOW.md` for
those state machines. The same append observer records
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
When delivery is enabled, the app wires the bounded Online Delivery runtime
that drains plans outside the authority writer. Benchmark channel/subscriber
routes write real metadata through Slot proposals with bounded concurrency and
preserve mutation order within each channel. Subscriber mutations synchronously
maintain UID-owned ordinary membership rows, but message SEND never does.

Channelappend creates exact-target recipient plans from the cluster UID
authority table. The Online Delivery runtime resolves each complete plan,
coalesces routes by owner node, executes local owner pushes directly, and
forwards remote owner pushes through access/node Delivery Push RPC. App-level
adapters translate bounded plan admission, terminal, pressure, owner-push, and
ACK observations into metrics and logs. The delivery runtime itself stays
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

If the runtime exposes CMD memberships and committed CMD Channel reads, New
wires internal/usecase/cmdsync through CMDSyncStore. Explicit bind/unbind
mutates user_cmd_channel_membership, /message/sync reads only those bound CMD
logs, and /message/syncack monotonically advances ack_seq. CMD SEND never
creates bindings, and CMD state never shares ordinary membership fields or
ordinary Channel sequence space.

Bench runtime controls flow from internal HTTP through `internal/infra/cluster`, `pkg/cluster.Node`, `pkg/cluster/channels.Service`, and finally the hosted Channel runtime runtime. These routes are benchmark-only observation/cleanup controls and do not replace the gateway SEND activation path.

Legacy channel management requests flow through internal/usecase/channel and
the infra/cluster ChannelMetadataStore to Slot metadata. Add captures one
committed Channel tail, writes channel-owned subscribers, then writes UID-owned
memberships initialized from that shared boundary. Remove deletes subscribers
first and tombstones memberships second. Failures are returned for idempotent
caller retry. A reset preserves retained members' personal state through the
source-version reducer. Person channels establish both participant memberships
before the first persistent append and then set directory_ready.

Conversation list reads flow through internal/usecase/conversation. The
ConversationStore pages one UID's user_channel_membership activation index,
then submits one aligned hydration batch. The Channel cluster service resolves
exact routes and groups remote items by Leader; local reads return the committed
tail, retention floor, display message, and current user's latest committed
ordinary sender sequence. The usecase constructs transient rows and returns
deletes, unresolved keys, an opaque continuation cursor, and done/coverage
metadata. Badge, hide, and activation commands mutate only ordinary membership.
Metrics expose bounded scanned/returned/delete/unresolved and
hydration-local/remote costs without identity labels.

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

SEND with channel authority routing enabled:

```text
gateway/API send
  -> message.App delegates to channelappend.Router
  -> local authority writer or forwarded Channel Append RPC
  -> ordinary Channel message row plus sender-sequence index commit atomically
     (CMD/SyncOnce uses the separate command Channel log)
  -> SENDACK
  -> bounded post-commit online-delivery planning and other independent effects
  -> zero conversation rows and zero recipient membership writes
```

Conversation directory synchronization is a read-side flow. It is not a
post-commit effect and has no cache flush or authority handoff lifecycle.

The bench presence snapshot controller aggregates `online.Registry.Snapshot`
and `runtime/presence.Directory.Snapshot`. It is read-only and exists so
wkbench can validate owner-route and authority-route counts after connection
runs.

The effective cluster node ID is also the message ID seed. `Config.Cluster.NodeID`
wins when set; top-level `Config.NodeID` is only the fallback.
