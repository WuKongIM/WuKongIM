# internal/infra/cluster Flow

## Responsibility

`internal/infra/cluster` adapts internal usecase/runtime ports to
`pkg/cluster` and `pkg/channel`. It maps channelappend append DTOs to
`pkg/channel` DTOs, resolves channel append authority through cluster, adapts
legacy-compatible channel metadata usecase calls to cluster Slot metadata
facades, adapts legacy-compatible user metadata calls and manager user scans to
UID Slot metadata facades, adapts read-only manager business channel and
channel runtime metadata scans to cluster Slot metadata reads, adapts conversation reads and read/delete
mutations to UID-owned conversation rows plus channel-owned committed message
logs, adapts read-only manager message pages to committed Channel runtime reads,
adapts manager message retention requests to fenced Slot metadata advances, and
routes manager connection reads over cluster node RPC, routes manager
distributed log reads to node-local cluster log storage or peer RPC, routes
manager Slot Raft status and compaction operations to the selected node-local cluster
operation or peer RPC, adapts manager Slot leader transfer and staged Slot
replica-move intents to cluster control, adapts manager node lifecycle
join/activation/leaving/removal and Controller voter promotion writes to
cluster control, routes startup seed JoinNode and readiness probes over
cluster node RPC, routes manager Controller Raft operations to node-local
cluster operations or peer RPC, routes ordinary application log reads to the
selected node's app-owned local reader or peer RPC, routes read-only manager
node config snapshots to the selected node's app-owned effective-config reader
or peer RPC, routes manager DB Inspect reads to node-local inspect readers or
peer RPC, routes manager diagnostics reads and tracking-rule mutations to the
selected node's internal diagnostics store or peer RPC, routes manager plugin
inventory reads and lifecycle mutations to the selected node's plugin lifecycle
usecase over peer RPC, routes
manager Controller task audit reads to the current Controller leader's
node-local audit store over peer RPC when needed, and adapts presence/delivery
ports plus channelappend recipient-authority resolution to cluster routing and
node RPC.

## Management Snapshot Flow

`ManagementSnapshotReader` adapts `pkg/cluster.Node` control snapshots to the
internal management usecase. It only exposes local control snapshot reads and
the local node ID for read-only manager node and Slot list rendering. Node
lifecycle writes are exposed through the separate
`ManagementNodeLifecycleAdapter` for join, activation, leaving, removal, and
Controller voter promotion transitions. Bounded Slot onboarding and Stage 4 scale-in Slot drain writes are
exposed through `ManagementSlotReplicaMoveAdapter`, which submits Stage 3
`slot_replica_move` task intents and never mutates assignments directly. Final
scale-in removal is exposed as a lifecycle writer primitive but remains gated by
the management usecase's `safe_to_remove` status before this adapter is called.
The adapter forwards the final `MarkNodeRemoved` request, including the
management usecase's safety-check state revision when provided, to cluster
control only after that gate and treats the resulting `removed` state as a
durable control-plane tombstone, not a node-local cleanup command.
Gateway session closure and Slot operations other than explicit node-local Raft
compaction or staged replica-move task creation stay unmigrated. Channel
migration manager APIs use the dedicated cluster migration store adapter,
which only exposes the Slot-owned Channel runtime migration facade. Gateway drain mode
itself is routed through the manager connection RPC remote writer below because
it is a target-node gateway admission toggle, not a control-plane assignment
write.
Controller voter promotion is only delegated here after the management usecase
has validated the target, target readiness, live preparation proof, expected
revision, previous durable voters, and observed Controller Raft voter set. This
adapter does not perform promotion safety decisions and does not implement the
target-node preparation RPC codec.

## Node Lifecycle RPC Flow

```text
seed join loop
  -> NodeLifecycleClient.JoinNode(seed_node_id, NodeJoinRequest)
  -> access/node NodeLifecycle RPC client
  -> cluster CallRPC(seed_node_id, RPCNodeLifecycle)
  -> seed node access/node lifecycle handler
  -> management JoinNode usecase

management activation gate
  -> NodeLifecycleClient.NodeReadiness(node_id)
  -> access/node NodeLifecycle RPC client
  -> cluster CallRPC(node_id, RPCNodeLifecycle)
  -> target node app-local readiness provider
  -> management NodeReadiness DTO

controller voter promotion gate
  -> NodeLifecycleClient.ControllerVoterReadiness(node_id)
  -> NodeLifecycleClient.PrepareControllerVoter(request)
  -> access/node NodeLifecycle RPC client
  -> cluster CallRPC(node_id, RPCNodeLifecycle)
  -> target node app-local readiness / preparation providers
  -> management readiness and preparation proof DTOs
```

`NodeLifecycleClient` is the narrow cluster-facing wrapper for startup seed
join, readiness, and Controller voter preparation RPCs. It maps readiness wire
DTOs into the management usecase's entry-independent readiness shapes and maps
Controller voter preparation endpoints/proof fields without making promotion
safety decisions. It does not validate membership, choose activation timing,
rebalance Slots, call ActivateNode, or submit final Controller voter promotion
writes.
The seed-side handler still routes durable join writes through the management
usecase and cluster control writer. Activation readiness is checked through
the typed node lifecycle RPC; callers should not substitute raw TCP probes for
the transport-ready gate because the RPC response also carries the target
mirror cluster ID, mirror revision, and app runtime readiness. Controller voter
preparation responses preserve the target node's observed Controller Raft
applied index and voter set as the live proof consumed by the management
usecase before the final cluster control write.

## Management Channel List Flow

`ChannelBusinessReader` adapts the management usecase's business channel list
port to the cluster channel metadata scan facade. It only exposes physical
Slot page reads; list filtering, cursor validation, and manager response
shaping stay in the usecase and access layers. `ManagementChannelReader` is
the remote half for non-local `node_id` filters: it forwards page requests
through access/node manager channel RPC and preserves the management DTO page.

`ChannelRuntimeMetaReader` adapts the management usecase's channel cluster list
port to the cluster runtime metadata scan facade. It only exposes read-only
physical Slot pages from `channel_runtime_meta`; `node_id`, `node_scope`,
channel ID filtering, max-message-sequence hydration, cursor validation, and
HTTP response shaping stay above this layer.

## Management User Flow

`UserMetadataStore` also exposes the management usecase's user list scan port
when the cluster node supports `ScanUsersSlotPage`. The adapter keeps this as
a read-only Slot metadata facade; user list filtering, device/presence joins,
token reset, force-offline, and system UID normalization stay above this layer.

## Append Flow

```text
channelappend.AppendBatchRequest
  -> channel.AppendBatchRequest
     (expected channel/leader epochs fence stale authority writes)
     (trace id, diagnostics channel key, append attempt, and per-message trace metadata stay transient)
  -> ChannelAppendNode.AppendChannelBatch
  -> record sendtrace `channel.append.local` for traced messages after completion
  -> channel.AppendBatchResult
  -> channelappend.AppendBatchResult
```

This adapter is the durable append ownership boundary: outbound message payloads
are cloned before entering `channel`, and inbound result payloads are cloned
unless the channelappend runtime marks them unnecessary for SENDACK-only flows.
Commit mode, expected authority epoch fences, and typed errors are mapped at
this boundary so the channelappend runtime stays cluster-agnostic.
`channel.ErrWriteFenced` is treated as a retryable route-not-ready condition:
the durable control-plane fence owns the decision, and upper send orchestration
should retry after metadata refresh or migration completion rather than treating
the write as a permanent append failure.
Channel runtime placement candidate shortage is also mapped to route-not-ready, so a
node loss that makes new channel replica placement impossible fails closed and
can be retried after the cluster regains enough schedulable data nodes.
The adapter records channel append sendtrace events only when tracing is enabled
and the request carries trace metadata, so untraced appends do not pay extra
timing or event-allocation cost.
When `ChannelAppendNode.AppendChannelBatch` returns a batch-level error, the
adapter logs `internal.infra.cluster.channel_append_batch_failed` at ERROR
level with the channel identity, authority fence, attempt, record count,
mapped error result, and raw source error before returning the mapped
channelappend error.
`ChannelIdempotencyStore` adapts the node-local Channel runtime idempotency lookup
facade for the channelappend runtime. It only returns a prior successful send
when the sender UID, client message number, canonical channel, and raw payload
hash match the durable index entry; hash mismatches are treated as lookup
misses so callers cannot turn conflicting key reuse into a false success.

## Message Sync Read Flow

```text
message.ChannelMessageQuery
  -> channelstore.ReadCommittedRequest
     (pull-up reads forward; pull-down/latest reads reverse with limit+1)
  -> ChannelMessageReadNode.ReadChannelCommitted
  -> channel/store committed messages
  -> message.ChannelMessagePage
```

The reader adapter trims `limit+1` results to preserve the legacy `more`
contract and returns messages to the usecase in ascending sequence order. It
preserves the committed message setting bitset so the message usecase can
enrich only stream messages with event projections; legacy HTTP-only field
shaping remains in `internal/access/api`.

## Message Event Projection Flow

```text
message.MessageEventAppend
  -> metadb.MessageEventAppend
  -> cluster Node.AppendMessageEvent
  -> Slot leader stream cache for stream.open/delta/snapshot
  -> terminal stream event merges cached snapshot and proposes a durable Slot FSM update
     (stream.finish flushes open cached lanes and finish marker in one batch proposal)
     (stream.finish without cached lanes or a complete snapshot fails closed on the cluster boundary)
  -> Slot FSM message event reducer
  -> message.MessageEventAppendResult

message event summary keys
  -> cluster Node.GetMessageEventStatesBatch
  -> route keys to Slot leaders
  -> compact durable lane states overlaid with in-flight Slot-leader cache states
```

`MessageEventStore` adapts the message usecase event projection port to the
cluster Slot metadata facade. It clones inbound payloads, maps cache or compact
reducer results back to usecase DTOs, and keeps route/not-leader/backpressure
errors in the same typed family used by channel append. Stream buffering policy
is implemented inside `pkg/cluster.Node` so all HTTP/API callers route
in-flight deltas to the Slot leader cache before terminal events are proposed.
The adapter does not validate HTTP request semantics, emit realtime event
packets, or implement `/message/eventsync`; those remain in access/usecase
layers or later phases.

## CMD Sync Flow

```text
cmdsync.Sync
  -> ListConversationActivePage(ConversationKindCMD, uid)
  -> ReadChannelCommitted(command channel/source SyncOnce channel, forward from read cursor)
       -> page forward until enough SyncOnce/command-channel messages are found
  -> cmdsync.SyncedMessage

cmdsync.SyncAck
  -> UpsertConversationStatesBatch(kind forced to ConversationKindCMD)
```

`CMDSyncStore` is the single internal adapter for durable command-message
sync. It reads CMD rows from the unified UID-owned conversation projection and
advances read progress by writing CMD-kind `ConversationState` rows back through
cluster Slot metadata. It does not create a second CMD-specific metadata
table or a pending overlay. Channel log reads use Channel runtime committed forward
reads, filter out ordinary source-channel messages, and return cloned payloads
to keep access/usecase layers from aliasing storage-owned memory.

## Management Message Flow

```text
management.MessageReader.QueryMessages
  -> ChannelMessageReadNode.ReadChannelCommitted(reverse, before_seq, limit+1)
  -> channel/store committed messages
  -> manager message DTO rows
```

The manager message adapter reads committed Channel runtime rows in descending order
for the manager message page. It converts timestamps from milliseconds to Unix
seconds, clones payloads, applies the manager's optional message id and client
message number filters within the returned page, and leaves cursor encoding and
HTTP response shaping to `internal/access/manager`.

For an unscoped latest query, the adapter reads the bounded node-local global
message-ID index from every active or leaving data node with at most eight RPCs
in flight, merges results by descending Snowflake message ID, verifies duplicate
replicas agree, and returns only unique messages. Query cost is bounded by node
count and page size rather than total channel count.

```text
management.MessageRetentionOperator.AdvanceMessageRetention
  -> MessageRetentionNode.GetChannelRuntimeMeta
  -> if local node is not the Channel runtime leader, forward once to meta.Leader
  -> leader re-reads ChannelRuntimeMeta and verifies local leadership
  -> ChannelRetentionView reads local Channel runtime HW/MinISR safety
  -> ReadChannelCommitted(reverse latest, min_seq = RetentionThroughSeq + 1)
  -> AdvanceChannelRetentionThroughSeq(fenced Slot metadata command)
  -> manager retention response
```

The manager retention adapter treats history deletion as logical channel log
compaction. It never deletes message rows. It computes a safe boundary no higher
than the requested sequence, latest committed visible message, Channel runtime HW,
and leader MinISR match offset. Durable checkpoint HW is deliberately left to the
physical trim path so the manager request does not wait on local cleanup
checkpointing. If any logical gate is below the request, the adapter returns a
blocked response with the limiting reason instead of partially advancing. Phase 1
intentionally does not use a replay cursor gate because internal does not yet
expose a durable committed replay-cursor contract. Successful non-dry-run
requests advance `RetentionThroughSeq` through the Slot metadata FSM using the
observed channel epoch, leader epoch, leader ID,
and lease fence. Dry-runs return the calculated boundary without proposing.
Requests first received on a non-leader forward to the metadata leader when
node RPC is available; the remote leader recomputes all fences locally, so
origin nodes never transmit stale metadata fences. Missing leaders, stale routes,
and local-only non-leader states return typed retryable errors to the manager
usecase.

## Management Channel RPC Flow

```text
management.RemoteBusinessChannelReader
  -> access/node Manager Channel RPC client
  -> cluster CallRPC(target node, RPCManagerChannels)
  -> target node access/node Manager Channel RPC handler
  -> target node ChannelBusinessReader.ScanChannelsSlotPage
```

The channel RPC adapter only chooses local versus remote execution for a
selected node. HTTP query parsing, cursor validation, channel filtering, and
response DTO shaping stay in the manager access and management usecase layers.

## Management Connection Flow

```text
management.RemoteConnectionReader
  -> access/node Manager Connection RPC client
  -> cluster CallRPC(target node, RPCManagerConnection)
  -> target node access/node Manager Connection RPC handler
  -> target node owner-local online registry
```

`ManagementConnectionReader` is the narrow remote half of the manager
connection, node-runtime summary, and gateway drain-mode surfaces. It forwards
non-local `node_id` list/detail reads, aggregate runtime-summary reads, and
gateway admission drain toggles to the owner node and preserves management
usecase DTOs across the RPC boundary. Local connection filtering, runtime
counter interpretation, and DTO shaping stay in the management usecase; this
adapter only chooses the typed node RPC client.

## Management Node Config Flow

```text
management.NodeConfigReader
  -> local node_id: app-owned redacted effective-config snapshot provider
  -> remote node_id: access/node Manager Node Config RPC client
  -> cluster CallRPC(target node, RPCManagerNodeConfig)
  -> target node access/node Manager Node Config RPC handler
  -> target node app-owned redacted effective-config snapshot provider
```

`ManagementNodeConfigReader` only chooses local versus remote execution for the
selected node. Node existence validation, HTTP permission checks, and response
DTO shaping stay in `internal/usecase/management` and
`internal/access/manager`. The adapter does not inspect environment variables,
configuration files, or secrets; it transports only the snapshot already
allowlisted and redacted by the selected node's app provider.

## Management Plugin Flow

```text
management.RemotePluginReader
  -> access/node Manager Plugin RPC client
  -> cluster CallRPC(target node, RPCManagerPlugins)
  -> target node access/node Manager Plugin RPC handler
  -> target node v2 plugin lifecycle usecase
```

`ManagementPluginReader` is the narrow remote half of the manager plugin
inventory and lifecycle API. It forwards non-local `node_id` list/detail reads,
config updates, restarts, and uninstalls to the selected node and preserves
management plugin DTOs across the RPC boundary. Local plugin reads/mutations,
HTTP permissions, and response shaping stay in the management usecase and
manager access layer. This adapter does not inspect plugin directories or call
the plugin runtime directly.

`PluginHTTPForwarder` reuses the same `RPCManagerPlugins` service with the
`http_forward` operation for `/plugin/httpForward` requests that target a
positive `toNodeId`. The receiver executes its local `/plugin/route` hook only;
fanout `toNodeId=-1` is intentionally deferred in the plugin usecase and never
enters the infra adapter.

`ManagementPluginBindingStore` adapts manager plugin binding reads and
mutations to cluster metadata. UID reads and mutations stay UID-owned.
Plugin-centric pages use cluster `ListPluginBindingsByPluginNo`, which owns
hash-slot fan-out, Slot-leader routing, heap merge, and opaque cursor semantics;
the infra adapter only maps metadata rows to management DTOs and reports
`ErrPluginBindingsUnavailable` when the underlying node lacks that scanner.

## Management Log Flow

```text
management.LogReader
  -> local node_id: cluster.LocalControllerLogEntries/LocalSlotLogEntries
  -> remote node_id: access/node Manager Log RPC client
  -> target node access/node Manager Log RPC handler
  -> target node cluster local log reader
```

`ManagementLogReader` preserves newest-first pagination and decoded payload
summaries across the infra boundary. It only chooses local versus remote
execution for the requested `node_id`; log pagination, Raft payload decoding,
and storage watermarks live in `pkg/cluster`, while HTTP parsing and manager
response shaping stay above this package.

## Management Slot Raft Flow

```text
management.SlotRaftOperator
  -> local node_id: cluster.LocalSlotRaftStatus(slot_id) or LocalCompactSlotRaftLog(slot_id)
  -> remote node_id: access/node Manager Slot Raft RPC client
  -> cluster CallRPC(target node, RPCManagerSlotRaft)
  -> target node access/node Manager Slot Raft RPC handler
  -> target node cluster local Slot Raft status or compaction
```

`ManagementSlotRaftOperator` only chooses local versus remote execution for the
requested node and Slot. HTTP parsing, permission checks, and response summary
shaping stay above this layer; actual Raft role/watermark reads and
snapshot/log compaction stay in `pkg/cluster` and `pkg/slot/multiraft`.

## Management Slot Leader Transfer Flow

```text
management.SlotRuntimeStatusReader
  -> cluster.LocalSlotRaftStatus(slot_id)

management.SlotLeaderTransferWriter
  -> cluster.RequestSlotLeaderTransfer(control.SlotLeaderTransferRequest)
  -> cluster control runtime
```

The leader-transfer adapters do not implement Slot Raft mechanics. They expose
the local Slot Raft leader/voter status needed by the management usecase
preflight, then pass the validated control intent into cluster control.

## Management Slot Replica Move Flow

```text
management.SlotReplicaMoveWriter
  -> cluster.RequestSlotReplicaMove(control.SlotReplicaMoveRequest)
  -> cluster control runtime
  -> Controller slot_replica_move task
  -> cluster task executor
  -> Slot Raft OpenLearner/ChangeConfig
  -> final Controller assignment commit
```

The replica-move adapter is only the infra boundary for manager onboarding task
creation. It does not choose source peers, mutate `DesiredPeers`, or execute
Slot Raft config changes; those stay in the management planner, Controller
task intent, and cluster task executor respectively. The onboarding target is
task-local staged state while it is opened and caught up as a learner; it is not
added to `DesiredPeers` until the executor commits the final assignment after
Slot Raft voters match the target set. When the source replica is still the
observed Slot leader, the executor transfers leadership and waits for a
non-source leader observation before removing the source voter, so the next
executor wakeup is based on observed Raft state rather than a blind control
revision churn. The final assignment commit also carries the executor's live
observed Slot Raft voter proof, and failed replica-move tasks remain visible in
control state without automatic executor retry until a later explicit operator
advance or cancellation path handles them.

## Management Node Lifecycle Flow

```text
management.NodeLifecycleWriter
  -> cluster.JoinNode(control.JoinNodeRequest)
  -> cluster.ActivateNode(control.ActivateNodeRequest)
  -> cluster control runtime
```

`ManagementNodeLifecycleAdapter` does not inspect readiness or rebalance Slots;
activation readiness is checked above it through `NodeLifecycleClient`. It only
passes manager-validated node join and activation intents into the cluster
control facade and preserves typed control errors for the management usecase to
map to operator-facing responses.

## Management Application Log Flow

```text
management.ApplicationLogReader
  -> local/empty node_id: app-owned node-local ordinary application log reader
  -> remote node_id: access/node Manager App Log RPC client
  -> cluster CallRPC(target node, RPCManagerAppLogs)
  -> target node access/node Manager App Log RPC handler
  -> target node app-owned node-local ordinary application log reader
```

`ManagementApplicationLogReader` is distinct from the distributed Raft log
reader above. It only chooses local versus remote execution for ordinary
application log sources and entry pages; file discovery, parsing, filtering,
cursor handling, and path hiding remain owned by `internal/app` and
`internal/log`.

## Management DB Inspect Flow

```text
management.DBInspectReader
  -> local node_id: app-wired pkg/db/inspect reader for node-local storage
  -> remote node_id: access/node Manager DB Inspect RPC client
  -> cluster CallRPC(target node, RPCManagerDBInspect)
  -> target node access/node Manager DB Inspect RPC handler
  -> target node app-wired pkg/db/inspect reader
```

`ManagementDBInspectReader` is the narrow remote half of the DB Inspect manager
page. It only chooses local versus remote execution for the requested
`node_id`; empty `node_id` normalization, node validation, query validation,
and response DTO shaping stay in `internal/usecase/management` and
`internal/access/manager`. The adapter preserves read-only diagnostics and
does not merge cluster rows, expose filesystem paths, or mutate storage.

## Management Diagnostics Flow

```text
management.DiagnosticsReader/DiagnosticsTrackingOperator
  -> local node_id: app-owned internal diagnostics store
  -> remote node_id: access/node Manager Diagnostics RPC client
  -> cluster CallRPC(target node, RPCManagerDiagnostics)
  -> target node access/node Manager Diagnostics RPC handler
  -> target node app-owned internal diagnostics store
```

`ManagementDiagnosticsReader` is the narrow remote half of the diagnostics
manager page. It only chooses local versus remote execution for the selected
node; aggregate target selection, skipped-node notes, tracking-rule fan-out,
and response DTO shaping stay in `internal/usecase/management`. The adapter
does not query legacy `internal` diagnostics state.

Bench runtime controls flow from internal HTTP through `internal/infra/cluster`, `pkg/cluster.Node`, `pkg/cluster/channels.Service`, and finally the hosted Channel runtime runtime. These routes are benchmark-only observation/cleanup controls and do not replace the gateway SEND activation path.

## Channel Metadata Flow

`ChannelMetadataStore` adapts `internal/usecase/channel.Store` and
`internal/usecase/message.PermissionStore` to the cluster Slot metadata
facade. When the cluster node also exposes `ChannelMembershipNode`, the same
adapter implements the channel usecase `MembershipIndex` port for UID-owned
reverse membership projection.

```text
channel usecase Store method
  -> ChannelMetadataNode facade
  -> pkg/cluster.Node
  -> Slot metadata read or Slot Raft propose

ordinary subscriber projection
  -> ChannelMembershipNode facade
  -> pkg/cluster.Node
  -> group by UID hash slot
  -> Slot Raft propose to the UID-owned hash slot
```

The adapter does not contain channel business rules. It clones subscriber UID
slices before forwarding mutations and converts the usecase's optional
subscriber mutation version into the required cluster facade argument. The
membership facade is separate from the channel metadata facade so tests and
future adapters can expose read/write channel metadata without implicitly
claiming support for the reverse membership index.
For message permission checks, the adapter exposes channel metadata reads,
subscriber point lookups, and subscriber-set non-emptiness. It uses direct
cluster lookup facades when available and falls back to bounded subscriber
page scans only for test or alternate nodes that do not expose point lookups.
When configured with `ChannelAppendMetadataCache`, successful channel metadata
upserts refresh append fanout metadata and deletes remove cached entries; final
subscriber mutation versions are still refreshed by the channel usecase
mutation observer after subscriber changes commit.

## Conversation Flow

`ConversationStore` adapts `internal/usecase/conversation` active-row,
durable state, read mutation, delete mutation, last-message, and recent-message
ports to cluster facades. Conversation rows are UID-owned metadata records,
while last-message display data and sync recents are read from channel-owned
message logs. When configured, list last-message reads run with a bounded
worker count; missing tails are skipped per row while routing/readiness errors
fail the request.

```text
conversation list usecase
  -> ListConversationActiveView(normal kind, uid, active cursor)
       -> wraps ListConversationActivePage(normal kind, uid, active cursor)
       -> UID-owned normal-kind conversation rows routed by UID hash slot
  -> GetLastVisibleMessages(current page keys)
       -> ReadChannelLastVisible(channel, visible_after_seq)
       -> if the tail is SyncOnce/CMD, reverse-scan committed rows for the latest ordinary message
       -> channel-owned route resolves the Channel runtime leader
       -> missing channel or no visible message returns no last message for that row

conversation sync usecase
  -> ListConversationActiveView(normal kind, uid, zero cursor)
       -> bounded active working-set scan
  -> GetConversationState(normal kind, uid, channel)
       -> durable UID-owned row for client-known overlay candidates
  -> GetLastVisibleMessages(candidate keys)
       -> newest channel-owned message for sync selection
  -> GetRecentMessages(final returned keys)
       -> ReadChannelCommitted(reverse, latest, paged)
       -> filter SyncOnce/CMD rows from ordinary legacy recents
       -> channel-owned committed rows used for legacy recents

conversation read-state mutation usecase
  -> UpsertConversationStates(uid-owned normal-kind read row)
       -> UpsertConversationStatesBatch
       -> UID-owned Slot metadata propose

conversation delete usecase
  -> HideConversations(uid-owned normal-kind delete barrier)
       -> ConversationAuthorityClient resolves the UID's exact RouteTarget
       -> local or RPC authority HideConversationsForTarget
       -> authority-owned HideConversationsBatch propose clears durable active_at
       -> authority reconciles the exact active-cache row before returning
```

The adapter clones row slices and message payloads across the boundary. It does
not own ordering, cursor, sync filtering, unread, read-cursor, delete-barrier,
or sparse-active rules; those stay in the conversation usecase so access
adapters can share the same list, sync, and mutation semantics.
It does own the storage-facing split between ordinary conversation hydration
and CMD sync hydration: normal conversation last-message/recent reads skip
`SyncOnce`/command-channel rows, while `CMDSyncStore` returns only those rows.
`metadb.ErrNotFound` and `channel.ErrChannelNotFound` during a single
last-message read mean that row has no display message, not that the whole list
failed. Routing, readiness, and other read errors still fail the request.

`ConversationAuthorityClient` routes UID-owned active cache calls to the
current authority leader and leaves cache/list business rules inside the local
authority implementation. Legacy patch admission resolves each `ActivePatch`
UID with `RouteKey(uid)`, maps the route's Slot leader term and Slot config
epoch into `RouteTarget`, groups patches by exact `RouteTarget`, and sends each
group to the local authority when the target leader is this node or through
access/node Conversation Authority RPC when the leader is remote. The local
authority epoch remains diagnostic metadata and is not the distributed
identity. Legacy patch admission is best-effort and does not retry
route-not-ready, stale-route, or not-leader errors; callers are expected to log
and drop failed active admission.
Active-batch admission resolves the affected UID set as `SenderUID` plus each
unique recipient UID through one lightweight `RouteAuthoritiesPartial`
snapshot per attempt when supported, with `RouteKeysPartial` retained as the
compatibility fallback,
coalesces duplicate recipient entries with `IsSender` OR semantics, and sends
one target-scoped batch per group. Aligned key-specific route failures do not
discard successfully routed siblings. Only the sender-owned target receives
`SenderUID`; other target batches carry an empty `SenderUID` and only their
recipient subset, so a receiver authority cannot cache the sender row by
mistake. Each target-scoped batch preserves the source
`metadb.ConversationKind`; invalid or zero kinds are left for downstream
validation instead of being normalized. If the sender is not in the recipient
set, the sender target still receives a sender-only batch. Exact target groups
are packed by `LeaderNodeID` for transport, but the leader envelope never
replaces each group's physical hash-slot, logical Slot Raft Group, leader-term,
and config-epoch fence. Active-batch admission retries route-not-ready,
stale-route, not-leader, and background Slot proposal backpressure within a
small bounded fresh-route window. Only aligned failed groups or failed UID
route items are resolved again; successful siblings are never issued twice.
Continued failure is returned to the caller so the post-commit path remains
bounded.
The routed active-batch surface is the normal channelappend fast path. Its first
attempt consumes caller-supplied exact target groups without another route
snapshot. If stale-route, not-leader, route-not-ready, proposal backpressure, or
a retryable transport failure affects a group, only that group's sender and
recipient rows are resolved again, and only once. Successfully admitted sibling
groups are never replayed. A terminal sibling failure is retained as the
overall result but does not suppress that one bounded retry for independent
retryable siblings. If backoff, fresh routing, or the retry attempt also
fails, the adapter joins that error with the retained terminal failure so
callers can classify both causes with `errors.Is`. The legacy
`AdmitActiveBatch` surface retains its
three-attempt compatibility window for callers that do not already own an
aligned route snapshot.
The normal one-batch path coalesces UIDs and recipient roles once, counts each
exact target group before allocation, and fills disjoint capacity-limited
slices from one shared recipient backing store. This avoids per-target growth
and intermediate retry copies for high-fanout admissions. A failed target is
cloned before retry so retaining one failed subset cannot retain the whole
happy-path backing allocation.
Delete-barrier writes group rows by UID and use the same fresh-target retry
loop as authority reads. The actual Slot write runs on the resolved authority
leader, which reconciles its cache and revalidates the exact target before it
returns. A stale target therefore causes an idempotent retry against the new
leader instead of leaving a clean cache baseline detached from durable state.
The remote RPC client chunks large patch groups at the codec collection limit.
Active-batch groups for the same remote leader are packed into one or more
`WKVC2` envelopes of at most 4,096 aggregate active rows and receive `WKVc2`
group-aligned statuses. A target group larger than one envelope is split while
preserving the sender only in its first fragment. The local path uses the same
bounded packing and optional bulk authority contract. A retryable transport
failure re-routes only the groups from the failed envelope; completed sibling
leaders and envelopes are not replayed. Context cancellation/deadline, codec,
and business errors are not classified as transport retries. List resolves the
requested UID once per retry attempt and reads the active view from that
authority target; the active-view response is not satisfied by a local DB-only
fallback when the UID authority is remote. Drain uses the caller-supplied exact
target for authority handoff.

```text
ConversationAuthorityClient
  -> AdmitPatches([]ActivePatch)
       -> RouteKey(patch.UID) for each patch
       -> group by RouteTarget
       -> local conversation authority for local groups
       -> access/node Conversation Authority Admit RPC for remote groups
  -> AdmitActiveBatch(ActiveBatch)
       -> one RouteAuthoritiesPartial snapshot for all pending unique UIDs
          (RouteKeysPartial compatibility fallback)
       -> coalesce duplicate recipient entries with IsSender OR
       -> group by exact RouteTarget
       -> set SenderUID only on the sender target's batch
       -> pack exact groups by LeaderNodeID without weakening target fences
       -> bounded local bulk authority calls of at most 4,096 active rows
       -> bounded access/node WKVC2 bulk RPC envelopes per remote leader
       -> preserve group-aligned statuses and re-route only failed groups
  -> AdmitRoutedActiveBatches([]ConversationActiveTargetBatch)
       -> admit caller-supplied exact targets without an initial route lookup
       -> preserve every physical hash-slot and logical Slot fence
       -> fresh-route only failed groups once; never replay successful siblings
  -> ListConversationActiveView(kind, uid)
       -> RouteKey(uid)
       -> local conversation authority active view for kind when local
       -> access/node Conversation Authority List RPC carrying kind when remote
  -> HideConversations([]ConversationDelete)
     -> group by UID and reject any group above the bounded 4096-row RPC contract before leader selection
       -> group rows by UID
       -> resolve a fresh exact RouteTarget for each UID group
       -> local authority hide when local
       -> access/node Conversation Authority Hide RPC when remote
  -> DrainAuthority(target)
       -> local drain when target leader is this node
       -> access/node Conversation Authority Drain RPC when remote
```

List route-not-ready, stale-route, and not-leader results are retried with a
short bounded backoff so authority movement can settle without changing
conversation list semantics. Legacy patch admission returns those errors
directly, while active-batch admission makes a small bounded fresh-route retry
window. A route snapshot outer failure retries the pending set, while an
aligned UID failure retries only that UID subset after already-routed siblings
have completed. Raw
cluster route errors returned by remote RPC calls are mapped to the same
conversation route sentinels before retry decisions.

## Channel Append Authority Flow

`ChannelAppendClient` adapts the channelappend router authority ports to
cluster. It resolves canonical channel append authority through the narrow
`Node.ResolveChannelAppendAuthority` facade, which delegates to the hosted
Channel runtime service so metadata creation policy remains in `pkg/cluster/channels`.
It attaches the large-channel flag and subscriber mutation version from a shared
`ChannelAppendMetadataCache` when present; cache misses read durable channel
metadata once and populate the cache. Subscriber mutation observers refresh the
same cache, so hot channels avoid a foreground Slot metadata lookup on every
SEND while still seeing low-churn fanout metadata changes. The adapter maps
`channel.Meta` and recipient fanout metadata to `channelappend.AuthorityTarget`
with the canonical `ChannelID`, `ChannelKey`, `LeaderNodeID`, `Epoch`,
`LeaderEpoch`, `RouteGeneration`, `Large`, and `SubscriberMutationVersion`.
It also exposes conditional authority invalidation to the router. Invalidation
delegates the generation through `cluster.Node` and removes only the exact
failed authority version; zero generation retains static/legacy tuple
compatibility. Subscriber/fanout metadata caching remains independent.

```text
channelappend.Router
  -> ChannelAppendClient.ResolveAppendAuthority(canonical channel)
       -> cluster.Node.ResolveChannelAppendAuthority
       -> channels.Service.ResolveAppendAuthority
       -> ChannelMetaEnsurer.EnsureChannelMeta when append would create metadata
       -> ChannelAppendMetadataCache hit: attach fanout metadata
       -> cache miss: cluster.Node.GetChannelMetadata and cache metadata
  -> local authority: channelappend.Group.SubmitLocal
  -> remote authority: ChannelAppendClient.ForwardSendBatch
       -> injected ChannelAppendRemoteForwarder
```

Route errors are translated at this adapter boundary:
`channel.ErrNotLeader` becomes `channelappend.ErrNotChannelAuthority`,
`channel.ErrStaleMeta` becomes `channelappend.ErrStaleRoute`, and
`channel.ErrNotReady` plus cluster readiness errors become
`channelappend.ErrRouteNotReady`. Remote forwarding is supplied by the
`internal/access/node` Channel Append RPC client; remote item results are
returned item-aligned without interpreting successful payloads.

## Recipient Authority Flow

`RecipientAuthorityResolver` is the cluster adapter for channelappend's
post-commit recipient grouping seam. `NewRecipientAuthorityResolver` accepts the
concrete app-wired cluster runtime, verifies only the required single-key route
capability, and returns nil when that capability is unavailable. All optional
batch capability probing stays private to the adapter, so the app composition
root does not import or reinterpret `pkg/cluster` route DTOs.

```text
channelappend recipient UID page
  -> RecipientAuthorityResolver.ResolveRecipientAuthorities
  -> prefer RouteAuthoritiesPartial for one aligned lightweight snapshot
  -> otherwise RouteAuthorities, RouteKeysPartial, RouteKeys, or RouteKey
  -> map cluster route errors to channelappend route errors
  -> convert each successful route to RecipientAuthorityTarget
  -> preserve key-specific errors and input alignment
```

The adapter preserves physical hash slot, logical Slot, actual leader, leader
term, config epoch, route revision, and diagnostic authority epoch. A zero
actual leader fails closed as `channelappend.ErrRouteNotReady`; preferred leader
metadata is not substituted for the actual elected leader. Batch result
cardinality mismatches also fail closed. When an observer is installed, exactly
one aggregate event records a bounded outcome, requested item count, distinct
successful physical hash-slot target count, and duration. The common 256-slot
target count uses a fixed bitmap and does not allocate; UID, route, Slot, and
node identities never enter observation labels.

## User Metadata Flow

`UserMetadataStore` adapts `internal/usecase/user` user/device metadata ports
to the cluster UID Slot metadata facade.

```text
user usecase metadata method
  -> UserMetadataNode facade
  -> pkg/cluster.Node
  -> UID Slot metadata read or Slot Raft propose
```

The adapter does not contain user business rules. It forwards create-only UID
metadata and per-device token upserts to cluster, while reads route by UID to
the current hash-slot metadata store.

## Error Mapping

```text
channel.ErrNotLeader / cluster.ErrNotLeader      -> channelappend.ErrNotLeader
channel.ErrStaleMeta / channel.ErrNotReplica     -> channelappend.ErrStaleRoute
channel.ErrChannelNotFound                         -> channelappend.ErrChannelNotFound
channel.ErrBackpressured                           -> channelappend.ErrBackpressured
cluster.ErrRouteNotReady / cluster.ErrNoSlotLeader / channel.ErrNotReady -> channelappend.ErrRouteNotReady
context cancellation/deadline                        -> unchanged
other errors                                         -> channelappend.ErrAppendFailed wrapping source
```

## Presence Authority Flow

`PresenceAuthorityClient` adapts the internal presence usecase authority port
and owner-action port to `pkg/cluster` UID routing and
`internal/access/node` RPC. The adapter does not own gateway activation
policy, authority conflict rules, or local session mutation rules.
`PresenceDirectoryAuthority` is the local-side adapter from the node-local
runtime presence directory to the fenced authority RPC contract; app only
constructs it and injects it into the client and access adapter.

```text
presence.Route / uid
  -> cluster.RouteKey(uid)
  -> presence.RouteTarget
  -> local accessnode.PresenceAuthority when target leader is this node
  -> access/node PresenceAuthority RPC client when target leader is remote

[]uid
  -> one cluster.RouteKeysPartial(uids) snapshot lookup
  -> aligned []presence.RouteTargetResult with complete route fences

[{exact presence.RouteTarget, uids}]
  -> group target batches by LeaderNodeID without another route lookup
  -> local leader: one target-batch authority read per exact target
  -> remote leader: one access/node batch envelope RPC per leader
  -> aligned []presence.EndpointLookupResult with group-scoped errors

presence.RouteAction
  -> action.OwnerNodeID
  -> local accessnode.PresenceOwner when owner is this node
  -> access/node PresenceOwner RPC client when owner is remote
```

`RegisterRoute`, `UnregisterRoute`, and `EndpointsByUID` resolve their target
from the UID carried by the request. `CommitRoute` and `AbortRoute` resolve
their target from the UID remembered for the pending token returned by
`RegisterRoute`. Resolved targets carry the Slot leader term and Slot config
epoch from cluster routes; the authority epoch is only local diagnostic
metadata. Touch batching uses `TouchRoutesTo(target, routes)` because the app
worker groups dirty owner sessions by the exact authority target observed
during flush. The adapter sends the batch locally when the target leader is
this node and uses access/node RPC for remote leaders.

Batch target resolution preserves input order and cardinality without falling
back to per-UID `RouteKey` calls or retrying individual failed entries inside
the batch. A batch-level routing or context error is copied to every aligned
item, while a key-specific error is mapped only onto its corresponding item;
successful items retain the physical hash slot, logical Slot, leader term, config
epoch, route revision, and authority epoch fences from the single cluster
routing snapshot. The app worker owns exact route requeue for failed items so a
later flush can resolve them against a newer routing snapshot.

Target-aware endpoint lookup consumes those already-resolved target fences. It
never calls `RouteKey` or `RouteKeysPartial` on the happy path. Multiple exact
hash-slot targets for the same remote leader share one RPC envelope, so network
work scales with leader count instead of recipient or hash-slot count.
Independent leader envelopes execute concurrently under a fixed bound while
aligned results remain in original input order. Each leader execution boundary
also converts a local-authority or remote-client panic into a terminal error for
only that leader's aligned groups, so a child worker cannot terminate the
process or discard successful sibling leaders. The local path uses the optional
target-group batch authority surface once for all groups assigned to the local
leader and keeps each exact target fence intact. The production local authority
then groups those targets by directory shard, so physical hash-slot groups touch
at most the configured shard count of read locks rather than being collapsed
into the smaller logical Slot set. Remote
transport or response-cardinality failures are copied only to groups assigned
to that leader; per-group stale/not-leader errors remain aligned and do not
discard successful results from other groups.
If one group returns not-leader, stale-route, or route-not-ready after waiting
in the asynchronous delivery queue, the adapter batch-resolves current targets
for only that group's UIDs and retries it once. Successful sibling groups are
not issued again. The retry remains aligned to the original group and rejects
an inconsistent refresh whose UIDs no longer share one exact target.
An optional observer emits exactly once for each leader execution stage, after
the aligned results are final. It uses only the fixed paths `local_bulk`,
`remote_bulk`, and `legacy_fallback`, bounded outcomes, and a boolean stale
retry label; item count, exact-target group count, and duration are numeric.
The observer is outside UID and target loops, and never labels UIDs, node IDs,
hash slots, route revisions, or authority epochs.

For the individual register, unregister, endpoint, commit, and abort paths, if
route resolution reports route-not-ready, stale routing, or not-leader, the
adapter waits a short bounded backoff and resolves a fresh `RouteKey` within a
bounded retry window. Authority calls retry stale routing and not-leader the
same way, while authority-side route-not-ready is returned as its original
bounded presence error so pending token cleanup semantics stay explicit.

Best-effort unregister calls are bounded by a short context timeout so gateway
close and rollback paths do not block indefinitely on route lookup or node RPC.

## Delivery Push Flow

`DeliveryPusher` adapts the internal delivery runtime pusher port to local
owner delivery or `internal/access/node` delivery RPC.

```text
delivery.PushCommand
  -> OwnerNodeID == localNodeID
       -> local runtime delivery Pusher
  -> OwnerNodeID != localNodeID
       -> access/node Delivery Push RPC client
       -> remote owner DeliveryOwnerPush
```

When the command targets this node but no local delivery pusher is installed,
the adapter marks all routes dropped because no owner-local session runtime can
accept them. When a remote client is missing or the remote RPC fails, it marks
all routes retryable and returns nil error so the delivery runtime can apply its
normal retry policy.

## Delivery Fanout Partition Flow

`DeliveryPartitioner` adapts the cluster UID hash-slot route table to
`runtime/delivery.Partitioner`. It caches the last valid partition layout by
route revision and hash-slot count, reuses the cached layout for repeated reads,
and falls back to the last valid layout when the route table is momentarily not
ready. On a cache miss, it reads the current snapshot hash-slot count, routes
each hash slot through `RouteHashSlot`, and merges contiguous hash-slot ranges
with the same leader into delivery partitions.

```text
cluster Snapshot.HashSlotCount
  -> RouteHashSlot(hashSlot)
  -> contiguous ranges grouped by Route.Leader
  -> runtime/delivery.Partition{LeaderNodeID, HashSlotStart, HashSlotEnd}
```

Route-table-not-ready, no-leader, and route lookup failures map to
`runtime/delivery.ErrRouteNotReady` only when no last valid partition layout is
available, so the async delivery sink can record the failure without adding
cluster-specific errors to the runtime package.
