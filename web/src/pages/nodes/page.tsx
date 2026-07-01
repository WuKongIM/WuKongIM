import { useCallback, useEffect, useState } from "react"
import { useIntl, type IntlShape } from "react-intl"
import { Link, useSearchParams } from "react-router-dom"

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
import { DynamicNodeLifecycleSheet, type DynamicNodeLifecycleMode } from "@/pages/nodes/dynamic-node-lifecycle"
import {
  ManagerApiError,
  getNode,
  getNodes,
  promoteControllerVoter,
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

function formatBooleanValue(intl: IntlShape, value: boolean) {
  return intl.formatMessage({ id: value ? "nodes.boolean.yes" : "nodes.boolean.no" })
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

function controllerRaftPath(nodeId: number) {
  return `/cluster/diagnostics?tab=controller-logs&node_id=${nodeId}`
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
  const [lifecycleMode, setLifecycleMode] = useState<DynamicNodeLifecycleMode>("join")
  const [lifecycleNode, setLifecycleNode] = useState<ManagerNode | null>(null)
  const [promoteTarget, setPromoteTarget] = useState<ManagerNode | null>(null)
  const [promotePending, setPromotePending] = useState(false)
  const [promoteError, setPromoteError] = useState<string | undefined>(undefined)

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
      return nodes
    } catch (error) {
      setState({
        nodes: null,
        loading: false,
        refreshing: false,
        error: error instanceof Error ? error : new Error("node request failed"),
      })
      return null
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
  }, [])

  const openJoinLifecycle = useCallback(() => {
    setLifecycleMode("join")
    setLifecycleNode(null)
    setLifecycleOpen(true)
  }, [])

  const openNodeLifecycle = useCallback((node: ManagerNode) => {
    setLifecycleMode("node")
    setLifecycleNode(node)
    setLifecycleOpen(true)
  }, [])

  const refreshAfterLifecycleAction = useCallback(async () => {
    const nodes = await loadNodes(true)
    if (!nodes) {
      setLifecycleNode(null)
      setLifecycleOpen(false)
      return
    }
    if (!lifecycleNode) {
      return
    }
    const nextLifecycleNode = nodes.items.find((node) => node.node_id === lifecycleNode.node_id) ?? null
    setLifecycleNode(nextLifecycleNode)
    if (!nextLifecycleNode) {
      setLifecycleOpen(false)
    }
  }, [lifecycleNode, loadNodes])

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
    if (!promoteTarget || promotePending) {
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
  }, [loadNodes, promotePending, promoteTarget])

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
      {!state.loading && !state.error && state.nodes ? (
        <div className="rounded-xl border border-border bg-card p-3 shadow-none">
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
                    const healthEvidence = nodeHealthEvidenceText(intl, node)
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
                            <Button
                              aria-label={intl.formatMessage(
                                { id: "nodes.lifecycle.openForNode" },
                                { id: node.node_id },
                              )}
                              onClick={() => openNodeLifecycle(node)}
                              size="sm"
                              variant="outline"
                            >
                              {intl.formatMessage({ id: "nodes.lifecycle.title" })}
                            </Button>
                            {canPromoteControllerVoter(node) ? (
                              <Button
                                aria-label={intl.formatMessage(
                                  { id: "nodes.action.promoteControllerVoterForNode" },
                                  { id: node.node_id },
                                )}
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
      ) : null}


      <DynamicNodeLifecycleSheet
        mode={lifecycleMode}
        node={lifecycleNode}
        onCompleted={refreshAfterLifecycleAction}
        onOpenChange={setLifecycleOpen}
        open={lifecycleOpen}
      />

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
          <section className="grid gap-3 md:grid-cols-2 xl:grid-cols-6">
            <SectionCard title={intl.formatMessage({ id: "nodes.metric.total" })}>
              <div className="text-3xl font-semibold text-foreground">{state.nodes.total}</div>
            </SectionCard>
            <SectionCard title={intl.formatMessage({ id: "nodes.metric.alive" })}>
              <div className="text-3xl font-semibold text-foreground">{aliveCount}</div>
            </SectionCard>
            <SectionCard title={intl.formatMessage({ id: "nodes.metric.unhealthy" })}>
              <div className="text-3xl font-semibold text-foreground">{unhealthyCount}</div>
            </SectionCard>
            <SectionCard title={intl.formatMessage({ id: "nodes.metric.draining" })}>
              <div className="text-3xl font-semibold text-foreground">{drainingCount}</div>
            </SectionCard>
            <SectionCard title={intl.formatMessage({ id: "nodes.metric.schedulable" })}>
              <div className="text-3xl font-semibold text-foreground">{schedulableCount}</div>
            </SectionCard>
            <SectionCard title={intl.formatMessage({ id: "nodes.metric.controllerVoters" })}>
              <div className="text-3xl font-semibold text-foreground">{controllerVoterCount}</div>
            </SectionCard>
          </section>

          <section className="grid gap-4 xl:grid-cols-[0.8fr_1.2fr]">
            <SectionCard
              description={intl.formatMessage({ id: "nodes.unhealthy.summary" }, { count: unhealthyCount })}
              title={intl.formatMessage({ id: "nodes.unhealthy.title" })}
            >
              <div className="rounded-lg border border-border bg-muted/30 px-3 py-3 text-sm leading-6 text-muted-foreground">
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
              <div className="grid gap-3 sm:grid-cols-3">
                <div className="rounded-lg border border-border bg-muted/20 px-3 py-3">
                  <div className="text-xs font-medium uppercase tracking-[0.14em] text-muted-foreground">
                    {intl.formatMessage({ id: "nodes.metric.gatewaySessions" })}
                  </div>
                  <div className="mt-2 text-2xl font-semibold text-foreground">{runtimeTotals.gatewaySessions}</div>
                </div>
                <div className="rounded-lg border border-border bg-muted/20 px-3 py-3">
                  <div className="text-xs font-medium uppercase tracking-[0.14em] text-muted-foreground">
                    {intl.formatMessage({ id: "nodes.metric.activeOnline" })}
                  </div>
                  <div className="mt-2 text-2xl font-semibold text-foreground">{runtimeTotals.activeOnline}</div>
                </div>
                <div className="rounded-lg border border-border bg-muted/20 px-3 py-3">
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
        <div className="rounded-xl border border-border bg-card p-3 shadow-none">
          {rows.length > 0 ? (
            <div className="overflow-x-auto rounded-lg border border-border">
              <table className="w-full border-collapse">
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
