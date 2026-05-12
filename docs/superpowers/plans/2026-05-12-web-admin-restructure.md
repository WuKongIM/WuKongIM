# Web Admin Restructure Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn `docs/raw/web-admin-restructure.md` into incremental, testable backend and frontend slices for the WuKongIM web manager, starting with the missing Channel Cluster operations view.

**Status:** P0 read path completed on 2026-05-12. Verification: focused backend tests, focused frontend tests, frontend build, and broader `GOWORK=off go test ./internal/... ./pkg/... -count=1` passed.

**Architecture:** Keep the completed React shell and route regrouping. Add manager-facing backend capabilities only under `internal/usecase/management` and `internal/access/manager`, wire dependencies from `internal/app`, and keep `web/` pages as thin API consumers. Do not introduce a generic service layer or any standalone non-cluster deployment branch; single-node deployments remain single-node clusters.

**Tech Stack:** Go 1.23, Gin manager API, existing management usecase layer, React 19, TypeScript, React Router DOM 7, Tailwind CSS 4, shadcn/Radix, Recharts, react-intl, Vitest, Bun/Vite.

---

## References

- Source requirement: `docs/raw/web-admin-restructure.md`
- Current shell/routing: `web/src/app/router.tsx`, `web/src/lib/navigation.ts`
- Current manager API client: `web/src/lib/manager-api.ts`, `web/src/lib/manager-api.types.ts`
- Existing channel runtime manager endpoints: `internal/usecase/management/channel_runtime_meta.go`, `internal/access/manager/channel_runtime_meta.go`
- Existing web channel list implementation: `web/src/pages/channels/page.tsx`, re-exported by `web/src/pages/channel-cluster/list/page.tsx`
- Existing dashboard implementation: `web/src/pages/dashboard/page.tsx`
- Follow `@superpowers:test-driven-development` for each implementation slice.
- Run `@superpowers:verification-before-completion` before claiming an implementation slice is complete.
- Re-check for `FLOW.md` before editing packages. At plan time, no `FLOW.md` existed under `web/`, `internal/access/manager/`, `internal/usecase/management/`, or `internal/app/`.

## Scope And Execution Strategy

This restructure spans several independent subsystems. Do not implement it as one large PR. Execute it in this order:

1. **P0 Channel Cluster read path** — channel health summary, unhealthy list, frontend pages, dashboard card.
2. **P0.5 Channel Cluster operations** — replica progress and repair/transfer actions after a safe backend runtime port is available.
3. **P1 Business management foundation** — storage/proxy scan primitives for users and business channels, then manager APIs and pages.
4. **P1 Settings and system users** — permissions page and manager-scoped system UID wrapper.
5. **P2 Monitoring and webhooks** — polling-first realtime monitor, then persistent webhook configuration.

The detailed executable steps below cover P0. Treat P1/P2 as follow-up plans after P0 lands, because they require new storage/index or config persistence contracts.

## Current State Summary

Already done:

- Six navigation groups exist in `web/src/lib/navigation.ts`.
- Routes exist for `/channel-cluster`, `/channel-cluster/list`, `/channel-cluster/unhealthy`, `/users`, `/channels-biz`, `/system-users`, `/monitor`, `/settings/permissions`, and `/settings/webhooks`.
- `/channels` redirects to `/channel-cluster/list`.
- `web/src/pages/channel-cluster/list/page.tsx` reuses `ChannelsPage` and already consumes `/manager/channel-runtime-meta`.
- Existing manager API covers overview, nodes, slots, tasks, connections, network, diagnostics, channel runtime metadata, messages, and message retention.

Important gaps:

- `web/src/pages/channel-cluster/page.tsx` and `web/src/pages/channel-cluster/unhealthy/page.tsx` are placeholders.
- `web/src/pages/dashboard/page.tsx` does not show channel-cluster health.
- There is no first-class `/manager/channel-cluster/*` API.
- User and business-channel list pages need storage scan primitives before they can be accurate and paged.
- Manager permission configuration is static; a writeable permissions UI needs a config persistence design before implementation.

## P0 File Structure

Backend:

- Create: `internal/usecase/management/channel_cluster.go` — aggregate channel runtime metadata into health summary and unhealthy pages.
- Create: `internal/usecase/management/channel_cluster_test.go` — usecase tests for health predicates, summary counts, leader distribution, unhealthy reasons, cursor paging, and invalid limits.
- Modify: `internal/usecase/management/app.go` — no new dependency required for read-only P0; add exported request/response comments if types live there instead of the new file.
- Create: `internal/access/manager/channel_cluster.go` — HTTP DTOs and handlers for `/manager/channel-cluster/summary` and `/manager/channel-cluster/unhealthy`.
- Modify: `internal/access/manager/routes.go` — register channel-cluster read routes under `cluster.channel` read permission.
- Modify: `internal/access/manager/server.go` — extend `Management` with the new read methods.
- Modify: `internal/access/manager/server_test.go` or create `internal/access/manager/channel_cluster_test.go` — endpoint JSON and permission tests.

Frontend:

- Modify: `web/src/lib/manager-api.types.ts` — add channel-cluster summary and unhealthy DTOs.
- Modify: `web/src/lib/manager-api.ts` — add `getChannelClusterSummary` and `getChannelClusterUnhealthy` wrappers.
- Modify: `web/src/lib/manager-api.test.ts` — lock URLs, query parameters, and error mapping for the new wrappers.
- Create: `web/src/pages/channel-cluster/page.test.tsx` — page tests for summary metrics and leader distribution.
- Modify: `web/src/pages/channel-cluster/page.tsx` — replace placeholder with real summary UI.
- Create: `web/src/pages/channel-cluster/unhealthy/page.test.tsx` — page tests for unhealthy tags, pagination, empty state, and error state.
- Modify: `web/src/pages/channel-cluster/unhealthy/page.tsx` — replace placeholder with unhealthy table.
- Modify: `web/src/pages/dashboard/page.test.tsx` — assert channel health card and refresh behavior.
- Modify: `web/src/pages/dashboard/page.tsx` — load and render channel-cluster summary.
- Modify: `web/src/i18n/messages/en.ts` — add P0 English copy.
- Modify: `web/src/i18n/messages/zh-CN.ts` — add P0 Chinese copy.
- Modify: `web/README.md` — update implemented page/API matrix.

Docs:

- Modify: `docs/raw/web-admin-restructure.md` — update “当前实现状态” after P0 lands.

## Task 1: Add Channel Cluster Health Aggregation In The Management Usecase

**Files:**
- Create: `internal/usecase/management/channel_cluster.go`
- Create: `internal/usecase/management/channel_cluster_test.go`

- [x] **Step 1: Write a failing summary test**

Add `TestGetChannelClusterSummaryAggregatesHealth` in `internal/usecase/management/channel_cluster_test.go`. Build a `fakeChannelRuntimeMetaReader` with four channels:

- active leader with `len(ISR) >= MinISR` → healthy,
- active leader with `len(ISR) < MinISR` → `isr_insufficient`,
- active channel with `Leader == 0` → `no_leader`,
- deleting channel → unhealthy by non-active status.

Assert:

```go
require.Equal(t, ChannelClusterSummary{
    Total:           4,
    Healthy:         1,
    ISRInsufficient: 1,
    NoLeader:        1,
    AvgReplicas:     2,
    AvgISR:          1.25,
    LeaderDistribution: []ChannelLeaderDistribution{
        {NodeID: 1, Count: 2},
        {NodeID: 2, Count: 1},
    },
}, got)
```

Keep the exact expected values aligned with the fixtures you create.

- [x] **Step 2: Run the usecase test to verify RED**

Run:

```bash
GOWORK=off go test ./internal/usecase/management -run TestGetChannelClusterSummaryAggregatesHealth -count=1
```

Expected: FAIL because `GetChannelClusterSummary` and the DTOs do not exist.

- [x] **Step 3: Implement the minimal summary usecase**

In `internal/usecase/management/channel_cluster.go`, add documented exported DTOs:

```go
type ChannelClusterSummary struct {
    // Total is the number of channel runtime records scanned across all physical slots.
    Total int
    // Healthy counts active channels with a leader and enough in-sync replicas.
    Healthy int
    // ISRInsufficient counts channels whose in-sync replica count is below MinISR.
    ISRInsufficient int
    // NoLeader counts channels with no current leader.
    NoLeader int
    // AvgReplicas is the average configured replica count across scanned channels.
    AvgReplicas float64
    // AvgISR is the average in-sync replica count across scanned channels.
    AvgISR float64
    // LeaderDistribution counts channels led by each non-zero node ID.
    LeaderDistribution []ChannelLeaderDistribution
}

type ChannelLeaderDistribution struct {
    // NodeID is the channel leader node ID.
    NodeID uint64
    // Count is the number of scanned channels led by NodeID.
    Count int
}
```

Implement `func (a *App) GetChannelClusterSummary(ctx context.Context) (ChannelClusterSummary, error)` by scanning every `a.cluster.SlotIDs()` in ascending order with `a.channelRuntimeMeta.ScanChannelRuntimeMetaSlotPage`. Use a small internal page limit constant such as `channelClusterScanPageLimit = 200` and loop until each slot returns `done == true`.

- [x] **Step 4: Re-run the summary test to verify GREEN**

Run:

```bash
GOWORK=off go test ./internal/usecase/management -run TestGetChannelClusterSummaryAggregatesHealth -count=1
```

Expected: PASS.

- [x] **Step 5: Write failing unhealthy list tests**

Add tests for:

- `ListChannelClusterUnhealthy` filters `ISR < MinISR`, `Leader == 0`, and `Status != active`.
- returned reasons are stable strings: `isr_insufficient`, `no_leader`, `status_not_active`.
- pagination uses the existing `ChannelRuntimeMetaListCursor` shape and returns `HasMore` plus `NextCursor`.
- invalid `Limit <= 0` returns `metadb.ErrInvalidArgument`.

- [x] **Step 6: Implement unhealthy list DTOs and filtering**

Add documented DTOs:

```go
type ChannelClusterUnhealthyItem struct {
    ChannelRuntimeMeta
    // Reasons describes why the channel is considered unhealthy.
    Reasons []string
}

type ListChannelClusterUnhealthyRequest struct {
    // Limit is the maximum unhealthy items to return.
    Limit int
    // Cursor resumes scanning from a previous response.
    Cursor ChannelRuntimeMetaListCursor
}

type ListChannelClusterUnhealthyResponse struct {
    // Items contains unhealthy channel rows.
    Items []ChannelClusterUnhealthyItem
    // HasMore reports whether another page exists.
    HasMore bool
    // NextCursor identifies the next scan position.
    NextCursor ChannelRuntimeMetaListCursor
}
```

Reuse the same scan order as `ListChannelRuntimeMeta`; only emit items where `channelClusterUnhealthyReasons(item)` is non-empty.

- [x] **Step 7: Run all management channel tests**

Run:

```bash
GOWORK=off go test ./internal/usecase/management -run 'Test(GetChannelCluster|ListChannelCluster|ListChannelRuntimeMeta)' -count=1
```

Expected: PASS.

- [x] **Step 8: Commit the usecase slice**

Run:

```bash
git add internal/usecase/management/channel_cluster.go internal/usecase/management/channel_cluster_test.go
git commit -m "feat: add channel cluster health aggregation"
```

## Task 2: Expose Channel Cluster Manager HTTP Read APIs

**Files:**
- Modify: `internal/access/manager/server.go`
- Modify: `internal/access/manager/routes.go`
- Create: `internal/access/manager/channel_cluster.go`
- Test: `internal/access/manager/server_test.go` or `internal/access/manager/channel_cluster_test.go`

- [x] **Step 1: Write failing endpoint tests**

Add tests for:

- `GET /manager/channel-cluster/summary` returns `total`, `healthy`, `isr_insufficient`, `no_leader`, `avg_replicas`, `avg_isr`, and `leader_distribution`.
- `GET /manager/channel-cluster/unhealthy?limit=2` returns `items`, `has_more`, and `next_cursor`.
- read routes require `cluster.channel` `r` when manager auth is enabled.
- invalid `limit` or `cursor` returns `400`.

Use the existing `managementStub` pattern in `internal/access/manager/server_test.go`; extend it with the two new methods.

- [x] **Step 2: Run endpoint tests to verify RED**

Run:

```bash
GOWORK=off go test ./internal/access/manager -run 'TestManagerChannelCluster' -count=1
```

Expected: FAIL because the handlers and routes do not exist.

- [x] **Step 3: Extend the manager `Management` interface**

In `internal/access/manager/server.go`, add English comments for:

```go
GetChannelClusterSummary(ctx context.Context) (managementusecase.ChannelClusterSummary, error)
ListChannelClusterUnhealthy(ctx context.Context, req managementusecase.ListChannelClusterUnhealthyRequest) (managementusecase.ListChannelClusterUnhealthyResponse, error)
```

- [x] **Step 4: Add handlers and DTO mapping**

Create `internal/access/manager/channel_cluster.go` with:

- `ChannelClusterSummaryResponse`,
- `ChannelLeaderDistributionDTO`,
- `ChannelClusterUnhealthyListResponse`,
- `ChannelClusterUnhealthyDTO`,
- `handleChannelClusterSummary`,
- `handleChannelClusterUnhealthy`,
- DTO conversion helpers.

Reuse `parseChannelRuntimeMetaLimit`, `encodeChannelRuntimeMetaCursor`, `decodeChannelRuntimeMetaCursor`, and `channelRuntimeMetaDTO` where possible.

- [x] **Step 5: Register routes**

In `internal/access/manager/routes.go`, register:

```go
channelCluster := s.engine.Group("/manager")
if s.auth.enabled() {
    channelCluster.Use(s.requirePermission("cluster.channel", "r"))
}
channelCluster.GET("/channel-cluster/summary", s.handleChannelClusterSummary)
channelCluster.GET("/channel-cluster/unhealthy", s.handleChannelClusterUnhealthy)
```

Do not register repair or leader-transfer routes in P0. They need a separate safe runtime write port and should not be exposed as fake buttons.

- [x] **Step 6: Re-run endpoint tests**

Run:

```bash
GOWORK=off go test ./internal/access/manager -run 'TestManagerChannelCluster' -count=1
```

Expected: PASS.

- [x] **Step 7: Run focused backend regression tests**

Run:

```bash
GOWORK=off go test ./internal/usecase/management ./internal/access/manager -count=1
```

Expected: PASS.

- [x] **Step 8: Commit the manager API slice**

Run:

```bash
git add internal/access/manager/server.go internal/access/manager/routes.go internal/access/manager/channel_cluster.go internal/access/manager/*channel_cluster*_test.go internal/access/manager/server_test.go
git commit -m "feat: expose channel cluster manager APIs"
```

## Task 3: Add Frontend Manager API Contracts

**Files:**
- Modify: `web/src/lib/manager-api.types.ts`
- Modify: `web/src/lib/manager-api.ts`
- Modify: `web/src/lib/manager-api.test.ts`

- [x] **Step 1: Write failing API client tests**

Add tests asserting:

```ts
await getChannelClusterSummary()
// fetch called with "/manager/channel-cluster/summary"

await getChannelClusterUnhealthy({ limit: 50, cursor: "abc" })
// fetch called with "/manager/channel-cluster/unhealthy?limit=50&cursor=abc"
```

Also assert `ManagerApiError` mapping still works for `503` from the summary endpoint.

- [x] **Step 2: Run client tests to verify RED**

Run:

```bash
cd web && bun run test src/lib/manager-api.test.ts
```

Expected: FAIL because wrappers do not exist.

- [x] **Step 3: Add DTO types and wrappers**

In `manager-api.types.ts`, add types matching backend snake_case JSON:

```ts
export type ManagerChannelClusterSummaryResponse = {
  total: number
  healthy: number
  isr_insufficient: number
  no_leader: number
  avg_replicas: number
  avg_isr: number
  leader_distribution: Array<{ node_id: number; count: number }>
}

export type ManagerChannelClusterUnhealthyItem = ManagerChannelRuntimeMeta & {
  reasons: string[]
}

export type ManagerChannelClusterUnhealthyResponse = {
  items: ManagerChannelClusterUnhealthyItem[]
  has_more: boolean
  next_cursor?: string
}

export type ChannelClusterUnhealthyParams = {
  limit?: number
  cursor?: string
}
```

In `manager-api.ts`, add:

```ts
export function getChannelClusterSummary() {
  return jsonManagerFetch<ManagerChannelClusterSummaryResponse>("/manager/channel-cluster/summary")
}

export function getChannelClusterUnhealthy(params: ChannelClusterUnhealthyParams = {}) {
  const search = new URLSearchParams()
  if (typeof params.limit === "number") search.set("limit", String(params.limit))
  if (params.cursor) search.set("cursor", params.cursor)
  const query = search.toString()
  return jsonManagerFetch<ManagerChannelClusterUnhealthyResponse>(
    query ? `/manager/channel-cluster/unhealthy?${query}` : "/manager/channel-cluster/unhealthy",
  )
}
```

- [x] **Step 4: Re-run client tests**

Run:

```bash
cd web && bun run test src/lib/manager-api.test.ts
```

Expected: PASS.

- [x] **Step 5: Commit frontend API contract slice**

Run:

```bash
git add web/src/lib/manager-api.ts web/src/lib/manager-api.types.ts web/src/lib/manager-api.test.ts
git commit -m "feat: add channel cluster api client"
```

## Task 4: Implement Channel Cluster Summary Page

**Files:**
- Create: `web/src/pages/channel-cluster/page.test.tsx`
- Modify: `web/src/pages/channel-cluster/page.tsx`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`

- [x] **Step 1: Write failing page tests**

Mock `getChannelClusterSummary` and assert the page renders:

- total channels,
- healthy channels,
- ISR insufficient channels,
- no-leader channels,
- average replicas / average ISR,
- leader distribution rows or chart labels,
- refresh button calls the API again,
- `403` renders the shared forbidden state,
- empty summary renders zero-state copy.

- [x] **Step 2: Run page test to verify RED**

Run:

```bash
cd web && bun run test src/pages/channel-cluster/page.test.tsx
```

Expected: FAIL because the page is still a placeholder.

- [x] **Step 3: Replace the placeholder with real UI**

Use existing shell components, not a new design system:

- `PageContainer` and `PageHeader` for title, description, and refresh action.
- compact metric cards for `total`, `healthy`, `isr_insufficient`, `no_leader`, `avg_replicas`, `avg_isr`.
- Recharts pie or a simple table for `leader_distribution`; prefer a table if the result is empty or very small.
- Link unhealthy counts to `/channel-cluster/unhealthy`.
- Use `ResourceState` for loading, forbidden, unavailable, error, and empty states.

- [x] **Step 4: Add i18n copy**

Add stable IDs such as:

- `channelCluster.metric.total`
- `channelCluster.metric.healthy`
- `channelCluster.metric.isrInsufficient`
- `channelCluster.metric.noLeader`
- `channelCluster.metric.avgReplicas`
- `channelCluster.metric.avgIsr`
- `channelCluster.leaderDistribution.title`
- `channelCluster.unhealthyLink`

- [x] **Step 5: Re-run page tests**

Run:

```bash
cd web && bun run test src/pages/channel-cluster/page.test.tsx
```

Expected: PASS.

- [x] **Step 6: Commit the summary page slice**

Run:

```bash
git add web/src/pages/channel-cluster/page.tsx web/src/pages/channel-cluster/page.test.tsx web/src/i18n/messages/en.ts web/src/i18n/messages/zh-CN.ts
git commit -m "feat: implement channel cluster summary page"
```

## Task 5: Implement Unhealthy Channel Page

**Files:**
- Create: `web/src/pages/channel-cluster/unhealthy/page.test.tsx`
- Modify: `web/src/pages/channel-cluster/unhealthy/page.tsx`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`

- [x] **Step 1: Write failing unhealthy page tests**

Mock `getChannelClusterUnhealthy` and assert:

- rows show channel ID/type, slot, leader, replicas, ISR, MaxMessageSeq, status, and reason tags,
- `Load more` calls the API with the returned cursor,
- empty list renders a healthy empty state,
- refresh clears pagination and reloads first page,
- error states map through `ManagerApiError` as other manager pages do.

- [x] **Step 2: Run page test to verify RED**

Run:

```bash
cd web && bun run test src/pages/channel-cluster/unhealthy/page.test.tsx
```

Expected: FAIL because the page is still a placeholder.

- [x] **Step 3: Replace the placeholder with a table-first page**

Implement local state with `{ pages, nextCursor, hasMore, loading, refreshing, loadingMore, error }`. Use the same table grammar as `web/src/pages/channels/page.tsx`:

- reason tags through `StatusBadge` or a small page-local badge helper,
- `Inspect` link to `/channel-cluster/list?channel_id=...` if the list page supports the query, otherwise keep a disabled action with a TODO comment,
- no repair/transfer buttons in P0.

- [x] **Step 4: Add i18n copy for reasons**

Add IDs such as:

- `channelCluster.unhealthy.reason.isrInsufficient`
- `channelCluster.unhealthy.reason.noLeader`
- `channelCluster.unhealthy.reason.statusNotActive`
- `channelCluster.unhealthy.emptyTitle`
- `channelCluster.unhealthy.emptyDescription`

- [x] **Step 5: Re-run unhealthy page tests**

Run:

```bash
cd web && bun run test src/pages/channel-cluster/unhealthy/page.test.tsx
```

Expected: PASS.

- [x] **Step 6: Commit the unhealthy page slice**

Run:

```bash
git add web/src/pages/channel-cluster/unhealthy/page.tsx web/src/pages/channel-cluster/unhealthy/page.test.tsx web/src/i18n/messages/en.ts web/src/i18n/messages/zh-CN.ts
git commit -m "feat: implement unhealthy channel page"
```

## Task 6: Add Channel Health To Dashboard

**Files:**
- Modify: `web/src/pages/dashboard/page.tsx`
- Modify: `web/src/pages/dashboard/page.test.tsx`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`

- [x] **Step 1: Write failing dashboard tests**

Update the manager API mock to include `getChannelClusterSummary`. Add assertions that dashboard renders:

- channel total,
- channel healthy count,
- ISR insufficient count,
- no-leader count,
- a link to `/channel-cluster` or `/channel-cluster/unhealthy`,
- refresh calls `getChannelClusterSummary` again.

- [x] **Step 2: Run dashboard tests to verify RED**

Run:

```bash
cd web && bun run test src/pages/dashboard/page.test.tsx
```

Expected: FAIL because the dashboard does not call the new API.

- [x] **Step 3: Extend dashboard loading state**

In `DashboardState`, add `channelCluster: ManagerChannelClusterSummaryResponse | null`. Update `loadDashboard` to fetch:

```ts
const [overview, tasks, nodes, channelCluster] = await Promise.all([
  getOverview(),
  getTasks(),
  getNodes(),
  getChannelClusterSummary(),
])
```

Render a compact channel health card near existing operations metrics, linking to the channel cluster pages.

- [x] **Step 4: Re-run dashboard tests**

Run:

```bash
cd web && bun run test src/pages/dashboard/page.test.tsx
```

Expected: PASS.

- [x] **Step 5: Commit the dashboard slice**

Run:

```bash
git add web/src/pages/dashboard/page.tsx web/src/pages/dashboard/page.test.tsx web/src/i18n/messages/en.ts web/src/i18n/messages/zh-CN.ts
git commit -m "feat: show channel health on dashboard"
```

## Task 7: P0 Documentation And Verification

**Files:**
- Modify: `docs/raw/web-admin-restructure.md`
- Modify: `web/README.md`

- [x] **Step 1: Update docs**

In `docs/raw/web-admin-restructure.md`, mark P0 read APIs/pages as complete and keep P0.5 action APIs listed as pending. In `web/README.md`, update the page/API status matrix.

- [x] **Step 2: Run focused backend verification**

Run:

```bash
GOWORK=off go test ./internal/usecase/management ./internal/access/manager -count=1
```

Expected: PASS.

- [x] **Step 3: Run focused frontend verification**

Run:

```bash
cd web && bun run test src/lib/manager-api.test.ts src/pages/channel-cluster/page.test.tsx src/pages/channel-cluster/unhealthy/page.test.tsx src/pages/dashboard/page.test.tsx
```

Expected: PASS.

- [x] **Step 4: Run frontend build**

Run:

```bash
cd web && bun run build
```

Expected: PASS. Do not commit generated `web/dist` hash churn unless the repo convention explicitly requires it.

- [x] **Step 5: Run broader Go tests if backend interfaces changed widely**

Run:

```bash
GOWORK=off go test ./internal/... ./pkg/... -count=1
```

Expected: PASS, or record any unrelated pre-existing failures clearly.

- [x] **Step 6: Commit docs and verification notes**

Run:

```bash
git add docs/raw/web-admin-restructure.md web/README.md
git commit -m "docs: update web admin restructure status"
```

## P0.5 Follow-Up Plan: Channel Cluster Operations

Create a separate implementation plan before this slice. Required design decisions:

- **Replica progress:** current `ChannelRuntimeMeta` contains `Replicas` and `ISR`, and channel status exposes leader-side committed/HW values, but a manager-safe per-replica `commit_seq`/`lag` read port is not yet exposed. Add a narrow runtime/read interface rather than guessing follower lag from metadata.
- **Repair:** existing channel leader repair primitives live under `internal/runtime/channelmeta` and `internal/access/node`. Wire a manager operation through `internal/usecase/management` and `internal/app` only after deciding whether the manager calls the repairer directly or asks `channelMetaSync.RefreshChannelMeta` to repair by policy.
- **Explicit leader transfer:** the raw API asks for `POST /manager/channel-cluster/:type/:id/leader/transfer` with `target_node_id`; current repair logic chooses safe candidates and does not expose a direct target transfer command. Add this only after the runtime supports explicit safe transfer or change the API to “repair leader”.
- **Frontend actions:** keep buttons hidden or disabled until backend write routes are real and permission-protected by `cluster.channel` `w`.

Likely files for that plan:

- `internal/usecase/management/channel_cluster_operations.go`
- `internal/usecase/management/channel_cluster_operations_test.go`
- `internal/access/manager/channel_cluster_operations.go`
- `internal/access/manager/channel_cluster_operations_test.go`
- `internal/app/channel_cluster_operations.go`
- `web/src/lib/manager-api.ts`
- `web/src/pages/channel-cluster/unhealthy/page.tsx`
- `web/src/pages/channel-cluster/list/page.tsx` or `web/src/pages/channels/page.tsx`

## P1 Follow-Up Plan: Users And Business Channels

Create one plan for storage/index foundation and one plan for UI/API integration.

### Storage/API Foundation

User list and business channel list require paged authoritative scans. Add these before building pages:

- `pkg/slot/meta/user_page.go` and tests — page users by UID, with optional keyword prefix/contains strategy chosen explicitly.
- `pkg/slot/proxy/identity_user_page_rpc.go` and tests — route user pages to authoritative slot leaders.
- `pkg/slot/meta/channel_page.go` and tests — page channels by type and keyword. Existing `ListChannelsByChannelID` is point-lookup oriented and is not enough for `/manager/channels?type=x&keyword=...`.
- `pkg/slot/proxy/channel_page_rpc.go` and tests — authoritative channel page reads.
- Keep all new exported structs and important fields documented in English.

### Manager Users

Likely files:

- `internal/usecase/management/users.go`
- `internal/usecase/management/users_test.go`
- `internal/access/manager/users.go`
- `internal/access/manager/users_test.go`
- `internal/app/build.go`
- `web/src/pages/users/page.tsx`
- `web/src/pages/users/page.test.tsx`

Recommended first API subset:

- `GET /manager/users?keyword=xxx&limit=50&cursor=xxx`
- `GET /manager/users/:uid`
- `POST /manager/users/:uid/kick`

Do not implement `ban`, `unban`, or token reset until the user domain model has explicit persisted fields and semantics for them. A UI button without durable behavior is worse than no button.

### Manager Business Channels

Likely files:

- `internal/usecase/management/business_channels.go`
- `internal/usecase/management/business_channels_test.go`
- `internal/access/manager/business_channels.go`
- `internal/access/manager/business_channels_test.go`
- `internal/app/build.go`
- `web/src/pages/channels-biz/page.tsx`
- `web/src/pages/channels-biz/page.test.tsx`

Recommended first API subset:

- `GET /manager/channels?type=x&keyword=xxx&limit=50&cursor=xxx`
- `GET /manager/channels/:type/:id`
- `GET /manager/channels/:type/:id/subscribers?limit=50&cursor=xxx`
- `POST /manager/channels/:type/:id/subscribers/add`
- `POST /manager/channels/:type/:id/subscribers/remove`
- `GET /manager/channels/:type/:id/denylist`
- `GET /manager/channels/:type/:id/allowlist`

Use existing `internal/usecase/channel` mutation methods where possible; add missing read methods before adding frontend tables.

## P1 Follow-Up Plan: Permissions And System Users

### Permissions

Start read-only:

- `GET /manager/settings/permissions` returns configured manager users, roles/grants, and the permission resource catalog.
- Source the data from `accessmanager.AuthConfig` or a management-safe copy wired from `internal/app/build.go`.
- Page: `web/src/pages/settings/permissions/page.tsx` shows configured users and a resource/action matrix.

Only add write operations after a persistent manager-auth configuration store exists. If new config fields are introduced, update `wukongim.conf.example` with detailed English comments.

### System Users

Do not call legacy `/user/systemuids*` directly from the manager UI. Add manager-scoped wrappers so auth and permissions are consistent:

- `GET /manager/system-users`
- `POST /manager/system-users/add`
- `POST /manager/system-users/remove`

Wire these through existing `user.App` system UID methods and protect them with a suitable permission, preferably `cluster.channel` `w` initially or a new documented resource if the permission model is expanded.

## P2 Follow-Up Plan: Monitor And Webhooks

### Realtime Monitor

Start with polling before WebSocket:

- Use existing `getOverview`, `getNetworkSummary`, and `getConnections` data.
- Implement `web/src/pages/monitor/page.tsx` with a 5s polling interval, pause-on-unmount, and error backoff.
- Add tests with fake timers.
- Add WebSocket only after the polling dashboard proves the data shape.

### Webhooks

Requires a persistence design before UI writes:

- Define config/store fields for callback URL, enabled event types, retry policy, and recent delivery log retention.
- Update `internal/app/config.go`, config tests, and `wukongim.conf.example` if config-backed.
- Add manager APIs under `/manager/settings/webhooks` only after persistence is decided.

## Final Acceptance Criteria

P0 is complete when:

- `GET /manager/channel-cluster/summary` and `/manager/channel-cluster/unhealthy` are covered by usecase and HTTP tests.
- `/channel-cluster` displays real health metrics and leader distribution.
- `/channel-cluster/unhealthy` displays filtered unhealthy channels with reason tags and pagination.
- `/dashboard` includes a channel-cluster health summary and refreshes it with the rest of the dashboard.
- English and Chinese i18n copy are complete.
- `docs/raw/web-admin-restructure.md` and `web/README.md` reflect the new implementation status.
- Focused Go and web tests pass, and the frontend build passes.
