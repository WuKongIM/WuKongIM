import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeEach, expect, test, vi } from "vitest"

import { createAnonymousAuthState, useAuthStore } from "@/auth/auth-store"
import { resetLocale } from "@/i18n/locale-store"
import { I18nProvider } from "@/i18n/provider"
import { ManagerApiError } from "@/lib/manager-api"
import { TasksPage } from "@/pages/tasks/page"

const getControllerTasksMock = vi.fn()
const getControllerTaskAuditsMock = vi.fn()
const getControllerTaskAuditEventsMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getControllerTasks: (...args: unknown[]) => getControllerTasksMock(...args),
    getControllerTaskAudits: (...args: unknown[]) => getControllerTaskAuditsMock(...args),
    getControllerTaskAuditEvents: (...args: unknown[]) => getControllerTaskAuditEventsMock(...args),
  }
})

const activeTasksFixture = {
  total: 1,
  items: [{
    task_id: "slot-1-replica-move-2-to-4-r9",
    slot_id: 1,
    kind: "slot_replica_move",
    step: "promote_learner",
    status: "running",
    source_node: 2,
    target_node: 4,
    target_peers: [1, 3, 4],
    completion_policy: "all_target_peers",
    config_epoch: 9,
    attempt: 1,
    last_error: "",
    participants: [{ node_id: 4, attempt: 1, status: "done", last_error: "" }],
  }],
}

const taskAuditsFixture = {
  total: 2,
  limit: 200,
  truncated: false,
  items: [
    {
      task_id: "slot-1-replica-move-2-to-4-r9",
      kind: "slot_replica_move",
      status: "completed",
      slot_id: 1,
      leader_id: 0,
      source_node: 2,
      target_node: 4,
      first_applied_raft_index: 11,
      last_applied_raft_index: 18,
      started_at: "2026-06-29T08:00:00Z",
      completed_at: "2026-06-29T08:01:00Z",
      event_count: 4,
      truncated: false,
      summary: "completed slot_replica_move task for slot 1",
      last_reason: "",
    },
    {
      task_id: "slot-2-bootstrap-1",
      kind: "bootstrap",
      status: "running",
      slot_id: 2,
      leader_id: 1,
      source_node: 0,
      target_node: 1,
      first_applied_raft_index: 21,
      last_applied_raft_index: 22,
      started_at: "2026-06-29T08:02:00Z",
      completed_at: null,
      event_count: 2,
      truncated: false,
      summary: "snapshot active bootstrap task for slot 2",
      last_reason: "",
    },
  ],
}

const taskAuditEventsFixture = {
  task: taskAuditsFixture.items[0],
  events: [
    {
      event_id: "event-created",
      task_id: "slot-1-replica-move-2-to-4-r9",
      type: "created",
      kind: "slot_replica_move",
      status: "running",
      slot_id: 1,
      leader_id: 0,
      source_node: 2,
      target_node: 4,
      applied_raft_index: 11,
      applied_raft_term: 2,
      command_kind: "upsert_slot_replica_move_task",
      participant_node: 0,
      occurred_at: "2026-06-29T08:00:00Z",
      summary: "created slot_replica_move task for slot 1",
      reason: "",
      details: { step: "open_learner" },
    },
    {
      event_id: "event-completed",
      task_id: "slot-1-replica-move-2-to-4-r9",
      type: "completed",
      kind: "slot_replica_move",
      status: "completed",
      slot_id: 1,
      leader_id: 0,
      source_node: 2,
      target_node: 4,
      applied_raft_index: 18,
      applied_raft_term: 2,
      command_kind: "complete_task",
      participant_node: 0,
      occurred_at: "2026-06-29T08:01:00Z",
      summary: "completed slot_replica_move task for slot 1",
      reason: "",
      details: { step: "commit_assignment" },
    },
  ],
  truncated: false,
}

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  getControllerTasksMock.mockReset()
  getControllerTaskAuditsMock.mockReset()
  getControllerTaskAuditEventsMock.mockReset()
  getControllerTasksMock.mockResolvedValue(activeTasksFixture)
  getControllerTaskAuditsMock.mockResolvedValue(taskAuditsFixture)
  getControllerTaskAuditEventsMock.mockResolvedValue(taskAuditEventsFixture)
  useAuthStore.setState({
    ...createAnonymousAuthState(),
    isHydrated: true,
    status: "authenticated",
    username: "admin",
    tokenType: "Bearer",
    accessToken: "token-1",
    expiresAt: "2099-05-14T12:00:00Z",
    permissions: [{ resource: "cluster.controller", actions: ["r"] }],
  })
})

function renderTasksPage() {
  return render(
    <I18nProvider>
      <TasksPage />
    </I18nProvider>,
  )
}

test("renders active ControllerV2 tasks and retained audit history", async () => {
  renderTasksPage()

  expect(await screen.findByRole("heading", { name: "Controller Tasks" })).toBeInTheDocument()
  expect(screen.getAllByText("slot-1-replica-move-2-to-4-r9").length).toBeGreaterThan(0)
  expect(screen.getAllByText("slot_replica_move").length).toBeGreaterThan(0)
  expect(screen.getByText("completed slot_replica_move task for slot 1")).toBeInTheDocument()
})

test("filters ControllerV2 tasks by kind, status, slot, node, and keyword", async () => {
  const user = userEvent.setup()
  renderTasksPage()

  await screen.findAllByText("slot-1-replica-move-2-to-4-r9")
  await user.selectOptions(screen.getByLabelText("Kind"), "slot_replica_move")
  await user.selectOptions(screen.getByLabelText("Status"), "completed")
  await user.type(screen.getByLabelText("Slot ID"), "1")
  await user.type(screen.getByLabelText("Node ID"), "4")
  await user.type(screen.getByLabelText("Keyword"), "completed")
  await user.click(screen.getByRole("button", { name: "Refresh" }))

  expect(getControllerTasksMock).toHaveBeenLastCalledWith({
    kind: "slot_replica_move",
    status: undefined,
    slotId: 1,
    nodeId: 4,
    limit: 50,
  })
  expect(getControllerTaskAuditsMock).toHaveBeenLastCalledWith({
    kind: "slot_replica_move",
    status: "completed",
    slotId: 1,
    nodeId: 4,
    keyword: "completed",
    limit: 200,
  })
})

test("opens retained task audit timeline", async () => {
  const user = userEvent.setup()
  renderTasksPage()

  await screen.findAllByText("slot-1-replica-move-2-to-4-r9")
  await user.click(screen.getAllByRole("button", { name: "View timeline" })[0])

  const dialog = await screen.findByRole("dialog")
  expect(within(dialog).getByText("event-created")).toBeInTheDocument()
  expect(within(dialog).getByText((_content, element) => element?.textContent === "Command: complete_task")).toBeInTheDocument()
  expect(getControllerTaskAuditEventsMock).toHaveBeenCalledWith("slot-1-replica-move-2-to-4-r9")
})

test("maps forbidden and unavailable errors", async () => {
  getControllerTasksMock.mockRejectedValueOnce(new ManagerApiError(403, "forbidden", "forbidden"))
  renderTasksPage()

  expect(await screen.findByRole("heading", { name: "Controller Tasks" })).toBeInTheDocument()
  expect(screen.getByText("You do not have permission to view this manager resource.")).toBeInTheDocument()
})

test("renders empty state", async () => {
  getControllerTasksMock.mockResolvedValueOnce({ total: 0, items: [] })
  getControllerTaskAuditsMock.mockResolvedValueOnce({ total: 0, limit: 200, truncated: false, items: [] })
  renderTasksPage()

  expect(await screen.findByText("No ControllerV2 tasks match the current filters.")).toBeInTheDocument()
})
