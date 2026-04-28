# Channel Page Layout Simplification Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Simplify the manager Channels page into a compact table-first operations page while preserving channel inspect, message navigation, and pagination behavior.

**Architecture:** Flatten `ChannelsPage` so it uses a compact header plus one table surface. Remove duplicated `PageHeader`, summary-card, `SectionCard`, and `TableToolbar` chrome; keep existing data fetching and detail sheet flows unchanged.

**Tech Stack:** React 19, TypeScript, React Intl, React Router, existing manager UI components, Vitest, Testing Library, Bun.

---

## File Structure

- Modify `web/src/pages/channels/page.tsx`
  - Remove `PageHeader`, `SectionCard`, and `TableToolbar` imports/usages for the main channel list.
  - Remove summary-card calculation and rendering.
  - Add compact header markup with title, loaded count, and refresh button.
  - Render the channel table in a flat card-like surface.
  - Move `Load more` to the bottom-right under the table.
  - Keep detail sheet and message navigation unchanged.
- Modify `web/src/pages/channels/page.test.tsx`
  - Add layout regression tests before implementation.
  - Update existing pagination tests if the button location changes but label remains the same.
- Modify `web/src/i18n/messages/en.ts` only if TypeScript/build reveals unused or missing keys.
- Modify `web/src/i18n/messages/zh-CN.ts` only if English message usage changes.
- Do not modify backend Go files or API types.
- Do not commit generated `web/dist` changes; restore build hash churn before committing/reporting.

## Task 1: Add Compact Layout Regression Tests

**Files:**
- Test: `web/src/pages/channels/page.test.tsx`

- [ ] **Step 1: Write a failing test for compact channel page chrome**

Add this test after `renderChannelsPage` helpers:

```tsx
test("uses compact channel page chrome without summary cards", async () => {
  getChannelRuntimeMetaMock.mockResolvedValueOnce({
    items: [channelRow],
    has_more: false,
  })

  renderChannelsPage()

  expect(await screen.findByText("alpha")).toBeInTheDocument()
  expect(screen.getByText("Loaded: 1")).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Refresh" })).toBeInTheDocument()
  expect(screen.queryByText("Scope: all channels")).not.toBeInTheDocument()
  expect(screen.queryByText("Channel lists and runtime drill-in status.")).not.toBeInTheDocument()
  expect(screen.queryByText("Loaded channels")).not.toBeInTheDocument()
  expect(screen.queryByText("Distinct physical slots represented in view.")).not.toBeInTheDocument()
  expect(screen.queryByText("Paged runtime metadata from the channel manager endpoints.")).not.toBeInTheDocument()
  expect(screen.queryByText("Inspect one channel to view slot ownership and runtime lease metadata.")).not.toBeInTheDocument()
})
```

Expected current failure: old scope chip, page description, summary cards, and toolbar descriptions still render.

- [ ] **Step 2: Run the focused channels test to verify RED**

Run:

```bash
cd web
bun run test src/pages/channels/page.test.tsx
```

Expected: FAIL on the new compact layout test.

## Task 2: Flatten Channels Page Layout

**Files:**
- Modify: `web/src/pages/channels/page.tsx`
- Test: `web/src/pages/channels/page.test.tsx`

- [ ] **Step 1: Remove unused layout imports**

In `web/src/pages/channels/page.tsx`, remove:

```tsx
import { TableToolbar } from "@/components/manager/table-toolbar"
import { PageHeader } from "@/components/shell/page-header"
import { SectionCard } from "@/components/shell/section-card"
```

Keep `PageContainer`, `DetailSheet`, `ResourceState`, `StatusBadge`, `Button`, and `KeyValueList`.

- [ ] **Step 2: Remove summary computation**

Delete the `summary` `useMemo` block because summary cards are no longer rendered. If `useMemo` becomes unused, remove it from the React import:

```tsx
import { useCallback, useEffect, useState } from "react"
```

- [ ] **Step 3: Replace `PageHeader` with compact header markup**

Replace the existing `PageHeader` block with:

```tsx
<div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
  <div>
    <h1 className="text-xl font-semibold tracking-tight text-foreground">
      {intl.formatMessage({ id: "nav.channels.title" })}
    </h1>
    <p className="mt-1 text-sm text-muted-foreground">
      {state.channels
        ? intl.formatMessage({ id: "channels.loadedValue" }, { count: state.channels.items.length })
        : intl.formatMessage({ id: "channels.loadedPending" })}
    </p>
  </div>
  <Button
    onClick={() => {
      void loadChannels(true)
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

- [ ] **Step 4: Replace summary cards and runtime `SectionCard` with one flat table surface**

Replace the fragment that renders summary cards and the `SectionCard` table with:

```tsx
<div className="rounded-xl border border-border bg-card p-3 shadow-none">
  {state.channels.items.length > 0 ? (
    <>
      <div className="overflow-x-auto rounded-lg border border-border">
        ...existing table...
      </div>
      {state.channels.has_more ? (
        <div className="mt-3 flex justify-end">
          <Button
            onClick={() => {
              void loadMoreChannels()
            }}
            size="sm"
            variant="outline"
          >
            {state.loadingMore
              ? intl.formatMessage({ id: "common.loading" })
              : intl.formatMessage({ id: "common.loadMore" })}
          </Button>
        </div>
      ) : null}
    </>
  ) : (
    <ResourceState kind="empty" title={intl.formatMessage({ id: "channels.runtimeTitle" })} />
  )}
</div>
```

Do not change the table columns, row actions, or detail sheet in this task.

- [ ] **Step 5: Run channels test to verify GREEN**

Run:

```bash
cd web
bun run test src/pages/channels/page.test.tsx
```

Expected: PASS.

- [ ] **Step 6: Commit layout flattening**

Run:

```bash
git add web/src/pages/channels/page.tsx web/src/pages/channels/page.test.tsx
git commit -m "Simplify channels page layout"
```

## Task 3: Focused Verification

**Files:**
- Potentially modified: `web/dist/index.html` from build; restore if unintended.

- [ ] **Step 1: Run focused web tests**

Run:

```bash
cd web
bun run test src/pages/channels/page.test.tsx src/pages/nodes/page.test.tsx src/lib/manager-api.test.ts
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
