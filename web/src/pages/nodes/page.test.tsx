import { render, screen, waitFor, within } from "@testing-library/react"
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
const activateNodeMock = vi.fn()
const startNodeOnboardingMock = vi.fn()
const getNodeOnboardingStatusMock = vi.fn()
const startNodeScaleInMock = vi.fn()
const setNodeScaleInDrainMock = vi.fn()
const advanceNodeScaleInMock = vi.fn()
const advanceNodeSlotMoveOutMock = vi.fn()
const removeNodeAfterScaleInMock = vi.fn()
const getControllerLogsMock = vi.fn()
const getControllerRaftStatusMock = vi.fn()
const compactControllerRaftLogOnNodeMock = vi.fn()
const compactControllerRaftLogsMock = vi.fn()
const promoteControllerVoterMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getNodes: (...args: unknown[]) => getNodesMock(...args),
    getNode: (...args: unknown[]) => getNodeMock(...args),
    activateNode: (...args: unknown[]) => activateNodeMock(...args),
    startNodeOnboarding: (...args: unknown[]) => startNodeOnboardingMock(...args),
    getNodeOnboardingStatus: (...args: unknown[]) => getNodeOnboardingStatusMock(...args),
    startNodeScaleIn: (...args: unknown[]) => startNodeScaleInMock(...args),
    setNodeScaleInDrain: (...args: unknown[]) => setNodeScaleInDrainMock(...args),
    advanceNodeScaleIn: (...args: unknown[]) => advanceNodeScaleInMock(...args),
    advanceNodeSlotMoveOut: (...args: unknown[]) => advanceNodeSlotMoveOutMock(...args),
    removeNodeAfterScaleIn: (...args: unknown[]) => removeNodeAfterScaleInMock(...args),
    getControllerLogs: (...args: unknown[]) => getControllerLogsMock(...args),
    getControllerRaftStatus: (...args: unknown[]) => getControllerRaftStatusMock(...args),
    compactControllerRaftLogOnNode: (...args: unknown[]) => compactControllerRaftLogOnNodeMock(...args),
    compactControllerRaftLogs: (...args: unknown[]) => compactControllerRaftLogsMock(...args),
    promoteControllerVoter: (...args: unknown[]) => promoteControllerVoterMock(...args),
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
    can_scale_in: false,
    can_onboard: true,
    can_move_slots_in: true,
    can_move_slots_out: true,
    can_promote_controller_voter: false,
  },
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
    voters: [nodeId],
    learners: [],
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

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  getNodesMock.mockReset()
  getNodeMock.mockReset()
  activateNodeMock.mockReset()
  startNodeOnboardingMock.mockReset()
  getNodeOnboardingStatusMock.mockReset()
  getNodeOnboardingStatusMock.mockResolvedValue(onboardingStatus(0))
  startNodeScaleInMock.mockReset()
  setNodeScaleInDrainMock.mockReset()
  advanceNodeScaleInMock.mockReset()
  advanceNodeSlotMoveOutMock.mockReset()
  removeNodeAfterScaleInMock.mockReset()
  getControllerLogsMock.mockReset()
  getControllerRaftStatusMock.mockReset()
  compactControllerRaftLogOnNodeMock.mockReset()
  compactControllerRaftLogsMock.mockReset()
  promoteControllerVoterMock.mockReset()
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
      { resource: "cluster.controller", actions: ["r", "w"] },
    ],
  })
})

function onboardingStatus(totalActive: number) {
  return {
    generated_at: "2026-07-01T08:00:00Z",
    state_revision: 42,
    target_node_id: 1,
    summary: { total_active: totalActive, pending: totalActive, running: 0, failed: 0 },
    tasks: [],
  }
}

function renderNodesPage(path = "/cluster/nodes?tab=list") {
  return render(
    <I18nProvider>
      <MemoryRouter initialEntries={[path]}>
        <NodesPage />
      </MemoryRouter>
    </I18nProvider>,
  )
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

test("renders layered node inventory fields and slot move row actions", async () => {
  getNodesMock.mockResolvedValueOnce({
    generated_at: "2026-04-23T08:00:01Z",
    controller_leader_id: 1,
    total: 1,
    items: [{
      ...nodeRow,
      actions: { ...nodeRow.actions, can_drain: false },
    }],
  })
  const user = userEvent.setup()

  renderNodesPage()

  expect(await screen.findByText("127.0.0.1:7000")).toBeInTheDocument()
  expect(screen.getByTestId("nodes-summary-strip")).toBeInTheDocument()
  expect(screen.getByRole("table", { name: /nodes/i })).toHaveClass("w-full")
  expect(screen.getByText("data")).toBeInTheDocument()
  expect(screen.getByText("active")).toBeInTheDocument()
  expect(screen.getByText("schedulable")).toBeInTheDocument()
  expect(screen.getByText("controller voter")).toBeInTheDocument()
  expect(screen.getByText("replicas 3 / leaders 2 / followers 1")).toBeInTheDocument()
  expect(screen.getByText("sessions 5 / online 4")).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Inspect node 1" })).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Move slots in for node 1" })).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Move slots out for node 1" })).toBeInTheDocument()
  expect(screen.queryByRole("button", { name: "More actions for node 1" })).not.toBeInTheDocument()
  expect(screen.queryByRole("button", { name: "Start scale-in for node 1" })).not.toBeInTheDocument()
  expect(screen.queryByRole("button", { name: "Set node 1 as Controller voter" })).not.toBeInTheDocument()
  expect(screen.queryByRole("button", { name: "Drain" })).not.toBeInTheDocument()
  expect(screen.queryByRole("button", { name: "Resume" })).not.toBeInTheDocument()
  expect(screen.queryByRole("button", { name: "Review scale-in for node 1" })).not.toBeInTheDocument()
})

test("renders lifecycle actions directly in node rows by state", async () => {
  const joiningNode = {
    ...nodeRow,
    node_id: 4,
    name: "node-4",
    addr: "127.0.0.1:7004",
    membership: { role: "data", join_state: "joining", schedulable: false },
    actions: { ...nodeRow.actions, can_onboard: false, can_move_slots_in: false, can_move_slots_out: false, can_scale_in: false },
  }
  const onboardNode = {
    ...nodeRow,
    node_id: 5,
    name: "node-5",
    addr: "127.0.0.1:7005",
    controller: { role: "follower", voter: false, leader_id: 1 },
    actions: { ...nodeRow.actions, can_onboard: true, can_move_slots_in: true, can_move_slots_out: true, can_scale_in: true },
  }
  const leavingNode = {
    ...nodeRow,
    node_id: 6,
    name: "node-6",
    addr: "127.0.0.1:7006",
    membership: { role: "data", join_state: "leaving", schedulable: false },
    controller: { role: "follower", voter: false, leader_id: 1 },
    actions: { ...nodeRow.actions, can_onboard: false, can_move_slots_in: false, can_move_slots_out: false, can_scale_in: true },
  }
  getNodesMock.mockResolvedValueOnce({
    generated_at: "2026-07-01T08:00:00Z",
    controller_leader_id: 1,
    total: 3,
    items: [joiningNode, onboardNode, leavingNode],
  })

  renderNodesPage()

  expect(await screen.findByText("127.0.0.1:7004")).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Activate node 4" })).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Move slots in for node 5" })).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Move slots out for node 5" })).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Advance scale-in for node 6" })).toBeInTheDocument()
  expect(screen.queryByRole("button", { name: "Start scale-in for node 4" })).not.toBeInTheDocument()
  expect(screen.queryByRole("button", { name: "Open lifecycle for node 4" })).not.toBeInTheDocument()
})

test("shows Controller voter promotion only for eligible non-voter node rows", async () => {
  const eligibleNode = {
    ...nodeRow,
    node_id: 4,
    name: "node-4",
    addr: "127.0.0.1:7004",
    is_local: false,
    controller: { role: "follower", voter: false, leader_id: 1 },
    actions: { ...nodeRow.actions, can_promote_controller_voter: true },
  }
  const alreadyVoter = {
    ...nodeRow,
    node_id: 5,
    name: "node-5",
    addr: "127.0.0.1:7005",
    is_local: false,
    controller: { role: "follower", voter: true, leader_id: 1 },
    actions: { ...nodeRow.actions, can_promote_controller_voter: true },
  }
  const missingHint = {
    ...nodeRow,
    node_id: 6,
    name: "node-6",
    addr: "127.0.0.1:7006",
    is_local: false,
    controller: { role: "learner", voter: false, leader_id: 1 },
    actions: { ...nodeRow.actions, can_promote_controller_voter: false },
  }
  getNodesMock.mockResolvedValueOnce({
    generated_at: "2026-07-01T08:00:00Z",
    controller_leader_id: 1,
    total: 3,
    items: [eligibleNode, alreadyVoter, missingHint],
  })

  renderNodesPage()

  expect(await screen.findByText("127.0.0.1:7004")).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Set node 4 as Controller voter" })).toBeEnabled()
  expect(screen.queryByRole("button", { name: "Set node 5 as Controller voter" })).not.toBeInTheDocument()
  expect(screen.queryByRole("button", { name: "Set node 6 as Controller voter" })).not.toBeInTheDocument()
})

test("disables Controller voter promotion without Controller write permission", async () => {
  const eligibleNode = {
    ...nodeRow,
    node_id: 4,
    name: "node-4",
    addr: "127.0.0.1:7004",
    is_local: false,
    controller: { role: "follower", voter: false, leader_id: 1 },
    actions: { ...nodeRow.actions, can_promote_controller_voter: true },
  }
  getNodesMock.mockResolvedValueOnce({
    generated_at: "2026-07-01T08:00:00Z",
    controller_leader_id: 1,
    total: 1,
    items: [eligibleNode],
  })
  useAuthStore.setState((state) => ({
    ...state,
    permissions: [
      { resource: "cluster.node", actions: ["r", "w"] },
      { resource: "cluster.slot", actions: ["r", "w"] },
    ],
  }))

  const user = userEvent.setup()
  renderNodesPage()

  const promoteButton = await screen.findByRole("button", { name: "Set node 4 as Controller voter" })
  expect(promoteButton).toBeDisabled()
  await user.click(promoteButton)

  expect(screen.queryByText("Set node as Controller voter?")).not.toBeInTheDocument()
  expect(promoteControllerVoterMock).not.toHaveBeenCalled()
})

test("confirms Controller voter promotion with no expected revision and refreshes the node list", async () => {
  const eligibleNode = {
    ...nodeRow,
    node_id: 4,
    name: "node-4",
    addr: "127.0.0.1:7004",
    is_local: false,
    controller: { role: "follower", voter: false, leader_id: 1 },
    actions: { ...nodeRow.actions, can_promote_controller_voter: true },
  }
  const promotedNode = {
    ...eligibleNode,
    controller: { ...eligibleNode.controller, voter: true },
    actions: { ...eligibleNode.actions, can_promote_controller_voter: false },
  }
  getNodesMock
    .mockResolvedValueOnce({
      generated_at: "2026-07-01T08:00:00Z",
      controller_leader_id: 1,
      total: 1,
      items: [eligibleNode],
    })
    .mockResolvedValueOnce({
      generated_at: "2026-07-01T08:00:01Z",
      controller_leader_id: 1,
      total: 1,
      items: [promotedNode],
    })
  promoteControllerVoterMock.mockResolvedValueOnce({
    changed: true,
    node_id: 4,
    state_revision: 10,
    previous_voters: [1],
    next_voters: [1, 4],
  })

  const user = userEvent.setup()
  renderNodesPage()

  await user.click(await screen.findByRole("button", { name: "Set node 4 as Controller voter" }))

  expect(await screen.findByText("Set node as Controller voter?")).toBeInTheDocument()
  expect(screen.getByText(/This changes Controller Raft quorum membership/)).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Set as Controller voter" }))

  expect(promoteControllerVoterMock).toHaveBeenCalledWith(4)
  await waitFor(() => {
    expect(screen.queryByText("Set node as Controller voter?")).not.toBeInTheDocument()
  })
  expect(getNodesMock).toHaveBeenCalledTimes(2)
  expect(screen.getByText("controller voter")).toBeInTheDocument()
  expect(screen.queryByRole("button", { name: "Set node 4 as Controller voter" })).not.toBeInTheDocument()
})

test("keeps Controller voter promotion dialog open when the backend reports a stale target", async () => {
  const eligibleNode = {
    ...nodeRow,
    node_id: 4,
    name: "node-4",
    addr: "127.0.0.1:7004",
    is_local: false,
    controller: { role: "follower", voter: false, leader_id: 1 },
    actions: { ...nodeRow.actions, can_promote_controller_voter: true },
  }
  getNodesMock.mockResolvedValueOnce({
    generated_at: "2026-07-01T08:00:00Z",
    controller_leader_id: 1,
    total: 1,
    items: [eligibleNode],
  })
  promoteControllerVoterMock.mockRejectedValueOnce(
    new ManagerApiError(409, "target_health_stale", "target_health_stale"),
  )

  const user = userEvent.setup()
  renderNodesPage()

  await user.click(await screen.findByRole("button", { name: "Set node 4 as Controller voter" }))
  await user.click(screen.getByRole("button", { name: "Set as Controller voter" }))

  expect(promoteControllerVoterMock).toHaveBeenCalledWith(4)
  expect(await screen.findByText("target_health_stale")).toBeInTheDocument()
  expect(screen.getByText("Set node as Controller voter?")).toBeInTheDocument()
  expect(getNodesMock).toHaveBeenCalledTimes(1)
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

test("opens the add-node lifecycle sheet from the toolbar only", async () => {
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
  expect(screen.queryByRole("button", { name: "Open lifecycle for node 1" })).not.toBeInTheDocument()
})

test("activates a joining node directly from the row and refreshes the list", async () => {
  const joiningNode = {
    ...nodeRow,
    node_id: 4,
    name: "node-4",
    addr: "127.0.0.1:7004",
    membership: { role: "data", join_state: "joining", schedulable: false },
    actions: { ...nodeRow.actions, can_onboard: false, can_move_slots_in: false, can_move_slots_out: false, can_scale_in: false },
  }
  const activeNode = {
    ...joiningNode,
    membership: { role: "data", join_state: "active", schedulable: true },
    actions: { ...nodeRow.actions, can_onboard: true, can_move_slots_in: true, can_move_slots_out: true, can_scale_in: true },
  }
  getNodesMock
    .mockResolvedValueOnce({
      generated_at: "2026-07-01T08:00:00Z",
      controller_leader_id: 1,
      total: 1,
      items: [joiningNode],
    })
    .mockResolvedValueOnce({
      generated_at: "2026-07-01T08:00:01Z",
      controller_leader_id: 1,
      total: 1,
      items: [activeNode],
    })
  activateNodeMock.mockResolvedValueOnce({
    changed: true,
    node_id: 4,
    addr: "127.0.0.1:7004",
    join_state: "active",
    revision: 49,
  })

  const user = userEvent.setup()
  renderNodesPage()

  expect(await screen.findByText("127.0.0.1:7004")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Activate node 4" }))

  expect(activateNodeMock).toHaveBeenCalledWith(4)
  await waitFor(() => {
    expect(screen.getByRole("button", { name: "Move slots in for node 4" })).toBeInTheDocument()
  })
})

test("starts onboarding from the row with a user-specified move count", async () => {
  const onboardNode = {
    ...nodeRow,
    actions: { ...nodeRow.actions, can_onboard: true, can_move_slots_in: true, can_move_slots_out: true, can_scale_in: true },
  }
  getNodesMock
    .mockResolvedValueOnce({
      generated_at: "2026-07-01T08:00:00Z",
      controller_leader_id: 1,
      total: 1,
      items: [onboardNode],
    })
    .mockResolvedValueOnce({
      generated_at: "2026-07-01T08:00:01Z",
      controller_leader_id: 1,
      total: 1,
      items: [onboardNode],
    })
  startNodeOnboardingMock.mockResolvedValueOnce({
    generated_at: "2026-07-01T08:00:01Z",
    state_revision: 49,
    target_node_id: 1,
    max_slot_moves: 1,
    created: 1,
    results: [],
    skipped: [],
  })

  const user = userEvent.setup()
  renderNodesPage()

  expect(await screen.findByText("127.0.0.1:7000")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Move slots in for node 1" }))

  expect(startNodeOnboardingMock).not.toHaveBeenCalled()
  expect(await screen.findByText("Confirm move slots in")).toBeInTheDocument()
  await user.clear(screen.getByLabelText("Slot move count"))
  await user.type(screen.getByLabelText("Slot move count"), "3")
  await user.click(screen.getByRole("button", { name: "Confirm" }))

  expect(startNodeOnboardingMock).toHaveBeenCalledWith(1, { maxSlotMoves: 3 })
  await waitFor(() => {
    expect(getNodesMock).toHaveBeenCalledTimes(2)
  })
})

test("moves slots out from a controller voter row with a user-specified move count", async () => {
  getNodesMock
    .mockResolvedValueOnce({
      generated_at: "2026-07-01T08:00:00Z",
      controller_leader_id: 1,
      total: 1,
      items: [nodeRow],
    })
    .mockResolvedValueOnce({
      generated_at: "2026-07-01T08:00:01Z",
      controller_leader_id: 1,
      total: 1,
      items: [nodeRow],
    })
  advanceNodeSlotMoveOutMock.mockResolvedValueOnce({
    generated_at: "2026-07-01T08:00:01Z",
    state_revision: 49,
    node_id: 1,
    created: 1,
    skipped: 0,
    candidates: [],
  })

  const user = userEvent.setup()
  renderNodesPage()

  expect(await screen.findByText("127.0.0.1:7000")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Move slots out for node 1" }))

  expect(advanceNodeSlotMoveOutMock).not.toHaveBeenCalled()
  expect(await screen.findByText("Confirm move slots out")).toBeInTheDocument()
  await user.clear(screen.getByLabelText("Slot move count"))
  await user.type(screen.getByLabelText("Slot move count"), "2")
  await user.click(screen.getByRole("button", { name: "Confirm" }))

  expect(advanceNodeSlotMoveOutMock).toHaveBeenCalledWith(1, { maxSlotMoves: 2 })
  await waitFor(() => {
    expect(getNodesMock).toHaveBeenCalledTimes(2)
  })
})

test("disables slot move-in while onboarding tasks are active", async () => {
  const onboardNode = {
    ...nodeRow,
    actions: { ...nodeRow.actions, can_onboard: true, can_move_slots_in: true, can_move_slots_out: true, can_scale_in: true },
  }
  getNodeOnboardingStatusMock.mockResolvedValueOnce(onboardingStatus(1))
  getNodesMock.mockResolvedValueOnce({
    generated_at: "2026-07-01T08:00:00Z",
    controller_leader_id: 1,
    total: 1,
    items: [onboardNode],
  })

  const user = userEvent.setup()
  renderNodesPage()

  const moveButton = await screen.findByRole("button", { name: "Move slots in for node 1" })
  await waitFor(() => {
    expect(moveButton).toBeDisabled()
  })
  await user.click(moveButton)

  expect(screen.queryByText("Confirm move slots in")).not.toBeInTheDocument()
  expect(startNodeOnboardingMock).not.toHaveBeenCalled()
})

test("ignores stale onboarding status responses after a newer node refresh", async () => {
  const onboardNode = {
    ...nodeRow,
    actions: { ...nodeRow.actions, can_onboard: true, can_move_slots_in: true, can_move_slots_out: true, can_scale_in: true },
  }
  let resolveFirstStatus: ((value: ReturnType<typeof onboardingStatus>) => void) | undefined
  getNodeOnboardingStatusMock
    .mockImplementationOnce(() => new Promise((resolve) => {
      resolveFirstStatus = resolve
    }))
    .mockResolvedValueOnce(onboardingStatus(0))
  getNodesMock
    .mockResolvedValueOnce({
      generated_at: "2026-07-01T08:00:00Z",
      controller_leader_id: 1,
      total: 1,
      items: [onboardNode],
    })
    .mockResolvedValueOnce({
      generated_at: "2026-07-01T08:00:01Z",
      controller_leader_id: 1,
      total: 1,
      items: [onboardNode],
    })

  const user = userEvent.setup()
  renderNodesPage()

  expect(await screen.findByText("127.0.0.1:7000")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Refresh" }))

  const moveButton = await screen.findByRole("button", { name: "Move slots in for node 1" })
  await waitFor(() => {
    expect(moveButton).toBeEnabled()
  })
  resolveFirstStatus?.(onboardingStatus(1))

  await waitFor(() => {
    expect(screen.getByRole("button", { name: "Move slots in for node 1" })).toBeEnabled()
  })
  expect(screen.queryByText("Migrating")).not.toBeInTheDocument()
})

test("starts scale-in from the row actions menu and refreshes the list", async () => {
  const scalableNode = {
    ...nodeRow,
    controller: { role: "follower", voter: false, leader_id: 1 },
    actions: { ...nodeRow.actions, can_scale_in: true },
  }
  const leavingNodeRow = {
    ...scalableNode,
    membership: { role: "data", join_state: "leaving", schedulable: false },
    actions: { ...nodeRow.actions, can_onboard: false, can_move_slots_in: false, can_move_slots_out: false, can_scale_in: true },
  }
  getNodesMock
    .mockResolvedValueOnce({
      generated_at: "2026-07-01T08:00:00Z",
      controller_leader_id: 1,
      total: 1,
      items: [scalableNode],
    })
    .mockResolvedValueOnce({
      generated_at: "2026-07-01T08:00:01Z",
      controller_leader_id: 1,
      total: 1,
      items: [leavingNodeRow],
    })
  startNodeScaleInMock.mockResolvedValueOnce({
    changed: true,
    node_id: 1,
    addr: "127.0.0.1:7000",
    join_state: "leaving",
    revision: 49,
  })

  const user = userEvent.setup()
  renderNodesPage()

  expect(await screen.findByText("127.0.0.1:7000")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "More actions for node 1" }))
  await user.click(screen.getByRole("button", { name: "Start scale-in for node 1" }))

  expect(startNodeScaleInMock).not.toHaveBeenCalled()
  expect(await screen.findByText("Confirm mark leaving")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Confirm" }))
  expect(startNodeScaleInMock).toHaveBeenCalledWith(1)
  await waitFor(() => {
    expect(screen.getByRole("button", { name: "Advance scale-in for node 1" })).toBeInTheDocument()
  })
})

test("shows a row action error when the post-action refresh fails", async () => {
  const scalableNode = {
    ...nodeRow,
    controller: { role: "follower", voter: false, leader_id: 1 },
    actions: { ...nodeRow.actions, can_scale_in: true },
  }
  getNodesMock
    .mockResolvedValueOnce({
      generated_at: "2026-07-01T08:00:00Z",
      controller_leader_id: 1,
      total: 1,
      items: [scalableNode],
    })
    .mockRejectedValueOnce(new ManagerApiError(503, "service_unavailable", "controller leader unavailable"))
  startNodeScaleInMock.mockResolvedValueOnce({
    changed: true,
    node_id: 1,
    addr: "127.0.0.1:7000",
    join_state: "leaving",
    revision: 49,
  })

  const user = userEvent.setup()
  renderNodesPage()

  expect(await screen.findByText("127.0.0.1:7000")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "More actions for node 1" }))
  await user.click(screen.getByRole("button", { name: "Start scale-in for node 1" }))

  expect(startNodeScaleInMock).not.toHaveBeenCalled()
  expect(await screen.findByText("Confirm mark leaving")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Confirm" }))
  expect(startNodeScaleInMock).toHaveBeenCalledWith(1)
  expect(await screen.findByText(/currently unavailable/i)).toBeInTheDocument()
})
