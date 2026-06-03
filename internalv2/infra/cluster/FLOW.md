# internalv2/infra/cluster Flow

## Responsibility

`internalv2/infra/cluster` adapts message usecase append ports to the
`pkg/clusterv2` public channel append API. It is the only phase-1 internalv2
package that maps message DTOs to `pkg/channelv2` DTOs.

## Append Flow

```text
message.AppendBatchRequest
  -> channelv2.AppendBatchRequest
  -> ChannelAppendNode.AppendChannelBatch
  -> channelv2.AppendBatchResult
  -> message.AppendBatchResult
```

Payloads are cloned in both directions unless the message usecase marks result
payloads as unnecessary for SENDACK-only flows. Commit mode and typed errors are
mapped at this boundary so the message usecase stays cluster-agnostic.

Bench runtime controls flow from internalv2 HTTP through `internalv2/infra/cluster`, `pkg/clusterv2.Node`, `pkg/clusterv2/channels.Service`, and finally the hosted ChannelV2 runtime. These routes are benchmark-only observation/cleanup controls and do not replace the gateway SEND activation path.

## Error Mapping

```text
channelv2.ErrNotLeader / clusterv2.ErrNotLeader      -> message.ErrNotLeader
channelv2.ErrStaleMeta                               -> message.ErrStaleRoute
channelv2.ErrChannelNotFound                         -> message.ErrChannelNotFound
channelv2.ErrBackpressured                           -> message.ErrBackpressured
clusterv2.ErrRouteNotReady / channelv2.ErrNotReady   -> message.ErrRouteNotReady
context cancellation/deadline                        -> unchanged
other errors                                         -> message.ErrAppendFailed wrapping source
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
adapter waits a short bounded backoff and resolves a fresh `RouteKey` a few
bounded times. Authority calls retry stale routing and not-leader the same way,
while authority-side route-not-ready is returned as its original bounded
presence error so pending token cleanup semantics stay explicit.

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
`runtime/delivery.Partitioner`. It reads the current snapshot hash-slot count,
routes each hash slot through `RouteHashSlot`, and merges contiguous hash-slot
ranges with the same leader into delivery partitions.

```text
clusterv2 Snapshot.HashSlotCount
  -> RouteHashSlot(hashSlot)
  -> contiguous ranges grouped by Route.Leader
  -> runtime/delivery.Partition{LeaderNodeID, HashSlotStart, HashSlotEnd}
```

Route-table-not-ready, no-leader, and route lookup failures map to
`runtime/delivery.ErrRouteNotReady`, so the async delivery sink can record the
failure without adding cluster-specific errors to the runtime package.
