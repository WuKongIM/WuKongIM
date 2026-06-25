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

## Execution Order

| Order | Stage | Plan | Unlocks |
| --- | --- | --- | --- |
| 1 | Read Model And Config Foundation | `2026-06-24-internalv2-dynamic-node-stage1.md` | Safe seed-join config, durable lifecycle projection, active-only placement |
| 2 | Dynamic Join And Activation | `2026-06-24-internalv2-dynamic-node-stage2.md` | Joining nodes can enter ControllerV2 state and become active without Slot moves |
| 3 | Slot Onboarding | `2026-06-24-internalv2-dynamic-node-stage3.md` | Bounded Slot replica movement to active nodes |
| 4 | Scale-In Preparation | `2026-06-24-internalv2-dynamic-node-stage4.md` | Leaving state, no-new-placement guarantee, Slot drain status |
| 5 | Channel And Connection Drain | `2026-06-24-internalv2-dynamic-node-stage5.md` plus Stage 5A-5E subplans | Safe-to-remove gate and explicit `removed` transition |

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

Use subagent-driven development for Stage 2 and Stage 3 because both cross ControllerV2, clusterv2, manager, and e2ev2 boundaries. Use inline execution only for small review fixes or documentation-only adjustments.
