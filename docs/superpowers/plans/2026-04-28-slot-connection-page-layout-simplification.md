# Slot and Connection Page Layout Simplification Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Simplify the manager Slots and Connections pages into compact table-first inventory pages while preserving slot operations, details, refresh, and error behavior.

**Architecture:** Flatten both pages so each uses a compact header plus one flat inventory table surface. Remove duplicated `PageHeader`, summary-card, `SectionCard`, and `TableToolbar` chrome; keep existing API calls, permissions, detail sheets, dialogs, and table columns unchanged.

**Tech Stack:** React 19, TypeScript, React Intl, existing manager UI components, Vitest, Testing Library, Bun.

---

## Execution Notes

- Implement in an isolated git worktree, not directly on `main`.
- Follow TDD: add failing layout regression tests before changing page code.
- Do not modify backend Go files, manager API contracts, or API types.
- Do not change slot operation permission logic.
- Do not commit generated `web/dist` changes; restore build hash churn after `bun run build`.
- If `web/dist/index.html` changes after build, restore only that generated file.

## File Structure

- Modify `web/src/pages/slots/page.tsx`
  - Remove `TableToolbar`, `PageHeader`, and most `SectionCard` usages for the main slot inventory.
  - Remove the `slotSummary` calculation and summary-card rendering.
  - Keep `useMemo` because `recoverStrategies`, `canWriteSlots`, and related permission values still use it.
  - Add compact header markup with title, total count, `Refresh`, `Add slot`, and `Rebalance slots`.
  - Render the existing slot table in one flat inventory surface.
  - Render `rebalancePlan` below the table in a lightweight result panel.
  - Keep slot table columns, Inspect action, detail sheet, and all dialogs unchanged.
- Modify `web/src/pages/slots/page.test.tsx`
  - Add a layout regression test before implementation.
  - Keep existing slot operation tests unchanged unless selectors need to account for the single refresh button.
- Modify `web/src/pages/connections/page.tsx`
  - Remove `TableToolbar`, `PageHeader`, and `SectionCard` imports/usages.
  - Remove the connection `summary` calculation and summary-card rendering.
  - Remove `useMemo` from the React import if no longer used.
  - Add compact header markup with title, total count, and `Refresh`.
  - Render the existing connection table in one flat inventory surface.
  - Keep connection table columns, Inspect action, and detail sheet unchanged.
- Modify `web/src/pages/connections/page.test.tsx`
  - Add a layout regression test before implementation.
  - Update the empty-state test to assert the compact page title/empty state instead of old `Connection Inventory` chrome.
- Modify `web/src/i18n/messages/en.ts` only if TypeScript/build reveals missing keys.
- Modify `web/src/i18n/messages/zh-CN.ts` only if English message usage changes.

## Task 0: Prepare Worktree and Baseline

**Files:**
- None

- [ ] **Step 1: Create an isolated worktree**

Run from repo root:

```bash
git check-ignore -q .worktrees
git worktree add .worktrees/slot-connection-page-layout-simplification -b feature/slot-connection-page-layout-simplification
cd .worktrees/slot-connection-page-layout-simplification
```

Expected: worktree exists on `feature/slot-connection-page-layout-simplification`.

- [ ] **Step 2: Install web dependencies if needed**

Run:

```bash
cd web
bun install
```

Expected: dependencies install or are already available.

- [ ] **Step 3: Run baseline focused tests**

Run:

```bash
cd web
bun run test src/pages/slots/page.test.tsx src/pages/connections/page.test.tsx
```

Expected: PASS before code changes. If this hangs, stop the process, rerun once, and record the actual result before continuing.

## Task 1: Add Slot Layout Regression Test

**Files:**
- Test: `web/src/pages/slots/page.test.tsx`

- [ ] **Step 1: Write a failing compact slots layout test**

Add this test after `renderSlotsPage`:

```tsx
test("uses compact slot page chrome without summary cards", async () => {
  getSlotsMock.mockResolvedValueOnce({ total: 1, items: [slotRow] })

  renderSlotsPage()

  expect(await screen.findByText("Slot 9")).toBeInTheDocument()
  expect(screen.getByText("Total: 1")).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Refresh" })).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Add slot" })).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Rebalance slots" })).toBeInTheDocument()
  expect(screen.queryByText("Scope: all slots")).not.toBeInTheDocument()
  expect(screen.queryByText("Slot distribution and runtime status.")).not.toBeInTheDocument()
  expect(screen.queryByText("Leader coverage")).not.toBeInTheDocument()
  expect(screen.queryByText("Slots currently reporting a leader.")).not.toBeInTheDocument()
  expect(screen.queryByText("Ready slots")).not.toBeInTheDocument()
  expect(screen.queryByText("Slots whose quorum state is ready.")).not.toBeInTheDocument()
  expect(screen.queryByText("In sync")).not.toBeInTheDocument()
  expect(screen.queryByText("Slots whose sync state is in sync.")).not.toBeInTheDocument()
  expect(screen.queryByText("Tracked slots")).not.toBeInTheDocument()
  expect(screen.queryByText("Physical slots currently tracked.")).not.toBeInTheDocument()
  expect(screen.queryByText("Slot Inventory")).not.toBeInTheDocument()
  expect(screen.queryByText("Current assignment and runtime state from the manager slot endpoints.")).not.toBeInTheDocument()
  expect(screen.queryByText("Cluster slots")).not.toBeInTheDocument()
  expect(screen.queryByText("Inspect one slot to view task state or run operator actions.")).not.toBeInTheDocument()
})
```

Expected current failure: old page description, scope chip, summary cards, inventory card, and toolbar text still render.

- [ ] **Step 2: Run slots test to verify RED**

Run:

```bash
cd web
bun run test src/pages/slots/page.test.tsx
```

Expected: FAIL on the new compact layout test for old chrome still being present.

## Task 2: Add Connection Layout Regression Test

**Files:**
- Test: `web/src/pages/connections/page.test.tsx`

- [ ] **Step 1: Write a failing compact connections layout test**

Add this test after `renderConnectionsPage`:

```tsx
test("uses compact connection page chrome without summary cards", async () => {
  getConnectionsMock.mockResolvedValueOnce({ total: 1, items: [connectionRow] })

  renderConnectionsPage()

  expect(await screen.findByText("u1")).toBeInTheDocument()
  expect(screen.getByText("Total: 1")).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Refresh" })).toBeInTheDocument()
  expect(screen.queryByText("Scope: local node")).not.toBeInTheDocument()
  expect(screen.queryByText("Connection inventory and transport state.")).not.toBeInTheDocument()
  expect(screen.queryByText("Sessions")).not.toBeInTheDocument()
  expect(screen.queryByText("Local sessions currently listed by the manager API.")).not.toBeInTheDocument()
  expect(screen.queryByText("Users")).not.toBeInTheDocument()
  expect(screen.queryByText("Distinct user IDs represented in the local view.")).not.toBeInTheDocument()
  expect(screen.queryByText("Slots")).not.toBeInTheDocument()
  expect(screen.queryByText("Distinct slot IDs represented in the local view.")).not.toBeInTheDocument()
  expect(screen.queryByText("Listeners")).not.toBeInTheDocument()
  expect(screen.queryByText("Listener types represented in the local view.")).not.toBeInTheDocument()
  expect(screen.queryByText("Connection Inventory")).not.toBeInTheDocument()
  expect(screen.queryByText("Current local connection records from the manager connections endpoints.")).not.toBeInTheDocument()
  expect(screen.queryByText("Local connections")).not.toBeInTheDocument()
  expect(screen.queryByText("Inspect one connection to view addresses, slot ownership, and session metadata.")).not.toBeInTheDocument()
})
```

Expected current failure: old page description, scope chip, summary cards, inventory card, and toolbar text still render.

- [ ] **Step 2: Run connections test to verify RED**

Run:

```bash
cd web
bun run test src/pages/connections/page.test.tsx
```

Expected: FAIL on the new compact layout test for old chrome still being present.

## Task 3: Flatten Slots Page Layout

**Files:**
- Modify: `web/src/pages/slots/page.tsx`
- Test: `web/src/pages/slots/page.test.tsx`

- [ ] **Step 1: Remove unused layout imports**

In `web/src/pages/slots/page.tsx`, remove:

```tsx
import { TableToolbar } from "@/components/manager/table-toolbar"
import { PageHeader } from "@/components/shell/page-header"
```

Keep `SectionCard` only if needed for the rebalance plan during the first edit; the final code should not use `SectionCard` for the main inventory.

- [ ] **Step 2: Remove `slotSummary`**

Delete the `slotSummary` `useMemo` block. Keep `useMemo` in the React import because the page still uses it for strategies and permissions.

- [ ] **Step 3: Replace `PageHeader` with compact slot header**

Replace the existing `PageHeader` block with:

```tsx
<div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
  <div>
    <h1 className="text-xl font-semibold tracking-tight text-foreground">
      {intl.formatMessage({ id: "nav.slots.title" })}
    </h1>
    <p className="mt-1 text-sm text-muted-foreground">
      {state.slots
        ? intl.formatMessage({ id: "slots.totalValue" }, { total: state.slots.total })
        : intl.formatMessage({ id: "slots.totalPending" })}
    </p>
  </div>
  <div className="flex flex-wrap gap-2">
    <Button
      onClick={() => {
        void loadSlots(true)
      }}
      size="sm"
      variant="outline"
    >
      {state.refreshing
        ? intl.formatMessage({ id: "common.refreshing" })
        : intl.formatMessage({ id: "common.refresh" })}
    </Button>
    <Button
      disabled={!canWriteSlots}
      onClick={() => {
        setAddOpen(true)
        setAddError("")
      }}
      size="sm"
      variant="outline"
    >
      {intl.formatMessage({ id: "slots.addSlot" })}
    </Button>
    <Button
      disabled={!canWriteSlots}
      onClick={() => {
        setRebalanceOpen(true)
        setRebalanceError("")
      }}
      size="sm"
    >
      {intl.formatMessage({ id: "slots.rebalance" })}
    </Button>
  </div>
</div>
```

- [ ] **Step 4: Replace summary cards and inventory `SectionCard` with a flat table surface**

Remove the four summary-card `SectionCard`s and replace the slot inventory `SectionCard` with:

```tsx
<div className="rounded-xl border border-border bg-card p-3 shadow-none">
  {state.slots.items.length > 0 ? (
    <div className="overflow-x-auto rounded-lg border border-border">
      {/* Move the existing slot table here unchanged. */}
    </div>
  ) : (
    <ResourceState kind="empty" title={intl.formatMessage({ id: "nav.slots.title" })} />
  )}
</div>
```

Move the existing `<table>` into the marked container without changing:

- Column order
- `StatusBadge` values
- Desired/current peer rendering
- Inspect button label and action
- Row key

- [ ] **Step 5: Replace rebalance plan `SectionCard` with a lightweight panel**

Replace the `rebalancePlan ? <SectionCard ...>` block with:

```tsx
{rebalancePlan ? (
  <div className="rounded-xl border border-border bg-card p-3 shadow-none">
    {rebalancePlan.items.length > 0 ? (
      <div className="space-y-3">
        {/* Keep existing rebalancePlan.items rendering unchanged. */}
      </div>
    ) : (
      <ResourceState kind="empty" title={intl.formatMessage({ id: "slots.rebalancePlan.title" })} />
    )}
  </div>
) : null}
```

Keep the item content (`Hash slot`, `From slot ... to slot ...`) unchanged.

- [ ] **Step 6: Remove `SectionCard` import if unused**

If no `SectionCard` remains in `web/src/pages/slots/page.tsx`, remove:

```tsx
import { SectionCard } from "@/components/shell/section-card"
```

- [ ] **Step 7: Run slots test to verify GREEN**

Run:

```bash
cd web
bun run test src/pages/slots/page.test.tsx
```

Expected: PASS.

## Task 4: Flatten Connections Page Layout

**Files:**
- Modify: `web/src/pages/connections/page.tsx`
- Test: `web/src/pages/connections/page.test.tsx`

- [ ] **Step 1: Remove unused layout imports**

In `web/src/pages/connections/page.tsx`, remove:

```tsx
import { useMemo } from "react"
import { TableToolbar } from "@/components/manager/table-toolbar"
import { PageHeader } from "@/components/shell/page-header"
import { SectionCard } from "@/components/shell/section-card"
```

The React import should become:

```tsx
import { useCallback, useEffect, useState } from "react"
```

- [ ] **Step 2: Remove connection summary computation**

Delete the `summary` `useMemo` block.

- [ ] **Step 3: Replace `PageHeader` with compact connection header**

Replace the existing `PageHeader` block with:

```tsx
<div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
  <div>
    <h1 className="text-xl font-semibold tracking-tight text-foreground">
      {intl.formatMessage({ id: "nav.connections.title" })}
    </h1>
    <p className="mt-1 text-sm text-muted-foreground">
      {state.connections
        ? intl.formatMessage({ id: "connections.totalValue" }, { total: state.connections.total })
        : intl.formatMessage({ id: "connections.totalPending" })}
    </p>
  </div>
  <Button
    onClick={() => {
      void loadConnections(true)
    }}
    size="sm"
    variant="outline"
  >
    {state.refreshing
      ? intl.formatMessage({ id: "common.refreshing" })
      : intl.formatMessage({ id: "common.refresh" })}
  </Button>
</div>
```

- [ ] **Step 4: Replace summary cards and inventory `SectionCard` with a flat table surface**

Remove the four summary-card `SectionCard`s and replace the connection inventory `SectionCard` with:

```tsx
<div className="rounded-xl border border-border bg-card p-3 shadow-none">
  {state.connections.items.length > 0 ? (
    <div className="overflow-x-auto rounded-lg border border-border">
      {/* Move the existing connection table here unchanged. */}
    </div>
  ) : (
    <ResourceState kind="empty" title={intl.formatMessage({ id: "nav.connections.title" })} />
  )}
</div>
```

Move the existing `<table>` into the marked container without changing:

- Column order
- `formatDevice(connection)`
- `formatTimestamp(intl, connection.connected_at)`
- Inspect button label and action
- Row key

- [ ] **Step 5: Update empty-state test for new chrome**

In `web/src/pages/connections/page.test.tsx`, update:

```tsx
expect((await screen.findAllByText("Connection Inventory")).length).toBeGreaterThan(0)
```

to:

```tsx
expect(await screen.findByText("Connections")).toBeInTheDocument()
```

Keep the `no manager data is available` assertion.

- [ ] **Step 6: Run connections test to verify GREEN**

Run:

```bash
cd web
bun run test src/pages/connections/page.test.tsx
```

Expected: PASS.

## Task 5: Commit Layout Flattening

**Files:**
- Modified: `web/src/pages/slots/page.tsx`
- Modified: `web/src/pages/slots/page.test.tsx`
- Modified: `web/src/pages/connections/page.tsx`
- Modified: `web/src/pages/connections/page.test.tsx`

- [ ] **Step 1: Review source diff**

Run:

```bash
git diff -- web/src/pages/slots/page.tsx web/src/pages/slots/page.test.tsx web/src/pages/connections/page.tsx web/src/pages/connections/page.test.tsx
```

Expected: only layout chrome/test changes; no API, permission, detail, or dialog behavior changes.

- [ ] **Step 2: Commit implementation**

Run:

```bash
git add web/src/pages/slots/page.tsx web/src/pages/slots/page.test.tsx web/src/pages/connections/page.tsx web/src/pages/connections/page.test.tsx
git commit -m "Simplify slot and connection page layouts"
```

## Task 6: Focused Verification

**Files:**
- Potentially modified: `web/dist/index.html` from build; restore if unintended.

- [ ] **Step 1: Run focused web tests**

Run:

```bash
cd web
bun run test src/pages/slots/page.test.tsx src/pages/connections/page.test.tsx src/pages/messages/page.test.tsx src/pages/channels/page.test.tsx src/pages/nodes/page.test.tsx src/lib/manager-api.test.ts
```

Expected: PASS.

- [ ] **Step 2: Run web production build**

Run:

```bash
cd web
bun run build
```

Expected: PASS. Vite may emit the existing chunk size warning.

- [ ] **Step 3: Restore generated build output if it changed**

Run:

```bash
git status --short
```

If only `web/dist/index.html` changed because of build asset hash churn, restore it:

```bash
git restore web/dist/index.html
```

Do not restore source files.

- [ ] **Step 4: Check diff hygiene**

Run:

```bash
git diff --check
git status --short
```

Expected: clean after intended commits.

- [ ] **Step 5: Final report**

Report:

- Commits created.
- Exact test/build commands and pass/fail status.
- Existing Vite chunk size warning if present.
