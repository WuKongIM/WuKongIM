# InternalV2 Dynamic Node Stage 9D Real Traffic Smoke Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a black-box e2ev2 production-readiness smoke proving dynamic join, activation, Slot onboarding, scale-in, drain, and remove while real WKProto traffic continues.

**Architecture:** Create a dedicated `test/e2ev2/cluster/dynamic_node_readiness` package with its own scenario contract and helpers. The scenario uses real `cmd/wukongimv2` processes, manager HTTP, public metrics, and WKProto clients only; it does not import server internals or mutate ControllerV2 directly.

**Tech Stack:** Go e2e tests with `-tags=e2e`, `test/e2ev2/suite`, manager HTTP APIs, WKProto clients, public `/metrics`, optional `cmd/wkcli sim` manual smoke guidance.

---

## Source Links

- Stage 9 spec: `docs/superpowers/specs/2026-06-29-internalv2-dynamic-node-stage9-production-readiness-design.md`
- Stage 9 master plan: `docs/superpowers/plans/2026-06-29-internalv2-dynamic-node-stage9-production-readiness.md`
- Stage 9C prerequisite: `docs/superpowers/plans/2026-06-29-internalv2-dynamic-node-stage9c-observability-manager-evidence.md`
- e2ev2 rules:
  - `test/e2ev2/AGENTS.md`
  - `test/e2ev2/cluster/AGENTS.md`
  - `test/e2ev2/cluster/dynamic_node_join/AGENTS.md`

## Entry Gate

- [x] Stage 9C has been implemented and its gate has passed.
- [x] Manager node list exposes health freshness fields.
- [x] Manager scale-in status exposes health blocker fields and `blocked_reasons`.
- [x] `/metrics` exposes Stage 9 lifecycle and health metric families.

## File Map

- Create: `test/e2ev2/cluster/dynamic_node_readiness/AGENTS.md`
  - Scenario package contract and serial run command.
- Create: `test/e2ev2/cluster/dynamic_node_readiness/traffic_helpers_test.go`
  - Continuous WKProto traffic worker and public metrics assertions.
- Create: `test/e2ev2/cluster/dynamic_node_readiness/lifecycle_helpers_test.go`
  - Node health polling, Slot ownership polling, scale-in drain helpers.
- Create: `test/e2ev2/cluster/dynamic_node_readiness/production_readiness_test.go`
  - The primary real-traffic lifecycle test.
- Modify: `test/e2ev2/suite/manager_client.go`
  - Add Stage 9 health fields to DTOs and optional polling helpers if the scenario needs them.
- Modify: `test/e2ev2/AGENTS.md`
  - Add the new scenario package to the e2ev2 catalog.
- Modify: `docs/superpowers/plans/2026-06-29-internalv2-dynamic-node-stage9-production-readiness.md`
  - Mark Gate 9D after the test passes.

---

### Task 1: Create Scenario Contract

**Files:**
- Create: `test/e2ev2/cluster/dynamic_node_readiness/AGENTS.md`
- Modify: `test/e2ev2/AGENTS.md`

- [x] **Step 1: Add package AGENTS**

Create `test/e2ev2/cluster/dynamic_node_readiness/AGENTS.md`:

```markdown
# dynamic_node_readiness AGENTS

This package proves Stage 9 production readiness for internalv2 dynamic data
nodes under real traffic.

## Scenario Contract

- Start a static three-node `cmd/wukongimv2` cluster with manager HTTP, metrics,
  bench API, gateway listeners, and short test-only health report intervals.
- Keep real WKProto `SEND -> SENDACK` traffic running while membership changes.
- Start node 4 through seed join and wait for manager-visible `joining` plus
  fresh health evidence.
- Activate node 4 and prove it becomes schedulable only after fresh alive health.
- Start one bounded Slot onboarding move to node 4 while traffic continues.
- Mark node 4 leaving, enable gateway drain, advance scale-in Slot drain, wait
  for safe-to-remove, and remove node 4.
- Prove public manager status and public metrics explain health freshness and
  lifecycle blockers throughout the flow.

## Rules

- Keep tests black-box: do not import `internalv2/app`, `internalv2/usecase`,
  storage internals, ControllerV2 internals, or clusterv2 internals.
- Use public manager HTTP, public `/metrics`, WKProto clients, and process
  handles from `test/e2ev2/suite`.
- Prefer polling public status over fixed sleeps.
- Keep task fanout bounded: usually `max_slot_moves=1`.
- Run this package serially with `-p=1`.

## Running

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_readiness -count=1 -timeout 12m -p=1
```
```

- [x] **Step 2: Add e2ev2 catalog entry**

Add one row to `test/e2ev2/AGENTS.md`:

```markdown
| `cluster` | `test/e2ev2/cluster/dynamic_node_readiness` | Prove Stage 9 dynamic-node production readiness: health freshness, manager/metrics evidence, and join/onboard/scale-in/remove while real WKProto traffic continues. | `GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_readiness -count=1 -timeout 12m -p=1` |
```

- [x] **Step 3: Commit**

```bash
git add test/e2ev2/cluster/dynamic_node_readiness/AGENTS.md test/e2ev2/AGENTS.md
git commit -m "test: add dynamic node readiness scenario contract"
```

---

### Task 2: Add Continuous Traffic And Metrics Helpers

**Files:**
- Create: `test/e2ev2/cluster/dynamic_node_readiness/traffic_helpers_test.go`

- [x] **Step 1: Write helper file**

Create `traffic_helpers_test.go`:

```go
//go:build e2e

package dynamic_node_readiness

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2ev2/suite"
	"github.com/stretchr/testify/require"
)

type trafficWorker struct {
	client *suite.WKProtoClient
	stop   chan struct{}
	done   chan struct{}
	sent   atomic.Uint64
	errs   atomic.Uint64
}

func startTrafficWorker(t testing.TB, cluster *suite.StartedCluster, node *suite.StartedNode, prefix string) *trafficWorker {
	t.Helper()
	client, err := suite.NewWKProtoClient()
	require.NoError(t, err)
	uid := prefix + "-sender"
	require.NoError(t, client.Connect(node.GatewayAddr(), uid, uid+"-device"), node.DumpDiagnostics())

	worker := &trafficWorker{client: client, stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(worker.done)
		defer func() { _ = client.Close() }()
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for seq := uint64(1); ; seq++ {
			select {
			case <-worker.stop:
				return
			case <-ticker.C:
			}
			msgNo := fmt.Sprintf("%s-%06d", prefix, seq)
			err := client.SendFrame(&frame.SendPacket{
				ChannelID:   uid,
				ChannelType: frame.ChannelTypePerson,
				ClientSeq:   seq,
				ClientMsgNo: msgNo,
				Payload:     []byte(msgNo),
			})
			if err != nil {
				worker.errs.Add(1)
				continue
			}
			ack, err := client.ReadSendAck()
			if err != nil || ack.ReasonCode != frame.ReasonSuccess {
				worker.errs.Add(1)
				continue
			}
			worker.sent.Add(1)
		}
	}()
	requireTrafficProgress(t, cluster, worker, 2, 10*time.Second)
	return worker
}

func stopTrafficWorker(t testing.TB, worker *trafficWorker) {
	t.Helper()
	if worker == nil {
		return
	}
	close(worker.stop)
	select {
	case <-worker.done:
	case <-time.After(5 * time.Second):
		t.Fatal("traffic worker did not stop")
	}
}
```

- [x] **Step 2: Add assertions**

Append:

```go
func requireTrafficProgress(t testing.TB, cluster *suite.StartedCluster, worker *trafficWorker, additional uint64, timeout time.Duration) {
	t.Helper()
	start := worker.sent.Load()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if worker.errs.Load() != 0 {
			t.Fatalf("traffic worker recorded %d errors\n%s", worker.errs.Load(), cluster.DumpDiagnostics())
		}
		if worker.sent.Load() >= start+additional {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("traffic worker sent=%d, want at least %d more\n%s", worker.sent.Load(), additional, cluster.DumpDiagnostics())
}

func requireMetricsContain(t testing.TB, node *suite.StartedNode, names ...string) {
	t.Helper()
	resp, err := http.Get("http://" + node.Spec.APIAddr + "/metrics")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	buf := new(strings.Builder)
	_, err = io.Copy(buf, resp.Body)
	require.NoError(t, err)
	body := buf.String()
	for _, name := range names {
		if !strings.Contains(body, name) {
			t.Fatalf("metrics from node %d missing %q", node.Spec.NodeID, name)
		}
	}
}
```

Add the missing `io` import.

- [x] **Step 3: Run package compile and verify helper issues**

Run:

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_readiness -run TestDoesNotExist -count=1
```

Expected: package compiles or fails only because no tests exist yet. Fix helper compile errors before continuing.

- [x] **Step 4: Commit**

```bash
git add test/e2ev2/cluster/dynamic_node_readiness/traffic_helpers_test.go
git commit -m "test: add dynamic node readiness traffic helpers"
```

---

### Task 3: Add Lifecycle Polling Helpers

**Files:**
- Create: `test/e2ev2/cluster/dynamic_node_readiness/lifecycle_helpers_test.go`
- Modify: `test/e2ev2/suite/manager_client.go`

- [x] **Step 1: Extend suite DTOs for health fields**

In `test/e2ev2/suite/manager_client.go`, extend node and scale-in DTOs with Stage 9 fields:

```go
type NodeHealthDTO struct {
	Status                  string `json:"status"`
	LastHeartbeatAt         string `json:"last_heartbeat_at"`
	Fresh                   bool   `json:"fresh"`
	Freshness               string `json:"freshness"`
	RuntimeReady            bool   `json:"runtime_ready"`
	ReportAgeMS             int64  `json:"report_age_ms"`
	ReportTTLMS             int64  `json:"report_ttl_ms"`
	ObservedControlRevision uint64 `json:"observed_control_revision"`
	ObservedSlotRevision    uint64 `json:"observed_slot_revision"`
	ErrorCode               string `json:"error_code"`
}
```

Add equivalent fields to `NodeScaleInStatusDTO`:

```go
BlockedByHealth         bool     `json:"blocked_by_health"`
BlockedByStaleRevision  bool     `json:"blocked_by_stale_revision"`
HealthFresh             bool     `json:"health_fresh"`
HealthStatus            string   `json:"health_status"`
HealthFreshness         string   `json:"health_freshness"`
HealthReportAgeMS       int64    `json:"health_report_age_ms"`
HealthReportTTLMS       int64    `json:"health_report_ttl_ms"`
ObservedControlRevision uint64   `json:"observed_control_revision"`
RequiredControlRevision uint64   `json:"required_control_revision"`
BlockedReasons          []string `json:"blocked_reasons"`
```

- [x] **Step 2: Create lifecycle helpers**

Create `lifecycle_helpers_test.go`:

```go
//go:build e2e

package dynamic_node_readiness

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2ev2/suite"
	"github.com/stretchr/testify/require"
)

func readinessOverrides() map[string]string {
	return map[string]string{
		"WK_BENCH_API_ENABLE":                         "true",
		"WK_METRICS_ENABLE":                           "true",
		"WK_TOP_API_ENABLE":                           "true",
		"WK_TOP_COLLECT_INTERVAL":                     "100ms",
		"WK_TOP_HISTORY_WINDOW":                       "2s",
		"WK_CLUSTER_NODE_HEALTH_REPORT_INTERVAL":      "500ms",
		"WK_CLUSTER_NODE_HEALTH_REPORT_TTL":           "3s",
		"WK_DELIVERY_ENABLE":                          "true",
	}
}

func eventuallyNodeHealthFresh(t testing.TB, cluster *suite.StartedCluster, manager *suite.ManagerClient, nodeID uint64, timeout time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var last suite.NodeDTO
	for {
		nodes, err := manager.ListNodes(ctx)
		if err == nil {
			for _, node := range nodes.Items {
				if node.NodeID == nodeID {
					last = node
					if node.Health.Fresh && node.Health.Freshness == "fresh" && node.Health.Status == "alive" && node.Health.RuntimeReady {
						return
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("node %d health did not become fresh: last=%#v\n%s", nodeID, last, cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}
```

- [x] **Step 3: Add Slot and scale-in helper snippets**

Append helpers adapted from `dynamic_node_join/scale_in_slot_drain_test.go`:

```go
func eventuallySlotsContainDesiredPeer(t testing.TB, cluster *suite.StartedCluster, managerNodeID uint64, nodeID uint64, timeout time.Duration) []suite.SlotDTO {
	t.Helper()
	managerNode := cluster.MustNode(managerNodeID)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var lastSlots []suite.SlotDTO
	for {
		var resp struct {
			Total int             `json:"total"`
			Items []suite.SlotDTO `json:"items"`
		}
		_, err := suite.GetJSON(ctx, "http://"+managerNode.Spec.ManagerAddr+"/manager/slots", &resp)
		if err == nil {
			lastSlots = append([]suite.SlotDTO(nil), resp.Items...)
			if resp.Total == len(resp.Items) && slotsContainDesiredPeer(resp.Items, nodeID) {
				return resp.Items
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("manager Slot inventory did not include node %d: last=%#v\n%s", nodeID, lastSlots, cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func slotsContainDesiredPeer(slots []suite.SlotDTO, nodeID uint64) bool {
	for _, slot := range slots {
		if slices.Contains(slot.Assignment.DesiredPeers, nodeID) {
			return true
		}
	}
	return false
}

func eventuallyScaleInSlotsDrained(t testing.TB, cluster *suite.StartedCluster, manager *suite.ManagerClient, nodeID uint64, timeout time.Duration) suite.NodeScaleInStatusDTO {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var last suite.NodeScaleInStatusDTO
	var lastErr error
	for {
		status, err := manager.NodeScaleInStatus(ctx, nodeID)
		if err == nil {
			last = status
			if !status.BlockedBySlots && status.SlotReplicaCount == 0 {
				return status
			}
			lastErr = fmt.Errorf("blocked_by_slots=%t slot_replica_count=%d", status.BlockedBySlots, status.SlotReplicaCount)
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			t.Fatalf("node %d scale-in slots did not drain: last=%#v lastErr=%v\n%s", nodeID, last, lastErr, cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}
```

- [x] **Step 4: Compile helper package**

Run:

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_readiness -run TestDoesNotExist -count=1
```

Expected: package compiles or reports no tests to run.

- [x] **Step 5: Commit**

```bash
git add test/e2ev2/suite/manager_client.go test/e2ev2/cluster/dynamic_node_readiness/lifecycle_helpers_test.go
git commit -m "test: add dynamic node readiness lifecycle helpers"
```

---

### Task 4: Add Primary Production Readiness Test

**Files:**
- Create: `test/e2ev2/cluster/dynamic_node_readiness/production_readiness_test.go`

- [x] **Step 1: Write the test**

Create `production_readiness_test.go`:

```go
//go:build e2e

package dynamic_node_readiness

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2ev2/suite"
	"github.com/stretchr/testify/require"
)

func TestDynamicNodeLifecycleWithContinuousTraffic(t *testing.T) {
	s := suite.New(t)
	const joinToken = "e2ev2-stage9-readiness-token"
	overrides := readinessOverrides()
	cluster := s.StartThreeNodeCluster(
		suite.WithManagerHTTP(),
		suite.WithDynamicJoinToken(joinToken),
		suite.WithNodeConfigOverrides(1, overrides),
		suite.WithNodeConfigOverrides(2, overrides),
		suite.WithNodeConfigOverrides(3, overrides),
		suite.WithNodeConfigOverrides(4, overrides),
	)

	readyCtx, cancelReady := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelReady()
	require.NoError(t, cluster.WaitClusterReady(readyCtx), cluster.DumpDiagnostics())

	manager := cluster.ManagerClient(t, 1)
	traffic := startTrafficWorker(t, cluster, cluster.MustNode(1), "stage9-readiness")
	defer stopTrafficWorker(t, traffic)

	node4 := cluster.StartSeedJoinNode(t, suite.SeedJoinNodeConfig{
		NodeID:    4,
		Seeds:     cluster.SeedAddrs(),
		JoinToken: joinToken,
	})
	require.NotNil(t, node4)
	manager.EventuallyNodeJoinState(t, 4, "joining", 30*time.Second)
	manager.EventuallyNodeReadiness(t, 4, true, 30*time.Second)
	eventuallyNodeHealthFresh(t, cluster, manager, 4, 30*time.Second)
	requireTrafficProgress(t, cluster, traffic, 3, 10*time.Second)

	manager.MustActivateNode(t, 4)
	manager.EventuallyNodeJoinState(t, 4, "active", 30*time.Second)
	eventuallyNodeHealthFresh(t, cluster, manager, 4, 30*time.Second)
	requireTrafficProgress(t, cluster, traffic, 3, 10*time.Second)

	onboardingPlan := manager.MustPlanOnboarding(t, 4, 1)
	require.Len(t, onboardingPlan.Candidates, 1, cluster.DumpDiagnostics())
	onboardingStart := manager.MustStartOnboarding(t, 4, 1)
	require.Equal(t, uint32(1), onboardingStart.Created, cluster.DumpDiagnostics())
	manager.EventuallyOnboardingSafe(t, 4, 60*time.Second)
	require.NotEmpty(t, eventuallySlotsContainDesiredPeer(t, cluster, 1, 4, 60*time.Second))
	requireTrafficProgress(t, cluster, traffic, 3, 10*time.Second)

	start := manager.MustStartScaleIn(t, 4)
	require.Equal(t, "leaving", start.JoinState)
	manager.EventuallyNodeJoinState(t, 4, "leaving", 30*time.Second)
	eventuallySetScaleInDrain(t, cluster, manager, 4, true, 30*time.Second)
	requireTrafficProgress(t, cluster, traffic, 3, 10*time.Second)

	plan := manager.MustPlanScaleIn(t, 4, 1)
	require.NotEmpty(t, plan.Candidates, cluster.DumpDiagnostics())
	advance := manager.MustAdvanceScaleIn(t, 4, 1)
	require.Equal(t, uint32(1), advance.Created, cluster.DumpDiagnostics())
	drained := eventuallyScaleInSlotsDrained(t, cluster, manager, 4, 60*time.Second)
	require.False(t, drained.BlockedByHealth, "health must not block after fresh reports: %#v", drained)
	require.False(t, drained.BlockedByStaleRevision, "revision freshness must not block after reports: %#v", drained)
	requireTrafficProgress(t, cluster, traffic, 3, 10*time.Second)

	safe := manager.EventuallyScaleInSafeToRemove(t, 4, 60*time.Second)
	require.True(t, safe.SafeToRemove, "status=%#v", safe)
	require.True(t, safe.HealthFresh, "status=%#v", safe)
	removed := manager.MustRemoveScaleInNode(t, 4)
	require.Equal(t, "removed", removed.JoinState)
	manager.EventuallyNodeJoinState(t, 4, "removed", 30*time.Second)
	requireTrafficProgress(t, cluster, traffic, 3, 10*time.Second)

	requireMetricsContain(t, cluster.MustNode(1),
		"wukongim_node_lifecycle_nodes",
		"wukongim_node_health_freshness_nodes",
		"wukongim_node_lifecycle_attempts_total",
		"wukongim_discovery_membership_revision",
	)
}
```

If the suite method name for scale-in planning differs, use the existing name from `test/e2ev2/suite/manager_client.go`.

- [x] **Step 2: Run the test and verify the first failure**

Run:

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_readiness -run TestDynamicNodeLifecycleWithContinuousTraffic -count=1 -timeout 12m -p=1
```

Expected before all Stage 9 implementation is complete: failure points to the first missing helper/API/metric. Fix only the root cause shown by the failure, then rerun.

- [x] **Step 3: Keep debugging evidence**

For every failure, capture the concrete reason in the test log or commit message:

```text
root_cause=<missing health freshness in manager DTO | stale health blocker | metric family absent | traffic sendack error | scale-in status unsafe>
evidence=<manager JSON field | metrics family | cluster diagnostics | sendack reason>
fix=<single code path changed>
```

- [x] **Step 4: Commit passing test**

```bash
git add test/e2ev2/cluster/dynamic_node_readiness
git commit -m "test: prove dynamic node lifecycle under continuous traffic"
```

---

### Task 5: Add Optional wkcli sim Runbook

**Files:**
- Create: `docs/superpowers/runbooks/internalv2-dynamic-node-stage9-wkcli-sim-smoke.md`

- [x] **Step 1: Write the runbook**

Create:

```markdown
# internalv2 Dynamic Node Stage 9 wkcli sim Smoke

This optional manual smoke complements
`test/e2ev2/cluster/dynamic_node_readiness`. It uses public v2 bench and
WKProto surfaces through `wkcli sim`; it does not import server internals.

## Command Shape

Start a Stage 9 cluster with manager HTTP, bench API, metrics, gateway
listeners, and:

```text
WK_CLUSTER_NODE_HEALTH_REPORT_INTERVAL=5s
WK_CLUSTER_NODE_HEALTH_REPORT_TTL=30s
```

Run traffic:

```bash
go run ./cmd/wkcli sim --server http://127.0.0.1:5001 --users 200 --groups 40 --group-members 20 --rate 2/s --status-listen 127.0.0.1:19091 --max-runtime 5m
```

While the simulator is running, use manager HTTP to:

1. seed-join node 4;
2. activate node 4;
3. start one Slot onboarding move;
4. mark node 4 leaving;
5. enable gateway drain;
6. advance one Slot scale-in move;
7. remove node 4 after `safe_to_remove=true`.

## Evidence To Keep

- `curl http://127.0.0.1:19091/status`
- manager node list JSON before and after each lifecycle transition
- manager scale-in status JSON before final remove
- `/metrics` snippets for `wukongim_node_lifecycle_nodes`,
  `wukongim_node_health_freshness_nodes`, and
  `wukongim_node_scale_in_blockers_total`
```

- [x] **Step 2: Commit runbook**

```bash
git add docs/superpowers/runbooks/internalv2-dynamic-node-stage9-wkcli-sim-smoke.md
git commit -m "docs: add dynamic node wkcli sim readiness runbook"
```

---

### Task 6: Run Stage Gate And Update Plan

**Files:**
- Modify: `docs/superpowers/plans/2026-06-29-internalv2-dynamic-node-stage9-production-readiness.md`

- [x] **Step 1: Run the Stage 9D gate**

Run:

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_readiness -run TestDynamicNodeLifecycleWithContinuousTraffic -count=1 -timeout 12m -p=1
git diff --check
```

Expected: PASS and no whitespace errors.

- [x] **Step 2: Run the dynamic-node e2ev2 regression subset**

Run:

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_join ./test/e2ev2/cluster/dynamic_node_readiness -count=1 -timeout 15m -p=1
```

Expected: PASS. This proves Stage 9 did not regress existing dynamic join/onboarding/scale-in scenarios.

- [x] **Step 3: Update the Stage 9 master gate**

In `docs/superpowers/plans/2026-06-29-internalv2-dynamic-node-stage9-production-readiness.md`, check Gate 9D only after both commands pass.

- [x] **Step 4: Commit gate update**

```bash
git add docs/superpowers/plans/2026-06-29-internalv2-dynamic-node-stage9-production-readiness.md
git commit -m "docs: record dynamic node readiness smoke gate"
```

---

## Execution Evidence

2026-06-30 implementation notes:

- Root cause: `pkg/slot/multiraft` selected a leader-transfer transferee only
  when a follower had caught up to the leader's local tail. Under continuous
  traffic, the leader tail can keep advancing beyond committed index, so
  Slot replica move `remove_voter` could repeatedly request a no-op transfer
  and remain pending. Fix: allow a committed voter to become the transfer
  target and let Raft finish catch-up during leadership transfer.
- Root cause: manager lifecycle metrics defined
  `wukongim_node_lifecycle_attempts_total`, but no management lifecycle write
  path observed attempts. Prometheus omitted the family until a sample existed.
  Fix: wire a lifecycle attempt observer through management usecases to
  `pkg/metrics`.
- Root cause: several e2ev2 manager helpers used a single long context for
  repeated public HTTP calls, and the final remove helper used one 5s POST.
  Under real control-plane writes and continuous traffic, this produced false
  timeouts. Fix: use bounded per-request contexts and idempotent state polling.
- Root cause: scale-in status also treated eligible replacement nodes whose
  low-frequency health report had not yet observed the newest control revision
  as unsafe, even when the live runtime summary had already observed it. Fix:
  leave health reports as freshness/readiness evidence and keep revision
  safety on the live runtime-summary gate.
- Root cause: control snapshot watchers used a non-blocking publish that could
  permanently drop the newest state event when a subscriber channel was full.
  Scale-in polling could then stay behind the durable ControllerV2 revision.
  Fix: make the upstream ControllerV2 runtime watcher replace stale buffered
  events with the newest locally visible state, and make clusterv2 control
  watcher publication reject older revisions after a newer revision has been
  published.
- Root cause: existing dynamic-node join tests started onboarding immediately
  after activation, before Stage 9 fresh health made the target schedulable.
  Fix: add public manager polling for health-schedulable nodes before
  onboarding and scale-in advancement.
- Root cause: concurrent scale-in advancement can race on ControllerV2 task
  phase progress and return `task_phase_mismatch`, which is an expected stale
  intent conflict rather than a server fault. Fix: classify it as retryable and
  map it to scale-in conflict.
- Root cause: lifecycle attempt metrics classified target-state-achieved
  activation no-ops as `noop`, so an idempotent successful operator activation
  could fail the public `activate/ok` metrics assertion. Fix: count lifecycle
  attempts as `ok` when the requested target state is reached.

Verified:

```bash
GOWORK=off go test ./pkg/metrics -count=1
GOWORK=off go test ./pkg/controllerv2 -run 'TestRuntimePublishStateCoalescesLatestEventWhenWatchFull|TestRuntimeRequestSlotReplicaMoveCreatesTaskWithoutChangingDesiredPeers|TestRuntimeMarkNodeRemovedTurnsLeavingNodeRemoved' -count=1
GOWORK=off go test ./pkg/clusterv2/control -count=1
GOWORK=off go test ./pkg/clusterv2 -count=1
GOWORK=off go test ./internalv2/usecase/management -count=1
GOWORK=off go test ./internalv2/app -run 'Test(ControlSnapshotMetricsObserverMapsNodeLifecycleHealth|NodeLifecycleMetricsObserverCountsScaleInBlockers|NodeLifecycleMetricsObserverDeduplicatesScaleInBlockersPerRevision)' -count=1
GOWORK=off go test ./pkg/slot/multiraft -count=1
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_join -run 'TestConcurrentScaleInAdvanceCreatesAtMostOneSlotTask|TestConcurrentOnboardingStartCreatesAtMostOneTask|TestScaleInSlotDrainMovesReplicasBeforeRemove|TestNodeOnboardingMovesSlotReplicaToActiveNode' -count=1 -timeout 8m -p=1
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_readiness -run TestDynamicNodeLifecycleWithContinuousTraffic -count=1 -timeout 12m -p=1
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_join ./test/e2ev2/cluster/dynamic_node_readiness -count=1 -timeout 15m -p=1
git diff --check
```

Observed pass for the real-traffic smoke:

```text
ok  	github.com/WuKongIM/WuKongIM/test/e2ev2/cluster/dynamic_node_readiness	241.625s
```

Observed pass for the serial dynamic-node regression subset:

```text
ok  	github.com/WuKongIM/WuKongIM/test/e2ev2/cluster/dynamic_node_join	164.029s
ok  	github.com/WuKongIM/WuKongIM/test/e2ev2/cluster/dynamic_node_readiness	208.090s
```
