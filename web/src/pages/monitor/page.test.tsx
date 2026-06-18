import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeEach, expect, test, vi } from "vitest"

import { resetLocale } from "@/i18n/locale-store"
import { I18nProvider } from "@/i18n/provider"
import { getRealtimeMonitor } from "@/lib/manager-api"
import { MonitorPage } from "@/pages/monitor/page"

vi.mock("@/lib/manager-api", async () => {
  const actual = await vi.importActual<typeof import("@/lib/manager-api")>("@/lib/manager-api")
  return {
    ...actual,
    getRealtimeMonitor: vi.fn(),
  }
})

function renderMonitorPage() {
  return render(
    <I18nProvider>
      <MonitorPage />
    </I18nProvider>,
  )
}

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  vi.mocked(getRealtimeMonitor).mockReset()
})

function readyMonitorResponse() {
  return {
    status: "ready" as const,
    generated_at: "2026-06-18T10:00:00Z",
    window_seconds: 900,
    step_seconds: 20,
    scope: { view: "prometheus" as const, node_id: 1, node_name: "node-1" },
    sources: { prometheus: { enabled: true, base_url: "http://127.0.0.1:9090", query_ms: 18, error: "" } },
    snapshot: [
      { key: "send", metric_key: "sendRate", value: 12.5, unit: "msg/s", tone: "normal" as const },
      { key: "online", metric_key: "activeConnections", value: 88, tone: "normal" as const },
    ],
    cards: [
      {
        key: "sendRate",
        stage: "sendEntry",
        tone: "normal" as const,
        unit: "msg/s",
        value: 12.5,
        available: true,
        error: "",
        series: [
          { timestamp: 1781767200000, value: 10 },
          { timestamp: 1781767220000, value: 12.5 },
        ],
        stats: [
          { key: "avg", value: 11.25 },
          { key: "peak", value: 12.5 },
          { key: "total", value: 450 },
        ],
      },
      {
        key: "activeConnections",
        stage: "sendEntry",
        tone: "normal" as const,
        unit: "",
        value: 88,
        available: true,
        error: "",
        series: [
          { timestamp: 1781767200000, value: 82 },
          { timestamp: 1781767220000, value: 88 },
        ],
        stats: [
          { key: "avg", value: 85 },
          { key: "peak", value: 88 },
          { key: "total", value: 3400 },
        ],
      },
    ],
  }
}

function disabledMonitorResponse() {
  return {
    status: "prometheus_disabled" as const,
    generated_at: "2026-06-18T10:00:00Z",
    window_seconds: 900,
    step_seconds: 20,
    scope: { view: "prometheus" as const },
    sources: {
      prometheus: {
        enabled: false,
        base_url: "",
        query_ms: 0,
        error: "prometheus is disabled; set WK_METRICS_ENABLE=true and WK_PROMETHEUS_ENABLE=true",
      },
    },
    snapshot: [],
    cards: [],
  }
}

function partialMonitorResponseWithNoDataCard() {
  return {
    ...readyMonitorResponse(),
    status: "partial" as const,
    sources: {
      prometheus: {
        enabled: true,
        base_url: "http://127.0.0.1:9090",
        query_ms: 22,
        error: "deliveryLatencyP99: no delivery latency samples in selected window",
      },
    },
    cards: [
      readyMonitorResponse().cards[0],
      {
        key: "deliveryLatencyP99",
        stage: "onlineDelivery",
        tone: "warning" as const,
        unit: "ms",
        value: 0,
        available: false,
        unavailable_reason: "no_delivery_latency_samples",
        error: "no delivery latency samples in selected window",
        series: [],
        stats: [],
      },
    ],
  }
}

test("renders realtime business monitor cards from prometheus data", async () => {
  vi.mocked(getRealtimeMonitor).mockResolvedValueOnce(readyMonitorResponse())
  renderMonitorPage()

  expect(screen.getByRole("heading", { name: "Live Monitor" })).toBeInTheDocument()
  expect(screen.getByText("Live Data")).toBeInTheDocument()
  expect(screen.getByText("Global business message path health trends.")).toBeInTheDocument()

  const cards = await screen.findAllByTestId("monitor-metric-card")
  expect(cards).toHaveLength(2)
  expect(within(cards[0]).getByText("Send Rate")).toBeInTheDocument()
  expect(within(cards[0]).getByText("12.5")).toBeInTheDocument()
  expect(within(cards[1]).getByText("Online Connections")).toBeInTheDocument()

  for (const label of ["Send", "Online"]) {
    expect(screen.getByText(label)).toBeInTheDocument()
  }

  for (const label of ["5m time range", "15m time range", "30m time range", "1h time range", "Pause live monitor"]) {
    expect(screen.getByRole("button", { name: label })).toBeInTheDocument()
  }
  expect(getRealtimeMonitor).toHaveBeenCalledWith({ window: "15m" })
})

test("renders monitor cards that have no prometheus data", async () => {
  vi.mocked(getRealtimeMonitor).mockResolvedValueOnce(partialMonitorResponseWithNoDataCard())
  renderMonitorPage()

  const cards = await screen.findAllByTestId("monitor-metric-card")
  expect(cards).toHaveLength(2)
  expect(screen.queryByText("Some monitor metrics are unavailable.")).not.toBeInTheDocument()
  expect(screen.queryByText("deliveryLatencyP99: no delivery latency samples in selected window")).not.toBeInTheDocument()
  expect(within(cards[1]).getByText("Delivery Latency P99")).toBeInTheDocument()
  expect(within(cards[1]).getAllByText("No data").length).toBeGreaterThan(0)
  expect(within(cards[1]).getByText("No delivery latency samples in the selected time range.")).toBeInTheDocument()
  expect(within(cards[1]).queryByText("no delivery latency samples in selected window")).not.toBeInTheDocument()
})

test("updates selected time range and pause state from the toolbar", async () => {
  const user = userEvent.setup()
  vi.mocked(getRealtimeMonitor).mockResolvedValue(readyMonitorResponse())
  renderMonitorPage()

  await user.click(screen.getByRole("button", { name: "30m time range" }))
  expect(screen.getByRole("button", { name: "30m time range" })).toHaveAttribute("aria-pressed", "true")
  expect(getRealtimeMonitor).toHaveBeenLastCalledWith({ window: "30m" })

  await user.click(screen.getByRole("button", { name: "Pause live monitor" }))
  expect(screen.getByRole("button", { name: "Resume live monitor" })).toBeInTheDocument()

  await user.click(screen.getByRole("button", { name: "Resume live monitor" }))
  expect(screen.getByRole("button", { name: "Pause live monitor" })).toBeInTheDocument()
})

test("shows prometheus setup guidance when realtime monitor is disabled", async () => {
  vi.mocked(getRealtimeMonitor).mockResolvedValueOnce(disabledMonitorResponse())
  renderMonitorPage()

  expect(await screen.findByText("Prometheus monitoring is not enabled")).toBeInTheDocument()
  expect(screen.getByText("WK_METRICS_ENABLE=true")).toBeInTheDocument()
  expect(screen.getByText("WK_PROMETHEUS_ENABLE=true")).toBeInTheDocument()
})
