import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeEach, expect, test, vi } from "vitest"

import { createAnonymousAuthState, useAuthStore } from "@/auth/auth-store"
import { I18nProvider } from "@/i18n/provider"
import { resetLocale } from "@/i18n/locale-store"
import { ManagerApiError } from "@/lib/manager-api"
import { OnboardingPage } from "@/pages/onboarding/page"

const getCandidatesMock = vi.fn()
const createPlanMock = vi.fn()
const startJobMock = vi.fn()
const getJobsMock = vi.fn()
const getJobMock = vi.fn()
const retryJobMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getNodeOnboardingCandidates: (...args: unknown[]) => getCandidatesMock(...args),
    createNodeOnboardingPlan: (...args: unknown[]) => createPlanMock(...args),
    startNodeOnboardingJob: (...args: unknown[]) => startJobMock(...args),
    getNodeOnboardingJobs: (...args: unknown[]) => getJobsMock(...args),
    getNodeOnboardingJob: (...args: unknown[]) => getJobMock(...args),
    retryNodeOnboardingJob: (...args: unknown[]) => retryJobMock(...args),
  }
})

const candidate = {
  node_id: 4,
  name: "node-4",
  addr: "127.0.0.1:7004",
  role: "data",
  join_state: "active",
  status: "alive",
  slot_count: 0,
  leader_count: 0,
  recommended: true,
}

const plannedJob = {
  job_id: "onboard-20260426-000001",
  target_node_id: 4,
  retry_of_job_id: "",
  status: "planned",
  created_at: "2026-04-26T12:00:00Z",
  updated_at: "2026-04-26T12:00:00Z",
  started_at: "0001-01-01T00:00:00Z",
  completed_at: "0001-01-01T00:00:00Z",
  plan_version: 1,
  plan_fingerprint: "fp-1",
  plan: {
    target_node_id: 4,
    summary: {
      current_target_slot_count: 0,
      planned_target_slot_count: 1,
      current_target_leader_count: 0,
      planned_leader_gain: 1,
    },
    moves: [{
      slot_id: 2,
      source_node_id: 1,
      target_node_id: 4,
      reason: "underloaded_target",
      desired_peers_before: [1, 2, 3],
      desired_peers_after: [2, 3, 4],
      current_leader_id: 1,
      leader_transfer_required: true,
    }],
    blocked_reasons: [],
  },
  moves: [{
    slot_id: 2,
    source_node_id: 1,
    target_node_id: 4,
    status: "pending",
    task_kind: "rebalance",
    task_slot_id: 2,
    started_at: "0001-01-01T00:00:00Z",
    completed_at: "0001-01-01T00:00:00Z",
    last_error: "",
    desired_peers_before: [1, 2, 3],
    desired_peers_after: [2, 3, 4],
    leader_before: 1,
    leader_after: 0,
    leader_transfer_required: true,
  }],
  current_move_index: -1,
  result_counts: { pending: 1, running: 0, completed: 0, failed: 0, skipped: 0 },
  last_error: "",
}

const runningJob = {
  ...plannedJob,
  status: "running",
  started_at: "2026-04-26T12:01:00Z",
  result_counts: { pending: 0, running: 1, completed: 0, failed: 0, skipped: 0 },
  moves: [{ ...plannedJob.moves[0], status: "running" }],
}

const completedJob = {
  ...runningJob,
  status: "completed",
  completed_at: "2026-04-26T12:02:00Z",
  result_counts: { pending: 0, running: 0, completed: 1, failed: 0, skipped: 0 },
  moves: [{ ...plannedJob.moves[0], status: "completed" }],
}

const failedJob = {
  ...runningJob,
  status: "failed",
  last_error: "leader transfer rejected",
  result_counts: { pending: 0, running: 0, completed: 0, failed: 1, skipped: 0 },
  moves: [{ ...plannedJob.moves[0], status: "failed", last_error: "leader transfer rejected" }],
}

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  vi.useRealTimers()
  getCandidatesMock.mockReset()
  createPlanMock.mockReset()
  startJobMock.mockReset()
  getJobsMock.mockReset()
  getJobMock.mockReset()
  retryJobMock.mockReset()
  getCandidatesMock.mockResolvedValue({ total: 1, items: [candidate] })
  getJobsMock.mockResolvedValue({ items: [], next_cursor: "", has_more: false })
  getJobMock.mockResolvedValue(runningJob)
  useAuthStore.setState({
    ...createAnonymousAuthState(),
    isHydrated: true,
    status: "authenticated",
    username: "admin",
    tokenType: "Bearer",
    accessToken: "token-1",
    expiresAt: "2099-04-22T12:00:00Z",
    permissions: [
      { resource: "cluster.node", actions: ["r"] },
      { resource: "cluster.slot", actions: ["r", "w"] },
    ],
  })
})

function renderOnboardingPage() {
  return render(
    <I18nProvider>
      <OnboardingPage />
    </I18nProvider>,
  )
}

test("creates a reviewed plan and starts onboarding after confirmation", async () => {
  createPlanMock.mockResolvedValueOnce(plannedJob)
  startJobMock.mockResolvedValueOnce(runningJob)

  const user = userEvent.setup()
  renderOnboardingPage()

  expect(await screen.findByText("Node 4")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Review plan for node 4" }))

  expect(createPlanMock).toHaveBeenCalledWith({ targetNodeId: 4 })
  expect(await screen.findByText("Slot 2")).toBeInTheDocument()
  expect(screen.getByText("1 -> 4")).toBeInTheDocument()

  await user.click(screen.getByRole("button", { name: "Start onboarding" }))
  await user.click(screen.getByRole("button", { name: "Confirm" }))

  expect(startJobMock).toHaveBeenCalledWith("onboard-20260426-000001")
  expect(await screen.findAllByText("running")).not.toHaveLength(0)
})

test("polls a running job until it reaches a terminal state", async () => {
  createPlanMock.mockResolvedValueOnce(plannedJob)
  startJobMock.mockResolvedValueOnce(runningJob)
  getJobMock.mockResolvedValueOnce(completedJob)

  const user = userEvent.setup()
  renderOnboardingPage()

  expect(await screen.findByText("Node 4")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Review plan for node 4" }))
  await user.click(await screen.findByRole("button", { name: "Start onboarding" }))
  await user.click(screen.getByRole("button", { name: "Confirm" }))

  expect(getJobMock).toHaveBeenCalledWith("onboard-20260426-000001")
  expect(await screen.findAllByText("completed")).not.toHaveLength(0)
})

test("retries a failed onboarding job from job history", async () => {
  getJobsMock.mockResolvedValueOnce({ items: [failedJob], next_cursor: "", has_more: false })
  retryJobMock.mockResolvedValueOnce(plannedJob)

  const user = userEvent.setup()
  renderOnboardingPage()

  expect(await screen.findByText("leader transfer rejected")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Retry job onboard-20260426-000001" }))

  expect(retryJobMock).toHaveBeenCalledWith("onboard-20260426-000001")
  expect(await screen.findByText("Plan Review")).toBeInTheDocument()
})

test("disables write actions without slot write permission", async () => {
  useAuthStore.setState({
    permissions: [
      { resource: "cluster.node", actions: ["r"] },
      { resource: "cluster.slot", actions: ["r"] },
    ],
  })

  renderOnboardingPage()

  const reviewButton = await screen.findByRole("button", { name: "Review plan for node 4" })
  expect(reviewButton).toBeDisabled()
  expect(screen.getByText("Requires cluster.slot write permission.")).toBeInTheDocument()
})

test("shows unavailable state when candidate loading fails", async () => {
  getCandidatesMock.mockRejectedValueOnce(
    new ManagerApiError(503, "service_unavailable", "node onboarding unavailable"),
  )

  renderOnboardingPage()

  expect(await screen.findByText(/currently unavailable/i)).toBeInTheDocument()
})
