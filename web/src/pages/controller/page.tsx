import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { useIntl, type IntlShape } from "react-intl"
import { useSearchParams } from "react-router-dom"

import { ActionFormDialog } from "@/components/manager/action-form-dialog"
import { NodeFilter, defaultNodeId, hasNode } from "@/components/manager/node-filter"
import { ResourceState } from "@/components/manager/resource-state"
import { StatusBadge } from "@/components/manager/status-badge"
import { PageContainer } from "@/components/shell/page-container"
import { SectionCard } from "@/components/shell/section-card"
import { Button } from "@/components/ui/button"
import { compactControllerRaftLogOnNode, compactControllerRaftLogs, getControllerLogs, getControllerRaftStatus, getNodes, ManagerApiError } from "@/lib/manager-api"
import type {
  ManagerControllerRaftCompactNodeResult,
  ManagerControllerRaftCompactResponse,
  ManagerControllerLogEntry,
  ManagerControllerLogsResponse,
  ManagerControllerRaftStatusResponse,
  ManagerNodesResponse,
} from "@/lib/manager-api.types"

const controllerLogPageLimit = 50

type ControllerLogsState = {
  page: ManagerControllerLogsResponse | null
  loading: boolean
  error: Error | null
}

type ControllerRaftStatusState = {
  status: ManagerControllerRaftStatusResponse | null
  loading: boolean
  error: Error | null
}

type ControllerCompactionState = {
  result: ManagerControllerRaftCompactResponse | null
  loading: boolean
  error: Error | null
}

type ControllerCompactionScope = "node" | "all"

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

function requestedNodeId(searchParams: URLSearchParams) {
  const value = Number(searchParams.get("node_id"))
  return Number.isInteger(value) && value > 0 ? value : null
}

function isRaftNoopEntry(entry: ManagerControllerLogEntry) {
  return entry.data_size === 0 && entry.decoded_type === "noop"
}

function formatControllerLogCommand(intl: IntlShape, entry: ManagerControllerLogEntry) {
  if (isRaftNoopEntry(entry)) {
    return intl.formatMessage({ id: "controller.logs.command.noop" })
  }
  return entry.decoded_type || entry.decode_status || "-"
}

function formatControllerLogDecoded(entry: ManagerControllerLogEntry) {
  if (isRaftNoopEntry(entry)) {
    return ""
  }
  return entry.decoded ? JSON.stringify(entry.decoded, null, 2) : ""
}

function formatRoleTerm(intl: IntlShape, status: ManagerControllerRaftStatusResponse) {
  return intl.formatMessage({ id: "controller.status.roleTerm" }, { role: status.role, term: status.term })
}

function formatCommitApplied(intl: IntlShape, status: ManagerControllerRaftStatusResponse) {
  return intl.formatMessage(
    { id: "controller.status.commitApplied" },
    { commit: status.commit_index, applied: status.applied_index },
  )
}

function formatSnapshot(intl: IntlShape, status: ManagerControllerRaftStatusResponse) {
  return intl.formatMessage(
    { id: "controller.status.snapshot" },
    { index: status.snapshot_index, term: status.snapshot_term },
  )
}

function formatPeerProgress(intl: IntlShape, match: number, next: number) {
  return intl.formatMessage({ id: "controller.status.peerProgress" }, { match, next })
}

function formatCompactionResult(intl: IntlShape, item: ManagerControllerRaftCompactNodeResult) {
  if (!item.success) {
    return item.error || intl.formatMessage({ id: "controller.compaction.result.failed" })
  }
  if (item.compacted) {
    return intl.formatMessage(
      { id: "controller.compaction.result.compacted" },
      { before: item.before_snapshot_index, after: item.after_snapshot_index, applied: item.applied_index },
    )
  }
  if (item.skipped_reason) {
    return intl.formatMessage({ id: "controller.compaction.result.skipped" }, { reason: item.skipped_reason })
  }
  return intl.formatMessage({ id: "controller.compaction.result.noChange" })
}

function ControllerCompactionResultPanel({
  intl,
  result,
}: {
  intl: IntlShape
  result: ManagerControllerRaftCompactResponse
}) {
  return (
    <SectionCard
      description={intl.formatMessage({ id: "controller.compaction.description" })}
      title={intl.formatMessage({ id: "controller.compaction.title" })}
    >
      <div className="mb-3 flex flex-wrap items-center gap-3 text-sm">
        <span className="font-medium text-foreground">
          {intl.formatMessage(
            { id: "controller.compaction.summary" },
            { succeeded: result.succeeded, failed: result.failed },
          )}
        </span>
        <span className="text-muted-foreground">
          {intl.formatMessage({ id: "controller.compaction.total" }, { total: result.total })}
        </span>
      </div>
      <div className="overflow-hidden rounded-lg border border-border">
        <table className="min-w-full divide-y divide-border text-sm">
          <thead className="bg-muted/30 text-left text-xs uppercase tracking-[0.16em] text-muted-foreground">
            <tr>
              <th className="px-3 py-2">{intl.formatMessage({ id: "controller.status.table.node" })}</th>
              <th className="px-3 py-2">{intl.formatMessage({ id: "controller.compaction.table.outcome" })}</th>
              <th className="px-3 py-2">{intl.formatMessage({ id: "controller.status.snapshotLabel" })}</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {result.items.map((item) => (
              <tr key={item.node_id}>
                <td className="px-3 py-3 font-medium text-foreground">
                  {intl.formatMessage({ id: "common.nodeValue" }, { id: item.node_id })}
                </td>
                <td className="px-3 py-3">
                  <StatusBadge value={item.success ? (item.compacted ? "compacted" : "skipped") : "failed"} />
                </td>
                <td className="px-3 py-3 text-muted-foreground">{formatCompactionResult(intl, item)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </SectionCard>
  )
}

function ControllerRaftStatusPanel({
  intl,
  status,
}: {
  intl: IntlShape
  status: ManagerControllerRaftStatusResponse
}) {
  return (
    <SectionCard
      description={intl.formatMessage({ id: "controller.status.description" }, { node: status.node_id })}
      title={intl.formatMessage({ id: "controller.status.title" })}
    >
      <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-5" data-testid="controller-status-strip">
        <div className="rounded-md border border-border bg-background px-3 py-3" data-controller-status-cell="">
          <div className="text-xs uppercase tracking-[0.16em] text-muted-foreground">
            {intl.formatMessage({ id: "controller.status.health" })}
          </div>
          <div className="mt-2">
            <StatusBadge value={status.health} />
          </div>
        </div>
        <div className="rounded-md border border-border bg-background px-3 py-3" data-controller-status-cell="">
          <div className="text-xs uppercase tracking-[0.16em] text-muted-foreground">
            {intl.formatMessage({ id: "controller.status.role" })}
          </div>
          <div className="mt-2 text-sm font-medium text-foreground">{formatRoleTerm(intl, status)}</div>
          <div className="mt-1 text-xs text-muted-foreground">
            {intl.formatMessage({ id: "controller.status.leader" }, { id: status.leader_id })}
          </div>
        </div>
        <div className="rounded-md border border-border bg-background px-3 py-3" data-controller-status-cell="">
          <div className="text-xs uppercase tracking-[0.16em] text-muted-foreground">
            {intl.formatMessage({ id: "controller.status.watermark" })}
          </div>
          <div className="mt-2 text-sm font-medium text-foreground">{formatCommitApplied(intl, status)}</div>
          <div className="mt-1 text-xs text-muted-foreground">
            {intl.formatMessage({ id: "controller.logs.range" }, { first: status.first_index, last: status.last_index })}
          </div>
        </div>
        <div className="rounded-md border border-border bg-background px-3 py-3" data-controller-status-cell="">
          <div className="text-xs uppercase tracking-[0.16em] text-muted-foreground">
            {intl.formatMessage({ id: "controller.status.snapshotLabel" })}
          </div>
          <div className="mt-2 text-sm font-medium text-foreground">{formatSnapshot(intl, status)}</div>
          <div className="mt-1 text-xs text-muted-foreground">
            {intl.formatMessage(
              { id: status.restore.failed ? "controller.status.restoreFailed" : "controller.status.restoreOk" },
              { index: status.restore.last_snapshot_index },
            )}
          </div>
        </div>
        <div className="rounded-md border border-border bg-background px-3 py-3" data-controller-status-cell="">
          <div className="text-xs uppercase tracking-[0.16em] text-muted-foreground">
            {intl.formatMessage({ id: "controller.status.compaction" })}
          </div>
          <div className="mt-2">
            <StatusBadge value={status.compaction.degraded ? "compaction_degraded" : "healthy"} />
          </div>
          <div className="mt-1 text-xs text-muted-foreground">
            {intl.formatMessage({ id: "controller.status.compactionTrigger" }, { entries: status.compaction.trigger_entries })}
          </div>
        </div>
      </div>

      <div className="mt-4 overflow-hidden rounded-lg border border-border">
        <div className="border-b border-border bg-muted/40 px-3 py-2 text-sm font-semibold text-foreground">
          {intl.formatMessage({ id: "controller.status.followers" })}
        </div>
        {status.peers.length > 0 ? (
          <div className="overflow-x-auto">
            <table className="min-w-full divide-y divide-border text-sm">
              <thead className="bg-muted/30 text-left text-xs uppercase tracking-[0.16em] text-muted-foreground">
                <tr>
                  <th className="px-3 py-2">{intl.formatMessage({ id: "controller.status.table.node" })}</th>
                  <th className="px-3 py-2">{intl.formatMessage({ id: "controller.status.table.progress" })}</th>
                  <th className="px-3 py-2">{intl.formatMessage({ id: "controller.status.table.state" })}</th>
                  <th className="px-3 py-2">{intl.formatMessage({ id: "controller.status.table.snapshot" })}</th>
                  <th className="px-3 py-2">{intl.formatMessage({ id: "controller.status.table.activity" })}</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border">
                {status.peers.map((peer) => (
                  <tr key={peer.node_id}>
                    <td className="px-3 py-3 font-medium text-foreground">
                      {intl.formatMessage({ id: "common.nodeValue" }, { id: peer.node_id })}
                    </td>
                    <td className="px-3 py-3 text-muted-foreground">{formatPeerProgress(intl, peer.match, peer.next)}</td>
                    <td className="px-3 py-3 text-muted-foreground">{peer.state || "-"}</td>
                    <td className="px-3 py-3">
                      <div className="flex flex-wrap gap-2">
                        {peer.needs_snapshot ? <StatusBadge value="needs_snapshot" /> : null}
                        {peer.snapshot_transferring ? <StatusBadge value="snapshot_transferring" /> : null}
                        {peer.pending_snapshot ? (
                          <span className="text-xs text-muted-foreground">
                            {intl.formatMessage({ id: "controller.status.pendingSnapshot" }, { index: peer.pending_snapshot })}
                          </span>
                        ) : null}
                        {!peer.needs_snapshot && !peer.snapshot_transferring && !peer.pending_snapshot ? (
                          <span className="text-muted-foreground">-</span>
                        ) : null}
                      </div>
                    </td>
                    <td className="px-3 py-3 text-muted-foreground">
                      {intl.formatMessage({ id: peer.recent_active ? "controller.status.recentActive" : "controller.status.inactive" })}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          <div className="px-3 py-4 text-sm text-muted-foreground">
            {intl.formatMessage({ id: "controller.status.noFollowers" })}
          </div>
        )}
      </div>
    </SectionCard>
  )
}

export function ControllerLogsPanel() {
  const intl = useIntl()
  const [searchParams, setSearchParams] = useSearchParams()
  const queryNodeId = useMemo(() => requestedNodeId(searchParams), [searchParams])
  const [nodes, setNodes] = useState<ManagerNodesResponse | null>(null)
  const [selectedNodeId, setSelectedNodeId] = useState<number | null>(null)
  const [state, setState] = useState<ControllerLogsState>({ page: null, loading: true, error: null })
  const [statusState, setStatusState] = useState<ControllerRaftStatusState>({ status: null, loading: false, error: null })
  const [compactionState, setCompactionState] = useState<ControllerCompactionState>({ result: null, loading: false, error: null })
  const [compactionDialogOpen, setCompactionDialogOpen] = useState(false)
  const [compactionScope, setCompactionScope] = useState<ControllerCompactionScope>("all")
  const [expandedIndexes, setExpandedIndexes] = useState<Set<number>>(() => new Set())
  const logsRequestSeq = useRef(0)
  const statusRequestSeq = useRef(0)

  const loadControllerLogs = useCallback(async (nodeId: number, cursor?: number) => {
    const requestSeq = logsRequestSeq.current + 1
    logsRequestSeq.current = requestSeq
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
      const page = await getControllerLogs({ nodeId, limit: controllerLogPageLimit, cursor })
      if (logsRequestSeq.current !== requestSeq) {
        return
      }
      setState((current) => ({
        page: cursor && current.page
          ? { ...page, items: [...current.page.items, ...page.items] }
          : page,
        loading: false,
        error: null,
      }))
    } catch (error) {
      if (logsRequestSeq.current !== requestSeq) {
        return
      }
      setState((current) => ({
        ...current,
        loading: false,
        error: error instanceof Error ? error : new Error("controller log request failed"),
      }))
    }
  }, [])

  const loadControllerStatus = useCallback(async (nodeId: number) => {
    const requestSeq = statusRequestSeq.current + 1
    statusRequestSeq.current = requestSeq
    setStatusState({ status: null, loading: true, error: null })
    try {
      const status = await getControllerRaftStatus(nodeId)
      if (statusRequestSeq.current !== requestSeq) {
        return
      }
      setStatusState({ status, loading: false, error: null })
    } catch (error) {
      if (statusRequestSeq.current !== requestSeq) {
        return
      }
      setStatusState({
        status: null,
        loading: false,
        error: error instanceof Error ? error : new Error("controller raft status request failed"),
      })
    }
  }, [])

  const handleNodeChange = useCallback((nodeId: number | null) => {
    logsRequestSeq.current += 1
    statusRequestSeq.current += 1
    setSelectedNodeId(nodeId)
    const nextParams = new URLSearchParams(searchParams)
    if (nodeId === null) {
      nextParams.delete("node_id")
    } else {
      nextParams.set("node_id", String(nodeId))
    }
    setSearchParams(nextParams, { replace: true })
    setState({ page: null, loading: false, error: null })
    setStatusState({ status: null, loading: false, error: null })
    setExpandedIndexes(new Set())
  }, [searchParams, setSearchParams])

  const openControllerCompactionDialog = useCallback(() => {
    setCompactionScope(selectedNodeId === null ? "all" : "node")
    setCompactionDialogOpen(true)
  }, [selectedNodeId])

  const handleControllerCompaction = useCallback(async (scope: ControllerCompactionScope) => {
    const targetNodeId = selectedNodeId
    let runCompaction: () => Promise<ManagerControllerRaftCompactResponse>
    if (scope === "node") {
      if (targetNodeId === null) {
        return
      }
      runCompaction = () => compactControllerRaftLogOnNode(targetNodeId)
    } else {
      runCompaction = compactControllerRaftLogs
    }
    setCompactionDialogOpen(false)
    setCompactionState({ result: null, loading: true, error: null })
    try {
      const result = await runCompaction()
      setCompactionState({ result, loading: false, error: null })
      if (selectedNodeId !== null) {
        void loadControllerLogs(selectedNodeId)
        void loadControllerStatus(selectedNodeId)
      }
    } catch (error) {
      setCompactionState({
        result: null,
        loading: false,
        error: error instanceof Error ? error : new Error("controller raft compaction request failed"),
      })
    }
  }, [loadControllerLogs, loadControllerStatus, selectedNodeId])

  useEffect(() => {
    let cancelled = false
    async function loadNodes() {
      try {
        const nextNodes = await getNodes()
        if (cancelled) {
          return
        }
        setNodes(nextNodes)
      } catch (error) {
        if (cancelled) {
          return
        }
        setNodes(null)
        setSelectedNodeId(null)
        setStatusState({ status: null, loading: false, error: null })
        setState({
          page: null,
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
    if (!nodes) {
      return
    }
    setSelectedNodeId((current) => {
      if (queryNodeId !== null && hasNode(nodes, queryNodeId)) {
        return queryNodeId
      }
      if (current !== null && hasNode(nodes, current)) {
        return current
      }
      return defaultNodeId(nodes)
    })
  }, [nodes, queryNodeId])

  useEffect(() => {
    if (selectedNodeId === null) {
      if (nodes && nodes.items.length === 0) {
        setState({ page: null, loading: false, error: null })
        setStatusState({ status: null, loading: false, error: null })
      }
      return
    }
    void loadControllerLogs(selectedNodeId)
    void loadControllerStatus(selectedNodeId)
  }, [loadControllerLogs, loadControllerStatus, nodes, selectedNodeId])

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

  const visibleStatus = statusState.status?.node_id === selectedNodeId ? statusState.status : null
  const visiblePage = state.page?.node_id === selectedNodeId ? state.page : null

  return (
    <>
      <div
        className="flex flex-col gap-3 border-b border-border pb-4 lg:flex-row lg:items-start lg:justify-between"
        data-testid="controller-workbench-toolbar"
      >
        <div className="space-y-2">
          <div className="font-mono text-[11px] font-semibold uppercase tracking-[0.22em] text-muted-foreground">
            {intl.formatMessage({ id: "controller.eyebrow" })}
          </div>
          <h2 className="text-xl font-semibold tracking-tight text-foreground">
            {intl.formatMessage({ id: "controller.title" })}
          </h2>
          <p className="max-w-3xl text-sm leading-6 text-muted-foreground">
            {intl.formatMessage({ id: "controller.description" })}
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <NodeFilter nodes={nodes} selectedNodeId={selectedNodeId} onNodeChange={handleNodeChange} />
          <Button
            disabled={compactionState.loading}
            onClick={openControllerCompactionDialog}
            size="sm"
            variant="outline"
          >
            {compactionState.loading ? intl.formatMessage({ id: "controller.compaction.triggering" }) : intl.formatMessage({ id: "controller.compaction.trigger" })}
          </Button>
          <Button
            disabled={state.loading || statusState.loading || selectedNodeId === null}
            onClick={() => {
              if (selectedNodeId !== null) {
                void loadControllerLogs(selectedNodeId)
                void loadControllerStatus(selectedNodeId)
              }
            }}
            size="sm"
            variant="outline"
          >
            {state.loading || statusState.loading ? intl.formatMessage({ id: "common.refreshing" }) : intl.formatMessage({ id: "common.refresh" })}
          </Button>
        </div>
      </div>

      {compactionState.error ? (
        <ResourceState
          kind={mapErrorKind(compactionState.error)}
          onRetry={openControllerCompactionDialog}
          retryLabel={intl.formatMessage({ id: "common.retry" })}
          title={intl.formatMessage({ id: "controller.compaction.title" })}
        />
      ) : compactionState.result ? (
        <ControllerCompactionResultPanel intl={intl} result={compactionState.result} />
      ) : null}

      {statusState.loading ? (
        <ResourceState kind="loading" title={intl.formatMessage({ id: "controller.status.title" })} />
      ) : statusState.error ? (
        <ResourceState
          kind={mapErrorKind(statusState.error)}
          onRetry={selectedNodeId !== null ? () => void loadControllerStatus(selectedNodeId) : undefined}
          retryLabel={intl.formatMessage({ id: "common.retry" })}
          title={intl.formatMessage({ id: "controller.status.title" })}
        />
      ) : visibleStatus ? (
        <ControllerRaftStatusPanel intl={intl} status={visibleStatus} />
      ) : null}

      {state.loading && !visiblePage ? (
        <ResourceState kind="loading" title={intl.formatMessage({ id: "controller.logs.title" })} />
      ) : state.error ? (
        <ResourceState
          kind={mapErrorKind(state.error)}
          onRetry={selectedNodeId !== null ? () => void loadControllerLogs(selectedNodeId) : undefined}
          retryLabel={intl.formatMessage({ id: "common.retry" })}
          title={intl.formatMessage({ id: "controller.logs.title" })}
        />
      ) : visiblePage && visiblePage.items.length > 0 ? (
        <section className="overflow-hidden rounded-lg border border-border bg-card" data-controller-surface="logs">
          <div className="flex flex-col gap-2 border-b border-border p-4 lg:flex-row lg:items-center lg:justify-between">
            <div>
              <h2 className="text-base font-semibold text-foreground">
                {intl.formatMessage({ id: "controller.logs.title" })}
              </h2>
              <p className="mt-1 text-sm text-muted-foreground">
                {intl.formatMessage(
                  { id: "controller.logs.watermark" },
                  { node: visiblePage.node_id, commit: visiblePage.commit_index, applied: visiblePage.applied_index },
                )}
              </p>
            </div>
            <div className="text-xs text-muted-foreground">
              {intl.formatMessage(
                { id: "controller.logs.range" },
                { first: visiblePage.first_index, last: visiblePage.last_index },
              )}
            </div>
          </div>
          <div className="overflow-x-auto">
            <table
              aria-label={intl.formatMessage({ id: "controller.logs.title" })}
              className="min-w-full divide-y divide-border text-sm"
            >
              <thead className="bg-muted/50 text-left text-xs uppercase tracking-[0.18em] text-muted-foreground">
                <tr>
                  <th className="px-3 py-2">{intl.formatMessage({ id: "controller.logs.table.index" })}</th>
                  <th className="px-3 py-2">{intl.formatMessage({ id: "controller.logs.table.createdAt" })}</th>
                  <th className="px-3 py-2">{intl.formatMessage({ id: "controller.logs.table.term" })}</th>
                  <th className="px-3 py-2">{intl.formatMessage({ id: "controller.logs.table.type" })}</th>
                  <th className="px-3 py-2">{intl.formatMessage({ id: "controller.logs.table.command" })}</th>
                  <th className="px-3 py-2">{intl.formatMessage({ id: "controller.logs.table.dataSize" })}</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border">
                {visiblePage.items.map((entry) => {
                  const decoded = formatControllerLogDecoded(entry)
                  const expanded = decoded ? expandedIndexes.has(entry.index) : false
                  return (
                    <tr key={entry.index} className="align-top">
                      <td className="px-3 py-3 font-medium text-foreground">{entry.index}</td>
                      <td className="px-3 py-3 text-muted-foreground">{formatLogEntryCreatedAt(intl, entry.created_at_ms)}</td>
                      <td className="px-3 py-3 text-muted-foreground">{entry.term}</td>
                      <td className="px-3 py-3 text-muted-foreground">{entry.type}</td>
                      <td className="px-3 py-3 text-foreground">
                        <div className="flex flex-col gap-2">
                          <span>{formatControllerLogCommand(intl, entry)}</span>
                          {decoded ? (
                            <Button onClick={() => toggleDecoded(entry.index)} size="sm" variant="ghost">
                              {intl.formatMessage({ id: expanded ? "controller.logs.table.hideDecoded" : "controller.logs.table.showDecoded" })}
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
                  )
                })}
              </tbody>
            </table>
          </div>
          {visiblePage.next_cursor ? (
            <div className="border-t border-border p-4">
              <Button
                disabled={state.loading}
                onClick={() => void loadControllerLogs(visiblePage.node_id, visiblePage.next_cursor)}
                variant="outline"
              >
                {intl.formatMessage({ id: "common.loadMore" })}
              </Button>
            </div>
          ) : null}
        </section>
      ) : (
        <ResourceState kind="empty" title={intl.formatMessage({ id: "controller.logs.title" })} />
      )}

      <ActionFormDialog
        description={intl.formatMessage({ id: "controller.compaction.dialog.description" })}
        onOpenChange={setCompactionDialogOpen}
        onSubmit={(event) => {
          event.preventDefault()
          void handleControllerCompaction(compactionScope)
        }}
        open={compactionDialogOpen}
        pending={compactionState.loading}
        submitLabel={intl.formatMessage({ id: "controller.compaction.trigger" })}
        title={intl.formatMessage({ id: "controller.compaction.dialog.title" })}
      >
        <fieldset className="space-y-3">
          <legend className="sr-only">{intl.formatMessage({ id: "controller.compaction.dialog.scope" })}</legend>
          <label className="flex cursor-pointer gap-3 rounded-lg border border-border bg-background p-3 text-sm">
            <input
              aria-label={intl.formatMessage({ id: "controller.compaction.scope.current" }, { node: selectedNodeId ?? "-" })}
              checked={compactionScope === "node"}
              className="mt-1"
              disabled={selectedNodeId === null}
              name="controller-compaction-scope"
              onChange={() => setCompactionScope("node")}
              type="radio"
              value="node"
            />
            <span>
              <span className="block font-medium text-foreground">
                {intl.formatMessage({ id: "controller.compaction.scope.current" }, { node: selectedNodeId ?? "-" })}
              </span>
              <span className="mt-1 block text-muted-foreground">
                {intl.formatMessage({ id: "controller.compaction.scope.currentDescription" })}
              </span>
            </span>
          </label>
          <label className="flex cursor-pointer gap-3 rounded-lg border border-border bg-background p-3 text-sm">
            <input
              aria-label={intl.formatMessage({ id: "controller.compaction.scope.all" })}
              checked={compactionScope === "all"}
              className="mt-1"
              name="controller-compaction-scope"
              onChange={() => setCompactionScope("all")}
              type="radio"
              value="all"
            />
            <span>
              <span className="block font-medium text-foreground">
                {intl.formatMessage({ id: "controller.compaction.scope.all" })}
              </span>
              <span className="mt-1 block text-muted-foreground">
                {intl.formatMessage({ id: "controller.compaction.scope.allDescription" })}
              </span>
            </span>
          </label>
        </fieldset>
      </ActionFormDialog>
    </>
  )
}

export function ControllerPage() {
  return (
    <PageContainer>
      <ControllerLogsPanel />
    </PageContainer>
  )
}
