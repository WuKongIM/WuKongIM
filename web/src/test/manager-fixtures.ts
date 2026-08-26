import type { ManagerNodesResponse, ManagerOverviewResponse } from "@/lib/manager-api.types"

export function healthyManagerNodes(total = 3): ManagerNodesResponse {
  return {
    generated_at: "2026-08-26T03:30:00Z",
    controller_leader_id: 1,
    total,
    items: Array.from({ length: total }, (_, index) => {
      const nodeId = index + 1
      return {
        node_id: nodeId,
        name: `node-${nodeId}`,
        addr: `127.0.0.1:70${nodeId}`,
        status: "alive",
        last_heartbeat_at: "2026-08-26T03:30:00Z",
        is_local: nodeId === 1,
        capacity_weight: 1,
        membership: { role: "data", join_state: "active", schedulable: true },
        controller: { role: nodeId === 1 ? "leader" : "follower", voter: true, leader_id: 1 },
        slot_stats: { count: 10, leader_count: nodeId === 1 ? 4 : 3 },
        channel_runtime: { active_total: 0, active_leader: 0, active_follower: 0, unknown: false },
      }
    }),
  }
}

export function healthyManagerOverview(total = 3): ManagerOverviewResponse {
  return {
    generated_at: "2026-08-26T03:30:00Z",
    cluster: { controller_leader_id: 1 },
    nodes: { total, alive: total, suspect: 0, dead: 0, draining: 0 },
    slots: {
      total: 10,
      ready: 10,
      quorum_lost: 0,
      leader_missing: 0,
      unreported: 0,
      peer_mismatch: 0,
      epoch_lag: 0,
    },
    tasks: { total: 0, pending: 0, running: 0, failed: 0 },
    anomalies: {
      slots: {
        quorum_lost: { count: 0, items: [] },
        leader_missing: { count: 0, items: [] },
        sync_mismatch: { count: 0, items: [] },
      },
      tasks: { failed: { count: 0, items: [] } },
    },
  }
}
