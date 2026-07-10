# Web System Logs UX Optimization Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn the Cluster Ops system logs page into a node process log troubleshooting workbench with clearer scope, a denser first screen, compact log rows, and better live-follow feedback.

**Architecture:** Keep the existing manager application-log API unchanged and refactor only the `web/` surface. `AppLogsPage` remains the data owner; new page-local components render the summary toolbar, compact console, row details, and row actions from the existing `ManagerApplicationLogEntry` DTO.

**Tech Stack:** React, TypeScript, React Router, react-intl, lucide-react, shadcn-style local UI primitives, Vitest, Testing Library, Bun.

## Global Constraints

- Keep this change frontend-only; do not add backend routes or widen the application log reader.
- Preserve fixed log sources `app`, `warn`, `error`, and `debug`; do not expose arbitrary file paths.
- Keep `/cluster/system-logs` as the route for compatibility even if visible copy changes to node process logs.
- Application logs are ordinary `WK_LOG_DIR` process logs, not Controller, Slot, Channel, or Raft distributed logs.
- Keep deployment wording as “single-node cluster” when deployment shape is mentioned.
- Follow `web/DESIGN.md`: white editorial-console shell, restrained accents, thin separators, dense operational surfaces, no decorative cards inside cards.
- Preserve unrelated local edits; run `git status --short` before editing and do not revert files outside this task.
- Use targeted web tests before broad checks: `cd web && bun run test -- ...`, then `cd web && bunx tsc -b`, then `cd web && bun run build`, then `git diff --check`.

---

## File Structure

- Modify `web/src/pages/app-logs/page.tsx`: keep data loading, selected filters, stream polling, and page composition; remove duplicate section header.
- Create `web/src/pages/app-logs/components/app-logs-toolbar.tsx`: render node/source/severity/keyword controls, summary metadata, refresh/search/follow controls.
- Create `web/src/pages/app-logs/components/app-log-console.tsx`: render console header, scroll-bounded log body, live status, load-more action, and jump-to-latest action.
- Create `web/src/pages/app-logs/components/app-log-row.tsx`: render one compact log row with expandable details and copy/link actions.
- Create `web/src/pages/app-logs/log-format.ts`: own level normalization, severity filter mapping, important field extraction, file/path display helpers, and byte formatting.
- Modify `web/src/pages/app-logs/page.test.tsx`: cover page scope copy, toolbar behavior, compact rows, details expansion, copy actions, and live-follow feedback.
- Modify `web/src/i18n/messages/en.ts`: update app log and navigation copy.
- Modify `web/src/i18n/messages/zh-CN.ts`: update app log and navigation copy.
- Modify existing shell/navigation tests only if visible labels change: `web/src/app/layout/sidebar-nav.test.tsx`, `web/src/app/router.test.tsx`, `web/src/pages/page-shells.test.tsx`.

---

### Task 1: Scope Copy and First-Screen Layout

**Files:**
- Modify: `web/src/pages/app-logs/page.test.tsx`
- Modify: `web/src/pages/app-logs/page.tsx`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`
- Modify if label assertions fail: `web/src/app/layout/sidebar-nav.test.tsx`
- Modify if label assertions fail: `web/src/app/router.test.tsx`
- Modify if label assertions fail: `web/src/pages/page-shells.test.tsx`

**Interfaces:**
- Consumes: existing `AppLogsPage`, `AppLogsPanel`, `PageHeader`, `ResourceState`, and manager API mocks in `page.test.tsx`.
- Produces: visible page title `Node Process Logs` / `节点进程日志`, route still `/cluster/system-logs`, a single visible top-level title, and scope text that says the page reads `WK_LOG_DIR` process logs.

- [ ] **Step 1: Write the failing copy/layout test**

Add this test to `web/src/pages/app-logs/page.test.tsx`:

```tsx
test("presents node process log scope without duplicate visible headings", async () => {
  renderPanel()

  expect(await screen.findByRole("heading", { name: "Node Process Logs" })).toBeInTheDocument()
  expect(screen.getByText(/WK_LOG_DIR process logs/)).toBeInTheDocument()
  expect(screen.getAllByRole("heading", { name: "Node Process Logs" })).toHaveLength(1)
  expect(screen.queryByText("ordinary WuKongIM process logs")).not.toBeInTheDocument()
})
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
cd web && bun run test -- src/pages/app-logs/page.test.tsx
```

Expected: FAIL because the page still renders `System Logs` and repeats the heading in the page header plus the section card.

- [ ] **Step 3: Update i18n copy**

Change the app log messages in `web/src/i18n/messages/en.ts` to:

```ts
"nav.systemLogs.title": "Node Logs",
"nav.systemLogs.description": "Inspect selected-node WK_LOG_DIR process logs by source and severity.",
"appLogs.title": "Node Process Logs",
"appLogs.description": "Inspect selected-node WK_LOG_DIR process logs. Controller, Slot, and Raft logs remain in their own diagnostic views.",
"appLogs.scopeHint": "WK_LOG_DIR process logs only",
"appLogs.distributedHint": "Controller, Slot, and Raft logs are not shown here.",
```

Change the matching messages in `web/src/i18n/messages/zh-CN.ts` to:

```ts
"nav.systemLogs.title": "节点日志",
"nav.systemLogs.description": "按来源与级别查看选中节点的 WK_LOG_DIR 进程日志。",
"appLogs.title": "节点进程日志",
"appLogs.description": "查看选中节点的 WK_LOG_DIR 进程日志；Controller、Slot、Raft 日志仍在各自诊断视图中查看。",
"appLogs.scopeHint": "仅 WK_LOG_DIR 进程日志",
"appLogs.distributedHint": "这里不展示 Controller、Slot 或 Raft 分布式日志。",
```

If existing tests assert the old menu text, update expected text from `System Logs` to `Node Logs` and from `系统日志` to `节点日志`.

- [ ] **Step 4: Remove the duplicate visual section title**

In `web/src/pages/app-logs/page.tsx`, keep `PageHeader` as the only visible page title. Replace the `SectionCard` wrapper in `AppLogsPanel` with an unframed section that starts with a compact scope/status strip:

```tsx
<section className="border-b border-border pb-3" data-app-logs-surface="controls">
  <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
    <span className="font-medium text-foreground">{intl.formatMessage({ id: "appLogs.scopeHint" })}</span>
    <span>{intl.formatMessage({ id: "appLogs.distributedHint" })}</span>
  </div>
  {/* Existing controls move here for this task. Task 2 extracts them. */}
</section>
```

Remove the now-unused `SectionCard` import after moving the controls out of the card.

- [ ] **Step 5: Run focused tests**

Run:

```bash
cd web && bun run test -- src/pages/app-logs/page.test.tsx src/app/router.test.tsx src/app/layout/sidebar-nav.test.tsx src/pages/page-shells.test.tsx
```

Expected: PASS. If a page-shell expectation still says `System Logs`, update it to `Node Process Logs` or `节点进程日志` based on the test locale.

- [ ] **Step 6: Commit Task 1**

```bash
git add web/src/pages/app-logs/page.tsx web/src/pages/app-logs/page.test.tsx web/src/i18n/messages/en.ts web/src/i18n/messages/zh-CN.ts web/src/app/layout/sidebar-nav.test.tsx web/src/app/router.test.tsx web/src/pages/page-shells.test.tsx
git commit -m "refactor(web): clarify node process logs scope"
```

---

### Task 2: Toolbar, Severity Filters, and Node Context

**Files:**
- Create: `web/src/pages/app-logs/components/app-logs-toolbar.tsx`
- Create: `web/src/pages/app-logs/log-format.ts`
- Modify: `web/src/pages/app-logs/page.tsx`
- Modify: `web/src/pages/app-logs/page.test.tsx`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`

**Interfaces:**
- Consumes:
  - `ManagerNodesResponse`, `ManagerNode`, `ManagerApplicationLogSource` from `web/src/lib/manager-api.types.ts`
  - `NodeFilter` selection semantics from `web/src/components/manager/node-filter.tsx`
- Produces:
  - `type AppLogSeverityFilter = "" | "DEBUG" | "INFO" | "WARN" | "ERROR" | "WARN_ERROR"`
  - `function levelsForSeverityFilter(filter: AppLogSeverityFilter): string[]`
  - `function formatLogSourceLabel(source: ManagerApplicationLogSource): string`
  - `AppLogsToolbar` component with a compact grid and Enter-to-search behavior.

- [ ] **Step 1: Write failing toolbar tests**

Add these tests to `web/src/pages/app-logs/page.test.tsx`:

```tsx
test("shows selected node context and source file metadata in the toolbar", async () => {
  renderPanel()

  expect(await screen.findByText("node-1")).toBeInTheDocument()
  expect(screen.getByText("127.0.0.1:7000")).toBeInTheDocument()
  expect(screen.getByText("local")).toBeInTheDocument()
  expect(screen.getByText("app.log")).toBeInTheDocument()
  expect(screen.getByText("1.0 KiB")).toBeInTheDocument()
})

test("supports warn plus error severity shortcut and enter-to-search", async () => {
  const user = userEvent.setup()
  renderPanel()

  await screen.findByText("gateway ready")
  await user.selectOptions(screen.getByLabelText("Severity"), "WARN_ERROR")
  await user.clear(screen.getByLabelText("Keyword"))
  await user.type(screen.getByLabelText("Keyword"), "stale route{Enter}")

  await waitFor(() => {
    expect(getApplicationLogEntriesMock).toHaveBeenLastCalledWith(expect.objectContaining({
      keyword: "stale route",
      levels: ["WARN", "ERROR"],
    }))
  })
})
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
cd web && bun run test -- src/pages/app-logs/page.test.tsx
```

Expected: FAIL because there is no `Severity` label, no `WARN_ERROR` option, no node context summary, and Enter in the keyword field does not trigger search.

- [ ] **Step 3: Create `log-format.ts`**

Create `web/src/pages/app-logs/log-format.ts`:

```ts
import type { ManagerApplicationLogEntry, ManagerApplicationLogSource } from "@/lib/manager-api.types"

export type AppLogSeverityFilter = "" | "DEBUG" | "INFO" | "WARN" | "ERROR" | "WARN_ERROR"

export const appLogSeverityOptions: AppLogSeverityFilter[] = ["", "WARN_ERROR", "ERROR", "WARN", "INFO", "DEBUG"]

export function levelsForSeverityFilter(filter: AppLogSeverityFilter): string[] {
  if (filter === "WARN_ERROR") return ["WARN", "ERROR"]
  return filter ? [filter] : []
}

export function normalizedLogLevel(level: string) {
  const normalized = level.toUpperCase().trim()
  switch (normalized) {
    case "DEBUG":
    case "INFO":
    case "WARN":
    case "WARNING":
    case "ERROR":
    case "FATAL":
      return normalized
    default:
      return ""
  }
}

export function displayLogLevel(level: string) {
  const normalized = normalizedLogLevel(level)
  if (normalized === "WARNING") return "WARN"
  return normalized || "UNKNOWN"
}

export function formatBytes(value: number) {
  if (value >= 1024 * 1024) return `${(value / (1024 * 1024)).toFixed(1)} MiB`
  if (value >= 1024) return `${(value / 1024).toFixed(1)} KiB`
  return `${value} B`
}

export function formatLogSourceLabel(source: ManagerApplicationLogSource) {
  return source.available ? `${source.name} · ${source.file}` : `${source.name} · unavailable`
}

export function importantLogFields(entry: ManagerApplicationLogEntry) {
  const fields = entry.fields ?? {}
  return ["node_id", "slot_id", "request_id", "trace_id"]
    .map((key) => [key, fields[key]] as const)
    .filter(([, value]) => value !== undefined && value !== null && value !== "")
}
```

- [ ] **Step 4: Create `AppLogsToolbar`**

Create `web/src/pages/app-logs/components/app-logs-toolbar.tsx`:

```tsx
import { Pause, Play, RefreshCw, Search } from "lucide-react"
import { useIntl } from "react-intl"

import { NodeFilter } from "@/components/manager/node-filter"
import { Button } from "@/components/ui/button"
import type { ManagerApplicationLogSource, ManagerNodesResponse } from "@/lib/manager-api.types"
import { appLogSeverityOptions, formatBytes, type AppLogSeverityFilter } from "@/pages/app-logs/log-format"

type AppLogsToolbarProps = {
  nodes: ManagerNodesResponse | null
  selectedNodeId: number | null
  onNodeChange: (nodeId: number | null) => void
  sources: ManagerApplicationLogSource[]
  source: string
  onSourceChange: (source: string) => void
  severity: AppLogSeverityFilter
  onSeverityChange: (severity: AppLogSeverityFilter) => void
  keyword: string
  onKeywordChange: (keyword: string) => void
  followTail: boolean
  onFollowTailChange: (follow: boolean) => void
  lineCount: string
  activeSource: ManagerApplicationLogSource | null
  liveMessage: string
  canLoad: boolean
  loading: boolean
  refreshing: boolean
  onRefresh: () => void
  onSearch: () => void
}

export function AppLogsToolbar(props: AppLogsToolbarProps) {
  const intl = useIntl()
  const activeNode = props.nodes?.items.find((node) => node.node_id === props.selectedNodeId) ?? null

  return (
    <section className="space-y-3 border-b border-border pb-3" data-app-logs-surface="toolbar">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2 text-sm font-medium text-foreground">
            <span>{activeNode?.name || intl.formatMessage({ id: "common.nodeValue" }, { id: props.selectedNodeId ?? "-" })}</span>
            {activeNode?.is_local ? <span className="rounded-sm bg-accent px-1.5 py-0.5 text-xs">{intl.formatMessage({ id: "appLogs.nodeLocal" })}</span> : null}
            {activeNode?.status ? <span className="text-xs text-muted-foreground">{activeNode.status}</span> : null}
          </div>
          <div className="mt-1 flex flex-wrap gap-x-3 gap-y-1 text-xs text-muted-foreground">
            <span>{activeNode?.addr ?? "-"}</span>
            <span>{props.activeSource?.file ?? props.source}</span>
            <span>{props.activeSource ? formatBytes(props.activeSource.size_bytes) : "-"}</span>
            <span>{props.lineCount}</span>
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Button disabled={!props.canLoad || props.refreshing} onClick={props.onRefresh} size="sm" type="button" variant="outline">
            <RefreshCw />
            {props.refreshing ? intl.formatMessage({ id: "common.refreshing" }) : intl.formatMessage({ id: "common.refresh" })}
          </Button>
          <Button aria-label={intl.formatMessage({ id: "appLogs.followTail" })} aria-pressed={props.followTail} disabled={!props.canLoad} onClick={() => props.onFollowTailChange(!props.followTail)} size="sm" type="button" variant={props.followTail ? "default" : "outline"}>
            {props.followTail ? <Play /> : <Pause />}
            {props.followTail ? intl.formatMessage({ id: "appLogs.status.following" }) : intl.formatMessage({ id: "appLogs.status.paused" })}
          </Button>
        </div>
      </div>

      <div className="grid gap-2 md:grid-cols-[minmax(12rem,1fr)_minmax(9rem,0.8fr)_minmax(9rem,0.8fr)_minmax(12rem,1fr)_auto] md:items-end">
        <NodeFilter nodes={props.nodes} selectedNodeId={props.selectedNodeId} onNodeChange={props.onNodeChange} />
        <label className="text-xs font-medium text-muted-foreground">
          {intl.formatMessage({ id: "appLogs.source" })}
          <select aria-label={intl.formatMessage({ id: "appLogs.source" })} className="mt-1 h-8 w-full rounded-md border border-border bg-background px-2 text-sm text-foreground" disabled={props.sources.length === 0} onChange={(event) => props.onSourceChange(event.target.value)} value={props.source}>
            {props.sources.map((item) => <option disabled={!item.available} key={item.name} value={item.name}>{item.name}</option>)}
          </select>
        </label>
        <label className="text-xs font-medium text-muted-foreground">
          {intl.formatMessage({ id: "appLogs.severity" })}
          <select aria-label={intl.formatMessage({ id: "appLogs.severity" })} className="mt-1 h-8 w-full rounded-md border border-border bg-background px-2 text-sm text-foreground" onChange={(event) => props.onSeverityChange(event.target.value as AppLogSeverityFilter)} value={props.severity}>
            {appLogSeverityOptions.map((option) => <option key={option || "all"} value={option}>{intl.formatMessage({ id: `appLogs.severity.${option || "all"}` })}</option>)}
          </select>
        </label>
        <label className="text-xs font-medium text-muted-foreground">
          {intl.formatMessage({ id: "appLogs.keyword" })}
          <input aria-label={intl.formatMessage({ id: "appLogs.keyword" })} className="mt-1 h-8 w-full rounded-md border border-border bg-background px-2 text-sm text-foreground" onChange={(event) => props.onKeywordChange(event.target.value)} onKeyDown={(event) => { if (event.key === "Enter") props.onSearch() }} placeholder={intl.formatMessage({ id: "appLogs.keyword.placeholder" })} value={props.keyword} />
        </label>
        <Button disabled={!props.canLoad || props.loading} onClick={props.onSearch} size="sm" type="button">
          <Search />
          {intl.formatMessage({ id: "common.search" })}
        </Button>
      </div>

      <div className="text-xs text-muted-foreground" role="status">
        {props.liveMessage || (props.followTail ? intl.formatMessage({ id: "appLogs.status.following" }) : intl.formatMessage({ id: "appLogs.status.paused" }))}
      </div>
    </section>
  )
}
```

- [ ] **Step 5: Wire toolbar into `page.tsx`**

In `web/src/pages/app-logs/page.tsx`, replace `const [level, setLevel] = useState("")` with:

```ts
const [severity, setSeverity] = useState<AppLogSeverityFilter>("")
const levels = useMemo(() => levelsForSeverityFilter(severity), [severity])
```

Import:

```ts
import { AppLogsToolbar } from "@/pages/app-logs/components/app-logs-toolbar"
import { levelsForSeverityFilter, type AppLogSeverityFilter } from "@/pages/app-logs/log-format"
```

Replace the inline filter markup with:

```tsx
<AppLogsToolbar
  activeSource={activeSource}
  canLoad={canLoad}
  followTail={followTail}
  keyword={keyword}
  lineCount={lineCount}
  liveMessage={liveMessage}
  loading={state.loading}
  nodes={nodes}
  onFollowTailChange={setFollowTail}
  onKeywordChange={setKeyword}
  onNodeChange={setSelectedNodeId}
  onRefresh={() => selectedNodeId !== null && void loadEntries(selectedNodeId, source, { refreshing: true })}
  onSearch={() => selectedNodeId !== null && void loadEntries(selectedNodeId, source)}
  onSeverityChange={setSeverity}
  onSourceChange={setSource}
  refreshing={state.refreshing}
  selectedNodeId={selectedNodeId}
  severity={severity}
  source={source}
  sources={sources}
/>
```

Update `emptyDescription` to use `severity`:

```ts
const emptyDescription = keyword || severity
  ? intl.formatMessage({ id: "appLogs.empty.filtered" })
  : intl.formatMessage({ id: "appLogs.empty" })
```

- [ ] **Step 6: Add toolbar i18n messages**

Add to `web/src/i18n/messages/en.ts`:

```ts
"appLogs.severity": "Severity",
"appLogs.severity.all": "All severities",
"appLogs.severity.WARN_ERROR": "WARN + ERROR",
"appLogs.severity.ERROR": "ERROR",
"appLogs.severity.WARN": "WARN",
"appLogs.severity.INFO": "INFO",
"appLogs.severity.DEBUG": "DEBUG",
"appLogs.nodeLocal": "local",
```

Add to `web/src/i18n/messages/zh-CN.ts`:

```ts
"appLogs.severity": "严重级别",
"appLogs.severity.all": "全部级别",
"appLogs.severity.WARN_ERROR": "WARN + ERROR",
"appLogs.severity.ERROR": "ERROR",
"appLogs.severity.WARN": "WARN",
"appLogs.severity.INFO": "INFO",
"appLogs.severity.DEBUG": "DEBUG",
"appLogs.nodeLocal": "本地",
```

- [ ] **Step 7: Run focused tests**

Run:

```bash
cd web && bun run test -- src/pages/app-logs/page.test.tsx
```

Expected: PASS.

- [ ] **Step 8: Commit Task 2**

```bash
git add web/src/pages/app-logs/page.tsx web/src/pages/app-logs/page.test.tsx web/src/pages/app-logs/components/app-logs-toolbar.tsx web/src/pages/app-logs/log-format.ts web/src/i18n/messages/en.ts web/src/i18n/messages/zh-CN.ts
git commit -m "refactor(web): add compact app log toolbar"
```

---

### Task 3: Compact Log Rows, Expandable Details, and Row Actions

**Files:**
- Create: `web/src/pages/app-logs/components/app-log-row.tsx`
- Create: `web/src/pages/app-logs/components/app-log-console.tsx`
- Modify: `web/src/pages/app-logs/log-format.ts`
- Modify: `web/src/pages/app-logs/page.tsx`
- Modify: `web/src/pages/app-logs/page.test.tsx`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`

**Interfaces:**
- Consumes: `ManagerApplicationLogEntry`, `displayLogLevel`, `importantLogFields`, `formatBytes`.
- Produces:
  - `function logLevelClassName(level: string): string`
  - `function formatFields(fields: Record<string, unknown> | null): string`
  - `AppLogRow` with compact default display and details expansion.
  - `AppLogConsole` with `data-system-log-console="terminal"` and row rendering.

- [ ] **Step 1: Write failing compact-row tests**

Add these tests to `web/src/pages/app-logs/page.test.tsx`:

```tsx
test("renders compact log rows and hides raw details until expanded", async () => {
  const user = userEvent.setup()
  const entry = {
    ...logEntry("gateway ready"),
    fields: { listener: "tcp", request_id: "req-1", trace_id: "trace-1" },
  }
  getApplicationLogEntriesMock.mockResolvedValueOnce({
    node_id: 1,
    source: "app",
    cursor: "cursor-1",
    rotated: false,
    items: [entry],
  })

  renderPanel()

  expect(await screen.findByText("gateway ready")).toBeInTheDocument()
  expect(screen.getByText("gateway")).toBeInTheDocument()
  expect(screen.getByText("request_id=req-1")).toBeInTheDocument()
  expect(screen.queryByText("1 gateway ready")).not.toBeInTheDocument()

  await user.click(screen.getByRole("button", { name: "Show log details 1" }))
  expect(screen.getByText("1 gateway ready")).toBeInTheDocument()
  expect(screen.getByText(JSON.stringify(entry.fields))).toBeInTheDocument()
})

test("copies message and raw content from row actions", async () => {
  const user = userEvent.setup()
  const writeText = vi.fn().mockResolvedValue(undefined)
  Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true })

  renderPanel()

  await screen.findByText("gateway ready")
  await user.click(screen.getByRole("button", { name: "Copy log message 1" }))
  expect(writeText).toHaveBeenCalledWith("gateway ready")

  await user.click(screen.getByRole("button", { name: "Show log details 1" }))
  await user.click(screen.getByRole("button", { name: "Copy raw log 1" }))
  expect(writeText).toHaveBeenCalledWith("1 gateway ready")
})
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
cd web && bun run test -- src/pages/app-logs/page.test.tsx
```

Expected: FAIL because raw details render by default and row copy actions do not exist.

- [ ] **Step 3: Move formatting helpers into `log-format.ts`**

Add to `web/src/pages/app-logs/log-format.ts`:

```ts
export function formatFields(fields: Record<string, unknown> | null) {
  if (!fields || Object.keys(fields).length === 0) return ""
  return JSON.stringify(fields)
}

export function logLevelClassName(level: string) {
  switch (normalizedLogLevel(level)) {
    case "ERROR":
    case "FATAL":
      return "border-red-400/30 bg-red-500/10 text-red-300"
    case "WARN":
    case "WARNING":
      return "border-amber-400/30 bg-amber-500/10 text-amber-300"
    case "INFO":
      return "border-emerald-400/30 bg-emerald-500/10 text-emerald-300"
    case "DEBUG":
      return "border-sky-400/30 bg-sky-500/10 text-sky-300"
    default:
      return "border-slate-500/30 bg-slate-500/10 text-slate-300"
  }
}

export function compactFieldLabel(key: string, value: unknown) {
  return `${key}=${String(value)}`
}
```

- [ ] **Step 4: Create `AppLogRow`**

Create `web/src/pages/app-logs/components/app-log-row.tsx`:

```tsx
import { ChevronDown, ChevronRight, Copy } from "lucide-react"
import { useState } from "react"
import { useIntl } from "react-intl"
import { Link } from "react-router-dom"

import { Button } from "@/components/ui/button"
import type { ManagerApplicationLogEntry } from "@/lib/manager-api.types"
import { compactFieldLabel, displayLogLevel, formatFields, importantLogFields, logLevelClassName } from "@/pages/app-logs/log-format"

type AppLogRowProps = {
  entry: ManagerApplicationLogEntry
}

function copyText(value: string) {
  if (!navigator.clipboard) return
  void navigator.clipboard.writeText(value)
}

export function AppLogRow({ entry }: AppLogRowProps) {
  const intl = useIntl()
  const [expanded, setExpanded] = useState(false)
  const fields = importantLogFields(entry)
  const details = formatFields(entry.fields)
  const slot = entry.fields?.slot_id
  const seq = entry.seq

  return (
    <article className="grid gap-2 border-b border-white/5 px-3 py-2 last:border-b-0 md:grid-cols-[9.5rem_4.5rem_minmax(0,1fr)_auto]" data-app-log-row="compact">
      <div className="whitespace-nowrap text-slate-500">{entry.time || "-"}</div>
      <div className={`h-fit max-w-[4.5rem] overflow-hidden truncate rounded border px-1.5 py-0.5 text-[11px] font-semibold leading-none ${logLevelClassName(entry.level)}`} title={entry.level || displayLogLevel(entry.level)}>
        {displayLogLevel(entry.level)}
      </div>
      <div className="min-w-0">
        <div className="flex flex-wrap items-center gap-2">
          {entry.module ? <span className="text-slate-400">{entry.module}</span> : null}
          <span className="break-words text-slate-100">{entry.message || entry.raw}</span>
        </div>
        <div className="mt-1 flex flex-wrap gap-1.5 text-[11px] text-slate-400">
          {fields.map(([key, value]) => <span className="rounded-sm border border-white/10 px-1.5 py-0.5" key={key}>{compactFieldLabel(key, value)}</span>)}
          {slot !== undefined && slot !== null ? <Link className="rounded-sm border border-white/10 px-1.5 py-0.5 text-sky-300 hover:text-sky-200" to={`/cluster/slots?tab=logs&slot_id=${encodeURIComponent(String(slot))}`}>{intl.formatMessage({ id: "appLogs.openSlot" }, { slot })}</Link> : null}
        </div>
        {expanded ? (
          <div className="mt-2 space-y-1 text-slate-500" data-app-log-row="details">
            {entry.caller ? <div className="break-all">{entry.caller}</div> : null}
            <pre className="whitespace-pre-wrap break-all">{entry.raw}</pre>
            {details ? <pre className="whitespace-pre-wrap break-words">{details}</pre> : null}
            <Button aria-label={intl.formatMessage({ id: "appLogs.copyRawAria" }, { seq })} onClick={() => copyText(entry.raw)} size="sm" type="button" variant="outline">
              <Copy />
              {intl.formatMessage({ id: "appLogs.copyRaw" })}
            </Button>
          </div>
        ) : null}
      </div>
      <div className="flex items-start gap-1">
        <Button aria-label={intl.formatMessage({ id: "appLogs.copyMessageAria" }, { seq })} onClick={() => copyText(entry.message || entry.raw)} size="icon" type="button" variant="ghost">
          <Copy />
        </Button>
        <Button aria-label={intl.formatMessage({ id: expanded ? "appLogs.hideDetailsAria" : "appLogs.showDetailsAria" }, { seq })} onClick={() => setExpanded((value) => !value)} size="icon" type="button" variant="ghost">
          {expanded ? <ChevronDown /> : <ChevronRight />}
        </Button>
      </div>
    </article>
  )
}
```

- [ ] **Step 5: Create `AppLogConsole`**

Create `web/src/pages/app-logs/components/app-log-console.tsx`:

```tsx
import { Terminal } from "lucide-react"
import { useIntl } from "react-intl"

import { Button } from "@/components/ui/button"
import type { ManagerApplicationLogEntry, ManagerApplicationLogSource } from "@/lib/manager-api.types"
import { AppLogRow } from "@/pages/app-logs/components/app-log-row"

type AppLogConsoleProps = {
  title: string
  entries: ManagerApplicationLogEntry[]
  lineCount: string
  activeSource: ManagerApplicationLogSource | null
  source: string
  followTail: boolean
  refreshing: boolean
  selectedNodeId: number | null
  atMaxTail: boolean
  onLoadMore: () => void
}

export function AppLogConsole(props: AppLogConsoleProps) {
  const intl = useIntl()

  return (
    <section aria-label={props.title} className="overflow-hidden rounded-lg border border-[#242833] bg-[#0f1115] font-mono text-slate-100 shadow-[0_18px_40px_rgba(15,17,21,0.18)]" data-system-log-console="terminal">
      <div className="flex flex-wrap items-center justify-between gap-2 border-b border-white/10 bg-[#151923] px-3 py-2 text-xs">
        <div className="flex min-w-0 items-center gap-2">
          <Terminal aria-hidden className="size-3.5 shrink-0 text-emerald-300" />
          <span className="font-semibold text-slate-100">{intl.formatMessage({ id: "appLogs.console.title" })}</span>
          <span className="truncate text-slate-400">{props.activeSource?.file ?? props.source}</span>
        </div>
        <div className="flex items-center gap-3 text-[11px] text-slate-400">
          <span>{props.lineCount}</span>
          <span>{props.followTail ? intl.formatMessage({ id: "appLogs.status.following" }) : intl.formatMessage({ id: "appLogs.status.paused" })}</span>
        </div>
      </div>
      <div className="max-h-[min(64vh,720px)] overflow-auto text-xs" role="log">
        {props.entries.map((entry) => <AppLogRow entry={entry} key={`${entry.seq}-${entry.offset}-${entry.raw}`} />)}
      </div>
      <div className="border-t border-white/10 bg-[#151923] p-3">
        <Button disabled={props.refreshing || props.selectedNodeId === null || props.atMaxTail} onClick={props.onLoadMore} size="sm" type="button" variant="outline">
          {props.refreshing ? intl.formatMessage({ id: "common.refreshing" }) : intl.formatMessage({ id: "common.loadMore" })}
        </Button>
      </div>
    </section>
  )
}
```

- [ ] **Step 6: Replace inline console in `page.tsx`**

Import:

```ts
import { AppLogConsole } from "@/pages/app-logs/components/app-log-console"
```

Replace the non-empty branch with:

```tsx
<AppLogConsole
  activeSource={activeSource}
  atMaxTail={state.entries.length >= appLogMaxTailLimit}
  entries={state.entries}
  followTail={followTail}
  lineCount={lineCount}
  onLoadMore={() => selectedNodeId !== null && void loadEntries(selectedNodeId, source, { limit: nextTailLimit, refreshing: true })}
  refreshing={state.refreshing}
  selectedNodeId={selectedNodeId}
  source={source}
  title={title}
/>
```

Remove `Terminal`, `logLevelClassName`, `displayLogLevel`, `formatFields`, and related inline helpers from `page.tsx` once imports are replaced.

- [ ] **Step 7: Add row action i18n**

Add to `web/src/i18n/messages/en.ts`:

```ts
"appLogs.showDetailsAria": "Show log details {seq}",
"appLogs.hideDetailsAria": "Hide log details {seq}",
"appLogs.copyMessageAria": "Copy log message {seq}",
"appLogs.copyRawAria": "Copy raw log {seq}",
"appLogs.copyRaw": "Copy raw",
"appLogs.openSlot": "Slot {slot}",
```

Add to `web/src/i18n/messages/zh-CN.ts`:

```ts
"appLogs.showDetailsAria": "展开日志详情 {seq}",
"appLogs.hideDetailsAria": "收起日志详情 {seq}",
"appLogs.copyMessageAria": "复制日志消息 {seq}",
"appLogs.copyRawAria": "复制原始日志 {seq}",
"appLogs.copyRaw": "复制原始日志",
"appLogs.openSlot": "槽位 {slot}",
```

- [ ] **Step 8: Run focused tests**

Run:

```bash
cd web && bun run test -- src/pages/app-logs/page.test.tsx
```

Expected: PASS.

- [ ] **Step 9: Commit Task 3**

```bash
git add web/src/pages/app-logs/page.tsx web/src/pages/app-logs/page.test.tsx web/src/pages/app-logs/components/app-log-console.tsx web/src/pages/app-logs/components/app-log-row.tsx web/src/pages/app-logs/log-format.ts web/src/i18n/messages/en.ts web/src/i18n/messages/zh-CN.ts
git commit -m "refactor(web): compact system log rows"
```

---

### Task 4: Live Follow Feedback and Rotation State

**Files:**
- Modify: `web/src/pages/app-logs/page.tsx`
- Modify: `web/src/pages/app-logs/components/app-log-console.tsx`
- Modify: `web/src/pages/app-logs/page.test.tsx`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`

**Interfaces:**
- Consumes: `streamApplicationLogEntries`, parsed NDJSON events, `state.rotated`, `liveMessage`, and `state.entries`.
- Produces:
  - `newLiveLineCount: number` state in `AppLogsPanel`
  - `onAcknowledgeLiveLines: () => void`
  - Console banner for appended live lines and log rotation.

- [ ] **Step 1: Write failing live feedback tests**

Add this test to `web/src/pages/app-logs/page.test.tsx`:

```tsx
test("shows live appended count and clears it from the console banner", async () => {
  const user = userEvent.setup()
  getApplicationLogEntriesMock.mockResolvedValueOnce({
    node_id: 1,
    source: "app",
    cursor: "cursor-1",
    rotated: false,
    items: [],
  })
  streamApplicationLogEntriesMock.mockResolvedValueOnce(new Response([
    JSON.stringify({ type: "line", cursor: "cursor-2", item: logEntry("live warning", 2) }),
  ].join("\n"), { status: 200 }))

  renderPanel()

  await user.click(await screen.findByLabelText("Follow tail"))
  expect(await screen.findByText("1 new live line")).toBeInTheDocument()

  await user.click(screen.getByRole("button", { name: "Jump to latest logs" }))
  expect(screen.queryByText("1 new live line")).not.toBeInTheDocument()
})
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
cd web && bun run test -- src/pages/app-logs/page.test.tsx
```

Expected: FAIL because the console does not expose live appended counts or a jump action.

- [ ] **Step 3: Add live-line count state in `page.tsx`**

Add state:

```ts
const [newLiveLineCount, setNewLiveLineCount] = useState(0)
```

Reset the counter when a manual load/search/refresh replaces the page:

```ts
setNewLiveLineCount(0)
```

Inside the stream polling `setState` path, after collecting `nextEntries`, increment:

```ts
if (nextEntries.length > 0) {
  setNewLiveLineCount((count) => count + nextEntries.length)
}
```

Pass to `AppLogConsole`:

```tsx
newLiveLineCount={newLiveLineCount}
rotated={state.rotated}
onAcknowledgeLiveLines={() => setNewLiveLineCount(0)}
```

- [ ] **Step 4: Render console live banner**

Update `AppLogConsoleProps` in `app-log-console.tsx`:

```ts
newLiveLineCount: number
rotated: boolean
onAcknowledgeLiveLines: () => void
```

Add this banner between the console header and log body:

```tsx
{props.rotated || props.newLiveLineCount > 0 ? (
  <div className="flex flex-wrap items-center justify-between gap-2 border-b border-white/10 bg-[#111827] px-3 py-2 text-xs text-slate-300">
    <span>
      {props.rotated
        ? intl.formatMessage({ id: "appLogs.status.rotated" })
        : intl.formatMessage({ id: "appLogs.liveLines" }, { count: props.newLiveLineCount })}
    </span>
    {props.newLiveLineCount > 0 ? (
      <Button aria-label={intl.formatMessage({ id: "appLogs.jumpLatestAria" })} onClick={props.onAcknowledgeLiveLines} size="sm" type="button" variant="outline">
        {intl.formatMessage({ id: "appLogs.jumpLatest" })}
      </Button>
    ) : null}
  </div>
) : null}
```

- [ ] **Step 5: Add live feedback i18n**

Add to `web/src/i18n/messages/en.ts`:

```ts
"appLogs.liveLines": "{count, plural, one {# new live line} other {# new live lines}}",
"appLogs.jumpLatest": "Jump to latest",
"appLogs.jumpLatestAria": "Jump to latest logs",
```

Add to `web/src/i18n/messages/zh-CN.ts`:

```ts
"appLogs.liveLines": "新增 {count} 行实时日志",
"appLogs.jumpLatest": "跳到最新",
"appLogs.jumpLatestAria": "跳到最新日志",
```

- [ ] **Step 6: Run focused tests**

Run:

```bash
cd web && bun run test -- src/pages/app-logs/page.test.tsx
```

Expected: PASS.

- [ ] **Step 7: Commit Task 4**

```bash
git add web/src/pages/app-logs/page.tsx web/src/pages/app-logs/page.test.tsx web/src/pages/app-logs/components/app-log-console.tsx web/src/i18n/messages/en.ts web/src/i18n/messages/zh-CN.ts
git commit -m "feat(web): improve live system log feedback"
```

---

### Task 5: Final Responsive QA and Verification

**Files:**
- Modify if needed: `web/src/pages/app-logs/page.tsx`
- Modify if needed: `web/src/pages/app-logs/components/app-logs-toolbar.tsx`
- Modify if needed: `web/src/pages/app-logs/components/app-log-console.tsx`
- Modify if needed: `web/src/pages/app-logs/components/app-log-row.tsx`
- Modify if needed: `web/src/pages/app-logs/page.test.tsx`

**Interfaces:**
- Consumes: all components and i18n from Tasks 1-4.
- Produces: verified source-only frontend change with no generated `web/dist/index.html` churn left in the working tree.

- [ ] **Step 1: Run full focused test set**

Run:

```bash
cd web && bun run test -- src/pages/app-logs/page.test.tsx src/app/router.test.tsx src/app/layout/sidebar-nav.test.tsx src/pages/page-shells.test.tsx src/pages/cluster/diagnostics/page.test.tsx
```

Expected: PASS.

- [ ] **Step 2: Run typecheck**

Run:

```bash
cd web && bunx tsc -b
```

Expected: exit 0 with no TypeScript errors.

- [ ] **Step 3: Run production build**

Run:

```bash
cd web && bun run build
```

Expected: exit 0.

- [ ] **Step 4: Remove generated build hash churn**

Run:

```bash
git status --short web/dist/index.html
```

If `web/dist/index.html` is modified and this plan did not intentionally change built assets, restore only that generated file:

```bash
git restore web/dist/index.html
```

- [ ] **Step 5: Run diff checks**

Run:

```bash
git diff --check
git diff --cached --check
```

Expected: both commands exit 0. `git diff --cached --check` is allowed to report no staged changes when no files are staged.

- [ ] **Step 6: Browser smoke check**

Start the dev server:

```bash
cd web && bun run dev -- --host 127.0.0.1
```

Open `/cluster/system-logs` with a manager API target or existing local manager. Verify visually:

- Desktop first screen shows the page title, compact toolbar, and log rows without duplicate headings.
- Mobile width around 390px shows controls without horizontal overlap.
- A long `caller` path appears only in expanded details.
- WARN + ERROR filter sends both levels.
- Live follow shows the new-line banner after an NDJSON line event.

- [ ] **Step 7: Commit Task 5**

```bash
git add web/src/pages/app-logs web/src/i18n/messages/en.ts web/src/i18n/messages/zh-CN.ts web/src/app/router.test.tsx web/src/app/layout/sidebar-nav.test.tsx web/src/pages/page-shells.test.tsx web/src/pages/cluster/diagnostics/page.test.tsx
git commit -m "test(web): verify system log workbench"
```
