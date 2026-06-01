# internalv2/access/gateway Flow

## Responsibility

`internalv2/access/gateway` adapts `pkg/gateway` events to entry-agnostic
message and presence usecases. It does not own durable send business rules or
presence authority policy.

## Presence Flow

```text
OnSessionActivate(Context)
  -> read authenticated UID, device, listener, and session fields from gateway Context/Session values
  -> map them into presence.ActivateCommand
  -> call presence.Activate
  -> classify known presence activation errors for gateway auth metrics
  -> return activation errors to gateway core so core writes system-error CONNACK and closes

OnSessionClose(Context)
  -> map UID and sessionID into presence.DeactivateCommand
  -> call presence.Deactivate

OnSessionActivateRollback(Context, err)
  -> map UID and sessionID into presence.DeactivateCommand
  -> call presence.Deactivate after a post-activation CONNACK write failure
```

## Send Flow

```text
OnFrame(SendPacket)
  -> map session and frame fields into message.SendCommand
  -> call message.Send
  -> map usecase result/error to frame.ReasonCode
  -> write SendackPacket

OnSendBatch([]SendBatchItem)
  -> compute one shared send deadline for the gateway micro-batch
  -> map valid packet items into message.SendBatchItem
  -> call message.SendBatch when available
  -> require item-aligned result count
  -> write one SendackPacket for every input item

OnFrame(PingPacket)
  -> best-effort touch presence activity for the gateway session
  -> write PongPacket on the same gateway session
```

Unauthenticated sends and nil message usecases are converted into sendacks
instead of raw protocol errors. Unsupported frames other than SEND and PING
still return `ErrUnsupportedFrame`.

Missing UID during session activation returns `ErrUnauthenticatedSession` to
gateway core; the adapter does not write CONNACK directly.

## Boundaries

- This package may import `pkg/gateway` and `pkg/protocol/frame`.
- This package must not import `pkg/clusterv2` or `pkg/channelv2`.
- Presence activation only maps gateway Context/session values into usecase
  commands. Authority, conflict, and route policy stay in the presence usecase.
- Single-frame SEND payloads are cloned while mapping so later frame reuse
  cannot mutate usecase commands. Batched SEND keeps the async-dispatch-owned
  frame payload until `internalv2/usecase/message` clones at the append boundary.
