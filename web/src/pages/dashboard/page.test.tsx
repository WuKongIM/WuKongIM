import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeEach, expect, test, vi } from "vitest"

import { I18nProvider } from "@/i18n/provider"
import { resetLocale } from "@/i18n/locale-store"
import { ManagerApiError } from "@/lib/manager-api"
import { DashboardPage } from "@/pages/dashboard/page"

const getOverviewMock = vi.fn()
const getTasksMock = vi.fn()
const getNodesMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getOverview: (...args: unknown[]) => getOverviewMock(...args),
    getTasks: (...args: unknown[]) => getTasksMock(...args),
    getNodes: (...args: unknown[]) => getNodesMock(...args),
  }
})

const overviewFixture = {
  generated_at: "2026-04-23T08:00:00Z",
  cluster: { controller_leader_id: 1 },
  nodes: { total: 3, alive: 3, suspect: 0, dead: 0, draining: 1 },
  slots: {
    total: 64,
    ready: 63,
    quorum_lost: 1,
    leader_missing: 0,
    unreported: 0,
    peer_mismatch: 1,
    epoch_lag: 0,
  },
  tasks: { total: 2, pending: 1, retrying: 1, failed: 0 },
  anomalies: {
    slots: {
      quorum_lost: {
        count: 1,
        items: [{
          slot_id: 9,
          quorum: "quorum_lost",
          sync: "peer_mismatch",
          leader_id: 0,
          desired_peers: [1, 2, 3],
          current_peers: [1, 2],
          last_report_at: "2026-04-23T08:00:00Z",
        }],
      },
      leader_missing: { count: 0, items: [] },
      sync_mismatch: { count: 0, items: [] },
    },
    tasks: {
      failed: { count: 0, items: [] },
      retrying: {
        count: 1,
        items: [{
          slot_id: 9,
          kind: "rebalance",
          step: "plan",
          status: "retrying",
          source_node: 1,
          target_node: 2,
          attempt: 3,
          next_run_at: null,
          last_error: "temporary failure",
        }],
      },
    },
  },
}

const tasksFixture = {
  total: 1,
  items: [{
    slot_id: 9,
    kind: "rebalance",
    step: "plan",
    status: "retrying",
    source_node: 1,
    target_node: 2,
    attempt: 3,
    next_run_at: null,
    last_error: "temporary failure",
  }],
}

const nodesFixture = {
  generated_at: "2026-04-23T08:00:00Z",
  controller_leader_id: 1,
  total: 2,
  items: [
    {
      node_id: 1,
      name: "node-1",
      addr: "127.0.0.1:7000",
      status: "alive",
      last_heartbeat_at: "2026-04-23T08:00:00Z",
      is_local: true,
      capacity_weight: 1,
      controller: {
        role: "leader",
        voter: true,
        leader_id: 1,
        raft_health: "snapshot_required",
        first_index: 10,
        applied_index: 20,
        snapshot_index: 9,
      },
      slot_stats: { count: 1, leader_count: 1 },
    },
    {
      node_id: 2,
      name: "node-2",
      addr: "127.0.0.1:7001",
      status: "alive",
      last_heartbeat_at: "2026-04-23T08:00:00Z",
      is_local: false,
      capacity_weight: 1,
      controller: {
        role: "follower",
        voter: true,
        leader_id: 1,
        raft_health: "unknown",
        first_index: 0,
        applied_index: 0,
        snapshot_index: 0,
      },
      slot_stats: { count: 1, leader_count: 0 },
    },
  ],
}

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  getOverviewMock.mockReset()
  getTasksMock.mockReset()
  getNodesMock.mockReset()
  getNodesMock.mockResolvedValue(nodesFixture)
})

function renderDashboard() {
  return render(
    <I18nProvider>
      <DashboardPage />
    </I18nProvider>,
  )
}

test("renders overview metrics and task queue from manager APIs", async () => {
  getOverviewMock.mockResolvedValue(overviewFixture)
  getTasksMock.mockResolvedValue(tasksFixture)

  renderDashboard()

  expect(await screen.findByText("63")).toBeInTheDocument()
  expect(screen.getByText("Controller leader")).toBeInTheDocument()
  expect(screen.getByText("rebalance")).toBeInTheDocument()
  expect(screen.getByText("temporary failure")).toBeInTheDocument()
  expect(screen.getAllByText(/slot 9/i).length).toBeGreaterThan(0)
})

test("renders controller raft health summary from node inventory", async () => {
  getOverviewMock.mockResolvedValue(overviewFixture)
  getTasksMock.mockResolvedValue(tasksFixture)

  renderDashboard()

  expect(await screen.findByText("Controller Raft Health")).toBeInTheDocument()
  expect(screen.getByText("snapshot required")).toBeInTheDocument()
  expect(screen.getByText("reported 1 / voters 2")).toBeInTheDocument()
  expect(screen.getByText("first 10 / applied 20 / snapshot 9")).toBeInTheDocument()
})

test("refresh triggers a new overview and task fetch", async () => {
  getOverviewMock.mockResolvedValue(overviewFixture)
  getTasksMock.mockResolvedValue(tasksFixture)

  const user = userEvent.setup()
  renderDashboard()

  await screen.findByText("63")
  await user.click(screen.getByRole("button", { name: "Refresh" }))

  expect(getOverviewMock).toHaveBeenCalledTimes(2)
  expect(getTasksMock).toHaveBeenCalledTimes(2)
  expect(getNodesMock).toHaveBeenCalledTimes(2)
})

test("shows a forbidden state when the manager overview request is denied", async () => {
  getOverviewMock.mockRejectedValue(
    new ManagerApiError(403, "forbidden", "forbidden"),
  )
  getTasksMock.mockResolvedValue(tasksFixture)

  renderDashboard()

  expect(await screen.findByText(/permission/i)).toBeInTheDocument()
})

test("shows an unavailable state when the manager overview request is unavailable", async () => {
  getOverviewMock.mockRejectedValue(
    new ManagerApiError(503, "service_unavailable", "controller leader unavailable"),
  )
  getTasksMock.mockResolvedValue(tasksFixture)

  renderDashboard()

  expect(await screen.findByText(/currently unavailable/i)).toBeInTheDocument()
})

test("uses Chinese dashboard copy and locale-aware generated timestamps", async () => {
  localStorage.setItem("wukongim_manager_locale", "zh-CN")
  getOverviewMock.mockResolvedValue(overviewFixture)
  getTasksMock.mockResolvedValue(tasksFixture)

  renderDashboard()

  expect(await screen.findByRole("heading", { name: "仪表盘" })).toBeInTheDocument()
  expect(screen.getByText("操作摘要")).toBeInTheDocument()
  expect(screen.getByText("控制器 Leader：1")).toBeInTheDocument()
  expect(screen.getByText("生成时间：2026/04/23 16:00:00")).toBeInTheDocument()
})
