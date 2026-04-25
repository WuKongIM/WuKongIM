import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeEach, expect, test, vi } from "vitest"

import { createAnonymousAuthState, useAuthStore } from "@/auth/auth-store"
import { I18nProvider } from "@/i18n/provider"
import { resetLocale } from "@/i18n/locale-store"
import { ManagerApiError } from "@/lib/manager-api"
import { NodesPage } from "@/pages/nodes/page"

const getNodesMock = vi.fn()
const getNodeMock = vi.fn()
const markNodeDrainingMock = vi.fn()
const resumeNodeMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getNodes: (...args: unknown[]) => getNodesMock(...args),
    getNode: (...args: unknown[]) => getNodeMock(...args),
    markNodeDraining: (...args: unknown[]) => markNodeDrainingMock(...args),
    resumeNode: (...args: unknown[]) => resumeNodeMock(...args),
  }
})

const nodeRow = {
  node_id: 1,
  addr: "127.0.0.1:7000",
  status: "alive",
  last_heartbeat_at: "2026-04-23T08:00:00Z",
  is_local: true,
  capacity_weight: 1,
  controller: { role: "leader" },
  slot_stats: { count: 3, leader_count: 2 },
}

const drainingNodeRow = {
  ...nodeRow,
  status: "draining",
}

const nodeDetail = {
  ...nodeRow,
  slots: {
    hosted_ids: [1, 2, 3],
    leader_ids: [1, 2],
  },
}

const drainingNodeDetail = {
  ...drainingNodeRow,
  slots: {
    hosted_ids: [1, 2, 3],
    leader_ids: [],
  },
}

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  getNodesMock.mockReset()
  getNodeMock.mockReset()
  markNodeDrainingMock.mockReset()
  resumeNodeMock.mockReset()
  useAuthStore.setState({
    ...createAnonymousAuthState(),
    isHydrated: true,
    status: "authenticated",
    username: "admin",
    tokenType: "Bearer",
    accessToken: "token-1",
    expiresAt: "2099-04-22T12:00:00Z",
    permissions: [{ resource: "cluster.node", actions: ["r", "w"] }],
  })
})

function renderNodesPage() {
  return render(
    <I18nProvider>
      <NodesPage />
    </I18nProvider>,
  )
}

test("opens node detail and refreshes after draining", async () => {
  getNodesMock.mockResolvedValueOnce({ total: 1, items: [nodeRow] })
  getNodeMock.mockResolvedValueOnce(nodeDetail)
  markNodeDrainingMock.mockResolvedValueOnce(drainingNodeDetail)
  getNodesMock.mockResolvedValueOnce({ total: 1, items: [drainingNodeRow] })
  getNodeMock.mockResolvedValueOnce(drainingNodeDetail)

  const user = userEvent.setup()
  renderNodesPage()

  expect(await screen.findByText("127.0.0.1:7000")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Inspect node 1" }))

  expect(await screen.findByText("Hosted IDs")).toBeInTheDocument()

  await user.click(screen.getByRole("button", { name: "Drain node" }))
  await user.click(screen.getByRole("button", { name: "Confirm" }))

  expect(markNodeDrainingMock).toHaveBeenCalledWith(1)
  expect(getNodesMock).toHaveBeenCalledTimes(2)
  expect(getNodeMock).toHaveBeenCalledTimes(2)
  expect(await screen.findAllByText("draining")).not.toHaveLength(0)
})

test("refreshes the open detail sheet after resuming a node", async () => {
  getNodesMock.mockResolvedValueOnce({ total: 1, items: [drainingNodeRow] })
  getNodeMock.mockResolvedValueOnce(drainingNodeDetail)
  resumeNodeMock.mockResolvedValueOnce(nodeDetail)
  getNodesMock.mockResolvedValueOnce({ total: 1, items: [nodeRow] })
  getNodeMock.mockResolvedValueOnce(nodeDetail)

  const user = userEvent.setup()
  renderNodesPage()

  expect(await screen.findByText("127.0.0.1:7000")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Inspect node 1" }))

  expect(await screen.findByText("Leader IDs")).toBeInTheDocument()

  await user.click(screen.getByRole("button", { name: "Resume node" }))
  await user.click(screen.getByRole("button", { name: "Confirm" }))

  expect(resumeNodeMock).toHaveBeenCalledWith(1)
  expect(getNodesMock).toHaveBeenCalledTimes(2)
  expect(getNodeMock).toHaveBeenCalledTimes(2)
  expect(await screen.findAllByText("alive")).not.toHaveLength(0)
})

test("shows a forbidden state when node list access is denied", async () => {
  getNodesMock.mockRejectedValueOnce(new ManagerApiError(403, "forbidden", "forbidden"))

  renderNodesPage()

  expect(await screen.findByText(/permission/i)).toBeInTheDocument()
})

test("shows an unavailable state when the manager node list is unavailable", async () => {
  getNodesMock.mockRejectedValueOnce(
    new ManagerApiError(503, "service_unavailable", "controller leader unavailable"),
  )

  renderNodesPage()

  expect(await screen.findByText(/currently unavailable/i)).toBeInTheDocument()
})
