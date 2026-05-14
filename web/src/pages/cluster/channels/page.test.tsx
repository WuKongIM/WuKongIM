import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { MemoryRouter, Route, Routes } from "react-router-dom"
import { beforeEach, expect, test, vi } from "vitest"

import { I18nProvider } from "@/i18n/provider"
import { resetLocale } from "@/i18n/locale-store"
import { ClusterChannelsPage } from "@/pages/cluster/channels/page"

const getChannelClusterSummaryMock = vi.fn()
const getChannelRuntimeMetaMock = vi.fn()
const getChannelClusterUnhealthyMock = vi.fn()
const getNodesMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getChannelClusterSummary: (...args: unknown[]) => getChannelClusterSummaryMock(...args),
    getChannelRuntimeMeta: (...args: unknown[]) => getChannelRuntimeMetaMock(...args),
    getChannelClusterUnhealthy: (...args: unknown[]) => getChannelClusterUnhealthyMock(...args),
    getNodes: (...args: unknown[]) => getNodesMock(...args),
  }
})

function renderPage(path = "/cluster/channels") {
  return render(
    <I18nProvider>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/cluster/channels" element={<ClusterChannelsPage />} />
        </Routes>
      </MemoryRouter>
    </I18nProvider>,
  )
}

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  getChannelClusterSummaryMock.mockReset()
  getChannelRuntimeMetaMock.mockReset()
  getChannelClusterUnhealthyMock.mockReset()
  getNodesMock.mockReset()
  getChannelClusterSummaryMock.mockResolvedValue({
    total: 1,
    healthy: 1,
    isr_insufficient: 0,
    no_leader: 0,
    avg_replicas: 1,
    avg_isr: 1,
    leader_distribution: [{ node_id: 1, count: 1 }],
  })
  getChannelRuntimeMetaMock.mockResolvedValue({
    items: [{
      channel_id: "alpha",
      channel_type: 1,
      slot_id: 9,
      channel_epoch: 11,
      leader_epoch: 5,
      leader: 2,
      replicas: [1, 2, 3],
      isr: [1, 2],
      min_isr: 2,
      status: "active",
    }],
    has_more: false,
  })
  getChannelClusterUnhealthyMock.mockResolvedValue({ items: [], next_cursor: "", has_more: false })
  getNodesMock.mockResolvedValue({
    total: 1,
    items: [{
      node_id: 1,
      addr: "127.0.0.1:7000",
      status: "alive",
      last_heartbeat_at: "2026-04-23T08:00:00Z",
      is_local: true,
      capacity_weight: 1,
      controller: { role: "leader" },
      slot_stats: { count: 1, leader_count: 1 },
    }],
  })
})

test("defaults to the channel cluster overview tab", async () => {
  renderPage()

  expect(screen.getByRole("tab", { name: "Overview" })).toHaveAttribute("aria-selected", "true")
  expect((await screen.findAllByText("Channel Cluster")).length).toBeGreaterThan(0)
  expect(await screen.findByText("CLUSTER / CHANNELS")).toBeInTheDocument()
})

test("renders the runtime list tab from the tab search param", async () => {
  renderPage("/cluster/channels?tab=list")

  expect(screen.getByRole("tab", { name: "List" })).toHaveAttribute("aria-selected", "true")
  expect(await screen.findByText("Channel ID")).toBeInTheDocument()
})

test("renders the unhealthy tab from the tab search param", async () => {
  renderPage("/cluster/channels?tab=unhealthy")

  expect(screen.getByRole("tab", { name: "Unhealthy" })).toHaveAttribute("aria-selected", "true")
  expect(await screen.findByText("Unhealthy Channels")).toBeInTheDocument()
})

test("tab clicks update the selected tab", async () => {
  const user = userEvent.setup()
  renderPage()

  await user.click(screen.getByRole("tab", { name: "List" }))

  expect(screen.getByRole("tab", { name: "List" })).toHaveAttribute("aria-selected", "true")
  expect(await screen.findByText("Channel ID")).toBeInTheDocument()
})
