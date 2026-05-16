import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { MemoryRouter } from "react-router-dom"
import { beforeEach, expect, test, vi } from "vitest"

import { I18nProvider } from "@/i18n/provider"
import { resetLocale } from "@/i18n/locale-store"
import { ManagerApiError } from "@/lib/manager-api"
import { ClusterDashboardPage } from "./page"

const getOverviewMock = vi.fn()
const getNodesMock = vi.fn()
const getTasksMock = vi.fn()
const getChannelClusterSummaryMock = vi.fn()
const getNetworkSummaryMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getOverview: (...args: unknown[]) => getOverviewMock(...args),
    getNodes: (...args: unknown[]) => getNodesMock(...args),
    getTasks: (...args: unknown[]) => getTasksMock(...args),
    getChannelClusterSummary: (...args: unknown[]) => getChannelClusterSummaryMock(...args),
    getNetworkSummary: (...args: unknown[]) => getNetworkSummaryMock(...args),
  }
})

const overviewFixture = {
  generated_at: "2026-05-15T08:00:00Z",
  cluster: { controller_leader_id: 1 },
  nodes: { total: 3, alive: 3, suspect: 0, dead: 0, draining: 1 },
  slots: { total: 64, ready: 63, quorum_lost: 1, leader_missing: 0, unreported: 0, peer_mismatch: 1, epoch_lag: 0 },
  tasks: { total: 2, pending: 1, retrying: 1, failed: 0 },
  anomalies: {
    slots: {
      quorum_lost: { count: 1, items: [{ slot_id: 9, quorum: "quorum_lost", sync: "peer_mismatch", leader_id: 0, desired_peers: [1, 2, 3], current_peers: [1, 2], last_report_at: "2026-05-15T08:00:00Z" }] },
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
  generated_at: "2026-05-15T08:00:00Z",
  controller_leader_id: 1,
  total: 2,
  items: [
    { node_id: 1, name: "node-1", addr: "127.0.0.1:7000", status: "alive", last_heartbeat_at: "2026-05-15T08:00:00Z", is_local: true, capacity_weight: 1, controller: { role: "leader", voter: true, leader_id: 1, raft_health: "snapshot_required", first_index: 10, applied_index: 20, snapshot_index: 9 }, slot_stats: { count: 1, leader_count: 1 } },
    { node_id: 2, name: "node-2", addr: "127.0.0.1:7001", status: "alive", last_heartbeat_at: "2026-05-15T08:00:00Z", is_local: false, capacity_weight: 1, controller: { role: "follower", voter: true, leader_id: 1, raft_health: "unknown", first_index: 0, applied_index: 0, snapshot_index: 0 }, slot_stats: { count: 1, leader_count: 0 } },
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

const networkFixture = {
  generated_at: "2026-05-15T08:00:00Z",
  scope: { view: "local_node", local_node_id: 1, controller_leader_id: 1 },
  source_status: { local_collector: "ok", controller_context: "ok", runtime_views: "ok", errors: {} },
  headline: { remote_peers: 1, alive_nodes: 2, suspect_nodes: 0, dead_nodes: 0, draining_nodes: 0, pool_active: 2, pool_idle: 3, rpc_inflight: 3, dial_errors_1m: 0, queue_full_1m: 0, timeouts_1m: 1, stale_observations: 0 },
  traffic: { scope: "local_total_by_msg_type", tx_bytes_1m: 1000, rx_bytes_1m: 2000, tx_bps: 100, rx_bps: 200, peer_breakdown_available: true, by_message_type: [] },
  peers: [{ node_id: 2, name: "node-2", addr: "127.0.0.1:7001", health: "alive", last_heartbeat_at: "2026-05-15T08:00:00Z", pools: { cluster: { active: 1, idle: 1 }, data_plane: { active: 1, idle: 1 } }, rpc: { inflight: 2, calls_1m: 100, p95_ms: 37, success_rate: 0.98 }, errors: { dial_error_1m: 0, queue_full_1m: 1, timeout_1m: 1, remote_error_1m: 0 } }],
  services: [{ service_id: 1, service: "raft.append", group: "controller", target_node: 2, inflight: 3, calls_1m: 100, success_1m: 90, expected_timeout_1m: 8, timeout_1m: 2, queue_full_1m: 0, remote_error_1m: 0, other_error_1m: 0, p50_ms: 8, p95_ms: 42, p99_ms: 80, last_seen_at: "2026-05-15T08:00:00Z" }],
  channel_replication: { pool: { active: 1, idle: 1 }, services: [], long_poll: { lane_count: 2, max_wait_ms: 30000, max_bytes: 1048576, max_channels: 256 }, long_poll_timeouts_1m: 4, data_plane_rpc_timeout_ms: 5000 },
  discovery: { listen_addr: "127.0.0.1:7000", advertise_addr: "127.0.0.1:7000", seeds: [], static_nodes: [], pool_size: 2, data_plane_pool_size: 2, dial_timeout_ms: 1000, controller_observation_interval_ms: 1000 },
  history: { window_seconds: 300, step_seconds: 60, traffic: [{ at: "2026-05-15T08:00:00Z", tx_bytes: 1000, rx_bytes: 2000 }], rpc: [{ at: "2026-05-15T08:00:00Z", calls: 100, success: 90, errors: 2, expected_timeouts: 8 }], errors: [] },
  events: [{ at: "2026-05-15T08:00:10Z", severity: "warning", kind: "rpc_timeout", target_node: 2, service: "raft.append", message: "timeout spike" }],
}

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  getOverviewMock.mockReset()
  getNodesMock.mockReset()
  getTasksMock.mockReset()
  getChannelClusterSummaryMock.mockReset()
  getNetworkSummaryMock.mockReset()
})

function mockSuccessfulClusterDashboard() {
  getOverviewMock.mockResolvedValue(overviewFixture)
  getNodesMock.mockResolvedValue(nodesFixture)
  getTasksMock.mockResolvedValue(tasksFixture)
  getChannelClusterSummaryMock.mockResolvedValue(channelClusterFixture)
  getNetworkSummaryMock.mockResolvedValue(networkFixture)
}

function renderClusterDashboard() {
  return render(
    <I18nProvider>
      <MemoryRouter initialEntries={["/cluster/dashboard"]}>
        <ClusterDashboardPage />
      </MemoryRouter>
    </I18nProvider>,
  )
}

test("renders cluster health and internal link metrics", async () => {
  mockSuccessfulClusterDashboard()

  renderClusterDashboard()

  expect(await screen.findByRole("heading", { name: "Cluster Dashboard" })).toBeInTheDocument()
  expect(screen.getByText("Cluster degraded")).toBeInTheDocument()
  expect(screen.getByText("Internal message/s")).toBeInTheDocument()
  expect(screen.getByText("RPC error rate")).toBeInTheDocument()
  expect(screen.getByText("Internal Link Trends")).toBeInTheDocument()
  expect(screen.getByText("Slot & channel replication")).toBeInTheDocument()
  expect(screen.getByText("Topology watermarks")).toBeInTheDocument()
})

test("keeps health sections visible when network summary fails", async () => {
  getOverviewMock.mockResolvedValue(overviewFixture)
  getNodesMock.mockResolvedValue(nodesFixture)
  getTasksMock.mockResolvedValue(tasksFixture)
  getChannelClusterSummaryMock.mockResolvedValue(channelClusterFixture)
  getNetworkSummaryMock.mockRejectedValue(new ManagerApiError(503, "unavailable", "network unavailable"))

  renderClusterDashboard()

  expect(await screen.findByText("Cluster degraded")).toBeInTheDocument()
  expect(screen.getByText("Network metrics unavailable")).toBeInTheDocument()
  expect(screen.getAllByText("sample").length).toBeGreaterThan(0)
})

test("fatal overview failure renders ResourceState", async () => {
  getOverviewMock.mockRejectedValue(new ManagerApiError(403, "forbidden", "forbidden"))
  getNodesMock.mockResolvedValue(nodesFixture)
  getTasksMock.mockResolvedValue(tasksFixture)
  getChannelClusterSummaryMock.mockResolvedValue(channelClusterFixture)
  getNetworkSummaryMock.mockResolvedValue(networkFixture)

  renderClusterDashboard()

  expect(await screen.findByText(/permission/i)).toBeInTheDocument()
})

test("refresh refetches all cluster endpoints", async () => {
  const user = userEvent.setup()
  mockSuccessfulClusterDashboard()
  renderClusterDashboard()

  await screen.findByText("Cluster degraded")
  await user.click(screen.getByRole("button", { name: "Refresh" }))

  await waitFor(() => expect(getOverviewMock).toHaveBeenCalledTimes(2))
  expect(getNetworkSummaryMock).toHaveBeenCalledTimes(2)
})
