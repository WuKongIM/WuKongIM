# internal/infra/delivery Flow

## Responsibility

`internal/infra/delivery` contains the two concrete adapters used by the deep
Online Delivery runtime:

- `PresenceResolver` adapts exact-target presence lookups to aligned
  `runtime/delivery.TargetPresenceResult` values.
- `LocalSessionWriter` validates one exact owner-local session, builds its
  WKProto `RecvPacket`, writes it, and returns Accepted, Retryable, or Dropped.

The package does not own subscriber discovery, authority grouping, queues,
retry policy, owner grouping, or pending RECVACK state. In particular, no ACK
token crosses into the session adapter; reservation, finish, rollback, feedback,
expiry, and reset all remain in `internal/runtime/delivery`.

## Presence Flow

```text
Runtime RecipientTargetBatch groups
  -> PresenceResolver.EndpointsByTargets
  -> presence usecase exact-target lookup
  -> item-aligned TargetPresenceResult groups
```

Group count, order, partial errors, routes, and every physical hash-slot/logical
Slot authority fence are preserved. The adapter does not retry or weaken a
stale target.

## Local Session Write Flow

```text
Runtime LocalSessionWrite
  -> validate UID, owner node/boot/sequence, and session ID
  -> resolve the exact active runtime/online session handle
  -> build one recipient-specific frame.RecvPacket
  -> write through the gateway SessionHandle
     -> success: Accepted
     -> transient write failure: Retryable
     -> stale route, invalid packet, closed session, or overflow: Dropped
```

Payload cloning occurs only where packet/session ownership requires it. The
adapter revalidates the exact live session on every call, including duplicate
route writes after the runtime rebinds their ACK identity.
