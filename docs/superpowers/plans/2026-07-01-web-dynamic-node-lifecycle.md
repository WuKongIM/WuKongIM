# Web Dynamic Node Lifecycle Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rebuild the web node expansion and scale-in UI around the current internalv2 per-node lifecycle APIs.

**Architecture:** Treat `web/src/lib/manager-api.ts` as the only HTTP boundary, then build a focused lifecycle sheet under `/cluster/nodes` that drives join, activate, onboarding, scale-in, drain, remove, and diagnostics stages. Remove stale standalone onboarding and cancel/drain/resume assumptions instead of wrapping them in compatibility helpers.

**Tech Stack:** React, TypeScript, React Intl, React Testing Library, Vitest, existing manager shell components.

---

## Source Spec

- `docs/superpowers/specs/2026-07-01-web-dynamic-node-lifecycle-design.md`

## File Structure

- Modify `web/src/lib/manager-api.types.ts`: add current per-stage response DTOs beside stale DTOs first, then remove stale DTOs after the node page stops compiling against them.
- Modify `web/src/lib/manager-api.ts`: add current internalv2 lifecycle calls; convert existing scale-in calls after the node page migrates; remove stale `/manager/node-onboarding/*`, `/draining`, `/resume`, and `/scale-in/cancel` calls in cleanup.
- Modify `web/src/lib/manager-api.test.ts`: prove endpoint paths, request bodies, and error preservation; add the stale-contract guard only after stale exports are removed.
- Create `web/src/pages/nodes/dynamic-node-lifecycle.tsx`: side-sheet content for join, activate, onboarding, scale-in, and diagnostics.
- Create `web/src/pages/nodes/dynamic-node-lifecycle.test.tsx`: focused component tests for lifecycle flows.
- Modify `web/src/pages/nodes/page.tsx`: wire row actions and header actions into `DynamicNodeLifecycleSheet`; remove stale `NodeOnboardingPanel` import.
- Modify `web/src/pages/nodes/page.test.tsx`: keep node list and row-action integration tests; remove tests for stale drain/resume/cancel and standalone onboarding job panel.
- Delete `web/src/pages/onboarding/page.tsx`: remove stale standalone onboarding job model.
- Delete `web/src/pages/onboarding/page.test.tsx`: remove stale standalone onboarding tests.
- Modify `web/src/i18n/messages/en.ts` and `web/src/i18n/messages/zh-CN.ts`: add lifecycle copy and remove copy that only supported old job/cancel flows.
- Modify `web/README.md`: update the `/cluster/nodes` row to list the current lifecycle APIs.

## Task 1: API Contract Types And Non-Conflicting Fetch Functions

**Files:**
- Modify: `web/src/lib/manager-api.types.ts`
- Modify: `web/src/lib/manager-api.ts`
- Modify: `web/src/lib/manager-api.test.ts`

- [ ] **Step 1: Write failing API contract tests**

Add this test near the end of `web/src/lib/manager-api.test.ts`, before the existing stale onboarding and scale-in tests. Keep existing `describe` setup and `fetchMock` helpers. This task does not change the existing `planNodeScaleIn`, `startNodeScaleIn`, `getNodeScaleInStatus`, or `advanceNodeScaleIn` functions yet because `web/src/pages/nodes/page.tsx` still compiles against their old report shape until Task 4.

```ts
it("calls current dynamic node lifecycle manager endpoints", async () => {
  const joinResponse = { created: true, node_id: 4, addr: "127.0.0.1:7004", join_state: "joining", revision: 41 }
  const activateResponse = { changed: true, node_id: 4, addr: "127.0.0.1:7004", join_state: "active", revision: 42 }
  const onboardingPlan = {
    generated_at: "2026-07-01T08:00:00Z",
    state_revision: 42,
    target_node_id: 4,
    max_slot_moves: 1,
    candidates: [{ slot_id: 7, source_node_id: 1, target_node_id: 4, target_peers: [2, 3, 4], config_epoch: 9 }],
    skipped: [{ slot_id: 8, reason: "active_task", message: "slot already has an active task" }],
  }
  const onboardingStart = {
    generated_at: "2026-07-01T08:00:01Z",
    state_revision: 43,
    target_node_id: 4,
    max_slot_moves: 1,
    created: 1,
    results: [{
      slot_id: 7,
      created: true,
      task: {
        slot_id: 7,
        kind: "slot_replica_move",
        step: "add_learner",
        status: "pending",
        source_node: 1,
        target_node: 4,
        attempt: 0,
        next_run_at: null,
        last_error: "",
      },
    }],
    skipped: [],
  }
  const onboardingStatus = {
    generated_at: "2026-07-01T08:00:02Z",
    state_revision: 44,
    target_node_id: 4,
    summary: { total_active: 1, pending: 1, running: 0, failed: 0 },
    tasks: [onboardingStart.results[0].task],
  }
  const drain = {
    node_id: 4,
    draining: true,
    accepting_new_sessions: false,
    gateway_sessions: 0,
    active_online: 0,
    closing_online: 0,
    total_online: 0,
    pending_activations: 0,
    unknown: false,
  }
  const remove = { changed: true, node_id: 4, join_state: "removed", revision: 49 }
  const diagnostics = {
    generated_at: "2026-07-01T08:02:00Z",
    state_revision: 49,
    node_id: 4,
    node: {
      node_id: 4,
      name: "node-4",
      addr: "127.0.0.1:7004",
      status: "alive",
      last_heartbeat_at: "2026-07-01T08:00:00Z",
      is_local: false,
      capacity_weight: 1,
      membership: { role: "data", join_state: "removed", schedulable: false },
      health: { status: "alive", last_heartbeat_at: "2026-07-01T08:00:00Z" },
      controller: { role: "follower", voter: false, leader_id: 1 },
      slot_stats: { count: 0, leader_count: 0 },
      slots: { replica_count: 0, leader_count: 0, follower_count: 0, quorum_lost_count: 0, unreported_count: 0 },
      runtime: { node_id: 4, active_online: 0, closing_online: 0, total_online: 0, gateway_sessions: 0, sessions_by_listener: {}, accepting_new_sessions: false, draining: true, unknown: false },
      actions: { can_drain: false, can_resume: false, can_scale_in: false, can_onboard: false },
    },
    scale_in: null,
    onboarding: null,
    active_tasks: [],
    task_audits: [],
    slots: [],
    summary: {
      safe_to_remove: true,
      blocked_reasons: [],
      active_task_count: 0,
      failed_task_count: 0,
      slot_replica_count: 0,
      slot_leader_count: 0,
      control_revision_gap: 0,
      slot_replica_move_state: "idle",
      oldest_task_age_seconds: 0,
      audit_available: true,
      runtime_unknown: false,
      slot_runtime_unknown: false,
      recommended_next_action: "remove_node",
      blocked_by_control_revision: false,
      blocked_by_slots: false,
      blocked_by_tasks: false,
    },
    sources: {
      control_snapshot: { available: true, last_error: "" },
      task_audit: { available: true, last_error: "" },
      slot_runtime: { available: true, last_error: "" },
    },
    warnings: [],
  }

  fetchMock
    .mockResolvedValueOnce(new Response(JSON.stringify(joinResponse), { status: 202 }))
    .mockResolvedValueOnce(new Response(JSON.stringify(activateResponse), { status: 202 }))
    .mockResolvedValueOnce(new Response(JSON.stringify(onboardingPlan), { status: 200 }))
    .mockResolvedValueOnce(new Response(JSON.stringify(onboardingStart), { status: 202 }))
    .mockResolvedValueOnce(new Response(JSON.stringify(onboardingStatus), { status: 200 }))
    .mockResolvedValueOnce(new Response(JSON.stringify(onboardingStart), { status: 202 }))
    .mockResolvedValueOnce(new Response(JSON.stringify(drain), { status: 200 }))
    .mockResolvedValueOnce(new Response(JSON.stringify(remove), { status: 202 }))
    .mockResolvedValueOnce(new Response(JSON.stringify(diagnostics), { status: 200 }))

  await expect(joinNode({ nodeId: 4, addr: "127.0.0.1:7004", name: "node-4", capacityWeight: 1 })).resolves.toEqual(joinResponse)
  await expect(activateNode(4)).resolves.toEqual(activateResponse)
  await expect(planNodeOnboarding(4, { maxSlotMoves: 1 })).resolves.toEqual(onboardingPlan)
  await expect(startNodeOnboarding(4, { maxSlotMoves: 1 })).resolves.toEqual(onboardingStart)
  await expect(getNodeOnboardingStatus(4)).resolves.toEqual(onboardingStatus)
  await expect(advanceNodeOnboarding(4, { maxSlotMoves: 1 })).resolves.toEqual(onboardingStart)
  await expect(setNodeScaleInDrain(4, { draining: true })).resolves.toEqual(drain)
  await expect(removeNodeAfterScaleIn(4)).resolves.toEqual(remove)
  await expect(getDynamicNodeDiagnostics(4, { taskLimit: 10, auditLimit: 10, slotLimit: 20 })).resolves.toEqual(diagnostics)

  expect(fetchMock.mock.calls.map((call) => call[0])).toEqual([
    "/manager/nodes/join",
    "/manager/nodes/4/activate",
    "/manager/nodes/4/onboarding/plan",
    "/manager/nodes/4/onboarding/start",
    "/manager/nodes/4/onboarding/status",
    "/manager/nodes/4/onboarding/advance",
    "/manager/nodes/4/scale-in/drain",
    "/manager/nodes/4/scale-in/remove",
    "/manager/nodes/4/diagnostics?task_limit=10&audit_limit=10&slot_limit=20",
  ])
  expect(JSON.parse((fetchMock.mock.calls[0]?.[1] as { body: string }).body)).toEqual({
    node_id: 4,
    name: "node-4",
    addr: "127.0.0.1:7004",
    capacity_weight: 1,
  })
  expect(JSON.parse((fetchMock.mock.calls[2]?.[1] as { body: string }).body)).toEqual({ max_slot_moves: 1 })
  expect(JSON.parse((fetchMock.mock.calls[6]?.[1] as { body: string }).body)).toEqual({ draining: true })
})
```

Update the import list at the top of `web/src/lib/manager-api.test.ts` so this test imports exactly these lifecycle functions:

```ts
import {
  activateNode,
  advanceNodeOnboarding,
  getDynamicNodeDiagnostics,
  getNodeOnboardingStatus,
  joinNode,
  planNodeOnboarding,
  removeNodeAfterScaleIn,
  setNodeScaleInDrain,
  startNodeOnboarding,
} from "@/lib/manager-api"
```

- [ ] **Step 2: Run API tests and verify failure**

Run:

```bash
cd web && ./node_modules/.bin/vitest run src/lib/manager-api.test.ts -t "current dynamic node lifecycle"
```

Expected: FAIL because `joinNode`, `activateNode`, current onboarding functions, drain, remove, and diagnostics functions are not exported yet.

- [ ] **Step 3: Add current lifecycle TypeScript DTOs**

In `web/src/lib/manager-api.types.ts`, add this current response model near the existing node lifecycle types. Do not remove the old onboarding job DTOs or `ManagerNodeScaleInReport` yet; `web/src/pages/onboarding/page.tsx` and the old scale-in panel still compile against them until Tasks 4 and 6.

```ts
export type ManagerJoinNodeInput = {
  nodeId: number
  name?: string
  addr: string
  capacityWeight?: number
}

export type ManagerJoinNodeResponse = {
  created: boolean
  node_id: number
  addr: string
  join_state: string
  revision: number
}

export type ManagerActivateNodeResponse = {
  changed: boolean
  node_id: number
  addr?: string
  join_state: string
  revision: number
}

export type ManagerNodeOnboardingActionInput = {
  maxSlotMoves?: number
}

export type ManagerNodeOnboardingSlotCandidate = {
  slot_id: number
  source_node_id: number
  target_node_id: number
  target_peers: number[]
  config_epoch: number
}

export type ManagerNodeOnboardingSkip = {
  slot_id: number
  reason: string
  message: string
}

export type ManagerNodeOnboardingPlanResponse = {
  generated_at: string
  state_revision: number
  target_node_id: number
  max_slot_moves: number
  candidates: ManagerNodeOnboardingSlotCandidate[]
  skipped: ManagerNodeOnboardingSkip[]
}

export type ManagerNodeOnboardingTaskResult = {
  slot_id: number
  created: boolean
  task?: ManagerTask
}

export type ManagerNodeOnboardingStartResponse = {
  generated_at: string
  state_revision: number
  target_node_id: number
  max_slot_moves: number
  created: number
  results: ManagerNodeOnboardingTaskResult[]
  skipped: ManagerNodeOnboardingSkip[]
}

export type ManagerNodeOnboardingStatusResponse = {
  generated_at: string
  state_revision: number
  target_node_id: number
  summary: {
    total_active: number
    pending: number
    running: number
    failed: number
  }
  tasks: ManagerTask[]
}

export type ManagerNodeScaleInActionInput = {
  maxSlotMoves?: number
}

export type ManagerNodeScaleInCandidate = {
  slot_id: number
  source_node_id: number
  target_node_id: number
  desired_peers: number[]
  target_peers: number[]
  config_epoch: number
}

export type ManagerNodeScaleInPlanResponse = {
  generated_at: string
  state_revision: number
  node_id: number
  candidates: ManagerNodeScaleInCandidate[]
  blocked_by_status: boolean
}

export type ManagerNodeScaleInStartResponse = {
  changed: boolean
  node_id: number
  addr: string
  join_state: string
  revision: number
}

export type ManagerNodeScaleInDrainResponse = {
  node_id: number
  draining: boolean
  accepting_new_sessions: boolean
  gateway_sessions: number
  active_online: number
  closing_online: number
  total_online: number
  pending_activations: number
  unknown: boolean
}

export type ManagerNodeScaleInStatusResponse = {
  node_id: number
  join_state: string
  generated_at: string
  state_revision: number
  safe_to_proceed: boolean
  safe_to_remove: boolean
  blocked_by_missing_node: boolean
  blocked_by_join_state: boolean
  blocked_by_control_revision: boolean
  blocked_by_health: boolean
  blocked_by_stale_revision: boolean
  blocked_by_controller_role: boolean
  blocked_by_data_role: boolean
  blocked_by_slots: boolean
  blocked_by_slot_leadership: boolean
  blocked_by_slot_runtime: boolean
  blocked_by_tasks: boolean
  blocked_by_channels: boolean
  blocked_by_runtime_drain: boolean
  unknown_runtime: boolean
  runtime_unknown: boolean
  unknown_control_revision: boolean
  unknown_channel_inventory: boolean
  health_fresh: boolean
  health_status: string
  health_freshness: string
  health_report_age_ms: number
  health_report_ttl_ms: number
  observed_control_revision: number
  required_control_revision: number
  blocked_reasons: string[]
  slot_replica_count: number
  slot_leader_count: number
  active_task_count: number
  failed_task_count: number
  channel_leader_count: number
  channel_replica_count: number
  channel_isr_count: number
  gateway_draining: boolean
  accepting_new_sessions: boolean
  gateway_sessions: number
  active_online: number
  closing_online: number
  total_online: number
  pending_activations: number
}

export type ManagerNodeScaleInAdvanceResponse = {
  generated_at: string
  state_revision: number
  node_id: number
  created: number
  skipped: number
  candidates: ManagerNodeScaleInCandidate[]
}

export type ManagerNodeScaleInRemoveResponse = {
  changed: boolean
  node_id: number
  join_state: string
  revision: number
}

export type ManagerDynamicNodeDiagnosticsLimits = {
  taskLimit?: number
  auditLimit?: number
  slotLimit?: number
}

export type ManagerDynamicNodeDiagnosticSource = {
  available: boolean
  last_error: string
}

export type ManagerDynamicNodeDiagnosticSlot = {
  slot_id: number
  desired_peers: number[]
  preferred_leader: number
  config_epoch: number
  task_id: string
  task_kind: string
  task_step: string
  task_status: string
  current_leader: number
  current_voters: number[]
}

export type ManagerDynamicNodeDiagnosticsResponse = {
  generated_at: string
  state_revision: number
  node_id: number
  node: ManagerNode
  scale_in: ManagerNodeScaleInStatusResponse | null
  onboarding: ManagerNodeOnboardingStatusResponse | null
  active_tasks: ManagerControllerTask[]
  task_audits: ManagerControllerTaskAuditSnapshot[]
  slots: ManagerDynamicNodeDiagnosticSlot[]
  summary: {
    safe_to_remove: boolean
    blocked_reasons: string[]
    active_task_count: number
    failed_task_count: number
    slot_replica_count: number
    slot_leader_count: number
    control_revision_gap: number
    slot_replica_move_state: string
    oldest_task_age_seconds: number
    audit_available: boolean
    runtime_unknown: boolean
    slot_runtime_unknown: boolean
    recommended_next_action: string
    blocked_by_control_revision: boolean
    blocked_by_slots: boolean
    blocked_by_tasks: boolean
  }
  sources: {
    control_snapshot: ManagerDynamicNodeDiagnosticSource
    task_audit: ManagerDynamicNodeDiagnosticSource
    slot_runtime: ManagerDynamicNodeDiagnosticSource
  }
  warnings: string[]
}
```

- [ ] **Step 4: Export current non-conflicting lifecycle API functions**

In `web/src/lib/manager-api.ts`, update lifecycle imports from `manager-api.types.ts` and add these functions beside the existing scale-in and old onboarding job functions. Do not modify `planNodeScaleIn`, `startNodeScaleIn`, `getNodeScaleInStatus`, or `advanceNodeScaleIn` in this task.

```ts
function buildNodeLifecycleMoveBody(input: { maxSlotMoves?: number } = {}) {
  return JSON.stringify({
    max_slot_moves: input.maxSlotMoves,
  })
}

function buildDynamicNodeDiagnosticsPath(nodeId: number, limits?: ManagerDynamicNodeDiagnosticsLimits) {
  const search = new URLSearchParams()
  if (typeof limits?.taskLimit === "number") {
    search.set("task_limit", String(limits.taskLimit))
  }
  if (typeof limits?.auditLimit === "number") {
    search.set("audit_limit", String(limits.auditLimit))
  }
  if (typeof limits?.slotLimit === "number") {
    search.set("slot_limit", String(limits.slotLimit))
  }
  const query = search.toString()
  return `/manager/nodes/${nodeId}/diagnostics${query ? `?${query}` : ""}`
}

export function joinNode(input: ManagerJoinNodeInput) {
  return jsonManagerFetch<ManagerJoinNodeResponse>("/manager/nodes/join", {
    method: "POST",
    body: JSON.stringify({
      node_id: input.nodeId,
      name: input.name ?? "",
      addr: input.addr,
      capacity_weight: input.capacityWeight,
    }),
  })
}

export function activateNode(nodeId: number) {
  return jsonManagerFetch<ManagerActivateNodeResponse>(`/manager/nodes/${nodeId}/activate`, {
    method: "POST",
  })
}

export function planNodeOnboarding(nodeId: number, input: ManagerNodeOnboardingActionInput = {}) {
  return jsonManagerFetch<ManagerNodeOnboardingPlanResponse>(`/manager/nodes/${nodeId}/onboarding/plan`, {
    method: "POST",
    body: buildNodeLifecycleMoveBody(input),
  })
}

export function startNodeOnboarding(nodeId: number, input: ManagerNodeOnboardingActionInput = {}) {
  return jsonManagerFetch<ManagerNodeOnboardingStartResponse>(`/manager/nodes/${nodeId}/onboarding/start`, {
    method: "POST",
    body: buildNodeLifecycleMoveBody(input),
  })
}

export function getNodeOnboardingStatus(nodeId: number) {
  return jsonManagerFetch<ManagerNodeOnboardingStatusResponse>(`/manager/nodes/${nodeId}/onboarding/status`)
}

export function advanceNodeOnboarding(nodeId: number, input: ManagerNodeOnboardingActionInput = {}) {
  return jsonManagerFetch<ManagerNodeOnboardingStartResponse>(`/manager/nodes/${nodeId}/onboarding/advance`, {
    method: "POST",
    body: buildNodeLifecycleMoveBody(input),
  })
}

export function setNodeScaleInDrain(nodeId: number, input: { draining: boolean }) {
  return jsonManagerFetch<ManagerNodeScaleInDrainResponse>(`/manager/nodes/${nodeId}/scale-in/drain`, {
    method: "POST",
    body: JSON.stringify({ draining: input.draining }),
  })
}

export function removeNodeAfterScaleIn(nodeId: number) {
  return jsonManagerFetch<ManagerNodeScaleInRemoveResponse>(`/manager/nodes/${nodeId}/scale-in/remove`, {
    method: "POST",
  })
}

export function getDynamicNodeDiagnostics(nodeId: number, limits?: ManagerDynamicNodeDiagnosticsLimits) {
  return jsonManagerFetch<ManagerDynamicNodeDiagnosticsResponse>(buildDynamicNodeDiagnosticsPath(nodeId, limits))
}
```

Leave `markNodeDraining`, `resumeNode`, `cancelNodeScaleIn`, and the old `getNodeOnboarding*` job functions in place until Task 6, because stale page code still imports them before the page migration is complete.

- [ ] **Step 5: Run API tests and commit**

Run:

```bash
cd web && ./node_modules/.bin/vitest run src/lib/manager-api.test.ts -t "current dynamic node lifecycle"
cd web && ./node_modules/.bin/tsc -b
git add web/src/lib/manager-api.types.ts web/src/lib/manager-api.ts web/src/lib/manager-api.test.ts
git commit -m "feat(web): align node lifecycle api bindings"
```

Expected: selected Vitest test passes, TypeScript passes, and commit succeeds.

## Task 2: Node Page Entry Points For Join And Activate

**Files:**
- Modify: `web/src/pages/nodes/page.tsx`
- Create: `web/src/pages/nodes/dynamic-node-lifecycle.tsx`
- Create: `web/src/pages/nodes/dynamic-node-lifecycle.test.tsx`
- Modify: `web/src/pages/nodes/page.test.tsx`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`

- [ ] **Step 1: Write failing join and activate tests**

Create `web/src/pages/nodes/dynamic-node-lifecycle.test.tsx` with this setup and the first two tests:

```tsx
import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { MemoryRouter } from "react-router-dom"
import { beforeEach, expect, test, vi } from "vitest"

import { createAnonymousAuthState, useAuthStore } from "@/auth/auth-store"
import { I18nProvider } from "@/i18n/provider"
import { resetLocale } from "@/i18n/locale-store"
import { ManagerApiError } from "@/lib/manager-api"
import { DynamicNodeLifecycleSheet } from "@/pages/nodes/dynamic-node-lifecycle"
import type { ManagerNode } from "@/lib/manager-api.types"

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
  runtime: { node_id: 4, active_online: 0, closing_online: 0, total_online: 0, gateway_sessions: 0, sessions_by_listener: {}, accepting_new_sessions: true, draining: false, unknown: false },
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
```

- [ ] **Step 2: Run lifecycle component tests and verify failure**

Run:

```bash
cd web && ./node_modules/.bin/vitest run src/pages/nodes/dynamic-node-lifecycle.test.tsx -t "joins a new data node|activates a joining node"
```

Expected: FAIL because `DynamicNodeLifecycleSheet` does not exist.

- [ ] **Step 3: Create the lifecycle sheet shell with join and activate**

Create `web/src/pages/nodes/dynamic-node-lifecycle.tsx` with this initial implementation:

```tsx
import { useCallback, useMemo, useState } from "react"
import { useIntl } from "react-intl"

import { useAuthStore } from "@/auth/auth-store"
import { DetailSheet } from "@/components/manager/detail-sheet"
import { StatusBadge } from "@/components/manager/status-badge"
import { Button } from "@/components/ui/button"
import {
  activateNode,
  joinNode,
} from "@/lib/manager-api"
import type {
  ManagerActivateNodeResponse,
  ManagerJoinNodeResponse,
  ManagerNode,
} from "@/lib/manager-api.types"

export type DynamicNodeLifecycleMode = "join" | "node"

export type DynamicNodeLifecycleSheetProps = {
  open: boolean
  mode: DynamicNodeLifecycleMode
  node: ManagerNode | null
  onOpenChange: (open: boolean) => void
  onCompleted: () => void
}

function hasPermission(permissions: { resource: string; actions: string[] }[], resource: string, action: string) {
  return permissions.some((permission) => {
    if (permission.resource !== resource && permission.resource !== "*") {
      return false
    }
    return permission.actions.includes(action) || permission.actions.includes("*")
  })
}

function nodeJoinState(node: ManagerNode | null) {
  return node?.membership?.join_state ?? "unknown"
}

export function DynamicNodeLifecycleSheet({
  open,
  mode,
  node,
  onOpenChange,
  onCompleted,
}: DynamicNodeLifecycleSheetProps) {
  const intl = useIntl()
  const permissions = useAuthStore((state) => state.permissions)
  const canWriteNodes = useMemo(() => hasPermission(permissions, "cluster.node", "w"), [permissions])
  const [nodeId, setNodeId] = useState("")
  const [name, setName] = useState("")
  const [addr, setAddr] = useState("")
  const [capacityWeight, setCapacityWeight] = useState("1")
  const [pending, setPending] = useState(false)
  const [error, setError] = useState("")
  const [joinResult, setJoinResult] = useState<ManagerJoinNodeResponse | null>(null)
  const [activateResult, setActivateResult] = useState<ManagerActivateNodeResponse | null>(null)

  const submitJoin = useCallback(async () => {
    if (!canWriteNodes) {
      return
    }
    setPending(true)
    setError("")
    try {
      const result = await joinNode({
        nodeId: Number(nodeId),
        addr: addr.trim(),
        name: name.trim(),
        capacityWeight: Number(capacityWeight),
      })
      setJoinResult(result)
      onCompleted()
    } catch (err) {
      setError(err instanceof Error ? err.message : "node join failed")
    } finally {
      setPending(false)
    }
  }, [addr, canWriteNodes, capacityWeight, name, nodeId, onCompleted])

  const submitActivate = useCallback(async () => {
    if (!node || !canWriteNodes) {
      return
    }
    setPending(true)
    setError("")
    try {
      const result = await activateNode(node.node_id)
      setActivateResult(result)
      onCompleted()
    } catch (err) {
      setError(err instanceof Error ? err.message : "node activation failed")
    } finally {
      setPending(false)
    }
  }, [canWriteNodes, node, onCompleted])

  const title = mode === "join" ? "Add node" : "Node lifecycle"

  return (
    <DetailSheet onOpenChange={onOpenChange} open={open} title={title}>
      <div className="space-y-4">
        {!canWriteNodes ? (
          <div className="rounded-lg border border-border bg-muted/30 px-4 py-3 text-sm text-muted-foreground">
            Requires cluster.node write permission.
          </div>
        ) : null}
        {error ? <p className="text-sm text-destructive">{error}</p> : null}

        {mode === "join" ? (
          <div className="grid gap-3">
            <label className="text-sm font-medium text-foreground">
              Node ID
              <input className="mt-1 h-9 w-full rounded-md border border-border bg-background px-3 text-sm" onChange={(event) => setNodeId(event.target.value)} value={nodeId} />
            </label>
            <label className="text-sm font-medium text-foreground">
              Address
              <input className="mt-1 h-9 w-full rounded-md border border-border bg-background px-3 text-sm" onChange={(event) => setAddr(event.target.value)} value={addr} />
            </label>
            <label className="text-sm font-medium text-foreground">
              Name
              <input className="mt-1 h-9 w-full rounded-md border border-border bg-background px-3 text-sm" onChange={(event) => setName(event.target.value)} value={name} />
            </label>
            <label className="text-sm font-medium text-foreground">
              Capacity weight
              <input className="mt-1 h-9 w-full rounded-md border border-border bg-background px-3 text-sm" onChange={(event) => setCapacityWeight(event.target.value)} value={capacityWeight} />
            </label>
            <Button disabled={pending || !canWriteNodes} onClick={() => void submitJoin()}>
              Join node
            </Button>
            {joinResult ? (
              <div className="rounded-lg border border-border bg-muted/30 p-3 text-sm text-muted-foreground">
                <div>Node {joinResult.node_id}</div>
                <div>Join state: {joinResult.join_state}</div>
                <div>Revision: {joinResult.revision}</div>
              </div>
            ) : null}
          </div>
        ) : null}

        {mode === "node" && node ? (
          <div className="space-y-4">
            <div className="flex flex-wrap items-center gap-2 text-sm text-muted-foreground">
              <StatusBadge value={nodeJoinState(node)} />
              <span>Node {node.node_id}</span>
              <span>{node.addr}</span>
            </div>
            {nodeJoinState(node) === "joining" ? (
              <Button disabled={pending || !canWriteNodes} onClick={() => void submitActivate()}>
                Activate node
              </Button>
            ) : null}
            {activateResult ? (
              <div className="rounded-lg border border-border bg-muted/30 p-3 text-sm text-muted-foreground">
                <div>Join state: {activateResult.join_state}</div>
                <div>Revision: {activateResult.revision}</div>
              </div>
            ) : null}
            <Button size="sm" type="button" variant="outline">Diagnostics</Button>
          </div>
        ) : null}
      </div>
    </DetailSheet>
  )
}
```

This uses literal English labels first so the failing tests become green quickly. Task 6 moves these labels into the message catalogs.

- [ ] **Step 4: Wire node page header and row actions**

In `web/src/pages/nodes/page.tsx`, remove this import:

```ts
import { NodeOnboardingPanel } from "@/pages/onboarding/page"
```

Add this import:

```ts
import { DynamicNodeLifecycleSheet, type DynamicNodeLifecycleMode } from "@/pages/nodes/dynamic-node-lifecycle"
```

Add state near the existing sheet state:

```ts
const [lifecycleOpen, setLifecycleOpen] = useState(false)
const [lifecycleMode, setLifecycleMode] = useState<DynamicNodeLifecycleMode>("join")
const [lifecycleNode, setLifecycleNode] = useState<ManagerNode | null>(null)
```

Add handlers inside `NodeClusterListPanel`:

```ts
const openJoinLifecycle = useCallback(() => {
  setLifecycleMode("join")
  setLifecycleNode(null)
  setLifecycleOpen(true)
}, [])

const openNodeLifecycle = useCallback((node: ManagerNode) => {
  setLifecycleMode("node")
  setLifecycleNode(node)
  setLifecycleOpen(true)
}, [])

const refreshAfterLifecycleAction = useCallback(() => {
  void loadNodes(true)
}, [loadNodes])
```

Replace the old standalone onboarding link button:

```tsx
<Button asChild size="sm" variant="outline">
  <Link to="/cluster/nodes?tab=list&panel=onboarding">
    {intl.formatMessage({ id: "nav.onboarding.title" })}
  </Link>
</Button>
```

with:

```tsx
<Button onClick={openJoinLifecycle} size="sm" variant="outline">
  Add node
</Button>
```

In the table actions cell, add this action before `Inspect`:

```tsx
<Button
  aria-label={`Open lifecycle for node ${node.node_id}`}
  onClick={() => openNodeLifecycle(node)}
  size="sm"
  variant="outline"
>
  Lifecycle
</Button>
```

Render the sheet near the existing detail sheets:

```tsx
<DynamicNodeLifecycleSheet
  mode={lifecycleMode}
  node={lifecycleNode}
  onCompleted={refreshAfterLifecycleAction}
  onOpenChange={setLifecycleOpen}
  open={lifecycleOpen}
/>
```

Remove this old panel render:

```tsx
{showOnboardingPanel ? <NodeOnboardingPanel /> : null}
```

Also remove the stale `showOnboardingPanel` variable and simplify the active tab calculation:

```ts
const activeTab = normalizeTab(searchParams.get("tab"))
```

- [ ] **Step 5: Run tests and commit**

Run:

```bash
cd web && ./node_modules/.bin/vitest run src/pages/nodes/dynamic-node-lifecycle.test.tsx src/pages/nodes/page.test.tsx -t "joins a new data node|activates a joining node"
cd web && ./node_modules/.bin/tsc -b
git add web/src/pages/nodes/page.tsx web/src/pages/nodes/dynamic-node-lifecycle.tsx web/src/pages/nodes/dynamic-node-lifecycle.test.tsx
git commit -m "feat(web): add node lifecycle join and activate panel"
```

Expected: selected tests pass, TypeScript passes, and commit succeeds.

## Task 3: Per-Node Slot Onboarding Flow

**Files:**
- Modify: `web/src/lib/manager-api.ts`
- Modify: `web/src/lib/manager-api.test.ts`
- Modify: `web/src/pages/nodes/dynamic-node-lifecycle.tsx`
- Modify: `web/src/pages/nodes/dynamic-node-lifecycle.test.tsx`
- Modify: `web/src/pages/nodes/page.tsx`
- Modify: `web/src/pages/nodes/page.test.tsx`

- [ ] **Step 1: Add failing onboarding tests**

Append this test to `web/src/pages/nodes/dynamic-node-lifecycle.test.tsx`:

```tsx
test("plans starts and advances bounded slot onboarding for an active node", async () => {
  planNodeOnboardingMock.mockResolvedValueOnce({
    generated_at: "2026-07-01T08:00:00Z",
    state_revision: 42,
    target_node_id: 4,
    max_slot_moves: 1,
    candidates: [{ slot_id: 7, source_node_id: 1, target_node_id: 4, target_peers: [2, 3, 4], config_epoch: 9 }],
    skipped: [{ slot_id: 8, reason: "active_task", message: "slot already has an active task" }],
  })
  startNodeOnboardingMock.mockResolvedValueOnce({
    generated_at: "2026-07-01T08:00:01Z",
    state_revision: 43,
    target_node_id: 4,
    max_slot_moves: 1,
    created: 1,
    results: [{ slot_id: 7, created: true, task: { slot_id: 7, kind: "slot_replica_move", step: "add_learner", status: "pending", source_node: 1, target_node: 4, attempt: 0, next_run_at: null, last_error: "" } }],
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
    skipped: [{ slot_id: 9, reason: "target_already_peer", message: "target node already hosts the slot" }],
  })
  const user = userEvent.setup()

  renderLifecycle("node", activeNode)

  await user.click(screen.getByRole("button", { name: "Plan slot onboarding" }))
  expect(planNodeOnboardingMock).toHaveBeenCalledWith(4, { maxSlotMoves: 1 })
  expect(await screen.findByText("Slot 7")).toBeInTheDocument()
  expect(screen.getByText("slot already has an active task")).toBeInTheDocument()

  await user.click(screen.getByRole("button", { name: "Start onboarding" }))
  expect(startNodeOnboardingMock).toHaveBeenCalledWith(4, { maxSlotMoves: 1 })
  expect(await screen.findByText("Created tasks: 1")).toBeInTheDocument()

  await user.click(screen.getByRole("button", { name: "Refresh onboarding status" }))
  expect(getNodeOnboardingStatusMock).toHaveBeenCalledWith(4)
  expect(await screen.findByText("Active tasks: 0")).toBeInTheDocument()

  await user.click(screen.getByRole("button", { name: "Advance onboarding" }))
  expect(advanceNodeOnboardingMock).toHaveBeenCalledWith(4, { maxSlotMoves: 1 })
  expect(await screen.findByText("target node already hosts the slot")).toBeInTheDocument()
})
```

- [ ] **Step 2: Run onboarding test and verify failure**

Run:

```bash
cd web && ./node_modules/.bin/vitest run src/pages/nodes/dynamic-node-lifecycle.test.tsx -t "bounded slot onboarding"
```

Expected: FAIL because onboarding controls are not implemented.

- [ ] **Step 3: Add onboarding state and actions**

In `web/src/pages/nodes/dynamic-node-lifecycle.tsx`, extend imports:

```ts
import {
  activateNode,
  advanceNodeOnboarding,
  getNodeOnboardingStatus,
  joinNode,
  planNodeOnboarding,
  startNodeOnboarding,
} from "@/lib/manager-api"
import type {
  ManagerActivateNodeResponse,
  ManagerJoinNodeResponse,
  ManagerNode,
  ManagerNodeOnboardingPlanResponse,
  ManagerNodeOnboardingStartResponse,
  ManagerNodeOnboardingStatusResponse,
} from "@/lib/manager-api.types"
```

Add onboarding state inside `DynamicNodeLifecycleSheet`:

```ts
const [maxSlotMoves, setMaxSlotMoves] = useState("1")
const [onboardingPlan, setOnboardingPlan] = useState<ManagerNodeOnboardingPlanResponse | null>(null)
const [onboardingStart, setOnboardingStart] = useState<ManagerNodeOnboardingStartResponse | null>(null)
const [onboardingStatus, setOnboardingStatus] = useState<ManagerNodeOnboardingStatusResponse | null>(null)
```

Add these handlers:

```ts
const boundedMovesInput = useCallback(() => ({ maxSlotMoves: Number(maxSlotMoves) || 1 }), [maxSlotMoves])

const runOnboardingPlan = useCallback(async () => {
  if (!node) {
    return
  }
  setPending(true)
  setError("")
  try {
    const result = await planNodeOnboarding(node.node_id, boundedMovesInput())
    setOnboardingPlan(result)
  } catch (err) {
    setError(err instanceof Error ? err.message : "node onboarding plan failed")
  } finally {
    setPending(false)
  }
}, [boundedMovesInput, node])

const runOnboardingStart = useCallback(async () => {
  if (!node) {
    return
  }
  setPending(true)
  setError("")
  try {
    const result = await startNodeOnboarding(node.node_id, boundedMovesInput())
    setOnboardingStart(result)
    onCompleted()
  } catch (err) {
    setError(err instanceof Error ? err.message : "node onboarding start failed")
  } finally {
    setPending(false)
  }
}, [boundedMovesInput, node, onCompleted])

const refreshOnboardingStatus = useCallback(async () => {
  if (!node) {
    return
  }
  setPending(true)
  setError("")
  try {
    const result = await getNodeOnboardingStatus(node.node_id)
    setOnboardingStatus(result)
  } catch (err) {
    setError(err instanceof Error ? err.message : "node onboarding status failed")
  } finally {
    setPending(false)
  }
}, [node])

const runOnboardingAdvance = useCallback(async () => {
  if (!node) {
    return
  }
  setPending(true)
  setError("")
  try {
    const result = await advanceNodeOnboarding(node.node_id, boundedMovesInput())
    setOnboardingStart(result)
    onCompleted()
  } catch (err) {
    setError(err instanceof Error ? err.message : "node onboarding advance failed")
  } finally {
    setPending(false)
  }
}, [boundedMovesInput, node, onCompleted])
```

- [ ] **Step 4: Render onboarding controls**

Inside the `mode === "node"` branch, after activate result, render this block for active schedulable nodes:

```tsx
{nodeJoinState(node) === "active" && node.membership?.schedulable ? (
  <div className="space-y-3 rounded-lg border border-border bg-muted/20 p-3">
    <div className="flex flex-col gap-3 sm:flex-row sm:items-end">
      <label className="text-sm font-medium text-foreground">
        Max slot moves
        <input className="mt-1 h-9 w-32 rounded-md border border-border bg-background px-3 text-sm" onChange={(event) => setMaxSlotMoves(event.target.value)} value={maxSlotMoves} />
      </label>
      <Button disabled={pending} onClick={() => void runOnboardingPlan()} size="sm" type="button" variant="outline">
        Plan slot onboarding
      </Button>
      <Button disabled={pending || !onboardingPlan} onClick={() => void runOnboardingStart()} size="sm" type="button">
        Start onboarding
      </Button>
      <Button disabled={pending} onClick={() => void refreshOnboardingStatus()} size="sm" type="button" variant="outline">
        Refresh onboarding status
      </Button>
      <Button disabled={pending} onClick={() => void runOnboardingAdvance()} size="sm" type="button" variant="outline">
        Advance onboarding
      </Button>
    </div>
    {onboardingPlan ? (
      <div className="space-y-2 text-sm">
        <div>State revision: {onboardingPlan.state_revision}</div>
        {onboardingPlan.candidates.map((candidate) => (
          <div className="rounded-md border border-border bg-background px-3 py-2" key={candidate.slot_id}>
            <div className="font-medium text-foreground">Slot {candidate.slot_id}</div>
            <div className="text-muted-foreground">{candidate.source_node_id} -&gt; {candidate.target_node_id}</div>
            <div className="text-muted-foreground">Target peers: {candidate.target_peers.join(", ")}</div>
          </div>
        ))}
        {onboardingPlan.skipped.map((skip) => (
          <div className="text-muted-foreground" key={`${skip.slot_id}-${skip.reason}`}>{skip.message || skip.reason}</div>
        ))}
      </div>
    ) : null}
    {onboardingStart ? (
      <div className="text-sm text-muted-foreground">
        Created tasks: {onboardingStart.created}
      </div>
    ) : null}
    {onboardingStatus ? (
      <div className="text-sm text-muted-foreground">
        Active tasks: {onboardingStatus.summary.total_active}
      </div>
    ) : null}
  </div>
) : null}
```

- [ ] **Step 5: Run onboarding tests and commit**

Run:

```bash
cd web && ./node_modules/.bin/vitest run src/pages/nodes/dynamic-node-lifecycle.test.tsx -t "bounded slot onboarding"
cd web && ./node_modules/.bin/tsc -b
git add web/src/pages/nodes/dynamic-node-lifecycle.tsx web/src/pages/nodes/dynamic-node-lifecycle.test.tsx
git commit -m "feat(web): add per-node slot onboarding flow"
```

Expected: selected test passes, TypeScript passes, and commit succeeds.

## Task 4: Scale-In, Drain Mode, Status, Advance, And Remove

**Files:**
- Modify: `web/src/pages/nodes/dynamic-node-lifecycle.tsx`
- Modify: `web/src/pages/nodes/dynamic-node-lifecycle.test.tsx`
- Modify: `web/src/pages/nodes/page.test.tsx`

- [ ] **Step 1: Add failing scale-in test**

Append this test to `web/src/pages/nodes/dynamic-node-lifecycle.test.tsx`:

```tsx
test("runs scale-in stages through leaving drain status advance and remove", async () => {
  const leavingNode: ManagerNode = {
    ...activeNode,
    membership: { role: "data", join_state: "leaving", schedulable: false },
    runtime: { ...activeNode.runtime, accepting_new_sessions: false, draining: true },
  }
  planNodeScaleInMock.mockResolvedValueOnce({
    generated_at: "2026-07-01T08:01:00Z",
    state_revision: 45,
    node_id: 4,
    candidates: [{ slot_id: 7, source_node_id: 4, target_node_id: 2, desired_peers: [1, 3, 4], target_peers: [1, 2, 3], config_epoch: 10 }],
    blocked_by_status: false,
  })
  startNodeScaleInMock.mockResolvedValueOnce({ changed: true, node_id: 4, addr: "127.0.0.1:7004", join_state: "leaving", revision: 46 })
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
  getNodeScaleInStatusMock.mockResolvedValueOnce({
    node_id: 4,
    join_state: "leaving",
    generated_at: "2026-07-01T08:01:01Z",
    state_revision: 47,
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
    health_report_age_ms: 100,
    health_report_ttl_ms: 5000,
    observed_control_revision: 47,
    required_control_revision: 47,
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
  })
  advanceNodeScaleInMock.mockResolvedValueOnce({
    generated_at: "2026-07-01T08:01:02Z",
    state_revision: 48,
    node_id: 4,
    created: 1,
    skipped: 0,
    candidates: [{ slot_id: 7, source_node_id: 4, target_node_id: 2, desired_peers: [1, 3, 4], target_peers: [1, 2, 3], config_epoch: 10 }],
  })
  removeNodeAfterScaleInMock.mockResolvedValueOnce({ changed: true, node_id: 4, join_state: "removed", revision: 49 })
  const user = userEvent.setup()

  renderLifecycle("node", leavingNode)

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

  await user.click(screen.getByRole("button", { name: "Remove node" }))
  expect(removeNodeAfterScaleInMock).toHaveBeenCalledWith(4)
  expect(await screen.findByText("Removed revision: 49")).toBeInTheDocument()
})
```

- [ ] **Step 2: Run scale-in test and verify failure**

Run:

```bash
cd web && ./node_modules/.bin/vitest run src/pages/nodes/dynamic-node-lifecycle.test.tsx -t "scale-in stages"
```

Expected: FAIL because scale-in controls are not implemented.

- [ ] **Step 3: Add scale-in state and handlers**

Before changing the component, replace the existing scale-in API functions in `web/src/lib/manager-api.ts` with the current internalv2 shapes:

```ts
export function planNodeScaleIn(nodeId: number, input: ManagerNodeScaleInActionInput = {}) {
  return jsonManagerFetch<ManagerNodeScaleInPlanResponse>(`/manager/nodes/${nodeId}/scale-in/plan`, {
    method: "POST",
    body: buildNodeLifecycleMoveBody(input),
  })
}

export function startNodeScaleIn(nodeId: number) {
  return jsonManagerFetch<ManagerNodeScaleInStartResponse>(`/manager/nodes/${nodeId}/scale-in/start`, {
    method: "POST",
  })
}

export function getNodeScaleInStatus(nodeId: number) {
  return jsonManagerFetch<ManagerNodeScaleInStatusResponse>(`/manager/nodes/${nodeId}/scale-in/status`)
}

export function advanceNodeScaleIn(nodeId: number, input: ManagerNodeScaleInActionInput = {}) {
  return jsonManagerFetch<ManagerNodeScaleInAdvanceResponse>(`/manager/nodes/${nodeId}/scale-in/advance`, {
    method: "POST",
    body: buildNodeLifecycleMoveBody(input),
  })
}
```

In `web/src/lib/manager-api.test.ts`, replace the old scale-in plan/start/status/advance/cancel test with this current stage test:

```ts
it("calls current node scale-in stage endpoints", async () => {
  const plan = {
    generated_at: "2026-07-01T08:01:00Z",
    state_revision: 45,
    node_id: 4,
    candidates: [{ slot_id: 7, source_node_id: 4, target_node_id: 2, desired_peers: [1, 3, 4], target_peers: [1, 2, 3], config_epoch: 10 }],
    blocked_by_status: false,
  }
  const start = { changed: true, node_id: 4, addr: "127.0.0.1:7004", join_state: "leaving", revision: 46 }
  const status = {
    node_id: 4,
    join_state: "leaving",
    generated_at: "2026-07-01T08:01:01Z",
    state_revision: 47,
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
    health_report_age_ms: 100,
    health_report_ttl_ms: 5000,
    observed_control_revision: 47,
    required_control_revision: 47,
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
  const advance = { generated_at: "2026-07-01T08:01:02Z", state_revision: 48, node_id: 4, created: 1, skipped: 0, candidates: plan.candidates }

  fetchMock
    .mockResolvedValueOnce(new Response(JSON.stringify(plan), { status: 200 }))
    .mockResolvedValueOnce(new Response(JSON.stringify(start), { status: 202 }))
    .mockResolvedValueOnce(new Response(JSON.stringify(status), { status: 200 }))
    .mockResolvedValueOnce(new Response(JSON.stringify(advance), { status: 202 }))

  await expect(planNodeScaleIn(4, { maxSlotMoves: 1 })).resolves.toEqual(plan)
  await expect(startNodeScaleIn(4)).resolves.toEqual(start)
  await expect(getNodeScaleInStatus(4)).resolves.toEqual(status)
  await expect(advanceNodeScaleIn(4, { maxSlotMoves: 1 })).resolves.toEqual(advance)

  expect(fetchMock.mock.calls.map((call) => call[0])).toEqual([
    "/manager/nodes/4/scale-in/plan",
    "/manager/nodes/4/scale-in/start",
    "/manager/nodes/4/scale-in/status",
    "/manager/nodes/4/scale-in/advance",
  ])
  expect(JSON.parse((fetchMock.mock.calls[0]?.[1] as { body: string }).body)).toEqual({ max_slot_moves: 1 })
  expect(JSON.parse((fetchMock.mock.calls[3]?.[1] as { body: string }).body)).toEqual({ max_slot_moves: 1 })
})
```

Run:

```bash
cd web && ./node_modules/.bin/vitest run src/lib/manager-api.test.ts -t "current node scale-in stage endpoints"
```

Expected: PASS for the new stage endpoint test.

Extend `web/src/pages/nodes/dynamic-node-lifecycle.tsx` imports:

```ts
import {
  activateNode,
  advanceNodeOnboarding,
  advanceNodeScaleIn,
  getNodeOnboardingStatus,
  getNodeScaleInStatus,
  joinNode,
  planNodeOnboarding,
  planNodeScaleIn,
  removeNodeAfterScaleIn,
  setNodeScaleInDrain,
  startNodeOnboarding,
  startNodeScaleIn,
} from "@/lib/manager-api"
```

Add scale-in response types to the type import:

```ts
ManagerNodeScaleInAdvanceResponse,
ManagerNodeScaleInDrainResponse,
ManagerNodeScaleInPlanResponse,
ManagerNodeScaleInRemoveResponse,
ManagerNodeScaleInStartResponse,
ManagerNodeScaleInStatusResponse,
```

Add state:

```ts
const [scaleInPlan, setScaleInPlan] = useState<ManagerNodeScaleInPlanResponse | null>(null)
const [scaleInStart, setScaleInStart] = useState<ManagerNodeScaleInStartResponse | null>(null)
const [scaleInDrain, setScaleInDrain] = useState<ManagerNodeScaleInDrainResponse | null>(null)
const [scaleInStatus, setScaleInStatus] = useState<ManagerNodeScaleInStatusResponse | null>(null)
const [scaleInAdvance, setScaleInAdvance] = useState<ManagerNodeScaleInAdvanceResponse | null>(null)
const [scaleInRemove, setScaleInRemove] = useState<ManagerNodeScaleInRemoveResponse | null>(null)
```

Add handlers:

```ts
const runScaleInPlan = useCallback(async () => {
  if (!node) {
    return
  }
  setPending(true)
  setError("")
  try {
    const result = await planNodeScaleIn(node.node_id, boundedMovesInput())
    setScaleInPlan(result)
  } catch (err) {
    setError(err instanceof Error ? err.message : "node scale-in plan failed")
  } finally {
    setPending(false)
  }
}, [boundedMovesInput, node])

const runScaleInStart = useCallback(async () => {
  if (!node) {
    return
  }
  setPending(true)
  setError("")
  try {
    const result = await startNodeScaleIn(node.node_id)
    setScaleInStart(result)
    onCompleted()
  } catch (err) {
    setError(err instanceof Error ? err.message : "node scale-in start failed")
  } finally {
    setPending(false)
  }
}, [node, onCompleted])

const enableDrainMode = useCallback(async () => {
  if (!node) {
    return
  }
  setPending(true)
  setError("")
  try {
    const result = await setNodeScaleInDrain(node.node_id, { draining: true })
    setScaleInDrain(result)
    onCompleted()
  } catch (err) {
    setError(err instanceof Error ? err.message : "node drain mode failed")
  } finally {
    setPending(false)
  }
}, [node, onCompleted])

const refreshScaleInStatus = useCallback(async () => {
  if (!node) {
    return
  }
  setPending(true)
  setError("")
  try {
    const result = await getNodeScaleInStatus(node.node_id)
    setScaleInStatus(result)
  } catch (err) {
    setError(err instanceof Error ? err.message : "node scale-in status failed")
  } finally {
    setPending(false)
  }
}, [node])

const runScaleInAdvance = useCallback(async () => {
  if (!node) {
    return
  }
  setPending(true)
  setError("")
  try {
    const result = await advanceNodeScaleIn(node.node_id, boundedMovesInput())
    setScaleInAdvance(result)
    onCompleted()
  } catch (err) {
    setError(err instanceof Error ? err.message : "node scale-in advance failed")
  } finally {
    setPending(false)
  }
}, [boundedMovesInput, node, onCompleted])

const runScaleInRemove = useCallback(async () => {
  if (!node || scaleInStatus?.safe_to_remove !== true) {
    return
  }
  setPending(true)
  setError("")
  try {
    const result = await removeNodeAfterScaleIn(node.node_id)
    setScaleInRemove(result)
    onCompleted()
  } catch (err) {
    setError(err instanceof Error ? err.message : "node removal failed")
  } finally {
    setPending(false)
  }
}, [node, onCompleted, scaleInStatus?.safe_to_remove])
```

- [ ] **Step 4: Render scale-in controls and safety evidence**

Inside the `mode === "node"` branch, after onboarding, render this block for active or leaving data nodes:

```tsx
{node.membership?.role === "data" && nodeJoinState(node) !== "removed" ? (
  <div className="space-y-3 rounded-lg border border-border bg-muted/20 p-3">
    <div className="flex flex-wrap gap-2">
      <Button disabled={pending} onClick={() => void runScaleInPlan()} size="sm" type="button" variant="outline">Plan scale-in</Button>
      <Button disabled={pending} onClick={() => void runScaleInStart()} size="sm" type="button">Mark leaving</Button>
      <Button disabled={pending} onClick={() => void enableDrainMode()} size="sm" type="button" variant="outline">Enable drain mode</Button>
      <Button disabled={pending} onClick={() => void refreshScaleInStatus()} size="sm" type="button" variant="outline">Refresh scale-in status</Button>
      <Button disabled={pending} onClick={() => void runScaleInAdvance()} size="sm" type="button" variant="outline">Advance scale-in</Button>
      <Button disabled={pending || scaleInStatus?.safe_to_remove !== true} onClick={() => void runScaleInRemove()} size="sm" type="button" variant="destructive">Remove node</Button>
    </div>
    {scaleInPlan ? (
      <div className="space-y-2 text-sm">
        {scaleInPlan.candidates.map((candidate) => (
          <div className="rounded-md border border-border bg-background px-3 py-2" key={candidate.slot_id}>
            <div className="font-medium text-foreground">Slot {candidate.slot_id}</div>
            <div className="text-muted-foreground">{candidate.source_node_id} -&gt; {candidate.target_node_id}</div>
            <div className="text-muted-foreground">Desired peers: {candidate.desired_peers.join(", ")}</div>
            <div className="text-muted-foreground">Target peers: {candidate.target_peers.join(", ")}</div>
          </div>
        ))}
      </div>
    ) : null}
    {scaleInStart ? (
      <div className="text-sm text-muted-foreground">Join state: {scaleInStart.join_state}</div>
    ) : null}
    {scaleInDrain ? (
      <div className="grid gap-1 text-sm text-muted-foreground md:grid-cols-2">
        <span>Accepting new sessions: {scaleInDrain.accepting_new_sessions ? "yes" : "no"}</span>
        <span>Gateway sessions: {scaleInDrain.gateway_sessions}</span>
        <span>Active online: {scaleInDrain.active_online}</span>
        <span>Closing online: {scaleInDrain.closing_online}</span>
      </div>
    ) : null}
    {scaleInStatus ? (
      <div className="grid gap-1 text-sm text-muted-foreground md:grid-cols-2">
        <span>Safe to proceed: {scaleInStatus.safe_to_proceed ? "yes" : "no"}</span>
        <span>Safe to remove: {scaleInStatus.safe_to_remove ? "yes" : "no"}</span>
        <span>Slot replicas: {scaleInStatus.slot_replica_count}</span>
        <span>Slot leaders: {scaleInStatus.slot_leader_count}</span>
        <span>Active tasks: {scaleInStatus.active_task_count}</span>
        <span>Failed tasks: {scaleInStatus.failed_task_count}</span>
        <span>Channel leaders: {scaleInStatus.channel_leader_count}</span>
        <span>Channel replicas: {scaleInStatus.channel_replica_count}</span>
        <span>Pending activations: {scaleInStatus.pending_activations}</span>
        {scaleInStatus.blocked_reasons.map((reason) => <span key={reason}>{reason}</span>)}
      </div>
    ) : null}
    {scaleInAdvance ? (
      <div className="text-sm text-muted-foreground">Created tasks: {scaleInAdvance.created} / skipped: {scaleInAdvance.skipped}</div>
    ) : null}
    {scaleInRemove ? (
      <div className="text-sm text-muted-foreground">Removed revision: {scaleInRemove.revision}</div>
    ) : null}
  </div>
) : null}
```

- [ ] **Step 5: Remove stale page scale-in and drain/resume code**

In `web/src/pages/nodes/page.tsx`, remove the old node action and old scale-in report UI now that the lifecycle sheet owns these operations. Delete these imports, types, helpers, state variables, callbacks, sheet sections, and confirmation dialogs:

```ts
ConfirmDialog
advanceNodeScaleIn
cancelNodeScaleIn
getNodeScaleInStatus
markNodeDraining
planNodeScaleIn
resumeNode
startNodeScaleIn
ManagerNodeScaleInReport
NodeAction
ScaleInAction
ScaleInConfirmAction
createScaleInPlanInput
isNodeScaleInReport
formatScaleInCheckLabel
canDrainNode
canResumeNode
canScaleInNode
formatScaleInMetricNumber
formatScaleInCheckStatus
formatChannelInventoryMetric
ScaleInReportView
selectedNodeId
pendingAction
actionError
actionPending
scaleInNodeId
scaleInReport
scaleInAction
scaleInError
scaleInConfirmAction
```

Keep the read-only node detail sheet and the new `DynamicNodeLifecycleSheet`. The node row action should open the lifecycle sheet instead of setting `scaleInNodeId` or `pendingAction`.

- [ ] **Step 6: Remove stale page tests for cancel/drain/resume**

In `web/src/pages/nodes/page.test.tsx`, delete tests that assert these stale functions:

```ts
markNodeDrainingMock
resumeNodeMock
cancelNodeScaleInMock
```

Remove their mock declarations and mock exports. Add this integration smoke test instead:

```tsx
test("opens lifecycle sheet from an active node row", async () => {
  getNodesMock.mockResolvedValueOnce({
    generated_at: "2026-07-01T08:00:00Z",
    controller_leader_id: 1,
    total: 1,
    items: [nodeRow],
  })

  const user = userEvent.setup()
  renderNodesPage()

  expect(await screen.findByText("127.0.0.1:7000")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Open lifecycle for node 1" }))

  expect(await screen.findByRole("dialog", { name: "Node lifecycle" })).toBeInTheDocument()
})
```

- [ ] **Step 7: Run scale-in tests and commit**

Run:

```bash
cd web && ./node_modules/.bin/vitest run src/lib/manager-api.test.ts src/pages/nodes/dynamic-node-lifecycle.test.tsx src/pages/nodes/page.test.tsx -t "current node scale-in stage endpoints|scale-in stages|opens lifecycle sheet"
cd web && ./node_modules/.bin/tsc -b
git add web/src/lib/manager-api.ts web/src/lib/manager-api.test.ts web/src/pages/nodes/dynamic-node-lifecycle.tsx web/src/pages/nodes/dynamic-node-lifecycle.test.tsx web/src/pages/nodes/page.tsx web/src/pages/nodes/page.test.tsx
git commit -m "feat(web): add staged node scale-in flow"
```

Expected: selected tests pass, TypeScript passes, and commit succeeds.

## Task 5: Dynamic Node Diagnostics Read-Only Evidence

**Files:**
- Modify: `web/src/pages/nodes/dynamic-node-lifecycle.tsx`
- Modify: `web/src/pages/nodes/dynamic-node-lifecycle.test.tsx`

- [ ] **Step 1: Add failing diagnostics test**

Append this test to `web/src/pages/nodes/dynamic-node-lifecycle.test.tsx`:

```tsx
test("loads read-only dynamic node diagnostics evidence", async () => {
  getDynamicNodeDiagnosticsMock.mockResolvedValueOnce({
    generated_at: "2026-07-01T08:02:00Z",
    state_revision: 49,
    node_id: 4,
    node: activeNode,
    scale_in: null,
    onboarding: null,
    active_tasks: [{
      task_id: "task-1",
      slot_id: 7,
      kind: "slot_replica_move",
      step: "remove_voter",
      status: "running",
      source_node: 4,
      target_node: 2,
      target_peers: [1, 2, 3],
      completion_policy: "all",
      config_epoch: 10,
      attempt: 1,
      last_error: "",
      participants: [],
    }],
    task_audits: [],
    slots: [{ slot_id: 7, desired_peers: [1, 3, 4], preferred_leader: 1, config_epoch: 10, task_id: "task-1", task_kind: "slot_replica_move", task_step: "remove_voter", task_status: "running", current_leader: 1, current_voters: [1, 3, 4] }],
    summary: {
      safe_to_remove: false,
      blocked_reasons: ["blocked_by_tasks"],
      active_task_count: 1,
      failed_task_count: 0,
      slot_replica_count: 1,
      slot_leader_count: 0,
      control_revision_gap: 0,
      slot_replica_move_state: "running",
      oldest_task_age_seconds: 12,
      audit_available: true,
      runtime_unknown: false,
      slot_runtime_unknown: false,
      recommended_next_action: "inspect_active_tasks",
      blocked_by_control_revision: false,
      blocked_by_slots: true,
      blocked_by_tasks: true,
    },
    sources: {
      control_snapshot: { available: true, last_error: "" },
      task_audit: { available: true, last_error: "" },
      slot_runtime: { available: true, last_error: "" },
    },
    warnings: ["slot runtime evidence is bounded"],
  })
  const user = userEvent.setup()

  renderLifecycle("node", activeNode)

  await user.click(screen.getByRole("button", { name: "Diagnostics" }))

  expect(getDynamicNodeDiagnosticsMock).toHaveBeenCalledWith(4, { taskLimit: 10, auditLimit: 10, slotLimit: 20 })
  expect(await screen.findByText("Recommended next action: inspect_active_tasks")).toBeInTheDocument()
  expect(screen.getByText("blocked_by_tasks")).toBeInTheDocument()
  expect(screen.getByText("task-1")).toBeInTheDocument()
  expect(screen.getByText("slot runtime evidence is bounded")).toBeInTheDocument()
})
```

- [ ] **Step 2: Run diagnostics test and verify failure**

Run:

```bash
cd web && ./node_modules/.bin/vitest run src/pages/nodes/dynamic-node-lifecycle.test.tsx -t "dynamic node diagnostics"
```

Expected: FAIL because diagnostics loading is not implemented.

- [ ] **Step 3: Add diagnostics state and handler**

Extend imports in `web/src/pages/nodes/dynamic-node-lifecycle.tsx`:

```ts
import {
  activateNode,
  advanceNodeOnboarding,
  advanceNodeScaleIn,
  getDynamicNodeDiagnostics,
  getNodeOnboardingStatus,
  getNodeScaleInStatus,
  joinNode,
  planNodeOnboarding,
  planNodeScaleIn,
  removeNodeAfterScaleIn,
  setNodeScaleInDrain,
  startNodeOnboarding,
  startNodeScaleIn,
} from "@/lib/manager-api"
```

Add `ManagerDynamicNodeDiagnosticsResponse` to the type import and add state:

```ts
const [diagnostics, setDiagnostics] = useState<ManagerDynamicNodeDiagnosticsResponse | null>(null)
```

Add this handler:

```ts
const loadDiagnostics = useCallback(async () => {
  if (!node) {
    return
  }
  setPending(true)
  setError("")
  try {
    const result = await getDynamicNodeDiagnostics(node.node_id, { taskLimit: 10, auditLimit: 10, slotLimit: 20 })
    setDiagnostics(result)
  } catch (err) {
    setError(err instanceof Error ? err.message : "dynamic node diagnostics failed")
  } finally {
    setPending(false)
  }
}, [node])
```

Change the diagnostics button to call it:

```tsx
<Button disabled={pending} onClick={() => void loadDiagnostics()} size="sm" type="button" variant="outline">
  Diagnostics
</Button>
```

- [ ] **Step 4: Render diagnostics evidence**

After the diagnostics button, add:

```tsx
{diagnostics ? (
  <div className="space-y-3 rounded-lg border border-border bg-muted/20 p-3 text-sm">
    <div className="font-medium text-foreground">Recommended next action: {diagnostics.summary.recommended_next_action}</div>
    <div className="grid gap-1 text-muted-foreground md:grid-cols-2">
      <span>Safe to remove: {diagnostics.summary.safe_to_remove ? "yes" : "no"}</span>
      <span>Active tasks: {diagnostics.summary.active_task_count}</span>
      <span>Failed tasks: {diagnostics.summary.failed_task_count}</span>
      <span>Slot replicas: {diagnostics.summary.slot_replica_count}</span>
      <span>Slot leaders: {diagnostics.summary.slot_leader_count}</span>
      <span>Oldest task age: {diagnostics.summary.oldest_task_age_seconds}s</span>
    </div>
    {diagnostics.summary.blocked_reasons.map((reason) => (
      <div className="text-muted-foreground" key={reason}>{reason}</div>
    ))}
    <div className="grid gap-1 text-muted-foreground md:grid-cols-3">
      <span>Control snapshot: {diagnostics.sources.control_snapshot.available ? "available" : diagnostics.sources.control_snapshot.last_error}</span>
      <span>Task audit: {diagnostics.sources.task_audit.available ? "available" : diagnostics.sources.task_audit.last_error}</span>
      <span>Slot runtime: {diagnostics.sources.slot_runtime.available ? "available" : diagnostics.sources.slot_runtime.last_error}</span>
    </div>
    {diagnostics.active_tasks.map((task) => (
      <div className="rounded-md border border-border bg-background px-3 py-2" key={task.task_id}>
        <div className="font-medium text-foreground">{task.task_id}</div>
        <div className="text-muted-foreground">{task.kind} / {task.step} / {task.status}</div>
      </div>
    ))}
    {diagnostics.slots.map((slot) => (
      <div className="text-muted-foreground" key={slot.slot_id}>Slot {slot.slot_id}: {slot.desired_peers.join(", ")}</div>
    ))}
    {diagnostics.warnings.map((warning) => (
      <div className="text-muted-foreground" key={warning}>{warning}</div>
    ))}
  </div>
) : null}
```

- [ ] **Step 5: Run diagnostics tests and commit**

Run:

```bash
cd web && ./node_modules/.bin/vitest run src/pages/nodes/dynamic-node-lifecycle.test.tsx -t "dynamic node diagnostics"
cd web && ./node_modules/.bin/tsc -b
git add web/src/pages/nodes/dynamic-node-lifecycle.tsx web/src/pages/nodes/dynamic-node-lifecycle.test.tsx
git commit -m "feat(web): show dynamic node diagnostics evidence"
```

Expected: selected test passes, TypeScript passes, and commit succeeds.

## Task 6: Remove Stale Standalone Onboarding Surface And Polish Copy

**Files:**
- Modify: `web/src/app/router.tsx`
- Modify: `web/src/lib/navigation.ts`
- Modify: `web/src/lib/manager-api.ts`
- Modify: `web/src/lib/manager-api.types.ts`
- Modify: `web/src/lib/manager-api.test.ts`
- Delete: `web/src/pages/onboarding/page.tsx`
- Delete: `web/src/pages/onboarding/page.test.tsx`
- Modify: `web/src/pages/nodes/page.tsx`
- Modify: `web/src/pages/nodes/page.test.tsx`
- Modify: `web/src/pages/page-shells.test.tsx`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`
- Modify: `web/README.md`

- [ ] **Step 1: Add stale-contract guard test**

In `web/src/lib/manager-api.test.ts`, add this test after the dynamic lifecycle API test:

```ts
it("does not expose stale node onboarding or scale-in cancel endpoints", async () => {
  const api = await import("@/lib/manager-api")

  expect("getNodeOnboardingCandidates" in api).toBe(false)
  expect("createNodeOnboardingPlan" in api).toBe(false)
  expect("startNodeOnboardingJob" in api).toBe(false)
  expect("getNodeOnboardingJobs" in api).toBe(false)
  expect("getNodeOnboardingJob" in api).toBe(false)
  expect("retryNodeOnboardingJob" in api).toBe(false)
  expect("markNodeDraining" in api).toBe(false)
  expect("resumeNode" in api).toBe(false)
  expect("cancelNodeScaleIn" in api).toBe(false)
})
```

- [ ] **Step 2: Remove stale onboarding files, routes, and API exports**

Run:

```bash
git rm web/src/pages/onboarding/page.tsx web/src/pages/onboarding/page.test.tsx
```

In `web/src/pages/nodes/page.tsx`, confirm this import is gone:

```ts
import { NodeOnboardingPanel } from "@/pages/onboarding/page"
```

Confirm there is no old render block:

```tsx
{showOnboardingPanel ? <NodeOnboardingPanel /> : null}
```

In `web/src/app/router.tsx`, change the legacy `/onboarding` redirect to the node list:

```tsx
{ path: "onboarding", element: <Navigate replace to="/cluster/nodes" /> },
```

In `web/src/lib/navigation.ts`, change the legacy path mapping so `/onboarding` resolves to the node list without query state:

```ts
"/onboarding": "/cluster/nodes",
```

In `web/src/lib/manager-api.ts`, remove the old helpers and exports:

```ts
buildNodeOnboardingJobsPath
markNodeDraining
resumeNode
cancelNodeScaleIn
getNodeOnboardingCandidates
createNodeOnboardingPlan
startNodeOnboardingJob
getNodeOnboardingJobs
getNodeOnboardingJob
retryNodeOnboardingJob
```

In `web/src/lib/manager-api.types.ts`, remove the old standalone onboarding and old scale-in report DTOs:

```ts
ManagerNodeScaleInReport
ManagerNodeScaleInChecks
ManagerNodeScaleInProgress
ManagerNodeScaleInRuntime
ManagerNodeScaleInBlockedReason
ManagerNodeScaleInLeader
ManagerNodeOnboardingCandidate
ManagerNodeOnboardingCandidatesResponse
ManagerNodeOnboardingPlanSummary
ManagerNodeOnboardingPlanMove
ManagerNodeOnboardingBlockedReason
ManagerNodeOnboardingPlan
ManagerNodeOnboardingMove
ManagerNodeOnboardingResultCounts
ManagerNodeOnboardingJob
ManagerNodeOnboardingJobsResponse
CreateNodeOnboardingPlanInput
NodeOnboardingJobsParams
```

In `web/src/lib/manager-api.test.ts`, remove the old imports and tests for `markNodeDraining`, `resumeNode`, `cancelNodeScaleIn`, and `/manager/node-onboarding/*`. Keep task-domain `node_onboarding` fixture values where they describe controller task domains instead of stale web endpoints.

In `web/src/pages/page-shells.test.tsx`, remove `getNodeOnboardingCandidatesMock`, `getNodeOnboardingJobsMock`, their `vi.mock` exports, and their setup values. If a shell test visits `/onboarding`, change the expected destination to `/cluster/nodes`.

- [ ] **Step 3: Update lifecycle copy in English**

In `web/src/i18n/messages/en.ts`, add or replace these messages:

```ts
"nodes.lifecycle.addNode": "Add node",
"nodes.lifecycle.openForNode": "Open lifecycle for node {id}",
"nodes.lifecycle.title": "Node lifecycle",
"nodes.lifecycle.join": "Join node",
"nodes.lifecycle.activate": "Activate node",
"nodes.lifecycle.planOnboarding": "Plan slot onboarding",
"nodes.lifecycle.startOnboarding": "Start onboarding",
"nodes.lifecycle.refreshOnboarding": "Refresh onboarding status",
"nodes.lifecycle.advanceOnboarding": "Advance onboarding",
"nodes.lifecycle.planScaleIn": "Plan scale-in",
"nodes.lifecycle.markLeaving": "Mark leaving",
"nodes.lifecycle.enableDrain": "Enable drain mode",
"nodes.lifecycle.refreshScaleIn": "Refresh scale-in status",
"nodes.lifecycle.advanceScaleIn": "Advance scale-in",
"nodes.lifecycle.remove": "Remove node",
"nodes.lifecycle.diagnostics": "Diagnostics",
"nodes.lifecycle.drainCopy": "Drain mode disables new gateway admission and does not close existing sessions.",
```

- [ ] **Step 4: Update lifecycle copy in Chinese**

In `web/src/i18n/messages/zh-CN.ts`, add or replace these messages:

```ts
"nodes.lifecycle.addNode": "添加节点",
"nodes.lifecycle.openForNode": "打开节点 {id} 生命周期",
"nodes.lifecycle.title": "节点生命周期",
"nodes.lifecycle.join": "登记节点",
"nodes.lifecycle.activate": "激活节点",
"nodes.lifecycle.planOnboarding": "规划槽位扩容",
"nodes.lifecycle.startOnboarding": "启动扩容",
"nodes.lifecycle.refreshOnboarding": "刷新扩容状态",
"nodes.lifecycle.advanceOnboarding": "推进扩容",
"nodes.lifecycle.planScaleIn": "规划缩容",
"nodes.lifecycle.markLeaving": "标记离开",
"nodes.lifecycle.enableDrain": "启用 Drain",
"nodes.lifecycle.refreshScaleIn": "刷新缩容状态",
"nodes.lifecycle.advanceScaleIn": "推进缩容",
"nodes.lifecycle.remove": "移除节点",
"nodes.lifecycle.diagnostics": "诊断",
"nodes.lifecycle.drainCopy": "Drain 只关闭新会话入口，不会强制关闭已有连接。",
```

- [ ] **Step 5: Replace literal lifecycle labels with i18n messages**

In `web/src/pages/nodes/dynamic-node-lifecycle.tsx`, replace literal button labels with `intl.formatMessage` calls. For example:

```tsx
<Button disabled={pending || !canWriteNodes} onClick={() => void submitJoin()}>
  {intl.formatMessage({ id: "nodes.lifecycle.join" })}
</Button>
```

Use the message IDs from Steps 3 and 4 for every lifecycle button introduced in Tasks 2 through 5.

- [ ] **Step 6: Update README API matrix**

In `web/README.md`, replace the `/cluster/nodes` row with:

```markdown
| `/cluster/nodes` | `GET /manager/nodes`, `GET /manager/nodes/:id`, `POST /manager/nodes/join`, `POST /manager/nodes/:id/activate`, per-node onboarding APIs, per-node scale-in APIs, and `GET /manager/nodes/:id/diagnostics` | Implemented |
```

If the route summary still lists `/onboarding` as a cluster page, change that wording to say `/onboarding` is only a legacy redirect to `/cluster/nodes`.

- [ ] **Step 7: Run stale search and tests**

Run:

```bash
rg -n "node-onboarding|panel=onboarding|cancelNodeScaleIn|markNodeDraining|resumeNode|confirm_statefulset_tail|force_close_connections|expected_tail_node_id" web/src web/README.md
cd web && ./node_modules/.bin/vitest run src/lib/manager-api.test.ts src/pages/nodes/dynamic-node-lifecycle.test.tsx src/pages/nodes/page.test.tsx src/pages/page-shells.test.tsx
cd web && ./node_modules/.bin/tsc -b
```

Expected: the `rg` command returns no matches, Vitest passes, and TypeScript passes.

- [ ] **Step 8: Commit stale cleanup and copy**

Run:

```bash
git add web/src/app/router.tsx web/src/lib/navigation.ts web/src/lib/manager-api.ts web/src/lib/manager-api.types.ts web/src/lib/manager-api.test.ts web/src/pages/nodes/page.tsx web/src/pages/nodes/page.test.tsx web/src/pages/nodes/dynamic-node-lifecycle.tsx web/src/pages/page-shells.test.tsx web/src/i18n/messages/en.ts web/src/i18n/messages/zh-CN.ts web/README.md
git commit -m "feat(web): remove stale node onboarding surface"
```

Expected: commit succeeds and only web lifecycle cleanup files are included.

## Task 7: Final Verification And Review

**Files:**
- Verify: `web/src/app/router.tsx`
- Verify: `web/src/lib/navigation.ts`
- Verify: `web/src/lib/manager-api.ts`
- Verify: `web/src/lib/manager-api.types.ts`
- Verify: `web/src/pages/nodes/dynamic-node-lifecycle.tsx`
- Verify: `web/src/pages/nodes/page.tsx`
- Verify: `web/README.md`

- [ ] **Step 1: Run focused web verification**

Run:

```bash
cd web && ./node_modules/.bin/vitest run src/lib/manager-api.test.ts src/pages/nodes/dynamic-node-lifecycle.test.tsx src/pages/nodes/page.test.tsx src/pages/page-shells.test.tsx
cd web && ./node_modules/.bin/tsc -b
git diff --check
```

Expected: all commands pass.

- [ ] **Step 2: Run stale contract scan**

Run:

```bash
rg -n "node-onboarding|panel=onboarding|scale-in/cancel|/draining|/resume|confirm_statefulset_tail|expected_tail_node_id|force_close_connections" web/src web/README.md
```

Expected: no output.

- [ ] **Step 3: Inspect final diff**

Run:

```bash
git status --short
git diff --stat HEAD
git diff -- web/src/lib/manager-api.ts web/src/lib/manager-api.types.ts web/src/pages/nodes/dynamic-node-lifecycle.tsx web/src/pages/nodes/page.tsx web/README.md
```

Expected: diff only contains current web dynamic node lifecycle implementation, and unrelated Stage 12 documents remain untouched unless they were already dirty before this work.

- [ ] **Step 4: Final commit if any verification-only edits were made**

If Step 1 or Step 2 required edits, run:

```bash
git add web/src/app/router.tsx web/src/lib/navigation.ts web/src/lib/manager-api.ts web/src/lib/manager-api.types.ts web/src/lib/manager-api.test.ts web/src/pages/nodes/dynamic-node-lifecycle.tsx web/src/pages/nodes/dynamic-node-lifecycle.test.tsx web/src/pages/nodes/page.tsx web/src/pages/nodes/page.test.tsx web/src/pages/page-shells.test.tsx web/src/i18n/messages/en.ts web/src/i18n/messages/zh-CN.ts web/README.md
git commit -m "test(web): verify dynamic node lifecycle"
```

Expected: commit succeeds only when verification edits exist. If no verification edits exist, skip this commit.
