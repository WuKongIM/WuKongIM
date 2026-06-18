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

test("uses explicit special stat values for non-generic monitor details", () => {
  const model = buildPreviewMonitorModel("15m", false)
  const cardsByKey = new Map(model.cards.map((card) => [card.key, card]))

  expect(cardsByKey.get("sendSuccessRate")?.stats).toContainEqual({
    labelId: "monitor.stat.topError",
    value: "auth_expired",
  })
  expect(cardsByKey.get("pendingCommitBacklog")?.stats).toContainEqual({
    labelId: "monitor.stat.oldestWait",
    value: "4.8s",
  })
  expect(cardsByKey.get("retryQueueDepth")?.stats).toContainEqual({
    labelId: "monitor.stat.retrySuccess",
    value: "97.8%",
  })
  expect(cardsByKey.get("pathErrorRate")?.stats).toContainEqual({
    labelId: "monitor.stat.topReason",
    value: "timeout",
  })
})

test("uses fixed preview totals independent of selected chart range", () => {
  const fifteenMinute = buildPreviewMonitorModel("15m", false)
  const oneHour = buildPreviewMonitorModel("1h", false)
  const sendStats15m = fifteenMinute.cards.find((card) => card.key === "sendRate")?.stats
  const sendStats1h = oneHour.cards.find((card) => card.key === "sendRate")?.stats

  expect(sendStats15m).toContainEqual({
    labelId: "monitor.stat.total5m",
    value: "372k",
  })
  expect(sendStats1h).toContainEqual({
    labelId: "monitor.stat.total5m",
    value: "372k",
  })
})
