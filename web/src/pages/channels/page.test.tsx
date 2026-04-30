import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom"
import { beforeEach, expect, test, vi } from "vitest"

import { createAnonymousAuthState, useAuthStore } from "@/auth/auth-store"
import { I18nProvider } from "@/i18n/provider"
import { resetLocale } from "@/i18n/locale-store"
import { ManagerApiError } from "@/lib/manager-api"
import { ChannelsPage } from "@/pages/channels/page"

const getChannelRuntimeMetaMock = vi.fn()
const getChannelRuntimeMetaDetailMock = vi.fn()
const getNodesMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getChannelRuntimeMeta: (...args: unknown[]) => getChannelRuntimeMetaMock(...args),
    getChannelRuntimeMetaDetail: (...args: unknown[]) => getChannelRuntimeMetaDetailMock(...args),
    getNodes: (...args: unknown[]) => getNodesMock(...args),
  }
})

const channelRow = {
  channel_id: "alpha",
  channel_type: 1,
  slot_id: 9,
  channel_epoch: 11,
  leader_epoch: 5,
  leader: 2,
  replicas: [1, 2, 3],
  isr: [1, 2],
  min_isr: 2,
  max_message_seq: 42,
  status: "active",
}

const secondChannelRow = {
  ...channelRow,
  channel_id: "beta",
  slot_id: 11,
}

const channelDetail = {
  ...channelRow,
  hash_slot: 3,
  features: 7,
  lease_until_ms: 1713859200000,
}

const nodeRow = {
  node_id: 1,
  name: "node-1",
  addr: "127.0.0.1:7001",
  status: "alive",
  last_heartbeat_at: "2026-04-23T08:00:00Z",
  is_local: false,
  capacity_weight: 1,
  controller: { role: "follower", voter: true, leader_id: 2 },
  slot_stats: { count: 1, leader_count: 0 },
}

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  getChannelRuntimeMetaMock.mockReset()
  getChannelRuntimeMetaDetailMock.mockReset()
  getNodesMock.mockReset()
  getNodesMock.mockResolvedValue({
    generated_at: "2026-04-23T08:00:00Z",
    controller_leader_id: 2,
    total: 2,
    items: [
      nodeRow,
      { ...nodeRow, node_id: 2, name: "node-2", is_local: true, controller: { role: "leader", voter: true, leader_id: 2 } },
    ],
  })
  useAuthStore.setState({
    ...createAnonymousAuthState(),
    isHydrated: true,
    status: "authenticated",
    username: "admin",
    tokenType: "Bearer",
    accessToken: "token-1",
    expiresAt: "2099-04-22T12:00:00Z",
    permissions: [{ resource: "cluster.channel", actions: ["r"] }],
  })
})

function renderChannelsPage() {
  return render(
    <I18nProvider>
      <MemoryRouter initialEntries={["/channels"]}>
        <Routes>
          <Route path="/channels" element={<ChannelsPage />} />
          <Route path="/messages" element={<LocationProbe />} />
        </Routes>
      </MemoryRouter>
    </I18nProvider>,
  )
}

function LocationProbe() {
  const location = useLocation()
  return <div>{`${location.pathname}${location.search}`}</div>
}

test("uses compact channel page chrome without summary cards", async () => {
  getChannelRuntimeMetaMock.mockResolvedValueOnce({
    items: [channelRow],
    has_more: false,
  })

  renderChannelsPage()

  expect(await screen.findByText("alpha")).toBeInTheDocument()
  expect(screen.getByText("Loaded: 1")).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Refresh" })).toBeInTheDocument()
  expect(screen.queryByText("Scope: all channels")).not.toBeInTheDocument()
  expect(screen.queryByText("Channel lists and runtime drill-in status.")).not.toBeInTheDocument()
  expect(screen.queryByText("Loaded channels")).not.toBeInTheDocument()
  expect(screen.queryByText("Distinct physical slots represented in view.")).not.toBeInTheDocument()
  expect(screen.queryByText("Paged runtime metadata from the channel manager endpoints.")).not.toBeInTheDocument()
  expect(screen.queryByText("Inspect one channel to view slot ownership and runtime lease metadata.")).not.toBeInTheDocument()
})

test("renders channel runtime rows and opens messages query", async () => {
  getChannelRuntimeMetaMock.mockResolvedValueOnce({
    items: [channelRow],
    has_more: false,
  })
  getChannelRuntimeMetaDetailMock.mockResolvedValueOnce(channelDetail)

  const user = userEvent.setup()
  renderChannelsPage()

  expect(await screen.findByText("alpha")).toBeInTheDocument()
  expect(screen.getByText("1, 2, 3")).toBeInTheDocument()
  expect(screen.getByText("42")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "View channel alpha messages" }))

  expect(await screen.findByText("/messages?channel_id=alpha&channel_type=1")).toBeInTheDocument()
})

test("defaults channel node filter to the local node and reloads when it changes", async () => {
  getChannelRuntimeMetaMock.mockResolvedValueOnce({
    items: [channelRow],
    has_more: false,
  })
  getChannelRuntimeMetaMock.mockResolvedValueOnce({
    items: [secondChannelRow],
    has_more: false,
  })

  const user = userEvent.setup()
  renderChannelsPage()

  const filter = await screen.findByLabelText("Node filter")
  expect(filter).toHaveValue("2")
  expect(await screen.findByText("alpha")).toBeInTheDocument()
  expect(getChannelRuntimeMetaMock).toHaveBeenCalledWith({ nodeId: 2 })

  await user.selectOptions(filter, "1")

  expect(await screen.findByText("beta")).toBeInTheDocument()
  expect(getChannelRuntimeMetaMock).toHaveBeenLastCalledWith({ nodeId: 1 })
})

test("opens channel detail from manager APIs", async () => {
  getChannelRuntimeMetaMock.mockResolvedValueOnce({
    items: [channelRow],
    has_more: false,
  })
  getChannelRuntimeMetaDetailMock.mockResolvedValueOnce(channelDetail)

  const user = userEvent.setup()
  renderChannelsPage()

  expect(await screen.findByText("alpha")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Inspect channel alpha" }))

  expect(await screen.findByText("Hash slot")).toBeInTheDocument()
  expect(getChannelRuntimeMetaDetailMock).toHaveBeenCalledWith(1, "alpha")
})

test("loads the next channel runtime page when more data is available", async () => {
  getChannelRuntimeMetaMock.mockResolvedValueOnce({
    items: [channelRow],
    has_more: true,
    next_cursor: "cursor-2",
  })
  getChannelRuntimeMetaMock.mockResolvedValueOnce({
    items: [secondChannelRow],
    has_more: false,
  })

  const user = userEvent.setup()
  renderChannelsPage()

  expect(await screen.findByText("alpha")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Load more" }))

  expect(await screen.findByText("beta")).toBeInTheDocument()
  expect(getChannelRuntimeMetaMock).toHaveBeenNthCalledWith(2, { nodeId: 2, cursor: "cursor-2" })
})

test("renders unavailable state when channel runtime data cannot be loaded", async () => {
  getChannelRuntimeMetaMock.mockRejectedValueOnce(
    new ManagerApiError(503, "service_unavailable", "slot leader authoritative read unavailable"),
  )

  renderChannelsPage()

  expect(await screen.findByText(/currently unavailable/i)).toBeInTheDocument()
})
