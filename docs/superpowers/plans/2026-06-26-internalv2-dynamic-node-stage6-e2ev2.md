# internalv2 Dynamic Node Lifecycle Stage 6 E2EV2 Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add higher-value black-box e2ev2 scenarios for dynamic node join, activation, real online delivery, onboarding, scale-in, drain, and removal so the Stage 2-5 lifecycle is protected by real process, real manager HTTP, and real WKProto coverage.

**Architecture:** Keep all new coverage in `test/e2ev2/cluster/dynamic_node_join` and reusable public-manager helpers in `test/e2ev2/suite`. Tests must exercise real `cmd/wukongimv2` processes and public entrypoints only. Do not import `internalv2/app`, `internalv2/usecase`, storage internals, or control-plane internals from scenario tests.

**Tech Stack:** Go e2e tests with `//go:build e2e`, `test/e2ev2/suite`, manager HTTP JSON APIs, WKProto clients, `stretchr/testify/require`.

---

## Source Links

- Spec: `docs/superpowers/specs/2026-06-24-internalv2-dynamic-node-lifecycle-design.md`
- Master index: `docs/superpowers/plans/2026-06-24-internalv2-dynamic-node-lifecycle.md`
- Stage 2 dynamic join: `docs/superpowers/plans/2026-06-24-internalv2-dynamic-node-stage2.md`
- Stage 3 onboarding: `docs/superpowers/plans/2026-06-24-internalv2-dynamic-node-stage3.md`
- Stage 5 removal index: `docs/superpowers/plans/2026-06-24-internalv2-dynamic-node-stage5.md`

## Current Coverage

- `TestDynamicJoinFourthDataNode` proves seed-join, activation, readiness, and a post-activation gateway SEND.
- `TestSlotReplicaMoveKeepsSendAvailable` proves bounded Slot onboarding can run while gateway SEND succeeds.
- `TestLeavingNodeCanBeRemovedAfterDrain` proves an empty joined node can be marked leaving, drained, reported safe, removed, and observed as `removed`.

## Coverage Gaps

- No e2ev2 test keeps an authenticated gateway session open while a node enters drain mode.
- No e2ev2 test proves drain mode rejects new gateway sessions while existing sessions can finish in-flight sends.
- No e2ev2 test proves an activated dynamic node participates in real cross-node online delivery, not just `SENDACK`.
- No e2ev2 test starts scale-in after node 4 has actually received Slot replicas through onboarding.
- No e2ev2 test drives the scale-in Slot drain `plan` and `advance` routes before final removal.
- No e2ev2 test verifies unsafe manager routes return conflict for active, controller-role, or not-yet-drained targets.
- No e2ev2 test proves final remove is idempotent after a node already reached `removed`.
- No e2ev2 test proves invalid seed-join tokens or unreachable advertise addresses fail without Slot assignment side effects.
- No e2ev2 test proves concurrent onboarding/scale-in task creation remains idempotent and bounded.
- `test/e2ev2/AGENTS.md` and scenario AGENTS docs do not yet describe the expanded Stage 5/6 dynamic lifecycle coverage.

## Stage 6 Order

| Order | Area | Result |
| --- | --- | --- |
| 6A | Suite helpers | Manager client can call scale-in `plan`, `advance`, raw status-code variants, activation status, and richer safety fields. |
| 6B | Dynamic node delivery | Activated node 4 participates in real online delivery with another cluster node. |
| 6C | Live gateway drain | Existing session continues, new session is rejected, final remove waits until runtime counters clear. |
| 6D | Slot scale-in drain | A node with onboarded Slot replicas is drained through `plan` and `advance` before removal. |
| 6E | Safety gates | Unsafe manager operations return bounded conflicts; remove is idempotent after `removed`. |
| 6F | Join and activation negatives | Invalid token and unreachable advertise address leave control state and Slot assignments safe. |
| 6G | Concurrent task guards | Concurrent onboarding or scale-in task creation creates at most one durable task per Slot. |
| 6H | Catalog docs | e2ev2 AGENTS catalog matches the actual dynamic node scenario set. |

## Execution Progress

- [x] Task 1: Added manager lifecycle / scale-in helper DTOs and raw status helpers in `test/e2ev2/suite/manager_client.go`.
  - Verified: `GOWORK=off go test -tags=e2e ./test/e2ev2/suite -count=1`
- [x] Task 2: Added activated dynamic node cross-node online delivery scenario in `dynamic_node_delivery_test.go`.
  - Verified: `GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_join -run TestActivatedDynamicNodeDeliversCrossNodeTraffic -count=1 -timeout 3m -p=1`
- [x] Task 3: Added live gateway drain scenario in `scale_in_live_session_test.go`; fixed retryable ControllerV2 revision-mismatch mapping for join / activate / leaving while preserving final remove conflict semantics.
  - Verified: `GOWORK=off go test ./internalv2/usecase/management -run 'Node|ScaleIn|MarkNodeRemoved' -count=1`
  - Verified: `GOWORK=off go test ./internalv2/access/manager -run 'NodeLifecycle|ScaleIn|Activate' -count=1`
  - Verified: `GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_join -run TestScaleInDrainKeepsExistingSessionAndBlocksFinalRemove -count=1 -timeout 4m -p=1`
  - Verified: `GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_join -count=1 -timeout 7m -p=1`
- [x] Task 4: Added onboarded Slot scale-in drain scenario in `scale_in_slot_drain_test.go`; fixed leaving-node Slot peer validation, control snapshot read freshness after missed watch events, and plan gating so Slot leadership remains a drain blocker but not a drain-task blocker.
  - Verified: `GOWORK=off go test ./pkg/clusterv2/control -run 'SnapshotValidateAllowsLeavingDataSlotPeer|RuntimeLocalSnapshotRefreshesFromBackendWhenWatchMissesLifecycleWrite|MarkNodeLeaving' -count=1`
  - Verified: `GOWORK=off go test ./pkg/controllerv2/state -run 'ValidateAllowsLeavingDataSlotPeer|ValidateRejectsRemovedSlotPeer|ValidateRejectsSlotPeer' -count=1`
  - Verified: `GOWORK=off go test ./internalv2/usecase/management -run 'AdvanceNodeScaleInAllowsSlotDrainWhenLeavingNodeIsSlotLeader|AdvanceNodeScaleInAllowsSlotDrainWhenChannelInventoryUnknown|AdvanceNodeScaleInCreatesMoveAwayFromLeavingNode|MarkNodeRemoved|ScaleIn' -count=1`
  - Verified: `GOWORK=off go test ./internalv2/access/manager -run 'ScaleIn|NodeLifecycle' -count=1`
  - Verified: `GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_join -run TestScaleInAdvancesSlotReplicasBeforeRemove -count=1 -timeout 6m -p=1`
  - Verified: `GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_join -count=1 -timeout 8m -p=1`
- [x] Task 5: Added scale-in safety gate and idempotent remove scenario in `scale_in_safety_gate_test.go`.
  - Verified: `GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_join -run TestScaleInRejectsUnsafeTargetsAndRemoveIsIdempotent -count=1 -timeout 5m -p=1`
- [x] Task 6: Added invalid seed-join token and unreachable advertise activation negative scenarios in `dynamic_node_join_negative_test.go`; added no-wait seed-join and node join-state suite helpers.
  - Verified: `GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_join -run 'TestSeedJoinRejectsInvalidJoinTokenWithoutMembership|TestActivateRejectsUnreachableAdvertiseAddr' -count=1 -timeout 3m -p=1`
- [x] Task 7: Added concurrent onboarding and scale-in task creation guard scenarios in `slot_onboarding_concurrency_test.go`; mapped ControllerV2 active-task conflict to bounded onboarding/scale-in conflict instead of manager 500.
  - Verified: `GOWORK=off go test ./pkg/controllerv2 -run 'SlotReplicaMove|ExpectedRevision|MarkNodeRemoved' -count=1`
  - Verified: `GOWORK=off go test ./internalv2/usecase/management -run 'ActiveTaskConflict|AdvanceNodeScaleIn|StartNodeOnboarding' -count=1`
  - Verified: `GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_join -run 'TestConcurrentOnboardingStartCreatesAtMostOneTask|TestConcurrentScaleInAdvanceCreatesAtMostOneSlotTask' -count=1 -timeout 5m -p=1`
- [x] Task 8: Updated e2ev2 catalog docs for the expanded dynamic node lifecycle scenario set.
  - Updated: `test/e2ev2/AGENTS.md`
  - Updated: `test/e2ev2/cluster/AGENTS.md`
  - Updated: `test/e2ev2/cluster/dynamic_node_join/AGENTS.md`
- [x] Task 9: Final verification.
  - Verified: `GOWORK=off go test ./pkg/controllerv2 ./pkg/clusterv2/control ./internalv2/usecase/management ./internalv2/access/manager -count=1`
  - Verified: `GOWORK=off go test -tags=e2e ./test/e2ev2/suite -count=1`
  - Verified: `GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_join -count=1 -timeout 10m -p=1`

## Rules

- Keep scenarios black-box and process-level.
- Keep dynamic join behavior honest: node 4 must join through `StartSeedJoinNode`; tests must not call an internal join shortcut.
- Describe deployment as a multi-node cluster.
- Keep test runtime bounded. Use one bounded Slot move at a time for onboarding and scale-in drain.
- Prefer polling public manager status over sleeps.
- Run dynamic-node e2e serially with `-p=1`.
- Do not hide flaky readiness by increasing global timeouts first; inspect diagnostics and add narrower readiness evidence if needed.

---

## Task 1: Add Lifecycle E2EV2 Suite Helpers

**Files:**

- `test/e2ev2/suite/manager_client.go`
- `test/e2ev2/suite/runtime.go`

**Red first:**

- Add the Stage 6B-6D scenario tests before helper implementation, or add the helper signatures as compile references from one scenario. The package should fail to compile until these methods and DTOs exist.

**Implementation:**

- Add public helper DTOs mirroring manager scale-in JSON:

```go
// NodeScaleInCandidateDTO describes one Slot replica move selected for scale-in drain.
type NodeScaleInCandidateDTO struct {
	SlotID       uint32   `json:"slot_id"`
	SourceNodeID uint64   `json:"source_node_id"`
	TargetNodeID uint64   `json:"target_node_id"`
	DesiredPeers []uint64 `json:"desired_peers"`
	TargetPeers  []uint64 `json:"target_peers"`
	ConfigEpoch  uint64   `json:"config_epoch"`
}

// NodeScaleInPlanDTO is the manager scale-in drain preview subset used by e2ev2.
type NodeScaleInPlanDTO struct {
	GeneratedAt     string                    `json:"generated_at"`
	StateRevision   uint64                    `json:"state_revision"`
	NodeID          uint64                    `json:"node_id"`
	Candidates      []NodeScaleInCandidateDTO `json:"candidates"`
	BlockedByStatus bool                      `json:"blocked_by_status"`
}

// NodeScaleInAdvanceDTO is the manager scale-in drain submit subset used by e2ev2.
type NodeScaleInAdvanceDTO struct {
	GeneratedAt   string                    `json:"generated_at"`
	StateRevision uint64                    `json:"state_revision"`
	NodeID        uint64                    `json:"node_id"`
	Created       uint32                    `json:"created"`
	Skipped       uint32                    `json:"skipped"`
	Candidates    []NodeScaleInCandidateDTO `json:"candidates"`
}
```

- Add success helpers:

```go
func (m *ManagerClient) MustPlanScaleIn(t testing.TB, nodeID uint64, maxSlotMoves uint32) NodeScaleInPlanDTO
func (m *ManagerClient) MustAdvanceScaleIn(t testing.TB, nodeID uint64, maxSlotMoves uint32) NodeScaleInAdvanceDTO
```

- Add raw status-code helpers for negative tests:

```go
func (m *ManagerClient) ActivateNodeStatus(ctx context.Context, nodeID uint64) (int, []byte, error)
func (m *ManagerClient) StartScaleInStatus(ctx context.Context, nodeID uint64) (int, []byte, error)
func (m *ManagerClient) SetScaleInDrainStatus(ctx context.Context, nodeID uint64, drain bool) (int, []byte, error)
func (m *ManagerClient) RemoveScaleInStatus(ctx context.Context, nodeID uint64) (int, []byte, error)
```

- Reuse the existing unexported `postJSONStatus` helper so response bodies remain available for diagnostics.
- Extend `NodeScaleInStatusDTO` to include every safety bit exposed by manager HTTP:
  - `blocked_by_slot_leadership`
  - `blocked_by_slot_runtime`
  - `unknown_runtime`
  - `unknown_control_revision`
  - `slot_leader_count`
  - `active_task_count`
  - `failed_task_count`
- If negative seed-join tests cannot use `StartSeedJoinNode` because it waits for readiness, add a separate no-wait helper with an explicit name, for example `StartSeedJoinNodeNoWait`.
- Keep comments on exported DTOs and methods.

**Verification:**

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/suite -count=1
```

---

## Task 2: Dynamic Node Online Delivery Scenario

**Files:**

- `test/e2ev2/cluster/dynamic_node_join/dynamic_node_delivery_test.go`

**Test:**

```go
func TestActivatedDynamicNodeDeliversCrossNodeTraffic(t *testing.T)
```

**Flow:**

- Start a three-node cluster with manager HTTP, dynamic join token, and delivery/top overrides on every node:

```go
map[string]string{
	"WK_DELIVERY_ENABLE":      "true",
	"WK_TOP_API_ENABLE":       "true",
	"WK_TOP_COLLECT_INTERVAL": "100ms",
	"WK_TOP_HISTORY_WINDOW":   "2s",
}
```

- Start node 4 with seed-join mode and the same delivery/top overrides.
- Wait for node 4 to reach `joining`, become publicly ready, activate it, and observe `active`.
- Connect user A to node 4 and user B to node 2 through WKProto.
- Send A -> B and require:
  - sender receives `SENDACK` success
  - user B receives `RECV`
  - `FromUID`, `ChannelID`, `ChannelType`, `Payload`, `MessageID`, and `MessageSeq` match the send result
  - user B sends `RecvAck`
  - node 2 top delivery `ack_bindings` rises and returns to zero
- Send B -> A and require the same assertions with node 4 as the recipient owner.
- Close both clients.

**Implementation notes:**

- Follow the existing pattern in `test/e2ev2/message/cross_node_delivery/cross_node_delivery_test.go`.
- Keep helper functions local to this scenario unless a second dynamic-node delivery test needs them.
- This test closes the biggest current gap: node 4 currently proves gateway `SENDACK`, but not real online recipient delivery.

**Verification:**

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_join -run TestActivatedDynamicNodeDeliversCrossNodeTraffic -count=1 -timeout 3m -p=1
```

---

## Task 3: Live Session Drain Scenario

**Files:**

- `test/e2ev2/cluster/dynamic_node_join/scale_in_live_session_test.go`

**Test:**

```go
func TestScaleInDrainKeepsExistingSessionAndBlocksFinalRemove(t *testing.T)
```

**Flow:**

- Start a three-node cluster with manager HTTP and a dynamic join token.
- Start node 4 with seed-join mode.
- Wait for `joining`, public readiness, activation, and `active`.
- Connect an authenticated WKProto sender through node 4 and send one message.
- Start scale-in for node 4.
- Assert scale-in status is not safe to remove while the session is open.
- Enable drain mode.
- Assert manager status reports:
  - `GatewayDraining == true`
  - `AcceptingNewSessions == false`
  - `SafeToRemove == false`
  - `BlockedByRuntimeDrain == true`
  - at least one of `GatewaySessions`, `ActiveOnline`, or `TotalOnline` is non-zero
- Use the existing sender to send one more message successfully after drain starts.
- Attempt a new WKProto connection to node 4 with a short context and require it to fail.
- Close the existing sender.
- Wait for `EventuallyScaleInSafeToRemove`.
- Remove node 4 and observe `removed`.

**Implementation notes:**

- Reuse same-package helpers from `slot_onboarding_test.go`:
  - `requireGatewaySender`
  - `sendGatewayMessage`
- Use existing `WKProtoClient.ConnectContext` with a short timeout for the rejected new-session assertion.
- Keep the negative connect assertion tolerant to transport-level failure shape; assert failure, not a specific private error string.

**Verification:**

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_join -run TestScaleInDrainKeepsExistingSessionAndBlocksFinalRemove -count=1 -p=1
```

---

## Task 4: Slot Scale-In Drain Scenario

**Files:**

- `test/e2ev2/cluster/dynamic_node_join/scale_in_slot_drain_test.go`

**Test:**

```go
func TestScaleInAdvancesSlotReplicasBeforeRemove(t *testing.T)
```

**Flow:**

- Start a three-node cluster with manager HTTP and a dynamic join token.
- Start node 4 with seed-join mode and activate it.
- Run one bounded onboarding move with `MustPlanOnboarding(4, 1)` and `MustStartOnboarding(4, 1)`.
- Wait for `EventuallyOnboardingSafe`.
- Confirm manager Slot inventory now contains node 4 in at least one `SlotDTO.Assignment.DesiredPeers`.
- Start scale-in for node 4 and enable drain mode.
- Read scale-in status and require:
  - `BlockedBySlots == true`
  - `SafeToRemove == false`
  - `SlotReplicaCount > 0`
- Call `MustPlanScaleIn(4, 1)` and require one candidate:
  - `SourceNodeID == 4`
  - `TargetNodeID != 4`
  - `DesiredPeers` contains 4
  - `TargetPeers` does not contain 4
  - `BlockedByStatus == false`
- Call `MustAdvanceScaleIn(4, 1)` and require `Created == 1`.
- Poll `NodeScaleInStatus` until Slot blockers clear.
- Wait for `EventuallyScaleInSafeToRemove`.
- Remove node 4 and observe `removed`.

**Implementation notes:**

- Add local test helpers in the scenario file:

```go
func requireSlotsContainDesiredPeer(t testing.TB, slots []suite.SlotDTO, nodeID uint64)
func eventuallyScaleInSlotsDrained(t testing.TB, manager *suite.ManagerClient, nodeID uint64, timeout time.Duration) suite.NodeScaleInStatusDTO
```

- Do not assert a specific Slot ID. The hash-slot count and planner choice are implementation details.
- Keep `max_slot_moves=1` so the test remains bounded and deterministic enough for e2e.

**Verification:**

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_join -run TestScaleInAdvancesSlotReplicasBeforeRemove -count=1 -p=1
```

---

## Task 5: Safety Gate And Idempotent Remove Scenario

**Files:**

- `test/e2ev2/cluster/dynamic_node_join/scale_in_safety_gate_test.go`

**Test:**

```go
func TestScaleInRejectsUnsafeTargetsAndRemoveIsIdempotent(t *testing.T)
```

**Flow:**

- Start a three-node cluster with manager HTTP and a dynamic join token.
- Start node 4 with seed-join mode and activate it.
- Before marking node 4 leaving, call raw drain helper and require HTTP `409`.
- Before marking node 4 leaving, call raw remove helper and require HTTP `409`.
- Attempt `StartScaleInStatus(ctx, 1)` against a controller-role node and require HTTP `409`.
- Start scale-in for node 4.
- Call raw remove helper before gateway drain and require HTTP `409`.
- Enable drain mode and wait for `EventuallyScaleInSafeToRemove`.
- Remove node 4 once and require `Changed == true`, `JoinState == "removed"`.
- Remove node 4 a second time through raw/success helper and require HTTP 2xx, `Changed == false`, `JoinState == "removed"`.

**Implementation notes:**

- Assert error bodies only at the public contract level: status code and JSON code `"conflict"` if the response body already exposes it.
- Keep body matching loose enough that wording changes do not break the e2e.
- This scenario should not create Slot onboarding tasks. Slot drain is covered by Task 3.

**Verification:**

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_join -run TestScaleInRejectsUnsafeTargetsAndRemoveIsIdempotent -count=1 -p=1
```

---

## Task 6: Join And Activation Negative Scenarios

**Files:**

- `test/e2ev2/cluster/dynamic_node_join/dynamic_node_join_negative_test.go`
- `test/e2ev2/suite/runtime.go`

**Tests:**

```go
func TestSeedJoinRejectsInvalidJoinTokenWithoutMembership(t *testing.T)
func TestActivateRejectsUnreachableAdvertiseAddr(t *testing.T)
```

**Invalid token flow:**

- Start a three-node cluster with manager HTTP and dynamic join token `good-token`.
- Capture stable Slot inventory before the negative join attempt.
- Start node 4 with seed-join mode but token `bad-token`.
- Use a no-wait or expect-failure seed-join helper so the test does not hang waiting for readiness.
- In a short bounded window, require:
  - manager node inventory does not contain node 4
  - Slot assignments remain unchanged
  - node 4 does not become public-ready, or exits with bounded diagnostics

**Unreachable advertise flow:**

- Start a three-node cluster with manager HTTP and a valid dynamic join token.
- Start node 4 with seed-join mode but an unreachable `WK_CLUSTER_ADVERTISE_ADDR`.
- Observe node 4 as `joining` through manager HTTP.
- Call `ActivateNodeStatus(ctx, 4)` and require HTTP `409`.
- Require response body to expose only public conflict/readiness evidence.
- Require node 4 remains `joining`.
- Require Slot assignments remain unchanged.

**Implementation notes:**

- If unreachable advertise addresses are hard to make deterministic on macOS, prefer a reserved local port that the test deliberately does not listen on.
- Keep negative windows short and diagnostic-rich.
- Do not assert internal error strings.

**Verification:**

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_join -run 'TestSeedJoinRejectsInvalidJoinTokenWithoutMembership|TestActivateRejectsUnreachableAdvertiseAddr' -count=1 -timeout 3m -p=1
```

---

## Task 7: Concurrent Task Creation Guard Scenario

**Files:**

- `test/e2ev2/cluster/dynamic_node_join/slot_onboarding_concurrency_test.go`

**Test:**

```go
func TestConcurrentOnboardingStartCreatesAtMostOneTask(t *testing.T)
```

**Flow:**

- Start a three-node cluster with manager HTTP and dynamic join token.
- Start node 4 with seed-join mode and activate it.
- Launch two concurrent onboarding start requests for node 4 with `max_slot_moves=1`.
- Require total `Created` across both responses is at most 1.
- Require no active onboarding task reports `Failed > 0`.
- While onboarding is active or immediately after it clears, send through node 4 gateway and require `SENDACK` success.
- Wait for `EventuallyOnboardingSafe`.
- Confirm stable Slot inventory remains valid and no duplicate active task remains for the same Slot.

**Optional follow-up in same file:**

```go
func TestConcurrentScaleInAdvanceCreatesAtMostOneSlotTask(t *testing.T)
```

- Only add this if Task 4's scale-in drain path is stable.
- Start from an onboarded node 4, mark it leaving and draining, then call `MustAdvanceScaleIn` concurrently with `max_slot_moves=1`.
- Require total `Created <= 1`, no failed tasks, eventual Slot blockers clear, and final remove succeeds.

**Verification:**

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_join -run 'TestConcurrentOnboardingStartCreatesAtMostOneTask|TestConcurrentScaleInAdvanceCreatesAtMostOneSlotTask' -count=1 -timeout 4m -p=1
```

---

## Task 8: Update E2EV2 Catalog Docs

**Files:**

- `test/e2ev2/AGENTS.md`
- `test/e2ev2/cluster/AGENTS.md`
- `test/e2ev2/cluster/dynamic_node_join/AGENTS.md`

**Implementation:**

- Update `dynamic_node_join/AGENTS.md` from Stage 2-only wording to Stage 2-6 lifecycle wording.
- List the scenario contract as:
  - seed join and activation
  - activated dynamic-node real online delivery
  - bounded Slot onboarding while SEND remains available
  - leaving plus empty-node drain/remove
  - live-session gateway drain
  - Slot replica scale-in drain
  - unsafe route conflicts and idempotent remove
  - invalid join token and unreachable advertise negative paths
  - concurrent onboarding/scale-in task creation guards
- Add a catalog row in `test/e2ev2/AGENTS.md` for `test/e2ev2/cluster/dynamic_node_join`.
- Add the focused serial command:

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_join -count=1 -p=1
```

**Verification:**

```bash
git diff --check
```

---

## Task 9: Final Verification

Run focused checks first:

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/suite -count=1
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_join -run 'TestDynamicJoinFourthDataNode|TestSlotReplicaMoveKeepsSendAvailable|TestLeavingNodeCanBeRemovedAfterDrain' -count=1 -timeout 3m -p=1
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_join -count=1 -timeout 6m -p=1
```

Run the related package gate:

```bash
GOWORK=off go test ./pkg/controllerv2 ./pkg/clusterv2/control ./pkg/clusterv2 ./internalv2/usecase/management ./internalv2/access/manager ./internalv2/access/node ./internalv2/infra/cluster ./internalv2/app -count=1
```

Run full serial e2ev2 before calling the work complete:

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/... -count=1 -timeout 10m -p=1
```

Finish with:

```bash
git diff --check
git status --short --branch
```

Expected final result:

- Dynamic node lifecycle has black-box coverage for join, activation, real online delivery, onboarding, live drain, Slot drain, unsafe operations, safe remove, negative join/activation paths, concurrent task guards, and idempotent remove.
- Full e2ev2 passes serially with `-p=1`.
- AGENTS docs describe the actual e2ev2 scenario set for future agents.

---

## Execution Notes: 2026-06-26

### Root Causes Found

1. Bootstrap tasks could remain active even after all participants reported `done`.
   - Evidence: `TestSeedJoinRejectsInvalidJoinTokenWithoutMembership` failed with all bootstrap participants done and manager runtime voters `[1,2,3]`, but the task stayed `pending`.
   - Root cause: the node task reconcile loop read `n.controlSnapshot`, which is only updated by best-effort watch events. Manager `LocalSnapshot` had already refreshed from ControllerV2 backend, but the node executor was retrying against a stale snapshot.
   - Fix: task reconcile loop now fetches a fresh `control.LocalSnapshot(ctx)` before retrying active tasks.

2. Slot replica onboarding could fail during `promote_learner` with `slot replica move learner catch-up timed out`.
   - Evidence: `TestSlotReplicaMoveKeepsSendAvailable -count=5` reproduced a failed active task at `promote_learner` with target node 4 still healthy.
   - Root cause: learner catch-up is an asynchronous progress gate, but the executor treated a bounded 300ms poll miss as terminal `FailTask`. Under real e2ev2 startup and SEND traffic, node 4 can legitimately need more than one reconcile window to catch up.
   - Fix: `promoteLearner` now leaves the task active when learner catch-up is not observed in the current bounded poll window, allowing the next fresh reconcile pass to continue.

3. Slot replica onboarding could fail during `remove_voter` with `slot replica move leader transfer observation timed out`.
   - Evidence: post-merge serial e2ev2 failed `TestSlotReplicaMoveKeepsSendAvailable`; manager status showed the active task failed at `remove_voter` while node 4 and the original voters were healthy.
   - Root cause: `TransferLeadership` only enqueues a Slot Raft control action and returns before the leader change is necessarily observable. The replica move executor treated one bounded 300ms observation miss as a terminal `FailTask`, permanently failing a move that a later reconcile pass could continue.
   - Fix: `removeVoter` now leaves the task active when the leader change is not observed in the current bounded poll window, preserving retry semantics for the next fresh reconcile pass.

### Verification Completed

```bash
GOWORK=off go test ./pkg/clusterv2/tasks -count=1
GOWORK=off go test ./pkg/clusterv2 -run 'TestTaskReconcileLoopUsesFreshLocalSnapshotWhenWatchMissesTaskProgress|TestNodeStartRetriesTaskReconcileAfterRetryableControlWrite|TestRetryableTaskReconcileErrorMatchesRemoteControllerNotLeader' -count=1
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_join -run TestSeedJoinRejectsInvalidJoinTokenWithoutMembership -count=1 -timeout 3m -p=1
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_join -run TestSlotReplicaMoveKeepsSendAvailable -count=5 -timeout 8m -p=1
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_join -count=1 -timeout 10m -p=1
GOWORK=off go test ./pkg/clusterv2 ./pkg/clusterv2/control ./pkg/clusterv2/tasks ./pkg/controllerv2 ./internalv2/usecase/management ./internalv2/access/manager -count=1
GOWORK=off go test -tags=e2e ./test/e2ev2/suite -count=1
GOWORK=off go test -tags=e2e ./test/e2ev2/... -count=1 -timeout 10m -p=1
git diff --check
```

### Code Review Follow-up

The final review found no critical correctness issue, but raised two production polish items for the new background task reconcile loop:

1. Idle cost:
   - Root cause: the first task retry loop fetched a fresh `LocalSnapshot` at the fast task interval even when the cached control snapshot had no active tasks.
   - Fix: the loop now starts and remains on a low-frequency idle interval when there are no cached tasks, and switches to the fast interval only while active tasks are present or when a cached active task cannot refresh the latest snapshot.

2. Observability:
   - Root cause: background `LocalSnapshot` and non-retryable task reconcile errors were ignored, making a stuck background retry path hard to diagnose.
   - Fix: `clusterv2.Snapshot.LastTaskReconcileError` now records the latest background snapshot/reconcile error and clears after a clean pass.

Minor review noise was also removed by switching dynamic delivery success prints to `t.Logf`.

Additional verification after review fixes:

```bash
GOWORK=off go test ./pkg/clusterv2 -run 'TestTaskReconcileLoopBacksOffFreshSnapshotWhenNoCachedTasks|TestTaskReconcileLoopRecordsLocalSnapshotError|TestTaskReconcileLoopRecordsNonRetryableReconcileError|TestTaskReconcileLoopUsesFreshLocalSnapshotWhenWatchMissesTaskProgress|TestNodeStartRetriesTaskReconcileAfterRetryableControlWrite|TestRetryableTaskReconcileErrorMatchesRemoteControllerNotLeader' -count=1
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_join -run TestActivatedDynamicNodeDeliversCrossNodeTraffic -count=1 -timeout 4m -p=1
GOWORK=off go test ./pkg/clusterv2 ./pkg/clusterv2/control ./pkg/clusterv2/tasks ./pkg/controllerv2 ./internalv2/usecase/management ./internalv2/access/manager -count=1
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_join -count=1 -timeout 10m -p=1
GOWORK=off go test -tags=e2e ./test/e2ev2/... -count=1 -timeout 10m -p=1
```

Additional verification after post-merge `remove_voter` leader-transfer fix:

```bash
GOWORK=off go test ./pkg/clusterv2/tasks -run TestSlotReplicaMoveExecutorDefersRemoveVoterWhenSourceLeadershipTransferStillPending -count=1
GOWORK=off go test ./pkg/clusterv2/tasks -count=1
GOWORK=off go test ./pkg/clusterv2 ./pkg/clusterv2/control ./pkg/clusterv2/tasks ./pkg/controllerv2 ./internalv2/usecase/management ./internalv2/access/manager -count=1
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_join -count=1 -timeout 10m -p=1
GOWORK=off go test -tags=e2e ./test/e2ev2/... -count=1 -timeout 10m -p=1
```
