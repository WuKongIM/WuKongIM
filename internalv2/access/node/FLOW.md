# internalv2/access/node Flow

## Responsibility

`internalv2/access/node` owns node-to-node RPC adaptation for the internalv2
path. It decodes deterministic binary payloads, calls entry-agnostic authority,
owner, delivery, or channel-write ports, and encodes stable binary responses.
It does not own presence conflict policy, channel routing policy, retries,
leases, or gateway session state.

## Presence Authority RPC

```text
remote authority client
  -> encode W K V P 2 request
  -> clusterv2 RPCPresenceAuthority
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
- `TouchRoutes(RouteTarget, []Route)`

## Presence Owner RPC

```text
remote owner-action client
  -> encode W K V P 2 request
  -> clusterv2 RPCPresenceOwner
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
  -> clusterv2 RPCDeliveryPush
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
  -> clusterv2 RPCDeliveryFanout
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
  -> encode W K V C 1 request
  -> clusterv2 RPCConversationAuthority
  -> Adapter.HandleConversationAuthorityRPC
  -> ConversationAuthority port
  -> encode W K V c 1 response
```

Supported conversation authority calls:

- `AdmitPatches(RouteTarget, []ActivePatch)`
- `AdmitActiveBatch(RouteTarget, conversationactive.ActiveBatch)`
- `ListUserConversationActiveViewForTarget(RouteTarget, uid, activeCursor, limit)`
- `DrainAuthority(RouteTarget)`

The RPC boundary is deliberately narrow:

- Admit carries already-materialized UID active patches to the current
  authority target for compatibility and handoff paths. Callers own patch
  construction; this package only transports the exact patch collection it
  receives.
- Active-batch admit carries the channelwrite output directly to one routed UID
  authority target. Sender/recipient route grouping is performed by
  `internalv2/infra/cluster`; this package only transports the exact batch
  subset it receives.
- List reads the target-owned active view from the authority node. The local
  authority implementation decides how to merge unflushed cache rows with DB
  rows; this package only transports the request and response.
- Drain asks an authority node to flush and retire one exact `RouteTarget`
  during handoff. Handoff ordering and cache state transitions stay in the app
  authority runtime.
- The client chunks Admit patch collections at the codec collection limit before
  calling clusterv2 RPC. Raw transport errors are returned to the infra/cluster
  route adapter; this package does not decide whether they should retry.
- The client also chunks active-batch recipient collections at the same codec
  collection limit. It preserves the batch sender field exactly as supplied by
  the routed caller.

## Channel Write RPC

```text
remote channel write forwarder
  -> encode W K V W 2 request
  -> clusterv2 RPCChannelWrite
  -> ChannelWriteAdapter.HandleChannelWriteRPC
  -> ChannelWrite.SubmitForAuthority
  -> encode W K V w 1 response
```

Channel write RPC transports one exact `channelwrite.AuthorityTarget` plus
item-aligned `channelwrite.SendCommand` values to the target channel authority
node. The target includes recipient fanout metadata (`Large` and
`SubscriberMutationVersion`) so the authority reactor can choose paged
large-channel fanout or cached non-large subscriber snapshots without resolving
metadata again. The server only submits to the local channel authority port; it
does not resolve routes, create proxy channel state, append directly outside
the authority reactor, or run post-commit side effects outside that reactor.
The client skips canceled or expired items before transport, normalizes
transport canceled/timeout errors to standard context errors, and preserves
active item order in returned item-aligned results.

## Codec Rules

Presence authority RPC uses fixed magic headers:

- Request: `W K V P 2`
- Response: `W K V R 2`

Delivery push RPC uses fixed magic headers:

- Request: `W K V D 1`
- Response: `W K V d 1`

Delivery fanout RPC uses fixed magic headers:

- Request: `W K V F 1`
- Response: `W K V f 1`

Conversation authority RPC uses fixed magic headers:

- Request: `W K V C 1`
- Response: `W K V c 1`

Conversation active-batch requests append the batch payload only for the
`admit_conversation_active_batch` op, after the shared request fields and legacy
patch collection. The stable batch field order is `SenderUID`, `ChannelID`,
`ChannelType`, `MessageSeq`, `ActiveAtMS`, then recipient entries in `UID`,
`IsSender` order.

Channel write RPC uses fixed magic headers:

- Request: `W K V W 2`
- Response: `W K V w 1`

Strings and collections are length-delimited with varints. Unsigned numeric
fields use uvarints and signed time/delay fields use varints. Decoders reject
unknown operations, malformed varints, oversized collections, truncated
payloads, and trailing bytes.
The codec is an internalv2 node-to-node contract and does not provide
mixed-version rolling-upgrade compatibility yet; incompatible payload layout
changes must bump the magic version when that compatibility is required.

Stable response statuses are:

- `ok`
- `not_leader`
- `stale_route`
- `route_not_ready`
- `context_canceled`
- `context_deadline_exceeded`
- `rejected`

Conversation authority responses may additionally use:

- `cache_pressure`

Channel write RPC statuses and item error codes preserve:

- `not_channel_authority`
- `backpressured`
- `append_result_missing`
- `channel_busy`

Delivery push and fanout responses currently use:

- `ok`
- `rejected`

## Boundaries

- This package may import `internalv2/usecase/presence` DTO aliases, runtime
  presence sentinel errors, `internalv2/usecase/conversation` DTOs and
  sentinel errors, `internalv2/contracts/channelwrite` DTOs and sentinel errors,
  runtime delivery DTOs, `internalv2/runtime/conversationactive.ActiveBatch`
  as the active worker RPC DTO, and the clusterv2 RPC service IDs.
- This package must not decide presence route conflict behavior.
- This package must not implement conversation active-row construction, cache
  merge, active-row flush, or handoff business logic.
- This package must not decide channel authority routing, create proxy channel
  state, perform non-authority appends, or run channel-write post-commit
  effects.
- This package must not mutate local gateway sessions or authority runtime
  state except through the `PresenceAuthority`, `PresenceOwner`, and
  `DeliveryOwnerPush` / `DeliveryFanoutRunner` / `ConversationAuthority` /
  standalone channel-write `ChannelWrite` adapter interface.
