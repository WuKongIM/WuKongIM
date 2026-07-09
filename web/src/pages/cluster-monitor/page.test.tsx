import { act, fireEvent, render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, beforeEach, expect, test, vi } from "vitest"

import { TooltipProvider } from "@/components/ui/tooltip"
import { resetLocale } from "@/i18n/locale-store"
import { I18nProvider } from "@/i18n/provider"
import { getRealtimeMonitor, getNodes } from "@/lib/manager-api"
import type { RealtimeMonitorResponse, ManagerNodesResponse } from "@/lib/manager-api.types"
import { ClusterMonitorPage } from "@/pages/cluster-monitor/page"

vi.mock("@/lib/manager-api", async () => {
  const actual = await vi.importActual<typeof import("@/lib/manager-api")>("@/lib/manager-api")
  return {
    ...actual,
    getRealtimeMonitor: vi.fn(),
    getNodes: vi.fn(),
  }
})

function renderClusterMonitorPage() {
  return render(
    <I18nProvider>
      <TooltipProvider>
        <ClusterMonitorPage />
      </TooltipProvider>
    </I18nProvider>,
  )
}

function clusterMonitorSurface(name: "toolbar" | "snapshot" | "metrics" | "loading" | "source-state") {
  return document.querySelector(`[data-cluster-monitor-surface="${name}"]`)
}

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  vi.mocked(getRealtimeMonitor).mockReset()
  vi.mocked(getNodes).mockReset()
  vi.mocked(getNodes).mockResolvedValue(managerNodesResponse())
})

afterEach(() => {
  vi.useRealTimers()
})

function managerNodesResponse(): ManagerNodesResponse {
  return {
    generated_at: "2026-06-18T10:00:00Z",
    controller_leader_id: 1,
    total: 2,
    items: [
      {
        node_id: 1,
        name: "node-1",
        addr: "127.0.0.1:11110",
        status: "alive",
        last_heartbeat_at: "2026-06-18T10:00:00Z",
        is_local: true,
        capacity_weight: 1,
        controller: { role: "leader", voter: true, leader_id: 1, raft_health: "healthy" },
        slot_stats: { count: 8, leader_count: 4 },
      },
      {
        node_id: 2,
        name: "node-2",
        addr: "127.0.0.1:11111",
        status: "alive",
        last_heartbeat_at: "2026-06-18T10:00:00Z",
        is_local: false,
        capacity_weight: 1,
        controller: { role: "follower", voter: true, leader_id: 1, raft_health: "healthy" },
        slot_stats: { count: 8, leader_count: 4 },
      },
    ],
  }
}

function readyClusterMonitorResponse(): RealtimeMonitorResponse {
  return {
    status: "ready" as const,
    generated_at: "2026-06-18T10:00:00Z",
    window_seconds: 900,
    step_seconds: 20,
    scope: { view: "realtime_monitor" },
    sources: {
      prometheus: { enabled: true, base_url: "http://127.0.0.1:9090", query_ms: 12, error: "" },
      control_snapshot: { enabled: true, query_ms: 2, error: "" },
    },
    categories: [
      { key: "common", count: 2 },
      { key: "control", count: 1 },
      { key: "internal", count: 1 },
    ],
    snapshot: [
      { key: "nodesAlive", metric_key: "controllerApplyGap", source: "control_snapshot" as const, value: 3, tone: "normal" as const },
      { key: "rpcErrorRate", metric_key: "rpcSuccessRate", source: "prometheus" as const, value: 0.14, unit: "%", tone: "normal" as const },
    ],
    cards: [
      {
        key: "controllerApplyGap",
        category: "control" as const,
        source: "prometheus" as const,
        stage: "controlPlane",
        tone: "warning" as const,
        unit: "entries",
        value: 15,
        available: true,
        error: "",
        series: [
          { timestamp: 1781767200000, value: 13 },
          { timestamp: 1781767220000, value: 15 },
        ],
        stats: [
          { key: "p95Gap", value: 15 },
          { key: "maxGap", value: 15 },
          { key: "slowNodes", text: "1" },
        ],
      },
      {
        key: "rpcSuccessRate",
        category: "internal" as const,
        source: "prometheus" as const,
        stage: "internalNetwork",
        tone: "normal" as const,
        unit: "%",
        value: 99.86,
        available: true,
        error: "",
        series: [
          { timestamp: 1781767200000, value: 99.8 },
          { timestamp: 1781767220000, value: 99.86 },
        ],
        stats: [
          { key: "callsPerSecond", text: "2.8k" },
          { key: "errorsPerSecond", value: 3.8 },
          { key: "timeouts", value: 9 },
        ],
      },
    ],
  }
}

function businessRealtimeMonitorResponse(): RealtimeMonitorResponse {
  return {
    ...readyClusterMonitorResponse(),
    snapshot: [],
    categories: [
      { key: "common", count: 3 },
      { key: "gateway", count: 1 },
      { key: "message", count: 1 },
      { key: "conversation", count: 1 },
    ],
    cards: [
      {
        key: "sendRate",
        category: "gateway" as const,
        source: "prometheus" as const,
        stage: "sendEntry",
        tone: "normal" as const,
        unit: "msg/s",
        value: 128.4,
        available: true,
        error: "",
        series: [
          { timestamp: 1781767200000, value: 121.2 },
          { timestamp: 1781767220000, value: 128.4 },
        ],
        stats: [
          { key: "avg", value: 124.8 },
          { key: "peak", value: 132.6 },
          { key: "total", value: 1_250, unit: "msg" },
        ],
      },
      {
        key: "deliveryLatencyP99",
        category: "message" as const,
        source: "prometheus" as const,
        stage: "onlineDelivery",
        tone: "warning" as const,
        unit: "ms",
        value: 48.2,
        available: true,
        error: "",
        series: [
          { timestamp: 1781767200000, value: 42.8 },
          { timestamp: 1781767220000, value: 48.2 },
        ],
        stats: [
          { key: "avg", value: 45.1 },
          { key: "peak", value: 51.6 },
        ],
      },
      {
        key: "conversationSyncRate",
        category: "conversation" as const,
        source: "prometheus" as const,
        stage: "conversationSync",
        tone: "normal" as const,
        unit: "req/s",
        value: 36.9,
        available: true,
        error: "",
        series: [
          { timestamp: 1781767200000, value: 33.4 },
          { timestamp: 1781767220000, value: 36.9 },
        ],
        stats: [
          { key: "avg", value: 35.2 },
          { key: "peak", value: 39.1 },
        ],
      },
    ],
  }
}

function gatewayOperatorMonitorResponse(): RealtimeMonitorResponse {
  return {
    ...readyClusterMonitorResponse(),
    snapshot: [],
    categories: [
      { key: "common", count: 3 },
      { key: "gateway", count: 20 },
    ],
    cards: [
      {
        key: "sendQueueUsage",
        category: "gateway" as const,
        source: "prometheus" as const,
        stage: "sendEntry",
        tone: "warning" as const,
        unit: "%",
        value: 62.5,
        available: true,
        error: "",
        series: [
          { timestamp: 1781767200000, value: 51.2 },
          { timestamp: 1781767220000, value: 62.5 },
        ],
        stats: [
          { key: "avg", value: 56.8 },
          { key: "peak", value: 62.5 },
        ],
      },
      {
        key: "connectionOpenRate",
        category: "gateway" as const,
        source: "prometheus" as const,
        stage: "sendEntry",
        tone: "normal" as const,
        unit: "conn/s",
        value: 12.8,
        available: true,
        error: "",
        series: [
          { timestamp: 1781767200000, value: 10.1 },
          { timestamp: 1781767220000, value: 12.8 },
        ],
        stats: [],
      },
      {
        key: "connectionCloseRate",
        category: "gateway" as const,
        source: "prometheus" as const,
        stage: "sendEntry",
        tone: "warning" as const,
        unit: "conn/s",
        value: 9.2,
        available: true,
        error: "",
        series: [
          { timestamp: 1781767200000, value: 6.4 },
          { timestamp: 1781767220000, value: 9.2 },
        ],
        stats: [],
      },
      {
        key: "connectionCloseReasonRate",
        category: "gateway" as const,
        source: "prometheus" as const,
        stage: "incidentClosure",
        tone: "warning" as const,
        unit: "conn/s",
        value: 4.8,
        available: true,
        error: "",
        series: [
          { timestamp: 1781767200000, value: 3.1, label: "idle", series_key: "reason=idle" },
          { timestamp: 1781767220000, value: 4.8, label: "idle", series_key: "reason=idle" },
        ],
        stats: [],
      },
      {
        key: "authSuccessRate",
        category: "gateway" as const,
        source: "prometheus" as const,
        stage: "sendEntry",
        tone: "normal" as const,
        unit: "%",
        value: 99.2,
        available: true,
        error: "",
        series: [
          { timestamp: 1781767200000, value: 98.9 },
          { timestamp: 1781767220000, value: 99.2 },
        ],
        stats: [],
      },
      {
        key: "authLatencyP99",
        category: "gateway" as const,
        source: "prometheus" as const,
        stage: "sendEntry",
        tone: "warning" as const,
        unit: "ms",
        value: 18.6,
        available: true,
        error: "",
        series: [
          { timestamp: 1781767200000, value: 16.4 },
          { timestamp: 1781767220000, value: 18.6 },
        ],
        stats: [],
      },
      {
        key: "sendackErrorRate",
        category: "gateway" as const,
        source: "prometheus" as const,
        stage: "incidentClosure",
        tone: "critical" as const,
        unit: "%",
        value: 0.42,
        available: true,
        error: "",
        series: [
          { timestamp: 1781767200000, value: 0.12 },
          { timestamp: 1781767220000, value: 0.42 },
        ],
        stats: [],
      },
      {
        key: "gatewayInboundTraffic",
        category: "gateway" as const,
        source: "prometheus" as const,
        stage: "sendEntry",
        tone: "normal" as const,
        unit: "B/s",
        value: 262_144,
        available: true,
        error: "",
        series: [
          { timestamp: 1781767200000, value: 131_072 },
          { timestamp: 1781767220000, value: 262_144 },
        ],
        stats: [],
      },
      {
        key: "gatewayOutboundTraffic",
        category: "gateway" as const,
        source: "prometheus" as const,
        stage: "sendEntry",
        tone: "normal" as const,
        unit: "B/s",
        value: 524_288,
        available: true,
        error: "",
        series: [
          { timestamp: 1781767200000, value: 262_144 },
          { timestamp: 1781767220000, value: 524_288 },
        ],
        stats: [],
      },
      {
        key: "frameHandleLatencyP99",
        category: "gateway" as const,
        source: "prometheus" as const,
        stage: "sendEntry",
        tone: "warning" as const,
        unit: "ms",
        value: 21.4,
        available: true,
        error: "",
        series: [
          { timestamp: 1781767200000, value: 18.2, label: "SEND", series_key: "frame_type=SEND" },
          { timestamp: 1781767220000, value: 21.4, label: "SEND", series_key: "frame_type=SEND" },
        ],
        stats: [],
      },
      {
        key: "asyncBatchWaitP99",
        category: "gateway" as const,
        source: "prometheus" as const,
        stage: "sendEntry",
        tone: "warning" as const,
        unit: "ms",
        value: 7.8,
        available: true,
        error: "",
        series: [
          { timestamp: 1781767200000, value: 6.2 },
          { timestamp: 1781767220000, value: 7.8 },
        ],
        stats: [],
      },
      {
        key: "asyncBatchRecordsP95",
        category: "gateway" as const,
        source: "prometheus" as const,
        stage: "sendEntry",
        tone: "normal" as const,
        unit: "records",
        value: 64,
        available: true,
        error: "",
        series: [
          { timestamp: 1781767200000, value: 32 },
          { timestamp: 1781767220000, value: 64 },
        ],
        stats: [],
      },
      {
        key: "asyncBatchBytesP95",
        category: "gateway" as const,
        source: "prometheus" as const,
        stage: "sendEntry",
        tone: "normal" as const,
        unit: "B",
        value: 65_536,
        available: true,
        error: "",
        series: [
          { timestamp: 1781767200000, value: 32_768 },
          { timestamp: 1781767220000, value: 65_536 },
        ],
        stats: [],
      },
      {
        key: "authQueueUsage",
        category: "gateway" as const,
        source: "prometheus" as const,
        stage: "runtimePressure",
        tone: "warning" as const,
        unit: "%",
        value: 41.2,
        available: true,
        error: "",
        series: [
          { timestamp: 1781767200000, value: 21.4 },
          { timestamp: 1781767220000, value: 41.2 },
        ],
        stats: [],
      },
      {
        key: "internalTransportQueueUsage",
        category: "gateway" as const,
        source: "prometheus" as const,
        stage: "runtimePressure",
        tone: "warning" as const,
        unit: "%",
        value: 72.1,
        available: true,
        error: "",
        series: [
          { timestamp: 1781767200000, value: 43.5 },
          { timestamp: 1781767220000, value: 72.1 },
        ],
        stats: [],
      },
      {
        key: "transportBytesUsage",
        category: "gateway" as const,
        source: "prometheus" as const,
        stage: "runtimePressure",
        tone: "warning" as const,
        unit: "%",
        value: 80.6,
        available: true,
        error: "",
        series: [
          { timestamp: 1781767200000, value: 62.4 },
          { timestamp: 1781767220000, value: 80.6 },
        ],
        stats: [],
      },
    ],
  }
}

function partialClusterMonitorResponse(): RealtimeMonitorResponse {
  return {
    ...readyClusterMonitorResponse(),
    status: "partial" as const,
    sources: {
      prometheus: { enabled: true, base_url: "http://127.0.0.1:9090", query_ms: 12, error: "query timed out for apply gap" },
      control_snapshot: { enabled: true, query_ms: 2, error: "" },
    },
    cards: [
      readyClusterMonitorResponse().cards[1],
      {
        key: "controllerApplyGap",
        category: "control" as const,
        source: "prometheus" as const,
        stage: "controlPlane",
        tone: "warning" as const,
        unit: "",
        value: 0,
        available: false,
        error: "prometheus series unavailable",
        series: [],
        stats: [],
      },
      {
        key: "unknownMetric",
        category: "control" as const,
        source: "prometheus" as const,
        stage: "controlPlane",
        tone: "critical" as const,
        unit: "",
        value: 999,
        available: true,
        error: "",
        series: [],
        stats: [],
      },
    ],
  }
}

function largeTrafficClusterMonitorResponse(): RealtimeMonitorResponse {
  return {
    ...readyClusterMonitorResponse(),
    snapshot: [],
    cards: [
      {
        key: "internalTraffic",
        category: "internal" as const,
        source: "prometheus" as const,
        stage: "internalNetwork",
        tone: "normal" as const,
        unit: "B/s",
        value: 398006.3,
        available: true,
        error: "",
        series: [
          { timestamp: 1781767200000, value: 293371.6 },
          { timestamp: 1781767220000, value: 398006.3 },
        ],
        stats: [
          { key: "avg", value: 293371.6 },
          { key: "peak", value: 398006.3 },
        ],
      },
    ],
  }
}

function internalOperatorMonitorResponse(): RealtimeMonitorResponse {
  return {
    ...readyClusterMonitorResponse(),
    snapshot: [],
    categories: [
      { key: "common", count: 3 },
      { key: "internal", count: 13 },
    ],
    cards: [
      {
        key: "internalTxTraffic",
        category: "internal" as const,
        source: "prometheus" as const,
        stage: "internalNetwork",
        tone: "normal" as const,
        unit: "B/s",
        value: 262_144,
        available: true,
        error: "",
        series: [
          { timestamp: 1781767200000, value: 131_072 },
          { timestamp: 1781767220000, value: 262_144 },
        ],
        stats: [],
      },
      {
        key: "internalRxTraffic",
        category: "internal" as const,
        source: "prometheus" as const,
        stage: "internalNetwork",
        tone: "normal" as const,
        unit: "B/s",
        value: 393_216,
        available: true,
        error: "",
        series: [
          { timestamp: 1781767200000, value: 262_144 },
          { timestamp: 1781767220000, value: 393_216 },
        ],
        stats: [],
      },
      {
        key: "rpcRate",
        category: "internal" as const,
        source: "prometheus" as const,
        stage: "internalNetwork",
        tone: "normal" as const,
        unit: "calls/s",
        value: 180,
        available: true,
        error: "",
        series: [
          { timestamp: 1781767200000, value: 150 },
          { timestamp: 1781767220000, value: 180 },
        ],
        stats: [],
      },
      {
        key: "rpcErrorRate",
        category: "internal" as const,
        source: "prometheus" as const,
        stage: "internalNetwork",
        tone: "critical" as const,
        unit: "%",
        value: 0.8,
        available: true,
        error: "",
        series: [
          { timestamp: 1781767200000, value: 0.2 },
          { timestamp: 1781767220000, value: 0.8 },
        ],
        stats: [],
      },
      {
        key: "rpcInflight",
        category: "internal" as const,
        source: "prometheus" as const,
        stage: "internalNetwork",
        tone: "warning" as const,
        unit: "",
        value: 42,
        available: true,
        error: "",
        series: [
          { timestamp: 1781767200000, value: 30 },
          { timestamp: 1781767220000, value: 42 },
        ],
        stats: [],
      },
      {
        key: "rpcLatencyP99",
        category: "internal" as const,
        source: "prometheus" as const,
        stage: "internalNetwork",
        tone: "warning" as const,
        unit: "ms",
        value: 48.6,
        available: true,
        error: "",
        series: [
          { timestamp: 1781767200000, value: 31.2 },
          { timestamp: 1781767220000, value: 48.6 },
        ],
        stats: [],
      },
      {
        key: "dialSuccessRate",
        category: "internal" as const,
        source: "prometheus" as const,
        stage: "internalNetwork",
        tone: "normal" as const,
        unit: "%",
        value: 99.4,
        available: true,
        error: "",
        series: [
          { timestamp: 1781767200000, value: 99.1 },
          { timestamp: 1781767220000, value: 99.4 },
        ],
        stats: [],
      },
      {
        key: "dialLatencyP95",
        category: "internal" as const,
        source: "prometheus" as const,
        stage: "internalNetwork",
        tone: "warning" as const,
        unit: "ms",
        value: 16.8,
        available: true,
        error: "",
        series: [
          { timestamp: 1781767200000, value: 12.4 },
          { timestamp: 1781767220000, value: 16.8 },
        ],
        stats: [],
      },
      {
        key: "internalTransportQueueUsage",
        category: "internal" as const,
        source: "prometheus" as const,
        stage: "runtimePressure",
        tone: "warning" as const,
        unit: "%",
        value: 71.5,
        available: true,
        error: "",
        series: [
          { timestamp: 1781767200000, value: 45.2 },
          { timestamp: 1781767220000, value: 71.5 },
        ],
        stats: [],
      },
      {
        key: "internalTransportAdmissionErrorRate",
        category: "internal" as const,
        source: "prometheus" as const,
        stage: "runtimePressure",
        tone: "critical" as const,
        unit: "%",
        value: 1.2,
        available: true,
        error: "",
        series: [
          { timestamp: 1781767200000, value: 0.3 },
          { timestamp: 1781767220000, value: 1.2 },
        ],
        stats: [],
      },
    ],
  }
}

function messageOperatorCard(
  key: string,
  stage: string,
  tone: "normal" | "warning" | "critical",
  unit: string,
  value: number,
): RealtimeMonitorResponse["cards"][number] {
  return {
    key,
    category: "message",
    source: "prometheus",
    stage,
    tone,
    unit,
    value,
    available: true,
    error: "",
    series: [
      { timestamp: 1781767200000, value: value * 0.8 },
      { timestamp: 1781767220000, value },
    ],
    stats: [],
  }
}

function messageOperatorMonitorResponse(): RealtimeMonitorResponse {
  return {
    ...readyClusterMonitorResponse(),
    snapshot: [],
    categories: [
      { key: "common", count: 3 },
      { key: "message", count: 19 },
    ],
    cards: [
      messageOperatorCard("messageSendRate", "sendEntry", "normal", "msg/s", 216),
      messageOperatorCard("messageSendackErrorRate", "errorClosure", "critical", "%", 0.42),
      messageOperatorCard("messageAppendErrorRate", "appendCommit", "critical", "%", 0.28),
      messageOperatorCard("messageAppendLatencyP95", "appendCommit", "warning", "ms", 28.4),
      messageOperatorCard("messageDispatchEnqueueRate", "appendCommit", "normal", "msg/s", 208),
      messageOperatorCard("messageDispatchOverflowRate", "appendCommit", "critical", "events/s", 0.4),
      messageOperatorCard("deliveryEnqueueRate", "onlineDelivery", "normal", "msg/s", 204),
      messageOperatorCard("deliveryQueueUsage", "onlineDelivery", "warning", "%", 63.5),
      messageOperatorCard("deliveryRetryRate", "offlineRetry", "warning", "events/s", 3.2),
      messageOperatorCard("deliveryAdmissionErrorRate", "onlineDelivery", "critical", "%", 1.1),
      messageOperatorCard("deliveryRouteExpireRate", "errorClosure", "critical", "events/s", 0.7),
    ],
  }
}

function channelOperatorCard(
  key: string,
  stage: string,
  tone: "normal" | "warning" | "critical",
  unit: string,
  value: number,
): RealtimeMonitorResponse["cards"][number] {
  return {
    key,
    category: "channel",
    source: "prometheus",
    stage,
    tone,
    unit,
    value,
    available: true,
    error: "",
    series: [
      { timestamp: 1781767200000, value: value * 0.8 },
      { timestamp: 1781767220000, value },
    ],
    stats: [],
  }
}

function channelOperatorMonitorResponse(): RealtimeMonitorResponse {
  return {
    ...readyClusterMonitorResponse(),
    snapshot: [],
    categories: [
      { key: "common", count: 3 },
      { key: "channel", count: 12 },
    ],
    cards: [
      channelOperatorCard("channelAppendLatencyP99", "channelReplication", "warning", "ms", 42),
      channelOperatorCard("activeChannels", "channelReplication", "normal", "", 1024),
      channelOperatorCard("channelAppendBatchRecordsP95", "channelReplication", "normal", "records", 64),
      channelOperatorCard("channelAppendBatchBytesP95", "channelReplication", "normal", "B", 32768),
      channelOperatorCard("channelAppendErrorRate", "channelReplication", "critical", "%", 0.35),
      channelOperatorCard("channelWriterAdmissionUsage", "runtimePressure", "warning", "%", 68),
      channelOperatorCard("channelRuntimeFollowersParked", "channelReplication", "warning", "", 128),
      channelOperatorCard("channelActivationRejectRate", "channelReplication", "critical", "events/s", 1.2),
      channelOperatorCard("channelReactorMailboxDepth", "runtimePressure", "warning", "", 23),
      channelOperatorCard("channelWorkerQueueDepth", "runtimePressure", "warning", "", 48),
      channelOperatorCard("channelPullHintErrorRate", "channelReplication", "critical", "%", 2.1),
      channelOperatorCard("channelReplicationLatencyP99", "channelReplication", "warning", "ms", 55),
    ],
  }
}

function databaseOperatorCard(
  key: string,
  stage: string,
  tone: "normal" | "warning" | "critical",
  unit: string,
  value: number,
): RealtimeMonitorResponse["cards"][number] {
  return {
    key,
    category: "database",
    source: "prometheus",
    stage,
    tone,
    unit,
    value,
    available: true,
    error: "",
    series: [
      { timestamp: 1781767200000, value: value * 0.8 },
      { timestamp: 1781767220000, value },
    ],
    stats: [],
  }
}

function databaseOperatorMonitorResponse(): RealtimeMonitorResponse {
  return {
    ...readyClusterMonitorResponse(),
    snapshot: [],
    categories: [
      { key: "common", count: 3 },
      { key: "database", count: 9 },
    ],
    cards: [
      databaseOperatorCard("storageWriteP99", "runtimePressure", "warning", "ms", 38.4),
      databaseOperatorCard("storageCommitErrorRate", "incidentClosure", "critical", "%", 0.25),
      databaseOperatorCard("storageCommitQueueUsage", "runtimePressure", "warning", "%", 61.5),
      databaseOperatorCard("storagePhysicalCommitP99", "runtimePressure", "warning", "ms", 24.2),
      databaseOperatorCard("storageCommitBatchRecordsP95", "runtimePressure", "normal", "records", 128),
      databaseOperatorCard("storageCommitBatchBytesP95", "runtimePressure", "normal", "B", 65_536),
      databaseOperatorCard("storagePebbleDiskUsage", "runtimePressure", "warning", "B", 1_048_576),
      databaseOperatorCard("storagePebbleReadAmplification", "runtimePressure", "warning", "", 4),
      databaseOperatorCard("storagePebbleCompactionDebt", "runtimePressure", "warning", "B", 262_144),
    ],
  }
}

function slotOperatorCard(
  key: string,
  stage: string,
  tone: "normal" | "warning" | "critical",
  unit: string,
  value: number,
): RealtimeMonitorResponse["cards"][number] {
  return {
    key,
    category: "slot",
    source: "prometheus",
    stage,
    tone,
    unit,
    value,
    available: true,
    error: "",
    series: [
      { timestamp: 1781767200000, value: value * 0.8 },
      { timestamp: 1781767220000, value },
    ],
    stats: [],
  }
}

function slotOperatorMonitorResponse(): RealtimeMonitorResponse {
  return {
    ...readyClusterMonitorResponse(),
    snapshot: [],
    categories: [
      { key: "common", count: 3 },
      { key: "slot", count: 10 },
    ],
    cards: [
      slotOperatorCard("slotLeaderStability", "slotReplication", "normal", "%", 99.8),
      slotOperatorCard("slotProposeRate", "slotReplication", "normal", "cmd/s", 180),
      slotOperatorCard("slotApplyGap", "slotReplication", "warning", "entries", 2),
      slotOperatorCard("slotLatencyP99", "slotReplication", "warning", "ms", 36),
      slotOperatorCard("slotProposalAdmissionRejectRate", "slotReplication", "critical", "%", 0.8),
      slotOperatorCard("slotLeaderChangeRate", "slotReplication", "warning", "events/s", 0.2),
      slotOperatorCard("slotReplicaLagMax", "slotReplication", "warning", "s", 1.4),
      slotOperatorCard("slotSchedulerQueueUsage", "runtimePressure", "warning", "%", 61),
      slotOperatorCard("slotSchedulerInflightUsage", "runtimePressure", "warning", "%", 45),
      slotOperatorCard("slotSchedulerTaskLatencyP99", "runtimePressure", "warning", "ms", 22),
    ],
  }
}

function largeTrafficClusterMonitorResponseWithoutStats(): RealtimeMonitorResponse {
  const response = largeTrafficClusterMonitorResponse()
  delete (response.cards[0] as unknown as Record<string, unknown>).stats
  return response
}

function burstyTrafficClusterMonitorResponse(): RealtimeMonitorResponse {
  return {
    ...readyClusterMonitorResponse(),
    snapshot: [],
    cards: [
      {
        key: "internalTraffic",
        category: "internal" as const,
        source: "prometheus" as const,
        stage: "internalNetwork",
        tone: "normal" as const,
        unit: "B/s",
        value: 4_358_584.201439876,
        available: true,
        error: "",
        series: [
          { timestamp: 1781767200000, value: 4_200_000_000 },
          { timestamp: 1781767220000, value: 4_358_584.201439876 },
        ],
        stats: [
          { key: "avg", value: 4_058_584.201439876 },
          { key: "peak", value: 4_358_584.201439876 },
        ],
      },
    ],
  }
}

function nodeMemoryClusterMonitorResponse(): RealtimeMonitorResponse {
  return {
    ...readyClusterMonitorResponse(),
    snapshot: [],
    cards: [
      {
        key: "nodeMemoryRSS",
        category: "node" as const,
        source: "prometheus" as const,
        stage: "runtimePressure",
        tone: "warning" as const,
        unit: "B",
        value: 536_870_912,
        available: true,
        error: "",
        series: [
          { timestamp: 1781767200000, value: 402_653_184 },
          { timestamp: 1781767220000, value: 536_870_912 },
        ],
        stats: [
          { key: "avg", value: 469_762_048 },
          { key: "peak", value: 536_870_912 },
        ],
      },
    ],
  }
}

function allNodeCpuClusterMonitorResponse(): RealtimeMonitorResponse {
  return {
    ...readyClusterMonitorResponse(),
    snapshot: [],
    cards: [
      {
        key: "nodeCpuPercent",
        category: "node" as const,
        source: "prometheus" as const,
        stage: "runtimePressure",
        tone: "warning" as const,
        unit: "%",
        value: 40,
        available: true,
        error: "",
        series: [
          { timestamp: 1781767200000, value: 12.5, label: "node-1", series_key: "node-1" },
          { timestamp: 1781767200000, value: 32.5, label: "node-2", series_key: "node-2" },
          { timestamp: 1781767220000, value: 15, label: "node-1", series_key: "node-1" },
          { timestamp: 1781767220000, value: 40, label: "node-2", series_key: "node-2" },
        ],
        stats: [
          { key: "node", label: "node-1", value: 15, unit: "%" },
          { key: "node", label: "node-2", value: 40, unit: "%" },
        ],
      },
    ],
  }
}

function nodeGCClusterMonitorResponse(): RealtimeMonitorResponse {
  return {
    ...readyClusterMonitorResponse(),
    snapshot: [],
    cards: [
      {
        key: "nodeGCPauseRate",
        category: "node" as const,
        source: "prometheus" as const,
        stage: "runtimePressure",
        tone: "warning" as const,
        unit: "ms/s",
        value: 0.75,
        available: true,
        error: "",
        series: [
          { timestamp: 1781767200000, value: 0.25, label: "node-1", series_key: "node-1" },
          { timestamp: 1781767220000, value: 0.75, label: "node-2", series_key: "node-2" },
        ],
        stats: [
          { key: "node", label: "node-1", value: 0.5, unit: "ms/s" },
          { key: "node", label: "node-2", value: 0.75, unit: "ms/s" },
        ],
      },
      {
        key: "nodeGCRate",
        category: "node" as const,
        source: "prometheus" as const,
        stage: "runtimePressure",
        tone: "warning" as const,
        unit: "events/s",
        value: 0.08,
        available: true,
        error: "",
        series: [{ timestamp: 1781767220000, value: 0.08 }],
        stats: [{ key: "peak", value: 0.08, unit: "events/s" }],
      },
      {
        key: "nodeGCCPUFraction",
        category: "node" as const,
        source: "prometheus" as const,
        stage: "runtimePressure",
        tone: "warning" as const,
        unit: "%",
        value: 1.2,
        available: true,
        error: "",
        series: [{ timestamp: 1781767220000, value: 1.2 }],
        stats: [{ key: "peak", value: 1.2, unit: "%" }],
      },
      {
        key: "nodeGCHeapGoalUsage",
        category: "node" as const,
        source: "prometheus" as const,
        stage: "runtimePressure",
        tone: "warning" as const,
        unit: "%",
        value: 68.4,
        available: true,
        error: "",
        series: [{ timestamp: 1781767220000, value: 68.4 }],
        stats: [{ key: "peak", value: 68.4, unit: "%" }],
      },
    ],
  }
}

function disabledClusterMonitorResponse(): RealtimeMonitorResponse {
  return {
    status: "prometheus_disabled" as const,
    generated_at: "2026-06-18T10:00:00Z",
    window_seconds: 900,
    step_seconds: 20,
    scope: { view: "realtime_monitor" },
    sources: {
      prometheus: {
        enabled: false,
        base_url: "",
        query_ms: 0,
        error:
          "prometheus is disabled; set WK_METRICS_ENABLE=true and either WK_PROMETHEUS_QUERY_BASE_URL or WK_PROMETHEUS_ENABLE=true",
      },
      control_snapshot: { enabled: true, query_ms: 1, error: "" },
    },
    categories: [],
    snapshot: [],
    cards: [],
  }
}

function unavailableClusterMonitorResponse(): RealtimeMonitorResponse {
  return {
    status: "prometheus_unavailable" as const,
    generated_at: "2026-06-18T10:00:00Z",
    window_seconds: 900,
    step_seconds: 20,
    scope: { view: "realtime_monitor" },
    sources: {
      prometheus: {
        enabled: true,
        base_url: "http://127.0.0.1:9090",
        query_ms: 0,
        error: "dial tcp 127.0.0.1:9090: connect: connection refused",
      },
      control_snapshot: { enabled: true, query_ms: 1, error: "" },
    },
    categories: [],
    snapshot: [],
    cards: [],
  }
}

test("renders cluster monitor cards from realtime API data", async () => {
  vi.mocked(getRealtimeMonitor).mockResolvedValueOnce(readyClusterMonitorResponse())
  renderClusterMonitorPage()

  expect(screen.getByRole("heading", { name: "Live Monitor" })).toBeInTheDocument()
  expect(screen.getByRole("heading", { name: /live monitor/i }).closest("section")).toHaveClass("border-b")
  expect(await screen.findByText("Live Data")).toBeInTheDocument()
  expect(screen.queryByText("UI Preview")).not.toBeInTheDocument()
  expect(screen.getByText("Cluster control plane, replication, internal network, queue, and storage watermarks.")).toBeInTheDocument()

  const cards = await screen.findAllByTestId("cluster-monitor-metric-card")
  expect(cards).toHaveLength(2)
  const toolbar = screen.getByLabelText(/category/i).closest("section")
  expect(toolbar).toHaveAttribute("data-monitor-toolbar", "true")
  expect(toolbar).toHaveAttribute("data-cluster-monitor-surface", "toolbar")
  expect(clusterMonitorSurface("snapshot")).toBeInTheDocument()
  expect(clusterMonitorSurface("metrics")).toBeInTheDocument()
  expect(screen.getAllByTestId("cluster-monitor-snapshot-cell").length).toBeGreaterThan(0)
  expect(clusterMonitorSurface("loading")).not.toBeInTheDocument()
  expect(clusterMonitorSurface("source-state")).not.toBeInTheDocument()
  expect(cards[0]).toHaveClass("shadow-none")
  expect(within(cards[0]).getByText("Controller Apply Gap")).toBeInTheDocument()
  expect(within(cards[0]).getByText("15")).toBeInTheDocument()
  expect(within(cards[0]).getByText("Slow Nodes")).toBeInTheDocument()
  expect(within(cards[1]).getByText("RPC Success Rate")).toBeInTheDocument()
  expect(within(cards[1]).getByText("2.8k")).toBeInTheDocument()

  expect(screen.getByText("Nodes Alive")).toBeInTheDocument()
  expect(screen.getByText("RPC Errors")).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Refresh now" })).toBeInTheDocument()
  expect(screen.getByRole("combobox", { name: "Auto refresh" })).toHaveValue("30s")
  expect(getRealtimeMonitor).toHaveBeenCalledWith({ window: "15m", category: "common" })
})

test("renders former business realtime monitor cards in cluster monitor page", async () => {
  vi.mocked(getRealtimeMonitor).mockResolvedValueOnce(businessRealtimeMonitorResponse())
  renderClusterMonitorPage()

  const cards = await screen.findAllByTestId("cluster-monitor-metric-card")
  expect(cards).toHaveLength(3)
  expect(within(cards[0]).getByText("Send Rate")).toBeInTheDocument()
  expect(within(cards[0]).getByText("Send Entry")).toBeInTheDocument()
  expect(within(cards[0]).getByText("128.4")).toBeInTheDocument()
  expect(within(cards[0]).getByText("Total")).toBeInTheDocument()
  expect(within(cards[0]).getByText("1,250 msg")).toBeInTheDocument()
  expect(within(cards[1]).getByText("Delivery Latency P99")).toBeInTheDocument()
  expect(within(cards[1]).getByText("Online Delivery")).toBeInTheDocument()
  expect(within(cards[2]).getByText("Conversation Sync Rate")).toBeInTheDocument()
  expect(within(cards[2]).getByText("Conversation Sync")).toBeInTheDocument()
})

test("renders gateway operator cards from realtime API data", async () => {
  vi.mocked(getRealtimeMonitor).mockResolvedValueOnce(gatewayOperatorMonitorResponse())
  renderClusterMonitorPage()

  const cards = await screen.findAllByTestId("cluster-monitor-metric-card")
  expect(cards).toHaveLength(16)
  expect(within(cards[0]).getByText("Gateway Send Queue")).toBeInTheDocument()
  expect(within(cards[0]).getByText("62.5")).toBeInTheDocument()
  expect(within(cards[1]).getByText("Connection Opens")).toBeInTheDocument()
  expect(within(cards[2]).getByText("Connection Closes")).toBeInTheDocument()
  expect(within(cards[3]).getByText("Close Reasons")).toBeInTheDocument()
  expect(within(cards[4]).getByText("Auth Success Rate")).toBeInTheDocument()
  expect(within(cards[5]).getByText("Auth Latency P99")).toBeInTheDocument()
  expect(within(cards[6]).getByText("Sendack Error Rate")).toBeInTheDocument()
  expect(within(cards[7]).getByText("Gateway Inbound Traffic")).toBeInTheDocument()
  expect(within(cards[8]).getByText("Gateway Outbound Traffic")).toBeInTheDocument()
  expect(within(cards[9]).getByText("Frame Latency P99")).toBeInTheDocument()
  expect(within(cards[10]).getByText("Async Batch Wait P99")).toBeInTheDocument()
  expect(within(cards[11]).getByText("Async Batch Records P95")).toBeInTheDocument()
  expect(within(cards[12]).getByText("Async Batch Bytes P95")).toBeInTheDocument()
  expect(within(cards[13]).getByText("Auth Queue Usage")).toBeInTheDocument()
  expect(within(cards[14]).getByText("Transport Queue Usage")).toBeInTheDocument()
  expect(within(cards[15]).getByText("Transport Bytes Usage")).toBeInTheDocument()
})

test("shows metric explanations from card help buttons", async () => {
  const user = userEvent.setup()
  vi.mocked(getRealtimeMonitor).mockResolvedValueOnce(readyClusterMonitorResponse())
  renderClusterMonitorPage()

  expect(await screen.findByRole("button", { name: "Explain Controller Apply Gap" })).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Explain RPC Success Rate" })).toBeInTheDocument()

  await user.click(screen.getByRole("button", { name: "Explain Controller Apply Gap" }))

  expect(
    await screen.findAllByText(
      "Number of controller Raft entries waiting to be applied. Sustained growth means the control plane is falling behind.",
    ),
  ).not.toHaveLength(0)
})

test("keeps known unavailable cards visible during partial responses", async () => {
  vi.mocked(getRealtimeMonitor).mockResolvedValueOnce(partialClusterMonitorResponse())
  renderClusterMonitorPage()

  const cards = await screen.findAllByTestId("cluster-monitor-metric-card")
  expect(cards).toHaveLength(2)
  expect(screen.queryByText("Cluster monitor data is partially available")).not.toBeInTheDocument()
  expect(screen.queryByText("query timed out for apply gap")).not.toBeInTheDocument()
  expect(within(cards[1]).getByText("Controller Apply Gap")).toBeInTheDocument()
  expect(within(cards[1]).getByText("Metric unavailable")).toBeInTheDocument()
  expect(within(cards[1]).getByText("No series data")).toBeInTheDocument()
  expect(within(cards[1]).queryByTestId("cluster-monitor-chart")).not.toBeInTheDocument()
  expect(within(cards[1]).getByText("prometheus series unavailable")).toBeInTheDocument()
  expect(screen.queryByText("unknownMetric")).not.toBeInTheDocument()
})

test("formats large internal traffic byte rates", async () => {
  vi.mocked(getRealtimeMonitor).mockResolvedValueOnce(largeTrafficClusterMonitorResponse())
  renderClusterMonitorPage()

  const card = await screen.findByTestId("cluster-monitor-metric-card")
  expect(within(card).getByText("Internal Traffic")).toBeInTheDocument()
  expect(within(card).getByText("388.7")).toBeInTheDocument()
  expect(within(card).getAllByText("KB/s").length).toBeGreaterThan(0)
  expect(within(card).getByText("286.5 KB/s")).toBeInTheDocument()
  expect(within(card).getByText("388.7 KB/s")).toBeInTheDocument()
  expect(within(card).queryByText("398,006.3")).not.toBeInTheDocument()
})

test("renders internal operator cards from realtime API data", async () => {
  vi.mocked(getRealtimeMonitor).mockResolvedValueOnce(internalOperatorMonitorResponse())
  renderClusterMonitorPage()

  const cards = await screen.findAllByTestId("cluster-monitor-metric-card")
  expect(cards).toHaveLength(10)
  expect(within(cards[0]).getByText("Internal TX Traffic")).toBeInTheDocument()
  expect(within(cards[1]).getByText("Internal RX Traffic")).toBeInTheDocument()
  expect(within(cards[2]).getByText("RPC Rate")).toBeInTheDocument()
  expect(within(cards[3]).getByText("RPC Error Rate")).toBeInTheDocument()
  expect(within(cards[4]).getByText("RPC Inflight")).toBeInTheDocument()
  expect(within(cards[5]).getByText("RPC Latency P99")).toBeInTheDocument()
  expect(within(cards[6]).getByText("Dial Success Rate")).toBeInTheDocument()
  expect(within(cards[7]).getByText("Dial Latency P95")).toBeInTheDocument()
  expect(within(cards[8]).getByText("Transport Queue Usage")).toBeInTheDocument()
  expect(within(cards[9]).getByText("Transport Admission Errors")).toBeInTheDocument()
})

test("renders message operator cards from realtime API data", async () => {
  vi.mocked(getRealtimeMonitor).mockResolvedValueOnce(messageOperatorMonitorResponse())
  renderClusterMonitorPage()

  const cards = await screen.findAllByTestId("cluster-monitor-metric-card")
  expect(cards).toHaveLength(11)
  expect(within(cards[0]).getByText("Message Send Rate")).toBeInTheDocument()
  expect(within(cards[1]).getByText("Message Sendack Error Rate")).toBeInTheDocument()
  expect(within(cards[2]).getByText("Append Error Rate")).toBeInTheDocument()
  expect(within(cards[3]).getByText("Append Latency P95")).toBeInTheDocument()
  expect(within(cards[4]).getByText("Dispatch Enqueue Rate")).toBeInTheDocument()
  expect(within(cards[5]).getByText("Dispatch Overflow Rate")).toBeInTheDocument()
  expect(within(cards[6]).getByText("Delivery Enqueue Rate")).toBeInTheDocument()
  expect(within(cards[7]).getByText("Delivery Queue Usage")).toBeInTheDocument()
  expect(within(cards[8]).getByText("Delivery Retry Rate")).toBeInTheDocument()
  expect(within(cards[9]).getByText("Delivery Admission Errors")).toBeInTheDocument()
  expect(within(cards[10]).getByText("Delivery Route Expired")).toBeInTheDocument()
})

test("renders channel operator cards from realtime API data", async () => {
  vi.mocked(getRealtimeMonitor).mockResolvedValueOnce(channelOperatorMonitorResponse())
  renderClusterMonitorPage()

  const cards = await screen.findAllByTestId("cluster-monitor-metric-card")
  expect(cards).toHaveLength(12)
  expect(within(cards[0]).getByText("Channel Append Latency P99")).toBeInTheDocument()
  expect(within(cards[1]).getByText("Active Channels")).toBeInTheDocument()
  expect(within(cards[2]).getByText("Append Batch Records P95")).toBeInTheDocument()
  expect(within(cards[3]).getByText("Append Batch Bytes P95")).toBeInTheDocument()
  expect(within(cards[4]).getByText("Channel Append Error Rate")).toBeInTheDocument()
  expect(within(cards[5]).getByText("Writer Admission Usage")).toBeInTheDocument()
  expect(within(cards[6]).getByText("Parked Followers")).toBeInTheDocument()
  expect(within(cards[7]).getByText("Channel Activation Rejects")).toBeInTheDocument()
  expect(within(cards[8]).getByText("Reactor Mailbox Depth")).toBeInTheDocument()
  expect(within(cards[9]).getByText("Channel Worker Queue Depth")).toBeInTheDocument()
  expect(within(cards[10]).getByText("Pull Hint Error Rate")).toBeInTheDocument()
  expect(within(cards[11]).getByText("Replication Latency P99")).toBeInTheDocument()
})

test("renders database operator cards from realtime API data", async () => {
  vi.mocked(getRealtimeMonitor).mockResolvedValueOnce(databaseOperatorMonitorResponse())
  renderClusterMonitorPage()

  const cards = await screen.findAllByTestId("cluster-monitor-metric-card")
  expect(cards).toHaveLength(9)
  expect(within(cards[0]).getByText("Storage Write P99")).toBeInTheDocument()
  expect(within(cards[1]).getByText("Storage Commit Error Rate")).toBeInTheDocument()
  expect(within(cards[2]).getByText("Storage Commit Queue Usage")).toBeInTheDocument()
  expect(within(cards[3]).getByText("Physical Commit P99")).toBeInTheDocument()
  expect(within(cards[4]).getByText("Commit Batch Records P95")).toBeInTheDocument()
  expect(within(cards[5]).getByText("Commit Batch Bytes P95")).toBeInTheDocument()
  expect(within(cards[6]).getByText("Pebble Disk Usage")).toBeInTheDocument()
  expect(within(cards[7]).getByText("Pebble Read Amplification")).toBeInTheDocument()
  expect(within(cards[8]).getByText("Pebble Compaction Debt")).toBeInTheDocument()
})

test("renders slot operator cards from realtime API data", async () => {
  vi.mocked(getRealtimeMonitor).mockResolvedValueOnce(slotOperatorMonitorResponse())
  renderClusterMonitorPage()

  const cards = await screen.findAllByTestId("cluster-monitor-metric-card")
  expect(cards).toHaveLength(10)
  expect(within(cards[0]).getByText("Slot Leader Stability")).toBeInTheDocument()
  expect(within(cards[1]).getByText("Slot Propose Rate")).toBeInTheDocument()
  expect(within(cards[2]).getByText("Slot Apply Gap")).toBeInTheDocument()
  expect(within(cards[3]).getByText("Slot Apply Latency P99")).toBeInTheDocument()
  expect(within(cards[4]).getByText("Slot Proposal Reject Rate")).toBeInTheDocument()
  expect(within(cards[5]).getByText("Slot Leader Changes")).toBeInTheDocument()
  expect(within(cards[6]).getByText("Slot Replica Lag Max")).toBeInTheDocument()
  expect(within(cards[7]).getByText("Slot Scheduler Queue Usage")).toBeInTheDocument()
  expect(within(cards[8]).getByText("Slot Scheduler Inflight Usage")).toBeInTheDocument()
  expect(within(cards[9]).getByText("Slot Scheduler Task Latency P99")).toBeInTheDocument()
})

test("keeps internal traffic unit readable when an old burst is larger than the current value", async () => {
  vi.mocked(getRealtimeMonitor).mockResolvedValueOnce(burstyTrafficClusterMonitorResponse())
  renderClusterMonitorPage()

  const card = await screen.findByTestId("cluster-monitor-metric-card")
  expect(within(card).getByText("Internal Traffic")).toBeInTheDocument()
  expect(within(card).getByText("4.2")).toBeInTheDocument()
  expect(within(card).getAllByText("MB/s").length).toBeGreaterThan(0)
  expect(within(card).getByText("3.9 MB/s")).toBeInTheDocument()
  expect(within(card).getByText("4.2 MB/s")).toBeInTheDocument()
  expect(within(card).queryByText("0.0")).not.toBeInTheDocument()
  expect(within(card).queryByText("GB/s")).not.toBeInTheDocument()
})

test("formats node memory pressure bytes", async () => {
  vi.mocked(getRealtimeMonitor).mockResolvedValueOnce(nodeMemoryClusterMonitorResponse())
  renderClusterMonitorPage()

  const card = await screen.findByTestId("cluster-monitor-metric-card")
  expect(within(card).getByText("Node Memory RSS")).toBeInTheDocument()
  expect(within(card).getByText("512")).toBeInTheDocument()
  expect(within(card).getAllByText("MB").length).toBeGreaterThan(0)
  expect(within(card).getByText("448 MB")).toBeInTheDocument()
  expect(within(card).getByText("512 MB")).toBeInTheDocument()
  expect(within(card).queryByText("536,870,912")).not.toBeInTheDocument()
})

test("renders all node resource pressure stats in global scope", async () => {
  vi.mocked(getRealtimeMonitor).mockResolvedValueOnce(allNodeCpuClusterMonitorResponse())
  renderClusterMonitorPage()

  const card = await screen.findByTestId("cluster-monitor-metric-card")
  expect(within(card).getByText("Node CPU")).toBeInTheDocument()
  expect(within(card).getByText("node-1")).toBeInTheDocument()
  expect(within(card).getByText("15%")).toBeInTheDocument()
  expect(within(card).getByText("node-2")).toBeInTheDocument()
  expect(within(card).getByText("40%")).toBeInTheDocument()
})

test("renders node GC pressure cards from realtime API data", async () => {
  vi.mocked(getRealtimeMonitor).mockResolvedValueOnce(nodeGCClusterMonitorResponse())
  renderClusterMonitorPage()

  const cards = await screen.findAllByTestId("cluster-monitor-metric-card")
  expect(cards).toHaveLength(4)
  expect(within(cards[0]).getByText("GC Pause Rate")).toBeInTheDocument()
  expect(within(cards[0]).getByText("ms/s")).toBeInTheDocument()
  expect(within(cards[0]).getByText("node-2")).toBeInTheDocument()
  expect(within(cards[1]).getByText("GC Rate")).toBeInTheDocument()
  expect(within(cards[2]).getByText("GC CPU Fraction")).toBeInTheDocument()
  expect(within(cards[3]).getByText("GC Heap Goal Usage")).toBeInTheDocument()
})

test("keeps rendering when cluster cards omit stats", async () => {
  vi.mocked(getRealtimeMonitor).mockResolvedValueOnce(largeTrafficClusterMonitorResponseWithoutStats())
  renderClusterMonitorPage()

  const card = await screen.findByTestId("cluster-monitor-metric-card")
  expect(within(card).getByText("Internal Traffic")).toBeInTheDocument()
  expect(within(card).getByText("388.7")).toBeInTheDocument()
  expect(within(card).getByText("KB/s")).toBeInTheDocument()
  expect(within(card).queryByText("Avg")).not.toBeInTheDocument()
})

test("shows prometheus setup guidance when realtime monitor is disabled", async () => {
  vi.mocked(getRealtimeMonitor).mockResolvedValueOnce(disabledClusterMonitorResponse())
  renderClusterMonitorPage()

  const disabledTitle = await screen.findByText("Prometheus monitoring is not enabled")
  expect(disabledTitle).toBeInTheDocument()
  expect(disabledTitle.closest("section")).toHaveAttribute("data-cluster-monitor-surface", "source-state")
  expect(screen.getByText("WK_METRICS_ENABLE=true")).toBeInTheDocument()
  expect(screen.getByText("WK_PROMETHEUS_ENABLE=true")).toBeInTheDocument()
  expect(screen.queryByTestId("cluster-monitor-metric-card")).not.toBeInTheDocument()
})

test("shows unavailable guidance for source errors and rejected requests", async () => {
  vi.mocked(getRealtimeMonitor).mockResolvedValueOnce(unavailableClusterMonitorResponse())
  const { unmount } = renderClusterMonitorPage()

  const unavailableTitle = await screen.findByText("Prometheus is unavailable")
  expect(unavailableTitle).toBeInTheDocument()
  expect(unavailableTitle.closest("section")).toHaveAttribute("data-cluster-monitor-surface", "source-state")
  expect(screen.getByText("dial tcp 127.0.0.1:9090: connect: connection refused")).toBeInTheDocument()
  expect(screen.queryByTestId("cluster-monitor-metric-card")).not.toBeInTheDocument()

  unmount()
  vi.mocked(getRealtimeMonitor).mockRejectedValueOnce(new Error("manager api unavailable"))
  renderClusterMonitorPage()

  const rejectedTitle = await screen.findByText("Prometheus is unavailable")
  expect(rejectedTitle).toBeInTheDocument()
  expect(rejectedTitle.closest("section")).toHaveAttribute("data-cluster-monitor-surface", "source-state")
  expect(screen.getByText("manager api unavailable")).toBeInTheDocument()
})

test("updates selected time range and auto refresh interval from the toolbar", async () => {
  const user = userEvent.setup()
  vi.mocked(getRealtimeMonitor).mockResolvedValue(readyClusterMonitorResponse())
  renderClusterMonitorPage()

  await user.click(await screen.findByRole("button", { name: "30m time range" }))
  expect(screen.getByRole("button", { name: "30m time range" })).toHaveAttribute("aria-pressed", "true")
  expect(getRealtimeMonitor).toHaveBeenLastCalledWith({ window: "30m", category: "common" })

  await user.selectOptions(screen.getByRole("combobox", { name: "Auto refresh" }), "off")
  expect(screen.getByRole("combobox", { name: "Auto refresh" })).toHaveValue("off")
})

test("filters realtime monitor by selected category", async () => {
  const user = userEvent.setup()
  vi.mocked(getRealtimeMonitor).mockResolvedValue(readyClusterMonitorResponse())
  renderClusterMonitorPage()

  const categorySelect = await screen.findByRole("combobox", { name: "Category" })
  expect(categorySelect).toHaveValue("common")
  expect(within(categorySelect).getByRole("option", { name: "Common" })).toBeInTheDocument()
  expect(within(categorySelect).getByRole("option", { name: "Database" })).toBeInTheDocument()
  expect(within(categorySelect).queryByRole("option", { name: "All" })).not.toBeInTheDocument()
  expect(getRealtimeMonitor).toHaveBeenCalledWith({ window: "15m", category: "common" })

  await user.selectOptions(categorySelect, "database")

  expect(categorySelect).toHaveValue("database")
  expect(getRealtimeMonitor).toHaveBeenLastCalledWith({ window: "15m", category: "database" })
})

test("manually and automatically refreshes realtime monitor data", async () => {
  vi.useFakeTimers()
  vi.mocked(getRealtimeMonitor).mockResolvedValue(readyClusterMonitorResponse())
  renderClusterMonitorPage()

  await act(async () => {
    await Promise.resolve()
    await Promise.resolve()
  })
  expect(screen.getAllByTestId("cluster-monitor-metric-card")).toHaveLength(2)
  expect(getRealtimeMonitor).toHaveBeenCalledTimes(1)

  fireEvent.click(screen.getByRole("button", { name: "Refresh now" }))
  await act(async () => {})
  expect(getRealtimeMonitor).toHaveBeenCalledTimes(2)
  expect(getRealtimeMonitor).toHaveBeenLastCalledWith({ window: "15m", category: "common" })

  await act(async () => {
    await vi.advanceTimersByTimeAsync(30_000)
  })
  expect(getRealtimeMonitor).toHaveBeenCalledTimes(3)
  expect(getRealtimeMonitor).toHaveBeenLastCalledWith({ window: "15m", category: "common" })

  fireEvent.change(screen.getByRole("combobox", { name: "Auto refresh" }), { target: { value: "off" } })
  await act(async () => {})
  await act(async () => {
    await vi.advanceTimersByTimeAsync(30_000)
  })
  expect(getRealtimeMonitor).toHaveBeenCalledTimes(3)
})

test("filters realtime monitor by selected node", async () => {
  const user = userEvent.setup()
  vi.mocked(getRealtimeMonitor).mockResolvedValue(readyClusterMonitorResponse())
  renderClusterMonitorPage()

  const nodeSelect = await screen.findByRole("combobox", { name: "Node" })
  expect(getRealtimeMonitor).toHaveBeenCalledWith({ window: "15m", category: "common" })

  await user.selectOptions(screen.getByRole("combobox", { name: "Category" }), "control")
  await user.selectOptions(nodeSelect, "2")

  expect(getRealtimeMonitor).toHaveBeenLastCalledWith({ window: "15m", category: "control", nodeId: 2 })
})

test("does not silently render preview fixture before the realtime API responds", () => {
  vi.mocked(getRealtimeMonitor).mockReturnValue(new Promise(() => undefined))
  renderClusterMonitorPage()

  const loadingText = screen.getByText("Loading cluster monitor data...")
  expect(loadingText).toBeInTheDocument()
  expect(loadingText.closest("section")).toHaveAttribute("data-cluster-monitor-surface", "loading")
  expect(screen.queryByText("UI Preview")).not.toBeInTheDocument()
  expect(screen.queryByTestId("cluster-monitor-metric-card")).not.toBeInTheDocument()
  expect(screen.queryByText("Incident Rate")).not.toBeInTheDocument()
})
