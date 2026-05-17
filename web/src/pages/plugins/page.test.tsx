import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeEach, expect, test, vi } from "vitest"

import { createAnonymousAuthState, useAuthStore } from "@/auth/auth-store"
import { resetLocale } from "@/i18n/locale-store"
import { I18nProvider } from "@/i18n/provider"
import { ManagerApiError } from "@/lib/manager-api"
import { PluginsPage } from "@/pages/plugins/page"

const getNodesMock = vi.fn()
const getNodePluginsMock = vi.fn()
const getNodePluginMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getNodes: (...args: unknown[]) => getNodesMock(...args),
    getNodePlugins: (...args: unknown[]) => getNodePluginsMock(...args),
    getNodePlugin: (...args: unknown[]) => getNodePluginMock(...args),
  }
})

const nodeRow = {
  node_id: 1,
  name: "node-1",
  addr: "127.0.0.1:7001",
  status: "alive",
  last_heartbeat_at: "2026-05-17T08:00:00Z",
  is_local: false,
  capacity_weight: 1,
  controller: { role: "follower", voter: true, leader_id: 2 },
  slot_stats: { count: 1, leader_count: 0 },
}

const pluginRow = {
  node_id: 2,
  plugin_no: "wk.echo",
  name: "Echo",
  version: "1.0.0",
  config_template: { fields: [{ name: "api_key", type: "secret", label: "API Key" }] },
  config: { api_key: "******" },
  created_at: "2026-05-16T09:00:00Z",
  updated_at: "2026-05-16T09:30:00Z",
  status: "running",
  enabled: true,
  methods: ["Route", "Send"],
  priority: 7,
  persist_after_sync: false,
  reply_sync: true,
  is_ai: 1,
  pid: 123,
  last_seen_at: "2026-05-16T09:31:00Z",
  last_error: "",
}

const failedPluginRow = {
  ...pluginRow,
  plugin_no: "wk.ai.reply",
  name: "AI Reply",
  status: "failed",
  enabled: false,
  methods: ["Receive"],
  priority: 3,
  pid: 0,
  last_error: "process exited",
}

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  getNodesMock.mockReset()
  getNodePluginsMock.mockReset()
  getNodePluginMock.mockReset()
  getNodesMock.mockResolvedValue({
    generated_at: "2026-05-17T08:00:00Z",
    controller_leader_id: 2,
    total: 2,
    items: [
      nodeRow,
      { ...nodeRow, node_id: 2, name: "node-2", is_local: true, controller: { role: "leader", voter: true, leader_id: 2 } },
    ],
  })
  useAuthStore.setState({
    ...createAnonymousAuthState(),
    isHydrated: true,
    status: "authenticated",
    username: "admin",
    tokenType: "Bearer",
    accessToken: "token-1",
    expiresAt: "2099-04-22T12:00:00Z",
    permissions: [{ resource: "cluster.plugin", actions: ["r", "w"] }],
  })
})

function renderPluginsPage() {
  return render(
    <I18nProvider>
      <PluginsPage />
    </I18nProvider>,
  )
}

test("renders node plugin inventory with summary counts", async () => {
  getNodePluginsMock.mockResolvedValueOnce({ node_id: 2, total: 2, items: [pluginRow, failedPluginRow] })

  renderPluginsPage()

  expect(await screen.findByText("wk.echo")).toBeInTheDocument()
  expect(screen.getByLabelText("Node filter")).toHaveValue("2")
  expect(getNodePluginsMock).toHaveBeenCalledWith(2)
  expect(screen.getByText("2 plugins")).toBeInTheDocument()
  expect(screen.getByText("1 running")).toBeInTheDocument()
  expect(screen.getByText("1 failed")).toBeInTheDocument()
  expect(screen.getByText("1 enabled")).toBeInTheDocument()
  expect(screen.getByText("Route, Send")).toBeInTheDocument()
  expect(screen.getByText("process exited")).toBeInTheDocument()
})

test("opens plugin detail from manager APIs", async () => {
  getNodePluginsMock.mockResolvedValueOnce({ node_id: 2, total: 1, items: [pluginRow] })
  getNodePluginMock.mockResolvedValueOnce(pluginRow)

  const user = userEvent.setup()
  renderPluginsPage()

  expect(await screen.findByText("wk.echo")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "View plugin wk.echo details" }))

  const dialog = await screen.findByRole("dialog")
  expect(within(dialog).getByText("Plugin details")).toBeInTheDocument()
  expect(within(dialog).getByText("API Key")).toBeInTheDocument()
  expect(within(dialog).getByText("******")).toBeInTheDocument()
  expect(getNodePluginMock).toHaveBeenCalledWith(2, "wk.echo")
})

test("maps plugin inventory errors to resource states", async () => {
  getNodePluginsMock.mockRejectedValueOnce(new ManagerApiError(403, "forbidden", "forbidden"))
  const { unmount } = renderPluginsPage()

  expect(await screen.findByText("You do not have permission to view this manager resource.")).toBeInTheDocument()
  unmount()

  getNodePluginsMock.mockRejectedValueOnce(new ManagerApiError(501, "not_implemented", "unsupported"))
  renderPluginsPage()
  expect(await screen.findByText("The manager service is currently unavailable.")).toBeInTheDocument()
  unmount()

  getNodePluginsMock.mockRejectedValueOnce(new ManagerApiError(503, "service_unavailable", "unavailable"))
  renderPluginsPage()
  expect(await screen.findByText("The manager service is currently unavailable.")).toBeInTheDocument()
})
