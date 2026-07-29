import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { MemoryRouter } from "react-router-dom"
import { beforeEach, expect, test, vi } from "vitest"

import { createAnonymousAuthState, useAuthStore } from "@/auth/auth-store"
import { I18nProvider } from "@/i18n/provider"
import { resetLocale } from "@/i18n/locale-store"
import { ManagerApiError } from "@/lib/manager-api"
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

function cloudDashboard(
  kind: "oss" | "cos",
  bucket: string,
  verification: "verified" | "unverified" = "unverified",
): ManagerBackupDashboard {
  const current = dashboard()
  current.credentials_configured = true
  current.state.revision = 8
  current.state.plan = {
    ...current.state.plan!,
    revision: 4,
    enabled: false,
    store: {
      kind,
      endpoint: kind === "oss"
        ? "https://oss-cn-hangzhou.aliyuncs.com"
        : "https://cos.ap-shanghai.myqcloud.com",
      region: kind === "oss" ? "cn-hangzhou" : "ap-shanghai",
      bucket,
      prefix: "cluster-a",
      credential_revision: 1,
    },
    repository_verification: {
      status: verification,
      ...(verification === "verified"
        ? { verified_at_unix_ms: 1_785_260_800_000 }
        : {}),
    },
  }
  return current
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
  saveBackupPlanMock.mockResolvedValue({
    plan: dashboard().state.plan,
    credentials_configured: false,
  })
  testBackupRepositoryMock.mockResolvedValue({
    ok: true,
    plan: dashboard().state.plan,
  })
  startBackupJobMock.mockResolvedValue({ id: "job-1" })
  verifyBackupArchiveMock.mockResolvedValue({ archive, manifest: {} })
  setBackupArchiveHoldMock.mockResolvedValue({ ...archive, held: true })
  startBackupRestoreMock.mockResolvedValue({ id: "restore-1" })
  setAuthenticated()
})

test("shows one simple scheduled full-backup page", async () => {
  getBackupDashboardMock.mockImplementationOnce(() => new Promise((resolve) => {
    setTimeout(() => resolve(dashboard()), 25)
  }))
  renderPage()

  expect(await screen.findByRole("heading", { name: "Backups" })).toBeInTheDocument()
  expect(await screen.findByText("Automatic backup")).toBeInTheDocument()
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

test("configures Alibaba OSS without requiring a custom endpoint", async () => {
  const user = userEvent.setup()
  const saved = cloudDashboard("oss", "wukongim-backups")
  getBackupDashboardMock
    .mockResolvedValueOnce(dashboard())
    .mockResolvedValue(saved)
  saveBackupPlanMock.mockResolvedValue({
    plan: saved.state.plan,
    credentials_configured: true,
  })
  renderPage()

  const storage = await screen.findByRole("combobox", { name: "Storage" })
  expect(screen.getByRole("option", { name: "Alibaba Cloud OSS" })).toBeInTheDocument()
  expect(screen.getByRole("option", { name: "Tencent Cloud COS" })).toBeInTheDocument()
  await user.selectOptions(storage, "oss")
  await user.type(screen.getByRole("textbox", { name: "Region" }), "cn-hangzhou")
  await user.type(screen.getByRole("textbox", { name: "Bucket" }), "wukongim-backups")
  const prefix = screen.getByRole("textbox", { name: "Prefix" })
  expect(prefix).toHaveValue("wukongim")
  await user.clear(prefix)
  await user.type(prefix, "cluster-a")
  await user.type(screen.getByRole("textbox", { name: "AccessKey ID" }), "access-key-id")
  await user.type(screen.getByLabelText("AccessKey Secret"), "access-key-secret")
  expect(screen.getByRole("textbox", { name: "Endpoint (optional)" })).toHaveAttribute(
    "placeholder",
    "https://oss-<region>.aliyuncs.com",
  )
  expect(screen.queryByText("Use path-style addressing")).not.toBeInTheDocument()

  await user.click(screen.getByRole("button", { name: "Save settings" }))

  await waitFor(() => expect(saveBackupPlanMock).toHaveBeenCalledWith(
    expect.objectContaining({
      store: {
        kind: "oss",
        endpoint: "",
        region: "cn-hangzhou",
        bucket: "wukongim-backups",
        prefix: "cluster-a",
        access_key: "access-key-id",
        secret_key: "access-key-secret",
      },
    }),
  ))
  await waitFor(() => {
    expect(screen.getByRole("textbox", { name: "Region" })).toHaveValue("cn-hangzhou")
    expect(screen.getByRole("textbox", { name: "Bucket" })).toHaveValue("wukongim-backups")
    expect(screen.getByRole("textbox", { name: "Prefix" })).toHaveValue("cluster-a")
    expect(screen.getByRole("textbox", { name: "Endpoint (optional)" })).toHaveValue(
      "https://oss-cn-hangzhou.aliyuncs.com",
    )
    expect(screen.getByRole("textbox", { name: "AccessKey ID" })).toHaveValue("")
    expect(screen.getByRole("textbox", { name: "AccessKey ID" })).toHaveAttribute(
      "placeholder",
      "Leave blank to keep saved credentials",
    )
    expect(screen.getByLabelText("AccessKey Secret")).toHaveValue("")
  })
})

test("uses Tencent COS names and submits the full Bucket name", async () => {
  const user = userEvent.setup()
  const saved = cloudDashboard("cos", "wukongim-backups-1250000000")
  getBackupDashboardMock
    .mockResolvedValueOnce(dashboard())
    .mockResolvedValue(saved)
  saveBackupPlanMock.mockResolvedValue({
    plan: saved.state.plan,
    credentials_configured: true,
  })
  renderPage()

  const storage = await screen.findByRole("combobox", { name: "Storage" })
  await user.selectOptions(storage, "cos")
  expect(screen.getByText(/full Bucket name including APPID/)).toBeInTheDocument()
  await user.type(screen.getByRole("textbox", { name: "Region" }), "ap-shanghai")
  await user.type(
    screen.getByRole("textbox", { name: "Bucket" }),
    "wukongim-backups-1250000000",
  )
  const prefix = screen.getByRole("textbox", { name: "Prefix" })
  await user.clear(prefix)
  await user.type(prefix, "cluster-a")
  await user.type(screen.getByRole("textbox", { name: "SecretId" }), "secret-id")
  await user.type(screen.getByLabelText("SecretKey"), "secret-key")
  expect(screen.getByRole("textbox", { name: "Endpoint (optional)" })).toHaveAttribute(
    "placeholder",
    "https://cos.<region>.myqcloud.com",
  )
  await user.click(screen.getByRole("button", { name: "Save settings" }))

  await waitFor(() => expect(saveBackupPlanMock).toHaveBeenCalledWith(
    expect.objectContaining({
      store: {
        kind: "cos",
        endpoint: "",
        region: "ap-shanghai",
        bucket: "wukongim-backups-1250000000",
        prefix: "cluster-a",
        access_key: "secret-id",
        secret_key: "secret-key",
      },
    }),
  ))
  await waitFor(() => {
    expect(screen.getByRole("textbox", { name: "Region" })).toHaveValue("ap-shanghai")
    expect(screen.getByRole("textbox", { name: "Bucket" })).toHaveValue(
      "wukongim-backups-1250000000",
    )
    expect(screen.getByRole("textbox", { name: "Prefix" })).toHaveValue("cluster-a")
    expect(screen.getByRole("textbox", { name: "Endpoint (optional)" })).toHaveValue(
      "https://cos.ap-shanghai.myqcloud.com",
    )
    expect(screen.getByRole("textbox", { name: "SecretId" })).toHaveValue("")
    expect(screen.getByRole("textbox", { name: "SecretId" })).toHaveAttribute(
      "placeholder",
      "Leave blank to keep saved credentials",
    )
    expect(screen.getByLabelText("SecretKey")).toHaveValue("")
  })
})

test("changing a cloud Region keeps the provider default endpoint selected", async () => {
  const user = userEvent.setup()
  const current = dashboard()
  current.credentials_configured = true
  current.state.plan!.store = {
    kind: "oss",
    region: "cn-hangzhou",
    bucket: "wukongim-backups",
    prefix: "cluster-a",
  }
  getBackupDashboardMock.mockResolvedValue(current)
  renderPage()

  const region = await screen.findByRole("textbox", { name: "Region" })
  await user.clear(region)
  await user.type(region, "cn-shanghai")
  await user.click(screen.getByRole("button", { name: "Save settings" }))

  await waitFor(() => expect(saveBackupPlanMock).toHaveBeenCalledWith(
    expect.objectContaining({
      store: expect.objectContaining({
        kind: "oss",
        endpoint: "",
        region: "cn-shanghai",
      }),
    }),
  ))
})

test("shows repository dirtiness and blocks testing and backup until saved and verified", async () => {
  const user = userEvent.setup()
  const current = cloudDashboard("oss", "wukongim-backups", "verified")
  current.state.plan!.enabled = true
  getBackupDashboardMock.mockResolvedValue(current)
  renderPage()

  expect(await screen.findByText(/Verified/)).toBeInTheDocument()
  const automatic = screen.getByRole("checkbox", { name: /Enable automatic backup/ })
  expect(automatic).toBeChecked()
  expect(screen.getByRole("button", { name: "Test storage" })).toBeEnabled()
  expect(screen.getByRole("button", { name: "Back up now" })).toBeEnabled()

  const region = screen.getByRole("textbox", { name: "Region" })
  await user.clear(region)
  await user.type(region, "cn-shanghai")

  expect(automatic).not.toBeChecked()
  expect(automatic).toBeDisabled()
  expect(screen.getByText("Not tested")).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Test storage" })).toBeDisabled()
  expect(screen.getByRole("button", { name: "Back up now" })).toBeDisabled()
  expect(screen.getByText("Save the repository settings before testing.")).toBeInTheDocument()
})

test("treats new credentials as an unsaved repository change", async () => {
  const user = userEvent.setup()
  const current = cloudDashboard("cos", "wukongim-backups-1250000000", "verified")
  getBackupDashboardMock.mockResolvedValue(current)
  renderPage()

  const testButton = await screen.findByRole("button", { name: "Test storage" })
  expect(testButton).toBeEnabled()
  await user.type(screen.getByRole("textbox", { name: "SecretId" }), "replacement-id")
  expect(testButton).toBeDisabled()
  expect(screen.getByText("Not tested")).toBeInTheDocument()
})

test("requires explicit unverified repositories to be tested before backup", async () => {
  const user = userEvent.setup()
  const current = cloudDashboard("oss", "wukongim-backups", "unverified")
  getBackupDashboardMock.mockResolvedValue(current)
  testBackupRepositoryMock.mockResolvedValue({
    ok: true,
    plan: {
      ...current.state.plan!,
      repository_verification: {
        status: "verified",
        verified_at_unix_ms: 1_785_260_900_000,
      },
    },
  })
  renderPage()

  const automatic = await screen.findByRole("checkbox", {
    name: /Enable automatic backup/,
  })
  expect(automatic).toBeDisabled()
  expect(screen.getByRole("button", { name: "Back up now" })).toBeDisabled()
  expect(screen.getByText("Not tested")).toBeInTheDocument()

  await user.click(screen.getByRole("button", { name: "Test storage" }))
  await waitFor(() => expect(testBackupRepositoryMock).toHaveBeenCalledWith(4))
  expect(await screen.findByText(/Verified/)).toBeInTheDocument()
})

test("shows legacy repository verification state without blocking backup", async () => {
  renderPage()

  expect(await screen.findByText("Verified before upgrade")).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Back up now" })).toBeEnabled()
})

test("renders actionable repository errors directly below the plan actions", async () => {
  const user = userEvent.setup()
  testBackupRepositoryMock.mockRejectedValue(new ManagerApiError(
    503,
    "backup_repository_auth_failed",
    "Alibaba Cloud OSS rejected the AccessKey ID.",
    undefined,
    {
      provider: "oss",
      stage: "write_marker",
      reason: "invalid_access_key",
      provider_code: "InvalidAccessKeyId",
      request_id: "request-1",
      node_id: 2,
    },
  ))
  renderPage()

  await user.click(await screen.findByRole("button", { name: "Test storage" }))

  const feedback = await screen.findByRole("alert")
  expect(feedback).toHaveTextContent("Alibaba Cloud OSS")
  expect(feedback).toHaveTextContent("AccessKey ID is invalid")
  expect(feedback).toHaveTextContent("Write test marker")
  expect(feedback).toHaveTextContent("InvalidAccessKeyId")
  expect(feedback).toHaveTextContent("request-1")
  expect(feedback).toHaveTextContent("2")
  expect(feedback).not.toHaveTextContent(
    "Cannot access the backup storage. Check its address, credentials, permissions, and free space, then try again.",
  )
  const actionRow = screen.getByTestId("backup-plan-actions")
  expect(
    actionRow.compareDocumentPosition(feedback) & Node.DOCUMENT_POSITION_FOLLOWING,
  ).toBeTruthy()
})

test("renders repository test success in the inline live region", async () => {
  const user = userEvent.setup()
  renderPage()

  await user.click(await screen.findByRole("button", { name: "Test storage" }))

  const feedback = await screen.findByTestId("backup-plan-feedback")
  expect(feedback).toHaveAttribute("aria-live", "polite")
  expect(feedback).toHaveTextContent("Storage test succeeded.")
})

test("shows a critical warning after two expected backups produce no success", async () => {
  const current = dashboard()
  current.backup_health = "critical"
  current.backup_health_reason = "successful_backup_stale"
  getBackupDashboardMock.mockResolvedValue(current)

  renderPage()

  expect(await screen.findByText(
    "No successful backup was produced across two expected runs. Recovery coverage is stale.",
  )).toBeInTheDocument()
})

test("shows verification and retention results in recent task history", async () => {
  const current = dashboard()
  current.state.history = [
    {
      id: "verify-1",
      kind: "verification",
      status: "failed",
      started_at_unix_ms: 1_785_260_000_000,
      completed_at_unix_ms: 1_785_260_001_000,
      error_code: "archive_corrupt",
    },
    {
      id: "backup-1",
      kind: "retention",
      status: "succeeded",
      started_at_unix_ms: 1_785_260_002_000,
      completed_at_unix_ms: 1_785_260_003_000,
    },
  ]
  getBackupDashboardMock.mockResolvedValue(current)

  renderPage()

  expect(await screen.findByText("Archive verification")).toBeInTheDocument()
  expect(screen.getByText("Retention cleanup")).toBeInTheDocument()
  expect(screen.getByText("archive_corrupt")).toBeInTheDocument()
  expect(screen.getByText("Succeeded")).toBeInTheDocument()
  expect(screen.getByText("Failed")).toBeInTheDocument()
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

test("renders restore bytes, rollback state, error, and per-node replica progress", async () => {
  const active = dashboard()
  active.state.active_restore = {
    id: "restore-job-1",
    backup_id: archive.id,
    initiator: "admin",
    status: "rolling_back",
    started_at_unix_ms: 1,
    deadline_unix_ms: 2,
    updated_unix_ms: 1,
    maintenance_entered: true,
    target_activation: "restore-target",
    logical_bytes: 4096,
    error_code: "switch_failed",
    slots: [
      { hash_slot: 0, status: "verified", attempt: 1, replica_node_ids: [1, 2] },
      { hash_slot: 1, status: "staged", attempt: 1, replica_node_ids: [2, 3] },
    ],
  }
  getBackupDashboardMock.mockResolvedValue(active)

  renderPage()

  const progress = await screen.findByTestId("backup-task-progress")
  expect(progress).toHaveTextContent("Rolling back")
  expect(progress).toHaveTextContent("Restored data: 4.0 KiB")
  expect(progress).toHaveTextContent("Failure: switch_failed")
  expect(progress).toHaveTextContent("Node 1: 1/1 verified slots")
  expect(progress).toHaveTextContent("Node 2: 1/2 verified slots")
  expect(progress).toHaveTextContent("Node 3: 0/1 verified slots")
})

test("does not call backup APIs without read permission", async () => {
  setAuthenticated([])
  renderPage()

  expect(await screen.findByText("You do not have permission to view backup management.")).toBeInTheDocument()
  expect(getBackupDashboardMock).not.toHaveBeenCalled()
})
