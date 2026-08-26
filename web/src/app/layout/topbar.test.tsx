import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { RouterProvider, createMemoryRouter } from "react-router-dom"
import { beforeEach, expect, test, vi } from "vitest"

import { AppProviders } from "@/app/providers"
import { routes } from "@/app/router"
import { resetThemePreference, THEME_STORAGE_KEY } from "@/app/theme-store"
import { useAuthStore } from "@/auth/auth-store"
import { resetLocale } from "@/i18n/locale-store"

const getOverviewMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getOverview: (...args: unknown[]) => getOverviewMock(...args),
  }
})

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  resetThemePreference()
  document.documentElement.classList.remove("dark", "light")
  getOverviewMock.mockReset()
  getOverviewMock.mockResolvedValue({
    generated_at: "2026-08-26T03:30:00Z",
    cluster: { controller_leader_id: 1 },
    nodes: { total: 3, alive: 3, suspect: 0, dead: 0, draining: 0 },
    slots: { total: 10, ready: 10, quorum_lost: 0, leader_missing: 0, unreported: 0, peer_mismatch: 0, epoch_lag: 0 },
    tasks: { total: 0, pending: 0, retrying: 0, failed: 0 },
    anomalies: {
      slots: {
        quorum_lost: { count: 0, items: [] },
        leader_missing: { count: 0, items: [] },
        sync_mismatch: { count: 0, items: [] },
      },
      tasks: {
        failed: { count: 0, items: [] },
        retrying: { count: 0, items: [] },
      },
    },
  })
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

  const banner = await screen.findByRole("banner")
  expect(banner).toHaveClass("bg-background", "border-b")
  expect(banner.querySelector("[data-brand-mark]")).toHaveClass("rounded-sm")
  expect(within(banner).getByText("WUKONGIM")).toBeInTheDocument()
  expect(within(banner).queryByRole("link", { name: "Overview" })).not.toBeInTheDocument()
  expect(within(banner).getByRole("link", { name: "Cluster Ops" })).toHaveAttribute("aria-current", "page")
  expect(within(banner).getByRole("link", { name: "Business" })).toBeInTheDocument()
  expect(within(banner).getByRole("link", { name: "System" })).toBeInTheDocument()
  expect(within(banner).getByRole("link", { name: "Cluster Ops" })).toHaveClass("top-section-link-active")
  expect(within(banner).getByText("Nodes")).toBeInTheDocument()
  expect(within(banner).getByText("Node inventory, roles, and lifecycle status.")).toBeInTheDocument()
  expect(within(banner).getByText("admin")).toBeInTheDocument()
  expect(within(banner).getByRole("group", { name: "Theme switcher" })).toBeInTheDocument()
  expect(within(banner).getByRole("button", { name: "System theme" })).toHaveAttribute("aria-pressed", "true")
  expect(within(banner).getByRole("button", { name: "Light theme" })).toBeInTheDocument()
  expect(within(banner).getByRole("button", { name: "Dark theme" })).toBeInTheDocument()
})

test("uses a high-contrast active top section pill in Chinese", async () => {
  localStorage.setItem("wukongim_manager_locale", "zh-CN")
  localStorage.setItem(THEME_STORAGE_KEY, "light")
  const router = createMemoryRouter(routes, { initialEntries: ["/cluster/nodes"] })

  render(
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>,
  )

  const banner = await screen.findByRole("banner")
  const activeSection = await within(banner).findByRole("link", { name: "集群运维" })

  expect(activeSection).toHaveClass("top-section-link-active")
  expect(activeSection).not.toHaveClass("bg-[#c8ffd8]", "text-[#06120b]")
  expect(activeSection).not.toHaveClass("text-foreground")
})

test("shows the live cluster health context and lets the user log out", async () => {
  const router = createMemoryRouter(routes, { initialEntries: ["/cluster/diagnostics?tab=network"] })
  const user = userEvent.setup()

  render(
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>,
  )

  const banner = await screen.findByRole("banner")
  expect(await within(banner).findByText("3 nodes · healthy")).toBeInTheDocument()
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

  const banner = await screen.findByRole("banner")
  await user.click(await within(banner).findByRole("button", { name: "中文" }))

  expect(within(banner).getByRole("link", { name: "集群运维" })).toHaveAttribute("aria-current", "page")
  expect(within(banner).getByRole("group", { name: "主题切换" })).toBeInTheDocument()
  expect(within(banner).getByRole("button", { name: "跟随系统" })).toBeInTheDocument()
  expect(await within(banner).findByText("3 个节点 · 运行正常")).toBeInTheDocument()
  expect(within(banner).getByRole("button", { name: "退出登录" })).toBeInTheDocument()
  expect(localStorage.getItem("wukongim_manager_locale")).toBe("zh-CN")
})

test("opens complete navigation from the mobile menu", async () => {
  const router = createMemoryRouter(routes, { initialEntries: ["/cluster/nodes"] })
  const user = userEvent.setup()

  render(
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>,
  )

  const banner = await screen.findByRole("banner")
  await user.click(within(banner).getByRole("button", { name: "Open navigation" }))

  const dialog = await screen.findByRole("dialog", { name: "Navigation" })
  expect(within(dialog).getByRole("link", { name: "Cluster Ops" })).toHaveAttribute("aria-current", "page")
  expect(within(dialog).getByRole("link", { name: "Business" })).toBeInTheDocument()
  expect(within(dialog).getByRole("link", { name: "System" })).toBeInTheDocument()
  expect(within(dialog).getByRole("link", { name: "Live Monitor" })).toBeInTheDocument()
  expect(within(dialog).getByRole("group", { name: "Theme switcher" })).toBeInTheDocument()
})
