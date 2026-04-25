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

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getConnections: (...args: unknown[]) => getConnectionsMock(...args),
    getConnection: (...args: unknown[]) => getConnectionMock(...args),
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

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  getConnectionsMock.mockReset()
  getConnectionMock.mockReset()
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

test("refreshes the connection inventory", async () => {
  getConnectionsMock.mockResolvedValueOnce({ total: 1, items: [connectionRow] })
  getConnectionsMock.mockResolvedValueOnce({ total: 1, items: [connectionRow] })

  const user = userEvent.setup()
  renderConnectionsPage()

  expect(await screen.findByText("u1")).toBeInTheDocument()
  await user.click(screen.getAllByRole("button", { name: "Refresh" })[0]!)

  expect(getConnectionsMock).toHaveBeenCalledTimes(2)
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

  expect((await screen.findAllByText("Connection Inventory")).length).toBeGreaterThan(0)
  expect(screen.getByText(/no manager data is available/i)).toBeInTheDocument()
})
