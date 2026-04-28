import { useCallback, useEffect, useMemo, useState } from "react"
import { useIntl, type IntlShape } from "react-intl"

import { useAuthStore } from "@/auth/auth-store"
import { ConfirmDialog } from "@/components/manager/confirm-dialog"
import { DetailSheet } from "@/components/manager/detail-sheet"
import { KeyValueList } from "@/components/manager/key-value-list"
import { ResourceState } from "@/components/manager/resource-state"
import { StatusBadge } from "@/components/manager/status-badge"
import { TableToolbar } from "@/components/manager/table-toolbar"
import { Button } from "@/components/ui/button"
import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
import { SectionCard } from "@/components/shell/section-card"
import {
  ManagerApiError,
  advanceNodeScaleIn,
  cancelNodeScaleIn,
  getNode,
  getNodes,
  getNodeScaleInStatus,
  markNodeDraining,
  planNodeScaleIn,
  resumeNode,
  startNodeScaleIn,
} from "@/lib/manager-api"
import type {
  ManagerNode,
  ManagerNodeDetailResponse,
  ManagerNodeScaleInReport,
  ManagerNodesResponse,
} from "@/lib/manager-api.types"

type NodeAction = "drain" | "resume" | null
type ScaleInAction = "plan" | "refresh" | "start" | "advance" | "cancel" | null
type ScaleInConfirmAction = "start" | "cancel" | null

type NodesState = {
  nodes: ManagerNodesResponse | null
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

function createScaleInPlanInput(nodeId: number) {
  return {
    confirmStatefulSetTail: true,
    expectedTailNodeId: nodeId,
  }
}

function isNodeScaleInReport(value: unknown): value is ManagerNodeScaleInReport {
  if (!value || typeof value !== "object") {
    return false
  }
  const report = value as Partial<ManagerNodeScaleInReport>
  return typeof report.node_id === "number" && typeof report.status === "string"
}

function formatScaleInCheckLabel(value: string) {
  return value.replaceAll("_", " ")
}

function formatBooleanValue(intl: IntlShape, value: boolean) {
  return intl.formatMessage({ id: value ? "nodes.scaleIn.yes" : "nodes.scaleIn.no" })
}

function nodeHealthStatus(node: ManagerNode) {
  return node.health?.status ?? node.status
}

function nodeLastHeartbeat(node: ManagerNode) {
  return node.health?.last_heartbeat_at ?? node.last_heartbeat_at
}

function nodeSlotSummary(node: ManagerNode) {
  const replicaCount = node.slots?.replica_count ?? node.slot_stats.count
  const leaderCount = node.slots?.leader_count ?? node.slot_stats.leader_count
  const followerCount = node.slots?.follower_count ?? Math.max(replicaCount - leaderCount, 0)
  return {
    replicaCount,
    leaderCount,
    followerCount,
    quorumLostCount: node.slots?.quorum_lost_count ?? 0,
    unreportedCount: node.slots?.unreported_count ?? 0,
  }
}

function nodeMembershipRole(node: ManagerNode) {
  return node.membership?.role ?? "unknown"
}

function nodeJoinState(node: ManagerNode) {
  return node.membership?.join_state ?? "unknown"
}

function nodeRuntimeSummaryText(intl: IntlShape, node: ManagerNode) {
  if (!node.runtime || node.runtime.unknown) {
    return intl.formatMessage({ id: "nodes.runtimeUnknown" })
  }
  return intl.formatMessage(
    { id: "nodes.runtimeSummary" },
    { sessions: node.runtime.gateway_sessions, online: node.runtime.active_online },
  )
}

function nodeSlotSummaryText(intl: IntlShape, node: ManagerNode) {
  const slots = nodeSlotSummary(node)
  return intl.formatMessage(
    { id: "nodes.slotLayerSummary" },
    {
      replicas: slots.replicaCount,
      leaders: slots.leaderCount,
      followers: slots.followerCount,
    },
  )
}

function nodeSchedulableText(intl: IntlShape, node: ManagerNode) {
  return intl.formatMessage({
    id: node.membership?.schedulable ? "nodes.schedulable" : "nodes.notSchedulable",
  })
}

function nodeControllerVoterText(intl: IntlShape, node: ManagerNode) {
  return intl.formatMessage({
    id: node.controller.voter ? "nodes.controllerVoter" : "nodes.controllerNonVoter",
  })
}

function canDrainNode(node: ManagerNode, canWriteNodes: boolean) {
  return canWriteNodes && (node.actions?.can_drain ?? true)
}

function canResumeNode(node: ManagerNode, canWriteNodes: boolean) {
  return canWriteNodes && (node.actions?.can_resume ?? true)
}

function canScaleInNode(node: ManagerNode, canReadScaleIn: boolean) {
  return canReadScaleIn && (node.actions?.can_scale_in ?? true)
}

function ScaleInMetricCard({ label, value }: { label: string; value: number | string }) {
  return (
    <div className="rounded-lg border border-border bg-muted/30 px-3 py-3">
      <div className="text-xs uppercase tracking-[0.16em] text-muted-foreground">{label}</div>
      <div className="mt-2 text-2xl font-semibold text-foreground">{value}</div>
    </div>
  )
}

function ScaleInReportView({ intl, report }: { intl: IntlShape; report: ManagerNodeScaleInReport }) {
  const checkEntries = Object.entries(report.checks)

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-2 text-sm text-muted-foreground">
        <StatusBadge value={report.status} />
        <span>
          {intl.formatMessage(
            { id: "nodes.scaleIn.safeToRemoveValue" },
            { value: formatBooleanValue(intl, report.safe_to_remove) },
          )}
        </span>
        {report.next_action ? (
          <span>
            {intl.formatMessage({ id: "nodes.scaleIn.nextActionValue" }, { value: report.next_action })}
          </span>
        ) : null}
      </div>

      <div className="grid gap-3 md:grid-cols-3">
        <ScaleInMetricCard
          label={intl.formatMessage({ id: "nodes.scaleIn.metrics.assignedReplicas" })}
          value={report.progress.assigned_slot_replicas}
        />
        <ScaleInMetricCard
          label={intl.formatMessage({ id: "nodes.scaleIn.metrics.observedReplicas" })}
          value={report.progress.observed_slot_replicas}
        />
        <ScaleInMetricCard
          label={intl.formatMessage({ id: "nodes.scaleIn.metrics.slotLeaders" })}
          value={report.progress.slot_leaders}
        />
        <ScaleInMetricCard
          label={intl.formatMessage({ id: "nodes.scaleIn.metrics.activeTasks" })}
          value={report.progress.active_tasks_involving_node}
        />
        <ScaleInMetricCard
          label={intl.formatMessage({ id: "nodes.scaleIn.metrics.activeConnections" })}
          value={
            report.progress.active_connections_unknown
              ? intl.formatMessage({ id: "nodes.scaleIn.unknown" })
              : report.progress.active_connections
          }
        />
        <ScaleInMetricCard
          label={intl.formatMessage({ id: "nodes.scaleIn.metrics.gatewaySessions" })}
          value={report.progress.gateway_sessions}
        />
      </div>

      <div className="rounded-lg border border-border bg-muted/20 p-3">
        <div className="text-sm font-semibold text-foreground">
          {intl.formatMessage({ id: "nodes.scaleIn.runtimeTitle" })}
        </div>
        <div className="mt-2 grid gap-2 text-sm text-muted-foreground md:grid-cols-2">
          <span>
            {intl.formatMessage(
              { id: "nodes.scaleIn.runtimeOnlineValue" },
              { active: report.runtime.active_online, closing: report.runtime.closing_online },
            )}
          </span>
          <span>
            {intl.formatMessage(
              { id: "nodes.scaleIn.runtimeAdmissionValue" },
              {
                accepting: formatBooleanValue(intl, report.runtime.accepting_new_sessions),
                draining: formatBooleanValue(intl, report.runtime.draining),
              },
            )}
          </span>
        </div>
      </div>

      {report.blocked_reasons.length > 0 ? (
        <div className="rounded-lg border border-border bg-muted/30 p-3 text-sm text-muted-foreground">
          <div className="mb-2 font-semibold text-foreground">
            {intl.formatMessage({ id: "nodes.scaleIn.blockedReasons" })}
          </div>
          {report.blocked_reasons.map((reason) => (
            <div key={`${reason.code}-${reason.slot_id}-${reason.node_id}`}>
              {reason.message || reason.code}
            </div>
          ))}
        </div>
      ) : null}

      <div className="grid gap-2 md:grid-cols-2">
        {checkEntries.map(([key, passed]) => (
          <div
            className="flex items-center justify-between rounded-lg border border-border bg-background px-3 py-2 text-sm"
            key={key}
          >
            <span className="text-muted-foreground">{formatScaleInCheckLabel(key)}</span>
            <StatusBadge value={passed ? "ready" : "blocked"} />
          </div>
        ))}
      </div>
    </div>
  )
}

export function NodesPage() {
  const intl = useIntl()
  const permissions = useAuthStore((state) => state.permissions)
  const canWriteNodes = useMemo(
    () => hasPermission(permissions, "cluster.node", "w"),
    [permissions],
  )
  const canReadScaleIn = useMemo(
    () => hasPermission(permissions, "cluster.node", "r") && hasPermission(permissions, "cluster.slot", "r"),
    [permissions],
  )
  const canWriteScaleIn = useMemo(
    () => hasPermission(permissions, "cluster.node", "w") && hasPermission(permissions, "cluster.slot", "w"),
    [permissions],
  )
  const [state, setState] = useState<NodesState>({
    nodes: null,
    loading: true,
    refreshing: false,
    error: null,
  })
  const [selectedNodeId, setSelectedNodeId] = useState<number | null>(null)
  const [detail, setDetail] = useState<ManagerNodeDetailResponse | null>(null)
  const [detailLoading, setDetailLoading] = useState(false)
  const [detailError, setDetailError] = useState<Error | null>(null)
  const [pendingAction, setPendingAction] = useState<NodeAction>(null)
  const [actionError, setActionError] = useState<string>("")
  const [actionPending, setActionPending] = useState(false)
  const [scaleInNodeId, setScaleInNodeId] = useState<number | null>(null)
  const [scaleInReport, setScaleInReport] = useState<ManagerNodeScaleInReport | null>(null)
  const [scaleInAction, setScaleInAction] = useState<ScaleInAction>(null)
  const [scaleInError, setScaleInError] = useState("")
  const [scaleInConfirmAction, setScaleInConfirmAction] = useState<ScaleInConfirmAction>(null)

  const loadNodes = useCallback(async (refreshing: boolean) => {
    setState((current) => ({
      ...current,
      loading: refreshing ? current.loading : true,
      refreshing,
      error: null,
    }))

    try {
      const nodes = await getNodes()
      setState({ nodes, loading: false, refreshing: false, error: null })
    } catch (error) {
      setState({
        nodes: null,
        loading: false,
        refreshing: false,
        error: error instanceof Error ? error : new Error("node request failed"),
      })
    }
  }, [])

  const loadNodeDetail = useCallback(async (nodeId: number) => {
    setDetailLoading(true)
    setDetailError(null)

    try {
      const nextDetail = await getNode(nodeId)
      setDetail(nextDetail)
    } catch (error) {
      setDetail(null)
      setDetailError(error instanceof Error ? error : new Error("node detail failed"))
    } finally {
      setDetailLoading(false)
    }
  }, [])

  useEffect(() => {
    void loadNodes(false)
  }, [loadNodes])

  const openDetail = useCallback(
    async (nodeId: number) => {
      setSelectedNodeId(nodeId)
      await loadNodeDetail(nodeId)
    },
    [loadNodeDetail],
  )

  const closeDetail = useCallback((open: boolean) => {
    if (open) {
      return
    }
    setSelectedNodeId(null)
    setDetail(null)
    setDetailError(null)
    setPendingAction(null)
    setActionError("")
  }, [])

  const applyScaleInError = useCallback((error: unknown) => {
    if (error instanceof ManagerApiError && isNodeScaleInReport(error.report)) {
      setScaleInNodeId(error.report.node_id)
      setScaleInReport(error.report)
    }
    setScaleInError(error instanceof Error ? error.message : "node scale-in action failed")
  }, [])

  const reviewScaleIn = useCallback(async (nodeId: number) => {
    if (!canReadScaleIn) {
      return
    }

    setScaleInNodeId(nodeId)
    setScaleInReport(null)
    setScaleInError("")
    setScaleInAction("plan")

    try {
      const report = await planNodeScaleIn(nodeId, createScaleInPlanInput(nodeId))
      setScaleInReport(report)
    } catch (error) {
      applyScaleInError(error)
    } finally {
      setScaleInAction(null)
    }
  }, [applyScaleInError, canReadScaleIn])

  const refreshScaleInStatus = useCallback(async () => {
    if (!scaleInNodeId) {
      return
    }

    setScaleInError("")
    setScaleInAction("refresh")

    try {
      const report = await getNodeScaleInStatus(scaleInNodeId)
      setScaleInReport(report)
    } catch (error) {
      applyScaleInError(error)
    } finally {
      setScaleInAction(null)
    }
  }, [applyScaleInError, scaleInNodeId])

  const startScaleIn = useCallback(async () => {
    if (!scaleInNodeId) {
      return
    }

    setScaleInError("")
    setScaleInAction("start")

    try {
      const report = await startNodeScaleIn(scaleInNodeId, createScaleInPlanInput(scaleInNodeId))
      setScaleInReport(report)
      setScaleInConfirmAction(null)
      await loadNodes(true)
    } catch (error) {
      applyScaleInError(error)
    } finally {
      setScaleInAction(null)
    }
  }, [applyScaleInError, loadNodes, scaleInNodeId])

  const advanceScaleIn = useCallback(async () => {
    if (!scaleInNodeId) {
      return
    }

    setScaleInError("")
    setScaleInAction("advance")

    try {
      const report = await advanceNodeScaleIn(scaleInNodeId, { maxLeaderTransfers: 1 })
      setScaleInReport(report)
    } catch (error) {
      applyScaleInError(error)
    } finally {
      setScaleInAction(null)
    }
  }, [applyScaleInError, scaleInNodeId])

  const cancelScaleIn = useCallback(async () => {
    if (!scaleInNodeId) {
      return
    }

    setScaleInError("")
    setScaleInAction("cancel")

    try {
      const report = await cancelNodeScaleIn(scaleInNodeId)
      setScaleInReport(report)
      setScaleInConfirmAction(null)
      await loadNodes(true)
    } catch (error) {
      applyScaleInError(error)
    } finally {
      setScaleInAction(null)
    }
  }, [applyScaleInError, loadNodes, scaleInNodeId])

  const runAction = useCallback(async () => {
    if (!selectedNodeId || !pendingAction) {
      return
    }

    setActionPending(true)
    setActionError("")

    try {
      if (pendingAction === "drain") {
        await markNodeDraining(selectedNodeId)
      } else {
        await resumeNode(selectedNodeId)
      }
      setPendingAction(null)
      await loadNodes(true)
      await loadNodeDetail(selectedNodeId)
    } catch (error) {
      setActionError(error instanceof Error ? error.message : "node action failed")
    } finally {
      setActionPending(false)
    }
  }, [loadNodeDetail, loadNodes, pendingAction, selectedNodeId])

  return (
    <PageContainer>
      <PageHeader
        title={intl.formatMessage({ id: "nav.nodes.title" })}
        description={intl.formatMessage({ id: "nav.nodes.description" })}
        actions={
          <>
            <Button
              onClick={() => {
                void loadNodes(true)
              }}
              size="sm"
              variant="outline"
            >
              {state.refreshing
                ? intl.formatMessage({ id: "common.refreshing" })
                : intl.formatMessage({ id: "common.refresh" })}
            </Button>
            <Button disabled size="sm">
              {intl.formatMessage({ id: "common.inspect" })}
            </Button>
          </>
        }
      >
        <div className="flex flex-wrap gap-2 text-xs text-muted-foreground">
          <div className="rounded-md border border-border bg-background px-3 py-2">
            {intl.formatMessage({ id: "nodes.scopeAllNodes" })}
          </div>
          <div className="rounded-md border border-border bg-background px-3 py-2">
            {state.nodes
              ? intl.formatMessage({ id: "nodes.totalValue" }, { total: state.nodes.total })
              : intl.formatMessage({ id: "nodes.totalPending" })}
          </div>
        </div>
      </PageHeader>

      {state.loading ? <ResourceState kind="loading" title={intl.formatMessage({ id: "nav.nodes.title" })} /> : null}
      {!state.loading && state.error ? (
        <ResourceState
          kind={mapErrorKind(state.error)}
          onRetry={() => {
            void loadNodes(false)
          }}
          title={intl.formatMessage({ id: "nav.nodes.title" })}
        />
      ) : null}
      {!state.loading && !state.error && state.nodes ? (
        <SectionCard
          description={intl.formatMessage({ id: "nodes.inventoryDescription" })}
          title={intl.formatMessage({ id: "nodes.inventoryTitle" })}
        >
          <TableToolbar
            description={intl.formatMessage({ id: "nodes.toolbarDescription" })}
            onRefresh={() => {
              void loadNodes(true)
            }}
            refreshing={state.refreshing}
            title={intl.formatMessage({ id: "nodes.toolbarTitle" })}
          />
          {state.nodes.items.length > 0 ? (
            <div className="overflow-x-auto rounded-lg border border-border">
              <table className="w-full border-collapse">
                <thead className="bg-muted/40 text-left text-xs uppercase tracking-[0.14em] text-muted-foreground">
                  <tr>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "nodes.table.node" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "nodes.table.address" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "nodes.table.membership" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "nodes.table.health" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "nodes.table.controller" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "nodes.table.slots" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "nodes.table.runtime" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "nodes.table.actions" })}</th>
                  </tr>
                </thead>
                <tbody>
                  {state.nodes.items.map((node) => {
                    const healthStatus = nodeHealthStatus(node)
                    const isDraining = healthStatus === "draining"
                    const lifecycleActionAllowed = isDraining
                      ? canResumeNode(node, canWriteNodes)
                      : canDrainNode(node, canWriteNodes)
                    return (
                      <tr className="border-t border-border" key={node.node_id}>
                        <td className="px-3 py-3 text-sm font-medium text-foreground">
                          <div>{node.node_id}</div>
                          {node.name ? (
                            <div className="mt-1 text-xs font-normal text-muted-foreground">{node.name}</div>
                          ) : null}
                        </td>
                        <td className="px-3 py-3 text-sm text-foreground">{node.addr}</td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">
                          <div className="font-medium text-foreground">{nodeMembershipRole(node)}</div>
                          <div className="mt-1">{nodeJoinState(node)}</div>
                          <div className="mt-1 text-xs">{nodeSchedulableText(intl, node)}</div>
                        </td>
                        <td className="px-3 py-3 text-sm text-foreground">
                          <StatusBadge value={healthStatus} />
                          <div className="mt-2 text-xs text-muted-foreground">
                            {formatTimestamp(intl, nodeLastHeartbeat(node))}
                          </div>
                        </td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">
                          <div className="font-medium text-foreground">{node.controller.role}</div>
                          <div className="mt-1 text-xs">{nodeControllerVoterText(intl, node)}</div>
                        </td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">
                          {nodeSlotSummaryText(intl, node)}
                        </td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">
                          {nodeRuntimeSummaryText(intl, node)}
                        </td>
                        <td className="px-3 py-3 text-sm text-foreground">
                          <div className="flex items-center gap-2">
                            <Button
                              aria-label={intl.formatMessage(
                                { id: "nodes.inspectNode" },
                                { id: node.node_id },
                              )}
                              onClick={() => {
                                void openDetail(node.node_id)
                              }}
                              size="sm"
                              variant="outline"
                            >
                              {intl.formatMessage({ id: "common.inspect" })}
                            </Button>
                            <Button
                              disabled={!lifecycleActionAllowed}
                              onClick={() => {
                                setSelectedNodeId(node.node_id)
                                setPendingAction(isDraining ? "resume" : "drain")
                                setActionError("")
                              }}
                              size="sm"
                              variant="outline"
                            >
                              {isDraining
                                ? intl.formatMessage({ id: "nodes.resume" })
                                : intl.formatMessage({ id: "nodes.drain" })}
                            </Button>
                            <Button
                              aria-label={intl.formatMessage(
                                { id: "nodes.scaleIn.reviewForNode" },
                                { id: node.node_id },
                              )}
                              disabled={!canScaleInNode(node, canReadScaleIn) || scaleInAction === "plan"}
                              onClick={() => {
                                void reviewScaleIn(node.node_id)
                              }}
                              size="sm"
                              variant="outline"
                            >
                              {intl.formatMessage({ id: "nodes.scaleIn.button" })}
                            </Button>
                          </div>
                        </td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            </div>
          ) : (
            <ResourceState kind="empty" title={intl.formatMessage({ id: "nodes.inventoryTitle" })} />
          )}
        </SectionCard>
      ) : null}

      {scaleInNodeId ? (
        <SectionCard
          action={(
            <div className="flex flex-wrap items-center gap-2">
              {scaleInReport ? (
                <Button
                  disabled={!canReadScaleIn || scaleInAction === "refresh"}
                  onClick={() => {
                    void refreshScaleInStatus()
                  }}
                  size="sm"
                  variant="outline"
                >
                  {intl.formatMessage({ id: "nodes.scaleIn.refreshStatus" })}
                </Button>
              ) : null}
              {scaleInReport?.can_start ? (
                <Button
                  disabled={!canWriteScaleIn || scaleInAction === "start"}
                  onClick={() => setScaleInConfirmAction("start")}
                  size="sm"
                >
                  {intl.formatMessage({ id: "nodes.scaleIn.start" })}
                </Button>
              ) : null}
              {scaleInReport?.can_advance ? (
                <Button
                  disabled={!canWriteScaleIn || scaleInAction === "advance"}
                  onClick={() => {
                    void advanceScaleIn()
                  }}
                  size="sm"
                >
                  {intl.formatMessage({ id: "nodes.scaleIn.advance" })}
                </Button>
              ) : null}
              {scaleInReport?.can_cancel ? (
                <Button
                  disabled={!canWriteScaleIn || scaleInAction === "cancel"}
                  onClick={() => setScaleInConfirmAction("cancel")}
                  size="sm"
                  variant="outline"
                >
                  {intl.formatMessage({ id: "nodes.scaleIn.cancel" })}
                </Button>
              ) : null}
            </div>
          )}
          description={intl.formatMessage(
            { id: "nodes.scaleIn.description" },
            { id: scaleInNodeId },
          )}
          title={intl.formatMessage({ id: "nodes.scaleIn.title" })}
        >
          {scaleInAction === "plan" && !scaleInReport ? (
            <ResourceState kind="loading" title={intl.formatMessage({ id: "nodes.scaleIn.title" })} />
          ) : null}
          {!canWriteScaleIn ? (
            <div className="mb-3 rounded-lg border border-border bg-muted/30 px-4 py-3 text-sm text-muted-foreground">
              {intl.formatMessage({ id: "nodes.scaleIn.permissionRequired" })}
            </div>
          ) : null}
          {scaleInError ? <p className="mb-3 text-sm text-destructive">{scaleInError}</p> : null}
          {scaleInReport ? <ScaleInReportView intl={intl} report={scaleInReport} /> : null}
        </SectionCard>
      ) : null}

      <DetailSheet
        description={
          detail
            ? intl.formatMessage({ id: "nodes.detailDescriptionValue" }, { value: detail.addr })
            : intl.formatMessage({ id: "nodes.detailDescriptionFallback" })
        }
        footer={
          detail ? (
            <div className="flex items-center justify-end gap-2">
              <Button
                disabled={
                  nodeHealthStatus(detail) === "draining"
                    ? !canResumeNode(detail, canWriteNodes)
                    : !canDrainNode(detail, canWriteNodes)
                }
                onClick={() => {
                  setPendingAction(nodeHealthStatus(detail) === "draining" ? "resume" : "drain")
                  setActionError("")
                }}
                size="sm"
              >
                {nodeHealthStatus(detail) === "draining"
                  ? intl.formatMessage({ id: "nodes.resumeNode" })
                  : intl.formatMessage({ id: "nodes.drainNode" })}
              </Button>
            </div>
          ) : null
        }
        onOpenChange={closeDetail}
        open={selectedNodeId !== null}
        title={
          detail
            ? intl.formatMessage({ id: "nodes.detailTitleValue" }, { id: detail.node_id })
            : intl.formatMessage({ id: "nodes.detailTitleFallback" })
        }
      >
        {detailLoading ? (
          <ResourceState kind="loading" title={intl.formatMessage({ id: "nodes.detailTitleFallback" })} />
        ) : null}
        {!detailLoading && detailError ? (
          <ResourceState
            kind={mapErrorKind(detailError)}
            onRetry={() => {
              if (selectedNodeId) {
                void loadNodeDetail(selectedNodeId)
              }
            }}
            title={intl.formatMessage({ id: "nodes.detailTitleFallback" })}
          />
        ) : null}
        {!detailLoading && !detailError && detail ? (
          <div className="space-y-4">
            <KeyValueList
              items={[
                { label: intl.formatMessage({ id: "nodes.detail.address" }), value: detail.addr },
                {
                  label: intl.formatMessage({ id: "nodes.detail.status" }),
                  value: <StatusBadge value={nodeHealthStatus(detail)} />,
                },
                {
                  label: intl.formatMessage({ id: "nodes.detail.membershipRole" }),
                  value: nodeMembershipRole(detail),
                },
                {
                  label: intl.formatMessage({ id: "nodes.detail.joinState" }),
                  value: nodeJoinState(detail),
                },
                {
                  label: intl.formatMessage({ id: "nodes.detail.schedulable" }),
                  value: nodeSchedulableText(intl, detail),
                },
                {
                  label: intl.formatMessage({ id: "nodes.detail.controllerRole" }),
                  value: detail.controller.role,
                },
                {
                  label: intl.formatMessage({ id: "nodes.detail.controllerVoter" }),
                  value: nodeControllerVoterText(intl, detail),
                },
                {
                  label: intl.formatMessage({ id: "nodes.detail.lastHeartbeat" }),
                  value: formatTimestamp(intl, nodeLastHeartbeat(detail)),
                },
                {
                  label: intl.formatMessage({ id: "nodes.detail.capacityWeight" }),
                  value: detail.capacity_weight,
                },
                {
                  label: intl.formatMessage({ id: "nodes.detail.slotSummary" }),
                  value: nodeSlotSummaryText(intl, detail),
                },
                {
                  label: intl.formatMessage({ id: "nodes.detail.runtime" }),
                  value: nodeRuntimeSummaryText(intl, detail),
                },
                {
                  label: intl.formatMessage({ id: "nodes.detail.hostedIds" }),
                  value: detail.slots.hosted_ids.join(", ") || "-",
                },
                {
                  label: intl.formatMessage({ id: "nodes.detail.leaderIds" }),
                  value: detail.slots.leader_ids.join(", ") || "-",
                },
              ]}
            />
          </div>
        ) : null}
      </DetailSheet>

      <ConfirmDialog
        confirmLabel={intl.formatMessage({ id: "common.confirm" })}
        description={
          pendingAction === "drain"
            ? intl.formatMessage({ id: "nodes.confirmDrainDescription" }, { id: selectedNodeId })
            : intl.formatMessage({ id: "nodes.confirmResumeDescription" }, { id: selectedNodeId })
        }
        error={actionError}
        onConfirm={() => {
          void runAction()
        }}
        onOpenChange={(open) => {
          if (!open) {
            setPendingAction(null)
            setActionError("")
          }
        }}
        open={pendingAction !== null}
        pending={actionPending}
        title={
          pendingAction === "resume"
            ? intl.formatMessage({ id: "nodes.resumeNode" })
            : intl.formatMessage({ id: "nodes.drainNode" })
        }
      />

      <ConfirmDialog
        confirmLabel={intl.formatMessage({ id: "common.confirm" })}
        description={
          scaleInConfirmAction === "cancel"
            ? intl.formatMessage({ id: "nodes.scaleIn.cancelConfirmDescription" }, { id: scaleInNodeId })
            : intl.formatMessage({ id: "nodes.scaleIn.startConfirmDescription" }, { id: scaleInNodeId })
        }
        error={scaleInConfirmAction ? scaleInError : ""}
        onConfirm={() => {
          if (scaleInConfirmAction === "cancel") {
            void cancelScaleIn()
            return
          }
          void startScaleIn()
        }}
        onOpenChange={(open) => {
          if (!open) {
            setScaleInConfirmAction(null)
            setScaleInError("")
          }
        }}
        open={scaleInConfirmAction !== null}
        pending={scaleInAction === scaleInConfirmAction}
        title={
          scaleInConfirmAction === "cancel"
            ? intl.formatMessage({ id: "nodes.scaleIn.cancel" })
            : intl.formatMessage({ id: "nodes.scaleIn.start" })
        }
      />
    </PageContainer>
  )
}
