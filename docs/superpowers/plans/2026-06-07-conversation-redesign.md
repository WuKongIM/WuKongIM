# Conversation Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rebuild internalv2 recent conversations around the UID-owned `conversation` table, sparse active ordering, and message-log last-message reads without using `channel_latest` in the active path.

**Architecture:** The read path scans UID-owned `conversation` rows by `active_at`, then loads last visible messages through channel-owned message-log routes. The write path uses an asynchronous conversation projector: personal messages write two rows, small groups fan out below a threshold, and large groups use `SparseActive` without member-wide updates. Storage changes land bottom-up: message DB tail reads, meta conversation state, Slot FSM/clusterv2 facades, then internalv2 usecase/API/projector wiring.

**Tech Stack:** Go, `pkg/db/message`, `pkg/db/meta`, `pkg/slot/fsm`, `pkg/clusterv2`, `internalv2/usecase`, `internalv2/infra`, `internalv2/access/api`, `go test`.

---

## Scope Check

This spec spans several dependent subsystems, but they form one vertical feature rather than independent products. Implement in the order below because later tasks depend on the storage and routing surfaces from earlier tasks.

Do not start by editing `/conversation/list`. First make message tail reads and UID-owned conversation paging real. The current workspace may contain `channel_latest` experimental code; treat it as old-path code to be stopped in Task 8, not as the final design.

## File Structure

### Message DB And ChannelV2 Durable Fields

- Modify: `pkg/db/internal/engine/iter.go`
  - Add copied-key reverse iterator helpers.
- Modify: `pkg/db/internal/engine/engine_test.go`
  - Verify bounded reverse iteration.
- Modify: `pkg/db/message/types.go`
  - Add `ServerTimestampMS` to `Record` and `Message`.
- Modify: `pkg/db/message/append.go`
  - Preserve `ServerTimestampMS` in `recordToRow`.
- Modify: `pkg/db/message/read.go`
  - Add `GetLastVisibleMessage`.
- Create: `pkg/db/message/last_visible_test.go`
  - Tail-read, visibility, and durable-field tests.
- Modify: `pkg/channelv2/reactor/append.go`
  - Preserve sender/idempotency/timestamp fields when converting append messages to records.
- Modify: `pkg/channelv2/store/channel_adapter.go`
  - Preserve fields through message DB compatibility encoding.
- Modify: `internalv2/contracts/messageevents/event.go`
  - Add `ServerTimestampMS`.
- Modify: `internalv2/usecase/message/send.go`
  - Emit committed events with the canonical server timestamp.
- Modify: `internalv2/usecase/message/send_test.go`
  - Verify committed events carry conversation fields.

### Meta Conversation State

- Modify: `pkg/db/meta/table_conversation.go`
  - Add `SparseActive`, `UserConversationActiveCursor`, active page scan, touch fence, and value backward compatibility.
- Modify: `pkg/db/meta/user_conversation_state_test.go`
  - Add sparse, cursor, join floor, and delete fence tests.
- Modify: `pkg/db/meta/schema_test.go`
  - Assert schema exposes `sparse_active`.
- Modify: `pkg/db/meta/inspect.go`
  - Include `sparse_active` in inspect rows.
- Modify: `pkg/db/meta/inspect_test.go`
  - Assert inspect output includes sparse state.
- Modify: `pkg/db/meta/compat.go`
  - Expose active page and batch touch/upsert semantics through compatibility APIs.
- Modify: `pkg/db/meta/FLOW.md`
  - Document sparse active conversation rows and active cursor scans.

### Slot FSM And Clusterv2

- Modify: `pkg/slot/fsm/command.go`
  - Extend user conversation TLV with `SparseActive`; add checked batch APIs if missing.
- Modify: `pkg/slot/fsm/statemachine.go`
  - Apply UID-owned conversation batches with hash-slot ownership checks.
- Modify: `pkg/slot/fsm/state_machine_test.go`
  - Add SparseActive and delete-fence apply tests.
- Modify: `pkg/slot/fsm/command_inspection.go`
  - Include `sparse_active` in command inspection.
- Modify: `pkg/slot/fsm/command_inspection_test.go`
  - Assert inspection includes sparse state.
- Modify: `pkg/clusterv2/node_meta.go`
  - Add UID-routed `UpsertUserConversationStatesBatch` and `ListUserConversationActivePage`.
- Create: `pkg/clusterv2/node_meta_conversation_test.go`
  - Verify routing, grouping, and page reads.
- Modify: `pkg/clusterv2/FLOW.md`
  - Document UID-owned conversation routing.

### Internalv2 Conversation Usecase And Infra

- Modify: `internalv2/usecase/conversation/types.go`
  - Replace membership/latest DTOs with active cursor, conversation row, last message, and unread fields.
- Modify: `internalv2/usecase/conversation/app.go`
  - List by active page and batch last-message load.
- Modify: `internalv2/usecase/conversation/app_test.go`
  - Verify sorting, cursor, SparseActive ordering, unread, missing last message, and person channel mapping.
- Modify: `internalv2/usecase/conversation/FLOW.md`
  - Replace membership-scan flow.
- Modify: `internalv2/infra/cluster/conversation.go`
  - Adapt clusterv2 active conversation page and channel-owned last-message batch.
- Modify: `internalv2/infra/cluster/FLOW.md`
  - Document cross-ownership reads.

### API, App, Projector, Metrics, And Cleanup

- Modify: `internalv2/access/api/conversation_list.go`
  - Emit the final `active_at` cursor response, `last_message`, `unread`, and `more`.
- Modify: `internalv2/access/api/conversation_list_test.go`
  - Update API tests for new JSON shape and error mapping.
- Modify: `internalv2/access/api/FLOW.md`
  - Remove membership scan wording.
- Modify: `internalv2/usecase/conversation/projector.go`
  - Implement dense/sparse projector policy.
- Create: `internalv2/usecase/conversation/projector_test.go`
  - Verify personal, small-group, and large-group fanout decisions.
- Modify: `internalv2/app/config.go`
  - Add `SmallGroupFanoutLimit` and last-message concurrency config with English comments.
- Modify: `internalv2/app/app.go`
  - Wire new conversation usecase, projector, and API.
- Modify: `internalv2/app/lifecycle.go`
  - Start/stop projector if it owns workers.
- Modify: `internalv2/app/conversation_api_smoke_test.go`
  - Update smoke tests to message-log last message.
- Modify: `internalv2/app/FLOW.md`
  - Document final wiring and channel_latest Phase 1 stop.
- Modify: `pkg/metrics/conversation.go`
  - Replace scan-window metrics with active-page and last-message metrics.
- Modify: `pkg/metrics/registry_test.go`
  - Verify new metric families.
- Modify: `wukongim.conf.example`
  - Add config keys introduced in `internalv2/app/config.go`.
- Modify: `AGENTS.md`
  - Update directory descriptions if conversation path descriptions changed.

---

### Task 1: Message Tail Reads And Durable Conversation Fields

**Files:**
- Modify: `pkg/db/internal/engine/iter.go`
- Modify: `pkg/db/internal/engine/engine_test.go`
- Modify: `pkg/db/message/types.go`
- Modify: `pkg/db/message/append.go`
- Modify: `pkg/db/message/read.go`
- Create: `pkg/db/message/last_visible_test.go`
- Modify: `pkg/channelv2/reactor/append.go`
- Modify: `pkg/channelv2/store/channel_adapter.go`
- Modify: `internalv2/contracts/messageevents/event.go`
- Modify: `internalv2/usecase/message/send.go`
- Modify: `internalv2/usecase/message/send_test.go`
- Modify: `pkg/db/message/FLOW.md`

- [ ] **Step 1: Read package flow docs**

Run:

```bash
test ! -f pkg/db/message/FLOW.md || sed -n '1,220p' pkg/db/message/FLOW.md
test ! -f internalv2/usecase/message/FLOW.md || sed -n '1,220p' internalv2/usecase/message/FLOW.md
```

Expected: the current message log append/read flow is visible before editing.

- [ ] **Step 2: Write reverse iterator tests**

Add tests in `pkg/db/internal/engine/engine_test.go`:

```go
func TestIterReverseWithinBounds(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	batch := db.NewBatch()
	for _, kv := range []struct {
		key   string
		value string
	}{
		{"k1", "v1"},
		{"k2", "v2"},
		{"k3", "v3"},
	} {
		if err := batch.Set([]byte(kv.key), []byte(kv.value)); err != nil {
			t.Fatalf("Set(%s): %v", kv.key, err)
		}
	}
	if err := batch.Commit(true); err != nil {
		t.Fatalf("Commit(): %v", err)
	}
	if err := batch.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}

	iter, err := db.NewIter(Span{Start: []byte("k1"), End: []byte("k4")}, IterOptions{})
	if err != nil {
		t.Fatalf("NewIter(): %v", err)
	}
	defer iter.Close()

	if !iter.Last() || string(iter.Key()) != "k3" {
		t.Fatalf("Last() key=%q, want k3", string(iter.Key()))
	}
	if !iter.Prev() || string(iter.Key()) != "k2" {
		t.Fatalf("Prev() key=%q, want k2", string(iter.Key()))
	}
	if !iter.SeekLT([]byte("k3")) || string(iter.Key()) != "k2" {
		t.Fatalf("SeekLT(k3) key=%q, want k2", string(iter.Key()))
	}
}
```

- [ ] **Step 3: Run reverse iterator test and verify failure**

Run:

```bash
go test ./pkg/db/internal/engine -run TestIterReverseWithinBounds -count=1
```

Expected: FAIL because `Iter.Last`, `Iter.Prev`, and `Iter.SeekLT` do not exist.

- [ ] **Step 4: Implement reverse iterator helpers**

Add to `pkg/db/internal/engine/iter.go`:

```go
// Last positions the iterator at the last key in bounds.
func (it *Iter) Last() bool {
	return it != nil && it.iter != nil && it.iter.Last()
}

// SeekLT positions the iterator at the last key strictly less than key.
func (it *Iter) SeekLT(key []byte) bool {
	return it != nil && it.iter != nil && it.iter.SeekLT(key)
}

// Prev moves the iterator to the previous key.
func (it *Iter) Prev() bool {
	return it != nil && it.iter != nil && it.iter.Prev()
}
```

- [ ] **Step 5: Verify engine test passes**

Run:

```bash
go test ./pkg/db/internal/engine -run TestIterReverseWithinBounds -count=1
```

Expected: PASS.

- [ ] **Step 6: Write message tail-read tests**

Create `pkg/db/message/last_visible_test.go`:

```go
package message

import (
	"context"
	"testing"
)

func TestChannelLogGetLastVisibleMessageReadsTail(t *testing.T) {
	db := openTestMessageDB(t)
	defer db.Close()
	log := db.Channel(ChannelKey("tail:2"), ChannelID{ID: "tail", Type: 2})
	ctx := context.Background()

	_, err := log.Append(ctx, []Record{
		{ID: 101, FromUID: "u1", ClientMsgNo: "c1", Payload: []byte("one"), ServerTimestampMS: 1000},
		{ID: 102, FromUID: "u2", ClientMsgNo: "c2", Payload: []byte("two"), ServerTimestampMS: 2000},
	}, AppendOptions{})
	if err != nil {
		t.Fatalf("Append(): %v", err)
	}

	msg, ok, err := log.GetLastVisibleMessage(ctx, 0)
	if err != nil || !ok {
		t.Fatalf("GetLastVisibleMessage() ok=%v err=%v, want ok", ok, err)
	}
	if msg.MessageID != 102 || msg.MessageSeq != 2 || msg.FromUID != "u2" || msg.ClientMsgNo != "c2" || msg.ServerTimestampMS != 2000 || string(msg.Payload) != "two" {
		t.Fatalf("last message = %+v, want seq 2 with durable fields", msg)
	}
}

func TestChannelLogGetLastVisibleMessageHonorsVisibleAfterSeq(t *testing.T) {
	db := openTestMessageDB(t)
	defer db.Close()
	log := db.Channel(ChannelKey("visible:2"), ChannelID{ID: "visible", Type: 2})
	ctx := context.Background()

	_, err := log.Append(ctx, []Record{
		{ID: 201, Payload: []byte("one"), ServerTimestampMS: 1000},
		{ID: 202, Payload: []byte("two"), ServerTimestampMS: 2000},
	}, AppendOptions{})
	if err != nil {
		t.Fatalf("Append(): %v", err)
	}

	msg, ok, err := log.GetLastVisibleMessage(ctx, 1)
	if err != nil || !ok {
		t.Fatalf("GetLastVisibleMessage(1) ok=%v err=%v, want ok", ok, err)
	}
	if msg.MessageSeq != 2 {
		t.Fatalf("visible last seq = %d, want 2", msg.MessageSeq)
	}
	_, ok, err = log.GetLastVisibleMessage(ctx, 2)
	if err != nil {
		t.Fatalf("GetLastVisibleMessage(2): %v", err)
	}
	if ok {
		t.Fatal("GetLastVisibleMessage(2) ok = true, want no visible message")
	}
}
```

- [ ] **Step 7: Run message tail tests and verify failure**

Run:

```bash
go test ./pkg/db/message -run 'TestChannelLogGetLastVisibleMessage' -count=1
```

Expected: FAIL because `Record.ServerTimestampMS`, `Message.ServerTimestampMS`, and `GetLastVisibleMessage` are missing.

- [ ] **Step 8: Preserve server timestamp in message rows**

Modify `pkg/db/message/types.go`:

```go
type Record struct {
	ID                uint64
	ClientMsgNo       string
	FromUID           string
	Payload           []byte
	SizeBytes         int
	ServerTimestampMS int64
}

type Message struct {
	MessageSeq        uint64
	MessageID         uint64
	ClientMsgNo       string
	FromUID           string
	PayloadHash       uint64
	Payload           []byte
	ServerTimestampMS int64
}
```

Modify `pkg/db/message/append.go` in `recordToRow`:

```go
Timestamp: record.ServerTimestampMS,
```

Modify `pkg/db/message/read.go` in `messageFromRow`:

```go
ServerTimestampMS: row.Timestamp,
```

- [ ] **Step 9: Implement `GetLastVisibleMessage`**

Add to `pkg/db/message/read.go`:

```go
// GetLastVisibleMessage returns the newest message whose sequence is greater than visibleAfterSeq.
func (l *ChannelLog) GetLastVisibleMessage(ctx context.Context, visibleAfterSeq uint64) (Message, bool, error) {
	if err := ctx.Err(); err != nil {
		return Message{}, false, err
	}
	if l == nil || l.db == nil || l.db.engine == nil {
		return Message{}, false, dberrors.ErrClosed
	}
	leo, err := l.LEO(ctx)
	if err != nil {
		return Message{}, false, err
	}
	if leo == 0 || leo <= visibleAfterSeq {
		return Message{}, false, nil
	}
	for seq := leo; seq > visibleAfterSeq; seq-- {
		msg, ok, err := l.GetBySeq(ctx, seq)
		if err != nil || ok {
			return msg, ok, err
		}
	}
	return Message{}, false, nil
}
```

Then replace the loop with a reverse-iterator implementation in the same task before committing:

```go
prefix := encodeMessageRowPrefix(l.key)
span := keycodec.NewPrefixSpan(prefix)
iter, err := l.db.engine.NewIter(engine.Span{Start: span.Start, End: span.End}, engine.IterOptions{})
```

Use `iter.Last()` / `iter.Prev()` to find the newest `messageHeaderFamilyID` row with `seq > visibleAfterSeq`, then call `GetBySeq(ctx, seq)` to materialize header and payload. The final implementation must not call `ReadReverse`.

- [ ] **Step 10: Preserve fields through channelv2 conversion**

Modify `pkg/channelv2/reactor/append.go` so `appendRecordsFromMessages` copies sender/idempotency/timestamp fields from `ch.Message` into `ch.Record`. If `ch.Message` lacks a server timestamp, add it in the same package with an English comment and set it at request admission time.

Modify `pkg/channelv2/store/channel_adapter.go` so `encodeRecordsForMessageDB` and `fromDBRecord` preserve:

```go
FromUID
ClientMsgNo
ServerTimestampMS
Payload
```

- [ ] **Step 11: Add committed event server timestamp**

Modify `internalv2/contracts/messageevents/event.go`:

```go
// ServerTimestampMS is the server append timestamp used for conversation ordering.
ServerTimestampMS int64
```

Modify `internalv2/usecase/message/send.go` to populate it from the same server timestamp used in channelv2/message append. If no timestamp exists in the command, derive it once before append and pass it through append and event emission.

- [ ] **Step 12: Verify message-related packages**

Run:

```bash
go test ./pkg/db/internal/engine ./pkg/db/message ./pkg/channelv2/... ./internalv2/contracts/messageevents ./internalv2/usecase/message -count=1
```

Expected: PASS.

- [ ] **Step 13: Update `pkg/db/message/FLOW.md`**

Document:

```text
ChannelLog.GetLastVisibleMessage uses reverse iteration over the channel row keyspace to fetch the newest visible message without scanning the full channel.
Message rows persist ServerTimestampMS, FromUID, ClientMsgNo, and Payload for conversation list display.
```

- [ ] **Step 14: Commit Task 1**

Run:

```bash
git add pkg/db/internal/engine pkg/db/message pkg/channelv2 internalv2/contracts/messageevents internalv2/usecase/message
git commit -m "feat: add message tail reads for conversations"
```

---

### Task 2: Conversation Meta Sparse Active State

**Files:**
- Modify: `pkg/db/meta/table_conversation.go`
- Modify: `pkg/db/meta/user_conversation_state_test.go`
- Modify: `pkg/db/meta/schema_test.go`
- Modify: `pkg/db/meta/inspect.go`
- Modify: `pkg/db/meta/inspect_test.go`
- Modify: `pkg/db/meta/compat.go`
- Modify: `pkg/db/meta/FLOW.md`

- [ ] **Step 1: Write sparse decode and active page tests**

Add tests to `pkg/db/meta/user_conversation_state_test.go`:

```go
func TestUserConversationStateSparseActiveRoundTrip(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	shard := store.db.HashSlot(3)

	state := UserConversationState{UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAt: 100, UpdatedAt: 101, SparseActive: true}
	if err := shard.UpsertUserConversationState(ctx, state); err != nil {
		t.Fatalf("UpsertUserConversationState(): %v", err)
	}
	got, ok, err := shard.GetUserConversationState(ctx, "u1", "g1", 2)
	if err != nil || !ok {
		t.Fatalf("GetUserConversationState() ok=%v err=%v, want ok", ok, err)
	}
	if !got.SparseActive {
		t.Fatalf("SparseActive = false, want true: %+v", got)
	}
}

func TestUserConversationActivePageUsesCursor(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	shard := store.db.HashSlot(3)
	for _, state := range []UserConversationState{
		{UID: "u1", ChannelID: "g-a", ChannelType: 2, ActiveAt: 300},
		{UID: "u1", ChannelID: "g-b", ChannelType: 2, ActiveAt: 200},
		{UID: "u1", ChannelID: "g-c", ChannelType: 2, ActiveAt: 100},
	} {
		if err := shard.UpsertUserConversationState(ctx, state); err != nil {
			t.Fatalf("UpsertUserConversationState(%+v): %v", state, err)
		}
	}
	page, cursor, done, err := shard.ListUserConversationActivePage(ctx, "u1", UserConversationActiveCursor{}, 2)
	if err != nil {
		t.Fatalf("ListUserConversationActivePage(): %v", err)
	}
	if done || len(page) != 2 || page[0].ChannelID != "g-a" || page[1].ChannelID != "g-b" {
		t.Fatalf("page=%+v cursor=%+v done=%v, want first two newest", page, cursor, done)
	}
	page, _, done, err = shard.ListUserConversationActivePage(ctx, "u1", cursor, 2)
	if err != nil {
		t.Fatalf("ListUserConversationActivePage(next): %v", err)
	}
	if !done || len(page) != 1 || page[0].ChannelID != "g-c" {
		t.Fatalf("next page=%+v done=%v, want final g-c", page, done)
	}
}

func TestUserConversationTouchIgnoredBelowDeletedToSeq(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	shard := store.db.HashSlot(3)
	base := UserConversationState{UID: "u1", ChannelID: "g1", ChannelType: 2, DeletedToSeq: 10, ActiveAt: 0}
	if err := shard.UpsertUserConversationState(ctx, base); err != nil {
		t.Fatalf("UpsertUserConversationState(): %v", err)
	}
	err := shard.TouchUserConversationActiveAt(ctx, UserConversationActivePatch{
		UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAt: 500, MessageSeq: 10,
	})
	if err != nil {
		t.Fatalf("TouchUserConversationActiveAt(): %v", err)
	}
	got, _, err := shard.GetUserConversationState(ctx, "u1", "g1", 2)
	if err != nil {
		t.Fatalf("GetUserConversationState(): %v", err)
	}
	if got.ActiveAt != 0 {
		t.Fatalf("ActiveAt = %d, want unchanged 0", got.ActiveAt)
	}
}
```

- [ ] **Step 2: Run meta tests and verify failure**

Run:

```bash
go test ./pkg/db/meta -run 'TestUserConversationStateSparseActiveRoundTrip|TestUserConversationActivePageUsesCursor|TestUserConversationTouchIgnoredBelowDeletedToSeq' -count=1
```

Expected: FAIL because `SparseActive` and active page API are missing or incomplete.

- [ ] **Step 3: Add `SparseActive` and cursor types**

Modify `pkg/db/meta/table_conversation.go`:

```go
type UserConversationState struct {
	UID          string
	ChannelID    string
	ChannelType  int64
	ReadSeq      uint64
	DeletedToSeq uint64
	ActiveAt     int64
	UpdatedAt    int64
	// SparseActive reports that ActiveAt is a low-frequency ordering anchor.
	SparseActive bool
}

type UserConversationActiveCursor struct {
	// ActiveAt is the active timestamp from the last emitted row.
	ActiveAt int64
	// ChannelID is the channel ID from the last emitted row.
	ChannelID string
	// ChannelType is the channel type from the last emitted row.
	ChannelType int64
}
```

- [ ] **Step 4: Extend value codec backward-compatibly**

Modify `encodeConversationValue` and `decodeConversationValue` so the encoded value appends one byte for `SparseActive` and old values decode with `false`. Use `0` and `1` bytes; reject any other sparse value as corrupt.

Expected behavior:

```text
old value length for read_seq/deleted_to_seq/active_at/updated_at -> SparseActive=false
new value with trailing 0 -> SparseActive=false
new value with trailing 1 -> SparseActive=true
```

- [ ] **Step 5: Implement active page scan**

Add `ListUserConversationActivePage` in `pkg/db/meta/table_conversation.go`. It should scan `conversationActiveIndexID` with prefix `uid`, after:

```text
(uid, active_at desc, channel_id, channel_type)
```

Return `limit` rows, a cursor from the last emitted row, and `done`.

- [ ] **Step 6: Enforce deleted-to-seq fence**

Ensure `TouchUserConversationActiveAt` and `WriteBatch.TouchUserConversationActiveAt` ignore patches where:

```go
patch.MessageSeq > 0 && patch.MessageSeq <= current.DeletedToSeq
```

This is already partially present in direct shard code; verify and align batch code.

- [ ] **Step 7: Expose compat and inspect**

Add to `pkg/db/meta/compat.go`:

```go
func (s *ShardStore) ListUserConversationActivePage(ctx context.Context, uid string, cursor UserConversationActiveCursor, limit int) ([]UserConversationState, UserConversationActiveCursor, bool, error)
```

Update `inspectConversationRow` to include:

```go
"sparse_active": state.SparseActive,
```

- [ ] **Step 8: Verify meta package**

Run:

```bash
go test ./pkg/db/meta -count=1
```

Expected: PASS.

- [ ] **Step 9: Update `pkg/db/meta/FLOW.md`**

Document:

```text
User conversation rows are UID-owned active-index rows. SparseActive marks rows whose ActiveAt is a low-frequency ordering anchor. Active list pages use (uid, active_at desc, channel_id, channel_type) cursors.
```

- [ ] **Step 10: Commit Task 2**

Run:

```bash
git add pkg/db/meta
git commit -m "feat: add sparse active conversation state"
```

---

### Task 3: Slot FSM And Clusterv2 Conversation Facades

**Files:**
- Modify: `pkg/slot/fsm/command.go`
- Modify: `pkg/slot/fsm/statemachine.go`
- Modify: `pkg/slot/fsm/state_machine_test.go`
- Modify: `pkg/slot/fsm/command_inspection.go`
- Modify: `pkg/slot/fsm/command_inspection_test.go`
- Modify: `pkg/clusterv2/node_meta.go`
- Create: `pkg/clusterv2/node_meta_conversation_test.go`
- Modify: `pkg/slot/FLOW.md`
- Modify: `pkg/clusterv2/FLOW.md`

- [ ] **Step 1: Write FSM sparse command tests**

Add tests in `pkg/slot/fsm/state_machine_test.go`:

```go
func TestStateMachineAppliesSparseUserConversationState(t *testing.T) {
	db := newTestMetaDB(t)
	sm := newTestStateMachine(t, db, 11)
	ctx := context.Background()
	cmd := Command{
		Type: CommandTypeMeta,
		Data: EncodeUpsertUserConversationStatesCommand([]metadb.UserConversationState{{
			UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAt: 100, SparseActive: true,
		}}),
	}
	if err := sm.Apply(ctx, cmd); err != nil {
		t.Fatalf("Apply(): %v", err)
	}
	got, err := db.ForHashSlot(11).GetUserConversationState(ctx, "u1", "g1", 2)
	if err != nil {
		t.Fatalf("GetUserConversationState(): %v", err)
	}
	if !got.SparseActive {
		t.Fatalf("SparseActive = false, want true: %+v", got)
	}
}
```

- [ ] **Step 2: Run FSM test and verify failure**

Run:

```bash
go test ./pkg/slot/fsm -run TestStateMachineAppliesSparseUserConversationState -count=1
```

Expected: FAIL if SparseActive is not encoded/decoded.

- [ ] **Step 3: Extend user conversation TLV**

In `pkg/slot/fsm/command.go`, add a new tag:

```go
tagUserConversationStateEntrySparseActive uint8 = 8
```

Update `encodeUserConversationStateEntry` to encode `SparseActive` as `0` or `1`. Update `decodeUserConversationStateEntry` to accept missing field as `false`.

- [ ] **Step 4: Add clusterv2 facade tests**

Create `pkg/clusterv2/node_meta_conversation_test.go` with tests for:

```text
TestClusterV2UserConversationBatchFacadeRoutesByUID
TestClusterV2ListUserConversationActivePageUsesUIDHashSlot
```

The batch test should write two users whose UIDs route to different hash slots and verify each row lands under the UID hash slot, not channel ID.

- [ ] **Step 5: Implement clusterv2 facades**

Add to `pkg/clusterv2/node_meta.go`:

```go
func (n *Node) UpsertUserConversationStatesBatch(ctx context.Context, states []metadb.UserConversationState) error

func (n *Node) ListUserConversationActivePage(ctx context.Context, uid string, cursor metadb.UserConversationActiveCursor, limit int) ([]metadb.UserConversationState, metadb.UserConversationActiveCursor, bool, error)
```

`UpsertUserConversationStatesBatch` groups by `RouteKey(state.UID)`, builds Slot FSM commands, and caps batch size at a named constant. `ListUserConversationActivePage` routes by `uid` and reads from `defaultSlotMetaDB.ForHashSlot(route.HashSlot)`.

- [ ] **Step 6: Verify slot and clusterv2**

Run:

```bash
go test ./pkg/slot/fsm ./pkg/clusterv2 -count=1
```

Expected: PASS.

- [ ] **Step 7: Update FLOW docs**

Update `pkg/slot/FLOW.md` and `pkg/clusterv2/FLOW.md` to include UID-owned conversation state routing and SparseActive.

- [ ] **Step 8: Commit Task 3**

Run:

```bash
git add pkg/slot pkg/clusterv2
git commit -m "feat: route user conversations through clusterv2"
```

---

### Task 4: Internalv2 Conversation List Usecase

**Files:**
- Modify: `internalv2/usecase/conversation/types.go`
- Modify: `internalv2/usecase/conversation/app.go`
- Modify: `internalv2/usecase/conversation/app_test.go`
- Modify: `internalv2/usecase/conversation/FLOW.md`
- Modify: `internalv2/infra/cluster/conversation.go`
- Modify: `internalv2/infra/cluster/FLOW.md`

- [ ] **Step 1: Replace old membership/latest tests**

Update `internalv2/usecase/conversation/app_test.go` with tests:

```go
func TestListUsesActivePageAndLoadsLastMessages(t *testing.T)
func TestListKeepsSparseOrderFromActiveAt(t *testing.T)
func TestListCalculatesUnreadFromReadAndDeletedFloor(t *testing.T)
func TestListReturnsConversationWhenLastMessageMissing(t *testing.T)
```

In the sparse ordering test, use two rows where the sparse row has a newer last message but older `ActiveAt`; expected order remains by `ActiveAt`.

- [ ] **Step 2: Run usecase tests and verify failure**

Run:

```bash
go test ./internalv2/usecase/conversation -count=1
```

Expected: FAIL because the current usecase scans membership and reads `channel_latest`.

- [ ] **Step 3: Define usecase DTOs and ports**

Modify `internalv2/usecase/conversation/types.go`:

```go
type Cursor struct {
	ActiveAt    int64
	ChannelID   string
	ChannelType int64
}

type LastMessage struct {
	MessageID         uint64
	MessageSeq        uint64
	FromUID           string
	ClientMsgNo        string
	ServerTimestampMS int64
	Payload           []byte
}

type Conversation struct {
	ChannelID     string
	ChannelType   int64
	ActiveAt      int64
	SparseActive  bool
	ReadSeq       uint64
	DeletedToSeq  uint64
	Unread        uint64
	LastMessage   *LastMessage
}
```

In `app.go`, define ports:

```go
type ConversationStore interface {
	ListUserConversationActivePage(ctx context.Context, uid string, cursor metadb.UserConversationActiveCursor, limit int) ([]metadb.UserConversationState, metadb.UserConversationActiveCursor, bool, error)
}

type MessageStore interface {
	GetLastVisibleMessageBatch(ctx context.Context, requests []LastVisibleMessageRequest) (map[metadb.ConversationKey]LastMessage, error)
}
```

- [ ] **Step 4: Implement list logic**

`List` should:

1. Validate `uid`, `limit`, and cursor.
2. Read `limit+1` active rows.
3. Trim to `limit` and set `HasMore`.
4. Build last-message requests for the returned rows with `visibleAfterSeq=DeletedToSeq`.
5. Calculate `Unread` as `max(0, last.MessageSeq - max(ReadSeq, DeletedToSeq))`.
6. Return missing last messages as `LastMessage=nil`.

- [ ] **Step 5: Implement infra adapter**

Modify `internalv2/infra/cluster/conversation.go` so:

```go
ListUserConversationActivePage -> clusterv2.Node.ListUserConversationActivePage
GetLastVisibleMessageBatch -> channel-owned committed log reader
```

If the channel-owned last-message route is not available yet, implement the adapter behind a small interface owned by `internalv2/infra/cluster` and make app wiring provide the real implementation in Task 7.

- [ ] **Step 6: Verify usecase and infra**

Run:

```bash
go test ./internalv2/usecase/conversation ./internalv2/infra/cluster -count=1
```

Expected: PASS.

- [ ] **Step 7: Update FLOW docs**

Replace membership/latest flow in:

```text
internalv2/usecase/conversation/FLOW.md
internalv2/infra/cluster/FLOW.md
```

with active-page plus message-log tail-read flow.

- [ ] **Step 8: Commit Task 4**

Run:

```bash
git add internalv2/usecase/conversation internalv2/infra/cluster
git commit -m "feat: list conversations from active state"
```

---

### Task 5: HTTP API And Conversation Metrics

**Files:**
- Modify: `internalv2/access/api/conversation_list.go`
- Modify: `internalv2/access/api/conversation_list_test.go`
- Modify: `internalv2/access/api/server.go`
- Modify: `internalv2/access/api/FLOW.md`
- Modify: `pkg/metrics/conversation.go`
- Modify: `pkg/metrics/registry_test.go`

- [ ] **Step 1: Update API tests for final JSON shape**

In `internalv2/access/api/conversation_list_test.go`, assert:

```json
{
  "conversations": [
    {
      "channel_id": "g1",
      "channel_type": 2,
      "active_at": 1000,
      "sparse_active": true,
      "read_seq": 3,
      "deleted_to_seq": 2,
      "unread": 4,
      "last_message": {
        "message_id": 9,
        "message_idstr": "9",
        "message_seq": 7,
        "from_uid": "u2",
        "server_timestamp_ms": 1001,
        "client_msg_no": "c1",
        "payload": "aGVsbG8="
      }
    }
  ],
  "next_cursor": {
    "active_at": 1000,
    "channel_id": "g1",
    "channel_type": 2
  },
  "more": 1
}
```

Also assert `truncated` and `scanned_memberships` are not present.

- [ ] **Step 2: Run API test and verify failure**

Run:

```bash
go test ./internalv2/access/api -run TestConversationList -count=1
```

Expected: FAIL while handler still returns old fields or old cursor shape.

- [ ] **Step 3: Update handler DTO mapping**

Modify `internalv2/access/api/conversation_list.go`:

```go
type conversationListCursor struct {
	ActiveAt    int64  `json:"active_at"`
	ChannelID   string `json:"channel_id"`
	ChannelType int64  `json:"channel_type"`
}

type conversationListItem struct {
	ChannelID    string                   `json:"channel_id"`
	ChannelType  int64                    `json:"channel_type"`
	ActiveAt     int64                    `json:"active_at"`
	SparseActive bool                     `json:"sparse_active"`
	ReadSeq      uint64                   `json:"read_seq"`
	DeletedToSeq uint64                   `json:"deleted_to_seq"`
	Unread       uint64                   `json:"unread"`
	LastMessage  *conversationLastMessage `json:"last_message"`
}
```

Keep `more` as `0/1`.

- [ ] **Step 4: Update observations and metrics**

Replace scan-window metrics with:

```text
returned_items
sparse_items
last_message_loads
last_message_errors
active_index_stale_skips
latency
```

Do not add `uid` or `channel_id` labels.

- [ ] **Step 5: Verify API and metrics**

Run:

```bash
go test ./internalv2/access/api ./pkg/metrics -count=1
```

Expected: PASS.

- [ ] **Step 6: Update API FLOW**

Document final JSON fields, active cursor, and removal of membership-scan response fields.

- [ ] **Step 7: Commit Task 5**

Run:

```bash
git add internalv2/access/api pkg/metrics
git commit -m "feat: expose active conversation list api"
```

---

### Task 6: Conversation Projector Dense And Sparse Fanout

**Files:**
- Create: `internalv2/usecase/conversation/projector.go`
- Create: `internalv2/usecase/conversation/projector_test.go`
- Modify: `internalv2/usecase/conversation/types.go`
- Modify: `internalv2/usecase/conversation/FLOW.md`

- [ ] **Step 1: Write projector policy tests**

Create `internalv2/usecase/conversation/projector_test.go` with tests:

```go
func TestProjectorPersonalMessageTouchesSenderAndPeer(t *testing.T)
func TestProjectorSmallGroupFansOutMembers(t *testing.T)
func TestProjectorLargeGroupTouchesOnlySender(t *testing.T)
func TestProjectorInitializesJoinFloor(t *testing.T)
```

Use fake member source and fake batch store. Assert:

```text
personal -> two dense rows
small group -> all members dense rows
large group -> sender sparse row only
join_seq=11 -> read_seq=10 and deleted_to_seq=10 on created row
```

- [ ] **Step 2: Run projector tests and verify failure**

Run:

```bash
go test ./internalv2/usecase/conversation -run TestProjector -count=1
```

Expected: FAIL because projector does not exist or still writes `channel_latest`.

- [ ] **Step 3: Define projector ports**

Add in `internalv2/usecase/conversation/types.go`:

```go
type MemberSource interface {
	ClassifyMembers(ctx context.Context, channelID string, channelType int64, limit int) (MemberClass, error)
}

type MemberClass struct {
	IsSmall bool
	Members []Member
}

type Member struct {
	UID     string
	JoinSeq uint64
}

type ConversationBatchStore interface {
	UpsertUserConversationStatesBatch(ctx context.Context, states []metadb.UserConversationState) error
}
```

- [ ] **Step 4: Implement projector policy**

`Projector.HandleCommitted` should:

1. Derive personal peer for person channels and write sender/peer dense rows.
2. For group channels, call `MemberSource.ClassifyMembers` with `small_group_fanout_limit + 1`.
3. For small groups, fan out dense rows to all returned members.
4. For large groups, write only sender sparse row when sender is non-empty.
5. Initialize new row floors from member `JoinSeq`.
6. Use `event.ServerTimestampMS` as `ActiveAt`.

- [ ] **Step 5: Verify projector tests**

Run:

```bash
go test ./internalv2/usecase/conversation -run TestProjector -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit Task 6**

Run:

```bash
git add internalv2/usecase/conversation
git commit -m "feat: add sparse conversation projector"
```

---

### Task 7: App Wiring, Config, And Smoke Tests

**Files:**
- Modify: `internalv2/app/config.go`
- Modify: `internalv2/app/app.go`
- Modify: `internalv2/app/lifecycle.go`
- Modify: `internalv2/app/app_test.go`
- Modify: `internalv2/app/conversation_api_smoke_test.go`
- Modify: `internalv2/app/FLOW.md`
- Modify: `wukongim.conf.example`

- [ ] **Step 1: Add config tests**

In `internalv2/app/app_test.go`, add a config/wiring test for:

```text
SmallGroupFanoutLimit default = 1000
MaxLastMessageConcurrency positive default
conversation usecase is non-nil when app builds
```

- [ ] **Step 2: Run app test and verify failure**

Run:

```bash
go test ./internalv2/app -run 'Test.*Conversation.*Wiring|Test.*Config' -count=1
```

Expected: FAIL until config and wiring are added.

- [ ] **Step 3: Add config fields**

In `internalv2/app/config.go`, add English comments:

```go
// SmallGroupFanoutLimit is the maximum member count that receives dense conversation fanout.
SmallGroupFanoutLimit int

// MaxLastMessageConcurrency limits concurrent channel-owned last-message reads per conversation list request.
MaxLastMessageConcurrency int
```

Wire config parsing and update `wukongim.conf.example` with `WK_` keys matching the project config convention.

- [ ] **Step 4: Wire usecase and projector**

In `internalv2/app/app.go`, construct:

```text
conversation usecase with clusterv2 conversation store and message tail reader
conversation projector with member classifier and batch store
HTTP API Conversations option
```

In `internalv2/app/lifecycle.go`, start/stop projector if it owns workers. If it is event-sink only and uses app worker queues, document that lifecycle has no separate start.

- [ ] **Step 5: Update smoke tests**

Update `internalv2/app/conversation_api_smoke_test.go`:

```text
personal SEND -> projector flush -> /conversation/list for sender and receiver
small group SEND -> member list visible
large group SEND -> non-sender active_at unchanged
last_message comes from message log fields
```

- [ ] **Step 6: Verify app package**

Run:

```bash
go test ./internalv2/app -count=1
```

Expected: PASS.

- [ ] **Step 7: Update app FLOW**

Document final conversation wiring and config keys.

- [ ] **Step 8: Commit Task 7**

Run:

```bash
git add internalv2/app wukongim.conf.example
git commit -m "feat: wire conversation redesign in app"
```

---

### Task 8: Stop ChannelLatest Active Path And Update Docs

**Files:**
- Modify: `internalv2/app/app.go`
- Modify: `internalv2/app/FLOW.md`
- Modify: `internalv2/usecase/conversation/FLOW.md`
- Modify: `internalv2/infra/cluster/FLOW.md`
- Modify: `pkg/db/meta/FLOW.md`
- Modify: `pkg/clusterv2/FLOW.md`
- Modify: `AGENTS.md`
- Remove active-path tests that only validate `channel_latest` as conversation source.

- [ ] **Step 1: Identify current latest references**

Run:

```bash
rg "channel_latest|ChannelLatest|UpsertChannelLatest|GetChannelLatest" internalv2 pkg docs AGENTS.md
```

Expected: references are listed. Categorize each as active-path, durability compatibility, or old test.

- [ ] **Step 2: Remove latest from active conversation path**

Remove or disconnect:

```text
conversation usecase membership/latest ports
app projector wiring that writes channel_latest for conversation list
API tests expecting scanned_memberships/truncated from membership scan
metrics that only describe channel_latest projection
```

Keep durable table IDs, command IDs, decoders, snapshot/inspect support, and any compatibility tests that protect existing data.

- [ ] **Step 3: Add acceptance check**

Create or update a focused test or script-backed Go test that runs:

```bash
rg "channel_latest|ChannelLatest|UpsertChannelLatest|GetChannelLatest" internalv2 pkg docs AGENTS.md
```

The expected allowed leftovers after Phase 1 are:

```text
durability compatibility code
tests for decoder/table compatibility
spec and plan docs
```

- [ ] **Step 4: Update docs**

Update FLOW files and `AGENTS.md` so they describe:

```text
conversation active state as UID-owned
last message read from message log
channel_latest not used by active conversation list
```

- [ ] **Step 5: Verify related packages**

Run:

```bash
go test ./internalv2/usecase/conversation ./internalv2/infra/cluster ./internalv2/access/api ./internalv2/app ./pkg/db/meta ./pkg/clusterv2 ./pkg/slot/fsm -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit Task 8**

Run:

```bash
git add internalv2 pkg docs AGENTS.md
git commit -m "refactor: stop channel latest conversation path"
```

---

### Task 9: End-To-End Verification And Performance Guardrails

**Files:**
- Modify: `internalv2/app/conversation_api_smoke_test.go`
- Create or modify routed smoke tests under `test/e2e/message` or `test/e2e/cluster` if a suitable harness exists.
- Modify: `docs/development/PROJECT_KNOWLEDGE.md` only if a durable project rule is discovered.

- [ ] **Step 1: Run full targeted unit set**

Run:

```bash
go test ./pkg/db/internal/engine ./pkg/db/message ./pkg/db/meta ./pkg/slot/fsm ./pkg/clusterv2 ./internalv2/contracts/messageevents ./internalv2/usecase/message ./internalv2/usecase/conversation ./internalv2/infra/cluster ./internalv2/access/api ./internalv2/app ./pkg/metrics -count=1
```

Expected: PASS.

- [ ] **Step 2: Run single-node cluster smoke**

Run:

```bash
go test ./internalv2/app -run 'TestConversation.*Smoke|TestConversationListAPI' -count=1
```

Expected: PASS with SEND -> projector flush -> `/conversation/list` returning message-log last message.

- [ ] **Step 3: Add routed ownership smoke**

If an existing multi-node e2e harness can start clusterv2 nodes quickly, add a smoke test proving:

```text
UID-owned conversation page routes by uid
channel-owned last-message read routes by channel
the API path does not read local message DB just because it is present
```

If the existing multi-node harness is too slow for unit tests, add this as an integration test under the existing integration-tag pattern:

```go
//go:build integration
```

- [ ] **Step 4: Run integration smoke only when requested**

Run only when the implementation owner is ready for slower verification:

```bash
go test -tags=integration ./test/e2e/... -run Conversation -count=1
```

Expected: PASS. If this is too slow for regular development, document the exact command in final handoff.

- [ ] **Step 5: Inspect metrics names**

Run:

```bash
rg "conversation_list_|conversation_projector_" pkg internalv2
```

Expected: metrics have low-cardinality labels only; no `uid`, `channel_id`, or `client_msg_no` labels.

- [ ] **Step 6: Final channel_latest active-path check**

Run:

```bash
rg "channel_latest|ChannelLatest|UpsertChannelLatest|GetChannelLatest" internalv2 pkg docs AGENTS.md
```

Expected: no active conversation list usecase, app wiring, or API handler depends on latest. Durable compatibility leftovers are documented.

- [ ] **Step 7: Commit verification updates**

Run:

```bash
git add internalv2 test docs
git commit -m "test: verify conversation redesign flow"
```

---

## Implementation Notes

- Use `apply_patch` for manual edits.
- Before editing any package, read its `FLOW.md` if present and update it when behavior changes.
- Keep new comments in English for exported fields and key methods.
- Keep projector writes asynchronous. Message append must not synchronously write per-user conversation rows.
- Do not physically reuse or repurpose `channel_latest` table IDs or command IDs.
- Do not add metrics labels containing UID, channel ID, client message number, or peer UID.

## Self-Review Checklist

- Spec coverage:
  - Message durable fields and tail read: Task 1.
  - SparseActive and active cursor: Task 2.
  - UID-owned slot/clusterv2 surface: Task 3.
  - Active-page list usecase and last message loading: Task 4.
  - Final API and metrics: Task 5.
  - Dense/sparse projector: Task 6.
  - App/config/smoke: Task 7.
  - ChannelLatest Phase 1 stop: Task 8.
  - Verification and routed smoke: Task 9.
- Placeholder scan:
  - This plan uses concrete file paths, commands, and expected outcomes.
  - No task requires inventing a missing subsystem without naming the files and tests first.
- Type consistency:
  - `ServerTimestampMS`, `SparseActive`, `UserConversationActiveCursor`, `GetLastVisibleMessage`, and `GetLastVisibleMessageBatch` are used consistently.
