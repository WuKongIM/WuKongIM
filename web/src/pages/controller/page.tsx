import { useCallback, useEffect, useState } from "react"
import { useIntl, type IntlShape } from "react-intl"

import { NodeFilter, defaultNodeId, hasNode } from "@/components/manager/node-filter"
import { ResourceState } from "@/components/manager/resource-state"
import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
import { Button } from "@/components/ui/button"
import { getControllerLogs, getNodes, ManagerApiError } from "@/lib/manager-api"
import type {
  ManagerControllerLogEntry,
  ManagerControllerLogsResponse,
  ManagerNodesResponse,
} from "@/lib/manager-api.types"

const controllerLogPageLimit = 50

type ControllerLogsState = {
  page: ManagerControllerLogsResponse | null
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

function formatDataSize(value: number) {
  return `${value} B`
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

export function ControllerPage() {
  const intl = useIntl()
  const [nodes, setNodes] = useState<ManagerNodesResponse | null>(null)
  const [selectedNodeId, setSelectedNodeId] = useState<number | null>(null)
  const [state, setState] = useState<ControllerLogsState>({ page: null, loading: true, error: null })
  const [expandedIndexes, setExpandedIndexes] = useState<Set<number>>(() => new Set())

  const loadControllerLogs = useCallback(async (nodeId: number, cursor?: number) => {
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
        error: error instanceof Error ? error : new Error("controller log request failed"),
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
    if (selectedNodeId === null) {
      if (nodes && nodes.items.length === 0) {
        setState({ page: null, loading: false, error: null })
      }
      return
    }
    void loadControllerLogs(selectedNodeId)
  }, [loadControllerLogs, nodes, selectedNodeId])

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

  return (
    <PageContainer>
      <PageHeader
        actions={(
          <div className="flex flex-wrap items-center gap-2">
            <NodeFilter nodes={nodes} selectedNodeId={selectedNodeId} onNodeChange={setSelectedNodeId} />
            <Button
              disabled={state.loading || selectedNodeId === null}
              onClick={() => {
                if (selectedNodeId !== null) {
                  void loadControllerLogs(selectedNodeId)
                }
              }}
              size="sm"
              variant="outline"
            >
              {state.loading ? intl.formatMessage({ id: "common.refreshing" }) : intl.formatMessage({ id: "common.refresh" })}
            </Button>
          </div>
        )}
        description={intl.formatMessage({ id: "controller.description" })}
        eyebrow={intl.formatMessage({ id: "controller.eyebrow" })}
        title={intl.formatMessage({ id: "controller.title" })}
      />

      {state.loading && !state.page ? (
        <ResourceState kind="loading" title={intl.formatMessage({ id: "controller.logs.title" })} />
      ) : state.error ? (
        <ResourceState
          kind={mapErrorKind(state.error)}
          onRetry={selectedNodeId !== null ? () => void loadControllerLogs(selectedNodeId) : undefined}
          retryLabel={intl.formatMessage({ id: "common.retry" })}
          title={intl.formatMessage({ id: "controller.logs.title" })}
        />
      ) : state.page && state.page.items.length > 0 ? (
        <section className="overflow-hidden rounded-lg border border-border bg-card">
          <div className="flex flex-col gap-2 border-b border-border p-4 lg:flex-row lg:items-center lg:justify-between">
            <div>
              <h2 className="text-base font-semibold text-foreground">
                {intl.formatMessage({ id: "controller.logs.title" })}
              </h2>
              <p className="mt-1 text-sm text-muted-foreground">
                {intl.formatMessage(
                  { id: "controller.logs.watermark" },
                  { node: state.page.node_id, commit: state.page.commit_index, applied: state.page.applied_index },
                )}
              </p>
            </div>
            <div className="text-xs text-muted-foreground">
              {intl.formatMessage(
                { id: "controller.logs.range" },
                { first: state.page.first_index, last: state.page.last_index },
              )}
            </div>
          </div>
          <div className="overflow-x-auto">
            <table className="min-w-full divide-y divide-border text-sm">
              <thead className="bg-muted/50 text-left text-xs uppercase tracking-[0.18em] text-muted-foreground">
                <tr>
                  <th className="px-3 py-2">{intl.formatMessage({ id: "controller.logs.table.index" })}</th>
                  <th className="px-3 py-2">{intl.formatMessage({ id: "controller.logs.table.term" })}</th>
                  <th className="px-3 py-2">{intl.formatMessage({ id: "controller.logs.table.type" })}</th>
                  <th className="px-3 py-2">{intl.formatMessage({ id: "controller.logs.table.command" })}</th>
                  <th className="px-3 py-2">{intl.formatMessage({ id: "controller.logs.table.dataSize" })}</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border">
                {state.page.items.map((entry) => {
                  const decoded = formatControllerLogDecoded(entry)
                  const expanded = decoded ? expandedIndexes.has(entry.index) : false
                  return (
                    <tr key={entry.index} className="align-top">
                      <td className="px-3 py-3 font-medium text-foreground">{entry.index}</td>
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
          {state.page.next_cursor ? (
            <div className="border-t border-border p-4">
              <Button
                disabled={state.loading}
                onClick={() => void loadControllerLogs(state.page!.node_id, state.page!.next_cursor)}
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
    </PageContainer>
  )
}
