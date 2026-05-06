import { useMemo, useState } from "react"
import { useIntl, type IntlShape } from "react-intl"

import { ResourceState } from "@/components/manager/resource-state"
import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
import { ManagerApiError } from "@/lib/manager-api"
import type { ManagerNetworkSummaryResponse } from "@/lib/manager-api.types"
import { ConnectionPoolCard } from "./components/connection-pool-card"
import { DetailDrawer, type NetworkDetailKind } from "./components/detail-drawer"
import { ErrorsCard } from "./components/errors-card"
import { EventsCard } from "./components/events-card"
import { FilterBar } from "./components/filter-bar"
import { MessageTypesCard } from "./components/message-types-card"
import { NodeHealthCard } from "./components/node-health-card"
import { RpcLatencyCard } from "./components/rpc-latency-card"
import { RpcSuccessCard } from "./components/rpc-success-card"
import { TrafficCard } from "./components/traffic-card"
import { useNetworkData } from "./hooks/use-network-data"
import { useNodeFilter } from "./hooks/use-node-filter"
import type { FilteredNetworkMetrics, NetworkTimeRange } from "./utils/aggregation"
import { formatTimestamp } from "./utils/formatters"

function mapErrorKind(error: Error | null) {
  if (!(error instanceof ManagerApiError)) return "error" as const
  if (error.status === 403) return "forbidden" as const
  if (error.status === 503) return "unavailable" as const
  return "error" as const
}

function resourceTitle(intl: IntlShape, error: Error | null) {
  if (mapErrorKind(error) === "forbidden") return intl.formatMessage({ id: "network.error.forbiddenTitle" })
  return intl.formatMessage({ id: "network.title" })
}

function filterLabels(intl: IntlShape) {
  return {
    nodeSelector: intl.formatMessage({ id: "network.filter.nodeSelector" }),
    allNodes: intl.formatMessage({ id: "network.filter.allNodes" }),
    timeRange: intl.formatMessage({ id: "network.filter.timeRange" }),
    last1m: intl.formatMessage({ id: "network.filter.last1m" }),
    last5m: intl.formatMessage({ id: "network.filter.last5m" }),
    last15m: intl.formatMessage({ id: "network.filter.last15m" }),
    autoRefresh: intl.formatMessage({ id: "network.filter.autoRefresh" }),
    refresh: intl.formatMessage({ id: "common.refresh" }),
    refreshing: intl.formatMessage({ id: "common.refreshing" }),
  }
}

function rangeLabel(intl: IntlShape, range: NetworkTimeRange) {
  if (range === "5m") return intl.formatMessage({ id: "network.filter.last5m" })
  if (range === "15m") return intl.formatMessage({ id: "network.filter.last15m" })
  return intl.formatMessage({ id: "network.filter.last1m" })
}

function scopeBadges(intl: IntlShape, summary: ManagerNetworkSummaryResponse, metrics: FilteredNetworkMetrics, timeRange: NetworkTimeRange) {
  const selectedLabels = metrics.selectedNodeIds.map((nodeId) => metrics.nodeOptions.find((node) => node.nodeId === nodeId)?.label ?? `node-${nodeId}`)
  const badges = [
    intl.formatMessage({ id: "network.scope.localNodeValue" }, { id: summary.scope.local_node_id }),
    intl.formatMessage({ id: "network.scope.controllerLeaderValue" }, { id: summary.scope.controller_leader_id }),
    intl.formatMessage({ id: "network.scope.generatedValue" }, { value: formatTimestamp(intl, summary.generated_at) }),
    metrics.isAllNodes
      ? intl.formatMessage({ id: "network.filter.allNodesScope" })
      : intl.formatMessage({ id: "network.filter.selectedNodes" }, { value: selectedLabels.join(", ") }),
    rangeLabel(intl, timeRange),
  ]
  if (summary.headline.remote_peers === 0) badges.push(intl.formatMessage({ id: "network.scope.singleNodeCluster" }))
  return badges
}

function detailTitle(intl: IntlShape, kind: NetworkDetailKind | null) {
  const ids: Record<NetworkDetailKind, string> = {
    health: "network.card.nodeHealth",
    pools: "network.card.connectionPool",
    latency: "network.card.rpcLatency",
    success: "network.card.rpcSuccess",
    traffic: "network.card.traffic",
    errors: "network.card.errors",
    messageTypes: "network.card.messageTypes",
    events: "network.card.events",
  }
  if (!kind) return ""
  return intl.formatMessage({ id: "network.detail.title" }, { title: intl.formatMessage({ id: ids[kind] }) })
}

export function NetworkPage() {
  const intl = useIntl()
  const filter = useNodeFilter()
  const network = useNetworkData({ selectedNodes: filter.selectedNodes, autoRefresh: filter.autoRefresh })
  const [detailKind, setDetailKind] = useState<NetworkDetailKind | null>(null)
  const labels = useMemo(() => filterLabels(intl), [intl])

  const summary = network.summary
  const metrics = network.filteredData
  const errorKind = mapErrorKind(network.error)

  return (
    <PageContainer>
      <PageHeader title={intl.formatMessage({ id: "network.title" })} description={intl.formatMessage({ id: "network.description" })} />

      {network.loading ? <ResourceState kind="loading" title={intl.formatMessage({ id: "network.title" })} description={intl.formatMessage({ id: "network.loading" })} /> : null}
      {!network.loading && network.error ? <ResourceState kind={errorKind} onRetry={() => { void network.refresh() }} title={resourceTitle(intl, network.error)} /> : null}

      {!network.loading && !network.error && summary && metrics ? (
        <>
          <FilterBar
            autoRefresh={filter.autoRefresh}
            labels={labels}
            nodeOptions={metrics.nodeOptions}
            onAutoRefreshToggle={filter.toggleAutoRefresh}
            onRefresh={() => { void network.refresh() }}
            onSelectedNodesChange={filter.setSelectedNodes}
            onTimeRangeChange={filter.setTimeRange}
            refreshing={network.refreshing}
            scopeBadges={scopeBadges(intl, summary, metrics, filter.timeRange)}
            selectedNodes={filter.selectedNodes}
            timeRange={filter.timeRange}
          />
          <section className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
            <NodeHealthCard metrics={metrics} onClick={() => setDetailKind("health")} />
            <ConnectionPoolCard metrics={metrics} onClick={() => setDetailKind("pools")} />
            <RpcLatencyCard metrics={metrics} onClick={() => setDetailKind("latency")} />
            <RpcSuccessCard metrics={metrics} onClick={() => setDetailKind("success")} />
            <TrafficCard metrics={metrics} onClick={() => setDetailKind("traffic")} timeRange={filter.timeRange} />
            <ErrorsCard metrics={metrics} onClick={() => setDetailKind("errors")} />
            <MessageTypesCard metrics={metrics} onClick={() => setDetailKind("messageTypes")} />
            <EventsCard metrics={metrics} onClick={() => setDetailKind("events")} />
          </section>
          <DetailDrawer kind={detailKind} metrics={metrics} onOpenChange={(open) => { if (!open) setDetailKind(null) }} open={detailKind !== null} title={detailTitle(intl, detailKind)} />
        </>
      ) : null}
    </PageContainer>
  )
}
