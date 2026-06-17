export type ManagerPermission = {
  resource: string
  actions: string[]
}

export type ManagerPermissionGrant = {
  resource: string
  actions: string[]
}

export type ManagerPermissionUser = {
  username: string
  permissions: ManagerPermissionGrant[]
}

export type ManagerPermissionResource = {
  resource: string
  actions: string[]
  description: string
}

export type ManagerPermissionsResponse = {
  auth_enabled: boolean
  current_user: string
  users: ManagerPermissionUser[]
  resources: ManagerPermissionResource[]
}

export type ManagerLoginResponse = {
  username: string
  token_type: string
  access_token: string
  expires_at: string
  permissions: ManagerPermission[]
}

export type ManagerOverviewResponse = {
  generated_at: string
  cluster: {
    controller_leader_id: number
  }
  nodes: {
    total: number
    alive: number
    suspect: number
    dead: number
    draining: number
  }
  slots: {
    total: number
    ready: number
    quorum_lost: number
    leader_missing: number
    unreported: number
    peer_mismatch: number
    epoch_lag: number
  }
  tasks: {
    total: number
    pending: number
    retrying: number
    failed: number
  }
  anomalies: {
    slots: {
      quorum_lost: ManagerOverviewSlotAnomalyGroup
      leader_missing: ManagerOverviewSlotAnomalyGroup
      sync_mismatch: ManagerOverviewSlotAnomalyGroup
    }
    tasks: {
      failed: ManagerOverviewTaskAnomalyGroup
      retrying: ManagerOverviewTaskAnomalyGroup
    }
  }
}

export type ManagerOverviewSlotAnomalyGroup = {
  count: number
  items: ManagerOverviewSlotAnomalyItem[]
}

export type ManagerOverviewSlotAnomalyItem = {
  slot_id: number
  quorum: string
  sync: string
  leader_id: number
  desired_peers: number[]
  current_peers: number[]
  last_report_at: string
}

export type ManagerOverviewTaskAnomalyGroup = {
  count: number
  items: ManagerOverviewTaskAnomalyItem[]
}

export type ManagerOverviewTaskAnomalyItem = {
  slot_id: number
  kind: string
  step: string
  status: string
  source_node: number
  target_node: number
  attempt: number
  next_run_at: string | null
  last_error: string
}

export type ManagerNetworkSummaryResponse = {
  generated_at: string
  scope: ManagerNetworkScope
  source_status: ManagerNetworkSourceStatus
  headline: ManagerNetworkHeadline
  traffic: ManagerNetworkTraffic
  peers: ManagerNetworkPeer[]
  services: ManagerNetworkRPCService[]
  channel_replication: ManagerNetworkChannelReplication
  discovery: ManagerNetworkDiscovery
  history: ManagerNetworkHistory
  events: ManagerNetworkEvent[]
}

export type ManagerNetworkScope = {
  view: string
  local_node_id: number
  controller_leader_id: number
}

export type ManagerNetworkSourceStatus = {
  local_collector: string
  controller_context: string
  runtime_views: string
  errors: Record<string, string>
}

export type ManagerNetworkHeadline = {
  remote_peers: number
  alive_nodes: number
  suspect_nodes: number
  dead_nodes: number
  draining_nodes: number
  pool_active: number
  pool_idle: number
  rpc_inflight: number
  dial_errors_1m: number
  queue_full_1m: number
  timeouts_1m: number
  stale_observations: number
}

export type ManagerNetworkTraffic = {
  scope: string
  tx_bytes_1m: number
  rx_bytes_1m: number
  tx_bps: number
  rx_bps: number
  peer_breakdown_available: boolean
  by_message_type: ManagerNetworkTrafficMessageType[]
}

export type ManagerNetworkTrafficMessageType = {
  direction: string
  message_type: string
  bytes_1m: number
  bps: number
}

export type ManagerNetworkHistory = {
  window_seconds: number
  step_seconds: number
  traffic: ManagerNetworkTrafficHistoryPoint[]
  rpc: ManagerNetworkRPCHistoryPoint[]
  errors: ManagerNetworkErrorHistoryPoint[]
}

export type ManagerNetworkTrafficHistoryPoint = {
  at: string
  tx_bytes: number
  rx_bytes: number
}

export type ManagerNetworkRPCHistoryPoint = {
  at: string
  calls: number
  success: number
  errors: number
  expected_timeouts: number
}

export type ManagerNetworkErrorHistoryPoint = {
  at: string
  dial_errors: number
  queue_full: number
  timeouts: number
  remote_errors: number
}

export type ManagerNetworkPeer = {
  node_id: number
  name: string
  addr: string
  health: string
  last_heartbeat_at: string
  pools: ManagerNetworkPeerPools
  rpc: ManagerNetworkPeerRPC
  errors: ManagerNetworkPeerErrors
}

export type ManagerNetworkPoolStats = {
  active: number
  idle: number
}

export type ManagerNetworkPeerPools = {
  cluster: ManagerNetworkPoolStats
  data_plane: ManagerNetworkPoolStats
}

export type ManagerNetworkPeerRPC = {
  inflight: number
  calls_1m: number
  p95_ms: number
  success_rate: number | null
}

export type ManagerNetworkPeerErrors = {
  dial_error_1m: number
  queue_full_1m: number
  timeout_1m: number
  remote_error_1m: number
}

export type ManagerNetworkRPCService = {
  service_id: number
  service: string
  group: string
  target_node: number
  inflight: number
  calls_1m: number
  success_1m: number
  expected_timeout_1m: number
  timeout_1m: number
  queue_full_1m: number
  remote_error_1m: number
  other_error_1m: number
  p50_ms: number
  p95_ms: number
  p99_ms: number
  last_seen_at: string
}

export type ManagerNetworkChannelReplication = {
  pool: ManagerNetworkPoolStats
  services: ManagerNetworkRPCService[]
  long_poll: ManagerNetworkLongPollConfig
  long_poll_timeouts_1m: number
  data_plane_rpc_timeout_ms: number
}

export type ManagerNetworkLongPollConfig = {
  lane_count: number
  max_wait_ms: number
  max_bytes: number
  max_channels: number
}

export type ManagerNetworkDiscovery = {
  listen_addr: string
  advertise_addr: string
  seeds: string[]
  static_nodes: ManagerNetworkDiscoveryNode[]
  pool_size: number
  data_plane_pool_size: number
  dial_timeout_ms: number
  controller_observation_interval_ms: number
}

export type ManagerNetworkDiscoveryNode = {
  node_id: number
  addr: string
}

export type ManagerNetworkEvent = {
  at: string
  severity: string
  kind: string
  target_node: number
  service: string
  message: string
}

export type ManagerMonitorMetricPoint = {
  at: string
  value: number
}

export type ManagerMonitorMetricSeries = {
  key: string
  unit: string
  latest: number
  peak: number
  avg: number
  points: ManagerMonitorMetricPoint[]
}

export type ManagerMonitorMetricsResponse = {
  generated_at: string
  window_seconds: number
  step_seconds: number
  points: number
  scope: {
    view: string
    local_node_id: number
    node_id?: number
  }
  capabilities: {
    node_filter: boolean
  }
  nodes: ManagerMonitorNode[]
  metrics: Record<string, ManagerMonitorMetricSeries>
}

export type ManagerMonitorNode = {
  node_id: number
  name: string
  is_local: boolean
  available: boolean
}

export type ManagerNode = {
  node_id: number
  name?: string
  addr: string
  status: string
  last_heartbeat_at: string
  is_local: boolean
  capacity_weight: number
  membership?: ManagerNodeMembership
  health?: ManagerNodeHealth
  controller: ManagerNodeController
  slot_stats: {
    count: number
    leader_count: number
  }
  slots?: ManagerNodeSlotsSummary
  runtime?: ManagerNodeRuntime
  actions?: ManagerNodeActions
}

export type ManagerNodeMembership = {
  role: string
  join_state: string
  schedulable: boolean
}

export type ManagerNodeHealth = {
  status: string
  last_heartbeat_at: string
}

export type ManagerNodeController = {
  role: string
  voter?: boolean
  leader_id?: number
  // raft_health is the manager-facing Controller Raft health bucket for this node.
  raft_health?: string
  // first_index is the first available Controller Raft log index on this node.
  first_index?: number
  // applied_index is the Controller Raft applied watermark on this node.
  applied_index?: number
  // snapshot_index is the latest Controller Raft snapshot index on this node.
  snapshot_index?: number
}

export type ManagerNodeSlotsSummary = {
  replica_count: number
  leader_count: number
  follower_count: number
  quorum_lost_count: number
  unreported_count: number
}

export type ManagerNodeRuntime = {
  node_id: number
  active_online: number
  closing_online: number
  total_online: number
  gateway_sessions: number
  sessions_by_listener: Record<string, number>
  accepting_new_sessions: boolean
  draining: boolean
  unknown: boolean
}

export type ManagerNodeActions = {
  can_drain: boolean
  can_resume: boolean
  can_scale_in: boolean
  can_onboard: boolean
}

export type ManagerNodesResponse = {
  generated_at: string
  controller_leader_id: number
  total: number
  items: ManagerNode[]
}

export type ManagerNodeDetailResponse = ManagerNode & {
  slots: {
    hosted_ids: number[]
    leader_ids: number[]
    replica_count: number
    leader_count: number
    follower_count: number
    quorum_lost_count: number
    unreported_count: number
  }
}

// ManagerNodeScaleInReport describes the backend-manager safety report for one node scale-in flow.
export type ManagerNodeScaleInReport = {
  node_id: number
  status: string
  safe_to_remove: boolean
  can_start: boolean
  can_advance: boolean
  can_cancel: boolean
  connection_safety_verified: boolean
  blocked_reasons: ManagerNodeScaleInBlockedReason[]
  checks: ManagerNodeScaleInChecks
  progress: ManagerNodeScaleInProgress
  runtime: ManagerNodeScaleInRuntime
  leaders: ManagerNodeScaleInLeader[]
  next_action?: string
}

// ManagerNodeScaleInChecks exposes individual preflight and runtime safety checks.
export type ManagerNodeScaleInChecks = {
  target_exists: boolean
  target_is_data_node: boolean
  target_is_active_or_draining: boolean
  target_is_not_controller_voter: boolean
  tail_node_mapping_verified: boolean
  remaining_data_nodes_enough: boolean
  controller_leader_available: boolean
  slot_replica_count_known: boolean
  no_other_draining_node: boolean
  no_active_hashslot_migrations: boolean
  no_running_onboarding: boolean
  no_active_reconcile_tasks_involving_target: boolean
  no_failed_reconcile_tasks: boolean
  runtime_views_complete_and_fresh: boolean
  all_slots_have_quorum: boolean
  target_not_unique_healthy_replica: boolean
  channel_inventory_available?: boolean | null
  no_active_channel_migrations_involving_target?: boolean | null
  no_channel_leaders_on_target?: boolean | null
  no_channel_replicas_on_target?: boolean | null
}

// ManagerNodeScaleInProgress contains counters that explain remaining scale-in work.
export type ManagerNodeScaleInProgress = {
  assigned_slot_replicas: number
  observed_slot_replicas: number
  slot_leaders: number
  active_tasks_involving_node: number
  active_migrations_involving_node: number
  channel_leaders?: number | null
  channel_replicas?: number | null
  active_channel_migrations_involving_node?: number | null
  active_connections: number
  closing_connections: number
  gateway_sessions: number
  active_connections_unknown: boolean
  channel_inventory_scanned?: boolean | null
  channel_inventory_partial?: boolean | null
  channel_inventory_error?: string | null
}

// ManagerNodeScaleInRuntime contains live runtime counters used for connection safety.
export type ManagerNodeScaleInRuntime = {
  node_id: number
  active_online: number
  closing_online: number
  total_online: number
  gateway_sessions: number
  sessions_by_listener: Record<string, number>
  accepting_new_sessions: boolean
  draining: boolean
  unknown: boolean
}

// ManagerNodeScaleInBlockedReason is a stable machine-readable blocker plus operator text.
export type ManagerNodeScaleInBlockedReason = {
  code: string
  message: string
  count: number
  slot_id: number
  node_id: number
}

// ManagerNodeScaleInLeader is reserved for leader transfer suggestions returned by the backend.
export type ManagerNodeScaleInLeader = {
  slot_id: number
  current_leader_id: number
  transfer_candidates: number[]
}

export type CreateNodeScaleInPlanInput = {
  confirmStatefulSetTail: boolean
  expectedTailNodeId: number
}

export type AdvanceNodeScaleInInput = {
  maxLeaderTransfers?: number
  maxChannelMigrations?: number
  forceCloseConnections?: boolean
}

export type ManagerSlot = {
  slot_id: number
  hash_slots?: ManagerSlotHashSlots | null
  state: {
    quorum: string
    sync: string
  }
  assignment: {
    desired_peers: number[]
    config_epoch: number
    balance_version: number
  }
  runtime: {
    current_peers: number[]
    current_voters?: number[]
    leader_id: number
    healthy_voters: number
    has_quorum: boolean
    observed_config_epoch: number
    last_report_at: string
  }
  node_log?: ManagerSlotNodeLog | null
}

export type ManagerSlotHashSlots = {
  count: number
  items: number[]
}

export type ManagerSlotNodeLog = {
  node_id: number
  leader_id: number
  commit_index: number
  applied_index: number
}

export type SlotListParams = {
  nodeId?: number
}

export type SlotLogListParams = {
  nodeId: number
  limit?: number
  cursor?: number
}

export type ControllerLogListParams = {
  nodeId: number
  limit?: number
  cursor?: number
}

export type ManagerSlotsResponse = {
  total: number
  items: ManagerSlot[]
}

export type ManagerSlotLogEntry = {
  index: number
  term: number
  type: string
  data_size: number
  decode_status?: string
  decoded_type?: string
  decoded?: Record<string, unknown>
}

export type ManagerSlotLogsResponse = {
  node_id: number
  slot_id: number
  first_index: number
  last_index: number
  commit_index: number
  applied_index: number
  next_cursor?: number
  items: ManagerSlotLogEntry[]
}

// ManagerSlotRaftCompactNodeResult describes one node-local Slot Raft compaction attempt.
export type ManagerSlotRaftCompactNodeResult = {
  // node_id is the node that handled the local compaction attempt.
  node_id: number
  // slot_id is the physical slot whose local Raft log was compacted.
  slot_id: number
  // success reports whether the node accepted and completed the attempt.
  success: boolean
  // applied_index is the node-local applied index used as the compaction target.
  applied_index: number
  // before_snapshot_index is the persisted snapshot index before the attempt.
  before_snapshot_index: number
  // after_snapshot_index is the persisted snapshot index after the attempt.
  after_snapshot_index: number
  // compacted reports whether this attempt created a new snapshot and compacted entries.
  compacted: boolean
  // skipped_reason explains why no new snapshot was created when compacted is false.
  skipped_reason: string
  // error is the failure message when success is false.
  error: string
}

// ManagerSlotRaftCompactResponse is returned after triggering node-local Slot Raft compaction.
export type ManagerSlotRaftCompactResponse = {
  // generated_at records when the compaction result was assembled.
  generated_at: string
  // total is the number of target attempts included in the response.
  total: number
  // succeeded is the number of attempts that completed.
  succeeded: number
  // failed is the number of attempts that returned an error.
  failed: number
  // items contains per-node Slot Raft compaction results.
  items: ManagerSlotRaftCompactNodeResult[]
}

export type ManagerControllerLogEntry = ManagerSlotLogEntry

export type ManagerControllerLogsResponse = {
  node_id: number
  first_index: number
  last_index: number
  commit_index: number
  applied_index: number
  next_cursor?: number
  items: ManagerControllerLogEntry[]
}

// ManagerControllerRaftCompaction describes Controller Raft log compaction state on one node.
export type ManagerControllerRaftCompaction = {
  // enabled reports whether Controller Raft snapshot compaction is enabled.
  enabled: boolean
  // trigger_entries is the applied-entry delta required before taking another snapshot.
  trigger_entries: number
  // check_interval_ms is the minimum interval between compaction checks.
  check_interval_ms: number
  // last_snapshot_index is the latest snapshot index created by compaction.
  last_snapshot_index: number
  // last_snapshot_at records when the latest snapshot was created.
  last_snapshot_at: string
  // last_check_at records the latest compaction check attempt.
  last_check_at: string
  // last_error is the latest compaction error, when present.
  last_error: string
  // last_error_at records when last_error was observed.
  last_error_at: string
  // degraded reports whether the latest compaction attempt failed.
  degraded: boolean
}

// ManagerControllerRaftRestore describes Controller metadata snapshot restore state.
export type ManagerControllerRaftRestore = {
  // last_snapshot_index is the index of the latest restored snapshot.
  last_snapshot_index: number
  // last_snapshot_term is the term of the latest restored snapshot.
  last_snapshot_term: number
  // last_restored_at records when the latest snapshot restore succeeded.
  last_restored_at: string
  // last_error is the latest restore error, when present.
  last_error: string
  // last_error_at records when last_error was observed.
  last_error_at: string
  // failed reports whether the latest restore attempt failed.
  failed: boolean
}

// ManagerControllerRaftPeer describes one follower from the Controller Raft leader's view.
export type ManagerControllerRaftPeer = {
  // node_id is the follower node ID.
  node_id: number
  // match is the highest log index known to match on the follower.
  match: number
  // next is the next log index the leader will send to the follower.
  next: number
  // state is the raft progress state.
  state: string
  // pending_snapshot is the snapshot index currently pending for the follower.
  pending_snapshot: number
  // recent_active reports whether the follower was recently active.
  recent_active: boolean
  // needs_snapshot reports whether the follower has fallen behind the local first index.
  needs_snapshot: boolean
  // snapshot_transferring reports whether raft is currently transferring a snapshot.
  snapshot_transferring: boolean
}

// ManagerControllerRaftStatusResponse is a node-scoped Controller Raft status snapshot.
export type ManagerControllerRaftStatusResponse = {
  // node_id is the node whose Controller Raft status was read.
  node_id: number
  // role is leader, follower, candidate, or unknown.
  role: string
  // leader_id is the Controller Raft leader known to the queried node.
  leader_id: number
  // term is the queried node's current Controller Raft term.
  term: number
  // health is the derived manager-facing status bucket.
  health: string
  // first_index is the first available Controller Raft log index.
  first_index: number
  // last_index is the last available Controller Raft log index.
  last_index: number
  // commit_index is the queried node's committed Controller Raft watermark.
  commit_index: number
  // applied_index is the queried node's applied Controller Raft watermark.
  applied_index: number
  // snapshot_index is the latest persisted Controller Raft snapshot index.
  snapshot_index: number
  // snapshot_term is the latest persisted Controller Raft snapshot term.
  snapshot_term: number
  // compaction describes Controller Raft log compaction state.
  compaction: ManagerControllerRaftCompaction
  // restore describes Controller metadata snapshot restore state.
  restore: ManagerControllerRaftRestore
  // peers contains leader-side follower progress.
  peers: ManagerControllerRaftPeer[]
}

// ManagerControllerRaftCompactNodeResult describes one Controller voter node compaction attempt.
export type ManagerControllerRaftCompactNodeResult = {
  // node_id is the Controller voter node that handled the attempt.
  node_id: number
  // success reports whether the node accepted and completed the local attempt.
  success: boolean
  // applied_index is the node-local applied index used as the compaction target.
  applied_index: number
  // before_snapshot_index is the persisted snapshot index before the attempt.
  before_snapshot_index: number
  // after_snapshot_index is the persisted snapshot index after the attempt.
  after_snapshot_index: number
  // compacted reports whether this attempt created a new snapshot and compacted entries.
  compacted: boolean
  // skipped_reason explains why no new snapshot was created when compacted is false.
  skipped_reason: string
  // error is the per-node failure message when success is false.
  error: string
}

// ManagerControllerRaftCompactResponse is returned after triggering control-plane-wide compaction.
export type ManagerControllerRaftCompactResponse = {
  // generated_at records when the fan-out result was assembled.
  generated_at: string
  // total is the number of Controller voter nodes targeted.
  total: number
  // succeeded is the number of nodes that completed the attempt.
  succeeded: number
  // failed is the number of nodes that returned an error.
  failed: number
  // items contains per-node results ordered by node id.
  items: ManagerControllerRaftCompactNodeResult[]
}

export type ManagerTask = {
  slot_id: number
  kind: string
  step: string
  status: string
  source_node: number
  target_node: number
  attempt: number
  next_run_at: string | null
  last_error: string
}

export type ManagerSlotDetailResponse = ManagerSlot & {
  task: ManagerTask | null
}

export type ManagerSlotRemoveResponse = {
  slot_id: number
  result: string
}

export type ManagerSlotRecoverResponse = {
  strategy: string
  result: string
  slot: ManagerSlotDetailResponse
}

export type ManagerSlotRebalancePlanItem = {
  hash_slot: number
  from_slot_id: number
  to_slot_id: number
}

export type ManagerSlotRebalanceResponse = {
  total: number
  items: ManagerSlotRebalancePlanItem[]
}

export type ManagerTasksResponse = {
  total: number
  items: ManagerTask[]
}

export type ManagerTaskDetailResponse = ManagerTask & {
  slot: {
    state: ManagerSlot["state"]
    assignment: ManagerSlot["assignment"]
    runtime: ManagerSlot["runtime"]
  }
}

export type ManagerDistributedTaskStatus =
  | "pending"
  | "running"
  | "retrying"
  | "blocked"
  | "failed"
  | "completed"
  | "cancelled"
  | "unknown"

export type ManagerDistributedTaskDomain =
  | "slot_reconcile"
  | "node_onboarding"
  | "node_scale_in"
  | "channel_migration"

export type ManagerDistributedTaskScopeType = "slot" | "node" | "channel" | "job"

export type DistributedTaskListParams = {
  domain?: ManagerDistributedTaskDomain
  status?: ManagerDistributedTaskStatus
  nodeId?: number
  scope?: ManagerDistributedTaskScopeType
  keyword?: string
  limit?: number
  cursor?: string
}

export type ManagerDistributedTaskScope = {
  type: ManagerDistributedTaskScopeType
  id: string
  slot_id: number
  channel_id: string
  channel_type: number
  node_id: number
}

export type ManagerDistributedTask = {
  id: string
  domain: ManagerDistributedTaskDomain
  kind: string
  status: ManagerDistributedTaskStatus
  phase: string
  scope: ManagerDistributedTaskScope
  source_node: number
  target_node: number
  owner_node: number
  attempt: number
  next_run_at: string | null
  created_at: string | null
  updated_at: string | null
  last_error: string
  summary: string
  links: Record<string, string>
}

export type ManagerDistributedTaskWarning = {
  domain: ManagerDistributedTaskDomain
  code: string
  message: string
}

export type ManagerDistributedTasksSummaryResponse = {
  total: number
  by_status: Record<ManagerDistributedTaskStatus, number>
  by_domain: Record<ManagerDistributedTaskDomain, number>
  partial: boolean
  warnings: ManagerDistributedTaskWarning[]
}

export type ManagerDistributedTasksResponse = {
  total: number
  items: ManagerDistributedTask[]
  next_cursor: string
  has_more: boolean
  partial: boolean
  warnings: ManagerDistributedTaskWarning[]
}

export type ManagerDistributedTaskDetailPayload = {
  domain: ManagerDistributedTaskDomain
  raw_status: string
  slot?: ManagerTaskDetailResponse | null
  node_onboarding?: ManagerNodeOnboardingJob | null
  node_scale_in?: ManagerNodeScaleInReport | null
  channel_migration?: Record<string, unknown> | null
}

export type ManagerDistributedTaskDetailResponse = {
  task: ManagerDistributedTask
  detail: ManagerDistributedTaskDetailPayload
}

export type ManagerChannelRuntimeMeta = {
  channel_id: string
  channel_type: number
  slot_id: number
  channel_epoch: number
  leader_epoch: number
  leader: number
  replicas: number[]
  isr: number[]
  min_isr: number
  max_message_seq?: number
  status: string
}

export type ManagerChannelRuntimeMetaListResponse = {
  items: ManagerChannelRuntimeMeta[]
  has_more: boolean
  next_cursor?: string
}

export type ManagerChannelRuntimeMetaDetailResponse = ManagerChannelRuntimeMeta & {
  hash_slot: number
  features: number
  lease_until_ms: number
}

export type BusinessChannelListParams = {
  nodeId?: number
  type?: number
  keyword?: string
  limit?: number
  cursor?: string
}

export type ManagerBusinessChannelListItem = {
  channel_id: string
  channel_type: number
  slot_id: number
  hash_slot: number
  ban: boolean
  disband: boolean
  send_ban: boolean
  subscriber_mutation_version: number
}

export type ManagerBusinessChannelsResponse = {
  items: ManagerBusinessChannelListItem[]
  has_more: boolean
  next_cursor?: string
}

export type ManagerBusinessChannelDetailResponse = ManagerBusinessChannelListItem & {
  has_subscribers: boolean
  has_allowlist: boolean
  has_denylist: boolean
}

export type UpsertBusinessChannelInput = {
  channelId: string
  channelType: number
  ban?: boolean
  disband?: boolean
  sendBan?: boolean
}

export type BusinessChannelMemberListKind = "subscribers" | "allowlist" | "denylist"

export type BusinessChannelMembersParams = {
  limit?: number
  cursor?: string
}

export type ManagerBusinessChannelMember = {
  uid: string
}

export type BusinessChannelMembersResponse = {
  items: ManagerBusinessChannelMember[]
  has_more: boolean
  next_cursor?: string
}

export type MutateBusinessChannelMembersInput = {
  uids: string[]
}

export type MutateBusinessChannelMembersResponse = {
  channel_id: string
  channel_type: number
  list: BusinessChannelMemberListKind
  changed: boolean
}

export type ManagerChannelClusterSummaryResponse = {
  total: number
  healthy: number
  isr_insufficient: number
  no_leader: number
  avg_replicas: number
  avg_isr: number
  leader_distribution: Array<{
    node_id: number
    count: number
  }>
}

export type ManagerChannelClusterUnhealthyItem = ManagerChannelRuntimeMeta & {
  reasons: string[]
}

export type ManagerChannelClusterUnhealthyResponse = {
  items: ManagerChannelClusterUnhealthyItem[]
  has_more: boolean
  next_cursor?: string
}

export type ManagerChannelClusterReplicaStatus = {
  node_id: number
  role: string
  is_leader: boolean
  in_isr: boolean
  reported: boolean
  commit_seq?: number | null
  leo?: number | null
  checkpoint_hw?: number | null
  lag?: number | null
}

export type ManagerChannelClusterReplicaDetailResponse = {
  channel: ManagerChannelRuntimeMetaDetailResponse
  runtime_reported: boolean
  commit_seq?: number | null
  min_available_seq?: number | null
  retention_through_seq?: number | null
  replicas: ManagerChannelClusterReplicaStatus[]
}

export type RepairChannelClusterLeaderInput = {
  reason?: string
}

export type ManagerChannelClusterRepairResponse = {
  changed: boolean
  channel: ManagerChannelRuntimeMetaDetailResponse
}

export type TransferChannelClusterLeaderInput = {
  target_node_id: number
}

export type ManagerChannelClusterLeaderTransferResponse = {
  changed: boolean
  channel: ManagerChannelRuntimeMetaDetailResponse
}

export type ManagerConnection = {
  node_id: number
  session_id: number
  uid: string
  device_id: string
  device_flag: string
  device_level: string
  slot_id: number
  state: string
  listener: string
  connected_at: string
  remote_addr: string
  local_addr: string
}

export type ManagerConnectionsResponse = {
  total: number
  items: ManagerConnection[]
}

export type ManagerConnectionDetailResponse = ManagerConnection

export type ManagerMessage = {
  message_id: number
  message_seq: number
  client_msg_no: string
  channel_id: string
  channel_type: number
  from_uid: string
  timestamp: number
  payload: string
}

export type ManagerMessagesResponse = {
  items: ManagerMessage[]
  has_more: boolean
  next_cursor?: string
}

export type RecentConversationsParams = {
  uid: string
  limit?: number
  msgCount?: number
  onlyUnread?: boolean
}

export type ManagerRecentConversation = {
  uid: string
  channel_id: string
  channel_type: number
  unread: number
  timestamp: number
  last_msg_seq: number
  last_client_msg_no: string
  read_to_msg_seq: number
  version: number
  recent_messages: ManagerMessage[]
}

export type ManagerRecentConversationsResponse = {
  uid: string
  limit: number
  msg_count: number
  only_unread: boolean
  truncated: boolean
  items: ManagerRecentConversation[]
}

export type ManagerNodeOnboardingCandidate = {
  node_id: number
  name: string
  addr: string
  role: string
  join_state: string
  status: string
  slot_count: number
  leader_count: number
  recommended: boolean
}

export type ManagerNodeOnboardingCandidatesResponse = {
  total: number
  items: ManagerNodeOnboardingCandidate[]
}

export type ManagerNodeOnboardingPlanSummary = {
  current_target_slot_count: number
  planned_target_slot_count: number
  current_target_leader_count: number
  planned_leader_gain: number
}

export type ManagerNodeOnboardingPlanMove = {
  slot_id: number
  source_node_id: number
  target_node_id: number
  reason: string
  desired_peers_before: number[]
  desired_peers_after: number[]
  current_leader_id: number
  leader_transfer_required: boolean
}

export type ManagerNodeOnboardingBlockedReason = {
  code: string
  scope: string
  slot_id: number
  node_id: number
  message: string
}

export type ManagerNodeOnboardingPlan = {
  target_node_id: number
  summary: ManagerNodeOnboardingPlanSummary
  moves: ManagerNodeOnboardingPlanMove[]
  blocked_reasons: ManagerNodeOnboardingBlockedReason[]
}

export type ManagerNodeOnboardingMove = {
  slot_id: number
  source_node_id: number
  target_node_id: number
  status: string
  task_kind: string
  task_slot_id: number
  started_at: string
  completed_at: string
  last_error: string
  desired_peers_before: number[]
  desired_peers_after: number[]
  leader_before: number
  leader_after: number
  leader_transfer_required: boolean
}

export type ManagerNodeOnboardingResultCounts = {
  pending: number
  running: number
  completed: number
  failed: number
  skipped: number
}

export type ManagerNodeOnboardingJob = {
  job_id: string
  target_node_id: number
  retry_of_job_id: string
  status: string
  created_at: string
  updated_at: string
  started_at: string
  completed_at: string
  plan_version: number
  plan_fingerprint: string
  plan: ManagerNodeOnboardingPlan
  moves: ManagerNodeOnboardingMove[]
  current_move_index: number
  result_counts: ManagerNodeOnboardingResultCounts
  last_error: string
}

export type ManagerNodeOnboardingJobsResponse = {
  items: ManagerNodeOnboardingJob[]
  next_cursor: string
  has_more: boolean
}

export type CreateNodeOnboardingPlanInput = {
  targetNodeId: number
}

export type NodeOnboardingJobsParams = {
  limit?: number
  cursor?: string
}

export type TransferSlotLeaderInput = {
  targetNodeId: number
}

export type RecoverSlotInput = {
  strategy: string
}

export type ChannelRuntimeMetaListParams = {
  nodeId?: number
  channelId?: string
  limit?: number
  cursor?: string
  includeMaxMessageSeq?: boolean
}

export type ChannelClusterUnhealthyParams = {
  limit?: number
  cursor?: string
}

export type ConnectionListParams = {
  nodeId?: number
}

export type ConnectionDetailParams = {
  nodeId?: number
}

export type MessageListParams = {
  channelId: string
  channelType: number
  limit?: number
  cursor?: string
  messageId?: number
  clientMsgNo?: string
}

export type UserListParams = {
  keyword?: string
  limit?: number
  cursor?: string
}

export type ManagerUserListItem = {
  uid: string
  slot_id: number
  hash_slot: number
  online: boolean
  online_device_count: number
  online_device_flags: string[] | null
  device_count: number
  token_set_count: number
}

export type ManagerUsersResponse = {
  items: ManagerUserListItem[]
  has_more: boolean
  next_cursor?: string
}

export type ManagerSystemUser = {
  uid: string
}

export type ManagerSystemUsersResponse = {
  items: ManagerSystemUser[]
  total: number
}

export type MutateSystemUsersInput = {
  uids: string[]
}

export type MutateSystemUsersResponse = {
  uids: string[]
  changed: boolean
}

export type ManagerPluginConfigField = {
  name?: string
  type?: string
  label?: string
  placeholder?: string
  required?: boolean
  default?: unknown
  options?: string[]
  description?: string
}

export type ManagerPluginConfigTemplate = {
  fields?: ManagerPluginConfigField[]
}

export type ManagerPlugin = {
  node_id: number
  plugin_no: string
  name: string
  version: string
  config_template?: ManagerPluginConfigTemplate
  config?: Record<string, unknown>
  created_at?: string | null
  updated_at?: string | null
  status: string
  enabled: boolean
  methods: string[]
  priority: number
  persist_after_sync: boolean
  reply_sync: boolean
  is_ai: number
  pid: number
  last_seen_at: string
  last_error: string
}

export type ManagerNodePluginsResponse = {
  node_id: number
  total: number
  items: ManagerPlugin[]
}

export type ManagerPluginMutationResponse = {
  node_id: number
  plugin_no: string
  changed: boolean
  plugin?: ManagerPlugin
}

export type PluginBindingListParams = {
  uid?: string
  pluginNo?: string
  limit?: number
  cursor?: string
}

export type ManagerPluginBindingWarning = {
  code: string
  message: string
  uid?: string
  plugin_no?: string
}

export type ManagerPluginBinding = {
  uid: string
  plugin_no: string
  plugin?: ManagerPlugin
  warnings: ManagerPluginBindingWarning[]
}

export type ManagerPluginBindingsResponse = {
  items: ManagerPluginBinding[]
  total: number
  next_cursor?: string
  has_more: boolean
  warnings?: ManagerPluginBindingWarning[]
}

export type MutatePluginBindingInput = {
  uid: string
  pluginNo: string
}

export type ManagerPluginBindingMutationResponse = {
  binding: ManagerPluginBinding
  changed: boolean
  warnings?: ManagerPluginBindingWarning[]
}

export type ManagerUserDevice = {
  device_flag: string
  device_level: string
  token_set: boolean
  online: boolean
  online_session_count: number
}

export type ManagerUserConnection = {
  node_id: number
  session_id: number
  uid: string
  device_id: string
  device_flag: string
  device_level: string
  slot_id?: number
  state?: string
  listener: string
  connected_at?: string
  remote_addr: string
  local_addr: string
}

export type ManagerUserDetailResponse = {
  uid: string
  slot_id: number
  hash_slot: number
  online: boolean
  devices: ManagerUserDevice[]
  connections: ManagerUserConnection[]
}

export type KickUserInput = {
  deviceFlag: "all" | "app" | "web" | "pc"
}

export type KickUserResponse = {
  uid: string
  device_flag: string
  changed: boolean
}

export type ResetUserTokenInput = {
  deviceFlag: "app" | "web" | "pc" | "system"
  deviceLevel: "master" | "slave"
  token?: string
}

export type ResetUserTokenResponse = {
  uid: string
  device_flag: string
  device_level: string
  token: string
}


export type ManagerMessageRetentionStatus = "advanced" | "would_advance" | "noop" | "blocked"

export type ManagerMessageRetentionBlockedReason = "" | "replay_cursor" | "min_isr_match_offset" | "hw" | "checkpoint_hw" | "current_boundary"

export type AdvanceMessageRetentionInput = {
  channelId: string
  channelType: number
  throughSeq: number
  dryRun?: boolean
}

export type AdvanceMessageRetentionResponse = {
  channel_id: string
  channel_type: number
  requested_through_seq: number
  advanced_through_seq: number
  min_available_seq: number
  status: ManagerMessageRetentionStatus
  blocked_reason?: ManagerMessageRetentionBlockedReason
}

export type ManagerDiagnosticsStatus = "ok" | "error" | "timeout" | "partial" | "not_found"

export type ManagerDiagnosticsEvent = {
  trace_id?: string
  span_id?: string
  parent_span_id?: string
  stage: string
  at: string
  duration_ms?: number
  node_id?: number
  peer_node_id?: number
  slot_id?: number
  channel_key?: string
  client_msg_no?: string
  message_seq?: number
  range_start?: number
  range_end?: number
  service?: string
  result: string
  error_code?: string
  error?: string
  attempt?: number
  queue_depth?: number
  replica_role?: string
  sample_reason?: string
}

export type ManagerDiagnosticsSummary = {
  first_failure_stage?: string
  first_failure_result?: string
  first_failure_error_code?: string
  slowest_stage?: string
  slowest_duration_ms?: number
  involved_nodes: number[]
  peer_nodes: number[]
  slot_id?: number
  channel_key?: string
  client_msg_no?: string
  message_seq?: number
  event_count: number
}

export type ManagerDiagnosticsNodeResult = {
  node_id: number
  status: "ok" | "not_found" | "unavailable" | "skipped"
  duration_ms: number
  event_count: number
  notes: string[]
}

export type ManagerDiagnosticsResponse = {
  scope: "cluster" | "local_node"
  status: ManagerDiagnosticsStatus
  generated_at: string
  query: Record<string, unknown>
  summary: ManagerDiagnosticsSummary
  nodes: ManagerDiagnosticsNodeResult[]
  events: ManagerDiagnosticsEvent[]
  notes: string[]
}

export type DiagnosticsCommonParams = { nodeId?: number; limit?: number }
export type DiagnosticsMessageParams = DiagnosticsCommonParams & (
  | { clientMsgNo: string; channelKey?: never; messageSeq?: never }
  | { clientMsgNo?: never; channelKey: string; messageSeq: number }
)
export type DiagnosticsEventsParams = DiagnosticsCommonParams & { stage?: string; result?: string; uid?: string; channelKey?: string }

export type DiagnosticsTrackingTarget = "sender_uid" | "channel"
export type DiagnosticsTrackingStatus = "ok" | "partial" | "error"

export type ManagerDiagnosticsTrackingRule = {
  rule_id: string
  target: DiagnosticsTrackingTarget
  uid?: string
  channel_key?: string
  channel_id?: string
  channel_type?: number
  sample_rate: number
  created_at?: string
  expires_at?: string
}

export type ManagerDiagnosticsTrackingNodeResult = {
  node_id: number
  status: "ok" | "unavailable" | "skipped"
  notes: string[]
}

export type ManagerDiagnosticsTrackingMutationResponse = {
  status: DiagnosticsTrackingStatus
  rule: ManagerDiagnosticsTrackingRule
  nodes: ManagerDiagnosticsTrackingNodeResult[]
  notes: string[]
}

export type ManagerDiagnosticsTrackingListResponse = {
  status: DiagnosticsTrackingStatus
  rules: ManagerDiagnosticsTrackingRule[]
  nodes: ManagerDiagnosticsTrackingNodeResult[]
  notes: string[]
}

export type ManagerDiagnosticsTrackingDeleteResponse = {
  status: DiagnosticsTrackingStatus
  rule_id: string
  nodes: ManagerDiagnosticsTrackingNodeResult[]
  notes: string[]
}

export type CreateDiagnosticsTrackingRuleInput =
  | { target: "sender_uid"; uid: string; ttlSeconds: number; sampleRate?: number }
  | { target: "channel"; channelId: string; channelType: number; ttlSeconds: number; sampleRate?: number }

export type ManagerDBInspectRow = Record<string, unknown>

export type ManagerDBInspectStats = {
  scan_mode: string
  scanned_hash_slots: number[]
  scanned_rows: number
  returned_rows: number
  has_more: boolean
  next_cursor: string
}

export type ManagerDBInspectQueryResponse = {
  node_id: number
  generated_at: string
  rows: ManagerDBInspectRow[]
  stats: ManagerDBInspectStats
}

export type ManagerDBInspectTablesResponse = ManagerDBInspectQueryResponse

export type ManagerDBInspectDescribeResponse = ManagerDBInspectQueryResponse

export type ManagerDBInspectQueryInput = {
  node_id?: number
  query: string
}
