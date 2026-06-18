import { expect, test } from "vitest"

import { formatMonitorChartValue } from "./monitor-metric-card"

test("formats business monitor chart tooltip values without leaking raw floats", () => {
  expect(formatMonitorChartValue(4.05884201439876, "req/s")).toBe("4.1 req/s")
  expect(formatMonitorChartValue(0.00405884201439876, "%")).toBe("0%")
  expect(formatMonitorChartValue(99.86123, "%")).toBe("99.86%")
  expect(formatMonitorChartValue(124, "")).toBe("124")
})
