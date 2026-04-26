import { useCallback, useEffect, useMemo, useState } from "react"
import { useIntl } from "react-intl"

import { useAuthStore } from "@/auth/auth-store"
import { ConfirmDialog } from "@/components/manager/confirm-dialog"
import { ResourceState } from "@/components/manager/resource-state"
import { StatusBadge } from "@/components/manager/status-badge"
import { TableToolbar } from "@/components/manager/table-toolbar"
import { Button } from "@/components/ui/button"
import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
import { SectionCard } from "@/components/shell/section-card"
import {
  ManagerApiError,
  createNodeOnboardingPlan,
  getNodeOnboardingCandidates,
  getNodeOnboardingJob,
  getNodeOnboardingJobs,
  retryNodeOnboardingJob,
  startNodeOnboardingJob,
} from "@/lib/manager-api"
import type {
  ManagerNodeOnboardingCandidate,
  ManagerNodeOnboardingJob,
  ManagerNodeOnboardingJobsResponse,
} from "@/lib/manager-api.types"

type OnboardingState = {
  candidates: ManagerNodeOnboardingCandidate[]
  jobs: ManagerNodeOnboardingJobsResponse
  loading: boolean
  refreshing: boolean
  error: Error | null
}

const emptyJobs: ManagerNodeOnboardingJobsResponse = { items: [], next_cursor: "", has_more: false }
const terminalJobStatuses = new Set(["completed", "failed", "cancelled"])

function hasPermission(
  permissions: { resource: string; actions: string[] }[],
  resource: string,
  action: string,
) {
  return permissions.some((permission) => {
    if (permission.resource !== resource && permission.resource !== "*") {
      return false
    }
    return permission.actions.includes(action) || permission.actions.includes("*")
  })
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

function mergeJobIntoPage(page: ManagerNodeOnboardingJobsResponse, job: ManagerNodeOnboardingJob) {
  const existingIndex = page.items.findIndex((item) => item.job_id === job.job_id)
  if (existingIndex < 0) {
    return { ...page, items: [job, ...page.items] }
  }
  const items = [...page.items]
  items[existingIndex] = job
  return { ...page, items }
}

function formatNodeLabel(nodeID: number) {
  return `Node ${nodeID}`
}

function formatPeerMove(source: number, target: number) {
  return `${source} -> ${target}`
}

export function OnboardingPage() {
  const intl = useIntl()
  const permissions = useAuthStore((state) => state.permissions)
  const canWriteSlots = useMemo(
    () => hasPermission(permissions, "cluster.slot", "w"),
    [permissions],
  )
  const [state, setState] = useState<OnboardingState>({
    candidates: [],
    jobs: emptyJobs,
    loading: true,
    refreshing: false,
    error: null,
  })
  const [activeJob, setActiveJob] = useState<ManagerNodeOnboardingJob | null>(null)
  const [planPendingNodeID, setPlanPendingNodeID] = useState<number | null>(null)
  const [actionError, setActionError] = useState("")
  const [startConfirmOpen, setStartConfirmOpen] = useState(false)
  const [startPending, setStartPending] = useState(false)
  const [retryPendingJobID, setRetryPendingJobID] = useState("")
  const activeJobID = activeJob?.job_id
  const activeJobStatus = activeJob?.status

  const loadOnboarding = useCallback(async (refreshing: boolean) => {
    setState((current) => ({
      ...current,
      loading: refreshing ? current.loading : true,
      refreshing,
      error: null,
    }))

    try {
      const [candidates, jobs] = await Promise.all([
        getNodeOnboardingCandidates(),
        getNodeOnboardingJobs({ limit: 20 }),
      ])
      setState({
        candidates: candidates.items,
        jobs,
        loading: false,
        refreshing: false,
        error: null,
      })
    } catch (error) {
      setState({
        candidates: [],
        jobs: emptyJobs,
        loading: false,
        refreshing: false,
        error: error instanceof Error ? error : new Error("node onboarding request failed"),
      })
    }
  }, [])

  useEffect(() => {
    void loadOnboarding(false)
  }, [loadOnboarding])

  useEffect(() => {
    if (!activeJobID || activeJobStatus !== "running") {
      return undefined
    }

    let cancelled = false
    const refreshJob = () => {
      void getNodeOnboardingJob(activeJobID).then((job) => {
        if (cancelled) {
          return
        }
        setActiveJob(job)
        setState((current) => ({
          ...current,
          jobs: mergeJobIntoPage(current.jobs, job),
        }))
      }).catch((error) => {
        if (!cancelled) {
          setActionError(error instanceof Error ? error.message : "node onboarding polling failed")
        }
      })
    }

    refreshJob()
    const timer = window.setInterval(refreshJob, 3000)

    return () => {
      cancelled = true
      window.clearInterval(timer)
    }
  }, [activeJobID, activeJobStatus])

  const createPlan = useCallback(async (candidate: ManagerNodeOnboardingCandidate) => {
    if (!canWriteSlots) {
      return
    }
    setPlanPendingNodeID(candidate.node_id)
    setActionError("")
    try {
      const job = await createNodeOnboardingPlan({ targetNodeId: candidate.node_id })
      setActiveJob(job)
      setState((current) => ({
        ...current,
        jobs: mergeJobIntoPage(current.jobs, job),
      }))
    } catch (error) {
      setActionError(error instanceof Error ? error.message : "node onboarding plan failed")
    } finally {
      setPlanPendingNodeID(null)
    }
  }, [canWriteSlots])

  const startJob = useCallback(async () => {
    if (!activeJob) {
      return
    }
    setStartPending(true)
    setActionError("")
    try {
      const job = await startNodeOnboardingJob(activeJob.job_id)
      setActiveJob(job)
      setState((current) => ({
        ...current,
        jobs: mergeJobIntoPage(current.jobs, job),
      }))
      setStartConfirmOpen(false)
    } catch (error) {
      setActionError(error instanceof Error ? error.message : "node onboarding start failed")
    } finally {
      setStartPending(false)
    }
  }, [activeJob])

  const retryJob = useCallback(async (jobID: string) => {
    if (!canWriteSlots) {
      return
    }
    setRetryPendingJobID(jobID)
    setActionError("")
    try {
      const job = await retryNodeOnboardingJob(jobID)
      setActiveJob(job)
      setState((current) => ({
        ...current,
        jobs: mergeJobIntoPage(current.jobs, job),
      }))
    } catch (error) {
      setActionError(error instanceof Error ? error.message : "node onboarding retry failed")
    } finally {
      setRetryPendingJobID("")
    }
  }, [canWriteSlots])

  const activeMoves = activeJob?.plan.moves ?? []
  const runningOrTerminal = activeJob ? activeJob.status === "running" || terminalJobStatuses.has(activeJob.status) : false

  return (
    <PageContainer>
      <PageHeader
        title={intl.formatMessage({ id: "nav.onboarding.title" })}
        description={intl.formatMessage({ id: "nav.onboarding.description" })}
        actions={
          <Button
            onClick={() => {
              void loadOnboarding(true)
            }}
            size="sm"
            variant="outline"
          >
            {state.refreshing
              ? intl.formatMessage({ id: "common.refreshing" })
              : intl.formatMessage({ id: "common.refresh" })}
          </Button>
        }
      >
        <div className="flex flex-wrap gap-2 text-xs text-muted-foreground">
          <div className="rounded-md border border-border bg-background px-3 py-2">
            {intl.formatMessage({ id: "onboarding.scope" })}
          </div>
          <div className="rounded-md border border-border bg-background px-3 py-2">
            {intl.formatMessage(
              { id: "onboarding.candidateCount" },
              { total: state.candidates.length },
            )}
          </div>
        </div>
      </PageHeader>

      {state.loading ? <ResourceState kind="loading" title={intl.formatMessage({ id: "nav.onboarding.title" })} /> : null}
      {!state.loading && state.error ? (
        <ResourceState
          kind={mapErrorKind(state.error)}
          onRetry={() => {
            void loadOnboarding(false)
          }}
          title={intl.formatMessage({ id: "nav.onboarding.title" })}
        />
      ) : null}

      {!state.loading && !state.error ? (
        <>
          {!canWriteSlots ? (
            <div className="rounded-lg border border-border bg-muted/30 px-4 py-3 text-sm text-muted-foreground">
              {intl.formatMessage({ id: "onboarding.permissionRequired" })}
            </div>
          ) : null}
          {actionError ? <p className="text-sm text-destructive">{actionError}</p> : null}

          <SectionCard
            description={intl.formatMessage({ id: "onboarding.candidatesDescription" })}
            title={intl.formatMessage({ id: "onboarding.candidatesTitle" })}
          >
            <TableToolbar
              description={intl.formatMessage({ id: "onboarding.candidatesToolbarDescription" })}
              onRefresh={() => {
                void loadOnboarding(true)
              }}
              refreshing={state.refreshing}
              title={intl.formatMessage({ id: "onboarding.candidatesToolbarTitle" })}
            />
            {state.candidates.length > 0 ? (
              <div className="overflow-x-auto rounded-lg border border-border">
                <table className="w-full border-collapse">
                  <thead className="bg-muted/40 text-left text-xs uppercase tracking-[0.14em] text-muted-foreground">
                    <tr>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "onboarding.table.node" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "onboarding.table.status" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "onboarding.table.load" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "onboarding.table.actions" })}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {state.candidates.map((candidate) => (
                      <tr className="border-t border-border" key={candidate.node_id}>
                        <td className="px-3 py-3 text-sm font-medium text-foreground">
                          {formatNodeLabel(candidate.node_id)}
                          <div className="text-xs font-normal text-muted-foreground">{candidate.addr}</div>
                        </td>
                        <td className="px-3 py-3 text-sm text-foreground">
                          <div className="flex flex-wrap gap-2">
                            <StatusBadge value={candidate.status} />
                            <StatusBadge value={candidate.join_state} />
                            {candidate.recommended ? <StatusBadge value="recommended" /> : null}
                          </div>
                        </td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">
                          {intl.formatMessage(
                            { id: "onboarding.loadValue" },
                            { slots: candidate.slot_count, leaders: candidate.leader_count },
                          )}
                        </td>
                        <td className="px-3 py-3 text-sm">
                          <Button
                            aria-label={intl.formatMessage(
                              { id: "onboarding.reviewPlanForNode" },
                              { id: candidate.node_id },
                            )}
                            disabled={!canWriteSlots || planPendingNodeID === candidate.node_id}
                            onClick={() => {
                              void createPlan(candidate)
                            }}
                            size="sm"
                          >
                            {intl.formatMessage({ id: "onboarding.reviewPlan" })}
                          </Button>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            ) : (
              <ResourceState kind="empty" title={intl.formatMessage({ id: "onboarding.candidatesTitle" })} />
            )}
          </SectionCard>

          {activeJob ? (
            <SectionCard
              action={activeJob.status === "planned" ? (
                <Button
                  disabled={!canWriteSlots || runningOrTerminal}
                  onClick={() => setStartConfirmOpen(true)}
                  size="sm"
                >
                  {intl.formatMessage({ id: "onboarding.start" })}
                </Button>
              ) : null}
              description={intl.formatMessage(
                { id: "onboarding.planDescription" },
                { job: activeJob.job_id },
              )}
              title={intl.formatMessage({ id: "onboarding.planTitle" })}
            >
              <div className="mb-4 flex flex-wrap items-center gap-2 text-sm text-muted-foreground">
                <StatusBadge value={activeJob.status} />
                <span>{intl.formatMessage({ id: "onboarding.targetNode" }, { id: activeJob.target_node_id })}</span>
                <span>{intl.formatMessage({ id: "onboarding.pendingCount" }, { count: activeJob.result_counts.pending })}</span>
                <span>{intl.formatMessage({ id: "onboarding.completedCount" }, { count: activeJob.result_counts.completed })}</span>
              </div>
              <div className="grid gap-3 md:grid-cols-4">
                <div className="rounded-lg border border-border bg-muted/30 px-3 py-3">
                  <div className="text-xs uppercase tracking-[0.16em] text-muted-foreground">
                    {intl.formatMessage({ id: "onboarding.summary.currentSlots" })}
                  </div>
                  <div className="mt-2 text-2xl font-semibold text-foreground">
                    {activeJob.plan.summary.current_target_slot_count}
                  </div>
                </div>
                <div className="rounded-lg border border-border bg-muted/30 px-3 py-3">
                  <div className="text-xs uppercase tracking-[0.16em] text-muted-foreground">
                    {intl.formatMessage({ id: "onboarding.summary.plannedSlots" })}
                  </div>
                  <div className="mt-2 text-2xl font-semibold text-foreground">
                    {activeJob.plan.summary.planned_target_slot_count}
                  </div>
                </div>
                <div className="rounded-lg border border-border bg-muted/30 px-3 py-3">
                  <div className="text-xs uppercase tracking-[0.16em] text-muted-foreground">
                    {intl.formatMessage({ id: "onboarding.summary.currentLeaders" })}
                  </div>
                  <div className="mt-2 text-2xl font-semibold text-foreground">
                    {activeJob.plan.summary.current_target_leader_count}
                  </div>
                </div>
                <div className="rounded-lg border border-border bg-muted/30 px-3 py-3">
                  <div className="text-xs uppercase tracking-[0.16em] text-muted-foreground">
                    {intl.formatMessage({ id: "onboarding.summary.leaderGain" })}
                  </div>
                  <div className="mt-2 text-2xl font-semibold text-foreground">
                    {activeJob.plan.summary.planned_leader_gain}
                  </div>
                </div>
              </div>
              <div className="mt-4 overflow-x-auto rounded-lg border border-border">
                <table className="w-full border-collapse">
                  <thead className="bg-muted/40 text-left text-xs uppercase tracking-[0.14em] text-muted-foreground">
                    <tr>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "onboarding.table.slot" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "onboarding.table.move" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "onboarding.table.peers" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "onboarding.table.leader" })}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {activeMoves.map((move) => (
                      <tr className="border-t border-border" key={move.slot_id}>
                        <td className="px-3 py-3 text-sm font-medium text-foreground">
                          {intl.formatMessage({ id: "onboarding.slotValue" }, { id: move.slot_id })}
                        </td>
                        <td className="px-3 py-3 text-sm text-foreground">
                          {formatPeerMove(move.source_node_id, move.target_node_id)}
                        </td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">
                          {move.desired_peers_before.join(", ")}{" -> "}{move.desired_peers_after.join(", ")}
                        </td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">
                          {move.leader_transfer_required
                            ? intl.formatMessage({ id: "onboarding.leaderTransferRequired" })
                            : intl.formatMessage({ id: "onboarding.leaderTransferNotRequired" })}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
              {activeJob.plan.blocked_reasons.length > 0 ? (
                <div className="mt-4 rounded-lg border border-border bg-muted/30 p-3 text-sm text-muted-foreground">
                  {activeJob.plan.blocked_reasons.map((reason) => (
                    <div key={`${reason.code}-${reason.slot_id}-${reason.node_id}`}>{reason.message || reason.code}</div>
                  ))}
                </div>
              ) : null}
            </SectionCard>
          ) : null}

          <SectionCard
            description={intl.formatMessage({ id: "onboarding.jobsDescription" })}
            title={intl.formatMessage({ id: "onboarding.jobsTitle" })}
          >
            {state.jobs.items.length > 0 ? (
              <div className="overflow-x-auto rounded-lg border border-border">
                <table className="w-full border-collapse">
                  <thead className="bg-muted/40 text-left text-xs uppercase tracking-[0.14em] text-muted-foreground">
                    <tr>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "onboarding.table.job" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "onboarding.table.target" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "onboarding.table.status" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "onboarding.table.result" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "onboarding.table.actions" })}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {state.jobs.items.map((job) => (
                      <tr className="border-t border-border" key={job.job_id}>
                        <td className="px-3 py-3 text-sm font-medium text-foreground">{job.job_id}</td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">{formatNodeLabel(job.target_node_id)}</td>
                        <td className="px-3 py-3 text-sm text-foreground"><StatusBadge value={job.status} /></td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">
                          {job.last_error || intl.formatMessage(
                            { id: "onboarding.resultValue" },
                            { completed: job.result_counts.completed, failed: job.result_counts.failed },
                          )}
                        </td>
                        <td className="px-3 py-3 text-sm">
                          {job.status === "failed" ? (
                            <Button
                              aria-label={intl.formatMessage(
                                { id: "onboarding.retryJobLabel" },
                                { id: job.job_id },
                              )}
                              disabled={!canWriteSlots || retryPendingJobID === job.job_id}
                              onClick={() => {
                                void retryJob(job.job_id)
                              }}
                              size="sm"
                              variant="outline"
                            >
                              {intl.formatMessage({ id: "common.retry" })}
                            </Button>
                          ) : (
                            <Button
                              onClick={() => setActiveJob(job)}
                              size="sm"
                              variant="outline"
                            >
                              {intl.formatMessage({ id: "common.inspect" })}
                            </Button>
                          )}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            ) : (
              <ResourceState kind="empty" title={intl.formatMessage({ id: "onboarding.jobsTitle" })} />
            )}
          </SectionCard>
        </>
      ) : null}

      <ConfirmDialog
        confirmLabel={intl.formatMessage({ id: "common.confirm" })}
        description={activeJob ? intl.formatMessage({ id: "onboarding.startConfirmDescription" }, { id: activeJob.job_id }) : undefined}
        error={actionError}
        onConfirm={() => {
          void startJob()
        }}
        onOpenChange={setStartConfirmOpen}
        open={startConfirmOpen}
        pending={startPending}
        title={intl.formatMessage({ id: "onboarding.startConfirmTitle" })}
      />
    </PageContainer>
  )
}
