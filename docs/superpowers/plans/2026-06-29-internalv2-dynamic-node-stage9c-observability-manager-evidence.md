# InternalV2 Dynamic Node Stage 9C Observability And Manager Evidence Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Expose dynamic-node lifecycle health, freshness, and scale-in blockers through low-cardinality Prometheus metrics and manager HTTP JSON fields.

**Architecture:** Reuse the existing `controlSnapshotMetricsObserver` path for current-state gauges and the manager usecase read models for operator JSON. Metrics aggregate by lifecycle state, health status, freshness, operation, result, task state, and bounded reason strings; they do not label by UID, channel ID, task ID, session ID, or address.

**Tech Stack:** Go, `pkg/metrics`, internalv2 app observability wiring, internalv2 management usecase, manager HTTP DTO tests.

---

## Source Links

- Stage 9 spec: `docs/superpowers/specs/2026-06-29-internalv2-dynamic-node-stage9-production-readiness-design.md`
- Stage 9 master plan: `docs/superpowers/plans/2026-06-29-internalv2-dynamic-node-stage9-production-readiness.md`
- Stage 9A prerequisite: `docs/superpowers/plans/2026-06-29-internalv2-dynamic-node-stage9a-health-report-model.md`
- Stage 9B prerequisite: `docs/superpowers/plans/2026-06-29-internalv2-dynamic-node-stage9b-health-gated-placement-remove.md`
- Required package docs before editing:
  - `internalv2/app/FLOW.md`
  - `internalv2/usecase/management/FLOW.md`
  - `internalv2/access/manager/FLOW.md`

## Entry Gate

- [ ] Stage 9B has been implemented and its gate has passed.
- [ ] `control.NodeSchedulableForPlacement` is the shared placement predicate.
- [ ] `NodeScaleInStatusResponse` already contains health blocker fields and `BlockedReasons`.

## File Map

- Create or modify: `pkg/metrics/node_lifecycle.go`
  - Add lifecycle/freshness gauges and bounded lifecycle operation counters.
- Modify: `pkg/metrics/registry.go`
  - Add `NodeLifecycle *NodeLifecycleMetrics`.
- Modify: `pkg/metrics/registry_test.go`
  - Assert metric names, values, and low-cardinality labels.
- Modify: `internalv2/app/observability.go`
  - Map control snapshots into lifecycle and health metrics.
- Modify: `internalv2/app/observability_test.go`
  - Test snapshot-to-metrics mapping for lifecycle, health freshness, tasks, and revision.
- Modify: `internalv2/usecase/management/nodes.go`
  - Add health freshness fields to manager node read model and make `membership.schedulable` use the shared predicate.
- Modify: `internalv2/usecase/management/nodes_test.go`
  - Test node list health fields.
- Modify: `internalv2/access/manager/nodes.go`
  - Add JSON fields to `NodeHealthDTO`.
- Modify: `internalv2/access/manager/server_test.go`
  - Assert manager node JSON maps health fields.
- Modify: `internalv2/access/manager/scale_in.go`
  - Ensure Stage 9B scale-in health fields are included in public JSON.
- Modify: `internalv2/access/manager/scale_in_test.go`
  - Keep status JSON coverage for health blockers.
- Modify: `internalv2/app/FLOW.md`
  - Document lifecycle metrics wiring.
- Modify: `internalv2/usecase/management/FLOW.md`
  - Document manager node health read model.
- Modify: `internalv2/access/manager/FLOW.md`
  - Document manager JSON fields.

## Metric Contract

Add these low-cardinality metric families:

```text
wukongim_node_lifecycle_nodes{join_state,status}
wukongim_node_health_freshness_nodes{freshness,status}
wukongim_node_health_report_age_seconds{freshness,status}
wukongim_node_lifecycle_attempts_total{operation,result}
wukongim_node_onboarding_tasks{state}
wukongim_node_scale_in_blockers_total{reason}
wukongim_slot_replica_move_duration_seconds{result}
wukongim_slot_replica_move_failures_total{reason}
wukongim_discovery_membership_revision
```

`wukongim_node_health_report_age_seconds` is a gauge containing the maximum current report age for each freshness/status group in the latest locally visible snapshot. It intentionally avoids a per-node label.

---

### Task 1: Add Node Lifecycle Metrics

**Files:**
- Create or modify: `pkg/metrics/node_lifecycle.go`
- Modify: `pkg/metrics/registry.go`
- Modify: `pkg/metrics/registry_test.go`

- [ ] **Step 1: Write failing metrics registry test**

Add to `pkg/metrics/registry_test.go`:

```go
func TestNodeLifecycleMetricsTrackHealthFreshnessAndBlockers(t *testing.T) {
	reg := New(1, "node-1")

	reg.NodeLifecycle.SetLifecycleNodes(map[NodeLifecycleKey]int{
		{JoinState: "active", Status: "alive"}: 2,
		{JoinState: "leaving", Status: "alive"}: 1,
	})
	reg.NodeLifecycle.SetHealthFreshnessNodes(map[NodeHealthFreshnessKey]int{
		{Freshness: "fresh", Status: "alive"}: 2,
		{Freshness: "stale", Status: "alive"}: 1,
	})
	reg.NodeLifecycle.SetHealthReportAgeMax(map[NodeHealthFreshnessKey]float64{
		{Freshness: "fresh", Status: "alive"}: 4.5,
		{Freshness: "stale", Status: "alive"}: 31,
	})
	reg.NodeLifecycle.ObserveLifecycleAttempt("activate", "ok")
	reg.NodeLifecycle.ObserveScaleInBlocker("target_health_stale")
	reg.NodeLifecycle.SetOnboardingTasks(map[string]int{"pending": 1, "running": 2, "failed": 0})
	reg.NodeLifecycle.SetDiscoveryMembershipRevision(42)

	families, err := reg.Gather()
	require.NoError(t, err)

	lifecycle := requireMetricFamily(t, families, "wukongim_node_lifecycle_nodes")
	require.Equal(t, float64(2), findMetricByLabels(t, lifecycle, map[string]string{
		"node_id": "1", "node_name": "node-1", "join_state": "active", "status": "alive",
	}).GetGauge().GetValue())

	freshness := requireMetricFamily(t, families, "wukongim_node_health_freshness_nodes")
	require.Equal(t, float64(1), findMetricByLabels(t, freshness, map[string]string{
		"node_id": "1", "node_name": "node-1", "freshness": "stale", "status": "alive",
	}).GetGauge().GetValue())

	age := requireMetricFamily(t, families, "wukongim_node_health_report_age_seconds")
	require.Equal(t, float64(31), findMetricByLabels(t, age, map[string]string{
		"node_id": "1", "node_name": "node-1", "freshness": "stale", "status": "alive",
	}).GetGauge().GetValue())

	attempts := requireMetricFamily(t, families, "wukongim_node_lifecycle_attempts_total")
	require.Equal(t, float64(1), findMetricByLabels(t, attempts, map[string]string{
		"node_id": "1", "node_name": "node-1", "operation": "activate", "result": "ok",
	}).GetCounter().GetValue())

	blockers := requireMetricFamily(t, families, "wukongim_node_scale_in_blockers_total")
	require.Equal(t, float64(1), findMetricByLabels(t, blockers, map[string]string{
		"node_id": "1", "node_name": "node-1", "reason": "target_health_stale",
	}).GetCounter().GetValue())

	revision := requireMetricFamily(t, families, "wukongim_discovery_membership_revision")
	require.Equal(t, float64(42), revision.GetMetric()[0].GetGauge().GetValue())
}
```

- [ ] **Step 2: Run the test and verify it fails**

Run:

```bash
GOWORK=off go test ./pkg/metrics -run TestNodeLifecycleMetricsTrackHealthFreshnessAndBlockers -count=1
```

Expected: compile fails because `NodeLifecycleMetrics` does not exist.

- [ ] **Step 3: Add metrics type**

Create `pkg/metrics/node_lifecycle.go`:

```go
package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// NodeLifecycleKey identifies current node lifecycle count buckets.
type NodeLifecycleKey struct {
	JoinState string
	Status    string
}

// NodeHealthFreshnessKey identifies current health freshness count buckets.
type NodeHealthFreshnessKey struct {
	Freshness string
	Status    string
}

// NodeLifecycleMetrics exposes low-cardinality dynamic node lifecycle evidence.
type NodeLifecycleMetrics struct {
	lifecycleNodes     *prometheus.GaugeVec
	healthFreshness    *prometheus.GaugeVec
	healthReportAgeMax *prometheus.GaugeVec
	lifecycleAttempts  *prometheus.CounterVec
	onboardingTasks    *prometheus.GaugeVec
	scaleInBlockers    *prometheus.CounterVec
	slotMoveDuration   *prometheus.HistogramVec
	slotMoveFailures   *prometheus.CounterVec
	membershipRevision prometheus.Gauge
}
```

Use `prometheus.NewGaugeVec`, `NewCounterVec`, and `NewHistogramVec` with `ConstLabels: labels`. Register all collectors in `newNodeLifecycleMetrics`.

- [ ] **Step 4: Add setter methods**

Implement methods with nil guards:

```go
func (m *NodeLifecycleMetrics) SetLifecycleNodes(counts map[NodeLifecycleKey]int) {
	if m == nil {
		return
	}
	for key, count := range counts {
		m.lifecycleNodes.WithLabelValues(fallbackMetricLabel(key.JoinState), fallbackMetricLabel(key.Status)).Set(float64(count))
	}
}

func (m *NodeLifecycleMetrics) SetHealthFreshnessNodes(counts map[NodeHealthFreshnessKey]int) {
	if m == nil {
		return
	}
	for key, count := range counts {
		m.healthFreshness.WithLabelValues(fallbackMetricLabel(key.Freshness), fallbackMetricLabel(key.Status)).Set(float64(count))
	}
}

func (m *NodeLifecycleMetrics) SetHealthReportAgeMax(ages map[NodeHealthFreshnessKey]float64) {
	if m == nil {
		return
	}
	for key, age := range ages {
		m.healthReportAgeMax.WithLabelValues(fallbackMetricLabel(key.Freshness), fallbackMetricLabel(key.Status)).Set(age)
	}
}

func (m *NodeLifecycleMetrics) ObserveLifecycleAttempt(operation, result string) {
	if m == nil {
		return
	}
	m.lifecycleAttempts.WithLabelValues(fallbackMetricLabel(operation), fallbackMetricLabel(result)).Inc()
}

func (m *NodeLifecycleMetrics) ObserveScaleInBlocker(reason string) {
	if m == nil {
		return
	}
	m.scaleInBlockers.WithLabelValues(fallbackMetricLabel(reason)).Inc()
}

func (m *NodeLifecycleMetrics) SetOnboardingTasks(counts map[string]int) {
	if m == nil {
		return
	}
	for state, count := range counts {
		m.onboardingTasks.WithLabelValues(fallbackMetricLabel(state)).Set(float64(count))
	}
}

func (m *NodeLifecycleMetrics) ObserveSlotReplicaMove(result string, d time.Duration) {
	if m == nil {
		return
	}
	m.slotMoveDuration.WithLabelValues(fallbackMetricLabel(result)).Observe(d.Seconds())
}

func (m *NodeLifecycleMetrics) ObserveSlotReplicaMoveFailure(reason string) {
	if m == nil {
		return
	}
	m.slotMoveFailures.WithLabelValues(fallbackMetricLabel(reason)).Inc()
}

func (m *NodeLifecycleMetrics) SetDiscoveryMembershipRevision(revision uint64) {
	if m == nil {
		return
	}
	m.membershipRevision.Set(float64(revision))
}
```

If `fallbackMetricLabel` does not exist in `pkg/metrics`, add a local helper that returns `"unknown"` for an empty string.

- [ ] **Step 5: Wire registry**

Add `NodeLifecycle *NodeLifecycleMetrics` to `Registry` and construct it in `New`.

- [ ] **Step 6: Run metrics tests**

Run:

```bash
GOWORK=off go test ./pkg/metrics -run 'NodeLifecycle|ClusterMonitorGaugesStayAbsentUntilObserved' -count=1
```

Expected: PASS. Existing absent-until-observed test should still pass for unrelated cluster monitor gauges.

- [ ] **Step 7: Commit**

```bash
git add pkg/metrics
git commit -m "feat: add dynamic node lifecycle metrics"
```

---

### Task 2: Map Control Snapshots To Lifecycle Metrics

**Files:**
- Modify: `internalv2/app/observability.go`
- Modify: `internalv2/app/observability_test.go`

- [ ] **Step 1: Write failing observer test**

Add to `internalv2/app/observability_test.go`:

```go
func TestControlSnapshotMetricsObserverMapsNodeLifecycleHealth(t *testing.T) {
	reg := obsmetrics.New(1, "n1")
	observer := controlSnapshotMetricsObserver{metrics: reg}

	observer.ObserveControlSnapshot(control.Snapshot{
		Revision: 42,
		Nodes: []control.Node{
			{NodeID: 1, Roles: []control.Role{control.RoleData}, JoinState: control.NodeJoinStateActive, Status: control.NodeAlive, Health: control.NodeHealth{Status: control.NodeAlive, Freshness: control.NodeHealthFresh, RuntimeReady: true, ReportAge: 4 * time.Second}},
			{NodeID: 2, Roles: []control.Role{control.RoleData}, JoinState: control.NodeJoinStateLeaving, Status: control.NodeAlive, Health: control.NodeHealth{Status: control.NodeAlive, Freshness: control.NodeHealthStale, RuntimeReady: true, ReportAge: 31 * time.Second}},
			{NodeID: 3, Roles: []control.Role{control.RoleData}, JoinState: control.NodeJoinStateActive, Status: control.NodeDown, Health: control.NodeHealth{Status: control.NodeDown, Freshness: control.NodeHealthFresh, RuntimeReady: false, ReportAge: time.Second}},
		},
		Tasks: []control.ReconcileTask{
			{Kind: control.TaskKindSlotReplicaMove, Status: control.TaskStatusPending},
			{Kind: control.TaskKindSlotReplicaMove, Status: control.TaskStatusRunning},
			{Kind: control.TaskKindSlotReplicaMove, Status: control.TaskStatusFailed},
		},
	})

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	lifecycle := requireAppMetricFamily(t, families, "wukongim_node_lifecycle_nodes")
	if got := findAppMetricByLabels(t, lifecycle, map[string]string{"join_state": "active", "status": "alive"}).GetGauge().GetValue(); got != 1 {
		t.Fatalf("active alive lifecycle nodes = %v, want 1", got)
	}
	freshness := requireAppMetricFamily(t, families, "wukongim_node_health_freshness_nodes")
	if got := findAppMetricByLabels(t, freshness, map[string]string{"freshness": "stale", "status": "alive"}).GetGauge().GetValue(); got != 1 {
		t.Fatalf("stale alive health nodes = %v, want 1", got)
	}
	age := requireAppMetricFamily(t, families, "wukongim_node_health_report_age_seconds")
	if got := findAppMetricByLabels(t, age, map[string]string{"freshness": "stale", "status": "alive"}).GetGauge().GetValue(); got != 31 {
		t.Fatalf("stale alive health age = %v, want 31", got)
	}
	tasks := requireAppMetricFamily(t, families, "wukongim_node_onboarding_tasks")
	if got := findAppMetricByLabels(t, tasks, map[string]string{"state": "running"}).GetGauge().GetValue(); got != 1 {
		t.Fatalf("running onboarding tasks = %v, want 1", got)
	}
}
```

- [ ] **Step 2: Run the test and verify it fails**

Run:

```bash
GOWORK=off go test ./internalv2/app -run TestControlSnapshotMetricsObserverMapsNodeLifecycleHealth -count=1
```

Expected: FAIL because observer does not write Stage 9 metrics.

- [ ] **Step 3: Add snapshot aggregation helpers**

In `internalv2/app/observability.go`, add helpers:

```go
func controllerLifecycleCounts(nodes []control.Node) map[obsmetrics.NodeLifecycleKey]int {
	counts := make(map[obsmetrics.NodeLifecycleKey]int)
	for _, node := range nodes {
		key := obsmetrics.NodeLifecycleKey{
			JoinState: string(managerControlJoinState(node.JoinState)),
			Status:    string(node.Status),
		}
		counts[key]++
	}
	return counts
}

func controllerHealthFreshnessCounts(nodes []control.Node) (map[obsmetrics.NodeHealthFreshnessKey]int, map[obsmetrics.NodeHealthFreshnessKey]float64) {
	counts := make(map[obsmetrics.NodeHealthFreshnessKey]int)
	ages := make(map[obsmetrics.NodeHealthFreshnessKey]float64)
	for _, node := range nodes {
		key := obsmetrics.NodeHealthFreshnessKey{
			Freshness: string(node.Health.Freshness),
			Status:    string(node.Health.Status),
		}
		if key.Freshness == "" {
			key.Freshness = string(control.NodeHealthMissing)
		}
		if key.Status == "" {
			key.Status = string(node.Status)
		}
		counts[key]++
		age := node.Health.ReportAge.Seconds()
		if age > ages[key] {
			ages[key] = age
		}
	}
	return counts, ages
}

func controllerOnboardingTaskCounts(tasks []control.ReconcileTask) map[string]int {
	counts := map[string]int{"pending": 0, "running": 0, "failed": 0}
	for _, task := range tasks {
		if task.Kind != control.TaskKindSlotReplicaMove {
			continue
		}
		counts[string(task.Status)]++
	}
	return counts
}
```

Use existing local naming helpers if present.

- [ ] **Step 4: Wire observer**

Extend `ObserveControlSnapshot`:

```go
o.metrics.NodeLifecycle.SetDiscoveryMembershipRevision(snapshot.Revision)
o.metrics.NodeLifecycle.SetLifecycleNodes(controllerLifecycleCounts(snapshot.Nodes))
freshnessCounts, ageMax := controllerHealthFreshnessCounts(snapshot.Nodes)
o.metrics.NodeLifecycle.SetHealthFreshnessNodes(freshnessCounts)
o.metrics.NodeLifecycle.SetHealthReportAgeMax(ageMax)
o.metrics.NodeLifecycle.SetOnboardingTasks(controllerOnboardingTaskCounts(snapshot.Tasks))
```

- [ ] **Step 5: Run observer tests**

Run:

```bash
GOWORK=off go test ./internalv2/app -run 'ControlSnapshotMetricsObserverMapsNodeLifecycleHealth|ControlSnapshotMetricsObserverMapsControllerHealth' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internalv2/app/observability.go internalv2/app/observability_test.go
git commit -m "feat: publish dynamic node lifecycle metrics"
```

---

### Task 3: Add Manager Node Health Fields

**Files:**
- Modify: `internalv2/usecase/management/nodes.go`
- Modify: `internalv2/usecase/management/nodes_test.go`
- Modify: `internalv2/access/manager/nodes.go`
- Modify: `internalv2/access/manager/server_test.go`

- [ ] **Step 1: Write failing management read-model test**

Add to `internalv2/usecase/management/nodes_test.go`:

```go
func TestListNodesIncludesHealthFreshnessFields(t *testing.T) {
	app := newNodesTestApp(t)
	app.snapshot.Nodes = []control.Node{{
		NodeID:    4,
		Addr:      "127.0.0.1:7004",
		Roles:     []control.Role{control.RoleData},
		JoinState: control.NodeJoinStateActive,
		Status:    control.NodeAlive,
		Health: control.NodeHealth{
			Status:                  control.NodeAlive,
			Freshness:               control.NodeHealthFresh,
			RuntimeReady:            true,
			ObservedControlRevision: 12,
			ObservedSlotRevision:    21,
			ReportAge:               4 * time.Second,
			ReportTTL:               30 * time.Second,
		},
	}}
	resp, err := app.ListNodes(context.Background(), ListNodesRequest{})
	if err != nil {
		t.Fatalf("ListNodes() error = %v", err)
	}
	node := resp.Nodes[0]
	if !node.Health.Fresh || node.Health.Freshness != "fresh" || node.Health.ReportAgeMS != 4000 {
		t.Fatalf("node health = %#v, want fresh health fields", node.Health)
	}
	if !node.Membership.Schedulable {
		t.Fatalf("membership = %#v, want schedulable from shared health predicate", node.Membership)
	}
}
```

- [ ] **Step 2: Run usecase test and verify it fails**

Run:

```bash
GOWORK=off go test ./internalv2/usecase/management -run TestListNodesIncludesHealthFreshnessFields -count=1
```

Expected: compile fails because `NodeHealth` fields are missing.

- [ ] **Step 3: Extend usecase `NodeHealth`**

Add fields:

```go
// Fresh reports whether the latest health report is fresh.
Fresh bool
// Freshness is fresh, stale, or missing.
Freshness string
// RuntimeReady reports whether the node can serve foreground traffic.
RuntimeReady bool
// ReportAgeMS is the current health report age in milliseconds.
ReportAgeMS int64
// ReportTTLMS is the configured freshness TTL in milliseconds.
ReportTTLMS int64
// ObservedControlRevision is the reported ControllerV2 logical revision.
ObservedControlRevision uint64
// ObservedSlotRevision is the reported local Slot observation revision.
ObservedSlotRevision uint64
// ErrorCode is a bounded machine-readable runtime reason.
ErrorCode string
```

In `buildNode`, compute:

```go
health := opts.node.Health
schedulable := control.NodeSchedulableForPlacement(opts.node)
lastHeartbeatAt := health.ReportedAt
if lastHeartbeatAt.IsZero() {
	lastHeartbeatAt = opts.generatedAt
}
```

Map:

```go
Health: NodeHealth{
	Status:                  managerNodeStatus(health.Status),
	LastHeartbeatAt:         lastHeartbeatAt,
	Fresh:                   health.Freshness == control.NodeHealthFresh,
	Freshness:               string(health.Freshness),
	RuntimeReady:            health.RuntimeReady,
	ReportAgeMS:             health.ReportAge.Milliseconds(),
	ReportTTLMS:             health.ReportTTL.Milliseconds(),
	ObservedControlRevision: health.ObservedControlRevision,
	ObservedSlotRevision:    health.ObservedSlotRevision,
	ErrorCode:               health.ErrorCode,
},
```

- [ ] **Step 4: Extend manager DTO and mapping**

In `internalv2/access/manager/nodes.go`, add fields to `NodeHealthDTO`:

```go
Fresh                   bool   `json:"fresh"`
Freshness               string `json:"freshness"`
RuntimeReady            bool   `json:"runtime_ready"`
ReportAgeMS             int64  `json:"report_age_ms"`
ReportTTLMS             int64  `json:"report_ttl_ms"`
ObservedControlRevision uint64 `json:"observed_control_revision"`
ObservedSlotRevision    uint64 `json:"observed_slot_revision"`
ErrorCode               string `json:"error_code,omitempty"`
```

Map them in the DTO conversion.

- [ ] **Step 5: Run manager node tests**

Run:

```bash
GOWORK=off go test ./internalv2/usecase/management ./internalv2/access/manager -run 'ListNodes|NodeHealth|HealthFreshness' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internalv2/usecase/management internalv2/access/manager
git commit -m "feat: expose node health freshness in manager nodes"
```

---

### Task 4: Count Scale-In Blocker Reasons

**Files:**
- Modify: `internalv2/app/observability.go`
- Modify: `internalv2/app/observability_test.go`
- Modify: `internalv2/usecase/management/scale_in.go`

- [ ] **Step 1: Write failing blocker metric test**

Add an app-level test that calls a small observer method directly:

```go
func TestNodeLifecycleMetricsObserverCountsScaleInBlockers(t *testing.T) {
	reg := obsmetrics.New(1, "n1")
	observer := nodeLifecycleMetricsObserver{metrics: reg}

	observer.ObserveScaleInStatus(managementusecase.NodeScaleInStatusResponse{
		BlockedReasons: []string{"target_health_stale", "eligible_node_health_revision_stale"},
	})

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	blockers := requireAppMetricFamily(t, families, "wukongim_node_scale_in_blockers_total")
	if got := findAppMetricByLabels(t, blockers, map[string]string{"reason": "target_health_stale"}).GetCounter().GetValue(); got != 1 {
		t.Fatalf("target_health_stale blockers = %v, want 1", got)
	}
}
```

- [ ] **Step 2: Run the test and verify it fails**

Run:

```bash
GOWORK=off go test ./internalv2/app -run TestNodeLifecycleMetricsObserverCountsScaleInBlockers -count=1
```

Expected: compile fails because `nodeLifecycleMetricsObserver` does not exist.

- [ ] **Step 3: Add observer and wire into management where practical**

Add a small app observer:

```go
type nodeLifecycleMetricsObserver struct {
	metrics *obsmetrics.Registry
}

func (o nodeLifecycleMetricsObserver) ObserveScaleInStatus(status managementusecase.NodeScaleInStatusResponse) {
	if o.metrics == nil || o.metrics.NodeLifecycle == nil {
		return
	}
	for _, reason := range status.BlockedReasons {
		o.metrics.NodeLifecycle.ObserveScaleInBlocker(reason)
	}
}
```

If management app already has an observer hook for status generation, wire this observer there. If it does not, add an optional callback to management app options:

```go
type ScaleInStatusObserver interface {
	ObserveScaleInStatus(NodeScaleInStatusResponse)
}
```

Call it after `NodeScaleInStatus` constructs the response, before returning.

- [ ] **Step 4: Run blocker metric tests**

Run:

```bash
GOWORK=off go test ./internalv2/app ./internalv2/usecase/management -run 'ScaleInBlockers|ScaleInStatus' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internalv2/app internalv2/usecase/management
git commit -m "feat: count dynamic node scale-in blockers"
```

---

### Task 5: Update FLOW Docs And Run Stage Gate

**Files:**
- Modify: `internalv2/app/FLOW.md`
- Modify: `internalv2/usecase/management/FLOW.md`
- Modify: `internalv2/access/manager/FLOW.md`
- Modify: `docs/superpowers/plans/2026-06-29-internalv2-dynamic-node-stage9-production-readiness.md`

- [ ] **Step 1: Update FLOW docs**

Add these points:

```markdown
- `controlSnapshotMetricsObserver` maps current control snapshots into
  lifecycle, health freshness, onboarding task, and membership revision metrics.
- Manager node list health uses `control.Node.Health`; `membership.schedulable`
  must match `control.NodeSchedulableForPlacement`.
- Scale-in status emits bounded `blocked_reasons`; metrics aggregate by reason
  only and never include task, channel, UID, or address labels.
```

- [ ] **Step 2: Run the Stage 9C gate**

Run:

```bash
GOWORK=off go test ./pkg/metrics ./internalv2/usecase/management ./internalv2/access/manager ./internalv2/app -run 'Lifecycle|Health|Metrics|Manager|ScaleIn' -count=1
git diff --check
```

Expected: PASS and no whitespace errors.

- [ ] **Step 3: Commit docs and gate update**

```bash
git add internalv2/app/FLOW.md internalv2/usecase/management/FLOW.md internalv2/access/manager/FLOW.md docs/superpowers/plans/2026-06-29-internalv2-dynamic-node-stage9-production-readiness.md
git commit -m "docs: document dynamic node readiness observability"
```
