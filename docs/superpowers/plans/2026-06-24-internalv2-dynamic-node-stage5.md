# internalv2 Dynamic Node Lifecycle Stage 5 Plan Index

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement each linked sub-plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Complete the future-safe node removal path by executing Stage 5 as five small, reviewable sub-stages instead of one large branch.

**Architecture:** Stage 5 turns Stage 4 scale-in preparation into a final safe-to-remove gate. The work is split so ControllerV2 tombstones, Channel inventory, gateway drain mode, final remove routing, and e2ev2/FLOW verification can each be implemented, reviewed, and merged independently.

**Tech Stack:** Go, ControllerV2 lifecycle writes, clusterv2 control forwarding, internalv2 management usecases, manager HTTP routes, manager node RPC, gateway admission control, e2ev2 black-box tests.

---

## Source Spec

- Spec: `docs/superpowers/specs/2026-06-24-internalv2-dynamic-node-lifecycle-design.md`
- Master index: `docs/superpowers/plans/2026-06-24-internalv2-dynamic-node-lifecycle.md`
- Previous stage: `docs/superpowers/plans/2026-06-24-internalv2-dynamic-node-stage4.md`

Stage 5 is the first stage that can write `removed`. The write must remain behind fail-closed evidence. No direct manager route may skip Slot, Channel, task, gateway, and runtime blockers.

## Sub-Stage Order

| Order | Sub-stage | Plan | Result |
| --- | --- | --- | --- |
| 5A | Removed Lifecycle Tombstone | `2026-06-24-internalv2-dynamic-node-stage5a-removed-lifecycle.md` | ControllerV2 and clusterv2 can mark an already-drained leaving data node `removed` |
| 5B | Channel Drain Inventory | `2026-06-24-internalv2-dynamic-node-stage5b-channel-drain-inventory.md` | Scale-in status includes bounded Channel leader/replica/ISR blockers |
| 5C | Gateway Drain Mode | `2026-06-24-internalv2-dynamic-node-stage5c-gateway-drain-mode.md` | Manager can put a node gateway into drain mode and runtime status reports drain blockers |
| 5D | Safe Remove Route | `2026-06-24-internalv2-dynamic-node-stage5d-safe-remove-route.md` | Manager remove route calls `MarkNodeRemoved` only when `safe_to_remove=true` |
| 5E | Remove Smoke And FLOW Docs | `2026-06-24-internalv2-dynamic-node-stage5e-remove-smoke-flow.md` | e2ev2 proves drained node removal; FLOW docs describe the complete path |

## Entry Gate

- [x] Stage 4 is merged into local `main`.
- [x] Scale-in preparation remains healthy on `main`:

```bash
GOWORK=off go test ./internalv2/usecase/management -run 'TestScaleInStatus|TestAdvanceNodeScaleIn|TestMarkNodeLeaving' -count=1
GOWORK=off go test ./internalv2/access/manager -run 'TestManagerScaleIn' -count=1
```

Expected: both commands pass. Leaving nodes reject new placement and scale-in status reports unsafe when runtime data is unknown.

## Cross-Sub-Stage Rules

- Keep `removed` as a tombstone state. Do not physically delete node identity from `cluster-state.json` in Stage 5.
- Do not dynamically add or remove Controller Raft voters.
- Do not mutate Slot `DesiredPeers` directly from manager or usecase code. Slot drain remains Stage 3 `slot_replica_move` task work.
- Do not report `safe_to_remove=true` when Channel inventory, runtime summary, gateway drain state, Slot status, task state, or control revision data is unknown.
- Keep `SafeToProceed` and `SafeToRemove` separate. `SafeToProceed` means the leaving node has no known Slot, task, or Channel blockers for progressing scale-in; `SafeToRemove` additionally requires gateway drain mode and empty runtime counters.
- Keep scans bounded. Channel inventory scans are explicit status/remove safety checks, not default node-list work.
- Execute and merge only one sub-stage per branch unless the user explicitly asks to combine them.

## Final Stage 5 Exit Gate

Run after 5A-5E are merged:

```bash
GOWORK=off go test ./pkg/controllerv2 ./pkg/clusterv2/control ./pkg/clusterv2 ./internalv2/usecase/management ./internalv2/access/manager ./internalv2/access/node ./internalv2/infra/cluster ./internalv2/app -count=1
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_join -run 'TestDynamicJoinFourthDataNode|TestSlotReplicaMoveKeepsSendAvailable|TestLeavingNodeCanBeRemovedAfterDrain' -count=1 -p=1
git diff --check
```

Confirm final lifecycle path:

```bash
rg -n "MarkNodeRemoved|mark_node_removed|SafeToRemove|NodeChannelDrainInventory|SetNodeDrainMode|set_drain_mode|scale-in/remove|removed" pkg internalv2 test/e2ev2
```

Expected: manager/operator and `internalv2` remove entrypoints reach `removed` only through the Stage 5D safe-to-remove usecase. `pkg/controllerv2` and `pkg/clusterv2/control` expose lower-level lifecycle writer primitives for the usecase, but they do not perform drain safety decisions.
