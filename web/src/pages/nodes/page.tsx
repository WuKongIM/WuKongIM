import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { MoreHorizontalIcon } from "lucide-react"
import { useIntl, type IntlShape } from "react-intl"
import { Link, useSearchParams } from "react-router-dom"

import { useAuthStore } from "@/auth/auth-store"
import { DetailSheet } from "@/components/manager/detail-sheet"
import { ConfirmDialog } from "@/components/manager/confirm-dialog"
import { KeyValueList } from "@/components/manager/key-value-list"
import { ResourceState } from "@/components/manager/resource-state"
import { StatusBadge } from "@/components/manager/status-badge"
import { Button } from "@/components/ui/button"
import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
import { PageTabs } from "@/components/shell/page-tabs"
import { SectionCard } from "@/components/shell/section-card"
import { ControllerLogsPanel } from "@/pages/controller/page"
import { DynamicNodeLifecycleSheet } from "@/pages/nodes/dynamic-node-lifecycle"
import {
  ManagerApiError,
  activateNode,
  advanceNodeScaleIn,
  advanceNodeSlotMoveOut,
  getNode,
  getNodeOnboardingStatus,
  getNodes,
  promoteControllerVoter,
  removeNodeAfterScaleIn,
  setNodeScaleInDrain,
  startNodeOnboarding,
  startNodeScaleIn,
} from "@/lib/manager-api"
import type {
  ManagerNode,
  ManagerNodeDetailResponse,
  ManagerNodesResponse,
} from "@/lib/manager-api.types"

type NodesState = {
  nodes: ManagerNodesResponse | null
  loading: boolean
  refreshing: boolean
  error: Error | null
}

type RowLifecycleAction =
  | "activate"
  | "onboard"
  | "slot-move-out"
  | "scale-in-start"
  | "scale-in-drain"
  | "scale-in-advance"
  | "scale-in-remove"

type RowActionTarget = {
  action: RowLifecycleAction
  node: ManagerNode
}

type RowOnboardingStatusState = {
  loading: boolean
  totalActive: number
  error: string | null
}

const tabs = [
  { id: "list", labelMessageId: "nodes.tabs.list" },
  { id: "logs", labelMessageId: "nodes.tabs.logs" },
] as const

type NodeClusterTab = (typeof tabs)[number]["id"]

function normalizeTab(value: string | null): NodeClusterTab {
  return tabs.some((tab) => tab.id === value) ? (value as NodeClusterTab) : "list"
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

function hasPermission(permissions: { resource: string; actions: string[] }[], resource: string, action: string) {
  return permissions.some((permission) => {
    if (permission.resource !== resource && permission.resource !== "*") {
      return false
    }
    return permission.actions.includes(action) || permission.actions.includes("*")
  })
}

function formatBooleanValue(intl: IntlShape, value: boolean) {
  return intl.formatMessage({ id: value ? "nodes.boolean.yes" : "nodes.boolean.no" })
}

function parsePositiveSafeInteger(value: string, defaultValue: number) {
  const trimmed = value.trim()
  if (!trimmed) {
    return defaultValue
  }
  const parsed = Number(trimmed)
  return Number.isSafeInteger(parsed) && parsed > 0 ? parsed : null
}

function nodeHealthStatus(node: ManagerNode) {
  const health = node.health
  if (!health) {
    return node.status
  }
  if (health.freshness && health.freshness !== "fresh") {
    return health.freshness
  }
  if (health.runtime_ready === false) {
    return "not_ready"
  }
  return health.status ?? node.status
}

function nodeLastHeartbeat(node: ManagerNode) {
  return node.health?.last_heartbeat_at ?? node.last_heartbeat_at
}

function nodeHealthEvidenceText(intl: IntlShape, node: ManagerNode) {
  const health = node.health
  if (!health?.freshness) {
    return null
  }
  return intl.formatMessage(
    { id: "nodes.healthEvidence" },
    {
      freshness: health.freshness,
      runtimeReady: formatBooleanValue(intl, health.runtime_ready ?? false),
      age: health.report_age_ms ?? 0,
      ttl: health.report_ttl_ms ?? 0,
    },
  )
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

function hasControllerRaftSummary(node: ManagerNode) {
  return Boolean(node.controller.raft_health && node.controller.raft_health !== "unknown")
}

function hasControllerRaftWatermark(node: ManagerNode) {
  return hasControllerRaftSummary(node) && (
    node.controller.first_index !== undefined &&
    node.controller.applied_index !== undefined &&
    node.controller.snapshot_index !== undefined
  )
}

function nodeControllerRaftHealth(intl: IntlShape, node: ManagerNode) {
  return hasControllerRaftSummary(node)
    ? <StatusBadge value={node.controller.raft_health!} />
    : intl.formatMessage({ id: "nodes.controllerRaftUnavailable" })
}

function nodeControllerRaftWatermark(intl: IntlShape, node: ManagerNode) {
  if (!hasControllerRaftWatermark(node)) {
    return intl.formatMessage({ id: "nodes.controllerRaftUnavailable" })
  }
  return intl.formatMessage(
    { id: "nodes.controllerRaftWatermark" },
    {
      first: node.controller.first_index ?? 0,
      applied: node.controller.applied_index ?? 0,
      snapshot: node.controller.snapshot_index ?? 0,
    },
  )
}

function isUnhealthyNode(node: ManagerNode) {
  const healthStatus = nodeHealthStatus(node)
  const slots = nodeSlotSummary(node)
  return (
    healthStatus !== "alive" ||
    nodeJoinState(node) !== "active" ||
    !node.membership?.schedulable ||
    slots.quorumLostCount > 0 ||
    slots.unreportedCount > 0 ||
    node.runtime?.unknown === true ||
    node.runtime?.draining === true
  )
}

function canPromoteControllerVoter(node: ManagerNode) {
  return node.actions?.can_promote_controller_voter === true && node.controller.voter !== true
}

function nodeRuntimeTotals(nodes: ManagerNode[]) {
  return nodes.reduce(
    (totals, node) => ({
      gatewaySessions: totals.gatewaySessions + (node.runtime?.gateway_sessions ?? 0),
      activeOnline: totals.activeOnline + (node.runtime?.active_online ?? 0),
      unknownRuntime: totals.unknownRuntime + (node.runtime?.unknown ? 1 : 0),
    }),
    { gatewaySessions: 0, activeOnline: 0, unknownRuntime: 0 },
  )
}

function NodeSummaryCell({ label, value }: { label: string; value: number | string }) {
  return (
    <div className="border-b border-border px-1 py-3 sm:px-3">
      <div className="text-xs text-muted-foreground">{label}</div>
      <div className="mt-1 font-mono text-2xl font-medium tabular-nums text-foreground">{value}</div>
    </div>
  )
}

function controllerRaftPath(nodeId: number) {
  return `/cluster/diagnostics?tab=controller-logs&node_id=${nodeId}`
}

function canMoveSlotsIn(node: ManagerNode) {
  return nodeJoinState(node) === "active" &&
    node.membership?.schedulable === true &&
    (node.actions?.can_move_slots_in === true || node.actions?.can_onboard === true)
}

function canOnboardNode(node: ManagerNode) {
  return canMoveSlotsIn(node)
}

function canMoveSlotsOut(node: ManagerNode) {
  return nodeJoinState(node) === "active" && node.actions?.can_move_slots_out === true
}

function canStartScaleIn(node: ManagerNode) {
  return nodeJoinState(node) === "active" && node.actions?.can_scale_in === true
}

function canAdvanceScaleIn(node: ManagerNode) {
  return nodeJoinState(node) === "leaving" && node.actions?.can_scale_in === true
}

function canShowRowActionMenu(node: ManagerNode) {
  return canStartScaleIn(node) || canAdvanceScaleIn(node)
}

function rowActionConfirmTitleId(action: RowLifecycleAction) {
  switch (action) {
    case "onboard":
      return "nodes.rowAction.confirm.onboard.title"
    case "slot-move-out":
      return "nodes.rowAction.confirm.moveOut.title"
    case "scale-in-start":
      return "nodes.lifecycle.confirm.markLeaving.title"
    case "scale-in-drain":
      return "nodes.lifecycle.confirm.drain.title"
    case "scale-in-advance":
      return "nodes.lifecycle.confirm.advanceScaleIn.title"
    case "scale-in-remove":
      return "nodes.lifecycle.confirm.remove.title"
    case "activate":
      return "nodes.lifecycle.activate"
  }
}

function rowActionConfirmDescriptionId(action: RowLifecycleAction) {
  switch (action) {
    case "onboard":
      return "nodes.rowAction.confirm.onboard.description"
    case "slot-move-out":
      return "nodes.rowAction.confirm.moveOut.description"
    case "scale-in-start":
      return "nodes.lifecycle.confirm.markLeaving.description"
    case "scale-in-drain":
      return "nodes.lifecycle.confirm.drain.description"
    case "scale-in-advance":
      return "nodes.lifecycle.confirm.advanceScaleIn.description"
    case "scale-in-remove":
      return "nodes.lifecycle.confirm.remove.description"
    case "activate":
      return "nodes.lifecycle.activate"
  }
}

function controllerRaftLink(intl: IntlShape, nodeId: number) {
  return (
    <Button asChild className="h-auto px-0" size="xs" variant="link">
      <Link
        aria-label={intl.formatMessage({ id: "nodes.controllerRaftOpenForNode" }, { id: nodeId })}
        to={controllerRaftPath(nodeId)}
      >
        {intl.formatMessage({ id: "nodes.controllerRaftOpen" })}
      </Link>
    </Button>
  )
}

export function NodeClusterListPanel() {
  const intl = useIntl()
  const permissions = useAuthStore((store) => store.permissions)
  const canWriteNodes = useMemo(() => hasPermission(permissions, "cluster.node", "w"), [permissions])
  const canWriteController = useMemo(() => hasPermission(permissions, "cluster.controller", "w"), [permissions])
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
  const [lifecycleOpen, setLifecycleOpen] = useState(false)
  const [rowActionPending, setRowActionPending] = useState<RowActionTarget | null>(null)
  const [rowConfirmAction, setRowConfirmAction] = useState<RowActionTarget | null>(null)
  const [rowActionError, setRowActionError] = useState<Error | null>(null)
  const [rowOnboardingMoves, setRowOnboardingMoves] = useState("1")
  const [onboardingStatusByNode, setOnboardingStatusByNode] = useState<Record<number, RowOnboardingStatusState>>({})
  const onboardingStatusRequestSeq = useRef(0)
  const [promoteTarget, setPromoteTarget] = useState<ManagerNode | null>(null)
  const [promotePending, setPromotePending] = useState(false)
  const [promoteError, setPromoteError] = useState<string | undefined>(undefined)
  const nodes = state.nodes?.items ?? []
  const aliveCount = nodes.filter((node) => nodeHealthStatus(node) === "alive").length
  const unhealthyCount = nodes.filter(isUnhealthyNode).length
  const drainingCount = nodes.filter((node) => node.runtime?.draining === true || nodeHealthStatus(node) === "draining").length
  const schedulableCount = nodes.filter((node) => node.membership?.schedulable).length
  const controllerVoterCount = nodes.filter((node) => node.controller.voter).length

  const loadOnboardingStatuses = useCallback(async (nodes: ManagerNode[]) => {
    const requestSeq = onboardingStatusRequestSeq.current + 1
    onboardingStatusRequestSeq.current = requestSeq
    const ids = nodes.filter(canOnboardNode).map((node) => node.node_id)
    if (ids.length === 0) {
      setOnboardingStatusByNode({})
      return
    }
    setOnboardingStatusByNode((current) => {
      const next: Record<number, RowOnboardingStatusState> = {}
      for (const id of ids) {
        next[id] = {
          loading: true,
          totalActive: current[id]?.totalActive ?? 0,
          error: null,
        }
      }
      return next
    })
    const results = await Promise.all(ids.map(async (id) => {
      try {
        const status = await getNodeOnboardingStatus(id)
        return { id, totalActive: status.summary.total_active, error: null }
      } catch (error) {
        const message = error instanceof Error ? error.message : "node onboarding status failed"
        return { id, totalActive: 0, error: message }
      }
    }))
    if (onboardingStatusRequestSeq.current !== requestSeq) {
      return
    }
    setOnboardingStatusByNode(() => {
      const next: Record<number, RowOnboardingStatusState> = {}
      for (const result of results) {
        next[result.id] = {
          loading: false,
          totalActive: result.totalActive,
          error: result.error,
        }
      }
      return next
    })
  }, [])

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
      void loadOnboardingStatuses(nodes.items)
      return nodes
    } catch (error) {
      onboardingStatusRequestSeq.current += 1
      setOnboardingStatusByNode({})
      setState({
        nodes: null,
        loading: false,
        refreshing: false,
        error: error instanceof Error ? error : new Error("node request failed"),
      })
      return null
    }
  }, [loadOnboardingStatuses])

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
  }, [])

  const openJoinLifecycle = useCallback(() => {
    setLifecycleOpen(true)
  }, [])

  const refreshAfterLifecycleAction = useCallback(async () => {
    await loadNodes(true)
  }, [loadNodes])

  const runRowLifecycleAction = useCallback(async (target: RowActionTarget) => {
    if (!canWriteNodes) {
      return false
    }
    setRowActionError(null)
    setRowActionPending(target)
    try {
      switch (target.action) {
        case "activate":
          await activateNode(target.node.node_id)
          break
        case "onboard":
          {
            const maxSlotMoves = parsePositiveSafeInteger(rowOnboardingMoves, 1)
            if (maxSlotMoves === null) {
              setRowActionError(new Error(intl.formatMessage({ id: "nodes.rowAction.invalidMoveCount" })))
              return false
            }
            await startNodeOnboarding(target.node.node_id, { maxSlotMoves })
          }
          break
        case "slot-move-out":
          {
            const maxSlotMoves = parsePositiveSafeInteger(rowOnboardingMoves, 1)
            if (maxSlotMoves === null) {
              setRowActionError(new Error(intl.formatMessage({ id: "nodes.rowAction.invalidMoveCount" })))
              return false
            }
            await advanceNodeSlotMoveOut(target.node.node_id, { maxSlotMoves })
          }
          break
        case "scale-in-start":
          await startNodeScaleIn(target.node.node_id)
          break
        case "scale-in-drain":
          await setNodeScaleInDrain(target.node.node_id, { draining: true })
          break
        case "scale-in-advance":
          await advanceNodeScaleIn(target.node.node_id, { maxSlotMoves: 1 })
          break
        case "scale-in-remove":
          await removeNodeAfterScaleIn(target.node.node_id)
          break
      }
      await loadNodes(true)
      return true
    } catch (error) {
      setRowActionError(error instanceof Error ? error : new Error("node lifecycle action failed"))
      return false
    } finally {
      setRowActionPending(null)
    }
  }, [canWriteNodes, intl, loadNodes, rowOnboardingMoves])

  const openRowConfirmAction = useCallback((target: RowActionTarget) => {
    setRowActionError(null)
    if (target.action === "onboard" || target.action === "slot-move-out") {
      setRowOnboardingMoves("1")
    }
    setRowConfirmAction(target)
  }, [])

  const confirmRowAction = useCallback(async () => {
    if (!rowConfirmAction) {
      return
    }
    const completed = await runRowLifecycleAction(rowConfirmAction)
    if (completed) {
      setRowConfirmAction(null)
    }
  }, [rowConfirmAction, runRowLifecycleAction])

  const openPromoteControllerVoter = useCallback((node: ManagerNode) => {
    setPromoteTarget(node)
    setPromoteError(undefined)
  }, [])

  const closePromoteControllerVoter = useCallback((open: boolean) => {
    if (open) {
      return
    }
    setPromoteTarget(null)
    setPromoteError(undefined)
  }, [])

  const confirmPromoteControllerVoter = useCallback(async () => {
    if (!promoteTarget || promotePending || !canWriteController) {
      return
    }
    setPromotePending(true)
    setPromoteError(undefined)
    try {
      await promoteControllerVoter(promoteTarget.node_id)
      setPromoteTarget(null)
      setPromoteError(undefined)
      await loadNodes(true)
    } catch (error) {
      setPromoteError(error instanceof Error ? error.message : "promote Controller voter failed")
    } finally {
      setPromotePending(false)
    }
  }, [canWriteController, loadNodes, promotePending, promoteTarget])

  return (
    <>
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h1 className="text-xl font-semibold tracking-tight text-foreground">
            {intl.formatMessage({ id: "nav.nodes.title" })}
          </h1>
          <p className="mt-1 text-sm text-muted-foreground">
            {state.nodes
              ? intl.formatMessage({ id: "nodes.totalValue" }, { total: state.nodes.total })
              : intl.formatMessage({ id: "nodes.totalPending" })}
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Button onClick={openJoinLifecycle} size="sm" variant="outline">
            {intl.formatMessage({ id: "nodes.lifecycle.addNode" })}
          </Button>
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
        </div>
      </div>

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
      {rowActionError && !state.error ? (
        <div className="rounded-lg border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive" role="alert">
          {rowActionError.message}
        </div>
      ) : null}
      {!state.loading && !state.error && state.nodes ? (
        <>
          <div
            className="grid gap-0 border-y border-border sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-6"
            data-testid="nodes-summary-strip"
          >
            <NodeSummaryCell label={intl.formatMessage({ id: "nodes.metric.total" })} value={nodes.length} />
            <NodeSummaryCell label={intl.formatMessage({ id: "nodes.metric.alive" })} value={aliveCount} />
            <NodeSummaryCell label={intl.formatMessage({ id: "nodes.metric.unhealthy" })} value={unhealthyCount} />
            <NodeSummaryCell label={intl.formatMessage({ id: "nodes.metric.draining" })} value={drainingCount} />
            <NodeSummaryCell label={intl.formatMessage({ id: "nodes.metric.schedulable" })} value={schedulableCount} />
            <NodeSummaryCell label={intl.formatMessage({ id: "nodes.metric.controllerVoters" })} value={controllerVoterCount} />
          </div>
          <div className="border border-border bg-card">
            {state.nodes.items.length > 0 ? (
              <div className="overflow-x-auto">
                <table aria-label={intl.formatMessage({ id: "nav.nodes.title" })} className="w-full border-collapse">
                  <thead className="border-b border-border bg-background text-left text-xs uppercase tracking-[0.14em] text-muted-foreground">
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
                    const healthEvidence = nodeHealthEvidenceText(intl, node)
                    const currentJoinState = nodeJoinState(node)
                    const rowActionDisabled = !canWriteNodes || rowActionPending !== null
                    const pendingActivate = rowActionPending?.node.node_id === node.node_id &&
                      rowActionPending.action === "activate"
                    const pendingOnboard = rowActionPending?.node.node_id === node.node_id &&
                      rowActionPending.action === "onboard"
                    const pendingMoveOut = rowActionPending?.node.node_id === node.node_id &&
                      rowActionPending.action === "slot-move-out"
                    const pendingAdvanceScaleIn = rowActionPending?.node.node_id === node.node_id &&
                      rowActionPending.action === "scale-in-advance"
                    const onboardingStatus = onboardingStatusByNode[node.node_id]
                    const onboardingInProgress = (onboardingStatus?.totalActive ?? 0) > 0
                    const onboardingStatusLoading = onboardingStatus?.loading === true
                    return (
                      <tr className="border-t border-border align-top hover:bg-muted/45" key={node.node_id}>
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
                          {healthEvidence ? (
                            <div className="mt-1 text-xs text-muted-foreground">{healthEvidence}</div>
                          ) : null}
                        </td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">
                          <div className="font-medium text-foreground">{node.controller.role}</div>
                          <div className="mt-1 text-xs">{nodeControllerVoterText(intl, node)}</div>
                          {hasControllerRaftSummary(node) ? (
                            <div className="mt-2">
                              <StatusBadge value={node.controller.raft_health!} />
                            </div>
                          ) : null}
                          {hasControllerRaftWatermark(node) ? (
                            <div className="mt-1 text-xs">{nodeControllerRaftWatermark(intl, node)}</div>
                          ) : null}
                          <div className="mt-1">{controllerRaftLink(intl, node.node_id)}</div>
                        </td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">
                          {nodeSlotSummaryText(intl, node)}
                        </td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">
                          {nodeRuntimeSummaryText(intl, node)}
                        </td>
                        <td className="px-3 py-3 text-sm text-foreground">
                          <div className="flex items-center gap-2">
                            {currentJoinState === "joining" ? (
                              <Button
                                aria-label={intl.formatMessage(
                                  { id: "nodes.rowAction.activateForNode" },
                                  { id: node.node_id },
                                )}
                                disabled={rowActionDisabled}
                                onClick={() => {
                                  void runRowLifecycleAction({ action: "activate", node })
                                }}
                                size="sm"
                              >
                                {pendingActivate
                                  ? intl.formatMessage({ id: "common.refreshing" })
                                  : intl.formatMessage({ id: "nodes.rowAction.activate" })}
                              </Button>
                            ) : null}
                            {canOnboardNode(node) ? (
                              <Button
                                aria-label={intl.formatMessage(
                                  { id: "nodes.rowAction.onboardForNode" },
                                  { id: node.node_id },
                                )}
                                disabled={rowActionDisabled || onboardingStatusLoading || onboardingInProgress}
                                onClick={() => openRowConfirmAction({ action: "onboard", node })}
                                size="sm"
                              >
                                {onboardingInProgress
                                  ? intl.formatMessage({ id: "nodes.rowAction.onboardingInProgress" })
                                  : pendingOnboard || onboardingStatusLoading
                                    ? intl.formatMessage({ id: "common.refreshing" })
                                    : intl.formatMessage({ id: "nodes.rowAction.onboard" })}
                              </Button>
                            ) : null}
                            {canMoveSlotsOut(node) ? (
                              <Button
                                aria-label={intl.formatMessage(
                                  { id: "nodes.rowAction.moveOutForNode" },
                                  { id: node.node_id },
                                )}
                                disabled={rowActionDisabled}
                                onClick={() => openRowConfirmAction({ action: "slot-move-out", node })}
                                size="sm"
                                variant="outline"
                              >
                                {pendingMoveOut
                                  ? intl.formatMessage({ id: "common.refreshing" })
                                  : intl.formatMessage({ id: "nodes.rowAction.moveOut" })}
                              </Button>
                            ) : null}
                            {canAdvanceScaleIn(node) ? (
                              <Button
                                aria-label={intl.formatMessage(
                                  { id: "nodes.rowAction.advanceScaleInForNode" },
                                  { id: node.node_id },
                                )}
                                disabled={rowActionDisabled}
                                onClick={() => openRowConfirmAction({ action: "scale-in-advance", node })}
                                size="sm"
                              >
                                {pendingAdvanceScaleIn
                                  ? intl.formatMessage({ id: "common.refreshing" })
                                  : intl.formatMessage({ id: "nodes.rowAction.advanceScaleIn" })}
                              </Button>
                            ) : null}
                            {canShowRowActionMenu(node) ? (
                              <details className="relative">
                                <summary
                                  aria-label={intl.formatMessage(
                                    { id: "nodes.rowAction.moreForNode" },
                                    { id: node.node_id },
                                  )}
                                  className="inline-flex size-7 cursor-pointer list-none items-center justify-center rounded-lg border border-border bg-background text-muted-foreground transition-colors hover:bg-muted hover:text-foreground [&::-webkit-details-marker]:hidden"
                                  role="button"
                                >
                                  <MoreHorizontalIcon aria-hidden="true" className="size-4" />
                                </summary>
                                <div className="absolute right-0 z-20 mt-1 flex min-w-44 flex-col gap-1 rounded-lg border border-border bg-popover p-1 text-popover-foreground shadow-lg">
                                  {canStartScaleIn(node) ? (
                                    <Button
                                      aria-label={intl.formatMessage(
                                        { id: "nodes.rowAction.startScaleInForNode" },
                                        { id: node.node_id },
                                      )}
                                      className="w-full justify-start"
                                      disabled={rowActionDisabled}
                                      onClick={() => openRowConfirmAction({ action: "scale-in-start", node })}
                                      size="sm"
                                      type="button"
                                      variant="ghost"
                                    >
                                      {intl.formatMessage({ id: "nodes.rowAction.startScaleIn" })}
                                    </Button>
                                  ) : null}
                                  {canAdvanceScaleIn(node) ? (
                                    <>
                                      <Button
                                        aria-label={intl.formatMessage(
                                          { id: "nodes.rowAction.enableDrainForNode" },
                                          { id: node.node_id },
                                        )}
                                        className="w-full justify-start"
                                        disabled={rowActionDisabled}
                                        onClick={() => openRowConfirmAction({ action: "scale-in-drain", node })}
                                        size="sm"
                                        type="button"
                                        variant="ghost"
                                      >
                                        {intl.formatMessage({ id: "nodes.rowAction.enableDrain" })}
                                      </Button>
                                      <Button
                                        aria-label={intl.formatMessage(
                                          { id: "nodes.rowAction.removeForNode" },
                                          { id: node.node_id },
                                        )}
                                        className="w-full justify-start"
                                        disabled={rowActionDisabled}
                                        onClick={() => openRowConfirmAction({ action: "scale-in-remove", node })}
                                        size="sm"
                                        type="button"
                                        variant="ghost"
                                      >
                                        {intl.formatMessage({ id: "nodes.rowAction.remove" })}
                                      </Button>
                                    </>
                                  ) : null}
                                </div>
                              </details>
                            ) : null}
                            {canPromoteControllerVoter(node) ? (
                              <Button
                                aria-label={intl.formatMessage(
                                  { id: "nodes.action.promoteControllerVoterForNode" },
                                  { id: node.node_id },
                                )}
                                disabled={!canWriteController}
                                onClick={() => openPromoteControllerVoter(node)}
                                size="sm"
                                variant="outline"
                              >
                                {intl.formatMessage({ id: "nodes.action.promoteControllerVoter" })}
                              </Button>
                            ) : null}
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
          </div>
        </>
      ) : null}


      <DynamicNodeLifecycleSheet
        mode="join"
        node={null}
        onCompleted={refreshAfterLifecycleAction}
        onOpenChange={setLifecycleOpen}
        open={lifecycleOpen}
      />

      <ConfirmDialog
        confirmLabel={intl.formatMessage({ id: "common.confirm" })}
        description={rowConfirmAction
          ? intl.formatMessage(
            { id: rowActionConfirmDescriptionId(rowConfirmAction.action) },
            { id: rowConfirmAction.node.node_id },
          )
          : ""}
        error={rowActionError?.message}
        onConfirm={() => {
          void confirmRowAction()
        }}
        onOpenChange={(nextOpen) => {
          if (!nextOpen) {
            setRowConfirmAction(null)
            setRowActionError(null)
          }
        }}
        open={rowConfirmAction !== null}
        pending={rowActionPending !== null}
        title={rowConfirmAction
          ? intl.formatMessage(
            { id: rowActionConfirmTitleId(rowConfirmAction.action) },
            { id: rowConfirmAction.node.node_id },
          )
          : ""}
      >
        {rowConfirmAction?.action === "onboard" || rowConfirmAction?.action === "slot-move-out" ? (
          <label className="block text-sm font-medium text-foreground">
            {intl.formatMessage({ id: "nodes.rowAction.moveCountLabel" })}
            <input
              aria-label={intl.formatMessage({ id: "nodes.rowAction.moveCountLabel" })}
              className="mt-1 h-9 w-full rounded-md border border-border bg-background px-3 text-sm"
              min={1}
              onChange={(event) => setRowOnboardingMoves(event.target.value)}
              type="number"
              value={rowOnboardingMoves}
            />
            <span className="mt-1 block text-xs font-normal text-muted-foreground">
              {intl.formatMessage({ id: "nodes.rowAction.moveCountHelp" })}
            </span>
          </label>
        ) : null}
      </ConfirmDialog>

      <ConfirmDialog
        confirmLabel={intl.formatMessage({ id: "nodes.promoteControllerVoter.confirm" })}
        description={intl.formatMessage({ id: "nodes.promoteControllerVoter.confirmBody" })}
        error={promoteError}
        onConfirm={() => {
          void confirmPromoteControllerVoter()
        }}
        onOpenChange={closePromoteControllerVoter}
        open={promoteTarget !== null}
        pending={promotePending}
        title={intl.formatMessage({ id: "nodes.promoteControllerVoter.confirmTitle" })}
      />

      <DetailSheet
        description={
          detail
            ? intl.formatMessage({ id: "nodes.detailDescriptionValue" }, { value: detail.addr })
            : intl.formatMessage({ id: "nodes.detailDescriptionFallback" })
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
          <div className="rounded-md border border-border bg-card p-3" data-node-surface="detail">
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
                  label: intl.formatMessage({ id: "nodes.detail.controllerRaftHealth" }),
                  value: nodeControllerRaftHealth(intl, detail),
                },
                {
                  label: intl.formatMessage({ id: "nodes.detail.controllerRaftWatermark" }),
                  value: nodeControllerRaftWatermark(intl, detail),
                },
                {
                  label: intl.formatMessage({ id: "nodes.detail.controllerRaftLink" }),
                  value: controllerRaftLink(intl, detail.node_id),
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
    </>
  )
}


export function NodeClusterOverviewPanel() {
  const intl = useIntl()
  const [state, setState] = useState<NodesState>({
    nodes: null,
    loading: true,
    refreshing: false,
    error: null,
  })

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
        error: error instanceof Error ? error : new Error("node overview request failed"),
      })
    }
  }, [])

  useEffect(() => {
    void loadNodes(false)
  }, [loadNodes])

  const nodes = state.nodes?.items ?? []
  const aliveCount = nodes.filter((node) => nodeHealthStatus(node) === "alive").length
  const unhealthyCount = nodes.filter(isUnhealthyNode).length
  const drainingCount = nodes.filter((node) => node.runtime?.draining === true || nodeHealthStatus(node) === "draining").length
  const schedulableCount = nodes.filter((node) => node.membership?.schedulable).length
  const controllerVoterCount = nodes.filter((node) => node.controller.voter).length
  const runtimeTotals = nodeRuntimeTotals(nodes)

  return (
    <>
      <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div>
          <h2 className="text-xl font-semibold tracking-tight text-foreground">
            {intl.formatMessage({ id: "nodes.overview.title" })}
          </h2>
          <p className="mt-1 max-w-3xl text-sm leading-6 text-muted-foreground">
            {intl.formatMessage({ id: "nav.nodes.description" })}
          </p>
        </div>
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
      </div>
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
        <>
          <div
            className="grid gap-0 border-y border-border sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-6"
            data-testid="nodes-summary-strip"
          >
            <NodeSummaryCell label={intl.formatMessage({ id: "nodes.metric.total" })} value={nodes.length} />
            <NodeSummaryCell label={intl.formatMessage({ id: "nodes.metric.alive" })} value={aliveCount} />
            <NodeSummaryCell label={intl.formatMessage({ id: "nodes.metric.unhealthy" })} value={unhealthyCount} />
            <NodeSummaryCell label={intl.formatMessage({ id: "nodes.metric.draining" })} value={drainingCount} />
            <NodeSummaryCell label={intl.formatMessage({ id: "nodes.metric.schedulable" })} value={schedulableCount} />
            <NodeSummaryCell label={intl.formatMessage({ id: "nodes.metric.controllerVoters" })} value={controllerVoterCount} />
          </div>

          <section className="grid gap-4 xl:grid-cols-[0.8fr_1.2fr]">
            <SectionCard
              description={intl.formatMessage({ id: "nodes.unhealthy.summary" }, { count: unhealthyCount })}
              title={intl.formatMessage({ id: "nodes.unhealthy.title" })}
            >
              <div
                className="rounded-md border border-border bg-muted/30 px-3 py-3 text-sm leading-6 text-muted-foreground"
                data-node-surface="overview-unhealthy"
              >
                {intl.formatMessage(
                  { id: "nodes.unhealthy.breakdown" },
                  {
                    unhealthy: unhealthyCount,
                    runtimeUnknown: runtimeTotals.unknownRuntime,
                    draining: drainingCount,
                  },
                )}
              </div>
            </SectionCard>

            <SectionCard title={intl.formatMessage({ id: "nodes.overview.runtime.title" })}>
              <div className="grid gap-3 sm:grid-cols-3" data-node-surface="overview-runtime">
                <div className="rounded-md border border-border bg-muted/20 px-3 py-3">
                  <div className="text-xs font-medium uppercase tracking-[0.14em] text-muted-foreground">
                    {intl.formatMessage({ id: "nodes.metric.gatewaySessions" })}
                  </div>
                  <div className="mt-2 text-2xl font-semibold text-foreground">{runtimeTotals.gatewaySessions}</div>
                </div>
                <div className="rounded-md border border-border bg-muted/20 px-3 py-3">
                  <div className="text-xs font-medium uppercase tracking-[0.14em] text-muted-foreground">
                    {intl.formatMessage({ id: "nodes.metric.activeOnline" })}
                  </div>
                  <div className="mt-2 text-2xl font-semibold text-foreground">{runtimeTotals.activeOnline}</div>
                </div>
                <div className="rounded-md border border-border bg-muted/20 px-3 py-3">
                  <div className="text-xs font-medium uppercase tracking-[0.14em] text-muted-foreground">
                    {intl.formatMessage({ id: "nodes.metric.runtimeUnknown" })}
                  </div>
                  <div className="mt-2 text-2xl font-semibold text-foreground">{runtimeTotals.unknownRuntime}</div>
                </div>
              </div>
            </SectionCard>
          </section>
        </>
      ) : null}
    </>
  )
}

export function NodeClusterUnhealthyPanel() {
  const intl = useIntl()
  const [state, setState] = useState<NodesState>({
    nodes: null,
    loading: true,
    refreshing: false,
    error: null,
  })

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
        error: error instanceof Error ? error : new Error("node unhealthy request failed"),
      })
    }
  }, [])

  useEffect(() => {
    void loadNodes(false)
  }, [loadNodes])

  const rows = state.nodes?.items.filter(isUnhealthyNode) ?? []

  return (
    <>
      <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div>
          <h2 className="text-xl font-semibold tracking-tight text-foreground">
            {intl.formatMessage({ id: "nodes.unhealthy.title" })}
          </h2>
          <p className="mt-1 max-w-3xl text-sm leading-6 text-muted-foreground">
            {intl.formatMessage({ id: "nodes.unhealthy.description" })}
          </p>
        </div>
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
      </div>
      {state.loading ? <ResourceState kind="loading" title={intl.formatMessage({ id: "nodes.unhealthy.title" })} /> : null}
      {!state.loading && state.error ? (
        <ResourceState
          kind={mapErrorKind(state.error)}
          onRetry={() => {
            void loadNodes(false)
          }}
          title={intl.formatMessage({ id: "nodes.unhealthy.title" })}
        />
      ) : null}
      {!state.loading && !state.error && state.nodes ? (
        <div className="rounded-md border border-border bg-card p-3 shadow-none" data-node-surface="unhealthy">
          {rows.length > 0 ? (
            <div className="overflow-x-auto rounded-md border border-border" data-node-surface="unhealthy-table">
              <table aria-label={intl.formatMessage({ id: "nodes.unhealthy.title" })} className="w-full border-collapse text-sm">
                <thead className="bg-muted/40 text-left text-xs uppercase tracking-[0.14em] text-muted-foreground">
                  <tr>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "nodes.table.node" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "nodes.table.address" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "nodes.table.health" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "nodes.table.membership" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "nodes.table.slots" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "nodes.table.runtime" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "nodes.unhealthy.table.lastHeartbeat" })}</th>
                  </tr>
                </thead>
                <tbody>
                  {rows.map((node) => {
                    const slots = nodeSlotSummary(node)
                    return (
                      <tr className="border-t border-border" key={node.node_id}>
                        <td className="px-3 py-3 text-sm font-medium text-foreground">
                          <div>{node.node_id}</div>
                          {node.name ? <div className="mt-1 text-xs font-normal text-muted-foreground">{node.name}</div> : null}
                        </td>
                        <td className="px-3 py-3 text-sm text-foreground">{node.addr}</td>
                        <td className="px-3 py-3 text-sm text-foreground"><StatusBadge value={nodeHealthStatus(node)} /></td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">
                          <div className="font-medium text-foreground">{nodeMembershipRole(node)}</div>
                          <div className="mt-1">{nodeJoinState(node)}</div>
                          <div className="mt-1 text-xs">{nodeSchedulableText(intl, node)}</div>
                        </td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">
                          {intl.formatMessage(
                            { id: "nodes.unhealthy.slotValue" },
                            { quorumLost: slots.quorumLostCount, unreported: slots.unreportedCount },
                          )}
                        </td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">{nodeRuntimeSummaryText(intl, node)}</td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">
                          {formatTimestamp(intl, nodeLastHeartbeat(node))}
                        </td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            </div>
          ) : (
            <ResourceState
              description={intl.formatMessage({ id: "nodes.unhealthy.emptyDescription" })}
              kind="empty"
              title={intl.formatMessage({ id: "nodes.unhealthy.emptyTitle" })}
            />
          )}
        </div>
      ) : null}
    </>
  )
}

export function NodesPage() {
  const intl = useIntl()
  const [searchParams, setSearchParams] = useSearchParams()
  const activeTab = normalizeTab(searchParams.get("tab"))

  function setTab(tab: string) {
    const next = new URLSearchParams(searchParams)
    next.set("tab", tab)
    if (tab !== "list") {
      next.delete("panel")
    }
    setSearchParams(next)
  }

  return (
    <PageContainer>
      <PageHeader
        eyebrow={intl.formatMessage({ id: "nav.path.cluster.nodes" })}
        title={intl.formatMessage({ id: "nav.nodes.title" })}
        description={intl.formatMessage({ id: "nav.nodes.description" })}
      />
      <PageTabs
        activeTab={activeTab}
        className="px-0 pt-0"
        tabs={tabs.map((tab) => ({ id: tab.id, label: intl.formatMessage({ id: tab.labelMessageId }) }))}
        onTabChange={setTab}
      />
      {activeTab === "list" ? <NodeClusterListPanel /> : null}
      {activeTab === "logs" ? <ControllerLogsPanel /> : null}
    </PageContainer>
  )
}
