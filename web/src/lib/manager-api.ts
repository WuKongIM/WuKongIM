import { getManagerApiBaseUrl } from "@/lib/env"
import type {
  ChannelRuntimeMetaListParams,
  ManagerChannelRuntimeMetaDetailResponse,
  ManagerChannelRuntimeMetaListResponse,
  ManagerConnectionDetailResponse,
  ManagerConnectionsResponse,
  ManagerLoginResponse,
  ManagerMessagesResponse,
  ManagerNodeDetailResponse,
  ManagerNodesResponse,
  ManagerOverviewResponse,
  ManagerPermission,
  ManagerSlotDetailResponse,
  ManagerSlotRecoverResponse,
  ManagerSlotRebalanceResponse,
  ManagerSlotsResponse,
  ManagerTaskDetailResponse,
  ManagerTasksResponse,
  MessageListParams,
  RecoverSlotInput,
  TransferSlotLeaderInput,
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
}

export class ManagerApiError extends Error {
  status: number
  error: string

  constructor(status: number, error: string, message: string) {
    super(message)
    this.name = "ManagerApiError"
    this.status = status
    this.error = error
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

export function getSlots() {
  return jsonManagerFetch<ManagerSlotsResponse>("/manager/slots")
}

export function getSlot(slotId: number) {
  return jsonManagerFetch<ManagerSlotDetailResponse>(`/manager/slots/${slotId}`)
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
