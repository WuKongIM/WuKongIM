import { useEffect, useMemo, useState } from "react"
import { useIntl } from "react-intl"

import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
import { getRealtimeMonitor } from "@/lib/manager-api"
import type {
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
  monitorNoDataLabelIds,
  monitorSnapshotLabelIds,
  monitorStageLabelIds,
  monitorStatLabelIds,
  monitorStatusByTone,
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
  const [isPaused, setIsPaused] = useState(false)
  const [state, setState] = useState<MonitorPageState>({ kind: "loading" })

  useEffect(() => {
    let cancelled = false
    setState({ kind: "loading" })

    getRealtimeMonitor({ window: timeRange })
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
    if (state.kind !== "ready" || !isRenderableMonitor(state.response)) return null
    return buildRealtimeMonitorModel(state.response, timeRange, isPaused)
  }, [isPaused, state, timeRange])
  const generatedAt = state.kind === "ready" ? state.response.generated_at : new Date().toISOString()
  const sourceError = state.kind === "ready" ? state.response.sources.prometheus.error : undefined

  return (
    <PageContainer className="max-w-[1600px] gap-4">
      <PageHeader
        description={intl.formatMessage({ id: "monitor.cardWallDescription" })}
        eyebrow={intl.formatMessage({ id: "monitor.liveBadge" })}
        title={intl.formatMessage({ id: "monitor.title" })}
      />

      <MonitorToolbar
        generatedAt={model?.generatedAt ?? generatedAt}
        isPaused={isPaused}
        onPauseToggle={() => setIsPaused((current) => !current)}
        onTimeRangeChange={setTimeRange}
        scopeLabelId={model?.scopeLabelId ?? "monitor.scope.global"}
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
      {state.kind === "ready" && state.response.status === "partial" ? <MonitorPartialWarning message={sourceError} /> : null}
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
  return (response.status === "ready" || response.status === "partial") && response.cards.some((card) => isMonitorMetricKey(card.key))
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
  const isAvailable = card.available !== false

  return {
    key: card.key,
    titleId: config.titleId,
    stage,
    stageLabelId: monitorStageLabelIds[stage],
    statusId: monitorStatusByTone[tone],
    tone,
    unit: isAvailable ? card.unit : "",
    value: isAvailable ? formatMonitorNumber(card.value, config.precision) : "-",
    series: isAvailable ? card.series : [],
    stats: isAvailable
      ? card.stats.map((stat) => ({
          labelId: monitorStatLabelIds[stat.key] ?? "monitor.stat.avg",
          value:
            stat.key === "total"
              ? formatMonitorNumber(stat.value, 0)
              : appendMonitorUnit(formatMonitorNumber(stat.value, config.precision), card.unit),
        }))
      : [],
    chartColor: config.chartColor,
    available: isAvailable,
    noDataLabelId: card.unavailable_reason ? monitorNoDataLabelIds[card.unavailable_reason] : undefined,
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

function MonitorPartialWarning({ message }: { message?: string }) {
  const intl = useIntl()

  return (
    <section className="rounded-lg border border-warning/30 bg-warning/8 px-4 py-3 text-sm text-warning" role="status">
      <p className="font-medium">{intl.formatMessage({ id: "monitor.prometheus.partialTitle" })}</p>
      {message ? <p className="mt-1 text-xs opacity-90">{message}</p> : null}
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
