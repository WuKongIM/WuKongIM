import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { MemoryRouter } from "react-router-dom"
import { beforeEach, expect, test, vi } from "vitest"

import { createAnonymousAuthState, useAuthStore } from "@/auth/auth-store"
import { I18nProvider } from "@/i18n/provider"
import { resetLocale } from "@/i18n/locale-store"
import type { ManagerBackupDashboard } from "@/lib/manager-api.types"
import { BackupsPage } from "@/pages/backups/page"

const getBackupDashboardMock = vi.fn()
const saveBackupPlanMock = vi.fn()
const testBackupRepositoryMock = vi.fn()
const startBackupJobMock = vi.fn()
const cancelBackupJobMock = vi.fn()
const verifyBackupArchiveMock = vi.fn()
const setBackupArchiveHoldMock = vi.fn()
const deleteBackupArchiveMock = vi.fn()
const startBackupRestoreMock = vi.fn()
const cancelBackupRestoreMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getBackupDashboard: (...args: unknown[]) => getBackupDashboardMock(...args),
    saveBackupPlan: (...args: unknown[]) => saveBackupPlanMock(...args),
    testBackupRepository: (...args: unknown[]) => testBackupRepositoryMock(...args),
    startBackupJob: (...args: unknown[]) => startBackupJobMock(...args),
    cancelBackupJob: (...args: unknown[]) => cancelBackupJobMock(...args),
    verifyBackupArchive: (...args: unknown[]) => verifyBackupArchiveMock(...args),
    setBackupArchiveHold: (...args: unknown[]) => setBackupArchiveHoldMock(...args),
    deleteBackupArchive: (...args: unknown[]) => deleteBackupArchiveMock(...args),
    startBackupRestore: (...args: unknown[]) => startBackupRestoreMock(...args),
    cancelBackupRestore: (...args: unknown[]) => cancelBackupRestoreMock(...args),
  }
})

const archive = {
  id: "backup-20260729-010000",
  trigger: "scheduled",
  source_cluster_id: "cluster-1",
  started_at_unix_ms: 1_785_260_400_000,
  completed_at_unix_ms: 1_785_260_700_000,
  logical_bytes: 2048,
  stored_bytes: 1024,
  records: 20,
  max_message_id: "88",
  held: false,
  health: "healthy" as const,
}

function dashboard(): ManagerBackupDashboard {
  return {
    credentials_configured: false,
    state: {
      revision: 7,
      manager_session_epoch: 0,
      plan: {
        revision: 3,
        enabled: true,
        store: { kind: "file" },
        cron: "0 1 * * *",
        time_zone: "Asia/Shanghai",
        retention_count: 7,
        rate_bytes_per_sec: 50 * 1_048_576,
        workers_per_node: 1,
        max_duration_ms: 12 * 3_600_000,
        schedule_cursor_unix_ms: 1_785_260_000_000,
        created_unix_ms: 1_785_250_000_000,
        updated_unix_ms: 1_785_260_000_000,
      },
    },
    archives: [archive],
  }
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

function setAuthenticated(permissions = [
  { resource: "cluster.backup", actions: ["r", "w"] },
]) {
  useAuthStore.setState({
    ...createAnonymousAuthState(),
    status: "authenticated",
    isHydrated: true,
    username: "admin",
    permissions,
  })
}

beforeEach(() => {
  resetLocale()
  localStorage.clear()
  vi.clearAllMocks()
  getBackupDashboardMock.mockResolvedValue(dashboard())
  saveBackupPlanMock.mockResolvedValue({ plan: dashboard().state.plan })
  testBackupRepositoryMock.mockResolvedValue({ ok: true })
  startBackupJobMock.mockResolvedValue({ id: "job-1" })
  verifyBackupArchiveMock.mockResolvedValue({ archive, manifest: {} })
  setBackupArchiveHoldMock.mockResolvedValue({ ...archive, held: true })
  startBackupRestoreMock.mockResolvedValue({ id: "restore-1" })
  setAuthenticated()
})

test("shows one simple scheduled full-backup page", async () => {
  renderPage()

  expect(await screen.findByRole("heading", { name: "Backups" })).toBeInTheDocument()
  expect(screen.getByText("Automatic backup")).toBeInTheDocument()
  expect(screen.getByRole("combobox", { name: "Backup frequency" })).toHaveValue("daily")
  expect(screen.getByText("backup-20260729-010000")).toBeInTheDocument()
  expect(screen.queryByText(/continuous/i)).not.toBeInTheDocument()
  expect(screen.queryByText(/checkpoint/i)).not.toBeInTheDocument()
})

test("saves a 12-hour plan using the current plan revision", async () => {
  const user = userEvent.setup()
  renderPage()

  const schedule = await screen.findByRole("combobox", { name: "Backup frequency" })
  await user.selectOptions(schedule, "half_day")
  await user.click(screen.getByRole("button", { name: "Save settings" }))

  await waitFor(() => expect(saveBackupPlanMock).toHaveBeenCalledWith(expect.objectContaining({
    expected_revision: 3,
    enabled: true,
    cron: "@every 12h",
    time_zone: "Asia/Shanghai",
    retention_count: 7,
    rate_mib_per_second: 50,
    workers_per_node: 1,
    max_duration_hours: 12,
    store: { kind: "file" },
  })))
})

test("keeps backup writes disabled when Manager is read-only", async () => {
  useAuthStore.setState({
    ...createAnonymousAuthState(),
    status: "readonly",
    isHydrated: true,
    username: "read-only",
    permissions: [{ resource: "cluster.backup", actions: ["r"] }],
  })
  renderPage()

  expect(await screen.findByText("Manager authentication is disabled. Backup changes and restores are unavailable.")).toBeInTheDocument()
  expect(await screen.findByRole("button", { name: "Save settings" })).toBeDisabled()
  expect(screen.queryByRole("button", { name: "Restore" })).not.toBeInTheDocument()
})

test("requires an explicit restore permission and administrator reauthentication", async () => {
  const user = userEvent.setup()
  setAuthenticated([
    { resource: "cluster.backup", actions: ["r", "w"] },
    { resource: "cluster.restore", actions: ["w"] },
  ])
  renderPage()

  await user.click(await screen.findByRole("button", { name: "Restore" }))
  expect(screen.getByRole("dialog")).toHaveTextContent("backup-20260729-010000")
  expect(screen.getByRole("textbox", { name: "Administrator" })).toHaveValue("admin")
  await user.type(screen.getByLabelText("Password"), "secret")
  await user.type(
    screen.getByLabelText("Type exactly: RESTORE backup-20260729-010000"),
    "RESTORE backup-20260729-010000",
  )
  await user.click(screen.getByRole("button", { name: "Start restore" }))

  await waitFor(() => expect(startBackupRestoreMock).toHaveBeenCalledWith(
    "backup-20260729-010000",
    {
      username: "admin",
      password: "secret",
      confirmation: "RESTORE backup-20260729-010000",
    },
  ))
})

test("does not treat a global wildcard as explicit restore permission", async () => {
  setAuthenticated([
    { resource: "cluster.backup", actions: ["r", "w"] },
    { resource: "*", actions: ["*"] },
  ])
  renderPage()

  expect(await screen.findByText("backup-20260729-010000")).toBeInTheDocument()
  expect(screen.queryByRole("button", { name: "Restore" })).not.toBeInTheDocument()
})

test("renders durable Hash Slot progress for an active backup", async () => {
  const active = dashboard()
  active.state.active_backup = {
    id: "backup-job-1",
    trigger: "manual",
    status: "exporting",
    plan_revision: 3,
    started_at_unix_ms: 1,
    deadline_unix_ms: 2,
    updated_unix_ms: 1,
    slots: [
      { hash_slot: 0, status: "complete", attempt: 1 },
      { hash_slot: 1, status: "running", attempt: 1 },
    ],
  }
  getBackupDashboardMock.mockResolvedValue(active)
  renderPage()

  expect(await screen.findByTestId("backup-task-progress")).toHaveTextContent("1/2 Hash Slots")
  expect(screen.getByRole("button", { name: "Back up now" })).toBeDisabled()
})

test("does not call backup APIs without read permission", async () => {
  setAuthenticated([])
  renderPage()

  expect(await screen.findByText("You do not have permission to view backup management.")).toBeInTheDocument()
  expect(getBackupDashboardMock).not.toHaveBeenCalled()
})
