import { useCallback, useEffect, useMemo, useState } from "react"
import { useIntl } from "react-intl"

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
  getChannelRuntimeMeta,
  getChannelRuntimeMetaDetail,
} from "@/lib/manager-api"
import type {
  ManagerChannelRuntimeMetaDetailResponse,
  ManagerChannelRuntimeMetaListResponse,
} from "@/lib/manager-api.types"

type ChannelsState = {
  channels: ManagerChannelRuntimeMetaListResponse | null
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

export function ChannelsPage() {
  const intl = useIntl()
  const [state, setState] = useState<ChannelsState>({
    channels: null,
    loading: true,
    refreshing: false,
    loadingMore: false,
    error: null,
  })
  const [selectedChannel, setSelectedChannel] = useState<{
    channelId: string
    channelType: number
  } | null>(null)
  const [detail, setDetail] = useState<ManagerChannelRuntimeMetaDetailResponse | null>(null)
  const [detailLoading, setDetailLoading] = useState(false)
  const [detailError, setDetailError] = useState<Error | null>(null)

  const loadChannels = useCallback(async (refreshing: boolean) => {
    setState((current) => ({
      ...current,
      loading: refreshing ? current.loading : true,
      refreshing,
      error: null,
    }))

    try {
      const channels = await getChannelRuntimeMeta()
      setState({
        channels,
        loading: false,
        refreshing: false,
        loadingMore: false,
        error: null,
      })
    } catch (error) {
      setState({
        channels: null,
        loading: false,
        refreshing: false,
        loadingMore: false,
        error: error instanceof Error ? error : new Error("channel runtime request failed"),
      })
    }
  }, [])

  const loadMoreChannels = useCallback(async () => {
    const cursor = state.channels?.next_cursor
    if (!cursor) {
      return
    }

    setState((current) => ({
      ...current,
      loadingMore: true,
    }))

    try {
      const nextPage = await getChannelRuntimeMeta({ cursor })
      setState((current) => ({
        channels: current.channels
          ? {
              items: [...current.channels.items, ...nextPage.items],
              has_more: nextPage.has_more,
              next_cursor: nextPage.next_cursor,
            }
          : nextPage,
        loading: false,
        refreshing: false,
        loadingMore: false,
        error: null,
      }))
    } catch (error) {
      setState((current) => ({
        ...current,
        loadingMore: false,
        error: error instanceof Error ? error : new Error("channel runtime request failed"),
      }))
    }
  }, [state.channels?.next_cursor])

  const loadChannelDetail = useCallback(async (channelType: number, channelId: string) => {
    setDetailLoading(true)
    setDetailError(null)

    try {
      const nextDetail = await getChannelRuntimeMetaDetail(channelType, channelId)
      setDetail(nextDetail)
    } catch (error) {
      setDetail(null)
      setDetailError(error instanceof Error ? error : new Error("channel runtime detail failed"))
    } finally {
      setDetailLoading(false)
    }
  }, [])

  useEffect(() => {
    void loadChannels(false)
  }, [loadChannels])

  const openDetail = useCallback(
    async (channelType: number, channelId: string) => {
      setSelectedChannel({ channelId, channelType })
      await loadChannelDetail(channelType, channelId)
    },
    [loadChannelDetail],
  )

  const closeDetail = useCallback((open: boolean) => {
    if (open) {
      return
    }
    setSelectedChannel(null)
    setDetail(null)
    setDetailError(null)
  }, [])

  const summary = useMemo(() => {
    const items = state.channels?.items ?? []
    return {
      total: items.length,
      active: items.filter((item) => item.status === "active").length,
      slots: new Set(items.map((item) => item.slot_id)).size,
      leaders: new Set(items.map((item) => item.leader)).size,
    }
  }, [state.channels])

  return (
    <PageContainer>
      <PageHeader
        title={intl.formatMessage({ id: "nav.channels.title" })}
        description={intl.formatMessage({ id: "nav.channels.description" })}
        actions={
          <Button
            onClick={() => {
              void loadChannels(true)
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
            {intl.formatMessage({ id: "channels.scopeAllChannels" })}
          </div>
          <div className="rounded-md border border-border bg-background px-3 py-2">
            {state.channels
              ? intl.formatMessage({ id: "channels.loadedValue" }, { count: state.channels.items.length })
              : intl.formatMessage({ id: "channels.loadedPending" })}
          </div>
        </div>
      </PageHeader>

      {state.loading ? <ResourceState kind="loading" title={intl.formatMessage({ id: "nav.channels.title" })} /> : null}
      {!state.loading && state.error ? (
        <ResourceState
          kind={mapErrorKind(state.error)}
          onRetry={() => {
            void loadChannels(false)
          }}
          title={intl.formatMessage({ id: "nav.channels.title" })}
        />
      ) : null}
      {!state.loading && !state.error && state.channels ? (
        <>
          <section className="grid gap-3 md:grid-cols-2 xl:grid-cols-4">
            <SectionCard
              description={intl.formatMessage({ id: "channels.cards.loaded.description" })}
              title={intl.formatMessage({ id: "channels.cards.loaded.title" })}
            >
              <div className="text-3xl font-semibold text-foreground">{summary.total}</div>
            </SectionCard>
            <SectionCard
              description={intl.formatMessage({ id: "channels.cards.active.description" })}
              title={intl.formatMessage({ id: "channels.cards.active.title" })}
            >
              <div className="text-3xl font-semibold text-foreground">{summary.active}</div>
            </SectionCard>
            <SectionCard
              description={intl.formatMessage({ id: "channels.cards.slots.description" })}
              title={intl.formatMessage({ id: "channels.cards.slots.title" })}
            >
              <div className="text-3xl font-semibold text-foreground">{summary.slots}</div>
            </SectionCard>
            <SectionCard
              description={intl.formatMessage({ id: "channels.cards.leaders.description" })}
              title={intl.formatMessage({ id: "channels.cards.leaders.title" })}
            >
              <div className="text-3xl font-semibold text-foreground">{summary.leaders}</div>
            </SectionCard>
          </section>

          <SectionCard
            description={intl.formatMessage({ id: "channels.runtimeDescription" })}
            title={intl.formatMessage({ id: "channels.runtimeTitle" })}
          >
            <TableToolbar
              actions={
                state.channels.has_more ? (
                  <Button
                    onClick={() => {
                      void loadMoreChannels()
                    }}
                    size="sm"
                    variant="outline"
                  >
                    {state.loadingMore
                      ? intl.formatMessage({ id: "common.loading" })
                      : intl.formatMessage({ id: "common.loadMore" })}
                  </Button>
                ) : null
              }
              description={intl.formatMessage({ id: "channels.toolbarDescription" })}
              onRefresh={() => {
                void loadChannels(true)
              }}
              refreshing={state.refreshing}
              title={intl.formatMessage({ id: "channels.toolbarTitle" })}
            />
            {state.channels.items.length > 0 ? (
              <div className="overflow-x-auto rounded-lg border border-border">
                <table className="w-full border-collapse">
                  <thead className="bg-muted/40 text-left text-xs uppercase tracking-[0.14em] text-muted-foreground">
                    <tr>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "channels.table.channelId" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "channels.table.type" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "channels.table.slot" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "channels.table.leader" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "channels.table.status" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "channels.table.actions" })}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {state.channels.items.map((channel) => (
                      <tr className="border-t border-border" key={`${channel.channel_type}-${channel.channel_id}`}>
                        <td className="px-3 py-3 text-sm font-medium text-foreground">{channel.channel_id}</td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">{channel.channel_type}</td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">{channel.slot_id}</td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">{channel.leader}</td>
                        <td className="px-3 py-3 text-sm text-foreground">
                          <StatusBadge value={channel.status} />
                        </td>
                        <td className="px-3 py-3 text-sm text-foreground">
                          <Button
                            aria-label={intl.formatMessage(
                              { id: "channels.inspectChannel" },
                              { id: channel.channel_id },
                            )}
                            onClick={() => {
                              void openDetail(channel.channel_type, channel.channel_id)
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
              <ResourceState kind="empty" title={intl.formatMessage({ id: "channels.runtimeTitle" })} />
            )}
          </SectionCard>
        </>
      ) : null}

      <DetailSheet
        description={
          detail
            ? intl.formatMessage({ id: "channels.detailDescriptionValue" }, { id: detail.slot_id })
            : intl.formatMessage({ id: "channels.detailDescriptionFallback" })
        }
        onOpenChange={closeDetail}
        open={selectedChannel !== null}
        title={
          detail
            ? intl.formatMessage({ id: "channels.detailTitleValue" }, { id: detail.channel_id })
            : intl.formatMessage({ id: "channels.detailTitleFallback" })
        }
      >
        {detailLoading ? (
          <ResourceState kind="loading" title={intl.formatMessage({ id: "channels.detailTitleFallback" })} />
        ) : null}
        {!detailLoading && detailError ? (
          <ResourceState
            kind={mapErrorKind(detailError)}
            onRetry={() => {
              if (selectedChannel) {
                void loadChannelDetail(selectedChannel.channelType, selectedChannel.channelId)
              }
            }}
            title={intl.formatMessage({ id: "channels.detailTitleFallback" })}
          />
        ) : null}
        {!detailLoading && !detailError && detail ? (
          <KeyValueList
            items={[
              { label: intl.formatMessage({ id: "channels.detail.channelId" }), value: detail.channel_id },
              { label: intl.formatMessage({ id: "channels.detail.channelType" }), value: detail.channel_type },
              { label: intl.formatMessage({ id: "channels.detail.slotId" }), value: detail.slot_id },
              { label: intl.formatMessage({ id: "channels.detail.hashSlot" }), value: detail.hash_slot },
              {
                label: intl.formatMessage({ id: "channels.detail.status" }),
                value: <StatusBadge value={detail.status} />,
              },
              { label: intl.formatMessage({ id: "channels.detail.leader" }), value: detail.leader },
              {
                label: intl.formatMessage({ id: "channels.detail.replicas" }),
                value: formatNodeList(detail.replicas),
              },
              { label: intl.formatMessage({ id: "channels.detail.isr" }), value: formatNodeList(detail.isr) },
              { label: intl.formatMessage({ id: "channels.detail.minIsr" }), value: detail.min_isr },
              {
                label: intl.formatMessage({ id: "channels.detail.channelEpoch" }),
                value: detail.channel_epoch,
              },
              {
                label: intl.formatMessage({ id: "channels.detail.leaderEpoch" }),
                value: detail.leader_epoch,
              },
              { label: intl.formatMessage({ id: "channels.detail.features" }), value: detail.features },
              {
                label: intl.formatMessage({ id: "channels.detail.leaseUntilMs" }),
                value: detail.lease_until_ms,
              },
            ]}
          />
        ) : null}
      </DetailSheet>
    </PageContainer>
  )
}
