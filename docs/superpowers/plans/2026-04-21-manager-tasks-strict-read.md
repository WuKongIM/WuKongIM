# Manager Tasks Strict Read Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add leader-consistent `GET /manager/tasks` and `GET /manager/tasks/:slot_id` endpoints so every node returns the same controller-leader task view or fails explicitly.

**Architecture:** Extend `pkg/cluster.API` with strict task list/detail reads that never fall back to stale local metadata, then add manager-facing task DTO aggregation in `internal/usecase/management`. Keep JWT, permission checks, path parsing, and HTTP error mapping in `internal/access/manager`, including `404` for missing task details and `503 service_unavailable` when the controller leader view is unavailable.

**Tech Stack:** Go, `gin`, `testing`, `testify`, `pkg/controller/meta`, `pkg/cluster`, `internal/usecase/management`, `internal/access/manager`, Markdown specs/plans.

---

## References

- Spec: `docs/superpowers/specs/2026-04-21-manager-api-foundation-design.md`
- Follow `@superpowers:test-driven-development` for every code change.
- Run `@superpowers:verification-before-completion` before claiming execution is done.
- Before editing a package, re-check whether it has a `FLOW.md`; if behavior changes, update it.
- Add English comments on new exported methods, DTOs, and non-obvious helpers to satisfy `AGENTS.md`.

## File Structure

- Modify: `pkg/cluster/api.go` — expose strict leader-consistent task read methods for manager-facing task queries.
- Modify: `pkg/cluster/cluster.go` — implement strict task list/detail reads without local fallback.
- Modify: `pkg/cluster/cluster_test.go` — strict task read behavior tests.
- Modify: `pkg/cluster/FLOW.md` — document strict manager task read semantics.
- Modify: `internal/usecase/management/app.go` — widen the strict cluster reader contract for task reads.
- Modify: `internal/usecase/management/slots.go` — extract reusable slot-context aggregation helpers if needed.
- Create: `internal/usecase/management/tasks.go` — task DTOs, enum string mapping, and task detail aggregation.
- Create: `internal/usecase/management/tasks_test.go` — task list/detail aggregation tests.
- Modify: `internal/access/manager/server.go` — expand the management dependency interface with task list/detail methods.
- Modify: `internal/access/manager/routes.go` — add `/manager/tasks` and `/manager/tasks/:slot_id` with `cluster.task:r` permission wiring.
- Create: `internal/access/manager/tasks.go` — task list/detail response DTOs and handlers.
- Modify: `internal/access/manager/server_test.go` — HTTP contract tests for task list/detail, `404`, `400`, and `503` behavior.
- Modify: `internal/app/observability_test.go` — update fake cluster implementations if `pkg/cluster.API` grows new strict task methods.
- Modify: `docs/superpowers/specs/2026-04-21-manager-api-foundation-design.md` — keep task endpoint contract and strict-read semantics aligned with implementation.

### Task 1: Add explicit strict task reads in `pkg/cluster`

**Files:**
- Modify: `pkg/cluster/api.go`
- Modify: `pkg/cluster/cluster.go`
- Modify: `pkg/cluster/cluster_test.go`
- Modify: `pkg/cluster/FLOW.md`

- [ ] **Step 1: Write the failing strict task-read tests**

Add tests in `pkg/cluster/cluster_test.go` that prove task reads never fall back to stale local metadata. Cover at least:

```go
func TestListTasksStrictReturnsLeaderTasksWithoutLocalFallback(t *testing.T) { /* strict method returns controller client tasks, not local tasks */ }
func TestListTasksStrictReturnsLeaderReadErrorWithoutLocalFallback(t *testing.T) { /* strict list returns ErrNoLeader / timeout instead of local tasks */ }
func TestGetReconcileTaskStrictReturnsLeaderTaskWithoutLocalFallback(t *testing.T) { /* strict detail returns controller client task, not local task */ }
func TestGetReconcileTaskStrictReturnsNotFoundWithoutLocalFallback(t *testing.T) { /* strict detail preserves controllermeta.ErrNotFound */ }
func TestGetReconcileTaskStrictReturnsLeaderReadErrorWithoutLocalFallback(t *testing.T) { /* strict detail returns ErrNoLeader / timeout instead of local task */ }
```

Seed the local `controllerMeta` store with different task payloads from the fake controller client so the test would clearly fail if fallback still happens.

- [ ] **Step 2: Run the focused cluster strict task tests to verify they fail**

Run:

```bash
go test ./pkg/cluster -run 'Test(ListTasksStrict|GetReconcileTaskStrict)' -count=1
```

Expected: FAIL because the strict task methods do not exist yet.

- [ ] **Step 3: Implement the minimal strict cluster task methods**

Add methods like these to `pkg/cluster.API` and `*Cluster`:

```go
ListTasksStrict(ctx context.Context) ([]controllermeta.ReconcileTask, error)
GetReconcileTaskStrict(ctx context.Context, slotID uint32) (controllermeta.ReconcileTask, error)
```

Rules:
- If the local node is the controller leader, it may read leader-local metadata directly.
- Otherwise it must use `controllerClient`.
- Do not fall back to local `controllerMeta` on `ErrNotLeader`, `ErrNoLeader`, timeout, or redirect failures.
- Preserve `controllermeta.ErrNotFound` for `GetReconcileTaskStrict` so the manager detail endpoint can map it to `404`.
- Keep existing non-strict `ListTasks` / `GetReconcileTask` behavior unchanged for non-manager callers.

- [ ] **Step 4: Re-run the focused cluster strict task tests to verify they pass**

Run:

```bash
go test ./pkg/cluster -run 'Test(ListTasksStrict|GetReconcileTaskStrict)' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the strict task-read slice**

```bash
git add pkg/cluster/api.go pkg/cluster/cluster.go pkg/cluster/cluster_test.go pkg/cluster/FLOW.md
git commit -m "feat: add strict manager task reads"
```

### Task 2: Add manager task list/detail usecases

**Files:**
- Modify: `internal/usecase/management/app.go`
- Modify: `internal/usecase/management/slots.go`
- Create: `internal/usecase/management/tasks.go`
- Create: `internal/usecase/management/tasks_test.go`

- [ ] **Step 1: Write the failing management usecase tests**

Add tests that lock both the task DTO contract and the strict-read dependency shape. Cover at least:

```go
func TestListTasksSortsBySlotIDAndMapsEnums(t *testing.T) { /* bootstrap / repair / rebalance + step / status strings */ }
func TestListTasksMapsRetryScheduleAndFailureMessage(t *testing.T) { /* retrying keeps NextRunAt, pending/failed map nil appropriately */ }
func TestGetTaskReturnsTaskWithSlotContext(t *testing.T) { /* task detail includes slot state + assignment + runtime */ }
func TestGetTaskReturnsNotFound(t *testing.T) { /* controllermeta.ErrNotFound passes through */ }
```

Define a fake cluster reader with only the strict methods the manager usecase should depend on:

```go
type fakeClusterReader struct {
    tasks       []controllermeta.ReconcileTask
    task        controllermeta.ReconcileTask
    taskErr     error
    assignments []controllermeta.SlotAssignment
    views       []controllermeta.SlotRuntimeView
}
```

- [ ] **Step 2: Run the focused management task tests to verify they fail**

Run:

```bash
go test ./internal/usecase/management -run 'Test(ListTasks|GetTask)' -count=1
```

Expected: FAIL because the task usecases do not exist yet.

- [ ] **Step 3: Implement the minimal management task aggregation**

Update `internal/usecase/management/app.go` to require:

```go
ListTasksStrict(ctx context.Context) ([]controllermeta.ReconcileTask, error)
GetReconcileTaskStrict(ctx context.Context, slotID uint32) (controllermeta.ReconcileTask, error)
```

Then implement `internal/usecase/management/tasks.go` with DTOs similar to:

```go
type Task struct {
    SlotID     uint32
    Kind       string
    Step       string
    Status     string
    SourceNode uint64
    TargetNode uint64
    Attempt    uint32
    NextRunAt  *time.Time
    LastError  string
}

type TaskDetail struct {
    Task
    Slot Slot
}
```

Implementation rules:
- Sort list results by `slot_id` ascending.
- Map task enums to stable strings (`bootstrap|repair|rebalance`, `add_learner|catch_up|promote|transfer_leader|remove_old`, `pending|retrying|failed`).
- Return `NextRunAt=nil` unless the task status is `retrying` and the timestamp is non-zero.
- Reuse the existing slot state derivation logic from `slots.go` for task detail slot context instead of duplicating the state rules.
- Pass `controllermeta.ErrNotFound` through unchanged.

- [ ] **Step 4: Re-run the focused management task tests to verify they pass**

Run:

```bash
go test ./internal/usecase/management -run 'Test(ListTasks|GetTask)' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the management task slice**

```bash
git add internal/usecase/management/app.go internal/usecase/management/slots.go internal/usecase/management/tasks.go internal/usecase/management/tasks_test.go
git commit -m "feat: add manager task usecases"
```

### Task 3: Add `/manager/tasks` and `/manager/tasks/:slot_id`

**Files:**
- Modify: `internal/access/manager/server.go`
- Modify: `internal/access/manager/routes.go`
- Create: `internal/access/manager/tasks.go`
- Modify: `internal/access/manager/server_test.go`

- [ ] **Step 1: Write the failing HTTP task tests**

Extend `internal/access/manager/server_test.go` with at least:

```go
func TestManagerTasksRejectsMissingToken(t *testing.T) { /* expect 401 */ }
func TestManagerTasksRejectsInsufficientPermission(t *testing.T) { /* expect 403 for cluster.task:r */ }
func TestManagerTasksReturnsAggregatedList(t *testing.T) { /* expect total + items */ }
func TestManagerTasksReturnsServiceUnavailableWhenLeaderConsistentReadUnavailable(t *testing.T) { /* expect 503 */ }
func TestManagerTaskDetailRejectsInvalidSlotID(t *testing.T) { /* expect 400 */ }
func TestManagerTaskDetailReturnsTaskWithSlotContext(t *testing.T) { /* expect task + slot context */ }
func TestManagerTaskDetailReturnsNotFound(t *testing.T) { /* expect 404 from controllermeta.ErrNotFound */ }
func TestManagerTaskDetailReturnsServiceUnavailableWhenLeaderConsistentReadUnavailable(t *testing.T) { /* expect 503 */ }
```

Use a stub management dependency that can return `ErrNoLeader`, `ErrNotLeader`, `context.DeadlineExceeded`, or `controllermeta.ErrNotFound`.

- [ ] **Step 2: Run the focused manager HTTP task tests to verify they fail**

Run:

```bash
go test ./internal/access/manager -run 'TestManager(Tasks|TaskDetail)' -count=1
```

Expected: FAIL because the task routes and handlers do not exist yet.

- [ ] **Step 3: Implement the minimal HTTP task endpoints**

Update the management dependency interface in `internal/access/manager/server.go`:

```go
ListTasks(ctx context.Context) ([]managementusecase.Task, error)
GetTask(ctx context.Context, slotID uint32) (managementusecase.TaskDetail, error)
```

Add route-specific permission wiring in `internal/access/manager/routes.go`:

```go
tasks := s.engine.Group("/manager")
if s.auth.enabled() {
    tasks.Use(s.requirePermission("cluster.task", "r"))
}
tasks.GET("/tasks", s.handleTasks)
tasks.GET("/tasks/:slot_id", s.handleTask)
```

Then implement:
- `handleTasks`
- `handleTask`
- task list/detail response DTOs
- `400` mapping for invalid `slot_id`
- `404` mapping for `controllermeta.ErrNotFound`
- `503` mapping for leader-consistent read unavailability using the existing helper semantics

- [ ] **Step 4: Re-run the focused manager HTTP task tests to verify they pass**

Run:

```bash
go test ./internal/access/manager -run 'TestManager(Tasks|TaskDetail)' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the manager HTTP task slice**

```bash
git add internal/access/manager/server.go internal/access/manager/routes.go internal/access/manager/tasks.go internal/access/manager/server_test.go
git commit -m "feat: add manager task endpoints"
```

### Task 4: Fix interface ripple and run focused verification

**Files:**
- Modify: `internal/app/observability_test.go` (if needed)
- Modify: `docs/superpowers/specs/2026-04-21-manager-api-foundation-design.md` (only if implementation details drift)

- [ ] **Step 1: Patch any fake `pkg/cluster.API` implementations broken by the new strict task methods**

Search for compile fallout and update only the necessary test fakes, especially `internal/app/observability_test.go`.

Run:

```bash
rg -n 'ListTasksStrict|GetReconcileTaskStrict|fake.*Cluster' internal pkg cmd -S
```

Expected: identify any fake cluster types that must implement the new interface methods.

- [ ] **Step 2: Run the focused manager-related verification suite**

Run:

```bash
go test ./cmd/wukongim ./internal/app ./internal/usecase/management ./internal/access/manager ./pkg/cluster -run 'Test(LoadConfig.*Manager|Config.*Manager|Manager(Login|Nodes|Slots|Tasks|TaskDetail)|List(Nodes|Slots|Tasks)|GetTask|NewBuildsOptionalManagerServerWhenConfigured|StartStartsManagerAfterAPIWhenEnabled|StartRollsBackAPIAndClusterWhenManagerStartFails|StopStopsManagerBeforeAPIGatewayAndClusterClose|AccessorsExposeBuiltRuntime|List(Nodes|ObservedRuntimeViews|SlotAssignments|Tasks)Strict|GetReconcileTaskStrict)' -count=1
```

Expected: PASS.

- [ ] **Step 3: Run a grep-based contract check**

Run:

```bash
rg -n 'cluster\.task|/manager/tasks|ListTasksStrict|GetReconcileTaskStrict|service_unavailable|not_found' cmd internal pkg docs -S
```

Expected: the new permission, routes, strict methods, and error semantics appear in the intended files only.

- [ ] **Step 4: Commit any remaining verification fallout or spec alignment**

If Task 4 required code or doc changes:

```bash
git add internal/app/observability_test.go docs/superpowers/specs/2026-04-21-manager-api-foundation-design.md
git commit -m "test: align manager task strict read coverage"
```

If no files changed, skip this commit.
