# Web Business Query Pages Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bring `/business/messages`, `/business/conversations`, and `/business/system-users` into the approved editorial console visual language without changing manager API behavior.

**Architecture:** Keep each page's existing data loading, URL auto-query, validation, refresh, pagination, dialogs, and row actions intact. Add narrow visual-contract tests first, then update page-local query toolbars, metadata rows, named tables, and bordered inventory surfaces. Do not touch dashboards, cluster support pages, system support pages, shared API clients, or i18n files.

**Tech Stack:** Vite, React 19, TypeScript, Tailwind CSS v4, Vitest, Testing Library, Bun.

---

## File Structure

- Modify `web/src/pages/messages/page.test.tsx`: add one visual-contract test for the message query toolbar and named inventory table.
- Modify `web/src/pages/messages/page.tsx`: mark the query and inventory surfaces, name the message table, flatten remaining `rounded-xl` shells, and preserve message query/detail/retention behavior.
- Modify `web/src/pages/conversations/page.test.tsx`: add one visual-contract test for the conversations query toolbar, metadata row, and named result table.
- Modify `web/src/pages/conversations/page.tsx`: move refresh into the compact query toolbar, mark the metadata row, name the result table, and flatten the result shell.
- Modify `web/src/pages/system-users/page.test.tsx`: add one visual-contract test for the metadata row and named system UID table.
- Modify `web/src/pages/system-users/page.tsx`: mark the metadata row and inventory table, flatten the table shell, and keep add/remove dialogs behavior unchanged.

No i18n file changes are required because the plan reuses existing labels:

- `nav.messages.title`
- `conversations.title`
- `systemUsers.list.title`
- existing field, action, and table labels already used by tests

Do not modify `web/src/pages/cluster-dashboard/*`, `web/src/pages/business-dashboard/*`, `/cluster/*`, `/system/*`, or generated `web/dist` files in this batch.

---

### Task 1: Messages Editorial Query And Inventory Surfaces

**Files:**
- Modify: `web/src/pages/messages/page.test.tsx`
- Modify: `web/src/pages/messages/page.tsx`

- [ ] **Step 1: Write the failing messages visual-contract test**

In `web/src/pages/messages/page.test.tsx`, add this test after `uses compact message page chrome without summary cards`:

```tsx
test("uses an editorial message query toolbar and named inventory table", async () => {
  getMessagesMock.mockResolvedValueOnce({
    items: [{
      message_id: 101,
      message_seq: 9,
      client_msg_no: "c-101",
      channel_id: "room-1",
      channel_type: 2,
      from_uid: "u1",
      timestamp: 1713859200,
      payload: "aGVsbG8=",
    }],
    has_more: false,
  })

  renderMessagesPage("/messages?channel_id=room-1&channel_type=2")

  const querySurface = screen.getByTestId("messages-query-surface")
  expect(querySurface).toHaveClass("rounded-lg", "border", "border-border", "bg-card", "p-3")
  expect(querySurface).not.toHaveClass("rounded-xl")

  const queryToolbar = screen.getByTestId("messages-query-toolbar")
  expect(queryToolbar).toHaveClass("grid", "gap-3")
  expect(within(queryToolbar).getByLabelText("Channel ID")).toHaveValue("room-1")
  expect(within(queryToolbar).getByLabelText("Channel type")).toHaveValue(2)
  expect(within(queryToolbar).getByRole("button", { name: "Search" })).toBeInTheDocument()

  const table = await screen.findByRole("table", { name: "Messages" })
  const inventorySurface = table.closest("[data-messages-surface='inventory']")
  expect(inventorySurface).toHaveClass("rounded-lg", "border", "border-border", "bg-card", "p-3")
  expect(inventorySurface).not.toHaveClass("rounded-xl")
  expect(table).toHaveClass("table-fixed", "text-sm")
})
```

- [ ] **Step 2: Run the messages test and verify RED**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/messages/page.test.tsx
```

Expected: FAIL because `messages-query-surface`, `messages-query-toolbar`, `data-messages-surface`, and the named `Messages` table do not exist yet.

- [ ] **Step 3: Mark and flatten the messages query surface**

In `web/src/pages/messages/page.tsx`, replace the query shell:

```tsx
<div className="rounded-xl border border-border bg-card p-3 shadow-none">
```

with:

```tsx
<div
  className="rounded-lg border border-border bg-card p-3 shadow-none"
  data-messages-surface="query"
  data-testid="messages-query-surface"
>
```

Then replace the query grid opening:

```tsx
<div className="grid gap-3 md:grid-cols-2 xl:grid-cols-[minmax(0,1.2fr)_10rem_10rem_minmax(0,1fr)_auto] xl:items-end">
```

with:

```tsx
<div
  className="grid gap-3 md:grid-cols-2 xl:grid-cols-[minmax(0,1.2fr)_10rem_10rem_minmax(0,1fr)_auto] xl:items-end"
  data-testid="messages-query-toolbar"
>
```

- [ ] **Step 4: Mark and flatten the messages result surface**

In the same file, replace the result shell:

```tsx
<div className="rounded-xl border border-border bg-card p-3 shadow-none">
```

with:

```tsx
<div className="rounded-lg border border-border bg-card p-3 shadow-none" data-messages-surface="inventory">
```

Only replace the second `rounded-xl` page-local shell, the one that wraps loading/error/result table content.

- [ ] **Step 5: Name and flatten the message table**

In `web/src/pages/messages/page.tsx`, replace:

```tsx
<div className="overflow-x-auto rounded-lg border border-border">
  <table className="w-full min-w-[1080px] table-fixed border-collapse">
```

with:

```tsx
<div className="overflow-x-auto rounded-md border border-border">
  <table
    aria-label={intl.formatMessage({ id: "nav.messages.title" })}
    className="w-full min-w-[1080px] table-fixed border-collapse text-sm"
  >
```

- [ ] **Step 6: Flatten message feedback and payload panel surfaces**

In the same file, replace the retention success notice:

```tsx
<div className="mb-3 rounded-lg border border-border bg-muted/30 px-3 py-2 text-sm text-foreground">
```

with:

```tsx
<div className="mb-3 rounded-md border border-border bg-muted/30 px-3 py-2 text-sm text-foreground">
```

Replace the payload detail section opening:

```tsx
<section className="rounded-lg border border-border bg-card">
```

with:

```tsx
<section className="rounded-md border border-border bg-card">
```

- [ ] **Step 7: Run the messages test and verify GREEN**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/messages/page.test.tsx
```

Expected: PASS.

- [ ] **Step 8: Commit Task 1**

```bash
git add web/src/pages/messages/page.tsx web/src/pages/messages/page.test.tsx
git commit -m "style(web): refine message query surface"
```

---

### Task 2: Conversations Editorial Query Workbench

**Files:**
- Modify: `web/src/pages/conversations/page.test.tsx`
- Modify: `web/src/pages/conversations/page.tsx`

- [ ] **Step 1: Write the failing conversations visual-contract test**

In `web/src/pages/conversations/page.test.tsx`, update the Testing Library import:

```tsx
import { render, screen, within } from "@testing-library/react"
```

Then add this test after `queries conversations by UID and renders previews`:

```tsx
test("uses an editorial conversations query toolbar and named result table", async () => {
  getRecentConversationsMock.mockResolvedValueOnce(conversationPage)

  const user = userEvent.setup()
  renderConversationsPage()

  const toolbar = screen.getByTestId("conversations-query-toolbar")
  expect(toolbar).toHaveClass("grid", "gap-3", "border-b", "border-border", "pb-4")
  await user.type(within(toolbar).getByPlaceholderText("Search UID"), "u1")
  await user.click(within(toolbar).getByRole("button", { name: "Search" }))

  const table = await screen.findByRole("table", { name: "Recent Conversations" })
  const resultSurface = table.closest("[data-conversations-surface='results']")
  expect(resultSurface).toHaveClass("overflow-x-auto", "rounded-md", "border", "border-border")
  expect(resultSurface).not.toHaveClass("rounded-xl")

  const metadataRow = screen.getByTestId("conversations-metadata-row")
  expect(metadataRow).toHaveClass("border-b", "border-border", "pb-3")
  expect(within(metadataRow).getByText("Loaded: 1")).toBeInTheDocument()
  expect(within(metadataRow).getByText("More conversations matched; increase the limit to inspect a larger working set.")).toBeInTheDocument()
  expect(within(toolbar).getByRole("button", { name: "Refresh" })).toBeInTheDocument()
})
```

- [ ] **Step 2: Run the conversations test and verify RED**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/conversations/page.test.tsx
```

Expected: FAIL because `conversations-query-toolbar`, `conversations-metadata-row`, `data-conversations-surface`, and the named table do not exist yet.

- [ ] **Step 3: Remove header refresh action and keep PageHeader open**

In `web/src/pages/conversations/page.tsx`, replace the current `PageHeader` block:

```tsx
<PageHeader
  actions={submitted ? (
    <Button onClick={refresh} size="sm" variant="outline">
      {state.refreshing ? intl.formatMessage({ id: "common.refreshing" }) : intl.formatMessage({ id: "common.refresh" })}
    </Button>
  ) : null}
  description={intl.formatMessage({ id: "conversations.description" })}
  title={intl.formatMessage({ id: "conversations.title" })}
/>
```

with:

```tsx
<PageHeader
  description={intl.formatMessage({ id: "conversations.description" })}
  title={intl.formatMessage({ id: "conversations.title" })}
/>
```

- [ ] **Step 4: Mark the conversations section and query toolbar**

Replace:

```tsx
<SectionCard description={summary} title={intl.formatMessage({ id: "conversations.title" })}>
  <form className="mb-4 grid gap-3 md:grid-cols-[minmax(0,1fr)_8rem_10rem_10rem_auto] md:items-end" onSubmit={submitSearch}>
```

with:

```tsx
<SectionCard
  className="overflow-hidden"
  description={summary}
  title={intl.formatMessage({ id: "conversations.title" })}
>
  <form
    className="mb-4 grid gap-3 border-b border-border pb-4 md:grid-cols-[minmax(0,1fr)_8rem_10rem_10rem_auto] md:items-end"
    data-testid="conversations-query-toolbar"
    onSubmit={submitSearch}
  >
```

- [ ] **Step 5: Move refresh into the conversations toolbar**

In `web/src/pages/conversations/page.tsx`, replace the single search button at the end of the form:

```tsx
<Button size="sm" type="submit">{intl.formatMessage({ id: "common.search" })}</Button>
```

with:

```tsx
<div className="flex flex-wrap items-center gap-2">
  <Button size="sm" type="submit">{intl.formatMessage({ id: "common.search" })}</Button>
  {submitted ? (
    <Button onClick={refresh} size="sm" type="button" variant="outline">
      {state.refreshing
        ? intl.formatMessage({ id: "common.refreshing" })
        : intl.formatMessage({ id: "common.refresh" })}
    </Button>
  ) : null}
</div>
```

- [ ] **Step 6: Mark the conversations metadata row**

Replace:

```tsx
<div className="mb-3 flex flex-wrap gap-3 text-sm text-muted-foreground">
```

with:

```tsx
<div
  className="mb-3 flex flex-wrap gap-3 border-b border-border pb-3 text-sm text-muted-foreground"
  data-testid="conversations-metadata-row"
>
```

- [ ] **Step 7: Name and mark the conversations result table**

Replace:

```tsx
<div className="overflow-x-auto rounded-lg border border-border">
  <table className="w-full border-collapse">
```

with:

```tsx
<div className="overflow-x-auto rounded-md border border-border" data-conversations-surface="results">
  <table aria-label={intl.formatMessage({ id: "conversations.title" })} className="w-full border-collapse text-sm">
```

- [ ] **Step 8: Run the conversations test and verify GREEN**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/conversations/page.test.tsx
```

Expected: PASS.

- [ ] **Step 9: Commit Task 2**

```bash
git add web/src/pages/conversations/page.tsx web/src/pages/conversations/page.test.tsx
git commit -m "style(web): refine conversations query surface"
```

---

### Task 3: System Users Editorial Inventory Surface

**Files:**
- Modify: `web/src/pages/system-users/page.test.tsx`
- Modify: `web/src/pages/system-users/page.tsx`

- [ ] **Step 1: Write the failing system-users visual-contract test**

In `web/src/pages/system-users/page.test.tsx`, add this test after `renders persisted system users`:

```tsx
test("uses an editorial system users metadata row and named table", async () => {
  getSystemUsersMock.mockResolvedValueOnce({ items: [{ uid: "sys-a" }], total: 1 })

  renderSystemUsersPage()

  const table = await screen.findByRole("table", { name: "Persisted system UIDs" })
  const inventorySurface = table.closest("[data-system-users-surface='inventory']")
  expect(inventorySurface).toHaveClass("overflow-x-auto", "rounded-md", "border", "border-border")
  expect(inventorySurface).not.toHaveClass("rounded-xl")

  const metadataRow = screen.getByTestId("system-users-metadata-row")
  expect(metadataRow).toHaveClass("border-b", "border-border", "pb-4")
  expect(within(metadataRow).getByText("1 persisted UID")).toBeInTheDocument()
  expect(within(metadataRow).getByText("Cache-only legacy operations are intentionally not exposed here.")).toBeInTheDocument()
})
```

- [ ] **Step 2: Run the system-users test and verify RED**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/system-users/page.test.tsx
```

Expected: FAIL because the table is not named and `system-users-metadata-row` / `data-system-users-surface` do not exist yet.

- [ ] **Step 3: Mark the system-users SectionCard and metadata row**

In `web/src/pages/system-users/page.tsx`, replace:

```tsx
<SectionCard
  description={intl.formatMessage({ id: "systemUsers.list.description" })}
  title={intl.formatMessage({ id: "systemUsers.list.title" })}
>
  <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
```

with:

```tsx
<SectionCard
  className="overflow-hidden"
  description={intl.formatMessage({ id: "systemUsers.list.description" })}
  title={intl.formatMessage({ id: "systemUsers.list.title" })}
>
  <div
    className="mb-4 flex flex-wrap items-center justify-between gap-3 border-b border-border pb-4"
    data-testid="system-users-metadata-row"
  >
```

- [ ] **Step 4: Flatten the system-users count pill**

Replace:

```tsx
<div className="rounded-full border border-border bg-muted/40 px-3 py-1 text-sm font-medium text-foreground">
```

with:

```tsx
<div className="font-mono text-sm font-semibold text-foreground">
```

- [ ] **Step 5: Name and mark the system-users table**

Replace:

```tsx
<div className="overflow-x-auto rounded-lg border border-border">
  <table className="w-full border-collapse">
```

with:

```tsx
<div className="overflow-x-auto rounded-md border border-border" data-system-users-surface="inventory">
  <table aria-label={intl.formatMessage({ id: "systemUsers.list.title" })} className="w-full border-collapse text-sm">
```

- [ ] **Step 6: Run the system-users test and verify GREEN**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/system-users/page.test.tsx
```

Expected: PASS.

- [ ] **Step 7: Commit Task 3**

```bash
git add web/src/pages/system-users/page.tsx web/src/pages/system-users/page.test.tsx
git commit -m "style(web): refine system users inventory"
```

---

### Task 4: Focused Verification And Source-Only Cleanup

**Files:**
- Inspect: `web/dist/index.html`
- Inspect: `git status --short`

- [ ] **Step 1: Run the focused business query page tests**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/messages/page.test.tsx src/pages/conversations/page.test.tsx src/pages/system-users/page.test.tsx
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

Expected: only committed source/test changes are in history, plus the pre-existing untracked files that are outside this implementation batch:

```text
?? docs/superpowers/plans/2026-07-05-legacy-app-slot-store-adapter.md
?? web/DESIGN.md
```

Do not stage either pre-existing untracked file.
