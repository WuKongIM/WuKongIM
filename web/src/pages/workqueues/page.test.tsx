import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import { beforeEach, expect, test, vi } from "vitest"

import { resetLocale } from "@/i18n/locale-store"
import { I18nProvider } from "@/i18n/provider"
import { ManagerApiError } from "@/lib/manager-api"
import { WorkqueuesPage } from "@/pages/workqueues/page"

const getRuntimeWorkqueuesMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return { ...actual, getRuntimeWorkqueues: (...args: unknown[]) => getRuntimeWorkqueuesMock(...args) }
})

const workqueueResponse = {
  generated_at: "2026-06-17T10:00:00Z",
  window_seconds: 10,
  scope: { view: "local_node", node_id: 1, node_name: "node-1", ready: true },
  summary: {
    overall_level: "degraded",
    total: 2,
    ok: 1,
    busy: 0,
    degraded: 1,
    critical: 0,
    hottest: {
      component: "gateway",
      pool: "async_send",
      queue: "send",
      priority: "none",
      level: "degraded",
      score: 0.82,
    },
  },
  items: [
    {
      component: "gateway",
      pool: "async_send",
      queue: "send",
      priority: "none",
      level: "degraded",
      score: 0.82,
      depth: 82,
      capacity: 100,
      inflight: 0,
      workers: 0,
      wait_p99_ms: 12.4,
      task_p99_ms: 20.5,
      admission_error_per_sec: 0.3,
      hint: "queue depth is approaching capacity",
    },
    {
      component: "db",
      pool: "message_commit",
      queue: "commit",
      priority: "none",
      level: "ok",
      score: 0.2,
      depth: 2,
      capacity: 10,
      inflight: 0,
      workers: 1,
      wait_p99_ms: 1.1,
      task_p99_ms: 2.2,
      admission_error_per_sec: 0,
      hint: "",
    },
  ],
  sources: {
    collector: { available: true, sample_count: 10 },
    metrics: { enabled: false, required: false },
    notes: [],
  },
}

function renderPage() {
  return render(
    <I18nProvider>
      <WorkqueuesPage />
    </I18nProvider>,
  )
}

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  getRuntimeWorkqueuesMock.mockReset()
})

test("renders summary and pressure rows", async () => {
  getRuntimeWorkqueuesMock.mockResolvedValue(workqueueResponse)
  renderPage()
  expect(await screen.findByRole("heading", { name: "Workqueue Monitor" })).toBeInTheDocument()
  expect(screen.getByText("degraded")).toBeInTheDocument()
  expect(screen.getByText("gateway")).toBeInTheDocument()
  expect(screen.getByText("async_send")).toBeInTheDocument()
  expect(screen.getByText("82 / 100")).toBeInTheDocument()
  expect(screen.getByText("12.4 ms")).toBeInTheDocument()
  expect(screen.getByText("0.30/s")).toBeInTheDocument()
})

test("filters ok rows when abnormal only is enabled", async () => {
  getRuntimeWorkqueuesMock.mockResolvedValue(workqueueResponse)
  renderPage()
  expect(await screen.findByText("message_commit")).toBeInTheDocument()
  fireEvent.click(screen.getByLabelText("Abnormal only"))
  expect(screen.queryByText("message_commit")).not.toBeInTheDocument()
  expect(screen.getByText("async_send")).toBeInTheDocument()
})

test("filters rows by component", async () => {
  getRuntimeWorkqueuesMock.mockResolvedValue(workqueueResponse)
  renderPage()
  const component = await screen.findByLabelText("Component")
  fireEvent.change(component, { target: { value: "db" } })
  expect(screen.getByText("message_commit")).toBeInTheDocument()
  expect(screen.queryByText("async_send")).not.toBeInTheDocument()
})

test("shows warming state for service unavailable responses", async () => {
  getRuntimeWorkqueuesMock.mockRejectedValue(new ManagerApiError(503, "service_unavailable", "top collector warming up"))
  renderPage()
  expect(await screen.findByRole("status")).toHaveAttribute("data-kind", "unavailable")
})

test("shows empty state when no pressure items are returned", async () => {
  getRuntimeWorkqueuesMock.mockResolvedValue({ ...workqueueResponse, summary: { ...workqueueResponse.summary, total: 0 }, items: [] })
  renderPage()
  expect(await screen.findByRole("status")).toHaveAttribute("data-kind", "empty")
})

test("refreshes with the selected window", async () => {
  getRuntimeWorkqueuesMock.mockResolvedValue(workqueueResponse)
  renderPage()
  await screen.findByText("async_send")
  fireEvent.change(screen.getByLabelText("Window"), { target: { value: "30s" } })
  fireEvent.click(screen.getByRole("button", { name: "Refresh" }))
  await waitFor(() => {
    expect(getRuntimeWorkqueuesMock).toHaveBeenLastCalledWith({ window: "30s", limit: 100 })
  })
})
