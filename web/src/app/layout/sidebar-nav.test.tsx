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

test("marks the current navigation item with aria-current", async () => {
  const router = createMemoryRouter(routes, { initialEntries: ["/slots"] })

  render(
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>,
  )

  expect(await screen.findByRole("link", { name: "Slots" })).toHaveAttribute(
    "aria-current",
    "page",
  )
})

test("renders sidebar links without description copy", async () => {
  const router = createMemoryRouter(routes, { initialEntries: ["/slots"] })

  render(
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>,
  )

  expect(await screen.findByRole("link", { name: "Slots" })).toBeInTheDocument()
  expect(screen.queryAllByText("Slot distribution and status shell.")).toHaveLength(0)
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

test("renders Chinese navigation labels and cluster context when locale is zh-CN", async () => {
  localStorage.setItem("wukongim_manager_locale", "zh-CN")
  const router = createMemoryRouter(routes, { initialEntries: ["/dashboard"] })

  render(
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>,
  )

  expect(await screen.findByRole("link", { name: "仪表盘" })).toBeInTheDocument()
  expect(screen.getByText("运行时")).toBeInTheDocument()
  expect(screen.getByText("单节点集群")).toBeInTheDocument()
})
