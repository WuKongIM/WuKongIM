import { useCallback, useEffect, useMemo, useState } from "react"
import { useIntl } from "react-intl"

import { ResourceState } from "@/components/manager/resource-state"
import { StatusBadge } from "@/components/manager/status-badge"
import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
import { SectionCard } from "@/components/shell/section-card"
import { ManagerApiError, getNodes, getOverview, getSlots } from "@/lib/manager-api"
import type {
  ManagerNode,
  ManagerNodesResponse,
  ManagerOverviewResponse,
  ManagerSlot,
  ManagerSlotsResponse,
} from "@/lib/manager-api.types"

type TopologyState = {
  overview: ManagerOverviewResponse | null
  nodes: ManagerNodesResponse | null
  slots: ManagerSlotsResponse | null
  loading: boolean
  error: Error | null
}

function emptyTopologyState(): TopologyState {
  return {
    overview: null,
    nodes: null,
    slots: null,
    loading: true,
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

function formatNodeList(nodeIds: number[] | undefined) {
  return nodeIds && nodeIds.length > 0 ? nodeIds.join(", ") : "-"
}

function slotMentionsNode(slot: ManagerSlot, nodeId: number) {
  return (
    slot.runtime.preferred_leader_id === nodeId ||
    slot.assignment.desired_peers.includes(nodeId) ||
    slot.runtime.current_peers.includes(nodeId) ||
    Boolean(slot.runtime.current_voters?.includes(nodeId))
  )
}

function nodeSlotSummary(node: ManagerNode) {
  const replicas = node.slots?.replica_count ?? node.slot_stats.count
  const leaders = node.slots?.leader_count ?? node.slot_stats.leader_count
  const followers = node.slots?.follower_count ?? Math.max(replicas - leaders, 0)
  return { replicas, leaders, followers }
}

function topologyAnomalyCount(overview: ManagerOverviewResponse) {
  return (
    overview.slots.quorum_lost +
    overview.slots.leader_missing +
    overview.slots.unreported +
    overview.slots.peer_mismatch +
    overview.slots.epoch_lag
  )
}

function nodeDisplayName(node: ManagerNode) {
  return node.name && node.name.length > 0 ? node.name : `Node ${node.node_id}`
}

function nodeRuntimeLabel(node: ManagerNode) {
  if (!node.runtime || node.runtime.unknown) {
    return null
  }
  return {
    sessions: node.runtime.gateway_sessions,
    online: node.runtime.active_online,
  }
}

function slotQuorumLabel(slot: ManagerSlot) {
  return slot.state.quorum === "ready" ? slot.state.quorum : `quorum ${slot.state.quorum}`
}

function leaderLabel(slot: ManagerSlot) {
  return slot.runtime.preferred_leader_id > 0 ? `Preferred leader ${slot.runtime.preferred_leader_id}` : "Preferred leader missing"
}

function hashSlotLabel(slot: ManagerSlot) {
  return slot.hash_slots ? `${slot.hash_slots.count} hash slots` : "-"
}

export function TopologyPage() {
  const intl = useIntl()
  const [state, setState] = useState<TopologyState>(emptyTopologyState)
  const [selectedNodeId, setSelectedNodeId] = useState<number | null>(null)

  const loadTopology = useCallback(async () => {
    setState((current) => ({ ...current, loading: true, error: null }))
    try {
      const [overview, nodes, slots] = await Promise.all([
        getOverview(),
        getNodes(),
        getSlots(),
      ])
      setState({ overview, nodes, slots, loading: false, error: null })
    } catch (error) {
      setState({
        overview: null,
        nodes: null,
        slots: null,
        loading: false,
        error: error instanceof Error ? error : new Error("topology request failed"),
      })
    }
  }, [])

  useEffect(() => {
    void loadTopology()
  }, [loadTopology])

  const overview = state.overview
  const nodes = state.nodes
  const slots = state.slots
  const filteredSlots = useMemo(() => {
    const items = slots?.items ?? []
    if (selectedNodeId === null) {
      return items
    }
    return items.filter((slot) => slotMentionsNode(slot, selectedNodeId))
  }, [selectedNodeId, slots])

  const controllerLeaderID = overview?.cluster.controller_leader_id ?? nodes?.controller_leader_id ?? 0

  return (
    <PageContainer>
      <PageHeader
        title={intl.formatMessage({ id: "nav.topology.title" })}
        description={intl.formatMessage({ id: "topology.description" })}
      >
        <div className="flex flex-wrap gap-2 text-xs text-muted-foreground">
          <div className="rounded-md border border-border bg-background px-3 py-2">
            {intl.formatMessage({ id: "topology.scopeClusterGraph" })}
          </div>
          <div className="rounded-md border border-border bg-background px-3 py-2">
            {intl.formatMessage({ id: "topology.statusReadonly" })}
          </div>
        </div>
      </PageHeader>

      {state.loading ? <ResourceState kind="loading" title={intl.formatMessage({ id: "nav.topology.title" })} /> : null}
      {!state.loading && state.error ? (
        <ResourceState
          kind={mapErrorKind(state.error)}
          onRetry={() => {
            void loadTopology()
          }}
          title={intl.formatMessage({ id: "nav.topology.title" })}
        />
      ) : null}

      {!state.loading && !state.error && overview && nodes && slots ? (
        <>
          <SectionCard
            description={intl.formatMessage({ id: "topology.summary.description" })}
            title={intl.formatMessage({ id: "topology.summary.title" })}
          >
            <div
              className="grid overflow-hidden rounded-md border border-border bg-card md:grid-cols-4"
              data-testid="topology-summary-strip"
            >
              <div className="border-b border-border px-3 py-3 text-sm font-semibold text-foreground last:border-b-0 md:border-r md:border-b-0 md:last:border-r-0" data-topology-summary-cell="">
                {controllerLeaderID > 0
                  ? intl.formatMessage({ id: "topology.controllerLeader" }, { id: controllerLeaderID })
                  : intl.formatMessage({ id: "topology.controllerLeader.empty" })}
              </div>
              <div className="border-b border-border px-3 py-3 text-sm font-semibold text-foreground last:border-b-0 md:border-r md:border-b-0 md:last:border-r-0" data-topology-summary-cell="">
                {intl.formatMessage({ id: "topology.nodesValue" }, { count: nodes.total })}
              </div>
              <div className="border-b border-border px-3 py-3 text-sm font-semibold text-foreground last:border-b-0 md:border-r md:border-b-0 md:last:border-r-0" data-topology-summary-cell="">
                {intl.formatMessage({ id: "topology.slotsValue" }, { count: slots.total })}
              </div>
              <div className="border-b border-border px-3 py-3 text-sm font-semibold text-foreground last:border-b-0 md:border-r md:border-b-0 md:last:border-r-0" data-topology-summary-cell="">
                {intl.formatMessage({ id: "topology.anomaliesValue" }, { count: topologyAnomalyCount(overview) })}
              </div>
            </div>
          </SectionCard>

          <SectionCard
            description={intl.formatMessage({ id: "topology.nodeTopology.description" })}
            title={intl.formatMessage({ id: "topology.nodeTopology.title" })}
          >
            <label className="mb-4 flex max-w-xs flex-col gap-2 text-sm font-medium text-foreground">
              {intl.formatMessage({ id: "topology.nodeFilter" })}
              <select
                aria-label={intl.formatMessage({ id: "topology.nodeFilter" })}
                className="rounded-md border border-border bg-background px-3 py-2 text-sm text-foreground"
                onChange={(event) => {
                  setSelectedNodeId(event.target.value ? Number(event.target.value) : null)
                }}
                value={selectedNodeId ?? ""}
              >
                <option value="">{intl.formatMessage({ id: "topology.allNodes" })}</option>
                {nodes.items.map((node) => (
                  <option key={node.node_id} value={node.node_id}>
                    {nodeDisplayName(node)} ({node.node_id})
                  </option>
                ))}
              </select>
            </label>

            {nodes.items.length > 0 ? (
              <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3" data-topology-surface="nodes">
                {nodes.items.map((node) => {
                  const slotsSummary = nodeSlotSummary(node)
                  const runtime = nodeRuntimeLabel(node)
                  return (
                    <article className="rounded-md border border-border bg-background p-4" key={node.node_id}>
                      <div className="flex items-start justify-between gap-3">
                        <div>
                          <h3 className="font-semibold text-foreground">{nodeDisplayName(node)}</h3>
                          <p className="mt-1 font-mono text-xs text-muted-foreground">{node.addr}</p>
                        </div>
                        <StatusBadge value={node.health?.status ?? node.status} />
                      </div>
                      <div className="mt-3 flex flex-wrap gap-2 text-xs text-muted-foreground">
                        {node.is_local ? (
                          <span className="rounded-md border border-border bg-muted/40 px-2 py-1">
                            {intl.formatMessage({ id: "topology.localNode" })}
                          </span>
                        ) : null}
                        <span className="rounded-md border border-border bg-muted/40 px-2 py-1">
                          {intl.formatMessage({ id: node.controller.voter ? "topology.controllerVoter" : "topology.controllerNonVoter" })}
                        </span>
                      </div>
                      <div className="mt-4 space-y-2 text-sm text-muted-foreground">
                        <div>
                          {intl.formatMessage(
                            { id: "topology.nodeSlots" },
                            { replicas: slotsSummary.replicas, leaders: slotsSummary.leaders, followers: slotsSummary.followers },
                          )}
                        </div>
                        <div>
                          {runtime
                            ? intl.formatMessage({ id: "topology.nodeRuntime" }, runtime)
                            : intl.formatMessage({ id: "topology.nodeRuntimeUnknown" })}
                        </div>
                      </div>
                    </article>
                  )
                })}
              </div>
            ) : (
              <ResourceState
                description={intl.formatMessage({ id: "topology.nodes.empty" })}
                kind="empty"
                title={intl.formatMessage({ id: "topology.nodeTopology.title" })}
              />
            )}
          </SectionCard>

          <SectionCard
            description={intl.formatMessage({ id: "topology.slotPlacement.description" })}
            title={intl.formatMessage({ id: "topology.slotPlacement.title" })}
          >
            <p className="mb-3 text-sm text-muted-foreground">
              {intl.formatMessage(
                { id: "topology.filteredSlots" },
                { shown: filteredSlots.length, total: slots.items.length },
              )}
            </p>
            {filteredSlots.length > 0 ? (
              <div
                className="overflow-x-auto rounded-md border border-border"
                data-topology-surface="slot-placement"
              >
                <table
                  aria-label={intl.formatMessage({ id: "topology.slotPlacement.title" })}
                  className="w-full border-collapse text-sm"
                >
                  <thead className="bg-muted/40 text-left text-xs uppercase tracking-[0.14em] text-muted-foreground">
                    <tr>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "topology.table.slot" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "topology.table.leader" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "topology.table.desiredPeers" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "topology.table.currentPeers" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "topology.table.quorum" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "topology.table.sync" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "topology.table.hashSlots" })}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {filteredSlots.map((slot) => (
                      <tr className="border-t border-border" key={slot.slot_id}>
                        <td className="px-3 py-3 text-sm font-medium text-foreground">
                          {intl.formatMessage({ id: "topology.slotValue" }, { id: slot.slot_id })}
                        </td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">{leaderLabel(slot)}</td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">{formatNodeList(slot.assignment.desired_peers)}</td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">{formatNodeList(slot.runtime.current_peers)}</td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">{slotQuorumLabel(slot)}</td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">{slot.state.sync.replaceAll("_", " ")}</td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">{hashSlotLabel(slot)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            ) : (
              <ResourceState
                description={intl.formatMessage({ id: "topology.slots.empty" })}
                kind="empty"
                title={intl.formatMessage({ id: "topology.slotPlacement.title" })}
              />
            )}
          </SectionCard>
        </>
      ) : null}
    </PageContainer>
  )
}
