import { expect, test } from "vitest"

import { buildPreviewClusterMonitorModel } from "./preview-data"

test("builds the cluster runtime monitor cards in troubleshooting order", () => {
  const model = buildPreviewClusterMonitorModel("15m", false)

  expect(model.cards.map((card) => card.key)).toEqual([
    "controllerProposeRate",
    "controllerApplyGap",
    "slotLeaderStability",
    "slotReplicaLagP99",
    "channelISRHealth",
    "channelAppendLatencyP99",
    "internalTraffic",
    "rpcSuccessRate",
    "rpcLatencyP95",
    "workqueuePressure",
    "storageWriteP99",
    "incidentRate",
  ])
  expect(model.scopeLabelId).toBe("clusterMonitor.scope.global")
  expect(model.snapshot.map((entry) => entry.labelId)).toEqual([
    "clusterMonitor.snapshot.nodesAlive",
    "clusterMonitor.snapshot.slotsReady",
    "clusterMonitor.snapshot.controllerApplyGap",
    "clusterMonitor.snapshot.channelISRAnomalies",
    "clusterMonitor.snapshot.rpcErrorRate",
    "clusterMonitor.snapshot.queuePressure",
    "clusterMonitor.snapshot.storageWriteP99",
  ])
})

test("builds deterministic preview data with chart series and stats", () => {
  const first = buildPreviewClusterMonitorModel("30m", false)
  const second = buildPreviewClusterMonitorModel("30m", false)

  expect(second).toEqual(first)
  expect(first.cards).toHaveLength(12)
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
  expect(cardsByKey.get("channelISRHealth")?.stats).toContainEqual({
    labelId: "clusterMonitor.stat.affectedChannels",
    value: "14",
  })
  expect(cardsByKey.get("rpcSuccessRate")?.stats).toContainEqual({
    labelId: "clusterMonitor.stat.timeouts",
    value: "9",
  })
  expect(cardsByKey.get("incidentRate")?.stats).toContainEqual({
    labelId: "clusterMonitor.stat.topReason",
    value: "rpc_timeout",
  })
})
