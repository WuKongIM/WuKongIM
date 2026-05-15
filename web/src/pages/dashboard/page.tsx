import { useCallback, useEffect, useMemo, useState } from "react"
import { useIntl } from "react-intl"

import { ResourceState } from "@/components/manager/resource-state"
import { PageContainer } from "@/components/shell/page-container"
import { SectionCard } from "@/components/shell/section-card"
import { ManagerApiError, getChannelClusterSummary, getNodes, getOverview, getTasks } from "@/lib/manager-api"
import type {
  ManagerChannelClusterSummaryResponse,
  ManagerNodesResponse,
  ManagerOverviewResponse,
  ManagerTasksResponse,
} from "@/lib/manager-api.types"

import { HealthHero } from "./components/health-hero"
import { IncidentList } from "./components/incident-list"
import { PulseTile } from "./components/pulse-tile"
import { SlotChannelHealth } from "./components/slot-channel-health"
import { TopologyRow } from "./components/topology-row"
import { useDashboardPulse } from "./use-dashboard-pulse"
import { buildIncidents, buildTopologyRows, computeVerdict } from "./view-model"

type DashboardState = {
  overview: ManagerOverviewResponse | null
  tasks: ManagerTasksResponse | null
  nodes: ManagerNodesResponse | null
  channelCluster: ManagerChannelClusterSummaryResponse | null
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

export function DashboardPage() {
  const intl = useIntl()
  const [state, setState] = useState<DashboardState>({
    overview: null,
    tasks: null,
    nodes: null,
    channelCluster: null,
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
      const [overview, tasks, nodes, channelCluster] = await Promise.all([
        getOverview(),
        getTasks(),
        getNodes(),
        getChannelClusterSummary(),
      ])
      setState({ overview, tasks, nodes, channelCluster, loading: false, refreshing: false, error: null })
    } catch (error) {
      setState({
        overview: null, tasks: null, nodes: null, channelCluster: null,
        loading: false, refreshing: false,
        error: error instanceof Error ? error : new Error("dashboard request failed"),
      })
    }
  }, [])

  useEffect(() => { void loadDashboard(false) }, [loadDashboard])

  const verdict = useMemo(
    () => (state.overview && state.nodes ? computeVerdict(state.overview, state.nodes) : null),
    [state.overview, state.nodes],
  )
  const incidents = useMemo(
    () => (state.overview && state.nodes ? buildIncidents(intl, state.overview, state.nodes) : []),
    [intl, state.overview, state.nodes],
  )
  const topologyRows = useMemo(
    () => (state.nodes ? buildTopologyRows(state.nodes) : []),
    [state.nodes],
  )
  const pulse = useDashboardPulse(state.overview?.generated_at ?? null)

  const title = intl.formatMessage({ id: "dashboard.title" })

  if (state.loading) {
    return (
      <PageContainer>
        <h1 className="text-2xl font-semibold tracking-[-0.04em] text-foreground sm:text-3xl">{title}</h1>
        <ResourceState kind="loading" title={title} />
      </PageContainer>
    )
  }

  if (state.error) {
    return (
      <PageContainer>
        <h1 className="text-2xl font-semibold tracking-[-0.04em] text-foreground sm:text-3xl">{title}</h1>
        <ResourceState
          kind={formatErrorKind(state.error)}
          onRetry={() => { void loadDashboard(false) }}
          title={title}
        />
      </PageContainer>
    )
  }

  if (!state.overview || !state.nodes || !state.channelCluster || !verdict) return null

  return (
    <PageContainer>
      {/* R1 — Health Hero */}
      <HealthHero
        controllerLeaderId={state.overview.cluster.controller_leader_id}
        generatedAt={state.overview.generated_at}
        incidentCount={incidents.length}
        nodesAlive={state.overview.nodes.alive}
        nodesTotal={state.overview.nodes.total}
        onRefresh={() => { void loadDashboard(true) }}
        refreshing={state.refreshing}
        slotsReady={state.overview.slots.ready}
        slotsTotal={state.overview.slots.total}
        verdict={verdict}
      />

      {/* R2 — Realtime Pulse */}
      {pulse ? (
        <section className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-5">
          <PulseTile
            label={intl.formatMessage({ id: "dashboard.pulse.sendPerSec" })}
            mocked={pulse.mocked}
            series={pulse.sendPerSec.series}
            sub={intl.formatMessage({ id: "dashboard.pulse.peakAvg" }, { peak: pulse.sendPerSec.peak, avg: pulse.sendPerSec.avg })}
            value={pulse.sendPerSec.latest}
          />
          <PulseTile
            label={intl.formatMessage({ id: "dashboard.pulse.deliverPerSec" })}
            mocked={pulse.mocked}
            series={pulse.deliverPerSec.series}
            sub={intl.formatMessage({ id: "dashboard.pulse.peakAvg" }, { peak: pulse.deliverPerSec.peak, avg: pulse.deliverPerSec.avg })}
            value={pulse.deliverPerSec.latest}
          />
          <PulseTile
            label={intl.formatMessage({ id: "dashboard.pulse.connections" })}
            mocked={pulse.mocked}
            series={pulse.connections.series}
            sub={intl.formatMessage({ id: "dashboard.pulse.peakAvg" }, { peak: pulse.connections.peak, avg: pulse.connections.avg })}
            value={pulse.connections.latest}
          />
          <PulseTile
            label={intl.formatMessage({ id: "dashboard.pulse.sendLatencyP99" })}
            mocked={pulse.mocked}
            series={pulse.sendLatencyP99.series}
            sub={intl.formatMessage({ id: "dashboard.pulse.latencyMs" }, { peak: pulse.sendLatencyP99.peak, avg: pulse.sendLatencyP99.avg })}
            value={pulse.sendLatencyP99.latest}
            valueSuffix="ms"
          />
          <PulseTile
            label={intl.formatMessage({ id: "dashboard.pulse.deliveryLatencyP99" })}
            mocked={pulse.mocked}
            series={pulse.deliveryLatencyP99.series}
            sub={intl.formatMessage({ id: "dashboard.pulse.latencyMs" }, { peak: pulse.deliveryLatencyP99.peak, avg: pulse.deliveryLatencyP99.avg })}
            tone="danger"
            value={pulse.deliveryLatencyP99.latest}
            valueSuffix="ms"
          />
          <PulseTile
            label={intl.formatMessage({ id: "dashboard.pulse.sendFailRate" })}
            mocked={pulse.mocked}
            series={pulse.sendFailRate.series}
            sub={intl.formatMessage({ id: "dashboard.pulse.peakAvg" }, { peak: pulse.sendFailRate.peak, avg: pulse.sendFailRate.avg })}
            tone="danger"
            value={pulse.sendFailRate.latest}
            valueSuffix="%"
          />
          <PulseTile
            label={intl.formatMessage({ id: "dashboard.pulse.deliveryFailRate" })}
            mocked={pulse.mocked}
            series={pulse.deliveryFailRate.series}
            sub={intl.formatMessage({ id: "dashboard.pulse.peakAvg" }, { peak: pulse.deliveryFailRate.peak, avg: pulse.deliveryFailRate.avg })}
            tone="danger"
            value={pulse.deliveryFailRate.latest}
            valueSuffix="%"
          />
          <PulseTile
            label={intl.formatMessage({ id: "dashboard.pulse.activeChannels" })}
            mocked={pulse.mocked}
            series={pulse.activeChannels.series}
            sub={intl.formatMessage({ id: "dashboard.pulse.peakAvg" }, { peak: pulse.activeChannels.peak, avg: pulse.activeChannels.avg })}
            value={pulse.activeChannels.latest}
          />
          <PulseTile
            label={intl.formatMessage({ id: "dashboard.pulse.retryQueueDepth" })}
            mocked={pulse.mocked}
            series={pulse.retryQueueDepth.series}
            sub={intl.formatMessage({ id: "dashboard.pulse.peakAvg" }, { peak: pulse.retryQueueDepth.peak, avg: pulse.retryQueueDepth.avg })}
            tone={pulse.retryQueueDepth.latest > 10 ? "danger" : "default"}
            value={pulse.retryQueueDepth.latest}
          />
          <PulseTile
            label={intl.formatMessage({ id: "dashboard.pulse.fanOutRate" })}
            mocked={pulse.mocked}
            series={pulse.fanOutRate.series}
            sub={intl.formatMessage({ id: "dashboard.pulse.peakAvg" }, { peak: pulse.fanOutRate.peak, avg: pulse.fanOutRate.avg })}
            value={pulse.fanOutRate.latest}
            valueSuffix="x"
          />
        </section>
      ) : null}

      {/* R3 + R4 — Slot/Channel Health + Active Incidents */}
      <section className="grid gap-4 xl:grid-cols-[2fr_3fr]">
        <SlotChannelHealth channelCluster={state.channelCluster} slots={state.overview.slots} />
        <IncidentList items={incidents} />
      </section>

      {/* R5 — Topology Snapshot */}
      <SectionCard title={intl.formatMessage({ id: "dashboard.topology.cardTitle" })}>
        {topologyRows.length > 0 ? (
          <div className="space-y-2">
            {topologyRows.map((row) => (
              <TopologyRow key={row.nodeId} row={row} />
            ))}
          </div>
        ) : (
          <ResourceState kind="empty" title={intl.formatMessage({ id: "dashboard.topology.cardTitle" })} />
        )}
      </SectionCard>
    </PageContainer>
  )
}
