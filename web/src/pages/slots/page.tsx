import type { FormEvent } from "react"
import { Fragment, useCallback, useEffect, useMemo, useState } from "react"
import { useIntl, type IntlShape } from "react-intl"

import { useAuthStore } from "@/auth/auth-store"
import { ActionFormDialog } from "@/components/manager/action-form-dialog"
import { ConfirmDialog } from "@/components/manager/confirm-dialog"
import { DetailSheet } from "@/components/manager/detail-sheet"
import { KeyValueList } from "@/components/manager/key-value-list"
import { ResourceState } from "@/components/manager/resource-state"
import { StatusBadge } from "@/components/manager/status-badge"
import { PageContainer } from "@/components/shell/page-container"
import { Button } from "@/components/ui/button"
import {
  ManagerApiError,
  addSlot,
  getNodes,
  getSlot,
  getSlotLogs,
  getSlots,
  removeSlot,
  rebalanceSlots,
  recoverSlot,
  transferSlotLeader,
} from "@/lib/manager-api"
import type {
  ManagerNode,
  ManagerNodesResponse,
  ManagerSlotDetailResponse,
  ManagerSlotLogEntry,
  ManagerSlotLogsResponse,
  ManagerSlotRebalanceResponse,
  ManagerSlotsResponse,
} from "@/lib/manager-api.types"

type SlotsState = {
  slots: ManagerSlotsResponse | null
  loading: boolean
  refreshing: boolean
  error: Error | null
}

type SlotLogsState = {
  page: ManagerSlotLogsResponse | null
  loading: boolean
  error: Error | null
}

const slotLogPageLimit = 50

const recoverStrategyValues = ["latest_live_replica"] as const

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

function formatDataSize(value: number) {
  return `${value} B`
}

function formatSlotLogCommand(entry: ManagerSlotLogEntry) {
  return entry.decoded_type || entry.decode_status || "-"
}

function formatSlotLogDecoded(entry: ManagerSlotLogEntry) {
  return entry.decoded ? JSON.stringify(entry.decoded, null, 2) : ""
}

export function SlotsPage() {
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
  const [slotLogs, setSlotLogs] = useState<SlotLogsState>({
    page: null,
    loading: false,
    error: null,
  })
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

  const loadSlotLogs = useCallback(async (slotId: number, nodeId: number, cursor?: number) => {
    const append = typeof cursor === "number"
    setSlotLogs((current) => ({
      ...current,
      loading: true,
      error: null,
    }))

    try {
      const page = await getSlotLogs(slotId, { nodeId, limit: slotLogPageLimit, cursor })
      setSlotLogs((current) => ({
        page: append && current.page
          ? { ...page, items: [...current.page.items, ...page.items] }
          : page,
        loading: false,
        error: null,
      }))
    } catch (error) {
      setSlotLogs((current) => ({
        ...current,
        loading: false,
        error: error instanceof Error ? error : new Error("slot log request failed"),
      }))
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
      const logNodeId = selectedNodeId
      await Promise.all([
        loadSlotDetail(slotId),
        logNodeId !== null ? loadSlotLogs(slotId, logNodeId) : Promise.resolve(),
      ])
    },
    [loadSlotDetail, loadSlotLogs, selectedNodeId],
  )

  const closeDetail = useCallback((open: boolean) => {
    if (open) {
      return
    }
    setSelectedSlotId(null)
    setDetail(null)
    setDetailError(null)
    setSlotLogs({ page: null, loading: false, error: null })
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
    if (selectedNodeId !== null) {
      await loadSlotLogs(slotId, selectedNodeId)
    }
  }, [loadSlotDetail, loadSlotLogs, loadSlots, selectedNodeId])

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

  const runAddSlot = useCallback(async () => {
    setAddPending(true)
    setAddError("")

    try {
      const nextDetail = await addSlot()
      setDetail(nextDetail)
      setSelectedSlotId(nextDetail.slot_id)
      setAddOpen(false)
      await loadSlots(true, selectedNodeId)
      if (selectedNodeId !== null) {
        await loadSlotLogs(nextDetail.slot_id, selectedNodeId)
      }
    } catch (error) {
      setAddError(error instanceof Error ? error.message : "slot add failed")
    } finally {
      setAddPending(false)
    }
  }, [loadSlotLogs, loadSlots, selectedNodeId])

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
      setSlotLogs({ page: null, loading: false, error: null })
      await loadSlots(true, selectedNodeId)
    } catch (error) {
      setRemoveError(error instanceof Error ? error.message : "slot remove failed")
    } finally {
      setRemovePending(false)
    }
  }, [loadSlots, selectedNodeId, selectedSlotId])

  return (
    <PageContainer>
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
          <div className="rounded-xl border border-border bg-card p-3 shadow-none">
            {state.slots.items.length > 0 ? (
              <div className="overflow-x-auto rounded-lg border border-border">
                <table className="w-full border-collapse">
                  <thead className="bg-muted/40 text-left text-xs uppercase tracking-[0.14em] text-muted-foreground">
                    <tr>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "slots.table.slot" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "slots.table.quorum" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "slots.table.sync" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "slots.table.desiredPeerSet" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "slots.table.currentPeerSet" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "slots.table.leader" })}</th>
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
                        <td className="px-3 py-3 text-sm text-muted-foreground">{slot.runtime.leader_id}</td>
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
            <div className="rounded-xl border border-border bg-card p-3 shadow-none">
              {rebalancePlan.items.length > 0 ? (
                <div className="space-y-3">
                  {rebalancePlan.items.map((item) => (
                    <div className="rounded-lg border border-border bg-muted/20 px-3 py-3" key={item.hash_slot}>
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
        </>
      ) : null}

      <DetailSheet
        description={
          detail
            ? intl.formatMessage({ id: "slots.detailDescriptionValue" }, { id: detail.runtime.leader_id })
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
          <>
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
                { label: intl.formatMessage({ id: "slots.detail.leaderId" }), value: detail.runtime.leader_id },
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
            <div className="mt-4 rounded-lg border border-border bg-muted/10 p-3">
              <div className="flex items-start justify-between gap-3">
                <div>
                  <h3 className="text-sm font-semibold text-foreground">
                    {intl.formatMessage({ id: "slots.logs.title" })}
                  </h3>
                  <p className="mt-1 text-xs text-muted-foreground">
                    {slotLogs.page
                      ? intl.formatMessage(
                        { id: "slots.logs.nodeWatermarkValue" },
                        {
                          node: slotLogs.page.node_id,
                          commit: slotLogs.page.commit_index,
                          applied: slotLogs.page.applied_index,
                        },
                      )
                      : intl.formatMessage(
                        { id: "slots.logs.description" },
                        { node: selectedNodeId ?? "-" },
                      )}
                  </p>
                </div>
                <Button
                  disabled={slotLogs.loading || selectedNodeId === null}
                  onClick={() => {
                    if (selectedNodeId !== null) {
                      void loadSlotLogs(detail.slot_id, selectedNodeId)
                    }
                  }}
                  size="sm"
                  variant="outline"
                >
                  {slotLogs.loading
                    ? intl.formatMessage({ id: "common.refreshing" })
                    : intl.formatMessage({ id: "common.refresh" })}
                </Button>
              </div>

              {slotLogs.loading && !slotLogs.page ? (
                <ResourceState kind="loading" title={intl.formatMessage({ id: "slots.logs.title" })} />
              ) : null}
              {!slotLogs.loading && slotLogs.error ? (
                <ResourceState
                  kind={mapErrorKind(slotLogs.error)}
                  onRetry={() => {
                    if (selectedNodeId !== null) {
                      void loadSlotLogs(detail.slot_id, selectedNodeId)
                    }
                  }}
                  title={intl.formatMessage({ id: "slots.logs.title" })}
                />
              ) : null}
              {!slotLogs.error && slotLogs.page ? (
                slotLogs.page.items.length > 0 ? (
                  <div className="mt-3 overflow-x-auto rounded-md border border-border">
                    <table className="w-full border-collapse">
                      <thead className="bg-background text-left text-[0.7rem] uppercase tracking-[0.14em] text-muted-foreground">
                        <tr>
                          <th className="px-3 py-2">{intl.formatMessage({ id: "slots.logs.table.index" })}</th>
                          <th className="px-3 py-2">{intl.formatMessage({ id: "slots.logs.table.term" })}</th>
                          <th className="px-3 py-2">{intl.formatMessage({ id: "slots.logs.table.type" })}</th>
                          <th className="px-3 py-2">{intl.formatMessage({ id: "slots.logs.table.command" })}</th>
                          <th className="px-3 py-2">{intl.formatMessage({ id: "slots.logs.table.dataSize" })}</th>
                        </tr>
                      </thead>
                      <tbody>
                        {slotLogs.page.items.map((entry) => {
                          const decoded = formatSlotLogDecoded(entry)
                          return (
                            <Fragment key={entry.index}>
                              <tr className="border-t border-border">
                                <td className="px-3 py-2 text-sm font-medium text-foreground">{entry.index}</td>
                                <td className="px-3 py-2 text-sm text-muted-foreground">{entry.term}</td>
                                <td className="px-3 py-2 text-sm text-muted-foreground">{entry.type}</td>
                                <td className="px-3 py-2 text-sm text-muted-foreground">
                                  {formatSlotLogCommand(entry)}
                                </td>
                                <td className="px-3 py-2 text-sm text-muted-foreground">
                                  {formatDataSize(entry.data_size)}
                                </td>
                              </tr>
                              {decoded ? (
                                <tr className="border-t border-border/60">
                                  <td className="px-3 pb-3" colSpan={5}>
                                    <div className="rounded-md border border-border/80 bg-muted/30 p-3">
                                      <div className="text-[0.68rem] font-semibold uppercase tracking-[0.14em] text-muted-foreground">
                                        {intl.formatMessage({ id: "slots.logs.table.decoded" })}
                                      </div>
                                      <pre className="mt-2 max-h-56 overflow-auto whitespace-pre-wrap break-words font-mono text-xs leading-relaxed text-foreground">
                                        {decoded}
                                      </pre>
                                    </div>
                                  </td>
                                </tr>
                              ) : null}
                            </Fragment>
                          )
                        })}
                      </tbody>
                    </table>
                    {slotLogs.page.next_cursor ? (
                      <div className="border-t border-border p-2 text-right">
                        <Button
                          disabled={slotLogs.loading}
                          onClick={() => {
                            if (slotLogs.page?.next_cursor) {
                              void loadSlotLogs(detail.slot_id, slotLogs.page.node_id, slotLogs.page.next_cursor)
                            }
                          }}
                          size="sm"
                          variant="outline"
                        >
                          {intl.formatMessage({ id: "slots.logs.loadMore" })}
                        </Button>
                      </div>
                    ) : null}
                  </div>
                ) : (
                  <ResourceState kind="empty" title={intl.formatMessage({ id: "slots.logs.title" })} />
                )
              ) : null}
            </div>
          </>
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
    </PageContainer>
  )
}
