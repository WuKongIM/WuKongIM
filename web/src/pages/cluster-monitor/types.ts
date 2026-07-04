export type ClusterMonitorTimeRange = "5m" | "15m" | "30m" | "1h"

export type ClusterMonitorTone = "normal" | "warning" | "critical" | "preview"

export type ClusterMonitorStage =
  | "controlPlane"
  | "slotReplication"
  | "channelReplication"
  | "internalNetwork"
  | "runtimePressure"
  | "incidentClosure"
  | "sendEntry"
  | "appendCommit"
  | "conversationSync"
  | "onlineDelivery"
  | "offlineRetry"
  | "errorClosure"

export type ClusterMonitorMetricKey =
  | "controllerApplyGap"
  | "controllerRaftStepQueueUsage"
  | "controllerRaftStepEnqueueLatencyP99"
  | "controllerRaftStepEnqueueErrorRate"
  | "controllerStateRevision"
  | "controllerActiveTasks"
  | "controllerFailedTasks"
  | "controllerNodesUnhealthy"
  | "controllerSlotLeaderSkew"
  | "controllerLeaderPresent"
  | "slotLeaderStability"
  | "slotProposeRate"
  | "slotApplyGap"
  | "slotLatencyP99"
  | "slotProposalAdmissionRejectRate"
  | "slotLeaderChangeRate"
  | "slotReplicaLagMax"
  | "slotSchedulerQueueUsage"
  | "slotSchedulerInflightUsage"
  | "slotSchedulerTaskLatencyP99"
  | "channelAppendLatencyP99"
  | "activeChannels"
  | "channelAppendBatchRecordsP95"
  | "channelAppendBatchBytesP95"
  | "channelAppendErrorRate"
  | "channelWriterAdmissionUsage"
  | "channelRuntimeFollowersParked"
  | "channelActivationRejectRate"
  | "channelReactorMailboxDepth"
  | "channelWorkerQueueDepth"
  | "channelPullHintErrorRate"
  | "channelReplicationLatencyP99"
  | "internalTraffic"
  | "internalTxTraffic"
  | "internalRxTraffic"
  | "rpcRate"
  | "rpcSuccessRate"
  | "rpcErrorRate"
  | "rpcInflight"
  | "rpcLatencyP95"
  | "rpcLatencyP99"
  | "dialSuccessRate"
  | "dialLatencyP95"
  | "internalTransportQueueUsage"
  | "internalTransportAdmissionErrorRate"
  | "workqueuePressure"
  | "nodeCpuPercent"
  | "nodeMemoryRSS"
  | "nodeGoroutines"
  | "nodeGCPauseRate"
  | "nodeGCRate"
  | "nodeGCCPUFraction"
  | "nodeGCHeapGoalUsage"
  | "storageWriteP99"
  | "storageCommitErrorRate"
  | "storageCommitQueueUsage"
  | "storagePhysicalCommitP99"
  | "storageCommitBatchRecordsP95"
  | "storageCommitBatchBytesP95"
  | "storagePebbleDiskUsage"
  | "storagePebbleReadAmplification"
  | "storagePebbleCompactionDebt"
  | "sendRate"
  | "sendSuccessRate"
  | "entryLatencyP99"
  | "messageSendRate"
  | "messageSendackErrorRate"
  | "sendQueueUsage"
  | "connectionOpenRate"
  | "connectionCloseRate"
  | "connectionCloseReasonRate"
  | "authSuccessRate"
  | "authLatencyP99"
  | "sendackErrorRate"
  | "gatewayInboundTraffic"
  | "gatewayOutboundTraffic"
  | "frameHandleLatencyP99"
  | "asyncBatchWaitP99"
  | "asyncBatchRecordsP95"
  | "asyncBatchBytesP95"
  | "authQueueUsage"
  | "transportQueueUsage"
  | "transportBytesUsage"
  | "commitRate"
  | "messageAppendErrorRate"
  | "messageAppendLatencyP95"
  | "commitLatencyP99"
  | "pendingCommitBacklog"
  | "messageDispatchEnqueueRate"
  | "messageDispatchOverflowRate"
  | "conversationSyncRate"
  | "conversationSyncLatencyP99"
  | "conversationSyncErrorRate"
  | "conversationReturnedItems"
  | "conversationRecentLoadLatencyP99"
  | "conversationActiveDirtyRows"
  | "conversationActiveNormalRows"
  | "conversationActiveCMDRows"
  | "conversationActiveNormalDirtyRows"
  | "conversationActiveCMDDirtyRows"
  | "conversationActiveOldestDirtyAge"
  | "conversationActiveFlushLatencyP99"
  | "conversationActiveFlushErrorRate"
  | "conversationAuthorityPressureRate"
  | "deliveryRate"
  | "deliveryLatencyP99"
  | "fanOutRatio"
  | "deliveryEnqueueRate"
  | "deliveryQueueUsage"
  | "deliveryRetryRate"
  | "deliveryAdmissionErrorRate"
  | "deliveryRouteExpireRate"
  | "offlineEnqueueRate"
  | "retryQueueDepth"
  | "pathErrorRate"
  | "activeConnections"

export type ClusterMonitorPoint = {
  timestamp: number
  value: number
  label?: string
  seriesKey?: string
}

export type ClusterMonitorStat = {
  labelId?: string
  label?: string
  value: string
}

export type ClusterMonitorMetricCard = {
  key: ClusterMonitorMetricKey
  titleId: string
  helpId: string
  stage: ClusterMonitorStage
  stageLabelId: string
  statusId: string
  tone: ClusterMonitorTone
  unit: string
  value: string
  available?: boolean
  error?: string
  series: ClusterMonitorPoint[]
  stats: ClusterMonitorStat[]
  chartColor: string
}

export type ClusterMonitorSnapshotEntry = {
  key: string
  labelId: string
  value: string
  unit?: string
  tone: ClusterMonitorTone
}

export type PreviewClusterMonitorModel = {
  generatedAt: string
  scopeLabelId: string
  timeRange: ClusterMonitorTimeRange
  isPaused: boolean
  snapshot: ClusterMonitorSnapshotEntry[]
  cards: ClusterMonitorMetricCard[]
}
