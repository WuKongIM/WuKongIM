# internalv2/infra/cluster Flow

## Responsibility

`internalv2/infra/cluster` adapts internalv2 usecase/runtime ports to
`pkg/clusterv2` and `pkg/channelv2`. It maps channelwrite append DTOs to
`pkg/channelv2` DTOs, resolves channel append authority through clusterv2,
persists channelwrite post-commit cursors in app-local channel metadata, adapts
legacy-compatible channel metadata usecase calls to clusterv2 Slot metadata
facades, adapts legacy-compatible user metadata calls to UID Slot metadata
facades, adapts conversation list reads to UID-owned active rows plus
channel-owned committed message logs, and adapts presence/delivery ports to
clusterv2 routing and node RPC.

## Append Flow

```text
channelwrite.AppendBatchRequest
  -> channelv2.AppendBatchRequest
     (expected channel/leader epochs fence stale authority writes)
     (trace id, diagnostics channel key, append attempt, and per-message trace metadata stay transient)
  -> ChannelAppendNode.AppendChannelBatch
  -> record sendtrace `channel.append.local` for traced messages after completion
  -> channelv2.AppendBatchResult
  -> channelwrite.AppendBatchResult
```

Payloads are cloned in both directions unless the channelwrite runtime marks result
payloads as unnecessary for SENDACK-only flows. Commit mode, expected authority
epoch fences, and typed errors are mapped at this boundary so the channelwrite
runtime stays cluster-agnostic.
The adapter records channel append sendtrace events only when tracing is enabled
and the request carries trace metadata, so untraced appends do not pay extra
timing or event-allocation cost.

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

Bench runtime controls flow from internalv2 HTTP through `internalv2/infra/cluster`, `pkg/clusterv2.Node`, `pkg/clusterv2/channels.Service`, and finally the hosted ChannelV2 runtime. These routes are benchmark-only observation/cleanup controls and do not replace the gateway SEND activation path.

## Channel Metadata Flow

`ChannelMetadataStore` adapts `internalv2/usecase/channel.Store` to the
clusterv2 Slot metadata facade. When the cluster node also exposes
`ChannelMembershipNode`, the same adapter implements the channel usecase
`MembershipIndex` port for UID-owned reverse membership projection.

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

## Conversation Read Flow

`ConversationStore` adapts `internalv2/usecase/conversation` active-row and
last-message read ports to clusterv2 facades. Conversation rows are UID-owned
metadata records, while last-message display data is read from each
channel-owned message log for the current page only. When configured,
last-message reads run with a bounded worker count; missing tails are skipped
per row while routing/readiness errors fail the request.

```text
conversation list usecase
  -> ListUserConversationActiveView(uid, active cursor)
       -> wraps ListUserConversationActivePage(uid, active cursor)
       -> UID-owned conversation rows routed by UID hash slot
  -> GetLastVisibleMessages(current page keys)
       -> ReadChannelLastVisible(channel, visible_after_seq)
       -> channel-owned route resolves the ChannelV2 leader
       -> missing channel or no visible message returns no last message for that row
```

The adapter clones row slices and message payloads across the boundary. It does
not own ordering, cursor, unread, or sparse-active rules; those stay in the
conversation usecase so access adapters can share the same list semantics.
`metadb.ErrNotFound` and `channelv2.ErrChannelNotFound` during a single
last-message read mean that row has no display message, not that the whole list
failed. Routing, readiness, and other read errors still fail the request.

`ConversationAuthorityClient` routes UID-owned active cache calls to the
current authority leader and leaves cache/list business rules inside the local
authority implementation. Admission resolves each patch UID with
`RouteKey(uid)`, groups patches by exact `RouteTarget`, and sends each group to
the local authority when the target leader is this node or through
access/node Conversation Authority RPC when the leader is remote. The remote
RPC client chunks large patch groups at the codec collection limit before
sending them. List resolves the requested UID once per retry attempt and reads
the active view from that authority target; the active-view response is not
satisfied by a local DB-only fallback when the UID authority is remote. Drain
uses the caller-supplied exact target for authority handoff.

```text
ConversationAuthorityClient
  -> AdmitPatches([]ActivePatch)
       -> RouteKey(patch.UID) for each patch
       -> group by RouteTarget
       -> local conversation authority for local groups
       -> access/node Conversation Authority Admit RPC for remote groups
  -> ListUserConversationActiveView(uid)
       -> RouteKey(uid)
       -> local conversation authority active view when local
       -> access/node Conversation Authority List RPC when remote
  -> DrainAuthority(target)
       -> local drain when target leader is this node
       -> access/node Conversation Authority Drain RPC when remote
```

Route-not-ready, stale-route, and not-leader results are retried with a short
bounded backoff so authority movement can settle without changing conversation
usecase semantics. Raw clusterv2 route errors returned by remote RPC calls are
mapped to the same conversation route sentinels before the retry decision.

## Channel Write Authority Flow

`ChannelWriteClient` adapts the channelwrite router authority ports to
clusterv2. It resolves canonical channel append authority through the narrow
`Node.ResolveChannelAppendAuthority` facade, which delegates to the hosted
ChannelV2 service so metadata creation policy remains in `pkg/clusterv2/channels`.
The adapter maps `channelv2.Meta` to `channelwrite.AuthorityTarget` with the
canonical `ChannelID`, `ChannelKey`, `LeaderNodeID`, `Epoch`, and
`LeaderEpoch`.

```text
channelwrite.Router
  -> ChannelWriteClient.ResolveAppendAuthority(canonical channel)
       -> clusterv2.Node.ResolveChannelAppendAuthority
       -> channels.Service.ResolveAppendAuthority
       -> ChannelMetaEnsurer.EnsureChannelMeta when append would create metadata
  -> local authority: channelwrite.Group.SubmitLocal
  -> remote authority: ChannelWriteClient.ForwardSendBatch
       -> injected ChannelWriteRemoteForwarder
```

Route errors are translated at this adapter boundary:
`channelv2.ErrNotLeader` becomes `channelwrite.ErrNotChannelAuthority`,
`channelv2.ErrStaleMeta` becomes `channelwrite.ErrStaleRoute`, and
`channelv2.ErrNotReady` plus clusterv2 readiness errors become
`channelwrite.ErrRouteNotReady`. Remote forwarding is supplied by the
`internalv2/access/node` Channel Write RPC client; remote item results are
returned item-aligned without interpreting successful payloads.

## Channel Write Cursor Flow

`ChannelWriteCursorStore` persists post-commit progress through app-local
channel metadata, not the channel message log. The adapter owns only a narrow
key/value channel metadata surface so the channelwrite runtime can load the
last completed sequence and store monotonic cursor advances after recipient
dispatch is accepted.

```text
channelwrite commit effect
  -> ChannelWriteCursorStore.StorePostCommitCursor(channel, message_seq)
       -> LoadChannelAppMetadata(channel, internalv2.channelwrite.post_commit_cursor)
       -> skip stale/non-advancing writes
       -> StoreChannelAppMetadata(channel, internalv2.channelwrite.post_commit_cursor)
```

`ChannelWriteCommittedReader` adapts `ReadChannelCommitted` to the
channelwrite replay port by reading forward from `lastCompletedSeq + 1` with a
bounded limit and returning `CommittedMessage` DTOs. This keeps replay ordering
and pagination in the runtime while preserving clusterv2/channelv2 as an
infrastructure detail.

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
channelv2.ErrNotLeader / clusterv2.ErrNotLeader      -> channelwrite.ErrNotLeader
channelv2.ErrStaleMeta / channelv2.ErrNotReplica     -> channelwrite.ErrStaleRoute
channelv2.ErrChannelNotFound                         -> channelwrite.ErrChannelNotFound
channelv2.ErrBackpressured                           -> channelwrite.ErrBackpressured
clusterv2.ErrRouteNotReady / clusterv2.ErrNoSlotLeader / channelv2.ErrNotReady -> channelwrite.ErrRouteNotReady
context cancellation/deadline                        -> unchanged
other errors                                         -> channelwrite.ErrAppendFailed wrapping source
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
`RegisterRoute`. Touch batching uses `TouchRoutesTo(target, routes)` because
the app worker groups dirty owner sessions by the exact authority target
observed during flush. The adapter sends the batch locally when the target
leader is this node and uses access/node RPC for remote leaders.

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
