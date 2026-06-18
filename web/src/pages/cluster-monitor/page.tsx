import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { useIntl } from "react-intl"

import { selectedMonitorNodeLabel } from "@/components/manager/monitor-node-selector"
import { monitorRefreshIntervalMs, type MonitorRefreshInterval } from "@/components/manager/monitor-refresh-controls"
import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
import { getClusterRealtimeMonitor, getNodes } from "@/lib/manager-api"
import type {
  ClusterRealtimeMonitorCard,
  ClusterRealtimeMonitorResponse,
  ClusterRealtimeMonitorSnapshotEntry as ApiSnapshotEntry,
  ClusterRealtimeMonitorStat as ApiStat,
  ClusterRealtimeMonitorTone,
  ManagerNodesResponse,
} from "@/lib/manager-api.types"

import { ClusterMonitorCardGrid } from "./components/cluster-monitor-card-grid"
import { ClusterMonitorSnapshotStrip } from "./components/cluster-monitor-snapshot-strip"
import { ClusterMonitorToolbar } from "./components/cluster-monitor-toolbar"
import {
  clusterMonitorMetricConfig,
  clusterMonitorSnapshotLabelIds,
  clusterMonitorStageLabelIds,
  clusterMonitorStatLabelIds,
  clusterMonitorStatusByTone,
} from "./metric-config"
import type {
  ClusterMonitorMetricCard,
  ClusterMonitorMetricKey,
  ClusterMonitorSnapshotEntry,
  ClusterMonitorStage,
  ClusterMonitorTimeRange,
  ClusterMonitorTone,
  PreviewClusterMonitorModel,
} from "./types"

type ClusterMonitorPageState =
  | { kind: "loading" }
  | { kind: "ready"; response: ClusterRealtimeMonitorResponse }
  | { kind: "error"; message: string }

export function ClusterMonitorPage() {
  const intl = useIntl()
  const [timeRange, setTimeRange] = useState<ClusterMonitorTimeRange>("15m")
  const [refreshInterval, setRefreshInterval] = useState<MonitorRefreshInterval>("30s")
  const [refreshNonce, setRefreshNonce] = useState(0)
  const [nodes, setNodes] = useState<ManagerNodesResponse | null>(null)
  const [selectedNodeId, setSelectedNodeId] = useState<number | null>(null)
  const [state, setState] = useState<ClusterMonitorPageState>({ kind: "loading" })
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

    getClusterRealtimeMonitor({ window: timeRange, ...(selectedNodeId ? { nodeId: selectedNodeId } : {}) })
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
    if (state.kind !== "ready" || !isRenderableClusterMonitor(state.response)) return null
    return buildClusterRealtimeMonitorModel(state.response, timeRange, refreshInterval === "off")
  }, [refreshInterval, state, timeRange])
  const generatedAt = state.kind === "ready" ? state.response.generated_at : new Date().toISOString()
  const sourceError = state.kind === "ready" ? getSourceError(state.response) : undefined
  const scopeLabel = selectedNodeId
    ? intl.formatMessage(
        { id: "clusterMonitor.scope.node" },
        { node: selectedMonitorNodeLabel(intl, nodes, selectedNodeId) },
      )
    : undefined

  return (
    <PageContainer className="max-w-[1600px] gap-4">
      <PageHeader
        description={intl.formatMessage({ id: "clusterMonitor.description" })}
        eyebrow={intl.formatMessage({ id: "clusterMonitor.liveBadge" })}
        title={intl.formatMessage({ id: "clusterMonitor.title" })}
      />

      <ClusterMonitorToolbar
        generatedAt={model?.generatedAt ?? generatedAt}
        nodes={nodes}
        onNodeChange={setSelectedNodeId}
        onRefresh={requestRefresh}
        onRefreshIntervalChange={setRefreshInterval}
        onTimeRangeChange={setTimeRange}
        refreshInterval={refreshInterval}
        scopeLabel={scopeLabel}
        scopeLabelId={model?.scopeLabelId ?? "clusterMonitor.scope.global"}
        selectedNodeId={selectedNodeId}
        timeRange={model?.timeRange ?? timeRange}
      />

      {state.kind === "loading" ? <ClusterMonitorLoadingState /> : null}
      {state.kind === "error" ? <ClusterMonitorSourceState kind="unavailable" message={state.message} /> : null}
      {state.kind === "ready" && state.response.status === "prometheus_disabled" ? (
        <ClusterMonitorSourceState kind="disabled" message={sourceError} />
      ) : null}
      {state.kind === "ready" && state.response.status === "prometheus_unavailable" ? (
        <ClusterMonitorSourceState kind="unavailable" message={sourceError} />
      ) : null}
      {model ? (
        <>
          {model.snapshot.length > 0 ? <ClusterMonitorSnapshotStrip entries={model.snapshot} /> : null}
          <ClusterMonitorCardGrid cards={model.cards} />
        </>
      ) : null}
    </PageContainer>
  )
}

function isRenderableClusterMonitor(response: ClusterRealtimeMonitorResponse) {
  if (response.status === "ready" || response.status === "partial") {
    return response.snapshot.some((entry) => clusterMonitorSnapshotLabelIds[entry.key]) || response.cards.some(isKnownClusterCard)
  }
  if (response.status === "prometheus_unavailable") {
    return response.cards.some((card) => card.available && isKnownClusterCard(card))
  }
  return false
}

function buildClusterRealtimeMonitorModel(
  response: ClusterRealtimeMonitorResponse,
  timeRange: ClusterMonitorTimeRange,
  isPaused: boolean,
): PreviewClusterMonitorModel {
  const cards = response.cards.flatMap((card) => {
    const mapped = mapClusterRealtimeCard(card)
    return mapped ? [mapped] : []
  })

  return {
    generatedAt: response.generated_at,
    scopeLabelId: "clusterMonitor.scope.global",
    timeRange,
    isPaused,
    snapshot: response.snapshot.flatMap((entry) => {
      const mapped = mapClusterRealtimeSnapshot(entry)
      return mapped ? [mapped] : []
    }),
    cards,
  }
}

function mapClusterRealtimeCard(card: ClusterRealtimeMonitorCard): ClusterMonitorMetricCard | null {
  if (!isClusterMonitorMetricKey(card.key)) return null

  const config = clusterMonitorMetricConfig[card.key]
  const stage = normalizeStage(card.stage, config.stage)
  const tone = normalizeTone(card.tone, config.tone)
  const rawUnit = card.unit ?? ""
  const rawSeries = clusterCardSeries(card)
  const rawStats = clusterCardStats(card)
  const displayScale = clusterDisplayScale(card)
  const unit = displayScale.unit
  const series = card.available ? scaleClusterSeries(rawSeries, displayScale.factor) : []
  const value = card.available ? formatApiValue(scaleClusterValue(card, displayScale.factor), config.precision) : "-"
  const stats = card.available ? mapClusterStats(rawStats, rawUnit, unit, config.precision, displayScale.factor) : unavailableStats(card.error)

  return {
    key: card.key,
    titleId: config.titleId,
    helpId: config.helpId,
    stage,
    stageLabelId: clusterMonitorStageLabelIds[stage],
    statusId: card.available ? clusterMonitorStatusByTone[tone] : "clusterMonitor.status.unavailable",
    tone,
    unit,
    value,
    available: card.available,
    error: card.error,
    series,
    stats,
    chartColor: config.chartColor,
  }
}

function mapClusterRealtimeSnapshot(entry: ApiSnapshotEntry): ClusterMonitorSnapshotEntry | null {
  const labelId = clusterMonitorSnapshotLabelIds[entry.key]
  if (!labelId) return null

  return {
    key: entry.key,
    labelId,
    value: formatApiValue(entry, entry.unit === "%" ? 2 : entry.unit ? 1 : 0),
    unit: entry.unit,
    tone: normalizeTone(entry.tone, "normal"),
  }
}

function mapClusterStats(stats: ApiStat[], rawCardUnit: string, displayCardUnit: string, precision: number, displayFactor: number) {
  return stats.flatMap((stat) => {
    const labelId = clusterMonitorStatLabelIds[stat.key]
    if (!labelId && !stat.label) return []
    const rawUnit = stat.unit ?? rawCardUnit
    const displayUnit = isScalableByteUnit(rawUnit) ? displayCardUnit : rawUnit
    const value = isScalableByteUnit(rawUnit) ? scaleClusterStat(stat, displayFactor) : stat

    return [
      {
        labelId,
        label: stat.label,
        value: formatApiStatValue(value, displayUnit, precision),
      },
    ]
  })
}

function unavailableStats(error: string) {
  return [
    {
      labelId: "clusterMonitor.stat.unavailableReason",
      value: error || "-",
    },
  ]
}

function isKnownClusterCard(card: ClusterRealtimeMonitorCard) {
  return isClusterMonitorMetricKey(card.key)
}

function isClusterMonitorMetricKey(key: string): key is ClusterMonitorMetricKey {
  return key in clusterMonitorMetricConfig
}

function normalizeStage(stage: string, fallback: ClusterMonitorStage): ClusterMonitorStage {
  if (stage in clusterMonitorStageLabelIds) return stage as ClusterMonitorStage
  return fallback
}

function normalizeTone(tone: ClusterRealtimeMonitorTone | string | undefined, fallback: ClusterMonitorTone): ClusterMonitorTone {
  if (tone === "normal" || tone === "warning" || tone === "critical") return tone
  return fallback === "preview" ? "normal" : fallback
}

function formatApiValue(value: { value?: number; text?: string; unit?: string }, precision: number) {
  if (value.text !== undefined && value.text !== "") return value.text
  if (typeof value.value === "number") return formatClusterNumber(value.value, precision)
  return "-"
}

function formatApiStatValue(stat: ApiStat, unit: string, precision: number) {
  if (stat.text !== undefined && stat.text !== "") return stat.text
  if (typeof stat.value !== "number") return "-"
  return appendClusterUnit(formatClusterNumber(stat.value, precision), unit)
}

function formatClusterNumber(value: number, precision: number) {
  return value.toLocaleString("en-US", {
    maximumFractionDigits: precision,
    minimumFractionDigits: precision > 0 && Math.abs(value) < 10 ? precision : 0,
  })
}

function appendClusterUnit(value: string, unit: string) {
  if (!unit) return value
  if (unit === "%" || unit === "x" || unit.startsWith("/")) return `${value}${unit}`
  return `${value} ${unit}`
}

type ClusterDisplayScale = {
  factor: number
  unit: string
}

function clusterDisplayScale(card: ClusterRealtimeMonitorCard): ClusterDisplayScale {
  const unit = card.unit ?? ""
  if (!isScalableByteUnit(unit)) return { factor: 1, unit }

  const currentValue = Math.abs(card.value ?? 0)
  if (currentValue > 0) return byteDisplayScale(currentValue, unit)

  let maxValue = currentValue
  for (const point of clusterCardSeries(card)) {
    maxValue = Math.max(maxValue, Math.abs(point.value))
  }
  for (const stat of clusterCardStats(card)) {
    if (typeof stat.value === "number" && isScalableByteUnit(stat.unit ?? unit)) {
      maxValue = Math.max(maxValue, Math.abs(stat.value))
    }
  }
  return byteDisplayScale(maxValue, unit)
}

function byteDisplayScale(value: number, unit: string): ClusterDisplayScale {
  const suffix = isByteRateUnit(unit) ? "/s" : ""
  const units = ["B", "KB", "MB", "GB", "TB"].map((item) => `${item}${suffix}`)
  let factor = 1
  let unitIndex = 0
  while (value >= 1024 && unitIndex < units.length - 1) {
    value /= 1024
    factor *= 1024
    unitIndex += 1
  }
  return { factor, unit: units[unitIndex] }
}

function isScalableByteUnit(unit: string) {
  return unit === "B" || isByteRateUnit(unit)
}

function isByteRateUnit(unit: string) {
  return unit === "B/s"
}

function clusterCardSeries(card: ClusterRealtimeMonitorCard) {
  return Array.isArray(card.series) ? card.series : []
}

function clusterCardStats(card: ClusterRealtimeMonitorCard) {
  return Array.isArray(card.stats) ? card.stats : []
}

function scaleClusterValue<T extends { value?: number; text?: string; unit?: string }>(value: T, factor: number): T {
  if (factor === 1 || typeof value.value !== "number" || value.text) return value
  return { ...value, value: value.value / factor }
}

function scaleClusterStat(stat: ApiStat, factor: number): ApiStat {
  if (factor === 1 || typeof stat.value !== "number" || stat.text) return stat
  return { ...stat, value: stat.value / factor }
}

function scaleClusterSeries(series: ClusterRealtimeMonitorCard["series"], factor: number) {
  return series.map((point) => ({
    timestamp: point.timestamp,
    value: factor === 1 ? point.value : point.value / factor,
    label: point.label,
    seriesKey: point.series_key,
  }))
}

function getSourceError(response: ClusterRealtimeMonitorResponse) {
  return response.sources.prometheus.error || response.sources.control_snapshot.error
}

function ClusterMonitorLoadingState() {
  const intl = useIntl()

  return (
    <section className="rounded-lg border border-border/80 bg-card/82 px-4 py-4 text-sm text-muted-foreground" role="status">
      {intl.formatMessage({ id: "clusterMonitor.prometheus.loading" })}
    </section>
  )
}

function ClusterMonitorSourceState({ kind, message }: { kind: "disabled" | "unavailable"; message?: string }) {
  const intl = useIntl()
  const isDisabled = kind === "disabled"

  return (
    <section className="rounded-lg border border-border/80 bg-card/88 px-5 py-6 text-sm text-muted-foreground" role="status">
      <div className="flex items-center gap-2 text-sm font-semibold text-foreground">
        <span className={isDisabled ? "size-2 rounded-full bg-warning" : "size-2 rounded-full bg-destructive"} />
        {intl.formatMessage({
          id: isDisabled ? "clusterMonitor.prometheus.disabledTitle" : "clusterMonitor.prometheus.unavailableTitle",
        })}
      </div>
      <p className="mt-2 max-w-3xl leading-6">
        {intl.formatMessage({
          id: isDisabled ? "clusterMonitor.prometheus.disabledDescription" : "clusterMonitor.prometheus.unavailableDescription",
        })}
      </p>
      {message ? <p className="mt-2 max-w-3xl text-xs leading-5 text-muted-foreground">{message}</p> : null}
      {isDisabled ? (
        <div className="mt-4 flex flex-wrap gap-2">
          <code className="rounded-md border border-border bg-background px-2.5 py-1 text-xs text-foreground">WK_METRICS_ENABLE=true</code>
          <code className="rounded-md border border-border bg-background px-2.5 py-1 text-xs text-foreground">
            WK_PROMETHEUS_ENABLE=true
          </code>
        </div>
      ) : null}
    </section>
  )
}
