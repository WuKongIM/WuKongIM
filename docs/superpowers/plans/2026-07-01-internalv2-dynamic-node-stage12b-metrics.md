# InternalV2 Dynamic Node Stage 12B Metrics Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add low-cardinality metrics that make dynamic-node Slot replica move blockers visible without adding high-cardinality labels or changing task semantics.

**Architecture:** Extend existing `pkg/metrics` Controller and NodeLifecycle metrics. Use control snapshots for active/failed task counts, task audit retained snapshots for task age, and a narrow `pkg/clusterv2/tasks` observer for local Slot replica move phase observations. Do not infer task age from Controller state `UpdatedAt`.

**Tech Stack:** Go, Prometheus client, `pkg/metrics`, `internalv2/app` observers, ControllerV2 task audit runtime, `pkg/clusterv2/tasks` Slot replica move executor.

---

## Prerequisites

Read before editing:

- `docs/superpowers/specs/2026-07-01-internalv2-dynamic-node-stage12-diagnostics-design.md`
- `pkg/metrics/controller.go`
- `pkg/metrics/node_lifecycle.go`
- `pkg/metrics/registry_test.go`
- `internalv2/app/observability.go`
- `internalv2/app/task_audit.go`
- `internalv2/observability/taskaudit/model.go`
- `internalv2/observability/taskaudit/retention.go`
- `pkg/clusterv2/tasks/slot_replica_move.go`
- `pkg/clusterv2/default_slots.go`

## Files

- Modify: `pkg/metrics/controller.go`
- Modify: `pkg/metrics/node_lifecycle.go`
- Modify: `pkg/metrics/registry_test.go`
- Modify: `internalv2/observability/taskaudit/model.go`
- Modify: `internalv2/observability/taskaudit/retention.go`
- Modify: `internalv2/observability/taskaudit/store_test.go`
- Modify: `internalv2/app/task_audit.go`
- Modify: `internalv2/app/task_audit_test.go`
- Modify: `internalv2/app/observability.go`
- Modify: `internalv2/app/observability_test.go`
- Modify: `pkg/clusterv2/tasks/slot_replica_move.go`
- Modify: `pkg/clusterv2/tasks/slot_replica_move_test.go`
- Modify: `pkg/clusterv2/default_slots.go`
- Modify: `pkg/clusterv2/node.go` or existing node construction files only if required to pass the observer into the executor.

## Metric Contract

Add or complete these families:

```text
wukongim_controller_tasks_active{type="slot_replica_move"}
wukongim_controller_tasks_failed{type="slot_replica_move"}
wukongim_controller_task_oldest_age_seconds{kind,status,step,source}
wukongim_slot_replica_move_phase_observed_total{step,result}
wukongim_slot_replica_move_phase_duration_seconds{step,result}
```

Bounded labels:

```text
kind: bootstrap | leader_transfer | slot_replica_move | other
status: pending | running | failed | completed | other | unknown
step: create_slot | transfer_leader | open_learner | add_learner | promote_learner | remove_voter | commit_assignment | other | unknown
source: audit | unknown
result: ok | fail | deferred | timeout | conflict | other
```

No metric may label by task ID, slot ID, node ID beyond the registry const label,
node address, UID, channel ID, session ID, or raw error string.

## Task 1: Include `slot_replica_move` In Controller Task Gauges

**Files:**
- Modify: `pkg/metrics/controller.go`
- Modify: `pkg/metrics/registry_test.go`
- Verify: `internalv2/app/observability.go`

- [ ] **Step 1: Write failing metrics test**

Add to `pkg/metrics/registry_test.go`:

```go
func TestControllerMetricsIncludeSlotReplicaMoveTasks(t *testing.T) {
	reg := New(1, "node-1")

	reg.Controller.SetTaskActive(map[string]int{"slot_replica_move": 2})
	reg.Controller.SetTaskFailed(map[string]int{"slot_replica_move": 1})

	families, err := reg.Gather()
	require.NoError(t, err)

	active := requireMetricFamily(t, families, "wukongim_controller_tasks_active")
	require.Equal(t, float64(2), findMetricByLabels(t, active, map[string]string{
		"node_id": "1", "node_name": "node-1", "type": "slot_replica_move",
	}).GetGauge().GetValue())

	failed := requireMetricFamily(t, families, "wukongim_controller_tasks_failed")
	require.Equal(t, float64(1), findMetricByLabels(t, failed, map[string]string{
		"node_id": "1", "node_name": "node-1", "type": "slot_replica_move",
	}).GetGauge().GetValue())
}
```

- [ ] **Step 2: Run RED test**

Run:

```bash
GOWORK=off go test ./pkg/metrics -run TestControllerMetricsIncludeSlotReplicaMoveTasks -count=1
```

Expected: fail because the family does not expose `slot_replica_move`.

- [ ] **Step 3: Extend task kind whitelist**

Modify `pkg/metrics/controller.go`:

```go
controllerTaskKinds = []string{"bootstrap", "leader_transfer", "slot_replica_move", "repair", "rebalance"}
```

Keep existing labels stable; only add the new kind.

- [ ] **Step 4: Run GREEN test**

Run:

```bash
GOWORK=off go test ./pkg/metrics -run 'ControllerMetrics.*Task|ControllerMetricsInclude' -count=1
```

Expected: PASS.

## Task 2: Add Task Audit Step To Retained Snapshots

**Files:**
- Modify: `internalv2/observability/taskaudit/model.go`
- Modify: `internalv2/observability/taskaudit/retention.go`
- Modify: `internalv2/observability/taskaudit/store_test.go`
- Modify: `internalv2/app/task_audit.go`
- Modify: `internalv2/app/task_audit_test.go`
- Modify: `internalv2/usecase/management/task_audit.go`
- Modify: `internalv2/access/manager/controller_task_audits.go`

- [ ] **Step 1: Write failing task audit tests**

Extend `internalv2/observability/taskaudit/store_test.go` so a retained
snapshot stores the latest task step from event details:

```go
require.Equal(t, "remove_voter", resp.Items[0].Step)
```

Extend `internalv2/app/task_audit_test.go` so management reads expose:

```go
require.Equal(t, "remove_voter", list.Items[0].Step)
```

- [ ] **Step 2: Run RED tests**

Run:

```bash
GOWORK=off go test ./internalv2/observability/taskaudit ./internalv2/app -run 'TaskAudit.*Step|Store.*Step' -count=1
```

Expected: fail because `Snapshot.Step` and management DTO step fields do not exist.

- [ ] **Step 3: Add bounded step field**

Add `Step string` to:

- `taskaudit.Snapshot`
- `management.ControllerTaskAuditSnapshot`
- `manager.ManagerControllerTaskAuditSnapshot`

In `internalv2/observability/taskaudit/retention.go`, set `snapshot.Step` from
`event.Details["step"]` when it is a non-empty string. If no step exists, keep
it empty and let metric normalization map it to `unknown`.

- [ ] **Step 4: Run GREEN tests**

Run:

```bash
GOWORK=off go test ./internalv2/observability/taskaudit ./internalv2/app ./internalv2/usecase/management ./internalv2/access/manager -run 'TaskAudit|ControllerTaskAudit' -count=1
```

Expected: PASS.

## Task 3: Add Audit-Derived Oldest Task Age Metric

**Files:**
- Modify: `pkg/metrics/controller.go`
- Modify: `pkg/metrics/registry_test.go`
- Modify: `internalv2/app/task_audit.go`
- Modify: `internalv2/app/task_audit_test.go`

- [ ] **Step 1: Write failing metrics test**

Add to `pkg/metrics/registry_test.go`:

```go
func TestControllerMetricsTrackAuditDerivedOldestTaskAge(t *testing.T) {
	reg := New(1, "node-1")
	reg.Controller.SetTaskOldestAge(map[ControllerTaskAgeKey]float64{
		{Kind: "slot_replica_move", Status: "running", Step: "remove_voter", Source: "audit"}: 42,
		{Kind: "raw-task-id", Status: "raw-status", Step: "raw-step", Source: "audit"}:     9,
	})

	families, err := reg.Gather()
	require.NoError(t, err)

	age := requireMetricFamily(t, families, "wukongim_controller_task_oldest_age_seconds")
	require.Equal(t, float64(42), findMetricByLabels(t, age, map[string]string{
		"node_id": "1", "node_name": "node-1", "kind": "slot_replica_move", "status": "running", "step": "remove_voter", "source": "audit",
	}).GetGauge().GetValue())
	require.Equal(t, float64(9), findMetricByLabels(t, age, map[string]string{
		"node_id": "1", "node_name": "node-1", "kind": "other", "status": "other", "step": "other", "source": "audit",
	}).GetGauge().GetValue())
}
```

- [ ] **Step 2: Run RED test**

Run:

```bash
GOWORK=off go test ./pkg/metrics -run TestControllerMetricsTrackAuditDerivedOldestTaskAge -count=1
```

Expected: fail because `ControllerTaskAgeKey` and `SetTaskOldestAge` do not exist.

- [ ] **Step 3: Add metric to ControllerMetrics**

Add:

```go
type ControllerTaskAgeKey struct {
	Kind   string
	Status string
	Step   string
	Source string
}
```

Add gauge:

```text
wukongim_controller_task_oldest_age_seconds{kind,status,step,source}
```

Implement `SetTaskOldestAge(map[ControllerTaskAgeKey]float64)` with reset and
bounded label normalization.

- [ ] **Step 4: Wire task audit runtime to metrics**

In `internalv2/app/task_audit.go`, after retained snapshots are updated or when
management reads are served, compute oldest age from retained snapshots:

```text
now - snapshot.StartedAt
```

Use only snapshots with non-zero `StartedAt`. Use source `audit`. Do not infer
age from `cluster-state.json UpdatedAt`.

- [ ] **Step 5: Run GREEN tests**

Run:

```bash
GOWORK=off go test ./pkg/metrics ./internalv2/app -run 'OldestTaskAge|TaskAudit|ControllerMetrics' -count=1
```

Expected: PASS.

## Task 4: Add Slot Replica Move Phase Metrics

**Files:**
- Modify: `pkg/metrics/node_lifecycle.go`
- Modify: `pkg/metrics/registry_test.go`
- Modify: `pkg/clusterv2/tasks/slot_replica_move.go`
- Modify: `pkg/clusterv2/tasks/slot_replica_move_test.go`
- Modify: `pkg/clusterv2/default_slots.go`

- [ ] **Step 1: Write failing registry test**

Add to `pkg/metrics/registry_test.go`:

```go
func TestNodeLifecycleMetricsTrackSlotReplicaMovePhases(t *testing.T) {
	reg := New(1, "node-1")

	reg.NodeLifecycle.ObserveSlotReplicaMovePhase("remove_voter", "ok", 150*time.Millisecond)
	reg.NodeLifecycle.ObserveSlotReplicaMovePhase("raw-step", "raw-result", 90*time.Millisecond)

	families, err := reg.Gather()
	require.NoError(t, err)

	total := requireMetricFamily(t, families, "wukongim_slot_replica_move_phase_observed_total")
	require.Equal(t, float64(1), findMetricByLabels(t, total, map[string]string{
		"node_id": "1", "node_name": "node-1", "step": "remove_voter", "result": "ok",
	}).GetCounter().GetValue())
	require.Equal(t, float64(1), findMetricByLabels(t, total, map[string]string{
		"node_id": "1", "node_name": "node-1", "step": "other", "result": "other",
	}).GetCounter().GetValue())

	requireMetricFamily(t, families, "wukongim_slot_replica_move_phase_duration_seconds")
}
```

- [ ] **Step 2: Run RED registry test**

Run:

```bash
GOWORK=off go test ./pkg/metrics -run TestNodeLifecycleMetricsTrackSlotReplicaMovePhases -count=1
```

Expected: fail because the phase methods and metric families do not exist.

- [ ] **Step 3: Add metrics method**

Add to `NodeLifecycleMetrics`:

```go
func (m *NodeLifecycleMetrics) ObserveSlotReplicaMovePhase(step, result string, d time.Duration)
```

The method increments total and observes duration. Negative durations become
zero.

- [ ] **Step 4: Add executor observer**

Add to `pkg/clusterv2/tasks/slot_replica_move.go`:

```go
type SlotReplicaMoveObserver interface {
	ObserveSlotReplicaMovePhase(step string, result string, d time.Duration)
}
```

Add `Observer SlotReplicaMoveObserver` to `SlotReplicaMoveExecutorConfig`.

Instrument these executor phases:

```text
open_learner
add_learner
promote_learner
transfer_leader
remove_voter
commit_assignment
```

Result rules:

- `ok`: the executor submitted or completed the phase without error.
- `fail`: the executor converted an error into `FailTask`.
- `deferred`: the executor observed that the task cannot advance yet and returned nil.
- `timeout`: local status observation timed out and the task was failed with a timeout reason.

- [ ] **Step 5: Write executor observer tests**

Add tests to `pkg/clusterv2/tasks/slot_replica_move_test.go`:

```go
func TestSlotReplicaMoveExecutorObservesRemoveVoterPhase(t *testing.T)
func TestSlotReplicaMoveExecutorObservesDeferredLeadershipTransfer(t *testing.T)
func TestSlotReplicaMoveExecutorObservesFailedPhase(t *testing.T)
```

Each test uses a fake observer that records `step`, `result`, and positive
duration. Assert no task IDs or Slot IDs are passed to the observer.

- [ ] **Step 6: Wire observer from clusterv2 default construction**

Pass the app metrics-backed observer into the Slot replica move executor where
`NewSlotReplicaMoveExecutor` is created. If the existing construction path does
not expose metrics there, add a narrow option rather than importing
`pkg/metrics` into `pkg/clusterv2/tasks`.

- [ ] **Step 7: Run GREEN tests**

Run:

```bash
GOWORK=off go test ./pkg/metrics -run 'NodeLifecycleMetricsTrackSlotReplicaMove|ControllerMetrics' -count=1
GOWORK=off go test ./pkg/clusterv2/tasks -run 'SlotReplicaMove.*Observe|SlotReplicaMove' -count=1
GOWORK=off go test ./pkg/clusterv2 -run 'SlotReplicaMove|DefaultSlots|Metrics' -count=1
```

Expected: PASS.

## Stage 12B Verification

Run:

```bash
GOWORK=off go test ./pkg/metrics -run 'ControllerTask|NodeLifecycle|SlotReplicaMove|Registry' -count=1
GOWORK=off go test ./internalv2/app -run 'ControlSnapshot|TaskAudit|NodeLifecycle|Metrics' -count=1
GOWORK=off go test ./pkg/clusterv2/tasks -run 'SlotReplicaMove.*Metric|SlotReplicaMove' -count=1
GOWORK=off go test ./pkg/clusterv2 ./pkg/controllerv2 -run 'Task|SlotReplicaMove|Metrics' -count=1
git diff --check
```

Expected: all commands pass.

## Commit

```bash
git add pkg/metrics internalv2/observability/taskaudit internalv2/app internalv2/usecase/management internalv2/access/manager pkg/clusterv2
git commit -m "feat: expose dynamic node task metrics"
```
