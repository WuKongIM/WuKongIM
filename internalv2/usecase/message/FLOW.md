# internalv2/usecase/message Flow

## Responsibility

`internalv2/usecase/message` owns entry-agnostic SEND orchestration and
compatible channel message sync. It knows about durable append/read ports and
committed-message events, but not gateway frames, wire protocols, HTTP JSON, or
concrete cluster runtimes.

## SendBatch Flow

```text
SendBatch(items)
  -> normalize nil contexts
  -> validate authenticated sender, channel, payload, and phase-1 send mode
     (request-scoped sends derive a temporary command channel from
      MessageScopedUIDs when RequestScoped=true)
  -> authorize send
  -> canonicalize person-channel IDs when `SendCommand.NormalizePersonChannel`
     is set by an entry adapter
  -> check optional sender idempotency using the canonical sender/client
     message key
  -> allocate message IDs for durable gateway-origin sends
  -> group sends by canonical channel while preserving per-channel order
  -> derive one server append timestamp per prepared send for durable storage
     and committed-message events
  -> clone payloads at the appender boundary and propagate sendtrace metadata
     plus one-based append attempt to request-level and per-message append
     payload fields
  -> append active channel groups through Appender.AppendBatch in first-seen
     channel order; gateway asynchronous dispatch owns outer concurrency
     (omit result payloads when no committed sink needs appended payload bytes)
     (retry transient batch-level route errors within the active item deadline)
  -> record per-message append observations through the optional observer,
     including batch-level, item-level, and short-result append errors
  -> record `message.send_durable` sendtrace events for traced active items,
     including successful sequences and stable append error classes
  -> submit committed-message events after successful append when a sink is configured
     (including sender identity and server timestamp, plus payload and request-scoped
      delivery UIDs only when the configured committed sinks require payload bytes)
  -> return item-aligned SendBatchItemResult values
```

Committed sinks may process events asynchronously and must not alter successful
send results. Metadata-only sinks do not require committed payload bytes; sinks
that perform online delivery keep payload bytes only when their contracts ask
for them.

`Send(ctx, cmd)` delegates to `SendBatch` with one item.

Append contexts are derived from active item deadlines, including explicit
batch item deadlines supplied by entry adapters, and are not tied to a single
client context cancellation. Items that are already canceled are filtered before
append and get their own error result.

## SyncChannelMessages Flow

```text
SyncChannelMessages(query)
  -> validate login_uid, channel_id, and channel_type with legacy error strings
  -> canonicalize person-channel IDs using login_uid
  -> cap limit to the legacy maximum
  -> call ChannelMessageReader.SyncMessages with a normalized ChannelID
  -> treat missing channel runtime/storage as an empty page
  -> clone payloads before returning SyncedMessage values to access adapters
```

The sync usecase returns `SyncedMessage` DTOs with the fields needed by legacy
HTTP responses. Concrete storage adapters may return zero values for fields that
the current ChannelV2 write path does not persist yet.

## Import Boundary

The usecase package must remain independent from concrete entries and cluster
adapters. The import-boundary test rejects imports of:

- `pkg/gateway`
- `pkg/protocol/frame`
- `pkg/clusterv2`
- `pkg/channelv2`
- `internalv2/access`
- `internalv2/app`
