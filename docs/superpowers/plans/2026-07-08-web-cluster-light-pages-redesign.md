# Web Cluster Light Pages Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bring `/cluster/topology`, `/cluster/channels`, and `/cluster/diagnostics` into the approved editorial console visual language without changing data flow or retired-route behavior.

**Architecture:** Keep every change page-local and reuse existing `PageHeader`, `SectionCard`, `PageTabs`, `ResourceState`, table, and i18n primitives. Add stable DOM markers and accessible table labels only where they support the visual contract tests, and preserve topology loading/filtering, cluster-channel list-only behavior, and diagnostics trace-only normalization.

**Tech Stack:** Vite, React 19, TypeScript, Tailwind CSS v4, Vitest, Testing Library, Bun.

---

## File Structure

- Modify: `web/src/pages/topology/page.tsx`
  - Converts the topology summary cards into a compact strip.
  - Adds a named node topology surface and tighter node card radius.
  - Adds a named, accessible slot placement table surface.
- Test: `web/src/pages/topology/page.test.tsx`
  - Adds rendered-DOM assertions for the summary strip, node surface, and slot placement table surface.
- Modify: `web/src/pages/cluster/channels/page.tsx`
  - Wraps the existing `ChannelClusterListPanel` in a neutral named list surface.
- Test: `web/src/pages/cluster/channels/page.test.tsx`
  - Adds rendered-DOM assertions that the list panel is inside the named surface while retired tabs remain absent.
- Modify: `web/src/pages/cluster/diagnostics/page.tsx`
  - Wraps the existing trace tab and `DiagnosticsTracePanel` in a named trace surface.
- Test: `web/src/pages/cluster/diagnostics/page.test.tsx`
  - Adds rendered-DOM assertions that only the tracing tab and trace panel are inside the named surface.
- Check: `web/src/pages/page-shells.test.tsx`
  - Remains route smoke coverage. No source change is planned.
- Do not modify: backend manager API code, route definitions, auth rules, i18n message files, dashboard pages, `/cluster/nodes`, `/cluster/slots`, or `/cluster/monitor`.

## Task 1: Topology Operations Surface

**Files:**
- Modify: `web/src/pages/topology/page.tsx`
- Test: `web/src/pages/topology/page.test.tsx`

- [ ] **Step 1: Write the failing test**

Update the first import in `web/src/pages/topology/page.test.tsx`:

```tsx
import { render, screen, within } from "@testing-library/react"
```

Extend the existing `renders topology summary, nodes, and slot matrix` test by adding these assertions after the existing summary value assertions and before the node-name assertions:

```tsx
  const summaryStrip = screen.getByTestId("topology-summary-strip")
  expect(summaryStrip).toHaveClass("grid", "overflow-hidden", "rounded-md", "border", "border-border", "bg-card")
  expect(summaryStrip.querySelectorAll("[data-topology-summary-cell]")).toHaveLength(4)
  expect(summaryStrip.querySelector("[data-topology-summary-cell]")).not.toHaveClass("rounded-lg")
```

Add these assertions after the existing node-name assertions:

```tsx
  const nodeSurface = screen.getByText("wk-node-1").closest("[data-topology-surface='nodes']")
  expect(nodeSurface).toHaveClass("grid", "gap-3", "md:grid-cols-2", "xl:grid-cols-3")

  const nodeCard = screen.getByText("wk-node-1").closest("article")
  expect(nodeCard).toHaveClass("rounded-md", "border", "border-border", "bg-background")
```

Add these assertions after the existing `Slot Placement` assertion:

```tsx
  const slotTable = screen.getByRole("table", { name: "Slot Placement" })
  const slotSurface = slotTable.closest("[data-topology-surface='slot-placement']")
  expect(slotSurface).toHaveClass("overflow-x-auto", "rounded-md", "border", "border-border")
  expect(slotTable).toHaveClass("w-full", "border-collapse", "text-sm")
  expect(within(slotTable).getByText("Slot 1")).toBeInTheDocument()
```

The updated test body should still include these behavior assertions:

```tsx
  expect(screen.getByText("Controller leader: 1")).toBeInTheDocument()
  expect(screen.getByText("Nodes: 2")).toBeInTheDocument()
  expect(screen.getByText("Slots: 3")).toBeInTheDocument()
  expect(screen.getByText("Slot anomalies: 2")).toBeInTheDocument()
  expect(screen.getByText("wk-node-1")).toBeInTheDocument()
  expect(screen.getByText("wk-node-2")).toBeInTheDocument()
  expect(screen.getByText("Slot Placement")).toBeInTheDocument()
  expect(screen.getByText("Preferred leader 1")).toBeInTheDocument()
  expect(screen.getByText("quorum lost")).toBeInTheDocument()
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/topology/page.test.tsx -t "renders topology summary, nodes, and slot matrix"
```

Expected: FAIL because `topology-summary-strip`, `data-topology-summary-cell`, `data-topology-surface`, and the slot table accessible name are not present yet.

- [ ] **Step 3: Convert the topology summary cards into a compact strip**

In `web/src/pages/topology/page.tsx`, replace the current summary grid:

```tsx
<div className="grid gap-3 md:grid-cols-4">
  <div className="rounded-lg border border-border bg-muted/30 p-3 text-sm font-semibold text-foreground">
    {controllerLeaderID > 0
      ? intl.formatMessage({ id: "topology.controllerLeader" }, { id: controllerLeaderID })
      : intl.formatMessage({ id: "topology.controllerLeader.empty" })}
  </div>
  <div className="rounded-lg border border-border bg-muted/30 p-3 text-sm font-semibold text-foreground">
    {intl.formatMessage({ id: "topology.nodesValue" }, { count: nodes.total })}
  </div>
  <div className="rounded-lg border border-border bg-muted/30 p-3 text-sm font-semibold text-foreground">
    {intl.formatMessage({ id: "topology.slotsValue" }, { count: slots.total })}
  </div>
  <div className="rounded-lg border border-border bg-muted/30 p-3 text-sm font-semibold text-foreground">
    {intl.formatMessage({ id: "topology.anomaliesValue" }, { count: topologyAnomalyCount(overview) })}
  </div>
</div>
```

with:

```tsx
<div
  className="grid overflow-hidden rounded-md border border-border bg-card md:grid-cols-4"
  data-testid="topology-summary-strip"
>
  <div className="border-b border-border px-3 py-3 text-sm font-semibold text-foreground last:border-b-0 md:border-r md:border-b-0 md:last:border-r-0" data-topology-summary-cell="">
    {controllerLeaderID > 0
      ? intl.formatMessage({ id: "topology.controllerLeader" }, { id: controllerLeaderID })
      : intl.formatMessage({ id: "topology.controllerLeader.empty" })}
  </div>
  <div className="border-b border-border px-3 py-3 text-sm font-semibold text-foreground last:border-b-0 md:border-r md:border-b-0 md:last:border-r-0" data-topology-summary-cell="">
    {intl.formatMessage({ id: "topology.nodesValue" }, { count: nodes.total })}
  </div>
  <div className="border-b border-border px-3 py-3 text-sm font-semibold text-foreground last:border-b-0 md:border-r md:border-b-0 md:last:border-r-0" data-topology-summary-cell="">
    {intl.formatMessage({ id: "topology.slotsValue" }, { count: slots.total })}
  </div>
  <div className="border-b border-border px-3 py-3 text-sm font-semibold text-foreground last:border-b-0 md:border-r md:border-b-0 md:last:border-r-0" data-topology-summary-cell="">
    {intl.formatMessage({ id: "topology.anomaliesValue" }, { count: topologyAnomalyCount(overview) })}
  </div>
</div>
```

This preserves the exact visible summary values.

- [ ] **Step 4: Add the named node topology surface**

In `web/src/pages/topology/page.tsx`, replace the node-card grid opening:

```tsx
<div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
```

with:

```tsx
<div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3" data-topology-surface="nodes">
```

Then replace the node card article opening:

```tsx
<article className="rounded-lg border border-border bg-background p-4" key={node.node_id}>
```

with:

```tsx
<article className="rounded-md border border-border bg-background p-4" key={node.node_id}>
```

Do not change the node filter, node list mapping, node status badge, local/controller chips, slot summary, or runtime summary.

- [ ] **Step 5: Add the named slot placement table surface**

In `web/src/pages/topology/page.tsx`, replace the slot table wrapper and table opening:

```tsx
<div className="overflow-x-auto rounded-lg border border-border">
  <table className="w-full border-collapse">
```

with:

```tsx
<div
  className="overflow-x-auto rounded-md border border-border"
  data-topology-surface="slot-placement"
>
  <table
    aria-label={intl.formatMessage({ id: "topology.slotPlacement.title" })}
    className="w-full border-collapse text-sm"
  >
```

Do not change `filteredSlots`, filtered-slot count text, table headings, row values, or empty slot rendering.

- [ ] **Step 6: Run focused topology tests**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/topology/page.test.tsx
```

Expected: PASS. Existing tests still cover node filtering, forbidden/unavailable errors, and empty node/slot states.

- [ ] **Step 7: Commit topology page change**

Run:

```bash
git add web/src/pages/topology/page.tsx web/src/pages/topology/page.test.tsx
git commit -m "style(web): refine topology operations surface"
```

Expected: commit succeeds with only topology page and topology test changes.

## Task 2: Cluster Channels Entry Surface

**Files:**
- Modify: `web/src/pages/cluster/channels/page.tsx`
- Test: `web/src/pages/cluster/channels/page.test.tsx`

- [ ] **Step 1: Write the failing test**

Update the first import in `web/src/pages/cluster/channels/page.test.tsx`:

```tsx
import { render, screen, within } from "@testing-library/react"
```

In the existing `defaults to the channel cluster list without overview or unhealthy tabs` test, add these assertions after `expect(await screen.findByText("alpha")).toBeInTheDocument()`:

```tsx
  const listSurface = screen.getByText("alpha").closest("[data-cluster-channels-surface='list']")
  expect(listSurface).toHaveClass("space-y-4")
  expect(within(listSurface as HTMLElement).getByText("Channel ID")).toBeInTheDocument()
```

The test must keep these existing behavior assertions:

```tsx
  expect(screen.queryByRole("tab", { name: "Overview" })).not.toBeInTheDocument()
  expect(screen.queryByRole("tab", { name: "Unhealthy" })).not.toBeInTheDocument()
  expect(screen.queryByRole("tab", { name: "List" })).not.toBeInTheDocument()
  expect(getChannelRuntimeMetaMock).toHaveBeenCalledWith({
    nodeId: 1,
    limit: 50,
    includeMaxMessageSeq: true,
  })
  expect(getBusinessChannelsMock).not.toHaveBeenCalled()
  expect(getChannelClusterSummaryMock).not.toHaveBeenCalled()
  expect(getChannelClusterUnhealthyMock).not.toHaveBeenCalled()
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/cluster/channels/page.test.tsx -t "defaults to the channel cluster list without overview or unhealthy tabs"
```

Expected: FAIL because the `data-cluster-channels-surface="list"` wrapper is not present yet.

- [ ] **Step 3: Add the neutral named list wrapper**

In `web/src/pages/cluster/channels/page.tsx`, replace:

```tsx
<ChannelClusterListPanel messagesHref="/business/messages" />
```

with:

```tsx
<div className="space-y-4" data-cluster-channels-surface="list">
  <ChannelClusterListPanel messagesHref="/business/messages" />
</div>
```

Do not add a new border here. `ChannelClusterListPanel` already owns its internal rounded card and table shell.

- [ ] **Step 4: Run focused cluster channels tests**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/cluster/channels/page.test.tsx
```

Expected: PASS. Existing tests still prove retired overview/unhealthy/list tabs are absent and legacy `?tab=list` stays list-only.

- [ ] **Step 5: Commit cluster channels change**

Run:

```bash
git add web/src/pages/cluster/channels/page.tsx web/src/pages/cluster/channels/page.test.tsx
git commit -m "style(web): refine cluster channel entry surface"
```

Expected: commit succeeds with only cluster channels page and test changes.

## Task 3: Cluster Diagnostics Trace Surface

**Files:**
- Modify: `web/src/pages/cluster/diagnostics/page.tsx`
- Test: `web/src/pages/cluster/diagnostics/page.test.tsx`

- [ ] **Step 1: Write the failing test**

Update the first import in `web/src/pages/cluster/diagnostics/page.test.tsx`:

```tsx
import { render, screen, within } from "@testing-library/react"
```

In the existing `defaults to the diagnostics trace tab` test, replace the first tab assertion:

```tsx
expect(screen.getByRole("tab", { name: "Tracing" })).toHaveAttribute("aria-selected", "true")
```

with:

```tsx
const tracingTab = screen.getByRole("tab", { name: "Tracing" })
expect(tracingTab).toHaveAttribute("aria-selected", "true")
```

Then add these assertions after `expect(await screen.findByText("Message Diagnostics")).toBeInTheDocument()`:

```tsx
  const traceSurface = screen.getByText("Message Diagnostics").closest("[data-cluster-diagnostics-surface='trace']")
  expect(traceSurface).toHaveClass("space-y-4")
  expect(within(traceSurface as HTMLElement).getByRole("tab", { name: "Tracing" })).toHaveAttribute("aria-selected", "true")
  expect(within(traceSurface as HTMLElement).queryByRole("tab", { name: "Network" })).not.toBeInTheDocument()
```

The test must keep these existing retired-tab assertions:

```tsx
  expect(screen.queryByRole("tab", { name: "Network" })).not.toBeInTheDocument()
  expect(screen.queryByRole("tab", { name: "Control Plane Logs" })).not.toBeInTheDocument()
  expect(screen.queryByRole("tab", { name: "Slot Logs" })).not.toBeInTheDocument()
  expect(screen.queryByRole("tab", { name: "App Logs" })).not.toBeInTheDocument()
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/cluster/diagnostics/page.test.tsx -t "defaults to the diagnostics trace tab"
```

Expected: FAIL because the `data-cluster-diagnostics-surface="trace"` wrapper is not present yet.

- [ ] **Step 3: Add the named trace wrapper**

In `web/src/pages/cluster/diagnostics/page.tsx`, replace:

```tsx
<PageTabs
  activeTab={activeTab}
  className="px-0 pt-0"
  tabs={tabs.map((tab) => ({ id: tab.id, label: intl.formatMessage({ id: tab.labelMessageId }) }))}
  onTabChange={setTab}
/>
{activeTab === "trace" ? <DiagnosticsTracePanel /> : null}
```

with:

```tsx
<div className="space-y-4" data-cluster-diagnostics-surface="trace">
  <PageTabs
    activeTab={activeTab}
    className="px-0 pt-0"
    tabs={tabs.map((tab) => ({ id: tab.id, label: intl.formatMessage({ id: tab.labelMessageId }) }))}
    onTabChange={setTab}
  />
  {activeTab === "trace" ? <DiagnosticsTracePanel /> : null}
</div>
```

Do not add network, controller-log, slot-log, or app-log panels. Do not change `tabs`, `normalizeTab`, or `setTab`.

- [ ] **Step 4: Run focused cluster diagnostics tests**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/cluster/diagnostics/page.test.tsx
```

Expected: PASS. Existing tests still prove retired tab values normalize to tracing and retired APIs are not called.

- [ ] **Step 5: Commit cluster diagnostics change**

Run:

```bash
git add web/src/pages/cluster/diagnostics/page.tsx web/src/pages/cluster/diagnostics/page.test.tsx
git commit -m "style(web): refine diagnostics trace entry surface"
```

Expected: commit succeeds with only cluster diagnostics page and test changes.

## Task 4: Focused Verification And Build Hygiene

**Files:**
- Check: `web/src/pages/topology/page.test.tsx`
- Check: `web/src/pages/cluster/channels/page.test.tsx`
- Check: `web/src/pages/cluster/diagnostics/page.test.tsx`
- Check: `web/src/pages/page-shells.test.tsx`
- Check: `web/dist/index.html`

- [ ] **Step 1: Run the combined page test gate**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/topology/page.test.tsx src/pages/cluster/channels/page.test.tsx src/pages/cluster/diagnostics/page.test.tsx src/pages/page-shells.test.tsx
```

Expected: PASS for the three target page test files and route shell coverage.

- [ ] **Step 2: Run TypeScript project build**

Run:

```bash
cd web && /Users/tt/.bun/bin/bunx tsc -b
```

Expected: PASS with no TypeScript errors.

- [ ] **Step 3: Run production build**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run build
```

Expected: PASS. A Vite chunk-size warning is acceptable if the command exits with status 0.

- [ ] **Step 4: Restore build hash churn when it is the only dist change**

Inspect dist changes:

```bash
git diff -- web/dist/index.html
```

If the diff only changes `/assets/index-*.js` or `/assets/index-*.css` hash references, restore that file:

```bash
git restore web/dist/index.html
```

Expected: `web/dist/index.html` is clean unless the build produced a real checked-in asset change that must be reviewed with the user.

- [ ] **Step 5: Run whitespace and conflict-marker check**

Run:

```bash
git diff --check
```

Expected: no whitespace errors or conflict markers.

- [ ] **Step 6: Inspect final status**

Run:

```bash
git status --short
```

Expected: no uncommitted source changes from this plan. Pre-existing untracked files may remain:

```text
?? docs/superpowers/plans/2026-07-05-legacy-app-slot-store-adapter.md
?? web/DESIGN.md
```

- [ ] **Step 7: Report completion**

Report:

```text
Implemented the approved cluster light pages redesign for /cluster/topology, /cluster/channels, and /cluster/diagnostics. Focused page tests, route shell coverage, TypeScript build, production build, and git diff check passed. No backend API, route, auth, i18n semantic, dashboard, nodes, slots, monitor, retired-tab, or unrelated page changes were made.
```
