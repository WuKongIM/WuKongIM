# internalv2 Dynamic Node Lifecycle Stage 5E Remove Smoke And FLOW Docs Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prove the complete drained-node removal path with one black-box e2ev2 smoke and update FLOW documentation for the packages touched by Stage 5.

**Architecture:** Stage 5E is validation and documentation only. It uses the manager routes added in Stage 5D to mark a non-onboarded fourth data node leaving, enable gateway drain mode, wait for `safe_to_remove=true`, mark the node `removed`, and observe the tombstone through manager node inventory.

**Tech Stack:** Go e2ev2 harness, manager HTTP client helpers, internalv2 FLOW docs, dynamic-node lifecycle plan index.

---

## Scope

Implements only Stage 5E from:

- Stage 5 index: `docs/superpowers/plans/2026-06-24-internalv2-dynamic-node-stage5.md`
- Previous sub-stage: `docs/superpowers/plans/2026-06-24-internalv2-dynamic-node-stage5d-safe-remove-route.md`

This sub-stage must not add new production routes, change safety gating, or relax the Stage 5D remove preconditions. If the smoke exposes a production bug, stop and fix the bug in the package that owns it before continuing the test.

## Entry Gate

- [ ] Stage 5D is merged into local `main`.
- [ ] Stage 5D focused verification passes:

```bash
GOWORK=off go test ./internalv2/usecase/management ./internalv2/access/manager ./internalv2/infra/cluster ./internalv2/app -run 'Test.*ScaleIn|Test.*Drain|Test.*Remove|TestMarkNodeRemoved' -count=1
```

Expected: PASS.

## File Structure

- Modify `test/e2ev2/suite/manager_client.go`
  - Adds scale-in status, drain, and remove helper methods.
- Modify or create `test/e2ev2/cluster/dynamic_node_join/node_remove_drain_test.go`
  - Adds the black-box drained-node removal smoke.
- Modify package `FLOW.md` files touched by Stage 5:
  - `internalv2/usecase/management/FLOW.md`
  - `internalv2/access/manager/FLOW.md`
  - `internalv2/access/node/FLOW.md`
  - `internalv2/infra/cluster/FLOW.md`
  - `pkg/clusterv2/FLOW.md`

## Task 1: Add e2ev2 Manager Scale-In Helpers

**File:** `test/e2ev2/suite/manager_client.go`

- [ ] **Step 1: Write helper types near the existing onboarding DTOs**

Add:

```go
// NodeScaleInStatusDTO is the manager scale-in status subset used by e2ev2.
type NodeScaleInStatusDTO struct {
	NodeID                  uint64 `json:"node_id"`
	JoinState               string `json:"join_state"`
	StateRevision           uint64 `json:"state_revision"`
	SafeToProceed           bool   `json:"safe_to_proceed"`
	SafeToRemove            bool   `json:"safe_to_remove"`
	BlockedBySlots          bool   `json:"blocked_by_slots"`
	BlockedByTasks          bool   `json:"blocked_by_tasks"`
	BlockedByChannels       bool   `json:"blocked_by_channels"`
	BlockedByRuntimeDrain   bool   `json:"blocked_by_runtime_drain"`
	RuntimeUnknown          bool   `json:"runtime_unknown"`
	UnknownChannelInventory bool   `json:"unknown_channel_inventory"`
	GatewayDraining         bool   `json:"gateway_draining"`
	AcceptingNewSessions    bool   `json:"accepting_new_sessions"`
	SlotReplicaCount        int    `json:"slot_replica_count"`
	ChannelLeaderCount      int    `json:"channel_leader_count"`
	ChannelReplicaCount     int    `json:"channel_replica_count"`
	ChannelISRCount         int    `json:"channel_isr_count"`
	GatewaySessions          int    `json:"gateway_sessions"`
	ActiveOnline             int    `json:"active_online"`
	ClosingOnline            int    `json:"closing_online"`
	TotalOnline              int    `json:"total_online"`
	PendingActivations       int    `json:"pending_activations"`
}

// NodeScaleInStartDTO is the manager leaving transition subset used by e2ev2.
type NodeScaleInStartDTO struct {
	Changed       bool   `json:"changed"`
	NodeID        uint64 `json:"node_id"`
	JoinState     string `json:"join_state"`
	StateRevision uint64 `json:"state_revision"`
}

// NodeScaleInDrainDTO is the manager gateway drain response subset used by e2ev2.
type NodeScaleInDrainDTO struct {
	NodeID               uint64 `json:"node_id"`
	Draining             bool   `json:"draining"`
	AcceptingNewSessions bool   `json:"accepting_new_sessions"`
	GatewaySessions      int    `json:"gateway_sessions"`
	ActiveOnline         int    `json:"active_online"`
	ClosingOnline        int    `json:"closing_online"`
	TotalOnline          int    `json:"total_online"`
	PendingActivations   int    `json:"pending_activations"`
	Unknown              bool   `json:"unknown"`
}

// NodeScaleInRemoveDTO is the manager removed transition subset used by e2ev2.
type NodeScaleInRemoveDTO struct {
	Changed   bool   `json:"changed"`
	NodeID    uint64 `json:"node_id"`
	JoinState string `json:"join_state"`
	Revision  uint64 `json:"revision"`
}
```

Use the exact JSON keys returned by Stage 5D manager handlers. If a field name in the Stage 5D implementation differs, align the e2ev2 DTO to the production JSON key and keep the DTO comment accurate.

- [ ] **Step 2: Add helper methods**

Add methods after `EventuallyOnboardingSafe`:

```go
// MustStartScaleIn marks a data node leaving through manager HTTP.
func (m *ManagerClient) MustStartScaleIn(t testing.TB, nodeID uint64) NodeScaleInStartDTO {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var out NodeScaleInStartDTO
	status, body, err := postJSONStatus(ctx, fmt.Sprintf("%s/manager/nodes/%d/scale-in/start", m.baseURL, nodeID), nil, &out)
	if err != nil || status/100 != 2 {
		if err == nil {
			err = fmt.Errorf("start scale-in node %d returned %d: %s", nodeID, status, strings.TrimSpace(string(body)))
		}
		t.Fatalf("%v\n%s", err, m.cluster.DumpDiagnostics())
	}
	return out
}

// MustSetScaleInDrain sets gateway drain mode through manager HTTP.
func (m *ManagerClient) MustSetScaleInDrain(t testing.TB, nodeID uint64, drain bool) NodeScaleInDrainDTO {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var out NodeScaleInDrainDTO
	status, body, err := postJSONStatus(ctx, fmt.Sprintf("%s/manager/nodes/%d/scale-in/drain", m.baseURL, nodeID), map[string]any{
		"draining": drain,
	}, &out)
	if err != nil || status/100 != 2 {
		if err == nil {
			err = fmt.Errorf("set scale-in drain node %d returned %d: %s", nodeID, status, strings.TrimSpace(string(body)))
		}
		t.Fatalf("%v\n%s", err, m.cluster.DumpDiagnostics())
	}
	return out
}

// NodeScaleInStatus returns the target node scale-in status.
func (m *ManagerClient) NodeScaleInStatus(ctx context.Context, nodeID uint64) (NodeScaleInStatusDTO, error) {
	var out NodeScaleInStatusDTO
	_, err := GetJSON(ctx, fmt.Sprintf("%s/manager/nodes/%d/scale-in/status", m.baseURL, nodeID), &out)
	if err != nil {
		return NodeScaleInStatusDTO{}, err
	}
	return out, nil
}

// EventuallyScaleInSafeToRemove waits until scale-in status reports final removal safety.
func (m *ManagerClient) EventuallyScaleInSafeToRemove(t testing.TB, nodeID uint64, timeout time.Duration) NodeScaleInStatusDTO {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ticker := time.NewTicker(managerPollInterval)
	defer ticker.Stop()

	var (
		lastResp NodeScaleInStatusDTO
		lastErr  error
	)
	for {
		resp, err := m.NodeScaleInStatus(ctx, nodeID)
		if err == nil {
			lastResp = resp
			if resp.SafeToRemove {
				return resp
			}
			lastErr = fmt.Errorf("safe_to_remove=false status=%#v", resp)
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			t.Fatalf("node %d scale-in did not become safe to remove: last=%#v lastErr=%v\n%s", nodeID, lastResp, lastErr, m.cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

// MustRemoveScaleInNode marks a fully drained node removed through manager HTTP.
func (m *ManagerClient) MustRemoveScaleInNode(t testing.TB, nodeID uint64) NodeScaleInRemoveDTO {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var out NodeScaleInRemoveDTO
	status, body, err := postJSONStatus(ctx, fmt.Sprintf("%s/manager/nodes/%d/scale-in/remove", m.baseURL, nodeID), nil, &out)
	if err != nil || status/100 != 2 {
		if err == nil {
			err = fmt.Errorf("remove scale-in node %d returned %d: %s", nodeID, status, strings.TrimSpace(string(body)))
		}
		t.Fatalf("%v\n%s", err, m.cluster.DumpDiagnostics())
	}
	return out
}
```

- [ ] **Step 3: Verify helper package compiles**

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/suite -run TestManagerClientScaleInHelpersCompile -count=1
```

Expected: package compilation succeeds. The named test does not need to exist; JSON field alignment is verified by the Stage 5E black-box smoke assertions below, not by this compile-only check.

## Task 2: Add Black-Box Drained Node Remove Smoke

**File:** `test/e2ev2/cluster/dynamic_node_join/node_remove_drain_test.go`

- [ ] **Step 1: Create the e2ev2 test**

Add:

```go
//go:build e2e

package dynamic_node_join

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2ev2/suite"
	"github.com/stretchr/testify/require"
)

func TestLeavingNodeCanBeRemovedAfterDrain(t *testing.T) {
	s := suite.New(t)
	const joinToken = "stage5-remove-token"
	cluster := s.StartThreeNodeCluster(
		suite.WithManagerHTTP(),
		suite.WithDynamicJoinToken(joinToken),
	)

	readyCtx, cancelReady := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelReady()
	require.NoError(t, cluster.WaitClusterReady(readyCtx), cluster.DumpDiagnostics())

	manager := cluster.ManagerClient(t, 1)
	before := manager.MustSlots(t)

	node4 := cluster.StartSeedJoinNode(t, suite.SeedJoinNodeConfig{
		NodeID:    4,
		Seeds:     cluster.SeedAddrs(),
		JoinToken: joinToken,
	})
	require.NotNil(t, node4)

	manager.EventuallyNodeJoinState(t, 4, "joining", 20*time.Second)
	manager.EventuallyNodeReadiness(t, 4, true, 20*time.Second)
	manager.MustActivateNode(t, 4)
	manager.EventuallyNodeJoinState(t, 4, "active", 20*time.Second)

	afterActivate := manager.MustSlots(t)
	require.True(t, suite.SameSlotAssignments(before, afterActivate), "activation without onboarding must not move Slots")

	start := manager.MustStartScaleIn(t, 4)
	require.Equal(t, uint64(4), start.NodeID)
	require.Equal(t, "leaving", start.JoinState)
	manager.EventuallyNodeJoinState(t, 4, "leaving", 20*time.Second)

	drain := manager.MustSetScaleInDrain(t, 4, true)
	require.True(t, drain.Draining)
	require.False(t, drain.AcceptingNewSessions)

	status := manager.EventuallyScaleInSafeToRemove(t, 4, 30*time.Second)
	require.True(t, status.SafeToRemove)
	require.False(t, status.BlockedBySlots)
	require.False(t, status.BlockedByChannels)
	require.False(t, status.BlockedByRuntimeDrain)
	require.True(t, status.GatewayDraining)
	require.False(t, status.AcceptingNewSessions)
	require.Zero(t, status.GatewaySessions)
	require.Zero(t, status.ActiveOnline)
	require.Zero(t, status.ClosingOnline)
	require.Zero(t, status.PendingActivations)

	removed := manager.MustRemoveScaleInNode(t, 4)
	require.Equal(t, uint64(4), removed.NodeID)
	require.Equal(t, "removed", removed.JoinState)
	manager.EventuallyNodeJoinState(t, 4, "removed", 20*time.Second)
}
```

This smoke intentionally does not onboard node 4. It proves the final remove path for a node that is active, has no Slot ownership, has no Channel ownership, has no gateway sessions, and is explicitly in drain mode.

- [ ] **Step 2: Verify RED or GREEN depending on Stage 5D state**

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_join -run TestLeavingNodeCanBeRemovedAfterDrain -count=1 -p=1
```

Expected before Stage 5D is complete: FAIL because `/scale-in/drain`, `/scale-in/remove`, or `safe_to_remove` is missing. Expected after Stage 5D is complete: PASS.

## Task 3: Update FLOW Documentation

**Files:**
- `internalv2/usecase/management/FLOW.md`
- `internalv2/access/manager/FLOW.md`
- `internalv2/access/node/FLOW.md`
- `internalv2/infra/cluster/FLOW.md`
- `pkg/clusterv2/FLOW.md`

- [ ] **Step 1: Read each FLOW file before editing**

```bash
for f in \
  internalv2/usecase/management/FLOW.md \
  internalv2/access/manager/FLOW.md \
  internalv2/access/node/FLOW.md \
  internalv2/infra/cluster/FLOW.md \
  pkg/clusterv2/FLOW.md; do
  test -f "$f" && sed -n '1,220p' "$f"
done
```

- [ ] **Step 2: Add these ownership facts under each file's routing or responsibility section**

Add the same facts, using each FLOW file's existing tone and headings. When a FLOW file has no routing or responsibility section, create a `## Dynamic Node Scale-In Removal` section:

- Manager HTTP owns operator intent routes only: start scale-in, set drain mode, read status, and remove when safe.
- Management usecase computes fail-closed safety from Controller snapshot, Slot status, Channel runtime meta inventory, task state, and runtime drain counters.
- Node RPC forwards remote gateway drain writes; it does not decide scale-in safety.
- Infra cluster adapter translates management lifecycle writes into clusterv2 node management calls.
- clusterv2 forwards `MarkNodeRemoved` through ControllerV2 control writes and keeps `removed` as a tombstone state.
- ControllerV2 owns authoritative lifecycle transitions and rejects removal of active data nodes and controller voters.

- [ ] **Step 3: Update plan completion evidence**

Gate 5 in `docs/superpowers/plans/2026-06-24-internalv2-dynamic-node-lifecycle.md` is the Stage 5 entry gate, not the Stage 5 completion gate. After the Stage 5E smoke passes, add the final passing command output to the branch handoff or commit message; do not add a new completion checkbox unless the lifecycle index has an explicit Stage 5 completion section.

## Task 4: Run Stage 5E Verification And Commit

- [ ] **Step 1: Run focused e2ev2 and package checks**

```bash
GOWORK=off go test ./pkg/controllerv2 ./pkg/clusterv2/control ./pkg/clusterv2 ./internalv2/usecase/management ./internalv2/access/manager ./internalv2/access/node ./internalv2/infra/cluster ./internalv2/app -count=1
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_join -run 'TestDynamicJoinFourthDataNode|TestSlotReplicaMoveKeepsSendAvailable|TestLeavingNodeCanBeRemovedAfterDrain' -count=1 -p=1
git diff --check
```

Expected: PASS.

- [ ] **Step 2: Commit Stage 5E**

```bash
git add test/e2ev2/suite/manager_client.go test/e2ev2/cluster/dynamic_node_join/node_remove_drain_test.go internalv2/usecase/management/FLOW.md internalv2/access/manager/FLOW.md internalv2/access/node/FLOW.md internalv2/infra/cluster/FLOW.md pkg/clusterv2/FLOW.md docs/superpowers/plans/2026-06-24-internalv2-dynamic-node-lifecycle.md
git commit -m "test: cover drained node removal"
```

## Exit Gate

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_join -run TestLeavingNodeCanBeRemovedAfterDrain -count=1 -p=1
git diff --check
rg -n "scale-in/(start|drain|status|remove)|MarkNodeRemoved|safe_to_remove|removed tombstone|ChannelRuntimeMeta" internalv2 pkg test/e2ev2 docs/superpowers/plans
```

Expected: the black-box smoke passes, FLOW files describe the final path, and `removed` remains reachable only through the manager safe-to-remove route.
