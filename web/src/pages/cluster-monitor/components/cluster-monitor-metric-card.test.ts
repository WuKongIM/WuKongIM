import { expect, test } from "vitest"

import { buildClusterChartModel, formatClusterChartValue } from "./cluster-monitor-metric-card"

test("formats cluster monitor chart tooltip values without leaking raw floats", () => {
  expect(formatClusterChartValue(4.05884201439876, "MB/s")).toBe("4.1 MB/s")
  expect(formatClusterChartValue(0.00405884201439876, "GB/s")).toBe("0.004 GB/s")
  expect(formatClusterChartValue(99.86123, "%")).toBe("99.86%")
  expect(formatClusterChartValue(124, "")).toBe("124")
})

test("builds separate chart curves for node-labeled cluster points", () => {
  const model = buildClusterChartModel(
    [
      { timestamp: 1781767200000, value: 12.5, label: "node-1", seriesKey: "node-1" },
      { timestamp: 1781767200000, value: 32.5, label: "node-2", seriesKey: "node-2" },
      { timestamp: 1781767220000, value: 15, label: "node-1", seriesKey: "node-1" },
      { timestamp: 1781767220000, value: 40, label: "node-2", seriesKey: "node-2" },
    ],
    "en",
  )

  expect(model.series.map((item) => ({ key: item.key, label: item.label }))).toEqual([
    { key: "node-1", label: "node-1" },
    { key: "node-2", label: "node-2" },
  ])
  expect(model.data.map((row) => ({ node1: row["node-1"], node2: row["node-2"] }))).toEqual([
    { node1: 12.5, node2: 32.5 },
    { node1: 15, node2: 40 },
  ])
})

test("ignores unlabeled fallback values when node-labeled cluster points exist", () => {
  const model = buildClusterChartModel(
    [
      { timestamp: 1781767200000, value: 0 },
      { timestamp: 1781767200000, value: 122.45, label: "node-1", seriesKey: "node-1" },
      { timestamp: 1781767200000, value: 135.49, label: "node-2", seriesKey: "node-2" },
      { timestamp: 1781767220000, value: 0 },
      { timestamp: 1781767220000, value: 123.31, label: "node-3", seriesKey: "node-3" },
    ],
    "en",
  )

  expect(model.series.map((item) => item.key)).toEqual(["node-1", "node-2", "node-3"])
  expect(model.data.some((row) => "value" in row)).toBe(false)
})
