# internalv2/usecase/message Flow

## Responsibility

`internalv2/usecase/message` owns the entry-agnostic message facade and
compatible channel message sync. SEND work is delegated to a configured
`channelwrite.Submitter`; this package does not own channel authority routing,
durable append, message ID allocation, or post-commit delivery effects. It knows
about the channel message read port for sync, but not gateway frames, wire
protocols, HTTP JSON, or concrete cluster runtimes.

## SendBatch Flow

```text
SendBatch(items)
  -> if Submitter is nil, return item-aligned ErrRouteNotReady
  -> delegate the item-aligned batch to Submitter.SendBatch
  -> return item-aligned SendBatchItemResult values
```

`Send(ctx, cmd)` delegates directly to `Submitter.Send`. The configured
submitter is normally the app-level channel write router, which resolves channel
append authority and admits work into the authority node's channel write
reactor. Validation, person-channel normalization, request-scoped command
channel derivation, message ID allocation, append retries, committed cursors,
subscriber scan, conversation projection, and online delivery are all owned by
`internalv2/runtime/channelwrite`.

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
