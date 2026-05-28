# internalv2/access/gateway Flow

## Responsibility

`internalv2/access/gateway` adapts `pkg/gateway` events to the entry-agnostic
message usecase. It does not own durable send business rules.

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
  -> write PongPacket on the same gateway session
```

Unauthenticated sends and nil message usecases are converted into sendacks
instead of raw protocol errors. Unsupported frames other than SEND and PING
still return `ErrUnsupportedFrame`.

## Boundaries

- This package may import `pkg/gateway` and `pkg/protocol/frame`.
- This package must not import `pkg/clusterv2` or `pkg/channelv2`.
- Single-frame SEND payloads are cloned while mapping so later frame reuse
  cannot mutate usecase commands. Batched SEND keeps the async-dispatch-owned
  frame payload until `internalv2/usecase/message` clones at the append boundary.
