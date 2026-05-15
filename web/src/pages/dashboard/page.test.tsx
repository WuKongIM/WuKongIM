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
  expect(screen.getAllByText(/63\/64/).length).toBeGreaterThan(0)
  expect(screen.getByText(/controller leader 1/i)).toBeInTheDocument()
})

test("renders active incidents with slot and task anomalies", async () => {
  getOverviewMock.mockResolvedValue(overviewFixture)
  getTasksMock.mockResolvedValue(tasksFixture)
  renderDashboard()

  expect(await screen.findByText(/Slot 9 — quorum lost/)).toBeInTheDocument()
  expect(screen.getByText(/rebalance/)).toBeInTheDocument()
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
  expect(screen.getAllByText(/first 10 \/ applied 20 \/ snapshot 9/).length).toBeGreaterThan(0)
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
  expect(screen.getByText(/运维 Cockpit/)).toBeInTheDocument()
  expect(screen.getByText(/控制器 Leader 1/)).toBeInTheDocument()
})
