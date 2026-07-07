# Web Operations Pages Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bring the active `slots`, `tasks`, and `controller` manager pages into the approved editorial console visual language without changing backend behavior.

**Architecture:** Keep each page's existing data loading, actions, dialogs, tabs, and URL behavior intact. Add narrow visual-contract tests first, then update page-local surfaces to use compact workbench wrappers, named tables, thin borders, and stable summary strips. Avoid shared component changes unless a page cannot express the approved layout locally.

**Tech Stack:** Vite, React 19, TypeScript, Tailwind CSS v4, Vitest, Testing Library, Bun.

---

## File Structure

- Modify `web/src/pages/slots/page.test.tsx`: add one visual-contract test for slot inventory and operation-result workbench surfaces.
- Modify `web/src/pages/slots/page.tsx`: name slot tables for accessibility, replace remaining page-local `rounded-xl` result/list shells with compact `rounded-lg` editorial surfaces, and add stable `data-slot-surface` attributes for tests.
- Modify `web/src/pages/tasks/page.test.tsx`: add one visual-contract test for the read-only task-center summary strip and filter toolbar.
- Modify `web/src/pages/tasks/page.tsx`: convert summary cards into a single rule-separated summary strip, mark the filter toolbar, and flatten timeline event cards.
- Modify `web/src/pages/controller/page.test.tsx`: add one visual-contract test for the controller toolbar, status strip, and log table.
- Modify `web/src/pages/controller/page.tsx`: mark the controller operation toolbar, convert Raft status cells into a compact status strip, and name the log table.

No i18n file changes are required because the plan reuses existing labels:

- `slots.inventoryTitle`
- `controller.logs.title`
- existing task labels and headings

Do not modify `web/src/pages/cluster-dashboard/*` or `web/src/pages/business-dashboard/*` in this batch.

---

### Task 1: Slots Editorial Workbench Surfaces

**Files:**
- Modify: `web/src/pages/slots/page.test.tsx`
- Modify: `web/src/pages/slots/page.tsx`

- [ ] **Step 1: Write the failing slot workbench test**

In `web/src/pages/slots/page.test.tsx`, add this test after `uses compact slot page chrome without summary cards`:

```tsx
test("marks slot inventory and operation results as editorial workbench surfaces", async () => {
  getSlotsMock.mockResolvedValue({ total: 1, items: [slotRow] })
  rebalanceSlotsMock.mockResolvedValue({
    total: 1,
    items: [{ hash_slot: 3, from_slot_id: 9, to_slot_id: 11 }],
  })

  const user = userEvent.setup()
  renderSlotsPage()

  const inventoryTable = await screen.findByRole("table", { name: "Slot Inventory" })
  const inventorySurface = inventoryTable.closest("[data-slot-surface='inventory']")
  expect(inventorySurface).toHaveClass("rounded-lg", "border", "border-border", "bg-card", "p-3")
  expect(inventorySurface).not.toHaveClass("rounded-xl")

  await user.click(screen.getByRole("button", { name: "Rebalance slots" }))
  await user.click(screen.getByRole("button", { name: "Confirm" }))

  const rebalanceSurface = (await screen.findByText("From slot 9 to slot 11")).closest(
    "[data-slot-surface='rebalance-result']",
  )
  expect(rebalanceSurface).toHaveClass("rounded-lg", "border", "border-border", "bg-card", "p-3")
  expect(rebalanceSurface).not.toHaveClass("rounded-xl")
})
```

- [ ] **Step 2: Run the slot test and verify RED**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/slots/page.test.tsx
```

Expected: FAIL because the slot inventory table does not yet have the accessible name `Slot Inventory` and the inventory/result wrappers do not expose `data-slot-surface`.

- [ ] **Step 3: Update the slot inventory wrapper**

In `web/src/pages/slots/page.tsx`, replace the slot list wrapper with this structure. Keep the existing table headers, rows, button handlers, and empty state content unchanged inside the shown positions.

```tsx
<div data-slot-surface="inventory" className="rounded-lg border border-border bg-card p-3">
  {state.slots.items.length > 0 ? (
    <div className="overflow-x-auto rounded-md border border-border">
      <table
        aria-label={intl.formatMessage({ id: "slots.inventoryTitle" })}
        className="w-full border-collapse text-sm"
      >
        <thead className="bg-muted/40 text-left text-xs uppercase tracking-[0.14em] text-muted-foreground">
          {/* keep existing slot table header cells */}
        </thead>
        <tbody>
          {/* keep existing slot table rows */}
        </tbody>
      </table>
    </div>
  ) : (
    <ResourceState kind="empty" title={intl.formatMessage({ id: "nav.slots.title" })} />
  )}
</div>
```

- [ ] **Step 4: Update slot operation result wrappers**

In the same file, update the rebalance and batch transfer result shells:

```tsx
<div data-slot-surface="rebalance-result" className="rounded-lg border border-border bg-card p-3">
  {/* keep existing rebalance result content */}
</div>
```

```tsx
<div data-slot-surface="batch-transfer-result" className="rounded-lg border border-border bg-card p-3">
  {/* keep existing batch transfer result content */}
</div>
```

Also update the unhealthy slot table shell near `SlotClusterUnhealthyPanel` so it uses the same compact page-local shape:

```tsx
<div data-slot-surface="unhealthy" className="rounded-lg border border-border bg-card p-3">
  {/* keep existing unhealthy slot table or empty state */}
</div>
```

- [ ] **Step 5: Run the slot test and verify GREEN**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/slots/page.test.tsx
```

Expected: PASS.

- [ ] **Step 6: Commit Task 1**

```bash
git add web/src/pages/slots/page.tsx web/src/pages/slots/page.test.tsx
git commit -m "style(web): tighten slots workbench surfaces"
```

---

### Task 2: Tasks Summary Strip And Filter Toolbar

**Files:**
- Modify: `web/src/pages/tasks/page.test.tsx`
- Modify: `web/src/pages/tasks/page.tsx`

- [ ] **Step 1: Write the failing task-center visual test**

In `web/src/pages/tasks/page.test.tsx`, add this test after `renders active Controller tasks and retained audit history`:

```tsx
test("uses a compact task-center summary strip and filter toolbar", async () => {
  renderTasksPage()

  expect(await screen.findByRole("heading", { name: "Controller Tasks" })).toBeInTheDocument()

  const summaryStrip = screen.getByTestId("tasks-summary-strip")
  expect(summaryStrip).toHaveClass("overflow-hidden", "rounded-lg", "border", "border-border", "bg-card")
  expect(summaryStrip.querySelectorAll("[data-task-summary-cell]")).toHaveLength(5)
  expect(summaryStrip.querySelector("[data-task-summary-cell]")).not.toHaveClass("rounded-lg")

  const filterToolbar = screen.getByTestId("tasks-filter-toolbar")
  expect(filterToolbar).toHaveClass("grid", "gap-3", "border-b", "border-border", "pb-4")
})
```

- [ ] **Step 2: Run the tasks test and verify RED**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/tasks/page.test.tsx
```

Expected: FAIL because `tasks-summary-strip`, `data-task-summary-cell`, and `tasks-filter-toolbar` do not exist yet.

- [ ] **Step 3: Replace task summary cards with a summary strip**

In `web/src/pages/tasks/page.tsx`, replace the current summary grid:

```tsx
<div className="grid gap-3 md:grid-cols-5">
  {summaryCards.map((card) => (
    <div className="rounded-lg border border-border bg-card px-4 py-3" key={card.label}>
      <div className="text-xs font-medium text-muted-foreground">{card.label}</div>
      <div className="mt-2 text-2xl font-semibold text-foreground">{formatCount(card.value)}</div>
    </div>
  ))}
</div>
```

with:

```tsx
<div
  className="grid overflow-hidden rounded-lg border border-border bg-card md:grid-cols-5"
  data-testid="tasks-summary-strip"
>
  {summaryCards.map((card, index) => (
    <div
      className="border-b border-border px-4 py-3 last:border-b-0 md:border-r md:border-b-0 md:last:border-r-0"
      data-task-summary-cell=""
      key={card.label}
    >
      <div className="font-mono text-[11px] font-semibold uppercase tracking-[0.14em] text-muted-foreground">
        {card.label}
      </div>
      <div className="mt-2 font-mono text-2xl font-semibold text-foreground">{formatCount(card.value)}</div>
      <span className="sr-only">{index + 1}</span>
    </div>
  ))}
</div>
```

- [ ] **Step 4: Mark and flatten the task filter toolbar**

In the task list `SectionCard`, replace the filter wrapper:

```tsx
<div className="mb-4 grid gap-3 md:grid-cols-5">
```

with:

```tsx
<div className="mb-4 grid gap-3 border-b border-border pb-4 md:grid-cols-5" data-testid="tasks-filter-toolbar">
```

Keep all existing labels, inputs, select values, and state updates unchanged.

- [ ] **Step 5: Flatten task warning and timeline event surfaces**

In `web/src/pages/tasks/page.tsx`, replace the audit truncation notice class:

```tsx
className="rounded-lg border border-border bg-secondary/40 px-4 py-3 text-sm text-foreground"
```

with:

```tsx
className="rounded-md border border-border bg-muted/30 px-4 py-3 text-sm text-foreground"
```

In the timeline event list, replace each event shell:

```tsx
<div className="rounded-lg border border-border bg-muted/30 p-3" key={event.event_id}>
```

with:

```tsx
<div className="rounded-md border border-border bg-background p-3" data-task-timeline-event="" key={event.event_id}>
```

- [ ] **Step 6: Run the tasks test and verify GREEN**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/tasks/page.test.tsx
```

Expected: PASS.

- [ ] **Step 7: Commit Task 2**

```bash
git add web/src/pages/tasks/page.tsx web/src/pages/tasks/page.test.tsx
git commit -m "style(web): compact controller task center"
```

---

### Task 3: Controller Workbench Surfaces

**Files:**
- Modify: `web/src/pages/controller/page.test.tsx`
- Modify: `web/src/pages/controller/page.tsx`

- [ ] **Step 1: Write the failing controller workbench test**

In `web/src/pages/controller/page.test.tsx`, add this test after `renders controller log entry creation time`:

```tsx
test("uses compact controller workbench surfaces", async () => {
  getNodesMock.mockResolvedValueOnce({
    generated_at: "2026-05-06T08:00:00Z",
    controller_leader_id: 2,
    total: 1,
    items: [{
      node_id: 2,
      name: "node-2",
      addr: "127.0.0.1:7002",
      status: "alive",
      last_heartbeat_at: "2026-05-06T07:59:58Z",
      is_local: true,
      capacity_weight: 1,
      controller: { role: "leader", voter: true, leader_id: 2 },
      slot_stats: { count: 0, leader_count: 0 },
    }],
  })
  getControllerLogsMock.mockResolvedValueOnce({
    node_id: 2,
    first_index: 1,
    last_index: 4,
    commit_index: 4,
    applied_index: 3,
    items: [{
      index: 4,
      term: 2,
      type: "normal",
      data_size: 12,
      decode_status: "ok",
      decoded_type: "add_slot",
      decoded: { command: "add_slot", new_slot_id: 9 },
      created_at_ms: Date.UTC(2026, 5, 18, 1, 10, 11, 123),
    }],
  })
  getControllerRaftStatusMock.mockResolvedValueOnce(controllerRaftStatus(2))

  renderControllerPage("/controller?node_id=2")

  const toolbar = await screen.findByTestId("controller-workbench-toolbar")
  expect(toolbar).toHaveClass("border-b", "border-border", "pb-4")

  const statusStrip = await screen.findByTestId("controller-status-strip")
  expect(statusStrip).toHaveClass("grid", "gap-3")
  expect(statusStrip.querySelectorAll("[data-controller-status-cell]")).toHaveLength(5)

  const logsTable = await screen.findByRole("table", { name: "Controller Raft Entries" })
  const logsSurface = logsTable.closest("[data-controller-surface='logs']")
  expect(logsSurface).toHaveClass("rounded-lg", "border", "border-border", "bg-card")
})
```

- [ ] **Step 2: Run the controller test and verify RED**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/controller/page.test.tsx
```

Expected: FAIL because the controller toolbar/status strip/test surface markers and log table accessible name do not exist yet.

- [ ] **Step 3: Mark the controller operation toolbar**

In `ControllerLogsPanel`, replace the top wrapper:

```tsx
<div className="flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between">
```

with:

```tsx
<div
  className="flex flex-col gap-3 border-b border-border pb-4 lg:flex-row lg:items-start lg:justify-between"
  data-testid="controller-workbench-toolbar"
>
```

Keep the node filter, compaction button, refresh button, and all handlers unchanged.

- [ ] **Step 4: Convert controller status cells into a named strip**

In `ControllerRaftStatusPanel`, replace the status grid opening:

```tsx
<div className="grid gap-3 md:grid-cols-2 xl:grid-cols-5">
```

with:

```tsx
<div className="grid gap-3 md:grid-cols-2 xl:grid-cols-5" data-testid="controller-status-strip">
```

For each of the five status cell wrappers, add `data-controller-status-cell=""` and use the same compact background:

```tsx
<div className="rounded-md border border-border bg-background px-3 py-3" data-controller-status-cell="">
  {/* keep existing health cell content */}
</div>
```

Apply that exact wrapper shape to the health, role, watermark, snapshot, and compaction cells.

- [ ] **Step 5: Name and mark the controller log table**

In the controller logs table section, replace:

```tsx
<section className="overflow-hidden rounded-lg border border-border bg-card">
```

with:

```tsx
<section className="overflow-hidden rounded-lg border border-border bg-card" data-controller-surface="logs">
```

Then replace:

```tsx
<table className="min-w-full divide-y divide-border text-sm">
```

with:

```tsx
<table
  aria-label={intl.formatMessage({ id: "controller.logs.title" })}
  className="min-w-full divide-y divide-border text-sm"
>
```

Keep all log rows, decoded expansion, pagination, and load-more behavior unchanged.

- [ ] **Step 6: Run the controller test and verify GREEN**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/controller/page.test.tsx
```

Expected: PASS.

- [ ] **Step 7: Commit Task 3**

```bash
git add web/src/pages/controller/page.tsx web/src/pages/controller/page.test.tsx
git commit -m "style(web): refine controller workbench surfaces"
```

---

### Task 4: Focused Verification And Source Cleanup

**Files:**
- Verify: `web/src/pages/slots/page.test.tsx`
- Verify: `web/src/pages/tasks/page.test.tsx`
- Verify: `web/src/pages/controller/page.test.tsx`
- Verify: TypeScript build graph
- Verify: production build

- [ ] **Step 1: Run focused page tests**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/slots/page.test.tsx src/pages/tasks/page.test.tsx src/pages/controller/page.test.tsx
```

Expected: PASS for all three files.

- [ ] **Step 2: Run TypeScript verification**

Run:

```bash
cd web && /Users/tt/.bun/bin/bunx tsc -b
```

Expected: exit code 0 with no TypeScript errors.

- [ ] **Step 3: Run production build**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run build
```

Expected: exit code 0. A Vite large chunk warning is acceptable because it already exists in this app.

- [ ] **Step 4: Restore generated dist hash churn if present**

Run:

```bash
git status --short
git diff -- web/dist/index.html
```

If the only change in `web/dist/index.html` is the generated asset hash, restore it:

```bash
git restore web/dist/index.html
```

Do not restore source files.

- [ ] **Step 5: Run whitespace check**

Run:

```bash
git diff --check
```

Expected: no output.

- [ ] **Step 6: Report implementation status**

Summarize:

- commits created
- focused tests passed
- TypeScript/build status
- whether `web/dist/index.html` was restored
- remaining unrelated untracked files, especially `web/DESIGN.md` and `docs/superpowers/plans/2026-07-05-legacy-app-slot-store-adapter.md`
