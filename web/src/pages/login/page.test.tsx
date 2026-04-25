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

test("submits credentials and redirects to /dashboard on success", async () => {
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

  await user.type(screen.getByLabelText(/username/i), "admin")
  await user.type(screen.getByLabelText(/password/i), "secret")
  await user.click(screen.getByRole("button", { name: /sign in/i }))

  expect(await screen.findByRole("heading", { name: "Dashboard" })).toBeInTheDocument()
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

  await user.type(screen.getByLabelText(/username/i), "admin")
  await user.type(screen.getByLabelText(/password/i), "bad")
  await user.click(screen.getByRole("button", { name: /sign in/i }))

  expect(await screen.findByText("Invalid username or password.")).toBeInTheDocument()
  expect(useAuthStore.getState().status).toBe("anonymous")
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

  expect(screen.getByRole("heading", { name: "登录" })).toBeInTheDocument()
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

  await user.type(screen.getByLabelText(/username/i), "admin")
  await user.type(screen.getByLabelText(/password/i), "secret")
  await user.click(screen.getByRole("button", { name: /sign in/i }))

  expect(screen.getByRole("button", { name: /signing in/i })).toBeDisabled()

  resolveLogin?.({
    username: "admin",
    tokenType: "Bearer",
    accessToken: "token-1",
    expiresAt: "2099-04-22T12:00:00Z",
    permissions: [],
  })

  expect(await screen.findByRole("heading", { name: "Dashboard" })).toBeInTheDocument()
})

test("switches the login page copy without navigating away", async () => {
  const router = createMemoryRouter(routes, { initialEntries: ["/login"] })
  const user = userEvent.setup()

  render(
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>,
  )

  expect(screen.getByRole("heading", { name: "Sign in" })).toBeInTheDocument()

  await user.click(screen.getByRole("button", { name: "中文" }))

  expect(screen.getByRole("heading", { name: "登录" })).toBeInTheDocument()
  expect(localStorage.getItem("wukongim_manager_locale")).toBe("zh-CN")
})
