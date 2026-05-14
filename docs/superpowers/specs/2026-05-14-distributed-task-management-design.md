# Distributed Task Management Readonly Design

## Goal

Add a read-only distributed task management page to the manager web console. The page aggregates task and job state from the cluster control plane and related manager workflows so operators can inspect what the cluster is doing without leaving the web UI.

This first phase is visibility only. It must not introduce task retry, cancel, advance, start, or other write operations.

## Scope

In scope:

- Add a `/tasks` web page for distributed task status aggregation and detail inspection.
- Add manager read APIs that expose a unified task summary, task list, and task detail model.
- Cover current task sources where a safe read model already exists or can be added without changing task execution semantics:
  - Controller Slot reconcile tasks.
  - Node onboarding jobs.
  - Node scale-in derived state.
  - Channel migration tasks.
- Preserve existing `/manager/tasks` and `/manager/tasks/:slot_id` behavior for Dashboard and Slot detail compatibility.
- Support filtering by task domain, status, node, scope type, and keyword.
- Return partial results with warnings when one task source is temporarily unavailable.
- Keep deployment wording consistent with the project rule: single-node deployments are single-node clusters.

Out of scope:

- Mutating actions such as retry, cancel, advance, start, abort, repair, rebalance, or leader transfer.
- A new durable global task registry or historical audit table.
- Reworking existing task executors or task lifecycle state machines.
- Web polling, live push, or long-running subscriptions beyond manual refresh.
- Inferring unproven runtime fields. Unknown values stay empty, null, or `unknown`.

## Recommended Approach

Use a backend aggregation API and keep the web page thin.

Alternatives considered:

1. Let the web page call existing APIs directly.
   - Fast to build, but it leaks source-specific task models into the frontend and duplicates mapping logic.
2. Add a unified read-only manager aggregation API.
   - Recommended. It keeps source-specific normalization in `internal/usecase/management`, follows existing layering, and gives web one stable task model.
3. Build a durable global task registry.
   - Too heavy for this phase. It requires executor and storage changes before the read-only need is proven.

## Backend API

Add read-only endpoints under `/manager`:

```text
GET /manager/distributed-tasks/summary
GET /manager/distributed-tasks?domain=&status=&node_id=&scope=&keyword=&limit=&cursor=
GET /manager/distributed-tasks/:domain/:id
```

Use the existing permission resource:

```text
cluster.task:r
```

The existing `/manager/tasks` endpoints remain as the Slot reconcile compatibility API.

### Summary Response

```json
{
  "total": 12,
  "by_status": {
    "pending": 3,
    "running": 2,
    "retrying": 1,
    "blocked": 1,
    "failed": 1,
    "completed": 4,
    "cancelled": 0,
    "unknown": 0
  },
  "by_domain": {
    "slot_reconcile": 4,
    "node_onboarding": 2,
    "node_scale_in": 1,
    "channel_migration": 5
  },
  "partial": false,
  "warnings": []
}
```

### List Response

```json
{
  "total": 12,
  "items": [
    {
      "id": "slot-reconcile:12",
      "domain": "slot_reconcile",
      "kind": "repair",
      "status": "retrying",
      "phase": "catch_up",
      "scope": {
        "type": "slot",
        "id": "12",
        "slot_id": 12,
        "channel_id": "",
        "channel_type": 0,
        "node_id": 0
      },
      "source_node": 3,
      "target_node": 5,
      "owner_node": 1,
      "attempt": 2,
      "next_run_at": "2026-05-14T10:00:00Z",
      "created_at": "",
      "updated_at": "",
      "last_error": "catch up timeout",
      "summary": "Slot 12 repair is retrying on catch_up.",
      "links": {
        "slot": "/slots?slot_id=12"
      }
    }
  ],
  "next_cursor": "",
  "has_more": false,
  "partial": false,
  "warnings": []
}
```

### Warning Model

```json
{
  "domain": "channel_migration",
  "code": "source_unavailable",
  "message": "channel migration task source unavailable"
}
```

List and summary requests should return `200` with `partial=true` when at least one source succeeds. They should return `503 service_unavailable` only when no source can produce data.

### Detail Response

The detail response wraps the common task fields and adds a source-specific payload:

```json
{
  "task": {},
  "detail": {
    "domain": "slot_reconcile",
    "raw_status": "retrying",
    "slot": {}
  }
}
```

Source-specific detail fields should reuse existing manager DTOs where possible:

- Slot reconcile: existing `TaskDetailDTO` slot context.
- Node onboarding: existing onboarding job DTO.
- Node scale-in: existing scale-in report DTO.
- Channel migration: existing channel migration detail DTO.

## Task Domains

### Slot Reconcile

Domain: `slot_reconcile`

Source:

- `cluster.ListTasksStrict(ctx)` for list.
- `cluster.GetReconcileTaskStrict(ctx, slotID)` plus Slot assignment/runtime reads for detail.

This is the same task family currently exposed by `/manager/tasks`.

### Node Onboarding

Domain: `node_onboarding`

Source:

- Existing node onboarding job list and detail usecases.

List items should represent durable jobs, not every move. The current move and result counts are available in detail. Jobs with `planned` status map to `pending`; jobs with `running`, `failed`, `completed`, or `cancelled` map directly where possible.

### Node Scale-In

Domain: `node_scale_in`

Source:

- Derived from nodes that are draining or have active scale-in progress.
- Existing scale-in report methods provide checks, progress, runtime, blocked reasons, and next action.

Because scale-in is a manager-driven flow rather than a single durable task row, IDs can be stable derived IDs such as `node-scale-in:<node_id>`.

### Channel Migration

Domain: `channel_migration`

Source:

- Active channel leader-transfer and replica-migration tasks.
- Existing per-channel detail DTO should be reused for detail.

If the backend lacks a global active channel migration list, add a management-layer read path against the channel migration store. Do not make the frontend scan channels and call per-channel APIs.

## Status Normalization

The unified API exposes these frontend-facing statuses:

```text
pending
running
retrying
blocked
failed
completed
cancelled
unknown
```

Mapping rules live in `internal/usecase/management`:

- Slot reconcile:
  - `pending` -> `pending`
  - `retrying` -> `retrying`
  - `failed` -> `failed`
- Node onboarding:
  - `planned` -> `pending`
  - `running` -> `running`
  - `failed` -> `failed`
  - `completed` -> `completed`
  - `cancelled` -> `cancelled`
- Node scale-in:
  - derive from report status, next action, and active counters.
  - blocked safety checks map to `blocked`.
  - active migration/drain work maps to `running`.
- Channel migration:
  - durable retry wait maps to `retrying`.
  - executor-active phases map to `running`.
  - validation blockers map to `blocked`.
  - terminal status maps to `failed`, `completed`, or `cancelled`.

The detail payload keeps source raw status and phase so operators can see the exact task state.

## Backend Layering

Add files:

```text
internal/usecase/management/distributed_tasks.go
internal/access/manager/distributed_tasks.go
```

Optional tests can live next to those files.

Responsibilities:

- `internal/access/manager`
  - Parse query parameters and path IDs.
  - Enforce `cluster.task:r`.
  - Convert usecase DTOs to JSON DTOs.
  - Map `ErrNotFound`, bad query, source unavailable, and partial success responses.
- `internal/usecase/management`
  - Define the common distributed task model.
  - Implement source adapters and status mapping.
  - Aggregate, sort, filter, paginate, and collect warnings.
- `internal/app`
  - Continue as the only composition root.
  - Wire any extra store/runtime dependencies required by new read-only source adapters.

Do not introduce a new global service package. The task center is a management usecase.

A small internal interface is useful for tests and extensibility:

```go
type distributedTaskSource interface {
    domain() string
    list(ctx context.Context, query DistributedTaskQuery) (DistributedTaskSourcePage, error)
    get(ctx context.Context, id string) (DistributedTaskDetail, error)
}
```

This interface stays unexported unless another package needs it.

## Filtering, Sorting, And Pagination

Supported query fields:

- `domain`: one of the known domains, empty for all.
- `status`: normalized status, empty for all.
- `node_id`: matches source, target, owner, or scoped node.
- `scope`: `slot`, `node`, `channel`, or `job`.
- `keyword`: matches task ID, kind, phase, scope ID, channel ID, or last error.
- `limit`: bounded by the handler, default 50.
- `cursor`: opaque cursor produced by the usecase.

Initial sorting:

1. failed
2. blocked
3. retrying
4. running
5. pending
6. completed
7. cancelled
8. unknown

Within the same status group, sort by updated time when available, then domain, then ID. The sort should be deterministic even when timestamps are missing.

Pagination can be source-aggregated in memory for the first phase because task counts should be small. If task volume grows, the usecase can switch to source-level cursor fan-in without changing the web contract.

## Web Page

Add route:

```text
/tasks
```

Navigation placement: Global Cluster, because these are control-plane and distributed operations tasks rather than general diagnostics.

Page sections:

1. Header
   - Title: Distributed Tasks.
   - Read-only badge.
   - Short description that the page aggregates cluster task state.
2. Summary cards
   - Total.
   - Running.
   - Retrying.
   - Failed.
   - Blocked.
   - Partial warning state when present.
3. Filter toolbar
   - Domain select.
   - Status select.
   - Scope select.
   - Node ID input.
   - Keyword input.
   - Refresh button.
4. Task table
   - Domain.
   - Kind.
   - Scope.
   - Status.
   - Phase.
   - Source -> Target.
   - Attempt.
   - Next run or updated time.
   - Last error.
   - Detail action.
5. Detail sheet
   - Common task fields.
   - Impact scope.
   - Node relationship.
   - Source-specific detail block.
   - Links to existing pages such as Slot, Node, Channel, or Onboarding.

The page must not show write controls in this phase.

## Web Types And Client

Add types to `web/src/lib/manager-api.types.ts`:

- `ManagerDistributedTaskStatus`
- `ManagerDistributedTaskDomain`
- `ManagerDistributedTaskScope`
- `ManagerDistributedTask`
- `ManagerDistributedTasksSummaryResponse`
- `ManagerDistributedTasksResponse`
- `ManagerDistributedTaskDetailResponse`

Add client functions to `web/src/lib/manager-api.ts`:

```ts
getDistributedTasksSummary()
getDistributedTasks(query)
getDistributedTask(domain, id)
```

The query builder should omit empty filters and encode cursor/keyword safely.

## Error And Empty States

- Initial load shows the existing loading `ResourceState`.
- `403` maps to forbidden.
- `503` maps to unavailable.
- `400` shows the backend message near the filter toolbar.
- Partial responses render available rows plus a warning banner.
- Empty list renders an empty state that says no distributed tasks match the filters.
- Unknown or null fields render as `-`, not guessed values.

## Documentation

Update after implementation:

- `web/README.md`
  - Add `/tasks` to the page/API matrix.
- `docs/raw/web-admin-restructure.md`
  - Add Distributed Tasks under Global Cluster or Diagnostics and mark read-only MVP as implemented.
- `AGENTS.md`
  - Only update the directory tree if the implementation adds or moves top-level directories. This design should not require that.

No configuration changes are expected. If implementation later adds config, update `wukongim.conf.example` and include detailed English comments.

## Testing

Backend tests:

- `internal/usecase/management/distributed_tasks_test.go`
  - status mapping by source.
  - deterministic sorting.
  - filters for domain, status, node, scope, and keyword.
  - partial warnings when one source fails.
  - detail lookup per source.
- `internal/access/manager/distributed_tasks_test.go` or existing server tests.
  - auth and permission checks.
  - summary/list/detail happy paths.
  - invalid query handling.
  - partial list response.
  - `404` for missing detail.
  - `503` when all sources are unavailable.

Frontend tests:

- `web/src/lib/manager-api.test.ts`
  - fetches summary, list, and detail endpoints with encoded query parameters.
- `web/src/pages/tasks/page.test.tsx`
  - renders summary cards and task table.
  - filters and refreshes.
  - opens detail sheet.
  - shows partial warnings.
  - maps forbidden/unavailable errors.
  - renders empty state.
- `web/src/pages/page-shells.test.tsx`
  - verifies `/tasks` appears in the shell in English and Chinese.

Targeted verification:

```bash
go test ./internal/access/manager ./internal/usecase/management
cd web && bun run test -- src/lib/manager-api.test.ts src/pages/tasks/page.test.tsx src/pages/page-shells.test.tsx
```

Use full `go test ./...` only if the implementation touches shared runtime, cluster, or app wiring beyond manager read paths.

## Follow-ups

- Add task write actions after the read-only task model proves useful and each action has its own safety design.
- Add historical task audit storage if operators need completed task history beyond source-local retention.
- Add live refresh or server-sent events if manual refresh is not enough.
- Add batch operation task domains such as batch channel leader drain after those workflows exist.
