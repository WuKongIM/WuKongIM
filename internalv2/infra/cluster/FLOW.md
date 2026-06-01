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
to `pkg/clusterv2` UID routing and `internalv2/access/node` RPC. The adapter
does not own gateway activation policy or authority conflict rules.

```text
presence.Route / uid
  -> clusterv2.RouteKey(uid)
  -> presence.RouteTarget
  -> local accessnode.PresenceAuthority when target leader is this node
  -> access/node PresenceAuthority RPC client when target leader is remote
```

`RegisterRoute`, `UnregisterRoute`, `EndpointsByUID`, and `RehydrateRoutes`
resolve their target from the UID carried by the request. `CommitRoute` and
`AbortRoute` resolve their target from the UID remembered for the pending token
returned by `RegisterRoute`.

If an authority call reports stale routing or not-leader, the adapter resolves a
fresh `RouteKey` and retries once. If route resolution is not ready, the adapter
returns `internalv2/runtime/presence.ErrRouteNotReady` so callers can treat the
operation as retryable without importing clusterv2 errors.
