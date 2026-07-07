import { render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeEach, expect, test, vi } from "vitest"

import { createAnonymousAuthState, useAuthStore } from "@/auth/auth-store"
import { resetLocale } from "@/i18n/locale-store"
import { I18nProvider } from "@/i18n/provider"
import { ManagerApiError } from "@/lib/manager-api"
import { UsersPage } from "@/pages/users/page"

const getUsersMock = vi.fn()
const getUserMock = vi.fn()
const kickUserMock = vi.fn()
const resetUserTokenMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getUsers: (...args: unknown[]) => getUsersMock(...args),
    getUser: (...args: unknown[]) => getUserMock(...args),
    kickUser: (...args: unknown[]) => kickUserMock(...args),
    resetUserToken: (...args: unknown[]) => resetUserTokenMock(...args),
  }
})

const userRow = {
  uid: "u1",
  slot_id: 1,
  hash_slot: 7,
  online: true,
  online_device_count: 1,
  online_device_flags: ["app"],
  device_count: 1,
  token_set_count: 1,
}

const detail = {
  uid: "u1",
  slot_id: 1,
  hash_slot: 7,
  online: true,
  devices: [{
    device_flag: "app",
    device_level: "master",
    token_set: true,
    online: true,
    online_session_count: 1,
  }],
  connections: [{
    node_id: 2,
    session_id: 10,
    uid: "u1",
    device_id: "d1",
    device_flag: "app",
    device_level: "master",
    listener: "tcp",
    remote_addr: "",
    local_addr: "",
  }],
}

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  getUsersMock.mockReset()
  getUserMock.mockReset()
  kickUserMock.mockReset()
  resetUserTokenMock.mockReset()
  useAuthStore.setState({
    ...createAnonymousAuthState(),
    isHydrated: true,
    status: "authenticated",
    username: "admin",
    tokenType: "Bearer",
    accessToken: "token-1",
    expiresAt: "2099-04-22T12:00:00Z",
    permissions: [{ resource: "cluster.user", actions: ["r", "w"] }],
  })
})

function renderUsersPage() {
  return render(
    <I18nProvider>
      <UsersPage />
    </I18nProvider>,
  )
}

test("renders the first user page", async () => {
  getUsersMock.mockResolvedValueOnce({ items: [userRow], has_more: false })

  renderUsersPage()

  expect(await screen.findByText("u1")).toBeInTheDocument()
  expect(screen.getByText("app")).toBeInTheDocument()
  expect(getUsersMock).toHaveBeenCalledWith({ limit: 50 })
})

test("uses an editorial user inventory toolbar and table surface", async () => {
  getUsersMock.mockResolvedValueOnce({ items: [userRow], has_more: false })

  renderUsersPage()

  const table = await screen.findByRole("table", { name: "Users" })
  const inventorySurface = table.closest("[data-users-surface='inventory']")
  expect(inventorySurface).toHaveClass("overflow-x-auto", "rounded-md", "border", "border-border")
  expect(inventorySurface).not.toHaveClass("rounded-xl")

  const toolbar = screen.getByTestId("users-filter-toolbar")
  expect(toolbar).toHaveClass("border-b", "border-border", "pb-4")
  expect(within(toolbar).getByPlaceholderText("Search UID")).toBeInTheDocument()
  expect(within(toolbar).getByRole("button", { name: "Search" })).toBeInTheDocument()
  expect(within(toolbar).getByRole("button", { name: "Refresh" })).toBeInTheDocument()
})

test("searches users by UID", async () => {
  getUsersMock.mockResolvedValueOnce({ items: [], has_more: false })
  getUsersMock.mockResolvedValueOnce({ items: [{ ...userRow, uid: "bob" }], has_more: false })

  const user = userEvent.setup()
  renderUsersPage()

  await screen.findByText("No manager data is available for this view yet.")
  await user.type(screen.getByPlaceholderText("Search UID"), "bob")
  await user.click(screen.getByRole("button", { name: "Search" }))

  expect(await screen.findByText("bob")).toBeInTheDocument()
  expect(getUsersMock).toHaveBeenLastCalledWith({ keyword: "bob", limit: 50 })
})

test("loads more users with next cursor", async () => {
  getUsersMock.mockResolvedValueOnce({ items: [userRow], has_more: true, next_cursor: "cursor-1" })
  getUsersMock.mockResolvedValueOnce({ items: [{ ...userRow, uid: "u2" }], has_more: false })

  const user = userEvent.setup()
  renderUsersPage()

  expect(await screen.findByText("u1")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Load more" }))

  expect(await screen.findByText("u2")).toBeInTheDocument()
  expect(getUsersMock).toHaveBeenLastCalledWith({ cursor: "cursor-1", limit: 50 })
})

test("opens detail and runs user actions", async () => {
  getUsersMock.mockResolvedValue({ items: [userRow], has_more: false })
  getUserMock.mockResolvedValue(detail)
  kickUserMock.mockResolvedValue({ uid: "u1", device_flag: "all", changed: true })
  resetUserTokenMock.mockResolvedValue({ uid: "u1", device_flag: "app", device_level: "master", token: "next-token" })

  const user = userEvent.setup()
  renderUsersPage()

  await user.click(await screen.findByRole("button", { name: "Inspect user u1" }))
  expect(await screen.findByText("Session 10")).toBeInTheDocument()

  await user.click(screen.getByRole("button", { name: "Kick" }))
  await user.click(screen.getByRole("button", { name: "Confirm kick" }))
  expect(kickUserMock).toHaveBeenCalledWith("u1", { deviceFlag: "all" })

  await user.click(screen.getByRole("button", { name: "Reset token" }))
  await user.click(screen.getByRole("button", { name: "Reset token" }))
  expect(resetUserTokenMock).toHaveBeenCalledWith("u1", { deviceFlag: "app", deviceLevel: "master" })
  expect(await screen.findByText("next-token")).toBeInTheDocument()
})

test("maps permission and availability errors", async () => {
  getUsersMock.mockRejectedValueOnce(new ManagerApiError(403, "forbidden", "forbidden"))
  const { unmount } = renderUsersPage()

  expect(await screen.findByText("You do not have permission to view this manager resource.")).toBeInTheDocument()
  unmount()

  getUsersMock.mockRejectedValueOnce(new ManagerApiError(503, "service_unavailable", "unavailable"))
  renderUsersPage()

  expect(await screen.findByText("The manager service is currently unavailable.")).toBeInTheDocument()
})
