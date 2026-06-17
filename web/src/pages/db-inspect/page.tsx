import { type FormEvent, useCallback, useEffect, useMemo, useState } from "react"
import { Database, Play, RefreshCw } from "lucide-react"
import { useIntl } from "react-intl"

import { ResourceState } from "@/components/manager/resource-state"
import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
import { SectionCard } from "@/components/shell/section-card"
import { Button } from "@/components/ui/button"
import {
  getDBInspectTable,
  getDBInspectTables,
  getNodes,
  ManagerApiError,
  queryDBInspect,
} from "@/lib/manager-api"
import type {
  ManagerDBInspectQueryResponse,
  ManagerDBInspectRow,
  ManagerNode,
} from "@/lib/manager-api.types"

type TableRow = {
  domain: string
  name: string
  table: string
}

type PageState = {
  nodes: ManagerNode[]
  tables: TableRow[]
  selectedNodeId: number
  query: string
  result: ManagerDBInspectQueryResponse | null
  describe: ManagerDBInspectQueryResponse | null
  selectedTable: string
  loading: boolean
  running: boolean
  describing: boolean
  error: Error | null
}

const defaultQuery = "show tables"

const templates = [
  "show tables",
  "describe meta.user",
  "select * from meta.user where uid='u1' limit 20",
  "select * from meta.channel where channel_id='g1' limit 20",
  "select * from message.channels limit 50",
  "select * from message.message where channel_key='g1:2' limit 20",
]

function errorKind(error: Error | null) {
  if (!(error instanceof ManagerApiError)) {
    return "error" as const
  }
  if (error.status === 403) {
    return "forbidden" as const
  }
  if (error.status === 503) {
    return "unavailable" as const
  }
  return "error" as const
}

function rowColumns(rows: ManagerDBInspectRow[]) {
  const seen = new Set<string>()
  const columns: string[] = []
  for (const row of rows) {
    for (const key of Object.keys(row)) {
      if (!seen.has(key)) {
        seen.add(key)
        columns.push(key)
      }
    }
  }
  return columns
}

function renderCell(value: unknown) {
  if (value === null || value === undefined || value === "") {
    return "-"
  }
  if (Array.isArray(value)) {
    return value.join(", ")
  }
  if (typeof value === "object") {
    return JSON.stringify(value)
  }
  return String(value)
}

function appendCursor(query: string, cursor: string) {
  const trimmed = stripCursorClauses(query)
  return `${trimmed} cursor '${cursor}'`
}

function stripCursorClauses(query: string) {
  let next = query.trim()
  for (;;) {
    const tokens = queryTokens(next)
    const index = tokens.findIndex((token) => token.text.toLowerCase() === "cursor")
    if (index < 0) {
      break
    }
    const token = tokens[index]
    const value = tokens[index + 1]
    let start = token.start
    let end = value?.end ?? token.end
    while (start > 0 && /\s/.test(next[start - 1])) {
      start -= 1
    }
    while (end < next.length && /\s/.test(next[end])) {
      end += 1
    }
    next = `${next.slice(0, start)}${start > 0 && end < next.length ? " " : ""}${next.slice(end)}`
  }
  return next.trim().replace(/\s+/g, " ")
}

function queryTokens(query: string) {
  const tokens: Array<{ end: number; start: number; text: string }> = []
  let start = -1
  let quoted = false
  for (let index = 0; index < query.length; index += 1) {
    const char = query[index]
    if (char === "'") {
      if (start < 0) {
        start = index
      }
      quoted = !quoted
      continue
    }
    if (/\s/.test(char) && !quoted) {
      if (start >= 0) {
        tokens.push({ start, end: index, text: query.slice(start, index) })
        start = -1
      }
      continue
    }
    if (start < 0) {
      start = index
    }
  }
  if (start >= 0) {
    tokens.push({ start, end: query.length, text: query.slice(start) })
  }
  return tokens
}

export function DBInspectPage() {
  const intl = useIntl()
  const [state, setState] = useState<PageState>({
    nodes: [],
    tables: [],
    selectedNodeId: 0,
    query: defaultQuery,
    result: null,
    describe: null,
    selectedTable: "",
    loading: true,
    running: false,
    describing: false,
    error: null,
  })

  const selectedNodeId = state.selectedNodeId || state.nodes[0]?.node_id || 0

  const loadInitial = useCallback(async () => {
    setState((current) => ({ ...current, loading: true, error: null }))
    try {
      const nodes = await getNodes()
      const selected = nodes.items.find((node) => node.is_local)?.node_id ?? nodes.items[0]?.node_id ?? 0
      const tables = await getDBInspectTables(selected ? { nodeId: selected } : undefined)
      setState((current) => ({
        ...current,
        nodes: nodes.items,
        selectedNodeId: selected,
        tables: tableRows(tables.rows),
        loading: false,
        error: null,
      }))
    } catch (error) {
      setState((current) => ({
        ...current,
        loading: false,
        error: error instanceof Error ? error : new Error("db inspect request failed"),
      }))
    }
  }, [])

  useEffect(() => {
    void loadInitial()
  }, [loadInitial])

  const runQuery = async (nextQuery?: string) => {
    const query = nextQuery ?? state.query
    if (!query.trim()) {
      setState((current) => ({
        ...current,
        error: new Error(intl.formatMessage({ id: "dbInspect.error.emptyQuery" })),
      }))
      return
    }

    setState((current) => ({ ...current, query, running: true, error: null }))
    try {
      const result = await queryDBInspect({ node_id: selectedNodeId || undefined, query: query.trim() })
      setState((current) => ({ ...current, result, running: false, error: null }))
    } catch (error) {
      setState((current) => ({
        ...current,
        running: false,
        error: error instanceof Error ? error : new Error("db inspect query failed"),
      }))
    }
  }

  const describeTable = async (tableName: string) => {
    const [domain, table] = tableName.split(".")
    setState((current) => ({ ...current, describing: true, selectedTable: tableName, error: null }))
    try {
      const describe = await getDBInspectTable(domain, table, selectedNodeId ? { nodeId: selectedNodeId } : undefined)
      setState((current) => ({ ...current, describe, describing: false }))
    } catch (error) {
      setState((current) => ({
        ...current,
        describing: false,
        error: error instanceof Error ? error : new Error("describe table failed"),
      }))
    }
  }

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    void runQuery()
  }

  const columns = useMemo(() => rowColumns(state.result?.rows ?? []), [state.result])
  const describeColumns = useMemo(() => rowColumns(state.describe?.rows ?? []), [state.describe])
  const tablesByDomain = useMemo(() => groupTables(state.tables), [state.tables])

  return (
    <PageContainer>
      <PageHeader
        actions={(
          <Button onClick={() => void loadInitial()} size="sm" variant="outline">
            <RefreshCw className="mr-2 size-4" />
            {intl.formatMessage({ id: "common.refresh" })}
          </Button>
        )}
        description={intl.formatMessage({ id: "dbInspect.description" })}
        title={intl.formatMessage({ id: "dbInspect.title" })}
      />

      <div className="grid gap-4 xl:grid-cols-[280px_minmax(0,1fr)]">
        <SectionCard title={intl.formatMessage({ id: "dbInspect.tables.title" })}>
          {state.loading ? <ResourceState kind="loading" title={intl.formatMessage({ id: "dbInspect.tables.title" })} /> : null}
          {!state.loading && state.tables.length > 0 ? (
            <div className="space-y-4">
              {Object.entries(tablesByDomain).map(([domain, rows]) => (
                <div key={domain}>
                  <div className="mb-2 text-xs font-semibold uppercase text-muted-foreground">
                    {domain}
                  </div>
                  <div className="space-y-1">
                    {rows.map((row) => (
                      <button
                        aria-label={intl.formatMessage({ id: "dbInspect.table.inspect" }, { table: row.table })}
                        className="flex w-full items-center justify-between rounded-md border border-border/70 px-3 py-2 text-left text-sm hover:bg-muted/50"
                        key={row.table}
                        onClick={() => void describeTable(row.table)}
                        type="button"
                      >
                        <span className="font-mono">{row.table}</span>
                        <Database className="size-4 text-muted-foreground" />
                      </button>
                    ))}
                  </div>
                </div>
              ))}
            </div>
          ) : null}
        </SectionCard>

        <div className="space-y-4">
          <SectionCard title={intl.formatMessage({ id: "dbInspect.query.title" })}>
            <form className="space-y-3" onSubmit={submit}>
              <div className="flex flex-wrap items-center gap-3">
                <label className="text-sm font-medium text-foreground" htmlFor="db-inspect-node">
                  {intl.formatMessage({ id: "dbInspect.node" })}
                </label>
                <select
                  className="rounded-md border border-input bg-background px-3 py-2 text-sm"
                  id="db-inspect-node"
                  onChange={(event) => {
                    setState((current) => ({ ...current, selectedNodeId: Number(event.target.value) }))
                  }}
                  value={selectedNodeId}
                >
                  {state.nodes.map((node) => (
                    <option key={node.node_id} value={node.node_id}>
                      {node.is_local
                        ? intl.formatMessage({ id: "common.localNodeValue" }, { label: node.name || `Node ${node.node_id}` })
                        : intl.formatMessage({ id: "common.nodeValue" }, { id: node.node_id })}
                    </option>
                  ))}
                </select>
              </div>
              <label className="block text-sm font-medium text-foreground" htmlFor="db-inspect-query">
                {intl.formatMessage({ id: "dbInspect.query.label" })}
              </label>
              <textarea
                className="min-h-28 w-full rounded-md border border-input bg-background px-3 py-2 font-mono text-sm"
                id="db-inspect-query"
                onChange={(event) => setState((current) => ({ ...current, query: event.target.value }))}
                value={state.query}
              />
              <div className="flex flex-wrap gap-2">
                {templates.map((template) => (
                  <Button
                    key={template}
                    onClick={() => setState((current) => ({ ...current, query: template }))}
                    size="sm"
                    type="button"
                    variant="outline"
                  >
                    {template}
                  </Button>
                ))}
              </div>
              <Button disabled={state.running} type="submit">
                <Play className="mr-2 size-4" />
                {state.running
                  ? intl.formatMessage({ id: "dbInspect.query.running" })
                  : intl.formatMessage({ id: "dbInspect.query.run" })}
              </Button>
            </form>
          </SectionCard>

          {state.error ? (
            <ResourceState
              description={state.error.message}
              kind={errorKind(state.error)}
              title={intl.formatMessage({ id: "dbInspect.title" })}
            />
          ) : null}

          {state.describe ? (
            <ResultSection
              columns={describeColumns}
              rows={state.describe.rows}
              title={intl.formatMessage({ id: "dbInspect.describe.title" }, { table: state.selectedTable })}
            />
          ) : null}

          {state.result ? (
            <SectionCard title={intl.formatMessage({ id: "dbInspect.results.title" })}>
              <StatsStrip result={state.result} />
              <ResultTable columns={columns} rows={state.result.rows} />
              {state.result.stats.has_more && state.result.stats.next_cursor ? (
                <Button
                  className="mt-4"
                  onClick={() => void runQuery(appendCursor(state.query, state.result?.stats.next_cursor ?? ""))}
                  size="sm"
                  variant="outline"
                >
                  {intl.formatMessage({ id: "dbInspect.nextPage" })}
                </Button>
              ) : null}
            </SectionCard>
          ) : null}
        </div>
      </div>
    </PageContainer>
  )
}

function tableRows(rows: ManagerDBInspectRow[]): TableRow[] {
  return rows
    .map((row) => ({
      domain: String(row.domain ?? ""),
      name: String(row.name ?? ""),
      table: String(row.table ?? ""),
    }))
    .filter((row) => row.domain && row.name && row.table)
}

function groupTables(rows: TableRow[]) {
  return rows.reduce<Record<string, TableRow[]>>((acc, row) => {
    acc[row.domain] = acc[row.domain] ?? []
    acc[row.domain].push(row)
    return acc
  }, {})
}

function StatsStrip({ result }: { result: ManagerDBInspectQueryResponse }) {
  return (
    <div className="mb-4 flex flex-wrap gap-2 text-xs text-muted-foreground">
      <span className="rounded-full border border-border px-2 py-1">{result.stats.scan_mode || "-"}</span>
      <span className="rounded-full border border-border px-2 py-1">scanned {result.stats.scanned_rows}</span>
      <span className="rounded-full border border-border px-2 py-1">returned {result.stats.returned_rows}</span>
      <span className="rounded-full border border-border px-2 py-1">
        slots {result.stats.scanned_hash_slots.length ? result.stats.scanned_hash_slots.join(",") : "-"}
      </span>
    </div>
  )
}

function ResultSection({ title, rows, columns }: { title: string; rows: ManagerDBInspectRow[]; columns: string[] }) {
  return (
    <SectionCard title={title}>
      <ResultTable columns={columns} rows={rows} />
    </SectionCard>
  )
}

function ResultTable({ columns, rows }: { columns: string[]; rows: ManagerDBInspectRow[] }) {
  if (rows.length === 0) {
    return <ResourceState kind="empty" title="DB Inspect" />
  }
  return (
    <div className="overflow-x-auto rounded-lg border border-border">
      <table className="w-full border-collapse">
        <thead className="bg-muted/40 text-left text-xs uppercase text-muted-foreground">
          <tr>
            {columns.map((column) => (
              <th className="px-3 py-3" key={column}>{column}</th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((row, index) => (
            <tr className="border-t border-border" key={`${index}-${columns.join(":")}`}>
              {columns.map((column) => (
                <td className="max-w-[320px] truncate px-3 py-3 font-mono text-xs text-foreground" key={column}>
                  {renderCell(row[column])}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
