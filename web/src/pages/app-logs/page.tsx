import { Pause, Play, RefreshCw, Search } from "lucide-react"
import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { useIntl } from "react-intl"

import { NodeFilter, defaultNodeId, hasNode } from "@/components/manager/node-filter"
import { ResourceState } from "@/components/manager/resource-state"
import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
import { SectionCard } from "@/components/shell/section-card"
import { Button } from "@/components/ui/button"
import {
  ManagerApiError,
  getApplicationLogEntries,
  getApplicationLogSources,
  getNodes,
  streamApplicationLogEntries,
} from "@/lib/manager-api"
import type {
  ManagerApplicationLogEntry,
  ManagerApplicationLogSource,
  ManagerApplicationLogStreamEvent,
  ManagerNodesResponse,
} from "@/lib/manager-api.types"

const appLogPageLimit = 100
const maxBufferedLines = 500
const livePollIntervalMs = 2000
const levelOptions = ["", "DEBUG", "INFO", "WARN", "ERROR"]

type AppLogState = {
  entries: ManagerApplicationLogEntry[]
  cursor: string
  rotated: boolean
  loading: boolean
  refreshing: boolean
  error: Error | null
}

function emptyLogState(): AppLogState {
  return {
    entries: [],
    cursor: "",
    rotated: false,
    loading: true,
    refreshing: false,
    error: null,
  }
}

function mapErrorKind(error: Error | null) {
  if (!(error instanceof ManagerApiError)) {
    return "error" as const
  }
  if (error.status === 403) {
    return "forbidden" as const
  }
  if (error.status === 404) {
    return "empty" as const
  }
  if (error.status === 503) {
    return "unavailable" as const
  }
  return "error" as const
}

function formatBytes(value: number) {
  if (value >= 1024 * 1024) {
    return `${(value / (1024 * 1024)).toFixed(1)} MiB`
  }
  if (value >= 1024) {
    return `${(value / 1024).toFixed(1)} KiB`
  }
  return `${value} B`
}

function formatFields(fields: Record<string, unknown> | null) {
  if (!fields || Object.keys(fields).length === 0) {
    return ""
  }
  return JSON.stringify(fields)
}

function parseStreamEvents(text: string): ManagerApplicationLogStreamEvent[] {
  return text
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean)
    .map((line) => JSON.parse(line) as ManagerApplicationLogStreamEvent)
}

function nextSource(sources: ManagerApplicationLogSource[], current: string) {
  if (sources.some((source) => source.name === current)) {
    return current
  }
  return sources.find((source) => source.name === "app")?.name ?? sources[0]?.name ?? "app"
}

function appendEntries(current: ManagerApplicationLogEntry[], next: ManagerApplicationLogEntry[]) {
  return [...current, ...next].slice(-maxBufferedLines)
}

export function AppLogsPanel() {
  const intl = useIntl()
  const [nodes, setNodes] = useState<ManagerNodesResponse | null>(null)
  const [selectedNodeId, setSelectedNodeId] = useState<number | null>(null)
  const [sources, setSources] = useState<ManagerApplicationLogSource[]>([])
  const [source, setSource] = useState("app")
  const [keyword, setKeyword] = useState("")
  const [level, setLevel] = useState("")
  const [followTail, setFollowTail] = useState(false)
  const [liveMessage, setLiveMessage] = useState("")
  const [state, setState] = useState<AppLogState>(emptyLogState)
  const cursorRef = useRef("")

  useEffect(() => {
    cursorRef.current = state.cursor
  }, [state.cursor])

  const activeSource = useMemo(
    () => sources.find((item) => item.name === source) ?? null,
    [source, sources],
  )

  const levels = useMemo(() => (level ? [level] : []), [level])

  const loadEntries = useCallback(async (
    nodeId: number,
    nextSourceName: string,
    options: { cursor?: string; refreshing?: boolean } = {},
  ) => {
    setState((current) => ({
      ...current,
      loading: !options.cursor && !options.refreshing,
      refreshing: Boolean(options.refreshing),
      error: null,
      entries: options.cursor ? current.entries : [],
    }))

    try {
      const page = await getApplicationLogEntries({
        nodeId,
        source: nextSourceName,
        limit: appLogPageLimit,
        cursor: options.cursor,
        keyword: keyword.trim(),
        levels,
      })
      setState((current) => ({
        entries: options.cursor ? appendEntries(current.entries, page.items) : page.items,
        cursor: page.cursor,
        rotated: page.rotated,
        loading: false,
        refreshing: false,
        error: null,
      }))
      setLiveMessage(page.rotated ? intl.formatMessage({ id: "appLogs.status.rotated" }) : "")
    } catch (error) {
      setState((current) => ({
        ...current,
        loading: false,
        refreshing: false,
        error: error instanceof Error ? error : new Error("application log request failed"),
      }))
    }
  }, [intl, keyword, levels])

  useEffect(() => {
    let cancelled = false

    async function loadNodes() {
      try {
        const nextNodes = await getNodes()
        if (cancelled) {
          return
        }
        setNodes(nextNodes)
        setSelectedNodeId((current) => {
          if (current !== null && hasNode(nextNodes, current)) {
            return current
          }
          return defaultNodeId(nextNodes)
        })
      } catch (error) {
        if (cancelled) {
          return
        }
        setNodes(null)
        setSelectedNodeId(null)
        setState({
          ...emptyLogState(),
          loading: false,
          error: error instanceof Error ? error : new Error("node request failed"),
        })
      }
    }

    void loadNodes()
    return () => {
      cancelled = true
    }
  }, [])

  useEffect(() => {
    let cancelled = false

    async function loadSources(nodeId: number) {
      try {
        const response = await getApplicationLogSources(nodeId)
        if (cancelled) {
          return
        }
        setSources(response.sources)
        setSource((current) => nextSource(response.sources, current))
      } catch (error) {
        if (cancelled) {
          return
        }
        setSources([])
        setState({
          ...emptyLogState(),
          loading: false,
          error: error instanceof Error ? error : new Error("application log source request failed"),
        })
      }
    }

    if (selectedNodeId === null) {
      setSources([])
      setState({ ...emptyLogState(), loading: false })
      return
    }
    void loadSources(selectedNodeId)
    return () => {
      cancelled = true
    }
  }, [selectedNodeId])

  useEffect(() => {
    if (selectedNodeId === null || sources.length === 0) {
      return
    }
    void loadEntries(selectedNodeId, source)
  }, [loadEntries, selectedNodeId, source, sources.length])

  useEffect(() => {
    if (!followTail || selectedNodeId === null) {
      return
    }

    const nodeId = selectedNodeId
    let cancelled = false
    let timer: number | undefined

    async function poll() {
      try {
        const response = await streamApplicationLogEntries({
          nodeId,
          source,
          limit: appLogPageLimit,
          cursor: cursorRef.current,
          keyword: keyword.trim(),
          levels,
        })
        const events = parseStreamEvents(await response.text())
        if (cancelled) {
          return
        }
        setState((current) => {
          let nextCursor = current.cursor
          let rotated = current.rotated
          const nextEntries: ManagerApplicationLogEntry[] = []

          for (const event of events) {
            if (event.cursor) {
              nextCursor = event.cursor
            }
            if (event.type === "rotation") {
              rotated = true
            }
            if (event.type === "line" && event.item) {
              nextEntries.push(event.item)
            }
          }

          return {
            ...current,
            entries: nextEntries.length > 0 ? appendEntries(current.entries, nextEntries) : current.entries,
            cursor: nextCursor,
            rotated,
          }
        })
        if (events.some((event) => event.type === "rotation")) {
          setLiveMessage(intl.formatMessage({ id: "appLogs.status.rotated" }))
        } else if (events.some((event) => event.type === "line")) {
          setLiveMessage(intl.formatMessage({ id: "appLogs.status.live" }))
        } else {
          setLiveMessage(intl.formatMessage({ id: "appLogs.status.heartbeat" }))
        }
      } catch {
        if (!cancelled) {
          setLiveMessage(intl.formatMessage({ id: "appLogs.status.error" }))
        }
      }

      if (!cancelled) {
        timer = window.setTimeout(poll, livePollIntervalMs)
      }
    }

    void poll()
    return () => {
      cancelled = true
      if (timer) {
        window.clearTimeout(timer)
      }
    }
  }, [followTail, intl, keyword, level, levels, selectedNodeId, source])

  const title = intl.formatMessage({ id: "appLogs.title" })
  const canLoad = selectedNodeId !== null && sources.length > 0
  const lineCount = intl.formatMessage({ id: "appLogs.lineCount" }, { count: state.entries.length })
  const emptyDescription = keyword || level
    ? intl.formatMessage({ id: "appLogs.empty.filtered" })
    : intl.formatMessage({ id: "appLogs.empty" })

  return (
    <div className="space-y-4">
      <h2 className="sr-only">{title}</h2>
      <SectionCard
        action={(
          <Button
            disabled={!canLoad || state.refreshing}
            onClick={() => selectedNodeId !== null && void loadEntries(selectedNodeId, source, { refreshing: true })}
            size="sm"
            type="button"
            variant="outline"
          >
            <RefreshCw />
            {state.refreshing ? intl.formatMessage({ id: "common.refreshing" }) : intl.formatMessage({ id: "common.refresh" })}
          </Button>
        )}
        description={intl.formatMessage({ id: "appLogs.description" })}
        title={title}
      >
        <div className="grid gap-3 lg:grid-cols-[minmax(0,1fr)_auto] lg:items-end">
          <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-5">
            <NodeFilter nodes={nodes} selectedNodeId={selectedNodeId} onNodeChange={setSelectedNodeId} />
            <label className="text-xs font-medium text-muted-foreground">
              {intl.formatMessage({ id: "appLogs.source" })}
              <select
                aria-label={intl.formatMessage({ id: "appLogs.source" })}
                className="mt-1 h-8 w-full rounded-md border border-border bg-background px-2 text-sm text-foreground"
                disabled={sources.length === 0}
                onChange={(event) => setSource(event.target.value)}
                value={source}
              >
                {sources.map((item) => (
                  <option disabled={!item.available} key={item.name} value={item.name}>
                    {item.name}
                  </option>
                ))}
              </select>
            </label>
            <label className="text-xs font-medium text-muted-foreground">
              {intl.formatMessage({ id: "appLogs.level" })}
              <select
                aria-label={intl.formatMessage({ id: "appLogs.level" })}
                className="mt-1 h-8 w-full rounded-md border border-border bg-background px-2 text-sm text-foreground"
                onChange={(event) => setLevel(event.target.value)}
                value={level}
              >
                {levelOptions.map((option) => (
                  <option key={option || "all"} value={option}>
                    {option || intl.formatMessage({ id: "appLogs.level.all" })}
                  </option>
                ))}
              </select>
            </label>
            <label className="text-xs font-medium text-muted-foreground">
              {intl.formatMessage({ id: "appLogs.keyword" })}
              <input
                aria-label={intl.formatMessage({ id: "appLogs.keyword" })}
                className="mt-1 h-8 w-full rounded-md border border-border bg-background px-2 text-sm text-foreground"
                onChange={(event) => setKeyword(event.target.value)}
                placeholder={intl.formatMessage({ id: "appLogs.keyword.placeholder" })}
                value={keyword}
              />
            </label>
            <label className="flex items-center gap-2 self-end text-sm font-medium text-foreground">
              <input
                checked={followTail}
                className="size-4 rounded border-border"
                disabled={!canLoad}
                onChange={(event) => setFollowTail(event.target.checked)}
                type="checkbox"
              />
              {intl.formatMessage({ id: "appLogs.followTail" })}
            </label>
          </div>
          <Button
            disabled={!canLoad || state.loading}
            onClick={() => selectedNodeId !== null && void loadEntries(selectedNodeId, source)}
            size="sm"
            type="button"
          >
            <Search />
            {intl.formatMessage({ id: "common.search" })}
          </Button>
        </div>

        <div className="mt-4 flex flex-wrap items-center gap-x-4 gap-y-2 text-xs text-muted-foreground">
          <span className="font-medium text-foreground">{lineCount}</span>
          <span>{intl.formatMessage({ id: "appLogs.cursor" })}: {state.cursor || "-"}</span>
          <span>
            {intl.formatMessage({ id: "appLogs.activeFile" })}: <span>{activeSource?.file ?? "-"}</span>
          </span>
          <span>{intl.formatMessage({ id: "appLogs.size" })}: {activeSource ? formatBytes(activeSource.size_bytes) : "-"}</span>
          {followTail ? (
            <span className="inline-flex items-center gap-1 font-medium text-[var(--status-running)]">
              <Play className="size-3" />
              {liveMessage || intl.formatMessage({ id: "appLogs.status.following" })}
            </span>
          ) : (
            <span className="inline-flex items-center gap-1">
              <Pause className="size-3" />
              {liveMessage || intl.formatMessage({ id: "appLogs.status.paused" })}
            </span>
          )}
        </div>
      </SectionCard>

      {state.loading ? (
        <ResourceState kind="loading" title={title} />
      ) : state.error ? (
        <ResourceState
          kind={mapErrorKind(state.error)}
          onRetry={() => selectedNodeId !== null && void loadEntries(selectedNodeId, source)}
          title={title}
        />
      ) : state.entries.length === 0 ? (
        <ResourceState description={emptyDescription} kind="empty" title={title} />
      ) : (
        <section aria-label={title} className="overflow-hidden rounded-lg border border-border bg-card">
          <div className="divide-y divide-border font-mono text-xs" role="log">
            {state.entries.map((entry) => (
              <article className="grid gap-2 px-3 py-2 md:grid-cols-[10rem_4rem_minmax(0,1fr)]" key={`${entry.seq}-${entry.offset}-${entry.raw}`}>
                <div className="text-muted-foreground">{entry.time || "-"}</div>
                <div className="font-semibold text-foreground">{entry.level || "-"}</div>
                <div className="min-w-0">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="break-words text-foreground">{entry.message || entry.raw}</span>
                    {entry.module ? <span className="text-muted-foreground">{entry.module}</span> : null}
                    {entry.caller ? <span className="text-muted-foreground">{entry.caller}</span> : null}
                  </div>
                  <pre className="mt-1 whitespace-pre-wrap break-words text-muted-foreground">{entry.raw}</pre>
                  {formatFields(entry.fields) ? (
                    <pre className="mt-1 whitespace-pre-wrap break-words text-muted-foreground">{formatFields(entry.fields)}</pre>
                  ) : null}
                </div>
              </article>
            ))}
          </div>
          <div className="border-t border-border p-3">
            <Button
              disabled={!state.cursor || state.refreshing || selectedNodeId === null}
              onClick={() => selectedNodeId !== null && void loadEntries(selectedNodeId, source, { cursor: state.cursor, refreshing: true })}
              size="sm"
              type="button"
              variant="outline"
            >
              {intl.formatMessage({ id: "common.loadMore" })}
            </Button>
          </div>
        </section>
      )}
    </div>
  )
}

export function AppLogsPage() {
  const intl = useIntl()
  return (
    <PageContainer>
      <PageHeader
        description={intl.formatMessage({ id: "appLogs.description" })}
        eyebrow={intl.formatMessage({ id: "nav.path.cluster.diagnostics" })}
        title={intl.formatMessage({ id: "appLogs.title" })}
      />
      <AppLogsPanel />
    </PageContainer>
  )
}
