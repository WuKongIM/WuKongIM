# InternalV2 Dynamic Node Stage 9 Production Readiness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make dynamic data-node lifecycle production-ready by adding durable health freshness, health-gated placement/removal, operator evidence, and one real-traffic lifecycle smoke.

**Architecture:** Stage 9 is split into four ordered subplans so each piece is testable and reviewable on its own. Stage 9A creates the health report contract without changing placement; Stage 9B consumes that contract for fail-closed placement and removal; Stage 9C exposes low-cardinality evidence; Stage 9D proves the full lifecycle under public real traffic.

**Tech Stack:** Go, ControllerV2, clusterv2, internalv2 manager/usecase layers, `pkg/metrics`, `test/e2ev2`, `cmd/wkcli sim`, `docs/superpowers` plan/spec workflow.

---

## Source Links

- Stage 9 spec: `docs/superpowers/specs/2026-06-29-internalv2-dynamic-node-stage9-production-readiness-design.md`
- Master lifecycle plan: `docs/superpowers/plans/2026-06-24-internalv2-dynamic-node-lifecycle.md`
- Stage 9A plan: `docs/superpowers/plans/2026-06-29-internalv2-dynamic-node-stage9a-health-report-model.md`
- Stage 9B plan: `docs/superpowers/plans/2026-06-29-internalv2-dynamic-node-stage9b-health-gated-placement-remove.md`
- Stage 9C plan: `docs/superpowers/plans/2026-06-29-internalv2-dynamic-node-stage9c-observability-manager-evidence.md`
- Stage 9D plan: `docs/superpowers/plans/2026-06-29-internalv2-dynamic-node-stage9d-real-traffic-smoke.md`

## Execution Order

| Order | Sub-stage | Plan | Unlocks |
| --- | --- | --- | --- |
| 9A | Health Report Model | `2026-06-29-internalv2-dynamic-node-stage9a-health-report-model.md` | Durable low-frequency health reports, freshness calculation, and control snapshot fields |
| 9B | Health-Gated Placement And Remove | `2026-06-29-internalv2-dynamic-node-stage9b-health-gated-placement-remove.md` | New placement excludes stale/unknown nodes and scale-in/remove reports health blockers |
| 9C | Observability And Manager Evidence | `2026-06-29-internalv2-dynamic-node-stage9c-observability-manager-evidence.md` | Prometheus and manager expose lifecycle readiness, health age, and blocker evidence |
| 9D | Real Traffic Production Smoke | `2026-06-29-internalv2-dynamic-node-stage9d-real-traffic-smoke.md` | Public-surface proof that join, onboarding, scale-in, and removal work while traffic continues |

## Cross-Stage Invariants

- Health reports are durable and low-frequency, but they must not make placement and lifecycle compare-and-set operations race with heartbeat churn.
- ControllerV2 membership remains authoritative. Manager and metrics read health evidence; they do not own the durable health truth.
- A node is schedulable only when it is a data-role `active` member with fresh `alive` health and `runtime_ready=true`.
- `joining`, `leaving`, `removed`, `suspect`, `down`, stale, and missing-health nodes must fail closed for new Slot and Channel placement.
- Scale-in/remove must explain stale or missing health with explicit fields and bounded reasons.
- Metrics labels stay low-cardinality: no UID, channel ID, task ID, session ID, or address labels.
- Tests under `test/e2ev2` stay black-box and use public manager HTTP, public metrics, WKProto, and process handles only.

## Stage Gates

- [x] **Gate 9A: health reports persist without logical revision churn**

Run:

```bash
GOWORK=off go test ./pkg/controllerv2/state ./pkg/controllerv2/fsm ./pkg/controllerv2 ./pkg/clusterv2/control ./pkg/clusterv2/observe ./pkg/clusterv2 -run 'Health|ReportNode|ControlWrite|Config' -count=1
GOWORK=off go test ./cmd/wukongimv2 -run 'Config|HealthReport' -count=1
git diff --check
```

Expected: health report commands persist report data and advance applied index, while logical placement/lifecycle revision is unchanged by periodic health-only writes.

Evidence (2026-06-30): PASS with `GOWORK=off go test ./pkg/controllerv2/state ./pkg/controllerv2/fsm ./pkg/controllerv2 ./pkg/clusterv2/control ./pkg/clusterv2/observe ./pkg/clusterv2 -run 'Health|ReportNode|ControlWrite|Config' -count=1`, `GOWORK=off go test ./cmd/wukongimv2 -run 'Config|HealthReport' -count=1`, and `git diff --check`.

- [x] **Gate 9B: placement and removal fail closed on stale or missing health**

Run:

```bash
GOWORK=off go test ./pkg/clusterv2 ./internalv2/usecase/management ./internalv2/access/manager -run 'Health|Placement|ScaleIn|Remove|NodeList' -count=1
git diff --check
```

Expected: active data nodes without fresh `alive` health are excluded from new placement, and manager scale-in status exposes health blockers.

Evidence (2026-06-30): PASS with `GOWORK=off go test ./pkg/clusterv2 ./internalv2/usecase/management ./internalv2/access/manager -run 'Health|Placement|ScaleIn|Remove|NodeList' -count=1` and `git diff --check`.

- [ ] **Gate 9C: operator evidence is visible and low-cardinality**

Run:

```bash
GOWORK=off go test ./pkg/metrics ./internalv2/usecase/management ./internalv2/access/manager ./internalv2/app -run 'Lifecycle|Health|Metrics|Manager|ScaleIn' -count=1
git diff --check
```

Expected: Prometheus families and manager JSON fields expose lifecycle and health freshness without high-cardinality labels.

- [ ] **Gate 9D: public real-traffic lifecycle smoke passes**

Run:

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_readiness -run TestDynamicNodeLifecycleWithContinuousTraffic -count=1 -timeout 12m -p=1
git diff --check
```

Expected: the test starts a three-node cluster, runs real traffic, adds and activates node 4, onboards one Slot replica, marks node 4 leaving, drains it, removes it, and proves traffic continues without send errors.

## Handoff Rules

- [ ] Execute sub-stages in order: 9A, 9B, 9C, then 9D.
- [ ] Use one focused branch or commit per sub-stage unless the user explicitly asks to combine them.
- [ ] Read each touched package's `FLOW.md` before editing and update it when behavior changes.
- [ ] Keep health report writes bounded and do not introduce any cluster-bypass path for a single-node deployment.
- [ ] After each sub-stage, update this master plan gate only after the gate command passes.

## Recommended Execution Mode

Use subagent-driven development. Stage 9 crosses ControllerV2 durability, clusterv2 runtime loops, manager DTOs, metrics, and e2ev2 traffic, so a fresh subagent per sub-stage plus review between stages is safer than batching the whole feature in one session.
