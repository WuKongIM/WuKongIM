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
  -> map UID and sessionID into delivery.SessionClosedCommand when delivery is configured
  -> call delivery.SessionClosed even when presence deactivation fails

OnSessionActivateRollback(Context, err)
  -> map UID and sessionID into presence.DeactivateCommand
  -> call presence.Deactivate after a post-activation CONNACK write failure
```

## Send Flow

```text
OnFrame(SendPacket)
  -> map session and frame fields into message.SendCommand
  -> when sendtrace is enabled and the packet has a channel id/type, generate one trace id and attach diagnostics channel key
  -> stamp the configured owner node id for sender echo suppression
  -> request person-channel canonicalization when ChannelType is person
  -> call message.SendBatch with one item
  -> record gateway.messages_send when trace metadata exists
  -> map usecase result/error to frame.ReasonCode
  -> write SendackPacket
  -> record gateway.write_sendack after the write attempt when trace metadata exists

OnSendBatch([]SendBatchItem)
  -> compute one shared send deadline for the gateway micro-batch
  -> map valid packet items into message.SendBatchItem
     (including person-channel canonicalization requests and sendtrace metadata only when enabled)
  -> call message.SendBatch
  -> require item-aligned result count
  -> record gateway.messages_send once per valid item when trace metadata exists
  -> write one SendackPacket for every input item
  -> record gateway.write_sendack after each write attempt when trace metadata exists

OnFrame(PingPacket)
  -> best-effort touch presence activity for the gateway session
  -> write PongPacket on the same gateway session

OnFrame(RecvackPacket)
  -> require an authenticated UID and positive message id
  -> map session id, message id, and message seq into delivery.RecvackCommand
  -> call delivery.Recvack when delivery is configured
```

`OnSendBatch` is a synchronous adapter. Gateway core already owns the bounded
asynchronous SEND queue, so this package does not add another SEND queue or
fire-and-forget SEND worker.

Unauthenticated sends and nil message usecases are converted into sendacks
instead of raw protocol errors. Unsupported frames other than SEND, PING, and
RECVACK still return `ErrUnsupportedFrame`. Stale or malformed RECVACK frames
are treated as best-effort delivery feedback and ignored without protocol
noise.

Missing UID during session activation returns `ErrUnauthenticatedSession` to
gateway core; the adapter does not write CONNACK directly.

## Boundaries

- This package may import `pkg/gateway` and `pkg/protocol/frame`.
- This package must not import `pkg/clusterv2` or `pkg/channelv2`.
- Presence activation only maps gateway Context/session values into usecase
  commands. The captured session handle exposes close/write behavior plus
  local/remote addresses for owner-local manager connection projection.
  Authority, conflict, and route policy stay in the presence usecase.
- Delivery feedback only maps gateway Context/session values into delivery
  commands. Fanout, ack tracking, and local push policy stay outside gateway.
- Single-frame and batched SEND payloads are mapped as immutable send-path
  slices. The adapter does not clone payload bytes; durable append and async
  delivery boundaries take ownership copies when they cross into storage or
  worker queues.
