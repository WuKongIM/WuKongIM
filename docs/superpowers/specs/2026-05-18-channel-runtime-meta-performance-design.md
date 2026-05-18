# Channel Runtime Meta List Performance Design

## Context

`GET /manager/channel-runtime-meta?node_id=1` is slow when the system has many channels, for example 8000 channel runtime metadata records. The current handler ignores `node_id`, so the request behaves like a global list. The usecase scans authoritative slot pages and enriches every returned row with `max_message_seq`. That enrichment creates an N+1 path because each row calls `Messages.MaxMessageSeq`, which re-reads runtime meta and then reads local channel checkpoint or calls the remote channel leader.

## Goals

- Make `node_id` meaningful for channel runtime meta list queries.
- Remove the default N+1 message sequence enrichment from the list endpoint.
- Preserve detail endpoint behavior for users that need exact per-channel `max_message_seq`.
- Keep the first change low-risk and compatible with existing pagination.
- Keep deployment wording consistent with single-node cluster semantics.

## Non-Goals

- Do not add a new metadb node index in the first pass.
- Do not change channel runtime meta storage layout in the first pass.
- Do not change slot/hash-slot ownership semantics.
- Do not remove `max_message_seq` support entirely.

## API Behavior

### List Endpoint

`GET /manager/channel-runtime-meta` accepts these additional query parameters:

- `node_id`: optional positive node ID filter.
- `node_scope`: optional node matching scope. Supported values are `any`, `leader`, `replica`, and `isr`. Default is `any` when `node_id` is present.
- `include_max_message_seq`: optional boolean. Default is `false`.

When `node_id` is omitted, the endpoint keeps the existing global pagination behavior.

When `node_id` is present:

- `leader` matches `meta.Leader == node_id`.
- `replica` matches `node_id` in `meta.Replicas`.
- `isr` matches `node_id` in `meta.ISR`.
- `any` matches any of leader, replica, or ISR.

The list response omits `max_message_seq` unless `include_max_message_seq=true`. This makes the common list page cheap and avoids per-row message-log reads.

### Detail Endpoint

`GET /manager/channel-runtime-meta/:channel_type/:channel_id` keeps returning `max_message_seq` because it handles one channel and the extra authoritative read is bounded.

## Usecase Changes

Add fields to `management.ListChannelRuntimeMetaRequest`:

```go
NodeID uint64
NodeScope string
IncludeMaxMessageSeq bool
```

Add filtering after each authoritative page scan. Filtering remains in usecase for the first pass so storage and RPC formats remain stable. If filtering removes rows and the response has not reached `Limit`, the usecase continues scanning subsequent pages and slots until it fills the page or reaches the end.

Keep cursor semantics based on the last scanned/emitted runtime meta item. For filtered lists, `NextCursor` should allow the next request to continue after the last emitted matching item. If a page has no matching items but more data exists, the usecase should continue internally instead of returning an empty page with `has_more=true`.

## Message Sequence Enrichment

Change `ChannelRuntimeMeta.MaxMessageSeq` to optional at manager/usecase and access DTO boundaries:

```go
MaxMessageSeq *uint64
```

Default list path:

- Does not call `channelMaxMessageSeq`.
- Returns rows without `max_message_seq`.

Explicit list path with `include_max_message_seq=true`:

- Enriches returned rows only.
- Avoids redundant runtime meta reads by adding an adapter method that accepts the already-scanned `metadb.ChannelRuntimeMeta`, for example `MaxMessageSeqForMeta(ctx, meta)` or an internal helper on the app adapter.
- Reads local checkpoint when `meta.Leader` is local and calls the remote node only when needed.

Detail path:

- Continues to call the existing single-channel max sequence logic or the new meta-aware helper.

## Error Handling

- Invalid `node_id` values (`0`, non-integer, negative) return HTTP 400.
- Invalid `node_scope` returns HTTP 400.
- Invalid `include_max_message_seq` returns HTTP 400.
- Existing slot leader authoritative read errors keep mapping to HTTP 503.
- If `include_max_message_seq=true` and one row's max sequence cannot be read, return the existing error rather than silently dropping the field.

## Testing

Add or update manager access tests:

- `node_id` is parsed and passed to management usecase.
- invalid `node_id` returns 400.
- `node_scope` is parsed and validated.
- `include_max_message_seq` is parsed and passed through.
- list response omits `max_message_seq` when not included.
- list response includes `max_message_seq` when requested.

Add or update management usecase tests:

- node scope filtering works for `leader`, `replica`, `isr`, and `any`.
- filtered scans continue across pages and slots until `Limit` matching rows are returned or data ends.
- default list path does not call `Messages.MaxMessageSeq`.
- explicit include path calls max sequence only for returned rows.
- explicit include path reuses already-scanned metadata and does not perform per-row `GetChannelRuntimeMeta`.

Add focused adapter tests:

- local leader max sequence reads the local checkpoint.
- remote leader max sequence calls `QueryChannelMessages` with `MaxSeqOnly=true`.
- no leader returns the existing no-leader error.

## Future Optimization

If channel counts grow beyond this first-pass fix, add metadb secondary indexes for node-oriented scans:

- leader node to channel runtime meta key.
- replica node to channel runtime meta key.
- ISR node to channel runtime meta key.

That change would make `node_id` filters storage-native instead of scan-and-filter. It should be a separate migration-aware design because it changes write paths and snapshot/restore coverage.
