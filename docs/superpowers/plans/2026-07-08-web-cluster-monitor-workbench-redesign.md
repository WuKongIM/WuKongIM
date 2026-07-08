# WuKongIM Web Cluster Monitor Workbench Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add stable named surface contracts and quiet editorial shell alignment to `/cluster/monitor` while preserving realtime monitor behavior.

**Architecture:** This is a frontend view-contract change only. The existing monitor model, API calls, category defaults, node filtering, time ranges, refresh behavior, chart rendering, and i18n copy stay unchanged while the major UI regions gain `data-cluster-monitor-surface` markers for tests and future maintenance.

**Tech Stack:** React, TypeScript, React Intl, Tailwind CSS, Vitest, React Testing Library, Bun.

---

## File Structure

- Modify: `web/src/pages/cluster-monitor/page.test.tsx`
  - Owns page-level contract tests for toolbar, snapshot, metric grid, loading state, disabled source state, unavailable source state, default `common` category, absent `All` category, selected node, time range, and refresh behavior.
- Modify: `web/src/pages/cluster-monitor/components/cluster-monitor-toolbar.tsx`
  - Adds the named toolbar surface marker to the existing toolbar section while keeping `data-monitor-toolbar="true"`.
- Modify: `web/src/pages/cluster-monitor/components/cluster-monitor-snapshot-strip.tsx`
  - Adds the named snapshot surface marker to the existing compact snapshot strip.
- Modify: `web/src/pages/cluster-monitor/components/cluster-monitor-card-grid.tsx`
  - Adds the named metric-grid surface marker to the existing responsive graph grid.
- Modify: `web/src/pages/cluster-monitor/page.tsx`
  - Adds named loading and source-state markers to existing status shells and aligns their radius with the editorial console surface language.
- Do not modify: `web/src/lib/manager-api.ts`, `web/src/lib/manager-api.types.ts`, `web/src/pages/cluster-monitor/metric-config.ts`, i18n files, routes, backend code, `cluster-dashboard`, or `business-dashboard`.

---

### Task 1: Cluster Monitor Surface Contract

**Files:**
- Modify: `web/src/pages/cluster-monitor/page.test.tsx`
- Modify: `web/src/pages/cluster-monitor/components/cluster-monitor-toolbar.tsx`
- Modify: `web/src/pages/cluster-monitor/components/cluster-monitor-snapshot-strip.tsx`
- Modify: `web/src/pages/cluster-monitor/components/cluster-monitor-card-grid.tsx`
- Modify: `web/src/pages/cluster-monitor/page.tsx`

- [ ] **Step 1: Write the failing surface-marker tests**

In `web/src/pages/cluster-monitor/page.test.tsx`, add this helper immediately after `renderClusterMonitorPage()`:

```ts
function clusterMonitorSurface(name: "toolbar" | "snapshot" | "metrics" | "loading" | "source-state") {
  return document.querySelector(`[data-cluster-monitor-surface="${name}"]`)
}
```

In `renders cluster monitor cards from realtime API data`, replace the existing toolbar and snapshot assertions:

```ts
expect(screen.getByLabelText(/category/i).closest("section")).toHaveAttribute("data-monitor-toolbar", "true")
expect(screen.getAllByTestId("cluster-monitor-snapshot-cell").length).toBeGreaterThan(0)
```

with this exact block:

```ts
const toolbar = screen.getByLabelText(/category/i).closest("section")
expect(toolbar).toHaveAttribute("data-monitor-toolbar", "true")
expect(toolbar).toHaveAttribute("data-cluster-monitor-surface", "toolbar")
expect(clusterMonitorSurface("snapshot")).toBeInTheDocument()
expect(clusterMonitorSurface("metrics")).toBeInTheDocument()
expect(screen.getAllByTestId("cluster-monitor-snapshot-cell").length).toBeGreaterThan(0)
expect(clusterMonitorSurface("loading")).not.toBeInTheDocument()
expect(clusterMonitorSurface("source-state")).not.toBeInTheDocument()
```

In `shows prometheus setup guidance when realtime monitor is disabled`, replace:

```ts
expect(await screen.findByText("Prometheus monitoring is not enabled")).toBeInTheDocument()
```

with this exact block:

```ts
const disabledTitle = await screen.findByText("Prometheus monitoring is not enabled")
expect(disabledTitle).toBeInTheDocument()
expect(disabledTitle.closest("section")).toHaveAttribute("data-cluster-monitor-surface", "source-state")
```

In `shows unavailable guidance for source errors and rejected requests`, replace:

```ts
expect(await screen.findByText("Prometheus is unavailable")).toBeInTheDocument()
expect(screen.getByText("dial tcp 127.0.0.1:9090: connect: connection refused")).toBeInTheDocument()
expect(screen.queryByTestId("cluster-monitor-metric-card")).not.toBeInTheDocument()
```

with this exact block:

```ts
const unavailableTitle = await screen.findByText("Prometheus is unavailable")
expect(unavailableTitle).toBeInTheDocument()
expect(unavailableTitle.closest("section")).toHaveAttribute("data-cluster-monitor-surface", "source-state")
expect(screen.getByText("dial tcp 127.0.0.1:9090: connect: connection refused")).toBeInTheDocument()
expect(screen.queryByTestId("cluster-monitor-metric-card")).not.toBeInTheDocument()
```

In the same test, replace:

```ts
expect(await screen.findByText("Prometheus is unavailable")).toBeInTheDocument()
expect(screen.getByText("manager api unavailable")).toBeInTheDocument()
```

with this exact block:

```ts
const rejectedTitle = await screen.findByText("Prometheus is unavailable")
expect(rejectedTitle).toBeInTheDocument()
expect(rejectedTitle.closest("section")).toHaveAttribute("data-cluster-monitor-surface", "source-state")
expect(screen.getByText("manager api unavailable")).toBeInTheDocument()
```

In `does not silently render preview fixture before the realtime API responds`, replace:

```ts
expect(screen.getByText("Loading cluster monitor data...")).toBeInTheDocument()
```

with this exact block:

```ts
const loadingText = screen.getByText("Loading cluster monitor data...")
expect(loadingText).toBeInTheDocument()
expect(loadingText.closest("section")).toHaveAttribute("data-cluster-monitor-surface", "loading")
```

- [ ] **Step 2: Run the focused tests and verify they fail for missing markers**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/cluster-monitor/page.test.tsx -t "renders cluster monitor cards|shows prometheus setup guidance|shows unavailable guidance|does not silently render"
```

Expected: FAIL. At least one assertion reports that `data-cluster-monitor-surface` is missing or has a different value.

- [ ] **Step 3: Add the toolbar marker**

In `web/src/pages/cluster-monitor/components/cluster-monitor-toolbar.tsx`, update the toolbar `<section>` to this exact opening tag:

```tsx
    <section
      className="flex flex-col gap-3 border-b border-border bg-background pb-4 lg:flex-row lg:items-center lg:justify-between"
      data-cluster-monitor-surface="toolbar"
      data-monitor-toolbar="true"
    >
```

- [ ] **Step 4: Add the snapshot strip marker**

In `web/src/pages/cluster-monitor/components/cluster-monitor-snapshot-strip.tsx`, update the snapshot strip opening tag to:

```tsx
    <section className="grid gap-2 sm:grid-cols-2 lg:grid-cols-4 xl:grid-cols-7" data-cluster-monitor-surface="snapshot">
```

Keep each snapshot cell unchanged:

```tsx
        <div className="border-b border-border px-1 py-3 sm:px-3" data-testid="cluster-monitor-snapshot-cell" key={entry.key}>
```

- [ ] **Step 5: Add the metric grid marker**

In `web/src/pages/cluster-monitor/components/cluster-monitor-card-grid.tsx`, update the metric grid opening tag to:

```tsx
    <section className="grid gap-3 md:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4" data-cluster-monitor-surface="metrics">
```

Keep metric card rendering unchanged:

```tsx
        <ClusterMonitorMetricCard card={card} key={card.key} />
```

- [ ] **Step 6: Add loading and source-state markers**

In `web/src/pages/cluster-monitor/page.tsx`, replace the loading state shell:

```tsx
    <section className="rounded-lg border border-border/80 bg-card/82 px-4 py-4 text-sm text-muted-foreground" role="status">
```

with:

```tsx
    <section
      className="rounded-md border border-border/80 bg-card/82 px-4 py-4 text-sm text-muted-foreground"
      data-cluster-monitor-surface="loading"
      role="status"
    >
```

In the same file, replace the source-state shell:

```tsx
    <section className="rounded-lg border border-border/80 bg-card/88 px-5 py-6 text-sm text-muted-foreground" role="status">
```

with:

```tsx
    <section
      className="rounded-md border border-border/80 bg-card/88 px-5 py-6 text-sm text-muted-foreground"
      data-cluster-monitor-surface="source-state"
      role="status"
    >
```

- [ ] **Step 7: Run focused tests and verify they pass**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/cluster-monitor/page.test.tsx
```

Expected: PASS. Existing assertions for first request `{ window: "15m", category: "common" }`, absent `All`, node filtering, category filtering, time range, manual refresh, auto refresh, source states, help buttons, byte formatting, and metric rendering continue to pass.

- [ ] **Step 8: Commit the surface contract change**

Run:

```bash
git status --short
git add web/src/pages/cluster-monitor/page.test.tsx \
  web/src/pages/cluster-monitor/components/cluster-monitor-toolbar.tsx \
  web/src/pages/cluster-monitor/components/cluster-monitor-snapshot-strip.tsx \
  web/src/pages/cluster-monitor/components/cluster-monitor-card-grid.tsx \
  web/src/pages/cluster-monitor/page.tsx
git commit -m "style(web): name cluster monitor surfaces"
```

Expected: commit succeeds and the existing unrelated untracked `docs/superpowers/plans/2026-07-05-legacy-app-slot-store-adapter.md` is not staged.

---

### Task 2: Verification And Build Closeout

**Files:**
- Verify: `web/src/pages/cluster-monitor/page.test.tsx`
- Verify: `web/src/pages/cluster-monitor/components/cluster-monitor-metric-card.test.ts`
- Verify: `web/src/pages/cluster-monitor/preview-data.test.ts`
- Verify: `web/src/pages/page-shells.test.tsx`
- Verify: `web/dist/index.html`

- [ ] **Step 1: Run the full focused monitor verification**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/cluster-monitor/page.test.tsx src/pages/cluster-monitor/components/cluster-monitor-metric-card.test.ts src/pages/cluster-monitor/preview-data.test.ts src/pages/page-shells.test.tsx
```

Expected: PASS. The page shell still renders `/cluster/monitor`, the metric card model tests still pass, and preview-data tests remain deterministic.

- [ ] **Step 2: Run TypeScript build verification**

Run:

```bash
cd web && /Users/tt/.bun/bin/bunx tsc -b
```

Expected: PASS with no TypeScript errors.

- [ ] **Step 3: Run production build verification**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run build
```

Expected: PASS. A Vite chunk-size warning is acceptable if the build exits successfully.

- [ ] **Step 4: Remove generated HTML hash churn when it is build-only**

Run:

```bash
git status --short web/dist/index.html
git diff -- web/dist/index.html
```

If the diff only changes hashed asset filenames generated by the build, run:

```bash
git restore -- web/dist/index.html
```

Expected: `web/dist/index.html` is clean after restore. If the file has a hand-authored source change, keep it and inspect why the build touched it.

- [ ] **Step 5: Run whitespace verification**

Run:

```bash
git diff --check
```

Expected: no whitespace errors.

- [ ] **Step 6: Inspect final local state**

Run:

```bash
git status --short --branch
git log --oneline -n 5
```

Expected: local `main` includes `style(web): name cluster monitor surfaces`, the plan/spec commits remain below it, and unrelated untracked files remain uncommitted.
