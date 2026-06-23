# internalv2/infra/cluster Flow

## Responsibility

`internalv2/infra/cluster` adapts internalv2 usecase/runtime ports to
`pkg/clusterv2` and `pkg/channelv2`. It maps channelappend append DTOs to
`pkg/channelv2` DTOs, resolves channel append authority through clusterv2, adapts
legacy-compatible channel metadata usecase calls to clusterv2 Slot metadata
facades, adapts legacy-compatible user metadata calls and manager user scans to
UID Slot metadata facades, adapts read-only manager business channel and
channel runtime metadata scans to clusterv2 Slot metadata reads, adapts conversation reads and read/delete
mutations to UID-owned conversation rows plus channel-owned committed message
logs, adapts read-only manager message pages to committed ChannelV2 reads,
adapts manager message retention requests to fenced Slot metadata advances, and
routes manager connection reads over clusterv2 node RPC, routes manager
distributed log reads to node-local clusterv2 log storage or peer RPC, routes
manager Slot Raft status and compaction operations to the selected node-local clusterv2
operation or peer RPC, adapts manager Slot leader transfer intents to
clusterv2 control, routes manager Controller Raft operations to node-local
clusterv2 operations or peer RPC, routes ordinary application log reads to the
selected node's app-owned local reader or peer RPC, routes manager DB Inspect
reads to node-local inspect readers or peer RPC, routes manager diagnostics
reads and tracking-rule mutations to the selected node's internalv2 diagnostics
store or peer RPC, routes manager plugin inventory reads and lifecycle
mutations to the selected node's plugin lifecycle usecase over peer RPC, and
adapts presence/delivery ports to clusterv2 routing and node RPC.

## Management Snapshot Flow

`ManagementSnapshotReader` adapts `pkg/clusterv2.Node` control snapshots to the
internalv2 management usecase. It only exposes local control snapshot reads and
the local node ID for read-only manager node and Slot list rendering;
lifecycle, scale-in, onboarding, and Slot operations other than explicit
node-local Raft compaction stay unmigrated.

## Management Channel List Flow

`ChannelBusinessReader` adapts the management usecase's business channel list
port to the clusterv2 channel metadata scan facade. It only exposes physical
Slot page reads; list filtering, cursor validation, and manager response
shaping stay in the usecase and access layers. `ManagementChannelReader` is
the remote half for non-local `node_id` filters: it forwards page requests
through access/node manager channel RPC and preserves the management DTO page.

`ChannelRuntimeMetaReader` adapts the management usecase's channel cluster list
port to the clusterv2 runtime metadata scan facade. It only exposes read-only
physical Slot pages from `channel_runtime_meta`; `node_id`, `node_scope`,
channel ID filtering, max-message-sequence hydration, cursor validation, and
HTTP response shaping stay above this layer.

## Management User Flow

`UserMetadataStore` also exposes the management usecase's user list scan port
when the clusterv2 node supports `ScanUsersSlotPage`. The adapter keeps this as
a read-only Slot metadata facade; user list filtering, device/presence joins,
token reset, force-offline, and system UID normalization stay above this layer.

## Append Flow

```text
channelappend.AppendBatchRequest
  -> channelv2.AppendBatchRequest
     (expected channel/leader epochs fence stale authority writes)
     (trace id, diagnostics channel key, append attempt, and per-message trace metadata stay transient)
  -> ChannelAppendNode.AppendChannelBatch
  -> record sendtrace `channel.append.local` for traced messages after completion
  -> channelv2.AppendBatchResult
  -> channelappend.AppendBatchResult
```

This adapter is the durable append ownership boundary: outbound message payloads
are cloned before entering `channelv2`, and inbound result payloads are cloned
unless the channelappend runtime marks them unnecessary for SENDACK-only flows.
Commit mode, expected authority epoch fences, and typed errors are mapped at
this boundary so the channelappend runtime stays cluster-agnostic.
The adapter records channel append sendtrace events only when tracing is enabled
and the request carries trace metadata, so untraced appends do not pay extra
timing or event-allocation cost.
When `ChannelAppendNode.AppendChannelBatch` returns a batch-level error, the
adapter logs `internalv2.infra.cluster.channel_append_batch_failed` at ERROR
level with the channel identity, authority fence, attempt, record count,
mapped error result, and raw source error before returning the mapped
channelappend error.

## Message Sync Read Flow

```text
message.ChannelMessageQuery
  -> channelstore.ReadCommittedRequest
     (pull-up reads forward; pull-down/latest reads reverse with limit+1)
  -> ChannelMessageReadNode.ReadChannelCommitted
  -> channelv2/store committed messages
  -> message.ChannelMessagePage
```

The reader adapter trims `limit+1` results to preserve the legacy `more`
contract and returns messages to the usecase in ascending sequence order. It
maps only the fields currently carried by ChannelV2 committed messages; legacy
HTTP-only field shaping remains in `internalv2/access/api`.

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

`CMDSyncStore` is the single internalv2 adapter for durable command-message
sync. It reads CMD rows from the unified UID-owned conversation projection and
advances read progress by writing CMD-kind `ConversationState` rows back through
clusterv2 Slot metadata. It does not create a second CMD-specific metadata
table or a pending overlay. Channel log reads use ChannelV2 committed forward
reads, filter out ordinary source-channel messages, and return cloned payloads
to keep access/usecase layers from aliasing storage-owned memory.

## Management Message Flow

```text
management.MessageReader.QueryMessages
  -> ChannelMessageReadNode.ReadChannelCommitted(reverse, before_seq, limit+1)
  -> channelv2/store committed messages
  -> manager message DTO rows
```

The manager message adapter reads committed ChannelV2 rows in descending order
for the manager message page. It converts timestamps from milliseconds to Unix
seconds, clones payloads, applies the manager's optional message id and client
message number filters within the returned page, and leaves cursor encoding and
HTTP response shaping to `internalv2/access/manager`.

```text
management.MessageRetentionOperator.AdvanceMessageRetention
  -> MessageRetentionNode.GetChannelRuntimeMeta
  -> if local node is not the ChannelV2 leader, forward once to meta.Leader
  -> leader re-reads ChannelRuntimeMeta and verifies local leadership
  -> ReadChannelCommitted(reverse latest, min_seq = RetentionThroughSeq + 1)
  -> AdvanceChannelRetentionThroughSeq(fenced Slot metadata command)
  -> manager retention response
```

The manager retention adapter treats history deletion as logical channel log
compaction. It never deletes message rows. It computes a safe boundary no
higher than the latest committed visible message, then advances
`RetentionThroughSeq` through the Slot metadata FSM using the observed channel
epoch, leader epoch, leader ID, and lease fence. Dry-runs return the calculated
boundary without proposing. Requests first received on a non-leader forward to
the metadata leader when node RPC is available; the remote leader recomputes
all fences locally, so origin nodes never transmit stale metadata fences.
Missing leaders, stale routes, and local-only non-leader states return typed
retryable errors to the manager usecase.

## Management Channel RPC Flow

```text
management.RemoteBusinessChannelReader
  -> access/node Manager Channel RPC client
  -> clusterv2 CallRPC(target node, RPCManagerChannels)
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
  -> clusterv2 CallRPC(target node, RPCManagerConnection)
  -> target node access/node Manager Connection RPC handler
  -> target node owner-local online registry
```

`ManagementConnectionReader` is the narrow remote half of the manager
connection page. It forwards non-local `node_id` list/detail reads to the owner
node and preserves management usecase DTOs across the RPC boundary. Local
connection filtering and DTO shaping stay in the management usecase; this
adapter only chooses the typed node RPC client.

## Management Plugin Flow

```text
management.RemotePluginReader
  -> access/node Manager Plugin RPC client
  -> clusterv2 CallRPC(target node, RPCManagerPlugins)
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
mutations to clusterv2 metadata. UID reads and mutations stay UID-owned.
Plugin-centric pages use clusterv2 `ListPluginBindingsByPluginNo`, which owns
hash-slot fan-out, Slot-leader routing, heap merge, and opaque cursor semantics;
the infra adapter only maps metadata rows to management DTOs and reports
`ErrPluginBindingsUnavailable` when the underlying node lacks that scanner.

## Management Log Flow

```text
management.LogReader
  -> local node_id: clusterv2.LocalControllerLogEntries/LocalSlotLogEntries
  -> remote node_id: access/node Manager Log RPC client
  -> target node access/node Manager Log RPC handler
  -> target node clusterv2 local log reader
```

`ManagementLogReader` preserves newest-first pagination and decoded payload
summaries across the infra boundary. It only chooses local versus remote
execution for the requested `node_id`; log pagination, Raft payload decoding,
and storage watermarks live in `pkg/clusterv2`, while HTTP parsing and manager
response shaping stay above this package.

## Management Slot Raft Flow

```text
management.SlotRaftOperator
  -> local node_id: clusterv2.LocalSlotRaftStatus(slot_id) or LocalCompactSlotRaftLog(slot_id)
  -> remote node_id: access/node Manager Slot Raft RPC client
  -> clusterv2 CallRPC(target node, RPCManagerSlotRaft)
  -> target node access/node Manager Slot Raft RPC handler
  -> target node clusterv2 local Slot Raft status or compaction
```

`ManagementSlotRaftOperator` only chooses local versus remote execution for the
requested node and Slot. HTTP parsing, permission checks, and response summary
shaping stay above this layer; actual Raft role/watermark reads and
snapshot/log compaction stay in `pkg/clusterv2` and `pkg/slot/multiraft`.

## Management Slot Leader Transfer Flow

```text
management.SlotRuntimeStatusReader
  -> clusterv2.LocalSlotRaftStatus(slot_id)

management.SlotLeaderTransferWriter
  -> clusterv2.RequestSlotLeaderTransfer(control.SlotLeaderTransferRequest)
  -> clusterv2 control runtime
```

The leader-transfer adapters do not implement Slot Raft mechanics. They expose
the local Slot Raft leader/voter status needed by the management usecase
preflight, then pass the validated control intent into clusterv2 control.

## Management Application Log Flow

```text
management.ApplicationLogReader
  -> local/empty node_id: app-owned node-local ordinary application log reader
  -> remote node_id: access/node Manager App Log RPC client
  -> clusterv2 CallRPC(target node, RPCManagerAppLogs)
  -> target node access/node Manager App Log RPC handler
  -> target node app-owned node-local ordinary application log reader
```

`ManagementApplicationLogReader` is distinct from the distributed Raft log
reader above. It only chooses local versus remote execution for ordinary
application log sources and entry pages; file discovery, parsing, filtering,
cursor handling, and path hiding remain owned by `internalv2/app` and
`internalv2/log`.

## Management DB Inspect Flow

```text
management.DBInspectReader
  -> local node_id: app-wired pkg/db/inspect reader for node-local storage
  -> remote node_id: access/node Manager DB Inspect RPC client
  -> clusterv2 CallRPC(target node, RPCManagerDBInspect)
  -> target node access/node Manager DB Inspect RPC handler
  -> target node app-wired pkg/db/inspect reader
```

`ManagementDBInspectReader` is the narrow remote half of the DB Inspect manager
page. It only chooses local versus remote execution for the requested
`node_id`; empty `node_id` normalization, node validation, query validation,
and response DTO shaping stay in `internalv2/usecase/management` and
`internalv2/access/manager`. The adapter preserves read-only diagnostics and
does not merge cluster rows, expose filesystem paths, or mutate storage.

## Management Diagnostics Flow

```text
management.DiagnosticsReader/DiagnosticsTrackingOperator
  -> local node_id: app-owned internalv2 diagnostics store
  -> remote node_id: access/node Manager Diagnostics RPC client
  -> clusterv2 CallRPC(target node, RPCManagerDiagnostics)
  -> target node access/node Manager Diagnostics RPC handler
  -> target node app-owned internalv2 diagnostics store
```

`ManagementDiagnosticsReader` is the narrow remote half of the diagnostics
manager page. It only chooses local versus remote execution for the selected
node; aggregate target selection, skipped-node notes, tracking-rule fan-out,
and response DTO shaping stay in `internalv2/usecase/management`. The adapter
does not query legacy `internal` diagnostics state.

Bench runtime controls flow from internalv2 HTTP through `internalv2/infra/cluster`, `pkg/clusterv2.Node`, `pkg/clusterv2/channels.Service`, and finally the hosted ChannelV2 runtime. These routes are benchmark-only observation/cleanup controls and do not replace the gateway SEND activation path.

## Channel Metadata Flow

`ChannelMetadataStore` adapts `internalv2/usecase/channel.Store` and
`internalv2/usecase/message.PermissionStore` to the clusterv2 Slot metadata
facade. When the cluster node also exposes `ChannelMembershipNode`, the same
adapter implements the channel usecase `MembershipIndex` port for UID-owned
reverse membership projection.

```text
channel usecase Store method
  -> ChannelMetadataNode facade
  -> pkg/clusterv2.Node
  -> Slot metadata read or Slot Raft propose

ordinary subscriber projection
  -> ChannelMembershipNode facade
  -> pkg/clusterv2.Node
  -> group by UID hash slot
  -> Slot Raft propose to the UID-owned hash slot
```

The adapter does not contain channel business rules. It clones subscriber UID
slices before forwarding mutations and converts the usecase's optional
subscriber mutation version into the required clusterv2 facade argument. The
membership facade is separate from the channel metadata facade so tests and
future adapters can expose read/write channel metadata without implicitly
claiming support for the reverse membership index.
For message permission checks, the adapter exposes channel metadata reads,
subscriber point lookups, and subscriber-set non-emptiness. It uses direct
clusterv2 lookup facades when available and falls back to bounded subscriber
page scans only for test or alternate nodes that do not expose point lookups.
When configured with `ChannelAppendMetadataCache`, successful channel metadata
upserts refresh append fanout metadata and deletes remove cached entries; final
subscriber mutation versions are still refreshed by the channel usecase
mutation observer after subscriber changes commit.

## Conversation Flow

`ConversationStore` adapts `internalv2/usecase/conversation` active-row,
durable state, read mutation, delete mutation, last-message, and recent-message
ports to clusterv2 facades. Conversation rows are UID-owned metadata records,
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
       -> channel-owned route resolves the ChannelV2 leader
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

conversation mutation usecase
  -> UpsertConversationStates(uid-owned normal-kind read row)
       -> UpsertConversationStatesBatch
       -> UID-owned Slot metadata propose
  -> HideConversations(uid-owned normal-kind delete barrier)
       -> HideConversationsBatch
       -> UID-owned Slot metadata propose that clears active_at
```

The adapter clones row slices and message payloads across the boundary. It does
not own ordering, cursor, sync filtering, unread, read-cursor, delete-barrier,
or sparse-active rules; those stay in the conversation usecase so access
adapters can share the same list, sync, and mutation semantics.
It does own the storage-facing split between ordinary conversation hydration
and CMD sync hydration: normal conversation last-message/recent reads skip
`SyncOnce`/command-channel rows, while `CMDSyncStore` returns only those rows.
`metadb.ErrNotFound` and `channelv2.ErrChannelNotFound` during a single
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
unique recipient UID, caches each UID's `RouteTarget` for the current attempt,
coalesces duplicate recipient entries with `IsSender` OR semantics, and sends
one target-scoped batch per group. Only the sender-owned target receives
`SenderUID`; other target batches carry an empty `SenderUID` and only their
recipient subset, so a receiver authority cannot cache the sender row by
mistake. Each target-scoped batch preserves the source
`metadb.ConversationKind`; invalid or zero kinds are left for downstream
validation instead of being normalized. If the sender is not in the recipient
set, the sender target still receives a sender-only batch. Active-batch
admission retries route-not-ready, stale-route, not-leader, and background Slot
proposal backpressure within a small bounded fresh-route window, regrouping
only target groups that failed on the prior attempt; continued failure is
returned to the caller so the post-commit path remains bounded.
The remote RPC client chunks large patch groups and active-batch recipient
groups at the codec collection limit before sending them. List resolves the
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
       -> RouteKey(SenderUID) plus RouteKey(recipient.UID) once per unique UID
       -> coalesce duplicate recipient entries with IsSender OR
       -> group by exact RouteTarget
       -> set SenderUID only on the sender target's batch
       -> local conversation authority for local groups
       -> access/node Conversation Authority ActiveBatch RPC for remote groups
  -> ListConversationActiveView(kind, uid)
       -> RouteKey(uid)
       -> local conversation authority active view for kind when local
       -> access/node Conversation Authority List RPC carrying kind when remote
  -> DrainAuthority(target)
       -> local drain when target leader is this node
       -> access/node Conversation Authority Drain RPC when remote
```

List route-not-ready, stale-route, and not-leader results are retried with a
short bounded backoff so authority movement can settle without changing
conversation list semantics. Legacy patch admission returns those errors
directly, while active-batch admission makes a small bounded fresh-route retry
window. Raw
clusterv2 route errors returned by remote RPC calls are mapped to the same
conversation route sentinels before retry decisions.

## Channel Append Authority Flow

`ChannelAppendClient` adapts the channelappend router authority ports to
clusterv2. It resolves canonical channel append authority through the narrow
`Node.ResolveChannelAppendAuthority` facade, which delegates to the hosted
ChannelV2 service so metadata creation policy remains in `pkg/clusterv2/channels`.
It attaches the large-channel flag and subscriber mutation version from a shared
`ChannelAppendMetadataCache` when present; cache misses read durable channel
metadata once and populate the cache. Subscriber mutation observers refresh the
same cache, so hot channels avoid a foreground Slot metadata lookup on every
SEND while still seeing low-churn fanout metadata changes. The adapter maps
`channelv2.Meta` and recipient fanout metadata to `channelappend.AuthorityTarget`
with the canonical `ChannelID`, `ChannelKey`, `LeaderNodeID`, `Epoch`,
`LeaderEpoch`, `Large`, and `SubscriberMutationVersion`.

```text
channelappend.Router
  -> ChannelAppendClient.ResolveAppendAuthority(canonical channel)
       -> clusterv2.Node.ResolveChannelAppendAuthority
       -> channels.Service.ResolveAppendAuthority
       -> ChannelMetaEnsurer.EnsureChannelMeta when append would create metadata
       -> ChannelAppendMetadataCache hit: attach fanout metadata
       -> cache miss: clusterv2.Node.GetChannelMetadata and cache metadata
  -> local authority: channelappend.Group.SubmitLocal
  -> remote authority: ChannelAppendClient.ForwardSendBatch
       -> injected ChannelAppendRemoteForwarder
```

Route errors are translated at this adapter boundary:
`channelv2.ErrNotLeader` becomes `channelappend.ErrNotChannelAuthority`,
`channelv2.ErrStaleMeta` becomes `channelappend.ErrStaleRoute`, and
`channelv2.ErrNotReady` plus clusterv2 readiness errors become
`channelappend.ErrRouteNotReady`. Remote forwarding is supplied by the
`internalv2/access/node` Channel Append RPC client; remote item results are
returned item-aligned without interpreting successful payloads.

## User Metadata Flow

`UserMetadataStore` adapts `internalv2/usecase/user` user/device metadata ports
to the clusterv2 UID Slot metadata facade.

```text
user usecase metadata method
  -> UserMetadataNode facade
  -> pkg/clusterv2.Node
  -> UID Slot metadata read or Slot Raft propose
```

The adapter does not contain user business rules. It forwards create-only UID
metadata and per-device token upserts to clusterv2, while reads route by UID to
the current hash-slot metadata store.

## Error Mapping

```text
channelv2.ErrNotLeader / clusterv2.ErrNotLeader      -> channelappend.ErrNotLeader
channelv2.ErrStaleMeta / channelv2.ErrNotReplica     -> channelappend.ErrStaleRoute
channelv2.ErrChannelNotFound                         -> channelappend.ErrChannelNotFound
channelv2.ErrBackpressured                           -> channelappend.ErrBackpressured
clusterv2.ErrRouteNotReady / clusterv2.ErrNoSlotLeader / channelv2.ErrNotReady -> channelappend.ErrRouteNotReady
context cancellation/deadline                        -> unchanged
other errors                                         -> channelappend.ErrAppendFailed wrapping source
```

## Presence Authority Flow

`PresenceAuthorityClient` adapts the internalv2 presence usecase authority port
and owner-action port to `pkg/clusterv2` UID routing and
`internalv2/access/node` RPC. The adapter does not own gateway activation
policy, authority conflict rules, or local session mutation rules.

```text
presence.Route / uid
  -> clusterv2.RouteKey(uid)
  -> presence.RouteTarget
  -> local accessnode.PresenceAuthority when target leader is this node
  -> access/node PresenceAuthority RPC client when target leader is remote

presence.RouteAction
  -> action.OwnerNodeID
  -> local accessnode.PresenceOwner when owner is this node
  -> access/node PresenceOwner RPC client when owner is remote
```

`RegisterRoute`, `UnregisterRoute`, and `EndpointsByUID` resolve their target
from the UID carried by the request. `CommitRoute` and `AbortRoute` resolve
their target from the UID remembered for the pending token returned by
`RegisterRoute`. Resolved targets carry the Slot leader term and Slot config
epoch from clusterv2 routes; the authority epoch is only local diagnostic
metadata. Touch batching uses `TouchRoutesTo(target, routes)` because the app
worker groups dirty owner sessions by the exact authority target observed
during flush. The adapter sends the batch locally when the target leader is
this node and uses access/node RPC for remote leaders.

If route resolution reports route-not-ready, stale routing, or not-leader, the
adapter waits a short bounded backoff and resolves a fresh `RouteKey` within a
bounded retry window. Authority calls retry stale routing and not-leader the
same way, while authority-side route-not-ready is returned as its original
bounded presence error so pending token cleanup semantics stay explicit.

Best-effort unregister calls are bounded by a short context timeout so gateway
close and rollback paths do not block indefinitely on route lookup or node RPC.

## Delivery Push Flow

`DeliveryPusher` adapts the internalv2 delivery runtime pusher port to local
owner delivery or `internalv2/access/node` delivery RPC.

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

`DeliveryPartitioner` adapts the clusterv2 UID hash-slot route table to
`runtime/delivery.Partitioner`. It caches the last valid partition layout by
route revision and hash-slot count, reuses the cached layout for repeated reads,
and falls back to the last valid layout when the route table is momentarily not
ready. On a cache miss, it reads the current snapshot hash-slot count, routes
each hash slot through `RouteHashSlot`, and merges contiguous hash-slot ranges
with the same leader into delivery partitions.

```text
clusterv2 Snapshot.HashSlotCount
  -> RouteHashSlot(hashSlot)
  -> contiguous ranges grouped by Route.Leader
  -> runtime/delivery.Partition{LeaderNodeID, HashSlotStart, HashSlotEnd}
```

Route-table-not-ready, no-leader, and route lookup failures map to
`runtime/delivery.ErrRouteNotReady` only when no last valid partition layout is
available, so the async delivery sink can record the failure without adding
cluster-specific errors to the runtime package.
