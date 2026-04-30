import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeEach, expect, test, vi } from "vitest"

import { createAnonymousAuthState, useAuthStore } from "@/auth/auth-store"
import { I18nProvider } from "@/i18n/provider"
import { resetLocale } from "@/i18n/locale-store"
import { ManagerApiError } from "@/lib/manager-api"
import { ConnectionsPage } from "@/pages/connections/page"

const getConnectionsMock = vi.fn()
const getConnectionMock = vi.fn()
const getNodesMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getConnections: (...args: unknown[]) => getConnectionsMock(...args),
    getConnection: (...args: unknown[]) => getConnectionMock(...args),
    getNodes: (...args: unknown[]) => getNodesMock(...args),
  }
})

const connectionRow = {
  session_id: 101,
  uid: "u1",
  device_id: "device-a",
  device_flag: "app",
  device_level: "master",
  slot_id: 9,
  state: "active",
  listener: "tcp",
  connected_at: "2026-04-23T08:00:00Z",
  remote_addr: "10.0.0.1:5000",
  local_addr: "127.0.0.1:7000",
}

const connectionDetail = {
  ...connectionRow,
  state: "closing",
}

const nodeRow = {
  node_id: 1,
  name: "node-1",
  addr: "127.0.0.1:7001",
  status: "alive",
  last_heartbeat_at: "2026-04-23T08:00:00Z",
  is_local: false,
  capacity_weight: 1,
  controller: { role: "follower", voter: true, leader_id: 2 },
  slot_stats: { count: 1, leader_count: 0 },
}

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  getConnectionsMock.mockReset()
  getConnectionMock.mockReset()
  getNodesMock.mockReset()
  getNodesMock.mockResolvedValue({
    generated_at: "2026-04-23T08:00:00Z",
    controller_leader_id: 2,
    total: 2,
    items: [
      nodeRow,
      { ...nodeRow, node_id: 2, name: "node-2", is_local: true, controller: { role: "leader", voter: true, leader_id: 2 } },
    ],
  })
  useAuthStore.setState({
    ...createAnonymousAuthState(),
    isHydrated: true,
    status: "authenticated",
    username: "admin",
    tokenType: "Bearer",
    accessToken: "token-1",
    expiresAt: "2099-04-22T12:00:00Z",
    permissions: [{ resource: "cluster.connection", actions: ["r"] }],
  })
})

function renderConnectionsPage() {
  return render(
    <I18nProvider>
      <ConnectionsPage />
    </I18nProvider>,
  )
}

test("uses compact connection page chrome without summary cards", async () => {
  getConnectionsMock.mockResolvedValueOnce({ total: 1, items: [connectionRow] })

  renderConnectionsPage()

  expect(await screen.findByText("u1")).toBeInTheDocument()
  expect(screen.getByText("Total: 1")).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Refresh" })).toBeInTheDocument()
  expect(screen.queryByText("Scope: local node")).not.toBeInTheDocument()
  expect(screen.queryByText("Connection inventory and transport state.")).not.toBeInTheDocument()
  expect(screen.queryByText("Sessions")).not.toBeInTheDocument()
  expect(screen.queryByText("Local sessions currently listed by the manager API.")).not.toBeInTheDocument()
  expect(screen.queryByText("Users")).not.toBeInTheDocument()
  expect(screen.queryByText("Distinct user IDs represented in the local view.")).not.toBeInTheDocument()
  expect(screen.queryByText("Slots")).not.toBeInTheDocument()
  expect(screen.queryByText("Distinct slot IDs represented in the local view.")).not.toBeInTheDocument()
  expect(screen.queryByText("Listeners")).not.toBeInTheDocument()
  expect(screen.queryByText("Listener types represented in the local view.")).not.toBeInTheDocument()
  expect(screen.queryByText("Connection Inventory")).not.toBeInTheDocument()
  expect(screen.queryByText("Current local connection records from the manager connections endpoints.")).not.toBeInTheDocument()
  expect(screen.queryByText("Local connections")).not.toBeInTheDocument()
  expect(screen.queryByText("Inspect one connection to view addresses, slot ownership, and session metadata.")).not.toBeInTheDocument()
})

test("renders connection rows and opens detail from manager APIs", async () => {
  getConnectionsMock.mockResolvedValueOnce({ total: 1, items: [connectionRow] })
  getConnectionMock.mockResolvedValueOnce(connectionDetail)

  const user = userEvent.setup()
  renderConnectionsPage()

  expect(await screen.findByText("u1")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Inspect connection 101" }))

  expect(await screen.findByText("Remote address")).toBeInTheDocument()
  expect(getConnectionMock).toHaveBeenCalledWith(101)
})

test("defaults connection node filter to the local node and reloads when it changes", async () => {
  getConnectionsMock.mockResolvedValueOnce({ total: 1, items: [connectionRow] })
  getConnectionsMock.mockResolvedValueOnce({
    total: 1,
    items: [{ ...connectionRow, session_id: 202, uid: "u2" }],
  })

  const user = userEvent.setup()
  renderConnectionsPage()

  const filter = await screen.findByLabelText("Node filter")
  expect(filter).toHaveValue("2")
  expect(await screen.findByText("u1")).toBeInTheDocument()
  expect(getConnectionsMock).toHaveBeenCalledWith({ nodeId: 2 })

  await user.selectOptions(filter, "1")

  expect(await screen.findByText("u2")).toBeInTheDocument()
  expect(getConnectionsMock).toHaveBeenLastCalledWith({ nodeId: 1 })
})

test("refreshes the connection inventory", async () => {
  getConnectionsMock.mockResolvedValueOnce({ total: 1, items: [connectionRow] })
  getConnectionsMock.mockResolvedValueOnce({ total: 1, items: [connectionRow] })

  const user = userEvent.setup()
  renderConnectionsPage()

  expect(await screen.findByText("u1")).toBeInTheDocument()
  await user.click(screen.getAllByRole("button", { name: "Refresh" })[0]!)

  expect(getConnectionsMock).toHaveBeenCalledTimes(2)
  expect(getConnectionsMock).toHaveBeenLastCalledWith({ nodeId: 2 })
})

test("renders unavailable state when connection data cannot be loaded", async () => {
  getConnectionsMock.mockRejectedValueOnce(
    new ManagerApiError(503, "service_unavailable", "management not configured"),
  )

  renderConnectionsPage()

  expect(await screen.findByText(/currently unavailable/i)).toBeInTheDocument()
})

test("renders empty state when there are no local connections", async () => {
  getConnectionsMock.mockResolvedValueOnce({ total: 0, items: [] })

  renderConnectionsPage()

  expect(await screen.findByRole("heading", { name: "Connections" })).toBeInTheDocument()
  expect(await screen.findByText(/no manager data is available/i)).toBeInTheDocument()
})
