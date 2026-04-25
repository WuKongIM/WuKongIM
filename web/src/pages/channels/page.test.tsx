import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeEach, expect, test, vi } from "vitest"

import { createAnonymousAuthState, useAuthStore } from "@/auth/auth-store"
import { I18nProvider } from "@/i18n/provider"
import { resetLocale } from "@/i18n/locale-store"
import { ManagerApiError } from "@/lib/manager-api"
import { ChannelsPage } from "@/pages/channels/page"

const getChannelRuntimeMetaMock = vi.fn()
const getChannelRuntimeMetaDetailMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getChannelRuntimeMeta: (...args: unknown[]) => getChannelRuntimeMetaMock(...args),
    getChannelRuntimeMetaDetail: (...args: unknown[]) => getChannelRuntimeMetaDetailMock(...args),
  }
})

const channelRow = {
  channel_id: "alpha",
  channel_type: 1,
  slot_id: 9,
  channel_epoch: 11,
  leader_epoch: 5,
  leader: 2,
  replicas: [1, 2, 3],
  isr: [1, 2],
  min_isr: 2,
  status: "active",
}

const secondChannelRow = {
  ...channelRow,
  channel_id: "beta",
  slot_id: 11,
}

const channelDetail = {
  ...channelRow,
  hash_slot: 3,
  features: 7,
  lease_until_ms: 1713859200000,
}

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  getChannelRuntimeMetaMock.mockReset()
  getChannelRuntimeMetaDetailMock.mockReset()
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

function renderChannelsPage() {
  return render(
    <I18nProvider>
      <ChannelsPage />
    </I18nProvider>,
  )
}

test("renders channel runtime rows and opens detail from manager APIs", async () => {
  getChannelRuntimeMetaMock.mockResolvedValueOnce({
    items: [channelRow],
    has_more: false,
  })
  getChannelRuntimeMetaDetailMock.mockResolvedValueOnce(channelDetail)

  const user = userEvent.setup()
  renderChannelsPage()

  expect(await screen.findByText("alpha")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Inspect channel alpha" }))

  expect(await screen.findByText("Hash slot")).toBeInTheDocument()
  expect(getChannelRuntimeMetaDetailMock).toHaveBeenCalledWith(1, "alpha")
})

test("loads the next channel runtime page when more data is available", async () => {
  getChannelRuntimeMetaMock.mockResolvedValueOnce({
    items: [channelRow],
    has_more: true,
    next_cursor: "cursor-2",
  })
  getChannelRuntimeMetaMock.mockResolvedValueOnce({
    items: [secondChannelRow],
    has_more: false,
  })

  const user = userEvent.setup()
  renderChannelsPage()

  expect(await screen.findByText("alpha")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Load more" }))

  expect(await screen.findByText("beta")).toBeInTheDocument()
  expect(getChannelRuntimeMetaMock).toHaveBeenNthCalledWith(2, { cursor: "cursor-2" })
})

test("renders unavailable state when channel runtime data cannot be loaded", async () => {
  getChannelRuntimeMetaMock.mockRejectedValueOnce(
    new ManagerApiError(503, "service_unavailable", "slot leader authoritative read unavailable"),
  )

  renderChannelsPage()

  expect(await screen.findByText(/currently unavailable/i)).toBeInTheDocument()
})
