import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom"
import { beforeEach, expect, test, vi } from "vitest"

import { I18nProvider } from "@/i18n/provider"
import { resetLocale } from "@/i18n/locale-store"
import { ManagerApiError } from "@/lib/manager-api"
import { ChannelClusterUnhealthyPage } from "@/pages/channel-cluster/unhealthy/page"

const getChannelClusterUnhealthyMock = vi.fn()
const getChannelClusterReplicasMock = vi.fn()
const repairChannelClusterLeaderMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getChannelClusterUnhealthy: (...args: unknown[]) => getChannelClusterUnhealthyMock(...args),
    getChannelClusterReplicas: (...args: unknown[]) => getChannelClusterReplicasMock(...args),
    repairChannelClusterLeader: (...args: unknown[]) => repairChannelClusterLeaderMock(...args),
  }
})

const unhealthyRow = {
  channel_id: "room-1",
  channel_type: 2,
  slot_id: 9,
  channel_epoch: 7,
  leader_epoch: 3,
  leader: 0,
  replicas: [1, 2, 3],
  isr: [2],
  min_isr: 2,
  max_message_seq: 42,
  status: "active",
  reasons: ["isr_insufficient", "no_leader"],
}

const secondUnhealthyRow = {
  ...unhealthyRow,
  channel_id: "room-2",
  slot_id: 10,
  leader: 3,
  isr: [1, 3],
  status: "deleting",
  reasons: ["status_not_active"],
}

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  getChannelClusterUnhealthyMock.mockReset()
  getChannelClusterReplicasMock.mockReset()
  repairChannelClusterLeaderMock.mockReset()
})

function LocationProbe() {
  const location = useLocation()
  return <div>{`${location.pathname}${location.search}`}</div>
}

function renderUnhealthyPage() {
  return render(
    <I18nProvider>
      <MemoryRouter initialEntries={["/channel-cluster/unhealthy"]}>
        <Routes>
          <Route path="/channel-cluster/unhealthy" element={<ChannelClusterUnhealthyPage />} />
          <Route path="/channel-cluster/list" element={<LocationProbe />} />
        </Routes>
      </MemoryRouter>
    </I18nProvider>,
  )
}

test("renders unhealthy channel rows with reason tags and inspect links", async () => {
  getChannelClusterUnhealthyMock.mockResolvedValueOnce({
    items: [unhealthyRow],
    has_more: false,
  })

  const user = userEvent.setup()
  renderUnhealthyPage()

  expect(await screen.findByRole("heading", { name: "Unhealthy Channels" })).toBeInTheDocument()
  const row = screen.getByRole("row", { name: /room-1/ })
  expect(within(row).getByText("room-1")).toBeInTheDocument()
  expect(within(row).getByText("2")).toBeInTheDocument()
  expect(within(row).getByText("9")).toBeInTheDocument()
  expect(within(row).getByText("0")).toBeInTheDocument()
  expect(within(row).getByText("1, 2, 3")).toBeInTheDocument()
  expect(within(row).getByText("2 / min 2")).toBeInTheDocument()
  expect(within(row).getByText("42")).toBeInTheDocument()
  expect(within(row).getByText("active")).toBeInTheDocument()
  expect(within(row).getByText("ISR insufficient")).toBeInTheDocument()
  expect(within(row).getByText("No leader")).toBeInTheDocument()

  await user.click(within(row).getByRole("link", { name: "Inspect channel room-1" }))

  expect(await screen.findByText("/channel-cluster/list?channel_id=room-1&channel_type=2")).toBeInTheDocument()
})

test("loads additional unhealthy pages with the returned cursor", async () => {
  getChannelClusterUnhealthyMock.mockResolvedValueOnce({
    items: [unhealthyRow],
    has_more: true,
    next_cursor: "cursor-2",
  })
  getChannelClusterUnhealthyMock.mockResolvedValueOnce({
    items: [secondUnhealthyRow],
    has_more: false,
  })

  const user = userEvent.setup()
  renderUnhealthyPage()

  expect(await screen.findByText("room-1")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Load more" }))

  expect(await screen.findByText("room-2")).toBeInTheDocument()
  expect(getChannelClusterUnhealthyMock).toHaveBeenNthCalledWith(2, { cursor: "cursor-2" })
})

test("refresh clears pagination and reloads the first unhealthy page", async () => {
  getChannelClusterUnhealthyMock.mockResolvedValueOnce({
    items: [unhealthyRow],
    has_more: true,
    next_cursor: "cursor-2",
  })
  getChannelClusterUnhealthyMock.mockResolvedValueOnce({
    items: [secondUnhealthyRow],
    has_more: false,
  })

  const user = userEvent.setup()
  renderUnhealthyPage()

  expect(await screen.findByText("room-1")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Refresh" }))

  expect(await screen.findByText("room-2")).toBeInTheDocument()
  expect(screen.queryByText("room-1")).not.toBeInTheDocument()
  expect(getChannelClusterUnhealthyMock).toHaveBeenNthCalledWith(2, {})
})

test("renders healthy empty state when no unhealthy channels are returned", async () => {
  getChannelClusterUnhealthyMock.mockResolvedValueOnce({
    items: [],
    has_more: false,
  })

  renderUnhealthyPage()

  expect(await screen.findByText("All scanned channels are healthy.")).toBeInTheDocument()
})

test("renders unavailable state when unhealthy channels cannot be loaded", async () => {
  getChannelClusterUnhealthyMock.mockRejectedValueOnce(new ManagerApiError(503, "service_unavailable", "unavailable"))

  renderUnhealthyPage()

  expect(await screen.findByText(/currently unavailable/i)).toBeInTheDocument()
})


test("inspects replica detail with reported and unknown values", async () => {
  getChannelClusterUnhealthyMock.mockResolvedValueOnce({ items: [unhealthyRow], has_more: false })
  getChannelClusterReplicasMock.mockResolvedValueOnce({
    channel: { ...unhealthyRow, hash_slot: 123, features: 0, lease_until_ms: 0 },
    runtime_reported: true,
    commit_seq: 42,
    min_available_seq: 1,
    retention_through_seq: 0,
    replicas: [
      { node_id: 1, role: "leader", is_leader: true, in_isr: true, reported: true, commit_seq: 42, leo: null, checkpoint_hw: null, lag: 0 },
      { node_id: 2, role: "follower", is_leader: false, in_isr: false, reported: false, commit_seq: null, leo: null, checkpoint_hw: null, lag: null },
    ],
  })

  const user = userEvent.setup()
  renderUnhealthyPage()

  const row = await screen.findByRole("row", { name: /room-1/ })
  await user.click(within(row).getByRole("button", { name: "Inspect replicas" }))

  expect(getChannelClusterReplicasMock).toHaveBeenCalledWith(2, "room-1")
  expect(await screen.findByText(/Runtime reported/)).toBeInTheDocument()
  const leaderRow = screen.getByRole("row", { name: /Node 1/ })
  expect(within(leaderRow).getByText("leader")).toBeInTheDocument()
  expect(within(leaderRow).getByText("Reported")).toBeInTheDocument()
  expect(within(leaderRow).getByText("42")).toBeInTheDocument()
  expect(within(leaderRow).getByText("0")).toBeInTheDocument()
  const followerRow = screen.getByRole("row", { name: /Node 2/ })
  expect(within(followerRow).getByText("follower")).toBeInTheDocument()
  expect(within(followerRow).getByText("Not reported")).toBeInTheDocument()
  expect(within(followerRow).getAllByText("-").length).toBeGreaterThan(0)
})

test("shows repair leader only for no leader rows", async () => {
  getChannelClusterUnhealthyMock.mockResolvedValueOnce({
    items: [unhealthyRow, { ...secondUnhealthyRow, reasons: ["isr_insufficient"] }],
    has_more: false,
  })

  renderUnhealthyPage()

  const noLeaderRow = await screen.findByRole("row", { name: /room-1/ })
  expect(within(noLeaderRow).getByRole("button", { name: "Repair leader" })).toBeInTheDocument()
  const isrOnlyRow = screen.getByRole("row", { name: /room-2/ })
  expect(within(isrOnlyRow).queryByRole("button", { name: "Repair leader" })).not.toBeInTheDocument()
})

test("repairs no leader rows and refreshes the first page", async () => {
  getChannelClusterUnhealthyMock.mockResolvedValueOnce({ items: [unhealthyRow], has_more: false })
  repairChannelClusterLeaderMock.mockResolvedValueOnce({
    changed: true,
    channel: { ...unhealthyRow, hash_slot: 123, features: 0, lease_until_ms: 0, leader: 2 },
  })
  getChannelClusterUnhealthyMock.mockResolvedValueOnce({ items: [secondUnhealthyRow], has_more: false })

  const user = userEvent.setup()
  renderUnhealthyPage()

  await user.click(within(await screen.findByRole("row", { name: /room-1/ })).getByRole("button", { name: "Repair leader" }))

  expect(repairChannelClusterLeaderMock).toHaveBeenCalledWith(2, "room-1", { reason: "no_leader" })
  expect(await screen.findByText("Leader repaired to node 2." )).toBeInTheDocument()
  expect(getChannelClusterUnhealthyMock).toHaveBeenNthCalledWith(2, {})
  expect(await screen.findByText("room-2")).toBeInTheDocument()
})

test("keeps row visible when repair finds no safe candidate", async () => {
  getChannelClusterUnhealthyMock.mockResolvedValueOnce({ items: [unhealthyRow], has_more: false })
  repairChannelClusterLeaderMock.mockRejectedValueOnce(new ManagerApiError(409, "conflict", "no safe candidate"))

  const user = userEvent.setup()
  renderUnhealthyPage()

  await user.click(within(await screen.findByRole("row", { name: /room-1/ })).getByRole("button", { name: "Repair leader" }))

  expect(await screen.findByText("No safe leader candidate." )).toBeInTheDocument()
  expect(screen.getByText("room-1")).toBeInTheDocument()
})

test("renders replica detail unavailable state", async () => {
  getChannelClusterUnhealthyMock.mockResolvedValueOnce({ items: [unhealthyRow], has_more: false })
  getChannelClusterReplicasMock.mockRejectedValueOnce(new ManagerApiError(503, "service_unavailable", "unavailable"))

  const user = userEvent.setup()
  renderUnhealthyPage()

  await user.click(within(await screen.findByRole("row", { name: /room-1/ })).getByRole("button", { name: "Inspect replicas" }))

  expect(await screen.findByText(/currently unavailable/i)).toBeInTheDocument()
})
