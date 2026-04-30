import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react"
import { useIntl, type IntlShape } from "react-intl"

import { ResourceState } from "@/components/manager/resource-state"
import { StatusBadge } from "@/components/manager/status-badge"
import { Button } from "@/components/ui/button"
import { ChartContainer, ChartLegend, ChartLegendContent, ChartTooltip, ChartTooltipContent, type ChartConfig } from "@/components/ui/chart"
import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
import { SectionCard } from "@/components/shell/section-card"
import { ManagerApiError, getNetworkSummary } from "@/lib/manager-api"
import type {
  ManagerNetworkDiscovery,
  ManagerNetworkEvent,
  ManagerNetworkHistory,
  ManagerNetworkPeer,
  ManagerNetworkPoolStats,
  ManagerNetworkRPCService,
  ManagerNetworkSummaryResponse,
  ManagerNetworkTrafficMessageType,
} from "@/lib/manager-api.types"
import { Area, AreaChart, Bar, BarChart, CartesianGrid, Cell, LabelList, Pie, PieChart, XAxis, YAxis } from "recharts"

type NetworkState = {
  summary: ManagerNetworkSummaryResponse | null
  loading: boolean
  refreshing: boolean
  error: Error | null
}

function formatTimestamp(intl: IntlShape, value: string) {
  const date = new Date(value)
  if (!value || value.startsWith("0001-") || Number.isNaN(date.getTime())) {
    return intl.formatMessage({ id: "network.latency.insufficientSamples" })
  }
  return new Intl.DateTimeFormat(intl.locale, {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  }).format(date)
}

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

function compactNumber(intl: IntlShape, value: number) {
  return new Intl.NumberFormat(intl.locale).format(value)
}

function bytes(intl: IntlShape, value: number) {
  return `${compactNumber(intl, value)} B`
}

function latency(intl: IntlShape, value: number) {
  if (value <= 0) return intl.formatMessage({ id: "network.latency.insufficientSamples" })
  return `${compactNumber(intl, value)} ms`
}

function successRate(intl: IntlShape, service: ManagerNetworkRPCService) {
  const completedCalls = Math.max(service.calls_1m - service.expected_timeout_1m, 0)
  if (completedCalls <= 0) return intl.formatMessage({ id: "network.latency.insufficientSamples" })
  const successfulCalls = Math.min(service.success_1m, completedCalls)
  return `${Math.round((successfulCalls / completedCalls) * 100)}%`
}

function HeaderBadge({ children }: { children: ReactNode }) {
  return <div className="rounded-md border border-border bg-background px-3 py-2">{children}</div>
}

function MetricCard({ label, value, detail, tone = "default" }: { label: string; value: string | number; detail?: string; tone?: "default" | "neutral" | "danger" }) {
  const toneClass = tone === "danger" ? "border-destructive/30 bg-destructive/5" : tone === "neutral" ? "border-blue-500/25 bg-blue-500/5" : "bg-muted/25"
  return (
    <div className={`rounded-xl border border-border px-4 py-4 ${toneClass}`}>
      <div className="text-xs uppercase tracking-[0.16em] text-muted-foreground">{label}</div>
      <div className="mt-2 text-2xl font-semibold text-foreground">{value}</div>
      {detail ? <div className="mt-1 text-xs text-muted-foreground">{detail}</div> : null}
    </div>
  )
}

const chartColors = {
  alive: "hsl(142 70% 45%)",
  suspect: "hsl(38 92% 50%)",
  dead: "hsl(0 72% 51%)",
  draining: "hsl(217 91% 60%)",
  active: "hsl(199 89% 48%)",
  idle: "hsl(210 14% 70%)",
  tx: "hsl(24 95% 53%)",
  rx: "hsl(173 80% 40%)",
  calls: "hsl(221 83% 53%)",
  success: "hsl(142 70% 45%)",
  errors: "hsl(0 72% 51%)",
  expected: "hsl(262 83% 58%)",
  dial: "hsl(18 92% 48%)",
  queue: "hsl(38 92% 50%)",
  timeout: "hsl(0 72% 51%)",
  remote: "hsl(330 81% 60%)",
}

function networkChartConfig(intl: IntlShape): ChartConfig {
  return {
    alive: { label: intl.formatMessage({ id: "network.legend.alive" }), color: chartColors.alive },
    suspect: { label: intl.formatMessage({ id: "network.legend.suspect" }), color: chartColors.suspect },
    dead: { label: intl.formatMessage({ id: "network.legend.dead" }), color: chartColors.dead },
    draining: { label: intl.formatMessage({ id: "network.legend.draining" }), color: chartColors.draining },
    active: { label: intl.formatMessage({ id: "network.legend.active" }), color: chartColors.active },
    idle: { label: intl.formatMessage({ id: "network.legend.idle" }), color: chartColors.idle },
    tx: { label: intl.formatMessage({ id: "network.legend.tx" }), color: chartColors.tx },
    rx: { label: intl.formatMessage({ id: "network.legend.rx" }), color: chartColors.rx },
    calls: { label: intl.formatMessage({ id: "network.legend.calls" }), color: chartColors.calls },
    success: { label: intl.formatMessage({ id: "network.legend.success" }), color: chartColors.success },
    errors: { label: intl.formatMessage({ id: "network.rpc.abnormalFailures" }), color: chartColors.errors },
    expected: { label: intl.formatMessage({ id: "network.rpc.expectedLongPollExpiries" }), color: chartColors.expected },
    dial: { label: intl.formatMessage({ id: "network.legend.dial" }), color: chartColors.dial },
    queue: { label: intl.formatMessage({ id: "network.legend.queue" }), color: chartColors.queue },
    timeout: { label: intl.formatMessage({ id: "network.legend.timeout" }), color: chartColors.timeout },
    remote: { label: intl.formatMessage({ id: "network.legend.remote" }), color: chartColors.remote },
  }
}

function shortTime(intl: IntlShape, value: string) {
  const date = new Date(value)
  if (!value || value.startsWith("0001-") || Number.isNaN(date.getTime())) return intl.formatMessage({ id: "network.latency.insufficientSamples" })
  return new Intl.DateTimeFormat(intl.locale, { hour: "2-digit", minute: "2-digit" }).format(date)
}

function nodeHealthData(intl: IntlShape, summary: ManagerNetworkSummaryResponse) {
  return [
    { key: "alive", name: intl.formatMessage({ id: "network.legend.alive" }), value: summary.headline.alive_nodes, fill: chartColors.alive },
    { key: "suspect", name: intl.formatMessage({ id: "network.legend.suspect" }), value: summary.headline.suspect_nodes, fill: chartColors.suspect },
    { key: "dead", name: intl.formatMessage({ id: "network.legend.dead" }), value: summary.headline.dead_nodes, fill: chartColors.dead },
    { key: "draining", name: intl.formatMessage({ id: "network.legend.draining" }), value: summary.headline.draining_nodes, fill: chartColors.draining },
  ]
}

function poolData(label: string, pool: ManagerNetworkPoolStats) {
  return [{ label, active: pool.active, idle: pool.idle }]
}

function headlinePoolData(intl: IntlShape, summary: ManagerNetworkSummaryResponse) {
  return [{ label: intl.formatMessage({ id: "network.chart.transportPool" }), active: summary.headline.pool_active, idle: summary.headline.pool_idle }]
}

function summaryHistory(summary: ManagerNetworkSummaryResponse): ManagerNetworkHistory {
  return summary.history ?? { window_seconds: 60, step_seconds: 60, traffic: [], rpc: [], errors: [] }
}

function errorMixData(intl: IntlShape, summary: ManagerNetworkSummaryResponse) {
  return [
    { label: intl.formatMessage({ id: "network.legend.dial" }), dial: summary.headline.dial_errors_1m, fill: chartColors.dial },
    { label: intl.formatMessage({ id: "network.legend.queue" }), queue: summary.headline.queue_full_1m, fill: chartColors.queue },
    { label: intl.formatMessage({ id: "network.legend.timeout" }), timeout: summary.headline.timeouts_1m, fill: chartColors.timeout },
  ]
}

function trafficHistoryData(intl: IntlShape, summary: ManagerNetworkSummaryResponse) {
  const history = summaryHistory(summary)
  if (history.traffic.length > 0) {
    return history.traffic.map((point) => ({ label: shortTime(intl, point.at), tx: point.tx_bytes, rx: point.rx_bytes }))
  }
  return [{ label: intl.formatMessage({ id: "network.chart.snapshot" }), tx: summary.traffic.tx_bytes_1m, rx: summary.traffic.rx_bytes_1m }]
}

function messageTypeData(items: ManagerNetworkTrafficMessageType[]) {
  const rows = new Map<string, { label: string; tx: number; rx: number }>()
  for (const item of items) {
    const row = rows.get(item.message_type) ?? { label: item.message_type, tx: 0, rx: 0 }
    if (item.direction === "tx") row.tx += item.bytes_1m
    if (item.direction === "rx") row.rx += item.bytes_1m
    rows.set(item.message_type, row)
  }
  return Array.from(rows.values())
}

function rpcHistoryData(intl: IntlShape, summary: ManagerNetworkSummaryResponse) {
  const history = summaryHistory(summary)
  if (history.rpc.length > 0) {
    return history.rpc.map((point) => ({
      label: shortTime(intl, point.at),
      calls: point.calls,
      success: point.success,
      errors: point.errors,
      expected: point.expected_timeouts,
    }))
  }
  const calls = summary.services.reduce((total, service) => total + service.calls_1m, 0)
  const success = summary.services.reduce((total, service) => total + service.success_1m, 0)
  const expected = summary.services.reduce((total, service) => total + service.expected_timeout_1m, 0)
  const errors = abnormalServiceFailures(summary.services)
  return [{ label: intl.formatMessage({ id: "network.chart.snapshot" }), calls, success, errors, expected }]
}

function peerPoolMiniData(peers: ManagerNetworkPeer[]) {
  return peers.map((peer) => ({
    label: peer.name || `node-${peer.node_id}`,
    clusterActive: peer.pools.cluster.active,
    clusterIdle: peer.pools.cluster.idle,
    dataActive: peer.pools.data_plane.active,
    dataIdle: peer.pools.data_plane.idle,
  }))
}

function longPollLimitData(intl: IntlShape, summary: ManagerNetworkSummaryResponse) {
  return [
    { label: intl.formatMessage({ id: "network.channel.longPollLanes" }), value: summary.channel_replication.long_poll.lane_count },
    { label: intl.formatMessage({ id: "network.channel.longPollWait" }), value: summary.channel_replication.long_poll.max_wait_ms },
    { label: intl.formatMessage({ id: "network.channel.longPollMaxBytes" }), value: summary.channel_replication.long_poll.max_bytes },
    { label: intl.formatMessage({ id: "network.channel.longPollMaxChannels" }), value: summary.channel_replication.long_poll.max_channels },
  ]
}

function abnormalServiceFailures(services: ManagerNetworkRPCService[]) {
  return services.reduce((total, service) => total + service.timeout_1m + service.queue_full_1m + service.remote_error_1m + service.other_error_1m, 0)
}

function abnormalFailureTotal(summary: ManagerNetworkSummaryResponse) {
  const history = summaryHistory(summary)
  const latestErrorPoint = history.errors.at(-1)
  if (latestErrorPoint) {
    return latestErrorPoint.dial_errors + latestErrorPoint.queue_full + latestErrorPoint.timeouts + latestErrorPoint.remote_errors
  }
  return summary.headline.dial_errors_1m + summary.headline.queue_full_1m + summary.headline.timeouts_1m + abnormalServiceFailures(summary.services)
}

function expectedLongPollExpiries(summary: ManagerNetworkSummaryResponse) {
  return Math.max(
    summary.channel_replication.long_poll_timeouts_1m,
    summary.services.reduce((total, service) => total + service.expected_timeout_1m, 0),
    summaryHistory(summary).rpc.reduce((total, point) => Math.max(total, point.expected_timeouts), 0),
  )
}

function historyFallback(intl: IntlShape, history: ManagerNetworkHistory, key: "traffic" | "rpc" | "errors") {
  return history[key].length === 0 ? <p className="mt-2 text-xs text-muted-foreground">{intl.formatMessage({ id: "network.chart.noHistorySnapshot" })}</p> : null
}

function ChartFrame({ children, label }: { children: ReactNode; label: string }) {
  return (
    <div className="rounded-xl border border-border bg-background p-3">
      <div className="mb-2 text-xs font-medium uppercase tracking-[0.14em] text-muted-foreground">{label}</div>
      {children}
    </div>
  )
}

function sourceLabel(intl: IntlShape, key: "local_collector" | "controller_context" | "runtime_views", value: string) {
  return intl.formatMessage({ id: `network.source.${key}` }, { status: value.replaceAll("_", " ") })
}

function isAdvertiseWildcard(addr: string) {
  return addr.startsWith("0.0.0.0") || addr.startsWith("[::]")
}

function ServiceRows({ intl, services }: { intl: IntlShape; services: ManagerNetworkRPCService[] }) {
  if (services.length === 0) {
    return <ResourceState kind="empty" title={intl.formatMessage({ id: "network.rpc.title" })} description={intl.formatMessage({ id: "network.rpc.empty" })} />
  }

  return (
    <div className="overflow-x-auto">
      <table className="min-w-full text-left text-sm">
        <thead className="text-xs uppercase tracking-[0.14em] text-muted-foreground">
          <tr>
            <th className="px-3 py-2">{intl.formatMessage({ id: "network.table.service" })}</th>
            <th className="px-3 py-2">{intl.formatMessage({ id: "network.table.group" })}</th>
            <th className="px-3 py-2">{intl.formatMessage({ id: "network.table.target" })}</th>
            <th className="px-3 py-2">{intl.formatMessage({ id: "network.table.inflight" })}</th>
            <th className="px-3 py-2">{intl.formatMessage({ id: "network.table.calls1m" })}</th>
            <th className="px-3 py-2">{intl.formatMessage({ id: "network.table.p95" })}</th>
            <th className="px-3 py-2">{intl.formatMessage({ id: "network.table.success" })}</th>
            <th className="px-3 py-2">{intl.formatMessage({ id: "network.table.errors" })}</th>
            <th className="px-3 py-2">{intl.formatMessage({ id: "network.table.lastSeen" })}</th>
          </tr>
        </thead>
        <tbody>
          {services.map((service) => {
            const errors = service.timeout_1m + service.queue_full_1m + service.remote_error_1m + service.other_error_1m
            return (
              <tr className="border-t border-border" key={`${service.service_id}-${service.service}-${service.target_node}`}>
                <td className="px-3 py-3 font-medium text-foreground">{service.service}</td>
                <td className="px-3 py-3 text-muted-foreground">{service.group}</td>
                <td className="px-3 py-3 text-muted-foreground">{service.target_node}</td>
                <td className="px-3 py-3 text-muted-foreground">{service.inflight}</td>
                <td className="px-3 py-3 text-muted-foreground">{compactNumber(intl, service.calls_1m)}</td>
                <td className="px-3 py-3 text-muted-foreground">{latency(intl, service.p95_ms)}</td>
                <td className="px-3 py-3 text-muted-foreground">{successRate(intl, service)}</td>
                <td className="px-3 py-3 text-muted-foreground">{compactNumber(intl, errors)}</td>
                <td className="px-3 py-3 text-muted-foreground">{formatTimestamp(intl, service.last_seen_at)}</td>
              </tr>
            )
          })}
        </tbody>
      </table>
    </div>
  )
}

function PeerCard({ intl, peer }: { intl: IntlShape; peer: ManagerNetworkPeer }) {
  const errors = peer.errors.dial_error_1m + peer.errors.queue_full_1m + peer.errors.timeout_1m + peer.errors.remote_error_1m
  const success = peer.rpc.success_rate === null ? intl.formatMessage({ id: "network.latency.insufficientSamples" }) : `${Math.round(peer.rpc.success_rate * 100)}%`
  const poolRows = [
    { label: intl.formatMessage({ id: "network.peer.clusterPool" }), active: peer.pools.cluster.active, idle: peer.pools.cluster.idle },
    { label: intl.formatMessage({ id: "network.peer.dataPlanePool" }), active: peer.pools.data_plane.active, idle: peer.pools.data_plane.idle },
  ]

  return (
    <div className="rounded-xl border border-border bg-background p-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <div className="text-sm font-semibold text-foreground">{peer.name || `node-${peer.node_id}`}</div>
          <div className="mt-1 text-xs text-muted-foreground">node {peer.node_id} - {peer.addr}</div>
        </div>
        <StatusBadge value={peer.health} />
      </div>
      <ChartContainer className="mt-4 h-36" config={networkChartConfig(intl)}>
        <BarChart accessibilityLayer data={poolRows} layout="vertical" margin={{ left: 12, right: 28 }}>
          <CartesianGrid horizontal={false} />
          <XAxis hide type="number" />
          <YAxis dataKey="label" tickLine={false} type="category" width={110} />
          <ChartTooltip content={<ChartTooltipContent />} />
          <Bar dataKey="active" fill="var(--color-active)" stackId="pool" radius={[4, 0, 0, 4]} />
          <Bar dataKey="idle" fill="var(--color-idle)" stackId="pool" radius={[0, 4, 4, 0]}>
            <LabelList dataKey="idle" position="right" />
          </Bar>
        </BarChart>
      </ChartContainer>
      <div className="mt-4 grid gap-3 md:grid-cols-2 xl:grid-cols-4">
        <MetricCard label={intl.formatMessage({ id: "network.peer.clusterPool" })} value={`${peer.pools.cluster.active}/${peer.pools.cluster.idle}`} detail={intl.formatMessage({ id: "network.pool.activeIdle" })} />
        <MetricCard label={intl.formatMessage({ id: "network.peer.dataPlanePool" })} value={`${peer.pools.data_plane.active}/${peer.pools.data_plane.idle}`} detail={intl.formatMessage({ id: "network.pool.activeIdle" })} />
        <MetricCard label={intl.formatMessage({ id: "network.peer.rpc" })} value={compactNumber(intl, peer.rpc.inflight)} detail={`${compactNumber(intl, peer.rpc.calls_1m)} calls - p95 ${latency(intl, peer.rpc.p95_ms)} - ${success}`} />
        <MetricCard label={intl.formatMessage({ id: "network.summary.errors" })} value={compactNumber(intl, errors)} />
      </div>
    </div>
  )
}

function DiscoveryList({ intl, discovery, dataPlaneTimeoutMs }: { intl: IntlShape; discovery: ManagerNetworkDiscovery; dataPlaneTimeoutMs: number }) {
  const items = [
    [intl.formatMessage({ id: "network.discovery.listen" }), discovery.listen_addr],
    [intl.formatMessage({ id: "network.discovery.advertise" }), discovery.advertise_addr],
    [intl.formatMessage({ id: "network.discovery.seeds" }), discovery.seeds.length ? discovery.seeds.join(", ") : "-"],
    [intl.formatMessage({ id: "network.discovery.staticNodes" }), discovery.static_nodes.length ? discovery.static_nodes.map((node) => `node ${node.node_id} ${node.addr}`).join(", ") : "-"],
    [intl.formatMessage({ id: "network.discovery.poolSize" }), compactNumber(intl, discovery.pool_size)],
    [intl.formatMessage({ id: "network.discovery.dataPlanePoolSize" }), compactNumber(intl, discovery.data_plane_pool_size)],
    [intl.formatMessage({ id: "network.discovery.dialTimeout" }), `${compactNumber(intl, discovery.dial_timeout_ms)} ms`],
    [intl.formatMessage({ id: "network.discovery.controllerObservation" }), `${compactNumber(intl, discovery.controller_observation_interval_ms)} ms`],
    [intl.formatMessage({ id: "network.channel.dataPlaneTimeout" }), `${compactNumber(intl, dataPlaneTimeoutMs)} ms`],
  ]

  return (
    <div className="space-y-3">
      {isAdvertiseWildcard(discovery.advertise_addr) ? (
        <div className="rounded-lg border border-border bg-muted/40 px-3 py-3 text-sm text-muted-foreground">
          {intl.formatMessage({ id: "network.discovery.advertiseWarning" })}
        </div>
      ) : null}
      <dl className="grid gap-3 md:grid-cols-2">
        {items.map(([label, value]) => (
          <div className="rounded-lg border border-border bg-background px-3 py-3" key={label}>
            <dt className="text-xs uppercase tracking-[0.14em] text-muted-foreground">{label}</dt>
            <dd className="mt-1 break-words text-sm font-medium text-foreground">{value}</dd>
          </div>
        ))}
      </dl>
    </div>
  )
}

function EventList({ intl, events }: { intl: IntlShape; events: ManagerNetworkEvent[] }) {
  if (events.length === 0) {
    return <ResourceState kind="empty" title={intl.formatMessage({ id: "network.events.title" })} description={intl.formatMessage({ id: "network.events.empty" })} />
  }
  return (
    <div className="space-y-3">
      {events.map((event) => (
        <div className="rounded-lg border border-border bg-background px-3 py-3" key={`${event.at}-${event.kind}-${event.target_node}-${event.service}`}>
          <div className="flex flex-wrap items-center gap-2 text-sm font-medium text-foreground">
            <StatusBadge value={event.severity} />
            <span>{event.kind}</span>
            <span className="text-muted-foreground">node {event.target_node}</span>
          </div>
          <div className="mt-2 text-sm text-muted-foreground">{event.service ? `${event.service} - ${event.message}` : event.message}</div>
          <div className="mt-1 text-xs text-muted-foreground">{formatTimestamp(intl, event.at)}</div>
        </div>
      ))}
    </div>
  )
}

export function NetworkPage() {
  const intl = useIntl()
  const [state, setState] = useState<NetworkState>({ summary: null, loading: true, refreshing: false, error: null })

  const loadNetwork = useCallback(async (refreshing: boolean) => {
    setState((current) => ({ ...current, loading: refreshing ? current.loading : true, refreshing, error: null }))
    try {
      const summary = await getNetworkSummary()
      setState({ summary, loading: false, refreshing: false, error: null })
    } catch (error) {
      setState({ summary: null, loading: false, refreshing: false, error: error instanceof Error ? error : new Error("network summary request failed") })
    }
  }, [])

  useEffect(() => {
    void loadNetwork(false)
  }, [loadNetwork])

  const summary = state.summary
  const channelServices = useMemo(() => summary?.channel_replication.services.filter((service) => service.group === "channel_data_plane") ?? [], [summary])
  const errorKind = mapErrorKind(state.error)
  const charts = useMemo(() => {
    if (!summary) return null
    return {
      health: nodeHealthData(intl, summary),
      pools: headlinePoolData(intl, summary),
      errors: errorMixData(intl, summary),
      trafficHistory: trafficHistoryData(intl, summary),
      messageTypes: messageTypeData(summary.traffic.by_message_type),
      rpcHistory: rpcHistoryData(intl, summary),
      peerPools: peerPoolMiniData(summary.peers),
      channelPool: poolData(intl.formatMessage({ id: "network.channel.dataPlanePool" }), summary.channel_replication.pool),
      longPollLimits: longPollLimitData(intl, summary),
      expectedExpiries: expectedLongPollExpiries(summary),
      abnormalFailures: abnormalFailureTotal(summary),
    }
  }, [intl, summary])

  return (
    <PageContainer>
      <PageHeader
        title={intl.formatMessage({ id: "network.title" })}
        description={intl.formatMessage({ id: "network.description" })}
        actions={<Button onClick={() => { void loadNetwork(true) }} size="sm" variant="outline">{state.refreshing ? intl.formatMessage({ id: "common.refreshing" }) : intl.formatMessage({ id: "common.refresh" })}</Button>}
      >
        <div className="flex flex-wrap gap-2 text-xs text-muted-foreground">
          <HeaderBadge>{intl.formatMessage({ id: "network.scope.localNode" })}</HeaderBadge>
          {summary ? <HeaderBadge>{intl.formatMessage({ id: "network.scope.localNodeValue" }, { id: summary.scope.local_node_id })}</HeaderBadge> : null}
          {summary ? <HeaderBadge>{intl.formatMessage({ id: "network.scope.controllerLeaderValue" }, { id: summary.scope.controller_leader_id })}</HeaderBadge> : null}
          {summary ? <HeaderBadge>{intl.formatMessage({ id: "network.scope.generatedValue" }, { value: formatTimestamp(intl, summary.generated_at) })}</HeaderBadge> : null}
          {summary?.headline.remote_peers === 0 ? <HeaderBadge>{intl.formatMessage({ id: "network.scope.singleNodeCluster" })}</HeaderBadge> : null}
        </div>
      </PageHeader>

      {state.loading ? <ResourceState kind="loading" title={intl.formatMessage({ id: "network.title" })} description={intl.formatMessage({ id: "network.loading" })} /> : null}
      {!state.loading && state.error ? <ResourceState kind={errorKind} onRetry={() => { void loadNetwork(false) }} title={resourceTitle(intl, state.error)} /> : null}

      {!state.loading && !state.error && summary && charts ? (
        <>
          <section className="grid gap-3 md:grid-cols-2 xl:grid-cols-6">
            <MetricCard label={intl.formatMessage({ id: "network.summary.remotePeers" })} value={summary.headline.remote_peers} />
            <MetricCard label={intl.formatMessage({ id: "network.summary.nodeHealth" })} value={`${summary.headline.alive_nodes}/${summary.headline.suspect_nodes}/${summary.headline.dead_nodes}/${summary.headline.draining_nodes}`} detail={intl.formatMessage({ id: "network.summary.nodeHealthDetail" })} />
            <MetricCard label={intl.formatMessage({ id: "network.summary.poolConnections" })} value={`${summary.headline.pool_active}/${summary.headline.pool_idle}`} detail={intl.formatMessage({ id: "network.pool.activeIdle" })} />
            <MetricCard label={intl.formatMessage({ id: "network.summary.rpcInflight" })} value={summary.headline.rpc_inflight} />
            <MetricCard label={intl.formatMessage({ id: "network.summary.errors" })} value={summary.headline.dial_errors_1m + summary.headline.queue_full_1m + summary.headline.timeouts_1m} detail={intl.formatMessage({ id: "network.summary.errorsDetail" })} tone="danger" />
            <MetricCard label={intl.formatMessage({ id: "network.summary.freshness" })} value={summary.headline.stale_observations} />
          </section>

          <section className="grid gap-4 xl:grid-cols-[0.9fr_1.1fr]">
            <SectionCard title={intl.formatMessage({ id: "network.chart.nodeHealth" })} description={intl.formatMessage({ id: "network.chart.nodeHealthDescription" })}>
              <ChartFrame label={intl.formatMessage({ id: "network.chart.distribution" })}>
                <ChartContainer className="mx-auto h-56 max-w-sm" config={networkChartConfig(intl)}>
                  <PieChart accessibilityLayer>
                    <ChartTooltip content={<ChartTooltipContent hideLabel />} />
                    <Pie data={charts.health} dataKey="value" innerRadius={54} nameKey="name" outerRadius={84}>
                      {charts.health.map((entry) => <Cell fill={entry.fill} key={entry.key} />)}
                      <LabelList dataKey="value" position="outside" />
                    </Pie>
                    <ChartLegend content={<ChartLegendContent nameKey="key" />} />
                  </PieChart>
                </ChartContainer>
              </ChartFrame>
              <div className="mt-3 grid gap-2 sm:grid-cols-4">
                {charts.health.map((item) => <MetricCard detail={intl.formatMessage({ id: `network.health.${item.key}` })} key={item.key} label={item.name} value={item.value} />)}
              </div>
              <div className="mt-4 grid gap-4 lg:grid-cols-2">
                <ChartFrame label={intl.formatMessage({ id: "network.chart.poolConnections" })}>
                  <ChartContainer className="h-40" config={networkChartConfig(intl)}>
                    <BarChart accessibilityLayer data={charts.pools} layout="vertical" margin={{ left: 12, right: 28 }}>
                      <CartesianGrid horizontal={false} />
                      <XAxis hide type="number" />
                      <YAxis dataKey="label" tickLine={false} type="category" width={90} />
                      <ChartTooltip content={<ChartTooltipContent />} />
                      <Bar dataKey="active" fill="var(--color-active)" stackId="pool" radius={[4, 0, 0, 4]} />
                      <Bar dataKey="idle" fill="var(--color-idle)" stackId="pool" radius={[0, 4, 4, 0]} />
                    </BarChart>
                  </ChartContainer>
                </ChartFrame>
                <ChartFrame label={intl.formatMessage({ id: "network.chart.errorMix" })}>
                  <ChartContainer className="h-40" config={networkChartConfig(intl)}>
                    <BarChart accessibilityLayer data={charts.errors} layout="vertical" margin={{ left: 12, right: 28 }}>
                      <CartesianGrid horizontal={false} />
                      <XAxis hide type="number" />
                      <YAxis dataKey="label" tickLine={false} type="category" width={120} />
                      <ChartTooltip content={<ChartTooltipContent />} />
                      <Bar dataKey="dial" fill="var(--color-dial)" stackId="errors" />
                      <Bar dataKey="queue" fill="var(--color-queue)" stackId="errors" />
                      <Bar dataKey="timeout" fill="var(--color-timeout)" stackId="errors" radius={[0, 4, 4, 0]} />
                    </BarChart>
                  </ChartContainer>
                </ChartFrame>
              </div>
            </SectionCard>

            <SectionCard title={intl.formatMessage({ id: "network.chart.trafficTrend" })} description={intl.formatMessage({ id: "network.chart.trafficTrendDescription" })}>
              <ChartFrame label={intl.formatMessage({ id: "network.chart.bytesOverWindow" }, { seconds: summaryHistory(summary).window_seconds })}>
                <ChartContainer className="h-64" config={networkChartConfig(intl)}>
                  <AreaChart accessibilityLayer data={charts.trafficHistory} margin={{ left: 12, right: 12 }}>
                    <CartesianGrid vertical={false} />
                    <XAxis dataKey="label" tickLine={false} />
                    <YAxis tickFormatter={(value) => compactNumber(intl, Number(value))} tickLine={false} />
                    <ChartTooltip content={<ChartTooltipContent />} />
                    <Area dataKey="tx" fill="var(--color-tx)" fillOpacity={0.25} stroke="var(--color-tx)" type="monotone" />
                    <Area dataKey="rx" fill="var(--color-rx)" fillOpacity={0.25} stroke="var(--color-rx)" type="monotone" />
                    <ChartLegend content={<ChartLegendContent />} />
                  </AreaChart>
                </ChartContainer>
              </ChartFrame>
              {historyFallback(intl, summaryHistory(summary), "traffic")}
              <div className="mt-3 grid gap-3 md:grid-cols-2">
                <MetricCard label={intl.formatMessage({ id: "network.traffic.txTotal" })} value={bytes(intl, summary.traffic.tx_bytes_1m)} detail={`${compactNumber(intl, summary.traffic.tx_bps)} bps`} />
                <MetricCard label={intl.formatMessage({ id: "network.traffic.rxTotal" })} value={bytes(intl, summary.traffic.rx_bytes_1m)} detail={`${compactNumber(intl, summary.traffic.rx_bps)} bps`} />
              </div>
            </SectionCard>
          </section>

          <section className="grid gap-4 xl:grid-cols-[1.1fr_0.9fr]">
            <SectionCard title={intl.formatMessage({ id: "network.chart.trafficByMessageType" })} description={intl.formatMessage({ id: "network.traffic.localTotalChart" })}>
              <ChartFrame label={intl.formatMessage({ id: "network.chart.localMessageBytes" })}>
                <ChartContainer className="h-72" config={networkChartConfig(intl)}>
                  <BarChart accessibilityLayer data={charts.messageTypes} layout="vertical" margin={{ left: 18, right: 36 }}>
                    <CartesianGrid horizontal={false} />
                    <XAxis type="number" tickFormatter={(value) => compactNumber(intl, Number(value))} />
                    <YAxis dataKey="label" tickLine={false} type="category" width={150} />
                    <ChartTooltip content={<ChartTooltipContent />} />
                    <Bar dataKey="tx" fill="var(--color-tx)" radius={[0, 4, 4, 0]} />
                    <Bar dataKey="rx" fill="var(--color-rx)" radius={[0, 4, 4, 0]}>
                      <LabelList dataKey="rx" position="right" />
                    </Bar>
                    <ChartLegend content={<ChartLegendContent />} />
                  </BarChart>
                </ChartContainer>
              </ChartFrame>
              <p className="mt-3 rounded-lg border border-border bg-muted/30 px-3 py-2 text-sm text-muted-foreground">{intl.formatMessage({ id: "network.traffic.localTotal" })}</p>
            </SectionCard>

            <SectionCard title={intl.formatMessage({ id: "network.chart.rpcCallsErrors" })} description={intl.formatMessage({ id: "network.rpc.description" })}>
              <ChartFrame label={intl.formatMessage({ id: "network.chart.rpcWindow" })}>
                <ChartContainer className="h-72" config={networkChartConfig(intl)}>
                  <BarChart accessibilityLayer data={charts.rpcHistory}>
                    <CartesianGrid vertical={false} />
                    <XAxis dataKey="label" tickLine={false} />
                    <YAxis tickFormatter={(value) => compactNumber(intl, Number(value))} tickLine={false} />
                    <ChartTooltip content={<ChartTooltipContent />} />
                    <Bar dataKey="success" fill="var(--color-success)" stackId="rpc" radius={[4, 4, 0, 0]} />
                    <Bar dataKey="errors" fill="var(--color-errors)" stackId="rpc" />
                    <Bar dataKey="expected" fill="var(--color-expected)" radius={[4, 4, 0, 0]} />
                    <ChartLegend content={<ChartLegendContent />} />
                  </BarChart>
                </ChartContainer>
              </ChartFrame>
              {historyFallback(intl, summaryHistory(summary), "rpc")}
              <div className="mt-3 grid gap-3 md:grid-cols-2">
                <MetricCard label={intl.formatMessage({ id: "network.rpc.expectedLongPollExpiries" })} value={compactNumber(intl, charts.expectedExpiries)} detail={intl.formatMessage({ id: "network.rpc.expectedLongPollExpiriesDetail" })} tone="neutral" />
                <MetricCard label={intl.formatMessage({ id: "network.rpc.abnormalFailures" })} value={compactNumber(intl, charts.abnormalFailures)} detail={intl.formatMessage({ id: "network.rpc.abnormalFailuresDetail" })} tone="danger" />
              </div>
            </SectionCard>
          </section>

          <section className="grid gap-4 xl:grid-cols-[0.9fr_1.1fr]">
            <SectionCard title={intl.formatMessage({ id: "network.chart.peerPoolBalance" })} description={intl.formatMessage({ id: "network.outbound.description" })}>
              {summary.peers.length === 0 ? (
                <ResourceState kind="empty" title={intl.formatMessage({ id: "network.chart.peerPoolBalance" })} description={intl.formatMessage({ id: "network.outbound.singleNodeEmpty" })} />
              ) : (
                <>
                  <ChartFrame label={intl.formatMessage({ id: "network.chart.peerPools" })}>
                    <ChartContainer className="h-72" config={networkChartConfig(intl)}>
                      <BarChart accessibilityLayer data={charts.peerPools} margin={{ left: 12, right: 12 }}>
                        <CartesianGrid vertical={false} />
                        <XAxis dataKey="label" tickLine={false} />
                        <YAxis tickLine={false} />
                        <ChartTooltip content={<ChartTooltipContent />} />
                        <Bar dataKey="clusterActive" fill="var(--color-active)" stackId="cluster" />
                        <Bar dataKey="clusterIdle" fill="var(--color-idle)" stackId="cluster" />
                        <Bar dataKey="dataActive" fill="var(--color-tx)" stackId="data" />
                        <Bar dataKey="dataIdle" fill="var(--color-rx)" stackId="data" />
                      </BarChart>
                    </ChartContainer>
                  </ChartFrame>
                  <div className="mt-4 space-y-3">{summary.peers.map((peer) => <PeerCard intl={intl} key={peer.node_id} peer={peer} />)}</div>
                </>
              )}
            </SectionCard>

            <SectionCard title={intl.formatMessage({ id: "network.chart.channelDataPlane" })} description={intl.formatMessage({ id: "network.channel.description" })}>
              <div className="grid gap-4 lg:grid-cols-2">
                <ChartFrame label={intl.formatMessage({ id: "network.channel.dataPlanePool" })}>
                  <ChartContainer className="h-56" config={networkChartConfig(intl)}>
                    <BarChart accessibilityLayer data={charts.channelPool} layout="vertical" margin={{ left: 12, right: 28 }}>
                      <CartesianGrid horizontal={false} />
                      <XAxis hide type="number" />
                      <YAxis dataKey="label" tickLine={false} type="category" width={130} />
                      <ChartTooltip content={<ChartTooltipContent />} />
                      <Bar dataKey="active" fill="var(--color-active)" stackId="pool" radius={[4, 0, 0, 4]} />
                      <Bar dataKey="idle" fill="var(--color-idle)" stackId="pool" radius={[0, 4, 4, 0]}>
                        <LabelList dataKey="idle" position="right" />
                      </Bar>
                    </BarChart>
                  </ChartContainer>
                </ChartFrame>
                <ChartFrame label={intl.formatMessage({ id: "network.chart.longPollLimits" })}>
                  <ChartContainer className="h-56" config={{ value: { label: intl.formatMessage({ id: "network.legend.value" }), color: chartColors.calls } }}>
                    <BarChart accessibilityLayer data={charts.longPollLimits} layout="vertical" margin={{ left: 12, right: 30 }}>
                      <CartesianGrid horizontal={false} />
                      <XAxis hide type="number" />
                      <YAxis dataKey="label" tickLine={false} type="category" width={110} />
                      <ChartTooltip content={<ChartTooltipContent />} />
                      <Bar dataKey="value" fill="var(--color-value)" radius={4}>
                        <LabelList dataKey="value" formatter={(value) => compactNumber(intl, Number(value ?? 0))} position="right" />
                      </Bar>
                    </BarChart>
                  </ChartContainer>
                </ChartFrame>
              </div>
              <div className="mt-4 grid gap-3 md:grid-cols-2 xl:grid-cols-5">
                <MetricCard label={intl.formatMessage({ id: "network.channel.longPollLanes" })} value={summary.channel_replication.long_poll.lane_count} />
                <MetricCard label={intl.formatMessage({ id: "network.channel.longPollWait" })} value={`${compactNumber(intl, summary.channel_replication.long_poll.max_wait_ms)} ms`} />
                <MetricCard label={intl.formatMessage({ id: "network.channel.longPollMaxBytes" })} value={bytes(intl, summary.channel_replication.long_poll.max_bytes)} />
                <MetricCard label={intl.formatMessage({ id: "network.channel.longPollMaxChannels" })} value={compactNumber(intl, summary.channel_replication.long_poll.max_channels)} />
                <MetricCard label={intl.formatMessage({ id: "network.channel.dataPlaneTimeout" })} value={`${compactNumber(intl, summary.channel_replication.data_plane_rpc_timeout_ms)} ms`} />
              </div>
              <div className="mt-4 grid gap-3 md:grid-cols-2">
                <MetricCard label={intl.formatMessage({ id: "network.rpc.expectedLongPollExpiries" })} value={compactNumber(intl, charts.expectedExpiries)} detail={intl.formatMessage({ id: "network.rpc.expectedLongPollExpiriesDetail" })} tone="neutral" />
                <MetricCard label={intl.formatMessage({ id: "network.rpc.abnormalFailures" })} value={compactNumber(intl, charts.abnormalFailures)} detail={intl.formatMessage({ id: "network.rpc.abnormalFailuresDetail" })} tone="danger" />
              </div>
              <div className="mt-4"><ServiceRows intl={intl} services={channelServices} /></div>
            </SectionCard>
          </section>

          <section className="grid gap-4 xl:grid-cols-[0.8fr_1.2fr]">
            <SectionCard title={intl.formatMessage({ id: "network.source.title" })}>
              <div className="flex flex-wrap gap-2 text-sm text-foreground">
                <StatusBadge value={summary.source_status.local_collector} />
                <span>{sourceLabel(intl, "local_collector", summary.source_status.local_collector)}</span>
                <StatusBadge value={summary.source_status.controller_context} />
                <span>{sourceLabel(intl, "controller_context", summary.source_status.controller_context)}</span>
                <StatusBadge value={summary.source_status.runtime_views} />
                <span>{sourceLabel(intl, "runtime_views", summary.source_status.runtime_views)}</span>
              </div>
            </SectionCard>

            <SectionCard title={intl.formatMessage({ id: "network.rpc.title" })} description={intl.formatMessage({ id: "network.rpc.description" })}>
              <ServiceRows intl={intl} services={summary.services} />
            </SectionCard>
          </section>

          <section className="grid gap-4 xl:grid-cols-2">
            <SectionCard title={intl.formatMessage({ id: "network.discovery.title" })} description={intl.formatMessage({ id: "network.discovery.description" })}>
              <DiscoveryList intl={intl} discovery={summary.discovery} dataPlaneTimeoutMs={summary.channel_replication.data_plane_rpc_timeout_ms} />
            </SectionCard>
            <SectionCard title={intl.formatMessage({ id: "network.events.title" })} description={intl.formatMessage({ id: "network.events.description" })}>
              <EventList intl={intl} events={summary.events} />
            </SectionCard>
          </section>
        </>
      ) : null}
    </PageContainer>
  )
}
