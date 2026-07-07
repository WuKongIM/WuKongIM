import { render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeEach, expect, test, vi } from "vitest"

import { createAnonymousAuthState, useAuthStore } from "@/auth/auth-store"
import { resetLocale } from "@/i18n/locale-store"
import { I18nProvider } from "@/i18n/provider"
import { ManagerApiError } from "@/lib/manager-api"
import { SystemUsersPage } from "@/pages/system-users/page"

const getSystemUsersMock = vi.fn()
const addSystemUsersMock = vi.fn()
const removeSystemUsersMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getSystemUsers: (...args: unknown[]) => getSystemUsersMock(...args),
    addSystemUsers: (...args: unknown[]) => addSystemUsersMock(...args),
    removeSystemUsers: (...args: unknown[]) => removeSystemUsersMock(...args),
  }
})

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  getSystemUsersMock.mockReset()
  addSystemUsersMock.mockReset()
  removeSystemUsersMock.mockReset()
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

function renderSystemUsersPage() {
  return render(
    <I18nProvider>
      <SystemUsersPage />
    </I18nProvider>,
  )
}

test("renders persisted system users", async () => {
  getSystemUsersMock.mockResolvedValueOnce({ items: [{ uid: "sys-a" }], total: 1 })

  renderSystemUsersPage()

  expect(await screen.findByText("sys-a")).toBeInTheDocument()
  expect(screen.getByText("1 persisted UID")).toBeInTheDocument()
})

test("uses an editorial system users metadata row and named table", async () => {
  getSystemUsersMock.mockResolvedValueOnce({ items: [{ uid: "sys-a" }], total: 1 })

  renderSystemUsersPage()

  const table = await screen.findByRole("table", { name: "Persisted system UIDs" })
  const inventorySurface = table.closest("[data-system-users-surface='inventory']")
  expect(inventorySurface).toHaveClass("overflow-x-auto", "rounded-md", "border", "border-border")
  expect(inventorySurface).not.toHaveClass("rounded-xl")
  expect(table).toHaveClass("text-sm")

  const metadata = screen.getByTestId("system-users-metadata-row")
  expect(metadata).toHaveClass("border-b", "border-border", "pb-4")
  expect(within(metadata).getByText("1 persisted UID")).toBeInTheDocument()
  expect(within(metadata).getByText("Cache-only UIDs are excluded until they are persisted.")).toBeInTheDocument()
})

test("adds normalized system users and refreshes", async () => {
  getSystemUsersMock.mockResolvedValueOnce({ items: [], total: 0 })
  getSystemUsersMock.mockResolvedValueOnce({ items: [{ uid: "sys-a" }, { uid: "sys-b" }], total: 2 })
  addSystemUsersMock.mockResolvedValueOnce({ uids: ["sys-a", "sys-b"], changed: true })

  const user = userEvent.setup()
  renderSystemUsersPage()

  await screen.findByText("No manager data is available for this view yet.")
  await user.click(screen.getByRole("button", { name: "Add system UIDs" }))
  await user.type(screen.getByLabelText("UIDs"), "sys-a, sys-b\nsys-a")
  await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Add system UIDs" }))

  expect(addSystemUsersMock).toHaveBeenCalledWith({ uids: ["sys-a", "sys-b"] })
  expect(await screen.findByText("sys-b")).toBeInTheDocument()
})

test("removes one system user after confirmation", async () => {
  getSystemUsersMock.mockResolvedValue({ items: [{ uid: "sys-a" }], total: 1 })
  removeSystemUsersMock.mockResolvedValueOnce({ uids: ["sys-a"], changed: true })

  const user = userEvent.setup()
  renderSystemUsersPage()

  await screen.findByText("sys-a")
  await user.click(screen.getByRole("button", { name: "Remove system UID sys-a" }))
  await user.click(screen.getByRole("button", { name: "Confirm remove" }))

  expect(removeSystemUsersMock).toHaveBeenCalledWith({ uids: ["sys-a"] })
})

test("validates empty add input", async () => {
  getSystemUsersMock.mockResolvedValueOnce({ items: [], total: 0 })

  const user = userEvent.setup()
  renderSystemUsersPage()

  await screen.findByText("No manager data is available for this view yet.")
  await user.click(screen.getByRole("button", { name: "Add system UIDs" }))
  await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Add system UIDs" }))

  expect(await screen.findByText("Enter at least one UID.")).toBeInTheDocument()
  expect(addSystemUsersMock).not.toHaveBeenCalled()
})

test("maps permission and availability errors", async () => {
  getSystemUsersMock.mockRejectedValueOnce(new ManagerApiError(403, "forbidden", "forbidden"))
  const { unmount } = renderSystemUsersPage()

  expect(await screen.findByText("You do not have permission to view this manager resource.")).toBeInTheDocument()
  unmount()

  getSystemUsersMock.mockRejectedValueOnce(new ManagerApiError(503, "service_unavailable", "unavailable"))
  renderSystemUsersPage()

  expect(await screen.findByText("The manager service is currently unavailable.")).toBeInTheDocument()
})
