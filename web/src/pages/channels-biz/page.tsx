import type { FormEvent } from "react"
import { useCallback, useEffect, useState } from "react"
import type { IntlShape } from "react-intl"
import { useIntl } from "react-intl"

import { ActionFormDialog } from "@/components/manager/action-form-dialog"
import { ConfirmDialog } from "@/components/manager/confirm-dialog"
import { DetailSheet } from "@/components/manager/detail-sheet"
import { KeyValueList } from "@/components/manager/key-value-list"
import { ResourceState } from "@/components/manager/resource-state"
import { StatusBadge } from "@/components/manager/status-badge"
import { Button } from "@/components/ui/button"
import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
import { SectionCard } from "@/components/shell/section-card"
import {
  addBusinessChannelMembers,
  getBusinessChannel,
  getBusinessChannelMembers,
  getBusinessChannels,
  ManagerApiError,
  removeBusinessChannelMembers,
  upsertBusinessChannel,
} from "@/lib/manager-api"
import type {
  BusinessChannelMemberListKind,
  BusinessChannelMembersResponse,
  ManagerBusinessChannelDetailResponse,
  ManagerBusinessChannelListItem,
  ManagerBusinessChannelsResponse,
} from "@/lib/manager-api.types"

const channelPageLimit = 50
const memberPageLimit = 100

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

const memberKinds: BusinessChannelMemberListKind[] = ["subscribers", "allowlist", "denylist"]

type ChannelsState = {
  items: ManagerBusinessChannelListItem[]
  hasMore: boolean
  nextCursor?: string
  loading: boolean
  refreshing: boolean
  error: Error | null
}

type MemberState = {
  items: BusinessChannelMembersResponse["items"]
  hasMore: boolean
  nextCursor?: string
  loading: boolean
  loadingMore: boolean
  error: Error | null
}

type SelectedChannel = {
  channelId: string
  channelType: number
}

function emptyChannelsState(): ChannelsState {
  return {
    items: [],
    hasMore: false,
    loading: true,
    refreshing: false,
    error: null,
  }
}

function emptyMemberState(loading = false): MemberState {
  return {
    items: [],
    hasMore: false,
    loading,
    loadingMore: false,
    error: null,
  }
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

function mergeChannels(
  current: ManagerBusinessChannelListItem[],
  page: ManagerBusinessChannelsResponse,
  append: boolean,
) {
  if (!append) {
    return page.items
  }
  const seen = new Set(current.map((item) => `${item.channel_type}:${item.channel_id}`))
  const next = [...current]
  for (const item of page.items) {
    const key = `${item.channel_type}:${item.channel_id}`
    if (!seen.has(key)) {
      next.push(item)
    }
  }
  return next
}

function mergeMembers(
  current: BusinessChannelMembersResponse["items"],
  page: BusinessChannelMembersResponse,
  append: boolean,
) {
  if (!append) {
    return page.items
  }
  const seen = new Set(current.map((item) => item.uid))
  const next = [...current]
  for (const item of page.items) {
    if (!seen.has(item.uid)) {
      next.push(item)
    }
  }
  return next
}

function normalizeUIDs(value: string) {
  const seen = new Set<string>()
  const uids: string[] = []
  for (const raw of value.split(/[,\s;]+/)) {
    const uid = raw.trim()
    if (uid && !seen.has(uid)) {
      seen.add(uid)
      uids.push(uid)
    }
  }
  return uids
}

function channelTypeLabel(intl: IntlShape, channelType: number) {
  const option = channelTypeOptions.find((item) => item.value === channelType)
  if (!option) {
    return intl.formatMessage({ id: "channelsBiz.type.custom" }, { type: channelType })
  }
  return `${intl.formatMessage({ id: option.labelId })} (${channelType})`
}

function memberKindLabel(intl: IntlShape, kind: BusinessChannelMemberListKind) {
  return intl.formatMessage({ id: `channelsBiz.members.${kind}` })
}

function flagValues(channel: ManagerBusinessChannelListItem) {
  const flags: string[] = []
  if (channel.ban) {
    flags.push("banned")
  }
  if (channel.disband) {
    flags.push("disbanded")
  }
  if (channel.send_ban) {
    flags.push("send_banned")
  }
  return flags.length ? flags : ["normal"]
}

export function ChannelsBizPage() {
  const intl = useIntl()
  const [state, setState] = useState<ChannelsState>(emptyChannelsState)
  const [keywordInput, setKeywordInput] = useState("")
  const [typeInput, setTypeInput] = useState("")
  const [activeKeyword, setActiveKeyword] = useState("")
  const [activeType, setActiveType] = useState<number | null>(null)
  const [selectedChannel, setSelectedChannel] = useState<SelectedChannel | null>(null)
  const [detail, setDetail] = useState<ManagerBusinessChannelDetailResponse | null>(null)
  const [detailLoading, setDetailLoading] = useState(false)
  const [detailError, setDetailError] = useState<Error | null>(null)
  const [activeMemberKind, setActiveMemberKind] = useState<BusinessChannelMemberListKind>("subscribers")
  const [memberState, setMemberState] = useState<MemberState>(() => emptyMemberState(false))
  const [upsertOpen, setUpsertOpen] = useState(false)
  const [upsertInitial, setUpsertInitial] = useState<ManagerBusinessChannelListItem | null>(null)
  const [upsertPending, setUpsertPending] = useState(false)
  const [upsertError, setUpsertError] = useState("")
  const [addOpen, setAddOpen] = useState(false)
  const [addPending, setAddPending] = useState(false)
  const [addError, setAddError] = useState("")
  const [removeUID, setRemoveUID] = useState<string | null>(null)
  const [removePending, setRemovePending] = useState(false)
  const [removeError, setRemoveError] = useState("")

  const runQuery = useCallback(async (options?: {
    keyword?: string
    typeFilter?: number | null
    cursor?: string
    append?: boolean
    refreshing?: boolean
  }) => {
    const keyword = options?.keyword?.trim() ?? activeKeyword
    const typeFilter = options?.typeFilter ?? activeType
    const append = options?.append ?? false
    setState((current) => ({
      ...current,
      loading: append || options?.refreshing ? current.loading : true,
      refreshing: Boolean(options?.refreshing || append),
      error: null,
    }))

    try {
      const params: Parameters<typeof getBusinessChannels>[0] = { limit: channelPageLimit }
      if (keyword) {
        params.keyword = keyword
      }
      if (typeFilter !== null) {
        params.type = typeFilter
      }
      if (options?.cursor) {
        params.cursor = options.cursor
      }

      const page = await getBusinessChannels(params)
      setState((current) => ({
        items: mergeChannels(current.items, page, append),
        hasMore: page.has_more,
        nextCursor: page.next_cursor,
        loading: false,
        refreshing: false,
        error: null,
      }))
      setActiveKeyword(keyword)
      setActiveType(typeFilter)
    } catch (error) {
      setState({
        items: [],
        hasMore: false,
        loading: false,
        refreshing: false,
        error: error instanceof Error ? error : new Error("business channel request failed"),
      })
    }
  }, [activeKeyword, activeType])

  const loadDetail = useCallback(async (channel: SelectedChannel) => {
    setDetailLoading(true)
    setDetailError(null)
    try {
      const nextDetail = await getBusinessChannel(channel.channelType, channel.channelId)
      setDetail(nextDetail)
    } catch (error) {
      setDetail(null)
      setDetailError(error instanceof Error ? error : new Error("business channel detail failed"))
    } finally {
      setDetailLoading(false)
    }
  }, [])

  const loadMembers = useCallback(async (
    channel: SelectedChannel,
    kind: BusinessChannelMemberListKind,
    options?: { append?: boolean; cursor?: string; preserve?: boolean },
  ) => {
    const append = options?.append ?? false
    const preserve = options?.preserve ?? false
    setMemberState((current) => ({
      ...current,
      items: append || preserve ? current.items : [],
      loading: append ? current.loading : !preserve || current.items.length === 0,
      loadingMore: append,
      error: null,
    }))

    try {
      const page = await getBusinessChannelMembers(channel.channelType, channel.channelId, kind, {
        limit: memberPageLimit,
        cursor: options?.cursor,
      })
      setMemberState((current) => ({
        items: mergeMembers(current.items, page, append),
        hasMore: page.has_more,
        nextCursor: page.next_cursor,
        loading: false,
        loadingMore: false,
        error: null,
      }))
    } catch (error) {
      setMemberState((current) => ({
        ...current,
        loading: false,
        loadingMore: false,
        error: error instanceof Error ? error : new Error("business channel member request failed"),
      }))
    }
  }, [])

  useEffect(() => {
    void runQuery({ keyword: "", typeFilter: null })
    // Initial load only; follow-up queries are user driven.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  useEffect(() => {
    if (!selectedChannel) {
      return
    }
    void loadMembers(selectedChannel, activeMemberKind)
  }, [activeMemberKind, loadMembers, selectedChannel])

  const submitSearch = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const typeFilter = typeInput ? Number(typeInput) : null
    void runQuery({ keyword: keywordInput, typeFilter })
  }

  const openDetail = async (channelType: number, channelId: string) => {
    const channel = { channelId, channelType }
    setSelectedChannel(channel)
    setDetail(null)
    setDetailError(null)
    setActiveMemberKind("subscribers")
    setMemberState(emptyMemberState(true))
    await loadDetail(channel)
  }

  const closeDetail = (open: boolean) => {
    if (open) {
      return
    }
    setSelectedChannel(null)
    setDetail(null)
    setDetailError(null)
    setMemberState(emptyMemberState(false))
    setAddOpen(false)
    setRemoveUID(null)
  }

  const openCreateDialog = () => {
    setUpsertInitial(null)
    setUpsertError("")
    setUpsertOpen(true)
  }

  const openEditDialog = () => {
    if (!detail) {
      return
    }
    setUpsertInitial(detail)
    setUpsertError("")
    setUpsertOpen(true)
  }

  const submitUpsert = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const form = new FormData(event.currentTarget)
    const channelId = String(form.get("channel_id") ?? "").trim()
    const channelType = Number(form.get("channel_type") ?? 0)
    if (!channelId || !Number.isInteger(channelType) || channelType <= 0) {
      setUpsertError(intl.formatMessage({ id: "channelsBiz.form.invalidMetadata" }))
      return
    }

    setUpsertPending(true)
    setUpsertError("")
    try {
      const nextDetail = await upsertBusinessChannel({
        channelId,
        channelType,
        ban: form.get("ban") === "on",
        disband: form.get("disband") === "on",
        sendBan: form.get("send_ban") === "on",
      })
      if (selectedChannel?.channelId === nextDetail.channel_id && selectedChannel.channelType === nextDetail.channel_type) {
        setDetail(nextDetail)
      }
      setUpsertOpen(false)
      await runQuery({ keyword: activeKeyword, typeFilter: activeType, refreshing: true })
    } catch (error) {
      setUpsertError(error instanceof Error ? error.message : "upsert failed")
    } finally {
      setUpsertPending(false)
    }
  }

  const submitAddMembers = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!selectedChannel || isSubscriberMutationBlocked) {
      return
    }
    const form = new FormData(event.currentTarget)
    const uids = normalizeUIDs(String(form.get("uids") ?? ""))
    if (uids.length === 0) {
      setAddError(intl.formatMessage({ id: "channelsBiz.members.emptyUIDs" }))
      return
    }

    setAddPending(true)
    setAddError("")
    try {
      await addBusinessChannelMembers(selectedChannel.channelType, selectedChannel.channelId, activeMemberKind, { uids })
      setAddOpen(false)
      await loadMembers(selectedChannel, activeMemberKind, { preserve: true })
      await loadDetail(selectedChannel)
    } catch (error) {
      setAddError(error instanceof Error ? error.message : "add members failed")
    } finally {
      setAddPending(false)
    }
  }

  const confirmRemoveMember = async () => {
    if (!selectedChannel || !removeUID || isSubscriberMutationBlocked) {
      return
    }

    setRemovePending(true)
    setRemoveError("")
    try {
      await removeBusinessChannelMembers(selectedChannel.channelType, selectedChannel.channelId, activeMemberKind, {
        uids: [removeUID],
      })
      setRemoveUID(null)
      await loadMembers(selectedChannel, activeMemberKind, { preserve: true })
      await loadDetail(selectedChannel)
    } catch (error) {
      setRemoveError(error instanceof Error ? error.message : "remove member failed")
    } finally {
      setRemovePending(false)
    }
  }

  const isSubscriberMutationBlocked = selectedChannel?.channelType === 1 && activeMemberKind === "subscribers"
  const upsertTitle = upsertInitial
    ? intl.formatMessage({ id: "channelsBiz.action.edit" })
    : intl.formatMessage({ id: "channelsBiz.action.new" })

  return (
    <PageContainer>
      <PageHeader
        actions={(
          <div className="flex flex-wrap gap-2">
            <Button onClick={openCreateDialog} size="sm">
              {intl.formatMessage({ id: "channelsBiz.action.new" })}
            </Button>
            <Button
              onClick={() => {
                void runQuery({ keyword: activeKeyword, typeFilter: activeType, refreshing: true })
              }}
              size="sm"
              variant="outline"
            >
              {state.refreshing
                ? intl.formatMessage({ id: "common.refreshing" })
                : intl.formatMessage({ id: "common.refresh" })}
            </Button>
          </div>
        )}
        title={intl.formatMessage({ id: "channelsBiz.title" })}
        description={intl.formatMessage({ id: "channelsBiz.description" })}
      />

      <SectionCard
        className="overflow-hidden"
        description={intl.formatMessage({ id: "channelsBiz.list.description" })}
        title={intl.formatMessage({ id: "channelsBiz.list.title" })}
      >
        <form
          className="mb-4 grid gap-3 border-b border-border pb-4 lg:grid-cols-[minmax(0,1fr)_220px_auto]"
          data-testid="channels-biz-filter-toolbar"
          onSubmit={submitSearch}
        >
          <input
            className="h-9 min-w-0 flex-1 rounded-md border border-border bg-background px-3 text-sm outline-none focus:ring-2 focus:ring-ring"
            onChange={(event) => setKeywordInput(event.target.value)}
            placeholder={intl.formatMessage({ id: "channelsBiz.search.placeholder" })}
            value={keywordInput}
          />
          <label className="sr-only" htmlFor="channels-biz-type-filter">
            {intl.formatMessage({ id: "channelsBiz.filter.type" })}
          </label>
          <select
            aria-label={intl.formatMessage({ id: "channelsBiz.filter.type" })}
            className="h-9 rounded-md border border-border bg-background px-2 text-sm outline-none focus:ring-2 focus:ring-ring"
            id="channels-biz-type-filter"
            onChange={(event) => setTypeInput(event.target.value)}
            value={typeInput}
          >
            <option value="">{intl.formatMessage({ id: "channelsBiz.filter.allTypes" })}</option>
            {channelTypeOptions.map((option) => (
              <option key={option.value} value={option.value}>
                {channelTypeLabel(intl, option.value)}
              </option>
            ))}
          </select>
          <Button size="sm" type="submit">
            {intl.formatMessage({ id: "common.search" })}
          </Button>
        </form>

        {state.loading ? <ResourceState kind="loading" title={intl.formatMessage({ id: "channelsBiz.title" })} /> : null}
        {!state.loading && state.error ? (
          <ResourceState
            kind={mapErrorKind(state.error)}
            onRetry={() => {
              void runQuery({ keyword: activeKeyword, typeFilter: activeType })
            }}
            title={intl.formatMessage({ id: "channelsBiz.title" })}
          />
        ) : null}
        {!state.loading && !state.error ? (
          state.items.length > 0 ? (
            <div className="space-y-3">
              <div className="overflow-x-auto rounded-md border border-border" data-channels-biz-surface="inventory">
                <table
                  aria-label={intl.formatMessage({ id: "channelsBiz.list.title" })}
                  className="w-full border-collapse text-sm"
                >
                  <thead className="bg-muted/40 text-left text-xs uppercase tracking-[0.14em] text-muted-foreground">
                    <tr>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "channelsBiz.table.channel" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "channelsBiz.table.type" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "channelsBiz.table.flags" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "channelsBiz.table.routing" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "channelsBiz.table.version" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "channelsBiz.table.actions" })}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {state.items.map((channel) => (
                      <tr className="border-t border-border" key={`${channel.channel_type}:${channel.channel_id}`}>
                        <td className="px-3 py-3 text-sm font-medium text-foreground">{channel.channel_id}</td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">
                          {channelTypeLabel(intl, channel.channel_type)}
                        </td>
                        <td className="px-3 py-3 text-sm text-foreground">
                          <div className="flex flex-wrap gap-1">
                            {flagValues(channel).map((flag) => (
                              <StatusBadge key={flag} value={flag} />
                            ))}
                          </div>
                        </td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">
                          {intl.formatMessage(
                            { id: "channelsBiz.routing.value" },
                            { slot: channel.slot_id, hash: channel.hash_slot },
                          )}
                        </td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">
                          {channel.subscriber_mutation_version}
                        </td>
                        <td className="px-3 py-3 text-sm text-foreground">
                          <Button
                            aria-label={intl.formatMessage(
                              { id: "channelsBiz.inspectChannel" },
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
              {state.hasMore && state.nextCursor ? (
                <Button
                  onClick={() => {
                    void runQuery({
                      keyword: activeKeyword,
                      typeFilter: activeType,
                      cursor: state.nextCursor,
                      append: true,
                    })
                  }}
                  size="sm"
                  variant="outline"
                >
                  {intl.formatMessage({ id: "common.loadMore" })}
                </Button>
              ) : null}
            </div>
          ) : (
            <ResourceState kind="empty" title={intl.formatMessage({ id: "channelsBiz.title" })} />
          )
        ) : null}
      </SectionCard>

      <DetailSheet
        description={
          detail
            ? intl.formatMessage(
                { id: "channelsBiz.detail.description" },
                { type: detail.channel_type, slot: detail.slot_id },
              )
            : undefined
        }
        footer={detail ? (
          <div className="flex justify-end gap-2">
            <Button onClick={openEditDialog} size="sm" variant="outline">
              {intl.formatMessage({ id: "channelsBiz.action.edit" })}
            </Button>
          </div>
        ) : null}
        onOpenChange={closeDetail}
        open={selectedChannel !== null}
        title={detail ? detail.channel_id : intl.formatMessage({ id: "channelsBiz.detail.title" })}
      >
        {detailLoading ? (
          <ResourceState kind="loading" title={intl.formatMessage({ id: "channelsBiz.detail.title" })} />
        ) : null}
        {!detailLoading && detailError ? (
          <ResourceState
            kind={mapErrorKind(detailError)}
            onRetry={() => {
              if (selectedChannel) {
                void loadDetail(selectedChannel)
              }
            }}
            title={intl.formatMessage({ id: "channelsBiz.detail.title" })}
          />
        ) : null}
        {!detailLoading && !detailError && detail && selectedChannel ? (
          <div className="space-y-5">
            <KeyValueList
              items={[
                { label: intl.formatMessage({ id: "channelsBiz.detail.channelId" }), value: detail.channel_id },
                {
                  label: intl.formatMessage({ id: "channelsBiz.detail.channelType" }),
                  value: channelTypeLabel(intl, detail.channel_type),
                },
                { label: intl.formatMessage({ id: "channelsBiz.detail.slotId" }), value: detail.slot_id },
                { label: intl.formatMessage({ id: "channelsBiz.detail.hashSlot" }), value: detail.hash_slot },
                {
                  label: intl.formatMessage({ id: "channelsBiz.detail.subscriberMutationVersion" }),
                  value: detail.subscriber_mutation_version,
                },
                {
                  label: intl.formatMessage({ id: "channelsBiz.detail.flags" }),
                  value: (
                    <div className="flex flex-wrap gap-1">
                      {flagValues(detail).map((flag) => (
                        <StatusBadge key={flag} value={flag} />
                      ))}
                    </div>
                  ),
                },
                {
                  label: intl.formatMessage({ id: "channelsBiz.detail.hasSubscribers" }),
                  value: detail.has_subscribers ? intl.formatMessage({ id: "common.confirm" }) : "-",
                },
                {
                  label: intl.formatMessage({ id: "channelsBiz.detail.hasAllowlist" }),
                  value: detail.has_allowlist ? intl.formatMessage({ id: "common.confirm" }) : "-",
                },
                {
                  label: intl.formatMessage({ id: "channelsBiz.detail.hasDenylist" }),
                  value: detail.has_denylist ? intl.formatMessage({ id: "common.confirm" }) : "-",
                },
              ]}
            />

            <div className="space-y-3">
              <div className="flex flex-wrap items-center justify-between gap-3">
                <div>
                  <h3 className="text-sm font-semibold text-foreground">
                    {intl.formatMessage({ id: "channelsBiz.members.title" })}
                  </h3>
                  <p className="mt-1 text-sm text-muted-foreground">
                    {intl.formatMessage({ id: "channelsBiz.members.description" })}
                  </p>
                </div>
                <Button
                  disabled={isSubscriberMutationBlocked}
                  onClick={() => {
                    setAddError("")
                    setAddOpen(true)
                  }}
                  size="sm"
                >
                  {intl.formatMessage({ id: "channelsBiz.members.add" })}
                </Button>
              </div>

              <div
                className="flex flex-wrap gap-2 rounded-md border border-border bg-muted/30 p-2"
                data-testid="channels-biz-member-toolbar"
              >
                {memberKinds.map((kind) => (
                  <Button
                    aria-pressed={activeMemberKind === kind}
                    key={kind}
                    onClick={() => setActiveMemberKind(kind)}
                    size="sm"
                    variant={activeMemberKind === kind ? "default" : "outline"}
                  >
                    {memberKindLabel(intl, kind)}
                  </Button>
                ))}
              </div>

              {isSubscriberMutationBlocked ? (
                <p className="rounded-md border border-border bg-muted/30 px-3 py-2 text-sm text-muted-foreground">
                  {intl.formatMessage({ id: "channelsBiz.members.personSubscriberBlocked" })}
                </p>
              ) : null}

              {memberState.loading ? (
                <ResourceState kind="loading" title={memberKindLabel(intl, activeMemberKind)} />
              ) : null}
              {!memberState.loading && memberState.error ? (
                <ResourceState
                  kind={mapErrorKind(memberState.error)}
                  onRetry={() => {
                    void loadMembers(selectedChannel, activeMemberKind)
                  }}
                  title={memberKindLabel(intl, activeMemberKind)}
                />
              ) : null}
              {!memberState.loading && !memberState.error ? (
                memberState.items.length > 0 ? (
                  <div className="space-y-3">
                    <div className="overflow-x-auto rounded-md border border-border" data-channels-biz-surface="members">
                      <table aria-label={memberKindLabel(intl, activeMemberKind)} className="w-full border-collapse text-sm">
                        <thead className="bg-muted/40 text-left text-xs uppercase tracking-[0.14em] text-muted-foreground">
                          <tr>
                            <th className="px-3 py-3">{intl.formatMessage({ id: "channelsBiz.members.uid" })}</th>
                            <th className="px-3 py-3">{intl.formatMessage({ id: "channelsBiz.table.actions" })}</th>
                          </tr>
                        </thead>
                        <tbody>
                          {memberState.items.map((member) => (
                            <tr className="border-t border-border" key={member.uid}>
                              <td className="px-3 py-3 text-sm font-medium text-foreground">{member.uid}</td>
                              <td className="px-3 py-3 text-sm">
                                <Button
                                  aria-label={intl.formatMessage(
                                    { id: "channelsBiz.members.removeMember" },
                                    { uid: member.uid },
                                  )}
                                  disabled={isSubscriberMutationBlocked}
                                  onClick={() => {
                                    setRemoveUID(member.uid)
                                    setRemoveError("")
                                  }}
                                  size="sm"
                                  variant="outline"
                                >
                                  {intl.formatMessage({ id: "channelsBiz.members.remove" })}
                                </Button>
                              </td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    </div>
                    {memberState.hasMore && memberState.nextCursor ? (
                      <Button
                        onClick={() => {
                          void loadMembers(selectedChannel, activeMemberKind, {
                            append: true,
                            cursor: memberState.nextCursor,
                          })
                        }}
                        size="sm"
                        variant="outline"
                      >
                        {memberState.loadingMore
                          ? intl.formatMessage({ id: "common.loading" })
                          : intl.formatMessage({ id: "common.loadMore" })}
                      </Button>
                    ) : null}
                  </div>
                ) : (
                  <ResourceState kind="empty" title={memberKindLabel(intl, activeMemberKind)} />
                )
              ) : null}
            </div>
          </div>
        ) : null}
      </DetailSheet>

      <ActionFormDialog
        description={intl.formatMessage({ id: "channelsBiz.form.description" })}
        error={upsertError}
        onOpenChange={setUpsertOpen}
        onSubmit={submitUpsert}
        open={upsertOpen}
        pending={upsertPending}
        submitLabel={intl.formatMessage({ id: "channelsBiz.form.save" })}
        title={upsertTitle}
      >
        <label className="block text-sm">
          {intl.formatMessage({ id: "channelsBiz.form.channelId" })}
          <input
            className="mt-1 h-9 w-full rounded-md border border-border bg-background px-3 text-sm"
            defaultValue={upsertInitial?.channel_id ?? ""}
            name="channel_id"
          />
        </label>
        <label className="block text-sm">
          {intl.formatMessage({ id: "channelsBiz.form.channelType" })}
          <select
            aria-label={intl.formatMessage({ id: "channelsBiz.form.channelType" })}
            className="mt-1 h-9 w-full rounded-md border border-border bg-background px-2 text-sm"
            defaultValue={upsertInitial?.channel_type ?? 2}
            name="channel_type"
          >
            {channelTypeOptions.map((option) => (
              <option key={option.value} value={option.value}>
                {channelTypeLabel(intl, option.value)}
              </option>
            ))}
          </select>
        </label>
        <label className="flex items-center gap-2 text-sm">
          <input defaultChecked={upsertInitial?.ban ?? false} name="ban" type="checkbox" />
          {intl.formatMessage({ id: "channelsBiz.form.ban" })}
        </label>
        <label className="flex items-center gap-2 text-sm">
          <input defaultChecked={upsertInitial?.disband ?? false} name="disband" type="checkbox" />
          {intl.formatMessage({ id: "channelsBiz.form.disband" })}
        </label>
        <label className="flex items-center gap-2 text-sm">
          <input defaultChecked={upsertInitial?.send_ban ?? false} name="send_ban" type="checkbox" />
          {intl.formatMessage({ id: "channelsBiz.form.sendBan" })}
        </label>
      </ActionFormDialog>

      <ActionFormDialog
        description={intl.formatMessage(
          { id: "channelsBiz.members.addDescription" },
          { list: memberKindLabel(intl, activeMemberKind) },
        )}
        error={addError}
        onOpenChange={setAddOpen}
        onSubmit={submitAddMembers}
        open={addOpen}
        pending={addPending}
        submitLabel={intl.formatMessage({ id: "channelsBiz.members.add" })}
        title={intl.formatMessage({ id: "channelsBiz.members.add" })}
      >
        <label className="block text-sm">
          {intl.formatMessage({ id: "channelsBiz.members.uids" })}
          <textarea
            className="mt-1 min-h-28 w-full rounded-md border border-border bg-background px-3 py-2 text-sm"
            name="uids"
            placeholder={intl.formatMessage({ id: "channelsBiz.members.uidsPlaceholder" })}
          />
        </label>
      </ActionFormDialog>

      <ConfirmDialog
        confirmLabel={intl.formatMessage({ id: "channelsBiz.members.confirmRemove" })}
        description={
          removeUID
            ? intl.formatMessage(
                { id: "channelsBiz.members.removeDescription" },
                { uid: removeUID, list: memberKindLabel(intl, activeMemberKind) },
              )
            : undefined
        }
        error={removeError}
        onConfirm={() => {
          void confirmRemoveMember()
        }}
        onOpenChange={(open) => {
          if (!open) {
            setRemoveUID(null)
          }
        }}
        open={removeUID !== null}
        pending={removePending}
        title={intl.formatMessage({ id: "channelsBiz.members.remove" })}
      />
    </PageContainer>
  )
}
