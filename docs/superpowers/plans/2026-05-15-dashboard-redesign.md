# Dashboard Health-First Cockpit Redesign — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the current dashboard page with a health-first cockpit layout (Health Hero → Realtime Pulse → Slot/Channel Health + Active Incidents → Topology Snapshot).

**Architecture:** The page becomes a thin orchestrator (`page.tsx`) that fetches data, derives view-models via pure functions (`view-model.ts`), and renders five regions via focused components. A `useDashboardPulse` hook provides deterministic mock sparkline data until a real metrics endpoint exists.

**Tech Stack:** React 19, TypeScript, Tailwind CSS 4, recharts (PieChart + LineChart), react-intl, vitest + testing-library.

---

## File Structure

```
web/src/pages/dashboard/
  page.tsx                          — orchestrator (fetch, derive, compose regions)
  page.test.tsx                     — integration tests (existing behaviour preserved + new)
  view-model.ts                     — pure functions: computeVerdict, buildIncidents, buildTopologyRows
  view-model.test.ts                — unit tests for view-model
  use-dashboard-pulse.ts            — hook returning deterministic mock sparkline series
  use-dashboard-pulse.test.ts       — unit test for deterministic output
  components/
    health-hero.tsx                  — R1 verdict bar
    pulse-tile.tsx                   — R2 single sparkline tile
    slot-channel-health.tsx          — R3 donut + counters
    incident-list.tsx               — R4 prioritised incident list
    topology-row.tsx                — R5 single node row
```

---

## Task 1: Add new i18n keys (en + zh-CN)

**Files:**
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`

- [ ] **Step 1: Add English dashboard keys**

Add after the existing `"dashboard.slotValue"` line in `en.ts`:

```typescript
  // Dashboard redesign — Health Hero
  "dashboard.scope": "Scope: single-node cluster",
  "dashboard.verdict.healthy": "Cluster healthy",
  "dashboard.verdict.degraded": "Cluster degraded",
  "dashboard.verdict.critical": "Cluster critical",
  "dashboard.verdict.summary": "{incidents} {incidents, plural, one {incident} other {incidents}} · controller leader {id} · {ready}/{total} slots ready · {alive}/{totalNodes} nodes alive",
  "dashboard.verdict.summaryHealthy": "All systems nominal. Generated {time}.",
  // Dashboard redesign — Pulse
  "dashboard.pulse.messagesPerSec": "Messages/s",
  "dashboard.pulse.connections": "Connections",
  "dashboard.pulse.txKbPerSec": "TX KB/s",
  "dashboard.pulse.rpcErrorRate": "RPC error %",
  "dashboard.pulse.peakAvg": "peak {peak} · avg {avg}",
  "dashboard.pulse.delta": "{sign}{delta} vs 30m ago",
  "dashboard.pulse.errorsOverCalls": "errors {errors} / calls {calls}",
  "dashboard.pulse.mockedTag": "mocked",
  // Dashboard redesign — Slot & Channel Health
  "dashboard.health.cardTitle": "Slot & channel health",
  "dashboard.health.slotsCenter": "{ready}/{total}",
  "dashboard.health.slotsCenterSub": "{percent}% ready",
  "dashboard.health.quorumLost": "Quorum lost: {count}",
  "dashboard.health.leaderMissing": "Leader missing: {count}",
  "dashboard.health.unreported": "Unreported: {count}",
  // Dashboard redesign — Incidents
  "dashboard.incidents.cardTitle": "Active incidents",
  "dashboard.incidents.cardCount": "({count})",
  "dashboard.incidents.cardDescription": "Slot, task, and controller anomalies sampled from the manager overview endpoint.",
  "dashboard.incidents.viewAll": "View all",
  "dashboard.incidents.inspect": "Inspect",
  "dashboard.incidents.slotQuorumLostTitle": "Slot {id} — quorum lost",
  "dashboard.incidents.slotLeaderMissingTitle": "Slot {id} — leader missing",
  "dashboard.incidents.slotSyncMismatchTitle": "Slot {id} — sync mismatch",
  "dashboard.incidents.taskFailedTitle": "Task {kind} failed on slot {id}",
  "dashboard.incidents.taskRetryingTitle": "Task {kind} retrying on slot {id}",
  "dashboard.incidents.controllerRaftTitle": "Controller raft {health} on node {id}",
  "dashboard.incidents.slotPeers": "desired [{desired}] → current [{current}]",
  "dashboard.incidents.taskDetail": "{kind} {step} · attempt {attempt} · last error: {error}",
  "dashboard.incidents.controllerRaftDetail": "first {first} / applied {applied} / snapshot {snapshot}",
  // Dashboard redesign — Topology
  "dashboard.topology.cardTitle": "Topology snapshot",
  "dashboard.topology.local": "local",
  "dashboard.topology.slotsValue": "{count} slots · {leaders} leader",
  "dashboard.topology.watermarkUnreported": "—",
  "dashboard.topology.openNode": "Open node {id}",
```

- [ ] **Step 2: Add Chinese dashboard keys**

Add after the existing `"dashboard.slotValue"` line in `zh-CN.ts`:

```typescript
  // Dashboard redesign — Health Hero
  "dashboard.scope": "范围：单节点集群",
  "dashboard.verdict.healthy": "集群健康",
  "dashboard.verdict.degraded": "集群降级",
  "dashboard.verdict.critical": "集群异常",
  "dashboard.verdict.summary": "{incidents} 个异常 · 控制器 Leader {id} · {ready}/{total} 槽位就绪 · {alive}/{totalNodes} 节点存活",
  "dashboard.verdict.summaryHealthy": "所有系统正常。生成时间：{time}。",
  // Dashboard redesign — Pulse
  "dashboard.pulse.messagesPerSec": "消息/秒",
  "dashboard.pulse.connections": "连接数",
  "dashboard.pulse.txKbPerSec": "发送 KB/s",
  "dashboard.pulse.rpcErrorRate": "RPC 错误率 %",
  "dashboard.pulse.peakAvg": "峰值 {peak} · 均值 {avg}",
  "dashboard.pulse.delta": "{sign}{delta} 较 30 分钟前",
  "dashboard.pulse.errorsOverCalls": "错误 {errors} / 调用 {calls}",
  "dashboard.pulse.mockedTag": "模拟数据",
  // Dashboard redesign — Slot & Channel Health
  "dashboard.health.cardTitle": "槽位与频道健康度",
  "dashboard.health.slotsCenter": "{ready}/{total}",
  "dashboard.health.slotsCenterSub": "{percent}% 就绪",
  "dashboard.health.quorumLost": "丢失法定人数：{count}",
  "dashboard.health.leaderMissing": "缺少 Leader：{count}",
  "dashboard.health.unreported": "未上报：{count}",
  // Dashboard redesign — Incidents
  "dashboard.incidents.cardTitle": "活动异常",
  "dashboard.incidents.cardCount": "（{count}）",
  "dashboard.incidents.cardDescription": "来自 manager overview 接口的槽位、任务与控制器异常样本。",
  "dashboard.incidents.viewAll": "查看全部",
  "dashboard.incidents.inspect": "查看",
  "dashboard.incidents.slotQuorumLostTitle": "槽位 {id} — 丢失法定人数",
  "dashboard.incidents.slotLeaderMissingTitle": "槽位 {id} — 缺少 Leader",
  "dashboard.incidents.slotSyncMismatchTitle": "槽位 {id} — 同步不一致",
  "dashboard.incidents.taskFailedTitle": "任务 {kind} 在槽位 {id} 失败",
  "dashboard.incidents.taskRetryingTitle": "任务 {kind} 在槽位 {id} 重试中",
  "dashboard.incidents.controllerRaftTitle": "Controller Raft {health} 于节点 {id}",
  "dashboard.incidents.slotPeers": "期望 [{desired}] → 当前 [{current}]",
  "dashboard.incidents.taskDetail": "{kind} {step} · 尝试 {attempt} · 最近错误：{error}",
  "dashboard.incidents.controllerRaftDetail": "first {first} / applied {applied} / snapshot {snapshot}",
  // Dashboard redesign — Topology
  "dashboard.topology.cardTitle": "拓扑快照",
  "dashboard.topology.local": "本地",
  "dashboard.topology.slotsValue": "{count} 槽位 · {leaders} 个 Leader",
  "dashboard.topology.watermarkUnreported": "—",
  "dashboard.topology.openNode": "打开节点 {id}",
```

- [ ] **Step 3: Remove obsolete keys from both files**

Remove these keys from both `en.ts` and `zh-CN.ts`:

```
dashboard.inspectAlerts
dashboard.controllerLeaderCardTitle
dashboard.controllerLeaderCardDescription
dashboard.controllerRaftCardTitle
dashboard.controllerRaftCardDescription
dashboard.controllerRaftReported
dashboard.controllerRaftWatermark
dashboard.controllerRaftWatermarkUnavailable
dashboard.controllerRaftOpen
dashboard.controllerRaftOpenForNode
dashboard.nodesCardTitle
dashboard.nodesCardDescription
dashboard.readySlotsCardTitle
dashboard.readySlotsCardDescription
dashboard.tasksCardTitle
dashboard.tasksCardDescription
dashboard.metricControllerHint
dashboard.operationsSummaryTitle
dashboard.operationsSummaryDescription
dashboard.nodesLabel
dashboard.nodesSummary
dashboard.slotsLabel
dashboard.slotsSummary
dashboard.tasksLabel
dashboard.tasksSummary
dashboard.generatedLabel
dashboard.alertListTitle
dashboard.alertListDescription
dashboard.controlQueueTitle
dashboard.controlQueueDescription
dashboard.table.slot
dashboard.table.kind
dashboard.table.status
dashboard.table.attempt
dashboard.table.lastError
dashboard.scopeSingleNodeCluster
```

Rename: `dashboard.scopeSingleNodeCluster` is replaced by `dashboard.scope` (already added above).

- [ ] **Step 4: Verify TypeScript compiles**

Run: `cd web && npx tsc --noEmit 2>&1 | head -30`
Expected: errors about missing dashboard components (page.tsx still references old code) — that's fine, we'll fix in later tasks.

- [ ] **Step 5: Commit**

```bash
git add web/src/i18n/messages/en.ts web/src/i18n/messages/zh-CN.ts
git commit -m "feat(web): add dashboard redesign i18n keys, remove obsolete"
```

---

## Task 2: Create `view-model.ts` with pure logic functions

**Files:**
- Create: `web/src/pages/dashboard/view-model.ts`
- Create: `web/src/pages/dashboard/view-model.test.ts`

- [ ] **Step 1: Write failing tests for `computeVerdict`**

Create `web/src/pages/dashboard/view-model.test.ts`:

```typescript
import { describe, expect, test } from "vitest"

import type { ManagerNodesResponse, ManagerOverviewResponse } from "@/lib/manager-api.types"
import { buildIncidents, computeVerdict, type IncidentItem } from "./view-model"

const baseOverview: ManagerOverviewResponse = {
  generated_at: "2026-04-23T08:00:00Z",
  cluster: { controller_leader_id: 1 },
  nodes: { total: 3, alive: 3, suspect: 0, dead: 0, draining: 0 },
  slots: { total: 64, ready: 64, quorum_lost: 0, leader_missing: 0, unreported: 0, peer_mismatch: 0, epoch_lag: 0 },
  tasks: { total: 0, pending: 0, retrying: 0, failed: 0 },
  anomalies: {
    slots: {
      quorum_lost: { count: 0, items: [] },
      leader_missing: { count: 0, items: [] },
      sync_mismatch: { count: 0, items: [] },
    },
    tasks: {
      failed: { count: 0, items: [] },
      retrying: { count: 0, items: [] },
    },
  },
}

const baseNodes: ManagerNodesResponse = {
  generated_at: "2026-04-23T08:00:00Z",
  controller_leader_id: 1,
  total: 2,
  items: [
    {
      node_id: 1, name: "node-1", addr: "127.0.0.1:7000", status: "alive",
      last_heartbeat_at: "2026-04-23T08:00:00Z", is_local: true, capacity_weight: 1,
      controller: { role: "leader", voter: true, leader_id: 1, raft_health: "healthy", first_index: 10, applied_index: 20, snapshot_index: 9 },
      slot_stats: { count: 32, leader_count: 16 },
    },
    {
      node_id: 2, name: "node-2", addr: "127.0.0.1:7001", status: "alive",
      last_heartbeat_at: "2026-04-23T08:00:00Z", is_local: false, capacity_weight: 1,
      controller: { role: "follower", voter: true, leader_id: 1, raft_health: "healthy", first_index: 10, applied_index: 20, snapshot_index: 9 },
      slot_stats: { count: 32, leader_count: 16 },
    },
  ],
}

describe("computeVerdict", () => {
  test("returns healthy when no anomalies", () => {
    expect(computeVerdict(baseOverview, baseNodes)).toBe("healthy")
  })

  test("returns degraded when slots have quorum_lost", () => {
    const overview = { ...baseOverview, slots: { ...baseOverview.slots, quorum_lost: 1 } }
    expect(computeVerdict(overview, baseNodes)).toBe("degraded")
  })

  test("returns degraded when tasks are retrying", () => {
    const overview = { ...baseOverview, tasks: { ...baseOverview.tasks, retrying: 1 } }
    expect(computeVerdict(overview, baseNodes)).toBe("degraded")
  })

  test("returns degraded when controller raft is snapshot_required", () => {
    const nodes: ManagerNodesResponse = {
      ...baseNodes,
      items: [{ ...baseNodes.items[0], controller: { ...baseNodes.items[0].controller, raft_health: "snapshot_required" } }, baseNodes.items[1]],
    }
    expect(computeVerdict(baseOverview, nodes)).toBe("degraded")
  })

  test("returns critical when tasks have failed", () => {
    const overview = { ...baseOverview, tasks: { ...baseOverview.tasks, failed: 1 } }
    expect(computeVerdict(overview, baseNodes)).toBe("critical")
  })

  test("returns critical when controller raft is restore_failed", () => {
    const nodes: ManagerNodesResponse = {
      ...baseNodes,
      items: [{ ...baseNodes.items[0], controller: { ...baseNodes.items[0].controller, raft_health: "restore_failed" } }, baseNodes.items[1]],
    }
    expect(computeVerdict(baseOverview, nodes)).toBe("critical")
  })

  test("critical wins over degraded", () => {
    const overview = { ...baseOverview, slots: { ...baseOverview.slots, quorum_lost: 1 }, tasks: { ...baseOverview.tasks, failed: 1 } }
    expect(computeVerdict(overview, baseNodes)).toBe("critical")
  })
})
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd web && npx vitest run src/pages/dashboard/view-model.test.ts 2>&1 | tail -10`
Expected: FAIL — module `./view-model` not found.

- [ ] **Step 3: Implement `view-model.ts`**

Create `web/src/pages/dashboard/view-model.ts`:

```typescript
import type { IntlShape } from "react-intl"

import type {
  ManagerChannelClusterSummaryResponse,
  ManagerNode,
  ManagerNodesResponse,
  ManagerOverviewResponse,
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

const DEGRADED_RAFT_STATES = new Set([
  "snapshot_required",
  "snapshot_transferring",
  "append_catchup",
  "compaction_degraded",
])

const CRITICAL_RAFT_STATES = new Set(["restore_failed"])

export function computeVerdict(overview: ManagerOverviewResponse, nodes: ManagerNodesResponse): Verdict {
  const hasCritical =
    overview.tasks.failed > 0 ||
    nodes.items.some((n) => CRITICAL_RAFT_STATES.has(n.controller.raft_health || ""))

  if (hasCritical) return "critical"

  const hasDegraded =
    overview.slots.quorum_lost > 0 ||
    overview.slots.leader_missing > 0 ||
    overview.tasks.retrying > 0 ||
    nodes.items.some((n) => DEGRADED_RAFT_STATES.has(n.controller.raft_health || ""))

  if (hasDegraded) return "degraded"

  return "healthy"
}

export function buildIncidents(
  intl: IntlShape,
  overview: ManagerOverviewResponse,
  nodes: ManagerNodesResponse,
): IncidentItem[] {
  const items: IncidentItem[] = []

  for (const slot of overview.anomalies.slots.quorum_lost.items) {
    items.push({
      key: `slot-quorum-${slot.slot_id}`,
      severity: "critical",
      title: intl.formatMessage({ id: "dashboard.incidents.slotQuorumLostTitle" }, { id: slot.slot_id }),
      detail: intl.formatMessage({ id: "dashboard.incidents.slotPeers" }, { desired: slot.desired_peers.join(","), current: slot.current_peers.join(",") }),
      href: `/cluster/slots?focus=${slot.slot_id}`,
      ariaLabel: intl.formatMessage({ id: "dashboard.incidents.slotQuorumLostTitle" }, { id: slot.slot_id }),
    })
  }

  for (const slot of overview.anomalies.slots.leader_missing.items) {
    items.push({
      key: `slot-leader-${slot.slot_id}`,
      severity: "critical",
      title: intl.formatMessage({ id: "dashboard.incidents.slotLeaderMissingTitle" }, { id: slot.slot_id }),
      detail: intl.formatMessage({ id: "dashboard.incidents.slotPeers" }, { desired: slot.desired_peers.join(","), current: slot.current_peers.join(",") }),
      href: `/cluster/slots?focus=${slot.slot_id}`,
      ariaLabel: intl.formatMessage({ id: "dashboard.incidents.slotLeaderMissingTitle" }, { id: slot.slot_id }),
    })
  }

  for (const slot of overview.anomalies.slots.sync_mismatch.items) {
    items.push({
      key: `slot-sync-${slot.slot_id}`,
      severity: "warning",
      title: intl.formatMessage({ id: "dashboard.incidents.slotSyncMismatchTitle" }, { id: slot.slot_id }),
      detail: intl.formatMessage({ id: "dashboard.incidents.slotPeers" }, { desired: slot.desired_peers.join(","), current: slot.current_peers.join(",") }),
      href: `/cluster/slots?focus=${slot.slot_id}`,
      ariaLabel: intl.formatMessage({ id: "dashboard.incidents.slotSyncMismatchTitle" }, { id: slot.slot_id }),
    })
  }

  for (const task of overview.anomalies.tasks.failed.items) {
    items.push({
      key: `task-failed-${task.slot_id}-${task.kind}`,
      severity: "critical",
      title: intl.formatMessage({ id: "dashboard.incidents.taskFailedTitle" }, { kind: task.kind, id: task.slot_id }),
      detail: intl.formatMessage({ id: "dashboard.incidents.taskDetail" }, { kind: task.kind, step: task.step, attempt: task.attempt, error: task.last_error || "-" }),
      href: `/cluster/tasks?focus=slot-${task.slot_id}`,
      ariaLabel: intl.formatMessage({ id: "dashboard.incidents.taskFailedTitle" }, { kind: task.kind, id: task.slot_id }),
    })
  }

  for (const task of overview.anomalies.tasks.retrying.items) {
    items.push({
      key: `task-retrying-${task.slot_id}-${task.kind}`,
      severity: "warning",
      title: intl.formatMessage({ id: "dashboard.incidents.taskRetryingTitle" }, { kind: task.kind, id: task.slot_id }),
      detail: intl.formatMessage({ id: "dashboard.incidents.taskDetail" }, { kind: task.kind, step: task.step, attempt: task.attempt, error: task.last_error || "-" }),
      href: `/cluster/tasks?focus=slot-${task.slot_id}`,
      ariaLabel: intl.formatMessage({ id: "dashboard.incidents.taskRetryingTitle" }, { kind: task.kind, id: task.slot_id }),
    })
  }

  for (const node of nodes.items) {
    const health = node.controller.raft_health || "unknown"
    if (DEGRADED_RAFT_STATES.has(health) || CRITICAL_RAFT_STATES.has(health)) {
      items.push({
        key: `raft-${node.node_id}`,
        severity: CRITICAL_RAFT_STATES.has(health) ? "critical" : "warning",
        title: intl.formatMessage({ id: "dashboard.incidents.controllerRaftTitle" }, { health: health.replaceAll("_", " "), id: node.node_id }),
        detail: intl.formatMessage({ id: "dashboard.incidents.controllerRaftDetail" }, { first: node.controller.first_index, applied: node.controller.applied_index, snapshot: node.controller.snapshot_index }),
        href: `/controller?node_id=${node.node_id}`,
        ariaLabel: intl.formatMessage({ id: "dashboard.incidents.controllerRaftTitle" }, { health: health.replaceAll("_", " "), id: node.node_id }),
      })
    }
  }

  return items
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

export function buildTopologyRows(nodes: ManagerNodesResponse): TopologyRowData[] {
  return nodes.items.map((node) => ({
    nodeId: node.node_id,
    name: node.name,
    status: node.status,
    isLocal: node.is_local,
    role: node.controller.role,
    slotsCount: node.slot_stats.count,
    leaderCount: node.slot_stats.leader_count,
    raftHealth: node.controller.raft_health || "unknown",
    hasWatermark: Boolean(node.controller.raft_health && node.controller.raft_health !== "unknown"),
    firstIndex: node.controller.first_index ?? 0,
    appliedIndex: node.controller.applied_index ?? 0,
    snapshotIndex: node.controller.snapshot_index ?? 0,
    href: `/controller?node_id=${node.node_id}`,
  }))
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd web && npx vitest run src/pages/dashboard/view-model.test.ts 2>&1 | tail -10`
Expected: all 7 tests PASS.

- [ ] **Step 5: Add `buildIncidents` tests**

Append to `view-model.test.ts`:

```typescript
describe("buildIncidents", () => {
  const intl = { formatMessage: ({ id }: { id: string }, values?: Record<string, unknown>) => {
    const parts = Object.entries(values || {}).map(([k, v]) => `${k}=${v}`)
    return `${id}${parts.length ? `(${parts.join(",")})` : ""}`
  }} as unknown as import("react-intl").IntlShape

  test("returns empty array when no anomalies", () => {
    expect(buildIncidents(intl, baseOverview, baseNodes)).toEqual([])
  })

  test("orders: quorum_lost > leader_missing > sync_mismatch > task_failed > task_retrying > raft", () => {
    const overview: ManagerOverviewResponse = {
      ...baseOverview,
      anomalies: {
        slots: {
          quorum_lost: { count: 1, items: [{ slot_id: 1, quorum: "quorum_lost", sync: "ok", leader_id: 1, desired_peers: [1, 2], current_peers: [1], last_report_at: "" }] },
          leader_missing: { count: 1, items: [{ slot_id: 2, quorum: "ok", sync: "ok", leader_id: 0, desired_peers: [1, 2], current_peers: [1, 2], last_report_at: "" }] },
          sync_mismatch: { count: 1, items: [{ slot_id: 3, quorum: "ok", sync: "peer_mismatch", leader_id: 1, desired_peers: [1, 2, 3], current_peers: [1, 2], last_report_at: "" }] },
        },
        tasks: {
          failed: { count: 1, items: [{ slot_id: 4, kind: "migrate", step: "copy", status: "failed", source_node: 1, target_node: 2, attempt: 5, next_run_at: null, last_error: "disk full" }] },
          retrying: { count: 1, items: [{ slot_id: 5, kind: "rebalance", step: "plan", status: "retrying", source_node: 1, target_node: 2, attempt: 3, next_run_at: null, last_error: "timeout" }] },
        },
      },
    }
    const nodes: ManagerNodesResponse = {
      ...baseNodes,
      items: [{ ...baseNodes.items[0], controller: { ...baseNodes.items[0].controller, raft_health: "snapshot_required" } }, baseNodes.items[1]],
    }

    const result = buildIncidents(intl, overview, nodes)
    const keys = result.map((i) => i.key)
    expect(keys).toEqual([
      "slot-quorum-1",
      "slot-leader-2",
      "slot-sync-3",
      "task-failed-4-migrate",
      "task-retrying-5-rebalance",
      "raft-1",
    ])
  })

  test("assigns correct severity", () => {
    const overview: ManagerOverviewResponse = {
      ...baseOverview,
      anomalies: {
        ...baseOverview.anomalies,
        slots: {
          ...baseOverview.anomalies.slots,
          quorum_lost: { count: 1, items: [{ slot_id: 9, quorum: "quorum_lost", sync: "ok", leader_id: 0, desired_peers: [1, 2, 3], current_peers: [1], last_report_at: "" }] },
        },
      },
    }
    const result = buildIncidents(intl, overview, baseNodes)
    expect(result[0].severity).toBe("critical")
  })
})
```

- [ ] **Step 6: Run all view-model tests**

Run: `cd web && npx vitest run src/pages/dashboard/view-model.test.ts 2>&1 | tail -10`
Expected: all tests PASS.

- [ ] **Step 7: Commit**

```bash
git add web/src/pages/dashboard/view-model.ts web/src/pages/dashboard/view-model.test.ts
git commit -m "feat(web): add dashboard view-model (verdict, incidents, topology)"
```

---

## Task 3: Create `use-dashboard-pulse` hook (deterministic mock)

**Files:**
- Create: `web/src/pages/dashboard/use-dashboard-pulse.ts`
- Create: `web/src/pages/dashboard/use-dashboard-pulse.test.ts`

- [ ] **Step 1: Write failing test**

Create `web/src/pages/dashboard/use-dashboard-pulse.test.ts`:

```typescript
import { describe, expect, test } from "vitest"

import { generatePulseData, type PulseSeries } from "./use-dashboard-pulse"

describe("generatePulseData", () => {
  test("returns 60 data points for each series", () => {
    const result = generatePulseData("2026-04-23T08:00:00Z")
    expect(result.messagesPerSec.series).toHaveLength(60)
    expect(result.connections.series).toHaveLength(60)
    expect(result.txKbPerSec.series).toHaveLength(60)
    expect(result.rpcErrorRate.series).toHaveLength(60)
  })

  test("is deterministic for the same seed", () => {
    const a = generatePulseData("2026-04-23T08:00:00Z")
    const b = generatePulseData("2026-04-23T08:00:00Z")
    expect(a).toEqual(b)
  })

  test("produces different data for different seeds", () => {
    const a = generatePulseData("2026-04-23T08:00:00Z")
    const b = generatePulseData("2026-04-23T09:00:00Z")
    expect(a.messagesPerSec.series).not.toEqual(b.messagesPerSec.series)
  })

  test("latest equals the last element of series", () => {
    const result = generatePulseData("2026-04-23T08:00:00Z")
    expect(result.messagesPerSec.latest).toBe(result.messagesPerSec.series[59])
  })

  test("peak is the max of the series", () => {
    const result = generatePulseData("2026-04-23T08:00:00Z")
    expect(result.messagesPerSec.peak).toBe(Math.max(...result.messagesPerSec.series))
  })

  test("avg is the mean of the series", () => {
    const result = generatePulseData("2026-04-23T08:00:00Z")
    const sum = result.messagesPerSec.series.reduce((a, b) => a + b, 0)
    expect(result.messagesPerSec.avg).toBe(Math.round(sum / 60))
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/pages/dashboard/use-dashboard-pulse.test.ts 2>&1 | tail -10`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement the hook**

Create `web/src/pages/dashboard/use-dashboard-pulse.ts`:

```typescript
import { useMemo } from "react"

export type PulseSeries = {
  latest: number
  peak: number
  avg: number
  series: number[]
}

export type PulseData = {
  messagesPerSec: PulseSeries
  connections: PulseSeries
  txKbPerSec: PulseSeries
  rpcErrorRate: PulseSeries
}

function seededRandom(seed: number) {
  let s = seed
  return () => {
    s = (s * 1664525 + 1013904223) >>> 0
    return (s >>> 0) / 4294967296
  }
}

function hashString(str: string): number {
  let hash = 5381
  for (let i = 0; i < str.length; i++) {
    hash = ((hash << 5) + hash + str.charCodeAt(i)) >>> 0
  }
  return hash
}

function generateSeries(rand: () => number, base: number, variance: number, count: number): number[] {
  const series: number[] = []
  let current = base
  for (let i = 0; i < count; i++) {
    current = Math.max(0, current + (rand() - 0.5) * variance)
    series.push(Math.round(current))
  }
  return series
}

function buildPulseSeries(series: number[]): PulseSeries {
  const latest = series[series.length - 1]
  const peak = Math.max(...series)
  const avg = Math.round(series.reduce((a, b) => a + b, 0) / series.length)
  return { latest, peak, avg, series }
}

export function generatePulseData(seed: string): PulseData {
  const hash = hashString(seed)
  const rand = seededRandom(hash)

  return {
    messagesPerSec: buildPulseSeries(generateSeries(rand, 1200, 400, 60)),
    connections: buildPulseSeries(generateSeries(rand, 850, 100, 60)),
    txKbPerSec: buildPulseSeries(generateSeries(rand, 320, 150, 60)),
    rpcErrorRate: buildPulseSeries(generateSeries(rand, 2, 3, 60)),
  }
}

export function useDashboardPulse(generatedAt: string | null): PulseData | null {
  return useMemo(() => {
    if (!generatedAt) return null
    return generatePulseData(generatedAt)
  }, [generatedAt])
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd web && npx vitest run src/pages/dashboard/use-dashboard-pulse.test.ts 2>&1 | tail -10`
Expected: all 6 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/pages/dashboard/use-dashboard-pulse.ts web/src/pages/dashboard/use-dashboard-pulse.test.ts
git commit -m "feat(web): add useDashboardPulse hook with deterministic mock data"
```

---

## Task 4: Create `HealthHero` component

**Files:**
- Create: `web/src/pages/dashboard/components/health-hero.tsx`

- [ ] **Step 1: Create the component**

Create `web/src/pages/dashboard/components/health-hero.tsx`:

```typescript
import { useIntl } from "react-intl"

import { Button } from "@/components/ui/button"
import { cn } from "@/lib/utils"
import type { Verdict } from "../view-model"

type HealthHeroProps = {
  verdict: Verdict
  generatedAt: string | null
  controllerLeaderId: number
  slotsReady: number
  slotsTotal: number
  nodesAlive: number
  nodesTotal: number
  incidentCount: number
  refreshing: boolean
  onRefresh: () => void
}

function formatTimestamp(locale: string, value: string) {
  return new Intl.DateTimeFormat(locale, {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  }).format(new Date(value))
}

export function HealthHero({
  verdict,
  generatedAt,
  controllerLeaderId,
  slotsReady,
  slotsTotal,
  nodesAlive,
  nodesTotal,
  incidentCount,
  refreshing,
  onRefresh,
}: HealthHeroProps) {
  const intl = useIntl()

  const verdictLabel = intl.formatMessage({ id: `dashboard.verdict.${verdict}` })
  const summary = verdict === "healthy" && generatedAt
    ? intl.formatMessage({ id: "dashboard.verdict.summaryHealthy" }, { time: formatTimestamp(intl.locale, generatedAt) })
    : intl.formatMessage({ id: "dashboard.verdict.summary" }, {
        incidents: incidentCount,
        id: controllerLeaderId,
        ready: slotsReady,
        total: slotsTotal,
        alive: nodesAlive,
        totalNodes: nodesTotal,
      })

  return (
    <section
      className="overflow-hidden rounded-3xl border border-border/80 bg-[linear-gradient(135deg,rgba(255,255,255,0.07),rgba(255,255,255,0.025))] shadow-[0_24px_80px_rgba(0,0,0,0.18)]"
    >
      <div className="flex flex-col gap-4 p-5 lg:flex-row lg:items-center lg:justify-between lg:p-6">
        <div className="flex items-center gap-4">
          <span
            aria-live="polite"
            className={cn(
              "inline-flex items-center gap-2 rounded-full border px-3 py-1.5 text-sm font-semibold",
              verdict === "healthy" && "border-success/25 bg-success/10 text-success",
              verdict === "degraded" && "border-warning/25 bg-warning/10 text-warning",
              verdict === "critical" && "border-destructive/30 bg-destructive/10 text-destructive",
            )}
            role="status"
          >
            <span className={cn(
              "size-2 rounded-full",
              verdict === "healthy" && "bg-success",
              verdict === "degraded" && "bg-warning",
              verdict === "critical" && "bg-destructive",
            )} />
            {verdictLabel}
          </span>
          <p className="text-sm text-muted-foreground">{summary}</p>
        </div>
        <div className="flex items-center gap-3">
          {generatedAt ? (
            <span className="text-xs text-muted-foreground">
              {intl.formatMessage({ id: "dashboard.generatedAtValue" }, { value: formatTimestamp(intl.locale, generatedAt) })}
            </span>
          ) : null}
          <Button onClick={onRefresh} size="sm" variant="outline">
            {refreshing
              ? intl.formatMessage({ id: "common.refreshing" })
              : intl.formatMessage({ id: "common.refresh" })}
          </Button>
        </div>
      </div>
      <div className="border-t border-border/80 bg-background/35 px-5 py-2">
        <div className="flex items-center gap-3 font-mono text-[11px] font-semibold uppercase tracking-[0.22em] text-muted-foreground">
          <span>{intl.formatMessage({ id: "dashboard.cockpitEyebrow" })}</span>
          <span className="text-border">·</span>
          <span>{intl.formatMessage({ id: "dashboard.scope" })}</span>
        </div>
      </div>
    </section>
  )
}
```

- [ ] **Step 2: Commit**

```bash
mkdir -p web/src/pages/dashboard/components
git add web/src/pages/dashboard/components/health-hero.tsx
git commit -m "feat(web): add HealthHero component for dashboard"
```

---

## Task 5: Create `PulseTile` component

**Files:**
- Create: `web/src/pages/dashboard/components/pulse-tile.tsx`

- [ ] **Step 1: Create the component**

Create `web/src/pages/dashboard/components/pulse-tile.tsx`:

```typescript
import { useIntl } from "react-intl"
import { Line, LineChart, ResponsiveContainer } from "recharts"

import { cn } from "@/lib/utils"

type PulseTileProps = {
  label: string
  value: number
  valueSuffix?: string
  sub: string
  series: number[]
  tone?: "default" | "danger"
  mocked?: boolean
}

export function PulseTile({ label, value, valueSuffix, sub, series, tone = "default", mocked }: PulseTileProps) {
  const intl = useIntl()
  const data = series.map((v, i) => ({ i, v }))
  const strokeColor = tone === "danger" ? "var(--chart-4)" : "var(--chart-1)"

  return (
    <div className="relative flex flex-col justify-between rounded-2xl border border-border/80 bg-card/88 px-4 py-3 shadow-[inset_0_1px_0_rgba(255,255,255,0.035)]">
      <div className="font-mono text-[11px] font-semibold uppercase tracking-[0.14em] text-muted-foreground">
        {label}
      </div>
      <div className="mt-2 flex items-baseline gap-1">
        <span className="font-mono text-2xl font-semibold tracking-[-0.04em] text-foreground">
          {value.toLocaleString()}
        </span>
        {valueSuffix ? <span className="text-sm text-muted-foreground">{valueSuffix}</span> : null}
      </div>
      <div className="mt-1 text-xs text-muted-foreground">{sub}</div>
      <div className="mt-3 h-10" aria-label={`${label}: ${value}`}>
        <ResponsiveContainer height="100%" width="100%">
          <LineChart data={data} margin={{ top: 2, right: 0, bottom: 0, left: 0 }}>
            <Line
              dataKey="v"
              dot={false}
              stroke={strokeColor}
              strokeWidth={1.5}
              type="monotone"
            />
          </LineChart>
        </ResponsiveContainer>
      </div>
      {mocked ? (
        <span className={cn(
          "absolute right-2 top-2 rounded-full border border-border/60 bg-muted/50 px-1.5 py-0.5 text-[9px] text-muted-foreground",
        )}>
          {intl.formatMessage({ id: "dashboard.pulse.mockedTag" })}
        </span>
      ) : null}
    </div>
  )
}
```

- [ ] **Step 2: Commit**

```bash
git add web/src/pages/dashboard/components/pulse-tile.tsx
git commit -m "feat(web): add PulseTile sparkline component for dashboard"
```

---

## Task 6: Create `SlotChannelHealth` component (donut)

**Files:**
- Create: `web/src/pages/dashboard/components/slot-channel-health.tsx`

- [ ] **Step 1: Create the component**

Create `web/src/pages/dashboard/components/slot-channel-health.tsx`:

```typescript
import { useIntl } from "react-intl"
import { Link } from "react-router-dom"
import { Cell, Pie, PieChart, ResponsiveContainer } from "recharts"

import { Button } from "@/components/ui/button"
import { SectionCard } from "@/components/shell/section-card"
import { cn } from "@/lib/utils"
import type { ManagerChannelClusterSummaryResponse, ManagerOverviewResponse } from "@/lib/manager-api.types"

type SlotChannelHealthProps = {
  slots: ManagerOverviewResponse["slots"]
  channelCluster: ManagerChannelClusterSummaryResponse
}

const DONUT_COLORS = {
  ready: "var(--chart-1)",
  quorumLost: "var(--chart-4)",
  leaderMissing: "var(--chart-3)",
  unreported: "var(--chart-5)",
}

export function SlotChannelHealth({ slots, channelCluster }: SlotChannelHealthProps) {
  const intl = useIntl()

  const donutData = [
    { key: "ready", value: slots.ready, fill: DONUT_COLORS.ready },
    { key: "quorumLost", value: slots.quorum_lost, fill: DONUT_COLORS.quorumLost },
    { key: "leaderMissing", value: slots.leader_missing, fill: DONUT_COLORS.leaderMissing },
    { key: "unreported", value: slots.unreported, fill: DONUT_COLORS.unreported },
  ].filter((d) => d.value > 0)

  const percent = slots.total > 0 ? Math.round((slots.ready / slots.total) * 100) : 0

  return (
    <SectionCard title={intl.formatMessage({ id: "dashboard.health.cardTitle" })}>
      <div className="flex flex-col items-center gap-4">
        <div className="relative h-36 w-36">
          <ResponsiveContainer height="100%" width="100%">
            <PieChart>
              <Pie
                data={donutData}
                dataKey="value"
                innerRadius={42}
                outerRadius={62}
                paddingAngle={2}
                strokeWidth={0}
              >
                {donutData.map((entry) => (
                  <Cell fill={entry.fill} key={entry.key} />
                ))}
              </Pie>
            </PieChart>
          </ResponsiveContainer>
          <div className="absolute inset-0 flex flex-col items-center justify-center">
            <span className="font-mono text-lg font-semibold text-foreground">
              {intl.formatMessage({ id: "dashboard.health.slotsCenter" }, { ready: slots.ready, total: slots.total })}
            </span>
            <span className="text-xs text-muted-foreground">
              {intl.formatMessage({ id: "dashboard.health.slotsCenterSub" }, { percent })}
            </span>
          </div>
        </div>

        <div className="grid w-full gap-1 text-sm">
          <CounterRow label={intl.formatMessage({ id: "dashboard.health.quorumLost" }, { count: slots.quorum_lost })} warn={slots.quorum_lost > 0} />
          <CounterRow label={intl.formatMessage({ id: "dashboard.health.leaderMissing" }, { count: slots.leader_missing })} warn={slots.leader_missing > 0} />
          <CounterRow label={intl.formatMessage({ id: "dashboard.health.unreported" }, { count: slots.unreported })} warn={false} />
        </div>

        <div className="w-full border-t border-border/80 pt-3">
          <div className="flex items-start justify-between">
            <div>
              <div className="text-xs uppercase tracking-[0.14em] text-muted-foreground">
                {intl.formatMessage({ id: "dashboard.channelHealthTitle" })}
              </div>
              <div className="mt-2 space-y-1 text-sm">
                <div className="text-success">
                  {intl.formatMessage({ id: "dashboard.channelHealthHealthy" }, { count: channelCluster.healthy })}
                </div>
                <div className={cn(channelCluster.isr_insufficient > 0 && "text-warning")}>
                  {intl.formatMessage({ id: "dashboard.channelHealthIsrInsufficient" }, { count: channelCluster.isr_insufficient })}
                </div>
                <div className={cn(channelCluster.no_leader > 0 && "text-destructive")}>
                  {intl.formatMessage({ id: "dashboard.channelHealthNoLeader" }, { count: channelCluster.no_leader })}
                </div>
              </div>
            </div>
            <Button asChild size="sm" variant="outline">
              <Link
                aria-label={intl.formatMessage({ id: "dashboard.channelHealthOpen" })}
                to="/cluster/channels?tab=overview"
              >
                {intl.formatMessage({ id: "common.inspect" })}
              </Link>
            </Button>
          </div>
        </div>
      </div>
    </SectionCard>
  )
}

function CounterRow({ label, warn }: { label: string; warn: boolean }) {
  return (
    <div className={cn("rounded-lg px-2 py-1", warn ? "text-warning" : "text-muted-foreground")}>
      {label}
    </div>
  )
}
```

- [ ] **Step 2: Commit**

```bash
git add web/src/pages/dashboard/components/slot-channel-health.tsx
git commit -m "feat(web): add SlotChannelHealth donut component for dashboard"
```

---

## Task 7: Create `IncidentList` component

**Files:**
- Create: `web/src/pages/dashboard/components/incident-list.tsx`

- [ ] **Step 1: Create the component**

Create `web/src/pages/dashboard/components/incident-list.tsx`:

```typescript
import { useIntl } from "react-intl"
import { Link } from "react-router-dom"

import { Button } from "@/components/ui/button"
import { SectionCard } from "@/components/shell/section-card"
import { cn } from "@/lib/utils"
import type { IncidentItem } from "../view-model"

type IncidentListProps = {
  items: IncidentItem[]
}

export function IncidentList({ items }: IncidentListProps) {
  const intl = useIntl()

  const title = `${intl.formatMessage({ id: "dashboard.incidents.cardTitle" })} ${items.length > 0 ? intl.formatMessage({ id: "dashboard.incidents.cardCount" }, { count: items.length }) : ""}`

  return (
    <SectionCard
      description={intl.formatMessage({ id: "dashboard.incidents.cardDescription" })}
      title={title}
    >
      {items.length > 0 ? (
        <div className="space-y-3">
          {items.map((item) => (
            <div
              className="flex items-start justify-between gap-3 rounded-2xl border border-border/80 bg-muted/25 px-3 py-3"
              key={item.key}
            >
              <div className="flex items-start gap-2">
                <span
                  className={cn(
                    "mt-1.5 size-2 shrink-0 rounded-full",
                    item.severity === "critical" && "bg-destructive",
                    item.severity === "warning" && "bg-warning",
                  )}
                />
                <div>
                  <div className="text-sm font-medium text-foreground">{item.title}</div>
                  <div className="mt-1 font-mono text-xs text-muted-foreground">{item.detail}</div>
                </div>
              </div>
              <Button asChild size="sm" variant="outline">
                <Link aria-label={item.ariaLabel} to={item.href}>
                  {intl.formatMessage({ id: "dashboard.incidents.inspect" })}
                </Link>
              </Button>
            </div>
          ))}
          <div className="text-right">
            <Button asChild size="sm" variant="ghost">
              <Link to="/cluster/diagnostics?tab=trace">
                {intl.formatMessage({ id: "dashboard.incidents.viewAll" })}
              </Link>
            </Button>
          </div>
        </div>
      ) : (
        <div className="rounded-2xl border border-primary/25 bg-primary/8 px-4 py-4">
          <div className="flex items-center gap-2 text-sm font-semibold text-foreground">
            <span className="size-2 rounded-full bg-[var(--status-healthy)]" />
            {intl.formatMessage({ id: "dashboard.noActiveAlertsTitle" })}
          </div>
          <p className="mt-2 text-sm leading-6 text-muted-foreground">
            {intl.formatMessage({ id: "dashboard.noActiveAlertsDescription" })}
          </p>
        </div>
      )}
    </SectionCard>
  )
}
```

- [ ] **Step 2: Commit**

```bash
git add web/src/pages/dashboard/components/incident-list.tsx
git commit -m "feat(web): add IncidentList component for dashboard"
```

---

## Task 8: Create `TopologyRow` component

**Files:**
- Create: `web/src/pages/dashboard/components/topology-row.tsx`

- [ ] **Step 1: Create the component**

Create `web/src/pages/dashboard/components/topology-row.tsx`:

```typescript
import { useIntl } from "react-intl"
import { Link } from "react-router-dom"

import { Button } from "@/components/ui/button"
import { StatusBadge } from "@/components/manager/status-badge"
import { cn } from "@/lib/utils"
import type { TopologyRowData } from "../view-model"

type TopologyRowProps = {
  row: TopologyRowData
}

export function TopologyRow({ row }: TopologyRowProps) {
  const intl = useIntl()

  return (
    <div className="grid grid-cols-[auto_1fr_auto_auto_auto_auto_auto] items-center gap-3 rounded-xl border border-border/60 bg-muted/20 px-3 py-2 text-sm">
      <span
        className={cn(
          "size-2 rounded-full",
          row.status === "alive" && "bg-[var(--status-healthy)]",
          (row.status === "draining" || row.status === "suspect") && "bg-[var(--status-warning)]",
          row.status === "dead" && "bg-[var(--status-error)]",
        )}
      />
      <div className="flex items-center gap-1.5 truncate">
        <span className="font-medium text-foreground">{row.name}</span>
        <span className="font-mono text-xs text-muted-foreground">(#{row.nodeId})</span>
        {row.isLocal ? (
          <span className="rounded-full border border-primary/25 bg-primary/10 px-1.5 py-0.5 text-[10px] font-medium text-primary">
            {intl.formatMessage({ id: "dashboard.topology.local" })}
          </span>
        ) : null}
      </div>
      <StatusBadge value={row.status} />
      <span className="text-xs text-muted-foreground">{row.role}</span>
      <span className="text-xs text-muted-foreground">
        {intl.formatMessage({ id: "dashboard.topology.slotsValue" }, { count: row.slotsCount, leaders: row.leaderCount })}
      </span>
      <span className="text-xs text-muted-foreground">
        {row.hasWatermark
          ? `first ${row.firstIndex} / applied ${row.appliedIndex} / snapshot ${row.snapshotIndex}`
          : intl.formatMessage({ id: "dashboard.topology.watermarkUnreported" })}
      </span>
      <Button asChild size="sm" variant="ghost">
        <Link
          aria-label={intl.formatMessage({ id: "dashboard.topology.openNode" }, { id: row.nodeId })}
          to={row.href}
        >
          ↗
        </Link>
      </Button>
    </div>
  )
}
```

- [ ] **Step 2: Commit**

```bash
git add web/src/pages/dashboard/components/topology-row.tsx
git commit -m "feat(web): add TopologyRow component for dashboard"
```

---

## Task 9: Rewrite `page.tsx` — the orchestrator

**Files:**
- Modify: `web/src/pages/dashboard/page.tsx`

- [ ] **Step 1: Replace `page.tsx` entirely**

Replace the full content of `web/src/pages/dashboard/page.tsx` with:

```typescript
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

  if (state.loading) {
    return (
      <PageContainer>
        <ResourceState kind="loading" title={intl.formatMessage({ id: "dashboard.title" })} />
      </PageContainer>
    )
  }

  if (state.error) {
    return (
      <PageContainer>
        <ResourceState
          kind={formatErrorKind(state.error)}
          onRetry={() => { void loadDashboard(false) }}
          title={intl.formatMessage({ id: "dashboard.title" })}
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
```

- [ ] **Step 2: Verify TypeScript compiles**

Run: `cd web && npx tsc --noEmit 2>&1 | head -20`
Expected: no errors (or only unrelated warnings from other pages).

- [ ] **Step 3: Commit**

```bash
git add web/src/pages/dashboard/page.tsx
git commit -m "feat(web): rewrite dashboard page as health-first cockpit"
```

---

## Task 10: Rewrite `page.test.tsx`

**Files:**
- Modify: `web/src/pages/dashboard/page.test.tsx`

- [ ] **Step 1: Replace `page.test.tsx` entirely**

Replace the full content of `web/src/pages/dashboard/page.test.tsx` with:

```typescript
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { MemoryRouter } from "react-router-dom"
import { beforeEach, expect, test, vi } from "vitest"

import { I18nProvider } from "@/i18n/provider"
import { resetLocale } from "@/i18n/locale-store"
import { ManagerApiError } from "@/lib/manager-api"
import { DashboardPage } from "@/pages/dashboard/page"

const getOverviewMock = vi.fn()
const getTasksMock = vi.fn()
const getNodesMock = vi.fn()
const getChannelClusterSummaryMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getOverview: (...args: unknown[]) => getOverviewMock(...args),
    getTasks: (...args: unknown[]) => getTasksMock(...args),
    getNodes: (...args: unknown[]) => getNodesMock(...args),
    getChannelClusterSummary: (...args: unknown[]) => getChannelClusterSummaryMock(...args),
  }
})

const overviewFixture = {
  generated_at: "2026-04-23T08:00:00Z",
  cluster: { controller_leader_id: 1 },
  nodes: { total: 3, alive: 3, suspect: 0, dead: 0, draining: 1 },
  slots: { total: 64, ready: 63, quorum_lost: 1, leader_missing: 0, unreported: 0, peer_mismatch: 1, epoch_lag: 0 },
  tasks: { total: 2, pending: 1, retrying: 1, failed: 0 },
  anomalies: {
    slots: {
      quorum_lost: { count: 1, items: [{ slot_id: 9, quorum: "quorum_lost", sync: "peer_mismatch", leader_id: 0, desired_peers: [1, 2, 3], current_peers: [1, 2], last_report_at: "2026-04-23T08:00:00Z" }] },
      leader_missing: { count: 0, items: [] },
      sync_mismatch: { count: 0, items: [] },
    },
    tasks: {
      failed: { count: 0, items: [] },
      retrying: { count: 1, items: [{ slot_id: 9, kind: "rebalance", step: "plan", status: "retrying", source_node: 1, target_node: 2, attempt: 3, next_run_at: null, last_error: "temporary failure" }] },
    },
  },
}

const tasksFixture = { total: 1, items: [{ slot_id: 9, kind: "rebalance", step: "plan", status: "retrying", source_node: 1, target_node: 2, attempt: 3, next_run_at: null, last_error: "temporary failure" }] }

const nodesFixture = {
  generated_at: "2026-04-23T08:00:00Z",
  controller_leader_id: 1,
  total: 2,
  items: [
    { node_id: 1, name: "node-1", addr: "127.0.0.1:7000", status: "alive", last_heartbeat_at: "2026-04-23T08:00:00Z", is_local: true, capacity_weight: 1, controller: { role: "leader", voter: true, leader_id: 1, raft_health: "snapshot_required", first_index: 10, applied_index: 20, snapshot_index: 9 }, slot_stats: { count: 1, leader_count: 1 } },
    { node_id: 2, name: "node-2", addr: "127.0.0.1:7001", status: "alive", last_heartbeat_at: "2026-04-23T08:00:00Z", is_local: false, capacity_weight: 1, controller: { role: "follower", voter: true, leader_id: 1, raft_health: "unknown", first_index: 0, applied_index: 0, snapshot_index: 0 }, slot_stats: { count: 1, leader_count: 0 } },
  ],
}

const channelClusterFixture = {
  total: 4,
  healthy: 1,
  isr_insufficient: 2,
  no_leader: 1,
  avg_replicas: 2,
  avg_isr: 1.5,
  leader_distribution: [{ node_id: 1, count: 3 }, { node_id: 2, count: 1 }],
}

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  getOverviewMock.mockReset()
  getTasksMock.mockReset()
  getNodesMock.mockReset()
  getChannelClusterSummaryMock.mockReset()
  getNodesMock.mockResolvedValue(nodesFixture)
  getChannelClusterSummaryMock.mockResolvedValue(channelClusterFixture)
})

function renderDashboard() {
  return render(
    <I18nProvider>
      <MemoryRouter initialEntries={["/dashboard"]}>
        <DashboardPage />
      </MemoryRouter>
    </I18nProvider>,
  )
}

test("renders verdict, slot ready count, and controller leader", async () => {
  getOverviewMock.mockResolvedValue(overviewFixture)
  getTasksMock.mockResolvedValue(tasksFixture)
  renderDashboard()

  expect(await screen.findByText("Cluster degraded")).toBeInTheDocument()
  expect(screen.getByText(/63\/64/)).toBeInTheDocument()
  expect(screen.getByText(/controller leader 1/i)).toBeInTheDocument()
})

test("renders active incidents with slot and task anomalies", async () => {
  getOverviewMock.mockResolvedValue(overviewFixture)
  getTasksMock.mockResolvedValue(tasksFixture)
  renderDashboard()

  expect(await screen.findByText(/Slot 9 — quorum lost/)).toBeInTheDocument()
  expect(screen.getByText(/rebalance/)).toBeInTheDocument()
  expect(screen.getByText(/temporary failure/)).toBeInTheDocument()
  expect(screen.getByText(/snapshot required/i)).toBeInTheDocument()
})

test("renders healthy empty state when no anomalies", async () => {
  getOverviewMock.mockResolvedValue({
    ...overviewFixture,
    slots: { ...overviewFixture.slots, quorum_lost: 0, peer_mismatch: 0 },
    tasks: { ...overviewFixture.tasks, total: 0, pending: 0, retrying: 0 },
    anomalies: { slots: { quorum_lost: { count: 0, items: [] }, leader_missing: { count: 0, items: [] }, sync_mismatch: { count: 0, items: [] } }, tasks: { failed: { count: 0, items: [] }, retrying: { count: 0, items: [] } } },
  })
  getTasksMock.mockResolvedValue({ total: 0, items: [] })
  getNodesMock.mockResolvedValue({ ...nodesFixture, items: [{ ...nodesFixture.items[0], controller: { ...nodesFixture.items[0].controller, raft_health: "healthy" } }, nodesFixture.items[1]] })
  renderDashboard()

  expect(await screen.findByText("Cluster healthy")).toBeInTheDocument()
  expect(screen.getByText("No active alerts")).toBeInTheDocument()
})

test("renders topology snapshot with node details", async () => {
  getOverviewMock.mockResolvedValue(overviewFixture)
  getTasksMock.mockResolvedValue(tasksFixture)
  renderDashboard()

  expect(await screen.findByText("node-1")).toBeInTheDocument()
  expect(screen.getByText("node-2")).toBeInTheDocument()
  expect(screen.getByText(/first 10 \/ applied 20 \/ snapshot 9/)).toBeInTheDocument()
  expect(screen.getByRole("link", { name: /Open node 1/ })).toHaveAttribute("href", "/controller?node_id=1")
})

test("renders channel health section", async () => {
  getOverviewMock.mockResolvedValue(overviewFixture)
  getTasksMock.mockResolvedValue(tasksFixture)
  renderDashboard()

  expect(await screen.findByText("Channel health")).toBeInTheDocument()
  expect(screen.getByText("Healthy: 1")).toBeInTheDocument()
  expect(screen.getByText("ISR insufficient: 2")).toBeInTheDocument()
  expect(screen.getByText("No leader: 1")).toBeInTheDocument()
  expect(screen.getByRole("link", { name: "Open channel cluster health" })).toHaveAttribute("href", "/cluster/channels?tab=overview")
})

test("refresh triggers new fetches for all four endpoints", async () => {
  getOverviewMock.mockResolvedValue(overviewFixture)
  getTasksMock.mockResolvedValue(tasksFixture)
  const user = userEvent.setup()
  renderDashboard()

  await screen.findByText("Cluster degraded")
  await user.click(screen.getByRole("button", { name: "Refresh" }))

  expect(getOverviewMock).toHaveBeenCalledTimes(2)
  expect(getTasksMock).toHaveBeenCalledTimes(2)
  expect(getNodesMock).toHaveBeenCalledTimes(2)
  expect(getChannelClusterSummaryMock).toHaveBeenCalledTimes(2)
})

test("shows forbidden state when overview returns 403", async () => {
  getOverviewMock.mockRejectedValue(new ManagerApiError(403, "forbidden", "forbidden"))
  getTasksMock.mockResolvedValue(tasksFixture)
  renderDashboard()

  expect(await screen.findByText(/permission/i)).toBeInTheDocument()
})

test("shows unavailable state when overview returns 503", async () => {
  getOverviewMock.mockRejectedValue(new ManagerApiError(503, "service_unavailable", "controller leader unavailable"))
  getTasksMock.mockResolvedValue(tasksFixture)
  renderDashboard()

  expect(await screen.findByText(/currently unavailable/i)).toBeInTheDocument()
})

test("uses Chinese copy when locale is zh-CN", async () => {
  localStorage.setItem("wukongim_manager_locale", "zh-CN")
  getOverviewMock.mockResolvedValue(overviewFixture)
  getTasksMock.mockResolvedValue(tasksFixture)
  renderDashboard()

  expect(await screen.findByText("集群降级")).toBeInTheDocument()
  expect(screen.getByText("运维 Cockpit")).toBeInTheDocument()
  expect(screen.getByText(/控制器 Leader 1/)).toBeInTheDocument()
})
```

- [ ] **Step 2: Run all tests**

Run: `cd web && npx vitest run src/pages/dashboard/ 2>&1 | tail -20`
Expected: all tests PASS.

- [ ] **Step 3: Commit**

```bash
git add web/src/pages/dashboard/page.test.tsx
git commit -m "test(web): rewrite dashboard tests for health-first cockpit"
```

---

## Task 11: Full build verification and cleanup

**Files:**
- Verify: all files in `web/src/pages/dashboard/`

- [ ] **Step 1: Run TypeScript check**

Run: `cd web && npx tsc --noEmit 2>&1 | head -20`
Expected: no errors.

- [ ] **Step 2: Run full test suite**

Run: `cd web && npx vitest run 2>&1 | tail -20`
Expected: all tests PASS.

- [ ] **Step 3: Run build**

Run: `cd web && npx vite build 2>&1 | tail -10`
Expected: build succeeds.

- [ ] **Step 4: Start dev server and verify visually**

Run: `cd web && npx vite --port 5173 &`
Open `http://localhost:5173/dashboard` in a browser. Verify:
- Health Hero shows verdict pill + summary
- 4 sparkline tiles render with "mocked" tag
- Donut renders slot proportions
- Incident list shows items (or healthy empty state)
- Topology rows list nodes with status badges

- [ ] **Step 5: Final commit (if any lint/format fixes needed)**

```bash
git add -A web/src/pages/dashboard/
git commit -m "chore(web): dashboard redesign cleanup"
```
