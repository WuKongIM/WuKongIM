import { render, screen } from "@testing-library/react"
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
