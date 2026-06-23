import { expect, test } from "vitest"

import { buildPreviewClusterMonitorModel } from "./preview-data"

test("builds the cluster runtime monitor cards in troubleshooting order", () => {
  const model = buildPreviewClusterMonitorModel("15m", false)

  expect(model.cards.map((card) => card.key)).toEqual([
    "controllerApplyGap",
    "controllerRaftStepQueueUsage",
    "controllerRaftStepEnqueueLatencyP99",
    "controllerRaftStepEnqueueErrorRate",
    "controllerStateRevision",
    "controllerActiveTasks",
    "controllerFailedTasks",
    "controllerNodesUnhealthy",
    "controllerSlotLeaderSkew",
    "controllerLeaderPresent",
    "slotLeaderStability",
    "slotProposeRate",
    "slotApplyGap",
    "slotLatencyP99",
    "channelAppendLatencyP99",
    "activeChannels",
    "internalTraffic",
    "rpcSuccessRate",
    "rpcLatencyP95",
    "workqueuePressure",
    "nodeCpuPercent",
    "nodeMemoryRSS",
    "nodeGoroutines",
    "storageWriteP99",
    "storagePebbleDiskUsage",
    "storagePebbleReadAmplification",
    "storagePebbleCompactionDebt",
  ])
  expect(model.scopeLabelId).toBe("clusterMonitor.scope.global")
  expect(model.snapshot.map((entry) => entry.labelId)).toEqual([
    "clusterMonitor.snapshot.nodesAlive",
    "clusterMonitor.snapshot.slotsReady",
    "clusterMonitor.snapshot.controllerApplyGap",
    "clusterMonitor.snapshot.rpcErrorRate",
    "clusterMonitor.snapshot.queuePressure",
    "clusterMonitor.snapshot.storageWriteP99",
  ])
})

test("builds deterministic preview data with chart series and stats", () => {
  const first = buildPreviewClusterMonitorModel("30m", false)
  const second = buildPreviewClusterMonitorModel("30m", false)

  expect(second).toEqual(first)
  expect(first.cards).toHaveLength(27)
  expect(first.cards[0].series.length).toBeGreaterThan(20)
  expect(first.cards[0].stats).toHaveLength(3)
})

test("uses fixed preview details for cluster-specific operational stats", () => {
  const model = buildPreviewClusterMonitorModel("15m", false)
  const cardsByKey = new Map(model.cards.map((card) => [card.key, card]))

  expect(cardsByKey.get("controllerApplyGap")?.stats).toContainEqual({
    labelId: "clusterMonitor.stat.slowNodes",
    value: "1",
  })
  expect(cardsByKey.get("activeChannels")?.stats).toContainEqual({
    labelId: "clusterMonitor.stat.peak",
    value: expect.any(String),
  })
  expect(cardsByKey.get("slotApplyGap")?.stats).toContainEqual({
    labelId: "clusterMonitor.stat.maxGap",
    value: expect.any(String),
  })
  expect(cardsByKey.get("rpcSuccessRate")?.stats).toContainEqual({
    labelId: "clusterMonitor.stat.timeouts",
    value: "9",
  })
})
