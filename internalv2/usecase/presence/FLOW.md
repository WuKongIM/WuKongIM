# internalv2/usecase/presence Flow

## Responsibility

`internalv2/usecase/presence` owns entry-agnostic connection presence
orchestration. It coordinates an owner-local registry with the current UID
authority client, but does not import gateway frames, wire protocol packages,
cluster runtimes, access adapters, or the app composition root.

## Activate Flow

```text
Activate(command)
  -> resolve UID hash slot
  -> build owner-local OwnerRoute and authority Route
  -> local.RegisterPending(LocalSession{Route, Session})
  -> authority.RegisterRoute(route)
     -> on error, local.MarkClosingAndUnregister(sessionID)
  -> route returned RouteAction values through OwnerActionClient
  -> authority.CommitRoute(token) when registration returned a pending token
  -> local.MarkActive(sessionID)
     -> on failure, authority.EnqueueUnregister(ctx, exact route identity, owner seq)
        and local.MarkClosingAndUnregister(sessionID)
  -> observe uid online status when an active owner-local session remains
```

`MarkActive` is the final local active re-check. A successful activation means
the authority accepted the route, required owner actions were acknowledged, and
the owner still has the session locally. Online-status observation is
best-effort and owner-local; it emits the legacy-compatible
`uid-deviceFlag-1-sessionID-deviceOnlineCount-totalOnlineCount` status only
after successful local activation when an active owner-local session exists.
The device and total counts are computed from active owner-local sessions after
activation, and pending sessions are not counted. Observation never adds
authority traffic.

## Deactivate Flow

```text
Deactivate(command)
  -> local.LocalSession(sessionID) to snapshot whether the removed session is active
  -> local.MarkClosingAndUnregister(sessionID)
  -> observe uid offline status when the removed session was active and no active
     owner-local sessions remain for the UID
  -> authority.EnqueueUnregister(ctx, exact route identity, owner seq)
```

Local removal happens before the authority tombstone is queued so owner-local
delivery no longer sees the route while unregister retry is pending. Offline
status observation is best-effort and emits the legacy-compatible offline status
only after the removed session was active and was the last active owner-local
session for that UID. The emitted value uses
`uid-deviceFlag-0-sessionID-deviceOnlineCount-totalOnlineCount`, with counts
computed after removal from active owner-local sessions. If the pre-removal
session snapshot is missing, the observer is skipped to avoid a false offline
event. Pending same-UID sessions do not count as online until activation
succeeds.

## Touch Flow

```text
Touch(command)
  -> local.MarkTouched(sessionID, activityUnix)
```

Touch is owner-local and only records observed client activity on the local
registry. It does not call the authority for each ping. Authority touch updates
are forwarded by the app touch worker in bounded batches from the local dirty
touch set, so frequent client activity does not become one RPC per ping.

## Query Flow

```text
EndpointsByUID(uid)
  -> authority.EndpointsByUID(uid)

EndpointsByUIDs(uids)
  -> authority.EndpointsByUIDs(uids) when the authority client exposes the
     optional batch surface
  -> otherwise loop through authority.EndpointsByUID(uid)
```

Lookup stays authority-routed behind the `AuthorityClient` port.

## Import Boundary

The usecase package must remain independent from concrete entries and cluster
adapters. The import-boundary test rejects imports of:

- `pkg/gateway`
- `pkg/protocol/frame`
- `pkg/clusterv2`
- `internalv2/access`
- `internalv2/app`
