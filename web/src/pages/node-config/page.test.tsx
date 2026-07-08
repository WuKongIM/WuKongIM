import { render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { MemoryRouter } from "react-router-dom"
import { beforeEach, expect, test, vi } from "vitest"

import { I18nProvider } from "@/i18n/provider"
import { resetLocale } from "@/i18n/locale-store"
import { ManagerApiError } from "@/lib/manager-api"
import type {
  ManagerNode,
  ManagerNodeConfigResponse,
  ManagerNodesResponse,
} from "@/lib/manager-api.types"
import { NodeConfigPage } from "@/pages/node-config/page"

const getNodesMock = vi.fn()
const getNodeConfigMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getNodes: (...args: unknown[]) => getNodesMock(...args),
    getNodeConfig: (...args: unknown[]) => getNodeConfigMock(...args),
  }
})

const nodeOne = nodeFixture(1, "node-1", true)
const nodeTwo = nodeFixture(2, "node-2", false)

const nodesResponse: ManagerNodesResponse = {
  generated_at: "2026-07-08T10:00:00Z",
  controller_leader_id: 1,
  total: 2,
  items: [nodeTwo, nodeOne],
}

const configByNode: Record<number, ManagerNodeConfigResponse> = {
  1: configFixture(1),
  2: {
    ...configFixture(2),
    groups: [{
      id: "node",
      title: "Node",
      items: [{
        key: "WK_CLUSTER_NODE_ID",
        label: "Node ID",
        value: "2",
        sensitive: false,
        redacted: false,
      }],
    }],
  },
}

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  getNodesMock.mockReset()
  getNodeConfigMock.mockReset()
  getNodesMock.mockResolvedValue(nodesResponse)
  getNodeConfigMock.mockImplementation((nodeId: number) => Promise.resolve(configByNode[nodeId]))
})

test("defaults to the local node and renders grouped effective config", async () => {
  renderNodeConfigPage()

  expect(await screen.findByRole("heading", { name: "Node Config" })).toBeInTheDocument()
  const nodeRail = screen.getByTestId("node-config-node-rail")
  expect(await within(nodeRail).findByText("node-1 · local")).toBeInTheDocument()
  expect(await screen.findByText("WK_CLUSTER_HASH_SLOT_COUNT")).toBeInTheDocument()
  expect(screen.getByText("effective_startup_config")).toBeInTheDocument()
  expect(screen.getByText("restart required")).toBeInTheDocument()
  expect(getNodeConfigMock).toHaveBeenCalledWith(1)
})

test("honors node_id query params and selects the matching node", async () => {
  renderNodeConfigPage("/cluster/node-config?node_id=2")

  const selectedNode = await screen.findByRole("button", { name: /node-2/i })
  await waitFor(() => expect(selectedNode).toHaveAttribute("aria-current", "true"))
  expect(await screen.findByText("WK_CLUSTER_NODE_ID")).toBeInTheDocument()
  expect(getNodeConfigMock).toHaveBeenCalledWith(2)
})

test("filters config rows by search and group tab", async () => {
  const user = userEvent.setup()
  renderNodeConfigPage()

  await screen.findByText("WK_CLUSTER_HASH_SLOT_COUNT")
  await user.type(screen.getByLabelText("Search config"), "jwt")

  expect(screen.getByText("WK_MANAGER_JWT_SECRET")).toBeInTheDocument()
  expect(screen.queryByText("WK_CLUSTER_HASH_SLOT_COUNT")).not.toBeInTheDocument()

  await user.click(screen.getByRole("tab", { name: "Cluster" }))
  expect(screen.getByText("No config values match this search.")).toBeInTheDocument()
})

test("copies the current filtered result with redacted values", async () => {
  const user = userEvent.setup()
  const writeText = vi.fn().mockResolvedValue(undefined)
  Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } })
  renderNodeConfigPage()

  await screen.findByText("WK_CLUSTER_HASH_SLOT_COUNT")
  await user.type(screen.getByLabelText("Search config"), "jwt")
  await user.click(screen.getByRole("button", { name: "Copy filtered result" }))

  await waitFor(() => expect(writeText).toHaveBeenCalledTimes(1))
  const copied = writeText.mock.calls[0][0] as string
  expect(copied).toContain("WK_MANAGER_JWT_SECRET")
  expect(copied).toContain("******")
  expect(copied).not.toContain("WK_CLUSTER_HASH_SLOT_COUNT")
  expect(await screen.findByText("Copied")).toBeInTheDocument()
})

test("keeps node rail visible when selected node config fails", async () => {
  getNodeConfigMock.mockRejectedValueOnce(
    new ManagerApiError(503, "service_unavailable", "node config unavailable"),
  )

  renderNodeConfigPage()

  const nodeRail = screen.getByTestId("node-config-node-rail")
  expect(await within(nodeRail).findByText("node-1 · local")).toBeInTheDocument()
  expect(within(nodeRail).getByText("node-2")).toBeInTheDocument()
  expect(await screen.findByText(/currently unavailable/i)).toBeInTheDocument()
})

function renderNodeConfigPage(path = "/cluster/node-config") {
  return render(
    <I18nProvider>
      <MemoryRouter initialEntries={[path]}>
        <NodeConfigPage />
      </MemoryRouter>
    </I18nProvider>,
  )
}

function nodeFixture(nodeId: number, name: string, isLocal: boolean): ManagerNode {
  return {
    node_id: nodeId,
    name,
    addr: `10.0.0.${nodeId}:11110`,
    status: "alive",
    last_heartbeat_at: "2026-07-08T10:00:00Z",
    is_local: isLocal,
    capacity_weight: 1,
    membership: { role: nodeId === 1 ? "data" : "replica", join_state: "active", schedulable: true },
    health: { status: "alive", last_heartbeat_at: "2026-07-08T10:00:00Z" },
    controller: { role: nodeId === 1 ? "leader" : "follower", voter: true, leader_id: 1 },
    slot_stats: { count: nodeId === 1 ? 86 : 85, leader_count: nodeId === 1 ? 44 : 42 },
    slots: {
      replica_count: 86,
      leader_count: nodeId === 1 ? 44 : 42,
      follower_count: nodeId === 1 ? 42 : 43,
      quorum_lost_count: 0,
      unreported_count: 0,
    },
  }
}

function configFixture(nodeId: number): ManagerNodeConfigResponse {
  return {
    generated_at: "2026-07-08T10:01:00Z",
    node_id: nodeId,
    source: "effective_startup_config",
    requires_restart: true,
    groups: [
      {
        id: "cluster",
        title: "Cluster",
        items: [{
          key: "WK_CLUSTER_HASH_SLOT_COUNT",
          label: "Hash slot count",
          value: "256",
          sensitive: false,
          redacted: false,
        }],
      },
      {
        id: "manager",
        title: "Manager",
        items: [{
          key: "WK_MANAGER_JWT_SECRET",
          label: "JWT secret",
          value: "******",
          sensitive: true,
          redacted: true,
        }],
      },
    ],
  }
}
