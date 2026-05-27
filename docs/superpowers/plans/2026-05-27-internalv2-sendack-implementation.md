# internalv2 Send-to-Sendack Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a parallel `internalv2` skeleton that proves `SendPacket -> SendackPacket` through `pkg/clusterv2` and `pkg/channelv2` without replacing the current `internal` production stack.

**Architecture:** Add a small dual-stack `internalv2` tree with a single app composition root, a WKProto gateway adapter, an entry-agnostic message usecase, a clusterv2 appender adapter, and focused contracts. The canonical send path is batch-oriented from day one: `gateway.OnSendBatch -> message.SendBatch -> clusterv2.AppendChannelBatch`.

**Tech Stack:** Go 1.23, `pkg/gateway`, `pkg/protocol/frame`, `pkg/clusterv2`, `pkg/clusterv2/channels`, `pkg/channelv2`, `pkg/channelv2/store`, existing `testing` package, and import-boundary tests.

---

## Reference Material

- Design spec: `docs/superpowers/specs/2026-05-27-internalv2-sendack-design.md`
- Project rules: `AGENTS.md`
- Current internal flow: `internal/FLOW.md`
- Current gateway adapter: `internal/access/gateway/*.go`
- Current message usecase: `internal/usecase/message/*.go`
- clusterv2 flow: `pkg/clusterv2/FLOW.md`
- channelv2 flow: `pkg/channelv2/FLOW.md`
- Gateway interfaces: `pkg/gateway/types/event.go`

Use `superpowers:test-driven-development` for each implementation task. Use `superpowers:verification-before-completion` before claiming the implementation is complete.

## File Structure

Create these files:

- `internalv2/FLOW.md` - responsibility, dependency direction, and phase-1 send flow.
- `internalv2/contracts/messageevents/event.go` - committed message event DTO used by message usecase and later delivery/conversation migration.
- `internalv2/usecase/message/errors.go` - phase-1 message and append errors.
- `internalv2/usecase/message/types.go` - send, append, reason, and commit-mode DTOs.
- `internalv2/usecase/message/ports.go` - usecase collaborator interfaces and no-op/default implementations.
- `internalv2/usecase/message/app.go` - message app constructor and dependency storage.
- `internalv2/usecase/message/send.go` - `Send` and `SendBatch` implementation.
- `internalv2/usecase/message/send_test.go` - focused usecase tests.
- `internalv2/usecase/message/import_boundary_test.go` - import guard for the usecase package.
- `internalv2/access/gateway/handler.go` - gateway handler surface and constructor.
- `internalv2/access/gateway/mapper.go` - `frame.SendPacket` to `message.SendCommand`, and Sendack writing.
- `internalv2/access/gateway/error_map.go` - internalv2 reason to WKProto reason mapping.
- `internalv2/access/gateway/batch.go` - gateway `OnSendBatch` implementation.
- `internalv2/access/gateway/handler_test.go` - handler and batch tests.
- `internalv2/infra/cluster/appender.go` - `message.Appender` backed by clusterv2 append.
- `internalv2/infra/cluster/error_map.go` - clusterv2/channelv2 typed error mapping to message errors.
- `internalv2/infra/cluster/appender_test.go` - DTO and error mapping tests.
- `internalv2/app/config.go` - phase-1 app configuration.
- `internalv2/app/app.go` - composition root and test injection options.
- `internalv2/app/lifecycle.go` - start/stop lifecycle.
- `internalv2/app/app_test.go` - lifecycle order and rollback tests.
- `internalv2/app/sendack_smoke_test.go` - single-node-cluster handler-level smoke.

Modify these files:

- `AGENTS.md` - add `internalv2/` to the directory structure.
- `docs/development/PROJECT_KNOWLEDGE.md` - add one concise note after verification that internalv2 phase 1 is a parallel send-to-sendack stack through clusterv2.

Do not modify `cmd/wukongim`, `wukongim.conf.example`, or existing `internal` packages in this plan.

## Task 0: Preflight And Baseline

**Files:**
- Read: `AGENTS.md`
- Read: `docs/superpowers/specs/2026-05-27-internalv2-sendack-design.md`
- Read: `pkg/clusterv2/FLOW.md`
- Read: `pkg/channelv2/FLOW.md`

- [ ] **Step 1: Confirm worktree state**

Run:

```bash
git status --short
```

Expected: no unrelated dirty files. If unrelated files exist, do not edit or stage them.

- [ ] **Step 2: Confirm clusterv2 and channelv2 baseline**

Run:

```bash
go test ./pkg/channelv2 ./pkg/clusterv2 -run 'TestPublicAPICompile|TestNodeInitializesDefaultChannelsWhenOptionMissing|TestNodeAppendChannelDelegatesToService' -count=1
```

Expected: PASS. If this fails, stop and diagnose the existing clusterv2/channelv2 baseline before creating `internalv2`.

- [ ] **Step 3: Read the spec**

Run:

```bash
sed -n '1,380p' docs/superpowers/specs/2026-05-27-internalv2-sendack-design.md
```

Expected: confirms phase 1 is a parallel dual-stack skeleton and does not wire `cmd/wukongim`.

This task is read-only.

## Task 1: Message Contracts, DTOs, And Import Boundary

**Files:**
- Create: `internalv2/contracts/messageevents/event.go`
- Create: `internalv2/usecase/message/errors.go`
- Create: `internalv2/usecase/message/types.go`
- Create: `internalv2/usecase/message/ports.go`
- Create: `internalv2/usecase/message/app.go`
- Create: `internalv2/usecase/message/import_boundary_test.go`
- Test: `internalv2/usecase/message/import_boundary_test.go`

- [ ] **Step 1: Create directories**

Run:

```bash
mkdir -p internalv2/contracts/messageevents internalv2/usecase/message
```

Expected: directories exist.

- [ ] **Step 2: Write the import-boundary test first**

Create `internalv2/usecase/message/import_boundary_test.go`:

```go
package message

import (
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

func TestMessageUsecaseImportBoundary(t *testing.T) {
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

- [ ] **Step 3: Run the boundary test and verify it compiles**

Run:

```bash
go test ./internalv2/usecase/message -run TestMessageUsecaseImportBoundary -count=1
```

Expected: PASS with only the boundary test present.

- [ ] **Step 4: Add committed event contract**

Create `internalv2/contracts/messageevents/event.go`:

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
	// ClientMsgNo is the client idempotency key.
	ClientMsgNo string
	// Payload is a copy of the committed payload.
	Payload []byte
}
```

- [ ] **Step 5: Add message errors**

Create `internalv2/usecase/message/errors.go`:

```go
package message

import "errors"

var (
	// ErrInvalidCommand reports a malformed send command.
	ErrInvalidCommand = errors.New("internalv2/message: invalid command")
	// ErrAppenderRequired reports that durable append is not configured.
	ErrAppenderRequired = errors.New("internalv2/message: appender required")
	// ErrMessageIDAllocatorRequired reports that message id allocation is not configured.
	ErrMessageIDAllocatorRequired = errors.New("internalv2/message: message id allocator required")
	// ErrNotLeader reports that the append target is no longer the leader.
	ErrNotLeader = errors.New("internalv2/message: not leader")
	// ErrStaleRoute reports that append used stale channel metadata.
	ErrStaleRoute = errors.New("internalv2/message: stale route")
	// ErrRouteNotReady reports that cluster routing is not ready for foreground writes.
	ErrRouteNotReady = errors.New("internalv2/message: route not ready")
	// ErrChannelNotFound reports that the target channel is not available.
	ErrChannelNotFound = errors.New("internalv2/message: channel not found")
	// ErrBackpressured reports bounded runtime pressure.
	ErrBackpressured = errors.New("internalv2/message: backpressured")
	// ErrAppendFailed wraps unexpected append failures.
	ErrAppendFailed = errors.New("internalv2/message: append failed")
)
```

- [ ] **Step 6: Add DTOs and reason enums**

Create `internalv2/usecase/message/types.go`:

```go
package message

import "context"

// Reason is the entry-agnostic result code for SEND.
type Reason uint8

const (
	// ReasonSuccess means the send was durably accepted.
	ReasonSuccess Reason = iota
	// ReasonInvalidRequest means the command is malformed.
	ReasonInvalidRequest
	// ReasonAuthFail means the sender is not authenticated.
	ReasonAuthFail
	// ReasonChannelNotExist means the channel cannot accept this send.
	ReasonChannelNotExist
	// ReasonNodeNotMatch means the client should retry through a fresher route.
	ReasonNodeNotMatch
	// ReasonSystemError means the send failed due to infrastructure pressure or error.
	ReasonSystemError
	// ReasonUnsupported means the phase-1 stack does not implement this send mode.
	ReasonUnsupported
)

// CommitMode controls when durable append completes.
type CommitMode uint8

const (
	// CommitModeQuorum waits for quorum commit.
	CommitModeQuorum CommitMode = iota + 1
	// CommitModeLocal completes after local durable append.
	CommitModeLocal
)

// ChannelID identifies a message channel.
type ChannelID struct {
	// ID is the client-visible channel id.
	ID string
	// Type is the protocol channel category.
	Type uint8
}

// SendCommand is an entry-agnostic SEND request.
type SendCommand struct {
	// FromUID is the authenticated sender uid.
	FromUID string
	// SenderSessionID is the node-local gateway session id.
	SenderSessionID uint64
	// ClientSeq is the client sequence echoed in Sendack.
	ClientSeq uint64
	// ClientMsgNo is the client idempotency key echoed in Sendack.
	ClientMsgNo string
	// ChannelID is the client-visible channel id.
	ChannelID string
	// ChannelType is the protocol channel category.
	ChannelType uint8
	// Payload is the message body. The usecase clones it before append.
	Payload []byte
	// NoPersist requests transient delivery; phase 1 returns ReasonUnsupported.
	NoPersist bool
	// SyncOnce marks a one-shot sync command; phase 1 passes it through only as a flag.
	SyncOnce bool
	// RedDot carries the client red-dot flag for future delivery side effects.
	RedDot bool
	// MessageID is optional and must be zero for gateway-origin sends.
	MessageID uint64
	// ProtocolVersion is the client protocol version.
	ProtocolVersion uint8
}

// SendResult is the client-facing SEND outcome.
type SendResult struct {
	// MessageID is the durable message id.
	MessageID uint64
	// MessageSeq is the committed channel sequence.
	MessageSeq uint64
	// Reason is the entry-agnostic result code.
	Reason Reason
}

// SendBatchItem carries one send command with its cancellation context.
type SendBatchItem struct {
	// Context is the per-send request context.
	Context context.Context
	// Command is the SEND command.
	Command SendCommand
}

// SendBatchItemResult aligns with one SendBatch item.
type SendBatchItemResult struct {
	// Result is the send result.
	Result SendResult
	// Err is a context or infrastructure error.
	Err error
}

// Message is the durable append payload used by the message appender port.
type Message struct {
	MessageID   uint64
	MessageSeq  uint64
	ChannelID   string
	ChannelType uint8
	FromUID     string
	ClientMsgNo string
	Payload     []byte
}

// AppendBatchRequest appends messages to one canonical channel.
type AppendBatchRequest struct {
	ChannelID  ChannelID
	Messages   []Message
	CommitMode CommitMode
}

// AppendBatchResult returns item-aligned append outcomes.
type AppendBatchResult struct {
	Items []AppendBatchItemResult
}

// AppendBatchItemResult is one append result inside a batch.
type AppendBatchItemResult struct {
	MessageID  uint64
	MessageSeq uint64
	Message    Message
	Err        error
}
```

- [ ] **Step 7: Add ports and app shell**

Create `internalv2/usecase/message/ports.go`:

```go
package message

import (
	"context"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/messageevents"
)

// Appender owns durable channel append routing.
type Appender interface {
	AppendBatch(context.Context, AppendBatchRequest) (AppendBatchResult, error)
}

// MessageIDAllocator allocates durable message ids.
type MessageIDAllocator interface {
	Next() uint64
}

// Decision is the result of send authorization.
type Decision struct {
	Allowed bool
	Reason  Reason
}

// Authorizer decides whether a send may enter durable append.
type Authorizer interface {
	AuthorizeSend(context.Context, SendCommand) (Decision, error)
}

// CommittedSink receives durable append events.
type CommittedSink interface {
	Submit(context.Context, messageevents.MessageCommitted) error
}

// Observer receives non-fatal send path observations.
type Observer interface {
	CommittedSinkError(SendCommand, error)
}

type allowAllAuthorizer struct{}

func (allowAllAuthorizer) AuthorizeSend(context.Context, SendCommand) (Decision, error) {
	return Decision{Allowed: true, Reason: ReasonSuccess}, nil
}

type noopCommittedSink struct{}

func (noopCommittedSink) Submit(context.Context, messageevents.MessageCommitted) error { return nil }
```

Create `internalv2/usecase/message/app.go`:

```go
package message

// Options configures the message usecase.
type Options struct {
	Appender   Appender
	MessageID MessageIDAllocator
	Authorizer Authorizer
	Committed  CommittedSink
	Observer   Observer
}

// App orchestrates entry-agnostic message sends.
type App struct {
	appender   Appender
	messageID  MessageIDAllocator
	authorizer Authorizer
	committed  CommittedSink
	observer   Observer
}

// New creates a message App.
func New(opts Options) *App {
	if opts.Authorizer == nil {
		opts.Authorizer = allowAllAuthorizer{}
	}
	if opts.Committed == nil {
		opts.Committed = noopCommittedSink{}
	}
	return &App{
		appender:   opts.Appender,
		messageID:  opts.MessageID,
		authorizer: opts.Authorizer,
		committed:  opts.Committed,
		observer:   opts.Observer,
	}
}
```

- [ ] **Step 8: Run tests and formatting**

Run:

```bash
gofmt -w internalv2/contracts/messageevents internalv2/usecase/message
go test ./internalv2/usecase/message -run TestMessageUsecaseImportBoundary -count=1
```

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internalv2/contracts/messageevents internalv2/usecase/message
git commit -m "feat(internalv2): add message send contracts"
```

## Task 2: Message SendBatch Usecase

**Files:**
- Modify: `internalv2/usecase/message/send.go`
- Test: `internalv2/usecase/message/send_test.go`

- [ ] **Step 1: Write failing send usecase tests**

Create `internalv2/usecase/message/send_test.go`:

```go
package message

import (
	"context"
	"errors"
	"testing"
)

func TestSendRejectsInvalidCommandsAndDoesNotAppend(t *testing.T) {
	appender := &recordingAppender{}
	ids := &sequenceIDs{next: 100}
	app := New(Options{Appender: appender, MessageID: ids})

	cases := []struct {
		name string
		cmd  SendCommand
		want Reason
	}{
		{name: "missing sender", cmd: SendCommand{ChannelID: "c1", ChannelType: 1, Payload: []byte("x")}, want: ReasonAuthFail},
		{name: "missing channel", cmd: SendCommand{FromUID: "u1", ChannelType: 1, Payload: []byte("x")}, want: ReasonInvalidRequest},
		{name: "missing channel type", cmd: SendCommand{FromUID: "u1", ChannelID: "c1", Payload: []byte("x")}, want: ReasonInvalidRequest},
		{name: "missing payload", cmd: SendCommand{FromUID: "u1", ChannelID: "c1", ChannelType: 1}, want: ReasonInvalidRequest},
		{name: "nopersist unsupported", cmd: SendCommand{FromUID: "u1", ChannelID: "c1", ChannelType: 1, Payload: []byte("x"), NoPersist: true}, want: ReasonUnsupported},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := app.Send(context.Background(), tc.cmd)
			if err != nil {
				t.Fatalf("Send() error = %v", err)
			}
			if got.Reason != tc.want {
				t.Fatalf("Send() Reason = %v, want %v", got.Reason, tc.want)
			}
		})
	}
	if appender.calls != 0 {
		t.Fatalf("append calls = %d, want 0", appender.calls)
	}
	if ids.allocated != 0 {
		t.Fatalf("allocated ids = %d, want 0", ids.allocated)
	}
}

func TestSendBatchAllocatesIDsAppendsAndPreservesOrder(t *testing.T) {
	appender := &recordingAppender{}
	ids := &sequenceIDs{next: 100}
	app := New(Options{Appender: appender, MessageID: ids})

	results := app.SendBatch([]SendBatchItem{
		{Context: context.Background(), Command: SendCommand{FromUID: "u1", ClientSeq: 1, ClientMsgNo: "m1", ChannelID: "room", ChannelType: 1, Payload: []byte("one")}},
		{Context: context.Background(), Command: SendCommand{FromUID: "u1", ClientSeq: 2, ClientMsgNo: "m2", ChannelID: "room", ChannelType: 1, Payload: []byte("two")}},
	})

	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	for i, result := range results {
		if result.Err != nil {
			t.Fatalf("result[%d] error = %v", i, result.Err)
		}
		if result.Result.Reason != ReasonSuccess {
			t.Fatalf("result[%d] reason = %v, want success", i, result.Result.Reason)
		}
	}
	if results[0].Result.MessageID != 100 || results[0].Result.MessageSeq != 1 {
		t.Fatalf("first result = %#v, want id=100 seq=1", results[0].Result)
	}
	if results[1].Result.MessageID != 101 || results[1].Result.MessageSeq != 2 {
		t.Fatalf("second result = %#v, want id=101 seq=2", results[1].Result)
	}
	if appender.calls != 1 {
		t.Fatalf("append calls = %d, want 1 same-channel segment", appender.calls)
	}
	if got := string(appender.requests[0].Messages[0].Payload); got != "one" {
		t.Fatalf("first appended payload = %q, want one", got)
	}
}

func TestSendBatchSplitsAdjacentChannelSegments(t *testing.T) {
	appender := &recordingAppender{}
	app := New(Options{Appender: appender, MessageID: &sequenceIDs{next: 1}})

	results := app.SendBatch([]SendBatchItem{
		{Command: SendCommand{FromUID: "u1", ChannelID: "a", ChannelType: 1, Payload: []byte("a1")}},
		{Command: SendCommand{FromUID: "u1", ChannelID: "b", ChannelType: 1, Payload: []byte("b1")}},
		{Command: SendCommand{FromUID: "u1", ChannelID: "a", ChannelType: 1, Payload: []byte("a2")}},
	})

	if len(results) != 3 {
		t.Fatalf("results = %d, want 3", len(results))
	}
	if appender.calls != 3 {
		t.Fatalf("append calls = %d, want 3 adjacent segments", appender.calls)
	}
}

func TestSendBatchMapsAppendItemErrorsToReasons(t *testing.T) {
	appender := &recordingAppender{itemErrs: []error{nil, ErrRouteNotReady}}
	app := New(Options{Appender: appender, MessageID: &sequenceIDs{next: 10}})

	results := app.SendBatch([]SendBatchItem{
		{Command: SendCommand{FromUID: "u1", ChannelID: "room", ChannelType: 1, Payload: []byte("ok")}},
		{Command: SendCommand{FromUID: "u1", ChannelID: "room", ChannelType: 1, Payload: []byte("retry")}},
	})

	if results[0].Result.Reason != ReasonSuccess {
		t.Fatalf("first reason = %v, want success", results[0].Result.Reason)
	}
	if results[1].Result.Reason != ReasonNodeNotMatch {
		t.Fatalf("second reason = %v, want node-not-match", results[1].Result.Reason)
	}
	if results[1].Err != nil {
		t.Fatalf("second err = %v, want nil business result", results[1].Err)
	}
}

func TestCommittedSinkErrorDoesNotChangeSendResult(t *testing.T) {
	observer := &recordingObserver{}
	app := New(Options{
		Appender:   &recordingAppender{},
		MessageID:  &sequenceIDs{next: 50},
		Committed:  failingCommitted{err: errors.New("sink down")},
		Observer:   observer,
	})

	result, err := app.Send(context.Background(), SendCommand{FromUID: "u1", ChannelID: "room", ChannelType: 1, Payload: []byte("hello")})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if result.Reason != ReasonSuccess || result.MessageSeq == 0 {
		t.Fatalf("Send() result = %#v, want success with seq", result)
	}
	if observer.committedErrors != 1 {
		t.Fatalf("committed errors = %d, want 1", observer.committedErrors)
	}
}

func TestSendReturnsErrorWhenAppenderOrAllocatorMissing(t *testing.T) {
	noAppender := New(Options{MessageID: &sequenceIDs{next: 1}})
	_, err := noAppender.Send(context.Background(), SendCommand{FromUID: "u1", ChannelID: "room", ChannelType: 1, Payload: []byte("hello")})
	if !errors.Is(err, ErrAppenderRequired) {
		t.Fatalf("no appender error = %v, want %v", err, ErrAppenderRequired)
	}

	noIDs := New(Options{Appender: &recordingAppender{}})
	_, err = noIDs.Send(context.Background(), SendCommand{FromUID: "u1", ChannelID: "room", ChannelType: 1, Payload: []byte("hello")})
	if !errors.Is(err, ErrMessageIDAllocatorRequired) {
		t.Fatalf("no allocator error = %v, want %v", err, ErrMessageIDAllocatorRequired)
	}
}
```

Append these test helpers in the same file:

```go
type recordingAppender struct {
	calls    int
	requests []AppendBatchRequest
	itemErrs []error
}

func (a *recordingAppender) AppendBatch(_ context.Context, req AppendBatchRequest) (AppendBatchResult, error) {
	a.calls++
	a.requests = append(a.requests, cloneAppendRequest(req))
	items := make([]AppendBatchItemResult, len(req.Messages))
	for i, msg := range req.Messages {
		msg.MessageSeq = uint64(i + 1)
		items[i] = AppendBatchItemResult{MessageID: msg.MessageID, MessageSeq: msg.MessageSeq, Message: msg}
		if i < len(a.itemErrs) {
			items[i].Err = a.itemErrs[i]
		}
	}
	return AppendBatchResult{Items: items}, nil
}

type sequenceIDs struct {
	next      uint64
	allocated int
}

func (s *sequenceIDs) Next() uint64 {
	id := s.next
	s.next++
	s.allocated++
	return id
}

type failingCommitted struct{ err error }

func (f failingCommitted) Submit(context.Context, messageevents.MessageCommitted) error { return f.err }

type recordingObserver struct{ committedErrors int }

func (o *recordingObserver) CommittedSinkError(SendCommand, error) { o.committedErrors++ }
```

Add the import for `messageevents` in the test file:

```go
import "github.com/WuKongIM/WuKongIM/internalv2/contracts/messageevents"
```

- [ ] **Step 2: Run tests and verify they fail**

Run:

```bash
go test ./internalv2/usecase/message -run 'TestSend|TestCommitted' -count=1
```

Expected: FAIL because `Send`, `SendBatch`, and helpers do not exist.

- [ ] **Step 3: Implement Send and SendBatch**

Create `internalv2/usecase/message/send.go`:

```go
package message

import (
	"context"
	"errors"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/messageevents"
)

// Send processes one send command.
func (a *App) Send(ctx context.Context, cmd SendCommand) (SendResult, error) {
	results := a.SendBatch([]SendBatchItem{{Context: ctx, Command: cmd}})
	if len(results) == 0 {
		return SendResult{}, nil
	}
	return results[0].Result, results[0].Err
}

// SendBatch processes send commands and returns item-aligned results.
func (a *App) SendBatch(items []SendBatchItem) []SendBatchItemResult {
	results := make([]SendBatchItemResult, len(items))
	prepared := make([]preparedSend, 0, len(items))
	for i, item := range items {
		ctx := item.Context
		if ctx == nil {
			ctx = context.Background()
		}
		next, done := a.prepare(ctx, item.Command)
		next.index = i
		next.ctx = ctx
		if done {
			results[i] = SendBatchItemResult{Result: next.result, Err: next.err}
			continue
		}
		prepared = append(prepared, next)
	}
	for _, segment := range splitSegments(prepared) {
		a.appendSegment(segment, results)
	}
	return results
}

type preparedSend struct {
	index  int
	ctx    context.Context
	cmd    SendCommand
	result SendResult
	err    error
}

func (a *App) prepare(ctx context.Context, cmd SendCommand) (preparedSend, bool) {
	if cmd.FromUID == "" {
		return preparedSend{result: SendResult{Reason: ReasonAuthFail}}, true
	}
	if cmd.ChannelID == "" || cmd.ChannelType == 0 || len(cmd.Payload) == 0 {
		return preparedSend{result: SendResult{Reason: ReasonInvalidRequest}}, true
	}
	if cmd.NoPersist {
		return preparedSend{result: SendResult{Reason: ReasonUnsupported}}, true
	}
	if err := ctx.Err(); err != nil {
		return preparedSend{err: err}, true
	}
	if a == nil || a.appender == nil {
		return preparedSend{err: ErrAppenderRequired}, true
	}
	decision, err := a.authorizer.AuthorizeSend(ctx, cmd)
	if err != nil {
		return preparedSend{err: err}, true
	}
	if !decision.Allowed {
		reason := decision.Reason
		if reason == ReasonSuccess {
			reason = ReasonInvalidRequest
		}
		return preparedSend{result: SendResult{Reason: reason}}, true
	}
	if cmd.MessageID == 0 {
		if a.messageID == nil {
			return preparedSend{err: ErrMessageIDAllocatorRequired}, true
		}
		cmd.MessageID = a.messageID.Next()
	}
	return preparedSend{cmd: cloneCommand(cmd)}, false
}

type segment struct {
	channel ChannelID
	items   []preparedSend
}

func splitSegments(items []preparedSend) []segment {
	out := make([]segment, 0, len(items))
	for _, item := range items {
		channel := ChannelID{ID: item.cmd.ChannelID, Type: item.cmd.ChannelType}
		if len(out) > 0 && out[len(out)-1].channel == channel {
			out[len(out)-1].items = append(out[len(out)-1].items, item)
			continue
		}
		out = append(out, segment{channel: channel, items: []preparedSend{item}})
	}
	return out
}

func (a *App) appendSegment(segment segment, results []SendBatchItemResult) {
	req := AppendBatchRequest{ChannelID: segment.channel, CommitMode: CommitModeQuorum, Messages: make([]Message, 0, len(segment.items))}
	for _, item := range segment.items {
		req.Messages = append(req.Messages, Message{
			MessageID:   item.cmd.MessageID,
			ChannelID:   item.cmd.ChannelID,
			ChannelType: item.cmd.ChannelType,
			FromUID:     item.cmd.FromUID,
			ClientMsgNo: item.cmd.ClientMsgNo,
			Payload:     cloneBytes(item.cmd.Payload),
		})
	}
	res, err := a.appender.AppendBatch(segment.items[0].ctx, req)
	if err != nil {
		for _, item := range segment.items {
			results[item.index] = SendBatchItemResult{Err: err}
		}
		return
	}
	for i, item := range segment.items {
		if i >= len(res.Items) {
			results[item.index] = SendBatchItemResult{Err: ErrAppendFailed}
			continue
		}
		appended := res.Items[i]
		if appended.Err != nil {
			results[item.index] = SendBatchItemResult{Result: SendResult{Reason: reasonForAppendError(appended.Err)}}
			continue
		}
		result := SendResult{MessageID: appended.MessageID, MessageSeq: appended.MessageSeq, Reason: ReasonSuccess}
		a.submitCommitted(item.ctx, item.cmd, appended)
		results[item.index] = SendBatchItemResult{Result: result}
	}
}

func (a *App) submitCommitted(ctx context.Context, cmd SendCommand, appended AppendBatchItemResult) {
	if a == nil || a.committed == nil {
		return
	}
	event := messageevents.MessageCommitted{
		MessageID:   appended.MessageID,
		MessageSeq:  appended.MessageSeq,
		ChannelID:   cmd.ChannelID,
		ChannelType: cmd.ChannelType,
		FromUID:     cmd.FromUID,
		ClientMsgNo: cmd.ClientMsgNo,
		Payload:     cloneBytes(appended.Message.Payload),
	}
	if err := a.committed.Submit(ctx, event); err != nil && a.observer != nil {
		a.observer.CommittedSinkError(cmd, err)
	}
}

func reasonForAppendError(err error) Reason {
	switch {
	case errors.Is(err, ErrChannelNotFound):
		return ReasonChannelNotExist
	case errors.Is(err, ErrNotLeader), errors.Is(err, ErrStaleRoute), errors.Is(err, ErrRouteNotReady):
		return ReasonNodeNotMatch
	default:
		return ReasonSystemError
	}
}

func cloneCommand(cmd SendCommand) SendCommand {
	cmd.Payload = cloneBytes(cmd.Payload)
	return cmd
}

func cloneAppendRequest(req AppendBatchRequest) AppendBatchRequest {
	req.Messages = append([]Message(nil), req.Messages...)
	for i := range req.Messages {
		req.Messages[i].Payload = cloneBytes(req.Messages[i].Payload)
	}
	return req
}

func cloneBytes(in []byte) []byte {
	if len(in) == 0 {
		return nil
	}
	out := make([]byte, len(in))
	copy(out, in)
	return out
}
```

- [ ] **Step 4: Run tests and formatting**

Run:

```bash
gofmt -w internalv2/usecase/message
go test ./internalv2/usecase/message -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internalv2/usecase/message
git commit -m "feat(internalv2): implement message send batch usecase"
```

## Task 3: Gateway Adapter And Sendack Mapping

**Files:**
- Create: `internalv2/access/gateway/handler.go`
- Create: `internalv2/access/gateway/mapper.go`
- Create: `internalv2/access/gateway/error_map.go`
- Create: `internalv2/access/gateway/batch.go`
- Test: `internalv2/access/gateway/handler_test.go`

- [ ] **Step 1: Write failing gateway tests**

Create `internalv2/access/gateway/handler_test.go`:

```go
package gateway

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	coregateway "github.com/WuKongIM/WuKongIM/pkg/gateway"
	gatewaysession "github.com/WuKongIM/WuKongIM/pkg/gateway/session"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestHandlerSendWritesSuccessSendack(t *testing.T) {
	messages := &recordingMessages{sendResult: message.SendResult{MessageID: 99, MessageSeq: 7, Reason: message.ReasonSuccess}}
	handler := New(Options{Messages: messages})
	ctx, written := testContext(t, "u1")
	err := handler.OnFrame(ctx, &frame.SendPacket{ClientSeq: 3, ClientMsgNo: "m1", ChannelID: "room", ChannelType: frame.ChannelTypeGroup, Payload: []byte("hello")})
	if err != nil {
		t.Fatalf("OnFrame() error = %v", err)
	}
	if len(messages.commands) != 1 {
		t.Fatalf("commands = %d, want 1", len(messages.commands))
	}
	cmd := messages.commands[0]
	if cmd.FromUID != "u1" || cmd.MessageID != 0 || cmd.ChannelID != "room" || string(cmd.Payload) != "hello" {
		t.Fatalf("mapped command = %#v", cmd)
	}
	ack, ok := written.last().(*frame.SendackPacket)
	if !ok {
		t.Fatalf("written frame = %T, want SendackPacket", written.last())
	}
	if ack.ClientSeq != 3 || ack.ClientMsgNo != "m1" || ack.MessageID != 99 || ack.MessageSeq != 7 || ack.ReasonCode != frame.ReasonSuccess {
		t.Fatalf("ack = %#v", ack)
	}
}

func TestHandlerSendRejectsUnauthenticatedWithSendack(t *testing.T) {
	handler := New(Options{Messages: &recordingMessages{}})
	ctx, written := testContext(t, "")
	err := handler.OnFrame(ctx, &frame.SendPacket{ClientSeq: 1, ClientMsgNo: "m1", ChannelID: "room", ChannelType: frame.ChannelTypeGroup, Payload: []byte("hello")})
	if err != nil {
		t.Fatalf("OnFrame() error = %v", err)
	}
	ack := written.last().(*frame.SendackPacket)
	if ack.ReasonCode != frame.ReasonAuthFail {
		t.Fatalf("reason = %v, want auth fail", ack.ReasonCode)
	}
}

func TestHandlerUnknownFrameReturnsUnsupported(t *testing.T) {
	handler := New(Options{Messages: &recordingMessages{}})
	ctx, _ := testContext(t, "u1")
	err := handler.OnFrame(ctx, &frame.PingPacket{})
	if !errors.Is(err, ErrUnsupportedFrame) {
		t.Fatalf("OnFrame() error = %v, want unsupported", err)
	}
}

func TestHandlerSendBatchWritesAlignedSendacks(t *testing.T) {
	messages := &recordingMessages{
		batchResults: []message.SendBatchItemResult{
			{Result: message.SendResult{MessageID: 10, MessageSeq: 1, Reason: message.ReasonSuccess}},
			{Result: message.SendResult{Reason: message.ReasonNodeNotMatch}},
		},
	}
	handler := New(Options{Messages: messages})
	ctx1, written1 := testContext(t, "u1")
	ctx2, written2 := testContext(t, "u1")
	err := handler.OnSendBatch([]coregateway.SendBatchItem{
		{Context: ctx1, Frame: &frame.SendPacket{ClientSeq: 1, ClientMsgNo: "m1", ChannelID: "room", ChannelType: frame.ChannelTypeGroup, Payload: []byte("one")}},
		{Context: ctx2, Frame: &frame.SendPacket{ClientSeq: 2, ClientMsgNo: "m2", ChannelID: "room", ChannelType: frame.ChannelTypeGroup, Payload: []byte("two")}},
	})
	if err != nil {
		t.Fatalf("OnSendBatch() error = %v", err)
	}
	if got := written1.last().(*frame.SendackPacket); got.MessageID != 10 || got.MessageSeq != 1 || got.ReasonCode != frame.ReasonSuccess {
		t.Fatalf("first ack = %#v", got)
	}
	if got := written2.last().(*frame.SendackPacket); got.ReasonCode != frame.ReasonNodeNotMatch {
		t.Fatalf("second ack = %#v", got)
	}
}
```

Append test helpers:

```go
type recordingMessages struct {
	commands     []message.SendCommand
	sendResult   message.SendResult
	sendErr      error
	batchResults []message.SendBatchItemResult
}

func (r *recordingMessages) Send(_ context.Context, cmd message.SendCommand) (message.SendResult, error) {
	r.commands = append(r.commands, cmd)
	return r.sendResult, r.sendErr
}

func (r *recordingMessages) SendBatch(items []message.SendBatchItem) []message.SendBatchItemResult {
	for _, item := range items {
		r.commands = append(r.commands, item.Command)
	}
	if r.batchResults != nil {
		return r.batchResults
	}
	out := make([]message.SendBatchItemResult, len(items))
	for i := range out {
		out[i].Result = r.sendResult
	}
	return out
}

type writtenFrames struct{ frames []frame.Frame }

func (w *writtenFrames) last() frame.Frame {
	if len(w.frames) == 0 {
		return nil
	}
	return w.frames[len(w.frames)-1]
}

func testContext(t *testing.T, uid string) (coregateway.Context, *writtenFrames) {
	t.Helper()
	written := &writtenFrames{}
	sess := gatewaysession.New(gatewaysession.Config{
		ID: 1,
		WriteFrameFn: func(f frame.Frame, _ gatewaysession.OutboundMeta) error {
			written.frames = append(written.frames, f)
			return nil
		},
	})
	if uid != "" {
		sess.SetValue(coregateway.SessionValueUID, uid)
	}
	return coregateway.Context{Session: sess, RequestContext: context.Background()}, written
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run:

```bash
go test ./internalv2/access/gateway -count=1
```

Expected: FAIL because the package implementation does not exist.

- [ ] **Step 3: Implement handler shell and interfaces**

Create `internalv2/access/gateway/handler.go`:

```go
package gateway

import (
	"context"
	"errors"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	coregateway "github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

var (
	ErrUnsupportedFrame        = errors.New("internalv2/access/gateway: unsupported frame")
	ErrUnauthenticatedSession  = errors.New("internalv2/access/gateway: unauthenticated session")
	ErrMissingRequestContext   = errors.New("internalv2/access/gateway: missing request context")
)

const defaultSendTimeout = 10 * time.Second

type MessageUsecase interface {
	Send(context.Context, message.SendCommand) (message.SendResult, error)
}

type MessageBatchUsecase interface {
	SendBatch([]message.SendBatchItem) []message.SendBatchItemResult
}

type Options struct {
	Messages    MessageUsecase
	SendTimeout time.Duration
}

type Handler struct {
	messages    MessageUsecase
	sendTimeout time.Duration
}

func New(opts Options) *Handler {
	if opts.SendTimeout <= 0 {
		opts.SendTimeout = defaultSendTimeout
	}
	return &Handler{messages: opts.Messages, sendTimeout: opts.SendTimeout}
}

func (h *Handler) OnListenerError(string, error) {}
func (h *Handler) OnSessionOpen(coregateway.Context) error { return nil }
func (h *Handler) OnSessionClose(coregateway.Context) error { return nil }
func (h *Handler) OnSessionError(coregateway.Context, error) {}

func (h *Handler) OnFrame(ctx coregateway.Context, f frame.Frame) error {
	switch pkt := f.(type) {
	case *frame.SendPacket:
		return h.handleSend(&ctx, pkt)
	default:
		return ErrUnsupportedFrame
	}
}
```

- [ ] **Step 4: Implement mapper and reason mapping**

Create `internalv2/access/gateway/error_map.go`:

```go
package gateway

import (
	"context"
	"errors"

	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func reasonCode(reason message.Reason) frame.ReasonCode {
	switch reason {
	case message.ReasonSuccess:
		return frame.ReasonSuccess
	case message.ReasonAuthFail:
		return frame.ReasonAuthFail
	case message.ReasonChannelNotExist:
		return frame.ReasonChannelNotExist
	case message.ReasonNodeNotMatch:
		return frame.ReasonNodeNotMatch
	case message.ReasonInvalidRequest, message.ReasonUnsupported:
		return frame.ReasonPayloadDecodeError
	default:
		return frame.ReasonSystemError
	}
}

func reasonForError(err error) (message.Reason, bool) {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return message.ReasonSystemError, true
	default:
		return 0, false
	}
}
```

Create `internalv2/access/gateway/mapper.go`:

```go
package gateway

import (
	"context"

	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	coregateway "github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func (h *Handler) handleSend(ctx *coregateway.Context, pkt *frame.SendPacket) error {
	cmd, err := mapSendCommand(ctx, pkt)
	if err != nil {
		return writeSendack(ctx, pkt, message.SendResult{Reason: message.ReasonAuthFail})
	}
	if ctx == nil || ctx.RequestContext == nil {
		return writeSendack(ctx, pkt, message.SendResult{Reason: message.ReasonSystemError})
	}
	reqCtx, cancel := context.WithTimeout(ctx.RequestContext, h.sendTimeout)
	defer cancel()
	result, err := h.messages.Send(reqCtx, cmd)
	if err != nil {
		if reason, ok := reasonForError(err); ok {
			result.Reason = reason
			return writeSendack(ctx, pkt, result)
		}
		return err
	}
	return writeSendack(ctx, pkt, result)
}

func mapSendCommand(ctx *coregateway.Context, pkt *frame.SendPacket) (message.SendCommand, error) {
	if ctx == nil || ctx.Session == nil {
		return message.SendCommand{}, ErrUnauthenticatedSession
	}
	uid, _ := ctx.Session.Value(coregateway.SessionValueUID).(string)
	if uid == "" {
		return message.SendCommand{}, ErrUnauthenticatedSession
	}
	version := uint8(frame.LatestVersion)
	if v, ok := ctx.Session.Value(coregateway.SessionValueProtocolVersion).(uint8); ok && v != 0 {
		version = v
	}
	if pkt == nil {
		return message.SendCommand{FromUID: uid, SenderSessionID: ctx.Session.ID(), ProtocolVersion: version}, nil
	}
	return message.SendCommand{
		FromUID:         uid,
		SenderSessionID: ctx.Session.ID(),
		ClientSeq:       pkt.ClientSeq,
		ClientMsgNo:     pkt.ClientMsgNo,
		ChannelID:       pkt.ChannelID,
		ChannelType:     pkt.ChannelType,
		Payload:         append([]byte(nil), pkt.Payload...),
		NoPersist:       pkt.Framer.NoPersist,
		SyncOnce:        pkt.Framer.SyncOnce,
		MessageID:       0,
		ProtocolVersion: version,
	}, nil
}

func writeSendack(ctx *coregateway.Context, pkt *frame.SendPacket, result message.SendResult) error {
	if ctx == nil || ctx.Session == nil {
		return ErrUnauthenticatedSession
	}
	var clientSeq uint64
	var clientMsgNo string
	if pkt != nil {
		clientSeq = pkt.ClientSeq
		clientMsgNo = pkt.ClientMsgNo
	}
	return ctx.WriteFrame(&frame.SendackPacket{
		MessageID:   int64(result.MessageID),
		MessageSeq:  result.MessageSeq,
		ClientSeq:   clientSeq,
		ClientMsgNo: clientMsgNo,
		ReasonCode:  reasonCode(result.Reason),
	})
}
```

- [ ] **Step 5: Implement batch handling**

Create `internalv2/access/gateway/batch.go`:

```go
package gateway

import (
	"context"
	"errors"

	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	coregateway "github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func (h *Handler) OnSendBatch(items []coregateway.SendBatchItem) error {
	if len(items) == 0 {
		return nil
	}
	results := make([]message.SendResult, len(items))
	validIndexes := make([]int, 0, len(items))
	validItems := make([]message.SendBatchItem, 0, len(items))
	for i, item := range items {
		ctx := item.Context
		if item.ReplyToken != "" {
			ctx.ReplyToken = item.ReplyToken
		}
		cmd, err := mapSendCommand(&ctx, item.Frame)
		if err != nil {
			results[i] = message.SendResult{Reason: message.ReasonAuthFail}
			continue
		}
		if ctx.RequestContext == nil {
			results[i] = message.SendResult{Reason: message.ReasonSystemError}
			continue
		}
		reqCtx, cancel := context.WithTimeout(ctx.RequestContext, h.sendTimeout)
		defer cancel()
		validIndexes = append(validIndexes, i)
		validItems = append(validItems, message.SendBatchItem{Context: reqCtx, Command: cmd})
	}
	if len(validItems) > 0 {
		batcher, ok := h.messages.(MessageBatchUsecase)
		if !ok {
			return errors.New("internalv2/access/gateway: message usecase does not implement SendBatch")
		}
		batchResults := batcher.SendBatch(validItems)
		for j, itemIndex := range validIndexes {
			if j >= len(batchResults) {
				return errors.New("internalv2/access/gateway: send batch result count mismatch")
			}
			result := batchResults[j].Result
			if batchResults[j].Err != nil {
				if reason, ok := reasonForError(batchResults[j].Err); ok {
					result.Reason = reason
				} else {
					return batchResults[j].Err
				}
			}
			results[itemIndex] = result
		}
	}
	for i, item := range items {
		ctx := item.Context
		if item.ReplyToken != "" {
			ctx.ReplyToken = item.ReplyToken
		}
		if err := writeSendack(&ctx, item.Frame, results[i]); err != nil {
			return err
		}
	}
	return nil
}

var _ coregateway.Handler = (*Handler)(nil)
var _ coregateway.SendBatchHandler = (*Handler)(nil)
var _ = frame.ReasonSuccess
```

- [ ] **Step 6: Run tests and formatting**

Run:

```bash
gofmt -w internalv2/access/gateway
go test ./internalv2/access/gateway -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internalv2/access/gateway
git commit -m "feat(internalv2): add gateway sendack adapter"
```

## Task 4: clusterv2 Channel Appender Adapter

**Files:**
- Create: `internalv2/infra/cluster/appender.go`
- Create: `internalv2/infra/cluster/error_map.go`
- Test: `internalv2/infra/cluster/appender_test.go`

- [ ] **Step 1: Write failing adapter tests**

Create `internalv2/infra/cluster/appender_test.go`:

```go
package cluster

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
)

func TestChannelAppenderMapsAppendBatchRequestAndResult(t *testing.T) {
	node := &recordingNode{}
	appender := NewChannelAppender(node)
	res, err := appender.AppendBatch(context.Background(), message.AppendBatchRequest{
		ChannelID: message.ChannelID{ID: "room", Type: 1},
		Messages: []message.Message{{MessageID: 10, FromUID: "u1", ClientMsgNo: "m1", Payload: []byte("hello")}},
		CommitMode: message.CommitModeQuorum,
	})
	if err != nil {
		t.Fatalf("AppendBatch() error = %v", err)
	}
	if node.calls != 1 {
		t.Fatalf("calls = %d, want 1", node.calls)
	}
	req := node.last
	if req.ChannelID.ID != "room" || req.ChannelID.Type != 1 || req.CommitMode != channelv2.CommitModeQuorum {
		t.Fatalf("mapped request = %#v", req)
	}
	if req.Messages[0].MessageID != 10 || req.Messages[0].FromUID != "u1" || string(req.Messages[0].Payload) != "hello" {
		t.Fatalf("mapped message = %#v", req.Messages[0])
	}
	if len(res.Items) != 1 || res.Items[0].MessageID != 10 || res.Items[0].MessageSeq != 1 {
		t.Fatalf("result = %#v", res)
	}
}

func TestChannelAppenderMapsTypedErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want error
	}{
		{name: "clusterv2 not leader", err: clusterv2.ErrNotLeader, want: message.ErrNotLeader},
		{name: "channelv2 stale", err: channelv2.ErrStaleMeta, want: message.ErrStaleRoute},
		{name: "channel missing", err: channelv2.ErrChannelNotFound, want: message.ErrChannelNotFound},
		{name: "backpressure", err: channelv2.ErrBackpressured, want: message.ErrBackpressured},
		{name: "route not ready", err: clusterv2.ErrRouteNotReady, want: message.ErrRouteNotReady},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			appender := NewChannelAppender(&recordingNode{err: tc.err})
			_, err := appender.AppendBatch(context.Background(), message.AppendBatchRequest{
				ChannelID: message.ChannelID{ID: "room", Type: 1},
				Messages: []message.Message{{MessageID: 1, Payload: []byte("x")}},
			})
			if !errors.Is(err, tc.want) {
				t.Fatalf("AppendBatch() error = %v, want %v", err, tc.want)
			}
		})
	}
}

type recordingNode struct {
	calls int
	last  channelv2.AppendBatchRequest
	err   error
}

func (n *recordingNode) AppendChannelBatch(_ context.Context, req channelv2.AppendBatchRequest) (channelv2.AppendBatchResult, error) {
	n.calls++
	n.last = req
	if n.err != nil {
		return channelv2.AppendBatchResult{}, n.err
	}
	items := make([]channelv2.AppendBatchItemResult, len(req.Messages))
	for i, msg := range req.Messages {
		msg.MessageSeq = uint64(i + 1)
		items[i] = channelv2.AppendBatchItemResult{MessageID: msg.MessageID, MessageSeq: msg.MessageSeq, Message: msg}
	}
	return channelv2.AppendBatchResult{Items: items}, nil
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run:

```bash
go test ./internalv2/infra/cluster -count=1
```

Expected: FAIL because the adapter package does not exist.

- [ ] **Step 3: Implement adapter**

Create `internalv2/infra/cluster/appender.go`:

```go
package cluster

import (
	"context"

	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2"
)

// ChannelAppendNode is the clusterv2 append surface used by internalv2.
type ChannelAppendNode interface {
	AppendChannelBatch(context.Context, channelv2.AppendBatchRequest) (channelv2.AppendBatchResult, error)
}

// ChannelAppender adapts clusterv2 channel append to the message usecase port.
type ChannelAppender struct {
	node ChannelAppendNode
}

// NewChannelAppender creates a ChannelAppender.
func NewChannelAppender(node ChannelAppendNode) *ChannelAppender {
	return &ChannelAppender{node: node}
}

// AppendBatch appends a message batch through clusterv2.
func (a *ChannelAppender) AppendBatch(ctx context.Context, req message.AppendBatchRequest) (message.AppendBatchResult, error) {
	if a == nil || a.node == nil {
		return message.AppendBatchResult{}, message.ErrAppenderRequired
	}
	res, err := a.node.AppendChannelBatch(ctx, channelv2.AppendBatchRequest{
		ChannelID:  channelv2.ChannelID{ID: req.ChannelID.ID, Type: req.ChannelID.Type},
		Messages:   toChannelMessages(req.Messages),
		CommitMode: toChannelCommitMode(req.CommitMode),
	})
	if err != nil {
		return message.AppendBatchResult{}, mapAppendError(err)
	}
	return fromChannelAppendResult(res), nil
}

func toChannelMessages(in []message.Message) []channelv2.Message {
	out := make([]channelv2.Message, 0, len(in))
	for _, msg := range in {
		out = append(out, channelv2.Message{
			MessageID:   msg.MessageID,
			MessageSeq:  msg.MessageSeq,
			ChannelID:   msg.ChannelID,
			ChannelType: msg.ChannelType,
			FromUID:     msg.FromUID,
			ClientMsgNo: msg.ClientMsgNo,
			Payload:     append([]byte(nil), msg.Payload...),
		})
	}
	return out
}

func toChannelCommitMode(mode message.CommitMode) channelv2.CommitMode {
	if mode == message.CommitModeLocal {
		return channelv2.CommitModeLocal
	}
	return channelv2.CommitModeQuorum
}

func fromChannelAppendResult(res channelv2.AppendBatchResult) message.AppendBatchResult {
	items := make([]message.AppendBatchItemResult, 0, len(res.Items))
	for _, item := range res.Items {
		items = append(items, message.AppendBatchItemResult{
			MessageID:  item.MessageID,
			MessageSeq: item.MessageSeq,
			Message:    fromChannelMessage(item.Message),
			Err:        mapAppendError(item.Err),
		})
	}
	return message.AppendBatchResult{Items: items}
}

func fromChannelMessage(msg channelv2.Message) message.Message {
	return message.Message{
		MessageID:   msg.MessageID,
		MessageSeq:  msg.MessageSeq,
		ChannelID:   msg.ChannelID,
		ChannelType: msg.ChannelType,
		FromUID:     msg.FromUID,
		ClientMsgNo: msg.ClientMsgNo,
		Payload:     append([]byte(nil), msg.Payload...),
	}
}
```

Create `internalv2/infra/cluster/error_map.go`:

```go
package cluster

import (
	"context"
	"errors"
	"fmt"

	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
)

func mapAppendError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, channelv2.ErrNotLeader), errors.Is(err, clusterv2.ErrNotLeader):
		return fmt.Errorf("%w: %v", message.ErrNotLeader, err)
	case errors.Is(err, channelv2.ErrStaleMeta):
		return fmt.Errorf("%w: %v", message.ErrStaleRoute, err)
	case errors.Is(err, channelv2.ErrChannelNotFound):
		return fmt.Errorf("%w: %v", message.ErrChannelNotFound, err)
	case errors.Is(err, channelv2.ErrBackpressured):
		return fmt.Errorf("%w: %v", message.ErrBackpressured, err)
	case errors.Is(err, clusterv2.ErrRouteNotReady), errors.Is(err, channelv2.ErrNotReady):
		return fmt.Errorf("%w: %v", message.ErrRouteNotReady, err)
	default:
		return fmt.Errorf("%w: %v", message.ErrAppendFailed, err)
	}
}
```

- [ ] **Step 4: Run tests and formatting**

Run:

```bash
gofmt -w internalv2/infra/cluster
go test ./internalv2/infra/cluster -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internalv2/infra/cluster
git commit -m "feat(internalv2): adapt message append to clusterv2"
```

## Task 5: App Composition And Lifecycle

**Files:**
- Create: `internalv2/app/config.go`
- Create: `internalv2/app/app.go`
- Create: `internalv2/app/lifecycle.go`
- Test: `internalv2/app/app_test.go`

- [ ] **Step 1: Write failing lifecycle tests**

Create `internalv2/app/app_test.go`:

```go
package app

import (
	"context"
	"errors"
	"testing"
)

func TestStartOrderIsClusterThenGateway(t *testing.T) {
	var calls []string
	cluster := &fakeCluster{start: func(context.Context) error { calls = append(calls, "cluster.start"); return nil }}
	gateway := &fakeGateway{start: func() error { calls = append(calls, "gateway.start"); return nil }}
	app, err := New(Config{NodeID: 1, DataDir: t.TempDir()}, WithCluster(cluster), WithGateway(gateway))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := app.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if got := joinCalls(calls); got != "cluster.start,gateway.start" {
		t.Fatalf("calls = %s", got)
	}
}

func TestGatewayStartFailureStopsCluster(t *testing.T) {
	var calls []string
	cluster := &fakeCluster{
		start: func(context.Context) error { calls = append(calls, "cluster.start"); return nil },
		stop:  func(context.Context) error { calls = append(calls, "cluster.stop"); return nil },
	}
	gateway := &fakeGateway{start: func() error { calls = append(calls, "gateway.start"); return errors.New("gateway down") }}
	app, err := New(Config{NodeID: 1, DataDir: t.TempDir()}, WithCluster(cluster), WithGateway(gateway))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := app.Start(context.Background()); err == nil {
		t.Fatal("Start() error = nil, want gateway failure")
	}
	if got := joinCalls(calls); got != "cluster.start,gateway.start,cluster.stop" {
		t.Fatalf("calls = %s", got)
	}
}

func TestStopOrderIsGatewayThenCluster(t *testing.T) {
	var calls []string
	cluster := &fakeCluster{
		start: func(context.Context) error { calls = append(calls, "cluster.start"); return nil },
		stop:  func(context.Context) error { calls = append(calls, "cluster.stop"); return nil },
	}
	gateway := &fakeGateway{
		start: func() error { calls = append(calls, "gateway.start"); return nil },
		stop:  func() error { calls = append(calls, "gateway.stop"); return nil },
	}
	app, err := New(Config{NodeID: 1, DataDir: t.TempDir()}, WithCluster(cluster), WithGateway(gateway))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := app.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := app.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if got := joinCalls(calls); got != "cluster.start,gateway.start,gateway.stop,cluster.stop" {
		t.Fatalf("calls = %s", got)
	}
}
```

Append test helpers:

```go
type fakeCluster struct {
	start func(context.Context) error
	stop  func(context.Context) error
}

func (f *fakeCluster) Start(ctx context.Context) error {
	if f.start != nil { return f.start(ctx) }
	return nil
}

func (f *fakeCluster) Stop(ctx context.Context) error {
	if f.stop != nil { return f.stop(ctx) }
	return nil
}

type fakeGateway struct {
	start func() error
	stop  func() error
}

func (f *fakeGateway) Start() error {
	if f.start != nil { return f.start() }
	return nil
}

func (f *fakeGateway) Stop() error {
	if f.stop != nil { return f.stop() }
	return nil
}

func joinCalls(calls []string) string {
	out := ""
	for i, call := range calls {
		if i > 0 { out += "," }
		out += call
	}
	return out
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run:

```bash
go test ./internalv2/app -run 'Test(Start|Gateway|Stop)' -count=1
```

Expected: FAIL because `internalv2/app` does not exist.

- [ ] **Step 3: Implement config and composition root**

Create `internalv2/app/config.go`:

```go
package app

import (
	"errors"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
	"github.com/WuKongIM/WuKongIM/pkg/gateway"
)

var (
	ErrInvalidConfig  = errors.New("internalv2/app: invalid config")
	ErrAlreadyStarted = errors.New("internalv2/app: already started")
	ErrStopped        = errors.New("internalv2/app: stopped")
)

// Config contains phase-1 internalv2 app configuration.
type Config struct {
	// NodeID is the stable node id.
	NodeID uint64
	// DataDir is the root data directory.
	DataDir string
	// Cluster configures clusterv2.
	Cluster clusterv2.Config
	// Gateway configures the client gateway.
	Gateway GatewayConfig
	// Message configures message send behavior.
	Message MessageConfig
}

// GatewayConfig contains client gateway settings.
type GatewayConfig struct {
	Listeners   []gateway.ListenerOptions
	Session     gateway.SessionOptions
	Transport   gateway.TransportOptions
	SendTimeout time.Duration
}

// MessageConfig contains message usecase settings.
type MessageConfig struct{}
```

Create `internalv2/app/app.go`:

```go
package app

import (
	"context"
	"sync/atomic"

	accessgateway "github.com/WuKongIM/WuKongIM/internalv2/access/gateway"
	clusterinfra "github.com/WuKongIM/WuKongIM/internalv2/infra/cluster"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
	"github.com/WuKongIM/WuKongIM/pkg/gateway"
)

type ClusterRuntime interface {
	Start(context.Context) error
	Stop(context.Context) error
}

type GatewayRuntime interface {
	Start() error
	Stop() error
}

type Option func(*App)

type App struct {
	cfg      Config
	cluster  ClusterRuntime
	gateway  GatewayRuntime
	handler  *accessgateway.Handler
	messages *message.App
	started  atomic.Bool
	stopped  atomic.Bool
}

func New(cfg Config, opts ...Option) (*App, error) {
	app := &App{cfg: cfg}
	for _, opt := range opts {
		if opt != nil {
			opt(app)
		}
	}
	if app.cluster == nil {
		node, err := clusterv2.New(defaultClusterConfig(cfg))
		if err != nil {
			return nil, err
		}
		app.cluster = node
		app.messages = message.New(message.Options{
			Appender:  clusterinfra.NewChannelAppender(node),
			MessageID: newNodeMessageIDs(cfg.NodeID),
		})
	}
	if app.messages == nil {
		app.messages = message.New(message.Options{MessageID: newNodeMessageIDs(cfg.NodeID)})
	}
	if app.handler == nil {
		app.handler = accessgateway.New(accessgateway.Options{Messages: app.messages, SendTimeout: cfg.Gateway.SendTimeout})
	}
	if app.gateway == nil && len(cfg.Gateway.Listeners) > 0 {
		gw, err := gateway.New(gateway.Options{Handler: app.handler, Listeners: cfg.Gateway.Listeners, DefaultSession: cfg.Gateway.Session, Transport: cfg.Gateway.Transport})
		if err != nil {
			return nil, err
		}
		app.gateway = gw
	}
	return app, nil
}

func WithCluster(cluster ClusterRuntime) Option {
	return func(a *App) { a.cluster = cluster }
}

func WithGateway(gateway GatewayRuntime) Option {
	return func(a *App) { a.gateway = gateway }
}

func WithMessages(messages *message.App) Option {
	return func(a *App) { a.messages = messages }
}

func (a *App) Handler() *accessgateway.Handler { if a == nil { return nil }; return a.handler }
func (a *App) Messages() *message.App { if a == nil { return nil }; return a.messages }

func defaultClusterConfig(cfg Config) clusterv2.Config {
	cluster := cfg.Cluster
	if cluster.NodeID == 0 {
		cluster.NodeID = cfg.NodeID
	}
	if cluster.DataDir == "" {
		cluster.DataDir = cfg.DataDir
	}
	return cluster
}

type nodeMessageIDs struct{ next atomic.Uint64 }

func newNodeMessageIDs(nodeID uint64) *nodeMessageIDs {
	g := &nodeMessageIDs{}
	g.next.Store(nodeID << 48)
	return g
}

func (g *nodeMessageIDs) Next() uint64 { return g.next.Add(1) }
```

- [ ] **Step 4: Implement lifecycle**

Create `internalv2/app/lifecycle.go`:

```go
package app

import (
	"context"
	"errors"
)

func (a *App) Start(ctx context.Context) error {
	if a == nil || a.cluster == nil {
		return ErrInvalidConfig
	}
	if a.stopped.Load() {
		return ErrStopped
	}
	if !a.started.CompareAndSwap(false, true) {
		return ErrAlreadyStarted
	}
	if err := a.cluster.Start(ctx); err != nil {
		a.started.Store(false)
		return err
	}
	if a.gateway != nil {
		if err := a.gateway.Start(); err != nil {
			stopErr := a.cluster.Stop(ctx)
			a.started.Store(false)
			return errors.Join(err, stopErr)
		}
	}
	return nil
}

func (a *App) Stop(ctx context.Context) error {
	if a == nil {
		return nil
	}
	a.stopped.Store(true)
	if !a.started.Swap(false) {
		return nil
	}
	var err error
	if a.gateway != nil {
		err = errors.Join(err, a.gateway.Stop())
	}
	if a.cluster != nil {
		err = errors.Join(err, a.cluster.Stop(ctx))
	}
	return err
}
```

- [ ] **Step 5: Run tests and formatting**

Run:

```bash
gofmt -w internalv2/app
go test ./internalv2/app -run 'Test(Start|Gateway|Stop)' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internalv2/app
git commit -m "feat(internalv2): add app lifecycle skeleton"
```

## Task 6: Single-Node-Cluster Sendack Smoke

**Files:**
- Modify: `internalv2/app/sendack_smoke_test.go`

- [ ] **Step 1: Write handler-level smoke test**

Create `internalv2/app/sendack_smoke_test.go`:

```go
package app

import (
	"context"
	"testing"

	accessgateway "github.com/WuKongIM/WuKongIM/internalv2/access/gateway"
	clusterinfra "github.com/WuKongIM/WuKongIM/internalv2/infra/cluster"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/channels"
	coregateway "github.com/WuKongIM/WuKongIM/pkg/gateway"
	gatewaysession "github.com/WuKongIM/WuKongIM/pkg/gateway/session"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestSingleNodeClusterSendPacketToSendack(t *testing.T) {
	channelID := channelv2.ChannelID{ID: "room", Type: frame.ChannelTypeGroup}
	svc, err := channels.NewService(channels.Config{
		LocalNode: 1,
		Store: channelstore.NewMemoryFactory(),
		MetaSource: channels.NewStaticMetaSource([]channelv2.Meta{{
			Key:         channelv2.ChannelKeyForID(channelID),
			ID:          channelID,
			Epoch:       1,
			LeaderEpoch: 1,
			Leader:      1,
			Replicas:    []channelv2.NodeID{1},
			ISR:         []channelv2.NodeID{1},
			MinISR:      1,
			Status:      channelv2.StatusActive,
		}}),
	})
	if err != nil {
		t.Fatalf("channels.NewService() error = %v", err)
	}
	node, err := clusterv2.New(clusterv2.Config{NodeID: 1, ListenAddr: "127.0.0.1:0", DataDir: t.TempDir()}, clusterv2.WithChannels(svc))
	if err != nil {
		t.Fatalf("clusterv2.New() error = %v", err)
	}
	appender := clusterinfra.NewChannelAppender(node)
	messages := message.New(message.Options{Appender: appender, MessageID: newNodeMessageIDs(1)})
	handler := accessgateway.New(accessgateway.Options{Messages: messages})
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("node.Start() error = %v", err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })

	written := &smokeWrittenFrames{}
	sess := gatewaysession.New(gatewaysession.Config{
		ID: 1,
		WriteFrameFn: func(f frame.Frame, _ gatewaysession.OutboundMeta) error {
			written.frames = append(written.frames, f)
			return nil
		},
	})
	sess.SetValue(coregateway.SessionValueUID, "u1")
	ctx := coregateway.Context{Session: sess, RequestContext: context.Background()}
	err = handler.OnFrame(ctx, &frame.SendPacket{ClientSeq: 1, ClientMsgNo: "m1", ChannelID: "room", ChannelType: frame.ChannelTypeGroup, Payload: []byte("hello")})
	if err != nil {
		t.Fatalf("OnFrame() error = %v", err)
	}
	ack, ok := written.frames[len(written.frames)-1].(*frame.SendackPacket)
	if !ok {
		t.Fatalf("last frame = %T, want SendackPacket", written.frames[len(written.frames)-1])
	}
	if ack.ReasonCode != frame.ReasonSuccess || ack.MessageID == 0 || ack.MessageSeq == 0 {
		t.Fatalf("ack = %#v, want success with non-zero id and seq", ack)
	}
}

type smokeWrittenFrames struct {
	frames []frame.Frame
}
```

- [ ] **Step 2: Run smoke and verify behavior**

Run:

```bash
go test ./internalv2/app -run TestSingleNodeClusterSendPacketToSendack -count=1
```

Expected: PASS. If this fails because `clusterv2.WithChannels` is not sufficient to load/apply channel metadata, first inspect `pkg/clusterv2/channels.Service.AppendBatch` and adapt the smoke setup by applying metadata through the service before sending. Do not add a non-cluster append bypass.

- [ ] **Step 3: Run all internalv2 tests**

Run:

```bash
go test ./internalv2/... -count=1
```

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internalv2/app/sendack_smoke_test.go
git commit -m "test(internalv2): cover send packet to sendack smoke"
```

## Task 7: Flow Docs, AGENTS Directory, And Project Knowledge

**Files:**
- Create: `internalv2/FLOW.md`
- Modify: `AGENTS.md`
- Modify: `docs/development/PROJECT_KNOWLEDGE.md`

- [ ] **Step 1: Add internalv2 flow doc**

Create `internalv2/FLOW.md`:

```markdown
# internalv2 Flow

## Responsibility

`internalv2` is a parallel business kernel for the next WuKongIM internal
architecture. Phase 1 proves the client `SEND -> SENDACK` path through
`pkg/clusterv2` and `pkg/channelv2` without replacing the existing `internal`
production stack.

## Package Boundaries

| Package | Responsibility |
|---------|----------------|
| `app` | Single composition root for config, dependency wiring, and lifecycle. |
| `access/gateway` | WKProto gateway adapter: frame mapping, Sendack writing, and reason mapping. |
| `usecase/message` | Entry-agnostic send usecase, batch orchestration, and ports. |
| `infra/cluster` | clusterv2/channelv2 adapter for the message append port. |
| `contracts/messageevents` | Committed message event DTOs for later delivery and conversation migration. |

## Send Flow

```text
pkg/gateway
  -> access/gateway.Handler
  -> usecase/message.App.SendBatch
  -> infra/cluster.ChannelAppender
  -> pkg/clusterv2.Node.AppendChannelBatch
  -> pkg/channelv2
  -> SendackPacket
```

## Rules

- `usecase/message` must not import gateway, protocol frame, clusterv2,
  channelv2, access, or app packages.
- Single-node deployment remains a single-node cluster.
- Phase 1 does not implement delivery, conversation projection, CMD sync,
  plugin hooks, manager APIs, or `cmd/wukongim` wiring.
```

- [ ] **Step 2: Update AGENTS directory structure**

In `AGENTS.md`, add under `internal/` or immediately after the current
`internal/` section:

```text
internalv2/              新版并行 internal 架构骨架；phase1 通过 clusterv2/channelv2 跑通 send -> sendack
  app/                   internalv2 唯一组合根，负责配置、依赖装配和生命周期
  access/gateway/        WKProto 网关适配，负责 frame 映射、sendack 写回和错误 reason 映射
  usecase/message/       入口无关 SEND 用例，批量编排和 ports
  infra/cluster/         clusterv2/channelv2 append 适配
  contracts/             committed message 等跨用例事件合约
```

- [ ] **Step 3: Record concise project knowledge**

Append to `docs/development/PROJECT_KNOWLEDGE.md` under `## Channel Runtime`:

```markdown
- `internalv2` phase 1 is a parallel send-to-sendack stack: gateway maps WKProto SEND to an entry-agnostic message usecase, and durable append goes through `pkg/clusterv2.Node.AppendChannelBatch`; it does not replace `internal` or bypass cluster semantics.
```

- [ ] **Step 4: Run docs and package verification**

Run:

```bash
git diff --check -- internalv2/FLOW.md AGENTS.md docs/development/PROJECT_KNOWLEDGE.md
go test ./internalv2/... -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internalv2/FLOW.md AGENTS.md docs/development/PROJECT_KNOWLEDGE.md
git commit -m "docs: document internalv2 sendack skeleton"
```

## Task 8: Final Verification

**Files:**
- Read: all files changed by this plan.

- [ ] **Step 1: Run focused package tests**

Run:

```bash
go test ./internalv2/... ./pkg/clusterv2 ./pkg/channelv2 -count=1
```

Expected: PASS.

- [ ] **Step 2: Run import boundary explicitly**

Run:

```bash
go test ./internalv2/usecase/message -run TestMessageUsecaseImportBoundary -count=1
```

Expected: PASS.

- [ ] **Step 3: Run diff checks**

Run:

```bash
git diff --check
git status --short
```

Expected: `git diff --check` has no output. `git status --short` shows no uncommitted implementation files after the planned commits.

- [ ] **Step 4: Summarize implementation**

Record in the final response:

- The packages added under `internalv2`.
- The verified send path.
- The exact tests run.
- Any residual risk from current `clusterv2/channelv2` limitations.

## Self-Review

- Spec coverage: Tasks cover package layout, message usecase, gateway Sendack mapping, clusterv2 adapter, app lifecycle, single-node-cluster smoke, import guard, FLOW documentation, AGENTS directory update, and project knowledge.
- Scope: The plan does not wire `cmd/wukongim`, does not alter existing `internal`, and does not implement delivery, conversation, CMD, plugin, or manager APIs.
- Type consistency: `message.SendBatch`, `message.Appender.AppendBatch`, `cluster.ChannelAppender.AppendBatch`, and gateway `OnSendBatch` all use the DTO names defined in Task 1.
- Red-flag scan: The plan has no unspecified implementation steps. The only conditional instruction is in the smoke task and names the exact package to inspect if clusterv2 metadata loading blocks the smoke.
