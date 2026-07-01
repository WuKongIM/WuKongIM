import { act, render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { flushSync } from "react-dom"
import { createRoot } from "react-dom/client"
import { MemoryRouter } from "react-router-dom"
import { beforeEach, expect, test, vi } from "vitest"

import { createAnonymousAuthState, useAuthStore } from "@/auth/auth-store"
import { I18nProvider } from "@/i18n/provider"
import { resetLocale } from "@/i18n/locale-store"
import { ManagerApiError } from "@/lib/manager-api"
import type { ManagerNode, ManagerSlotTask } from "@/lib/manager-api.types"
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

function lifecycleElement(mode: "join" | "node", node: ManagerNode | null = null, onCompleted = vi.fn()) {
  return (
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
    </I18nProvider>
  )
}

function createSafeScaleInStatus(nodeId = 4) {
  return {
    node_id: nodeId,
    join_state: "leaving",
    generated_at: "2026-07-01T08:00:01Z",
    state_revision: 44,
    safe_to_proceed: true,
    safe_to_remove: true,
    blocked_by_missing_node: false,
    blocked_by_join_state: false,
    blocked_by_control_revision: false,
    blocked_by_health: false,
    blocked_by_stale_revision: false,
    blocked_by_controller_role: false,
    blocked_by_data_role: false,
    blocked_by_slots: false,
    blocked_by_slot_leadership: false,
    blocked_by_slot_runtime: false,
    blocked_by_tasks: false,
    blocked_by_channels: false,
    blocked_by_runtime_drain: false,
    unknown_runtime: false,
    runtime_unknown: false,
    unknown_control_revision: false,
    unknown_channel_inventory: false,
    health_fresh: true,
    health_status: "alive",
    health_freshness: "fresh",
    health_report_age_ms: 10,
    health_report_ttl_ms: 30000,
    observed_control_revision: 44,
    required_control_revision: 44,
    blocked_reasons: [],
    slot_replica_count: 0,
    slot_leader_count: 0,
    active_task_count: 0,
    failed_task_count: 0,
    channel_leader_count: 0,
    channel_replica_count: 0,
    channel_isr_count: 0,
    gateway_draining: true,
    accepting_new_sessions: false,
    gateway_sessions: 0,
    active_online: 0,
    closing_online: 0,
    total_online: 0,
    pending_activations: 0,
  }
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

test("plans starts and advances bounded slot onboarding for an active node", async () => {
  const slotTask = {
    task_id: "slot-replica-move-7-4",
    slot_id: 7,
    kind: "slot_replica_move",
    step: "add_learner",
    status: "pending",
    source_node: 1,
    target_node: 4,
    target_peers: [2, 3, 4],
    completion_policy: "all_target_peers",
    config_epoch: 9,
    attempt: 0,
    last_error: "",
    phase_index: 0,
    observed_config_index: 0,
    observed_voters: [2, 3],
    observed_learners: [],
    participants: [],
  } satisfies ManagerSlotTask
  planNodeOnboardingMock.mockResolvedValueOnce({
    generated_at: "2026-07-01T08:00:00Z",
    state_revision: 42,
    target_node_id: 4,
    max_slot_moves: 1,
    candidates: [{
      slot_id: 7,
      source_node_id: 1,
      target_node_id: 4,
      target_peers: [2, 3, 4],
      config_epoch: 9,
    }],
    skipped: [{ slot_id: 8, reason: "active_task", message: "slot already has an active task" }],
  })
  planNodeOnboardingMock.mockResolvedValueOnce({
    generated_at: "2026-07-01T08:00:04Z",
    state_revision: 46,
    target_node_id: 4,
    max_slot_moves: 1,
    candidates: [{
      slot_id: 9,
      source_node_id: 2,
      target_node_id: 4,
      target_peers: [1, 3, 4],
      config_epoch: 10,
    }],
    skipped: [],
  })
  startNodeOnboardingMock.mockResolvedValueOnce({
    generated_at: "2026-07-01T08:00:01Z",
    state_revision: 43,
    target_node_id: 4,
    max_slot_moves: 1,
    created: 1,
    results: [{ slot_id: 7, created: true, task: slotTask }],
    skipped: [],
  })
  getNodeOnboardingStatusMock.mockResolvedValueOnce({
    generated_at: "2026-07-01T08:00:02Z",
    state_revision: 44,
    target_node_id: 4,
    summary: { total_active: 0, pending: 0, running: 0, failed: 0 },
    tasks: [],
  })
  advanceNodeOnboardingMock.mockResolvedValueOnce({
    generated_at: "2026-07-01T08:00:03Z",
    state_revision: 45,
    target_node_id: 4,
    max_slot_moves: 1,
    created: 0,
    results: [],
    skipped: [{ slot_id: 7, reason: "already_hosted", message: "target node already hosts the slot" }],
  })
  const onCompleted = vi.fn()
  const user = userEvent.setup()

  renderLifecycle("node", activeNode, onCompleted)

  await user.click(screen.getByRole("button", { name: "Plan slot onboarding" }))

  expect(planNodeOnboardingMock).toHaveBeenCalledWith(4, { maxSlotMoves: 1 })
  expect(await screen.findByText("Slot 7")).toBeInTheDocument()
  expect(screen.getByText("slot already has an active task")).toBeInTheDocument()

  await user.click(screen.getByRole("button", { name: "Start onboarding" }))

  expect(startNodeOnboardingMock).toHaveBeenCalledWith(4, { maxSlotMoves: 1 })
  expect(await screen.findByText("Created tasks: 1")).toBeInTheDocument()
  expect(onCompleted).toHaveBeenCalledTimes(1)

  await user.click(screen.getByRole("button", { name: "Refresh onboarding status" }))

  expect(getNodeOnboardingStatusMock).toHaveBeenCalledWith(4)
  expect(await screen.findByText("Active tasks: 0")).toBeInTheDocument()

  await user.click(screen.getByRole("button", { name: "Plan slot onboarding" }))

  expect(planNodeOnboardingMock).toHaveBeenLastCalledWith(4, { maxSlotMoves: 1 })
  expect(await screen.findByText("Slot 9")).toBeInTheDocument()
  expect(screen.queryByText("Created tasks: 1")).not.toBeInTheDocument()
  expect(screen.queryByText("Active tasks: 0")).not.toBeInTheDocument()

  await user.click(screen.getByRole("button", { name: "Advance onboarding" }))

  expect(advanceNodeOnboardingMock).toHaveBeenCalledWith(4, { maxSlotMoves: 1 })
  expect(await screen.findByText("target node already hosts the slot")).toBeInTheDocument()
  expect(onCompleted).toHaveBeenCalledTimes(2)
})

test("invalid onboarding move bounds do not call plan and blank defaults to one", async () => {
  planNodeOnboardingMock.mockResolvedValueOnce({
    generated_at: "2026-07-01T08:00:00Z",
    state_revision: 42,
    target_node_id: 4,
    max_slot_moves: 1,
    candidates: [{
      slot_id: 7,
      source_node_id: 1,
      target_node_id: 4,
      target_peers: [2, 3, 4],
      config_epoch: 9,
    }],
    skipped: [],
  })
  const user = userEvent.setup()

  renderLifecycle("node", activeNode)

  await user.clear(screen.getByLabelText("Max slot moves"))
  await user.type(screen.getByLabelText("Max slot moves"), "-1")
  await user.click(screen.getByRole("button", { name: "Plan slot onboarding" }))

  expect(planNodeOnboardingMock).not.toHaveBeenCalled()
  expect(screen.getByRole("alert")).toHaveTextContent("Max slot moves must be a positive safe integer.")

  await user.clear(screen.getByLabelText("Max slot moves"))
  await user.type(screen.getByLabelText("Max slot moves"), "1.5")
  await user.click(screen.getByRole("button", { name: "Plan slot onboarding" }))

  expect(planNodeOnboardingMock).not.toHaveBeenCalled()
  expect(screen.getByRole("alert")).toHaveTextContent("Max slot moves must be a positive safe integer.")

  await user.clear(screen.getByLabelText("Max slot moves"))
  await user.click(screen.getByRole("button", { name: "Plan slot onboarding" }))

  expect(planNodeOnboardingMock).toHaveBeenCalledWith(4, { maxSlotMoves: 1 })
  expect(await screen.findByText("Slot 7")).toBeInTheDocument()
  expect(screen.queryByRole("alert")).not.toBeInTheDocument()
})

test("runs scale-in stages through leaving drain status advance and remove", async () => {
  const leavingNode: ManagerNode = {
    ...activeNode,
    membership: { role: "data", join_state: "leaving", schedulable: false },
    runtime: { ...activeNode.runtime, accepting_new_sessions: false, draining: true },
    actions: { ...activeNode.actions, can_onboard: false, can_scale_in: true },
  }
  const scaleInCandidate = {
    slot_id: 7,
    source_node_id: 4,
    target_node_id: 1,
    desired_peers: [1, 2, 3],
    target_peers: [1, 2, 3],
    config_epoch: 9,
  }
  planNodeScaleInMock.mockResolvedValueOnce({
    generated_at: "2026-07-01T08:00:00Z",
    state_revision: 42,
    node_id: 4,
    candidates: [scaleInCandidate],
    blocked_by_status: false,
  })
  startNodeScaleInMock.mockResolvedValueOnce({
    changed: true,
    node_id: 4,
    addr: "127.0.0.1:7004",
    join_state: "leaving",
    revision: 43,
  })
  setNodeScaleInDrainMock.mockResolvedValueOnce({
    node_id: 4,
    draining: true,
    accepting_new_sessions: false,
    gateway_sessions: 0,
    active_online: 0,
    closing_online: 0,
    total_online: 0,
    pending_activations: 0,
    unknown: false,
  })
  const safeScaleInStatus = {
    node_id: 4,
    join_state: "leaving",
    generated_at: "2026-07-01T08:00:01Z",
    state_revision: 44,
    safe_to_proceed: true,
    safe_to_remove: true,
    blocked_by_missing_node: false,
    blocked_by_join_state: false,
    blocked_by_control_revision: false,
    blocked_by_health: false,
    blocked_by_stale_revision: false,
    blocked_by_controller_role: false,
    blocked_by_data_role: false,
    blocked_by_slots: false,
    blocked_by_slot_leadership: false,
    blocked_by_slot_runtime: false,
    blocked_by_tasks: false,
    blocked_by_channels: false,
    blocked_by_runtime_drain: false,
    unknown_runtime: false,
    runtime_unknown: false,
    unknown_control_revision: false,
    unknown_channel_inventory: false,
    health_fresh: true,
    health_status: "alive",
    health_freshness: "fresh",
    health_report_age_ms: 10,
    health_report_ttl_ms: 30000,
    observed_control_revision: 44,
    required_control_revision: 44,
    blocked_reasons: [],
    slot_replica_count: 0,
    slot_leader_count: 0,
    active_task_count: 0,
    failed_task_count: 0,
    channel_leader_count: 0,
    channel_replica_count: 0,
    channel_isr_count: 0,
    gateway_draining: true,
    accepting_new_sessions: false,
    gateway_sessions: 0,
    active_online: 0,
    closing_online: 0,
    total_online: 0,
    pending_activations: 0,
  }
  getNodeScaleInStatusMock
    .mockResolvedValueOnce(safeScaleInStatus)
    .mockResolvedValueOnce(safeScaleInStatus)
  advanceNodeScaleInMock.mockResolvedValueOnce({
    generated_at: "2026-07-01T08:00:02Z",
    state_revision: 45,
    node_id: 4,
    created: 1,
    skipped: 0,
    candidates: [scaleInCandidate],
  })
  removeNodeAfterScaleInMock.mockResolvedValueOnce({
    changed: true,
    node_id: 4,
    join_state: "removed",
    revision: 49,
  })
  const onCompleted = vi.fn()
  const user = userEvent.setup()

  renderLifecycle("node", leavingNode, onCompleted)

  await user.click(screen.getByRole("button", { name: "Plan scale-in" }))

  expect(planNodeScaleInMock).toHaveBeenCalledWith(4, { maxSlotMoves: 1 })
  expect(await screen.findByText("Target peers: 1, 2, 3")).toBeInTheDocument()

  await user.click(screen.getByRole("button", { name: "Mark leaving" }))

  expect(startNodeScaleInMock).toHaveBeenCalledWith(4)
  expect(await screen.findByText("Join state: leaving")).toBeInTheDocument()

  await user.click(screen.getByRole("button", { name: "Enable drain mode" }))

  expect(setNodeScaleInDrainMock).toHaveBeenCalledWith(4, { draining: true })
  expect(await screen.findByText("Accepting new sessions: no")).toBeInTheDocument()

  await user.click(screen.getByRole("button", { name: "Refresh scale-in status" }))

  expect(getNodeScaleInStatusMock).toHaveBeenCalledWith(4)
  expect(await screen.findByText("Safe to remove: yes")).toBeInTheDocument()

  await user.click(screen.getByRole("button", { name: "Advance scale-in" }))

  expect(advanceNodeScaleInMock).toHaveBeenCalledWith(4, { maxSlotMoves: 1 })
  expect(await screen.findByText("Created tasks: 1 / skipped: 0")).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Remove node" })).toBeDisabled()

  await user.click(screen.getByRole("button", { name: "Refresh scale-in status" }))

  expect(getNodeScaleInStatusMock).toHaveBeenCalledTimes(2)
  expect(await screen.findByText("Safe to remove: yes")).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Remove node" })).toBeEnabled()

  await user.click(screen.getByRole("button", { name: "Remove node" }))

  expect(removeNodeAfterScaleInMock).toHaveBeenCalledWith(4)
  expect(await screen.findByText("Removed revision: 49")).toBeInTheDocument()
  expect(onCompleted).toHaveBeenCalledTimes(4)
})

test("invalidates safe scale-in status before a failed mutating action", async () => {
  const leavingNode: ManagerNode = {
    ...activeNode,
    membership: { role: "data", join_state: "leaving", schedulable: false },
    runtime: { ...activeNode.runtime, accepting_new_sessions: false, draining: true },
    actions: { ...activeNode.actions, can_onboard: false, can_scale_in: true },
  }
  getNodeScaleInStatusMock.mockResolvedValueOnce(createSafeScaleInStatus())
  advanceNodeScaleInMock.mockRejectedValueOnce(new Error("advance rejected"))
  const user = userEvent.setup()

  renderLifecycle("node", leavingNode)

  await user.click(screen.getByRole("button", { name: "Refresh scale-in status" }))

  expect(await screen.findByText("Safe to remove: yes")).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Remove node" })).toBeEnabled()

  await user.click(screen.getByRole("button", { name: "Advance scale-in" }))

  expect(advanceNodeScaleInMock).toHaveBeenCalledWith(4, { maxSlotMoves: 1 })
  expect(await screen.findByRole("alert")).toHaveTextContent("advance rejected")
  expect(screen.getByRole("button", { name: "Remove node" })).toBeDisabled()
})

test("invalidates safe scale-in status before a failed status refresh", async () => {
  const leavingNode: ManagerNode = {
    ...activeNode,
    membership: { role: "data", join_state: "leaving", schedulable: false },
    runtime: { ...activeNode.runtime, accepting_new_sessions: false, draining: true },
    actions: { ...activeNode.actions, can_onboard: false, can_scale_in: true },
  }
  getNodeScaleInStatusMock
    .mockResolvedValueOnce(createSafeScaleInStatus())
    .mockRejectedValueOnce(new Error("status rejected"))
  const user = userEvent.setup()

  renderLifecycle("node", leavingNode)

  await user.click(screen.getByRole("button", { name: "Refresh scale-in status" }))

  expect(await screen.findByText("Safe to remove: yes")).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Remove node" })).toBeEnabled()

  await user.click(screen.getByRole("button", { name: "Refresh scale-in status" }))

  expect(getNodeScaleInStatusMock).toHaveBeenCalledTimes(2)
  expect(await screen.findByRole("alert")).toHaveTextContent("status rejected")
  expect(screen.getByRole("button", { name: "Remove node" })).toBeDisabled()
})

test("does not reuse safe scale-in status when the mounted sheet switches nodes", async () => {
  const leavingNode: ManagerNode = {
    ...activeNode,
    membership: { role: "data", join_state: "leaving", schedulable: false },
    runtime: { ...activeNode.runtime, accepting_new_sessions: false, draining: true },
    actions: { ...activeNode.actions, can_onboard: false, can_scale_in: true },
  }
  const nextLeavingNode: ManagerNode = {
    ...leavingNode,
    node_id: 5,
    name: "node-5",
    addr: "127.0.0.1:7005",
    runtime: leavingNode.runtime ? { ...leavingNode.runtime, node_id: 5 } : leavingNode.runtime,
  }
  getNodeScaleInStatusMock.mockResolvedValueOnce(createSafeScaleInStatus(4))
  const user = userEvent.setup()
  const container = document.createElement("div")
  document.body.appendChild(container)
  const root = createRoot(container)

  await act(async () => {
    root.render(lifecycleElement("node", leavingNode))
  })

  await user.click(screen.getByRole("button", { name: "Refresh scale-in status" }))

  expect(await screen.findByText("Safe to remove: yes")).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Remove node" })).toBeEnabled()

  let removeButton: HTMLElement | null = null
  act(() => {
    flushSync(() => {
      root.render(lifecycleElement("node", nextLeavingNode))
    })
    removeButton = screen.getByRole("button", { name: "Remove node" })
    expect(removeButton).toBeDisabled()
  })

  await user.click(removeButton!)

  expect(removeNodeAfterScaleInMock).not.toHaveBeenCalled()

  await act(async () => {
    root.unmount()
  })
  container.remove()
})

test("disables scale-in actions while node is joining", async () => {
  const user = userEvent.setup()

  renderLifecycle("node", joiningNode)

  const actionNames = [
    "Plan scale-in",
    "Mark leaving",
    "Enable drain mode",
    "Refresh scale-in status",
    "Advance scale-in",
    "Remove node",
  ]
  for (const actionName of actionNames) {
    const button = screen.getByRole("button", { name: actionName })
    expect(button).toBeDisabled()
    await user.click(button)
  }

  expect(screen.getByRole("button", { name: "Diagnostics" })).toBeInTheDocument()
  expect(planNodeScaleInMock).not.toHaveBeenCalled()
  expect(startNodeScaleInMock).not.toHaveBeenCalled()
  expect(setNodeScaleInDrainMock).not.toHaveBeenCalled()
  expect(getNodeScaleInStatusMock).not.toHaveBeenCalled()
  expect(advanceNodeScaleInMock).not.toHaveBeenCalled()
  expect(removeNodeAfterScaleInMock).not.toHaveBeenCalled()
})

test("disables scale-in actions when backend can_scale_in is false", async () => {
  const scaleInBlockedNode: ManagerNode = {
    ...activeNode,
    actions: { ...activeNode.actions, can_scale_in: false },
  }
  const user = userEvent.setup()

  renderLifecycle("node", scaleInBlockedNode)

  const actionNames = [
    "Plan scale-in",
    "Mark leaving",
    "Enable drain mode",
    "Refresh scale-in status",
    "Advance scale-in",
    "Remove node",
  ]
  for (const actionName of actionNames) {
    const button = screen.getByRole("button", { name: actionName })
    expect(button).toBeDisabled()
    await user.click(button)
  }

  expect(planNodeScaleInMock).not.toHaveBeenCalled()
  expect(startNodeScaleInMock).not.toHaveBeenCalled()
  expect(setNodeScaleInDrainMock).not.toHaveBeenCalled()
  expect(getNodeScaleInStatusMock).not.toHaveBeenCalled()
  expect(advanceNodeScaleInMock).not.toHaveBeenCalled()
  expect(removeNodeAfterScaleInMock).not.toHaveBeenCalled()
})
