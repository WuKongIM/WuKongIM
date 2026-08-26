import { render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeEach, expect, test, vi } from "vitest"
import {
  MemoryRouter,
  Route,
  Routes,
  useLocation,
  useNavigate,
} from "react-router-dom"

import { createAnonymousAuthState, useAuthStore } from "@/auth/auth-store"
import { resetLocale } from "@/i18n/locale-store"
import { I18nProvider } from "@/i18n/provider"
import { ManagerApiError } from "@/lib/manager-api"
import { ChannelsBizPage } from "@/pages/channels-biz/page"

const getBusinessChannelsMock = vi.fn()
const getBusinessChannelMock = vi.fn()
const createBusinessChannelMock = vi.fn()
const updateBusinessChannelMock = vi.fn()
const getBusinessChannelMembersMock = vi.fn()
const addBusinessChannelMembersMock = vi.fn()
const removeBusinessChannelMembersMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getBusinessChannels: (...args: unknown[]) => getBusinessChannelsMock(...args),
    getBusinessChannel: (...args: unknown[]) => getBusinessChannelMock(...args),
    createBusinessChannel: (...args: unknown[]) => createBusinessChannelMock(...args),
    updateBusinessChannel: (...args: unknown[]) => updateBusinessChannelMock(...args),
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
  createBusinessChannelMock.mockReset()
  updateBusinessChannelMock.mockReset()
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

function LocationProbe() {
  const location = useLocation()
  const navigate = useNavigate()
  return (
    <>
      <output data-testid="location-search">{location.search}</output>
      <button onClick={() => navigate(-1)} type="button">Back in history</button>
    </>
  )
}

function renderChannelsBizPage(initialEntry = "/business/channels") {
  return render(
    <MemoryRouter initialEntries={[initialEntry]}>
      <I18nProvider>
        <Routes>
          <Route
            path="/business/channels"
            element={(
              <>
                <ChannelsBizPage />
                <LocationProbe />
              </>
            )}
          />
        </Routes>
      </I18nProvider>
    </MemoryRouter>,
  )
}

test("renders the first business channel page", async () => {
  getBusinessChannelsMock.mockResolvedValueOnce({ items: [groupChannel], has_more: false })

  renderChannelsBizPage()

  expect(await screen.findByText("g1")).toBeInTheDocument()
  expect(screen.getByText("Send banned")).toBeInTheDocument()
  expect(getBusinessChannelsMock).toHaveBeenCalledWith({ limit: 50 })
})

test("uses editorial business channel inventory and member surfaces", async () => {
  getBusinessChannelsMock.mockResolvedValue({ items: [groupChannel], has_more: false })
  getBusinessChannelMock.mockResolvedValue(groupDetail)
  getBusinessChannelMembersMock.mockResolvedValue({ items: [{ uid: "u1" }], has_more: false })

  const user = userEvent.setup()
  renderChannelsBizPage()

  const table = await screen.findByRole("table", { name: "Business channels" })
  expect(table).toHaveClass("block", "md:table")
  expect(within(table).getByText("g1").closest("td")).toHaveAttribute("data-label", "Channel")
  const inventorySurface = table.closest("[data-channels-biz-surface='inventory']")
  expect(inventorySurface).toHaveClass("overflow-x-auto", "rounded-md", "border", "border-border")
  expect(inventorySurface).not.toHaveClass("rounded-xl")

  const toolbar = screen.getByTestId("channels-biz-filter-toolbar")
  expect(toolbar).toHaveClass("border-b", "border-border", "pb-4")
  expect(within(toolbar).getByPlaceholderText("Search channel ID")).toBeInTheDocument()
  expect(within(toolbar).getByLabelText("Channel type")).toBeInTheDocument()

  await user.click(screen.getByRole("button", { name: "View member data for channel g1" }))

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

  await user.click(await screen.findByRole("button", { name: "View details for channel g1" }))

  expect(await screen.findByText("Subscriber mutation version")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Member data" }))
  expect(await screen.findByText("u1")).toBeInTheDocument()
  expect(getBusinessChannelMock).toHaveBeenCalledWith(2, "g1")
  expect(getBusinessChannelMembersMock).toHaveBeenCalledWith(2, "g1", "subscribers", { limit: 100 })

  await user.click(screen.getByRole("button", { name: "Allowlist" }))
  expect(await screen.findByText("allow-u1")).toBeInTheDocument()
  expect(getBusinessChannelMembersMock).toHaveBeenLastCalledWith(2, "g1", "allowlist", { limit: 100 })
})

test("creates or updates channel metadata and refreshes the list", async () => {
  getBusinessChannelsMock.mockResolvedValue({ items: [groupChannel], has_more: false })
  createBusinessChannelMock.mockResolvedValue({
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
  await user.click(screen.getByRole("button", { name: "Create channel" }))

  expect(createBusinessChannelMock).toHaveBeenCalledWith({
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
    requested_count: 2,
    changed_count: 2,
  })
  removeBusinessChannelMembersMock.mockResolvedValue({
    channel_id: "g1",
    channel_type: 2,
    list: "subscribers",
    requested_count: 1,
    changed_count: 1,
  })

  const user = userEvent.setup()
  renderChannelsBizPage()

  await user.click(await screen.findByRole("button", { name: "View member data for channel g1" }))
  expect(await screen.findByText("u1")).toBeInTheDocument()

  await user.click(screen.getByRole("button", { name: "Add members" }))
  await user.type(screen.getByLabelText("User UIDs"), "u2, u3\nu2")
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

  await user.click(await screen.findByRole("button", { name: "View member data for channel p1" }))

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

test("opens member data through URL state and browser back closes the sheet", async () => {
  getBusinessChannelsMock.mockResolvedValue({ items: [groupChannel], has_more: false })
  getBusinessChannelMembersMock.mockResolvedValue({ items: [{ uid: "u1" }], has_more: false })

  const user = userEvent.setup()
  renderChannelsBizPage()

  await user.click(await screen.findByRole("button", { name: "View member data for channel g1" }))
  expect(screen.getByTestId("location-search")).toHaveTextContent(
    "?channel_id=g1&channel_type=2&member_list=subscribers",
  )
  expect(await screen.findByRole("table", { name: "Subscribers" })).toBeInTheDocument()

  await user.click(screen.getByRole("button", { name: "Back in history" }))
  await waitFor(() => {
    expect(screen.queryByRole("table", { name: "Subscribers" })).not.toBeInTheDocument()
  })
})

test("restores a denylist deep link and keeps the member tab order", async () => {
  getBusinessChannelsMock.mockResolvedValue({ items: [groupChannel], has_more: false })
  getBusinessChannelMembersMock.mockResolvedValue({ items: [{ uid: "blocked-u1" }], has_more: false })

  renderChannelsBizPage("/business/channels?channel_id=g1&channel_type=2&member_list=denylist")

  expect(await screen.findByText("blocked-u1")).toBeInTheDocument()
  expect(getBusinessChannelMembersMock).toHaveBeenCalledWith(2, "g1", "denylist", { limit: 100 })
  const toolbar = screen.getByTestId("channels-biz-member-toolbar")
  expect(
    within(toolbar)
      .getAllByRole("button")
      .map((button) => button.textContent)
      .filter((label) => ["Subscribers", "Denylist", "Allowlist"].includes(label ?? "")),
  ).toEqual(["Subscribers", "Denylist", "Allowlist"])
})

test("preserves an exact legacy channel ID from the deep link", async () => {
  getBusinessChannelsMock.mockResolvedValue({ items: [], has_more: false })
  getBusinessChannelMembersMock.mockResolvedValue({ items: [{ uid: "u1" }], has_more: false })

  renderChannelsBizPage("/business/channels?channel_id=%20legacy%20&channel_type=2&member_list=allowlist")

  expect(await screen.findByText("u1")).toBeInTheDocument()
  expect(getBusinessChannelMembersMock).toHaveBeenCalledWith(2, " legacy ", "allowlist", { limit: 100 })
})

test("performs exact UID hit and miss searches and clear returns to page one", async () => {
  getBusinessChannelsMock.mockResolvedValue({ items: [groupChannel], has_more: false })
  getBusinessChannelMembersMock
    .mockResolvedValueOnce({ items: [{ uid: "u1" }], has_more: false })
    .mockResolvedValueOnce({ items: [{ uid: "exact-u" }], has_more: false })
    .mockResolvedValueOnce({ items: [], has_more: false })
    .mockResolvedValueOnce({ items: [{ uid: "u1" }], has_more: false })

  const user = userEvent.setup()
  renderChannelsBizPage("/business/channels?channel_id=g1&channel_type=2&member_list=subscribers")

  await screen.findByText("u1")
  const search = screen.getByLabelText("Exact UID")
  const memberSearchForm = search.closest("form")
  expect(memberSearchForm).not.toBeNull()
  await user.type(search, "exact-u")
  await user.click(within(memberSearchForm as HTMLFormElement).getByRole("button", { name: "Search" }))
  expect(await screen.findByText("UID exact-u is in this list.")).toBeInTheDocument()
  expect(getBusinessChannelMembersMock).toHaveBeenLastCalledWith(2, "g1", "subscribers", {
    limit: 100,
    uid: "exact-u",
  })

  await user.clear(search)
  await user.type(search, "missing-u")
  await user.click(within(memberSearchForm as HTMLFormElement).getByRole("button", { name: "Search" }))
  expect(await screen.findByText("UID missing-u is not in this list.")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Add missing-u" }))
  const addDialog = screen.getAllByRole("dialog").at(-1)
  expect(addDialog).toBeDefined()
  expect(within(addDialog as HTMLElement).getByLabelText("User UIDs")).toHaveValue("missing-u")
  await user.click(within(addDialog as HTMLElement).getByRole("button", { name: "Cancel" }))

  await user.click(screen.getByRole("button", { name: "Clear search" }))
  expect(await screen.findByText("u1")).toBeInTheDocument()
  expect(getBusinessChannelMembersMock).toHaveBeenLastCalledWith(2, "g1", "subscribers", {
    limit: 100,
  })
})

test("manually refreshes the active authoritative member page", async () => {
  getBusinessChannelsMock.mockResolvedValue({ items: [groupChannel], has_more: false })
  getBusinessChannelMembersMock
    .mockResolvedValueOnce({ items: [{ uid: "before-refresh" }], has_more: false })
    .mockResolvedValueOnce({ items: [{ uid: "after-refresh" }], has_more: false })

  const user = userEvent.setup()
  renderChannelsBizPage("/business/channels?channel_id=g1&channel_type=2&member_list=subscribers")

  await screen.findByText("before-refresh")
  await user.click(screen.getByRole("button", { name: "Refresh member data" }))
  expect(await screen.findByText("after-refresh")).toBeInTheDocument()
  expect(screen.queryByText("before-refresh")).not.toBeInTheDocument()
  expect(getBusinessChannelMembersMock).toHaveBeenLastCalledWith(2, "g1", "subscribers", {
    limit: 100,
  })
})

test("retries an exact-mode refresh without losing the exact UID query", async () => {
  getBusinessChannelsMock.mockResolvedValue({ items: [groupChannel], has_more: false })
  getBusinessChannelMembersMock
    .mockResolvedValueOnce({ items: [{ uid: "u1" }], has_more: false })
    .mockResolvedValueOnce({ items: [{ uid: "exact-u" }], has_more: false })
    .mockRejectedValueOnce(new Error("exact refresh failed"))
    .mockResolvedValueOnce({ items: [{ uid: "exact-u" }], has_more: false })

  const user = userEvent.setup()
  renderChannelsBizPage("/business/channels?channel_id=g1&channel_type=2&member_list=subscribers")

  await screen.findByText("u1")
  const search = screen.getByLabelText("Exact UID")
  const memberSearchForm = search.closest("form")
  await user.type(search, "exact-u")
  await user.click(within(memberSearchForm as HTMLFormElement).getByRole("button", { name: "Search" }))
  await screen.findByText("UID exact-u is in this list.")

  await user.click(screen.getByRole("button", { name: "Refresh member data" }))
  await screen.findByText("exact refresh failed")
  await user.click(screen.getByRole("button", { name: "Retry" }))

  await waitFor(() => {
    expect(getBusinessChannelMembersMock).toHaveBeenLastCalledWith(2, "g1", "subscribers", {
      limit: 100,
      uid: "exact-u",
    })
  })
})

test("uses current-page cursor navigation and preserves the page after a next read failure", async () => {
  getBusinessChannelsMock.mockResolvedValue({ items: [groupChannel], has_more: false })
  getBusinessChannelMembersMock
    .mockResolvedValueOnce({ items: [{ uid: "page-1" }], has_more: true, next_cursor: "cursor-2" })
    .mockResolvedValueOnce({ items: [{ uid: "page-2" }], has_more: true, next_cursor: "cursor-3" })
    .mockRejectedValueOnce(new Error("next failed"))
    .mockResolvedValueOnce({ items: [{ uid: "page-1" }], has_more: true, next_cursor: "cursor-2" })

  const user = userEvent.setup()
  renderChannelsBizPage("/business/channels?channel_id=g1&channel_type=2&member_list=subscribers")

  await screen.findByText("page-1")
  await user.click(screen.getByRole("button", { name: "Next" }))
  expect(await screen.findByText("page-2")).toBeInTheDocument()
  expect(screen.queryByText("page-1")).not.toBeInTheDocument()

  await user.click(screen.getByRole("button", { name: "Next" }))
  expect(await screen.findByText("next failed")).toBeInTheDocument()
  expect(screen.getByText("page-2")).toBeInTheDocument()

  await user.click(screen.getByRole("button", { name: "Previous" }))
  expect(await screen.findByText("page-1")).toBeInTheDocument()
  expect(getBusinessChannelMembersMock).toHaveBeenLastCalledWith(2, "g1", "subscribers", {
    limit: 100,
  })
})

test("hides every channel write control for read-only permissions", async () => {
  useAuthStore.setState({
    permissions: [{ resource: "cluster.channel", actions: ["r"] }],
  })
  getBusinessChannelsMock.mockResolvedValue({ items: [groupChannel], has_more: false })
  getBusinessChannelMembersMock.mockResolvedValue({ items: [{ uid: "u1" }], has_more: false })

  renderChannelsBizPage("/business/channels?channel_id=g1&channel_type=2&member_list=subscribers")

  expect(await screen.findByText("u1")).toBeInTheDocument()
  expect(screen.queryByRole("button", { name: "New channel" })).not.toBeInTheDocument()
  expect(screen.queryByRole("button", { name: "Add members" })).not.toBeInTheDocument()
  expect(screen.queryByRole("button", { name: "Remove members" })).not.toBeInTheDocument()
  expect(screen.queryByRole("button", { name: "Remove member u1" })).not.toBeInTheDocument()
})

test("rejects an invalid UID batch before writing", async () => {
  getBusinessChannelsMock.mockResolvedValue({ items: [groupChannel], has_more: false })
  getBusinessChannelMembersMock.mockResolvedValue({ items: [{ uid: "u1" }], has_more: false })

  const user = userEvent.setup()
  renderChannelsBizPage("/business/channels?channel_id=g1&channel_type=2&member_list=subscribers")

  await screen.findByText("u1")
  await user.click(screen.getByRole("button", { name: "Add members" }))
  await user.type(screen.getByLabelText("User UIDs"), "valid-u, invalid uid")
  const dialog = screen.getAllByRole("dialog").at(-1)
  expect(dialog).toBeDefined()
  await user.click(within(dialog as HTMLElement).getByRole("button", { name: "Add members" }))

  expect(await screen.findByText(/These UIDs are invalid: invalid uid/)).toBeInTheDocument()
  expect(addBusinessChannelMembersMock).not.toHaveBeenCalled()
})

test("bulk removal requires a channel-list-count-preview confirmation", async () => {
  getBusinessChannelsMock.mockResolvedValue({ items: [groupChannel], has_more: false })
  getBusinessChannelMembersMock.mockResolvedValue({ items: [{ uid: "u1" }], has_more: false })
  removeBusinessChannelMembersMock.mockResolvedValue({
    channel_id: "g1",
    channel_type: 2,
    list: "subscribers",
    requested_count: 2,
    changed_count: 1,
  })

  const user = userEvent.setup()
  renderChannelsBizPage("/business/channels?channel_id=g1&channel_type=2&member_list=subscribers")

  await screen.findByText("u1")
  await user.click(screen.getByRole("button", { name: "Remove members" }))
  await user.type(screen.getByLabelText("UIDs to remove"), "u1;u2")
  await user.click(screen.getByRole("button", { name: "Review removal" }))

  expect(screen.getByText(/Channel: g1 \(2\).*List: Subscribers.*Remove 2 UID\(s\).*u1, u2/)).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Confirm remove" }))
  expect(removeBusinessChannelMembersMock).toHaveBeenCalledWith(2, "g1", "subscribers", {
    uids: ["u1", "u2"],
  })
  expect(await screen.findByText("Processed 2; changed 1.")).toBeInTheDocument()
})
