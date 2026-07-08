import { render, screen } from "@testing-library/react"
import { RouterProvider, createMemoryRouter } from "react-router-dom"
import { beforeEach, expect, test } from "vitest"

import { AppProviders } from "@/app/providers"
import { createAnonymousAuthState, useAuthStore } from "@/auth/auth-store"
import { routes } from "@/app/router"

function authenticatedState() {
  return {
    status: "authenticated" as const,
    isHydrated: true,
    username: "admin",
    tokenType: "Bearer",
    accessToken: "token-1",
    expiresAt: "2099-04-22T12:00:00Z",
    permissions: [],
  }
}

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

test("redirects authenticated /login visits to the cluster live monitor", async () => {
  useAuthStore.setState(authenticatedState())

  const router = createMemoryRouter(routes, { initialEntries: ["/login"] })

  render(
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>,
  )

  expect(await screen.findByRole("heading", { name: "Live Monitor" })).toBeInTheDocument()
  expect(router.state.location.pathname).toBe("/cluster/monitor")
})

test("renders the app shell for authenticated routes", async () => {
  useAuthStore.setState(authenticatedState())

  const router = createMemoryRouter(routes, { initialEntries: ["/cluster/nodes"] })

  render(
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>,
  )

  expect(await screen.findByLabelText("Primary navigation")).toBeInTheDocument()
  expect(screen.getByRole("banner")).toBeInTheDocument()
  expect(screen.getByRole("main")).toBeInTheDocument()
})

test("renders the shell for redesigned cluster routes", async () => {
  useAuthStore.setState(authenticatedState())
  const router = createMemoryRouter(routes, { initialEntries: ["/cluster/plugins"] })

  render(
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>,
  )

  expect(await screen.findByLabelText("Primary navigation")).toBeInTheDocument()
  expect(screen.getByRole("main")).toBeInTheDocument()
})

test.each([
  ["/", "/cluster/monitor"],
  ["/dashboard", "/cluster/dashboard"],
  ["/nodes", "/cluster/nodes"],
  ["/workqueues", "/cluster/workqueues"],
  ["/channel-cluster/unhealthy", "/cluster/channels"],
  ["/network", "/cluster/diagnostics?tab=trace"],
  ["/app-logs", "/cluster/diagnostics?tab=trace"],
  ["/connections", "/business/connections"],
  ["/system/connections", "/business/connections"],
  ["/db-inspect", "/system/db"],
])("redirects legacy %s to %s", async (from, to) => {
  useAuthStore.setState(authenticatedState())
  const router = createMemoryRouter(routes, { initialEntries: [from] })

  render(
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>,
  )

  await screen.findByRole("main")
  expect(router.state.location.pathname + router.state.location.search).toBe(to)
})

test("does not register retired monitor route aliases", () => {
  const appRoute = routes.find((route) => route.path === "/")
  const registeredPaths = appRoute?.children?.map((route) => route.path)

  expect(registeredPaths).not.toContain("monitor")
  expect(registeredPaths).not.toContain("business/monitor")
})

test("does not register the removed topology page or legacy alias", () => {
  const appRoute = routes.find((route) => route.path === "/")
  const registeredPaths = appRoute?.children?.map((route) => route.path)

  expect(registeredPaths).not.toContain("cluster/topology")
  expect(registeredPaths).not.toContain("topology")
})

test("renders the workqueue monitor route", async () => {
  useAuthStore.setState(authenticatedState())
  const router = createMemoryRouter(routes, { initialEntries: ["/cluster/workqueues"] })

  render(
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>,
  )

  expect(await screen.findByRole("heading", { name: "Workqueue Monitor" })).toBeInTheDocument()
})

test("renders the cluster live monitor route", async () => {
  useAuthStore.setState(authenticatedState())
  const router = createMemoryRouter(routes, { initialEntries: ["/cluster/monitor"] })

  render(
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>,
  )

  expect(await screen.findByRole("heading", { name: "Live Monitor" })).toBeInTheDocument()
  expect(screen.queryByText("UI Preview")).not.toBeInTheDocument()
})

test("normalizes retired controller diagnostics route to tracing", async () => {
  useAuthStore.setState(authenticatedState())
  const router = createMemoryRouter(routes, { initialEntries: ["/controller?node_id=1&tab=controller-logs"] })

  render(
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>,
  )

  await screen.findByRole("main")
  expect(router.state.location.pathname + router.state.location.search).toBe(
    "/cluster/diagnostics?node_id=1&tab=trace",
  )
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
  expect(screen.queryByRole("heading", { name: "Cluster Dashboard" })).not.toBeInTheDocument()
})
