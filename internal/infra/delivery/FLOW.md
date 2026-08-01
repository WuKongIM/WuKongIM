# internal/infra/delivery Flow

## Responsibility

`internal/infra/delivery` adapts the entry-independent delivery runtime to the
owner node's concrete online registry, gateway session, and WuKong protocol
packet. Production composition uses `LocalSessionWriter`, which owns final
exact-route revalidation, packet construction, session writes, and
terminal-versus-retryable write classification. The canonical Online Delivery
runtime owns pending RECVACK reservation and retry policy around that narrow
port. This package also owns the narrow
presence-usecase adapters used by both channelappend and canonical Online
Delivery recipient routing, so the app composition root only constructs and
injects the runtime ports.

The package does not resolve cluster ownership, page channel subscribers, or
choose retry policy. Those decisions remain in `internal/runtime/delivery` and
the cluster adapters composed by `internal/app`.

## Canonical Owner-local Session Write Flow

```text
runtime/delivery.PushOwner
  -> reserve item-aligned pending RECVACK state inside runtime/delivery
  -> LocalSessionWriter validates the exact active UID/session/owner identity
  -> build the recipient-specific frame.RecvPacket
  -> write through the owner-local gateway SessionHandle
     -> success: runtime finishes that reservation and reports accepted
     -> stale route/build/closed/overflow failure: runtime rolls back and reports dropped
     -> transient write failure: runtime rolls back and reports retryable
```

## Legacy Compatibility Owner-local Push Flow

The following `LocalOwnerPusher` path remains compiled for compatibility tests
and cleanup only. Production app composition does not construct it.

The multi-route path validates routes before reserving an item-aligned ACK
batch, then revalidates each route after its final reservation. This keeps one
batch lock path for unique recipients while preventing a stale session from
receiving a write after reservation.

```text
runtime/delivery.PushCommand with multiple routes
  -> validate the exact active UID/session/owner identity in runtime/online
  -> reserve item-aligned pending RECVACK tokens through delivery.Manager
  -> refresh only later duplicate UID/session/message reservations
  -> revalidate each exact session after its final reservation or refresh
  -> build the recipient-specific frame.RecvPacket
  -> write through the owner-local gateway SessionHandle
     -> success: finish that reservation and report accepted
     -> stale route/build failure: roll back and report dropped
     -> transient write failure: roll back and report retryable
     -> closed/overflow write failure: roll back and report dropped
  -> finish the batch ACK accounting with accepted indexes and rollbacks
```

The common single-route path avoids the batch bookkeeping. It builds the
recipient packet first, binds or refreshes the pending ACK, then performs the
one exact-session lookup immediately before the write.

```text
runtime/delivery.PushCommand with one route
  -> clone payload and build the recipient-specific frame.RecvPacket
  -> bind or refresh one pending RECVACK reservation
  -> resolve and validate the exact active UID/session/owner identity
  -> write through the owner-local gateway SessionHandle
     -> success: finish that reservation and report accepted
     -> stale route: roll back and report dropped
     -> transient write failure: roll back and report retryable
     -> closed/overflow write failure: roll back and report dropped
```

Duplicate recipient rows intentionally keep duplicate writes. Later duplicate
rows refresh their shared UID/session/message reservation immediately before
the write so a fast earlier RECVACK cannot consume the reservation for the next
attempt. The common single-route path stays allocation-light; token-fenced
rollback preserves a previous committed reservation when a refresh attempt or
its write fails.

`LocalOwnerPusher.SetAckManager` exists only to close the construction cycle
between the pusher, fanout worker, retry scheduler, and delivery manager.
Compatibility constructors and tests must call it exactly once before any
concurrent `Push` call; production app composition has no such cycle.

The convergence path also provides `LocalSessionWriter`, which owns only final
exact-session validation, packet construction, and physical writes. The new
Online Delivery runtime retains pending-ACK ownership around that narrow port.
App composition now constructs `LocalSessionWriter`; `LocalOwnerPusher` remains
compiled only for compatibility tests and a later cleanup slice. A
missing owner-local registry is an unavailable adapter, not a stale route, so
the writer returns a retryable result instead of terminally dropping the route.

In the compatibility pusher, stale pending-ACK expiry is activity-driven and
globally throttled; ordinary pushes do not scan the tracker on every call. The
canonical runtime owns the equivalent throttled expiry policy directly.

## Presence Adapters

`ChannelAppendPresenceResolver` converts the entry-agnostic presence usecase's
flat and exact-target lookup results into channelappend delivery DTOs. Exact
target group cardinality, result order, partial errors, and all physical
hash-slot/logical Slot Raft Group fencing fields are preserved.

`PresenceResolver` converts the same exact-target lookup into canonical Online
Delivery results. It preserves one result per target in input order, copies all
authority fencing fields and route metadata, and reports a missing presence
dependency as an aligned availability error for every target instead of
misclassifying recipients as offline.
