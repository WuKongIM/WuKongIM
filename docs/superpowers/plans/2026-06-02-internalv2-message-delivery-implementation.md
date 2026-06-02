# internalv2 Message Delivery Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build an independent internalv2 delivery module using UID-authority partitioned fanout and recipient-owner local recvack.

**Architecture:** Durable append still produces `MessageCommitted`; delivery consumes that event asynchronously. Fanout tasks are partitioned by UID authority target, push handoff is batched by recipient owner node, and client recvack is tracked locally on the recipient owner. Runtime packages stay entry- and cluster-agnostic so they can be benchmarked without gateway or cluster startup.

**Tech Stack:** Go 1.23, internalv2 usecase/runtime layering, clusterv2 node RPC, pkg/gateway sessions, pkg/protocol frame DTOs, `go test` benchmarks.

---

## Scope Check

This plan implements the P1 scope from `docs/superpowers/specs/2026-06-02-internalv2-message-delivery-design.md`.

It intentionally excludes durable delivery replay, offline sync writes, CMD sync, plugin hooks, full `NoPersist`, and guaranteed online arrival ordering.

## File Structure

Create delivery usecase files:

- `internalv2/usecase/delivery/FLOW.md`: package responsibility and flow.
- `internalv2/usecase/delivery/app.go`: options and app construction.
- `internalv2/usecase/delivery/ports.go`: runtime and observer ports.
- `internalv2/usecase/delivery/types.go`: entry-agnostic commands and results.
- `internalv2/usecase/delivery/submit.go`: committed, recvack, and session-close forwarding.
- `internalv2/usecase/delivery/import_boundary_test.go`: import boundary.
- `internalv2/usecase/delivery/app_test.go`: usecase forwarding and clone tests.

Create delivery runtime files:

- `internalv2/runtime/delivery/FLOW.md`: runtime flow and benchmark boundary.
- `internalv2/runtime/delivery/types.go`: envelope, partition, route, push DTOs.
- `internalv2/runtime/delivery/ack_tracker.go`: recipient-owner local pending recvack tracker.
- `internalv2/runtime/delivery/ack_tracker_test.go`: ack tracker tests.
- `internalv2/runtime/delivery/planner.go`: committed event partition planning and ephemeral subscriber plan.
- `internalv2/runtime/delivery/planner_test.go`: partition planning tests.
- `internalv2/runtime/delivery/fanout_worker.go`: subscriber page, presence resolution, and push handoff.
- `internalv2/runtime/delivery/fanout_worker_test.go`: fanout worker tests.
- `internalv2/runtime/delivery/manager.go`: runtime facade used by the usecase.
- `internalv2/runtime/delivery/manager_test.go`: end-to-end no-cluster runtime tests.
- `internalv2/runtime/delivery/benchmark_test.go`: required delivery-only benchmarks.

Modify existing event and message files:

- `internalv2/contracts/messageevents/event.go`: add delivery fields and `Clone`.
- `internalv2/contracts/messageevents/event_test.go`: add clone tests.
- `internalv2/usecase/message/send.go`: include sender session and delivery flags in committed event.
- `internalv2/usecase/message/send_test.go`: verify committed delivery fields.

Modify gateway and app files:

- `internalv2/access/gateway/handler.go`: handle `RecvackPacket`.
- `internalv2/access/gateway/FLOW.md`: document recvack flow.
- `internalv2/access/gateway/handler_test.go`: recvack tests.
- `internalv2/app/config.go`: add delivery config with English field comments.
- `internalv2/app/app.go`: wire delivery runtime and usecase.
- `internalv2/app/lifecycle.go`: start and stop delivery worker runtime if needed.
- `internalv2/app/FLOW.md`: document delivery wiring.
- `internalv2/app/app_test.go`: wiring tests.

Modify node RPC and cluster adapter files:

- `pkg/clusterv2/net/ids.go`: add delivery RPC service ID.
- `pkg/clusterv2/net/ids_test.go`: assert service ID uniqueness.
- `internalv2/access/node/delivery_codec.go`: deterministic delivery RPC codec.
- `internalv2/access/node/delivery_rpc.go`: delivery node adapter and client.
- `internalv2/access/node/delivery_codec_test.go`: codec tests.
- `internalv2/access/node/delivery_rpc_test.go`: adapter and client tests.
- `internalv2/infra/cluster/delivery.go`: task router and owner push adapters.
- `internalv2/infra/cluster/delivery_test.go`: local and remote routing tests.

---

### Task 1: Extend Committed Event Contract

**Files:**
- Modify: `internalv2/contracts/messageevents/event.go`
- Create: `internalv2/contracts/messageevents/event_test.go`
- Modify: `internalv2/usecase/message/send.go`
- Modify: `internalv2/usecase/message/send_test.go`

- [ ] **Step 1: Write the committed event clone test**

Add `internalv2/contracts/messageevents/event_test.go`:

```go
package messageevents

import "testing"

func TestMessageCommittedCloneDeepCopiesDeliveryFields(t *testing.T) {
	event := MessageCommitted{
		MessageID:        100,
		MessageSeq:       7,
		ChannelID:        "g1",
		ChannelType:      2,
		FromUID:          "u1",
		SenderSessionID:  11,
		ClientMsgNo:      "c1",
		RedDot:           true,
		Payload:          []byte("payload"),
		MessageScopedUIDs: []string{"u2", "u3"},
	}

	cloned := event.Clone()
	event.Payload[0] = 'P'
	event.MessageScopedUIDs[0] = "changed"

	if string(cloned.Payload) != "payload" {
		t.Fatalf("cloned payload = %q, want payload", string(cloned.Payload))
	}
	if got := cloned.MessageScopedUIDs[0]; got != "u2" {
		t.Fatalf("cloned scoped uid[0] = %q, want u2", got)
	}
	if cloned.SenderSessionID != 11 || !cloned.RedDot {
		t.Fatalf("clone lost delivery fields: %#v", cloned)
	}
}
```

- [ ] **Step 2: Run the event clone test and verify it fails**

Run:

```bash
go test ./internalv2/contracts/messageevents -run TestMessageCommittedCloneDeepCopiesDeliveryFields
```

Expected: FAIL with missing fields or missing `Clone`.

- [ ] **Step 3: Add delivery fields and Clone**

Modify `internalv2/contracts/messageevents/event.go`:

```go
package messageevents

// MessageCommitted is emitted after a durable channel append succeeds.
type MessageCommitted struct {
	// MessageID is the globally unique durable message identifier.
	MessageID uint64
	// MessageSeq is the committed channel sequence assigned by channelv2.
	MessageSeq uint64
	// ChannelID is the client-visible channel identifier.
	ChannelID string
	// ChannelType is the protocol channel category.
	ChannelType uint8
	// FromUID is the sender user id.
	FromUID string
	// SenderSessionID is the owner-local sender session id used to skip echo to the same connection.
	SenderSessionID uint64
	// ClientMsgNo is the client idempotency key.
	ClientMsgNo string
	// RedDot carries the client red-dot flag for delivery side effects.
	RedDot bool
	// Payload is a copy of the committed payload.
	Payload []byte
	// MessageScopedUIDs contains one-shot delivery recipients for request-scoped messages.
	MessageScopedUIDs []string
}

// Clone returns a deep copy safe for asynchronous fanout.
func (e MessageCommitted) Clone() MessageCommitted {
	e.Payload = append([]byte(nil), e.Payload...)
	e.MessageScopedUIDs = append([]string(nil), e.MessageScopedUIDs...)
	return e
}
```

- [ ] **Step 4: Run the event clone test and verify it passes**

Run:

```bash
go test ./internalv2/contracts/messageevents -run TestMessageCommittedCloneDeepCopiesDeliveryFields
```

Expected: PASS.

- [ ] **Step 5: Write the message committed field test**

In `internalv2/usecase/message/send_test.go`, add a recording sink and test near existing committed tests:

```go
func TestSubmitCommittedIncludesDeliveryFields(t *testing.T) {
	committed := &capturingCommitted{}
	app := New(Options{
		Appender:  &recordingAppender{},
		MessageID: &sequenceIDs{next: 100},
		Committed: committed,
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:         "u1",
		SenderSessionID: 42,
		ClientMsgNo:     "client-1",
		ChannelID:       "g1",
		ChannelType:     2,
		Payload:         []byte("hello"),
		RedDot:          true,
	})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if result.Reason != ReasonSuccess {
		t.Fatalf("Send() reason = %v, want success", result.Reason)
	}
	if len(committed.events) != 1 {
		t.Fatalf("committed events = %d, want 1", len(committed.events))
	}
	event := committed.events[0]
	if event.SenderSessionID != 42 || !event.RedDot {
		t.Fatalf("event delivery fields = %#v, want sender session and red dot", event)
	}
	if event.ClientMsgNo != "client-1" || event.FromUID != "u1" {
		t.Fatalf("event identity fields = %#v", event)
	}
}

type capturingCommitted struct {
	events []messageevents.MessageCommitted
}

func (c *capturingCommitted) Submit(_ context.Context, event messageevents.MessageCommitted) error {
	c.events = append(c.events, event.Clone())
	return nil
}
```

- [ ] **Step 6: Run the message committed field test and verify it fails**

Run:

```bash
go test ./internalv2/usecase/message -run TestSubmitCommittedIncludesDeliveryFields
```

Expected: FAIL because `submitCommitted` does not populate new fields.

- [ ] **Step 7: Populate delivery fields in submitCommitted**

Modify `internalv2/usecase/message/send.go` inside `submitCommitted`:

```go
event := messageevents.MessageCommitted{
	MessageID:       appended.MessageID,
	MessageSeq:      appended.MessageSeq,
	ChannelID:       cmd.ChannelID,
	ChannelType:     cmd.ChannelType,
	FromUID:         cmd.FromUID,
	SenderSessionID: cmd.SenderSessionID,
	ClientMsgNo:     cmd.ClientMsgNo,
	RedDot:          cmd.RedDot,
	Payload:         cloneBytes(appended.Message.Payload),
}
```

- [ ] **Step 8: Run focused tests**

Run:

```bash
go test ./internalv2/contracts/messageevents ./internalv2/usecase/message -run 'Test(MessageCommittedCloneDeepCopiesDeliveryFields|SubmitCommittedIncludesDeliveryFields|CommittedSinkErrorDoesNotChangeSendResult)'
```

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internalv2/contracts/messageevents/event.go internalv2/contracts/messageevents/event_test.go internalv2/usecase/message/send.go internalv2/usecase/message/send_test.go
git commit -m "Add internalv2 committed delivery fields"
```

---

### Task 2: Add Delivery Usecase Boundary

**Files:**
- Create: `internalv2/usecase/delivery/FLOW.md`
- Create: `internalv2/usecase/delivery/app.go`
- Create: `internalv2/usecase/delivery/ports.go`
- Create: `internalv2/usecase/delivery/types.go`
- Create: `internalv2/usecase/delivery/submit.go`
- Create: `internalv2/usecase/delivery/import_boundary_test.go`
- Create: `internalv2/usecase/delivery/app_test.go`

- [ ] **Step 1: Write FLOW**

Create `internalv2/usecase/delivery/FLOW.md`:

```markdown
# internalv2/usecase/delivery Flow

## Responsibility

`internalv2/usecase/delivery` owns entry-agnostic online delivery orchestration
boundaries. It accepts committed message events and forwards delivery feedback
commands to a runtime port.

The usecase does not import gateway frames, access adapters, app composition,
or concrete cluster runtimes.

## Flow

```text
MessageCommitted
  -> delivery.App.SubmitCommitted
  -> runtime.SubmitCommitted

RecvackCommand
  -> delivery.App.Recvack
  -> runtime.Recvack

SessionClosedCommand
  -> delivery.App.SessionClosed
  -> runtime.SessionClosed
```
```

- [ ] **Step 2: Write failing usecase tests**

Create `internalv2/usecase/delivery/app_test.go`:

```go
package delivery

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/messageevents"
)

func TestSubmitCommittedClonesEventBeforeRuntime(t *testing.T) {
	runtime := &recordingRuntime{}
	app := New(Options{Runtime: runtime})
	event := messageevents.MessageCommitted{
		MessageID:        1,
		ChannelID:        "g1",
		ChannelType:      2,
		Payload:          []byte("hello"),
		MessageScopedUIDs: []string{"u1"},
	}

	if err := app.SubmitCommitted(context.Background(), event); err != nil {
		t.Fatalf("SubmitCommitted() error = %v", err)
	}
	event.Payload[0] = 'H'
	event.MessageScopedUIDs[0] = "changed"

	if got := string(runtime.events[0].Payload); got != "hello" {
		t.Fatalf("runtime payload = %q, want hello", got)
	}
	if got := runtime.events[0].MessageScopedUIDs[0]; got != "u1" {
		t.Fatalf("runtime scoped uid = %q, want u1", got)
	}
}

func TestFeedbackCommandsForwardToRuntime(t *testing.T) {
	runtime := &recordingRuntime{}
	app := New(Options{Runtime: runtime})

	if err := app.Recvack(context.Background(), RecvackCommand{UID: "u1", SessionID: 7, MessageID: 10, MessageSeq: 11}); err != nil {
		t.Fatalf("Recvack() error = %v", err)
	}
	if err := app.SessionClosed(context.Background(), SessionClosedCommand{UID: "u1", SessionID: 7}); err != nil {
		t.Fatalf("SessionClosed() error = %v", err)
	}

	if runtime.recvacks != 1 || runtime.closes != 1 {
		t.Fatalf("runtime feedback counts recvacks=%d closes=%d", runtime.recvacks, runtime.closes)
	}
}

type recordingRuntime struct {
	events   []messageevents.MessageCommitted
	recvacks int
	closes   int
}

func (r *recordingRuntime) SubmitCommitted(_ context.Context, event messageevents.MessageCommitted) error {
	r.events = append(r.events, event)
	return nil
}

func (r *recordingRuntime) Recvack(context.Context, RecvackCommand) error {
	r.recvacks++
	return nil
}

func (r *recordingRuntime) SessionClosed(context.Context, SessionClosedCommand) error {
	r.closes++
	return nil
}
```

- [ ] **Step 3: Run usecase tests and verify they fail**

Run:

```bash
go test ./internalv2/usecase/delivery
```

Expected: FAIL because the package files do not exist yet.

- [ ] **Step 4: Add delivery usecase types and ports**

Create `internalv2/usecase/delivery/types.go`:

```go
package delivery

// RecvackCommand records one client receive acknowledgement observed by the recipient owner.
type RecvackCommand struct {
	// UID is the authenticated recipient user id.
	UID string
	// SessionID is the recipient owner-local gateway session id.
	SessionID uint64
	// MessageID is the durable message id being acknowledged.
	MessageID uint64
	// MessageSeq is the durable channel sequence acknowledged by the client.
	MessageSeq uint64
}

// SessionClosedCommand removes owner-local pending delivery state for one session.
type SessionClosedCommand struct {
	// UID is the authenticated user id for the closed session.
	UID string
	// SessionID is the recipient owner-local gateway session id.
	SessionID uint64
}
```

Create `internalv2/usecase/delivery/ports.go`:

```go
package delivery

import (
	"context"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/messageevents"
	runtimedelivery "github.com/WuKongIM/WuKongIM/internalv2/runtime/delivery"
)

// Runtime owns delivery fanout and recipient-owner acknowledgement state.
type Runtime interface {
	SubmitCommitted(context.Context, messageevents.MessageCommitted) error
	Recvack(context.Context, runtimedelivery.Recvack) error
	SessionClosed(context.Context, runtimedelivery.SessionClosed) error
}
```

- [ ] **Step 5: Add delivery App**

Create `internalv2/usecase/delivery/app.go`:

```go
package delivery

// Options configures the delivery usecase.
type Options struct {
	// Runtime owns fanout and recipient-owner acknowledgement state.
	Runtime Runtime
}

// App forwards entry-agnostic delivery commands to the configured runtime.
type App struct {
	runtime Runtime
}

// New creates a delivery App.
func New(opts Options) *App {
	return &App{runtime: opts.Runtime}
}
```

Create `internalv2/usecase/delivery/submit.go`:

```go
package delivery

import (
	"context"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/messageevents"
	runtimedelivery "github.com/WuKongIM/WuKongIM/internalv2/runtime/delivery"
)

// SubmitCommitted submits one durable message event for online delivery.
func (a *App) SubmitCommitted(ctx context.Context, event messageevents.MessageCommitted) error {
	if a == nil || a.runtime == nil {
		return nil
	}
	return a.runtime.SubmitCommitted(ctx, event.Clone())
}

// Recvack records one client receive acknowledgement on the recipient owner.
func (a *App) Recvack(ctx context.Context, cmd RecvackCommand) error {
	if a == nil || a.runtime == nil {
		return nil
	}
	return a.runtime.Recvack(ctx, runtimedelivery.Recvack{
		UID:        cmd.UID,
		SessionID:  cmd.SessionID,
		MessageID:  cmd.MessageID,
		MessageSeq: cmd.MessageSeq,
	})
}

// SessionClosed removes pending delivery state for one local gateway session.
func (a *App) SessionClosed(ctx context.Context, cmd SessionClosedCommand) error {
	if a == nil || a.runtime == nil {
		return nil
	}
	return a.runtime.SessionClosed(ctx, runtimedelivery.SessionClosed{
		UID:       cmd.UID,
		SessionID: cmd.SessionID,
	})
}
```

- [ ] **Step 6: Add import boundary test**

Create `internalv2/usecase/delivery/import_boundary_test.go`:

```go
package delivery

import (
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

func TestDeliveryUsecaseImportBoundary(t *testing.T) {
	forbidden := []string{
		"github.com/WuKongIM/WuKongIM/pkg/gateway",
		"github.com/WuKongIM/WuKongIM/pkg/protocol/frame",
		"github.com/WuKongIM/WuKongIM/pkg/clusterv2",
		"github.com/WuKongIM/WuKongIM/pkg/channelv2",
		"github.com/WuKongIM/WuKongIM/internalv2/access",
		"github.com/WuKongIM/WuKongIM/internalv2/app",
	}
	files, err := parser.ParseDir(token.NewFileSet(), ".", func(info os.FileInfo) bool {
		name := info.Name()
		return strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")
	}, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("ParseDir() error = %v", err)
	}
	for _, pkg := range files {
		for filename, file := range pkg.Files {
			for _, imp := range file.Imports {
				path := strings.Trim(imp.Path.Value, `"`)
				for _, bad := range forbidden {
					if path == bad || strings.HasPrefix(path, bad+"/") {
						t.Fatalf("%s imports forbidden package %q", filename, path)
					}
				}
			}
		}
	}
}
```

- [ ] **Step 7: Run delivery usecase tests**

Run:

```bash
go test ./internalv2/usecase/delivery
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internalv2/usecase/delivery
git commit -m "Add internalv2 delivery usecase boundary"
```

---

### Task 3: Add Recipient Ack Tracker Runtime

**Files:**
- Create: `internalv2/runtime/delivery/FLOW.md`
- Create: `internalv2/runtime/delivery/types.go`
- Create: `internalv2/runtime/delivery/ack_tracker.go`
- Create: `internalv2/runtime/delivery/ack_tracker_test.go`

- [ ] **Step 1: Write ack tracker tests**

Create `internalv2/runtime/delivery/ack_tracker_test.go`:

```go
package delivery

import (
	"testing"
	"time"
)

func TestAckTrackerAckClearsPending(t *testing.T) {
	tracker := NewAckTracker(AckTrackerOptions{ShardCount: 4, Now: fixedUnix(100)})
	tracker.Bind(PendingRecvAck{UID: "u1", SessionID: 7, MessageID: 10, MessageSeq: 11, ChannelID: "g1", ChannelType: 2})

	acked, ok := tracker.Ack(Recvack{UID: "u1", SessionID: 7, MessageID: 10, MessageSeq: 11})
	if !ok {
		t.Fatal("Ack() ok=false, want true")
	}
	if acked.MessageID != 10 || acked.DeliveredAt == 0 {
		t.Fatalf("acked = %#v, want message and delivered time", acked)
	}
	if tracker.PendingCount() != 0 {
		t.Fatalf("pending count = %d, want 0", tracker.PendingCount())
	}
}

func TestAckTrackerSessionClosedClearsOnlyThatSession(t *testing.T) {
	tracker := NewAckTracker(AckTrackerOptions{ShardCount: 4, Now: fixedUnix(100)})
	tracker.Bind(PendingRecvAck{UID: "u1", SessionID: 7, MessageID: 10})
	tracker.Bind(PendingRecvAck{UID: "u1", SessionID: 8, MessageID: 11})

	removed := tracker.SessionClosed("u1", 7)
	if len(removed) != 1 || removed[0].MessageID != 10 {
		t.Fatalf("removed = %#v, want message 10", removed)
	}
	if tracker.PendingCount() != 1 {
		t.Fatalf("pending count = %d, want 1", tracker.PendingCount())
	}
}

func TestAckTrackerExpireRemovesOldPending(t *testing.T) {
	now := int64(100)
	tracker := NewAckTracker(AckTrackerOptions{ShardCount: 2, Now: func() int64 { return now }})
	tracker.Bind(PendingRecvAck{UID: "u1", SessionID: 7, MessageID: 10})

	now = 200
	expired := tracker.Expire(50 * time.Second)
	if len(expired) != 1 || expired[0].MessageID != 10 {
		t.Fatalf("expired = %#v, want message 10", expired)
	}
}

func fixedUnix(value int64) func() int64 {
	return func() int64 { return value }
}
```

- [ ] **Step 2: Run ack tracker tests and verify they fail**

Run:

```bash
go test ./internalv2/runtime/delivery -run TestAckTracker
```

Expected: FAIL because runtime package files do not exist yet.

- [ ] **Step 3: Add runtime FLOW and types**

Create `internalv2/runtime/delivery/FLOW.md`:

```markdown
# internalv2/runtime/delivery Flow

## Responsibility

`internalv2/runtime/delivery` owns online delivery fanout primitives and
recipient-owner recvack state. It is independent from gateway, app, and concrete
cluster runtimes so it can be benchmarked in isolation.

## Flow

```text
MessageCommitted
  -> Manager.SubmitCommitted
  -> Planner creates fanout tasks
  -> FanoutWorker resolves presence and pushes owner batches

Push accepted by recipient owner
  -> AckTracker.Bind
  -> client Recvack
  -> AckTracker.Ack local
```
```

Create `internalv2/runtime/delivery/types.go`:

```go
package delivery

// PendingRecvAck records one message handed to a local recipient session.
type PendingRecvAck struct {
	// UID is the recipient user id.
	UID string
	// SessionID is the recipient owner-local gateway session id.
	SessionID uint64
	// MessageID is the durable message identifier.
	MessageID uint64
	// MessageSeq is the durable channel sequence.
	MessageSeq uint64
	// ChannelID is the client-visible channel id.
	ChannelID string
	// ChannelType is the protocol channel category.
	ChannelType uint8
	// DeliveredAt records when the owner accepted the local write.
	DeliveredAt int64
}

// Recvack identifies one client acknowledgement.
type Recvack struct {
	UID        string
	SessionID  uint64
	MessageID  uint64
	MessageSeq uint64
}

// SessionClosed identifies one owner-local session close event.
type SessionClosed struct {
	UID       string
	SessionID uint64
}
```

- [ ] **Step 4: Add AckTracker implementation**

Create `internalv2/runtime/delivery/ack_tracker.go`:

```go
package delivery

import (
	"sync"
	"time"
)

const defaultAckTrackerShards = 32

// AckTrackerOptions configures recipient-owner pending recvack tracking.
type AckTrackerOptions struct {
	// ShardCount spreads pending ack maps across independent locks.
	ShardCount int
	// Now returns unix seconds for deterministic tests.
	Now func() int64
}

// AckTracker stores pending recvacks for owner-local sessions.
type AckTracker struct {
	shards []ackShard
	now    func() int64
}

type ackShard struct {
	mu        sync.Mutex
	byMessage map[ackKey]PendingRecvAck
	bySession map[sessionKey]map[ackKey]struct{}
}

type ackKey struct {
	uid       string
	sessionID uint64
	messageID uint64
}

type sessionKey struct {
	uid       string
	sessionID uint64
}

// NewAckTracker creates a sharded pending recvack tracker.
func NewAckTracker(opts AckTrackerOptions) *AckTracker {
	shards := opts.ShardCount
	if shards <= 0 {
		shards = defaultAckTrackerShards
	}
	now := opts.Now
	if now == nil {
		now = func() int64 { return time.Now().Unix() }
	}
	tracker := &AckTracker{shards: make([]ackShard, shards), now: now}
	for i := range tracker.shards {
		tracker.shards[i].byMessage = make(map[ackKey]PendingRecvAck)
		tracker.shards[i].bySession = make(map[sessionKey]map[ackKey]struct{})
	}
	return tracker
}

// Bind records a message handed to one local session.
func (t *AckTracker) Bind(pending PendingRecvAck) {
	if t == nil || pending.UID == "" || pending.SessionID == 0 || pending.MessageID == 0 {
		return
	}
	if pending.DeliveredAt == 0 {
		pending.DeliveredAt = t.now()
	}
	key := ackKey{uid: pending.UID, sessionID: pending.SessionID, messageID: pending.MessageID}
	session := sessionKey{uid: pending.UID, sessionID: pending.SessionID}
	shard := t.shard(pending.SessionID)
	shard.mu.Lock()
	shard.byMessage[key] = pending
	keys := shard.bySession[session]
	if keys == nil {
		keys = make(map[ackKey]struct{})
		shard.bySession[session] = keys
	}
	keys[key] = struct{}{}
	shard.mu.Unlock()
}

// Ack removes and returns one pending acknowledgement.
func (t *AckTracker) Ack(cmd Recvack) (PendingRecvAck, bool) {
	if t == nil {
		return PendingRecvAck{}, false
	}
	key := ackKey{uid: cmd.UID, sessionID: cmd.SessionID, messageID: cmd.MessageID}
	shard := t.shard(cmd.SessionID)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	pending, ok := shard.byMessage[key]
	if !ok {
		return PendingRecvAck{}, false
	}
	delete(shard.byMessage, key)
	session := sessionKey{uid: cmd.UID, sessionID: cmd.SessionID}
	if keys := shard.bySession[session]; keys != nil {
		delete(keys, key)
		if len(keys) == 0 {
			delete(shard.bySession, session)
		}
	}
	return pending, true
}

// SessionClosed removes pending acknowledgements for one owner-local session.
func (t *AckTracker) SessionClosed(uid string, sessionID uint64) []PendingRecvAck {
	if t == nil {
		return nil
	}
	shard := t.shard(sessionID)
	session := sessionKey{uid: uid, sessionID: sessionID}
	shard.mu.Lock()
	defer shard.mu.Unlock()
	keys := shard.bySession[session]
	if len(keys) == 0 {
		return nil
	}
	out := make([]PendingRecvAck, 0, len(keys))
	for key := range keys {
		if pending, ok := shard.byMessage[key]; ok {
			out = append(out, pending)
			delete(shard.byMessage, key)
		}
	}
	delete(shard.bySession, session)
	return out
}

// Expire removes pending acks older than ttl.
func (t *AckTracker) Expire(ttl time.Duration) []PendingRecvAck {
	if t == nil || ttl <= 0 {
		return nil
	}
	cutoff := t.now() - int64(ttl/time.Second)
	var out []PendingRecvAck
	for i := range t.shards {
		shard := &t.shards[i]
		shard.mu.Lock()
		for key, pending := range shard.byMessage {
			if pending.DeliveredAt > cutoff {
				continue
			}
			out = append(out, pending)
			delete(shard.byMessage, key)
			session := sessionKey{uid: key.uid, sessionID: key.sessionID}
			if keys := shard.bySession[session]; keys != nil {
				delete(keys, key)
				if len(keys) == 0 {
					delete(shard.bySession, session)
				}
			}
		}
		shard.mu.Unlock()
	}
	return out
}

// PendingCount returns current pending ack count.
func (t *AckTracker) PendingCount() int {
	if t == nil {
		return 0
	}
	total := 0
	for i := range t.shards {
		shard := &t.shards[i]
		shard.mu.Lock()
		total += len(shard.byMessage)
		shard.mu.Unlock()
	}
	return total
}

func (t *AckTracker) shard(sessionID uint64) *ackShard {
	return &t.shards[sessionID%uint64(len(t.shards))]
}
```

- [ ] **Step 5: Run ack tracker tests**

Run:

```bash
go test ./internalv2/runtime/delivery -run TestAckTracker
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internalv2/runtime/delivery
git commit -m "Add internalv2 delivery ack tracker"
```

---

### Task 4: Add Planner, Fanout Worker, and Runtime Facade

**Files:**
- Modify: `internalv2/runtime/delivery/types.go`
- Create: `internalv2/runtime/delivery/planner.go`
- Create: `internalv2/runtime/delivery/planner_test.go`
- Create: `internalv2/runtime/delivery/fanout_worker.go`
- Create: `internalv2/runtime/delivery/fanout_worker_test.go`
- Create: `internalv2/runtime/delivery/manager.go`
- Create: `internalv2/runtime/delivery/manager_test.go`

- [ ] **Step 1: Write planner test**

Create `internalv2/runtime/delivery/planner_test.go`:

```go
package delivery

import (
	"context"
	"testing"
)

func TestPlannerBuildsOneTaskPerAuthorityPartition(t *testing.T) {
	planner := NewPlanner(PlannerOptions{
		Partitioner: staticPartitioner{
			partitions: []Partition{
				{ID: 1, LeaderNodeID: 10, HashSlotStart: 0, HashSlotEnd: 9},
				{ID: 2, LeaderNodeID: 20, HashSlotStart: 10, HashSlotEnd: 19},
			},
		},
	})

	tasks, err := planner.Plan(context.Background(), Envelope{ChannelID: "g1", ChannelType: 2, MessageID: 100, MessageSeq: 7})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("tasks = %d, want 2", len(tasks))
	}
	if tasks[0].Partition.LeaderNodeID != 10 || tasks[1].Partition.LeaderNodeID != 20 {
		t.Fatalf("tasks = %#v, want authority leaders", tasks)
	}
}
```

- [ ] **Step 2: Write fanout worker test**

Create `internalv2/runtime/delivery/fanout_worker_test.go`:

```go
package delivery

import (
	"context"
	"testing"
)

func TestFanoutWorkerResolvesPresenceAndPushesByOwner(t *testing.T) {
	subscribers := &fakeSubscriberPlanner{
		pages: map[uint32][]string{7: []string{"u1", "u2", "u3"}},
	}
	presence := fakePresenceResolver{
		routes: map[string][]Route{
			"u1": {{UID: "u1", OwnerNodeID: 1, SessionID: 11}},
			"u2": {{UID: "u2", OwnerNodeID: 2, SessionID: 22}},
		},
	}
	pusher := &recordingPusher{}
	worker := NewFanoutWorker(FanoutWorkerOptions{Subscribers: subscribers, Presence: presence, Push: pusher, PageSize: 128})

	err := worker.RunTask(context.Background(), FanoutTask{
		Envelope:  Envelope{ChannelID: "g1", ChannelType: 2, MessageID: 100, MessageSeq: 7},
		Partition: Partition{ID: 7, LeaderNodeID: 1},
	})
	if err != nil {
		t.Fatalf("RunTask() error = %v", err)
	}
	if got := len(pusher.commands); got != 2 {
		t.Fatalf("push commands = %d, want one per owner node", got)
	}
}
```

- [ ] **Step 3: Run planner and worker tests and verify they fail**

Run:

```bash
go test ./internalv2/runtime/delivery -run 'Test(Planner|FanoutWorker)'
```

Expected: FAIL with missing planner and worker types.

- [ ] **Step 4: Extend runtime DTOs**

Append to `internalv2/runtime/delivery/types.go`:

```go
// Envelope is the immutable committed message DTO consumed by delivery.
type Envelope struct {
	MessageID        uint64
	MessageSeq       uint64
	ChannelID        string
	ChannelType      uint8
	FromUID          string
	SenderSessionID  uint64
	ClientMsgNo      string
	RedDot           bool
	Payload          []byte
	MessageScopedUIDs []string
}

// Partition identifies one UID-authority fanout target.
type Partition struct {
	ID            uint32
	LeaderNodeID  uint64
	HashSlotStart uint16
	HashSlotEnd   uint16
}

// FanoutTask carries one partition of one committed message.
type FanoutTask struct {
	Envelope  Envelope
	Partition Partition
	Cursor    string
	Attempt   int
}

// Route identifies one virtual presence route for a recipient session.
type Route struct {
	UID         string
	OwnerNodeID uint64
	OwnerBootID uint64
	OwnerSeq    uint64
	SessionID   uint64
	DeviceID    string
	DeviceFlag  uint8
	DeviceLevel uint8
}

// PushCommand sends one message to routes owned by the same recipient node.
type PushCommand struct {
	OwnerNodeID uint64
	Envelope    Envelope
	Routes      []Route
}

// PushResult reports owner-node handoff outcomes.
type PushResult struct {
	Accepted  []Route
	Retryable []Route
	Dropped   []Route
}
```

- [ ] **Step 5: Add planner implementation**

Create `internalv2/runtime/delivery/planner.go`:

```go
package delivery

import "context"

// Partitioner returns current UID-authority delivery partitions.
type Partitioner interface {
	Partitions(context.Context) ([]Partition, error)
}

// PlannerOptions configures committed event planning.
type PlannerOptions struct {
	Partitioner Partitioner
}

// Planner creates one fanout task per current UID-authority partition.
type Planner struct {
	partitioner Partitioner
}

// NewPlanner creates a Planner.
func NewPlanner(opts PlannerOptions) *Planner {
	return &Planner{partitioner: opts.Partitioner}
}

// Plan expands one committed message into partition fanout tasks.
func (p *Planner) Plan(ctx context.Context, env Envelope) ([]FanoutTask, error) {
	if p == nil || p.partitioner == nil {
		return []FanoutTask{{Envelope: cloneEnvelope(env), Partition: Partition{ID: 1}}}, nil
	}
	partitions, err := p.partitioner.Partitions(ctx)
	if err != nil {
		return nil, err
	}
	tasks := make([]FanoutTask, 0, len(partitions))
	for _, partition := range partitions {
		tasks = append(tasks, FanoutTask{Envelope: cloneEnvelope(env), Partition: partition, Attempt: 1})
	}
	return tasks, nil
}

func cloneEnvelope(env Envelope) Envelope {
	env.Payload = append([]byte(nil), env.Payload...)
	env.MessageScopedUIDs = append([]string(nil), env.MessageScopedUIDs...)
	return env
}
```

- [ ] **Step 6: Add fanout worker implementation**

Create `internalv2/runtime/delivery/fanout_worker.go`:

```go
package delivery

import "context"

const defaultFanoutPageSize = 512

// SubscriberPlanner pages recipient UIDs for one fanout partition.
type SubscriberPlanner interface {
	NextPartitionPage(context.Context, FanoutTask, string, int) (UIDPage, error)
}

// UIDPage contains one page of partition recipients.
type UIDPage struct {
	UIDs       []string
	NextCursor string
	Done       bool
}

// PresenceResolver expands UIDs to currently online routes.
type PresenceResolver interface {
	EndpointsByUIDs(context.Context, []string) (map[string][]Route, error)
}

// Pusher hands off recipient routes to owner nodes.
type Pusher interface {
	Push(context.Context, PushCommand) (PushResult, error)
}

// FanoutWorkerOptions configures one worker.
type FanoutWorkerOptions struct {
	Subscribers SubscriberPlanner
	Presence    PresenceResolver
	Push        Pusher
	PageSize    int
}

// FanoutWorker runs partition fanout tasks.
type FanoutWorker struct {
	subscribers SubscriberPlanner
	presence    PresenceResolver
	push        Pusher
	pageSize    int
}

// NewFanoutWorker creates a FanoutWorker.
func NewFanoutWorker(opts FanoutWorkerOptions) *FanoutWorker {
	pageSize := opts.PageSize
	if pageSize <= 0 {
		pageSize = defaultFanoutPageSize
	}
	return &FanoutWorker{subscribers: opts.Subscribers, presence: opts.Presence, push: opts.Push, pageSize: pageSize}
}

// RunTask resolves subscribers and hands online routes to owner nodes.
func (w *FanoutWorker) RunTask(ctx context.Context, task FanoutTask) error {
	if w == nil || w.subscribers == nil || w.presence == nil || w.push == nil {
		return nil
	}
	cursor := task.Cursor
	for {
		page, err := w.subscribers.NextPartitionPage(ctx, task, cursor, w.pageSize)
		if err != nil {
			return err
		}
		if len(page.UIDs) > 0 {
			routesByUID, err := w.presence.EndpointsByUIDs(ctx, page.UIDs)
			if err != nil {
				return err
			}
			if err := w.pushRoutes(ctx, task.Envelope, routesByUID); err != nil {
				return err
			}
		}
		if page.Done {
			return nil
		}
		if page.NextCursor == "" || page.NextCursor == cursor {
			return nil
		}
		cursor = page.NextCursor
	}
}

func (w *FanoutWorker) pushRoutes(ctx context.Context, env Envelope, routesByUID map[string][]Route) error {
	byOwner := make(map[uint64][]Route)
	for _, routes := range routesByUID {
		for _, route := range routes {
			if route.OwnerNodeID == 0 || isSenderRoute(env, route) {
				continue
			}
			byOwner[route.OwnerNodeID] = append(byOwner[route.OwnerNodeID], route)
		}
	}
	for ownerNodeID, routes := range byOwner {
		if _, err := w.push.Push(ctx, PushCommand{OwnerNodeID: ownerNodeID, Envelope: cloneEnvelope(env), Routes: append([]Route(nil), routes...)}); err != nil {
			return err
		}
	}
	return nil
}

func isSenderRoute(env Envelope, route Route) bool {
	return route.UID == env.FromUID && env.SenderSessionID != 0 && route.SessionID == env.SenderSessionID
}
```

- [ ] **Step 7: Add runtime manager**

Create `internalv2/runtime/delivery/manager.go`:

```go
package delivery

import (
	"context"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/messageevents"
)

// ManagerOptions configures the delivery runtime facade.
type ManagerOptions struct {
	Planner *Planner
	Worker  *FanoutWorker
	Acks    *AckTracker
}

// Manager owns delivery planning, fanout, and recipient-owner ack state.
type Manager struct {
	planner *Planner
	worker  *FanoutWorker
	acks    *AckTracker
}

// NewManager creates a Manager.
func NewManager(opts ManagerOptions) *Manager {
	if opts.Acks == nil {
		opts.Acks = NewAckTracker(AckTrackerOptions{})
	}
	return &Manager{planner: opts.Planner, worker: opts.Worker, acks: opts.Acks}
}

// SubmitCommitted plans and runs fanout for one committed message.
func (m *Manager) SubmitCommitted(ctx context.Context, event messageevents.MessageCommitted) error {
	if m == nil || m.planner == nil || m.worker == nil {
		return nil
	}
	tasks, err := m.planner.Plan(ctx, envelopeFromEvent(event))
	if err != nil {
		return err
	}
	for _, task := range tasks {
		if err := m.worker.RunTask(ctx, task); err != nil {
			return err
		}
	}
	return nil
}

// Recvack records one owner-local client ack.
func (m *Manager) Recvack(_ context.Context, cmd Recvack) error {
	if m == nil || m.acks == nil {
		return nil
	}
	_, _ = m.acks.Ack(cmd)
	return nil
}

// SessionClosed clears pending ack state for one owner-local session.
func (m *Manager) SessionClosed(_ context.Context, cmd SessionClosed) error {
	if m == nil || m.acks == nil {
		return nil
	}
	_ = m.acks.SessionClosed(cmd.UID, cmd.SessionID)
	return nil
}

// BindPendingAck records a RecvPacket handed to a local session.
func (m *Manager) BindPendingAck(pending PendingRecvAck) {
	if m == nil || m.acks == nil {
		return
	}
	m.acks.Bind(pending)
}

func envelopeFromEvent(event messageevents.MessageCommitted) Envelope {
	return Envelope{
		MessageID:        event.MessageID,
		MessageSeq:       event.MessageSeq,
		ChannelID:        event.ChannelID,
		ChannelType:      event.ChannelType,
		FromUID:          event.FromUID,
		SenderSessionID:  event.SenderSessionID,
		ClientMsgNo:      event.ClientMsgNo,
		RedDot:           event.RedDot,
		Payload:          append([]byte(nil), event.Payload...),
		MessageScopedUIDs: append([]string(nil), event.MessageScopedUIDs...),
	}
}
```

- [ ] **Step 8: Run runtime tests**

Run:

```bash
go test ./internalv2/runtime/delivery
```

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internalv2/runtime/delivery
git commit -m "Add internalv2 delivery runtime fanout"
```

---

### Task 5: Add Delivery Node RPC and Cluster Adapters

**Files:**
- Modify: `pkg/clusterv2/net/ids.go`
- Modify: `pkg/clusterv2/net/ids_test.go`
- Create: `internalv2/access/node/delivery_codec.go`
- Create: `internalv2/access/node/delivery_rpc.go`
- Create: `internalv2/access/node/delivery_codec_test.go`
- Create: `internalv2/access/node/delivery_rpc_test.go`
- Create: `internalv2/infra/cluster/delivery.go`
- Create: `internalv2/infra/cluster/delivery_test.go`

- [ ] **Step 1: Write service ID test update**

Modify `pkg/clusterv2/net/ids_test.go` to add:

```go
"delivery_push": RPCDeliveryPush,
```

inside the existing `ids` map.

- [ ] **Step 2: Run service ID test and verify it fails**

Run:

```bash
go test ./pkg/clusterv2/net -run TestRPCServiceIDsAreUniqueAndNonZero
```

Expected: FAIL with `undefined: RPCDeliveryPush`.

- [ ] **Step 3: Add service ID**

Modify `pkg/clusterv2/net/ids.go` after `RPCPresenceOwner`:

```go
// RPCDeliveryPush serves internalv2 owner-node delivery push batches.
RPCDeliveryPush
```

- [ ] **Step 4: Write delivery codec round-trip test**

Create `internalv2/access/node/delivery_codec_test.go`:

```go
package node

import (
	"reflect"
	"testing"

	runtimedelivery "github.com/WuKongIM/WuKongIM/internalv2/runtime/delivery"
)

func TestDeliveryPushCodecRoundTrip(t *testing.T) {
	req := deliveryPushRequest{
		Command: runtimedelivery.PushCommand{
			OwnerNodeID: 3,
			Envelope: runtimedelivery.Envelope{
				MessageID: 10,
				MessageSeq: 11,
				ChannelID: "g1",
				ChannelType: 2,
				FromUID: "u1",
				Payload: []byte("hello"),
			},
			Routes: []runtimedelivery.Route{{UID: "u2", OwnerNodeID: 3, SessionID: 22}},
		},
	}
	body, err := encodeDeliveryPushRequest(req)
	if err != nil {
		t.Fatalf("encodeDeliveryPushRequest() error = %v", err)
	}
	got, err := decodeDeliveryPushRequest(body)
	if err != nil {
		t.Fatalf("decodeDeliveryPushRequest() error = %v", err)
	}
	if !reflect.DeepEqual(got, req) {
		t.Fatalf("decoded request = %#v, want %#v", got, req)
	}
}
```

- [ ] **Step 5: Run codec test and verify it fails**

Run:

```bash
go test ./internalv2/access/node -run TestDeliveryPushCodecRoundTrip
```

Expected: FAIL because delivery codec is missing.

- [ ] **Step 6: Add delivery RPC codec**

Create `internalv2/access/node/delivery_codec.go` with deterministic binary encoding mirroring the presence codec style:

```go
package node

import (
	"fmt"

	runtimedelivery "github.com/WuKongIM/WuKongIM/internalv2/runtime/delivery"
)

var (
	deliveryPushRequestMagic  = [...]byte{'W', 'K', 'V', 'D', 1}
	deliveryPushResponseMagic = [...]byte{'W', 'K', 'V', 'd', 1}
)

type deliveryPushRequest struct {
	Command runtimedelivery.PushCommand
}

type deliveryPushResponse struct {
	Status string
	Result runtimedelivery.PushResult
}

func encodeDeliveryPushRequest(req deliveryPushRequest) ([]byte, error) {
	dst := make([]byte, 0, 256)
	dst = append(dst, deliveryPushRequestMagic[:]...)
	dst = appendPushCommand(dst, req.Command)
	return dst, nil
}

func decodeDeliveryPushRequest(body []byte) (deliveryPushRequest, error) {
	if !hasMagic(body, deliveryPushRequestMagic[:]) {
		return deliveryPushRequest{}, fmt.Errorf("internalv2/access/node: invalid delivery push request codec")
	}
	cmd, offset, err := readPushCommand(body, len(deliveryPushRequestMagic))
	if err != nil {
		return deliveryPushRequest{}, err
	}
	if offset != len(body) {
		return deliveryPushRequest{}, fmt.Errorf("internalv2/access/node: trailing delivery push request bytes")
	}
	return deliveryPushRequest{Command: cmd}, nil
}
```

Add these codec functions in the same file, using the varint helpers already
available in `internalv2/access/node/presence_codec.go`:

```go
func appendPushCommand(dst []byte, cmd runtimedelivery.PushCommand) []byte
func readPushCommand(body []byte, offset int) (runtimedelivery.PushCommand, int, error)
func appendDeliveryEnvelope(dst []byte, env runtimedelivery.Envelope) []byte
func readDeliveryEnvelope(body []byte, offset int) (runtimedelivery.Envelope, int, error)
func appendDeliveryRoutes(dst []byte, routes []runtimedelivery.Route) []byte
func readDeliveryRoutes(body []byte, offset int) ([]runtimedelivery.Route, int, error)
func appendPushResult(dst []byte, result runtimedelivery.PushResult) []byte
func readPushResult(body []byte, offset int) (runtimedelivery.PushResult, int, error)
```

Encode fields in this order:

```text
PushCommand:
  OwnerNodeID
  Envelope
  Routes

Envelope:
  MessageID
  MessageSeq
  ChannelID
  ChannelType
  FromUID
  SenderSessionID
  ClientMsgNo
  RedDot as 0 or 1 byte
  Payload
  MessageScopedUIDs

Route:
  UID
  OwnerNodeID
  OwnerBootID
  OwnerSeq
  SessionID
  DeviceID
  DeviceFlag
  DeviceLevel

PushResult:
  Accepted routes
  Retryable routes
  Dropped routes
```

- [ ] **Step 7: Add delivery RPC adapter and client**

Create `internalv2/access/node/delivery_rpc.go`:

```go
package node

import (
	"context"
	"fmt"

	runtimedelivery "github.com/WuKongIM/WuKongIM/internalv2/runtime/delivery"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/clusterv2/net"
)

// DeliveryPushRPCServiceID is the clusterv2 RPC service for owner-node push batches.
const DeliveryPushRPCServiceID uint8 = clusternet.RPCDeliveryPush

// DeliveryOwnerPush handles push batches on the recipient owner node.
type DeliveryOwnerPush interface {
	Push(context.Context, runtimedelivery.PushCommand) (runtimedelivery.PushResult, error)
}

// DeliveryOptions installs delivery RPC handlers on the node adapter.
type DeliveryOptions struct {
	// Delivery handles owner-node push batches.
	Delivery DeliveryOwnerPush
}

// WithDelivery installs delivery RPC dependencies on an existing Adapter.
func (a *Adapter) WithDelivery(opts DeliveryOptions) *Adapter {
	if a == nil {
		return nil
	}
	a.delivery = opts.Delivery
	return a
}

// HandleDeliveryPushRPC handles one encoded owner-node delivery push RPC.
func (a *Adapter) HandleDeliveryPushRPC(ctx context.Context, payload []byte) ([]byte, error) {
	req, err := decodeDeliveryPushRequest(payload)
	if err != nil {
		return nil, err
	}
	if a == nil || a.delivery == nil {
		return encodeDeliveryPushResponse(deliveryPushResponse{Status: rpcStatusRejected})
	}
	result, err := a.delivery.Push(ctx, req.Command)
	if err != nil {
		return encodeDeliveryPushResponse(deliveryPushResponse{Status: rpcStatusRejected})
	}
	return encodeDeliveryPushResponse(deliveryPushResponse{Status: rpcStatusOK, Result: result})
}

// PushBatch sends one owner-node delivery push batch.
func (c *Client) PushBatch(ctx context.Context, nodeID uint64, cmd runtimedelivery.PushCommand) (runtimedelivery.PushResult, error) {
	if c == nil || c.node == nil {
		return runtimedelivery.PushResult{}, fmt.Errorf("internalv2/access/node: delivery rpc client not configured")
	}
	body, err := encodeDeliveryPushRequest(deliveryPushRequest{Command: cmd})
	if err != nil {
		return runtimedelivery.PushResult{}, err
	}
	respBody, err := c.node.CallRPC(ctx, nodeID, DeliveryPushRPCServiceID, body)
	if err != nil {
		return runtimedelivery.PushResult{}, err
	}
	resp, err := decodeDeliveryPushResponse(respBody)
	if err != nil {
		return runtimedelivery.PushResult{}, err
	}
	if resp.Status != rpcStatusOK {
		return runtimedelivery.PushResult{}, fmt.Errorf("internalv2/access/node: delivery push status %q", resp.Status)
	}
	return resp.Result, nil
}
```

Modify `internalv2/access/node/presence_rpc.go` so `Adapter` has:

```go
// delivery handles owner-node message push batches.
delivery DeliveryOwnerPush
```

- [ ] **Step 8: Add cluster adapter tests and implementation**

Create `internalv2/infra/cluster/delivery.go`:

```go
package cluster

import (
	"context"

	accessnode "github.com/WuKongIM/WuKongIM/internalv2/access/node"
	runtimedelivery "github.com/WuKongIM/WuKongIM/internalv2/runtime/delivery"
)

// DeliveryPushNode is the clusterv2 surface required by delivery push adapters.
type DeliveryPushNode interface {
	NodeID() uint64
	CallRPC(context.Context, uint64, uint8, []byte) ([]byte, error)
	RegisterRPC(uint8, NodeRPCHandler)
}

// DeliveryPusher routes local and remote owner-node push batches.
type DeliveryPusher struct {
	localNodeID uint64
	local       runtimedelivery.Pusher
	remote      *accessnode.Client
}

// NewDeliveryPusher creates a delivery pusher adapter.
func NewDeliveryPusher(localNodeID uint64, local runtimedelivery.Pusher, remote *accessnode.Client) *DeliveryPusher {
	return &DeliveryPusher{localNodeID: localNodeID, local: local, remote: remote}
}
```

Write `Push` so local owner batches call `local.Push`, remote batches call
`remote.PushBatch`, and missing remote client returns all routes as retryable.

- [ ] **Step 9: Run RPC and adapter tests**

Run:

```bash
go test ./pkg/clusterv2/net ./internalv2/access/node ./internalv2/infra/cluster -run 'Test(RPCServiceIDsAreUniqueAndNonZero|Delivery)'
```

Expected: PASS.

- [ ] **Step 10: Commit**

```bash
git add pkg/clusterv2/net/ids.go pkg/clusterv2/net/ids_test.go internalv2/access/node internalv2/infra/cluster
git commit -m "Add internalv2 delivery push RPC"
```

---

### Task 6: Wire Gateway Recvack, Local Push, and App Composition

**Files:**
- Modify: `internalv2/access/gateway/handler.go`
- Modify: `internalv2/access/gateway/FLOW.md`
- Modify: `internalv2/access/gateway/handler_test.go`
- Modify: `internalv2/runtime/online/types.go`
- Modify: `internalv2/runtime/online/registry.go`
- Modify: `internalv2/runtime/online/registry_test.go`
- Modify: `internalv2/app/config.go`
- Modify: `internalv2/app/app.go`
- Modify: `internalv2/app/lifecycle.go`
- Modify: `internalv2/app/FLOW.md`
- Modify: `internalv2/app/app_test.go`

- [ ] **Step 1: Write gateway recvack test**

Add to `internalv2/access/gateway/handler_test.go`:

```go
func TestOnFrameRecvackForwardsToDelivery(t *testing.T) {
	delivery := &recordingDelivery{}
	handler := New(Options{Messages: &recordingMessages{}, Delivery: delivery})
	sess := session.New(session.Config{ID: 77})
	sess.SetValue(coregateway.SessionValueUID, "u1")

	err := handler.OnFrame(coregateway.Context{Session: sess, RequestContext: context.Background()}, &frame.RecvackPacket{
		MessageID:  100,
		MessageSeq: 7,
	})
	if err != nil {
		t.Fatalf("OnFrame(recvack) error = %v", err)
	}
	if delivery.recvacks != 1 {
		t.Fatalf("recvacks = %d, want 1", delivery.recvacks)
	}
}
```

- [ ] **Step 2: Run gateway test and verify it fails**

Run:

```bash
go test ./internalv2/access/gateway -run TestOnFrameRecvackForwardsToDelivery
```

Expected: FAIL because the handler has no delivery dependency and no recvack case.

- [ ] **Step 3: Add gateway delivery interface**

Modify `internalv2/access/gateway/handler.go`:

```go
// DeliveryUsecase receives client delivery feedback from gateway sessions.
type DeliveryUsecase interface {
	Recvack(context.Context, delivery.RecvackCommand) error
	SessionClosed(context.Context, delivery.SessionClosedCommand) error
}
```

Add `Delivery DeliveryUsecase` to `Options`, add a `delivery DeliveryUsecase`
field to `Handler`, and initialize it in `New`.

Add a `case *frame.RecvackPacket` in `OnFrame`:

```go
case *frame.RecvackPacket:
	return h.handleRecvack(&ctx, pkt)
```

Implement:

```go
func (h *Handler) handleRecvack(ctx *coregateway.Context, pkt *frame.RecvackPacket) error {
	if h == nil || h.delivery == nil || ctx == nil || ctx.Session == nil || pkt == nil {
		return nil
	}
	uid, _ := ctx.Session.Value(coregateway.SessionValueUID).(string)
	if uid == "" {
		return ErrUnauthenticatedSession
	}
	return h.delivery.Recvack(requestContextFromContext(ctx), delivery.RecvackCommand{
		UID:        uid,
		SessionID:  ctx.Session.ID(),
		MessageID:  uint64(pkt.MessageID),
		MessageSeq: pkt.MessageSeq,
	})
}
```

Also call `delivery.SessionClosed` from `OnSessionClose` after presence
deactivation.

- [ ] **Step 4: Extend online local session write surface**

Modify `internalv2/runtime/online/types.go` so `SessionHandle` supports writes
through an entry-agnostic method:

```go
// SessionHandle writes and closes a concrete gateway session without importing entry packages.
type SessionHandle interface {
	WriteDelivery(any) error
	CloseSession(reason string) error
}
```

In `internalv2/access/gateway/presence.go`, wrap gateway sessions with a type
that implements `WriteDelivery(any) error` by accepting `frame.Frame` values and
calling `Session.WriteFrame`.

- [ ] **Step 5: Add local owner pusher**

Create a local pusher in `internalv2/app` or `internalv2/runtime/delivery` that:

```go
func (p localOwnerPusher) Push(_ context.Context, cmd runtimedelivery.PushCommand) (runtimedelivery.PushResult, error) {
	var result runtimedelivery.PushResult
	for _, route := range cmd.Routes {
		local, ok := p.online.LocalSession(route.SessionID)
		if !ok || local.Route.UID != route.UID || local.State != online.RouteStateActive {
			result.Dropped = append(result.Dropped, route)
			continue
		}
		packet := buildRecvPacket(cmd.Envelope, route.UID)
		if err := local.Session.WriteDelivery(packet); err != nil {
			result.Retryable = append(result.Retryable, route)
			continue
		}
		p.delivery.BindPendingAck(runtimedelivery.PendingRecvAck{
			UID:         route.UID,
			SessionID:   route.SessionID,
			MessageID:   cmd.Envelope.MessageID,
			MessageSeq:  cmd.Envelope.MessageSeq,
			ChannelID:   cmd.Envelope.ChannelID,
			ChannelType: cmd.Envelope.ChannelType,
		})
		result.Accepted = append(result.Accepted, route)
	}
	return result, nil
}
```

- [ ] **Step 6: Add delivery config**

Modify `internalv2/app/config.go`:

```go
// DeliveryConfig contains online message delivery runtime settings.
type DeliveryConfig struct {
	// Enabled wires internalv2 committed messages to the delivery runtime.
	Enabled bool
	// FanoutPageSize bounds subscriber UIDs resolved per fanout page.
	FanoutPageSize int
	// PushBatchSize bounds routes sent in one owner-node push batch.
	PushBatchSize int
	// PendingAckTTL bounds recipient-owner pending recvack state.
	PendingAckTTL time.Duration
}
```

Add `Delivery DeliveryConfig` to `Config`, plus defaults and validation:

```go
func defaultDeliveryConfig(cfg DeliveryConfig) DeliveryConfig {
	if cfg.FanoutPageSize == 0 {
		cfg.FanoutPageSize = 512
	}
	if cfg.PushBatchSize == 0 {
		cfg.PushBatchSize = 512
	}
	if cfg.PendingAckTTL == 0 {
		cfg.PendingAckTTL = 30 * time.Second
	}
	return cfg
}
```

- [ ] **Step 7: Wire app**

Modify `internalv2/app/app.go`:

```go
if app.cfg.Delivery.Enabled && app.delivery == nil {
	ackTracker := deliveryruntime.NewAckTracker(deliveryruntime.AckTrackerOptions{})
	manager := deliveryruntime.NewManager(deliveryruntime.ManagerOptions{Acks: ackTracker})
	app.delivery = deliveryusecase.New(deliveryusecase.Options{Runtime: manager})
	app.messages = message.New(message.Options{
		Appender: clusterinfra.NewChannelAppender(node),
		MessageID: newNodeMessageIDs(clusterCfg.NodeID),
		Committed: app.delivery,
	})
}
```

Adjust placement to preserve existing appender/message construction and avoid
overwriting `WithMessages`.

- [ ] **Step 8: Run gateway and app tests**

Run:

```bash
go test ./internalv2/access/gateway ./internalv2/app ./internalv2/runtime/online
```

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internalv2/access/gateway internalv2/runtime/online internalv2/app
git commit -m "Wire internalv2 delivery gateway feedback"
```

---

### Task 7: Add Delivery Benchmarks and Final Verification

**Files:**
- Create: `internalv2/runtime/delivery/benchmark_test.go`
- Modify: `docs/development/PROJECT_KNOWLEDGE.md` if implementation discovers a stable project rule.

- [ ] **Step 1: Add no-cluster benchmarks**

Create `internalv2/runtime/delivery/benchmark_test.go`:

```go
package delivery

import (
	"context"
	"strconv"
	"testing"
)

func BenchmarkPlannerPartition100K(b *testing.B) {
	planner := NewPlanner(PlannerOptions{Partitioner: benchmarkPartitioner(64)})
	env := Envelope{ChannelID: "g1", ChannelType: 2, MessageID: 1, MessageSeq: 1}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		tasks, err := planner.Plan(context.Background(), env)
		if err != nil || len(tasks) != 64 {
			b.Fatalf("Plan() tasks=%d err=%v", len(tasks), err)
		}
	}
}

func BenchmarkRecipientAckTrackerRecvack(b *testing.B) {
	tracker := NewAckTracker(AckTrackerOptions{ShardCount: 64, Now: func() int64 { return 100 }})
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sessionID := uint64(i%10000 + 1)
		messageID := uint64(i + 1)
		uid := "u" + strconv.Itoa(i%10000)
		tracker.Bind(PendingRecvAck{UID: uid, SessionID: sessionID, MessageID: messageID})
		_, _ = tracker.Ack(Recvack{UID: uid, SessionID: sessionID, MessageID: messageID})
	}
}
```

Add `BenchmarkFanoutWorkerLocalPresence`, `BenchmarkFanoutWorkerRemotePushBatch`,
and `BenchmarkDeliveryEndToEndNoCluster` using fake subscriber, presence, and
pusher types in this file.

- [ ] **Step 2: Run benchmark smoke**

Run:

```bash
go test ./internalv2/runtime/delivery -run '^$' -bench 'Benchmark(PlannerPartition100K|RecipientAckTrackerRecvack)' -benchmem
```

Expected: PASS with benchmark output.

- [ ] **Step 3: Run targeted package tests**

Run:

```bash
go test ./internalv2/contracts/messageevents ./internalv2/usecase/message ./internalv2/usecase/delivery ./internalv2/runtime/delivery ./internalv2/access/gateway ./internalv2/access/node ./internalv2/infra/cluster ./internalv2/app ./pkg/clusterv2/net
```

Expected: PASS.

- [ ] **Step 4: Run broad internalv2 tests**

Run:

```bash
go test ./internalv2/...
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internalv2/runtime/delivery/benchmark_test.go docs/development/PROJECT_KNOWLEDGE.md
git commit -m "Add internalv2 delivery benchmarks"
```

Skip `docs/development/PROJECT_KNOWLEDGE.md` in this commit if no stable project rule was discovered.

---

## Self-Review Notes

Spec coverage:

- Independent module: Tasks 2, 3, and 4 create usecase/runtime boundaries.
- UID authority partitioned fanout: Tasks 4 and 5 create planner, worker, and routing adapters.
- Recipient-owner local recvack: Tasks 3 and 6 create ack tracker and gateway feedback.
- Committed event delivery fields: Task 1 extends the event contract.
- Single-node cluster semantics: Task 6 app wiring uses the same cluster surfaces for all deployments.
- Standalone pressure testing: Task 7 adds no-cluster benchmarks.

Type consistency:

- `messageevents.MessageCommitted` maps to `runtime/delivery.Envelope`.
- `usecase/delivery.RecvackCommand` maps to `runtime/delivery.Recvack`.
- `runtime/delivery.PushCommand` is the DTO used by node RPC and cluster adapters.

Execution should use a fresh worktree at implementation time if the main
workspace is not clean.
