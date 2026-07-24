import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import { beforeEach, expect, test, vi } from "vitest"

import { createAnonymousAuthState, useAuthStore } from "@/auth/auth-store"
import { resetLocale } from "@/i18n/locale-store"
import { I18nProvider } from "@/i18n/provider"
import { MCPSettingsPage } from "@/pages/settings/mcp/page"

const getMCPStatusMock = vi.fn()
const getMCPAuditsMock = vi.fn()
const getNodesMock = vi.fn()
const createMCPTokenMock = vi.fn()
const setMCPOwnerMock = vi.fn()
const startMCPMock = vi.fn()
const stopMCPMock = vi.fn()
const revokeMCPTokenMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getMCPStatus: (...args: unknown[]) => getMCPStatusMock(...args),
    getMCPAudits: (...args: unknown[]) => getMCPAuditsMock(...args),
    getNodes: (...args: unknown[]) => getNodesMock(...args),
    createMCPToken: (...args: unknown[]) => createMCPTokenMock(...args),
    setMCPOwner: (...args: unknown[]) => setMCPOwnerMock(...args),
    startMCP: (...args: unknown[]) => startMCPMock(...args),
    stopMCP: (...args: unknown[]) => stopMCPMock(...args),
    revokeMCPToken: (...args: unknown[]) => revokeMCPTokenMock(...args),
  }
})

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  for (const mock of [
    getMCPStatusMock, getMCPAuditsMock, getNodesMock, createMCPTokenMock,
    setMCPOwnerMock, startMCPMock, stopMCPMock, revokeMCPTokenMock,
  ]) {
    mock.mockReset()
  }
  useAuthStore.setState({
    ...createAnonymousAuthState(),
    isHydrated: true,
    status: "authenticated",
    username: "admin",
    tokenType: "Bearer",
    accessToken: "manager-token",
    expiresAt: "2099-04-22T12:00:00Z",
    permissions: [{ resource: "cluster.mcp", actions: ["r", "w"] }],
  })
  getMCPStatusMock.mockResolvedValue({
    cluster_id: "cluster-a", revision: 7, enabled: false, observed_status: "stopped",
    owner_node_id: 0, owner_candidates: [], credentials: [], warnings: [],
  })
  getMCPAuditsMock.mockResolvedValue({ items: [] })
  getNodesMock.mockResolvedValue({
    items: [
      { node_id: 1, name: "node-1", status: "alive" },
      { node_id: 2, name: "node-2", status: "alive" },
    ],
  })
})

function renderPage() {
  return render(
    <I18nProvider>
      <MCPSettingsPage />
    </I18nProvider>,
  )
}

test("configures an owner and displays a generated token exactly once", async () => {
  getMCPStatusMock
    .mockResolvedValueOnce({
      cluster_id: "cluster-a", revision: 7, enabled: false, observed_status: "stopped",
      owner_node_id: 0, owner_candidates: [
        { node_id: 2, status: "alive" },
      ], credentials: [], warnings: [],
    })
    .mockResolvedValue({
      cluster_id: "cluster-a", revision: 8, enabled: false, observed_status: "stopped",
      owner_node_id: 2, owner_candidates: [
        { node_id: 2, status: "alive" },
      ], credentials: [], warnings: [],
    })
  setMCPOwnerMock.mockResolvedValue({ accepted: true })
  createMCPTokenMock.mockResolvedValue({
    credential_id: "credential-a",
    token: "wko_credential-a_one-time-secret",
    created_at_unix_ms: 1710000001000,
    revision: 8,
  })
  const writeText = vi.fn().mockResolvedValue(undefined)
  Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } })

  renderPage()

  expect(await screen.findByRole("heading", { name: "Operations MCP" })).toBeInTheDocument()
  expect(getNodesMock).not.toHaveBeenCalled()
  expect(screen.getByText("Plain HTTP")).toBeInTheDocument()
  fireEvent.change(screen.getByLabelText("Execution owner"), { target: { value: "2" } })
  fireEvent.click(screen.getByRole("button", { name: "Save owner" }))
  await waitFor(() => {
    expect(setMCPOwnerMock).toHaveBeenCalledWith({
      ownerNodeId: 2,
      expectedRevision: 7,
      idempotencyKey: expect.any(String),
    })
  })
  await waitFor(() => {
    expect(screen.getByText("Node 2")).toBeInTheDocument()
  })

  fireEvent.click(screen.getByRole("button", { name: "Generate token" }))
  expect(await screen.findByText("wko_credential-a_one-time-secret")).toBeInTheDocument()
  fireEvent.click(screen.getByRole("button", { name: "Copy token" }))
  expect(writeText).toHaveBeenCalledWith("wko_credential-a_one-time-secret")
})

test("renders desired and observed state with recent audit summaries", async () => {
  getMCPStatusMock.mockResolvedValue({
    cluster_id: "cluster-a", revision: 9, enabled: true, observed_status: "ready",
    owner_node_id: 2,
    owner_candidates: [{ node_id: 2, status: "alive" }],
    credentials: [{ id: "credential-a", created_at_unix_ms: 1710000001000, old: false }],
    warnings: [],
  })
  getMCPAuditsMock.mockResolvedValue({
    items: [{
      request_id: "request-1", phase: "owner", credential_id: "credential-a", tool: "cluster_health",
      result: "ok", started_at: "2026-07-24T09:00:00Z", duration_ms: 12,
      response_bytes: 420, cache_hit: true,
    }],
  })

  renderPage()

  expect(await screen.findByText("Enabled")).toBeInTheDocument()
  expect(screen.getByText("Ready")).toBeInTheDocument()
  expect(screen.getByText("cluster_health")).toBeInTheDocument()
  expect(screen.getByText("request-1")).toBeInTheDocument()
})
