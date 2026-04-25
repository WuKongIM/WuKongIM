import { render, screen } from "@testing-library/react"
import { RouterProvider, createMemoryRouter } from "react-router-dom"
import { beforeEach, expect, test } from "vitest"

import { AppProviders } from "@/app/providers"
import { createAnonymousAuthState, useAuthStore } from "@/auth/auth-store"
import { routes } from "@/app/router"

beforeEach(() => {
  localStorage.clear()
  useAuthStore.setState({ ...createAnonymousAuthState(), isHydrated: true })
})

test("redirects anonymous /dashboard visits to /login", async () => {
  const router = createMemoryRouter(routes, { initialEntries: ["/dashboard"] })

  render(
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>,
  )

  expect(await screen.findByRole("heading", { name: /sign in/i })).toBeInTheDocument()
})

test("redirects authenticated /login visits to /dashboard", async () => {
  useAuthStore.setState({
    status: "authenticated",
    isHydrated: true,
    username: "admin",
    tokenType: "Bearer",
    accessToken: "token-1",
    expiresAt: "2099-04-22T12:00:00Z",
    permissions: [],
  })

  const router = createMemoryRouter(routes, { initialEntries: ["/login"] })

  render(
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>,
  )

  expect(await screen.findByRole("heading", { name: "Dashboard" })).toBeInTheDocument()
})

test("renders the app shell for authenticated routes", async () => {
  useAuthStore.setState({
    status: "authenticated",
    isHydrated: true,
    username: "admin",
    tokenType: "Bearer",
    accessToken: "token-1",
    expiresAt: "2099-04-22T12:00:00Z",
    permissions: [],
  })

  const router = createMemoryRouter(routes, { initialEntries: ["/nodes"] })

  render(
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>,
  )

  expect(await screen.findByLabelText("Primary navigation")).toBeInTheDocument()
  expect(screen.getByRole("banner")).toBeInTheDocument()
  expect(screen.getByRole("main")).toBeInTheDocument()
})

test("waits for hydration before showing the login route", () => {
  useAuthStore.setState(createAnonymousAuthState())

  const router = createMemoryRouter(routes, { initialEntries: ["/dashboard"] })

  render(
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>,
  )

  expect(screen.queryByRole("heading", { name: /sign in/i })).not.toBeInTheDocument()
  expect(screen.queryByRole("heading", { name: "Dashboard" })).not.toBeInTheDocument()
})
