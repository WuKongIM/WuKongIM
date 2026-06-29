# InternalV2 Dynamic Node Stage 9A Health Report Model Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a durable, low-frequency ControllerV2 node health report model and surface it through clusterv2 control snapshots without changing placement decisions yet.

**Architecture:** Store one compact `NodeHealthReport` per node in `cluster-state.json`, with leader-side report timestamps and bounded payload fields. Health-only writes update the state file and applied Raft index but do not advance the logical placement/lifecycle `Revision`, so heartbeat churn cannot break existing compare-and-set operations.

**Tech Stack:** Go, ControllerV2 state/FSM/raft runtime, clusterv2 control adapter and transport codec, clusterv2 observe reporter, `cmd/wukongimv2` config.

---

## Source Links

- Stage 9 spec: `docs/superpowers/specs/2026-06-29-internalv2-dynamic-node-stage9-production-readiness-design.md`
- Stage 9 master plan: `docs/superpowers/plans/2026-06-29-internalv2-dynamic-node-stage9-production-readiness.md`
- Required package docs before editing:
  - `pkg/controllerv2/FLOW.md`
  - `pkg/clusterv2/FLOW.md`
  - `internalv2/app/FLOW.md`

## File Map

- Modify: `pkg/controllerv2/state/types.go`
  - Add `NodeHealthReport` and `ClusterState.NodeHealthReports`.
- Modify: `pkg/controllerv2/state/normalize.go`
  - Clone, sort, and normalize health reports by `node_id`.
- Modify: `pkg/controllerv2/state/validate.go`
  - Validate report node IDs, status values, and bounded error codes.
- Modify: `pkg/controllerv2/state/codec.go`
  - Include health reports in the checksum view.
- Modify: `pkg/controllerv2/state/state_test.go`
  - Add clone, normalize, validate, checksum, and JSON round-trip tests.
- Modify: `pkg/controllerv2/command/command.go`
  - Add `KindReportNodeHealth` and command payload field.
- Modify: `pkg/controllerv2/fsm/fsm.go`
  - Add an `Updated` apply result bit for durable updates that do not advance logical revision.
- Modify: `pkg/controllerv2/fsm/mutations.go`
  - Route `KindReportNodeHealth`.
- Modify: `pkg/controllerv2/fsm/mutation_handlers.go`
  - Apply health reports without incrementing `ClusterState.Revision`.
- Modify: `pkg/controllerv2/fsm/fsm_test.go`
  - Prove health reports persist, advance applied index, and do not advance logical revision.
- Modify: `pkg/controllerv2/raft/service.go`
  - Carry `Updated` through `raft.ProposalResult`.
- Modify: `pkg/controllerv2/raft/apply_scheduler.go`
  - Copy `ApplyResult.Updated` into proposal results.
- Create: `pkg/controllerv2/runtime_node_health.go`
  - Add `ReportNodeHealth`.
- Modify: `pkg/controllerv2/types.go`
  - Add request/result aliases for node health reporting if needed by callers.
- Modify: `pkg/clusterv2/control/controller.go`
  - Extend `NodeReport` and snapshot health types.
- Modify: `pkg/clusterv2/control/snapshot.go`
  - Add `NodeHealth` fields to `control.Node` and helpers for freshness.
- Modify: `pkg/clusterv2/control/controllerv2.go`
  - Map ControllerV2 health reports into control snapshots.
- Modify: `pkg/clusterv2/control/codec.go`
  - Add `report_node_health` control write action.
- Modify: `pkg/clusterv2/control/control_write.go`
  - Dispatch forwarded health reports to the leader-side runtime.
- Modify: `pkg/clusterv2/control/runtime.go`
  - Implement local and forwarded `ReportNode`.
- Modify: `pkg/clusterv2/control/*_test.go`
  - Add codec, runtime, adapter, and transport tests.
- Modify: `pkg/clusterv2/observe/reporter.go`
  - Add runtime readiness, observed control revision, observed slot revision, and report sequence fields.
- Modify: `pkg/clusterv2/observe/observe_test.go`
  - Prove reporter sends the new bounded fields.
- Modify: `pkg/clusterv2/config.go`
  - Add `HealthReportConfig` defaults and validation.
- Modify: `pkg/clusterv2/node.go`
  - Add report loop fields.
- Modify: `pkg/clusterv2/node_lifecycle.go`
  - Start and stop the health report loop.
- Modify: `pkg/clusterv2/node_loops.go`
  - Add the low-frequency report loop.
- Modify: `pkg/clusterv2/config_test.go`
  - Test defaults and invalid health report intervals.
- Modify: `cmd/wukongimv2/config.go`
  - Parse `WK_CLUSTER_NODE_HEALTH_REPORT_INTERVAL` and `WK_CLUSTER_NODE_HEALTH_REPORT_TTL`.
- Modify: `cmd/wukongimv2/config_test.go`
  - Test config file/env parsing.
- Modify: `wukongim.conf.example`
  - Document the new health report keys.
- Modify: `cmd/wukongimv2/wukongimv2.conf.example`
  - Document the new health report keys for the standalone v2 entry.
- Modify: `pkg/controllerv2/FLOW.md`
  - Explain health-only writes and revision semantics.
- Modify: `pkg/clusterv2/FLOW.md`
  - Explain the report loop and snapshot health fields.

## Revision Semantics

Health reports must not use `validateChanged`, because that increments `ClusterState.Revision`. They should:

- update `ClusterState.NodeHealthReports`;
- update `ClusterState.AppliedRaftIndex`;
- update the checksum;
- return `ApplyResult{Updated: true}`;
- keep `ClusterState.Revision` unchanged;
- publish by checksum changes through `publishIfChanged`.

This keeps existing lifecycle/task CAS fences stable while still making health reports durable and visible.

---

### Task 1: Add State Model And Persistence Tests

**Files:**
- Modify: `pkg/controllerv2/state/types.go`
- Modify: `pkg/controllerv2/state/normalize.go`
- Modify: `pkg/controllerv2/state/validate.go`
- Modify: `pkg/controllerv2/state/codec.go`
- Modify: `pkg/controllerv2/state/state_test.go`

- [ ] **Step 1: Write failing state tests**

Append tests like these to `pkg/controllerv2/state/state_test.go`:

```go
func TestClusterStateNodeHealthReportsCloneNormalizeValidate(t *testing.T) {
	st := testClusterState()
	st.NodeHealthReports = []NodeHealthReport{
		{NodeID: 2, Status: NodeStatusAlive, RuntimeReady: true, ObservedControlRevision: 7, ObservedSlotRevision: 11, ReportSeq: 3, ReportedAtUnixMilli: 1710000002000},
		{NodeID: 1, Status: NodeStatusSuspect, RuntimeReady: false, ObservedControlRevision: 6, ReportSeq: 4, ReportedAtUnixMilli: 1710000001000, ErrorCode: "runtime_starting"},
	}

	clone := st.Clone()
	clone.NodeHealthReports[0].Status = NodeStatusDown
	if st.NodeHealthReports[0].Status != NodeStatusAlive {
		t.Fatalf("Clone() shared NodeHealthReports backing array")
	}

	st.Normalize()
	if got := st.NodeHealthReports[0].NodeID; got != 1 {
		t.Fatalf("Normalize() first health report node_id=%d, want 1", got)
	}
	if err := st.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestClusterStateRejectsInvalidNodeHealthReports(t *testing.T) {
	base := testClusterState()
	base.NodeHealthReports = []NodeHealthReport{{NodeID: 99, Status: NodeStatusAlive, ReportedAtUnixMilli: 1710000000000}}
	if err := base.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want invalid referenced node")
	}

	base = testClusterState()
	base.NodeHealthReports = []NodeHealthReport{{NodeID: 1, Status: NodeStatus("bad"), ReportedAtUnixMilli: 1710000000000}}
	if err := base.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want invalid status")
	}
}

func TestChecksumIncludesNodeHealthReports(t *testing.T) {
	left := testClusterState()
	right := left.Clone()
	left.NodeHealthReports = []NodeHealthReport{{NodeID: 1, Status: NodeStatusAlive, RuntimeReady: true, ReportSeq: 1, ReportedAtUnixMilli: 1710000000000}}
	right.NodeHealthReports = []NodeHealthReport{{NodeID: 1, Status: NodeStatusDown, RuntimeReady: false, ReportSeq: 2, ReportedAtUnixMilli: 1710000001000}}

	leftChecksum, err := Checksum(left)
	if err != nil {
		t.Fatalf("Checksum(left) error = %v", err)
	}
	rightChecksum, err := Checksum(right)
	if err != nil {
		t.Fatalf("Checksum(right) error = %v", err)
	}
	if leftChecksum == rightChecksum {
		t.Fatalf("Checksum() ignored NodeHealthReports: %s", leftChecksum)
	}
}
```

- [ ] **Step 2: Run the tests and verify they fail**

Run:

```bash
GOWORK=off go test ./pkg/controllerv2/state -run 'NodeHealth|ChecksumIncludesNodeHealth' -count=1
```

Expected: compile fails because `NodeHealthReport` and `NodeHealthReports` do not exist.

- [ ] **Step 3: Add the state model**

Add to `pkg/controllerv2/state/types.go`:

```go
// NodeHealthReport stores one bounded low-frequency runtime health report.
type NodeHealthReport struct {
	// NodeID is the reporting node identity.
	NodeID uint64 `json:"node_id"`
	// Status is the reported runtime health status.
	Status NodeStatus `json:"status"`
	// RuntimeReady reports whether the node can serve foreground cluster traffic.
	RuntimeReady bool `json:"runtime_ready"`
	// ObservedControlRevision is the latest logical ControllerV2 revision observed by the node.
	ObservedControlRevision uint64 `json:"observed_control_revision"`
	// ObservedSlotRevision is the latest local Slot runtime revision observed by the node.
	ObservedSlotRevision uint64 `json:"observed_slot_revision,omitempty"`
	// ReportSeq is a node-local sequence used for diagnostics.
	ReportSeq uint64 `json:"report_seq"`
	// ReportedAtUnixMilli is filled by the Controller leader when the report is proposed.
	ReportedAtUnixMilli int64 `json:"reported_at_unix_milli"`
	// AppliedRaftIndex is the Controller Raft index that stored this report.
	AppliedRaftIndex uint64 `json:"applied_raft_index,omitempty"`
	// ErrorCode is a bounded machine-readable runtime reason.
	ErrorCode string `json:"error_code,omitempty"`
}
```

Add to `ClusterState`:

```go
// NodeHealthReports stores one compact health report per node.
NodeHealthReports []NodeHealthReport `json:"node_health_reports,omitempty"`
```

- [ ] **Step 4: Add clone, normalize, validate, and checksum wiring**

Implement helpers matching existing slice patterns:

```go
func cloneNodeHealthReports(in []NodeHealthReport) []NodeHealthReport {
	if len(in) == 0 {
		return nil
	}
	out := make([]NodeHealthReport, len(in))
	copy(out, in)
	return out
}
```

In `Clone`, deep copy `NodeHealthReports`. In `Normalize`, sort by `NodeID`. In `checksumClusterState`, add:

```go
NodeHealthReports []NodeHealthReport `json:"node_health_reports,omitempty"`
```

In validation, require:

```go
func validNodeStatus(status NodeStatus) bool {
	return status == NodeStatusAlive || status == NodeStatusSuspect || status == NodeStatusDown
}
```

Then reject duplicate reports, `NodeID==0`, missing referenced nodes, invalid status, negative `ReportedAtUnixMilli`, and `len(ErrorCode) > 128`.

- [ ] **Step 5: Run state tests**

Run:

```bash
GOWORK=off go test ./pkg/controllerv2/state -run 'NodeHealth|ChecksumIncludesNodeHealth|ClusterState' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/controllerv2/state
git commit -m "feat: add controllerv2 node health state model"
```

---

### Task 2: Add ControllerV2 Health Report Command

**Files:**
- Modify: `pkg/controllerv2/command/command.go`
- Modify: `pkg/controllerv2/fsm/fsm.go`
- Modify: `pkg/controllerv2/fsm/mutations.go`
- Modify: `pkg/controllerv2/fsm/mutation_handlers.go`
- Modify: `pkg/controllerv2/fsm/fsm_test.go`
- Modify: `pkg/controllerv2/raft/service.go`
- Modify: `pkg/controllerv2/raft/apply_scheduler.go`

- [ ] **Step 1: Write failing FSM tests**

Append tests to `pkg/controllerv2/fsm/fsm_test.go`:

```go
func TestApplyReportNodeHealthPersistsWithoutLogicalRevisionBump(t *testing.T) {
	sm := newLoadedStateMachine(t)
	before := sm.Snapshot(context.Background())

	report := state.NodeHealthReport{
		NodeID:                  1,
		Status:                  state.NodeStatusAlive,
		RuntimeReady:            true,
		ObservedControlRevision: before.Revision,
		ObservedSlotRevision:    12,
		ReportSeq:               1,
		ReportedAtUnixMilli:     1710000000000,
	}
	result, err := sm.Apply(context.Background(), before.AppliedRaftIndex+1, command.Command{
		Kind:       command.KindReportNodeHealth,
		NodeHealth: &report,
	})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if !result.Updated || result.Changed || result.Revision != before.Revision {
		t.Fatalf("Apply() = %#v, want Updated without logical revision bump from %d", result, before.Revision)
	}
	after := sm.Snapshot(context.Background())
	if after.Revision != before.Revision {
		t.Fatalf("Revision = %d, want unchanged %d", after.Revision, before.Revision)
	}
	if len(after.NodeHealthReports) != 1 || after.NodeHealthReports[0].AppliedRaftIndex != result.AppliedRaftIndex {
		t.Fatalf("NodeHealthReports = %#v, result = %#v", after.NodeHealthReports, result)
	}
}

func TestApplyReportNodeHealthRejectsUnknownNode(t *testing.T) {
	sm := newLoadedStateMachine(t)
	report := state.NodeHealthReport{NodeID: 99, Status: state.NodeStatusAlive, ReportedAtUnixMilli: 1710000000000}
	result, err := sm.Apply(context.Background(), 2, command.Command{Kind: command.KindReportNodeHealth, NodeHealth: &report})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if !result.Rejected || result.Reason != ReasonInvalidState {
		t.Fatalf("Apply() = %#v, want invalid state reject", result)
	}
}
```

- [ ] **Step 2: Run the tests and verify they fail**

Run:

```bash
GOWORK=off go test ./pkg/controllerv2/fsm -run 'ReportNodeHealth' -count=1
```

Expected: compile fails because command kind, payload, and `ApplyResult.Updated` do not exist.

- [ ] **Step 3: Add command kind and payload**

In `pkg/controllerv2/command/command.go`, add:

```go
// KindReportNodeHealth stores a bounded low-frequency node health report.
KindReportNodeHealth Kind = "report_node_health"
```

Add to `Command`:

```go
// NodeHealth contains a health report for KindReportNodeHealth.
NodeHealth *state.NodeHealthReport `json:"node_health,omitempty"`
```

- [ ] **Step 4: Add durable-update result semantics**

Add `Updated bool` to `fsm.ApplyResult` and `raft.ProposalResult` with this comment:

```go
// Updated is true when the command changed durable state without advancing the logical cluster-state revision.
Updated bool
```

Copy it in `proposalResultFromApplyResult`.

- [ ] **Step 5: Implement health mutation**

Add the command branch in `applyMutation`. Add this handler:

```go
func (sm *StateMachine) applyReportNodeHealth(next *state.ClusterState, raftIndex uint64, cmd command.Command) ApplyResult {
	if next.Revision == 0 || cmd.NodeHealth == nil {
		return reject(ReasonInvalidCommand)
	}
	before := next.Clone()
	report := *cmd.NodeHealth
	report.AppliedRaftIndex = raftIndex
	upsertNodeHealthReport(next, report)
	next.Normalize()
	if err := next.Validate(); err != nil {
		*next = before
		return reject(ReasonInvalidState)
	}
	if reflect.DeepEqual(before.NodeHealthReports, next.NodeHealthReports) {
		return noop(ReasonNoChange)
	}
	return ApplyResult{Updated: true}
}
```

Add an `upsertNodeHealthReport` helper beside existing node/task helpers:

```go
func upsertNodeHealthReport(st *state.ClusterState, report state.NodeHealthReport) {
	for i := range st.NodeHealthReports {
		if st.NodeHealthReports[i].NodeID == report.NodeID {
			st.NodeHealthReports[i] = report
			return
		}
	}
	st.NodeHealthReports = append(st.NodeHealthReports, report)
}
```

- [ ] **Step 6: Run FSM tests**

Run:

```bash
GOWORK=off go test ./pkg/controllerv2/fsm -run 'ReportNodeHealth|ApplyBatch|ApplyUpsertNodeNoop' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add pkg/controllerv2/command pkg/controllerv2/fsm pkg/controllerv2/raft
git commit -m "feat: persist controllerv2 node health reports"
```

---

### Task 3: Add Runtime API And clusterv2 Snapshot Mapping

**Files:**
- Create: `pkg/controllerv2/runtime_node_health.go`
- Modify: `pkg/controllerv2/runtime_test.go`
- Modify: `pkg/clusterv2/control/controller.go`
- Modify: `pkg/clusterv2/control/snapshot.go`
- Modify: `pkg/clusterv2/control/controllerv2.go`
- Modify: `pkg/clusterv2/control/codec.go`
- Modify: `pkg/clusterv2/control/control_write.go`
- Modify: `pkg/clusterv2/control/runtime.go`
- Modify: `pkg/clusterv2/control/*_test.go`

- [ ] **Step 1: Write failing runtime and snapshot tests**

Add a ControllerV2 runtime test:

```go
func TestRuntimeReportNodeHealthUsesLeaderTimestamp(t *testing.T) {
	runtime := startSingleVoterRuntime(t, "cluster-node-health")
	_ = readStateEvent(t, runtime.Watch())

	result, err := runtime.ReportNodeHealth(context.Background(), ReportNodeHealthRequest{
		NodeID:                  1,
		Status:                  NodeStatusAlive,
		RuntimeReady:            true,
		ObservedControlRevision: 1,
		ReportSeq:               7,
	})
	if err != nil {
		t.Fatalf("ReportNodeHealth() error = %v", err)
	}
	if !result.Updated || result.Changed {
		t.Fatalf("ReportNodeHealth() = %#v, want updated without changed", result)
	}
	st, err := runtime.LocalState(context.Background())
	if err != nil {
		t.Fatalf("LocalState() error = %v", err)
	}
	if len(st.NodeHealthReports) != 1 || st.NodeHealthReports[0].ReportedAtUnixMilli == 0 {
		t.Fatalf("NodeHealthReports = %#v, want leader-side timestamp", st.NodeHealthReports)
	}
}
```

Add a clusterv2 control snapshot test:

```go
func TestControllerV2AdapterMapsNodeHealthFreshness(t *testing.T) {
	now := time.Date(2026, 6, 29, 10, 0, 0, 0, time.UTC)
	st := cv2.ClusterState{
		Revision: 9,
		Nodes: []cv2.Node{{NodeID: 1, Addr: "n1", Roles: []cv2.NodeRole{cv2.NodeRoleData}, JoinState: cv2.NodeJoinStateActive, Status: cv2.NodeStatusAlive}},
		NodeHealthReports: []cv2.NodeHealthReport{{
			NodeID:                  1,
			Status:                  cv2.NodeStatusAlive,
			RuntimeReady:            true,
			ObservedControlRevision: 9,
			ReportSeq:               3,
			ReportedAtUnixMilli:     now.Add(-5 * time.Second).UnixMilli(),
		}},
	}
	snap := snapshotFromControllerState(st, 1, now, 30*time.Second)
	if len(snap.Nodes) != 1 || snap.Nodes[0].Health.Freshness != NodeHealthFresh {
		t.Fatalf("snapshot nodes = %#v, want fresh health", snap.Nodes)
	}
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run:

```bash
GOWORK=off go test ./pkg/controllerv2 ./pkg/clusterv2/control -run 'ReportNodeHealth|HealthFreshness|ControlWrite' -count=1
```

Expected: compile fails because runtime API, snapshot health type, and control write action do not exist.

- [ ] **Step 3: Add ControllerV2 runtime API**

Create `pkg/controllerv2/runtime_node_health.go`:

```go
package controllerv2

import (
	"context"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/command"
	cv2raft "github.com/WuKongIM/WuKongIM/pkg/controllerv2/raft"
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/state"
)

// ReportNodeHealthRequest stores one low-frequency node health report.
type ReportNodeHealthRequest struct {
	NodeID                  uint64
	Status                  NodeStatus
	RuntimeReady            bool
	ObservedControlRevision uint64
	ObservedSlotRevision    uint64
	ReportSeq               uint64
	ErrorCode               string
}

// ReportNodeHealthResult describes the committed report outcome.
type ReportNodeHealthResult struct {
	Updated          bool
	Changed          bool
	Revision         uint64
	AppliedRaftIndex uint64
}

// ReportNodeHealth persists a bounded node health report through Controller Raft.
func (r *Runtime) ReportNodeHealth(ctx context.Context, req ReportNodeHealthRequest) (ReportNodeHealthResult, error) {
	if err := ctxErr(ctx); err != nil {
		return ReportNodeHealthResult{}, err
	}
	now := r.cfg.Now()
	if now.IsZero() {
		now = time.Now()
	}
	report := state.NodeHealthReport{
		NodeID:                  req.NodeID,
		Status:                  state.NodeStatus(req.Status),
		RuntimeReady:            req.RuntimeReady,
		ObservedControlRevision: req.ObservedControlRevision,
		ObservedSlotRevision:    req.ObservedSlotRevision,
		ReportSeq:               req.ReportSeq,
		ReportedAtUnixMilli:     now.UTC().UnixMilli(),
		ErrorCode:               req.ErrorCode,
	}
	result, err := r.raft.ProposeResult(ctx, command.Command{
		Kind:       command.KindReportNodeHealth,
		IssuedAt:   now.UTC(),
		NodeHealth: &report,
	})
	if err != nil {
		return ReportNodeHealthResult{}, err
	}
	if err := r.publishFromState(ctx); err != nil {
		return ReportNodeHealthResult{}, err
	}
	return ReportNodeHealthResult{Updated: result.Updated, Changed: result.Changed, Revision: result.Revision, AppliedRaftIndex: result.AppliedRaftIndex}, nil
}
```

Use existing package aliases where names differ.

- [ ] **Step 4: Add clusterv2 health snapshot types**

In `pkg/clusterv2/control/snapshot.go`, add:

```go
// NodeHealthFreshness describes whether a health report is usable for decisions.
type NodeHealthFreshness string

const (
	NodeHealthFresh   NodeHealthFreshness = "fresh"
	NodeHealthStale   NodeHealthFreshness = "stale"
	NodeHealthMissing NodeHealthFreshness = "missing"
)

// NodeHealth describes durable low-frequency health evidence for one node.
type NodeHealth struct {
	Status                  NodeStatus
	Freshness               NodeHealthFreshness
	RuntimeReady            bool
	ObservedControlRevision uint64
	ObservedSlotRevision    uint64
	ReportSeq               uint64
	ReportedAt              time.Time
	ReportAge               time.Duration
	ReportTTL               time.Duration
	ErrorCode               string
}
```

Add to `control.Node`:

```go
// Health contains durable low-frequency runtime health evidence.
Health NodeHealth
```

Add a helper:

```go
func BuildNodeHealth(report state.NodeHealthReport, exists bool, now time.Time, ttl time.Duration) NodeHealth {
	if !exists {
		return NodeHealth{Freshness: NodeHealthMissing, ReportTTL: ttl}
	}
	reportedAt := time.UnixMilli(report.ReportedAtUnixMilli).UTC()
	age := now.Sub(reportedAt)
	freshness := NodeHealthFresh
	if ttl <= 0 || age > ttl {
		freshness = NodeHealthStale
	}
	return NodeHealth{
		Status:                  NodeStatus(report.Status),
		Freshness:               freshness,
		RuntimeReady:            report.RuntimeReady,
		ObservedControlRevision: report.ObservedControlRevision,
		ObservedSlotRevision:    report.ObservedSlotRevision,
		ReportSeq:               report.ReportSeq,
		ReportedAt:              reportedAt,
		ReportAge:               age,
		ReportTTL:               ttl,
		ErrorCode:               report.ErrorCode,
	}
}
```

- [ ] **Step 5: Add control write action**

Add to `pkg/clusterv2/control/codec.go`:

```go
ControlWriteActionReportNodeHealth ControlWriteAction = "report_node_health"
```

Add `ReportNodeHealth NodeReport` to request JSON structs and marshal/unmarshal switches. In `control_write.go`, dispatch to the leader-side applier:

```go
case ControlWriteActionReportNodeHealth:
	err = a.ReportNode(ctx, req.ReportNodeHealth)
```

- [ ] **Step 6: Implement `control.Runtime.ReportNode`**

In `pkg/clusterv2/control/runtime.go`, make `ReportNode` call the local ControllerV2 runtime when local node is leader-capable, or forward `ControlWriteActionReportNodeHealth` through the existing control write client when needed. The request passed to ControllerV2 must not trust a remote timestamp.

Use this mapping:

```go
cv2.ReportNodeHealthRequest{
	NodeID:                  report.NodeID,
	Status:                  cv2.NodeStatus(report.Status),
	RuntimeReady:            report.RuntimeReady,
	ObservedControlRevision: report.ObservedControlRevision,
	ObservedSlotRevision:    report.ObservedSlotRevision,
	ReportSeq:               report.ReportSeq,
	ErrorCode:               report.ErrorCode,
}
```

- [ ] **Step 7: Run focused control tests**

Run:

```bash
GOWORK=off go test ./pkg/controllerv2 ./pkg/clusterv2/control -run 'ReportNodeHealth|ReportNode|ControlWrite|Freshness' -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add pkg/controllerv2 pkg/clusterv2/control
git commit -m "feat: expose node health through clusterv2 control"
```

---

### Task 4: Wire Low-Frequency Reporter Loop And Config

**Files:**
- Modify: `pkg/clusterv2/observe/reporter.go`
- Modify: `pkg/clusterv2/observe/observe_test.go`
- Modify: `pkg/clusterv2/config.go`
- Modify: `pkg/clusterv2/config_test.go`
- Modify: `pkg/clusterv2/node.go`
- Modify: `pkg/clusterv2/node_lifecycle.go`
- Modify: `pkg/clusterv2/node_loops.go`
- Modify: `cmd/wukongimv2/config.go`
- Modify: `cmd/wukongimv2/config_test.go`
- Modify: `wukongim.conf.example`
- Modify: `cmd/wukongimv2/wukongimv2.conf.example`

- [ ] **Step 1: Write failing reporter and config tests**

In `pkg/clusterv2/observe/observe_test.go`, update `TestReporterCallsController` to assert:

```go
if controller.node.RuntimeReady != true ||
	controller.node.ObservedControlRevision != 9 ||
	controller.node.ObservedSlotRevision != 12 ||
	controller.node.ReportSeq != 1 {
	t.Fatalf("ReportNode() = %#v, want bounded health fields", controller.node)
}
```

Construct reporter with:

```go
reporter := NewReporter(ReporterConfig{
	NodeID:                  2,
	Addr:                    "127.0.0.1:1002",
	Controller:              controller,
	RuntimeReady:            func() bool { return true },
	ObservedControlRevision: func() uint64 { return 9 },
	ObservedSlotRevision:    func() uint64 { return 12 },
	SlotStatus:              func() []SlotStatus { return []SlotStatus{{SlotID: 1, Leader: 2}} },
})
```

In `pkg/clusterv2/config_test.go`, add:

```go
func TestConfigAppliesHealthReportDefaults(t *testing.T) {
	cfg := Config{NodeID: 1, ListenAddr: "127.0.0.1:7001", DataDir: t.TempDir()}
	cfg.applyDefaults()
	if cfg.HealthReport.Interval != 5*time.Second || cfg.HealthReport.TTL != 30*time.Second {
		t.Fatalf("HealthReport defaults = %s/%s, want 5s/30s", cfg.HealthReport.Interval, cfg.HealthReport.TTL)
	}
}

func TestConfigRejectsInvalidHealthReportTTL(t *testing.T) {
	cfg := validConfig(t)
	cfg.HealthReport.Interval = 5 * time.Second
	cfg.HealthReport.TTL = time.Second
	if err := cfg.validate(); err == nil {
		t.Fatal("validate() error = nil, want TTL below interval rejected")
	}
}
```

In `cmd/wukongimv2/config_test.go`, add env/config coverage:

```go
func TestConfigParsesClusterNodeHealthReportTuning(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, []string{
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR=" + filepath.Join(dir, "data"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7001",
		"WK_CLUSTER_ID=health-config",
		`WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7001"}]`,
		"WK_CLUSTER_NODE_HEALTH_REPORT_INTERVAL=3s",
		"WK_CLUSTER_NODE_HEALTH_REPORT_TTL=15s",
	})
	cfg, err := loadConfig([]string{"-config", path})
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}
	if cfg.Cluster.HealthReport.Interval != 3*time.Second || cfg.Cluster.HealthReport.TTL != 15*time.Second {
		t.Fatalf("HealthReport = %s/%s, want 3s/15s", cfg.Cluster.HealthReport.Interval, cfg.Cluster.HealthReport.TTL)
	}
}
```

- [ ] **Step 2: Run the tests and verify they fail**

Run:

```bash
GOWORK=off go test ./pkg/clusterv2/observe ./pkg/clusterv2 ./cmd/wukongimv2 -run 'Reporter|HealthReport|Config' -count=1
```

Expected: compile/config failures because new fields and keys do not exist.

- [ ] **Step 3: Add clusterv2 health report config**

In `pkg/clusterv2/config.go`, add:

```go
// HealthReportConfig controls low-frequency node health reporting to ControllerV2.
type HealthReportConfig struct {
	// Interval controls how often a node reports compact health evidence.
	Interval time.Duration
	// TTL bounds how long the control plane may trust the latest report.
	TTL time.Duration
}
```

Add `HealthReport HealthReportConfig` to `Config`. Defaults:

```go
if c.HealthReport.Interval == 0 {
	c.HealthReport.Interval = 5 * time.Second
}
if c.HealthReport.TTL == 0 {
	c.HealthReport.TTL = 30 * time.Second
}
```

Validation:

```go
if c.HealthReport.Interval <= 0 || c.HealthReport.TTL <= 0 || c.HealthReport.TTL < c.HealthReport.Interval {
	return ErrInvalidConfig
}
```

- [ ] **Step 4: Extend observe reporter**

Add these fields to `observe.ReporterConfig`:

```go
RuntimeReady            func() bool
ObservedControlRevision func() uint64
ObservedSlotRevision    func() uint64
```

Add `reportSeq atomic.Uint64` to `Reporter` and use:

```go
seq := r.reportSeq.Add(1)
report := control.NodeReport{
	NodeID:                  r.cfg.NodeID,
	Addr:                    r.cfg.Addr,
	Status:                  control.NodeAlive,
	RuntimeReady:            callBool(r.cfg.RuntimeReady),
	ObservedControlRevision: callUint64(r.cfg.ObservedControlRevision),
	ObservedSlotRevision:    callUint64(r.cfg.ObservedSlotRevision),
	ReportSeq:               seq,
}
return r.cfg.Controller.ReportNode(ctx, report)
```

Use small helpers returning `false` or `0` for nil callbacks.

- [ ] **Step 5: Wire clusterv2 node loop**

Add fields to `Node`:

```go
healthReportCancel context.CancelFunc
healthReportWG     sync.WaitGroup
```

In `node_loops.go`, add:

```go
func (n *Node) startHealthReportLoop() {
	if n == nil || n.control == nil || n.healthReportCancel != nil {
		return
	}
	reporter := observe.NewReporter(observe.ReporterConfig{
		NodeID:                  n.cfg.NodeID,
		Addr:                    n.cfg.ListenAddr,
		Controller:              n.control,
		RuntimeReady:            n.runtimeReadyForHealthReport,
		ObservedControlRevision: n.observedControlRevision,
		ObservedSlotRevision:    n.observedSlotRevision,
		SlotStatus:              n.localSlotStatuses,
	})
	ctx, cancel := context.WithCancel(context.Background())
	n.healthReportCancel = cancel
	n.healthReportWG.Add(1)
	go func() {
		defer n.healthReportWG.Done()
		_ = reporter.ReportNode(ctx)
		loop := observe.NewLoop(n.cfg.HealthReport.Interval, func(ctx context.Context) error {
			return reporter.ReportNode(ctx)
		})
		loop.Start(ctx)
		<-ctx.Done()
		loop.Stop()
	}()
}
```

Add matching `stopHealthReportLoop`. Start it after `n.started.Store(true)` and stop it before `stopWatchLoop`.

- [ ] **Step 6: Parse config keys**

In `cmd/wukongimv2/config.go`, add supported keys:

```go
"WK_CLUSTER_NODE_HEALTH_REPORT_INTERVAL",
"WK_CLUSTER_NODE_HEALTH_REPORT_TTL",
```

Parse:

```go
if raw := configValue(values, "WK_CLUSTER_NODE_HEALTH_REPORT_INTERVAL"); raw != "" {
	interval, err := parseDuration("WK_CLUSTER_NODE_HEALTH_REPORT_INTERVAL", raw)
	if err != nil {
		return app.Config{}, err
	}
	cfg.Cluster.HealthReport.Interval = interval
}
if raw := configValue(values, "WK_CLUSTER_NODE_HEALTH_REPORT_TTL"); raw != "" {
	ttl, err := parseDuration("WK_CLUSTER_NODE_HEALTH_REPORT_TTL", raw)
	if err != nil {
		return app.Config{}, err
	}
	cfg.Cluster.HealthReport.TTL = ttl
}
```

Add English comments and example values to both config examples:

```text
# Low-frequency durable node health report cadence. TTL must be >= interval.
WK_CLUSTER_NODE_HEALTH_REPORT_INTERVAL=5s
WK_CLUSTER_NODE_HEALTH_REPORT_TTL=30s
```

- [ ] **Step 7: Run focused reporter/config tests**

Run:

```bash
GOWORK=off go test ./pkg/clusterv2/observe ./pkg/clusterv2 ./cmd/wukongimv2 -run 'Reporter|HealthReport|Config' -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add pkg/clusterv2 cmd/wukongimv2 wukongim.conf.example
git commit -m "feat: wire dynamic node health reporting loop"
```

---

### Task 5: Update FLOW Docs And Run Stage Gate

**Files:**
- Modify: `pkg/controllerv2/FLOW.md`
- Modify: `pkg/clusterv2/FLOW.md`

- [ ] **Step 1: Update ControllerV2 FLOW**

Add a short section to `pkg/controllerv2/FLOW.md`:

```markdown
### Node Health Reports

`ReportNodeHealth` is a low-frequency Controller Raft write that stores one
bounded `NodeHealthReport` per node. It updates durable health evidence and
`AppliedRaftIndex`, but it does not advance the logical cluster-state
`Revision`; placement and lifecycle compare-and-set fences must not race with
heartbeat churn. Consumers that need health freshness compare report age against
the configured TTL instead of interpreting membership `JoinState` as liveness.
```

- [ ] **Step 2: Update clusterv2 FLOW**

Add a short section to `pkg/clusterv2/FLOW.md`:

```markdown
### Health Report Loop

The node health report loop sends compact runtime evidence through
`control.ReportNode`: `status`, `runtime_ready`, observed control revision,
observed Slot revision, and a node-local report sequence. ControllerV2 fills the
leader-side report timestamp, stores the report durably, and control snapshots
derive `fresh`, `stale`, or `missing` health from the configured TTL.
```

- [ ] **Step 3: Run the Stage 9A gate**

Run:

```bash
GOWORK=off go test ./pkg/controllerv2/state ./pkg/controllerv2/fsm ./pkg/controllerv2 ./pkg/clusterv2/control ./pkg/clusterv2/observe ./pkg/clusterv2 -run 'Health|ReportNode|ControlWrite|Config' -count=1
GOWORK=off go test ./cmd/wukongimv2 -run 'Config|HealthReport' -count=1
git diff --check
```

Expected: PASS and no whitespace errors.

- [ ] **Step 4: Commit docs and gate update**

```bash
git add pkg/controllerv2/FLOW.md pkg/clusterv2/FLOW.md docs/superpowers/plans/2026-06-29-internalv2-dynamic-node-stage9-production-readiness.md
git commit -m "docs: document dynamic node health report model"
```
