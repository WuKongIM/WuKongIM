import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeEach, expect, test, vi } from "vitest"

import { createAnonymousAuthState, useAuthStore } from "@/auth/auth-store"
import { resetLocale } from "@/i18n/locale-store"
import { I18nProvider } from "@/i18n/provider"
import { ManagerApiError } from "@/lib/manager-api"
import { DBInspectPage } from "@/pages/db-inspect/page"

const getNodesMock = vi.fn()
const getDBInspectTablesMock = vi.fn()
const getDBInspectTableMock = vi.fn()
const queryDBInspectMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getNodes: (...args: unknown[]) => getNodesMock(...args),
    getDBInspectTables: (...args: unknown[]) => getDBInspectTablesMock(...args),
    getDBInspectTable: (...args: unknown[]) => getDBInspectTableMock(...args),
    queryDBInspect: (...args: unknown[]) => queryDBInspectMock(...args),
  }
})

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  getNodesMock.mockReset()
  getDBInspectTablesMock.mockReset()
  getDBInspectTableMock.mockReset()
  queryDBInspectMock.mockReset()
  useAuthStore.setState({
    ...createAnonymousAuthState(),
    isHydrated: true,
    status: "authenticated",
    username: "admin",
    tokenType: "Bearer",
    accessToken: "token-1",
    expiresAt: "2099-06-17T12:00:00Z",
    permissions: [{ resource: "cluster.db", actions: ["r"] }],
  })
})

function renderPage() {
  return render(
    <I18nProvider>
      <DBInspectPage />
    </I18nProvider>,
  )
}

function nodesResponse() {
  return {
    generated_at: "2026-06-17T10:00:00Z",
    controller_leader_id: 1,
    total: 1,
    items: [
      {
        node_id: 1,
        name: "node-1",
        addr: "127.0.0.1:7001",
        status: "online",
        last_heartbeat_at: "2026-06-17T10:00:00Z",
        is_local: true,
        capacity_weight: 100,
        membership: { role: "voter", join_state: "joined", schedulable: true },
        health: { status: "online", last_heartbeat_at: "2026-06-17T10:00:00Z" },
        controller: { role: "leader", voter: true },
        slot_stats: { count: 1, leader_count: 1 },
        slots: { leader_count: 1, replica_count: 1, follower_count: 0, quorum_lost_count: 0, unreported_count: 0 },
        runtime: {
          node_id: 1,
          active_online: 0,
          closing_online: 0,
          total_online: 0,
          gateway_sessions: 0,
          sessions_by_listener: {},
          accepting_new_sessions: true,
          draining: false,
          unknown: false,
        },
        actions: {
          can_drain: false,
          can_resume: false,
          can_scale_in: false,
          can_onboard: false,
          can_move_slots_in: false,
          can_move_slots_out: false,
          can_promote_controller_voter: false,
        },
      },
    ],
  }
}

test("loads table list and runs a query", async () => {
  getNodesMock.mockResolvedValueOnce(nodesResponse())
  getDBInspectTablesMock.mockResolvedValueOnce({
    node_id: 1,
    generated_at: "2026-06-17T10:00:00Z",
    rows: [{ domain: "meta", name: "user", table: "meta.user" }],
    stats: { scan_mode: "", scanned_hash_slots: [], scanned_rows: 0, returned_rows: 1, has_more: false, next_cursor: "" },
  })
  queryDBInspectMock.mockResolvedValueOnce({
    node_id: 1,
    generated_at: "2026-06-17T10:00:01Z",
    rows: [{ uid: "u1", token: "t1" }],
    stats: { scan_mode: "point-partition", scanned_hash_slots: [3], scanned_rows: 1, returned_rows: 1, has_more: false, next_cursor: "" },
  })

  const user = userEvent.setup()
  renderPage()

  expect(await screen.findByText("meta.user")).toBeInTheDocument()
  await user.clear(screen.getByLabelText("Inspect query"))
  await user.type(screen.getByLabelText("Inspect query"), "select uid, token from meta.user where uid='u1' limit 20")
  await user.click(screen.getByRole("button", { name: "Run query" }))

  expect(queryDBInspectMock).toHaveBeenCalledWith({
    node_id: 1,
    query: "select uid, token from meta.user where uid='u1' limit 20",
  })
  expect(await screen.findByText("u1")).toBeInTheDocument()
  expect(screen.getByText("point-partition")).toBeInTheDocument()
})

test("uses editorial db inspect rail workbench and named result tables", async () => {
  getNodesMock.mockResolvedValueOnce(nodesResponse())
  getDBInspectTablesMock.mockResolvedValueOnce({
    node_id: 1,
    generated_at: "2026-06-17T10:00:00Z",
    rows: [{ domain: "meta", name: "user", table: "meta.user" }],
    stats: { scan_mode: "", scanned_hash_slots: [], scanned_rows: 0, returned_rows: 1, has_more: false, next_cursor: "" },
  })
  getDBInspectTableMock.mockResolvedValueOnce({
    node_id: 1,
    generated_at: "2026-06-17T10:00:00Z",
    rows: [{ column: "uid", type: "string" }],
    stats: { scan_mode: "", scanned_hash_slots: [], scanned_rows: 1, returned_rows: 1, has_more: false, next_cursor: "" },
  })
  queryDBInspectMock.mockResolvedValueOnce({
    node_id: 1,
    generated_at: "2026-06-17T10:00:01Z",
    rows: [{ uid: "u1" }],
    stats: { scan_mode: "point-partition", scanned_hash_slots: [3], scanned_rows: 1, returned_rows: 1, has_more: false, next_cursor: "" },
  })

  const user = userEvent.setup()
  renderPage()

  const rail = await screen.findByTestId("db-inspect-table-rail")
  expect(rail).toHaveClass("overflow-hidden")

  await user.click(await screen.findByRole("button", { name: "Inspect meta.user" }))
  const describeTable = await screen.findByRole("table", { name: "meta.user columns" })
  const describeSurface = describeTable.closest("[data-db-inspect-surface='describe']")
  expect(describeSurface).toHaveClass("overflow-x-auto", "rounded-md", "border", "border-border")

  const workbench = screen.getByTestId("db-inspect-query-workbench")
  expect(workbench).toHaveClass("border-b", "border-border", "pb-4")
  expect(within(workbench).getByLabelText("Inspect query")).toBeInTheDocument()

  await user.clear(screen.getByLabelText("Inspect query"))
  await user.type(screen.getByLabelText("Inspect query"), "select uid from meta.user limit 20")
  await user.click(screen.getByRole("button", { name: "Run query" }))

  const resultTable = await screen.findByRole("table", { name: "Results" })
  const resultSurface = resultTable.closest("[data-db-inspect-surface='results']")
  expect(resultSurface).toHaveClass("overflow-x-auto", "rounded-md", "border", "border-border")
  expect(resultTable).toHaveClass("text-sm")
  expect(screen.getByTestId("db-inspect-stats-strip")).toHaveClass("border-b", "border-border", "pb-3")
})

test("describes a selected table", async () => {
  getNodesMock.mockResolvedValueOnce(nodesResponse())
  getDBInspectTablesMock.mockResolvedValueOnce({
    node_id: 1,
    generated_at: "2026-06-17T10:00:00Z",
    rows: [{ domain: "message", name: "message", table: "message.message" }],
    stats: { scan_mode: "", scanned_hash_slots: [], scanned_rows: 0, returned_rows: 1, has_more: false, next_cursor: "" },
  })
  getDBInspectTableMock.mockResolvedValueOnce({
    node_id: 1,
    generated_at: "2026-06-17T10:00:00Z",
    rows: [{ column: "message_seq", type: "uint64" }],
    stats: { scan_mode: "", scanned_hash_slots: [], scanned_rows: 0, returned_rows: 1, has_more: false, next_cursor: "" },
  })

  const user = userEvent.setup()
  renderPage()

  await user.click(await screen.findByRole("button", { name: "Inspect message.message" }))
  expect(getDBInspectTableMock).toHaveBeenCalledWith("message", "message", { nodeId: 1 })
  expect(await screen.findByText("message_seq")).toBeInTheDocument()
  expect(screen.getByText("uint64")).toBeInTheDocument()
})

test("runs next page with cursor", async () => {
  getNodesMock.mockResolvedValueOnce(nodesResponse())
  getDBInspectTablesMock.mockResolvedValueOnce({
    node_id: 1,
    generated_at: "2026-06-17T10:00:00Z",
    rows: [],
    stats: { scan_mode: "", scanned_hash_slots: [], scanned_rows: 0, returned_rows: 0, has_more: false, next_cursor: "" },
  })
  queryDBInspectMock.mockResolvedValueOnce({
    node_id: 1,
    generated_at: "2026-06-17T10:00:01Z",
    rows: [{ uid: "u1" }],
    stats: { scan_mode: "local-bounded", scanned_hash_slots: [1], scanned_rows: 1, returned_rows: 1, has_more: true, next_cursor: "abc" },
  })
  queryDBInspectMock.mockResolvedValueOnce({
    node_id: 1,
    generated_at: "2026-06-17T10:00:02Z",
    rows: [{ uid: "u2" }],
    stats: { scan_mode: "local-bounded", scanned_hash_slots: [2], scanned_rows: 1, returned_rows: 1, has_more: false, next_cursor: "" },
  })

  const user = userEvent.setup()
  renderPage()

  await user.click(screen.getByRole("button", { name: "Run query" }))
  await screen.findByText("u1")
  await user.click(screen.getByRole("button", { name: "Next page" }))

  expect(queryDBInspectMock).toHaveBeenLastCalledWith({
    node_id: 1,
    query: "show tables cursor 'abc'",
  })
  expect(await screen.findByText("u2")).toBeInTheDocument()
})

test("replaces an existing cursor clause before appending next cursor", async () => {
  getNodesMock.mockResolvedValueOnce(nodesResponse())
  getDBInspectTablesMock.mockResolvedValueOnce({
    node_id: 1,
    generated_at: "2026-06-17T10:00:00Z",
    rows: [],
    stats: { scan_mode: "", scanned_hash_slots: [], scanned_rows: 0, returned_rows: 0, has_more: false, next_cursor: "" },
  })
  queryDBInspectMock.mockResolvedValueOnce({
    node_id: 1,
    generated_at: "2026-06-17T10:00:01Z",
    rows: [{ uid: "u1" }],
    stats: { scan_mode: "local-bounded", scanned_hash_slots: [1], scanned_rows: 1, returned_rows: 1, has_more: true, next_cursor: "new-cursor" },
  })
  queryDBInspectMock.mockResolvedValueOnce({
    node_id: 1,
    generated_at: "2026-06-17T10:00:02Z",
    rows: [{ uid: "u2" }],
    stats: { scan_mode: "local-bounded", scanned_hash_slots: [2], scanned_rows: 1, returned_rows: 1, has_more: false, next_cursor: "" },
  })

  const user = userEvent.setup()
  renderPage()

  await user.clear(screen.getByLabelText("Inspect query"))
  await user.type(screen.getByLabelText("Inspect query"), "select * from meta.user cursor 'old-cursor' limit 20")
  await user.click(screen.getByRole("button", { name: "Run query" }))
  await screen.findByText("u1")
  await user.click(screen.getByRole("button", { name: "Next page" }))

  expect(queryDBInspectMock).toHaveBeenLastCalledWith({
    node_id: 1,
    query: "select * from meta.user limit 20 cursor 'new-cursor'",
  })
})

test("keeps cursor text inside quoted query literals", async () => {
  getNodesMock.mockResolvedValueOnce(nodesResponse())
  getDBInspectTablesMock.mockResolvedValueOnce({
    node_id: 1,
    generated_at: "2026-06-17T10:00:00Z",
    rows: [],
    stats: { scan_mode: "", scanned_hash_slots: [], scanned_rows: 0, returned_rows: 0, has_more: false, next_cursor: "" },
  })
  queryDBInspectMock.mockResolvedValueOnce({
    node_id: 1,
    generated_at: "2026-06-17T10:00:01Z",
    rows: [{ uid: "has cursor abc" }],
    stats: { scan_mode: "local-bounded", scanned_hash_slots: [1], scanned_rows: 1, returned_rows: 1, has_more: true, next_cursor: "new-cursor" },
  })
  queryDBInspectMock.mockResolvedValueOnce({
    node_id: 1,
    generated_at: "2026-06-17T10:00:02Z",
    rows: [{ uid: "next" }],
    stats: { scan_mode: "local-bounded", scanned_hash_slots: [2], scanned_rows: 1, returned_rows: 1, has_more: false, next_cursor: "" },
  })

  const user = userEvent.setup()
  renderPage()

  await user.clear(screen.getByLabelText("Inspect query"))
  await user.type(screen.getByLabelText("Inspect query"), "select * from meta.user where uid='has cursor abc' limit 20")
  await user.click(screen.getByRole("button", { name: "Run query" }))
  await screen.findByText("has cursor abc")
  await user.click(screen.getByRole("button", { name: "Next page" }))

  expect(queryDBInspectMock).toHaveBeenLastCalledWith({
    node_id: 1,
    query: "select * from meta.user where uid='has cursor abc' limit 20 cursor 'new-cursor'",
  })
})

test("keeps query text when the query fails", async () => {
  getNodesMock.mockResolvedValueOnce(nodesResponse())
  getDBInspectTablesMock.mockResolvedValueOnce({
    node_id: 1,
    generated_at: "2026-06-17T10:00:00Z",
    rows: [],
    stats: { scan_mode: "", scanned_hash_slots: [], scanned_rows: 0, returned_rows: 0, has_more: false, next_cursor: "" },
  })
  queryDBInspectMock.mockRejectedValueOnce(new ManagerApiError(400, "invalid_request", "invalid db inspect query"))

  const user = userEvent.setup()
  renderPage()

  await user.clear(screen.getByLabelText("Inspect query"))
  await user.type(screen.getByLabelText("Inspect query"), "select * from meta.user order by uid")
  await user.click(screen.getByRole("button", { name: "Run query" }))

  expect(await screen.findByText("invalid db inspect query")).toBeInTheDocument()
  expect(screen.getByLabelText("Inspect query")).toHaveValue("select * from meta.user order by uid")
})
