# Web Node Config Menu Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `/cluster/node-config` as a standalone single-node effective configuration browser under the cluster operations navigation.

**Architecture:** Reuse the existing manager API functions `getNodes()` and `getNodeConfig(nodeId)` with a new page-local React component. Add one route and one navigation item, keep node detail as a compact entry point to the canonical page, and keep all filtering/copy behavior client-side over the bounded config snapshot.

**Tech Stack:** React, React Router, React Intl, Vitest, Testing Library, Bun, existing shell components.

---

## File Structure

- Create `web/src/pages/node-config/page.tsx`
  - Owns page state, selected node URL state, config loading, client-side filters, copy behavior, and rendering.
- Create `web/src/pages/node-config/page.test.tsx`
  - Covers local-node default selection, query-param selection, filtering, group tabs, copy, and scoped errors.
- Modify `web/src/app/router.tsx`
  - Adds `/cluster/node-config`.
- Modify `web/src/lib/navigation.ts`
  - Adds the `配置` navigation item immediately before `诊断`.
- Modify `web/src/i18n/messages/en.ts`
  - Adds English navigation and page messages.
- Modify `web/src/i18n/messages/zh-CN.ts`
  - Adds Chinese navigation and page messages.
- Modify `web/src/pages/nodes/page.tsx`
  - Adds a canonical full-config link from the node detail config section.
- Modify `web/src/pages/nodes/page.test.tsx`
  - Asserts the node detail link targets `/cluster/node-config?node_id={node_id}`.
- Modify `web/src/pages/page-shells.test.tsx`
  - Adds route shell coverage for English, Chinese, and path-label tests.

## Task 1: Route, Navigation, And Shell Tests

**Files:**
- Modify: `web/src/pages/page-shells.test.tsx`
- Modify: `web/src/app/router.tsx`
- Modify: `web/src/lib/navigation.ts`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`
- Create: `web/src/pages/node-config/page.tsx`

- [x] **Step 1: Write the failing shell tests**

Add these cases:

```ts
["/cluster/node-config", "Node Config", "Effective configuration"],
["/cluster/node-config", "CLUSTER / CONFIG"],
["/cluster/node-config", "节点配置", "有效配置"],
```

- [x] **Step 2: Run shell tests to verify RED**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/page-shells.test.tsx
```

Expected: FAIL because `/cluster/node-config` has no route/navigation metadata.

- [x] **Step 3: Add minimal route and navigation implementation**

Add route:

```ts
import { NodeConfigPage } from "@/pages/node-config/page"
{ path: "cluster/node-config", element: <NodeConfigPage /> },
```

Add navigation item before `/cluster/diagnostics`:

```ts
{
  href: "/cluster/node-config",
  titleMessageId: "nav.nodeConfig.title",
  descriptionMessageId: "nav.nodeConfig.description",
  pathLabelMessageId: "nav.path.cluster.nodeConfig",
  icon: SlidersHorizontal,
},
```

Add minimal page:

```tsx
export function NodeConfigPage() {
  const intl = useIntl()
  return (
    <PageContainer>
      <PageHeader
        title={intl.formatMessage({ id: "nodeConfig.title" })}
        description={intl.formatMessage({ id: "nodeConfig.description" })}
      />
      <SectionCard title={intl.formatMessage({ id: "nodeConfig.config.title" })}>
        <ResourceState kind="empty" title={intl.formatMessage({ id: "nodeConfig.config.empty" })} />
      </SectionCard>
    </PageContainer>
  )
}
```

- [x] **Step 4: Run shell tests to verify GREEN**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/page-shells.test.tsx
```

Expected: PASS.

## Task 2: Node Config Page Behavior

**Files:**
- Create: `web/src/pages/node-config/page.test.tsx`
- Modify: `web/src/pages/node-config/page.tsx`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`

- [x] **Step 1: Write failing page behavior tests**

Cover these behaviors in one focused test file:

```ts
test("defaults to the local node and renders grouped effective config", async () => {
  renderNodeConfigPage()
  expect(await screen.findByRole("heading", { name: "Node Config" })).toBeInTheDocument()
  expect(await screen.findByText("node-1 · local")).toBeInTheDocument()
  expect(await screen.findByText("WK_CLUSTER_HASH_SLOT_COUNT")).toBeInTheDocument()
  expect(getNodeConfigMock).toHaveBeenCalledWith(1)
})

test("honors node_id query params and selects the matching node", async () => {
  renderNodeConfigPage("/cluster/node-config?node_id=2")
  expect(await screen.findByText("node-2")).toBeInTheDocument()
  expect(getNodeConfigMock).toHaveBeenCalledWith(2)
})

test("filters config rows by search and group tab", async () => {
  renderNodeConfigPage()
  await user.type(await screen.findByLabelText("Search config"), "jwt")
  expect(screen.getByText("WK_MANAGER_JWT_SECRET")).toBeInTheDocument()
  expect(screen.queryByText("WK_CLUSTER_HASH_SLOT_COUNT")).not.toBeInTheDocument()
  await user.click(screen.getByRole("tab", { name: "Cluster" }))
  expect(screen.getByText("No config values match this search.")).toBeInTheDocument()
})

test("copies the current filtered result with redacted values", async () => {
  const writeText = vi.fn().mockResolvedValue(undefined)
  Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } })
  renderNodeConfigPage()
  await user.type(await screen.findByLabelText("Search config"), "jwt")
  await user.click(screen.getByRole("button", { name: "Copy filtered result" }))
  expect(writeText).toHaveBeenCalledWith(expect.stringContaining("WK_MANAGER_JWT_SECRET"))
  expect(writeText).toHaveBeenCalledWith(expect.stringContaining("******"))
})

test("keeps node rail visible when selected node config fails", async () => {
  getNodeConfigMock.mockRejectedValueOnce(new ManagerApiError(503, "service_unavailable", "node config unavailable"))
  renderNodeConfigPage()
  expect(await screen.findByText("node-1 · local")).toBeInTheDocument()
  expect(await screen.findByText(/currently unavailable/i)).toBeInTheDocument()
})
```

- [x] **Step 2: Run page tests to verify RED**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/node-config/page.test.tsx
```

Expected: FAIL because the page has not implemented loading, selection, filters, or copy.

- [x] **Step 3: Implement the page behavior**

Implement:

```ts
type NodeConfigState = {
  nodes: ManagerNodesResponse | null
  selectedNodeId: number | null
  config: ManagerNodeConfigResponse | null
  loadingNodes: boolean
  loadingConfig: boolean
  nodeError: Error | null
  configError: Error | null
  nodeQuery: string
  configQuery: string
  activeGroup: string
  copied: boolean
}
```

Use derived helpers:

```ts
function filterConfigGroups(config: ManagerNodeConfigResponse | null, groupId: string, query: string) {
  const normalized = query.trim().toLowerCase()
  return (config?.groups ?? [])
    .filter((group) => groupId === "all" || group.id === groupId)
    .map((group) => ({
      ...group,
      items: group.items.filter((item) => (
        !normalized ||
        item.key.toLowerCase().includes(normalized) ||
        item.label.toLowerCase().includes(normalized) ||
        item.value.toLowerCase().includes(normalized)
      )),
    }))
    .filter((group) => group.items.length > 0)
}
```

Render:

- Node rail as `SectionCard` with node search and selected buttons.
- Summary strip with selected node, source, restart requirement, generated time.
- Config workbench with search input, `role="tablist"` group tabs, table sections, and copy button.
- Scoped `ResourceState` blocks for node and config errors.

- [x] **Step 4: Run page tests to verify GREEN**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/node-config/page.test.tsx
```

Expected: PASS.

## Task 3: Node Detail Deep Link

**Files:**
- Modify: `web/src/pages/nodes/page.test.tsx`
- Modify: `web/src/pages/nodes/page.tsx`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`

- [x] **Step 1: Write failing node detail link test**

Extend the existing config detail test:

```ts
const fullConfigLink = within(configSurface as HTMLElement).getByRole("link", { name: "View full config" })
expect(fullConfigLink).toHaveAttribute("href", "/cluster/node-config?node_id=1")
```

- [x] **Step 2: Run nodes tests to verify RED**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/nodes/page.test.tsx
```

Expected: FAIL because the link does not exist.

- [x] **Step 3: Add link to `NodeConfigSection`**

Add `nodeId` prop and render:

```tsx
<Button asChild size="xs" variant="outline">
  <Link to={`/cluster/node-config?node_id=${nodeId}`}>
    {intl.formatMessage({ id: "nodes.config.viewFull" })}
  </Link>
</Button>
```

- [x] **Step 4: Run nodes tests to verify GREEN**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/nodes/page.test.tsx
```

Expected: PASS.

## Task 4: Final Verification

**Files:**
- Verify all modified files.

- [x] **Step 1: Run focused tests**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/lib/manager-api.test.ts src/pages/node-config/page.test.tsx src/pages/nodes/page.test.tsx src/pages/page-shells.test.tsx
```

Expected: PASS.

- [x] **Step 2: Run TypeScript**

Run:

```bash
cd web && /Users/tt/.bun/bin/bunx tsc -b
```

Expected: PASS.

- [x] **Step 3: Run production build**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run build
```

Expected: PASS, allowing only the existing Vite chunk warning.

- [x] **Step 4: Check whitespace and generated churn**

Run:

```bash
git diff --check
git status --short
```

Expected: no whitespace errors. If only `web/dist/index.html` asset hashes changed, restore that generated hash churn.
