import { useCallback, useEffect, useMemo, useState } from "react"
import { useIntl } from "react-intl"

import { ResourceState } from "@/components/manager/resource-state"
import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
import {
  ManagerApiError,
  getChannelClusterSummary,
  getNetworkSummary,
  getNodes,
  getOverview,
  getTasks,
} from "@/lib/manager-api"
import type {
  ManagerChannelClusterSummaryResponse,
  ManagerNetworkSummaryResponse,
  ManagerNodesResponse,
  ManagerOverviewResponse,
  ManagerTasksResponse,
} from "@/lib/manager-api.types"

import { ClusterHealthHero } from "./components/cluster-health-hero"
import { ClusterIncidentList } from "./components/cluster-incident-list"
import { ClusterLinkTrends } from "./components/cluster-link-trends"
import { ClusterMetricStrip } from "./components/cluster-metric-strip"
import { ClusterReplicationHealth } from "./components/cluster-replication-health"
import { ClusterTopologyWatermarks } from "./components/cluster-topology-watermarks"
import {
  buildClusterIncidents,
  buildClusterMetricStrip,
  buildInternalLinkMetrics,
  buildTopologyRows,
  computeClusterVerdict,
} from "./view-model"

type ClusterDashboardState = {
  overview: ManagerOverviewResponse | null
  tasks: ManagerTasksResponse | null
  nodes: ManagerNodesResponse | null
  channelCluster: ManagerChannelClusterSummaryResponse | null
  network: ManagerNetworkSummaryResponse | null
  networkError: Error | null
  loading: boolean
  refreshing: boolean
  error: Error | null
}

function formatErrorKind(error: Error | null) {
  if (!(error instanceof ManagerApiError)) return "error" as const
  if (error.status === 403) return "forbidden" as const
  if (error.status === 503) return "unavailable" as const
  return "error" as const
}

export function ClusterDashboardPage() {
  const intl = useIntl()
  const [state, setState] = useState<ClusterDashboardState>({
    overview: null,
    tasks: null,
    nodes: null,
    channelCluster: null,
    network: null,
    networkError: null,
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
      networkError: null,
    }))

    try {
      const [overview, tasks, nodes, channelCluster] = await Promise.all([
        getOverview(),
        getTasks(),
        getNodes(),
        getChannelClusterSummary(),
      ])

      let network: ManagerNetworkSummaryResponse | null = null
      let networkError: Error | null = null
      try {
        network = await getNetworkSummary()
      } catch (error) {
        networkError = error instanceof Error ? error : new Error("network summary request failed")
      }

      setState({
        overview,
        tasks,
        nodes,
        channelCluster,
        network,
        networkError,
        loading: false,
        refreshing: false,
        error: null,
      })
    } catch (error) {
      setState({
        overview: null,
        tasks: null,
        nodes: null,
        channelCluster: null,
        network: null,
        networkError: null,
        loading: false,
        refreshing: false,
        error: error instanceof Error ? error : new Error("cluster dashboard request failed"),
      })
    }
  }, [])

  useEffect(() => {
    void loadDashboard(false)
  }, [loadDashboard])

  const title = intl.formatMessage({ id: "clusterDashboard.title" })

  const model = useMemo(() => {
    if (!state.overview || !state.nodes || !state.channelCluster) return null
    const linkMetrics = buildInternalLinkMetrics(state.network)
    return {
      verdict: computeClusterVerdict(state.overview, state.nodes),
      linkMetrics,
      metricStrip: buildClusterMetricStrip(state.overview, state.channelCluster, linkMetrics),
      incidents: buildClusterIncidents(intl, state.overview, state.nodes, state.network),
      topologyRows: buildTopologyRows(state.nodes, state.network),
    }
  }, [intl, state.channelCluster, state.network, state.nodes, state.overview])

  let content = null

  if (state.loading) {
    content = <ResourceState kind="loading" title={title} />
  } else if (state.error) {
    content = (
      <ResourceState kind={formatErrorKind(state.error)} onRetry={() => { void loadDashboard(false) }} title={title} />
    )
  } else if (state.overview && state.nodes && state.channelCluster && model) {
    content = (
      <>
        <ClusterHealthHero
          controllerLeaderId={state.overview.cluster.controller_leader_id}
          generatedAt={state.overview.generated_at}
          incidentCount={model.incidents.length}
          nodesAlive={state.overview.nodes.alive}
          nodesTotal={state.overview.nodes.total}
          onRefresh={() => { void loadDashboard(true) }}
          refreshing={state.refreshing}
          slotsReady={state.overview.slots.ready}
          slotsTotal={state.overview.slots.total}
          verdict={model.verdict}
        />
        <ClusterMetricStrip metrics={model.metricStrip} />
        <ClusterLinkTrends linkMetrics={model.linkMetrics} networkError={state.networkError} />
        <section className="grid gap-4 xl:grid-cols-[2fr_3fr]">
          <ClusterReplicationHealth channelCluster={state.channelCluster} slots={state.overview.slots} />
          <ClusterIncidentList items={model.incidents} />
        </section>
        <ClusterTopologyWatermarks rows={model.topologyRows} />
      </>
    )
  }

  return (
    <PageContainer>
      <PageHeader
        description={intl.formatMessage({ id: "clusterDashboard.description" })}
        title={title}
      />
      {content}
    </PageContainer>
  )
}
