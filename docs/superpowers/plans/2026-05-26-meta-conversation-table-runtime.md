# Meta Conversation Table Runtime Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Migrate `pkg/db/meta` `conversation` and `cmd_conversation` tables onto the meta table runtime while preserving durable key layouts and public behavior.

**Architecture:** Add typed `TableSpec`s for user and CMD conversation state, and extend the table runtime with legacy projected indexes whose index tuple can derive the primary key without appending a primary suffix. Keep domain methods responsible for conversation merge/touch/hide/read semantics while delegating primary reads, writes, active-index maintenance, active scans, and primary-prefix pages to runtime-backed helpers.

**Tech Stack:** Go, `pkg/db/meta` table runtime, `pkg/db/internal/schema`, existing Pebble-backed meta test store.

---

## Starting Constraints

- Work in `.worktrees/meta-conversation-table-runtime` on branch `feature/meta-conversation-table-runtime`.
- Run Go commands with `GOWORK=off` because parent `go.work` does not include nested worktrees.
- Do not touch the unrelated dirty deletion in the main worktree: `docs/superpowers/specs/2026-05-26-meta-table-codec-design.md`.
- Follow `pkg/db/meta/FLOW.md`; update it if conversation flow wording changes.
- Use TDD: write failing tests first, verify failure, implement minimal code, verify pass, commit.
- Keep storage boundary clean: `pkg/db/meta` must not import Pebble directly.

## File Structure

- Modify: `pkg/db/meta/table_runtime.go`
  - Add projected legacy index support for indexes whose physical key is only `indexParts`, with primary key derived from decoded index parts.
  - Preserve existing runtime behavior for normal secondary indexes, `PrimaryFromIndex`, `DescriptorOnly`, and `StorePrimaryValue`.
- Modify: `pkg/db/meta/table_runtime_test.go`
  - Add focused runtime tests for projected index encoding, scan, stale skipping, malformed key handling, and validation.
- Modify: `pkg/db/meta/table_conversation.go`
  - Own `UserConversationState`, `conversationTable` spec, `ConversationTable` schema export, runtime-backed wrappers, active-index projection, value codec, and table-specific stage helper.
- Modify: `pkg/db/meta/table_cmd_conversation.go`
  - Own `CMDConversationState`, `cmdConversationTable` spec, `CMDConversationTable` schema export, runtime-backed wrappers, active-index projection reuse, value codec, and table-specific stage helper.
- Modify: `pkg/db/meta/schema.go`
  - Remove manual `ConversationTable` and `CMDConversationTable` descriptor declarations and central registry entries.
- Modify: `pkg/db/meta/schema_test.go`
  - Assert conversation and CMD schema descriptors still include `conversationActiveIndexID`.
- Modify: `pkg/db/meta/user_conversation_state_test.go`
  - Add active-index stale/malformed/raw-layout regressions and keep existing behavior tests passing.
- Modify: `pkg/db/meta/cmd_conversation_state_test.go`
  - Add active-index stale/malformed/raw-layout regressions and keep existing behavior tests passing.
- Modify: `pkg/db/meta/compat.go`
  - Keep public compatibility methods unchanged; update internal batch staging only if old helpers move to runtime-backed helpers.
- Modify: `pkg/db/meta/FLOW.md`
  - Update the runtime-backed table list and conversation flow wording.

---

### Task 1: Add Conversation Runtime Regression Tests

**Files:**
- Modify: `pkg/db/meta/user_conversation_state_test.go`
- Modify: `pkg/db/meta/cmd_conversation_state_test.go`
- Modify: `pkg/db/meta/schema_test.go`

- [ ] **Step 1: Inspect current conversation tests**

Run:

```bash
rg -n 'TestUserConversation|TestCMDConversation|ConversationTable|CMDConversationTable' pkg/db/meta/*test.go pkg/db/meta/schema.go
```

Expected: existing user/CMD behavior tests and schema validation test are found.

- [ ] **Step 2: Add schema active-index assertions**

In `pkg/db/meta/schema_test.go`, extend `TestMetaSchemaValidateAllTables` with two booleans:

```go
conversationActiveIndexRegistered := false
cmdConversationActiveIndexRegistered := false
```

Inside the table loop, set them when:

```go
if table.ID == TableIDConversation {
    for _, index := range table.Indexes {
        if index.ID == conversationActiveIndexID && index.Name == "idx_conversation_active" {
            conversationActiveIndexRegistered = true
        }
    }
}
if table.ID == TableIDCMDConversation {
    for _, index := range table.Indexes {
        if index.ID == conversationActiveIndexID && index.Name == "idx_cmd_conversation_active" {
            cmdConversationActiveIndexRegistered = true
        }
    }
}
```

After the loop, fail if either bool is false.

- [ ] **Step 3: Add user conversation raw active-index layout test**

Add to `pkg/db/meta/user_conversation_state_test.go`:

```go
func TestUserConversationActiveIndexKeepsLegacyLayout(t *testing.T) {
    store := openTestMetaStore(t)
    defer store.close(t)
    ctx := context.Background()
    shard := store.db.HashSlot(21)

    state := UserConversationState{UID: "u-layout", ChannelID: "g1", ChannelType: 2, ActiveAt: 123, UpdatedAt: 10}
    if err := shard.UpsertUserConversationState(ctx, state); err != nil {
        t.Fatalf("UpsertUserConversationState: %v", err)
    }

    legacyKey := encodeConversationActiveIndexKey(21, TableIDConversation, state.UID, state.ActiveAt, state.ChannelID, state.ChannelType)
    if _, ok, err := store.db.get(legacyKey); err != nil || !ok {
        t.Fatalf("legacy active index ok=%v err=%v", ok, err)
    }

    suffixedKey, err := encodeTableIndexKey(21, TableIDConversation, conversationActiveIndexID,
        KeyParts{String(state.UID), Int64Desc(state.ActiveAt), String(state.ChannelID), Int64Ordered(state.ChannelType)},
        KeyParts{String(state.UID), String(state.ChannelID), Int64Ordered(state.ChannelType)})
    if err != nil {
        t.Fatalf("generic suffixed key: %v", err)
    }
    if _, ok, err := store.db.get(suffixedKey); err != nil || ok {
        t.Fatalf("suffixed active index ok=%v err=%v, want missing", ok, err)
    }
}
```

Expected before migration: PASS, because current custom code writes the legacy key.

- [ ] **Step 4: Add CMD conversation raw active-index layout test**

Add to `pkg/db/meta/cmd_conversation_state_test.go`:

```go
func TestCMDConversationActiveIndexKeepsLegacyLayout(t *testing.T) {
    store := openTestMetaStore(t)
    defer store.close(t)
    ctx := context.Background()
    shard := store.db.HashSlot(22)

    state := CMDConversationState{UID: "u-layout", ChannelID: "g1____cmd", ChannelType: 2, ActiveAt: 123, UpdatedAt: 10}
    if err := shard.UpsertCMDConversationState(ctx, state); err != nil {
        t.Fatalf("UpsertCMDConversationState: %v", err)
    }

    legacyKey := encodeConversationActiveIndexKey(22, TableIDCMDConversation, state.UID, state.ActiveAt, state.ChannelID, state.ChannelType)
    if _, ok, err := store.db.get(legacyKey); err != nil || !ok {
        t.Fatalf("legacy cmd active index ok=%v err=%v", ok, err)
    }

    suffixedKey, err := encodeTableIndexKey(22, TableIDCMDConversation, conversationActiveIndexID,
        KeyParts{String(state.UID), Int64Desc(state.ActiveAt), String(state.ChannelID), Int64Ordered(state.ChannelType)},
        KeyParts{String(state.UID), String(state.ChannelID), Int64Ordered(state.ChannelType)})
    if err != nil {
        t.Fatalf("generic suffixed cmd key: %v", err)
    }
    if _, ok, err := store.db.get(suffixedKey); err != nil || ok {
        t.Fatalf("suffixed cmd active index ok=%v err=%v, want missing", ok, err)
    }
}
```

Expected before migration: PASS.

- [ ] **Step 5: Add stale active-index skip tests**

Add to user conversation tests:

```go
func TestUserConversationActiveSkipsStaleIndex(t *testing.T) {
    store := openTestMetaStore(t)
    defer store.close(t)
    ctx := context.Background()
    shard := store.db.HashSlot(23)

    state := UserConversationState{UID: "u-stale", ChannelID: "g1", ChannelType: 2, ActiveAt: 100}
    if err := shard.UpsertUserConversationState(ctx, state); err != nil {
        t.Fatalf("UpsertUserConversationState: %v", err)
    }
    staleKey := encodeConversationActiveIndexKey(23, TableIDConversation, state.UID, 200, state.ChannelID, state.ChannelType)
    batch := store.engine.NewBatch()
    if err := batch.Set(staleKey, nil); err != nil {
        t.Fatalf("set stale active index: %v", err)
    }
    if err := batch.Commit(true); err != nil {
        t.Fatalf("commit stale active index: %v", err)
    }
    batch.Close()

    rows, err := shard.ListUserConversationActive(ctx, state.UID, 10)
    if err != nil || len(rows) != 1 || rows[0].ActiveAt != 100 {
        t.Fatalf("active rows=%#v err=%v", rows, err)
    }
}
```

Add an equivalent CMD test using `TableIDCMDConversation` and `ListCMDConversationActive`.

Expected before migration: PASS.

- [ ] **Step 6: Add malformed active-index error tests**

Add a malformed key under each active-index prefix. Example for user conversation:

```go
func TestUserConversationActiveReturnsCorruptForMalformedIndex(t *testing.T) {
    store := openTestMetaStore(t)
    defer store.close(t)
    ctx := context.Background()
    shard := store.db.HashSlot(24)

    malformed := append(encodeConversationActiveIndexPrefix(24, TableIDConversation, "u-bad"), 0x01)
    batch := store.engine.NewBatch()
    if err := batch.Set(malformed, nil); err != nil {
        t.Fatalf("set malformed index: %v", err)
    }
    if err := batch.Commit(true); err != nil {
        t.Fatalf("commit malformed index: %v", err)
    }
    batch.Close()

    _, err := shard.ListUserConversationActive(ctx, "u-bad", 10)
    if !errors.Is(err, dberrors.ErrCorruptValue) {
        t.Fatalf("ListUserConversationActive err = %v, want corrupt", err)
    }
}
```

Add the equivalent CMD test. If `errors` or `dberrors` imports are missing, add them.

Expected before migration: PASS.

- [ ] **Step 7: Run regression tests before implementation**

Run:

```bash
GOWORK=off go test ./pkg/db/meta -run 'TestUserConversation|TestCMDConversation|TestMetaSchemaValidateAllTables' -count=1
```

Expected: PASS before implementation. These tests lock current behavior.

- [ ] **Step 8: Commit regression tests**

```bash
git add pkg/db/meta/user_conversation_state_test.go pkg/db/meta/cmd_conversation_state_test.go pkg/db/meta/schema_test.go
git commit -m "test(db): cover conversation runtime migration behavior"
```

---

### Task 2: Add Runtime Support For Projected Legacy Indexes

**Files:**
- Modify: `pkg/db/meta/table_runtime.go`
- Modify: `pkg/db/meta/table_runtime_test.go`

- [ ] **Step 1: Add failing runtime test for projected index legacy key**

Add to `pkg/db/meta/table_runtime_test.go` a test table whose index tuple is `(owner, active_at desc, id)` and primary is `(owner, id)`. The index physically stores only the index tuple, and derives primary from index parts:

```go
type runtimeProjectedRow struct {
    Owner    string
    ID       string
    ActiveAt int64
    Value    string
}
```

Add helper `newRuntimeProjectedIndexTable(t *testing.T) Table[runtimeProjectedRow]` with:

- table ID `65032`;
- primary layout `KeyLayout{KeyString, KeyString}`;
- active index ID `2`, layout `KeyLayout{KeyString, KeyInt64Desc, KeyString}`;
- `PrimaryKeyFromIndexParts` projection returning `{parts[0], parts[2]}`;
- index key emitted only when `ActiveAt > 0`.

Write:

```go
func TestTableRuntimeProjectedIndexKeepsLegacyKey(t *testing.T) {
    store := openTestMetaStore(t)
    defer store.close(t)
    table := newRuntimeProjectedIndexTable(t)
    shard := store.db.HashSlot(25)
    ctx := context.Background()

    row := runtimeProjectedRow{Owner: "o", ID: "a", ActiveAt: 100, Value: "v"}
    if err := table.Upsert(ctx, shard, row); err != nil {
        t.Fatalf("Upsert: %v", err)
    }

    legacyKey, err := encodeTableIndexScanPrefix(25, table.Schema().ID, 2, KeyParts{String("o"), Int64Desc(100), String("a")})
    if err != nil {
        t.Fatalf("legacy key: %v", err)
    }
    if _, ok, err := store.db.get(legacyKey); err != nil || !ok {
        t.Fatalf("legacy key ok=%v err=%v", ok, err)
    }

    suffixedKey, err := encodeTableIndexKey(25, table.Schema().ID, 2,
        KeyParts{String("o"), Int64Desc(100), String("a")},
        KeyParts{String("o"), String("a")})
    if err != nil {
        t.Fatalf("suffixed key: %v", err)
    }
    if _, ok, err := store.db.get(suffixedKey); err != nil || ok {
        t.Fatalf("suffixed key ok=%v err=%v, want missing", ok, err)
    }
}
```

Run:

```bash
GOWORK=off go test ./pkg/db/meta -run TestTableRuntimeProjectedIndexKeepsLegacyKey -count=1
```

Expected: FAIL because current runtime appends primary parts for non-`PrimaryFromIndex` indexes.

- [ ] **Step 2: Add failing projected index scan/stale/malformed tests**

Add:

```go
func TestTableRuntimeProjectedIndexScanAndStaleHandling(t *testing.T) { ... }
func TestTableRuntimeProjectedIndexMalformedKeyReturnsCorrupt(t *testing.T) { ... }
```

The scan test should insert two rows for owner `o`, verify newest-first order by `ActiveAt`, then insert a stale legacy index key with a mismatched `ActiveAt` and verify it is skipped.

The malformed test should append one byte to `encodeTableIndexScanPrefix(hashSlot, table.Schema().ID, 2, KeyParts{String("bad")})`, then call `ScanIndex` with prefix `KeyParts{String("bad")}` and assert `errors.Is(err, dberrors.ErrCorruptValue)`.

Run:

```bash
GOWORK=off go test ./pkg/db/meta -run 'TestTableRuntimeProjectedIndex' -count=1
```

Expected: FAIL.

- [ ] **Step 3: Extend `IndexSpec`**

In `pkg/db/meta/table_runtime.go`, add to `IndexSpec[R]`:

```go
// PrimaryKeyFromIndexParts derives primary key parts from legacy index parts.
PrimaryKeyFromIndexParts func(KeyParts) (KeyParts, bool)
// CorruptIndexKeyIsError reports malformed keys under a scan prefix as corrupt.
CorruptIndexKeyIsError bool
```

Validation rules in `normalizeTableSpec`:

- `DescriptorOnly` cannot set either new field.
- `PrimaryFromIndex` and `PrimaryKeyFromIndexParts` are mutually exclusive.
- `PrimaryKeyFromIndexParts` requires `Key != nil` and is allowed only for non-unique or unique indexes whose projection is safe; document with comments that the tuple must uniquely derive the primary key.

- [ ] **Step 4: Preserve legacy projected index key encoding**

Update `indexEntryKey`:

```go
if index.PrimaryFromIndex || index.PrimaryKeyFromIndexParts != nil {
    return encodeTableIndexScanPrefix(hashSlot, t.spec.ID, index.ID, indexParts)
}
return encodeTableIndexKey(hashSlot, t.spec.ID, index.ID, indexParts, primary)
```

Keep `StorePrimaryValue` behavior unchanged.

- [ ] **Step 5: Decode primary keys from projected index parts**

Change `decodeIndexKey` to derive primary parts when `PrimaryKeyFromIndexParts != nil`:

```go
if index.PrimaryKeyFromIndexParts != nil {
    if len(rest) != 0 { ... corrupt ... }
    primaryParts, ok := index.PrimaryKeyFromIndexParts(indexParts)
    if !ok || t.validatePrimaryKey(primaryParts) != nil { ... corrupt ... }
    return indexParts, primaryParts, true
}
```

To preserve malformed-key behavior, refactor `decodeIndexKey` or `scanIndex` so decode failures under the requested prefix return `dberrors.ErrCorruptValue` when `index.CorruptIndexKeyIsError` is true. Do not globally change existing indexes to error on malformed keys unless tests show that behavior is already required.

- [ ] **Step 6: Ensure stale verification still works**

Make sure `rowMatchesIndex` uses `indexPartsForRow(row, index, pk)` so projected indexes compare full emitted active-index parts against the scanned index parts. This should already be true after the channel migration; preserve it.

- [ ] **Step 7: Run runtime tests**

```bash
GOWORK=off go test ./pkg/db/meta -run 'TestTableRuntimeProjectedIndex|TestTableRuntimeScanIndex|TestTableRuntimePrimaryFromIndex' -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit runtime extension**

```bash
git add pkg/db/meta/table_runtime.go pkg/db/meta/table_runtime_test.go
git commit -m "feat(db): support projected legacy meta indexes"
```

---

### Task 3: Move User Conversation Schema And CRUD To Runtime

**Files:**
- Modify: `pkg/db/meta/table_conversation.go`
- Modify: `pkg/db/meta/schema.go`
- Modify: `pkg/db/meta/schema_test.go`

- [ ] **Step 1: Add `conversationTable` spec**

In `pkg/db/meta/table_conversation.go`, add `pkg/db/internal/schema` import and define the typed table near the `UserConversationState` type.

Use table-local column constants if needed for clear descriptors. Example:

```go
const (
    conversationColumnUID uint16 = 1
    conversationColumnChannelID uint16 = 2
    conversationColumnChannelType uint16 = 3
    conversationColumnValue uint16 = 4
    conversationColumnActiveAt uint16 = 5
)
```

Then add:

```go
var conversationTable = registerMetaTable(TableSpec[UserConversationState]{
    ID:   TableIDConversation,
    Name: "conversation",
    Columns: []schema.Column{
        {ID: conversationColumnUID, Name: "uid", Type: schema.TypeString, Required: true},
        {ID: conversationColumnChannelID, Name: "channel_id", Type: schema.TypeString, Required: true},
        {ID: conversationColumnChannelType, Name: "channel_type", Type: schema.TypeInt64, Required: true},
        {ID: conversationColumnValue, Name: "value", Type: schema.TypeBytes},
        {ID: conversationColumnActiveAt, Name: "active_at", Type: schema.TypeInt64},
    },
    Families: []schema.Family{{ID: conversationPrimaryFamilyID, Name: "primary", Columns: []uint16{conversationColumnValue, conversationColumnActiveAt}}},
    Primary: PrimarySpec[UserConversationState]{
        IndexID:  1,
        FamilyID: conversationPrimaryFamilyID,
        Name:     "pk_conversation",
        Columns:  []uint16{conversationColumnUID, conversationColumnChannelID, conversationColumnChannelType},
        Layout:   KeyLayout{KeyString, KeyString, KeyInt64Ordered},
        Key: func(state UserConversationState) KeyParts {
            return KeyParts{String(state.UID), String(state.ChannelID), Int64Ordered(state.ChannelType)}
        },
    },
    Indexes: []IndexSpec[UserConversationState]{
        {
            ID:      conversationActiveIndexID,
            Name:    "idx_conversation_active",
            Columns: []uint16{conversationColumnUID, conversationColumnActiveAt, conversationColumnChannelID, conversationColumnChannelType},
            Layout:  KeyLayout{KeyString, KeyInt64Desc, KeyString, KeyInt64Ordered},
            Key: func(state UserConversationState) (KeyParts, bool) {
                if state.ActiveAt <= 0 {
                    return nil, false
                }
                return KeyParts{String(state.UID), Int64Desc(state.ActiveAt), String(state.ChannelID), Int64Ordered(state.ChannelType)}, true
            },
            PrimaryKeyFromIndexParts: conversationPrimaryFromActiveIndexParts,
            CorruptIndexKeyIsError:   true,
        },
    },
    Validate: validateUserConversationState,
    EncodeValue: func(state UserConversationState) ([]byte, error) {
        return encodeConversationValue(state.ReadSeq, state.DeletedToSeq, state.ActiveAt, state.UpdatedAt), nil
    },
    DecodeValue: func(primary KeyParts, value []byte) (UserConversationState, error) {
        return decodeUserConversationValue(primary[0].S, primary[1].S, primary[2].I64, value)
    },
})

// ConversationTable describes the user conversation table schema.
var ConversationTable = conversationTable.Schema()
```

Add helper:

```go
func conversationPrimaryFromActiveIndexParts(parts KeyParts) (KeyParts, bool) {
    if len(parts) != 4 {
        return nil, false
    }
    return KeyParts{parts[0], parts[2], parts[3]}, true
}
```

- [ ] **Step 2: Remove manual conversation schema registration**

In `pkg/db/meta/schema.go`:

- remove `{Table: ConversationTable}` from `init()`;
- remove `ConversationTable = activeMetaTable(TableIDConversation, "conversation")` from the `var` block.

Do not remove `activeMetaTable` yet if CMD or other tables still use it.

- [ ] **Step 3: Convert `GetUserConversationState`**

Replace manual key/get body with runtime get after existing validation:

```go
state, ok, err := conversationTable.Get(ctx, s, KeyParts{String(uid), String(channelID), Int64Ordered(channelType)})
return state, ok, err
```

- [ ] **Step 4: Add runtime-backed stage helper**

Replace `stageUserConversationState` internals with runtime index helpers while keeping the same signature for `Shard` and `WriteBatch` callers:

```go
func (s *Shard) stageUserConversationState(batch *engine.Batch, primaryKey []byte, existing UserConversationState, exists bool, next UserConversationState) error {
    pk := KeyParts{String(next.UID), String(next.ChannelID), Int64Ordered(next.ChannelType)}
    if exists {
        if err := conversationTable.stageDeleteIndexEntries(batch, s.hashSlot, existing, pk); err != nil {
            return err
        }
    }
    value := encodeConversationValue(next.ReadSeq, next.DeletedToSeq, next.ActiveAt, next.UpdatedAt)
    if err := batch.Set(primaryKey, value); err != nil {
        return err
    }
    return conversationTable.stagePutIndexEntries(batch, s.hashSlot, next, pk, value)
}
```

If direct access to unexported runtime helpers needs minor signature adjustment, keep it package-local and covered by tests.

- [ ] **Step 5: Convert `UpsertUserConversationState`, `Touch`, `Clear`, and `Hide` reads to runtime**

Keep locking, merge, no-op, and idempotency logic unchanged. Replace `getUserConversationStateByKey` calls with `conversationTable.Get` where practical, or keep `getUserConversationStateByKey` as a thin wrapper around `conversationTable.Get` so existing code and `compat.go` compile.

- [ ] **Step 6: Convert active scan to runtime**

Replace manual iterator in `ListUserConversationActive` with:

```go
rows, _, _, err := conversationTable.ScanIndex(ctx, s, conversationActiveIndexID, KeyParts{String(uid)}, nil, limit)
return rows, err
```

Keep existing `validateConversationLimit(limit)` before the scan.

- [ ] **Step 7: Convert primary page to runtime**

Replace manual iterator in `ListUserConversationStatePage` with `ScanPrimaryPrefix`:

```go
var after KeyParts
if cursor != (ConversationCursor{}) {
    after = KeyParts{String(uid), String(cursor.ChannelID), Int64Ordered(cursor.ChannelType)}
}
rows, next, done, err := conversationTable.ScanPrimaryPrefix(ctx, s, KeyParts{String(uid)}, after, limit)
if err != nil {
    return nil, ConversationCursor{}, false, err
}
nextCursor := cursor
if len(next) >= 3 {
    nextCursor = ConversationCursor{ChannelID: next[1].S, ChannelType: next[2].I64}
} else if len(rows) > 0 {
    last := rows[len(rows)-1]
    nextCursor = ConversationCursor{ChannelID: last.ChannelID, ChannelType: last.ChannelType}
}
return rows, nextCursor, done, nil
```

The fallback from `rows[len(rows)-1]` is required because `ScanPrimaryPrefix`
returns an empty cursor when `done == true`, while the existing public API still
returns the last emitted row as the cursor on final pages.

- [ ] **Step 8: Run user conversation focused tests**

```bash
GOWORK=off go test ./pkg/db/meta -run 'TestUserConversation|TestMetaSchemaValidateAllTables|TestTableRuntimeProjectedIndex' -count=1
```

Expected: PASS.

---

### Task 4: Move CMD Conversation Schema And CRUD To Runtime

**Files:**
- Modify: `pkg/db/meta/table_cmd_conversation.go`
- Modify: `pkg/db/meta/schema.go`
- Modify: `pkg/db/meta/schema_test.go`

- [ ] **Step 1: Add `cmdConversationTable` spec**

In `pkg/db/meta/table_cmd_conversation.go`, add `pkg/db/internal/schema` import and table-local column constants, or reuse the conversation column constants if they remain in package scope and names are clear.

Add:

```go
var cmdConversationTable = registerMetaTable(TableSpec[CMDConversationState]{
    ID:   TableIDCMDConversation,
    Name: "cmd_conversation",
    Columns: []schema.Column{...},
    Families: []schema.Family{{ID: cmdConversationPrimaryFamilyID, Name: "primary", Columns: []uint16{conversationColumnValue, conversationColumnActiveAt}}},
    Primary: PrimarySpec[CMDConversationState]{
        IndexID:  1,
        FamilyID: cmdConversationPrimaryFamilyID,
        Name:     "pk_cmd_conversation",
        Columns:  []uint16{conversationColumnUID, conversationColumnChannelID, conversationColumnChannelType},
        Layout:   KeyLayout{KeyString, KeyString, KeyInt64Ordered},
        Key: func(state CMDConversationState) KeyParts {
            return KeyParts{String(state.UID), String(state.ChannelID), Int64Ordered(state.ChannelType)}
        },
    },
    Indexes: []IndexSpec[CMDConversationState]{
        {
            ID:      conversationActiveIndexID,
            Name:    "idx_cmd_conversation_active",
            Columns: []uint16{conversationColumnUID, conversationColumnActiveAt, conversationColumnChannelID, conversationColumnChannelType},
            Layout:  KeyLayout{KeyString, KeyInt64Desc, KeyString, KeyInt64Ordered},
            Key: func(state CMDConversationState) (KeyParts, bool) {
                if state.ActiveAt <= 0 {
                    return nil, false
                }
                return KeyParts{String(state.UID), Int64Desc(state.ActiveAt), String(state.ChannelID), Int64Ordered(state.ChannelType)}, true
            },
            PrimaryKeyFromIndexParts: conversationPrimaryFromActiveIndexParts,
            CorruptIndexKeyIsError:   true,
        },
    },
    Validate: validateCMDConversationState,
    EncodeValue: func(state CMDConversationState) ([]byte, error) {
        return encodeConversationValue(state.ReadSeq, state.DeletedToSeq, state.ActiveAt, state.UpdatedAt), nil
    },
    DecodeValue: func(primary KeyParts, value []byte) (CMDConversationState, error) {
        return decodeCMDConversationValue(primary[0].S, primary[1].S, primary[2].I64, value)
    },
})

// CMDConversationTable describes the command conversation table schema.
var CMDConversationTable = cmdConversationTable.Schema()
```

- [ ] **Step 2: Remove manual CMD schema registration**

In `pkg/db/meta/schema.go`:

- remove `{Table: CMDConversationTable}` from `init()`;
- remove `CMDConversationTable = activeMetaTable(TableIDCMDConversation, "cmd_conversation")` from the `var` block.

If `activeMetaTable` is no longer used by any table, remove it and any now-unused imports/helpers.

- [ ] **Step 3: Convert `GetCMDConversationState`**

Use:

```go
state, ok, err := cmdConversationTable.Get(ctx, s, KeyParts{String(uid), String(channelID), Int64Ordered(channelType)})
return state, ok, err
```

Keep existing validation.

- [ ] **Step 4: Convert CMD stage helper**

Replace `stageCMDConversationState` internals with runtime index helpers matching the user conversation helper, but using `cmdConversationTable` and `cmdConversationPrimaryFamilyID`.

- [ ] **Step 5: Keep CMD merge/read semantics unchanged**

Keep `mergeCMDConversationState` and `AdvanceCMDConversationReadSeq` behavior as-is. Only replace manual row get/write mechanics with runtime-backed helper calls.

- [ ] **Step 6: Convert CMD active scan to runtime**

Use:

```go
rows, _, _, err := cmdConversationTable.ScanIndex(ctx, s, conversationActiveIndexID, KeyParts{String(uid)}, nil, limit)
return rows, err
```

Keep existing validation.

- [ ] **Step 7: Run CMD focused tests**

```bash
GOWORK=off go test ./pkg/db/meta -run 'TestCMDConversation|TestMetaSchemaValidateAllTables|TestTableRuntimeProjectedIndex' -count=1
```

Expected: PASS.

- [ ] **Step 8: Run combined focused tests**

```bash
GOWORK=off go test ./pkg/db/meta -run 'TestUserConversation|TestCMDConversation|TestMetaSchemaValidateAllTables|TestTableRuntime' -count=1
```

Expected: PASS.

---

### Task 5: Clean Up Manual Conversation Boilerplate And Docs

**Files:**
- Modify: `pkg/db/meta/table_conversation.go`
- Modify: `pkg/db/meta/table_cmd_conversation.go`
- Modify: `pkg/db/meta/schema.go`
- Modify: `pkg/db/meta/keys.go` only if helpers become unused
- Modify: `pkg/db/meta/FLOW.md`

- [ ] **Step 1: Find unused conversation helpers**

Run:

```bash
rg -n 'decodeConversationRowKey|decodeConversationActiveIndexKey|encodeConversationRowPrefix|encodeCMDConversationRowPrefix|activeMetaTable' pkg/db/meta
```

Expected: manual decode helpers may become unused after runtime migration. Remove only helpers with no references. Keep encode helpers still used by tests, compat, or raw key assertions.

- [ ] **Step 2: Check imports**

Run:

```bash
gofmt -w pkg/db/meta/table_conversation.go pkg/db/meta/table_cmd_conversation.go pkg/db/meta/schema.go pkg/db/meta/table_runtime.go pkg/db/meta/*conversation*_test.go pkg/db/meta/schema_test.go pkg/db/meta/table_runtime_test.go
go test ./pkg/db/meta -run '^$'
```

Use `GOWORK=off` for the test command if `go test` is run separately:

```bash
GOWORK=off go test ./pkg/db/meta -run '^$'
```

Expected: compile succeeds; no unused imports.

- [ ] **Step 3: Update FLOW**

In `pkg/db/meta/FLOW.md`, change the conversation line from custom primary/index maintenance to runtime-backed wording. Suggested wording:

```md
11. User and CMD conversation tables use the table runtime for primary rows,
    primary-prefix pages, active-index maintenance, and active scans; their typed
    methods keep merge, hide, clear, and read-advance business semantics.
```

- [ ] **Step 4: Run focused tests**

```bash
GOWORK=off go test ./pkg/db/meta -run 'TestUserConversation|TestCMDConversation|TestMetaSchemaValidateAllTables|TestTableRuntime' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit migration**

```bash
git add pkg/db/meta/table_runtime.go pkg/db/meta/table_runtime_test.go \
  pkg/db/meta/table_conversation.go pkg/db/meta/table_cmd_conversation.go \
  pkg/db/meta/schema.go pkg/db/meta/schema_test.go \
  pkg/db/meta/user_conversation_state_test.go pkg/db/meta/cmd_conversation_state_test.go \
  pkg/db/meta/keys.go pkg/db/meta/FLOW.md
git commit -m "refactor(db): migrate conversation metadata to table runtime"
```

If `pkg/db/meta/keys.go` is unchanged, omit it from the final `git add` or allow Git to ignore it.

---

### Task 6: Verification

**Files:**
- No code changes unless verification finds a real issue.

- [ ] **Step 1: Run package tests**

```bash
GOWORK=off go test ./pkg/db/meta -count=1
```

Expected: PASS.

- [ ] **Step 2: Run db tests**

```bash
GOWORK=off go test ./pkg/db/... -count=1
```

Expected: PASS.

- [ ] **Step 3: Check storage boundary**

```bash
rg -n 'github.com/cockroachdb/pebble|pebble\.' pkg/db/meta
```

Expected: no output.

- [ ] **Step 4: Check working tree**

```bash
git status --short
```

Expected: clean.
