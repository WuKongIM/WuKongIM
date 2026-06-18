import type { ClusterMonitorMetricKey, ClusterMonitorStage, ClusterMonitorTone } from "./types"

type ClusterMonitorMetricConfig = {
  titleId: string
  helpId: string
  chartColor: string
  precision: number
  stage: ClusterMonitorStage
  tone: ClusterMonitorTone
}

export const clusterMonitorMetricConfig: Record<ClusterMonitorMetricKey, ClusterMonitorMetricConfig> = {
  controllerProposeRate: {
    titleId: "clusterMonitor.metrics.controllerProposeRate",
    helpId: "clusterMonitor.help.controllerProposeRate",
    chartColor: "#2563eb",
    precision: 1,
    stage: "controlPlane",
    tone: "normal",
  },
  controllerApplyGap: {
    titleId: "clusterMonitor.metrics.controllerApplyGap",
    helpId: "clusterMonitor.help.controllerApplyGap",
    chartColor: "#1d4ed8",
    precision: 0,
    stage: "controlPlane",
    tone: "warning",
  },
  slotLeaderStability: {
    titleId: "clusterMonitor.metrics.slotLeaderStability",
    helpId: "clusterMonitor.help.slotLeaderStability",
    chartColor: "#0f766e",
    precision: 2,
    stage: "slotReplication",
    tone: "normal",
  },
  slotProposeRate: {
    titleId: "clusterMonitor.metrics.slotProposeRate",
    helpId: "clusterMonitor.help.slotProposeRate",
    chartColor: "#0891b2",
    precision: 1,
    stage: "slotReplication",
    tone: "normal",
  },
  slotApplyGap: {
    titleId: "clusterMonitor.metrics.slotApplyGap",
    helpId: "clusterMonitor.help.slotApplyGap",
    chartColor: "#ca8a04",
    precision: 0,
    stage: "slotReplication",
    tone: "warning",
  },
  slotLatencyP99: {
    titleId: "clusterMonitor.metrics.slotLatencyP99",
    helpId: "clusterMonitor.help.slotLatencyP99",
    chartColor: "#16a34a",
    precision: 1,
    stage: "slotReplication",
    tone: "warning",
  },
  channelAppendLatencyP99: {
    titleId: "clusterMonitor.metrics.channelAppendLatencyP99",
    helpId: "clusterMonitor.help.channelAppendLatencyP99",
    chartColor: "#22c55e",
    precision: 1,
    stage: "channelReplication",
    tone: "normal",
  },
  activeChannels: {
    titleId: "clusterMonitor.metrics.activeChannels",
    helpId: "clusterMonitor.help.activeChannels",
    chartColor: "#0d9488",
    precision: 0,
    stage: "channelReplication",
    tone: "normal",
  },
  internalTraffic: {
    titleId: "clusterMonitor.metrics.internalTraffic",
    helpId: "clusterMonitor.help.internalTraffic",
    chartColor: "#4f46e5",
    precision: 1,
    stage: "internalNetwork",
    tone: "normal",
  },
  rpcSuccessRate: {
    titleId: "clusterMonitor.metrics.rpcSuccessRate",
    helpId: "clusterMonitor.help.rpcSuccessRate",
    chartColor: "#0284c7",
    precision: 2,
    stage: "internalNetwork",
    tone: "normal",
  },
  rpcLatencyP95: {
    titleId: "clusterMonitor.metrics.rpcLatencyP95",
    helpId: "clusterMonitor.help.rpcLatencyP95",
    chartColor: "#7c3aed",
    precision: 1,
    stage: "internalNetwork",
    tone: "warning",
  },
  workqueuePressure: {
    titleId: "clusterMonitor.metrics.workqueuePressure",
    helpId: "clusterMonitor.help.workqueuePressure",
    chartColor: "#d97706",
    precision: 1,
    stage: "runtimePressure",
    tone: "warning",
  },
  storageWriteP99: {
    titleId: "clusterMonitor.metrics.storageWriteP99",
    helpId: "clusterMonitor.help.storageWriteP99",
    chartColor: "#db2777",
    precision: 1,
    stage: "runtimePressure",
    tone: "warning",
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
  rpcErrorRate: "clusterMonitor.snapshot.rpcErrorRate",
  queuePressure: "clusterMonitor.snapshot.queuePressure",
  storageWriteP99: "clusterMonitor.snapshot.storageWriteP99",
}

export const clusterMonitorStatLabelIds: Record<string, string> = {
  avg: "clusterMonitor.stat.avg",
  peak: "clusterMonitor.stat.peak",
  latest: "clusterMonitor.stat.latest",
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
