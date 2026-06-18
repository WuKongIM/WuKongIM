import { act, render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom"
import { beforeEach, expect, test, vi } from "vitest"

import { I18nProvider } from "@/i18n/provider"
import { resetLocale } from "@/i18n/locale-store"
import { ControllerPage } from "@/pages/controller/page"

const getNodesMock = vi.fn()
const getControllerLogsMock = vi.fn()
const getControllerRaftStatusMock = vi.fn()
const compactControllerRaftLogOnNodeMock = vi.fn()
const compactControllerRaftLogsMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getNodes: (...args: unknown[]) => getNodesMock(...args),
    getControllerLogs: (...args: unknown[]) => getControllerLogsMock(...args),
    getControllerRaftStatus: (...args: unknown[]) => getControllerRaftStatusMock(...args),
    compactControllerRaftLogOnNode: (...args: unknown[]) => compactControllerRaftLogOnNodeMock(...args),
    compactControllerRaftLogs: (...args: unknown[]) => compactControllerRaftLogsMock(...args),
  }
})

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  getNodesMock.mockReset()
  getControllerLogsMock.mockReset()
  getControllerRaftStatusMock.mockReset()
  compactControllerRaftLogOnNodeMock.mockReset()
  compactControllerRaftLogsMock.mockReset()
})

function LocationProbe() {
  const location = useLocation()
  return <div data-testid="location">{`${location.pathname}${location.search}`}</div>
}

function renderControllerPage(initialEntry = "/controller") {
  return render(
    <I18nProvider>
      <MemoryRouter initialEntries={[initialEntry]}>
        <Routes>
          <Route
            element={(
              <>
                <ControllerPage />
                <LocationProbe />
              </>
            )}
            path="/controller"
          />
        </Routes>
      </MemoryRouter>
    </I18nProvider>,
  )
}

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((nextResolve) => {
    resolve = nextResolve
  })
  return { promise, resolve }
}

function controllerLogPage(nodeId: number) {
  return {
    node_id: nodeId,
    first_index: 1,
    last_index: 2,
    commit_index: 2,
    applied_index: 2,
    items: [],
  }
}

function controllerRaftStatus(nodeId: number, health = "healthy") {
  return {
    node_id: nodeId,
    role: nodeId === 1 ? "follower" : "leader",
    leader_id: 2,
    term: 7,
    health,
    first_index: nodeId * 10,
    last_index: nodeId * 10 + 5,
    commit_index: nodeId * 10 + 4,
    applied_index: nodeId * 10 + 3,
    snapshot_index: nodeId * 10 - 1,
    snapshot_term: 3,
    compaction: {
      enabled: true,
      trigger_entries: 100,
      check_interval_ms: 2000,
      last_snapshot_index: nodeId * 10 - 1,
      last_snapshot_at: "2026-05-06T08:01:00Z",
      last_check_at: "2026-05-06T08:02:00Z",
      last_error: "",
      last_error_at: "0001-01-01T00:00:00Z",
      degraded: false,
    },
    restore: {
      last_snapshot_index: nodeId * 10 - 2,
      last_snapshot_term: 2,
      last_restored_at: "2026-05-06T08:04:00Z",
      last_error: "",
      last_error_at: "0001-01-01T00:00:00Z",
      failed: false,
    },
    peers: [],
  }
}

test("renders controller raft status and peer snapshot progress", async () => {
  getNodesMock.mockResolvedValueOnce({
    generated_at: "2026-05-06T08:00:00Z",
    controller_leader_id: 2,
    total: 1,
    items: [{
      node_id: 2,
      name: "node-2",
      addr: "127.0.0.1:7002",
      status: "alive",
      last_heartbeat_at: "2026-05-06T07:59:58Z",
      is_local: true,
      capacity_weight: 1,
      controller: { role: "leader", voter: true, leader_id: 2 },
      slot_stats: { count: 0, leader_count: 0 },
    }],
  })
  getControllerLogsMock.mockResolvedValueOnce({
    node_id: 2,
    first_index: 10,
    last_index: 42,
    commit_index: 40,
    applied_index: 39,
    items: [],
  })
  getControllerRaftStatusMock.mockResolvedValueOnce({
    node_id: 2,
    role: "leader",
    leader_id: 2,
    term: 7,
    health: "snapshot_transferring",
    first_index: 10,
    last_index: 42,
    commit_index: 40,
    applied_index: 39,
    snapshot_index: 9,
    snapshot_term: 3,
    compaction: {
      enabled: true,
      trigger_entries: 100,
      check_interval_ms: 2000,
      last_snapshot_index: 9,
      last_snapshot_at: "2026-05-06T08:01:00Z",
      last_check_at: "2026-05-06T08:02:00Z",
      last_error: "",
      last_error_at: "0001-01-01T00:00:00Z",
      degraded: false,
    },
    restore: {
      last_snapshot_index: 8,
      last_snapshot_term: 2,
      last_restored_at: "2026-05-06T08:04:00Z",
      last_error: "",
      last_error_at: "0001-01-01T00:00:00Z",
      failed: false,
    },
    peers: [{
      node_id: 3,
      match: 21,
      next: 22,
      state: "snapshot",
      pending_snapshot: 9,
      recent_active: true,
      needs_snapshot: true,
      snapshot_transferring: true,
    }],
  })

  renderControllerPage()

  expect(await screen.findByText("Controller Raft Status")).toBeInTheDocument()
  expect(getControllerRaftStatusMock).toHaveBeenCalledWith(2)
  expect(screen.getAllByText("snapshot transferring")).not.toHaveLength(0)
  expect(screen.getByText("commit 40 / applied 39")).toBeInTheDocument()
  expect(screen.getByText("snapshot 9 / term 3")).toBeInTheDocument()
  expect(screen.getByText("Follower Progress")).toBeInTheDocument()
  expect(screen.getByText("Node 3")).toBeInTheDocument()
  expect(screen.getByText("match 21 / next 22")).toBeInTheDocument()
  expect(screen.getByText("needs snapshot")).toBeInTheDocument()
})

test("preselects the controller node from the URL query", async () => {
  getNodesMock.mockResolvedValueOnce({
    generated_at: "2026-05-06T08:00:00Z",
    controller_leader_id: 2,
    total: 2,
    items: [
      {
        node_id: 1,
        name: "node-1",
        addr: "127.0.0.1:7001",
        status: "alive",
        last_heartbeat_at: "2026-05-06T07:59:58Z",
        is_local: true,
        capacity_weight: 1,
        controller: { role: "follower", voter: true, leader_id: 2 },
        slot_stats: { count: 0, leader_count: 0 },
      },
      {
        node_id: 2,
        name: "node-2",
        addr: "127.0.0.1:7002",
        status: "alive",
        last_heartbeat_at: "2026-05-06T07:59:58Z",
        is_local: false,
        capacity_weight: 1,
        controller: { role: "leader", voter: true, leader_id: 2 },
        slot_stats: { count: 0, leader_count: 0 },
      },
    ],
  })
  getControllerLogsMock.mockImplementation(({ nodeId }: { nodeId: number }) => Promise.resolve(controllerLogPage(nodeId)))
  getControllerRaftStatusMock.mockImplementation((nodeId: number) => Promise.resolve(controllerRaftStatus(nodeId)))

  renderControllerPage("/controller?node_id=2")

  expect(await screen.findByText("Node 2 local Controller Raft health, compaction, restore, and follower catch-up.")).toBeInTheDocument()
  expect(screen.getByLabelText("Node filter")).toHaveValue("2")
  expect(getControllerLogsMock).toHaveBeenCalledWith(expect.objectContaining({ nodeId: 2 }))
  expect(getControllerRaftStatusMock).toHaveBeenCalledWith(2)
})

test("renders controller log entry creation time", async () => {
  const createdAtMS = Date.UTC(2026, 5, 18, 1, 10, 11, 123)
  const formattedCreatedAt = new Intl.DateTimeFormat("en", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  }).format(new Date(createdAtMS))
  getNodesMock.mockResolvedValueOnce({
    generated_at: "2026-05-06T08:00:00Z",
    controller_leader_id: 2,
    total: 1,
    items: [{
      node_id: 2,
      name: "node-2",
      addr: "127.0.0.1:7002",
      status: "alive",
      last_heartbeat_at: "2026-05-06T07:59:58Z",
      is_local: true,
      capacity_weight: 1,
      controller: { role: "leader", voter: true, leader_id: 2 },
      slot_stats: { count: 0, leader_count: 0 },
    }],
  })
  getControllerLogsMock.mockResolvedValueOnce({
    node_id: 2,
    first_index: 1,
    last_index: 4,
    commit_index: 4,
    applied_index: 3,
    items: [
      {
        index: 4,
        term: 2,
        type: "normal",
        created_at_ms: createdAtMS,
        data_size: 12,
        decode_status: "ok",
        decoded_type: "init_cluster_state",
        decoded: { command: "init_cluster_state" },
      },
    ],
  })
  getControllerRaftStatusMock.mockResolvedValueOnce(controllerRaftStatus(2))

  renderControllerPage("/controller?node_id=2")

  expect(await screen.findByText("Created at")).toBeInTheDocument()
  expect(screen.getByText(formattedCreatedAt)).toBeInTheDocument()
})

test("keeps the latest selected node status when an older request resolves later", async () => {
  const user = userEvent.setup()
  const nodeOneStatus = deferred<ReturnType<typeof controllerRaftStatus>>()

  getNodesMock.mockResolvedValueOnce({
    generated_at: "2026-05-06T08:00:00Z",
    controller_leader_id: 2,
    total: 2,
    items: [
      {
        node_id: 1,
        name: "node-1",
        addr: "127.0.0.1:7001",
        status: "alive",
        last_heartbeat_at: "2026-05-06T07:59:58Z",
        is_local: true,
        capacity_weight: 1,
        controller: { role: "follower", voter: true, leader_id: 2 },
        slot_stats: { count: 0, leader_count: 0 },
      },
      {
        node_id: 2,
        name: "node-2",
        addr: "127.0.0.1:7002",
        status: "alive",
        last_heartbeat_at: "2026-05-06T07:59:58Z",
        is_local: false,
        capacity_weight: 1,
        controller: { role: "leader", voter: true, leader_id: 2 },
        slot_stats: { count: 0, leader_count: 0 },
      },
    ],
  })
  getControllerLogsMock.mockImplementation(({ nodeId }: { nodeId: number }) => Promise.resolve(controllerLogPage(nodeId)))
  getControllerRaftStatusMock.mockImplementation((nodeId: number) => {
    if (nodeId === 1) {
      return nodeOneStatus.promise
    }
    return Promise.resolve(controllerRaftStatus(nodeId, "snapshot_required"))
  })

  renderControllerPage()

  await waitFor(() => expect(getControllerRaftStatusMock).toHaveBeenCalledWith(1))
  await user.selectOptions(screen.getByLabelText("Node filter"), "2")

  expect(await screen.findByText("Node 2 local Controller Raft health, compaction, restore, and follower catch-up.")).toBeInTheDocument()
  expect(screen.getByTestId("location")).toHaveTextContent("/controller?node_id=2")

  await act(async () => {
    nodeOneStatus.resolve(controllerRaftStatus(1, "restore_failed"))
    await nodeOneStatus.promise
  })

  expect(screen.getByText("Node 2 local Controller Raft health, compaction, restore, and follower catch-up.")).toBeInTheDocument()
  expect(screen.queryByText("Node 1 local Controller Raft health, compaction, restore, and follower catch-up.")).not.toBeInTheDocument()
  expect(screen.queryByText("restore failed")).not.toBeInTheDocument()
})

test("hides the old node status immediately when the selected node changes", async () => {
  const user = userEvent.setup()
  const nodeTwoStatus = deferred<ReturnType<typeof controllerRaftStatus>>()

  getNodesMock.mockResolvedValueOnce({
    generated_at: "2026-05-06T08:00:00Z",
    controller_leader_id: 2,
    total: 2,
    items: [
      {
        node_id: 1,
        name: "node-1",
        addr: "127.0.0.1:7001",
        status: "alive",
        last_heartbeat_at: "2026-05-06T07:59:58Z",
        is_local: true,
        capacity_weight: 1,
        controller: { role: "follower", voter: true, leader_id: 2 },
        slot_stats: { count: 0, leader_count: 0 },
      },
      {
        node_id: 2,
        name: "node-2",
        addr: "127.0.0.1:7002",
        status: "alive",
        last_heartbeat_at: "2026-05-06T07:59:58Z",
        is_local: false,
        capacity_weight: 1,
        controller: { role: "leader", voter: true, leader_id: 2 },
        slot_stats: { count: 0, leader_count: 0 },
      },
    ],
  })
  getControllerLogsMock.mockImplementation(({ nodeId }: { nodeId: number }) => Promise.resolve(controllerLogPage(nodeId)))
  getControllerRaftStatusMock.mockImplementation((nodeId: number) => {
    if (nodeId === 2) {
      return nodeTwoStatus.promise
    }
    return Promise.resolve(controllerRaftStatus(nodeId, "healthy"))
  })

  renderControllerPage()

  expect(await screen.findByText("Node 1 local Controller Raft health, compaction, restore, and follower catch-up.")).toBeInTheDocument()
  await user.selectOptions(screen.getByLabelText("Node filter"), "2")

  expect(screen.queryByText("Node 1 local Controller Raft health, compaction, restore, and follower catch-up.")).not.toBeInTheDocument()

  await act(async () => {
    nodeTwoStatus.resolve(controllerRaftStatus(2, "snapshot_required"))
    await nodeTwoStatus.promise
  })

  expect(await screen.findByText("Node 2 local Controller Raft health, compaction, restore, and follower catch-up.")).toBeInTheDocument()
})

test("prompts for control-plane-wide compaction and refreshes the selected node view", async () => {
  const user = userEvent.setup()
  getNodesMock.mockResolvedValueOnce({
    generated_at: "2026-05-06T08:00:00Z",
    controller_leader_id: 2,
    total: 1,
    items: [{
      node_id: 2,
      name: "node-2",
      addr: "127.0.0.1:7002",
      status: "alive",
      last_heartbeat_at: "2026-05-06T07:59:58Z",
      is_local: true,
      capacity_weight: 1,
      controller: { role: "leader", voter: true, leader_id: 2 },
      slot_stats: { count: 0, leader_count: 0 },
    }],
  })
  getControllerLogsMock.mockImplementation(({ nodeId }: { nodeId: number }) => Promise.resolve(controllerLogPage(nodeId)))
  getControllerRaftStatusMock.mockImplementation((nodeId: number) => Promise.resolve(controllerRaftStatus(nodeId)))
  compactControllerRaftLogsMock.mockResolvedValueOnce({
    generated_at: "2026-05-07T10:00:00Z",
    total: 2,
    succeeded: 1,
    failed: 1,
    items: [{
      node_id: 1,
      success: true,
      applied_index: 42,
      before_snapshot_index: 30,
      after_snapshot_index: 42,
      compacted: true,
      skipped_reason: "",
      error: "",
    }, {
      node_id: 2,
      success: false,
      applied_index: 0,
      before_snapshot_index: 0,
      after_snapshot_index: 0,
      compacted: false,
      skipped_reason: "",
      error: "node stopped",
    }],
  })

  renderControllerPage("/controller?node_id=2")

  expect(await screen.findByText("Node 2 local Controller Raft health, compaction, restore, and follower catch-up.")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Trigger compaction" }))
  const dialog = screen.getByRole("dialog")
  await user.click(within(dialog).getByRole("radio", { name: "All Controller nodes" }))
  await user.click(within(dialog).getByRole("button", { name: "Trigger compaction" }))

  await waitFor(() => expect(compactControllerRaftLogsMock).toHaveBeenCalledTimes(1))
  expect(compactControllerRaftLogOnNodeMock).not.toHaveBeenCalled()
  expect(await screen.findByText("Compaction Result")).toBeInTheDocument()
  expect(screen.getByText("1 succeeded / 1 failed")).toBeInTheDocument()
  expect(screen.getByText("Node 1")).toBeInTheDocument()
  expect(screen.getByText("Node 2")).toBeInTheDocument()
  expect(screen.getByText("node stopped")).toBeInTheDocument()
  expect(getControllerRaftStatusMock).toHaveBeenCalledTimes(2)
  expect(getControllerRaftStatusMock).toHaveBeenLastCalledWith(2)
  expect(getControllerLogsMock).toHaveBeenCalledTimes(2)
})

test("prompts for current-node compaction and posts the selected node", async () => {
  const user = userEvent.setup()
  getNodesMock.mockResolvedValueOnce({
    generated_at: "2026-05-06T08:00:00Z",
    controller_leader_id: 2,
    total: 1,
    items: [{
      node_id: 2,
      name: "node-2",
      addr: "127.0.0.1:7002",
      status: "alive",
      last_heartbeat_at: "2026-05-06T07:59:58Z",
      is_local: true,
      capacity_weight: 1,
      controller: { role: "leader", voter: true, leader_id: 2 },
      slot_stats: { count: 0, leader_count: 0 },
    }],
  })
  getControllerLogsMock.mockImplementation(({ nodeId }: { nodeId: number }) => Promise.resolve(controllerLogPage(nodeId)))
  getControllerRaftStatusMock.mockImplementation((nodeId: number) => Promise.resolve(controllerRaftStatus(nodeId)))
  compactControllerRaftLogOnNodeMock.mockResolvedValueOnce({
    generated_at: "2026-05-07T10:03:00Z",
    total: 1,
    succeeded: 1,
    failed: 0,
    items: [{
      node_id: 2,
      success: true,
      applied_index: 50,
      before_snapshot_index: 40,
      after_snapshot_index: 50,
      compacted: true,
      skipped_reason: "",
      error: "",
    }],
  })

  renderControllerPage("/controller?node_id=2")

  expect(await screen.findByText("Node 2 local Controller Raft health, compaction, restore, and follower catch-up.")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Trigger compaction" }))
  const dialog = screen.getByRole("dialog")
  expect(within(dialog).getByRole("radio", { name: "Current node 2" })).toBeChecked()
  await user.click(within(dialog).getByRole("button", { name: "Trigger compaction" }))

  await waitFor(() => expect(compactControllerRaftLogOnNodeMock).toHaveBeenCalledWith(2))
  expect(compactControllerRaftLogsMock).not.toHaveBeenCalled()
  expect(await screen.findByText("Compaction Result")).toBeInTheDocument()
  expect(screen.getByText("1 succeeded / 0 failed")).toBeInTheDocument()
  expect(screen.getByText("snapshot 40 → 50 at applied 50")).toBeInTheDocument()
  expect(getControllerRaftStatusMock).toHaveBeenCalledTimes(2)
  expect(getControllerLogsMock).toHaveBeenCalledTimes(2)
})
