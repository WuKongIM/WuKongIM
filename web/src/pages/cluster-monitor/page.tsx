import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { useIntl } from "react-intl"

import { selectedMonitorNodeLabel } from "@/components/manager/monitor-node-selector"
import { monitorRefreshIntervalMs, type MonitorRefreshInterval } from "@/components/manager/monitor-refresh-controls"
import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
import { getRealtimeMonitor, getNodes } from "@/lib/manager-api"
import type {
  RealtimeMonitorCard,
  RealtimeMonitorCategory,
  RealtimeMonitorResponse,
  RealtimeMonitorSnapshotEntry as ApiSnapshotEntry,
  RealtimeMonitorStat as ApiStat,
  RealtimeMonitorTone,
  ManagerNodesResponse,
} from "@/lib/manager-api.types"

import { ClusterMonitorCardGrid } from "./components/cluster-monitor-card-grid"
import { ClusterMonitorSnapshotStrip } from "./components/cluster-monitor-snapshot-strip"
import { ClusterMonitorToolbar } from "./components/cluster-monitor-toolbar"
import { GoroutineMonitorTable } from "./components/goroutine-monitor-table"
import {
  clusterMonitorMetricConfig,
  clusterMonitorMetricTone,
  clusterMonitorMetricOperationalPriority,
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
  { kind: "loading" } | { kind: "ready"; response: RealtimeMonitorResponse } | { kind: "error"; message: string }

export function ClusterMonitorPage() {
  const intl = useIntl()
  const [timeRange, setTimeRange] = useState<ClusterMonitorTimeRange>("15m")
  const [selectedCategory, setSelectedCategory] = useState<RealtimeMonitorCategory>("common")
  const [refreshInterval, setRefreshInterval] = useState<MonitorRefreshInterval>("30s")
  const [refreshNonce, setRefreshNonce] = useState(0)
  const [nodes, setNodes] = useState<ManagerNodesResponse | null>(null)
  const [selectedNodeId, setSelectedNodeId] = useState<number | null>(null)
  const [state, setState] = useState<ClusterMonitorPageState>({
    kind: "loading",
  })
  const lastQueryKeyRef = useRef<string | null>(null)
  const requestRefresh = useCallback(() => {
    setRefreshNonce((current) => current + 1)
  }, [])
  const changeCategory = useCallback((category: RealtimeMonitorCategory) => {
    setSelectedCategory(category)
    setRefreshInterval((current) => (category === "goroutines" ? "5s" : current === "5s" ? "30s" : current))
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
    const queryKey = `${timeRange}:${selectedNodeId ?? "all"}:${selectedCategory}`
    const isSameQuery = lastQueryKeyRef.current === queryKey
    lastQueryKeyRef.current = queryKey
    setState((current) => (isSameQuery && current.kind === "ready" ? current : { kind: "loading" }))

    getRealtimeMonitor({
      window: timeRange,
      category: selectedCategory,
      ...(selectedNodeId ? { nodeId: selectedNodeId } : {}),
    })
      .then((response) => {
        if (!cancelled) {
          setState({ kind: "ready", response })
        }
      })
      .catch((error: unknown) => {
        if (!cancelled) {
          setState({
            kind: "error",
            message: error instanceof Error ? error.message : String(error),
          })
        }
      })

    return () => {
      cancelled = true
    }
  }, [refreshNonce, selectedCategory, selectedNodeId, timeRange])

  useEffect(() => {
    const intervalMs = monitorRefreshIntervalMs(refreshInterval)
    if (intervalMs === null) return undefined

    const intervalId = window.setInterval(requestRefresh, intervalMs)
    return () => window.clearInterval(intervalId)
  }, [refreshInterval, requestRefresh])

  const model = useMemo(() => {
    if (state.kind !== "ready" || !isRenderableClusterMonitor(state.response)) return null
    return buildRealtimeMonitorModel(state.response, timeRange, refreshInterval === "off", intl.locale)
  }, [intl.locale, refreshInterval, state, timeRange])
  const generatedAt = state.kind === "ready" ? state.response.generated_at : new Date().toISOString()
  const scopeLabel = selectedNodeId
    ? intl.formatMessage({ id: "clusterMonitor.scope.node" }, { node: selectedMonitorNodeLabel(intl, nodes, selectedNodeId) })
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
        onCategoryChange={changeCategory}
        onNodeChange={setSelectedNodeId}
        onRefresh={requestRefresh}
        onRefreshIntervalChange={setRefreshInterval}
        onTimeRangeChange={setTimeRange}
        refreshInterval={refreshInterval}
        selectedCategory={selectedCategory}
        scopeLabel={scopeLabel}
        scopeLabelId={model?.scopeLabelId ?? "clusterMonitor.scope.global"}
        selectedNodeId={selectedNodeId}
        timeRange={model?.timeRange ?? timeRange}
      />

      {state.kind === "loading" ? <ClusterMonitorLoadingState /> : null}
      {state.kind === "error" ? <ClusterMonitorSourceState kind="unavailable" /> : null}
      {state.kind === "ready" && state.response.status === "prometheus_disabled" ? (
        <ClusterMonitorSourceState kind="disabled" />
      ) : null}
      {state.kind === "ready" && state.response.status === "prometheus_unavailable" ? (
        <ClusterMonitorSourceState kind="unavailable" />
      ) : null}
      {model ? (
        <>
          {model.snapshot.length > 0 ? <ClusterMonitorSnapshotStrip entries={model.snapshot} /> : null}
          {selectedCategory === "goroutines" && state.kind === "ready" && state.response.goroutines ? (
            <GoroutineMonitorTable data={state.response.goroutines} showComplete={selectedNodeId !== null} />
          ) : null}
          {model.cards.length > 0 ? <ClusterMonitorCardGrid cards={model.cards} /> : null}
        </>
      ) : null}
    </PageContainer>
  )
}

function isRenderableClusterMonitor(response: RealtimeMonitorResponse) {
  if (response.status === "ready" || response.status === "partial") {
    return (
      response.snapshot.some((entry) => clusterMonitorSnapshotLabelIds[entry.key]) ||
      response.cards.some(isKnownClusterCard) ||
      Boolean(response.goroutines?.nodes.length)
    )
  }
  if (response.status === "prometheus_unavailable") {
    return response.cards.some((card) => card.available && isKnownClusterCard(card))
  }
  return false
}

function buildRealtimeMonitorModel(
  response: RealtimeMonitorResponse,
  timeRange: ClusterMonitorTimeRange,
  isPaused: boolean,
  locale: string,
): PreviewClusterMonitorModel {
  const cards = response.cards.flatMap((card) => {
    const mapped = mapClusterRealtimeCard(card, locale)
    return mapped ? [mapped] : []
  }).sort((left, right) => clusterMonitorMetricOperationalPriority(left.key) - clusterMonitorMetricOperationalPriority(right.key))

  return {
    generatedAt: response.generated_at,
    scopeLabelId: "clusterMonitor.scope.global",
    timeRange,
    isPaused,
    snapshot: response.snapshot.flatMap((entry) => {
      const mapped = mapClusterRealtimeSnapshot(entry, locale)
      return mapped ? [mapped] : []
    }),
    cards,
  }
}

function mapClusterRealtimeCard(card: RealtimeMonitorCard, locale: string): ClusterMonitorMetricCard | null {
  if (!isClusterMonitorMetricKey(card.key)) return null

  const config = clusterMonitorMetricConfig[card.key]
  const stage = normalizeStage(card.stage, config.stage)
  const fallbackTone = normalizeTone(card.tone, config.tone)
  const tone = card.available
    ? clusterMonitorMetricTone(card.key, card.value, fallbackTone)
    : "preview"
  const rawUnit = card.unit ?? ""
  const rawSeries = clusterCardSeries(card)
  const rawStats = clusterCardStats(card)
  const displayScale = clusterDisplayScale(card)
  const unit = displayScale.unit
  const series = card.available ? scaleClusterSeries(rawSeries, displayScale.factor) : []
  const value = card.available ? formatApiValue(scaleClusterValue(card, displayScale.factor), config.precision, locale) : "-"
  const stats = card.available
    ? mapClusterStats(rawStats, rawUnit, unit, config.precision, displayScale.factor, locale)
    : unavailableStats(card.unavailable_reason)
  const unavailable = unavailablePresentation(card.unavailable_reason)

  return {
    key: card.key,
    titleId: config.titleId,
    helpId: config.helpId,
    stage,
    stageLabelId: clusterMonitorStageLabelIds[stage],
    statusId: card.available ? clusterMonitorStatusByTone[tone] : unavailable.statusId,
    tone,
    unit,
    value,
    available: card.available,
    error: undefined,
    series,
    stats,
    chartColor: config.chartColor,
  }
}

function mapClusterRealtimeSnapshot(entry: ApiSnapshotEntry, locale: string): ClusterMonitorSnapshotEntry | null {
  const labelId = clusterMonitorSnapshotLabelIds[entry.key]
  if (!labelId) return null

  return {
    key: entry.key,
    labelId,
    value: formatApiValue(entry, entry.unit === "%" ? 2 : entry.unit ? 1 : 0, locale),
    unit: entry.unit,
    tone: normalizeTone(entry.tone, "normal"),
  }
}

function mapClusterStats(
  stats: ApiStat[],
  rawCardUnit: string,
  displayCardUnit: string,
  precision: number,
  displayFactor: number,
  locale: string,
) {
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
        seriesKey: stat.series_key,
        value: formatApiStatValue(value, displayUnit, precision, locale),
      },
    ]
  })
}

function unavailableStats(reason: string | undefined) {
  const unavailable = unavailablePresentation(reason)
  return [
    {
      labelId: "clusterMonitor.stat.unavailableReason",
      value: "",
      valueId: unavailable.descriptionId,
    },
  ]
}

function unavailablePresentation(reason: string | undefined) {
  const noSamples = reason === "prometheus_no_data" || Boolean(reason?.startsWith("no_"))
  return noSamples
    ? {
      statusId: "clusterMonitor.status.noSamples",
      descriptionId: "clusterMonitor.unavailable.noSamples",
    }
    : {
      statusId: "clusterMonitor.status.queryFailed",
      descriptionId: "clusterMonitor.unavailable.queryFailed",
    }
}

function isKnownClusterCard(card: RealtimeMonitorCard) {
  return isClusterMonitorMetricKey(card.key)
}

function isClusterMonitorMetricKey(key: string): key is ClusterMonitorMetricKey {
  return key in clusterMonitorMetricConfig
}

function normalizeStage(stage: string, fallback: ClusterMonitorStage): ClusterMonitorStage {
  if (stage in clusterMonitorStageLabelIds) return stage as ClusterMonitorStage
  return fallback
}

function normalizeTone(tone: RealtimeMonitorTone | string | undefined, fallback: ClusterMonitorTone): ClusterMonitorTone {
  if (tone === "normal" || tone === "warning" || tone === "critical") return tone
  return fallback === "preview" ? "normal" : fallback
}

function formatApiValue(value: { value?: number; text?: string; unit?: string }, precision: number, locale: string) {
  if (value.text !== undefined && value.text !== "") return value.text
  if (typeof value.value === "number") return formatClusterNumber(value.value, precision, locale)
  return "-"
}

function formatApiStatValue(stat: ApiStat, unit: string, precision: number, locale: string) {
  if (stat.text !== undefined && stat.text !== "") return stat.text
  if (typeof stat.value !== "number") return "-"
  return appendClusterUnit(formatClusterNumber(stat.value, precision, locale), unit)
}

function formatClusterNumber(value: number, precision: number, locale: string) {
  return value.toLocaleString(locale, {
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

function clusterDisplayScale(card: RealtimeMonitorCard): ClusterDisplayScale {
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

function clusterCardSeries(card: RealtimeMonitorCard) {
  return Array.isArray(card.series) ? card.series : []
}

function clusterCardStats(card: RealtimeMonitorCard) {
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

function scaleClusterSeries(series: RealtimeMonitorCard["series"], factor: number) {
  return series.map((point) => ({
    timestamp: point.timestamp,
    value: factor === 1 ? point.value : point.value / factor,
    label: point.label,
    seriesKey: point.series_key,
  }))
}

function ClusterMonitorLoadingState() {
  const intl = useIntl()

  return (
    <section
      className="rounded-md border border-border/80 bg-card/82 px-4 py-4 text-sm text-muted-foreground"
      data-cluster-monitor-surface="loading"
      role="status"
    >
      {intl.formatMessage({ id: "clusterMonitor.prometheus.loading" })}
    </section>
  )
}

function ClusterMonitorSourceState({ kind }: { kind: "disabled" | "unavailable" }) {
  const intl = useIntl()
  const isDisabled = kind === "disabled"

  return (
    <section
      className="rounded-md border border-border/80 bg-card/88 px-5 py-6 text-sm text-muted-foreground"
      data-cluster-monitor-surface="source-state"
      role="status"
    >
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
