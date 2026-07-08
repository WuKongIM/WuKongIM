import { render, screen, within } from "@testing-library/react"
import { beforeEach, expect, test, vi } from "vitest"

import { createAnonymousAuthState, useAuthStore } from "@/auth/auth-store"
import { resetLocale } from "@/i18n/locale-store"
import { I18nProvider } from "@/i18n/provider"
import { ManagerApiError } from "@/lib/manager-api"
import { PermissionsPage } from "@/pages/settings/permissions/page"

const getPermissionsMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getPermissions: (...args: unknown[]) => getPermissionsMock(...args),
  }
})

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  getPermissionsMock.mockReset()
  useAuthStore.setState({
    ...createAnonymousAuthState(),
    isHydrated: true,
    status: "authenticated",
    username: "admin",
    tokenType: "Bearer",
    accessToken: "token-1",
    expiresAt: "2099-04-22T12:00:00Z",
    permissions: [{ resource: "cluster.permission", actions: ["r"] }],
  })
})

function renderPermissionsPage() {
  return render(
    <I18nProvider>
      <PermissionsPage />
    </I18nProvider>,
  )
}

test("renders auth summary, users, and permission catalog", async () => {
  getPermissionsMock.mockResolvedValueOnce({
    auth_enabled: true,
    current_user: "admin",
    users: [
      { username: "admin", permissions: [{ resource: "*", actions: ["*"] }] },
      { username: "viewer", permissions: [{ resource: "cluster.node", actions: ["r"] }] },
    ],
    resources: [
      { resource: "cluster.permission", actions: ["r"], description: "Read manager authentication and permission configuration snapshots." },
      { resource: "cluster.node", actions: ["r", "w"], description: "Read node inventory and perform node lifecycle actions." },
    ],
  })

  renderPermissionsPage()

  expect(await screen.findByText("Auth enabled")).toBeInTheDocument()
  expect(screen.getByText("Current user: admin")).toBeInTheDocument()
  expect(screen.getByText("admin")).toBeInTheDocument()
  expect(screen.getByText("viewer")).toBeInTheDocument()
  expect(screen.getByText("*:*")).toBeInTheDocument()
  expect(screen.getByText("cluster.node:r")).toBeInTheDocument()
  expect(screen.getByText("cluster.permission")).toBeInTheDocument()
  expect(screen.getByText("r / w")).toBeInTheDocument()
})

test("uses compact permission summary and named table surfaces", async () => {
  getPermissionsMock.mockResolvedValueOnce({
    auth_enabled: true,
    current_user: "admin",
    users: [
      { username: "admin", permissions: [{ resource: "*", actions: ["*"] }] },
      { username: "viewer", permissions: [{ resource: "cluster.node", actions: ["r"] }] },
    ],
    resources: [
      { resource: "cluster.permission", actions: ["r"], description: "Read manager authentication and permission configuration snapshots." },
      { resource: "cluster.node", actions: ["r", "w"], description: "Read node inventory and perform node lifecycle actions." },
    ],
  })

  renderPermissionsPage()

  const summaryStrip = await screen.findByTestId("permissions-summary-strip")
  expect(summaryStrip).toHaveClass("grid", "overflow-hidden", "rounded-md", "border", "border-border", "bg-card")
  expect(summaryStrip.querySelectorAll("[data-permission-summary-cell]")).toHaveLength(4)
  expect(summaryStrip.querySelector("[data-permission-summary-cell]")).not.toHaveClass("rounded-lg")

  const readonlyNotice = screen.getByTestId("permissions-readonly-notice")
  expect(readonlyNotice).toHaveClass("border-t", "border-border", "pt-3")

  const usersTable = screen.getByRole("table", { name: "Static Manager Users" })
  const usersSurface = usersTable.closest("[data-permissions-surface='users']")
  expect(usersSurface).toHaveClass("overflow-x-auto", "rounded-md", "border", "border-border")
  expect(usersTable).toHaveClass("text-sm")
  expect(within(usersTable).getByText("viewer")).toBeInTheDocument()

  const catalogTable = screen.getByRole("table", { name: "Permission Catalog" })
  const catalogSurface = catalogTable.closest("[data-permissions-surface='catalog']")
  expect(catalogSurface).toHaveClass("overflow-x-auto", "rounded-md", "border", "border-border")
  expect(catalogTable).toHaveClass("text-sm")
  expect(within(catalogTable).getByText("cluster.permission")).toBeInTheDocument()
})

test("renders auth disabled with empty static users", async () => {
  getPermissionsMock.mockResolvedValueOnce({
    auth_enabled: false,
    current_user: "",
    users: [],
    resources: [{ resource: "*", actions: ["*"], description: "Wildcard access to all manager resources and actions." }],
  })

  renderPermissionsPage()

  expect(await screen.findByText("Auth disabled")).toBeInTheDocument()
  expect(screen.getByText("No static manager users are visible for this configuration.")).toBeInTheDocument()
  expect(screen.getAllByText("*")).toHaveLength(2)
})

test("maps forbidden and unavailable errors", async () => {
  getPermissionsMock.mockRejectedValueOnce(new ManagerApiError(403, "forbidden", "forbidden"))
  const { unmount } = renderPermissionsPage()

  expect(await screen.findByText("You do not have permission to view this manager resource.")).toBeInTheDocument()
  unmount()

  getPermissionsMock.mockRejectedValueOnce(new ManagerApiError(503, "service_unavailable", "unavailable"))
  renderPermissionsPage()

  expect(await screen.findByText("The manager service is currently unavailable.")).toBeInTheDocument()
})
