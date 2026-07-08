import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeEach, expect, test, vi } from "vitest"

import { createAnonymousAuthState, useAuthStore } from "@/auth/auth-store"
import { resetLocale } from "@/i18n/locale-store"
import { I18nProvider } from "@/i18n/provider"
import { ManagerApiError } from "@/lib/manager-api"
import { TopologyPage } from "@/pages/topology/page"

const getOverviewMock = vi.fn()
const getNodesMock = vi.fn()
const getSlotsMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getOverview: (...args: unknown[]) => getOverviewMock(...args),
    getNodes: (...args: unknown[]) => getNodesMock(...args),
    getSlots: (...args: unknown[]) => getSlotsMock(...args),
  }
})

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  getOverviewMock.mockReset()
  getNodesMock.mockReset()
  getSlotsMock.mockReset()
  useAuthStore.setState({
    ...createAnonymousAuthState(),
    isHydrated: true,
    status: "authenticated",
    username: "admin",
    tokenType: "Bearer",
    accessToken: "token-1",
    expiresAt: "2099-04-22T12:00:00Z",
    permissions: [
      { resource: "cluster.overview", actions: ["r"] },
      { resource: "cluster.node", actions: ["r"] },
      { resource: "cluster.slot", actions: ["r"] },
    ],
  })
})

function renderTopologyPage() {
  return render(
    <I18nProvider>
      <TopologyPage />
    </I18nProvider>,
  )
}

function mockSuccessfulTopology() {
  getOverviewMock.mockResolvedValueOnce({
    generated_at: "2026-05-14T01:00:00Z",
    cluster: { controller_leader_id: 1 },
    nodes: { total: 2, alive: 2, suspect: 0, dead: 0, draining: 0 },
    slots: {
      total: 3,
      ready: 2,
      quorum_lost: 1,
      leader_missing: 0,
      unreported: 0,
      peer_mismatch: 1,
      epoch_lag: 0,
    },
    tasks: { total: 0, pending: 0, retrying: 0, failed: 0 },
    anomalies: {
      slots: {
        quorum_lost: {
          count: 1,
          items: [{
            slot_id: 2,
            quorum: "lost",
            sync: "peer_mismatch",
            leader_id: 0,
            desired_peers: [1, 2],
            current_peers: [1],
            last_report_at: "2026-05-14T01:00:00Z",
          }],
        },
        leader_missing: { count: 0, items: [] },
        sync_mismatch: { count: 1, items: [] },
      },
      tasks: {
        failed: { count: 0, items: [] },
        retrying: { count: 0, items: [] },
      },
    },
  })
  getNodesMock.mockResolvedValueOnce({
    generated_at: "2026-05-14T01:00:00Z",
    controller_leader_id: 1,
    total: 2,
    items: [
      {
        node_id: 1,
        name: "wk-node-1",
        addr: "127.0.0.1:11110",
        status: "alive",
        last_heartbeat_at: "2026-05-14T01:00:00Z",
        is_local: true,
        capacity_weight: 1,
        membership: { role: "controller_voter", join_state: "active", schedulable: true },
        health: { status: "alive", last_heartbeat_at: "2026-05-14T01:00:00Z" },
        controller: { role: "leader", voter: true, leader_id: 1, raft_health: "healthy" },
        slot_stats: { count: 2, leader_count: 1 },
        slots: { replica_count: 2, leader_count: 1, follower_count: 1, quorum_lost_count: 0, unreported_count: 0 },
        runtime: {
          node_id: 1,
          active_online: 8,
          closing_online: 0,
          total_online: 8,
          gateway_sessions: 5,
          sessions_by_listener: {},
          accepting_new_sessions: true,
          draining: false,
          unknown: false,
        },
      },
      {
        node_id: 2,
        name: "wk-node-2",
        addr: "127.0.0.1:11111",
        status: "alive",
        last_heartbeat_at: "2026-05-14T01:00:00Z",
        is_local: false,
        capacity_weight: 1,
        membership: { role: "data", join_state: "active", schedulable: true },
        health: { status: "alive", last_heartbeat_at: "2026-05-14T01:00:00Z" },
        controller: { role: "follower", voter: false, leader_id: 1, raft_health: "healthy" },
        slot_stats: { count: 2, leader_count: 1 },
        slots: { replica_count: 2, leader_count: 1, follower_count: 1, quorum_lost_count: 1, unreported_count: 0 },
      },
    ],
  })
  getSlotsMock.mockResolvedValueOnce({
    total: 3,
    items: [
      {
        slot_id: 1,
        hash_slots: { count: 2, items: [0, 1] },
        state: { quorum: "ready", sync: "synced" },
        assignment: { desired_peers: [1, 2], config_epoch: 3, balance_version: 1 },
        runtime: {
          current_peers: [1, 2],
          current_voters: [1, 2],
          preferred_leader_id: 1,
          healthy_voters: 2,
          has_quorum: true,
          observed_config_epoch: 3,
          last_report_at: "2026-05-14T01:00:00Z",
        },
      },
      {
        slot_id: 2,
        hash_slots: { count: 1, items: [2] },
        state: { quorum: "lost", sync: "peer_mismatch" },
        assignment: { desired_peers: [1], config_epoch: 4, balance_version: 1 },
        runtime: {
          current_peers: [1],
          current_voters: [1],
          preferred_leader_id: 0,
          healthy_voters: 1,
          has_quorum: false,
          observed_config_epoch: 4,
          last_report_at: "2026-05-14T01:00:00Z",
        },
      },
      {
        slot_id: 3,
        hash_slots: null,
        state: { quorum: "ready", sync: "synced" },
        assignment: { desired_peers: [2], config_epoch: 5, balance_version: 1 },
        runtime: {
          current_peers: [2],
          current_voters: [2],
          preferred_leader_id: 2,
          healthy_voters: 1,
          has_quorum: true,
          observed_config_epoch: 5,
          last_report_at: "2026-05-14T01:00:00Z",
        },
      },
    ],
  })
}

test("renders topology summary, nodes, and slot matrix", async () => {
  mockSuccessfulTopology()

  renderTopologyPage()

  expect(await screen.findByRole("heading", { name: "Topology" })).toBeInTheDocument()
  expect(screen.getByText("Topology Summary")).toBeInTheDocument()
  expect(screen.getByText("Controller leader: 1")).toBeInTheDocument()
  expect(screen.getByText("Nodes: 2")).toBeInTheDocument()
  expect(screen.getByText("Slots: 3")).toBeInTheDocument()
  expect(screen.getByText("Slot anomalies: 2")).toBeInTheDocument()

  const summaryStrip = screen.getByTestId("topology-summary-strip")
  expect(summaryStrip).toHaveClass("grid", "overflow-hidden", "rounded-md", "border", "border-border", "bg-card")
  expect(summaryStrip.querySelectorAll("[data-topology-summary-cell]")).toHaveLength(4)
  expect(summaryStrip.querySelector("[data-topology-summary-cell]")).not.toHaveClass("rounded-lg")

  expect(screen.getByText("wk-node-1")).toBeInTheDocument()
  expect(screen.getByText("wk-node-2")).toBeInTheDocument()

  const nodeSurface = screen.getByText("wk-node-1").closest("[data-topology-surface='nodes']")
  expect(nodeSurface).toHaveClass("grid", "gap-3", "md:grid-cols-2", "xl:grid-cols-3")

  const nodeCard = screen.getByText("wk-node-1").closest("article")
  expect(nodeCard).toHaveClass("rounded-md", "border", "border-border", "bg-background")

  expect(screen.getByText("Slot Placement")).toBeInTheDocument()

  const slotTable = screen.getByRole("table", { name: "Slot Placement" })
  const slotSurface = slotTable.closest("[data-topology-surface='slot-placement']")
  expect(slotSurface).toHaveClass("overflow-x-auto", "rounded-md", "border", "border-border")
  expect(slotTable).toHaveClass("w-full", "border-collapse", "text-sm")
  expect(within(slotTable).getByText("Slot 1")).toBeInTheDocument()

  expect(screen.getByText("Preferred leader 1")).toBeInTheDocument()
  expect(screen.getByText("quorum lost")).toBeInTheDocument()
})

test("filters slot matrix by selected node", async () => {
  const user = userEvent.setup()
  mockSuccessfulTopology()

  renderTopologyPage()

  await screen.findByText("Slot 1")
  await user.selectOptions(screen.getByLabelText("Node filter"), "2")

  expect(screen.getByText("Showing 2 of 3 slots")).toBeInTheDocument()
  expect(screen.getByText("Slot 1")).toBeInTheDocument()
  expect(screen.getByText("Slot 3")).toBeInTheDocument()
  expect(screen.queryByText("Slot 2")).not.toBeInTheDocument()
})

test("maps forbidden and unavailable errors", async () => {
  getOverviewMock.mockRejectedValueOnce(new ManagerApiError(403, "forbidden", "forbidden"))
  getNodesMock.mockResolvedValueOnce({ generated_at: "", controller_leader_id: 0, total: 0, items: [] })
  getSlotsMock.mockResolvedValueOnce({ total: 0, items: [] })

  const { unmount } = renderTopologyPage()

  expect(await screen.findByText("You do not have permission to view this manager resource.")).toBeInTheDocument()
  unmount()

  getOverviewMock.mockRejectedValueOnce(new ManagerApiError(503, "service_unavailable", "unavailable"))
  getNodesMock.mockResolvedValueOnce({ generated_at: "", controller_leader_id: 0, total: 0, items: [] })
  getSlotsMock.mockResolvedValueOnce({ total: 0, items: [] })

  renderTopologyPage()

  expect(await screen.findByText("The manager service is currently unavailable.")).toBeInTheDocument()
})

test("renders empty node and slot states", async () => {
  getOverviewMock.mockResolvedValueOnce({
    generated_at: "2026-05-14T01:00:00Z",
    cluster: { controller_leader_id: 0 },
    nodes: { total: 0, alive: 0, suspect: 0, dead: 0, draining: 0 },
    slots: { total: 0, ready: 0, quorum_lost: 0, leader_missing: 0, unreported: 0, peer_mismatch: 0, epoch_lag: 0 },
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
  getNodesMock.mockResolvedValueOnce({ generated_at: "2026-05-14T01:00:00Z", controller_leader_id: 0, total: 0, items: [] })
  getSlotsMock.mockResolvedValueOnce({ total: 0, items: [] })

  renderTopologyPage()

  expect(await screen.findByText("No cluster nodes are available for this topology view.")).toBeInTheDocument()
  expect(screen.getByText("No slots are available for this topology view.")).toBeInTheDocument()
})
