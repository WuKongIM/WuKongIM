import { render, screen } from "@testing-library/react"
import { MemoryRouter, Route, Routes } from "react-router-dom"
import { beforeEach, expect, test, vi } from "vitest"

import { I18nProvider } from "@/i18n/provider"
import { resetLocale } from "@/i18n/locale-store"
import { ClusterChannelsPage } from "@/pages/cluster/channels/page"

const getChannelClusterSummaryMock = vi.fn()
const getChannelRuntimeMetaMock = vi.fn()
const getChannelClusterUnhealthyMock = vi.fn()
const getNodesMock = vi.fn()
const getBusinessChannelsMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getChannelClusterSummary: (...args: unknown[]) => getChannelClusterSummaryMock(...args),
    getChannelRuntimeMeta: (...args: unknown[]) => getChannelRuntimeMetaMock(...args),
    getChannelClusterUnhealthy: (...args: unknown[]) => getChannelClusterUnhealthyMock(...args),
    getNodes: (...args: unknown[]) => getNodesMock(...args),
    getBusinessChannels: (...args: unknown[]) => getBusinessChannelsMock(...args),
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
  getBusinessChannelsMock.mockReset()
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
  getBusinessChannelsMock.mockResolvedValue({
    items: [{
      channel_id: "alpha",
      channel_type: 1,
      slot_id: 9,
      hash_slot: 3,
      ban: false,
      disband: false,
      send_ban: true,
      subscriber_mutation_version: 7,
    }],
    has_more: false,
  })
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

test("defaults to the channel cluster list without overview or unhealthy tabs", async () => {
  renderPage()

  expect(screen.queryByRole("tab", { name: "Overview" })).not.toBeInTheDocument()
  expect(screen.queryByRole("tab", { name: "Unhealthy" })).not.toBeInTheDocument()
  expect(screen.queryByRole("tab", { name: "List" })).not.toBeInTheDocument()
  expect(await screen.findByText("alpha")).toBeInTheDocument()
  expect(await screen.findByText("CLUSTER / CHANNELS")).toBeInTheDocument()
  expect(getChannelRuntimeMetaMock).toHaveBeenCalledWith({
    nodeId: 1,
    limit: 50,
    includeMaxMessageSeq: true,
  })
  expect(getBusinessChannelsMock).not.toHaveBeenCalled()
  expect(getChannelClusterSummaryMock).not.toHaveBeenCalled()
  expect(getChannelClusterUnhealthyMock).not.toHaveBeenCalled()
})

test("keeps legacy list tab URLs on the list page", async () => {
  renderPage("/cluster/channels?tab=list")

  expect(screen.queryByRole("tab")).not.toBeInTheDocument()
  expect(await screen.findByText("Channel ID")).toBeInTheDocument()
  expect(await screen.findByText("alpha")).toBeInTheDocument()
})
