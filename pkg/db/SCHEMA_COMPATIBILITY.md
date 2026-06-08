# pkg/db Schema Compatibility

This document explains how to add fields to existing `pkg/db` tables without
breaking old data, rolling upgrades, rollback, snapshots, or diagnostics.

The short rule is: make durable formats append-only, make new fields optional
when reading old rows, and do not make new indexes or new field semantics
correctness-critical until old rows and mixed-version nodes are handled.

## Compatibility Targets

Every existing-table field change must define what remains compatible:

- Old on-disk rows must be readable by the new code.
- New rows should be readable by old code during rolling upgrade whenever the
  table can be touched by old and new nodes at the same time.
- A rollback should not turn rows written by the new binary into corrupt rows
  for the old binary.
- Hash-slot snapshots, channel snapshots, indexes, inspect APIs, and legacy
  compatibility wrappers must remain internally consistent.

If any of these cannot be true, the change is not a simple field add. Treat it
as a format migration and gate it behind an explicit rollout plan.

## Stable Durable IDs

The following IDs are part of the stored format and must be treated as permanent:

- table IDs, such as `meta.TableID*` and `message.TableIDMessage`
- row family IDs
- primary and secondary index IDs
- column IDs
- primary-key and index key layouts
- system key IDs
- value codec versions and fixed binary payload layouts

Do not renumber, reuse, reorder with a different meaning, or delete these IDs.
When adding a field, allocate a new column ID and keep all old IDs unchanged.
Prefer assigning the next highest column ID so `rowcodec.Writer` calls can stay
in ascending order.

## Prefer Column Values For New Fields

`pkg/db/internal/rowcodec` is the safest format for compatible field additions:

- values are stored by stable column ID
- unknown columns are ignored by older decoders
- missing columns naturally decode to Go zero values
- values are wrapped with a checksum-bound envelope

For tables already encoded with `rowcodec.CodecColumns`, add fields this way:

1. Add a new column ID constant.
2. Add a `schema.Column` with `Required: false`.
3. Add the column to the correct `schema.Family`.
4. Add the field to the typed row struct.
5. Encode the column in ascending column-ID order.
6. Decode the column in the `switch`; keep the `default` case ignoring unknown
   columns.
7. Normalize missing old-row values after decode when zero is not the desired
   in-memory behavior.
8. Add tests that decode a pre-change payload without the new column.
9. Add tests that append an unknown future column and confirm current decoders
   ignore it when the format is expected to be rolling-upgrade safe.

Use an existing `rowcodec.Type` for rolling-upgrade-safe field additions.
Adding a new encoded value type is a codec change; old scanners may not be able
to skip it.

Do not bump the value envelope version just because an optional column was
added. Keep the same version so old and new code can share the row format.

Only bump a value version for an incompatible encoding change, and then the
decoder must support every older version that can still exist on disk before
the writer starts emitting the new version.

## Required Columns

Never mark a newly added column on an existing table as `Required: true`.
Old rows do not contain that column.

Use `Required: true` only for:

- columns that were already effectively required in all existing rows
- primary key columns already encoded in the durable key
- new tables that have no old rows

If the new behavior needs a non-zero value, compute it in decode/normalize logic
from existing fields or use an explicit backfill before relying on it.

## Fixed Or Raw Binary Values

Some metadata tables still use custom fixed or raw binary payloads, for example
`appendValue*` / `readValue*` helpers in `pkg/db/meta`, catalog values in
`pkg/db/message`, and several system records.

These formats are not automatically forward-compatible. A decoder that requires
`len(rest) == 0` will reject new tail bytes written by a newer binary.

For these tables, choose one of these patterns:

- Best: move only the new field into a separate optional rowcodec/system record,
  leaving the old value bytes untouched.
- Good: convert the value to an envelope/column codec only when old binaries
  cannot read or write the row during the rollout, and keep a dual decoder for
  the old value bytes.
- Acceptable for new-code-only compatibility: append the field at the tail and
  update the new decoder to accept both the old shorter length and the new
  longer length.

Appending to fixed/raw values is not enough for rolling-upgrade compatibility
unless the previous released decoder already ignores unknown tail bytes.

When a custom binary decoder is changed, write tests for all supported lengths
or versions. Keep corruption checks strict for malformed payloads that do not
match any supported version.

## Primary Keys And Existing Indexes

Do not change an existing primary key or index layout in place:

- do not insert a field into an existing key
- do not reorder key parts
- do not change sort direction or uniqueness
- do not change what a key part means

Such changes move rows to different keyspaces and require a new table or a new
index ID plus a migration/backfill plan.

## Adding A Secondary Index

Adding an index is more than adding a schema descriptor. Existing rows will not
have index entries until they are backfilled or rewritten.

Safe index additions need a plan for:

- writing the new index entry on all creates, updates, batch writes, deletes,
  truncates, retention deletes, and snapshot installs that touch the table
- backfilling or lazily repairing index entries for existing rows
- validating uniqueness before a unique index becomes authoritative
- keeping reads correct while the index is incomplete
- deleting old index entries whenever indexed columns change
- including the index keyspace in snapshots or import/export behavior

Do not make a new index the only read path until old rows have been covered.
During rollout, prefer a fallback scan or a verified repair path.

## Message Table Fields

For `pkg/db/message` message rows:

- update `messageRow`, `MessageTable`, `encodeMessageHeader` or
  `encodeMessagePayload`, `decodeMessageHeaderColumn`, and materialization logic
  such as `messageFromRow`
- keep `messageValueVersion` unchanged for optional rowcodec columns
- keep header and payload families separate unless the new field really belongs
  with payload bytes
- update append/apply-fetch/compat paths that construct `messageRow`
- update every index maintenance path if the field participates in an index
- update inspect output only if the field should be visible to diagnostics

The message reader combines header and payload families by sequence. A new field
must not make old messages fail `validateMaterializedMessageRow`.

## Metadata Table Fields

For `pkg/db/meta` table-runtime tables:

- update the table's `TableSpec` columns and families
- update the typed struct and English field comments for exported fields
- update `EncodeValue` / `EncodeValueWithKey`
- update `DecodeValue` / `DecodeValueWithKey`
- update `Validate` only for invariants that old rows can satisfy
- update typed methods, batch overlays, cache invalidation, and compatibility
  wrappers when they read or write the field
- update inspect row mapping if diagnostics should expose the field

For rowcodec-backed metadata values, normalize missing columns after decode.
For custom binary metadata values, follow the fixed/raw rules above.

## Snapshots And Import

Metadata hash-slot snapshots export raw row, index, and system keyspaces. Field
additions inside an existing row value usually do not need snapshot version
changes, but the imported value must be decodable by the receiving binary.

Check these cases:

- a snapshot produced before the field existed imports into new code
- a snapshot produced by new code imports into any old binary that may still be
  supported during rollback
- preserving imports still preserve or replace the intended table spans
- new indexes or system records are included in the appropriate keyspace

Do not bump `slotSnapshotVersion` for an ordinary optional field. Bump it only
when the snapshot payload format itself changes.

## Tests To Add

At minimum, add focused tests near the changed package:

- schema validation still passes
- old encoded row/value without the new field decodes successfully
- new encoded row/value round-trips with the field
- unknown future rowcodec columns are ignored when expected
- default/normalize behavior for missing fields is explicit
- any changed index is written, read, deleted, and repaired/backfilled as planned
- snapshot/export/import behavior covers the new field when relevant
- legacy compatibility wrappers still preserve the field or intentionally ignore
  it with documented behavior

Keep these as unit tests unless the change requires real multi-node rollout
behavior. Longer rollout or backfill tests should use the integration tag.

## Review Checklist

Before merging an existing-table field add, verify:

- no durable ID was reused or renumbered
- the field is optional for old rows
- old rows decode with a well-defined default
- new rows do not become corrupt for supported rollback or mixed-version readers
- primary/index key layouts were not changed in place
- all write paths populate or intentionally omit the field
- all read paths handle both missing and present values
- inspect, snapshots, caches, and compatibility adapters are updated when needed
- related tests were run, at least `go test ./pkg/db/...`
