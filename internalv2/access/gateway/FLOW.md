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
  -> map valid packet items into message.SendBatchItem
  -> call message.SendBatch when available
  -> require item-aligned result count
  -> write one SendackPacket for every input item
```

Unauthenticated sends and nil message usecases are converted into sendacks
instead of raw protocol errors. Unsupported non-SEND frames still return
`ErrUnsupportedFrame`.

## Boundaries

- This package may import `pkg/gateway` and `pkg/protocol/frame`.
- This package must not import `pkg/clusterv2` or `pkg/channelv2`.
- Payloads are cloned while mapping so later frame reuse cannot mutate usecase
  commands.
