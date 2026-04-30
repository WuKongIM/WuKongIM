import { getManagerApiBaseUrl } from "@/lib/env"
import type {
  ChannelRuntimeMetaListParams,
  ManagerChannelRuntimeMetaDetailResponse,
  ManagerChannelRuntimeMetaListResponse,
  ManagerConnectionDetailResponse,
  ManagerConnectionsResponse,
  ManagerLoginResponse,
  ManagerMessagesResponse,
  ManagerNetworkSummaryResponse,
  ManagerNodeOnboardingCandidatesResponse,
  ManagerNodeOnboardingJob,
  ManagerNodeOnboardingJobsResponse,
  ManagerNodeDetailResponse,
  ManagerNodeScaleInReport,
  ManagerNodesResponse,
  ManagerOverviewResponse,
  ManagerPermission,
  ManagerSlotDetailResponse,
  ManagerSlotLogsResponse,
  ManagerSlotRemoveResponse,
  ManagerSlotRecoverResponse,
  ManagerSlotRebalanceResponse,
  ManagerSlotsResponse,
  ManagerTaskDetailResponse,
  ManagerTasksResponse,
  MessageListParams,
  NodeOnboardingJobsParams,
  SlotListParams,
  SlotLogListParams,
  RecoverSlotInput,
  TransferSlotLeaderInput,
  CreateNodeOnboardingPlanInput,
  CreateNodeScaleInPlanInput,
  AdvanceNodeScaleInInput,
} from "@/lib/manager-api.types"

export type ManagerAuthConfig = {
  getAccessToken: () => string
  onUnauthorized: () => void
}

export type ManagerLoginCredentials = {
  username: string
  password: string
}

export type ManagerSession = {
  username: string
  tokenType: string
  accessToken: string
  expiresAt: string
  permissions: ManagerPermission[]
}

type ManagerErrorResponse = {
  error?: string
  message?: string
  report?: unknown
}

export class ManagerApiError extends Error {
  status: number
  error: string
  report?: unknown

  constructor(status: number, error: string, message: string, report?: unknown) {
    super(message)
    this.name = "ManagerApiError"
    this.status = status
    this.error = error
    this.report = report
  }
}

let authConfig: ManagerAuthConfig | null = null

export function configureManagerAuth(config: ManagerAuthConfig) {
  authConfig = config
}

export function resetManagerAuthConfig() {
  authConfig = null
}

function buildManagerUrl(path: string) {
  const base = getManagerApiBaseUrl()
  if (!base) {
    return path
  }

  return `${base}${path.startsWith("/") ? path : `/${path}`}`
}

async function parseManagerError(response: Response) {
  let payload: ManagerErrorResponse | null = null

  try {
    payload = (await response.json()) as ManagerErrorResponse
  } catch {
    payload = null
  }

  return new ManagerApiError(
    response.status,
    payload?.error ?? "request_failed",
    payload?.message ?? `Request failed with status ${response.status}`,
    payload?.report,
  )
}

async function jsonManagerFetch<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await managerFetch(path, init)
  return (await response.json()) as T
}

function buildChannelRuntimeMetaPath(params?: ChannelRuntimeMetaListParams) {
  const search = new URLSearchParams()
  if (typeof params?.limit === "number") {
    search.set("limit", String(params.limit))
  }
  if (params?.cursor) {
    search.set("cursor", params.cursor)
  }

  const query = search.toString()
  return query ? `/manager/channel-runtime-meta?${query}` : "/manager/channel-runtime-meta"
}

function buildNodeOnboardingJobsPath(params?: NodeOnboardingJobsParams) {
  const search = new URLSearchParams()
  if (typeof params?.limit === "number") {
    search.set("limit", String(params.limit))
  }
  if (params?.cursor) {
    search.set("cursor", params.cursor)
  }

  const query = search.toString()
  return query ? `/manager/node-onboarding/jobs?${query}` : "/manager/node-onboarding/jobs"
}

function buildMessageListPath(params: MessageListParams) {
  const search = new URLSearchParams()
  search.set("channel_id", params.channelId)
  search.set("channel_type", String(params.channelType))
  if (typeof params.limit === "number") {
    search.set("limit", String(params.limit))
  }
  if (params.cursor) {
    search.set("cursor", params.cursor)
  }
  if (typeof params.messageId === "number") {
    search.set("message_id", String(params.messageId))
  }
  if (params.clientMsgNo) {
    search.set("client_msg_no", params.clientMsgNo)
  }
  return `/manager/messages?${search.toString()}`
}

export async function managerFetch(path: string, init?: RequestInit) {
  const headers = new Headers(init?.headers)
  headers.set("Accept", "application/json")

  if (init?.body && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json")
  }

  const token = authConfig?.getAccessToken()?.trim()
  if (token) {
    headers.set("Authorization", `Bearer ${token}`)
  }

  const response = await fetch(buildManagerUrl(path), {
    ...init,
    headers,
  })

  if (!response.ok) {
    const error = await parseManagerError(response)
    if (response.status === 401) {
      authConfig?.onUnauthorized()
    }
    throw error
  }

  return response
}

export async function loginManager(credentials: ManagerLoginCredentials): Promise<ManagerSession> {
  const payload = await jsonManagerFetch<ManagerLoginResponse>("/manager/login", {
    method: "POST",
    body: JSON.stringify(credentials),
  })

  return {
    username: payload.username,
    tokenType: payload.token_type,
    accessToken: payload.access_token,
    expiresAt: payload.expires_at,
    permissions: payload.permissions,
  }
}

export function getOverview() {
  return jsonManagerFetch<ManagerOverviewResponse>("/manager/overview")
}

export function getNetworkSummary() {
  return jsonManagerFetch<ManagerNetworkSummaryResponse>("/manager/network/summary")
}

export function getNodes() {
  return jsonManagerFetch<ManagerNodesResponse>("/manager/nodes")
}

export function getNode(nodeId: number) {
  return jsonManagerFetch<ManagerNodeDetailResponse>(`/manager/nodes/${nodeId}`)
}

export function markNodeDraining(nodeId: number) {
  return jsonManagerFetch<ManagerNodeDetailResponse>(`/manager/nodes/${nodeId}/draining`, {
    method: "POST",
  })
}

export function resumeNode(nodeId: number) {
  return jsonManagerFetch<ManagerNodeDetailResponse>(`/manager/nodes/${nodeId}/resume`, {
    method: "POST",
  })
}

function buildNodeScaleInPlanBody(input: CreateNodeScaleInPlanInput) {
  return JSON.stringify({
    confirm_statefulset_tail: input.confirmStatefulSetTail,
    expected_tail_node_id: input.expectedTailNodeId,
  })
}

export function planNodeScaleIn(nodeId: number, input: CreateNodeScaleInPlanInput) {
  return jsonManagerFetch<ManagerNodeScaleInReport>(`/manager/nodes/${nodeId}/scale-in/plan`, {
    method: "POST",
    body: buildNodeScaleInPlanBody(input),
  })
}

export function startNodeScaleIn(nodeId: number, input: CreateNodeScaleInPlanInput) {
  return jsonManagerFetch<ManagerNodeScaleInReport>(`/manager/nodes/${nodeId}/scale-in/start`, {
    method: "POST",
    body: buildNodeScaleInPlanBody(input),
  })
}

export function getNodeScaleInStatus(nodeId: number) {
  return jsonManagerFetch<ManagerNodeScaleInReport>(`/manager/nodes/${nodeId}/scale-in/status`)
}

export function advanceNodeScaleIn(nodeId: number, input: AdvanceNodeScaleInInput = {}) {
  return jsonManagerFetch<ManagerNodeScaleInReport>(`/manager/nodes/${nodeId}/scale-in/advance`, {
    method: "POST",
    body: JSON.stringify({
      max_leader_transfers: input.maxLeaderTransfers,
      force_close_connections: input.forceCloseConnections,
    }),
  })
}

export function cancelNodeScaleIn(nodeId: number) {
  return jsonManagerFetch<ManagerNodeScaleInReport>(`/manager/nodes/${nodeId}/scale-in/cancel`, {
    method: "POST",
  })
}

export function getSlots(params: SlotListParams = {}) {
  const search = new URLSearchParams()
  if (params.nodeId) {
    search.set("node_id", String(params.nodeId))
  }
  const query = search.toString()
  return jsonManagerFetch<ManagerSlotsResponse>(`/manager/slots${query ? `?${query}` : ""}`)
}

export function getSlot(slotId: number) {
  return jsonManagerFetch<ManagerSlotDetailResponse>(`/manager/slots/${slotId}`)
}

export function getSlotLogs(slotId: number, params: SlotLogListParams) {
  const search = new URLSearchParams()
  search.set("node_id", String(params.nodeId))
  if (typeof params.limit === "number") {
    search.set("limit", String(params.limit))
  }
  if (typeof params.cursor === "number") {
    search.set("cursor", String(params.cursor))
  }
  return jsonManagerFetch<ManagerSlotLogsResponse>(`/manager/slots/${slotId}/logs?${search.toString()}`)
}

export function addSlot() {
  return jsonManagerFetch<ManagerSlotDetailResponse>("/manager/slots", {
    method: "POST",
  })
}

export function removeSlot(slotId: number) {
  return jsonManagerFetch<ManagerSlotRemoveResponse>(`/manager/slots/${slotId}`, {
    method: "DELETE",
  })
}

export function transferSlotLeader(slotId: number, input: TransferSlotLeaderInput) {
  return jsonManagerFetch<ManagerSlotDetailResponse>(`/manager/slots/${slotId}/leader/transfer`, {
    method: "POST",
    body: JSON.stringify({ target_node_id: input.targetNodeId }),
  })
}

export function recoverSlot(slotId: number, input: RecoverSlotInput) {
  return jsonManagerFetch<ManagerSlotRecoverResponse>(`/manager/slots/${slotId}/recover`, {
    method: "POST",
    body: JSON.stringify({ strategy: input.strategy }),
  })
}

export function rebalanceSlots() {
  return jsonManagerFetch<ManagerSlotRebalanceResponse>("/manager/slots/rebalance", {
    method: "POST",
  })
}

export function getTasks() {
  return jsonManagerFetch<ManagerTasksResponse>("/manager/tasks")
}

export function getTask(slotId: number) {
  return jsonManagerFetch<ManagerTaskDetailResponse>(`/manager/tasks/${slotId}`)
}

export function getConnections() {
  return jsonManagerFetch<ManagerConnectionsResponse>("/manager/connections")
}

export function getConnection(sessionId: number) {
  return jsonManagerFetch<ManagerConnectionDetailResponse>(`/manager/connections/${sessionId}`)
}

export function getMessages(params: MessageListParams) {
  return jsonManagerFetch<ManagerMessagesResponse>(buildMessageListPath(params))
}

export function getChannelRuntimeMeta(params?: ChannelRuntimeMetaListParams) {
  return jsonManagerFetch<ManagerChannelRuntimeMetaListResponse>(buildChannelRuntimeMetaPath(params))
}

export function getChannelRuntimeMetaDetail(channelType: number, channelId: string) {
  return jsonManagerFetch<ManagerChannelRuntimeMetaDetailResponse>(
    `/manager/channel-runtime-meta/${channelType}/${encodeURIComponent(channelId)}`,
  )
}

export function getNodeOnboardingCandidates() {
  return jsonManagerFetch<ManagerNodeOnboardingCandidatesResponse>("/manager/node-onboarding/candidates")
}

export function createNodeOnboardingPlan(input: CreateNodeOnboardingPlanInput) {
  return jsonManagerFetch<ManagerNodeOnboardingJob>("/manager/node-onboarding/plan", {
    method: "POST",
    body: JSON.stringify({ target_node_id: input.targetNodeId }),
  })
}

export function startNodeOnboardingJob(jobId: string) {
  return jsonManagerFetch<ManagerNodeOnboardingJob>(`/manager/node-onboarding/jobs/${encodeURIComponent(jobId)}/start`, {
    method: "POST",
  })
}

export function getNodeOnboardingJobs(params?: NodeOnboardingJobsParams) {
  return jsonManagerFetch<ManagerNodeOnboardingJobsResponse>(buildNodeOnboardingJobsPath(params))
}

export function getNodeOnboardingJob(jobId: string) {
  return jsonManagerFetch<ManagerNodeOnboardingJob>(`/manager/node-onboarding/jobs/${encodeURIComponent(jobId)}`)
}

export function retryNodeOnboardingJob(jobId: string) {
  return jsonManagerFetch<ManagerNodeOnboardingJob>(`/manager/node-onboarding/jobs/${encodeURIComponent(jobId)}/retry`, {
    method: "POST",
  })
}
