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
const startNodeScaleInMock = vi.fn()
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
    startNodeScaleIn: (...args: unknown[]) => startNodeScaleInMock(...args),
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

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  getNodesMock.mockReset()
  getNodeMock.mockReset()
  startNodeScaleInMock.mockReset()
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

test("renders layered node inventory fields and lifecycle row actions", async () => {
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
  expect(screen.getByRole("button", { name: "Open lifecycle for node 1" })).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Inspect node 1" })).toBeInTheDocument()
  expect(screen.queryByRole("button", { name: "Drain" })).not.toBeInTheDocument()
  expect(screen.queryByRole("button", { name: "Resume" })).not.toBeInTheDocument()
  expect(screen.queryByRole("button", { name: "Review scale-in for node 1" })).not.toBeInTheDocument()
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

test("opens lifecycle sheet from an active node row", async () => {
  getNodesMock.mockResolvedValueOnce({
    generated_at: "2026-07-01T08:00:00Z",
    controller_leader_id: 1,
    total: 1,
    items: [nodeRow],
  })

  const user = userEvent.setup()
  renderNodesPage()

  expect(await screen.findByText("127.0.0.1:7000")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Open lifecycle for node 1" }))

  expect(await screen.findByRole("dialog", { name: "Node lifecycle" })).toBeInTheDocument()
})

test("refreshes the open lifecycle node after a lifecycle action completes", async () => {
  const leavingNodeRow = {
    ...nodeRow,
    membership: { role: "data", join_state: "leaving", schedulable: false },
    actions: { ...nodeRow.actions, can_onboard: false },
  }
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
  await user.click(screen.getByRole("button", { name: "Open lifecycle for node 1" }))

  const dialog = await screen.findByRole("dialog", { name: "Node lifecycle" })
  expect(within(dialog).getByRole("button", { name: "Plan slot onboarding" })).toBeInTheDocument()

  await user.click(within(dialog).getByRole("button", { name: "Mark leaving" }))

  expect(startNodeScaleInMock).toHaveBeenCalledWith(1)
  await waitFor(() => {
    expect(within(dialog).queryByRole("button", { name: "Plan slot onboarding" })).not.toBeInTheDocument()
  })
  expect(within(dialog).getAllByText("leaving")).not.toHaveLength(0)
})

test("closes the open lifecycle sheet when the refreshed node is missing", async () => {
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
      total: 0,
      items: [],
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
  await user.click(screen.getByRole("button", { name: "Open lifecycle for node 1" }))

  const dialog = await screen.findByRole("dialog", { name: "Node lifecycle" })
  await user.click(within(dialog).getByRole("button", { name: "Mark leaving" }))

  expect(startNodeScaleInMock).toHaveBeenCalledWith(1)
  await waitFor(() => {
    expect(screen.queryByRole("dialog", { name: "Node lifecycle" })).not.toBeInTheDocument()
  })
})
