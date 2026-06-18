import { useEffect, useMemo, useState } from "react"
import { useIntl } from "react-intl"

import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
import { getClusterRealtimeMonitor } from "@/lib/manager-api"
import type {
  ClusterRealtimeMonitorCard,
  ClusterRealtimeMonitorResponse,
  ClusterRealtimeMonitorSnapshotEntry as ApiSnapshotEntry,
  ClusterRealtimeMonitorStat as ApiStat,
  ClusterRealtimeMonitorTone,
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
  const [isPaused, setIsPaused] = useState(false)
  const [state, setState] = useState<ClusterMonitorPageState>({ kind: "loading" })

  useEffect(() => {
    let cancelled = false
    setState({ kind: "loading" })

    getClusterRealtimeMonitor({ window: timeRange })
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
  }, [timeRange])

  const model = useMemo(() => {
    if (state.kind !== "ready" || !isRenderableClusterMonitor(state.response)) return null
    return buildClusterRealtimeMonitorModel(state.response, timeRange, isPaused)
  }, [isPaused, state, timeRange])
  const generatedAt = state.kind === "ready" ? state.response.generated_at : new Date().toISOString()
  const sourceError = state.kind === "ready" ? getSourceError(state.response) : undefined

  return (
    <PageContainer className="max-w-[1600px] gap-4">
      <PageHeader
        description={intl.formatMessage({ id: "clusterMonitor.description" })}
        eyebrow={intl.formatMessage({ id: "clusterMonitor.liveBadge" })}
        title={intl.formatMessage({ id: "clusterMonitor.title" })}
      />

      <ClusterMonitorToolbar
        generatedAt={model?.generatedAt ?? generatedAt}
        isPaused={model?.isPaused ?? isPaused}
        onPauseToggle={() => setIsPaused((current) => !current)}
        onTimeRangeChange={setTimeRange}
        scopeLabelId={model?.scopeLabelId ?? "clusterMonitor.scope.global"}
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
      {state.kind === "ready" && state.response.status === "partial" ? (
        <ClusterMonitorPartialWarning message={sourceError} />
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
  const includeUnavailable = response.status === "partial"
  const cards = response.cards.flatMap((card) => {
    const mapped = mapClusterRealtimeCard(card, includeUnavailable)
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

function mapClusterRealtimeCard(card: ClusterRealtimeMonitorCard, includeUnavailable: boolean): ClusterMonitorMetricCard | null {
  if (!isClusterMonitorMetricKey(card.key)) return null
  if (!card.available && !includeUnavailable) return null

  const config = clusterMonitorMetricConfig[card.key]
  const stage = normalizeStage(card.stage, config.stage)
  const tone = normalizeTone(card.tone, config.tone)
  const unit = card.unit ?? ""
  const value = card.available ? formatApiValue(card, config.precision) : "-"
  const stats = card.available ? mapClusterStats(card.stats, unit, config.precision) : unavailableStats(card.error)

  return {
    key: card.key,
    titleId: config.titleId,
    stage,
    stageLabelId: clusterMonitorStageLabelIds[stage],
    statusId: card.available ? clusterMonitorStatusByTone[tone] : "clusterMonitor.status.unavailable",
    tone,
    unit,
    value,
    available: card.available,
    error: card.error,
    series: card.available ? card.series : [],
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

function mapClusterStats(stats: ApiStat[], cardUnit: string, precision: number) {
  return stats.flatMap((stat) => {
    const labelId = clusterMonitorStatLabelIds[stat.key]
    if (!labelId) return []

    return [
      {
        labelId,
        value: formatApiStatValue(stat, stat.unit ?? cardUnit, precision),
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

function ClusterMonitorPartialWarning({ message }: { message?: string }) {
  const intl = useIntl()

  return (
    <section className="rounded-lg border border-warning/30 bg-warning/8 px-4 py-3 text-sm text-warning" role="status">
      <p className="font-medium">{intl.formatMessage({ id: "clusterMonitor.prometheus.partialTitle" })}</p>
      {message ? <p className="mt-1 text-xs opacity-90">{message}</p> : null}
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
