# InternalV2 Dynamic Node Stage 10A Gofail Readiness Recovery Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add opt-in gofail-backed recovery coverage for Stage 9 dynamic-node health freshness, ControllerV2 watch publication, and scale-in retry safety.

**Architecture:** Reuse the existing gofail build script, `test/e2ev2/suite.GofailEndpoint`, and `test/e2ev2/cluster/dynamic_node_faults` package. Add narrow failpoints only at ControllerV2 event/health boundaries, then prove recovery through public manager HTTP and real `cmd/wukongimv2` processes. Keep default unit tests and default e2ev2 runs free of gofail runtime requirements.

**Tech Stack:** Go, `go.etcd.io/gofail@v0.2.0`, ControllerV2, clusterv2 control runtime, internalv2 manager APIs, `test/e2ev2`, `scripts/build-gofail-binary.sh`.

---

## Source Links

- Stage 9 master plan: `docs/superpowers/plans/2026-06-29-internalv2-dynamic-node-stage9-production-readiness.md`
- Stage 9D real-traffic smoke: `docs/superpowers/plans/2026-06-29-internalv2-dynamic-node-stage9d-real-traffic-smoke.md`
- Stage 7 gofail plan: `docs/superpowers/plans/2026-06-26-internalv2-dynamic-node-stage7-gofail-fault-injection.md`
- Existing gofail scenario package: `test/e2ev2/cluster/dynamic_node_faults`
- Existing gofail helper: `test/e2ev2/suite/gofail.go`

## Scope

Stage 10A is a bounded recovery gate, not a broad chaos suite.

- In scope: failpoint exposure, health report pause/recovery, ControllerV2 watch event drop/recovery, scale-in concurrent retry safety while Slot drain is delayed.
- Out of scope: random fault scheduling, long soak testing, full network partition matrix, production `WK_*` failpoint configuration, generated gofail files committed to the repo.
- All new e2ev2 fault tests must skip unless `WK_E2EV2_GOFAIL_DYNAMIC_NODE=1` and `WK_E2EV2_BINARY` points to a gofail-enabled `cmd/wukongimv2`.

## File Map

- Modify: `pkg/controllerv2/runtime_node_health.go`
  - Add `wkReportNodeHealthFault` marker to pause health reports for selected nodes.
- Modify: `pkg/controllerv2/runtime_refresh.go`
  - Add `wkControllerV2StateEventDrop` marker to drop ControllerV2 watch events for selected revisions.
- Modify: `pkg/controllerv2/gofail_markers_test.go`
  - Preserve both new markers and test helper parsing.
- Modify: `test/e2ev2/cluster/dynamic_node_faults/AGENTS.md`
  - Add Stage 10A scenario contract and updated failpoint list.
- Modify: `test/e2ev2/cluster/dynamic_node_faults/gofail_smoke_test.go`
  - Require the two new failpoints to be listed.
- Create: `test/e2ev2/cluster/dynamic_node_faults/stage10a_helpers_test.go`
  - Shared short health TTL overrides and manager polling helpers.
- Create: `test/e2ev2/cluster/dynamic_node_faults/health_readiness_fault_test.go`
  - Health report pause makes active node unschedulable, then recovers.
- Create: `test/e2ev2/cluster/dynamic_node_faults/control_watch_recovery_fault_test.go`
  - Dropped ControllerV2 state event does not permanently hide a lifecycle transition.
- Create: `test/e2ev2/cluster/dynamic_node_faults/scale_in_retry_fault_test.go`
  - Concurrent scale-in advance remains bounded while an existing gofail delay holds the Slot drain task active.
- Modify: `test/e2ev2/AGENTS.md` and `test/e2ev2/cluster/AGENTS.md`
  - Keep catalog commands current for Stage 10A.

---

### Task 1: Add ControllerV2 Health And Watch Failpoints

**Files:**
- Modify: `pkg/controllerv2/runtime_node_health.go`
- Modify: `pkg/controllerv2/runtime_refresh.go`
- Modify: `pkg/controllerv2/gofail_markers_test.go`

- [ ] **Step 1: Write failing marker/helper tests**

Add these tests to `pkg/controllerv2/gofail_markers_test.go`:

```go
func TestStage10AGofailMarkersArePreserved(t *testing.T) {
	files := map[string][]string{
		"runtime_node_health.go": {
			"// gofail: var wkReportNodeHealthFault string",
			"// if err := gofailReportNodeHealthFault(wkReportNodeHealthFault, req.NodeID); err != nil { return ReportNodeHealthResult{}, err }",
		},
		"runtime_refresh.go": {
			"// gofail: var wkControllerV2StateEventDrop string",
			"// if gofailDropControllerV2StateEvent(wkControllerV2StateEventDrop, st.Revision) { return }",
		},
	}
	for file, wants := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		text := string(data)
		for _, want := range wants {
			if !strings.Contains(text, want) {
				t.Fatalf("%s missing marker %q", file, want)
			}
		}
	}
}

func TestGofailReportNodeHealthFaultMatchesNode(t *testing.T) {
	if err := gofailReportNodeHealthFault("", 4); err != nil {
		t.Fatalf("disabled failpoint returned %v", err)
	}
	if err := gofailReportNodeHealthFault("4:paused health", 4); err == nil || !strings.Contains(err.Error(), "paused health") {
		t.Fatalf("node-specific match error = %v, want paused health", err)
	}
	if err := gofailReportNodeHealthFault("4:paused health", 3); err != nil {
		t.Fatalf("non-matching node returned %v", err)
	}
	if err := gofailReportNodeHealthFault("all:paused health", 3); err == nil || !strings.Contains(err.Error(), "paused health") {
		t.Fatalf("all match error = %v, want paused health", err)
	}
}

func TestGofailDropControllerV2StateEventMatchesRevision(t *testing.T) {
	if gofailDropControllerV2StateEvent("", 10) {
		t.Fatal("disabled failpoint dropped event")
	}
	if !gofailDropControllerV2StateEvent("all", 10) {
		t.Fatal("all failpoint did not drop event")
	}
	if !gofailDropControllerV2StateEvent("revision:10", 10) {
		t.Fatal("revision-specific failpoint did not drop matching event")
	}
	if gofailDropControllerV2StateEvent("revision:10", 11) {
		t.Fatal("revision-specific failpoint dropped non-matching event")
	}
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run:

```bash
GOWORK=off go test ./pkg/controllerv2 -run 'TestStage10AGofailMarkersArePreserved|TestGofailReportNodeHealthFaultMatchesNode|TestGofailDropControllerV2StateEventMatchesRevision' -count=1
```

Expected: FAIL because the new helpers and markers do not exist yet.

- [ ] **Step 3: Add health failpoint helper and marker**

In `pkg/controllerv2/runtime_node_health.go`, extend the existing import block with `fmt`, `strconv`, and `strings`. Keep the existing `command` and `state` imports:

```go
import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/command"
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/state"
)
```

Add the marker near the start of `ReportNodeHealth`, after `ctxErr` and nil checks and before `nowFunc`:

```go
	// gofail: var wkReportNodeHealthFault string
	// if err := gofailReportNodeHealthFault(wkReportNodeHealthFault, req.NodeID); err != nil { return ReportNodeHealthResult{}, err }
```

Add the helper at file scope:

```go
func gofailReportNodeHealthFault(raw string, nodeID uint64) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	target, message, ok := strings.Cut(raw, ":")
	if !ok {
		return fmt.Errorf("controllerv2: %s", raw)
	}
	target = strings.TrimSpace(target)
	message = strings.TrimSpace(message)
	if message == "" {
		message = "node health report fault"
	}
	if target == "all" {
		return fmt.Errorf("controllerv2: %s", message)
	}
	want, err := strconv.ParseUint(target, 10, 64)
	if err != nil || want != nodeID {
		return nil
	}
	return fmt.Errorf("controllerv2: %s", message)
}
```

- [ ] **Step 4: Add watch event drop helper and marker**

In `pkg/controllerv2/runtime_refresh.go`, extend the existing import block with `strconv` and `strings`. Keep the existing `cv2raft` and `goroutine` imports:

```go
import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	cv2raft "github.com/WuKongIM/WuKongIM/pkg/controllerv2/raft"
	"github.com/WuKongIM/WuKongIM/pkg/goroutine"
)
```

At the start of `publishLatestStateEvent`, after the nil channel check:

```go
	// gofail: var wkControllerV2StateEventDrop string
	// if gofailDropControllerV2StateEvent(wkControllerV2StateEventDrop, st.Revision) { return }
```

Add the helper at file scope:

```go
func gofailDropControllerV2StateEvent(raw string, revision uint64) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	if raw == "all" {
		return true
	}
	target, value, ok := strings.Cut(raw, ":")
	if !ok || strings.TrimSpace(target) != "revision" {
		return false
	}
	want, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
	return err == nil && want == revision
}
```

- [ ] **Step 5: Verify Task 1**

Run:

```bash
gofmt -w pkg/controllerv2/runtime_node_health.go pkg/controllerv2/runtime_refresh.go pkg/controllerv2/gofail_markers_test.go
GOWORK=off go test ./pkg/controllerv2 -run 'TestStage10AGofailMarkersArePreserved|TestGofailReportNodeHealthFaultMatchesNode|TestGofailDropControllerV2StateEventMatchesRevision|TestRuntimePublishStateCoalescesLatestEventWhenWatchFull|TestRuntimeReportNodeHealthPersistsWithoutRevisionBump' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit Task 1**

```bash
git add pkg/controllerv2/runtime_node_health.go pkg/controllerv2/runtime_refresh.go pkg/controllerv2/gofail_markers_test.go
git commit -m "test: add dynamic node readiness failpoints"
```

---

### Task 2: Add Stage 10A E2E Fault Helpers And Smoke Listing

**Files:**
- Modify: `test/e2ev2/cluster/dynamic_node_faults/gofail_smoke_test.go`
- Create: `test/e2ev2/cluster/dynamic_node_faults/stage10a_helpers_test.go`

- [ ] **Step 1: Extend gofail smoke listing**

In `TestGofailDynamicNodeBinaryExposesFailpoints`, add these names to `WaitListed`:

```go
		"wkReportNodeHealthFault",
		"wkControllerV2StateEventDrop",
```

- [ ] **Step 2: Add shared helper constants and health polling**

Create `test/e2ev2/cluster/dynamic_node_faults/stage10a_helpers_test.go`:

```go
//go:build e2e

package dynamic_node_faults

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2ev2/suite"
)

const (
	reportNodeHealthFault          = "wkReportNodeHealthFault"
	controllerV2StateEventDrop     = "wkControllerV2StateEventDrop"
	stage10AHealthReportInterval   = "200ms"
	stage10AHealthReportTTL        = "2s"
)

func stage10AOverrides() map[string]string {
	return map[string]string{
		"WK_BENCH_API_ENABLE":                    "true",
		"WK_METRICS_ENABLE":                      "true",
		"WK_CLUSTER_NODE_HEALTH_REPORT_INTERVAL": stage10AHealthReportInterval,
		"WK_CLUSTER_NODE_HEALTH_REPORT_TTL":      stage10AHealthReportTTL,
		"WK_DELIVERY_ENABLE":                     "true",
	}
}

func startStage10AFaultableCluster(t testing.TB, joinToken string) faultableDynamicCluster {
	t.Helper()
	nodeFails := map[uint64]suite.GofailEndpoint{
		1: suite.ReserveGofailEndpoint(t),
		2: suite.ReserveGofailEndpoint(t),
		3: suite.ReserveGofailEndpoint(t),
		4: suite.ReserveGofailEndpoint(t),
	}
	overrides := stage10AOverrides()
	s := suite.New(testingT(t))
	cluster := s.StartThreeNodeCluster(
		suite.WithManagerHTTP(),
		suite.WithDynamicJoinToken(joinToken),
		suite.WithNodeConfigOverrides(1, overrides),
		suite.WithNodeConfigOverrides(2, overrides),
		suite.WithNodeConfigOverrides(3, overrides),
		suite.WithNodeConfigOverrides(4, overrides),
		suite.WithNodeEnv(1, nodeFails[1].Env()),
		suite.WithNodeEnv(2, nodeFails[2].Env()),
		suite.WithNodeEnv(3, nodeFails[3].Env()),
		suite.WithNodeEnv(4, nodeFails[4].Env()),
	)
	readyCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := cluster.WaitClusterReady(readyCtx); err != nil {
		t.Fatalf("cluster not ready: %v\n%s", err, cluster.DumpDiagnostics())
	}
	return faultableDynamicCluster{cluster: cluster, manager: cluster.ManagerClient(t, 1), nodeFails: nodeFails}
}

func waitNodeHealthState(t testing.TB, f faultableDynamicCluster, nodeID uint64, timeout time.Duration, check func(suite.NodeDTO) error) suite.NodeDTO {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var last suite.NodeDTO
	var lastErr error
	for {
		reqCtx, cancelReq := context.WithTimeout(ctx, 3*time.Second)
		list, err := f.manager.ListNodes(reqCtx)
		cancelReq()
		if err == nil {
			for _, item := range list.Items {
				if item.NodeID != nodeID {
					continue
				}
				last = item
				if checkErr := check(item); checkErr == nil {
					return item
				} else {
					lastErr = checkErr
				}
			}
			if last.NodeID == 0 {
				lastErr = fmt.Errorf("node %d missing from manager list", nodeID)
			}
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			t.Fatalf("node %d health state did not match: last=%#v lastErr=%v\n%s", nodeID, last, lastErr, f.cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func waitNodeSchedulable(t testing.TB, f faultableDynamicCluster, nodeID uint64, timeout time.Duration) suite.NodeDTO {
	t.Helper()
	return waitNodeHealthState(t, f, nodeID, timeout, func(node suite.NodeDTO) error {
		if node.Membership.Schedulable && node.Health.Fresh && node.Health.Status == "alive" && node.Health.RuntimeReady {
			return nil
		}
		return fmt.Errorf("node not schedulable: membership=%#v health=%#v", node.Membership, node.Health)
	})
}

func waitNodeUnschedulableBecauseHealthStale(t testing.TB, f faultableDynamicCluster, nodeID uint64, timeout time.Duration) suite.NodeDTO {
	t.Helper()
	return waitNodeHealthState(t, f, nodeID, timeout, func(node suite.NodeDTO) error {
		if !node.Membership.Schedulable && !node.Health.Fresh && node.Health.Freshness == "stale" {
			return nil
		}
		return fmt.Errorf("node did not become stale/unschedulable: membership=%#v health=%#v", node.Membership, node.Health)
	})
}
```

- [ ] **Step 3: Verify helpers compile without gofail env**

Run:

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_faults -run 'TestGofailDynamicNodeBinaryExposesFailpoints|TestScaleInTaskActiveStatusRejectsFailedBlockedTasks' -count=1 -timeout 2m -p=1
```

Expected: PASS with gofail smoke skipped if `WK_E2EV2_GOFAIL_DYNAMIC_NODE` is not set.

- [ ] **Step 4: Commit Task 2**

```bash
git add test/e2ev2/cluster/dynamic_node_faults/gofail_smoke_test.go test/e2ev2/cluster/dynamic_node_faults/stage10a_helpers_test.go
git commit -m "test: add stage10a gofail readiness helpers"
```

---

### Task 3: Prove Health Report Fault Fails Closed And Recovers

**Files:**
- Create: `test/e2ev2/cluster/dynamic_node_faults/health_readiness_fault_test.go`

- [ ] **Step 1: Write the opt-in e2e test**

Create `test/e2ev2/cluster/dynamic_node_faults/health_readiness_fault_test.go`:

```go
//go:build e2e

package dynamic_node_faults

import (
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2ev2/suite"
	"github.com/stretchr/testify/require"
)

func TestStage10AHealthReportFaultMakesActiveNodeUnschedulableThenRecovers(t *testing.T) {
	requireGofailDynamicNodeEnabled(t)

	const joinToken = "e2ev2-stage10a-health-fault-token"
	f := startStage10AFaultableCluster(t, joinToken)
	startActiveNode4(t, f, joinToken)
	waitNodeSchedulable(t, f, 4, 30*time.Second)

	waitGofailListed(t, f.cluster, f.nodeFails[4], reportNodeHealthFault)
	enableGofail(t, f.cluster, f.nodeFails[4], reportNodeHealthFault, `return("4:stage10a health report paused")`)
	defer disableGofail(t, f.nodeFails[4], reportNodeHealthFault)

	stale := waitNodeUnschedulableBecauseHealthStale(t, f, 4, 10*time.Second)
	require.False(t, stale.Membership.Schedulable, "node=%#v\n%s", stale, f.cluster.DumpDiagnostics())
	require.False(t, stale.Health.Fresh, "node=%#v\n%s", stale, f.cluster.DumpDiagnostics())
	require.Equal(t, "stale", stale.Health.Freshness, "node=%#v\n%s", stale, f.cluster.DumpDiagnostics())

	plan := f.manager.MustPlanOnboarding(t, 4, 1)
	require.True(t, plan.BlockedByStatus || len(plan.Candidates) == 0, "plan=%#v node=%#v\n%s", plan, stale, f.cluster.DumpDiagnostics())
	requireAnyGofailCountAtLeast(t, f.cluster, []suite.GofailEndpoint{f.nodeFails[4]}, reportNodeHealthFault, 1)

	requireDisableGofail(t, f.nodeFails[4], reportNodeHealthFault)
	fresh := waitNodeSchedulable(t, f, 4, 30*time.Second)
	require.True(t, fresh.Membership.Schedulable, "node=%#v\n%s", fresh, f.cluster.DumpDiagnostics())
}
```

- [ ] **Step 2: Run without opt-in and verify it skips**

Run:

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_faults -run TestStage10AHealthReportFaultMakesActiveNodeUnschedulableThenRecovers -count=1 -timeout 2m -p=1
```

Expected: PASS with skip message when `WK_E2EV2_GOFAIL_DYNAMIC_NODE` is not set.

- [ ] **Step 3: Build gofail binary**

Run:

```bash
scripts/build-gofail-binary.sh --cmd ./cmd/wukongimv2 --package internalv2/usecase/management --package pkg/controllerv2 --package pkg/clusterv2/tasks --package pkg/clusterv2/net --out /tmp/wukongimv2-gofail
```

Expected: binary created at `/tmp/wukongimv2-gofail`.

- [ ] **Step 4: Run opt-in health recovery test**

Run:

```bash
WK_E2EV2_BINARY=/tmp/wukongimv2-gofail WK_E2EV2_GOFAIL_DYNAMIC_NODE=1 GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_faults -run TestStage10AHealthReportFaultMakesActiveNodeUnschedulableThenRecovers -count=1 -timeout 8m -p=1
```

Expected: PASS. If it fails, collect manager node-list health fields, gofail count, and node diagnostics before changing code.

- [ ] **Step 5: Commit Task 3**

```bash
git add test/e2ev2/cluster/dynamic_node_faults/health_readiness_fault_test.go
git commit -m "test: prove health fault fail-closed recovery"
```

---

### Task 4: Prove Dropped ControllerV2 State Events Recover

**Files:**
- Create: `test/e2ev2/cluster/dynamic_node_faults/control_watch_recovery_fault_test.go`

- [ ] **Step 1: Write the opt-in watch recovery test**

Create `test/e2ev2/cluster/dynamic_node_faults/control_watch_recovery_fault_test.go`:

```go
//go:build e2e

package dynamic_node_faults

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStage10AControllerV2StateEventDropRecoversAfterHealthWakeup(t *testing.T) {
	requireGofailDynamicNodeEnabled(t)

	const joinToken = "e2ev2-stage10a-watch-drop-token"
	f := startStage10AFaultableCluster(t, joinToken)
	startActiveNode4(t, f, joinToken)
	waitNodeSchedulable(t, f, 4, 30*time.Second)

	for _, endpoint := range f.staticFailpoints() {
		waitGofailListed(t, f.cluster, endpoint, controllerV2StateEventDrop)
		enableGofail(t, f.cluster, endpoint, controllerV2StateEventDrop, `return("all")`)
		defer disableGofail(t, endpoint, controllerV2StateEventDrop)
	}

	start := f.manager.MustStartScaleIn(t, 4)
	require.Equal(t, "leaving", start.JoinState)
	for _, endpoint := range f.staticFailpoints() {
		requireAnyGofailCountAtLeast(t, f.cluster, []suite.GofailEndpoint{endpoint}, controllerV2StateEventDrop, 1)
		requireDisableGofail(t, endpoint, controllerV2StateEventDrop)
	}

	leaving := f.manager.EventuallyNodeJoinState(t, 4, "leaving", 30*time.Second)
	require.Equal(t, "leaving", leaving.Membership.JoinState, "node=%#v\n%s", leaving, f.cluster.DumpDiagnostics())

	drain := f.manager.MustSetScaleInDrain(t, 4, true)
	require.True(t, drain.Draining)
}
```

- [ ] **Step 2: Run opt-in watch recovery test**

Run:

```bash
WK_E2EV2_BINARY=/tmp/wukongimv2-gofail WK_E2EV2_GOFAIL_DYNAMIC_NODE=1 GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_faults -run TestStage10AControllerV2StateEventDropRecoversAfterHealthWakeup -count=1 -timeout 8m -p=1
```

Expected: PASS. The important assertion is not that the event is invisible forever; it is that after the failpoint is disabled, a subsequent health/report publication wakes the local control snapshot and manager can proceed.

- [ ] **Step 3: Commit Task 4**

```bash
git add test/e2ev2/cluster/dynamic_node_faults/control_watch_recovery_fault_test.go
git commit -m "test: prove controller watch drop recovery"
```

---

### Task 5: Prove Scale-In Concurrent Retry Stays Bounded Under Delay

**Files:**
- Create: `test/e2ev2/cluster/dynamic_node_faults/scale_in_retry_fault_test.go`
- Modify: `test/e2ev2/cluster/dynamic_node_faults/AGENTS.md`
- Modify: `test/e2ev2/AGENTS.md`
- Modify: `test/e2ev2/cluster/AGENTS.md`

- [ ] **Step 1: Write delayed concurrent scale-in advance test**

Create `test/e2ev2/cluster/dynamic_node_faults/scale_in_retry_fault_test.go`:

```go
//go:build e2e

package dynamic_node_faults

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStage10AScaleInConcurrentAdvanceStaysBoundedWhileSlotDrainDelayed(t *testing.T) {
	requireGofailDynamicNodeEnabled(t)

	const joinToken = "e2ev2-stage10a-scale-in-retry-token"
	f := startFaultableDynamicCluster(t, joinToken)
	startActiveNode4(t, f, joinToken)
	onboardOneSlotToNode4ForDelayedScaleIn(t, f)
	plan := startScaleInDrainWithOneSlotMove(t, f)
	candidate := plan.Candidates[0]

	for _, endpoint := range f.allFailpoints() {
		waitGofailListed(t, f.cluster, endpoint, slotReplicaMoveTransferLeaderDelay)
		enableGofail(t, f.cluster, endpoint, slotReplicaMoveTransferLeaderDelay, `return("5s")`)
		defer disableGofail(t, endpoint, slotReplicaMoveTransferLeaderDelay)
	}

	const attempts = 4
	type result struct {
		status int
		body   []byte
		err    error
	}
	results := make([]result, attempts)
	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, status, body, err := f.manager.AdvanceScaleIn(ctx, 4, 1)
			results[idx] = result{status: status, body: body, err: err}
		}(i)
	}
	wg.Wait()

	created := 0
	conflicts := 0
	for _, res := range results {
		require.NoError(t, res.err, "status=%d body=%s\n%s", res.status, string(res.body), f.cluster.DumpDiagnostics())
		switch res.status {
		case 200, 202:
			created++
		case 409:
			conflicts++
		default:
			t.Fatalf("unexpected advance status=%d body=%s\n%s", res.status, string(res.body), f.cluster.DumpDiagnostics())
		}
	}
	require.LessOrEqual(t, created, 1, "too many advance requests succeeded: results=%#v candidate=%#v\n%s", results, candidate, f.cluster.DumpDiagnostics())
	require.GreaterOrEqual(t, conflicts, 1, "expected at least one bounded conflict: results=%#v\n%s", results, f.cluster.DumpDiagnostics())

	waitScaleInTaskActive(t, f, 4, 10*time.Second)
	for _, endpoint := range f.allFailpoints() {
		requireDisableGofail(t, endpoint, slotReplicaMoveTransferLeaderDelay)
	}
	safe := f.manager.EventuallyScaleInSafeToRemove(t, 4, 90*time.Second)
	require.True(t, safe.SafeToRemove, "status=%#v\n%s", safe, f.cluster.DumpDiagnostics())
}
```

- [ ] **Step 2: Update scenario catalogs**

In `test/e2ev2/cluster/dynamic_node_faults/AGENTS.md`, add bullets:

```markdown
- Stage 10A coverage must prove health report faults make active nodes fail closed for new placement and recover after reports resume.
- Stage 10A coverage must prove a dropped ControllerV2 state event does not permanently hide a lifecycle transition after later health/report wakeups.
- Stage 10A coverage must prove concurrent scale-in advancement under delayed Slot drain produces at most one task and bounded conflicts, not 500s.
```

In `test/e2ev2/AGENTS.md` and `test/e2ev2/cluster/AGENTS.md`, keep the `dynamic_node_faults` command:

```bash
scripts/build-gofail-binary.sh --cmd ./cmd/wukongimv2 --package internalv2/usecase/management --package pkg/controllerv2 --package pkg/clusterv2/tasks --package pkg/clusterv2/net --out /tmp/wukongimv2-gofail
WK_E2EV2_BINARY=/tmp/wukongimv2-gofail WK_E2EV2_GOFAIL_DYNAMIC_NODE=1 GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_faults -count=1 -timeout 15m -p=1
```

- [ ] **Step 3: Run Stage 10A gate**

Run:

```bash
GOWORK=off go test ./pkg/controllerv2 -count=1
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_faults -count=1 -timeout 2m -p=1
scripts/build-gofail-binary.sh --cmd ./cmd/wukongimv2 --package internalv2/usecase/management --package pkg/controllerv2 --package pkg/clusterv2/tasks --package pkg/clusterv2/net --out /tmp/wukongimv2-gofail
WK_E2EV2_BINARY=/tmp/wukongimv2-gofail WK_E2EV2_GOFAIL_DYNAMIC_NODE=1 GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_faults -run 'TestStage10A|TestGofailDynamicNodeBinaryExposesFailpoints' -count=1 -timeout 15m -p=1
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_readiness -run TestDynamicNodeLifecycleWithContinuousTraffic -count=1 -timeout 12m -p=1
git diff --check
```

Expected: PASS. The non-gofail e2e command must skip opt-in tests cleanly; the gofail command must list and exercise the new failpoints; Stage 9D readiness must still pass after the failpoint additions.

- [ ] **Step 4: Commit Task 5**

```bash
git add test/e2ev2/cluster/dynamic_node_faults/scale_in_retry_fault_test.go test/e2ev2/cluster/dynamic_node_faults/AGENTS.md test/e2ev2/AGENTS.md test/e2ev2/cluster/AGENTS.md
git commit -m "test: prove stage10a dynamic node fault recovery"
```

---

## Stage 10A Completion Gate

Stage 10A is complete only when all of these pass on the development branch:

```bash
GOWORK=off go test ./pkg/controllerv2 -count=1
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_faults -count=1 -timeout 2m -p=1
scripts/build-gofail-binary.sh --cmd ./cmd/wukongimv2 --package internalv2/usecase/management --package pkg/controllerv2 --package pkg/clusterv2/tasks --package pkg/clusterv2/net --out /tmp/wukongimv2-gofail
WK_E2EV2_BINARY=/tmp/wukongimv2-gofail WK_E2EV2_GOFAIL_DYNAMIC_NODE=1 GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_faults -run 'TestStage10A|TestGofailDynamicNodeBinaryExposesFailpoints' -count=1 -timeout 15m -p=1
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_readiness -run TestDynamicNodeLifecycleWithContinuousTraffic -count=1 -timeout 12m -p=1
git diff --check
```

## Root-Cause Rules During Execution

- If a Stage 10A e2e fails, do not weaken assertions first. Capture the exact manager response, failpoint count, node diagnostics, and relevant metric/manager fields.
- If health does not become stale, verify `wkReportNodeHealthFault` count first, then verify `WK_CLUSTER_NODE_HEALTH_REPORT_TTL=2s` landed in the node config.
- If watch-drop recovery does not occur, check whether later health reports are listed and whether ControllerV2 `publishState` is re-emitting same-revision checksum changes.
- If concurrent scale-in returns 500, trace the error mapping from manager HTTP -> management usecase -> ControllerV2 rejection before changing the test.
- Keep all new failpoints low-cardinality and local to tests; do not add production config switches.
