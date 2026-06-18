import { act, fireEvent, render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, beforeEach, expect, test, vi } from "vitest"

import { TooltipProvider } from "@/components/ui/tooltip"
import { resetLocale } from "@/i18n/locale-store"
import { I18nProvider } from "@/i18n/provider"
import { getNodes, getRealtimeMonitor } from "@/lib/manager-api"
import type { ManagerNodesResponse } from "@/lib/manager-api.types"
import { MonitorPage } from "@/pages/monitor/page"

vi.mock("@/lib/manager-api", async () => {
  const actual = await vi.importActual<typeof import("@/lib/manager-api")>("@/lib/manager-api")
  return {
    ...actual,
    getNodes: vi.fn(),
    getRealtimeMonitor: vi.fn(),
  }
})

function renderMonitorPage() {
  return render(
    <I18nProvider>
      <TooltipProvider>
        <MonitorPage />
      </TooltipProvider>
    </I18nProvider>,
  )
}

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  vi.mocked(getNodes).mockReset()
  vi.mocked(getRealtimeMonitor).mockReset()
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
      { key: "conversationSyncP99", metric_key: "conversationSyncLatencyP99", value: 18.4, unit: "ms", tone: "normal" as const },
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
        key: "conversationSyncRate",
        stage: "conversationSync",
        tone: "normal" as const,
        unit: "sync/s",
        value: 9.5,
        available: true,
        error: "",
        series: [
          { timestamp: 1781767200000, value: 8 },
          { timestamp: 1781767220000, value: 9.5 },
        ],
        stats: [
          { key: "avg", value: 8.75 },
          { key: "peak", value: 9.5 },
          { key: "total", value: 315 },
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

function partialMonitorResponseWithUnavailableConversationCard(unavailableReason: string | null = "no_conversation_recent_load_samples") {
  const response = readyMonitorResponse()

  return {
    ...response,
    status: "partial" as const,
    cards: [
      response.cards[0],
      {
        key: "conversationRecentLoadLatencyP99",
        stage: "conversationSync",
        tone: "warning" as const,
        unit: "ms",
        value: 0,
        available: false,
        error: "",
        ...(unavailableReason === null ? {} : { unavailable_reason: unavailableReason }),
        series: [],
        stats: [],
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

function longPrecisionMonitorResponse() {
  const response = readyMonitorResponse()

  return {
    ...response,
    cards: [
      {
        ...response.cards[0],
        value: 12.345678901,
        series: [
          { timestamp: 1781767200000, value: 10.123456789 },
          { timestamp: 1781767220000, value: 12.345678901 },
        ],
        stats: [
          { key: "avg", value: 11.23456789 },
          { key: "peak", value: 12.345678901 },
          { key: "total", value: 404.444444 },
        ],
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
  expect(cards).toHaveLength(3)
  expect(within(cards[0]).getByText("Send Rate")).toBeInTheDocument()
  expect(within(cards[0]).getByText("12.5")).toBeInTheDocument()
  expect(within(cards[1]).getByText("Conversation Sync Rate")).toBeInTheDocument()
  expect(within(cards[1]).getByText("9.5")).toBeInTheDocument()
  expect(within(cards[2]).getByText("Online Connections")).toBeInTheDocument()

  for (const label of ["Send", "Conversation Sync P99", "Online"]) {
    expect(screen.getByText(label)).toBeInTheDocument()
  }

  for (const label of ["5m time range", "15m time range", "30m time range", "1h time range", "Refresh now"]) {
    expect(screen.getByRole("button", { name: label })).toBeInTheDocument()
  }
  expect(screen.getByRole("combobox", { name: "Auto refresh" })).toHaveValue("30s")
  expect(getRealtimeMonitor).toHaveBeenCalledWith({ window: "15m" })
})

test("shows metric explanations from business monitor card help buttons", async () => {
  const user = userEvent.setup()
  vi.mocked(getRealtimeMonitor).mockResolvedValueOnce(readyMonitorResponse())
  renderMonitorPage()

  expect(await screen.findByRole("button", { name: "Explain Send Rate" })).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Explain Conversation Sync Rate" })).toBeInTheDocument()

  await user.click(screen.getByRole("button", { name: "Explain Send Rate" }))

  expect(await screen.findAllByText("Rate of client messages accepted by gateway connections.")).not.toHaveLength(0)
})

test("formats realtime business monitor values without leaking raw floats", async () => {
  vi.mocked(getRealtimeMonitor).mockResolvedValueOnce(longPrecisionMonitorResponse())
  renderMonitorPage()

  const card = await screen.findByTestId("monitor-metric-card")
  expect(within(card).getByText("Send Rate")).toBeInTheDocument()
  expect(within(card).getByText("12.3")).toBeInTheDocument()
  expect(within(card).getByText("11.2 msg/s")).toBeInTheDocument()
  expect(within(card).getByText("12.3 msg/s")).toBeInTheDocument()
  expect(within(card).queryByText("12.345678901")).not.toBeInTheDocument()
  expect(within(card).queryByText("11.23456789 msg/s")).not.toBeInTheDocument()
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

test("updates selected time range and auto refresh interval from the toolbar", async () => {
  const user = userEvent.setup()
  vi.mocked(getRealtimeMonitor).mockResolvedValue(readyMonitorResponse())
  renderMonitorPage()

  await user.click(screen.getByRole("button", { name: "30m time range" }))
  expect(screen.getByRole("button", { name: "30m time range" })).toHaveAttribute("aria-pressed", "true")
  expect(getRealtimeMonitor).toHaveBeenLastCalledWith({ window: "30m" })

  await user.selectOptions(screen.getByRole("combobox", { name: "Auto refresh" }), "off")
  expect(screen.getByRole("combobox", { name: "Auto refresh" })).toHaveValue("off")
})

test("manually and automatically refreshes realtime business monitor data", async () => {
  vi.useFakeTimers()
  vi.mocked(getRealtimeMonitor).mockResolvedValue(readyMonitorResponse())
  renderMonitorPage()

  await act(async () => {
    await Promise.resolve()
    await Promise.resolve()
  })
  expect(screen.getAllByTestId("monitor-metric-card")).toHaveLength(3)
  expect(getRealtimeMonitor).toHaveBeenCalledTimes(1)

  fireEvent.click(screen.getByRole("button", { name: "Refresh now" }))
  await act(async () => {})
  expect(getRealtimeMonitor).toHaveBeenCalledTimes(2)
  expect(getRealtimeMonitor).toHaveBeenLastCalledWith({ window: "15m" })

  await act(async () => {
    await vi.advanceTimersByTimeAsync(30_000)
  })
  expect(getRealtimeMonitor).toHaveBeenCalledTimes(3)
  expect(getRealtimeMonitor).toHaveBeenLastCalledWith({ window: "15m" })

  fireEvent.change(screen.getByRole("combobox", { name: "Auto refresh" }), { target: { value: "off" } })
  await act(async () => {})
  await act(async () => {
    await vi.advanceTimersByTimeAsync(30_000)
  })
  expect(getRealtimeMonitor).toHaveBeenCalledTimes(3)
})

test("filters realtime business monitor by selected node", async () => {
  const user = userEvent.setup()
  vi.mocked(getRealtimeMonitor).mockResolvedValue(readyMonitorResponse())
  renderMonitorPage()

  const nodeSelect = await screen.findByRole("combobox", { name: "Node" })
  expect(getRealtimeMonitor).toHaveBeenCalledWith({ window: "15m" })

  await user.selectOptions(nodeSelect, "2")

  expect(getRealtimeMonitor).toHaveBeenLastCalledWith({ window: "15m", nodeId: 2 })
})

test("renders localized no-data copy for unavailable realtime conversation cards", async () => {
  vi.mocked(getRealtimeMonitor).mockResolvedValueOnce(partialMonitorResponseWithUnavailableConversationCard())
  renderMonitorPage()

  const cards = await screen.findAllByTestId("monitor-metric-card")
  expect(cards).toHaveLength(2)
  expect(within(cards[1]).getByText("Conversation Recent Load Latency P99")).toBeInTheDocument()
  expect(within(cards[1]).getByText("-")).toBeInTheDocument()
  expect(within(cards[1]).getByText("No recent-message load samples in the selected time range.")).toBeInTheDocument()
})

test.each([
  ["unknown unavailable reason", "prometheus_query_error"],
  ["missing unavailable reason", null],
])("renders generic no-data copy for %s", async (_, unavailableReason) => {
  vi.mocked(getRealtimeMonitor).mockResolvedValueOnce(partialMonitorResponseWithUnavailableConversationCard(unavailableReason))
  renderMonitorPage()

  const cards = await screen.findAllByTestId("monitor-metric-card")
  expect(cards).toHaveLength(2)
  expect(within(cards[1]).getByText("Conversation Recent Load Latency P99")).toBeInTheDocument()
  expect(within(cards[1]).getByText("Metric data is unavailable.")).toBeInTheDocument()
})

test("shows prometheus setup guidance when realtime monitor is disabled", async () => {
  vi.mocked(getRealtimeMonitor).mockResolvedValueOnce(disabledMonitorResponse())
  renderMonitorPage()

  expect(await screen.findByText("Prometheus monitoring is not enabled")).toBeInTheDocument()
  expect(screen.getByText("WK_METRICS_ENABLE=true")).toBeInTheDocument()
  expect(screen.getByText("WK_PROMETHEUS_ENABLE=true")).toBeInTheDocument()
})
