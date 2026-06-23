import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react"
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
const updateNodePluginConfigMock = vi.fn()
const restartNodePluginMock = vi.fn()
const deleteNodePluginMock = vi.fn()
const getPluginBindingsMock = vi.fn()
const createPluginBindingMock = vi.fn()
const deletePluginBindingMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getNodes: (...args: unknown[]) => getNodesMock(...args),
    getNodePlugins: (...args: unknown[]) => getNodePluginsMock(...args),
    getNodePlugin: (...args: unknown[]) => getNodePluginMock(...args),
    updateNodePluginConfig: (...args: unknown[]) => updateNodePluginConfigMock(...args),
    restartNodePlugin: (...args: unknown[]) => restartNodePluginMock(...args),
    deleteNodePlugin: (...args: unknown[]) => deleteNodePluginMock(...args),
    getPluginBindings: (...args: unknown[]) => getPluginBindingsMock(...args),
    createPluginBinding: (...args: unknown[]) => createPluginBindingMock(...args),
    deletePluginBinding: (...args: unknown[]) => deletePluginBindingMock(...args),
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
  updateNodePluginConfigMock.mockReset()
  restartNodePluginMock.mockReset()
  deleteNodePluginMock.mockReset()
  getPluginBindingsMock.mockReset()
  createPluginBindingMock.mockReset()
  deletePluginBindingMock.mockReset()
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

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((nextResolve) => {
    resolve = nextResolve
  })
  return { promise, resolve }
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

test("ignores stale plugin inventory responses after switching nodes", async () => {
  const node2Plugins = deferred<{ node_id: number; total: number; items: typeof pluginRow[] }>()
  const node1Plugin = { ...pluginRow, node_id: 1, plugin_no: "wk.node1", name: "Node 1 Plugin" }
  getNodePluginsMock.mockImplementationOnce(() => node2Plugins.promise)
  getNodePluginsMock.mockResolvedValueOnce({ node_id: 1, total: 1, items: [node1Plugin] })

  const user = userEvent.setup()
  renderPluginsPage()

  const nodeFilter = await screen.findByLabelText("Node filter")
  expect(nodeFilter).toHaveValue("2")
  await user.selectOptions(nodeFilter, "1")

  expect(await screen.findByText("wk.node1")).toBeInTheDocument()
  await act(async () => {
    node2Plugins.resolve({ node_id: 2, total: 1, items: [pluginRow] })
    await node2Plugins.promise
  })

  expect(screen.getByText("wk.node1")).toBeInTheDocument()
  expect(screen.queryByText("wk.echo")).not.toBeInTheDocument()
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

test("validates plugin config JSON before updating", async () => {
  getNodePluginsMock.mockResolvedValueOnce({ node_id: 2, total: 1, items: [pluginRow] })

  const user = userEvent.setup()
  renderPluginsPage()

  expect(await screen.findByText("wk.echo")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Configure plugin wk.echo" }))
  const textarea = within(screen.getByRole("dialog")).getByLabelText("Config JSON")
  await user.clear(textarea)
  await user.type(textarea, "not-json")
  await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Update config" }))

  expect(await screen.findByText("Enter a valid JSON object.")).toBeInTheDocument()
  expect(updateNodePluginConfigMock).not.toHaveBeenCalled()

  fireEvent.change(textarea, { target: { value: "[]" } })
  await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Update config" }))

  expect(await screen.findByText("Config must be a JSON object.")).toBeInTheDocument()
  expect(updateNodePluginConfigMock).not.toHaveBeenCalled()
})

test("updates plugin config and refreshes inventory", async () => {
  getNodePluginsMock.mockResolvedValueOnce({ node_id: 2, total: 1, items: [pluginRow] })
  getNodePluginsMock.mockResolvedValueOnce({ node_id: 2, total: 1, items: [{ ...pluginRow, priority: 9 }] })
  updateNodePluginConfigMock.mockResolvedValueOnce({ node_id: 2, plugin_no: "wk.echo", changed: true, plugin: pluginRow })

  const user = userEvent.setup()
  renderPluginsPage()

  expect(await screen.findByText("wk.echo")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Configure plugin wk.echo" }))
  const textarea = within(screen.getByRole("dialog")).getByLabelText("Config JSON")
  fireEvent.change(textarea, { target: { value: "{\"api_key\":\"next\"}" } })
  await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Update config" }))

  await waitFor(() => {
    expect(updateNodePluginConfigMock).toHaveBeenCalledWith(2, "wk.echo", { api_key: "next" })
  })
  expect(getNodePluginsMock).toHaveBeenCalledTimes(2)
})

test("confirms plugin restart and refreshes inventory", async () => {
  getNodePluginsMock.mockResolvedValueOnce({ node_id: 2, total: 1, items: [pluginRow] })
  getNodePluginsMock.mockResolvedValueOnce({ node_id: 2, total: 1, items: [pluginRow] })
  restartNodePluginMock.mockResolvedValueOnce({ node_id: 2, plugin_no: "wk.echo", changed: true, plugin: pluginRow })

  const user = userEvent.setup()
  renderPluginsPage()

  expect(await screen.findByText("wk.echo")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Restart plugin wk.echo" }))
  await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Restart plugin" }))

  await waitFor(() => {
    expect(restartNodePluginMock).toHaveBeenCalledWith(2, "wk.echo")
  })
  expect(getNodePluginsMock).toHaveBeenCalledTimes(2)
})

test("confirms plugin uninstall and refreshes inventory", async () => {
  getNodePluginsMock.mockResolvedValueOnce({ node_id: 2, total: 1, items: [pluginRow] })
  getNodePluginsMock.mockResolvedValueOnce({ node_id: 2, total: 0, items: [] })
  deleteNodePluginMock.mockResolvedValueOnce(undefined)

  const user = userEvent.setup()
  renderPluginsPage()

  expect(await screen.findByText("wk.echo")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Uninstall plugin wk.echo" }))
  await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Uninstall plugin" }))

  await waitFor(() => {
    expect(deleteNodePluginMock).toHaveBeenCalledWith(2, "wk.echo")
  })
  expect(getNodePluginsMock).toHaveBeenCalledTimes(2)
  expect(await screen.findByText("No manager data is available for this view yet.")).toBeInTheDocument()
})

test("queries plugin bindings by UID and plugin number", async () => {
  getNodePluginsMock.mockResolvedValueOnce({ node_id: 2, total: 1, items: [pluginRow] })
  getPluginBindingsMock.mockResolvedValueOnce({
    items: [{ uid: "u1", plugin_no: "wk.echo", warnings: [] }],
    total: 1,
    has_more: false,
  })
  getPluginBindingsMock.mockResolvedValueOnce({
    items: [{ uid: "u2", plugin_no: "wk.echo", warnings: [{ code: "plugin_missing", message: "Plugin missing" }] }],
    total: 1,
    has_more: false,
  })

  const user = userEvent.setup()
  renderPluginsPage()

  expect(await screen.findByText("wk.echo")).toBeInTheDocument()
  await user.type(screen.getByLabelText("Binding query"), "u1")
  await user.click(screen.getByRole("button", { name: "Search bindings" }))

  expect(await screen.findByText("u1")).toBeInTheDocument()
  expect(getPluginBindingsMock).toHaveBeenCalledWith({ uid: "u1" })

  await user.selectOptions(screen.getByLabelText("Binding selector"), "plugin")
  await user.clear(screen.getByLabelText("Binding query"))
  await user.type(screen.getByLabelText("Binding query"), "wk.echo")
  await user.click(screen.getByRole("button", { name: "Search bindings" }))

  expect(await screen.findByText("u2")).toBeInTheDocument()
  expect(screen.getByText("Plugin missing")).toBeInTheDocument()
  expect(getPluginBindingsMock).toHaveBeenLastCalledWith({ pluginNo: "wk.echo", limit: 50 })
})

test("loads more plugin bindings with cursor", async () => {
  getNodePluginsMock.mockResolvedValueOnce({ node_id: 2, total: 1, items: [pluginRow] })
  getPluginBindingsMock.mockResolvedValueOnce({
    items: [{ uid: "u1", plugin_no: "wk.echo", warnings: [] }],
    total: 2,
    next_cursor: "cursor-2",
    has_more: true,
  })
  getPluginBindingsMock.mockResolvedValueOnce({
    items: [{ uid: "u2", plugin_no: "wk.echo", warnings: [] }],
    total: 2,
    has_more: false,
  })

  const user = userEvent.setup()
  renderPluginsPage()

  expect(await screen.findByText("wk.echo")).toBeInTheDocument()
  await user.selectOptions(screen.getByLabelText("Binding selector"), "plugin")
  await user.type(screen.getByLabelText("Binding query"), "wk.echo")
  await user.click(screen.getByRole("button", { name: "Search bindings" }))

  expect(await screen.findByText("u1")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Load more bindings" }))

  expect(await screen.findByText("u2")).toBeInTheDocument()
  expect(screen.getByText("u1")).toBeInTheDocument()
  expect(getPluginBindingsMock).toHaveBeenLastCalledWith({ pluginNo: "wk.echo", limit: 50, cursor: "cursor-2" })
  expect(screen.queryByRole("button", { name: "Load more bindings" })).not.toBeInTheDocument()
})

test("validates empty binding search", async () => {
  getNodePluginsMock.mockResolvedValueOnce({ node_id: 2, total: 1, items: [pluginRow] })

  const user = userEvent.setup()
  renderPluginsPage()

  expect(await screen.findByText("wk.echo")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Search bindings" }))

  expect(await screen.findByText("Enter UID or plugin number.")).toBeInTheDocument()
  expect(getPluginBindingsMock).not.toHaveBeenCalled()
})

test("adds and deletes plugin bindings", async () => {
  getNodePluginsMock.mockResolvedValueOnce({ node_id: 2, total: 1, items: [pluginRow] })
  getPluginBindingsMock.mockResolvedValueOnce({
    items: [{ uid: "u1", plugin_no: "wk.echo", warnings: [] }],
    total: 1,
    has_more: false,
  })
  getPluginBindingsMock.mockResolvedValueOnce({
    items: [{ uid: "u1", plugin_no: "wk.echo", warnings: [] }, { uid: "u2", plugin_no: "wk.echo", warnings: [] }],
    total: 2,
    has_more: false,
  })
  getPluginBindingsMock.mockResolvedValueOnce({
    items: [{ uid: "u2", plugin_no: "wk.echo", warnings: [] }],
    total: 1,
    has_more: false,
  })
  createPluginBindingMock.mockResolvedValueOnce({ binding: { uid: "u2", plugin_no: "wk.echo", warnings: [] }, changed: true })
  deletePluginBindingMock.mockResolvedValueOnce({ binding: { uid: "u1", plugin_no: "wk.echo", warnings: [] }, changed: true })

  const user = userEvent.setup()
  renderPluginsPage()

  expect(await screen.findByText("wk.echo")).toBeInTheDocument()
  await user.type(screen.getByLabelText("Binding query"), "u1")
  await user.click(screen.getByRole("button", { name: "Search bindings" }))
  expect(await screen.findByText("u1")).toBeInTheDocument()

  await user.click(screen.getByRole("button", { name: "Add binding" }))
  await user.type(within(screen.getByRole("dialog")).getByLabelText("UID"), "u2")
  await user.type(within(screen.getByRole("dialog")).getByLabelText("Plugin No"), "wk.echo")
  await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Add binding" }))

  await waitFor(() => {
    expect(createPluginBindingMock).toHaveBeenCalledWith({ uid: "u2", pluginNo: "wk.echo" })
  })
  expect(getPluginBindingsMock).toHaveBeenCalledTimes(2)

  await user.click(screen.getByRole("button", { name: "Delete binding u1 wk.echo" }))
  await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Delete binding" }))

  await waitFor(() => {
    expect(deletePluginBindingMock).toHaveBeenCalledWith({ uid: "u1", pluginNo: "wk.echo" })
  })
  expect(getPluginBindingsMock).toHaveBeenCalledTimes(3)
})
