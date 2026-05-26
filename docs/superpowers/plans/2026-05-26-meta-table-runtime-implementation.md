# Meta Table Runtime Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `pkg/db/meta` table runtime so ordinary metadata tables can be added from one table file while preserving existing public APIs and storage semantics.

**Architecture:** Add a meta-local registry, typed key codec, and generic `Table[R]` runtime for common primary CRUD, secondary index maintenance, batch staging, and registry-derived snapshot spans. Migrate `user`, `device`, and `plugin_binding` onto the runtime first; keep complex cache/guard/state-machine tables custom.

**Tech Stack:** Go, `pkg/db/internal/engine`, `pkg/db/internal/keycodec`, `pkg/db/internal/schema`, existing `pkg/db/meta` shard and batch primitives.

---

## Starting Constraints

- Worktree may contain unrelated user edits. Before each task, run `git status --short` and do not touch unrelated dirty files.
- Follow `AGENTS.md`: if `pkg/db/meta/FLOW.md` becomes stale, update it in the same task that changes the flow.
- Storage code in `pkg/db/meta` must not import Pebble directly; use `pkg/db/internal/engine` only.
- Keep single-node wording out of storage changes unless needed; this plan does not touch deployment semantics.
- Use TDD: write or update failing tests first, run the narrow test, implement minimal code, run the narrow test again, then commit.

## File Structure

### New Files

- `pkg/db/meta/table_registry.go`
  - Owns `SnapshotPolicy`, `metaTableRegistry`, descriptor registration, registry-derived `Tables()`, table ID validation, and row span discovery helpers.
- `pkg/db/meta/table_registry_test.go`
  - Tests descriptor registration, duplicate IDs, invalid descriptors, snapshot policy, and `Tables()` ordering.
- `pkg/db/meta/table_key.go`
  - Owns `KeyPartKind`, `KeyPart`, `KeyParts`, `KeyLayout`, key constructors, encode/decode helpers, primary/index key builders.
- `pkg/db/meta/table_key_test.go`
  - Tests key ordering, round trips, prefix spans, and decode failure handling.
- `pkg/db/meta/table_runtime.go`
  - Owns `TableSpec[R]`, `Table[R]`, common CRUD, primary scan, index scan, unique index checks, and batch staging helpers.
- `pkg/db/meta/table_runtime_test.go`
  - Uses a test-only runtime table to prove CRUD, index maintenance, stale-index skip, unique conflicts, and batch staging.

### Modified Files

- `pkg/db/meta/schema.go`
  - Replace manual `Tables()` implementation with registry-backed `Tables()`.
  - Keep existing table descriptor variables until migrated.
  - Register non-migrated existing descriptors in `init()`.
- `pkg/db/meta/snapshot.go`
  - Replace hard-coded `Tables()` row-span logic and `TableIDHashSlotMigration` preservation branch with registry helpers/policy.
- `pkg/db/meta/batch.go`
  - Add generic runtime row overlays in `batchCommitState`.
  - Replace `Batch.CreateUser` and `Batch.UpsertUser` with runtime staging once `user` migrates.
  - Add `Batch.UpsertDevice` if not already public on `Batch`; compatibility can call runtime or keep existing behavior where semantics differ.
- `pkg/db/meta/table_user.go`
  - Register `userTable` with `TableSpec[User]`.
  - Convert `Shard` CRUD/page methods to thin runtime wrappers.
  - Keep `encodeUserValue` / `decodeUserValue` durable format unchanged.
- `pkg/db/meta/table_device.go`
  - Register `deviceTable` with `TableSpec[Device]`.
  - Convert `Shard.UpsertDevice` and `Shard.GetDevice` to runtime wrappers.
  - Keep durable value format unchanged.
- `pkg/db/meta/table_plugin_binding.go`
  - Register `pluginBindingTable` with a plugin-number secondary index.
  - Convert bind/unbind/list/scan/exist methods to runtime wrappers where possible.
  - Keep durable value format unchanged.
- `pkg/db/meta/compat.go`
  - Route compatibility methods to migrated typed wrappers only where behavior is unchanged.
  - Preserve legacy `WriteBatch.CreateUser` idempotent behavior if it differs from strict `Batch.CreateUser`.
- `pkg/db/meta/schema_test.go`
  - Keep existing schema validation and add assertions that registry-backed `Tables()` still includes all legacy tables.
- `pkg/db/meta/user_test.go`
  - Keep existing behavior tests; add focused tests only for behavior changed by runtime migration if needed.
- `pkg/db/meta/device_test.go`
  - Keep existing hash-slot scoped behavior tests.
- `pkg/db/meta/plugin_binding_test.go`
  - Keep existing behavior tests; add a stale-index skip test through runtime if not already covered globally.
- `pkg/db/meta/snapshot_test.go`
  - Add or adjust assertions proving registered tables are included automatically and migration preserve policy still works.
- `pkg/db/meta/batch_test.go`
  - Add runtime-backed user/device staging tests and preserve lock-order/rollback tests.
- `pkg/db/meta/FLOW.md`
  - Document registry/runtime flow and the rule that ordinary new tables use `TableSpec` by default.
- `docs/development/PROJECT_KNOWLEDGE.md`
  - Add one short storage note if useful: ordinary new `pkg/db/meta` tables should use the meta table runtime.

---

### Task 1: Add Registry Skeleton And Snapshot Policy

**Files:**
- Create: `pkg/db/meta/table_registry.go`
- Create: `pkg/db/meta/table_registry_test.go`
- Modify: `pkg/db/meta/schema.go`
- Modify: `pkg/db/meta/schema_test.go`

- [ ] **Step 1: Write failing registry tests**

Add `pkg/db/meta/table_registry_test.go`:

```go
package meta

import (
    "strings"
    "testing"

    "github.com/WuKongIM/WuKongIM/pkg/db/internal/schema"
)

func TestMetaTableRegistryRejectsDuplicateTableID(t *testing.T) {
    registry := newMetaTableRegistry()
    table := simpleMetaTable(65001, "test_duplicate")
    if err := registry.register(metaTableDescriptor{Table: table}); err != nil {
        t.Fatalf("first register: %v", err)
    }
    err := registry.register(metaTableDescriptor{Table: table})
    if err == nil || !strings.Contains(err.Error(), "duplicate") {
        t.Fatalf("second register err = %v, want duplicate error", err)
    }
}

func TestMetaTableRegistryTablesAreSortedAndCopied(t *testing.T) {
    registry := newMetaTableRegistry()
    if err := registry.register(metaTableDescriptor{Table: simpleMetaTable(65002, "z_table")}); err != nil {
        t.Fatalf("register z: %v", err)
    }
    if err := registry.register(metaTableDescriptor{Table: simpleMetaTable(65001, "a_table")}); err != nil {
        t.Fatalf("register a: %v", err)
    }

    tables := registry.tables()
    if len(tables) != 2 || tables[0].ID != 65001 || tables[1].ID != 65002 {
        t.Fatalf("tables order = %#v", tables)
    }
    tables[0].Name = "mutated"
    again := registry.tables()
    if again[0].Name != "a_table" {
        t.Fatalf("registry returned mutable table copy: %q", again[0].Name)
    }
}

func TestMetaTableRegistryPreservePolicy(t *testing.T) {
    registry := newMetaTableRegistry()
    table := simpleMetaTable(65003, "preserved")
    if err := registry.register(metaTableDescriptor{Table: table, SnapshotPolicy: SnapshotPolicy{PreserveOnImport: true}}); err != nil {
        t.Fatalf("register: %v", err)
    }
    descriptor, ok := registry.lookup(65003)
    if !ok || !descriptor.SnapshotPolicy.PreserveOnImport {
        t.Fatalf("preserve policy = %#v ok=%v", descriptor.SnapshotPolicy, ok)
    }
}

func TestMetaTableRegistryRejectsInvalidSchema(t *testing.T) {
    registry := newMetaTableRegistry()
    err := registry.register(metaTableDescriptor{Table: schema.Table{ID: 65004, Name: "invalid"}})
    if err == nil {
        t.Fatal("register invalid schema succeeded")
    }
}
```

Extend `pkg/db/meta/schema_test.go` after the existing `seen` assertions:

```go
if len(tables) != len(defaultMetaRegistry.tables()) {
    t.Fatalf("Tables() length = %d, registry length = %d", len(tables), len(defaultMetaRegistry.tables()))
}
```

- [ ] **Step 2: Run failing tests**

Run: `go test ./pkg/db/meta -run 'TestMetaTableRegistry|TestMetaSchemaValidateAllTables' -count=1`

Expected: FAIL because `newMetaTableRegistry`, `metaTableDescriptor`, `SnapshotPolicy`, and `defaultMetaRegistry` are undefined.

- [ ] **Step 3: Implement registry skeleton**

Create `pkg/db/meta/table_registry.go`:

```go
package meta

import (
    "fmt"
    "sort"

    "github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
    "github.com/WuKongIM/WuKongIM/pkg/db/internal/schema"
)

// SnapshotPolicy controls table behavior during hash-slot snapshot import.
type SnapshotPolicy struct {
    // PreserveOnImport keeps existing local rows when importing preserving snapshots.
    PreserveOnImport bool
}

type metaTableDescriptor struct {
    Table          schema.Table
    SnapshotPolicy SnapshotPolicy
}

type metaTableRegistry struct {
    byID map[uint32]metaTableDescriptor
}

var defaultMetaRegistry = newMetaTableRegistry()

func newMetaTableRegistry() *metaTableRegistry {
    return &metaTableRegistry{byID: make(map[uint32]metaTableDescriptor)}
}

func (r *metaTableRegistry) register(descriptor metaTableDescriptor) error {
    if r == nil {
        return fmt.Errorf("%w: nil meta table registry", dberrors.ErrInvalidArgument)
    }
    if err := schema.ValidateTable(descriptor.Table); err != nil {
        return err
    }
    if _, ok := r.byID[descriptor.Table.ID]; ok {
        return fmt.Errorf("%w: duplicate meta table id %d", dberrors.ErrInvalidArgument, descriptor.Table.ID)
    }
    r.byID[descriptor.Table.ID] = cloneMetaTableDescriptor(descriptor)
    return nil
}

func (r *metaTableRegistry) mustRegister(descriptor metaTableDescriptor) {
    if err := r.register(descriptor); err != nil {
        panic(err)
    }
}

func (r *metaTableRegistry) lookup(tableID uint32) (metaTableDescriptor, bool) {
    if r == nil {
        return metaTableDescriptor{}, false
    }
    descriptor, ok := r.byID[tableID]
    if !ok {
        return metaTableDescriptor{}, false
    }
    return cloneMetaTableDescriptor(descriptor), true
}

func (r *metaTableRegistry) tables() []schema.Table {
    if r == nil || len(r.byID) == 0 {
        return nil
    }
    ids := make([]uint32, 0, len(r.byID))
    for id := range r.byID {
        ids = append(ids, id)
    }
    sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
    tables := make([]schema.Table, 0, len(ids))
    for _, id := range ids {
        tables = append(tables, cloneSchemaTable(r.byID[id].Table))
    }
    return tables
}

func cloneMetaTableDescriptor(descriptor metaTableDescriptor) metaTableDescriptor {
    descriptor.Table = cloneSchemaTable(descriptor.Table)
    return descriptor
}

func cloneSchemaTable(table schema.Table) schema.Table {
    table.Columns = append([]schema.Column(nil), table.Columns...)
    table.Families = append([]schema.Family(nil), table.Families...)
    for i := range table.Families {
        table.Families[i].Columns = append([]uint16(nil), table.Families[i].Columns...)
    }
    table.Primary.Columns = append([]uint16(nil), table.Primary.Columns...)
    table.Primary.Covering = append([]uint16(nil), table.Primary.Covering...)
    table.Indexes = append([]schema.Index(nil), table.Indexes...)
    for i := range table.Indexes {
        table.Indexes[i].Columns = append([]uint16(nil), table.Indexes[i].Columns...)
        table.Indexes[i].Covering = append([]uint16(nil), table.Indexes[i].Covering...)
    }
    return table
}
```

Modify `pkg/db/meta/schema.go`:

```go
// Tables returns every metadata table descriptor.
func Tables() []schema.Table {
    return defaultMetaRegistry.tables()
}
```

Add an `init` near the descriptor declarations in `schema.go`:

```go
func init() {
    for _, descriptor := range []metaTableDescriptor{
        {Table: UserTable},
        {Table: DeviceTable},
        {Table: ChannelTable},
        {Table: SubscriberTable},
        {Table: ChannelRuntimeMetaTable},
        {Table: ConversationTable},
        {Table: CMDConversationTable},
        {Table: PluginBindingTable},
        {Table: ChannelMigrationTable},
        {Table: HashSlotMigrationTable, SnapshotPolicy: SnapshotPolicy{PreserveOnImport: true}},
    } {
        defaultMetaRegistry.mustRegister(descriptor)
    }
}
```

- [ ] **Step 4: Run registry/schema tests**

Run: `go test ./pkg/db/meta -run 'TestMetaTableRegistry|TestMetaSchemaValidateAllTables' -count=1`

Expected: PASS.

- [ ] **Step 5: Run package tests**

Run: `go test ./pkg/db/meta -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/db/meta/table_registry.go pkg/db/meta/table_registry_test.go pkg/db/meta/schema.go pkg/db/meta/schema_test.go
git commit -m "feat(db): add meta table registry"
```

---

### Task 2: Route Snapshot Row Spans Through Registry

**Files:**
- Modify: `pkg/db/meta/table_registry.go`
- Modify: `pkg/db/meta/snapshot.go`
- Modify: `pkg/db/meta/snapshot_test.go`

- [ ] **Step 1: Write failing snapshot policy test**

Add to `pkg/db/meta/snapshot_test.go`:

```go
func TestSnapshotReplaceSpansUseRegistryPolicies(t *testing.T) {
    store := openTestMetaStore(t)
    defer store.close(t)

    spans := hashSlotSnapshotReplaceSpans(9, true)
    for _, span := range spans {
        if bytesInSpan(encodeHashSlotMigrationStateKey(9), span) {
            t.Fatalf("preserving replace spans include hash-slot migration key span: %#v", span)
        }
    }

    foundUser := false
    userKey := encodeUserRowKey(9, "u1", userPrimaryFamilyID)
    for _, span := range spans {
        if bytesInSpan(userKey, span) {
            foundUser = true
            break
        }
    }
    if !foundUser {
        t.Fatalf("preserving replace spans did not include user row span")
    }
}
```

If `bytesInSpan` is already available in `snapshot.go`, keep this test in package `meta` so it can call it.

- [ ] **Step 2: Run failing snapshot test**

Run: `go test ./pkg/db/meta -run TestSnapshotReplaceSpansUseRegistryPolicies -count=1`

Expected: FAIL until row span policy helpers exist or hard-coded behavior is replaced.

- [ ] **Step 3: Add registry span helpers**

In `pkg/db/meta/table_registry.go` add:

```go
func (r *metaTableRegistry) rowTablesForSnapshot(preserve bool) []schema.Table {
    if r == nil {
        return nil
    }
    ids := make([]uint32, 0, len(r.byID))
    for id, descriptor := range r.byID {
        if preserve && descriptor.SnapshotPolicy.PreserveOnImport {
            continue
        }
        ids = append(ids, id)
    }
    sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
    tables := make([]schema.Table, 0, len(ids))
    for _, id := range ids {
        tables = append(tables, cloneSchemaTable(r.byID[id].Table))
    }
    return tables
}
```

- [ ] **Step 4: Replace hard-coded snapshot span logic**

Modify `pkg/db/meta/snapshot.go`:

```go
func hashSlotSnapshotReplaceSpans(hashSlot HashSlot, preserveMigrationMeta bool) []Span {
    if !preserveMigrationMeta {
        return hashSlotAllDataSpans(hashSlot)
    }
    tables := defaultMetaRegistry.rowTablesForSnapshot(true)
    spans := make([]Span, 0, len(tables)+2)
    for _, table := range tables {
        spans = append(spans, prefixSpan(encodeRowPrefix(hashSlot, table.ID)))
    }
    spans = append(spans, hashSlotIndexSpan(hashSlot), hashSlotSystemSpan(hashSlot))
    return spans
}
```

Keep `isHashSlotMigrationSnapshotKey` unchanged for now; it still protects local migration rows during preserving import. This task only removes table ID hard-coding from replacement span enumeration.

- [ ] **Step 5: Run snapshot tests**

Run: `go test ./pkg/db/meta -run 'TestSnapshot|TestMetaTableRegistry' -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/db/meta/table_registry.go pkg/db/meta/snapshot.go pkg/db/meta/snapshot_test.go
git commit -m "feat(db): derive meta snapshot spans from registry"
```

---

### Task 3: Add KeyParts Codec

**Files:**
- Create: `pkg/db/meta/table_key.go`
- Create: `pkg/db/meta/table_key_test.go`

- [ ] **Step 1: Write failing key codec tests**

Create `pkg/db/meta/table_key_test.go`:

```go
package meta

import (
    "bytes"
    "testing"

    "github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
)

func TestTableKeyPartsRoundTrip(t *testing.T) {
    parts := KeyParts{String("u1"), Int64Ordered(-7), Int64Desc(99), Uint64(42), Uint8(3)}
    layout := KeyLayout{KeyString, KeyInt64Ordered, KeyInt64Desc, KeyUint64, KeyUint8}

    encoded, err := encodeKeyParts(nil, parts)
    if err != nil {
        t.Fatalf("encodeKeyParts(): %v", err)
    }
    decoded, rest, err := decodeKeyParts(encoded, layout)
    if err != nil {
        t.Fatalf("decodeKeyParts(): %v", err)
    }
    if len(rest) != 0 {
        t.Fatalf("rest len = %d", len(rest))
    }
    if !decoded.Equal(parts) {
        t.Fatalf("decoded = %#v, want %#v", decoded, parts)
    }
}

func TestTableKeyPartOrdering(t *testing.T) {
    older, err := encodeKeyParts(nil, KeyParts{Int64Desc(100)})
    if err != nil {
        t.Fatalf("encode older: %v", err)
    }
    newer, err := encodeKeyParts(nil, KeyParts{Int64Desc(200)})
    if err != nil {
        t.Fatalf("encode newer: %v", err)
    }
    if bytes.Compare(newer, older) >= 0 {
        t.Fatalf("desc order failed: newer=%x older=%x", newer, older)
    }
}

func TestTablePrimaryAndIndexKeysKeepExistingPrefixes(t *testing.T) {
    primary, err := encodeTablePrimaryRowKey(7, 65010, KeyParts{String("u1")}, 0)
    if err != nil {
        t.Fatalf("primary key: %v", err)
    }
    if !bytes.HasPrefix(primary, encodeRowPrefix(7, 65010)) {
        t.Fatalf("primary key %x does not use row prefix %x", primary, encodeRowPrefix(7, 65010))
    }

    index, err := encodeTableIndexKey(7, 65010, 2, KeyParts{String("owner")}, KeyParts{String("u1")})
    if err != nil {
        t.Fatalf("index key: %v", err)
    }
    if !bytes.HasPrefix(index, encodeIndexPrefix(7, 65010, 2)) {
        t.Fatalf("index key %x does not use index prefix %x", index, encodeIndexPrefix(7, 65010, 2))
    }
}

func TestDecodeTablePrimaryKeyRejectsWrongFamily(t *testing.T) {
    key, err := encodeTablePrimaryRowKey(7, 65010, KeyParts{String("u1")}, 1)
    if err != nil {
        t.Fatalf("primary key: %v", err)
    }
    _, ok := decodeTablePrimaryRowKey(encodeRowPrefix(7, 65010), key, KeyLayout{KeyString}, 0)
    if ok {
        t.Fatal("decoded key with wrong family")
    }
}

func TestTableIndexPrefixSpan(t *testing.T) {
    prefix, err := encodeTableIndexScanPrefix(7, 65010, 2, KeyParts{String("owner")})
    if err != nil {
        t.Fatalf("prefix: %v", err)
    }
    span := keycodec.NewPrefixSpan(prefix)
    key, err := encodeTableIndexKey(7, 65010, 2, KeyParts{String("owner"), String("u1")}, KeyParts{String("u1")})
    if err != nil {
        t.Fatalf("index key: %v", err)
    }
    if !bytesInSpan(key, Span{Start: span.Start, End: span.End}) {
        t.Fatalf("key %x not in prefix span %#v", key, span)
    }
}
```

- [ ] **Step 2: Run failing key tests**

Run: `go test ./pkg/db/meta -run TestTableKey -count=1`

Expected: FAIL because key types and helpers are undefined.

- [ ] **Step 3: Implement key codec**

Create `pkg/db/meta/table_key.go`:

```go
package meta

import (
    "bytes"
    "encoding/binary"
    "fmt"

    "github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
    "github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
)

// KeyPartKind identifies one encoded table key component.
type KeyPartKind uint8

const (
    KeyString KeyPartKind = iota + 1
    KeyInt64Ordered
    KeyInt64Desc
    KeyUint64
    KeyUint8
)

// KeyLayout describes how to decode ordered key bytes back into KeyParts.
type KeyLayout []KeyPartKind

// KeyParts is the logical key used by meta table primary keys and indexes.
type KeyParts []KeyPart

// KeyPart stores one typed ordered key component.
type KeyPart struct {
    Kind KeyPartKind
    S    string
    I64  int64
    U64  uint64
    U8   uint8
}

func String(value string) KeyPart { return KeyPart{Kind: KeyString, S: value} }
func Int64Ordered(value int64) KeyPart { return KeyPart{Kind: KeyInt64Ordered, I64: value} }
func Int64Desc(value int64) KeyPart { return KeyPart{Kind: KeyInt64Desc, I64: value} }
func Uint64(value uint64) KeyPart { return KeyPart{Kind: KeyUint64, U64: value} }
func Uint8(value uint8) KeyPart { return KeyPart{Kind: KeyUint8, U8: value} }

func (parts KeyParts) Equal(other KeyParts) bool {
    if len(parts) != len(other) {
        return false
    }
    for i := range parts {
        if parts[i] != other[i] {
            return false
        }
    }
    return true
}

func encodeKeyParts(dst []byte, parts KeyParts) ([]byte, error) {
    for _, part := range parts {
        switch part.Kind {
        case KeyString:
            if len(part.S) > maxKeyStringLen {
                return nil, dberrors.ErrInvalidArgument
            }
            dst = keycodec.AppendString(dst, part.S)
        case KeyInt64Ordered:
            dst = keycodec.AppendInt64Ordered(dst, part.I64)
        case KeyInt64Desc:
            dst = keycodec.AppendInt64Desc(dst, part.I64)
        case KeyUint64:
            dst = keycodec.AppendUint64(dst, part.U64)
        case KeyUint8:
            dst = append(dst, part.U8)
        default:
            return nil, fmt.Errorf("%w: unknown key part kind %d", dberrors.ErrInvalidArgument, part.Kind)
        }
    }
    return dst, nil
}

func decodeKeyParts(src []byte, layout KeyLayout) (KeyParts, []byte, error) {
    parts := make(KeyParts, 0, len(layout))
    rest := src
    for _, kind := range layout {
        switch kind {
        case KeyString:
            value, next, err := keycodec.ReadString(rest)
            if err != nil {
                return nil, nil, dberrors.ErrCorruptValue
            }
            parts = append(parts, String(value))
            rest = next
        case KeyInt64Ordered:
            if len(rest) < 8 {
                return nil, nil, dberrors.ErrCorruptValue
            }
            ordered := binary.BigEndian.Uint64(rest[:8])
            parts = append(parts, Int64Ordered(int64(ordered^(uint64(1)<<63))))
            rest = rest[8:]
        case KeyInt64Desc:
            if len(rest) < 8 {
                return nil, nil, dberrors.ErrCorruptValue
            }
            ordered := ^binary.BigEndian.Uint64(rest[:8])
            parts = append(parts, Int64Desc(int64(ordered^(uint64(1)<<63))))
            rest = rest[8:]
        case KeyUint64:
            if len(rest) < 8 {
                return nil, nil, dberrors.ErrCorruptValue
            }
            parts = append(parts, Uint64(binary.BigEndian.Uint64(rest[:8])))
            rest = rest[8:]
        case KeyUint8:
            if len(rest) < 1 {
                return nil, nil, dberrors.ErrCorruptValue
            }
            parts = append(parts, Uint8(rest[0]))
            rest = rest[1:]
        default:
            return nil, nil, fmt.Errorf("%w: unknown key layout kind %d", dberrors.ErrInvalidArgument, kind)
        }
    }
    return parts, rest, nil
}

func encodeTablePrimaryRowKey(hashSlot HashSlot, tableID uint32, primary KeyParts, familyID uint16) ([]byte, error) {
    key := encodeRowPrefix(hashSlot, tableID)
    var err error
    key, err = encodeKeyParts(key, primary)
    if err != nil {
        return nil, err
    }
    return keycodec.AppendUint16(key, familyID), nil
}

func decodeTablePrimaryRowKey(prefix []byte, key []byte, layout KeyLayout, familyID uint16) (KeyParts, bool) {
    if !bytes.HasPrefix(key, prefix) {
        return nil, false
    }
    parts, rest, err := decodeKeyParts(key[len(prefix):], layout)
    if err != nil || len(rest) != 2 || binary.BigEndian.Uint16(rest) != familyID {
        return nil, false
    }
    return parts, true
}

func encodeTableIndexScanPrefix(hashSlot HashSlot, tableID uint32, indexID uint16, prefixParts KeyParts) ([]byte, error) {
    key := encodeIndexPrefix(hashSlot, tableID, indexID)
    return encodeKeyParts(key, prefixParts)
}

func encodeTableIndexKey(hashSlot HashSlot, tableID uint32, indexID uint16, indexParts KeyParts, primary KeyParts) ([]byte, error) {
    key, err := encodeTableIndexScanPrefix(hashSlot, tableID, indexID, indexParts)
    if err != nil {
        return nil, err
    }
    return encodeKeyParts(key, primary)
}
```

- [ ] **Step 4: Run key tests**

Run: `go test ./pkg/db/meta -run TestTableKey -count=1`

Expected: PASS.

- [ ] **Step 5: Run package tests**

Run: `go test ./pkg/db/meta -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/db/meta/table_key.go pkg/db/meta/table_key_test.go
git commit -m "feat(db): add meta table key codec"
```

---

### Task 4: Add Typed TableSpec Registration

**Files:**
- Modify: `pkg/db/meta/table_registry.go`
- Create: `pkg/db/meta/table_runtime.go`
- Modify: `pkg/db/meta/table_registry_test.go`

- [ ] **Step 1: Write failing typed spec tests**

Append to `pkg/db/meta/table_registry_test.go`:

```go
type registryTestRow struct {
    ID    string
    Owner string
}

func TestRegisterMetaTableBuildsSchemaDescriptor(t *testing.T) {
    registry := newMetaTableRegistry()
    table, err := registerMetaTableInRegistry(registry, TableSpec[registryTestRow]{
        ID:   65020,
        Name: "registry_test",
        Columns: []schema.Column{
            {ID: 1, Name: "id", Type: schema.TypeString, Required: true},
            {ID: 2, Name: "owner", Type: schema.TypeString},
            {ID: 3, Name: "value", Type: schema.TypeBytes},
        },
        Families: []schema.Family{{ID: 0, Name: "primary", Columns: []uint16{3}}},
        Primary: PrimarySpec[registryTestRow]{
            IndexID: 1,
            FamilyID: 0,
            Name: "pk_registry_test",
            Columns: []uint16{1},
            Layout: KeyLayout{KeyString},
            Key: func(row registryTestRow) KeyParts { return KeyParts{String(row.ID)} },
        },
        Indexes: []IndexSpec[registryTestRow]{
            {
                ID: 2,
                Name: "idx_registry_test_owner",
                Columns: []uint16{2, 1},
                Layout: KeyLayout{KeyString, KeyString},
                Key: func(row registryTestRow) (KeyParts, bool) {
                    return KeyParts{String(row.Owner), String(row.ID)}, row.Owner != ""
                },
            },
        },
        EncodeValue: func(row registryTestRow) ([]byte, error) { return []byte(row.Owner), nil },
        DecodeValue: func(primary KeyParts, value []byte) (registryTestRow, error) {
            return registryTestRow{ID: primary[0].S, Owner: string(value)}, nil
        },
    })
    if err != nil {
        t.Fatalf("registerMetaTableInRegistry(): %v", err)
    }
    if table.Schema().Name != "registry_test" {
        t.Fatalf("schema name = %q", table.Schema().Name)
    }
    tables := registry.tables()
    if len(tables) != 1 || tables[0].Primary.Name != "pk_registry_test" || len(tables[0].Indexes) != 1 {
        t.Fatalf("registered tables = %#v", tables)
    }
}

func TestRegisterMetaTableRejectsMismatchedLayout(t *testing.T) {
    registry := newMetaTableRegistry()
    _, err := registerMetaTableInRegistry(registry, TableSpec[registryTestRow]{
        ID: 65021,
        Name: "bad_layout",
        Columns: []schema.Column{{ID: 1, Name: "id", Type: schema.TypeString, Required: true}},
        Families: []schema.Family{{ID: 0, Name: "primary"}},
        Primary: PrimarySpec[registryTestRow]{
            IndexID: 1,
            FamilyID: 0,
            Name: "pk_bad_layout",
            Columns: []uint16{1},
            Layout: KeyLayout{},
            Key: func(row registryTestRow) KeyParts { return KeyParts{String(row.ID)} },
        },
        EncodeValue: func(row registryTestRow) ([]byte, error) { return nil, nil },
        DecodeValue: func(primary KeyParts, value []byte) (registryTestRow, error) { return registryTestRow{}, nil },
    })
    if err == nil {
        t.Fatal("register with mismatched layout succeeded")
    }
}
```

- [ ] **Step 2: Run failing typed spec tests**

Run: `go test ./pkg/db/meta -run 'TestRegisterMetaTable' -count=1`

Expected: FAIL because `TableSpec`, `PrimarySpec`, `IndexSpec`, and `registerMetaTableInRegistry` are undefined.

- [ ] **Step 3: Implement typed specs and schema projection**

Create `pkg/db/meta/table_runtime.go` with the type definitions and registration helpers:

```go
package meta

import (
    "fmt"

    "github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
    "github.com/WuKongIM/WuKongIM/pkg/db/internal/schema"
)

// TableSpec describes one runtime-backed metadata table.
type TableSpec[R any] struct {
    ID   uint32
    Name string

    Columns []schema.Column
    Families []schema.Family
    Primary PrimarySpec[R]
    Indexes []IndexSpec[R]
    SnapshotPolicy SnapshotPolicy

    Validate func(R) error
    EncodeValue func(R) ([]byte, error)
    DecodeValue func(KeyParts, []byte) (R, error)
}

// PrimarySpec describes the primary key and row family for a table.
type PrimarySpec[R any] struct {
    IndexID uint16
    FamilyID uint16
    Name string
    Columns []uint16
    Layout KeyLayout
    Key func(R) KeyParts
}

// IndexSpec describes one secondary index.
type IndexSpec[R any] struct {
    ID uint16
    Name string
    Unique bool
    Columns []uint16
    Layout KeyLayout
    Key func(R) (KeyParts, bool)
}

// Table is a typed handle for common metadata table operations.
type Table[R any] struct {
    spec TableSpec[R]
    schema schema.Table
}

func registerMetaTable[R any](spec TableSpec[R]) Table[R] {
    table, err := registerMetaTableInRegistry(defaultMetaRegistry, spec)
    if err != nil {
        panic(err)
    }
    return table
}

func registerMetaTableInRegistry[R any](registry *metaTableRegistry, spec TableSpec[R]) (Table[R], error) {
    normalized, tableSchema, err := normalizeTableSpec(spec)
    if err != nil {
        return Table[R]{}, err
    }
    table := Table[R]{spec: normalized, schema: tableSchema}
    if err := registry.register(metaTableDescriptor{Table: tableSchema, SnapshotPolicy: spec.SnapshotPolicy}); err != nil {
        return Table[R]{}, err
    }
    return table, nil
}

func (t Table[R]) Schema() schema.Table {
    return cloneSchemaTable(t.schema)
}

func normalizeTableSpec[R any](spec TableSpec[R]) (TableSpec[R], schema.Table, error) {
    if spec.ID == 0 || spec.Name == "" || spec.Primary.Key == nil || spec.EncodeValue == nil || spec.DecodeValue == nil {
        return spec, schema.Table{}, fmt.Errorf("%w: incomplete table spec", dberrors.ErrInvalidArgument)
    }
    if spec.Primary.IndexID == 0 || spec.Primary.Name == "" || len(spec.Primary.Layout) != len(spec.Primary.Columns) {
        return spec, schema.Table{}, fmt.Errorf("%w: invalid primary spec", dberrors.ErrInvalidArgument)
    }
    indexes := make([]schema.Index, 0, len(spec.Indexes))
    for _, index := range spec.Indexes {
        if index.ID == 0 || index.Name == "" || index.Key == nil || len(index.Layout) != len(index.Columns) {
            return spec, schema.Table{}, fmt.Errorf("%w: invalid index spec", dberrors.ErrInvalidArgument)
        }
        indexes = append(indexes, schema.Index{ID: index.ID, Name: index.Name, Unique: index.Unique, Columns: append([]uint16(nil), index.Columns...)})
    }
    tableSchema := schema.Table{
        ID: spec.ID,
        Name: spec.Name,
        Columns: append([]schema.Column(nil), spec.Columns...),
        Families: cloneFamilies(spec.Families),
        Primary: schema.Index{ID: spec.Primary.IndexID, Name: spec.Primary.Name, Unique: true, Primary: true, Columns: append([]uint16(nil), spec.Primary.Columns...)},
        Indexes: indexes,
    }
    if err := schema.ValidateTable(tableSchema); err != nil {
        return spec, schema.Table{}, err
    }
    return spec, tableSchema, nil
}

func cloneFamilies(families []schema.Family) []schema.Family {
    out := append([]schema.Family(nil), families...)
    for i := range out {
        out[i].Columns = append([]uint16(nil), out[i].Columns...)
    }
    return out
}
```

- [ ] **Step 4: Run typed spec tests**

Run: `go test ./pkg/db/meta -run 'TestRegisterMetaTable|TestMetaTableRegistry' -count=1`

Expected: PASS.

- [ ] **Step 5: Run package tests**

Run: `go test ./pkg/db/meta -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/db/meta/table_runtime.go pkg/db/meta/table_registry.go pkg/db/meta/table_registry_test.go
git commit -m "feat(db): add typed meta table specs"
```

---

### Task 5: Implement Primary CRUD Runtime

**Files:**
- Modify: `pkg/db/meta/table_runtime.go`
- Create or modify: `pkg/db/meta/table_runtime_test.go`

- [ ] **Step 1: Write failing primary CRUD tests**

Create `pkg/db/meta/table_runtime_test.go`:

```go
package meta

import (
    "context"
    "testing"

    "github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
    "github.com/WuKongIM/WuKongIM/pkg/db/internal/schema"
)

type runtimeTestRow struct {
    ID string
    Owner string
    Value string
}

func newRuntimeTestTable(t *testing.T) Table[runtimeTestRow] {
    t.Helper()
    table, err := registerMetaTableInRegistry(newMetaTableRegistry(), TableSpec[runtimeTestRow]{
        ID: 65030,
        Name: "runtime_test",
        Columns: []schema.Column{
            {ID: 1, Name: "id", Type: schema.TypeString, Required: true},
            {ID: 2, Name: "owner", Type: schema.TypeString},
            {ID: 3, Name: "value", Type: schema.TypeString},
        },
        Families: []schema.Family{{ID: 0, Name: "primary", Columns: []uint16{2, 3}}},
        Primary: PrimarySpec[runtimeTestRow]{
            IndexID: 1,
            FamilyID: 0,
            Name: "pk_runtime_test",
            Columns: []uint16{1},
            Layout: KeyLayout{KeyString},
            Key: func(row runtimeTestRow) KeyParts { return KeyParts{String(row.ID)} },
        },
        Validate: func(row runtimeTestRow) error {
            return validateKeyString(row.ID)
        },
        EncodeValue: func(row runtimeTestRow) ([]byte, error) {
            value := appendValueString(nil, row.Owner)
            value = appendValueString(value, row.Value)
            return value, nil
        },
        DecodeValue: func(primary KeyParts, value []byte) (runtimeTestRow, error) {
            owner, rest, err := readValueString(value)
            if err != nil {
                return runtimeTestRow{}, err
            }
            val, rest, err := readValueString(rest)
            if err != nil {
                return runtimeTestRow{}, err
            }
            if len(rest) != 0 {
                return runtimeTestRow{}, dberrors.ErrCorruptValue
            }
            return runtimeTestRow{ID: primary[0].S, Owner: owner, Value: val}, nil
        },
    })
    if err != nil {
        t.Fatalf("register test table: %v", err)
    }
    return table
}

func TestTableRuntimePrimaryCRUD(t *testing.T) {
    store := openTestMetaStore(t)
    defer store.close(t)
    table := newRuntimeTestTable(t)
    shard := store.db.HashSlot(3)
    ctx := context.Background()

    row := runtimeTestRow{ID: "a", Owner: "o1", Value: "v1"}
    if err := table.Create(ctx, shard, row); err != nil {
        t.Fatalf("Create(): %v", err)
    }
    if err := table.Create(ctx, shard, row); err != dberrors.ErrAlreadyExists {
        t.Fatalf("duplicate Create() = %v, want ErrAlreadyExists", err)
    }

    got, ok, err := table.Get(ctx, shard, KeyParts{String("a")})
    if err != nil || !ok || got != row {
        t.Fatalf("Get() = %#v ok=%v err=%v", got, ok, err)
    }

    updated := runtimeTestRow{ID: "a", Owner: "o1", Value: "v2"}
    if err := table.Update(ctx, shard, updated); err != nil {
        t.Fatalf("Update(): %v", err)
    }
    got, ok, err = table.Get(ctx, shard, KeyParts{String("a")})
    if err != nil || !ok || got.Value != "v2" {
        t.Fatalf("Get after update = %#v ok=%v err=%v", got, ok, err)
    }

    if err := table.Update(ctx, shard, runtimeTestRow{ID: "missing"}); err != dberrors.ErrNotFound {
        t.Fatalf("missing Update() = %v, want ErrNotFound", err)
    }

    if err := table.Delete(ctx, shard, KeyParts{String("a")}); err != nil {
        t.Fatalf("Delete(): %v", err)
    }
    _, ok, err = table.Get(ctx, shard, KeyParts{String("a")})
    if err != nil || ok {
        t.Fatalf("Get deleted ok=%v err=%v", ok, err)
    }
}

func TestTableRuntimePrimaryScan(t *testing.T) {
    store := openTestMetaStore(t)
    defer store.close(t)
    table := newRuntimeTestTable(t)
    shard := store.db.HashSlot(4)
    ctx := context.Background()

    for _, id := range []string{"a", "b", "c"} {
        if err := table.Upsert(ctx, shard, runtimeTestRow{ID: id, Owner: "o", Value: id}); err != nil {
            t.Fatalf("Upsert(%s): %v", id, err)
        }
    }
    rows, cursor, done, err := table.ScanPrimary(ctx, shard, nil, 2)
    if err != nil || done || len(rows) != 2 || rows[0].ID != "a" || rows[1].ID != "b" || !cursor.Equal(KeyParts{String("b")}) {
        t.Fatalf("first page rows=%#v cursor=%#v done=%v err=%v", rows, cursor, done, err)
    }
    rows, cursor, done, err = table.ScanPrimary(ctx, shard, cursor, 2)
    if err != nil || !done || len(rows) != 1 || rows[0].ID != "c" || len(cursor) != 0 {
        t.Fatalf("second page rows=%#v cursor=%#v done=%v err=%v", rows, cursor, done, err)
    }
}
```

- [ ] **Step 2: Run failing primary runtime tests**

Run: `go test ./pkg/db/meta -run 'TestTableRuntimePrimary' -count=1`

Expected: FAIL because `Table` CRUD methods are undefined.

- [ ] **Step 3: Implement primary CRUD methods**

In `pkg/db/meta/table_runtime.go`, add methods:

```go
func (t Table[R]) Get(ctx context.Context, s *Shard, pk KeyParts) (R, bool, error)
func (t Table[R]) Create(ctx context.Context, s *Shard, row R) error
func (t Table[R]) Update(ctx context.Context, s *Shard, row R) error
func (t Table[R]) Upsert(ctx context.Context, s *Shard, row R) error
func (t Table[R]) Delete(ctx context.Context, s *Shard, pk KeyParts) error
func (t Table[R]) ScanPrimary(ctx context.Context, s *Shard, after KeyParts, limit int) ([]R, KeyParts, bool, error)
func (t Table[R]) ScanPrimaryPrefix(ctx context.Context, s *Shard, prefix KeyParts, after KeyParts, limit int) ([]R, KeyParts, bool, error)
```

Implementation rules:

- Always call `s.check(ctx)` first.
- Validate `pk` length against `t.spec.Primary.Layout` before encoding.
- `Create` returns `dberrors.ErrAlreadyExists` if the primary key exists.
- `Update` returns `dberrors.ErrNotFound` if the primary key is missing.
- `Delete` is idempotent for now: deleting a missing row returns nil. Do not use it for APIs that require `ErrNotFound`.
- Use `s.lock()` for write methods.
- Use `s.db.engine.NewBatch()` and `batch.Commit(true)`.
- Use `engine.NewIter` for `ScanPrimary`; use `keycodec.PrefixEnd` on the encoded cursor key to start after a cursor.
- `ScanPrimary` with `limit <= 0` returns no rows and `done=true`, matching existing page APIs.
- `ScanPrimaryPrefix` supports prefix scans such as plugin bindings by UID; when `limit <= 0`, it scans all matching rows.

Representative implementation helper shape:

```go
type tableWriteMode uint8

const (
    tableWriteCreate tableWriteMode = iota + 1
    tableWriteUpdate
    tableWriteUpsert
)

func (t Table[R]) primaryKey(row R) (KeyParts, error) {
    parts := t.spec.Primary.Key(row)
    if len(parts) != len(t.spec.Primary.Layout) {
        return nil, dberrors.ErrInvalidArgument
    }
    return parts, nil
}
```

- [ ] **Step 4: Run primary runtime tests**

Run: `go test ./pkg/db/meta -run 'TestTableRuntimePrimary' -count=1`

Expected: PASS.

- [ ] **Step 5: Run package tests**

Run: `go test ./pkg/db/meta -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/db/meta/table_runtime.go pkg/db/meta/table_runtime_test.go
git commit -m "feat(db): add meta table primary runtime"
```

---

### Task 6: Add Secondary Index Runtime

**Files:**
- Modify: `pkg/db/meta/table_runtime.go`
- Modify: `pkg/db/meta/table_runtime_test.go`

- [ ] **Step 1: Extend test table with indexes**

Update `newRuntimeTestTable` in `pkg/db/meta/table_runtime_test.go` to include indexes:

```go
Indexes: []IndexSpec[runtimeTestRow]{
    {
        ID: 2,
        Name: "idx_runtime_test_owner",
        Columns: []uint16{2, 1},
        Layout: KeyLayout{KeyString, KeyString},
        Key: func(row runtimeTestRow) (KeyParts, bool) {
            if row.Owner == "" {
                return nil, false
            }
            return KeyParts{String(row.Owner), String(row.ID)}, true
        },
    },
    {
        ID: 3,
        Name: "uidx_runtime_test_value",
        Unique: true,
        Columns: []uint16{3},
        Layout: KeyLayout{KeyString},
        Key: func(row runtimeTestRow) (KeyParts, bool) {
            if row.Value == "" {
                return nil, false
            }
            return KeyParts{String(row.Value)}, true
        },
    },
},
```

- [ ] **Step 2: Add failing index tests**

Append to `pkg/db/meta/table_runtime_test.go`:

```go
func TestTableRuntimeIndexMaintenanceAndScan(t *testing.T) {
    store := openTestMetaStore(t)
    defer store.close(t)
    table := newRuntimeTestTable(t)
    shard := store.db.HashSlot(5)
    ctx := context.Background()

    if err := table.Upsert(ctx, shard, runtimeTestRow{ID: "a", Owner: "o1", Value: "v1"}); err != nil {
        t.Fatalf("upsert a: %v", err)
    }
    if err := table.Upsert(ctx, shard, runtimeTestRow{ID: "b", Owner: "o1", Value: "v2"}); err != nil {
        t.Fatalf("upsert b: %v", err)
    }
    rows, cursor, done, err := table.ScanIndex(ctx, shard, 2, KeyParts{String("o1")}, nil, 10)
    if err != nil || !done || len(rows) != 2 || rows[0].ID != "a" || rows[1].ID != "b" || len(cursor) != 0 {
        t.Fatalf("owner scan rows=%#v cursor=%#v done=%v err=%v", rows, cursor, done, err)
    }

    if err := table.Upsert(ctx, shard, runtimeTestRow{ID: "a", Owner: "o2", Value: "v1"}); err != nil {
        t.Fatalf("move owner: %v", err)
    }
    rows, _, done, err = table.ScanIndex(ctx, shard, 2, KeyParts{String("o1")}, nil, 10)
    if err != nil || !done || len(rows) != 1 || rows[0].ID != "b" {
        t.Fatalf("old owner scan rows=%#v done=%v err=%v", rows, done, err)
    }
}

func TestTableRuntimeUniqueIndexConflict(t *testing.T) {
    store := openTestMetaStore(t)
    defer store.close(t)
    table := newRuntimeTestTable(t)
    shard := store.db.HashSlot(6)
    ctx := context.Background()

    if err := table.Create(ctx, shard, runtimeTestRow{ID: "a", Owner: "o1", Value: "same"}); err != nil {
        t.Fatalf("create a: %v", err)
    }
    err := table.Create(ctx, shard, runtimeTestRow{ID: "b", Owner: "o2", Value: "same"})
    if err != dberrors.ErrAlreadyExists {
        t.Fatalf("unique conflict err = %v, want ErrAlreadyExists", err)
    }
}

func TestTableRuntimeIndexScanSkipsStaleEntries(t *testing.T) {
    store := openTestMetaStore(t)
    defer store.close(t)
    table := newRuntimeTestTable(t)
    shard := store.db.HashSlot(7)
    ctx := context.Background()

    if err := table.Upsert(ctx, shard, runtimeTestRow{ID: "a", Owner: "current", Value: "v1"}); err != nil {
        t.Fatalf("upsert: %v", err)
    }
    staleKey, err := encodeTableIndexKey(shard.HashSlot(), table.Schema().ID, 2, KeyParts{String("stale"), String("a")}, KeyParts{String("a")})
    if err != nil {
        t.Fatalf("stale index key: %v", err)
    }
    batch := store.engine.NewBatch()
    if err := batch.Set(staleKey, nil); err != nil {
        t.Fatalf("set stale index: %v", err)
    }
    if err := batch.Commit(true); err != nil {
        t.Fatalf("commit stale index: %v", err)
    }
    batch.Close()

    rows, _, done, err := table.ScanIndex(ctx, shard, 2, KeyParts{String("stale")}, nil, 10)
    if err != nil || !done || len(rows) != 0 {
        t.Fatalf("stale scan rows=%#v done=%v err=%v", rows, done, err)
    }
}
```

- [ ] **Step 3: Run failing index tests**

Run: `go test ./pkg/db/meta -run 'TestTableRuntime(Index|Unique)' -count=1`

Expected: FAIL until index maintenance and scan exist.

- [ ] **Step 4: Implement index maintenance and scan**

In `pkg/db/meta/table_runtime.go`:

- Add `indexByID` helper.
- In `Create/Update/Upsert`, after loading old row:
  - `stageDeleteIndexes(batch, hashSlot, oldRow, oldPK)` if old exists.
  - `stageUniqueIndexChecks(ctx, s, row, pk)` before setting new row.
  - `stagePutIndexes(batch, hashSlot, row, pk)` after setting row.
- In `Delete`, load old row and delete its indexes before deleting primary.
- Implement `ScanIndex`:
  - Build scan prefix with `encodeTableIndexScanPrefix`.
  - If `after` is non-empty, encode the `after` key parts with the same index prefix and start at `keycodec.PrefixEnd(encodedAfter)`.
  - Decode full index key suffix by first decoding `index.Layout`, then decoding `primary.Layout` from remaining bytes.
  - Get primary row.
  - Recompute the row's index parts and only return matching rows.
  - Return `done=false` and cursor equal to the last emitted full index parts when `limit` is reached and another candidate remains.

- [ ] **Step 5: Run index tests**

Run: `go test ./pkg/db/meta -run 'TestTableRuntime' -count=1`

Expected: PASS.

- [ ] **Step 6: Run package tests**

Run: `go test ./pkg/db/meta -count=1`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add pkg/db/meta/table_runtime.go pkg/db/meta/table_runtime_test.go
git commit -m "feat(db): add meta table index runtime"
```

---

### Task 7: Add Runtime Batch Staging

**Files:**
- Modify: `pkg/db/meta/table_runtime.go`
- Modify: `pkg/db/meta/batch.go`
- Modify: `pkg/db/meta/table_runtime_test.go`
- Modify: `pkg/db/meta/batch_test.go`

- [ ] **Step 1: Write failing runtime batch tests**

Append to `pkg/db/meta/table_runtime_test.go`:

```go
func TestTableRuntimeBatchStaging(t *testing.T) {
    store := openTestMetaStore(t)
    defer store.close(t)
    table := newRuntimeTestTable(t)
    ctx := context.Background()

    batch := store.db.NewBatch()
    if err := table.StageCreate(batch, 8, runtimeTestRow{ID: "a", Owner: "o1", Value: "v1"}); err != nil {
        t.Fatalf("StageCreate: %v", err)
    }
    if err := table.StageUpdate(batch, 8, runtimeTestRow{ID: "a", Owner: "o2", Value: "v2"}); err != nil {
        t.Fatalf("StageUpdate: %v", err)
    }
    if err := batch.Commit(ctx); err != nil {
        t.Fatalf("Commit: %v", err)
    }

    got, ok, err := table.Get(ctx, store.db.HashSlot(8), KeyParts{String("a")})
    if err != nil || !ok || got.Owner != "o2" || got.Value != "v2" {
        t.Fatalf("Get staged row = %#v ok=%v err=%v", got, ok, err)
    }
    rows, _, done, err := table.ScanIndex(ctx, store.db.HashSlot(8), 2, KeyParts{String("o1")}, nil, 10)
    if err != nil || !done || len(rows) != 0 {
        t.Fatalf("old index rows=%#v done=%v err=%v", rows, done, err)
    }
}

func TestTableRuntimeBatchCreateDuplicateRollsBack(t *testing.T) {
    store := openTestMetaStore(t)
    defer store.close(t)
    table := newRuntimeTestTable(t)
    ctx := context.Background()

    batch := store.db.NewBatch()
    _ = table.StageCreate(batch, 9, runtimeTestRow{ID: "a", Owner: "o", Value: "v1"})
    _ = table.StageCreate(batch, 9, runtimeTestRow{ID: "a", Owner: "o", Value: "v2"})
    if err := batch.Commit(ctx); err != dberrors.ErrAlreadyExists {
        t.Fatalf("Commit duplicate err = %v, want ErrAlreadyExists", err)
    }
    if _, ok, err := table.Get(ctx, store.db.HashSlot(9), KeyParts{String("a")}); err != nil || ok {
        t.Fatalf("duplicate rollback get ok=%v err=%v", ok, err)
    }
}
```

- [ ] **Step 2: Run failing batch tests**

Run: `go test ./pkg/db/meta -run 'TestTableRuntimeBatch' -count=1`

Expected: FAIL because `StageCreate`, `StageUpdate`, and batch overlays are missing.

- [ ] **Step 3: Add batch overlay state**

Modify `pkg/db/meta/batch.go`:

```go
type batchCommitState struct {
    db *MetaDB
    createdUsers map[string]struct{}
    userWrites map[string]struct{}
    tableRows map[string]tableRowOverlay
    tableCreates map[string]struct{}
    runtimeMeta map[string]runtimeMetaOverlay
    migrationTasks map[string]migrationTaskOverlay
    channelPublishes map[string]Channel
    channelDeletes map[string]struct{}
}

type tableRowOverlay struct {
    value []byte
    exists bool
}
```

Initialize these maps in `Commit`:

```go
tableRows: make(map[string]tableRowOverlay),
tableCreates: make(map[string]struct{}),
```

- [ ] **Step 4: Implement table stage helpers**

In `pkg/db/meta/table_runtime.go`, add:

```go
func (t Table[R]) StageCreate(b *Batch, hashSlot HashSlot, row R) error
func (t Table[R]) StageUpdate(b *Batch, hashSlot HashSlot, row R) error
func (t Table[R]) StageUpsert(b *Batch, hashSlot HashSlot, row R) error
func (t Table[R]) StageDelete(b *Batch, hashSlot HashSlot, pk KeyParts) error
```

Implementation rules:

- Stage methods validate table spec and row immediately where possible.
- Each stage method calls `b.ensureOpen()` before adding operations.
- The operation closure uses a shared helper like `tableApplyWrite(ctx, state, engineBatch, hashSlot, mode, row)`.
- The helper reads from `state.tableRows[string(primaryKey)]` first, then committed storage.
- `StageCreate` records `state.tableCreates[string(primaryKey)]` and returns `ErrAlreadyExists` if the same key was already created in the batch or exists in committed storage.
- `StageUpdate` treats a prior staged create/upsert as existing.
- Index maintenance uses the old overlay row when present.

- [ ] **Step 5: Run runtime batch tests**

Run: `go test ./pkg/db/meta -run 'TestTableRuntimeBatch' -count=1`

Expected: PASS.

- [ ] **Step 6: Run existing batch tests**

Run: `go test ./pkg/db/meta -run TestMetaBatch -count=1`

Expected: PASS; if `TestMetaBatchReadYourWritesAndPublishesChannelCache` fails, do not change channel overlay behavior. Fix only table overlay interactions.

- [ ] **Step 7: Commit**

```bash
git add pkg/db/meta/table_runtime.go pkg/db/meta/batch.go pkg/db/meta/table_runtime_test.go pkg/db/meta/batch_test.go
git commit -m "feat(db): add meta table batch staging"
```

---

### Task 8: Migrate User Table To Runtime

**Files:**
- Modify: `pkg/db/meta/schema.go`
- Modify: `pkg/db/meta/table_user.go`
- Modify: `pkg/db/meta/batch.go`
- Modify: `pkg/db/meta/compat.go`
- Modify: `pkg/db/meta/user_test.go`
- Modify: `pkg/db/meta/batch_test.go`

- [ ] **Step 1: Add/adjust user behavior tests**

Keep `TestUserCRUDAndPage`. Add a focused batch test if not covered:

```go
func TestMetaBatchRuntimeUserCreateAndUpdate(t *testing.T) {
    store := openTestMetaStore(t)
    defer store.close(t)
    ctx := context.Background()

    batch := store.db.NewBatch()
    if err := batch.CreateUser(11, User{UID: "u1", Token: "t1"}); err != nil {
        t.Fatalf("CreateUser stage: %v", err)
    }
    if err := batch.UpsertUser(11, User{UID: "u1", Token: "t2"}); err != nil {
        t.Fatalf("UpsertUser stage: %v", err)
    }
    if err := batch.Commit(ctx); err != nil {
        t.Fatalf("Commit: %v", err)
    }
    got, ok, err := store.db.HashSlot(11).GetUser(ctx, "u1")
    if err != nil || !ok || got.Token != "t2" {
        t.Fatalf("GetUser = %#v ok=%v err=%v", got, ok, err)
    }
}
```

- [ ] **Step 2: Run tests before migration**

Run: `go test ./pkg/db/meta -run 'TestUser|TestMetaBatchRuntimeUser' -count=1`

Expected: Existing user tests PASS. New runtime-specific batch test may PASS with old code or fail depending on current same-batch semantics; keep it as a regression target.

- [ ] **Step 3: Define `userTable` spec**

In `pkg/db/meta/table_user.go`, add near the top:

```go
var userTable = registerMetaTable(TableSpec[User]{
    ID: TableIDUser,
    Name: "user",
    Columns: []schema.Column{
        {ID: columnIDStringKey, Name: "key", Type: schema.TypeString, Required: true},
        {ID: columnIDValue, Name: "value", Type: schema.TypeBytes},
    },
    Families: []schema.Family{{ID: userPrimaryFamilyID, Name: "primary", Columns: []uint16{columnIDValue}}},
    Primary: PrimarySpec[User]{
        IndexID: userPrimaryIndexID,
        FamilyID: userPrimaryFamilyID,
        Name: "pk_user",
        Columns: []uint16{columnIDStringKey},
        Layout: KeyLayout{KeyString},
        Key: func(user User) KeyParts { return KeyParts{String(user.UID)} },
    },
    Validate: func(user User) error { return validateKeyString(user.UID) },
    EncodeValue: func(user User) ([]byte, error) { return encodeUserValue(user), nil },
    DecodeValue: func(primary KeyParts, value []byte) (User, error) {
        return decodeUserValue(primary[0].S, value)
    },
})

var UserTable = userTable.Schema()
```

Import `pkg/db/internal/schema` in `table_user.go`.

- [ ] **Step 4: Remove central user descriptor registration**

In `pkg/db/meta/schema.go`:

- Remove `UserTable = simpleMetaTable(TableIDUser, "user")` from the var block.
- Remove `{Table: UserTable}` from the central descriptor `init()`.
- Keep `simpleMetaTable` because non-migrated tables still use it.

- [ ] **Step 5: Convert user methods to runtime wrappers**

In `pkg/db/meta/table_user.go`:

```go
func (s *Shard) CreateUser(ctx context.Context, user User) error {
    return userTable.Create(ctx, s, user)
}

func (s *Shard) UpsertUser(ctx context.Context, user User) error {
    return userTable.Upsert(ctx, s, user)
}

func (s *Shard) UpdateUser(ctx context.Context, user User) error {
    return userTable.Update(ctx, s, user)
}

func (s *Shard) GetUser(ctx context.Context, uid string) (User, bool, error) {
    if err := validateKeyString(uid); err != nil {
        return User{}, false, err
    }
    return userTable.Get(ctx, s, KeyParts{String(uid)})
}

func (s *Shard) DeleteUser(ctx context.Context, uid string) error {
    if err := validateKeyString(uid); err != nil {
        return err
    }
    return userTable.Delete(ctx, s, KeyParts{String(uid)})
}

func (s *Shard) ListUsersPage(ctx context.Context, cursorUID string, limit int) ([]User, string, bool, error) {
    if cursorUID != "" {
        if err := validateKeyString(cursorUID); err != nil {
            return nil, "", false, err
        }
    }
    var after KeyParts
    if cursorUID != "" {
        after = KeyParts{String(cursorUID)}
    }
    users, cursor, done, err := userTable.ScanPrimary(ctx, s, after, limit)
    if err != nil || done || len(cursor) == 0 {
        return users, "", done, err
    }
    return users, cursor[0].S, done, nil
}
```

Remove now-unused `bytes`, `engine`, and `keycodec` imports from `table_user.go` if applicable. Keep `encodeUserValue` / `decodeUserValue` unchanged.

- [ ] **Step 6: Convert strict `Batch` user methods**

In `pkg/db/meta/batch.go`:

```go
func (b *Batch) CreateUser(hashSlot HashSlot, user User) error {
    return userTable.StageCreate(b, hashSlot, user)
}

func (b *Batch) UpsertUser(hashSlot HashSlot, user User) error {
    return userTable.StageUpsert(b, hashSlot, user)
}
```

Remove `createdUsers` / `userWrites` fields from `batchCommitState` only after compatibility no longer uses them. If `compat.WriteBatch.CreateUser` still depends on `userWrites`, keep those fields until Task 10 or update compatibility behavior carefully.

- [ ] **Step 7: Preserve compatibility `WriteBatch.CreateUser` behavior**

Inspect current `WriteBatch.CreateUser`: it treats an already existing committed user as a no-op. Preserve that behavior. If runtime `StageCreate` is strict, keep this method custom for now and add a comment:

```go
// CreateUser keeps the legacy compatibility no-op behavior when the user already exists.
```

Do not route `WriteBatch.CreateUser` to strict `Batch.CreateUser` unless tests prove legacy callers expect conflicts.

- [ ] **Step 8: Run user tests**

Run: `go test ./pkg/db/meta -run 'TestUser|TestMetaBatch|TestMetaSchemaValidateAllTables' -count=1`

Expected: PASS.

- [ ] **Step 9: Run package tests**

Run: `go test ./pkg/db/meta -count=1`

Expected: PASS.

- [ ] **Step 10: Commit**

```bash
git add pkg/db/meta/schema.go pkg/db/meta/table_user.go pkg/db/meta/batch.go pkg/db/meta/compat.go pkg/db/meta/user_test.go pkg/db/meta/batch_test.go
git commit -m "refactor(db): migrate user metadata to table runtime"
```

---

### Task 9: Migrate Device Table To Runtime

**Files:**
- Modify: `pkg/db/meta/schema.go`
- Modify: `pkg/db/meta/table_device.go`
- Modify: `pkg/db/meta/compat.go`
- Modify: `pkg/db/meta/device_test.go`

- [ ] **Step 1: Confirm device behavior tests**

Run: `go test ./pkg/db/meta -run TestDevice -count=1`

Expected: PASS before migration.

- [ ] **Step 2: Define `deviceTable` spec**

In `pkg/db/meta/table_device.go`, add:

```go
var deviceTable = registerMetaTable(TableSpec[Device]{
    ID: TableIDDevice,
    Name: "device",
    Columns: []schema.Column{
        {ID: columnIDStringKey, Name: "uid", Type: schema.TypeString, Required: true},
        {ID: columnIDIntKey, Name: "device_flag", Type: schema.TypeInt64, Required: true},
        {ID: columnIDValue, Name: "value", Type: schema.TypeBytes},
    },
    Families: []schema.Family{{ID: devicePrimaryFamilyID, Name: "primary", Columns: []uint16{columnIDValue}}},
    Primary: PrimarySpec[Device]{
        IndexID: devicePrimaryIndexID,
        FamilyID: devicePrimaryFamilyID,
        Name: "pk_device",
        Columns: []uint16{columnIDStringKey, columnIDIntKey},
        Layout: KeyLayout{KeyString, KeyInt64Ordered},
        Key: func(device Device) KeyParts { return KeyParts{String(device.UID), Int64Ordered(device.DeviceFlag)} },
    },
    Validate: validateDevice,
    EncodeValue: func(device Device) ([]byte, error) { return encodeDeviceValue(device), nil },
    DecodeValue: func(primary KeyParts, value []byte) (Device, error) {
        return decodeDeviceValue(primary[0].S, primary[1].I64, value)
    },
})

var DeviceTable = deviceTable.Schema()
```

If `validateDevice` does not exist, add:

```go
func validateDevice(device Device) error {
    return validateKeyString(device.UID)
}
```

- [ ] **Step 3: Remove central device descriptor registration**

In `pkg/db/meta/schema.go` remove:

- `DeviceTable = simpleMetaTable(TableIDDevice, "device")`
- `{Table: DeviceTable}` from central init.

- [ ] **Step 4: Convert device methods**

In `pkg/db/meta/table_device.go`:

```go
func (s *Shard) UpsertDevice(ctx context.Context, device Device) error {
    return deviceTable.Upsert(ctx, s, device)
}

func (s *Shard) GetDevice(ctx context.Context, uid string, deviceFlag int64) (Device, bool, error) {
    if err := validateKeyString(uid); err != nil {
        return Device{}, false, err
    }
    return deviceTable.Get(ctx, s, KeyParts{String(uid), Int64Ordered(deviceFlag)})
}
```

Keep durable `encodeDeviceValue` / `decodeDeviceValue` unchanged.

- [ ] **Step 5: Update compatibility batch upsert if behavior is identical**

In `pkg/db/meta/compat.go`, replace custom `WriteBatch.UpsertDevice` body with:

```go
return deviceTable.StageUpsert(b.batch, HashSlot(hashSlot), device)
```

Only do this if the existing behavior is exactly an upsert with no special guard.

- [ ] **Step 6: Run device tests**

Run: `go test ./pkg/db/meta -run 'TestDevice|TestMetaSchemaValidateAllTables' -count=1`

Expected: PASS.

- [ ] **Step 7: Run package tests**

Run: `go test ./pkg/db/meta -count=1`

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add pkg/db/meta/schema.go pkg/db/meta/table_device.go pkg/db/meta/compat.go pkg/db/meta/device_test.go
git commit -m "refactor(db): migrate device metadata to table runtime"
```

---

### Task 10: Migrate Plugin Binding Table To Runtime

**Files:**
- Modify: `pkg/db/meta/schema.go`
- Modify: `pkg/db/meta/table_plugin_binding.go`
- Modify: `pkg/db/meta/compat.go`
- Modify: `pkg/db/meta/plugin_binding_test.go`

- [ ] **Step 1: Add behavior test for stale plugin index skip**

Append to `pkg/db/meta/plugin_binding_test.go`:

```go
func TestPluginBindingScanSkipsStaleIndex(t *testing.T) {
    store := openTestMetaStore(t)
    defer store.close(t)
    ctx := context.Background()
    shard := store.db.HashSlot(22)

    if err := shard.BindPluginUser(ctx, PluginUserBinding{UID: "u1", PluginNo: "p1", CreatedAtMS: 1, UpdatedAtMS: 1}); err != nil {
        t.Fatalf("BindPluginUser: %v", err)
    }
    staleKey := encodePluginBindingPluginIndexKey(22, "stale", "u1")
    batch := store.engine.NewBatch()
    if err := batch.Set(staleKey, nil); err != nil {
        t.Fatalf("set stale index: %v", err)
    }
    if err := batch.Commit(true); err != nil {
        t.Fatalf("commit stale index: %v", err)
    }
    batch.Close()

    rows, _, done, err := shard.ScanPluginBindingsByPluginNo(ctx, "stale", PluginUserBindingCursor{}, 10)
    if err != nil || !done || len(rows) != 0 {
        t.Fatalf("stale scan rows=%#v done=%v err=%v", rows, done, err)
    }
}
```

- [ ] **Step 2: Run plugin tests before migration**

Run: `go test ./pkg/db/meta -run TestPluginBinding -count=1`

Expected: PASS. If stale index already skips correctly, this confirms existing behavior.

- [ ] **Step 3: Define `pluginBindingTable` spec**

In `pkg/db/meta/table_plugin_binding.go`, add or replace descriptor setup:

```go
var pluginBindingTable = registerMetaTable(TableSpec[PluginUserBinding]{
    ID: TableIDPluginBinding,
    Name: "plugin_binding",
    Columns: []schema.Column{
        {ID: pluginBindingColumnUID, Name: "uid", Type: schema.TypeString, Required: true},
        {ID: pluginBindingColumnPluginNo, Name: "plugin_no", Type: schema.TypeString, Required: true},
        {ID: pluginBindingColumnValue, Name: "value", Type: schema.TypeBytes},
    },
    Families: []schema.Family{{ID: pluginBindingPrimaryFamilyID, Name: "primary", Columns: []uint16{pluginBindingColumnValue}}},
    Primary: PrimarySpec[PluginUserBinding]{
        IndexID: pluginBindingPrimaryIndexID,
        FamilyID: pluginBindingPrimaryFamilyID,
        Name: "pk_plugin_binding",
        Columns: []uint16{pluginBindingColumnUID, pluginBindingColumnPluginNo},
        Layout: KeyLayout{KeyString, KeyString},
        Key: func(binding PluginUserBinding) KeyParts { return KeyParts{String(binding.UID), String(binding.PluginNo)} },
    },
    Indexes: []IndexSpec[PluginUserBinding]{
        {
            ID: pluginBindingPluginIndexID,
            Name: "idx_plugin_binding_plugin_no",
            Columns: []uint16{pluginBindingColumnPluginNo, pluginBindingColumnUID},
            Layout: KeyLayout{KeyString, KeyString},
            Key: func(binding PluginUserBinding) (KeyParts, bool) {
                return KeyParts{String(binding.PluginNo), String(binding.UID)}, true
            },
        },
    },
    Validate: validatePluginUserBinding,
    EncodeValue: func(binding PluginUserBinding) ([]byte, error) { return encodePluginUserBindingValue(binding), nil },
    DecodeValue: func(primary KeyParts, value []byte) (PluginUserBinding, error) {
        return decodePluginUserBindingValue(primary[0].S, primary[1].S, value)
    },
})

var PluginBindingTable = pluginBindingTable.Schema()
```

Add package-local plugin binding column and primary index IDs if they do not exist:

```go
const (
    pluginBindingPrimaryIndexID uint16 = 1
    pluginBindingColumnUID uint16 = 1
    pluginBindingColumnPluginNo uint16 = 2
    pluginBindingColumnValue uint16 = 3
)
```

Do not reuse `columnIDIntKey` for `plugin_no`; it is named for integer key columns in other simple descriptors.

- [ ] **Step 4: Remove central plugin descriptor registration**

In `pkg/db/meta/schema.go` remove:

- `PluginBindingTable = simpleMetaTable(TableIDPluginBinding, "plugin_binding")`
- `{Table: PluginBindingTable}` from central init.

- [ ] **Step 5: Convert plugin write methods**

In `pkg/db/meta/table_plugin_binding.go`:

```go
func (s *Shard) BindPluginUser(ctx context.Context, binding PluginUserBinding) error {
    if err := s.check(ctx); err != nil {
        return err
    }
    if err := validatePluginUserBinding(binding); err != nil {
        return err
    }
    existing, ok, err := s.GetPluginUserBinding(ctx, binding.UID, binding.PluginNo)
    if err != nil {
        return err
    }
    if ok {
        binding.CreatedAtMS = existing.CreatedAtMS
        if binding.UpdatedAtMS < existing.UpdatedAtMS {
            binding.UpdatedAtMS = existing.UpdatedAtMS
        }
    }
    return pluginBindingTable.Upsert(ctx, s, binding)
}

func (s *Shard) UnbindPluginUser(ctx context.Context, uid, pluginNo string) error {
    if err := validatePluginUserBindingIdentity(uid, pluginNo); err != nil {
        return err
    }
    return pluginBindingTable.Delete(ctx, s, KeyParts{String(uid), String(pluginNo)})
}
```

Add private/public helper if needed:

```go
func (s *Shard) GetPluginUserBinding(ctx context.Context, uid, pluginNo string) (PluginUserBinding, bool, error) {
    if err := validatePluginUserBindingIdentity(uid, pluginNo); err != nil {
        return PluginUserBinding{}, false, err
    }
    return pluginBindingTable.Get(ctx, s, KeyParts{String(uid), String(pluginNo)})
}
```

- [ ] **Step 6: Convert plugin scan methods**

Use runtime scans while preserving cursors:

```go
func (s *Shard) ListPluginBindingsByUID(ctx context.Context, uid string) ([]PluginUserBinding, error) {
    if err := validatePluginBindingUID(uid); err != nil {
        return nil, err
    }
    rows, _, _, err := pluginBindingTable.ScanPrimaryPrefix(ctx, s, KeyParts{String(uid)}, nil, 0)
    return rows, err
}
```

For `ScanPluginBindingsByPluginNo`, use index prefix `KeyParts{String(pluginNo)}` and cursor UID:

```go
var after KeyParts
if cursor.UID != "" {
    after = KeyParts{String(pluginNo), String(cursor.UID)}
}
rows, next, done, err := pluginBindingTable.ScanIndex(ctx, s, pluginBindingPluginIndexID, KeyParts{String(pluginNo)}, after, limit)
```

Map `next` to `PluginUserBindingCursor{PluginNo: pluginNo, UID: next[1].S}` when not done.

- [ ] **Step 7: Convert compatibility methods if behavior is unchanged**

In `pkg/db/meta/compat.go`, methods like `BindPluginUser`, `UnbindPluginUser`, `ListPluginBindingsByUID`, and `ScanPluginBindingsByPluginNo` already delegate to shard methods. They should need no change unless signatures were adjusted.

- [ ] **Step 8: Run plugin tests**

Run: `go test ./pkg/db/meta -run 'TestPluginBinding|TestMetaSchemaValidateAllTables' -count=1`

Expected: PASS.

- [ ] **Step 9: Run package tests**

Run: `go test ./pkg/db/meta -count=1`

Expected: PASS.

- [ ] **Step 10: Commit**

```bash
git add pkg/db/meta/schema.go pkg/db/meta/table_plugin_binding.go pkg/db/meta/compat.go pkg/db/meta/plugin_binding_test.go
git commit -m "refactor(db): migrate plugin bindings to table runtime"
```

---

### Task 11: Clean Up Central Boilerplate And Docs

**Files:**
- Modify: `pkg/db/meta/schema.go`
- Modify: `pkg/db/meta/keys.go`
- Modify: `pkg/db/meta/types.go`
- Modify: `pkg/db/meta/FLOW.md`
- Modify: `docs/development/PROJECT_KNOWLEDGE.md`

- [ ] **Step 1: Find now-unused helpers/constants**

Run:

```bash
rg -n 'encodeUserRowKey|decodeUserKeyUID|encodeDeviceRowKey|encodePluginBindingRow|encodePluginBindingPluginIndex|userPrimaryIndexID|devicePrimaryIndexID|pluginBindingPrimaryIndexID' pkg/db/meta
```

Expected: Some helpers may still be used by tests or compatibility. Remove only helpers that have no references after runtime migration.

- [ ] **Step 2: Remove unused code conservatively**

Rules:

- Do not remove key helpers still used by tests that intentionally validate legacy key format.
- Do not remove table ID constants from `types.go` if external packages may refer to them.
- Move table-local family/index constants next to their table if they are only used there.
- Keep `encodeRowPrefix`, `encodeIndexPrefix`, and snapshot span helpers; runtime depends on them.

- [ ] **Step 3: Update `pkg/db/meta/FLOW.md`**

Add a short flow item after schema/key helpers:

```markdown
3. Ordinary metadata tables register a `TableSpec` in their `table_<name>.go`
   file; the registry drives `Tables()`, row spans for snapshots, and common
   primary/index runtime behavior.
```

Adjust later numbering. Keep the existing notes for custom complex tables.

- [ ] **Step 4: Update project knowledge only if useful**

Append one concise note to `docs/development/PROJECT_KNOWLEDGE.md` under `Local storage`:

```markdown
- Ordinary new `pkg/db/meta` tables should use the meta table runtime registry;
  custom code is reserved for cache, guard, monotonic, or multi-record state-machine semantics.
```

Keep the document concise as required by `AGENTS.md`.

- [ ] **Step 5: Run package tests**

Run: `go test ./pkg/db/meta -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/db/meta/schema.go pkg/db/meta/keys.go pkg/db/meta/types.go pkg/db/meta/FLOW.md docs/development/PROJECT_KNOWLEDGE.md
git commit -m "docs(db): document meta table runtime workflow"
```

---

### Task 12: Full Verification

**Files:**
- No code changes unless verification finds a real issue.

- [ ] **Step 1: Run focused package tests**

Run: `go test ./pkg/db/... -count=1`

Expected: PASS.

- [ ] **Step 2: Run targeted broader tests**

Run: `go test ./internal/... ./pkg/... -count=1`

Expected: PASS. If unrelated failures appear, capture package/name/error and verify whether the failure touches `pkg/db/meta` before changing code.

- [ ] **Step 3: Check storage import boundary**

Run:

```bash
rg -n 'github.com/cockroachdb/pebble|pebble\.' pkg/db/meta
```

Expected: no output.

- [ ] **Step 4: Check remaining central add-table hotspots**

Run:

```bash
rg -n 'Tables\(\)|hashSlotSnapshotReplaceSpans|TableID.* stores|encode.*RowKey|encode.*IndexKey' pkg/db/meta
```

Expected: remaining results are either runtime internals, complex custom tables, or legacy table APIs. New ordinary tables should not require edits in `schema.go`, `keys.go`, `snapshot.go`, or `batch.go`.

- [ ] **Step 5: Check working tree**

Run: `git status --short`

Expected: only intended changes are present. Do not revert unrelated user changes listed before this plan started.

- [ ] **Step 6: Write verification note if desired**

If the implementation produces important verification details, add `docs/superpowers/reports/YYYY-MM-DD-meta-table-runtime-verification.md` with commands and results.

- [ ] **Step 7: Final commit if verification doc was added**

```bash
git add docs/superpowers/reports/YYYY-MM-DD-meta-table-runtime-verification.md
git commit -m "docs: verify meta table runtime"
```

---

## Implementation Notes

- `ScanPrimary` in this plan needs two scan modes:
  - Full primary scan after a cursor for user-style pages.
  - Prefix scan for plugin UID lists. If overloading `ScanPrimary` makes the API ambiguous, add `ScanPrimaryPrefix` explicitly.
- Runtime index scan cursor semantics should be documented in `table_runtime.go`. For the first migrated indexed table, the full index key includes plugin number and UID, so cursoring by UID is stable.
- Compatibility `WriteBatch.CreateUser` currently differs from strict `Batch.CreateUser`. Preserve observed behavior unless a caller audit proves it is safe to tighten.
- Keep value codecs unchanged for migrated tables. This refactor should not require a disk migration.
- Keep complex tables custom. Do not migrate `channel_runtime_meta`, `channel_migration`, or `hashslot_migration` as part of this plan.
