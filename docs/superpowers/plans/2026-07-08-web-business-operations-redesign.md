# Web Business Operations Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bring `/business/users`, `/business/channels`, and `/business/connections` into the approved editorial console visual language without changing manager API behavior.

**Architecture:** Keep each page's data loading, permissions, filters, pagination, dialogs, detail sheets, and action semantics intact. Add narrow visual-contract tests first, then adjust page-local wrappers, toolbars, table names, and dense detail surfaces using existing shell and manager components. Do not touch the unused dashboard routes or introduce new shared UI abstractions unless a page cannot express the approved layout locally.

**Tech Stack:** Vite, React 19, TypeScript, Tailwind CSS v4, Vitest, Testing Library, Bun.

---

## File Structure

- Modify `web/src/pages/users/page.test.tsx`: add one visual-contract test for the users inventory toolbar and table surface.
- Modify `web/src/pages/users/page.tsx`: move refresh into the inventory toolbar, name the users table, flatten list/detail surfaces, and keep existing user actions unchanged.
- Modify `web/src/pages/channels-biz/page.test.tsx`: add one visual-contract test for the business channel inventory toolbar, named table, member toolbar, and member table.
- Modify `web/src/pages/channels-biz/page.tsx`: mark the search/type toolbar, name the inventory and member tables, flatten member management surfaces, and keep metadata/member actions unchanged.
- Modify `web/src/pages/connections/page.test.tsx`: add one visual-contract test for the compact connection header toolbar and named inventory table.
- Modify `web/src/pages/connections/page.tsx`: keep the compact connection page chrome, mark the filter toolbar, name the table, and reduce the remaining `rounded-xl` inventory shell.

No i18n file changes are required because the plan reuses existing labels:

- `users.list.title`
- `channelsBiz.list.title`
- `connections.table.*`
- `nav.connections.title`
- member kind labels from `channelsBiz.members.*`

Do not modify `web/src/pages/cluster-dashboard/*`, `web/src/pages/business-dashboard/*`, `/business/messages`, `/business/conversations`, or `/business/system-users` in this batch.

---

### Task 1: Users Editorial Inventory Surface

**Files:**
- Modify: `web/src/pages/users/page.test.tsx`
- Modify: `web/src/pages/users/page.tsx`

- [ ] **Step 1: Write the failing users visual-contract test**

In `web/src/pages/users/page.test.tsx`, add this test after `renders the first user page`:

```tsx
test("uses an editorial user inventory toolbar and table surface", async () => {
  getUsersMock.mockResolvedValueOnce({ items: [userRow], has_more: false })

  renderUsersPage()

  const table = await screen.findByRole("table", { name: "Users" })
  const inventorySurface = table.closest("[data-users-surface='inventory']")
  expect(inventorySurface).toHaveClass("overflow-x-auto", "rounded-md", "border", "border-border")
  expect(inventorySurface).not.toHaveClass("rounded-xl")

  const toolbar = screen.getByTestId("users-filter-toolbar")
  expect(toolbar).toHaveClass("border-b", "border-border", "pb-4")
  expect(within(toolbar).getByPlaceholderText("Search UID")).toBeInTheDocument()
  expect(within(toolbar).getByRole("button", { name: "Search" })).toBeInTheDocument()
  expect(within(toolbar).getByRole("button", { name: "Refresh" })).toBeInTheDocument()
})
```

Also update the imports at the top of `web/src/pages/users/page.test.tsx`:

```tsx
import { render, screen, waitFor, within } from "@testing-library/react"
```

- [ ] **Step 2: Run the users test and verify RED**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/users/page.test.tsx
```

Expected: FAIL because the users table is not named and `data-users-surface` / `users-filter-toolbar` are not present.

- [ ] **Step 3: Move refresh into the users toolbar**

In `web/src/pages/users/page.tsx`, replace the current `SectionCard` opening block:

```tsx
<SectionCard
  action={
    <div className="flex flex-wrap gap-2">
      <Button
        onClick={() => {
          void runQuery({ keyword: activeKeyword, refreshing: true })
        }}
        size="sm"
        variant="outline"
      >
        {state.refreshing
          ? intl.formatMessage({ id: "common.refreshing" })
          : intl.formatMessage({ id: "common.refresh" })}
      </Button>
    </div>
  }
  description={intl.formatMessage({ id: "users.list.description" })}
  title={intl.formatMessage({ id: "users.list.title" })}
>
```

with:

```tsx
<SectionCard
  className="overflow-hidden"
  description={intl.formatMessage({ id: "users.list.description" })}
  title={intl.formatMessage({ id: "users.list.title" })}
>
```

Then replace the current search form:

```tsx
<form className="mb-4 flex flex-col gap-2 sm:flex-row" onSubmit={submitSearch}>
```

with this toolbar and nested form opening:

```tsx
<div
  className="mb-4 flex flex-col gap-3 border-b border-border pb-4 lg:flex-row lg:items-end lg:justify-between"
  data-testid="users-filter-toolbar"
>
  <form className="flex min-w-0 flex-1 flex-col gap-2 sm:flex-row" onSubmit={submitSearch}>
```

Immediately after the existing search form closing `</form>`, insert the refresh button inside the toolbar:

```tsx
  <Button
    onClick={() => {
      void runQuery({ keyword: activeKeyword, refreshing: true })
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

- [ ] **Step 4: Name and mark the users table surface**

In `web/src/pages/users/page.tsx`, replace:

```tsx
<div className="overflow-x-auto rounded-lg border border-border">
  <table className="w-full border-collapse">
```

with:

```tsx
<div className="overflow-x-auto rounded-md border border-border" data-users-surface="inventory">
  <table aria-label={intl.formatMessage({ id: "users.list.title" })} className="w-full border-collapse text-sm">
```

- [ ] **Step 5: Flatten users detail micro-surfaces**

In `web/src/pages/users/page.tsx`, replace every detail card class in the detail sheet:

```tsx
className="rounded-lg border border-border p-3 text-sm"
```

with:

```tsx
className="rounded-md border border-border bg-background p-3 text-sm"
```

Also replace the reset-token result shell:

```tsx
className="rounded-lg border border-border bg-muted/40 p-3 text-sm"
```

with:

```tsx
className="rounded-md border border-border bg-muted/30 p-3 text-sm"
```

- [ ] **Step 6: Run the users test and verify GREEN**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/users/page.test.tsx
```

Expected: PASS.

- [ ] **Step 7: Commit Task 1**

```bash
git add web/src/pages/users/page.tsx web/src/pages/users/page.test.tsx
git commit -m "style(web): refine users inventory surface"
```

---

### Task 2: Business Channels Editorial Inventory Surface

**Files:**
- Modify: `web/src/pages/channels-biz/page.test.tsx`
- Modify: `web/src/pages/channels-biz/page.tsx`

- [ ] **Step 1: Write the failing business channels visual-contract test**

In `web/src/pages/channels-biz/page.test.tsx`, add this test after `renders the first business channel page`:

```tsx
test("uses editorial business channel inventory and member surfaces", async () => {
  getBusinessChannelsMock.mockResolvedValue({ items: [groupChannel], has_more: false })
  getBusinessChannelMock.mockResolvedValue(groupDetail)
  getBusinessChannelMembersMock.mockResolvedValue({ items: [{ uid: "u1" }], has_more: false })

  const user = userEvent.setup()
  renderChannelsBizPage()

  const table = await screen.findByRole("table", { name: "Business channels" })
  const inventorySurface = table.closest("[data-channels-biz-surface='inventory']")
  expect(inventorySurface).toHaveClass("overflow-x-auto", "rounded-md", "border", "border-border")
  expect(inventorySurface).not.toHaveClass("rounded-xl")

  const toolbar = screen.getByTestId("channels-biz-filter-toolbar")
  expect(toolbar).toHaveClass("border-b", "border-border", "pb-4")
  expect(within(toolbar).getByPlaceholderText("Search channel ID")).toBeInTheDocument()
  expect(within(toolbar).getByLabelText("Channel type")).toBeInTheDocument()

  await user.click(screen.getByRole("button", { name: "Inspect channel g1" }))

  const memberToolbar = await screen.findByTestId("channels-biz-member-toolbar")
  expect(memberToolbar).toHaveClass("rounded-md", "border", "border-border", "bg-muted/30", "p-2")

  const memberTable = await screen.findByRole("table", { name: "Subscribers" })
  const memberSurface = memberTable.closest("[data-channels-biz-surface='members']")
  expect(memberSurface).toHaveClass("overflow-x-auto", "rounded-md", "border", "border-border")
})
```

`within` is already imported in this test file, so no import change is required.

- [ ] **Step 2: Run the business channels test and verify RED**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/channels-biz/page.test.tsx
```

Expected: FAIL because the inventory and member tables are not named and the new surface/test IDs are not present.

- [ ] **Step 3: Mark the business channel filter toolbar**

In `web/src/pages/channels-biz/page.tsx`, update the `SectionCard` opening block from:

```tsx
<SectionCard
  description={intl.formatMessage({ id: "channelsBiz.list.description" })}
  title={intl.formatMessage({ id: "channelsBiz.list.title" })}
>
```

to:

```tsx
<SectionCard
  className="overflow-hidden"
  description={intl.formatMessage({ id: "channelsBiz.list.description" })}
  title={intl.formatMessage({ id: "channelsBiz.list.title" })}
>
```

Then replace the current search form opening:

```tsx
<form className="mb-4 flex flex-col gap-2 lg:flex-row" onSubmit={submitSearch}>
```

with:

```tsx
<form
  className="mb-4 grid gap-3 border-b border-border pb-4 lg:grid-cols-[minmax(0,1fr)_220px_auto]"
  data-testid="channels-biz-filter-toolbar"
  onSubmit={submitSearch}
>
```

- [ ] **Step 4: Name and mark the business channel inventory table**

In `web/src/pages/channels-biz/page.tsx`, replace:

```tsx
<div className="overflow-x-auto rounded-lg border border-border">
  <table className="w-full border-collapse">
```

with:

```tsx
<div className="overflow-x-auto rounded-md border border-border" data-channels-biz-surface="inventory">
  <table
    aria-label={intl.formatMessage({ id: "channelsBiz.list.title" })}
    className="w-full border-collapse text-sm"
  >
```

- [ ] **Step 5: Flatten the member-management toolbar and blocked notice**

In the member section of `web/src/pages/channels-biz/page.tsx`, replace the member tab wrapper:

```tsx
<div className="flex flex-wrap gap-2">
```

with:

```tsx
<div
  className="flex flex-wrap gap-2 rounded-md border border-border bg-muted/30 p-2"
  data-testid="channels-biz-member-toolbar"
>
```

Replace the person-channel blocked notice class:

```tsx
className="rounded-lg border border-border bg-muted/40 px-3 py-2 text-sm text-muted-foreground"
```

with:

```tsx
className="rounded-md border border-border bg-muted/30 px-3 py-2 text-sm text-muted-foreground"
```

- [ ] **Step 6: Name and mark the member table**

In the same member section, replace:

```tsx
<div className="overflow-x-auto rounded-lg border border-border">
  <table className="w-full border-collapse">
```

with:

```tsx
<div className="overflow-x-auto rounded-md border border-border" data-channels-biz-surface="members">
  <table aria-label={memberKindLabel(intl, activeMemberKind)} className="w-full border-collapse text-sm">
```

- [ ] **Step 7: Flatten business channel dialog form inputs**

In `web/src/pages/channels-biz/page.tsx`, keep all form names and handlers unchanged, but adjust these classes:

```tsx
className="mt-1 h-9 w-full rounded-md border border-border bg-background px-3"
```

to:

```tsx
className="mt-1 h-9 w-full rounded-md border border-border bg-background px-3 text-sm"
```

```tsx
className="mt-1 h-9 w-full rounded-md border border-border bg-background px-2"
```

to:

```tsx
className="mt-1 h-9 w-full rounded-md border border-border bg-background px-2 text-sm"
```

```tsx
className="mt-1 min-h-28 w-full rounded-md border border-border bg-background px-3 py-2"
```

to:

```tsx
className="mt-1 min-h-28 w-full rounded-md border border-border bg-background px-3 py-2 text-sm"
```

- [ ] **Step 8: Run the business channels test and verify GREEN**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/channels-biz/page.test.tsx
```

Expected: PASS.

- [ ] **Step 9: Commit Task 2**

```bash
git add web/src/pages/channels-biz/page.tsx web/src/pages/channels-biz/page.test.tsx
git commit -m "style(web): refine business channel inventory"
```

---

### Task 3: Connections Compact Inventory Surface

**Files:**
- Modify: `web/src/pages/connections/page.test.tsx`
- Modify: `web/src/pages/connections/page.tsx`

- [ ] **Step 1: Write the failing connections visual-contract test**

In `web/src/pages/connections/page.test.tsx`, add this test after `uses compact connection page chrome without summary cards`:

```tsx
test("uses an editorial connection filter toolbar and inventory table", async () => {
  getConnectionsMock.mockResolvedValueOnce({ total: 1, items: [connectionRow] })

  renderConnectionsPage()

  const table = await screen.findByRole("table", { name: "Connections" })
  const inventorySurface = table.closest("[data-connections-surface='inventory']")
  expect(inventorySurface).toHaveClass("rounded-lg", "border", "border-border", "bg-card", "p-3")
  expect(inventorySurface).not.toHaveClass("rounded-xl")

  const toolbar = screen.getByTestId("connections-filter-toolbar")
  expect(toolbar).toHaveClass("flex", "flex-wrap", "items-center", "gap-2")
  expect(within(toolbar).getByLabelText("Node filter")).toBeInTheDocument()
  expect(within(toolbar).getByRole("button", { name: "Refresh" })).toBeInTheDocument()
})
```

Also update the imports at the top of `web/src/pages/connections/page.test.tsx`:

```tsx
import { render, screen, within } from "@testing-library/react"
```

- [ ] **Step 2: Run the connections test and verify RED**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/connections/page.test.tsx
```

Expected: FAIL because the connections table is not named and `data-connections-surface` / `connections-filter-toolbar` are not present.

- [ ] **Step 3: Mark the compact connection header toolbar**

In `web/src/pages/connections/page.tsx`, replace the page header wrapper:

```tsx
<div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
```

with:

```tsx
<div className="flex flex-col gap-3 border-b border-border pb-4 sm:flex-row sm:items-end sm:justify-between">
```

Then replace the toolbar wrapper:

```tsx
<div className="flex flex-wrap gap-2">
```

with:

```tsx
<div className="flex flex-wrap items-center gap-2" data-testid="connections-filter-toolbar">
```

- [ ] **Step 4: Name and mark the connection inventory table**

In `web/src/pages/connections/page.tsx`, replace:

```tsx
<div className="rounded-xl border border-border bg-card p-3 shadow-none">
```

with:

```tsx
<div className="rounded-lg border border-border bg-card p-3 shadow-none" data-connections-surface="inventory">
```

Then replace:

```tsx
<table className="w-full border-collapse">
```

with:

```tsx
<table aria-label={intl.formatMessage({ id: "nav.connections.title" })} className="w-full border-collapse text-sm">
```

- [ ] **Step 5: Flatten the connection table scroll shell**

In the same inventory block, replace:

```tsx
<div className="overflow-x-auto rounded-lg border border-border">
```

with:

```tsx
<div className="overflow-x-auto rounded-md border border-border">
```

- [ ] **Step 6: Run the connections test and verify GREEN**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/connections/page.test.tsx
```

Expected: PASS.

- [ ] **Step 7: Commit Task 3**

```bash
git add web/src/pages/connections/page.tsx web/src/pages/connections/page.test.tsx
git commit -m "style(web): refine connection inventory"
```

---

### Task 4: Focused Verification And Source-Only Cleanup

**Files:**
- Inspect: `web/dist/index.html`
- Inspect: `git status --short`

- [ ] **Step 1: Run the focused business-page test set**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/users/page.test.tsx src/pages/channels-biz/page.test.tsx src/pages/connections/page.test.tsx
```

Expected: PASS for all three page test files.

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

Expected: PASS. The existing Vite large chunk warning is acceptable if it matches the current warning shape.

- [ ] **Step 4: Restore generated web build hash churn when needed**

Run:

```bash
git status --short web/dist/index.html
```

If the only change in `web/dist/index.html` is Vite asset hash churn, restore that file:

```bash
git restore web/dist/index.html
```

Expected: no intended source change remains under `web/dist`.

- [ ] **Step 5: Run whitespace verification**

Run:

```bash
git diff --check
```

Expected: no whitespace errors.

- [ ] **Step 6: Confirm only intended files changed**

Run:

```bash
git status --short
```

Expected: only the three committed task changes are in history, plus the pre-existing untracked files that are outside this implementation batch:

```text
?? docs/superpowers/plans/2026-07-05-legacy-app-slot-store-adapter.md
?? web/DESIGN.md
```

Do not stage either pre-existing untracked file.
