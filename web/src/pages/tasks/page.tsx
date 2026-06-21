import { type ReactNode, useCallback, useEffect, useMemo, useState } from "react"
import { type IntlShape, useIntl } from "react-intl"

import { DetailSheet } from "@/components/manager/detail-sheet"
import { KeyValueList } from "@/components/manager/key-value-list"
import { ResourceState } from "@/components/manager/resource-state"
import { StatusBadge } from "@/components/manager/status-badge"
import { Button } from "@/components/ui/button"
import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
import { SectionCard } from "@/components/shell/section-card"
import {
  ManagerApiError,
  getDistributedTask,
  getDistributedTasks,
  getDistributedTasksSummary,
} from "@/lib/manager-api"
import type {
  DistributedTaskListParams,
  ManagerDistributedTask,
  ManagerDistributedTaskDetailResponse,
  ManagerDistributedTasksResponse,
  ManagerDistributedTasksSummaryResponse,
} from "@/lib/manager-api.types"

type TasksState = {
  summary: ManagerDistributedTasksSummaryResponse | null
  tasks: ManagerDistributedTasksResponse | null
  loading: boolean
  refreshing: boolean
  error: Error | null
}

const domainOptions = ["slot_reconcile", "node_onboarding", "node_scale_in", "channel_migration"] as const
const statusOptions = ["pending", "running", "retrying", "blocked", "failed", "completed", "cancelled", "unknown"] as const
const scopeOptions = ["slot", "node", "channel", "job"] as const

function emptyTasksState(): TasksState {
  return {
    summary: null,
    tasks: null,
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

function formatTaskScope(task: ManagerDistributedTask) {
  if (task.scope.type === "slot") {
    return `Slot ${task.scope.slot_id}`
  }
  if (task.scope.type === "node") {
    return `Node ${task.scope.node_id}`
  }
  if (task.scope.type === "channel") {
    return `${task.scope.channel_type}/${task.scope.channel_id}`
  }
  return task.scope.id || "-"
}

function formatNodePair(task: ManagerDistributedTask) {
  if (!task.source_node && !task.target_node) {
    return "-"
  }
  return `${task.source_node || "-"} -> ${task.target_node || "-"}`
}

function formatTaskTime(task: ManagerDistributedTask) {
  return task.next_run_at ?? task.updated_at ?? task.created_at ?? "-"
}

function formatCount(value: number | undefined) {
  return String(value ?? 0)
}

function formatNodeList(nodes?: number[] | null) {
  return nodes && nodes.length > 0 ? nodes.join(", ") : "-"
}

function formatBoolean(intl: IntlShape, value?: boolean | null) {
  if (typeof value !== "boolean") {
    return "-"
  }
  return intl.formatMessage({ id: value ? "tasks.boolean.yes" : "tasks.boolean.no" })
}

function DetailSection({ children, title }: { children: ReactNode; title: string }) {
  return (
    <section className="space-y-3 rounded-lg border border-border bg-muted/20 p-3">
      <h3 className="text-xs font-medium uppercase tracking-[0.16em] text-muted-foreground">{title}</h3>
      {children}
    </section>
  )
}

function SourceSpecificDetail({ detail, intl }: { detail: ManagerDistributedTaskDetailResponse; intl: IntlShape }) {
  const source = detail.detail

  if (source.slot) {
    return (
      <DetailSection title={intl.formatMessage({ id: "tasks.detail.slotContext" })}>
        <KeyValueList
          items={[
            { label: intl.formatMessage({ id: "tasks.detail.quorum" }), value: <StatusBadge value={source.slot.slot.state.quorum} /> },
            { label: intl.formatMessage({ id: "tasks.detail.sync" }), value: <StatusBadge value={source.slot.slot.state.sync} /> },
            { label: intl.formatMessage({ id: "tasks.detail.leader" }), value: source.slot.slot.runtime.preferred_leader_id },
            { label: intl.formatMessage({ id: "tasks.detail.desiredPeers" }), value: formatNodeList(source.slot.slot.assignment.desired_peers) },
            { label: intl.formatMessage({ id: "tasks.detail.currentPeers" }), value: formatNodeList(source.slot.slot.runtime.current_peers) },
            { label: intl.formatMessage({ id: "tasks.detail.observedEpoch" }), value: source.slot.slot.runtime.observed_config_epoch },
          ]}
        />
      </DetailSection>
    )
  }

  if (source.node_onboarding) {
    const counts = source.node_onboarding.result_counts
    return (
      <DetailSection title={intl.formatMessage({ id: "tasks.detail.nodeOnboardingContext" })}>
        <KeyValueList
          items={[
            { label: intl.formatMessage({ id: "tasks.detail.targetNode" }), value: source.node_onboarding.target_node_id },
            { label: intl.formatMessage({ id: "tasks.detail.planVersion" }), value: source.node_onboarding.plan_version },
            { label: intl.formatMessage({ id: "tasks.detail.currentMove" }), value: source.node_onboarding.current_move_index },
            {
              label: intl.formatMessage({ id: "tasks.detail.resultCounts" }),
              value: `running ${counts.running}, completed ${counts.completed}, failed ${counts.failed}`,
            },
          ]}
        />
      </DetailSection>
    )
  }

  if (source.node_scale_in) {
    const progress = source.node_scale_in.progress
    return (
      <DetailSection title={intl.formatMessage({ id: "tasks.detail.nodeScaleInContext" })}>
        <KeyValueList
          items={[
            { label: intl.formatMessage({ id: "tasks.detail.targetNode" }), value: source.node_scale_in.node_id },
            { label: intl.formatMessage({ id: "tasks.detail.safeToRemove" }), value: formatBoolean(intl, source.node_scale_in.safe_to_remove) },
            { label: intl.formatMessage({ id: "tasks.detail.slotLeaders" }), value: progress.slot_leaders },
            { label: intl.formatMessage({ id: "tasks.detail.activeTasks" }), value: progress.active_tasks_involving_node },
            {
              label: intl.formatMessage({ id: "tasks.detail.channelMigrations" }),
              value: progress.active_channel_migrations_involving_node ?? progress.active_migrations_involving_node,
            },
            { label: intl.formatMessage({ id: "tasks.detail.activeConnections" }), value: progress.active_connections },
          ]}
        />
      </DetailSection>
    )
  }

  if (source.channel_migration) {
    return (
      <DetailSection title={intl.formatMessage({ id: "tasks.detail.channelMigrationContext" })}>
        <pre className="max-h-80 overflow-auto rounded-md bg-background p-3 text-xs text-muted-foreground">
          {JSON.stringify(source.channel_migration, null, 2)}
        </pre>
      </DetailSection>
    )
  }

  return null
}

export function TasksPage() {
  const intl = useIntl()
  const [state, setState] = useState<TasksState>(emptyTasksState)
  const [domain, setDomain] = useState("")
  const [status, setStatus] = useState("")
  const [scope, setScope] = useState("")
  const [nodeIdText, setNodeIdText] = useState("")
  const [keyword, setKeyword] = useState("")
  const [detailTask, setDetailTask] = useState<ManagerDistributedTask | null>(null)
  const [detail, setDetail] = useState<ManagerDistributedTaskDetailResponse | null>(null)
  const [detailLoading, setDetailLoading] = useState(false)
  const [detailError, setDetailError] = useState("")

  const buildQuery = useCallback((): DistributedTaskListParams => {
    const nodeId = Number(nodeIdText)
    return {
      domain: domain ? (domain as DistributedTaskListParams["domain"]) : undefined,
      status: status ? (status as DistributedTaskListParams["status"]) : undefined,
      nodeId: Number.isFinite(nodeId) && nodeId > 0 ? nodeId : undefined,
      scope: scope ? (scope as DistributedTaskListParams["scope"]) : undefined,
      keyword: keyword.trim() || undefined,
      limit: 50,
    }
  }, [domain, keyword, nodeIdText, scope, status])

  const loadTasks = useCallback(async (refreshing = false) => {
    setState((current) => ({
      ...current,
      loading: !refreshing,
      refreshing,
      error: null,
    }))
    try {
      const [summary, tasks] = await Promise.all([
        getDistributedTasksSummary(),
        getDistributedTasks(buildQuery()),
      ])
      setState({ summary, tasks, loading: false, refreshing: false, error: null })
    } catch (error) {
      setState({
        summary: null,
        tasks: null,
        loading: false,
        refreshing: false,
        error: error instanceof Error ? error : new Error("distributed task request failed"),
      })
    }
  }, [buildQuery])

  useEffect(() => {
    void loadTasks(false)
    // Initial load intentionally uses default filters only.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const openDetail = useCallback(async (task: ManagerDistributedTask) => {
    setDetailTask(task)
    setDetail(null)
    setDetailError("")
    setDetailLoading(true)
    try {
      const response = await getDistributedTask(task.domain, task.id)
      setDetail(response)
    } catch (error) {
      setDetailError(error instanceof Error ? error.message : "task detail request failed")
    } finally {
      setDetailLoading(false)
    }
  }, [])

  const warnings = state.tasks?.warnings ?? []
  const summary = state.summary
  const tasks = state.tasks?.items ?? []

  const summaryCards = useMemo(() => [
    { label: intl.formatMessage({ id: "tasks.summary.total" }), value: summary?.total },
    { label: intl.formatMessage({ id: "tasks.summary.running" }), value: summary?.by_status.running },
    { label: intl.formatMessage({ id: "tasks.summary.retrying" }), value: summary?.by_status.retrying },
    { label: intl.formatMessage({ id: "tasks.summary.failed" }), value: summary?.by_status.failed },
    { label: intl.formatMessage({ id: "tasks.summary.blocked" }), value: summary?.by_status.blocked },
  ], [intl, summary])

  return (
    <PageContainer>
      <PageHeader
        actions={<span className="rounded-md border border-border bg-muted px-2 py-1 text-xs font-medium text-muted-foreground">{intl.formatMessage({ id: "tasks.readOnly" })}</span>}
        description={intl.formatMessage({ id: "tasks.description" })}
        eyebrow={intl.formatMessage({ id: "nav.group.globalCluster" })}
        title={intl.formatMessage({ id: "tasks.title" })}
      />

      {state.loading ? (
        <ResourceState kind="loading" title={intl.formatMessage({ id: "tasks.title" })} />
      ) : state.error ? (
        <ResourceState
          kind={mapErrorKind(state.error)}
          onRetry={() => void loadTasks(false)}
          title={intl.formatMessage({ id: "tasks.title" })}
        />
      ) : (
        <>
          <div className="grid gap-3 md:grid-cols-5">
            {summaryCards.map((card) => (
              <div className="rounded-lg border border-border bg-card px-4 py-3" key={card.label}>
                <div className="text-xs font-medium text-muted-foreground">{card.label}</div>
                <div className="mt-2 text-2xl font-semibold text-foreground">{formatCount(card.value)}</div>
              </div>
            ))}
          </div>

          {state.tasks?.partial || warnings.length > 0 ? (
            <div className="rounded-lg border border-border bg-secondary/40 px-4 py-3 text-sm text-foreground">
              {intl.formatMessage({ id: "tasks.partialWarning" })}
            </div>
          ) : null}

          <SectionCard
            action={(
              <Button disabled={state.refreshing} onClick={() => void loadTasks(true)} size="sm" type="button" variant="outline">
                {intl.formatMessage({ id: "tasks.actions.refresh" })}
              </Button>
            )}
            description={intl.formatMessage({ id: "tasks.listDescription" })}
            title={intl.formatMessage({ id: "tasks.listTitle" })}
          >
            <div className="mb-4 grid gap-3 md:grid-cols-5">
              <label className="text-sm font-medium text-foreground">
                {intl.formatMessage({ id: "tasks.filters.domain" })}
                <select className="mt-1 h-8 w-full rounded-md border border-border bg-background px-2 text-sm" onChange={(event) => setDomain(event.target.value)} value={domain}>
                  <option value="">{intl.formatMessage({ id: "tasks.filters.all" })}</option>
                  {domainOptions.map((option) => <option key={option} value={option}>{option}</option>)}
                </select>
              </label>
              <label className="text-sm font-medium text-foreground">
                {intl.formatMessage({ id: "tasks.filters.status" })}
                <select className="mt-1 h-8 w-full rounded-md border border-border bg-background px-2 text-sm" onChange={(event) => setStatus(event.target.value)} value={status}>
                  <option value="">{intl.formatMessage({ id: "tasks.filters.all" })}</option>
                  {statusOptions.map((option) => <option key={option} value={option}>{option}</option>)}
                </select>
              </label>
              <label className="text-sm font-medium text-foreground">
                {intl.formatMessage({ id: "tasks.filters.scope" })}
                <select className="mt-1 h-8 w-full rounded-md border border-border bg-background px-2 text-sm" onChange={(event) => setScope(event.target.value)} value={scope}>
                  <option value="">{intl.formatMessage({ id: "tasks.filters.all" })}</option>
                  {scopeOptions.map((option) => <option key={option} value={option}>{option}</option>)}
                </select>
              </label>
              <label className="text-sm font-medium text-foreground">
                {intl.formatMessage({ id: "tasks.filters.node" })}
                <input className="mt-1 h-8 w-full rounded-md border border-border bg-background px-2 text-sm" onChange={(event) => setNodeIdText(event.target.value)} value={nodeIdText} />
              </label>
              <label className="text-sm font-medium text-foreground">
                {intl.formatMessage({ id: "tasks.filters.keyword" })}
                <input className="mt-1 h-8 w-full rounded-md border border-border bg-background px-2 text-sm" onChange={(event) => setKeyword(event.target.value)} value={keyword} />
              </label>
            </div>

            {tasks.length === 0 ? (
              <ResourceState
                kind="empty"
                title={intl.formatMessage({ id: "tasks.empty.title" })}
                description={intl.formatMessage({ id: "tasks.empty.description" })}
              />
            ) : (
              <div className="overflow-x-auto">
                <table className="w-full min-w-[920px] border-collapse text-left">
                  <thead className="text-xs uppercase tracking-[0.16em] text-muted-foreground">
                    <tr>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "tasks.table.domain" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "tasks.table.kind" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "tasks.table.scope" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "tasks.table.status" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "tasks.table.phase" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "tasks.table.nodes" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "tasks.table.attempt" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "tasks.table.updated" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "tasks.table.error" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "tasks.table.actions" })}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {tasks.map((task) => (
                      <tr className="border-t border-border" key={`${task.domain}-${task.id}`}>
                        <td className="px-3 py-3 text-sm text-foreground">{task.domain}</td>
                        <td className="px-3 py-3 text-sm text-foreground">{task.kind}</td>
                        <td className="px-3 py-3 text-sm text-foreground">{formatTaskScope(task)}</td>
                        <td className="px-3 py-3"><StatusBadge value={task.status} /></td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">{task.phase || "-"}</td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">{formatNodePair(task)}</td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">{task.attempt}</td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">{formatTaskTime(task)}</td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">{task.last_error || "-"}</td>
                        <td className="px-3 py-3">
                          <Button onClick={() => void openDetail(task)} size="sm" type="button" variant="outline">
                            {intl.formatMessage({ id: "tasks.actions.viewDetail" })}
                          </Button>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </SectionCard>
        </>
      )}

      <DetailSheet
        description={detailTask ? formatTaskScope(detailTask) : undefined}
        onOpenChange={(open) => {
          if (!open) {
            setDetailTask(null)
            setDetail(null)
            setDetailError("")
          }
        }}
        open={detailTask !== null}
        title={intl.formatMessage({ id: "tasks.detailTitle" })}
      >
        {detailLoading ? <ResourceState kind="loading" title={intl.formatMessage({ id: "tasks.detailTitle" })} /> : null}
        {detailError ? <p className="text-sm text-destructive">{detailError}</p> : null}
        {detail ? (
          <div className="space-y-4 text-sm">
            <div className="rounded-lg border border-border bg-muted/30 p-3">
              <div className="text-xs font-medium uppercase tracking-[0.16em] text-muted-foreground">ID</div>
              <div className="mt-1 font-mono text-foreground">{detail.task.id}</div>
            </div>
            <div className="grid gap-3 md:grid-cols-2">
              <div><span className="text-muted-foreground">{intl.formatMessage({ id: "tasks.table.domain" })}: </span>{detail.task.domain}</div>
              <div><span className="text-muted-foreground">{intl.formatMessage({ id: "tasks.table.status" })}: </span>{detail.task.status}</div>
              <div><span className="text-muted-foreground">{intl.formatMessage({ id: "tasks.table.phase" })}: </span>{detail.task.phase || "-"}</div>
              <div><span className="text-muted-foreground">{intl.formatMessage({ id: "tasks.table.nodes" })}: </span>{formatNodePair(detail.task)}</div>
            </div>
            <div className="rounded-lg border border-border bg-muted/30 p-3">
              <div className="text-xs font-medium uppercase tracking-[0.16em] text-muted-foreground">{intl.formatMessage({ id: "tasks.detailRawStatus" })}</div>
              <div className="mt-1 text-foreground">{detail.detail.raw_status || "-"}</div>
            </div>
            <SourceSpecificDetail detail={detail} intl={intl} />
          </div>
        ) : null}
      </DetailSheet>
    </PageContainer>
  )
}
