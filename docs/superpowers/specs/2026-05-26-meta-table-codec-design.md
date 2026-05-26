# Meta Table Codec Design

## Goal

Make compatible `pkg/db/meta` table and field changes more centralized while
preserving the current storage layout, layering rules, and existing behavior.
The first implementation will introduce shared package-private helpers and
migrate two representative tables:

- `ChannelRuntimeMeta`: a complex rowcodec-backed table with many fields.
- `PluginBinding`: a simple table with a secondary index and legacy fixed-value
  compatibility needs.

This change is intentionally scoped to `pkg/db/meta`; message storage remains
unchanged.

## Non-Goals

- Do not introduce a generic ORM or reflection-heavy CRUD generator.
- Do not change existing table IDs, column IDs, key encoding, index IDs, or
  snapshot keyspaces.
- Do not migrate every metadata table in one pass.
- Do not add a general schema migration runner.

## Design

### Shared Column Codec Helpers

Add a package-private helper file under `pkg/db/meta`, tentatively
`table_codec.go`. It will define typed column descriptors for rowcodec-backed
metadata values. Each descriptor owns:

- stable column ID;
- schema name and logical type;
- whether the value is required for decode;
- getter used by encode;
- setter used by decode.

The helper will expose small constructor functions such as `stringColumn`,
`uint64Column`, `int64Column`, `uint8Column`, `boolColumn`, and `bytesColumn`.
The exact names can stay package-private.

Encoding will iterate descriptors in declared order and write values with
`rowcodec.Writer`, then wrap the payload with `rowcodec.Wrap`. Descriptor order
must be ascending by column ID; tests should reject duplicate or descending IDs.

Decoding will unwrap the rowcodec envelope, scan columns, apply known setters,
ignore unknown columns for forward compatibility, and then check required
columns. Missing optional columns keep Go zero values or defaults applied by the
caller after decode.

### Shared Schema Helpers

Add helper functions that build `schema.Column` and `schema.Family` entries from
the same column descriptor list. Table descriptors remain ordinary
`schema.Table` values returned by `Tables()`.

This reduces drift between schema declarations and value codecs without changing
how existing tests and callers consume table descriptors.

### ChannelRuntimeMeta Migration

Replace the hand-written runtime metadata encode/decode scanner switch with a
column descriptor list. Keep these stable:

- `TableIDChannelRuntimeMeta`;
- row key format;
- `channelRuntimeMetaPrimaryFamilyID`;
- all existing runtime metadata column IDs;
- `runtimeMetaValueVersion = 1`;
- rowcodec envelope with checksum.

`ChannelRuntimeMetaTable` should describe the real runtime metadata columns
rather than using the current generic `simpleMetaTable` descriptor.

Business logic remains hand-written because it encodes important semantics:
monotonic updates, route generation, retention advancement, write fences,
guards, and pagination.

### PluginBinding Migration

Migrate `PluginUserBinding` value encoding to the new rowcodec column helper for
new writes. Preserve these stable parts:

- `TableIDPluginBinding`;
- row key format: UID + plugin number + family;
- plugin number secondary index key format;
- index value behavior.

The decoder must support both formats:

1. new rowcodec envelope values;
2. legacy fixed binary values currently produced by
   `encodePluginUserBindingValue`.

This preserves old data compatibility. Existing index reads must continue to
load and verify the primary row so stale index entries are not trusted.

`PluginBindingTable` should describe concrete columns: UID, plugin number,
created time, and updated time. Key columns can be included in the schema even
when they are derived from the row key; the value codec only needs to encode the
non-key fields unless the implementation chooses to include self-describing key
columns as optional value columns.

### Adding A New Meta Table After This Refactor

A normal new table should require these concentrated edits:

1. Add stable table/family/index IDs in `pkg/db/meta/types.go`.
2. Create `pkg/db/meta/table_<name>.go` with the public row type, column
   descriptors, schema table descriptor, value encode/decode helpers, validation,
   and `Shard` methods.
3. Add row/index key helpers in `pkg/db/meta/keys.go`.
4. Add the table descriptor to `Tables()`.
5. Add compatibility wrappers in `compat.go` only when legacy callers need them.
6. Add batch helpers only when cross-table, guarded, or multi-slot atomic writes
   require them.
7. Add focused schema, codec, CRUD, index, and snapshot/delete tests.

Adding a field to a table that already uses the new helper should usually only
require adding the struct field, one column descriptor, and tests. If the new
field participates in an index, the index key and set/delete paths still need
explicit code.

## Data Compatibility

- Existing rowcodec values remain readable because value versions and column IDs
  are unchanged.
- Unknown columns are ignored during decode, enabling forward-compatible reads by
  newer code.
- Missing optional columns decode to zero values, enabling reads of old rows.
- Plugin binding supports both legacy fixed binary and new rowcodec values.
- No existing snapshot format changes are required because hash-slot snapshots
  copy raw key/value entries.

## Error Handling

- Invalid descriptor definitions should fail in unit tests; runtime helpers may
  return `dberrors.ErrInvalidArgument` for duplicate/descending columns if called
  incorrectly.
- Decode errors should continue to return `dberrors.ErrCorruptValue` or wrapped
  checksum errors from `rowcodec.Unwrap`.
- Required missing columns should return `dberrors.ErrCorruptValue`.
- Optional missing columns should not fail decode.

## Testing Plan

Run targeted tests rather than the full integration suite:

- `go test ./pkg/db/internal/...`
- `go test ./pkg/db/meta`

Add or update tests for:

- descriptor validation and schema table IDs;
- column helper round trips, required columns, unknown column ignore behavior;
- `ChannelRuntimeMeta` round trip and existing behavior tests;
- `PluginBinding` new rowcodec round trip;
- `PluginBinding` legacy fixed-value decode compatibility;
- existing plugin binding secondary index behavior.

## Risks

- A helper that tries to own CRUD would overfit and conflict with existing guard,
  cache, and index semantics. This design avoids that by centralizing only
  schema/value codec plumbing.
- Plugin binding format migration introduces mixed value formats. The decoder
  must reliably detect rowcodec envelope values before falling back to legacy
  fixed values.
- Including key-derived fields in schema but not value payloads can be confusing.
  Tests and helper naming should make clear which columns are value-encoded.
