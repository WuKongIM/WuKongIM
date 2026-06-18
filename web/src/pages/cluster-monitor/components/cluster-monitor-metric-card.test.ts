import { expect, test } from "vitest"

import { formatClusterChartValue } from "./cluster-monitor-metric-card"

test("formats cluster monitor chart tooltip values without leaking raw floats", () => {
  expect(formatClusterChartValue(4.05884201439876, "MB/s")).toBe("4.1 MB/s")
  expect(formatClusterChartValue(0.00405884201439876, "GB/s")).toBe("0.004 GB/s")
  expect(formatClusterChartValue(99.86123, "%")).toBe("99.86%")
  expect(formatClusterChartValue(124, "")).toBe("124")
})
