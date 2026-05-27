# internalv2/usecase/message Flow

## Responsibility

`internalv2/usecase/message` owns entry-agnostic SEND orchestration. It knows
about durable append ports and committed-message events, but not gateway frames,
wire protocols, or concrete cluster runtimes.

## SendBatch Flow

```text
SendBatch(items)
  -> normalize nil contexts
  -> validate authenticated sender, channel, payload, and phase-1 send mode
  -> authorize send
  -> allocate message IDs for durable gateway-origin sends
  -> clone payloads
  -> split adjacent sends by canonical channel
  -> append each active segment through Appender.AppendBatch
  -> submit committed-message events after successful append
  -> return item-aligned SendBatchItemResult values
```

`Send(ctx, cmd)` delegates to `SendBatch` with one item.

Append contexts are derived from active item deadlines and are not tied to a
single client context cancellation. Items that are already canceled are filtered
before append and get their own error result.

## Import Boundary

The usecase package must remain independent from concrete entries and cluster
adapters. The import-boundary test rejects imports of:

- `pkg/gateway`
- `pkg/protocol/frame`
- `pkg/clusterv2`
- `pkg/channelv2`
- `internalv2/access`
- `internalv2/app`
