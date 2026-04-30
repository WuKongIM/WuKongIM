import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeEach, expect, test, vi } from "vitest"

import { I18nProvider } from "@/i18n/provider"
import { resetLocale } from "@/i18n/locale-store"
import { ManagerApiError } from "@/lib/manager-api"
import { NetworkPage } from "@/pages/network/page"

const getNetworkSummaryMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getNetworkSummary: (...args: unknown[]) => getNetworkSummaryMock(...args),
  }
})

const networkSummaryFixture = {
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
    draining_nodes: 0,
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
  expect(screen.getByText("Outbound Peers")).toBeInTheDocument()
  expect(screen.getByText("node-2")).toBeInTheDocument()
  expect(screen.getAllByText("channel_long_poll_fetch").length).toBeGreaterThan(0)
})

test("renders single-node cluster outbound empty state as healthy", async () => {
  getNetworkSummaryMock.mockResolvedValue({
    ...networkSummaryFixture,
    headline: { ...networkSummaryFixture.headline, remote_peers: 0 },
    peers: [],
  })

  renderNetworkPage()

  expect(await screen.findByText("Single-node cluster")).toBeInTheDocument()
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
