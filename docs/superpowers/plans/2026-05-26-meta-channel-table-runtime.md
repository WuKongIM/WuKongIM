# Meta Channel Table Runtime Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Migrate the `pkg/db/meta` channel table onto the meta table runtime while preserving channel cache and public API behavior.

**Architecture:** Add a typed `channelTable` `TableSpec[Channel]` in `table_channel.go`, remove channel from central manual schema registration, and route ordinary channel CRUD/index reads through the runtime. Keep `Batch.UpsertChannel` custom for now so post-commit cache publishing remains unchanged.

**Tech Stack:** Go, `pkg/db/meta` table runtime, `pkg/db/internal/schema`, existing meta engine/cache tests.

---

## Starting Constraints

- Work in `.worktrees/meta-channel-table-runtime` on branch `feature/meta-channel-table-runtime`.
- Run Go commands with `GOWORK=off` because the parent `go.work` does not include nested worktrees.
- Do not touch the unrelated dirty files in the main worktree.
- Follow `pkg/db/meta/FLOW.md`; update it if channel flow wording changes.
- Use TDD: write failing tests first, verify failure, implement, verify pass, commit.

## File Structure

- Modify: `pkg/db/meta/table_channel.go`
  - Owns `Channel`, `channelTable` spec, schema export, CRUD wrappers, cache invalidation, value codec.
- Modify: `pkg/db/meta/schema.go`
  - Remove the manual `ChannelTable` descriptor and central registry entry.
- Modify: `pkg/db/meta/channel_test.go` or existing channel-related test file
  - Add focused stale-index/runtime behavior tests if not already covered.
- Modify: `pkg/db/meta/schema_test.go`
  - Keep `Tables()` assertions passing with registry-backed channel schema.
- Modify: `pkg/db/meta/FLOW.md`
  - Adjust flow item that currently says channel methods remain custom.

---

### Task 1: Add Channel Runtime Regression Tests

**Files:**
- Modify: `pkg/db/meta/channel_test.go` or `pkg/db/meta/table_channel_test.go`
- Modify: `pkg/db/meta/schema_test.go`

- [ ] **Step 1: Inspect existing channel tests**

Run:

```bash
rg -n 'TestChannel|Channel' pkg/db/meta/*test.go
```

Expected: find existing channel CRUD/index/cache tests or identify where to add a new channel test file.

- [ ] **Step 2: Write failing stale index test**

Add this test in the existing channel test file, or create `pkg/db/meta/table_channel_test.go` if no focused file exists:

```go
func TestChannelListByChannelIDSkipsStaleRuntimeIndex(t *testing.T) {
    store := openTestMetaStore(t)
    defer store.close(t)
    ctx := context.Background()
    shard := store.db.HashSlot(31)

    if err := shard.UpsertChannel(ctx, Channel{ChannelID: "ch", ChannelType: 1, Ban: true}); err != nil {
        t.Fatalf("UpsertChannel: %v", err)
    }

    staleKey := encodeChannelIDIndexKey(31, "stale", 99)
    batch := store.engine.NewBatch()
    if err := batch.Set(staleKey, nil); err != nil {
        t.Fatalf("set stale index: %v", err)
    }
    if err := batch.Commit(true); err != nil {
        t.Fatalf("commit stale index: %v", err)
    }
    batch.Close()

    rows, err := shard.ListChannelsByChannelID(ctx, "stale")
    if err != nil || len(rows) != 0 {
        t.Fatalf("stale rows=%#v err=%v", rows, err)
    }
}
```

- [ ] **Step 3: Add schema assertion if useful**

In `pkg/db/meta/schema_test.go`, assert `ChannelTable` still appears with `channelIDIndexID` after registration. Keep the assertion small and table-specific.

- [ ] **Step 4: Run tests to establish baseline**

Run:

```bash
GOWORK=off go test ./pkg/db/meta -run 'TestChannel|TestMetaSchemaValidateAllTables' -count=1
```

Expected: existing tests pass. The new stale-index test may already pass with custom code; if so, keep it as a regression test and proceed.

- [ ] **Step 5: Commit tests if they pass before implementation**

```bash
git add pkg/db/meta/*channel*_test.go pkg/db/meta/schema_test.go
git commit -m "test(db): cover channel runtime migration behavior"
```

---

### Task 2: Define Channel TableSpec And Remove Manual Schema Entry

**Files:**
- Modify: `pkg/db/meta/table_channel.go`
- Modify: `pkg/db/meta/schema.go`

- [ ] **Step 1: Add `channelTable` spec**

In `pkg/db/meta/table_channel.go`, import `pkg/db/internal/schema` and add near the `Channel` type:

```go
var channelTable = registerMetaTable(TableSpec[Channel]{
    ID:   TableIDChannel,
    Name: "channel",
    Columns: []schema.Column{
        {ID: columnIDStringKey, Name: "channel_id", Type: schema.TypeString, Required: true},
        {ID: columnIDIntKey, Name: "channel_type", Type: schema.TypeInt64, Required: true},
        {ID: columnIDValue, Name: "value", Type: schema.TypeBytes},
        {ID: columnIDUpdatedAt, Name: "updated_at", Type: schema.TypeInt64},
    },
    Families: []schema.Family{{ID: channelPrimaryFamilyID, Name: "primary", Columns: []uint16{columnIDValue, columnIDUpdatedAt}}},
    Primary: PrimarySpec[Channel]{
        IndexID:  channelPrimaryIndexID,
        FamilyID: channelPrimaryFamilyID,
        Name:     "pk_channel",
        Columns:  []uint16{columnIDStringKey, columnIDIntKey},
        Layout:   KeyLayout{KeyString, KeyInt64Ordered},
        Key: func(channel Channel) KeyParts {
            return KeyParts{String(channel.ChannelID), Int64Ordered(channel.ChannelType)}
        },
    },
    Indexes: []IndexSpec[Channel]{
        {
            ID:      channelIDIndexID,
            Name:    "idx_channel_id",
            Columns: []uint16{columnIDStringKey, columnIDIntKey},
            Layout:  KeyLayout{KeyString, KeyInt64Ordered},
            Key: func(channel Channel) (KeyParts, bool) {
                return KeyParts{String(channel.ChannelID), Int64Ordered(channel.ChannelType)}, true
            },
        },
    },
    Validate: validateChannel,
    EncodeValue: func(channel Channel) ([]byte, error) { return encodeChannelValue(channel), nil },
    DecodeValue: func(primary KeyParts, value []byte) (Channel, error) {
        return decodeChannelValue(primary[0].S, primary[1].I64, value)
    },
})

// ChannelTable describes the channel table schema.
var ChannelTable = channelTable.Schema()
```

- [ ] **Step 2: Remove manual channel descriptor from `schema.go`**

Delete the `ChannelTable = schema.Table{...}` var entry and remove `{Table: ChannelTable}` from the `init()` descriptor list.

- [ ] **Step 3: Run schema tests**

```bash
GOWORK=off go test ./pkg/db/meta -run TestMetaSchemaValidateAllTables -count=1
```

Expected: PASS. If registration panics, fix the descriptor IDs/layout until schema validation passes.

---

### Task 3: Route Channel CRUD Through Runtime

**Files:**
- Modify: `pkg/db/meta/table_channel.go`

- [ ] **Step 1: Convert create/upsert/update methods**

Replace the write bodies with runtime calls while preserving cache invalidation:

```go
func (s *Shard) CreateChannel(ctx context.Context, channel Channel) error {
    if err := channelTable.Create(ctx, s, channel); err != nil {
        return err
    }
    s.db.deleteChannelCache(channel.ChannelID, channel.ChannelType)
    return nil
}
```

Use the same pattern for `UpsertChannel` and `UpdateChannel`.

- [ ] **Step 2: Convert get/delete/list methods**

Use:

```go
return channelTable.Get(ctx, s, KeyParts{String(channelID), Int64Ordered(channelType)})
```

For `ListChannelsByChannelID`, use:

```go
rows, _, _, err := channelTable.ScanIndex(ctx, s, channelIDIndexID, KeyParts{String(channelID)}, nil, 0)
```

If `ScanIndex` treats `limit <= 0` as empty, use a large limit only temporarily and add a TODO to introduce an unlimited index scan. Prefer adding `ScanIndexAll` only if needed by tests.

- [ ] **Step 3: Keep batch channel helper custom**

Do not remove `stageChannel` yet. `Batch.UpsertChannel` depends on `state.channelPublishes` after commit.

- [ ] **Step 4: Run channel tests**

```bash
GOWORK=off go test ./pkg/db/meta -run 'TestChannel|TestMetaBatch' -count=1
```

Expected: PASS. If index list returns empty because `ScanIndex` lacks unlimited mode, implement the smallest runtime extension in Task 4.

---

### Task 4: Add Unlimited Index Scan Only If Needed

**Files:**
- Modify: `pkg/db/meta/table_runtime.go`
- Modify: `pkg/db/meta/table_runtime_test.go`

- [ ] **Step 1: Write failing runtime test**

If Task 3 shows `ScanIndex` cannot list all rows, add:

```go
func TestTableRuntimeScanIndexAll(t *testing.T) {
    store := openTestMetaStore(t)
    defer store.close(t)
    table := newRuntimeTestTable(t)
    shard := store.db.HashSlot(32)
    ctx := context.Background()

    _ = table.Upsert(ctx, shard, runtimeTestRow{ID: "a", Owner: "o", Value: "v1"})
    _ = table.Upsert(ctx, shard, runtimeTestRow{ID: "b", Owner: "o", Value: "v2"})

    rows, err := table.ScanIndexAll(ctx, shard, 2, KeyParts{String("o")})
    if err != nil || len(rows) != 2 {
        t.Fatalf("ScanIndexAll rows=%#v err=%v", rows, err)
    }
}
```

- [ ] **Step 2: Implement minimal `ScanIndexAll`**

Add a method that shares the same scan logic as `ScanIndex` but has unlimited mode. Do not change existing `ScanIndex(limit<=0)` behavior because plugin binding public API relies on invalid limit checks at a higher layer.

- [ ] **Step 3: Run runtime tests**

```bash
GOWORK=off go test ./pkg/db/meta -run TestTableRuntimeScanIndexAll -count=1
```

Expected: PASS.

---

### Task 5: Clean Up Channel-Specific Boilerplate And Docs

**Files:**
- Modify: `pkg/db/meta/table_channel.go`
- Modify: `pkg/db/meta/keys.go`
- Modify: `pkg/db/meta/FLOW.md`

- [ ] **Step 1: Find unused channel helpers**

```bash
rg -n 'encodeChannelRowKey|decodeChannelIDIndexType|writeChannelLocked|stageChannel' pkg/db/meta
```

Expected: `encodeChannelRowKey` and `stageChannel` may still be used by batch/compat. Remove only helpers with no references.

- [ ] **Step 2: Update FLOW**

Change the flow line from “Channel typed methods remain custom...” to mention that channel ordinary CRUD uses the table runtime while batch/cache orchestration remains custom.

- [ ] **Step 3: Run focused tests**

```bash
GOWORK=off go test ./pkg/db/meta -run 'TestChannel|TestMetaBatch|TestMetaSchemaValidateAllTables' -count=1
```

Expected: PASS.

- [ ] **Step 4: Commit channel migration**

```bash
git add pkg/db/meta/table_channel.go pkg/db/meta/schema.go pkg/db/meta/table_runtime.go pkg/db/meta/table_runtime_test.go pkg/db/meta/keys.go pkg/db/meta/FLOW.md pkg/db/meta/*channel*_test.go pkg/db/meta/schema_test.go
git commit -m "refactor(db): migrate channel metadata to table runtime"
```

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
