# System DB UI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Improve `/system/db` scanning ergonomics and visual density without changing DB inspect request behavior.

**Architecture:** Keep all runtime behavior in `web/src/pages/db-inspect/page.tsx`. Add small local helpers for table filtering and stats formatting, with visible copy provided through existing i18n message files. Extend focused page tests so the UI contract is proven without coupling to every Tailwind class.

**Tech Stack:** React 19, TypeScript, React Intl, lucide-react, Tailwind classes, Vitest, Testing Library.

---

## File Structure

- Modify `web/src/pages/db-inspect/page.tsx`: add table search state, filtered table rail, selected table styling, compact workbench metadata, and denser result table surfaces.
- Modify `web/src/pages/db-inspect/page.test.tsx`: cover table filtering, selected table state, empty match state, and preserved query/describe behavior.
- Modify `web/src/i18n/messages/en.ts`: add English DB inspect UI labels.
- Modify `web/src/i18n/messages/zh-CN.ts`: add Chinese DB inspect UI labels.

## Tasks

### Task 1: Lock The New Table Rail Behavior

**Files:**
- Modify: `web/src/pages/db-inspect/page.test.tsx`

- [x] **Step 1: Add a failing test for search, counts, and selected state**

Add this test near the existing editorial rail test:

```tsx
test("filters table rail and marks the inspected table", async () => {
  getNodesMock.mockResolvedValueOnce(nodesResponse())
  getDBInspectTablesMock.mockResolvedValueOnce({
    node_id: 1,
    generated_at: "2026-06-17T10:00:00Z",
    rows: [
      { domain: "meta", name: "user", table: "meta.user" },
      { domain: "meta", name: "channel", table: "meta.channel" },
      { domain: "message", name: "message", table: "message.message" },
    ],
    stats: { scan_mode: "", scanned_hash_slots: [], scanned_rows: 0, returned_rows: 3, has_more: false, next_cursor: "" },
  })
  getDBInspectTableMock.mockResolvedValueOnce({
    node_id: 1,
    generated_at: "2026-06-17T10:00:00Z",
    rows: [{ column: "uid", type: "string" }],
    stats: { scan_mode: "", scanned_hash_slots: [], scanned_rows: 1, returned_rows: 1, has_more: false, next_cursor: "" },
  })

  const user = userEvent.setup()
  renderPage()

  expect(await screen.findByText("3 tables")).toBeInTheDocument()
  await user.type(screen.getByLabelText("Search tables"), "user")
  expect(screen.getByText("1 of 3 tables")).toBeInTheDocument()
  expect(screen.getByText("meta.user")).toBeInTheDocument()
  expect(screen.queryByText("meta.channel")).not.toBeInTheDocument()

  const tableButton = screen.getByRole("button", { name: "Inspect meta.user" })
  await user.click(tableButton)
  expect(tableButton).toHaveAttribute("aria-current", "true")
})
```

- [x] **Step 2: Run the focused test and verify it fails**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/db-inspect/page.test.tsx
```

Expected: the new test fails because `Search tables`, `3 tables`, and `aria-current` are not implemented yet.

### Task 2: Implement The Table Rail

**Files:**
- Modify: `web/src/pages/db-inspect/page.tsx`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`

- [x] **Step 1: Add page state and helpers**

Extend `PageState`:

```ts
type PageState = {
  nodes: ManagerNode[]
  tables: TableRow[]
  selectedNodeId: number
  tableFilter: string
  query: string
  result: ManagerDBInspectQueryResponse | null
  describe: ManagerDBInspectQueryResponse | null
  selectedTable: string
  loading: boolean
  running: boolean
  describing: boolean
  error: Error | null
}
```

Initialize `tableFilter: ""`.

Add helpers below `groupTables`:

```ts
function filterTables(rows: TableRow[], filter: string) {
  const normalized = filter.trim().toLowerCase()
  if (!normalized) {
    return rows
  }
  return rows.filter((row) => (
    row.table.toLowerCase().includes(normalized)
      || row.domain.toLowerCase().includes(normalized)
      || row.name.toLowerCase().includes(normalized)
  ))
}

function formatTableCount(intl: IntlShape, filtered: number, total: number) {
  if (filtered === total) {
    return intl.formatMessage({ id: "dbInspect.tables.count" }, { count: total })
  }
  return intl.formatMessage({ id: "dbInspect.tables.filteredCount" }, { filtered, total })
}
```

- [x] **Step 2: Add i18n messages**

In `web/src/i18n/messages/en.ts`, add:

```ts
"dbInspect.tables.search": "Search tables",
"dbInspect.tables.count": "{count, plural, one {# table} other {# tables}}",
"dbInspect.tables.filteredCount": "{filtered} of {total} tables",
"dbInspect.tables.emptyFilter": "No tables match this search.",
```

In `web/src/i18n/messages/zh-CN.ts`, add:

```ts
"dbInspect.tables.search": "搜索表",
"dbInspect.tables.count": "{count} 张表",
"dbInspect.tables.filteredCount": "{filtered}/{total} 张表",
"dbInspect.tables.emptyFilter": "没有匹配的表。",
```

- [x] **Step 3: Update rail markup**

Use `filteredTables` and `filteredTablesByDomain`, render a compact search input, count row, empty-filter state, and `aria-current` on the selected table button.

- [x] **Step 4: Run the rail test**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/db-inspect/page.test.tsx
```

Expected: the new table rail test passes with the existing tests.

### Task 3: Polish Query Workbench And Result Surfaces

**Files:**
- Modify: `web/src/pages/db-inspect/page.tsx`
- Modify: `web/src/pages/db-inspect/page.test.tsx`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`

- [x] **Step 1: Add failing test assertions for compact result metadata**

Extend `uses editorial db inspect rail workbench and named result tables` with:

```tsx
expect(screen.getByText("Query templates")).toBeInTheDocument()
expect(screen.getByText("Node 1")).toBeInTheDocument()
expect(screen.getByText("generated 2026-06-17T10:00:01Z")).toBeInTheDocument()
expect(screen.getByText("1 row")).toBeInTheDocument()
```

- [x] **Step 2: Add i18n messages**

In `web/src/i18n/messages/en.ts`, add:

```ts
"dbInspect.templates.title": "Query templates",
"dbInspect.meta.node": "Node {id}",
"dbInspect.meta.generated": "generated {time}",
"dbInspect.meta.rows": "{count, plural, one {# row} other {# rows}}",
```

In `web/src/i18n/messages/zh-CN.ts`, add:

```ts
"dbInspect.templates.title": "查询模板",
"dbInspect.meta.node": "节点 {id}",
"dbInspect.meta.generated": "生成于 {time}",
"dbInspect.meta.rows": "{count} 行",
```

- [x] **Step 3: Update result metadata and table density**

Add metadata chips inside `StatsStrip` for `node_id`, `generated_at`, and returned rows. Add a small "Query templates" label above template buttons. Keep existing button labels and table accessible names unchanged.

- [x] **Step 4: Run page tests**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/db-inspect/page.test.tsx src/lib/manager-api.test.ts
```

Expected: all tests pass.

### Task 4: Final Verification

**Files:**
- Verify: `web/src/pages/db-inspect/page.tsx`
- Verify: `web/src/pages/db-inspect/page.test.tsx`
- Verify: `web/src/i18n/messages/en.ts`
- Verify: `web/src/i18n/messages/zh-CN.ts`

- [x] **Step 1: Type-check frontend**

Run:

```bash
cd web && /Users/tt/.bun/bin/bunx tsc -b
```

Expected: exits 0.

- [x] **Step 2: Run focused tests**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/db-inspect/page.test.tsx src/lib/manager-api.test.ts
```

Expected: exits 0.

- [x] **Step 3: Check whitespace**

Run:

```bash
git diff --check
```

Expected: exits 0.
