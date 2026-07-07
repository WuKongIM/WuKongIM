import { render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeEach, expect, test, vi } from "vitest"

import { createAnonymousAuthState, useAuthStore } from "@/auth/auth-store"
import { resetLocale } from "@/i18n/locale-store"
import { I18nProvider } from "@/i18n/provider"
import { ManagerApiError } from "@/lib/manager-api"
import { ChannelsBizPage } from "@/pages/channels-biz/page"

const getBusinessChannelsMock = vi.fn()
const getBusinessChannelMock = vi.fn()
const upsertBusinessChannelMock = vi.fn()
const getBusinessChannelMembersMock = vi.fn()
const addBusinessChannelMembersMock = vi.fn()
const removeBusinessChannelMembersMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getBusinessChannels: (...args: unknown[]) => getBusinessChannelsMock(...args),
    getBusinessChannel: (...args: unknown[]) => getBusinessChannelMock(...args),
    upsertBusinessChannel: (...args: unknown[]) => upsertBusinessChannelMock(...args),
    getBusinessChannelMembers: (...args: unknown[]) => getBusinessChannelMembersMock(...args),
    addBusinessChannelMembers: (...args: unknown[]) => addBusinessChannelMembersMock(...args),
    removeBusinessChannelMembers: (...args: unknown[]) => removeBusinessChannelMembersMock(...args),
  }
})

const groupChannel = {
  channel_id: "g1",
  channel_type: 2,
  slot_id: 4,
  hash_slot: 12,
  ban: false,
  disband: false,
  send_ban: true,
  subscriber_mutation_version: 7,
}

const groupDetail = {
  ...groupChannel,
  has_subscribers: true,
  has_allowlist: false,
  has_denylist: true,
}

const personChannel = {
  ...groupChannel,
  channel_id: "p1",
  channel_type: 1,
  send_ban: false,
}

const personDetail = {
  ...personChannel,
  has_subscribers: true,
  has_allowlist: false,
  has_denylist: false,
}

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  getBusinessChannelsMock.mockReset()
  getBusinessChannelMock.mockReset()
  upsertBusinessChannelMock.mockReset()
  getBusinessChannelMembersMock.mockReset()
  addBusinessChannelMembersMock.mockReset()
  removeBusinessChannelMembersMock.mockReset()
  useAuthStore.setState({
    ...createAnonymousAuthState(),
    isHydrated: true,
    status: "authenticated",
    username: "admin",
    tokenType: "Bearer",
    accessToken: "token-1",
    expiresAt: "2099-04-22T12:00:00Z",
    permissions: [{ resource: "cluster.channel", actions: ["r", "w"] }],
  })
})

function renderChannelsBizPage() {
  return render(
    <I18nProvider>
      <ChannelsBizPage />
    </I18nProvider>,
  )
}

test("renders the first business channel page", async () => {
  getBusinessChannelsMock.mockResolvedValueOnce({ items: [groupChannel], has_more: false })

  renderChannelsBizPage()

  expect(await screen.findByText("g1")).toBeInTheDocument()
  expect(screen.getByText("send banned")).toBeInTheDocument()
  expect(getBusinessChannelsMock).toHaveBeenCalledWith({ limit: 50 })
})

test("uses editorial business channel inventory and member surfaces", async () => {
  getBusinessChannelsMock.mockResolvedValue({ items: [groupChannel], has_more: false })
  getBusinessChannelMock.mockResolvedValue(groupDetail)
  getBusinessChannelMembersMock.mockResolvedValue({ items: [{ uid: "u1" }], has_more: false })

  const user = userEvent.setup()
  renderChannelsBizPage()

  const table = await screen.findByRole("table", { name: "Business channels" })
  const inventorySurface = table.closest("[data-channels-biz-surface='inventory']")
  expect(inventorySurface).toHaveClass("overflow-x-auto", "rounded-md", "border", "border-border")
  expect(inventorySurface).not.toHaveClass("rounded-xl")

  const toolbar = screen.getByTestId("channels-biz-filter-toolbar")
  expect(toolbar).toHaveClass("border-b", "border-border", "pb-4")
  expect(within(toolbar).getByPlaceholderText("Search channel ID")).toBeInTheDocument()
  expect(within(toolbar).getByLabelText("Channel type")).toBeInTheDocument()

  await user.click(screen.getByRole("button", { name: "Inspect channel g1" }))

  const memberToolbar = await screen.findByTestId("channels-biz-member-toolbar")
  expect(memberToolbar).toHaveClass("rounded-md", "border", "border-border", "bg-muted/30", "p-2")

  const memberTable = await screen.findByRole("table", { name: "Subscribers" })
  const memberSurface = memberTable.closest("[data-channels-biz-surface='members']")
  expect(memberSurface).toHaveClass("overflow-x-auto", "rounded-md", "border", "border-border")
})

test("searches by channel ID, filters by type, and loads more", async () => {
  getBusinessChannelsMock.mockResolvedValueOnce({ items: [], has_more: false })
  getBusinessChannelsMock.mockResolvedValueOnce({
    items: [{ ...groupChannel, channel_id: "alpha-room" }],
    has_more: true,
    next_cursor: "cursor-1",
  })
  getBusinessChannelsMock.mockResolvedValueOnce({
    items: [{ ...groupChannel, channel_id: "alpha-room-2" }],
    has_more: false,
  })

  const user = userEvent.setup()
  renderChannelsBizPage()

  await screen.findByText("No manager data is available for this view yet.")
  await user.type(screen.getByPlaceholderText("Search channel ID"), "alpha")
  await user.selectOptions(screen.getByLabelText("Channel type"), "2")
  await user.click(screen.getByRole("button", { name: "Search" }))

  expect(await screen.findByText("alpha-room")).toBeInTheDocument()
  expect(getBusinessChannelsMock).toHaveBeenLastCalledWith({ keyword: "alpha", type: 2, limit: 50 })

  await user.click(screen.getByRole("button", { name: "Load more" }))
  expect(await screen.findByText("alpha-room-2")).toBeInTheDocument()
  expect(getBusinessChannelsMock).toHaveBeenLastCalledWith({
    keyword: "alpha",
    type: 2,
    limit: 50,
    cursor: "cursor-1",
  })
})

test("opens detail and switches member tabs", async () => {
  getBusinessChannelsMock.mockResolvedValue({ items: [groupChannel], has_more: false })
  getBusinessChannelMock.mockResolvedValue(groupDetail)
  getBusinessChannelMembersMock.mockResolvedValueOnce({ items: [{ uid: "u1" }], has_more: false })
  getBusinessChannelMembersMock.mockResolvedValueOnce({ items: [{ uid: "allow-u1" }], has_more: false })

  const user = userEvent.setup()
  renderChannelsBizPage()

  await user.click(await screen.findByRole("button", { name: "Inspect channel g1" }))

  expect(await screen.findByText("Subscriber mutation version")).toBeInTheDocument()
  expect(await screen.findByText("u1")).toBeInTheDocument()
  expect(getBusinessChannelMock).toHaveBeenCalledWith(2, "g1")
  expect(getBusinessChannelMembersMock).toHaveBeenCalledWith(2, "g1", "subscribers", { limit: 100 })

  await user.click(screen.getByRole("button", { name: "Allowlist" }))
  expect(await screen.findByText("allow-u1")).toBeInTheDocument()
  expect(getBusinessChannelMembersMock).toHaveBeenLastCalledWith(2, "g1", "allowlist", { limit: 100 })
})

test("creates or updates channel metadata and refreshes the list", async () => {
  getBusinessChannelsMock.mockResolvedValue({ items: [groupChannel], has_more: false })
  upsertBusinessChannelMock.mockResolvedValue({
    ...groupDetail,
    channel_id: "new-room",
    ban: true,
  })

  const user = userEvent.setup()
  renderChannelsBizPage()

  await screen.findByText("g1")
  await user.click(screen.getByRole("button", { name: "New channel" }))
  await user.type(screen.getByLabelText("Channel ID"), "new-room")
  await user.selectOptions(screen.getByLabelText("Metadata channel type"), "2")
  await user.click(screen.getByLabelText("Ban channel"))
  await user.click(screen.getByRole("button", { name: "Save channel" }))

  expect(upsertBusinessChannelMock).toHaveBeenCalledWith({
    channelId: "new-room",
    channelType: 2,
    ban: true,
    disband: false,
    sendBan: false,
  })
  await waitFor(() => expect(getBusinessChannelsMock).toHaveBeenCalledTimes(2))
})

test("adds normalized members and removes one member", async () => {
  getBusinessChannelsMock.mockResolvedValue({ items: [groupChannel], has_more: false })
  getBusinessChannelMock.mockResolvedValue(groupDetail)
  getBusinessChannelMembersMock.mockResolvedValue({ items: [{ uid: "u1" }], has_more: false })
  addBusinessChannelMembersMock.mockResolvedValue({
    channel_id: "g1",
    channel_type: 2,
    list: "subscribers",
    changed: true,
  })
  removeBusinessChannelMembersMock.mockResolvedValue({
    channel_id: "g1",
    channel_type: 2,
    list: "subscribers",
    changed: true,
  })

  const user = userEvent.setup()
  renderChannelsBizPage()

  await user.click(await screen.findByRole("button", { name: "Inspect channel g1" }))
  expect(await screen.findByText("u1")).toBeInTheDocument()

  await user.click(screen.getByRole("button", { name: "Add members" }))
  await user.type(screen.getByLabelText("UIDs"), "u2, u3\nu2")
  const dialogs = screen.getAllByRole("dialog")
  await user.click(within(dialogs[dialogs.length - 1]).getByRole("button", { name: "Add members" }))

  expect(addBusinessChannelMembersMock).toHaveBeenCalledWith(2, "g1", "subscribers", { uids: ["u2", "u3"] })

  await user.click(screen.getByRole("button", { name: "Remove member u1" }))
  await user.click(screen.getByRole("button", { name: "Confirm remove" }))

  expect(removeBusinessChannelMembersMock).toHaveBeenCalledWith(2, "g1", "subscribers", { uids: ["u1"] })
})

test("disables ordinary subscriber edits for person channels", async () => {
  getBusinessChannelsMock.mockResolvedValue({ items: [personChannel], has_more: false })
  getBusinessChannelMock.mockResolvedValue(personDetail)
  getBusinessChannelMembersMock.mockResolvedValue({ items: [{ uid: "u1" }], has_more: false })

  const user = userEvent.setup()
  renderChannelsBizPage()

  await user.click(await screen.findByRole("button", { name: "Inspect channel p1" }))

  expect(await screen.findByText("Person channels do not support ordinary subscriber edits.")).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Add members" })).toBeDisabled()
  expect(screen.getByRole("button", { name: "Remove member u1" })).toBeDisabled()
})

test("maps permission and availability errors", async () => {
  getBusinessChannelsMock.mockRejectedValueOnce(new ManagerApiError(403, "forbidden", "forbidden"))
  const { unmount } = renderChannelsBizPage()

  expect(await screen.findByText("You do not have permission to view this manager resource.")).toBeInTheDocument()
  unmount()

  getBusinessChannelsMock.mockRejectedValueOnce(new ManagerApiError(503, "service_unavailable", "unavailable"))
  renderChannelsBizPage()

  expect(await screen.findByText("The manager service is currently unavailable.")).toBeInTheDocument()
})
