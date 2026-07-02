# InternalV2 Dynamic Node Stage 12C E2EV2 And Runbook Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make dynamic-node e2ev2 failures and delayed Slot replica move scenarios produce actionable diagnostic artifacts, then update the readiness gate and runbook to use those artifacts.

**Architecture:** Add e2ev2 helper code that calls only public manager HTTP, public `wkcli node diagnose`, and `/metrics`. Wire the helper into dynamic-node operations and selected gofail delay scenarios. Extend `scripts/e2ev2/dynamic-node-readiness-gate.sh --profile ops` so the evidence directory links diagnostic bundles without changing the correctness tests themselves.

**Tech Stack:** Go e2ev2 harness, `test/e2ev2/suite`, `cmd/wkcli`, public manager HTTP, Prometheus text snapshots, Bash readiness gate, dynamic-node runbook.

---

## Prerequisites

Read before editing:

- `docs/superpowers/specs/2026-07-01-internalv2-dynamic-node-stage12-diagnostics-design.md`
- `docs/superpowers/plans/2026-07-01-internalv2-dynamic-node-stage12a-diagnostic-surface.md`
- `docs/superpowers/plans/2026-07-01-internalv2-dynamic-node-stage12b-metrics.md`
- `test/e2ev2/suite/manager_client.go`
- `test/e2ev2/suite/metrics.go`
- `test/e2ev2/cluster/dynamic_node_operations/ops_cli_test.go`
- `test/e2ev2/cluster/dynamic_node_faults/scale_in_slot_drain_fault_test.go`
- `test/e2ev2/cluster/dynamic_node_faults/slot_replica_move_fault_test.go`
- `scripts/e2ev2/dynamic-node-readiness-gate.sh`
- `scripts/dynamic_node_readiness_gate_script_test.go`
- `docs/superpowers/runbooks/internalv2-dynamic-node-operations.md`

## Files

- Modify: `test/e2ev2/suite/manager_client.go`
- Create: `test/e2ev2/suite/dynamic_node_diagnostics.go`
- Create: `test/e2ev2/suite/dynamic_node_diagnostics_test.go`
- Modify: `test/e2ev2/cluster/dynamic_node_operations/ops_cli_test.go`
- Modify: `test/e2ev2/cluster/dynamic_node_faults/scale_in_slot_drain_fault_test.go`
- Modify: `test/e2ev2/cluster/dynamic_node_faults/slot_replica_move_fault_test.go`
- Modify: `scripts/e2ev2/dynamic-node-readiness-gate.sh`
- Modify: `scripts/dynamic_node_readiness_gate_script_test.go`
- Modify: `test/e2ev2/cluster/dynamic_node_operations/AGENTS.md`
- Modify: `test/e2ev2/cluster/dynamic_node_faults/AGENTS.md`
- Modify: `docs/superpowers/runbooks/internalv2-dynamic-node-operations.md`

## Artifact Contract

Each dynamic-node diagnostic bundle lives under a test-controlled directory:

```text
dynamic-node-diagnostics/
  node-4-manager-diagnostics.json
  node-4-wkcli-diagnose.json
  node-1-metrics.prom
  node-2-metrics.prom
  node-3-metrics.prom
  node-4-metrics.prom
  cluster-diagnostics.txt
```

The helper must be best-effort:

- if one artifact fails, write a `*.error.txt` file for that artifact;
- do not hide the original test assertion failure;
- never call internal Go packages or private runtime APIs;
- never require metrics when metrics are disabled, but record the scrape error.

## Task 1: Add Suite Diagnostic Bundle Helper

**Files:**
- Create: `test/e2ev2/suite/dynamic_node_diagnostics.go`
- Create: `test/e2ev2/suite/dynamic_node_diagnostics_test.go`
- Modify: `test/e2ev2/suite/manager_client.go`

- [ ] **Step 1: Write failing helper tests**

Add tests:

```go
func TestDynamicNodeDiagnosticBundleWritesManagerWKCLIMetricsAndClusterFiles(t *testing.T)
func TestDynamicNodeDiagnosticBundleWritesErrorFiles(t *testing.T)
func TestManagerClientDynamicNodeDiagnosticsCallsPublicRoute(t *testing.T)
```

The manager route test must assert:

```text
GET /manager/nodes/4/diagnostics
```

The bundle test must assert file names:

```text
node-4-manager-diagnostics.json
node-4-wkcli-diagnose.json
node-1-metrics.prom
cluster-diagnostics.txt
```

- [ ] **Step 2: Run RED tests**

Run:

```bash
GOWORK=off go test ./test/e2ev2/suite -run DynamicNodeDiagnostic -count=1
```

Expected: fail because helper types and route client do not exist.

- [ ] **Step 3: Add manager client method**

Add to `test/e2ev2/suite/manager_client.go`:

```go
func (m *ManagerClient) DynamicNodeDiagnosticsRaw(ctx context.Context, nodeID uint64) ([]byte, error)
```

The method must call:

```text
/manager/nodes/<node_id>/diagnostics
```

- [ ] **Step 4: Add bundle helper**

Create `test/e2ev2/suite/dynamic_node_diagnostics.go`:

```go
type DynamicNodeDiagnosticBundleOptions struct {
	NodeID     uint64
	Manager   *ManagerClient
	ContextDir string
	WKCLIArgs  []string
	OutDir     string
}

func DumpDynamicNodeDiagnostics(t testing.TB, cluster *StartedCluster, opts DynamicNodeDiagnosticBundleOptions) string
```

Behavior:

- create `opts.OutDir` when set, otherwise `t.TempDir()/dynamic-node-diagnostics`;
- write manager diagnostics JSON;
- run `go run ./cmd/wkcli node diagnose <node_id> --json` with the provided
  context dir and manager context args;
- scrape `/metrics` from every running node with manager/API metrics enabled;
- write `cluster.DumpDiagnostics()` to `cluster-diagnostics.txt`;
- return the output directory path.

- [ ] **Step 5: Run GREEN suite tests**

Run:

```bash
GOWORK=off go test ./test/e2ev2/suite -run DynamicNodeDiagnostic -count=1
```

Expected: PASS.

## Task 2: Wire Diagnostics Into Operations E2EV2

**Files:**
- Modify: `test/e2ev2/cluster/dynamic_node_operations/ops_cli_test.go`
- Modify: `test/e2ev2/cluster/dynamic_node_operations/AGENTS.md`

- [ ] **Step 1: Write failing operation assertions**

In `TestWKCLINodeOperationsLifecycleWithTraffic`, after the first scale-in
advance and before waiting for final safety, call the bundle helper for node 4.
Assert the returned directory contains:

```text
node-4-manager-diagnostics.json
node-4-wkcli-diagnose.json
cluster-diagnostics.txt
```

Assert the manager diagnostics JSON includes:

```text
"node_id": 4
"active_tasks"
"summary"
"recommended_next_action"
```

- [ ] **Step 2: Run RED focused e2e**

Run:

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_operations -run TestWKCLINodeOperationsLifecycleWithTraffic -count=1 -timeout 12m -p=1
```

Expected: fail until Stage 12A route and helper exist.

- [ ] **Step 3: Add diagnostic dump call**

Use the same `contextDir` created for `wkcli context add ops`. Pass:

```go
suite.DynamicNodeDiagnosticBundleOptions{
	NodeID:     4,
	Manager:    manager,
	ContextDir: contextDir,
	WKCLIArgs:  []string{"--context", "ops"},
	OutDir:     filepath.Join(t.TempDir(), "dynamic-node-diagnostics"),
}
```

On assertion failures, include the bundle path in the failure message.

- [ ] **Step 4: Run GREEN focused e2e**

Run:

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_operations -run TestWKCLINodeOperationsLifecycleWithTraffic -count=1 -timeout 12m -p=1
```

Expected: PASS.

## Task 3: Assert Diagnostics During Gofail Delay Windows

**Files:**
- Modify: `test/e2ev2/cluster/dynamic_node_faults/scale_in_slot_drain_fault_test.go`
- Modify: `test/e2ev2/cluster/dynamic_node_faults/slot_replica_move_fault_test.go`
- Modify: `test/e2ev2/cluster/dynamic_node_faults/AGENTS.md`

- [ ] **Step 1: Add delayed scale-in diagnostic assertion**

In `TestScaleInSlotDrainSurvivesDelayedLeaderTransfer`, after
`waitGofailCountWhileScaleInBlocked`, collect diagnostics and assert:

```text
summary.safe_to_remove=false
summary.recommended_next_action in ["inspect_controller_task","inspect_slot_runtime"]
active_tasks includes kind=slot_replica_move
active_tasks includes phase_index
active_tasks includes observed_voters
```

This proves the delay window is explainable while the task is still active.

- [ ] **Step 2: Add delayed onboarding diagnostic assertion**

In the delayed onboarding Slot replica move scenario, collect diagnostics for
node 4 while the failpoint is enabled and assert:

```text
onboarding.summary.total_active > 0
active_tasks includes target_node=4
task_audits contains slot_replica_move or task_audit source warning is explicit
```

- [ ] **Step 3: Run opt-in focused gofail tests**

Run:

```bash
scripts/build-gofail-binary.sh --cmd ./cmd/wukongimv2 --package internalv2/usecase/management --package pkg/controllerv2 --package pkg/clusterv2/tasks --package pkg/clusterv2/net --out /tmp/wukongimv2-gofail
WK_E2EV2_BINARY=/tmp/wukongimv2-gofail WK_E2EV2_GOFAIL_DYNAMIC_NODE=1 GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_faults -run 'TestSlotReplicaMoveSurvivesDelayedLeaderTransfer|TestScaleInSlotDrainSurvivesDelayedLeaderTransfer' -count=1 -timeout 15m -p=1
```

Expected: PASS.

## Task 4: Extend Readiness Gate Evidence

**Files:**
- Modify: `scripts/e2ev2/dynamic-node-readiness-gate.sh`
- Modify: `scripts/dynamic_node_readiness_gate_script_test.go`

- [ ] **Step 1: Write failing script tests**

Add dry-run expectations for ops profile:

```text
diagnostics_artifact_glob=data/dynamic-node-readiness-gate/<timestamp>/**/dynamic-node-diagnostics
```

Add a fake successful Stage11 ops command that creates:

```text
dynamic-node-diagnostics/node-4-manager-diagnostics.json
```

Assert `summary.md` links the diagnostic artifact path.

- [ ] **Step 2: Run RED script tests**

Run:

```bash
GOWORK=off go test ./scripts -run DynamicNodeReadinessGate -count=1
```

Expected: fail until script support is added.

- [ ] **Step 3: Add artifact collection**

Extend the gate script so `--profile ops`:

- preserves Stage11 ops logs as today;
- finds `dynamic-node-diagnostics` directories under the e2e temp/artifact
  root when the test prints or exports a path;
- copies or links them under:

```text
$OUT_DIR/diagnostics/
```

- appends a `## Diagnostics` section to `summary.md`.

Keep this best-effort. Missing diagnostic artifacts should add a warning but
must not fail a green correctness run.

- [ ] **Step 4: Run GREEN script tests**

Run:

```bash
GOWORK=off go test ./scripts -run DynamicNodeReadinessGate -count=1
scripts/e2ev2/dynamic-node-readiness-gate.sh --profile ops --dry-run
```

Expected: PASS.

## Task 5: Update Runbook Triage Tree

**Files:**
- Modify: `docs/superpowers/runbooks/internalv2-dynamic-node-operations.md`

- [ ] **Step 1: Add the diagnostic command**

Add:

```bash
go run ./cmd/wkcli node diagnose 4 --context prod-a
go run ./cmd/wkcli node diagnose 4 --context prod-a --json
```

- [ ] **Step 2: Add blocker decision tree**

Document:

```text
blocked_by_control_revision -> compare required/observed revision, inspect node health and readyz
blocked_by_tasks -> inspect active task step, phase_index, observed_voters/learners, audit last_reason
blocked_by_slots -> run plan/advance only when no active conflicting task exists
blocked_by_slot_runtime -> compare desired peers with live voters/learners and leader
unknown_runtime -> check process, readyz, metrics scrape, manager node health
unknown_channel_inventory -> do not remove; retry inventory read, collect channel status
```

- [ ] **Step 3: Run docs check**

Run:

```bash
git diff --check
```

Expected: PASS.

## Stage 12C Verification

Run:

```bash
GOWORK=off go test ./test/e2ev2/suite -run DynamicNodeDiagnostic -count=1
GOWORK=off go test ./scripts -run DynamicNodeReadinessGate -count=1
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_operations -run TestWKCLINodeOperationsLifecycleWithTraffic -count=1 -timeout 12m -p=1
scripts/e2ev2/dynamic-node-readiness-gate.sh --profile ops
git diff --check
```

Optional opt-in gofail evidence:

```bash
scripts/build-gofail-binary.sh --cmd ./cmd/wukongimv2 --package internalv2/usecase/management --package pkg/controllerv2 --package pkg/clusterv2/tasks --package pkg/clusterv2/net --out /tmp/wukongimv2-gofail
WK_E2EV2_BINARY=/tmp/wukongimv2-gofail WK_E2EV2_GOFAIL_DYNAMIC_NODE=1 GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_faults -run 'TestSlotReplicaMoveSurvivesDelayedLeaderTransfer|TestScaleInSlotDrainSurvivesDelayedLeaderTransfer|TestLeavingNodeRestartDuringScaleInSlotDrainRecovers' -count=1 -timeout 15m -p=1
```

Expected: all required commands pass; optional gofail evidence should pass
before merging if Stage 12B touched Slot replica move executor instrumentation.

## Commit

```bash
git add test/e2ev2 scripts docs/superpowers/runbooks/internalv2-dynamic-node-operations.md
git commit -m "test: collect dynamic node diagnostics evidence"
```
