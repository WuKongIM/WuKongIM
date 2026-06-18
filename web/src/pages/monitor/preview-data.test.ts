import { expect, test } from "vitest"

import { buildPreviewMonitorModel } from "./preview-data"

test("builds the business path monitor cards in the required order", () => {
  const model = buildPreviewMonitorModel("15m", false)

  expect(model.cards.map((card) => card.key)).toEqual([
    "sendRate",
    "sendSuccessRate",
    "entryLatencyP99",
    "commitRate",
    "commitLatencyP99",
    "pendingCommitBacklog",
    "deliveryRate",
    "deliveryLatencyP99",
    "fanOutRatio",
    "offlineEnqueueRate",
    "retryQueueDepth",
    "pathErrorRate",
  ])
  expect(model.scopeLabelId).toBe("monitor.scope.global")
  expect(model.snapshot.map((entry) => entry.labelId)).toEqual([
    "monitor.snapshot.send",
    "monitor.snapshot.delivery",
    "monitor.snapshot.entryP99",
    "monitor.snapshot.deliveryP99",
    "monitor.snapshot.errors",
    "monitor.snapshot.retryDepth",
    "monitor.snapshot.online",
  ])
})

test("builds deterministic 30m preview data with rich series and stats", () => {
  const first = buildPreviewMonitorModel("30m", false)
  const second = buildPreviewMonitorModel("30m", false)

  expect(second).toEqual(first)
  expect(first.cards[0].series.length).toBeGreaterThan(20)
  expect(first.cards[0].stats).toHaveLength(3)
})
