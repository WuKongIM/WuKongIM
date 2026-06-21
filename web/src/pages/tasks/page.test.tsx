import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeEach, expect, test, vi } from "vitest"

import { createAnonymousAuthState, useAuthStore } from "@/auth/auth-store"
import { resetLocale } from "@/i18n/locale-store"
import { I18nProvider } from "@/i18n/provider"
import { ManagerApiError } from "@/lib/manager-api"
import { TasksPage } from "@/pages/tasks/page"

const getDistributedTasksSummaryMock = vi.fn()
const getDistributedTasksMock = vi.fn()
const getDistributedTaskMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getDistributedTasksSummary: (...args: unknown[]) => getDistributedTasksSummaryMock(...args),
    getDistributedTasks: (...args: unknown[]) => getDistributedTasksMock(...args),
    getDistributedTask: (...args: unknown[]) => getDistributedTaskMock(...args),
  }
})

const summaryFixture = {
  total: 2,
  by_status: { pending: 0, running: 1, retrying: 1, blocked: 0, failed: 0, completed: 0, cancelled: 0, unknown: 0 },
  by_domain: { slot_reconcile: 1, node_onboarding: 0, node_scale_in: 0, channel_migration: 1 },
  partial: false,
  warnings: [],
}

const tasksFixture = {
  total: 2,
  items: [
    {
      id: "slot-reconcile:1",
      domain: "slot_reconcile",
      kind: "repair",
      status: "retrying",
      phase: "catch_up",
      scope: { type: "slot", id: "1", slot_id: 1, channel_id: "", channel_type: 0, node_id: 0 },
      source_node: 2,
      target_node: 3,
      owner_node: 0,
      attempt: 1,
      next_run_at: null,
      created_at: null,
      updated_at: "2026-05-14T10:00:00Z",
      last_error: "learner catch-up timeout",
      summary: "Slot 1 repair is retrying.",
      links: { slot: "/slots?slot_id=1" },
    },
    {
      id: "migration-1",
      domain: "channel_migration",
      kind: "replica_replace",
      status: "running",
      phase: "catch_up",
      scope: { type: "channel", id: "2/room-1", slot_id: 0, channel_id: "room-1", channel_type: 2, node_id: 0 },
      source_node: 2,
      target_node: 4,
      owner_node: 0,
      attempt: 0,
      next_run_at: null,
      created_at: "2026-05-14T09:55:00Z",
      updated_at: "2026-05-14T10:01:00Z",
      last_error: "",
      summary: "Channel migration is running.",
      links: { channel: "/channel-cluster/list?channel_id=room-1" },
    },
  ],
  next_cursor: "",
  has_more: false,
  partial: false,
  warnings: [],
}

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  getDistributedTasksSummaryMock.mockReset()
  getDistributedTasksMock.mockReset()
  getDistributedTaskMock.mockReset()
  getDistributedTasksSummaryMock.mockResolvedValue(summaryFixture)
  getDistributedTasksMock.mockResolvedValue(tasksFixture)
  getDistributedTaskMock.mockResolvedValue({
    task: tasksFixture.items[0],
    detail: { domain: "slot_reconcile", raw_status: "retrying", slot: null },
  })
  useAuthStore.setState({
    ...createAnonymousAuthState(),
    isHydrated: true,
    status: "authenticated",
    username: "admin",
    tokenType: "Bearer",
    accessToken: "token-1",
    expiresAt: "2099-05-14T12:00:00Z",
    permissions: [{ resource: "cluster.task", actions: ["r"] }],
  })
})

function renderTasksPage() {
  return render(
    <I18nProvider>
      <TasksPage />
    </I18nProvider>,
  )
}

test("renders summary cards and distributed task rows", async () => {
  renderTasksPage()

  expect(await screen.findByRole("heading", { name: "Distributed Tasks" })).toBeInTheDocument()
  expect(screen.getByText("2")).toBeInTheDocument()
  expect(screen.getByText("Slot 1")).toBeInTheDocument()
  expect(screen.getAllByText("slot_reconcile").length).toBeGreaterThan(0)
  expect(screen.getByText("learner catch-up timeout")).toBeInTheDocument()
})

test("filters task list by domain, status, node, and keyword", async () => {
  const user = userEvent.setup()
  renderTasksPage()

  await screen.findByText("Slot 1")
  await user.selectOptions(screen.getByLabelText("Domain"), "channel_migration")
  await user.selectOptions(screen.getByLabelText("Status"), "running")
  await user.type(screen.getByLabelText("Node ID"), "4")
  await user.type(screen.getByLabelText("Keyword"), "room")
  await user.click(screen.getByRole("button", { name: "Refresh" }))

  expect(getDistributedTasksMock).toHaveBeenLastCalledWith({
    domain: "channel_migration",
    status: "running",
    nodeId: 4,
    scope: undefined,
    keyword: "room",
    limit: 50,
  })
})

test("opens task detail sheet", async () => {
  const user = userEvent.setup()
  renderTasksPage()

  await screen.findByText("Slot 1")
  await user.click(screen.getAllByRole("button", { name: "View detail" })[0])

  const dialog = await screen.findByRole("dialog")
  expect(within(dialog).getByText("slot-reconcile:1")).toBeInTheDocument()
  expect(getDistributedTaskMock).toHaveBeenCalledWith("slot_reconcile", "slot-reconcile:1")
})

test("renders source-specific task detail payload", async () => {
  getDistributedTaskMock.mockResolvedValueOnce({
    task: tasksFixture.items[0],
    detail: {
      domain: "slot_reconcile",
      raw_status: "retrying",
      slot: {
        slot_id: 1,
        kind: "repair",
        step: "catch_up",
        status: "retrying",
        source_node: 2,
        target_node: 3,
        attempt: 1,
        next_run_at: null,
        last_error: "learner catch-up timeout",
        slot: {
          state: { quorum: "healthy", sync: "synced" },
          assignment: { desired_peers: [2, 3], config_epoch: 8, balance_version: 1 },
          runtime: {
            current_peers: [2, 3],
            current_voters: [2, 3],
            preferred_leader_id: 2,
            healthy_voters: 2,
            has_quorum: true,
            observed_config_epoch: 8,
            last_report_at: "2026-05-14T10:00:00Z",
          },
        },
      },
    },
  })
  const user = userEvent.setup()
  renderTasksPage()

  await screen.findByText("Slot 1")
  await user.click(screen.getAllByRole("button", { name: "View detail" })[0])

  const dialog = await screen.findByRole("dialog")
  expect(within(dialog).getByText("Slot context")).toBeInTheDocument()
  expect(within(dialog).getByText("healthy")).toBeInTheDocument()
  expect(within(dialog).getAllByText("2, 3").length).toBeGreaterThan(0)
})

test("renders partial warnings with available rows", async () => {
  getDistributedTasksMock.mockResolvedValueOnce({
    ...tasksFixture,
    partial: true,
    warnings: [{ domain: "channel_migration", code: "source_unavailable", message: "channel migration unavailable" }],
  })
  renderTasksPage()

  expect(await screen.findByText("Some task sources are unavailable. Showing partial results.")).toBeInTheDocument()
  expect(screen.getByText("Slot 1")).toBeInTheDocument()
})

test("maps forbidden and unavailable errors", async () => {
  getDistributedTasksSummaryMock.mockRejectedValueOnce(new ManagerApiError(403, "forbidden", "forbidden"))
  renderTasksPage()

  expect(await screen.findByRole("heading", { name: "Distributed Tasks" })).toBeInTheDocument()
  expect(screen.getByText("You do not have permission to view this manager resource.")).toBeInTheDocument()
})

test("renders empty state", async () => {
  getDistributedTasksMock.mockResolvedValueOnce({ total: 0, items: [], next_cursor: "", has_more: false, partial: false, warnings: [] })
  renderTasksPage()

  expect(await screen.findByText("No distributed tasks match the current filters.")).toBeInTheDocument()
})
