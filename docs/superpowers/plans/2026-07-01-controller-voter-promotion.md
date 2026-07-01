# Controller Voter Promotion Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let an operator promote an eligible active non-Controller node to a real Controller Raft voter from the internalv2 manager node list.

**Architecture:** Build from the ControllerV2 core outward. First add an atomic FSM command that updates `Controllers` and `Nodes[].Roles` together, then expose real Raft learner-to-voter membership APIs, then wire clusterv2 control writes, manager usecase/API, metrics, and web row actions. The operation must prove live Controller Raft membership before durable state claims the target is a voter.

**Tech Stack:** Go, ControllerV2 FSM/Raft, clusterv2 typed RPC/control writer, internalv2 manager usecases and Gin HTTP routes, React/Vitest manager web UI, Mermaid docs.

---

## File Map

### ControllerV2 Core

- Modify `pkg/controllerv2/command/command.go`: add `KindPromoteControllerVoter` and `ControllerVoterPromotion` command payload.
- Modify `pkg/controllerv2/command/codec_test.go`: assert JSON round trip for the new command payload.
- Modify `pkg/controllerv2/fsm/mutations.go`: dispatch the new command and add deterministic reject reason constants.
- Modify `pkg/controllerv2/fsm/mutation_handlers.go`: atomically add the target to `ClusterState.Controllers` and to the target node roles.
- Modify `pkg/controllerv2/fsm/mutation_helpers.go`: add node-role and controller-voter set helpers used by the mutation.
- Modify `pkg/controllerv2/fsm/fsm_test.go`: cover changed promotion, idempotent promotion, stale expected voter sets, missing proof, missing node, inactive node, and address mismatch.
- Create `pkg/controllerv2/raft/membership.go`: expose learner add and learner promotion methods on `raft.Service`.
- Modify `pkg/controllerv2/raft/service.go`: extend proposal plumbing to handle ConfChange proposals and membership responses.
- Modify `pkg/controllerv2/raft/service_run.go`: propose ConfChange entries, complete membership waiters when committed, and refresh status ConfState.
- Modify `pkg/controllerv2/raft/status.go`: add current voters and learners to `raft.Status`.
- Modify `pkg/controllerv2/raft/service_helpers.go`: extract voters and learners from etcd raft status.
- Modify `pkg/controllerv2/raft/apply_scheduler.go`: return committed ConfState and index to the proposal tracker for membership changes.
- Modify `pkg/controllerv2/raft/service_test.go`: cover learner add, voter promotion, status ConfState, and target catch-up from snapshot/log.
- Create `pkg/controllerv2/runtime_controller_voter_promotion.go`: orchestrate final ControllerV2 promotion command after Raft membership proof.
- Modify `pkg/controllerv2/runtime.go`: expose target preparation and promotion facades.
- Modify `pkg/controllerv2/runtime_start.go`: support promotion-mode voter start without bootstrap and without reusing mirror-applied state as Raft-applied state.
- Modify `pkg/controllerv2/types.go`: export promotion request/result/status types.
- Modify `pkg/controllerv2/runtime_test.go`: cover final command proposal, idempotency, expected revision, and mirror-to-learner preparation.

### clusterv2 Control Layer

- Create `pkg/clusterv2/control/controller_voter_promotion.go`: define control-layer request/result types and ControllerV2 mapping.
- Modify `pkg/clusterv2/control/codec.go`: add `promote_controller_voter` control-write action and JSON branches.
- Modify `pkg/clusterv2/control/control_write.go`: route generic control writes to `PromoteControllerVoter`.
- Modify `pkg/clusterv2/control/runtime.go`: forward promotion writes to the current Controller leader before local execution.
- Modify `pkg/clusterv2/control/log_entries.go`: preserve Controller Raft status voter/learner fields.
- Modify `pkg/clusterv2/control/codec_test.go`, `pkg/clusterv2/control/transport_test.go`, and `pkg/clusterv2/control/runtime_test.go`: cover codec, RPC routing, leader forwarding, and retry state.
- Modify `pkg/clusterv2/node_management.go`: expose `Node.PromoteControllerVoter`.
- Modify `pkg/clusterv2/node_defaults.go`: register target-side prepare RPC with the existing node RPC surface.
- Modify `pkg/clusterv2/node_logs.go`: keep voter/learner status fields visible through node-local status.
- Modify `pkg/clusterv2/node_management_test.go`, `pkg/clusterv2/node_logs_test.go`, and `pkg/clusterv2/net/ids_test.go`: cover root facade and status propagation.

### internalv2 Adapter, Usecase, HTTP

- Modify `internalv2/access/node/node_lifecycle_rpc.go`: add `controller_voter_readiness` and `prepare_controller_voter` operations to the existing node lifecycle RPC envelope.
- Modify `internalv2/access/node/node_lifecycle_rpc_test.go`: cover readiness and prepare wire requests.
- Modify `internalv2/app/seed_join_loop.go`: implement target readiness and prepare methods using local app and clusterv2 runtime state.
- Modify `internalv2/app/wiring.go`: wire management promotion ports and node RPC provider.
- Modify `internalv2/infra/cluster/node_lifecycle.go`: map controller voter readiness and prepare responses for the management usecase.
- Modify `internalv2/infra/cluster/management_node_lifecycle.go`: expose `PromoteControllerVoter` through the cluster management adapter.
- Modify `internalv2/infra/cluster/node_lifecycle_test.go` and `internalv2/infra/cluster/management_node_lifecycle_test.go`: cover adapter mappings and semantic errors.
- Create `internalv2/usecase/management/controller_voter_promotion.go`: validate eligibility, readiness, revision fences, and bounded blocker codes.
- Create `internalv2/usecase/management/controller_voter_promotion_test.go`: cover happy path, idempotency, blockers, writer unavailable, readiness unavailable, and expected revision mismatch.
- Modify `internalv2/usecase/management/nodes.go`: add `CanPromoteControllerVoter` node-list hint and new port fields.
- Modify `internalv2/access/manager/controller_voter_promotion.go`: add HTTP handler and response mapping.
- Modify `internalv2/access/manager/server.go`: register `POST /manager/nodes/:node_id/controller-voter/promote` under `cluster.controller:w`.
- Modify `internalv2/access/manager/nodes.go`: add `actions.can_promote_controller_voter`.
- Modify `internalv2/access/manager/server_test.go` and `internalv2/access/manager/node_lifecycle_test.go`: cover route permission and JSON mapping.

### Web

- Modify `web/src/lib/manager-api.types.ts`: add action hint and promotion request/response types.
- Modify `web/src/lib/manager-api.ts`: add `promoteControllerVoter(nodeId, expectedRevision)`.
- Modify `web/src/lib/manager-api.test.ts`: cover URL, method, body, and response parsing.
- Modify `web/src/pages/nodes/page.tsx`: add row action, confirmation dialog copy, success refresh, and conflict display.
- Modify `web/src/pages/nodes/page.test.tsx`: cover visibility, confirm, success, and conflict.
- Modify `web/src/i18n/messages/en.ts` and `web/src/i18n/messages/zh-CN.ts`: add action, confirm, warning, and error strings.

### Observability And Docs

- Modify `pkg/metrics/controller.go` or the current Controller metrics registry file: add low-cardinality attempt/blocker/phase/voter/learner metrics.
- Modify `internalv2/app/observability.go` or the current manager metrics observer wiring file: record management promotion attempts and blocker codes.
- Modify `pkg/metrics/registry_test.go` and the relevant internalv2 app observability tests.
- Modify `pkg/controllerv2/FLOW.md`, `pkg/clusterv2/FLOW.md`, `internalv2/usecase/management/FLOW.md`, and `internalv2/access/manager/FLOW.md`.

## Execution Rules

- Preserve unrelated dirty files. Before every commit, run `git diff --name-only` and stage only files listed in the current task.
- Use `GOWORK=off /usr/local/go/bin/go test` for Go packages unless local environment proves plain `go` is required.
- Use `yarn --cwd web vitest run ...` for web tests because `web/package.json` declares Yarn.
- Do not connect the web action until ControllerV2 promotion and clusterv2 forwarding tests pass.
- Do not label metrics by node ID, address, task ID, or cluster ID.
- Do not scan messages, channels, online sessions, or Slot assignments for this operation.

### Task 0: Guard Workspace And Re-read Spec

**Files:**
- Read: `docs/superpowers/specs/2026-07-01-controller-voter-promotion-design.md`
- Read: `pkg/controllerv2/FLOW.md`
- Read: `pkg/clusterv2/FLOW.md`
- Read: `internalv2/usecase/management/FLOW.md`
- Read: `internalv2/access/manager/FLOW.md`

- [ ] **Step 1: Capture dirty worktree**

Run:

```bash
git status --short
```

Expected: output may include unrelated modified web/docs/script files. Record them in the task notes and do not stage them.

- [ ] **Step 2: Re-read approved spec and package flows**

Run:

```bash
sed -n '1,360p' docs/superpowers/specs/2026-07-01-controller-voter-promotion-design.md
sed -n '1,220p' pkg/controllerv2/FLOW.md
sed -n '1,220p' pkg/clusterv2/FLOW.md
sed -n '1,220p' internalv2/usecase/management/FLOW.md
sed -n '1,220p' internalv2/access/manager/FLOW.md
```

Expected: confirms the approved API, learner-first flow, manager permissions, and FLOW obligations.

### Task 1: ControllerV2 FSM Atomic Promotion Command

**Files:**
- Modify: `pkg/controllerv2/command/command.go`
- Modify: `pkg/controllerv2/command/codec_test.go`
- Modify: `pkg/controllerv2/fsm/mutations.go`
- Modify: `pkg/controllerv2/fsm/mutation_handlers.go`
- Modify: `pkg/controllerv2/fsm/mutation_helpers.go`
- Modify: `pkg/controllerv2/fsm/fsm_test.go`

- [ ] **Step 1: Write failing FSM tests**

Add these tests to `pkg/controllerv2/fsm/fsm_test.go`:

```go
func TestApplyPromoteControllerVoterAddsControllerAndNodeRole(t *testing.T) {
	ctx := context.Background()
	sm, _ := initializedStateMachine(t, 1)
	expected := uint64(1)

	result, err := sm.Apply(ctx, 2, command.Command{
		Kind:             command.KindPromoteControllerVoter,
		ExpectedRevision: &expected,
		ControllerVoterPromotion: &command.ControllerVoterPromotion{
			TargetNodeID:            3,
			TargetAddr:              "n3",
			ExpectedPreviousVoters:  []uint64{1, 2},
			ObservedConfigIndex:     11,
			ObservedVoters:          []uint64{1, 2, 3},
		},
	})
	require.NoError(t, err)
	require.Equal(t, ApplyResult{Changed: true, Revision: 2, AppliedRaftIndex: 2}, result)

	snap := sm.Snapshot(ctx)
	require.Equal(t, []state.ControllerVoter{
		{NodeID: 1, Addr: "n1", Role: state.ControllerRoleVoter},
		{NodeID: 2, Addr: "n2", Role: state.ControllerRoleVoter},
		{NodeID: 3, Addr: "n3", Role: state.ControllerRoleVoter},
	}, snap.Controllers)
	require.True(t, findFSMNode(t, snap, 3).HasRole(state.NodeRoleControllerVoter))
	require.True(t, findFSMNode(t, snap, 3).HasRole(state.NodeRoleData))
	require.NoError(t, snap.Validate())
}

func TestApplyPromoteControllerVoterNoopsWhenAlreadyConsistent(t *testing.T) {
	ctx := context.Background()
	sm, _ := initializedStateMachine(t, 1)
	expected := uint64(1)
	applyOK(t, sm, 2, command.Command{
		Kind:             command.KindPromoteControllerVoter,
		ExpectedRevision: &expected,
		ControllerVoterPromotion: &command.ControllerVoterPromotion{
			TargetNodeID:           3,
			TargetAddr:             "n3",
			ExpectedPreviousVoters: []uint64{1, 2},
			ObservedConfigIndex:    11,
			ObservedVoters:         []uint64{1, 2, 3},
		},
	})
	expected = 2

	result, err := sm.Apply(ctx, 3, command.Command{
		Kind:             command.KindPromoteControllerVoter,
		ExpectedRevision: &expected,
		ControllerVoterPromotion: &command.ControllerVoterPromotion{
			TargetNodeID:           3,
			TargetAddr:             "n3",
			ExpectedPreviousVoters: []uint64{1, 2, 3},
			ObservedConfigIndex:    12,
			ObservedVoters:         []uint64{1, 2, 3},
		},
	})
	require.NoError(t, err)
	require.Equal(t, ApplyResult{Noop: true, Reason: ReasonNoChange, Revision: 2, AppliedRaftIndex: 3}, result)
}

func TestApplyPromoteControllerVoterRejectsMissingRaftProof(t *testing.T) {
	sm, _ := initializedStateMachine(t, 1)
	expected := uint64(1)

	result, err := sm.Apply(context.Background(), 2, command.Command{
		Kind:             command.KindPromoteControllerVoter,
		ExpectedRevision: &expected,
		ControllerVoterPromotion: &command.ControllerVoterPromotion{
			TargetNodeID:           3,
			TargetAddr:             "n3",
			ExpectedPreviousVoters: []uint64{1, 2},
			ObservedConfigIndex:    11,
			ObservedVoters:         []uint64{1, 2},
		},
	})
	require.NoError(t, err)
	require.Equal(t, ApplyResult{Rejected: true, Reason: ReasonControllerVoterProofMissing, Revision: 1, AppliedRaftIndex: 2}, result)
}

func TestApplyPromoteControllerVoterRejectsExpectedVoterSetMismatch(t *testing.T) {
	sm, _ := initializedStateMachine(t, 1)
	expected := uint64(1)

	result, err := sm.Apply(context.Background(), 2, command.Command{
		Kind:             command.KindPromoteControllerVoter,
		ExpectedRevision: &expected,
		ControllerVoterPromotion: &command.ControllerVoterPromotion{
			TargetNodeID:           3,
			TargetAddr:             "n3",
			ExpectedPreviousVoters: []uint64{1, 9},
			ObservedConfigIndex:    11,
			ObservedVoters:         []uint64{1, 2, 3},
		},
	})
	require.NoError(t, err)
	require.Equal(t, ApplyResult{Rejected: true, Reason: ReasonControllerVoterSetMismatch, Revision: 1, AppliedRaftIndex: 2}, result)
}

func TestApplyPromoteControllerVoterRejectsInactiveOrAddressMismatch(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*state.ClusterState)
		addr string
	}{
		{
			name: "inactive",
			edit: func(st *state.ClusterState) {
				for i := range st.Nodes {
					if st.Nodes[i].NodeID == 3 {
						st.Nodes[i].JoinState = state.NodeJoinStateLeaving
					}
				}
			},
			addr: "n3",
		},
		{
			name: "address mismatch",
			edit: func(st *state.ClusterState) {},
			addr: "other",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sm, _ := initializedStateMachine(t, 1)
			st := sm.Snapshot(context.Background())
			tc.edit(&st)
			require.NoError(t, sm.Restore(context.Background(), st))
			expected := st.Revision

			result, err := sm.Apply(context.Background(), st.AppliedRaftIndex+1, command.Command{
				Kind:             command.KindPromoteControllerVoter,
				ExpectedRevision: &expected,
				ControllerVoterPromotion: &command.ControllerVoterPromotion{
					TargetNodeID:           3,
					TargetAddr:             tc.addr,
					ExpectedPreviousVoters: []uint64{1, 2},
					ObservedConfigIndex:    11,
					ObservedVoters:         []uint64{1, 2, 3},
				},
			})
			require.NoError(t, err)
			require.True(t, result.Rejected)
			require.Equal(t, ReasonInvalidState, result.Reason)
		})
	}
}

func findFSMNode(t *testing.T, st state.ClusterState, nodeID uint64) state.Node {
	t.Helper()
	for _, node := range st.Nodes {
		if node.NodeID == nodeID {
			return node
		}
	}
	t.Fatalf("node %d not found", nodeID)
	return state.Node{}
}
```

- [ ] **Step 2: Add failing command codec test**

Add this case to `pkg/controllerv2/command/codec_test.go`:

```go
func TestControllerVoterPromotionCommandRoundTrip(t *testing.T) {
	expected := uint64(7)
	cmd := Command{
		Kind:             KindPromoteControllerVoter,
		ExpectedRevision: &expected,
		ControllerVoterPromotion: &ControllerVoterPromotion{
			TargetNodeID:           4,
			TargetAddr:             "10.0.0.4:11110",
			ExpectedPreviousVoters: []uint64{1, 2, 3},
			ObservedConfigIndex:    42,
			ObservedVoters:         []uint64{1, 2, 3, 4},
		},
	}
	encoded, err := Encode(cmd)
	require.NoError(t, err)
	var raw map[string]any
	require.NoError(t, json.Unmarshal(encoded, &raw))
	require.Contains(t, raw, "controller_voter_promotion")

	decoded, err := Decode(encoded)
	require.NoError(t, err)
	require.Equal(t, cmd, decoded)
}
```

- [ ] **Step 3: Run tests and confirm red**

Run:

```bash
GOWORK=off /usr/local/go/bin/go test ./pkg/controllerv2/command ./pkg/controllerv2/fsm -run 'TestControllerVoterPromotionCommandRoundTrip|TestApplyPromoteControllerVoter' -count=1
```

Expected: fail with undefined `KindPromoteControllerVoter`, `ControllerVoterPromotion`, and FSM reason identifiers.

- [ ] **Step 4: Add command payload**

In `pkg/controllerv2/command/command.go`, add:

```go
// KindPromoteControllerVoter atomically declares a live Controller Raft voter in durable cluster state.
KindPromoteControllerVoter Kind = "promote_controller_voter"
```

Add this field to `Command`:

```go
// ControllerVoterPromotion carries a proven Controller Raft membership promotion.
ControllerVoterPromotion *ControllerVoterPromotion `json:"controller_voter_promotion,omitempty"`
```

Add this type near the other command payload types:

```go
// ControllerVoterPromotion records a proven promotion of one node into Controller Raft voting membership.
type ControllerVoterPromotion struct {
	// TargetNodeID is the active cluster node that has been promoted in live Controller Raft.
	TargetNodeID uint64 `json:"target_node_id"`
	// TargetAddr is the stable control-plane address expected in durable node state.
	TargetAddr string `json:"target_addr"`
	// ExpectedPreviousVoters fences the state command to the voter set observed before the promotion.
	ExpectedPreviousVoters []uint64 `json:"expected_previous_voters,omitempty"`
	// ObservedConfigIndex is the Controller Raft config index that proved the target voter.
	ObservedConfigIndex uint64 `json:"observed_config_index"`
	// ObservedVoters is the live Controller Raft voter set observed after learner promotion.
	ObservedVoters []uint64 `json:"observed_voters,omitempty"`
}
```

- [ ] **Step 5: Add FSM dispatch and helpers**

In `pkg/controllerv2/fsm/mutations.go`, add reason constants:

```go
// ReasonControllerVoterProofMissing marks a promotion without live Controller Raft voter evidence.
ReasonControllerVoterProofMissing = "controller_voter_proof_missing"
// ReasonControllerVoterSetMismatch marks a promotion fenced to a stale Controller voter set.
ReasonControllerVoterSetMismatch = "controller_voter_set_mismatch"
```

Add a dispatch branch:

```go
case command.KindPromoteControllerVoter:
	result = sm.applyPromoteControllerVoter(next, cmd)
```

In `pkg/controllerv2/fsm/mutation_helpers.go`, add:

```go
func controllerVoterNodeIDs(controllers []state.ControllerVoter) []uint64 {
	out := make([]uint64, 0, len(controllers))
	for _, controller := range controllers {
		out = append(out, controller.NodeID)
	}
	return out
}

func findNodeIndexByID(nodes []state.Node, nodeID uint64) int {
	for i := range nodes {
		if nodes[i].NodeID == nodeID {
			return i
		}
	}
	return -1
}

func hasNodeRole(roles []state.NodeRole, role state.NodeRole) bool {
	for _, existing := range roles {
		if existing == role {
			return true
		}
	}
	return false
}

func appendNodeRoleIfMissing(roles []state.NodeRole, role state.NodeRole) []state.NodeRole {
	if hasNodeRole(roles, role) {
		return append([]state.NodeRole(nil), roles...)
	}
	out := append([]state.NodeRole(nil), roles...)
	return append(out, role)
}

func controllerVoterByNodeID(controllers []state.ControllerVoter, nodeID uint64) (state.ControllerVoter, bool) {
	for _, controller := range controllers {
		if controller.NodeID == nodeID {
			return controller, true
		}
	}
	return state.ControllerVoter{}, false
}
```

- [ ] **Step 6: Implement atomic promotion handler**

In `pkg/controllerv2/fsm/mutation_handlers.go`, add:

```go
func (sm *StateMachine) applyPromoteControllerVoter(next *state.ClusterState, cmd command.Command) ApplyResult {
	promotion := cmd.ControllerVoterPromotion
	if next.Revision == 0 || promotion == nil || promotion.TargetNodeID == 0 || promotion.TargetAddr == "" {
		return reject(ReasonInvalidCommand)
	}
	if promotion.ObservedConfigIndex == 0 || !containsUint64(promotion.ObservedVoters, promotion.TargetNodeID) {
		return reject(ReasonControllerVoterProofMissing)
	}
	currentVoters := controllerVoterNodeIDs(next.Controllers)
	_, alreadyController := controllerVoterByNodeID(next.Controllers, promotion.TargetNodeID)
	if len(promotion.ExpectedPreviousVoters) > 0 && !alreadyController && !sameUint64Set(currentVoters, promotion.ExpectedPreviousVoters) {
		return reject(ReasonControllerVoterSetMismatch)
	}
	nodeIndex := findNodeIndexByID(next.Nodes, promotion.TargetNodeID)
	if nodeIndex < 0 {
		return reject(ReasonInvalidState)
	}
	node := next.Nodes[nodeIndex]
	if node.Addr != promotion.TargetAddr || node.JoinState != state.NodeJoinStateActive {
		return reject(ReasonInvalidState)
	}
	if alreadyController && hasNodeRole(node.Roles, state.NodeRoleControllerVoter) {
		return noop(ReasonNoChange)
	}

	before := next.Clone()
	if !alreadyController {
		next.Controllers = append(next.Controllers, state.ControllerVoter{
			NodeID: promotion.TargetNodeID,
			Addr:   promotion.TargetAddr,
			Role:   state.ControllerRoleVoter,
		})
	}
	next.Nodes[nodeIndex].Roles = appendNodeRoleIfMissing(node.Roles, state.NodeRoleControllerVoter)
	next.Normalize()
	return validateChanged(next, before, cmd)
}
```

- [ ] **Step 7: Run focused FSM tests**

Run:

```bash
GOWORK=off /usr/local/go/bin/go test ./pkg/controllerv2/command ./pkg/controllerv2/fsm -run 'TestControllerVoterPromotionCommandRoundTrip|TestApplyPromoteControllerVoter' -count=1
```

Expected: PASS.

- [ ] **Step 8: Run full package tests for touched core packages**

Run:

```bash
GOWORK=off /usr/local/go/bin/go test ./pkg/controllerv2/command ./pkg/controllerv2/fsm -count=1
```

Expected: PASS.

- [ ] **Step 9: Commit Task 1**

Run:

```bash
git add pkg/controllerv2/command/command.go pkg/controllerv2/command/codec_test.go pkg/controllerv2/fsm/mutations.go pkg/controllerv2/fsm/mutation_handlers.go pkg/controllerv2/fsm/mutation_helpers.go pkg/controllerv2/fsm/fsm_test.go
git commit -m "feat: add controller voter promotion state command"
```

Expected: commit succeeds and stages no unrelated files.

### Task 2: Controller Raft Membership API And Status

**Files:**
- Create: `pkg/controllerv2/raft/membership.go`
- Modify: `pkg/controllerv2/raft/service.go`
- Modify: `pkg/controllerv2/raft/service_run.go`
- Modify: `pkg/controllerv2/raft/status.go`
- Modify: `pkg/controllerv2/raft/service_helpers.go`
- Modify: `pkg/controllerv2/raft/apply_scheduler.go`
- Modify: `pkg/controllerv2/raft/service_test.go`

- [ ] **Step 1: Write failing Raft membership tests**

Add these tests to `pkg/controllerv2/raft/service_test.go`:

```go
func TestControllerRaftAddsLearnerThenPromotesVoter(t *testing.T) {
	cluster := newRaftTestCluster(t, []uint64{1, 2, 3})
	cluster.start(t)
	cluster.propose(t, testInitCommand("wk-raft-membership", cluster.peers))
	cluster.waitForRevision(t, 1)

	target := cluster.addNode(t, 4)
	require.NoError(t, target.service.Start(context.Background()))
	leader := cluster.waitForLeader(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	learner, err := leader.service.AddLearner(ctx, 4)
	cancel()
	require.NoError(t, err)
	require.Contains(t, learner.ConfState.Learners, uint64(4))

	require.Eventually(t, func() bool {
		st := target.service.Status()
		return containsUint64ForRaftTest(st.Learners, 4)
	}, 5*time.Second, 10*time.Millisecond)

	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	voter, err := leader.service.PromoteLearner(ctx, 4)
	cancel()
	require.NoError(t, err)
	require.Contains(t, voter.ConfState.Voters, uint64(4))

	require.Eventually(t, func() bool {
		st := target.service.Status()
		return containsUint64ForRaftTest(st.Voters, 4) && !containsUint64ForRaftTest(st.Learners, 4)
	}, 5*time.Second, 10*time.Millisecond)
}

func TestControllerRaftMembershipStatusReportsVotersAndLearners(t *testing.T) {
	cluster := newRaftTestCluster(t, []uint64{1})
	cluster.start(t)
	cluster.propose(t, testInitCommand("wk-raft-status-confstate", cluster.peers))
	cluster.waitForRevision(t, 1)

	status := cluster.nodes[0].service.Status()
	require.Equal(t, []uint64{1}, status.Voters)
	require.Empty(t, status.Learners)
}

func containsUint64ForRaftTest(items []uint64, item uint64) bool {
	for _, existing := range items {
		if existing == item {
			return true
		}
	}
	return false
}
```

Add this helper to the test cluster:

```go
func (c *testRaftCluster) addNode(t *testing.T, id uint64) *testRaftNode {
	t.Helper()
	c.peers = append(c.peers, Peer{NodeID: id, Addr: fmt.Sprintf("n%d", id)})
	dir := t.TempDir()
	statePath := filepath.Join(dir, "cluster-state.json")
	node := &testRaftNode{
		id:           id,
		dir:          dir,
		raftDir:      filepath.Join(dir, "controller-raft"),
		statePath:    statePath,
		stateMachine: newTestStateMachine(t, statePath),
	}
	c.rebuildService(t, node)
	c.nodes = append(c.nodes, node)
	return node
}
```

- [ ] **Step 2: Run tests and confirm red**

Run:

```bash
GOWORK=off /usr/local/go/bin/go test ./pkg/controllerv2/raft -run 'TestControllerRaftAddsLearnerThenPromotesVoter|TestControllerRaftMembershipStatusReportsVotersAndLearners' -count=1
```

Expected: fail with undefined membership methods and status fields.

- [ ] **Step 3: Add membership result types and service methods**

Create `pkg/controllerv2/raft/membership.go`:

```go
package raft

import (
	"context"

	"go.etcd.io/raft/v3/raftpb"
)

// MembershipChangeResult describes one committed Controller Raft membership change.
type MembershipChangeResult struct {
	// Index is the committed Raft log index of the membership change.
	Index uint64
	// ConfState is the Raft configuration after applying the committed change.
	ConfState raftpb.ConfState
}

// AddLearner adds nodeID as a non-voting Controller Raft learner.
func (s *Service) AddLearner(ctx context.Context, nodeID uint64) (MembershipChangeResult, error) {
	return s.submitMembershipChange(ctx, raftpb.ConfChange{
		Type:   raftpb.ConfChangeAddLearnerNode,
		NodeID: nodeID,
	})
}

// PromoteLearner promotes nodeID from learner to Controller Raft voter.
func (s *Service) PromoteLearner(ctx context.Context, nodeID uint64) (MembershipChangeResult, error) {
	return s.submitMembershipChange(ctx, raftpb.ConfChange{
		Type:   raftpb.ConfChangeAddNode,
		NodeID: nodeID,
	})
}
```

In `pkg/controllerv2/raft/service.go`, extend `proposalRequest` and responses:

```go
type proposalRequest struct {
	ctx        context.Context
	cmd        command.Command
	confChange *raftpb.ConfChange
	probe      bool
	resp       chan proposalResponse
}

type proposalResponse struct {
	result     ProposalResult
	membership MembershipChangeResult
	err        error
}
```

Add submit helper:

```go
func (s *Service) submitMembershipChange(ctx context.Context, cc raftpb.ConfChange) (MembershipChangeResult, error) {
	if cc.NodeID == 0 {
		return MembershipChangeResult{}, ErrInvalidConfig
	}
	resp, err := s.submitProposal(ctx, proposalRequest{confChange: &cc})
	if err != nil {
		return MembershipChangeResult{}, err
	}
	return resp.membership, nil
}
```

Adjust `submitProposal` to return `proposalResponse` internally while preserving `ProposeResult` public behavior. The public `ProposeResult` returns `response.result`.

- [ ] **Step 4: Complete ConfChange proposal in the run loop**

In `pkg/controllerv2/raft/service_run.go`, change the proposal case to branch on `req.confChange`:

```go
case req := <-proposalCh:
	if err := req.ctx.Err(); err != nil {
		req.resp <- proposalResponse{err: err}
		continue
	}
	if rawNode.Status().RaftState != etcdraft.StateLeader {
		req.resp <- proposalResponse{err: ErrNotLeader}
		continue
	}
	if req.confChange != nil {
		if err := rawNode.ProposeConfChange(*req.confChange); err != nil {
			req.resp <- proposalResponse{err: err}
			continue
		}
		trackerMu.Lock()
		tracker.enqueue(trackedProposal{resp: req.resp})
		trackerMu.Unlock()
		continue
	}
	var data []byte
	if !req.probe {
		encoded, err := command.Encode(req.cmd)
		if err != nil {
			req.resp <- proposalResponse{err: err}
			continue
		}
		data = encoded
	}
	if err := rawNode.Propose(data); err != nil {
		req.resp <- proposalResponse{err: err}
		continue
	}
	trackerMu.Lock()
	tracker.enqueue(trackedProposal{resp: req.resp, probe: req.probe})
	trackerMu.Unlock()
```

Extend the apply scheduler ConfChange completion path so the tracker receives:

```go
if s.complete != nil {
	s.complete(entry.Index, ProposalResult{Noop: true, AppliedRaftIndex: entry.Index}, nil)
}
```

Then update `proposal_tracker.go` so ConfChange completions can attach `MembershipChangeResult{Index: entry.Index, ConfState: result.state}`. Keep normal command behavior unchanged.

- [ ] **Step 5: Add ConfState to status**

In `pkg/controllerv2/raft/status.go`, add:

```go
// Voters is the current Controller Raft voting set observed by the local RawNode.
Voters []uint64
// Learners is the current Controller Raft learner set observed by the local RawNode.
Learners []uint64
```

In `pkg/controllerv2/raft/service_helpers.go`, update `updateStatus`:

```go
st.Voters = raftStatusVoters(status)
st.Learners = raftStatusLearners(status)
```

Add:

```go
func raftStatusVoters(status etcdraft.Status) []uint64 {
	voters := make([]uint64, 0, status.Config.Voters.IDs().Len())
	for _, id := range status.Config.Voters.IDs() {
		voters = append(voters, uint64(id))
	}
	return voters
}

func raftStatusLearners(status etcdraft.Status) []uint64 {
	learners := make([]uint64, 0, len(status.Config.Learners))
	for id := range status.Config.Learners {
		learners = append(learners, uint64(id))
	}
	return learners
}
```

- [ ] **Step 6: Run focused membership tests**

Run:

```bash
GOWORK=off /usr/local/go/bin/go test ./pkg/controllerv2/raft -run 'TestControllerRaftAddsLearnerThenPromotesVoter|TestControllerRaftMembershipStatusReportsVotersAndLearners' -count=1
```

Expected: PASS.

- [ ] **Step 7: Run full ControllerV2 raft tests**

Run:

```bash
GOWORK=off /usr/local/go/bin/go test ./pkg/controllerv2/raft -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit Task 2**

Run:

```bash
git add pkg/controllerv2/raft
git commit -m "feat: expose controller raft membership changes"
```

Expected: commit succeeds and stages only raft package files.

### Task 3: ControllerV2 Runtime Promotion And Mirror Preparation

**Files:**
- Create: `pkg/controllerv2/runtime_controller_voter_promotion.go`
- Modify: `pkg/controllerv2/runtime.go`
- Modify: `pkg/controllerv2/runtime_start.go`
- Modify: `pkg/controllerv2/types.go`
- Modify: `pkg/controllerv2/runtime_test.go`

- [ ] **Step 1: Write failing runtime tests**

Add tests to `pkg/controllerv2/runtime_test.go`:

```go
func TestRuntimePromoteControllerVoterRequiresLiveVoterProof(t *testing.T) {
	runtime := startedSingleControllerRuntime(t)
	_, err := runtime.JoinNode(context.Background(), JoinNodeRequest{NodeID: 4, Addr: "n4", Roles: []NodeRole{NodeRoleData}, CapacityWeight: 1})
	require.NoError(t, err)
	_, err = runtime.ActivateNode(context.Background(), ActivateNodeRequest{NodeID: 4})
	require.NoError(t, err)

	st, err := runtime.LocalState(context.Background())
	require.NoError(t, err)
	_, err = runtime.PromoteControllerVoter(context.Background(), PromoteControllerVoterRequest{
		NodeID:           4,
		Addr:             "n4",
		ExpectedRevision: st.Revision,
		ObservedConfigIndex: 0,
		ObservedVoters:      []uint64{1, 4},
	})
	require.ErrorIs(t, err, ErrProposalRejected)
}

func TestRuntimePromoteControllerVoterCommitsStateAfterProof(t *testing.T) {
	runtime := startedSingleControllerRuntime(t)
	_, err := runtime.JoinNode(context.Background(), JoinNodeRequest{NodeID: 4, Addr: "n4", Roles: []NodeRole{NodeRoleData}, CapacityWeight: 1})
	require.NoError(t, err)
	_, err = runtime.ActivateNode(context.Background(), ActivateNodeRequest{NodeID: 4})
	require.NoError(t, err)
	st, err := runtime.LocalState(context.Background())
	require.NoError(t, err)

	result, err := runtime.PromoteControllerVoter(context.Background(), PromoteControllerVoterRequest{
		NodeID:              4,
		Addr:                "n4",
		ExpectedRevision:    st.Revision,
		ExpectedVoters:      []uint64{1},
		ObservedConfigIndex: 7,
		ObservedVoters:      []uint64{1, 4},
	})
	require.NoError(t, err)
	require.True(t, result.Changed)
	require.Equal(t, []uint64{1, 4}, result.NextVoters)

	after, err := runtime.LocalState(context.Background())
	require.NoError(t, err)
	require.True(t, findRuntimeNode(t, after, 4).HasRole(NodeRoleControllerVoter))
}

func TestRuntimePrepareControllerVoterMovesMirrorStateAside(t *testing.T) {
	dir := t.TempDir()
	store := statefile.New(filepath.Join(dir, "cluster-state.json"))
	mirrored := testRuntimeClusterState("wk-prepare", 1)
	mirrored.AppliedRaftIndex = 21
	require.NoError(t, store.Save(context.Background(), mirrored))

	runtime, err := NewRuntime(RuntimeConfig{
		NodeID:    4,
		Addr:      "n4",
		StateDir:  dir,
		ClusterID: "wk-prepare",
		Role:      RuntimeRoleMirror,
		Voters:    []Voter{{NodeID: 1, Addr: "n1"}, {NodeID: 4, Addr: "n4"}},
		SyncClient: &SyncClient{},
	})
	require.NoError(t, err)

	result, err := runtime.PrepareControllerVoter(context.Background(), PrepareControllerVoterRequest{
		NodeID:           4,
		ClusterID:        "wk-prepare",
		ExpectedRevision: 1,
		NextVoters:       []Voter{{NodeID: 1, Addr: "n1"}, {NodeID: 4, Addr: "n4"}},
	})
	require.NoError(t, err)
	require.True(t, result.Prepared)
	require.FileExists(t, filepath.Join(dir, "cluster-state.mirror-before-controller-voter-promotion.json"))
}
```

- [ ] **Step 2: Run tests and confirm red**

Run:

```bash
GOWORK=off /usr/local/go/bin/go test ./pkg/controllerv2 -run 'TestRuntimePromoteControllerVoter|TestRuntimePrepareControllerVoter' -count=1
```

Expected: fail with undefined runtime methods and types.

- [ ] **Step 3: Add public runtime types**

In `pkg/controllerv2/types.go`, export:

```go
// PromoteControllerVoterRequest finalizes one proven Controller voter promotion in ControllerV2 state.
type PromoteControllerVoterRequest struct {
	// NodeID is the target node.
	NodeID uint64
	// Addr is the stable control-plane address expected for the target node.
	Addr string
	// ExpectedRevision fences the final durable state command.
	ExpectedRevision uint64
	// ExpectedVoters is the Controller voter set observed before live promotion.
	ExpectedVoters []uint64
	// ObservedConfigIndex is the live Controller Raft config index proving voter membership.
	ObservedConfigIndex uint64
	// ObservedVoters is the live Controller Raft voter set after learner promotion.
	ObservedVoters []uint64
}

// PromoteControllerVoterResult describes the durable state after final promotion.
type PromoteControllerVoterResult struct {
	// Changed reports whether the final state command advanced cluster-state revision.
	Changed bool
	// Node is the durable node record after promotion.
	Node Node
	// Revision is the resulting ControllerV2 state revision.
	Revision uint64
	// PreviousVoters is the voter set observed before promotion.
	PreviousVoters []uint64
	// NextVoters is the voter set after promotion.
	NextVoters []uint64
}

// PrepareControllerVoterRequest asks a mirror node to become ready for Controller Raft learner traffic.
type PrepareControllerVoterRequest struct {
	// NodeID is the local target node.
	NodeID uint64
	// ClusterID is the expected cluster identity.
	ClusterID string
	// ExpectedRevision is the mirrored control revision that must be visible before preparation.
	ExpectedRevision uint64
	// NextVoters includes the existing voters plus this target.
	NextVoters []Voter
}

// PrepareControllerVoterResult reports target-side preparation state.
type PrepareControllerVoterResult struct {
	// Prepared reports whether this call created or confirmed a learner-ready local runtime.
	Prepared bool
	// StateRevision is the mirrored revision preserved for foreground route continuity.
	StateRevision uint64
}
```

- [ ] **Step 4: Implement final state command facade**

Create `pkg/controllerv2/runtime_controller_voter_promotion.go`:

```go
package controllerv2

import (
	"context"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/command"
)

// PromoteControllerVoter commits the durable ControllerV2 role update after live Raft voter proof exists.
func (r *Runtime) PromoteControllerVoter(ctx context.Context, req PromoteControllerVoterRequest) (PromoteControllerVoterResult, error) {
	if err := ctxErr(ctx); err != nil {
		return PromoteControllerVoterResult{}, err
	}
	if r == nil || r.raft == nil {
		return PromoteControllerVoterResult{}, ErrNotStarted
	}
	st, err := r.LocalState(ctx)
	if err != nil {
		return PromoteControllerVoterResult{}, err
	}
	node, ok := findLifecycleNode(st, req.NodeID)
	if !ok {
		return PromoteControllerVoterResult{}, ErrNodeLifecycleNotFound
	}
	if req.Addr == "" {
		req.Addr = node.Addr
	}
	expectedRevision := req.ExpectedRevision
	if expectedRevision == 0 {
		expectedRevision = st.Revision
	}
	proposal, err := r.raft.ProposeResult(ctx, command.Command{
		Kind:             command.KindPromoteControllerVoter,
		IssuedAt:         r.cfg.Now().UTC(),
		ExpectedRevision: &expectedRevision,
		ControllerVoterPromotion: &command.ControllerVoterPromotion{
			TargetNodeID:           req.NodeID,
			TargetAddr:             req.Addr,
			ExpectedPreviousVoters: append([]uint64(nil), req.ExpectedVoters...),
			ObservedConfigIndex:    req.ObservedConfigIndex,
			ObservedVoters:         append([]uint64(nil), req.ObservedVoters...),
		},
	})
	if err != nil {
		return PromoteControllerVoterResult{}, err
	}
	if err := r.publishFromState(ctx); err != nil {
		return PromoteControllerVoterResult{}, err
	}
	updated, err := r.LocalState(ctx)
	if err != nil {
		return PromoteControllerVoterResult{}, err
	}
	finalNode, ok := findLifecycleNode(updated, req.NodeID)
	if !ok {
		return PromoteControllerVoterResult{}, fmt.Errorf("controllerv2: node %d not found after controller voter promotion", req.NodeID)
	}
	return PromoteControllerVoterResult{
		Changed:        proposal.Changed,
		Node:           finalNode,
		Revision:       updated.Revision,
		PreviousVoters: append([]uint64(nil), req.ExpectedVoters...),
		NextVoters:     controllerVoterIDsFromState(updated),
	}, nil
}

func controllerVoterIDsFromState(st ClusterState) []uint64 {
	out := make([]uint64, 0, len(st.Controllers))
	for _, controller := range st.Controllers {
		out = append(out, controller.NodeID)
	}
	return out
}
```

- [ ] **Step 5: Implement mirror preparation conservatively**

In `pkg/controllerv2/runtime_controller_voter_promotion.go`, add:

```go
// PrepareControllerVoter stops mirror refresh and starts a Raft participant ready for learner traffic.
func (r *Runtime) PrepareControllerVoter(ctx context.Context, req PrepareControllerVoterRequest) (PrepareControllerVoterResult, error) {
	if err := ctxErr(ctx); err != nil {
		return PrepareControllerVoterResult{}, err
	}
	if r == nil {
		return PrepareControllerVoterResult{}, ErrNotStarted
	}
	if req.NodeID == 0 || req.NodeID != r.cfg.NodeID || req.ClusterID == "" || req.ClusterID != r.cfg.ClusterID {
		return PrepareControllerVoterResult{}, fmt.Errorf("controllerv2: invalid controller voter preparation request")
	}
	st, err := r.LocalState(ctx)
	if err != nil {
		return PrepareControllerVoterResult{}, err
	}
	if st.Revision < req.ExpectedRevision {
		return PrepareControllerVoterResult{}, ErrExpectedRevisionMismatch
	}
	if r.raft != nil {
		return PrepareControllerVoterResult{Prepared: true, StateRevision: st.Revision}, nil
	}
	if r.refreshCancel != nil {
		r.refreshCancel()
		r.refreshWG.Wait()
		r.refreshCancel = nil
	}
	if err := r.moveMirrorStateAside(); err != nil {
		return PrepareControllerVoterResult{}, err
	}
	r.cfg.Role = RuntimeRoleVoter
	r.cfg.Voters = append([]Voter(nil), req.NextVoters...)
	r.cfg.AllowBootstrap = false
	if err := r.startVoter(ctx); err != nil {
		return PrepareControllerVoterResult{}, err
	}
	return PrepareControllerVoterResult{Prepared: true, StateRevision: st.Revision}, nil
}
```

Add `moveMirrorStateAside` with atomic rename of `cluster-state.json` to `cluster-state.mirror-before-controller-voter-promotion.json`. If the backup path already exists, keep it and remove the active mirror file before voter start.

- [ ] **Step 6: Run runtime tests**

Run:

```bash
GOWORK=off /usr/local/go/bin/go test ./pkg/controllerv2 -run 'TestRuntimePromoteControllerVoter|TestRuntimePrepareControllerVoter' -count=1
```

Expected: PASS.

- [ ] **Step 7: Run ControllerV2 core package tests**

Run:

```bash
GOWORK=off /usr/local/go/bin/go test ./pkg/controllerv2 ./pkg/controllerv2/raft ./pkg/controllerv2/fsm -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit Task 3**

Run:

```bash
git add pkg/controllerv2
git commit -m "feat: prepare and finalize controller voter promotion"
```

Expected: commit succeeds and stages only ControllerV2 files.

### Task 4: clusterv2 Control Writer And Target Prepare RPC

**Files:**
- Create: `pkg/clusterv2/control/controller_voter_promotion.go`
- Modify: `pkg/clusterv2/control/codec.go`
- Modify: `pkg/clusterv2/control/control_write.go`
- Modify: `pkg/clusterv2/control/runtime.go`
- Modify: `pkg/clusterv2/control/log_entries.go`
- Modify: `pkg/clusterv2/control/codec_test.go`
- Modify: `pkg/clusterv2/control/transport_test.go`
- Modify: `pkg/clusterv2/control/runtime_test.go`
- Modify: `pkg/clusterv2/node_management.go`
- Modify: `pkg/clusterv2/node_logs.go`
- Modify: `pkg/clusterv2/node_management_test.go`
- Modify: `pkg/clusterv2/node_logs_test.go`

- [ ] **Step 1: Write failing control codec and handler tests**

Add to `pkg/clusterv2/control/codec_test.go`:

```go
func TestControlWriteRequestCodecRoundTripPromoteControllerVoter(t *testing.T) {
	req := ControlWriteRequest{
		Action: ControlWriteActionPromoteControllerVoter,
		PromoteControllerVoter: PromoteControllerVoterRequest{
			NodeID:           4,
			ExpectedRevision: 9,
		},
	}
	payload, err := EncodeControlWriteRequest(req)
	require.NoError(t, err)
	got, err := DecodeControlWriteRequest(payload)
	require.NoError(t, err)
	require.Equal(t, req, got)
	require.Contains(t, string(payload), "promote_controller_voter")
	require.Contains(t, string(payload), "expected_revision")
}
```

Add to `pkg/clusterv2/control/transport_test.go`:

```go
func TestNewControlWriteHandlerCallsPromoteControllerVoter(t *testing.T) {
	applier := &recordingControlWriteApplier{
		promoteControllerVoterResult: PromoteControllerVoterResult{
			Changed: true,
			Node: Node{NodeID: 4, Addr: "n4", Roles: []Role{RoleData, RoleController}},
			Revision: 10,
			PreviousVoters: []uint64{1, 2, 3},
			NextVoters: []uint64{1, 2, 3, 4},
			Warnings: []string{"controller_voter_count_even"},
		},
	}
	network := clusternet.NewMemoryNetwork()
	network.Register(1, clusternet.RPCControlWrite, NewControlWriteHandler(applier))
	client := NewControlWriteClient(network)

	result, err := client.Submit(context.Background(), 1, ControlWriteRequest{
		Action: ControlWriteActionPromoteControllerVoter,
		PromoteControllerVoter: PromoteControllerVoterRequest{NodeID: 4, ExpectedRevision: 9},
	})
	require.NoError(t, err)
	require.Equal(t, PromoteControllerVoterRequest{NodeID: 4, ExpectedRevision: 9}, applier.promoteControllerVoterReq)
	require.Equal(t, applier.promoteControllerVoterResult, result.PromoteControllerVoter)
}
```

- [ ] **Step 2: Run tests and confirm red**

Run:

```bash
GOWORK=off /usr/local/go/bin/go test ./pkg/clusterv2/control -run 'TestControlWriteRequestCodecRoundTripPromoteControllerVoter|TestNewControlWriteHandlerCallsPromoteControllerVoter' -count=1
```

Expected: fail with undefined control action and result fields.

- [ ] **Step 3: Add control request and result types**

Create `pkg/clusterv2/control/controller_voter_promotion.go`:

```go
package control

import cv2 "github.com/WuKongIM/WuKongIM/pkg/controllerv2"

// PromoteControllerVoterRequest requests online promotion of an active data node into Controller Raft voting membership.
type PromoteControllerVoterRequest struct {
	// NodeID is the target node.
	NodeID uint64 `json:"node_id"`
	// ExpectedRevision fences the manager intent to the observed control revision.
	ExpectedRevision uint64 `json:"expected_revision,omitempty"`
}

// PromoteControllerVoterResult describes the control-state result of promotion.
type PromoteControllerVoterResult struct {
	// Changed reports whether durable ControllerV2 state changed.
	Changed bool `json:"changed"`
	// Node is the promoted node record.
	Node Node `json:"node"`
	// Revision is the resulting control-state revision.
	Revision uint64 `json:"revision"`
	// PreviousVoters is the voter set before promotion.
	PreviousVoters []uint64 `json:"previous_voters,omitempty"`
	// NextVoters is the voter set after promotion.
	NextVoters []uint64 `json:"next_voters,omitempty"`
	// Warnings contains bounded operator warnings.
	Warnings []string `json:"warnings,omitempty"`
}

func promoteControllerVoterResultFromCV2(result cv2.PromoteControllerVoterResult) PromoteControllerVoterResult {
	return PromoteControllerVoterResult{
		Changed:        result.Changed,
		Node:           controlNodeFromControllerNode(result.Node),
		Revision:       result.Revision,
		PreviousVoters: append([]uint64(nil), result.PreviousVoters...),
		NextVoters:     append([]uint64(nil), result.NextVoters...),
		Warnings:       controllerVoterPromotionWarnings(result.NextVoters),
	}
}

func controllerVoterPromotionWarnings(voters []uint64) []string {
	if len(voters) > 0 && len(voters)%2 == 0 {
		return []string{"controller_voter_count_even"}
	}
	return nil
}
```

- [ ] **Step 4: Wire generic control write codec and handler**

In `pkg/clusterv2/control/codec.go`, add:

```go
// ControlWriteActionPromoteControllerVoter promotes one active node into Controller Raft voting membership.
ControlWriteActionPromoteControllerVoter ControlWriteAction = "promote_controller_voter"
```

Add request/response fields:

```go
PromoteControllerVoter PromoteControllerVoterRequest `json:"promote_controller_voter,omitempty"`
PromoteControllerVoter *PromoteControllerVoterRequest `json:"promote_controller_voter,omitempty"`
PromoteControllerVoter PromoteControllerVoterResult `json:"promote_controller_voter,omitempty"`
PromoteControllerVoter *PromoteControllerVoterResult `json:"promote_controller_voter,omitempty"`
```

Update `MarshalJSON` request and response branches to include the new action and result.

In `pkg/clusterv2/control/control_write.go`, extend `ControlWriteApplier`:

```go
// PromoteControllerVoter submits an online Controller voter promotion.
PromoteControllerVoter(context.Context, PromoteControllerVoterRequest) (PromoteControllerVoterResult, error)
```

Add handler branch:

```go
case ControlWriteActionPromoteControllerVoter:
	result, err := applier.PromoteControllerVoter(ctx, req.PromoteControllerVoter)
	if err != nil {
		return encodeControlWriteErrorResponse(err)
	}
	resp.PromoteControllerVoter = result
```

- [ ] **Step 5: Implement control runtime forwarding**

In `pkg/clusterv2/control/runtime.go`, add:

```go
// PromoteControllerVoter promotes one active node into ControllerV2 voting membership.
func (r *Runtime) PromoteControllerVoter(ctx context.Context, req PromoteControllerVoterRequest) (PromoteControllerVoterResult, error) {
	if err := ctxErr(ctx); err != nil {
		return PromoteControllerVoterResult{}, err
	}
	if r == nil || r.backend == nil {
		return PromoteControllerVoterResult{}, cv2.ErrNotStarted
	}
	if r.canForwardControlWriteToLeader() {
		resp, err := r.forwardControlWrite(ctx, ControlWriteRequest{
			Action: ControlWriteActionPromoteControllerVoter,
			PromoteControllerVoter: req,
		})
		if err != nil {
			return PromoteControllerVoterResult{}, err
		}
		return resp.PromoteControllerVoter, nil
	}
	result, err := r.backend.PromoteControllerVoter(ctx, cv2.PromoteControllerVoterRequest{
		NodeID:           req.NodeID,
		ExpectedRevision: req.ExpectedRevision,
	})
	if shouldForwardControlWrite(err) {
		resp, err := r.forwardControlWriteAfterError(ctx, ControlWriteRequest{
			Action: ControlWriteActionPromoteControllerVoter,
			PromoteControllerVoter: req,
		}, err)
		if err != nil {
			return PromoteControllerVoterResult{}, err
		}
		return resp.PromoteControllerVoter, nil
	}
	if err != nil {
		return PromoteControllerVoterResult{}, err
	}
	return promoteControllerVoterResultFromCV2(result), nil
}
```

- [ ] **Step 6: Run clusterv2 control tests**

Run:

```bash
GOWORK=off /usr/local/go/bin/go test ./pkg/clusterv2/control -run 'PromoteControllerVoter|ControlWrite' -count=1
```

Expected: PASS.

- [ ] **Step 7: Expose root Node facade**

In `pkg/clusterv2/node_management.go`, add:

```go
// PromoteControllerVoter promotes one active non-Controller node into Controller Raft voting membership.
func (n *Node) PromoteControllerVoter(ctx context.Context, req control.PromoteControllerVoterRequest) (control.PromoteControllerVoterResult, error) {
	if err := n.ensureStarted(ctx); err != nil {
		return control.PromoteControllerVoterResult{}, err
	}
	return n.control.PromoteControllerVoter(ctx, req)
}
```

Add a focused unit test in `pkg/clusterv2/node_management_test.go` that injects a fake control runtime, calls `Node.PromoteControllerVoter`, and asserts the request was delegated unchanged.

- [ ] **Step 8: Run clusterv2 package tests**

Run:

```bash
GOWORK=off /usr/local/go/bin/go test ./pkg/clusterv2/control ./pkg/clusterv2 -run 'PromoteControllerVoter|ControlWrite|ControllerRaftStatus' -count=1
```

Expected: PASS.

- [ ] **Step 9: Commit Task 4**

Run:

```bash
git add pkg/clusterv2/control pkg/clusterv2/node_management.go pkg/clusterv2/node_management_test.go pkg/clusterv2/node_logs.go pkg/clusterv2/node_logs_test.go
git commit -m "feat: route controller voter promotion through clusterv2"
```

Expected: commit succeeds and stages only clusterv2 files.

### Task 5: internalv2 Management Usecase And Infra Ports

**Files:**
- Create: `internalv2/usecase/management/controller_voter_promotion.go`
- Create: `internalv2/usecase/management/controller_voter_promotion_test.go`
- Modify: `internalv2/usecase/management/nodes.go`
- Modify: `internalv2/infra/cluster/node_lifecycle.go`
- Modify: `internalv2/infra/cluster/management_node_lifecycle.go`
- Modify: `internalv2/infra/cluster/node_lifecycle_test.go`
- Modify: `internalv2/infra/cluster/management_node_lifecycle_test.go`

- [ ] **Step 1: Write failing usecase tests**

Create `internalv2/usecase/management/controller_voter_promotion_test.go`:

```go
package management

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
	"github.com/stretchr/testify/require"
)

func TestPromoteControllerVoterValidatesReadinessAndCallsWriter(t *testing.T) {
	snapshot := controllerPromotionSnapshot()
	writer := &fakeControllerVoterPromoter{}
	app := New(Options{
		Cluster: controllerPromotionCluster{snapshot: snapshot},
		ControllerVoterPromoter: writer,
		ControllerVoterReadiness: fakeControllerVoterReadinessReader{resp: ControllerVoterReadiness{
			NodeID: 4, ClusterID: "wk", Reachable: true, TransportReady: true, ControlReady: true,
			RuntimeReady: true, CanPrepare: true, MirrorRevision: snapshot.Revision,
		}},
		Now: func() time.Time { return time.Unix(100, 0) },
	})

	resp, err := app.PromoteControllerVoter(context.Background(), PromoteControllerVoterRequest{NodeID: 4, ExpectedRevision: snapshot.Revision})
	require.NoError(t, err)
	require.True(t, resp.Changed)
	require.Equal(t, control.PromoteControllerVoterRequest{NodeID: 4, ExpectedRevision: snapshot.Revision}, writer.req)
}

func TestPromoteControllerVoterRejectsStaleHealth(t *testing.T) {
	snapshot := controllerPromotionSnapshot()
	snapshot.Nodes[1].Health.Freshness = control.NodeHealthStale
	app := New(Options{
		Cluster: controllerPromotionCluster{snapshot: snapshot},
		ControllerVoterPromoter: &fakeControllerVoterPromoter{},
		ControllerVoterReadiness: fakeControllerVoterReadinessReader{},
	})

	_, err := app.PromoteControllerVoter(context.Background(), PromoteControllerVoterRequest{NodeID: 4, ExpectedRevision: snapshot.Revision})
	require.ErrorIs(t, err, ErrControllerVoterPromotionBlocked)
	require.Contains(t, err.Error(), "target_health_stale")
}

func TestPromoteControllerVoterAlreadyVoterReturnsNoop(t *testing.T) {
	snapshot := controllerPromotionSnapshot()
	snapshot.Nodes[1].Roles = []control.Role{control.RoleData, control.RoleController}
	app := New(Options{Cluster: controllerPromotionCluster{snapshot: snapshot}})

	resp, err := app.PromoteControllerVoter(context.Background(), PromoteControllerVoterRequest{NodeID: 4})
	require.NoError(t, err)
	require.False(t, resp.Changed)
	require.Equal(t, []uint64{1, 4}, resp.NextVoters)
}

func TestPromoteControllerVoterRejectsExpectedRevisionMismatch(t *testing.T) {
	snapshot := controllerPromotionSnapshot()
	app := New(Options{Cluster: controllerPromotionCluster{snapshot: snapshot}})

	_, err := app.PromoteControllerVoter(context.Background(), PromoteControllerVoterRequest{NodeID: 4, ExpectedRevision: snapshot.Revision + 1})
	require.ErrorIs(t, err, ErrControllerVoterPromotionBlocked)
	require.Contains(t, err.Error(), "expected_revision_mismatch")
}

type fakeControllerVoterPromoter struct {
	req control.PromoteControllerVoterRequest
	res control.PromoteControllerVoterResult
	err error
}

func (f *fakeControllerVoterPromoter) PromoteControllerVoter(_ context.Context, req control.PromoteControllerVoterRequest) (control.PromoteControllerVoterResult, error) {
	f.req = req
	if f.err != nil {
		return control.PromoteControllerVoterResult{}, f.err
	}
	if f.res.Revision == 0 {
		f.res = control.PromoteControllerVoterResult{Changed: true, Revision: req.ExpectedRevision + 1, PreviousVoters: []uint64{1}, NextVoters: []uint64{1, req.NodeID}}
	}
	return f.res, nil
}

type fakeControllerVoterReadinessReader struct {
	resp ControllerVoterReadiness
	err error
}

func (f fakeControllerVoterReadinessReader) ControllerVoterReadiness(_ context.Context, nodeID uint64) (ControllerVoterReadiness, error) {
	if f.err != nil {
		return ControllerVoterReadiness{}, f.err
	}
	f.resp.NodeID = nodeID
	return f.resp, nil
}

type controllerPromotionCluster struct {
	snapshot control.Snapshot
}

func (c controllerPromotionCluster) NodeID() uint64 { return 1 }
func (c controllerPromotionCluster) LocalControlSnapshot(context.Context) (control.Snapshot, error) {
	return c.snapshot, nil
}

func controllerPromotionSnapshot() control.Snapshot {
	now := time.Unix(100, 0)
	return control.Snapshot{
		ClusterID: "wk",
		Revision: 8,
		ControllerID: 1,
		Nodes: []control.Node{
			{NodeID: 1, Addr: "n1", Roles: []control.Role{control.RoleData, control.RoleController}, JoinState: control.NodeJoinStateActive, Status: control.NodeAlive},
			{NodeID: 4, Addr: "n4", Roles: []control.Role{control.RoleData}, JoinState: control.NodeJoinStateActive, Status: control.NodeAlive, Health: control.NodeHealth{Freshness: control.NodeHealthFresh, Status: control.NodeAlive, RuntimeReady: true, ObservedControlRevision: 8, ReportedAt: now}},
		},
	}
}

var errControllerPromotionTest = errors.New("controller promotion test")
```

- [ ] **Step 2: Run tests and confirm red**

Run:

```bash
GOWORK=off /usr/local/go/bin/go test ./internalv2/usecase/management -run 'TestPromoteControllerVoter' -count=1
```

Expected: fail with undefined options, types, and errors.

- [ ] **Step 3: Add usecase ports, errors, request, response**

In `internalv2/usecase/management/nodes.go`, add to `Options` and `App`:

```go
// ControllerVoterPromoter submits Controller voter promotion writes.
ControllerVoterPromoter ControllerVoterPromoter
// ControllerVoterReadiness reads target readiness for Controller voter promotion.
ControllerVoterReadiness ControllerVoterReadinessReader
```

Add interfaces:

```go
// ControllerVoterPromoter submits cluster-authoritative Controller voter promotion writes.
type ControllerVoterPromoter interface {
	PromoteControllerVoter(context.Context, control.PromoteControllerVoterRequest) (control.PromoteControllerVoterResult, error)
}

// ControllerVoterReadinessReader reads target readiness before Controller voter promotion.
type ControllerVoterReadinessReader interface {
	ControllerVoterReadiness(context.Context, uint64) (ControllerVoterReadiness, error)
}
```

Create `internalv2/usecase/management/controller_voter_promotion.go`:

```go
package management

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
)

var (
	// ErrControllerVoterPromotionUnavailable reports missing promotion infrastructure.
	ErrControllerVoterPromotionUnavailable = errors.New("controller voter promotion unavailable")
	// ErrControllerVoterPromotionBlocked reports a fail-closed promotion blocker.
	ErrControllerVoterPromotionBlocked = errors.New("controller voter promotion blocked")
)

// PromoteControllerVoterRequest is the manager-facing promotion intent.
type PromoteControllerVoterRequest struct {
	// NodeID is the target node.
	NodeID uint64
	// ExpectedRevision fences the request to the operator's observed state.
	ExpectedRevision uint64
}

// PromoteControllerVoterResponse is the manager-facing promotion result.
type PromoteControllerVoterResponse struct {
	Changed bool
	NodeID uint64
	StateRevision uint64
	PreviousVoters []uint64
	NextVoters []uint64
	Warnings []string
}

// ControllerVoterReadiness reports target-side promotion readiness.
type ControllerVoterReadiness struct {
	NodeID uint64
	ClusterID string
	Reachable bool
	TransportReady bool
	ControlReady bool
	RuntimeReady bool
	CanPrepare bool
	MirrorRevision uint64
	Unknown bool
	LastError string
}

// PromoteControllerVoter validates and submits one Controller voter promotion.
func (a *App) PromoteControllerVoter(ctx context.Context, req PromoteControllerVoterRequest) (PromoteControllerVoterResponse, error) {
	if req.NodeID == 0 {
		return PromoteControllerVoterResponse{}, fmt.Errorf("%w: invalid_node_id", ErrControllerVoterPromotionBlocked)
	}
	if a == nil || a.cluster == nil {
		return PromoteControllerVoterResponse{}, ErrControllerVoterPromotionUnavailable
	}
	snapshot, err := a.cluster.LocalControlSnapshot(ctx)
	if err != nil {
		return PromoteControllerVoterResponse{}, err
	}
	if req.ExpectedRevision != 0 && req.ExpectedRevision != snapshot.Revision {
		return PromoteControllerVoterResponse{}, fmt.Errorf("%w: expected_revision_mismatch", ErrControllerVoterPromotionBlocked)
	}
	node, ok := findControlNode(snapshot, req.NodeID)
	if !ok {
		return PromoteControllerVoterResponse{}, ErrNodeLifecycleNotFound
	}
	previous := controllerVotersFromSnapshot(snapshot)
	if hasControlRole(node.Roles, control.RoleController) {
		return PromoteControllerVoterResponse{Changed: false, NodeID: req.NodeID, StateRevision: snapshot.Revision, PreviousVoters: previous, NextVoters: previous}, nil
	}
	if blocker := controllerPromotionBlocker(snapshot, node); blocker != "" {
		return PromoteControllerVoterResponse{}, fmt.Errorf("%w: %s", ErrControllerVoterPromotionBlocked, blocker)
	}
	if a.controllerVoterReadiness == nil || a.controllerVoterPromoter == nil {
		return PromoteControllerVoterResponse{}, ErrControllerVoterPromotionUnavailable
	}
	readiness, err := a.controllerVoterReadiness.ControllerVoterReadiness(ctx, req.NodeID)
	if err != nil {
		return PromoteControllerVoterResponse{}, fmt.Errorf("%w: %v", ErrControllerVoterPromotionUnavailable, err)
	}
	if blocker := controllerPromotionReadinessBlocker(readiness, req.NodeID, snapshot.ClusterID, snapshot.Revision); blocker != "" {
		return PromoteControllerVoterResponse{}, fmt.Errorf("%w: %s", ErrControllerVoterPromotionBlocked, blocker)
	}
	result, err := a.controllerVoterPromoter.PromoteControllerVoter(ctx, control.PromoteControllerVoterRequest{NodeID: req.NodeID, ExpectedRevision: snapshot.Revision})
	if err != nil {
		return PromoteControllerVoterResponse{}, err
	}
	return PromoteControllerVoterResponse{
		Changed: result.Changed, NodeID: req.NodeID, StateRevision: result.Revision,
		PreviousVoters: append([]uint64(nil), result.PreviousVoters...),
		NextVoters: append([]uint64(nil), result.NextVoters...),
		Warnings: append([]string(nil), result.Warnings...),
	}, nil
}

func controllerPromotionBlocker(snapshot control.Snapshot, node control.Node) string {
	switch {
	case node.JoinState != control.NodeJoinStateActive:
		return "target_not_active"
	case strings.TrimSpace(node.Addr) == "":
		return "target_address_missing"
	case node.Health.Freshness != control.NodeHealthFresh:
		return "target_health_stale"
	case node.Health.Status != control.NodeAlive:
		return "target_not_alive"
	case !node.Health.RuntimeReady:
		return "target_runtime_not_ready"
	case node.Health.ObservedControlRevision < snapshot.Revision:
		return "target_revision_stale"
	default:
		return ""
	}
}

func controllerVotersFromSnapshot(snapshot control.Snapshot) []uint64 {
	out := make([]uint64, 0)
	for _, node := range snapshot.Nodes {
		if hasControlRole(node.Roles, control.RoleController) && node.JoinState == control.NodeJoinStateActive {
			out = append(out, node.NodeID)
		}
	}
	return out
}

func controllerPromotionReadinessBlocker(readiness ControllerVoterReadiness, nodeID uint64, clusterID string, revision uint64) string {
	switch {
	case readiness.Unknown:
		return "target_readiness_unknown"
	case readiness.NodeID != nodeID:
		return "target_readiness_node_mismatch"
	case readiness.ClusterID != "" && readiness.ClusterID != clusterID:
		return "target_cluster_mismatch"
	case !readiness.Reachable || !readiness.TransportReady || !readiness.ControlReady || !readiness.RuntimeReady:
		return "target_readiness_not_ready"
	case !readiness.CanPrepare:
		return "target_prepare_unavailable"
	case readiness.MirrorRevision < revision:
		return "target_revision_stale"
	default:
		return ""
	}
}
```

- [ ] **Step 4: Add action hint**

In `NodeActions`, add:

```go
// CanPromoteControllerVoter reports whether the node can be considered for Controller voter promotion.
CanPromoteControllerVoter bool
```

In `buildNode`, set:

```go
CanPromoteControllerVoter: opts.node.JoinState == control.NodeJoinStateActive && !controllerVoter,
```

- [ ] **Step 5: Run usecase tests**

Run:

```bash
GOWORK=off /usr/local/go/bin/go test ./internalv2/usecase/management -run 'TestPromoteControllerVoter|TestListNodes' -count=1
```

Expected: PASS.

- [ ] **Step 6: Wire infra adapter methods**

In `internalv2/infra/cluster/management_node_lifecycle.go`, add:

```go
// PromoteControllerVoter delegates Controller voter promotion to clusterv2.
func (m *Management) PromoteControllerVoter(ctx context.Context, req control.PromoteControllerVoterRequest) (control.PromoteControllerVoterResult, error) {
	if m == nil || m.node == nil {
		return control.PromoteControllerVoterResult{}, management.ErrControllerVoterPromotionUnavailable
	}
	return m.node.PromoteControllerVoter(ctx, req)
}
```

In `internalv2/infra/cluster/node_lifecycle.go`, add readiness adapter method that maps `accessnode.ControllerVoterReadinessResponse` to `management.ControllerVoterReadiness`.

- [ ] **Step 7: Run infra tests**

Run:

```bash
GOWORK=off /usr/local/go/bin/go test ./internalv2/infra/cluster -run 'ControllerVoter|NodeLifecycle' -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit Task 5**

Run:

```bash
git add internalv2/usecase/management internalv2/infra/cluster
git commit -m "feat: validate controller voter promotion in management usecase"
```

Expected: commit succeeds and stages only internalv2 usecase/infra files.

### Task 6: internalv2 Node RPC And App Wiring

**Files:**
- Modify: `internalv2/access/node/node_lifecycle_rpc.go`
- Modify: `internalv2/access/node/node_lifecycle_rpc_test.go`
- Modify: `internalv2/app/seed_join_loop.go`
- Modify: `internalv2/app/wiring.go`
- Modify: `internalv2/app/seed_join_loop_test.go`

- [ ] **Step 1: Write failing node RPC tests**

Add to `internalv2/access/node/node_lifecycle_rpc_test.go`:

```go
func TestNodeLifecycleClientControllerVoterReadiness(t *testing.T) {
	readiness := &fakeControllerVoterReadinessProvider{
		response: ControllerVoterReadinessResponse{NodeID: 4, ClusterID: "cluster-a", Reachable: true, CanPrepare: true, MirrorRevision: 9},
	}
	adapter := NewNodeLifecycleAdapter(NodeLifecycleOptions{ControllerVoterReadiness: readiness})
	network := clusternet.NewMemoryNetwork()
	network.Register(4, NodeLifecycleRPCServiceID, adapter)
	client := NewNodeLifecycleClient(network)

	got, err := client.ControllerVoterReadiness(context.Background(), 4, ControllerVoterReadinessRequest{NodeID: 4, ClusterID: "cluster-a"})
	require.NoError(t, err)
	require.Equal(t, readiness.response, got)
	require.Equal(t, ControllerVoterReadinessRequest{NodeID: 4, ClusterID: "cluster-a"}, readiness.request)
}

func TestNodeLifecycleClientPrepareControllerVoter(t *testing.T) {
	preparer := &fakeControllerVoterPreparer{
		response: PrepareControllerVoterResponse{NodeID: 4, Prepared: true, StateRevision: 9},
	}
	adapter := NewNodeLifecycleAdapter(NodeLifecycleOptions{ControllerVoterPreparer: preparer})
	network := clusternet.NewMemoryNetwork()
	network.Register(4, NodeLifecycleRPCServiceID, adapter)
	client := NewNodeLifecycleClient(network)

	req := PrepareControllerVoterRequest{NodeID: 4, ClusterID: "cluster-a", ExpectedRevision: 9, NextVoters: []ControllerVoter{{NodeID: 1, Addr: "n1"}, {NodeID: 4, Addr: "n4"}}}
	got, err := client.PrepareControllerVoter(context.Background(), 4, req)
	require.NoError(t, err)
	require.Equal(t, preparer.response, got)
	require.Equal(t, req, preparer.request)
}
```

- [ ] **Step 2: Run tests and confirm red**

Run:

```bash
GOWORK=off /usr/local/go/bin/go test ./internalv2/access/node -run 'ControllerVoter' -count=1
```

Expected: fail with undefined RPC operations and provider interfaces.

- [ ] **Step 3: Extend lifecycle RPC envelope**

In `internalv2/access/node/node_lifecycle_rpc.go`, add operations:

```go
nodeLifecycleOpControllerVoterReadiness = "controller_voter_readiness"
nodeLifecycleOpPrepareControllerVoter = "prepare_controller_voter"
```

Add request/response types:

```go
// ControllerVoterReadinessRequest asks a node whether it can prepare as a Controller voter.
type ControllerVoterReadinessRequest struct {
	NodeID uint64 `json:"node_id"`
	ClusterID string `json:"cluster_id"`
}

// ControllerVoterReadinessResponse reports target-side Controller voter preparation readiness.
type ControllerVoterReadinessResponse struct {
	NodeID uint64 `json:"node_id"`
	ClusterID string `json:"cluster_id"`
	Reachable bool `json:"reachable"`
	TransportReady bool `json:"transport_ready"`
	ControlReady bool `json:"control_ready"`
	RuntimeReady bool `json:"runtime_ready"`
	CanPrepare bool `json:"can_prepare"`
	MirrorRevision uint64 `json:"mirror_revision"`
	Unknown bool `json:"unknown"`
	LastError string `json:"last_error,omitempty"`
}

// ControllerVoter identifies a Controller voter endpoint for target preparation.
type ControllerVoter struct {
	NodeID uint64 `json:"node_id"`
	Addr string `json:"addr"`
}

// PrepareControllerVoterRequest asks a target node to start Controller Raft learner readiness.
type PrepareControllerVoterRequest struct {
	NodeID uint64 `json:"node_id"`
	ClusterID string `json:"cluster_id"`
	ExpectedRevision uint64 `json:"expected_revision"`
	NextVoters []ControllerVoter `json:"next_voters"`
}

// PrepareControllerVoterResponse reports the target preparation result.
type PrepareControllerVoterResponse struct {
	NodeID uint64 `json:"node_id"`
	Prepared bool `json:"prepared"`
	StateRevision uint64 `json:"state_revision"`
}
```

Add provider interfaces and client methods for both operations.

- [ ] **Step 4: Implement app readiness and prepare providers**

In `internalv2/app/seed_join_loop.go`, add:

```go
// ControllerVoterReadiness reports whether this node can prepare for Controller voter promotion.
func (a *App) ControllerVoterReadiness(ctx context.Context, req accessnode.ControllerVoterReadinessRequest) (accessnode.ControllerVoterReadinessResponse, error) {
	base, err := a.NodeReadiness(ctx, accessnode.NodeReadinessRequest{NodeID: req.NodeID, ClusterID: req.ClusterID})
	if err != nil {
		return accessnode.ControllerVoterReadinessResponse{}, err
	}
	return accessnode.ControllerVoterReadinessResponse{
		NodeID: base.NodeID, ClusterID: base.MirrorClusterID, Reachable: base.Reachable,
		TransportReady: base.TransportReady, ControlReady: base.ControlReady,
		RuntimeReady: base.RuntimeReady, CanPrepare: base.Ready, MirrorRevision: base.MirrorRevision,
		Unknown: base.Unknown, LastError: base.LastError,
	}, nil
}

// PrepareControllerVoter prepares this node for Controller Raft learner traffic.
func (a *App) PrepareControllerVoter(ctx context.Context, req accessnode.PrepareControllerVoterRequest) (accessnode.PrepareControllerVoterResponse, error) {
	if a == nil || a.cluster == nil {
		return accessnode.PrepareControllerVoterResponse{}, errors.New("app cluster not configured")
	}
	voters := make([]controllerv2.Voter, 0, len(req.NextVoters))
	for _, voter := range req.NextVoters {
		voters = append(voters, controllerv2.Voter{NodeID: voter.NodeID, Addr: voter.Addr})
	}
	result, err := a.cluster.PrepareControllerVoter(ctx, controllerv2.PrepareControllerVoterRequest{
		NodeID: req.NodeID, ClusterID: req.ClusterID, ExpectedRevision: req.ExpectedRevision, NextVoters: voters,
	})
	if err != nil {
		return accessnode.PrepareControllerVoterResponse{}, err
	}
	return accessnode.PrepareControllerVoterResponse{NodeID: req.NodeID, Prepared: result.Prepared, StateRevision: result.StateRevision}, nil
}
```

- [ ] **Step 5: Wire providers**

In `internalv2/app/wiring.go`, pass the app as `ControllerVoterReadiness` and `ControllerVoterPreparer` in the node lifecycle adapter options. Pass `ControllerVoterPromoter` and `ControllerVoterReadiness` into `management.New`.

- [ ] **Step 6: Run app and access-node tests**

Run:

```bash
GOWORK=off /usr/local/go/bin/go test ./internalv2/access/node ./internalv2/app -run 'ControllerVoter|NodeReadiness|Wiring' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit Task 6**

Run:

```bash
git add internalv2/access/node internalv2/app
git commit -m "feat: expose controller voter preparation RPC"
```

Expected: commit succeeds and stages only node RPC and app wiring files.

### Task 7: Manager HTTP Route And Node List DTO

**Files:**
- Create: `internalv2/access/manager/controller_voter_promotion.go`
- Modify: `internalv2/access/manager/server.go`
- Modify: `internalv2/access/manager/nodes.go`
- Modify: `internalv2/access/manager/server_test.go`
- Modify: `internalv2/access/manager/node_lifecycle_test.go`

- [ ] **Step 1: Write failing HTTP tests**

Add to `internalv2/access/manager/server_test.go`:

```go
func TestPromoteControllerVoterRouteRequiresControllerWritePermission(t *testing.T) {
	stub := &managerNodesStub{}
	srv := New(Options{
		Auth: AuthConfig{On: true, JWTSecret: "secret", Users: []UserConfig{{
			Username: "ops", Password: "pw",
			Permissions: []PermissionConfig{{Resource: "cluster.node", Actions: []string{"w"}}},
		}}},
		Management: stub,
	})
	token := loginTestToken(t, srv, "ops", "pw")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/4/controller-voter/promote", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+token)
	srv.Engine().ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestPromoteControllerVoterRouteAccepted(t *testing.T) {
	stub := &managerNodesStub{
		promoteControllerVoterResp: managementusecase.PromoteControllerVoterResponse{
			Changed: true, NodeID: 4, StateRevision: 10,
			PreviousVoters: []uint64{1, 2, 3}, NextVoters: []uint64{1, 2, 3, 4},
			Warnings: []string{"controller_voter_count_even"},
		},
	}
	srv := New(Options{Management: stub})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/4/controller-voter/promote", strings.NewReader(`{"expected_revision":9}`))
	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusAccepted, rec.Code)
	require.Equal(t, managementusecase.PromoteControllerVoterRequest{NodeID: 4, ExpectedRevision: 9}, stub.promoteControllerVoterReq)
	require.JSONEq(t, `{"changed":true,"node_id":4,"state_revision":10,"previous_voters":[1,2,3],"next_voters":[1,2,3,4],"warnings":["controller_voter_count_even"]}`, rec.Body.String())
}

func TestPromoteControllerVoterRouteConflict(t *testing.T) {
	stub := &managerNodesStub{promoteControllerVoterErr: fmt.Errorf("%w: target_health_stale", managementusecase.ErrControllerVoterPromotionBlocked)}
	srv := New(Options{Management: stub})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/4/controller-voter/promote", strings.NewReader(`{}`))
	srv.Engine().ServeHTTP(rec, req)
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Contains(t, rec.Body.String(), "target_health_stale")
}
```

- [ ] **Step 2: Run tests and confirm red**

Run:

```bash
GOWORK=off /usr/local/go/bin/go test ./internalv2/access/manager -run 'TestPromoteControllerVoterRoute' -count=1
```

Expected: fail with missing route and interface method.

- [ ] **Step 3: Add handler**

Create `internalv2/access/manager/controller_voter_promotion.go`:

```go
package manager

import (
	"errors"
	"net/http"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	"github.com/gin-gonic/gin"
)

type promoteControllerVoterRequestDTO struct {
	ExpectedRevision uint64 `json:"expected_revision,omitempty"`
}

type promoteControllerVoterResponseDTO struct {
	Changed bool `json:"changed"`
	NodeID uint64 `json:"node_id"`
	StateRevision uint64 `json:"state_revision"`
	PreviousVoters []uint64 `json:"previous_voters"`
	NextVoters []uint64 `json:"next_voters"`
	Warnings []string `json:"warnings,omitempty"`
}

func (s *Server) handlePromoteControllerVoter(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	nodeID, ok := parseUint64Path(c, "node_id")
	if !ok {
		return
	}
	var body promoteControllerVoterRequestDTO
	if c.Request.Body != nil {
		if err := c.ShouldBindJSON(&body); err != nil {
			jsonError(c, http.StatusBadRequest, "invalid_request", "invalid request")
			return
		}
	}
	resp, err := s.management.PromoteControllerVoter(c.Request.Context(), managementusecase.PromoteControllerVoterRequest{NodeID: nodeID, ExpectedRevision: body.ExpectedRevision})
	if err != nil {
		switch {
		case errors.Is(err, managementusecase.ErrNodeLifecycleNotFound):
			jsonError(c, http.StatusNotFound, "not_found", err.Error())
		case errors.Is(err, managementusecase.ErrControllerVoterPromotionBlocked):
			jsonError(c, http.StatusConflict, "conflict", err.Error())
		case errors.Is(err, managementusecase.ErrControllerVoterPromotionUnavailable):
			jsonError(c, http.StatusServiceUnavailable, "service_unavailable", err.Error())
		default:
			jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
		}
		return
	}
	status := http.StatusOK
	if resp.Changed {
		status = http.StatusAccepted
	}
	c.JSON(status, promoteControllerVoterResponseDTO{
		Changed: resp.Changed, NodeID: resp.NodeID, StateRevision: resp.StateRevision,
		PreviousVoters: resp.PreviousVoters, NextVoters: resp.NextVoters, Warnings: resp.Warnings,
	})
}
```

- [ ] **Step 4: Register route and DTO action**

In `internalv2/access/manager/server.go`, add to `Management`:

```go
// PromoteControllerVoter promotes one active node into Controller Raft voting membership.
PromoteControllerVoter(ctx context.Context, req managementusecase.PromoteControllerVoterRequest) (managementusecase.PromoteControllerVoterResponse, error)
```

Register under controller writes:

```go
controllerRaftWrites.POST("/nodes/:node_id/controller-voter/promote", s.handlePromoteControllerVoter)
```

In `internalv2/access/manager/nodes.go`, add:

```go
// CanPromoteControllerVoter reports whether Controller voter promotion can be considered.
CanPromoteControllerVoter bool `json:"can_promote_controller_voter"`
```

Map it:

```go
CanPromoteControllerVoter: item.Actions.CanPromoteControllerVoter,
```

- [ ] **Step 5: Run manager HTTP tests**

Run:

```bash
GOWORK=off /usr/local/go/bin/go test ./internalv2/access/manager -run 'TestPromoteControllerVoterRoute|TestHandleNodes' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit Task 7**

Run:

```bash
git add internalv2/access/manager
git commit -m "feat: add manager controller voter promotion route"
```

Expected: commit succeeds and stages only manager HTTP files.

### Task 8: Web Node List Action

**Files:**
- Modify: `web/src/lib/manager-api.types.ts`
- Modify: `web/src/lib/manager-api.ts`
- Modify: `web/src/lib/manager-api.test.ts`
- Modify: `web/src/pages/nodes/page.tsx`
- Modify: `web/src/pages/nodes/page.test.tsx`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`

- [ ] **Step 1: Inspect existing dirty web changes**

Run:

```bash
git status --short web/src/lib/manager-api.types.ts web/src/lib/manager-api.ts web/src/lib/manager-api.test.ts web/src/pages/nodes/page.tsx web/src/pages/nodes/page.test.tsx web/src/i18n/messages/en.ts web/src/i18n/messages/zh-CN.ts
git diff -- web/src/pages/nodes/page.tsx web/src/pages/nodes/page.test.tsx web/src/i18n/messages/en.ts web/src/i18n/messages/zh-CN.ts
```

Expected: if these files are already modified, keep the existing changes and layer this task on top.

- [ ] **Step 2: Write failing API client test**

In `web/src/lib/manager-api.test.ts`, add:

```ts
test("promoteControllerVoter posts expected revision", async () => {
  fetchMock.mockResolvedValueOnce(jsonResponse({
    changed: true,
    node_id: 4,
    state_revision: 10,
    previous_voters: [1, 2, 3],
    next_voters: [1, 2, 3, 4],
    warnings: ["controller_voter_count_even"],
  }))

  await expect(promoteControllerVoter(4, 9)).resolves.toEqual({
    changed: true,
    node_id: 4,
    state_revision: 10,
    previous_voters: [1, 2, 3],
    next_voters: [1, 2, 3, 4],
    warnings: ["controller_voter_count_even"],
  })
  expect(fetchMock).toHaveBeenCalledWith("/manager/nodes/4/controller-voter/promote", expect.objectContaining({
    method: "POST",
    body: JSON.stringify({ expected_revision: 9 }),
  }))
})
```

- [ ] **Step 3: Write failing page tests**

In `web/src/pages/nodes/page.test.tsx`, add:

```tsx
test("shows controller voter promotion action for eligible node", async () => {
  listNodesMock.mockResolvedValue(nodesResponse({
    items: [{ ...nodeRow, node_id: 4, controller: { ...nodeRow.controller, voter: false }, actions: { ...nodeRow.actions, can_promote_controller_voter: true } }],
  }))
  renderNodesPage()

  expect(await screen.findByRole("button", { name: /set as controller voter/i })).toBeEnabled()
})

test("confirms controller voter promotion and refreshes nodes", async () => {
  listNodesMock
    .mockResolvedValueOnce(nodesResponse({
      items: [{ ...nodeRow, node_id: 4, controller: { ...nodeRow.controller, voter: false }, actions: { ...nodeRow.actions, can_promote_controller_voter: true } }],
    }))
    .mockResolvedValueOnce(nodesResponse({
      items: [{ ...nodeRow, node_id: 4, controller: { ...nodeRow.controller, voter: true }, actions: { ...nodeRow.actions, can_promote_controller_voter: false } }],
    }))
  promoteControllerVoterMock.mockResolvedValue({
    changed: true,
    node_id: 4,
    state_revision: 10,
    previous_voters: [1, 2, 3],
    next_voters: [1, 2, 3, 4],
    warnings: ["controller_voter_count_even"],
  })
  const user = userEvent.setup()
  renderNodesPage()

  await user.click(await screen.findByRole("button", { name: /set as controller voter/i }))
  await user.click(await screen.findByRole("button", { name: /confirm/i }))

  expect(promoteControllerVoterMock).toHaveBeenCalledWith(4, expect.any(Number))
  expect(listNodesMock).toHaveBeenCalledTimes(2)
})

test("renders controller voter promotion conflict", async () => {
  listNodesMock.mockResolvedValue(nodesResponse({
    items: [{ ...nodeRow, node_id: 4, controller: { ...nodeRow.controller, voter: false }, actions: { ...nodeRow.actions, can_promote_controller_voter: true } }],
  }))
  promoteControllerVoterMock.mockRejectedValue(new Error("controller voter promotion blocked: target_health_stale"))
  const user = userEvent.setup()
  renderNodesPage()

  await user.click(await screen.findByRole("button", { name: /set as controller voter/i }))
  await user.click(await screen.findByRole("button", { name: /confirm/i }))

  expect(await screen.findByText(/target_health_stale/i)).toBeInTheDocument()
})
```

- [ ] **Step 4: Run tests and confirm red**

Run:

```bash
yarn --cwd web vitest run src/lib/manager-api.test.ts src/pages/nodes/page.test.tsx --runInBand
```

Expected: fail with undefined client method, type field, or button text.

- [ ] **Step 5: Add API types and client**

In `web/src/lib/manager-api.types.ts`, add:

```ts
can_promote_controller_voter: boolean
```

to `ManagerNodeActions`.

Add:

```ts
export type PromoteControllerVoterResponse = {
  changed: boolean
  node_id: number
  state_revision: number
  previous_voters: number[]
  next_voters: number[]
  warnings?: string[]
}
```

In `web/src/lib/manager-api.ts`, add:

```ts
export function promoteControllerVoter(nodeId: number, expectedRevision?: number) {
  const body = expectedRevision === undefined ? {} : { expected_revision: expectedRevision }
  return jsonManagerFetch<PromoteControllerVoterResponse>(`/manager/nodes/${nodeId}/controller-voter/promote`, {
    method: "POST",
    body: JSON.stringify(body),
  })
}
```

- [ ] **Step 6: Add page action and confirmation**

In `web/src/pages/nodes/page.tsx`, add an eligibility helper:

```ts
function canPromoteControllerVoter(node: ManagerNode) {
  return node.actions?.can_promote_controller_voter === true && node.controller?.voter !== true
}
```

Add a row action using the existing action/menu/confirm-dialog pattern. The confirm body must include these three facts through localized strings:

- changes Controller Raft quorum membership;
- even voter counts may not improve fault tolerance;
- `1 -> 2` can make either node failure block Controller writes, so `2 -> 3` should follow.

Call:

```ts
await promoteControllerVoter(target.node_id, nodes?.state_revision ?? undefined)
await refreshNodes()
```

Use the existing toast/error state path used by onboarding and scale-in actions.

- [ ] **Step 7: Add i18n strings**

In `web/src/i18n/messages/en.ts`, add concise English strings for:

```ts
"nodes.action.promoteControllerVoter": "Set as Controller voter",
"nodes.promoteControllerVoter.confirmTitle": "Set node as Controller voter?",
"nodes.promoteControllerVoter.confirmBody": "This changes Controller Raft quorum membership. Even voter counts may not improve fault tolerance. A 1 to 2 voter change means either node failure can block Controller writes, so plan a 2 to 3 promotion next.",
"nodes.promoteControllerVoter.success": "Controller voter promotion submitted.",
```

In `web/src/i18n/messages/zh-CN.ts`, add:

```ts
"nodes.action.promoteControllerVoter": "设为 Controller voter",
"nodes.promoteControllerVoter.confirmTitle": "将该节点设为 Controller voter？",
"nodes.promoteControllerVoter.confirmBody": "该操作会变更 Controller Raft 仲裁成员。偶数 voter 不一定提升容灾能力。1 到 2 个 voter 后，任一节点故障都可能阻塞 Controller 写入，建议继续规划 2 到 3 个 voter。",
"nodes.promoteControllerVoter.success": "Controller voter 提升已提交。",
```

- [ ] **Step 8: Run web tests**

Run:

```bash
yarn --cwd web vitest run src/lib/manager-api.test.ts src/pages/nodes/page.test.tsx --runInBand
```

Expected: PASS.

- [ ] **Step 9: Commit Task 8**

Run:

```bash
git add web/src/lib/manager-api.types.ts web/src/lib/manager-api.ts web/src/lib/manager-api.test.ts web/src/pages/nodes/page.tsx web/src/pages/nodes/page.test.tsx web/src/i18n/messages/en.ts web/src/i18n/messages/zh-CN.ts
git commit -m "feat: add controller voter promotion action"
```

Expected: commit succeeds and includes existing user edits in those files only if they are required for the final file content.

### Task 9: Metrics, Status Surfaces, And FLOW Docs

**Files:**
- Modify: `pkg/metrics/controller.go` or the current Controller metrics registry file
- Modify: `internalv2/app/observability.go` or the current manager metrics observer wiring file
- Modify: `pkg/metrics/registry_test.go`
- Modify: `pkg/controllerv2/FLOW.md`
- Modify: `pkg/clusterv2/FLOW.md`
- Modify: `internalv2/usecase/management/FLOW.md`
- Modify: `internalv2/access/manager/FLOW.md`

- [ ] **Step 1: Locate exact metrics files**

Run:

```bash
rg -n "Controller.*metrics|controller_raft|ScaleInStatusObserver|ObserveNodeLifecycleAttempt|prometheus" pkg/metrics internalv2/app internalv2/usecase/management
```

Expected: identifies the registry file and the current manager observer wiring file.

- [ ] **Step 2: Write failing metrics tests**

Add registry assertions for these metric names:

```text
wukongimv2_controller_voter_promotion_attempts_total
wukongimv2_controller_voter_promotion_blockers_total
wukongimv2_controller_raft_voters
wukongimv2_controller_raft_learners
wukongimv2_controller_voter_promotion_phase_seconds
```

Expected labels:

```text
result
reason
phase
```

Do not add labels for node ID, address, task ID, or cluster ID.

- [ ] **Step 3: Run metrics tests and confirm red**

Run:

```bash
GOWORK=off /usr/local/go/bin/go test ./pkg/metrics ./internalv2/app -run 'ControllerVoterPromotion|Metrics|Observer' -count=1
```

Expected: fail with missing metric definitions or observer methods.

- [ ] **Step 4: Implement metrics**

Add counters, gauges, and histogram with bounded labels. Use the existing metrics helper style in the file located by Step 1. Add usecase observer calls for:

```text
result=changed
result=noop
result=blocked
result=unavailable
reason=target_health_stale
reason=target_revision_stale
reason=expected_revision_mismatch
```

Set voter/learner gauges from Controller Raft status collection. Record phase histogram for:

```text
phase=readiness
phase=prepare
phase=add_learner
phase=catch_up
phase=promote_voter
phase=commit_state
```

- [ ] **Step 5: Update FLOW docs**

Update these documents:

- `pkg/controllerv2/FLOW.md`: document learner-first membership APIs, mirror-state move-aside preparation, and atomic `KindPromoteControllerVoter`.
- `pkg/clusterv2/FLOW.md`: document `Node.PromoteControllerVoter`, generic control-write forwarding, and target prepare RPC.
- `internalv2/usecase/management/FLOW.md`: document promotion eligibility, readiness, bounded blocker codes, and node-list hint.
- `internalv2/access/manager/FLOW.md`: document the new `POST /manager/nodes/:node_id/controller-voter/promote` route and `cluster.controller:w` permission.

- [ ] **Step 6: Run docs and metrics verification**

Run:

```bash
GOWORK=off /usr/local/go/bin/go test ./pkg/metrics ./internalv2/app -run 'ControllerVoterPromotion|Metrics|Observer' -count=1
git diff --check -- pkg/controllerv2/FLOW.md pkg/clusterv2/FLOW.md internalv2/usecase/management/FLOW.md internalv2/access/manager/FLOW.md
```

Expected: tests PASS and diff check reports no output.

- [ ] **Step 7: Commit Task 9**

Run:

```bash
git add pkg/metrics internalv2/app pkg/controllerv2/FLOW.md pkg/clusterv2/FLOW.md internalv2/usecase/management/FLOW.md internalv2/access/manager/FLOW.md
git commit -m "feat: observe controller voter promotion"
```

Expected: commit succeeds.

### Task 10: End-to-End Verification And Final Sweep

**Files:**
- Modify tests only if narrow verification exposes missing test helpers.
- Read all changed files from Tasks 1 through 9.

- [ ] **Step 1: Run focused Go verification**

Run:

```bash
GOWORK=off /usr/local/go/bin/go test ./pkg/controllerv2/... ./pkg/clusterv2/control ./pkg/clusterv2 ./internalv2/usecase/management ./internalv2/access/node ./internalv2/access/manager ./internalv2/infra/cluster -count=1
```

Expected: PASS.

- [ ] **Step 2: Run focused web verification**

Run:

```bash
yarn --cwd web vitest run src/lib/manager-api.test.ts src/pages/nodes/page.test.tsx --runInBand
```

Expected: PASS.

- [ ] **Step 3: Run formatting and diff checks**

Run:

```bash
gofmt -w pkg/controllerv2 pkg/clusterv2/control pkg/clusterv2 internalv2/usecase/management internalv2/access/node internalv2/access/manager internalv2/infra/cluster internalv2/app
git diff --check
rg -n "T[O]DO|T[B]D|F[I]XME|implement l[a]ter|fill in deta[i]ls|a[p]propriate|s[i]milar to|write tests for the a[b]ove" pkg/controllerv2 pkg/clusterv2 internalv2 web/src docs/superpowers/plans/2026-07-01-controller-voter-promotion.md
```

Expected: `gofmt` changes only Go formatting, `git diff --check` reports no output, and the placeholder scan reports no matches.

- [ ] **Step 4: Inspect final diff**

Run:

```bash
git diff --stat
git diff --name-only
```

Expected: changed files match the File Map. Unrelated pre-existing dirty files are either untouched or intentionally incorporated into Task 8 only.

- [ ] **Step 5: Final commit if formatting changed**

Run:

```bash
git add pkg/controllerv2 pkg/clusterv2 internalv2 web/src docs/superpowers/plans/2026-07-01-controller-voter-promotion.md
git commit -m "test: verify controller voter promotion flow"
```

Expected: commit is created only if Step 3 produced formatting or test-support changes after Task 9.

- [ ] **Step 6: Final status**

Run:

```bash
git status --short
```

Expected: only unrelated user work remains dirty. If files from this plan remain dirty, inspect and either commit them or explain the remaining risk before handing back.
