import type { FormEvent } from "react"
import { useCallback, useEffect, useMemo, useState } from "react"
import { useIntl, type IntlShape } from "react-intl"

import { useAuthStore } from "@/auth/auth-store"
import { ActionFormDialog } from "@/components/manager/action-form-dialog"
import { ConfirmDialog } from "@/components/manager/confirm-dialog"
import { DetailSheet } from "@/components/manager/detail-sheet"
import { KeyValueList } from "@/components/manager/key-value-list"
import { ResourceState } from "@/components/manager/resource-state"
import { StatusBadge } from "@/components/manager/status-badge"
import { TableToolbar } from "@/components/manager/table-toolbar"
import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
import { SectionCard } from "@/components/shell/section-card"
import { Button } from "@/components/ui/button"
import {
  ManagerApiError,
  getSlot,
  getSlots,
  rebalanceSlots,
  recoverSlot,
  transferSlotLeader,
} from "@/lib/manager-api"
import type {
  ManagerSlotDetailResponse,
  ManagerSlotRebalanceResponse,
  ManagerSlotsResponse,
} from "@/lib/manager-api.types"

type SlotsState = {
  slots: ManagerSlotsResponse | null
  loading: boolean
  refreshing: boolean
  error: Error | null
}

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
    if (permission.resource !== resource) {
      return false
    }
    return permission.actions.includes(action) || permission.actions.includes("*")
  })
}

function formatNodeList(nodeIds: number[]) {
  return nodeIds.length > 0 ? nodeIds.join(", ") : "-"
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
  const [rebalanceOpen, setRebalanceOpen] = useState(false)
  const [rebalancePending, setRebalancePending] = useState(false)
  const [rebalanceError, setRebalanceError] = useState("")
  const [rebalancePlan, setRebalancePlan] = useState<ManagerSlotRebalanceResponse | null>(null)

  const loadSlots = useCallback(async (refreshing: boolean) => {
    setState((current) => ({
      ...current,
      loading: refreshing ? current.loading : true,
      refreshing,
      error: null,
    }))

    try {
      const slots = await getSlots()
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
    void loadSlots(false)
  }, [loadSlots])

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
  }, [])

  const refreshOpenDetail = useCallback(async (slotId: number) => {
    await loadSlots(true)
    await loadSlotDetail(slotId)
  }, [loadSlotDetail, loadSlots])

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

  const slotSummary = useMemo(() => {
    const items = state.slots?.items ?? []
    return {
      total: items.length,
      ready: items.filter((item) => item.state.quorum === "ready").length,
      inSync: items.filter((item) => item.state.sync === "in_sync").length,
      leaders: items.filter((item) => item.runtime.leader_id > 0).length,
    }
  }, [state.slots])

  return (
    <PageContainer>
      <PageHeader
        title={intl.formatMessage({ id: "nav.slots.title" })}
        description={intl.formatMessage({ id: "nav.slots.description" })}
        actions={
          <>
            <Button
              onClick={() => {
                void loadSlots(true)
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
                setRebalanceOpen(true)
                setRebalanceError("")
              }}
              size="sm"
            >
              {intl.formatMessage({ id: "slots.rebalance" })}
            </Button>
          </>
        }
      >
        <div className="flex flex-wrap gap-2 text-xs text-muted-foreground">
          <div className="rounded-md border border-border bg-background px-3 py-2">
            {intl.formatMessage({ id: "slots.scopeAllSlots" })}
          </div>
          <div className="rounded-md border border-border bg-background px-3 py-2">
            {state.slots
              ? intl.formatMessage({ id: "slots.totalValue" }, { total: state.slots.total })
              : intl.formatMessage({ id: "slots.totalPending" })}
          </div>
        </div>
      </PageHeader>

      {state.loading ? <ResourceState kind="loading" title={intl.formatMessage({ id: "nav.slots.title" })} /> : null}
      {!state.loading && state.error ? (
        <ResourceState
          kind={mapErrorKind(state.error)}
          onRetry={() => {
            void loadSlots(false)
          }}
          title={intl.formatMessage({ id: "nav.slots.title" })}
        />
      ) : null}
      {!state.loading && !state.error && state.slots ? (
        <>
          <section className="grid gap-3 md:grid-cols-2 xl:grid-cols-4">
            <SectionCard
              description={intl.formatMessage({ id: "slots.cards.leaderCoverage.description" })}
              title={intl.formatMessage({ id: "slots.cards.leaderCoverage.title" })}
            >
              <div className="text-3xl font-semibold text-foreground">{slotSummary.leaders}</div>
            </SectionCard>
            <SectionCard
              description={intl.formatMessage({ id: "slots.cards.ready.description" })}
              title={intl.formatMessage({ id: "slots.cards.ready.title" })}
            >
              <div className="text-3xl font-semibold text-foreground">{slotSummary.ready}</div>
            </SectionCard>
            <SectionCard
              description={intl.formatMessage({ id: "slots.cards.inSync.description" })}
              title={intl.formatMessage({ id: "slots.cards.inSync.title" })}
            >
              <div className="text-3xl font-semibold text-foreground">{slotSummary.inSync}</div>
            </SectionCard>
            <SectionCard
              description={intl.formatMessage({ id: "slots.cards.tracked.description" })}
              title={intl.formatMessage({ id: "slots.cards.tracked.title" })}
            >
              <div className="text-3xl font-semibold text-foreground">{slotSummary.total}</div>
            </SectionCard>
          </section>

          <SectionCard
            description={intl.formatMessage({ id: "slots.inventoryDescription" })}
            title={intl.formatMessage({ id: "slots.inventoryTitle" })}
          >
            <TableToolbar
              description={intl.formatMessage({ id: "slots.toolbarDescription" })}
              onRefresh={() => {
                void loadSlots(true)
              }}
              refreshing={state.refreshing}
              title={intl.formatMessage({ id: "slots.toolbarTitle" })}
            />
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
              <ResourceState kind="empty" title={intl.formatMessage({ id: "slots.inventoryTitle" })} />
            )}
          </SectionCard>

          {rebalancePlan ? (
            <SectionCard
              description={intl.formatMessage({ id: "slots.rebalancePlan.description" })}
              title={intl.formatMessage({ id: "slots.rebalancePlan.title" })}
            >
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
            </SectionCard>
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
    </PageContainer>
  )
}
