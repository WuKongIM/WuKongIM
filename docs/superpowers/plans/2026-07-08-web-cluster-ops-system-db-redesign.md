# Web Cluster Ops And System DB Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bring `/cluster/plugins`, `/cluster/workqueues`, and `/system/db` into the approved editorial console visual language without changing manager API behavior.

**Architecture:** Keep the redesign page-local and reuse existing shell, card, table, form, and i18n primitives. Add testable layout markers only where the current DOM lacks a stable semantic hook, and preserve all existing data loading, dialogs, filters, pagination, confirmation, and error mapping behavior.

**Tech Stack:** Vite, React 19, TypeScript, Tailwind CSS v4, Vitest, Testing Library, Bun.

---

## File Structure

- Modify: `web/src/pages/plugins/page.tsx`
  - Adds editorial toolbar/table surface classes and stable test attributes for plugin inventory and plugin bindings.
  - Uses existing i18n message IDs; no new strings.
- Test: `web/src/pages/plugins/page.test.tsx`
  - Adds a focused visual-contract test for the plugin inventory toolbar/table and binding toolbar/table.
- Modify: `web/src/pages/workqueues/page.tsx`
  - Converts the filter controls into a compact query toolbar, converts summary cards into a status strip, and adds a named workqueue table surface.
  - Uses existing i18n message IDs; no new strings.
- Test: `web/src/pages/workqueues/page.test.tsx`
  - Adds a focused visual-contract test for the toolbar, metadata row, status strip, and named table.
- Modify: `web/src/pages/db-inspect/page.tsx`
  - Converts the table list, query form, describe table, result table, and stats metadata into compact editorial surfaces.
  - Uses existing i18n message IDs; no new strings.
- Test: `web/src/pages/db-inspect/page.test.tsx`
  - Adds `within` import and a focused visual-contract test for the rail, query workbench, describe table, result table, and stats strip.
- Do not modify: `web/src/i18n/messages/en.ts`, `web/src/i18n/messages/zh-CN.ts`
  - This plan avoids new user-facing copy.
- Do not modify: backend manager API code, route definitions, auth rules, `cluster-dashboard`, `business-dashboard`, `/cluster/nodes`, `/cluster/slots`, `/cluster/channels`, `/cluster/topology`, `/cluster/diagnostics`, `/system/permissions`, `/system/webhooks`.

## Task 1: Plugin Operations Surfaces

**Files:**
- Modify: `web/src/pages/plugins/page.tsx`
- Test: `web/src/pages/plugins/page.test.tsx`

- [ ] **Step 1: Write the failing test**

Add this test after `renders node plugin inventory with summary counts` in `web/src/pages/plugins/page.test.tsx`:

```tsx
test("uses editorial plugin inventory and binding table surfaces", async () => {
  getNodePluginsMock.mockResolvedValueOnce({ node_id: 2, total: 1, items: [pluginRow] })
  getPluginBindingsMock.mockResolvedValueOnce({
    items: [{ uid: "u1", plugin_no: "wk.echo", warnings: [] }],
    total: 1,
    has_more: false,
  })

  const user = userEvent.setup()
  renderPluginsPage()

  const inventoryTable = await screen.findByRole("table", { name: "Node plugin inventory" })
  const inventorySurface = inventoryTable.closest("[data-plugins-surface='inventory']")
  expect(inventorySurface).toHaveClass("overflow-x-auto", "rounded-md", "border", "border-border")
  expect(inventoryTable).toHaveClass("text-sm")

  const inventoryToolbar = screen.getByTestId("plugins-inventory-toolbar")
  expect(inventoryToolbar).toHaveClass("border-b", "border-border", "pb-4")
  expect(within(inventoryToolbar).getByLabelText("Plugin keyword")).toBeInTheDocument()

  await user.type(screen.getByLabelText("Binding query"), "u1")
  await user.click(screen.getByRole("button", { name: "Search bindings" }))

  const bindingsTable = await screen.findByRole("table", { name: "Plugin bindings" })
  const bindingsSurface = bindingsTable.closest("[data-plugins-surface='bindings']")
  expect(bindingsSurface).toHaveClass("overflow-x-auto", "rounded-md", "border", "border-border")
  expect(bindingsTable).toHaveClass("text-sm")

  const bindingsToolbar = screen.getByTestId("plugins-bindings-toolbar")
  expect(bindingsToolbar).toHaveClass("border-b", "border-border", "pb-4")
})
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/plugins/page.test.tsx -t "uses editorial plugin inventory and binding table surfaces"
```

Expected: FAIL because `plugins-inventory-toolbar`, `plugins-bindings-toolbar`, and `data-plugins-surface` are not present yet.

- [ ] **Step 3: Update plugin inventory toolbar and table surface**

In `web/src/pages/plugins/page.tsx`, update the `PluginInventoryFiltersBar` root element to include the new test id and toolbar border:

```tsx
return (
  <div
    className="mb-4 grid gap-3 border-b border-border pb-4 lg:grid-cols-[minmax(220px,1fr)_180px_180px]"
    data-testid="plugins-inventory-toolbar"
  >
```

In the plugin inventory table block, replace the existing wrapper and table opening with:

```tsx
<div
  className="overflow-x-auto rounded-md border border-border"
  data-plugins-surface="inventory"
>
  <table
    aria-label={intl.formatMessage({ id: "plugins.inventory.title" })}
    className="w-full border-collapse text-sm"
  >
```

- [ ] **Step 4: Update plugin binding toolbar and table surface**

In `web/src/pages/plugins/page.tsx`, update the plugin binding search form opening to:

```tsx
<form
  aria-label={intl.formatMessage({ id: "plugins.bindings.title" })}
  className="flex flex-col gap-3 border-b border-border pb-4 lg:flex-row lg:items-end"
  data-testid="plugins-bindings-toolbar"
  onSubmit={(event) => {
    void submitBindingSearch(event)
  }}
>
```

In the plugin binding results block, replace the existing wrapper and table opening with:

```tsx
<div
  className="overflow-x-auto rounded-md border border-border"
  data-plugins-surface="bindings"
>
  <table
    aria-label={intl.formatMessage({ id: "plugins.bindings.title" })}
    className="w-full border-collapse text-sm"
  >
```

- [ ] **Step 5: Run focused plugin page tests**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/plugins/page.test.tsx
```

Expected: PASS. Existing plugin behavior tests still cover node selection, local filtering, detail sheet loading, stale response protection, config validation/update, restart/uninstall confirmations, binding search, add, delete, and load-more behavior.

- [ ] **Step 6: Commit plugin page change**

Run:

```bash
git add web/src/pages/plugins/page.tsx web/src/pages/plugins/page.test.tsx
git commit -m "style(web): refine plugin operations surfaces"
```

Expected: commit succeeds with only plugin page and plugin test changes.

## Task 2: Workqueue Operations Surface

**Files:**
- Modify: `web/src/pages/workqueues/page.tsx`
- Test: `web/src/pages/workqueues/page.test.tsx`

- [ ] **Step 1: Write the failing test**

Add this test after `renders summary and pressure rows` in `web/src/pages/workqueues/page.test.tsx`:

```tsx
test("uses an editorial workqueue toolbar status strip and named table", async () => {
  getRuntimeWorkqueuesMock.mockResolvedValue(workqueueResponse)
  renderPage()

  const toolbar = await screen.findByTestId("workqueues-query-toolbar")
  expect(toolbar).toHaveClass("rounded-lg", "border", "border-border", "bg-card", "p-3")
  expect(within(toolbar).getByLabelText("Node")).toBeInTheDocument()
  expect(within(toolbar).getByLabelText("Window")).toBeInTheDocument()
  expect(within(toolbar).getByLabelText("Component")).toBeInTheDocument()

  const metadata = screen.getByTestId("workqueues-metadata-row")
  expect(metadata).toHaveClass("border-t", "border-border", "pt-3")
  expect(within(metadata).getByText(/node-1/)).toBeInTheDocument()

  const statusStrip = screen.getByTestId("workqueues-status-strip")
  expect(statusStrip).toHaveClass("grid", "border", "border-border")
  expect(within(statusStrip).getByText("degraded overall")).toBeInTheDocument()

  const table = screen.getByRole("table", { name: "Workqueue Monitor" })
  const surface = table.closest("[data-workqueues-surface='inventory']")
  expect(surface).toHaveClass("rounded-md", "border", "border-border")
  expect(table).toHaveClass("text-sm")
})
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/workqueues/page.test.tsx -t "uses an editorial workqueue toolbar status strip and named table"
```

Expected: FAIL because the toolbar, metadata row, status strip, named table, and table surface markers are not present yet.

- [ ] **Step 3: Update the toolbar shell and metadata row**

In `web/src/pages/workqueues/page.tsx`, replace the current control wrapper:

```tsx
<div className="flex flex-col gap-3 rounded-lg border border-border bg-card px-4 py-3 md:flex-row md:items-end md:justify-between">
```

with this compact toolbar wrapper:

```tsx
<div
  className="rounded-lg border border-border bg-card p-3"
  data-testid="workqueues-query-toolbar"
>
```

Do not edit the controls grid that starts with `className="grid gap-3 sm:grid-cols-2 lg:grid-cols-5"` in this step. Replace the current scope text block:

```tsx
<div className="text-xs text-muted-foreground">
  {intl.formatMessage({ id: "workqueues.scope.node" })}: {nodeLabel}
  <span className="mx-2">/</span>
  {intl.formatMessage({ id: "workqueues.scope.samples" })}: {sampleCount}
</div>
```

with:

```tsx
<div
  className="mt-3 flex flex-wrap items-center gap-3 border-t border-border pt-3 text-xs text-muted-foreground"
  data-testid="workqueues-metadata-row"
>
  <span>{intl.formatMessage({ id: "workqueues.scope.node" })}: {nodeLabel}</span>
  <span>{intl.formatMessage({ id: "workqueues.scope.samples" })}: {sampleCount}</span>
</div>
```

- [ ] **Step 4: Convert the summary cards into a status strip**

In `web/src/pages/workqueues/page.tsx`, replace the five-card summary grid:

```tsx
<div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-5">
```

through its closing `</div>` for the summary area with:

```tsx
<div
  className="grid overflow-hidden rounded-md border border-border bg-card sm:grid-cols-2 xl:grid-cols-5"
  data-testid="workqueues-status-strip"
>
  <SummaryStripItem
    label={intl.formatMessage({ id: "workqueues.summary.level" })}
    value={intl.formatMessage({ id: "workqueues.summary.overallValue" }, { level: response.summary.overall_level })}
  />
  <SummaryStripItem
    label={intl.formatMessage({ id: "workqueues.summary.total" })}
    value={response.summary.total}
  />
  <SummaryStripItem
    label={intl.formatMessage({ id: "workqueues.summary.degraded" })}
    value={abnormalCount}
  />
  <SummaryStripItem
    label={intl.formatMessage({ id: "workqueues.summary.hottest" })}
    value={formatHottest(response)}
  />
  <SummaryStripItem
    label={intl.formatMessage({ id: "workqueues.summary.window" })}
    value={`${response.window_seconds}s`}
  />
</div>
```

Add this helper below `ColumnHeader` and above `export function WorkqueuesPage()`:

```tsx
function SummaryStripItem({ label, value }: { label: string; value: number | string }) {
  return (
    <div className="border-b border-border px-3 py-3 last:border-b-0 sm:border-r sm:last:border-r-0 xl:border-b-0">
      <div className="text-xs font-medium text-muted-foreground">{label}</div>
      <div className="mt-2 truncate text-sm font-semibold text-foreground">{value}</div>
    </div>
  )
}
```

- [ ] **Step 5: Name and compact the table surface**

In `web/src/pages/workqueues/page.tsx`, replace the table section opening:

```tsx
<section className="rounded-lg border border-border bg-card">
```

with:

```tsx
<section
  className="rounded-md border border-border bg-card"
  data-workqueues-surface="inventory"
>
```

Replace the table opening:

```tsx
<table className="w-full min-w-[1080px] border-collapse text-left">
```

with:

```tsx
<table
  aria-label={title}
  className="w-full min-w-[1080px] border-collapse text-left text-sm"
>
```

- [ ] **Step 6: Run focused workqueue page tests**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/workqueues/page.test.tsx
```

Expected: PASS. Existing tests still cover table help tooltips, API-provided service labels, loading state, abnormal count, component options, abnormal-only filtering, component filtering, service-unavailable state, empty state, refresh window, and selected-node reload.

- [ ] **Step 7: Commit workqueue page change**

Run:

```bash
git add web/src/pages/workqueues/page.tsx web/src/pages/workqueues/page.test.tsx
git commit -m "style(web): refine workqueue operations surface"
```

Expected: commit succeeds with only workqueue page and workqueue test changes.

## Task 3: DB Inspect Workbench

**Files:**
- Modify: `web/src/pages/db-inspect/page.tsx`
- Test: `web/src/pages/db-inspect/page.test.tsx`

- [ ] **Step 1: Write the failing test**

Update the first import in `web/src/pages/db-inspect/page.test.tsx`:

```tsx
import { render, screen, within } from "@testing-library/react"
```

Add this test after `loads table list and runs a query`:

```tsx
test("uses editorial db inspect rail workbench and named result tables", async () => {
  getNodesMock.mockResolvedValueOnce(nodesResponse())
  getDBInspectTablesMock.mockResolvedValueOnce({
    node_id: 1,
    generated_at: "2026-06-17T10:00:00Z",
    rows: [{ domain: "meta", name: "user", table: "meta.user" }],
    stats: { scan_mode: "", scanned_hash_slots: [], scanned_rows: 0, returned_rows: 1, has_more: false, next_cursor: "" },
  })
  getDBInspectTableMock.mockResolvedValueOnce({
    node_id: 1,
    generated_at: "2026-06-17T10:00:00Z",
    rows: [{ column: "uid", type: "string" }],
    stats: { scan_mode: "", scanned_hash_slots: [], scanned_rows: 1, returned_rows: 1, has_more: false, next_cursor: "" },
  })
  queryDBInspectMock.mockResolvedValueOnce({
    node_id: 1,
    generated_at: "2026-06-17T10:00:01Z",
    rows: [{ uid: "u1" }],
    stats: { scan_mode: "point-partition", scanned_hash_slots: [3], scanned_rows: 1, returned_rows: 1, has_more: false, next_cursor: "" },
  })

  const user = userEvent.setup()
  renderPage()

  const rail = await screen.findByTestId("db-inspect-table-rail")
  expect(rail).toHaveClass("overflow-hidden")

  await user.click(await screen.findByRole("button", { name: "Inspect meta.user" }))
  const describeTable = await screen.findByRole("table", { name: "meta.user columns" })
  const describeSurface = describeTable.closest("[data-db-inspect-surface='describe']")
  expect(describeSurface).toHaveClass("overflow-x-auto", "rounded-md", "border", "border-border")

  const workbench = screen.getByTestId("db-inspect-query-workbench")
  expect(workbench).toHaveClass("border-b", "border-border", "pb-4")
  expect(within(workbench).getByLabelText("Inspect query")).toBeInTheDocument()

  await user.clear(screen.getByLabelText("Inspect query"))
  await user.type(screen.getByLabelText("Inspect query"), "select uid from meta.user limit 20")
  await user.click(screen.getByRole("button", { name: "Run query" }))

  const resultTable = await screen.findByRole("table", { name: "Results" })
  const resultSurface = resultTable.closest("[data-db-inspect-surface='results']")
  expect(resultSurface).toHaveClass("overflow-x-auto", "rounded-md", "border", "border-border")
  expect(resultTable).toHaveClass("text-sm")
  expect(screen.getByTestId("db-inspect-stats-strip")).toHaveClass("border-b", "border-border", "pb-3")
})
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/db-inspect/page.test.tsx -t "uses editorial db inspect rail workbench and named result tables"
```

Expected: FAIL because the table rail, query workbench, describe table name, result table name, and stats strip markers are not present yet.

- [ ] **Step 3: Add compact rail and query workbench markers**

In `web/src/pages/db-inspect/page.tsx`, replace the table list `SectionCard` with this wrapped version:

```tsx
<div className="overflow-hidden" data-testid="db-inspect-table-rail">
  <SectionCard
    className="overflow-hidden"
    title={intl.formatMessage({ id: "dbInspect.tables.title" })}
  >
    {state.loading ? <ResourceState kind="loading" title={intl.formatMessage({ id: "dbInspect.tables.title" })} /> : null}
    {!state.loading && state.tables.length > 0 ? (
      <div className="space-y-4">
        {Object.entries(tablesByDomain).map(([domain, rows]) => (
          <div key={domain}>
            <div className="mb-2 text-xs font-semibold uppercase text-muted-foreground">
              {domain}
            </div>
            <div className="space-y-1">
              {rows.map((row) => (
                <button
                  aria-label={intl.formatMessage({ id: "dbInspect.table.inspect" }, { table: row.table })}
                  className="flex w-full items-center justify-between rounded-md border border-border/70 px-3 py-2 text-left text-sm hover:bg-muted/50"
                  key={row.table}
                  onClick={() => void describeTable(row.table)}
                  type="button"
                >
                  <span className="font-mono">{row.table}</span>
                  <Database className="size-4 text-muted-foreground" />
                </button>
              ))}
            </div>
          </div>
        ))}
      </div>
    ) : null}
  </SectionCard>
</div>
```

In the query card, replace:

```tsx
<form className="space-y-3" onSubmit={submit}>
```

with:

```tsx
<form
  className="space-y-3 border-b border-border pb-4"
  data-testid="db-inspect-query-workbench"
  onSubmit={submit}
>
```

- [ ] **Step 4: Add semantic table labels and compact result surfaces**

In `web/src/pages/db-inspect/page.tsx`, replace the describe result call:

```tsx
<ResultSection
  columns={describeColumns}
  rows={state.describe.rows}
  title={intl.formatMessage({ id: "dbInspect.describe.title" }, { table: state.selectedTable })}
/>
```

with:

```tsx
<ResultSection
  columns={describeColumns}
  rows={state.describe.rows}
  surface="describe"
  title={intl.formatMessage({ id: "dbInspect.describe.title" }, { table: state.selectedTable })}
/>
```

Replace the query result table call:

```tsx
<ResultTable columns={columns} rows={state.result.rows} />
```

with:

```tsx
<ResultTable
  ariaLabel={intl.formatMessage({ id: "dbInspect.results.title" })}
  columns={columns}
  rows={state.result.rows}
  surface="results"
/>
```

Replace the `StatsStrip` component with:

```tsx
function StatsStrip({ result }: { result: ManagerDBInspectQueryResponse }) {
  return (
    <div
      className="mb-4 flex flex-wrap gap-2 border-b border-border pb-3 text-xs text-muted-foreground"
      data-testid="db-inspect-stats-strip"
    >
      <span className="rounded-full border border-border px-2 py-1">{result.stats.scan_mode || "-"}</span>
      <span className="rounded-full border border-border px-2 py-1">scanned {result.stats.scanned_rows}</span>
      <span className="rounded-full border border-border px-2 py-1">returned {result.stats.returned_rows}</span>
      <span className="rounded-full border border-border px-2 py-1">
        slots {result.stats.scanned_hash_slots.length ? result.stats.scanned_hash_slots.join(",") : "-"}
      </span>
    </div>
  )
}
```

Replace `ResultSection` and `ResultTable` with:

```tsx
function ResultSection({
  columns,
  rows,
  surface,
  title,
}: {
  columns: string[]
  rows: ManagerDBInspectRow[]
  surface: "describe" | "results"
  title: string
}) {
  return (
    <SectionCard title={title}>
      <ResultTable ariaLabel={title} columns={columns} rows={rows} surface={surface} />
    </SectionCard>
  )
}

function ResultTable({
  ariaLabel,
  columns,
  rows,
  surface,
}: {
  ariaLabel: string
  columns: string[]
  rows: ManagerDBInspectRow[]
  surface: "describe" | "results"
}) {
  if (rows.length === 0) {
    return <ResourceState kind="empty" title={ariaLabel} />
  }
  return (
    <div
      className="overflow-x-auto rounded-md border border-border"
      data-db-inspect-surface={surface}
    >
      <table aria-label={ariaLabel} className="w-full border-collapse text-sm">
        <thead className="bg-muted/40 text-left text-xs uppercase text-muted-foreground">
          <tr>
            {columns.map((column) => (
              <th className="px-3 py-3" key={column}>{column}</th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((row, index) => (
            <tr className="border-t border-border" key={`${index}-${columns.join(":")}`}>
              {columns.map((column) => (
                <td className="max-w-[320px] truncate px-3 py-3 font-mono text-xs text-foreground" key={column}>
                  {renderCell(row[column])}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
```

- [ ] **Step 5: Run focused DB Inspect page tests**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/db-inspect/page.test.tsx
```

Expected: PASS. Existing tests still cover table loading, query execution, describe loading, cursor pagination, cursor replacement, quoted cursor literals, and query failure text preservation.

- [ ] **Step 6: Commit DB Inspect page change**

Run:

```bash
git add web/src/pages/db-inspect/page.tsx web/src/pages/db-inspect/page.test.tsx
git commit -m "style(web): refine db inspect workbench"
```

Expected: commit succeeds with only DB Inspect page and DB Inspect test changes.

## Task 4: Focused Verification And Build Hygiene

**Files:**
- Check: `web/src/pages/plugins/page.test.tsx`
- Check: `web/src/pages/workqueues/page.test.tsx`
- Check: `web/src/pages/db-inspect/page.test.tsx`
- Check: `web/dist/index.html`

- [ ] **Step 1: Run the combined page test gate**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/plugins/page.test.tsx src/pages/workqueues/page.test.tsx src/pages/db-inspect/page.test.tsx
```

Expected: PASS for all three page test files.

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

Expected: PASS. This may update `web/dist/index.html` only because Vite asset hashes changed.

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

Expected: only intentional committed changes are absent from the working tree. Pre-existing untracked files may remain:

```text
?? docs/superpowers/plans/2026-07-05-legacy-app-slot-store-adapter.md
?? web/DESIGN.md
```

- [ ] **Step 7: Report completion**

Report:

```text
Implemented the approved remaining-page redesign for /cluster/plugins, /cluster/workqueues, and /system/db. Focused page tests, TypeScript build, production build, and git diff check passed. No backend API, route, auth, or unused dashboard changes were made.
```
