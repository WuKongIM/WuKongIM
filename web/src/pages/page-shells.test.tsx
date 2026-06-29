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
const getRecentConversationsMock = vi.fn()
const getSlotsMock = vi.fn()
const getNodeOnboardingCandidatesMock = vi.fn()
const getNodeOnboardingJobsMock = vi.fn()
const getNetworkSummaryMock = vi.fn()
const getDashboardMetricsMock = vi.fn()
const getDiagnosticsTraceMock = vi.fn()
const getDiagnosticsMessageMock = vi.fn()
const getDiagnosticsEventsMock = vi.fn()
const getDistributedTasksSummaryMock = vi.fn()
const getDistributedTasksMock = vi.fn()
const getDistributedTaskMock = vi.fn()
const getControllerTasksMock = vi.fn()
const getControllerTaskAuditsMock = vi.fn()
const getControllerTaskAuditEventsMock = vi.fn()
const getUsersMock = vi.fn()
const getBusinessChannelsMock = vi.fn()
const getSystemUsersMock = vi.fn()
const getPermissionsMock = vi.fn()

const dashboardMetricSeries = (latest: number, peak = latest, avg = latest) => ({
  latest,
  peak,
  avg,
  series: [avg, latest],
})

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
    getRecentConversations: (...args: unknown[]) => getRecentConversationsMock(...args),
    getSlots: (...args: unknown[]) => getSlotsMock(...args),
    getNodeOnboardingCandidates: (...args: unknown[]) => getNodeOnboardingCandidatesMock(...args),
    getNodeOnboardingJobs: (...args: unknown[]) => getNodeOnboardingJobsMock(...args),
    getNetworkSummary: (...args: unknown[]) => getNetworkSummaryMock(...args),
    getDashboardMetrics: (...args: unknown[]) => getDashboardMetricsMock(...args),
    getDiagnosticsTrace: (...args: unknown[]) => getDiagnosticsTraceMock(...args),
    getDiagnosticsMessage: (...args: unknown[]) => getDiagnosticsMessageMock(...args),
    getDiagnosticsEvents: (...args: unknown[]) => getDiagnosticsEventsMock(...args),
    getDistributedTasksSummary: (...args: unknown[]) => getDistributedTasksSummaryMock(...args),
    getDistributedTasks: (...args: unknown[]) => getDistributedTasksMock(...args),
    getDistributedTask: (...args: unknown[]) => getDistributedTaskMock(...args),
    getControllerTasks: (...args: unknown[]) => getControllerTasksMock(...args),
    getControllerTaskAudits: (...args: unknown[]) => getControllerTaskAuditsMock(...args),
    getControllerTaskAuditEvents: (...args: unknown[]) => getControllerTaskAuditEventsMock(...args),
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
  getRecentConversationsMock.mockReset()
  getSlotsMock.mockReset()
  getNodeOnboardingCandidatesMock.mockReset()
  getNodeOnboardingJobsMock.mockReset()
  getNetworkSummaryMock.mockReset()
  getDashboardMetricsMock.mockReset()
  getDiagnosticsTraceMock.mockReset()
  getDiagnosticsMessageMock.mockReset()
  getDiagnosticsEventsMock.mockReset()
  getDistributedTasksSummaryMock.mockReset()
  getDistributedTasksMock.mockReset()
  getDistributedTaskMock.mockReset()
  getControllerTasksMock.mockReset()
  getControllerTaskAuditsMock.mockReset()
  getControllerTaskAuditEventsMock.mockReset()
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
  getControllerTasksMock.mockResolvedValue({ total: 0, items: [] })
  getControllerTaskAuditsMock.mockResolvedValue({ total: 0, limit: 200, truncated: false, items: [] })
  getControllerTaskAuditEventsMock.mockResolvedValue({ task: null, events: [], truncated: false })
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
  getChannelRuntimeMetaMock.mockResolvedValue({ items: [], has_more: false })
  getMessagesMock.mockResolvedValue({ items: [], has_more: false })
  getRecentConversationsMock.mockResolvedValue({ uid: "", limit: 50, msg_count: 1, only_unread: false, truncated: false, items: [] })
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
        preferred_leader_id: 2,
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
  getDashboardMetricsMock.mockResolvedValue({
    generated_at: "2026-05-15T08:30:00Z",
    window_seconds: 300,
    step_seconds: 30,
    points: 10,
    metrics: {
      send_per_sec: dashboardMetricSeries(2800),
      deliver_per_sec: dashboardMetricSeries(2700),
      connections: dashboardMetricSeries(18400),
      send_latency_p99_ms: dashboardMetricSeries(31),
      delivery_latency_p99_ms: dashboardMetricSeries(42),
      send_fail_rate_percent: dashboardMetricSeries(0.02),
      delivery_fail_rate_percent: dashboardMetricSeries(0.01),
      active_channels: dashboardMetricSeries(2143),
      retry_queue_depth: dashboardMetricSeries(8),
      fan_out_rate: dashboardMetricSeries(3.4),
    },
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
  ["/cluster/dashboard", "Cluster Dashboard", "Internal Link Trends"],
  ["/business/dashboard", "Business Dashboard", "Business Message Trends"],
  ["/cluster/nodes", "Nodes", "127.0.0.1:7000"],
  ["/cluster/slots", "Slots", "Slot 9"],
  ["/cluster/channels", "Channel Cluster", "No manager data is available for this view yet."],
  ["/cluster/plugins", "Plugins", "Node plugin inventory"],
  ["/cluster/tasks", "Controller Tasks", "Task audit history"],
  ["/cluster/topology", "Topology", "Topology Summary"],
  ["/cluster/diagnostics", "Diagnostics", "Message Diagnostics"],
  ["/business/users", "User Management", "Users"],
  ["/business/channels", "Channel Management", "Business channels"],
  ["/business/messages", "Messages", "Channel ID"],
  ["/business/conversations", "Recent Conversations", "Enter a UID to inspect recent conversations."],
  ["/business/system-users", "System Users", "Persisted system UIDs"],
  ["/business/connections", "Connections", "Session"],
  ["/system/permissions", "Permissions", "Authentication Summary"],
  ["/system/webhooks", "Webhook Configuration", "Coming Soon"],
])("renders %s shell", async (path, title, section) => {
  const router = createMemoryRouter(routes, { initialEntries: [path] })

  render(
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>,
  )

  await screen.findAllByText(section)
  expect(screen.getAllByRole("heading", { name: title }).length).toBeGreaterThan(0)
  expect(screen.queryByText(/workspace/i)).not.toBeInTheDocument()
})

it.each([
  ["/cluster/dashboard", "CLUSTER / DASHBOARD"],
  ["/cluster/nodes", "CLUSTER / NODES"],
  ["/cluster/slots", "CLUSTER / SLOTS"],
  ["/cluster/channels", "CLUSTER / CHANNELS"],
  ["/cluster/plugins", "CLUSTER / PLUGINS"],
  ["/cluster/tasks", "Controller Tasks"],
  ["/cluster/topology", "CLUSTER / TOPOLOGY"],
  ["/cluster/diagnostics", "CLUSTER / DIAGNOSTICS"],
  ["/business/dashboard", "BUSINESS / DASHBOARD"],
  ["/business/users", "BUSINESS / USERS"],
  ["/business/channels", "BUSINESS / CHANNELS"],
  ["/business/messages", "BUSINESS / MESSAGES"],
  ["/business/conversations", "BUSINESS / CONVERSATIONS"],
  ["/business/system-users", "BUSINESS / SYSTEM USERS"],
  ["/business/connections", "BUSINESS / CONNECTIONS"],
  ["/system/permissions", "SYSTEM / PERMISSIONS"],
  ["/system/webhooks", "SYSTEM / WEBHOOKS"],
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
  ["/cluster/dashboard", "集群仪表盘", "内部链路趋势"],
  ["/business/dashboard", "业务仪表盘", "业务消息趋势"],
  ["/cluster/nodes", "节点", "127.0.0.1:7000"],
  ["/cluster/slots", "槽位", "槽位 9"],
  ["/cluster/channels", "频道集群", "当前视图还没有可用的管理面数据。"],
  ["/cluster/plugins", "插件管理", "节点插件清单"],
  ["/cluster/tasks", "Controller 任务", "任务审计历史"],
  ["/cluster/topology", "拓扑", "拓扑摘要"],
  ["/cluster/diagnostics", "诊断", "消息诊断"],
  ["/business/users", "用户管理", "用户"],
  ["/business/channels", "频道管理", "业务频道"],
  ["/business/messages", "消息", "频道 ID"],
  ["/business/conversations", "最近会话", "输入 UID 查看最近会话。"],
  ["/business/system-users", "系统用户", "持久化系统 UID"],
  ["/business/connections", "连接", "会话"],
  ["/system/permissions", "权限管理", "认证摘要"],
  ["/system/webhooks", "Webhook 配置", "即将推出"],
])("renders %s in Chinese", async (path, title, section) => {
  localStorage.setItem("wukongim_manager_locale", "zh-CN")
  const router = createMemoryRouter(routes, { initialEntries: [path] })

  render(
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>,
  )

  await screen.findAllByText(section)
  expect(screen.getAllByRole("heading", { name: title }).length).toBeGreaterThan(0)
})
