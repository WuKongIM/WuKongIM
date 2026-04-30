export type ManagerPermission = {
  resource: string
  actions: string[]
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
}

// ManagerNodeScaleInProgress contains counters that explain remaining scale-in work.
export type ManagerNodeScaleInProgress = {
  assigned_slot_replicas: number
  observed_slot_replicas: number
  slot_leaders: number
  active_tasks_involving_node: number
  active_migrations_involving_node: number
  active_connections: number
  closing_connections: number
  gateway_sessions: number
  active_connections_unknown: boolean
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
  forceCloseConnections?: boolean
}

export type ManagerSlot = {
  slot_id: number
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
  max_message_seq: number
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

export type ManagerConnection = {
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
  limit?: number
  cursor?: string
}

export type MessageListParams = {
  channelId: string
  channelType: number
  limit?: number
  cursor?: string
  messageId?: number
  clientMsgNo?: string
}
