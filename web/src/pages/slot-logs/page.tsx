import { Fragment, useCallback, useEffect, useState } from "react"
import { useIntl, type IntlShape } from "react-intl"

import { useAuthStore } from "@/auth/auth-store"
import { ConfirmDialog } from "@/components/manager/confirm-dialog"
import { NodeFilter, defaultNodeId, hasNode } from "@/components/manager/node-filter"
import { ResourceState } from "@/components/manager/resource-state"
import { StatusBadge } from "@/components/manager/status-badge"
import { PageContainer } from "@/components/shell/page-container"
import { SectionCard } from "@/components/shell/section-card"
import { Button } from "@/components/ui/button"
import { compactSlotRaftLogOnNode, getNodes, getSlotLogs, getSlots, ManagerApiError } from "@/lib/manager-api"
import type {
  ManagerNodesResponse,
  ManagerSlotLogEntry,
  ManagerSlotLogsResponse,
  ManagerSlotRaftCompactNodeResult,
  ManagerSlotRaftCompactResponse,
  ManagerSlotsResponse,
} from "@/lib/manager-api.types"

const slotLogPageLimit = 50

type SlotLogsState = {
  page: ManagerSlotLogsResponse | null
  loading: boolean
  error: Error | null
}

type SlotListState = {
  slots: ManagerSlotsResponse | null
  loading: boolean
  error: Error | null
}

type SlotCompactionState = {
  result: ManagerSlotRaftCompactResponse | null
  loading: boolean
  error: Error | null
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

function formatDataSize(value: number) {
  return `${value} B`
}

function formatLogEntryCreatedAt(intl: IntlShape, value?: number) {
  if (!value || value <= 0) {
    return "-"
  }
  return new Intl.DateTimeFormat(intl.locale, {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  }).format(new Date(value))
}

function isRaftNoopEntry(entry: ManagerSlotLogEntry) {
  return entry.data_size === 0 && entry.decoded_type === "noop"
}

function formatSlotLogCommand(intl: IntlShape, entry: ManagerSlotLogEntry) {
  if (isRaftNoopEntry(entry)) {
    return intl.formatMessage({ id: "slotLogs.logs.command.noop" })
  }
  return entry.decoded_type || entry.decode_status || "-"
}

function formatSlotLogCommandHint(intl: IntlShape, entry: ManagerSlotLogEntry) {
  if (isRaftNoopEntry(entry)) {
    return intl.formatMessage({ id: "slotLogs.logs.command.noopHint" })
  }
  return ""
}

function formatSlotLogDecoded(entry: ManagerSlotLogEntry) {
  if (isRaftNoopEntry(entry)) {
    return ""
  }
  return entry.decoded ? JSON.stringify(entry.decoded, null, 2) : ""
}

function hasSlot(slots: ManagerSlotsResponse | null, slotId: number) {
  return Boolean(slots?.items.some((slot) => slot.slot_id === slotId))
}

function defaultSlotId(slots: ManagerSlotsResponse | null) {
  return slots?.items[0]?.slot_id ?? null
}

function formatSlotCompactionResult(intl: IntlShape, item: ManagerSlotRaftCompactNodeResult) {
  if (!item.success) {
    return item.error || intl.formatMessage({ id: "slotLogs.compaction.result.failed" })
  }
  if (item.compacted) {
    return intl.formatMessage(
      { id: "slotLogs.compaction.result.compacted" },
      { before: item.before_snapshot_index, after: item.after_snapshot_index, applied: item.applied_index },
    )
  }
  if (item.skipped_reason) {
    return intl.formatMessage({ id: "slotLogs.compaction.result.skipped" }, { reason: item.skipped_reason })
  }
  return intl.formatMessage({ id: "slotLogs.compaction.result.noChange" })
}

function SlotCompactionResultPanel({
  intl,
  result,
}: {
  intl: IntlShape
  result: ManagerSlotRaftCompactResponse
}) {
  return (
    <SectionCard
      description={intl.formatMessage({ id: "slotLogs.compaction.description" })}
      title={intl.formatMessage({ id: "slotLogs.compaction.title" })}
    >
      <div className="mb-3 flex flex-wrap items-center gap-3 text-sm">
        <span className="font-medium text-foreground">
          {intl.formatMessage(
            { id: "slotLogs.compaction.summary" },
            { succeeded: result.succeeded, failed: result.failed },
          )}
        </span>
        <span className="text-muted-foreground">
          {intl.formatMessage({ id: "slotLogs.compaction.total" }, { total: result.total })}
        </span>
      </div>
      <div className="overflow-hidden rounded-lg border border-border">
        <table className="min-w-full divide-y divide-border text-sm">
          <thead className="bg-muted/30 text-left text-xs uppercase tracking-[0.16em] text-muted-foreground">
            <tr>
              <th className="px-3 py-2">{intl.formatMessage({ id: "slotLogs.compaction.table.node" })}</th>
              <th className="px-3 py-2">{intl.formatMessage({ id: "slotLogs.compaction.table.slot" })}</th>
              <th className="px-3 py-2">{intl.formatMessage({ id: "slotLogs.compaction.table.outcome" })}</th>
              <th className="px-3 py-2">{intl.formatMessage({ id: "slotLogs.compaction.table.snapshot" })}</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {result.items.map((item) => (
              <tr key={`${item.node_id}-${item.slot_id}`}>
                <td className="px-3 py-3 font-medium text-foreground">
                  {intl.formatMessage({ id: "common.nodeValue" }, { id: item.node_id })}
                </td>
                <td className="px-3 py-3 font-medium text-foreground">
                  {intl.formatMessage({ id: "slotLogs.slotValue" }, { id: item.slot_id })}
                </td>
                <td className="px-3 py-3">
                  <StatusBadge value={item.success ? (item.compacted ? "compacted" : "skipped") : "failed"} />
                </td>
                <td className="px-3 py-3 text-muted-foreground">{formatSlotCompactionResult(intl, item)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </SectionCard>
  )
}

export function SlotLogsPanel() {
  const intl = useIntl()
  const permissions = useAuthStore((authState) => authState.permissions)
  const [nodes, setNodes] = useState<ManagerNodesResponse | null>(null)
  const [selectedNodeId, setSelectedNodeId] = useState<number | null>(null)
  const [selectedSlotId, setSelectedSlotId] = useState<number | null>(null)
  const [slotsState, setSlotsState] = useState<SlotListState>({
    slots: null,
    loading: true,
    error: null,
  })
  const [state, setState] = useState<SlotLogsState>({
    page: null,
    loading: false,
    error: null,
  })
  const [compactionState, setCompactionState] = useState<SlotCompactionState>({
    result: null,
    loading: false,
    error: null,
  })
  const [compactionDialogOpen, setCompactionDialogOpen] = useState(false)
  const [expandedIndexes, setExpandedIndexes] = useState<Set<number>>(() => new Set())
  const [slotReloadKey, setSlotReloadKey] = useState(0)

  const loadSlotLogs = useCallback(async (nodeId: number, slotId: number, cursor?: number) => {
    setState((current) => ({
      ...current,
      loading: true,
      error: null,
      page: cursor ? current.page : null,
    }))
    if (!cursor) {
      setExpandedIndexes(new Set())
    }

    try {
      const page = await getSlotLogs(slotId, { nodeId, limit: slotLogPageLimit, cursor })
      setState((current) => ({
        page: cursor && current.page
          ? { ...page, items: [...current.page.items, ...page.items] }
          : page,
        loading: false,
        error: null,
      }))
    } catch (error) {
      setState((current) => ({
        ...current,
        loading: false,
        error: error instanceof Error ? error : new Error("slot log request failed"),
      }))
    }
  }, [])

  useEffect(() => {
    let cancelled = false
    async function loadNodes() {
      try {
        const nextNodes = await getNodes()
        if (cancelled) {
          return
        }
        setNodes(nextNodes)
        setSelectedNodeId((current) => {
          if (current !== null && hasNode(nextNodes, current)) {
            return current
          }
          return defaultNodeId(nextNodes)
        })
      } catch (error) {
        if (cancelled) {
          return
        }
        setNodes(null)
        setSelectedNodeId(null)
        setSlotsState({
          slots: null,
          loading: false,
          error: error instanceof Error ? error : new Error("node request failed"),
        })
      }
    }
    void loadNodes()
    return () => {
      cancelled = true
    }
  }, [])

  useEffect(() => {
    let cancelled = false
    async function loadSlots(nodeId: number) {
      setSlotsState((current) => ({ ...current, loading: true, error: null }))
      setState({ page: null, loading: false, error: null })
      try {
        const nextSlots = await getSlots({ nodeId })
        if (cancelled) {
          return
        }
        setSlotsState({ slots: nextSlots, loading: false, error: null })
        setSelectedSlotId((current) => {
          if (current !== null && hasSlot(nextSlots, current)) {
            return current
          }
          return defaultSlotId(nextSlots)
        })
      } catch (error) {
        if (cancelled) {
          return
        }
        setSlotsState({
          slots: null,
          loading: false,
          error: error instanceof Error ? error : new Error("slot request failed"),
        })
        setSelectedSlotId(null)
      }
    }

    if (selectedNodeId === null) {
      if (nodes && nodes.items.length === 0) {
        setSlotsState({ slots: null, loading: false, error: null })
      }
      setSelectedSlotId(null)
      return
    }
    void loadSlots(selectedNodeId)
    return () => {
      cancelled = true
    }
  }, [nodes, selectedNodeId, slotReloadKey])

  useEffect(() => {
    if (selectedNodeId === null || selectedSlotId === null || slotsState.loading || slotsState.error) {
      return
    }
    void loadSlotLogs(selectedNodeId, selectedSlotId)
  }, [loadSlotLogs, selectedNodeId, selectedSlotId, slotsState.error, slotsState.loading])

  const toggleDecoded = useCallback((index: number) => {
    setExpandedIndexes((current) => {
      const next = new Set(current)
      if (next.has(index)) {
        next.delete(index)
      } else {
        next.add(index)
      }
      return next
    })
  }, [])

  const canRefresh = selectedNodeId !== null && selectedSlotId !== null && !state.loading && !slotsState.loading
  const hasSlotCompactionPermission = hasPermission(permissions, "cluster.node", "w") && hasPermission(permissions, "cluster.slot", "w")
  const canCompactSlotLogs = hasSlotCompactionPermission && selectedNodeId !== null && selectedSlotId !== null && !compactionState.loading

  const handleSlotCompaction = useCallback(async () => {
    if (!canCompactSlotLogs || selectedNodeId === null || selectedSlotId === null) {
      return
    }
    const targetNodeId = selectedNodeId
    const targetSlotId = selectedSlotId
    setCompactionDialogOpen(false)
    setCompactionState({ result: null, loading: true, error: null })
    try {
      const result = await compactSlotRaftLogOnNode(targetNodeId, targetSlotId)
      setCompactionState({ result, loading: false, error: null })
      void loadSlotLogs(targetNodeId, targetSlotId)
    } catch (error) {
      setCompactionState({
        result: null,
        loading: false,
        error: error instanceof Error ? error : new Error("slot raft compaction request failed"),
      })
    }
  }, [canCompactSlotLogs, loadSlotLogs, selectedNodeId, selectedSlotId])

  return (
    <>
      <div className="flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between">
        <div className="space-y-2">
          <div className="font-mono text-[11px] font-semibold uppercase tracking-[0.22em] text-muted-foreground">
            {intl.formatMessage({ id: "slotLogs.eyebrow" })}
          </div>
          <h2 className="text-xl font-semibold tracking-tight text-foreground">
            {intl.formatMessage({ id: "slotLogs.title" })}
          </h2>
          <p className="max-w-3xl text-sm leading-6 text-muted-foreground">
            {intl.formatMessage({ id: "slotLogs.description" })}
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <NodeFilter nodes={nodes} selectedNodeId={selectedNodeId} onNodeChange={setSelectedNodeId} />
          <label className="flex items-center gap-2 text-xs font-medium text-muted-foreground">
            <span>{intl.formatMessage({ id: "slotLogs.slotFilter" })}</span>
            <select
              aria-label={intl.formatMessage({ id: "slotLogs.slotFilter" })}
              className="h-7 rounded-md border border-border bg-background px-2 text-sm text-foreground outline-none focus:border-ring focus:ring-2 focus:ring-ring/30"
              disabled={!slotsState.slots || slotsState.slots.items.length === 0}
              onChange={(event) => {
                const nextSlotId = Number(event.target.value)
                setSelectedSlotId(Number.isInteger(nextSlotId) && nextSlotId >= 0 ? nextSlotId : null)
              }}
              value={selectedSlotId ?? ""}
            >
              {slotsState.slots?.items.map((slot) => (
                <option key={slot.slot_id} value={slot.slot_id}>
                  {intl.formatMessage({ id: "slotLogs.slotValue" }, { id: slot.slot_id })}
                </option>
              ))}
            </select>
          </label>
          <Button
            disabled={!canCompactSlotLogs}
            onClick={() => setCompactionDialogOpen(true)}
            size="sm"
            variant="outline"
          >
            {compactionState.loading ? intl.formatMessage({ id: "slotLogs.compaction.triggering" }) : intl.formatMessage({ id: "slotLogs.compaction.trigger" })}
          </Button>
          {!hasSlotCompactionPermission ? (
            <span className="text-xs text-muted-foreground">
              {intl.formatMessage({ id: "slotLogs.compaction.permissionRequired" })}
            </span>
          ) : null}
          <Button
            disabled={!canRefresh}
            onClick={() => {
              if (selectedNodeId !== null && selectedSlotId !== null) {
                void loadSlotLogs(selectedNodeId, selectedSlotId)
              }
            }}
            size="sm"
            variant="outline"
          >
            {state.loading ? intl.formatMessage({ id: "common.refreshing" }) : intl.formatMessage({ id: "common.refresh" })}
          </Button>
        </div>
      </div>

      {compactionState.error ? (
        <ResourceState
          kind={mapErrorKind(compactionState.error)}
          onRetry={() => setCompactionDialogOpen(true)}
          retryLabel={intl.formatMessage({ id: "common.retry" })}
          title={intl.formatMessage({ id: "slotLogs.compaction.title" })}
        />
      ) : compactionState.result ? (
        <SlotCompactionResultPanel intl={intl} result={compactionState.result} />
      ) : null}

      {slotsState.loading ? (
        <ResourceState kind="loading" title={intl.formatMessage({ id: "slotLogs.logs.title" })} />
      ) : slotsState.error ? (
        <ResourceState
          kind={mapErrorKind(slotsState.error)}
          onRetry={selectedNodeId !== null ? () => setSlotReloadKey((current) => current + 1) : undefined}
          retryLabel={intl.formatMessage({ id: "common.retry" })}
          title={intl.formatMessage({ id: "slotLogs.logs.title" })}
        />
      ) : state.loading && !state.page ? (
        <ResourceState kind="loading" title={intl.formatMessage({ id: "slotLogs.logs.title" })} />
      ) : state.error ? (
        <ResourceState
          kind={mapErrorKind(state.error)}
          onRetry={selectedNodeId !== null && selectedSlotId !== null ? () => void loadSlotLogs(selectedNodeId, selectedSlotId) : undefined}
          retryLabel={intl.formatMessage({ id: "common.retry" })}
          title={intl.formatMessage({ id: "slotLogs.logs.title" })}
        />
      ) : state.page && state.page.items.length > 0 ? (
        <section className="overflow-hidden rounded-lg border border-border bg-card">
          <div className="flex flex-col gap-2 border-b border-border p-4 lg:flex-row lg:items-center lg:justify-between">
            <div>
              <h2 className="text-base font-semibold text-foreground">
                {intl.formatMessage({ id: "slotLogs.logs.title" })}
              </h2>
              <p className="mt-1 text-sm text-muted-foreground">
                {intl.formatMessage(
                  { id: "slotLogs.logs.watermark" },
                  {
                    node: state.page.node_id,
                    slot: state.page.slot_id,
                    commit: state.page.commit_index,
                    applied: state.page.applied_index,
                  },
                )}
              </p>
            </div>
            <div className="text-xs text-muted-foreground">
              {intl.formatMessage(
                { id: "slotLogs.logs.range" },
                { first: state.page.first_index, last: state.page.last_index },
              )}
            </div>
          </div>
          <div className="overflow-x-auto">
            <table className="min-w-full divide-y divide-border text-sm">
              <thead className="bg-muted/50 text-left text-xs uppercase tracking-[0.18em] text-muted-foreground">
                <tr>
                  <th className="px-3 py-2">{intl.formatMessage({ id: "slotLogs.logs.table.index" })}</th>
                  <th className="px-3 py-2">{intl.formatMessage({ id: "slotLogs.logs.table.createdAt" })}</th>
                  <th className="px-3 py-2">{intl.formatMessage({ id: "slotLogs.logs.table.term" })}</th>
                  <th className="px-3 py-2">{intl.formatMessage({ id: "slotLogs.logs.table.type" })}</th>
                  <th className="px-3 py-2">{intl.formatMessage({ id: "slotLogs.logs.table.command" })}</th>
                  <th className="px-3 py-2">{intl.formatMessage({ id: "slotLogs.logs.table.dataSize" })}</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border">
                {state.page.items.map((entry) => {
                  const decoded = formatSlotLogDecoded(entry)
                  const expanded = decoded ? expandedIndexes.has(entry.index) : false
                  const commandHint = formatSlotLogCommandHint(intl, entry)
                  return (
                    <Fragment key={entry.index}>
                      <tr className="align-top">
                        <td className="px-3 py-3 font-medium text-foreground">{entry.index}</td>
                        <td className="px-3 py-3 text-muted-foreground">{formatLogEntryCreatedAt(intl, entry.created_at_ms)}</td>
                        <td className="px-3 py-3 text-muted-foreground">{entry.term}</td>
                        <td className="px-3 py-3 text-muted-foreground">{entry.type}</td>
                        <td className="px-3 py-3 text-foreground">
                          <div className="flex flex-col gap-2">
                            <span>{formatSlotLogCommand(intl, entry)}</span>
                            {commandHint ? (
                              <span className="text-xs text-muted-foreground/75">{commandHint}</span>
                            ) : null}
                            {decoded ? (
                              <Button onClick={() => toggleDecoded(entry.index)} size="sm" variant="ghost">
                                {intl.formatMessage({ id: expanded ? "slotLogs.logs.table.hideDecoded" : "slotLogs.logs.table.showDecoded" })}
                              </Button>
                            ) : null}
                            {expanded ? (
                              <pre className="max-w-xl overflow-x-auto rounded-md border border-border bg-muted/60 p-3 text-xs leading-5 text-muted-foreground">
                                {decoded}
                              </pre>
                            ) : null}
                          </div>
                        </td>
                        <td className="px-3 py-3 text-muted-foreground">{formatDataSize(entry.data_size)}</td>
                      </tr>
                    </Fragment>
                  )
                })}
              </tbody>
            </table>
          </div>
          {state.page.next_cursor ? (
            <div className="border-t border-border p-4">
              <Button
                disabled={state.loading}
                onClick={() => void loadSlotLogs(state.page!.node_id, state.page!.slot_id, state.page!.next_cursor)}
                variant="outline"
              >
                {intl.formatMessage({ id: "common.loadMore" })}
              </Button>
            </div>
          ) : null}
        </section>
      ) : (
        <ResourceState kind="empty" title={intl.formatMessage({ id: "slotLogs.logs.title" })} />
      )}

      <ConfirmDialog
        confirmLabel={intl.formatMessage({ id: "slotLogs.compaction.trigger" })}
        description={intl.formatMessage(
          { id: "slotLogs.compaction.dialog.description" },
          { node: selectedNodeId ?? "-", slot: selectedSlotId ?? "-" },
        )}
        onConfirm={() => void handleSlotCompaction()}
        onOpenChange={setCompactionDialogOpen}
        open={compactionDialogOpen}
        pending={compactionState.loading}
        title={intl.formatMessage({ id: "slotLogs.compaction.dialog.title" })}
      />
    </>
  )
}

export function SlotLogsPage() {
  return (
    <PageContainer>
      <SlotLogsPanel />
    </PageContainer>
  )
}
