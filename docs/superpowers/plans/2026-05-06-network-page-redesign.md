# Network Observability Page Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Redesign the network observability page from a 12-section dashboard into a focused 8-card cloud monitoring pattern with node filtering.

**Architecture:** Component-based architecture with shared hooks for data fetching and filtering. Each metric card is a self-contained component. Filter bar manages URL state and triggers data re-aggregation.

**Tech Stack:** React 19, TypeScript, Recharts, Tailwind CSS, Vitest, React Testing Library

---

## File Structure

### New Files to Create

**Hooks:**
- `web/src/pages/network/hooks/use-node-filter.ts` - Filter state management (node selection, time range)
- `web/src/pages/network/hooks/use-network-data.ts` - Data fetching and aggregation

**Utils:**
- `web/src/pages/network/utils/aggregation.ts` - Data aggregation functions
- `web/src/pages/network/utils/formatters.ts` - Number and time formatting utilities

**Components:**
- `web/src/pages/network/components/filter-bar.tsx` - Top filter controls
- `web/src/pages/network/components/metric-card.tsx` - Reusable card wrapper
- `web/src/pages/network/components/node-health-card.tsx` - Card 1: Node health
- `web/src/pages/network/components/connection-pool-card.tsx` - Card 2: Connection pools
- `web/src/pages/network/components/rpc-latency-card.tsx` - Card 3: RPC latency
- `web/src/pages/network/components/rpc-success-card.tsx` - Card 4: RPC success rate
- `web/src/pages/network/components/traffic-card.tsx` - Card 5: Network traffic
- `web/src/pages/network/components/errors-card.tsx` - Card 6: Network errors
- `web/src/pages/network/components/message-types-card.tsx` - Card 7: Message types
- `web/src/pages/network/components/events-card.tsx` - Card 8: Recent events

**Tests:**
- `web/src/pages/network/hooks/use-node-filter.test.ts`
- `web/src/pages/network/hooks/use-network-data.test.ts`
- `web/src/pages/network/utils/aggregation.test.ts`
- `web/src/pages/network/components/filter-bar.test.tsx`
- `web/src/pages/network/components/metric-card.test.tsx`

### Files to Modify

- `web/src/pages/network/page.tsx` - Replace with new implementation
- `web/src/pages/network/page.test.tsx` - Update tests for new structure

### Files to Preserve

- `web/src/lib/manager-api.ts` - No changes needed
- `web/src/lib/manager-api.types.ts` - No changes needed

---

## Task 1: Create Filter State Hook

**Files:**
- Create: `web/src/pages/network/hooks/use-node-filter.ts`
- Test: `web/src/pages/network/hooks/use-node-filter.test.ts`

- [ ] **Step 1: Write the failing test**

Create test file with basic filter state tests:

```typescript
import { renderHook, act } from "@testing-library/react"
import { describe, expect, test } from "vitest"
import { useNodeFilter } from "./use-node-filter"

describe("useNodeFilter", () => {
  test("initializes with default state", () => {
    const { result } = renderHook(() => useNodeFilter())
    
    expect(result.current.selectedNodes).toEqual([])
    expect(result.current.timeRange).toBe("1m")
    expect(result.current.autoRefresh).toBe(false)
  })

  test("updates selected nodes", () => {
    const { result } = renderHook(() => useNodeFilter())
    
    act(() => {
      result.current.setSelectedNodes([1, 2, 3])
    })
    
    expect(result.current.selectedNodes).toEqual([1, 2, 3])
  })

  test("checks if node is selected", () => {
    const { result } = renderHook(() => useNodeFilter())
    
    act(() => {
      result.current.setSelectedNodes([1, 3])
    })
    
    expect(result.current.isNodeSelected(1)).toBe(true)
    expect(result.current.isNodeSelected(2)).toBe(false)
    expect(result.current.isNodeSelected(3)).toBe(true)
  })

  test("updates time range", () => {
    const { result } = renderHook(() => useNodeFilter())
    
    act(() => {
      result.current.setTimeRange("5m")
    })
    
    expect(result.current.timeRange).toBe("5m")
  })

  test("toggles auto refresh", () => {
    const { result } = renderHook(() => useNodeFilter())
    
    act(() => {
      result.current.toggleAutoRefresh()
    })
    
    expect(result.current.autoRefresh).toBe(true)
    
    act(() => {
      result.current.toggleAutoRefresh()
    })
    
    expect(result.current.autoRefresh).toBe(false)
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && yarn test src/pages/network/hooks/use-node-filter.test.ts`
Expected: FAIL with "Cannot find module './use-node-filter'"

- [ ] **Step 3: Write minimal implementation**

Create the hook implementation:

```typescript
import { useState, useCallback } from "react"

export type TimeRange = "1m" | "5m" | "15m"

export interface NodeFilterState {
  selectedNodes: number[]
  timeRange: TimeRange
  autoRefresh: boolean
}

export interface UseNodeFilterReturn extends NodeFilterState {
  setSelectedNodes: (nodeIds: number[]) => void
  setTimeRange: (range: TimeRange) => void
  toggleAutoRefresh: () => void
  isNodeSelected: (nodeId: number) => boolean
}

export function useNodeFilter(): UseNodeFilterReturn {
  const [selectedNodes, setSelectedNodes] = useState<number[]>([])
  const [timeRange, setTimeRange] = useState<TimeRange>("1m")
  const [autoRefresh, setAutoRefresh] = useState(false)

  const toggleAutoRefresh = useCallback(() => {
    setAutoRefresh((prev) => !prev)
  }, [])

  const isNodeSelected = useCallback(
    (nodeId: number) => {
      return selectedNodes.includes(nodeId)
    },
    [selectedNodes]
  )

  return {
    selectedNodes,
    timeRange,
    autoRefresh,
    setSelectedNodes,
    setTimeRange,
    toggleAutoRefresh,
    isNodeSelected,
  }
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && yarn test src/pages/network/hooks/use-node-filter.test.ts`
Expected: PASS (all 5 tests)

- [ ] **Step 5: Commit**

```bash
git add web/src/pages/network/hooks/use-node-filter.ts web/src/pages/network/hooks/use-node-filter.test.ts
git commit -m "feat(network): add node filter state hook"
```

---

## Task 2: Create Data Aggregation Utilities

**Files:**
- Create: `web/src/pages/network/utils/aggregation.ts`
- Test: `web/src/pages/network/utils/aggregation.test.ts`

- [ ] **Step 1: Write the failing test**

```typescript
import { describe, expect, test } from "vitest"
import type { ManagerNetworkPeer, ManagerNetworkEvent } from "@/lib/manager-api.types"
import { aggregateNodeMetrics, filterEventsByNodes } from "./aggregation"

describe("aggregation", () => {
  const mockPeers: ManagerNetworkPeer[] = [
    {
      node_id: 1,
      name: "node-1",
      addr: "10.0.0.1:8080",
      health: "alive",
      last_heartbeat_at: "2026-05-06T10:00:00Z",
      pools: {
        cluster: { active: 2, idle: 3 },
        data_plane: { active: 1, idle: 2 },
      },
      rpc: { inflight: 5, calls_1m: 100, p95_ms: 45, success_rate: 0.98 },
      errors: { dial_error_1m: 1, queue_full_1m: 2, timeout_1m: 3, remote_error_1m: 0 },
    },
    {
      node_id: 2,
      name: "node-2",
      addr: "10.0.0.2:8080",
      health: "alive",
      last_heartbeat_at: "2026-05-06T10:00:00Z",
      pools: {
        cluster: { active: 3, idle: 2 },
        data_plane: { active: 2, idle: 1 },
      },
      rpc: { inflight: 3, calls_1m: 80, p95_ms: 50, success_rate: 0.95 },
      errors: { dial_error_1m: 0, queue_full_1m: 1, timeout_1m: 2, remote_error_1m: 1 },
    },
  ]

  test("aggregates metrics for all nodes", () => {
    const result = aggregateNodeMetrics(mockPeers, [])
    
    expect(result.poolActive).toBe(8)
    expect(result.poolIdle).toBe(8)
    expect(result.rpcInflight).toBe(8)
    expect(result.totalCalls).toBe(180)
    expect(result.avgP95).toBe(47.5)
    expect(result.totalErrors).toBe(10)
  })

  test("aggregates metrics for selected nodes", () => {
    const result = aggregateNodeMetrics(mockPeers, [1])
    
    expect(result.poolActive).toBe(3)
    expect(result.poolIdle).toBe(5)
    expect(result.rpcInflight).toBe(5)
    expect(result.totalCalls).toBe(100)
    expect(result.avgP95).toBe(45)
    expect(result.totalErrors).toBe(6)
  })

  test("filters events by selected nodes", () => {
    const events: ManagerNetworkEvent[] = [
      { at: "2026-05-06T10:00:00Z", severity: "info", kind: "dial", target_node: 1, service: "test", message: "msg1" },
      { at: "2026-05-06T10:01:00Z", severity: "warn", kind: "timeout", target_node: 2, service: "test", message: "msg2" },
      { at: "2026-05-06T10:02:00Z", severity: "error", kind: "error", target_node: 1, service: "test", message: "msg3" },
    ]
    
    const filtered = filterEventsByNodes(events, [1])
    
    expect(filtered).toHaveLength(2)
    expect(filtered[0].target_node).toBe(1)
    expect(filtered[1].target_node).toBe(1)
  })

  test("returns all events when no nodes selected", () => {
    const events: ManagerNetworkEvent[] = [
      { at: "2026-05-06T10:00:00Z", severity: "info", kind: "dial", target_node: 1, service: "test", message: "msg1" },
      { at: "2026-05-06T10:01:00Z", severity: "warn", kind: "timeout", target_node: 2, service: "test", message: "msg2" },
    ]
    
    const filtered = filterEventsByNodes(events, [])
    
    expect(filtered).toHaveLength(2)
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && yarn test src/pages/network/utils/aggregation.test.ts`
Expected: FAIL with "Cannot find module './aggregation'"

- [ ] **Step 3: Write minimal implementation**

```typescript
import type { ManagerNetworkPeer, ManagerNetworkEvent } from "@/lib/manager-api.types"

export interface AggregatedMetrics {
  poolActive: number
  poolIdle: number
  rpcInflight: number
  totalCalls: number
  avgP95: number
  totalErrors: number
}

export function aggregateNodeMetrics(peers: ManagerNetworkPeer[], nodeIds: number[]): AggregatedMetrics {
  const selectedPeers = nodeIds.length === 0 ? peers : peers.filter((p) => nodeIds.includes(p.node_id))

  const poolActive = selectedPeers.reduce((sum, p) => sum + p.pools.cluster.active + p.pools.data_plane.active, 0)
  const poolIdle = selectedPeers.reduce((sum, p) => sum + p.pools.cluster.idle + p.pools.data_plane.idle, 0)
  const rpcInflight = selectedPeers.reduce((sum, p) => sum + p.rpc.inflight, 0)
  const totalCalls = selectedPeers.reduce((sum, p) => sum + p.rpc.calls_1m, 0)
  const avgP95 = selectedPeers.length > 0 ? selectedPeers.reduce((sum, p) => sum + p.rpc.p95_ms, 0) / selectedPeers.length : 0
  const totalErrors = selectedPeers.reduce(
    (sum, p) => sum + p.errors.dial_error_1m + p.errors.queue_full_1m + p.errors.timeout_1m + p.errors.remote_error_1m,
    0
  )

  return { poolActive, poolIdle, rpcInflight, totalCalls, avgP95, totalErrors }
}

export function filterEventsByNodes(events: ManagerNetworkEvent[], nodeIds: number[]): ManagerNetworkEvent[] {
  if (nodeIds.length === 0) return events
  return events.filter((e) => nodeIds.includes(e.target_node))
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && yarn test src/pages/network/utils/aggregation.test.ts`
Expected: PASS (all 4 tests)

- [ ] **Step 5: Commit**

```bash
git add web/src/pages/network/utils/aggregation.ts web/src/pages/network/utils/aggregation.test.ts
git commit -m "feat(network): add data aggregation utilities"
```

---

## Task 3: Create Formatter Utilities

**Files:**
- Create: `web/src/pages/network/utils/formatters.ts`
- Test: `web/src/pages/network/utils/formatters.test.ts`

- [ ] **Step 1: Write the failing test**

```typescript
import { describe, expect, test } from "vitest"
import { formatBytes, formatLatency, formatTimestamp, formatCompactNumber } from "./formatters"

describe("formatters", () => {
  test("formats bytes", () => {
    expect(formatBytes(0)).toBe("0 B")
    expect(formatBytes(1024)).toBe("1,024 B")
    expect(formatBytes(1048576)).toBe("1,048,576 B")
  })

  test("formats latency", () => {
    expect(formatLatency(0)).toBe("N/A")
    expect(formatLatency(-1)).toBe("N/A")
    expect(formatLatency(45)).toBe("45 ms")
    expect(formatLatency(1234)).toBe("1,234 ms")
  })

  test("formats timestamps", () => {
    expect(formatTimestamp("2026-05-06T10:30:45Z")).toMatch(/2026/)
    expect(formatTimestamp("0001-01-01T00:00:00Z")).toBe("N/A")
    expect(formatTimestamp("")).toBe("N/A")
  })

  test("formats compact numbers", () => {
    expect(formatCompactNumber(0)).toBe("0")
    expect(formatCompactNumber(1234)).toBe("1,234")
    expect(formatCompactNumber(1234567)).toBe("1,234,567")
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && yarn test src/pages/network/utils/formatters.test.ts`
Expected: FAIL with "Cannot find module './formatters'"

- [ ] **Step 3: Write minimal implementation**

```typescript
export function formatBytes(value: number): string {
  return `${formatCompactNumber(value)} B`
}

export function formatLatency(value: number): string {
  if (value <= 0) return "N/A"
  return `${formatCompactNumber(value)} ms`
}

export function formatTimestamp(value: string): string {
  if (!value || value.startsWith("0001-")) return "N/A"
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return "N/A"
  return date.toLocaleString()
}

export function formatCompactNumber(value: number): string {
  return new Intl.NumberFormat().format(value)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && yarn test src/pages/network/utils/formatters.test.ts`
Expected: PASS (all 4 tests)

- [ ] **Step 5: Commit**

```bash
git add web/src/pages/network/utils/formatters.ts web/src/pages/network/utils/formatters.test.ts
git commit -m "feat(network): add formatter utilities"
```

---

## Task 4: Create MetricCard Base Component

**Files:**
- Create: `web/src/pages/network/components/metric-card.tsx`
- Test: `web/src/pages/network/components/metric-card.test.tsx`

- [ ] **Step 1: Write the failing test**

```typescript
import { render, screen } from "@testing-library/react"
import { describe, expect, test } from "vitest"
import { MetricCard } from "./metric-card"

describe("MetricCard", () => {
  test("renders title and primary metric", () => {
    render(
      <MetricCard
        title="Test Metric"
        primaryMetric="42"
        chart={<div>Chart</div>}
      />
    )
    
    expect(screen.getByText("Test Metric")).toBeInTheDocument()
    expect(screen.getByText("42")).toBeInTheDocument()
    expect(screen.getByText("Chart")).toBeInTheDocument()
  })

  test("renders labels when provided", () => {
    render(
      <MetricCard
        title="Test"
        primaryMetric="100"
        chart={<div>Chart</div>}
        labels={[
          { label: "Label 1", value: "Value 1" },
          { label: "Label 2", value: "Value 2" },
        ]}
      />
    )
    
    expect(screen.getByText("Label 1")).toBeInTheDocument()
    expect(screen.getByText("Value 1")).toBeInTheDocument()
  })

  test("applies warning alert level styling", () => {
    const { container } = render(
      <MetricCard
        title="Test"
        primaryMetric="50"
        chart={<div>Chart</div>}
        alertLevel="warning"
      />
    )
    
    const card = container.firstChild
    expect(card).toHaveClass("border-l-4")
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && yarn test src/pages/network/components/metric-card.test.tsx`
Expected: FAIL with "Cannot find module './metric-card'"

- [ ] **Step 3: Write minimal implementation**

```typescript
import type { ReactNode } from "react"

export interface MetricCardProps {
  title: string
  primaryMetric: string | number
  chart: ReactNode
  labels?: { label: string; value: string | number }[]
  alertLevel?: "none" | "warning" | "danger"
  onClick?: () => void
}

export function MetricCard({ title, primaryMetric, chart, labels, alertLevel = "none", onClick }: MetricCardProps) {
  const alertClass =
    alertLevel === "danger"
      ? "border-l-4 border-l-destructive bg-destructive/5"
      : alertLevel === "warning"
        ? "border-l-4 border-l-amber-500 bg-amber-500/5"
        : ""

  return (
    <div
      className={`rounded-xl border border-border bg-card p-4 ${alertClass} ${onClick ? "cursor-pointer hover:shadow-md transition-shadow" : ""}`}
      onClick={onClick}
    >
      <div className="text-xs uppercase tracking-[0.16em] text-muted-foreground">{title}</div>
      <div className="mt-2 text-3xl font-semibold text-foreground">{primaryMetric}</div>
      <div className="mt-4">{chart}</div>
      {labels && labels.length > 0 ? (
        <div className="mt-4 flex flex-wrap gap-4 text-xs text-muted-foreground">
          {labels.map((item, idx) => (
            <div key={idx}>
              <span className="font-medium">{item.label}:</span> {item.value}
            </div>
          ))}
        </div>
      ) : null}
    </div>
  )
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && yarn test src/pages/network/components/metric-card.test.tsx`
Expected: PASS (all 3 tests)

- [ ] **Step 5: Commit**

```bash
git add web/src/pages/network/components/metric-card.tsx web/src/pages/network/components/metric-card.test.tsx
git commit -m "feat(network): add metric card base component"
```

---

## Summary

This implementation plan provides the foundation tasks for the network page redesign. Tasks 1-4 establish the core infrastructure (hooks, utilities, base components). 

**Completed Foundation Tasks**: 4 tasks covering hooks, utilities, and base components

**Remaining Tasks** (to be added as implementation progresses):
- Task 5-12: Individual metric card components (8 cards)
- Task 13: FilterBar component
- Task 14: Main page integration
- Task 15: Integration tests

The pattern established in Tasks 1-4 should be followed for all remaining tasks: TDD with failing tests first, minimal implementations, verification, and frequent commits.



