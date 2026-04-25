import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { RouterProvider, createMemoryRouter } from "react-router-dom"
import { beforeEach, expect, test } from "vitest"

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

test("shows the current route title, description, and logged-in username", async () => {
  const router = createMemoryRouter(routes, { initialEntries: ["/network"] })

  render(
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>,
  )

  expect(within(screen.getByRole("banner")).getByText("Network")).toBeInTheDocument()
  expect(
    within(screen.getByRole("banner")).getByText("Transport summary and runtime status."),
  ).toBeInTheDocument()
  expect(within(screen.getByRole("banner")).getByText("admin")).toBeInTheDocument()
})

test("keeps global actions and lets the user log out", async () => {
  const router = createMemoryRouter(routes, { initialEntries: ["/network"] })
  const user = userEvent.setup()

  render(
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>,
  )

  expect(
    await within(screen.getByRole("banner")).findByRole("button", { name: /refresh/i }),
  ).toBeInTheDocument()
  expect(within(screen.getByRole("banner")).getByRole("button", { name: /search/i })).toBeInTheDocument()
  expect(within(screen.getByRole("banner")).getByRole("button", { name: /logout/i })).toBeInTheDocument()

  await user.click(within(screen.getByRole("banner")).getByRole("button", { name: /logout/i }))

  expect(await screen.findByRole("heading", { name: /sign in/i })).toBeInTheDocument()
  expect(useAuthStore.getState().status).toBe("anonymous")
  expect(screen.queryByText("Control plane")).not.toBeInTheDocument()
  expect(screen.queryByText("Manager shell")).not.toBeInTheDocument()
})

test("switches topbar actions to Chinese and persists the locale", async () => {
  localStorage.setItem("wukongim_manager_locale", "en")
  const router = createMemoryRouter(routes, { initialEntries: ["/network"] })
  const user = userEvent.setup()

  render(
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>,
  )

  await user.click(await within(screen.getByRole("banner")).findByRole("button", { name: "中文" }))

  expect(within(screen.getByRole("banner")).getByRole("button", { name: "刷新" })).toBeInTheDocument()
  expect(within(screen.getByRole("banner")).getByRole("button", { name: "搜索" })).toBeInTheDocument()
  expect(localStorage.getItem("wukongim_manager_locale")).toBe("zh-CN")
})

test("reads the translated route title and description from shared metadata", async () => {
  localStorage.setItem("wukongim_manager_locale", "zh-CN")
  const router = createMemoryRouter(routes, { initialEntries: ["/network"] })

  render(
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>,
  )

  expect(await within(screen.getByRole("banner")).findByText("网络")).toBeInTheDocument()
  expect(within(screen.getByRole("banner")).getByText("尚未暴露的管理面网络覆盖说明。")).toBeInTheDocument()
})
