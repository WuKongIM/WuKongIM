# internal/infra/delivery Flow

## Responsibility

`internal/infra/delivery` adapts canonical Online Delivery ports to the
entry-agnostic presence usecase, owner-local online registry, gateway session,
and WuKong protocol packet. It contains adapters only; retry, pending RECVACK,
owner grouping, and plan orchestration remain in `internal/runtime/delivery`.

The retired `LocalOwnerPusher` compatibility stack has been removed.
`LocalSessionWriter` is the single owner-local physical-write adapter used by
production composition.

## Owner-local Session Write Flow

```text
runtime/delivery.PushOwner
  -> reserve item-aligned pending RECVACK state in runtime/delivery
  -> LocalSessionWriter validates exact active UID/session/owner identity
  -> build the recipient-specific frame.RecvPacket
  -> write through the owner-local gateway SessionHandle
     -> success: runtime finishes that reservation and reports accepted
     -> transient write failure: runtime rolls back and reports retryable
     -> stale route/build/closed/overflow failure: runtime rolls back and reports dropped
```

The writer never receives or mutates ACK tokens. A missing online registry is
adapter unavailability and therefore retryable, not evidence that the route is
terminally stale. Session lookup validates the full owner fence immediately
before the physical write.

## Presence Adapters

`ChannelAppendPresenceResolver` converts the entry-agnostic presence usecase's
flat and exact-target lookup results into channelappend delivery DTOs. Exact
target cardinality, result order, partial errors, and physical hash-slot plus
logical Slot Raft Group fences are preserved.

`PresenceResolver` converts the same exact-target lookup into canonical Online
Delivery results. It preserves one result per input target, copies all fencing
and route metadata, and reports a missing presence dependency as an aligned
availability error for every target instead of classifying recipients as
offline.
