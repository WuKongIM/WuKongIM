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
  -> observe uid online status when an owner-local session remains
```

`MarkActive` is the final local active re-check. A successful activation means
the authority accepted the route, required owner actions were acknowledged, and
the owner still has the session locally. Online-status observation is
best-effort and owner-local; it emits the legacy-compatible `uid-1` status only
after successful local activation and never adds authority traffic.

## Deactivate Flow

```text
Deactivate(command)
  -> local.MarkClosingAndUnregister(sessionID)
  -> observe uid offline status when no owner-local sessions remain for the UID
  -> authority.EnqueueUnregister(ctx, exact route identity, owner seq)
```

Local removal happens before the authority tombstone is queued so owner-local
delivery no longer sees the route while unregister retry is pending. Offline
status observation is best-effort and emits the legacy-compatible `uid-0` status
only after the removed session was the last owner-local session for that UID.

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
