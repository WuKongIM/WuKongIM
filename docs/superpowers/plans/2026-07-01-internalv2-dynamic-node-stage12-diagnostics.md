# InternalV2 Dynamic Node Stage 12 Diagnostics Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add bounded dynamic-node diagnostics so slow Slot replica movement, Controller task blockers, and control revision lag can be explained from manager HTTP, `wkcli`, metrics, e2ev2 evidence, and the runbook.

**Architecture:** Keep lifecycle and placement semantics unchanged. Add read-only diagnostic aggregation in `internalv2/usecase/management` and `internalv2/access/manager`, expose it through a manager-only `wkcli node diagnose` command, extend existing low-cardinality metrics, and make e2ev2 gates dump the diagnostic bundle automatically.

**Tech Stack:** Go, Gin manager HTTP, internalv2 management usecase, ControllerV2 task audit, `cmd/wkcli`, Prometheus metrics, e2ev2 real-process harness, Bash readiness gate, `docs/superpowers`.

---

## Source Links

- Design spec: `docs/superpowers/specs/2026-07-01-internalv2-dynamic-node-stage12-diagnostics-design.md`
- Dynamic-node master plan: `docs/superpowers/plans/2026-06-24-internalv2-dynamic-node-lifecycle.md`
- Stage 11 prerequisite: `docs/superpowers/plans/2026-06-30-internalv2-dynamic-node-stage11-operations-productization.md`
- Stage 12A plan: `docs/superpowers/plans/2026-07-01-internalv2-dynamic-node-stage12a-diagnostic-surface.md`
- Stage 12B plan: `docs/superpowers/plans/2026-07-01-internalv2-dynamic-node-stage12b-metrics.md`
- Stage 12C plan: `docs/superpowers/plans/2026-07-01-internalv2-dynamic-node-stage12c-e2ev2-runbook.md`

## Entry Gate

Run before starting Stage 12A:

```bash
GOWORK=off go test ./cmd/wkcli ./cmd/wkcli/internal/... -count=1
GOWORK=off go test ./scripts -run DynamicNodeReadinessGate -count=1
scripts/e2ev2/dynamic-node-readiness-gate.sh --profile ops
git diff --check
```

Expected:

- Stage 11 CLI and readiness gate remain green.
- The working tree contains only intentional Stage 12 documentation before code work starts.

## Execution Order

| Order | Stage | Plan | Output |
| --- | --- | --- | --- |
| 1 | Stage 12A Diagnostic Surface | `2026-07-01-internalv2-dynamic-node-stage12a-diagnostic-surface.md` | `GET /manager/nodes/:node_id/diagnostics` and `wkcli node diagnose` |
| 2 | Stage 12B Metrics | `2026-07-01-internalv2-dynamic-node-stage12b-metrics.md` | Low-cardinality task/Slot replica move metrics for dynamic-node blockers |
| 3 | Stage 12C E2EV2 And Runbook | `2026-07-01-internalv2-dynamic-node-stage12c-e2ev2-runbook.md` | Automatic diagnostic artifacts in e2ev2 and updated operations runbook |

## Cross-Stage Invariants

- Dynamic membership remains ControllerV2-authoritative.
- Manager diagnostic routes are read-only and require manager read permissions.
- `wkcli node` remains a manager HTTP client and must not import internal runtime packages.
- `slot_replica_move` tasks remain the only Slot replica drain/onboarding primitive.
- Diagnostics must explain blockers but must not retry, cancel, or force tasks.
- Metrics must not add task ID, node address, UID, channel ID, session ID, or raw error labels.
- E2EV2 diagnostic collection must be best-effort and must not mask the original test failure.

## Stage Gates

- [ ] **Gate 12A: Diagnostic surface merged locally**

Run:

```bash
GOWORK=off go test ./internalv2/usecase/management -run 'DynamicNodeDiagnostics|ControllerTask|NodeScaleIn|NodeOnboarding' -count=1
GOWORK=off go test ./internalv2/access/manager -run 'DynamicNodeDiagnostics|ControllerTask|ScaleIn|Onboarding|Route' -count=1
GOWORK=off go test ./cmd/wkcli/internal/nodeops -run 'Diagnose|ControllerTask|ImportBoundary' -count=1
GOWORK=off go test ./cmd/wkcli ./cmd/wkcli/internal/... -count=1
git diff --check
```

Expected: manager diagnostics and `wkcli node diagnose` expose node, blockers, related tasks, audit summaries, and related Slot rows without new forbidden imports.

- [ ] **Gate 12B: Metrics merged locally**

Run:

```bash
GOWORK=off go test ./pkg/metrics -run 'ControllerTask|NodeLifecycle|SlotReplicaMove|Registry' -count=1
GOWORK=off go test ./internalv2/app -run 'ControlSnapshot|TaskAudit|NodeLifecycle|Metrics' -count=1
GOWORK=off go test ./pkg/clusterv2/tasks -run 'SlotReplicaMove.*Metric|SlotReplicaMove' -count=1
GOWORK=off go test ./pkg/clusterv2 ./pkg/controllerv2 -run 'Task|SlotReplicaMove|Metrics' -count=1
git diff --check
```

Expected: `slot_replica_move` appears in Controller task gauges, task age is audit-derived only, and Slot replica move phase metrics use bounded labels.

- [ ] **Gate 12C: E2EV2 evidence and runbook merged locally**

Run:

```bash
GOWORK=off go test ./scripts -run DynamicNodeReadinessGate -count=1
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_operations -run TestWKCLINodeOperationsLifecycleWithTraffic -count=1 -timeout 12m -p=1
scripts/e2ev2/dynamic-node-readiness-gate.sh --profile ops
git diff --check
```

Expected: operations e2e writes dynamic-node diagnostic artifacts, the ops gate links them from its summary, and the runbook explains every major blocker path.

## Final Stage 12 Gate

Run on merged local `main` after 12A, 12B, and 12C:

```bash
GOWORK=off go test ./internalv2/usecase/management ./internalv2/access/manager ./cmd/wkcli ./cmd/wkcli/internal/... ./pkg/metrics ./pkg/clusterv2/tasks ./internalv2/app -count=1
GOWORK=off go test ./scripts -run DynamicNodeReadinessGate -count=1
scripts/e2ev2/dynamic-node-readiness-gate.sh --profile ops
git diff --check
```

Expected: all diagnostic, CLI, metrics, and gate checks pass on local `main`.

## Recommended Execution Mode

Use subagent-driven development. Suggested split:

- Worker 1 owns Stage 12A manager/usecase and CLI files.
- Worker 2 owns Stage 12B metrics and executor instrumentation.
- Worker 3 owns Stage 12C e2ev2 helper, gate script, and runbook updates.

Review after each sub-stage before starting the next. Stage 12A must land before
Stage 12C because e2ev2 diagnostic dumping depends on the manager diagnostic
route and `wkcli node diagnose`.
