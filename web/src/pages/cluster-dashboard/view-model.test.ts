import type { IntlShape } from "react-intl"
import { describe, expect, it } from "vitest"

import type {
  ManagerChannelClusterSummaryResponse,
  ManagerNetworkRPCService,
  ManagerNetworkSummaryResponse,
  ManagerNodesResponse,
  ManagerOverviewResponse,
} from "@/lib/manager-api.types"
import {
  buildClusterIncidents,
  buildClusterMetricStrip,
  buildInternalLinkMetrics,
  buildTopologyRows,
  computeClusterVerdict,
} from "./view-model"

const intl = {
  formatMessage: ({ id }: { id: string }, values?: Record<string, unknown>) => `${id}${JSON.stringify(values ?? {})}`,
} as Pick<IntlShape, "formatMessage">

function makeOverview(
  overrides: Partial<{
    tasksRetrying: number
    tasksFailed: number
    slotsQuorumLost: number
    retryingItems: ManagerOverviewResponse["anomalies"]["tasks"]["retrying"]["items"]
  }> = {},
): ManagerOverviewResponse {
  return {
    generated_at: "2026-05-15T08:00:00Z",
    cluster: { controller_leader_id: 1 },
    nodes: { total: 3, alive: 3, suspect: 0, dead: 0, draining: 0 },
    slots: {
      total: 16,
      ready: 15,
      quorum_lost: overrides.slotsQuorumLost ?? 0,
      leader_missing: 0,
      unreported: 0,
      peer_mismatch: 0,
      epoch_lag: 0,
    },
    tasks: {
      total: 2,
      pending: 0,
      retrying: overrides.tasksRetrying ?? 0,
      failed: overrides.tasksFailed ?? 0,
    },
    anomalies: {
      slots: {
        quorum_lost: { count: 0, items: [] },
        leader_missing: { count: 0, items: [] },
        sync_mismatch: { count: 0, items: [] },
      },
      tasks: {
        failed: { count: 0, items: [] },
        retrying: { count: overrides.retryingItems?.length ?? 0, items: overrides.retryingItems ?? [] },
      },
    },
  }
}

function makeOverviewWithOneRetry() {
  return makeOverview({
    tasksRetrying: 1,
    retryingItems: [
      {
        slot_id: 9,
        kind: "rebalance",
        step: "move",
        status: "retrying",
        source_node: 1,
        target_node: 2,
        attempt: 2,
        next_run_at: null,
        last_error: "",
      },
    ],
  })
}

function makeNodes(
  overrides: Array<Partial<ManagerNodesResponse["items"][number] & { raft_health: string; applied_index: number }>> = [],
): ManagerNodesResponse {
  const source = overrides.length > 0 ? overrides : [{}]
  const items = source.map((override, index) => ({
    node_id: override.node_id ?? index + 1,
    name: override.name,
    addr: override.addr ?? `127.0.0.1:${7000 + index}`,
    status: override.status ?? "alive",
    last_heartbeat_at: override.last_heartbeat_at ?? "2026-05-15T08:00:00Z",
    is_local: override.is_local ?? index === 0,
    capacity_weight: override.capacity_weight ?? 1,
    controller: {
      role: override.controller?.role ?? "follower",
      raft_health: override.raft_health ?? override.controller?.raft_health ?? "healthy",
      first_index: override.controller?.first_index ?? 1,
      applied_index: override.applied_index ?? override.controller?.applied_index ?? 10,
      snapshot_index: override.controller?.snapshot_index ?? 0,
    },
    slot_stats: override.slot_stats ?? { count: 4, leader_count: 2 },
  }))
  return { generated_at: "2026-05-15T08:00:00Z", controller_leader_id: 1, total: items.length, items }
}

function makeRpcService(overrides: Partial<ManagerNetworkRPCService> = {}): ManagerNetworkRPCService {
  return {
    service_id: overrides.service_id ?? 1,
    service: overrides.service ?? "raft.append",
    group: overrides.group ?? "controller",
    target_node: overrides.target_node ?? 2,
    inflight: overrides.inflight ?? 3,
    calls_1m: overrides.calls_1m ?? 60,
    success_1m: overrides.success_1m ?? 58,
    expected_timeout_1m: overrides.expected_timeout_1m ?? 0,
    timeout_1m: overrides.timeout_1m ?? 1,
    queue_full_1m: overrides.queue_full_1m ?? 0,
    remote_error_1m: overrides.remote_error_1m ?? 1,
    other_error_1m: overrides.other_error_1m ?? 0,
    p50_ms: overrides.p50_ms ?? 8,
    p95_ms: overrides.p95_ms ?? 42,
    p99_ms: overrides.p99_ms ?? 80,
    last_seen_at: overrides.last_seen_at ?? "2026-05-15T08:00:00Z",
  }
}

function makeNetworkSummary(
  overrides: Partial<{
    services: ManagerNetworkRPCService[]
    historyRpc: ManagerNetworkSummaryResponse["history"]["rpc"]
  }> = {},
): ManagerNetworkSummaryResponse {
  return {
    generated_at: "2026-05-15T08:00:00Z",
    scope: { view: "local_node", local_node_id: 1, controller_leader_id: 1 },
    source_status: { local_collector: "ok", controller_context: "ok", runtime_views: "ok", errors: {} },
    headline: {
      remote_peers: 2,
      alive_nodes: 3,
      suspect_nodes: 0,
      dead_nodes: 0,
      draining_nodes: 0,
      pool_active: 2,
      pool_idle: 4,
      rpc_inflight: 3,
      dial_errors_1m: 0,
      queue_full_1m: 0,
      timeouts_1m: 1,
      stale_observations: 0,
    },
    traffic: {
      scope: "local_total_by_msg_type",
      tx_bytes_1m: 1024,
      rx_bytes_1m: 2048,
      tx_bps: 128,
      rx_bps: 256,
      peer_breakdown_available: true,
      by_message_type: [],
    },
    peers: [
      {
        node_id: 2,
        name: "node-2",
        addr: "127.0.0.1:7002",
        health: "alive",
        last_heartbeat_at: "2026-05-15T08:00:00Z",
        pools: { cluster: { active: 1, idle: 1 }, data_plane: { active: 1, idle: 2 } },
        rpc: { inflight: 2, calls_1m: 100, p95_ms: 37, success_rate: 0.98 },
        errors: { dial_error_1m: 0, queue_full_1m: 1, timeout_1m: 1, remote_error_1m: 0 },
      },
    ],
    services: overrides.services ?? [makeRpcService()],
    channel_replication: {
      pool: { active: 1, idle: 1 },
      services: [],
      long_poll: { lane_count: 2, max_wait_ms: 30000, max_bytes: 1048576, max_channels: 256 },
      long_poll_timeouts_1m: 4,
      data_plane_rpc_timeout_ms: 5000,
    },
    discovery: {
      listen_addr: "127.0.0.1:7000",
      advertise_addr: "127.0.0.1:7000",
      seeds: [],
      static_nodes: [],
      pool_size: 2,
      data_plane_pool_size: 2,
      dial_timeout_ms: 1000,
      controller_observation_interval_ms: 1000,
    },
    history: {
      window_seconds: 300,
      step_seconds: 60,
      traffic: [{ at: "2026-05-15T08:00:00Z", tx_bytes: 1024, rx_bytes: 2048 }],
      rpc: overrides.historyRpc ?? [{ at: "2026-05-15T08:00:00Z", calls: 60, success: 58, errors: 2, expected_timeouts: 0 }],
      errors: [],
    },
    events: [{ at: "2026-05-15T08:00:10Z", severity: "warning", kind: "rpc_timeout", target_node: 2, service: "raft.append", message: "timeout spike" }],
  }
}

function makeChannelCluster(): ManagerChannelClusterSummaryResponse {
  return {
    total: 100,
    healthy: 98,
    isr_insufficient: 1,
    no_leader: 1,
    avg_replicas: 3,
    avg_isr: 2.9,
    leader_distribution: [{ node_id: 1, count: 40 }],
  }
}

describe("computeClusterVerdict", () => {
  it("returns healthy with no anomalies", () => {
    expect(computeClusterVerdict(makeOverview(), makeNodes())).toBe("healthy")
  })

  it("returns degraded for slot quorum loss or retrying tasks", () => {
    expect(computeClusterVerdict(makeOverview({ slotsQuorumLost: 1 }), makeNodes())).toBe("degraded")
    expect(computeClusterVerdict(makeOverview({ tasksRetrying: 1 }), makeNodes())).toBe("degraded")
  })

  it("returns critical for failed tasks or restore_failed raft health", () => {
    expect(computeClusterVerdict(makeOverview({ tasksFailed: 1 }), makeNodes())).toBe("critical")
    expect(computeClusterVerdict(makeOverview(), makeNodes([{ raft_health: "restore_failed" }]))).toBe("critical")
  })
})

describe("buildClusterIncidents", () => {
  it("includes slot, task, raft, and network event incidents", () => {
    const incidents = buildClusterIncidents(
      intl,
      makeOverviewWithOneRetry(),
      makeNodes([{ raft_health: "snapshot_required" }]),
      makeNetworkSummary(),
    )

    expect(incidents.map((item) => item.key)).toContain("task-retrying-9-rebalance")
    expect(incidents.some((item) => item.key.startsWith("controller-raft-"))).toBe(true)
    expect(incidents.some((item) => item.key.startsWith("network-event-"))).toBe(true)
  })
})

describe("buildInternalLinkMetrics", () => {
  it("derives rpc error rate without counting expected long-poll expiries", () => {
    const metrics = buildInternalLinkMetrics(makeNetworkSummary({
      services: [makeRpcService({ calls_1m: 100, success_1m: 90, expected_timeout_1m: 8, timeout_1m: 2, queue_full_1m: 0, remote_error_1m: 0, other_error_1m: 0 })],
      historyRpc: [{ at: "2026-05-15T08:00:00Z", calls: 100, success: 90, errors: 2, expected_timeouts: 8 }],
    }))

    expect(metrics.rpcErrorRate.value).toBeCloseTo(2.17, 1)
    expect(metrics.rpcErrorRate.source).toBe("derived")
    expect(metrics.rpcCallsPerSecond.value).toBeCloseTo(100 / 60, 2)
  })

  it("handles zero rpc calls", () => {
    const metrics = buildInternalLinkMetrics(makeNetworkSummary({ services: [] }))
    expect(metrics.rpcErrorRate.value).toBe(0)
    expect(metrics.rpcErrorRate.formatted).toBe("0.00%")
  })

  it("derives internal message rate from rpc history when no direct metric exists", () => {
    const metrics = buildInternalLinkMetrics(makeNetworkSummary({
      historyRpc: [
        { at: "2026-05-15T08:00:00Z", calls: 30, success: 30, errors: 0, expected_timeouts: 0 },
        { at: "2026-05-15T08:01:00Z", calls: 90, success: 90, errors: 0, expected_timeouts: 0 },
      ],
    }))

    expect(metrics.internalMessagesPerSecond.value).toBeCloseTo(1.5, 2)
    expect(metrics.internalMessagesPerSecond.source).toBe("derived")
  })

  it("handles partial network summaries without history", () => {
    const partial = makeNetworkSummary()
    delete (partial as Partial<ManagerNetworkSummaryResponse>).history

    const metrics = buildInternalLinkMetrics(partial)

    expect(metrics.rpcSeries).toEqual([])
    expect(metrics.internalMessagesPerSecond.value).toBeCloseTo(1, 2)
  })
})

describe("buildClusterMetricStrip", () => {
  it("returns node, slot, task, channel, internal message, rpc error, and rpc latency cards", () => {
    const strip = buildClusterMetricStrip(makeOverview(), makeChannelCluster(), buildInternalLinkMetrics(makeNetworkSummary()))
    expect(strip.map((item) => item.key)).toEqual([
      "nodes",
      "slots",
      "tasks",
      "channels",
      "internalMessages",
      "rpcErrorRate",
      "rpcLatency",
    ])
  })
})

describe("buildTopologyRows", () => {
  it("maps node watermarks and attaches per-node rpc summaries", () => {
    const rows = buildTopologyRows(makeNodes([{ node_id: 2, raft_health: "healthy", applied_index: 42 }]), makeNetworkSummary())
    expect(rows[0]).toMatchObject({ nodeId: 2, appliedIndex: 42, rpcErrorRate: expect.any(Number) })
  })
})
