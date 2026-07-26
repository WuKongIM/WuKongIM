import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { MemoryRouter } from "react-router-dom"
import { beforeEach, expect, test, vi } from "vitest"

import { createAnonymousAuthState, useAuthStore } from "@/auth/auth-store"
import { I18nProvider } from "@/i18n/provider"
import { resetLocale } from "@/i18n/locale-store"
import type { ManagerBackupStatusResponse } from "@/lib/manager-api.types"
import { BackupsPage } from "@/pages/backups/page"

const getBackupStatusMock = vi.fn()
const getBackupCheckpointsMock = vi.fn()
const publishBackupCheckpointMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getBackupStatus: (...args: unknown[]) => getBackupStatusMock(...args),
    getBackupCheckpoints: (...args: unknown[]) => getBackupCheckpointsMock(...args),
    publishBackupCheckpoint: (...args: unknown[]) => publishBackupCheckpointMock(...args),
  }
})

function status(authEnabled = true): ManagerBackupStatusResponse {
  return {
    enabled: true,
    health: "healthy",
    checkpoint_age_seconds: 30,
    latest_checkpoint: {
      id: "checkpoint-1",
      effective_at_unix_millis: 1_753_056_300_000,
      created_at_unix_millis: 1_753_056_330_000,
      held: false,
    },
    coordinator_node_id: 2,
    observed_at_unix_millis: 1_753_056_360_000,
    auth_enabled: authEnabled,
    running: true,
    max_checkpoint_age_seconds: 300,
    policy: {
      capture_reconcile_interval_seconds: 5,
      checkpoint_interval_seconds: 300,
      capture_worker_count: 4,
      staging_max_bytes: 1024,
      source_pin_max_age_seconds: 3600,
      max_source_pinned_bytes: 4096,
    },
    capture_leases: [],
    local_capture_statuses: [{
      hash_slot: 0,
      state: "idle",
      lease_current: true,
      frontier_revision: 2,
      metadata_source_watermark: 10,
      message_source_watermark: 20,
      metadata_frontier_watermark: 10,
      message_frontier_watermark: 20,
      observed_at_unix_millis: 1_753_056_360_000,
    }],
    erasure_streams: [],
  }
}

const checkpoint = {
  id: "checkpoint-1",
  effective_at_unix_millis: 1_753_056_300_000,
  created_at_unix_millis: 1_753_056_330_000,
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
  getBackupCheckpointsMock.mockResolvedValue({ items: [checkpoint], total: 1 })
  publishBackupCheckpointMock.mockResolvedValue({ checkpoint })
  useAuthStore.setState({
    ...createAnonymousAuthState(),
    status: "authenticated",
    isHydrated: true,
    permissions: [{ resource: "cluster.backup", actions: ["r", "w"] }],
  })
})

test("publishes the current continuous frontier after explicit confirmation", async () => {
  const user = userEvent.setup()
  renderPage()

  await screen.findByText("Cluster backup status")
  await user.click(screen.getByRole("button", { name: "Publish checkpoint" }))
  expect(screen.getByRole("dialog")).toHaveTextContent("complete Hash Slot frontier")
  await user.click(screen.getByRole("button", { name: "Confirm publication" }))

  await waitFor(() => expect(publishBackupCheckpointMock).toHaveBeenCalledTimes(1))
})

test("forces checkpoint publication read-only when manager authentication is disabled", async () => {
  getBackupStatusMock.mockResolvedValue(status(false))
  renderPage()

  const publish = await screen.findByRole("button", { name: "Publish checkpoint" })
  expect(publish).toBeDisabled()
  await waitFor(() => expect(publish).toHaveAttribute("title", expect.stringMatching(/authentication/i)))
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
  expect(getBackupCheckpointsMock).not.toHaveBeenCalled()
})

test("lists immutable checkpoints and applies the ID filter only on submit", async () => {
  const user = userEvent.setup()
  renderPage()

  await screen.findByText("Cluster backup status")
  await user.click(screen.getByRole("tab", { name: "Checkpoints" }))
  expect(screen.getByText("checkpoint-1")).toBeInTheDocument()

  const initialCalls = getBackupCheckpointsMock.mock.calls.length
  await user.type(screen.getByRole("textbox", { name: "Search checkpoint ID" }), "exact")
  expect(getBackupCheckpointsMock).toHaveBeenCalledTimes(initialCalls)

  await user.click(screen.getByRole("button", { name: "Search" }))
  await waitFor(() => expect(getBackupCheckpointsMock).toHaveBeenLastCalledWith(expect.objectContaining({
    id: "exact",
  })))
})

test("shows checkpoint catalog failures even when status succeeds", async () => {
  getBackupCheckpointsMock.mockRejectedValue(new Error("catalog unavailable"))
  const user = userEvent.setup()
  renderPage()

  await screen.findByText("Cluster backup status")
  await user.click(screen.getByRole("tab", { name: "Checkpoints" }))
  expect(await screen.findByText("Immutable checkpoint catalog")).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument()
})

test("disables publication when backup is disabled", async () => {
  getBackupStatusMock.mockResolvedValue({
    ...status(),
    enabled: false,
    health: "disabled",
    running: false,
  })
  renderPage()

  const publish = await screen.findByRole("button", { name: "Publish checkpoint" })
  expect(publish).toBeDisabled()
  await waitFor(() => expect(publish).toHaveAttribute("title", "Cluster backup is disabled in startup configuration."))
})
