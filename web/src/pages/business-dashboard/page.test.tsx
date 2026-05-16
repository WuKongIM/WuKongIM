import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { MemoryRouter } from "react-router-dom"
import { beforeEach, expect, test, vi } from "vitest"

import { I18nProvider } from "@/i18n/provider"
import { resetLocale } from "@/i18n/locale-store"
import { ManagerApiError } from "@/lib/manager-api"
import { BusinessDashboardPage } from "./page"

const getDashboardMetricsMock = vi.fn()
const getUsersMock = vi.fn()
const getBusinessChannelsMock = vi.fn()
const getSystemUsersMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getDashboardMetrics: (...args: unknown[]) => getDashboardMetricsMock(...args),
    getUsers: (...args: unknown[]) => getUsersMock(...args),
    getBusinessChannels: (...args: unknown[]) => getBusinessChannelsMock(...args),
    getSystemUsers: (...args: unknown[]) => getSystemUsersMock(...args),
  }
})

const series = (latest: number, peak = latest, avg = latest) => ({ latest, peak, avg, series: [avg, latest] })

const metricsFixture = {
  generated_at: "2026-05-15T08:30:00Z",
  window_seconds: 300,
  step_seconds: 30,
  points: 10,
  metrics: {
    send_per_sec: series(2800),
    deliver_per_sec: series(2700),
    connections: series(18400),
    send_latency_p99_ms: series(31),
    delivery_latency_p99_ms: series(42),
    send_fail_rate_percent: series(0.02),
    delivery_fail_rate_percent: series(0.01),
    active_channels: series(2143),
    retry_queue_depth: series(8),
    fan_out_rate: series(3.4),
  },
}

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  getDashboardMetricsMock.mockReset()
  getUsersMock.mockReset()
  getBusinessChannelsMock.mockReset()
  getSystemUsersMock.mockReset()
})

function mockSuccessfulBusinessDashboard() {
  getDashboardMetricsMock.mockResolvedValue(metricsFixture)
  getUsersMock.mockResolvedValue({ items: [{ uid: "u1" }], has_more: false })
  getBusinessChannelsMock.mockResolvedValue({ items: [{ channel_id: "c1", channel_type: 1 }], has_more: false })
  getSystemUsersMock.mockResolvedValue({ items: [{ uid: "sys" }], total: 1 })
}

function renderBusinessDashboard() {
  return render(
    <I18nProvider>
      <MemoryRouter initialEntries={["/business/dashboard"]}>
        <BusinessDashboardPage />
      </MemoryRouter>
    </I18nProvider>,
  )
}

test("renders business quality metrics and management entry cards", async () => {
  mockSuccessfulBusinessDashboard()
  renderBusinessDashboard()

  expect(await screen.findByRole("heading", { name: "Business Dashboard" })).toBeInTheDocument()
  expect(screen.getByText("Business normal")).toBeInTheDocument()
  expect(screen.getByText("Send msg/s")).toBeInTheDocument()
  expect(screen.getByText("Deliver msg/s")).toBeInTheDocument()
  expect(screen.getByText("Business Message Trends")).toBeInTheDocument()
  expect(screen.getByRole("link", { name: /^Users/ })).toHaveAttribute("href", "/business/users")
  expect(screen.getByRole("link", { name: /Live Monitor/ })).toHaveAttribute("href", "/business/monitor")
})

test("shows sample badges when optional summaries are unavailable", async () => {
  getDashboardMetricsMock.mockResolvedValue(metricsFixture)
  getUsersMock.mockRejectedValue(new ManagerApiError(503, "unavailable", "users unavailable"))
  getBusinessChannelsMock.mockRejectedValue(new ManagerApiError(503, "unavailable", "channels unavailable"))
  getSystemUsersMock.mockRejectedValue(new ManagerApiError(503, "unavailable", "system users unavailable"))

  renderBusinessDashboard()

  expect(await screen.findByText("Business normal")).toBeInTheDocument()
  expect(screen.getAllByText("sample").length).toBeGreaterThan(0)
})

test("metrics failure renders ResourceState", async () => {
  getDashboardMetricsMock.mockRejectedValue(new ManagerApiError(503, "service_unavailable", "metrics warming"))
  renderBusinessDashboard()

  expect(await screen.findByRole("status")).toHaveAttribute("data-kind", "unavailable")
})

test("refresh refetches metrics and optional summaries", async () => {
  const user = userEvent.setup()
  mockSuccessfulBusinessDashboard()
  renderBusinessDashboard()

  await screen.findByText("Business normal")
  await user.click(screen.getByRole("button", { name: "Refresh" }))

  await waitFor(() => expect(getDashboardMetricsMock).toHaveBeenCalledTimes(2))
  expect(getUsersMock).toHaveBeenCalledTimes(2)
})
