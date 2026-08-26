import { useSyncExternalStore } from "react"

import { getOverview } from "@/lib/manager-api"

export type ClusterHealth = "healthy" | "warning" | "critical" | "unknown"

export type ClusterStatusSnapshot = {
  health: ClusterHealth
  total: number | null
  alive: number | null
  loading: boolean
}

export const clusterHealthPresentation: Record<ClusterHealth, {
  summaryMessageId: string
  stateMessageId: string
  iconClassName: string
  dotClassName: string
}> = {
  healthy: {
    summaryMessageId: "shell.clusterSummaryHealthy",
    stateMessageId: "shell.ready",
    iconClassName: "text-success",
    dotClassName: "bg-[var(--status-healthy)]",
  },
  warning: {
    summaryMessageId: "shell.clusterSummaryWarning",
    stateMessageId: "shell.degraded",
    iconClassName: "text-warning",
    dotClassName: "bg-warning",
  },
  critical: {
    summaryMessageId: "shell.clusterSummaryCritical",
    stateMessageId: "shell.unhealthy",
    iconClassName: "text-destructive",
    dotClassName: "bg-destructive",
  },
  unknown: {
    summaryMessageId: "shell.clusterSummaryUnknown",
    stateMessageId: "shell.unknown",
    iconClassName: "text-muted-foreground",
    dotClassName: "bg-muted-foreground",
  },
}

const initialSnapshot: ClusterStatusSnapshot = {
  health: "unknown",
  total: null,
  alive: null,
  loading: true,
}

let snapshot = initialSnapshot
let subscribers = 0
let refreshTimer: number | undefined
let refreshInFlight: Promise<void> | null = null
let refreshController: AbortController | null = null
let refreshGeneration = 0
const listeners = new Set<() => void>()
const clusterStatusRequestTimeoutMs = 10_000

function emit(next: ClusterStatusSnapshot) {
  snapshot = next
  listeners.forEach((listener) => listener())
}

function classifyClusterHealth(overview: Awaited<ReturnType<typeof getOverview>>): ClusterHealth {
  const { nodes, slots, tasks, cluster } = overview
  if (nodes.total <= 0) return "unknown"
  if (
    nodes.dead > 0 ||
    cluster.controller_leader_id <= 0 ||
    slots.quorum_lost > 0 ||
    slots.leader_missing > 0 ||
    tasks.failed > 0
  ) {
    return "critical"
  }
  if (
    nodes.alive < nodes.total ||
    nodes.suspect > 0 ||
    nodes.draining > 0 ||
    slots.unreported > 0 ||
    slots.peer_mismatch > 0 ||
    slots.epoch_lag > 0 ||
    tasks.retrying > 0
  ) {
    return "warning"
  }
  return "healthy"
}

function refreshClusterStatus() {
  if (refreshInFlight) return refreshInFlight

  const generation = ++refreshGeneration
  const controller = new AbortController()
  refreshController = controller
  const timeout = window.setTimeout(() => controller.abort(), clusterStatusRequestTimeoutMs)
  const request = getOverview({ signal: controller.signal })
    .then((overview) => {
      if (generation !== refreshGeneration) return
      emit({
        health: classifyClusterHealth(overview),
        total: overview.nodes.total,
        alive: overview.nodes.alive,
        loading: false,
      })
    })
    .catch(() => {
      if (generation !== refreshGeneration) return
      emit({ health: "unknown", total: null, alive: null, loading: false })
    })
    .finally(() => {
      window.clearTimeout(timeout)
      if (generation === refreshGeneration) {
        refreshInFlight = null
        refreshController = null
      }
    })
  refreshInFlight = request
  return request
}

function subscribe(listener: () => void) {
  listeners.add(listener)
  subscribers += 1
  if (subscribers === 1) {
    void refreshClusterStatus()
    refreshTimer = window.setInterval(() => void refreshClusterStatus(), 30_000)
  }

  return () => {
    listeners.delete(listener)
    subscribers -= 1
    if (subscribers === 0 && refreshTimer !== undefined) {
      window.clearInterval(refreshTimer)
      refreshTimer = undefined
      refreshGeneration += 1
      refreshController?.abort()
      refreshController = null
      refreshInFlight = null
      snapshot = initialSnapshot
    }
  }
}

function getSnapshot() {
  return snapshot
}

/** Returns the shared, periodically refreshed cluster health shown by the application shell. */
export function useClusterStatus() {
  return useSyncExternalStore(subscribe, getSnapshot, getSnapshot)
}
