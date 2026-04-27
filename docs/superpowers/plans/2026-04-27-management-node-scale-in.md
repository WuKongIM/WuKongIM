# Management Node Scale-in Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build V1 manager-driven node scale-in so operators can safely drain one non-controller WuKongIM node to `ready_to_remove` before manually scaling Kubernetes down.

**Architecture:** The manager usecase computes a live scale-in report from strict cluster reads, configured replica count, and target-node runtime summaries. `start` marks a node Draining, `advance` performs bounded Slot leader transfer, `cancel` resumes the node, and `/readyz` becomes Draining-aware through cached local node status. Gateway/runtime work adds target-node connection/session summaries and an admission drain gate without adding a durable controller job in V1.

**Tech Stack:** Go, Gin manager HTTP APIs, WuKongIM `pkg/cluster` controller RPC/read APIs, `internal/usecase/management`, `internal/access/node` RPC, `internal/gateway`, `internal/runtime/online`, Prometheus-compatible observability paths.

---

## Source Documents

- Spec: `docs/superpowers/specs/2026-04-27-management-node-scale-in-design.md`
- Package flow docs to keep consistent when touched:
  - `pkg/cluster/FLOW.md`
  - `pkg/controller/FLOW.md` only if durable controller/job semantics are added, which V1 should avoid

## File Structure

Create or modify these files:

- `pkg/cluster/api.go` — add strict active-migration read to `API`.
- `pkg/cluster/operator.go` or new `pkg/cluster/scalein_safety.go` — implement strict active-migration read using leader-backed assignment/hash-slot table refresh.
- `pkg/cluster/errors.go` — add an observation-not-ready error if strict runtime view warmup needs a distinct error.
- `pkg/cluster/cluster.go` — make strict runtime view reads fail when the local Controller leader is not warm, if needed.
- `pkg/cluster/controller_handler.go` — make remote `list_runtime_views` fail during Controller leader warmup, if needed.
- `pkg/cluster/cluster_test.go` — tests for strict active migrations and runtime-view readiness behavior.
- `pkg/cluster/FLOW.md` — update if new public cluster API/readiness semantics are added.
- `internal/usecase/management/app.go` — add `SlotReplicaN`, `RuntimeSummaryReader`, and scale-in method contracts.
- `internal/usecase/management/node_scalein.go` — scale-in report, preflight, status, start/cancel/advance usecase.
- `internal/usecase/management/node_scalein_test.go` — usecase unit tests.
- `internal/access/manager/server.go` — extend `Management` interface with scale-in methods.
- `internal/access/manager/routes.go` — add scale-in routes and permissions.
- `internal/access/manager/node_scalein.go` — manager handlers and DTO mapping.
- `internal/access/manager/server_test.go` — manager route tests.
- `internal/app/node_drain_state.go` — cached local drain-state provider.
- `internal/app/app.go` — retain the online registry and local drain-state fields needed by runtime summary and readiness.
- `internal/app/runtime_summary.go` — app-level runtime summary collector plus separate management and node-RPC adapters.
- `internal/app/observability.go` — add `node_not_draining` readiness check.
- `internal/app/build.go` — wire `SlotReplicaN`, runtime summary reader, and node status hook updates.
- `internal/runtime/online/types.go` — add online summary type/method to `Registry`.
- `internal/runtime/online/registry.go` — implement active/closing online summary.
- `internal/runtime/online/registry_test.go` — online summary tests.
- `internal/gateway/types/options.go` — add drain/admission gate option if gate is configured via options.
- `internal/gateway/core/server.go` — add accepting/drain gate and gateway session summary.
- `internal/gateway/gateway.go` — expose accepting state and session summary through `Gateway`.
- `internal/gateway/core/server_test.go` or existing gateway tests — admission/session summary tests.
- `internal/access/node/options.go` — add runtime summary dependency and register node runtime summary RPC.
- `internal/access/node/service_ids.go` — reserve the runtime summary RPC service ID.
- `internal/access/node/runtime_summary_rpc.go` — node RPC handler and request/response DTO.
- `internal/access/node/client.go` — client method for runtime summary RPC.
- `internal/access/node/runtime_summary_rpc_test.go` — node RPC tests.
- `docs/wiki/` or `docs/development/` — V1 manual runbook if requested during implementation.
- `wukongim.conf.example` — only if implementation adds new config keys. Prefer no new config in V1.

## Implementation Notes

- Do not call `RemoveSlot` in the scale-in path.
- Do not introduce durable `NodeDecommissionJob` in V1.
- Do not report `safe_to_remove=true` if strict cluster reads, strict active migrations, runtime views, or runtime summaries are unavailable.
- Do not treat `force_close_connections` as a safety override. It may trigger a close attempt only after gateway core lifecycle support exists.
- Management usecases must depend on interfaces, not on `internal/access/node` concrete adapters.
- Check `FLOW.md` before editing packages that contain one; update it if public flow semantics change.

---

### Task 1: Strict Cluster Safety Reads

**Files:**
- Modify: `pkg/cluster/api.go`
- Create: `pkg/cluster/scalein_safety.go` or modify `pkg/cluster/operator.go`
- Modify: `pkg/cluster/cluster.go`
- Modify: `pkg/cluster/controller_handler.go`
- Modify: `pkg/cluster/errors.go`
- Test: `pkg/cluster/cluster_test.go`
- Maybe modify: `pkg/cluster/FLOW.md`

- [ ] **Step 1: Read cluster flow and current strict read tests**

Run:

```bash
sed -n '1,120p' pkg/cluster/FLOW.md
rg -n "List.*Strict|warmupComplete|GetMigrationStatus" pkg/cluster/*.go pkg/cluster/*_test.go
```

Expected: confirm existing strict read behavior and test naming before editing.

- [ ] **Step 2: Write failing test for strict active migration read**

Add a test in `pkg/cluster/cluster_test.go` named similar to:

```go
func TestListActiveMigrationsStrictRefreshesLeaderHashSlotTable(t *testing.T) {
    // Arrange a cluster with controllerClient.RefreshAssignments returning
    // a HashSlotTable containing one active migration.
    // Act: call ListActiveMigrationsStrict.
    // Assert: returned migrations match the leader-backed table.
}
```

If the existing test helpers make a full table setup too expensive, write the smaller package-level test around a helper that calls `ListSlotAssignmentsStrict` and reads the refreshed `HashSlotTable`.

- [ ] **Step 3: Run the failing test**

Run:

```bash
go test ./pkg/cluster -run TestListActiveMigrationsStrictRefreshesLeaderHashSlotTable -count=1
```

Expected: FAIL because `ListActiveMigrationsStrict` does not exist.

- [ ] **Step 4: Add strict migration API**

In `pkg/cluster/api.go`, add:

```go
// ListActiveMigrationsStrict returns the controller leader's active hash-slot migrations without local fallback.
ListActiveMigrationsStrict(ctx context.Context) ([]HashSlotMigration, error)
```

Implement in `pkg/cluster/scalein_safety.go`:

```go
func (c *Cluster) ListActiveMigrationsStrict(ctx context.Context) ([]HashSlotMigration, error) {
    if c == nil {
        return nil, ErrNotStarted
    }
    if _, err := c.ListSlotAssignmentsStrict(ctx); err != nil {
        return nil, err
    }
    table := c.GetHashSlotTable()
    if table == nil {
        return nil, ErrNotStarted
    }
    return table.ActiveMigrations(), nil
}
```

This intentionally reuses `ListSlotAssignmentsStrict`, because leader assignment responses already carry the HashSlotTable and local leader strict assignment reads sync the router table from controller meta.

- [ ] **Step 5: Run strict migration test**

Run:

```bash
go test ./pkg/cluster -run TestListActiveMigrationsStrictRefreshesLeaderHashSlotTable -count=1
```

Expected: PASS.

- [ ] **Step 6: Write failing test for runtime view warmup fail-closed behavior**

Add a test in `pkg/cluster/cluster_test.go` for current Controller leader runtime views when warmup is incomplete. Name it:

```go
func TestListObservedRuntimeViewsStrictFailsWhenLeaderObservationWarmupIncomplete(t *testing.T) {
    // Arrange local controller leader with controllerHost warmup not complete.
    // Act: call ListObservedRuntimeViewsStrict.
    // Assert: returns ErrObservationNotReady or ErrNotStarted-style fail-closed error.
}
```

- [ ] **Step 7: Run the failing runtime warmup test**

Run:

```bash
go test ./pkg/cluster -run TestListObservedRuntimeViewsStrictFailsWhenLeaderObservationWarmupIncomplete -count=1
```

Expected: FAIL because strict runtime views currently return local observations even during warmup.

- [ ] **Step 8: Add observation-not-ready error and fail-closed checks**

In `pkg/cluster/errors.go`, add:

```go
ErrObservationNotReady = errors.New("raftcluster: observation not ready")
```

In `pkg/cluster/cluster.go`, update local strict runtime views:

```go
func (c *Cluster) ListObservedRuntimeViewsStrict(ctx context.Context) ([]controllermeta.SlotRuntimeView, error) {
    if c != nil && c.controllerHost != nil && c.controllerHost.IsLeader(c.cfg.NodeID) {
        if !c.controllerHost.warmupComplete() {
            return nil, ErrObservationNotReady
        }
        return c.controllerHost.snapshotObservations().RuntimeViews, nil
    }
    // existing remote path...
}
```

In `pkg/cluster/controller_handler.go`, before serving `controllerRPCListRuntimeViews`, return `ErrObservationNotReady` if `controllerHost.warmupComplete()` is false.

- [ ] **Step 9: Run cluster strict read tests**

Run:

```bash
go test ./pkg/cluster -run 'TestList(ActiveMigrationsStrict|ObservedRuntimeViewsStrict|NodesStrict|TasksStrict|SlotAssignmentsStrict)' -count=1
```

Expected: PASS. If existing tests expected old behavior, update tests to the new fail-closed strict semantics.

- [ ] **Step 10: Update cluster flow doc if needed**

If `pkg/cluster/FLOW.md` mentions strict runtime views or manager/operator reads, add one sentence that strict runtime view reads fail while Controller leader observation warmup is incomplete.

- [ ] **Step 11: Commit Task 1**

Run:

```bash
git add pkg/cluster/api.go pkg/cluster/scalein_safety.go pkg/cluster/cluster.go pkg/cluster/controller_handler.go pkg/cluster/errors.go pkg/cluster/cluster_test.go pkg/cluster/FLOW.md
git commit -m "feat: add strict scale-in safety reads"
```

If `scalein_safety.go` or `FLOW.md` was not changed, omit it from `git add`.

---

### Task 2: Management Options and Runtime Summary Interface

**Files:**
- Modify: `internal/usecase/management/app.go`
- Modify: `internal/app/build.go`
- Create: `internal/usecase/management/node_scalein.go`
- Test: `internal/usecase/management/node_scalein_test.go`

- [ ] **Step 1: Write failing compile-oriented test for SlotReplicaN validation**

Create or update `internal/usecase/management/node_scalein_test.go` with:

```go
func TestPlanNodeScaleInBlocksWhenSlotReplicaNMissing(t *testing.T) {
    app := New(Options{Cluster: &fakeScaleInCluster{}})

    report, err := app.PlanNodeScaleIn(context.Background(), 1, NodeScaleInPlanRequest{})

    require.NoError(t, err)
    require.False(t, report.Checks.SlotReplicaCountKnown)
    require.Contains(t, scaleInReasonCodes(report.BlockedReasons), "slot_replica_count_unknown")
}
```

- [ ] **Step 2: Run test to verify compile failure**

Run:

```bash
go test ./internal/usecase/management -run TestPlanNodeScaleInBlocksWhenSlotReplicaNMissing -count=1
```

Expected: FAIL to compile because scale-in types/methods do not exist.

- [ ] **Step 3: Add runtime summary and options types**

In `internal/usecase/management/app.go`, add:

```go
// RuntimeSummaryReader reads local or remote node runtime state needed by scale-in safety checks.
type RuntimeSummaryReader interface {
    NodeRuntimeSummary(ctx context.Context, nodeID uint64) (NodeRuntimeSummary, error)
}

// NodeRuntimeSummary contains target-node connection and gateway admission counters.
type NodeRuntimeSummary struct {
    NodeID               uint64
    ActiveOnline         int
    ClosingOnline        int
    TotalOnline          int
    GatewaySessions      int
    SessionsByListener   map[string]int
    AcceptingNewSessions bool
    Draining             bool
    Unknown              bool
}
```

Extend `Options`:

```go
SlotReplicaN              int
ScaleInRuntimeViewMaxAge  time.Duration
RuntimeSummary            RuntimeSummaryReader
```

Extend `App` with matching fields and assign them in `New`. If `ScaleInRuntimeViewMaxAge <= 0`, use a package default such as `30 * time.Second`. This is a code default for V1; do not add a config key unless implementation finds an existing suitable knob.

- [ ] **Step 4: Wire SlotReplicaN in app build**

In `internal/app/build.go`, where `managementusecase.New` is called, pass:

```go
SlotReplicaN: cfg.Cluster.SlotReplicaN,
```

Leave `RuntimeSummary` nil until the runtime summary implementation task. Nil must mean unknown connection safety, not safe.

- [ ] **Step 5: Add minimal scale-in type stubs**

Create `internal/usecase/management/node_scalein.go` with status/report/check/reason types and a stub `PlanNodeScaleIn` sufficient for the missing replica-count test:

```go
type NodeScaleInStatus string

const (
    NodeScaleInStatusBlocked NodeScaleInStatus = "blocked"
)

type NodeScaleInReport struct {
    NodeID                   uint64
    Status                   NodeScaleInStatus
    SafeToRemove             bool
    ConnectionSafetyVerified bool
    Checks                   NodeScaleInChecks
    Progress                 NodeScaleInProgress
    BlockedReasons           []NodeScaleInBlockedReason
}

type NodeScaleInChecks struct {
    SlotReplicaCountKnown bool
}

type NodeScaleInBlockedReason struct {
    Code    string
    Message string
    Count   int
    SlotID  uint32
    NodeID  uint64
}

type NodeScaleInPlanRequest struct {
    ConfirmStatefulSetTail bool
    ExpectedTailNodeID     uint64
}

func (a *App) PlanNodeScaleIn(ctx context.Context, nodeID uint64, req NodeScaleInPlanRequest) (NodeScaleInReport, error) {
    report := NodeScaleInReport{NodeID: nodeID, Status: NodeScaleInStatusBlocked}
    if a == nil || a.slotReplicaN <= 0 {
        report.BlockedReasons = append(report.BlockedReasons, NodeScaleInBlockedReason{Code: "slot_replica_count_unknown", Message: "slot replica count is not configured"})
        return report, nil
    }
    report.Checks.SlotReplicaCountKnown = true
    return report, nil
}
```

Use actual field names matching your `App` struct.

- [ ] **Step 6: Run management tests**

Run:

```bash
go test ./internal/usecase/management -run TestPlanNodeScaleInBlocksWhenSlotReplicaNMissing -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit Task 2**

Run:

```bash
git add internal/usecase/management/app.go internal/usecase/management/node_scalein.go internal/usecase/management/node_scalein_test.go internal/app/build.go
git commit -m "feat: add scale-in management options"
```

---

### Task 3: Scale-in Report and Preflight

**Files:**
- Modify: `internal/usecase/management/node_scalein.go`
- Test: `internal/usecase/management/node_scalein_test.go`
- Modify fake cluster test helpers in the same test file or reuse existing fake reader patterns

- [ ] **Step 1: Add failing tests for all blocking preflight checks**

In `internal/usecase/management/node_scalein_test.go`, add table-driven tests for these cases:

```go
func TestPlanNodeScaleInPreflightBlockedReasons(t *testing.T) {
    tests := []struct {
        name      string
        app       *App
        nodeID    uint64
        req       NodeScaleInPlanRequest
        wantCodes []string
    }{
        {name: "missing node", wantCodes: []string{"target_not_found"}},
        {name: "non data node", wantCodes: []string{"target_not_data_node"}},
        {name: "controller voter", wantCodes: []string{"target_is_controller_voter"}},
        {name: "target not active or draining", wantCodes: []string{"target_not_active_or_draining"}},
        {name: "tail mapping unverified", wantCodes: []string{"tail_node_mapping_unverified"}},
        {name: "other draining node", wantCodes: []string{"other_draining_node_exists"}},
        {name: "remaining data nodes insufficient", wantCodes: []string{"remaining_data_nodes_insufficient"}},
        {name: "controller leader unavailable", wantCodes: []string{"controller_leader_unavailable"}},
        {name: "active migration", wantCodes: []string{"active_hashslot_migrations_exist"}},
        {name: "running onboarding", wantCodes: []string{"running_onboarding_exists"}},
        {name: "active target task", wantCodes: []string{"active_reconcile_tasks_involving_target"}},
        {name: "failed task", wantCodes: []string{"failed_reconcile_tasks_exist"}},
        {name: "runtime view incomplete", wantCodes: []string{"runtime_views_incomplete_or_stale"}},
        {name: "runtime view stale", wantCodes: []string{"runtime_views_incomplete_or_stale"}},
        {name: "quorum lost", wantCodes: []string{"slot_quorum_lost"}},
        {name: "unique healthy replica", wantCodes: []string{"target_unique_healthy_replica"}},
    }
    // Build focused fake inputs per case and assert reason codes.
}
```

Add helper:

```go
func scaleInReasonCodes(reasons []NodeScaleInBlockedReason) []string { ... }
```

- [ ] **Step 2: Run preflight tests to verify they fail**

Run:

```bash
go test ./internal/usecase/management -run TestPlanNodeScaleInPreflightBlockedReasons -count=1
```

Expected: FAIL because checks are not implemented.

- [ ] **Step 3: Extend management cluster interface**

In `internal/usecase/management/app.go`, add to `ClusterReader`:

```go
// ListActiveMigrationsStrict returns controller-leader active hash-slot migrations.
ListActiveMigrationsStrict(ctx context.Context) ([]raftcluster.HashSlotMigration, error)
```

This should be satisfied by `raftcluster.API` after Task 1.

- [ ] **Step 4: Implement report input loading**

In `node_scalein.go`, implement a helper:

```go
type nodeScaleInSnapshot struct {
    nodes          []controllermeta.ClusterNode
    assignments    []controllermeta.SlotAssignment
    views          []controllermeta.SlotRuntimeView
    tasks          []controllermeta.ReconcileTask
    migrations     []raftcluster.HashSlotMigration
    onboardingJobs []controllermeta.NodeOnboardingJob
    runtime        NodeRuntimeSummary
}

func (a *App) loadNodeScaleInSnapshot(ctx context.Context, nodeID uint64) (nodeScaleInSnapshot, error) {
    // Strict reads only. Return read errors to caller.
}
```

Load onboarding jobs with `ListNodeOnboardingJobs(ctx, scaleInOnboardingJobScanLimit, "")`. Use a conservative scan limit such as 100; if `hasMore` is true, fail closed with `running_onboarding_exists` or a dedicated read-error path rather than assuming the omitted page is safe. If any strict cluster read fails because the Controller leader is unavailable or observation warmup is incomplete, add `controller_leader_unavailable`/return the read error as appropriate and never mark the report safe. If `RuntimeSummary` is nil, set `runtime.Unknown=true` and do not return an error.

- [ ] **Step 5: Implement core preflight helpers**

Add small helpers in `node_scalein.go`:

```go
func findScaleInNode(nodes []controllermeta.ClusterNode, nodeID uint64) (controllermeta.ClusterNode, bool)
func scaleInNodeIsControllerVoter(a *App, node controllermeta.ClusterNode) bool
func activeAliveDataNodes(nodes []controllermeta.ClusterNode) []controllermeta.ClusterNode
func scaleInNodeActiveOrDraining(node controllermeta.ClusterNode) bool
func scaleInAssignmentContains(assignment controllermeta.SlotAssignment, nodeID uint64) bool
func scaleInTaskInvolves(task controllermeta.ReconcileTask, nodeID uint64) bool
func scaleInTaskActive(task controllermeta.ReconcileTask) bool
func scaleInTaskFailed(task controllermeta.ReconcileTask) bool
func scaleInOnboardingJobRunning(job controllermeta.NodeOnboardingJob) bool
```

Keep each helper deterministic and side-effect free.

- [ ] **Step 6: Implement runtime view completeness**

For every assignment, require a runtime view with the same `SlotID` and a fresh `LastReportAt`:

```go
func scaleInRuntimeViewsCompleteAndFresh(now time.Time, maxAge time.Duration, assignments []controllermeta.SlotAssignment, views []controllermeta.SlotRuntimeView) bool
```

V1 freshness policy:

- Missing view for any assigned Slot blocks with `runtime_views_incomplete_or_stale`.
- `LastReportAt.IsZero()` blocks with `runtime_views_incomplete_or_stale`.
- `now.Sub(LastReportAt) > maxAge` blocks with `runtime_views_incomplete_or_stale`.
- `LastReportAt` more than a small future tolerance, such as 1 second, blocks to catch clock/serialization anomalies.
- Task 1 strict runtime-view warmup errors also block; do not fall back to local observation caches.

- [ ] **Step 7: Implement tail mapping policy**

Use the `NodeScaleInPlanRequest` type introduced in Task 2 and implement one Go method signature only:

```go
func (a *App) PlanNodeScaleIn(ctx context.Context, nodeID uint64, req NodeScaleInPlanRequest) (NodeScaleInReport, error)
```

Do not add a same-name overload or a separate `PlanNodeScaleInWithRequest`; the manager interface in Task 5 calls this exact method. Preflight should pass tail mapping only if:

```go
req.ConfirmStatefulSetTail && req.ExpectedTailNodeID == nodeID
```

Do not use max NodeID as backend-verified safety. You may include `SuggestedTailNodeID` in the report later, but keep it display-only.

- [ ] **Step 8: Implement progress and status calculation**

Report counters:

```go
AssignedSlotReplicas
SlotLeaders
ActiveTasksInvolvingNode
ActiveMigrationsInvolvingNode
ActiveConnections
ClosingConnections
GatewaySessions
ActiveConnectionsUnknown
```

Status precedence:

```go
blocked -> not_started -> failed -> migrating_replicas -> transferring_leaders -> waiting_connections -> ready_to_remove
```

Add a short traceability table as a comment in the test or near the table-driven cases mapping each required preflight code to its input source: nodes, assignments, runtime views, tasks, active migrations, onboarding jobs, `SlotReplicaN`, strict read errors, and runtime summary.

Before `start`, `active_reconcile_tasks_involving_target` blocks any active task that references the target. After the target is already Draining, tasks generated by the drain are progress signals; they keep status in `migrating_replicas` unless failed, but they should not make an idempotent `start` fail as a new preflight violation.

`ready_to_remove` requires Draining node status plus zero replicas, leaders, tasks, migrations, active/closing connections, and gateway sessions.

- [ ] **Step 9: Run preflight/status tests**

Run:

```bash
go test ./internal/usecase/management -run 'TestPlanNodeScaleIn|TestGetNodeScaleInStatus' -count=1
```

Expected: PASS.

- [ ] **Step 10: Commit Task 3**

Run:

```bash
git add internal/usecase/management/app.go internal/usecase/management/node_scalein.go internal/usecase/management/node_scalein_test.go
git commit -m "feat: compute node scale-in safety report"
```

---

### Task 4: Start, Cancel, and Leader Transfer Advance

**Files:**
- Modify: `internal/usecase/management/node_scalein.go`
- Test: `internal/usecase/management/node_scalein_test.go`

- [ ] **Step 1: Add failing start/cancel/advance tests**

Add tests:

```go
func TestStartNodeScaleInBlocksWhenPreflightFails(t *testing.T) { ... }
func TestStartNodeScaleInMarksNodeDrainingAndRefreshesReport(t *testing.T) { ... }
func TestStartNodeScaleInIsIdempotentWhenAlreadyDraining(t *testing.T) { ... }
func TestCancelNodeScaleInCallsResumeNode(t *testing.T) { ... }
func TestAdvanceNodeScaleInTransfersOneLeaderToAliveDataCandidate(t *testing.T) { ... }
func TestAdvanceNodeScaleInReturnsInvalidStateWhenNoLeaderCandidate(t *testing.T) { ... }
func TestAdvanceNodeScaleInDoesNotTreatForceCloseAsSafetyOverride(t *testing.T) { ... }
```

- [ ] **Step 2: Run failing tests**

Run:

```bash
go test ./internal/usecase/management -run 'Test(Start|Cancel|Advance)NodeScaleIn' -count=1
```

Expected: FAIL because action methods are not implemented.

- [ ] **Step 3: Implement action errors and request types**

In `node_scalein.go`, add:

```go
var (
    ErrNodeScaleInBlocked = errors.New("management: node scale-in blocked")
    ErrInvalidNodeScaleInState = errors.New("management: invalid node scale-in state")
)

type AdvanceNodeScaleInRequest struct {
    MaxLeaderTransfers    int
    ForceCloseConnections bool
}
```

Implement these usecase method signatures so `*management.App` satisfies the manager interface in Task 5:

```go
func (a *App) StartNodeScaleIn(ctx context.Context, nodeID uint64, req NodeScaleInPlanRequest) (NodeScaleInReport, error)
func (a *App) GetNodeScaleInStatus(ctx context.Context, nodeID uint64) (NodeScaleInReport, error)
func (a *App) AdvanceNodeScaleIn(ctx context.Context, nodeID uint64, req AdvanceNodeScaleInRequest) (NodeScaleInReport, error)
func (a *App) CancelNodeScaleIn(ctx context.Context, nodeID uint64) (NodeScaleInReport, error)
```

If you need to return a report with an error, use a typed error wrapper:

```go
type NodeScaleInReportError struct {
    Err error
    Report NodeScaleInReport
}
```

Add `Unwrap() error`.

- [ ] **Step 4: Implement `StartNodeScaleIn`**

Behavior:

1. Build report with request context by calling `PlanNodeScaleIn(ctx, nodeID, req)`.
2. If blocking reasons exist and target is not already safely Draining for this operation, return `ErrNodeScaleInBlocked` with report.
3. If target status is already Draining, return report.
4. Call `a.cluster.MarkNodeDraining(ctx, nodeID)`.
5. Rebuild and return report.

- [ ] **Step 5: Implement `CancelNodeScaleIn`**

Behavior:

1. If target node is not found, return `controllermeta.ErrNotFound`.
2. If not Draining, return current report.
3. Call `a.cluster.ResumeNode(ctx, nodeID)`.
4. Rebuild and return report.

- [ ] **Step 6: Implement `AdvanceNodeScaleIn` leader transfer**

Behavior:

1. Clamp `MaxLeaderTransfers`: default 1, max 3.
2. If `ForceCloseConnections` is true before gateway close exists, add a blocked reason or return `ErrInvalidNodeScaleInState`; do not mark safe.
3. For each leader where target is current leader, choose candidate from `DesiredPeers`/`CurrentPeers` that is Active + Alive + Data and not target.
4. Call `a.cluster.TransferSlotLeader(ctx, slotID, multiraft.NodeID(candidate))`.
5. Stop after limit.
6. Rebuild and return report.

- [ ] **Step 7: Run action tests**

Run:

```bash
go test ./internal/usecase/management -run 'Test(Start|Cancel|Advance)NodeScaleIn' -count=1
```

Expected: PASS.

- [ ] **Step 8: Run full management usecase tests**

Run:

```bash
go test ./internal/usecase/management -count=1
```

Expected: PASS.

- [ ] **Step 9: Commit Task 4**

Run:

```bash
git add internal/usecase/management/node_scalein.go internal/usecase/management/node_scalein_test.go
git commit -m "feat: add node scale-in actions"
```

---

### Task 5: Manager Scale-in HTTP API

**Files:**
- Modify: `internal/access/manager/server.go`
- Modify: `internal/access/manager/routes.go`
- Create: `internal/access/manager/node_scalein.go`
- Test: `internal/access/manager/server_test.go`

- [ ] **Step 1: Add failing manager route tests**

In `internal/access/manager/server_test.go`, add tests:

```go
func TestManagerNodeScaleInPlanReturnsReport(t *testing.T) { ... }
func TestManagerNodeScaleInStartReturnsBlockedReport(t *testing.T) { ... }
func TestManagerNodeScaleInStatusReturnsReport(t *testing.T) { ... }
func TestManagerNodeScaleInAdvanceClampsRequest(t *testing.T) { ... }
func TestManagerNodeScaleInCancelReturnsReport(t *testing.T) { ... }
func TestManagerNodeScaleInRoutesRequirePermissions(t *testing.T) { ... }
func TestManagerNodeScaleInRejectsInvalidNodeID(t *testing.T) { ... }
func TestManagerNodeScaleInRejectsInvalidJSONBody(t *testing.T) { ... }
```

Follow existing manager tests using `httptest`, `managementStub`, and permission config.

- [ ] **Step 2: Run failing route tests**

Run:

```bash
go test ./internal/access/manager -run 'TestManagerNodeScaleIn' -count=1
```

Expected: FAIL because routes and interface methods do not exist.

- [ ] **Step 3: Extend manager `Management` interface**

In `internal/access/manager/server.go`, add methods:

```go
PlanNodeScaleIn(ctx context.Context, nodeID uint64, req managementusecase.NodeScaleInPlanRequest) (managementusecase.NodeScaleInReport, error)
StartNodeScaleIn(ctx context.Context, nodeID uint64, req managementusecase.NodeScaleInPlanRequest) (managementusecase.NodeScaleInReport, error)
GetNodeScaleInStatus(ctx context.Context, nodeID uint64) (managementusecase.NodeScaleInReport, error)
AdvanceNodeScaleIn(ctx context.Context, nodeID uint64, req managementusecase.AdvanceNodeScaleInRequest) (managementusecase.NodeScaleInReport, error)
CancelNodeScaleIn(ctx context.Context, nodeID uint64) (managementusecase.NodeScaleInReport, error)
```

Update `managementStub` in tests.

- [ ] **Step 4: Add routes**

In `internal/access/manager/routes.go`, add read group:

```go
scaleInReads := s.engine.Group("/manager")
if s.auth.enabled() {
    scaleInReads.Use(s.requirePermission("cluster.node", "r"))
    scaleInReads.Use(s.requirePermission("cluster.slot", "r"))
}
scaleInReads.POST("/nodes/:node_id/scale-in/plan", s.handleNodeScaleInPlan)
scaleInReads.GET("/nodes/:node_id/scale-in/status", s.handleNodeScaleInStatus)
```

Add write group:

```go
scaleInWrites := s.engine.Group("/manager")
if s.auth.enabled() {
    scaleInWrites.Use(s.requirePermission("cluster.node", "w"))
    scaleInWrites.Use(s.requirePermission("cluster.slot", "w"))
}
scaleInWrites.POST("/nodes/:node_id/scale-in/start", s.handleNodeScaleInStart)
scaleInWrites.POST("/nodes/:node_id/scale-in/advance", s.handleNodeScaleInAdvance)
scaleInWrites.POST("/nodes/:node_id/scale-in/cancel", s.handleNodeScaleInCancel)
```

- [ ] **Step 5: Implement DTO handlers**

Create `internal/access/manager/node_scalein.go` with request/response mapping.

Request DTOs:

```go
type nodeScaleInPlanRequest struct {
    ConfirmStatefulSetTail bool   `json:"confirm_statefulset_tail"`
    ExpectedTailNodeID     uint64 `json:"expected_tail_node_id"`
}

type advanceNodeScaleInRequest struct {
    MaxLeaderTransfers    int  `json:"max_leader_transfers"`
    ForceCloseConnections bool `json:"force_close_connections"`
}
```

Map reports to snake_case JSON matching the spec.

- [ ] **Step 6: Implement scale-in error mapping**

In handler file, add error mapping that preserves the wrapped scale-in cause:

```go
func handleNodeScaleInError(c *gin.Context, err error) {
    var reportErr *managementusecase.NodeScaleInReportError
    switch {
    case errors.As(err, &reportErr) && errors.Is(reportErr.Err, managementusecase.ErrNodeScaleInBlocked):
        c.JSON(http.StatusConflict, nodeScaleInErrorResponse("scale_in_blocked", "scale-in blocked", reportErr.Report))
    case errors.As(err, &reportErr) && errors.Is(reportErr.Err, managementusecase.ErrInvalidNodeScaleInState):
        c.JSON(http.StatusConflict, nodeScaleInErrorResponse("invalid_scale_in_state", "invalid scale-in state", reportErr.Report))
    case errors.Is(err, controllermeta.ErrNotFound):
        jsonError(c, http.StatusNotFound, "node_not_found", "node not found")
    case errors.Is(err, raftcluster.ErrObservationNotReady):
        jsonError(c, http.StatusServiceUnavailable, "controller_observation_not_ready", "controller observation not ready")
    case leaderConsistentReadUnavailable(err):
        jsonError(c, http.StatusServiceUnavailable, "controller_unavailable", "controller leader unavailable")
    default:
        jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
    }
}
```

- [ ] **Step 7: Run manager route tests**

Run:

```bash
go test ./internal/access/manager -run 'TestManagerNodeScaleIn' -count=1
```

Expected: PASS.

- [ ] **Step 8: Run all manager access tests**

Run:

```bash
go test ./internal/access/manager -count=1
```

Expected: PASS.

- [ ] **Step 9: Commit Task 5**

Run:

```bash
git add internal/access/manager/server.go internal/access/manager/routes.go internal/access/manager/node_scalein.go internal/access/manager/server_test.go
git commit -m "feat: add manager node scale-in api"
```

---

### Task 6: Cached Drain State and Readiness

**Files:**
- Create: `internal/app/node_drain_state.go`
- Modify: `internal/app/app.go`
- Modify: `internal/app/build.go`
- Modify: `internal/app/observability.go`
- Test: existing `internal/app` observability/readiness tests or add `internal/app/node_drain_state_test.go`

- [ ] **Step 1: Write failing drain-state tests**

Create `internal/app/node_drain_state_test.go`:

```go
func TestNodeDrainStateMarksLocalNodeDraining(t *testing.T) { ... }
func TestNodeDrainStateTreatsStaleUnknownAsNotReady(t *testing.T) { ... }
func TestReadyzReportIncludesNodeNotDrainingCheck(t *testing.T) { ... }
```

- [ ] **Step 2: Run failing app tests**

Run:

```bash
go test ./internal/app -run 'TestNodeDrainState|TestReadyzReportIncludesNodeNotDrainingCheck' -count=1
```

Expected: FAIL because drain state does not exist and readiness lacks the check.

- [ ] **Step 3: Implement cached drain state**

Create `internal/app/node_drain_state.go`:

```go
type nodeDrainState struct {
    localNodeID uint64
    staleAfter  time.Duration
    now         func() time.Time
    mu          sync.RWMutex
    status      controllermeta.NodeStatus
    observedAt  time.Time
    known       bool
}

func newNodeDrainState(localNodeID uint64, now func() time.Time) *nodeDrainState { ... }
func (s *nodeDrainState) Observe(nodeID uint64, status controllermeta.NodeStatus) { ... }
func (s *nodeDrainState) Ready() (bool, string) { ... }
func (s *nodeDrainState) Draining() bool { ... }
func (s *nodeDrainState) KnownNotDraining() bool { ... }
```

`Ready` returns false for Draining and false for stale/unknown if stale policy is enabled. Use a conservative but not tiny `staleAfter`, e.g. `15s`, to avoid probe blast radius during short leader changes.

- [ ] **Step 4: Wire observer hook**

In `internal/app/app.go`, add `nodeDrainState *nodeDrainState` to `App`. In `internal/app/build.go`, initialize `app.nodeDrainState` before cluster creation and update the existing `OnNodeStatusChange` hook:

```go
app.nodeDrainState.Observe(nodeID, to)
```

Also seed the state from `ListNodesStrict` opportunistically after cluster start if there is an existing lifecycle point; if not, let unknown fail closed only after stale policy says so.

- [ ] **Step 5: Update readiness**

In `internal/app/observability.go`, update `readyzReport`:

```go
nodeNotDraining, drainReason := a.localNodeNotDraining()
ready := gatewayReady && clusterReady && hashSlotReady && nodeNotDraining
```

Add response check:

```go
"node_not_draining": nodeNotDraining,
"node_drain_reason": drainReason,
```

- [ ] **Step 6: Run app tests**

Run:

```bash
go test ./internal/app -run 'TestNodeDrainState|TestReadyz' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit Task 6**

Run:

```bash
git add internal/app/node_drain_state.go internal/app/app.go internal/app/build.go internal/app/observability.go internal/app/*_test.go
git commit -m "feat: make readiness drain aware"
```

---

### Task 7: Local Online and Gateway Session Summary

**Files:**
- Modify: `internal/runtime/online/types.go`
- Modify: `internal/runtime/online/registry.go`
- Test: `internal/runtime/online/registry_test.go`
- Modify: `internal/gateway/core/server.go`
- Modify: `internal/gateway/gateway.go`
- Test: gateway core tests, create if absent

- [ ] **Step 1: Write failing online summary test**

In `internal/runtime/online/registry_test.go`, add:

```go
func TestMemoryRegistrySummaryCountsActiveAndClosingConnections(t *testing.T) { ... }
```

Assert active, closing, total, and listener counts.

- [ ] **Step 2: Run failing online summary test**

Run:

```bash
go test ./internal/runtime/online -run TestMemoryRegistrySummaryCountsActiveAndClosingConnections -count=1
```

Expected: FAIL because summary API does not exist.

- [ ] **Step 3: Add online summary API**

In `internal/runtime/online/types.go`, add:

```go
type Summary struct {
    Active            int
    Closing           int
    Total             int
    SessionsByListener map[string]int
}
```

Extend `Registry`:

```go
Summary() Summary
```

Implement in `MemoryRegistry.Summary()` by iterating `bySession` under lock and counting both `LocalRouteStateActive` and `LocalRouteStateClosing`.

- [ ] **Step 4: Run online package tests**

Run:

```bash
go test ./internal/runtime/online -count=1
```

Expected: PASS.

- [ ] **Step 5: Write failing gateway session summary/admission tests**

In gateway core tests, add tests around `core.Server` if test helpers exist. If no suitable test file exists, create `internal/gateway/core/server_drain_test.go` with package `core`.

Tests:

```go
func TestServerSessionSummaryCountsUnauthenticatedStatesByListener(t *testing.T) { ... }
func TestServerRejectsOpenWhenNotAccepting(t *testing.T) { ... }
```

- [ ] **Step 6: Run failing gateway tests**

Run:

```bash
go test ./internal/gateway/core -run 'TestServer(SessionSummary|RejectsOpenWhenNotAccepting)' -count=1
```

Expected: FAIL because gateway summary/admission controls do not exist.

- [ ] **Step 7: Add gateway session summary and accepting gate**

In `internal/gateway/core/server.go`, add atomic accepting state to `Server`:

```go
accepting atomic.Bool
```

Initialize `accepting.Store(true)` in `NewServer`.

Add methods:

```go
type SessionSummary struct {
    GatewaySessions    int
    SessionsByListener map[string]int
    AcceptingNewSessions bool
}

func (s *Server) SetAcceptingNewSessions(accepting bool) { ... }
func (s *Server) AcceptingNewSessions() bool { ... }
func (s *Server) SessionSummary() SessionSummary { ... }
```

At the top of `onOpen`, before creating/registering `sessionState`, reject when not accepting:

```go
if !s.AcceptingNewSessions() {
    _ = conn.Close()
    return nil
}
```

Use the transport connection's close method if available; if close API differs, follow the existing transport abstraction.

- [ ] **Step 8: Expose through gateway wrapper**

In `internal/gateway/gateway.go`, add:

```go
func (g *Gateway) SetAcceptingNewSessions(accepting bool)
func (g *Gateway) AcceptingNewSessions() bool
func (g *Gateway) SessionSummary() core.SessionSummary
```

- [ ] **Step 9: Run gateway tests**

Run:

```bash
go test ./internal/gateway/core ./internal/gateway -count=1
```

Expected: PASS.

- [ ] **Step 10: Commit Task 7**

Run:

```bash
git add internal/runtime/online/types.go internal/runtime/online/registry.go internal/runtime/online/registry_test.go internal/gateway/core/server.go internal/gateway/core/*_test.go internal/gateway/gateway.go
git commit -m "feat: add runtime session summaries"
```

---

### Task 8: Runtime Summary Reader and Node RPC

**Files:**
- Modify: `internal/app/app.go`
- Create: `internal/app/runtime_summary.go`
- Modify: `internal/app/build.go`
- Modify: `internal/access/node/options.go`
- Modify: `internal/access/node/service_ids.go`
- Create: `internal/access/node/runtime_summary_rpc.go`
- Modify: `internal/access/node/client.go`
- Test: `internal/access/node/runtime_summary_rpc_test.go`
- Modify: `internal/usecase/management/node_scalein.go`

- [ ] **Step 1: Write failing node RPC tests**

Create `internal/access/node/runtime_summary_rpc_test.go`:

```go
func TestRuntimeSummaryRPCReturnsLocalSummary(t *testing.T) { ... }
func TestRuntimeSummaryClientCallsRemoteNode(t *testing.T) { ... }
```

Follow existing node RPC tests for handler/client patterns.

- [ ] **Step 2: Run failing node RPC tests**

Run:

```bash
go test ./internal/access/node -run TestRuntimeSummary -count=1
```

Expected: FAIL because RPC does not exist.

- [ ] **Step 3: Define runtime summary provider interface in node adapter**

In `internal/access/node/options.go`, add:

```go
type RuntimeSummary struct {
    NodeID               uint64
    ActiveOnline         int
    ClosingOnline        int
    TotalOnline          int
    GatewaySessions      int
    SessionsByListener   map[string]int
    AcceptingNewSessions bool
    Draining             bool
    Unknown              bool
}

type RuntimeSummaryProvider interface {
    LocalRuntimeSummary(ctx context.Context) (RuntimeSummary, error)
}
```

Keep this DTO local to `internal/access/node`. Do not import management DTOs here.

Add to `Options` and `Adapter`:

```go
RuntimeSummary RuntimeSummaryProvider
```

- [ ] **Step 4: Register runtime summary RPC**

Choose an unused service ID in `internal/access/node/service_ids.go`. Add handler registration in `New`:

```go
opts.Cluster.RPCMux().Handle(runtimeSummaryRPCServiceID, adapter.handleRuntimeSummaryRPC)
```

Create `internal/access/node/runtime_summary_rpc.go` with JSON request/response encoding consistent with other node RPCs.

- [ ] **Step 5: Add client method**

In `internal/access/node/client.go`, add:

```go
func (c *Client) RuntimeSummary(ctx context.Context, nodeID uint64) (RuntimeSummary, error) {
    return callDirectRPC(ctx, c, nodeID, runtimeSummaryRPCServiceID, runtimeSummaryRequest{NodeID: nodeID}, decodeRuntimeSummaryResponse)
}
```

- [ ] **Step 6: Implement app runtime summary adapters**

Create `internal/app/runtime_summary.go` with a shared local collector plus two small adapters:

```go
type runtimeSummaryCollector struct {
    app *App
}

func (c runtimeSummaryCollector) localNodeSummary(ctx context.Context) (accessnode.RuntimeSummary, error) { ... }

type managementRuntimeSummaryReader struct {
    collector runtimeSummaryCollector
    nodeClient *accessnode.Client
}

func (r managementRuntimeSummaryReader) NodeRuntimeSummary(ctx context.Context, nodeID uint64) (managementusecase.NodeRuntimeSummary, error) { ... }

type nodeRuntimeSummaryProvider struct {
    collector runtimeSummaryCollector
}

func (p nodeRuntimeSummaryProvider) LocalRuntimeSummary(ctx context.Context) (accessnode.RuntimeSummary, error) { ... }
```

`managementRuntimeSummaryReader` satisfies `management.RuntimeSummaryReader`. `nodeRuntimeSummaryProvider` satisfies `access/node.RuntimeSummaryProvider`. Do not try to make one type implement both interfaces with a same-name method returning different DTOs; Go does not allow that.

`internal/app` owns DTO mapping in both directions:

- local collector returns `accessnode.RuntimeSummary` for the node RPC boundary;
- management adapter maps local or remote `accessnode.RuntimeSummary` to `managementusecase.NodeRuntimeSummary`;
- node RPC adapter never imports `internal/usecase/management`.

Local collector path:


```go
onlineSummary := a.onlineRegistry.Summary()
gatewaySummary := a.gateway.SessionSummary()
return management.NodeRuntimeSummary{
    NodeID: nodeID,
    ActiveOnline: onlineSummary.Active,
    ClosingOnline: onlineSummary.Closing,
    TotalOnline: onlineSummary.Total,
    GatewaySessions: gatewaySummary.GatewaySessions,
    SessionsByListener: merge listener maps,
    AcceptingNewSessions: gatewaySummary.AcceptingNewSessions,
    Draining: a.nodeDrainState != nil && a.nodeDrainState.Draining(),
}

Do not derive `Draining` only from gateway admission, because admission may be disabled while local drain state is still unknown during startup.
```

Remote management path calls `app.nodeClient.RuntimeSummary(ctx, nodeID)` and maps the `accessnode.RuntimeSummary` DTO into the management DTO.

- [ ] **Step 7: Wire app runtime summary**

In `internal/app/build.go`:

- Add `onlineRegistry online.Registry` and `nodeDrainState *nodeDrainState` fields to `internal/app.App` if they do not already exist.
- Assign `app.onlineRegistry = onlineRegistry` immediately after `online.NewRegistry()` in `build.go`.
- Construct `runtimeSummaryCollector` after `nodeClient`, `onlineRegistry`, `gateway`, and `nodeDrainState` are available.
- Pass `nodeRuntimeSummaryProvider{collector: collector}` into `access/node.New` options.
- Pass `managementRuntimeSummaryReader{collector: collector, nodeClient: app.nodeClient}` into `managementusecase.New` options.

Avoid import cycles by placing adapters in `internal/app`.

- [ ] **Step 8: Update scale-in report to use runtime summary**

In `internal/usecase/management/node_scalein.go`, replace unknown runtime summary when `RuntimeSummaryReader` exists. Set:

```go
ConnectionSafetyVerified = !summary.Unknown
Progress.ActiveConnections = summary.ActiveOnline
Progress.ClosingConnections = summary.ClosingOnline
Progress.GatewaySessions = summary.GatewaySessions
```

`waiting_connections` applies when any count is non-zero or unknown.

- [ ] **Step 9: Run tests**

Run:

```bash
go test ./internal/access/node ./internal/app ./internal/usecase/management -run 'RuntimeSummary|NodeScaleIn' -count=1
```

Expected: PASS.

- [ ] **Step 10: Commit Task 8**

Run:

```bash
git add internal/app/app.go internal/app/runtime_summary.go internal/app/build.go internal/access/node/options.go internal/access/node/service_ids.go internal/access/node/runtime_summary_rpc.go internal/access/node/client.go internal/access/node/runtime_summary_rpc_test.go internal/usecase/management/node_scalein.go internal/usecase/management/node_scalein_test.go
git commit -m "feat: add node runtime summary rpc"
```

---

### Task 9: Gateway Admission Drain Wiring

**Files:**
- Modify: `internal/app/build.go`
- Modify: `internal/app/node_drain_state.go`
- Modify: `internal/app/runtime_summary.go`
- Modify: `internal/gateway/gateway.go` if not finished in Task 7
- Test: `internal/app` tests or gateway integration-style unit tests

- [ ] **Step 1: Write failing test that Draining disables gateway admission**

In `internal/app/node_drain_state_test.go` or a new app test:

```go
func TestLocalNodeDrainingDisablesGatewayAdmission(t *testing.T) { ... }
```

If constructing full `App` is too heavy, test the coordinator object that observes drain state and calls gateway `SetAcceptingNewSessions(false)`.

- [ ] **Step 2: Run failing test**

Run:

```bash
go test ./internal/app -run TestLocalNodeDrainingDisablesGatewayAdmission -count=1
```

Expected: FAIL because no coordinator toggles gateway admission.

- [ ] **Step 3: Add admission update hook**

In the existing `OnNodeStatusChange` hook in `internal/app/build.go`, after updating node drain state, add:

```go
if nodeID == cfg.Node.ID && app.gateway != nil {
    app.gateway.SetAcceptingNewSessions(to != controllermeta.NodeStatusDraining)
}
```

Do not rely only on future status-change events. Before the gateway starts accepting traffic, seed local drain state from the latest strict node snapshot or the controller observer cache. Initialize gateway admission to `false` while local status is unknown; set it to `true` only after the cached local status is known and not `NodeStatusDraining`. This prevents a pod that restarts while already Draining from admitting new sessions before the next status-change event.

- [ ] **Step 4: Reflect admission in runtime summary**

Ensure `internal/app/runtime_summary.go` returns `AcceptingNewSessions=false` when gateway admission is disabled and sets `Draining=true` only when local drain state says Draining. Unknown local drain state should keep admission false but should not be reported as confirmed Draining.

- [ ] **Step 5: Run app and gateway-related tests**

Run:

```bash
go test ./internal/app ./internal/gateway ./internal/gateway/core -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit Task 9**

Run:

```bash
git add internal/app/build.go internal/app/node_drain_state.go internal/app/runtime_summary.go internal/app/*_test.go internal/gateway/gateway.go internal/gateway/core/server.go internal/gateway/core/*_test.go
git commit -m "feat: stop gateway admission during drain"
```

---

### Task 10: Documentation and FLOW Updates

**Files:**
- Create: `docs/wiki/operations/manager-node-scale-in.md` or nearest existing operations/runbook location
- Modify: `pkg/cluster/FLOW.md` if changed in Task 1
- Maybe modify: `docs/development/PROJECT_KNOWLEDGE.md` if a concise durable project rule was discovered

- [ ] **Step 1: Write the runbook**

Create `docs/wiki/operations/manager-node-scale-in.md` with:

```markdown
# Manager-Driven Node Scale-in Runbook

## Scope
- Supports one non-controller data/gateway node at a time.
- Does not remove physical Slots.
- Does not scale Kubernetes automatically.

## Required Safety
- Target must be StatefulSet tail node, verified by explicit mapping or operator confirmation.
- Wait for `ready_to_remove` before `kubectl scale`.

## Procedure
1. Call plan.
2. Start scale-in.
3. Watch status.
4. Advance leader transfer.
5. Wait for connection/session counters to zero.
6. Scale StatefulSet down manually.
7. Verify health.

## Rollback
- Before Pod removal, call cancel.
- After Pod removal, do not reuse PVC without following recovery procedure.
```

- [ ] **Step 2: Check PROJECT_KNOWLEDGE need**

If this work establishes a project rule that is not already documented, append one concise bullet to `docs/development/PROJECT_KNOWLEDGE.md`, for example:

```markdown
- Manager-driven node scale-in drains a node to `ready_to_remove`; it must not call physical Slot removal or Kubernetes scale-down directly.
```

Keep the document short per AGENTS instructions.

- [ ] **Step 3: Run doc/path checks**

Run:

```bash
test -s docs/wiki/operations/manager-node-scale-in.md
rg -n "RemoveSlot|ready_to_remove|StatefulSet" docs/wiki/operations/manager-node-scale-in.md
```

Expected: file exists and contains key safety warnings.

- [ ] **Step 4: Commit Task 10**

Run:

```bash
git add docs/wiki/operations/manager-node-scale-in.md docs/development/PROJECT_KNOWLEDGE.md pkg/cluster/FLOW.md
git commit -m "docs: add node scale-in runbook"
```

Omit unchanged files from `git add`.

---

### Task 11: Final Verification

**Files:**
- All changed files

- [ ] **Step 1: Run targeted test suites**

Run:

```bash
go test ./pkg/cluster ./internal/usecase/management ./internal/access/manager ./internal/runtime/online ./internal/gateway/core ./internal/gateway ./internal/access/node ./internal/app -count=1
```

Expected: PASS.

- [ ] **Step 2: Run broader unit tests**

Run:

```bash
go test ./internal/... ./pkg/... -count=1
```

Expected: PASS. If it is too slow or unrelated failures appear, capture exact failures and do not claim full pass.

- [ ] **Step 3: Run full unit suite if practical**

Run:

```bash
go test ./... -count=1
```

Expected: PASS. Do not run integration tests unless explicitly requested.

- [ ] **Step 4: Manual API smoke test on a dev cluster**

Start a local 3-node dev cluster using the existing Docker Compose flow, then call:

```bash
curl -s -X POST http://127.0.0.1:5301/manager/nodes/3/scale-in/plan \
  -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer <token>' \
  -d '{"confirm_statefulset_tail":true,"expected_tail_node_id":3}'
```

Expected: JSON report with `safe_to_remove=false` unless node 3 already has no replicas/leaders/sessions.

- [ ] **Step 5: Check git diff**

Run:

```bash
git status --short
git diff --check
```

Expected: only intentional files changed; no whitespace errors.

- [ ] **Step 6: Final commit if needed**

If any verification fixes were made:

```bash
git add <changed-files>
git commit -m "test: verify node scale-in flow"
```

- [ ] **Step 7: Report verification evidence**

In the final response, include exact commands run and whether they passed. Do not claim unrun integration coverage.
