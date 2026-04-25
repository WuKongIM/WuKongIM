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
const transferSlotLeaderMock = vi.fn()
const recoverSlotMock = vi.fn()
const rebalanceSlotsMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getSlots: (...args: unknown[]) => getSlotsMock(...args),
    getSlot: (...args: unknown[]) => getSlotMock(...args),
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
})

function renderSlotsPage() {
  return render(
    <I18nProvider>
      <SlotsPage />
    </I18nProvider>,
  )
}

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

test("renders unavailable state when the slot list request fails", async () => {
  getSlotsMock.mockRejectedValueOnce(
    new ManagerApiError(503, "service_unavailable", "slot leader unavailable"),
  )

  renderSlotsPage()

  expect(await screen.findByText(/currently unavailable/i)).toBeInTheDocument()
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
