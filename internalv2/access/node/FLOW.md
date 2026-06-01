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

## Codec Rules

Presence authority RPC uses fixed magic headers:

- Request: `W K V P 2`
- Response: `W K V R 2`

Strings and collections are length-delimited with varints. Unsigned numeric
fields use uvarints and signed time/delay fields use varints. Decoders reject
unknown operations, malformed varints, truncated payloads, and trailing bytes.
The codec is an internalv2 node-to-node contract and does not provide
mixed-version rolling-upgrade compatibility yet; incompatible payload layout
changes must bump the magic version when that compatibility is required.

Stable response statuses are:

- `ok`
- `not_leader`
- `stale_route`
- `route_not_ready`
- `rejected`

## Boundaries

- This package may import `internalv2/usecase/presence` DTO aliases, runtime
  presence sentinel errors, and the clusterv2 RPC service IDs.
- This package must not decide presence route conflict behavior.
- This package must not mutate local gateway sessions or authority runtime
  state except through the `PresenceAuthority` and `PresenceOwner` interfaces.
