import { render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeEach, expect, test, vi } from "vitest"

import { resetLocale } from "@/i18n/locale-store"
import { I18nProvider } from "@/i18n/provider"
import { AppLogsPage } from "@/pages/app-logs/page"

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
    { name: "app", file: "/var/lib/wukongim/logs/app.log", available: true, size_bytes: 1024, modified_at: "2026-06-17T10:00:00Z" },
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

function pathLikeLogEntry(seq = 3) {
  const path = "/src/internal/runtime/channelappend/append.go:84"
  return {
    ...logEntry(path, seq),
    level: path,
    caller: path,
    raw: path,
  }
}

function renderPanel() {
  return render(
    <I18nProvider>
      <AppLogsPage />
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

test("loads node process logs for the default local node", async () => {
  renderPanel()

  expect(await screen.findByText("CLUSTER / NODE LOGS")).toBeInTheDocument()
  expect(await screen.findByRole("heading", { name: "Node Process Logs" })).toBeInTheDocument()
  expect(await screen.findByText("gateway ready")).toBeInTheDocument()
  const toolbar = document.querySelector("[data-app-logs-surface='toolbar']")
  expect(toolbar).not.toBeNull()
  expect(within(toolbar as HTMLElement).getByText("app.log")).toBeInTheDocument()
  expect(within(toolbar as HTMLElement).queryByText("/var/lib/wukongim/logs/app.log")).not.toBeInTheDocument()
  expect(screen.getAllByText("1 line").length).toBeGreaterThan(0)

  await waitFor(() => {
    expect(getApplicationLogSourcesMock).toHaveBeenCalledWith(1)
    expect(getApplicationLogEntriesMock).toHaveBeenCalledWith(expect.objectContaining({
      nodeId: 1,
      source: "app",
      limit: 100,
    }))
  })
})

test("shows selected node context and source file metadata in the toolbar", async () => {
  renderPanel()

  expect(await screen.findByText("node-1")).toBeInTheDocument()
  const toolbar = document.querySelector("[data-app-logs-surface='toolbar']")
  expect(toolbar).not.toBeNull()
  expect(within(toolbar as HTMLElement).getByText("127.0.0.1:7000")).toBeInTheDocument()
  expect(within(toolbar as HTMLElement).getByText("local")).toBeInTheDocument()
  await waitFor(() => {
    expect(within(toolbar as HTMLElement).getByText("app.log")).toBeInTheDocument()
    expect(within(toolbar as HTMLElement).queryByText("/var/lib/wukongim/logs/app.log")).not.toBeInTheDocument()
    expect(within(toolbar as HTMLElement).getByText("1.0 KiB")).toBeInTheDocument()
  })
  expect(screen.getByRole("option", { name: "app · app.log" })).toBeInTheDocument()
  expect(screen.getByRole("option", { name: "error · unavailable" })).toBeInTheDocument()
})

test("supports warn plus error severity shortcut and enter-to-search", async () => {
  const user = userEvent.setup()
  renderPanel()

  await screen.findByText("gateway ready")
  await user.selectOptions(screen.getByLabelText("Severity"), "WARN_ERROR")
  await user.clear(screen.getByLabelText("Keyword"))
  await user.type(screen.getByLabelText("Keyword"), "stale route{Enter}")

  await waitFor(() => {
    expect(getApplicationLogEntriesMock).toHaveBeenLastCalledWith(expect.objectContaining({
      keyword: "stale route",
      levels: ["WARN", "ERROR"],
    }))
  })
})

test("presents node process log scope without duplicate visible headings", async () => {
  renderPanel()

  expect(await screen.findByRole("heading", { name: "Node Process Logs" })).toBeInTheDocument()
  expect(screen.getByText("WK_LOG_DIR process logs only")).toBeInTheDocument()
  expect(screen.getAllByRole("heading", { name: "Node Process Logs" })).toHaveLength(1)
  expect(screen.queryByText("ordinary WuKongIM process logs")).not.toBeInTheDocument()
})

test("uses node process log wording for empty states", async () => {
  const user = userEvent.setup()
  getApplicationLogEntriesMock.mockResolvedValue({
    node_id: 1,
    source: "app",
    cursor: "",
    rotated: false,
    items: [],
  })

  renderPanel()

  expect(await screen.findByText("No node process log lines were returned from WK_LOG_DIR.")).toBeInTheDocument()
  await user.type(screen.getByLabelText("Keyword"), "fatal")
  expect(await screen.findByText("No node process log lines from WK_LOG_DIR match the current filters.")).toBeInTheDocument()
})

test("renders returned log lines inside a terminal console surface", async () => {
  getApplicationLogEntriesMock.mockResolvedValueOnce({
    node_id: 1,
    source: "app",
    cursor: "cursor-1",
    rotated: false,
    items: [logEntry("gateway ready"), logEntry("listener backpressure", 2)],
  })

  renderPanel()

  expect(await screen.findByText("listener backpressure")).toBeInTheDocument()
  const log = screen.getByRole("log")
  const terminal = log.closest("[data-system-log-console='terminal']")

  expect(terminal).toHaveAttribute("data-system-log-console", "terminal")
  expect(screen.getByText("Console")).toBeInTheDocument()
  expect(within(terminal as HTMLElement).getByRole("log")).toBe(log)
  expect(within(log).getByText("INFO")).toBeInTheDocument()
  expect(within(log).getByText("WARN")).toBeInTheDocument()
})

test("keeps path-like log metadata from overlapping the console message", async () => {
  const user = userEvent.setup()
  getApplicationLogEntriesMock.mockResolvedValueOnce({
    node_id: 1,
    source: "app",
    cursor: "cursor-1",
    rotated: false,
    items: [pathLikeLogEntry()],
  })

  renderPanel()

  const log = await screen.findByRole("log")
  expect((await within(log).findAllByText("/src/internal/runtime/channelappend/append.go:84")).length).toBe(2)
  expect(within(log).getByText("UNKNOWN")).toBeInTheDocument()
  expect(log.querySelector("[data-system-log-entry='raw']")).toBeNull()
  await user.click(screen.getByRole("button", { name: "Show log details 3" }))
  expect((within(log).getAllByText("/src/internal/runtime/channelappend/append.go:84")).length).toBe(3)
  expect(log.querySelector("[data-system-log-entry='raw']")).not.toBeNull()
  expect(within(log).getByRole("button", { name: "Copy raw log 3" })).toBeInTheDocument()
})

test("renders compact log rows and hides raw details until expanded", async () => {
  const user = userEvent.setup()
  const entry = {
    ...logEntry("gateway ready"),
    fields: { listener: "tcp", request_id: "req-1", trace_id: "trace-1" },
  }
  getApplicationLogEntriesMock.mockResolvedValueOnce({
    node_id: 1,
    source: "app",
    cursor: "cursor-1",
    rotated: false,
    items: [entry],
  })

  renderPanel()

  expect(await screen.findByText("gateway ready")).toBeInTheDocument()
  expect(screen.getByText("gateway")).toBeInTheDocument()
  expect(screen.getByText("request_id=req-1")).toBeInTheDocument()
  expect(screen.queryByText("1 gateway ready")).not.toBeInTheDocument()

  await user.click(screen.getByRole("button", { name: "Show log details 1" }))
  expect(screen.getByText("1 gateway ready")).toBeInTheDocument()
  expect(screen.getByText(JSON.stringify(entry.fields))).toBeInTheDocument()
})

test("copies message and raw content from row actions", async () => {
  const user = userEvent.setup()
  const writeText = vi.fn().mockResolvedValue(undefined)
  Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true })

  renderPanel()

  await screen.findByText("gateway ready")
  await user.click(screen.getByRole("button", { name: "Copy log message 1" }))
  expect(writeText).toHaveBeenCalledWith("gateway ready")

  await user.click(screen.getByRole("button", { name: "Show log details 1" }))
  await user.click(screen.getByRole("button", { name: "Copy raw log 1" }))
  expect(writeText).toHaveBeenCalledWith("1 gateway ready")
})

test("load more expands the tail window instead of polling only newer lines", async () => {
  const user = userEvent.setup()
  getApplicationLogEntriesMock.mockResolvedValueOnce({
    node_id: 1,
    source: "app",
    cursor: "cursor-1",
    rotated: false,
    items: [logEntry("tail line", 10)],
  })
  getApplicationLogEntriesMock.mockResolvedValueOnce({
    node_id: 1,
    source: "app",
    cursor: "cursor-2",
    rotated: false,
    items: [logEntry("older line", 1), logEntry("tail line", 10)],
  })

  renderPanel()

  expect(await screen.findByText("tail line")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Load more" }))

  expect(await screen.findByText("older line")).toBeInTheDocument()
  await waitFor(() => {
    expect(getApplicationLogEntriesMock).toHaveBeenLastCalledWith(expect.objectContaining({
      nodeId: 1,
      source: "app",
      limit: 200,
    }))
  })
  expect(getApplicationLogEntriesMock.mock.lastCall?.[0]).not.toHaveProperty("cursor")
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

  await screen.findByRole("heading", { name: "Node Process Logs" })
  expect(await screen.findByText("0 lines")).toBeInTheDocument()
  await user.click(screen.getByLabelText("Follow tail"))

  expect((await screen.findAllByText("Log rotated; cursor moved to the new file.")).length).toBeGreaterThan(0)
  expect(await screen.findByText("Log rotated; cursor moved to the new file. · 1 new live line")).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Jump to latest logs" })).toBeInTheDocument()
  expect(await screen.findByText("live warning")).toBeInTheDocument()
  expect(screen.getAllByText("1 line").length).toBeGreaterThan(0)
  await waitFor(() => {
    expect(streamApplicationLogEntriesMock).toHaveBeenCalledWith(expect.objectContaining({
      nodeId: 1,
      source: "app",
      cursor: "cursor-1",
      limit: 100,
    }))
  })
})

test("shows live appended count and clears it from the console banner", async () => {
  const user = userEvent.setup()
  getApplicationLogEntriesMock.mockResolvedValueOnce({
    node_id: 1,
    source: "app",
    cursor: "cursor-1",
    rotated: false,
    items: [],
  })
  streamApplicationLogEntriesMock.mockResolvedValueOnce(new Response([
    JSON.stringify({ type: "line", cursor: "cursor-2", item: logEntry("live warning", 2) }),
  ].join("\n"), { status: 200 }))

  renderPanel()

  await user.click(await screen.findByLabelText("Follow tail"))
  expect(await screen.findByText("1 new live line")).toBeInTheDocument()

  await user.click(screen.getByRole("button", { name: "Jump to latest logs" }))
  expect(screen.queryByText("1 new live line")).not.toBeInTheDocument()
})
