import { getManagerApiBaseUrl } from "@/lib/env"
import type {
  ChannelRuntimeMetaListParams,
  ChannelClusterUnhealthyParams,
  ControllerLogListParams,
  ConnectionDetailParams,
  ConnectionListParams,
  DiagnosticsCommonParams,
  DiagnosticsEventsParams,
  DiagnosticsMessageParams,
  KickUserInput,
  KickUserResponse,
  ManagerChannelRuntimeMetaDetailResponse,
  ManagerChannelRuntimeMetaListResponse,
  ManagerChannelClusterSummaryResponse,
  ManagerChannelClusterUnhealthyResponse,
  ManagerChannelClusterReplicaDetailResponse,
  ManagerChannelClusterRepairResponse,
  ManagerChannelClusterLeaderTransferResponse,
  ManagerConnectionDetailResponse,
  ManagerControllerLogsResponse,
  ManagerControllerRaftCompactResponse,
  ManagerControllerRaftStatusResponse,
  ManagerConnectionsResponse,
  ManagerDiagnosticsResponse,
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
  ManagerUserDetailResponse,
  ManagerUsersResponse,
  ManagerSlotDetailResponse,
  ManagerSlotLogsResponse,
  ManagerSlotRaftCompactResponse,
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
  ResetUserTokenInput,
  ResetUserTokenResponse,
  TransferSlotLeaderInput,
  CreateNodeOnboardingPlanInput,
  CreateNodeScaleInPlanInput,
  AdvanceNodeScaleInInput,
  AdvanceMessageRetentionInput,
  AdvanceMessageRetentionResponse,
  BusinessChannelListParams,
  BusinessChannelMemberListKind,
  BusinessChannelMembersParams,
  BusinessChannelMembersResponse,
  ManagerBusinessChannelDetailResponse,
  ManagerBusinessChannelsResponse,
  MutateBusinessChannelMembersInput,
  MutateBusinessChannelMembersResponse,
  RepairChannelClusterLeaderInput,
  TransferChannelClusterLeaderInput,
  UpsertBusinessChannelInput,
  UserListParams,
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
  if (typeof params?.nodeId === "number") {
    search.set("node_id", String(params.nodeId))
  }
  if (typeof params?.limit === "number") {
    search.set("limit", String(params.limit))
  }
  if (params?.cursor) {
    search.set("cursor", params.cursor)
  }
  if (params?.channelId) {
    search.set("channel_id", params.channelId)
  }

  const query = search.toString()
  return query ? `/manager/channel-runtime-meta?${query}` : "/manager/channel-runtime-meta"
}

function buildChannelClusterUnhealthyPath(params?: ChannelClusterUnhealthyParams) {
  const search = new URLSearchParams()
  if (typeof params?.limit === "number") {
    search.set("limit", String(params.limit))
  }
  if (params?.cursor) {
    search.set("cursor", params.cursor)
  }

  const query = search.toString()
  return query ? `/manager/channel-cluster/unhealthy?${query}` : "/manager/channel-cluster/unhealthy"
}

function buildConnectionListPath(params?: ConnectionListParams) {
  const search = new URLSearchParams()
  if (typeof params?.nodeId === "number") {
    search.set("node_id", String(params.nodeId))
  }

  const query = search.toString()
  return query ? `/manager/connections?${query}` : "/manager/connections"
}

function buildConnectionDetailPath(sessionId: number, params?: ConnectionDetailParams) {
  const search = new URLSearchParams()
  if (typeof params?.nodeId === "number") {
    search.set("node_id", String(params.nodeId))
  }

  const query = search.toString()
  return query ? `/manager/connections/${sessionId}?${query}` : `/manager/connections/${sessionId}`
}

function buildUsersPath(params?: UserListParams) {
  const search = new URLSearchParams()
  if (params?.keyword !== undefined) {
    search.set("keyword", params.keyword)
  }
  if (typeof params?.limit === "number") {
    search.set("limit", String(params.limit))
  }
  if (params?.cursor) {
    search.set("cursor", params.cursor)
  }

  const query = search.toString()
  return query ? `/manager/users?${query}` : "/manager/users"
}

function buildBusinessChannelsPath(params?: BusinessChannelListParams) {
  const search = new URLSearchParams()
  if (typeof params?.type === "number") {
    search.set("type", String(params.type))
  }
  if (params?.keyword !== undefined) {
    search.set("keyword", params.keyword)
  }
  if (typeof params?.limit === "number") {
    search.set("limit", String(params.limit))
  }
  if (params?.cursor) {
    search.set("cursor", params.cursor)
  }

  const query = search.toString()
  return query ? `/manager/channels?${query}` : "/manager/channels"
}

function buildBusinessChannelMembersPath(
  channelType: number,
  channelId: string,
  listKind: BusinessChannelMemberListKind,
  params?: BusinessChannelMembersParams,
) {
  const search = new URLSearchParams()
  if (typeof params?.limit === "number") {
    search.set("limit", String(params.limit))
  }
  if (params?.cursor) {
    search.set("cursor", params.cursor)
  }
  const path = `/manager/channels/${channelType}/${encodeURIComponent(channelId)}/${listKind}`
  const query = search.toString()
  return query ? `${path}?${query}` : path
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

function applyDiagnosticsCommonParams(search: URLSearchParams, params?: DiagnosticsCommonParams) {
  if (typeof params?.nodeId === "number") {
    search.set("node_id", String(params.nodeId))
  }
  if (typeof params?.limit === "number") {
    search.set("limit", String(params.limit))
  }
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

function buildLogListSearch(params: { nodeId: number; limit?: number; cursor?: number }) {
  const search = new URLSearchParams()
  search.set("node_id", String(params.nodeId))
  if (typeof params.limit === "number") {
    search.set("limit", String(params.limit))
  }
  if (typeof params.cursor === "number") {
    search.set("cursor", String(params.cursor))
  }
  return search
}

export function getSlotLogs(slotId: number, params: SlotLogListParams) {
  const search = buildLogListSearch(params)
  return jsonManagerFetch<ManagerSlotLogsResponse>(`/manager/slots/${slotId}/logs?${search.toString()}`)
}

export function compactSlotRaftLogOnNode(nodeId: number, slotId: number) {
  return jsonManagerFetch<ManagerSlotRaftCompactResponse>(`/manager/nodes/${nodeId}/slots/${slotId}/compact`, {
    method: "POST",
  })
}

export function getControllerLogs(params: ControllerLogListParams) {
  const search = buildLogListSearch(params)
  return jsonManagerFetch<ManagerControllerLogsResponse>(`/manager/controller/logs?${search.toString()}`)
}

export function getControllerRaftStatus(nodeId: number) {
  return jsonManagerFetch<ManagerControllerRaftStatusResponse>(`/manager/nodes/${nodeId}/controller-raft`)
}

export function compactControllerRaftLogs() {
  return jsonManagerFetch<ManagerControllerRaftCompactResponse>("/manager/controller-raft/compact", {
    method: "POST",
  })
}

export function compactControllerRaftLogOnNode(nodeId: number) {
  return jsonManagerFetch<ManagerControllerRaftCompactResponse>(`/manager/nodes/${nodeId}/controller-raft/compact`, {
    method: "POST",
  })
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

export function getConnections(params?: ConnectionListParams) {
  return jsonManagerFetch<ManagerConnectionsResponse>(buildConnectionListPath(params))
}

export function getConnection(sessionId: number, params?: ConnectionDetailParams) {
  return jsonManagerFetch<ManagerConnectionDetailResponse>(buildConnectionDetailPath(sessionId, params))
}

export function getUsers(params?: UserListParams) {
  return jsonManagerFetch<ManagerUsersResponse>(buildUsersPath(params))
}

export function getUser(uid: string) {
  return jsonManagerFetch<ManagerUserDetailResponse>(`/manager/users/${encodeURIComponent(uid)}`)
}

export function kickUser(uid: string, input: KickUserInput) {
  return jsonManagerFetch<KickUserResponse>(`/manager/users/${encodeURIComponent(uid)}/kick`, {
    method: "POST",
    body: JSON.stringify({ device_flag: input.deviceFlag }),
  })
}

export function resetUserToken(uid: string, input: ResetUserTokenInput) {
  return jsonManagerFetch<ResetUserTokenResponse>(`/manager/users/${encodeURIComponent(uid)}/token/reset`, {
    method: "POST",
    body: JSON.stringify({
      device_flag: input.deviceFlag,
      device_level: input.deviceLevel,
      token: input.token,
    }),
  })
}

export function getBusinessChannels(params?: BusinessChannelListParams) {
  return jsonManagerFetch<ManagerBusinessChannelsResponse>(buildBusinessChannelsPath(params))
}

export function getBusinessChannel(channelType: number, channelId: string) {
  return jsonManagerFetch<ManagerBusinessChannelDetailResponse>(
    `/manager/channels/${channelType}/${encodeURIComponent(channelId)}`,
  )
}

export function upsertBusinessChannel(input: UpsertBusinessChannelInput) {
  return jsonManagerFetch<ManagerBusinessChannelDetailResponse>("/manager/channels", {
    method: "POST",
    body: JSON.stringify({
      channel_id: input.channelId,
      channel_type: input.channelType,
      ban: input.ban,
      disband: input.disband,
      send_ban: input.sendBan,
    }),
  })
}

export function getBusinessChannelMembers(
  channelType: number,
  channelId: string,
  listKind: BusinessChannelMemberListKind,
  params?: BusinessChannelMembersParams,
) {
  return jsonManagerFetch<BusinessChannelMembersResponse>(
    buildBusinessChannelMembersPath(channelType, channelId, listKind, params),
  )
}

export function addBusinessChannelMembers(
  channelType: number,
  channelId: string,
  listKind: BusinessChannelMemberListKind,
  input: MutateBusinessChannelMembersInput,
) {
  return jsonManagerFetch<MutateBusinessChannelMembersResponse>(
    `/manager/channels/${channelType}/${encodeURIComponent(channelId)}/${listKind}/add`,
    {
      method: "POST",
      body: JSON.stringify({ uids: input.uids }),
    },
  )
}

export function removeBusinessChannelMembers(
  channelType: number,
  channelId: string,
  listKind: BusinessChannelMemberListKind,
  input: MutateBusinessChannelMembersInput,
) {
  return jsonManagerFetch<MutateBusinessChannelMembersResponse>(
    `/manager/channels/${channelType}/${encodeURIComponent(channelId)}/${listKind}/remove`,
    {
      method: "POST",
      body: JSON.stringify({ uids: input.uids }),
    },
  )
}

export function getMessages(params: MessageListParams) {
  return jsonManagerFetch<ManagerMessagesResponse>(buildMessageListPath(params))
}

export function advanceMessageRetention(input: AdvanceMessageRetentionInput) {
  return jsonManagerFetch<AdvanceMessageRetentionResponse>("/manager/messages/retention", {
    method: "POST",
    body: JSON.stringify({
      channel_id: input.channelId,
      channel_type: input.channelType,
      through_seq: input.throughSeq,
      dry_run: input.dryRun,
    }),
  })
}

export function getDiagnosticsTrace(traceId: string, params?: DiagnosticsCommonParams) {
  const search = new URLSearchParams()
  applyDiagnosticsCommonParams(search, params)
  const query = search.toString()
  const path = `/manager/diagnostics/trace/${encodeURIComponent(traceId)}`
  return jsonManagerFetch<ManagerDiagnosticsResponse>(query ? `${path}?${query}` : path)
}

export function getDiagnosticsMessage(params: DiagnosticsMessageParams) {
  const hasClientSelector = Boolean(params.clientMsgNo)
  const hasChannelSelector = Boolean(params.channelKey) || typeof params.messageSeq === "number"
  if (hasClientSelector === hasChannelSelector || (hasChannelSelector && (!params.channelKey || typeof params.messageSeq !== "number"))) {
    return Promise.reject(new Error("diagnostics message selector must be either clientMsgNo or channelKey plus messageSeq"))
  }

  const search = new URLSearchParams()
  if (params.clientMsgNo) {
    search.set("client_msg_no", params.clientMsgNo)
  }
  if (params.channelKey) {
    search.set("channel_key", params.channelKey)
  }
  if (typeof params.messageSeq === "number") {
    search.set("message_seq", String(params.messageSeq))
  }
  applyDiagnosticsCommonParams(search, params)
  return jsonManagerFetch<ManagerDiagnosticsResponse>(`/manager/diagnostics/message?${search.toString()}`)
}

export function getDiagnosticsEvents(params?: DiagnosticsEventsParams) {
  const search = new URLSearchParams()
  applyDiagnosticsCommonParams(search, params)
  if (params?.stage) {
    search.set("stage", params.stage)
  }
  if (params?.result) {
    search.set("result", params.result)
  }
  const query = search.toString()
  return jsonManagerFetch<ManagerDiagnosticsResponse>(query ? `/manager/diagnostics/events?${query}` : "/manager/diagnostics/events")
}

export function getChannelRuntimeMeta(params?: ChannelRuntimeMetaListParams) {
  return jsonManagerFetch<ManagerChannelRuntimeMetaListResponse>(buildChannelRuntimeMetaPath(params))
}

export function getChannelRuntimeMetaDetail(channelType: number, channelId: string) {
  return jsonManagerFetch<ManagerChannelRuntimeMetaDetailResponse>(
    `/manager/channel-runtime-meta/${channelType}/${encodeURIComponent(channelId)}`,
  )
}

export function getChannelClusterSummary() {
  return jsonManagerFetch<ManagerChannelClusterSummaryResponse>("/manager/channel-cluster/summary")
}

export function getChannelClusterUnhealthy(params?: ChannelClusterUnhealthyParams) {
  return jsonManagerFetch<ManagerChannelClusterUnhealthyResponse>(buildChannelClusterUnhealthyPath(params))
}

export function getChannelClusterReplicas(channelType: number, channelId: string) {
  return jsonManagerFetch<ManagerChannelClusterReplicaDetailResponse>(
    `/manager/channel-cluster/${channelType}/${encodeURIComponent(channelId)}/replicas`,
  )
}

export function repairChannelClusterLeader(
  channelType: number,
  channelId: string,
  input: RepairChannelClusterLeaderInput = {},
) {
  return jsonManagerFetch<ManagerChannelClusterRepairResponse>(
    `/manager/channel-cluster/${channelType}/${encodeURIComponent(channelId)}/repair`,
    { method: "POST", body: JSON.stringify(input) },
  )
}

export function transferChannelClusterLeader(
  channelType: number,
  channelId: string,
  input: TransferChannelClusterLeaderInput,
) {
  return jsonManagerFetch<ManagerChannelClusterLeaderTransferResponse>(
    `/manager/channel-cluster/${channelType}/${encodeURIComponent(channelId)}/leader/transfer`,
    { method: "POST", body: JSON.stringify(input) },
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
