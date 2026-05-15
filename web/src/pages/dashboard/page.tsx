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
        <section className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
          <PulseTile
            label={intl.formatMessage({ id: "dashboard.pulse.messagesPerSec" })}
            mocked
            series={pulse.messagesPerSec.series}
            sub={intl.formatMessage({ id: "dashboard.pulse.peakAvg" }, { peak: pulse.messagesPerSec.peak, avg: pulse.messagesPerSec.avg })}
            value={pulse.messagesPerSec.latest}
          />
          <PulseTile
            label={intl.formatMessage({ id: "dashboard.pulse.connections" })}
            mocked
            series={pulse.connections.series}
            sub={intl.formatMessage({ id: "dashboard.pulse.peakAvg" }, { peak: pulse.connections.peak, avg: pulse.connections.avg })}
            value={pulse.connections.latest}
          />
          <PulseTile
            label={intl.formatMessage({ id: "dashboard.pulse.txKbPerSec" })}
            mocked
            series={pulse.txKbPerSec.series}
            sub={intl.formatMessage({ id: "dashboard.pulse.peakAvg" }, { peak: pulse.txKbPerSec.peak, avg: pulse.txKbPerSec.avg })}
            value={pulse.txKbPerSec.latest}
            valueSuffix="KB/s"
          />
          <PulseTile
            label={intl.formatMessage({ id: "dashboard.pulse.rpcErrorRate" })}
            mocked
            series={pulse.rpcErrorRate.series}
            sub={intl.formatMessage({ id: "dashboard.pulse.errorsOverCalls" }, { errors: pulse.rpcErrorRate.latest, calls: pulse.rpcErrorRate.peak })}
            tone="danger"
            value={pulse.rpcErrorRate.latest}
            valueSuffix="%"
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
