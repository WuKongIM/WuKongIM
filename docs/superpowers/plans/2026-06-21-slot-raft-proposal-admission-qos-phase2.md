# Slot Raft Proposal Admission QoS Phase 2 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add long-term Slot Raft proposal admission classes so background conversation-active projection pressure is bounded and observable while foreground metadata proposals retain reserved queue capacity.

**Architecture:** Keep the public `Runtime.Propose(ctx, slotID, data)` shape stable and carry proposal class through context, defaulting to foreground for existing callers. Multi-Raft owns admission enforcement; `pkg/clusterv2/propose` propagates class across local and forwarded Slot proposals; conversation active durable flushes mark their Slot metadata writes as background. Admission results become explicit typed errors and low-cardinality metrics.

**Tech Stack:** Go 1.25, `pkg/slot/multiraft`, `pkg/clusterv2/propose`, `pkg/clusterv2`, `internalv2/infra/cluster`, `internalv2/app` observability, Prometheus metrics.

---

## File Structure

- Modify `pkg/slot/multiraft/types.go`: add `ProposalClass`, `ProposalAdmissionObserver`, and `RaftOptions.MaxQueuedBackgroundControls`.
- Modify `pkg/slot/multiraft/stage_observer.go`: add `WithProposalClass` and `ProposalClassFromContext`.
- Modify `pkg/slot/multiraft/errors.go`: add typed retryable admission errors.
- Modify `pkg/slot/multiraft/config.go`: default and validate foreground/background admission limits.
- Modify `pkg/slot/multiraft/slot.go`: track queued background proposal controls, enforce admission, and emit admission observations outside locks.
- Modify `pkg/slot/multiraft/api.go`: stamp proposal controls with the context class.
- Modify `pkg/clusterv2/propose/types.go`: add clusterv2 proposal class context helpers and retryable errors.
- Modify `pkg/clusterv2/propose/codec.go`: encode/decode proposal class in forwarded requests while accepting legacy v1 payloads.
- Modify `pkg/clusterv2/propose/service.go` and `forward.go`: propagate class to local and remote leaders.
- Modify `pkg/clusterv2/default_slot_proposer.go`: map clusterv2 proposal class to Multi-Raft proposal class and map Multi-Raft typed errors back to propose typed errors.
- Modify `pkg/clusterv2/node_meta.go`: mark `TouchUserConversationActiveAtBatch` as background before proposing.
- Modify `internalv2/infra/cluster/conversation_authority.go`: treat background proposal throttling as bounded retryable route pressure for active-batch/flush paths.
- Modify `pkg/metrics/slot.go` and `internalv2/app/observability.go`: expose `wukongim_slot_proposal_admission_total{class,result}`.
- Update `pkg/clusterv2/FLOW.md` and `internalv2/infra/cluster/FLOW.md` if the final behavior differs from current flow notes.

---

### Task 1: Preserve Slot Runtime Pressure Guards

**Files:**
- Modify: `pkg/slot/multiraft/config.go`
- Modify: `pkg/slot/multiraft/runtime.go`
- Modify: `pkg/slot/multiraft/scheduler.go`
- Modify: `pkg/slot/multiraft/slot.go`
- Modify: `pkg/slot/multiraft/types.go`
- Test: `pkg/slot/multiraft/config_test.go`
- Test: `pkg/slot/multiraft/scheduler_test.go`
- Test: `pkg/slot/multiraft/step_test.go`
- Test: `pkg/slot/multiraft/proposal_test.go`

- [ ] **Step 1: Verify existing guard tests**

Run:

```bash
go test ./pkg/slot/multiraft -run 'TestNormalizeRaftOptionsDefaultsBackpressureLimits|TestValidateRaftOptionsRejectsNegativeBackpressureLimits|TestEnqueueRequestRejectsWhenQueueLimitReached|TestEnqueueControlRejectsWhenQueueLimitReached|TestBeginApplyRejectsWhenApplyLimitReached|TestReusableBuffersClearRetainedReferences|TestSchedulerPendingBufferReusesBackingAfterDrain|TestRuntimeTickScratchReusesBackingAndClearsReferences|TestApplyCommittedEntriesPassesCommandDataWithoutExtraCopy' -count=1
```

Expected: PASS.

- [ ] **Step 2: Keep the guard implementation narrow**

Ensure these invariants stay true while later tasks modify admission:

```go
const (
	defaultMaxQueuedRequests = uint64(10000)
	defaultMaxQueuedControls = uint64(10000)
	defaultMaxApplyingTasks  = uint64(1024)
)
```

Use the actual repository defaults if they already differ, but keep the meaning unchanged:

```go
// MaxQueuedRequests bounds inbound Raft messages buffered per Slot before scheduler processing catches up.
// MaxQueuedControls bounds locally submitted control actions per Slot.
// MaxApplyingTasks bounds async apply tasks accepted per Slot.
```

- [ ] **Step 3: Re-run the package**

Run:

```bash
go test ./pkg/slot/multiraft -count=1
```

Expected: PASS.

---

### Task 2: Add Multi-Raft Proposal Classes And Background Admission

**Files:**
- Modify: `pkg/slot/multiraft/types.go`
- Modify: `pkg/slot/multiraft/stage_observer.go`
- Modify: `pkg/slot/multiraft/errors.go`
- Modify: `pkg/slot/multiraft/config.go`
- Modify: `pkg/slot/multiraft/api.go`
- Modify: `pkg/slot/multiraft/slot.go`
- Test: `pkg/slot/multiraft/config_test.go`
- Test: `pkg/slot/multiraft/step_test.go`

- [ ] **Step 1: Write failing tests for class defaults and background reserve**

Add tests shaped like:

```go
func TestProposalClassContextDefaultsForeground(t *testing.T) {
	if got := ProposalClassFromContext(context.Background()); got != ProposalClassForeground {
		t.Fatalf("default class = %q, want %q", got, ProposalClassForeground)
	}
	ctx := WithProposalClass(context.Background(), ProposalClassBackground)
	if got := ProposalClassFromContext(ctx); got != ProposalClassBackground {
		t.Fatalf("context class = %q, want %q", got, ProposalClassBackground)
	}
}

func TestBackgroundProposalAdmissionLeavesForegroundReserve(t *testing.T) {
	g := newLeaderTestSlotForDrain()
	g.maxQueuedControls = 4
	g.maxQueuedBackgroundControls = 1

	if err := g.enqueueControl(controlAction{kind: controlPropose, proposalClass: ProposalClassBackground, future: newFuture(nil)}); err != nil {
		t.Fatalf("first background enqueue error = %v", err)
	}
	if err := g.enqueueControl(controlAction{kind: controlPropose, proposalClass: ProposalClassBackground, future: newFuture(nil)}); !errors.Is(err, ErrBackgroundProposalThrottled) {
		t.Fatalf("second background enqueue error = %v, want ErrBackgroundProposalThrottled", err)
	}
	if err := g.enqueueControl(controlAction{kind: controlPropose, proposalClass: ProposalClassForeground, future: newFuture(nil)}); err != nil {
		t.Fatalf("foreground enqueue after background throttle error = %v", err)
	}
}
```

- [ ] **Step 2: Run tests to verify RED**

Run:

```bash
go test ./pkg/slot/multiraft -run 'TestProposalClassContextDefaultsForeground|TestBackgroundProposalAdmissionLeavesForegroundReserve' -count=1
```

Expected: FAIL because `ProposalClass`, `WithProposalClass`, `ProposalClassFromContext`, and `controlAction.proposalClass` do not exist yet.

- [ ] **Step 3: Implement proposal class and typed errors**

Add:

```go
type ProposalClass string

const (
	ProposalClassForeground ProposalClass = "foreground"
	ProposalClassBackground ProposalClass = "background"
)

type ProposalAdmissionObserver interface {
	ObserveSlotProposalAdmission(slotID SlotID, class ProposalClass, result string)
}
```

Add retryable sentinels:

```go
ErrProposalBackpressure        = errors.New("multiraft: proposal backpressure")
ErrBackgroundProposalThrottled = errors.New("multiraft: background proposal throttled")
ErrApplyBacklogHigh            = errors.New("multiraft: apply backlog high")
```

Add context helpers:

```go
func WithProposalClass(ctx context.Context, class ProposalClass) context.Context
func ProposalClassFromContext(ctx context.Context) ProposalClass
```

Normalize unknown class values to foreground.

- [ ] **Step 4: Implement background admission accounting**

Extend `controlAction` and `slot`:

```go
proposalClass                ProposalClass
maxQueuedBackgroundControls  int
queuedBackgroundControls     int
```

Admission rules:

```go
if queueLimitReached(g.maxQueuedControls, len(g.controls)) {
	return fmt.Errorf("%w: %w", ErrProposalBackpressure, ErrSlotBusy)
}
if action.kind == controlPropose && action.proposalClass == ProposalClassBackground &&
	queueLimitReached(g.maxQueuedBackgroundControls, g.queuedBackgroundControls) {
	return fmt.Errorf("%w: %w", ErrBackgroundProposalThrottled, ErrProposalBackpressure)
}
```

Increment `queuedBackgroundControls` only after admission succeeds; decrement it when the action leaves `g.controls` in `takeControlBatch`.

- [ ] **Step 5: Run tests to verify GREEN**

Run:

```bash
go test ./pkg/slot/multiraft -run 'TestProposalClassContextDefaultsForeground|TestBackgroundProposalAdmissionLeavesForegroundReserve|TestEnqueueControlRejectsWhenQueueLimitReached' -count=1
```

Expected: PASS.

---

### Task 3: Propagate Proposal Class Through Clusterv2 Local And Remote Propose

**Files:**
- Modify: `pkg/clusterv2/propose/types.go`
- Modify: `pkg/clusterv2/propose/codec.go`
- Modify: `pkg/clusterv2/propose/service.go`
- Modify: `pkg/clusterv2/propose/forward.go`
- Modify: `pkg/clusterv2/default_slot_proposer.go`
- Test: `pkg/clusterv2/propose/propose_test.go`
- Test: `pkg/clusterv2/default_slot_proposer_test.go`

- [ ] **Step 1: Write failing propagation tests**

Add tests shaped like:

```go
func TestServiceProposeRemoteLeaderCarriesProposalClass(t *testing.T) {
	forward := &fakeForward{}
	svc := NewService(Config{LocalNode: 1, Router: fakeRouter{route: routing.Route{HashSlot: 3, SlotID: 11, Leader: 2}}, Slots: &fakeSlots{}, Forward: forward})
	err := svc.Propose(WithProposalClass(context.Background(), ProposalClassBackground), Request{Key: "u1", Command: []byte("cmd")})
	if err != nil {
		t.Fatalf("Propose() error = %v", err)
	}
	if forward.req.Class != ProposalClassBackground {
		t.Fatalf("forward class = %q, want background", forward.req.Class)
	}
}

func TestForwardHandlerPassesProposalClassToSlots(t *testing.T) {
	slots := &fakeSlots{local: true}
	handler := NewForwardHandler(slots)
	payload, err := EncodeForwardRequest(ForwardRequest{SlotID: 1, HashSlot: 0, Payload: EncodePayload(0, []byte("cmd")), Class: ProposalClassBackground})
	if err != nil {
		t.Fatalf("EncodeForwardRequest() error = %v", err)
	}
	if _, err := handler.HandleRPC(context.Background(), payload); err != nil {
		t.Fatalf("HandleRPC() error = %v", err)
	}
	if got := ProposalClassFromContext(slots.ctx); got != ProposalClassBackground {
		t.Fatalf("slot context class = %q, want background", got)
	}
}
```

- [ ] **Step 2: Run tests to verify RED**

Run:

```bash
go test ./pkg/clusterv2/propose -run 'TestServiceProposeRemoteLeaderCarriesProposalClass|TestForwardHandlerPassesProposalClassToSlots' -count=1
```

Expected: FAIL because the class field and helpers do not exist.

- [ ] **Step 3: Implement clusterv2 proposal class and codec**

Add `ProposalClass` helpers to `pkg/clusterv2/propose`, add `Class ProposalClass` to `ForwardRequest`, and encode v2 forwards as:

```text
[version:1][class:1][slot_id:4][hash_slot:2][payload_len:4][payload:n]
```

Keep decode support for legacy v1:

```text
[version:1][slot_id:4][hash_slot:2][payload_len:4][payload:n]
```

Legacy v1 decodes as foreground.

- [ ] **Step 4: Map class and typed errors at defaultSlotProposer**

Before calling `multiraft.Runtime.Propose`, map context class:

```go
if propose.ProposalClassFromContext(ctx) == propose.ProposalClassBackground {
	ctx = multiraft.WithProposalClass(ctx, multiraft.ProposalClassBackground)
}
```

Map errors:

```go
case errors.Is(err, multiraft.ErrBackgroundProposalThrottled):
	return propose.ErrBackgroundProposalThrottled
case errors.Is(err, multiraft.ErrProposalBackpressure):
	return propose.ErrProposalBackpressure
```

- [ ] **Step 5: Run tests to verify GREEN**

Run:

```bash
go test ./pkg/clusterv2/propose ./pkg/clusterv2 -run 'TestServiceProposeRemoteLeaderCarriesProposalClass|TestForwardHandlerPassesProposalClassToSlots|TestDefaultSlotProposerPassesBackgroundProposalClass' -count=1
```

Expected: PASS.

---

### Task 4: Mark Conversation Active Durable Flush As Background

**Files:**
- Modify: `pkg/clusterv2/node_meta.go`
- Modify: `internalv2/infra/cluster/conversation_authority.go`
- Test: `pkg/clusterv2/node_meta_conversation_test.go`
- Test: `internalv2/infra/cluster/conversation_authority_test.go`

- [ ] **Step 1: Write failing tests**

Add a clusterv2 test using a recording proposer:

```go
func TestTouchUserConversationActiveAtBatchMarksProposalBackground(t *testing.T) {
	node := &Node{cfg: Config{Slots: SlotConfig{HashSlotCount: 8}}, proposer: &recordingProposer{}}
	node.router.UpdateControlSnapshot(control.Snapshot{RoutesReady: true, HashSlotCount: 8, Slots: []control.Slot{{ID: 1, Leader: 1, HashSlots: []uint16{1}}}})
	err := node.TouchUserConversationActiveAtBatch(context.Background(), []metadb.UserConversationActivePatch{{UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAt: 10}})
	if err != nil {
		t.Fatalf("TouchUserConversationActiveAtBatch() error = %v", err)
	}
	if got := propose.ProposalClassFromContext(node.proposer.(*recordingProposer).ctx); got != propose.ProposalClassBackground {
		t.Fatalf("proposal class = %q, want background", got)
	}
}
```

Add an infra retry test where active batch or flush returns `propose.ErrBackgroundProposalThrottled` and is retried within the bounded active window.

- [ ] **Step 2: Run tests to verify RED**

Run:

```bash
go test ./pkg/clusterv2 -run TestTouchUserConversationActiveAtBatchMarksProposalBackground -count=1
go test ./internalv2/infra/cluster -run TestConversationAuthorityClientAdmitActiveBatchRetriesBackgroundBackpressure -count=1
```

Expected: FAIL because the background context and error mapping do not exist.

- [ ] **Step 3: Mark background and map retryable pressure**

In `TouchUserConversationActiveAtBatch`, wrap the context:

```go
ctx = propose.WithProposalClass(ctx, propose.ProposalClassBackground)
```

In `mapConversationRouteError`, map proposal pressure to route-not-ready:

```go
case errors.Is(err, propose.ErrBackgroundProposalThrottled), errors.Is(err, propose.ErrProposalBackpressure):
	return fmt.Errorf("%w: %w", conversationusecase.ErrRouteNotReady, err)
```

- [ ] **Step 4: Run tests to verify GREEN**

Run:

```bash
go test ./pkg/clusterv2 -run 'TestTouchUserConversationActiveAtBatchMarksProposalBackground|TestClusterV2TouchUserConversationActiveAtBatchRoutesByUID' -count=1
go test ./internalv2/infra/cluster -run 'TestConversationAuthorityClientAdmitActiveBatch' -count=1
```

Expected: PASS.

---

### Task 5: Add Proposal Admission Metrics

**Files:**
- Modify: `pkg/metrics/slot.go`
- Modify: `pkg/metrics/registry_test.go`
- Modify: `internalv2/app/observability.go`
- Modify: `internalv2/app/observability_test.go`

- [ ] **Step 1: Write failing metrics tests**

Add assertions that `slotMetricsObserver.ObserveSlotProposalAdmission(multiraft.ProposalClassBackground, "throttled")` increments:

```text
wukongim_slot_proposal_admission_total{class="background",result="throttled"}
```

- [ ] **Step 2: Run tests to verify RED**

Run:

```bash
go test ./pkg/metrics -run TestSlotAndTransportMetricsTrackProposalsLeaderChangesAndRPCs -count=1
go test ./internalv2/app -run TestRuntimePressureAdapterMapsGatewayChannelSlotTransportAndDB -count=1
```

Expected: FAIL because the metric and observer method do not exist.

- [ ] **Step 3: Implement metric and observer forwarding**

Add to `SlotMetrics`:

```go
proposalAdmission *prometheus.CounterVec
```

Register:

```go
Name: "wukongim_slot_proposal_admission_total"
Help: "Total Slot proposal admission decisions by class and result."
Labels: []string{"class", "result"}
```

Add:

```go
func (m *SlotMetrics) ObserveProposalAdmission(class string, result string)
```

Add observer forwarding in `slotMetricsObserver` and `multiSlotObserver`.

- [ ] **Step 4: Run tests to verify GREEN**

Run:

```bash
go test ./pkg/metrics -run TestSlotAndTransportMetricsTrackProposalsLeaderChangesAndRPCs -count=1
go test ./internalv2/app -run TestRuntimePressureAdapterMapsGatewayChannelSlotTransportAndDB -count=1
```

Expected: PASS.

---

### Task 6: Final Verification And Documentation

**Files:**
- Modify if needed: `pkg/clusterv2/FLOW.md`
- Modify if needed: `internalv2/infra/cluster/FLOW.md`
- Modify if needed: `docs/development/PROJECT_KNOWLEDGE.md`

- [ ] **Step 1: Run focused package tests**

Run:

```bash
go test ./pkg/slot/multiraft -count=1
go test ./pkg/clusterv2/propose ./pkg/clusterv2 -run 'TestServicePropose|TestForward|TestDefaultSlotProposer|TestClusterV2TouchUserConversationActiveAtBatch|TestTouchUserConversationActiveAtBatchMarksProposalBackground' -count=1
go test ./internalv2/infra/cluster -run 'TestConversationAuthorityClientAdmitActiveBatch' -count=1
go test ./pkg/metrics ./internalv2/app -run 'TestSlotAndTransportMetricsTrackProposalsLeaderChangesAndRPCs|TestRuntimePressureAdapterMapsGatewayChannelSlotTransportAndDB' -count=1
```

Expected: PASS.

- [ ] **Step 2: Run race on the changed core package**

Run:

```bash
go test -race ./pkg/slot/multiraft -count=1
```

Expected: PASS. A macOS linker `LC_DYSYMTAB` warning is acceptable only if tests pass.

- [ ] **Step 3: Check formatting and diff hygiene**

Run:

```bash
gofmt -w pkg/slot/multiraft pkg/clusterv2/propose pkg/clusterv2 internalv2/infra/cluster internalv2/app pkg/metrics
git diff --check
```

Expected: no output from `git diff --check`.

- [ ] **Step 4: Commit**

Stage only phase-2 files and commit:

```bash
git add docs/superpowers/plans/2026-06-21-slot-raft-proposal-admission-qos-phase2.md \
  pkg/slot/multiraft \
  pkg/clusterv2/propose \
  pkg/clusterv2/default_slot_proposer.go \
  pkg/clusterv2/default_slot_proposer_test.go \
  pkg/clusterv2/node_meta.go \
  pkg/clusterv2/node_meta_conversation_test.go \
  internalv2/infra/cluster/conversation_authority.go \
  internalv2/infra/cluster/conversation_authority_test.go \
  pkg/metrics/slot.go \
  pkg/metrics/registry_test.go \
  internalv2/app/observability.go \
  internalv2/app/observability_test.go \
  pkg/clusterv2/FLOW.md \
  internalv2/infra/cluster/FLOW.md
git commit -m "feat: add slot proposal admission qos"
```

Expected: one commit containing only phase-2 plan and implementation files.

---

## Self-Review

- Spec coverage: covers proposal admission classes, background throttling, retryable errors, conversation active low-priority proposals, remote forwarding propagation, and admission metrics. It intentionally does not add external config keys in this phase, so `wukongim.conf.example` does not need a config update.
- Placeholder scan: no deferred-work markers or unbounded test instructions remain.
- Type consistency: `ProposalClassForeground`, `ProposalClassBackground`, `ErrProposalBackpressure`, and `ErrBackgroundProposalThrottled` are used consistently across Multi-Raft, clusterv2 propose, and internalv2 infra mapping.
