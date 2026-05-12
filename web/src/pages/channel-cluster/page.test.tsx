import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { MemoryRouter } from "react-router-dom"
import { beforeEach, expect, test, vi } from "vitest"

import { I18nProvider } from "@/i18n/provider"
import { resetLocale } from "@/i18n/locale-store"
import { ManagerApiError } from "@/lib/manager-api"
import { ChannelClusterPage } from "@/pages/channel-cluster/page"

const getChannelClusterSummaryMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getChannelClusterSummary: (...args: unknown[]) => getChannelClusterSummaryMock(...args),
  }
})

const summaryFixture = {
  total: 4,
  healthy: 1,
  isr_insufficient: 2,
  no_leader: 1,
  avg_replicas: 2,
  avg_isr: 1.5,
  leader_distribution: [
    { node_id: 1, count: 3 },
    { node_id: 2, count: 1 },
  ],
}

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  getChannelClusterSummaryMock.mockReset()
})

function renderChannelClusterPage() {
  return render(
    <I18nProvider>
      <MemoryRouter initialEntries={["/channel-cluster"]}>
        <ChannelClusterPage />
      </MemoryRouter>
    </I18nProvider>,
  )
}

test("renders channel cluster summary metrics and leader distribution", async () => {
  getChannelClusterSummaryMock.mockResolvedValueOnce(summaryFixture)

  renderChannelClusterPage()

  expect(await screen.findByRole("heading", { name: "Channel Cluster Overview" })).toBeInTheDocument()
  expect(screen.getByText("Total channels")).toBeInTheDocument()
  expect(screen.getByText("Healthy channels")).toBeInTheDocument()
  expect(screen.getByText("ISR insufficient")).toBeInTheDocument()
  expect(screen.getByText("No leader")).toBeInTheDocument()
  expect(screen.getByText("Average replicas")).toBeInTheDocument()
  expect(screen.getByText("Average ISR")).toBeInTheDocument()
  expect(screen.getByText("Leader distribution")).toBeInTheDocument()
  expect(screen.getByText("Node 1")).toBeInTheDocument()
  expect(screen.getByText("3 channels")).toBeInTheDocument()
  expect(screen.getByRole("link", { name: "Open unhealthy channels" })).toHaveAttribute(
    "href",
    "/channel-cluster/unhealthy",
  )
})

test("refreshes channel cluster summary", async () => {
  getChannelClusterSummaryMock.mockResolvedValueOnce(summaryFixture)
  getChannelClusterSummaryMock.mockResolvedValueOnce({ ...summaryFixture, healthy: 2 })

  const user = userEvent.setup()
  renderChannelClusterPage()

  expect(await screen.findByText("Total channels")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Refresh" }))

  expect(getChannelClusterSummaryMock).toHaveBeenCalledTimes(2)
  expect(await screen.findByText("2")).toBeInTheDocument()
})

test("renders forbidden state when channel cluster summary is denied", async () => {
  getChannelClusterSummaryMock.mockRejectedValueOnce(new ManagerApiError(403, "forbidden", "forbidden"))

  renderChannelClusterPage()

  expect(await screen.findByText(/permission/i)).toBeInTheDocument()
})

test("renders empty state when there are no channel runtime records", async () => {
  getChannelClusterSummaryMock.mockResolvedValueOnce({
    ...summaryFixture,
    total: 0,
    healthy: 0,
    isr_insufficient: 0,
    no_leader: 0,
    avg_replicas: 0,
    avg_isr: 0,
    leader_distribution: [],
  })

  renderChannelClusterPage()

  expect(await screen.findByText("No channel runtime metadata is available yet.")).toBeInTheDocument()
})
