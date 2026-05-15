import { describe, it, expect } from "vitest"
import {
  computeVerdict,
  buildIncidents,
  buildTopologyRows,
} from "./view-model"
import type {
  ManagerOverviewResponse,
  ManagerNodesResponse,
} from "@/lib/manager-api.types"

// Minimal intl mock: returns the key + JSON-serialised values so tests can
// assert both the right key and the right interpolation values.
const intl = {
  formatMessage: (
    { id }: { id: string },
    values?: Record<string, unknown>,
  ) => id + JSON.stringify(values || {}),
}

// ─── Fixtures ────────────────────────────────────────────────────────────────

function makeOverview(
  overrides: Partial<{
    tasksRetrying: number
    tasksFailed: number
    slotsQuorumLost: number
    slotsLeaderMissing: number
    quorumLostItems: ManagerOverviewResponse["anomalies"]["slots"]["quorum_lost"]["items"]
    leaderMissingItems: ManagerOverviewResponse["anomalies"]["slots"]["leader_missing"]["items"]
    syncMismatchItems: ManagerOverviewResponse["anomalies"]["slots"]["sync_mismatch"]["items"]
    tasksFailedItems: ManagerOverviewResponse["anomalies"]["tasks"]["failed"]["items"]
    tasksRetryingItems: ManagerOverviewResponse["anomalies"]["tasks"]["retrying"]["items"]
  }> = {},
): ManagerOverviewResponse {
  return {
    generated_at: "2024-01-01T00:00:00Z",
    cluster: { controller_leader_id: 1 },
    nodes: { total: 1, alive: 1, suspect: 0, dead: 0, draining: 0 },
    slots: {
      total: 10,
      ready: 10,
      quorum_lost: overrides.slotsQuorumLost ?? 0,
      leader_missing: overrides.slotsLeaderMissing ?? 0,
      unreported: 0,
      peer_mismatch: 0,
      epoch_lag: 0,
    },
    tasks: {
      total: 0,
      pending: 0,
      retrying: overrides.tasksRetrying ?? 0,
      failed: overrides.tasksFailed ?? 0,
    },
    anomalies: {
      slots: {
        quorum_lost: {
          count: overrides.quorumLostItems?.length ?? 0,
          items: overrides.quorumLostItems ?? [],
        },
        leader_missing: {
          count: overrides.leaderMissingItems?.length ?? 0,
          items: overrides.leaderMissingItems ?? [],
        },
        sync_mismatch: {
          count: overrides.syncMismatchItems?.length ?? 0,
          items: overrides.syncMismatchItems ?? [],
        },
      },
      tasks: {
        failed: {
          count: overrides.tasksFailedItems?.length ?? 0,
          items: overrides.tasksFailedItems ?? [],
        },
        retrying: {
          count: overrides.tasksRetryingItems?.length ?? 0,
          items: overrides.tasksRetryingItems ?? [],
        },
      },
    },
  }
}

function makeNodes(
  nodeOverrides: Array<{
    node_id?: number
    raft_health?: string
    is_local?: boolean
    status?: string
    role?: string
    first_index?: number
    applied_index?: number
    snapshot_index?: number
    name?: string
  }> = [],
): ManagerNodesResponse {
  const items = nodeOverrides.map((o) => ({
    node_id: o.node_id ?? 1,
    name: o.name,
    addr: "127.0.0.1:8080",
    status: o.status ?? "active",
    last_heartbeat_at: "2024-01-01T00:00:00Z",
    is_local: o.is_local ?? false,
    capacity_weight: 1,
    controller: {
      role: o.role ?? "voter",
      raft_health: o.raft_health,
      first_index: o.first_index,
      applied_index: o.applied_index,
      snapshot_index: o.snapshot_index,
    },
    slot_stats: { count: 5, leader_count: 2 },
  }))

  return {
    generated_at: "2024-01-01T00:00:00Z",
    controller_leader_id: 1,
    total: items.length,
    items,
  }
}

const emptyOverview = makeOverview()
const emptyNodes = makeNodes()

// ─── computeVerdict ───────────────────────────────────────────────────────────

describe("computeVerdict", () => {
  it("returns healthy when there are no anomalies", () => {
    expect(computeVerdict(emptyOverview, emptyNodes)).toBe("healthy")
  })

  it("returns degraded when slots.quorum_lost > 0", () => {
    expect(
      computeVerdict(makeOverview({ slotsQuorumLost: 1 }), emptyNodes),
    ).toBe("degraded")
  })

  it("returns degraded when slots.leader_missing > 0", () => {
    expect(
      computeVerdict(makeOverview({ slotsLeaderMissing: 1 }), emptyNodes),
    ).toBe("degraded")
  })

  it("returns degraded when tasks.retrying > 0", () => {
    expect(
      computeVerdict(makeOverview({ tasksRetrying: 1 }), emptyNodes),
    ).toBe("degraded")
  })

  it("returns degraded when a node has snapshot_required raft_health", () => {
    expect(
      computeVerdict(emptyOverview, makeNodes([{ raft_health: "snapshot_required" }])),
    ).toBe("degraded")
  })

  it("returns degraded when a node has snapshot_transferring raft_health", () => {
    expect(
      computeVerdict(emptyOverview, makeNodes([{ raft_health: "snapshot_transferring" }])),
    ).toBe("degraded")
  })

  it("returns degraded when a node has append_catchup raft_health", () => {
    expect(
      computeVerdict(emptyOverview, makeNodes([{ raft_health: "append_catchup" }])),
    ).toBe("degraded")
  })

  it("returns degraded when a node has compaction_degraded raft_health", () => {
    expect(
      computeVerdict(emptyOverview, makeNodes([{ raft_health: "compaction_degraded" }])),
    ).toBe("degraded")
  })

  it("returns critical when tasks.failed > 0", () => {
    expect(
      computeVerdict(makeOverview({ tasksFailed: 1 }), emptyNodes),
    ).toBe("critical")
  })

  it("returns critical when a node has restore_failed raft_health", () => {
    expect(
      computeVerdict(emptyOverview, makeNodes([{ raft_health: "restore_failed" }])),
    ).toBe("critical")
  })

  it("critical wins over degraded (tasks.failed + slots.quorum_lost)", () => {
    expect(
      computeVerdict(
        makeOverview({ tasksFailed: 1, slotsQuorumLost: 1 }),
        emptyNodes,
      ),
    ).toBe("critical")
  })

  it("critical wins over degraded (restore_failed node + retrying tasks)", () => {
    expect(
      computeVerdict(
        makeOverview({ tasksRetrying: 1 }),
        makeNodes([{ raft_health: "restore_failed" }]),
      ),
    ).toBe("critical")
  })

  it("ignores unknown raft_health for verdict", () => {
    expect(
      computeVerdict(emptyOverview, makeNodes([{ raft_health: "unknown" }])),
    ).toBe("healthy")
  })

  it("ignores healthy raft_health for verdict", () => {
    expect(
      computeVerdict(emptyOverview, makeNodes([{ raft_health: "healthy" }])),
    ).toBe("healthy")
  })
})

// ─── buildIncidents ───────────────────────────────────────────────────────────

describe("buildIncidents", () => {
  it("returns empty array when there are no anomalies", () => {
    expect(buildIncidents(intl, emptyOverview, emptyNodes)).toEqual([])
  })

  it("produces a critical incident for quorum_lost slot", () => {
    const overview = makeOverview({
      slotsQuorumLost: 1,
      quorumLostItems: [
        {
          slot_id: 42,
          quorum: "lost",
          sync: "ok",
          leader_id: 0,
          desired_peers: [1, 2, 3],
          current_peers: [1],
          last_report_at: "2024-01-01T00:00:00Z",
        },
      ],
    })
    const incidents = buildIncidents(intl, overview, emptyNodes)
    expect(incidents).toHaveLength(1)
    const inc = incidents[0]
    expect(inc.severity).toBe("critical")
    expect(inc.href).toBe("/cluster/slots?focus=42")
    expect(inc.key).toBe("slot-quorum-lost-42")
    expect(inc.title).toContain("dashboard.incidents.slotQuorumLostTitle")
    expect(inc.title).toContain('"id":42')
    expect(inc.detail).toContain("dashboard.incidents.slotPeers")
    expect(inc.detail).toContain("1, 2, 3")
    expect(inc.detail).toContain('"current":"1"')
  })

  it("produces a critical incident for leader_missing slot", () => {
    const overview = makeOverview({
      slotsLeaderMissing: 1,
      leaderMissingItems: [
        {
          slot_id: 7,
          quorum: "ok",
          sync: "ok",
          leader_id: 0,
          desired_peers: [1, 2],
          current_peers: [1, 2],
          last_report_at: "2024-01-01T00:00:00Z",
        },
      ],
    })
    const incidents = buildIncidents(intl, overview, emptyNodes)
    expect(incidents).toHaveLength(1)
    expect(incidents[0].severity).toBe("critical")
    expect(incidents[0].href).toBe("/cluster/slots?focus=7")
    expect(incidents[0].key).toBe("slot-leader-missing-7")
  })

  it("produces a warning incident for sync_mismatch slot", () => {
    const overview = makeOverview({
      syncMismatchItems: [
        {
          slot_id: 3,
          quorum: "ok",
          sync: "mismatch",
          leader_id: 1,
          desired_peers: [1, 2],
          current_peers: [1, 2],
          last_report_at: "2024-01-01T00:00:00Z",
        },
      ],
    })
    const incidents = buildIncidents(intl, overview, emptyNodes)
    expect(incidents).toHaveLength(1)
    expect(incidents[0].severity).toBe("warning")
    expect(incidents[0].href).toBe("/cluster/slots?focus=3")
    expect(incidents[0].key).toBe("slot-sync-mismatch-3")
  })

  it("produces a critical incident for failed task", () => {
    const overview = makeOverview({
      tasksFailed: 1,
      tasksFailedItems: [
        {
          slot_id: 10,
          kind: "replica_add",
          step: "transfer",
          status: "failed",
          source_node: 1,
          target_node: 2,
          attempt: 3,
          next_run_at: null,
          last_error: "timeout",
        },
      ],
    })
    const incidents = buildIncidents(intl, overview, emptyNodes)
    expect(incidents).toHaveLength(1)
    const inc = incidents[0]
    expect(inc.severity).toBe("critical")
    expect(inc.href).toBe("/cluster/tasks?focus=slot-10")
    expect(inc.key).toBe("task-failed-10-replica_add")
    expect(inc.title).toContain("dashboard.incidents.taskFailedTitle")
    expect(inc.detail).toContain("dashboard.incidents.taskDetail")
    expect(inc.detail).toContain('"error":"timeout"')
  })

  it("produces a warning incident for retrying task", () => {
    const overview = makeOverview({
      tasksRetrying: 1,
      tasksRetryingItems: [
        {
          slot_id: 5,
          kind: "replica_remove",
          step: "init",
          status: "retrying",
          source_node: 1,
          target_node: 2,
          attempt: 1,
          next_run_at: null,
          last_error: "",
        },
      ],
    })
    const incidents = buildIncidents(intl, overview, emptyNodes)
    expect(incidents).toHaveLength(1)
    expect(incidents[0].severity).toBe("warning")
    expect(incidents[0].href).toBe("/cluster/tasks?focus=slot-5")
    expect(incidents[0].key).toBe("task-retrying-5-replica_remove")
  })

  it("produces a critical incident for restore_failed node", () => {
    const nodes = makeNodes([
      { node_id: 2, raft_health: "restore_failed", first_index: 1, applied_index: 100, snapshot_index: 50 },
    ])
    const incidents = buildIncidents(intl, emptyOverview, nodes)
    expect(incidents).toHaveLength(1)
    const inc = incidents[0]
    expect(inc.severity).toBe("critical")
    expect(inc.href).toBe("/controller?node_id=2")
    expect(inc.key).toBe("controller-raft-2")
    expect(inc.title).toContain("dashboard.incidents.controllerRaftTitle")
    expect(inc.title).toContain('"health":"restore failed"')
    expect(inc.detail).toContain("dashboard.incidents.controllerRaftDetail")
    expect(inc.detail).toContain('"first":1')
    expect(inc.detail).toContain('"applied":100')
    expect(inc.detail).toContain('"snapshot":50')
  })

  it("produces a warning incident for snapshot_required node", () => {
    const nodes = makeNodes([{ node_id: 3, raft_health: "snapshot_required" }])
    const incidents = buildIncidents(intl, emptyOverview, nodes)
    expect(incidents).toHaveLength(1)
    expect(incidents[0].severity).toBe("warning")
    expect(incidents[0].href).toBe("/controller?node_id=3")
  })

  it("replaces underscores with spaces in raft health label", () => {
    const nodes = makeNodes([{ node_id: 4, raft_health: "append_catchup" }])
    const incidents = buildIncidents(intl, emptyOverview, nodes)
    expect(incidents[0].title).toContain('"health":"append catchup"')
  })

  it("skips nodes with healthy raft_health", () => {
    const nodes = makeNodes([{ node_id: 1, raft_health: "healthy" }])
    expect(buildIncidents(intl, emptyOverview, nodes)).toHaveLength(0)
  })

  it("skips nodes with unknown raft_health", () => {
    const nodes = makeNodes([{ node_id: 1, raft_health: "unknown" }])
    expect(buildIncidents(intl, emptyOverview, nodes)).toHaveLength(0)
  })

  it("skips nodes with no raft_health", () => {
    const nodes = makeNodes([{ node_id: 1 }])
    expect(buildIncidents(intl, emptyOverview, nodes)).toHaveLength(0)
  })

  it("respects priority ordering: quorum_lost before leader_missing before sync_mismatch before tasks", () => {
    const overview = makeOverview({
      slotsQuorumLost: 1,
      quorumLostItems: [
        { slot_id: 1, quorum: "lost", sync: "ok", leader_id: 0, desired_peers: [], current_peers: [], last_report_at: "" },
      ],
      leaderMissingItems: [
        { slot_id: 2, quorum: "ok", sync: "ok", leader_id: 0, desired_peers: [], current_peers: [], last_report_at: "" },
      ],
      syncMismatchItems: [
        { slot_id: 3, quorum: "ok", sync: "mismatch", leader_id: 1, desired_peers: [], current_peers: [], last_report_at: "" },
      ],
      tasksFailed: 1,
      tasksFailedItems: [
        { slot_id: 4, kind: "k", step: "s", status: "failed", source_node: 1, target_node: 2, attempt: 1, next_run_at: null, last_error: "" },
      ],
      tasksRetrying: 1,
      tasksRetryingItems: [
        { slot_id: 5, kind: "k", step: "s", status: "retrying", source_node: 1, target_node: 2, attempt: 1, next_run_at: null, last_error: "" },
      ],
    })
    const nodes = makeNodes([{ node_id: 10, raft_health: "restore_failed" }])
    const incidents = buildIncidents(intl, overview, nodes)

    expect(incidents[0].key).toBe("slot-quorum-lost-1")
    expect(incidents[1].key).toBe("slot-leader-missing-2")
    expect(incidents[2].key).toBe("slot-sync-mismatch-3")
    expect(incidents[3].key).toBe("task-failed-4-k")
    expect(incidents[4].key).toBe("task-retrying-5-k")
    expect(incidents[5].key).toBe("controller-raft-10")
  })
})

// ─── buildTopologyRows ────────────────────────────────────────────────────────

describe("buildTopologyRows", () => {
  it("returns empty array for empty nodes", () => {
    expect(buildTopologyRows(emptyNodes)).toEqual([])
  })

  it("maps all fields correctly", () => {
    const nodes = makeNodes([
      {
        node_id: 5,
        name: "node-5",
        status: "active",
        is_local: true,
        role: "leader",
        raft_health: "healthy",
        first_index: 10,
        applied_index: 200,
        snapshot_index: 100,
      },
    ])
    const rows = buildTopologyRows(nodes)
    expect(rows).toHaveLength(1)
    const row = rows[0]
    expect(row.nodeId).toBe(5)
    expect(row.name).toBe("node-5")
    expect(row.status).toBe("active")
    expect(row.isLocal).toBe(true)
    expect(row.role).toBe("leader")
    expect(row.slotsCount).toBe(5)
    expect(row.leaderCount).toBe(2)
    expect(row.raftHealth).toBe("healthy")
    expect(row.firstIndex).toBe(10)
    expect(row.appliedIndex).toBe(200)
    expect(row.snapshotIndex).toBe(100)
    expect(row.href).toBe("/controller?node_id=5")
  })

  it("hasWatermark is true when raft_health is present and not unknown", () => {
    const nodes = makeNodes([{ node_id: 1, raft_health: "healthy" }])
    expect(buildTopologyRows(nodes)[0].hasWatermark).toBe(true)
  })

  it("hasWatermark is false when raft_health is unknown", () => {
    const nodes = makeNodes([{ node_id: 1, raft_health: "unknown" }])
    expect(buildTopologyRows(nodes)[0].hasWatermark).toBe(false)
  })

  it("hasWatermark is false when raft_health is absent", () => {
    const nodes = makeNodes([{ node_id: 1 }])
    expect(buildTopologyRows(nodes)[0].hasWatermark).toBe(false)
  })

  it("uses node_id as name fallback when name is absent", () => {
    const nodes = makeNodes([{ node_id: 99 }])
    expect(buildTopologyRows(nodes)[0].name).toBe("99")
  })

  it("defaults index fields to 0 when absent", () => {
    const nodes = makeNodes([{ node_id: 1 }])
    const row = buildTopologyRows(nodes)[0]
    expect(row.firstIndex).toBe(0)
    expect(row.appliedIndex).toBe(0)
    expect(row.snapshotIndex).toBe(0)
  })

  it("maps multiple nodes in order", () => {
    const nodes = makeNodes([
      { node_id: 1, raft_health: "healthy" },
      { node_id: 2, raft_health: "restore_failed" },
      { node_id: 3 },
    ])
    const rows = buildTopologyRows(nodes)
    expect(rows.map((r) => r.nodeId)).toEqual([1, 2, 3])
  })
})
