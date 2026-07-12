import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { MemoryRouter } from "react-router-dom"
import { beforeEach, expect, test, vi } from "vitest"

import { createAnonymousAuthState, useAuthStore } from "@/auth/auth-store"
import { resetLocale, setLocale } from "@/i18n/locale-store"
import { I18nProvider } from "@/i18n/provider"
import { ManagerApiError } from "@/lib/manager-api"
import { ConversationsPage } from "@/pages/conversations/page"

const getRecentConversationsMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getRecentConversations: (...args: unknown[]) => getRecentConversationsMock(...args),
  }
})

const conversationPage = {
  uid: "u1",
  limit: 50,
  msg_count: 1,
  only_unread: false,
  truncated: true,
  items: [{
    uid: "u1",
    channel_id: "g1",
    channel_type: 2,
    unread: 4,
    timestamp: 1778852000,
    last_msg_seq: 12,
    last_client_msg_no: "c12",
    read_to_msg_seq: 8,
    version: 1000,
    recent_messages: [{
      message_id: "99",
      message_seq: 12,
      client_msg_no: "c12",
      channel_id: "g1",
      channel_type: 2,
      from_uid: "u2",
      timestamp: 1778852000,
      payload: btoa("hello manager"),
    }],
  }],
}

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  getRecentConversationsMock.mockReset()
  useAuthStore.setState({
    ...createAnonymousAuthState(),
    isHydrated: true,
    status: "authenticated",
    username: "admin",
    tokenType: "Bearer",
    accessToken: "token-1",
    expiresAt: "2099-04-22T12:00:00Z",
    permissions: [{ resource: "cluster.channel", actions: ["r"] }],
  })
})

function renderConversationsPage(initialEntry = "/business/conversations") {
  return render(
    <MemoryRouter initialEntries={[initialEntry]}>
      <I18nProvider>
        <ConversationsPage />
      </I18nProvider>
    </MemoryRouter>,
  )
}

test("renders initial empty prompt", () => {
  renderConversationsPage()

  expect(screen.getByRole("heading", { name: "Recent Conversations" })).toBeInTheDocument()
  expect(screen.getByText("Enter a UID to query recent conversations.")).toBeInTheDocument()
  expect(getRecentConversationsMock).not.toHaveBeenCalled()
})

test("queries conversations by UID and renders previews", async () => {
  getRecentConversationsMock.mockResolvedValueOnce(conversationPage)

  const user = userEvent.setup()
  renderConversationsPage()

  await user.type(screen.getByPlaceholderText("Search UID"), "u1")
  await user.click(screen.getByRole("button", { name: "Search" }))

  expect(await screen.findByText("g1")).toBeInTheDocument()
  expect(screen.getByText("hello manager")).toBeInTheDocument()
  expect(screen.getByText("Results truncated. Increase limit or refine the query.")).toBeInTheDocument()
  expect(getRecentConversationsMock).toHaveBeenCalledWith({ uid: "u1", limit: 50, msgCount: 1, onlyUnread: false })
  expect(screen.getByRole("link", { name: "View messages for g1" })).toHaveAttribute("href", "/business/messages?channel_id=g1&channel_type=2")
})

test("uses an editorial conversations query toolbar and named result table", async () => {
  getRecentConversationsMock.mockResolvedValueOnce({
    uid: "u1",
    limit: 20,
    msg_count: 0,
    only_unread: false,
    truncated: true,
    items: [{
      uid: "u1",
      channel_id: "room-1",
      channel_type: 2,
      unread: 3,
      timestamp: 1713859200,
      last_msg_seq: 99,
      last_client_msg_no: "",
      read_to_msg_seq: 0,
      version: 1,
      recent_messages: [],
    }],
  })

  const user = userEvent.setup()
  renderConversationsPage()

  const toolbar = screen.getByTestId("conversations-query-toolbar")
  expect(toolbar).toHaveClass("grid", "gap-3", "border-b", "border-border", "pb-4")

  await user.type(within(toolbar).getByLabelText("UID"), "u1")
  await user.click(within(toolbar).getByRole("button", { name: "Search" }))

  const table = await screen.findByRole("table", { name: "Recent Conversations" })
  const resultSurface = table.closest("[data-conversations-surface='results']")
  expect(resultSurface).toHaveClass("overflow-x-auto", "rounded-md", "border", "border-border")
  expect(table).toHaveClass("text-sm")

  const metadata = screen.getByTestId("conversations-metadata-row")
  expect(metadata).toHaveClass("border-b", "border-border", "pb-3")
  expect(within(metadata).getByText("Loaded: 1")).toBeInTheDocument()
  expect(within(metadata).getByText("Results truncated. Increase limit or refine the query.")).toBeInTheDocument()
  const refreshButton = within(toolbar).getByRole("button", { name: "Refresh" })
  expect(refreshButton).toBeInTheDocument()

  getRecentConversationsMock.mockReturnValueOnce(new Promise(() => {}))
  await user.click(refreshButton)

  expect(await within(toolbar).findByRole("button", { name: "Refreshing..." })).toBeDisabled()
})

test("renders conversations metadata through locale messages", async () => {
  setLocale("zh-CN")
  getRecentConversationsMock.mockResolvedValueOnce(conversationPage)

  const user = userEvent.setup()
  renderConversationsPage()

  await user.type(screen.getByLabelText("UID"), "u1")
  await user.click(screen.getByRole("button", { name: "搜索" }))

  const metadata = await screen.findByTestId("conversations-metadata-row")
  expect(within(metadata).getByText("已加载：1")).toBeInTheDocument()
  expect(within(metadata).getByText("结果已截断。请调大数量或细化查询。")).toBeInTheDocument()
})

test("auto queries uid from URL and passes only unread", async () => {
  getRecentConversationsMock.mockResolvedValueOnce({ ...conversationPage, only_unread: true, truncated: false })

  renderConversationsPage("/business/conversations?uid=u1&only_unread=true")

  expect(await screen.findByText("g1")).toBeInTheDocument()
  expect(getRecentConversationsMock).toHaveBeenCalledWith({ uid: "u1", limit: 50, msgCount: 1, onlyUnread: true })
})

test("validates limit and message count", async () => {
  const user = userEvent.setup()
  renderConversationsPage()

  await user.type(screen.getByPlaceholderText("Search UID"), "u1")
  await user.clear(screen.getByLabelText("Limit"))
  await user.type(screen.getByLabelText("Limit"), "0")
  await user.click(screen.getByRole("button", { name: "Search" }))
  expect(screen.getByText("Limit must be between 1 and 200.")).toBeInTheDocument()

  await user.clear(screen.getByLabelText("Limit"))
  await user.type(screen.getByLabelText("Limit"), "50")
  await user.clear(screen.getByLabelText("Message previews"))
  await user.type(screen.getByLabelText("Message previews"), "11")
  await user.click(screen.getByRole("button", { name: "Search" }))
  expect(screen.getByText("Message previews must be between 0 and 10.")).toBeInTheDocument()
})

test("maps permission and availability errors", async () => {
  getRecentConversationsMock.mockRejectedValueOnce(new ManagerApiError(403, "forbidden", "forbidden"))
  const { unmount } = renderConversationsPage("/business/conversations?uid=u1")

  expect(await screen.findByText("You do not have permission to view this manager resource.")).toBeInTheDocument()
  unmount()

  getRecentConversationsMock.mockRejectedValueOnce(new ManagerApiError(503, "service_unavailable", "unavailable"))
  renderConversationsPage("/business/conversations?uid=u1")

  expect(await screen.findByText("The manager service is currently unavailable.")).toBeInTheDocument()
})
