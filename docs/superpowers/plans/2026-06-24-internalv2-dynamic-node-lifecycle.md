# internalv2 Dynamic Node Lifecycle Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Execute internalv2 dynamic node lifecycle support in five ordered, reviewable stages from safe read models through future-safe node removal.

**Architecture:** This is the master execution index for the staged plans. Each stage produces a testable increment and must pass its exit gate before the next stage starts, so dynamic join, Slot onboarding, scale-in preparation, and drain safety do not blur together.

**Tech Stack:** Go, ControllerV2, clusterv2, internalv2 manager/usecase layers, e2ev2 black-box tests, `docs/superpowers` plan/spec workflow.

---

## Source Spec

- Spec: `docs/superpowers/specs/2026-06-24-internalv2-dynamic-node-lifecycle-design.md`
- Stage 1 plan: `docs/superpowers/plans/2026-06-24-internalv2-dynamic-node-stage1.md`
- Stage 2 plan: `docs/superpowers/plans/2026-06-24-internalv2-dynamic-node-stage2.md`
- Stage 3 plan: `docs/superpowers/plans/2026-06-24-internalv2-dynamic-node-stage3.md`
- Stage 4 plan: `docs/superpowers/plans/2026-06-24-internalv2-dynamic-node-stage4.md`
- Stage 5 plan: `docs/superpowers/plans/2026-06-24-internalv2-dynamic-node-stage5.md`
  - Stage 5A plan: `docs/superpowers/plans/2026-06-24-internalv2-dynamic-node-stage5a-removed-lifecycle.md`
  - Stage 5B plan: `docs/superpowers/plans/2026-06-24-internalv2-dynamic-node-stage5b-channel-drain-inventory.md`
  - Stage 5C plan: `docs/superpowers/plans/2026-06-24-internalv2-dynamic-node-stage5c-gateway-drain-mode.md`
  - Stage 5D plan: `docs/superpowers/plans/2026-06-24-internalv2-dynamic-node-stage5d-safe-remove-route.md`
  - Stage 5E plan: `docs/superpowers/plans/2026-06-24-internalv2-dynamic-node-stage5e-remove-smoke-flow.md`
- Stage 6 plan: `docs/superpowers/plans/2026-06-26-internalv2-dynamic-node-stage6-e2ev2.md`
- Stage 7 plan: `docs/superpowers/plans/2026-06-26-internalv2-dynamic-node-stage7-gofail-fault-injection.md`
- Stage 8 plan: `docs/superpowers/plans/2026-06-29-internalv2-dynamic-node-stage8-scale-in-fault-injection.md`
- Stage 9 spec: `docs/superpowers/specs/2026-06-29-internalv2-dynamic-node-stage9-production-readiness-design.md`
- Stage 9 plan: `docs/superpowers/plans/2026-06-29-internalv2-dynamic-node-stage9-production-readiness.md`
  - Stage 9A plan: `docs/superpowers/plans/2026-06-29-internalv2-dynamic-node-stage9a-health-report-model.md`
  - Stage 9B plan: `docs/superpowers/plans/2026-06-29-internalv2-dynamic-node-stage9b-health-gated-placement-remove.md`
  - Stage 9C plan: `docs/superpowers/plans/2026-06-29-internalv2-dynamic-node-stage9c-observability-manager-evidence.md`
  - Stage 9D plan: `docs/superpowers/plans/2026-06-29-internalv2-dynamic-node-stage9d-real-traffic-smoke.md`

## Execution Order

| Order | Stage | Plan | Unlocks |
| --- | --- | --- | --- |
| 1 | Read Model And Config Foundation | `2026-06-24-internalv2-dynamic-node-stage1.md` | Safe seed-join config, durable lifecycle projection, active-only placement |
| 2 | Dynamic Join And Activation | `2026-06-24-internalv2-dynamic-node-stage2.md` | Joining nodes can enter ControllerV2 state and become active without Slot moves |
| 3 | Slot Onboarding | `2026-06-24-internalv2-dynamic-node-stage3.md` | Bounded Slot replica movement to active nodes |
| 4 | Scale-In Preparation | `2026-06-24-internalv2-dynamic-node-stage4.md` | Leaving state, no-new-placement guarantee, Slot drain status |
| 5 | Channel And Connection Drain | `2026-06-24-internalv2-dynamic-node-stage5.md` plus Stage 5A-5E subplans | Safe-to-remove gate and explicit `removed` transition |
| 6 | Expanded E2EV2 Coverage | `2026-06-26-internalv2-dynamic-node-stage6-e2ev2.md` | Real-process dynamic node delivery, drain, safety, negative, and concurrency scenarios |
| 7 | Gofail Join And Onboarding Faults | `2026-06-26-internalv2-dynamic-node-stage7-gofail-fault-injection.md` | Opt-in gofail coverage for join, onboarding control writes, Slot movement, and restart recovery |
| 8 | Scale-In Fault Injection | `2026-06-29-internalv2-dynamic-node-stage8-scale-in-fault-injection.md` | Scale-in/remove fail-closed and post-commit retry proofs |
| 9 | Production Readiness | `2026-06-29-internalv2-dynamic-node-stage9-production-readiness.md` plus Stage 9A-9D subplans | Durable health freshness, health-gated placement/remove, operator evidence, and real-traffic readiness smoke |

## Cross-Stage Invariants

- Dynamic membership must stay ControllerV2-authoritative; no `internalv2` code path may add a cluster-bypass branch.
- `WK_CLUSTER_NODES` remains static Controller voter bootstrap input. Dynamic nodes use seed discovery and must not silently form a single-node cluster.
- `DesiredPeers` means committed Slot voters only. Learner targets must stay out of `DesiredPeers` until promotion is proven.
- Placement candidates must be active data nodes only. `joining`, `leaving`, `removed`, `suspect`, and `down` nodes are not schedulable.
- Manager operation routes may create bounded Controller-backed intents, but handlers must not mutate Slot Raft or Controller state directly.
- Removal status fails closed when runtime summaries, channel inventory, Slot status, or control revision data are unknown.
- Operator-path scans and task batches must stay bounded. Default manager pages stay lightweight; full-cardinality inventories are explicit detail or status APIs.

## Stage Gates

- [x] **Gate 1: Stage 1 merged locally**

Run:

```bash
git log -1 --oneline
GOWORK=off go test ./pkg/controllerv2/state ./pkg/clusterv2/control ./pkg/clusterv2 ./cmd/wukongimv2 ./internalv2/usecase/management -count=1
```

Expected: tests pass, and the commit includes only Stage 1 foundation changes.

- [x] **Gate 2: Stage 2 starts only after Gate 1**

Confirm:

```bash
rg -n "JoinState|CapacityWeight|ClusterSeeds|AdvertiseAddr|JoinToken" pkg/controllerv2 pkg/clusterv2 cmd/wukongimv2 internalv2/usecase/management
```

Expected: lifecycle and seed-join foundation exists before adding join writes.

- [x] **Gate 3: Stage 3 starts only after Stage 2 e2ev2 join passes**

Run:

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_join -run TestDynamicJoinFourthDataNode -count=1 -p=1
```

Expected: the fourth node joins and activates, while existing Slot assignments remain unchanged.

- [x] **Gate 4: Stage 4 starts only after Stage 3 Slot move passes**

Run:

```bash
GOWORK=off go test ./pkg/clusterv2/tasks ./internalv2/usecase/management ./internalv2/access/manager -count=1
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_join -run TestSlotReplicaMoveKeepsSendAvailable -count=1 -p=1
```

Expected: one Slot replica can move to an active node without adding the learner target to `DesiredPeers` before promotion.

- [x] **Gate 5: Stage 5 starts only after leaving-state status fails closed**

Run:

```bash
GOWORK=off go test ./internalv2/usecase/management -run 'TestScaleInStatus|TestMarkNodeLeaving' -count=1
GOWORK=off go test ./internalv2/access/manager -run 'TestManagerScaleIn' -count=1
```

Expected: leaving nodes reject new placement and scale-in status reports unsafe when runtime data is unknown.

- [x] **Gate 6: Stage 6 starts only after Stage 5 safe remove smoke passes**

Run:

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_join -run TestLeavingNodeCanBeRemovedAfterDrain -count=1 -timeout 5m -p=1
```

Expected: an activated, drained dynamic node reaches `removed` through manager
HTTP without internal shortcuts.

- [x] **Gate 7: Stage 7 starts only after Stage 6 dynamic-node e2ev2 suite passes**

Run:

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_join -count=1 -timeout 10m -p=1
```

Expected: dynamic join, online delivery, onboarding, scale-in drain, live-session
drain, safety gates, negative joins, and concurrency scenarios pass.

- [x] **Gate 8: Stage 8 starts only after Stage 7 gofail dynamic-node suite passes**

Run:

```bash
scripts/build-gofail-binary.sh --cmd ./cmd/wukongimv2 --package pkg/clusterv2/tasks --package pkg/clusterv2/net --out /tmp/wukongimv2-gofail
WK_E2EV2_BINARY=/tmp/wukongimv2-gofail WK_E2EV2_GOFAIL_DYNAMIC_NODE=1 GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_faults -count=1 -timeout 10m -p=1
```

Expected: existing Stage 7 gofail join, onboarding, Slot movement, and restart
coverage passes before Stage 8 adds scale-in/remove-specific faults.

This is the Stage 7 pre-gate command. The full Stage 8 gofail command, including
scale-in/remove fault packages, lives in the Stage 8 plan and
`test/e2ev2/cluster/dynamic_node_faults/AGENTS.md`.

## Stage 8 Completion

- [x] **Stage 8 scale-in/remove fault injection merged locally**

Run on merged local `main`:

```bash
GOWORK=off go test ./internalv2/usecase/management ./pkg/controllerv2 ./pkg/clusterv2/tasks ./pkg/clusterv2/net -count=1
GOWORK=off go test -tags=e2e ./test/e2ev2/suite ./test/e2ev2/cluster/dynamic_node_faults -count=1 -timeout 2m -p=1
scripts/build-gofail-binary.sh --cmd ./cmd/wukongimv2 --package internalv2/usecase/management --package pkg/controllerv2 --package pkg/clusterv2/tasks --package pkg/clusterv2/net --out /tmp/wukongimv2-gofail
WK_E2EV2_BINARY=/tmp/wukongimv2-gofail WK_E2EV2_GOFAIL_DYNAMIC_NODE=1 GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_faults -count=1 -timeout 12m -p=1
git diff --check
```

Expected: Stage 8 package tests, opt-out e2ev2, gofail binary build, full
opt-in scale-in/remove fault suite, and whitespace checks pass.

Evidence: Stage 8 was fast-forward merged into local `main` at `ef408158` on
2026-06-29, then the commands above passed on merged `main`.

- [ ] **Gate 9: Stage 9 starts only after Stage 8 completion**

Run Stage 9 through its four subplans in order:

```bash
GOWORK=off go test ./pkg/controllerv2/state ./pkg/controllerv2/fsm ./pkg/controllerv2 ./pkg/clusterv2/control ./pkg/clusterv2/observe ./pkg/clusterv2 -run 'Health|ReportNode|ControlWrite|Config' -count=1
GOWORK=off go test ./pkg/clusterv2 ./internalv2/usecase/management ./internalv2/access/manager -run 'Health|Placement|ScaleIn|Remove|NodeList' -count=1
GOWORK=off go test ./pkg/metrics ./internalv2/usecase/management ./internalv2/access/manager ./internalv2/app -run 'Lifecycle|Health|Metrics|Manager|ScaleIn' -count=1
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_readiness -run TestDynamicNodeLifecycleWithContinuousTraffic -count=1 -timeout 12m -p=1
git diff --check
```

Expected: health reports are durable without logical revision churn, placement
and remove gates fail closed on stale health, manager/metrics expose bounded
evidence, and real traffic continues through join, onboarding, scale-in, and
remove.

## Stage 5 Sub-Stage Chain

Run Stage 5 as five separate branches or commits unless the user explicitly asks to combine them:

| Order | Sub-stage | Plan | Completion proof |
| --- | --- | --- | --- |
| 5A | Removed Lifecycle Tombstone | `2026-06-24-internalv2-dynamic-node-stage5a-removed-lifecycle.md` | ControllerV2 and clusterv2 can mark an already-drained leaving data node `removed` |
| 5B | Channel Drain Inventory | `2026-06-24-internalv2-dynamic-node-stage5b-channel-drain-inventory.md` | Scale-in status fails closed on unknown Channel inventory or target Channel ownership |
| 5C | Gateway Drain Mode | `2026-06-24-internalv2-dynamic-node-stage5c-gateway-drain-mode.md` | Manager can set gateway drain mode and runtime drain blockers affect safety |
| 5D | Safe Remove Route | `2026-06-24-internalv2-dynamic-node-stage5d-safe-remove-route.md` | Manager remove route calls `MarkNodeRemoved` only when `safe_to_remove=true` |
| 5E | Remove Smoke And FLOW Docs | `2026-06-24-internalv2-dynamic-node-stage5e-remove-smoke-flow.md` | e2ev2 proves drained node removal and FLOW docs describe the path |

## Handoff Rules

- [ ] Create a fresh worktree or confirm the current worktree is clean before executing each stage.
- [ ] Execute only one stage per branch unless the user explicitly asks to combine stages.
- [ ] Run the stage-specific tests from that stage plan before committing.
- [ ] Update `FLOW.md` files in touched packages when routes, ownership, or package responsibilities change.
- [ ] After each stage commit, update this index by checking the completed stage gate in the working branch.

## Recommended Execution Mode

Use subagent-driven development for Stage 2, Stage 3, and Stage 8 because they
cross ControllerV2, clusterv2, manager, gofail, and e2ev2 boundaries. Use inline
execution only for small review fixes or documentation-only adjustments.
