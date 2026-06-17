import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeEach, expect, test, vi } from "vitest"

import { resetLocale } from "@/i18n/locale-store"
import { I18nProvider } from "@/i18n/provider"
import { AppLogsPanel } from "@/pages/app-logs/page"

const getNodesMock = vi.fn()
const getApplicationLogSourcesMock = vi.fn()
const getApplicationLogEntriesMock = vi.fn()
const streamApplicationLogEntriesMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getNodes: (...args: unknown[]) => getNodesMock(...args),
    getApplicationLogSources: (...args: unknown[]) => getApplicationLogSourcesMock(...args),
    getApplicationLogEntries: (...args: unknown[]) => getApplicationLogEntriesMock(...args),
    streamApplicationLogEntries: (...args: unknown[]) => streamApplicationLogEntriesMock(...args),
  }
})

const nodeResponse = {
  total: 2,
  items: [{
    node_id: 1,
    name: "node-1",
    addr: "127.0.0.1:7000",
    status: "alive",
    last_heartbeat_at: "2026-06-17T10:00:00Z",
    is_local: true,
    capacity_weight: 1,
    controller: { role: "leader" },
    slot_stats: { count: 1, leader_count: 1 },
  }, {
    node_id: 2,
    name: "node-2",
    addr: "127.0.0.1:7001",
    status: "alive",
    last_heartbeat_at: "2026-06-17T10:00:00Z",
    is_local: false,
    capacity_weight: 1,
    controller: { role: "follower" },
    slot_stats: { count: 1, leader_count: 0 },
  }],
}

const sourceResponse = {
  node_id: 1,
  sources: [
    { name: "app", file: "app.log", available: true, size_bytes: 1024, modified_at: "2026-06-17T10:00:00Z" },
    { name: "warn", file: "warn.log", available: true, size_bytes: 64 },
    { name: "error", file: "error.log", available: false, size_bytes: 0 },
  ],
}

function logEntry(message: string, seq = 1) {
  return {
    seq,
    offset: seq * 10,
    time: "2026-06-17T10:00:01Z",
    level: seq === 1 ? "INFO" : "WARN",
    module: "gateway",
    caller: "server.go:10",
    message,
    fields: { listener: "tcp" },
    raw: `${seq} ${message}`,
    truncated: false,
  }
}

function renderPanel() {
  return render(
    <I18nProvider>
      <AppLogsPanel />
    </I18nProvider>,
  )
}

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  getNodesMock.mockReset()
  getApplicationLogSourcesMock.mockReset()
  getApplicationLogEntriesMock.mockReset()
  streamApplicationLogEntriesMock.mockReset()

  getNodesMock.mockResolvedValue(nodeResponse)
  getApplicationLogSourcesMock.mockResolvedValue(sourceResponse)
  getApplicationLogEntriesMock.mockResolvedValue({
    node_id: 1,
    source: "app",
    cursor: "cursor-1",
    rotated: false,
    items: [logEntry("gateway ready")],
  })
})

test("loads ordinary application logs for the default local node", async () => {
  renderPanel()

  expect(await screen.findByRole("heading", { name: "Application Logs" })).toBeInTheDocument()
  expect(await screen.findByText("gateway ready")).toBeInTheDocument()
  expect(screen.getByText("app.log")).toBeInTheDocument()
  expect(screen.getByText("1 line")).toBeInTheDocument()

  await waitFor(() => {
    expect(getApplicationLogSourcesMock).toHaveBeenCalledWith(1)
    expect(getApplicationLogEntriesMock).toHaveBeenCalledWith(expect.objectContaining({
      nodeId: 1,
      source: "app",
      limit: 100,
    }))
  })
})

test("appends rotation and line events when live follow is enabled", async () => {
  const user = userEvent.setup()
  getApplicationLogEntriesMock.mockResolvedValueOnce({
    node_id: 1,
    source: "app",
    cursor: "cursor-1",
    rotated: false,
    items: [],
  })
  streamApplicationLogEntriesMock.mockResolvedValueOnce(new Response([
    JSON.stringify({ type: "rotation", cursor: "cursor-2", rotated: true }),
    JSON.stringify({ type: "line", cursor: "cursor-2", item: logEntry("live warning", 2) }),
  ].join("\n"), { status: 200 }))

  renderPanel()

  await screen.findByRole("heading", { name: "Application Logs" })
  expect(await screen.findByText("0 lines")).toBeInTheDocument()
  await user.click(screen.getByLabelText("Follow tail"))

  expect(await screen.findByText("Log rotated; cursor moved to the new file.")).toBeInTheDocument()
  expect(await screen.findByText("live warning")).toBeInTheDocument()
  expect(screen.getByText("1 line")).toBeInTheDocument()
  await waitFor(() => {
    expect(streamApplicationLogEntriesMock).toHaveBeenCalledWith(expect.objectContaining({
      nodeId: 1,
      source: "app",
      cursor: "cursor-1",
      limit: 100,
    }))
  })
})
