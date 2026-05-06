import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { MemoryRouter } from "react-router-dom"
import { beforeEach, expect, test, vi } from "vitest"

import { I18nProvider } from "@/i18n/provider"
import { resetLocale } from "@/i18n/locale-store"
import { ManagerApiError } from "@/lib/manager-api"
import { NetworkPage } from "@/pages/network/page"
import { networkSummaryFixture } from "@/pages/network/test-fixtures"

const getNetworkSummaryMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getNetworkSummary: (...args: unknown[]) => getNetworkSummaryMock(...args),
  }
})

function renderNetworkPage(initialEntry = "/network") {
  return render(
    <MemoryRouter initialEntries={[initialEntry]}>
      <I18nProvider>
        <NetworkPage />
      </I18nProvider>
    </MemoryRouter>,
  )
}

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  getNetworkSummaryMock.mockReset()
})

test("renders filter bar and focused network metric cards", async () => {
  getNetworkSummaryMock.mockResolvedValue(networkSummaryFixture)

  renderNetworkPage()

  expect(screen.getByRole("status")).toHaveAttribute("data-kind", "loading")
  expect(await screen.findByRole("heading", { name: "Network" })).toBeInTheDocument()
  expect(screen.getByLabelText("Node selector")).toBeInTheDocument()
  expect(screen.getByLabelText("Time range")).toHaveValue("1m")
  expect(screen.getByText("Local Node: 1")).toBeInTheDocument()
  expect(screen.getByText("Controller Leader: 2")).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Refresh" })).toBeInTheDocument()

  for (const title of [
    "Node Health Status",
    "Connection Pool Status",
    "RPC Call Latency",
    "RPC Success Rate",
    "Network Traffic",
    "Network Errors",
    "Message Type Distribution",
    "Recent Events",
  ]) {
    expect(screen.getByText(title)).toBeInTheDocument()
  }

  expect(screen.queryByText("Peer Pool Balance")).not.toBeInTheDocument()
  expect(screen.queryByText("Discovery & Config")).not.toBeInTheDocument()
})

test("filters dashboard cards by selected node and keeps time range controls visible", async () => {
  getNetworkSummaryMock.mockResolvedValue(networkSummaryFixture)
  const user = userEvent.setup()

  renderNetworkPage()

  expect(await screen.findByText("Connection Pool Status")).toBeInTheDocument()
  await user.selectOptions(screen.getByLabelText("Node selector"), ["2"])
  await user.selectOptions(screen.getByLabelText("Time range"), "15m")

  expect(screen.getByLabelText("Time range")).toHaveValue("15m")
  expect(screen.getByText("Selected nodes: node-2")).toBeInTheDocument()
  expect(screen.getByText("Cluster: 1/2")).toBeInTheDocument()
  expect(screen.getByText("Data Plane: 2/1")).toBeInTheDocument()
})

test("shows primary metrics for the eight dashboard cards", async () => {
  getNetworkSummaryMock.mockResolvedValue(networkSummaryFixture)

  renderNetworkPage()

  expect(await screen.findByText("Node Health Status")).toBeInTheDocument()
  expect(screen.getByText("2 / 3")).toBeInTheDocument()
  expect(screen.getByText("3 / 4")).toBeInTheDocument()
  expect(screen.getByText("7 ms")).toBeInTheDocument()
  expect(screen.getByText("97.5%")).toBeInTheDocument()
  expect(screen.getByText("192 bps")).toBeInTheDocument()
  expect(screen.getByText("6")).toBeInTheDocument()
  expect(screen.getByText("channel_long_poll_fetch")).toBeInTheDocument()
  expect(screen.getByText("1 event")).toBeInTheDocument()
})

test("opens a detail drawer from metric cards", async () => {
  getNetworkSummaryMock.mockResolvedValue(networkSummaryFixture)
  const user = userEvent.setup()

  renderNetworkPage()

  await user.click(await screen.findByLabelText(/RPC Call Latency/i))

  expect(screen.getByRole("dialog")).toBeInTheDocument()
  expect(screen.getByText("RPC Call Latency Details")).toBeInTheDocument()
  expect(screen.getByText("Service Breakdown")).toBeInTheDocument()
  expect(screen.getAllByText("channel_long_poll_fetch").length).toBeGreaterThan(0)
})

test("refreshes network summary", async () => {
  getNetworkSummaryMock.mockResolvedValue(networkSummaryFixture)

  const user = userEvent.setup()
  renderNetworkPage()

  await screen.findByText("Node Health Status")
  await user.click(screen.getByRole("button", { name: /refresh/i }))

  await waitFor(() => expect(getNetworkSummaryMock).toHaveBeenCalledTimes(2))
})

test("maps forbidden errors to ResourceState", async () => {
  getNetworkSummaryMock.mockRejectedValue(new ManagerApiError(403, "forbidden", "forbidden"))

  renderNetworkPage()

  expect(await screen.findByText("Forbidden")).toBeInTheDocument()
})

test("maps unavailable errors to ResourceState", async () => {
  getNetworkSummaryMock.mockRejectedValue(new ManagerApiError(503, "unavailable", "unavailable"))

  renderNetworkPage()

  expect(await screen.findByText("The manager service is currently unavailable.")).toBeInTheDocument()
  expect(screen.getByRole("status")).toHaveAttribute("data-kind", "unavailable")
})

test("renders single-node cluster badge", async () => {
  getNetworkSummaryMock.mockResolvedValue({
    ...networkSummaryFixture,
    headline: { ...networkSummaryFixture.headline, remote_peers: 0, alive_nodes: 1, draining_nodes: 0 },
    peers: [],
  })

  renderNetworkPage()

  expect((await screen.findAllByText("Single-node cluster")).length).toBeGreaterThan(0)
})
