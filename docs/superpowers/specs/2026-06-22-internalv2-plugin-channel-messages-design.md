# Internalv2 Plugin Channel Messages Design

## Goal

Migrate the PDK-compatible plugin-origin host RPC `/channel/messages` into `internalv2` so plugins can read authoritative channel message pages through the v2 committed-message read path.

## Scope

- Add `/channel/messages` to `internalv2/access/plugin`.
- Add `ChannelMessages` orchestration and mapping to `internalv2/usecase/plugin`.
- Wire the v2 app plugin subsystem to the existing clusterv2-backed channel message reader when available.
- Cover usecase, access, app wiring, and benchmark behavior.
- Update plugin flow documentation.

Out of scope:

- `/cluster/config`, `/cluster/channels/belongNode`, `/conversation/channels`, `/plugin/httpForward`, and stream host RPCs.
- New storage readers or direct channel log access from the plugin usecase.
- Login-UID-based person channel normalization. The legacy `/channel/messages` host RPC is a storage-facing batch read and carries explicit channel IDs.

## Compatibility Rules

- The access layer decodes and encodes existing `internal/usecase/plugin/pluginproto` protobuf messages.
- Request batch shape stays `ChannelMessageBatchReq` and response shape stays `ChannelMessageBatchResp`.
- Each item maps to `message.ChannelMessageQuery`:
  - `ChannelID.ID = req.channelId`
  - `ChannelID.Type = uint8(req.channelType)`
  - `StartSeq = req.startMessageSeq`
  - `Limit = req.limit`, with legacy default `100` and cap `10000`
  - `PullMode = message.PullModeUp`
- `metadb.ErrNotFound` returns an item-aligned empty response instead of failing the whole batch.
- Other reader errors fail the host RPC.
- Response metadata echoes the request channel ID, channel type, start message seq, and effective limit.
- Message payloads are cloned before crossing the plugin boundary.

## Architecture

```text
plugin process
  -> wkrpc /channel/messages
  -> internalv2/access/plugin.Server.handleChannelMessages
  -> internalv2/usecase/plugin.App.ChannelMessages
  -> MessageReader.SyncMessages
  -> clusterinfra.ChannelMessageReader
  -> clusterv2 ReadChannelCommitted
```

`internalv2/access/plugin` remains a protocol adapter. It owns route registration, protobuf body limits, timeout context, caller UID propagation, and response writing.

`internalv2/usecase/plugin` owns legacy PDK mapping and depends on a narrow `MessageReader` port using the v2 message usecase query/page types.

`internalv2/app` wires `clusterinfra.NewChannelMessageReader(readNode)` into plugin options when the cluster implements `clusterinfra.ChannelMessageReadNode`. This reuses the same committed-message read adapter already used by the v2 message usecase without forcing plugin reads through `message.App.SyncChannelMessages`, which requires login UID semantics that legacy plugin reads do not have.

## Failure Semantics

- Missing reader returns `ErrMessageReaderRequired`.
- Missing channel rows (`metadb.ErrNotFound`) return empty item responses.
- Oversized request or response bodies keep using the existing access-layer body limit errors.
- The batch is item-aligned: successful items before an infrastructure error are not returned when the overall host RPC fails, matching the current legacy usecase behavior.

## Performance Notes

The hot cost is committed message read I/O. The plugin layer adds only batch proto decode, query mapping, payload clone, and response encode. Benchmarks should cover usecase mapping and access handler round-trip overhead to keep a baseline as more host RPCs migrate.

## Test Plan

- `internalv2/usecase/plugin`:
  - batch request maps to reader queries
  - default limit `100`
  - capped limit `10000`
  - `metadb.ErrNotFound` maps to empty item response
  - missing reader returns `ErrMessageReaderRequired`
  - response uses effective limit and clones message payload
- `internalv2/access/plugin`:
  - route registration includes `/channel/messages`
  - handler decodes request, applies timeout, passes caller UID, and writes `ChannelMessageBatchResp`
- `internalv2/app`:
  - plugin subsystem wires a channel message reader when cluster read support exists
- Benchmarks:
  - `BenchmarkChannelMessagesFromPluginReq`
  - `BenchmarkChannelMessagesHostRPCHandler`

## Self Review

- Scope is one host RPC and does not mix in cluster, HTTP forward, conversation, or stream migration.
- Boundaries remain `access -> usecase -> port`, with `app` owning infra wiring.
- The design explicitly avoids login UID normalization because legacy `/channel/messages` does not carry a caller user.
