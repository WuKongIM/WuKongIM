# InternalV2 Dynamic Node Stage 9B Health-Gated Placement And Remove Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Consume Stage 9A health freshness so new Slot/Channel placement and dynamic-node scale-in/remove fail closed when node health is stale, missing, down, suspect, or not runtime-ready.

**Architecture:** Centralize the schedulable-node predicate on `control.Node` health fields, then replace duplicated active-data filters in clusterv2 and management scale-in planning. Scale-in status gains explicit health blocker fields and bounded reason strings so operators can see exactly why removal is unsafe.

**Tech Stack:** Go, clusterv2 control snapshots, ChannelV2 default placement views, internalv2 management usecase, manager HTTP DTOs.

---

## Source Links

- Stage 9 spec: `docs/superpowers/specs/2026-06-29-internalv2-dynamic-node-stage9-production-readiness-design.md`
- Stage 9 master plan: `docs/superpowers/plans/2026-06-29-internalv2-dynamic-node-stage9-production-readiness.md`
- Stage 9A prerequisite: `docs/superpowers/plans/2026-06-29-internalv2-dynamic-node-stage9a-health-report-model.md`
- Required package docs before editing:
  - `pkg/clusterv2/FLOW.md`
  - `internalv2/usecase/management/FLOW.md`
  - `internalv2/access/manager/FLOW.md`

## Entry Gate

- [ ] Stage 9A has been implemented and its gate has passed.
- [ ] `control.Node` has `Health.Freshness`, `Health.Status`, `Health.RuntimeReady`, `Health.ObservedControlRevision`, and `Health.ReportAge`.
- [ ] ControllerV2 health-only writes do not advance logical `Snapshot.Revision`.

## File Map

- Modify: `pkg/clusterv2/control/snapshot.go`
  - Add a reusable `NodeSchedulableForPlacement` helper.
- Modify: `pkg/clusterv2/control/control_test.go`
  - Test schedulable predicates.
- Modify: `pkg/clusterv2/node_snapshot.go`
  - Update `activeDataNodeIDs` to require fresh alive health and runtime readiness.
- Modify: `pkg/clusterv2/node_snapshot_test.go`
  - Test active data filtering for fresh, stale, missing, suspect, down, joining, leaving, and removed nodes.
- Modify: `pkg/clusterv2/channel_data_nodes.go`
  - Ensure default ChannelV2 placement receives only health-schedulable active nodes.
- Modify: `internalv2/usecase/management/scale_in.go`
  - Add health blocker fields and use health-gated replacement candidates.
- Modify: `internalv2/usecase/management/scale_in_test.go`
  - Add stale/missing/down health status and plan blocker tests.
- Modify: `internalv2/access/manager/scale_in.go`
  - Add JSON DTO fields.
- Modify: `internalv2/access/manager/scale_in_test.go`
  - Assert manager JSON maps health blockers.
- Modify: `pkg/clusterv2/FLOW.md`
  - Update active data node semantics.
- Modify: `internalv2/usecase/management/FLOW.md`
  - Update scale-in fail-closed health semantics.
- Modify: `internalv2/access/manager/FLOW.md`
  - Document manager health blocker DTOs.

---

### Task 1: Add Shared Health-Schedulable Predicate

**Files:**
- Modify: `pkg/clusterv2/control/snapshot.go`
- Modify: `pkg/clusterv2/control/control_test.go`

- [ ] **Step 1: Write the failing predicate test**

Add to `pkg/clusterv2/control/control_test.go`:

```go
func TestNodeSchedulableForPlacementRequiresFreshAliveHealth(t *testing.T) {
	base := Node{
		NodeID:    1,
		Roles:     []Role{RoleData},
		JoinState: NodeJoinStateActive,
		Status:    NodeAlive,
		Health: NodeHealth{
			Status:       NodeAlive,
			Freshness:    NodeHealthFresh,
			RuntimeReady: true,
		},
	}
	tests := []struct {
		name string
		node Node
		want bool
	}{
		{name: "fresh alive active data", node: base, want: true},
		{name: "missing health", node: withHealth(base, NodeHealth{Freshness: NodeHealthMissing}), want: false},
		{name: "stale health", node: withHealth(base, NodeHealth{Status: NodeAlive, Freshness: NodeHealthStale, RuntimeReady: true}), want: false},
		{name: "suspect health", node: withHealth(base, NodeHealth{Status: NodeSuspect, Freshness: NodeHealthFresh, RuntimeReady: true}), want: false},
		{name: "down health", node: withHealth(base, NodeHealth{Status: NodeDown, Freshness: NodeHealthFresh, RuntimeReady: true}), want: false},
		{name: "not runtime ready", node: withHealth(base, NodeHealth{Status: NodeAlive, Freshness: NodeHealthFresh, RuntimeReady: false}), want: false},
		{name: "joining", node: withJoinState(base, NodeJoinStateJoining), want: false},
		{name: "leaving", node: withJoinState(base, NodeJoinStateLeaving), want: false},
		{name: "removed", node: withJoinState(base, NodeJoinStateRemoved), want: false},
		{name: "controller only", node: withRoles(base, []Role{RoleControllerVoter}), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NodeSchedulableForPlacement(tt.node); got != tt.want {
				t.Fatalf("NodeSchedulableForPlacement() = %v, want %v for %#v", got, tt.want, tt.node)
			}
		})
	}
}
```

Add small test helpers in the same file:

```go
func withHealth(node Node, health NodeHealth) Node { node.Health = health; return node }
func withJoinState(node Node, state NodeJoinState) Node { node.JoinState = state; return node }
func withRoles(node Node, roles []Role) Node { node.Roles = roles; return node }
```

- [ ] **Step 2: Run the test and verify it fails**

Run:

```bash
GOWORK=off go test ./pkg/clusterv2/control -run TestNodeSchedulableForPlacementRequiresFreshAliveHealth -count=1
```

Expected: compile fails because `NodeSchedulableForPlacement` does not exist.

- [ ] **Step 3: Implement the predicate**

Add to `pkg/clusterv2/control/snapshot.go`:

```go
// NodeSchedulableForPlacement reports whether a node can receive new data placement.
func NodeSchedulableForPlacement(node Node) bool {
	if !nodeHasRole(node.Roles, RoleData) {
		return false
	}
	if normalizeNodeJoinState(node.JoinState) != NodeJoinStateActive {
		return false
	}
	return node.Health.Freshness == NodeHealthFresh &&
		node.Health.Status == NodeAlive &&
		node.Health.RuntimeReady
}

func normalizeNodeJoinState(state NodeJoinState) NodeJoinState {
	if state == "" {
		return NodeJoinStateActive
	}
	return state
}

func nodeHasRole(roles []Role, role Role) bool {
	for _, item := range roles {
		if item == role {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run predicate tests**

Run:

```bash
GOWORK=off go test ./pkg/clusterv2/control -run TestNodeSchedulableForPlacementRequiresFreshAliveHealth -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/clusterv2/control
git commit -m "feat: define health-gated node placement predicate"
```

---

### Task 2: Gate clusterv2 Placement Candidate Views

**Files:**
- Modify: `pkg/clusterv2/node_snapshot.go`
- Modify: `pkg/clusterv2/node_snapshot_test.go`
- Modify: `pkg/clusterv2/channel_data_nodes.go`

- [ ] **Step 1: Write failing active-data filtering test**

Update or add a test in `pkg/clusterv2/node_snapshot_test.go`:

```go
func TestActiveDataNodeIDsRequireFreshAliveHealth(t *testing.T) {
	nodes := []control.Node{
		healthNode(1, control.NodeJoinStateActive, control.NodeAlive, control.NodeHealthFresh, true),
		healthNode(2, control.NodeJoinStateActive, control.NodeAlive, control.NodeHealthStale, true),
		healthNode(3, control.NodeJoinStateActive, control.NodeSuspect, control.NodeHealthFresh, true),
		healthNode(4, control.NodeJoinStateJoining, control.NodeAlive, control.NodeHealthFresh, true),
		healthNode(5, control.NodeJoinStateLeaving, control.NodeAlive, control.NodeHealthFresh, true),
		healthNode(6, control.NodeJoinStateActive, control.NodeAlive, control.NodeHealthMissing, false),
	}
	got := activeDataNodeIDs(nodes)
	want := []uint64{1}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("activeDataNodeIDs() = %v, want %v", got, want)
	}
}
```

Use a helper:

```go
func healthNode(nodeID uint64, joinState control.NodeJoinState, status control.NodeStatus, freshness control.NodeHealthFreshness, ready bool) control.Node {
	return control.Node{
		NodeID:    nodeID,
		Roles:     []control.Role{control.RoleData},
		JoinState: joinState,
		Status:    status,
		Health: control.NodeHealth{
			Status:       status,
			Freshness:    freshness,
			RuntimeReady: ready,
		},
	}
}
```

- [ ] **Step 2: Run the test and verify it fails**

Run:

```bash
GOWORK=off go test ./pkg/clusterv2 -run TestActiveDataNodeIDsRequireFreshAliveHealth -count=1
```

Expected: FAIL because current `activeDataNodeIDs` still includes stale/suspect active data nodes.

- [ ] **Step 3: Update active data filtering**

Replace the body of `activeDataNodeIDs` in `pkg/clusterv2/node_snapshot.go` with:

```go
func activeDataNodeIDs(nodes []control.Node) []uint64 {
	out := make([]uint64, 0, len(nodes))
	for _, node := range nodes {
		if !control.NodeSchedulableForPlacement(node) {
			continue
		}
		out = append(out, node.NodeID)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
```

Remove or update the old comment that said health freshness was intentionally ignored.

- [ ] **Step 4: Run clusterv2 placement tests**

Run:

```bash
GOWORK=off go test ./pkg/clusterv2 -run 'ActiveDataNodeIDs|ChannelDataNodes|ApplySnapshot|DefaultChannel' -count=1
```

Expected: PASS. If an existing test relied on health-free active nodes, update the fixture to add fresh `NodeHealth`.

- [ ] **Step 5: Commit**

```bash
git add pkg/clusterv2
git commit -m "feat: gate clusterv2 placement on fresh node health"
```

---

### Task 3: Add Scale-In Health Blockers

**Files:**
- Modify: `internalv2/usecase/management/scale_in.go`
- Modify: `internalv2/usecase/management/scale_in_test.go`

- [ ] **Step 1: Write failing scale-in status tests**

Add tests to `internalv2/usecase/management/scale_in_test.go`:

```go
func TestScaleInStatusBlocksWhenTargetHealthIsStale(t *testing.T) {
	app := newScaleInTestApp(t)
	app.snapshot.Nodes = []control.Node{
		scaleInHealthNode(1, control.NodeJoinStateActive, control.NodeAlive, control.NodeHealthFresh, true, app.snapshot.Revision),
		scaleInHealthNode(4, control.NodeJoinStateLeaving, control.NodeAlive, control.NodeHealthStale, true, app.snapshot.Revision),
	}
	status, err := app.NodeScaleInStatus(context.Background(), NodeScaleInStatusRequest{NodeID: 4})
	if err != nil {
		t.Fatalf("NodeScaleInStatus() error = %v", err)
	}
	if !status.BlockedByHealth || status.HealthFresh || status.HealthStatus != string(control.NodeAlive) {
		t.Fatalf("status = %#v, want stale target health blocker", status)
	}
	requireContainsString(t, status.BlockedReasons, "target_health_stale")
}

func TestScaleInStatusBlocksWhenEligibleNodeHasNotObservedRevision(t *testing.T) {
	app := newScaleInTestApp(t)
	app.snapshot.Revision = 22
	app.snapshot.Nodes = []control.Node{
		scaleInHealthNode(1, control.NodeJoinStateActive, control.NodeAlive, control.NodeHealthFresh, true, 21),
		scaleInHealthNode(4, control.NodeJoinStateLeaving, control.NodeAlive, control.NodeHealthFresh, true, 22),
	}
	status, err := app.NodeScaleInStatus(context.Background(), NodeScaleInStatusRequest{NodeID: 4})
	if err != nil {
		t.Fatalf("NodeScaleInStatus() error = %v", err)
	}
	if !status.BlockedByStaleRevision || !status.BlockedByHealth {
		t.Fatalf("status = %#v, want stale observed revision health blocker", status)
	}
	requireContainsString(t, status.BlockedReasons, "eligible_node_health_revision_stale")
}
```

Helper:

```go
func scaleInHealthNode(nodeID uint64, joinState control.NodeJoinState, status control.NodeStatus, freshness control.NodeHealthFreshness, ready bool, observedRevision uint64) control.Node {
	return control.Node{
		NodeID:    nodeID,
		Roles:     []control.Role{control.RoleData},
		JoinState: joinState,
		Status:    status,
		Health: control.NodeHealth{
			Status:                  status,
			Freshness:               freshness,
			RuntimeReady:            ready,
			ObservedControlRevision: observedRevision,
			ReportAge:               time.Second,
			ReportTTL:               30 * time.Second,
		},
	}
}
```

- [ ] **Step 2: Run the tests and verify they fail**

Run:

```bash
GOWORK=off go test ./internalv2/usecase/management -run 'TestScaleInStatusBlocksWhenTargetHealth|TestScaleInStatusBlocksWhenEligibleNodeHasNotObservedRevision' -count=1
```

Expected: compile fails because response fields are missing.

- [ ] **Step 3: Add response fields**

Add to `NodeScaleInStatusResponse`:

```go
// BlockedByHealth reports stale, missing, down, suspect, or not-ready health evidence.
BlockedByHealth bool
// BlockedByStaleRevision reports fresh health that has not observed the required control revision.
BlockedByStaleRevision bool
// HealthFresh reports whether the target node health is fresh.
HealthFresh bool
// HealthStatus is the target node reported health status.
HealthStatus string
// HealthFreshness is fresh, stale, or missing for the target node report.
HealthFreshness string
// HealthReportAgeMS is the target node health report age in milliseconds.
HealthReportAgeMS int64
// HealthReportTTLMS is the configured freshness TTL in milliseconds.
HealthReportTTLMS int64
// ObservedControlRevision is the target node's reported control revision.
ObservedControlRevision uint64
// RequiredControlRevision is the revision this status requires eligible nodes to observe.
RequiredControlRevision uint64
// BlockedReasons contains bounded machine-readable safety reasons.
BlockedReasons []string
```

- [ ] **Step 4: Implement health blocker marking**

Add constants near other blocker strings:

```go
const (
	scaleInReasonTargetHealthMissing            = "target_health_missing"
	scaleInReasonTargetHealthStale              = "target_health_stale"
	scaleInReasonTargetHealthNotAlive           = "target_health_not_alive"
	scaleInReasonTargetRuntimeNotReady          = "target_runtime_not_ready"
	scaleInReasonEligibleHealthMissing          = "eligible_node_health_missing"
	scaleInReasonEligibleHealthStale            = "eligible_node_health_stale"
	scaleInReasonEligibleHealthRevisionStale    = "eligible_node_health_revision_stale"
	scaleInReasonEligibleRuntimeNotReady        = "eligible_node_runtime_not_ready"
)
```

Add helper:

```go
func markBlockedReason(response *NodeScaleInStatusResponse, reason string) {
	for _, existing := range response.BlockedReasons {
		if existing == reason {
			return
		}
	}
	response.BlockedReasons = append(response.BlockedReasons, reason)
}
```

Add health blocker method:

```go
func markScaleInHealthBlockers(snapshot control.Snapshot, targetNode uint64, response *NodeScaleInStatusResponse) {
	response.RequiredControlRevision = snapshot.Revision
	target, ok := findControlNode(snapshot.Nodes, targetNode)
	if !ok {
		return
	}
	health := target.Health
	response.HealthFresh = health.Freshness == control.NodeHealthFresh
	response.HealthStatus = string(health.Status)
	response.HealthFreshness = string(health.Freshness)
	response.HealthReportAgeMS = health.ReportAge.Milliseconds()
	response.HealthReportTTLMS = health.ReportTTL.Milliseconds()
	response.ObservedControlRevision = health.ObservedControlRevision

	switch {
	case health.Freshness == control.NodeHealthMissing:
		response.BlockedByHealth = true
		markBlockedReason(response, scaleInReasonTargetHealthMissing)
	case health.Freshness != control.NodeHealthFresh:
		response.BlockedByHealth = true
		markBlockedReason(response, scaleInReasonTargetHealthStale)
	case health.Status != control.NodeAlive:
		response.BlockedByHealth = true
		markBlockedReason(response, scaleInReasonTargetHealthNotAlive)
	case !health.RuntimeReady:
		response.BlockedByHealth = true
		markBlockedReason(response, scaleInReasonTargetRuntimeNotReady)
	}

	for _, node := range snapshot.Nodes {
		if node.NodeID == targetNode || !hasControlRole(node.Roles, control.RoleData) {
			continue
		}
		if controlNodeJoinState(node.JoinState) != control.NodeJoinStateActive {
			continue
		}
		h := node.Health
		if h.Freshness == control.NodeHealthMissing {
			response.BlockedByHealth = true
			markBlockedReason(response, scaleInReasonEligibleHealthMissing)
			continue
		}
		if h.Freshness != control.NodeHealthFresh {
			response.BlockedByHealth = true
			markBlockedReason(response, scaleInReasonEligibleHealthStale)
			continue
		}
		if !h.RuntimeReady {
			response.BlockedByHealth = true
			markBlockedReason(response, scaleInReasonEligibleRuntimeNotReady)
			continue
		}
		if h.ObservedControlRevision < snapshot.Revision {
			response.BlockedByHealth = true
			response.BlockedByStaleRevision = true
			markBlockedReason(response, scaleInReasonEligibleHealthRevisionStale)
		}
	}
}
```

Call it inside `nodeScaleInStatusFromSnapshot` after target node lookup and before computing `SafeToProceed`.

- [ ] **Step 5: Include health blockers in plan blocking**

Update `scaleInStatusBlocksPlan`:

```go
return status.BlockedByMissingNode ||
	status.BlockedByJoinState ||
	status.BlockedByControlRevision ||
	status.BlockedByHealth ||
	status.BlockedByStaleRevision ||
	status.BlockedByControllerRole ||
	status.BlockedByDataRole ||
	status.BlockedBySlotRuntime ||
	status.BlockedByTasks ||
	status.UnknownRuntime
```

- [ ] **Step 6: Run management tests**

Run:

```bash
GOWORK=off go test ./internalv2/usecase/management -run 'ScaleInStatus|PlanNodeScaleIn|Health' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internalv2/usecase/management
git commit -m "feat: block scale-in on stale node health"
```

---

### Task 4: Gate Scale-In Replacement Candidates

**Files:**
- Modify: `internalv2/usecase/management/scale_in.go`
- Modify: `internalv2/usecase/management/scale_in_test.go`

- [ ] **Step 1: Write failing replacement-candidate test**

Add:

```go
func TestPlanNodeScaleInSkipsStaleHealthReplacementNode(t *testing.T) {
	app := newScaleInTestApp(t)
	app.snapshot.Revision = 31
	app.snapshot.Nodes = []control.Node{
		scaleInHealthNode(1, control.NodeJoinStateActive, control.NodeAlive, control.NodeHealthStale, true, 31),
		scaleInHealthNode(2, control.NodeJoinStateActive, control.NodeAlive, control.NodeHealthFresh, true, 31),
		scaleInHealthNode(4, control.NodeJoinStateLeaving, control.NodeAlive, control.NodeHealthFresh, true, 31),
	}
	app.snapshot.Slots = []control.SlotAssignment{{SlotID: 1, DesiredPeers: []uint64{1, 4}, ConfigEpoch: 7}}

	plan, err := app.PlanNodeScaleIn(context.Background(), NodeScaleInPlanRequest{NodeID: 4, MaxSlotMoves: 1})
	if err != nil {
		t.Fatalf("PlanNodeScaleIn() error = %v", err)
	}
	if len(plan.Candidates) != 1 || plan.Candidates[0].TargetNodeID != 2 {
		t.Fatalf("Candidates = %#v, want fresh node 2 as replacement", plan.Candidates)
	}
}
```

- [ ] **Step 2: Run the test and verify it fails**

Run:

```bash
GOWORK=off go test ./internalv2/usecase/management -run TestPlanNodeScaleInSkipsStaleHealthReplacementNode -count=1
```

Expected: FAIL because stale active nodes are still considered replacements.

- [ ] **Step 3: Update replacement active-node list**

Change `scaleInActiveDataNodeIDs`:

```go
func scaleInActiveDataNodeIDs(nodes []control.Node) []uint64 {
	out := make([]uint64, 0, len(nodes))
	for _, node := range nodes {
		if control.NodeSchedulableForPlacement(node) {
			out = append(out, node.NodeID)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
```

Keep target leaving-node validation separate; a leaving target is never a replacement candidate.

- [ ] **Step 4: Run plan tests**

Run:

```bash
GOWORK=off go test ./internalv2/usecase/management -run 'PlanNodeScaleIn|ScaleInStatus' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internalv2/usecase/management
git commit -m "feat: gate scale-in replacements on fresh health"
```

---

### Task 5: Map Manager DTO Health Blockers

**Files:**
- Modify: `internalv2/access/manager/scale_in.go`
- Modify: `internalv2/access/manager/scale_in_test.go`

- [ ] **Step 1: Write failing manager DTO test**

In `internalv2/access/manager/scale_in_test.go`, add or extend status mapping assertions:

```go
func TestManagerScaleInStatusMapsHealthBlockers(t *testing.T) {
	response := nodeScaleInStatusResponseDTO(managementusecase.NodeScaleInStatusResponse{
		NodeID:                  4,
		JoinState:               "leaving",
		GeneratedAt:             time.Unix(1710000000, 0).UTC(),
		StateRevision:           22,
		BlockedByHealth:         true,
		BlockedByStaleRevision:  true,
		HealthFresh:             false,
		HealthStatus:            "alive",
		HealthFreshness:         "stale",
		HealthReportAgeMS:       31000,
		HealthReportTTLMS:       30000,
		ObservedControlRevision: 21,
		RequiredControlRevision: 22,
		BlockedReasons:          []string{"target_health_stale", "eligible_node_health_revision_stale"},
	})
	if !response.BlockedByHealth || !response.BlockedByStaleRevision || response.HealthFresh {
		t.Fatalf("response = %#v, want health blockers", response)
	}
	if response.HealthReportAgeMS != 31000 || response.RequiredControlRevision != 22 {
		t.Fatalf("response = %#v, want health age and required revision", response)
	}
	if !reflect.DeepEqual(response.BlockedReasons, []string{"target_health_stale", "eligible_node_health_revision_stale"}) {
		t.Fatalf("BlockedReasons = %#v", response.BlockedReasons)
	}
}
```

- [ ] **Step 2: Run the test and verify it fails**

Run:

```bash
GOWORK=off go test ./internalv2/access/manager -run TestManagerScaleInStatusMapsHealthBlockers -count=1
```

Expected: compile fails because DTO fields are missing.

- [ ] **Step 3: Add DTO fields and mapping**

Add to `ManagerNodeScaleInStatusResponse`:

```go
BlockedByHealth         bool     `json:"blocked_by_health"`
BlockedByStaleRevision  bool     `json:"blocked_by_stale_revision"`
HealthFresh             bool     `json:"health_fresh"`
HealthStatus            string   `json:"health_status"`
HealthFreshness         string   `json:"health_freshness"`
HealthReportAgeMS       int64    `json:"health_report_age_ms"`
HealthReportTTLMS       int64    `json:"health_report_ttl_ms"`
ObservedControlRevision uint64   `json:"observed_control_revision"`
RequiredControlRevision uint64   `json:"required_control_revision"`
BlockedReasons          []string `json:"blocked_reasons"`
```

Map these fields in `nodeScaleInStatusResponseDTO`.

- [ ] **Step 4: Run manager tests**

Run:

```bash
GOWORK=off go test ./internalv2/access/manager -run 'ScaleInStatus|HealthBlockers' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internalv2/access/manager
git commit -m "feat: expose scale-in health blockers in manager API"
```

---

### Task 6: Update FLOW Docs And Run Stage Gate

**Files:**
- Modify: `pkg/clusterv2/FLOW.md`
- Modify: `internalv2/usecase/management/FLOW.md`
- Modify: `internalv2/access/manager/FLOW.md`
- Modify: `docs/superpowers/plans/2026-06-29-internalv2-dynamic-node-stage9-production-readiness.md`

- [ ] **Step 1: Update FLOW docs**

Add these points:

```markdown
- New Slot and Channel placement uses `control.NodeSchedulableForPlacement`.
  A node must be data-role, `active`, fresh `alive`, and runtime-ready.
- Scale-in status now fails closed on missing/stale health and on fresh reports
  that have not observed the required control revision.
- Manager scale-in status returns bounded health blocker fields and
  `blocked_reasons` strings for operator diagnosis.
```

- [ ] **Step 2: Run the Stage 9B gate**

Run:

```bash
GOWORK=off go test ./pkg/clusterv2 ./internalv2/usecase/management ./internalv2/access/manager -run 'Health|Placement|ScaleIn|Remove|NodeList' -count=1
git diff --check
```

Expected: PASS and no whitespace errors.

- [ ] **Step 3: Commit docs and gate update**

```bash
git add pkg/clusterv2/FLOW.md internalv2/usecase/management/FLOW.md internalv2/access/manager/FLOW.md docs/superpowers/plans/2026-06-29-internalv2-dynamic-node-stage9-production-readiness.md
git commit -m "docs: document health-gated dynamic node placement"
```
