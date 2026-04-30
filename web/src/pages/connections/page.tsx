import { useCallback, useEffect, useState } from "react"
import { useIntl, type IntlShape } from "react-intl"

import { DetailSheet } from "@/components/manager/detail-sheet"
import { KeyValueList } from "@/components/manager/key-value-list"
import { NodeFilter, defaultNodeId, hasNode } from "@/components/manager/node-filter"
import { ResourceState } from "@/components/manager/resource-state"
import { StatusBadge } from "@/components/manager/status-badge"
import { Button } from "@/components/ui/button"
import { PageContainer } from "@/components/shell/page-container"
import { ManagerApiError, getConnection, getConnections, getNodes } from "@/lib/manager-api"
import type {
  ManagerConnectionDetailResponse,
  ManagerConnectionsResponse,
  ManagerNodesResponse,
} from "@/lib/manager-api.types"

type ConnectionsState = {
  connections: ManagerConnectionsResponse | null
  loading: boolean
  refreshing: boolean
  error: Error | null
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

function formatDevice(value: { device_flag: string; device_level: string }) {
  return `${value.device_flag} / ${value.device_level}`
}

export function ConnectionsPage() {
  const intl = useIntl()
  const [state, setState] = useState<ConnectionsState>({
    connections: null,
    loading: true,
    refreshing: false,
    error: null,
  })
  const [nodes, setNodes] = useState<ManagerNodesResponse | null>(null)
  const [selectedNodeId, setSelectedNodeId] = useState<number | null>(null)
  const [selectedSessionId, setSelectedSessionId] = useState<number | null>(null)
  const [detail, setDetail] = useState<ManagerConnectionDetailResponse | null>(null)
  const [detailLoading, setDetailLoading] = useState(false)
  const [detailError, setDetailError] = useState<Error | null>(null)

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
        setState({ connections: null, loading: false, refreshing: false, error: null })
      }
    } catch (error) {
      setNodes(null)
      setSelectedNodeId(null)
      setState({
        connections: null,
        loading: false,
        refreshing: false,
        error: error instanceof Error ? error : new Error("node request failed"),
      })
    }
  }, [])

  const loadConnections = useCallback(async (refreshing: boolean, nodeId: number | null) => {
    if (!nodeId) {
      setState({ connections: null, loading: false, refreshing: false, error: null })
      return
    }

    setState((current) => ({
      ...current,
      loading: refreshing ? current.loading : true,
      refreshing,
      error: null,
    }))

    try {
      const connections = await getConnections({ nodeId })
      setState({ connections, loading: false, refreshing: false, error: null })
    } catch (error) {
      setState({
        connections: null,
        loading: false,
        refreshing: false,
        error: error instanceof Error ? error : new Error("connection request failed"),
      })
    }
  }, [])

  const loadConnectionDetail = useCallback(async (sessionId: number) => {
    setDetailLoading(true)
    setDetailError(null)

    try {
      const nextDetail = await getConnection(sessionId)
      setDetail(nextDetail)
    } catch (error) {
      setDetail(null)
      setDetailError(error instanceof Error ? error : new Error("connection detail failed"))
    } finally {
      setDetailLoading(false)
    }
  }, [])

  useEffect(() => {
    void loadNodes()
  }, [loadNodes])

  useEffect(() => {
    if (selectedNodeId !== null) {
      void loadConnections(false, selectedNodeId)
    }
  }, [loadConnections, selectedNodeId])

  const openDetail = useCallback(
    async (sessionId: number) => {
      setSelectedSessionId(sessionId)
      await loadConnectionDetail(sessionId)
    },
    [loadConnectionDetail],
  )

  const closeDetail = useCallback((open: boolean) => {
    if (open) {
      return
    }
    setSelectedSessionId(null)
    setDetail(null)
    setDetailError(null)
  }, [])

  return (
    <PageContainer>
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h1 className="text-xl font-semibold tracking-tight text-foreground">
            {intl.formatMessage({ id: "nav.connections.title" })}
          </h1>
          <p className="mt-1 text-sm text-muted-foreground">
            {state.connections
              ? intl.formatMessage({ id: "connections.totalValue" }, { total: state.connections.total })
              : intl.formatMessage({ id: "connections.totalPending" })}
          </p>
        </div>
        <div className="flex flex-wrap gap-2">
          <NodeFilter nodes={nodes} onNodeChange={setSelectedNodeId} selectedNodeId={selectedNodeId} />
          <Button
            onClick={() => {
              void loadConnections(true, selectedNodeId)
            }}
            size="sm"
            variant="outline"
          >
            {state.refreshing
              ? intl.formatMessage({ id: "common.refreshing" })
              : intl.formatMessage({ id: "common.refresh" })}
          </Button>
        </div>
      </div>

      {state.loading ? <ResourceState kind="loading" title={intl.formatMessage({ id: "nav.connections.title" })} /> : null}
      {!state.loading && state.error ? (
        <ResourceState
          kind={mapErrorKind(state.error)}
          onRetry={() => {
            void loadConnections(false, selectedNodeId)
          }}
          title={intl.formatMessage({ id: "nav.connections.title" })}
        />
      ) : null}
      {!state.loading && !state.error && state.connections ? (
        <div className="rounded-xl border border-border bg-card p-3 shadow-none">
          {state.connections.items.length > 0 ? (
            <div className="overflow-x-auto rounded-lg border border-border">
              <table className="w-full border-collapse">
                <thead className="bg-muted/40 text-left text-xs uppercase tracking-[0.14em] text-muted-foreground">
                  <tr>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "connections.table.session" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "connections.table.uid" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "connections.table.deviceId" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "connections.table.device" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "connections.table.listener" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "connections.table.state" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "connections.table.connectedAt" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "connections.table.actions" })}</th>
                  </tr>
                </thead>
                <tbody>
                  {state.connections.items.map((connection) => (
                    <tr className="border-t border-border" key={connection.session_id}>
                      <td className="px-3 py-3 text-sm font-medium text-foreground">
                        {connection.session_id}
                      </td>
                      <td className="px-3 py-3 text-sm text-foreground">{connection.uid}</td>
                      <td className="px-3 py-3 text-sm text-muted-foreground">
                        {connection.device_id}
                      </td>
                      <td className="px-3 py-3 text-sm text-muted-foreground">
                        {formatDevice(connection)}
                      </td>
                      <td className="px-3 py-3 text-sm text-muted-foreground">
                        {connection.listener}
                      </td>
                      <td className="px-3 py-3 text-sm text-foreground">
                        <StatusBadge value={connection.state} />
                      </td>
                      <td className="px-3 py-3 text-sm text-muted-foreground">
                        {formatTimestamp(intl, connection.connected_at)}
                      </td>
                      <td className="px-3 py-3 text-sm text-foreground">
                        <Button
                          aria-label={intl.formatMessage(
                            { id: "connections.inspectConnection" },
                            { id: connection.session_id },
                          )}
                          onClick={() => {
                            void openDetail(connection.session_id)
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
            <ResourceState kind="empty" title={intl.formatMessage({ id: "nav.connections.title" })} />
          )}
        </div>
      ) : null}

      <DetailSheet
        description={
          detail
            ? intl.formatMessage({ id: "connections.detailDescriptionValue" }, { uid: detail.uid })
            : intl.formatMessage({ id: "connections.detailDescriptionFallback" })
        }
        onOpenChange={closeDetail}
        open={selectedSessionId !== null}
        title={
          detail
            ? intl.formatMessage({ id: "connections.detailTitleValue" }, { id: detail.session_id })
            : intl.formatMessage({ id: "connections.detailTitleFallback" })
        }
      >
        {detailLoading ? (
          <ResourceState kind="loading" title={intl.formatMessage({ id: "connections.detailTitleFallback" })} />
        ) : null}
        {!detailLoading && detailError ? (
          <ResourceState
            kind={mapErrorKind(detailError)}
            onRetry={() => {
              if (selectedSessionId !== null) {
                void loadConnectionDetail(selectedSessionId)
              }
            }}
            title={intl.formatMessage({ id: "connections.detailTitleFallback" })}
          />
        ) : null}
        {!detailLoading && !detailError && detail ? (
          <KeyValueList
            items={[
              { label: intl.formatMessage({ id: "connections.detail.sessionId" }), value: detail.session_id },
              { label: intl.formatMessage({ id: "connections.detail.uid" }), value: detail.uid },
              { label: intl.formatMessage({ id: "connections.detail.deviceId" }), value: detail.device_id },
              {
                label: intl.formatMessage({ id: "connections.detail.deviceFlag" }),
                value: detail.device_flag,
              },
              {
                label: intl.formatMessage({ id: "connections.detail.deviceLevel" }),
                value: detail.device_level,
              },
              { label: intl.formatMessage({ id: "connections.detail.slotId" }), value: detail.slot_id },
              { label: intl.formatMessage({ id: "connections.detail.listener" }), value: detail.listener },
              {
                label: intl.formatMessage({ id: "connections.detail.state" }),
                value: <StatusBadge value={detail.state} />,
              },
              {
                label: intl.formatMessage({ id: "connections.detail.connectedAt" }),
                value: formatTimestamp(intl, detail.connected_at),
              },
              {
                label: intl.formatMessage({ id: "connections.detail.remoteAddress" }),
                value: detail.remote_addr || "-",
              },
              {
                label: intl.formatMessage({ id: "connections.detail.localAddress" }),
                value: detail.local_addr || "-",
              },
            ]}
          />
        ) : null}
      </DetailSheet>
    </PageContainer>
  )
}
