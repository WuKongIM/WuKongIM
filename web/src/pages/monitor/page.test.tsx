import { render, screen } from "@testing-library/react"
import { beforeEach, expect, test, vi } from "vitest"

import { resetLocale } from "@/i18n/locale-store"
import { I18nProvider } from "@/i18n/provider"
import { ManagerApiError } from "@/lib/manager-api"
import { MonitorPage } from "@/pages/monitor/page"

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
      points: [{ at: "2026-05-15T08:29:55Z", value: 8 }],
    },
    deliver_rate: {
      key: "deliver_rate",
      unit: "msg/s",
      latest: 7,
      peak: 7,
      avg: 5,
      points: [{ at: "2026-05-15T08:29:55Z", value: 7 }],
    },
    online_connections: {
      key: "online_connections",
      unit: "connections",
      latest: 12,
      peak: 12,
      avg: 11,
      points: [{ at: "2026-05-15T08:29:55Z", value: 12 }],
    },
  },
}

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
  getMonitorMetricsMock.mockReset()
})

test("renders real monitor metrics and hides unsupported mock-only sections", async () => {
  getMonitorMetricsMock.mockResolvedValue(monitorResponse)

  renderMonitorPage()

  expect(await screen.findByText("Send Rate")).toBeInTheDocument()
  expect(screen.getByText("Deliver Rate")).toBeInTheDocument()
  expect(screen.getByText("Online Connections")).toBeInTheDocument()
  expect(screen.queryByText("CPU Usage")).not.toBeInTheDocument()
  expect(screen.queryByText("Storage I/O")).not.toBeInTheDocument()
  expect(screen.getByLabelText("Node filter")).toBeDisabled()
})

test("shows the local node instead of all nodes when the API cannot aggregate nodes", async () => {
  getMonitorMetricsMock.mockResolvedValue(monitorResponse)

  renderMonitorPage()

  const nodeFilter = await screen.findByLabelText("Node filter")
  expect(nodeFilter).toBeDisabled()
  expect(nodeFilter).toHaveValue("1")
  expect(screen.queryByRole("option", { name: "All Nodes" })).not.toBeInTheDocument()
})

test("shows warming state when the monitor collector is unavailable", async () => {
  getMonitorMetricsMock.mockRejectedValue(new ManagerApiError(503, "service_unavailable", "monitor metrics collector warming up"))

  renderMonitorPage()

  expect(screen.getByRole("heading", { name: "Live Monitor" })).toBeInTheDocument()
  expect(await screen.findByRole("status")).toHaveAttribute("data-kind", "unavailable")
})
