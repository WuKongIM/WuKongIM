import type { ClusterMonitorMetricKey, ClusterMonitorStage, ClusterMonitorTone } from "./types"

type ClusterMonitorMetricConfig = {
  titleId: string
  chartColor: string
  precision: number
  stage: ClusterMonitorStage
  tone: ClusterMonitorTone
}

export const clusterMonitorMetricConfig: Record<ClusterMonitorMetricKey, ClusterMonitorMetricConfig> = {
  controllerProposeRate: {
    titleId: "clusterMonitor.metrics.controllerProposeRate",
    chartColor: "#2563eb",
    precision: 1,
    stage: "controlPlane",
    tone: "normal",
  },
  controllerApplyGap: {
    titleId: "clusterMonitor.metrics.controllerApplyGap",
    chartColor: "#1d4ed8",
    precision: 0,
    stage: "controlPlane",
    tone: "warning",
  },
  slotLeaderStability: {
    titleId: "clusterMonitor.metrics.slotLeaderStability",
    chartColor: "#0f766e",
    precision: 2,
    stage: "slotReplication",
    tone: "normal",
  },
  slotReplicaLagP99: {
    titleId: "clusterMonitor.metrics.slotReplicaLagP99",
    chartColor: "#0891b2",
    precision: 1,
    stage: "slotReplication",
    tone: "warning",
  },
  channelISRHealth: {
    titleId: "clusterMonitor.metrics.channelISRHealth",
    chartColor: "#16a34a",
    precision: 2,
    stage: "channelReplication",
    tone: "normal",
  },
  channelAppendLatencyP99: {
    titleId: "clusterMonitor.metrics.channelAppendLatencyP99",
    chartColor: "#22c55e",
    precision: 1,
    stage: "channelReplication",
    tone: "normal",
  },
  internalTraffic: {
    titleId: "clusterMonitor.metrics.internalTraffic",
    chartColor: "#4f46e5",
    precision: 1,
    stage: "internalNetwork",
    tone: "normal",
  },
  rpcSuccessRate: {
    titleId: "clusterMonitor.metrics.rpcSuccessRate",
    chartColor: "#0284c7",
    precision: 2,
    stage: "internalNetwork",
    tone: "normal",
  },
  rpcLatencyP95: {
    titleId: "clusterMonitor.metrics.rpcLatencyP95",
    chartColor: "#7c3aed",
    precision: 1,
    stage: "internalNetwork",
    tone: "warning",
  },
  workqueuePressure: {
    titleId: "clusterMonitor.metrics.workqueuePressure",
    chartColor: "#d97706",
    precision: 1,
    stage: "runtimePressure",
    tone: "warning",
  },
  storageWriteP99: {
    titleId: "clusterMonitor.metrics.storageWriteP99",
    chartColor: "#db2777",
    precision: 1,
    stage: "runtimePressure",
    tone: "warning",
  },
  incidentRate: {
    titleId: "clusterMonitor.metrics.incidentRate",
    chartColor: "#dc2626",
    precision: 2,
    stage: "incidentClosure",
    tone: "critical",
  },
}

export const clusterMonitorStageLabelIds: Record<ClusterMonitorStage, string> = {
  controlPlane: "clusterMonitor.stage.controlPlane",
  slotReplication: "clusterMonitor.stage.slotReplication",
  channelReplication: "clusterMonitor.stage.channelReplication",
  internalNetwork: "clusterMonitor.stage.internalNetwork",
  runtimePressure: "clusterMonitor.stage.runtimePressure",
  incidentClosure: "clusterMonitor.stage.incidentClosure",
}

export const clusterMonitorStatusByTone: Record<ClusterMonitorTone, string> = {
  normal: "clusterMonitor.status.normal",
  warning: "clusterMonitor.status.warning",
  critical: "clusterMonitor.status.critical",
  preview: "clusterMonitor.status.preview",
}

export const clusterMonitorSnapshotLabelIds: Record<string, string> = {
  nodesAlive: "clusterMonitor.snapshot.nodesAlive",
  slotsReady: "clusterMonitor.snapshot.slotsReady",
  controllerApplyGap: "clusterMonitor.snapshot.controllerApplyGap",
  channelISRAnomalies: "clusterMonitor.snapshot.channelISRAnomalies",
  rpcErrorRate: "clusterMonitor.snapshot.rpcErrorRate",
  queuePressure: "clusterMonitor.snapshot.queuePressure",
  storageWriteP99: "clusterMonitor.snapshot.storageWriteP99",
}

export const clusterMonitorStatLabelIds: Record<string, string> = {
  avg: "clusterMonitor.stat.avg",
  peak: "clusterMonitor.stat.peak",
  rejected: "clusterMonitor.stat.rejected",
  p95Gap: "clusterMonitor.stat.p95Gap",
  maxGap: "clusterMonitor.stat.maxGap",
  slowNodes: "clusterMonitor.stat.slowNodes",
  leaderMissing: "clusterMonitor.stat.leaderMissing",
  quorumLost: "clusterMonitor.stat.quorumLost",
  transfers: "clusterMonitor.stat.transfers",
  p50: "clusterMonitor.stat.p50",
  p95: "clusterMonitor.stat.p95",
  laggingSlots: "clusterMonitor.stat.laggingSlots",
  isrInsufficient: "clusterMonitor.stat.isrInsufficient",
  noLeader: "clusterMonitor.stat.noLeader",
  affectedChannels: "clusterMonitor.stat.affectedChannels",
  slowAppends: "clusterMonitor.stat.slowAppends",
  tx: "clusterMonitor.stat.tx",
  rx: "clusterMonitor.stat.rx",
  callsPerSecond: "clusterMonitor.stat.callsPerSecond",
  errorsPerSecond: "clusterMonitor.stat.errorsPerSecond",
  timeouts: "clusterMonitor.stat.timeouts",
  inflight: "clusterMonitor.stat.inflight",
  busy: "clusterMonitor.stat.busy",
  critical: "clusterMonitor.stat.critical",
  queueFull: "clusterMonitor.stat.queueFull",
  flushWait: "clusterMonitor.stat.flushWait",
  warning: "clusterMonitor.stat.warning",
  topReason: "clusterMonitor.stat.topReason",
  unavailableReason: "clusterMonitor.stat.unavailableReason",
}
