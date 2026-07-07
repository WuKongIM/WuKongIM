# Message Event Projection Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add first-stage message event projection support with `POST /message/event` and `/channel/messagesync` event summaries, without `/message/eventsync` or realtime event fanout.

**Architecture:** Event append stays inside the promoted access -> usecase -> infra -> cluster -> Slot FSM -> metadb flow. The storage layer keeps one state row per `(channel_id, channel_type, client_msg_no, event_key)` plus one cursor row per `(channel_id, channel_type, client_msg_no)`, and the Slot FSM returns the assigned `msg_event_seq` through a narrow result-returning proposal path. Message sync enriches returned messages from batched event-state reads.

**Tech Stack:** Go, Gin HTTP API, internal usecase ports, pkg/cluster Slot proposals, pkg/slot/fsm TLV commands, pkg/db/meta typed tables, Go unit tests.

---

## File Structure

- Create `pkg/db/meta/table_message_event.go`: typed meta tables, reducer, sequence cursor, append/list APIs, and value codecs.
- Create `pkg/db/meta/message_event_test.go`: storage/reducer tests migrated from the legacy project.
- Modify `pkg/db/meta/types.go`: table IDs and family/index IDs for message event state and cursor.
- Modify `pkg/db/meta/compat.go`: `ShardStore` and `WriteBatch` compatibility methods used by Slot FSM and cluster facade.
- Modify `pkg/db/meta/schema_test.go`: schema registry assertions for the new tables.
- Modify `pkg/cluster/propose/types.go`, `pkg/cluster/propose/service.go`, `pkg/cluster/propose/forward.go`, `pkg/cluster/default_slot_proposer.go`, `pkg/cluster/api.go`, `pkg/cluster/node.go`: result-returning proposal path.
- Add or modify proposal tests in `pkg/cluster/propose/propose_test.go` and `pkg/cluster/default_slot_proposer_test.go`.
- Modify `pkg/slot/fsm/command.go`: TLV command for message event append and result encoding/decoding.
- Modify `pkg/slot/fsm/state_machine_test.go`: FSM apply test for message event append result and stored state.
- Modify `pkg/cluster/node_meta.go`: cluster facade methods for append and batched state reads.
- Create `pkg/cluster/node_meta_message_event_test.go`: single-node cluster facade tests.
- Create `internal/infra/cluster/message_event.go`: adapter from message usecase ports to cluster facade.
- Create `internal/infra/cluster/message_event_test.go`: adapter mapping and error tests.
- Modify `internal/usecase/message/app.go`, `internal/usecase/message/ports.go`, `internal/usecase/message/errors.go`, `internal/usecase/message/types.go`, `internal/usecase/message/sync.go`, `internal/usecase/message/FLOW.md`: event options, ports, DTOs, errors, sync enrichment, and flow docs.
- Create `internal/usecase/message/event.go` and `internal/usecase/message/event_summary.go`: append orchestration and event summary mapping.
- Create `internal/usecase/message/event_test.go`: append validation, person-channel normalization, message existence, idempotency, terminal behavior, and sync enrichment tests.
- Modify `internal/access/api/server.go`, `internal/access/api/message_send.go`, `internal/access/api/message_legacy_model.go`, `internal/access/api/channel_messagesync.go`, `internal/access/api/FLOW.md`: API interface, route registration, response mapping, and docs.
- Create `internal/access/api/message_event.go` and `internal/access/api/message_event_test.go`: HTTP request/response mapping and compatible error tests.
- Modify `internal/app/wiring.go`: inject the cluster event adapter into the message usecase.
- Modify `internal/app/app_test.go`: wiring coverage for message events when the cluster supports the facade.
- Modify `pkg/db/meta/FLOW.md` and `internal/FLOW.md`: document the replicated message event projection location and route.

## Constants And Shapes

Use these exact metadb constants in `pkg/db/meta/table_message_event.go`:

```go
const (
	EventTypeStreamDelta    = "stream.delta"
	EventTypeStreamClose    = "stream.close"
	EventTypeStreamError    = "stream.error"
	EventTypeStreamCancel   = "stream.cancel"
	EventTypeStreamSnapshot = "stream.snapshot"
	EventTypeStreamFinish   = "stream.finish"

	EventStatusOpen      = "open"
	EventStatusClosed    = "closed"
	EventStatusError     = "error"
	EventStatusCancelled = "cancelled"

	EventKeyDefault = "main"
	EventKeyFinish  = "__finish__"

	VisibilityPublic     = "public"
	VisibilityPrivate    = "private"
	VisibilityRestricted = "restricted"

	SnapshotKindText = "text"
)
```

Use these storage DTOs:

```go
type MessageEventAppend struct {
	ChannelID   string
	ChannelType int64
	ClientMsgNo string
	EventID     string
	EventKey    string
	EventType   string
	Visibility  string
	OccurredAt  int64
	Payload     []byte
	UpdatedAt   int64
}

type MessageEventAppendResult struct {
	ChannelID    string
	ChannelType  int64
	ClientMsgNo  string
	EventID      string
	EventKey     string
	MsgEventSeq uint64
	Status       string
	State        MessageEventState
}

type MessageEventState struct {
	ChannelID       string
	ChannelType     int64
	ClientMsgNo     string
	EventKey        string
	Status          string
	LastMsgEventSeq uint64
	LastEventID     string
	LastEventType   string
	LastVisibility  string
	LastOccurredAt  int64
	SnapshotPayload []byte
	EndReason       uint8
	Error           string
	UpdatedAt       int64
}

type MessageEventCursor struct {
	ChannelID        string
	ChannelType      int64
	ClientMsgNo      string
	LastMsgEventSeq  uint64
	UpdatedAt        int64
}

type MessageEventMessageKey struct {
	ChannelID   string
	ChannelType int64
	ClientMsgNo string
}
```

## Task 1: Meta Storage And Reducer

**Files:**
- Modify: `pkg/db/meta/types.go`
- Create: `pkg/db/meta/table_message_event.go`
- Modify: `pkg/db/meta/compat.go`
- Modify: `pkg/db/meta/schema_test.go`
- Create: `pkg/db/meta/message_event_test.go`
- Modify: `pkg/db/meta/FLOW.md`

- [ ] **Step 1: Add failing storage tests**

Create `pkg/db/meta/message_event_test.go` with these test names and assertions:

```go
func TestMessageEventAppendTextLifecycle(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	shard := store.db.ForHashSlot(4)

	first, err := shard.AppendMessageEvent(ctx, MessageEventAppend{
		ChannelID: "g1", ChannelType: 2, ClientMsgNo: "cmn-text",
		EventID: "evt-1", EventKey: "main", EventType: EventTypeStreamDelta,
		Payload: []byte(`{"kind":"text","delta":"hello"}`), OccurredAt: 10, UpdatedAt: 11,
	})
	if err != nil {
		t.Fatalf("AppendMessageEvent(first): %v", err)
	}
	if first.MsgEventSeq != 1 || first.Status != EventStatusOpen {
		t.Fatalf("first result = %#v, want seq=1 status=open", first)
	}

	second, err := shard.AppendMessageEvent(ctx, MessageEventAppend{
		ChannelID: "g1", ChannelType: 2, ClientMsgNo: "cmn-text",
		EventID: "evt-2", EventKey: "main", EventType: EventTypeStreamDelta,
		Payload: []byte(`{"kind":"text","delta":" world"}`), OccurredAt: 12, UpdatedAt: 13,
	})
	if err != nil {
		t.Fatalf("AppendMessageEvent(second): %v", err)
	}
	if second.MsgEventSeq != 2 {
		t.Fatalf("second seq = %d, want 2", second.MsgEventSeq)
	}

	closed, err := shard.AppendMessageEvent(ctx, MessageEventAppend{
		ChannelID: "g1", ChannelType: 2, ClientMsgNo: "cmn-text",
		EventID: "evt-3", EventKey: "main", EventType: EventTypeStreamClose,
		Payload: []byte(`{"end_reason":2}`), OccurredAt: 14, UpdatedAt: 15,
	})
	if err != nil {
		t.Fatalf("AppendMessageEvent(close): %v", err)
	}
	if closed.MsgEventSeq != 3 || closed.State.EndReason != 2 {
		t.Fatalf("closed result = %#v, want seq=3 end_reason=2", closed)
	}

	state, err := shard.GetMessageEventState(ctx, "g1", 2, "cmn-text", "main")
	if err != nil {
		t.Fatalf("GetMessageEventState(): %v", err)
	}
	if state.Status != EventStatusClosed || state.LastMsgEventSeq != 3 {
		t.Fatalf("state = %#v, want closed seq=3", state)
	}
	if got, want := string(state.SnapshotPayload), `{"kind":"text","text":"hello world"}`; !jsonEqualForTest(got, want) {
		t.Fatalf("snapshot = %s, want %s", got, want)
	}
}

func jsonEqualForTest(left, right string) bool {
	var leftValue any
	var rightValue any
	if err := json.Unmarshal([]byte(left), &leftValue); err != nil {
		return false
	}
	if err := json.Unmarshal([]byte(right), &rightValue); err != nil {
		return false
	}
	return reflect.DeepEqual(leftValue, rightValue)
}
```

Add these additional tests in the same file:

```go
func TestMessageEventAppendIdempotentByLastEventID(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	shard := store.db.ForHashSlot(4)
	first, err := shard.AppendMessageEvent(ctx, MessageEventAppend{
		ChannelID: "g1", ChannelType: 2, ClientMsgNo: "cmn-idem",
		EventID: "evt-same", EventKey: "main", EventType: EventTypeStreamDelta,
		Payload: []byte(`{"kind":"text","delta":"A"}`), UpdatedAt: 10,
	})
	if err != nil {
		t.Fatalf("AppendMessageEvent(first): %v", err)
	}
	second, err := shard.AppendMessageEvent(ctx, MessageEventAppend{
		ChannelID: "g1", ChannelType: 2, ClientMsgNo: "cmn-idem",
		EventID: "evt-same", EventKey: "main", EventType: EventTypeStreamDelta,
		Payload: []byte(`{"kind":"text","delta":"B"}`), UpdatedAt: 11,
	})
	if err != nil {
		t.Fatalf("AppendMessageEvent(second): %v", err)
	}
	if second.MsgEventSeq != first.MsgEventSeq {
		t.Fatalf("second seq = %d, want %d", second.MsgEventSeq, first.MsgEventSeq)
	}
	state, err := shard.GetMessageEventState(ctx, "g1", 2, "cmn-idem", "main")
	if err != nil {
		t.Fatalf("GetMessageEventState(): %v", err)
	}
	if got, want := string(state.SnapshotPayload), `{"kind":"text","text":"A"}`; !jsonEqualForTest(got, want) {
		t.Fatalf("snapshot = %s, want %s", got, want)
	}
}

func TestMessageEventAppendTerminalDoesNotAdvanceSeq(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	shard := store.db.ForHashSlot(4)
	closed, err := shard.AppendMessageEvent(ctx, MessageEventAppend{
		ChannelID: "g1", ChannelType: 2, ClientMsgNo: "cmn-terminal",
		EventID: "evt-close", EventKey: "main", EventType: EventTypeStreamClose, UpdatedAt: 10,
	})
	if err != nil {
		t.Fatalf("AppendMessageEvent(close): %v", err)
	}
	ignored, err := shard.AppendMessageEvent(ctx, MessageEventAppend{
		ChannelID: "g1", ChannelType: 2, ClientMsgNo: "cmn-terminal",
		EventID: "evt-after", EventKey: "main", EventType: EventTypeStreamDelta,
		Payload: []byte(`{"kind":"text","delta":"ignored"}`), UpdatedAt: 11,
	})
	if err != nil {
		t.Fatalf("AppendMessageEvent(after close): %v", err)
	}
	if ignored.MsgEventSeq != closed.MsgEventSeq || ignored.Status != EventStatusClosed {
		t.Fatalf("ignored result = %#v, want terminal seq %d", ignored, closed.MsgEventSeq)
	}
}

func TestMessageEventAppendNormalizesEmptyEventKey(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	result, err := store.db.ForHashSlot(4).AppendMessageEvent(context.Background(), MessageEventAppend{
		ChannelID: "g1", ChannelType: 2, ClientMsgNo: "cmn-key",
		EventID: "evt-1", EventKey: "   ", EventType: EventTypeStreamDelta, UpdatedAt: 10,
	})
	if err != nil {
		t.Fatalf("AppendMessageEvent(): %v", err)
	}
	if result.EventKey != EventKeyDefault {
		t.Fatalf("event key = %q, want %q", result.EventKey, EventKeyDefault)
	}
}

func TestMessageEventAppendFinishUsesFinishKey(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	result, err := store.db.ForHashSlot(4).AppendMessageEvent(context.Background(), MessageEventAppend{
		ChannelID: "g1", ChannelType: 2, ClientMsgNo: "cmn-finish",
		EventID: "evt-finish", EventType: EventTypeStreamFinish, UpdatedAt: 10,
	})
	if err != nil {
		t.Fatalf("AppendMessageEvent(): %v", err)
	}
	if result.EventKey != EventKeyFinish || result.Status != EventStatusClosed {
		t.Fatalf("result = %#v, want finish key closed", result)
	}
}

func TestMessageEventAppendBatchOverlayAllocatesDistinctSeq(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	batch := store.db.NewWriteBatch()
	defer batch.Close()
	first, err := batch.AppendMessageEvent(4, MessageEventAppend{
		ChannelID: "g1", ChannelType: 2, ClientMsgNo: "cmn-batch",
		EventID: "evt-1", EventKey: "main", EventType: EventTypeStreamDelta, UpdatedAt: 10,
	})
	if err != nil {
		t.Fatalf("AppendMessageEvent(first): %v", err)
	}
	second, err := batch.AppendMessageEvent(4, MessageEventAppend{
		ChannelID: "g1", ChannelType: 2, ClientMsgNo: "cmn-batch",
		EventID: "evt-2", EventKey: "tool", EventType: EventTypeStreamDelta, UpdatedAt: 11,
	})
	if err != nil {
		t.Fatalf("AppendMessageEvent(second): %v", err)
	}
	if first.MsgEventSeq != 1 || second.MsgEventSeq != 2 {
		t.Fatalf("seqs = %d/%d, want 1/2", first.MsgEventSeq, second.MsgEventSeq)
	}
	if err := batch.Commit(); err != nil {
		t.Fatalf("Commit(): %v", err)
	}
	states, err := store.db.ForHashSlot(4).ListMessageEventStates(ctx, "g1", 2, "cmn-batch", 10)
	if err != nil {
		t.Fatalf("ListMessageEventStates(): %v", err)
	}
	if len(states) != 2 {
		t.Fatalf("states = %d, want 2", len(states))
	}
}

func TestMessageEventListStatesByClientMsgNo(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	shard := store.db.ForHashSlot(4)
	_, _ = shard.AppendMessageEvent(ctx, MessageEventAppend{ChannelID: "g1", ChannelType: 2, ClientMsgNo: "cmn-a", EventID: "evt-a", EventKey: "main", EventType: EventTypeStreamDelta, UpdatedAt: 10})
	_, _ = shard.AppendMessageEvent(ctx, MessageEventAppend{ChannelID: "g1", ChannelType: 2, ClientMsgNo: "cmn-b", EventID: "evt-b", EventKey: "main", EventType: EventTypeStreamDelta, UpdatedAt: 11})
	states, err := shard.ListMessageEventStates(ctx, "g1", 2, "cmn-a", 10)
	if err != nil {
		t.Fatalf("ListMessageEventStates(): %v", err)
	}
	if len(states) != 1 || states[0].ClientMsgNo != "cmn-a" {
		t.Fatalf("states = %#v, want only cmn-a", states)
	}
}
```

- [ ] **Step 2: Run the failing tests**

Run: `go test ./pkg/db/meta -run 'TestMessageEvent|TestMetaSchemaValidateAllTables'`

Expected: compile errors for undefined `MessageEventAppend`, `AppendMessageEvent`, `GetMessageEventState`, and table IDs.

- [ ] **Step 3: Add table IDs and schema registration**

In `pkg/db/meta/types.go`, append these IDs after `TableIDChannelLatest`:

```go
// TableIDMessageEventState stores message event lane projection state.
TableIDMessageEventState uint32 = 13
// TableIDMessageEventCursor stores the latest event sequence per message.
TableIDMessageEventCursor uint32 = 14
```

Add family/index constants:

```go
messageEventStatePrimaryFamilyID uint16 = 0
messageEventStatePrimaryIndexID  uint16 = 1

messageEventCursorPrimaryFamilyID uint16 = 0
messageEventCursorPrimaryIndexID  uint16 = 1
```

In `pkg/db/meta/table_message_event.go`, define `MessageEventStateTable` and `MessageEventCursorTable` using `registerMetaTable`. The state primary key is `channel_id, channel_type, client_msg_no, event_key`. The cursor primary key is `channel_id, channel_type, client_msg_no`.

- [ ] **Step 4: Add reducer and append APIs**

In `pkg/db/meta/table_message_event.go`, implement:

```go
func (s *Shard) GetMessageEventState(ctx context.Context, channelID string, channelType int64, clientMsgNo string, eventKey string) (MessageEventState, bool, error)
func (s *Shard) ListMessageEventStates(ctx context.Context, channelID string, channelType int64, clientMsgNo string, limit int) ([]MessageEventState, error)
func (s *Shard) AppendMessageEvent(ctx context.Context, event MessageEventAppend) (MessageEventAppendResult, error)
func (b *Batch) AppendMessageEvent(hashSlot HashSlot, event MessageEventAppend) (MessageEventAppendResult, error)
```

The reducer must lower-case `EventType`, normalize empty `EventKey` to `main`, convert `stream.finish` to event key `__finish__`, and keep payload byte slices cloned before storage. Use `json.Unmarshal` for text deltas and encode text snapshots with compact `json.Marshal(map[string]string{"kind":"text","text": text})`. Add private overlay maps to `Batch` so staged message event appends in one commit see each other's cursor and state updates.

- [ ] **Step 5: Add compatibility batch methods**

In `pkg/db/meta/compat.go`, add methods:

```go
func (s *ShardStore) AppendMessageEvent(ctx context.Context, event MessageEventAppend) (MessageEventAppendResult, error)
func (s *ShardStore) GetMessageEventState(ctx context.Context, channelID string, channelType int64, clientMsgNo string, eventKey string) (MessageEventState, error)
func (s *ShardStore) ListMessageEventStates(ctx context.Context, channelID string, channelType int64, clientMsgNo string, limit int) ([]MessageEventState, error)
func (b *WriteBatch) AppendMessageEvent(hashSlot uint16, event MessageEventAppend) (MessageEventAppendResult, error)
```

Make `WriteBatch.AppendMessageEvent` delegate to `Batch.AppendMessageEvent`; the overlay maps live in `Batch` because direct batch users and compatibility batch users need the same sequence allocation behavior.

- [ ] **Step 6: Update schema tests and docs**

In `pkg/db/meta/schema_test.go`, assert both new table IDs are present and have primary names:

```go
pk_message_event_state
pk_message_event_cursor
```

In `pkg/db/meta/FLOW.md`, add a short paragraph stating that message event projections are hash-slot scoped meta rows replicated by Slot Raft and snapshotted with other meta rows.

- [ ] **Step 7: Run storage tests**

Run: `go test ./pkg/db/meta -run 'TestMessageEvent|TestMetaSchemaValidateAllTables'`

Expected: `ok  	github.com/WuKongIM/WuKongIM/pkg/db/meta`

- [ ] **Step 8: Commit storage work**

```bash
git add pkg/db/meta/types.go pkg/db/meta/table_message_event.go pkg/db/meta/compat.go pkg/db/meta/schema_test.go pkg/db/meta/message_event_test.go pkg/db/meta/FLOW.md
git commit -m "feat: add message event projection storage"
```

## Task 2: Result-Returning Slot Proposal Path

**Files:**
- Modify: `pkg/cluster/propose/types.go`
- Modify: `pkg/cluster/propose/service.go`
- Modify: `pkg/cluster/propose/forward.go`
- Modify: `pkg/cluster/default_slot_proposer.go`
- Modify: `pkg/cluster/api.go`
- Modify: `pkg/cluster/node.go`
- Modify: `pkg/cluster/propose/propose_test.go`
- Modify: `pkg/cluster/default_slot_proposer_test.go`

- [ ] **Step 1: Add failing proposal result tests**

Add tests that prove both local and forwarded proposals return apply bytes:

```go
func TestServiceProposeResultReturnsLocalApplyData(t *testing.T) {
	slots := &fakeSlots{result: []byte("seq=7")}
	svc := NewService(Config{
		LocalNode: 1,
		Router:    singleRouteRouter{route: routing.Route{SlotID: 11, HashSlot: 3, Leader: 1}},
		Slots:     slots,
	})
	got, err := svc.ProposeResult(context.Background(), Request{Key: "g1", Command: []byte("cmd")})
	if err != nil {
		t.Fatalf("ProposeResult() error = %v", err)
	}
	if string(got) != "seq=7" {
		t.Fatalf("result = %q, want seq=7", got)
	}
}

func TestServiceProposeResultReturnsForwardedApplyData(t *testing.T) {
	forward := &fakeForward{result: []byte("remote-seq=9")}
	svc := NewService(Config{
		LocalNode: 1,
		Router:    singleRouteRouter{route: routing.Route{SlotID: 11, HashSlot: 3, Leader: 2}},
		Slots:     &fakeSlots{},
		Forward:   forward,
	})
	got, err := svc.ProposeResult(context.Background(), Request{Key: "g1", Command: []byte("cmd")})
	if err != nil {
		t.Fatalf("ProposeResult() error = %v", err)
	}
	if string(got) != "remote-seq=9" {
		t.Fatalf("result = %q, want remote-seq=9", got)
	}
}
```

- [ ] **Step 2: Run the failing proposal tests**

Run: `go test ./pkg/cluster/propose ./pkg/cluster -run 'TestServiceProposeResult|TestDefaultSlotProposer'`

Expected: compile errors for undefined `ProposeResult`.

- [ ] **Step 3: Add result interfaces without breaking existing callers**

In `pkg/cluster/propose/types.go`, add:

```go
type ResultSlotRuntime interface {
	IsLocalLeader(slotID uint32) bool
	ProposeResult(context.Context, uint32, []byte) ([]byte, error)
}

type ResultForwardClient interface {
	ForwardProposeResult(context.Context, uint64, ForwardRequest) ([]byte, error)
}
```

Keep the existing `SlotRuntime` and `ForwardClient` interfaces unchanged.

- [ ] **Step 4: Implement service result routing**

In `pkg/cluster/propose/service.go`, add:

```go
func (s *Service) ProposeResult(ctx context.Context, req Request) ([]byte, error)
func (s *Service) proposeOnceResult(ctx context.Context, req Request) ([]byte, error)
func (s *Service) proposeToLeaderResult(ctx context.Context, route routing.Route, leader uint64, payload []byte) ([]byte, error)
```

Make `Propose` call `ProposeResult` and discard the result. Local routing uses `ResultSlotRuntime` when available. Remote routing uses `ResultForwardClient` when available and falls back to `ForwardPropose` with a nil result for old test fakes.

- [ ] **Step 5: Return result bytes through network forwarding**

In `pkg/cluster/propose/forward.go`, add:

```go
func (c *NetworkForwardClient) ForwardProposeResult(ctx context.Context, nodeID uint64, req ForwardRequest) ([]byte, error)
```

Change `ForwardPropose` to call `ForwardProposeResult` and discard the returned bytes. Change `ForwardHandler.HandleRPC` to call `ProposeResult` when its `slots` value implements `ResultSlotRuntime`; otherwise keep the existing error-only path.

- [ ] **Step 6: Expose result proposal from default slot proposer and node**

In `pkg/cluster/default_slot_proposer.go`, add:

```go
func (p defaultSlotProposer) ProposeResult(ctx context.Context, slotID uint32, payload []byte) ([]byte, error)
```

Move the existing future wait code into `ProposeResult`; keep `Propose` as a wrapper that discards bytes.

In `pkg/cluster/api.go`, add:

```go
func (n *Node) ProposeResult(ctx context.Context, req ProposeRequest) ([]byte, error)
```

In `pkg/cluster/node.go`, implement it by calling `n.proposer.ProposeResult` when available. Keep `Node.Propose` as a wrapper.

- [ ] **Step 7: Run proposal tests**

Run: `go test ./pkg/cluster/propose ./pkg/cluster -run 'TestServiceProposeResult|TestNetworkForwardClient|TestForwardHandler|TestDefaultSlotProposer'`

Expected: all selected tests pass.

- [ ] **Step 8: Commit proposal work**

```bash
git add pkg/cluster/propose/types.go pkg/cluster/propose/service.go pkg/cluster/propose/forward.go pkg/cluster/default_slot_proposer.go pkg/cluster/api.go pkg/cluster/node.go pkg/cluster/propose/propose_test.go pkg/cluster/default_slot_proposer_test.go
git commit -m "feat: return slot proposal apply results"
```

## Task 3: Slot FSM Command And Cluster Facade

**Files:**
- Modify: `pkg/slot/fsm/command.go`
- Modify: `pkg/slot/fsm/state_machine_test.go`
- Modify: `pkg/cluster/node_meta.go`
- Create: `pkg/cluster/node_meta_message_event_test.go`

- [ ] **Step 1: Add failing FSM tests**

In `pkg/slot/fsm/state_machine_test.go`, add:

```go
func TestStateMachineAppliesMessageEventAppend(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm := mustNewStateMachine(t, db, 11)

	result, err := sm.Apply(ctx, multiraft.Command{
		SlotID: 11,
		Index:  1,
		Term:   1,
		Data: EncodeAppendMessageEventCommand(metadb.MessageEventAppend{
			ChannelID: "g1", ChannelType: 2, ClientMsgNo: "cmn-1",
			EventID: "evt-1", EventKey: "main", EventType: metadb.EventTypeStreamDelta,
			Payload: []byte(`{"kind":"text","delta":"hi"}`), OccurredAt: 10, UpdatedAt: 11,
		}),
	})
	if err != nil {
		t.Fatalf("Apply(append message event) error = %v", err)
	}
	got, err := DecodeAppendMessageEventResult(result)
	if err != nil {
		t.Fatalf("DecodeAppendMessageEventResult(): %v", err)
	}
	if got.MsgEventSeq != 1 || got.Status != metadb.EventStatusOpen {
		t.Fatalf("result = %#v, want seq=1 open", got)
	}
	state, err := db.ForSlot(11).GetMessageEventState(ctx, "g1", 2, "cmn-1", "main")
	if err != nil {
		t.Fatalf("GetMessageEventState(): %v", err)
	}
	if state.LastMsgEventSeq != 1 {
		t.Fatalf("state seq = %d, want 1", state.LastMsgEventSeq)
	}
}
```

- [ ] **Step 2: Run failing FSM test**

Run: `go test ./pkg/slot/fsm -run TestStateMachineAppliesMessageEventAppend`

Expected: compile errors for undefined encoder and decoder.

- [ ] **Step 3: Implement TLV command and result codecs**

In `pkg/slot/fsm/command.go`, allocate:

```go
cmdTypeAppendMessageEvent uint8 = 48
```

Add tags for append fields:

```go
tagMessageEventChannelID  uint8 = 1
tagMessageEventChannelType uint8 = 2
tagMessageEventClientMsgNo uint8 = 3
tagMessageEventEventID     uint8 = 4
tagMessageEventEventKey    uint8 = 5
tagMessageEventEventType   uint8 = 6
tagMessageEventVisibility  uint8 = 7
tagMessageEventOccurredAt  uint8 = 8
tagMessageEventPayload     uint8 = 9
tagMessageEventUpdatedAt   uint8 = 10
```

Add an `appendMessageEventCmd` that calls `wb.AppendMessageEvent(hashSlot, event)`, stores the returned `metadb.MessageEventAppendResult`, and implements `applyResult() []byte`.

Expose:

```go
func EncodeAppendMessageEventCommand(event metadb.MessageEventAppend) []byte
func EncodeAppendMessageEventCommandChecked(event metadb.MessageEventAppend) ([]byte, error)
func DecodeAppendMessageEventResult(data []byte) (metadb.MessageEventAppendResult, error)
```

- [ ] **Step 4: Add cluster facade tests**

Create `pkg/cluster/node_meta_message_event_test.go`:

```go
func TestClusterMessageEventFacadeUsesChannelHashSlotAndReturnsSeq(t *testing.T) {
	node := newDefaultSingleNode(t)
	startNode(t, node)
	t.Cleanup(func() { stopNodes(t, node) })

	channelID := "event-channel"
	route := waitRouteKeyLeaderReady(t, node, channelID)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	first, err := node.AppendMessageEvent(ctx, metadb.MessageEventAppend{
		ChannelID: channelID, ChannelType: 2, ClientMsgNo: "cmn-1",
		EventID: "evt-1", EventKey: "main", EventType: metadb.EventTypeStreamDelta,
		Payload: []byte(`{"kind":"text","delta":"hi"}`), OccurredAt: 10, UpdatedAt: 11,
	})
	if err != nil {
		t.Fatalf("AppendMessageEvent(first): %v", err)
	}
	if first.MsgEventSeq != 1 {
		t.Fatalf("first seq = %d, want 1", first.MsgEventSeq)
	}

	second, err := node.AppendMessageEvent(ctx, metadb.MessageEventAppend{
		ChannelID: channelID, ChannelType: 2, ClientMsgNo: "cmn-1",
		EventID: "evt-2", EventKey: "main", EventType: metadb.EventTypeStreamDelta,
		Payload: []byte(`{"kind":"text","delta":"!"}`), OccurredAt: 12, UpdatedAt: 13,
	})
	if err != nil {
		t.Fatalf("AppendMessageEvent(second): %v", err)
	}
	if second.MsgEventSeq != 2 {
		t.Fatalf("second seq = %d, want 2", second.MsgEventSeq)
	}

	stored, err := node.defaultSlotMetaDB.ForHashSlot(route.HashSlot).GetMessageEventState(ctx, channelID, 2, "cmn-1", "main")
	if err != nil {
		t.Fatalf("stored event state: %v", err)
	}
	if stored.LastMsgEventSeq != 2 {
		t.Fatalf("stored seq = %d, want 2", stored.LastMsgEventSeq)
	}
}
```

- [ ] **Step 5: Implement cluster facade**

In `pkg/cluster/node_meta.go`, add:

```go
func (n *Node) AppendMessageEvent(ctx context.Context, event metadb.MessageEventAppend) (metadb.MessageEventAppendResult, error)
func (n *Node) GetMessageEventStatesBatch(ctx context.Context, keys []metadb.MessageEventMessageKey, limit int) (map[metadb.MessageEventMessageKey][]metadb.MessageEventState, error)
```

If the result decoder returns an error, wrap and return it. Group batched reads by route hash slot and skip `metadb.ErrNotFound`.

- [ ] **Step 6: Run FSM and cluster tests**

Run: `go test ./pkg/slot/fsm ./pkg/cluster -run 'TestStateMachineAppliesMessageEventAppend|TestClusterMessageEventFacade'`

Expected: selected tests pass.

- [ ] **Step 7: Commit FSM and cluster facade**

```bash
git add pkg/slot/fsm/command.go pkg/slot/fsm/state_machine_test.go pkg/cluster/node_meta.go pkg/cluster/node_meta_message_event_test.go
git commit -m "feat: add message event slot command"
```

## Task 4: Cluster Infra Adapter

**Files:**
- Create: `internal/infra/cluster/message_event.go`
- Create: `internal/infra/cluster/message_event_test.go`
- Modify: `internal/infra/cluster/FLOW.md`

- [ ] **Step 1: Add failing adapter tests**

Create tests for append, message lookup, and batched states:

```go
func TestMessageEventStoreAppendMapsToNode(t *testing.T) {
	node := &recordingMessageEventNode{
		appendResult: metadb.MessageEventAppendResult{MsgEventSeq: 7, Status: metadb.EventStatusOpen},
	}
	store := NewMessageEventStore(node)
	result, err := store.AppendMessageEvent(context.Background(), message.EventAppend{
		ChannelID: "g1", ChannelType: 2, ClientMsgNo: "cmn-1",
		EventID: "evt-1", EventKey: "main", EventType: metadb.EventTypeStreamDelta,
	})
	if err != nil {
		t.Fatalf("AppendMessageEvent() error = %v", err)
	}
	if result.MsgEventSeq != 7 || node.appendCalls[0].ChannelID != "g1" {
		t.Fatalf("result=%#v calls=%#v, want seq=7 g1", result, node.appendCalls)
	}
}

func TestMessageEventStoreLookupUsesIdempotency(t *testing.T) {
	node := &recordingMessageEventNode{
		idempotencyOK: true,
		idempotencyHit: channelstore.IdempotencyHit{
			Message: channelruntime.Message{MessageID: 99, MessageSeq: 3, FromUID: "u1", ClientMsgNo: "cmn-1"},
		},
	}
	store := NewMessageEventStore(node)
	got, ok, err := store.LookupMessage(context.Background(), message.EventMessageLookup{
		ChannelID: "g1", ChannelType: 2, FromUID: "u1", ClientMsgNo: "cmn-1",
	})
	if err != nil || !ok {
		t.Fatalf("LookupMessage() ok=%v err=%v, want hit", ok, err)
	}
	if got.MessageSeq != 3 || got.MessageID != 99 {
		t.Fatalf("message = %#v, want idempotency hit", got)
	}
}
```

- [ ] **Step 2: Run failing adapter tests**

Run: `go test ./internal/infra/cluster -run TestMessageEventStore`

Expected: compile errors for undefined `NewMessageEventStore` and message event port types.

- [ ] **Step 3: Implement adapter**

In `internal/infra/cluster/message_event.go`, define:

```go
type MessageEventNode interface {
	AppendMessageEvent(context.Context, metadb.MessageEventAppend) (metadb.MessageEventAppendResult, error)
	GetMessageEventStatesBatch(context.Context, []metadb.MessageEventMessageKey, int) (map[metadb.MessageEventMessageKey][]metadb.MessageEventState, error)
	LookupChannelIdempotency(context.Context, channelruntime.ChannelID, string, string) (channelstore.IdempotencyHit, bool, error)
}

type MessageEventStore struct {
	node MessageEventNode
}

func NewMessageEventStore(node MessageEventNode) *MessageEventStore
```

Map append errors through `mapAppendError`, and treat missing idempotency lookup as `(zero, false, nil)` unless the error is a context error.

- [ ] **Step 4: Update infra flow docs**

In `internal/infra/cluster/FLOW.md`, add that `MessageEventStore` adapts message event append, idempotency lookup, and state batch reads to the cluster node facade.

- [ ] **Step 5: Run adapter tests**

Run: `go test ./internal/infra/cluster -run TestMessageEventStore`

Expected: selected tests pass.

- [ ] **Step 6: Commit adapter work**

```bash
git add internal/infra/cluster/message_event.go internal/infra/cluster/message_event_test.go internal/infra/cluster/FLOW.md
git commit -m "feat: adapt message events to cluster"
```

## Task 5: Message Usecase Append And Sync Enrichment

**Files:**
- Modify: `internal/usecase/message/app.go`
- Modify: `internal/usecase/message/ports.go`
- Modify: `internal/usecase/message/errors.go`
- Modify: `internal/usecase/message/types.go`
- Modify: `internal/usecase/message/sync.go`
- Create: `internal/usecase/message/event.go`
- Create: `internal/usecase/message/event_summary.go`
- Create: `internal/usecase/message/event_test.go`
- Modify: `internal/usecase/message/FLOW.md`

- [ ] **Step 1: Add failing usecase tests**

Create `internal/usecase/message/event_test.go` with tests:

```go
func TestAppendEventNormalizesPersonChannelAndRequiresExistingMessage(t *testing.T) {
	store := &recordingEventStore{
		messageHit: EventMessage{MessageID: 99, MessageSeq: 3, FromUID: "u1", ClientMsgNo: "cmn-1"},
		appendResult: EventAppendResult{MsgEventSeq: 1, Status: metadb.EventStatusOpen},
	}
	app := New(Options{Events: store, Now: func() time.Time { return time.UnixMilli(1234) }})
	result, err := app.AppendEvent(context.Background(), AppendEventCommand{
		FromUID: "u1", ChannelID: "u2", ChannelType: channelTypePerson,
		ClientMsgNo: "cmn-1", EventID: "evt-1", EventType: metadb.EventTypeStreamDelta,
		Payload: []byte(`{"kind":"text","delta":"hi"}`),
	})
	if err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}
	wantChannel := runtimechannelid.EncodePersonChannel("u1", "u2")
	if store.lookups[0].ChannelID != wantChannel || store.appends[0].ChannelID != wantChannel {
		t.Fatalf("normalized channel lookup=%#v append=%#v, want %s", store.lookups[0], store.appends[0], wantChannel)
	}
	if result.MsgEventSeq != 1 || result.StreamStatus != metadb.EventStatusOpen {
		t.Fatalf("result = %#v, want seq=1 open", result)
	}
}

func TestAppendEventRejectsMissingMessage(t *testing.T) {
	app := New(Options{Events: &recordingEventStore{}})
	_, err := app.AppendEvent(context.Background(), AppendEventCommand{
		FromUID: "u1", ChannelID: "g1", ChannelType: 2,
		ClientMsgNo: "missing", EventID: "evt-1", EventType: metadb.EventTypeStreamDelta,
	})
	if !errors.Is(err, ErrMessageEventTargetNotFound) {
		t.Fatalf("error = %v, want ErrMessageEventTargetNotFound", err)
	}
}
```

Add sync enrichment tests:

```go
func TestSyncChannelMessagesEnrichesEventMeta(t *testing.T) {
	reader := &recordingChannelMessageReader{
		page: ChannelMessagePage{Messages: []SyncedMessage{{
			MessageID: 10, MessageSeq: 1, ClientMsgNo: "cmn-1",
			ChannelID: "g1", ChannelType: 2, FromUID: "u1",
		}}},
	}
	store := &recordingEventStore{
		states: map[EventMessageKey][]EventState{
			{ChannelID: "g1", ChannelType: 2, ClientMsgNo: "cmn-1"}: {
				{EventKey: metadb.EventKeyDefault, Status: metadb.EventStatusClosed, LastMsgEventSeq: 2, SnapshotPayload: []byte(`{"kind":"text","text":"hello"}`), EndReason: 1},
			},
		},
	}
	app := New(Options{Reader: reader, Events: store})
	result, err := app.SyncChannelMessages(context.Background(), SyncChannelMessagesQuery{
		LoginUID: "u1", ChannelID: "g1", ChannelType: 2, Limit: 10,
	})
	if err != nil {
		t.Fatalf("SyncChannelMessages() error = %v", err)
	}
	got := result.Messages[0]
	if got.EventMeta == nil || !got.EventMeta.HasEvents || got.EventMeta.LastMsgEventSeq != 2 {
		t.Fatalf("event meta = %#v, want has events seq=2", got.EventMeta)
	}
	if got.EventSyncHint == nil || got.EventSyncHint.ClientMsgNo != "cmn-1" {
		t.Fatalf("event sync hint = %#v, want cmn-1", got.EventSyncHint)
	}
	if string(got.StreamData) != "hello" || got.End != 1 || got.EndReason != 1 {
		t.Fatalf("legacy stream fields data=%q end=%d reason=%d, want hello/1/1", got.StreamData, got.End, got.EndReason)
	}
}

func TestSyncChannelMessagesBasicModeOmitsSnapshots(t *testing.T) {
	meta, _, _, _, _, _ := buildMessageEventMeta("cmn-1", []EventState{{
		EventKey: metadb.EventKeyDefault, Status: metadb.EventStatusOpen,
		LastMsgEventSeq: 1, SnapshotPayload: []byte(`{"kind":"text","text":"hello"}`),
	}}, "basic")
	if meta == nil || len(meta.Events) != 1 {
		t.Fatalf("meta = %#v, want one event", meta)
	}
	if meta.Events[0].Snapshot != nil {
		t.Fatalf("snapshot = %#v, want nil in basic mode", meta.Events[0].Snapshot)
	}
}

func TestBuildMessageEventMetaExcludesFinishKeyAndMarksCompleted(t *testing.T) {
	meta, hint, _, _, _, _ := buildMessageEventMeta("cmn-1", []EventState{
		{EventKey: metadb.EventKeyDefault, Status: metadb.EventStatusClosed, LastMsgEventSeq: 2},
		{EventKey: metadb.EventKeyFinish, Status: metadb.EventStatusClosed, LastMsgEventSeq: 3},
	}, "full")
	if meta == nil || !meta.Completed || meta.LastMsgEventSeq != 3 || meta.EventVersion != 3 {
		t.Fatalf("meta = %#v, want completed version 3", meta)
	}
	if len(meta.Events) != 1 || meta.Events[0].EventKey != metadb.EventKeyDefault {
		t.Fatalf("events = %#v, want only main", meta.Events)
	}
	if hint == nil || hint.ClientMsgNo != "cmn-1" || hint.FromMsgEventSeq != 0 {
		t.Fatalf("hint = %#v, want cmn-1 from 0", hint)
	}
}
```

At the bottom of `internal/usecase/message/event_test.go`, extend the existing `recordingChannelMessageReader` or add a new `recordingEventStore` with fields for `lookups`, `appends`, `states`, `messageHit`, `messageOK`, `appendResult`, and `err`. Its methods must copy byte slices before storing calls.

- [ ] **Step 2: Run failing usecase tests**

Run: `go test ./internal/usecase/message -run 'TestAppendEvent|TestSyncChannelMessages.*Event|TestBuildMessageEventMeta|TestMessageUsecaseImportBoundary'`

Expected: compile errors for append event types and `Options.Events`.

- [ ] **Step 3: Define usecase port and DTOs**

In `internal/usecase/message/ports.go`, add:

```go
type EventStore interface {
	LookupMessage(context.Context, EventMessageLookup) (EventMessage, bool, error)
	AppendMessageEvent(context.Context, EventAppend) (EventAppendResult, error)
	ListMessageEventStatesBatch(context.Context, []EventMessageKey, int) (map[EventMessageKey][]EventState, error)
}
```

In `internal/usecase/message/types.go`, define aliases or structs for `AppendEventCommand`, `AppendEventResult`, `EventAppend`, `EventAppendResult`, `EventMessageLookup`, `EventMessage`, `EventMessageKey`, `EventState`, `MessageEventMeta`, `MessageEventKeyMeta`, and `MessageEventSyncHint`. Use `json.RawMessage` for snapshot values in `MessageEventKeyMeta`.

- [ ] **Step 4: Implement append orchestration**

In `internal/usecase/message/event.go`, implement:

```go
func (a *App) AppendEvent(ctx context.Context, cmd AppendEventCommand) (AppendEventResult, error)
```

Rules:

- trim `FromUID`, `ChannelID`, `ClientMsgNo`, `EventID`, `EventType`, `EventKey`, and `Visibility`.
- require `from_uid`, `channel_id`, `channel_type`, `client_msg_no`, `event_id`, and `event_type`.
- normalize person channel IDs with `runtimechannelid.NormalizePersonChannel(cmd.FromUID, cmd.ChannelID)`.
- default empty `EventKey` to `metadb.EventKeyDefault`; map `stream.finish` to `metadb.EventKeyFinish`.
- default empty `Visibility` to `metadb.VisibilityPublic`.
- default empty `OccurredAt` to `a.now().UTC().UnixMilli()`.
- call `Events.LookupMessage` before append and return `ErrMessageEventTargetNotFound` when it misses.
- call `Events.AppendMessageEvent` with the normalized fields.

- [ ] **Step 5: Implement sync enrichment**

In `internal/usecase/message/sync.go`, after cloning the reader page, call a new helper:

```go
func (a *App) enrichSyncedMessagesWithEvents(ctx context.Context, messages []SyncedMessage, mode string) ([]SyncedMessage, error)
```

Only collect messages with non-empty `ClientMsgNo`. Deduplicate keys by `(channel_id, channel_type, client_msg_no)`. Use `ListMessageEventStatesBatch` with a per-message limit of `64`. If the event store is nil, return messages unchanged.

In `internal/usecase/message/event_summary.go`, implement:

```go
func buildMessageEventMeta(clientMsgNo string, states []EventState, mode string) (*MessageEventMeta, *MessageEventSyncHint, []byte, uint8, uint8, string)
```

Use `full` for an empty mode and `basic` for snapshot omission. Sort event key entries by `LastMsgEventSeq` ascending for stable output.

- [ ] **Step 6: Update errors, options, and docs**

In `internal/usecase/message/app.go`, add `Events EventStore` to `Options` and `events EventStore` to `App`.

In `internal/usecase/message/errors.go`, add:

```go
ErrMessageEventStoreRequired = errors.New("internal/message: message event store required")
ErrMessageEventTargetNotFound = errors.New("message event target message not found")
ErrEventFromUIDRequired = errors.New("from_uid不能为空！")
ErrEventChannelIDRequired = errors.New("channel_id不能为空！")
ErrEventChannelTypeRequired = errors.New("channel_type不能为空！")
ErrEventClientMsgNoRequired = errors.New("client_msg_no不能为空！")
ErrEventIDRequired = errors.New("event_id不能为空！")
ErrEventTypeRequired = errors.New("event_type不能为空！")
```

Update `internal/usecase/message/FLOW.md` with an `AppendEvent Flow` section.

- [ ] **Step 7: Run usecase tests**

Run: `go test ./internal/usecase/message -run 'TestAppendEvent|TestSyncChannelMessages.*Event|TestBuildMessageEventMeta|TestMessageUsecaseImportBoundary'`

Expected: selected tests pass.

- [ ] **Step 8: Commit usecase work**

```bash
git add internal/usecase/message/app.go internal/usecase/message/ports.go internal/usecase/message/errors.go internal/usecase/message/types.go internal/usecase/message/sync.go internal/usecase/message/event.go internal/usecase/message/event_summary.go internal/usecase/message/event_test.go internal/usecase/message/FLOW.md
git commit -m "feat: add message event usecase"
```

## Task 6: HTTP API Route And Legacy Message Sync Mapping

**Files:**
- Modify: `internal/access/api/server.go`
- Modify: `internal/access/api/message_send.go`
- Create: `internal/access/api/message_event.go`
- Create: `internal/access/api/message_event_test.go`
- Modify: `internal/access/api/message_legacy_model.go`
- Modify: `internal/access/api/channel_messagesync.go`
- Modify: `internal/access/api/message_legacy_test.go`
- Modify: `internal/access/api/FLOW.md`

- [ ] **Step 1: Add failing API tests**

Create `internal/access/api/message_event_test.go`:

```go
func TestMessageEventMapsCompatibleRequestToUsecase(t *testing.T) {
	messages := &recordingMessageUsecase{
		eventResult: messageusecase.AppendEventResult{
			ClientMsgNo: "cmn-1", EventKey: "main", EventID: "evt-1",
			MsgEventSeq: 7, StreamStatus: metadb.EventStatusOpen,
			ChannelID: "g1", ChannelType: 2, FromUID: "u1",
		},
	}
	srv := New(Options{Messages: messages})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/message/event", bytes.NewBufferString(`{"from_uid":"u1","channel_id":"g1","channel_type":2,"client_msg_no":"cmn-1","event_id":"evt-1","event_type":"stream.delta","payload":{"kind":"text","delta":"hi"}}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	if !jsonEqual(rec.Body.String(), `{"client_msg_no":"cmn-1","event_key":"main","event_id":"evt-1","msg_event_seq":7,"stream_status":"open","channel_id":"g1","channel_type":2,"from_uid":"u1"}`) {
		t.Fatalf("body = %q, want event append response", rec.Body.String())
	}
	if len(messages.eventCalls) != 1 || string(messages.eventCalls[0].Payload) != `{"kind":"text","delta":"hi"}` {
		t.Fatalf("event calls = %#v, want one raw JSON payload call", messages.eventCalls)
	}
}
```

Also add:

```go
func TestMessageEventReturnsCompatibleErrors(t *testing.T) {
	for _, tt := range []struct {
		name     string
		messages MessageUsecase
		body     string
		status   int
		want     string
	}{
		{name: "invalid json", body: `{"from_uid":`, status: http.StatusBadRequest, want: `{"error":"invalid request"}`},
		{name: "missing usecase", body: `{"from_uid":"u1","channel_id":"g1","channel_type":2,"client_msg_no":"cmn-1","event_id":"evt-1","event_type":"stream.delta"}`, status: http.StatusInternalServerError, want: `{"error":"message usecase not configured"}`},
		{name: "missing from uid", messages: &recordingMessageUsecase{eventErr: messageusecase.ErrEventFromUIDRequired}, body: `{"channel_id":"g1","channel_type":2,"client_msg_no":"cmn-1","event_id":"evt-1","event_type":"stream.delta"}`, status: http.StatusBadRequest, want: `{"msg":"from_uid不能为空！","status":400}`},
		{name: "missing target", messages: &recordingMessageUsecase{eventErr: messageusecase.ErrMessageEventTargetNotFound}, body: `{"from_uid":"u1","channel_id":"g1","channel_type":2,"client_msg_no":"cmn-1","event_id":"evt-1","event_type":"stream.delta"}`, status: http.StatusBadRequest, want: `{"msg":"message event target message not found","status":400}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := New(Options{Messages: tt.messages})
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/message/event", bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")
			srv.Handler().ServeHTTP(rec, req)
			if rec.Code != tt.status {
				t.Fatalf("status = %d body = %s, want %d", rec.Code, rec.Body.String(), tt.status)
			}
			if !jsonEqual(rec.Body.String(), tt.want) {
				t.Fatalf("body = %q, want %s", rec.Body.String(), tt.want)
			}
		})
	}
}

func TestChannelMessageSyncIncludesEventFields(t *testing.T) {
	messages := &recordingMessageUsecase{
		syncResult: messageusecase.SyncChannelMessagesResult{
			Messages: []messageusecase.SyncedMessage{{
				MessageID: 88, MessageSeq: 2, ClientMsgNo: "cmn-1",
				FromUID: "u1", ChannelID: "g1", ChannelType: 2,
				EventMeta: &messageusecase.MessageEventMeta{HasEvents: true, LastMsgEventSeq: 3},
				EventSyncHint: &messageusecase.MessageEventSyncHint{ClientMsgNo: "cmn-1", FromMsgEventSeq: 0},
				StreamData: []byte("hello"), End: 1, EndReason: 2, Error: "timeout",
			}},
		},
	}
	srv := New(Options{Messages: messages})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/channel/messagesync", bytes.NewBufferString(`{"login_uid":"u1","channel_id":"g1","channel_type":2,"limit":10}`))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"event_meta"`) || !strings.Contains(rec.Body.String(), `"event_sync_hint"`) || !strings.Contains(rec.Body.String(), `"end":1`) {
		t.Fatalf("body = %s, want event fields", rec.Body.String())
	}
}
```

Update the `recordingMessageUsecase` test double with `eventCalls []messageusecase.AppendEventCommand`, `eventResult messageusecase.AppendEventResult`, and `eventErr error`.

- [ ] **Step 2: Run failing API tests**

Run: `go test ./internal/access/api -run 'TestMessageEvent|TestChannelMessageSync'`

Expected: `/message/event` returns 404 or compile errors for interface changes.

- [ ] **Step 3: Extend API message interface and register route**

In `internal/access/api/server.go`, add to `MessageUsecase`:

```go
AppendEvent(context.Context, messageusecase.AppendEventCommand) (messageusecase.AppendEventResult, error)
```

In `internal/access/api/message_send.go`, register:

```go
s.engine.POST("/message/event", s.handleMessageEvent)
```

- [ ] **Step 4: Implement message event handler**

In `internal/access/api/message_event.go`, define request and response structs:

```go
type messageEventRequest struct {
	ChannelID   string          `json:"channel_id"`
	ChannelType uint8           `json:"channel_type"`
	FromUID     string          `json:"from_uid"`
	ClientMsgNo string          `json:"client_msg_no"`
	EventID     string          `json:"event_id"`
	EventType   string          `json:"event_type"`
	EventKey    string          `json:"event_key"`
	Visibility  string          `json:"visibility"`
	OccurredAt  int64           `json:"occurred_at"`
	Payload     json.RawMessage `json:"payload"`
	Headers     map[string]string `json:"headers"`
}
```

Return the response fields from the approved design. Use `writeJSONError` for usecase errors and `writeSendJSONError` only for malformed JSON or missing usecase.

- [ ] **Step 5: Map event fields in legacy message response**

In `internal/access/api/message_legacy_model.go`, copy these fields from `messageusecase.SyncedMessage`:

```go
End: msg.End,
EndReason: msg.EndReason,
Error: msg.Error,
StreamData: cloneLegacyBytes(msg.StreamData),
EventMeta: msg.EventMeta,
EventSyncHint: msg.EventSyncHint,
```

Add this helper in the same file:

```go
func cloneLegacyBytes(in []byte) []byte {
	if len(in) == 0 {
		return nil
	}
	out := make([]byte, len(in))
	copy(out, in)
	return out
}
```

Update `internal/access/api/message_legacy_test.go` recording fake to implement `AppendEvent`.

- [ ] **Step 6: Update API docs**

In `internal/access/api/FLOW.md`, add `POST /message/event` to the compatible message routes and state that `/message/eventsync` is not registered in this stage.

- [ ] **Step 7: Run API tests**

Run: `go test ./internal/access/api -run 'TestMessageEvent|TestChannelMessageSync|TestSendMessage'`

Expected: selected tests pass.

- [ ] **Step 8: Commit API work**

```bash
git add internal/access/api/server.go internal/access/api/message_send.go internal/access/api/message_event.go internal/access/api/message_event_test.go internal/access/api/message_legacy_model.go internal/access/api/channel_messagesync.go internal/access/api/message_legacy_test.go internal/access/api/FLOW.md
git commit -m "feat: expose message event api"
```

## Task 7: App Wiring, Flow Docs, And Final Verification

**Files:**
- Modify: `internal/app/wiring.go`
- Modify: `internal/app/app_test.go`
- Modify: `internal/FLOW.md`
- Modify: `internal/app/FLOW.md`

- [ ] **Step 1: Add failing wiring test**

In `internal/app/app_test.go`, add:

```go
func TestNewWiresMessageEventStoreWhenClusterSupportsIt(t *testing.T) {
	cluster := newFakePresenceCluster(1, nil)
	cluster.snapshot = readyFakeClusterSnapshot(1, 16)
	app, err := newTestApp(t, Config{Cluster: clusterpkg.Config{NodeID: 1}}, WithCluster(cluster))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if app.Messages() == nil {
		t.Fatal("message usecase was not wired")
	}
	_, err = app.Messages().AppendEvent(context.Background(), message.AppendEventCommand{
		FromUID: "u1", ChannelID: "g1", ChannelType: 2,
		ClientMsgNo: "cmn-1", EventID: "evt-1", EventType: metadb.EventTypeStreamDelta,
	})
	if !errors.Is(err, message.ErrMessageEventTargetNotFound) {
		t.Fatalf("AppendEvent() error = %v, want target-not-found from wired event store", err)
	}
}
```

Extend the fake cluster with `LookupChannelIdempotency`, `AppendMessageEvent`, and `GetMessageEventStatesBatch` methods.

- [ ] **Step 2: Run failing wiring test**

Run: `go test ./internal/app -run TestNewWiresMessageEventStoreWhenClusterSupportsIt`

Expected: `AppendEvent` returns `ErrMessageEventStoreRequired`.

- [ ] **Step 3: Wire event store**

In `internal/app/wiring.go`, inside `wireMessages`, after reader wiring:

```go
if eventNode, ok := a.cluster.(clusterinfra.MessageEventNode); ok {
	messageOpts.Events = clusterinfra.NewMessageEventStore(eventNode)
}
```

- [ ] **Step 4: Update flow docs**

Update `internal/FLOW.md` with the new route:

```text
POST /message/event
  -> access/api
  -> usecase/message AppendEvent
  -> infra/cluster MessageEventStore
  -> pkg/cluster AppendMessageEvent
  -> pkg/slot/fsm append message event command
  -> pkg/db/meta message_event_state/message_event_cursor
```

In `internal/app/FLOW.md`, add the event store injection to the message wiring description.

- [ ] **Step 5: Run focused verification**

Run:

```bash
go test ./pkg/db/meta ./pkg/slot/fsm ./pkg/cluster/propose ./pkg/cluster ./internal/usecase/message ./internal/infra/cluster ./internal/access/api ./internal/app
```

Expected: all packages pass.

- [ ] **Step 6: Check formatting and file consistency**

Run:

```bash
gofmt -w pkg/db/meta/table_message_event.go pkg/db/meta/message_event_test.go pkg/cluster/propose/types.go pkg/cluster/propose/service.go pkg/cluster/propose/forward.go pkg/cluster/default_slot_proposer.go pkg/cluster/api.go pkg/cluster/node.go pkg/slot/fsm/command.go pkg/slot/fsm/state_machine_test.go pkg/cluster/node_meta.go pkg/cluster/node_meta_message_event_test.go internal/infra/cluster/message_event.go internal/infra/cluster/message_event_test.go internal/usecase/message/app.go internal/usecase/message/ports.go internal/usecase/message/errors.go internal/usecase/message/types.go internal/usecase/message/sync.go internal/usecase/message/event.go internal/usecase/message/event_summary.go internal/usecase/message/event_test.go internal/access/api/server.go internal/access/api/message_send.go internal/access/api/message_event.go internal/access/api/message_event_test.go internal/access/api/message_legacy_model.go internal/access/api/message_legacy_test.go internal/app/wiring.go internal/app/app_test.go
git diff --check
```

Expected: `git diff --check` prints no output.

- [ ] **Step 7: Confirm `/message/eventsync` is absent**

Run:

```bash
rg -n 'eventsync|handleMessageEventSync|/message/eventsync' internal pkg
```

Expected: no route registration or handler in promoted code. Design docs may mention it as out of scope.

- [ ] **Step 8: Commit wiring and docs**

```bash
git add internal/app/wiring.go internal/app/app_test.go internal/FLOW.md internal/app/FLOW.md
git commit -m "chore: wire message event projection"
```

## Final Verification

Run this before reporting completion:

```bash
go test ./pkg/db/meta ./pkg/slot/fsm ./pkg/cluster/propose ./pkg/cluster ./internal/usecase/message ./internal/infra/cluster ./internal/access/api ./internal/app
```

Expected: all listed packages pass.

Then run:

```bash
git status --short
```

Expected: only intentional untracked local drafts remain. Preserve unrelated untracked files.
