# internalv2 Send Authority Pipeline Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Refactor only the `internalv2` SEND path so gateway sends route through sender UID authority, `SENDACK` returns after channel durable commit, and recipient-side conversation plus delivery runs asynchronously on recipient UID authorities.

**Architecture:** Split the path into a synchronous sender-authority/channel-commit pipeline and an asynchronous recipient-authority pipeline. `access` packages only adapt frames/RPC payloads, `usecase` packages own entry-agnostic orchestration, `infra/cluster` adapts clusterv2 routing/RPC, and `internalv2/app` wires one clear graph while deleting replaced parallel committed sinks.

**Tech Stack:** Go, `internalv2` usecase/infra/access/app packages, clusterv2 node RPC, channelv2 append/read surfaces, existing `go test` unit and e2e harnesses.

---

## File Structure

Create:

- `internalv2/contracts/authority/target.go`: shared fenced UID authority target DTO.
- `internalv2/contracts/authority/target_test.go`: target validation tests.
- `internalv2/usecase/message/sender_authority.go`: gateway-facing sender authority router.
- `internalv2/usecase/message/sender_idempotency.go`: idempotent send lookup DTOs and helper.
- `internalv2/usecase/message/sender_authority_test.go`: sender router tests.
- `internalv2/usecase/recipient/types.go`: recipient pipeline DTOs, ports, errors, options.
- `internalv2/usecase/recipient/processor.go`: recipient authority processor.
- `internalv2/usecase/recipient/dispatcher.go`: async post-commit dispatcher and bounded worker.
- `internalv2/usecase/recipient/import_boundary_test.go`: recipient package boundary test.
- `internalv2/usecase/recipient/processor_test.go`: conversation-before-delivery tests.
- `internalv2/usecase/recipient/dispatcher_test.go`: subscriber paging, grouping, retry tests.
- `internalv2/access/node/sender_authority_codec.go`: sender authority RPC codec.
- `internalv2/access/node/sender_authority_rpc.go`: sender authority RPC handler/client methods.
- `internalv2/access/node/sender_authority_codec_test.go`: codec round-trip and malformed payload tests.
- `internalv2/access/node/sender_authority_rpc_test.go`: sender RPC adapter/client tests.
- `internalv2/access/node/recipient_authority_codec.go`: recipient authority RPC codec.
- `internalv2/access/node/recipient_authority_rpc.go`: recipient authority RPC handler/client methods.
- `internalv2/access/node/recipient_authority_codec_test.go`: codec round-trip and malformed payload tests.
- `internalv2/access/node/recipient_authority_rpc_test.go`: recipient RPC adapter/client tests.
- `internalv2/infra/cluster/sender_authority.go`: clusterv2-backed sender authority resolver/remote sender.
- `internalv2/infra/cluster/sender_authority_test.go`: sender authority route tests.
- `internalv2/infra/cluster/recipient_authority.go`: clusterv2-backed recipient authority resolver/remote dispatcher.
- `internalv2/infra/cluster/recipient_authority_test.go`: recipient target grouping/route retry tests.
- `internalv2/usecase/recipient/FLOW.md`: package flow documentation.

Modify:

- `pkg/clusterv2/net/ids.go`: add sender and recipient authority RPC service IDs.
- `pkg/clusterv2/net/ids_test.go`: assert new service IDs are unique.
- `internalv2/usecase/message/app.go`: add optional idempotency lookup dependency.
- `internalv2/usecase/message/ports.go`: add idempotency lookup port.
- `internalv2/usecase/message/send.go`: consult idempotency after validation/normalization and before allocating a new message ID.
- `internalv2/usecase/message/send_test.go`: idempotency recovery tests.
- `internalv2/access/node/presence_rpc.go`: extend `Options`, `Adapter`, and `Client` with sender/recipient authority ports.
- `internalv2/app/app.go`: wire sender router into gateway, wire sender RPC to raw local submitter, wire recipient pipeline as committed sink.
- `internalv2/app/conversation.go`: delete `committedSinkGroup` path once recipient pipeline replaces it.
- `internalv2/app/delivery.go`: keep connection-owner push/ack behavior and remove post-commit subscriber scanning from the new path.
- `internalv2/app/delivery_meta.go`: expose recipient subscriber pages for the recipient pipeline.
- `internalv2/app/app_test.go`: app wiring and old-path removal tests.
- `internalv2/FLOW.md`, `internalv2/access/gateway/FLOW.md`, `internalv2/access/node/FLOW.md`, `internalv2/usecase/message/FLOW.md`, `internalv2/usecase/conversation/FLOW.md`, `internalv2/usecase/delivery/FLOW.md`, `internalv2/infra/cluster/FLOW.md`: update flow docs.
- `test/e2e/message/wukongimv2_single_node_send/sendack_test.go`: keep single-node cluster coverage aligned with the new path.

---

### Task 1: Shared Authority Target and RPC Service IDs

**Files:**
- Create: `internalv2/contracts/authority/target.go`
- Create: `internalv2/contracts/authority/target_test.go`
- Modify: `pkg/clusterv2/net/ids.go`
- Modify: `pkg/clusterv2/net/ids_test.go`

- [ ] **Step 1: Write target validation tests**

Add `internalv2/contracts/authority/target_test.go`:

```go
package authority

import "testing"

func TestTargetIsLocal(t *testing.T) {
	target := Target{HashSlot: 3, SlotID: 9, LeaderNodeID: 2, RouteRevision: 7, AuthorityEpoch: 11}
	if !target.IsLocal(2) {
		t.Fatalf("IsLocal(2) = false, want true")
	}
	if target.IsLocal(1) {
		t.Fatalf("IsLocal(1) = true, want false")
	}
}

func TestTargetValidateRejectsMissingLeader(t *testing.T) {
	target := Target{HashSlot: 3, SlotID: 9, RouteRevision: 7, AuthorityEpoch: 11}
	if err := target.Validate(); err == nil {
		t.Fatalf("Validate() error = nil, want missing leader error")
	}
}
```

- [ ] **Step 2: Run target tests and verify they fail**

Run: `go test ./internalv2/contracts/authority`

Expected: FAIL because package `authority` does not exist.

- [ ] **Step 3: Add the target DTO**

Add `internalv2/contracts/authority/target.go`:

```go
package authority

import "errors"

var (
	// ErrInvalidTarget reports a missing or unusable UID authority target.
	ErrInvalidTarget = errors.New("internalv2/contracts/authority: invalid target")
)

// Target fences work to one observed UID hash-slot authority.
type Target struct {
	// HashSlot is the logical UID hash slot selected for the request.
	HashSlot uint16
	// SlotID is the physical Slot that owns HashSlot.
	SlotID uint32
	// LeaderNodeID is the node that was authority leader when this target was resolved.
	LeaderNodeID uint64
	// RouteRevision is the clusterv2 route-table revision used to resolve this target.
	RouteRevision uint64
	// AuthorityEpoch fences leadership changes for HashSlot.
	AuthorityEpoch uint64
}

// Validate reports whether the target can fence foreground authority work.
func (t Target) Validate() error {
	if t.LeaderNodeID == 0 {
		return ErrInvalidTarget
	}
	return nil
}

// IsLocal reports whether nodeID is the target leader.
func (t Target) IsLocal(nodeID uint64) bool {
	return nodeID != 0 && t.LeaderNodeID == nodeID
}
```

- [ ] **Step 4: Add clusterv2 RPC IDs and uniqueness coverage**

Modify `pkg/clusterv2/net/ids.go` by adding two IDs after `RPCConversationAuthority`:

```go
	// RPCSenderAuthority serves internalv2 sender UID authority SEND requests.
	RPCSenderAuthority
	// RPCRecipientAuthority serves internalv2 recipient UID authority post-commit requests.
	RPCRecipientAuthority
```

Modify `pkg/clusterv2/net/ids_test.go` by adding these map entries to `TestRPCServiceIDsAreUniqueAndNonZero`:

```go
		"sender_authority":       RPCSenderAuthority,
		"recipient_authority":    RPCRecipientAuthority,
```

- [ ] **Step 5: Run tests**

Run: `go test ./internalv2/contracts/authority ./pkg/clusterv2/net`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internalv2/contracts/authority pkg/clusterv2/net/ids.go pkg/clusterv2/net/ids_test.go
git commit -m "feat: add internalv2 authority target contract"
```

---

### Task 2: Sender Authority Router in `usecase/message`

**Files:**
- Create: `internalv2/usecase/message/sender_authority.go`
- Create: `internalv2/usecase/message/sender_authority_test.go`
- Modify: `internalv2/usecase/message/import_boundary_test.go`

- [ ] **Step 1: Write router tests**

Add `internalv2/usecase/message/sender_authority_test.go`:

```go
package message

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/authority"
)

func TestSenderAuthorityRouterUsesLocalSubmitterForLocalTarget(t *testing.T) {
	local := authority.Target{HashSlot: 1, SlotID: 2, LeaderNodeID: 7, RouteRevision: 3, AuthorityEpoch: 4}
	resolver := &senderAuthorityResolverForTest{target: local}
	submitter := &senderAuthoritySubmitterForTest{results: []SendBatchItemResult{{Result: SendResult{MessageID: 10, MessageSeq: 2, Reason: ReasonSuccess}}}}
	remote := &senderAuthorityRemoteForTest{}

	router := NewSenderAuthorityRouter(SenderAuthorityRouterOptions{
		LocalNodeID: 7,
		Resolver:    resolver,
		Local:       submitter,
		Remote:      remote,
	})

	results := router.SendBatch([]SendBatchItem{{Context: context.Background(), Command: SendCommand{FromUID: "u1"}}})
	if len(results) != 1 || results[0].Result.MessageID != 10 {
		t.Fatalf("results = %#v, want local result", results)
	}
	if submitter.calls != 1 {
		t.Fatalf("local calls = %d, want 1", submitter.calls)
	}
	if remote.calls != 0 {
		t.Fatalf("remote calls = %d, want 0", remote.calls)
	}
}

func TestSenderAuthorityRouterUsesRemoteForRemoteTarget(t *testing.T) {
	remoteTarget := authority.Target{HashSlot: 1, SlotID: 2, LeaderNodeID: 8, RouteRevision: 3, AuthorityEpoch: 4}
	resolver := &senderAuthorityResolverForTest{target: remoteTarget}
	local := &senderAuthoritySubmitterForTest{}
	remote := &senderAuthorityRemoteForTest{results: []SendBatchItemResult{{Result: SendResult{MessageID: 11, MessageSeq: 3, Reason: ReasonSuccess}}}}

	router := NewSenderAuthorityRouter(SenderAuthorityRouterOptions{
		LocalNodeID: 7,
		Resolver:    resolver,
		Local:       local,
		Remote:      remote,
	})

	results := router.SendBatch([]SendBatchItem{{Context: context.Background(), Command: SendCommand{FromUID: "u1"}}})
	if len(results) != 1 || results[0].Result.MessageID != 11 {
		t.Fatalf("results = %#v, want remote result", results)
	}
	if remote.calls != 1 || remote.target != remoteTarget {
		t.Fatalf("remote calls/target = %d/%#v, want 1/%#v", remote.calls, remote.target, remoteTarget)
	}
	if local.calls != 0 {
		t.Fatalf("local calls = %d, want 0", local.calls)
	}
}

func TestSenderAuthorityRouterMapsResolveError(t *testing.T) {
	resolver := &senderAuthorityResolverForTest{err: ErrRouteNotReady}
	router := NewSenderAuthorityRouter(SenderAuthorityRouterOptions{LocalNodeID: 7, Resolver: resolver})
	results := router.SendBatch([]SendBatchItem{{Context: context.Background(), Command: SendCommand{FromUID: "u1"}}})
	if len(results) != 1 || !errors.Is(results[0].Err, ErrRouteNotReady) {
		t.Fatalf("result err = %v, want ErrRouteNotReady", results[0].Err)
	}
}

func TestSenderAuthorityRouterPreservesInputOrderAcrossTargets(t *testing.T) {
	localTarget := authority.Target{HashSlot: 1, SlotID: 2, LeaderNodeID: 7, RouteRevision: 3, AuthorityEpoch: 4}
	remoteTarget := authority.Target{HashSlot: 2, SlotID: 3, LeaderNodeID: 8, RouteRevision: 3, AuthorityEpoch: 5}
	resolver := &senderAuthorityResolverForTest{targetsByUID: map[string]authority.Target{"local": localTarget, "remote": remoteTarget}}
	local := &senderAuthoritySubmitterForTest{results: []SendBatchItemResult{{Result: SendResult{MessageID: 1, MessageSeq: 1, Reason: ReasonSuccess}}}}
	remote := &senderAuthorityRemoteForTest{results: []SendBatchItemResult{{Result: SendResult{MessageID: 2, MessageSeq: 2, Reason: ReasonSuccess}}}}
	router := NewSenderAuthorityRouter(SenderAuthorityRouterOptions{LocalNodeID: 7, Resolver: resolver, Local: local, Remote: remote})

	results := router.SendBatch([]SendBatchItem{
		{Context: context.Background(), Command: SendCommand{FromUID: "remote"}},
		{Context: context.Background(), Command: SendCommand{FromUID: "local"}},
	})
	if results[0].Result.MessageID != 2 || results[1].Result.MessageID != 1 {
		t.Fatalf("ordered message ids = %d/%d, want 2/1", results[0].Result.MessageID, results[1].Result.MessageID)
	}
}

type senderAuthorityResolverForTest struct {
	target       authority.Target
	targetsByUID map[string]authority.Target
	err          error
}

func (r *senderAuthorityResolverForTest) ResolveUIDAuthority(_ context.Context, uid string) (authority.Target, error) {
	if r.err != nil {
		return authority.Target{}, r.err
	}
	if r.targetsByUID != nil {
		return r.targetsByUID[uid], nil
	}
	return r.target, nil
}

type senderAuthoritySubmitterForTest struct {
	results []SendBatchItemResult
	calls   int
}

func (s *senderAuthoritySubmitterForTest) SendBatch(items []SendBatchItem) []SendBatchItemResult {
	s.calls++
	if len(s.results) == len(items) {
		return append([]SendBatchItemResult(nil), s.results...)
	}
	out := make([]SendBatchItemResult, len(items))
	for i := range out {
		out[i] = SendBatchItemResult{Result: SendResult{MessageID: uint64(i + 1), Reason: ReasonSuccess}}
	}
	return out
}

type senderAuthorityRemoteForTest struct {
	results []SendBatchItemResult
	calls   int
	target  authority.Target
}

func (r *senderAuthorityRemoteForTest) SendBatchToAuthority(_ context.Context, target authority.Target, items []SendBatchItem) []SendBatchItemResult {
	r.calls++
	r.target = target
	if len(r.results) == len(items) {
		return append([]SendBatchItemResult(nil), r.results...)
	}
	return make([]SendBatchItemResult, len(items))
}
```

- [ ] **Step 2: Run the router tests and verify they fail**

Run: `go test ./internalv2/usecase/message -run 'TestSenderAuthorityRouter'`

Expected: FAIL because `NewSenderAuthorityRouter` and related interfaces do not exist.

- [ ] **Step 3: Add the router implementation**

Add `internalv2/usecase/message/sender_authority.go`:

```go
package message

import (
	"context"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/authority"
)

// UIDAuthorityResolver resolves a UID to its current fenced authority target.
type UIDAuthorityResolver interface {
	ResolveUIDAuthority(context.Context, string) (authority.Target, error)
}

// SenderAuthoritySubmitter owns local channel commit once this node is sender authority.
type SenderAuthoritySubmitter interface {
	SendBatch([]SendBatchItem) []SendBatchItemResult
}

// RemoteSenderAuthority forwards sends to a remote sender UID authority.
type RemoteSenderAuthority interface {
	SendBatchToAuthority(context.Context, authority.Target, []SendBatchItem) []SendBatchItemResult
}

// SenderAuthorityRouterOptions configures the gateway-facing sender authority router.
type SenderAuthorityRouterOptions struct {
	// LocalNodeID is this node's clusterv2 identity.
	LocalNodeID uint64
	// Resolver maps FromUID values to fenced UID authority targets.
	Resolver UIDAuthorityResolver
	// Local commits sends when this node owns the sender UID authority.
	Local SenderAuthoritySubmitter
	// Remote forwards sends to non-local sender UID authorities.
	Remote RemoteSenderAuthority
}

// SenderAuthorityRouter routes SEND commands to sender UID authority before channel commit.
type SenderAuthorityRouter struct {
	localNodeID uint64
	resolver    UIDAuthorityResolver
	local       SenderAuthoritySubmitter
	remote      RemoteSenderAuthority
}

// NewSenderAuthorityRouter creates a sender authority router.
func NewSenderAuthorityRouter(opts SenderAuthorityRouterOptions) *SenderAuthorityRouter {
	return &SenderAuthorityRouter{
		localNodeID: opts.LocalNodeID,
		resolver:    opts.Resolver,
		local:       opts.Local,
		remote:      opts.Remote,
	}
}

// Send routes one command through sender UID authority.
func (r *SenderAuthorityRouter) Send(ctx context.Context, cmd SendCommand) (SendResult, error) {
	results := r.SendBatch([]SendBatchItem{{Context: ctx, Command: cmd}})
	if len(results) == 0 {
		return SendResult{}, nil
	}
	return results[0].Result, results[0].Err
}

// SendBatch routes each item to its sender UID authority and returns item-aligned results.
func (r *SenderAuthorityRouter) SendBatch(items []SendBatchItem) []SendBatchItemResult {
	results := make([]SendBatchItemResult, len(items))
	for i, item := range items {
		ctx := item.Context
		if ctx == nil {
			ctx = context.Background()
		}
		if item.Command.FromUID == "" {
			results[i] = SendBatchItemResult{Result: SendResult{Reason: ReasonAuthFail}}
			continue
		}
		target, err := r.resolve(ctx, item.Command.FromUID)
		if err != nil {
			results[i] = SendBatchItemResult{Err: err}
			continue
		}
		if target.IsLocal(r.localNodeID) {
			results[i] = r.sendLocal(item)
			continue
		}
		results[i] = r.sendRemote(ctx, target, item)
	}
	return results
}

func (r *SenderAuthorityRouter) resolve(ctx context.Context, uid string) (authority.Target, error) {
	if r == nil || r.resolver == nil {
		return authority.Target{}, ErrRouteNotReady
	}
	target, err := r.resolver.ResolveUIDAuthority(ctx, uid)
	if err != nil {
		return authority.Target{}, err
	}
	if err := target.Validate(); err != nil {
		return authority.Target{}, ErrRouteNotReady
	}
	return target, nil
}

func (r *SenderAuthorityRouter) sendLocal(item SendBatchItem) SendBatchItemResult {
	if r == nil || r.local == nil {
		return SendBatchItemResult{Err: ErrRouteNotReady}
	}
	results := r.local.SendBatch([]SendBatchItem{item})
	if len(results) != 1 {
		return SendBatchItemResult{Err: ErrAppendResultMissing}
	}
	return results[0]
}

func (r *SenderAuthorityRouter) sendRemote(ctx context.Context, target authority.Target, item SendBatchItem) SendBatchItemResult {
	if r == nil || r.remote == nil {
		return SendBatchItemResult{Err: ErrRouteNotReady}
	}
	results := r.remote.SendBatchToAuthority(ctx, target, []SendBatchItem{item})
	if len(results) != 1 {
		return SendBatchItemResult{Err: ErrAppendResultMissing}
	}
	return results[0]
}
```

- [ ] **Step 4: Keep the import boundary explicit**

Modify `internalv2/usecase/message/import_boundary_test.go` and keep these forbidden imports present:

```go
		"github.com/WuKongIM/WuKongIM/pkg/clusterv2",
		"github.com/WuKongIM/WuKongIM/internalv2/access",
		"github.com/WuKongIM/WuKongIM/internalv2/app",
```

- [ ] **Step 5: Run tests**

Run: `go test ./internalv2/usecase/message`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internalv2/usecase/message/sender_authority.go internalv2/usecase/message/sender_authority_test.go internalv2/usecase/message/import_boundary_test.go
git commit -m "feat: route sends through sender authority usecase"
```

---

### Task 3: Sender Idempotency Lookup in `message.App`

**Files:**
- Create: `internalv2/usecase/message/sender_idempotency.go`
- Modify: `internalv2/usecase/message/app.go`
- Modify: `internalv2/usecase/message/ports.go`
- Modify: `internalv2/usecase/message/send.go`
- Modify: `internalv2/usecase/message/send_test.go`

- [ ] **Step 1: Add failing idempotency tests**

Append to `internalv2/usecase/message/send_test.go`:

```go
func TestSendReturnsIdempotentResultBeforeAllocatingNewMessageID(t *testing.T) {
	appender := &fakeAppender{}
	ids := &sequenceMessageIDAllocator{next: 100}
	idempotency := &recordingIdempotencyLookup{
		result: SendResult{MessageID: 42, MessageSeq: 7, Reason: ReasonSuccess},
		ok:     true,
	}
	app := New(Options{Appender: appender, MessageID: ids, Idempotency: idempotency})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "g1",
		ChannelType: 2,
		ClientMsgNo: "client-1",
		Payload:     []byte("hello"),
	})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if result.MessageID != 42 || result.MessageSeq != 7 {
		t.Fatalf("result = %#v, want idempotent 42/7", result)
	}
	if len(appender.requests) != 0 {
		t.Fatalf("append requests = %d, want 0", len(appender.requests))
	}
	if ids.next != 100 {
		t.Fatalf("next id = %d, want 100", ids.next)
	}
}

func TestSendFallsThroughWhenIdempotencyMisses(t *testing.T) {
	appender := &fakeAppender{result: AppendBatchResult{Items: []AppendBatchItemResult{{MessageID: 100, MessageSeq: 8}}}}
	ids := &sequenceMessageIDAllocator{next: 100}
	idempotency := &recordingIdempotencyLookup{}
	app := New(Options{Appender: appender, MessageID: ids, Idempotency: idempotency})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "g1",
		ChannelType: 2,
		ClientMsgNo: "client-1",
		Payload:     []byte("hello"),
	})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if result.MessageID != 100 || len(appender.requests) != 1 {
		t.Fatalf("result/requests = %#v/%d, want append result and one request", result, len(appender.requests))
	}
}

type recordingIdempotencyLookup struct {
	query  IdempotencyQuery
	result SendResult
	ok     bool
	err    error
}

func (l *recordingIdempotencyLookup) LookupSend(ctx context.Context, query IdempotencyQuery) (SendResult, bool, error) {
	l.query = query
	return l.result, l.ok, l.err
}
```

If existing test fakes have different names, adapt the test to the local fake types already defined at the bottom of `send_test.go`.

- [ ] **Step 2: Run tests and verify they fail**

Run: `go test ./internalv2/usecase/message -run 'TestSendReturnsIdempotentResult|TestSendFallsThroughWhenIdempotencyMisses'`

Expected: FAIL because `Options.Idempotency` and `IdempotencyQuery` do not exist.

- [ ] **Step 3: Add idempotency DTOs and port**

Add `internalv2/usecase/message/sender_idempotency.go`:

```go
package message

import "context"

// IdempotencyQuery identifies one canonical sender/client message key.
type IdempotencyQuery struct {
	// FromUID is the authenticated sender UID.
	FromUID string
	// ClientMsgNo is the sender-provided idempotency key.
	ClientMsgNo string
	// ChannelID is the canonical channel ID after request normalization.
	ChannelID string
	// ChannelType is the canonical channel type.
	ChannelType uint8
}

// SendIdempotencyLookup recovers a previously committed send result.
type SendIdempotencyLookup interface {
	LookupSend(context.Context, IdempotencyQuery) (SendResult, bool, error)
}
```

Modify `internalv2/usecase/message/app.go`:

```go
type Options struct {
	Appender    Appender
	MessageReader ChannelMessageReader
	MessageID   MessageIDAllocator
	Authorizer  Authorizer
	Committed   CommittedSink
	Observer    Observer
	// Idempotency recovers successful sends after sender-authority response loss.
	Idempotency SendIdempotencyLookup
}

type App struct {
	appender      Appender
	messageReader ChannelMessageReader
	messageID     MessageIDAllocator
	authorizer    Authorizer
	committed     CommittedSink
	observer      Observer
	idempotency   SendIdempotencyLookup
}
```

Ensure `New` assigns `idempotency: opts.Idempotency`.

- [ ] **Step 4: Consult idempotency in `prepareSend`**

Modify `internalv2/usecase/message/send.go` after authorization and person-channel normalization, before message ID allocation:

```go
	if existing, ok, err := a.lookupIdempotentSend(ctx, cmd); err != nil {
		return preparedSend{err: err}, true
	} else if ok {
		return preparedSend{result: existing}, true
	}
```

Add helper in `send.go`:

```go
func (a *App) lookupIdempotentSend(ctx context.Context, cmd SendCommand) (SendResult, bool, error) {
	if a == nil || a.idempotency == nil || cmd.ClientMsgNo == "" {
		return SendResult{}, false, nil
	}
	return a.idempotency.LookupSend(ctx, IdempotencyQuery{
		FromUID:     cmd.FromUID,
		ClientMsgNo: cmd.ClientMsgNo,
		ChannelID:   cmd.ChannelID,
		ChannelType: cmd.ChannelType,
	})
}
```

Apply the same helper in `prepareRequestScopedSend` after the request-scoped channel has been derived and authorization has passed.

- [ ] **Step 5: Run tests**

Run: `go test ./internalv2/usecase/message`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internalv2/usecase/message
git commit -m "feat: recover idempotent internalv2 sends"
```

---

### Task 4: Sender Authority RPC Codec and Adapter

**Files:**
- Create: `internalv2/access/node/sender_authority_codec.go`
- Create: `internalv2/access/node/sender_authority_rpc.go`
- Create: `internalv2/access/node/sender_authority_codec_test.go`
- Create: `internalv2/access/node/sender_authority_rpc_test.go`
- Modify: `internalv2/access/node/presence_rpc.go`

- [ ] **Step 1: Write codec round-trip tests**

Add `internalv2/access/node/sender_authority_codec_test.go`:

```go
package node

import (
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/authority"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
)

func TestSenderAuthorityCodecRoundTrip(t *testing.T) {
	req := senderAuthorityRequest{
		Target: authority.Target{HashSlot: 1, SlotID: 2, LeaderNodeID: 3, RouteRevision: 4, AuthorityEpoch: 5},
		Items: []senderAuthorityItem{{
			Command: message.SendCommand{FromUID: "u1", ChannelID: "g1", ChannelType: 2, ClientMsgNo: "c1", Payload: []byte("hello")},
			Timeout: 250 * time.Millisecond,
		}},
	}
	body, err := encodeSenderAuthorityRequest(req)
	if err != nil {
		t.Fatalf("encodeSenderAuthorityRequest() error = %v", err)
	}
	got, err := decodeSenderAuthorityRequest(body)
	if err != nil {
		t.Fatalf("decodeSenderAuthorityRequest() error = %v", err)
	}
	if got.Target != req.Target || len(got.Items) != 1 || got.Items[0].Command.FromUID != "u1" ||
		string(got.Items[0].Command.Payload) != "hello" || got.Items[0].Timeout != 250*time.Millisecond {
		t.Fatalf("decoded request = %#v, want %#v", got, req)
	}
}

func TestSenderAuthorityCodecRejectsMalformedPayload(t *testing.T) {
	if _, err := decodeSenderAuthorityRequest([]byte("bad")); err == nil {
		t.Fatalf("decodeSenderAuthorityRequest() error = nil, want error")
	}
}
```

- [ ] **Step 2: Write RPC adapter tests**

Add `internalv2/access/node/sender_authority_rpc_test.go`:

```go
package node

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/authority"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
)

func TestHandleSenderAuthorityRPCCallsLocalSubmitter(t *testing.T) {
	target := authority.Target{HashSlot: 1, SlotID: 2, LeaderNodeID: 3, RouteRevision: 4, AuthorityEpoch: 5}
	submitter := &recordingSenderAuthoritySubmitter{
		results: []message.SendBatchItemResult{{Result: message.SendResult{MessageID: 10, MessageSeq: 1, Reason: message.ReasonSuccess}}},
	}
	adapter := New(Options{SenderAuthority: submitter})
	body, err := encodeSenderAuthorityRequest(senderAuthorityRequest{
		Target: target,
		Items: []senderAuthorityItem{{Command: message.SendCommand{FromUID: "u1", ChannelID: "g1", ChannelType: 2, Payload: []byte("x")}}},
	})
	if err != nil {
		t.Fatalf("encodeSenderAuthorityRequest() error = %v", err)
	}
	respBody, err := adapter.HandleSenderAuthorityRPC(context.Background(), body)
	if err != nil {
		t.Fatalf("HandleSenderAuthorityRPC() error = %v", err)
	}
	resp, err := decodeSenderAuthorityResponse(respBody)
	if err != nil {
		t.Fatalf("decodeSenderAuthorityResponse() error = %v", err)
	}
	if resp.Status != rpcStatusOK || len(resp.Results) != 1 || resp.Results[0].Result.MessageID != 10 {
		t.Fatalf("response = %#v, want ok result", resp)
	}
	if submitter.target != target {
		t.Fatalf("target = %#v, want %#v", submitter.target, target)
	}
}

type recordingSenderAuthoritySubmitter struct {
	target  authority.Target
	results []message.SendBatchItemResult
}

func (s *recordingSenderAuthoritySubmitter) SendBatchForAuthority(_ context.Context, target authority.Target, items []message.SendBatchItem) []message.SendBatchItemResult {
	s.target = target
	return append([]message.SendBatchItemResult(nil), s.results...)
}
```

- [ ] **Step 3: Run tests and verify they fail**

Run: `go test ./internalv2/access/node -run 'SenderAuthority'`

Expected: FAIL because sender authority codec and adapter do not exist.

- [ ] **Step 4: Implement codec and adapter**

Add `internalv2/access/node/sender_authority_codec.go` with:

```go
package node

import (
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/authority"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
)

var (
	senderAuthorityRequestMagic  = [...]byte{'W', 'K', 'V', 'S', 1}
	senderAuthorityResponseMagic = [...]byte{'W', 'K', 'V', 's', 1}
)

const maxSenderAuthorityCollectionLen = 4096

type senderAuthorityItem struct {
	Command message.SendCommand
	Timeout time.Duration
}

type senderAuthorityRequest struct {
	Target authority.Target
	Items  []senderAuthorityItem
}

type senderAuthorityResponse struct {
	Status  string
	Results []message.SendBatchItemResult
}

func encodeSenderAuthorityRequest(req senderAuthorityRequest) ([]byte, error) {
	dst := make([]byte, 0, 256)
	dst = append(dst, senderAuthorityRequestMagic[:]...)
	dst = appendAuthorityTarget(dst, req.Target)
	dst = appendUvarint(dst, uint64(len(req.Items)))
	for _, item := range req.Items {
		dst = appendMessageSendCommand(dst, item.Command)
		dst = appendVarint(dst, int64(item.Timeout))
	}
	return dst, nil
}

func decodeSenderAuthorityRequest(body []byte) (senderAuthorityRequest, error) {
	if !hasMagic(body, senderAuthorityRequestMagic[:]) {
		return senderAuthorityRequest{}, fmt.Errorf("internalv2/access/node: invalid sender authority request codec")
	}
	offset := len(senderAuthorityRequestMagic)
	target, next, err := readAuthorityTarget(body, offset)
	if err != nil {
		return senderAuthorityRequest{}, err
	}
	offset = next
	count, next, err := readUvarint(body, offset)
	if err != nil {
		return senderAuthorityRequest{}, err
	}
	offset = next
	if count > maxSenderAuthorityCollectionLen {
		return senderAuthorityRequest{}, fmt.Errorf("internalv2/access/node: sender authority request too large")
	}
	req := senderAuthorityRequest{Target: target, Items: make([]senderAuthorityItem, 0, int(count))}
	for i := 0; i < int(count); i++ {
		cmd, next, err := readMessageSendCommand(body, offset)
		if err != nil {
			return senderAuthorityRequest{}, err
		}
		offset = next
		timeoutNanos, next, err := readVarint(body, offset)
		if err != nil {
			return senderAuthorityRequest{}, err
		}
		offset = next
		req.Items = append(req.Items, senderAuthorityItem{Command: cmd, Timeout: time.Duration(timeoutNanos)})
	}
	if offset != len(body) {
		return senderAuthorityRequest{}, fmt.Errorf("internalv2/access/node: trailing sender authority request bytes")
	}
	return req, nil
}
```

In the same file, implement `appendAuthorityTarget`, `readAuthorityTarget`, `appendMessageSendCommand`, `readMessageSendCommand`, response encode/decode, and result encode/decode using existing helper style from `delivery_codec.go`.

Add `internalv2/access/node/sender_authority_rpc.go`:

```go
package node

import (
	"context"
	"fmt"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/authority"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/clusterv2/net"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

const SenderAuthorityRPCServiceID uint8 = clusternet.RPCSenderAuthority

type SenderAuthority interface {
	SendBatchForAuthority(context.Context, authority.Target, []message.SendBatchItem) []message.SendBatchItemResult
}

func (a *Adapter) HandleSenderAuthorityRPC(ctx context.Context, payload []byte) ([]byte, error) {
	req, err := decodeSenderAuthorityRequest(payload)
	if err != nil {
		a.rpcLogger().Warn("sender authority rpc decode failed", wklog.Event("internalv2.access.node.sender_authority_decode_failed"), wklog.Int("payloadBytes", len(payload)), wklog.Error(err))
		return nil, err
	}
	if a == nil || a.senderAuthority == nil {
		return encodeSenderAuthorityResponse(senderAuthorityResponse{Status: rpcStatusRejected})
	}
	items := make([]message.SendBatchItem, 0, len(req.Items))
	for _, item := range req.Items {
		items = append(items, message.SendBatchItem{Context: ctx, Command: item.Command})
	}
	results := a.senderAuthority.SendBatchForAuthority(ctx, req.Target, items)
	return encodeSenderAuthorityResponse(senderAuthorityResponse{Status: rpcStatusOK, Results: results})
}

func (c *Client) SendBatchToAuthority(ctx context.Context, target authority.Target, items []message.SendBatchItem) []message.SendBatchItemResult {
	if c == nil || c.node == nil {
		return senderAuthorityErrorResults(len(items), fmt.Errorf("internalv2/access/node: sender authority rpc client not configured"))
	}
	reqItems := make([]senderAuthorityItem, 0, len(items))
	for _, item := range items {
		reqItems = append(reqItems, senderAuthorityItem{Command: item.Command})
	}
	body, err := encodeSenderAuthorityRequest(senderAuthorityRequest{Target: target, Items: reqItems})
	if err != nil {
		return senderAuthorityErrorResults(len(items), err)
	}
	respBody, err := c.node.CallRPC(ctx, target.LeaderNodeID, SenderAuthorityRPCServiceID, body)
	if err != nil {
		return senderAuthorityErrorResults(len(items), err)
	}
	resp, err := decodeSenderAuthorityResponse(respBody)
	if err != nil {
		return senderAuthorityErrorResults(len(items), err)
	}
	if resp.Status != rpcStatusOK {
		return senderAuthorityErrorResults(len(items), senderAuthorityErrorForStatus(resp.Status))
	}
	return resp.Results
}
```

Modify `internalv2/access/node/presence_rpc.go`:

```go
type Options struct {
	Authority PresenceAuthority
	Owner PresenceOwner
	Delivery DeliveryOwnerPush
	DeliveryFanout DeliveryFanoutRunner
	ConversationAuthority ConversationAuthority
	SenderAuthority SenderAuthority
	Logger wklog.Logger
}

type Adapter struct {
	authority PresenceAuthority
	owner PresenceOwner
	delivery DeliveryOwnerPush
	deliveryFanout DeliveryFanoutRunner
	conversation ConversationAuthority
	senderAuthority SenderAuthority
	logger wklog.Logger
}
```

Ensure `New` assigns `senderAuthority: opts.SenderAuthority`.

- [ ] **Step 5: Run tests**

Run: `go test ./internalv2/access/node`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internalv2/access/node
git commit -m "feat: add sender authority node rpc"
```

---

### Task 5: Cluster Sender Authority Adapter

**Files:**
- Create: `internalv2/infra/cluster/sender_authority.go`
- Create: `internalv2/infra/cluster/sender_authority_test.go`

- [ ] **Step 1: Write cluster adapter tests**

Add `internalv2/infra/cluster/sender_authority_test.go`:

```go
package cluster

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/authority"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
)

func TestSenderAuthorityResolverMapsRoute(t *testing.T) {
	node := &fakeSenderAuthorityNode{route: clusterv2.Route{HashSlot: 1, SlotID: 2, Leader: 3, Revision: 4, AuthorityEpoch: 5}}
	client := NewSenderAuthorityClient(node, nil)
	target, err := client.ResolveUIDAuthority(context.Background(), "u1")
	if err != nil {
		t.Fatalf("ResolveUIDAuthority() error = %v", err)
	}
	want := authority.Target{HashSlot: 1, SlotID: 2, LeaderNodeID: 3, RouteRevision: 4, AuthorityEpoch: 5}
	if target != want {
		t.Fatalf("target = %#v, want %#v", target, want)
	}
}

func TestSenderAuthorityResolverMapsRouteErrors(t *testing.T) {
	node := &fakeSenderAuthorityNode{err: clusterv2.ErrRouteNotReady}
	client := NewSenderAuthorityClient(node, nil)
	_, err := client.ResolveUIDAuthority(context.Background(), "u1")
	if !errors.Is(err, message.ErrRouteNotReady) {
		t.Fatalf("err = %v, want message.ErrRouteNotReady", err)
	}
}

type fakeSenderAuthorityNode struct {
	route clusterv2.Route
	err   error
}

func (f *fakeSenderAuthorityNode) NodeID() uint64 { return 1 }
func (f *fakeSenderAuthorityNode) RouteKey(string) (clusterv2.Route, error) {
	return f.route, f.err
}
func (f *fakeSenderAuthorityNode) CallRPC(context.Context, uint64, uint8, []byte) ([]byte, error) {
	return nil, errors.New("unexpected rpc")
}
func (f *fakeSenderAuthorityNode) RegisterRPC(uint8, clusterv2.NodeRPCHandler) {}
```

- [ ] **Step 2: Run tests and verify they fail**

Run: `go test ./internalv2/infra/cluster -run 'TestSenderAuthority'`

Expected: FAIL because `NewSenderAuthorityClient` does not exist.

- [ ] **Step 3: Implement sender authority client**

Add `internalv2/infra/cluster/sender_authority.go`:

```go
package cluster

import (
	"context"
	"fmt"

	accessnode "github.com/WuKongIM/WuKongIM/internalv2/access/node"
	"github.com/WuKongIM/WuKongIM/internalv2/contracts/authority"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
)

type SenderAuthorityNode interface {
	NodeID() uint64
	RouteKey(string) (clusterv2.Route, error)
	CallRPC(context.Context, uint64, uint8, []byte) ([]byte, error)
	RegisterRPC(uint8, clusterv2.NodeRPCHandler)
}

type SenderAuthorityClient struct {
	node   SenderAuthorityNode
	remote *accessnode.Client
}

func NewSenderAuthorityClient(node SenderAuthorityNode, remote *accessnode.Client) *SenderAuthorityClient {
	if remote == nil {
		remote = accessnode.NewClient(node)
	}
	return &SenderAuthorityClient{node: node, remote: remote}
}

func (c *SenderAuthorityClient) ResolveUIDAuthority(ctx context.Context, uid string) (authority.Target, error) {
	if c == nil || c.node == nil {
		return authority.Target{}, message.ErrRouteNotReady
	}
	route, err := c.node.RouteKey(uid)
	if err != nil {
		return authority.Target{}, mapMessageRouteError(err)
	}
	if route.Leader == 0 {
		return authority.Target{}, fmt.Errorf("%w: route leader unknown", message.ErrRouteNotReady)
	}
	return authority.Target{HashSlot: route.HashSlot, SlotID: route.SlotID, LeaderNodeID: route.Leader, RouteRevision: route.Revision, AuthorityEpoch: route.AuthorityEpoch}, nil
}

func (c *SenderAuthorityClient) SendBatchToAuthority(ctx context.Context, target authority.Target, items []message.SendBatchItem) []message.SendBatchItemResult {
	if c == nil || c.remote == nil {
		return senderAuthorityClusterErrors(len(items), message.ErrRouteNotReady)
	}
	return c.remote.SendBatchToAuthority(ctx, target, items)
}

func senderAuthorityClusterErrors(n int, err error) []message.SendBatchItemResult {
	out := make([]message.SendBatchItemResult, n)
	for i := range out {
		out[i].Err = err
	}
	return out
}
```

Add `mapMessageRouteError` in this file or reuse existing error mapping if it already exists:

```go
func mapMessageRouteError(err error) error {
	switch {
	case errors.Is(err, clusterv2.ErrRouteNotReady), errors.Is(err, clusterv2.ErrNoSlotLeader):
		return message.ErrRouteNotReady
	case errors.Is(err, clusterv2.ErrNotLeader):
		return message.ErrNotLeader
	default:
		return err
	}
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internalv2/infra/cluster -run 'TestSenderAuthority|Test.*Appender'`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internalv2/infra/cluster/sender_authority.go internalv2/infra/cluster/sender_authority_test.go
git commit -m "feat: route sender authority through clusterv2"
```

---

### Task 6: Recipient Usecase Processor

**Files:**
- Create: `internalv2/usecase/recipient/types.go`
- Create: `internalv2/usecase/recipient/processor.go`
- Create: `internalv2/usecase/recipient/processor_test.go`
- Create: `internalv2/usecase/recipient/import_boundary_test.go`

- [ ] **Step 1: Write processor tests**

Add `internalv2/usecase/recipient/processor_test.go`:

```go
package recipient

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/authority"
	"github.com/WuKongIM/WuKongIM/internalv2/contracts/messageevents"
	conversationusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/conversation"
)

func TestProcessorUpdatesConversationBeforeDelivery(t *testing.T) {
	conversation := &recordingConversationUpdater{}
	delivery := &recordingDeliverySubmitter{}
	processor := NewProcessor(ProcessorOptions{LocalNodeID: 1, Conversation: conversation, Delivery: delivery})
	event := messageevents.MessageCommitted{MessageID: 10, MessageSeq: 2, ChannelID: "g1", ChannelType: 2, FromUID: "u1", ServerTimestampMS: 100}
	req := ProcessRequest{
		Target:     authority.Target{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4},
		Event:      event,
		Recipients: []Recipient{{UID: "u2"}, {UID: "u3", JoinSeq: 2}},
	}
	if err := processor.Process(context.Background(), req); err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if len(conversation.patches) != 2 {
		t.Fatalf("patches = %d, want 2", len(conversation.patches))
	}
	if len(delivery.events) != 1 || len(delivery.events[0].MessageScopedUIDs) != 2 {
		t.Fatalf("delivery events = %#v, want scoped delivery", delivery.events)
	}
	if conversation.calledAfterDelivery {
		t.Fatalf("conversation update happened after delivery")
	}
}

func TestProcessorSkipsDeliveryWhenConversationFails(t *testing.T) {
	conversation := &recordingConversationUpdater{err: errors.New("conversation failed")}
	delivery := &recordingDeliverySubmitter{}
	processor := NewProcessor(ProcessorOptions{LocalNodeID: 1, Conversation: conversation, Delivery: delivery})
	err := processor.Process(context.Background(), ProcessRequest{
		Target:     authority.Target{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4},
		Event:      messageevents.MessageCommitted{MessageID: 10, MessageSeq: 2, ChannelID: "g1", ChannelType: 2, ServerTimestampMS: 100},
		Recipients: []Recipient{{UID: "u2"}},
	})
	if err == nil {
		t.Fatalf("Process() error = nil, want conversation error")
	}
	if len(delivery.events) != 0 {
		t.Fatalf("delivery events = %d, want 0", len(delivery.events))
	}
}

func TestProcessorRejectsStaleTarget(t *testing.T) {
	processor := NewProcessor(ProcessorOptions{LocalNodeID: 1})
	err := processor.Process(context.Background(), ProcessRequest{
		Target:     authority.Target{HashSlot: 1, SlotID: 2, LeaderNodeID: 2, RouteRevision: 3, AuthorityEpoch: 4},
		Event:      messageevents.MessageCommitted{MessageID: 10},
		Recipients: []Recipient{{UID: "u2"}},
	})
	if !errors.Is(err, ErrNotLeader) {
		t.Fatalf("err = %v, want ErrNotLeader", err)
	}
}

type recordingConversationUpdater struct {
	patches             []conversationusecase.ActivePatch
	err                 error
	calledAfterDelivery bool
}

func (u *recordingConversationUpdater) AdmitPatches(_ context.Context, patches []conversationusecase.ActivePatch) error {
	u.patches = append(u.patches, patches...)
	return u.err
}

type recordingDeliverySubmitter struct {
	events []messageevents.MessageCommitted
}

func (s *recordingDeliverySubmitter) SubmitDelivery(_ context.Context, event messageevents.MessageCommitted) error {
	s.events = append(s.events, event.Clone())
	return nil
}
```

- [ ] **Step 2: Add import boundary test**

Add `internalv2/usecase/recipient/import_boundary_test.go`:

```go
package recipient

import (
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

func TestRecipientUsecaseImportBoundary(t *testing.T) {
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

- [ ] **Step 3: Run tests and verify they fail**

Run: `go test ./internalv2/usecase/recipient`

Expected: FAIL because package implementation does not exist.

- [ ] **Step 4: Implement recipient processor**

Add `internalv2/usecase/recipient/types.go`:

```go
package recipient

import (
	"context"
	"errors"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/authority"
	"github.com/WuKongIM/WuKongIM/internalv2/contracts/messageevents"
	conversationusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/conversation"
)

var (
	ErrNotLeader     = errors.New("internalv2/usecase/recipient: not leader")
	ErrStaleRoute    = errors.New("internalv2/usecase/recipient: stale route")
	ErrRouteNotReady = errors.New("internalv2/usecase/recipient: route not ready")
)

type Recipient struct {
	UID     string
	JoinSeq uint64
}

type ProcessRequest struct {
	Target     authority.Target
	Event      messageevents.MessageCommitted
	Recipients []Recipient
}

type ConversationUpdater interface {
	AdmitPatches(context.Context, []conversationusecase.ActivePatch) error
}

type DeliverySubmitter interface {
	SubmitDelivery(context.Context, messageevents.MessageCommitted) error
}

type ProcessorOptions struct {
	LocalNodeID  uint64
	Conversation ConversationUpdater
	Delivery     DeliverySubmitter
}
```

Add `internalv2/usecase/recipient/processor.go`:

```go
package recipient

import (
	"context"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/messageevents"
	conversationusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/conversation"
)

type Processor struct {
	localNodeID  uint64
	conversation ConversationUpdater
	delivery     DeliverySubmitter
}

func NewProcessor(opts ProcessorOptions) *Processor {
	return &Processor{localNodeID: opts.LocalNodeID, conversation: opts.Conversation, delivery: opts.Delivery}
}

func (p *Processor) Process(ctx context.Context, req ProcessRequest) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if !req.Target.IsLocal(p.localNodeID) {
		return ErrNotLeader
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	patches := recipientConversationPatches(req.Event, req.Recipients)
	if len(patches) > 0 && p.conversation != nil {
		if err := p.conversation.AdmitPatches(ctx, patches); err != nil {
			return err
		}
	}
	if p.delivery == nil {
		return nil
	}
	event := req.Event.Clone()
	event.MessageScopedUIDs = recipientUIDs(req.Recipients)
	return p.delivery.SubmitDelivery(ctx, event)
}

func recipientConversationPatches(event messageevents.MessageCommitted, recipients []Recipient) []conversationusecase.ActivePatch {
	patches := make([]conversationusecase.ActivePatch, 0, len(recipients))
	for _, recipient := range recipients {
		if recipient.UID == "" {
			continue
		}
		readSeq, deletedToSeq := joinSeqFloors(recipient.JoinSeq)
		patches = append(patches, conversationusecase.ActivePatch{
			UID:          recipient.UID,
			ChannelID:    event.ChannelID,
			ChannelType:  int64(event.ChannelType),
			ReadSeq:      readSeq,
			DeletedToSeq: deletedToSeq,
			ActiveAt:     event.ServerTimestampMS,
			UpdatedAt:    event.ServerTimestampMS,
			MessageSeq:    event.MessageSeq,
		})
	}
	return patches
}

func joinSeqFloors(joinSeq uint64) (uint64, uint64) {
	if joinSeq == 0 {
		return 0, 0
	}
	return joinSeq - 1, joinSeq - 1
}

func recipientUIDs(recipients []Recipient) []string {
	uids := make([]string, 0, len(recipients))
	for _, recipient := range recipients {
		if recipient.UID != "" {
			uids = append(uids, recipient.UID)
		}
	}
	return uids
}
```

- [ ] **Step 5: Run tests**

Run: `go test ./internalv2/usecase/recipient`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internalv2/usecase/recipient
git commit -m "feat: add recipient authority processor"
```

---

### Task 7: Recipient Dispatcher and Bounded Subscriber Paging

**Files:**
- Create/Modify: `internalv2/usecase/recipient/dispatcher.go`
- Create/Modify: `internalv2/usecase/recipient/dispatcher_test.go`
- Modify: `internalv2/usecase/recipient/types.go`

- [ ] **Step 1: Write dispatcher tests**

Add `internalv2/usecase/recipient/dispatcher_test.go`:

```go
package recipient

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/authority"
	"github.com/WuKongIM/WuKongIM/internalv2/contracts/messageevents"
)

func TestDispatcherUsesScopedRecipientsDirectly(t *testing.T) {
	localTarget := authority.Target{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	resolver := &dispatcherAuthorityResolver{targets: map[string]authority.Target{"u2": localTarget, "u3": localTarget}}
	processor := &recordingProcessor{}
	dispatcher := NewDispatcher(DispatcherOptions{LocalNodeID: 1, Resolver: resolver, Local: processor, PageSize: 2, TargetBatchSize: 2})

	event := messageevents.MessageCommitted{MessageID: 10, ChannelID: "g1", ChannelType: 2, MessageScopedUIDs: []string{"u2", "u3"}}
	if err := dispatcher.SubmitCommitted(context.Background(), event); err != nil {
		t.Fatalf("SubmitCommitted() error = %v", err)
	}
	if len(processor.requests) != 1 || len(processor.requests[0].Recipients) != 2 {
		t.Fatalf("requests = %#v, want one local scoped group", processor.requests)
	}
}

func TestDispatcherPagesSubscribersWithoutLoadingAll(t *testing.T) {
	target := authority.Target{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	source := &pagedRecipientSource{pages: []RecipientPage{
		{Recipients: []Recipient{{UID: "u1"}, {UID: "u2"}}, Cursor: "next"},
		{Recipients: []Recipient{{UID: "u3"}}, Done: true},
	}}
	resolver := &dispatcherAuthorityResolver{defaultTarget: target}
	processor := &recordingProcessor{}
	dispatcher := NewDispatcher(DispatcherOptions{LocalNodeID: 1, Recipients: source, Resolver: resolver, Local: processor, PageSize: 2, TargetBatchSize: 2})

	if err := dispatcher.SubmitCommitted(context.Background(), messageevents.MessageCommitted{MessageID: 10, ChannelID: "g1", ChannelType: 2}); err != nil {
		t.Fatalf("SubmitCommitted() error = %v", err)
	}
	if source.calls != 2 {
		t.Fatalf("source calls = %d, want 2", source.calls)
	}
	if len(processor.requests) != 2 {
		t.Fatalf("processor requests = %d, want 2 page groups", len(processor.requests))
	}
}

type dispatcherAuthorityResolver struct {
	defaultTarget authority.Target
	targets       map[string]authority.Target
}

func (r *dispatcherAuthorityResolver) ResolveRecipientAuthority(_ context.Context, uid string) (authority.Target, error) {
	if r.targets != nil {
		return r.targets[uid], nil
	}
	return r.defaultTarget, nil
}

type recordingProcessor struct {
	requests []ProcessRequest
}

func (p *recordingProcessor) Process(ctx context.Context, req ProcessRequest) error {
	p.requests = append(p.requests, req)
	return nil
}

type pagedRecipientSource struct {
	pages []RecipientPage
	calls int
}

func (s *pagedRecipientSource) NextPage(_ context.Context, event messageevents.MessageCommitted, cursor string, limit int) (RecipientPage, error) {
	page := s.pages[s.calls]
	s.calls++
	return page, nil
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run: `go test ./internalv2/usecase/recipient -run 'TestDispatcher'`

Expected: FAIL because dispatcher types do not exist.

- [ ] **Step 3: Add dispatcher ports and implementation**

Extend `internalv2/usecase/recipient/types.go`:

```go
type RecipientPage struct {
	Recipients []Recipient
	Cursor     string
	Done       bool
}

type RecipientSource interface {
	NextPage(context.Context, messageevents.MessageCommitted, string, int) (RecipientPage, error)
}

type RecipientAuthorityResolver interface {
	ResolveRecipientAuthority(context.Context, string) (authority.Target, error)
}

type RecipientRemote interface {
	ProcessRemote(context.Context, ProcessRequest) error
}

type LocalProcessor interface {
	Process(context.Context, ProcessRequest) error
}

type DispatcherOptions struct {
	LocalNodeID      uint64
	Recipients      RecipientSource
	Resolver        RecipientAuthorityResolver
	Local           LocalProcessor
	Remote          RecipientRemote
	PageSize        int
	TargetBatchSize int
}
```

Add `internalv2/usecase/recipient/dispatcher.go`:

```go
package recipient

import (
	"context"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/authority"
	"github.com/WuKongIM/WuKongIM/internalv2/contracts/messageevents"
)

const defaultPageSize = 512
const defaultTargetBatchSize = 512

type Dispatcher struct {
	localNodeID      uint64
	recipients      RecipientSource
	resolver        RecipientAuthorityResolver
	local           LocalProcessor
	remote          RecipientRemote
	pageSize        int
	targetBatchSize int
}

func NewDispatcher(opts DispatcherOptions) *Dispatcher {
	if opts.PageSize <= 0 {
		opts.PageSize = defaultPageSize
	}
	if opts.TargetBatchSize <= 0 {
		opts.TargetBatchSize = defaultTargetBatchSize
	}
	return &Dispatcher{localNodeID: opts.LocalNodeID, recipients: opts.Recipients, resolver: opts.Resolver, local: opts.Local, remote: opts.Remote, pageSize: opts.PageSize, targetBatchSize: opts.TargetBatchSize}
}

func (d *Dispatcher) SubmitCommitted(ctx context.Context, event messageevents.MessageCommitted) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(event.MessageScopedUIDs) > 0 {
		recipients := make([]Recipient, 0, len(event.MessageScopedUIDs))
		for _, uid := range event.MessageScopedUIDs {
			recipients = append(recipients, Recipient{UID: uid})
		}
		return d.dispatchRecipients(ctx, event, recipients)
	}
	cursor := ""
	for {
		if d.recipients == nil {
			return nil
		}
		page, err := d.recipients.NextPage(ctx, event, cursor, d.pageSize)
		if err != nil {
			return err
		}
		if err := d.dispatchRecipients(ctx, event, page.Recipients); err != nil {
			return err
		}
		if page.Done {
			return nil
		}
		if page.Cursor == "" || page.Cursor == cursor {
			return ErrRouteNotReady
		}
		cursor = page.Cursor
	}
}

func (d *Dispatcher) dispatchRecipients(ctx context.Context, event messageevents.MessageCommitted, recipients []Recipient) error {
	groups, err := d.groupByTarget(ctx, recipients)
	if err != nil {
		return err
	}
	for target, group := range groups {
		req := ProcessRequest{Target: target, Event: event.Clone(), Recipients: group}
		if target.IsLocal(d.localNodeID) && d.local != nil {
			if err := d.local.Process(ctx, req); err != nil {
				return err
			}
			continue
		}
		if d.remote == nil {
			return ErrRouteNotReady
		}
		if err := d.remote.ProcessRemote(ctx, req); err != nil {
			return err
		}
	}
	return nil
}

func (d *Dispatcher) groupByTarget(ctx context.Context, recipients []Recipient) (map[authority.Target][]Recipient, error) {
	groups := make(map[authority.Target][]Recipient)
	for _, recipient := range recipients {
		if recipient.UID == "" {
			continue
		}
		if d.resolver == nil {
			return nil, ErrRouteNotReady
		}
		target, err := d.resolver.ResolveRecipientAuthority(ctx, recipient.UID)
		if err != nil {
			return nil, err
		}
		groups[target] = append(groups[target], recipient)
	}
	return groups, nil
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internalv2/usecase/recipient`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internalv2/usecase/recipient
git commit -m "feat: dispatch committed messages by recipient authority"
```

---

### Task 8: Recipient Authority RPC

**Files:**
- Create: `internalv2/access/node/recipient_authority_codec.go`
- Create: `internalv2/access/node/recipient_authority_rpc.go`
- Create: `internalv2/access/node/recipient_authority_codec_test.go`
- Create: `internalv2/access/node/recipient_authority_rpc_test.go`
- Modify: `internalv2/access/node/presence_rpc.go`

- [ ] **Step 1: Write recipient codec tests**

Add `internalv2/access/node/recipient_authority_codec_test.go`:

```go
package node

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/authority"
	"github.com/WuKongIM/WuKongIM/internalv2/contracts/messageevents"
	recipientusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/recipient"
)

func TestRecipientAuthorityCodecRoundTrip(t *testing.T) {
	req := recipientAuthorityRequest{
		Target: authority.Target{HashSlot: 1, SlotID: 2, LeaderNodeID: 3, RouteRevision: 4, AuthorityEpoch: 5},
		Event:  messageevents.MessageCommitted{MessageID: 10, MessageSeq: 2, ChannelID: "g1", ChannelType: 2, FromUID: "u1", Payload: []byte("hello")},
		Recipients: []recipientusecase.Recipient{{UID: "u2"}, {UID: "u3", JoinSeq: 4}},
	}
	body, err := encodeRecipientAuthorityRequest(req)
	if err != nil {
		t.Fatalf("encodeRecipientAuthorityRequest() error = %v", err)
	}
	got, err := decodeRecipientAuthorityRequest(body)
	if err != nil {
		t.Fatalf("decodeRecipientAuthorityRequest() error = %v", err)
	}
	if got.Target != req.Target || got.Event.MessageID != 10 || len(got.Recipients) != 2 || got.Recipients[1].JoinSeq != 4 {
		t.Fatalf("decoded request = %#v, want %#v", got, req)
	}
}
```

- [ ] **Step 2: Write recipient RPC tests**

Add `internalv2/access/node/recipient_authority_rpc_test.go`:

```go
package node

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/authority"
	"github.com/WuKongIM/WuKongIM/internalv2/contracts/messageevents"
	recipientusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/recipient"
)

func TestHandleRecipientAuthorityRPCCallsProcessor(t *testing.T) {
	processor := &recordingRecipientAuthorityProcessor{}
	adapter := New(Options{RecipientAuthority: processor})
	req := recipientAuthorityRequest{
		Target: authority.Target{HashSlot: 1, SlotID: 2, LeaderNodeID: 3, RouteRevision: 4, AuthorityEpoch: 5},
		Event:  messageevents.MessageCommitted{MessageID: 10},
		Recipients: []recipientusecase.Recipient{{UID: "u2"}},
	}
	body, err := encodeRecipientAuthorityRequest(req)
	if err != nil {
		t.Fatalf("encodeRecipientAuthorityRequest() error = %v", err)
	}
	respBody, err := adapter.HandleRecipientAuthorityRPC(context.Background(), body)
	if err != nil {
		t.Fatalf("HandleRecipientAuthorityRPC() error = %v", err)
	}
	resp, err := decodeRecipientAuthorityResponse(respBody)
	if err != nil {
		t.Fatalf("decodeRecipientAuthorityResponse() error = %v", err)
	}
	if resp.Status != rpcStatusOK || processor.req.Target != req.Target {
		t.Fatalf("resp/req = %#v/%#v, want ok and target", resp, processor.req)
	}
}

type recordingRecipientAuthorityProcessor struct {
	req recipientusecase.ProcessRequest
}

func (p *recordingRecipientAuthorityProcessor) Process(_ context.Context, req recipientusecase.ProcessRequest) error {
	p.req = req
	return nil
}
```

- [ ] **Step 3: Run tests and verify they fail**

Run: `go test ./internalv2/access/node -run 'RecipientAuthority'`

Expected: FAIL because recipient RPC implementation does not exist.

- [ ] **Step 4: Implement codec and adapter**

Add codec functions matching the sender authority codec, using magic values:

```go
var (
	recipientAuthorityRequestMagic  = [...]byte{'W', 'K', 'V', 'A', 1}
	recipientAuthorityResponseMagic = [...]byte{'W', 'K', 'V', 'a', 1}
)
```

Encode/decode:

- `authority.Target`
- `messageevents.MessageCommitted`
- `[]recipient.Recipient`
- response `Status string`

Add `internalv2/access/node/recipient_authority_rpc.go`:

```go
package node

import (
	"context"
	"fmt"

	recipientusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/recipient"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/clusterv2/net"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

const RecipientAuthorityRPCServiceID uint8 = clusternet.RPCRecipientAuthority

type RecipientAuthority interface {
	Process(context.Context, recipientusecase.ProcessRequest) error
}

func (a *Adapter) HandleRecipientAuthorityRPC(ctx context.Context, payload []byte) ([]byte, error) {
	req, err := decodeRecipientAuthorityRequest(payload)
	if err != nil {
		a.rpcLogger().Warn("recipient authority rpc decode failed", wklog.Event("internalv2.access.node.recipient_authority_decode_failed"), wklog.Int("payloadBytes", len(payload)), wklog.Error(err))
		return nil, err
	}
	if a == nil || a.recipientAuthority == nil {
		return encodeRecipientAuthorityResponse(recipientAuthorityResponse{Status: rpcStatusRejected})
	}
	err = a.recipientAuthority.Process(ctx, recipientusecase.ProcessRequest{Target: req.Target, Event: req.Event, Recipients: req.Recipients})
	return encodeRecipientAuthorityResponse(recipientAuthorityResponse{Status: recipientAuthorityStatusForError(err)})
}

func (c *Client) ProcessRecipientAuthority(ctx context.Context, nodeID uint64, req recipientusecase.ProcessRequest) error {
	if c == nil || c.node == nil {
		return fmt.Errorf("internalv2/access/node: recipient authority rpc client not configured")
	}
	body, err := encodeRecipientAuthorityRequest(recipientAuthorityRequest{Target: req.Target, Event: req.Event, Recipients: req.Recipients})
	if err != nil {
		return err
	}
	respBody, err := c.node.CallRPC(ctx, nodeID, RecipientAuthorityRPCServiceID, body)
	if err != nil {
		return err
	}
	resp, err := decodeRecipientAuthorityResponse(respBody)
	if err != nil {
		return err
	}
	return recipientAuthorityErrorForStatus(resp.Status)
}
```

Extend `Options` and `Adapter` in `presence_rpc.go` with `RecipientAuthority RecipientAuthority`.

- [ ] **Step 5: Run tests**

Run: `go test ./internalv2/access/node`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internalv2/access/node
git commit -m "feat: add recipient authority node rpc"
```

---

### Task 9: Cluster Recipient Authority Adapter

**Files:**
- Create: `internalv2/infra/cluster/recipient_authority.go`
- Create: `internalv2/infra/cluster/recipient_authority_test.go`

- [ ] **Step 1: Write adapter tests**

Add `internalv2/infra/cluster/recipient_authority_test.go`:

```go
package cluster

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/authority"
	recipientusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/recipient"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
)

func TestRecipientAuthorityResolverMapsRoute(t *testing.T) {
	node := &fakeRecipientAuthorityNode{route: clusterv2.Route{HashSlot: 1, SlotID: 2, Leader: 3, Revision: 4, AuthorityEpoch: 5}}
	client := NewRecipientAuthorityClient(node, nil)
	target, err := client.ResolveRecipientAuthority(context.Background(), "u1")
	if err != nil {
		t.Fatalf("ResolveRecipientAuthority() error = %v", err)
	}
	want := authority.Target{HashSlot: 1, SlotID: 2, LeaderNodeID: 3, RouteRevision: 4, AuthorityEpoch: 5}
	if target != want {
		t.Fatalf("target = %#v, want %#v", target, want)
	}
}

func TestRecipientAuthorityRemoteForwardsToTargetLeader(t *testing.T) {
	node := &fakeRecipientAuthorityNode{nodeID: 1}
	remote := &recordingRecipientRemote{}
	client := NewRecipientAuthorityClientWithRemote(node, remote)
	req := recipientusecase.ProcessRequest{Target: authority.Target{LeaderNodeID: 2}, Recipients: []recipientusecase.Recipient{{UID: "u1"}}}
	if err := client.ProcessRemote(context.Background(), req); err != nil {
		t.Fatalf("ProcessRemote() error = %v", err)
	}
	if remote.nodeID != 2 || len(remote.req.Recipients) != 1 {
		t.Fatalf("remote node/req = %d/%#v, want node 2 and request", remote.nodeID, remote.req)
	}
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run: `go test ./internalv2/infra/cluster -run 'RecipientAuthority'`

Expected: FAIL because recipient cluster adapter does not exist.

- [ ] **Step 3: Implement adapter**

Add `internalv2/infra/cluster/recipient_authority.go`:

```go
package cluster

import (
	"context"
	"fmt"

	accessnode "github.com/WuKongIM/WuKongIM/internalv2/access/node"
	"github.com/WuKongIM/WuKongIM/internalv2/contracts/authority"
	recipientusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/recipient"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
)

type RecipientAuthorityNode interface {
	NodeID() uint64
	RouteKey(string) (clusterv2.Route, error)
	CallRPC(context.Context, uint64, uint8, []byte) ([]byte, error)
	RegisterRPC(uint8, clusterv2.NodeRPCHandler)
}

type RecipientAuthorityClient struct {
	node   RecipientAuthorityNode
	remote *accessnode.Client
}

func NewRecipientAuthorityClient(node RecipientAuthorityNode, remote *accessnode.Client) *RecipientAuthorityClient {
	if remote == nil {
		remote = accessnode.NewClient(node)
	}
	return &RecipientAuthorityClient{node: node, remote: remote}
}

func (c *RecipientAuthorityClient) ResolveRecipientAuthority(ctx context.Context, uid string) (authority.Target, error) {
	if c == nil || c.node == nil {
		return authority.Target{}, recipientusecase.ErrRouteNotReady
	}
	route, err := c.node.RouteKey(uid)
	if err != nil {
		return authority.Target{}, mapRecipientRouteError(err)
	}
	if route.Leader == 0 {
		return authority.Target{}, fmt.Errorf("%w: route leader unknown", recipientusecase.ErrRouteNotReady)
	}
	return authority.Target{HashSlot: route.HashSlot, SlotID: route.SlotID, LeaderNodeID: route.Leader, RouteRevision: route.Revision, AuthorityEpoch: route.AuthorityEpoch}, nil
}

func (c *RecipientAuthorityClient) ProcessRemote(ctx context.Context, req recipientusecase.ProcessRequest) error {
	if c == nil || c.remote == nil {
		return recipientusecase.ErrRouteNotReady
	}
	return c.remote.ProcessRecipientAuthority(ctx, req.Target.LeaderNodeID, req)
}
```

Map clusterv2 route errors to `recipientusecase.ErrRouteNotReady`, `ErrNotLeader`, and `ErrStaleRoute`.

- [ ] **Step 4: Run tests**

Run: `go test ./internalv2/infra/cluster -run 'RecipientAuthority|Delivery|ConversationAuthority'`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internalv2/infra/cluster/recipient_authority.go internalv2/infra/cluster/recipient_authority_test.go
git commit -m "feat: route recipient authority through clusterv2"
```

---

### Task 10: App Wiring and Old Sink Removal

**Files:**
- Modify: `internalv2/app/app.go`
- Modify: `internalv2/app/conversation.go`
- Modify: `internalv2/app/delivery.go`
- Modify: `internalv2/app/delivery_meta.go`
- Modify: `internalv2/app/app_test.go`

- [ ] **Step 1: Write app wiring tests**

Add to `internalv2/app/app_test.go`:

```go
func TestNewWiresGatewayToSenderAuthorityRouter(t *testing.T) {
	app := newTestApp(t, Config{Delivery: DeliveryConfig{Enabled: true}})
	if _, ok := app.handlerMessages().(*messageusecase.SenderAuthorityRouter); !ok {
		t.Fatalf("gateway messages = %T, want *message.SenderAuthorityRouter", app.handlerMessages())
	}
}

func TestNewRegistersSenderAndRecipientAuthorityRPC(t *testing.T) {
	cluster := newRecordingRPCCluster()
	app := newTestAppWithCluster(t, cluster, Config{Delivery: DeliveryConfig{Enabled: true}})
	if app == nil {
		t.Fatal("app is nil")
	}
	if !cluster.hasRPC(accessnode.SenderAuthorityRPCServiceID) {
		t.Fatalf("sender authority rpc was not registered")
	}
	if !cluster.hasRPC(accessnode.RecipientAuthorityRPCServiceID) {
		t.Fatalf("recipient authority rpc was not registered")
	}
}
```

Use the existing app test helper style. If no helper exposes handler messages, add a test-only method in `internalv2/app/app_test.go` using existing unexported access rather than production API.

- [ ] **Step 2: Run tests and verify they fail**

Run: `go test ./internalv2/app -run 'TestNewWiresGatewayToSenderAuthorityRouter|TestNewRegistersSenderAndRecipientAuthorityRPC'`

Expected: FAIL because app wiring still injects raw `message.App` and does not register new RPCs.

- [ ] **Step 3: Wire sender authority**

In `internalv2/app/app.go`, after creating `app.messages`, create:

```go
senderAuthority := clusterinfra.NewSenderAuthorityClient(app.cluster.(clusterinfra.SenderAuthorityNode), nil)
app.senderMessages = message.NewSenderAuthorityRouter(message.SenderAuthorityRouterOptions{
	LocalNodeID: clusterCfg.NodeID,
	Resolver:    senderAuthority,
	Local:       app.messages,
	Remote:      senderAuthority,
})
```

Then pass `Messages: app.senderMessages` to `accessgateway.New`.

Add an `App` field:

```go
senderMessages accessgateway.MessageUsecase
```

Keep sender RPC using a local submitter wrapper around `app.messages`, not `app.senderMessages`.

- [ ] **Step 4: Wire recipient pipeline as committed sink**

Create the recipient processor and dispatcher in `internalv2/app/app.go`:

```go
recipientAuthority := clusterinfra.NewRecipientAuthorityClient(app.cluster.(clusterinfra.RecipientAuthorityNode), nil)
recipientProcessor := recipientusecase.NewProcessor(recipientusecase.ProcessorOptions{
	LocalNodeID:  clusterCfg.NodeID,
	Conversation: app.recipientConversationUpdater(),
	Delivery:     app.recipientDeliverySubmitter(),
})
recipientDispatcher := recipientusecase.NewDispatcher(recipientusecase.DispatcherOptions{
	LocalNodeID:      clusterCfg.NodeID,
	Recipients:      app.recipientSource(),
	Resolver:        recipientAuthority,
	Local:           recipientProcessor,
	Remote:          recipientAuthority,
	PageSize:        app.cfg.Delivery.FanoutPageSize,
	TargetBatchSize: app.cfg.Delivery.PushBatchSize,
})
messageOpts.Committed = recipientDispatcher
```

Add small app adapters:

- `recipientConversationUpdater` calls the existing conversation authority `AdmitPatches`.
- `recipientDeliverySubmitter` calls `delivery.SubmitCommitted`.
- `recipientSource` adapts `deliveryMetaStore` subscriber pages to `recipient.RecipientPage`.

- [ ] **Step 5: Register sender and recipient RPC handlers**

When the cluster supports RPC registration, add:

```go
adapter := accessnode.New(accessnode.Options{
	SenderAuthority:    localSenderAuthority{submitter: app.messages, localNodeID: clusterCfg.NodeID},
	RecipientAuthority: recipientProcessor,
	Delivery:           localPusher,
	DeliveryFanout:     fanoutWorker,
	ConversationAuthority: app.conversationAuthority,
	Logger:             app.logger.Named("node"),
})
presenceNode.RegisterRPC(accessnode.SenderAuthorityRPCServiceID, nodeRPCHandlerFunc(adapter.HandleSenderAuthorityRPC))
presenceNode.RegisterRPC(accessnode.RecipientAuthorityRPCServiceID, nodeRPCHandlerFunc(adapter.HandleRecipientAuthorityRPC))
```

`localSenderAuthority.SendBatchForAuthority` must reject non-local targets before calling `app.messages.SendBatch`.

- [ ] **Step 6: Delete replaced committed sink group path**

Remove `combineCommittedSinks` usage from `internalv2/app/app.go`. Delete or reduce `committedSinkGroup` in `internalv2/app/conversation.go` after tests no longer reference it. Keep standalone conversation authority cache helpers.

- [ ] **Step 7: Run tests**

Run: `go test ./internalv2/app`

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internalv2/app
git commit -m "feat: wire internalv2 authority send pipeline"
```

---

### Task 11: Channel Idempotency Lookup Adapter

**Files:**
- Modify: `internalv2/infra/cluster/message_reader.go`
- Modify: `internalv2/infra/cluster/message_reader_test.go`
- Modify: `pkg/channelv2/channel.go`
- Modify: `pkg/channelv2/service/lookup.go`
- Modify: `pkg/channelv2/store/adapter.go`
- Modify: `pkg/channelv2/store/channel_adapter.go`

- [ ] **Step 1: Add infra lookup tests**

Add to `internalv2/infra/cluster/message_reader_test.go`:

```go
func TestChannelMessageReaderLooksUpIdempotentSend(t *testing.T) {
	node := &fakeChannelMessageReadNode{
		idempotency: channelv2.Message{MessageID: 42, MessageSeq: 7, ChannelID: "g1", ChannelType: 2, FromUID: "u1", ClientMsgNo: "c1"},
		idempotencyOK: true,
	}
	reader := NewChannelMessageReader(node)
	result, ok, err := reader.LookupSend(context.Background(), message.IdempotencyQuery{FromUID: "u1", ClientMsgNo: "c1", ChannelID: "g1", ChannelType: 2})
	if err != nil {
		t.Fatalf("LookupSend() error = %v", err)
	}
	if !ok || result.MessageID != 42 || result.MessageSeq != 7 {
		t.Fatalf("LookupSend() = %#v ok %v, want 42/7 true", result, ok)
	}
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run: `go test ./internalv2/infra/cluster -run 'TestChannelMessageReaderLooksUpIdempotentSend'`

Expected: FAIL because the lookup surface does not exist.

- [ ] **Step 3: Add channelv2 lookup surface**

Add to `pkg/channelv2/channel.go`:

```go
// IdempotencyLookup verifies whether a sender/client key already committed.
type IdempotencyLookup interface {
	LookupIdempotency(context.Context, ChannelID, string, string) (Message, bool, error)
}
```

Add service plumbing in `pkg/channelv2/service/lookup.go` and store adapter plumbing using the existing `pkg/db/message.LookupIdempotency` support. Return a `channelv2.Message` with at least `MessageID`, `MessageSeq`, `ChannelID`, `ChannelType`, `FromUID`, and `ClientMsgNo`.

- [ ] **Step 4: Implement `LookupSend` in infra**

Modify `internalv2/infra/cluster/message_reader.go` so `ChannelMessageReader` implements `message.SendIdempotencyLookup`:

```go
func (r *ChannelMessageReader) LookupSend(ctx context.Context, query message.IdempotencyQuery) (message.SendResult, bool, error) {
	lookup, ok := r.node.(interface {
		LookupIdempotency(context.Context, channelv2.ChannelID, string, string) (channelv2.Message, bool, error)
	})
	if !ok {
		return message.SendResult{}, false, nil
	}
	msg, found, err := lookup.LookupIdempotency(ctx, channelv2.ChannelID{ID: query.ChannelID, Type: query.ChannelType}, query.FromUID, query.ClientMsgNo)
	if err != nil {
		return message.SendResult{}, false, mapReadError(err)
	}
	if !found {
		return message.SendResult{}, false, nil
	}
	return message.SendResult{MessageID: msg.MessageID, MessageSeq: msg.MessageSeq, Reason: message.ReasonSuccess}, true, nil
}
```

- [ ] **Step 5: Wire idempotency into `message.Options`**

In `internalv2/app/app.go`, when `readNode` is available and `messageOpts.MessageReader` is set to `reader`, also set:

```go
messageOpts.Idempotency = reader
```

- [ ] **Step 6: Run tests**

Run: `go test ./pkg/channelv2/... ./internalv2/infra/cluster ./internalv2/app`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add pkg/channelv2 internalv2/infra/cluster internalv2/app
git commit -m "feat: recover sends by channel idempotency"
```

---

### Task 12: Flow Docs and Targeted E2E Coverage

**Files:**
- Create: `internalv2/usecase/recipient/FLOW.md`
- Modify: `internalv2/FLOW.md`
- Modify: `internalv2/access/gateway/FLOW.md`
- Modify: `internalv2/access/node/FLOW.md`
- Modify: `internalv2/usecase/message/FLOW.md`
- Modify: `internalv2/usecase/conversation/FLOW.md`
- Modify: `internalv2/usecase/delivery/FLOW.md`
- Modify: `internalv2/infra/cluster/FLOW.md`
- Modify: `test/e2e/message/wukongimv2_single_node_send/sendack_test.go`

- [ ] **Step 1: Write recipient FLOW**

Add `internalv2/usecase/recipient/FLOW.md`:

```markdown
# internalv2/usecase/recipient Flow

## Responsibility

`internalv2/usecase/recipient` owns recipient-authority post-commit orchestration. It receives committed channel messages after `SENDACK` eligibility, resolves bounded recipient pages, groups recipients by UID authority, and guarantees each recipient UID's recent conversation row is admitted before scoped delivery is submitted.

The package is entry-agnostic and cluster-agnostic. It must not import gateway frames, clusterv2, channelv2, access adapters, or the app composition root.

## Flow

```text
MessageCommitted
  -> Dispatcher.SubmitCommitted
  -> resolve recipients from MessageScopedUIDs, person participants, or paged subscribers
  -> group recipients by fenced UID authority target
  -> local Processor.Process or remote recipient authority RPC
  -> project one conversation active patch per recipient UID
  -> admit conversation patches
  -> submit delivery with MessageScopedUIDs set to the same recipient group
```

Delivery receives scoped UID groups and does not rescan channel subscribers on this path.
```

- [ ] **Step 2: Update existing FLOW files**

Update each listed `FLOW.md` with the new two-stage path:

```text
Gateway SEND
  -> sender UID authority router
  -> local channel commit usecase
  -> channel authority durable append
  -> SENDACK
  -> recipient authority pipeline
  -> conversation update
  -> scoped delivery
  -> connection owner push
```

In `internalv2/usecase/delivery/FLOW.md`, state that the new post-commit path supplies scoped UIDs and delivery no longer owns subscriber scanning for that path.

- [ ] **Step 3: Add or adjust single-node e2e assertions**

In `test/e2e/message/wukongimv2_single_node_send/sendack_test.go`, keep the existing `SEND -> SENDACK -> conversation visible -> recv` checks. Add an assertion that `SENDACK` arrives before waiting for conversation/delivery side effects by reading the ack first and only then polling conversation/recv.

- [ ] **Step 4: Run targeted tests**

Run:

```bash
go test ./internalv2/... ./pkg/clusterv2/net ./pkg/channelv2/...
go test ./test/e2e/message/wukongimv2_single_node_send -run Test -count=1
```

Expected: PASS. If the e2e package requires integration tags in this repo, run the narrow equivalent with the existing tag used by that package and record the exact command in the final task summary.

- [ ] **Step 5: Commit**

```bash
git add internalv2 test/e2e/message/wukongimv2_single_node_send pkg/channelv2 pkg/clusterv2/net
git commit -m "docs: update internalv2 authority send flows"
```

---

## Final Verification

- [ ] Run unit tests:

```bash
go test ./internalv2/... ./pkg/clusterv2/net ./pkg/channelv2/...
```

Expected: PASS.

- [ ] Run focused e2e:

```bash
go test ./test/e2e/message/wukongimv2_single_node_send -count=1
```

Expected: PASS or documented skip if the package requires integration-only runtime setup.

- [ ] Inspect old path removal:

```bash
rg -n "combineCommittedSinks|DeliveryFanoutRPCServiceID|DeliveryFanout|SubscriberPlanner" internalv2
```

Expected: no old parallel committed sink path remains. Delivery fanout/subscriber planner references may remain only where they support connection-owner push tests or compatibility explicitly kept by the implementation.

- [ ] Check status:

```bash
git status --short
```

Expected: clean.
