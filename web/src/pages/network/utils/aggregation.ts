import type {
  ManagerNetworkEvent,
  ManagerNetworkHistory,
  ManagerNetworkPeer,
  ManagerNetworkPoolStats,
  ManagerNetworkRPCService,
  ManagerNetworkSummaryResponse,
  ManagerNetworkTrafficMessageType,
} from "@/lib/manager-api.types"

export type NetworkTimeRange = "1m" | "5m" | "15m"
export type NetworkAlertLevel = "none" | "warning" | "danger"

export type NetworkNodeOption = {
  nodeId: number
  label: string
  health: string
  isLocal: boolean
}

export type FilteredNetworkMetrics = {
  isAllNodes: boolean
  selectedNodeIds: number[]
  selectedPeers: ManagerNetworkPeer[]
  nodeOptions: NetworkNodeOption[]
  services: ManagerNetworkRPCService[]
  history: ManagerNetworkHistory
  health: { alive: number; suspect: number; dead: number; draining: number; total: number }
  pools: {
    cluster: ManagerNetworkPoolStats
    dataPlane: ManagerNetworkPoolStats
    active: number
    idle: number
    utilization: number
    alertLevel: NetworkAlertLevel
  }
  latency: { p50Ms: number; p95Ms: number; p99Ms: number; alertLevel: NetworkAlertLevel }
  rpc: { calls: number; success: number; failures: number; expectedTimeouts: number; successRate: number | null; alertLevel: NetworkAlertLevel }
  traffic: {
    txBytes: number
    rxBytes: number
    txBps: number
    rxBps: number
    messageTypes: ManagerNetworkTrafficMessageType[]
    isFilteredByNode: boolean
    scopeNote: string | null
  }
  errors: { dial: number; queueFull: number; timeouts: number; remote: number; total: number; alertLevel: NetworkAlertLevel }
  events: ManagerNetworkEvent[]
}

const emptyHistory: ManagerNetworkHistory = { window_seconds: 60, step_seconds: 60, traffic: [], rpc: [], errors: [] }

export function aggregateNetworkMetrics(summary: ManagerNetworkSummaryResponse, selectedNodeIds: number[]): FilteredNetworkMetrics {
  const selected = normalizeSelectedNodeIds(selectedNodeIds)
  const isAllNodes = selected.length === 0
  const selectedPeers = isAllNodes ? summary.peers : summary.peers.filter((peer) => selected.includes(peer.node_id))
  const services = isAllNodes ? summary.services : summary.services.filter((service) => selected.includes(service.target_node))

  const health = isAllNodes ? headlineHealth(summary) : selectedPeerHealth(selectedPeers)
  const pools = aggregatePools(summary, selectedPeers, isAllNodes)
  const rpc = aggregateRpc(services)
  const latency = aggregateLatency(services, selectedPeers)
  const errors = aggregateErrors(summary, selectedPeers, isAllNodes)
  const traffic = aggregateTraffic(summary, isAllNodes)
  const events = filterEventsByNodes(summary.events, selected)

  return {
    isAllNodes,
    selectedNodeIds: selected,
    selectedPeers,
    nodeOptions: nodeOptionsFromSummary(summary),
    services,
    history: summary.history ?? emptyHistory,
    health,
    pools,
    latency,
    rpc,
    traffic,
    errors,
    events,
  }
}

export function normalizeSelectedNodeIds(nodeIds: number[]) {
  return Array.from(new Set(nodeIds.filter((nodeId) => Number.isInteger(nodeId) && nodeId > 0))).sort((a, b) => a - b)
}

export function nodeOptionsFromSummary(summary: ManagerNetworkSummaryResponse): NetworkNodeOption[] {
  const options: NetworkNodeOption[] = [{
    nodeId: summary.scope.local_node_id,
    label: `node-${summary.scope.local_node_id} (local)`,
    health: "alive",
    isLocal: true,
  }]

  for (const peer of summary.peers) {
    options.push({
      nodeId: peer.node_id,
      label: peer.name || `node-${peer.node_id}`,
      health: peer.health,
      isLocal: false,
    })
  }

  return options
}

export function filterEventsByNodes(events: ManagerNetworkEvent[], selectedNodeIds: number[]) {
  if (selectedNodeIds.length === 0) return events
  return events.filter((event) => selectedNodeIds.includes(event.target_node))
}

function headlineHealth(summary: ManagerNetworkSummaryResponse) {
  const health = {
    alive: summary.headline.alive_nodes,
    suspect: summary.headline.suspect_nodes,
    dead: summary.headline.dead_nodes,
    draining: summary.headline.draining_nodes,
  }
  return { ...health, total: health.alive + health.suspect + health.dead + health.draining }
}

function selectedPeerHealth(peers: ManagerNetworkPeer[]) {
  const health = { alive: 0, suspect: 0, dead: 0, draining: 0 }
  for (const peer of peers) {
    if (peer.health === "alive") health.alive += 1
    else if (peer.health === "suspect") health.suspect += 1
    else if (peer.health === "dead") health.dead += 1
    else if (peer.health === "draining") health.draining += 1
  }
  return { ...health, total: health.alive + health.suspect + health.dead + health.draining }
}

function aggregatePools(summary: ManagerNetworkSummaryResponse, peers: ManagerNetworkPeer[], isAllNodes: boolean): FilteredNetworkMetrics["pools"] {
  const cluster = isAllNodes
    ? { active: summary.headline.pool_active, idle: summary.headline.pool_idle }
    : peers.reduce((total, peer) => addPool(total, peer.pools.cluster), { active: 0, idle: 0 })
  const dataPlane = isAllNodes
    ? { ...summary.channel_replication.pool }
    : peers.reduce((total, peer) => addPool(total, peer.pools.data_plane), { active: 0, idle: 0 })
  const active = cluster.active + dataPlane.active
  const idle = cluster.idle + dataPlane.idle
  const total = active + idle
  const utilization = total > 0 ? active / total : 0

  return {
    cluster,
    dataPlane,
    active,
    idle,
    utilization,
    alertLevel: utilization > 0.8 ? "warning" : "none",
  }
}

function addPool(total: ManagerNetworkPoolStats, pool: ManagerNetworkPoolStats) {
  return { active: total.active + pool.active, idle: total.idle + pool.idle }
}

function aggregateRpc(services: ManagerNetworkRPCService[]): FilteredNetworkMetrics["rpc"] {
  const totals = services.reduce((total, service) => {
    total.calls += service.calls_1m
    total.success += service.success_1m
    total.expectedTimeouts += service.expected_timeout_1m
    total.failures += service.timeout_1m + service.queue_full_1m + service.remote_error_1m + service.other_error_1m
    return total
  }, { calls: 0, success: 0, failures: 0, expectedTimeouts: 0 })
  const completedCalls = Math.max(totals.calls - totals.expectedTimeouts, 0)
  const successRate = completedCalls > 0 ? Math.min(totals.success, completedCalls) / completedCalls : null

  return {
    ...totals,
    successRate,
    alertLevel: successRate !== null && successRate < 0.95 ? "danger" : "none",
  }
}

function aggregateLatency(services: ManagerNetworkRPCService[], peers: ManagerNetworkPeer[]): FilteredNetworkMetrics["latency"] {
  const p50Ms = weightedLatency(services, "p50_ms")
  const serviceP95 = weightedLatency(services, "p95_ms")
  const p99Ms = weightedLatency(services, "p99_ms")
  const peerP95 = average(peers.map((peer) => peer.rpc.p95_ms).filter((value) => value > 0))
  const p95Ms = serviceP95 > 0 ? serviceP95 : peerP95

  return {
    p50Ms,
    p95Ms,
    p99Ms,
    alertLevel: p95Ms > 100 ? "danger" : "none",
  }
}

function weightedLatency(services: ManagerNetworkRPCService[], key: "p50_ms" | "p95_ms" | "p99_ms") {
  let weighted = 0
  let weight = 0
  for (const service of services) {
    const value = service[key]
    if (value <= 0) continue
    const calls = Math.max(service.calls_1m, 1)
    weighted += value * calls
    weight += calls
  }
  return weight > 0 ? Math.round(weighted / weight) : 0
}

function average(values: number[]) {
  if (values.length === 0) return 0
  return Math.round(values.reduce((total, value) => total + value, 0) / values.length)
}

function aggregateErrors(summary: ManagerNetworkSummaryResponse, peers: ManagerNetworkPeer[], isAllNodes: boolean): FilteredNetworkMetrics["errors"] {
  const base = isAllNodes
    ? {
      dial: summary.headline.dial_errors_1m,
      queueFull: summary.headline.queue_full_1m,
      timeouts: summary.headline.timeouts_1m,
      remote: 0,
    }
    : peers.reduce((total, peer) => ({
      dial: total.dial + peer.errors.dial_error_1m,
      queueFull: total.queueFull + peer.errors.queue_full_1m,
      timeouts: total.timeouts + peer.errors.timeout_1m,
      remote: total.remote + peer.errors.remote_error_1m,
    }), { dial: 0, queueFull: 0, timeouts: 0, remote: 0 })
  const total = base.dial + base.queueFull + base.timeouts + base.remote

  return { ...base, total, alertLevel: total > 10 ? "danger" : total > 0 ? "warning" : "none" }
}

function aggregateTraffic(summary: ManagerNetworkSummaryResponse, isAllNodes: boolean): FilteredNetworkMetrics["traffic"] {
  return {
    txBytes: summary.traffic.tx_bytes_1m,
    rxBytes: summary.traffic.rx_bytes_1m,
    txBps: summary.traffic.tx_bps,
    rxBps: summary.traffic.rx_bps,
    messageTypes: summary.traffic.by_message_type,
    isFilteredByNode: isAllNodes,
    scopeNote: isAllNodes ? null : "Current API exposes local total traffic only; selected node traffic is not inferred.",
  }
}
