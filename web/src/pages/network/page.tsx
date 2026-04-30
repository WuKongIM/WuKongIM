import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react"
import { useIntl, type IntlShape } from "react-intl"

import { ResourceState } from "@/components/manager/resource-state"
import { StatusBadge } from "@/components/manager/status-badge"
import { Button } from "@/components/ui/button"
import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
import { SectionCard } from "@/components/shell/section-card"
import { ManagerApiError, getNetworkSummary } from "@/lib/manager-api"
import type {
  ManagerNetworkDiscovery,
  ManagerNetworkEvent,
  ManagerNetworkPeer,
  ManagerNetworkRPCService,
  ManagerNetworkSummaryResponse,
} from "@/lib/manager-api.types"

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

function MetricCard({ label, value, detail }: { label: string; value: string | number; detail?: string }) {
  return (
    <div className="rounded-xl border border-border bg-muted/25 px-4 py-4">
      <div className="text-xs uppercase tracking-[0.16em] text-muted-foreground">{label}</div>
      <div className="mt-2 text-2xl font-semibold text-foreground">{value}</div>
      {detail ? <div className="mt-1 text-xs text-muted-foreground">{detail}</div> : null}
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

  return (
    <div className="rounded-xl border border-border bg-background p-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <div className="text-sm font-semibold text-foreground">{peer.name || `node-${peer.node_id}`}</div>
          <div className="mt-1 text-xs text-muted-foreground">node {peer.node_id} - {peer.addr}</div>
        </div>
        <StatusBadge value={peer.health} />
      </div>
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

      {!state.loading && !state.error && summary ? (
        <>
          <section className="grid gap-3 md:grid-cols-2 xl:grid-cols-6">
            <MetricCard label={intl.formatMessage({ id: "network.summary.remotePeers" })} value={summary.headline.remote_peers} />
            <MetricCard label={intl.formatMessage({ id: "network.summary.nodeHealth" })} value={`${summary.headline.alive_nodes}/${summary.headline.suspect_nodes}/${summary.headline.dead_nodes}/${summary.headline.draining_nodes}`} detail={intl.formatMessage({ id: "network.summary.nodeHealthDetail" })} />
            <MetricCard label={intl.formatMessage({ id: "network.summary.poolConnections" })} value={`${summary.headline.pool_active}/${summary.headline.pool_idle}`} detail={intl.formatMessage({ id: "network.pool.activeIdle" })} />
            <MetricCard label={intl.formatMessage({ id: "network.summary.rpcInflight" })} value={summary.headline.rpc_inflight} />
            <MetricCard label={intl.formatMessage({ id: "network.summary.errors" })} value={summary.headline.dial_errors_1m + summary.headline.queue_full_1m + summary.headline.timeouts_1m} detail={intl.formatMessage({ id: "network.summary.errorsDetail" })} />
            <MetricCard label={intl.formatMessage({ id: "network.summary.freshness" })} value={summary.headline.stale_observations} />
          </section>

          <section className="grid gap-4 xl:grid-cols-[0.85fr_1.15fr]">
            <SectionCard title={intl.formatMessage({ id: "network.summary.title" })} description={intl.formatMessage({ id: "network.traffic.localTotal" })}>
              <div className="grid gap-3 md:grid-cols-2">
                <MetricCard label="TX" value={`${compactNumber(intl, summary.traffic.tx_bytes_1m)} B`} detail={`${compactNumber(intl, summary.traffic.tx_bps)} bps`} />
                <MetricCard label="RX" value={`${compactNumber(intl, summary.traffic.rx_bytes_1m)} B`} detail={`${compactNumber(intl, summary.traffic.rx_bps)} bps`} />
              </div>
              <div className="mt-4 space-y-2">
                {summary.traffic.by_message_type.map((item) => (
                  <div className="flex items-center justify-between rounded-lg border border-border bg-background px-3 py-2 text-sm" key={`${item.direction}-${item.message_type}`}>
                    <span className="font-medium text-foreground">{item.message_type}</span>
                    <span className="text-muted-foreground">{item.direction} - {compactNumber(intl, item.bytes_1m)} B - {compactNumber(intl, item.bps)} bps</span>
                  </div>
                ))}
              </div>
            </SectionCard>
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
          </section>

          <SectionCard title={intl.formatMessage({ id: "network.outbound.title" })} description={intl.formatMessage({ id: "network.outbound.description" })}>
            {summary.peers.length === 0 ? (
              <ResourceState kind="empty" title={intl.formatMessage({ id: "network.outbound.title" })} description={intl.formatMessage({ id: "network.outbound.singleNodeEmpty" })} />
            ) : (
              <div className="space-y-3">{summary.peers.map((peer) => <PeerCard intl={intl} key={peer.node_id} peer={peer} />)}</div>
            )}
          </SectionCard>

          <SectionCard title={intl.formatMessage({ id: "network.rpc.title" })} description={intl.formatMessage({ id: "network.rpc.description" })}>
            <ServiceRows intl={intl} services={summary.services} />
          </SectionCard>

          <SectionCard title={intl.formatMessage({ id: "network.channel.title" })} description={intl.formatMessage({ id: "network.channel.description" })}>
            <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-7">
              <MetricCard label={intl.formatMessage({ id: "network.channel.dataPlanePool" })} value={`${summary.channel_replication.pool.active}/${summary.channel_replication.pool.idle}`} detail={intl.formatMessage({ id: "network.pool.activeIdle" })} />
              <MetricCard label={intl.formatMessage({ id: "network.channel.longPollLanes" })} value={summary.channel_replication.long_poll.lane_count} />
              <MetricCard label={intl.formatMessage({ id: "network.channel.longPollWait" })} value={`${compactNumber(intl, summary.channel_replication.long_poll.max_wait_ms)} ms`} />
              <MetricCard label={intl.formatMessage({ id: "network.channel.longPollMaxBytes" })} value={`${compactNumber(intl, summary.channel_replication.long_poll.max_bytes)} B`} />
              <MetricCard label={intl.formatMessage({ id: "network.channel.longPollMaxChannels" })} value={compactNumber(intl, summary.channel_replication.long_poll.max_channels)} />
              <MetricCard label={intl.formatMessage({ id: "network.channel.longPollExpiries" })} value={summary.channel_replication.long_poll_timeouts_1m} />
              <MetricCard label={intl.formatMessage({ id: "network.channel.dataPlaneTimeout" })} value={`${compactNumber(intl, summary.channel_replication.data_plane_rpc_timeout_ms)} ms`} />
            </div>
            <div className="mt-4"><ServiceRows intl={intl} services={channelServices} /></div>
          </SectionCard>

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
