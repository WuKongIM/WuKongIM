# Raftcluster Test Speedup Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Cut `go test ./pkg/cluster/raftcluster` wall-clock time by at least 50% without reducing the behavioral coverage of the default integration suite.

**Architecture:** Keep the existing black-box integration coverage, but introduce a dedicated test-only timing profile, cheaper readiness/leader-stability probes, and cheaper teardown behavior inside the test harness. Do not change production cluster semantics; all speedups live in test helpers or in test-only code paths used by the harness.

**Tech Stack:** Go, `testing`, `testify/require`, real `raftcluster`, real `metadb`, real `raftstorage` (Pebble-backed), real TCP listeners.

---

## File Map

**Primary files to modify**
- Modify: `pkg/cluster/raftcluster/cluster_test.go`
  - Add a dedicated test timing profile for `newStartedTestNode()`.
  - Make cluster startup helpers reuse the fast profile.
  - Replace the expensive leader-stability heuristic with a faster test-specific probe.
  - Remove or shrink unnecessary fixed teardown sleep in `stopNodes()`.
- Modify: `pkg/cluster/raftcluster/cluster_internal_test.go`
  - Align any duplicated polling helpers/constants with the new fast test profile if they currently assume the old 100ms/10-tick defaults.
- Modify: `pkg/cluster/raftcluster/stress_test.go` only if a helper signature change in `cluster_test.go` requires a call-site update.

**Optional follow-up files**
- Create: `pkg/cluster/raftcluster/test_timing_test.go`
  - Small focused tests for the fast timing profile and helper invariants if keeping them separate is clearer than embedding them in `cluster_test.go`.

**Verification targets**
- Test: `go test ./pkg/cluster/raftcluster -run '^(TestClusterBootstrapsManagedGroupsFromControllerAssignments|TestClusterMarkNodeDrainingMovesAssignmentsAway|TestClusterForceReconcileRetriesFailedRepair|TestClusterControllerLeaderFailoverResumesInFlightRepair)$' -count=1`
- Test: `go test ./pkg/cluster/raftcluster -count=1`
- Test: `go test ./pkg/cluster/raftcluster -count=3`

---

### Task 1: Introduce a dedicated fast test timing profile

**Files:**
- Modify: `pkg/cluster/raftcluster/cluster_test.go:61`
- Test: `go test ./pkg/cluster/raftcluster -run '^TestClusterBootstrapsManagedGroupsFromControllerAssignments$' -count=1 -v`

- [ ] **Step 1: Write the failing test**

Add a focused test that constructs a harness node and asserts the test helper injects explicit non-default timing values instead of relying on production defaults. Prefer a small helper-level assertion over a full integration scenario.

```go
func TestNewStartedTestNodeUsesFastTestClusterTiming(t *testing.T) {
    node := startSingleNodeWithController(t, 1, 1)
    require.Equal(t, 20*time.Millisecond, node.cluster.Config().TickInterval())
}
```

If `Cluster` does not expose config directly, assert through a small extracted helper that returns the `raftcluster.Config` used by `newStartedTestNode()`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/cluster/raftcluster -run '^TestNewStartedTestNodeUsesFastTestClusterTiming$' -count=1`
Expected: FAIL because the harness currently uses production defaults from `pkg/cluster/raftcluster/config.go`.

- [ ] **Step 3: Write minimal implementation**

Inside `newStartedTestNode()` in `pkg/cluster/raftcluster/cluster_test.go`, add explicit test-only values to the `raftcluster.Config` literal instead of inheriting defaults:

```go
TickInterval:   25 * time.Millisecond,
ElectionTick:   6,
HeartbeatTick:  1,
DialTimeout:    750 * time.Millisecond,
ForwardTimeout: 750 * time.Millisecond,
PoolSize:       1,
```

Guidelines:
- Keep values conservative enough to avoid introducing election churn.
- Do not change `pkg/cluster/raftcluster/config.go`; production defaults stay untouched.
- Put the values behind named test constants near the top of `cluster_test.go` so later tasks can reuse them.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/cluster/raftcluster -run '^TestNewStartedTestNodeUsesFastTestClusterTiming$' -count=1`
Expected: PASS.

- [ ] **Step 5: Verify a representative integration scenario still passes**

Run: `go test ./pkg/cluster/raftcluster -run '^TestClusterBootstrapsManagedGroupsFromControllerAssignments$' -count=1 -v`
Expected: PASS with visibly lower startup/settle time than before.

- [ ] **Step 6: Commit**

```bash
git add pkg/cluster/raftcluster/cluster_test.go
git commit -m "test(raftcluster): speed up cluster harness timing"
```

---

### Task 2: Replace the expensive leader-stability heuristic with a fast test probe

**Files:**
- Modify: `pkg/cluster/raftcluster/cluster_test.go:728`
- Test: `go test ./pkg/cluster/raftcluster -run '^(TestClusterBootstrapsManagedGroupsFromControllerAssignments|TestClusterContinuesServingWithOneReplicaNodeDown|TestClusterTransferGroupLeaderDelegatesToManagedGroup)$' -count=1`

- [ ] **Step 1: Write the failing test**

Add a small helper test that asserts the leader-stability probe can declare success well under the current ~2 second confirmation window when all nodes already agree.

```go
func TestStableLeaderWithinUsesShortConfirmationWindow(t *testing.T) {
    nodes := startThreeNodes(t, 1)
    start := time.Now()
    _ = waitForStableLeader(t, nodes, 1)
    require.Less(t, time.Since(start), time.Second)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/cluster/raftcluster -run '^TestStableLeaderWithinUsesShortConfirmationWindow$' -count=1`
Expected: FAIL because `stableLeaderWithin()` currently requires 10 confirmations with 200ms sleep (`pkg/cluster/raftcluster/cluster_test.go:728`).

- [ ] **Step 3: Write minimal implementation**

Refactor `stableLeaderWithin()` into a test-tuned probe using constants, for example:

```go
const (
    testLeaderPollInterval   = 50 * time.Millisecond
    testLeaderConfirmations  = 4
)
```

Update the loop to:
- poll every `50ms` instead of `200ms`
- require only `4` consecutive matching leader observations
- keep the same public helper signature so existing call-sites do not change

Do **not** weaken correctness by removing the consecutive confirmation requirement entirely.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/cluster/raftcluster -run '^TestStableLeaderWithinUsesShortConfirmationWindow$' -count=1`
Expected: PASS.

- [ ] **Step 5: Run affected integration coverage**

Run: `go test ./pkg/cluster/raftcluster -run '^(TestClusterBootstrapsManagedGroupsFromControllerAssignments|TestClusterContinuesServingWithOneReplicaNodeDown|TestClusterTransferGroupLeaderDelegatesToManagedGroup)$' -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/cluster/raftcluster/cluster_test.go
git commit -m "test(raftcluster): shorten leader stability probe"
```

---

### Task 3: Make managed-group settle checks cheaper without losing coverage

**Files:**
- Modify: `pkg/cluster/raftcluster/cluster_test.go:433`
- Test: `go test ./pkg/cluster/raftcluster -run '^(TestClusterListGroupAssignmentsReflectsControllerState|TestClusterMarkNodeDrainingMovesAssignmentsAway|TestClusterRebalancesAfterRecoveredNodeReturns)$' -count=1 -v`

- [ ] **Step 1: Write the failing test**

Add a targeted helper test asserting `waitForManagedGroupsSettled()` does not serialize a multi-second leader wait per group once assignments are already present.

```go
func TestWaitForManagedGroupsSettledReturnsQuicklyAfterAssignmentsExist(t *testing.T) {
    nodes := startThreeNodesWithControllerWithSettle(t, 4, 3, false)
    start := time.Now()
    waitForManagedGroupsSettled(t, nodes, 4)
    require.Less(t, time.Since(start), 2*time.Second)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/cluster/raftcluster -run '^TestWaitForManagedGroupsSettledReturnsQuicklyAfterAssignmentsExist$' -count=1`
Expected: FAIL or run noticeably above the threshold because `waitForManagedGroupsSettled()` currently nests `stableLeaderWithin(..., 2*time.Second)` inside an outer `Eventually()`.

- [ ] **Step 3: Write minimal implementation**

Refactor `waitForManagedGroupsSettled()` to avoid repeated nested timeouts:
- Keep `waitForControllerAssignments()` as the first gate.
- Load assignments once per poll.
- For each group, call the now-fast `stableLeaderWithin()` with a short timeout derived from the fast test profile.
- Prefer a bounded inner timeout like `300ms`-`500ms`, not `2s`.
- Keep the reconcile-task-not-found check so the test still proves the controller finished reconciling.

If needed, split the helper into two focused helpers:
- `waitForAssignmentsVisible()`
- `waitForAssignmentsQuiesced()`

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/cluster/raftcluster -run '^TestWaitForManagedGroupsSettledReturnsQuicklyAfterAssignmentsExist$' -count=1`
Expected: PASS.

- [ ] **Step 5: Run representative settle-heavy coverage**

Run: `go test ./pkg/cluster/raftcluster -run '^(TestClusterListGroupAssignmentsReflectsControllerState|TestClusterMarkNodeDrainingMovesAssignmentsAway|TestClusterRebalancesAfterRecoveredNodeReturns)$' -count=1 -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/cluster/raftcluster/cluster_test.go
git commit -m "test(raftcluster): reduce managed-group settle latency"
```

---

### Task 4: Remove the unconditional 3-second teardown tax

**Files:**
- Modify: `pkg/cluster/raftcluster/cluster_test.go:402`
- Test: `go test ./pkg/cluster/raftcluster -run '^(TestTestNodeRestartReopensClusterWithSameListenAddr|TestThreeNodeClusterReelectsAfterLeaderRestart)$' -count=3`

- [ ] **Step 1: Write the failing test**

Add a helper-level test that stops a node set and asserts teardown does not impose an unnecessary multi-second sleep after the databases have already closed.

```go
func TestStopNodesReturnsWithoutFixedThreeSecondDelay(t *testing.T) {
    nodes := startThreeNodes(t, 1)
    start := time.Now()
    stopNodes(nodes)
    require.Less(t, time.Since(start), time.Second)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/cluster/raftcluster -run '^TestStopNodesReturnsWithoutFixedThreeSecondDelay$' -count=1`
Expected: FAIL because `stopNodes()` currently always sleeps for 3 seconds after any stop.

- [ ] **Step 3: Write minimal implementation**

Replace the unconditional sleep with one of these bounded strategies:
- preferred: a short polling loop (`<=250ms` total) that checks whether reopened Pebble paths still error due to a lock, then exits early when clear
- fallback: a much smaller fixed grace window (`100ms`-`250ms`) justified by fresh evidence from repeated restart tests

Do not remove the grace window blindly; the existing comment indicates prior teardown races with Pebble finalization.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/cluster/raftcluster -run '^TestStopNodesReturnsWithoutFixedThreeSecondDelay$' -count=1`
Expected: PASS.

- [ ] **Step 5: Run restart-focused regression coverage**

Run: `go test ./pkg/cluster/raftcluster -run '^(TestTestNodeRestartReopensClusterWithSameListenAddr|TestThreeNodeClusterReelectsAfterLeaderRestart)$' -count=3`
Expected: PASS across repeated runs.

- [ ] **Step 6: Commit**

```bash
git add pkg/cluster/raftcluster/cluster_test.go
git commit -m "test(raftcluster): remove fixed teardown sleep"
```

---

### Task 5: Re-baseline the package and decide whether slow-path segregation is still needed

**Files:**
- Modify: `pkg/cluster/raftcluster/cluster_test.go` only if one or two cases still need explicit longer waits
- Optional Modify: `pkg/cluster/raftcluster/stress_test.go`
- Optional Create: `docs/superpowers/plans/2026-04-09-raftcluster-test-speedup-results.md`

- [ ] **Step 1: Run the full package once serially to get a clean baseline**

Run: `go test ./pkg/cluster/raftcluster -count=1`
Expected: PASS, with wall-clock materially lower than the pre-change baseline.

- [ ] **Step 2: Run the full package repeatedly to check flake rate**

Run: `go test ./pkg/cluster/raftcluster -count=3`
Expected: PASS all three runs.

- [ ] **Step 3: If any single test still dominates, isolate it with timing data**

Run one-at-a-time on the remaining slowest candidates, likely:

```bash
go test ./pkg/cluster/raftcluster -run '^(TestClusterForceReconcileRetriesFailedRepair|TestClusterSurfacesFailedRepairAfterRetryExhaustion|TestClusterControllerLeaderFailoverResumesInFlightRepair)$' -count=1 -v
```

Document which one remains slow and why.

- [ ] **Step 4: Only if needed, segregate pathological long-haul cases**

If the package is still too slow after Tasks 1-4, move the rare long-haul scenarios behind a dedicated opt-in gate while keeping the main integration suite default-on. Prefer a `testing.Short()` or explicit env gate for only the truly pathological cases, not the whole suite.

- [ ] **Step 5: Commit**

```bash
git add pkg/cluster/raftcluster/cluster_test.go pkg/cluster/raftcluster/stress_test.go
git commit -m "test(raftcluster): rebaseline package runtime"
```

---

## Notes for the Implementer

- The biggest known fixed costs are currently:
  - production-like timing defaults inherited by the test harness from `pkg/cluster/raftcluster/config.go`
  - expensive nested settle checks in `waitForManagedGroupsSettled()` (`pkg/cluster/raftcluster/cluster_test.go:433`)
  - `stableLeaderWithin()` requiring 10 confirmations with 200ms sleep (`pkg/cluster/raftcluster/cluster_test.go:728`)
  - unconditional `time.Sleep(3 * time.Second)` in `stopNodes()` (`pkg/cluster/raftcluster/cluster_test.go:402`)
- Do not change `pkg/cluster/raftcluster/config.go` defaults. Production timings are not the optimization target.
- Keep `stress_test.go` opt-in. It is already correctly gated by `WKCLUSTER_STRESS`.
- Prefer one tuning knob change per commit so any flake introduced by faster timing is easy to bisect.
- If repeated `-count=3` runs show new flakes, back off the timing profile slightly before touching more code.

## Expected Outcome

After Tasks 1-4, `go test ./pkg/cluster/raftcluster -count=1` should drop from the current multi-minute range to roughly 1-2 minutes on the same machine, while preserving the default black-box coverage.
