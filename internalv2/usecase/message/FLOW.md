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
  -> canonicalize person-channel IDs when `SendCommand.NormalizePersonChannel`
     is set by an entry adapter
  -> allocate message IDs for durable gateway-origin sends
  -> group sends by canonical channel while preserving per-channel order
  -> clone payloads at the appender boundary
  -> append active channel groups through Appender.AppendBatch with bounded
     channel-level concurrency
     (omit result payloads when no committed sink is configured)
     (retry transient batch-level route errors within the active item deadline)
  -> record per-message append observations through the optional observer,
     including batch-level, item-level, and short-result append errors
  -> submit committed-message events after successful append when a sink is configured
     (including sender identity and request-scoped delivery UIDs)
  -> return item-aligned SendBatchItemResult values
```

`Send(ctx, cmd)` delegates to `SendBatch` with one item.

Append contexts are derived from active item deadlines, including explicit
batch item deadlines supplied by entry adapters, and are not tied to a single
client context cancellation. Items that are already canceled are filtered before
append and get their own error result.

## Import Boundary

The usecase package must remain independent from concrete entries and cluster
adapters. The import-boundary test rejects imports of:

- `pkg/gateway`
- `pkg/protocol/frame`
- `pkg/clusterv2`
- `pkg/channelv2`
- `internalv2/access`
- `internalv2/app`
