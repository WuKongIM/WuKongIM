import { act, render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { MemoryRouter } from "react-router-dom"
import { beforeEach, expect, test, vi } from "vitest"

import { createAnonymousAuthState, useAuthStore } from "@/auth/auth-store"
import { I18nProvider } from "@/i18n/provider"
import { resetLocale } from "@/i18n/locale-store"
import { MessagesPage } from "@/pages/messages/page"

const getMessagesMock = vi.fn()
const getChannelRuntimeMetaMock = vi.fn()
const advanceMessageRetentionMock = vi.fn()
const writeTextMock = vi.fn()
const emptyMessagesPage = { items: [], has_more: false }

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getMessages: (...args: unknown[]) => getMessagesMock(...args),
    getChannelRuntimeMeta: (...args: unknown[]) => getChannelRuntimeMetaMock(...args),
    advanceMessageRetention: (...args: unknown[]) => advanceMessageRetentionMock(...args),
  }
})

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  getMessagesMock.mockReset()
  getMessagesMock.mockResolvedValue(emptyMessagesPage)
  getChannelRuntimeMetaMock.mockReset()
  getChannelRuntimeMetaMock.mockResolvedValue({ items: [], has_more: false })
  advanceMessageRetentionMock.mockReset()
  writeTextMock.mockReset()
  Object.defineProperty(window.navigator, "clipboard", {
    configurable: true,
    value: {
      writeText: writeTextMock,
    },
  })
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

function renderMessagesPage(initialEntry = "/messages") {
  return render(
    <I18nProvider>
      <MemoryRouter initialEntries={[initialEntry]}>
        <MessagesPage />
      </MemoryRouter>
    </I18nProvider>,
  )
}

test("uses compact message page chrome without summary cards", async () => {
  getMessagesMock.mockResolvedValueOnce({
    items: [{
      message_id: "101",
      message_seq: 9,
      client_msg_no: "c-101",
      channel_id: "room-1",
      channel_type: 2,
      from_uid: "u1",
      timestamp: 1713859200,
      payload: "aGVsbG8=",
    }],
    has_more: false,
  })

  renderMessagesPage("/messages?channel_id=room-1&channel_type=2")

  expect(await screen.findByText("c-101")).toBeInTheDocument()
  expect(screen.getByText("Scope: channel room-1")).toBeInTheDocument()
  expect(screen.getByText("Loaded: 1")).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Refresh" })).toBeInTheDocument()
  expect(screen.getByLabelText("Channel ID")).toHaveValue("room-1")
  expect(screen.getByLabelText("Channel type")).toHaveValue(2)
  expect(screen.getByRole("button", { name: "Search" })).toBeInTheDocument()
  expect(screen.queryByText("Channel-scoped message search and pagination.")).not.toBeInTheDocument()
  expect(screen.queryByText("Message Filters")).not.toBeInTheDocument()
  expect(screen.queryByText("Filter by channel, message ID, or client message number before querying the manager API.")).not.toBeInTheDocument()
  expect(screen.queryByText("Loaded messages")).not.toBeInTheDocument()
  expect(screen.queryByText("Total matched messages loaded into the current page set.")).not.toBeInTheDocument()
  expect(screen.queryByText("Senders")).not.toBeInTheDocument()
  expect(screen.queryByText("Distinct senders represented in the current result set.")).not.toBeInTheDocument()
  expect(screen.queryByText("Message Query")).not.toBeInTheDocument()
  expect(screen.queryByText("Paged results returned by the manager message query endpoint.")).not.toBeInTheDocument()
  expect(screen.queryByText("Query results")).not.toBeInTheDocument()
  expect(screen.queryByText("Newest matched messages ordered by committed sequence.")).not.toBeInTheDocument()
})

test("uses an editorial message query toolbar and named inventory table", async () => {
  getMessagesMock.mockResolvedValueOnce({
    items: [{
      message_id: "101",
      message_seq: 9,
      client_msg_no: "c-101",
      channel_id: "room-1",
      channel_type: 2,
      from_uid: "u1",
      timestamp: 1713859200,
      payload: "aGVsbG8=",
    }],
    has_more: false,
  })

  renderMessagesPage("/messages?channel_id=room-1&channel_type=2")

  const querySurface = screen.getByTestId("messages-query-surface")
  expect(querySurface).toHaveClass("rounded-lg", "border", "border-border", "bg-card", "p-3")
  expect(querySurface).not.toHaveClass("rounded-xl")

  const queryToolbar = screen.getByTestId("messages-query-toolbar")
  expect(queryToolbar).toHaveClass("grid", "gap-3")
  expect(within(queryToolbar).getByLabelText("Channel ID")).toHaveValue("room-1")
  expect(within(queryToolbar).getByLabelText("Channel type")).toHaveValue(2)
  expect(within(queryToolbar).getByRole("button", { name: "Search" })).toBeInTheDocument()

  const table = await screen.findByRole("table", { name: "Messages" })
  const inventorySurface = table.closest("[data-messages-surface='inventory']")
  expect(inventorySurface).toHaveClass("rounded-lg", "border", "border-border", "bg-card", "p-3")
  expect(inventorySurface).not.toHaveClass("rounded-xl")
  expect(table).toHaveClass("table-fixed", "text-sm")
})

test("auto-runs a message query from URL channel params", async () => {
  getMessagesMock.mockResolvedValueOnce({
    items: [{
      message_id: "101",
      message_seq: 9,
      client_msg_no: "c-101",
      channel_id: "room-1",
      channel_type: 2,
      from_uid: "u1",
      timestamp: 1713859200,
      payload: "aGVsbG8=",
    }],
    has_more: false,
  })

  renderMessagesPage("/messages?channel_id=room-1&channel_type=2")

  expect(await screen.findByText("c-101")).toBeInTheDocument()
  expect(getMessagesMock).toHaveBeenCalledWith({
    channelId: "room-1",
    channelType: 2,
    limit: 50,
    clientMsgNo: "",
  })
  expect(screen.getByLabelText("Channel ID")).toHaveValue("room-1")
  expect(screen.getByLabelText("Channel type")).toHaveValue(2)
})

test("shows the latest messages across the cluster by default", async () => {
  getMessagesMock.mockResolvedValueOnce({
    items: [{
      message_id: "201",
      message_seq: 7,
      client_msg_no: "c-201",
      channel_id: "room-latest",
      channel_type: 2,
      from_uid: "u2",
      timestamp: 1713859300,
      payload: "bGF0ZXN0",
    }],
    has_more: false,
  })

  renderMessagesPage()

  expect(await screen.findByText("c-201")).toBeInTheDocument()
  expect(screen.getByText("Scope: latest across cluster")).toBeInTheDocument()
  expect(screen.getByText("room-latest")).toBeInTheDocument()
  expect(getMessagesMock).toHaveBeenCalledWith({ limit: 50, clientMsgNo: "" })
})

test("does not let a slow default latest request replace a newer channel query", async () => {
  let resolveDefault!: (page: typeof emptyMessagesPage) => void
  getMessagesMock.mockImplementationOnce(() => new Promise((resolve) => {
    resolveDefault = resolve
  }))
  getMessagesMock.mockResolvedValueOnce({
    items: [{
      message_id: "301",
      message_seq: 9,
      client_msg_no: "channel-result",
      channel_id: "room-1",
      channel_type: 2,
      from_uid: "u1",
      timestamp: 1713859400,
      payload: "",
    }],
    has_more: false,
  })

  const user = userEvent.setup()
  renderMessagesPage()
  await waitFor(() => expect(getMessagesMock).toHaveBeenCalledTimes(1))

  await user.type(screen.getByLabelText("Channel ID"), "room-1")
  await user.clear(screen.getByLabelText("Channel type"))
  await user.type(screen.getByLabelText("Channel type"), "2")
  await user.click(screen.getByRole("button", { name: "Search" }))
  expect(await screen.findByText("channel-result")).toBeInTheDocument()

  await act(async () => {
    resolveDefault({ items: [], has_more: false })
  })

  expect(screen.getByText("channel-result")).toBeInTheDocument()
  expect(screen.getByText("Scope: channel room-1")).toBeInTheDocument()
})

test("submits a message query and renders rows", async () => {
  getMessagesMock.mockResolvedValueOnce(emptyMessagesPage)
  getMessagesMock.mockResolvedValueOnce({
    items: [{
      message_id: "101",
      message_seq: 9,
      client_msg_no: "c-101",
      channel_id: "room-1",
      channel_type: 2,
      from_uid: "u1",
      timestamp: 1713859200,
      payload: "aGVsbG8=",
    }],
    has_more: false,
  })

  const user = userEvent.setup()
  renderMessagesPage()

  await user.type(screen.getByLabelText("Channel ID"), "room-1")
  await user.clear(screen.getByLabelText("Channel type"))
  await user.type(screen.getByLabelText("Channel type"), "2")
  await user.click(screen.getByRole("button", { name: "Search" }))

  expect(getMessagesMock).toHaveBeenCalledWith({
    channelId: "room-1",
    channelType: 2,
    limit: 50,
    clientMsgNo: "",
  })
  expect(await screen.findByText("c-101")).toBeInTheDocument()
  expect(screen.getByText("hello")).toBeInTheDocument()
})

test("renders UTF-8 message payloads as readable text", async () => {
  getMessagesMock.mockResolvedValueOnce({
    items: [{
      message_id: "102",
      message_seq: 10,
      client_msg_no: "c-102",
      channel_id: "room-1",
      channel_type: 2,
      from_uid: "u1",
      timestamp: 1713859200,
      payload: "5L2g5aW977yM8J+Riw==",
    }],
    has_more: false,
  })

  renderMessagesPage("/messages?channel_id=room-1&channel_type=2")

  expect(await screen.findByText("你好，👋")).toBeInTheDocument()
  expect(screen.queryByText("5L2g5aW977yM8J+Riw==")).not.toBeInTheDocument()
})

test("submits a uint64 message ID without converting it to a JavaScript number", async () => {
  getMessagesMock.mockResolvedValueOnce(emptyMessagesPage)

  const user = userEvent.setup()
  renderMessagesPage()

  await user.type(screen.getByLabelText("Channel ID"), "room-1")
  await user.clear(screen.getByLabelText("Channel type"))
  await user.type(screen.getByLabelText("Channel type"), "2")
  await user.type(screen.getByLabelText("Message ID"), "2076275923258192001")
  await user.click(screen.getByRole("button", { name: "Search" }))

  expect(getMessagesMock).toHaveBeenLastCalledWith({
    channelId: "room-1",
    channelType: 2,
    limit: 50,
    messageId: "2076275923258192001",
    clientMsgNo: "",
    cursor: undefined,
  })
})

test("keeps long payload text on one line in a dedicated-width payload column", async () => {
  const longPayload = "s".repeat(160)
  getMessagesMock.mockResolvedValueOnce(emptyMessagesPage)
  getMessagesMock.mockResolvedValueOnce({
    items: [{
      message_id: "101",
      message_seq: 9,
      client_msg_no: "c-101",
      channel_id: "room-1",
      channel_type: 2,
      from_uid: "u1",
      timestamp: 1713859200,
      payload: btoa(longPayload),
    }],
    has_more: false,
  })

  const user = userEvent.setup()
  renderMessagesPage()

  await user.type(screen.getByLabelText("Channel ID"), "room-1")
  await user.clear(screen.getByLabelText("Channel type"))
  await user.type(screen.getByLabelText("Channel type"), "2")
  await user.click(screen.getByRole("button", { name: "Search" }))

  const payloadPreview = await screen.findByText(longPayload)
  const payloadCell = payloadPreview.closest("td")
  const table = payloadCell?.closest("table")
  const payloadColumn = table?.querySelectorAll("col").item(6)

  expect(table).toHaveClass("table-fixed")
  expect(payloadColumn).toHaveClass("w-[20rem]")
  expect(payloadPreview).toHaveClass("truncate")
  expect(payloadCell).not.toHaveClass("break-all")
})

test("loads fuzzy channel suggestions from the channel ID input and applies the selection", async () => {
  getChannelRuntimeMetaMock.mockResolvedValueOnce({
    items: [
      {
        channel_id: "room-alpha",
        channel_type: 2,
        slot_id: 9,
        channel_epoch: 7,
        leader_epoch: 3,
        leader: 2,
        replicas: [1, 2, 3],
        isr: [2, 3],
        min_isr: 2,
        max_message_seq: 12,
        status: "active",
      },
    ],
    has_more: false,
  })

  const user = userEvent.setup()
  renderMessagesPage()

  await user.type(screen.getByLabelText("Channel ID"), "room")

  await waitFor(() => {
    expect(getChannelRuntimeMetaMock).toHaveBeenLastCalledWith({ channelId: "room", limit: 15 })
  })
  await user.click(await screen.findByRole("option", { name: /room-alpha.*Type 2/ }))

  expect(screen.getByLabelText("Channel ID")).toHaveValue("room-alpha")
  expect(screen.getByLabelText("Channel type")).toHaveValue(2)
})

test("loads the next page with the same filters", async () => {
  getMessagesMock.mockResolvedValueOnce(emptyMessagesPage)
  getMessagesMock.mockResolvedValueOnce({
    items: [{
      message_id: "101",
      message_seq: 9,
      client_msg_no: "c-101",
      channel_id: "room-1",
      channel_type: 2,
      from_uid: "u1",
      timestamp: 1713859200,
      payload: "aGVsbG8=",
    }],
    has_more: true,
    next_cursor: "cursor-2",
  })
  getMessagesMock.mockResolvedValueOnce({
    items: [{
      message_id: "102",
      message_seq: 8,
      client_msg_no: "c-102",
      channel_id: "room-1",
      channel_type: 2,
      from_uid: "u2",
      timestamp: 1713859201,
      payload: "d29ybGQ=",
    }],
    has_more: false,
  })

  const user = userEvent.setup()
  renderMessagesPage()

  await user.type(screen.getByLabelText("Channel ID"), "room-1")
  await user.clear(screen.getByLabelText("Channel type"))
  await user.type(screen.getByLabelText("Channel type"), "2")
  await user.type(screen.getByLabelText("Client message no"), "dup-1")
  await user.click(screen.getByRole("button", { name: "Search" }))

  expect(await screen.findByText("c-101")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Load more" }))

  expect(getMessagesMock).toHaveBeenNthCalledWith(3, {
    channelId: "room-1",
    channelType: 2,
    limit: 50,
    clientMsgNo: "dup-1",
    cursor: "cursor-2",
  })
  expect(await screen.findByText("c-102")).toBeInTheDocument()
})

test("opens message detail, toggles payload format, and copies the visible payload", async () => {
  getMessagesMock.mockResolvedValueOnce(emptyMessagesPage)
  getMessagesMock.mockResolvedValueOnce({
    items: [{
      message_id: "101",
      message_seq: 9,
      client_msg_no: "c-101",
      channel_id: "room-1",
      channel_type: 2,
      from_uid: "u1",
      timestamp: 1713859200,
      payload: "aGVsbG8=",
    }],
    has_more: false,
  })

  const user = userEvent.setup()
  const writeTextSpy = vi.spyOn(window.navigator.clipboard, "writeText").mockResolvedValue(undefined)
  renderMessagesPage()

  await user.type(screen.getByLabelText("Channel ID"), "room-1")
  await user.clear(screen.getByLabelText("Channel type"))
  await user.type(screen.getByLabelText("Channel type"), "2")
  await user.click(screen.getByRole("button", { name: "Search" }))

  expect(await screen.findByText("c-101")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Inspect message 101" }))

  const detail = await screen.findByRole("dialog")
  expect(within(detail).getByText("Message 101")).toBeInTheDocument()
  expect(within(detail).getByText("hello")).toBeInTheDocument()

  await user.click(within(detail).getByRole("button", { name: "Base64" }))
  const updatedDetail = await screen.findByRole("dialog")
  expect(within(updatedDetail).getByText("aGVsbG8=")).toBeInTheDocument()

  await user.click(within(updatedDetail).getByRole("button", { name: "Copy payload" }))
  expect(writeTextSpy).toHaveBeenCalledWith("aGVsbG8=")
})

test("deletes message history through a selected sequence after confirmation", async () => {
  getMessagesMock.mockResolvedValueOnce({
    items: [{
      message_id: "101",
      message_seq: 9,
      client_msg_no: "c-101",
      channel_id: "room-1",
      channel_type: 2,
      from_uid: "u1",
      timestamp: 1713859200,
      payload: "aGVsbG8=",
    }],
    has_more: false,
  })
  advanceMessageRetentionMock.mockResolvedValueOnce({
    channel_id: "room-1",
    channel_type: 2,
    requested_through_seq: 9,
    advanced_through_seq: 9,
    min_available_seq: 10,
    status: "advanced",
  })
  getMessagesMock.mockResolvedValueOnce({ items: [], has_more: false })

  const user = userEvent.setup()
  renderMessagesPage("/messages?channel_id=room-1&channel_type=2")

  expect(await screen.findByText("c-101")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Delete history through seq 9" }))
  const dialog = await screen.findByRole("dialog")
  await user.click(within(dialog).getByRole("button", { name: "Confirm" }))

  await waitFor(() => {
    expect(advanceMessageRetentionMock).toHaveBeenCalledWith({ channelId: "room-1", channelType: 2, throughSeq: 9 })
  })
  expect(await screen.findByText(/Min available seq: 10/)).toBeInTheDocument()
  expect(getMessagesMock).toHaveBeenCalledTimes(2)
})

test("keeps delete dialog open when message retention is blocked", async () => {
  getMessagesMock.mockResolvedValueOnce({
    items: [{
      message_id: "101",
      message_seq: 9,
      client_msg_no: "c-101",
      channel_id: "room-1",
      channel_type: 2,
      from_uid: "u1",
      timestamp: 1713859200,
      payload: "aGVsbG8=",
    }],
    has_more: false,
  })
  advanceMessageRetentionMock.mockResolvedValueOnce({
    channel_id: "room-1",
    channel_type: 2,
    requested_through_seq: 9,
    advanced_through_seq: 8,
    min_available_seq: 9,
    status: "blocked",
    blocked_reason: "replay_cursor",
  })

  const user = userEvent.setup()
  renderMessagesPage("/messages?channel_id=room-1&channel_type=2")

  expect(await screen.findByText("c-101")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Delete history through seq 9" }))
  const dialog = await screen.findByRole("dialog")
  await user.click(within(dialog).getByRole("button", { name: "Confirm" }))

  expect(await within(dialog).findByText("History retention is blocked: replay_cursor")).toBeInTheDocument()
  expect(getMessagesMock).toHaveBeenCalledTimes(1)
})
