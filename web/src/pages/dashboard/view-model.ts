import type { IntlShape } from "react-intl"
import type {
  ManagerOverviewResponse,
  ManagerNodesResponse,
  ManagerNode,
} from "@/lib/manager-api.types"

export type Verdict = "healthy" | "degraded" | "critical"

export type IncidentItem = {
  key: string
  severity: "critical" | "warning"
  title: string
  detail: string
  href: string
  ariaLabel: string
}

export type TopologyRowData = {
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
  href: string
}

const CRITICAL_RAFT_STATES = new Set(["restore_failed"])

const DEGRADED_RAFT_STATES = new Set([
  "snapshot_required",
  "snapshot_transferring",
  "append_catchup",
  "compaction_degraded",
])

export function computeVerdict(
  overview: ManagerOverviewResponse,
  nodes: ManagerNodesResponse,
): Verdict {
  // Check critical conditions first
  if (overview.tasks.failed > 0) return "critical"

  for (const node of nodes.items) {
    const raftHealth = node.controller.raft_health
    if (raftHealth && CRITICAL_RAFT_STATES.has(raftHealth)) return "critical"
  }

  // Check degraded conditions
  if (
    overview.slots.quorum_lost > 0 ||
    overview.slots.leader_missing > 0 ||
    overview.tasks.retrying > 0
  ) {
    return "degraded"
  }

  for (const node of nodes.items) {
    const raftHealth = node.controller.raft_health
    if (raftHealth && DEGRADED_RAFT_STATES.has(raftHealth)) return "degraded"
  }

  return "healthy"
}

function raftSeverity(raftHealth: string): "critical" | "warning" {
  return CRITICAL_RAFT_STATES.has(raftHealth) ? "critical" : "warning"
}

export function buildIncidents(
  intl: Pick<IntlShape, "formatMessage">,
  overview: ManagerOverviewResponse,
  nodes: ManagerNodesResponse,
): IncidentItem[] {
  const incidents: IncidentItem[] = []

  // 1. Slot quorum lost — severity critical
  for (const item of overview.anomalies.slots.quorum_lost.items) {
    const title = intl.formatMessage(
      { id: "dashboard.incidents.slotQuorumLostTitle" },
      { id: item.slot_id },
    )
    const detail = intl.formatMessage(
      { id: "dashboard.incidents.slotPeers" },
      {
        desired: item.desired_peers.join(", "),
        current: item.current_peers.join(", "),
      },
    )
    const href = `/cluster/slots?focus=${item.slot_id}`
    incidents.push({
      key: `slot-quorum-lost-${item.slot_id}`,
      severity: "critical",
      title,
      detail,
      href,
      ariaLabel: title,
    })
  }

  // 2. Slot leader missing — severity critical
  for (const item of overview.anomalies.slots.leader_missing.items) {
    const title = intl.formatMessage(
      { id: "dashboard.incidents.slotLeaderMissingTitle" },
      { id: item.slot_id },
    )
    const href = `/cluster/slots?focus=${item.slot_id}`
    incidents.push({
      key: `slot-leader-missing-${item.slot_id}`,
      severity: "critical",
      title,
      detail: "",
      href,
      ariaLabel: title,
    })
  }

  // 3. Slot sync mismatch — severity warning
  for (const item of overview.anomalies.slots.sync_mismatch.items) {
    const title = intl.formatMessage(
      { id: "dashboard.incidents.slotSyncMismatchTitle" },
      { id: item.slot_id },
    )
    const href = `/cluster/slots?focus=${item.slot_id}`
    incidents.push({
      key: `slot-sync-mismatch-${item.slot_id}`,
      severity: "warning",
      title,
      detail: "",
      href,
      ariaLabel: title,
    })
  }

  // 4. Tasks failed — severity critical
  for (const item of overview.anomalies.tasks.failed.items) {
    const title = intl.formatMessage(
      { id: "dashboard.incidents.taskFailedTitle" },
      { kind: item.kind, id: item.slot_id },
    )
    const detail = intl.formatMessage(
      { id: "dashboard.incidents.taskDetail" },
      {
        kind: item.kind,
        step: item.step,
        attempt: item.attempt,
        error: item.last_error,
      },
    )
    const href = `/cluster/tasks?focus=slot-${item.slot_id}`
    incidents.push({
      key: `task-failed-${item.slot_id}-${item.kind}`,
      severity: "critical",
      title,
      detail,
      href,
      ariaLabel: title,
    })
  }

  // 5. Tasks retrying — severity warning
  for (const item of overview.anomalies.tasks.retrying.items) {
    const title = intl.formatMessage(
      { id: "dashboard.incidents.taskRetryingTitle" },
      { kind: item.kind, id: item.slot_id },
    )
    const href = `/cluster/tasks?focus=slot-${item.slot_id}`
    incidents.push({
      key: `task-retrying-${item.slot_id}-${item.kind}`,
      severity: "warning",
      title,
      detail: "",
      href,
      ariaLabel: title,
    })
  }

  // 6. Nodes with degraded/critical raft health
  for (const node of nodes.items) {
    const raftHealth = node.controller.raft_health
    if (!raftHealth || raftHealth === "healthy" || raftHealth === "unknown") continue
    if (!CRITICAL_RAFT_STATES.has(raftHealth) && !DEGRADED_RAFT_STATES.has(raftHealth)) continue

    const healthLabel = raftHealth.replace(/_/g, " ")
    const title = intl.formatMessage(
      { id: "dashboard.incidents.controllerRaftTitle" },
      { health: healthLabel, id: node.node_id },
    )
    const detail = intl.formatMessage(
      { id: "dashboard.incidents.controllerRaftDetail" },
      {
        first: node.controller.first_index ?? 0,
        applied: node.controller.applied_index ?? 0,
        snapshot: node.controller.snapshot_index ?? 0,
      },
    )
    const href = `/controller?node_id=${node.node_id}`
    incidents.push({
      key: `controller-raft-${node.node_id}`,
      severity: raftSeverity(raftHealth),
      title,
      detail,
      href,
      ariaLabel: title,
    })
  }

  return incidents
}

export function buildTopologyRows(nodes: ManagerNodesResponse): TopologyRowData[] {
  return nodes.items.map((node: ManagerNode): TopologyRowData => {
    const raftHealth = node.controller.raft_health ?? "unknown"
    const hasWatermark = raftHealth !== "unknown"

    return {
      nodeId: node.node_id,
      name: node.name ?? String(node.node_id),
      status: node.status,
      isLocal: node.is_local,
      role: node.controller.role,
      slotsCount: node.slot_stats.count,
      leaderCount: node.slot_stats.leader_count,
      raftHealth,
      hasWatermark,
      firstIndex: node.controller.first_index ?? 0,
      appliedIndex: node.controller.applied_index ?? 0,
      snapshotIndex: node.controller.snapshot_index ?? 0,
      href: `/controller?node_id=${node.node_id}`,
    }
  })
}
