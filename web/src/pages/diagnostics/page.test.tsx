import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { MemoryRouter } from "react-router-dom"
import { beforeEach, expect, test, vi } from "vitest"

import { AppProviders } from "@/app/providers"
import { ManagerApiError } from "@/lib/manager-api"
import type { ManagerDiagnosticsResponse } from "@/lib/manager-api.types"
import { DiagnosticsPage } from "@/pages/diagnostics/page"

const getDiagnosticsTraceMock = vi.fn()
const getDiagnosticsMessageMock = vi.fn()
const getDiagnosticsEventsMock = vi.fn()
const listDiagnosticsTrackingRulesMock = vi.fn()
const createDiagnosticsTrackingRuleMock = vi.fn()
const deleteDiagnosticsTrackingRuleMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getDiagnosticsTrace: (...args: unknown[]) => getDiagnosticsTraceMock(...args),
    getDiagnosticsMessage: (...args: unknown[]) => getDiagnosticsMessageMock(...args),
    getDiagnosticsEvents: (...args: unknown[]) => getDiagnosticsEventsMock(...args),
    listDiagnosticsTrackingRules: (...args: unknown[]) => listDiagnosticsTrackingRulesMock(...args),
    createDiagnosticsTrackingRule: (...args: unknown[]) => createDiagnosticsTrackingRuleMock(...args),
    deleteDiagnosticsTrackingRule: (...args: unknown[]) => deleteDiagnosticsTrackingRuleMock(...args),
  }
})

function renderPage() {
  return render(
    <AppProviders>
      <MemoryRouter>
        <DiagnosticsPage />
      </MemoryRouter>
    </AppProviders>,
  )
}

function diagnosticsResponse(overrides: Partial<ManagerDiagnosticsResponse> = {}): ManagerDiagnosticsResponse {
  return {
    scope: "cluster",
    status: "ok",
    generated_at: "2026-05-06T08:00:00Z",
    query: { trace_id: "tr-1" },
    summary: {
      first_failure_stage: "store_append",
      first_failure_result: "error",
      first_failure_error_code: "E_APPEND",
      slowest_stage: "raft_commit",
      slowest_duration_ms: 42,
      involved_nodes: [1, 2],
      peer_nodes: [2],
      slot_id: 9,
      channel_key: "unsafe-channel-key",
      client_msg_no: "c-1",
      message_seq: 7,
      event_count: 2,
    },
    nodes: [
      { node_id: 1, status: "ok", duration_ms: 12, event_count: 1, notes: [] },
      { node_id: 2, status: "ok", duration_ms: 42, event_count: 1, notes: [] },
    ],
    events: [
      {
        trace_id: "tr-1",
        stage: "gateway_accept",
        at: "2026-05-06T08:00:00Z",
        duration_ms: 5,
        node_id: 1,
        result: "ok",
        channel_key: "unsafe-channel-key",
      },
      {
        trace_id: "tr-1",
        stage: "store_append",
        at: "2026-05-06T08:00:01Z",
        duration_ms: 42,
        node_id: 2,
        slot_id: 9,
        result: "error",
        error_code: "E_APPEND",
        error: "append failed",
      },
    ],
    notes: ["sampled trace"],
    ...overrides,
  }
}

beforeEach(() => {
  getDiagnosticsTraceMock.mockReset()
  getDiagnosticsMessageMock.mockReset()
  getDiagnosticsEventsMock.mockReset()
  listDiagnosticsTrackingRulesMock.mockReset()
  createDiagnosticsTrackingRuleMock.mockReset()
  deleteDiagnosticsTrackingRuleMock.mockReset()
  listDiagnosticsTrackingRulesMock.mockResolvedValue({ status: "ok", rules: [], nodes: [], notes: [] })
})

test("creates a sender uid tracking rule", async () => {
  const user = userEvent.setup()
  createDiagnosticsTrackingRuleMock.mockResolvedValue({
    status: "ok",
    rule: { rule_id: "rule-1", target: "sender_uid", uid: "u1", sample_rate: 1, expires_at: "2026-05-14T10:00:00Z" },
    nodes: [{ node_id: 1, status: "ok", notes: [] }],
    notes: [],
  })

  renderPage()

  await user.selectOptions(await screen.findByLabelText("Tracking target"), "sender_uid")
  await user.type(screen.getByLabelText("Sender UID"), "u1")
  await user.selectOptions(screen.getByLabelText("TTL"), "3600")
  await user.click(screen.getByRole("button", { name: "Start tracking" }))

  await waitFor(() => expect(createDiagnosticsTrackingRuleMock).toHaveBeenCalledWith({ target: "sender_uid", uid: "u1", ttlSeconds: 3600, sampleRate: 1 }))
  expect(await screen.findByText("u1")).toBeInTheDocument()
})

test("creates a channel tracking rule", async () => {
  const user = userEvent.setup()
  createDiagnosticsTrackingRuleMock.mockResolvedValue({
    status: "ok",
    rule: { rule_id: "rule-2", target: "channel", channel_key: "channel/2/ZzE", channel_id: "g1", channel_type: 2, sample_rate: 1 },
    nodes: [],
    notes: [],
  })

  renderPage()

  await user.selectOptions(await screen.findByLabelText("Tracking target"), "channel")
  await user.type(screen.getByLabelText("Channel ID"), "g1")
  await user.type(screen.getByLabelText("Channel Type"), "2")
  await user.click(screen.getByRole("button", { name: "Start tracking" }))

  await waitFor(() => expect(createDiagnosticsTrackingRuleMock).toHaveBeenCalledWith({ target: "channel", channelId: "g1", channelType: 2, ttlSeconds: 3600, sampleRate: 1 }))
})

test("queries recent events from a tracking rule", async () => {
  const user = userEvent.setup()
  listDiagnosticsTrackingRulesMock.mockResolvedValue({
    status: "ok",
    rules: [{ rule_id: "rule-1", target: "sender_uid", uid: "u1", sample_rate: 1 }],
    nodes: [],
    notes: [],
  })
  getDiagnosticsEventsMock.mockResolvedValue(diagnosticsResponse())

  renderPage()

  await user.click(await screen.findByRole("button", { name: "Query recent events" }))

  await waitFor(() => expect(getDiagnosticsEventsMock).toHaveBeenCalledWith({ uid: "u1", limit: 100 }))
})

test("deletes a tracking rule", async () => {
  const user = userEvent.setup()
  listDiagnosticsTrackingRulesMock
    .mockResolvedValueOnce({
      status: "ok",
      rules: [{ rule_id: "rule-1", target: "sender_uid", uid: "u1", sample_rate: 1 }],
      nodes: [],
      notes: [],
    })
    .mockResolvedValueOnce({ status: "ok", rules: [], nodes: [], notes: [] })
  deleteDiagnosticsTrackingRuleMock.mockResolvedValue({ status: "ok", rule_id: "rule-1", nodes: [], notes: [] })

  renderPage()

  await user.click(await screen.findByRole("button", { name: "Stop tracking" }))

  await waitFor(() => expect(deleteDiagnosticsTrackingRuleMock).toHaveBeenCalledWith("rule-1"))
})

test("runs a trace query and renders the summary", async () => {
  const user = userEvent.setup()
  const response = diagnosticsResponse()
  getDiagnosticsTraceMock.mockResolvedValue(response)

  renderPage()

  await user.type(screen.getByLabelText("Trace ID"), "tr-1")
  await user.click(screen.getByRole("button", { name: "Run diagnostics" }))

  await waitFor(() => expect(getDiagnosticsTraceMock).toHaveBeenCalledWith("tr-1", { limit: 100 }))
  expect(await screen.findByText("Status")).toBeInTheDocument()
  expect(screen.getAllByText("ok").length).toBeGreaterThan(0)
  expect(screen.getAllByText("2 events").length).toBeGreaterThan(0)
  expect(screen.getAllByText("store_append").length).toBeGreaterThan(0)
})

test("validates channel sequence input", async () => {
  const user = userEvent.setup()
  renderPage()

  await user.selectOptions(screen.getByLabelText("Query mode"), "channel_seq")
  await user.type(screen.getByLabelText("Message Seq"), "0")
  await user.click(screen.getByRole("button", { name: "Run diagnostics" }))

  expect(getDiagnosticsMessageMock).not.toHaveBeenCalled()
  expect(screen.getByText("Channel key is required."))
  expect(screen.getByText("Message sequence must be a positive integer."))
})

test("renders partial node results", async () => {
  const user = userEvent.setup()
  getDiagnosticsTraceMock.mockResolvedValue(diagnosticsResponse({
    status: "partial",
    nodes: [
      { node_id: 1, status: "ok", duration_ms: 8, event_count: 1, notes: [] },
      { node_id: 3, status: "unavailable", duration_ms: 0, event_count: 0, notes: ["diagnostics RPC timeout"] },
      { node_id: 4, status: "skipped", duration_ms: 0, event_count: 0, notes: ["node is dead"] },
    ],
    notes: ["results are incomplete"],
  }))

  renderPage()

  await user.type(screen.getByLabelText("Trace ID"), "tr-1")
  await user.click(screen.getByRole("button", { name: "Run diagnostics" }))

  expect(await screen.findByText("Partial diagnostics result"))
  expect(screen.getByText("diagnostics RPC timeout"))
  expect(screen.getByText("node is dead"))
})

test("renders not_found empty state", async () => {
  const user = userEvent.setup()
  getDiagnosticsTraceMock.mockResolvedValue(diagnosticsResponse({
    status: "not_found",
    summary: { involved_nodes: [], peer_nodes: [], event_count: 0 },
    events: [],
    nodes: [{ node_id: 1, status: "not_found", duration_ms: 2, event_count: 0, notes: ["store disabled"] }],
    notes: ["store disabled"],
  }))

  renderPage()

  await user.type(screen.getByLabelText("Trace ID"), "missing")
  await user.click(screen.getByRole("button", { name: "Run diagnostics" }))

  expect(await screen.findByText("No diagnostics events found"))
  expect(screen.getByText(/sampling missed the event/i))
})

test("renders forbidden state from ManagerApiError", async () => {
  const user = userEvent.setup()
  getDiagnosticsTraceMock.mockRejectedValue(new ManagerApiError(403, "forbidden", "forbidden"))

  renderPage()

  await user.type(screen.getByLabelText("Trace ID"), "tr-1")
  await user.click(screen.getByRole("button", { name: "Run diagnostics" }))

  expect(await screen.findByText("Diagnostics permission required"))
  expect(screen.getByText(/cluster\.diagnostics:r/))
})

test("builds related slot log and connection links", async () => {
  const user = userEvent.setup()
  getDiagnosticsTraceMock.mockResolvedValue(diagnosticsResponse())

  renderPage()

  await user.type(screen.getByLabelText("Trace ID"), "tr-1")
  await user.click(screen.getByRole("button", { name: "Run diagnostics" }))

  expect(await screen.findByRole("link", { name: "Slot 9 logs on node 2" })).toHaveAttribute(
    "href",
    "/cluster/slots?tab=logs&slot_id=9&node_id=2",
  )
  expect(screen.getByRole("link", { name: "Connections on node 2" })).toHaveAttribute(
    "href",
    "/business/connections?node_id=2",
  )
  expect(screen.getByRole("link", { name: "Nodes" })).toHaveAttribute("href", "/cluster/nodes")
})

test("exports the current diagnostics JSON", async () => {
  const user = userEvent.setup()
  const response = diagnosticsResponse()
  getDiagnosticsTraceMock.mockResolvedValue(response)
  const writeText = vi.fn().mockResolvedValue(undefined)
  Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } })

  renderPage()

  await user.type(screen.getByLabelText("Trace ID"), "tr-1")
  await user.click(screen.getByRole("button", { name: "Run diagnostics" }))
  await user.click(await screen.findByRole("button", { name: "Export JSON" }))

  expect(writeText).toHaveBeenCalledWith(JSON.stringify(response, null, 2))
})
