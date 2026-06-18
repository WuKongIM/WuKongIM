import { render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeEach, expect, test, vi } from "vitest"

import { createAnonymousAuthState, useAuthStore } from "@/auth/auth-store"
import { I18nProvider } from "@/i18n/provider"
import { resetLocale } from "@/i18n/locale-store"
import { SlotLogsPage } from "@/pages/slot-logs/page"

const getNodesMock = vi.fn()
const getSlotsMock = vi.fn()
const getSlotLogsMock = vi.fn()
const compactSlotRaftLogOnNodeMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getNodes: (...args: unknown[]) => getNodesMock(...args),
    getSlots: (...args: unknown[]) => getSlotsMock(...args),
    getSlotLogs: (...args: unknown[]) => getSlotLogsMock(...args),
    compactSlotRaftLogOnNode: (...args: unknown[]) => compactSlotRaftLogOnNodeMock(...args),
  }
})

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

const slotRow = {
  slot_id: 9,
  state: { quorum: "ready", sync: "in_sync" },
  assignment: { desired_peers: [1, 2, 3], config_epoch: 7, balance_version: 4 },
  runtime: {
    current_peers: [1, 2, 3],
    leader_id: 1,
    healthy_voters: 3,
    has_quorum: true,
    observed_config_epoch: 7,
    last_report_at: "2026-04-23T08:00:00Z",
  },
}

function slotLogsPage(firstIndex: number) {
  return {
    node_id: 1,
    slot_id: 9,
    first_index: firstIndex,
    last_index: 12,
    commit_index: 12,
    applied_index: 10,
    items: [{
      index: 12,
      term: 3,
      type: "normal",
      data_size: 16,
      decode_status: "ok",
      decoded_type: "slot_config",
      decoded: { command: "slot_config", slot_id: 9 },
    }],
  }
}

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  getNodesMock.mockReset()
  getSlotsMock.mockReset()
  getSlotLogsMock.mockReset()
  compactSlotRaftLogOnNodeMock.mockReset()

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
  getNodesMock.mockResolvedValue({ total: 1, items: [nodeRow] })
  getSlotsMock.mockResolvedValue({ total: 1, items: [slotRow] })
  getSlotLogsMock.mockResolvedValue(slotLogsPage(1))
})

test("triggers node-scoped slot log compaction and refreshes current logs", async () => {
  getSlotLogsMock
    .mockResolvedValueOnce(slotLogsPage(1))
    .mockResolvedValueOnce(slotLogsPage(6))
  compactSlotRaftLogOnNodeMock.mockResolvedValueOnce({
    generated_at: "2026-05-08T10:03:00Z",
    total: 1,
    succeeded: 1,
    failed: 0,
    items: [{
      node_id: 1,
      slot_id: 9,
      success: true,
      applied_index: 10,
      before_snapshot_index: 4,
      after_snapshot_index: 10,
      compacted: true,
      skipped_reason: "",
      error: "",
    }],
  })

  const user = userEvent.setup()
  renderSlotLogsPage()

  expect(await screen.findByText("slot_config")).toBeInTheDocument()

  await user.click(screen.getByRole("button", { name: "Compact logs" }))
  await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Compact logs" }))

  await waitFor(() => expect(compactSlotRaftLogOnNodeMock).toHaveBeenCalledWith(1, 9))
  expect(await screen.findByText("snapshot 4 → 10 at applied 10")).toBeInTheDocument()
  await waitFor(() => expect(getSlotLogsMock).toHaveBeenCalledTimes(2))
  expect(getSlotLogsMock).toHaveBeenLastCalledWith(9, { nodeId: 1, limit: 50, cursor: undefined })
})

test("disables slot log compaction without both node and slot write permissions", async () => {
  useAuthStore.setState({
    permissions: [{ resource: "cluster.slot", actions: ["r", "w"] }],
  })

  renderSlotLogsPage()

  expect(await screen.findByText("slot_config")).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Compact logs" })).toBeDisabled()
  expect(screen.getByText("Requires cluster.node and cluster.slot write permissions.")).toBeInTheDocument()
})

test("renders slot log entry creation time", async () => {
  const createdAtMS = Date.UTC(2026, 5, 18, 1, 10, 11, 123)
  const formattedCreatedAt = new Intl.DateTimeFormat("en", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  }).format(new Date(createdAtMS))
  getSlotLogsMock.mockResolvedValueOnce({
    ...slotLogsPage(1),
    items: [{
      index: 12,
      term: 3,
      type: "normal",
      created_at_ms: createdAtMS,
      data_size: 16,
      decode_status: "ok",
      decoded_type: "slot_config",
      decoded: { command: "slot_config", slot_id: 9 },
    }],
  })

  renderSlotLogsPage()

  expect(await screen.findByText("Created at")).toBeInTheDocument()
  expect(screen.getByText(formattedCreatedAt)).toBeInTheDocument()
})

function renderSlotLogsPage() {
  return render(
    <I18nProvider>
      <SlotLogsPage />
    </I18nProvider>,
  )
}
