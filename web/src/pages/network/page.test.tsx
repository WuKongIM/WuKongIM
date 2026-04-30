import { render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeEach, expect, test, vi } from "vitest"

import { I18nProvider } from "@/i18n/provider"
import { resetLocale } from "@/i18n/locale-store"
import { ManagerApiError } from "@/lib/manager-api"
import type { ManagerNetworkSummaryResponse } from "@/lib/manager-api.types"
import { NetworkPage } from "@/pages/network/page"

const getNetworkSummaryMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getNetworkSummary: (...args: unknown[]) => getNetworkSummaryMock(...args),
  }
})

const networkSummaryFixture: ManagerNetworkSummaryResponse = {
  generated_at: "2026-04-23T08:00:00Z",
  scope: { view: "local_node", local_node_id: 1, controller_leader_id: 2 },
  source_status: {
    local_collector: "ok",
    controller_context: "ok",
    runtime_views: "ok",
    errors: {},
  },
  headline: {
    remote_peers: 1,
    alive_nodes: 2,
    suspect_nodes: 0,
    dead_nodes: 0,
    draining_nodes: 1,
    pool_active: 3,
    pool_idle: 4,
    rpc_inflight: 5,
    dial_errors_1m: 1,
    queue_full_1m: 2,
    timeouts_1m: 3,
    stale_observations: 0,
  },
  traffic: {
    scope: "local_total_by_msg_type",
    tx_bytes_1m: 4096,
    rx_bytes_1m: 2048,
    tx_bps: 128,
    rx_bps: 64,
    peer_breakdown_available: false,
    by_message_type: [
      { direction: "tx", message_type: "channel_long_poll_fetch", bytes_1m: 1024, bps: 32 },
      { direction: "rx", message_type: "channel_long_poll_fetch", bytes_1m: 512, bps: 16 },
      { direction: "tx", message_type: "controller_ping", bytes_1m: 256, bps: 8 },
    ],
  },
  peers: [{
    node_id: 2,
    name: "node-2",
    addr: "10.0.0.2:11110",
    health: "alive",
    last_heartbeat_at: "2026-04-23T08:00:00Z",
    pools: {
      cluster: { active: 1, idle: 2 },
      data_plane: { active: 2, idle: 1 },
    },
    rpc: { inflight: 5, calls_1m: 120, p95_ms: 7, success_rate: 0.98 },
    errors: { dial_error_1m: 1, queue_full_1m: 2, timeout_1m: 3, remote_error_1m: 4 },
  }],
  services: [{
    service_id: 35,
    service: "channel_long_poll_fetch",
    group: "channel_data_plane",
    target_node: 2,
    inflight: 1,
    calls_1m: 40,
    success_1m: 39,
    expected_timeout_1m: 0,
    timeout_1m: 1,
    queue_full_1m: 0,
    remote_error_1m: 0,
    other_error_1m: 0,
    p50_ms: 3,
    p95_ms: 7,
    p99_ms: 11,
    last_seen_at: "2026-04-23T08:00:00Z",
  }, {
    service_id: 13,
    service: "controller_ping",
    group: "controller",
    target_node: 2,
    inflight: 0,
    calls_1m: 0,
    success_1m: 0,
    expected_timeout_1m: 0,
    timeout_1m: 0,
    queue_full_1m: 0,
    remote_error_1m: 0,
    other_error_1m: 0,
    p50_ms: 0,
    p95_ms: 0,
    p99_ms: 0,
    last_seen_at: "2026-04-23T08:00:00Z",
  }],
  channel_replication: {
    pool: { active: 2, idle: 1 },
    services: [{
      service_id: 35,
      service: "channel_long_poll_fetch",
      group: "channel_data_plane",
      target_node: 2,
      inflight: 1,
      calls_1m: 40,
      success_1m: 39,
      expected_timeout_1m: 0,
      timeout_1m: 1,
      queue_full_1m: 0,
      remote_error_1m: 0,
      other_error_1m: 0,
      p50_ms: 3,
      p95_ms: 7,
      p99_ms: 11,
      last_seen_at: "2026-04-23T08:00:00Z",
    }],
    long_poll: { lane_count: 8, max_wait_ms: 30000, max_bytes: 1048576, max_channels: 128 },
    long_poll_timeouts_1m: 6,
    data_plane_rpc_timeout_ms: 5000,
  },
  discovery: {
    listen_addr: "0.0.0.0:11110",
    advertise_addr: "10.0.0.1:11110",
    seeds: ["10.0.0.2:11110"],
    static_nodes: [{ node_id: 2, addr: "10.0.0.2:11110" }],
    pool_size: 4,
    data_plane_pool_size: 8,
    dial_timeout_ms: 3000,
    controller_observation_interval_ms: 10000,
  },
  history: {
    window_seconds: 60,
    step_seconds: 30,
    traffic: [
      { at: "2026-04-23T07:59:30Z", tx_bytes: 1024, rx_bytes: 512 },
      { at: "2026-04-23T08:00:00Z", tx_bytes: 4096, rx_bytes: 2048 },
    ],
    rpc: [
      { at: "2026-04-23T07:59:30Z", calls: 10, success: 9, errors: 1, expected_timeouts: 2 },
      { at: "2026-04-23T08:00:00Z", calls: 40, success: 39, errors: 1, expected_timeouts: 6 },
    ],
    errors: [
      { at: "2026-04-23T07:59:30Z", dial_errors: 0, queue_full: 1, timeouts: 1, remote_errors: 0 },
      { at: "2026-04-23T08:00:00Z", dial_errors: 1, queue_full: 2, timeouts: 3, remote_errors: 4 },
    ],
  },
  events: [{
    at: "2026-04-23T08:00:00Z",
    severity: "warning",
    kind: "rpc_timeout",
    target_node: 2,
    service: "channel_long_poll_fetch",
    message: "timeout waiting for long poll",
  }],
}

function renderNetworkPage() {
  return render(
    <I18nProvider>
      <NetworkPage />
    </I18nProvider>,
  )
}

function metricCards(label: string) {
  return screen.getAllByText(label).map((labelElement) => {
    const card = labelElement.parentElement
    if (!card) throw new Error(`Missing metric card for ${label}`)
    return card
  })
}

function expectAllMetricCards(label: string, value: string) {
  for (const card of metricCards(label)) {
    expect(within(card).getByText(value)).toBeInTheDocument()
  }
}

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  getNetworkSummaryMock.mockReset()
})

test("renders loading then local-node network summary", async () => {
  getNetworkSummaryMock.mockResolvedValue(networkSummaryFixture)

  renderNetworkPage()

  expect(screen.getByRole("status")).toHaveAttribute("data-kind", "loading")
  expect(await screen.findByRole("heading", { name: "Network" })).toBeInTheDocument()
  expect(screen.getByText("Local-node view")).toBeInTheDocument()
  expect(screen.getByText(/local total by message type/i)).toBeInTheDocument()
  expect(screen.queryByText(/not exposed/i)).not.toBeInTheDocument()
  expect(screen.getByText("Remote Peers")).toBeInTheDocument()
  expect(screen.getByText("2/0/0/1")).toBeInTheDocument()
  expect(screen.getByText("Node Health Distribution")).toBeInTheDocument()
  expect(screen.getByText("Traffic Trend")).toBeInTheDocument()
  expect(screen.getByText("Traffic by Message Type")).toBeInTheDocument()
  expect(screen.getByText("RPC Calls & Errors")).toBeInTheDocument()
  expect(screen.getByText("Peer Pool Balance")).toBeInTheDocument()
  expect(screen.getByText("Channel Data-plane")).toBeInTheDocument()
  expect(screen.getByText("node-2")).toBeInTheDocument()
  expect(screen.getAllByText("channel_long_poll_fetch").length).toBeGreaterThan(0)
  expect(screen.getByText("TX Total")).toBeInTheDocument()
  expect(screen.getByText("4,096 B")).toBeInTheDocument()
  expect(screen.getByText("RX Total")).toBeInTheDocument()
  expect(screen.getByText("2,048 B")).toBeInTheDocument()
  expect(screen.getAllByText("Expected long-poll expiries").length).toBeGreaterThan(0)
  expectAllMetricCards("Expected long-poll expiries", "6")
  expect(screen.getAllByText("Abnormal failures").length).toBeGreaterThan(0)
  expectAllMetricCards("Abnormal failures", "10")
  expect(screen.getAllByText("channel_long_poll_fetch").length).toBeGreaterThan(0)
  expect(screen.getByText(/TX 1,024 B/i)).toBeInTheDocument()
  expect(screen.getByText(/RX 512 B/i)).toBeInTheDocument()
  expect(screen.getAllByText("controller_ping").length).toBeGreaterThan(0)
  expect(screen.getByText(/TX 256 B/i)).toBeInTheDocument()
  expect(screen.getByText(/local totals only/i)).toBeInTheDocument()
  expect(screen.getByText("Long-poll Max Bytes")).toBeInTheDocument()
  expect(screen.getByText("Long-poll Max Channels")).toBeInTheDocument()
})

test("renders visible fallback values when history arrays are empty", async () => {
  getNetworkSummaryMock.mockResolvedValue({
    ...networkSummaryFixture,
    history: {
      ...networkSummaryFixture.history,
      traffic: [],
      rpc: [],
      errors: [],
    },
  })

  renderNetworkPage()

  expect(await screen.findByText("Traffic Trend")).toBeInTheDocument()
  expect(screen.getAllByText("No history samples; showing snapshot totals.").length).toBeGreaterThan(0)
  expect(screen.getByText("TX Total")).toBeInTheDocument()
  expect(screen.getByText("4,096 B")).toBeInTheDocument()
  expect(screen.getAllByText("Expected long-poll expiries").length).toBeGreaterThan(0)
  expect(screen.getAllByText("Abnormal failures").length).toBeGreaterThan(0)
  expectAllMetricCards("Abnormal failures", "10")
})

test("shows only current long-poll expiries as neutral samples", async () => {
  getNetworkSummaryMock.mockResolvedValue({
    ...networkSummaryFixture,
    services: [
      { ...networkSummaryFixture.services[0], expected_timeout_1m: 1 },
      { ...networkSummaryFixture.services[1], expected_timeout_1m: 99 },
    ],
    channel_replication: {
      ...networkSummaryFixture.channel_replication,
      long_poll_timeouts_1m: 1,
    },
    history: {
      ...networkSummaryFixture.history,
      rpc: [
        ...networkSummaryFixture.history.rpc,
        { at: "2026-04-23T08:00:30Z", calls: 100, success: 0, errors: 0, expected_timeouts: 100 },
      ],
    },
  })

  renderNetworkPage()

  expect(await screen.findByText("RPC Calls & Errors")).toBeInTheDocument()
  expectAllMetricCards("Expected long-poll expiries", "1")
})

test("uses current one-minute totals for abnormal failure KPI", async () => {
  getNetworkSummaryMock.mockResolvedValue({
    ...networkSummaryFixture,
    history: {
      ...networkSummaryFixture.history,
      errors: [
        { at: "2026-04-23T07:59:30Z", dial_errors: 3, queue_full: 2, timeouts: 1, remote_errors: 2 },
        { at: "2026-04-23T08:00:00Z", dial_errors: 0, queue_full: 0, timeouts: 1, remote_errors: 0 },
      ],
    },
  })

  renderNetworkPage()

  expect(await screen.findByText("RPC Calls & Errors")).toBeInTheDocument()
  expectAllMetricCards("Abnormal failures", "10")
})

test("renders single-node cluster outbound empty state as healthy", async () => {
  getNetworkSummaryMock.mockResolvedValue({
    ...networkSummaryFixture,
    headline: { ...networkSummaryFixture.headline, remote_peers: 0 },
    peers: [],
  })

  renderNetworkPage()

  expect((await screen.findAllByText("Single-node cluster")).length).toBeGreaterThan(0)
  expect(screen.getByText(/no remote node-to-node transport links/i)).toBeInTheDocument()
})

test("shows unavailable controller source without hiding local collector data", async () => {
  getNetworkSummaryMock.mockResolvedValue({
    ...networkSummaryFixture,
    source_status: {
      local_collector: "ok",
      controller_context: "unavailable",
      runtime_views: "ok",
      errors: { controller_context: "leader not reachable" },
    },
  })

  renderNetworkPage()

  expect(await screen.findByText("Controller context unavailable")).toBeInTheDocument()
  expect(screen.getByText("Local collector ok")).toBeInTheDocument()
  expect(screen.getByText("node-2")).toBeInTheDocument()
})

test("treats expected long-poll expiries as neutral service samples", async () => {
  const expectedLongPollService = {
    ...networkSummaryFixture.services[0],
    calls_1m: 1,
    success_1m: 0,
    expected_timeout_1m: 1,
    timeout_1m: 0,
  }
  getNetworkSummaryMock.mockResolvedValue({
    ...networkSummaryFixture,
    services: [expectedLongPollService],
    channel_replication: {
      ...networkSummaryFixture.channel_replication,
      services: [expectedLongPollService],
      long_poll_timeouts_1m: 1,
    },
  })

  renderNetworkPage()

  expect((await screen.findAllByText("channel_long_poll_fetch")).length).toBeGreaterThan(0)
  expect(screen.queryByText("0%")).not.toBeInTheDocument()
  expect(screen.getAllByText("Insufficient samples").length).toBeGreaterThan(0)
})

test("refreshes network summary", async () => {
  getNetworkSummaryMock.mockResolvedValue(networkSummaryFixture)

  const user = userEvent.setup()
  renderNetworkPage()

  await screen.findByText("node-2")
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
