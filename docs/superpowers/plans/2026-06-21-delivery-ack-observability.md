# Delivery Ack Observability Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make delivery pending recvack state observable through one runtime-owned ack event path, then verify `RECV -> RECVACK -> ack_bindings=0` with real `cmd/wukongimv2` black-box coverage.

**Architecture:** `internalv2/runtime/delivery.Manager` becomes the only producer of pending ack state events. `AckTracker` keeps an O(1) derived pending count, while app-level top and metrics observers subscribe to `AckEvent` and update `/top` and Prometheus gauges from the same count. E2E tests use public WKProto and HTTP APIs only.

**Tech Stack:** Go, `testing`, Prometheus client model helpers, existing `test/e2ev2/suite` process harness, WKProto frame clients.

---

## Execution Setup

Before touching implementation code, create an isolated worktree or branch via
`superpowers:using-git-worktrees`.

Suggested branch:

```bash
git switch -c codex/delivery-ack-observability
```

Use `GOWORK=off` for all Go commands in this repository.

## File Structure

- Modify `internalv2/runtime/delivery/ack_tracker.go`
  - Add an atomic derived pending count.
  - Add a result-returning bind method without breaking the existing `Bind(...) bool` API.
- Modify `internalv2/runtime/delivery/ack_tracker_test.go`
  - Cover overwrite, rejection, ack, session close, and expiry count behavior.
- Modify `internalv2/runtime/delivery/observability.go`
  - Add ack action/result constants, `AckEvent`, and `AckObserver`.
- Modify `internalv2/runtime/delivery/manager.go`
  - Wire `AckObserver` through `ManagerOptions`.
  - Emit ack events from bind, recvack, session close, and expiry.
- Create `internalv2/runtime/delivery/manager_ack_observer_test.go`
  - Verify Manager emits bounded ack events with correct changed and pending counts.
- Modify `internalv2/app/observability.go`
  - Map ack events into Prometheus delivery ack gauges.
  - Fan out ack events through `multiDeliveryObserver`.
- Modify `internalv2/app/top_observer.go`
  - Map ack events into top delivery ack gauges.
- Modify `internalv2/app/wiring.go`
  - Pass combined ack observer to `runtime/delivery.Manager`.
  - Remove top collector plumbing from `localOwnerPusher`.
- Modify `internalv2/app/delivery.go`
  - Remove `localOwnerPusher.top` and `observePendingAcks`.
- Modify `internalv2/app/top_collector_test.go`
  - Verify top observer maps `AckEvent.PendingCount` to `ack_bindings`.
- Modify `internalv2/app/observability_test.go`
  - Verify metrics observer maps `AckEvent.PendingCount` to `wukongim_delivery_ack_bindings`.
  - Verify combined delivery observer fans out ack events.
- Create `test/e2ev2/suite/top.go`
  - Add public `/top/v1/snapshot?view=delivery` helpers for e2e tests.
- Modify `test/e2ev2/suite/api_test.go`
  - Unit-test top helper JSON decoding.
- Modify `test/e2ev2/message/cross_node_delivery/cross_node_delivery_test.go`
  - Assert owner-node `ack_bindings` rises before recvack and returns to zero after recvack.
- Modify `test/e2ev2/message/cross_node_delivery/AGENTS.md`
  - Document the new ack binding black-box assertion.
- Modify `internalv2/runtime/delivery/FLOW.md`
  - Document ack event ownership and O(1) pending count.

## Task 1: AckTracker O(1) Pending Count

**Files:**
- Modify: `internalv2/runtime/delivery/ack_tracker.go`
- Modify: `internalv2/runtime/delivery/ack_tracker_test.go`

- [ ] **Step 1: Write failing AckTracker count tests**

Append these tests to `internalv2/runtime/delivery/ack_tracker_test.go`:

```go
func TestAckTrackerBindResultReportsAddedAndRejected(t *testing.T) {
	tracker := NewAckTracker(AckTrackerOptions{ShardCount: 4, MaxPendingPerSession: 1})

	first := tracker.BindResult(PendingRecvAck{UID: "u1", SessionID: 10, MessageID: 1001})
	if !first.Bound || !first.Added || first.PendingCount != 1 {
		t.Fatalf("first BindResult() = %#v, want bound added count 1", first)
	}

	overwrite := tracker.BindResult(PendingRecvAck{UID: "u1", SessionID: 10, MessageID: 1001, MessageSeq: 2})
	if !overwrite.Bound || overwrite.Added || overwrite.PendingCount != 1 {
		t.Fatalf("overwrite BindResult() = %#v, want bound not-added count 1", overwrite)
	}

	rejected := tracker.BindResult(PendingRecvAck{UID: "u1", SessionID: 10, MessageID: 1002})
	if rejected.Bound || rejected.Added || rejected.PendingCount != 1 {
		t.Fatalf("rejected BindResult() = %#v, want rejected count 1", rejected)
	}
}

func TestAckTrackerPendingCountStaysConsistentAcrossMutations(t *testing.T) {
	now := int64(200)
	tracker := NewAckTracker(AckTrackerOptions{
		ShardCount: 4,
		Now: func() int64 {
			return now
		},
	})

	tracker.Bind(PendingRecvAck{UID: "u1", SessionID: 10, MessageID: 1001, DeliveredAt: 100})
	tracker.Bind(PendingRecvAck{UID: "u1", SessionID: 10, MessageID: 1002, DeliveredAt: 190})
	tracker.Bind(PendingRecvAck{UID: "u2", SessionID: 20, MessageID: 2001, DeliveredAt: 100})
	if got := tracker.PendingCount(); got != 3 {
		t.Fatalf("PendingCount() after binds = %d, want 3", got)
	}

	if _, ok := tracker.Ack(Recvack{UID: "u1", SessionID: 10, MessageID: 1002}); !ok {
		t.Fatalf("Ack() ok = false, want true")
	}
	if got := tracker.PendingCount(); got != 2 {
		t.Fatalf("PendingCount() after ack = %d, want 2", got)
	}

	removed := tracker.SessionClosed("u2", 20)
	if len(removed) != 1 {
		t.Fatalf("SessionClosed() removed %d, want 1", len(removed))
	}
	if got := tracker.PendingCount(); got != 1 {
		t.Fatalf("PendingCount() after close = %d, want 1", got)
	}

	expired := tracker.Expire(50 * time.Second)
	if len(expired) != 1 || expired[0].MessageID != 1001 {
		t.Fatalf("Expire() = %#v, want message 1001", expired)
	}
	if got := tracker.PendingCount(); got != 0 {
		t.Fatalf("PendingCount() after expire = %d, want 0", got)
	}
}
```

- [ ] **Step 2: Run the focused tests and verify the expected failure**

Run:

```bash
GOWORK=off go test ./internalv2/runtime/delivery -run 'TestAckTrackerBindResultReportsAddedAndRejected|TestAckTrackerPendingCountStaysConsistentAcrossMutations' -count=1
```

Expected: FAIL because `BindResult` is not defined.

- [ ] **Step 3: Implement AckTracker count support**

In `internalv2/runtime/delivery/ack_tracker.go`, add `sync/atomic` to imports:

```go
import (
	"sync"
	"sync/atomic"
	"time"
)
```

Add a derived count field to `AckTracker`:

```go
type AckTracker struct {
	now                  func() int64
	maxPendingPerSession int
	shards               []ackTrackerShard
	pendingCount         atomic.Int64
}
```

Add this result type near `AckTrackerOptions`:

```go
// AckBindResult describes the outcome of binding one pending recvack.
type AckBindResult struct {
	// Bound reports whether the pending ack was stored.
	Bound bool
	// Added reports whether this bind created a new pending ack entry.
	Added bool
	// PendingCount is the owner-local pending ack count after the mutation.
	PendingCount int
}
```

Replace `Bind` with a wrapper and add `BindResult`:

```go
// Bind records a delivered message that is waiting for a recipient recvack.
func (t *AckTracker) Bind(pending PendingRecvAck) bool {
	return t.BindResult(pending).Bound
}

// BindResult records a delivered message and reports whether it changed the pending set.
func (t *AckTracker) BindResult(pending PendingRecvAck) AckBindResult {
	if t == nil || pending.UID == "" || pending.SessionID == 0 || pending.MessageID == 0 {
		if t == nil {
			return AckBindResult{}
		}
		return AckBindResult{PendingCount: t.PendingCount()}
	}
	if pending.DeliveredAt == 0 {
		pending.DeliveredAt = t.now()
	}
	shard := t.shard(pending.SessionID)
	shard.mu.Lock()
	defer shard.mu.Unlock()

	messageKey := ackMessageKey{uid: pending.UID, sessionID: pending.SessionID, messageID: pending.MessageID}
	sessionKey := ackSessionKey{uid: pending.UID, sessionID: pending.SessionID}
	messages := shard.bySession[sessionKey]
	_, existed := shard.byMessage[messageKey]
	if t.maxPendingPerSession > 0 && !existed && len(messages) >= t.maxPendingPerSession {
		return AckBindResult{PendingCount: int(t.pendingCount.Load())}
	}
	if messages == nil {
		messages = make(map[uint64]struct{})
		shard.bySession[sessionKey] = messages
	}
	shard.byMessage[messageKey] = pending
	messages[pending.MessageID] = struct{}{}
	if !existed {
		return AckBindResult{Bound: true, Added: true, PendingCount: int(t.pendingCount.Add(1))}
	}
	return AckBindResult{Bound: true, PendingCount: int(t.pendingCount.Load())}
}
```

Update `Ack` to decrement only on a real removal:

```go
delete(shard.byMessage, messageKey)
t.deleteSessionMessageLocked(shard, ackSessionKey{uid: ack.UID, sessionID: ack.SessionID}, ack.MessageID)
t.pendingCount.Add(-1)
return pending, true
```

Update `SessionClosed` after deleting the session:

```go
delete(shard.bySession, sessionKey)
if len(removed) > 0 {
	t.pendingCount.Add(-int64(len(removed)))
}
return removed
```

Update `Expire` to decrement by removed rows per shard:

```go
for i := range t.shards {
	shard := &t.shards[i]
	shard.mu.Lock()
	removedInShard := 0
	for messageKey, pending := range shard.byMessage {
		if pending.DeliveredAt > cutoff {
			continue
		}
		removed = append(removed, pending)
		delete(shard.byMessage, messageKey)
		t.deleteSessionMessageLocked(shard, ackSessionKey{uid: messageKey.uid, sessionID: messageKey.sessionID}, messageKey.messageID)
		removedInShard++
	}
	if removedInShard > 0 {
		t.pendingCount.Add(-int64(removedInShard))
	}
	shard.mu.Unlock()
}
```

Replace `PendingCount` with an O(1) read:

```go
// PendingCount returns the total number of pending recvacks across all shards.
func (t *AckTracker) PendingCount() int {
	if t == nil {
		return 0
	}
	return int(t.pendingCount.Load())
}
```

- [ ] **Step 4: Run AckTracker tests**

Run:

```bash
GOWORK=off go test ./internalv2/runtime/delivery -run 'TestAckTracker' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit Task 1**

```bash
git add internalv2/runtime/delivery/ack_tracker.go internalv2/runtime/delivery/ack_tracker_test.go
git commit -m "feat: track delivery pending ack count"
```

## Task 2: Runtime AckObserver Events

**Files:**
- Modify: `internalv2/runtime/delivery/observability.go`
- Modify: `internalv2/runtime/delivery/manager.go`
- Create: `internalv2/runtime/delivery/manager_ack_observer_test.go`

- [ ] **Step 1: Write failing Manager ack observer tests**

Create `internalv2/runtime/delivery/manager_ack_observer_test.go`:

```go
package delivery

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestManagerAckObserverReportsPendingTransitions(t *testing.T) {
	observer := &recordingAckObserver{}
	manager := NewManager(ManagerOptions{
		Acks: newAckTrackerWithClock(100),
		AckObserver: observer,
	})

	if ok := manager.BindPendingAck(PendingRecvAck{UID: "u1", SessionID: 10, MessageID: 1001}); !ok {
		t.Fatalf("BindPendingAck() ok = false, want true")
	}
	if err := manager.Recvack(context.Background(), Recvack{UID: "u1", SessionID: 10, MessageID: 1001}); err != nil {
		t.Fatalf("Recvack() error = %v", err)
	}
	if err := manager.Recvack(context.Background(), Recvack{UID: "u1", SessionID: 10, MessageID: 404}); err != nil {
		t.Fatalf("Recvack(miss) error = %v", err)
	}

	want := []AckEvent{
		{Action: DeliveryAckActionBind, Result: DeliveryAckResultOK, Changed: 1, PendingCount: 1},
		{Action: DeliveryAckActionAck, Result: DeliveryAckResultOK, Changed: 1, PendingCount: 0},
		{Action: DeliveryAckActionAck, Result: DeliveryAckResultMiss, Changed: 0, PendingCount: 0},
	}
	if !reflect.DeepEqual(observer.Events(), want) {
		t.Fatalf("ack events = %#v, want %#v", observer.Events(), want)
	}
}

func TestManagerAckObserverReportsCloseExpireAndRejectedBind(t *testing.T) {
	now := int64(200)
	observer := &recordingAckObserver{}
	manager := NewManager(ManagerOptions{
		Acks: NewAckTracker(AckTrackerOptions{
			ShardCount:           4,
			MaxPendingPerSession: 1,
			Now: func() int64 {
				return now
			},
		}),
		AckObserver: observer,
	})

	manager.BindPendingAck(PendingRecvAck{UID: "u1", SessionID: 10, MessageID: 1001, DeliveredAt: 100})
	manager.BindPendingAck(PendingRecvAck{UID: "u1", SessionID: 10, MessageID: 1002, DeliveredAt: 100})
	manager.BindPendingAck(PendingRecvAck{UID: "u2", SessionID: 20, MessageID: 2001, DeliveredAt: 100})
	if err := manager.SessionClosed(context.Background(), SessionClosed{UID: "u2", SessionID: 20}); err != nil {
		t.Fatalf("SessionClosed() error = %v", err)
	}
	manager.ExpirePendingAcks(50 * time.Second)

	want := []AckEvent{
		{Action: DeliveryAckActionBind, Result: DeliveryAckResultOK, Changed: 1, PendingCount: 1},
		{Action: DeliveryAckActionBind, Result: DeliveryAckResultRejected, Changed: 0, PendingCount: 1},
		{Action: DeliveryAckActionBind, Result: DeliveryAckResultOK, Changed: 1, PendingCount: 2},
		{Action: DeliveryAckActionSessionClosed, Result: DeliveryAckResultOK, Changed: 1, PendingCount: 1},
		{Action: DeliveryAckActionExpire, Result: DeliveryAckResultOK, Changed: 1, PendingCount: 0},
	}
	if !reflect.DeepEqual(observer.Events(), want) {
		t.Fatalf("ack events = %#v, want %#v", observer.Events(), want)
	}
}

func newAckTrackerWithClock(now int64) *AckTracker {
	return NewAckTracker(AckTrackerOptions{
		ShardCount: 4,
		Now: func() int64 {
			return now
		},
	})
}

type recordingAckObserver struct {
	events []AckEvent
}

func (o *recordingAckObserver) ObserveAck(event AckEvent) {
	o.events = append(o.events, event)
}

func (o *recordingAckObserver) Events() []AckEvent {
	return append([]AckEvent(nil), o.events...)
}
```

- [ ] **Step 2: Run the focused tests and verify the expected failure**

Run:

```bash
GOWORK=off go test ./internalv2/runtime/delivery -run 'TestManagerAckObserver' -count=1
```

Expected: FAIL because `AckEvent`, ack constants, and `ManagerOptions.AckObserver` are not defined.

- [ ] **Step 3: Add ack observer contract**

In `internalv2/runtime/delivery/observability.go`, add these constants near the existing delivery result constants:

```go
const (
	// DeliveryAckActionBind reports a pending ack bind attempt.
	DeliveryAckActionBind = "bind"
	// DeliveryAckActionAck reports a client recvack mutation attempt.
	DeliveryAckActionAck = "ack"
	// DeliveryAckActionSessionClosed reports cleanup for a closed owner-local session.
	DeliveryAckActionSessionClosed = "session_closed"
	// DeliveryAckActionExpire reports TTL cleanup for stale pending acks.
	DeliveryAckActionExpire = "expire"

	// DeliveryAckResultOK reports that the ack mutation changed state successfully.
	DeliveryAckResultOK = "ok"
	// DeliveryAckResultMiss reports that an ack mutation found no matching pending entry.
	DeliveryAckResultMiss = "miss"
	// DeliveryAckResultRejected reports that a pending ack bind was rejected by limits or invalid input.
	DeliveryAckResultRejected = "rejected"
	// DeliveryAckResultNoop reports that a cleanup mutation had no matching state to remove.
	DeliveryAckResultNoop = "noop"
)
```

Add the interface and event struct after `ManagerObserver`:

```go
// AckObserver receives owner-local pending recvack state changes.
type AckObserver interface {
	ObserveAck(AckEvent)
}

// AckEvent describes one owner-local pending recvack mutation.
type AckEvent struct {
	// Action is bind, ack, session_closed, or expire.
	Action string
	// Result is ok, miss, rejected, or noop.
	Result string
	// Changed is the number of pending ack entries added or removed.
	Changed int
	// PendingCount is the owner-local pending ack count after the mutation.
	PendingCount int
}
```

- [ ] **Step 4: Wire and emit ack events from Manager**

In `internalv2/runtime/delivery/manager.go`, extend `ManagerOptions`:

```go
// AckObserver receives owner-local pending recvack state changes.
AckObserver AckObserver
```

Extend `Manager`:

```go
ackObserver AckObserver
```

Set the field in `NewManager`:

```go
manager := &Manager{
	planner:     opts.Planner,
	runner:      runner,
	acks:        acks,
	ackObserver: opts.AckObserver,
}
```

Replace the ack-related methods with event-emitting versions:

```go
// Recvack clears a pending recipient recvack and ignores unknown acks.
func (m *Manager) Recvack(_ context.Context, cmd Recvack) error {
	if m == nil || m.acks == nil {
		return nil
	}
	_, ok := m.acks.Ack(cmd)
	result := DeliveryAckResultMiss
	changed := 0
	if ok {
		result = DeliveryAckResultOK
		changed = 1
	}
	m.observeAck(DeliveryAckActionAck, result, changed, m.acks.PendingCount())
	return nil
}

// SessionClosed clears pending recvacks for a closed recipient-owner session.
func (m *Manager) SessionClosed(_ context.Context, cmd SessionClosed) error {
	if m == nil || m.acks == nil {
		return nil
	}
	removed := m.acks.SessionClosed(cmd.UID, cmd.SessionID)
	result := DeliveryAckResultNoop
	if len(removed) > 0 {
		result = DeliveryAckResultOK
	}
	m.observeAck(DeliveryAckActionSessionClosed, result, len(removed), m.acks.PendingCount())
	return nil
}

// BindPendingAck records one delivery waiting for a client recvack.
func (m *Manager) BindPendingAck(pending PendingRecvAck) bool {
	if m == nil || m.acks == nil {
		return false
	}
	result := m.acks.BindResult(pending)
	eventResult := DeliveryAckResultRejected
	changed := 0
	if result.Bound {
		eventResult = DeliveryAckResultOK
		if result.Added {
			changed = 1
		}
	}
	m.observeAck(DeliveryAckActionBind, eventResult, changed, result.PendingCount)
	return result.Bound
}

// ExpirePendingAcks removes pending recvacks older than ttl.
func (m *Manager) ExpirePendingAcks(ttl time.Duration) []PendingRecvAck {
	if m == nil || m.acks == nil {
		return nil
	}
	removed := m.acks.Expire(ttl)
	result := DeliveryAckResultNoop
	if len(removed) > 0 {
		result = DeliveryAckResultOK
	}
	m.observeAck(DeliveryAckActionExpire, result, len(removed), m.acks.PendingCount())
	return removed
}

func (m *Manager) observeAck(action, result string, changed, pendingCount int) {
	if m == nil || m.ackObserver == nil {
		return
	}
	m.ackObserver.ObserveAck(AckEvent{
		Action:       action,
		Result:       result,
		Changed:      changed,
		PendingCount: pendingCount,
	})
}
```

- [ ] **Step 5: Run runtime delivery tests**

Run:

```bash
GOWORK=off go test ./internalv2/runtime/delivery -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit Task 2**

```bash
git add internalv2/runtime/delivery/observability.go internalv2/runtime/delivery/manager.go internalv2/runtime/delivery/manager_ack_observer_test.go
git commit -m "feat: emit delivery ack state events"
```

## Task 3: App Top and Metrics Ack Observers

**Files:**
- Modify: `internalv2/app/observability.go`
- Modify: `internalv2/app/top_observer.go`
- Modify: `internalv2/app/wiring.go`
- Modify: `internalv2/app/delivery.go`
- Modify: `internalv2/app/top_collector_test.go`
- Modify: `internalv2/app/observability_test.go`

- [ ] **Step 1: Write failing top observer test**

Append to `internalv2/app/top_collector_test.go`:

```go
func TestTopDeliveryObserverMapsAckEventToAckBindings(t *testing.T) {
	collector := newTopCollector(topCollectorOptions{
		ClusterSnapshot: func() clusterv2.Snapshot {
			return clusterv2.Snapshot{RoutesReady: true, SlotsReady: true, ChannelsReady: true}
		},
	})
	observer := topDeliveryObserver{top: collector}

	collector.recordSampleAt(time.Unix(100, 0))
	observer.ObserveAck(runtimedelivery.AckEvent{PendingCount: 7})
	collector.recordSampleAt(time.Unix(110, 0))

	snapshot, err := collector.SnapshotTop(context.Background(), accessapi.TopSnapshotQuery{
		Window: 10 * time.Second,
		View:   accessapi.TopViewDelivery,
	})
	if err != nil {
		t.Fatalf("SnapshotTop() error = %v", err)
	}
	if snapshot.Delivery == nil || snapshot.Delivery.AckBindings != 7 {
		t.Fatalf("delivery snapshot = %#v, want ack_bindings 7", snapshot.Delivery)
	}
}
```

- [ ] **Step 2: Write failing metrics and fanout tests**

Append to `internalv2/app/observability_test.go`:

```go
func TestDeliveryMetricsObserverMapsAckEventToGauge(t *testing.T) {
	reg := obsmetrics.New(1, "n1")
	observer := deliveryMetricsObserver{metrics: reg}

	observer.ObserveAck(runtimedelivery.AckEvent{PendingCount: 6})

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	ackBindings := requireAppMetricFamily(t, families, "wukongim_delivery_ack_bindings")
	if got := findAppMetricByLabels(t, ackBindings, nil).GetGauge().GetValue(); got != 6 {
		t.Fatalf("delivery ack bindings = %v, want 6", got)
	}
}

func TestCombinedDeliveryObserverFansOutAckEvents(t *testing.T) {
	reg := obsmetrics.New(1, "n1")
	collector := newTopCollector(topCollectorOptions{
		ClusterSnapshot: func() clusterv2.Snapshot {
			return clusterv2.Snapshot{RoutesReady: true, SlotsReady: true, ChannelsReady: true}
		},
	})
	observer := combineDeliveryObservers(
		deliveryMetricsObserver{metrics: reg},
		topDeliveryObserver{top: collector},
	)
	ackObserver, ok := observer.(runtimedelivery.AckObserver)
	if !ok {
		t.Fatalf("combined observer does not implement AckObserver")
	}

	collector.recordSampleAt(time.Unix(100, 0))
	ackObserver.ObserveAck(runtimedelivery.AckEvent{PendingCount: 9})
	collector.recordSampleAt(time.Unix(110, 0))

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	ackBindings := requireAppMetricFamily(t, families, "wukongim_delivery_ack_bindings")
	if got := findAppMetricByLabels(t, ackBindings, nil).GetGauge().GetValue(); got != 9 {
		t.Fatalf("metrics ack bindings = %v, want 9", got)
	}
	snapshot, err := collector.SnapshotTop(context.Background(), accessapi.TopSnapshotQuery{
		Window: 10 * time.Second,
		View:   accessapi.TopViewDelivery,
	})
	if err != nil {
		t.Fatalf("SnapshotTop() error = %v", err)
	}
	if snapshot.Delivery == nil || snapshot.Delivery.AckBindings != 9 {
		t.Fatalf("top ack bindings = %#v, want 9", snapshot.Delivery)
	}
}
```

- [ ] **Step 3: Run app tests and verify expected failure**

Run:

```bash
GOWORK=off go test ./internalv2/app -run 'TestTopDeliveryObserverMapsAckEventToAckBindings|TestDeliveryMetricsObserverMapsAckEventToGauge|TestCombinedDeliveryObserverFansOutAckEvents' -count=1
```

Expected: FAIL because app observers do not implement `ObserveAck`.

- [ ] **Step 4: Implement top and metrics ack observers**

In `internalv2/app/top_observer.go`, add:

```go
func (o topDeliveryObserver) ObserveAck(event runtimedelivery.AckEvent) {
	if o.top == nil {
		return
	}
	o.top.SetDeliveryAckBindings(int64(event.PendingCount))
}
```

Add this compile assertion near the delivery observer assertions:

```go
var _ runtimedelivery.AckObserver = topDeliveryObserver{}
```

In `internalv2/app/observability.go`, add:

```go
func (o deliveryMetricsObserver) ObserveAck(event runtimedelivery.AckEvent) {
	if o.metrics == nil {
		return
	}
	o.metrics.Delivery.SetAckBindings(event.PendingCount)
}
```

Add `multiDeliveryObserver` fanout:

```go
func (o multiDeliveryObserver) ObserveAck(event runtimedelivery.AckEvent) {
	for _, observer := range o {
		ackObserver, ok := observer.(runtimedelivery.AckObserver)
		if ok {
			ackObserver.ObserveAck(event)
		}
	}
}
```

Add compile assertions:

```go
var _ runtimedelivery.AckObserver = deliveryMetricsObserver{}
var _ runtimedelivery.AckObserver = multiDeliveryObserver{}
```

- [ ] **Step 5: Wire AckObserver into Manager**

In `internalv2/app/wiring.go`, inside `wireDelivery`, remove the local `top`
variable used only by `localOwnerPusher` and construct the pusher without it:

```go
localPusher := &localOwnerPusher{online: a.online, pendingAckTTL: a.cfg.Delivery.PendingAckTTL, logger: a.logger.Named("delivery.owner")}
```

After `managerObserver` selection, add:

```go
var ackObserver runtimedelivery.AckObserver
if observer, ok := deliveryObserver.(runtimedelivery.AckObserver); ok {
	ackObserver = observer
}
```

Pass it to `NewManager`:

```go
AckObserver: ackObserver,
```

- [ ] **Step 6: Remove localOwnerPusher top refresh responsibility**

In `internalv2/app/delivery.go`, remove the `top *topCollector` field from
`localOwnerPusher`.

Remove calls to:

```go
p.observePendingAcks()
```

Remove the method:

```go
func (p localOwnerPusher) observePendingAcks() {
	if p.top != nil && p.delivery != nil {
		p.top.SetDeliveryAckBindings(int64(p.delivery.PendingAckCount()))
	}
}
```

Keep calls to `p.delivery.BindPendingAck`, `p.delivery.Recvack`, and
`p.delivery.ExpirePendingAcks`; those calls now emit ack events through
`Manager`.

- [ ] **Step 7: Run app tests**

Run:

```bash
GOWORK=off go test ./internalv2/app -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit Task 3**

```bash
git add internalv2/app/observability.go internalv2/app/top_observer.go internalv2/app/wiring.go internalv2/app/delivery.go internalv2/app/top_collector_test.go internalv2/app/observability_test.go
git commit -m "feat: expose delivery ack bindings through observers"
```

## Task 4: Black-Box Top Helper and Cross-Node RECVACK E2E

**Files:**
- Create: `test/e2ev2/suite/top.go`
- Modify: `test/e2ev2/suite/api_test.go`
- Modify: `test/e2ev2/message/cross_node_delivery/cross_node_delivery_test.go`
- Modify: `test/e2ev2/message/cross_node_delivery/AGENTS.md`

- [ ] **Step 1: Write failing top helper API test**

Append to `test/e2ev2/suite/api_test.go`:

```go
func TestFetchTopDeliveryDecodesPublicResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/top/v1/snapshot", r.URL.Path)
		require.Equal(t, "delivery", r.URL.Query().Get("view"))

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"delivery":{"ack_bindings":3,"retry_queue_depth":2}}`))
	}))
	defer server.Close()

	delivery, err := FetchTopDelivery(context.Background(), strings.TrimPrefix(server.URL, "http://"))
	require.NoError(t, err)
	require.Equal(t, int64(3), delivery.AckBindings)
	require.Equal(t, int64(2), delivery.RetryQueueDepth)
}
```

- [ ] **Step 2: Run suite tests and verify expected failure**

Run:

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/suite -run TestFetchTopDeliveryDecodesPublicResponse -count=1
```

Expected: FAIL because `FetchTopDelivery` is not defined.

- [ ] **Step 3: Implement top helper**

Create `test/e2ev2/suite/top.go`:

```go
//go:build e2e

package suite

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// TopDeliverySnapshot is the public delivery section returned by /top/v1/snapshot.
type TopDeliverySnapshot struct {
	// AckBindings is the current number of owner-local pending recvack bindings.
	AckBindings int64 `json:"ack_bindings"`
	// RetryQueueDepth is the current delivery retry queue depth.
	RetryQueueDepth int64 `json:"retry_queue_depth"`
}

type topSnapshotResponse struct {
	Delivery *TopDeliverySnapshot `json:"delivery"`
}

// FetchTopDelivery fetches the public delivery top snapshot from one node.
func FetchTopDelivery(ctx context.Context, apiAddr string) (TopDeliverySnapshot, error) {
	var out topSnapshotResponse
	_, err := GetJSON(ctx, "http://"+apiAddr+"/top/v1/snapshot?view=delivery&window=1s", &out)
	if err != nil {
		return TopDeliverySnapshot{}, err
	}
	if out.Delivery == nil {
		return TopDeliverySnapshot{}, fmt.Errorf("delivery top section missing")
	}
	return *out.Delivery, nil
}

// RequireTopDeliveryAckBindingsAtLeastEventually waits until ack_bindings reaches at least want.
func RequireTopDeliveryAckBindingsAtLeastEventually(t *testing.T, node StartedNode, want int64) {
	t.Helper()
	requireTopDeliveryAckBindingsEventually(t, node, want, func(got int64) bool { return got >= want }, ">=")
}

// RequireTopDeliveryAckBindingsEventually waits until ack_bindings equals want.
func RequireTopDeliveryAckBindingsEventually(t *testing.T, node StartedNode, want int64) {
	t.Helper()
	requireTopDeliveryAckBindingsEventually(t, node, want, func(got int64) bool { return got == want }, "==")
}

func requireTopDeliveryAckBindingsEventually(t *testing.T, node StartedNode, want int64, match func(int64) bool, op string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var last TopDeliverySnapshot
	var lastErr error
	for {
		last, lastErr = FetchTopDelivery(ctx, node.APIAddr())
		if lastErr == nil && match(last.AckBindings) {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("top delivery ack_bindings = %d err=%v, want %s %d\n%s", last.AckBindings, lastErr, op, want, node.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}
```

- [ ] **Step 4: Run suite helper tests**

Run:

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/suite -run 'TestFetchTopDeliveryDecodesPublicResponse|TestGetJSONDecodesPublicResponse' -count=1
```

Expected: PASS.

- [ ] **Step 5: Extend cross-node delivery e2e**

In `test/e2ev2/message/cross_node_delivery/cross_node_delivery_test.go`, replace
the cluster start with:

```go
overrides := deliveryTopOverrides()
cluster := s.StartThreeNodeCluster(
	suite.WithNodeConfigOverrides(1, overrides),
	suite.WithNodeConfigOverrides(2, overrides),
	suite.WithNodeConfigOverrides(3, overrides),
)
```

Add this helper:

```go
func deliveryTopOverrides() map[string]string {
	return map[string]string{
		"WK_DELIVERY_ENABLE":       "true",
		"WK_TOP_API_ENABLE":        "true",
		"WK_TOP_COLLECT_INTERVAL":  "100ms",
		"WK_TOP_HISTORY_WINDOW":    "2s",
	}
}
```

Change the two send calls:

```go
sendAndRequireRecv(t, cluster, cluster.MustNode(2), userA, userB, "e2ev2-cross-a", "e2ev2-cross-b", 1, "e2ev2-cross-a-to-b-1", []byte("hello b from a"))
sendAndRequireRecv(t, cluster, cluster.MustNode(1), userB, userA, "e2ev2-cross-b", "e2ev2-cross-a", 1, "e2ev2-cross-b-to-a-1", []byte("hello a from b"))
```

Change the helper signature:

```go
func sendAndRequireRecv(
	t *testing.T,
	cluster *suite.StartedCluster,
	recipientOwner *suite.StartedNode,
	sender, recipient *suite.WKProtoClient,
	senderUID, recipientUID string,
	clientSeq uint64,
	clientMsgNo string,
	payload []byte,
) {
```

Before sending `RecvAck`, assert pending ack was observed:

```go
suite.RequireTopDeliveryAckBindingsAtLeastEventually(t, *recipientOwner, 1)
```

After `RecvAck`, assert it returns to zero:

```go
require.NoError(t, recipient.RecvAck(recv.MessageID, recv.MessageSeq), cluster.DumpDiagnostics())
suite.RequireTopDeliveryAckBindingsEventually(t, *recipientOwner, 0)
```

- [ ] **Step 6: Update cross-node scenario documentation**

In `test/e2ev2/message/cross_node_delivery/AGENTS.md`, add this rule:

```markdown
- After each `RECV`, assert the recipient owner node reports a pending
  `ack_bindings` value through `/top/v1/snapshot?view=delivery`, then send
  `RecvAck` and assert the same owner node returns to `ack_bindings=0`.
```

- [ ] **Step 7: Run cross-node e2e**

Run:

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/message/cross_node_delivery -count=1 -timeout 2m
```

Expected: PASS.

- [ ] **Step 8: Commit Task 4**

```bash
git add test/e2ev2/suite/top.go test/e2ev2/suite/api_test.go test/e2ev2/message/cross_node_delivery/cross_node_delivery_test.go test/e2ev2/message/cross_node_delivery/AGENTS.md
git commit -m "test: assert delivery recvack clears ack bindings"
```

## Task 5: Flow Docs and Final Verification

**Files:**
- Modify: `internalv2/runtime/delivery/FLOW.md`

- [ ] **Step 1: Update runtime delivery flow docs**

In `internalv2/runtime/delivery/FLOW.md`, add this paragraph after the existing
observer paragraph:

```markdown
Ack state changes are observed from `Manager`, not from gateway or app
adapters. `BindPendingAck`, `Recvack`, `SessionClosed`, and
`ExpirePendingAcks` emit bounded ack events with action, result, changed count,
and the owner-local pending count. Top and Prometheus observers consume this
same event so `ack_bindings` reflects `AckTracker` state transitions.
```

Add this sentence to the `AckTracker` paragraph:

```markdown
`AckTracker` maintains an O(1) derived pending count so recvack hot paths do
not scan all shards for observability.
```

- [ ] **Step 2: Run gofmt**

Run:

```bash
gofmt -w internalv2/runtime/delivery/ack_tracker.go internalv2/runtime/delivery/ack_tracker_test.go internalv2/runtime/delivery/observability.go internalv2/runtime/delivery/manager.go internalv2/runtime/delivery/manager_ack_observer_test.go internalv2/app/observability.go internalv2/app/top_observer.go internalv2/app/wiring.go internalv2/app/delivery.go internalv2/app/top_collector_test.go internalv2/app/observability_test.go test/e2ev2/suite/top.go test/e2ev2/suite/api_test.go test/e2ev2/message/cross_node_delivery/cross_node_delivery_test.go
```

Expected: command exits 0.

- [ ] **Step 3: Run targeted unit tests**

Run:

```bash
GOWORK=off go test ./internalv2/runtime/delivery ./internalv2/app -count=1
```

Expected: PASS.

- [ ] **Step 4: Run e2e suite helper tests**

Run:

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/suite -count=1
```

Expected: PASS.

- [ ] **Step 5: Run delivery black-box e2e**

Run:

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/message/cross_node_delivery -count=1 -timeout 2m
```

Expected: PASS.

- [ ] **Step 6: Run broader regression**

Run:

```bash
GOWORK=off go test ./internal/... ./internalv2/... ./pkg/...
```

Expected: PASS.

- [ ] **Step 7: Inspect git diff**

Run:

```bash
git diff --stat
git diff -- internalv2/runtime/delivery internalv2/app test/e2ev2
```

Expected: only delivery ack observability, app observer wiring, e2e helper, and
FLOW/AGENTS docs changed.

- [ ] **Step 8: Commit Task 5**

```bash
git add internalv2/runtime/delivery/FLOW.md
git commit -m "docs: update delivery ack observability flow"
```

## Self-Review Checklist

- Spec coverage:
  - Runtime-owned ack events: Task 2.
  - O(1) pending count: Task 1.
  - Top and Prometheus from the same event: Task 3.
  - Black-box `RECV -> RECVACK -> ack_bindings=0`: Task 4.
  - Retry behavior preserved: Task 3 and Task 5 targeted app/runtime tests.
  - FLOW and scenario docs updated: Task 4 and Task 5.
- Placeholder scan:
  - No task contains open-ended implementation gaps.
  - Each code-changing step includes the concrete code shape to add or replace.
- Type consistency:
  - `AckEvent`, `AckObserver`, `DeliveryAckAction*`, and `DeliveryAckResult*`
    are defined before app observers consume them.
  - `AckTracker.Bind` remains compatible; `BindResult` is additive.
  - `multiDeliveryObserver` remains the app-level fanout type for optional
    observer capabilities.
