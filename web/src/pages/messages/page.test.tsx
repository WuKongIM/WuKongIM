import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeEach, expect, test, vi } from "vitest"

import { createAnonymousAuthState, useAuthStore } from "@/auth/auth-store"
import { I18nProvider } from "@/i18n/provider"
import { resetLocale } from "@/i18n/locale-store"
import { MessagesPage } from "@/pages/messages/page"

const getMessagesMock = vi.fn()
const writeTextMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getMessages: (...args: unknown[]) => getMessagesMock(...args),
  }
})

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  getMessagesMock.mockReset()
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

function renderMessagesPage() {
  return render(
    <I18nProvider>
      <MessagesPage />
    </I18nProvider>,
  )
}

test("submits a message query and renders rows", async () => {
  getMessagesMock.mockResolvedValueOnce({
    items: [{
      message_id: 101,
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

test("loads the next page with the same filters", async () => {
  getMessagesMock.mockResolvedValueOnce({
    items: [{
      message_id: 101,
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
      message_id: 102,
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

  expect(getMessagesMock).toHaveBeenNthCalledWith(2, {
    channelId: "room-1",
    channelType: 2,
    limit: 50,
    clientMsgNo: "dup-1",
    cursor: "cursor-2",
  })
  expect(await screen.findByText("c-102")).toBeInTheDocument()
})

test("opens message detail, toggles payload format, and copies the visible payload", async () => {
  getMessagesMock.mockResolvedValueOnce({
    items: [{
      message_id: 101,
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
