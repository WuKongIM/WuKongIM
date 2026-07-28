import { act, render, screen, waitFor } from "@testing-library/react"
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
const getBackupCheckpointMock = vi.fn()
const publishBackupCheckpointMock = vi.fn()
const setBackupCheckpointHoldMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getBackupStatus: (...args: unknown[]) => getBackupStatusMock(...args),
    getBackupCheckpoints: (...args: unknown[]) => getBackupCheckpointsMock(...args),
    getBackupCheckpoint: (...args: unknown[]) => getBackupCheckpointMock(...args),
    publishBackupCheckpoint: (...args: unknown[]) => publishBackupCheckpointMock(...args),
    setBackupCheckpointHold: (...args: unknown[]) => setBackupCheckpointHoldMock(...args),
  }
})

function status(authEnabled = true): ManagerBackupStatusResponse {
  return {
    enabled: true,
    health: "healthy",
    checkpoint_age_seconds: 60,
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
      target_segment_bytes: 64,
      max_segment_bytes: 256,
      max_segment_open_duration_seconds: 30,
      staging_max_bytes: 1024,
      source_pin_max_age_seconds: 3600,
      max_source_pinned_bytes: 4096,
    },
    capture_leases: [],
    capture_statuses: [{
      hash_slot: 0,
      state: "idle",
      lease_current: true,
      frontier_revision: 2,
      metadata_source_watermark: 10,
      message_source_watermark: 20,
      metadata_frontier_watermark: 10,
      message_frontier_watermark: 20,
      metadata_lag: 0,
      message_lag: 0,
      observed_at_unix_millis: 1_753_056_360_000,
    }],
    capture_status_complete: true,
    capture_status_missing_node_ids: [],
    capture_status_missing_slots: [],
    integrity_audit: {
      revision: 1,
      slots: [],
      debt_objects: 0,
      last_success_at_unix_millis: 1_753_056_350_000,
      updated_at_unix_millis: 1_753_056_350_000,
    },
    compaction: { debt_slots: 0, slots: [] },
    garbage_collection: { debt_repositories: 0, cursors: [] },
    erasure_streams: [],
  }
}

const checkpoint = {
  id: "checkpoint-1",
  effective_at_unix_millis: 1_753_056_300_000,
  created_at_unix_millis: 1_753_056_330_000,
  held: false,
}

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((resolvePromise) => {
    resolve = resolvePromise
  })
  return { promise, resolve }
}

function renderPage(initialEntry = "/cluster/backups") {
  return render(
    <I18nProvider>
      <MemoryRouter initialEntries={[initialEntry]}>
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
  getBackupCheckpointMock.mockResolvedValue({
    ...checkpoint,
    source_cluster_id: "source-cluster",
    source_generation: "generation-7",
    hash_slot_count: 256,
    erasure_streams: [],
  })
  publishBackupCheckpointMock.mockResolvedValue({ checkpoint })
  setBackupCheckpointHoldMock.mockResolvedValue({ ...checkpoint, held: true })
  useAuthStore.setState({
    ...createAnonymousAuthState(),
    status: "authenticated",
    isHydrated: true,
    permissions: [{ resource: "cluster.backup", actions: ["r", "w"] }],
  })
})

test("summarizes healthy protection around the latest recoverable time", async () => {
  renderPage()

  expect(await screen.findByText("Backup protection is healthy")).toBeInTheDocument()
  expect(screen.getByText("Latest recoverable time")).toBeInTheDocument()
  expect(screen.getByText(/1 minute ago/)).toBeInTheDocument()
  expect(screen.queryByRole("button", { name: "Publish checkpoint" })).not.toBeInTheDocument()
})

test("keeps only the three most recent restore points on the overview", async () => {
  getBackupCheckpointsMock.mockResolvedValue({
    items: [
      checkpoint,
      { ...checkpoint, id: "checkpoint-2", effective_at_unix_millis: 1_753_056_000_000 },
      { ...checkpoint, id: "checkpoint-3", effective_at_unix_millis: 1_753_055_700_000 },
      { ...checkpoint, id: "checkpoint-4", effective_at_unix_millis: 1_753_055_400_000 },
    ],
    total: 4,
  })
  renderPage()

  expect(await screen.findByText("Recent restore points")).toBeInTheDocument()
  expect(screen.getByText("checkpoint-1")).toBeInTheDocument()
  expect(screen.getByText("checkpoint-2")).toBeInTheDocument()
  expect(screen.getByText("checkpoint-3")).toBeInTheDocument()
  expect(screen.queryByText("checkpoint-4")).not.toBeInTheDocument()
  expect(screen.getByRole("button", { name: "View all restore points" })).toBeInTheDocument()
})

test("publishes the current continuous frontier after explicit confirmation", async () => {
  const user = userEvent.setup()
  renderPage()

  await screen.findByText("Backup protection is healthy")
  await user.click(screen.getByRole("button", { name: "More backup actions" }))
  await user.click(screen.getByRole("button", { name: "Publish current recovery point" }))
  expect(screen.getByRole("dialog")).toHaveTextContent("complete Hash Slot frontier")
  await user.click(screen.getByRole("button", { name: "Confirm publication" }))

  await waitFor(() => expect(publishBackupCheckpointMock).toHaveBeenCalledTimes(1))
})

test("hides every write entry when manager authentication is disabled", async () => {
  getBackupStatusMock.mockResolvedValue(status(false))
  renderPage()

  expect(await screen.findByText("Read-only")).toBeInTheDocument()
  expect(screen.queryByRole("button", { name: "More backup actions" })).not.toBeInTheDocument()
  expect(screen.queryByRole("button", { name: "Publish current recovery point" })).not.toBeInTheDocument()
})

test("promotes recovery and capture problems into the health summary", async () => {
  getBackupStatusMock.mockResolvedValue({
    ...status(),
    health: "degraded",
    checkpoint_age_seconds: 420,
    capture_status_complete: false,
    capture_status_missing_node_ids: [3],
    capture_status_missing_slots: [17, 18],
  })
  renderPage()

  expect(await screen.findByText("Backup needs attention")).toBeInTheDocument()
  expect(screen.getAllByText("Recovery target exceeded by 2 minutes").length).toBeGreaterThan(0)
  expect(screen.getByText(
    "2 Slot observations across 1 lease holders are unavailable.",
  )).toBeInTheDocument()
  expect(screen.queryByText("Backup protection is healthy")).not.toBeInTheDocument()
})

test("shows a single setup guide when continuous backup is disabled", async () => {
  getBackupStatusMock.mockResolvedValue({
    ...status(),
    enabled: false,
    health: "disabled",
    latest_checkpoint: undefined,
    capture_status_complete: false,
    capture_status_missing_node_ids: [],
    capture_status_missing_slots: [],
  })
  renderPage()

  expect(await screen.findByText("Continuous backup is not enabled")).toBeInTheDocument()
  expect(screen.getByText("Prepare two repositories, deploy the key package, then enable backup in startup configuration.")).toBeInTheDocument()
  expect(screen.queryByText("Latest recoverable time")).not.toBeInTheDocument()
  expect(screen.queryByRole("tab")).not.toBeInTheDocument()
})

test("shows the setup guide even when a disabled deployment opens a restore-points URL", async () => {
  getBackupStatusMock.mockResolvedValue({
    ...status(),
    enabled: false,
    health: "disabled",
  })
  renderPage("/cluster/backups?tab=checkpoints")

  expect(await screen.findByText("Continuous backup is not enabled")).toBeInTheDocument()
  expect(screen.queryByText("Search restore point ID")).not.toBeInTheDocument()
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

test("keeps the last successful data visible when a refresh fails", async () => {
  const user = userEvent.setup()
  renderPage()

  expect(await screen.findByText("Backup protection is healthy")).toBeInTheDocument()
  getBackupStatusMock.mockRejectedValueOnce(new Error("status unavailable"))
  await user.click(screen.getByRole("button", { name: "Refresh" }))

  expect(await screen.findByText("Refresh failed. Showing the last known backup data.")).toBeInTheDocument()
  expect(screen.getByText("Backup protection is healthy")).toBeInTheDocument()
})

test("lists restore points and applies search, protection, and date filters only on submit", async () => {
  const user = userEvent.setup()
  renderPage()

  await screen.findByText("Backup protection is healthy")
  await user.click(screen.getByRole("tab", { name: "Restore points" }))
  expect(screen.getAllByText("checkpoint-1").length).toBeGreaterThan(0)

  const initialCalls = getBackupCheckpointsMock.mock.calls.length
  await user.type(screen.getByRole("textbox", { name: "Search restore point ID" }), "exact")
  await user.click(screen.getByRole("button", { name: "Filters" }))
  await user.selectOptions(screen.getByRole("combobox", { name: "Protection" }), "true")
  await user.type(screen.getByLabelText("Recoverable from"), "2025-07-01")
  await user.type(screen.getByLabelText("Recoverable to"), "2025-07-31")
  expect(getBackupCheckpointsMock).toHaveBeenCalledTimes(initialCalls)

  await user.click(screen.getByRole("button", { name: "Apply filters" }))
  await waitFor(() => expect(getBackupCheckpointsMock).toHaveBeenLastCalledWith(expect.objectContaining({
    id: "exact",
    held: true,
    effectiveFrom: new Date("2025-07-01T00:00:00").getTime(),
    effectiveTo: new Date("2025-08-01T00:00:00").getTime() - 1,
  })))
})

test("keeps the newest restore-point filter result when requests finish out of order", async () => {
  const user = userEvent.setup()
  const first = deferred<{ items: typeof checkpoint[]; total: number }>()
  const second = deferred<{ items: typeof checkpoint[]; total: number }>()
  const checkpointTwo = { ...checkpoint, id: "checkpoint-2" }
  getBackupCheckpointsMock
    .mockResolvedValueOnce({ items: [checkpoint], total: 1 })
    .mockImplementationOnce(() => first.promise)
    .mockImplementationOnce(() => second.promise)
  renderPage()

  await screen.findByText("Backup protection is healthy")
  await user.click(screen.getByRole("tab", { name: "Restore points" }))
  const search = screen.getByRole("textbox", { name: "Search restore point ID" })
  await user.type(search, "first")
  await user.click(screen.getByRole("button", { name: "Apply filters" }))
  await waitFor(() => expect(getBackupCheckpointsMock).toHaveBeenLastCalledWith(
    expect.objectContaining({ id: "first" }),
  ))

  await user.clear(search)
  await user.type(search, "second")
  await user.click(screen.getByRole("button", { name: "Apply filters" }))
  await waitFor(() => expect(getBackupCheckpointsMock).toHaveBeenLastCalledWith(
    expect.objectContaining({ id: "second" }),
  ))

  await act(async () => {
    second.resolve({ items: [checkpointTwo], total: 1 })
    await second.promise
  })
  expect((await screen.findAllByText("checkpoint-2")).length).toBeGreaterThan(0)

  await act(async () => {
    first.resolve({ items: [checkpoint], total: 1 })
    await first.promise
  })
  expect(screen.queryByText("checkpoint-1")).not.toBeInTheDocument()
  expect(screen.getAllByText("checkpoint-2").length).toBeGreaterThan(0)
})

test("opens one restore point with recovery, protection, and technical details", async () => {
  const user = userEvent.setup()
  renderPage()

  await screen.findByText("Backup protection is healthy")
  await user.click(screen.getByRole("tab", { name: "Restore points" }))
  await user.click(screen.getAllByRole("button", { name: "View checkpoint-1 details" })[0])

  expect(await screen.findByRole("dialog")).toHaveTextContent("Recovery preparation")
  expect(screen.getByRole("dialog")).toHaveTextContent("source-cluster")
  expect(getBackupCheckpointMock).toHaveBeenCalledWith("checkpoint-1")
  expect(screen.getByRole("button", { name: "Prepare recovery" })).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Protect from cleanup" })).toBeInTheDocument()
})

test("keeps the newest restore-point detail when requests finish out of order", async () => {
  const user = userEvent.setup()
  const first = deferred<Awaited<ReturnType<typeof getBackupCheckpointMock>>>()
  const second = deferred<Awaited<ReturnType<typeof getBackupCheckpointMock>>>()
  const checkpointTwo = { ...checkpoint, id: "checkpoint-2" }
  getBackupCheckpointsMock.mockResolvedValue({
    items: [checkpoint, checkpointTwo],
    total: 2,
  })
  getBackupCheckpointMock.mockImplementation((checkpointID: string) => (
    checkpointID === "checkpoint-1" ? first.promise : second.promise
  ))
  renderPage()

  await screen.findByText("Backup protection is healthy")
  await user.click(screen.getByRole("tab", { name: "Restore points" }))
  await user.click(screen.getAllByRole("button", { name: "View checkpoint-1 details" })[0])
  await user.click(screen.getByRole("button", { name: "Close" }))
  await user.click(screen.getAllByRole("button", { name: "View checkpoint-2 details" })[0])

  await act(async () => {
    second.resolve({
      ...checkpointTwo,
      source_cluster_id: "source-cluster-2",
      source_generation: "generation-8",
      hash_slot_count: 256,
      erasure_streams: [],
    })
    await second.promise
  })
  expect(await screen.findByText("source-cluster-2")).toBeInTheDocument()

  await act(async () => {
    first.resolve({
      ...checkpoint,
      source_cluster_id: "source-cluster-1",
      source_generation: "generation-7",
      hash_slot_count: 256,
      erasure_streams: [],
    })
    await first.promise
  })
  expect(screen.getByRole("dialog")).toHaveTextContent("source-cluster-2")
  expect(screen.queryByText("source-cluster-1")).not.toBeInTheDocument()
})

test("protects a restore point directly and hides protection actions from read-only users", async () => {
  const user = userEvent.setup()
  renderPage()

  await screen.findByText("Backup protection is healthy")
  await user.click(screen.getByRole("tab", { name: "Restore points" }))
  await user.click(screen.getAllByRole("button", { name: "View checkpoint-1 details" })[0])
  await screen.findByText("Recovery preparation")
  await user.click(screen.getByRole("button", { name: "Protect from cleanup" }))
  await waitFor(() => expect(setBackupCheckpointHoldMock).toHaveBeenCalledWith("checkpoint-1", true))

  act(() => {
    useAuthStore.setState({
      ...useAuthStore.getState(),
      permissions: [{ resource: "cluster.backup", actions: ["r"] }],
    })
  })
  await waitFor(() => {
    expect(screen.queryByRole("button", { name: "Protect from cleanup" })).not.toBeInTheDocument()
    expect(screen.queryByRole("button", { name: "Release protection" })).not.toBeInTheDocument()
  })
  expect(screen.getByRole("button", { name: "Prepare recovery" })).toBeInTheDocument()
})

test("reloads the active protection filter after changing retention protection", async () => {
  const user = userEvent.setup()
  getBackupCheckpointsMock
    .mockResolvedValueOnce({ items: [checkpoint], total: 1 })
    .mockResolvedValueOnce({ items: [checkpoint], total: 1 })
    .mockResolvedValueOnce({ items: [], total: 0 })
  renderPage()

  await screen.findByText("Backup protection is healthy")
  await user.click(screen.getByRole("tab", { name: "Restore points" }))
  await user.click(screen.getByRole("button", { name: "Filters" }))
  await user.selectOptions(screen.getByRole("combobox", { name: "Protection" }), "false")
  await user.click(screen.getByRole("button", { name: "Apply filters" }))
  await waitFor(() => expect(getBackupCheckpointsMock).toHaveBeenLastCalledWith(
    expect.objectContaining({ held: false }),
  ))

  await user.click(screen.getAllByRole("button", { name: "View checkpoint-1 details" })[0])
  await screen.findByText("Recovery preparation")
  await user.click(screen.getByRole("button", { name: "Protect from cleanup" }))

  expect(await screen.findByText("No matching restore points.")).toBeInTheDocument()
  expect(getBackupCheckpointsMock).toHaveBeenLastCalledWith(
    expect.objectContaining({ held: false }),
  )
})

test("requires the exact restore point ID before releasing cleanup protection", async () => {
  const user = userEvent.setup()
  const protectedCheckpoint = { ...checkpoint, held: true }
  getBackupCheckpointsMock.mockResolvedValue({ items: [protectedCheckpoint], total: 1 })
  getBackupCheckpointMock.mockResolvedValue({
    ...protectedCheckpoint,
    source_cluster_id: "source-cluster",
    source_generation: "generation-7",
    hash_slot_count: 256,
    erasure_streams: [],
  })
  setBackupCheckpointHoldMock.mockResolvedValue({ ...protectedCheckpoint, held: false })
  renderPage()

  await screen.findByText("Backup protection is healthy")
  await user.click(screen.getByRole("tab", { name: "Restore points" }))
  await user.click(screen.getAllByRole("button", { name: "View checkpoint-1 details" })[0])
  await screen.findByText("Recovery preparation")
  await user.click(screen.getByRole("button", { name: "Release protection" }))

  const confirmation = screen.getByRole("textbox", { name: "Restore point ID" })
  const release = screen.getByRole("button", { name: "Confirm release" })
  await user.type(confirmation, "wrong")
  expect(release).toBeDisabled()
  await user.clear(confirmation)
  await user.type(confirmation, "checkpoint-1")
  await user.click(release)

  await waitFor(() => expect(setBackupCheckpointHoldMock).toHaveBeenCalledWith("checkpoint-1", false))
})

test("shows checkpoint catalog failures even when status succeeds", async () => {
  getBackupCheckpointsMock.mockRejectedValue(new Error("catalog unavailable"))
  const user = userEvent.setup()
  renderPage()

  await screen.findByText("Backup protection is healthy")
  await user.click(screen.getByRole("tab", { name: "Restore points" }))
  expect((await screen.findAllByText("Restore points")).length).toBeGreaterThan(0)
  expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument()
})

test("hides publication when backup is disabled", async () => {
  getBackupStatusMock.mockResolvedValue({
    ...status(),
    enabled: false,
    health: "disabled",
    running: false,
  })
  renderPage()

  expect(await screen.findByText("Continuous backup is not enabled")).toBeInTheDocument()
  expect(screen.queryByRole("button", { name: "More backup actions" })).not.toBeInTheDocument()
  expect(screen.queryByRole("button", { name: "Publish current recovery point" })).not.toBeInTheDocument()
})
