import { render, screen } from "@testing-library/react"
import { RouterProvider, createMemoryRouter } from "react-router-dom"
import { beforeEach, expect, test, vi } from "vitest"

import { AppProviders } from "@/app/providers"
import { createAnonymousAuthState, useAuthStore } from "@/auth/auth-store"
import { routes } from "@/app/router"

const getNodesMock = vi.fn()
const getApplicationLogSourcesMock = vi.fn()
const getApplicationLogEntriesMock = vi.fn()
const getPermissionsMock = vi.fn()
const getBackupStatusMock = vi.fn()
const getBackupRestorePointsMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getNodes: (...args: unknown[]) => getNodesMock(...args),
    getApplicationLogSources: (...args: unknown[]) => getApplicationLogSourcesMock(...args),
    getApplicationLogEntries: (...args: unknown[]) => getApplicationLogEntriesMock(...args),
    getPermissions: (...args: unknown[]) => getPermissionsMock(...args),
    getBackupStatus: (...args: unknown[]) => getBackupStatusMock(...args),
    getBackupRestorePoints: (...args: unknown[]) => getBackupRestorePointsMock(...args),
  }
})

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
  getNodesMock.mockReset()
  getApplicationLogSourcesMock.mockReset()
  getApplicationLogEntriesMock.mockReset()
  getPermissionsMock.mockReset()
  getBackupStatusMock.mockReset()
  getBackupRestorePointsMock.mockReset()
  getPermissionsMock.mockRejectedValue(new Error("authentication required"))
  getBackupStatusMock.mockResolvedValue({
    enabled: true,
    health: "healthy",
    recovery_point_age_seconds: 30,
    verification_age_seconds: 60,
    pending_garbage_count: 0,
    coordinator_node_id: 1,
    observed_at_unix_millis: 1_753_056_360_000,
    auth_enabled: false,
    running: true,
    max_recovery_point_age_seconds: 300,
    max_verification_age_seconds: 86_400,
    policy: {
      incremental_interval_seconds: 5,
      restore_point_interval_seconds: 300,
      independent_full_interval_seconds: 86_400,
      materialized_full_interval_seconds: 2_592_000,
      monthly_retention_months: 12,
      object_lock_days: 30,
      max_parallel_partitions: 4,
      staging_max_bytes: 1024,
      primary_region: "cn-a",
      secondary_region: "cn-b",
      kms_region: "cn-a",
    },
    dependencies: {
      primary: { health: "healthy", region: "cn-a" },
      secondary: { health: "healthy", region: "cn-b" },
      kms: { health: "healthy", region: "cn-a" },
      staging: { health: "healthy" },
      utc: { health: "healthy" },
    },
    capacity: {
      total: 1,
      held: 0,
      pending: 0,
      max: 4096,
      warning_at: 3276,
      critical_at: 3891,
      level: "normal",
    },
  })
  getBackupRestorePointsMock.mockResolvedValue({ items: [], total: 0 })
  getNodesMock.mockResolvedValue({
    total: 1,
    items: [{
      node_id: 1,
      addr: "127.0.0.1:7000",
      status: "alive",
      last_heartbeat_at: "2026-04-23T08:00:00Z",
      is_local: true,
      capacity_weight: 1,
      controller: { role: "leader" },
      slot_stats: { count: 1, leader_count: 1 },
      channel_runtime: { active_total: 0, active_leader: 0, active_follower: 0, unknown: false },
    }],
  })
  getApplicationLogSourcesMock.mockResolvedValue({
    node_id: 1,
    sources: [{ name: "app", file: "app.log", available: true, size_bytes: 0 }],
  })
  getApplicationLogEntriesMock.mockResolvedValue({
    node_id: 1,
    source: "app",
    cursor: "",
    rotated: false,
    items: [],
  })
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

test("opens backup management read-only for a fresh auth-disabled browser", async () => {
  getPermissionsMock.mockResolvedValue({
    auth_enabled: false,
    current_user: "",
    users: [],
    resources: [],
  })
  const router = createMemoryRouter(routes, { initialEntries: ["/cluster/backups"] })

  render(
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>,
  )

  expect(await screen.findByRole("heading", { name: "Backup Management" })).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Create backup now" })).toBeDisabled()
  expect(useAuthStore.getState()).toMatchObject({
    status: "readonly",
    permissions: [{ resource: "cluster.backup", actions: ["r"] }],
  })
})

test("confines the auth-disabled readonly session to backup management", async () => {
  getPermissionsMock.mockResolvedValue({
    auth_enabled: false,
    current_user: "",
    users: [],
    resources: [],
  })
  const router = createMemoryRouter(routes, { initialEntries: ["/cluster/nodes"] })

  render(
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>,
  )

  expect(await screen.findByRole("heading", { name: "Backup Management" })).toBeInTheDocument()
  expect(router.state.location.pathname).toBe("/cluster/backups")
  expect(getNodesMock).not.toHaveBeenCalled()
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
  ["/app-logs", "/cluster/system-logs"],
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

test("renders the cluster system logs route", async () => {
  useAuthStore.setState(authenticatedState())
  const router = createMemoryRouter(routes, { initialEntries: ["/cluster/system-logs"] })

  render(
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>,
  )

  expect(await screen.findByRole("heading", { level: 1, name: "Node Process Logs" })).toBeInTheDocument()
  expect(router.state.location.pathname).toBe("/cluster/system-logs")
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
