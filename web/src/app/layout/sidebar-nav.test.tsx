import { render, screen, within } from "@testing-library/react"
import { RouterProvider, createMemoryRouter } from "react-router-dom"
import { beforeEach } from "vitest"

import { AppProviders } from "@/app/providers"
import { routes } from "@/app/router"
import { useAuthStore } from "@/auth/auth-store"
import { resetLocale } from "@/i18n/locale-store"

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  useAuthStore.setState({
    status: "authenticated",
    isHydrated: true,
    username: "admin",
    tokenType: "Bearer",
    accessToken: "token-1",
    expiresAt: "2099-04-22T12:00:00Z",
    permissions: [],
  })
})

test("shows only the active section navigation items", async () => {
  const router = createMemoryRouter(routes, { initialEntries: ["/cluster/nodes"] })

  render(
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>,
  )

  expect(await screen.findByRole("link", { name: "Nodes" })).toHaveAttribute("aria-current", "page")
  expect(screen.getByRole("link", { name: "Slots" })).toBeInTheDocument()
  expect(screen.getByRole("link", { name: "Channel Cluster" })).toBeInTheDocument()
  expect(screen.queryByRole("link", { name: "Users" })).not.toBeInTheDocument()
})

test("keeps the cluster context visible in the sidebar", async () => {
  const router = createMemoryRouter(routes, { initialEntries: ["/dashboard"] })

  render(
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>,
  )

  expect(await screen.findByText("Cluster status")).toBeInTheDocument()
  expect(screen.getByText("Single-node cluster")).toBeInTheDocument()
})

test("keeps the primary menu outside the scrollable content area", async () => {
  const router = createMemoryRouter(routes, { initialEntries: ["/dashboard"] })

  render(
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>,
  )

  const main = await screen.findByRole("main")
  const contentFrame = main.parentElement
  const appShell = contentFrame?.parentElement

  expect(appShell).toHaveClass("h-screen", "overflow-hidden")
  expect(contentFrame).toHaveClass("min-h-0", "flex-1")
  expect(main).toHaveClass("min-h-0", "overflow-y-auto")
  expect(screen.getByRole("navigation", { name: "Primary navigation" }).parentElement).toBe(contentFrame)
})

test("shows the cockpit health context in the topbar", async () => {
  const router = createMemoryRouter(routes, { initialEntries: ["/dashboard"] })

  render(
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>,
  )

  expect((await screen.findAllByText("Operations cockpit")).length).toBeGreaterThan(0)
  expect(screen.getByText("Single-node cluster · healthy")).toBeInTheDocument()
  expect(screen.queryByRole("button", { name: "Search" })).not.toBeInTheDocument()
})

test("renders Chinese navigation labels and cluster context", async () => {
  localStorage.setItem("wukongim_manager_locale", "zh-CN")
  const router = createMemoryRouter(routes, { initialEntries: ["/cluster/nodes"] })

  render(
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>,
  )

  expect(await screen.findByRole("link", { name: "节点" })).toHaveAttribute("aria-current", "page")
  expect(screen.getAllByText("集群运维").length).toBeGreaterThan(0)
  expect(screen.getByText("单节点集群")).toBeInTheDocument()
})


test("omits the cluster dashboard menu item from the cluster section", async () => {
  const router = createMemoryRouter(routes, { initialEntries: ["/cluster/dashboard"] })

  render(
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>,
  )

  const nav = await screen.findByRole("navigation", { name: "Primary navigation" })
  const links = within(nav).getAllByRole("link").map((link) => link.textContent)
  expect(links).not.toContain("Dashboard")
  expect(links.slice(0, 2)).toEqual(["Live Monitor", "Nodes"])
})

test("omits the business dashboard menu item from the business section", async () => {
  const router = createMemoryRouter(routes, { initialEntries: ["/business/dashboard"] })

  render(
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>,
  )

  const nav = await screen.findByRole("navigation", { name: "Primary navigation" })
  const links = within(nav).getAllByRole("link").map((link) => link.textContent)
  expect(links).not.toContain("Dashboard")
  expect(links.slice(0, 2)).toEqual(["Live Monitor", "Connections"])
})

test("moves connections from the system section into business management", async () => {
  const businessRouter = createMemoryRouter(routes, { initialEntries: ["/business/connections"] })

  const { unmount } = render(
    <AppProviders>
      <RouterProvider router={businessRouter} />
    </AppProviders>,
  )

  expect(await screen.findByRole("link", { name: "Connections" })).toHaveAttribute("aria-current", "page")
  expect(screen.getByRole("link", { name: "Connections" })).toHaveAttribute("href", "/business/connections")
  unmount()

  const systemRouter = createMemoryRouter(routes, { initialEntries: ["/system/permissions"] })

  render(
    <AppProviders>
      <RouterProvider router={systemRouter} />
    </AppProviders>,
  )

  expect(await screen.findByRole("link", { name: "Permissions" })).toBeInTheDocument()
  expect(screen.queryByRole("link", { name: "Connections" })).not.toBeInTheDocument()
})
