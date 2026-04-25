# Manager Slot Recover And Rebalance Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `POST /manager/slots/:slot_id/recover` and `POST /manager/slots/rebalance` so the backend manager can run slot recovery and cluster-wide slot rebalance through JWT-protected manager APIs with controller-leader-consistent semantics.

**Architecture:** First add a strict recover entry in `pkg/cluster` so manager recover never falls back to stale local assignment state. Then add recover/rebalance orchestration in `internal/usecase/management`, where recover reuses strict slot detail pre/post reads and rebalance first forces a leader-sourced assignment/hash-slot-table sync before calculating and starting migrations. Finally expose both operations in `internal/access/manager` with explicit outcome DTOs and conflict/error mapping.

**Tech Stack:** Go, `gin`, `testing`, `testify`, `internal/access/manager`, `internal/usecase/management`, `pkg/cluster`, `internal/app`, Markdown specs/plans.

---

## References

- Spec: `docs/superpowers/specs/2026-04-22-manager-slot-recover-rebalance-design.md`
- Existing slot leader transfer pattern: `internal/usecase/management/slot_operator.go`, `internal/access/manager/slot_operator.go`
- Existing slot read path: `internal/usecase/management/slot_detail.go`, `internal/access/manager/slots.go`
- Cluster operator surface: `pkg/cluster/operator.go`, `pkg/cluster/api.go`
- Cluster flow doc to keep aligned: `pkg/cluster/FLOW.md`
- Follow `@superpowers:test-driven-development` for every behavior change.
- Run `@superpowers:verification-before-completion` before claiming execution is done.
- Before editing a package, re-check whether it has a `FLOW.md`; if behavior changes, update it.
- Add English comments on new exported methods, DTOs, and error values to satisfy `AGENTS.md`.

## File Structure

- Modify: `pkg/cluster/api.go` — extend the public cluster API surface that `internal/app` stores behind `raftcluster.API`.
- Modify: `pkg/cluster/operator.go` — add a strict recover path and keep the existing non-strict path shared through one helper.
- Modify: `pkg/cluster/operator_test.go` — unit tests for strict recover and rebalance preconditions.
- Modify: `pkg/cluster/FLOW.md` — document the new strict recover operator path for manager use.
- Modify: `internal/app/observability_test.go` — keep the fake cluster implementation compiling after the API surface grows.
- Modify: `internal/usecase/management/app.go` — extend the management cluster dependency with strict recover and rebalance methods.
- Create: `internal/usecase/management/slot_recover_rebalance.go` — manager usecases and domain errors for recover/rebalance.
- Create: `internal/usecase/management/slot_recover_rebalance_test.go` — usecase tests for recover/rebalance orchestration.
- Modify: `internal/usecase/management/nodes_test.go` — extend the shared fake cluster reader with new hooks if that remains the lowest-noise fake.
- Modify: `internal/access/manager/server.go` — extend the manager dependency interface.
- Modify: `internal/access/manager/routes.go` — add `cluster.slot:w` routes for recover and rebalance.
- Modify: `internal/access/manager/slot_operator.go` — add recover/rebalance handlers and DTO mapping alongside slot leader transfer.
- Modify: `internal/access/manager/server_test.go` — extend the management stub with recover/rebalance responses and errors.
- Create: `internal/access/manager/slot_recover_rebalance_test.go` — focused HTTP contract tests.

### Task 1: Add strict slot recover support to `pkg/cluster`

**Files:**
- Modify: `pkg/cluster/api.go`
- Modify: `pkg/cluster/operator.go`
- Modify: `pkg/cluster/operator_test.go`
- Modify: `pkg/cluster/FLOW.md`
- Modify: `internal/app/observability_test.go`

- [ ] **Step 1: Write the failing cluster tests**

Add focused tests that pin the new behavior before changing production code, for example:

```go
func TestRecoverSlotStrictUsesLeaderAssignments(t *testing.T) {
    cluster := &Cluster{
        controllerResources: controllerResources{
            controllerLeaderWaitTimeout: testControllerLeaderWaitTimeout,
            controllerClient: fakeControllerClient{
                assignments: []controllermeta.SlotAssignment{{SlotID: 1, DesiredPeers: []uint64{1, 2, 3}}},
            },
        },
        cfg: Config{NodeID: 1},
        runtime: fakeRecoverRuntime{statusErr: nil},
    }

    err := cluster.RecoverSlotStrict(context.Background(), 1, RecoverStrategyLatestLiveReplica)

    require.NoError(t, err)
}

func TestRecoverSlotStrictReturnsManualRecoveryRequiredWhenQuorumLost(t *testing.T) { /* strict assignments found, reachable < quorum */ }
func TestRecoverSlotStrictReturnsNotFoundWhenStrictAssignmentsMissSlot(t *testing.T) { /* strict read says slot missing */ }
```

Also add one rebalance precondition test if needed to lock the helper split, for example a unit proving the helper still leaves `Rebalance()` behavior unchanged.

- [ ] **Step 2: Run the focused cluster tests to verify they fail**

Run:

```bash
go test ./pkg/cluster -run 'TestRecoverSlotStrict' -count=1
```

Expected: FAIL because `RecoverSlotStrict` and any shared helper do not exist yet.

- [ ] **Step 3: Implement the minimal strict cluster path**

Update `pkg/cluster/api.go` to add:

```go
RecoverSlotStrict(ctx context.Context, slotID uint32, strategy RecoverStrategy) error
Rebalance(ctx context.Context) ([]MigrationPlan, error)
```

Refactor `pkg/cluster/operator.go` so both recover entry points share one helper:

```go
func (c *Cluster) RecoverSlot(ctx context.Context, slotID uint32, strategy RecoverStrategy) error {
    assignments, err := c.ListSlotAssignments(ctx)
    if err != nil {
        return err
    }
    return c.recoverSlotWithAssignments(ctx, slotID, strategy, assignments)
}

func (c *Cluster) RecoverSlotStrict(ctx context.Context, slotID uint32, strategy RecoverStrategy) error {
    assignments, err := c.ListSlotAssignmentsStrict(ctx)
    if err != nil {
        return err
    }
    return c.recoverSlotWithAssignments(ctx, slotID, strategy, assignments)
}
```

Keep the helper responsible for:
- validating `RecoverStrategyLatestLiveReplica`
- locating `DesiredPeers` for the target slot
- counting reachable peers
- returning `ErrSlotNotFound` / `ErrManualRecoveryRequired` when appropriate

Then update `internal/app/observability_test.go` so `fakeObservabilityCluster` satisfies the expanded `raftcluster.API`, and add one short note to `pkg/cluster/FLOW.md` explaining that manager recover uses the strict controller-leader path.

- [ ] **Step 4: Re-run the focused cluster tests to verify they pass**

Run:

```bash
go test ./pkg/cluster -run 'TestRecoverSlotStrict' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the cluster slice**

```bash
git add pkg/cluster/api.go pkg/cluster/operator.go pkg/cluster/operator_test.go pkg/cluster/FLOW.md internal/app/observability_test.go
git commit -m "feat: add strict slot recovery operator"
```

### Task 2: Add manager recover/rebalance usecases in `internal/usecase/management`

**Files:**
- Modify: `internal/usecase/management/app.go`
- Create: `internal/usecase/management/slot_recover_rebalance.go`
- Create: `internal/usecase/management/slot_recover_rebalance_test.go`
- Modify: `internal/usecase/management/nodes_test.go`

- [ ] **Step 1: Write the failing usecase tests**

Create focused tests before any production code changes, for example:

```go
func TestRecoverSlotRejectsUnsupportedStrategy(t *testing.T) {
    app := New(Options{Cluster: &fakeClusterReader{}})

    _, err := app.RecoverSlot(context.Background(), 2, SlotRecoverStrategy("unknown"))

    require.ErrorIs(t, err, ErrUnsupportedRecoverStrategy)
}

func TestRecoverSlotReturnsNotFoundBeforeCallingOperator(t *testing.T) { /* strict GetSlot pre-check */ }
func TestRecoverSlotReturnsOutcomeAndReloadedSlot(t *testing.T) { /* pre-read -> RecoverSlotStrict -> post-read */ }
func TestRecoverSlotPropagatesManualRecoveryRequired(t *testing.T) { /* want raftcluster.ErrManualRecoveryRequired */ }

func TestRebalanceSlotsRejectsWhenMigrationsAlreadyInProgress(t *testing.T) { /* want ErrSlotMigrationsInProgress */ }
func TestRebalanceSlotsReturnsEmptyPlan(t *testing.T) { /* total=0 items=[] */ }
func TestRebalanceSlotsMapsMigrationPlanItems(t *testing.T) { /* strict sync first, then Rebalance() */ }
func TestRebalanceSlotsPropagatesStrictSyncAndOperatorErrors(t *testing.T) { /* no leader / not started */ }
```

Extend the management fake cluster with fields such as:

```go
recoverSlotStrictErr error
recoverSlotStrictCalls int
recoverSlotStrictSlotID uint32
recoverSlotStrictStrategy raftcluster.RecoverStrategy

migrationStatus []raftcluster.HashSlotMigration
rebalancePlan   []raftcluster.MigrationPlan
rebalanceErr    error
rebalanceCalls  int
```

- [ ] **Step 2: Run the focused usecase tests to verify they fail**

Run:

```bash
go test ./internal/usecase/management -run 'Test(RecoverSlot|RebalanceSlots)' -count=1
```

Expected: FAIL because the new usecase methods, DTOs, and fake hooks do not exist yet.

- [ ] **Step 3: Implement the minimal usecase changes**

Extend `internal/usecase/management/app.go` with:

```go
RecoverSlotStrict(ctx context.Context, slotID uint32, strategy raftcluster.RecoverStrategy) error
Rebalance(ctx context.Context) ([]raftcluster.MigrationPlan, error)
GetMigrationStatus() []raftcluster.HashSlotMigration
```

Create `internal/usecase/management/slot_recover_rebalance.go` with:

```go
var ErrUnsupportedRecoverStrategy = errors.New("management: unsupported slot recover strategy")
var ErrSlotMigrationsInProgress = errors.New("management: slot migrations already in progress")

type SlotRecoverStrategy string
const SlotRecoverStrategyLatestLiveReplica SlotRecoverStrategy = "latest_live_replica"

type SlotRecoverResult struct { Strategy string; Result string; Slot SlotDetail }
type SlotRebalancePlanItem struct { HashSlot uint16; FromSlotID uint32; ToSlotID uint32 }
type SlotRebalanceResult struct { Total int; Items []SlotRebalancePlanItem }
```

Implement:

```go
func (a *App) RecoverSlot(ctx context.Context, slotID uint32, strategy SlotRecoverStrategy) (SlotRecoverResult, error)
func (a *App) RebalanceSlots(ctx context.Context) (SlotRebalanceResult, error)
```

Rules:
- `RecoverSlot` first calls `GetSlot(ctx, slotID)`; if the slot is missing, return `controllermeta.ErrNotFound` without calling the operator.
- Only accept `SlotRecoverStrategyLatestLiveReplica`; otherwise return `ErrUnsupportedRecoverStrategy`.
- Call `a.cluster.RecoverSlotStrict(...)`, then reload via `GetSlot(ctx, slotID)` and return `Result: "quorum_reachable"`.
- `RebalanceSlots` must call `a.cluster.ListSlotAssignmentsStrict(ctx)` first even if the assignments are unused; this is the leader-sourced sync step that refreshes the local router table.
- If `len(a.cluster.GetMigrationStatus()) > 0`, return `ErrSlotMigrationsInProgress` and do not call `Rebalance()`.
- Map `[]raftcluster.MigrationPlan` to manager DTOs in order without introducing extra sorting.

- [ ] **Step 4: Re-run the focused usecase tests to verify they pass**

Run:

```bash
go test ./internal/usecase/management -run 'Test(RecoverSlot|RebalanceSlots)' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the usecase slice**

```bash
git add internal/usecase/management/app.go internal/usecase/management/slot_recover_rebalance.go internal/usecase/management/slot_recover_rebalance_test.go internal/usecase/management/nodes_test.go
git commit -m "feat: add manager slot recover and rebalance usecases"
```

### Task 3: Add manager HTTP endpoints for recover and rebalance

**Files:**
- Modify: `internal/access/manager/server.go`
- Modify: `internal/access/manager/routes.go`
- Modify: `internal/access/manager/slot_operator.go`
- Modify: `internal/access/manager/server_test.go`
- Create: `internal/access/manager/slot_recover_rebalance_test.go`

- [ ] **Step 1: Write the failing HTTP contract tests**

Add focused tests such as:

```go
func TestManagerSlotRecoverRejectsMissingToken(t *testing.T) { /* expect 401 */ }
func TestManagerSlotRecoverRejectsInvalidBody(t *testing.T) { /* expect 400 */ }
func TestManagerSlotRecoverRejectsUnsupportedStrategy(t *testing.T) { /* expect 400 */ }
func TestManagerSlotRecoverReturnsNotFound(t *testing.T) { /* expect 404 */ }
func TestManagerSlotRecoverReturnsConflictWhenManualRecoveryRequired(t *testing.T) { /* expect 409 */ }
func TestManagerSlotRecoverReturnsUpdatedOutcome(t *testing.T) { /* expect SlotRecoverResponse */ }

func TestManagerSlotRebalanceRejectsMissingToken(t *testing.T) { /* expect 401 */ }
func TestManagerSlotRebalanceReturnsConflictWhenMigrationsInProgress(t *testing.T) { /* expect 409 */ }
func TestManagerSlotRebalanceReturnsEmptyPlan(t *testing.T) { /* expect {total:0,items:[]} */ }
func TestManagerSlotRebalanceReturnsPlanItems(t *testing.T) { /* expect SlotRebalanceResponse */ }
func TestManagerSlotRebalanceReturnsServiceUnavailableWhenLeaderUnavailable(t *testing.T) { /* expect 503 */ }
```

Extend the manager stub with:

```go
slotRecover       managementusecase.SlotRecoverResult
slotRecoverErr    error
slotRebalance     managementusecase.SlotRebalanceResult
slotRebalanceErr  error
```

and methods:

```go
RecoverSlot(context.Context, uint32, managementusecase.SlotRecoverStrategy) (managementusecase.SlotRecoverResult, error)
RebalanceSlots(context.Context) (managementusecase.SlotRebalanceResult, error)
```

- [ ] **Step 2: Run the focused HTTP tests to verify they fail**

Run:

```bash
go test ./internal/access/manager -run 'TestManagerSlot(Recover|Rebalance)' -count=1
```

Expected: FAIL because the manager interface, routes, DTOs, and handlers do not exist yet.

- [ ] **Step 3: Implement the minimal HTTP write path**

Update `internal/access/manager/server.go` with:

```go
RecoverSlot(ctx context.Context, slotID uint32, strategy managementusecase.SlotRecoverStrategy) (managementusecase.SlotRecoverResult, error)
RebalanceSlots(ctx context.Context) (managementusecase.SlotRebalanceResult, error)
```

Update `internal/access/manager/routes.go` so the existing `cluster.slot:w` group also registers:

```go
slotWrites.POST("/slots/:slot_id/recover", s.handleSlotRecover)
slotWrites.POST("/slots/rebalance", s.handleSlotRebalance)
```

Extend `internal/access/manager/slot_operator.go` with:

```go
type slotRecoverRequest struct { Strategy string `json:"strategy"` }
type SlotRecoverResponse struct { Strategy string `json:"strategy"`; Result string `json:"result"`; Slot SlotDetailDTO `json:"slot"` }
type SlotRebalancePlanDTO struct { HashSlot uint16 `json:"hash_slot"`; FromSlotID uint32 `json:"from_slot_id"`; ToSlotID uint32 `json:"to_slot_id"` }
type SlotRebalanceResponse struct { Total int `json:"total"`; Items []SlotRebalancePlanDTO `json:"items"` }
```

Handler rules:
- `handleSlotRecover` parses `slot_id`, parses JSON body, calls `s.management.RecoverSlot(...)`, maps `ErrUnsupportedRecoverStrategy` to `400`, `controllermeta.ErrNotFound` to `404`, `raftcluster.ErrManualRecoveryRequired` to `409`, leader/operator unavailable to `503`, and returns `SlotRecoverResponse` on success.
- `handleSlotRebalance` takes no body, calls `s.management.RebalanceSlots()`, maps `ErrSlotMigrationsInProgress` to `409`, leader/operator unavailable to `503`, and returns `SlotRebalanceResponse`.
- Reuse `slotDetailDTO(...)` for the nested slot payload instead of duplicating slot detail mapping.

- [ ] **Step 4: Re-run the focused HTTP tests to verify they pass**

Run:

```bash
go test ./internal/access/manager -run 'TestManagerSlot(Recover|Rebalance)' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the access slice**

```bash
git add internal/access/manager/server.go internal/access/manager/routes.go internal/access/manager/slot_operator.go internal/access/manager/server_test.go internal/access/manager/slot_recover_rebalance_test.go
git commit -m "feat: add manager slot recover and rebalance endpoints"
```

### Task 4: Run focused verification on the finished slice

**Files:**
- No source changes expected unless verification exposes a real issue.

- [ ] **Step 1: Run the cross-package operator and manager verification**

Run:

```bash
go test ./pkg/cluster ./internal/usecase/management ./internal/access/manager -count=1
```

Expected: PASS.

- [ ] **Step 2: Run targeted app verification for manager wiring and fake API coverage**

Run:

```bash
go test ./internal/app -run 'Test(BuildWiresObservabilityIntoAPIAndChannelCluster|NewBuildsOptionalManagerServerWhenConfigured|StartStartsManagerAfterAPIWhenEnabled|StartRollsBackAPIAndClusterWhenManagerStartFails|StopStopsManagerBeforeAPIGatewayAndClusterClose)' -count=1
```

Expected: PASS.

- [ ] **Step 3: If verification exposes failures, fix only the proven issue and re-run the same commands**

Do not bundle unrelated cleanup. Keep fixes scoped to what the failing output proves.
