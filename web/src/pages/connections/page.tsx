import { useCallback, useEffect, useMemo, useState } from "react"
import { useIntl, type IntlShape } from "react-intl"

import { DetailSheet } from "@/components/manager/detail-sheet"
import { KeyValueList } from "@/components/manager/key-value-list"
import { ResourceState } from "@/components/manager/resource-state"
import { StatusBadge } from "@/components/manager/status-badge"
import { TableToolbar } from "@/components/manager/table-toolbar"
import { Button } from "@/components/ui/button"
import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
import { SectionCard } from "@/components/shell/section-card"
import { ManagerApiError, getConnection, getConnections } from "@/lib/manager-api"
import type {
  ManagerConnectionDetailResponse,
  ManagerConnectionsResponse,
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
  const [selectedSessionId, setSelectedSessionId] = useState<number | null>(null)
  const [detail, setDetail] = useState<ManagerConnectionDetailResponse | null>(null)
  const [detailLoading, setDetailLoading] = useState(false)
  const [detailError, setDetailError] = useState<Error | null>(null)

  const loadConnections = useCallback(async (refreshing: boolean) => {
    setState((current) => ({
      ...current,
      loading: refreshing ? current.loading : true,
      refreshing,
      error: null,
    }))

    try {
      const connections = await getConnections()
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
    void loadConnections(false)
  }, [loadConnections])

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

  const summary = useMemo(() => {
    const items = state.connections?.items ?? []
    return {
      total: items.length,
      users: new Set(items.map((item) => item.uid)).size,
      slots: new Set(items.map((item) => item.slot_id)).size,
      listeners: new Set(items.map((item) => item.listener)).size,
    }
  }, [state.connections])

  return (
    <PageContainer>
      <PageHeader
        title={intl.formatMessage({ id: "nav.connections.title" })}
        description={intl.formatMessage({ id: "nav.connections.description" })}
        actions={
          <Button
            onClick={() => {
              void loadConnections(true)
            }}
            size="sm"
            variant="outline"
          >
            {state.refreshing
              ? intl.formatMessage({ id: "common.refreshing" })
              : intl.formatMessage({ id: "common.refresh" })}
          </Button>
        }
      >
        <div className="flex flex-wrap gap-2 text-xs text-muted-foreground">
          <div className="rounded-md border border-border bg-background px-3 py-2">
            {intl.formatMessage({ id: "connections.scopeLocalNode" })}
          </div>
          <div className="rounded-md border border-border bg-background px-3 py-2">
            {state.connections
              ? intl.formatMessage({ id: "connections.totalValue" }, { total: state.connections.total })
              : intl.formatMessage({ id: "connections.totalPending" })}
          </div>
        </div>
      </PageHeader>

      {state.loading ? <ResourceState kind="loading" title={intl.formatMessage({ id: "nav.connections.title" })} /> : null}
      {!state.loading && state.error ? (
        <ResourceState
          kind={mapErrorKind(state.error)}
          onRetry={() => {
            void loadConnections(false)
          }}
          title={intl.formatMessage({ id: "nav.connections.title" })}
        />
      ) : null}
      {!state.loading && !state.error && state.connections ? (
        <>
          <section className="grid gap-3 md:grid-cols-2 xl:grid-cols-4">
            <SectionCard
              description={intl.formatMessage({ id: "connections.cards.sessions.description" })}
              title={intl.formatMessage({ id: "connections.cards.sessions.title" })}
            >
              <div className="text-3xl font-semibold text-foreground">{summary.total}</div>
            </SectionCard>
            <SectionCard
              description={intl.formatMessage({ id: "connections.cards.users.description" })}
              title={intl.formatMessage({ id: "connections.cards.users.title" })}
            >
              <div className="text-3xl font-semibold text-foreground">{summary.users}</div>
            </SectionCard>
            <SectionCard
              description={intl.formatMessage({ id: "connections.cards.slots.description" })}
              title={intl.formatMessage({ id: "connections.cards.slots.title" })}
            >
              <div className="text-3xl font-semibold text-foreground">{summary.slots}</div>
            </SectionCard>
            <SectionCard
              description={intl.formatMessage({ id: "connections.cards.listeners.description" })}
              title={intl.formatMessage({ id: "connections.cards.listeners.title" })}
            >
              <div className="text-3xl font-semibold text-foreground">{summary.listeners}</div>
            </SectionCard>
          </section>

          <SectionCard
            description={intl.formatMessage({ id: "connections.inventoryDescription" })}
            title={intl.formatMessage({ id: "connections.inventoryTitle" })}
          >
            <TableToolbar
              description={intl.formatMessage({ id: "connections.toolbarDescription" })}
              onRefresh={() => {
                void loadConnections(true)
              }}
              refreshing={state.refreshing}
              title={intl.formatMessage({ id: "connections.toolbarTitle" })}
            />
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
              <ResourceState kind="empty" title={intl.formatMessage({ id: "connections.inventoryTitle" })} />
            )}
          </SectionCard>
        </>
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
