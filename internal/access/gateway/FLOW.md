# internal/access/gateway Flow

## Responsibility

`internal/access/gateway` adapts `pkg/gateway` events to entry-agnostic
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
  -> map session uid, device id/flag, and frame fields into message.SendCommand
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
     (including session device id/flag, person-channel canonicalization requests, and sendtrace metadata only when enabled)
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

OnFrame(terminal EventPacket)
  -> reject immediately when the current session already sealed its outbound path
  -> strictly parse the reserved request EVENT
  -> project only epoch + SHA-256 capability proof into benchterminal
  -> explicitly copy the fixed nonce into a redacting SessionFence
  -> call Controller.SealAndEnqueue with a per-call sealer bound to this exact session
  -> under the session write lock, seal ordinary outbound admission and enqueue the exact ACK
  -> reject every later inbound frame before SEND, presence, or delivery usecases
```

`OnSendBatch` is a synchronous adapter. Gateway core already owns the bounded
asynchronous SEND queue, so this package does not add another SEND queue or
fire-and-forget SEND worker.

Unauthenticated sends and nil message usecases are converted into sendacks
instead of raw protocol errors. Unsupported frames other than SEND, PING,
RECVACK, and the strictly reserved terminal EVENT still return
`ErrUnsupportedFrame`. Stale or malformed RECVACK frames
are treated as best-effort delivery feedback and ignored without protocol
noise.

Known route-authority errors (`ErrRouteNotReady`, stale route, not leader) and
send deadline expiry are written as `ReasonNodeNotMatch` so WKProto clients can
retry through a fresher route during channel runtime migration or failover windows.
The sendack observer still records deadline expiry with error class `timeout`
to keep route-wait and timeout diagnostics separate from durable append
successes. When the message usecase attaches bounded batch-stage timing to a
deadline error, the existing `internal.access.gateway.send_failed` warning also
records `permissionDuration`, `preAppendDuration`, `submitterDuration`, and
`deadlineBudgetBeforeSubmit`. This adds no second failure record and keeps the
original error classification intact. Canceled sends before the app-owned
planned-shutdown fence retain the existing warning, low-cardinality sendack
observation, and optional trace so unexpected runtime cancellation remains
diagnosable. After `App.Stop` crosses that explicit fence, canceled sends remain
visible through the observer and trace but do not emit one identity-bearing
warning per draining in-flight item.

Missing UID during session activation returns `ErrUnauthenticatedSession` to
gateway core; the adapter does not write CONNACK directly.

## Boundaries

- This package may import `internal/usecase/benchterminal`, `pkg/gateway`, and
  `pkg/protocol/frame`; terminal epoch state, proof validation, exact-count
  uniqueness, exact-count checks, and permanent failure policy remain in the
  usecase.
- This package must not import `pkg/cluster` or `pkg/channel`.
- Presence activation only maps gateway Context/session values into usecase
  commands. The captured session handle exposes close/write behavior plus
  local/remote addresses for owner-local manager connection projection.
  Authority, conflict, and route policy stay in the presence usecase.
- Delivery feedback only maps gateway Context/session values into delivery
  commands. Fanout, ack tracking, and local push policy stay outside gateway.
- The terminal EVENT adapter never exposes or logs raw capability, nonce,
  digest, request payload, or ACK payload. It maps the validated protocol
  values into fixed-size redacting usecase values and verifies the usecase
  SessionID against the currently authenticated session before sealing.
- Single-frame and batched SEND payloads are mapped as immutable send-path
  slices. The adapter does not clone payload bytes; durable append and async
  delivery boundaries take ownership copies when they cross into storage or
  worker queues.
- Plugin Send hooks are not invoked in this adapter. They run inside
  `internal/usecase/message` after permission checks, so all entry points
  share the same hook behavior.
