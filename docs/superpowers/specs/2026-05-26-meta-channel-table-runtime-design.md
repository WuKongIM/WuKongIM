# Meta Channel Table Runtime Design

## Overview

`pkg/db/meta` now has a local table runtime for ordinary metadata tables. The
first migration covered `user`, `device`, and `plugin_binding`. The next useful
step is migrating `Channel` because it is still a common table with a primary
row and one queryable secondary index, but it also exercises the runtime with
external cache side effects.

This design migrates only the channel table onto `TableSpec`. It does not try to
migrate subscriber, conversation, runtime metadata, or migration state-machine
behavior in the same change.

## Goals

- Move channel schema registration into `table_channel.go` with a typed
  `channelTable` spec.
- Use the table runtime for channel primary CRUD and the `channel_id` secondary
  index.
- Preserve the existing durable key layout, value layout, public API behavior,
  and channel cache invalidation behavior.
- Remove `ChannelTable` from the central manual registry in `schema.go`.
- Keep implementation narrow enough that follow-up table migrations can reuse
  the pattern.

## Non-Goals

- Do not migrate subscriber mutation orchestration in this change.
- Do not migrate `Conversation`, `CMDConversation`, `ChannelRuntimeMeta`,
  `ChannelMigration`, or `HashSlotMigration` yet.
- Do not change channel value encoding or channel table IDs/index IDs.
- Do not introduce a general hook system in the runtime just for cache side
  effects.

## Proposed Design

Add `channelTable = registerMetaTable(TableSpec[Channel]{...})` in
`pkg/db/meta/table_channel.go`.

The spec uses:

- table ID: `TableIDChannel`;
- primary key: `(channel_id, channel_type)` encoded as
  `KeyParts{String(channel.ChannelID), Int64Ordered(channel.ChannelType)}`;
- primary family: `channelPrimaryFamilyID`;
- secondary index: `channelIDIndexID`, keyed by `(channel_id, channel_type)`;
- value codec: existing `encodeChannelValue` and `decodeChannelValue`.

`ChannelTable` becomes `channelTable.Schema()`. `schema.go` stops declaring and
registering `ChannelTable` manually.

Shard methods remain domain-oriented:

- `CreateChannel`, `UpsertChannel`, and `UpdateChannel` call the matching
  runtime operation and then invalidate the channel cache on success.
- `GetChannel` still checks cache first, falls back to `channelTable.Get`, then
  stores successful reads in cache.
- `DeleteChannel` calls `channelTable.Delete` and invalidates cache. Its current
  missing-row behavior remains idempotent.
- `ListChannelsByChannelID` calls `channelTable.ScanIndex` with a
  `channel_id` prefix and verifies ordering through existing behavior tests.

The existing `stageChannel` helper stays custom for `Batch.UpsertChannel`
because that method also publishes channel cache entries after commit through
`batchCommitState.channelPublishes`. Moving batch channel writes to the generic
runtime can be a later improvement, but is not needed for the current goal.

## Error Handling

- Validation continues to reject invalid `ChannelID` through `validateChannel` or
  `validateKeyString` before runtime calls.
- Runtime operations keep returning `ErrAlreadyExists` for create conflicts and
  `ErrNotFound` for update misses.
- `ListChannelsByChannelID` returns an invalid-argument error for invalid channel
  IDs and otherwise skips stale index entries through runtime index validation.

## Testing

Use the existing channel tests as the compatibility contract and add focused
regressions for runtime-specific behavior:

- schema still includes all tables and channel descriptor is registry-backed;
- channel CRUD and cache invalidation still behave as before;
- channel-id index scan skips stale index entries;
- create duplicate and update missing still map to the same errors;
- `Batch.UpsertChannel` still publishes cache after commit.

Verification commands:

- `GOWORK=off go test ./pkg/db/meta -run 'TestChannel|TestMetaBatch|TestMetaSchemaValidateAllTables' -count=1`
- `GOWORK=off go test ./pkg/db/meta -count=1`
- `GOWORK=off go test ./pkg/db/... -count=1`
