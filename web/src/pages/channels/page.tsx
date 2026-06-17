import { useCallback, useEffect, useState } from "react"
import type { FormEvent } from "react"
import type { IntlShape } from "react-intl"
import { useIntl } from "react-intl"
import { useNavigate } from "react-router-dom"

import { NodeFilter, hasNode } from "@/components/manager/node-filter"
import { ResourceState } from "@/components/manager/resource-state"
import { StatusBadge } from "@/components/manager/status-badge"
import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
import { Button } from "@/components/ui/button"
import { ManagerApiError, getChannelRuntimeMeta, getNodes } from "@/lib/manager-api"
import type {
  ChannelRuntimeMetaListParams,
  ManagerChannelRuntimeMetaListResponse,
  ManagerNodesResponse,
} from "@/lib/manager-api.types"

const channelPageLimit = 50

const channelTypeOptions = [
  { value: 1, labelId: "channelsBiz.type.person" },
  { value: 2, labelId: "channelsBiz.type.group" },
  { value: 3, labelId: "channelsBiz.type.customerService" },
  { value: 4, labelId: "channelsBiz.type.community" },
  { value: 5, labelId: "channelsBiz.type.communityTopic" },
  { value: 6, labelId: "channelsBiz.type.info" },
  { value: 7, labelId: "channelsBiz.type.data" },
  { value: 8, labelId: "channelsBiz.type.temp" },
  { value: 9, labelId: "channelsBiz.type.live" },
  { value: 10, labelId: "channelsBiz.type.visitors" },
  { value: 11, labelId: "channelsBiz.type.agent" },
  { value: 12, labelId: "channelsBiz.type.agentGroup" },
]

type ChannelsState = {
  channels: ManagerChannelRuntimeMetaListResponse | null
  loading: boolean
  refreshing: boolean
  loadingMore: boolean
  error: Error | null
}

type ChannelClusterListPanelProps = {
  messagesHref?: string
}

type ChannelFilters = {
  keyword: string
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

function channelTypeLabel(intl: IntlShape, channelType: number) {
  const option = channelTypeOptions.find((item) => item.value === channelType)
  if (!option) {
    return intl.formatMessage({ id: "channelsBiz.type.custom" }, { type: channelType })
  }
  return `${intl.formatMessage({ id: option.labelId })} (${channelType})`
}

function firstNodeId(nodes: ManagerNodesResponse | null) {
  return nodes?.items[0]?.node_id ?? null
}

function channelListParams(filters: ChannelFilters, nodeId: number, cursor?: string): ChannelRuntimeMetaListParams {
  const params: ChannelRuntimeMetaListParams = { limit: channelPageLimit, includeMaxMessageSeq: true }
  params.nodeId = nodeId
  const keyword = filters.keyword.trim()
  if (keyword) {
    params.channelId = keyword
  }
  if (cursor) {
    params.cursor = cursor
  }
  return params
}

function appendChannelPage(
  current: ManagerChannelRuntimeMetaListResponse | null,
  nextPage: ManagerChannelRuntimeMetaListResponse,
): ManagerChannelRuntimeMetaListResponse {
  if (!current) {
    return nextPage
  }
  return {
    items: [...current.items, ...nextPage.items],
    has_more: nextPage.has_more,
    next_cursor: nextPage.next_cursor,
  }
}

export function ChannelClusterListPanel({
  messagesHref = "/messages",
}: ChannelClusterListPanelProps = {}) {
  const intl = useIntl()
  const navigate = useNavigate()
  const [state, setState] = useState<ChannelsState>({
    channels: null,
    loading: true,
    refreshing: false,
    loadingMore: false,
    error: null,
  })
  const [draftFilters, setDraftFilters] = useState<ChannelFilters>({ keyword: "" })
  const [activeFilters, setActiveFilters] = useState<ChannelFilters>({ keyword: "" })
  const [nodes, setNodes] = useState<ManagerNodesResponse | null>(null)
  const [selectedNodeId, setSelectedNodeId] = useState<number | null>(null)

  const loadNodes = useCallback(async () => {
    try {
      const nextNodes = await getNodes()
      setNodes(nextNodes)
      setSelectedNodeId((current) => {
        if (current !== null && hasNode(nextNodes, current)) {
          return current
        }
        return firstNodeId(nextNodes)
      })
      if (nextNodes.items.length === 0) {
        setState({ channels: null, loading: false, refreshing: false, loadingMore: false, error: null })
      }
    } catch (error) {
      setNodes(null)
      setSelectedNodeId(null)
      setState({
        channels: null,
        loading: false,
        refreshing: false,
        loadingMore: false,
        error: error instanceof Error ? error : new Error("node request failed"),
      })
    }
  }, [])

  const loadChannels = useCallback(async (refreshing: boolean, filters: ChannelFilters, nodeId: number) => {
    setState((current) => ({
      ...current,
      loading: refreshing ? current.loading : true,
      refreshing,
      loadingMore: false,
      error: null,
    }))

    try {
      const channels = await getChannelRuntimeMeta(channelListParams(filters, nodeId))
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
        error: error instanceof Error ? error : new Error("channel list request failed"),
      })
    }
  }, [])

  const loadMoreChannels = useCallback(async () => {
    const cursor = state.channels?.next_cursor
    if (!cursor || selectedNodeId === null) {
      return
    }

    setState((current) => ({
      ...current,
      loadingMore: true,
      error: null,
    }))

    try {
      const nextPage = await getChannelRuntimeMeta(channelListParams(activeFilters, selectedNodeId, cursor))
      setState((current) => ({
        channels: appendChannelPage(current.channels, nextPage),
        loading: false,
        refreshing: false,
        loadingMore: false,
        error: null,
      }))
    } catch (error) {
      setState((current) => ({
        ...current,
        loadingMore: false,
        error: error instanceof Error ? error : new Error("channel list request failed"),
      }))
    }
  }, [activeFilters, selectedNodeId, state.channels?.next_cursor])

  useEffect(() => {
    void loadNodes()
  }, [loadNodes])

  useEffect(() => {
    if (selectedNodeId !== null) {
      void loadChannels(false, activeFilters, selectedNodeId)
    }
  }, [activeFilters, loadChannels, selectedNodeId])

  function submitFilters(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setActiveFilters({
      keyword: draftFilters.keyword.trim(),
    })
  }

  const openMessages = useCallback(
    (channelType: number, channelId: string) => {
      navigate(`${messagesHref}?channel_id=${encodeURIComponent(channelId)}&channel_type=${channelType}`)
    },
    [messagesHref, navigate],
  )

  return (
    <>
      <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div>
          <h1 className="text-xl font-semibold tracking-tight text-foreground">
            {intl.formatMessage({ id: "nav.channels.title" })}
          </h1>
          <p className="mt-1 text-sm text-muted-foreground">
            {state.channels
              ? intl.formatMessage({ id: "channels.loadedValue" }, { count: state.channels.items.length })
              : intl.formatMessage({ id: "channels.loadedPending" })}
          </p>
        </div>
        <form className="flex flex-wrap gap-2" onSubmit={submitFilters}>
          <NodeFilter
            nodes={nodes}
            selectedNodeId={selectedNodeId}
            onNodeChange={(nodeId) => {
              setSelectedNodeId(nodeId)
            }}
          />
          <input
            className="h-8 min-w-52 rounded-md border border-border bg-background px-3 text-sm text-foreground outline-none focus:border-ring focus:ring-2 focus:ring-ring/30"
            onChange={(event) => setDraftFilters((current) => ({ ...current, keyword: event.target.value }))}
            placeholder={intl.formatMessage({ id: "channelsBiz.search.placeholder" })}
            value={draftFilters.keyword}
          />
          <Button size="sm" type="submit" variant="outline">
            {intl.formatMessage({ id: "common.search" })}
          </Button>
          <Button
            onClick={() => {
              if (selectedNodeId !== null) {
                void loadChannels(true, activeFilters, selectedNodeId)
              }
            }}
            size="sm"
            type="button"
            variant="outline"
          >
            {state.refreshing
              ? intl.formatMessage({ id: "common.refreshing" })
              : intl.formatMessage({ id: "common.refresh" })}
          </Button>
        </form>
      </div>

      {state.loading ? <ResourceState kind="loading" title={intl.formatMessage({ id: "nav.channels.title" })} /> : null}
      {!state.loading && state.error ? (
        <ResourceState
          kind={mapErrorKind(state.error)}
          onRetry={() => {
            if (selectedNodeId !== null) {
              void loadChannels(false, activeFilters, selectedNodeId)
            } else {
              void loadNodes()
            }
          }}
          title={intl.formatMessage({ id: "nav.channels.title" })}
        />
      ) : null}
      {!state.loading && !state.error && state.channels ? (
        <div className="rounded-xl border border-border bg-card p-3 shadow-none">
          {state.channels.items.length > 0 ? (
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
                      <th className="px-3 py-3">{intl.formatMessage({ id: "channels.table.actions" })}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {state.channels.items.map((channel) => (
                      <tr className="border-t border-border" key={`${channel.channel_type}-${channel.channel_id}`}>
                        <td className="px-3 py-3 text-sm font-medium text-foreground">{channel.channel_id}</td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">
                          {channelTypeLabel(intl, channel.channel_type)}
                        </td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">{channel.slot_id}</td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">{channel.leader || "-"}</td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">{channel.replicas.join(", ") || "-"}</td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">
                          {channel.isr.join(", ") || "-"} / {channel.min_isr}
                        </td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">
                          {channel.max_message_seq ?? "-"}
                        </td>
                        <td className="px-3 py-3 text-sm text-foreground">
                          <StatusBadge value={channel.status} />
                        </td>
                        <td className="px-3 py-3 text-sm text-foreground">
                          <Button
                            aria-label={intl.formatMessage(
                              { id: "channels.viewMessages" },
                              { id: channel.channel_id },
                            )}
                            onClick={() => {
                              openMessages(channel.channel_type, channel.channel_id)
                            }}
                            size="sm"
                            variant="outline"
                          >
                            {intl.formatMessage({ id: "channels.messagesAction" })}
                          </Button>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
              {state.channels.has_more ? (
                <div className="mt-3 flex justify-end">
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
                </div>
              ) : null}
            </>
          ) : (
            <ResourceState kind="empty" title={intl.formatMessage({ id: "nav.channels.title" })} />
          )}
        </div>
      ) : null}
    </>
  )
}

export function ChannelsPage() {
  const intl = useIntl()

  return (
    <PageContainer>
      <PageHeader
        eyebrow={intl.formatMessage({ id: "nav.path.cluster.channels" })}
        title={intl.formatMessage({ id: "nav.channels.title" })}
        description={intl.formatMessage({ id: "nav.channels.description" })}
      />
      <ChannelClusterListPanel />
    </PageContainer>
  )
}
