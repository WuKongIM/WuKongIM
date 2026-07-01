import { act, render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { MemoryRouter } from "react-router-dom"
import { beforeEach, expect, test, vi } from "vitest"

import { createAnonymousAuthState, useAuthStore } from "@/auth/auth-store"
import { I18nProvider } from "@/i18n/provider"
import { resetLocale } from "@/i18n/locale-store"
import { ManagerApiError } from "@/lib/manager-api"
import type { ManagerNode } from "@/lib/manager-api.types"
import { DynamicNodeLifecycleSheet } from "@/pages/nodes/dynamic-node-lifecycle"

const joinNodeMock = vi.fn()
const activateNodeMock = vi.fn()
const planNodeOnboardingMock = vi.fn()
const startNodeOnboardingMock = vi.fn()
const getNodeOnboardingStatusMock = vi.fn()
const advanceNodeOnboardingMock = vi.fn()
const planNodeScaleInMock = vi.fn()
const startNodeScaleInMock = vi.fn()
const setNodeScaleInDrainMock = vi.fn()
const getNodeScaleInStatusMock = vi.fn()
const advanceNodeScaleInMock = vi.fn()
const cancelNodeScaleInMock = vi.fn()
const removeNodeAfterScaleInMock = vi.fn()
const getDynamicNodeDiagnosticsMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    joinNode: (...args: unknown[]) => joinNodeMock(...args),
    activateNode: (...args: unknown[]) => activateNodeMock(...args),
    planNodeOnboarding: (...args: unknown[]) => planNodeOnboardingMock(...args),
    startNodeOnboarding: (...args: unknown[]) => startNodeOnboardingMock(...args),
    getNodeOnboardingStatus: (...args: unknown[]) => getNodeOnboardingStatusMock(...args),
    advanceNodeOnboarding: (...args: unknown[]) => advanceNodeOnboardingMock(...args),
    planNodeScaleIn: (...args: unknown[]) => planNodeScaleInMock(...args),
    startNodeScaleIn: (...args: unknown[]) => startNodeScaleInMock(...args),
    setNodeScaleInDrain: (...args: unknown[]) => setNodeScaleInDrainMock(...args),
    getNodeScaleInStatus: (...args: unknown[]) => getNodeScaleInStatusMock(...args),
    advanceNodeScaleIn: (...args: unknown[]) => advanceNodeScaleInMock(...args),
    cancelNodeScaleIn: (...args: unknown[]) => cancelNodeScaleInMock(...args),
    removeNodeAfterScaleIn: (...args: unknown[]) => removeNodeAfterScaleInMock(...args),
    getDynamicNodeDiagnostics: (...args: unknown[]) => getDynamicNodeDiagnosticsMock(...args),
  }
})

const activeNode: ManagerNode = {
  node_id: 4,
  name: "node-4",
  addr: "127.0.0.1:7004",
  status: "alive",
  last_heartbeat_at: "2026-07-01T08:00:00Z",
  is_local: false,
  capacity_weight: 1,
  membership: { role: "data", join_state: "active", schedulable: true },
  health: { status: "alive", last_heartbeat_at: "2026-07-01T08:00:00Z", freshness: "fresh", runtime_ready: true },
  controller: { role: "follower", voter: false, leader_id: 1 },
  slot_stats: { count: 0, leader_count: 0 },
  slots: { replica_count: 0, leader_count: 0, follower_count: 0, quorum_lost_count: 0, unreported_count: 0 },
  runtime: {
    node_id: 4,
    active_online: 0,
    closing_online: 0,
    total_online: 0,
    gateway_sessions: 0,
    sessions_by_listener: {},
    accepting_new_sessions: true,
    draining: false,
    unknown: false,
  },
  actions: { can_drain: false, can_resume: false, can_scale_in: true, can_onboard: true },
}

const joiningNode: ManagerNode = {
  ...activeNode,
  membership: { role: "data", join_state: "joining", schedulable: false },
  actions: { can_drain: false, can_resume: false, can_scale_in: false, can_onboard: false },
}

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  joinNodeMock.mockReset()
  activateNodeMock.mockReset()
  planNodeOnboardingMock.mockReset()
  startNodeOnboardingMock.mockReset()
  getNodeOnboardingStatusMock.mockReset()
  advanceNodeOnboardingMock.mockReset()
  planNodeScaleInMock.mockReset()
  startNodeScaleInMock.mockReset()
  setNodeScaleInDrainMock.mockReset()
  getNodeScaleInStatusMock.mockReset()
  advanceNodeScaleInMock.mockReset()
  cancelNodeScaleInMock.mockReset()
  removeNodeAfterScaleInMock.mockReset()
  getDynamicNodeDiagnosticsMock.mockReset()
  useAuthStore.setState({
    ...createAnonymousAuthState(),
    isHydrated: true,
    status: "authenticated",
    username: "admin",
    tokenType: "Bearer",
    accessToken: "token-1",
    expiresAt: "2099-04-22T12:00:00Z",
    permissions: [
      { resource: "cluster.node", actions: ["r", "w"] },
      { resource: "cluster.slot", actions: ["r", "w"] },
    ],
  })
})

function renderLifecycle(mode: "join" | "node", node: ManagerNode | null = null, onCompleted = vi.fn()) {
  return render(
    <I18nProvider>
      <MemoryRouter>
        <DynamicNodeLifecycleSheet
          mode={mode}
          node={node}
          onCompleted={onCompleted}
          onOpenChange={() => undefined}
          open
        />
      </MemoryRouter>
    </I18nProvider>,
  )
}

test("joins a new data node and reports joining state", async () => {
  const onCompleted = vi.fn()
  joinNodeMock.mockResolvedValueOnce({
    created: true,
    node_id: 4,
    addr: "127.0.0.1:7004",
    join_state: "joining",
    revision: 41,
  })
  const user = userEvent.setup()

  renderLifecycle("join", null, onCompleted)

  await user.type(screen.getByLabelText("Node ID"), "4")
  await user.type(screen.getByLabelText("Address"), "127.0.0.1:7004")
  await user.type(screen.getByLabelText("Name"), "node-4")
  await user.clear(screen.getByLabelText("Capacity weight"))
  await user.type(screen.getByLabelText("Capacity weight"), "1")
  await user.click(screen.getByRole("button", { name: "Join node" }))

  expect(joinNodeMock).toHaveBeenCalledWith({
    nodeId: 4,
    addr: "127.0.0.1:7004",
    name: "node-4",
    capacityWeight: 1,
  })
  expect(await screen.findByText("Join state: joining")).toBeInTheDocument()
  expect(onCompleted).toHaveBeenCalled()
})

test("activates a joining node and preserves readiness conflict details", async () => {
  activateNodeMock.mockRejectedValueOnce(new ManagerApiError(
    409,
    "conflict",
    "runtime_ready=false control_revision=40 required=42",
  ))
  const user = userEvent.setup()

  renderLifecycle("node", joiningNode)

  await user.click(screen.getByRole("button", { name: "Activate node" }))

  expect(activateNodeMock).toHaveBeenCalledWith(4)
  expect(await screen.findByText("runtime_ready=false control_revision=40 required=42")).toBeInTheDocument()
  const dialog = screen.getByRole("dialog", { name: "Node lifecycle" })
  expect(within(dialog).getByRole("button", { name: "Diagnostics" })).toBeInTheDocument()
})

test("invalid join input does not call joinNode and shows a validation message", async () => {
  const user = userEvent.setup()

  renderLifecycle("join")

  await user.click(screen.getByRole("button", { name: "Join node" }))

  expect(joinNodeMock).not.toHaveBeenCalled()
  expect(screen.getByRole("alert")).toHaveTextContent("Node ID must be a positive number.")
})

test("resets transient state across close reopen and node lifecycle switch", async () => {
  joinNodeMock.mockResolvedValueOnce({
    created: true,
    node_id: 4,
    addr: "127.0.0.1:7004",
    join_state: "joining",
    revision: 41,
  })
  const user = userEvent.setup()
  const { rerender } = render(
    <I18nProvider>
      <MemoryRouter>
        <DynamicNodeLifecycleSheet
          mode="join"
          node={null}
          onCompleted={() => undefined}
          onOpenChange={() => undefined}
          open
        />
      </MemoryRouter>
    </I18nProvider>,
  )

  await user.type(screen.getByLabelText("Node ID"), "4")
  await user.type(screen.getByLabelText("Address"), "127.0.0.1:7004")
  await user.type(screen.getByLabelText("Name"), "node-4")
  await user.click(screen.getByRole("button", { name: "Join node" }))

  expect(await screen.findByText("Join state: joining")).toBeInTheDocument()

  rerender(
    <I18nProvider>
      <MemoryRouter>
        <DynamicNodeLifecycleSheet
          mode="join"
          node={null}
          onCompleted={() => undefined}
          onOpenChange={() => undefined}
          open={false}
        />
      </MemoryRouter>
    </I18nProvider>,
  )
  rerender(
    <I18nProvider>
      <MemoryRouter>
        <DynamicNodeLifecycleSheet
          mode="join"
          node={null}
          onCompleted={() => undefined}
          onOpenChange={() => undefined}
          open
        />
      </MemoryRouter>
    </I18nProvider>,
  )

  expect(screen.queryByText("Join state: joining")).not.toBeInTheDocument()
  expect(screen.getByLabelText("Node ID")).toHaveValue(null)
  expect(screen.getByLabelText("Address")).toHaveValue("")
  expect(screen.getByLabelText("Name")).toHaveValue("")

  rerender(
    <I18nProvider>
      <MemoryRouter>
        <DynamicNodeLifecycleSheet
          mode="node"
          node={joiningNode}
          onCompleted={() => undefined}
          onOpenChange={() => undefined}
          open
        />
      </MemoryRouter>
    </I18nProvider>,
  )

  const dialog = screen.getByRole("dialog", { name: "Node lifecycle" })
  expect(within(dialog).queryByText("Join state: joining")).not.toBeInTheDocument()
  expect(within(dialog).queryByText("node-4")).not.toBeInTheDocument()
  expect(within(dialog).queryByRole("alert")).not.toBeInTheDocument()
})

test("ignores join completion after close before reopening same lifecycle", async () => {
  let resolveJoin: ((value: {
    created: boolean
    node_id: number
    addr: string
    join_state: string
    revision: number
  }) => void) | null = null
  const joinPromise = new Promise<{
    created: boolean
    node_id: number
    addr: string
    join_state: string
    revision: number
  }>((resolve) => {
    resolveJoin = resolve
  })
  joinNodeMock.mockReturnValueOnce(joinPromise)
  const user = userEvent.setup()
  const { rerender } = render(
    <I18nProvider>
      <MemoryRouter>
        <DynamicNodeLifecycleSheet
          mode="join"
          node={null}
          onCompleted={() => undefined}
          onOpenChange={() => undefined}
          open
        />
      </MemoryRouter>
    </I18nProvider>,
  )

  await user.type(screen.getByLabelText("Node ID"), "4")
  await user.type(screen.getByLabelText("Address"), "127.0.0.1:7004")
  await user.type(screen.getByLabelText("Name"), "node-4")
  await user.click(screen.getByRole("button", { name: "Join node" }))

  rerender(
    <I18nProvider>
      <MemoryRouter>
        <DynamicNodeLifecycleSheet
          mode="join"
          node={null}
          onCompleted={() => undefined}
          onOpenChange={() => undefined}
          open={false}
        />
      </MemoryRouter>
    </I18nProvider>,
  )

  await act(async () => {
    resolveJoin?.({
      created: true,
      node_id: 4,
      addr: "127.0.0.1:7004",
      join_state: "joining",
      revision: 41,
    })
    await joinPromise
  })

  rerender(
    <I18nProvider>
      <MemoryRouter>
        <DynamicNodeLifecycleSheet
          mode="join"
          node={null}
          onCompleted={() => undefined}
          onOpenChange={() => undefined}
          open
        />
      </MemoryRouter>
    </I18nProvider>,
  )

  expect(screen.queryByText("Join state: joining")).not.toBeInTheDocument()
  expect(screen.queryByRole("alert")).not.toBeInTheDocument()
  expect(screen.getByLabelText("Node ID")).toHaveValue(null)
  expect(screen.getByLabelText("Address")).toHaveValue("")
  expect(screen.getByLabelText("Name")).toHaveValue("")
})
