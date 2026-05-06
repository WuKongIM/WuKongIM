import { describe, expect, it } from "vitest"

import { networkSummaryFixture } from "@/pages/network/test-fixtures"
import { aggregateNetworkMetrics } from "./aggregation"

describe("aggregateNetworkMetrics", () => {
  it("uses headline and history metrics for all nodes", () => {
    const metrics = aggregateNetworkMetrics(networkSummaryFixture, [])

    expect(metrics.isAllNodes).toBe(true)
    expect(metrics.health).toMatchObject({ alive: 2, suspect: 0, dead: 0, draining: 1, total: 3 })
    expect(metrics.pools.cluster).toEqual({ active: 3, idle: 4 })
    expect(metrics.pools.dataPlane).toEqual({ active: 2, idle: 1 })
    expect(metrics.errors.total).toBe(6)
    expect(metrics.rpc.calls).toBe(40)
    expect(metrics.rpc.successRate).toBe(0.975)
    expect(metrics.latency.p95Ms).toBe(7)
    expect(metrics.traffic.txBytes).toBe(4096)
    expect(metrics.events).toHaveLength(1)
  })

  it("aggregates selected peer nodes without inventing per-peer traffic", () => {
    const metrics = aggregateNetworkMetrics(networkSummaryFixture, [2])

    expect(metrics.isAllNodes).toBe(false)
    expect(metrics.selectedNodeIds).toEqual([2])
    expect(metrics.health).toMatchObject({ alive: 1, suspect: 0, dead: 0, draining: 0, total: 1 })
    expect(metrics.pools.cluster).toEqual({ active: 1, idle: 2 })
    expect(metrics.pools.dataPlane).toEqual({ active: 2, idle: 1 })
    expect(metrics.errors).toMatchObject({ dial: 1, queueFull: 2, timeouts: 3, remote: 4, total: 10 })
    expect(metrics.rpc.calls).toBe(40)
    expect(metrics.rpc.successRate).toBe(0.975)
    expect(metrics.traffic.isFilteredByNode).toBe(false)
    expect(metrics.traffic.scopeNote).toMatch(/local total/i)
    expect(metrics.events.map((event) => event.target_node)).toEqual([2])
  })
})
