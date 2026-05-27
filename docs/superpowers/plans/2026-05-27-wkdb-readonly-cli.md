# wkdb Read-Only CLI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a read-only `wkdb` CLI that opens one node data directory offline and inspects local metadata and message stores with bounded SQL-like queries.

**Architecture:** Add read-only support to the shared Pebble engine, expose table-specific inspection helpers from `pkg/db/meta` and `pkg/db/message`, then build a small `pkg/db/inspect` planner/parser layer consumed by `cmd/wkdb`. The CLI owns only flag parsing, config-path resolution, REPL handling, and output formatting.

**Tech Stack:** Go `flag`, standard-library parsing/encoding, Pebble read-only open through `pkg/db/internal/engine`, existing `pkg/db/meta`, `pkg/db/message`, and `pkg/cluster.HashSlotForKey`.

---

## File Structure

- Modify `pkg/db/internal/engine/db.go`
  - Add a documented `Options.ReadOnly` field.
  - Pass `ReadOnly` through to Pebble options.

- Modify `pkg/db/internal/engine/engine_test.go`
  - Cover read-only reopen and failed write attempts.

- Create `pkg/db/meta/inspect.go`
  - Add table-local read-only scan types.
  - Implement table schema lookup, row conversion, partition scans, and local bounded scans using existing unexported table runtime handles.

- Create `pkg/db/meta/inspect_test.go`
  - Cover user, device, channel, subscriber, conversation, runtime metadata, plugin binding, and migration table scans.

- Create `pkg/db/message/inspect.go`
  - Add read-only catalog and message row inspection helpers.
  - Convert message rows to stable column maps.

- Create `pkg/db/message/inspect_test.go`
  - Cover catalog scanning, channel message scanning, sequence cursors, and required `channel_key`.

- Create `pkg/db/inspect/types.go`
  - Define public store options, query AST, result rows, stats, cursor payloads, and errors.

- Create `pkg/db/inspect/store.go`
  - Open meta/message engines read-only.
  - Own close lifecycle for both physical stores.

- Create `pkg/db/inspect/parser.go`
  - Parse the supported SQL-like subset.

- Create `pkg/db/inspect/planner.go`
  - Resolve tables, partition keys, hash-slot derivation, scan modes, limits, and cursor validation.

- Create `pkg/db/inspect/cursor.go`
  - Encode and decode opaque cursor strings with query-hash validation.

- Create `pkg/db/inspect/execute.go`
  - Dispatch planned scans to `pkg/db/meta` and `pkg/db/message` inspection helpers.

- Create tests:
  - `pkg/db/inspect/parser_test.go`
  - `pkg/db/inspect/planner_test.go`
  - `pkg/db/inspect/cursor_test.go`
  - `pkg/db/inspect/store_test.go`
  - `pkg/db/inspect/execute_test.go`

- Create `cmd/wkdb/main.go`
  - Add `query` and `repl` subcommands.

- Create `cmd/wkdb/config.go`
  - Resolve `--data-dir`, `--meta-path`, `--message-path`, `--config`, and `--hash-slot-count`.
  - Parse only the storage/hash-slot keys needed by `wkdb` from `KEY=value` config files and environment variables.

- Create `cmd/wkdb/output.go`
  - Render `table`, `json`, and `jsonl`.

- Create `cmd/wkdb/main_test.go`, `cmd/wkdb/config_test.go`, and `cmd/wkdb/output_test.go`
  - Cover CLI behavior without running an app cluster.

- Create `cmd/wkdb/README.md`
  - Document examples, local-only semantics, limit behavior, and cursor pagination.

---

## Task 1: Engine Read-Only Open

**Files:**
- Modify: `pkg/db/internal/engine/db.go`
- Modify: `pkg/db/internal/engine/engine_test.go`

- [ ] **Step 1: Write failing read-only engine tests**

Add this test to `pkg/db/internal/engine/engine_test.go`:

```go
func TestOpenReadOnlyRejectsWrites(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir, Options{})
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	batch := db.NewBatch()
	if err := batch.Set([]byte("k"), []byte("v")); err != nil {
		t.Fatalf("Set(): %v", err)
	}
	if err := batch.Commit(true); err != nil {
		t.Fatalf("Commit(): %v", err)
	}
	if err := batch.Close(); err != nil {
		t.Fatalf("Close batch(): %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}

	readOnly, err := Open(dir, Options{ReadOnly: true})
	if err != nil {
		t.Fatalf("Open(read-only): %v", err)
	}
	defer readOnly.Close()

	got, ok, err := readOnly.Get([]byte("k"))
	if err != nil {
		t.Fatalf("Get(): %v", err)
	}
	if !ok || string(got) != "v" {
		t.Fatalf("Get() = %q, %v; want v, true", got, ok)
	}
	roBatch := readOnly.NewBatch()
	defer roBatch.Close()
	if err := roBatch.Set([]byte("next"), []byte("value")); err != nil {
		t.Fatalf("Set() should stage before Pebble rejects commit: %v", err)
	}
	if err := roBatch.Commit(true); err == nil {
		t.Fatal("Commit(read-only) = nil, want error")
	}
}
```

- [ ] **Step 2: Run the failing test**

Run:

```bash
go test ./pkg/db/internal/engine -run TestOpenReadOnlyRejectsWrites -count=1
```

Expected: compile failure because `Options.ReadOnly` does not exist.

- [ ] **Step 3: Add the read-only option**

Update `pkg/db/internal/engine/db.go`:

```go
// Options controls Pebble engine tuning.
type Options struct {
	// CacheSize configures Pebble block cache bytes.
	CacheSize int64
	// MemTableSize configures Pebble memtable bytes.
	MemTableSize int64
	// ReadOnly opens the engine without allowing writes or background compactions.
	ReadOnly bool
}
```

Update `pebbleOptions` in the same file:

```go
	popts := &pebble.Options{
		CacheSize:                   opts.CacheSize,
		MemTableSize:                uint64(opts.MemTableSize),
		MemTableStopWritesThreshold: 4,
		L0CompactionThreshold:       8,
		L0StopWritesThreshold:       24,
		ReadOnly:                    opts.ReadOnly,
	}
```

- [ ] **Step 4: Verify the engine test passes**

Run:

```bash
go test ./pkg/db/internal/engine -run TestOpenReadOnlyRejectsWrites -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/db/internal/engine/db.go pkg/db/internal/engine/engine_test.go
git commit -m "feat(db): support read-only engine open"
```

---

## Task 2: Metadata Inspection API

**Files:**
- Create: `pkg/db/meta/inspect.go`
- Create: `pkg/db/meta/inspect_test.go`

- [ ] **Step 1: Write failing tests for metadata scans**

Create `pkg/db/meta/inspect_test.go` with these tests:

```go
package meta

import (
	"context"
	"testing"
)

func TestInspectScanUserByHashSlot(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if err := db.HashSlot(7).UpsertUser(ctx, User{UID: "u7", Token: "tok", DeviceFlag: 1, DeviceLevel: 2}); err != nil {
		t.Fatalf("UpsertUser(): %v", err)
	}
	result, err := InspectScan(ctx, db, InspectScanRequest{
		Table:       "user",
		HashSlot:    7,
		HashSlotSet: true,
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("InspectScan(): %v", err)
	}
	if len(result.Rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1", len(result.Rows))
	}
	if result.Rows[0]["uid"] != "u7" || result.Rows[0]["token"] != "tok" {
		t.Fatalf("row = %#v", result.Rows[0])
	}
	if result.ScannedRows != 1 || !result.Done {
		t.Fatalf("stats scanned=%d done=%v", result.ScannedRows, result.Done)
	}
}

func TestInspectScanUserLocalBoundedLimitAcrossHashSlots(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	for _, item := range []struct {
		slot HashSlot
		uid  string
	}{
		{slot: 1, uid: "u1"},
		{slot: 2, uid: "u2"},
		{slot: 3, uid: "u3"},
	} {
		if err := db.HashSlot(item.slot).UpsertUser(ctx, User{UID: item.uid, Token: item.uid}); err != nil {
			t.Fatalf("UpsertUser(%s): %v", item.uid, err)
		}
	}
	result, err := InspectScan(ctx, db, InspectScanRequest{
		Table:         "user",
		HashSlotCount: 4,
		Limit:         2,
	})
	if err != nil {
		t.Fatalf("InspectScan(): %v", err)
	}
	if len(result.Rows) != 2 {
		t.Fatalf("len(rows) = %d, want 2", len(result.Rows))
	}
	if result.Done {
		t.Fatal("Done = true, want more rows")
	}
	if result.Next == nil || result.Next.HashSlot == 0 {
		t.Fatalf("Next = %#v, want continuation", result.Next)
	}
}

func TestInspectScanRejectsUnknownTable(t *testing.T) {
	db := openTestDB(t)
	_, err := InspectScan(context.Background(), db, InspectScanRequest{Table: "missing", HashSlot: 1, Limit: 1})
	if err == nil {
		t.Fatal("InspectScan() error = nil, want error")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
go test ./pkg/db/meta -run 'TestInspectScan' -count=1
```

Expected: compile failure because `InspectScan` and related types do not exist.

- [ ] **Step 3: Add metadata inspect types and dispatch**

Create `pkg/db/meta/inspect.go`:

```go
package meta

import (
	"context"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

// InspectRow is a stable column map returned by read-only metadata inspection.
type InspectRow map[string]any

// InspectCursor identifies the last returned primary row for metadata inspection.
type InspectCursor struct {
	// HashSlot is the hash-slot partition that produced the last returned row.
	HashSlot HashSlot
	// Primary stores table-specific primary key parts in schema order.
	Primary []any
}

// InspectScanRequest describes one read-only metadata scan.
type InspectScanRequest struct {
	// Table is the metadata table name without the "meta." prefix.
	Table string
	// HashSlot restricts the scan to one partition when HashSlotSet is true.
	HashSlot HashSlot
	// HashSlotSet reports whether HashSlot was explicitly selected by the planner.
	HashSlotSet bool
	// HashSlotCount is required for local bounded scans.
	HashSlotCount uint16
	// Filters contains normalized equality and range filters.
	Filters map[string]any
	// After resumes the scan after a prior row.
	After *InspectCursor
	// Limit caps returned rows for this scan.
	Limit int
}

// InspectScanResult is one page of metadata inspection rows.
type InspectScanResult struct {
	Rows             []InspectRow
	Next             *InspectCursor
	Done             bool
	ScannedRows      int
	ScannedHashSlots []HashSlot
}

// InspectScan reads metadata rows without mutating store state.
func InspectScan(ctx context.Context, db *MetaDB, req InspectScanRequest) (InspectScanResult, error) {
	if db == nil || db.engine == nil {
		return InspectScanResult{}, dberrors.ErrClosed
	}
	if req.Limit <= 0 {
		req.Limit = 100
	}
	if req.HashSlotSet {
		return inspectScanOneHashSlot(ctx, db, req.HashSlot, req)
	}
	if req.HashSlotCount == 0 {
		return InspectScanResult{}, fmt.Errorf("%w: hash slot count required", dberrors.ErrInvalidArgument)
	}
	return inspectScanLocalBounded(ctx, db, req)
}
```

- [ ] **Step 4: Implement local bounded scan and user row conversion**

Append this code to `pkg/db/meta/inspect.go`:

```go
func inspectScanLocalBounded(ctx context.Context, db *MetaDB, req InspectScanRequest) (InspectScanResult, error) {
	startSlot := HashSlot(0)
	if req.After != nil {
		startSlot = req.After.HashSlot
	}
	out := InspectScanResult{Done: true}
	for slot := startSlot; slot < HashSlot(req.HashSlotCount); slot++ {
		pageReq := req
		pageReq.HashSlot = slot
		pageReq.HashSlotSet = true
		if req.After == nil || req.After.HashSlot != slot {
			pageReq.After = nil
		}
		pageReq.Limit = req.Limit - len(out.Rows)
		page, err := inspectScanOneHashSlot(ctx, db, slot, pageReq)
		if err != nil {
			return InspectScanResult{}, err
		}
		if page.ScannedRows > 0 || len(page.Rows) > 0 {
			out.ScannedHashSlots = append(out.ScannedHashSlots, slot)
		}
		out.ScannedRows += page.ScannedRows
		out.Rows = append(out.Rows, page.Rows...)
		if len(out.Rows) >= req.Limit {
			out.Next = page.Next
			out.Done = page.Done
			if out.Next == nil && !page.Done {
				out.Next = &InspectCursor{HashSlot: slot}
			}
			return out, nil
		}
		if !page.Done {
			out.Next = page.Next
			out.Done = false
			return out, nil
		}
	}
	return out, nil
}

func inspectScanOneHashSlot(ctx context.Context, db *MetaDB, slot HashSlot, req InspectScanRequest) (InspectScanResult, error) {
	switch req.Table {
	case "user":
		return inspectScanUsers(ctx, db.HashSlot(slot), slot, req)
	default:
		return InspectScanResult{}, fmt.Errorf("%w: unknown inspect table %q", dberrors.ErrInvalidArgument, req.Table)
	}
}

func inspectScanUsers(ctx context.Context, shard *Shard, slot HashSlot, req InspectScanRequest) (InspectScanResult, error) {
	after := ""
	if req.After != nil && len(req.After.Primary) > 0 {
		if value, ok := req.After.Primary[0].(string); ok {
			after = value
		}
	}
	rows, cursor, done, err := shard.ListUsersPage(ctx, after, req.Limit)
	if err != nil {
		return InspectScanResult{}, err
	}
	out := InspectScanResult{
		Rows:             make([]InspectRow, 0, len(rows)),
		Done:             done,
		ScannedRows:      len(rows),
		ScannedHashSlots: []HashSlot{slot},
	}
	for _, row := range rows {
		if !inspectRowMatches(map[string]any{"uid": row.UID, "token": row.Token}, req.Filters) {
			continue
		}
		out.Rows = append(out.Rows, inspectUserRow(row))
	}
	if !done {
		out.Next = &InspectCursor{HashSlot: slot, Primary: []any{cursor}}
	}
	return out, nil
}

func inspectUserRow(row User) InspectRow {
	return InspectRow{
		"uid":          row.UID,
		"token":        row.Token,
		"device_flag":  row.DeviceFlag,
		"device_level": row.DeviceLevel,
	}
}

func inspectRowMatches(row map[string]any, filters map[string]any) bool {
	for key, want := range filters {
		if got, ok := row[key]; !ok || got != want {
			return false
		}
	}
	return true
}
```

- [ ] **Step 5: Run metadata inspect tests**

Run:

```bash
go test ./pkg/db/meta -run 'TestInspectScan' -count=1
```

Expected: PASS for the initial user table coverage.

- [ ] **Step 6: Commit**

```bash
git add pkg/db/meta/inspect.go pkg/db/meta/inspect_test.go
git commit -m "feat(db): add metadata inspection scan"
```

---

## Task 3: Metadata Inspection Table Coverage

**Files:**
- Modify: `pkg/db/meta/inspect.go`
- Modify: `pkg/db/meta/inspect_test.go`

- [ ] **Step 1: Add table coverage tests**

Extend `pkg/db/meta/inspect_test.go` with one focused test per table:

```go
func TestInspectScanChannelByPrefix(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if err := db.HashSlot(9).UpsertChannel(ctx, Channel{ChannelID: "g1", ChannelType: 2, Ban: 1, SubscriberMutationVersion: 3}); err != nil {
		t.Fatalf("UpsertChannel(): %v", err)
	}
	result, err := InspectScan(ctx, db, InspectScanRequest{
		Table:       "channel",
		HashSlot:    9,
		HashSlotSet: true,
		Filters:     map[string]any{"channel_id": "g1"},
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("InspectScan(channel): %v", err)
	}
	if len(result.Rows) != 1 || result.Rows[0]["channel_id"] != "g1" || result.Rows[0]["subscriber_mutation_version"] != uint64(3) {
		t.Fatalf("rows = %#v", result.Rows)
	}
}

func TestInspectScanDeviceByUID(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if err := db.HashSlot(4).UpsertDevice(ctx, Device{UID: "u1", DeviceFlag: 1, Token: "tok", DeviceLevel: 2}); err != nil {
		t.Fatalf("UpsertDevice(): %v", err)
	}
	result, err := InspectScan(ctx, db, InspectScanRequest{
		Table:       "device",
		HashSlot:    4,
		HashSlotSet: true,
		Filters:     map[string]any{"uid": "u1"},
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("InspectScan(device): %v", err)
	}
	if len(result.Rows) != 1 || result.Rows[0]["device_flag"] != int64(1) {
		t.Fatalf("rows = %#v", result.Rows)
	}
}

func TestInspectTablesIncludesRegisteredSchemas(t *testing.T) {
	tables := InspectTables()
	names := make(map[string]struct{}, len(tables))
	for _, table := range tables {
		names[table.Name] = struct{}{}
	}
	for _, name := range []string{"user", "device", "channel", "subscriber", "conversation", "cmd_conversation", "plugin_binding", "channel_runtime_meta", "channel_migration", "hashslot_migration"} {
		if _, ok := names[name]; !ok {
			t.Fatalf("InspectTables() missing %s: %#v", name, tables)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
go test ./pkg/db/meta -run 'TestInspectScan|TestInspectTables' -count=1
```

Expected: failures for unsupported tables and missing `InspectTables`.

- [ ] **Step 3: Add schema catalog and table dispatch**

Add this function to `pkg/db/meta/inspect.go`:

```go
// InspectTables returns metadata table schemas available to the read-only CLI.
func InspectTables() []schema.Table {
	return Tables()
}
```

Add the required import:

```go
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/schema"
```

Expand `inspectScanOneHashSlot`:

```go
	switch req.Table {
	case "user":
		return inspectScanUsers(ctx, db.HashSlot(slot), slot, req)
	case "device":
		return inspectScanDevices(ctx, db.HashSlot(slot), slot, req)
	case "channel":
		return inspectScanChannels(ctx, db.HashSlot(slot), slot, req)
	case "channel_runtime_meta":
		return inspectScanChannelRuntimeMeta(ctx, db.HashSlot(slot), slot, req)
	case "subscriber":
		return inspectScanSubscribers(ctx, db.HashSlot(slot), slot, req)
	case "conversation":
		return inspectScanConversations(ctx, db.HashSlot(slot), slot, req)
	case "cmd_conversation":
		return inspectScanCMDConversations(ctx, db.HashSlot(slot), slot, req)
	case "plugin_binding":
		return inspectScanPluginBindings(ctx, db.HashSlot(slot), slot, req)
	case "channel_migration":
		return inspectScanChannelMigrations(ctx, db.HashSlot(slot), slot, req)
	case "hashslot_migration":
		return inspectScanHashSlotMigrations(ctx, db.HashSlot(slot), slot, req)
	default:
		return InspectScanResult{}, fmt.Errorf("%w: unknown inspect table %q", dberrors.ErrInvalidArgument, req.Table)
	}
```

- [ ] **Step 4: Add table-specific scanners**

Implement these helpers in `pkg/db/meta/inspect.go`. Each helper uses the table runtime already present in this package and converts typed rows to stable column names:

```go
func inspectScanDevices(ctx context.Context, shard *Shard, slot HashSlot, req InspectScanRequest) (InspectScanResult, error) {
	var after KeyParts
	if req.After != nil && len(req.After.Primary) >= 2 {
		after = KeyParts{String(req.After.Primary[0].(string)), Int64Ordered(req.After.Primary[1].(int64))}
	}
	var prefix KeyParts
	if uid, ok := req.Filters["uid"].(string); ok {
		prefix = KeyParts{String(uid)}
	}
	rows, cursor, done, err := deviceTable.ScanPrimaryPrefix(ctx, shard, prefix, after, req.Limit)
	if err != nil {
		return InspectScanResult{}, err
	}
	out := inspectResultFromRows(slot, rows, done, cursor, inspectDeviceRow)
	out.ScannedRows = len(rows)
	return out, nil
}

func inspectDeviceRow(row Device) InspectRow {
	return InspectRow{
		"uid":          row.UID,
		"device_flag":  row.DeviceFlag,
		"token":        row.Token,
		"device_level": row.DeviceLevel,
	}
}

func inspectScanChannels(ctx context.Context, shard *Shard, slot HashSlot, req InspectScanRequest) (InspectScanResult, error) {
	var prefix KeyParts
	if channelID, ok := req.Filters["channel_id"].(string); ok {
		prefix = KeyParts{String(channelID)}
	}
	rows, cursor, done, err := channelTable.ScanPrimaryPrefix(ctx, shard, prefix, nil, req.Limit)
	if err != nil {
		return InspectScanResult{}, err
	}
	return inspectResultFromRows(slot, rows, done, cursor, inspectChannelRow), nil
}

func inspectChannelRow(row Channel) InspectRow {
	return InspectRow{
		"channel_id":                  row.ChannelID,
		"channel_type":                row.ChannelType,
		"ban":                         row.Ban,
		"disband":                     row.Disband,
		"send_ban":                    row.SendBan,
		"allow_stranger":              row.AllowStranger,
		"subscriber_mutation_version": row.SubscriberMutationVersion,
	}
}
```

Implement the remaining scanners with these exact row converters:

```go
func inspectSubscriberRow(row Subscriber) InspectRow {
	return InspectRow{"channel_id": row.ChannelID, "channel_type": row.ChannelType, "uid": row.UID}
}

func inspectConversationRow(row UserConversationState) InspectRow {
	return InspectRow{"uid": row.UID, "channel_id": row.ChannelID, "channel_type": row.ChannelType, "read_seq": row.ReadSeq, "unread": row.Unread, "timestamp": row.Timestamp, "version": row.Version, "active_at": row.ActiveAt, "deleted_to_seq": row.DeletedToSeq}
}

func inspectCMDConversationRow(row CMDConversationState) InspectRow {
	return InspectRow{"uid": row.UID, "channel_id": row.ChannelID, "channel_type": row.ChannelType, "read_seq": row.ReadSeq, "updated_at": row.UpdatedAt}
}

func inspectPluginBindingRow(row PluginUserBinding) InspectRow {
	return InspectRow{"uid": row.UID, "plugin_no": row.PluginNo, "enabled": row.Enabled, "version": row.Version, "updated_at": row.UpdatedAt}
}

func inspectChannelRuntimeMetaRow(row ChannelRuntimeMeta) InspectRow {
	return InspectRow{"channel_id": row.ChannelID, "channel_type": row.ChannelType, "channel_epoch": row.ChannelEpoch, "leader_epoch": row.LeaderEpoch, "route_generation": row.RouteGeneration, "replicas": row.Replicas, "isr": row.ISR, "leader": row.Leader, "min_isr": row.MinISR, "status": row.Status, "features": row.Features, "lease_until_ms": row.LeaseUntilMS, "retention_through_seq": row.RetentionThroughSeq, "retention_updated_at_ms": row.RetentionUpdatedAtMS, "write_fence_token": row.WriteFenceToken, "write_fence_version": row.WriteFenceVersion, "write_fence_reason": row.WriteFenceReason, "write_fence_until_ms": row.WriteFenceUntilMS}
}

func inspectChannelMigrationRow(row ChannelMigrationTask) InspectRow {
	return InspectRow{"channel_id": row.ChannelID, "channel_type": row.ChannelType, "task_id": row.TaskID, "kind": row.Kind, "status": row.Status, "phase": row.Phase, "source_node_id": row.SourceNodeID, "target_node_id": row.TargetNodeID, "leader_id": row.LeaderID, "epoch": row.Epoch, "created_at_ms": row.CreatedAtMS, "updated_at_ms": row.UpdatedAtMS, "completed_at_ms": row.CompletedAtMS, "owner_node_id": row.OwnerNodeID, "owner_lease_until_ms": row.OwnerLeaseUntilMS}
}

func inspectHashSlotMigrationRow(row HashSlotMigrationState) InspectRow {
	return InspectRow{"hash_slot": row.HashSlot, "source_slot": row.SourceSlot, "target_slot": row.TargetSlot, "phase": row.Phase, "version": row.Version, "updated_at_ms": row.UpdatedAtMS}
}
```

For each scanner, use the matching unexported table runtime variable in this package (`subscriberTable`, `conversationTable`, `cmdConversationTable`, `pluginBindingTable`, `channelRuntimeMetaTable`, `channelMigrationTable`, and `hashSlotMigrationTable`) and `ScanPrimary` or `ScanPrimaryPrefix` according to the table primary key. Slice fields remain slices; `cmd/wkdb` JSON-encodes them for table output.

- [ ] **Step 5: Run metadata package tests**

Run:

```bash
go test ./pkg/db/meta -run 'TestInspectScan|TestInspectTables' -count=1
```

Expected: PASS.

- [ ] **Step 6: Run all metadata tests**

Run:

```bash
go test ./pkg/db/meta -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add pkg/db/meta/inspect.go pkg/db/meta/inspect_test.go
git commit -m "feat(db): cover metadata inspect tables"
```

---

## Task 4: Message Inspection API

**Files:**
- Create: `pkg/db/message/inspect.go`
- Create: `pkg/db/message/inspect_test.go`

- [ ] **Step 1: Write failing message inspect tests**

Create `pkg/db/message/inspect_test.go`:

```go
package message

import (
	"context"
	"testing"
)

func TestInspectListChannels(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	log := db.Channel(ChannelKey("g1:2"), ChannelID{ID: "g1", Type: 2})
	if _, err := log.Append(ctx, []Record{{ID: 100, Payload: []byte("hello")}}, AppendOptions{}); err != nil {
		t.Fatalf("Append(): %v", err)
	}
	result, err := InspectChannels(ctx, db, InspectMessageRequest{Limit: 10})
	if err != nil {
		t.Fatalf("InspectChannels(): %v", err)
	}
	if len(result.Rows) != 1 || result.Rows[0]["channel_key"] != "g1:2" {
		t.Fatalf("rows = %#v", result.Rows)
	}
}

func TestInspectMessagesByChannelKeyAndCursor(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	log := db.Channel(ChannelKey("g1:2"), ChannelID{ID: "g1", Type: 2})
	if _, err := log.Append(ctx, []Record{
		{ID: 101, ClientMsgNo: "c1", FromUID: "u1", Payload: []byte("one")},
		{ID: 102, ClientMsgNo: "c2", FromUID: "u2", Payload: []byte("two")},
	}, AppendOptions{}); err != nil {
		t.Fatalf("Append(): %v", err)
	}
	first, err := InspectMessages(ctx, db, InspectMessageRequest{ChannelKey: "g1:2", Limit: 1})
	if err != nil {
		t.Fatalf("InspectMessages(first): %v", err)
	}
	if len(first.Rows) != 1 || first.Rows[0]["message_seq"] != uint64(1) || first.Next == nil {
		t.Fatalf("first = %#v", first)
	}
	second, err := InspectMessages(ctx, db, InspectMessageRequest{ChannelKey: "g1:2", AfterSeq: first.Next.AfterSeq, Limit: 1})
	if err != nil {
		t.Fatalf("InspectMessages(second): %v", err)
	}
	if len(second.Rows) != 1 || second.Rows[0]["message_id"] != uint64(102) {
		t.Fatalf("second = %#v", second)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
go test ./pkg/db/message -run 'TestInspect' -count=1
```

Expected: compile failure because message inspect APIs do not exist.

- [ ] **Step 3: Add message inspect types and channel catalog scan**

Create `pkg/db/message/inspect.go`:

```go
package message

import (
	"context"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

// InspectMessageRow is a stable column map returned by read-only message inspection.
type InspectMessageRow map[string]any

// InspectMessageCursor identifies the next message scan position.
type InspectMessageCursor struct {
	// AfterSeq is the last emitted message sequence.
	AfterSeq uint64
	// AfterChannelKey is the last emitted catalog channel key.
	AfterChannelKey string
}

// InspectMessageRequest describes one read-only message-domain scan.
type InspectMessageRequest struct {
	ChannelKey      string
	AfterSeq        uint64
	AfterChannelKey string
	Limit           int
}

// InspectMessageResult is one page of message-domain rows.
type InspectMessageResult struct {
	Rows        []InspectMessageRow
	Next        *InspectMessageCursor
	Done        bool
	ScannedRows int
}

// InspectChannels returns channel catalog rows.
func InspectChannels(ctx context.Context, db *MessageDB, req InspectMessageRequest) (InspectMessageResult, error) {
	if db == nil {
		return InspectMessageResult{}, dberrors.ErrClosed
	}
	if req.Limit <= 0 {
		req.Limit = 100
	}
	entries, err := db.ListChannels(ctx)
	if err != nil {
		return InspectMessageResult{}, err
	}
	out := InspectMessageResult{Rows: make([]InspectMessageRow, 0, req.Limit), Done: true}
	for _, entry := range entries {
		if req.AfterChannelKey != "" && string(entry.Key) <= req.AfterChannelKey {
			continue
		}
		if req.ChannelKey != "" && string(entry.Key) != req.ChannelKey {
			continue
		}
		out.ScannedRows++
		out.Rows = append(out.Rows, InspectMessageRow{
			"channel_key":  string(entry.Key),
			"channel_id":   entry.ID.ID,
			"channel_type": entry.ID.Type,
		})
		if len(out.Rows) == req.Limit {
			out.Done = false
			out.Next = &InspectMessageCursor{AfterChannelKey: string(entry.Key)}
			return out, nil
		}
	}
	return out, nil
}
```

- [ ] **Step 4: Add message row scan**

Append to `pkg/db/message/inspect.go`:

```go
// InspectMessages returns message rows for one channel key.
func InspectMessages(ctx context.Context, db *MessageDB, req InspectMessageRequest) (InspectMessageResult, error) {
	if db == nil {
		return InspectMessageResult{}, dberrors.ErrClosed
	}
	if req.ChannelKey == "" {
		return InspectMessageResult{}, fmt.Errorf("%w: channel_key required", dberrors.ErrInvalidArgument)
	}
	if req.Limit <= 0 {
		req.Limit = 100
	}
	fromSeq := req.AfterSeq + 1
	if fromSeq == 1 && req.AfterSeq == 0 {
		fromSeq = 1
	}
	log := db.Channel(ChannelKey(req.ChannelKey), ChannelID{})
	messages, err := log.Read(ctx, fromSeq, ReadOptions{Limit: req.Limit + 1})
	if err != nil {
		return InspectMessageResult{}, err
	}
	out := InspectMessageResult{Rows: make([]InspectMessageRow, 0, req.Limit), Done: true, ScannedRows: len(messages)}
	for i, msg := range messages {
		if i == req.Limit {
			out.Done = false
			out.Next = &InspectMessageCursor{AfterSeq: out.Rows[len(out.Rows)-1]["message_seq"].(uint64)}
			return out, nil
		}
		out.Rows = append(out.Rows, inspectMessageRow(msg))
	}
	return out, nil
}

func inspectMessageRow(msg Message) InspectMessageRow {
	return InspectMessageRow{
		"message_seq":   msg.MessageSeq,
		"message_id":    msg.MessageID,
		"client_msg_no": msg.ClientMsgNo,
		"from_uid":      msg.FromUID,
		"payload_hash":  msg.PayloadHash,
		"payload_size":  uint64(len(msg.Payload)),
		"payload":       append([]byte(nil), msg.Payload...),
	}
}
```

- [ ] **Step 5: Run message inspect tests**

Run:

```bash
go test ./pkg/db/message -run 'TestInspect' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/db/message/inspect.go pkg/db/message/inspect_test.go
git commit -m "feat(db): add message inspection scans"
```

---

## Task 5: Public Inspect Store And Parser

**Files:**
- Create: `pkg/db/inspect/types.go`
- Create: `pkg/db/inspect/store.go`
- Create: `pkg/db/inspect/parser.go`
- Create: `pkg/db/inspect/parser_test.go`
- Create: `pkg/db/inspect/store_test.go`

- [ ] **Step 1: Write parser tests**

Create `pkg/db/inspect/parser_test.go`:

```go
package inspect

import "testing"

func TestParseShowTables(t *testing.T) {
	q, err := Parse("show tables")
	if err != nil {
		t.Fatalf("Parse(): %v", err)
	}
	if q.Kind != QueryShowTables {
		t.Fatalf("Kind = %v, want show tables", q.Kind)
	}
}

func TestParseDescribeTable(t *testing.T) {
	q, err := Parse("describe meta.user")
	if err != nil {
		t.Fatalf("Parse(): %v", err)
	}
	if q.Kind != QueryDescribe || q.Table != "meta.user" {
		t.Fatalf("query = %#v", q)
	}
}

func TestParseSelectWhereLimit(t *testing.T) {
	q, err := Parse("select uid, token from meta.user where uid='u1' limit 20")
	if err != nil {
		t.Fatalf("Parse(): %v", err)
	}
	if q.Kind != QuerySelect || q.Table != "meta.user" || q.Limit != 20 {
		t.Fatalf("query = %#v", q)
	}
	if len(q.Columns) != 2 || q.Filters["uid"] != "u1" {
		t.Fatalf("query = %#v", q)
	}
}

func TestParseRejectsUnsupportedJoin(t *testing.T) {
	if _, err := Parse("select * from meta.user join meta.device on uid"); err == nil {
		t.Fatal("Parse(join) = nil, want error")
	}
}
```

- [ ] **Step 2: Write read-only store open test**

Create `pkg/db/inspect/store_test.go`:

```go
package inspect

import "testing"

func TestOpenStoreRequiresPath(t *testing.T) {
	if _, err := OpenStore(Options{}); err == nil {
		t.Fatal("OpenStore(empty) = nil, want error")
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run:

```bash
go test ./pkg/db/inspect -run 'TestParse|TestOpenStore' -count=1
```

Expected: package does not exist.

- [ ] **Step 4: Add public inspect types**

Create `pkg/db/inspect/types.go`:

```go
package inspect

import "errors"

var (
	ErrInvalidQuery      = errors.New("inspect: invalid query")
	ErrUnsupportedQuery  = errors.New("inspect: unsupported query")
	ErrCursorMismatch   = errors.New("inspect: cursor does not match query")
	ErrHashSlotRequired = errors.New("inspect: hash slot count required")
)

type QueryKind uint8

const (
	QueryShowTables QueryKind = iota + 1
	QueryDescribe
	QuerySelect
)

type Options struct {
	MetaPath       string
	MessagePath    string
	HashSlotCount  uint16
	DefaultLimit   int
	MaxLimit       int
}

type Query struct {
	Kind    QueryKind
	Table   string
	Columns []string
	Filters map[string]any
	Limit   int
	Cursor  string
}

type Row map[string]any

type Result struct {
	Rows  []Row
	Stats Stats
}

type Stats struct {
	ScanMode         string
	ScannedHashSlots []uint16
	ScannedRows      int
	ReturnedRows     int
	HasMore          bool
	NextCursor       string
}
```

- [ ] **Step 5: Add read-only store open**

Create `pkg/db/inspect/store.go`:

```go
package inspect

import (
	"errors"

	"github.com/WuKongIM/WuKongIM/pkg/db"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/message"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

type Store struct {
	opts          Options
	metaEngine    *engine.DB
	messageEngine *engine.DB
	metaDB        *meta.MetaDB
	messageDB     *message.MessageDB
}

func OpenStore(opts Options) (*Store, error) {
	if opts.MetaPath == "" && opts.MessagePath == "" {
		return nil, db.ErrInvalidArgument
	}
	if opts.DefaultLimit <= 0 {
		opts.DefaultLimit = 100
	}
	if opts.MaxLimit <= 0 {
		opts.MaxLimit = 10000
	}
	store := &Store{opts: opts}
	if opts.MetaPath != "" {
		eng, err := engine.Open(opts.MetaPath, engine.Options{ReadOnly: true})
		if err != nil {
			return nil, err
		}
		store.metaEngine = eng
		store.metaDB = meta.NewDB(eng)
	}
	if opts.MessagePath != "" {
		eng, err := engine.Open(opts.MessagePath, engine.Options{ReadOnly: true})
		if err != nil {
			_ = store.Close()
			return nil, err
		}
		store.messageEngine = eng
		store.messageDB = message.NewDB(eng)
	}
	return store, nil
}

func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	return errors.Join(closeEngine(s.metaEngine), closeEngine(s.messageEngine))
}

func closeEngine(db *engine.DB) error {
	if db == nil {
		return nil
	}
	return db.Close()
}
```

- [ ] **Step 6: Add parser implementation**

Create `pkg/db/inspect/parser.go`:

```go
package inspect

import (
	"fmt"
	"strconv"
	"strings"
)

func Parse(raw string) (Query, error) {
	raw = strings.TrimSpace(strings.TrimSuffix(raw, ";"))
	lower := strings.ToLower(raw)
	switch {
	case lower == "show tables":
		return Query{Kind: QueryShowTables}, nil
	case strings.HasPrefix(lower, "describe "):
		table := strings.TrimSpace(raw[len("describe "):])
		if table == "" {
			return Query{}, fmt.Errorf("%w: missing table", ErrInvalidQuery)
		}
		return Query{Kind: QueryDescribe, Table: table}, nil
	case strings.HasPrefix(lower, "select "):
		return parseSelect(raw)
	default:
		return Query{}, fmt.Errorf("%w: %q", ErrUnsupportedQuery, raw)
	}
}

func parseSelect(raw string) (Query, error) {
	lower := strings.ToLower(raw)
	if strings.Contains(lower, " join ") || strings.Contains(lower, " group by ") || strings.Contains(lower, " order by ") || strings.Contains(lower, " offset ") {
		return Query{}, fmt.Errorf("%w: unsupported select clause", ErrUnsupportedQuery)
	}
	fromIdx := strings.Index(lower, " from ")
	if fromIdx < 0 {
		return Query{}, fmt.Errorf("%w: missing from", ErrInvalidQuery)
	}
	columns := splitCSV(strings.TrimSpace(raw[len("select "):fromIdx]))
	rest := strings.TrimSpace(raw[fromIdx+len(" from "):])
	q := Query{Kind: QuerySelect, Columns: columns, Filters: map[string]any{}}
	for _, marker := range []string{" where ", " limit ", " cursor "} {
		if idx := strings.Index(strings.ToLower(rest), marker); idx >= 0 {
			q.Table = strings.TrimSpace(rest[:idx])
			return parseSelectTail(q, strings.TrimSpace(rest[idx:]))
		}
	}
	q.Table = strings.TrimSpace(rest)
	return q, nil
}

func parseSelectTail(q Query, tail string) (Query, error) {
	for tail != "" {
		lower := strings.ToLower(tail)
		switch {
		case strings.HasPrefix(lower, "where "):
			next := nextClauseIndex(tail[len("where "):])
			whereRaw := strings.TrimSpace(tail[len("where ") : len("where ")+next])
			filters, err := parseFilters(whereRaw)
			if err != nil {
				return Query{}, err
			}
			q.Filters = filters
			tail = strings.TrimSpace(tail[len("where ")+next:])
		case strings.HasPrefix(lower, "limit "):
			next := nextClauseIndex(tail[len("limit "):])
			n, err := strconv.Atoi(strings.TrimSpace(tail[len("limit ") : len("limit ")+next]))
			if err != nil || n <= 0 {
				return Query{}, fmt.Errorf("%w: invalid limit", ErrInvalidQuery)
			}
			q.Limit = n
			tail = strings.TrimSpace(tail[len("limit ")+next:])
		case strings.HasPrefix(lower, "cursor "):
			q.Cursor = strings.Trim(strings.TrimSpace(tail[len("cursor "):]), "'")
			tail = ""
		default:
			return Query{}, fmt.Errorf("%w: invalid select tail %q", ErrInvalidQuery, tail)
		}
	}
	return q, nil
}
```

Add these helpers in the same file:

```go
func splitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func nextClauseIndex(raw string) int {
	lower := strings.ToLower(raw)
	next := len(raw)
	for _, marker := range []string{" where ", " limit ", " cursor "} {
		if idx := strings.Index(lower, marker); idx >= 0 && idx < next {
			next = idx
		}
	}
	return next
}

func parseFilters(raw string) (map[string]any, error) {
	out := map[string]any{}
	for _, part := range strings.Split(raw, " and ") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			return nil, fmt.Errorf("%w: unsupported filter %q", ErrUnsupportedQuery, part)
		}
		parsed, err := parseLiteral(strings.TrimSpace(value))
		if err != nil {
			return nil, err
		}
		out[strings.TrimSpace(key)] = parsed
	}
	return out, nil
}

func parseLiteral(raw string) (any, error) {
	if strings.HasPrefix(raw, "'") && strings.HasSuffix(raw, "'") && len(raw) >= 2 {
		return strings.Trim(raw, "'"), nil
	}
	if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return n, nil
	}
	return nil, fmt.Errorf("%w: invalid literal %q", ErrInvalidQuery, raw)
}
```

- [ ] **Step 7: Run parser and store tests**

Run:

```bash
go test ./pkg/db/inspect -run 'TestParse|TestOpenStore' -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add pkg/db/inspect/types.go pkg/db/inspect/store.go pkg/db/inspect/parser.go pkg/db/inspect/parser_test.go pkg/db/inspect/store_test.go
git commit -m "feat(db): add inspect query parser and store"
```

---

## Task 6: Planner, Cursor, And Execute

**Files:**
- Create: `pkg/db/inspect/planner.go`
- Create: `pkg/db/inspect/cursor.go`
- Create: `pkg/db/inspect/execute.go`
- Create: `pkg/db/inspect/planner_test.go`
- Create: `pkg/db/inspect/cursor_test.go`
- Create: `pkg/db/inspect/execute_test.go`
- Modify: `pkg/db/inspect/types.go`

- [ ] **Step 1: Write planner tests**

Create `pkg/db/inspect/planner_test.go`:

```go
package inspect

import "testing"

func TestPlanMetaPointPartitionFromUID(t *testing.T) {
	plan, err := planQuery(Options{HashSlotCount: 256}, Query{
		Kind:    QuerySelect,
		Table:   "meta.user",
		Filters: map[string]any{"uid": "u1"},
		Limit:   20,
	})
	if err != nil {
		t.Fatalf("planQuery(): %v", err)
	}
	if plan.ScanMode != scanModePointPartition || !plan.HashSlotSet {
		t.Fatalf("plan = %#v", plan)
	}
}

func TestPlanMetaLocalBoundedRequiresHashSlotCount(t *testing.T) {
	_, err := planQuery(Options{}, Query{Kind: QuerySelect, Table: "meta.user", Limit: 20})
	if err == nil {
		t.Fatal("planQuery() = nil, want hash slot count error")
	}
}

func TestPlanMessageRequiresChannelKey(t *testing.T) {
	_, err := planQuery(Options{}, Query{Kind: QuerySelect, Table: "message.message", Limit: 20})
	if err == nil {
		t.Fatal("planQuery(message without channel_key) = nil, want error")
	}
}
```

- [ ] **Step 2: Write cursor tests**

Create `pkg/db/inspect/cursor_test.go`:

```go
package inspect

import "testing"

func TestCursorRoundTrip(t *testing.T) {
	q := Query{Kind: QuerySelect, Table: "meta.user", Filters: map[string]any{"uid": "u1"}, Limit: 10}
	payload := cursorPayload{Version: 1, Domain: "meta", Table: "user", ScanMode: string(scanModePointPartition), HashSlot: 12, Primary: []any{"u1"}, QueryHash: queryHash(q)}
	encoded, err := encodeCursor(payload)
	if err != nil {
		t.Fatalf("encodeCursor(): %v", err)
	}
	got, err := decodeCursor(encoded, q)
	if err != nil {
		t.Fatalf("decodeCursor(): %v", err)
	}
	if got.HashSlot != 12 || got.Primary[0] != "u1" {
		t.Fatalf("cursor = %#v", got)
	}
}

func TestCursorRejectsQueryMismatch(t *testing.T) {
	q := Query{Kind: QuerySelect, Table: "meta.user", Filters: map[string]any{"uid": "u1"}, Limit: 10}
	payload := cursorPayload{Version: 1, Domain: "meta", Table: "user", QueryHash: queryHash(q)}
	encoded, err := encodeCursor(payload)
	if err != nil {
		t.Fatalf("encodeCursor(): %v", err)
	}
	other := Query{Kind: QuerySelect, Table: "meta.user", Filters: map[string]any{"uid": "u2"}, Limit: 10}
	if _, err := decodeCursor(encoded, other); err == nil {
		t.Fatal("decodeCursor(mismatch) = nil, want error")
	}
}
```

- [ ] **Step 3: Write execute tests**

Create `pkg/db/inspect/execute_test.go`:

```go
package inspect

import (
	"testing"
)

func TestNormalizeLimitDefaultAndMax(t *testing.T) {
	opts := Options{DefaultLimit: 100, MaxLimit: 10000}
	if got := normalizeLimit(opts, 0); got != 100 {
		t.Fatalf("normalizeLimit(default) = %d", got)
	}
	if got := normalizeLimit(opts, 20000); got != 10000 {
		t.Fatalf("normalizeLimit(max) = %d", got)
	}
}
```

- [ ] **Step 4: Run tests to verify they fail**

Run:

```bash
go test ./pkg/db/inspect -run 'TestPlan|TestCursor|TestNormalizeLimit' -count=1
```

Expected: compile failure because planner functions do not exist.

- [ ] **Step 5: Add planner types**

Create `pkg/db/inspect/planner.go`:

```go
package inspect

import (
	"fmt"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/cluster"
)

type scanMode string

const (
	scanModePointPartition    scanMode = "point-partition"
	scanModeExplicitPartition scanMode = "explicit-partition"
	scanModeLocalBounded      scanMode = "local-bounded"
	scanModeMessageChannel    scanMode = "message-channel"
	scanModeMessageCatalog    scanMode = "message-catalog"
)

type plan struct {
	Query
	Domain        string
	TableName     string
	ScanMode      scanMode
	HashSlot      uint16
	HashSlotSet   bool
	HashSlotCount uint16
	Cursor        *cursorPayload
}

var partitionKeys = map[string]string{
	"meta.user":                 "uid",
	"meta.device":               "uid",
	"meta.channel":              "channel_id",
	"meta.channel_runtime_meta": "channel_id",
	"meta.subscriber":           "channel_id",
	"meta.conversation":         "uid",
	"meta.cmd_conversation":     "uid",
	"meta.plugin_binding":       "uid",
	"meta.channel_migration":    "channel_id",
	"meta.hashslot_migration":   "hash_slot",
	"message.message":           "channel_key",
}

func planQuery(opts Options, q Query) (plan, error) {
	q.Limit = normalizeLimit(opts, q.Limit)
	domain, tableName, ok := strings.Cut(q.Table, ".")
	if !ok {
		return plan{}, fmt.Errorf("%w: table must be domain-qualified", ErrInvalidQuery)
	}
	p := plan{Query: q, Domain: domain, TableName: tableName, HashSlotCount: opts.HashSlotCount}
	if q.Cursor != "" {
		cursor, err := decodeCursor(q.Cursor, q)
		if err != nil {
			return plan{}, err
		}
		p.Cursor = &cursor
	}
	switch domain {
	case "meta":
		return planMetaQuery(opts, p)
	case "message":
		return planMessageQuery(p)
	default:
		return plan{}, fmt.Errorf("%w: unknown domain %q", ErrInvalidQuery, domain)
	}
}
```

- [ ] **Step 6: Add cursor encoding**

Create `pkg/db/inspect/cursor.go`:

```go
package inspect

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type cursorPayload struct {
	Version    int    `json:"version"`
	Domain     string `json:"domain"`
	Table      string `json:"table"`
	ScanMode   string `json:"scan_mode"`
	HashSlot   uint16 `json:"hash_slot,omitempty"`
	Primary    []any  `json:"primary,omitempty"`
	ChannelKey string `json:"channel_key,omitempty"`
	AfterSeq   uint64 `json:"after_seq,omitempty"`
	QueryHash  string `json:"query_hash"`
}

func encodeCursor(payload cursorPayload) (string, error) {
	payload.Version = 1
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeCursor(raw string, q Query) (cursorPayload, error) {
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return cursorPayload{}, fmt.Errorf("%w: malformed cursor", ErrInvalidQuery)
	}
	var payload cursorPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return cursorPayload{}, fmt.Errorf("%w: malformed cursor", ErrInvalidQuery)
	}
	if payload.Version != 1 {
		return cursorPayload{}, fmt.Errorf("%w: unsupported cursor version", ErrInvalidQuery)
	}
	if payload.QueryHash != queryHash(q) {
		return cursorPayload{}, ErrCursorMismatch
	}
	return payload, nil
}

func queryHash(q Query) string {
	parts := []string{q.Table}
	columns := append([]string(nil), q.Columns...)
	sort.Strings(columns)
	parts = append(parts, strings.Join(columns, ","))
	filterKeys := make([]string, 0, len(q.Filters))
	for key := range q.Filters {
		filterKeys = append(filterKeys, key)
	}
	sort.Strings(filterKeys)
	for _, key := range filterKeys {
		if key == "cursor" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%v", key, q.Filters[key]))
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return "sha256:" + hex.EncodeToString(sum[:])
}
```

- [ ] **Step 7: Add meta and message planning**

Append to `pkg/db/inspect/planner.go`:

```go
func planMetaQuery(opts Options, p plan) (plan, error) {
	if raw, ok := p.Filters["hash_slot"]; ok {
		slot, ok := asUint16(raw)
		if !ok {
			return plan{}, fmt.Errorf("%w: invalid hash_slot", ErrInvalidQuery)
		}
		p.HashSlot = slot
		p.HashSlotSet = true
		p.ScanMode = scanModeExplicitPartition
		return p, nil
	}
	if keyName := partitionKeys[p.Table]; keyName != "" {
		if raw, ok := p.Filters[keyName]; ok {
			key, ok := raw.(string)
			if !ok {
				return plan{}, fmt.Errorf("%w: partition key %s must be string", ErrInvalidQuery, keyName)
			}
			if opts.HashSlotCount == 0 {
				return plan{}, ErrHashSlotRequired
			}
			p.HashSlot = cluster.HashSlotForKey(key, opts.HashSlotCount)
			p.HashSlotSet = true
			p.ScanMode = scanModePointPartition
			return p, nil
		}
	}
	if opts.HashSlotCount == 0 {
		return plan{}, ErrHashSlotRequired
	}
	p.ScanMode = scanModeLocalBounded
	return p, nil
}

func planMessageQuery(p plan) (plan, error) {
	switch p.Table {
	case "message.channels":
		p.ScanMode = scanModeMessageCatalog
		return p, nil
	case "message.message":
		if _, ok := p.Filters["channel_key"].(string); !ok {
			return plan{}, fmt.Errorf("%w: channel_key required", ErrInvalidQuery)
		}
		p.ScanMode = scanModeMessageChannel
		return p, nil
	default:
		return plan{}, fmt.Errorf("%w: unknown message table %q", ErrInvalidQuery, p.Table)
	}
}

func normalizeLimit(opts Options, limit int) int {
	defaultLimit := opts.DefaultLimit
	if defaultLimit <= 0 {
		defaultLimit = 100
	}
	maxLimit := opts.MaxLimit
	if maxLimit <= 0 {
		maxLimit = 10000
	}
	if limit <= 0 {
		return defaultLimit
	}
	if limit > maxLimit {
		return maxLimit
	}
	return limit
}
```

Add `asUint16` in the same file.

- [ ] **Step 8: Add execute dispatch**

Create `pkg/db/inspect/execute.go`:

```go
package inspect

import (
	"context"
	"fmt"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	msgdb "github.com/WuKongIM/WuKongIM/pkg/db/message"
)

func (s *Store) Query(ctx context.Context, raw string) (Result, error) {
	q, err := Parse(raw)
	if err != nil {
		return Result{}, err
	}
	switch q.Kind {
	case QueryShowTables:
		return s.showTables(), nil
	case QueryDescribe:
		return s.describe(q.Table)
	case QuerySelect:
		p, err := planQuery(s.opts, q)
		if err != nil {
			return Result{}, err
		}
		return s.executePlan(ctx, p)
	default:
		return Result{}, fmt.Errorf("%w: unknown query kind", ErrInvalidQuery)
	}
}

func (s *Store) executePlan(ctx context.Context, p plan) (Result, error) {
	switch p.ScanMode {
	case scanModePointPartition, scanModeExplicitPartition, scanModeLocalBounded:
		req := metadb.InspectScanRequest{
			Table:         p.TableName,
			HashSlot:      metadb.HashSlot(p.HashSlot),
			HashSlotSet:   p.HashSlotSet,
			HashSlotCount: p.HashSlotCount,
			Filters:       p.Filters,
			Limit:         p.Limit,
		}
		if p.Cursor != nil {
			req.After = &metadb.InspectCursor{HashSlot: metadb.HashSlot(p.Cursor.HashSlot), Primary: p.Cursor.Primary}
		}
		page, err := metadb.InspectScan(ctx, s.metaDB, req)
		if err != nil {
			return Result{}, err
		}
		return resultFromMetaPage(p, page)
	case scanModeMessageCatalog:
		req := msgdb.InspectMessageRequest{Limit: p.Limit}
		if p.Cursor != nil {
			req.AfterChannelKey = p.Cursor.ChannelKey
		}
		page, err := msgdb.InspectChannels(ctx, s.messageDB, req)
		if err != nil {
			return Result{}, err
		}
		return resultFromMessagePage(string(p.ScanMode), page)
	case scanModeMessageChannel:
		req := msgdb.InspectMessageRequest{ChannelKey: p.Filters["channel_key"].(string), Limit: p.Limit}
		if p.Cursor != nil {
			req.AfterSeq = p.Cursor.AfterSeq
		}
		page, err := msgdb.InspectMessages(ctx, s.messageDB, req)
		if err != nil {
			return Result{}, err
		}
		return resultFromMessagePage(p, page)
	default:
		return Result{}, fmt.Errorf("%w: scan mode %q", ErrUnsupportedQuery, p.ScanMode)
	}
}
```

Add these result helpers in the same file:

```go
func (s *Store) showTables() Result {
	rows := make([]Row, 0)
	for _, table := range metadb.InspectTables() {
		rows = append(rows, Row{"table": "meta." + table.Name, "domain": "meta"})
	}
	rows = append(rows, Row{"table": "message.channels", "domain": "message"})
	rows = append(rows, Row{"table": "message.message", "domain": "message"})
	return Result{Rows: rows, Stats: Stats{ReturnedRows: len(rows)}}
}

func (s *Store) describe(tableName string) (Result, error) {
	rows := make([]Row, 0)
	for _, table := range metadb.InspectTables() {
		if tableName != "meta."+table.Name {
			continue
		}
		for _, column := range table.Columns {
			rows = append(rows, Row{"table": tableName, "column": column.Name, "type": column.Type, "required": column.Required})
		}
		return Result{Rows: rows, Stats: Stats{ReturnedRows: len(rows)}}, nil
	}
	if tableName == "message.channels" || tableName == "message.message" {
		return describeMessageTable(tableName), nil
	}
	return Result{}, fmt.Errorf("%w: unknown table %q", ErrInvalidQuery, tableName)
}

func resultFromMetaPage(p plan, page metadb.InspectScanResult) (Result, error) {
	rows := make([]Row, 0, len(page.Rows))
	for _, row := range page.Rows {
		rows = append(rows, Row(row))
	}
	stats := Stats{ScanMode: string(p.ScanMode), ScannedRows: page.ScannedRows, ReturnedRows: len(rows), HasMore: !page.Done}
	for _, slot := range page.ScannedHashSlots {
		stats.ScannedHashSlots = append(stats.ScannedHashSlots, uint16(slot))
	}
	if page.Next != nil {
		cursor, err := encodeCursor(cursorPayload{Domain: p.Domain, Table: p.TableName, ScanMode: string(p.ScanMode), HashSlot: uint16(page.Next.HashSlot), Primary: page.Next.Primary, QueryHash: queryHash(p.Query)})
		if err != nil {
			return Result{}, err
		}
		stats.NextCursor = cursor
	}
	return Result{Rows: rows, Stats: stats}, nil
}

func resultFromMessagePage(p plan, page msgdb.InspectMessageResult) (Result, error) {
	rows := make([]Row, 0, len(page.Rows))
	for _, row := range page.Rows {
		rows = append(rows, Row(row))
	}
	stats := Stats{ScanMode: string(p.ScanMode), ScannedRows: page.ScannedRows, ReturnedRows: len(rows), HasMore: !page.Done}
	if page.Next != nil {
		payload := cursorPayload{Domain: p.Domain, Table: p.TableName, ScanMode: string(p.ScanMode), QueryHash: queryHash(p.Query)}
		if p.ScanMode == scanModeMessageCatalog {
			payload.ChannelKey = page.Next.AfterChannelKey
		} else {
			payload.ChannelKey = p.Filters["channel_key"].(string)
			payload.AfterSeq = page.Next.AfterSeq
		}
		cursor, err := encodeCursor(payload)
		if err != nil {
			return Result{}, err
		}
		stats.NextCursor = cursor
	}
	return Result{Rows: rows, Stats: stats}, nil
}
```

- [ ] **Step 9: Run planner tests**

Run:

```bash
go test ./pkg/db/inspect -run 'TestPlan|TestCursor|TestNormalizeLimit' -count=1
```

Expected: PASS.

- [ ] **Step 10: Run inspect package tests**

Run:

```bash
go test ./pkg/db/inspect -count=1
```

Expected: PASS.

- [ ] **Step 11: Commit**

```bash
git add pkg/db/inspect
git commit -m "feat(db): plan and execute inspect queries"
```

---

## Task 7: wkdb CLI

**Files:**
- Create: `cmd/wkdb/main.go`
- Create: `cmd/wkdb/config.go`
- Create: `cmd/wkdb/output.go`
- Create: `cmd/wkdb/main_test.go`
- Create: `cmd/wkdb/config_test.go`
- Create: `cmd/wkdb/output_test.go`

- [ ] **Step 1: Write config resolution tests**

Create `cmd/wkdb/config_test.go`:

```go
package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveConfigFromDataDir(t *testing.T) {
	dir := t.TempDir()
	cfg, err := resolveCLIConfig(cliFlags{dataDir: dir}, nil)
	if err != nil {
		t.Fatalf("resolveCLIConfig(): %v", err)
	}
	if cfg.options.MetaPath != filepath.Join(dir, "data") {
		t.Fatalf("MetaPath = %q", cfg.options.MetaPath)
	}
	if cfg.options.MessagePath != filepath.Join(dir, "channellog") {
		t.Fatalf("MessagePath = %q", cfg.options.MessagePath)
	}
}

func TestResolveConfigFileStorageKeys(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "wukongim.conf")
	content := "WK_NODE_DATA_DIR=" + filepath.Join(dir, "node-1") + "\nWK_CLUSTER_HASH_SLOT_COUNT=256\n"
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(): %v", err)
	}
	cfg, err := resolveCLIConfig(cliFlags{configPath: configPath}, nil)
	if err != nil {
		t.Fatalf("resolveCLIConfig(): %v", err)
	}
	if cfg.options.HashSlotCount != 256 {
		t.Fatalf("HashSlotCount = %d", cfg.options.HashSlotCount)
	}
}
```

- [ ] **Step 2: Write output tests**

Create `cmd/wkdb/output_test.go`:

```go
package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/inspect"
)

func TestRenderJSONL(t *testing.T) {
	var buf bytes.Buffer
	result := inspect.Result{Rows: []inspect.Row{{"uid": "u1"}, {"uid": "u2"}}}
	if err := renderResult(&buf, "jsonl", result); err != nil {
		t.Fatalf("renderResult(): %v", err)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], `"uid":"u1"`) {
		t.Fatalf("output = %q", buf.String())
	}
}
```

- [ ] **Step 3: Write main command tests**

Create `cmd/wkdb/main_test.go`:

```go
package main

import (
	"bytes"
	"testing"
)

func TestRunRejectsUnknownCommand(t *testing.T) {
	var stderr bytes.Buffer
	code := runWithIO([]string{"missing"}, nil, &stderr)
	if code == 0 {
		t.Fatal("exit code = 0, want failure")
	}
	if stderr.Len() == 0 {
		t.Fatal("stderr is empty")
	}
}
```

- [ ] **Step 4: Run tests to verify they fail**

Run:

```bash
go test ./cmd/wkdb -count=1
```

Expected: package does not exist.

- [ ] **Step 5: Add CLI config resolution**

Create `cmd/wkdb/config.go`:

```go
package main

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/db/inspect"
)

type cliFlags struct {
	configPath    string
	dataDir       string
	metaPath      string
	messagePath   string
	hashSlotCount uint16
	format        string
}

type cliConfig struct {
	options inspect.Options
	format  string
}

func resolveCLIConfig(flags cliFlags, env []string) (cliConfig, error) {
	values := map[string]string{}
	if flags.configPath != "" {
		fileValues, err := readKeyValueFile(flags.configPath)
		if err != nil {
			return cliConfig{}, err
		}
		for key, value := range fileValues {
			values[key] = value
		}
	}
	for _, item := range env {
		key, value, ok := strings.Cut(item, "=")
		if ok && strings.HasPrefix(key, "WK_") {
			values[key] = value
		}
	}
	dataDir := firstNonEmpty(flags.dataDir, values["WK_NODE_DATA_DIR"])
	metaPath := firstNonEmpty(flags.metaPath, values["WK_STORAGE_DB_PATH"])
	messagePath := firstNonEmpty(flags.messagePath, values["WK_STORAGE_CHANNEL_LOG_PATH"])
	if dataDir != "" {
		if metaPath == "" {
			metaPath = filepath.Join(dataDir, "data")
		}
		if messagePath == "" {
			messagePath = filepath.Join(dataDir, "channellog")
		}
	}
	hashSlotCount := flags.hashSlotCount
	if hashSlotCount == 0 {
		hashSlotCount = parseHashSlotCount(values)
	}
	format := firstNonEmpty(flags.format, "table")
	return cliConfig{options: inspect.Options{MetaPath: metaPath, MessagePath: messagePath, HashSlotCount: hashSlotCount}, format: format}, nil
}

func readKeyValueFile(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return readKeyValues(file), nil
}

func readKeyValues(r io.Reader) map[string]string {
	out := map[string]string{}
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if ok {
			out[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	return out
}
```

Add `firstNonEmpty` and `parseHashSlotCount`:

```go
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func parseHashSlotCount(values map[string]string) uint16 {
	for _, key := range []string{"WK_CLUSTER_HASH_SLOT_COUNT", "WK_CLUSTER_INITIAL_SLOT_COUNT", "WK_CLUSTER_SLOT_COUNT"} {
		raw := strings.TrimSpace(values[key])
		if raw == "" {
			continue
		}
		n, err := strconv.ParseUint(raw, 10, 16)
		if err == nil {
			return uint16(n)
		}
	}
	return 0
}
```

- [ ] **Step 6: Add CLI main**

Create `cmd/wkdb/main.go`:

```go
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/db/inspect"
)

const (
	exitOK       = 0
	exitConfig   = 1
	exitQuery    = 2
	exitInternal = 3
)

func main() {
	os.Exit(runWithIO(os.Args[1:], os.Stdin, os.Stderr))
}

func runWithIO(args []string, stdin io.Reader, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: wkdb [flags] <query|repl>")
		return exitConfig
	}
	flags, rest, code := parseFlags(args, stderr)
	if code != exitOK {
		return code
	}
	if len(rest) == 0 {
		fmt.Fprintln(stderr, "usage: wkdb [flags] <query|repl>")
		return exitConfig
	}
	cfg, err := resolveCLIConfig(flags, os.Environ())
	if err != nil {
		fmt.Fprintf(stderr, "config error: %v\n", err)
		return exitConfig
	}
	store, err := inspect.OpenStore(cfg.options)
	if err != nil {
		fmt.Fprintf(stderr, "open store: %v\n", err)
		return exitConfig
	}
	defer store.Close()
	switch rest[0] {
	case "query":
		if len(rest) < 2 {
			fmt.Fprintln(stderr, "usage: wkdb [flags] query <sql>")
			return exitConfig
		}
		return runQuery(context.Background(), store, cfg.format, strings.Join(rest[1:], " "), stderr)
	case "repl":
		return runREPL(context.Background(), store, cfg.format, stdin, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", rest[0])
		return exitConfig
	}
}
```

Add `parseFlags`, `runQuery`, and `runREPL` in the same file:

```go
func parseFlags(args []string, stderr io.Writer) (cliFlags, []string, int) {
	var flags cliFlags
	var hashSlotCount uint
	fs := flag.NewFlagSet("wkdb", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&flags.configPath, "config", "", "path to wukongim.conf")
	fs.StringVar(&flags.dataDir, "data-dir", "", "node data directory")
	fs.StringVar(&flags.metaPath, "meta-path", "", "metadata store path")
	fs.StringVar(&flags.messagePath, "message-path", "", "message store path")
	fs.UintVar(&hashSlotCount, "hash-slot-count", 0, "cluster hash slot count")
	fs.StringVar(&flags.format, "format", "table", "output format: table, json, jsonl")
	if err := fs.Parse(args); err != nil {
		return cliFlags{}, nil, exitConfig
	}
	if hashSlotCount > 65535 {
		fmt.Fprintln(stderr, "--hash-slot-count must be <= 65535")
		return cliFlags{}, nil, exitConfig
	}
	flags.hashSlotCount = uint16(hashSlotCount)
	return flags, fs.Args(), exitOK
}

func runQuery(ctx context.Context, store *inspect.Store, format string, sql string, stderr io.Writer) int {
	result, err := store.Query(ctx, sql)
	if err != nil {
		fmt.Fprintf(stderr, "query error: %v\n", err)
		return exitQuery
	}
	if err := renderResult(os.Stdout, format, result); err != nil {
		fmt.Fprintf(stderr, "render error: %v\n", err)
		return exitInternal
	}
	return exitOK
}

func runREPL(ctx context.Context, store *inspect.Store, format string, stdin io.Reader, stderr io.Writer) int {
	scanner := bufio.NewScanner(stdin)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if line == "exit" || line == "quit" {
			return exitOK
		}
		_ = runQuery(ctx, store, format, line, stderr)
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(stderr, "read repl: %v\n", err)
		return exitInternal
	}
	return exitOK
}
```

- [ ] **Step 7: Add output renderers**

Create `cmd/wkdb/output.go`:

```go
package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/WuKongIM/WuKongIM/pkg/db/inspect"
)

func renderResult(w io.Writer, format string, result inspect.Result) error {
	switch format {
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	case "jsonl":
		enc := json.NewEncoder(w)
		for _, row := range result.Rows {
			if err := enc.Encode(normalizeRow(row)); err != nil {
				return err
			}
		}
		return nil
	case "table":
		return renderTable(w, result)
	default:
		return fmt.Errorf("unknown format %q", format)
	}
}

func normalizeRow(row inspect.Row) map[string]any {
	out := make(map[string]any, len(row))
	for key, value := range row {
		if bytes, ok := value.([]byte); ok {
			out[key] = base64.StdEncoding.EncodeToString(bytes)
			continue
		}
		out[key] = value
	}
	return out
}

func renderTable(w io.Writer, result inspect.Result) error {
	if len(result.Rows) == 0 {
		fmt.Fprintf(w, "rows=0 has_more=%v\n", result.Stats.HasMore)
		return nil
	}
	columns := sortedColumns(result.Rows)
	for i, column := range columns {
		if i > 0 {
			fmt.Fprint(w, "\t")
		}
		fmt.Fprint(w, column)
	}
	fmt.Fprintln(w)
	for _, row := range result.Rows {
		normalized := normalizeRow(row)
		for i, column := range columns {
			if i > 0 {
				fmt.Fprint(w, "\t")
			}
			fmt.Fprint(w, normalized[column])
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintf(w, "rows=%d has_more=%v scan_mode=%s scanned_rows=%d next_cursor=%s\n", result.Stats.ReturnedRows, result.Stats.HasMore, result.Stats.ScanMode, result.Stats.ScannedRows, result.Stats.NextCursor)
	return nil
}
```

Add `sortedColumns` in the same file:

```go
func sortedColumns(rows []inspect.Row) []string {
	seen := map[string]struct{}{}
	for _, row := range rows {
		for key := range row {
			seen[key] = struct{}{}
		}
	}
	columns := make([]string, 0, len(seen))
	for key := range seen {
		columns = append(columns, key)
	}
	sort.Strings(columns)
	return columns
}
```

- [ ] **Step 8: Run wkdb tests**

Run:

```bash
go test ./cmd/wkdb -count=1
```

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add cmd/wkdb
git commit -m "feat: add wkdb read-only cli"
```

---

## Task 8: Documentation And Verification

**Files:**
- Create: `cmd/wkdb/README.md`
- Modify: `docs/superpowers/plans/2026-05-27-wkdb-readonly-cli.md` only if the implementation discovers a required correction to this plan.

- [ ] **Step 1: Write CLI README**

Create `cmd/wkdb/README.md`:

```markdown
# wkdb

`wkdb` is a read-only offline inspection CLI for one WuKongIM node data directory.

Examples:

```bash
wkdb --data-dir ./node-1 query "show tables"
wkdb --data-dir ./node-1 --hash-slot-count 256 query "select * from meta.user where uid='u1'"
wkdb --data-dir ./node-1 --hash-slot-count 256 query "select * from meta.user limit 100"
wkdb --data-dir ./node-1 query "select * from message.channels limit 20"
wkdb --data-dir ./node-1 query "select * from message.message where channel_key='g1:2' limit 50"
```

Metadata rows are stored by hash slot. When a query includes a table partition key such as `uid` or `channel_id`, `wkdb` derives the hash slot automatically from `--hash-slot-count` or `--config`. Queries without a partition key perform a bounded local scan over this node's files in `hash_slot, primary_key` order.

`limit` is the total number of rows returned by the query. `offset` is not supported; use the returned cursor for pagination.

`wkdb` is local-only. It does not contact cluster peers and does not return a global cluster view.
```

- [ ] **Step 2: Run focused tests**

Run:

```bash
go test ./pkg/db/internal/engine ./pkg/db/meta ./pkg/db/message ./pkg/db/inspect ./cmd/wkdb -count=1
```

Expected: PASS.

- [ ] **Step 3: Run broader unit tests**

Run:

```bash
go test ./pkg/db/... ./cmd/wkdb -count=1
```

Expected: PASS.

- [ ] **Step 4: Smoke test CLI help and parser**

Run:

```bash
go run ./cmd/wkdb
```

Expected: exits non-zero and prints `usage: wkdb [flags] <query|repl>`.

- [ ] **Step 5: Commit docs**

```bash
git add cmd/wkdb/README.md
git commit -m "docs: document wkdb read-only inspection"
```

---

## Final Verification

- [ ] Run:

```bash
go test ./pkg/db/... ./cmd/wkdb -count=1
```

Expected: PASS.

- [ ] Run:

```bash
go test ./cmd/wukongim ./internal/app -run 'TestConfigApplyDefaultsDerivesStoragePathsFromDataDir|TestLoadConfig' -count=1
```

Expected: PASS. This guards against accidental drift in storage path assumptions used by `wkdb`.

- [ ] Check worktree:

```bash
git status --short
```

Expected: only intentional changes remain.
