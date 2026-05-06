import { useCallback, useMemo } from "react"
import { useSearchParams } from "react-router-dom"

import type { NetworkTimeRange } from "../utils/aggregation"

const validRanges = new Set<NetworkTimeRange>(["1m", "5m", "15m"])

function parseNodes(value: string | null) {
  if (!value) return []
  return value.split(",").map((item) => Number(item)).filter((item) => Number.isInteger(item) && item > 0)
}

function serializeNodes(nodeIds: number[]) {
  return Array.from(new Set(nodeIds)).filter((nodeId) => Number.isInteger(nodeId) && nodeId > 0).sort((a, b) => a - b).join(",")
}

export function useNodeFilter() {
  const [searchParams, setSearchParams] = useSearchParams()

  const selectedNodes = useMemo(() => parseNodes(searchParams.get("nodes")), [searchParams])
  const timeRange = useMemo<NetworkTimeRange>(() => {
    const value = searchParams.get("range") as NetworkTimeRange | null
    return value && validRanges.has(value) ? value : "1m"
  }, [searchParams])
  const autoRefresh = searchParams.get("autoRefresh") === "1"

  const updateParams = useCallback((update: (next: URLSearchParams) => void) => {
    setSearchParams((current) => {
      const next = new URLSearchParams(current)
      update(next)
      return next
    }, { replace: true })
  }, [setSearchParams])

  const setSelectedNodes = useCallback((nodeIds: number[]) => {
    updateParams((next) => {
      const serialized = serializeNodes(nodeIds)
      if (serialized) next.set("nodes", serialized)
      else next.delete("nodes")
    })
  }, [updateParams])

  const setTimeRange = useCallback((range: NetworkTimeRange) => {
    updateParams((next) => {
      next.set("range", range)
    })
  }, [updateParams])

  const toggleAutoRefresh = useCallback(() => {
    updateParams((next) => {
      if (next.get("autoRefresh") === "1") next.delete("autoRefresh")
      else next.set("autoRefresh", "1")
    })
  }, [updateParams])

  return {
    selectedNodes,
    timeRange,
    autoRefresh,
    setSelectedNodes,
    setTimeRange,
    toggleAutoRefresh,
    isNodeSelected: (nodeId: number) => selectedNodes.includes(nodeId),
  }
}
