import { act, render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { MemoryRouter } from "react-router-dom"
import { beforeEach, expect, test, vi } from "vitest"

import { createAnonymousAuthState, useAuthStore } from "@/auth/auth-store"
import { I18nProvider } from "@/i18n/provider"
import { resetLocale } from "@/i18n/locale-store"
import type { ManagerBackupStatusResponse } from "@/lib/manager-api.types"
import { shouldRefreshRestorePointList } from "@/pages/backups/backup-refresh"
import { BackupsPage } from "@/pages/backups/page"

const getBackupStatusMock = vi.fn()
const getBackupRestorePointsMock = vi.fn()
const triggerMaterializedBackupMock = vi.fn()
const cancelBackupJobMock = vi.fn()
const verifyBackupRestorePointMock = vi.fn()
const setBackupRestorePointHoldMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getBackupStatus: (...args: unknown[]) => getBackupStatusMock(...args),
    getBackupRestorePoints: (...args: unknown[]) => getBackupRestorePointsMock(...args),
    triggerMaterializedBackup: (...args: unknown[]) => triggerMaterializedBackupMock(...args),
    cancelBackupJob: (...args: unknown[]) => cancelBackupJobMock(...args),
    verifyBackupRestorePoint: (...args: unknown[]) => verifyBackupRestorePointMock(...args),
    setBackupRestorePointHold: (...args: unknown[]) => setBackupRestorePointHoldMock(...args),
  }
})

function status(authEnabled = true): ManagerBackupStatusResponse {
  return {
    enabled: true,
    health: "healthy",
    recovery_point_age_seconds: 30,
    verification_age_seconds: 60,
    pending_garbage_count: 1,
    coordinator_node_id: 2,
    observed_at_unix_millis: 1_753_056_360_000,
    auth_enabled: authEnabled,
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
    capacity: { total: 2, held: 0, pending: 1, max: 4096, warning_at: 3276, critical_at: 3891, level: "normal" },
  }
}

const restorePoint = {
  id: "restore-exact-1",
  kind: "materialized_full",
  effective_at_unix_millis: 1_753_056_300_000,
  created_at_unix_millis: 1_753_056_330_000,
  primary_verified: true,
  secondary_verified: true,
  held: false,
}

function renderPage() {
  return render(
    <I18nProvider>
      <MemoryRouter initialEntries={["/cluster/backups"]}>
        <BackupsPage />
      </MemoryRouter>
    </I18nProvider>,
  )
}

beforeEach(() => {
  resetLocale()
  localStorage.clear()
  vi.clearAllMocks()
  getBackupStatusMock.mockResolvedValue(status())
  getBackupRestorePointsMock.mockResolvedValue({ items: [restorePoint], total: 1 })
  triggerMaterializedBackupMock.mockResolvedValue({ id: "job-1" })
  verifyBackupRestorePointMock.mockResolvedValue({ id: "verify-1", status: "pending" })
  setBackupRestorePointHoldMock.mockResolvedValue({ ...restorePoint, held: true })
  useAuthStore.setState({
    ...createAnonymousAuthState(),
    status: "authenticated",
    isHydrated: true,
    permissions: [{ resource: "cluster.backup", actions: ["r", "w"] }],
  })
})

test("starts only a materialized full backup after explicit confirmation", async () => {
  const user = userEvent.setup()
  renderPage()

  await screen.findByText("Cluster backup status")
  await user.click(screen.getByRole("button", { name: "Create backup now" }))
  expect(screen.getByRole("dialog")).toHaveTextContent("storage IO")
  await user.click(screen.getByRole("button", { name: "Confirm backup" }))

  await waitFor(() => expect(triggerMaterializedBackupMock).toHaveBeenCalledTimes(1))
})

test("forces write actions read-only when manager authentication is disabled", async () => {
  getBackupStatusMock.mockResolvedValue(status(false))
  renderPage()

  const trigger = await screen.findByRole("button", { name: "Create backup now" })
  expect(trigger).toBeDisabled()
  await waitFor(() => expect(trigger).toHaveAttribute("title", expect.stringMatching(/authentication/i)))
})

test("shows one forbidden state without calling backup APIs when read permission is missing", async () => {
  useAuthStore.setState({
    ...createAnonymousAuthState(),
    status: "authenticated",
    isHydrated: true,
    permissions: [],
  })
  renderPage()

  expect(await screen.findByText("You do not have permission to view backup management.")).toBeInTheDocument()
  expect(getBackupStatusMock).not.toHaveBeenCalled()
  expect(getBackupRestorePointsMock).not.toHaveBeenCalled()
})

test("binds recovery commands to the exact point and suppresses commands after failed dual-copy audit", async () => {
  const user = userEvent.setup()
  getBackupRestorePointsMock.mockResolvedValue({
    items: [{
      ...restorePoint,
      last_verification: {
        status: "failed",
        started_at_unix_millis: 1,
        completed_at_unix_millis: 2,
        primary_verified: false,
        secondary_verified: false,
        failure_category: "verification_failed",
      },
    }],
    total: 1,
  })
  renderPage()

  await user.click(screen.getByRole("tab", { name: "Recovery guide" }))
  await screen.findByText("restore-exact-1")
  expect(screen.getByText(/failed dual-repository verification/i)).toBeInTheDocument()
  expect(screen.queryByText(/\$WK_MANAGER_TOKEN/)).not.toBeInTheDocument()
})

test("applies restore-point filters only when search is submitted", async () => {
  const user = userEvent.setup()
  renderPage()

  await screen.findByText("Cluster backup status")
  await user.click(screen.getByRole("tab", { name: "Restore points" }))
  const initialCalls = getBackupRestorePointsMock.mock.calls.length
  await user.type(screen.getByRole("textbox", { name: "Search restore-point ID" }), "exact-1")

  await new Promise((resolve) => window.setTimeout(resolve, 0))
  expect(getBackupRestorePointsMock).toHaveBeenCalledTimes(initialCalls)

  await user.click(screen.getByRole("button", { name: "Search" }))
  await waitFor(() => expect(getBackupRestorePointsMock).toHaveBeenLastCalledWith(expect.objectContaining({
    id: "exact-1",
  })))
})

test("shows publication verification separately and offers one copy action per recovery command", async () => {
  const user = userEvent.setup()
  renderPage()

  await screen.findByText("Cluster backup status")
  await user.click(screen.getByRole("tab", { name: "Restore points" }))
  expect(screen.getByText("Primary + secondary")).toBeInTheDocument()

  await user.click(screen.getByRole("button", { name: "Recovery guide" }))
  await user.type(screen.getByRole("textbox", { name: "Target restore Manager URL" }), "https://restore.example.com")
  await user.click(screen.getByRole("radio", { name: "Preserve restored tokens" }))

  expect(screen.getAllByRole("button", { name: /Copy command/ })).toHaveLength(4)
})

test("refreshes the restore-point list only when a backup or verification operation completes", () => {
  const activeBackup = {
    ...status(),
    active: {
      id: "job-1",
      epoch: 7,
      kind: "materialized_full",
      status: "publishing",
      hash_slot_count: 256,
      restore_point_id: "",
      completed_partitions: 240,
      started_at_unix_millis: 1,
      updated_at_unix_millis: 2,
    },
  }
  const runningVerification = {
    ...status(),
    verification: {
      id: "verify-1",
      restore_point_id: restorePoint.id,
      status: "running" as const,
      started_at_unix_millis: 1,
      primary_verified: false,
      secondary_verified: false,
    },
  }

  expect(shouldRefreshRestorePointList(null, activeBackup)).toBe(false)
  expect(shouldRefreshRestorePointList(activeBackup, status())).toBe(true)
  expect(shouldRefreshRestorePointList(activeBackup, {
    ...activeBackup,
    active: { ...activeBackup.active, status: "capturing" },
  })).toBe(false)
  expect(shouldRefreshRestorePointList(null, runningVerification)).toBe(false)
  expect(shouldRefreshRestorePointList(runningVerification, {
    ...status(),
    verification: {
      ...runningVerification.verification,
      status: "succeeded",
      completed_at_unix_millis: 3,
      primary_verified: true,
      secondary_verified: true,
    },
  })).toBe(true)
})

test("retries a completion refresh after an overlapping restore-point request finishes", async () => {
  vi.useFakeTimers()
  try {
    let resolveInitialPoints: ((value: { items: (typeof restorePoint)[]; total: number }) => void) | undefined
    const initialPoints = new Promise<{ items: (typeof restorePoint)[]; total: number }>((resolve) => {
      resolveInitialPoints = resolve
    })
    getBackupRestorePointsMock
      .mockImplementationOnce(() => initialPoints)
      .mockResolvedValue({ items: [restorePoint], total: 1 })
    getBackupStatusMock
      .mockResolvedValueOnce({
        ...status(),
        active: {
          id: "job-overlap",
          epoch: 8,
          kind: "materialized_full",
          status: "publishing",
          hash_slot_count: 256,
          restore_point_id: "",
          completed_partitions: 255,
          started_at_unix_millis: 1,
          updated_at_unix_millis: 2,
        },
      })
      .mockResolvedValue(status())

    renderPage()
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0)
    })
    expect(getBackupRestorePointsMock).toHaveBeenCalledTimes(1)

    await act(async () => {
      await vi.advanceTimersByTimeAsync(15_000)
    })
    expect(getBackupStatusMock).toHaveBeenCalledTimes(2)
    expect(getBackupRestorePointsMock).toHaveBeenCalledTimes(1)

    await act(async () => {
      resolveInitialPoints?.({ items: [restorePoint], total: 1 })
      await initialPoints
    })
    await act(async () => {
      await vi.advanceTimersByTimeAsync(15_000)
    })

    expect(getBackupRestorePointsMock).toHaveBeenCalledTimes(2)
  } finally {
    vi.useRealTimers()
  }
})
