import { act, fireEvent, render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, beforeEach, expect, test, vi } from "vitest"

import { TooltipProvider } from "@/components/ui/tooltip"
import { resetLocale } from "@/i18n/locale-store"
import { I18nProvider } from "@/i18n/provider"
import { getClusterRealtimeMonitor, getNodes } from "@/lib/manager-api"
import type { ClusterRealtimeMonitorResponse, ManagerNodesResponse } from "@/lib/manager-api.types"
import { ClusterMonitorPage } from "@/pages/cluster-monitor/page"

vi.mock("@/lib/manager-api", async () => {
  const actual = await vi.importActual<typeof import("@/lib/manager-api")>("@/lib/manager-api")
  return {
    ...actual,
    getClusterRealtimeMonitor: vi.fn(),
    getNodes: vi.fn(),
  }
})

function renderClusterMonitorPage() {
  return render(
    <I18nProvider>
      <TooltipProvider>
        <ClusterMonitorPage />
      </TooltipProvider>
    </I18nProvider>,
  )
}

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  vi.mocked(getClusterRealtimeMonitor).mockReset()
  vi.mocked(getNodes).mockReset()
  vi.mocked(getNodes).mockResolvedValue(managerNodesResponse())
})

afterEach(() => {
  vi.useRealTimers()
})

function managerNodesResponse(): ManagerNodesResponse {
  return {
    generated_at: "2026-06-18T10:00:00Z",
    controller_leader_id: 1,
    total: 2,
    items: [
      {
        node_id: 1,
        name: "node-1",
        addr: "127.0.0.1:11110",
        status: "alive",
        last_heartbeat_at: "2026-06-18T10:00:00Z",
        is_local: true,
        capacity_weight: 1,
        controller: { role: "leader", voter: true, leader_id: 1, raft_health: "healthy" },
        slot_stats: { count: 8, leader_count: 4 },
      },
      {
        node_id: 2,
        name: "node-2",
        addr: "127.0.0.1:11111",
        status: "alive",
        last_heartbeat_at: "2026-06-18T10:00:00Z",
        is_local: false,
        capacity_weight: 1,
        controller: { role: "follower", voter: true, leader_id: 1, raft_health: "healthy" },
        slot_stats: { count: 8, leader_count: 4 },
      },
    ],
  }
}

function readyClusterMonitorResponse(): ClusterRealtimeMonitorResponse {
  return {
    status: "ready" as const,
    generated_at: "2026-06-18T10:00:00Z",
    window_seconds: 900,
    step_seconds: 20,
    scope: { view: "cluster" },
    sources: {
      prometheus: { enabled: true, base_url: "http://127.0.0.1:9090", query_ms: 12, error: "" },
      control_snapshot: { enabled: true, query_ms: 2, error: "" },
    },
    snapshot: [
      { key: "nodesAlive", metric_key: "controllerProposeRate", source: "control_snapshot" as const, text: "3/3", tone: "normal" as const },
      { key: "rpcErrorRate", metric_key: "rpcSuccessRate", source: "prometheus" as const, value: 0.14, unit: "%", tone: "normal" as const },
    ],
    cards: [
      {
        key: "controllerProposeRate",
        source: "prometheus" as const,
        stage: "controlPlane",
        tone: "normal" as const,
        unit: "cmd/s",
        value: 18.4,
        available: true,
        error: "",
        series: [
          { timestamp: 1781767200000, value: 17.8 },
          { timestamp: 1781767220000, value: 18.4 },
        ],
        stats: [
          { key: "avg", value: 18.1 },
          { key: "peak", value: 19.3 },
          { key: "rejected", text: "0" },
        ],
      },
      {
        key: "rpcSuccessRate",
        source: "prometheus" as const,
        stage: "internalNetwork",
        tone: "normal" as const,
        unit: "%",
        value: 99.86,
        available: true,
        error: "",
        series: [
          { timestamp: 1781767200000, value: 99.8 },
          { timestamp: 1781767220000, value: 99.86 },
        ],
        stats: [
          { key: "callsPerSecond", text: "2.8k" },
          { key: "errorsPerSecond", value: 3.8 },
          { key: "timeouts", value: 9 },
        ],
      },
    ],
  }
}

function partialClusterMonitorResponse(): ClusterRealtimeMonitorResponse {
  return {
    ...readyClusterMonitorResponse(),
    status: "partial" as const,
    sources: {
      prometheus: { enabled: true, base_url: "http://127.0.0.1:9090", query_ms: 12, error: "query timed out for apply gap" },
      control_snapshot: { enabled: true, query_ms: 2, error: "" },
    },
    cards: [
      readyClusterMonitorResponse().cards[0],
      {
        key: "controllerApplyGap",
        source: "prometheus" as const,
        stage: "controlPlane",
        tone: "warning" as const,
        unit: "",
        available: false,
        error: "prometheus series unavailable",
        series: [],
        stats: [],
      },
      {
        key: "unknownMetric",
        source: "prometheus" as const,
        stage: "controlPlane",
        tone: "critical" as const,
        unit: "",
        value: 999,
        available: true,
        error: "",
        series: [],
        stats: [],
      },
    ],
  }
}

function largeTrafficClusterMonitorResponse(): ClusterRealtimeMonitorResponse {
  return {
    ...readyClusterMonitorResponse(),
    snapshot: [],
    cards: [
      {
        key: "internalTraffic",
        source: "prometheus" as const,
        stage: "internalNetwork",
        tone: "normal" as const,
        unit: "B/s",
        value: 398006.3,
        available: true,
        error: "",
        series: [
          { timestamp: 1781767200000, value: 293371.6 },
          { timestamp: 1781767220000, value: 398006.3 },
        ],
        stats: [
          { key: "avg", value: 293371.6 },
          { key: "peak", value: 398006.3 },
        ],
      },
    ],
  }
}

function largeTrafficClusterMonitorResponseWithoutStats(): ClusterRealtimeMonitorResponse {
  const response = largeTrafficClusterMonitorResponse()
  delete (response.cards[0] as unknown as Record<string, unknown>).stats
  return response
}

function burstyTrafficClusterMonitorResponse(): ClusterRealtimeMonitorResponse {
  return {
    ...readyClusterMonitorResponse(),
    snapshot: [],
    cards: [
      {
        key: "internalTraffic",
        source: "prometheus" as const,
        stage: "internalNetwork",
        tone: "normal" as const,
        unit: "B/s",
        value: 4_358_584.201439876,
        available: true,
        error: "",
        series: [
          { timestamp: 1781767200000, value: 4_200_000_000 },
          { timestamp: 1781767220000, value: 4_358_584.201439876 },
        ],
        stats: [
          { key: "avg", value: 4_058_584.201439876 },
          { key: "peak", value: 4_358_584.201439876 },
        ],
      },
    ],
  }
}

function nodeMemoryClusterMonitorResponse(): ClusterRealtimeMonitorResponse {
  return {
    ...readyClusterMonitorResponse(),
    snapshot: [],
    cards: [
      {
        key: "nodeMemoryRSS",
        source: "prometheus" as const,
        stage: "runtimePressure",
        tone: "warning" as const,
        unit: "B",
        value: 536_870_912,
        available: true,
        error: "",
        series: [
          { timestamp: 1781767200000, value: 402_653_184 },
          { timestamp: 1781767220000, value: 536_870_912 },
        ],
        stats: [
          { key: "avg", value: 469_762_048 },
          { key: "peak", value: 536_870_912 },
        ],
      },
    ],
  }
}

function allNodeCpuClusterMonitorResponse(): ClusterRealtimeMonitorResponse {
  return {
    ...readyClusterMonitorResponse(),
    snapshot: [],
    cards: [
      {
        key: "nodeCpuPercent",
        source: "prometheus" as const,
        stage: "runtimePressure",
        tone: "warning" as const,
        unit: "%",
        value: 40,
        available: true,
        error: "",
        series: [
          { timestamp: 1781767200000, value: 12.5, label: "node-1", series_key: "node-1" },
          { timestamp: 1781767200000, value: 32.5, label: "node-2", series_key: "node-2" },
          { timestamp: 1781767220000, value: 15, label: "node-1", series_key: "node-1" },
          { timestamp: 1781767220000, value: 40, label: "node-2", series_key: "node-2" },
        ],
        stats: [
          { key: "node", label: "node-1", value: 15, unit: "%" },
          { key: "node", label: "node-2", value: 40, unit: "%" },
        ],
      },
    ],
  }
}

function disabledClusterMonitorResponse(): ClusterRealtimeMonitorResponse {
  return {
    status: "prometheus_disabled" as const,
    generated_at: "2026-06-18T10:00:00Z",
    window_seconds: 900,
    step_seconds: 20,
    scope: { view: "cluster" },
    sources: {
      prometheus: {
        enabled: false,
        base_url: "",
        query_ms: 0,
        error: "prometheus is disabled; set WK_METRICS_ENABLE=true and WK_PROMETHEUS_ENABLE=true",
      },
      control_snapshot: { enabled: true, query_ms: 1, error: "" },
    },
    snapshot: [],
    cards: [],
  }
}

function unavailableClusterMonitorResponse(): ClusterRealtimeMonitorResponse {
  return {
    status: "prometheus_unavailable" as const,
    generated_at: "2026-06-18T10:00:00Z",
    window_seconds: 900,
    step_seconds: 20,
    scope: { view: "cluster" },
    sources: {
      prometheus: {
        enabled: true,
        base_url: "http://127.0.0.1:9090",
        query_ms: 0,
        error: "dial tcp 127.0.0.1:9090: connect: connection refused",
      },
      control_snapshot: { enabled: true, query_ms: 1, error: "" },
    },
    snapshot: [],
    cards: [],
  }
}

test("renders cluster monitor cards from realtime API data", async () => {
  vi.mocked(getClusterRealtimeMonitor).mockResolvedValueOnce(readyClusterMonitorResponse())
  renderClusterMonitorPage()

  expect(screen.getByRole("heading", { name: "Live Monitor" })).toBeInTheDocument()
  expect(await screen.findByText("Live Data")).toBeInTheDocument()
  expect(screen.queryByText("UI Preview")).not.toBeInTheDocument()
  expect(screen.getByText("Cluster control plane, replication, internal network, queue, and storage watermarks.")).toBeInTheDocument()

  const cards = await screen.findAllByTestId("cluster-monitor-metric-card")
  expect(cards).toHaveLength(2)
  expect(within(cards[0]).getByText("Controller Propose Rate")).toBeInTheDocument()
  expect(within(cards[0]).getByText("18.4")).toBeInTheDocument()
  expect(within(cards[0]).getByText("0")).toBeInTheDocument()
  expect(within(cards[1]).getByText("RPC Success Rate")).toBeInTheDocument()
  expect(within(cards[1]).getByText("2.8k")).toBeInTheDocument()

  expect(screen.getByText("Nodes Alive")).toBeInTheDocument()
  expect(screen.getByText("RPC Errors")).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Refresh now" })).toBeInTheDocument()
  expect(screen.getByRole("combobox", { name: "Auto refresh" })).toHaveValue("30s")
  expect(getClusterRealtimeMonitor).toHaveBeenCalledWith({ window: "15m" })
})

test("shows metric explanations from card help buttons", async () => {
  const user = userEvent.setup()
  vi.mocked(getClusterRealtimeMonitor).mockResolvedValueOnce(readyClusterMonitorResponse())
  renderClusterMonitorPage()

  expect(await screen.findByRole("button", { name: "Explain Controller Propose Rate" })).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Explain RPC Success Rate" })).toBeInTheDocument()

  await user.click(screen.getByRole("button", { name: "Explain Controller Propose Rate" }))

  expect(await screen.findAllByText("Rate of control-plane decisions proposed by the controller.")).not.toHaveLength(0)
})

test("keeps known unavailable cards visible during partial responses", async () => {
  vi.mocked(getClusterRealtimeMonitor).mockResolvedValueOnce(partialClusterMonitorResponse())
  renderClusterMonitorPage()

  const cards = await screen.findAllByTestId("cluster-monitor-metric-card")
  expect(cards).toHaveLength(2)
  expect(screen.queryByText("Cluster monitor data is partially available")).not.toBeInTheDocument()
  expect(screen.queryByText("query timed out for apply gap")).not.toBeInTheDocument()
  expect(within(cards[1]).getByText("Controller Apply Gap")).toBeInTheDocument()
  expect(within(cards[1]).getByText("Metric unavailable")).toBeInTheDocument()
  expect(within(cards[1]).getByText("No series data")).toBeInTheDocument()
  expect(within(cards[1]).queryByTestId("cluster-monitor-chart")).not.toBeInTheDocument()
  expect(within(cards[1]).getByText("prometheus series unavailable")).toBeInTheDocument()
  expect(screen.queryByText("unknownMetric")).not.toBeInTheDocument()
})

test("formats large internal traffic byte rates", async () => {
  vi.mocked(getClusterRealtimeMonitor).mockResolvedValueOnce(largeTrafficClusterMonitorResponse())
  renderClusterMonitorPage()

  const card = await screen.findByTestId("cluster-monitor-metric-card")
  expect(within(card).getByText("Internal Traffic")).toBeInTheDocument()
  expect(within(card).getByText("388.7")).toBeInTheDocument()
  expect(within(card).getAllByText("KB/s").length).toBeGreaterThan(0)
  expect(within(card).getByText("286.5 KB/s")).toBeInTheDocument()
  expect(within(card).getByText("388.7 KB/s")).toBeInTheDocument()
  expect(within(card).queryByText("398,006.3")).not.toBeInTheDocument()
})

test("keeps internal traffic unit readable when an old burst is larger than the current value", async () => {
  vi.mocked(getClusterRealtimeMonitor).mockResolvedValueOnce(burstyTrafficClusterMonitorResponse())
  renderClusterMonitorPage()

  const card = await screen.findByTestId("cluster-monitor-metric-card")
  expect(within(card).getByText("Internal Traffic")).toBeInTheDocument()
  expect(within(card).getByText("4.2")).toBeInTheDocument()
  expect(within(card).getAllByText("MB/s").length).toBeGreaterThan(0)
  expect(within(card).getByText("3.9 MB/s")).toBeInTheDocument()
  expect(within(card).getByText("4.2 MB/s")).toBeInTheDocument()
  expect(within(card).queryByText("0.0")).not.toBeInTheDocument()
  expect(within(card).queryByText("GB/s")).not.toBeInTheDocument()
})

test("formats node memory pressure bytes", async () => {
  vi.mocked(getClusterRealtimeMonitor).mockResolvedValueOnce(nodeMemoryClusterMonitorResponse())
  renderClusterMonitorPage()

  const card = await screen.findByTestId("cluster-monitor-metric-card")
  expect(within(card).getByText("Node Memory RSS")).toBeInTheDocument()
  expect(within(card).getByText("512")).toBeInTheDocument()
  expect(within(card).getAllByText("MB").length).toBeGreaterThan(0)
  expect(within(card).getByText("448 MB")).toBeInTheDocument()
  expect(within(card).getByText("512 MB")).toBeInTheDocument()
  expect(within(card).queryByText("536,870,912")).not.toBeInTheDocument()
})

test("renders all node resource pressure stats in global scope", async () => {
  vi.mocked(getClusterRealtimeMonitor).mockResolvedValueOnce(allNodeCpuClusterMonitorResponse())
  renderClusterMonitorPage()

  const card = await screen.findByTestId("cluster-monitor-metric-card")
  expect(within(card).getByText("Node CPU")).toBeInTheDocument()
  expect(within(card).getByText("node-1")).toBeInTheDocument()
  expect(within(card).getByText("15%")).toBeInTheDocument()
  expect(within(card).getByText("node-2")).toBeInTheDocument()
  expect(within(card).getByText("40%")).toBeInTheDocument()
})

test("keeps rendering when cluster cards omit stats", async () => {
  vi.mocked(getClusterRealtimeMonitor).mockResolvedValueOnce(largeTrafficClusterMonitorResponseWithoutStats())
  renderClusterMonitorPage()

  const card = await screen.findByTestId("cluster-monitor-metric-card")
  expect(within(card).getByText("Internal Traffic")).toBeInTheDocument()
  expect(within(card).getByText("388.7")).toBeInTheDocument()
  expect(within(card).getByText("KB/s")).toBeInTheDocument()
  expect(within(card).queryByText("Avg")).not.toBeInTheDocument()
})

test("shows prometheus setup guidance when cluster realtime monitor is disabled", async () => {
  vi.mocked(getClusterRealtimeMonitor).mockResolvedValueOnce(disabledClusterMonitorResponse())
  renderClusterMonitorPage()

  expect(await screen.findByText("Prometheus monitoring is not enabled")).toBeInTheDocument()
  expect(screen.getByText("WK_METRICS_ENABLE=true")).toBeInTheDocument()
  expect(screen.getByText("WK_PROMETHEUS_ENABLE=true")).toBeInTheDocument()
  expect(screen.queryByTestId("cluster-monitor-metric-card")).not.toBeInTheDocument()
})

test("shows unavailable guidance for source errors and rejected requests", async () => {
  vi.mocked(getClusterRealtimeMonitor).mockResolvedValueOnce(unavailableClusterMonitorResponse())
  const { unmount } = renderClusterMonitorPage()

  expect(await screen.findByText("Prometheus is unavailable")).toBeInTheDocument()
  expect(screen.getByText("dial tcp 127.0.0.1:9090: connect: connection refused")).toBeInTheDocument()
  expect(screen.queryByTestId("cluster-monitor-metric-card")).not.toBeInTheDocument()

  unmount()
  vi.mocked(getClusterRealtimeMonitor).mockRejectedValueOnce(new Error("manager api unavailable"))
  renderClusterMonitorPage()

  expect(await screen.findByText("Prometheus is unavailable")).toBeInTheDocument()
  expect(screen.getByText("manager api unavailable")).toBeInTheDocument()
})

test("updates selected time range and auto refresh interval from the toolbar", async () => {
  const user = userEvent.setup()
  vi.mocked(getClusterRealtimeMonitor).mockResolvedValue(readyClusterMonitorResponse())
  renderClusterMonitorPage()

  await user.click(await screen.findByRole("button", { name: "30m time range" }))
  expect(screen.getByRole("button", { name: "30m time range" })).toHaveAttribute("aria-pressed", "true")
  expect(getClusterRealtimeMonitor).toHaveBeenLastCalledWith({ window: "30m" })

  await user.selectOptions(screen.getByRole("combobox", { name: "Auto refresh" }), "off")
  expect(screen.getByRole("combobox", { name: "Auto refresh" })).toHaveValue("off")
})

test("manually and automatically refreshes cluster realtime monitor data", async () => {
  vi.useFakeTimers()
  vi.mocked(getClusterRealtimeMonitor).mockResolvedValue(readyClusterMonitorResponse())
  renderClusterMonitorPage()

  await act(async () => {
    await Promise.resolve()
    await Promise.resolve()
  })
  expect(screen.getAllByTestId("cluster-monitor-metric-card")).toHaveLength(2)
  expect(getClusterRealtimeMonitor).toHaveBeenCalledTimes(1)

  fireEvent.click(screen.getByRole("button", { name: "Refresh now" }))
  await act(async () => {})
  expect(getClusterRealtimeMonitor).toHaveBeenCalledTimes(2)
  expect(getClusterRealtimeMonitor).toHaveBeenLastCalledWith({ window: "15m" })

  await act(async () => {
    await vi.advanceTimersByTimeAsync(30_000)
  })
  expect(getClusterRealtimeMonitor).toHaveBeenCalledTimes(3)
  expect(getClusterRealtimeMonitor).toHaveBeenLastCalledWith({ window: "15m" })

  fireEvent.change(screen.getByRole("combobox", { name: "Auto refresh" }), { target: { value: "off" } })
  await act(async () => {})
  await act(async () => {
    await vi.advanceTimersByTimeAsync(30_000)
  })
  expect(getClusterRealtimeMonitor).toHaveBeenCalledTimes(3)
})

test("filters cluster realtime monitor by selected node", async () => {
  const user = userEvent.setup()
  vi.mocked(getClusterRealtimeMonitor).mockResolvedValue(readyClusterMonitorResponse())
  renderClusterMonitorPage()

  const nodeSelect = await screen.findByRole("combobox", { name: "Node" })
  expect(getClusterRealtimeMonitor).toHaveBeenCalledWith({ window: "15m" })

  await user.selectOptions(nodeSelect, "2")

  expect(getClusterRealtimeMonitor).toHaveBeenLastCalledWith({ window: "15m", nodeId: 2 })
})

test("does not silently render preview fixture before the realtime API responds", () => {
  vi.mocked(getClusterRealtimeMonitor).mockReturnValue(new Promise(() => undefined))
  renderClusterMonitorPage()

  expect(screen.getByText("Loading cluster monitor data...")).toBeInTheDocument()
  expect(screen.queryByText("UI Preview")).not.toBeInTheDocument()
  expect(screen.queryByTestId("cluster-monitor-metric-card")).not.toBeInTheDocument()
  expect(screen.queryByText("Incident Rate")).not.toBeInTheDocument()
})
