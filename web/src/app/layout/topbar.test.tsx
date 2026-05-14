import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { RouterProvider, createMemoryRouter } from "react-router-dom"
import { beforeEach, expect, test, vi } from "vitest"

import { AppProviders } from "@/app/providers"
import { routes } from "@/app/router"
import { useAuthStore } from "@/auth/auth-store"
import { resetLocale } from "@/i18n/locale-store"

const getNetworkSummaryMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getNetworkSummary: (...args: unknown[]) => getNetworkSummaryMock(...args),
  }
})

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  getNetworkSummaryMock.mockReset()
  getNetworkSummaryMock.mockImplementation(() => new Promise(() => {}))
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

test("renders brand, top sections, route metadata, and logged-in username", async () => {
  const router = createMemoryRouter(routes, { initialEntries: ["/cluster/nodes"] })

  render(
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>,
  )

  const banner = screen.getByRole("banner")
  expect(within(banner).getByText("WUKONGIM")).toBeInTheDocument()
  expect(within(banner).getByRole("link", { name: "Overview" })).toBeInTheDocument()
  expect(within(banner).getByRole("link", { name: "Cluster Ops" })).toHaveAttribute("aria-current", "page")
  expect(within(banner).getByRole("link", { name: "Business" })).toBeInTheDocument()
  expect(within(banner).getByRole("link", { name: "System" })).toBeInTheDocument()
  expect(within(banner).getByRole("link", { name: "Cluster Ops" })).toHaveClass("text-[#06120b]")
  expect(within(banner).getByText("Nodes")).toBeInTheDocument()
  expect(within(banner).getByText("Node inventory, roles, and lifecycle status.")).toBeInTheDocument()
  expect(within(banner).getByText("admin")).toBeInTheDocument()
})

test("keeps cockpit health context and lets the user log out", async () => {
  const router = createMemoryRouter(routes, { initialEntries: ["/cluster/diagnostics?tab=network"] })
  const user = userEvent.setup()

  render(
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>,
  )

  const banner = screen.getByRole("banner")
  expect(await within(banner).findByText("Single-node cluster · healthy")).toBeInTheDocument()
  expect(within(banner).queryByRole("button", { name: /refresh/i })).not.toBeInTheDocument()
  expect(within(banner).queryByRole("button", { name: /search/i })).not.toBeInTheDocument()
  expect(within(banner).getByRole("button", { name: /logout/i })).toBeInTheDocument()

  await user.click(within(banner).getByRole("button", { name: /logout/i }))

  expect(await screen.findByRole("heading", { name: /sign in/i })).toBeInTheDocument()
  expect(useAuthStore.getState().status).toBe("anonymous")
})

test("switches topbar actions and sections to Chinese", async () => {
  localStorage.setItem("wukongim_manager_locale", "en")
  const router = createMemoryRouter(routes, { initialEntries: ["/cluster/nodes"] })
  const user = userEvent.setup()

  render(
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>,
  )

  await user.click(await within(screen.getByRole("banner")).findByRole("button", { name: "中文" }))

  expect(within(screen.getByRole("banner")).getByRole("link", { name: "集群运维" })).toHaveAttribute("aria-current", "page")
  expect(within(screen.getByRole("banner")).getByText("单节点集群 · 健康")).toBeInTheDocument()
  expect(within(screen.getByRole("banner")).getByRole("button", { name: "退出登录" })).toBeInTheDocument()
  expect(localStorage.getItem("wukongim_manager_locale")).toBe("zh-CN")
})
