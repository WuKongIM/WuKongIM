# Meta Table Runtime Design

## Overview

`pkg/db/meta` currently makes adding a metadata table expensive. A new table can
require coordinated edits in table IDs, schema descriptors, key helpers, CRUD
code, secondary index maintenance, batch staging, compatibility adapters,
snapshot import/export, and tests.

This design adds a meta-local table runtime. The runtime keeps table-specific
business decisions explicit, but centralizes primary row keys, secondary index
keys, common CRUD, index maintenance, batch staging, and snapshot/delete table
registration. A normal new metadata table should usually require only one table
file and one test file.

## Goals

- Make ordinary metadata table additions concentrated in one business table
  file.
- Preserve the current metadata keyspace semantics:
  `meta / hash-slot / row|index|system`.
- Keep `pkg/db/meta` storage code free of Pebble imports.
- Keep complex existing table semantics intact while simple tables migrate
  incrementally.
- Let snapshot export/import and hash-slot deletion automatically include newly
  registered tables.
- Avoid generator, reflection, or tag-based magic in the first version.

## Non-Goals

- Do not introduce `go generate` or generated typed APIs.
- Do not move this runtime into `pkg/db/internal/table` in the first version.
- Do not change `pkg/db/message`.
- Do not force all existing metadata tables onto the runtime immediately.
- Do not rewrite existing durable value encodings unless a migrated table opts
  into a new codec.
- Do not add a generic global service layer or another aggregate storage object.

## Recommended Approach

Add a `pkg/db/meta`-local table runtime.

This is narrower than a reusable `pkg/db/internal/table` package, but it avoids
over-abstracting around the different partition models used by metadata
hash-slots and message channel logs. `pkg/db/message` can later reuse the same
ideas only if the meta runtime proves stable.

## Files

New runtime files:

- `pkg/db/meta/table_registry.go`
  - Registers metadata table specs.
  - Drives `Tables()`.
  - Validates stable IDs and descriptor conflicts.
  - Exposes row spans and snapshot policy.
- `pkg/db/meta/table_runtime.go`
  - Implements common create, upsert, get, delete, primary scan, index scan, and
    batch staging.
  - Maintains secondary indexes by reading old rows and replacing old index
    entries.
- `pkg/db/meta/table_key.go`
  - Encodes and decodes typed key parts.
  - Builds primary row keys and secondary index keys.
  - Keeps key encoding explicit and ordered.

Per-table files:

- `pkg/db/meta/table_<name>.go`
  - Defines the row type.
  - Defines the `TableSpec`.
  - Defines validation and value encode/decode functions.
  - Defines thin `Shard` and optional `Batch` wrappers.
- `pkg/db/meta/table_<name>_test.go`
  - Tests the table's public behavior.
  - Relies on runtime tests for common CRUD and index mechanics.

## Table Registration

Each table registers one typed spec:

```go
var fooTable = registerMetaTable(TableSpec[Foo]{
    ID:   TableIDFoo,
    Name: "foo",

    Primary: PrimarySpec[Foo]{
        IndexID:  1,
        FamilyID: 0,
        Name:     "pk_foo",
        Layout:   KeyLayout{KeyString},
        Key: func(row Foo) KeyParts {
            return KeyParts{String(row.ID)}
        },
        Decode: decodeFooPrimary,
    },

    Indexes: []IndexSpec[Foo]{
        {
            ID:   2,
            Name: "idx_foo_owner",
            Layout: KeyLayout{KeyString, KeyString},
            Key: func(row Foo) (KeyParts, bool) {
                return KeyParts{String(row.Owner), String(row.ID)}, true
            },
        },
    },

    EncodeValue: encodeFooValue,
    DecodeValue: decodeFooValue,
    Validate:    validateFoo,
})
```

`Tables()` becomes registry-derived instead of manually listing every table.
The registry rejects:

- Empty table names or zero table IDs.
- Reused table IDs.
- Duplicate family IDs or index IDs inside one table.
- Key layouts that do not match the table's primary or index key functions.
- Invalid schema descriptors according to `schema.ValidateTable`.

Table IDs can live next to the table spec in `table_<name>.go`. `types.go` can
keep public IDs for legacy tables, but new table IDs do not need another central
edit unless they are part of a public compatibility surface.

The generic `TableSpec[R]` registers two views:

- A typed `Table[R]` handle used by table wrappers.
- An untyped descriptor projection stored in the registry for schema listing,
  snapshot span discovery, and duplicate ID validation.

This avoids storing generic values in the registry while keeping per-table code
typed.

## Runtime API

The runtime exposes typed handles:

```go
type Table[R any] struct {
    spec TableSpec[R]
}

func (t Table[R]) Get(ctx context.Context, s *Shard, pk KeyParts) (R, bool, error)
func (t Table[R]) Create(ctx context.Context, s *Shard, row R) error
func (t Table[R]) Update(ctx context.Context, s *Shard, row R) error
func (t Table[R]) Upsert(ctx context.Context, s *Shard, row R) error
func (t Table[R]) Delete(ctx context.Context, s *Shard, pk KeyParts) error
func (t Table[R]) ScanPrimary(ctx context.Context, s *Shard, after KeyParts, limit int) ([]R, KeyParts, bool, error)
func (t Table[R]) ScanIndex(ctx context.Context, s *Shard, indexID uint16, prefix KeyParts, after KeyParts, limit int) ([]R, KeyParts, bool, error)
```

The public table APIs stay explicit and domain-oriented:

```go
func (s *Shard) UpsertFoo(ctx context.Context, row Foo) error {
    return fooTable.Upsert(ctx, s, row)
}

func (s *Shard) GetFoo(ctx context.Context, id string) (Foo, bool, error) {
    return fooTable.Get(ctx, s, KeyParts{String(id)})
}
```

This keeps callers unaware of the table runtime while still avoiding repeated
storage boilerplate.

`Update` requires an existing primary row and returns `ErrNotFound` if the row is
missing. This preserves APIs such as `UpdateUser` without needing a get-then-set
race outside the table lock.

## Key Encoding

The runtime keeps the existing durable key prefix model:

```text
meta / hash-slot / row(tableID) / primary-key-parts... / familyID
meta / hash-slot / index(tableID,indexID) / index-key-parts... / primary-key-parts...
```

`KeyParts` is a small explicit key DSL:

```go
type KeyParts []KeyPart

type KeyPart struct {
    Kind KeyPartKind
    S    string
    I64  int64
    U64  uint64
    U8   uint8
}
```

Supported constructors in the first version:

- `String(value string)`
- `Int64Ordered(value int64)`
- `Int64Desc(value int64)`
- `Uint64(value uint64)`
- `Uint8(value uint8)`

Each primary key and index key also declares a `KeyLayout`:

```go
type KeyLayout []KeyPartKind
```

The layout is required because index scans must decode bytes back into
`KeyParts`. Constructors define values for encoding; layouts define how to parse
keys during scans.

The runtime is responsible for:

- Encoding primary row keys.
- Decoding primary row key suffixes.
- Building primary prefix spans.
- Encoding index keys.
- Decoding primary key suffixes from index keys.
- Building index prefix spans.

New tables should not add custom `encodeFooRowKey` or
`encodeFooIndexKey` helpers unless they have a special non-table record layout.

Physical secondary index keys always append the primary key after the index key
parts:

```text
index(tableID,indexID) / index-key-parts... / primary-key-parts...
```

For non-unique indexes, this naturally gives stable ordering and duplicate index
keys. For unique indexes, the runtime scans the unique index prefix without the
primary suffix and rejects the write if it finds a different primary key.

## Value Encoding

The runtime does not mandate one value codec.

Each table spec provides:

```go
EncodeValue func(R) ([]byte, error)
DecodeValue func(primary KeyParts, value []byte) (R, error)
```

Rules:

- Existing migrated tables can keep their current binary value format.
- New tables should prefer `rowcodec.Writer` and `rowcodec.Scanner` when they
  need optional or future-extensible fields.
- Value decode should validate that no unexpected trailing bytes remain for
  hand-written binary formats.
- Primary key fields can be reconstructed from the decoded primary key instead
  of repeated in the value, unless self-description is intentionally needed.

This avoids forced durable format churn while still making new tables easier to
extend.

## Index Maintenance

Common upsert flow:

```text
1. Validate the row.
2. Compute the primary key.
3. Lock the hash-slot.
4. Read the old row by primary key.
5. If an old row exists, delete its old index entries.
6. Write the primary row.
7. Write the new index entries.
8. Commit synchronously.
```

Common delete flow:

```text
1. Lock the hash-slot.
2. Read the old row by primary key.
3. If it exists, delete its old index entries.
4. Delete the primary row.
5. Commit synchronously.
```

Index scan flow:

```text
1. Scan the index prefix.
2. Decode the primary key suffix from each index key.
3. Read the primary row.
4. Recompute the requested index key from the row.
5. Return the row only if it still matches the scanned index entry.
```

The verification step makes stale secondary indexes safe by default. Existing
conversation active scans already use this idea; the runtime makes it the
standard behavior for new tables.

`IndexSpec.Key(row) (KeyParts, bool)` supports conditional indexes:

- `true`: write the index.
- `false`: omit the index for this row.

`Unique=true` checks that the same index key does not point at a different
primary key before writing.

Covering index values are out of scope for the first version.

## Batch Integration

Keep the existing `Batch` object, hash-slot ordering, and guard overlays. Add
typed staging helpers on table handles:

```go
func (t Table[R]) StageCreate(b *Batch, hashSlot HashSlot, row R) error
func (t Table[R]) StageUpdate(b *Batch, hashSlot HashSlot, row R) error
func (t Table[R]) StageUpsert(b *Batch, hashSlot HashSlot, row R) error
func (t Table[R]) StageDelete(b *Batch, hashSlot HashSlot, pk KeyParts) error
```

These helpers internally use the existing `b.addOp` mechanism. New table
batch APIs can be thin wrappers:

```go
func (b *Batch) UpsertFoo(hashSlot HashSlot, row Foo) error {
    return fooTable.StageUpsert(b, hashSlot, row)
}
```

The first version should use runtime batch staging for ordinary tables only:

- `user`
- `device`
- `plugin_binding`
- newly added simple metadata tables

Runtime batch staging keeps a per-table primary-key overlay for staged writes and
deletes. This lets `StageCreate` reject duplicate creates in the same batch,
`StageUpdate` see earlier staged rows, and index maintenance use the latest
staged value instead of only committed storage.

Keep custom batch code for tables with cache, guard, monotonic, or multi-record
semantics:

- `channel`
- `subscriber`
- `channel_runtime_meta`
- `channel_migration`
- `hashslot_migration`

## Snapshot And Hash-Slot Delete

The registry makes new tables participate in hash-slot data operations by
default.

Snapshot/delete changes:

- `Tables()` comes from the registry.
- Row replacement spans are derived from registered table IDs.
- Index and system spans can stay hash-slot-wide as they are today.
- Migration preservation becomes a table option instead of a hard-coded table
  list:

```go
SnapshotPolicy: SnapshotPolicy{
    PreserveOnImport: true,
}
```

`TableIDHashSlotMigration` uses `PreserveOnImport` so preserving imports keep
local migration metadata. New tables default to normal snapshot replacement.

## Migration Plan

### Step 1: Runtime Skeleton

- Add registry, table runtime, and key helpers.
- Convert `Tables()` to registry-backed descriptors.
- Convert snapshot/delete row span discovery to the registry.
- Do not migrate table CRUD yet.
- Run `go test ./pkg/db/...`.

### Step 2: Migrate Simple Primary Tables

- Migrate `user`.
- Migrate `device`.
- Keep public `Shard` and compatibility API behavior unchanged.
- Prove create, upsert, update, get, delete, primary page scan, and batch
  staging.

### Step 3: Migrate One Indexed Table

- Migrate `plugin_binding`.
- Prove automatic index maintenance.
- Prove index scan pagination.
- Prove stale index verification.

### Step 4: Default New Tables To Runtime

- New ordinary metadata tables use the runtime by default.
- Complex tables can combine custom logic with runtime primary/index helpers
  when useful.
- Defer migration of complex state machine tables until there is a concrete
  benefit.

## Testing

Add runtime-level tests:

- Registry validation:
  - Table ID uniqueness.
  - Family and index descriptor validity.
  - `Tables()` includes all registered tables.
- Key codec:
  - Stable ordering for each supported key part kind.
  - Prefix spans include expected keys and exclude adjacent keys.
  - Primary key suffix decode round-trips.
- CRUD:
  - Create rejects duplicates.
  - Upsert creates and updates rows.
  - Delete removes rows and index entries.
  - Primary scans page correctly.
- Indexes:
  - Index entries are written on create.
  - Old index entries are removed on update.
  - Conditional indexes can be omitted.
  - Stale index entries are skipped.
  - Unique indexes reject conflicting primary rows.
- Batch:
  - Staged create/upsert/delete commit atomically.
  - Multi-hash-slot lock order remains sorted.
  - Closed batches reject new operations.
- Snapshot:
  - Registered tables are exported, imported, and deleted automatically.
  - Preserve-on-import only preserves tables with the policy flag.

Keep existing table tests for behavior regression. Do not duplicate all runtime
CRUD cases in each migrated table test.

## Compatibility

The migration preserves public APIs:

- `MetaDB`
- `Shard`
- compatibility `DB`
- `ShardStore`
- `WriteBatch`

Existing callers should not need to know whether a table uses the runtime.
Compatibility methods only change when a table exposes a legacy facade method
that must call a new runtime-backed typed wrapper.

## Risks

- Generic runtime bugs can affect every migrated table.
  - Mitigation: migrate only simple tables first and keep broad runtime tests.
- Unique index conflict checks can add reads.
  - Mitigation: only enable `Unique` where required.
- Batch read-your-writes overlays may become more complex if generalized too
  early.
  - Mitigation: first support ordinary staged writes; keep guard-heavy tables
    custom.
- Mixed old and runtime table styles may temporarily look inconsistent.
  - Mitigation: document that the runtime is the default path for new ordinary
    tables, while complex existing tables migrate only when useful.

## Expected New Table Workflow

For a normal new metadata table:

1. Add `pkg/db/meta/table_foo.go`.
2. Define `Foo`, `TableIDFoo`, `fooTable`, value codec, validation, and typed
   `Shard` methods.
3. Add `pkg/db/meta/table_foo_test.go`.
4. Run `go test ./pkg/db/meta`.

No default changes should be needed in central key helpers, schema listing,
snapshot, delete, or batch internals.
