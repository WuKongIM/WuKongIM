import { getManagerApiBaseUrl } from "@/lib/env"
import type {
  ChannelRuntimeMetaListParams,
  ChannelClusterUnhealthyParams,
  ControllerLogListParams,
  ConnectionDetailParams,
  ConnectionListParams,
  CreateDiagnosticsTrackingRuleInput,
  DiagnosticsCommonParams,
  DiagnosticsEventsParams,
  DiagnosticsMessageParams,
  DistributedTaskListParams,
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
  ManagerDBInspectDescribeResponse,
  ManagerDBInspectQueryInput,
  ManagerDBInspectQueryResponse,
  ManagerDBInspectTablesResponse,
  ManagerDiagnosticsTrackingDeleteResponse,
  ManagerDiagnosticsTrackingListResponse,
  ManagerDiagnosticsTrackingMutationResponse,
  ManagerDiagnosticsResponse,
  ManagerDistributedTaskDetailResponse,
  ManagerDistributedTaskDomain,
  ManagerDistributedTasksResponse,
  ManagerDistributedTasksSummaryResponse,
  ManagerLoginResponse,
  ManagerMessagesResponse,
  ManagerRecentConversationsResponse,
  ManagerRuntimeWorkqueuesResponse,
  ManagerNetworkSummaryResponse,
  ManagerNodeOnboardingCandidatesResponse,
  ManagerNodeOnboardingJob,
  ManagerNodeOnboardingJobsResponse,
  ManagerNodeDetailResponse,
  ManagerNodeScaleInReport,
  ManagerNodesResponse,
  ManagerOverviewResponse,
  ManagerPermission,
  ManagerPermissionsResponse,
  ManagerNodePluginsResponse,
  ManagerPlugin,
  ManagerPluginBindingMutationResponse,
  ManagerPluginBindingsResponse,
  ManagerPluginMutationResponse,
  ManagerUserDetailResponse,
  ManagerUsersResponse,
  ManagerSlotDetailResponse,
  ManagerSlotLogsResponse,
  ManagerSlotRaftCompactResponse,
  ManagerSlotRemoveResponse,
  ManagerSlotRecoverResponse,
  ManagerSlotRebalanceResponse,
  ManagerSystemUsersResponse,
  ManagerSlotsResponse,
  ManagerTaskDetailResponse,
  ManagerTasksResponse,
  MessageListParams,
  RecentConversationsParams,
  RuntimeWorkqueueParams,
  MutateSystemUsersInput,
  MutateSystemUsersResponse,
  MutatePluginBindingInput,
  NodeOnboardingJobsParams,
  PluginBindingListParams,
  SlotListParams,
  SlotLogListParams,
  RecoverSlotInput,
  ResetUserTokenInput,
  ResetUserTokenResponse,
  RealtimeMonitorResponse,
  TransferSlotLeaderInput,
  CreateNodeOnboardingPlanInput,
  CreateNodeScaleInPlanInput,
  AdvanceNodeScaleInInput,
  AdvanceMessageRetentionInput,
  AdvanceMessageRetentionResponse,
  ApplicationLogListParams,
  BusinessChannelListParams,
  BusinessChannelMemberListKind,
  BusinessChannelMembersParams,
  BusinessChannelMembersResponse,
  ManagerApplicationLogEntriesResponse,
  ManagerApplicationLogSourcesResponse,
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
  if (typeof params?.includeMaxMessageSeq === "boolean") {
    search.set("include_max_message_seq", String(params.includeMaxMessageSeq))
  }

  const query = search.toString()
  return query ? `/manager/channel-runtime-meta?${query}` : "/manager/channel-runtime-meta"
}

function buildRuntimeWorkqueuesPath(params?: RuntimeWorkqueueParams) {
  const search = new URLSearchParams()
  if (params?.window) {
    search.set("window", params.window)
  }
  if (typeof params?.limit === "number") {
    search.set("limit", String(params.limit))
  }

  const query = search.toString()
  return query ? `/manager/runtime/workqueues?${query}` : "/manager/runtime/workqueues"
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

function buildDBInspectNodeSearch(params?: { nodeId?: number }) {
  const search = new URLSearchParams()
  if (typeof params?.nodeId === "number") {
    search.set("node_id", String(params.nodeId))
  }
  const query = search.toString()
  return query ? `?${query}` : ""
}

function buildPluginBindingsPath(params?: PluginBindingListParams) {
  const search = new URLSearchParams()
  if (params?.uid) {
    search.set("uid", params.uid)
  }
  if (params?.pluginNo) {
    search.set("plugin_no", params.pluginNo)
  }
  if (typeof params?.limit === "number") {
    search.set("limit", String(params.limit))
  }
  if (params?.cursor) {
    search.set("cursor", params.cursor)
  }

  const query = search.toString()
  return query ? `/manager/plugin-bindings?${query}` : "/manager/plugin-bindings"
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
  if (typeof params?.nodeId === "number") {
    search.set("node_id", String(params.nodeId))
  }
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

function buildDistributedTasksPath(params?: DistributedTaskListParams) {
  const search = new URLSearchParams()
  if (params?.domain) {
    search.set("domain", params.domain)
  }
  if (params?.status) {
    search.set("status", params.status)
  }
  if (typeof params?.nodeId === "number") {
    search.set("node_id", String(params.nodeId))
  }
  if (params?.scope) {
    search.set("scope", params.scope)
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
  return query ? `/manager/distributed-tasks?${query}` : "/manager/distributed-tasks"
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

function buildRecentConversationsPath(params: RecentConversationsParams) {
  const search = new URLSearchParams()
  search.set("uid", params.uid)
  if (typeof params.limit === "number") {
    search.set("limit", String(params.limit))
  }
  if (typeof params.msgCount === "number") {
    search.set("msg_count", String(params.msgCount))
  }
  if (typeof params.onlyUnread === "boolean") {
    search.set("only_unread", String(params.onlyUnread))
  }
  return `/manager/conversations?${search.toString()}`
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
  if (!headers.has("Accept")) {
    headers.set("Accept", "application/json")
  }

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

export function getPermissions() {
  return jsonManagerFetch<ManagerPermissionsResponse>("/manager/permissions")
}

export function getDBInspectTables(params?: { nodeId?: number }) {
  return jsonManagerFetch<ManagerDBInspectTablesResponse>(
    `/manager/db/inspect/tables${buildDBInspectNodeSearch(params)}`,
  )
}

export function getDBInspectTable(domain: string, table: string, params?: { nodeId?: number }) {
  return jsonManagerFetch<ManagerDBInspectDescribeResponse>(
    `/manager/db/inspect/tables/${encodeURIComponent(domain)}/${encodeURIComponent(table)}${buildDBInspectNodeSearch(params)}`,
  )
}

export function queryDBInspect(input: ManagerDBInspectQueryInput) {
  return jsonManagerFetch<ManagerDBInspectQueryResponse>("/manager/db/inspect/query", {
    method: "POST",
    body: JSON.stringify(input),
  })
}

export function getNetworkSummary() {
  return jsonManagerFetch<ManagerNetworkSummaryResponse>("/manager/network/summary")
}

export function getRealtimeMonitor(params?: { window?: string; step?: string }) {
  const search = new URLSearchParams()
  if (params?.window) search.set("window", params.window)
  if (params?.step) search.set("step", params.step)
  const query = search.toString()
  return jsonManagerFetch<RealtimeMonitorResponse>(`/manager/monitor/realtime${query ? `?${query}` : ""}`)
}

export function getNodes() {
  return jsonManagerFetch<ManagerNodesResponse>("/manager/nodes")
}

export function getNode(nodeId: number) {
  return jsonManagerFetch<ManagerNodeDetailResponse>(`/manager/nodes/${nodeId}`)
}

export function getNodePlugins(nodeId: number) {
  return jsonManagerFetch<ManagerNodePluginsResponse>(`/manager/nodes/${nodeId}/plugins`)
}

export function getNodePlugin(nodeId: number, pluginNo: string) {
  return jsonManagerFetch<ManagerPlugin>(`/manager/nodes/${nodeId}/plugins/${encodeURIComponent(pluginNo)}`)
}

export function updateNodePluginConfig(nodeId: number, pluginNo: string, config: Record<string, unknown>) {
  return jsonManagerFetch<ManagerPluginMutationResponse>(
    `/manager/nodes/${nodeId}/plugins/${encodeURIComponent(pluginNo)}/config`,
    {
      method: "PUT",
      body: JSON.stringify(config),
    },
  )
}

export function restartNodePlugin(nodeId: number, pluginNo: string) {
  return jsonManagerFetch<ManagerPluginMutationResponse>(
    `/manager/nodes/${nodeId}/plugins/${encodeURIComponent(pluginNo)}/restart`,
    {
      method: "POST",
    },
  )
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
      max_channel_migrations: input.maxChannelMigrations ?? 1,
      force_close_connections: input.forceCloseConnections ?? false,
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

function buildApplicationLogEntriesPath(path: string, params: ApplicationLogListParams) {
  const search = new URLSearchParams()
  search.set("node_id", String(params.nodeId))
  if (params.source) {
    search.set("source", params.source)
  }
  if (typeof params.limit === "number") {
    search.set("limit", String(params.limit))
  }
  if (params.cursor) {
    search.set("cursor", params.cursor)
  }
  if (params.keyword) {
    search.set("keyword", params.keyword)
  }
  if (params.levels?.length) {
    search.set("levels", params.levels.join(","))
  }
  return `${path}?${search.toString()}`
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

export function getApplicationLogSources(nodeId: number) {
  return jsonManagerFetch<ManagerApplicationLogSourcesResponse>(`/manager/app-logs/sources?node_id=${nodeId}`)
}

export function getApplicationLogEntries(params: ApplicationLogListParams) {
  return jsonManagerFetch<ManagerApplicationLogEntriesResponse>(
    buildApplicationLogEntriesPath("/manager/app-logs", params),
  )
}

export function streamApplicationLogEntries(params: ApplicationLogListParams) {
  return managerFetch(buildApplicationLogEntriesPath("/manager/app-logs/stream", params), {
    headers: { Accept: "application/x-ndjson" },
  })
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

export function getDistributedTasksSummary() {
  return jsonManagerFetch<ManagerDistributedTasksSummaryResponse>("/manager/distributed-tasks/summary")
}

export function getDistributedTasks(params?: DistributedTaskListParams) {
  return jsonManagerFetch<ManagerDistributedTasksResponse>(buildDistributedTasksPath(params))
}

export function getDistributedTask(domain: ManagerDistributedTaskDomain, id: string) {
  return jsonManagerFetch<ManagerDistributedTaskDetailResponse>(
    `/manager/distributed-tasks/${domain}/${encodeURIComponent(id)}`,
  )
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

export function getSystemUsers() {
  return jsonManagerFetch<ManagerSystemUsersResponse>("/manager/system-users")
}

export function addSystemUsers(input: MutateSystemUsersInput) {
  return jsonManagerFetch<MutateSystemUsersResponse>("/manager/system-users/add", {
    method: "POST",
    body: JSON.stringify({ uids: input.uids }),
  })
}

export function removeSystemUsers(input: MutateSystemUsersInput) {
  return jsonManagerFetch<MutateSystemUsersResponse>("/manager/system-users/remove", {
    method: "POST",
    body: JSON.stringify({ uids: input.uids }),
  })
}

export function getPluginBindings(params?: PluginBindingListParams) {
  return jsonManagerFetch<ManagerPluginBindingsResponse>(buildPluginBindingsPath(params))
}

export function createPluginBinding(input: MutatePluginBindingInput) {
  return jsonManagerFetch<ManagerPluginBindingMutationResponse>("/manager/plugin-bindings", {
    method: "POST",
    body: JSON.stringify({ uid: input.uid, plugin_no: input.pluginNo }),
  })
}

export function deletePluginBinding(input: MutatePluginBindingInput) {
  return jsonManagerFetch<ManagerPluginBindingMutationResponse>("/manager/plugin-bindings", {
    method: "DELETE",
    body: JSON.stringify({ uid: input.uid, plugin_no: input.pluginNo }),
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

export function getRecentConversations(params: RecentConversationsParams) {
  return jsonManagerFetch<ManagerRecentConversationsResponse>(buildRecentConversationsPath(params))
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
  if (params?.uid) {
    search.set("uid", params.uid)
  }
  if (params?.channelKey) {
    search.set("channel_key", params.channelKey)
  }
  const query = search.toString()
  return jsonManagerFetch<ManagerDiagnosticsResponse>(query ? `/manager/diagnostics/events?${query}` : "/manager/diagnostics/events")
}

export function listDiagnosticsTrackingRules() {
  return jsonManagerFetch<ManagerDiagnosticsTrackingListResponse>("/manager/diagnostics/tracking-rules")
}

export function createDiagnosticsTrackingRule(input: CreateDiagnosticsTrackingRuleInput) {
  const body = input.target === "sender_uid"
    ? {
        target: input.target,
        uid: input.uid,
        ttl_seconds: input.ttlSeconds,
        sample_rate: input.sampleRate ?? 1,
      }
    : {
        target: input.target,
        channel_id: input.channelId,
        channel_type: input.channelType,
        ttl_seconds: input.ttlSeconds,
        sample_rate: input.sampleRate ?? 1,
      }

  return jsonManagerFetch<ManagerDiagnosticsTrackingMutationResponse>("/manager/diagnostics/tracking-rules", {
    method: "POST",
    body: JSON.stringify(body),
  })
}

export function deleteDiagnosticsTrackingRule(ruleId: string) {
  return jsonManagerFetch<ManagerDiagnosticsTrackingDeleteResponse>(
    `/manager/diagnostics/tracking-rules/${encodeURIComponent(ruleId)}`,
    { method: "DELETE" },
  )
}

export function getChannelRuntimeMeta(params?: ChannelRuntimeMetaListParams) {
  return jsonManagerFetch<ManagerChannelRuntimeMetaListResponse>(buildChannelRuntimeMetaPath(params))
}

export function getChannelRuntimeMetaDetail(channelType: number, channelId: string) {
  return jsonManagerFetch<ManagerChannelRuntimeMetaDetailResponse>(
    `/manager/channel-runtime-meta/${channelType}/${encodeURIComponent(channelId)}`,
  )
}

export function getRuntimeWorkqueues(params?: RuntimeWorkqueueParams) {
  return jsonManagerFetch<ManagerRuntimeWorkqueuesResponse>(buildRuntimeWorkqueuesPath(params))
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

export type DashboardMetricsSeriesDTO = {
  latest: number
  peak: number
  avg: number
  series: number[]
}

export type DashboardMetricsResponse = {
  generated_at: string
  window_seconds: number
  step_seconds: number
  points: number
  metrics: {
    send_per_sec: DashboardMetricsSeriesDTO
    deliver_per_sec: DashboardMetricsSeriesDTO
    connections: DashboardMetricsSeriesDTO
    send_latency_p99_ms: DashboardMetricsSeriesDTO
    delivery_latency_p99_ms: DashboardMetricsSeriesDTO
    send_fail_rate_percent: DashboardMetricsSeriesDTO
    delivery_fail_rate_percent: DashboardMetricsSeriesDTO
    active_channels: DashboardMetricsSeriesDTO
    retry_queue_depth: DashboardMetricsSeriesDTO
    fan_out_rate: DashboardMetricsSeriesDTO
  }
}

export function getDashboardMetrics(params?: { window?: string; step?: string }) {
  const search = new URLSearchParams()
  if (params?.window) search.set("window", params.window)
  if (params?.step) search.set("step", params.step)
  const query = search.toString()
  return jsonManagerFetch<DashboardMetricsResponse>(`/manager/dashboard/metrics${query ? `?${query}` : ""}`)
}
