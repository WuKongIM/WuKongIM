import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { RouterProvider, createMemoryRouter } from "react-router-dom"
import { beforeEach, expect, test, vi } from "vitest"

import { AppProviders } from "@/app/providers"
import { routes } from "@/app/router"
import { createAnonymousAuthState, useAuthStore } from "@/auth/auth-store"
import { resetLocale } from "@/i18n/locale-store"
import { ManagerApiError } from "@/lib/manager-api"

const loginManagerMock = vi.fn()
vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    loginManager: (...args: unknown[]) => loginManagerMock(...args),
  }
})

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  useAuthStore.setState({ ...createAnonymousAuthState(), isHydrated: true })
  loginManagerMock.mockReset()
})

test("submits credentials and redirects to the cluster live monitor on success", async () => {
  loginManagerMock.mockResolvedValue({
    username: "admin",
    tokenType: "Bearer",
    accessToken: "token-1",
    expiresAt: "2099-04-22T12:00:00Z",
    permissions: [],
  })

  const router = createMemoryRouter(routes, { initialEntries: ["/login"] })
  const user = userEvent.setup()

  render(
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>,
  )

  await user.type(await screen.findByLabelText(/username/i), "admin")
  await user.type(screen.getByLabelText("Password", { selector: "input" }), "secret")
  await user.click(screen.getByRole("button", { name: /sign in/i }))

  expect(await screen.findByRole("heading", { name: "Live Monitor" })).toBeInTheDocument()
  expect(router.state.location.pathname).toBe("/cluster/monitor")
  expect(useAuthStore.getState().accessToken).toBe("token-1")
  expect(localStorage.getItem("wukongim_manager_auth")).toContain("token-1")
})

test("shows the invalid credentials message for 401 responses", async () => {
  loginManagerMock.mockRejectedValue(
    new ManagerApiError(401, "invalid_credentials", "invalid credentials"),
  )

  const router = createMemoryRouter(routes, { initialEntries: ["/login"] })
  const user = userEvent.setup()

  render(
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>,
  )

  await user.type(await screen.findByLabelText(/username/i), "admin")
  await user.type(screen.getByLabelText("Password", { selector: "input" }), "bad")
  await user.click(await screen.findByRole("button", { name: /sign in/i }))

  expect(await screen.findByText("Invalid username or password.")).toBeInTheDocument()
  expect(useAuthStore.getState().status).toBe("anonymous")
})

test("does not mark credentials invalid when the login service is unavailable", async () => {
  loginManagerMock.mockRejectedValue(
    new ManagerApiError(503, "service_unavailable", "service unavailable"),
  )

  const router = createMemoryRouter(routes, { initialEntries: ["/login"] })
  const user = userEvent.setup()

  render(
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>,
  )

  await user.click(screen.getByRole("button", { name: /sign in/i }))

  expect(await screen.findByText("Login service is unavailable. Please try again.")).toBeInTheDocument()
  expect(screen.getByLabelText("Username")).toHaveAttribute("aria-invalid", "false")
  expect(screen.getByLabelText("Password")).toHaveAttribute("aria-invalid", "false")
})

test("shows translated Chinese login copy and a translated 401 error", async () => {
  localStorage.setItem("wukongim_manager_locale", "zh-CN")
  loginManagerMock.mockRejectedValue(
    new ManagerApiError(401, "invalid_credentials", "invalid credentials"),
  )

  const router = createMemoryRouter(routes, { initialEntries: ["/login"] })
  const user = userEvent.setup()

  render(
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>,
  )

  expect(await screen.findByRole("heading", { name: "登录" })).toBeInTheDocument()
  expect(screen.getByText("使用管理员账号登录。")).toBeInTheDocument()
  expect(screen.getByText("登录后将按账号权限展示可执行操作。")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "登录" }))

  expect(await screen.findByText("用户名或密码错误。")).toBeInTheDocument()
})

test("shows a loading state while the login request is in flight", async () => {
  let resolveLogin: ((value: unknown) => void) | undefined
  loginManagerMock.mockReturnValue(
    new Promise((resolve) => {
      resolveLogin = resolve
    }),
  )

  const router = createMemoryRouter(routes, { initialEntries: ["/login"] })
  const user = userEvent.setup()

  render(
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>,
  )

  await user.type(await screen.findByLabelText(/username/i), "admin")
  await user.type(screen.getByLabelText("Password", { selector: "input" }), "secret")
  await user.click(screen.getByRole("button", { name: /sign in/i }))

  expect(screen.getByRole("button", { name: /signing in/i })).toBeDisabled()

  resolveLogin?.({
    username: "admin",
    tokenType: "Bearer",
    accessToken: "token-1",
    expiresAt: "2099-04-22T12:00:00Z",
    permissions: [],
  })

  expect(await screen.findByRole("heading", { name: "Live Monitor" })).toBeInTheDocument()
  expect(router.state.location.pathname).toBe("/cluster/monitor")
})

test("switches the login page copy without navigating away", async () => {
  const router = createMemoryRouter(routes, { initialEntries: ["/login"] })
  const user = userEvent.setup()

  render(
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>,
  )

  expect(await screen.findByRole("heading", { name: "Sign in" })).toBeInTheDocument()

  await user.click(screen.getByRole("button", { name: "中文" }))

  expect(screen.getByRole("heading", { name: "登录" })).toBeInTheDocument()
  expect(localStorage.getItem("wukongim_manager_locale")).toBe("zh-CN")
})

test("renders a focused manager access experience", async () => {
  const router = createMemoryRouter(routes, { initialEntries: ["/login"] })

  render(
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>,
  )

  expect(await screen.findAllByText("WuKongIM Manager")).toHaveLength(2)
  expect(screen.getByRole("heading", { name: "See the signal. Run the cluster." })).toBeInTheDocument()
  expect(screen.getByText("256 hash slots")).toBeInTheDocument()
  expect(screen.getByText("Permission-scoped")).toBeInTheDocument()
  expect(screen.getByLabelText("Theme switcher")).toBeInTheDocument()
  expect(screen.getByTestId("login-manager-preview")).toHaveClass("bg-[#071829]")
  expect(screen.getByTestId("login-form-panel")).toHaveClass("bg-background")
  expect(document.querySelector("[class*='radial-gradient']")).not.toBeInTheDocument()
})

test("toggles password visibility without clearing the field", async () => {
  const router = createMemoryRouter(routes, { initialEntries: ["/login"] })
  const user = userEvent.setup()

  render(
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>,
  )

  const passwordInput = await screen.findByLabelText("Password")
  await user.type(passwordInput, "secret")

  expect(passwordInput).toHaveAttribute("type", "password")
  await user.click(screen.getByRole("button", { name: "Show password" }))
  expect(passwordInput).toHaveAttribute("type", "text")
  expect(passwordInput).toHaveValue("secret")

  await user.click(screen.getByRole("button", { name: "Hide password" }))
  expect(passwordInput).toHaveAttribute("type", "password")
})
