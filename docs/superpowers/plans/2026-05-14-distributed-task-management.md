# Distributed Task Management Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a read-only `/tasks` manager web page backed by unified manager APIs for distributed task summary, filtering, list, and detail inspection.

**Architecture:** Keep task normalization in `internal/usecase/management`, expose a thin DTO layer from `internal/access/manager`, and keep `web/` as a consumer of one stable task model. Existing `/manager/tasks` remains the Slot reconcile compatibility API; new `/manager/distributed-tasks*` endpoints aggregate Slot reconcile tasks, node onboarding jobs, node scale-in state, and channel migration tasks without adding write operations.

**Tech Stack:** Go, Gin, existing management usecase layer, React 19, TypeScript, React Intl, Vitest, Testing Library.

---

## Pre-Execution Notes

- Use @superpowers:test-driven-development for every implementation task.
- Execute implementation in an isolated worktree, not the main worktree.
- Suggested branch/worktree:
  - branch: `feat/distributed-task-management`
  - path: `.worktrees/distributed-task-management`
- Read `AGENTS.md` before implementation.
- Read `internal/FLOW.md` before changing `internal/*`. Update it only if the described flow becomes stale.
- Do not add retry, cancel, advance, start, abort, repair, rebalance, or leader-transfer controls to `/tasks`.
- Do not change existing `/manager/tasks` response shapes.
- Do not make the frontend scan channels or nodes to discover task sources. Web must call only the unified distributed task APIs.
- Keep unknown/proven-runtime semantics: unknown fields render as `-`, `null`, or `unknown`; do not infer missing values.
- Use “single-node cluster” in user-facing copy when deployment shape is mentioned.

## File Structure

### Backend usecase

- Create: `internal/usecase/management/distributed_tasks.go`
  - Common task model, status/domain/scope constants, query/result structs, source aggregation, filtering, sorting, summary, pagination, and detail dispatch.
  - Source adapters for Slot reconcile, node onboarding, node scale-in, and channel migration.
- Create: `internal/usecase/management/distributed_tasks_test.go`
  - Unit tests for status mapping, filtering, deterministic sorting, pagination, partial warnings, and source detail dispatch.
- Modify: `internal/usecase/management/app.go`
  - Add comments/method requirements only if needed; keep `ChannelMigrationStore` focused and add a read method only if implementation cannot safely derive channel migration task lists from existing methods.
- Modify: `internal/usecase/management/channel_migration_test.go` and `internal/usecase/management/node_scalein_test.go`
  - Update fake `ChannelMigrationStore` only if the interface changes.

### Backend manager access

- Create: `internal/access/manager/distributed_tasks.go`
  - JSON DTOs, query parsing, cursor encode/decode calls, handlers, and response conversion.
- Modify: `internal/access/manager/routes.go`
  - Register read-only distributed task routes under `cluster.task:r`.
- Modify: `internal/access/manager/server.go`
  - Add three methods to the `Management` interface.
- Modify: `internal/access/manager/server_test.go`
  - Add handler tests and extend `managementStub`.
- Modify: `internal/access/manager/cursor_codec.go`
  - Add a small opaque cursor codec for distributed task offset pagination.
- Modify: `internal/access/manager/cursor_codec_benchmark_test.go` only if benchmarks require compile updates.

### Frontend API and page

- Modify: `web/src/lib/manager-api.types.ts`
  - Add distributed task query, summary, list, item, warning, detail, and detail payload types.
- Modify: `web/src/lib/manager-api.ts`
  - Add `getDistributedTasksSummary`, `getDistributedTasks`, `getDistributedTask`, and a query builder.
- Modify: `web/src/lib/manager-api.test.ts`
  - Add API client tests for summary/list/detail and encoded query strings.
- Create: `web/src/pages/tasks/page.tsx`
  - Read-only task center page with summary cards, filters, table, partial-warning banner, and detail sheet.
- Create: `web/src/pages/tasks/page.test.tsx`
  - Page tests for rendering, filters, refresh, detail sheet, partial warnings, errors, and empty state.
- Modify: `web/src/app/router.tsx`
  - Add route `tasks`.
- Modify: `web/src/lib/navigation.ts`
  - Add `/tasks` under Global Cluster.
- Modify: `web/src/i18n/messages/en.ts`
  - Add nav/page strings.
- Modify: `web/src/i18n/messages/zh-CN.ts`
  - Add matching Chinese strings.
- Modify: `web/src/pages/page-shells.test.tsx`
  - Add `/tasks` shell expectations for English and Chinese.

### Docs

- Modify: `web/README.md`
  - Add `/tasks` to the page/API matrix.
- Modify: `docs/raw/web-admin-restructure.md`
  - Add distributed task center to the navigation/design notes and mark the read-only MVP as implemented after code lands.
- Do not update `AGENTS.md` unless implementation adds/moves top-level directories.
- Do not update `wukongim.conf.example`; this feature adds no config.

---

## Task 0: Create Isolated Worktree

**Files:**
- None.

- [ ] **Step 1: Check current repository status**

Run:

```bash
git status --short --branch
```

Expected: note any existing changes. Do not stage, revert, or overwrite unrelated user work.

- [ ] **Step 2: Create the feature worktree**

Run:

```bash
git worktree add .worktrees/distributed-task-management -b feat/distributed-task-management
```

Expected: new worktree created on `feat/distributed-task-management`.

- [ ] **Step 3: Enter and verify the worktree**

Run:

```bash
cd .worktrees/distributed-task-management
git branch --show-current
git status --short
```

Expected: branch is `feat/distributed-task-management`; status is clean.

- [ ] **Step 4: Read required local guidance**

Run:

```bash
sed -n '1,240p' AGENTS.md
sed -n '1,240p' internal/FLOW.md
```

Expected: confirm layering and terminology rules before editing.

---

## Task 1: Add Usecase Model And Aggregation Core

**Files:**
- Create: `internal/usecase/management/distributed_tasks.go`
- Create: `internal/usecase/management/distributed_tasks_test.go`

- [ ] **Step 1: Write failing aggregation tests**

Create `internal/usecase/management/distributed_tasks_test.go` with table-driven tests for the pure aggregation path. Start with fake sources so this task does not depend on cluster or store fakes.

Use this skeleton:

```go
package management

import (
    "context"
    "errors"
    "testing"
    "time"

    "github.com/stretchr/testify/require"
)

type fakeDistributedTaskSource struct {
    name     DistributedTaskDomain
    items    []DistributedTask
    warnings []DistributedTaskWarning
    err      error
    detail   DistributedTaskDetail
}

func (f fakeDistributedTaskSource) domain() DistributedTaskDomain { return f.name }
func (f fakeDistributedTaskSource) list(context.Context) ([]DistributedTask, []DistributedTaskWarning, error) {
    return append([]DistributedTask(nil), f.items...), append([]DistributedTaskWarning(nil), f.warnings...), f.err
}
func (f fakeDistributedTaskSource) get(context.Context, string) (DistributedTaskDetail, error) {
    if f.err != nil {
        return DistributedTaskDetail{}, f.err
    }
    return f.detail, nil
}

func TestAggregateDistributedTasksFiltersSortsAndPaginates(t *testing.T) {
    now := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)
    updated := now.Add(-time.Minute)

    result, err := aggregateDistributedTasks(context.Background(), []distributedTaskSource{
        fakeDistributedTaskSource{name: DistributedTaskDomainSlotReconcile, items: []DistributedTask{{
            ID: "1", Domain: DistributedTaskDomainSlotReconcile, Kind: "repair", Status: DistributedTaskStatusRetrying,
            Scope: DistributedTaskScope{Type: DistributedTaskScopeSlot, ID: "1", SlotID: 1}, TargetNode: 3, UpdatedAt: &updated,
        }}},
        fakeDistributedTaskSource{name: DistributedTaskDomainNodeOnboarding, items: []DistributedTask{{
            ID: "job-1", Domain: DistributedTaskDomainNodeOnboarding, Kind: "onboarding", Status: DistributedTaskStatusFailed,
            Scope: DistributedTaskScope{Type: DistributedTaskScopeJob, ID: "job-1"}, TargetNode: 4, LastError: "plan changed",
        }}},
    }, DistributedTaskQuery{Status: DistributedTaskStatusRetrying, NodeID: 3, Limit: 1}, now)

    require.NoError(t, err)
    require.Equal(t, 1, result.Total)
    require.Len(t, result.Items, 1)
    require.Equal(t, "1", result.Items[0].ID)
    require.False(t, result.Partial)
    require.False(t, result.HasMore)
}

func TestAggregateDistributedTasksReturnsPartialWarnings(t *testing.T) {
    result, err := aggregateDistributedTasks(context.Background(), []distributedTaskSource{
        fakeDistributedTaskSource{name: DistributedTaskDomainSlotReconcile, items: []DistributedTask{{ID: "1", Domain: DistributedTaskDomainSlotReconcile, Status: DistributedTaskStatusPending}}},
        fakeDistributedTaskSource{name: DistributedTaskDomainChannelMigration, err: errors.New("store unavailable")},
    }, DistributedTaskQuery{Limit: 50}, time.Now())

    require.NoError(t, err)
    require.True(t, result.Partial)
    require.Len(t, result.Warnings, 1)
    require.Equal(t, DistributedTaskDomainChannelMigration, result.Warnings[0].Domain)
    require.Equal(t, "source_unavailable", result.Warnings[0].Code)
}

func TestAggregateDistributedTasksReturnsUnavailableWhenAllSourcesFail(t *testing.T) {
    _, err := aggregateDistributedTasks(context.Background(), []distributedTaskSource{
        fakeDistributedTaskSource{name: DistributedTaskDomainSlotReconcile, err: errors.New("no leader")},
    }, DistributedTaskQuery{Limit: 50}, time.Now())

    require.ErrorIs(t, err, ErrDistributedTasksUnavailable)
}

func TestDistributedTaskSummaryCountsStatusAndDomain(t *testing.T) {
    summary, err := aggregateDistributedTaskSummary(context.Background(), []distributedTaskSource{
        fakeDistributedTaskSource{name: DistributedTaskDomainSlotReconcile, items: []DistributedTask{{ID: "1", Domain: DistributedTaskDomainSlotReconcile, Status: DistributedTaskStatusRetrying}}},
        fakeDistributedTaskSource{name: DistributedTaskDomainNodeOnboarding, items: []DistributedTask{{ID: "job", Domain: DistributedTaskDomainNodeOnboarding, Status: DistributedTaskStatusRunning}}},
    }, time.Now())

    require.NoError(t, err)
    require.Equal(t, 2, summary.Total)
    require.Equal(t, 1, summary.ByStatus[DistributedTaskStatusRetrying])
    require.Equal(t, 1, summary.ByDomain[DistributedTaskDomainNodeOnboarding])
}
```

- [ ] **Step 2: Run tests to verify RED**

Run:

```bash
go test ./internal/usecase/management -run 'TestAggregateDistributedTasks|TestDistributedTaskSummary' -count=1
```

Expected: FAIL because distributed task types/functions are undefined.

- [ ] **Step 3: Implement common model and pure aggregation**

Create `internal/usecase/management/distributed_tasks.go` with these core declarations and helpers:

```go
package management

import (
    "context"
    "errors"
    "sort"
    "strconv"
    "strings"
    "time"
)

// DistributedTaskDomain identifies a source family in the read-only task center.
type DistributedTaskDomain string

const (
    DistributedTaskDomainSlotReconcile   DistributedTaskDomain = "slot_reconcile"
    DistributedTaskDomainNodeOnboarding  DistributedTaskDomain = "node_onboarding"
    DistributedTaskDomainNodeScaleIn     DistributedTaskDomain = "node_scale_in"
    DistributedTaskDomainChannelMigration DistributedTaskDomain = "channel_migration"
)

// DistributedTaskStatus is the normalized manager-facing task lifecycle state.
type DistributedTaskStatus string

const (
    DistributedTaskStatusPending   DistributedTaskStatus = "pending"
    DistributedTaskStatusRunning   DistributedTaskStatus = "running"
    DistributedTaskStatusRetrying  DistributedTaskStatus = "retrying"
    DistributedTaskStatusBlocked   DistributedTaskStatus = "blocked"
    DistributedTaskStatusFailed    DistributedTaskStatus = "failed"
    DistributedTaskStatusCompleted DistributedTaskStatus = "completed"
    DistributedTaskStatusCancelled DistributedTaskStatus = "cancelled"
    DistributedTaskStatusUnknown   DistributedTaskStatus = "unknown"
)

// DistributedTaskScopeType describes the primary object affected by a task.
type DistributedTaskScopeType string

const (
    DistributedTaskScopeSlot    DistributedTaskScopeType = "slot"
    DistributedTaskScopeNode    DistributedTaskScopeType = "node"
    DistributedTaskScopeChannel DistributedTaskScopeType = "channel"
    DistributedTaskScopeJob     DistributedTaskScopeType = "job"
)

var ErrDistributedTasksUnavailable = errors.New("management: distributed task sources unavailable")
var ErrDistributedTaskNotFound = errors.New("management: distributed task not found")
```

Include English comments on exported structs and fields:

```go
type DistributedTaskQuery struct {
    Domain  DistributedTaskDomain
    Status  DistributedTaskStatus
    NodeID  uint64
    Scope   DistributedTaskScopeType
    Keyword string
    Limit   int
    Offset  int
}

type DistributedTaskScope struct {
    Type        DistributedTaskScopeType
    ID          string
    SlotID      uint32
    ChannelID   string
    ChannelType int64
    NodeID      uint64
}

type DistributedTask struct {
    ID         string
    Domain     DistributedTaskDomain
    Kind       string
    Status     DistributedTaskStatus
    Phase      string
    Scope      DistributedTaskScope
    SourceNode uint64
    TargetNode uint64
    OwnerNode  uint64
    Attempt    uint32
    NextRunAt  *time.Time
    CreatedAt  *time.Time
    UpdatedAt  *time.Time
    LastError  string
    Summary    string
    Links      map[string]string
}
```

Implement:

- `aggregateDistributedTasks(ctx, sources, query, now)`
- `aggregateDistributedTaskSummary(ctx, sources, now)`
- `normalizeDistributedTaskLimit(limit int) int` with default `50`, max `200`.
- `distributedTaskMatchesQuery(task, query)`.
- `distributedTaskMentionsNode(task, nodeID)` matching source, target, owner, and scoped node.
- `distributedTaskKeywordText(task)` matching ID, domain, kind, phase, scope ID, channel ID, and last error.
- `sortDistributedTasks(items)` using severity order: failed, blocked, retrying, running, pending, completed, cancelled, unknown; then newest updated time; then domain; then ID.

- [ ] **Step 4: Run aggregation tests to verify GREEN**

Run:

```bash
go test ./internal/usecase/management -run 'TestAggregateDistributedTasks|TestDistributedTaskSummary' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit aggregation core**

Run:

```bash
git add internal/usecase/management/distributed_tasks.go internal/usecase/management/distributed_tasks_test.go
git commit -m "feat: add distributed task aggregation model"
```

Expected: commit succeeds.

---

## Task 2: Add Usecase Source Adapters

**Files:**
- Modify: `internal/usecase/management/distributed_tasks.go`
- Modify: `internal/usecase/management/distributed_tasks_test.go`
- Modify: `internal/usecase/management/app.go` only if `ChannelMigrationStore` requires a new list method.
- Modify fake store methods in existing management tests only if the interface changes.

- [ ] **Step 1: Write failing source adapter tests**

Append tests to `internal/usecase/management/distributed_tasks_test.go`.

Cover each source:

```go
func TestSlotReconcileSourceMapsExistingTasks(t *testing.T) {
    next := time.Date(2026, 5, 14, 10, 5, 0, 0, time.UTC)
    app := New(Options{Cluster: fakeClusterReader{tasks: []controllermeta.ReconcileTask{{
        SlotID: 2, Kind: controllermeta.TaskKindRepair, Step: controllermeta.TaskStepCatchUp,
        Status: controllermeta.TaskStatusRetrying, SourceNode: 3, TargetNode: 5, Attempt: 1, NextRunAt: next,
        LastError: "learner catch-up timeout",
    }}}})

    items, warnings, err := distributedSlotReconcileSource{app: app}.list(context.Background())

    require.NoError(t, err)
    require.Empty(t, warnings)
    require.Len(t, items, 1)
    require.Equal(t, DistributedTaskDomainSlotReconcile, items[0].Domain)
    require.Equal(t, DistributedTaskStatusRetrying, items[0].Status)
    require.Equal(t, DistributedTaskScopeSlot, items[0].Scope.Type)
    require.Equal(t, uint32(2), items[0].Scope.SlotID)
}

func TestNodeOnboardingSourceMapsJobs(t *testing.T) {
    now := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)
    app := New(Options{Cluster: fakeClusterReader{nodeOnboardingJobs: []controllermeta.NodeOnboardingJob{{
        JobID: "job-1", TargetNodeID: 4, Status: controllermeta.OnboardingJobStatusRunning,
        CreatedAt: now, UpdatedAt: now.Add(time.Minute), CurrentMoveIndex: 0,
    }}}})

    items, warnings, err := distributedNodeOnboardingSource{app: app}.list(context.Background())

    require.NoError(t, err)
    require.Empty(t, warnings)
    require.Len(t, items, 1)
    require.Equal(t, "job-1", items[0].ID)
    require.Equal(t, DistributedTaskStatusRunning, items[0].Status)
    require.Equal(t, uint64(4), items[0].TargetNode)
}
```

Add tests for:

- Node scale-in source creates a task only for draining data nodes and maps `NodeScaleInStatusDrainingChannels` to `running`.
- Node scale-in source maps safety-blocked report to `blocked`.
- Channel migration source deduplicates tasks seen from both source and target node scans.
- Channel migration source maps `metadb.ChannelMigrationStatusAborted` to normalized `cancelled`.
- Channel migration detail decodes the opaque ID and returns `ErrDistributedTaskNotFound` when the active task ID does not match.

- [ ] **Step 2: Run source tests to verify RED**

Run:

```bash
go test ./internal/usecase/management -run 'TestSlotReconcileSource|TestNodeOnboardingSource|TestNodeScaleInSource|TestChannelMigrationSource' -count=1
```

Expected: FAIL because source adapters are undefined or incomplete.

- [ ] **Step 3: Implement `App` entry methods**

In `internal/usecase/management/distributed_tasks.go`, add:

```go
func (a *App) ListDistributedTasks(ctx context.Context, query DistributedTaskQuery) (DistributedTaskListResult, error) {
    if a == nil {
        return DistributedTaskListResult{}, nil
    }
    return aggregateDistributedTasks(ctx, a.distributedTaskSources(), query, a.now())
}

func (a *App) GetDistributedTasksSummary(ctx context.Context) (DistributedTaskSummary, error) {
    if a == nil {
        return DistributedTaskSummary{}, nil
    }
    return aggregateDistributedTaskSummary(ctx, a.distributedTaskSources(), a.now())
}

func (a *App) GetDistributedTask(ctx context.Context, domain DistributedTaskDomain, id string) (DistributedTaskDetail, error) {
    for _, source := range a.distributedTaskSources() {
        if source.domain() == domain {
            return source.get(ctx, id)
        }
    }
    return DistributedTaskDetail{}, ErrDistributedTaskNotFound
}
```

Add `distributedTaskSources()` so sources are present only when their dependencies exist:

```go
func (a *App) distributedTaskSources() []distributedTaskSource {
    if a == nil {
        return nil
    }
    sources := make([]distributedTaskSource, 0, 4)
    if a.cluster != nil {
        sources = append(sources,
            distributedSlotReconcileSource{app: a},
            distributedNodeOnboardingSource{app: a},
            distributedNodeScaleInSource{app: a},
        )
    }
    if a.cluster != nil && a.channelMigration != nil {
        sources = append(sources, distributedChannelMigrationSource{app: a})
    }
    return sources
}
```

- [ ] **Step 4: Implement source adapters**

Implement these unexported structs in `distributed_tasks.go`:

```go
type distributedSlotReconcileSource struct{ app *App }
type distributedNodeOnboardingSource struct{ app *App }
type distributedNodeScaleInSource struct{ app *App }
type distributedChannelMigrationSource struct{ app *App }
```

Guidance:

- Slot reconcile source should reuse `a.ListTasks(ctx)` and `a.GetTask(ctx, slotID)`.
- Node onboarding source should reuse `a.ListNodeOnboardingJobs(ctx, ListNodeOnboardingJobsRequest{Limit: distributedTaskSourceScanLimit})` and `a.GetNodeOnboardingJob(ctx, id)`.
- Node scale-in source should list nodes, select data nodes whose controller status is `draining`, and call `a.GetNodeScaleInStatus(ctx, nodeID)`.
- Channel migration source should list nodes, call `a.channelMigration.ListActiveChannelMigrationTasksForNode(ctx, node.NodeID, distributedTaskSourceScanLimit)` for each active data node, and dedupe by channel type + channel ID + task ID.
- Channel migration detail should decode an opaque ID to channel type, channel ID, and task ID; call `a.GetChannelMigration(ctx, channel.ChannelID{ID: channelID, Type: uint8(channelType)})`; then verify the returned task ID matches.

If existing `ListActiveChannelMigrationTasksForNode` proves too weak in implementation, extend `ChannelMigrationStore` with a read-only method:

```go
// ListActiveChannelMigrationTasks returns active channel migration tasks in deterministic order.
ListActiveChannelMigrationTasks(ctx context.Context, limit int) ([]metadb.ChannelMigrationTask, bool, error)
```

Only add that method if necessary. If added, implement it on the existing `pkg/slot/proxy.Store` path and update fakes.

- [ ] **Step 5: Implement normalization helpers**

Add helpers:

```go
func normalizeNodeOnboardingStatus(status string) DistributedTaskStatus
func normalizeNodeScaleInStatus(status NodeScaleInStatus) DistributedTaskStatus
func normalizeChannelMigrationStatus(status string) DistributedTaskStatus
func timePtrIfSet(t time.Time) *time.Time
func timePtrFromUnixMS(ms int64) *time.Time
func encodeChannelMigrationDistributedTaskID(task metadb.ChannelMigrationTask) string
func decodeChannelMigrationDistributedTaskID(id string) (channelID string, channelType int64, taskID string, err error)
```

Use `base64.RawURLEncoding` for channel migration IDs so channel IDs containing delimiters do not break detail lookup.

- [ ] **Step 6: Run source adapter tests to verify GREEN**

Run:

```bash
go test ./internal/usecase/management -run 'TestSlotReconcileSource|TestNodeOnboardingSource|TestNodeScaleInSource|TestChannelMigrationSource' -count=1
```

Expected: PASS.

- [ ] **Step 7: Run all management usecase tests**

Run:

```bash
go test ./internal/usecase/management -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit source adapters**

Run:

```bash
git add internal/usecase/management/distributed_tasks.go internal/usecase/management/distributed_tasks_test.go internal/usecase/management/app.go internal/usecase/management/*_test.go
git commit -m "feat: aggregate manager distributed task sources"
```

Expected: commit succeeds. If `app.go` or unrelated tests were not changed, omit them from `git add`.

---

## Task 3: Expose Manager Distributed Task APIs

**Files:**
- Create: `internal/access/manager/distributed_tasks.go`
- Modify: `internal/access/manager/routes.go`
- Modify: `internal/access/manager/server.go`
- Modify: `internal/access/manager/server_test.go`
- Modify: `internal/access/manager/cursor_codec.go`

- [ ] **Step 1: Write failing handler tests**

Add tests near existing task tests in `internal/access/manager/server_test.go`:

```go
func TestManagerDistributedTasksRejectsMissingToken(t *testing.T) {
    srv := New(Options{Auth: testAuthConfig(nil), Management: managementStub{}})

    rec := httptest.NewRecorder()
    req := httptest.NewRequest(http.MethodGet, "/manager/distributed-tasks", nil)
    srv.Engine().ServeHTTP(rec, req)

    require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestManagerDistributedTasksRejectsInsufficientPermission(t *testing.T) {
    srv := New(Options{Auth: testAuthConfig([]UserConfig{{Username: "viewer", Password: "secret", Permissions: []PermissionConfig{{Resource: "cluster.slot", Actions: []string{"r"}}}}}), Management: managementStub{}})

    rec := httptest.NewRecorder()
    req := httptest.NewRequest(http.MethodGet, "/manager/distributed-tasks", nil)
    req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "viewer"))
    srv.Engine().ServeHTTP(rec, req)

    require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestManagerDistributedTasksReturnsFilteredList(t *testing.T) {
    updated := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)
    srv := New(Options{
        Auth: testAuthConfig([]UserConfig{{Username: "admin", Password: "secret", Permissions: []PermissionConfig{{Resource: "cluster.task", Actions: []string{"r"}}}}}),
        Management: managementStub{distributedTasks: managementusecase.DistributedTaskListResult{
            Total: 1,
            Items: []managementusecase.DistributedTask{{
                ID: "1", Domain: "slot_reconcile", Kind: "repair", Status: "retrying", Phase: "catch_up",
                Scope: managementusecase.DistributedTaskScope{Type: "slot", ID: "1", SlotID: 1}, TargetNode: 3, UpdatedAt: &updated,
                Links: map[string]string{"slot": "/slots?slot_id=1"},
            }},
        }},
    })

    rec := httptest.NewRecorder()
    req := httptest.NewRequest(http.MethodGet, "/manager/distributed-tasks?domain=slot_reconcile&status=retrying&node_id=3&scope=slot&keyword=repair&limit=25", nil)
    req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))
    srv.Engine().ServeHTTP(rec, req)

    require.Equal(t, http.StatusOK, rec.Code)
    require.JSONEq(t, `{"total":1,"items":[{"id":"1","domain":"slot_reconcile","kind":"repair","status":"retrying","phase":"catch_up","scope":{"type":"slot","id":"1","slot_id":1,"channel_id":"","channel_type":0,"node_id":0},"source_node":0,"target_node":3,"owner_node":0,"attempt":0,"next_run_at":null,"created_at":null,"updated_at":"2026-05-14T10:00:00Z","last_error":"","summary":"","links":{"slot":"/slots?slot_id=1"}}],"next_cursor":"","has_more":false,"partial":false,"warnings":[]}`, rec.Body.String())
}
```

Also add tests for:

- `GET /manager/distributed-tasks/summary` returns status/domain counts.
- `GET /manager/distributed-tasks/:domain/:id` returns source-specific detail payload.
- invalid `status`, `domain`, `scope`, `node_id`, `limit`, or `cursor` returns `400`.
- all sources unavailable maps to `503`.
- missing detail maps to `404`.
- partial list includes `partial=true` and warnings.

- [ ] **Step 2: Run handler tests to verify RED**

Run:

```bash
go test ./internal/access/manager -run 'TestManagerDistributedTasks' -count=1
```

Expected: FAIL because routes/interface/handlers are not implemented.

- [ ] **Step 3: Add management interface methods**

In `internal/access/manager/server.go`, add to `Management`:

```go
// GetDistributedTasksSummary returns normalized read-only task counts across task domains.
GetDistributedTasksSummary(ctx context.Context) (managementusecase.DistributedTaskSummary, error)
// ListDistributedTasks returns one normalized read-only distributed task page.
ListDistributedTasks(ctx context.Context, query managementusecase.DistributedTaskQuery) (managementusecase.DistributedTaskListResult, error)
// GetDistributedTask returns one normalized read-only distributed task detail.
GetDistributedTask(ctx context.Context, domain managementusecase.DistributedTaskDomain, id string) (managementusecase.DistributedTaskDetail, error)
```

Update `managementStub` in `server_test.go` with matching fields and methods.

- [ ] **Step 4: Add cursor codec**

In `internal/access/manager/cursor_codec.go`, add a magic constant and encode/decode helpers:

```go
var distributedTaskCursorMagic = [...]byte{'W', 'K', 'D', 'T'}

func encodeDistributedTaskCursor(offset int) string {
    if offset <= 0 {
        return ""
    }
    var data [len(distributedTaskCursorMagic) + 1 + 4]byte
    copy(data[:], distributedTaskCursorMagic[:])
    data[len(distributedTaskCursorMagic)] = managerCursorVersion
    binary.BigEndian.PutUint32(data[len(distributedTaskCursorMagic)+1:], uint32(offset))
    return encodeCursorBase64(data[:])
}

func decodeDistributedTaskCursor(raw string) (int, error) {
    if raw == "" {
        return 0, nil
    }
    payload, err := base64.RawURLEncoding.DecodeString(raw)
    if err != nil {
        return 0, err
    }
    if len(payload) != len(distributedTaskCursorMagic)+1+4 || !hasCursorMagic(payload, distributedTaskCursorMagic) || payload[len(distributedTaskCursorMagic)] != managerCursorVersion {
        return 0, strconv.ErrSyntax
    }
    offset := binary.BigEndian.Uint32(payload[len(distributedTaskCursorMagic)+1:])
    return int(offset), nil
}
```

If using a filter hash to bind cursors to filters, include it in the payload and tests. Keep this first phase simple unless tests show cursor reuse risks.

- [ ] **Step 5: Implement handlers and DTO conversion**

Create `internal/access/manager/distributed_tasks.go`.

Include request parsing:

```go
func (s *Server) handleDistributedTasks(c *gin.Context)
func (s *Server) handleDistributedTasksSummary(c *gin.Context)
func (s *Server) handleDistributedTask(c *gin.Context)
func parseDistributedTaskQuery(c *gin.Context) (managementusecase.DistributedTaskQuery, error)
```

DTOs should include explicit `null` times for empty values:

```go
type DistributedTaskDTO struct {
    ID         string                  `json:"id"`
    Domain     string                  `json:"domain"`
    Kind       string                  `json:"kind"`
    Status     string                  `json:"status"`
    Phase      string                  `json:"phase"`
    Scope      DistributedTaskScopeDTO `json:"scope"`
    SourceNode uint64                  `json:"source_node"`
    TargetNode uint64                  `json:"target_node"`
    OwnerNode  uint64                  `json:"owner_node"`
    Attempt    uint32                  `json:"attempt"`
    NextRunAt  *time.Time             `json:"next_run_at"`
    CreatedAt  *time.Time             `json:"created_at"`
    UpdatedAt  *time.Time             `json:"updated_at"`
    LastError  string                  `json:"last_error"`
    Summary    string                  `json:"summary"`
    Links      map[string]string       `json:"links"`
}
```

Detail payload can use existing same-package DTO helpers:

```go
type DistributedTaskDetailPayloadDTO struct {
    Domain           string                     `json:"domain"`
    RawStatus        string                     `json:"raw_status"`
    Slot             *TaskDetailDTO            `json:"slot,omitempty"`
    NodeOnboarding   *nodeOnboardingJobDTO     `json:"node_onboarding,omitempty"`
    NodeScaleIn      *nodeScaleInReportDTO     `json:"node_scale_in,omitempty"`
    ChannelMigration *channelMigrationDetailDTO `json:"channel_migration,omitempty"`
}
```

- [ ] **Step 6: Register routes**

In `internal/access/manager/routes.go`, add a route group near existing `tasks` routes:

```go
distributedTasks := s.engine.Group("/manager")
if s.auth.enabled() {
    distributedTasks.Use(s.requirePermission("cluster.task", "r"))
}
distributedTasks.GET("/distributed-tasks/summary", s.handleDistributedTasksSummary)
distributedTasks.GET("/distributed-tasks", s.handleDistributedTasks)
distributedTasks.GET("/distributed-tasks/:domain/:id", s.handleDistributedTask)
```

Register `/summary` before the dynamic detail route.

- [ ] **Step 7: Run handler tests to verify GREEN**

Run:

```bash
go test ./internal/access/manager -run 'TestManagerDistributedTasks' -count=1
```

Expected: PASS.

- [ ] **Step 8: Run targeted backend tests**

Run:

```bash
go test ./internal/access/manager ./internal/usecase/management -count=1
```

Expected: PASS.

- [ ] **Step 9: Commit backend API**

Run:

```bash
git add internal/access/manager/distributed_tasks.go internal/access/manager/routes.go internal/access/manager/server.go internal/access/manager/server_test.go internal/access/manager/cursor_codec.go
git commit -m "feat: expose distributed task manager APIs"
```

Expected: commit succeeds.

---

## Task 4: Add Web API Types And Client

**Files:**
- Modify: `web/src/lib/manager-api.types.ts`
- Modify: `web/src/lib/manager-api.ts`
- Modify: `web/src/lib/manager-api.test.ts`

- [ ] **Step 1: Write failing API client tests**

Add to `web/src/lib/manager-api.test.ts`:

```ts
it("fetches distributed task summary, list, and detail", async () => {
  const summary = {
    total: 1,
    by_status: { pending: 0, running: 0, retrying: 1, blocked: 0, failed: 0, completed: 0, cancelled: 0, unknown: 0 },
    by_domain: { slot_reconcile: 1, node_onboarding: 0, node_scale_in: 0, channel_migration: 0 },
    partial: false,
    warnings: [],
  }
  const list = {
    total: 1,
    items: [{
      id: "1",
      domain: "slot_reconcile",
      kind: "repair",
      status: "retrying",
      phase: "catch_up",
      scope: { type: "slot", id: "1", slot_id: 1, channel_id: "", channel_type: 0, node_id: 0 },
      source_node: 0,
      target_node: 3,
      owner_node: 0,
      attempt: 1,
      next_run_at: null,
      created_at: null,
      updated_at: "2026-05-14T10:00:00Z",
      last_error: "",
      summary: "Slot 1 repair is retrying.",
      links: { slot: "/slots?slot_id=1" },
    }],
    next_cursor: "",
    has_more: false,
    partial: false,
    warnings: [],
  }
  const detail = { task: list.items[0], detail: { domain: "slot_reconcile", raw_status: "retrying", slot: null } }

  fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(summary), { status: 200 }))
  fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(list), { status: 200 }))
  fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(detail), { status: 200 }))

  await expect(getDistributedTasksSummary()).resolves.toEqual(summary)
  await expect(getDistributedTasks({ domain: "slot_reconcile", status: "retrying", nodeId: 3, scope: "slot", keyword: "repair", limit: 25, cursor: "abc" })).resolves.toEqual(list)
  await expect(getDistributedTask("slot_reconcile", "1")).resolves.toEqual(detail)

  expect(fetchMock).toHaveBeenNthCalledWith(1, "/manager/distributed-tasks/summary", expect.anything())
  expect(fetchMock).toHaveBeenNthCalledWith(2, "/manager/distributed-tasks?domain=slot_reconcile&status=retrying&node_id=3&scope=slot&keyword=repair&limit=25&cursor=abc", expect.anything())
  expect(fetchMock).toHaveBeenNthCalledWith(3, "/manager/distributed-tasks/slot_reconcile/1", expect.anything())
})
```

- [ ] **Step 2: Run API test to verify RED**

Run:

```bash
cd web && bun run test -- src/lib/manager-api.test.ts
```

Expected: FAIL because types/functions are missing.

- [ ] **Step 3: Add TypeScript types**

In `web/src/lib/manager-api.types.ts`, add:

```ts
export type ManagerDistributedTaskStatus = "pending" | "running" | "retrying" | "blocked" | "failed" | "completed" | "cancelled" | "unknown"
export type ManagerDistributedTaskDomain = "slot_reconcile" | "node_onboarding" | "node_scale_in" | "channel_migration"
export type ManagerDistributedTaskScopeType = "slot" | "node" | "channel" | "job"

export type DistributedTaskListParams = {
  domain?: ManagerDistributedTaskDomain
  status?: ManagerDistributedTaskStatus
  nodeId?: number
  scope?: ManagerDistributedTaskScopeType
  keyword?: string
  limit?: number
  cursor?: string
}

export type ManagerDistributedTaskScope = {
  type: ManagerDistributedTaskScopeType
  id: string
  slot_id: number
  channel_id: string
  channel_type: number
  node_id: number
}

export type ManagerDistributedTask = {
  id: string
  domain: ManagerDistributedTaskDomain
  kind: string
  status: ManagerDistributedTaskStatus
  phase: string
  scope: ManagerDistributedTaskScope
  source_node: number
  target_node: number
  owner_node: number
  attempt: number
  next_run_at: string | null
  created_at: string | null
  updated_at: string | null
  last_error: string
  summary: string
  links: Record<string, string>
}
```

Also add summary/list/detail response types. Detail payload can keep known source-specific fields optional and use existing imported types where available:

```ts
export type ManagerDistributedTaskDetailPayload = {
  domain: ManagerDistributedTaskDomain
  raw_status: string
  slot?: ManagerTaskDetailResponse | null
  node_onboarding?: ManagerNodeOnboardingJob | null
  node_scale_in?: ManagerNodeScaleInReport | null
  channel_migration?: Record<string, unknown> | null
}
```

If a precise channel migration type already exists or is added, use it instead of `Record<string, unknown>`.

- [ ] **Step 4: Add client functions**

In `web/src/lib/manager-api.ts`, import the new types and add:

```ts
function buildDistributedTasksPath(params?: DistributedTaskListParams) {
  const search = new URLSearchParams()
  if (params?.domain) search.set("domain", params.domain)
  if (params?.status) search.set("status", params.status)
  if (typeof params?.nodeId === "number") search.set("node_id", String(params.nodeId))
  if (params?.scope) search.set("scope", params.scope)
  if (params?.keyword !== undefined) search.set("keyword", params.keyword)
  if (typeof params?.limit === "number") search.set("limit", String(params.limit))
  if (params?.cursor) search.set("cursor", params.cursor)
  const query = search.toString()
  return query ? `/manager/distributed-tasks?${query}` : "/manager/distributed-tasks"
}

export function getDistributedTasksSummary() {
  return jsonManagerFetch<ManagerDistributedTasksSummaryResponse>("/manager/distributed-tasks/summary")
}

export function getDistributedTasks(params?: DistributedTaskListParams) {
  return jsonManagerFetch<ManagerDistributedTasksResponse>(buildDistributedTasksPath(params))
}

export function getDistributedTask(domain: ManagerDistributedTaskDomain, id: string) {
  return jsonManagerFetch<ManagerDistributedTaskDetailResponse>(`/manager/distributed-tasks/${domain}/${encodeURIComponent(id)}`)
}
```

- [ ] **Step 5: Run API test to verify GREEN**

Run:

```bash
cd web && bun run test -- src/lib/manager-api.test.ts
```

Expected: PASS.

- [ ] **Step 6: Commit web API client**

Run:

```bash
git add web/src/lib/manager-api.types.ts web/src/lib/manager-api.ts web/src/lib/manager-api.test.ts
git commit -m "feat: add distributed task web API client"
```

Expected: commit succeeds.

---

## Task 5: Build The Read-Only Tasks Page

**Files:**
- Create: `web/src/pages/tasks/page.tsx`
- Create: `web/src/pages/tasks/page.test.tsx`
- Modify: `web/src/app/router.tsx`
- Modify: `web/src/lib/navigation.ts`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`
- Modify: `web/src/pages/page-shells.test.tsx`

- [ ] **Step 1: Write failing page tests**

Create `web/src/pages/tasks/page.test.tsx`.

Use existing page test patterns from Dashboard/Topology. Mock the new API functions:

```tsx
import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeEach, expect, test, vi } from "vitest"

import { createAnonymousAuthState, useAuthStore } from "@/auth/auth-store"
import { resetLocale } from "@/i18n/locale-store"
import { I18nProvider } from "@/i18n/provider"
import { ManagerApiError } from "@/lib/manager-api"
import { TasksPage } from "@/pages/tasks/page"

const getDistributedTasksSummaryMock = vi.fn()
const getDistributedTasksMock = vi.fn()
const getDistributedTaskMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getDistributedTasksSummary: (...args: unknown[]) => getDistributedTasksSummaryMock(...args),
    getDistributedTasks: (...args: unknown[]) => getDistributedTasksMock(...args),
    getDistributedTask: (...args: unknown[]) => getDistributedTaskMock(...args),
  }
})

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  getDistributedTasksSummaryMock.mockReset()
  getDistributedTasksMock.mockReset()
  getDistributedTaskMock.mockReset()
  useAuthStore.setState({
    ...createAnonymousAuthState(),
    isHydrated: true,
    status: "authenticated",
    username: "admin",
    tokenType: "Bearer",
    accessToken: "token-1",
    expiresAt: "2099-05-14T12:00:00Z",
    permissions: [{ resource: "cluster.task", actions: ["r"] }],
  })
})

function renderTasksPage() {
  return render(
    <I18nProvider>
      <TasksPage />
    </I18nProvider>,
  )
}
```

Add tests:

- renders summary cards and rows from the mocked list.
- changing domain/status filters calls `getDistributedTasks` with params.
- keyword filter and refresh button reload the list.
- clicking “View detail” opens a `DetailSheet` with source-specific detail.
- partial warnings show a warning banner while rows still render.
- `ManagerApiError(403, ...)` maps to forbidden `ResourceState`.
- `ManagerApiError(503, ...)` maps to unavailable `ResourceState`.
- empty list shows no matching tasks copy.

- [ ] **Step 2: Run page tests to verify RED**

Run:

```bash
cd web && bun run test -- src/pages/tasks/page.test.tsx
```

Expected: FAIL because the page and route do not exist.

- [ ] **Step 3: Add route and navigation**

In `web/src/app/router.tsx`:

```tsx
import { TasksPage } from "@/pages/tasks/page"
// ...
{ path: "tasks", element: <TasksPage /> },
```

Place the route in Global Cluster near `nodes` and `slots`.

In `web/src/lib/navigation.ts`, import an icon such as `ListChecks` or `ClipboardList` from `lucide-react` and add:

```ts
{
  href: "/tasks",
  titleMessageId: "nav.tasks.title",
  descriptionMessageId: "nav.tasks.description",
  icon: ListChecks,
}
```

- [ ] **Step 4: Add i18n strings**

In `web/src/i18n/messages/en.ts`, add strings such as:

```ts
"nav.tasks.title": "Tasks",
"nav.tasks.description": "Read distributed task state.",
"tasks.title": "Distributed Tasks",
"tasks.description": "Read-only task center for cluster reconciliation, onboarding, scale-in, and channel migration work.",
"tasks.readOnly": "Read-only",
"tasks.summary.total": "Total",
"tasks.summary.running": "Running",
"tasks.summary.retrying": "Retrying",
"tasks.summary.failed": "Failed",
"tasks.summary.blocked": "Blocked",
"tasks.filters.domain": "Domain",
"tasks.filters.status": "Status",
"tasks.filters.scope": "Scope",
"tasks.filters.node": "Node ID",
"tasks.filters.keyword": "Keyword",
"tasks.actions.refresh": "Refresh",
"tasks.actions.viewDetail": "View detail",
"tasks.empty.title": "No distributed tasks",
"tasks.partialWarning": "Some task sources are unavailable. Showing partial results.",
```

Add matching `zh-CN` strings.

- [ ] **Step 5: Implement `TasksPage`**

Create `web/src/pages/tasks/page.tsx`.

Implementation guidance:

- Use `PageContainer`, `PageHeader`, `SectionCard`, `ResourceState`, `StatusBadge`, `TableToolbar`, and `DetailSheet` patterns already present in manager pages.
- Load summary and list in parallel on initial render.
- Keep filters in local state; call list with filters when filter values change or refresh is clicked.
- Keep summary global for all tasks; do not recompute summary from the current filtered page.
- Avoid write buttons.
- Render unknown values with `-`.
- Detail sheet calls `getDistributedTask(task.domain, task.id)` only when opened.

Useful local helpers:

```tsx
function formatTaskScope(task: ManagerDistributedTask) {
  if (task.scope.type === "slot") return `Slot ${task.scope.slot_id}`
  if (task.scope.type === "node") return `Node ${task.scope.node_id}`
  if (task.scope.type === "channel") return `${task.scope.channel_type}/${task.scope.channel_id}`
  return task.scope.id || "-"
}

function formatNodePair(task: ManagerDistributedTask) {
  if (!task.source_node && !task.target_node) return "-"
  return `${task.source_node || "-"} -> ${task.target_node || "-"}`
}

function formatTaskTime(task: ManagerDistributedTask) {
  return task.next_run_at ?? task.updated_at ?? task.created_at ?? "-"
}
```

- [ ] **Step 6: Add shell tests**

Update `web/src/pages/page-shells.test.tsx` to include:

```ts
["/tasks", "Tasks", "Distributed Tasks"],
```

and the Chinese equivalent.

- [ ] **Step 7: Run page tests to verify GREEN**

Run:

```bash
cd web && bun run test -- src/pages/tasks/page.test.tsx src/pages/page-shells.test.tsx
```

Expected: PASS.

- [ ] **Step 8: Commit tasks page**

Run:

```bash
git add web/src/pages/tasks/page.tsx web/src/pages/tasks/page.test.tsx web/src/app/router.tsx web/src/lib/navigation.ts web/src/i18n/messages/en.ts web/src/i18n/messages/zh-CN.ts web/src/pages/page-shells.test.tsx
git commit -m "feat: add distributed tasks page"
```

Expected: commit succeeds.

---

## Task 6: Update Documentation

**Files:**
- Modify: `web/README.md`
- Modify: `docs/raw/web-admin-restructure.md`

- [ ] **Step 1: Update web API matrix**

In `web/README.md`, add `/tasks` to the Page And API Matrix:

```markdown
| `/tasks` | `GET /manager/distributed-tasks/summary`, `GET /manager/distributed-tasks`, `GET /manager/distributed-tasks/:domain/:id` | Implemented |
```

- [ ] **Step 2: Update admin restructure notes**

In `docs/raw/web-admin-restructure.md`:

- Add “任务中心 /tasks” under Global Cluster or Diagnostics.
- Note that the MVP is read-only and aggregates Slot reconcile, onboarding, scale-in, and channel migration states.
- Keep write operations as follow-ups.

- [ ] **Step 3: Verify docs diff**

Run:

```bash
git diff -- web/README.md docs/raw/web-admin-restructure.md
```

Expected: docs only describe implemented read-only APIs and no write controls.

- [ ] **Step 4: Commit docs**

Run:

```bash
git add web/README.md docs/raw/web-admin-restructure.md
git commit -m "docs: document distributed task center"
```

Expected: commit succeeds.

---

## Task 7: Final Verification

**Files:**
- No new files unless previous steps reveal required docs updates.

- [ ] **Step 1: Run targeted Go tests**

Run:

```bash
go test ./internal/access/manager ./internal/usecase/management -count=1
```

Expected: PASS.

- [ ] **Step 2: Run targeted web tests**

Run:

```bash
cd web && bun run test -- src/lib/manager-api.test.ts src/pages/tasks/page.test.tsx src/pages/page-shells.test.tsx
```

Expected: PASS.

- [ ] **Step 3: Run web build**

Run:

```bash
cd web && bun run build
```

Expected: PASS.

- [ ] **Step 4: Run broader unit tests if backend interfaces changed**

If `ChannelMigrationStore`, `ClusterReader`, `Management`, or app wiring changed beyond manager handlers, run:

```bash
go test ./internal/... ./pkg/slot/... ./pkg/cluster/... -count=1
```

Expected: PASS. If this is too slow, record the exact packages that were skipped and why.

- [ ] **Step 5: Inspect git diff and status**

Run:

```bash
git status --short
git log --oneline --max-count=8
```

Expected: only intended committed changes on the feature branch.

- [ ] **Step 6: Do not claim completion without evidence**

Before final response, report exact commands run and their outcomes. If any command failed or was skipped, say so explicitly.
