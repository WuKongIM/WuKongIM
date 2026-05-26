# Meta Conversation Table Runtime Design

## Overview

`pkg/db/meta` now routes `user`, `device`, `plugin_binding`, and ordinary
`channel` access through the local table runtime. The next migration should cover
`conversation` and `cmd_conversation` together because they share the same
primary-key shape, value codec, active-index layout, and stale-index verification
pattern.

This design migrates ordinary storage mechanics for both conversation tables onto
`TableSpec` while keeping the domain-specific merge, touch, hide, clear, and read
advance semantics in their existing typed `Shard` methods.

## Goals

- Move `ConversationTable` and `CMDConversationTable` schema registration into
  their table files through typed runtime specs.
- Preserve durable table IDs, primary key layouts, active-index key layouts,
  value encodings, and public API behavior.
- Use the table runtime for primary gets, primary prefix pages, active-index
  maintenance, and active-index scans that skip stale entries.
- Keep user conversation and CMD conversation business rules explicit and easy to
  test.
- Avoid changing compatibility surfaces in `DB`, `ShardStore`, and `WriteBatch`
  beyond using the same underlying table rows/indexes.

## Non-Goals

- Do not migrate `subscriber`, `channel_runtime_meta`, `channel_migration`, or
  `hashslot_migration` in this change.
- Do not redesign conversation merge semantics or introduce new API behavior.
- Do not change row value encoding or add new persisted columns.
- Do not introduce a general event/hook framework for these tables.

## Current Behavior To Preserve

`UserConversationState` rows are keyed by `(uid, channel_id, channel_type)` under
`TableIDConversation`. `CMDConversationState` rows use the same logical key under
`TableIDCMDConversation`. Both values encode `read_seq`, `deleted_to_seq`,
`active_at`, and `updated_at`.

Both tables maintain an active index with existing layout:

```text
(uid, active_at desc, channel_id, channel_type)
```

Active scans return newest-first rows and verify the primary row before returning
an index hit. Stale active-index entries are ignored when the primary row is
missing, inactive, or has a different `ActiveAt`.

User conversation rules:

- `UpsertUserConversationState` keeps `ActiveAt` monotonic when a row exists.
- `TouchUserConversationActiveAt` creates missing rows only when the touch passes
  the delete barrier and advances `ActiveAt`.
- `ClearUserConversationActiveAt` de-duplicates and sorts keys, then clears only
  existing active rows.
- `HideUserConversation` advances `DeletedToSeq`, clears `ActiveAt`, updates
  `UpdatedAt` when newer, and does not create a missing row for a zero delete
  barrier.

CMD conversation rules:

- `UpsertCMDConversationState` merges `ReadSeq`, `DeletedToSeq`, `ActiveAt`, and
  `UpdatedAt` by taking existing maxima.
- `AdvanceCMDConversationReadSeq` advances only existing rows and ignores stale
  read patches.

## Proposed Design

Add `conversationTable = registerMetaTable(TableSpec[UserConversationState]{...})`
in `pkg/db/meta/table_conversation.go` and
`cmdConversationTable = registerMetaTable(TableSpec[CMDConversationState]{...})`
in `pkg/db/meta/table_cmd_conversation.go`.

Each spec defines:

- primary key `(uid, channel_id, channel_type)` with layout
  `KeyLayout{KeyString, KeyString, KeyInt64Ordered}`;
- primary family ID `conversationPrimaryFamilyID` or
  `cmdConversationPrimaryFamilyID`;
- active index `conversationActiveIndexID` with layout
  `KeyLayout{KeyString, KeyInt64Desc, KeyString, KeyInt64Ordered}`;
- active index key emitted only when `ActiveAt > 0`;
- a legacy-index primary projection from active-index parts back to primary
  parts:
  `(uid, active_at desc, channel_id, channel_type) -> (uid, channel_id,
  channel_type)`;
- value codec using the existing `encodeConversationValue`,
  `decodeUserConversationValue`, and `decodeCMDConversationValue` helpers.

`ConversationTable` and `CMDConversationTable` become `table.Schema()` exports.
`schema.go` stops declaring them with `activeMetaTable(...)` and stops manually
registering them in `init()`.

The domain methods keep their current orchestration but delegate row mechanics to
the runtime:

- `GetUserConversationState` / `GetCMDConversationState` use `Table.Get`.
- Upserts load current state through the runtime, apply existing merge rules, and
  write through `Table.Upsert` or a small shared stage helper.
- `ListUserConversationStatePage` uses runtime primary-prefix pagination for the
  `uid` prefix.
- `ListUserConversationActive` and `ListCMDConversationActive` use runtime index
  scanning with the `uid` prefix and existing `limit` validation.
- Any existing compatibility `WriteBatch` paths that stage conversation rows
  should use the same runtime staging helper so primary rows and active indexes
  are maintained by one implementation.

The table runtime needs one explicit compatibility extension for these active
indexes. The existing generic secondary index format appends primary key parts to
`indexParts`, which would change the durable active-index key. `PrimaryFromIndex`
only covers indexes whose index tuple is exactly the primary tuple, which is not
true here because `active_at desc` sits between `uid` and `channel_id`.

Add a reusable runtime option such as
`PrimaryKeyFromIndexParts func(KeyParts) (KeyParts, bool)`. When set:

- index entry keys are encoded as the index tuple only, preserving the existing
  physical active-index layout;
- scan decode derives primary parts from the decoded index tuple using the
  projection function;
- index scans still load the primary row and verify that the current row emits
  the same active-index tuple before returning it;
- stale active-index entries are skipped if the projected primary row is missing
  or no longer active;
- write/delete paths continue to compute old and new active-index keys from
  decoded rows, so active-index cleanup remains exact.

This option is only valid when the legacy index tuple uniquely contains enough
information to derive the primary key. Runtime spec normalization should reject
or clearly guard invalid uses where projection is absent, malformed, or
ambiguous.

For both conversation tables, the projection is:

```go
func(parts KeyParts) (KeyParts, bool) {
    if len(parts) != 4 {
        return nil, false
    }
    return KeyParts{parts[0], parts[2], parts[3]}, true
}
```

This extension should be generic enough to replace ad hoc manual active-index
decoding without changing key layouts for future tables with legacy index keys.

## Error Handling

- Validation remains at typed method boundaries for `uid`, `channel_id`, cursor,
  and positive `limit` checks.
- Runtime decode errors should still surface as corrupt-value errors for primary
  row reads.
- Active scans should continue to skip stale index entries rather than treating
  missing or inactive primary rows as errors.
- Existing idempotent methods keep idempotent behavior: stale touches, stale read
  advances, zero-barrier hide for missing rows, and no-op clears remain nil.

## Testing

Add focused red tests before implementation:

- `ConversationTable` and `CMDConversationTable` still appear in `Tables()` with
  the active index descriptor.
- Raw active-index keys for user and CMD conversations keep the legacy layout and
  do not append a primary-key suffix.
- Malformed active-index keys keep current error behavior rather than being
  silently skipped.
- User and CMD active scans skip stale active-index entries.
- User primary pagination keeps existing `(channel_id, channel_type)` ordering
  and cursor behavior.
- User upsert/touch/clear/hide semantics remain unchanged.
- CMD upsert merge and read-advance semantics remain unchanged.
- Runtime index scans continue to validate invalid index IDs and limits as
  before.

Verification commands:

- `GOWORK=off go test ./pkg/db/meta -run 'TestUserConversation|TestCMDConversation|TestMetaSchemaValidateAllTables|TestTableRuntime' -count=1`
- `GOWORK=off go test ./pkg/db/meta -count=1`
- `GOWORK=off go test ./pkg/db/... -count=1`
- `rg -n 'github.com/cockroachdb/pebble|pebble\.' pkg/db/meta`
