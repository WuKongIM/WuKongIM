# internal/access/node Flow

## Responsibility

`internal/access/node` owns node-to-node RPC adaptation for the internal
path. It decodes deterministic binary payloads, calls entry-agnostic authority,
owner, delivery, or channel-write ports, and encodes stable binary responses.
It does not own presence conflict policy, channel routing policy, retries,
leases, or gateway session state.

## Presence Authority RPC

```text
remote authority client
  -> encode W K V P 2 request
  -> cluster RPCPresenceAuthority
  -> Adapter.HandlePresenceAuthorityRPC
  -> PresenceAuthority method
  -> encode W K V R 2 response
  -> client maps status to typed authority error
```

Supported authority calls:

- `RegisterRoute(RouteTarget, Route)`
- `CommitRoute(RouteTarget, PendingRouteToken)`
- `AbortRoute(RouteTarget, PendingRouteToken)`
- `UnregisterRoute(RouteTarget, RouteIdentity, ownerSeq)`
- `EndpointsByUID(RouteTarget, uid)`
- `EndpointsByTargets([]{RouteTarget, []uid})`
- `TouchRoutes(RouteTarget, []Route)`

`EndpointsByTargets` is the bounded fanout lookup path. One request carries
multiple exact authority targets that all name the destination leader, and the
response stays aligned with the input groups. Each group has its own stable
status and routes, so a stale target does not discard successful sibling
groups. Authorities may implement `EndpointsByTargets` to resolve the complete
ordered group collection in one call. The production directory uses that seam
to lock each touched directory shard once while retaining every group's
complete target fence and aligned status. Authorities that do not expose that
seam may implement `EndpointsByUIDs` to validate and read one target under a
single directory lock; otherwise the adapter preserves compatibility by calling
`EndpointsByUID` for each UID in that group. Both the
group count and aggregate UID/route counts use the presence RPC collection
limit. A group that would exceed the per-response route budget is returned as a
group-scoped rejection while bounded sibling groups keep their aligned results.
The original single-UID operation and its `WKVP2`/`WKVR2` byte layout
remain unchanged. During a rolling upgrade, an older peer reports the new op id
as unsupported; the new client recognizes only that capability error and falls
back to the original single-UID RPC for the affected destination. Other
transport failures do not trigger the compatibility fanout.

## Presence Owner RPC

```text
remote owner-action client
  -> encode W K V P 2 request
  -> cluster RPCPresenceOwner
  -> Adapter.HandlePresenceOwnerRPC
  -> PresenceOwner.ApplyRouteAction
  -> encode W K V R 2 response
```

Supported owner calls:

- `ApplyRouteAction(RouteAction)`

## Delivery Push RPC

```text
remote delivery client
  -> encode W K V D 1 request
  -> cluster RPCDeliveryPush
  -> Adapter.HandleDeliveryPushRPC
  -> DeliveryOwnerPush.Push
  -> encode W K V d 1 response
```

Delivery push requests carry one `runtime/delivery.PushCommand` in the stable
field order `OwnerNodeID`, `Envelope`, and `Routes`. The envelope includes the
committed message identifiers, sender echo-suppression fields, payload, red-dot
flag, and request-scoped UIDs. Responses carry status plus accepted, retryable,
and dropped route groups.

## Delivery Fanout RPC

```text
remote delivery fanout router
  -> encode W K V F 1 request
  -> cluster RPCDeliveryFanout
  -> Adapter.HandleDeliveryFanoutRPC
  -> DeliveryFanoutRunner.RunTask
  -> encode W K V f 1 response
```

Delivery fanout requests carry one `runtime/delivery.FanoutTask` in the stable
field order `Envelope`, `Partition`, `Cursor`, and `Attempt`. The receiving
node runs only the subscriber fanout task; owner-node delivery still uses the
separate Delivery Push RPC after presence resolution.

## Conversation Authority RPC

```text
remote conversation authority client
  -> encode W K V C 1 single-target request
     or W K V C 2 multi-target active-batch request
  -> cluster RPCConversationAuthority
  -> Adapter.HandleConversationAuthorityRPC
  -> ConversationAuthority port
     or optional ConversationBatchAuthority port
  -> encode W K V c 1 single-target response
     or W K V c 2 group-aligned response
```

Supported conversation authority calls:

- `AdmitPatches(RouteTarget, []ActivePatch)`
- `AdmitActiveBatch(RouteTarget, conversationactive.ActiveBatch)`
- `HideConversationsForTarget(RouteTarget, []metadb.ConversationDelete)`
  preserves canceled/deadline status across the node RPC boundary, matching local authority calls.
- `ListConversationActiveViewForTarget(RouteTarget, kind, uid, activeCursor, limit)`
- `DrainAuthority(RouteTarget)`

The RPC boundary is deliberately narrow:

- Admit carries already-materialized UID active patches to the current
  authority target for compatibility and handoff paths. Callers own patch
  construction; this package only transports the exact patch collection it
  receives.
- Active-batch admit carries the channelappend output directly to one routed UID
  authority target. Sender/recipient route grouping is performed by
  `internal/infra/cluster`; this package only transports the exact batch
  subset it receives.
- Bulk active-batch admit carries multiple exact targets for one destination
  leader in one `WKVC2` envelope. Every group retains its own hash slot, Slot,
  leader term, config epoch, route revision, and authority epoch fence. The
  `WKVc2` response contains one stable status per input group in the same
  order, so one stale target does not discard successful sibling groups. The
  adapter uses `ConversationBatchAuthority` when implemented and otherwise
  preserves compatibility by calling `AdmitActiveBatch` once per group.
- Bulk request group count and aggregate active-row count are both limited to
  4,096. The client rejects a zero or mismatched destination leader before
  transport, and both codecs reject malformed, truncated, oversized, unknown-
  status, or trailing data. Existing `WKVC1`/`WKVc1` bytes and the single-group
  API remain unchanged.
- During a rolling upgrade, only the exact old-peer `WKVC2` invalid-codec
  remote error enables fallback to the original `WKVC1` calls. Other transport
  or protocol errors do not fan out. Unsupported capability is cached per
  client and destination node for a bounded TTL to avoid repeated hot-path
  probes. A transport cancellation is normalized to `context.Canceled`; a
  retryable connection timeout remains distinct for the routed client to
  classify without weakening the caller deadline.
- Hide carries one exact, ordered `ConversationDelete` collection to the fenced
  UID authority target. The client does not split the mutation collection:
  collections above 4,096 entries fail before transport, and malformed or
  oversized wire collections fail closed during decode. The adapter waits for
  the authority result and maps it through the existing route status contract.
  The `WKVC1` hide extension follows the existing common request prefix and
  appends `DeleteCount`, then each delete in the stable field order `UID`,
  `Kind`, `ChannelID`, `ChannelType`, `DeletedToSeq`, and `UpdatedAt`. Existing
  operation byte layouts are unchanged.
- List reads the target-owned active view for one `metadb.ConversationKind`
  from the authority node. The local authority implementation decides how to
  merge unflushed cache rows with DB rows; this package only transports the
  request and response.
- Drain asks an authority node to flush and retire one exact `RouteTarget`
  during handoff. Handoff ordering and cache state transitions stay in the app
  authority runtime.
- The client chunks Admit patch collections at the codec collection limit before
  calling cluster RPC. Raw transport errors are returned to the infra/cluster
  route adapter; this package does not decide whether they should retry.
- The client also chunks active-batch recipient collections at the same codec
  collection limit. It preserves the batch sender field exactly as supplied by
  the routed caller.

## Channel Append RPC

```text
remote channel append forwarder
  -> encode W K V A 2 request
  -> cluster RPCChannelAuthoritySend
  -> ChannelAppendAdapter.HandleChannelAppendRPC
  -> ChannelAppend.SubmitForAuthority
  -> encode W K V a 1 response
```

Channel Append RPC transports one exact `channelappend.AuthorityTarget` plus
item-aligned `channelappend.SendCommand` values to the target channel authority
node. The target includes recipient fanout metadata (`Large` and
`SubscriberMutationVersion`) so the authority reactor can choose paged
large-channel fanout or cached non-large subscriber snapshots without resolving
metadata again. The server only submits to the local channel authority port; it
does not resolve routes, create proxy channel state, append directly outside
the authority reactor, or run post-commit side effects outside that reactor.
The client skips canceled or expired items before transport, normalizes
transport canceled/timeout errors to standard context errors, maps unavailable
target-node transport errors (`ErrDialFailed`, `ErrNodeNotFound`, `ErrStopped`,
connection reset/refused/closed, broken pipe, and EOF) to
`channelappend.ErrRouteNotReady`, and preserves active item order in returned
item-aligned results.

## Manager Connection RPC

```text
remote manager connection reader
  -> encode W K V M 2 request
  -> cluster RPCManagerConnection
  -> Adapter.HandleManagerConnectionRPC
  -> Management connection reader port
  -> encode W K V m 2 response
```

Manager Connection RPC transports the manager connection list/detail read
requests, lightweight runtime-summary reads, and `set_drain_mode` gateway
admission writes for one owner node. The server calls the owner-local
management connection/runtime/drain port, which reads the online registry and
gateway counters plus the always-on Channel runtime summary and can toggle only
that node's new-session admission; the
client maps stable RPC statuses back to manager usecase errors. The server-side
drain port is deliberately a local runtime primitive and does not re-run
durable cluster lifecycle checks that the origin manager usecase has already
performed. Summary and drain responses return aggregate counts only and do not
carry per-connection details. This package only transports the request and
response DTOs. It does not decide which manager HTTP request should target a
remote node, and it does not close gateway sessions. It also does not decide
scale-in safety or mark node lifecycle tombstones; those decisions stay in the
manager access and management usecase layers.

## Manager Log RPC

```text
remote manager log reader
  -> encode W K V L 1 request
  -> cluster RPCManagerLogs
  -> Adapter.HandleManagerLogRPC
  -> Management log reader port
  -> encode W K V l 1 response
```

Manager Log RPC transports Controller and Slot distributed log page requests to
the selected node. The server reads only node-local log storage through the
configured log reader; local/remote targeting is decided by the caller in
`internal/infra/cluster`. Responses preserve the manager usecase DTOs,
including decoded JSON-friendly payload summaries, newest-first entry order,
and `next_cursor` pagination state.

## Manager Controller Raft RPC

```text
remote manager controller-raft operator
  -> encode W K V R 1 request
  -> cluster RPCManagerControllerRaft
  -> Adapter.HandleManagerControllerRaftRPC
  -> Management Controller Raft operator port
  -> encode W K V r 1 response
```

Manager Controller Raft RPC transports node-local Controller Raft status reads
and explicit compaction attempts to the selected node. The server calls only
the configured management Controller Raft operator; it does not fan out across
Controller voters and does not decide which HTTP request should target a remote
node. Cluster-wide manual compaction is assembled above this package by the
management usecase.

## Manager Slot Raft RPC

```text
remote manager slot-raft operator
  -> encode W K V S 1 request
  -> cluster RPCManagerSlotRaft
  -> Adapter.HandleManagerSlotRaftRPC
  -> Management Slot Raft operator port
  -> encode W K V s 1 response
```

Manager Slot Raft RPC transports one explicit node-local Slot compaction
attempt to the selected node. The server calls only the configured management
Slot Raft operator; it does not fan out across Slot replicas, inspect log
payloads, or decide which HTTP request should target a remote node. Per-target
summary shaping is assembled above this package by the management usecase.

## Manager Message Retention RPC

```text
remote manager retention operator
  -> encode W K V T 1 request
  -> cluster RPCManagerMessageRetention
  -> Adapter.HandleManagerMessageRetentionRPC
  -> Management message retention operator port
  -> encode W K V t 1 response
```

Manager Message Retention RPC transports one explicit channel history
retention request to the channel leader node. The server calls only the
configured management retention operator; it revalidates local Channel
leadership, recomputes the safe boundary from fresh runtime metadata and
committed messages, and maps retryable not-leader, stale-route, and
route-not-ready statuses back to typed caller errors. Origin nodes do not send
metadata fences across this RPC boundary.

## Node Lifecycle RPC

```text
joining node startup loop
  -> encode W K V N 1 JoinNode request
  -> cluster RPCNodeLifecycle
  -> Adapter.HandleNodeLifecycleRPC
  -> validate cluster_id and join_token
  -> Management JoinNode usecase
  -> encode W K V n 1 management JoinNodeResponse
```

Node Lifecycle RPC transports pre-membership data-node join requests from a
joining node to an existing seed node. The client carries the joining node ID,
advertised cluster address, cluster ID, join token, and capacity weight. The
server validates the cluster ID and token at the RPC boundary before delegating
only the manager-facing join fields to the management usecase, which in turn
uses the cluster control lifecycle writer and its Controller-leader
forwarding. The same service carries the activation readiness probe DTO:
expected cluster ID, mirrored cluster ID/revision, reachability, transport,
control, runtime, unknown, and last-error fields. This package only preserves
that wire shape and maps stable statuses; the management usecase owns the
activation gate. The service also carries Controller voter promotion readiness
and target preparation requests. Those operations validate the configured
cluster ID at the RPC boundary, preserve the next Controller voter endpoints,
and return the target-side live proof fields (`observed_config_index` and
`observed_voters`) produced after preparation. Prepare conflicts and
Controller expected-revision mismatches map back to the management
Controller voter promotion blocked error; generic lifecycle conflict mapping is
not reused for these promotion safety failures.

## Manager Channel RPC

```text
remote manager channel reader
  -> encode W K V H 1 request
  -> cluster RPCManagerChannels
  -> Adapter.HandleManagerChannelRPC
  -> Management channel reader port
  -> encode W K V h 1 response
```

Manager Channel RPC transports read-only business channel list page requests to
the selected node. The server calls the local management channel port, which
scans this node's Slot metadata; the client maps stable RPC statuses back to
manager usecase errors. The RPC payload preserves node, filter, cursor,
`has_more`, and next-cursor state, but it does not implement channel mutations
or decide which HTTP request targets a remote node.

## Manager Node Config RPC

```text
remote manager node-config reader
  -> encode W K V C 1 request
  -> cluster RPCManagerNodeConfig
  -> Adapter.HandleManagerNodeConfigRPC
  -> Management node-config reader port
  -> encode W K V c 1 response
```

Manager Node Config RPC transports one read-only selected-node effective config
snapshot request to the selected node. The server calls only the configured
local management node-config reader port, which returns an allowlisted and
redacted snapshot owned by `internal/app`; the client maps stable RPC statuses
back to management usecase errors. The payload is internal strict JSON behind a
fixed magic prefix because the snapshot is a manager DTO rather than a hot
data-plane frame. This RPC does not read configuration files itself, expose raw
secrets, mutate runtime configuration, or decide which manager HTTP request
targets a remote node.

## Manager Plugin RPC

```text
remote manager plugin reader
  -> encode W K V J 1 request
  -> cluster RPCManagerPlugins
  -> Adapter.HandleManagerPluginRPC
  -> list/get/update_config/restart/uninstall: Management plugin reader port
  -> http_forward: local PluginHTTPRouter.Route(/plugin/route)
  -> encode W K V j 1 response
```

Manager Plugin RPC transports node-local plugin list/detail reads, lifecycle
mutations, and plugin HTTP route forwarding to the selected node. List/detail
and lifecycle operations call only the configured management plugin reader
port, which reads or mutates the target node's plugin lifecycle usecase.
`update_config` carries the raw desired-config JSON object bytes; `restart`
returns the latest plugin detail; `uninstall` returns only an accepted status.
`http_forward` calls the node-local `PluginHTTPRouter.Route` port directly; it
must not call `HTTPForward` again or the remote path can recurse. The client
maps stable RPC statuses back to manager/plugin usecase errors. The plugin
payload preserves plugin number, display metadata, config template bytes,
redacted desired config JSON, desired-state timestamps, hook methods,
PersistAfter/reply sync flags, process ID, last-seen timestamp, and latest
error text. This RPC does not decide which manager HTTP request targets a
remote node.

## Manager DB Inspect RPC

```text
remote manager DB inspect reader
  -> encode W K V B 1 request
  -> cluster RPCManagerDBInspect
  -> Adapter.HandleManagerDBInspectRPC
  -> Management DB inspect reader port
  -> encode W K V b 1 response
```

Manager DB Inspect RPC transports read-only DB Inspect table list, table
description, and query requests to the selected node. The server reads only the
target node's local inspect surface; empty `node_id` normalization and
local-vs-remote targeting are decided above this package by the management
usecase and infra/cluster adapter. The payload preserves JSON-friendly dynamic
rows and stats, but it does not merge rows across nodes, expose filesystem
paths, or mutate storage.

## Manager Task Audit RPC

```text
remote manager task audit reader
  -> encode W K V U 1 request
  -> cluster RPCManagerTaskAudit
  -> Adapter.HandleManagerTaskAuditRPC
  -> Management Controller task audit reader port
  -> encode W K V u 1 response
```

Manager Task Audit RPC transports retained Controller task history list and
single-task event timeline reads to the selected node. The server calls only
the configured local task audit reader; JSONL retention, default limits,
corrupt-line replay policy, and audit event construction stay in the
observability and management usecase layers. The codec uses strict JSON after
the stable magic prefix so unknown fields and trailing bytes fail closed. This
RPC does not inspect legacy `pkg/controller` state and does not mutate
Controller task state.

## Manager Diagnostics RPC

```text
remote manager diagnostics reader/operator
  -> encode W K V D Q request
  -> cluster RPCManagerDiagnostics
  -> Adapter.HandleManagerDiagnosticsRPC
  -> Management diagnostics reader/tracking port
  -> encode W K V D R response
```

Manager Diagnostics RPC transports internal diagnostics trace/message/event
queries and tracking-rule mutations to the selected node. The server calls
only the configured local diagnostics port; aggregate node selection,
alive/suspect/down filtering, and HTTP permission checks are decided above this
package by the management usecase and manager access layer. The payload uses
internal diagnostics DTOs and does not read legacy `internal` diagnostics
state. Event queries may select one exact physical `SlotID`. PreferredLeader
reconciliation events preserve their decision, actual and preferred leader,
Raft term, and Controller config epoch as explicit fields rather than reusing
generic peer or error fields. Event count is not a reconcile-rate signal:
node-local retention keeps state changes immediately and resamples an unchanged
signature at most once every 30 seconds, while Prometheus keeps aggregate rates.

## Manager Application Log RPC

```text
remote manager application log reader
  -> encode W K V G 1 request
  -> cluster RPCManagerAppLogs
  -> Adapter.HandleManagerAppLogRPC
  -> Management application log reader port
  -> encode W K V g 1 response
```

Manager Application Log RPC transports ordinary application log source and
entry page requests to the selected node. It is separate from Manager Log RPC
and does not read Controller, Slot, Channel, Raft, or other distributed logs.
The server calls only the configured management application log reader port; it
does not inspect filesystem paths, discover files, merge logs across nodes, or
decide which manager HTTP request should target a remote node.

## Codec Rules

Presence authority RPC uses fixed magic headers:

- Request: `W K V P 2`
- Response: `W K V R 2`

Presence authority request targets carry `HashSlot`, `SlotID`,
`LeaderNodeID`, Slot `LeaderTerm`, Slot `ConfigEpoch`, route revision, and the
diagnostic authority epoch in that order.

Delivery push RPC uses fixed magic headers:

- Request: `W K V D 1`
- Response: `W K V d 1`

Delivery fanout RPC uses fixed magic headers:

- Request: `W K V F 1`
- Response: `W K V f 1`

Conversation authority RPC uses fixed magic headers:

- Single-group request: `W K V C 1`
- Single-group response: `W K V c 1`
- Bulk-group request: `W K V C 2` (`WKVC2`)
- Bulk-group response: `W K V c 2` (`WKVc2`)

`WKVC2` carries an ordered collection of exact `RouteTarget` plus
`ActiveBatch` groups, bounded by 4,096 aggregate rows. `WKVc2` carries one
status for every input group in the same order. `WKVC1`/`WKVc1` retain their
original single-group layout for rolling-upgrade fallback.

Conversation authority request targets carry `HashSlot`, `SlotID`,
`LeaderNodeID`, Slot `LeaderTerm`, Slot `ConfigEpoch`, route revision, and the
diagnostic authority epoch in that order. The shared request fields then carry
`UID`, `metadb.ConversationKind`, active cursor, limit, legacy patch collection,
and drain result placeholders. Invalid or zero conversation kinds are rejected
by the decoder instead of being normalized.

Manager Controller Raft RPC uses fixed magic headers:

- Request: `W K V R 1`
- Response: `W K V r 1`

Conversation active-batch requests append the batch payload only for the
`admit_conversation_active_batch` op, after the shared request fields and legacy
patch collection. The stable batch field order is `Kind`, `SenderUID`,
`ChannelID`, `ChannelType`, `MessageSeq`, `ActiveAtMS`, then recipient entries
in `UID`, `IsSender` order. Active-view response rows use
`metadb.ConversationState` and encode row `Kind`; cursors use
`metadb.ConversationActiveCursor`.

Channel Append RPC uses fixed magic headers:

- Request: `W K V A 1`
- Response: `W K V a 1`

Manager Connection RPC uses fixed magic headers:

- Request: `W K V M 2`
- Response: `W K V m 2`

Manager Log RPC uses fixed magic headers:

- Request: `W K V L 1`
- Response: `W K V l 1`

Manager Plugin RPC uses fixed magic headers:

- Request: `W K V J 1`
- Response: `W K V j 1`

Manager DB Inspect RPC uses fixed magic headers:

- Request: `W K V B 1`
- Response: `W K V b 1`

Manager Diagnostics RPC uses fixed magic headers:

- Request: `W K V D Q 2`
- Response: `W K V D R 2`

The version 2 Manager diagnostics reader also accepts version 1 request and
response payloads. The shared diagnostics binary helper emits `W K D Q 3`
requests and `W K D R 4` responses while accepting request version 2 and
response versions 2-3. Older payloads decode the physical Slot query and
PreferredLeader reconciliation fields as zero values.

Manager Application Log RPC uses fixed magic headers:

- Request: `W K V G 1`
- Response: `W K V g 1`

Manager Node Config RPC uses fixed magic headers:

- Request: `W K V C 1`
- Response: `W K V c 1`

Manager Backup RPC uses fixed magic headers:

- Request: `W K B M Q 1`
- Response: `W K B M R 1`

Strings and collections are length-delimited with varints. Unsigned numeric
fields use uvarints and signed time/delay fields use varints. Decoders reject
unknown operations, malformed varints, oversized collections, truncated
payloads, and trailing bytes.
The codec is an internal node-to-node contract. Layout changes must bump the
magic version. Decoders accept only the explicitly documented older payloads,
while encoders always emit the latest version; this does not promise
bidirectional mixed-version rolling-upgrade compatibility.

Stable response statuses are:

- `ok`
- `not_leader`
- `stale_route`
- `route_not_ready`
- `context_canceled`
- `context_deadline_exceeded`
- `not_found`
- `invalid_argument`
- `unavailable`
- `rejected`

Conversation authority responses may additionally use:

- `cache_pressure`

Channel Append RPC statuses and item error codes preserve:

- `not_channel_authority`
- `backpressured`
- `append_result_missing`
- `channel_busy`

Delivery push and fanout responses currently use:

- `ok`
- `rejected`

## Boundaries

- This package may import `internal/usecase/presence` DTO aliases, runtime
  presence sentinel errors, `internal/usecase/conversation` DTOs and
  sentinel errors, `internal/contracts/channelappend` DTOs and sentinel errors,
  runtime delivery DTOs, `internal/runtime/conversationactive.ActiveBatch`
  as the active worker RPC DTO, internal diagnostics DTOs, and the cluster
  RPC service IDs.
- This package must not decide presence route conflict behavior.
- This package must not implement conversation active-row construction, cache
  merge, active-row flush, or handoff business logic.
- This package must not decide channel authority routing, create proxy channel
  state, perform non-authority appends, or run channel-write post-commit
  effects.
- This package must not mutate local gateway sessions or authority runtime
  state except through the `PresenceAuthority`, `PresenceOwner`, and
  `DeliveryOwnerPush` / `DeliveryFanoutRunner` / `ConversationAuthority` /
  standalone channel-write `ChannelAppend`, manager connection reader, and
  manager log reader, manager plugin reader, manager DB inspect reader,
  manager diagnostics reader/operator, and manager application log reader
  adapter interfaces.

## Backup RPC

Manager backup control uses the separate bounded `manager backup` RPC. Any
Manager node resolves the current Controller Leader and sends exactly one
status, page, trigger, cancel, hold, release, or verification-start request to
that node. The receiver rechecks that it is still Leader before entering the
usecase. Leadership transitions return a retryable unavailable error; writes
are never blindly replayed because their outcome may already be durable.
The versioned `WKBMQ1` request and `WKBMR1` response prefixes fence the
strict, bounded JSON control envelope; unversioned or unknown-version payloads
are rejected before operation dispatch.

Backup RPCs carry only bounded control requests, logical cut summaries, and
repository references. Message payload capture executes on the selected source
node and uploads directly to both repositories. Each message-shard response
also returns the exact record count and greatest message ID computed from the
same pinned snapshot, so the partition worker can publish cumulative signed
evidence without rescanning the uploaded stream. Restore target inspection,
partition installation, and verification are registered only for explicit
restore mode. Verification carries the expected canonical metadata SHA-256 on
the first batch and bounded Channel cut batches thereafter; the install report
also carries the recomputed metadata/message counts and greatest message ID for
fail-closed comparison with the signed restore-point evidence.

The message-shard request remains `WKVB1`; its evidence-bearing response is
`WKVb2`. Restore install uses `WKVI2/WKVi2` because the plan/report wire shape
contains record counts and the message-ID fence. The unchanged pairs are
`WKVP1/WKVp1` for partitions, `WKVR1/WKVr1` for target inspection, and
`WKVY1/WKVy1` for restore verification. Decoders reject unknown JSON fields,
trailing bytes, invalid digests, oversized batches, and older evidence-free
response/install versions.

The restore install request also carries the immutable permanent-erasure
ledger version, boundary, and SHA-256 from the Controller plan. Every node
rejects missing or malformed ledger evidence before entering the restore-only
installer. Restore verification batches carry each Channel's independently
verified physical-erasure prefix in addition to checkpoint/LEO boundaries.
