# Plugin Management Page Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Redesign `/cluster/plugins` into a simpler operator page with a compact node-local plugin inventory, local filters, and the existing UID binding workflow.

**Architecture:** Keep all behavior on the current manager API boundary. Add local derived filtering over the selected node's loaded plugin `items`, reduce the main inventory table to high-signal columns, and move low-frequency runtime/config fields into the existing detail sheet. Preserve existing mutation dialogs and refresh behavior.

**Tech Stack:** React 19, TypeScript, Vite, Vitest, Testing Library, `react-intl`, Tailwind utility classes, existing WuKongIM manager web components.

---

## File Structure

- Modify `web/src/pages/plugins/page.test.tsx`: add regression tests for local filtering and compact inventory/detail behavior.
- Modify `web/src/pages/plugins/page.tsx`: add local filter state/helpers, compact inventory table rendering, and small internal components for summary/filter/table/binding areas.
- Modify `web/src/i18n/messages/en.ts`: add filter labels and updated copy used by the redesigned plugin page.
- Modify `web/src/i18n/messages/zh-CN.ts`: add matching Chinese strings.
- No backend files change. No manager API client changes are required.

## Task 1: Plugin Inventory Filters

**Files:**
- Modify: `web/src/pages/plugins/page.test.tsx`
- Modify: `web/src/pages/plugins/page.tsx`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`

- [ ] **Step 1: Write the failing filter test**

Add this test to `web/src/pages/plugins/page.test.tsx` after `renders node plugin inventory with summary counts`:

```tsx
test("filters plugin inventory locally by keyword status and method", async () => {
  getNodePluginsMock.mockResolvedValueOnce({ node_id: 2, total: 2, items: [pluginRow, failedPluginRow] })

  const user = userEvent.setup()
  renderPluginsPage()

  expect(await screen.findByText("wk.echo")).toBeInTheDocument()
  expect(screen.getByText("wk.ai.reply")).toBeInTheDocument()

  await user.type(screen.getByLabelText("Plugin keyword"), "ai")
  expect(screen.queryByText("wk.echo")).not.toBeInTheDocument()
  expect(screen.getByText("wk.ai.reply")).toBeInTheDocument()

  await user.selectOptions(screen.getByLabelText("Status filter"), "running")
  expect(await screen.findByText("No manager data is available for this view yet.")).toBeInTheDocument()

  await user.selectOptions(screen.getByLabelText("Status filter"), "failed")
  expect(screen.getByText("wk.ai.reply")).toBeInTheDocument()

  await user.selectOptions(screen.getByLabelText("Method filter"), "Receive")
  expect(screen.getByText("wk.ai.reply")).toBeInTheDocument()

  expect(getNodePluginsMock).toHaveBeenCalledTimes(1)
})
```

- [ ] **Step 2: Run the filter test to verify it fails**

Run:

```bash
cd web && bun run test -- src/pages/plugins/page.test.tsx -t "filters plugin inventory locally by keyword status and method"
```

Expected: FAIL because `Plugin keyword`, `Status filter`, and `Method filter` controls do not exist yet.

- [ ] **Step 3: Add filter strings**

In `web/src/i18n/messages/en.ts`, add:

```ts
  "plugins.filters.keyword": "Plugin keyword",
  "plugins.filters.keywordPlaceholder": "Search plugin, method, or error",
  "plugins.filters.status": "Status filter",
  "plugins.filters.status.all": "All statuses",
  "plugins.filters.method": "Method filter",
  "plugins.filters.method.all": "All methods",
  "plugins.filteredEmpty": "No plugins match the current filters.",
```

In `web/src/i18n/messages/zh-CN.ts`, add:

```ts
  "plugins.filters.keyword": "插件关键字",
  "plugins.filters.keywordPlaceholder": "搜索插件、方法或错误",
  "plugins.filters.status": "状态筛选",
  "plugins.filters.status.all": "全部状态",
  "plugins.filters.method": "方法筛选",
  "plugins.filters.method.all": "全部方法",
  "plugins.filteredEmpty": "当前筛选条件下没有插件。",
```

- [ ] **Step 4: Add local filter helpers and controls**

In `web/src/pages/plugins/page.tsx`, add these helpers near the existing plugin summary helpers:

```tsx
const allFilterValue = "__all__"

type PluginInventoryFilters = {
  keyword: string
  status: string
  method: string
}

function pluginFilterOptions(page: ManagerNodePluginsResponse | null) {
  const statuses = new Set<string>()
  const methods = new Set<string>()
  for (const plugin of page?.items ?? []) {
    if (plugin.status) {
      statuses.add(plugin.status)
    }
    for (const method of plugin.methods) {
      methods.add(method)
    }
  }
  return {
    statuses: Array.from(statuses).sort(),
    methods: Array.from(methods).sort(),
  }
}

function pluginMatchesFilters(plugin: ManagerPlugin, filters: PluginInventoryFilters) {
  const keyword = filters.keyword.trim().toLowerCase()
  const keywordMatches = !keyword || [
    plugin.plugin_no,
    plugin.name,
    plugin.version,
    plugin.last_error,
    ...plugin.methods,
  ].some((value) => value.toLowerCase().includes(keyword))

  const statusMatches = filters.status === allFilterValue || plugin.status === filters.status
  const methodMatches = filters.method === allFilterValue || plugin.methods.includes(filters.method)
  return keywordMatches && statusMatches && methodMatches
}
```

Inside `PluginsPage`, add state and derived values near `summary`:

```tsx
  const [filters, setFilters] = useState<PluginInventoryFilters>({
    keyword: "",
    status: allFilterValue,
    method: allFilterValue,
  })
  const filterOptions = useMemo(() => pluginFilterOptions(state.page), [state.page])
  const filteredPlugins = useMemo(
    () => (state.page?.items ?? []).filter((plugin) => pluginMatchesFilters(plugin, filters)),
    [filters, state.page],
  )
```

Inside the inventory `SectionCard`, render a filter bar before the resource state:

```tsx
        <div className="mb-4 grid gap-3 lg:grid-cols-[minmax(220px,1fr)_180px_180px]">
          <label className="flex min-w-0 flex-col gap-1 text-xs font-medium text-muted-foreground" htmlFor="plugin-keyword-filter">
            {intl.formatMessage({ id: "plugins.filters.keyword" })}
            <input
              className="h-8 rounded-md border border-border bg-background px-2 text-sm text-foreground outline-none focus:border-ring focus:ring-2 focus:ring-ring/30"
              id="plugin-keyword-filter"
              onChange={(event) => setFilters((current) => ({ ...current, keyword: event.target.value }))}
              placeholder={intl.formatMessage({ id: "plugins.filters.keywordPlaceholder" })}
              value={filters.keyword}
            />
          </label>
          <label className="flex flex-col gap-1 text-xs font-medium text-muted-foreground" htmlFor="plugin-status-filter">
            {intl.formatMessage({ id: "plugins.filters.status" })}
            <select
              className="h-8 rounded-md border border-border bg-background px-2 text-sm text-foreground outline-none focus:border-ring focus:ring-2 focus:ring-ring/30"
              id="plugin-status-filter"
              onChange={(event) => setFilters((current) => ({ ...current, status: event.target.value }))}
              value={filters.status}
            >
              <option value={allFilterValue}>{intl.formatMessage({ id: "plugins.filters.status.all" })}</option>
              {filterOptions.statuses.map((status) => <option key={status} value={status}>{status}</option>)}
            </select>
          </label>
          <label className="flex flex-col gap-1 text-xs font-medium text-muted-foreground" htmlFor="plugin-method-filter">
            {intl.formatMessage({ id: "plugins.filters.method" })}
            <select
              className="h-8 rounded-md border border-border bg-background px-2 text-sm text-foreground outline-none focus:border-ring focus:ring-2 focus:ring-ring/30"
              id="plugin-method-filter"
              onChange={(event) => setFilters((current) => ({ ...current, method: event.target.value }))}
              value={filters.method}
            >
              <option value={allFilterValue}>{intl.formatMessage({ id: "plugins.filters.method.all" })}</option>
              {filterOptions.methods.map((method) => <option key={method} value={method}>{method}</option>)}
            </select>
          </label>
        </div>
```

Update the plugin list branch to render `filteredPlugins` instead of `state.page.items`. When the loaded page has rows but filters remove all rows, show:

```tsx
<ResourceState kind="empty" title={intl.formatMessage({ id: "plugins.filteredEmpty" })} />
```

- [ ] **Step 5: Run the filter test to verify it passes**

Run:

```bash
cd web && bun run test -- src/pages/plugins/page.test.tsx -t "filters plugin inventory locally by keyword status and method"
```

Expected: PASS.

## Task 2: Compact Inventory Table

**Files:**
- Modify: `web/src/pages/plugins/page.test.tsx`
- Modify: `web/src/pages/plugins/page.tsx`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`

- [ ] **Step 1: Write the failing compact table test**

Add this test to `web/src/pages/plugins/page.test.tsx` after the filter test:

```tsx
test("keeps low frequency plugin fields in the detail sheet instead of the inventory table", async () => {
  getNodePluginsMock.mockResolvedValueOnce({ node_id: 2, total: 1, items: [pluginRow] })
  getNodePluginMock.mockResolvedValueOnce(pluginRow)

  const user = userEvent.setup()
  renderPluginsPage()

  expect(await screen.findByText("wk.echo")).toBeInTheDocument()
  expect(screen.queryByRole("columnheader", { name: "Priority" })).not.toBeInTheDocument()
  expect(screen.queryByRole("columnheader", { name: "PID" })).not.toBeInTheDocument()
  expect(screen.queryByRole("columnheader", { name: "Enabled" })).not.toBeInTheDocument()

  await user.click(screen.getByRole("button", { name: "View plugin wk.echo details" }))

  const dialog = await screen.findByRole("dialog")
  expect(within(dialog).getByText("Priority")).toBeInTheDocument()
  expect(within(dialog).getByText("7")).toBeInTheDocument()
  expect(within(dialog).getByText("PID")).toBeInTheDocument()
  expect(within(dialog).getByText("123")).toBeInTheDocument()
})
```

- [ ] **Step 2: Run the compact table test to verify it fails**

Run:

```bash
cd web && bun run test -- src/pages/plugins/page.test.tsx -t "keeps low frequency plugin fields in the detail sheet"
```

Expected: FAIL because the current inventory table still has `Priority`, `PID`, and `Enabled` column headers.

- [ ] **Step 3: Update table strings**

In `web/src/i18n/messages/en.ts`, keep existing keys and add:

```ts
  "plugins.table.capabilities": "Capabilities",
  "plugins.table.lastSignal": "Last signal",
```

In `web/src/i18n/messages/zh-CN.ts`, add:

```ts
  "plugins.table.capabilities": "能力",
  "plugins.table.lastSignal": "最近信号",
```

- [ ] **Step 4: Replace the wide plugin table with compact columns**

In `web/src/pages/plugins/page.tsx`, replace the plugin inventory table header with:

```tsx
                  <tr>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "plugins.table.plugin" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "plugins.table.status" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "plugins.table.capabilities" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "plugins.table.lastSignal" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "plugins.table.actions" })}</th>
                  </tr>
```

Replace each row body with:

```tsx
                    <tr className="border-t border-border" key={plugin.plugin_no}>
                      <td className="px-3 py-3 text-sm">
                        <div className="font-medium text-foreground">{plugin.plugin_no}</div>
                        <div className="text-xs text-muted-foreground">{plugin.name} · {plugin.version}</div>
                      </td>
                      <td className="px-3 py-3 text-sm">
                        <div className="flex flex-col gap-1">
                          <StatusBadge value={plugin.status} />
                          <span className="text-xs text-muted-foreground">
                            {intl.formatMessage({ id: plugin.enabled ? "plugins.enabled.yes" : "plugins.enabled.no" })}
                          </span>
                        </div>
                      </td>
                      <td className="px-3 py-3 text-sm text-muted-foreground">
                        {formatMethods(plugin, intl)}
                      </td>
                      <td className="px-3 py-3 text-sm text-muted-foreground">
                        <div>{formatTimestamp(intl, plugin.last_seen_at)}</div>
                        {plugin.last_error ? <div className="mt-1 max-w-[280px] truncate text-xs text-destructive">{plugin.last_error}</div> : null}
                      </td>
                      <td className="px-3 py-3 text-sm">
                        <div className="flex flex-wrap gap-2">
                          {/* keep the existing Details, Configure, Restart, and Uninstall buttons unchanged */}
                        </div>
                      </td>
                    </tr>
```

Keep the existing button implementations inside the actions `div`.

- [ ] **Step 5: Run the compact table test to verify it passes**

Run:

```bash
cd web && bun run test -- src/pages/plugins/page.test.tsx -t "keeps low frequency plugin fields in the detail sheet"
```

Expected: PASS.

## Task 3: Binding Query Stays Operational After Redesign

**Files:**
- Modify: `web/src/pages/plugins/page.test.tsx`
- Modify: `web/src/pages/plugins/page.tsx`

- [ ] **Step 1: Run existing binding tests before refactor**

Run:

```bash
cd web && bun run test -- src/pages/plugins/page.test.tsx -t "queries plugin bindings by UID and plugin number"
```

Expected: PASS before refactor. This confirms the binding behavior baseline.

- [ ] **Step 2: Extract small internal rendering helpers without changing binding behavior**

Inside `web/src/pages/plugins/page.tsx`, extract a local helper for the filter bar and a local helper for summary pills. Keep all binding state, mutation functions, and refresh behavior in `PluginsPage`.

Use this component shape for the filter bar:

```tsx
function PluginInventoryFiltersBar({
  filters,
  intl,
  onChange,
  options,
}: {
  filters: PluginInventoryFilters
  intl: IntlShape
  onChange: (filters: PluginInventoryFilters) => void
  options: ReturnType<typeof pluginFilterOptions>
}) {
  return (
    <div className="mb-4 grid gap-3 lg:grid-cols-[minmax(220px,1fr)_180px_180px]">
      {/* move the working keyword/status/method controls here unchanged */}
    </div>
  )
}
```

Use it from `PluginsPage` as:

```tsx
        <PluginInventoryFiltersBar
          filters={filters}
          intl={intl}
          onChange={setFilters}
          options={filterOptions}
        />
```

- [ ] **Step 3: Run binding tests after refactor**

Run:

```bash
cd web && bun run test -- src/pages/plugins/page.test.tsx -t "queries plugin bindings by UID and plugin number"
cd web && bun run test -- src/pages/plugins/page.test.tsx -t "adds and deletes plugin bindings"
```

Expected: both PASS.

## Task 4: Focused Verification And Commit

**Files:**
- Verify: `web/src/pages/plugins/page.test.tsx`
- Verify: `web/src/lib/manager-api.test.ts`
- Verify: `web/src/pages/plugins/page.tsx`
- Verify: `web/src/i18n/messages/en.ts`
- Verify: `web/src/i18n/messages/zh-CN.ts`

- [ ] **Step 1: Run focused page/API tests**

Run:

```bash
cd web && bun run test -- src/lib/manager-api.test.ts src/pages/plugins/page.test.tsx
```

Expected: PASS for both test files.

- [ ] **Step 2: Run web build**

Run:

```bash
cd web && bun run build
```

Expected: PASS. An existing Vite chunk-size warning is acceptable if the build exits 0.

- [ ] **Step 3: Restore generated Vite hash churn if present**

Run:

```bash
git status --short web/dist/index.html
```

If only `web/dist/index.html` changed because of build hash output, restore that generated file:

```bash
git restore web/dist/index.html
```

Expected: no source changes are lost.

- [ ] **Step 4: Run whitespace check**

Run:

```bash
git diff --check
```

Expected: no output and exit code 0.

- [ ] **Step 5: Review final diff**

Run:

```bash
git diff -- web/src/pages/plugins/page.tsx web/src/pages/plugins/page.test.tsx web/src/i18n/messages/en.ts web/src/i18n/messages/zh-CN.ts
```

Expected: diff only includes the plugin page redesign, local filters, i18n strings, and tests.

- [ ] **Step 6: Commit implementation**

Run:

```bash
git add web/src/pages/plugins/page.tsx web/src/pages/plugins/page.test.tsx web/src/i18n/messages/en.ts web/src/i18n/messages/zh-CN.ts docs/superpowers/plans/2026-07-07-plugin-management-page-redesign.md
git commit -m "feat(web): redesign plugin management page"
```

Expected: one implementation commit on the current branch.

## Self-Review

- Spec coverage: The plan covers current API boundary preservation, local filters, compact inventory, detail-sheet low-frequency fields, binding query preservation, error/mutation behavior, performance notes, and focused verification.
- Placeholder scan: No `TBD`, `TODO`, `implement later`, or vague test requests remain.
- Type consistency: The helper type is `PluginInventoryFilters`; the all-value constant is `allFilterValue`; tests use existing accessible names and new i18n labels.
