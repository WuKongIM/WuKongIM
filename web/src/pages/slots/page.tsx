import type { FormEvent } from "react"
import { useCallback, useEffect, useMemo, useState } from "react"
import { useIntl, type IntlShape } from "react-intl"
import { useSearchParams } from "react-router-dom"

import { useAuthStore } from "@/auth/auth-store"
import { ActionFormDialog } from "@/components/manager/action-form-dialog"
import { ConfirmDialog } from "@/components/manager/confirm-dialog"
import { DetailSheet } from "@/components/manager/detail-sheet"
import { KeyValueList } from "@/components/manager/key-value-list"
import { ResourceState } from "@/components/manager/resource-state"
import { StatusBadge } from "@/components/manager/status-badge"
import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
import { PageTabs } from "@/components/shell/page-tabs"
import { SectionCard } from "@/components/shell/section-card"
import { Button } from "@/components/ui/button"
import { SlotLogsPanel } from "@/pages/slot-logs/page"
import {
  ManagerApiError,
  addSlot,
  executeSlotLeaderTransferBatch,
  getNodes,
  getOverview,
  getSlot,
  getSlots,
  planSlotLeaderTransfers,
  removeSlot,
  rebalanceSlots,
  recoverSlot,
  transferSlotLeader,
} from "@/lib/manager-api"
import type {
  ManagerNode,
  ManagerNodesResponse,
  ManagerOverviewResponse,
  ManagerOverviewSlotAnomalyItem,
  ManagerSlotDetailResponse,
  ManagerSlotHashSlots,
  ManagerSlotLeaderTransferBatchExecuteResponse,
  ManagerSlotLeaderTransferBatchPlanResponse,
  ManagerSlotRebalanceResponse,
  ManagerSlotsResponse,
} from "@/lib/manager-api.types"

type SlotsState = {
  slots: ManagerSlotsResponse | null
  loading: boolean
  refreshing: boolean
  error: Error | null
}

type SlotClusterOverviewState = {
  overview: ManagerOverviewResponse | null
  loading: boolean
  refreshing: boolean
  error: Error | null
}

type SlotAnomalyRow = ManagerOverviewSlotAnomalyItem & {
  reason: "quorum_lost" | "leader_missing" | "sync_mismatch"
}

const tabs = [
  { id: "list", labelMessageId: "slots.tabs.list" },
  { id: "logs", labelMessageId: "slots.tabs.logs" },
] as const

type SlotClusterTab = (typeof tabs)[number]["id"]

const recoverStrategyValues = ["latest_live_replica"] as const
const defaultBatchTargetPolicy = "least_leaders"
const defaultBatchMaxTasks = "8"

function normalizeTab(value: string | null): SlotClusterTab {
  return tabs.some((tab) => tab.id === value) ? (value as SlotClusterTab) : "list"
}

function formatTimestamp(intl: IntlShape, value: string) {
  return new Intl.DateTimeFormat(intl.locale, {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  }).format(new Date(value))
}

function SlotSummaryCell({ label, value }: { label: string; value: number | string }) {
  return (
    <div className="border-b border-border px-1 py-3 sm:px-3">
      <div className="text-xs text-muted-foreground">{label}</div>
      <div className="mt-1 font-mono text-2xl font-medium tabular-nums text-foreground">{value}</div>
    </div>
  )
}

function mapErrorKind(error: Error | null) {
  if (!(error instanceof ManagerApiError)) {
    return "error" as const
  }
  if (error.status === 403) {
    return "forbidden" as const
  }
  if (error.status === 503) {
    return "unavailable" as const
  }
  return "error" as const
}

function hasPermission(
  permissions: { resource: string; actions: string[] }[],
  resource: string,
  action: string,
) {
  return permissions.some((permission) => {
    if (permission.resource !== resource && permission.resource !== "*") {
      return false
    }
    return permission.actions.includes(action) || permission.actions.includes("*")
  })
}

function formatNodeList(nodeIds: number[]) {
  return nodeIds.length > 0 ? nodeIds.join(", ") : "-"
}

function parsePositiveInteger(value: string) {
  const parsed = Number(value)
  return Number.isInteger(parsed) && parsed > 0 ? parsed : null
}

function parseOptionalPositiveInteger(value: string) {
  if (value.trim() === "") {
    return 0
  }
  return parsePositiveInteger(value)
}

function parseSlotIDList(value: string) {
  const trimmed = value.trim()
  if (!trimmed) {
    return [] as number[]
  }
  const parts = trimmed.split(/[\s,]+/).filter(Boolean)
  const ids = parts.map((part) => Number(part))
  return ids.every((id) => Number.isInteger(id) && id > 0) ? ids : null
}

function defaultNodeId(nodes: ManagerNodesResponse | null) {
  if (!nodes || nodes.items.length === 0) {
    return null
  }
  return nodes.items.find((node) => node.is_local)?.node_id ?? nodes.items[0].node_id
}

function hasNode(nodes: ManagerNodesResponse | null, nodeId: number) {
  return Boolean(nodes?.items.some((node) => node.node_id === nodeId))
}

function formatNodeOption(intl: IntlShape, node: ManagerNode) {
  const label = node.name
    ? `${node.name} (${node.node_id})`
    : intl.formatMessage({ id: "slots.nodeValue" }, { id: node.node_id })
  return node.is_local ? intl.formatMessage({ id: "slots.localNodeValue" }, { label }) : label
}

function formatNodeLog(intl: IntlShape, slot: ManagerSlotsResponse["items"][number]) {
  if (!slot.node_log) {
    return "-"
  }
  return intl.formatMessage(
    { id: "slots.logHeightValue" },
    { commit: slot.node_log.commit_index, applied: slot.node_log.applied_index },
  )
}

function nodeRaftStatus(slot: ManagerSlotsResponse["items"][number]) {
  return slot.node_log?.role || "unknown"
}

function formatActualLeader(slot: ManagerSlotsResponse["items"][number]) {
  const leaderId = slot.runtime.leader_id || slot.node_log?.leader_id || 0
  return leaderId > 0 ? String(leaderId) : "-"
}

function sortedHashSlotItems(ownership: ManagerSlotHashSlots) {
  const items = Array.isArray(ownership.items) ? ownership.items : []
  return Array.from(new Set(items)).sort((left, right) => left - right)
}

function formatHashSlotRanges(items: number[]) {
  if (items.length === 0) {
    return "-"
  }

  const ranges: string[] = []
  let start = items[0]
  let previous = items[0]

  for (const item of items.slice(1)) {
    if (item === previous + 1) {
      previous = item
      continue
    }
    ranges.push(start === previous ? `${start}` : `${start}-${previous}`)
    start = item
    previous = item
  }
  ranges.push(start === previous ? `${start}` : `${start}-${previous}`)

  return ranges.join(", ")
}

function formatHashSlotCount(intl: IntlShape, ownership: ManagerSlotHashSlots) {
  return intl.formatMessage({ id: "slots.hashSlotCountValue" }, { count: ownership.count })
}

function slotAnomalyRows(overview: ManagerOverviewResponse): SlotAnomalyRow[] {
  return [
    ...overview.anomalies.slots.quorum_lost.items.map((item) => ({ ...item, reason: "quorum_lost" as const })),
    ...overview.anomalies.slots.leader_missing.items.map((item) => ({ ...item, reason: "leader_missing" as const })),
    ...overview.anomalies.slots.sync_mismatch.items.map((item) => ({ ...item, reason: "sync_mismatch" as const })),
  ]
}

function slotAnomalyCount(overview: ManagerOverviewResponse) {
  return (
    overview.anomalies.slots.quorum_lost.count +
    overview.anomalies.slots.leader_missing.count +
    overview.anomalies.slots.sync_mismatch.count
  )
}

function slotAnomalyReasonMessageId(reason: SlotAnomalyRow["reason"]) {
  switch (reason) {
    case "quorum_lost":
      return "slots.unhealthy.reason.quorumLost"
    case "leader_missing":
      return "slots.unhealthy.reason.leaderMissing"
    case "sync_mismatch":
      return "slots.unhealthy.reason.syncMismatch"
  }
}

function HashSlotOwnershipValue({
  intl,
  ownership,
}: {
  intl: IntlShape
  ownership?: ManagerSlotHashSlots | null
}) {
  if (!ownership) {
    return <span className="text-muted-foreground">-</span>
  }

  const items = sortedHashSlotItems(ownership)
  const ranges = formatHashSlotRanges(items)

  return (
    <div className="max-w-64">
      <div className="text-sm font-medium text-foreground">{formatHashSlotCount(intl, ownership)}</div>
      <div className="mt-1 truncate font-mono text-xs text-muted-foreground" title={ranges}>
        {ranges}
      </div>
    </div>
  )
}

export function SlotClusterListPanel() {
  const intl = useIntl()
  const recoverStrategies = useMemo(
    () => [
      {
        value: recoverStrategyValues[0],
        label: intl.formatMessage({ id: "slots.recoveryStrategy.latestLiveReplica" }),
      },
    ],
    [intl],
  )
  const permissions = useAuthStore((state) => state.permissions)
  const canWriteSlots = useMemo(
    () => hasPermission(permissions, "cluster.slot", "w"),
    [permissions],
  )
  const [state, setState] = useState<SlotsState>({
    slots: null,
    loading: true,
    refreshing: false,
    error: null,
  })
  const [nodes, setNodes] = useState<ManagerNodesResponse | null>(null)
  const [selectedNodeId, setSelectedNodeId] = useState<number | null>(null)
  const [selectedSlotId, setSelectedSlotId] = useState<number | null>(null)
  const [detail, setDetail] = useState<ManagerSlotDetailResponse | null>(null)
  const [detailLoading, setDetailLoading] = useState(false)
  const [detailError, setDetailError] = useState<Error | null>(null)
  const [transferOpen, setTransferOpen] = useState(false)
  const [transferPending, setTransferPending] = useState(false)
  const [transferError, setTransferError] = useState("")
  const [targetNodeId, setTargetNodeId] = useState("")
  const [recoverOpen, setRecoverOpen] = useState(false)
  const [recoverPending, setRecoverPending] = useState(false)
  const [recoverError, setRecoverError] = useState("")
  const [recoverStrategy, setRecoverStrategy] = useState<string>(recoverStrategies[0].value)
  const [addOpen, setAddOpen] = useState(false)
  const [addPending, setAddPending] = useState(false)
  const [addError, setAddError] = useState("")
  const [removeOpen, setRemoveOpen] = useState(false)
  const [removePending, setRemovePending] = useState(false)
  const [removeError, setRemoveError] = useState("")
  const [rebalanceOpen, setRebalanceOpen] = useState(false)
  const [rebalancePending, setRebalancePending] = useState(false)
  const [rebalanceError, setRebalanceError] = useState("")
  const [rebalancePlan, setRebalancePlan] = useState<ManagerSlotRebalanceResponse | null>(null)
  const [batchTransferOpen, setBatchTransferOpen] = useState(false)
  const [batchTransferPending, setBatchTransferPending] = useState(false)
  const [batchTransferError, setBatchTransferError] = useState("")
  const [batchSourceNodeId, setBatchSourceNodeId] = useState("")
  const [batchTargetNodeId, setBatchTargetNodeId] = useState("")
  const [batchSlotIds, setBatchSlotIds] = useState("")
  const [batchMaxTasks, setBatchMaxTasks] = useState(defaultBatchMaxTasks)
  const [batchTargetPolicy, setBatchTargetPolicy] = useState(defaultBatchTargetPolicy)
  const [batchTransferPlan, setBatchTransferPlan] = useState<ManagerSlotLeaderTransferBatchPlanResponse | null>(null)
  const [batchTransferResult, setBatchTransferResult] =
    useState<ManagerSlotLeaderTransferBatchExecuteResponse | null>(null)

  const loadNodes = useCallback(async () => {
    try {
      const nextNodes = await getNodes()
      setNodes(nextNodes)
      setSelectedNodeId((current) => {
        if (current !== null && hasNode(nextNodes, current)) {
          return current
        }
        return defaultNodeId(nextNodes)
      })
      if (nextNodes.items.length === 0) {
        setState({ slots: null, loading: false, refreshing: false, error: null })
      }
    } catch (error) {
      setNodes(null)
      setSelectedNodeId(null)
      setState({
        slots: null,
        loading: false,
        refreshing: false,
        error: error instanceof Error ? error : new Error("node request failed"),
      })
    }
  }, [])

  const loadSlots = useCallback(async (refreshing: boolean, nodeId: number | null) => {
    if (!nodeId) {
      setState({ slots: null, loading: false, refreshing: false, error: null })
      return
    }
    setState((current) => ({
      ...current,
      loading: refreshing ? current.loading : true,
      refreshing,
      error: null,
    }))

    try {
      const slots = await getSlots({ nodeId })
      setState({ slots, loading: false, refreshing: false, error: null })
    } catch (error) {
      setState({
        slots: null,
        loading: false,
        refreshing: false,
        error: error instanceof Error ? error : new Error("slot request failed"),
      })
    }
  }, [])

  const loadSlotDetail = useCallback(async (slotId: number) => {
    setDetailLoading(true)
    setDetailError(null)

    try {
      const nextDetail = await getSlot(slotId)
      setDetail(nextDetail)
    } catch (error) {
      setDetail(null)
      setDetailError(error instanceof Error ? error : new Error("slot detail failed"))
    } finally {
      setDetailLoading(false)
    }
  }, [])

  useEffect(() => {
    void loadNodes()
  }, [loadNodes])

  useEffect(() => {
    if (selectedNodeId !== null) {
      void loadSlots(false, selectedNodeId)
    }
  }, [loadSlots, selectedNodeId])

  const openDetail = useCallback(
    async (slotId: number) => {
      setSelectedSlotId(slotId)
      await loadSlotDetail(slotId)
    },
    [loadSlotDetail],
  )

  const closeDetail = useCallback((open: boolean) => {
    if (open) {
      return
    }
    setSelectedSlotId(null)
    setDetail(null)
    setDetailError(null)
    setTransferOpen(false)
    setTransferError("")
    setTargetNodeId("")
    setRecoverOpen(false)
    setRecoverError("")
    setRecoverStrategy(recoverStrategies[0].value)
    setRemoveOpen(false)
    setRemoveError("")
  }, [])

  const refreshOpenDetail = useCallback(async (slotId: number) => {
    await loadSlots(true, selectedNodeId)
    await loadSlotDetail(slotId)
  }, [loadSlotDetail, loadSlots, selectedNodeId])

  const submitTransfer = useCallback(async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()

    if (!selectedSlotId) {
      return
    }

    const parsedTargetNodeId = Number(targetNodeId)
    if (!Number.isInteger(parsedTargetNodeId) || parsedTargetNodeId <= 0) {
      setTransferError(intl.formatMessage({ id: "slots.validation.targetNodeId" }))
      return
    }

    setTransferPending(true)
    setTransferError("")

    try {
      await transferSlotLeader(selectedSlotId, { targetNodeId: parsedTargetNodeId })
      setTransferOpen(false)
      setTargetNodeId("")
      await refreshOpenDetail(selectedSlotId)
    } catch (error) {
      setTransferError(error instanceof Error ? error.message : "slot transfer failed")
    } finally {
      setTransferPending(false)
    }
  }, [intl, refreshOpenDetail, selectedSlotId, targetNodeId])

  const submitRecover = useCallback(async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()

    if (!selectedSlotId) {
      return
    }

    if (!recoverStrategy) {
      setRecoverError(intl.formatMessage({ id: "slots.validation.recoveryStrategy" }))
      return
    }

    setRecoverPending(true)
    setRecoverError("")

    try {
      await recoverSlot(selectedSlotId, { strategy: recoverStrategy })
      setRecoverOpen(false)
      await refreshOpenDetail(selectedSlotId)
    } catch (error) {
      setRecoverError(error instanceof Error ? error.message : "slot recovery failed")
    } finally {
      setRecoverPending(false)
    }
  }, [recoverStrategy, refreshOpenDetail, selectedSlotId])

  const runRebalance = useCallback(async () => {
    setRebalancePending(true)
    setRebalanceError("")

    try {
      const nextPlan = await rebalanceSlots()
      setRebalancePlan(nextPlan)
      setRebalanceOpen(false)
    } catch (error) {
      setRebalanceError(error instanceof Error ? error.message : "slot rebalance failed")
    } finally {
      setRebalancePending(false)
    }
  }, [])

  const resetBatchTransferPlan = useCallback(() => {
    setBatchTransferPlan(null)
    setBatchTransferError("")
  }, [])

  const submitBatchTransfer = useCallback(async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()

    const sourceNodeId = parsePositiveInteger(batchSourceNodeId)
    const targetNodeId = parseOptionalPositiveInteger(batchTargetNodeId)
    const slotIds = parseSlotIDList(batchSlotIds)
    const maxTasks = parsePositiveInteger(batchMaxTasks)
    if (!sourceNodeId) {
      setBatchTransferError(intl.formatMessage({ id: "slots.validation.sourceNodeId" }))
      return
    }
    if (targetNodeId === null) {
      setBatchTransferError(intl.formatMessage({ id: "slots.validation.targetNodeId" }))
      return
    }
    if (slotIds === null) {
      setBatchTransferError(intl.formatMessage({ id: "slots.validation.slotIds" }))
      return
    }
    if (!maxTasks) {
      setBatchTransferError(intl.formatMessage({ id: "slots.validation.maxTasks" }))
      return
    }

    const input = {
      sourceNodeId,
      targetNodeId,
      slotIds,
      maxTasks,
      targetPolicy: batchTargetPolicy,
    }
    setBatchTransferPending(true)
    setBatchTransferError("")

    try {
      if (!batchTransferPlan) {
        const nextPlan = await planSlotLeaderTransfers(input)
        setBatchTransferPlan(nextPlan)
        return
      }

      const nextResult = await executeSlotLeaderTransferBatch({
        ...input,
        stateRevision: batchTransferPlan.state_revision,
        planId: batchTransferPlan.plan_id,
      })
      setBatchTransferResult(nextResult)
      setBatchTransferPlan(null)
      setBatchTransferOpen(false)
      await loadSlots(true, selectedNodeId)
    } catch (error) {
      setBatchTransferError(error instanceof Error ? error.message : "slot leader transfer batch failed")
    } finally {
      setBatchTransferPending(false)
    }
  }, [
    batchMaxTasks,
    batchSlotIds,
    batchSourceNodeId,
    batchTargetNodeId,
    batchTargetPolicy,
    batchTransferPlan,
    intl,
    loadSlots,
    selectedNodeId,
  ])

  const runAddSlot = useCallback(async () => {
    setAddPending(true)
    setAddError("")

    try {
      const nextDetail = await addSlot()
      setDetail(nextDetail)
      setSelectedSlotId(nextDetail.slot_id)
      setAddOpen(false)
      await loadSlots(true, selectedNodeId)
    } catch (error) {
      setAddError(error instanceof Error ? error.message : "slot add failed")
    } finally {
      setAddPending(false)
    }
  }, [loadSlots, selectedNodeId])

  const runRemoveSlot = useCallback(async () => {
    if (!selectedSlotId) {
      return
    }

    setRemovePending(true)
    setRemoveError("")

    try {
      await removeSlot(selectedSlotId)
      setRemoveOpen(false)
      setSelectedSlotId(null)
      setDetail(null)
      setDetailError(null)
      await loadSlots(true, selectedNodeId)
    } catch (error) {
      setRemoveError(error instanceof Error ? error.message : "slot remove failed")
    } finally {
      setRemovePending(false)
    }
  }, [loadSlots, selectedNodeId, selectedSlotId])

  return (
    <>
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h1 className="text-xl font-semibold tracking-tight text-foreground">
            {intl.formatMessage({ id: "nav.slots.title" })}
          </h1>
          <p className="mt-1 text-sm text-muted-foreground">
            {state.slots
              ? intl.formatMessage({ id: "slots.totalValue" }, { total: state.slots.total })
              : intl.formatMessage({ id: "slots.totalPending" })}
          </p>
        </div>
        <div className="flex flex-wrap gap-2">
          <label className="flex items-center gap-2 text-xs font-medium text-muted-foreground">
            <span>{intl.formatMessage({ id: "slots.nodeFilter" })}</span>
            <select
              className="h-7 rounded-md border border-border bg-background px-2 text-sm text-foreground outline-none focus:border-ring focus:ring-2 focus:ring-ring/30"
              disabled={!nodes || nodes.items.length === 0}
              onChange={(event) => {
                const nextNodeId = Number(event.target.value)
                setSelectedNodeId(Number.isInteger(nextNodeId) && nextNodeId > 0 ? nextNodeId : null)
              }}
              value={selectedNodeId ?? ""}
            >
              {nodes?.items.map((node) => (
                <option key={node.node_id} value={node.node_id}>
                  {formatNodeOption(intl, node)}
                </option>
              ))}
            </select>
          </label>
          <Button
            onClick={() => {
              void loadSlots(true, selectedNodeId)
            }}
            size="sm"
            variant="outline"
          >
            {state.refreshing
              ? intl.formatMessage({ id: "common.refreshing" })
              : intl.formatMessage({ id: "common.refresh" })}
          </Button>
          <Button
            disabled={!canWriteSlots}
            onClick={() => {
              setAddOpen(true)
              setAddError("")
            }}
            size="sm"
            variant="outline"
          >
            {intl.formatMessage({ id: "slots.addSlot" })}
          </Button>
          <Button
            disabled={!canWriteSlots}
            onClick={() => {
              setBatchTransferOpen(true)
              setBatchTransferError("")
              setBatchTransferPlan(null)
            }}
            size="sm"
            variant="outline"
          >
            {intl.formatMessage({ id: "slots.batchTransferLeader" })}
          </Button>
          <Button
            disabled={!canWriteSlots}
            onClick={() => {
              setRebalanceOpen(true)
              setRebalanceError("")
            }}
            size="sm"
          >
            {intl.formatMessage({ id: "slots.rebalance" })}
          </Button>
        </div>
      </div>

      {state.loading ? <ResourceState kind="loading" title={intl.formatMessage({ id: "nav.slots.title" })} /> : null}
      {!state.loading && state.error ? (
        <ResourceState
          kind={mapErrorKind(state.error)}
          onRetry={() => {
            if (selectedNodeId !== null) {
              void loadSlots(false, selectedNodeId)
              return
            }
            void loadNodes()
          }}
          title={intl.formatMessage({ id: "nav.slots.title" })}
        />
      ) : null}
      {!state.loading && !state.error && state.slots ? (
        <>
          <div data-slot-surface="inventory" className="rounded-lg border border-border bg-card p-3">
            {state.slots.items.length > 0 ? (
              <div className="overflow-x-auto rounded-md border border-border">
                <table
                  aria-label={intl.formatMessage({ id: "slots.inventoryTitle" })}
                  className="w-full border-collapse text-sm"
                >
                  <thead className="bg-muted/40 text-left text-xs uppercase tracking-[0.14em] text-muted-foreground">
                    <tr>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "slots.table.slot" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "slots.table.hashSlots" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "slots.table.quorum" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "slots.table.sync" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "slots.table.desiredPeerSet" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "slots.table.currentPeerSet" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "slots.table.leader" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "slots.table.status" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "slots.table.logHeight" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "slots.table.actions" })}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {state.slots.items.map((slot) => (
                      <tr className="border-t border-border" key={slot.slot_id}>
                        <td className="px-3 py-3 text-sm font-medium text-foreground">
                          {intl.formatMessage({ id: "slots.slotValue" }, { id: slot.slot_id })}
                        </td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">
                          <HashSlotOwnershipValue intl={intl} ownership={slot.hash_slots} />
                        </td>
                        <td className="px-3 py-3 text-sm text-foreground">
                          <StatusBadge value={slot.state.quorum} />
                        </td>
                        <td className="px-3 py-3 text-sm text-foreground">
                          <StatusBadge value={slot.state.sync} />
                        </td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">
                          {formatNodeList(slot.assignment.desired_peers)}
                        </td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">
                          {formatNodeList(slot.runtime.current_peers)}
                        </td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">{formatActualLeader(slot)}</td>
                        <td className="px-3 py-3 text-sm text-foreground">
                          <StatusBadge value={nodeRaftStatus(slot)} />
                        </td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">{formatNodeLog(intl, slot)}</td>
                        <td className="px-3 py-3 text-sm text-foreground">
                          <Button
                            aria-label={intl.formatMessage({ id: "slots.inspectSlot" }, { id: slot.slot_id })}
                            onClick={() => {
                              void openDetail(slot.slot_id)
                            }}
                            size="sm"
                            variant="outline"
                          >
                            {intl.formatMessage({ id: "common.inspect" })}
                          </Button>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            ) : (
              <ResourceState kind="empty" title={intl.formatMessage({ id: "nav.slots.title" })} />
            )}
          </div>

          {rebalancePlan ? (
            <div data-slot-surface="rebalance-result" className="rounded-md border border-border bg-card p-3">
              {rebalancePlan.items.length > 0 ? (
                <div className="space-y-3">
                  {rebalancePlan.items.map((item) => (
                    <div className="rounded-md border border-border bg-muted/20 px-3 py-3" key={item.hash_slot}>
                      <div className="text-sm font-medium text-foreground">
                        {intl.formatMessage({ id: "slots.rebalancePlan.hashSlotValue" }, { id: item.hash_slot })}
                      </div>
                      <div className="mt-1 text-sm text-muted-foreground">
                        {intl.formatMessage(
                          { id: "slots.rebalancePlan.fromToValue" },
                          { from: item.from_slot_id, to: item.to_slot_id },
                        )}
                      </div>
                    </div>
                  ))}
                </div>
              ) : (
                <ResourceState kind="empty" title={intl.formatMessage({ id: "slots.rebalancePlan.title" })} />
              )}
            </div>
          ) : null}

          {batchTransferResult ? (
            <div data-slot-surface="batch-transfer-result" className="rounded-md border border-border bg-card p-3">
              <div className="text-sm font-medium text-foreground">
                {intl.formatMessage(
                  { id: "slots.batchTransferResult.summary" },
                  {
                    created: batchTransferResult.summary.created,
                    existing: batchTransferResult.summary.existing,
                    failed: batchTransferResult.summary.failed,
                  },
                )}
              </div>
              {batchTransferResult.results.length > 0 ? (
                <div className="mt-3 grid gap-2 md:grid-cols-2 xl:grid-cols-3">
                  {batchTransferResult.results.map((item) => (
                    <div className="rounded-md border border-border bg-muted/20 px-3 py-2" key={item.slot_id}>
                      <div className="text-sm font-medium text-foreground">
                        {intl.formatMessage(
                          { id: "slots.batchTransferResult.item" },
                          { slot: item.slot_id, target: item.target_node_id },
                        )}
                      </div>
                      <div className="mt-1 text-xs text-muted-foreground">{item.status}</div>
                    </div>
                  ))}
                </div>
              ) : null}
            </div>
          ) : null}
        </>
      ) : null}

      <DetailSheet
        description={
          detail
            ? intl.formatMessage({ id: "slots.detailDescriptionValue" }, { id: formatActualLeader(detail) })
            : intl.formatMessage({ id: "slots.detailDescriptionFallback" })
        }
        footer={
          detail ? (
            <div className="flex items-center justify-end gap-2">
              <Button
                disabled={!canWriteSlots}
                onClick={() => {
                  setRemoveOpen(true)
                  setRemoveError("")
                }}
                size="sm"
                variant="destructive"
              >
                {intl.formatMessage({ id: "slots.removeSlot" })}
              </Button>
              <Button
                disabled={!canWriteSlots}
                onClick={() => {
                  setTransferOpen(true)
                  setTransferError("")
                }}
                size="sm"
                variant="outline"
              >
                {intl.formatMessage({ id: "slots.transferLeader" })}
              </Button>
              <Button
                disabled={!canWriteSlots}
                onClick={() => {
                  setRecoverOpen(true)
                  setRecoverError("")
                }}
                size="sm"
              >
                {intl.formatMessage({ id: "slots.recoverSlot" })}
              </Button>
            </div>
          ) : null
        }
        onOpenChange={closeDetail}
        open={selectedSlotId !== null}
        title={
          detail
            ? intl.formatMessage({ id: "slots.detailTitleValue" }, { id: detail.slot_id })
            : intl.formatMessage({ id: "slots.detailTitleFallback" })
        }
      >
        {detailLoading ? (
          <ResourceState kind="loading" title={intl.formatMessage({ id: "slots.detailTitleFallback" })} />
        ) : null}
        {!detailLoading && detailError ? (
          <ResourceState
            kind={mapErrorKind(detailError)}
            onRetry={() => {
              if (selectedSlotId) {
                void loadSlotDetail(selectedSlotId)
              }
            }}
            title={intl.formatMessage({ id: "slots.detailTitleFallback" })}
          />
        ) : null}
        {!detailLoading && !detailError && detail ? (
          <div className="rounded-md border border-border bg-card p-3" data-slot-surface="detail">
            <KeyValueList
              items={[
                {
                  label: intl.formatMessage({ id: "slots.detail.desiredPeers" }),
                  value: formatNodeList(detail.assignment.desired_peers),
                },
                {
                  label: intl.formatMessage({ id: "slots.detail.currentPeers" }),
                  value: formatNodeList(detail.runtime.current_peers),
                },
                {
                  label: intl.formatMessage({ id: "slots.detail.hashSlots" }),
                  value: <HashSlotOwnershipValue intl={intl} ownership={detail.hash_slots} />,
                },
                {
                  label: intl.formatMessage({ id: "slots.detail.taskStatus" }),
                  value: detail.task ? <StatusBadge value={detail.task.status} /> : "-",
                },
                {
                  label: intl.formatMessage({ id: "slots.detail.taskStep" }),
                  value: detail.task?.step ?? "-",
                },
                {
                  label: intl.formatMessage({ id: "slots.detail.quorum" }),
                  value: <StatusBadge value={detail.state.quorum} />,
                },
                {
                  label: intl.formatMessage({ id: "slots.detail.sync" }),
                  value: <StatusBadge value={detail.state.sync} />,
                },
                { label: intl.formatMessage({ id: "slots.detail.leaderId" }), value: formatActualLeader(detail) },
                {
                  label: intl.formatMessage({ id: "slots.detail.healthyVoters" }),
                  value: detail.runtime.healthy_voters,
                },
                {
                  label: intl.formatMessage({ id: "slots.detail.configEpoch" }),
                  value: detail.assignment.config_epoch,
                },
                {
                  label: intl.formatMessage({ id: "slots.detail.observedEpoch" }),
                  value: detail.runtime.observed_config_epoch,
                },
                {
                  label: intl.formatMessage({ id: "slots.detail.lastReport" }),
                  value: formatTimestamp(intl, detail.runtime.last_report_at),
                },
                {
                  label: intl.formatMessage({ id: "slots.detail.lastError" }),
                  value: detail.task?.last_error || "-",
                },
              ]}
            />
          </div>
        ) : null}
      </DetailSheet>

      <ActionFormDialog
        description={
          selectedSlotId
            ? intl.formatMessage({ id: "slots.transferDescription" }, { id: selectedSlotId })
            : undefined
        }
        error={transferError}
        onOpenChange={(open) => {
          setTransferOpen(open)
          if (!open) {
            setTransferError("")
            setTargetNodeId("")
          }
        }}
        onSubmit={(event) => {
          void submitTransfer(event)
        }}
        open={transferOpen}
        pending={transferPending}
        submitLabel={intl.formatMessage({ id: "slots.transfer" })}
        title={intl.formatMessage({ id: "slots.transferLeader" })}
      >
        <label className="grid gap-2 text-sm text-foreground">
          <span>{intl.formatMessage({ id: "slots.targetNodeId" })}</span>
          <input
            className="rounded-md border border-border bg-background px-3 py-2 text-sm outline-none"
            inputMode="numeric"
            onChange={(event) => setTargetNodeId(event.target.value)}
            value={targetNodeId}
          />
        </label>
      </ActionFormDialog>

      <ActionFormDialog
        description={intl.formatMessage({ id: "slots.batchTransferDescription" })}
        error={batchTransferError}
        onOpenChange={(open) => {
          setBatchTransferOpen(open)
          if (!open) {
            setBatchTransferError("")
            setBatchTransferPlan(null)
          }
        }}
        onSubmit={(event) => {
          void submitBatchTransfer(event)
        }}
        open={batchTransferOpen}
        pending={batchTransferPending}
        submitLabel={intl.formatMessage({
          id: batchTransferPlan ? "slots.batchTransferExecute" : "slots.batchTransferPreview",
        })}
        title={intl.formatMessage({ id: "slots.batchTransferLeader" })}
      >
        <div className="grid gap-3 sm:grid-cols-2">
          <label className="grid gap-2 text-sm text-foreground">
            <span>{intl.formatMessage({ id: "slots.sourceNodeId" })}</span>
            <input
              className="rounded-md border border-border bg-background px-3 py-2 text-sm outline-none"
              inputMode="numeric"
              onChange={(event) => {
                setBatchSourceNodeId(event.target.value)
                resetBatchTransferPlan()
              }}
              value={batchSourceNodeId}
            />
          </label>
          <label className="grid gap-2 text-sm text-foreground">
            <span>{intl.formatMessage({ id: "slots.targetNodeId" })}</span>
            <input
              className="rounded-md border border-border bg-background px-3 py-2 text-sm outline-none"
              inputMode="numeric"
              onChange={(event) => {
                setBatchTargetNodeId(event.target.value)
                resetBatchTransferPlan()
              }}
              value={batchTargetNodeId}
            />
          </label>
        </div>
        <label className="grid gap-2 text-sm text-foreground">
          <span>{intl.formatMessage({ id: "slots.batchTransferSlotIds" })}</span>
          <input
            className="rounded-md border border-border bg-background px-3 py-2 text-sm outline-none"
            onChange={(event) => {
              setBatchSlotIds(event.target.value)
              resetBatchTransferPlan()
            }}
            value={batchSlotIds}
          />
        </label>
        <div className="grid gap-3 sm:grid-cols-2">
          <label className="grid gap-2 text-sm text-foreground">
            <span>{intl.formatMessage({ id: "slots.batchTransferMaxTasks" })}</span>
            <input
              className="rounded-md border border-border bg-background px-3 py-2 text-sm outline-none"
              inputMode="numeric"
              onChange={(event) => {
                setBatchMaxTasks(event.target.value)
                resetBatchTransferPlan()
              }}
              value={batchMaxTasks}
            />
          </label>
          <label className="grid gap-2 text-sm text-foreground">
            <span>{intl.formatMessage({ id: "slots.batchTransferTargetPolicy" })}</span>
            <select
              className="rounded-md border border-border bg-background px-3 py-2 text-sm outline-none"
              onChange={(event) => {
                setBatchTargetPolicy(event.target.value)
                resetBatchTransferPlan()
              }}
              value={batchTargetPolicy}
            >
              <option value={defaultBatchTargetPolicy}>
                {intl.formatMessage({ id: "slots.batchTransferTargetPolicy.leastLeaders" })}
              </option>
            </select>
          </label>
        </div>
        {batchTransferPlan ? (
          <div className="rounded-lg border border-border bg-muted/20 px-3 py-3">
            <div className="text-sm font-medium text-foreground">
              {intl.formatMessage(
                { id: "slots.batchTransferPlan.summary" },
                {
                  candidates: batchTransferPlan.summary.candidates,
                  wouldCreate: batchTransferPlan.summary.would_create,
                  skipped: batchTransferPlan.summary.skipped,
                },
              )}
            </div>
            <div className="mt-3 grid gap-2">
              {batchTransferPlan.candidates.map((candidate) => (
                <div className="text-sm text-muted-foreground" key={candidate.slot_id}>
                  {intl.formatMessage(
                    { id: "slots.batchTransferPlan.candidate" },
                    { slot: candidate.slot_id, target: candidate.target_node_id },
                  )}
                </div>
              ))}
              {batchTransferPlan.skipped.map((item) => (
                <div className="text-sm text-muted-foreground" key={`skip-${item.slot_id}`}>
                  {intl.formatMessage(
                    { id: "slots.batchTransferPlan.skipped" },
                    { slot: item.slot_id, reason: item.message || item.reason },
                  )}
                </div>
              ))}
            </div>
          </div>
        ) : null}
      </ActionFormDialog>

      <ActionFormDialog
        description={
          selectedSlotId
            ? intl.formatMessage({ id: "slots.recoverDescription" }, { id: selectedSlotId })
            : undefined
        }
        error={recoverError}
        onOpenChange={(open) => {
          setRecoverOpen(open)
          if (!open) {
            setRecoverError("")
            setRecoverStrategy(recoverStrategies[0].value)
          }
        }}
        onSubmit={(event) => {
          void submitRecover(event)
        }}
        open={recoverOpen}
        pending={recoverPending}
        submitLabel={intl.formatMessage({ id: "slots.recover" })}
        title={intl.formatMessage({ id: "slots.recoverSlot" })}
      >
        <label className="grid gap-2 text-sm text-foreground">
          <span>{intl.formatMessage({ id: "slots.recoveryStrategy" })}</span>
          <select
            className="rounded-md border border-border bg-background px-3 py-2 text-sm outline-none"
            onChange={(event) => setRecoverStrategy(event.target.value)}
            value={recoverStrategy}
          >
            {recoverStrategies.map((strategy) => (
              <option key={strategy.value} value={strategy.value}>
                {strategy.label}
              </option>
            ))}
          </select>
        </label>
      </ActionFormDialog>

      <ConfirmDialog
        confirmLabel={intl.formatMessage({ id: "common.confirm" })}
        description={intl.formatMessage({ id: "slots.rebalanceDescription" })}
        error={rebalanceError}
        onConfirm={() => {
          void runRebalance()
        }}
        onOpenChange={(open) => {
          setRebalanceOpen(open)
          if (!open) {
            setRebalanceError("")
          }
        }}
        open={rebalanceOpen}
        pending={rebalancePending}
        title={intl.formatMessage({ id: "slots.rebalance" })}
      />

      <ConfirmDialog
        confirmLabel={intl.formatMessage({ id: "common.confirm" })}
        description={intl.formatMessage({ id: "slots.addDescription" })}
        error={addError}
        onConfirm={() => {
          void runAddSlot()
        }}
        onOpenChange={(open) => {
          setAddOpen(open)
          if (!open) {
            setAddError("")
          }
        }}
        open={addOpen}
        pending={addPending}
        title={intl.formatMessage({ id: "slots.addSlot" })}
      />

      <ConfirmDialog
        confirmLabel={intl.formatMessage({ id: "common.confirm" })}
        description={
          selectedSlotId
            ? intl.formatMessage({ id: "slots.removeDescription" }, { id: selectedSlotId })
            : undefined
        }
        error={removeError}
        onConfirm={() => {
          void runRemoveSlot()
        }}
        onOpenChange={(open) => {
          setRemoveOpen(open)
          if (!open) {
            setRemoveError("")
          }
        }}
        open={removeOpen}
        pending={removePending}
        title={intl.formatMessage({ id: "slots.removeSlot" })}
      />
    </>
  )
}


export function SlotClusterOverviewPanel() {
  const intl = useIntl()
  const [state, setState] = useState<SlotClusterOverviewState>({
    overview: null,
    loading: true,
    refreshing: false,
    error: null,
  })

  const loadOverview = useCallback(async (refreshing: boolean) => {
    setState((current) => ({
      ...current,
      loading: refreshing ? current.loading : true,
      refreshing,
      error: null,
    }))

    try {
      const overview = await getOverview()
      setState({ overview, loading: false, refreshing: false, error: null })
    } catch (error) {
      setState({
        overview: null,
        loading: false,
        refreshing: false,
        error: error instanceof Error ? error : new Error("slot overview request failed"),
      })
    }
  }, [])

  useEffect(() => {
    void loadOverview(false)
  }, [loadOverview])

  const overview = state.overview
  const unhealthyCount = overview ? slotAnomalyCount(overview) : 0

  return (
    <>
      <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div>
          <h2 className="text-xl font-semibold tracking-tight text-foreground">
            {intl.formatMessage({ id: "slots.overview.title" })}
          </h2>
          <p className="mt-1 max-w-3xl text-sm leading-6 text-muted-foreground">
            {intl.formatMessage({ id: "nav.slots.description" })}
          </p>
        </div>
        <Button
          onClick={() => {
            void loadOverview(true)
          }}
          size="sm"
          variant="outline"
        >
          {state.refreshing
            ? intl.formatMessage({ id: "common.refreshing" })
            : intl.formatMessage({ id: "common.refresh" })}
        </Button>
      </div>
      {state.loading ? <ResourceState kind="loading" title={intl.formatMessage({ id: "nav.slots.title" })} /> : null}
      {!state.loading && state.error ? (
        <ResourceState
          kind={mapErrorKind(state.error)}
          onRetry={() => {
            void loadOverview(false)
          }}
          title={intl.formatMessage({ id: "nav.slots.title" })}
        />
      ) : null}
      {!state.loading && !state.error && overview ? (
        <>
          <div
            className="grid gap-0 border-y border-border sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-6"
            data-testid="slots-overview-summary-strip"
          >
            <SlotSummaryCell label={intl.formatMessage({ id: "slots.metric.total" })} value={overview.slots.total} />
            <SlotSummaryCell label={intl.formatMessage({ id: "slots.cards.ready.title" })} value={overview.slots.ready} />
            <SlotSummaryCell label={intl.formatMessage({ id: "slots.metric.quorumLost" })} value={overview.slots.quorum_lost} />
            <SlotSummaryCell label={intl.formatMessage({ id: "slots.metric.leaderMissing" })} value={overview.slots.leader_missing} />
            <SlotSummaryCell label={intl.formatMessage({ id: "slots.metric.peerMismatch" })} value={overview.slots.peer_mismatch} />
            <SlotSummaryCell label={intl.formatMessage({ id: "slots.metric.unreported" })} value={overview.slots.unreported} />
          </div>

          <section className="grid gap-4 xl:grid-cols-[0.8fr_1.2fr]">
            <SectionCard
              description={intl.formatMessage({ id: "slots.unhealthy.summary" }, { count: unhealthyCount })}
              title={intl.formatMessage({ id: "slots.unhealthy.title" })}
            >
              <div
                className="rounded-md border border-border bg-muted/30 px-3 py-3 text-sm leading-6 text-muted-foreground"
                data-slot-surface="overview-unhealthy"
              >
                {intl.formatMessage(
                  { id: "slots.unhealthy.breakdown" },
                  {
                    quorumLost: overview.anomalies.slots.quorum_lost.count,
                    leaderMissing: overview.anomalies.slots.leader_missing.count,
                    syncMismatch: overview.anomalies.slots.sync_mismatch.count,
                  },
                )}
              </div>
            </SectionCard>

            <SectionCard title={intl.formatMessage({ id: "slots.overview.runtime.title" })}>
              <div className="grid gap-3 sm:grid-cols-3" data-slot-surface="overview-runtime">
                <div className="rounded-md border border-border bg-muted/20 px-3 py-3">
                  <div className="text-xs font-medium uppercase tracking-[0.14em] text-muted-foreground">
                    {intl.formatMessage({ id: "slots.metric.epochLag" })}
                  </div>
                  <div className="mt-2 text-2xl font-semibold text-foreground">{overview.slots.epoch_lag}</div>
                </div>
                <div className="rounded-md border border-border bg-muted/20 px-3 py-3">
                  <div className="text-xs font-medium uppercase tracking-[0.14em] text-muted-foreground">
                    {intl.formatMessage({ id: "slots.metric.taskRetrying" })}
                  </div>
                  <div className="mt-2 text-2xl font-semibold text-foreground">{overview.tasks.retrying}</div>
                </div>
                <div className="rounded-md border border-border bg-muted/20 px-3 py-3">
                  <div className="text-xs font-medium uppercase tracking-[0.14em] text-muted-foreground">
                    {intl.formatMessage({ id: "slots.metric.taskFailed" })}
                  </div>
                  <div className="mt-2 text-2xl font-semibold text-foreground">{overview.tasks.failed}</div>
                </div>
              </div>
            </SectionCard>
          </section>
        </>
      ) : null}
    </>
  )
}

export function SlotClusterUnhealthyPanel() {
  const intl = useIntl()
  const [state, setState] = useState<SlotClusterOverviewState>({
    overview: null,
    loading: true,
    refreshing: false,
    error: null,
  })

  const loadOverview = useCallback(async (refreshing: boolean) => {
    setState((current) => ({
      ...current,
      loading: refreshing ? current.loading : true,
      refreshing,
      error: null,
    }))

    try {
      const overview = await getOverview()
      setState({ overview, loading: false, refreshing: false, error: null })
    } catch (error) {
      setState({
        overview: null,
        loading: false,
        refreshing: false,
        error: error instanceof Error ? error : new Error("slot unhealthy request failed"),
      })
    }
  }, [])

  useEffect(() => {
    void loadOverview(false)
  }, [loadOverview])

  const rows = state.overview ? slotAnomalyRows(state.overview) : []

  return (
    <>
      <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div>
          <h2 className="text-xl font-semibold tracking-tight text-foreground">
            {intl.formatMessage({ id: "slots.unhealthy.title" })}
          </h2>
          <p className="mt-1 max-w-3xl text-sm leading-6 text-muted-foreground">
            {intl.formatMessage({ id: "slots.unhealthy.description" })}
          </p>
        </div>
        <Button
          onClick={() => {
            void loadOverview(true)
          }}
          size="sm"
          variant="outline"
        >
          {state.refreshing
            ? intl.formatMessage({ id: "common.refreshing" })
            : intl.formatMessage({ id: "common.refresh" })}
        </Button>
      </div>
      {state.loading ? <ResourceState kind="loading" title={intl.formatMessage({ id: "slots.unhealthy.title" })} /> : null}
      {!state.loading && state.error ? (
        <ResourceState
          kind={mapErrorKind(state.error)}
          onRetry={() => {
            void loadOverview(false)
          }}
          title={intl.formatMessage({ id: "slots.unhealthy.title" })}
        />
      ) : null}
      {!state.loading && !state.error && state.overview ? (
        <div data-slot-surface="unhealthy" className="rounded-md border border-border bg-card p-3">
          {rows.length > 0 ? (
            <div className="overflow-x-auto rounded-md border border-border" data-slot-surface="unhealthy-table">
              <table aria-label={intl.formatMessage({ id: "slots.unhealthy.title" })} className="w-full border-collapse text-sm">
                <thead className="bg-muted/40 text-left text-xs uppercase tracking-[0.14em] text-muted-foreground">
                  <tr>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "slots.table.slot" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "slots.unhealthy.table.reason" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "slots.table.quorum" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "slots.table.sync" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "slots.table.desiredPeerSet" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "slots.table.currentPeerSet" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "slots.table.leader" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "slots.unhealthy.table.lastReport" })}</th>
                  </tr>
                </thead>
                <tbody>
                  {rows.map((row) => (
                    <tr className="border-t border-border" key={`${row.reason}-${row.slot_id}`}>
                      <td className="px-3 py-3 text-sm font-medium text-foreground">
                        {intl.formatMessage({ id: "slots.slotValue" }, { id: row.slot_id })}
                      </td>
                      <td className="px-3 py-3 text-sm text-foreground">
                        {intl.formatMessage({ id: slotAnomalyReasonMessageId(row.reason) })}
                      </td>
                      <td className="px-3 py-3 text-sm text-foreground"><StatusBadge value={row.quorum} /></td>
                      <td className="px-3 py-3 text-sm text-foreground"><StatusBadge value={row.sync} /></td>
                      <td className="px-3 py-3 text-sm text-muted-foreground">{formatNodeList(row.desired_peers)}</td>
                      <td className="px-3 py-3 text-sm text-muted-foreground">{formatNodeList(row.current_peers)}</td>
                      <td className="px-3 py-3 text-sm text-muted-foreground">{row.leader_id}</td>
                      <td className="px-3 py-3 text-sm text-muted-foreground">
                        {formatTimestamp(intl, row.last_report_at)}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ) : (
            <ResourceState
              description={intl.formatMessage({ id: "slots.unhealthy.emptyDescription" })}
              kind="empty"
              title={intl.formatMessage({ id: "slots.unhealthy.emptyTitle" })}
            />
          )}
        </div>
      ) : null}
    </>
  )
}

export function SlotsPage() {
  const intl = useIntl()
  const [searchParams, setSearchParams] = useSearchParams()
  const activeTab = normalizeTab(searchParams.get("tab"))

  function setTab(tab: string) {
    const next = new URLSearchParams(searchParams)
    next.set("tab", tab)
    setSearchParams(next)
  }

  return (
    <PageContainer>
      <PageHeader
        eyebrow={intl.formatMessage({ id: "nav.path.cluster.slots" })}
        title={intl.formatMessage({ id: "nav.slots.title" })}
        description={intl.formatMessage({ id: "nav.slots.description" })}
      />
      <PageTabs
        activeTab={activeTab}
        className="px-0 pt-0"
        tabs={tabs.map((tab) => ({ id: tab.id, label: intl.formatMessage({ id: tab.labelMessageId }) }))}
        onTabChange={setTab}
      />
      {activeTab === "list" ? <SlotClusterListPanel /> : null}
      {activeTab === "logs" ? <SlotLogsPanel /> : null}
    </PageContainer>
  )
}
