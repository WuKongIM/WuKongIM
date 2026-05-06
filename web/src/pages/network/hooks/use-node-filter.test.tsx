import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { MemoryRouter } from "react-router-dom"
import { expect, test } from "vitest"

import { useNodeFilter } from "./use-node-filter"

function Harness() {
  const filter = useNodeFilter()
  return (
    <div>
      <div data-testid="nodes">{filter.selectedNodes.join(",") || "all"}</div>
      <div data-testid="range">{filter.timeRange}</div>
      <div data-testid="auto">{String(filter.autoRefresh)}</div>
      <button onClick={() => filter.setSelectedNodes([2, 3])}>nodes</button>
      <button onClick={() => filter.setTimeRange("15m")}>range</button>
      <button onClick={filter.toggleAutoRefresh}>auto</button>
    </div>
  )
}

test("reads and writes network filter state through URL params", async () => {
  const user = userEvent.setup()
  render(<Harness />, { wrapper: ({ children }) => <MemoryRouter initialEntries={["/network?nodes=2&range=5m&autoRefresh=1"]}>{children}</MemoryRouter> })

  expect(screen.getByTestId("nodes")).toHaveTextContent("2")
  expect(screen.getByTestId("range")).toHaveTextContent("5m")
  expect(screen.getByTestId("auto")).toHaveTextContent("true")

  await user.click(screen.getByRole("button", { name: "nodes" }))
  await user.click(screen.getByRole("button", { name: "range" }))
  await user.click(screen.getByRole("button", { name: "auto" }))

  expect(screen.getByTestId("nodes")).toHaveTextContent("2,3")
  expect(screen.getByTestId("range")).toHaveTextContent("15m")
  expect(screen.getByTestId("auto")).toHaveTextContent("false")
})
