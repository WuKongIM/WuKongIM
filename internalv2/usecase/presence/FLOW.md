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
  -> build owner-local OnlineConn and authority Route
  -> local.RegisterPending(conn)
  -> authority.RegisterRoute(route)
     -> on error, local.MarkClosingAndUnregister(sessionID)
  -> route returned RouteAction values through OwnerActionClient
  -> authority.CommitRoute(token) when registration returned a pending token
  -> local.MarkActive(sessionID)
     -> on failure, authority.EnqueueUnregister(ctx, exact route identity, owner seq)
        and local.MarkClosingAndUnregister(sessionID)
```

`MarkActive` is the final local active re-check. A successful activation means
the authority accepted the route, required owner actions were acknowledged, and
the owner still has the session locally.

## Deactivate Flow

```text
Deactivate(command)
  -> local.MarkClosingAndUnregister(sessionID)
  -> authority.EnqueueUnregister(ctx, exact route identity, owner seq)
```

Local removal happens before the authority tombstone is queued so owner-local
delivery no longer sees the route while unregister retry is pending.

## Query Flow

```text
EndpointsByUID(uid)
  -> authority.EndpointsByUID(uid)
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
