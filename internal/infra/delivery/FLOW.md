# internal/infra/delivery Flow

## Responsibility

`internal/infra/delivery` adapts the entry-independent delivery runtime to the
owner node's concrete online registry, gateway session, and WuKong protocol
packet. It owns the owner-local push state machine: exact route revalidation,
pending RECVACK reservation lifecycle, packet construction, session writes,
and terminal-versus-retryable write classification. It also owns the narrow
presence-usecase to channelappend route adapter used by recipient delivery, so
the app composition root only constructs and injects the runtime port.

The package does not resolve cluster ownership, page channel subscribers, or
choose retry policy. Those decisions remain in `internal/runtime/delivery` and
the cluster adapters composed by `internal/app`.

## Owner-local Push Flow

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
between the pusher, fanout worker, retry scheduler, and delivery manager. App
composition must call it exactly once before any concurrent `Push` call.

Stale pending-ACK expiry is activity-driven and globally throttled per pusher;
ordinary pushes do not scan the tracker on every call.

## Channelappend Presence Adapter

`ChannelAppendPresenceResolver` converts the entry-agnostic presence usecase's
flat and exact-target lookup results into channelappend delivery DTOs. Exact
target group cardinality, result order, partial errors, and all physical
hash-slot/logical Slot Raft Group fencing fields are preserved.
