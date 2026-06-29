import { useCallback, useEffect, useMemo, useState } from "react"

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
  getControllerTaskAuditEvents,
  getControllerTaskAudits,
  getControllerTasks,
} from "@/lib/manager-api"
import type {
  ControllerTaskAuditListParams,
  ControllerTaskAuditStatus,
  ControllerTaskKind,
  ControllerTaskListParams,
  ControllerTaskStatus,
  ManagerControllerTask,
  ManagerControllerTaskAuditEventsResponse,
  ManagerControllerTaskAuditSnapshot,
  ManagerControllerTaskAuditsResponse,
  ManagerControllerTasksResponse,
} from "@/lib/manager-api.types"
import { useIntl } from "react-intl"

type TasksState = {
  active: ManagerControllerTasksResponse | null
  audits: ManagerControllerTaskAuditsResponse | null
  loading: boolean
  refreshing: boolean
  error: Error | null
}

const kindOptions: ControllerTaskKind[] = ["bootstrap", "leader_transfer", "slot_replica_move"]
const statusOptions: ControllerTaskAuditStatus[] = ["pending", "running", "failed", "completed"]

function emptyTasksState(): TasksState {
  return {
    active: null,
    audits: null,
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

function parsePositiveInt(raw: string) {
  const value = Number(raw)
  return Number.isFinite(value) && value > 0 ? value : undefined
}

function formatNodePair(sourceNode: number, targetNode: number) {
  if (!sourceNode && !targetNode) {
    return "-"
  }
  return `${sourceNode || "-"} -> ${targetNode || "-"}`
}

function formatNodeList(nodes?: number[] | null) {
  return nodes && nodes.length > 0 ? nodes.join(", ") : "-"
}

function formatTime(value?: string | null) {
  return value || "-"
}

function formatCount(value: number | undefined) {
  return String(value ?? 0)
}

function formatParticipants(task: ManagerControllerTask) {
  if (!task.participants || task.participants.length === 0) {
    return "-"
  }
  return task.participants.map((item) => `${item.node_id}:${item.status}`).join(", ")
}

function taskAuditStatusForActive(status: ControllerTaskAuditStatus | ""): ControllerTaskStatus | undefined {
  if (status === "pending" || status === "running" || status === "failed") {
    return status
  }
  return undefined
}

export function TasksPage() {
  const intl = useIntl()
  const [state, setState] = useState<TasksState>(emptyTasksState)
  const [kind, setKind] = useState<ControllerTaskKind | "">("")
  const [status, setStatus] = useState<ControllerTaskAuditStatus | "">("")
  const [slotIdText, setSlotIdText] = useState("")
  const [nodeIdText, setNodeIdText] = useState("")
  const [keyword, setKeyword] = useState("")
  const [timelineTask, setTimelineTask] = useState<ManagerControllerTaskAuditSnapshot | null>(null)
  const [timeline, setTimeline] = useState<ManagerControllerTaskAuditEventsResponse | null>(null)
  const [timelineLoading, setTimelineLoading] = useState(false)
  const [timelineError, setTimelineError] = useState("")

  const buildQueries = useCallback((): { active: ControllerTaskListParams; audits: ControllerTaskAuditListParams } => {
    const slotId = parsePositiveInt(slotIdText)
    const nodeId = parsePositiveInt(nodeIdText)
    const active: ControllerTaskListParams = {
      kind: kind || undefined,
      status: taskAuditStatusForActive(status),
      slotId,
      nodeId,
      limit: 50,
    }
    const audits: ControllerTaskAuditListParams = {
      kind: kind || undefined,
      status: status || undefined,
      slotId,
      nodeId,
      keyword: keyword.trim() || undefined,
      limit: 200,
    }
    return { active, audits }
  }, [kind, keyword, nodeIdText, slotIdText, status])

  const loadTasks = useCallback(async (refreshing = false) => {
    setState((current) => ({
      ...current,
      loading: !refreshing,
      refreshing,
      error: null,
    }))
    try {
      const query = buildQueries()
      const [active, audits] = await Promise.all([
        getControllerTasks(query.active),
        getControllerTaskAudits(query.audits),
      ])
      setState({ active, audits, loading: false, refreshing: false, error: null })
    } catch (error) {
      setState({
        active: null,
        audits: null,
        loading: false,
        refreshing: false,
        error: error instanceof Error ? error : new Error("controller task request failed"),
      })
    }
  }, [buildQueries])

  useEffect(() => {
    void loadTasks(false)
    // Initial load intentionally uses default filters only.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const openTimeline = useCallback(async (task: ManagerControllerTaskAuditSnapshot) => {
    setTimelineTask(task)
    setTimeline(null)
    setTimelineError("")
    setTimelineLoading(true)
    try {
      const response = await getControllerTaskAuditEvents(task.task_id)
      setTimeline(response)
    } catch (error) {
      setTimelineError(error instanceof Error ? error.message : "task audit request failed")
    } finally {
      setTimelineLoading(false)
    }
  }, [])

  const activeTasks = state.active?.items ?? []
  const auditTasks = state.audits?.items ?? []

  const summaryCards = useMemo(() => {
    const activeTotal = state.active?.total ?? activeTasks.length
    const activeRunning = activeTasks.filter((task) => task.status === "running").length
    const activeFailed = activeTasks.filter((task) => task.status === "failed").length
    const completed = auditTasks.filter((task) => task.status === "completed").length
    return [
      { label: intl.formatMessage({ id: "tasks.summary.total" }), value: activeTotal },
      { label: intl.formatMessage({ id: "tasks.summary.running" }), value: activeRunning },
      { label: intl.formatMessage({ id: "tasks.summary.failed" }), value: activeFailed },
      { label: intl.formatMessage({ id: "tasks.summary.history" }), value: state.audits?.total },
      { label: intl.formatMessage({ id: "tasks.summary.completed" }), value: completed },
    ]
  }, [activeTasks, auditTasks, intl, state.active?.total, state.audits?.total])

  const hasRows = activeTasks.length > 0 || auditTasks.length > 0

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

          {state.audits?.truncated ? (
            <div className="rounded-lg border border-border bg-secondary/40 px-4 py-3 text-sm text-foreground">
              {intl.formatMessage({ id: "tasks.auditTruncated" })}
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
                {intl.formatMessage({ id: "tasks.filters.kind" })}
                <select className="mt-1 h-8 w-full rounded-md border border-border bg-background px-2 text-sm" onChange={(event) => setKind(event.target.value as ControllerTaskKind | "")} value={kind}>
                  <option value="">{intl.formatMessage({ id: "tasks.filters.all" })}</option>
                  {kindOptions.map((option) => <option key={option} value={option}>{option}</option>)}
                </select>
              </label>
              <label className="text-sm font-medium text-foreground">
                {intl.formatMessage({ id: "tasks.filters.status" })}
                <select className="mt-1 h-8 w-full rounded-md border border-border bg-background px-2 text-sm" onChange={(event) => setStatus(event.target.value as ControllerTaskAuditStatus | "")} value={status}>
                  <option value="">{intl.formatMessage({ id: "tasks.filters.all" })}</option>
                  {statusOptions.map((option) => <option key={option} value={option}>{option}</option>)}
                </select>
              </label>
              <label className="text-sm font-medium text-foreground">
                {intl.formatMessage({ id: "tasks.filters.slot" })}
                <input className="mt-1 h-8 w-full rounded-md border border-border bg-background px-2 text-sm" onChange={(event) => setSlotIdText(event.target.value)} value={slotIdText} />
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

            {!hasRows ? (
              <ResourceState
                kind="empty"
                title={intl.formatMessage({ id: "tasks.empty.title" })}
                description={intl.formatMessage({ id: "tasks.empty.description" })}
              />
            ) : null}
          </SectionCard>

          <SectionCard
            description={intl.formatMessage({ id: "tasks.activeDescription" })}
            title={intl.formatMessage({ id: "tasks.activeTitle" })}
          >
            {activeTasks.length === 0 ? (
              <ResourceState
                kind="empty"
                title={intl.formatMessage({ id: "tasks.activeEmpty.title" })}
                description={intl.formatMessage({ id: "tasks.activeEmpty.description" })}
              />
            ) : (
              <div className="overflow-x-auto">
                <table className="w-full min-w-[960px] border-collapse text-left">
                  <thead className="text-xs uppercase tracking-[0.16em] text-muted-foreground">
                    <tr>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "tasks.table.id" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "tasks.table.kind" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "tasks.table.slot" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "tasks.table.status" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "tasks.table.step" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "tasks.table.nodes" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "tasks.table.peers" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "tasks.table.participants" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "tasks.table.error" })}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {activeTasks.map((task) => (
                      <tr className="border-t border-border" key={task.task_id}>
                        <td className="px-3 py-3 font-mono text-xs text-foreground">{task.task_id}</td>
                        <td className="px-3 py-3 text-sm text-foreground">{task.kind}</td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">{task.slot_id}</td>
                        <td className="px-3 py-3"><StatusBadge value={task.status} /></td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">{task.step || "-"}</td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">{formatNodePair(task.source_node, task.target_node)}</td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">{formatNodeList(task.target_peers)}</td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">{formatParticipants(task)}</td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">{task.last_error || "-"}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </SectionCard>

          <SectionCard
            description={intl.formatMessage({ id: "tasks.auditDescription" })}
            title={intl.formatMessage({ id: "tasks.auditTitle" })}
          >
            {auditTasks.length === 0 ? (
              <ResourceState
                kind="empty"
                title={intl.formatMessage({ id: "tasks.auditEmpty.title" })}
                description={intl.formatMessage({ id: "tasks.auditEmpty.description" })}
              />
            ) : (
              <div className="overflow-x-auto">
                <table className="w-full min-w-[980px] border-collapse text-left">
                  <thead className="text-xs uppercase tracking-[0.16em] text-muted-foreground">
                    <tr>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "tasks.table.id" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "tasks.table.kind" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "tasks.table.status" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "tasks.table.slot" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "tasks.table.nodes" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "tasks.table.raft" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "tasks.table.updated" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "tasks.table.summary" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "tasks.table.actions" })}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {auditTasks.map((task) => (
                      <tr className="border-t border-border" key={task.task_id}>
                        <td className="px-3 py-3 font-mono text-xs text-foreground">{task.task_id}</td>
                        <td className="px-3 py-3 text-sm text-foreground">{task.kind}</td>
                        <td className="px-3 py-3"><StatusBadge value={task.status} /></td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">{task.slot_id}</td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">{formatNodePair(task.source_node, task.target_node)}</td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">{task.first_applied_raft_index}{" -> "}{task.last_applied_raft_index}</td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">{formatTime(task.completed_at ?? task.started_at)}</td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">{task.summary || task.last_reason || "-"}</td>
                        <td className="px-3 py-3">
                          <Button onClick={() => void openTimeline(task)} size="sm" type="button" variant="outline">
                            {intl.formatMessage({ id: "tasks.actions.viewTimeline" })}
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
        description={timelineTask ? `${timelineTask.kind} / slot ${timelineTask.slot_id}` : undefined}
        onOpenChange={(open) => {
          if (!open) {
            setTimelineTask(null)
            setTimeline(null)
            setTimelineError("")
          }
        }}
        open={timelineTask !== null}
        title={intl.formatMessage({ id: "tasks.timelineTitle" })}
      >
        {timelineLoading ? <ResourceState kind="loading" title={intl.formatMessage({ id: "tasks.timelineTitle" })} /> : null}
        {timelineError ? <p className="text-sm text-destructive">{timelineError}</p> : null}
        {timeline ? (
          <div className="space-y-4 text-sm">
            <KeyValueList
              items={[
                { label: "ID", value: timeline.task.task_id },
                { label: intl.formatMessage({ id: "tasks.table.status" }), value: <StatusBadge value={timeline.task.status} /> },
                { label: intl.formatMessage({ id: "tasks.table.nodes" }), value: formatNodePair(timeline.task.source_node, timeline.task.target_node) },
                { label: intl.formatMessage({ id: "tasks.table.raft" }), value: `${timeline.task.first_applied_raft_index} -> ${timeline.task.last_applied_raft_index}` },
              ]}
            />
            <div className="space-y-3">
              {timeline.events.map((event) => (
                <div className="rounded-lg border border-border bg-muted/30 p-3" key={event.event_id}>
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <div className="font-mono text-xs text-foreground">{event.event_id}</div>
                    <StatusBadge value={event.type} />
                  </div>
                  <div className="mt-2 grid gap-2 text-xs text-muted-foreground md:grid-cols-2">
                    <div>{intl.formatMessage({ id: "tasks.table.status" })}: {event.status || "-"}</div>
                    <div>{intl.formatMessage({ id: "tasks.table.raft" })}: {event.applied_raft_index}/{event.applied_raft_term}</div>
                    <div>{intl.formatMessage({ id: "tasks.timeline.command" })}: {event.command_kind || "-"}</div>
                    <div>{intl.formatMessage({ id: "tasks.timeline.participant" })}: {event.participant_node || "-"}</div>
                  </div>
                  <div className="mt-2 text-sm text-foreground">{event.summary || event.reason || "-"}</div>
                </div>
              ))}
            </div>
          </div>
        ) : null}
      </DetailSheet>
    </PageContainer>
  )
}
