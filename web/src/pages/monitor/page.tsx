import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { useIntl } from "react-intl"

import { selectedMonitorNodeLabel } from "@/components/manager/monitor-node-selector"
import { monitorRefreshIntervalMs, type MonitorRefreshInterval } from "@/components/manager/monitor-refresh-controls"
import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
import { getNodes, getRealtimeMonitor } from "@/lib/manager-api"
import type {
  ManagerNodesResponse,
  RealtimeMonitorCard,
  RealtimeMonitorResponse,
  RealtimeMonitorSnapshotEntry as ApiSnapshotEntry,
  RealtimeMonitorTone,
} from "@/lib/manager-api.types"

import { MonitorCardGrid } from "./components/monitor-card-grid"
import { MonitorSnapshotStrip } from "./components/monitor-snapshot-strip"
import { MonitorToolbar } from "./components/monitor-toolbar"
import {
  monitorMetricConfig,
  monitorSnapshotLabelIds,
  monitorStageLabelIds,
  monitorStatLabelIds,
  monitorStatusByTone,
  monitorUnavailableReasonLabelIds,
} from "./metric-config"
import type {
  MonitorMetricCard,
  MonitorMetricKey,
  MonitorSnapshotEntry,
  MonitorStage,
  MonitorTone,
  PreviewMonitorModel,
  TimeRange,
} from "./types"

type MonitorPageState =
  | { kind: "loading" }
  | { kind: "ready"; response: RealtimeMonitorResponse }
  | { kind: "error"; message: string }

export function MonitorPage() {
  const intl = useIntl()
  const [timeRange, setTimeRange] = useState<TimeRange>("15m")
  const [refreshInterval, setRefreshInterval] = useState<MonitorRefreshInterval>("30s")
  const [refreshNonce, setRefreshNonce] = useState(0)
  const [nodes, setNodes] = useState<ManagerNodesResponse | null>(null)
  const [selectedNodeId, setSelectedNodeId] = useState<number | null>(null)
  const [state, setState] = useState<MonitorPageState>({ kind: "loading" })
  const lastQueryKeyRef = useRef<string | null>(null)
  const requestRefresh = useCallback(() => {
    setRefreshNonce((current) => current + 1)
  }, [])

  useEffect(() => {
    let cancelled = false
    getNodes()
      .then((response) => {
        if (!cancelled) {
          setNodes(response)
        }
      })
      .catch(() => {
        if (!cancelled) {
          setNodes(null)
        }
      })

    return () => {
      cancelled = true
    }
  }, [])

  useEffect(() => {
    let cancelled = false
    const queryKey = `${timeRange}:${selectedNodeId ?? "all"}`
    const isSameQuery = lastQueryKeyRef.current === queryKey
    lastQueryKeyRef.current = queryKey
    setState((current) => (isSameQuery && current.kind === "ready" ? current : { kind: "loading" }))

    getRealtimeMonitor({ window: timeRange, ...(selectedNodeId ? { nodeId: selectedNodeId } : {}) })
      .then((response) => {
        if (!cancelled) {
          setState({ kind: "ready", response })
        }
      })
      .catch((error: unknown) => {
        if (!cancelled) {
          setState({ kind: "error", message: error instanceof Error ? error.message : String(error) })
        }
      })

    return () => {
      cancelled = true
    }
  }, [refreshNonce, selectedNodeId, timeRange])

  useEffect(() => {
    const intervalMs = monitorRefreshIntervalMs(refreshInterval)
    if (intervalMs === null) return undefined

    const intervalId = window.setInterval(requestRefresh, intervalMs)
    return () => window.clearInterval(intervalId)
  }, [refreshInterval, requestRefresh])

  const model = useMemo(() => {
    if (state.kind !== "ready" || !isRenderableMonitor(state.response)) return null
    return buildRealtimeMonitorModel(state.response, timeRange, refreshInterval === "off")
  }, [refreshInterval, state, timeRange])
  const generatedAt = state.kind === "ready" ? state.response.generated_at : new Date().toISOString()
  const sourceError = state.kind === "ready" ? state.response.sources.prometheus.error : undefined
  const scopeLabel = selectedNodeId
    ? intl.formatMessage(
        { id: "monitor.scope.node" },
        { node: selectedMonitorNodeLabel(intl, nodes, selectedNodeId) },
      )
    : undefined

  return (
    <PageContainer className="max-w-[1600px] gap-4">
      <PageHeader
        description={intl.formatMessage({ id: "monitor.cardWallDescription" })}
        eyebrow={intl.formatMessage({ id: "monitor.liveBadge" })}
        title={intl.formatMessage({ id: "monitor.title" })}
      />

      <MonitorToolbar
        generatedAt={model?.generatedAt ?? generatedAt}
        nodes={nodes}
        onNodeChange={setSelectedNodeId}
        onRefresh={requestRefresh}
        onRefreshIntervalChange={setRefreshInterval}
        onTimeRangeChange={setTimeRange}
        refreshInterval={refreshInterval}
        scopeLabel={scopeLabel}
        scopeLabelId={model?.scopeLabelId ?? "monitor.scope.global"}
        selectedNodeId={selectedNodeId}
        timeRange={model?.timeRange ?? timeRange}
      />

      {state.kind === "loading" ? <MonitorLoadingState /> : null}
      {state.kind === "error" ? <MonitorSourceState kind="unavailable" message={state.message} /> : null}
      {state.kind === "ready" && state.response.status === "prometheus_disabled" ? (
        <MonitorSourceState kind="disabled" message={sourceError} />
      ) : null}
      {state.kind === "ready" && state.response.status === "prometheus_unavailable" ? (
        <MonitorSourceState kind="unavailable" message={sourceError} />
      ) : null}
      {model ? (
        <>
          {model.snapshot.length > 0 ? <MonitorSnapshotStrip entries={model.snapshot} /> : null}
          <MonitorCardGrid cards={model.cards} />
        </>
      ) : null}
    </PageContainer>
  )
}

function isRenderableMonitor(response: RealtimeMonitorResponse) {
  return (
    response.status === "ready" ||
    response.status === "partial" ||
    response.status === "prometheus_unavailable"
  ) && response.cards.some((card) => isMonitorMetricKey(card.key))
}

function buildRealtimeMonitorModel(
  response: RealtimeMonitorResponse,
  timeRange: TimeRange,
  isPaused: boolean,
): PreviewMonitorModel {
  const cards = response.cards.flatMap((card) => {
    const mapped = mapRealtimeCard(card)
    return mapped ? [mapped] : []
  })

  return {
    generatedAt: response.generated_at,
    scopeLabelId: "monitor.scope.global",
    timeRange,
    isPaused,
    snapshot: response.snapshot.flatMap((entry) => {
      const mapped = mapRealtimeSnapshot(entry)
      return mapped ? [mapped] : []
    }),
    cards,
  }
}

function mapRealtimeCard(card: RealtimeMonitorCard): MonitorMetricCard | null {
  if (!isMonitorMetricKey(card.key)) return null
  const config = monitorMetricConfig[card.key]
  const stage = normalizeStage(card.stage)
  const tone = normalizeTone(card.tone)
  const available = card.available && card.series.length > 0
  const unavailableReasonLabelId = available
    ? undefined
    : ((card.unavailable_reason ? monitorUnavailableReasonLabelIds[card.unavailable_reason] : undefined) ??
      "monitor.noData.unavailable")

  return {
    key: card.key,
    titleId: config.titleId,
    helpId: config.helpId,
    stage,
    stageLabelId: monitorStageLabelIds[stage],
    statusId: available ? monitorStatusByTone[tone] : "monitor.status.noData",
    tone,
    unit: card.unit,
    value: available ? formatMonitorNumber(card.value, config.precision) : "-",
    available,
    unavailableReasonLabelId,
    error: card.error,
    series: card.series,
    stats: available
      ? card.stats.map((stat) => ({
          labelId: monitorStatLabelIds[stat.key] ?? "monitor.stat.avg",
          value:
            stat.key === "total"
              ? formatMonitorNumber(stat.value, 0)
              : appendMonitorUnit(formatMonitorNumber(stat.value, config.precision), card.unit),
        }))
      : [],
    chartColor: config.chartColor,
  }
}

function mapRealtimeSnapshot(entry: ApiSnapshotEntry): MonitorSnapshotEntry | null {
  const labelId = monitorSnapshotLabelIds[entry.key]
  if (!labelId) return null

  return {
    key: entry.key,
    labelId,
    value: formatMonitorNumber(entry.value, entry.unit === "%" ? 2 : entry.unit ? 1 : 0),
    unit: entry.unit,
    tone: normalizeTone(entry.tone),
  }
}

function isMonitorMetricKey(key: string): key is MonitorMetricKey {
  return key in monitorMetricConfig
}

function normalizeStage(stage: string): MonitorStage {
  if (stage in monitorStageLabelIds) return stage as MonitorStage
  return "sendEntry"
}

function normalizeTone(tone: RealtimeMonitorTone): MonitorTone {
  if (tone === "warning" || tone === "critical") return tone
  return "normal"
}

function formatMonitorNumber(value: number, precision: number) {
  return value.toLocaleString("en-US", {
    maximumFractionDigits: precision,
    minimumFractionDigits: precision > 0 && Math.abs(value) < 10 ? precision : 0,
  })
}

function appendMonitorUnit(value: string, unit: string) {
  if (!unit) return value
  if (unit === "%" || unit === "x") return `${value}${unit}`
  return `${value} ${unit}`
}

function MonitorLoadingState() {
  const intl = useIntl()

  return (
    <section className="rounded-lg border border-border/80 bg-card/82 px-4 py-4 text-sm text-muted-foreground" role="status">
      {intl.formatMessage({ id: "monitor.prometheus.loading" })}
    </section>
  )
}

function MonitorSourceState({ kind, message }: { kind: "disabled" | "unavailable"; message?: string }) {
  const intl = useIntl()
  const isDisabled = kind === "disabled"

  return (
    <section className="rounded-lg border border-border/80 bg-card/88 px-5 py-6 text-sm text-muted-foreground" role="status">
      <div className="flex items-center gap-2 text-sm font-semibold text-foreground">
        <span className={isDisabled ? "size-2 rounded-full bg-warning" : "size-2 rounded-full bg-destructive"} />
        {intl.formatMessage({ id: isDisabled ? "monitor.prometheus.disabledTitle" : "monitor.prometheus.unavailableTitle" })}
      </div>
      <p className="mt-2 max-w-3xl leading-6">
        {intl.formatMessage({ id: isDisabled ? "monitor.prometheus.disabledDescription" : "monitor.prometheus.unavailableDescription" })}
      </p>
      {message ? <p className="mt-2 max-w-3xl text-xs leading-5 text-muted-foreground">{message}</p> : null}
      {isDisabled ? (
        <div className="mt-4 flex flex-wrap gap-2">
          <code className="rounded-md border border-border bg-background px-2.5 py-1 text-xs text-foreground">WK_METRICS_ENABLE=true</code>
          <code className="rounded-md border border-border bg-background px-2.5 py-1 text-xs text-foreground">WK_PROMETHEUS_ENABLE=true</code>
        </div>
      ) : null}
    </section>
  )
}
