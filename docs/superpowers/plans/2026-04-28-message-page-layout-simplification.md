# Message Page Layout Simplification Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Simplify the manager Messages page into a compact query-and-results page while preserving URL auto-query, manual search, pagination, and message detail behavior.

**Architecture:** Flatten `MessagesPage` so it uses a compact header, a lightweight inline filter surface, and one flat result surface. Remove duplicated `PageHeader`, summary-card, `SectionCard`, and `TableToolbar` chrome while keeping existing data fetching, validation, payload formatting, and detail sheet flows unchanged.

**Tech Stack:** React 19, TypeScript, React Intl, React Router, existing manager UI components, Vitest, Testing Library, Bun.

---

## Execution Notes

- Create and implement in an isolated git worktree before touching source files.
- Do not modify backend Go files or manager API contracts.
- Do not add, remove, or rename query fields in this pass.
- Do not commit generated `web/dist` changes; restore build hash churn after `bun run build`.
- If `web/dist/index.html` changes after build, restore only that generated file.

## File Structure

- Modify `web/src/pages/messages/page.tsx`
  - Remove `TableToolbar`, `PageHeader`, and `SectionCard` imports/usages.
  - Remove the message summary calculation and summary-card rendering.
  - Keep `useMemo` because payload preview still uses it.
  - Add compact header markup with title, scope, loaded count, and refresh button when a query has been submitted.
  - Replace the filter `SectionCard` with a lightweight inline filter surface.
  - Render loading, error, empty, table, and load-more states in a flat result surface.
  - Keep table columns, query behavior, detail sheet, payload toggle, and clipboard copy unchanged.
- Modify `web/src/pages/messages/page.test.tsx`
  - Add a layout regression test before implementation.
  - Keep existing behavior tests unchanged unless query locations require robust selectors.
- Modify `web/src/i18n/messages/en.ts` only if TypeScript/build reveals missing keys.
- Modify `web/src/i18n/messages/zh-CN.ts` only if English message usage changes.

## Task 1: Add Compact Layout Regression Test

**Files:**
- Test: `web/src/pages/messages/page.test.tsx`

- [ ] **Step 1: Write a failing test for compact message page chrome**

Add this test after `renderMessagesPage`:

```tsx
test("uses compact message page chrome without summary cards", async () => {
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

  expect(await screen.findByText("c-101")).toBeInTheDocument()
  expect(screen.getByText("Scope: channel room-1")).toBeInTheDocument()
  expect(screen.getByText("Loaded: 1")).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Refresh" })).toBeInTheDocument()
  expect(screen.getByLabelText("Channel ID")).toHaveValue("room-1")
  expect(screen.getByLabelText("Channel type")).toHaveValue(2)
  expect(screen.getByRole("button", { name: "Search" })).toBeInTheDocument()
  expect(screen.queryByText("Channel-scoped message search and pagination.")).not.toBeInTheDocument()
  expect(screen.queryByText("Message Filters")).not.toBeInTheDocument()
  expect(screen.queryByText("Filter by channel, message ID, or client message number before querying the manager API.")).not.toBeInTheDocument()
  expect(screen.queryByText("Loaded messages")).not.toBeInTheDocument()
  expect(screen.queryByText("Total matched messages loaded into the current page set.")).not.toBeInTheDocument()
  expect(screen.queryByText("Senders")).not.toBeInTheDocument()
  expect(screen.queryByText("Distinct senders represented in the current result set.")).not.toBeInTheDocument()
  expect(screen.queryByText("Message Query")).not.toBeInTheDocument()
  expect(screen.queryByText("Paged results returned by the manager message query endpoint.")).not.toBeInTheDocument()
  expect(screen.queryByText("Query results")).not.toBeInTheDocument()
  expect(screen.queryByText("Newest matched messages ordered by committed sequence.")).not.toBeInTheDocument()
})
```

Expected current failure: old page description, filter card, summary cards, result card, and toolbar descriptions still render.

- [ ] **Step 2: Run the focused messages test to verify RED**

Run:

```bash
cd web
bun run test src/pages/messages/page.test.tsx
```

Expected: FAIL on the new compact layout test for old chrome still being present.

## Task 2: Flatten Messages Page Layout

**Files:**
- Modify: `web/src/pages/messages/page.tsx`
- Test: `web/src/pages/messages/page.test.tsx`

- [ ] **Step 1: Remove unused layout imports**

In `web/src/pages/messages/page.tsx`, remove:

```tsx
import { TableToolbar } from "@/components/manager/table-toolbar"
import { PageHeader } from "@/components/shell/page-header"
import { SectionCard } from "@/components/shell/section-card"
```

Keep `PageContainer`, `DetailSheet`, `KeyValueList`, `ResourceState`, and `Button`.

- [ ] **Step 2: Remove summary computation**

Delete the `summary` `useMemo` block:

```tsx
const summary = useMemo(() => {
  const items = state.messages?.items ?? []
  return {
    total: items.length,
    senders: new Set(items.map((item) => item.from_uid)).size,
  }
}, [state.messages])
```

Do not remove `useMemo` from the React import because `visiblePayload` still uses it.

- [ ] **Step 3: Replace `PageHeader` with compact header markup**

Replace the existing `PageHeader` block with:

```tsx
<div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
  <div>
    <h1 className="text-xl font-semibold tracking-tight text-foreground">
      {intl.formatMessage({ id: "nav.messages.title" })}
    </h1>
    <div className="mt-1 flex flex-wrap gap-3 text-sm text-muted-foreground">
      <span>
        {submitted
          ? intl.formatMessage({ id: "messages.scopeValue" }, { id: submitted.channelId })
          : intl.formatMessage({ id: "messages.scopePending" })}
      </span>
      <span>
        {state.messages
          ? intl.formatMessage({ id: "messages.loadedValue" }, { count: state.messages.items.length })
          : intl.formatMessage({ id: "messages.loadedPending" })}
      </span>
    </div>
  </div>
  {submitted ? (
    <Button
      onClick={() => {
        void refresh()
      }}
      size="sm"
      variant="outline"
    >
      {state.refreshing
        ? intl.formatMessage({ id: "common.refreshing" })
        : intl.formatMessage({ id: "common.refresh" })}
    </Button>
  ) : null}
</div>
```

- [ ] **Step 4: Replace the filter `SectionCard` with an inline filter surface**

Replace the `SectionCard` wrapping the query form with:

```tsx
<div className="rounded-xl border border-border bg-card p-3 shadow-none">
  <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-[minmax(0,1.2fr)_10rem_10rem_minmax(0,1fr)_auto] xl:items-end">
    <label className="grid gap-2 text-sm text-foreground">
      <span>{intl.formatMessage({ id: "messages.form.channelId" })}</span>
      <input
        aria-label={intl.formatMessage({ id: "messages.form.channelId" })}
        className="min-h-10 rounded-md border border-input bg-background px-3 py-2 text-sm"
        onChange={(event) => {
          setQuery((current) => ({ ...current, channelId: event.target.value }))
        }}
        value={query.channelId}
      />
    </label>
    <label className="grid gap-2 text-sm text-foreground">
      <span>{intl.formatMessage({ id: "messages.form.channelType" })}</span>
      <input
        aria-label={intl.formatMessage({ id: "messages.form.channelType" })}
        className="min-h-10 rounded-md border border-input bg-background px-3 py-2 text-sm"
        onChange={(event) => {
          setQuery((current) => ({ ...current, channelType: event.target.value }))
        }}
        type="number"
        value={query.channelType}
      />
    </label>
    <label className="grid gap-2 text-sm text-foreground">
      <span>{intl.formatMessage({ id: "messages.form.messageId" })}</span>
      <input
        aria-label={intl.formatMessage({ id: "messages.form.messageId" })}
        className="min-h-10 rounded-md border border-input bg-background px-3 py-2 text-sm"
        onChange={(event) => {
          setQuery((current) => ({ ...current, messageId: event.target.value }))
        }}
        type="number"
        value={query.messageId}
      />
    </label>
    <label className="grid gap-2 text-sm text-foreground">
      <span>{intl.formatMessage({ id: "messages.form.clientMsgNo" })}</span>
      <input
        aria-label={intl.formatMessage({ id: "messages.form.clientMsgNo" })}
        className="min-h-10 rounded-md border border-input bg-background px-3 py-2 text-sm"
        onChange={(event) => {
          setQuery((current) => ({ ...current, clientMsgNo: event.target.value }))
        }}
        value={query.clientMsgNo}
      />
    </label>
    <div className="flex flex-wrap items-center gap-3">
      <Button
        onClick={() => {
          void submit()
        }}
      >
        {intl.formatMessage({ id: "common.search" })}
      </Button>
    </div>
  </div>
  {validationError ? <p className="mt-3 text-sm text-destructive">{validationError}</p> : null}
</div>
```

- [ ] **Step 5: Remove message summary cards**

Delete the whole block:

```tsx
{state.messages ? (
  <section className="grid gap-3 md:grid-cols-2 xl:grid-cols-2">
    ...
  </section>
) : null}
```

- [ ] **Step 6: Replace result `SectionCard` and `TableToolbar` with one flat result surface**

Replace the result `SectionCard` and `TableToolbar` wrapper with:

```tsx
<div className="rounded-xl border border-border bg-card p-3 shadow-none">
  {state.loading ? <ResourceState kind="loading" title={intl.formatMessage({ id: "nav.messages.title" })} /> : null}
  {!state.loading && state.error ? (
    <ResourceState
      kind={mapErrorKind(state.error)}
      onRetry={submitted ? () => {
        void refresh()
      } : undefined}
      title={intl.formatMessage({ id: "nav.messages.title" })}
    />
  ) : null}
  {!state.loading && !state.error && !state.queried ? (
    <ResourceState kind="empty" title={intl.formatMessage({ id: "nav.messages.title" })} />
  ) : null}
  {!state.loading && !state.error && state.messages ? (
    state.messages.items.length > 0 ? (
      <>
        <div className="overflow-x-auto rounded-lg border border-border">
          {/* Move the existing messages table here unchanged. */}
        </div>
        {state.messages.has_more ? (
          <div className="mt-3 flex justify-end">
            <Button
              onClick={() => {
                void loadMore()
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
      <ResourceState kind="empty" title={intl.formatMessage({ id: "nav.messages.title" })} />
    )
  ) : null}
</div>
```

Move the existing `<table>` and its columns/rows into the marked `overflow-x-auto` container without changing:

- Column order
- `decodePayload(message.payload)` rendering
- Inspect button label and action
- Row key
- `formatTimestamp()` usage

- [ ] **Step 7: Run messages test to verify GREEN**

Run:

```bash
cd web
bun run test src/pages/messages/page.test.tsx
```

Expected: PASS.

- [ ] **Step 8: Commit layout flattening**

Run:

```bash
git add web/src/pages/messages/page.tsx web/src/pages/messages/page.test.tsx
git commit -m "Simplify messages page layout"
```

## Task 3: Focused Verification

**Files:**
- Potentially modified: `web/dist/index.html` from build; restore if unintended.

- [ ] **Step 1: Run focused web tests**

Run:

```bash
cd web
bun run test src/pages/messages/page.test.tsx src/pages/channels/page.test.tsx src/pages/nodes/page.test.tsx src/lib/manager-api.test.ts
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
