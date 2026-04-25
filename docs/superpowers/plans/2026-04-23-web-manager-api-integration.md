# Web Manager API Integration Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Connect the existing `web/` management console to the current manager read/write APIs so `Dashboard`, `Nodes`, `Slots`, and `Channels` show real data and operator actions work without changing the backend API contract.

**Architecture:** Keep `internal/access/manager/*` as the source-of-truth contract and concentrate frontend integration in a single manager API client plus page-local resource state. Allow a thin frontend DTO mapping layer where it improves component ergonomics, but lock tests to the real backend payloads so the UI never starts driving backend contract changes.

**Tech Stack:** React 19, React Router 7, TypeScript, Vite, Vitest, Testing Library, existing `web/` shell components, browser `fetch`, existing auth store/client wiring.

---

## References

- Spec: `docs/superpowers/specs/2026-04-22-web-manager-api-integration-design.md`
- Keep existing auth behavior from `docs/superpowers/specs/2026-04-22-web-manager-login-auth-design.md`
- Follow `@superpowers:test-driven-development` for every slice
- Run `@superpowers:verification-before-completion` before claiming implementation is done
- `web/` and `internal/access/manager/` currently have no `FLOW.md`; re-check before editing in case one appears

## Critical Review Notes Before Starting

- The user clarified the contract rule: frontend tables/details/actions must follow the existing backend APIs, but frontend-internal camelCase DTO mapping is allowed. Do not rewrite the spec back into a “raw snake_case everywhere” implementation.
- Default assumption: no Go code changes. Only touch `internal/access/manager/*` if a frontend test or manual verification proves the documented backend contract is not what the handlers currently return. If that happens, add/adjust the relevant Go test first and then make the smallest backend fix.
- Keep auth naming stable unless there is a concrete integration reason to touch it. This plan does not require changing `auth-store.ts` from camelCase to snake_case.
- Prefer page-local loading/error/action state over introducing a global async cache layer.
- Shared UI abstractions should stay small and manager-specific. Do not build a generic admin framework.

## File Structure

- Modify: `web/src/lib/manager-api.ts` — add endpoint wrappers for overview, nodes, slots, tasks, channels, and actions.
- Create: `web/src/lib/manager-api.types.ts` — define backend response/request shapes and optional thin frontend model helpers.
- Modify: `web/src/lib/manager-api.test.ts` — lock request URLs, methods, bodies, backend response payloads, and error/status handling.
- Create: `web/src/components/manager/resource-state.tsx` — shared loading/error/empty/forbidden/unavailable state renderer.
- Create: `web/src/components/manager/status-badge.tsx` — consistent badges for status/quorum/sync/action states.
- Create: `web/src/components/manager/detail-sheet.tsx` — shared right-side detail shell built on the existing sheet component.
- Create: `web/src/components/manager/key-value-list.tsx` — compact detail metadata renderer.
- Create: `web/src/components/manager/table-toolbar.tsx` — per-page refresh/action toolbar.
- Create: `web/src/components/manager/confirm-dialog.tsx` — confirmation dialog for node and slot actions.
- Create: `web/src/components/manager/action-form-dialog.tsx` — simple form dialog for transfer/recover flows.
- Create: `web/src/pages/dashboard/page.test.tsx` — overview/tasks rendering tests.
- Modify: `web/src/pages/dashboard/page.tsx` — replace placeholders with real overview/tasks UI.
- Create: `web/src/pages/nodes/page.test.tsx` — node list/detail/action tests.
- Modify: `web/src/pages/nodes/page.tsx` — nodes list, detail sheet, and draining/resume actions.
- Create: `web/src/pages/slots/page.test.tsx` — slot list/detail/action tests.
- Modify: `web/src/pages/slots/page.tsx` — slots list, detail sheet, transfer/recover/rebalance actions.
- Create: `web/src/pages/channels/page.test.tsx` — channel list/detail/pagination tests.
- Modify: `web/src/pages/channels/page.tsx` — channel runtime list, cursor paging, and detail sheet.
- Modify: `web/src/pages/connections/page.tsx` — explicit “no manager API yet” state.
- Modify: `web/src/pages/network/page.tsx` — explicit “no manager API yet” state.
- Modify: `web/src/pages/topology/page.tsx` — explicit “no manager API yet” state.
- Modify: `web/src/pages/page-shells.test.tsx` — update unsupported-page expectations and keep shell coverage.
- Modify: `web/src/app/layout/topbar.tsx` — make the global refresh button harmless when pages own their own refresh actions, or leave it visually stable if page-level refresh is sufficient.
- Modify: `web/src/app/layout/topbar.test.tsx` — update if topbar copy/behavior changes.
- Modify: `web/README.md` — describe which pages now use real manager APIs and which remain intentionally unimplemented.

### Task 1: Expand the manager API client contract without changing backend endpoints

**Files:**
- Create: `web/src/lib/manager-api.types.ts`
- Modify: `web/src/lib/manager-api.ts`
- Modify: `web/src/lib/manager-api.test.ts`

- [ ] **Step 1: Write the failing manager API contract tests**

Add focused tests for the new read/write wrappers before touching the client implementation. Cover at least:
- `getOverview()` calling `GET /manager/overview`
- `getNodes()` / `getNode(nodeId)`
- `markNodeDraining(nodeId)` and `resumeNode(nodeId)` using `POST`
- `getSlots()` / `getSlot(slotId)`
- `transferSlotLeader(slotId, { targetNodeId })` sending `{ "target_node_id": ... }`
- `recoverSlot(slotId, { strategy })` sending `{ "strategy": ... }`
- `rebalanceSlots()` calling `POST /manager/slots/rebalance`
- `getTasks()` / `getTask(slotId)`
- `getChannelRuntimeMeta({ limit, cursor })` preserving backend query names
- `getChannelRuntimeMetaDetail(channelType, channelId)`
- `403`, `404`, `409`, and `503` error mapping through `ManagerApiError`

Use raw backend payloads in the test fixtures, for example:

```ts
fetchMock.mockResolvedValue(
  new Response(
    JSON.stringify({
      total: 1,
      items: [{
        node_id: 1,
        addr: "127.0.0.1:7000",
        status: "alive",
        last_heartbeat_at: "2026-04-23T08:00:00Z",
        is_local: true,
        capacity_weight: 1,
        controller: { role: "leader" },
        slot_stats: { count: 3, leader_count: 2 },
      }],
    }),
    { status: 200 },
  ),
)
```

If you choose to return mapped camelCase models from helper functions, assert both the outgoing backend contract and the mapped return shape explicitly.

- [ ] **Step 2: Run the focused client tests to verify they fail**

Run: `cd web && bun run test -- src/lib/manager-api.test.ts`
Expected: FAIL because the wrappers/types/error handling for the new endpoints do not exist yet.

- [ ] **Step 3: Implement the minimal endpoint wrappers and backend contract types**

Add `manager-api.types.ts` with raw backend DTOs and only the smallest useful internal model helpers. A valid direction is:

```ts
export type ManagerNodeListResponse = {
  total: number
  items: Array<{
    node_id: number
    addr: string
    status: string
    last_heartbeat_at: string
    is_local: boolean
    capacity_weight: number
    controller: { role: string }
    slot_stats: { count: number; leader_count: number }
  }>
}

export async function getNodes() {
  const response = await managerFetch("/manager/nodes")
  return (await response.json()) as ManagerNodeListResponse
}
```

Only add mapping helpers where page code clearly benefits from them, and keep those helpers adjacent to the contract types so the boundary stays obvious.

- [ ] **Step 4: Re-run the focused client tests to verify they pass**

Run: `cd web && bun run test -- src/lib/manager-api.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit the client contract slice**

Run:

```bash
git add web/src/lib/manager-api.types.ts web/src/lib/manager-api.ts web/src/lib/manager-api.test.ts
git commit -m "feat: add web manager api client wrappers"
```

### Task 2: Add the small shared manager UI primitives needed by the data pages

**Files:**
- Create: `web/src/components/manager/resource-state.tsx`
- Create: `web/src/components/manager/status-badge.tsx`
- Create: `web/src/components/manager/detail-sheet.tsx`
- Create: `web/src/components/manager/key-value-list.tsx`
- Create: `web/src/components/manager/table-toolbar.tsx`
- Create: `web/src/components/manager/confirm-dialog.tsx`
- Create: `web/src/components/manager/action-form-dialog.tsx`
- Modify: `web/src/components/shell/shell-components.test.tsx`

- [ ] **Step 1: Write failing component tests for the reusable manager primitives**

Add a focused component test file or extend `shell-components.test.tsx` to lock the smallest stable behaviors:
- `ResourceState` renders loading, empty, forbidden, unavailable, and retry states distinctly
- `StatusBadge` renders different visual variants for at least `alive`, `quorum_lost`, and `failed`
- `DetailSheet` shows a title/description shell around provided children
- `ConfirmDialog` disables submit while `pending`
- `ActionFormDialog` shows fields, validation message area, and submit/cancel actions

Example:

```tsx
test("resource state renders forbidden copy", () => {
  render(<ResourceState kind="forbidden" title="Nodes" />)
  expect(screen.getByText(/permission/i)).toBeInTheDocument()
})
```

- [ ] **Step 2: Run the focused component tests to verify they fail**

Run: `cd web && bun run test -- src/components/shell/shell-components.test.tsx`
Expected: FAIL because the new manager components are not implemented yet.

- [ ] **Step 3: Implement the minimal reusable manager components**

Build only the primitives required by the page tasks. Keep each component presentational and stateless except for small controlled dialog wiring. Reuse the existing `Button`, `Sheet`, and other `components/ui` primitives where possible.

- [ ] **Step 4: Re-run the focused component tests to verify they pass**

Run: `cd web && bun run test -- src/components/shell/shell-components.test.tsx`
Expected: PASS.

- [ ] **Step 5: Commit the shared manager primitives**

Run:

```bash
git add web/src/components/manager web/src/components/shell/shell-components.test.tsx
git commit -m "feat: add manager ui primitives"
```

### Task 3: Implement the real dashboard overview and task summary page

**Files:**
- Create: `web/src/pages/dashboard/page.test.tsx`
- Modify: `web/src/pages/dashboard/page.tsx`

- [ ] **Step 1: Write the failing dashboard page tests**

Add tests that mock `getOverview()` and `getTasks()` and lock these behaviors:
- metrics render backend-driven counts
- anomaly sections show representative slot/task samples
- tasks summary/table renders `slot_id`, `kind`, `status`, `attempt`, and `last_error`
- `Refresh` triggers a re-fetch
- a `403` or `503` response shows the appropriate `ResourceState`

Example:

```tsx
test("renders overview metrics and task queue from manager APIs", async () => {
  getOverviewMock.mockResolvedValue({
    generated_at: "2026-04-23T08:00:00Z",
    cluster: { controller_leader_id: 1 },
    nodes: { total: 3, alive: 3, suspect: 0, dead: 0, draining: 1 },
    slots: { total: 64, ready: 63, quorum_lost: 1, leader_missing: 0, unreported: 0, peer_mismatch: 1, epoch_lag: 0 },
    tasks: { total: 2, pending: 1, retrying: 1, failed: 0 },
    anomalies: { ... },
  })
  getTasksMock.mockResolvedValue({ total: 1, items: [{ slot_id: 9, kind: "rebalance", step: "plan", status: "retrying", source_node: 1, target_node: 2, attempt: 3, next_run_at: null, last_error: "" }] })

  render(<DashboardPage />)

  expect(await screen.findByText("63")).toBeInTheDocument()
  expect(screen.getByText("rebalance")).toBeInTheDocument()
})
```

- [ ] **Step 2: Run the focused dashboard tests to verify they fail**

Run: `cd web && bun run test -- src/pages/dashboard/page.test.tsx`
Expected: FAIL because the dashboard still renders placeholders.

- [ ] **Step 3: Implement the minimal dashboard data UI**

Replace placeholders with:
- real metric cards backed by `overview`
- a compact operations summary section using the current backend counters
- anomaly lists using `overview.anomalies`
- a task queue table/summary powered by `getTasks()`
- page-local `loading`, `refreshing`, and error states

Keep the layout recognizable so the shell does not visually regress.

- [ ] **Step 4: Re-run the focused dashboard tests to verify they pass**

Run: `cd web && bun run test -- src/pages/dashboard/page.test.tsx`
Expected: PASS.

- [ ] **Step 5: Commit the dashboard slice**

Run:

```bash
git add web/src/pages/dashboard/page.tsx web/src/pages/dashboard/page.test.tsx
git commit -m "feat: connect dashboard to manager overview"
```

### Task 4: Implement the nodes page with detail sheet and draining/resume actions

**Files:**
- Create: `web/src/pages/nodes/page.test.tsx`
- Modify: `web/src/pages/nodes/page.tsx`

- [ ] **Step 1: Write the failing nodes page tests**

Add tests that mock `getNodes()`, `getNode()`, `markNodeDraining()`, and `resumeNode()` and cover:
- list rendering from backend node fields
- opening the detail sheet via `Inspect`
- rendering `slots.hosted_ids` and `slots.leader_ids`
- confirming `draining` and refreshing list + detail after success
- confirming `resume` and refreshing list + detail after success
- showing forbidden/unavailable states when the backend returns `403`/`503`

Example:

```tsx
test("opens node detail and refreshes after draining", async () => {
  getNodesMock.mockResolvedValue({ total: 1, items: [nodeRow] })
  getNodeMock.mockResolvedValue(nodeDetail)
  markNodeDrainingMock.mockResolvedValue(nodeDetailAfterDrain)

  render(<NodesPage />)

  await user.click(await screen.findByRole("button", { name: /inspect/i }))
  expect(await screen.findByText(/hosted ids/i)).toBeInTheDocument()

  await user.click(screen.getByRole("button", { name: /draining/i }))
  await user.click(screen.getByRole("button", { name: /confirm/i }))

  expect(markNodeDrainingMock).toHaveBeenCalledWith(1)
  expect(getNodesMock).toHaveBeenCalledTimes(2)
})
```

- [ ] **Step 2: Run the focused nodes page tests to verify they fail**

Run: `cd web && bun run test -- src/pages/nodes/page.test.tsx`
Expected: FAIL because the page is still placeholder-only.

- [ ] **Step 3: Implement the minimal nodes page**

Add:
- page-local load/refresh logic for `getNodes()`
- list UI for the important node columns
- a detail sheet that lazily loads `getNode(nodeId)`
- action buttons gated by the current permission list and wired to confirm dialogs
- post-action refresh of the list plus the open detail sheet, if any

Use a thin presentational mapping helper only if it materially simplifies the component.

- [ ] **Step 4: Re-run the focused nodes page tests to verify they pass**

Run: `cd web && bun run test -- src/pages/nodes/page.test.tsx`
Expected: PASS.

- [ ] **Step 5: Commit the nodes slice**

Run:

```bash
git add web/src/pages/nodes/page.tsx web/src/pages/nodes/page.test.tsx
git commit -m "feat: connect node manager views"
```

### Task 5: Implement the slots page with detail sheet and operator actions

**Files:**
- Create: `web/src/pages/slots/page.test.tsx`
- Modify: `web/src/pages/slots/page.tsx`

- [ ] **Step 1: Write the failing slots page tests**

Add tests that mock `getSlots()`, `getSlot()`, `transferSlotLeader()`, `recoverSlot()`, and `rebalanceSlots()` and cover:
- slot list rendering from backend list data
- opening detail to show assignment/runtime/task fields
- leader transfer dialog posting `target_node_id`
- recover dialog posting `strategy`
- rebalance confirmation showing returned plan items
- refreshing the relevant data after each successful action
- rendering `409` conflict and `503` unavailable errors distinctly

- [ ] **Step 2: Run the focused slots page tests to verify they fail**

Run: `cd web && bun run test -- src/pages/slots/page.test.tsx`
Expected: FAIL because the page still renders placeholders.

- [ ] **Step 3: Implement the minimal slots page**

Build:
- a real table based on `getSlots()`
- summary cards from list/overview-compatible slot states
- a detail sheet from `getSlot(slotId)`
- a transfer form dialog, recover form dialog, and rebalance confirm/result flow
- page-local refresh/error handling after each action

Keep request bodies in backend field names even if the page stores form state in camelCase.

- [ ] **Step 4: Re-run the focused slots page tests to verify they pass**

Run: `cd web && bun run test -- src/pages/slots/page.test.tsx`
Expected: PASS.

- [ ] **Step 5: Commit the slots slice**

Run:

```bash
git add web/src/pages/slots/page.tsx web/src/pages/slots/page.test.tsx
git commit -m "feat: connect slot manager views"
```

### Task 6: Implement the channels page with cursor pagination and detail drill-in

**Files:**
- Create: `web/src/pages/channels/page.test.tsx`
- Modify: `web/src/pages/channels/page.tsx`

- [ ] **Step 1: Write the failing channels page tests**

Add tests that mock `getChannelRuntimeMeta()` and `getChannelRuntimeMetaDetail()` and cover:
- first-page rendering from backend list data
- `has_more` / `next_cursor` driven pagination without invented page numbers
- opening detail by `channel_type` + `channel_id`
- not-found/unavailable detail states when the backend returns `404` or `503`

Example list fixture:

```ts
{
  items: [{
    channel_id: "u1@u2",
    channel_type: 1,
    slot_id: 9,
    channel_epoch: 7,
    leader_epoch: 3,
    leader: 2,
    replicas: [1, 2, 3],
    isr: [2, 3],
    min_isr: 2,
    status: "active",
  }],
  has_more: true,
  next_cursor: "cursor-2",
}
```

- [ ] **Step 2: Run the focused channels page tests to verify they fail**

Run: `cd web && bun run test -- src/pages/channels/page.test.tsx`
Expected: FAIL because the page is still placeholder-only.

- [ ] **Step 3: Implement the minimal channels page**

Add:
- page-local first-page loading
- next/previous cursor state only as needed to support navigation
- a real table from backend runtime metadata
- a detail sheet from `getChannelRuntimeMetaDetail(channelType, channelId)`
- appropriate empty/not-found/unavailable states

Do not add generic client-side filters or sorting unless a test already requires them.

- [ ] **Step 4: Re-run the focused channels page tests to verify they pass**

Run: `cd web && bun run test -- src/pages/channels/page.test.tsx`
Expected: PASS.

- [ ] **Step 5: Commit the channels slice**

Run:

```bash
git add web/src/pages/channels/page.tsx web/src/pages/channels/page.test.tsx
git commit -m "feat: connect channel runtime views"
```

### Task 7: Replace unsupported runtime placeholders with explicit “API not available yet” states

**Files:**
- Modify: `web/src/pages/connections/page.tsx`
- Modify: `web/src/pages/network/page.tsx`
- Modify: `web/src/pages/topology/page.tsx`
- Modify: `web/src/pages/page-shells.test.tsx`
- Modify: `web/README.md`
- Modify: `web/src/app/layout/topbar.tsx`
- Modify: `web/src/app/layout/topbar.test.tsx`

- [ ] **Step 1: Write the failing shell/copy tests**

Update `page-shells.test.tsx` so the unsupported pages now assert explicit “manager API not wired yet” messaging instead of generic placeholder sections. If the topbar needs copy or button changes to avoid implying a global live refresh, add/update a matching topbar test first.

- [ ] **Step 2: Run the focused shell tests to verify they fail**

Run: `cd web && bun run test -- src/pages/page-shells.test.tsx src/app/layout/topbar.test.tsx`
Expected: FAIL because the current pages still advertise generic static placeholders.

- [ ] **Step 3: Implement the unsupported-page copy updates**

Replace misleading placeholder blocks with a clear explanation that:
- no corresponding manager API is currently exposed
- the page route is intentionally reserved
- future backend support can plug in without route changes

Only change the topbar if it materially improves consistency with page-local refresh behavior.

Update `web/README.md` to document:
- which routes now use real manager APIs
- which routes remain intentionally unimplemented
- that backend API contracts remain the source of truth

- [ ] **Step 4: Re-run the focused shell tests to verify they pass**

Run: `cd web && bun run test -- src/pages/page-shells.test.tsx src/app/layout/topbar.test.tsx`
Expected: PASS.

- [ ] **Step 5: Commit the unsupported-page slice**

Run:

```bash
git add web/src/pages/connections/page.tsx web/src/pages/network/page.tsx web/src/pages/topology/page.tsx web/src/pages/page-shells.test.tsx web/README.md web/src/app/layout/topbar.tsx web/src/app/layout/topbar.test.tsx
git commit -m "docs: clarify unsupported web manager pages"
```

### Task 8: Run full frontend verification and only then consider backend investigation

**Files:**
- Modify: none unless verification reveals a concrete bug

- [ ] **Step 1: Run the focused frontend suite for the changed slices**

Run:

```bash
cd web && bun run test -- \
  src/lib/manager-api.test.ts \
  src/pages/dashboard/page.test.tsx \
  src/pages/nodes/page.test.tsx \
  src/pages/slots/page.test.tsx \
  src/pages/channels/page.test.tsx \
  src/pages/page-shells.test.tsx \
  src/app/router.test.tsx \
  src/app/layout/topbar.test.tsx
```

Expected: PASS.

- [ ] **Step 2: Run the full frontend test suite**

Run: `cd web && bun run test`
Expected: PASS.

- [ ] **Step 3: Run a production build check**

Run: `cd web && bun run build`
Expected: PASS.

- [ ] **Step 4: Investigate backend only if a contract mismatch is proven**

If any frontend test or manual check proves the current Go handlers do not match the documented/expected API, stop and create a new micro-slice:
- add or update the relevant Go test in `internal/access/manager/*_test.go`
- run that Go test and watch it fail
- make the smallest handler/DTO fix
- rerun the focused Go test and the affected frontend test

If no mismatch is found, do not edit Go code.

- [ ] **Step 5: Commit only the fixes required by verification**

If verification required additional frontend or backend fixes, commit them with the smallest truthful message. If everything already passed after Task 7, do not create an extra commit here.

## Local Plan Review Checklist

- [ ] Every new behavior starts with a failing test
- [ ] Frontend pages consume existing backend capabilities instead of inventing new contract requirements
- [ ] CamelCase DTO mapping, if used, stays internal and is covered by tests against raw backend payloads
- [ ] `Dashboard`, `Nodes`, `Slots`, and `Channels` each own their local loading/error/action state
- [ ] `Connections`, `Network`, and `Topology` clearly state that no manager API exists yet
- [ ] No backend file changes happen unless a failing test proves a contract mismatch
- [ ] Final verification includes targeted tests, full frontend tests, and a production build
