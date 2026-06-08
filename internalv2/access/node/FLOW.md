# internalv2/access/node Flow

## Responsibility

`internalv2/access/node` owns node-to-node RPC adaptation for the internalv2
path. It decodes deterministic binary payloads, calls entry-agnostic authority
or owner ports, and encodes stable binary responses. It does not own presence
conflict policy, routing policy, retries, leases, or gateway session state.

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
- `ListUserConversationActiveViewForTarget(RouteTarget, uid, activeCursor, limit)`
- `DrainAuthority(RouteTarget)`

The RPC boundary is deliberately narrow:

- Admit carries already-projected UID active patches to the current authority
  target; projection policy stays in `internalv2/usecase/conversation` and the
  app committed sink.
- List reads the target-owned active view from the authority node. The local
  authority implementation decides how to merge unflushed cache rows with DB
  rows; this package only transports the request and response.
- Drain asks an authority node to flush and retire one exact `RouteTarget`
  during handoff. Handoff ordering and cache state transitions stay in the app
  authority runtime.
- The client chunks Admit patch collections at the codec collection limit before
  calling clusterv2 RPC. Raw transport errors are returned to the infra/cluster
  route adapter; this package does not decide whether they should retry.

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
- `cache_pressure`
- `rejected`

Delivery push and fanout responses currently use:

- `ok`
- `rejected`

## Boundaries

- This package may import `internalv2/usecase/presence` DTO aliases, runtime
  presence sentinel errors, `internalv2/usecase/conversation` DTOs and
  sentinel errors, runtime delivery DTOs, and the clusterv2 RPC service IDs.
- This package must not decide presence route conflict behavior.
- This package must not implement conversation projection, cache merge,
  projection-flush, or handoff business logic.
- This package must not mutate local gateway sessions or authority runtime
  state except through the `PresenceAuthority`, `PresenceOwner`, and
  `DeliveryOwnerPush` / `DeliveryFanoutRunner` / `ConversationAuthority`
  interfaces.
