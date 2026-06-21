# Unified Conversation Projection Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace split ordinary/CMD conversation projection storage with one UID-owned, kind-aware conversation projection model, then build CMD sync on top of that model.

**Architecture:** `pkg/db/meta` owns the canonical `(uid, kind, channel_id, channel_type)` projection table. Slot FSM and `pkg/clusterv2` expose generic conversation commands/facades routed by UID hash slot. `internalv2/runtime/conversationactive` becomes kind-aware, while ordinary conversation and CMD sync remain separate usecases over the shared projection/runtime layer.

**Tech Stack:** Go, Pebble-backed `pkg/db/internal` table runtime, Slot FSM command codec, `pkg/clusterv2`, `internalv2` usecases, Gin HTTP handlers, Go unit tests and e2ev2 black-box tests.

---

## File Structure

Storage and schema:

- Modify: `pkg/db/meta/types.go`
- Modify: `pkg/db/meta/table_conversation.go`
- Delete: `pkg/db/meta/table_cmd_conversation.go`
- Modify: `pkg/db/meta/compat.go`
- Modify: `pkg/db/meta/keys.go`
- Modify: `pkg/db/meta/inspect.go`
- Modify: `pkg/db/meta/inspect_test.go`
- Modify: `pkg/db/meta/user_conversation_state_test.go`
- Delete: `pkg/db/meta/cmd_conversation_state_test.go`
- Modify: `pkg/db/meta/FLOW.md`

Slot FSM:

- Modify: `pkg/slot/fsm/command.go`
- Modify: `pkg/slot/fsm/command_inspection.go`
- Modify: `pkg/slot/fsm/command_inspection_test.go`
- Modify: `pkg/slot/fsm/state_machine_test.go`

Cluster facade:

- Modify: `pkg/clusterv2/node_meta.go`
- Modify: `pkg/clusterv2/node_meta_conversation_test.go`

Runtime:

- Modify: `internalv2/runtime/conversationactive/types.go`
- Modify: `internalv2/runtime/conversationactive/manager.go`
- Modify: `internalv2/runtime/conversationactive/active_view.go`
- Modify: `internalv2/runtime/conversationactive/manager_test.go`
- Modify: `internalv2/runtime/conversationactive/manager_benchmark_test.go`
- Create: `internalv2/runtime/conversationactive/FLOW.md`

Conversation usecase and adapters:

- Modify: `internalv2/usecase/conversation/app.go`
- Modify: `internalv2/usecase/conversation/types.go`
- Modify: `internalv2/usecase/conversation/sync.go`
- Modify: `internalv2/usecase/conversation/unread.go`
- Modify: `internalv2/usecase/conversation/*_test.go`
- Modify: `internalv2/infra/cluster/conversation.go`
- Modify: `internalv2/infra/cluster/conversation_test.go`
- Modify: `internalv2/app/conversation_authority.go`
- Modify: `internalv2/app/conversation_authority_test.go`
- Modify: `internalv2/app/app_test.go`

Channel append:

- Modify: `internalv2/runtime/channelappend/options.go`
- Modify: `internalv2/runtime/channelappend/recipient.go`
- Modify: `internalv2/runtime/channelappend/recipient_test.go`
- Modify: `internalv2/runtime/channelappend/FLOW.md`

CMD sync:

- Create: `internalv2/usecase/cmdsync/types.go`
- Create: `internalv2/usecase/cmdsync/app.go`
- Create: `internalv2/usecase/cmdsync/records.go`
- Create: `internalv2/usecase/cmdsync/intent.go`
- Create: `internalv2/usecase/cmdsync/import_boundary_test.go`
- Create: `internalv2/usecase/cmdsync/*_test.go`
- Modify: `internalv2/infra/cluster/message_reader.go`
- Create: `internalv2/infra/cluster/cmdsync.go`
- Create: `internalv2/access/api/message_sync.go`
- Modify: `internalv2/access/api/routes.go`
- Modify: `internalv2/access/api/FLOW.md`
- Modify: `internalv2/app/wiring.go`
- Modify: `internalv2/app/FLOW.md`

E2E:

- Create or modify: `test/e2ev2/message/*cmd_sync*_test.go`

---

### Task 1: Introduce the Unified Conversation Row in `pkg/db/meta`

**Files:**

- Modify: `pkg/db/meta/types.go`
- Modify: `pkg/db/meta/table_conversation.go`
- Delete: `pkg/db/meta/table_cmd_conversation.go`
- Modify: `pkg/db/meta/keys.go`
- Modify: `pkg/db/meta/user_conversation_state_test.go`
- Delete: `pkg/db/meta/cmd_conversation_state_test.go`
- Modify: `pkg/db/meta/FLOW.md`

- [ ] **Step 1: Write failing storage tests for kind isolation**

Add tests to `pkg/db/meta/user_conversation_state_test.go`:

```go
func TestConversationKindIsPartOfPrimaryKey(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	shard := store.db.HashSlot(4)
	ctx := context.Background()

	normal := ConversationState{UID: "u1", Kind: ConversationKindNormal, ChannelID: "g1", ChannelType: 2, ReadSeq: 3, ActiveAt: 100, UpdatedAt: 100}
	cmd := ConversationState{UID: "u1", Kind: ConversationKindCMD, ChannelID: "g1", ChannelType: 2, ReadSeq: 9, ActiveAt: 200, UpdatedAt: 200}
	if err := shard.UpsertConversationState(ctx, normal); err != nil {
		t.Fatalf("UpsertConversationState(normal): %v", err)
	}
	if err := shard.UpsertConversationState(ctx, cmd); err != nil {
		t.Fatalf("UpsertConversationState(cmd): %v", err)
	}

	gotNormal, ok, err := shard.GetConversationState(ctx, ConversationKindNormal, "u1", "g1", 2)
	if err != nil || !ok {
		t.Fatalf("GetConversationState(normal) ok=%v err=%v", ok, err)
	}
	gotCMD, ok, err := shard.GetConversationState(ctx, ConversationKindCMD, "u1", "g1", 2)
	if err != nil || !ok {
		t.Fatalf("GetConversationState(cmd) ok=%v err=%v", ok, err)
	}
	if gotNormal.ReadSeq != 3 || gotCMD.ReadSeq != 9 {
		t.Fatalf("states share primary key: normal=%+v cmd=%+v", gotNormal, gotCMD)
	}
}

func TestConversationActivePageScansOnlyRequestedKind(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	shard := store.db.HashSlot(4)
	ctx := context.Background()

	rows := []ConversationState{
		{UID: "u1", Kind: ConversationKindNormal, ChannelID: "normal-a", ChannelType: 2, ActiveAt: 100},
		{UID: "u1", Kind: ConversationKindCMD, ChannelID: "cmd-a____cmd", ChannelType: 2, ActiveAt: 300},
		{UID: "u1", Kind: ConversationKindNormal, ChannelID: "normal-b", ChannelType: 2, ActiveAt: 200},
	}
	for _, row := range rows {
		if err := shard.UpsertConversationState(ctx, row); err != nil {
			t.Fatalf("UpsertConversationState(%+v): %v", row, err)
		}
	}

	got, cursor, done, err := shard.ListConversationActivePage(ctx, ConversationKindNormal, "u1", ConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListConversationActivePage(normal): %v", err)
	}
	want := []ConversationState{
		{UID: "u1", Kind: ConversationKindNormal, ChannelID: "normal-b", ChannelType: 2, ActiveAt: 200},
		{UID: "u1", Kind: ConversationKindNormal, ChannelID: "normal-a", ChannelType: 2, ActiveAt: 100},
	}
	if !equalConversationStates(got, want) || !done || cursor.ChannelID != "normal-a" {
		t.Fatalf("normal active page = %+v cursor=%+v done=%v, want %+v done", got, cursor, done, want)
	}
}

func TestConversationTouchAndHideAreKindIsolated(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	shard := store.db.HashSlot(4)
	ctx := context.Background()

	for _, kind := range []ConversationKind{ConversationKindNormal, ConversationKindCMD} {
		if err := shard.UpsertConversationState(ctx, ConversationState{UID: "u1", Kind: kind, ChannelID: "same", ChannelType: 2, ReadSeq: 1, ActiveAt: 10}); err != nil {
			t.Fatalf("UpsertConversationState(%d): %v", kind, err)
		}
	}
	if err := shard.TouchConversationActiveAt(ctx, ConversationActivePatch{UID: "u1", Kind: ConversationKindCMD, ChannelID: "same", ChannelType: 2, ReadSeq: 8, ActiveAt: 80, UpdatedAt: 80}); err != nil {
		t.Fatalf("TouchConversationActiveAt(cmd): %v", err)
	}
	if err := shard.HideConversation(ctx, ConversationDelete{UID: "u1", Kind: ConversationKindNormal, ChannelID: "same", ChannelType: 2, DeletedToSeq: 5, UpdatedAt: 90}); err != nil {
		t.Fatalf("HideConversation(normal): %v", err)
	}
	normal, _, err := shard.GetConversationState(ctx, ConversationKindNormal, "u1", "same", 2)
	if err != nil {
		t.Fatalf("GetConversationState(normal): %v", err)
	}
	cmd, _, err := shard.GetConversationState(ctx, ConversationKindCMD, "u1", "same", 2)
	if err != nil {
		t.Fatalf("GetConversationState(cmd): %v", err)
	}
	if normal.DeletedToSeq != 5 || normal.ReadSeq != 1 || cmd.ReadSeq != 8 || cmd.DeletedToSeq != 0 {
		t.Fatalf("kind isolation failed: normal=%+v cmd=%+v", normal, cmd)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
go test ./pkg/db/meta -run 'TestConversationKindIsPartOfPrimaryKey|TestConversationActivePageScansOnlyRequestedKind|TestConversationTouchAndHideAreKindIsolated'
```

Expected: FAIL with undefined identifiers such as `ConversationState`, `ConversationKindNormal`, or `ListConversationActivePage`.

- [ ] **Step 3: Replace conversation storage types with kind-aware types**

In `pkg/db/meta/types.go`, add:

```go
// ConversationKind identifies one logical UID-owned conversation projection view.
type ConversationKind uint8

const (
	// ConversationKindNormal stores ordinary chat conversation cursors.
	ConversationKindNormal ConversationKind = 1
	// ConversationKindCMD stores command-channel sync cursors.
	ConversationKindCMD ConversationKind = 2
)
```

In `pkg/db/meta/table_conversation.go`, rename `UserConversationState` to `ConversationState`, `UserConversationKey` to `ConversationStateKey`, `UserConversationActiveCursor` to `ConversationActiveCursor`, `UserConversationActivePatch` to `ConversationActivePatch`, and `UserConversationDelete` to `ConversationDelete`. Add `Kind ConversationKind` to every row/patch/delete/key struct with English field comments.

Use this key layout:

```go
const conversationColumnKind uint16 = 7

Primary: PrimarySpec[ConversationState]{
	Name:    "pk_conversation",
	Columns: []uint16{conversationColumnUID, conversationColumnKind, conversationColumnChannelID, conversationColumnChannelType},
	Layout:  KeyLayout{KeyString, KeyUint64, KeyString, KeyInt64Ordered},
	Key: func(state ConversationState) KeyParts {
		return KeyParts{String(state.UID), Uint64(uint64(state.Kind)), String(state.ChannelID), Int64Ordered(state.ChannelType)}
	},
}
```

Use this active index layout:

```go
Columns: []uint16{conversationColumnUID, conversationColumnKind, conversationColumnActiveAt, conversationColumnChannelID, conversationColumnChannelType},
Layout:  KeyLayout{KeyString, KeyUint64, KeyInt64Desc, KeyString, KeyInt64Ordered},
Key: func(state ConversationState) (KeyParts, bool) {
	if state.ActiveAt <= 0 {
		return nil, false
	}
	return KeyParts{String(state.UID), Uint64(uint64(state.Kind)), Int64Desc(state.ActiveAt), String(state.ChannelID), Int64Ordered(state.ChannelType)}, true
},
```

Do not encode kind into `conversationColumnValue`.

- [ ] **Step 4: Remove the separate CMD conversation table**

Delete `pkg/db/meta/table_cmd_conversation.go` and `pkg/db/meta/cmd_conversation_state_test.go`.

Remove `cmd_conversation` from `pkg/db/meta/inspect.go` and table inspection tests. In `pkg/db/meta/types.go`, keep `TableIDCMDConversation` as a reserved ID with this comment:

```go
// TableIDCMDConversation is reserved by the development-era split CMD table and must not be reused.
TableIDCMDConversation uint32 = 7
```

Do not register a `cmd_conversation` table.

- [ ] **Step 5: Update storage methods**

Replace the public methods with these names:

```go
func (s *Shard) GetConversationState(ctx context.Context, kind ConversationKind, uid, channelID string, channelType int64) (ConversationState, bool, error)
func (s *Shard) UpsertConversationState(ctx context.Context, state ConversationState) error
func (s *Shard) TouchConversationActiveAt(ctx context.Context, patch ConversationActivePatch) error
func (s *Shard) ClearConversationActiveAt(ctx context.Context, kind ConversationKind, uid string, keys []ConversationKey) error
func (s *Shard) HideConversation(ctx context.Context, req ConversationDelete) error
func (s *Shard) ListConversationActive(ctx context.Context, kind ConversationKind, uid string, limit int) ([]ConversationState, error)
func (s *Shard) ListConversationActivePage(ctx context.Context, kind ConversationKind, uid string, cursor ConversationActiveCursor, limit int) ([]ConversationState, ConversationActiveCursor, bool, error)
func (s *Shard) ListConversationStatePage(ctx context.Context, kind ConversationKind, uid string, cursor ConversationCursor, limit int) ([]ConversationState, ConversationCursor, bool, error)
```

Validation must reject `kind == 0`. Existing merge semantics stay monotonic for `ReadSeq`, `DeletedToSeq`, `ActiveAt`, and `UpdatedAt`.

- [ ] **Step 6: Update key helpers and FLOW**

Update helpers in `pkg/db/meta/keys.go` so conversation row/index keys include kind. Remove command conversation key helpers. Update `pkg/db/meta/FLOW.md` to describe one kind-aware conversation table and remove the separate CMD table statement.

- [ ] **Step 7: Run storage tests**

Run:

```bash
go test ./pkg/db/meta
```

Expected: PASS.

- [ ] **Step 8: Commit storage model**

```bash
git add pkg/db/meta
git commit -m "refactor: unify conversation metadata by kind"
```

---

### Task 2: Replace Slot FSM Conversation Commands with Generic Kind-Aware Commands

**Files:**

- Modify: `pkg/db/meta/compat.go`
- Modify: `pkg/slot/fsm/command.go`
- Modify: `pkg/slot/fsm/command_inspection.go`
- Modify: `pkg/slot/fsm/command_inspection_test.go`
- Modify: `pkg/slot/fsm/state_machine_test.go`

- [ ] **Step 1: Write failing FSM tests for kind round-trip and apply isolation**

In `pkg/slot/fsm/state_machine_test.go`, replace the separate user/CMD tests with:

```go
func TestApplyBatchConversationStateKindsStayIsolated(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	bsm, ok := mustNewStateMachine(t, db, 11).(multiraft.BatchStateMachine)
	if !ok {
		t.Fatal("state machine does not implement multiraft.BatchStateMachine")
	}

	command, err := EncodeUpsertConversationStatesCommandChecked([]metadb.ConversationState{
		{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "g1", ChannelType: 2, ReadSeq: 1, ActiveAt: 100},
		{UID: "u1", Kind: metadb.ConversationKindCMD, ChannelID: "g1", ChannelType: 2, ReadSeq: 8, ActiveAt: 200},
	})
	if err != nil {
		t.Fatalf("EncodeUpsertConversationStatesCommandChecked(): %v", err)
	}
	results, err := bsm.ApplyBatch(ctx, []multiraft.Command{{SlotID: 11, Index: 1, Term: 1, Data: command}})
	if err != nil || len(results) != 1 || string(results[0]) != ApplyResultOK {
		t.Fatalf("ApplyBatch() results=%q err=%v", results, err)
	}
	normal, _, err := db.ForHashSlot(11).GetConversationState(ctx, metadb.ConversationKindNormal, "u1", "g1", 2)
	if err != nil {
		t.Fatalf("GetConversationState(normal): %v", err)
	}
	cmd, _, err := db.ForHashSlot(11).GetConversationState(ctx, metadb.ConversationKindCMD, "u1", "g1", 2)
	if err != nil {
		t.Fatalf("GetConversationState(cmd): %v", err)
	}
	if normal.ReadSeq != 1 || cmd.ReadSeq != 8 {
		t.Fatalf("conversation states = normal:%+v cmd:%+v", normal, cmd)
	}
}

func TestConversationBatchCheckedEncoderRejectsOwnedHashSlotMismatch(t *testing.T) {
	const hashSlotCount = 256
	uidForFive := uidForHashSlot(t, hashSlotCount, 5)
	_, err := EncodeUpsertConversationStateBatchCommandChecked(hashSlotCount, []ConversationStateBatchItem{
		{HashSlot: 7, State: metadb.ConversationState{UID: uidForFive, Kind: metadb.ConversationKindNormal, ChannelID: "g", ChannelType: 2}},
	})
	if !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("EncodeUpsertConversationStateBatchCommandChecked() error = %v, want ErrInvalidArgument", err)
	}
}
```

In `pkg/slot/fsm/command_inspection_test.go`, assert the inspection payload includes `"kind": "normal"` or numeric kind for every conversation command.

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
go test ./pkg/slot/fsm -run 'TestApplyBatchConversationStateKindsStayIsolated|TestConversationBatchCheckedEncoderRejectsOwnedHashSlotMismatch|TestDecodeCommandInspectionIncludes.*Conversation'
```

Expected: FAIL with missing `EncodeUpsertConversationStatesCommandChecked` or old type names.

- [ ] **Step 3: Rename command entries and codecs**

In `pkg/slot/fsm/command.go`, replace user-specific batch item names with:

```go
type ConversationStateBatchItem struct {
	// HashSlot is the logical hash slot that owns State.UID.
	HashSlot uint16
	// State is the durable conversation state row.
	State metadb.ConversationState
}

type ConversationActivePatchBatchItem struct {
	// HashSlot is the logical hash slot that owns Patch.UID.
	HashSlot uint16
	// Patch is the active-at mutation for one conversation row.
	Patch metadb.ConversationActivePatch
}

type ConversationDeleteBatchItem struct {
	// HashSlot is the logical hash slot that owns Delete.UID.
	HashSlot uint16
	// Delete hides one conversation row.
	Delete metadb.ConversationDelete
}
```

Encode `Kind` in every conversation state, patch, and delete entry. Use a new TLV tag near the existing conversation tags:

```go
tagConversationEntryKind uint8 = 8
```

The decoder must require `Kind != 0`.

- [ ] **Step 4: Collapse command functions into generic names**

Use these exported command names:

```go
func EncodeUpsertConversationStatesCommand(states []metadb.ConversationState) []byte
func EncodeUpsertConversationStatesCommandChecked(states []metadb.ConversationState) ([]byte, error)
func EncodeUpsertConversationStateBatchCommand(items []ConversationStateBatchItem) []byte
func EncodeUpsertConversationStateBatchCommandChecked(hashSlotCount uint16, items []ConversationStateBatchItem) ([]byte, error)
func EncodeTouchConversationActiveAtCommand(patches []metadb.ConversationActivePatch) []byte
func EncodeTouchConversationActiveAtCommandChecked(patches []metadb.ConversationActivePatch) ([]byte, error)
func EncodeTouchConversationActiveAtBatchCommand(items []ConversationActivePatchBatchItem) []byte
func EncodeTouchConversationActiveAtBatchCommandChecked(hashSlotCount uint16, items []ConversationActivePatchBatchItem) ([]byte, error)
func EncodeHideConversationsCommand(deletes []metadb.ConversationDelete) []byte
func EncodeHideConversationsCommandChecked(deletes []metadb.ConversationDelete) ([]byte, error)
func EncodeHideConversationBatchCommand(items []ConversationDeleteBatchItem) []byte
func EncodeHideConversationBatchCommandChecked(hashSlotCount uint16, items []ConversationDeleteBatchItem) ([]byte, error)
```

Remove the exported CMD-specific command functions from the final code.

- [ ] **Step 5: Add the generic write-batch methods used by apply paths**

In `pkg/db/meta/compat.go`, add these methods on `WriteBatch`:

```go
func (b *WriteBatch) UpsertConversationState(hashSlot uint16, state ConversationState) error
func (b *WriteBatch) TouchConversationActiveAt(hashSlot uint16, patches []ConversationActivePatch) error
func (b *WriteBatch) HideConversation(hashSlot uint16, req ConversationDelete) error
```

Each method should stage operations against `conversationTable` using the kind-aware primary key. `TouchConversationActiveAt` must keep the existing read-your-writes overlay behavior for active timestamps, sparse-active changes, delete fences, read floors, and delete floors.

- [ ] **Step 6: Update apply paths**

Replace calls to `wb.UpsertUserConversationState`, `wb.TouchUserConversationActiveAt`, and `wb.HideUserConversation` with the generic write batch methods added in Step 5. Apply commands by row `Kind`; do not branch on `ConversationKindCMD`.

- [ ] **Step 7: Run FSM tests**

Run:

```bash
go test ./pkg/slot/fsm
```

Expected: PASS.

- [ ] **Step 8: Commit FSM command update**

```bash
git add pkg/db/meta/compat.go pkg/slot/fsm
git commit -m "refactor: make slot conversation commands kind-aware"
```

---

### Task 3: Finalize `pkg/db/meta` Compatibility Surface

**Files:**

- Modify: `pkg/db/meta/compat.go`
- Modify: `pkg/db/meta/user_conversation_state_test.go`

- [ ] **Step 1: Write failing shard-store tests**

Add to `pkg/db/meta/user_conversation_state_test.go`:

```go
func TestShardStoreConversationKindMethods(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Fatalf("Close(): %v", err)
		}
	}()
	ctx := context.Background()
	shard := db.ForHashSlot(4)

	if err := shard.UpsertConversationState(ctx, ConversationState{UID: "u1", Kind: ConversationKindNormal, ChannelID: "g1", ChannelType: 2, ReadSeq: 1}); err != nil {
		t.Fatalf("UpsertConversationState(normal): %v", err)
	}
	if err := shard.UpsertConversationState(ctx, ConversationState{UID: "u1", Kind: ConversationKindCMD, ChannelID: "g1", ChannelType: 2, ReadSeq: 7}); err != nil {
		t.Fatalf("UpsertConversationState(cmd): %v", err)
	}
	normal, err := shard.GetConversationState(ctx, ConversationKindNormal, "u1", "g1", 2)
	if err != nil {
		t.Fatalf("GetConversationState(normal): %v", err)
	}
	cmd, err := shard.GetConversationState(ctx, ConversationKindCMD, "u1", "g1", 2)
	if err != nil {
		t.Fatalf("GetConversationState(cmd): %v", err)
	}
	if normal.ReadSeq != 1 || cmd.ReadSeq != 7 {
		t.Fatalf("shard store states = normal:%+v cmd:%+v", normal, cmd)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
go test ./pkg/db/meta -run TestShardStoreConversationKindMethods
```

Expected: FAIL with missing `ShardStore.UpsertConversationState` or `ShardStore.GetConversationState`.

- [ ] **Step 3: Replace compatibility methods**

In `pkg/db/meta/compat.go`, expose generic `ShardStore` methods:

```go
func (s *ShardStore) GetConversationState(ctx context.Context, kind ConversationKind, uid, channelID string, channelType int64) (ConversationState, error)
func (s *ShardStore) UpsertConversationState(ctx context.Context, state ConversationState) error
func (s *ShardStore) TouchConversationActiveAt(ctx context.Context, patch ConversationActivePatch) error
func (s *ShardStore) HideConversation(ctx context.Context, req ConversationDelete) error
func (s *ShardStore) ListConversationActive(ctx context.Context, kind ConversationKind, uid string, limit int) ([]ConversationState, error)
func (s *ShardStore) ListConversationActivePage(ctx context.Context, kind ConversationKind, uid string, after ConversationActiveCursor, limit int) ([]ConversationState, ConversationActiveCursor, bool, error)
```

Keep the generic `WriteBatch` methods from Task 2:

```go
func (b *WriteBatch) UpsertConversationState(hashSlot uint16, state ConversationState) error
func (b *WriteBatch) TouchConversationActiveAt(hashSlot uint16, patches []ConversationActivePatch) error
func (b *WriteBatch) HideConversation(hashSlot uint16, req ConversationDelete) error
```

Delete `UpsertCMDConversationState` and `AdvanceCMDConversationReadSeq` from the final compatibility surface. CMD read advancement will use `UpsertConversationState` with `Kind: ConversationKindCMD`.

- [ ] **Step 4: Run compatibility tests**

Run:

```bash
go test ./pkg/db/meta
```

Expected: PASS.

- [ ] **Step 5: Commit compatibility surface**

```bash
git add pkg/db/meta
git commit -m "refactor: expose generic conversation metadata batches"
```

---

### Task 4: Update `pkg/clusterv2` Conversation Facade

**Files:**

- Modify: `pkg/clusterv2/node_meta.go`
- Modify: `pkg/clusterv2/node_meta_conversation_test.go`

- [ ] **Step 1: Write failing clusterv2 facade tests**

In `pkg/clusterv2/node_meta_conversation_test.go`, replace user-specific test names with generic tests:

```go
func TestClusterV2ConversationBatchFacadeRoutesByUIDAndKind(t *testing.T) {
	node := newTestNodeWithMeta(t)
	ctx := context.Background()

	uid := uidForHashSlot(t, node.cfg.Slots.HashSlotCount, 5)
	if err := node.UpsertConversationStatesBatch(ctx, []metadb.ConversationState{
		{UID: uid, Kind: metadb.ConversationKindNormal, ChannelID: "g1", ChannelType: 2, ActiveAt: 100},
		{UID: uid, Kind: metadb.ConversationKindCMD, ChannelID: "g1", ChannelType: 2, ActiveAt: 300},
	}); err != nil {
		t.Fatalf("UpsertConversationStatesBatch(): %v", err)
	}

	normalPage, _, done, err := node.ListConversationActivePage(ctx, metadb.ConversationKindNormal, uid, metadb.ConversationActiveCursor{}, 10)
	if err != nil || !done {
		t.Fatalf("ListConversationActivePage(normal) done=%v err=%v", done, err)
	}
	cmdPage, _, done, err := node.ListConversationActivePage(ctx, metadb.ConversationKindCMD, uid, metadb.ConversationActiveCursor{}, 10)
	if err != nil || !done {
		t.Fatalf("ListConversationActivePage(cmd) done=%v err=%v", done, err)
	}
	if len(normalPage) != 1 || normalPage[0].Kind != metadb.ConversationKindNormal || len(cmdPage) != 1 || cmdPage[0].Kind != metadb.ConversationKindCMD {
		t.Fatalf("pages = normal:%+v cmd:%+v", normalPage, cmdPage)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
go test ./pkg/clusterv2 -run TestClusterV2ConversationBatchFacadeRoutesByUIDAndKind
```

Expected: FAIL with missing `UpsertConversationStatesBatch`.

- [ ] **Step 3: Replace facade methods**

In `pkg/clusterv2/node_meta.go`, expose:

```go
func (n *Node) UpsertConversationStatesBatch(ctx context.Context, states []metadb.ConversationState) error
func (n *Node) HideConversationsBatch(ctx context.Context, deletes []metadb.ConversationDelete) error
func (n *Node) TouchConversationActiveAtBatch(ctx context.Context, patches []metadb.ConversationActivePatch) error
func (n *Node) GetConversationState(ctx context.Context, kind metadb.ConversationKind, uid, channelID string, channelType int64) (metadb.ConversationState, bool, error)
func (n *Node) GetConversationStates(ctx context.Context, keys []metadb.ConversationStateKey) (map[metadb.ConversationStateKey]metadb.ConversationState, error)
func (n *Node) ListConversationActivePage(ctx context.Context, kind metadb.ConversationKind, uid string, after metadb.ConversationActiveCursor, limit int) ([]metadb.ConversationState, metadb.ConversationActiveCursor, bool, error)
```

Group mutations by UID route exactly as current user conversation methods do. Build `metafsm.ConversationStateBatchItem`, `ConversationActivePatchBatchItem`, and `ConversationDeleteBatchItem`.

- [ ] **Step 4: Remove old facade names from final call sites**

Update callers in this task only where compile requires it inside `pkg/clusterv2`. Do not keep exported `UpsertUserConversationStatesBatch`, `TouchUserConversationActiveAtBatch`, or CMD-specific facade names in the final clusterv2 API.

- [ ] **Step 5: Run clusterv2 tests**

Run:

```bash
go test ./pkg/clusterv2
```

Expected: PASS.

- [ ] **Step 6: Commit clusterv2 facade**

```bash
git add pkg/clusterv2
git commit -m "refactor: expose kind-aware conversation facade"
```

---

### Task 5: Make `conversationactive` Kind-Aware

**Files:**

- Modify: `internalv2/runtime/conversationactive/types.go`
- Modify: `internalv2/runtime/conversationactive/manager.go`
- Modify: `internalv2/runtime/conversationactive/active_view.go`
- Modify: `internalv2/runtime/conversationactive/manager_test.go`
- Modify: `internalv2/runtime/conversationactive/manager_benchmark_test.go`
- Create: `internalv2/runtime/conversationactive/FLOW.md`

- [ ] **Step 1: Write failing runtime tests**

Add or update tests in `internalv2/runtime/conversationactive/manager_test.go`:

```go
func TestManagerKeepsKindsIsolatedForSameChannel(t *testing.T) {
	ctx := context.Background()
	store := &recordingActiveStore{}
	m := NewManager(Options{Store: store})

	if err := m.MarkActive(ctx, []ActivePatch{
		{Kind: metadb.ConversationKindNormal, UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAtMS: 100, ReadSeq: 1},
		{Kind: metadb.ConversationKindCMD, UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAtMS: 200, ReadSeq: 9},
	}); err != nil {
		t.Fatalf("MarkActive(): %v", err)
	}

	normal, err := m.ListActiveView(ctx, metadb.ConversationKindNormal, "u1", metadb.ConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListActiveView(normal): %v", err)
	}
	cmd, err := m.ListActiveView(ctx, metadb.ConversationKindCMD, "u1", metadb.ConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListActiveView(cmd): %v", err)
	}
	if len(normal.Rows) != 1 || normal.Rows[0].Kind != metadb.ConversationKindNormal || normal.Rows[0].ReadSeq != 1 {
		t.Fatalf("normal page = %+v", normal.Rows)
	}
	if len(cmd.Rows) != 1 || cmd.Rows[0].Kind != metadb.ConversationKindCMD || cmd.Rows[0].ReadSeq != 9 {
		t.Fatalf("cmd page = %+v", cmd.Rows)
	}
}

func TestFlushPersistsKindAwarePatches(t *testing.T) {
	ctx := context.Background()
	store := &recordingActiveStore{}
	m := NewManager(Options{Store: store})
	if err := m.MarkActive(ctx, []ActivePatch{{Kind: metadb.ConversationKindCMD, UID: "u1", ChannelID: "g1____cmd", ChannelType: 2, ActiveAtMS: 1000, ReadSeq: 7}}); err != nil {
		t.Fatalf("MarkActive(): %v", err)
	}
	if _, err := m.Flush(ctx, 10); err != nil {
		t.Fatalf("Flush(): %v", err)
	}
	if len(store.touches) != 1 || len(store.touches[0]) != 1 {
		t.Fatalf("touches = %+v", store.touches)
	}
	if got := store.touches[0][0]; got.Kind != metadb.ConversationKindCMD || got.ChannelID != "g1____cmd" || got.ReadSeq != 7 {
		t.Fatalf("touch patch = %+v", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
go test ./internalv2/runtime/conversationactive -run 'TestManagerKeepsKindsIsolatedForSameChannel|TestFlushPersistsKindAwarePatches'
```

Expected: FAIL with missing `Kind` field or old method signature.

- [ ] **Step 3: Update runtime types**

In `types.go`, add `Kind metadb.ConversationKind` to `ActiveBatch` and `ActivePatch`. Replace `ActiveStore` with:

```go
type ActiveStore interface {
	ListConversationActivePage(ctx context.Context, kind metadb.ConversationKind, uid string, after metadb.ConversationActiveCursor, limit int) ([]metadb.ConversationState, metadb.ConversationActiveCursor, bool, error)
	GetConversationState(ctx context.Context, kind metadb.ConversationKind, uid, channelID string, channelType int64) (metadb.ConversationState, bool, error)
	GetConversationStates(ctx context.Context, keys []metadb.ConversationStateKey) (map[metadb.ConversationStateKey]metadb.ConversationState, error)
	TouchConversationActiveAt(ctx context.Context, patches []metadb.ConversationActivePatch) error
}
```

`ActiveViewPage.Rows` becomes `[]metadb.ConversationState` and `Cursor` becomes `metadb.ConversationActiveCursor`.

- [ ] **Step 4: Update cache key**

In `manager.go`, change:

```go
type conversationKey struct {
	kind        metadb.ConversationKind
	channelID   string
	channelType uint8
}
```

Every place constructing `conversationKey` must include `patch.Kind`. `AdmitActiveBatch` must copy `batch.Kind` into every produced patch and reject no rows for kind zero by relying on store validation at flush time.

- [ ] **Step 5: Update active view merge**

Change:

```go
func (m *Manager) ListActiveView(ctx context.Context, kind metadb.ConversationKind, uid string, after metadb.ConversationActiveCursor, limit int) (ActiveViewPage, error)
```

Filter cache rows by `key.kind == kind`. Durable reads call `m.store.ListConversationActivePage(ctx, kind, uid, after, dbLimit)`. Hydration calls `GetConversationState(ctx, kind, uid, channelID, channelType)`.

- [ ] **Step 6: Update flush conversion**

Replace `activePatchMetaPatch` with:

```go
func activePatchMetaPatch(patch ActivePatch) metadb.ConversationActivePatch {
	return metadb.ConversationActivePatch{
		UID:         patch.UID,
		Kind:        patch.Kind,
		ChannelID:   patch.ChannelID,
		ChannelType: int64(patch.ChannelType),
		ReadSeq:     patch.ReadSeq,
		ActiveAt:    patch.ActiveAtMS,
		UpdatedAt:   patch.ActiveAtMS,
	}
}
```

Cooldown keys use `metadb.ConversationStateKey{UID: entry.patch.UID, Kind: entry.patch.Kind, ChannelID: entry.patch.ChannelID, ChannelType: int64(entry.patch.ChannelType)}`.

- [ ] **Step 7: Add FLOW**

Create `internalv2/runtime/conversationactive/FLOW.md`:

```markdown
# internalv2/runtime/conversationactive Flow

`conversationactive` owns the in-memory UID-owned active conversation cache for
all conversation kinds. Callers provide a `metadb.ConversationKind`; this package
does not infer kind from channel IDs.

Current flow:

1. Channel append or another authority-owned caller submits an `ActiveBatch`.
2. The manager merges rows by `(uid, kind, channel_id, channel_type)`.
3. Dirty rows flush through `ActiveStore.TouchConversationActiveAt`.
4. Active view reads merge cache rows with durable `(uid, kind)` active-index pages.
5. Hash-slot handoff drains dirty rows scoped by UID hash slot before authority moves.
```

- [ ] **Step 8: Run runtime tests and benchmark compile**

Run:

```bash
go test ./internalv2/runtime/conversationactive
go test ./internalv2/runtime/conversationactive -run '^$' -bench BenchmarkMarkActiveCoalesces100KUpdates -benchtime=1x
```

Expected: PASS. The benchmark command must compile and complete without panics.

- [ ] **Step 9: Commit runtime update**

```bash
git add internalv2/runtime/conversationactive
git commit -m "refactor: make conversation active runtime kind-aware"
```

---

### Task 6: Update Ordinary Conversation Usecase and Cluster Adapter to Normal Kind

**Files:**

- Modify: `internalv2/usecase/conversation/app.go`
- Modify: `internalv2/usecase/conversation/types.go`
- Modify: `internalv2/usecase/conversation/sync.go`
- Modify: `internalv2/usecase/conversation/unread.go`
- Modify: `internalv2/usecase/conversation/*_test.go`
- Modify: `internalv2/infra/cluster/conversation.go`
- Modify: `internalv2/infra/cluster/conversation_test.go`

- [ ] **Step 1: Write failing normal-kind tests**

In `internalv2/usecase/conversation/sync_test.go`, update the command-filter test into a kind-isolation test:

```go
func TestSyncReadsOnlyNormalKindRows(t *testing.T) {
	store := newConversationSyncStore()
	store.active = []metadb.ConversationState{
		{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "chat", ChannelType: 2, ReadSeq: 1, ActiveAt: 100},
		{UID: "u1", Kind: metadb.ConversationKindCMD, ChannelID: "chat____cmd", ChannelType: 2, ReadSeq: 1, ActiveAt: 200},
	}
	store.last[metadb.ConversationKey{ChannelID: "chat", ChannelType: 2}] = LastMessage{MessageSeq: 3, ServerTimestampMS: 1000}

	app := New(Options{Store: store, StateStore: store, Messages: store})
	got, err := app.Sync(context.Background(), SyncQuery{UID: "u1"})
	if err != nil {
		t.Fatalf("Sync(): %v", err)
	}
	if gotIDs := syncConversationIDs(got.Conversations); !reflect.DeepEqual(gotIDs, []string{"chat"}) {
		t.Fatalf("conversation IDs = %#v, want normal kind only", gotIDs)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
go test ./internalv2/usecase/conversation -run TestSyncReadsOnlyNormalKindRows
```

Expected: FAIL with old metadb type names or old store signatures.

- [ ] **Step 3: Update usecase store interfaces**

In `app.go`, replace metadb user-specific types with generic conversation types. The ordinary usecase must always pass `metadb.ConversationKindNormal`.

```go
type Store interface {
	ListConversationActiveView(ctx context.Context, kind metadb.ConversationKind, uid string, after metadb.ConversationActiveCursor, limit int) (ActiveViewPage, error)
}

type StateStore interface {
	GetConversationState(ctx context.Context, kind metadb.ConversationKind, uid, channelID string, channelType int64) (metadb.ConversationState, bool, error)
}
```

`List`, `Sync`, `ClearUnread`, `SetUnread`, and `DeleteConversation` use `metadb.ConversationKindNormal`.

- [ ] **Step 4: Remove suffix filtering from ordinary conversation sync**

In `sync.go`, delete logic that filters command channels by suffix. The active rows are already scoped to normal kind.

- [ ] **Step 5: Update cluster adapter**

In `internalv2/infra/cluster/conversation.go`, update node interfaces and store methods to call:

```go
ListConversationActivePage(ctx, metadb.ConversationKindNormal, uid, after, limit)
GetConversationState(ctx, metadb.ConversationKindNormal, uid, channelID, channelType)
UpsertConversationStatesBatch(ctx, states)
HideConversationsBatch(ctx, deletes)
```

Recent and last-message reads remain channel-log reads and do not need kind.

- [ ] **Step 6: Run usecase and adapter tests**

Run:

```bash
go test ./internalv2/usecase/conversation ./internalv2/infra/cluster
```

Expected: PASS.

- [ ] **Step 7: Commit ordinary conversation update**

```bash
git add internalv2/usecase/conversation internalv2/infra/cluster
git commit -m "refactor: route ordinary conversations by normal kind"
```

---

### Task 7: Update App Conversation Authority and RPC Surfaces

**Files:**

- Modify: `internalv2/app/conversation_authority.go`
- Modify: `internalv2/app/conversation_authority_test.go`
- Modify: `internalv2/infra/cluster/conversation_authority.go`
- Modify: `internalv2/infra/cluster/conversation_authority_test.go`
- Modify: `internalv2/app/app_test.go`
- Modify: `internalv2/app/FLOW.md`

- [ ] **Step 1: Write failing authority test for kind transport**

In `internalv2/infra/cluster/conversation_authority_test.go`, add:

```go
func TestConversationAuthorityClientAdmitActiveBatchPreservesKind(t *testing.T) {
	local := &fakeConversationAuthorityLocal{}
	client := NewConversationAuthorityClient(ConversationAuthorityClientOptions{
		LocalNodeID: 1,
		Local:       local,
		Router:      fixedConversationAuthorityRouter(1),
	})
	err := client.AdmitActiveBatch(context.Background(), conversationactive.ActiveBatch{
		Kind:        metadb.ConversationKindCMD,
		SenderUID:   "sender",
		ChannelID:   "g1____cmd",
		ChannelType: 2,
		MessageSeq:  9,
		ActiveAtMS:  1000,
		Recipients:  []conversationactive.ActiveEntry{{UID: "receiver"}},
	})
	if err != nil {
		t.Fatalf("AdmitActiveBatch(): %v", err)
	}
	if len(local.admitCalls) != 1 || local.admitCalls[0].batch.Kind != metadb.ConversationKindCMD {
		t.Fatalf("admit calls = %+v", local.admitCalls)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
go test ./internalv2/infra/cluster -run TestConversationAuthorityClientAdmitActiveBatchPreservesKind
```

Expected: FAIL until runtime and RPC DTOs carry kind.

- [ ] **Step 3: Update app store adapter**

In `internalv2/app/conversation_authority.go`, replace `conversationAuthorityStore` methods with:

```go
ListConversationActivePage(context.Context, metadb.ConversationKind, string, metadb.ConversationActiveCursor, int) ([]metadb.ConversationState, metadb.ConversationActiveCursor, bool, error)
GetConversationState(context.Context, metadb.ConversationKind, string, string, int64) (metadb.ConversationState, bool, error)
GetConversationStates(context.Context, []metadb.ConversationStateKey) (map[metadb.ConversationStateKey]metadb.ConversationState, error)
TouchConversationActiveAtBatch(context.Context, []metadb.ConversationActivePatch) error
```

Update `conversationActiveStoreAdapter` to implement the new `conversationactive.ActiveStore` interface.

- [ ] **Step 4: Update authority list signatures**

Change list methods to include kind:

```go
func (a *conversationAuthority) ListConversationActiveView(ctx context.Context, kind metadb.ConversationKind, uid string, after metadb.ConversationActiveCursor, limit int) (conversationusecase.ActiveViewPage, error)
func (a *conversationAuthority) ListConversationActiveViewForTarget(ctx context.Context, target conversationusecase.RouteTarget, kind metadb.ConversationKind, uid string, after metadb.ConversationActiveCursor, limit int) (conversationusecase.ActiveViewPage, error)
```

Ordinary conversation callers pass `ConversationKindNormal`. CMD sync callers added later pass `ConversationKindCMD`.

- [ ] **Step 5: Update RPC codecs or DTOs**

Where `internalv2/infra/cluster/conversation_authority.go` sends `conversationactive.ActiveBatch`, ensure `Kind` is encoded and decoded for local and remote authority calls. Tests must verify sender and receiver split preserves kind in each target group.

- [ ] **Step 6: Update FLOW**

In `internalv2/app/FLOW.md`, describe conversation authority as kind-aware and UID-owned. State that single-node cluster uses the same authority route path.

- [ ] **Step 7: Run app and cluster authority tests**

Run:

```bash
go test ./internalv2/app ./internalv2/infra/cluster
```

Expected: PASS.

- [ ] **Step 8: Commit app authority update**

```bash
git add internalv2/app internalv2/infra/cluster
git commit -m "refactor: carry conversation kind through authority"
```

---

### Task 8: Emit Conversation Kind from Channel Append

**Files:**

- Modify: `internalv2/runtime/channelappend/options.go`
- Modify: `internalv2/runtime/channelappend/recipient.go`
- Modify: `internalv2/runtime/channelappend/recipient_test.go`
- Modify: `internalv2/runtime/channelappend/FLOW.md`

- [ ] **Step 1: Write failing recipient tests**

In `internalv2/runtime/channelappend/recipient_test.go`, add:

```go
func TestRecipientProcessorAdmitsNormalConversationKind(t *testing.T) {
	active := &recordingActiveAdmitterForRecipientTest{}
	ports := recipientProcessorTestPorts()
	ports.activeAdmitter = active
	event := recipientCommittedEnvelopeForTest("chat", 2)
	event.SyncOnce = false

	if err := dispatchRecipientSet(context.Background(), event, []Recipient{{UID: "u1"}}, []string{"u1"}, ports); err != nil {
		t.Fatalf("dispatchRecipientSet(): %v", err)
	}
	if len(active.batches) != 1 || active.batches[0].Kind != metadb.ConversationKindNormal {
		t.Fatalf("active batches = %+v", active.batches)
	}
}

func TestRecipientProcessorAdmitsCMDConversationKindForSyncOnce(t *testing.T) {
	active := &recordingActiveAdmitterForRecipientTest{}
	ports := recipientProcessorTestPorts()
	ports.activeAdmitter = active
	event := recipientCommittedEnvelopeForTest("chat____cmd", 2)
	event.SyncOnce = true

	if err := dispatchRecipientSet(context.Background(), event, []Recipient{{UID: "u1"}}, []string{"u1"}, ports); err != nil {
		t.Fatalf("dispatchRecipientSet(): %v", err)
	}
	if len(active.batches) != 1 || active.batches[0].Kind != metadb.ConversationKindCMD {
		t.Fatalf("active batches = %+v", active.batches)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
go test ./internalv2/runtime/channelappend -run 'TestRecipientProcessorAdmits.*ConversationKind'
```

Expected: FAIL until active batches carry kind.

- [ ] **Step 3: Add kind selection helper**

In `recipient.go`, add:

```go
func conversationKindForCommittedEnvelope(event CommittedEnvelope) metadb.ConversationKind {
	if event.SyncOnce || runtimechannelid.IsCommandChannel(event.ChannelID) {
		return metadb.ConversationKindCMD
	}
	return metadb.ConversationKindNormal
}
```

Use this helper when building `conversationactive.ActiveBatch`.

- [ ] **Step 4: Keep active admission single-path**

Do not add a second CMD active sink in channel append. Both normal and CMD activity call the same `ConversationActiveAdmitter` with different `Kind`.

- [ ] **Step 5: Update FLOW**

In `internalv2/runtime/channelappend/FLOW.md`, state that post-commit recipient processing emits explicit `ConversationKindNormal` or `ConversationKindCMD` activity and does not infer kind inside `conversationactive`.

- [ ] **Step 6: Run channel append tests**

Run:

```bash
go test ./internalv2/runtime/channelappend
```

Expected: PASS.

- [ ] **Step 7: Commit channel append kind emission**

```bash
git add internalv2/runtime/channelappend
git commit -m "refactor: emit conversation kind from channel append"
```

---

### Task 9: Add `internalv2/usecase/cmdsync` on the Unified Projection

**Files:**

- Create: `internalv2/usecase/cmdsync/types.go`
- Create: `internalv2/usecase/cmdsync/app.go`
- Create: `internalv2/usecase/cmdsync/records.go`
- Create: `internalv2/usecase/cmdsync/import_boundary_test.go`
- Create: `internalv2/usecase/cmdsync/app_test.go`

- [ ] **Step 1: Write failing CMD sync usecase tests**

Create `internalv2/usecase/cmdsync/app_test.go` with:

```go
func TestSyncReadsCMDKindRowsAndStripsCommandSuffix(t *testing.T) {
	store := newCmdSyncStore()
	store.active = []metadb.ConversationState{{
		UID: "u1", Kind: metadb.ConversationKindCMD, ChannelID: "g1____cmd", ChannelType: 2, ActiveAt: 100,
	}}
	store.messages[CommandChannelKey{ChannelID: "g1____cmd", ChannelType: 2}] = []SyncedMessage{{
		MessageID: 1, MessageSeq: 3, ChannelID: "g1____cmd", ChannelType: 2, FromUID: "u2", Payload: []byte("cmd"),
	}}
	app := New(Options{States: store, Messages: store})

	got, err := app.Sync(context.Background(), SyncQuery{UID: "u1", Limit: 10})
	if err != nil {
		t.Fatalf("Sync(): %v", err)
	}
	if len(got.Messages) != 1 || got.Messages[0].ChannelID != "g1" || got.Messages[0].MessageSeq != 3 {
		t.Fatalf("messages = %+v", got.Messages)
	}
}

func TestSyncAckAdvancesCMDKindReadSeqOnlyFromLatestGeneration(t *testing.T) {
	store := newCmdSyncStore()
	store.active = []metadb.ConversationState{{
		UID: "u1", Kind: metadb.ConversationKindCMD, ChannelID: "g1____cmd", ChannelType: 2, ActiveAt: 100,
	}}
	store.messages[CommandChannelKey{ChannelID: "g1____cmd", ChannelType: 2}] = []SyncedMessage{{MessageSeq: 5, ChannelID: "g1____cmd", ChannelType: 2}}
	app := New(Options{States: store, Messages: store})

	if _, err := app.Sync(context.Background(), SyncQuery{UID: "u1", Limit: 10}); err != nil {
		t.Fatalf("Sync(): %v", err)
	}
	if err := app.SyncAck(context.Background(), SyncAckCommand{UID: "u1", LastMessageSeq: 5}); err != nil {
		t.Fatalf("SyncAck(): %v", err)
	}
	if len(store.upserts) != 1 || store.upserts[0].Kind != metadb.ConversationKindCMD || store.upserts[0].ReadSeq != 5 {
		t.Fatalf("upserts = %+v", store.upserts)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
go test ./internalv2/usecase/cmdsync
```

Expected: FAIL because package does not exist.

- [ ] **Step 3: Define v2 CMD sync DTOs**

In `types.go`, define:

```go
type SyncQuery struct {
	UID string
	MessageSeq uint64
	Limit int
}

type SyncAckCommand struct {
	UID string
	LastMessageSeq uint64
}

type SyncedMessage struct {
	MessageID uint64
	MessageSeq uint64
	ChannelID string
	ChannelType uint8
	FromUID string
	ClientMsgNo string
	ServerTimestampMS int64
	Payload []byte
}

type SyncResult struct {
	Messages []SyncedMessage
}

type StateStore interface {
	ListConversationActiveView(ctx context.Context, uid string, limit int) ([]metadb.ConversationState, error)
	UpsertConversationStates(ctx context.Context, states []metadb.ConversationState) error
}

type MessageStore interface {
	LoadCommandMessages(ctx context.Context, key CommandChannelKey, fromSeq uint64, limit int) ([]SyncedMessage, error)
}
```

`StateStore` must be CMD-only from the usecase point of view; the infra adapter supplies `ConversationKindCMD`.

- [ ] **Step 4: Port sync generation logic from legacy without legacy imports**

Port the legacy `internal/usecase/cmdsync` logic into v2, replacing `pkg/channel.Message` with `SyncedMessage` and `metadb.CMDConversationState` with `metadb.ConversationState`.

Rules:

- `Sync` lists only CMD kind rows through the store.
- `fromSeq = max(ReadSeq, DeletedToSeq) + 1`.
- results sort by timestamp/channel/type/seq/messageID.
- client-facing response strips one command suffix.
- `SyncAck` upserts CMD kind states with advanced `ReadSeq`.
- no `internal/usecase/cmdsync` imports in `internalv2/usecase/cmdsync`.

- [ ] **Step 5: Add import boundary test**

Create `internalv2/usecase/cmdsync/import_boundary_test.go`:

```go
func TestCmdSyncUsecaseImportBoundary(t *testing.T) {
	for _, forbidden := range []string{
		"github.com/WuKongIM/WuKongIM/internal/access",
		"github.com/WuKongIM/WuKongIM/internal/app",
		"github.com/WuKongIM/WuKongIM/internal/usecase/cmdsync",
		"github.com/WuKongIM/WuKongIM/internalv2/access",
		"github.com/WuKongIM/WuKongIM/internalv2/app",
		"github.com/WuKongIM/WuKongIM/pkg/gateway",
		"github.com/WuKongIM/WuKongIM/pkg/protocol/frame",
	} {
		if packageImports(t, ".", forbidden) {
			t.Fatalf("cmdsync usecase imports forbidden package %s", forbidden)
		}
	}
}
```

Use the same import scanning helper style as existing `internalv2/usecase/conversation/import_boundary_test.go`.

- [ ] **Step 6: Run CMD sync usecase tests**

Run:

```bash
go test ./internalv2/usecase/cmdsync
```

Expected: PASS.

- [ ] **Step 7: Commit CMD sync usecase**

```bash
git add internalv2/usecase/cmdsync
git commit -m "feat: add v2 cmd sync usecase"
```

---

### Task 10: Add CMD Sync Cluster Adapter, API Routes, and Wiring

**Files:**

- Create: `internalv2/infra/cluster/cmdsync.go`
- Modify: `internalv2/infra/cluster/message_reader.go`
- Create: `internalv2/access/api/message_sync.go`
- Modify: `internalv2/access/api/routes.go`
- Modify: `internalv2/access/api/FLOW.md`
- Modify: `internalv2/app/wiring.go`
- Modify: `internalv2/app/FLOW.md`
- Modify: `internalv2/app/app_test.go`

- [ ] **Step 1: Write failing infra adapter tests**

Create `internalv2/infra/cluster/cmdsync_test.go` with:

```go
func TestCMDSyncStoreListsCMDKindOnly(t *testing.T) {
	node := &cmdSyncNodeFake{
		rows: []metadb.ConversationState{
			{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "normal", ChannelType: 2, ActiveAt: 300},
			{UID: "u1", Kind: metadb.ConversationKindCMD, ChannelID: "cmd____cmd", ChannelType: 2, ActiveAt: 200},
		},
	}
	store := NewCMDSyncStore(node)
	rows, err := store.ListConversationActiveView(context.Background(), "u1", 10)
	if err != nil {
		t.Fatalf("ListConversationActiveView(): %v", err)
	}
	if len(rows) != 1 || rows[0].Kind != metadb.ConversationKindCMD {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestCMDMessageReaderReadsCommittedCommandMessages(t *testing.T) {
	node := &cmdSyncNodeFake{
		readResult: channelstore.ReadCommittedResult{Messages: []channelv2.Message{{MessageSeq: 4, ChannelID: "g1____cmd", ChannelType: 2, Payload: []byte("x")}}},
	}
	store := NewCMDSyncStore(node)
	msgs, err := store.LoadCommandMessages(context.Background(), cmdsync.CommandChannelKey{ChannelID: "g1____cmd", ChannelType: 2}, 3, 10)
	if err != nil {
		t.Fatalf("LoadCommandMessages(): %v", err)
	}
	if len(msgs) != 1 || msgs[0].MessageSeq != 4 || node.lastReadReq.FromSeq != 3 {
		t.Fatalf("msgs=%+v req=%+v", msgs, node.lastReadReq)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
go test ./internalv2/infra/cluster -run 'TestCMDSyncStore|TestCMDMessageReader'
```

Expected: FAIL because `NewCMDSyncStore` does not exist.

- [ ] **Step 3: Implement CMD sync adapter**

Create `internalv2/infra/cluster/cmdsync.go` with:

```go
type CMDSyncNode interface {
	ListConversationActivePage(context.Context, metadb.ConversationKind, string, metadb.ConversationActiveCursor, int) ([]metadb.ConversationState, metadb.ConversationActiveCursor, bool, error)
	UpsertConversationStatesBatch(context.Context, []metadb.ConversationState) error
	ReadChannelCommitted(context.Context, channelv2.ChannelID, channelstore.ReadCommittedRequest) (channelstore.ReadCommittedResult, error)
}

type CMDSyncStore struct {
	node CMDSyncNode
}
```

`ListConversationActiveView` calls `ListConversationActivePage` with `metadb.ConversationKindCMD`. `UpsertConversationStates` forces `Kind: ConversationKindCMD` on cloned states before writing. `LoadCommandMessages` uses `ReadChannelCommitted` with forward read, `FromSeq`, `Limit`, and `MaxBytes: maxInt()`.

- [ ] **Step 4: Write failing API route tests**

In `internalv2/access/api`, add handler tests equivalent to legacy validation:

```go
func TestMessageSyncRejectsMissingUID(t *testing.T) {
	server := newTestServerWithCMDSync(t, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/message/sync", strings.NewReader(`{"uid":""}`))
	req.Header.Set("Content-Type", "application/json")
	server.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "uid不能为空") {
		t.Fatalf("response code=%d body=%s", rec.Code, rec.Body.String())
	}
}
```

Add success tests for `/message/sync` returning a bare array and `/message/syncack` returning `{"status":200}`.

- [ ] **Step 5: Implement API handlers and routes**

Create `internalv2/access/api/message_sync.go` using v2 `cmdsync`. Register:

```go
r.POST("/message/sync", s.handleMessageSync)
r.POST("/message/syncack", s.handleMessageSyncAck)
```

Keep legacy validation messages:

- invalid JSON: `数据格式有误！`
- empty uid: `uid不能为空！`
- negative limit: `limit不能为负数！`
- zero `last_message_seq`: `last_message_seq不能为空！`

- [ ] **Step 6: Wire app dependencies**

In `internalv2/app/wiring.go`, construct:

```go
cmdSyncStore := clusterinfra.NewCMDSyncStore(a.clusterNode)
cmdSyncApp := cmdsync.New(cmdsync.Options{
	States:   cmdSyncStore,
	Messages: cmdSyncStore,
	Now:      a.now,
})
```

Pass `cmdSyncApp` to the API server options. Do not create a separate CMD pending updater.

- [ ] **Step 7: Update FLOW docs**

Update `internalv2/access/api/FLOW.md` with `/message/sync` and `/message/syncack`. Update `internalv2/app/FLOW.md` with CMD sync wiring over the unified conversation projection.

- [ ] **Step 8: Run adapter, API, and app tests**

Run:

```bash
go test ./internalv2/infra/cluster ./internalv2/access/api ./internalv2/app
```

Expected: PASS.

- [ ] **Step 9: Commit CMD sync wiring**

```bash
git add internalv2/infra/cluster internalv2/access/api internalv2/app
git commit -m "feat: wire cmd sync over unified conversations"
```

---

### Task 11: Update Observability and Manager Views for Kind-Aware Active Rows

**Files:**

- Modify: `internalv2/app/observability.go`
- Modify: `internalv2/app/manager_monitor_prometheus.go`
- Modify: `internalv2/app/observability_test.go`
- Modify: `internalv2/app/manager_monitor_prometheus_test.go`

- [ ] **Step 1: Write failing metrics tests for kind-aware fields**

Update observability tests so `conversationactive.CacheObservation` can report total rows and optionally rows by kind:

```go
func TestConversationActiveObserverRecordsKindAwareTotals(t *testing.T) {
	observer := newTestConversationAuthorityMetricsObserver()
	observer.ObserveConversationActiveCache(conversationactive.CacheObservation{
		Rows:      3,
		DirtyRows: 2,
		RowsByKind: map[metadb.ConversationKind]int{
			metadb.ConversationKindNormal: 1,
			metadb.ConversationKindCMD:    2,
		},
	})
	got := observer.snapshot()
	if got.ConversationActiveRows != 3 || got.ConversationActiveCMDRows != 2 {
		t.Fatalf("snapshot = %+v", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
go test ./internalv2/app -run 'TestConversationActiveObserverRecordsKindAwareTotals|TestManagerMonitor'
```

Expected: FAIL until observations include kind-aware counts.

- [ ] **Step 3: Extend observations conservatively**

In `conversationactive.CacheObservation`, add:

```go
// RowsByKind counts cached rows by conversation kind.
RowsByKind map[metadb.ConversationKind]int
// DirtyRowsByKind counts dirty cached rows by conversation kind.
DirtyRowsByKind map[metadb.ConversationKind]int
```

Populate these maps inside `cacheObservation`. Keep existing aggregate fields for manager compatibility.

- [ ] **Step 4: Add low-cardinality metrics**

Expose separate normal/CMD counters or gauge keys in manager monitor responses. Use explicit keys such as:

- `conversationActiveNormalRows`
- `conversationActiveCMDRows`
- `conversationActiveNormalDirtyRows`
- `conversationActiveCMDDirtyRows`

Avoid high-cardinality channel or UID labels.

- [ ] **Step 5: Run app observability tests**

Run:

```bash
go test ./internalv2/app -run 'Observability|ManagerMonitor'
```

Expected: PASS.

- [ ] **Step 6: Commit observability update**

```bash
git add internalv2/app internalv2/runtime/conversationactive
git commit -m "feat: expose kind-aware conversation active metrics"
```

---

### Task 12: Add E2E Coverage for Isolation and CMD Sync

**Files:**

- Create or modify: `test/e2ev2/message/cmd_sync_test.go`
- Modify: `test/e2ev2/suite/*` only if helper support is missing

- [ ] **Step 1: Write black-box e2ev2 test**

Create `test/e2ev2/message/cmd_sync_test.go`:

```go
func TestUnifiedConversationCMDIsolation(t *testing.T) {
	s := suite.StartSingleNodeCluster(t)
	defer s.Stop(t)

	alice := s.MustConnect(t, "alice")
	bob := s.MustConnect(t, "bob")
	defer alice.Close()
	defer bob.Close()

	alice.MustSendText(t, "room1", 2, "normal")
	s.EventuallyConversationContains(t, "alice", "room1")

	alice.MustSendSyncOnceText(t, "room1", 2, "cmd")
	cmdMessages := s.MustMessageSync(t, "alice", 10)
	if len(cmdMessages) == 0 {
		t.Fatalf("message sync returned no CMD messages")
	}
	for _, msg := range cmdMessages {
		if strings.Contains(msg.ChannelID, "____cmd") {
			t.Fatalf("client-facing CMD channel leaked suffix: %+v", msg)
		}
	}

	conversations := s.MustConversationSync(t, "alice")
	for _, conv := range conversations {
		if strings.Contains(conv.ChannelID, "____cmd") {
			t.Fatalf("ordinary conversation leaked CMD row: %+v", conv)
		}
	}

	s.MustMessageSyncAck(t, "alice", cmdMessages[len(cmdMessages)-1].MessageSeq)
	afterAck := s.MustMessageSync(t, "alice", 10)
	if len(afterAck) != 0 {
		t.Fatalf("message sync after ack = %+v, want empty", afterAck)
	}
}
```

If the suite lacks `MustSendSyncOnceText`, `MustMessageSync`, or `MustMessageSyncAck`, add helpers in `test/e2ev2/suite` that call the existing HTTP API endpoints and WK protocol send path.

- [ ] **Step 2: Run targeted e2ev2 test**

Run:

```bash
go test ./test/e2ev2/message -run TestUnifiedConversationCMDIsolation
```

Expected: PASS.

- [ ] **Step 3: Run broader internalv2 and storage tests**

Run:

```bash
go test ./pkg/db/meta ./pkg/slot/fsm ./pkg/clusterv2 ./internalv2/runtime/conversationactive ./internalv2/usecase/conversation ./internalv2/usecase/cmdsync ./internalv2/infra/cluster ./internalv2/access/api ./internalv2/app ./internalv2/runtime/channelappend
```

Expected: PASS.

- [ ] **Step 4: Commit e2e coverage**

```bash
git add test/e2ev2/message test/e2ev2/suite
git commit -m "test: cover unified conversation cmd sync isolation"
```

---

### Task 13: Final Cleanup and Documentation Pass

**Files:**

- Modify: `internalv2/FLOW.md`
- Modify: `internalv2/app/FLOW.md`
- Modify: `internalv2/access/api/FLOW.md`
- Modify: `internalv2/runtime/channelappend/FLOW.md`
- Modify: `pkg/db/meta/FLOW.md`
- Modify: `docs/development/PROJECT_KNOWLEDGE.md` only if a concise durable rule is missing
- Remove stale references discovered by `rg`

- [ ] **Step 1: Search for stale split-surface names**

Run:

```bash
rg -n "UserConversation|CMDConversation|cmd_conversation|ListUserConversation|TouchUserConversation|HideUserConversation|UpsertUserConversation" pkg internalv2 test docs
```

Expected: only intentional legacy `internal/` references remain, plus historical wording in the committed design if still useful. No `internalv2` or `pkg/clusterv2` final API should use those names.

- [ ] **Step 2: Update docs**

Update the FLOW files listed above so they all describe:

- one canonical conversation projection table,
- `ConversationKindNormal` for ordinary conversation list,
- `ConversationKindCMD` for CMD sync,
- UID hash slot ownership for both kinds,
- no suffix filtering in ordinary conversation storage/listing.

- [ ] **Step 3: Run final unit suite**

Run:

```bash
go test ./internal/... ./internalv2/... ./pkg/...
```

Expected: PASS.

- [ ] **Step 4: Check git diff**

Run:

```bash
git status --short
git diff --stat
```

Expected: only intended files changed since the last task commit.

- [ ] **Step 5: Commit cleanup**

```bash
git add internalv2 pkg docs test
git commit -m "docs: document unified conversation projection flow"
```

---

## Implementation Notes

- Do not add long-lived wrappers that preserve `cmd_conversation` or `UserConversation` as public v2 surfaces.
- Do not infer CMD kind from `____cmd` inside `conversationactive` or `pkg/db/meta`.
- Do not bypass cluster routing for single-node cluster; all writes still route by UID hash slot.
- Keep `hashSlotCount` validation default-compatible with 256 slots.
- Keep comments for exported types and important struct fields in English.
- If a task touches a package that has `FLOW.md`, read it before editing and update it when behavior changes.
- Existing unrelated dirty files in the worktree must not be reverted or staged.

## Verification Summary

The final branch is complete when these commands pass:

```bash
go test ./pkg/db/meta
go test ./pkg/slot/fsm
go test ./pkg/clusterv2
go test ./internalv2/runtime/conversationactive
go test ./internalv2/usecase/conversation ./internalv2/usecase/cmdsync
go test ./internalv2/infra/cluster ./internalv2/access/api ./internalv2/app ./internalv2/runtime/channelappend
go test ./test/e2ev2/message -run TestUnifiedConversationCMDIsolation
go test ./internal/... ./internalv2/... ./pkg/...
```
