import { useCallback, useEffect, useMemo, useState } from "react"
import { useIntl } from "react-intl"

import { ResourceState } from "@/components/manager/resource-state"
import { Button } from "@/components/ui/button"
import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
import { ManagerApiError, getRuntimeWorkqueues } from "@/lib/manager-api"
import type {
  ManagerRuntimeWorkqueueItem,
  ManagerRuntimeWorkqueuesResponse,
} from "@/lib/manager-api.types"

type WindowValue = "10s" | "30s" | "1m"

type WorkqueuesState = {
  response: ManagerRuntimeWorkqueuesResponse | null
  loading: boolean
  refreshing: boolean
  error: Error | null
}

const windowOptions: WindowValue[] = ["10s", "30s", "1m"]
const abnormalLevels = new Set(["busy", "degraded", "critical"])

function emptyState(): WorkqueuesState {
  return {
    response: null,
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
  if (error.status === 503) {
    return "unavailable" as const
  }
  return "error" as const
}

function isAbnormal(item: ManagerRuntimeWorkqueueItem) {
  return abnormalLevels.has(item.level)
}

function formatDepth(item: ManagerRuntimeWorkqueueItem) {
  return `${item.depth} / ${item.capacity}`
}

function formatMs(value: number) {
  return value > 0 ? `${value.toFixed(1)} ms` : "-"
}

function formatRate(value: number) {
  return value > 0 ? `${value.toFixed(2)}/s` : "-"
}

function formatScore(value: number) {
  return value.toFixed(2)
}

function formatHottest(response: ManagerRuntimeWorkqueuesResponse | null) {
  const hottest = response?.summary.hottest
  if (!hottest) {
    return "-"
  }
  return `${hottest.component} / ${hottest.pool}`
}

function levelClassName(level: string) {
  if (level === "critical") {
    return "border-red-500/40 bg-red-500/10 text-red-500"
  }
  if (level === "degraded") {
    return "border-amber-500/40 bg-amber-500/10 text-amber-500"
  }
  if (level === "busy") {
    return "border-sky-500/40 bg-sky-500/10 text-sky-500"
  }
  return "border-emerald-500/35 bg-emerald-500/10 text-emerald-500"
}

function LevelPill({ level }: { level: string }) {
  return (
    <span className={`inline-flex rounded-md border px-2 py-0.5 text-xs font-medium ${levelClassName(level)}`}>
      {level}
    </span>
  )
}

export function WorkqueuesPage() {
  const intl = useIntl()
  const [windowValue, setWindowValue] = useState<WindowValue>("10s")
  const [autoRefresh, setAutoRefresh] = useState(false)
  const [abnormalOnly, setAbnormalOnly] = useState(false)
  const [component, setComponent] = useState("")
  const [state, setState] = useState<WorkqueuesState>(emptyState)

  const title = intl.formatMessage({ id: "workqueues.title" })

  const loadWorkqueues = useCallback(async (refreshing = false, windowOverride?: WindowValue) => {
    const nextWindow = windowOverride ?? windowValue
    setState((current) => ({
      ...current,
      loading: !refreshing,
      refreshing,
      error: null,
    }))
    try {
      const response = await getRuntimeWorkqueues({ window: nextWindow, limit: 100 })
      setState({ response, loading: false, refreshing: false, error: null })
    } catch (error) {
      setState({
        response: null,
        loading: false,
        refreshing: false,
        error: error instanceof Error ? error : new Error("runtime workqueue request failed"),
      })
    }
  }, [windowValue])

  useEffect(() => {
    void loadWorkqueues(false, "10s")
    // Initial load intentionally uses the default window.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  useEffect(() => {
    if (!autoRefresh) {
      return
    }
    const timer = window.setInterval(() => {
      void loadWorkqueues(true)
    }, 5000)
    return () => window.clearInterval(timer)
  }, [autoRefresh, loadWorkqueues])

  const response = state.response
  const allItems = response?.items ?? []
  const components = useMemo(() => (
    Array.from(new Set(allItems.map((item) => item.component))).sort()
  ), [allItems])

  const filteredItems = useMemo(() => allItems.filter((item) => {
    if (component && item.component !== component) {
      return false
    }
    if (abnormalOnly && !isAbnormal(item)) {
      return false
    }
    return true
  }), [abnormalOnly, allItems, component])

  const abnormalCount = (response?.summary.degraded ?? 0) + (response?.summary.critical ?? 0)
  const sampleCount = response?.sources.collector.sample_count ?? 0
  const nodeLabel = response ? `${response.scope.node_name || response.scope.node_id} (#${response.scope.node_id})` : "-"
  const emptyDescription = allItems.length > 0
    ? intl.formatMessage({ id: "workqueues.filteredEmpty" })
    : intl.formatMessage({ id: "workqueues.empty" })

  if (state.loading) {
    return (
      <PageContainer>
        <div className="rounded-lg border border-border bg-card px-4 py-3 text-sm text-muted-foreground">
          {intl.formatMessage({ id: "common.loading" })}
        </div>
      </PageContainer>
    )
  }

  if (state.error) {
    return (
      <PageContainer>
        <PageHeader
          description={intl.formatMessage({ id: "workqueues.description" })}
          title={title}
        />
        <ResourceState
          kind={mapErrorKind(state.error)}
          onRetry={() => void loadWorkqueues(false)}
          title={title}
        />
      </PageContainer>
    )
  }

  return (
    <PageContainer>
      <PageHeader
        actions={(
          <Button disabled={state.refreshing} onClick={() => void loadWorkqueues(true)} size="sm" type="button" variant="outline">
            {state.refreshing ? intl.formatMessage({ id: "common.refreshing" }) : intl.formatMessage({ id: "common.refresh" })}
          </Button>
        )}
        description={intl.formatMessage({ id: "workqueues.description" })}
        eyebrow={intl.formatMessage({ id: "nav.group.globalCluster" })}
        title={title}
      />

      <div className="space-y-4">
        <div className="flex flex-col gap-3 rounded-lg border border-border bg-card px-4 py-3 md:flex-row md:items-end md:justify-between">
          <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
            <label className="text-sm font-medium text-foreground">
              {intl.formatMessage({ id: "workqueues.controls.window" })}
              <select
                aria-label={intl.formatMessage({ id: "workqueues.controls.window" })}
                className="mt-1 h-8 w-full rounded-md border border-border bg-background px-2 text-sm"
                onChange={(event) => setWindowValue(event.target.value as WindowValue)}
                value={windowValue}
              >
                {windowOptions.map((option) => <option key={option} value={option}>{option}</option>)}
              </select>
            </label>
            <label className="text-sm font-medium text-foreground">
              {intl.formatMessage({ id: "workqueues.controls.component" })}
              <select
                aria-label={intl.formatMessage({ id: "workqueues.controls.component" })}
                className="mt-1 h-8 w-full rounded-md border border-border bg-background px-2 text-sm"
                onChange={(event) => setComponent(event.target.value)}
                value={component}
              >
                <option value="">{intl.formatMessage({ id: "workqueues.controls.allComponents" })}</option>
                {components.map((option) => <option key={option} value={option}>{`${option} component`}</option>)}
              </select>
            </label>
            <label className="flex items-center gap-2 self-end text-sm font-medium text-foreground">
              <input
                checked={autoRefresh}
                className="size-4 rounded border-border"
                onChange={(event) => setAutoRefresh(event.target.checked)}
                type="checkbox"
              />
              {intl.formatMessage({ id: "workqueues.controls.autoRefresh" })}
            </label>
            <label className="flex items-center gap-2 self-end text-sm font-medium text-foreground">
              <input
                checked={abnormalOnly}
                className="size-4 rounded border-border"
                onChange={(event) => setAbnormalOnly(event.target.checked)}
                type="checkbox"
              />
              {intl.formatMessage({ id: "workqueues.controls.abnormalOnly" })}
            </label>
          </div>
          <div className="text-xs text-muted-foreground">
            {intl.formatMessage({ id: "workqueues.scope.node" })}: {nodeLabel}
            <span className="mx-2">/</span>
            {intl.formatMessage({ id: "workqueues.scope.samples" })}: {sampleCount}
          </div>
        </div>

        {response ? (
          <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-5">
            <div className="rounded-lg border border-border bg-card px-4 py-3">
              <div className="text-xs font-medium text-muted-foreground">{intl.formatMessage({ id: "workqueues.summary.level" })}</div>
              <div className="mt-2 text-sm font-semibold text-foreground">
                {intl.formatMessage({ id: "workqueues.summary.overallValue" }, { level: response.summary.overall_level })}
              </div>
            </div>
            <div className="rounded-lg border border-border bg-card px-4 py-3">
              <div className="text-xs font-medium text-muted-foreground">{intl.formatMessage({ id: "workqueues.summary.total" })}</div>
              <div className="mt-2 text-2xl font-semibold text-foreground">{response.summary.total}</div>
            </div>
            <div className="rounded-lg border border-border bg-card px-4 py-3">
              <div className="text-xs font-medium text-muted-foreground">{intl.formatMessage({ id: "workqueues.summary.degraded" })}</div>
              <div className="mt-2 text-2xl font-semibold text-foreground">{abnormalCount}</div>
            </div>
            <div className="rounded-lg border border-border bg-card px-4 py-3">
              <div className="text-xs font-medium text-muted-foreground">{intl.formatMessage({ id: "workqueues.summary.hottest" })}</div>
              <div className="mt-2 truncate text-sm font-semibold text-foreground">{formatHottest(response)}</div>
            </div>
            <div className="rounded-lg border border-border bg-card px-4 py-3">
              <div className="text-xs font-medium text-muted-foreground">{intl.formatMessage({ id: "workqueues.summary.window" })}</div>
              <div className="mt-2 text-2xl font-semibold text-foreground">{response.window_seconds}s</div>
            </div>
          </div>
        ) : null}

        <section className="rounded-lg border border-border bg-card">
          {filteredItems.length === 0 ? (
            <div className="p-4">
              <ResourceState kind="empty" title={title} description={emptyDescription} />
            </div>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full min-w-[1180px] border-collapse text-left">
                <thead className="text-xs uppercase text-muted-foreground">
                  <tr>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "workqueues.table.level" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "workqueues.table.component" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "workqueues.table.pool" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "workqueues.table.queue" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "workqueues.table.depth" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "workqueues.table.inflight" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "workqueues.table.score" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "workqueues.table.wait" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "workqueues.table.task" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "workqueues.table.admission" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "workqueues.table.hint" })}</th>
                  </tr>
                </thead>
                <tbody>
                  {filteredItems.map((item) => (
                    <tr className="border-t border-border" key={`${item.component}-${item.pool}-${item.queue}-${item.priority}`}>
                      <td className="px-3 py-3"><LevelPill level={item.level} /></td>
                      <td className="px-3 py-3 text-sm font-medium text-foreground">{item.component}</td>
                      <td className="px-3 py-3 text-sm text-foreground">{item.pool}</td>
                      <td className="px-3 py-3 text-sm text-muted-foreground">{item.queue}</td>
                      <td className="px-3 py-3 text-sm text-muted-foreground">{formatDepth(item)}</td>
                      <td className="px-3 py-3 text-sm text-muted-foreground">{item.inflight}</td>
                      <td className="px-3 py-3 text-sm text-muted-foreground">{formatScore(item.score)}</td>
                      <td className="px-3 py-3 text-sm text-muted-foreground">{formatMs(item.wait_p99_ms)}</td>
                      <td className="px-3 py-3 text-sm text-muted-foreground">{formatMs(item.task_p99_ms)}</td>
                      <td className="px-3 py-3 text-sm text-muted-foreground">{formatRate(item.admission_error_per_sec)}</td>
                      <td className="max-w-[260px] px-3 py-3 text-sm text-muted-foreground">
                        <span className="block truncate">{item.hint || "-"}</span>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </section>
      </div>
    </PageContainer>
  )
}
