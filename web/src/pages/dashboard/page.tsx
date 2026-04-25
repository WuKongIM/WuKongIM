import { useCallback, useEffect, useMemo, useState } from "react"
import { useIntl, type IntlShape } from "react-intl"

import { ResourceState } from "@/components/manager/resource-state"
import { StatusBadge } from "@/components/manager/status-badge"
import { Button } from "@/components/ui/button"
import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
import { SectionCard } from "@/components/shell/section-card"
import { ManagerApiError, getOverview, getTasks } from "@/lib/manager-api"
import type {
  ManagerOverviewResponse,
  ManagerTask,
  ManagerTasksResponse,
} from "@/lib/manager-api.types"

type DashboardState = {
  overview: ManagerOverviewResponse | null
  tasks: ManagerTasksResponse | null
  loading: boolean
  refreshing: boolean
  error: Error | null
}

function formatTimestamp(intl: IntlShape, value: string) {
  return new Intl.DateTimeFormat(intl.locale, {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  }).format(new Date(value))
}

function formatErrorKind(error: Error | null) {
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

function buildAlertItems(intl: IntlShape, overview: ManagerOverviewResponse) {
  return [
    ...overview.anomalies.slots.quorum_lost.items.map((item) => ({
      key: `slot-quorum-${item.slot_id}`,
      title: intl.formatMessage({ id: "dashboard.slotValue" }, { id: item.slot_id }),
      detail: `${item.quorum} / ${item.sync}`,
      tone: item.quorum,
    })),
    ...overview.anomalies.tasks.retrying.items.map((item) => ({
      key: `task-retrying-${item.slot_id}`,
      title: intl.formatMessage({ id: "dashboard.slotValue" }, { id: item.slot_id }),
      detail: `${item.kind} ${item.status}`,
      tone: item.status,
    })),
  ]
}

function renderTaskRow(intl: IntlShape, task: ManagerTask) {
  return (
    <tr className="border-t border-border" key={`${task.slot_id}-${task.kind}-${task.step}`}>
      <td className="px-3 py-3 text-sm font-medium text-foreground">
        {intl.formatMessage({ id: "dashboard.slotValue" }, { id: task.slot_id })}
      </td>
      <td className="px-3 py-3 text-sm text-foreground">{task.kind}</td>
      <td className="px-3 py-3 text-sm text-foreground">
        <StatusBadge value={task.status} />
      </td>
      <td className="px-3 py-3 text-sm text-muted-foreground">{task.attempt}</td>
      <td className="px-3 py-3 text-sm text-muted-foreground">{task.last_error || "-"}</td>
    </tr>
  )
}

export function DashboardPage() {
  const intl = useIntl()
  const [state, setState] = useState<DashboardState>({
    overview: null,
    tasks: null,
    loading: true,
    refreshing: false,
    error: null,
  })

  const loadDashboard = useCallback(async (refreshing: boolean) => {
    setState((current) => ({
      ...current,
      loading: refreshing ? current.loading : true,
      refreshing,
      error: null,
    }))

    try {
      const [overview, tasks] = await Promise.all([getOverview(), getTasks()])
      setState({ overview, tasks, loading: false, refreshing: false, error: null })
    } catch (error) {
      setState({
        overview: null,
        tasks: null,
        loading: false,
        refreshing: false,
        error: error instanceof Error ? error : new Error("dashboard request failed"),
      })
    }
  }, [])

  useEffect(() => {
    void loadDashboard(false)
  }, [loadDashboard])

  const alertItems = useMemo(
    () => (state.overview ? buildAlertItems(intl, state.overview) : []),
    [intl, state.overview],
  )

  return (
    <PageContainer>
      <PageHeader
        title={intl.formatMessage({ id: "dashboard.title" })}
        description={intl.formatMessage({ id: "dashboard.description" })}
        actions={
          <>
            <Button
              onClick={() => {
                void loadDashboard(true)
              }}
              size="sm"
              variant="outline"
            >
              {state.refreshing
                ? intl.formatMessage({ id: "common.refreshing" })
                : intl.formatMessage({ id: "common.refresh" })}
            </Button>
            <Button disabled size="sm">
              {intl.formatMessage({ id: "common.export" })}
            </Button>
          </>
        }
      >
        <div className="flex flex-wrap gap-2 text-xs text-muted-foreground">
          <div className="rounded-md border border-border bg-background px-3 py-2">
            {intl.formatMessage({ id: "dashboard.scopeSingleNodeCluster" })}
          </div>
          <div className="rounded-md border border-border bg-background px-3 py-2">
            {state.overview
              ? intl.formatMessage(
                  { id: "dashboard.generatedAtValue" },
                  { value: formatTimestamp(intl, state.overview.generated_at) },
                )
              : intl.formatMessage({ id: "dashboard.generatedAtPending" })}
          </div>
          <div className="rounded-md border border-border bg-background px-3 py-2">
            {state.overview
              ? intl.formatMessage(
                  { id: "dashboard.controllerLeaderValue" },
                  { id: state.overview.cluster.controller_leader_id },
                )
              : intl.formatMessage({ id: "dashboard.controllerLeaderPending" })}
          </div>
        </div>
      </PageHeader>

      {state.loading ? (
        <ResourceState kind="loading" title={intl.formatMessage({ id: "dashboard.title" })} />
      ) : null}
      {!state.loading && state.error ? (
        <ResourceState
          kind={formatErrorKind(state.error)}
          onRetry={() => {
            void loadDashboard(false)
          }}
          title={intl.formatMessage({ id: "dashboard.title" })}
        />
      ) : null}
      {!state.loading && !state.error && state.overview && state.tasks ? (
        <>
          <section className="grid gap-3 md:grid-cols-2 xl:grid-cols-4">
            <SectionCard
              description={intl.formatMessage({ id: "dashboard.controllerLeaderCardDescription" })}
              title={intl.formatMessage({ id: "dashboard.controllerLeaderCardTitle" })}
            >
              <div className="text-3xl font-semibold text-foreground">
                {state.overview.cluster.controller_leader_id}
              </div>
            </SectionCard>
            <SectionCard
              description={intl.formatMessage({ id: "dashboard.nodesCardDescription" })}
              title={intl.formatMessage({ id: "dashboard.nodesCardTitle" })}
            >
              <div className="text-3xl font-semibold text-foreground">{state.overview.nodes.total}</div>
            </SectionCard>
            <SectionCard
              description={intl.formatMessage({ id: "dashboard.readySlotsCardDescription" })}
              title={intl.formatMessage({ id: "dashboard.readySlotsCardTitle" })}
            >
              <div className="text-3xl font-semibold text-foreground">{state.overview.slots.ready}</div>
            </SectionCard>
            <SectionCard
              description={intl.formatMessage({ id: "dashboard.tasksCardDescription" })}
              title={intl.formatMessage({ id: "dashboard.tasksCardTitle" })}
            >
              <div className="text-3xl font-semibold text-foreground">{state.overview.tasks.total}</div>
            </SectionCard>
          </section>

          <section className="grid gap-4 xl:grid-cols-[1.15fr_0.85fr]">
            <SectionCard
              description={intl.formatMessage({ id: "dashboard.operationsSummaryDescription" })}
              title={intl.formatMessage({ id: "dashboard.operationsSummaryTitle" })}
            >
              <div className="grid gap-3 md:grid-cols-2">
                <div className="rounded-lg border border-border bg-muted/30 px-3 py-3">
                  <div className="text-xs uppercase tracking-[0.16em] text-muted-foreground">
                    {intl.formatMessage({ id: "dashboard.nodesLabel" })}
                  </div>
                  <div className="mt-2 text-sm text-foreground">
                    {intl.formatMessage(
                      { id: "dashboard.nodesSummary" },
                      { alive: state.overview.nodes.alive, draining: state.overview.nodes.draining },
                    )}
                  </div>
                </div>
                <div className="rounded-lg border border-border bg-muted/30 px-3 py-3">
                  <div className="text-xs uppercase tracking-[0.16em] text-muted-foreground">
                    {intl.formatMessage({ id: "dashboard.slotsLabel" })}
                  </div>
                  <div className="mt-2 text-sm text-foreground">
                    {intl.formatMessage(
                      { id: "dashboard.slotsSummary" },
                      {
                        ready: state.overview.slots.ready,
                        quorumLost: state.overview.slots.quorum_lost,
                      },
                    )}
                  </div>
                </div>
                <div className="rounded-lg border border-border bg-muted/30 px-3 py-3">
                  <div className="text-xs uppercase tracking-[0.16em] text-muted-foreground">
                    {intl.formatMessage({ id: "dashboard.tasksLabel" })}
                  </div>
                  <div className="mt-2 text-sm text-foreground">
                    {intl.formatMessage(
                      { id: "dashboard.tasksSummary" },
                      {
                        pending: state.overview.tasks.pending,
                        retrying: state.overview.tasks.retrying,
                      },
                    )}
                  </div>
                </div>
                <div className="rounded-lg border border-border bg-muted/30 px-3 py-3">
                  <div className="text-xs uppercase tracking-[0.16em] text-muted-foreground">
                    {intl.formatMessage({ id: "dashboard.generatedLabel" })}
                  </div>
                  <div className="mt-2 text-sm text-foreground">
                    {formatTimestamp(intl, state.overview.generated_at)}
                  </div>
                </div>
              </div>
            </SectionCard>
            <SectionCard
              description={intl.formatMessage({ id: "dashboard.alertListDescription" })}
              title={intl.formatMessage({ id: "dashboard.alertListTitle" })}
            >
              {alertItems.length > 0 ? (
                <div className="space-y-3">
                  {alertItems.map((item) => (
                    <div className="rounded-lg border border-border bg-muted/20 px-3 py-3" key={item.key}>
                      <div className="flex items-center justify-between gap-3">
                        <div className="text-sm font-medium text-foreground">{item.title}</div>
                        <StatusBadge value={item.tone} />
                      </div>
                      <div className="mt-2 text-sm text-muted-foreground">{item.detail}</div>
                    </div>
                  ))}
                </div>
              ) : (
                <ResourceState kind="empty" title={intl.formatMessage({ id: "dashboard.alertListTitle" })} />
              )}
            </SectionCard>
          </section>

          <section>
            <SectionCard
              description={intl.formatMessage({ id: "dashboard.controlQueueDescription" })}
              title={intl.formatMessage({ id: "dashboard.controlQueueTitle" })}
            >
              {state.tasks.items.length > 0 ? (
                <div className="overflow-x-auto rounded-lg border border-border">
                  <table className="w-full border-collapse">
                    <thead className="bg-muted/40 text-left text-xs uppercase tracking-[0.14em] text-muted-foreground">
                      <tr>
                        <th className="px-3 py-3">{intl.formatMessage({ id: "dashboard.table.slot" })}</th>
                        <th className="px-3 py-3">{intl.formatMessage({ id: "dashboard.table.kind" })}</th>
                        <th className="px-3 py-3">{intl.formatMessage({ id: "dashboard.table.status" })}</th>
                        <th className="px-3 py-3">{intl.formatMessage({ id: "dashboard.table.attempt" })}</th>
                        <th className="px-3 py-3">{intl.formatMessage({ id: "dashboard.table.lastError" })}</th>
                      </tr>
                    </thead>
                    <tbody>{state.tasks.items.map((task) => renderTaskRow(intl, task))}</tbody>
                  </table>
                </div>
              ) : (
                <ResourceState kind="empty" title={intl.formatMessage({ id: "dashboard.controlQueueTitle" })} />
              )}
            </SectionCard>
          </section>
        </>
      ) : null}
    </PageContainer>
  )
}
