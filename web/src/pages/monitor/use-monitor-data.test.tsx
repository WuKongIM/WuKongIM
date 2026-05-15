import { render, screen, waitFor } from "@testing-library/react"
import { beforeEach, expect, test, vi } from "vitest"

import { useMonitorData } from "./use-monitor-data"
import type { NodeId } from "./types"

const getMonitorMetricsMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return { ...actual, getMonitorMetrics: (...args: unknown[]) => getMonitorMetricsMock(...args) }
})

const monitorResponse = {
  generated_at: "2026-05-15T08:30:00Z",
  window_seconds: 10,
  step_seconds: 5,
  points: 2,
  scope: { view: "local_node", local_node_id: 1 },
  capabilities: { node_filter: false },
  nodes: [{ node_id: 1, name: "node-1", is_local: true, available: true }],
  metrics: {
    send_rate: {
      key: "send_rate",
      unit: "msg/s",
      latest: 8,
      peak: 8,
      avg: 6,
      points: [
        { at: "2026-05-15T08:29:50Z", value: 4 },
        { at: "2026-05-15T08:29:55Z", value: 8 },
      ],
    },
    online_connections: {
      key: "online_connections",
      unit: "connections",
      latest: 12,
      peak: 12,
      avg: 11,
      points: [
        { at: "2026-05-15T08:29:50Z", value: 10 },
        { at: "2026-05-15T08:29:55Z", value: 12 },
      ],
    },
  },
}

function Harness({ paused = false, selectedNode = "all" }: { paused?: boolean; selectedNode?: NodeId }) {
  const state = useMonitorData("5m", paused, selectedNode)
  return (
    <div>
      <div data-testid="loading">{String(state.loading)}</div>
      <div data-testid="node-filter">{String(state.nodeFilterEnabled)}</div>
      <div data-testid="nodes">{state.nodes.map((node) => node.label).join(",")}</div>
      <div data-testid="send">{state.data.sendRate.map((point) => point.value).join(",")}</div>
      <button onClick={() => { void state.refresh() }}>refresh</button>
    </div>
  )
}

beforeEach(() => {
  getMonitorMetricsMock.mockReset()
})

test("loads real monitor metrics from the manager API", async () => {
  getMonitorMetricsMock.mockResolvedValue(monitorResponse)

  render(<Harness />)

  expect(screen.getByTestId("loading")).toHaveTextContent("true")
  expect(await screen.findByTestId("send")).toHaveTextContent("4,8")
  expect(screen.getByTestId("nodes")).toHaveTextContent("node-1 (local)")
  expect(screen.getByTestId("node-filter")).toHaveTextContent("false")
  expect(getMonitorMetricsMock).toHaveBeenCalledWith({ window: "5m", step: "5s" })
})

test("does not poll while paused but still supports manual refresh", async () => {
  getMonitorMetricsMock.mockResolvedValue(monitorResponse)

  render(<Harness paused />)

  await screen.findByText("4,8")
  await waitFor(() => expect(getMonitorMetricsMock).toHaveBeenCalledTimes(1))

  screen.getByRole("button", { name: "refresh" }).click()
  await waitFor(() => expect(getMonitorMetricsMock).toHaveBeenCalledTimes(2))
})

test("requests selected node metrics when node filtering is enabled", async () => {
  getMonitorMetricsMock.mockResolvedValue({
    ...monitorResponse,
    capabilities: { node_filter: true },
    nodes: [
      { node_id: 1, name: "node-1", is_local: true, available: true },
      { node_id: 2, name: "node-2", is_local: false, available: true },
    ],
  })

  render(<Harness selectedNode="2" />)

  await screen.findByText("4,8")
  expect(getMonitorMetricsMock).toHaveBeenCalledWith({ window: "5m", step: "5s", nodeId: "2" })
})
