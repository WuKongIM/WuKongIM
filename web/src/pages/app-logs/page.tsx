import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { useIntl } from "react-intl"

import { defaultNodeId, hasNode } from "@/components/manager/node-filter"
import { ResourceState } from "@/components/manager/resource-state"
import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
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
import { AppLogConsole } from "@/pages/app-logs/components/app-log-console"
import { AppLogsToolbar } from "@/pages/app-logs/components/app-logs-toolbar"
import {
  levelsForSeverityFilter,
  type AppLogSeverityFilter,
} from "@/pages/app-logs/log-format"

const appLogPageLimit = 100
const appLogMaxTailLimit = 1000
const maxBufferedLines = 500
const livePollIntervalMs = 2000

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
  const [keyword, setKeyword] = useState(() => new URLSearchParams(window.location.search).get("keyword")?.trim() ?? "")
  const [severity, setSeverity] = useState<AppLogSeverityFilter>("")
  const [followTail, setFollowTail] = useState(false)
  const [liveMessage, setLiveMessage] = useState("")
  const [newLiveLineCount, setNewLiveLineCount] = useState(0)
  const [state, setState] = useState<AppLogState>(emptyLogState)
  const cursorRef = useRef("")

  useEffect(() => {
    cursorRef.current = state.cursor
  }, [state.cursor])

  const activeSource = useMemo(
    () => sources.find((item) => item.name === source) ?? null,
    [source, sources],
  )

  const levels = useMemo(() => levelsForSeverityFilter(severity), [severity])

  const loadEntries = useCallback(async (
    nodeId: number,
    nextSourceName: string,
    options: { cursor?: string; limit?: number; refreshing?: boolean } = {},
  ) => {
    setNewLiveLineCount(0)
    setState((current) => ({
      ...current,
      loading: !options.cursor && !options.refreshing,
      refreshing: Boolean(options.refreshing),
      error: null,
      entries: options.cursor || options.refreshing ? current.entries : [],
    }))

    try {
      const page = await getApplicationLogEntries({
        nodeId,
        source: nextSourceName,
        limit: options.limit ?? appLogPageLimit,
        keyword: keyword.trim(),
        levels,
        ...(options.cursor ? { cursor: options.cursor } : {}),
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

          if (nextEntries.length > 0) {
            setNewLiveLineCount((count) => count + nextEntries.length)
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
  }, [followTail, intl, keyword, levels, selectedNodeId, source])

  const title = intl.formatMessage({ id: "appLogs.title" })
  const canLoad = selectedNodeId !== null && sources.length > 0
  const lineCount = intl.formatMessage({ id: "appLogs.lineCount" }, { count: state.entries.length })
  const emptyDescription = keyword || severity
    ? intl.formatMessage({ id: "appLogs.empty.filtered" })
    : intl.formatMessage({ id: "appLogs.empty" })
  const nextTailLimit = Math.min(
    appLogMaxTailLimit,
    Math.max(state.entries.length + appLogPageLimit, appLogPageLimit * 2),
  )

  return (
    <div className="space-y-4">
      <section className="border-b border-border pb-3" data-app-logs-surface="controls">
        <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
          <span className="font-medium text-foreground">{intl.formatMessage({ id: "appLogs.scopeHint" })}</span>
          <span>{intl.formatMessage({ id: "appLogs.distributedHint" })}</span>
        </div>
        <div className="mt-3">
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
        </div>
      </section>

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
        <AppLogConsole
          activeSource={activeSource}
          atMaxTail={state.entries.length >= appLogMaxTailLimit}
          entries={state.entries}
          followTail={followTail}
          lineCount={lineCount}
          newLiveLineCount={newLiveLineCount}
          onAcknowledgeLiveLines={() => setNewLiveLineCount(0)}
          onLoadMore={() =>
            selectedNodeId !== null &&
            void loadEntries(selectedNodeId, source, { limit: nextTailLimit, refreshing: true })
          }
          refreshing={state.refreshing}
          rotated={state.rotated}
          selectedNodeId={selectedNodeId}
          source={source}
          title={title}
        />
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
        eyebrow={intl.formatMessage({ id: "nav.path.cluster.systemLogs" })}
        title={intl.formatMessage({ id: "appLogs.title" })}
      />
      <AppLogsPanel />
    </PageContainer>
  )
}
