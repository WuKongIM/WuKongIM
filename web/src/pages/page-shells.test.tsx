import { render, screen } from "@testing-library/react"
import { RouterProvider, createMemoryRouter } from "react-router-dom"
import { beforeEach, vi } from "vitest"

import { AppProviders } from "@/app/providers"
import { routes } from "@/app/router"
import { useAuthStore } from "@/auth/auth-store"
import { resetLocale } from "@/i18n/locale-store"

const getOverviewMock = vi.fn()
const getTasksMock = vi.fn()
const getNodesMock = vi.fn()
const getChannelClusterSummaryMock = vi.fn()
const getChannelRuntimeMetaMock = vi.fn()
const getConnectionsMock = vi.fn()
const getControllerLogsMock = vi.fn()
const getControllerRaftStatusMock = vi.fn()
const getSlotLogsMock = vi.fn()
const getMessagesMock = vi.fn()
const getSlotsMock = vi.fn()
const getNodeOnboardingCandidatesMock = vi.fn()
const getNodeOnboardingJobsMock = vi.fn()
const getNetworkSummaryMock = vi.fn()
const getDiagnosticsTraceMock = vi.fn()
const getDiagnosticsMessageMock = vi.fn()
const getDiagnosticsEventsMock = vi.fn()
const getDistributedTasksSummaryMock = vi.fn()
const getDistributedTasksMock = vi.fn()
const getDistributedTaskMock = vi.fn()
const getUsersMock = vi.fn()
const getBusinessChannelsMock = vi.fn()
const getSystemUsersMock = vi.fn()
const getPermissionsMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getOverview: (...args: unknown[]) => getOverviewMock(...args),
    getTasks: (...args: unknown[]) => getTasksMock(...args),
    getNodes: (...args: unknown[]) => getNodesMock(...args),
    getChannelClusterSummary: (...args: unknown[]) => getChannelClusterSummaryMock(...args),
    getChannelRuntimeMeta: (...args: unknown[]) => getChannelRuntimeMetaMock(...args),
    getConnections: (...args: unknown[]) => getConnectionsMock(...args),
    getControllerLogs: (...args: unknown[]) => getControllerLogsMock(...args),
    getControllerRaftStatus: (...args: unknown[]) => getControllerRaftStatusMock(...args),
    getSlotLogs: (...args: unknown[]) => getSlotLogsMock(...args),
    getMessages: (...args: unknown[]) => getMessagesMock(...args),
    getSlots: (...args: unknown[]) => getSlotsMock(...args),
    getNodeOnboardingCandidates: (...args: unknown[]) => getNodeOnboardingCandidatesMock(...args),
    getNodeOnboardingJobs: (...args: unknown[]) => getNodeOnboardingJobsMock(...args),
    getNetworkSummary: (...args: unknown[]) => getNetworkSummaryMock(...args),
    getDiagnosticsTrace: (...args: unknown[]) => getDiagnosticsTraceMock(...args),
    getDiagnosticsMessage: (...args: unknown[]) => getDiagnosticsMessageMock(...args),
    getDiagnosticsEvents: (...args: unknown[]) => getDiagnosticsEventsMock(...args),
    getDistributedTasksSummary: (...args: unknown[]) => getDistributedTasksSummaryMock(...args),
    getDistributedTasks: (...args: unknown[]) => getDistributedTasksMock(...args),
    getDistributedTask: (...args: unknown[]) => getDistributedTaskMock(...args),
    getUsers: (...args: unknown[]) => getUsersMock(...args),
    getBusinessChannels: (...args: unknown[]) => getBusinessChannelsMock(...args),
    getSystemUsers: (...args: unknown[]) => getSystemUsersMock(...args),
    getPermissions: (...args: unknown[]) => getPermissionsMock(...args),
  }
})

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  getOverviewMock.mockReset()
  getTasksMock.mockReset()
  getNodesMock.mockReset()
  getChannelClusterSummaryMock.mockReset()
  getChannelRuntimeMetaMock.mockReset()
  getConnectionsMock.mockReset()
  getControllerLogsMock.mockReset()
  getControllerRaftStatusMock.mockReset()
  getSlotLogsMock.mockReset()
  getMessagesMock.mockReset()
  getSlotsMock.mockReset()
  getNodeOnboardingCandidatesMock.mockReset()
  getNodeOnboardingJobsMock.mockReset()
  getNetworkSummaryMock.mockReset()
  getDiagnosticsTraceMock.mockReset()
  getDiagnosticsMessageMock.mockReset()
  getDiagnosticsEventsMock.mockReset()
  getDistributedTasksSummaryMock.mockReset()
  getDistributedTasksMock.mockReset()
  getDistributedTaskMock.mockReset()
  getUsersMock.mockReset()
  getBusinessChannelsMock.mockReset()
  getSystemUsersMock.mockReset()
  getPermissionsMock.mockReset()

  getOverviewMock.mockResolvedValue({
    generated_at: "2026-04-23T08:00:00Z",
    cluster: { controller_leader_id: 1 },
    nodes: { total: 1, alive: 1, suspect: 0, dead: 0, draining: 0 },
    slots: {
      total: 1,
      ready: 1,
      quorum_lost: 0,
      leader_missing: 0,
      unreported: 0,
      peer_mismatch: 0,
      epoch_lag: 0,
    },
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
  })
  getTasksMock.mockResolvedValue({ total: 0, items: [] })
  getDistributedTasksSummaryMock.mockResolvedValue({
    total: 0,
    by_status: { pending: 0, running: 0, retrying: 0, blocked: 0, failed: 0, completed: 0, cancelled: 0, unknown: 0 },
    by_domain: { slot_reconcile: 0, node_onboarding: 0, node_scale_in: 0, channel_migration: 0 },
    partial: false,
    warnings: [],
  })
  getDistributedTasksMock.mockResolvedValue({ total: 0, items: [], next_cursor: "", has_more: false, partial: false, warnings: [] })
  getDistributedTaskMock.mockResolvedValue({ task: null, detail: { domain: "slot_reconcile", raw_status: "" } })
  getUsersMock.mockResolvedValue({ items: [], has_more: false })
  getBusinessChannelsMock.mockResolvedValue({ items: [], has_more: false })
  getSystemUsersMock.mockResolvedValue({ items: [], total: 0 })
  getPermissionsMock.mockResolvedValue({ auth_enabled: true, current_user: "admin", users: [], resources: [] })
  getChannelClusterSummaryMock.mockResolvedValue({
    total: 1,
    healthy: 1,
    isr_insufficient: 0,
    no_leader: 0,
    avg_replicas: 1,
    avg_isr: 1,
    leader_distribution: [{ node_id: 1, count: 1 }],
  })
  getNodesMock.mockResolvedValue({
    total: 1,
    items: [{
      node_id: 1,
      addr: "127.0.0.1:7000",
      status: "alive",
      last_heartbeat_at: "2026-04-23T08:00:00Z",
      is_local: true,
      capacity_weight: 1,
      controller: { role: "leader" },
      slot_stats: { count: 1, leader_count: 1 },
    }],
  })
  getChannelRuntimeMetaMock.mockResolvedValue({
    items: [{
      channel_id: "alpha",
      channel_type: 1,
      slot_id: 9,
      channel_epoch: 11,
      leader_epoch: 5,
      leader: 2,
      replicas: [1, 2, 3],
      isr: [1, 2],
      min_isr: 2,
      status: "active",
    }],
    has_more: false,
  })
  getMessagesMock.mockResolvedValue({ items: [], has_more: false })
  getControllerLogsMock.mockResolvedValue({
    node_id: 1,
    first_index: 1,
    last_index: 4,
    commit_index: 4,
    applied_index: 3,
    items: [{ index: 4, term: 2, type: "normal", data_size: 12, decode_status: "ok", decoded_type: "add_slot", decoded: { command: "add_slot", new_slot_id: 9 } }],
  })
  getControllerRaftStatusMock.mockResolvedValue({
    node_id: 1,
    role: "leader",
    leader_id: 1,
    term: 2,
    health: "healthy",
    first_index: 1,
    last_index: 4,
    commit_index: 4,
    applied_index: 3,
    snapshot_index: 0,
    snapshot_term: 0,
    compaction: {
      enabled: true,
      trigger_entries: 100,
      check_interval_ms: 1000,
      last_snapshot_index: 0,
      last_snapshot_at: "0001-01-01T00:00:00Z",
      last_check_at: "0001-01-01T00:00:00Z",
      last_error: "",
      last_error_at: "0001-01-01T00:00:00Z",
      degraded: false,
    },
    restore: {
      last_snapshot_index: 0,
      last_snapshot_term: 0,
      last_restored_at: "0001-01-01T00:00:00Z",
      last_error: "",
      last_error_at: "0001-01-01T00:00:00Z",
      failed: false,
    },
    peers: [],
  })
  getSlotLogsMock.mockResolvedValue({
    node_id: 1,
    slot_id: 9,
    first_index: 1,
    last_index: 4,
    commit_index: 4,
    applied_index: 3,
    items: [{ index: 4, term: 2, type: "normal", data_size: 12, decode_status: "ok", decoded_type: "slot_config", decoded: { command: "slot_config", slot_id: 9 } }],
  })
  getConnectionsMock.mockResolvedValue({
    total: 1,
    items: [{
      node_id: 1,
      session_id: 101,
      uid: "u1",
      device_id: "device-a",
      device_flag: "app",
      device_level: "master",
      slot_id: 9,
      state: "active",
      listener: "tcp",
      connected_at: "2026-04-23T08:00:00Z",
      remote_addr: "10.0.0.1:5000",
      local_addr: "127.0.0.1:7000",
    }],
  })
  getNodeOnboardingCandidatesMock.mockResolvedValue({
    total: 1,
    items: [{
      node_id: 4,
      name: "node-4",
      addr: "127.0.0.1:7004",
      role: "data",
      join_state: "active",
      status: "alive",
      slot_count: 0,
      leader_count: 0,
      recommended: true,
    }],
  })
  getNodeOnboardingJobsMock.mockResolvedValue({ items: [], next_cursor: "", has_more: false })
  getSlotsMock.mockResolvedValue({
    total: 1,
    items: [{
      slot_id: 9,
      state: { quorum: "ready", sync: "in_sync" },
      assignment: { desired_peers: [1, 2, 3], config_epoch: 7, balance_version: 4 },
      runtime: {
        current_peers: [1, 2, 3],
        leader_id: 2,
        healthy_voters: 3,
        has_quorum: true,
        observed_config_epoch: 7,
        last_report_at: "2026-04-23T08:00:00Z",
      },
    }],
  })
  getNetworkSummaryMock.mockResolvedValue({
    generated_at: "2026-04-23T08:00:00Z",
    scope: { view: "local_node", local_node_id: 1, controller_leader_id: 1 },
    source_status: { local_collector: "ok", controller_context: "ok", runtime_views: "ok", errors: {} },
    headline: {
      remote_peers: 0,
      alive_nodes: 1,
      suspect_nodes: 0,
      dead_nodes: 0,
      draining_nodes: 0,
      pool_active: 0,
      pool_idle: 0,
      rpc_inflight: 0,
      dial_errors_1m: 0,
      queue_full_1m: 0,
      timeouts_1m: 0,
      stale_observations: 0,
    },
    traffic: {
      scope: "local_total_by_msg_type",
      tx_bytes_1m: 0,
      rx_bytes_1m: 0,
      tx_bps: 0,
      rx_bps: 0,
      peer_breakdown_available: false,
      by_message_type: [],
    },
    peers: [],
    services: [],
    channel_replication: {
      pool: { active: 0, idle: 0 },
      services: [],
      long_poll: { lane_count: 0, max_wait_ms: 0, max_bytes: 0, max_channels: 0 },
      long_poll_timeouts_1m: 0,
      data_plane_rpc_timeout_ms: 0,
    },
    discovery: {
      listen_addr: "",
      advertise_addr: "",
      seeds: [],
      static_nodes: [],
      pool_size: 0,
      data_plane_pool_size: 0,
      dial_timeout_ms: 0,
      controller_observation_interval_ms: 0,
    },
    events: [],
  })

  useAuthStore.setState({
    status: "authenticated",
    isHydrated: true,
    username: "admin",
    tokenType: "Bearer",
    accessToken: "token-1",
    expiresAt: "2099-04-22T12:00:00Z",
    permissions: [],
  })
})

it.each([
  ["/dashboard", "Dashboard", "Topology snapshot"],
  ["/monitor", "Live Monitor", "Coming Soon"],
  ["/cluster/nodes", "Nodes", "Address"],
  ["/cluster/slots", "Slots", "Slot"],
  ["/cluster/channels", "Channel Cluster", "Channel Cluster Overview"],
  ["/cluster/tasks", "Distributed Tasks", "Task queue"],
  ["/cluster/topology", "Topology", "Topology Summary"],
  ["/cluster/diagnostics", "Diagnostics", "Message Diagnostics"],
  ["/business/users", "User Management", "Users"],
  ["/business/channels", "Channel Management", "Business channels"],
  ["/business/messages", "Messages", "Channel ID"],
  ["/business/system-users", "System Users", "Persisted system UIDs"],
  ["/system/permissions", "Permissions", "Authentication Summary"],
  ["/system/webhooks", "Webhook Configuration", "Coming Soon"],
  ["/system/connections", "Connections", "Session"],
])("renders %s shell", async (path, title, section) => {
  const router = createMemoryRouter(routes, { initialEntries: [path] })

  render(
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>,
  )

  await screen.findAllByText(section)
  expect(screen.getByRole("heading", { name: title })).toBeInTheDocument()
  expect(screen.queryByText(/workspace/i)).not.toBeInTheDocument()
})

test("dashboard shows monochrome workbench sections", async () => {
  const router = createMemoryRouter(routes, { initialEntries: ["/dashboard"] })

  render(
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>,
  )

  await screen.findByText("Topology snapshot")
  expect(screen.getByRole("heading", { name: "Dashboard" })).toBeInTheDocument()
  expect(screen.getAllByText(/Active incidents/).length).toBeGreaterThan(0)
  expect(screen.getAllByText(/Slot & channel health/).length).toBeGreaterThan(0)
  expect(screen.queryByText("Pin board")).not.toBeInTheDocument()
})

it.each([
  ["/cluster/nodes", "CLUSTER / NODES"],
  ["/cluster/slots", "Slot"],
  ["/cluster/channels", "CLUSTER / CHANNELS"],
  ["/cluster/tasks", "Task queue"],
  ["/cluster/topology", "CLUSTER / TOPOLOGY"],
  ["/cluster/diagnostics", "CLUSTER / DIAGNOSTICS"],
  ["/business/users", "BUSINESS / USERS"],
  ["/business/channels", "BUSINESS / CHANNELS"],
  ["/business/messages", "BUSINESS / MESSAGES"],
  ["/business/system-users", "BUSINESS / SYSTEM USERS"],
  ["/system/permissions", "SYSTEM / PERMISSIONS"],
  ["/system/webhooks", "SYSTEM / WEBHOOKS"],
  ["/system/connections", "SYSTEM / CONNECTIONS"],
])("renders the redesigned path label for %s", async (path, label) => {
  const router = createMemoryRouter(routes, { initialEntries: [path] })

  render(
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>,
  )

  expect(await screen.findByText(label)).toBeInTheDocument()
})

it.each([
  ["/dashboard", "仪表盘", "拓扑快照"],
  ["/monitor", "实时监控", "即将推出"],
  ["/cluster/nodes", "节点", "地址"],
  ["/cluster/slots", "槽位", "槽位"],
  ["/cluster/channels", "频道集群", "频道集群总览"],
  ["/cluster/tasks", "分布式任务", "任务队列"],
  ["/cluster/topology", "拓扑", "拓扑摘要"],
  ["/cluster/diagnostics", "诊断", "消息诊断"],
  ["/business/users", "用户管理", "用户"],
  ["/business/channels", "频道管理", "业务频道"],
  ["/business/messages", "消息", "频道 ID"],
  ["/business/system-users", "系统用户", "持久化系统 UID"],
  ["/system/permissions", "权限管理", "认证摘要"],
  ["/system/webhooks", "Webhook 配置", "即将推出"],
  ["/system/connections", "连接", "会话"],
])("renders %s in Chinese", async (path, title, section) => {
  localStorage.setItem("wukongim_manager_locale", "zh-CN")
  const router = createMemoryRouter(routes, { initialEntries: [path] })

  render(
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>,
  )

  await screen.findAllByText(section)
  expect(screen.getByRole("heading", { name: title })).toBeInTheDocument()
})
