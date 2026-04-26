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

export type ManagerNode = {
  node_id: number
  addr: string
  status: string
  last_heartbeat_at: string
  is_local: boolean
  capacity_weight: number
  controller: {
    role: string
  }
  slot_stats: {
    count: number
    leader_count: number
  }
}

export type ManagerNodesResponse = {
  total: number
  items: ManagerNode[]
}

export type ManagerNodeDetailResponse = ManagerNode & {
  slots: {
    hosted_ids: number[]
    leader_ids: number[]
  }
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
    leader_id: number
    healthy_voters: number
    has_quorum: boolean
    observed_config_epoch: number
    last_report_at: string
  }
}

export type ManagerSlotsResponse = {
  total: number
  items: ManagerSlot[]
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
