import { useCallback, useEffect, useState } from "react"
import { useIntl } from "react-intl"
import { Link } from "react-router-dom"

import { ResourceState } from "@/components/manager/resource-state"
import { StatusBadge } from "@/components/manager/status-badge"
import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
import { SectionCard } from "@/components/shell/section-card"
import { Button } from "@/components/ui/button"
import { ManagerApiError, getChannelClusterUnhealthy } from "@/lib/manager-api"
import type { ManagerChannelClusterUnhealthyItem } from "@/lib/manager-api.types"

type ChannelClusterUnhealthyState = {
  items: ManagerChannelClusterUnhealthyItem[]
  hasMore: boolean
  nextCursor?: string
  loading: boolean
  refreshing: boolean
  loadingMore: boolean
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

function formatNodeList(nodeIds: number[]) {
  return nodeIds.length > 0 ? nodeIds.join(", ") : "-"
}

function channelInspectPath(channel: ManagerChannelClusterUnhealthyItem) {
  return `/channel-cluster/list?channel_id=${encodeURIComponent(channel.channel_id)}&channel_type=${channel.channel_type}`
}

function reasonMessageId(reason: string) {
  switch (reason) {
    case "isr_insufficient":
      return "channelCluster.unhealthy.reason.isrInsufficient"
    case "no_leader":
      return "channelCluster.unhealthy.reason.noLeader"
    case "status_not_active":
      return "channelCluster.unhealthy.reason.statusNotActive"
    default:
      return "channelCluster.unhealthy.reason.unknown"
  }
}

export function ChannelClusterUnhealthyPage() {
  const intl = useIntl()
  const [state, setState] = useState<ChannelClusterUnhealthyState>({
    items: [],
    hasMore: false,
    loading: true,
    refreshing: false,
    loadingMore: false,
    error: null,
  })

  const loadFirstPage = useCallback(async (refreshing: boolean) => {
    setState((current) => ({
      ...current,
      loading: refreshing ? current.loading : true,
      refreshing,
      loadingMore: false,
      error: null,
    }))

    try {
      const page = await getChannelClusterUnhealthy({})
      setState({
        items: page.items,
        hasMore: page.has_more,
        nextCursor: page.next_cursor,
        loading: false,
        refreshing: false,
        loadingMore: false,
        error: null,
      })
    } catch (error) {
      setState({
        items: [],
        hasMore: false,
        loading: false,
        refreshing: false,
        loadingMore: false,
        error: error instanceof Error ? error : new Error("channel cluster unhealthy request failed"),
      })
    }
  }, [])

  const loadMore = useCallback(async () => {
    if (!state.nextCursor) {
      return
    }

    setState((current) => ({
      ...current,
      loadingMore: true,
      error: null,
    }))

    try {
      const page = await getChannelClusterUnhealthy({ cursor: state.nextCursor })
      setState((current) => ({
        items: [...current.items, ...page.items],
        hasMore: page.has_more,
        nextCursor: page.next_cursor,
        loading: false,
        refreshing: false,
        loadingMore: false,
        error: null,
      }))
    } catch (error) {
      setState((current) => ({
        ...current,
        loadingMore: false,
        error: error instanceof Error ? error : new Error("channel cluster unhealthy request failed"),
      }))
    }
  }, [state.nextCursor])

  useEffect(() => {
    void loadFirstPage(false)
  }, [loadFirstPage])

  return (
    <PageContainer>
      <PageHeader
        title={intl.formatMessage({ id: "channelCluster.unhealthy.title" })}
        description={intl.formatMessage({ id: "channelCluster.unhealthy.description" })}
        actions={
          <Button
            onClick={() => {
              void loadFirstPage(true)
            }}
            size="sm"
            variant="outline"
          >
            {state.refreshing
              ? intl.formatMessage({ id: "common.refreshing" })
              : intl.formatMessage({ id: "common.refresh" })}
          </Button>
        }
      />
      {state.loading ? (
        <ResourceState kind="loading" title={intl.formatMessage({ id: "channelCluster.unhealthy.title" })} />
      ) : null}
      {!state.loading && state.error ? (
        <ResourceState
          kind={mapErrorKind(state.error)}
          onRetry={() => {
            void loadFirstPage(false)
          }}
          title={intl.formatMessage({ id: "channelCluster.unhealthy.title" })}
        />
      ) : null}
      {!state.loading && !state.error ? (
        <SectionCard
          description={intl.formatMessage(
            { id: "channelCluster.unhealthy.loadedValue" },
            { count: state.items.length },
          )}
          title={intl.formatMessage({ id: "channelCluster.unhealthy.tableTitle" })}
        >
          {state.items.length > 0 ? (
            <>
              <div className="overflow-x-auto rounded-lg border border-border">
                <table className="w-full border-collapse">
                  <thead className="bg-muted/40 text-left text-xs uppercase tracking-[0.14em] text-muted-foreground">
                    <tr>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "channels.table.channelId" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "channels.table.type" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "channels.table.slot" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "channels.table.leader" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "channels.table.replicas" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "channelCluster.unhealthy.table.isr" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "channels.table.maxMessageSeq" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "channels.table.status" })}</th>
                      <th className="px-3 py-3">
                        {intl.formatMessage({ id: "channelCluster.unhealthy.table.reasons" })}
                      </th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "channels.table.actions" })}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {state.items.map((channel) => (
                      <tr className="border-t border-border" key={`${channel.channel_type}-${channel.channel_id}`}>
                        <td className="px-3 py-3 text-sm font-medium text-foreground">{channel.channel_id}</td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">{channel.channel_type}</td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">{channel.slot_id}</td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">{channel.leader}</td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">{formatNodeList(channel.replicas)}</td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">
                          {intl.formatMessage(
                            { id: "channelCluster.unhealthy.isrValue" },
                            { isr: formatNodeList(channel.isr), min: channel.min_isr },
                          )}
                        </td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">{channel.max_message_seq}</td>
                        <td className="px-3 py-3 text-sm text-foreground">
                          <StatusBadge value={channel.status} />
                        </td>
                        <td className="px-3 py-3 text-sm text-foreground">
                          <div className="flex flex-wrap gap-2">
                            {channel.reasons.map((reason) => (
                              <span
                                className="rounded-full border border-border bg-muted/40 px-2 py-1 text-xs text-foreground"
                                key={reason}
                              >
                                {intl.formatMessage({ id: reasonMessageId(reason) }, { reason })}
                              </span>
                            ))}
                          </div>
                        </td>
                        <td className="px-3 py-3 text-sm text-foreground">
                          <Button asChild size="sm" variant="outline">
                            <Link
                              aria-label={intl.formatMessage(
                                { id: "channels.inspectChannel" },
                                { id: channel.channel_id },
                              )}
                              to={channelInspectPath(channel)}
                            >
                              {intl.formatMessage({ id: "common.inspect" })}
                            </Link>
                          </Button>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
              {state.hasMore ? (
                <div className="mt-3 flex justify-end">
                  <Button
                    onClick={() => {
                      void loadMore()
                    }}
                    size="sm"
                    variant="outline"
                  >
                    {state.loadingMore
                      ? intl.formatMessage({ id: "common.loading" })
                      : intl.formatMessage({ id: "common.loadMore" })}
                  </Button>
                </div>
              ) : null}
            </>
          ) : (
            <ResourceState
              description={intl.formatMessage({ id: "channelCluster.unhealthy.emptyDescription" })}
              kind="empty"
              title={intl.formatMessage({ id: "channelCluster.unhealthy.emptyTitle" })}
            />
          )}
        </SectionCard>
      ) : null}
    </PageContainer>
  )
}
