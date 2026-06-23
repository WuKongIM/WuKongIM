import { describe, expect, it } from "vitest"

import type { DashboardMetricsResponse } from "@/lib/manager-api"
import {
  buildBusinessEntryCards,
  buildBusinessMetricStrip,
  buildBusinessRisks,
  buildBusinessTrendSeries,
  computeBusinessVerdict,
} from "./view-model"

const series = (latest: number, peak = latest, avg = latest) => ({ latest, peak, avg, series: [avg, latest] })

const metricsFixture: DashboardMetricsResponse = {
  generated_at: "2026-05-15T08:30:00Z",
  window_seconds: 300,
  step_seconds: 30,
  points: 10,
  metrics: {
    send_per_sec: series(2800),
    deliver_per_sec: series(2700),
    connections: series(18400),
    send_latency_p99_ms: series(31),
    delivery_latency_p99_ms: series(42),
    send_fail_rate_percent: series(0.02),
    delivery_fail_rate_percent: series(0.01),
    active_channels: series(2143),
    retry_queue_depth: series(8),
    fan_out_rate: series(3.4),
  },
}

function withMetric<K extends keyof DashboardMetricsResponse["metrics"]>(
  metrics: DashboardMetricsResponse,
  key: K,
  latest: number,
): DashboardMetricsResponse {
  return {
    ...metrics,
    metrics: {
      ...metrics.metrics,
      [key]: { ...metrics.metrics[key], latest },
    },
  }
}

describe("computeBusinessVerdict", () => {
  it("returns normal for healthy message quality", () => {
    expect(computeBusinessVerdict(metricsFixture)).toBe("normal")
  })

  it("returns degraded for retry pressure or elevated failure rate", () => {
    expect(computeBusinessVerdict(withMetric(metricsFixture, "retry_queue_depth", 200))).toBe("degraded")
    expect(computeBusinessVerdict(withMetric(metricsFixture, "send_fail_rate_percent", 2))).toBe("degraded")
  })

  it("returns critical for severe failure rate or latency", () => {
    expect(computeBusinessVerdict(withMetric(metricsFixture, "delivery_fail_rate_percent", 10))).toBe("critical")
    expect(computeBusinessVerdict(withMetric(metricsFixture, "delivery_latency_p99_ms", 5000))).toBe("critical")
  })
})

describe("buildBusinessMetricStrip", () => {
  it("returns all business quality metrics", () => {
    expect(buildBusinessMetricStrip(metricsFixture).map((item) => item.key)).toEqual([
      "sendRate",
      "deliverRate",
      "sendLatency",
      "deliveryLatency",
      "sendFailRate",
      "deliveryFailRate",
      "connections",
      "activeChannels",
      "retryQueue",
      "fanOut",
    ])
  })
})

describe("buildBusinessTrendSeries", () => {
  it("returns throughput, latency, and failure trend groups", () => {
    const trends = buildBusinessTrendSeries(metricsFixture)
    expect(trends.throughput[0]).toMatchObject({ index: 0, send: 2800, deliver: 2700 })
    expect(trends.latency[1]).toMatchObject({ send: 31, delivery: 42 })
    expect(trends.failures[1]).toMatchObject({ send: 0.02, delivery: 0.01 })
  })
})

describe("buildBusinessRisks", () => {
  it("reports delivery gap when deliver rate falls below send rate", () => {
    const risks = buildBusinessRisks(withMetric(metricsFixture, "deliver_per_sec", 1000))
    expect(risks.map((risk) => risk.key)).toContain("deliveryGap")
  })
})

describe("buildBusinessEntryCards", () => {
  it("uses deterministic sample labels when optional summaries are absent", () => {
    const cards = buildBusinessEntryCards()
    expect(cards.map((card) => card.source)).toEqual(["sample", "sample", "real", "sample"])
    expect(cards.map((card) => card.key)).toEqual(["users", "channels", "messages", "systemUsers"])
  })
})
