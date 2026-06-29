# InternalV2 Dynamic Node Stage 8 Scale-In Fault Injection Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add opt-in gofail-backed e2ev2 coverage for dynamic node scale-in and removal fault recovery.

**Architecture:** Keep production behavior unchanged unless tests prove an unreachable fault window. Reuse existing clusterv2 network and Slot replica-move gofail markers, add black-box e2ev2 scenarios under `test/e2ev2/cluster/dynamic_node_faults`, and require every injected failure to expose root-cause evidence through gofail counts plus manager scale-in/task status.

**Tech Stack:** Go 1.23, `go.etcd.io/gofail`, `cmd/wukongimv2`, `test/e2ev2/suite`, manager HTTP APIs, clusterv2 `slot_replica_move` tasks.

---

## Source Links

- Spec: `docs/superpowers/specs/2026-06-29-internalv2-dynamic-node-stage8-scale-in-fault-injection-design.md`
- Dynamic node lifecycle spec: `docs/superpowers/specs/2026-06-24-internalv2-dynamic-node-lifecycle-design.md`
- Master plan index: `docs/superpowers/plans/2026-06-24-internalv2-dynamic-node-lifecycle.md`
- Stage 6 e2ev2 plan: `docs/superpowers/plans/2026-06-26-internalv2-dynamic-node-stage6-e2ev2.md`
- Stage 7 gofail plan: `docs/superpowers/plans/2026-06-26-internalv2-dynamic-node-stage7-gofail-fault-injection.md`

## File Map

- Create: `test/e2ev2/cluster/dynamic_node_faults/scale_in_fault_helpers_test.go`
  - Shared setup and assertions for Stage 8 scale-in fault tests.
- Modify: `internalv2/usecase/management/channel_drain.go`
  - Narrow Channel inventory gofail marker.
- Create: `internalv2/usecase/management/gofail_markers_test.go`
  - Marker preservation and disabled-marker behavior tests.
- Modify: `pkg/controllerv2/runtime_node_lifecycle.go`
  - Narrow post-commit `MarkNodeRemoved` response-loss marker.
- Create: `pkg/controllerv2/gofail_markers_test.go`
  - Marker preservation and disabled-marker behavior tests.
- Create: `test/e2ev2/cluster/dynamic_node_faults/scale_in_fail_closed_fault_test.go`
  - Channel inventory and manager runtime summary fail-closed scenarios.
- Create: `test/e2ev2/cluster/dynamic_node_faults/scale_in_remove_post_commit_fault_test.go`
  - Final remove post-commit response-loss idempotency scenario.
- Create: `test/e2ev2/cluster/dynamic_node_faults/scale_in_slot_drain_fault_test.go`
  - Delayed Slot drain and restart-during-scale-in scenarios.
- Modify: `test/e2ev2/cluster/dynamic_node_faults/AGENTS.md`
  - Catalog Stage 8 scenarios and update the runtime command timeout.
- Modify: `docs/superpowers/plans/2026-06-24-internalv2-dynamic-node-lifecycle.md`
  - Link Stage 6, Stage 7, and Stage 8 in the master stage chain.

Production changes are limited to narrow gofail markers and helper functions
that are inert in normal builds. Do not change scale-in semantics unless a
Stage 8 test exposes a real bug with evidence.

## Entry Gate

- [ ] Current branch is clean:

```bash
git status --short --branch
```

Expected: no unstaged or staged files except intentional Stage 8 changes after work begins.

- [ ] Existing focused suites pass before adding new scenarios:

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/suite ./test/e2ev2/cluster/dynamic_node_faults -count=1 -timeout 2m -p=1
```

Expected: PASS. Gofail scenario tests skip unless `WK_E2EV2_GOFAIL_DYNAMIC_NODE=1`.

---

### Task 1: Add Shared Scale-In Fault Helpers

**Files:**
- Create: `test/e2ev2/cluster/dynamic_node_faults/scale_in_fault_helpers_test.go`

- [ ] **Step 1: Create the helper file**

Create `scale_in_fault_helpers_test.go`:

```go
//go:build e2e

package dynamic_node_faults

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2ev2/suite"
	"github.com/stretchr/testify/require"
)

const (
	slotReplicaMoveTransferLeaderDelay = "wkSlotReplicaMoveTransferLeaderDelay"
	slotReplicaMoveRemoveVoterDelay    = "wkSlotReplicaMoveRemoveVoterDelay"
	scaleInRuntimeSummaryFaultMarker   = "temporary scale-in runtime summary fault"
	scaleInChannelInventoryFault       = "wkScaleInChannelDrainInventoryFault"
	markNodeRemovedPostCommitFault     = "wkMarkNodeRemovedPostCommitFault"
)

type faultableDynamicCluster struct {
	cluster   *suite.StartedCluster
	manager   *suite.ManagerClient
	nodeFails map[uint64]suite.GofailEndpoint
}

func startFaultableDynamicCluster(t testing.TB, joinToken string) faultableDynamicCluster {
	t.Helper()

	nodeFails := map[uint64]suite.GofailEndpoint{
		1: suite.ReserveGofailEndpoint(t),
		2: suite.ReserveGofailEndpoint(t),
		3: suite.ReserveGofailEndpoint(t),
		4: suite.ReserveGofailEndpoint(t),
	}
	s := suite.New(t)
	cluster := s.StartThreeNodeCluster(
		suite.WithManagerHTTP(),
		suite.WithDynamicJoinToken(joinToken),
		suite.WithNodeEnv(1, nodeFails[1].Env()),
		suite.WithNodeEnv(2, nodeFails[2].Env()),
		suite.WithNodeEnv(3, nodeFails[3].Env()),
		suite.WithNodeEnv(4, nodeFails[4].Env()),
	)
	readyCtx, readyCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer readyCancel()
	require.NoError(t, cluster.WaitClusterReady(readyCtx), cluster.DumpDiagnostics())
	return faultableDynamicCluster{
		cluster:   cluster,
		manager:   cluster.ManagerClient(t, 1),
		nodeFails: nodeFails,
	}
}

func (f faultableDynamicCluster) staticFailpoints() []suite.GofailEndpoint {
	return []suite.GofailEndpoint{f.nodeFails[1], f.nodeFails[2], f.nodeFails[3]}
}

func (f faultableDynamicCluster) allFailpoints() []suite.GofailEndpoint {
	return []suite.GofailEndpoint{f.nodeFails[1], f.nodeFails[2], f.nodeFails[3], f.nodeFails[4]}
}
```

- [ ] **Step 2: Add dynamic node setup helpers**

Append:

```go
func startActiveNode4(t testing.TB, f faultableDynamicCluster, joinToken string) *suite.StartedNode {
	t.Helper()

	node4 := f.cluster.StartSeedJoinNode(t, suite.SeedJoinNodeConfig{
		NodeID:    4,
		Seeds:     f.cluster.SeedAddrs(),
		JoinToken: joinToken,
	})
	f.manager.EventuallyNodeJoinState(t, 4, "joining", 20*time.Second)
	f.manager.EventuallyNodeReadiness(t, 4, true, 20*time.Second)
	f.manager.MustActivateNode(t, 4)
	f.manager.EventuallyNodeJoinState(t, 4, "active", 20*time.Second)

	readyCtx, readyCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer readyCancel()
	require.NoError(t, f.cluster.WaitClusterReady(readyCtx), f.cluster.DumpDiagnostics())
	return node4
}

func onboardOneSlotToNode4(t testing.TB, f faultableDynamicCluster) suite.NodeOnboardingPlanDTO {
	t.Helper()

	plan := f.manager.MustPlanOnboarding(t, 4, 1)
	require.Len(t, plan.Candidates, 1, f.cluster.DumpDiagnostics())
	candidate := plan.Candidates[0]
	ensureSlotLeaderForReplicaMoveSource(t, f.cluster, candidate.SlotID, candidate.SourceNodeID, 45*time.Second)

	start := f.manager.MustStartOnboarding(t, 4, 1)
	require.Equal(t, uint32(1), start.Created, f.cluster.DumpDiagnostics())
	f.manager.EventuallyOnboardingSafe(t, 4, 90*time.Second)
	return plan
}

func startScaleInDrainWithOneSlotMove(t testing.TB, f faultableDynamicCluster) suite.NodeScaleInPlanDTO {
	t.Helper()

	start := f.manager.MustStartScaleIn(t, 4)
	require.Equal(t, uint64(4), start.NodeID)
	require.Equal(t, "leaving", start.JoinState)
	f.manager.EventuallyNodeJoinState(t, 4, "leaving", 20*time.Second)

	drain := f.manager.MustSetScaleInDrain(t, 4, true)
	require.True(t, drain.Draining)
	require.False(t, drain.AcceptingNewSessions)

	plan := eventuallyFaultableScaleInPlan(t, f, 4, 1, 30*time.Second)
	require.Len(t, plan.Candidates, 1, f.cluster.DumpDiagnostics())
	candidate := plan.Candidates[0]
	require.Equal(t, uint64(4), candidate.SourceNodeID, "scale-in candidate must move away from node 4: %#v\n%s", candidate, f.cluster.DumpDiagnostics())
	ensureSlotLeaderForReplicaMoveSource(t, f.cluster, candidate.SlotID, candidate.SourceNodeID, 45*time.Second)
	return plan
}

func eventuallyFaultableScaleInPlan(t testing.TB, f faultableDynamicCluster, nodeID uint64, maxSlotMoves uint32, timeout time.Duration) suite.NodeScaleInPlanDTO {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var (
		lastStatus suite.NodeScaleInStatusDTO
		lastPlan   suite.NodeScaleInPlanDTO
		lastErr    error
	)
	for {
		status, err := f.manager.NodeScaleInStatus(ctx, nodeID)
		if err == nil {
			lastStatus = status
			if status.JoinState == "leaving" && status.GatewayDraining && status.BlockedBySlots {
				plan := f.manager.MustPlanScaleIn(t, nodeID, maxSlotMoves)
				lastPlan = plan
				if len(plan.Candidates) > 0 {
					return plan
				}
				lastErr = fmt.Errorf("plan has no candidates: %#v", plan)
			} else {
				lastErr = fmt.Errorf("status not ready for scale-in plan: %#v", status)
			}
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			t.Fatalf("node %d scale-in plan did not become ready: status=%#v plan=%#v lastErr=%v\n%s", nodeID, lastStatus, lastPlan, lastErr, f.cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}
```

- [ ] **Step 3: Add scale-in status and fault assertions**

Append:

```go
func waitScaleInTaskActive(t testing.TB, f faultableDynamicCluster, nodeID uint64, timeout time.Duration) suite.NodeScaleInStatusDTO {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var (
		lastStatus suite.NodeScaleInStatusDTO
		lastErr    error
	)
	for {
		status, err := f.manager.NodeScaleInStatus(ctx, nodeID)
		if err == nil {
			lastStatus = status
			if status.ActiveTaskCount > 0 || status.BlockedByTasks {
				return status
			}
			lastErr = fmt.Errorf("scale-in task inactive: active=%d blocked_by_tasks=%t failed=%d status=%#v",
				status.ActiveTaskCount,
				status.BlockedByTasks,
				status.FailedTaskCount,
				status,
			)
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			t.Fatalf("node %d scale-in task did not become active: last=%#v lastErr=%v\n%s", nodeID, lastStatus, lastErr, f.cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func requireScaleInStatus(t testing.TB, f faultableDynamicCluster, nodeID uint64, check func(suite.NodeScaleInStatusDTO) error) suite.NodeScaleInStatusDTO {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	status, err := f.manager.NodeScaleInStatus(ctx, nodeID)
	require.NoError(t, err, f.cluster.DumpDiagnostics())
	if err := check(status); err != nil {
		t.Fatalf("scale-in status assertion failed: %v status=%#v\n%s", err, status, f.cluster.DumpDiagnostics())
	}
	return status
}

func requireScaleInRemoveConflict(t testing.TB, status int, body []byte, err error, diagnostics string) {
	t.Helper()

	text := strings.TrimSpace(string(body))
	require.NoError(t, err, "status=%d body=%s\n%s", status, text, diagnostics)
	require.Equal(t, http.StatusConflict, status, "body=%s\n%s", text, diagnostics)
	require.True(t,
		strings.Contains(text, `"conflict"`) || strings.Contains(text, `"error"`),
		"expected bounded conflict body, status=%d body=%s\n%s",
		status,
		text,
		diagnostics,
	)
}
```

- [ ] **Step 4: Compile helper package**

Run:

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_faults -run TestBoundedFaultedOnboardingStartAcceptsHTTPFaultWithMarker -count=1 -timeout 2m -p=1
```

Expected: PASS. This command compiles the new helper file while running an existing fast helper test.

- [ ] **Step 5: Commit Task 1**

```bash
git add test/e2ev2/cluster/dynamic_node_faults/scale_in_fault_helpers_test.go
git commit -m "test: add scale-in fault helpers"
```

---

### Task 2: Add Narrow Channel Inventory Failpoint

**Files:**
- Modify: `internalv2/usecase/management/channel_drain.go`
- Create: `internalv2/usecase/management/gofail_markers_test.go`

- [ ] **Step 1: Add marker preservation and disabled behavior tests**

Create `internalv2/usecase/management/gofail_markers_test.go`:

```go
package management

import (
	"os"
	"strings"
	"testing"
)

func TestScaleInChannelDrainInventoryGofailMarkerIsPreserved(t *testing.T) {
	data, err := os.ReadFile("channel_drain.go")
	if err != nil {
		t.Fatalf("read channel_drain.go: %v", err)
	}
	text := string(data)
	for _, want := range []string{
		"// gofail: var wkScaleInChannelDrainInventoryFault string",
		"// if err := gofailScaleInChannelDrainInventoryFault(wkScaleInChannelDrainInventoryFault); err != nil {",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing gofail marker %q", want)
		}
	}
}

func TestScaleInChannelDrainInventoryGofailDisabled(t *testing.T) {
	if err := gofailScaleInChannelDrainInventoryFault(""); err != nil {
		t.Fatalf("disabled failpoint returned error: %v", err)
	}
}
```

- [ ] **Step 2: Run the marker test and verify it fails**

Run:

```bash
GOWORK=off go test ./internalv2/usecase/management -run 'TestScaleInChannelDrainInventoryGofail' -count=1
```

Expected: FAIL because `gofailScaleInChannelDrainInventoryFault` and the marker do not exist.

- [ ] **Step 3: Add the inert helper and marker**

In `internalv2/usecase/management/channel_drain.go`, add:

```go
func gofailScaleInChannelDrainInventoryFault(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	return errors.New(raw)
}
```

Update imports to include `strings`.

Inside `nodeChannelDrainInventoryFromSnapshot`, immediately before
`ScanChannelRuntimeMetaSlotPage`:

```go
			// gofail: var wkScaleInChannelDrainInventoryFault string
			// if err := gofailScaleInChannelDrainInventoryFault(wkScaleInChannelDrainInventoryFault); err != nil {
			// 	resp.Unknown = true
			// 	resp.Safe = false
			// 	resp.LastError = err.Error()
			// 	return resp
			// }
			page, nextCursor, done, err := a.channelRuntimeMeta.ScanChannelRuntimeMetaSlotPage(ctx, slotID, after, limit)
```

This marker must remain disabled in normal builds and must not change existing
Channel inventory behavior.

- [ ] **Step 4: Verify management tests**

Run:

```bash
GOWORK=off go test ./internalv2/usecase/management -run 'TestScaleInChannelDrainInventoryGofail|TestNodeChannelDrainInventory|TestScaleInStatus' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit Task 2**

```bash
git add internalv2/usecase/management/channel_drain.go internalv2/usecase/management/gofail_markers_test.go
git commit -m "test: add scale-in channel inventory failpoint"
```

---

### Task 3: Add Mark-Removed Post-Commit Failpoint

**Files:**
- Modify: `pkg/controllerv2/runtime_node_lifecycle.go`
- Create: `pkg/controllerv2/gofail_markers_test.go`

- [ ] **Step 1: Add marker preservation and disabled behavior tests**

Create `pkg/controllerv2/gofail_markers_test.go`:

```go
package controllerv2

import (
	"os"
	"strings"
	"testing"
)

func TestMarkNodeRemovedPostCommitGofailMarkerIsPreserved(t *testing.T) {
	data, err := os.ReadFile("runtime_node_lifecycle.go")
	if err != nil {
		t.Fatalf("read runtime_node_lifecycle.go: %v", err)
	}
	text := string(data)
	for _, want := range []string{
		"// gofail: var wkMarkNodeRemovedPostCommitFault string",
		"// if err := gofailMarkNodeRemovedPostCommitFault(wkMarkNodeRemovedPostCommitFault); err != nil { return MarkNodeRemovedResult{}, err }",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing gofail marker %q", want)
		}
	}
}

func TestMarkNodeRemovedPostCommitGofailDisabled(t *testing.T) {
	if err := gofailMarkNodeRemovedPostCommitFault(""); err != nil {
		t.Fatalf("disabled failpoint returned error: %v", err)
	}
}
```

- [ ] **Step 2: Run the marker test and verify it fails**

Run:

```bash
GOWORK=off go test ./pkg/controllerv2 -run 'TestMarkNodeRemovedPostCommitGofail' -count=1
```

Expected: FAIL because the helper and marker do not exist.

- [ ] **Step 3: Add the helper and post-commit marker**

In `pkg/controllerv2/runtime_node_lifecycle.go`, add:

```go
func gofailMarkNodeRemovedPostCommitFault(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	return fmt.Errorf("controllerv2: %s", raw)
}
```

The file already imports `fmt` and `strings`.

In `MarkNodeRemoved`, after `publishFromState(ctx)` succeeds and before
`updated, err := r.LocalState(ctx)`:

```go
	if err := r.publishFromState(ctx); err != nil {
		return MarkNodeRemovedResult{}, err
	}
	// gofail: var wkMarkNodeRemovedPostCommitFault string
	// if err := gofailMarkNodeRemovedPostCommitFault(wkMarkNodeRemovedPostCommitFault); err != nil { return MarkNodeRemovedResult{}, err }
	updated, err := r.LocalState(ctx)
```

This deliberately simulates response loss after the durable commit and publish
path, not a pre-write RPC failure.

- [ ] **Step 4: Verify controllerv2 tests**

Run:

```bash
GOWORK=off go test ./pkg/controllerv2 -run 'TestMarkNodeRemoved|TestMarkNodeRemovedPostCommitGofail|SlotReplicaMove' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit Task 3**

```bash
git add pkg/controllerv2/runtime_node_lifecycle.go pkg/controllerv2/gofail_markers_test.go
git commit -m "test: add mark removed post-commit failpoint"
```

---

### Task 4: Prove Channel And Runtime Fail-Closed Scale-In Status

**Files:**
- Create: `test/e2ev2/cluster/dynamic_node_faults/scale_in_fail_closed_fault_test.go`

- [ ] **Step 1: Add the Channel inventory fail-closed scenario**

Create `scale_in_fail_closed_fault_test.go`:

```go
//go:build e2e

package dynamic_node_faults

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2ev2/suite"
	"github.com/stretchr/testify/require"
)

func TestScaleInChannelInventoryFaultFailsClosed(t *testing.T) {
	requireGofailDynamicNodeEnabled(t)

	const joinToken = "e2ev2-gofail-channel-inventory-token"
	f := startFaultableDynamicCluster(t, joinToken)
	startActiveNode4(t, f, joinToken)

	start := f.manager.MustStartScaleIn(t, 4)
	require.Equal(t, "leaving", start.JoinState)
	drain := f.manager.MustSetScaleInDrain(t, 4, true)
	require.True(t, drain.Draining)

	for _, endpoint := range f.allFailpoints() {
		waitGofailListed(t, f.cluster, endpoint, scaleInChannelInventoryFault)
	}
	for _, endpoint := range f.allFailpoints() {
		enableGofail(t, f.cluster, endpoint, scaleInChannelInventoryFault, `return("temporary channel inventory fault")`)
		defer disableGofail(t, endpoint, scaleInChannelInventoryFault)
	}

	requireScaleInStatus(t, f, 4, func(status suite.NodeScaleInStatusDTO) error {
		if !status.UnknownChannelInventory || !status.BlockedByChannels {
			return fmt.Errorf("channel inventory did not fail closed: %#v", status)
		}
		if status.SafeToRemove {
			return fmt.Errorf("safe_to_remove=true while channel inventory is unknown")
		}
		return nil
	})
	requireAnyGofailCountAtLeast(t, f.cluster, f.allFailpoints(), scaleInChannelInventoryFault, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	status, body, err := f.manager.RemoveScaleInStatus(ctx, 4)
	cancel()
	requireScaleInRemoveConflict(t, status, body, err, f.cluster.DumpDiagnostics())
	f.manager.EventuallyNodeJoinState(t, 4, "leaving", 5*time.Second)

	for _, endpoint := range f.allFailpoints() {
		requireDisableGofail(t, endpoint, scaleInChannelInventoryFault)
	}
	safe := f.manager.EventuallyScaleInSafeToRemove(t, 4, 30*time.Second)
	require.True(t, safe.SafeToRemove, "status=%#v\n%s", safe, f.cluster.DumpDiagnostics())
	require.False(t, safe.UnknownChannelInventory, "status=%#v\n%s", safe, f.cluster.DumpDiagnostics())
	require.Zero(t, safe.ChannelLeaderCount, "status=%#v\n%s", safe, f.cluster.DumpDiagnostics())
	require.Zero(t, safe.ChannelReplicaCount, "status=%#v\n%s", safe, f.cluster.DumpDiagnostics())
	require.Zero(t, safe.ChannelISRCount, "status=%#v\n%s", safe, f.cluster.DumpDiagnostics())

	removed := f.manager.MustRemoveScaleInNode(t, 4)
	require.Equal(t, "removed", removed.JoinState)
}
```

- [ ] **Step 2: Add the manager runtime summary fail-closed scenario**

Append:

```go
func TestScaleInRuntimeSummaryFaultFailsClosed(t *testing.T) {
	requireGofailDynamicNodeEnabled(t)

	const joinToken = "e2ev2-gofail-runtime-summary-token"
	f := startFaultableDynamicCluster(t, joinToken)
	startActiveNode4(t, f, joinToken)

	start := f.manager.MustStartScaleIn(t, 4)
	require.Equal(t, "leaving", start.JoinState)

	for _, endpoint := range f.allFailpoints() {
		waitGofailListed(t, f.cluster, endpoint, clusterNetCallShardFault)
	}
	for _, endpoint := range f.allFailpoints() {
		enableGofail(t, f.cluster, endpoint, clusterNetCallShardFault, `return("manager_connection:`+scaleInRuntimeSummaryFaultMarker+`")`)
		requireGofailContains(t, f.cluster, endpoint, clusterNetCallShardFault, scaleInRuntimeSummaryFaultMarker)
		defer disableGofail(t, endpoint, clusterNetCallShardFault)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	drainStatus, drainBody, err := f.manager.SetScaleInDrainStatus(ctx, 4, true)
	cancel()
	requireScaleInRemoveConflict(t, drainStatus, drainBody, err, f.cluster.DumpDiagnostics())
	requireAnyGofailCountAtLeast(t, f.cluster, f.allFailpoints(), clusterNetCallShardFault, 1)

	requireScaleInStatus(t, f, 4, func(status suite.NodeScaleInStatusDTO) error {
		if !status.UnknownRuntime && !status.RuntimeUnknown {
			return fmt.Errorf("runtime summary did not become unknown: %#v", status)
		}
		if !status.BlockedByRuntimeDrain {
			return fmt.Errorf("runtime drain blocker missing: %#v", status)
		}
		if status.SafeToRemove {
			return fmt.Errorf("safe_to_remove=true while runtime summary is unknown")
		}
		return nil
	})

	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	removeStatus, removeBody, err := f.manager.RemoveScaleInStatus(ctx, 4)
	cancel()
	requireScaleInRemoveConflict(t, removeStatus, removeBody, err, f.cluster.DumpDiagnostics())
	f.manager.EventuallyNodeJoinState(t, 4, "leaving", 5*time.Second)

	for _, endpoint := range f.allFailpoints() {
		requireDisableGofail(t, endpoint, clusterNetCallShardFault)
	}
	drain := f.manager.MustSetScaleInDrain(t, 4, true)
	require.True(t, drain.Draining)
	safe := f.manager.EventuallyScaleInSafeToRemove(t, 4, 30*time.Second)
	require.True(t, safe.SafeToRemove, "status=%#v\n%s", safe, f.cluster.DumpDiagnostics())
	require.False(t, safe.UnknownRuntime || safe.RuntimeUnknown, "status=%#v\n%s", safe, f.cluster.DumpDiagnostics())

	removed := f.manager.MustRemoveScaleInNode(t, 4)
	require.Equal(t, "removed", removed.JoinState)
}
```

- [ ] **Step 3: Compile without opt-in faults**

Run:

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_faults -run 'TestScaleInChannelInventoryFaultFailsClosed|TestScaleInRuntimeSummaryFaultFailsClosed' -count=1 -timeout 2m -p=1
```

Expected: PASS with skips when `WK_E2EV2_GOFAIL_DYNAMIC_NODE` is unset.

- [ ] **Step 4: Build the gofail-enabled v2 binary**

Run:

```bash
scripts/build-gofail-binary.sh --cmd ./cmd/wukongimv2 --package internalv2/usecase/management --package pkg/clusterv2/tasks --package pkg/clusterv2/net --package pkg/controllerv2 --out /tmp/wukongimv2-gofail
```

Expected: script exits 0 and writes `/tmp/wukongimv2-gofail`.

- [ ] **Step 5: Run the fail-closed scenarios**

Run:

```bash
WK_E2EV2_BINARY=/tmp/wukongimv2-gofail WK_E2EV2_GOFAIL_DYNAMIC_NODE=1 GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_faults -run 'TestScaleInChannelInventoryFaultFailsClosed|TestScaleInRuntimeSummaryFaultFailsClosed' -count=1 -timeout 7m -p=1
```

Expected: PASS. Each test must prove non-zero gofail count, bounded remove conflict, node 4 still `leaving`, and successful remove after disabling the fault.

- [ ] **Step 6: Commit Task 4**

```bash
git add test/e2ev2/cluster/dynamic_node_faults/scale_in_fail_closed_fault_test.go
git commit -m "test: cover scale-in fail-closed faults"
```

---

### Task 5: Prove Removed Post-Commit Response Loss Is Idempotent

**Files:**
- Create: `test/e2ev2/cluster/dynamic_node_faults/scale_in_remove_post_commit_fault_test.go`

- [ ] **Step 1: Add the post-commit response-loss scenario**

Create `scale_in_remove_post_commit_fault_test.go`:

```go
//go:build e2e

package dynamic_node_faults

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2ev2/suite"
	"github.com/stretchr/testify/require"
)

func TestScaleInRemovePostCommitResponseLossIsIdempotent(t *testing.T) {
	requireGofailDynamicNodeEnabled(t)

	const joinToken = "e2ev2-gofail-remove-post-commit-token"
	f := startFaultableDynamicCluster(t, joinToken)
	startActiveNode4(t, f, joinToken)

	start := f.manager.MustStartScaleIn(t, 4)
	require.Equal(t, "leaving", start.JoinState)
	drain := f.manager.MustSetScaleInDrain(t, 4, true)
	require.True(t, drain.Draining)
	safe := f.manager.EventuallyScaleInSafeToRemove(t, 4, 30*time.Second)
	require.True(t, safe.SafeToRemove, "status=%#v\n%s", safe, f.cluster.DumpDiagnostics())

	for _, endpoint := range f.allFailpoints() {
		waitGofailListed(t, f.cluster, endpoint, markNodeRemovedPostCommitFault)
	}
	for _, endpoint := range f.allFailpoints() {
		enableGofail(t, f.cluster, endpoint, markNodeRemovedPostCommitFault, `return("temporary mark removed post commit fault")`)
		defer disableGofail(t, endpoint, markNodeRemovedPostCommitFault)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	status, body, err := f.manager.RemoveScaleInStatus(ctx, 4)
	cancel()
	if err == nil && status/100 == 2 {
		t.Fatalf("remove unexpectedly succeeded on post-commit response-loss request: status=%d body=%s\n%s", status, string(body), f.cluster.DumpDiagnostics())
	}
	errText := ""
	if err != nil {
		errText = err.Error()
	}
	require.True(t,
		strings.Contains(errText, "temporary mark removed post commit fault") ||
			strings.Contains(string(body), "temporary mark removed post commit fault"),
		"status=%d err=%v body=%s\n%s",
		status,
		err,
		string(body),
		f.cluster.DumpDiagnostics(),
	)
	requireAnyGofailCountAtLeast(t, f.cluster, f.allFailpoints(), markNodeRemovedPostCommitFault, 1)

	for _, endpoint := range f.allFailpoints() {
		requireDisableGofail(t, endpoint, markNodeRemovedPostCommitFault)
	}

	f.manager.EventuallyNodeJoinState(t, 4, "removed", 20*time.Second)
	second := f.manager.MustRemoveScaleInNode(t, 4)
	require.False(t, second.Changed, "second remove should be idempotent: %#v\n%s", second, f.cluster.DumpDiagnostics())
	require.Equal(t, "removed", second.JoinState)
	requireScaleInStatus(t, f, 4, func(status suite.NodeScaleInStatusDTO) error {
		if status.FailedTaskCount != 0 || status.ActiveTaskCount != 0 {
			return fmt.Errorf("unexpected task counters after post-commit response loss: %#v", status)
		}
		return nil
	})
}
```

- [ ] **Step 2: Compile without opt-in faults**

Run:

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_faults -run TestScaleInRemovePostCommitResponseLossIsIdempotent -count=1 -timeout 2m -p=1
```

Expected: PASS with skip when `WK_E2EV2_GOFAIL_DYNAMIC_NODE` is unset.

- [ ] **Step 3: Run the post-commit scenario**

Run:

```bash
WK_E2EV2_BINARY=/tmp/wukongimv2-gofail WK_E2EV2_GOFAIL_DYNAMIC_NODE=1 GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_faults -run TestScaleInRemovePostCommitResponseLossIsIdempotent -count=1 -timeout 5m -p=1
```

Expected: PASS. First remove must fail after the post-commit marker, and retry must prove `removed` idempotency with `changed=false`.

- [ ] **Step 4: Commit Task 5**

```bash
git add test/e2ev2/cluster/dynamic_node_faults/scale_in_remove_post_commit_fault_test.go
git commit -m "test: cover removed post-commit retry"
```

---

### Task 6: Prove Delayed Slot Drain Does Not Permit Early Remove

**Files:**
- Create: `test/e2ev2/cluster/dynamic_node_faults/scale_in_slot_drain_fault_test.go`

- [ ] **Step 1: Add the delayed scale-in Slot drain scenario**

Create `scale_in_slot_drain_fault_test.go`:

```go
//go:build e2e

package dynamic_node_faults

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2ev2/suite"
	"github.com/stretchr/testify/require"
)

func TestScaleInSlotDrainSurvivesDelayedLeaderTransfer(t *testing.T) {
	requireGofailDynamicNodeEnabled(t)

	const joinToken = "e2ev2-gofail-scale-in-slot-drain-token"
	f := startFaultableDynamicCluster(t, joinToken)
	startActiveNode4(t, f, joinToken)
	onboardOneSlotToNode4(t, f)
	plan := startScaleInDrainWithOneSlotMove(t, f)
	candidate := plan.Candidates[0]
	require.Equal(t, uint64(4), candidate.SourceNodeID, "candidate=%#v\n%s", candidate, f.cluster.DumpDiagnostics())

	for _, endpoint := range f.allFailpoints() {
		waitGofailListed(t, f.cluster, endpoint, slotReplicaMoveTransferLeaderDelay)
	}
	for _, endpoint := range f.allFailpoints() {
		enableGofail(t, f.cluster, endpoint, slotReplicaMoveTransferLeaderDelay, `return("2s")`)
		defer disableGofail(t, endpoint, slotReplicaMoveTransferLeaderDelay)
	}

	advance := f.manager.MustAdvanceScaleIn(t, 4, 1)
	require.Equal(t, uint32(1), advance.Created, "advance=%#v\n%s", advance, f.cluster.DumpDiagnostics())
	active := waitScaleInTaskActive(t, f, 4, 10*time.Second)
	require.False(t, active.SafeToRemove, "status=%#v\n%s", active, f.cluster.DumpDiagnostics())
	requireAnyGofailCountAtLeast(t, f.cluster, f.allFailpoints(), slotReplicaMoveTransferLeaderDelay, 1)
	requireScaleInStatus(t, f, 4, func(status suite.NodeScaleInStatusDTO) error {
		if status.SafeToRemove {
			return fmt.Errorf("safe_to_remove=true while delayed Slot drain is still active")
		}
		if !status.BlockedBySlots && !status.BlockedByTasks {
			return fmt.Errorf("expected Slot or task blocker while delayed; status=%#v", status)
		}
		return nil
	})

	for _, endpoint := range f.allFailpoints() {
		requireDisableGofail(t, endpoint, slotReplicaMoveTransferLeaderDelay)
	}
	safe := f.manager.EventuallyScaleInSafeToRemove(t, 4, 90*time.Second)
	require.True(t, safe.SafeToRemove, "status=%#v\n%s", safe, f.cluster.DumpDiagnostics())
	require.False(t, safe.BlockedBySlots, "status=%#v\n%s", safe, f.cluster.DumpDiagnostics())
	require.Zero(t, safe.FailedTaskCount, "status=%#v\n%s", safe, f.cluster.DumpDiagnostics())

	removed := f.manager.MustRemoveScaleInNode(t, 4)
	require.Equal(t, "removed", removed.JoinState)
}
```

- [ ] **Step 2: Compile the scenario without opt-in faults**

Run:

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_faults -run TestScaleInSlotDrainSurvivesDelayedLeaderTransfer -count=1 -timeout 2m -p=1
```

Expected: PASS with skip when `WK_E2EV2_GOFAIL_DYNAMIC_NODE` is unset.

- [ ] **Step 3: Run the gofail scenario**

Run:

```bash
WK_E2EV2_BINARY=/tmp/wukongimv2-gofail WK_E2EV2_GOFAIL_DYNAMIC_NODE=1 GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_faults -run TestScaleInSlotDrainSurvivesDelayedLeaderTransfer -count=1 -timeout 7m -p=1
```

Expected: PASS. The test must observe `wkSlotReplicaMoveTransferLeaderDelay` count and show `safe_to_remove=false` while the delayed Slot drain is active.

- [ ] **Step 4: Commit Task 6**

```bash
git add test/e2ev2/cluster/dynamic_node_faults/scale_in_slot_drain_fault_test.go
git commit -m "test: cover delayed scale-in slot drain"
```

---

### Task 7: Prove Restart During Scale-In Slot Drain Recovers

**Files:**
- Modify: `test/e2ev2/cluster/dynamic_node_faults/scale_in_slot_drain_fault_test.go`

- [ ] **Step 1: Add generic delayed-failpoint disable helper**

Update the imports in `scale_in_slot_drain_fault_test.go` to add `strings`,
then append:

```go
func requireDisableDelayFailpoint(t testing.TB, endpoint suite.GofailEndpoint, failpoint string, tolerateDisabled bool) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := endpoint.Disable(ctx, failpoint)
	if err == nil {
		return
	}
	if tolerateDisabled && strings.Contains(err.Error(), "failpoint is disabled") {
		return
	}
	require.NoError(t, err)
}
```

- [ ] **Step 2: Add the restart recovery scenario**

Append:

```go
func TestLeavingNodeRestartDuringScaleInSlotDrainRecovers(t *testing.T) {
	requireGofailDynamicNodeEnabled(t)

	const joinToken = "e2ev2-gofail-restart-scale-in-token"
	f := startFaultableDynamicCluster(t, joinToken)
	startActiveNode4(t, f, joinToken)
	onboardOneSlotToNode4(t, f)
	plan := startScaleInDrainWithOneSlotMove(t, f)
	require.Equal(t, uint64(4), plan.Candidates[0].SourceNodeID, "plan=%#v\n%s", plan, f.cluster.DumpDiagnostics())

	for _, endpoint := range f.allFailpoints() {
		waitGofailListed(t, f.cluster, endpoint, slotReplicaMoveTransferLeaderDelay)
	}
	for _, endpoint := range f.allFailpoints() {
		enableGofail(t, f.cluster, endpoint, slotReplicaMoveTransferLeaderDelay, `return("30s")`)
		defer disableGofail(t, endpoint, slotReplicaMoveTransferLeaderDelay)
	}

	advance := f.manager.MustAdvanceScaleIn(t, 4, 1)
	require.Equal(t, uint32(1), advance.Created, "advance=%#v\n%s", advance, f.cluster.DumpDiagnostics())
	active := waitScaleInTaskActive(t, f, 4, 30*time.Second)
	require.False(t, active.SafeToRemove, "status=%#v\n%s", active, f.cluster.DumpDiagnostics())
	requireAnyGofailCountAtLeast(t, f.cluster, f.allFailpoints(), slotReplicaMoveTransferLeaderDelay, 1)

	t.Logf("restarting node 4 during scale-in Slot drain: status=%#v", active)
	require.NoError(t, f.cluster.RestartNode(4), f.cluster.DumpDiagnostics())
	waitGofailListed(t, f.cluster, f.nodeFails[4], slotReplicaMoveTransferLeaderDelay)

	for _, endpoint := range f.staticFailpoints() {
		requireDisableDelayFailpoint(t, endpoint, slotReplicaMoveTransferLeaderDelay, false)
	}
	requireDisableDelayFailpoint(t, f.nodeFails[4], slotReplicaMoveTransferLeaderDelay, true)

	readyCtx, readyCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer readyCancel()
	require.NoError(t, f.cluster.WaitClusterReady(readyCtx), f.cluster.DumpDiagnostics())
	f.manager.EventuallyNodeJoinState(t, 4, "leaving", 20*time.Second)

	safe := f.manager.EventuallyScaleInSafeToRemove(t, 4, 90*time.Second)
	require.True(t, safe.SafeToRemove, "status=%#v\n%s", safe, f.cluster.DumpDiagnostics())
	require.Zero(t, safe.ActiveTaskCount, "status=%#v\n%s", safe, f.cluster.DumpDiagnostics())
	require.Zero(t, safe.FailedTaskCount, "status=%#v\n%s", safe, f.cluster.DumpDiagnostics())

	removed := f.manager.MustRemoveScaleInNode(t, 4)
	require.Equal(t, "removed", removed.JoinState)
	f.manager.EventuallyNodeJoinState(t, 4, "removed", 20*time.Second)
}
```

- [ ] **Step 3: Compile the scenario without opt-in faults**

Run:

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_faults -run TestLeavingNodeRestartDuringScaleInSlotDrainRecovers -count=1 -timeout 2m -p=1
```

Expected: PASS with skip when `WK_E2EV2_GOFAIL_DYNAMIC_NODE` is unset.

- [ ] **Step 4: Run the gofail scenario**

Run:

```bash
WK_E2EV2_BINARY=/tmp/wukongimv2-gofail WK_E2EV2_GOFAIL_DYNAMIC_NODE=1 GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_faults -run TestLeavingNodeRestartDuringScaleInSlotDrainRecovers -count=1 -timeout 8m -p=1
```

Expected: PASS. The test must prove node 4 stays `leaving` after restart, the Slot drain task recovers, and final remove reaches `removed` with zero failed tasks.

- [ ] **Step 5: Commit Task 7**

```bash
git add test/e2ev2/cluster/dynamic_node_faults/scale_in_slot_drain_fault_test.go
git commit -m "test: cover restart during scale-in slot drain"
```

---

### Task 8: Update Catalog And Stage Links

**Files:**
- Modify: `test/e2ev2/cluster/dynamic_node_faults/AGENTS.md`
- Modify: `docs/superpowers/plans/2026-06-24-internalv2-dynamic-node-lifecycle.md`
- Modify: `docs/superpowers/plans/2026-06-29-internalv2-dynamic-node-stage8-scale-in-fault-injection.md`

- [ ] **Step 1: Update dynamic_node_faults catalog**

In `test/e2ev2/cluster/dynamic_node_faults/AGENTS.md`, add Stage 8 coverage to
the scenario contract:

```markdown
- Prove scale-in Slot drain remains fail-closed while Slot replica movement is
  delayed and recovers after the delay clears.
- Prove Channel drain inventory and manager runtime-summary faults keep
  `safe_to_remove=false` and final remove bounded-conflict.
- Prove final remove is idempotent when the `removed` commit succeeds but the
  response is lost.
- Prove a leaving node restart during scale-in Slot drain keeps the durable task
  recoverable and reaches `removed` after drain.
```

Update the running command to include the new gofail packages:

```bash
scripts/build-gofail-binary.sh --cmd ./cmd/wukongimv2 --package internalv2/usecase/management --package pkg/controllerv2 --package pkg/clusterv2/tasks --package pkg/clusterv2/net --out /tmp/wukongimv2-gofail
WK_E2EV2_BINARY=/tmp/wukongimv2-gofail WK_E2EV2_GOFAIL_DYNAMIC_NODE=1 GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_faults -count=1 -timeout 12m -p=1
```

- [ ] **Step 2: Verify the dynamic-node master index**

In `docs/superpowers/plans/2026-06-24-internalv2-dynamic-node-lifecycle.md`,
confirm links for Stage 6, Stage 7, and Stage 8 exist under Source Spec and
Execution Order. The Stage 8 row must say:

```markdown
| 8 | Scale-In Fault Injection | `2026-06-29-internalv2-dynamic-node-stage8-scale-in-fault-injection.md` | Scale-in/remove fail-closed and post-commit retry proofs |
```

Confirm the Stage 8 gate is present:

````markdown
- [ ] **Gate 8: Stage 8 starts only after Stage 7 gofail dynamic-node suite passes**

Run:

```bash
scripts/build-gofail-binary.sh --cmd ./cmd/wukongimv2 --package pkg/clusterv2/tasks --package pkg/clusterv2/net --out /tmp/wukongimv2-gofail
WK_E2EV2_BINARY=/tmp/wukongimv2-gofail WK_E2EV2_GOFAIL_DYNAMIC_NODE=1 GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_faults -count=1 -timeout 10m -p=1
```

Expected: Stage 7 scenarios pass before Stage 8 adds scale-in/remove faults.
````

- [ ] **Step 3: Update this plan execution progress**

In this plan's `Execution Progress` section, check tasks only after their
commits land. Keep the checklist in task order.

- [ ] **Step 4: Commit Task 8**

```bash
git add test/e2ev2/cluster/dynamic_node_faults/AGENTS.md docs/superpowers/plans/2026-06-24-internalv2-dynamic-node-lifecycle.md docs/superpowers/plans/2026-06-29-internalv2-dynamic-node-stage8-scale-in-fault-injection.md
git commit -m "docs: link dynamic node stage 8 fault plan"
```

---

### Task 9: Final Verification And Review

**Files:**
- No new files. Verify the branch state and review the implemented plan.

- [ ] **Step 1: Run focused unit tests**

Run:

```bash
GOWORK=off go test ./internalv2/usecase/management ./pkg/controllerv2 ./pkg/clusterv2/tasks ./pkg/clusterv2/net -count=1
```

Expected: PASS.

- [ ] **Step 2: Run opt-out e2ev2 compile/skip verification**

Run:

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/suite ./test/e2ev2/cluster/dynamic_node_faults -count=1 -timeout 2m -p=1
```

Expected: PASS. Gofail scenarios skip without `WK_E2EV2_GOFAIL_DYNAMIC_NODE=1`.

- [ ] **Step 3: Build the full gofail binary**

Run:

```bash
scripts/build-gofail-binary.sh --cmd ./cmd/wukongimv2 --package internalv2/usecase/management --package pkg/controllerv2 --package pkg/clusterv2/tasks --package pkg/clusterv2/net --out /tmp/wukongimv2-gofail
```

Expected: PASS and `/tmp/wukongimv2-gofail` exists.

- [ ] **Step 4: Run the full opt-in gofail dynamic-node suite**

Run:

```bash
WK_E2EV2_BINARY=/tmp/wukongimv2-gofail WK_E2EV2_GOFAIL_DYNAMIC_NODE=1 GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_faults -count=1 -timeout 12m -p=1
```

Expected: PASS.

- [ ] **Step 5: Run formatting and diff checks**

Run:

```bash
gofmt -w internalv2/usecase/management/channel_drain.go internalv2/usecase/management/gofail_markers_test.go pkg/controllerv2/runtime_node_lifecycle.go pkg/controllerv2/gofail_markers_test.go test/e2ev2/cluster/dynamic_node_faults/scale_in_fault_helpers_test.go test/e2ev2/cluster/dynamic_node_faults/scale_in_fail_closed_fault_test.go test/e2ev2/cluster/dynamic_node_faults/scale_in_remove_post_commit_fault_test.go test/e2ev2/cluster/dynamic_node_faults/scale_in_slot_drain_fault_test.go
git diff --check
git status --short
```

Expected: `git diff --check` has no output and only intentional files are modified.

- [ ] **Step 6: Request code review**

Use `superpowers:requesting-code-review` with:

- Description: Stage 8 gofail-backed scale-in/remove fault injection.
- Requirements: This plan and the Stage 8 spec.
- Base SHA: commit before Task 1.
- Head SHA: current HEAD.

Fix Critical and Important findings before merge.

- [ ] **Step 7: Commit final review fixes**

If review or final verification required fixes:

```bash
git add <changed-files>
git commit -m "fix: address stage 8 fault review"
```

Expected: no uncommitted implementation changes remain except intentionally
untracked local artifacts such as `/tmp/wukongimv2-gofail`, which is outside the
repository.

## Execution Progress

- [ ] Task 1: Shared scale-in fault helpers
- [ ] Task 2: Channel inventory failpoint
- [ ] Task 3: Mark-removed post-commit failpoint
- [ ] Task 4: Channel/runtime fail-closed e2ev2
- [ ] Task 5: Removed post-commit idempotency e2ev2
- [ ] Task 6: Delayed scale-in Slot drain e2ev2
- [ ] Task 7: Restart during scale-in Slot drain e2ev2
- [ ] Task 8: Catalog and stage links
- [ ] Task 9: Final verification and review
