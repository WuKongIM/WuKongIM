import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeEach, expect, test, vi } from "vitest"

import { resetLocale } from "@/i18n/locale-store"
import { I18nProvider } from "@/i18n/provider"
import { getClusterRealtimeMonitor } from "@/lib/manager-api"
import { ClusterMonitorPage } from "@/pages/cluster-monitor/page"

vi.mock("@/lib/manager-api", async () => {
  const actual = await vi.importActual<typeof import("@/lib/manager-api")>("@/lib/manager-api")
  return {
    ...actual,
    getClusterRealtimeMonitor: vi.fn(),
  }
})

function renderClusterMonitorPage() {
  return render(
    <I18nProvider>
      <ClusterMonitorPage />
    </I18nProvider>,
  )
}

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  vi.mocked(getClusterRealtimeMonitor).mockReset()
})

function readyClusterMonitorResponse() {
  return {
    status: "ready" as const,
    generated_at: "2026-06-18T10:00:00Z",
    window_seconds: 900,
    step_seconds: 20,
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

function partialClusterMonitorResponse() {
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

function disabledClusterMonitorResponse() {
  return {
    status: "prometheus_disabled" as const,
    generated_at: "2026-06-18T10:00:00Z",
    window_seconds: 900,
    step_seconds: 20,
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

function unavailableClusterMonitorResponse() {
  return {
    status: "prometheus_unavailable" as const,
    generated_at: "2026-06-18T10:00:00Z",
    window_seconds: 900,
    step_seconds: 20,
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
  expect(getClusterRealtimeMonitor).toHaveBeenCalledWith({ window: "15m" })
})

test("keeps known unavailable cards visible during partial responses", async () => {
  vi.mocked(getClusterRealtimeMonitor).mockResolvedValueOnce(partialClusterMonitorResponse())
  renderClusterMonitorPage()

  expect(await screen.findByText("Cluster monitor data is partially available")).toBeInTheDocument()
  expect(screen.getByText("query timed out for apply gap")).toBeInTheDocument()

  const cards = await screen.findAllByTestId("cluster-monitor-metric-card")
  expect(cards).toHaveLength(2)
  expect(within(cards[1]).getByText("Controller Apply Gap")).toBeInTheDocument()
  expect(within(cards[1]).getByText("Metric unavailable")).toBeInTheDocument()
  expect(within(cards[1]).getByText("prometheus series unavailable")).toBeInTheDocument()
  expect(screen.queryByText("unknownMetric")).not.toBeInTheDocument()
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

test("updates selected time range and pause state from the toolbar", async () => {
  const user = userEvent.setup()
  vi.mocked(getClusterRealtimeMonitor).mockResolvedValue(readyClusterMonitorResponse())
  renderClusterMonitorPage()

  await user.click(await screen.findByRole("button", { name: "30m time range" }))
  expect(screen.getByRole("button", { name: "30m time range" })).toHaveAttribute("aria-pressed", "true")
  expect(getClusterRealtimeMonitor).toHaveBeenLastCalledWith({ window: "30m" })

  await user.click(screen.getByRole("button", { name: "Pause live monitor" }))
  expect(screen.getByRole("button", { name: "Resume live monitor" })).toBeInTheDocument()

  await user.click(screen.getByRole("button", { name: "Resume live monitor" }))
  expect(screen.getByRole("button", { name: "Pause live monitor" })).toBeInTheDocument()
})

test("does not silently render preview fixture before the realtime API responds", () => {
  vi.mocked(getClusterRealtimeMonitor).mockReturnValue(new Promise(() => undefined))
  renderClusterMonitorPage()

  expect(screen.getByText("Loading cluster monitor data...")).toBeInTheDocument()
  expect(screen.queryByText("UI Preview")).not.toBeInTheDocument()
  expect(screen.queryByTestId("cluster-monitor-metric-card")).not.toBeInTheDocument()
  expect(screen.queryByText("Incident Rate")).not.toBeInTheDocument()
})
