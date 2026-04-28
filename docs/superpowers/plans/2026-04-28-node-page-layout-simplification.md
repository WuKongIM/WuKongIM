# Node Page Layout Simplification Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Simplify the manager Nodes page layout into a compact operations page while preserving the existing node table data and node lifecycle workflows.

**Architecture:** Flatten `NodesPage` so the main page has one compact header and one table surface. Keep node details in the existing `DetailSheet`, and move scale-in report rendering from an inline `SectionCard` into its own `DetailSheet` so secondary workflow state does not stretch the main list page.

**Tech Stack:** React 19, TypeScript, React Intl, existing manager UI components, Vitest, Testing Library, Bun.

---

## File Structure

- Modify `web/src/pages/nodes/page.tsx`
  - Remove nested `PageHeader`, `SectionCard`, and `TableToolbar` usage for the main node list.
  - Add a compact header area directly inside `PageContainer`.
  - Keep the existing node table row content and row actions.
  - Replace the inline scale-in `SectionCard` with a `DetailSheet` dedicated to scale-in review.
  - Keep existing confirm dialogs for node lifecycle and scale-in start/cancel.
- Modify `web/src/pages/nodes/page.test.tsx`
  - Add failing layout tests first.
  - Update existing scale-in tests to interact with the sheet instead of the inline section.
- Modify `web/src/i18n/messages/en.ts`
  - Add or adjust concise labels for the compact count and scale-in sheet title if needed.
  - Remove no longer referenced layout-only copy only if TypeScript or tests reveal it is unused; otherwise leave keys intact to avoid broad churn.
- Modify `web/src/i18n/messages/zh-CN.ts`
  - Mirror any English message changes.
- Do not modify backend Go files or API types.
- Do not commit generated `web/dist` changes; if `bun run build` mutates `web/dist/index.html`, restore that file before committing unless project policy explicitly wants committed build output.

## Task 1: Add Layout Regression Tests

**Files:**
- Test: `web/src/pages/nodes/page.test.tsx`

- [ ] **Step 1: Add a failing test for the compact page chrome**

Add a test near the existing node page render tests:

```tsx
test("uses compact node page chrome without duplicate header actions", async () => {
  getNodesMock.mockResolvedValueOnce({
    generated_at: "2026-04-23T08:00:01Z",
    controller_leader_id: 1,
    total: 1,
    items: [nodeRow],
  })

  renderNodesPage()

  expect(await screen.findByText("127.0.0.1:7000")).toBeInTheDocument()
  expect(screen.queryByText("Scope: all nodes")).not.toBeInTheDocument()
  expect(screen.queryByText("Current node placement, role, and lifecycle state from the manager API.")).not.toBeInTheDocument()
  expect(screen.queryByText("Inspect a node for hosted slot details or run lifecycle actions.")).not.toBeInTheDocument()
  expect(screen.queryByRole("button", { name: "Inspect" })).not.toBeInTheDocument()
  expect(screen.getByText("Total: 1")).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Refresh" })).toBeInTheDocument()
})
```

Expected current failure: stale layout text and the disabled top-level `Inspect` button still exist.

- [ ] **Step 2: Run the focused page test to verify RED**

Run:

```bash
cd web
bun run test src/pages/nodes/page.test.tsx
```

Expected: FAIL on `uses compact node page chrome without duplicate header actions` because current page still renders the duplicated layout chrome.

## Task 2: Move Scale-In Review Into a Sheet

**Files:**
- Test: `web/src/pages/nodes/page.test.tsx`
- Modify: `web/src/pages/nodes/page.tsx`

- [ ] **Step 1: Add a failing scale-in sheet test**

Update the existing `reviews a scale-in plan and starts scale-in after confirmation` test or add a focused test:

```tsx
test("opens scale-in review in a sheet instead of an inline section", async () => {
  getNodesMock.mockResolvedValueOnce({
    generated_at: "2026-04-23T08:00:01Z",
    controller_leader_id: 1,
    total: 1,
    items: [nodeRow],
  })
  planNodeScaleInMock.mockResolvedValueOnce(scaleInPlanReport)

  const user = userEvent.setup()
  renderNodesPage()

  expect(await screen.findByText("127.0.0.1:7000")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Review scale-in for node 1" }))

  const dialog = await screen.findByRole("dialog", { name: "Scale-in Plan" })
  expect(dialog).toHaveTextContent("Assigned replicas")
  expect(screen.queryByText("Node 1 manager-driven scale-in safety report.")).not.toBeInTheDocument()
})
```

Note: If the accessible name does not match because `DetailSheet` uses a fixed `aria-labelledby`, assert `screen.getByRole("dialog")` and title text inside it instead.

Expected current failure: scale-in report renders inline inside `SectionCard`, not inside a `role="dialog"` sheet.

- [ ] **Step 2: Run page tests to verify RED**

Run:

```bash
cd web
bun run test src/pages/nodes/page.test.tsx
```

Expected: FAIL because scale-in content is still inline.

- [ ] **Step 3: Introduce scale-in sheet close behavior**

In `web/src/pages/nodes/page.tsx`, add a close callback near `closeDetail`:

```tsx
const closeScaleIn = useCallback((open: boolean) => {
  if (open) {
    return
  }
  setScaleInNodeId(null)
  setScaleInReport(null)
  setScaleInError("")
  setScaleInAction(null)
  setScaleInConfirmAction(null)
}, [])
```

Keep existing scale-in state variables.

- [ ] **Step 4: Render scale-in content inside `DetailSheet`**

Replace the inline block:

```tsx
{scaleInNodeId ? (
  <SectionCard ...>
    ...
  </SectionCard>
) : null}
```

with:

```tsx
<DetailSheet
  description={
    scaleInNodeId
      ? intl.formatMessage({ id: "nodes.scaleIn.description" }, { id: scaleInNodeId })
      : intl.formatMessage({ id: "nodes.scaleIn.title" })
  }
  footer={
    scaleInNodeId ? (
      <div className="flex flex-wrap items-center justify-end gap-2">
        {scaleInReport ? (
          <Button
            disabled={!canReadScaleIn || scaleInAction === "refresh"}
            onClick={() => {
              void refreshScaleInStatus()
            }}
            size="sm"
            variant="outline"
          >
            {intl.formatMessage({ id: "nodes.scaleIn.refreshStatus" })}
          </Button>
        ) : null}
        {scaleInReport?.can_start ? (
          <Button
            disabled={!canWriteScaleIn || scaleInAction === "start"}
            onClick={() => setScaleInConfirmAction("start")}
            size="sm"
          >
            {intl.formatMessage({ id: "nodes.scaleIn.start" })}
          </Button>
        ) : null}
        {scaleInReport?.can_advance ? (
          <Button
            disabled={!canWriteScaleIn || scaleInAction === "advance"}
            onClick={() => {
              void advanceScaleIn()
            }}
            size="sm"
          >
            {intl.formatMessage({ id: "nodes.scaleIn.advance" })}
          </Button>
        ) : null}
        {scaleInReport?.can_cancel ? (
          <Button
            disabled={!canWriteScaleIn || scaleInAction === "cancel"}
            onClick={() => setScaleInConfirmAction("cancel")}
            size="sm"
            variant="outline"
          >
            {intl.formatMessage({ id: "nodes.scaleIn.cancel" })}
          </Button>
        ) : null}
      </div>
    ) : null
  }
  onOpenChange={closeScaleIn}
  open={scaleInNodeId !== null}
  title={intl.formatMessage({ id: "nodes.scaleIn.title" })}
>
  {scaleInAction === "plan" && !scaleInReport ? (
    <ResourceState kind="loading" title={intl.formatMessage({ id: "nodes.scaleIn.title" })} />
  ) : null}
  {!canWriteScaleIn ? (
    <div className="mb-3 rounded-lg border border-border bg-muted/30 px-4 py-3 text-sm text-muted-foreground">
      {intl.formatMessage({ id: "nodes.scaleIn.permissionRequired" })}
    </div>
  ) : null}
  {scaleInError ? <p className="mb-3 text-sm text-destructive">{scaleInError}</p> : null}
  {scaleInReport ? <ScaleInReportView intl={intl} report={scaleInReport} /> : null}
</DetailSheet>
```

Preserve all scale-in action functions and confirm dialogs.

- [ ] **Step 5: Run page tests to verify scale-in behavior GREEN**

Run:

```bash
cd web
bun run test src/pages/nodes/page.test.tsx
```

Expected: PASS for scale-in sheet tests and existing scale-in flow tests after expectation updates.

## Task 3: Flatten Main Nodes Layout

**Files:**
- Modify: `web/src/pages/nodes/page.tsx`
- Modify: `web/src/i18n/messages/en.ts` if needed
- Modify: `web/src/i18n/messages/zh-CN.ts` if needed
- Test: `web/src/pages/nodes/page.test.tsx`

- [ ] **Step 1: Remove unused layout imports**

In `web/src/pages/nodes/page.tsx`, remove imports that are no longer needed after flattening:

```tsx
import { PageHeader } from "@/components/shell/page-header"
import { SectionCard } from "@/components/shell/section-card"
import { TableToolbar } from "@/components/manager/table-toolbar"
```

Keep `PageContainer`, `DetailSheet`, `ResourceState`, `StatusBadge`, `Button`, `ConfirmDialog`, and `KeyValueList`.

- [ ] **Step 2: Replace `PageHeader` with compact header markup**

At the top of the returned JSX, replace the existing `PageHeader` block with:

```tsx
<div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
  <div>
    <h1 className="text-xl font-semibold tracking-tight text-foreground">
      {intl.formatMessage({ id: "nav.nodes.title" })}
    </h1>
    <p className="mt-1 text-sm text-muted-foreground">
      {state.nodes
        ? intl.formatMessage({ id: "nodes.totalValue" }, { total: state.nodes.total })
        : intl.formatMessage({ id: "nodes.totalPending" })}
    </p>
  </div>
  <Button
    onClick={() => {
      void loadNodes(true)
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

This removes the scope chip, page description, and disabled top-level Inspect action.

- [ ] **Step 3: Replace `SectionCard` + `TableToolbar` around the table**

Replace:

```tsx
<SectionCard description=... title=...>
  <TableToolbar ... />
  ...table or empty state...
</SectionCard>
```

with a single surface:

```tsx
<div className="rounded-xl border border-border bg-card p-3 shadow-none">
  {state.nodes.items.length > 0 ? (
    <div className="overflow-x-auto rounded-lg border border-border">
      ...existing table...
    </div>
  ) : (
    <ResourceState kind="empty" title={intl.formatMessage({ id: "nodes.inventoryTitle" })} />
  )}
</div>
```

Do not change table columns or row actions in this task.

- [ ] **Step 4: Remove stale layout message usage if TypeScript flags it**

If `bun run build` reports unused i18n imports or no issue, leave message keys intact. If tests or lint explicitly assert absence, update `web/src/i18n/messages/en.ts` and `web/src/i18n/messages/zh-CN.ts` only for labels used by new compact UI.

- [ ] **Step 5: Run the page test to verify compact layout GREEN**

Run:

```bash
cd web
bun run test src/pages/nodes/page.test.tsx
```

Expected: PASS.

- [ ] **Step 6: Commit layout flattening**

Run:

```bash
git add web/src/pages/nodes/page.tsx web/src/pages/nodes/page.test.tsx web/src/i18n/messages/en.ts web/src/i18n/messages/zh-CN.ts
git commit -m "Simplify nodes page layout"
```

## Task 4: Focused Verification

**Files:**
- Potentially modified: `web/dist/index.html` from build; restore if unintended.

- [ ] **Step 1: Run focused web tests**

Run:

```bash
cd web
bun run test src/pages/nodes/page.test.tsx src/lib/manager-api.test.ts
```

Expected: PASS, with all node page and API client tests green.

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

If only `web/dist/index.html` changed due to asset hash churn and the project does not expect committed build output for this UI edit, restore it:

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

Expected: `git diff --check` has no output. `git status --short` is clean after all intended commits, or shows only intentional uncommitted changes if the implementer has not committed yet.

- [ ] **Step 5: Final report**

Report:

- Commits created.
- Exact test/build commands run and pass/fail status.
- Any known warning, especially Vite chunk size warning.
