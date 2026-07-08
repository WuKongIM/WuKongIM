import { render, screen, within } from "@testing-library/react"
import { MemoryRouter, Route, Routes } from "react-router-dom"
import { beforeEach, expect, test, vi } from "vitest"

import { I18nProvider } from "@/i18n/provider"
import { resetLocale } from "@/i18n/locale-store"
import { ClusterDiagnosticsPage } from "@/pages/cluster/diagnostics/page"

const getNetworkSummaryMock = vi.fn()
const getNodesMock = vi.fn()
const getControllerLogsMock = vi.fn()
const getControllerRaftStatusMock = vi.fn()
const getApplicationLogSourcesMock = vi.fn()
const getApplicationLogEntriesMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getNetworkSummary: (...args: unknown[]) => getNetworkSummaryMock(...args),
    getNodes: (...args: unknown[]) => getNodesMock(...args),
    getControllerLogs: (...args: unknown[]) => getControllerLogsMock(...args),
    getControllerRaftStatus: (...args: unknown[]) => getControllerRaftStatusMock(...args),
    getApplicationLogSources: (...args: unknown[]) => getApplicationLogSourcesMock(...args),
    getApplicationLogEntries: (...args: unknown[]) => getApplicationLogEntriesMock(...args),
  }
})

function renderPage(path = "/cluster/diagnostics") {
  return render(
    <I18nProvider>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/cluster/diagnostics" element={<ClusterDiagnosticsPage />} />
        </Routes>
      </MemoryRouter>
    </I18nProvider>,
  )
}

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  getNetworkSummaryMock.mockReset()
  getNodesMock.mockReset()
  getControllerLogsMock.mockReset()
  getControllerRaftStatusMock.mockReset()
  getApplicationLogSourcesMock.mockReset()
  getApplicationLogEntriesMock.mockReset()
  getNetworkSummaryMock.mockResolvedValue({
    generated_at: "2026-04-23T08:00:00Z",
    scope: { view: "local_node", local_node_id: 1, controller_leader_id: 1 },
    source_status: { local_collector: "ok", controller_context: "ok", runtime_views: "ok", errors: {} },
    headline: {
      remote_peers: 0,
      alive_nodes: 1,
      suspect_nodes: 0,
      dead_nodes: 0,
      draining_nodes: 0,
      pool_active: 0,
      pool_idle: 0,
      rpc_inflight: 0,
      dial_errors_1m: 0,
      queue_full_1m: 0,
      timeouts_1m: 0,
      stale_observations: 0,
    },
    traffic: { scope: "local_total_by_msg_type", tx_bytes_1m: 0, rx_bytes_1m: 0, tx_bps: 0, rx_bps: 0, peer_breakdown_available: false, by_message_type: [] },
    peers: [],
    services: [],
    channel_replication: {
      pool: { active: 0, idle: 0 },
      services: [],
      long_poll: { lane_count: 0, max_wait_ms: 0, max_bytes: 0, max_channels: 0 },
      long_poll_timeouts_1m: 0,
      data_plane_rpc_timeout_ms: 0,
    },
    discovery: { listen_addr: "", advertise_addr: "", seeds: [], static_nodes: [], pool_size: 0, data_plane_pool_size: 0, dial_timeout_ms: 0, controller_observation_interval_ms: 0 },
    events: [],
  })
  getNodesMock.mockResolvedValue({ total: 1, items: [{ node_id: 1, addr: "127.0.0.1:7000", status: "alive", last_heartbeat_at: "2026-04-23T08:00:00Z", is_local: true, capacity_weight: 1, controller: { role: "leader" }, slot_stats: { count: 1, leader_count: 1 } }] })
  getControllerLogsMock.mockResolvedValue({ node_id: 1, first_index: 1, last_index: 1, commit_index: 1, applied_index: 1, items: [] })
  getControllerRaftStatusMock.mockResolvedValue({ node_id: 1, role: "leader", leader_id: 1, term: 1, health: "healthy", first_index: 1, last_index: 1, commit_index: 1, applied_index: 1, voters: [1], learners: [], snapshot_index: 0, snapshot_term: 0, compaction: { enabled: false, trigger_entries: 0, check_interval_ms: 0, last_snapshot_index: 0, last_snapshot_at: "", last_check_at: "", last_error: "", last_error_at: "", degraded: false }, restore: { last_snapshot_index: 0, last_snapshot_term: 0, last_restored_at: "", last_error: "", last_error_at: "", failed: false }, peers: [] })
  getApplicationLogSourcesMock.mockResolvedValue({ node_id: 1, sources: [{ name: "app", file: "app.log", available: true, size_bytes: 0 }] })
  getApplicationLogEntriesMock.mockResolvedValue({ node_id: 1, source: "app", cursor: "", rotated: false, items: [] })
})

test("defaults to the diagnostics trace tab", async () => {
  renderPage()

  const tracingTab = screen.getByRole("tab", { name: "Tracing" })
  expect(tracingTab).toHaveAttribute("aria-selected", "true")
  expect(screen.queryByRole("tab", { name: "Network" })).not.toBeInTheDocument()
  expect(screen.queryByRole("tab", { name: "Control Plane Logs" })).not.toBeInTheDocument()
  expect(screen.queryByRole("tab", { name: "Slot Logs" })).not.toBeInTheDocument()
  expect(screen.queryByRole("tab", { name: "App Logs" })).not.toBeInTheDocument()
  expect(await screen.findByText("Message Diagnostics")).toBeInTheDocument()
  const traceSurface = screen.getByText("Message Diagnostics").closest("[data-cluster-diagnostics-surface='trace']")
  expect(traceSurface).toHaveClass("space-y-4")
  expect(within(traceSurface as HTMLElement).getByRole("tab", { name: "Tracing" })).toHaveAttribute("aria-selected", "true")
  expect(within(traceSurface as HTMLElement).queryByRole("tab", { name: "Network" })).not.toBeInTheDocument()
  expect(await screen.findByText("CLUSTER / DIAGNOSTICS")).toBeInTheDocument()
})

test("normalizes retired diagnostics tabs to tracing", async () => {
  renderPage("/cluster/diagnostics?tab=network")

  expect(screen.getByRole("tab", { name: "Tracing" })).toHaveAttribute("aria-selected", "true")
  expect(await screen.findByText("Message Diagnostics")).toBeInTheDocument()
  expect(screen.queryByText("Node Health Status")).not.toBeInTheDocument()
  expect(screen.queryByRole("heading", { name: "Application Logs" })).not.toBeInTheDocument()
  expect(getNetworkSummaryMock).not.toHaveBeenCalled()
  expect(getApplicationLogSourcesMock).not.toHaveBeenCalled()
})
