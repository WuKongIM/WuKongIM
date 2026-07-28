import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { MemoryRouter, Route, Routes } from "react-router-dom"
import { beforeEach, expect, test, vi } from "vitest"

import { createAnonymousAuthState, useAuthStore } from "@/auth/auth-store"
import { I18nProvider } from "@/i18n/provider"
import { resetLocale, setLocale } from "@/i18n/locale-store"
import { BackupRecoveryPage } from "@/pages/backups/recovery-page"

const getBackupCheckpointMock = vi.fn()
const getBackupCheckpointsMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getBackupCheckpoint: (...args: unknown[]) => getBackupCheckpointMock(...args),
    getBackupCheckpoints: (...args: unknown[]) => getBackupCheckpointsMock(...args),
  }
})

function renderPage() {
  return render(
    <I18nProvider>
      <MemoryRouter initialEntries={["/cluster/backups/recovery/checkpoint-1"]}>
        <Routes>
          <Route
            element={<BackupRecoveryPage />}
            path="/cluster/backups/recovery/:checkpointId"
          />
        </Routes>
      </MemoryRouter>
    </I18nProvider>,
  )
}

beforeEach(() => {
  resetLocale()
  localStorage.clear()
  vi.clearAllMocks()
  getBackupCheckpointMock.mockResolvedValue({
    id: "checkpoint-1",
    effective_at_unix_millis: 1_753_056_300_000,
    created_at_unix_millis: 1_753_056_330_000,
    held: true,
    source_cluster_id: "source-cluster",
    source_generation: "generation-7",
    hash_slot_count: 256,
    erasure_streams: [],
  })
  getBackupCheckpointsMock.mockResolvedValue({
    catalog_head_token: "catalog-token",
    items: [],
    total: 1,
  })
  useAuthStore.setState({
    ...createAnonymousAuthState(),
    status: "authenticated",
    isHydrated: true,
    permissions: [{ resource: "cluster.backup", actions: ["r"] }],
  })
})

test("builds safe copyable recovery commands without collecting secrets", async () => {
  const user = userEvent.setup()
  const clipboardWrite = vi.spyOn(navigator.clipboard, "writeText")
  renderPage()

  expect(await screen.findByText("Prepare cluster recovery")).toBeInTheDocument()
  expect(getBackupCheckpointMock).toHaveBeenCalledWith("checkpoint-1")
  expect(getBackupCheckpointsMock).toHaveBeenCalledWith({ id: "checkpoint-1", limit: 1 })
  expect(screen.queryByRole("textbox", { name: /token/i })).not.toBeInTheDocument()

  const target = screen.getByRole("textbox", { name: "Target Manager URL" })
  await user.type(target, "http://restore.example.com")
  expect(screen.getByText("Use HTTPS, or HTTP only for localhost.")).toBeInTheDocument()
  expect(screen.queryByRole("button", { name: "Copy all commands" })).not.toBeInTheDocument()

  await user.clear(target)
  await user.type(target, "https://restore.example.com?token=must-not-be-here")
  expect(screen.getByText("Use HTTPS, or HTTP only for localhost.")).toBeInTheDocument()
  expect(screen.queryByRole("button", { name: "Copy all commands" })).not.toBeInTheDocument()

  await user.clear(target)
  fireEvent.change(target, { target: { value: "http://[::1]:8080" } })
  expect(await screen.findByRole("button", { name: "Copy all commands" })).toBeInTheDocument()

  await user.clear(target)
  await user.type(target, "https://restore.example.com")
  await user.click(screen.getByRole("radio", { name: "Invalidate restored client tokens" }))

  const commands = await screen.findByText(/wkcli backup restore plan/)
  expect(commands.closest("code")).toHaveTextContent("--checkpoint 'checkpoint-1'")
  expect(commands.closest("code")).toHaveTextContent("--catalog-head 'catalog-token'")
  expect(commands.closest("code")).toHaveTextContent("--invalidate-tokens")
  expect(commands.closest("code")).toHaveTextContent('$WK_MANAGER_TOKEN')

  await user.click(screen.getByRole("button", { name: "Copy all commands" }))
  await waitFor(() => expect(clipboardWrite).toHaveBeenCalledTimes(1))
  expect(clipboardWrite.mock.calls[0][0]).toContain("wkcli backup fence-source")
  expect(clipboardWrite.mock.calls[0][0]).toContain("source-fence-receipt.json")
})

test("localizes the restore point label in exported Markdown", async () => {
  const user = userEvent.setup()
  const createObjectURL = vi.fn(() => "blob:recovery-runbook")
  Object.defineProperty(URL, "createObjectURL", {
    configurable: true,
    value: createObjectURL,
  })
  Object.defineProperty(URL, "revokeObjectURL", {
    configurable: true,
    value: vi.fn(),
  })
  const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {})
  setLocale("zh-CN")
  renderPage()

  expect(await screen.findByText("准备集群恢复")).toBeInTheDocument()
  await user.type(screen.getByRole("textbox", { name: "目标 Manager 地址" }), "https://restore.example.com")
  await user.click(screen.getByRole("button", { name: "更多" }))
  await user.click(screen.getByRole("button", { name: "导出 Markdown" }))

  const exported = createObjectURL.mock.calls[0]?.[0] as Blob
  expect(await exported.text()).toContain("恢复点: `checkpoint-1`")
  expect(await exported.text()).not.toContain("Restore point:")
  click.mockRestore()
})

test("shows one forbidden state without loading restore point data", async () => {
  useAuthStore.setState({
    ...createAnonymousAuthState(),
    status: "authenticated",
    isHydrated: true,
    permissions: [],
  })
  renderPage()

  expect(await screen.findByText("You do not have permission to prepare a recovery.")).toBeInTheDocument()
  expect(getBackupCheckpointMock).not.toHaveBeenCalled()
  expect(getBackupCheckpointsMock).not.toHaveBeenCalled()
})
