import { useCallback, useEffect, useMemo, useState } from "react"

import { getNetworkSummary } from "@/lib/manager-api"
import type { ManagerNetworkSummaryResponse } from "@/lib/manager-api.types"

import { aggregateNetworkMetrics, type FilteredNetworkMetrics } from "../utils/aggregation"

const autoRefreshMs = 30_000

type NetworkDataState = {
  summary: ManagerNetworkSummaryResponse | null
  loading: boolean
  refreshing: boolean
  error: Error | null
}

export function useNetworkData({ selectedNodes, autoRefresh }: { selectedNodes: number[]; autoRefresh: boolean }) {
  const [state, setState] = useState<NetworkDataState>({ summary: null, loading: true, refreshing: false, error: null })

  const loadNetwork = useCallback(async (refreshing: boolean) => {
    setState((current) => ({ ...current, loading: refreshing ? current.loading : true, refreshing, error: null }))
    try {
      const summary = await getNetworkSummary()
      setState({ summary, loading: false, refreshing: false, error: null })
    } catch (error) {
      setState({ summary: null, loading: false, refreshing: false, error: error instanceof Error ? error : new Error("network summary request failed") })
    }
  }, [])

  useEffect(() => {
    void loadNetwork(false)
  }, [loadNetwork])

  useEffect(() => {
    if (!autoRefresh) return undefined
    const timer = window.setInterval(() => {
      void loadNetwork(true)
    }, autoRefreshMs)
    return () => window.clearInterval(timer)
  }, [autoRefresh, loadNetwork])

  const filteredData = useMemo<FilteredNetworkMetrics | null>(() => {
    if (!state.summary) return null
    return aggregateNetworkMetrics(state.summary, selectedNodes)
  }, [selectedNodes, state.summary])

  return {
    ...state,
    filteredData,
    refresh: () => loadNetwork(true),
  }
}
