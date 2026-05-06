import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeEach, expect, test, vi } from "vitest"

import { networkSummaryFixture } from "@/pages/network/test-fixtures"
import { useNetworkData } from "./use-network-data"

const getNetworkSummaryMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return { ...actual, getNetworkSummary: (...args: unknown[]) => getNetworkSummaryMock(...args) }
})

function Harness({ autoRefresh = false, selectedNodes = [] }: { autoRefresh?: boolean; selectedNodes?: number[] }) {
  const state = useNetworkData({ autoRefresh, selectedNodes })
  return (
    <div>
      <div data-testid="loading">{String(state.loading)}</div>
      <div data-testid="alive">{state.filteredData?.health.alive ?? "none"}</div>
      <button onClick={() => { void state.refresh() }}>refresh</button>
    </div>
  )
}

beforeEach(() => {
  getNetworkSummaryMock.mockReset()
})

test("fetches and aggregates network summary", async () => {
  getNetworkSummaryMock.mockResolvedValue(networkSummaryFixture)

  render(<Harness selectedNodes={[2]} />)

  expect(screen.getByTestId("loading")).toHaveTextContent("true")
  expect(await screen.findByTestId("alive")).toHaveTextContent("1")
})

test("manual refresh fetches again", async () => {
  getNetworkSummaryMock.mockResolvedValue(networkSummaryFixture)
  const user = userEvent.setup()

  render(<Harness />)

  await screen.findByText("2")
  await user.click(screen.getByRole("button", { name: "refresh" }))

  await waitFor(() => expect(getNetworkSummaryMock).toHaveBeenCalledTimes(2))
})
