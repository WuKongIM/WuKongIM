import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { MemoryRouter } from "react-router-dom"
import { beforeEach, expect, test, vi } from "vitest"

import { createAnonymousAuthState, useAuthStore } from "@/auth/auth-store"
import { I18nProvider } from "@/i18n/provider"
import { resetLocale } from "@/i18n/locale-store"
import { ManagerApiError } from "@/lib/manager-api"
import { NodesPage } from "@/pages/nodes/page"

const getNodesMock = vi.fn()
const getNodeMock = vi.fn()
const markNodeDrainingMock = vi.fn()
const resumeNodeMock = vi.fn()
const planNodeScaleInMock = vi.fn()
const startNodeScaleInMock = vi.fn()
const getNodeScaleInStatusMock = vi.fn()
const advanceNodeScaleInMock = vi.fn()
const cancelNodeScaleInMock = vi.fn()
const getControllerLogsMock = vi.fn()
const getControllerRaftStatusMock = vi.fn()
const compactControllerRaftLogOnNodeMock = vi.fn()
const compactControllerRaftLogsMock = vi.fn()
const getNodeOnboardingCandidatesMock = vi.fn()
const getNodeOnboardingJobsMock = vi.fn()
const getNodeOnboardingJobMock = vi.fn()
const createNodeOnboardingPlanMock = vi.fn()
const startNodeOnboardingJobMock = vi.fn()
const retryNodeOnboardingJobMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getNodes: (...args: unknown[]) => getNodesMock(...args),
    getNode: (...args: unknown[]) => getNodeMock(...args),
    markNodeDraining: (...args: unknown[]) => markNodeDrainingMock(...args),
    resumeNode: (...args: unknown[]) => resumeNodeMock(...args),
    planNodeScaleIn: (...args: unknown[]) => planNodeScaleInMock(...args),
    startNodeScaleIn: (...args: unknown[]) => startNodeScaleInMock(...args),
    getNodeScaleInStatus: (...args: unknown[]) => getNodeScaleInStatusMock(...args),
    advanceNodeScaleIn: (...args: unknown[]) => advanceNodeScaleInMock(...args),
    cancelNodeScaleIn: (...args: unknown[]) => cancelNodeScaleInMock(...args),
    getControllerLogs: (...args: unknown[]) => getControllerLogsMock(...args),
    getControllerRaftStatus: (...args: unknown[]) => getControllerRaftStatusMock(...args),
    compactControllerRaftLogOnNode: (...args: unknown[]) => compactControllerRaftLogOnNodeMock(...args),
    compactControllerRaftLogs: (...args: unknown[]) => compactControllerRaftLogsMock(...args),
    getNodeOnboardingCandidates: (...args: unknown[]) => getNodeOnboardingCandidatesMock(...args),
    getNodeOnboardingJobs: (...args: unknown[]) => getNodeOnboardingJobsMock(...args),
    getNodeOnboardingJob: (...args: unknown[]) => getNodeOnboardingJobMock(...args),
    createNodeOnboardingPlan: (...args: unknown[]) => createNodeOnboardingPlanMock(...args),
    startNodeOnboardingJob: (...args: unknown[]) => startNodeOnboardingJobMock(...args),
    retryNodeOnboardingJob: (...args: unknown[]) => retryNodeOnboardingJobMock(...args),
  }
})

const nodeRow = {
  node_id: 1,
  name: "node-1",
  addr: "127.0.0.1:7000",
  status: "alive",
  last_heartbeat_at: "2026-04-23T08:00:00Z",
  is_local: true,
  capacity_weight: 1,
  membership: { role: "data", join_state: "active", schedulable: true },
  health: { status: "alive", last_heartbeat_at: "2026-04-23T08:00:00Z" },
  controller: { role: "leader", voter: true, leader_id: 1 },
  slot_stats: { count: 3, leader_count: 2 },
  slots: {
    replica_count: 3,
    leader_count: 2,
    follower_count: 1,
    quorum_lost_count: 0,
    unreported_count: 0,
  },
  runtime: {
    node_id: 1,
    active_online: 4,
    closing_online: 0,
    total_online: 4,
    gateway_sessions: 5,
    sessions_by_listener: {},
    accepting_new_sessions: true,
    draining: false,
    unknown: false,
  },
  actions: {
    can_drain: true,
    can_resume: false,
    can_scale_in: true,
    can_onboard: false,
  },
}

const drainingNodeRow = {
  ...nodeRow,
  status: "draining",
  health: { ...nodeRow.health, status: "draining" },
  runtime: { ...nodeRow.runtime, accepting_new_sessions: false, draining: true },
  actions: { ...nodeRow.actions, can_drain: false, can_resume: true },
}

const nodeDetail = {
  ...nodeRow,
  slots: {
    hosted_ids: [1, 2, 3],
    leader_ids: [1, 2],
    replica_count: 3,
    leader_count: 2,
    follower_count: 1,
    quorum_lost_count: 0,
    unreported_count: 0,
  },
}

function controllerLogPage(nodeId: number) {
  return {
    node_id: nodeId,
    first_index: 1,
    last_index: 2,
    commit_index: 2,
    applied_index: 2,
    items: [{
      index: 2,
      term: 1,
      type: "normal",
      data_size: 12,
      decode_status: "ok",
      decoded_type: "cluster_config",
      decoded: { command: "cluster_config" },
    }],
  }
}

function controllerRaftStatus(nodeId: number) {
  return {
    node_id: nodeId,
    role: "leader",
    leader_id: nodeId,
    term: 1,
    health: "healthy",
    first_index: 1,
    last_index: 2,
    commit_index: 2,
    applied_index: 2,
    snapshot_index: 0,
    snapshot_term: 0,
    compaction: {
      enabled: true,
      trigger_entries: 100,
      check_interval_ms: 2000,
      last_snapshot_index: 0,
      last_snapshot_at: "",
      last_check_at: "",
      last_error: "",
      last_error_at: "",
      degraded: false,
    },
    restore: {
      last_snapshot_index: 0,
      last_snapshot_term: 0,
      last_restored_at: "",
      last_error: "",
      last_error_at: "",
      failed: false,
    },
    peers: [],
  }
}

const drainingNodeDetail = {
  ...drainingNodeRow,
  slots: {
    hosted_ids: [1, 2, 3],
    leader_ids: [],
    replica_count: 3,
    leader_count: 0,
    follower_count: 3,
    quorum_lost_count: 0,
    unreported_count: 0,
  },
}

const scaleInPlanReport = {
  node_id: 1,
  status: "not_started",
  safe_to_remove: false,
  can_start: true,
  can_advance: false,
  can_cancel: false,
  connection_safety_verified: true,
  blocked_reasons: [],
  checks: {
    target_exists: true,
    target_is_data_node: true,
    target_is_active_or_draining: true,
    target_is_not_controller_voter: true,
    tail_node_mapping_verified: true,
    remaining_data_nodes_enough: true,
    controller_leader_available: true,
    slot_replica_count_known: true,
    no_other_draining_node: true,
    no_active_hashslot_migrations: true,
    no_running_onboarding: true,
    no_active_reconcile_tasks_involving_target: true,
    no_failed_reconcile_tasks: true,
    runtime_views_complete_and_fresh: true,
    all_slots_have_quorum: true,
    target_not_unique_healthy_replica: true,
    channel_inventory_available: true,
    no_active_channel_migrations_involving_target: true,
    no_channel_leaders_on_target: true,
    no_channel_replicas_on_target: true,
  },
  progress: {
    assigned_slot_replicas: 0,
    observed_slot_replicas: 0,
    slot_leaders: 0,
    active_tasks_involving_node: 0,
    active_migrations_involving_node: 0,
    channel_leaders: 3,
    channel_replicas: 5,
    active_channel_migrations_involving_node: 2,
    active_connections: 0,
    closing_connections: 0,
    gateway_sessions: 0,
    active_connections_unknown: false,
    channel_inventory_scanned: true,
    channel_inventory_partial: false,
    channel_inventory_error: "",
  },
  runtime: {
    node_id: 1,
    active_online: 0,
    closing_online: 0,
    total_online: 0,
    gateway_sessions: 0,
    sessions_by_listener: {},
    accepting_new_sessions: false,
    draining: false,
    unknown: false,
  },
  leaders: [],
  next_action: "start",
}

const legacyScaleInReport = {
  ...scaleInPlanReport,
  checks: omitKeys(scaleInPlanReport.checks, [
    "channel_inventory_available",
    "no_active_channel_migrations_involving_target",
    "no_channel_leaders_on_target",
    "no_channel_replicas_on_target",
  ] as const),
  progress: omitKeys(scaleInPlanReport.progress, [
    "channel_leaders",
    "channel_replicas",
    "active_channel_migrations_involving_node",
    "channel_inventory_scanned",
    "channel_inventory_partial",
    "channel_inventory_error",
  ] as const),
}

const partialChannelInventoryReport = {
  ...scaleInPlanReport,
  checks: {
    ...scaleInPlanReport.checks,
    channel_inventory_available: false,
  },
  progress: {
    ...scaleInPlanReport.progress,
    channel_inventory_scanned: true,
    channel_inventory_partial: true,
    channel_inventory_error: "channel runtime unavailable",
  },
}

const scaleInRunningReport = {
  ...scaleInPlanReport,
  status: "migrating_replicas",
  can_start: false,
  can_advance: true,
  can_cancel: true,
  progress: {
    ...scaleInPlanReport.progress,
    assigned_slot_replicas: 4,
    observed_slot_replicas: 4,
    active_tasks_involving_node: 2,
  },
  runtime: {
    ...scaleInPlanReport.runtime,
    draining: true,
  },
  next_action: "wait_reconcile_tasks",
}

const scaleInReadyReport = {
  ...scaleInPlanReport,
  status: "ready_to_remove",
  safe_to_remove: true,
  can_start: false,
  can_advance: false,
  can_cancel: false,
  next_action: "remove_pod",
}

const scaleInBlockedReport = {
  ...scaleInPlanReport,
  status: "blocked",
  can_start: false,
  connection_safety_verified: false,
  blocked_reasons: [{
    code: "target_is_controller_voter",
    message: "controller voter cannot be removed",
    count: 0,
    slot_id: 0,
    node_id: 1,
  }],
  checks: {
    ...scaleInPlanReport.checks,
    target_is_not_controller_voter: false,
  },
  next_action: "",
}

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  getNodesMock.mockReset()
  getNodeMock.mockReset()
  markNodeDrainingMock.mockReset()
  resumeNodeMock.mockReset()
  planNodeScaleInMock.mockReset()
  startNodeScaleInMock.mockReset()
  getNodeScaleInStatusMock.mockReset()
  advanceNodeScaleInMock.mockReset()
  cancelNodeScaleInMock.mockReset()
  getControllerLogsMock.mockReset()
  getControllerRaftStatusMock.mockReset()
  compactControllerRaftLogOnNodeMock.mockReset()
  compactControllerRaftLogsMock.mockReset()
  getNodeOnboardingCandidatesMock.mockReset()
  getNodeOnboardingJobsMock.mockReset()
  getNodeOnboardingJobMock.mockReset()
  createNodeOnboardingPlanMock.mockReset()
  startNodeOnboardingJobMock.mockReset()
  retryNodeOnboardingJobMock.mockReset()
  getNodeOnboardingCandidatesMock.mockResolvedValue({ total: 0, items: [] })
  getNodeOnboardingJobsMock.mockResolvedValue({ items: [], next_cursor: "", has_more: false })
  useAuthStore.setState({
    ...createAnonymousAuthState(),
    isHydrated: true,
    status: "authenticated",
    username: "admin",
    tokenType: "Bearer",
    accessToken: "token-1",
    expiresAt: "2099-04-22T12:00:00Z",
    permissions: [
      { resource: "cluster.node", actions: ["r", "w"] },
      { resource: "cluster.slot", actions: ["r", "w"] },
    ],
  })
})

function renderNodesPage(path = "/cluster/nodes?tab=list") {
  return render(
    <I18nProvider>
      <MemoryRouter initialEntries={[path]}>
        <NodesPage />
      </MemoryRouter>
    </I18nProvider>,
  )
}

function omitKeys<T extends object, K extends keyof T>(value: T, keys: readonly K[]): Omit<T, K> {
  const copy: Partial<T> = { ...value }
  for (const key of keys) {
    delete copy[key]
  }
  return copy as Omit<T, K>
}

function getMetricCard(container: HTMLElement, label: string) {
  const labelElement = within(container).getByText(label)
  const card = labelElement.parentElement
  expect(card).not.toBeNull()
  return card as HTMLElement
}

function expectMetricValue(container: HTMLElement, label: string, value: string) {
  expect(getMetricCard(container, label)).toHaveTextContent(value)
}


test("renders node cluster tabs and defaults to list", async () => {
  getNodesMock.mockResolvedValueOnce({
    generated_at: "2026-04-23T08:00:01Z",
    controller_leader_id: 1,
    total: 1,
    items: [nodeRow],
  })

  renderNodesPage("/cluster/nodes")

  expect(screen.getByRole("tab", { name: "List" })).toHaveAttribute("aria-selected", "true")
  expect(screen.getByRole("tab", { name: "Logs" })).toBeInTheDocument()
  expect(screen.queryByRole("tab", { name: "Overview" })).not.toBeInTheDocument()
  expect(screen.queryByRole("tab", { name: "Unhealthy" })).not.toBeInTheDocument()
  expect(await screen.findByText("127.0.0.1:7000")).toBeInTheDocument()
  expect(screen.queryByText("Node Cluster Overview")).not.toBeInTheDocument()
})

test("renders the node list tab from the tab search param", async () => {
  getNodesMock.mockResolvedValueOnce({
    generated_at: "2026-04-23T08:00:01Z",
    controller_leader_id: 1,
    total: 1,
    items: [nodeRow],
  })

  renderNodesPage("/cluster/nodes?tab=list")

  expect(screen.getByRole("tab", { name: "List" })).toHaveAttribute("aria-selected", "true")
  expect(await screen.findByText("127.0.0.1:7000")).toBeInTheDocument()
})

test("renders node distributed logs from the logs tab", async () => {
  getNodesMock.mockResolvedValue({
    generated_at: "2026-04-23T08:00:01Z",
    controller_leader_id: 1,
    total: 1,
    items: [nodeRow],
  })
  getControllerLogsMock.mockResolvedValue(controllerLogPage(1))
  getControllerRaftStatusMock.mockResolvedValue(controllerRaftStatus(1))

  renderNodesPage("/cluster/nodes?tab=logs")

  expect(screen.getByRole("tab", { name: "Logs" })).toHaveAttribute("aria-selected", "true")
  expect(await screen.findByText("Controller Logs")).toBeInTheDocument()
  expect(await screen.findByText("cluster_config")).toBeInTheDocument()
})

test("node tab clicks update the selected tab", async () => {
  getNodesMock.mockResolvedValue({
    generated_at: "2026-04-23T08:00:01Z",
    controller_leader_id: 1,
    total: 1,
    items: [nodeRow],
  })
  getControllerLogsMock.mockResolvedValue(controllerLogPage(1))
  getControllerRaftStatusMock.mockResolvedValue(controllerRaftStatus(1))

  const user = userEvent.setup()
  renderNodesPage("/cluster/nodes")

  await user.click(screen.getByRole("tab", { name: "Logs" }))

  expect(screen.getByRole("tab", { name: "Logs" })).toHaveAttribute("aria-selected", "true")
})

test("omits distributed log health from the node list and detail", async () => {
  getNodesMock.mockResolvedValueOnce({
    generated_at: "2026-04-23T08:00:01Z",
    controller_leader_id: 1,
    total: 1,
    items: [nodeRow],
  })
  getNodeMock.mockResolvedValueOnce(nodeDetail)

  const user = userEvent.setup()
  renderNodesPage()

  expect(await screen.findByText("127.0.0.1:7000")).toBeInTheDocument()
  expect(screen.queryByText("Distributed Log")).not.toBeInTheDocument()
  expect(screen.queryByText("max lag 7 / apply gap 2")).not.toBeInTheDocument()
  expect(screen.queryByText("2 unhealthy / 1 unavailable")).not.toBeInTheDocument()

  await user.click(screen.getByRole("button", { name: "Inspect node 1" }))

  expect(await screen.findByText("Hosted IDs")).toBeInTheDocument()
  expect(screen.queryByText("Distributed Log Health")).not.toBeInTheDocument()
  expect(screen.queryByText("Slot 9")).not.toBeInTheDocument()
  expect(screen.queryByText("commit 93 / applied 91")).not.toBeInTheDocument()
  expect(screen.queryByText("leader commit 100 / lag 7")).not.toBeInTheDocument()
})

test("renders layered node inventory fields and honors backend action hints", async () => {
  getNodesMock.mockResolvedValueOnce({
    generated_at: "2026-04-23T08:00:01Z",
    controller_leader_id: 1,
    total: 1,
    items: [{
      ...nodeRow,
      actions: { ...nodeRow.actions, can_drain: false },
    }],
  })

  renderNodesPage()

  expect(await screen.findByText("127.0.0.1:7000")).toBeInTheDocument()
  expect(screen.getByText("data")).toBeInTheDocument()
  expect(screen.getByText("active")).toBeInTheDocument()
  expect(screen.getByText("schedulable")).toBeInTheDocument()
  expect(screen.getByText("controller voter")).toBeInTheDocument()
  expect(screen.getByText("replicas 3 / leaders 2 / followers 1")).toBeInTheDocument()
  expect(screen.getByText("sessions 5 / online 4")).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Drain" })).toBeDisabled()
})

test("renders dynamic node health freshness evidence", async () => {
  getNodesMock.mockResolvedValueOnce({
    generated_at: "2026-04-23T08:00:01Z",
    controller_leader_id: 1,
    total: 1,
    items: [{
      ...nodeRow,
      membership: { ...nodeRow.membership, schedulable: false },
      health: {
        status: "alive",
        last_heartbeat_at: "2026-04-23T08:00:00Z",
        fresh: false,
        freshness: "missing",
        runtime_ready: false,
        report_age_ms: 0,
        report_ttl_ms: 30000,
        observed_control_revision: 0,
        observed_slot_revision: 0,
        error_code: "health_report_missing",
      },
    }],
  })

  renderNodesPage()

  expect(await screen.findByText("127.0.0.1:7000")).toBeInTheDocument()
  expect(screen.getByText("missing")).toBeInTheDocument()
  expect(screen.getByText("freshness missing / ready no / age 0 ms / ttl 30000 ms")).toBeInTheDocument()
  expect(screen.getByText("not schedulable")).toBeInTheDocument()
})

test("renders controller raft health summary in the node list and detail", async () => {
  const controller = {
    ...nodeRow.controller,
    raft_health: "snapshot_required",
    first_index: 10,
    applied_index: 20,
    snapshot_index: 9,
  }
  getNodesMock.mockResolvedValueOnce({
    generated_at: "2026-04-23T08:00:01Z",
    controller_leader_id: 1,
    total: 1,
    items: [{ ...nodeRow, controller }],
  })
  getNodeMock.mockResolvedValueOnce({ ...nodeDetail, controller })

  const user = userEvent.setup()
  renderNodesPage()

  expect(await screen.findByText("snapshot required")).toBeInTheDocument()
  expect(screen.getByText("first 10 / applied 20 / snapshot 9")).toBeInTheDocument()
  expect(screen.getByRole("link", { name: "Open Controller Raft for node 1" })).toHaveAttribute(
    "href",
    "/cluster/diagnostics?tab=controller-logs&node_id=1",
  )
  await user.click(screen.getByRole("button", { name: "Inspect node 1" }))

  expect(await screen.findByText("Controller Raft Health")).toBeInTheDocument()
  expect(screen.getAllByText("snapshot required")).not.toHaveLength(0)
  expect(screen.getAllByText("first 10 / applied 20 / snapshot 9")).not.toHaveLength(0)
  expect(screen.getAllByRole("link", { name: "Open Controller Raft for node 1" })).not.toHaveLength(0)
})

test("renders unavailable controller raft summary without fake zero watermarks", async () => {
  const controller = {
    ...nodeRow.controller,
    raft_health: "unknown",
    first_index: 0,
    applied_index: 0,
    snapshot_index: 0,
  }
  getNodesMock.mockResolvedValueOnce({
    generated_at: "2026-04-23T08:00:01Z",
    controller_leader_id: 1,
    total: 1,
    items: [{ ...nodeRow, controller }],
  })
  getNodeMock.mockResolvedValueOnce({ ...nodeDetail, controller })

  const user = userEvent.setup()
  renderNodesPage()

  expect(await screen.findByText("127.0.0.1:7000")).toBeInTheDocument()
  expect(screen.queryByText("unknown")).not.toBeInTheDocument()
  expect(screen.getByRole("link", { name: "Open Controller Raft for node 1" })).toHaveAttribute(
    "href",
    "/cluster/diagnostics?tab=controller-logs&node_id=1",
  )
  await user.click(screen.getByRole("button", { name: "Inspect node 1" }))

  expect(await screen.findByText("Controller Raft Health")).toBeInTheDocument()
  expect(screen.getAllByText("not reported")).not.toHaveLength(0)
  expect(screen.queryByText("first 0 / applied 0 / snapshot 0")).not.toBeInTheDocument()
})

test("requires a complete controller raft watermark before rendering watermark values", async () => {
  const controller = {
    ...nodeRow.controller,
    raft_health: "healthy",
    applied_index: 20,
  }
  getNodesMock.mockResolvedValueOnce({
    generated_at: "2026-04-23T08:00:01Z",
    controller_leader_id: 1,
    total: 1,
    items: [{ ...nodeRow, controller }],
  })
  getNodeMock.mockResolvedValueOnce({ ...nodeDetail, controller })

  const user = userEvent.setup()
  renderNodesPage()

  expect(await screen.findByText("healthy")).toBeInTheDocument()
  expect(screen.queryByText("first 0 / applied 20 / snapshot 0")).not.toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Inspect node 1" }))

  expect(await screen.findByText("Controller Raft Watermark")).toBeInTheDocument()
  expect(screen.getByText("not reported")).toBeInTheDocument()
  expect(screen.queryByText("first 0 / applied 20 / snapshot 0")).not.toBeInTheDocument()
})

test("uses compact node page chrome without duplicate header actions", async () => {
  getNodesMock.mockResolvedValueOnce({
    generated_at: "2026-04-23T08:00:01Z",
    controller_leader_id: 1,
    total: 1,
    items: [nodeRow],
  })

  renderNodesPage()

  expect(await screen.findByText("127.0.0.1:7000")).toBeInTheDocument()
  expect(screen.queryByText("Scope: all nodes")).not.toBeInTheDocument()
  expect(screen.queryByText("Current node placement, role, and lifecycle state from the manager API.")).not.toBeInTheDocument()
  expect(screen.queryByText("Inspect a node for hosted slot details or run lifecycle actions.")).not.toBeInTheDocument()
  expect(screen.queryByRole("button", { name: "Inspect" })).not.toBeInTheDocument()
  expect(screen.getByText("Total: 1")).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Refresh" })).toBeInTheDocument()
})

test("opens scale-in review in a sheet instead of an inline section", async () => {
  getNodesMock.mockResolvedValueOnce({
    generated_at: "2026-04-23T08:00:01Z",
    controller_leader_id: 1,
    total: 1,
    items: [nodeRow],
  })
  planNodeScaleInMock.mockResolvedValueOnce(scaleInPlanReport)

  const user = userEvent.setup()
  renderNodesPage()

  expect(await screen.findByText("127.0.0.1:7000")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Review scale-in for node 1" }))

  const dialog = await screen.findByRole("dialog", { name: "Scale-in Plan" })
  expect(within(dialog).getByText("Assigned replicas")).toBeInTheDocument()
  expectMetricValue(dialog, "Channel leaders", "3")
  expectMetricValue(dialog, "Channel replicas", "5")
  expectMetricValue(dialog, "Active channel migrations", "2")
  expectMetricValue(dialog, "Channel inventory", "ok")
  expect(within(dialog).getByText("Node 1 manager-driven scale-in safety report.")).toBeInTheDocument()
})

test("renders safe scale-in channel fallbacks for legacy reports", async () => {
  getNodesMock.mockResolvedValueOnce({
    generated_at: "2026-04-23T08:00:01Z",
    controller_leader_id: 1,
    total: 1,
    items: [nodeRow],
  })
  planNodeScaleInMock.mockResolvedValueOnce(legacyScaleInReport)

  const user = userEvent.setup()
  renderNodesPage()

  expect(await screen.findByText("127.0.0.1:7000")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Review scale-in for node 1" }))

  const dialog = await screen.findByRole("dialog", { name: "Scale-in Plan" })
  expectMetricValue(dialog, "Channel leaders", "0")
  expectMetricValue(dialog, "Channel replicas", "0")
  expectMetricValue(dialog, "Active channel migrations", "0")
  expectMetricValue(dialog, "Channel inventory", "unknown")
})

test("renders partial channel inventory errors as supporting text", async () => {
  getNodesMock.mockResolvedValueOnce({
    generated_at: "2026-04-23T08:00:01Z",
    controller_leader_id: 1,
    total: 1,
    items: [nodeRow],
  })
  planNodeScaleInMock.mockResolvedValueOnce(partialChannelInventoryReport)

  const user = userEvent.setup()
  renderNodesPage()

  expect(await screen.findByText("127.0.0.1:7000")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Review scale-in for node 1" }))

  const dialog = await screen.findByRole("dialog", { name: "Scale-in Plan" })
  const inventoryCard = getMetricCard(dialog, "Channel inventory")
  expect(inventoryCard).toHaveTextContent("partial")
  expect(inventoryCard).toHaveTextContent("Error: channel runtime unavailable")
})

test("opens node detail and refreshes after draining", async () => {
  getNodesMock.mockResolvedValueOnce({
    generated_at: "2026-04-23T08:00:01Z",
    controller_leader_id: 1,
    total: 1,
    items: [nodeRow],
  })
  getNodeMock.mockResolvedValueOnce(nodeDetail)
  markNodeDrainingMock.mockResolvedValueOnce(drainingNodeDetail)
  getNodesMock.mockResolvedValueOnce({
    generated_at: "2026-04-23T08:00:02Z",
    controller_leader_id: 1,
    total: 1,
    items: [drainingNodeRow],
  })
  getNodeMock.mockResolvedValueOnce(drainingNodeDetail)

  const user = userEvent.setup()
  renderNodesPage()

  expect(await screen.findByText("127.0.0.1:7000")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Inspect node 1" }))

  expect(await screen.findByText("Hosted IDs")).toBeInTheDocument()

  await user.click(screen.getByRole("button", { name: "Drain node" }))
  await user.click(screen.getByRole("button", { name: "Confirm" }))

  expect(markNodeDrainingMock).toHaveBeenCalledWith(1)
  expect(getNodesMock).toHaveBeenCalledTimes(2)
  expect(getNodeMock).toHaveBeenCalledTimes(2)
  expect(await screen.findAllByText("draining")).not.toHaveLength(0)
})

test("refreshes the open detail sheet after resuming a node", async () => {
  getNodesMock.mockResolvedValueOnce({
    generated_at: "2026-04-23T08:00:01Z",
    controller_leader_id: 1,
    total: 1,
    items: [drainingNodeRow],
  })
  getNodeMock.mockResolvedValueOnce(drainingNodeDetail)
  resumeNodeMock.mockResolvedValueOnce(nodeDetail)
  getNodesMock.mockResolvedValueOnce({
    generated_at: "2026-04-23T08:00:02Z",
    controller_leader_id: 1,
    total: 1,
    items: [nodeRow],
  })
  getNodeMock.mockResolvedValueOnce(nodeDetail)

  const user = userEvent.setup()
  renderNodesPage()

  expect(await screen.findByText("127.0.0.1:7000")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Inspect node 1" }))

  expect(await screen.findByText("Leader IDs")).toBeInTheDocument()

  await user.click(screen.getByRole("button", { name: "Resume node" }))
  await user.click(screen.getByRole("button", { name: "Confirm" }))

  expect(resumeNodeMock).toHaveBeenCalledWith(1)
  expect(getNodesMock).toHaveBeenCalledTimes(2)
  expect(getNodeMock).toHaveBeenCalledTimes(2)
  expect(await screen.findAllByText("alive")).not.toHaveLength(0)
})

test("shows a forbidden state when node list access is denied", async () => {
  getNodesMock.mockRejectedValueOnce(new ManagerApiError(403, "forbidden", "forbidden"))

  renderNodesPage()

  expect(await screen.findByText(/permission/i)).toBeInTheDocument()
})

test("shows an unavailable state when the manager node list is unavailable", async () => {
  getNodesMock.mockRejectedValueOnce(
    new ManagerApiError(503, "service_unavailable", "controller leader unavailable"),
  )

  renderNodesPage()

  expect(await screen.findByText(/currently unavailable/i)).toBeInTheDocument()
})

test("reviews a scale-in plan and starts scale-in after confirmation", async () => {
  getNodesMock.mockResolvedValueOnce({
    generated_at: "2026-04-23T08:00:01Z",
    controller_leader_id: 1,
    total: 1,
    items: [nodeRow],
  })
  planNodeScaleInMock.mockResolvedValueOnce(scaleInPlanReport)
  startNodeScaleInMock.mockResolvedValueOnce(scaleInRunningReport)
  getNodesMock.mockResolvedValueOnce({
    generated_at: "2026-04-23T08:00:02Z",
    controller_leader_id: 1,
    total: 1,
    items: [drainingNodeRow],
  })

  const user = userEvent.setup()
  renderNodesPage()

  expect(await screen.findByText("127.0.0.1:7000")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Review scale-in for node 1" }))

  expect(planNodeScaleInMock).toHaveBeenCalledWith(1, {
    confirmStatefulSetTail: true,
    expectedTailNodeId: 1,
  })
  expect(await screen.findByText("Scale-in Plan")).toBeInTheDocument()
  expect(screen.getByText("Assigned replicas")).toBeInTheDocument()

  await user.click(screen.getByRole("button", { name: "Start scale-in" }))
  expect(screen.getByText("Confirm that node 1 is the StatefulSet tail pod before starting scale-in.")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Confirm" }))

  expect(startNodeScaleInMock).toHaveBeenCalledWith(1, {
    confirmStatefulSetTail: true,
    expectedTailNodeId: 1,
  })
  expect(getNodesMock).toHaveBeenCalledTimes(2)
  expect(await screen.findAllByText("migrating replicas")).not.toHaveLength(0)
})

test("uses a blocked scale-in report returned with a 409 error", async () => {
  getNodesMock.mockResolvedValueOnce({
    generated_at: "2026-04-23T08:00:01Z",
    controller_leader_id: 1,
    total: 1,
    items: [nodeRow],
  })
  planNodeScaleInMock.mockRejectedValueOnce(
    new ManagerApiError(409, "scale_in_blocked", "scale-in blocked", scaleInBlockedReport),
  )

  const user = userEvent.setup()
  renderNodesPage()

  expect(await screen.findByText("127.0.0.1:7000")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Review scale-in for node 1" }))

  expect(await screen.findByText("controller voter cannot be removed")).toBeInTheDocument()
  expect(screen.getAllByText("blocked")).not.toHaveLength(0)
})

test("advances an active scale-in report", async () => {
  getNodesMock.mockResolvedValueOnce({
    generated_at: "2026-04-23T08:00:01Z",
    controller_leader_id: 1,
    total: 1,
    items: [nodeRow],
  })
  planNodeScaleInMock.mockResolvedValueOnce(scaleInRunningReport)
  advanceNodeScaleInMock.mockResolvedValueOnce(scaleInReadyReport)

  const user = userEvent.setup()
  renderNodesPage()

  expect(await screen.findByText("127.0.0.1:7000")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Review scale-in for node 1" }))
  await user.click(await screen.findByRole("button", { name: "Advance scale-in" }))

  expect(advanceNodeScaleInMock).toHaveBeenCalledWith(1, {
    maxLeaderTransfers: 1,
    maxChannelMigrations: 1,
  })
  expect(await screen.findByText("Safe to remove: yes")).toBeInTheDocument()
})

test("cancels an active scale-in report", async () => {
  getNodesMock.mockResolvedValueOnce({
    generated_at: "2026-04-23T08:00:01Z",
    controller_leader_id: 1,
    total: 1,
    items: [nodeRow],
  })
  planNodeScaleInMock.mockResolvedValueOnce(scaleInRunningReport)
  cancelNodeScaleInMock.mockResolvedValueOnce(scaleInPlanReport)
  getNodesMock.mockResolvedValueOnce({
    generated_at: "2026-04-23T08:00:02Z",
    controller_leader_id: 1,
    total: 1,
    items: [nodeRow],
  })

  const user = userEvent.setup()
  renderNodesPage()

  expect(await screen.findByText("127.0.0.1:7000")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Review scale-in for node 1" }))

  await user.click(await screen.findByRole("button", { name: "Cancel scale-in" }))
  await user.click(screen.getByRole("button", { name: "Confirm" }))

  expect(cancelNodeScaleInMock).toHaveBeenCalledWith(1)
  expect(getNodesMock).toHaveBeenCalledTimes(2)
})

test("opens lifecycle sheets from the nodes page entry points", async () => {
  getNodesMock.mockResolvedValueOnce({
    generated_at: "2026-04-23T08:00:01Z",
    controller_leader_id: 1,
    total: 1,
    items: [nodeRow],
  })
  const user = userEvent.setup()

  renderNodesPage()

  expect(await screen.findByText("127.0.0.1:7000")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Add node" }))

  let dialog = screen.getByRole("dialog", { name: "Add node" })
  expect(within(dialog).getByLabelText("Node ID")).toBeInTheDocument()

  await user.click(within(dialog).getByRole("button", { name: "Close" }))
  await user.click(screen.getByRole("button", { name: "Open lifecycle for node 1" }))

  dialog = screen.getByRole("dialog", { name: "Node lifecycle" })
  expect(within(dialog).getByText("Node 1")).toBeInTheDocument()
  expect(within(dialog).getByText("127.0.0.1:7000")).toBeInTheDocument()
})
