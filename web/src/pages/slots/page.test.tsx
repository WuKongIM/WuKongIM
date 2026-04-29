import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeEach, expect, test, vi } from "vitest"

import { createAnonymousAuthState, useAuthStore } from "@/auth/auth-store"
import { I18nProvider } from "@/i18n/provider"
import { resetLocale } from "@/i18n/locale-store"
import { ManagerApiError } from "@/lib/manager-api"
import { SlotsPage } from "@/pages/slots/page"

const getSlotsMock = vi.fn()
const getSlotMock = vi.fn()
const getNodesMock = vi.fn()
const addSlotMock = vi.fn()
const removeSlotMock = vi.fn()
const transferSlotLeaderMock = vi.fn()
const recoverSlotMock = vi.fn()
const rebalanceSlotsMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getSlots: (...args: unknown[]) => getSlotsMock(...args),
    getSlot: (...args: unknown[]) => getSlotMock(...args),
    getNodes: (...args: unknown[]) => getNodesMock(...args),
    addSlot: (...args: unknown[]) => addSlotMock(...args),
    removeSlot: (...args: unknown[]) => removeSlotMock(...args),
    transferSlotLeader: (...args: unknown[]) => transferSlotLeaderMock(...args),
    recoverSlot: (...args: unknown[]) => recoverSlotMock(...args),
    rebalanceSlots: (...args: unknown[]) => rebalanceSlotsMock(...args),
  }
})

const slotRow = {
  slot_id: 9,
  state: { quorum: "ready", sync: "in_sync" },
  assignment: { desired_peers: [1, 2, 3], config_epoch: 7, balance_version: 4 },
  runtime: {
    current_peers: [1, 2, 3],
    leader_id: 2,
    healthy_voters: 3,
    has_quorum: true,
    observed_config_epoch: 7,
    last_report_at: "2026-04-23T08:00:00Z",
  },
}

const nodeRow = {
  node_id: 1,
  name: "node-1",
  addr: "127.0.0.1:7001",
  status: "alive",
  last_heartbeat_at: "2026-04-23T08:00:00Z",
  is_local: true,
  capacity_weight: 1,
  controller: { role: "leader", voter: true, leader_id: 1 },
  slot_stats: { count: 1, leader_count: 1 },
}

const slotDetail = {
  ...slotRow,
  task: {
    slot_id: 9,
    kind: "rebalance",
    step: "plan",
    status: "retrying",
    source_node: 1,
    target_node: 2,
    attempt: 2,
    next_run_at: null,
    last_error: "temporary failure",
  },
}

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  getSlotsMock.mockReset()
  getSlotMock.mockReset()
  getNodesMock.mockReset()
  addSlotMock.mockReset()
  removeSlotMock.mockReset()
  transferSlotLeaderMock.mockReset()
  recoverSlotMock.mockReset()
  rebalanceSlotsMock.mockReset()
  useAuthStore.setState({
    ...createAnonymousAuthState(),
    isHydrated: true,
    status: "authenticated",
    username: "admin",
    tokenType: "Bearer",
    accessToken: "token-1",
    expiresAt: "2099-04-22T12:00:00Z",
    permissions: [{ resource: "cluster.slot", actions: ["r", "w"] }],
  })
  getNodesMock.mockResolvedValue({
    generated_at: "2026-04-23T08:00:00Z",
    controller_leader_id: 1,
    total: 1,
    items: [nodeRow],
  })
})

test("enables slot write actions for wildcard manager permissions", async () => {
  useAuthStore.setState({
    permissions: [{ resource: "*", actions: ["*"] }],
  })
  getSlotsMock.mockResolvedValueOnce({ total: 1, items: [slotRow] })
  getSlotMock.mockResolvedValueOnce(slotDetail)

  const user = userEvent.setup()
  renderSlotsPage()

  expect(await screen.findByText("Slot 9")).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Add slot" })).toBeEnabled()
  expect(screen.getByRole("button", { name: "Rebalance slots" })).toBeEnabled()

  await user.click(screen.getByRole("button", { name: "Inspect slot 9" }))

  expect(await screen.findByText("Desired peers")).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Transfer leader" })).toBeEnabled()
  expect(screen.getByRole("button", { name: "Recover slot" })).toBeEnabled()
  expect(screen.getByRole("button", { name: "Remove slot" })).toBeEnabled()
})

test("adds a physical slot and opens the returned detail", async () => {
  const addedSlot = {
    ...slotRow,
    slot_id: 11,
    state: { quorum: "unknown", sync: "unreported" },
    assignment: { desired_peers: [1, 2, 3], config_epoch: 1, balance_version: 0 },
    runtime: {
      ...slotRow.runtime,
      current_peers: [],
      leader_id: 0,
      healthy_voters: 0,
      has_quorum: false,
      observed_config_epoch: 0,
    },
    task: null,
  }
  getSlotsMock.mockResolvedValueOnce({ total: 1, items: [slotRow] })
  addSlotMock.mockResolvedValueOnce(addedSlot)
  getSlotsMock.mockResolvedValueOnce({ total: 2, items: [slotRow, addedSlot] })

  const user = userEvent.setup()
  renderSlotsPage()

  expect(await screen.findByText("Slot 9")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Add slot" }))
  await user.click(screen.getByRole("button", { name: "Confirm" }))

  expect(addSlotMock).toHaveBeenCalledTimes(1)
  expect(getSlotsMock).toHaveBeenCalledTimes(2)
  expect(await screen.findByRole("heading", { name: "Slot 11" })).toBeInTheDocument()
  expect(screen.getByText("Leader 0")).toBeInTheDocument()
})

function renderSlotsPage() {
  return render(
    <I18nProvider>
      <SlotsPage />
    </I18nProvider>,
  )
}

test("uses compact slot page chrome without summary cards", async () => {
  getSlotsMock.mockResolvedValueOnce({ total: 1, items: [slotRow] })

  renderSlotsPage()

  expect(await screen.findByText("Slot 9")).toBeInTheDocument()
  expect(screen.getByText("Total: 1")).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Refresh" })).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Add slot" })).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Rebalance slots" })).toBeInTheDocument()
  expect(screen.queryByText("Scope: all slots")).not.toBeInTheDocument()
  expect(screen.queryByText("Slot distribution and runtime status.")).not.toBeInTheDocument()
  expect(screen.queryByText("Leader coverage")).not.toBeInTheDocument()
  expect(screen.queryByText("Slots currently reporting a leader.")).not.toBeInTheDocument()
  expect(screen.queryByText("Ready slots")).not.toBeInTheDocument()
  expect(screen.queryByText("Slots whose quorum state is ready.")).not.toBeInTheDocument()
  expect(screen.queryByText("In sync")).not.toBeInTheDocument()
  expect(screen.queryByText("Slots whose sync state is in sync.")).not.toBeInTheDocument()
  expect(screen.queryByText("Tracked slots")).not.toBeInTheDocument()
  expect(screen.queryByText("Physical slots currently tracked.")).not.toBeInTheDocument()
  expect(screen.queryByText("Slot Inventory")).not.toBeInTheDocument()
  expect(screen.queryByText("Current assignment and runtime state from the manager slot endpoints.")).not.toBeInTheDocument()
  expect(screen.queryByText("Cluster slots")).not.toBeInTheDocument()
  expect(screen.queryByText("Inspect one slot to view task state or run operator actions.")).not.toBeInTheDocument()
})

test("defaults to the local node filter and shows selected-node log height", async () => {
  getNodesMock.mockResolvedValueOnce({
    generated_at: "2026-04-23T08:00:00Z",
    controller_leader_id: 1,
    total: 2,
    items: [
      { ...nodeRow, node_id: 1, name: "node-1", is_local: false },
      { ...nodeRow, node_id: 2, name: "node-2", is_local: true },
    ],
  })
  getSlotsMock.mockResolvedValueOnce({
    total: 1,
    items: [{
      ...slotRow,
      node_log: { node_id: 2, leader_id: 2, commit_index: 93, applied_index: 91 },
    }],
  })

  renderSlotsPage()

  expect(await screen.findByText("Slot 9")).toBeInTheDocument()
  expect(screen.getByLabelText("Node filter")).toHaveValue("2")
  expect(getSlotsMock).toHaveBeenCalledWith({ nodeId: 2 })
  expect(screen.getByText("commit 93 / applied 91")).toBeInTheDocument()
})

test("opens slot detail and transfers the leader", async () => {
  getSlotsMock.mockResolvedValueOnce({ total: 1, items: [slotRow] })
  getSlotMock.mockResolvedValueOnce(slotDetail)
  transferSlotLeaderMock.mockResolvedValueOnce(slotDetail)
  getSlotsMock.mockResolvedValueOnce({ total: 1, items: [slotRow] })
  getSlotMock.mockResolvedValueOnce(slotDetail)

  const user = userEvent.setup()
  renderSlotsPage()

  expect(await screen.findByText("Slot 9")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Inspect slot 9" }))

  expect(await screen.findByText("Desired peers")).toBeInTheDocument()

  await user.click(screen.getByRole("button", { name: "Transfer leader" }))
  await user.type(screen.getByLabelText("Target node ID"), "2")
  await user.click(screen.getByRole("button", { name: "Transfer" }))

  expect(transferSlotLeaderMock).toHaveBeenCalledWith(9, { targetNodeId: 2 })
  expect(getSlotsMock).toHaveBeenCalledTimes(2)
  expect(getSlotMock).toHaveBeenCalledTimes(2)
})

test("submits the recover action using the selected strategy", async () => {
  getSlotsMock.mockResolvedValueOnce({ total: 1, items: [slotRow] })
  getSlotMock.mockResolvedValueOnce(slotDetail)
  recoverSlotMock.mockResolvedValueOnce({
    strategy: "latest_live_replica",
    result: "scheduled",
    slot: slotDetail,
  })
  getSlotsMock.mockResolvedValueOnce({ total: 1, items: [slotRow] })
  getSlotMock.mockResolvedValueOnce(slotDetail)

  const user = userEvent.setup()
  renderSlotsPage()

  expect(await screen.findByText("Slot 9")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Inspect slot 9" }))
  expect(await screen.findByText("Task status")).toBeInTheDocument()

  await user.click(screen.getByRole("button", { name: "Recover slot" }))
  await user.selectOptions(screen.getByLabelText("Recovery strategy"), "latest_live_replica")
  await user.click(screen.getByRole("button", { name: "Recover" }))

  expect(recoverSlotMock).toHaveBeenCalledWith(9, { strategy: "latest_live_replica" })
  expect(getSlotsMock).toHaveBeenCalledTimes(2)
  expect(getSlotMock).toHaveBeenCalledTimes(2)
})

test("shows rebalance plan results after confirmation", async () => {
  getSlotsMock.mockResolvedValue({ total: 1, items: [slotRow] })
  rebalanceSlotsMock.mockResolvedValue({
    total: 1,
    items: [{ hash_slot: 3, from_slot_id: 9, to_slot_id: 11 }],
  })

  const user = userEvent.setup()
  renderSlotsPage()

  expect(await screen.findByText("Slot 9")).toBeInTheDocument()

  await user.click(screen.getByRole("button", { name: "Rebalance slots" }))
  await user.click(screen.getByRole("button", { name: "Confirm" }))

  expect(rebalanceSlotsMock).toHaveBeenCalledTimes(1)
  expect(await screen.findByText("From slot 9 to slot 11")).toBeInTheDocument()
})

test("starts physical slot removal and refreshes the list", async () => {
  getSlotsMock.mockResolvedValueOnce({ total: 1, items: [slotRow] })
  getSlotMock.mockResolvedValueOnce(slotDetail)
  removeSlotMock.mockResolvedValueOnce({ slot_id: 9, result: "removal_started" })
  getSlotsMock.mockResolvedValueOnce({ total: 1, items: [slotRow] })

  const user = userEvent.setup()
  renderSlotsPage()

  expect(await screen.findByText("Slot 9")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Inspect slot 9" }))
  expect(await screen.findByText("Desired peers")).toBeInTheDocument()

  await user.click(screen.getByRole("button", { name: "Remove slot" }))
  await user.click(screen.getByRole("button", { name: "Confirm" }))

  expect(removeSlotMock).toHaveBeenCalledWith(9)
  expect(getSlotsMock).toHaveBeenCalledTimes(2)
  expect(await screen.findByText("Total: 1")).toBeInTheDocument()
  expect(screen.queryByText("Desired peers")).not.toBeInTheDocument()
})

test("renders unavailable state when the slot list request fails", async () => {
  getSlotsMock.mockRejectedValueOnce(
    new ManagerApiError(503, "service_unavailable", "slot leader unavailable"),
  )

  renderSlotsPage()

  expect(await screen.findByText(/currently unavailable/i)).toBeInTheDocument()
})

test("retries the selected node slot request after a list failure", async () => {
  getSlotsMock
    .mockRejectedValueOnce(new ManagerApiError(503, "service_unavailable", "slot leader unavailable"))
    .mockResolvedValueOnce({ total: 1, items: [slotRow] })

  const user = userEvent.setup()
  renderSlotsPage()

  expect(await screen.findByText(/currently unavailable/i)).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Retry" }))

  expect(getSlotsMock).toHaveBeenLastCalledWith({ nodeId: 1 })
  expect(await screen.findByText("Slot 9")).toBeInTheDocument()
})

test("shows conflict feedback when slot rebalance is rejected", async () => {
  getSlotsMock.mockResolvedValue({ total: 1, items: [slotRow] })
  rebalanceSlotsMock.mockRejectedValueOnce(
    new ManagerApiError(409, "conflict", "slot migrations already in progress"),
  )

  const user = userEvent.setup()
  renderSlotsPage()

  expect(await screen.findByText("Slot 9")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Rebalance slots" }))
  await user.click(screen.getByRole("button", { name: "Confirm" }))

  expect(await screen.findByText("slot migrations already in progress")).toBeInTheDocument()
})

test("shows conflict feedback when slot remove is rejected", async () => {
  getSlotsMock.mockResolvedValueOnce({ total: 1, items: [slotRow] })
  getSlotMock.mockResolvedValueOnce(slotDetail)
  removeSlotMock.mockRejectedValueOnce(
    new ManagerApiError(409, "conflict", "slot migrations already in progress"),
  )

  const user = userEvent.setup()
  renderSlotsPage()

  expect(await screen.findByText("Slot 9")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Inspect slot 9" }))
  expect(await screen.findByText("Desired peers")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Remove slot" }))
  await user.click(screen.getByRole("button", { name: "Confirm" }))

  expect(await screen.findByText("slot migrations already in progress")).toBeInTheDocument()
})

test("shows translated Chinese validation when the transfer target is invalid", async () => {
  localStorage.setItem("wukongim_manager_locale", "zh-CN")
  getSlotsMock.mockResolvedValueOnce({ total: 1, items: [slotRow] })
  getSlotMock.mockResolvedValueOnce(slotDetail)

  const user = userEvent.setup()
  renderSlotsPage()

  expect(await screen.findByText("槽位 9")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "查看槽位 9" }))
  await user.click(screen.getByRole("button", { name: "转移 Leader" }))
  await user.click(screen.getByRole("button", { name: "转移" }))

  expect(await screen.findByText("请输入有效的目标节点 ID。")).toBeInTheDocument()
})
