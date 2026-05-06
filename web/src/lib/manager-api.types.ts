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
