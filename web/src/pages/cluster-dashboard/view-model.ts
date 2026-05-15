import type { IntlShape } from "react-intl"

import type {
  ManagerChannelClusterSummaryResponse,
  ManagerNetworkEvent,
  ManagerNetworkSummaryResponse,
  ManagerNodesResponse,
  ManagerOverviewResponse,
} from "@/lib/manager-api.types"

export type MetricSource = "real" | "derived" | "sample"
export type ClusterVerdict = "healthy" | "degraded" | "critical"

export type ClusterMetricValue = {
  key: string
  labelId: string
  value: number
  formatted: string
  detail: string
  tone: "default" | "warning" | "danger"
  source: MetricSource
  href?: string
}

export type ClusterIncident = {
  key: string
  severity: "critical" | "warning"
  title: string
  detail: string
  href: string
  ariaLabel: string
}

export type ClusterLinkMetrics = {
  internalMessagesPerSecond: ClusterMetricValue
  rpcCallsPerSecond: ClusterMetricValue
  rpcErrorRate: ClusterMetricValue
  rpcLatencyP95: ClusterMetricValue
  rpcInflight: ClusterMetricValue
  queueFull: ClusterMetricValue
  timeouts: ClusterMetricValue
  trafficSeries: Array<{ at: string; txBytes: number; rxBytes: number }>
  rpcSeries: Array<{ at: string; calls: number; errors: number; expectedTimeouts: number }>
}

export type ClusterTopologyRow = {
  nodeId: number
  name: string
  status: string
  isLocal: boolean
  role: string
  slotsCount: number
  leaderCount: number
  raftHealth: string
  hasWatermark: boolean
  firstIndex: number
  appliedIndex: number
  snapshotIndex: number
  rpcErrorRate: number | null
  rpcP95Ms: number | null
  href: string
}

const CRITICAL_RAFT_STATES = new Set(["restore_failed"])
const DEGRADED_RAFT_STATES = new Set([
  "snapshot_required",
  "snapshot_transferring",
  "append_catchup",
  "compaction_degraded",
])

export function computeClusterVerdict(
  overview: ManagerOverviewResponse,
  nodes: ManagerNodesResponse,
): ClusterVerdict {
  if (overview.tasks.failed > 0) return "critical"
  if (nodes.items.some((node) => node.controller.raft_health && CRITICAL_RAFT_STATES.has(node.controller.raft_health))) {
    return "critical"
  }
  if (overview.slots.quorum_lost > 0 || overview.slots.leader_missing > 0 || overview.tasks.retrying > 0) {
    return "degraded"
  }
  if (nodes.items.some((node) => node.controller.raft_health && DEGRADED_RAFT_STATES.has(node.controller.raft_health))) {
    return "degraded"
  }
  return "healthy"
}

function raftSeverity(raftHealth: string): "critical" | "warning" {
  return CRITICAL_RAFT_STATES.has(raftHealth) ? "critical" : "warning"
}

export function buildClusterIncidents(
  intl: Pick<IntlShape, "formatMessage">,
  overview: ManagerOverviewResponse,
  nodes: ManagerNodesResponse,
  network?: ManagerNetworkSummaryResponse | null,
): ClusterIncident[] {
  const incidents: ClusterIncident[] = []

  for (const item of overview.anomalies.slots.quorum_lost.items) {
    const title = intl.formatMessage({ id: "clusterDashboard.incidents.slotQuorumLostTitle" }, { id: item.slot_id })
    incidents.push({
      key: `slot-quorum-lost-${item.slot_id}`,
      severity: "critical",
      title,
      detail: intl.formatMessage(
        { id: "clusterDashboard.incidents.slotPeers" },
        { desired: item.desired_peers.join(", "), current: item.current_peers.join(", ") },
      ),
      href: `/cluster/slots?focus=${item.slot_id}`,
      ariaLabel: title,
    })
  }

  for (const item of overview.anomalies.slots.leader_missing.items) {
    const title = intl.formatMessage({ id: "clusterDashboard.incidents.slotLeaderMissingTitle" }, { id: item.slot_id })
    incidents.push({
      key: `slot-leader-missing-${item.slot_id}`,
      severity: "critical",
      title,
      detail: "",
      href: `/cluster/slots?focus=${item.slot_id}`,
      ariaLabel: title,
    })
  }

  for (const item of overview.anomalies.slots.sync_mismatch.items) {
    const title = intl.formatMessage({ id: "clusterDashboard.incidents.slotSyncMismatchTitle" }, { id: item.slot_id })
    incidents.push({
      key: `slot-sync-mismatch-${item.slot_id}`,
      severity: "warning",
      title,
      detail: "",
      href: `/cluster/slots?focus=${item.slot_id}`,
      ariaLabel: title,
    })
  }

  for (const item of overview.anomalies.tasks.failed.items) {
    const title = intl.formatMessage({ id: "clusterDashboard.incidents.taskFailedTitle" }, { kind: item.kind, id: item.slot_id })
    incidents.push({
      key: `task-failed-${item.slot_id}-${item.kind}`,
      severity: "critical",
      title,
      detail: intl.formatMessage(
        { id: "clusterDashboard.incidents.taskDetail" },
        { kind: item.kind, step: item.step, attempt: item.attempt, error: item.last_error },
      ),
      href: `/cluster/tasks?focus=slot-${item.slot_id}`,
      ariaLabel: title,
    })
  }

  for (const item of overview.anomalies.tasks.retrying.items) {
    const title = intl.formatMessage({ id: "clusterDashboard.incidents.taskRetryingTitle" }, { kind: item.kind, id: item.slot_id })
    incidents.push({
      key: `task-retrying-${item.slot_id}-${item.kind}`,
      severity: "warning",
      title,
      detail: "",
      href: `/cluster/tasks?focus=slot-${item.slot_id}`,
      ariaLabel: title,
    })
  }

  for (const node of nodes.items) {
    const raftHealth = node.controller.raft_health
    if (!raftHealth || raftHealth === "healthy" || raftHealth === "unknown") continue
    if (!CRITICAL_RAFT_STATES.has(raftHealth) && !DEGRADED_RAFT_STATES.has(raftHealth)) continue

    const title = intl.formatMessage(
      { id: "clusterDashboard.incidents.controllerRaftTitle" },
      { health: raftHealth.replace(/_/g, " "), id: node.node_id },
    )
    incidents.push({
      key: `controller-raft-${node.node_id}`,
      severity: raftSeverity(raftHealth),
      title,
      detail: intl.formatMessage(
        { id: "clusterDashboard.incidents.controllerRaftDetail" },
        {
          first: node.controller.first_index ?? 0,
          applied: node.controller.applied_index ?? 0,
          snapshot: node.controller.snapshot_index ?? 0,
        },
      ),
      href: `/cluster/diagnostics?tab=controller-logs&node_id=${node.node_id}`,
      ariaLabel: title,
    })
  }

  for (const [index, event] of (network?.events ?? []).entries()) {
    incidents.push(networkEventIncident(intl, event, index))
  }

  return incidents
}

function networkEventIncident(
  intl: Pick<IntlShape, "formatMessage">,
  event: ManagerNetworkEvent,
  index: number,
): ClusterIncident {
  const title = intl.formatMessage(
    { id: "clusterDashboard.incidents.networkEventTitle" },
    { kind: event.kind, node: event.target_node },
  )
  return {
    key: `network-event-${event.at}-${index}`,
    severity: event.severity === "error" || event.severity === "critical" ? "critical" : "warning",
    title,
    detail: event.message || event.service,
    href: "/cluster/diagnostics?tab=network",
    ariaLabel: title,
  }
}

export function buildInternalLinkMetrics(network?: ManagerNetworkSummaryResponse | null): ClusterLinkMetrics {
  if (!network) {
    return {
      internalMessagesPerSecond: metric("internalMessages", "clusterDashboard.metric.internalMessages", 0, "0.00/s", "", "default", "sample"),
      rpcCallsPerSecond: metric("rpcCallsPerSecond", "clusterDashboard.metric.rpcCalls", 0, "0.00/s", "", "default", "sample"),
      rpcErrorRate: metric("rpcErrorRate", "clusterDashboard.metric.rpcErrorRate", 0, "0.00%", "", "default", "sample"),
      rpcLatencyP95: metric("rpcLatency", "clusterDashboard.metric.rpcLatency", 0, "0 ms", "", "default", "sample"),
      rpcInflight: metric("rpcInflight", "clusterDashboard.metric.rpcInflight", 0, "0", "", "default", "sample"),
      queueFull: metric("queueFull", "clusterDashboard.metric.queueFull", 0, "0", "", "default", "sample"),
      timeouts: metric("timeouts", "clusterDashboard.metric.timeouts", 0, "0", "", "default", "sample"),
      trafficSeries: [],
      rpcSeries: [],
    }
  }

  const services = network.services ?? []
  const calls = sum(services.map((service) => service.calls_1m))
  const expectedTimeouts = sum(services.map((service) => service.expected_timeout_1m))
  const failures = sum(services.map((service) =>
    service.timeout_1m + service.queue_full_1m + service.remote_error_1m + service.other_error_1m,
  ))
  // Expected long-poll expiries are successful waits, not operator-actionable RPC failures.
  const completedCalls = Math.max(calls - expectedTimeouts, 0)
  const rpcErrorRate = completedCalls > 0 ? (failures / completedCalls) * 100 : 0
  const rpcCallsPerSecond = calls / 60
  const history = network.history
  const rpcHistory = history?.rpc ?? []
  const trafficHistory = history?.traffic ?? []
  const latestRpcPoint = rpcHistory.at(-1)
  const stepSeconds = Math.max(history?.step_seconds || 60, 1)
  const internalMessagesPerSecond = latestRpcPoint ? latestRpcPoint.calls / stepSeconds : rpcCallsPerSecond
  const weightedLatency = calls > 0
    ? sum(services.map((service) => service.p95_ms * Math.max(service.calls_1m, 1))) / sum(services.map((service) => Math.max(service.calls_1m, 1)))
    : 0
  const queueFull = sum(services.map((service) => service.queue_full_1m)) + network.headline.queue_full_1m
  const timeouts = sum(services.map((service) => service.timeout_1m)) + network.headline.timeouts_1m

  return {
    internalMessagesPerSecond: metric("internalMessages", "clusterDashboard.metric.internalMessages", internalMessagesPerSecond, `${internalMessagesPerSecond.toFixed(2)}/s`, "RPC history derived", "default", "derived"),
    rpcCallsPerSecond: metric("rpcCallsPerSecond", "clusterDashboard.metric.rpcCalls", rpcCallsPerSecond, `${rpcCallsPerSecond.toFixed(2)}/s`, `${calls} calls/min`, "default", "derived"),
    rpcErrorRate: metric("rpcErrorRate", "clusterDashboard.metric.rpcErrorRate", rpcErrorRate, `${rpcErrorRate.toFixed(2)}%`, `${failures} failures/min`, rpcErrorRate >= 5 ? "danger" : rpcErrorRate >= 1 ? "warning" : "default", "derived"),
    rpcLatencyP95: metric("rpcLatency", "clusterDashboard.metric.rpcLatency", weightedLatency, `${Math.round(weightedLatency)} ms`, "weighted p95", weightedLatency >= 1000 ? "danger" : weightedLatency >= 250 ? "warning" : "default", "derived"),
    rpcInflight: metric("rpcInflight", "clusterDashboard.metric.rpcInflight", network.headline.rpc_inflight, String(network.headline.rpc_inflight), "inflight", "default", "real"),
    queueFull: metric("queueFull", "clusterDashboard.metric.queueFull", queueFull, String(queueFull), "queue full/min", queueFull > 0 ? "warning" : "default", "real"),
    timeouts: metric("timeouts", "clusterDashboard.metric.timeouts", timeouts, String(timeouts), "timeouts/min", timeouts > 0 ? "warning" : "default", "real"),
    trafficSeries: trafficHistory.map((point) => ({ at: point.at, txBytes: point.tx_bytes, rxBytes: point.rx_bytes })),
    rpcSeries: rpcHistory.map((point) => ({
      at: point.at,
      calls: point.calls,
      errors: point.errors,
      expectedTimeouts: point.expected_timeouts,
    })),
  }
}

export function buildClusterMetricStrip(
  overview: ManagerOverviewResponse,
  channelCluster: ManagerChannelClusterSummaryResponse,
  linkMetrics: ClusterLinkMetrics,
): ClusterMetricValue[] {
  return [
    metric("nodes", "clusterDashboard.metric.nodes", overview.nodes.alive, `${overview.nodes.alive}/${overview.nodes.total}`, "alive/total", overview.nodes.dead > 0 ? "danger" : "default", "real", "/cluster/nodes"),
    metric("slots", "clusterDashboard.metric.slots", overview.slots.ready, `${overview.slots.ready}/${overview.slots.total}`, "ready/total", overview.slots.quorum_lost > 0 ? "danger" : overview.slots.leader_missing > 0 ? "warning" : "default", "real", "/cluster/slots"),
    metric("tasks", "clusterDashboard.metric.tasks", overview.tasks.failed + overview.tasks.retrying, `${overview.tasks.failed}/${overview.tasks.retrying}`, "failed/retrying", overview.tasks.failed > 0 ? "danger" : overview.tasks.retrying > 0 ? "warning" : "default", "real", "/cluster/tasks"),
    metric("channels", "clusterDashboard.metric.channels", channelCluster.isr_insufficient + channelCluster.no_leader, `${channelCluster.healthy}/${channelCluster.total}`, "healthy/total", channelCluster.no_leader > 0 ? "danger" : channelCluster.isr_insufficient > 0 ? "warning" : "default", "real", "/cluster/channels"),
    linkMetrics.internalMessagesPerSecond,
    linkMetrics.rpcErrorRate,
    linkMetrics.rpcLatencyP95,
  ]
}

export function buildTopologyRows(
  nodes: ManagerNodesResponse,
  network?: ManagerNetworkSummaryResponse | null,
): ClusterTopologyRow[] {
  return nodes.items.map((node) => {
    const raftHealth = node.controller.raft_health ?? "unknown"
    const peer = network?.peers.find((item) => item.node_id === node.node_id)
    const peerErrors = peer ? peer.errors.queue_full_1m + peer.errors.timeout_1m + peer.errors.remote_error_1m : 0
    const rpcErrorRate = peer && peer.rpc.calls_1m > 0 ? (peerErrors / peer.rpc.calls_1m) * 100 : peer ? 0 : null

    return {
      nodeId: node.node_id,
      name: node.name ?? String(node.node_id),
      status: node.status,
      isLocal: node.is_local,
      role: node.controller.role,
      slotsCount: node.slot_stats.count,
      leaderCount: node.slot_stats.leader_count,
      raftHealth,
      hasWatermark: raftHealth !== "unknown",
      firstIndex: node.controller.first_index ?? 0,
      appliedIndex: node.controller.applied_index ?? 0,
      snapshotIndex: node.controller.snapshot_index ?? 0,
      rpcErrorRate,
      rpcP95Ms: peer?.rpc.p95_ms ?? null,
      href: `/cluster/diagnostics?tab=controller-logs&node_id=${node.node_id}`,
    }
  })
}

function metric(
  key: string,
  labelId: string,
  value: number,
  formatted: string,
  detail: string,
  tone: ClusterMetricValue["tone"],
  source: MetricSource,
  href?: string,
): ClusterMetricValue {
  return { key, labelId, value, formatted, detail, tone, source, href }
}

function sum(values: number[]) {
  return values.reduce((total, value) => total + value, 0)
}
