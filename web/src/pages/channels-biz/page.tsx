import type { FormEvent } from "react"
import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { useIntl, type IntlShape } from "react-intl"
import { Link, useSearchParams } from "react-router-dom"

import { useAuthStore } from "@/auth/auth-store"
import { hasManagerPermission } from "@/auth/permissions"
import { ActionFormDialog } from "@/components/manager/action-form-dialog"
import { ConfirmDialog } from "@/components/manager/confirm-dialog"
import { DetailSheet } from "@/components/manager/detail-sheet"
import { KeyValueList } from "@/components/manager/key-value-list"
import { ResourceState } from "@/components/manager/resource-state"
import { StatusBadge } from "@/components/manager/status-badge"
import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
import { SectionCard } from "@/components/shell/section-card"
import { Button } from "@/components/ui/button"
import {
  addBusinessChannelMembers,
  createBusinessChannel,
  getBusinessChannel,
  getBusinessChannelMembers,
  getBusinessChannels,
  ManagerApiError,
  removeBusinessChannelMembers,
  updateBusinessChannel,
} from "@/lib/manager-api"
import type {
  BusinessChannelMemberListKind,
  BusinessChannelMembersResponse,
  ManagerBusinessChannelDetailResponse,
  ManagerBusinessChannelListItem,
  ManagerBusinessChannelsResponse,
  MutateBusinessChannelMembersResponse,
} from "@/lib/manager-api.types"

const channelPageLimit = 50
const memberPageLimit = 100
const maxMutationUIDs = 500
const maxUIDBytes = 256

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

const memberKinds: BusinessChannelMemberListKind[] = ["subscribers", "denylist", "allowlist"]

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
  currentCursor?: string
  previousCursors: Array<string | undefined>
  loading: boolean
  error: Error | null
}

type SelectedChannel = {
  channelId: string
  channelType: number
}

type UIDParseResult =
  | { ok: true; uids: string[] }
  | { ok: false; messageId: string; invalid: string[] }

type MemberNotice = {
  response?: MutateBusinessChannelMembersResponse
  error?: string
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
    previousCursors: [],
    loading,
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

function parseUIDs(value: string): UIDParseResult {
  const parts = value.split(/[,;\r\n]+/)
  const invalid: string[] = []
  const seen = new Set<string>()
  const uids: string[] = []

  for (const part of parts) {
    const uid = part.trim()
    if (!uid) {
      continue
    }
    if (
      !new TextEncoder().encode(uid).length
      || new TextEncoder().encode(uid).length > maxUIDBytes
      || /[\s\p{C}]/u.test(uid)
    ) {
      invalid.push(uid)
      continue
    }
    if (!seen.has(uid)) {
      seen.add(uid)
      uids.push(uid)
    }
  }

  if (invalid.length > 0) {
    return { ok: false, messageId: "channelsBiz.members.invalidUIDs", invalid }
  }
  if (uids.length === 0) {
    return { ok: false, messageId: "channelsBiz.members.emptyUIDs", invalid: [] }
  }
  if (uids.length > maxMutationUIDs) {
    return { ok: false, messageId: "channelsBiz.members.tooManyUIDs", invalid: [] }
  }
  return { ok: true, uids }
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

function normalizeMemberKind(value: string | null): BusinessChannelMemberListKind | null {
  return memberKinds.includes(value as BusinessChannelMemberListKind)
    ? (value as BusinessChannelMemberListKind)
    : null
}

function formatUIDParseError(intl: IntlShape, result: Exclude<UIDParseResult, { ok: true }>) {
  if (result.invalid.length === 0) {
    return intl.formatMessage({ id: result.messageId })
  }
  return intl.formatMessage(
    { id: result.messageId },
    { uids: result.invalid.slice(0, 5).join(", ") },
  )
}

export function ChannelsBizPage() {
  const intl = useIntl()
  const permissions = useAuthStore((store) => store.permissions)
  const canWrite = useMemo(
    () => hasManagerPermission(permissions, "cluster.channel", "w"),
    [permissions],
  )
  const [searchParams, setSearchParams] = useSearchParams()
  const selectedChannelID = searchParams.get("channel_id") ?? ""
  const selectedChannelType = Number(searchParams.get("channel_type"))
  const selectedChannel = useMemo<SelectedChannel | null>(() => {
    if (
      !selectedChannelID
      || !Number.isInteger(selectedChannelType)
      || selectedChannelType <= 0
      || selectedChannelType > 255
    ) {
      return null
    }
    return { channelId: selectedChannelID, channelType: selectedChannelType }
  }, [selectedChannelID, selectedChannelType])
  const activeMemberKind = normalizeMemberKind(searchParams.get("member_list")) ?? "subscribers"
  const sheetSection = normalizeMemberKind(searchParams.get("member_list")) ? "members" : "detail"

  const [state, setState] = useState<ChannelsState>(emptyChannelsState)
  const [keywordInput, setKeywordInput] = useState("")
  const [typeInput, setTypeInput] = useState("")
  const [activeKeyword, setActiveKeyword] = useState("")
  const [activeType, setActiveType] = useState<number | null>(null)
  const [detail, setDetail] = useState<ManagerBusinessChannelDetailResponse | null>(null)
  const [detailLoading, setDetailLoading] = useState(false)
  const [detailError, setDetailError] = useState<Error | null>(null)
  const [memberState, setMemberState] = useState<MemberState>(() => emptyMemberState(false))
  const [memberSearchInput, setMemberSearchInput] = useState("")
  const [activeMemberSearch, setActiveMemberSearch] = useState("")
  const [memberNotice, setMemberNotice] = useState<MemberNotice | null>(null)
  const [upsertOpen, setUpsertOpen] = useState(false)
  const [upsertInitial, setUpsertInitial] = useState<ManagerBusinessChannelDetailResponse | null>(null)
  const [upsertPending, setUpsertPending] = useState(false)
  const [upsertError, setUpsertError] = useState("")
  const [addOpen, setAddOpen] = useState(false)
  const [addUIDsInput, setAddUIDsInput] = useState("")
  const [addPending, setAddPending] = useState(false)
  const [addError, setAddError] = useState("")
  const [removeDraftOpen, setRemoveDraftOpen] = useState(false)
  const [removeDraftError, setRemoveDraftError] = useState("")
  const [removeUIDs, setRemoveUIDs] = useState<string[]>([])
  const [removePending, setRemovePending] = useState(false)
  const [removeError, setRemoveError] = useState("")
  const memberRequestID = useRef(0)

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

  const loadMemberPage = useCallback(async (
    channel: SelectedChannel,
    kind: BusinessChannelMemberListKind,
    options?: {
      cursor?: string
      previousCursors?: Array<string | undefined>
      preserve?: boolean
    },
  ) => {
    const requestID = ++memberRequestID.current
    const preserve = options?.preserve ?? false
    setMemberState((current) => ({
      ...current,
      items: preserve ? current.items : [],
      loading: true,
      error: null,
    }))
    try {
      const params: Parameters<typeof getBusinessChannelMembers>[3] = { limit: memberPageLimit }
      if (options?.cursor) {
        params.cursor = options.cursor
      }
      const page = await getBusinessChannelMembers(channel.channelType, channel.channelId, kind, params)
      if (requestID !== memberRequestID.current) {
        return null
      }
      setMemberState({
        items: page.items,
        hasMore: page.has_more,
        nextCursor: page.next_cursor,
        currentCursor: options?.cursor,
        previousCursors: options?.previousCursors ?? [],
        loading: false,
        error: null,
      })
      return page
    } catch (error) {
      if (requestID !== memberRequestID.current) {
        return null
      }
      setMemberState((current) => ({
        ...current,
        loading: false,
        error: error instanceof Error ? error : new Error("business channel member request failed"),
      }))
      return null
    }
  }, [])

  const loadMemberExact = useCallback(async (
    channel: SelectedChannel,
    kind: BusinessChannelMemberListKind,
    uid: string,
    preserve = false,
  ) => {
    const requestID = ++memberRequestID.current
    setMemberState((current) => ({
      ...current,
      items: preserve ? current.items : [],
      loading: true,
      error: null,
    }))
    try {
      const page = await getBusinessChannelMembers(channel.channelType, channel.channelId, kind, {
        limit: memberPageLimit,
        uid,
      })
      if (requestID !== memberRequestID.current) {
        return null
      }
      setMemberState({
        items: page.items,
        hasMore: false,
        previousCursors: [],
        loading: false,
        error: null,
      })
      return page
    } catch (error) {
      if (requestID !== memberRequestID.current) {
        return null
      }
      setMemberState((current) => ({
        ...current,
        loading: false,
        error: error instanceof Error ? error : new Error("business channel member request failed"),
      }))
      return null
    }
  }, [])

  useEffect(() => {
    void runQuery({ keyword: "", typeFilter: null })
    // Initial load only; follow-up queries are user driven.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  useEffect(() => {
    setDetail(null)
    setDetailError(null)
    if (!selectedChannel || sheetSection !== "detail") {
      return
    }
    void loadDetail(selectedChannel)
  }, [loadDetail, selectedChannel, sheetSection])

  useEffect(() => {
    setMemberSearchInput("")
    setActiveMemberSearch("")
    setMemberNotice(null)
    setMemberState(emptyMemberState(Boolean(selectedChannel && sheetSection === "members")))
    if (!selectedChannel || sheetSection !== "members") {
      return
    }
    void loadMemberPage(selectedChannel, activeMemberKind)
  }, [
    activeMemberKind,
    loadMemberPage,
    selectedChannel,
    sheetSection,
  ])

  const setSheet = (
    channel: SelectedChannel | null,
    memberKind?: BusinessChannelMemberListKind,
    replace = false,
  ) => {
    const next = new URLSearchParams(searchParams)
    if (!channel) {
      next.delete("channel_id")
      next.delete("channel_type")
      next.delete("member_list")
    } else {
      next.set("channel_id", channel.channelId)
      next.set("channel_type", String(channel.channelType))
      if (memberKind) {
        next.set("member_list", memberKind)
      } else {
        next.delete("member_list")
      }
    }
    setSearchParams(next, { replace })
  }

  const submitSearch = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    void runQuery({ keyword: keywordInput, typeFilter: typeInput ? Number(typeInput) : null })
  }

  const submitMemberSearch = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!selectedChannel) {
      return
    }
    const uid = memberSearchInput.trim()
    if (!uid) {
      setActiveMemberSearch("")
      void loadMemberPage(selectedChannel, activeMemberKind)
      return
    }
    const parsed = parseUIDs(uid)
    if (!parsed.ok || parsed.uids.length !== 1 || parsed.uids[0] !== uid) {
      setMemberState((current) => ({
        ...current,
        error: new Error(
          parsed.ok
            ? intl.formatMessage({ id: "channelsBiz.members.exactOneUID" })
            : formatUIDParseError(intl, parsed),
        ),
      }))
      return
    }
    setActiveMemberSearch(uid)
    void loadMemberExact(selectedChannel, activeMemberKind, uid)
  }

  const clearMemberSearch = () => {
    if (!selectedChannel) {
      return
    }
    setMemberSearchInput("")
    setActiveMemberSearch("")
    void loadMemberPage(selectedChannel, activeMemberKind)
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
    if (!channelId || !Number.isInteger(channelType) || channelType <= 0 || channelType > 255) {
      setUpsertError(intl.formatMessage({ id: "channelsBiz.form.invalidMetadata" }))
      return
    }

    const flags = {
      ban: form.get("ban") === "on",
      disband: form.get("disband") === "on",
      sendBan: form.get("send_ban") === "on",
    }
    setUpsertPending(true)
    setUpsertError("")
    try {
      const nextDetail = upsertInitial
        ? await updateBusinessChannel(upsertInitial.channel_type, upsertInitial.channel_id, flags)
        : await createBusinessChannel({ channelId, channelType, ...flags })
      if (
        selectedChannel?.channelId === nextDetail.channel_id
        && selectedChannel.channelType === nextDetail.channel_type
      ) {
        setDetail(nextDetail)
      }
      setUpsertOpen(false)
      await runQuery({ keyword: activeKeyword, typeFilter: activeType, refreshing: true })
    } catch (error) {
      setUpsertError(error instanceof Error ? error.message : "channel mutation failed")
    } finally {
      setUpsertPending(false)
    }
  }

  const refreshMembersAfterMutation = async (
    operation: "add" | "remove",
    mutationSucceeded: boolean,
  ) => {
    if (!selectedChannel) {
      return
    }
    if (activeMemberSearch) {
      await loadMemberExact(selectedChannel, activeMemberKind, activeMemberSearch, true)
      return
    }
    if (operation === "add" && mutationSucceeded) {
      await loadMemberPage(selectedChannel, activeMemberKind, { preserve: true })
      return
    }
    const page = await loadMemberPage(selectedChannel, activeMemberKind, {
      cursor: memberState.currentCursor,
      previousCursors: memberState.previousCursors,
      preserve: true,
    })
    if (
      operation === "remove"
      && page
      && page.items.length === 0
      && memberState.previousCursors.length > 0
    ) {
      const previousCursors = memberState.previousCursors.slice(0, -1)
      await loadMemberPage(selectedChannel, activeMemberKind, {
        cursor: memberState.previousCursors[memberState.previousCursors.length - 1],
        previousCursors,
        preserve: true,
      })
    }
  }

  const submitAddMembers = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!selectedChannel || !canWrite || isSubscriberMutationBlocked) {
      return
    }
    const form = new FormData(event.currentTarget)
    const parsed = parseUIDs(String(form.get("uids") ?? ""))
    if (!parsed.ok) {
      setAddError(formatUIDParseError(intl, parsed))
      return
    }

    setAddPending(true)
    setAddError("")
    try {
      const response = await addBusinessChannelMembers(
        selectedChannel.channelType,
        selectedChannel.channelId,
        activeMemberKind,
        { uids: parsed.uids },
      )
      setMemberNotice({ response })
      setAddOpen(false)
      await refreshMembersAfterMutation("add", true)
    } catch (error) {
      const message = error instanceof Error ? error.message : "add members failed"
      setAddError(intl.formatMessage({ id: "channelsBiz.members.uncertainFailure" }, { error: message }))
      setMemberNotice({ error: message })
      await refreshMembersAfterMutation("add", false)
    } finally {
      setAddPending(false)
    }
  }

  const submitRemoveDraft = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const form = new FormData(event.currentTarget)
    const parsed = parseUIDs(String(form.get("uids") ?? ""))
    if (!parsed.ok) {
      setRemoveDraftError(formatUIDParseError(intl, parsed))
      return
    }
    setRemoveDraftError("")
    setRemoveDraftOpen(false)
    setRemoveUIDs(parsed.uids)
  }

  const confirmRemoveMembers = async () => {
    if (!selectedChannel || removeUIDs.length === 0 || !canWrite || isSubscriberMutationBlocked) {
      return
    }
    setRemovePending(true)
    setRemoveError("")
    try {
      const response = await removeBusinessChannelMembers(
        selectedChannel.channelType,
        selectedChannel.channelId,
        activeMemberKind,
        { uids: removeUIDs },
      )
      setMemberNotice({ response })
      setRemoveUIDs([])
      await refreshMembersAfterMutation("remove", true)
    } catch (error) {
      const message = error instanceof Error ? error.message : "remove members failed"
      setRemoveError(intl.formatMessage({ id: "channelsBiz.members.uncertainFailure" }, { error: message }))
      setMemberNotice({ error: message })
      await refreshMembersAfterMutation("remove", false)
    } finally {
      setRemovePending(false)
    }
  }

  const isSubscriberMutationBlocked =
    selectedChannel?.channelType === 1 && activeMemberKind === "subscribers"
  const upsertTitle = upsertInitial
    ? intl.formatMessage({ id: "channelsBiz.action.edit" })
    : intl.formatMessage({ id: "channelsBiz.action.new" })

  return (
    <PageContainer>
      <PageHeader
        actions={(
          <div className="flex flex-wrap gap-2">
            {canWrite ? (
              <Button onClick={openCreateDialog} size="sm">
                {intl.formatMessage({ id: "channelsBiz.action.new" })}
              </Button>
            ) : null}
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
                            {flagValues(channel).map((flag) => <StatusBadge key={flag} value={flag} />)}
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
                          <div className="flex flex-wrap gap-2">
                            <Button
                              aria-label={intl.formatMessage(
                                { id: "channelsBiz.viewDetailAria" },
                                { id: channel.channel_id },
                              )}
                              onClick={() => setSheet({
                                channelId: channel.channel_id,
                                channelType: channel.channel_type,
                              })}
                              size="sm"
                              variant="outline"
                            >
                              {intl.formatMessage({ id: "channelsBiz.action.viewDetail" })}
                            </Button>
                            <Button
                              aria-label={intl.formatMessage(
                                { id: "channelsBiz.viewMembersAria" },
                                { id: channel.channel_id },
                              )}
                              onClick={() => setSheet(
                                { channelId: channel.channel_id, channelType: channel.channel_type },
                                "subscribers",
                              )}
                              size="sm"
                              variant="outline"
                            >
                              {intl.formatMessage({ id: "channelsBiz.action.memberData" })}
                            </Button>
                          </div>
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
          selectedChannel
            ? intl.formatMessage(
                { id: "channelsBiz.detail.description" },
                { type: selectedChannel.channelType, slot: detail?.slot_id ?? "-" },
              )
            : undefined
        }
        footer={
          sheetSection === "detail" && detail && canWrite ? (
            <div className="flex justify-end gap-2">
              <Button onClick={openEditDialog} size="sm" variant="outline">
                {intl.formatMessage({ id: "channelsBiz.action.edit" })}
              </Button>
            </div>
          ) : null
        }
        onOpenChange={(open) => {
          if (!open) {
            setSheet(null, undefined, true)
            setAddOpen(false)
            setRemoveDraftOpen(false)
            setRemoveUIDs([])
          }
        }}
        open={selectedChannel !== null}
        title={selectedChannel?.channelId ?? intl.formatMessage({ id: "channelsBiz.detail.title" })}
      >
        {selectedChannel ? (
          <div className="space-y-5">
            <div className="flex gap-2 border-b border-border pb-3">
              <Button
                onClick={() => setSheet(selectedChannel)}
                size="sm"
                variant={sheetSection === "detail" ? "default" : "outline"}
              >
                {intl.formatMessage({ id: "channelsBiz.action.viewDetail" })}
              </Button>
              <Button
                onClick={() => setSheet(selectedChannel, activeMemberKind)}
                size="sm"
                variant={sheetSection === "members" ? "default" : "outline"}
              >
                {intl.formatMessage({ id: "channelsBiz.action.memberData" })}
              </Button>
            </div>

            {sheetSection === "detail" ? (
              <>
                {detailLoading ? (
                  <ResourceState kind="loading" title={intl.formatMessage({ id: "channelsBiz.detail.title" })} />
                ) : null}
                {!detailLoading && detailError ? (
                  <ResourceState
                    kind={mapErrorKind(detailError)}
                    onRetry={() => void loadDetail(selectedChannel)}
                    title={intl.formatMessage({ id: "channelsBiz.detail.title" })}
                  />
                ) : null}
                {!detailLoading && !detailError && detail ? (
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
                            {flagValues(detail).map((flag) => <StatusBadge key={flag} value={flag} />)}
                          </div>
                        ),
                      },
                      {
                        label: intl.formatMessage({ id: "channelsBiz.detail.hasSubscribers" }),
                        value: detail.has_subscribers ? intl.formatMessage({ id: "common.confirm" }) : "-",
                      },
                      {
                        label: intl.formatMessage({ id: "channelsBiz.detail.hasDenylist" }),
                        value: detail.has_denylist ? intl.formatMessage({ id: "common.confirm" }) : "-",
                      },
                      {
                        label: intl.formatMessage({ id: "channelsBiz.detail.hasAllowlist" }),
                        value: detail.has_allowlist ? intl.formatMessage({ id: "common.confirm" }) : "-",
                      },
                    ]}
                  />
                ) : null}
              </>
            ) : (
              <section className="space-y-4">
                <div
                  className="flex flex-wrap items-center justify-between gap-2 rounded-md border border-border bg-muted/30 p-2"
                  data-testid="channels-biz-member-toolbar"
                >
                  <div className="flex flex-wrap gap-2">
                    <Button
                      aria-label={intl.formatMessage({ id: "channelsBiz.members.refresh" })}
                      disabled={memberState.loading}
                      onClick={() => {
                        if (activeMemberSearch) {
                          void loadMemberExact(
                            selectedChannel,
                            activeMemberKind,
                            activeMemberSearch,
                            true,
                          )
                        } else {
                          void loadMemberPage(selectedChannel, activeMemberKind, {
                            cursor: memberState.currentCursor,
                            previousCursors: memberState.previousCursors,
                            preserve: true,
                          })
                        }
                      }}
                      size="sm"
                      variant="outline"
                    >
                      {intl.formatMessage({ id: "common.refresh" })}
                    </Button>
                    {memberKinds.map((kind) => (
                      <Button
                        key={kind}
                        onClick={() => setSheet(selectedChannel, kind)}
                        size="sm"
                        variant={activeMemberKind === kind ? "default" : "outline"}
                      >
                        {memberKindLabel(intl, kind)}
                      </Button>
                    ))}
                  </div>
                  {canWrite ? (
                    <div className="flex flex-wrap gap-2">
                      <Button
                        disabled={isSubscriberMutationBlocked}
                        onClick={() => {
                          setAddError("")
                          setAddUIDsInput("")
                          setAddOpen(true)
                        }}
                        size="sm"
                      >
                        {intl.formatMessage({ id: "channelsBiz.members.add" })}
                      </Button>
                      <Button
                        disabled={isSubscriberMutationBlocked}
                        onClick={() => {
                          setRemoveDraftError("")
                          setRemoveDraftOpen(true)
                        }}
                        size="sm"
                        variant="outline"
                      >
                        {intl.formatMessage({ id: "channelsBiz.members.removeMany" })}
                      </Button>
                    </div>
                  ) : null}
                </div>

                {isSubscriberMutationBlocked ? (
                  <p className="text-sm text-muted-foreground">
                    {intl.formatMessage({ id: "channelsBiz.members.personSubscriberBlocked" })}
                  </p>
                ) : null}

                <form className="flex flex-wrap gap-2" onSubmit={submitMemberSearch}>
                  <input
                    aria-label={intl.formatMessage({ id: "channelsBiz.members.searchLabel" })}
                    className="h-9 min-w-0 flex-1 rounded-md border border-border bg-background px-3 text-sm"
                    onChange={(event) => setMemberSearchInput(event.target.value)}
                    placeholder={intl.formatMessage({ id: "channelsBiz.members.searchPlaceholder" })}
                    value={memberSearchInput}
                  />
                  <Button size="sm" type="submit">
                    {intl.formatMessage({ id: "common.search" })}
                  </Button>
                  {activeMemberSearch ? (
                    <Button onClick={clearMemberSearch} size="sm" type="button" variant="outline">
                      {intl.formatMessage({ id: "channelsBiz.members.clearSearch" })}
                    </Button>
                  ) : null}
                </form>

                {memberNotice?.response ? (
                  <p className="rounded-md border border-border bg-muted/30 px-3 py-2 text-sm" role="status">
                    {intl.formatMessage(
                      { id: "channelsBiz.members.mutationResult" },
                      {
                        requested: memberNotice.response.requested_count,
                        changed: memberNotice.response.changed_count,
                      },
                    )}
                  </p>
                ) : null}
                {memberNotice?.error ? (
                  <p className="rounded-md border border-destructive/40 px-3 py-2 text-sm text-destructive" role="alert">
                    {intl.formatMessage(
                      { id: "channelsBiz.members.uncertainFailure" },
                      { error: memberNotice.error },
                    )}
                  </p>
                ) : null}

                {memberState.loading && memberState.items.length === 0 ? (
                  <ResourceState kind="loading" title={memberKindLabel(intl, activeMemberKind)} />
                ) : null}
                {!memberState.loading && memberState.error && memberState.items.length === 0 ? (
                  <ResourceState
                    kind={mapErrorKind(memberState.error)}
                    onRetry={() => {
                      if (activeMemberSearch) {
                        void loadMemberExact(selectedChannel, activeMemberKind, activeMemberSearch)
                      } else {
                        void loadMemberPage(selectedChannel, activeMemberKind)
                      }
                    }}
                    title={memberKindLabel(intl, activeMemberKind)}
                  />
                ) : null}
                {memberState.error && memberState.items.length > 0 ? (
                  <div className="rounded-md border border-destructive/40 px-3 py-2 text-sm text-destructive">
                    <span>{memberState.error.message}</span>
                    <Button
                      className="ml-3"
                      onClick={() => {
                        if (activeMemberSearch) {
                          void loadMemberExact(selectedChannel, activeMemberKind, activeMemberSearch, true)
                        } else {
                          void loadMemberPage(selectedChannel, activeMemberKind, {
                            cursor: memberState.currentCursor,
                            previousCursors: memberState.previousCursors,
                            preserve: true,
                          })
                        }
                      }}
                      size="sm"
                      variant="outline"
                    >
                      {intl.formatMessage({ id: "common.retry" })}
                    </Button>
                  </div>
                ) : null}

                {!memberState.loading || memberState.items.length > 0 ? (
                  activeMemberSearch && memberState.items.length === 0 && !memberState.error ? (
                    <div className="flex flex-wrap items-center justify-between gap-3 rounded-md border border-border px-3 py-4">
                      <p className="text-sm text-muted-foreground" role="status">
                        {intl.formatMessage(
                          { id: "channelsBiz.members.searchMiss" },
                          { uid: activeMemberSearch },
                        )}
                      </p>
                      {canWrite && !isSubscriberMutationBlocked ? (
                        <Button
                          onClick={() => {
                            setAddError("")
                            setAddUIDsInput(activeMemberSearch)
                            setAddOpen(true)
                          }}
                          size="sm"
                        >
                          {intl.formatMessage(
                            { id: "channelsBiz.members.addSearchMiss" },
                            { uid: activeMemberSearch },
                          )}
                        </Button>
                      ) : null}
                    </div>
                  ) : memberState.items.length > 0 ? (
                    <div className="overflow-x-auto rounded-md border border-border" data-channels-biz-surface="members">
                      {activeMemberSearch ? (
                        <p className="border-b border-border px-3 py-2 text-sm text-muted-foreground" role="status">
                          {intl.formatMessage(
                            { id: "channelsBiz.members.searchHit" },
                            { uid: activeMemberSearch },
                          )}
                        </p>
                      ) : null}
                      <table
                        aria-label={memberKindLabel(intl, activeMemberKind)}
                        className="w-full border-collapse text-sm"
                      >
                        <thead className="bg-muted/40 text-left text-xs uppercase tracking-[0.14em] text-muted-foreground">
                          <tr>
                            <th className="px-3 py-3">{intl.formatMessage({ id: "channelsBiz.members.uid" })}</th>
                            <th className="px-3 py-3">{intl.formatMessage({ id: "channelsBiz.table.actions" })}</th>
                          </tr>
                        </thead>
                        <tbody>
                          {memberState.items.map((member) => (
                            <tr className="border-t border-border" key={member.uid}>
                              <td className="px-3 py-3 font-medium">
                                <Link
                                  className="text-primary underline-offset-4 hover:underline"
                                  to={`/business/users?uid=${encodeURIComponent(member.uid)}`}
                                >
                                  {member.uid}
                                </Link>
                              </td>
                              <td className="px-3 py-3">
                                {canWrite ? (
                                  <Button
                                    aria-label={intl.formatMessage(
                                      { id: "channelsBiz.members.removeMember" },
                                      { uid: member.uid },
                                    )}
                                    disabled={isSubscriberMutationBlocked}
                                    onClick={() => {
                                      setRemoveError("")
                                      setRemoveUIDs([member.uid])
                                    }}
                                    size="sm"
                                    variant="outline"
                                  >
                                    {intl.formatMessage({ id: "channelsBiz.members.remove" })}
                                  </Button>
                                ) : (
                                  <span className="text-muted-foreground">-</span>
                                )}
                              </td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    </div>
                  ) : !memberState.error ? (
                    <ResourceState kind="empty" title={memberKindLabel(intl, activeMemberKind)} />
                  ) : null
                ) : null}

                {!activeMemberSearch && memberState.items.length > 0 ? (
                  <div className="flex items-center justify-between">
                    <Button
                      disabled={memberState.loading || memberState.previousCursors.length === 0}
                      onClick={() => {
                        const previousCursors = memberState.previousCursors.slice(0, -1)
                        void loadMemberPage(selectedChannel, activeMemberKind, {
                          cursor: memberState.previousCursors[memberState.previousCursors.length - 1],
                          previousCursors,
                          preserve: true,
                        })
                      }}
                      size="sm"
                      variant="outline"
                    >
                      {intl.formatMessage({ id: "channelsBiz.members.previous" })}
                    </Button>
                    <span className="text-xs text-muted-foreground">
                      {intl.formatMessage({ id: "channelsBiz.members.pageSize" }, { limit: memberPageLimit })}
                    </span>
                    <Button
                      disabled={memberState.loading || !memberState.hasMore || !memberState.nextCursor}
                      onClick={() => {
                        if (!memberState.nextCursor) {
                          return
                        }
                        void loadMemberPage(selectedChannel, activeMemberKind, {
                          cursor: memberState.nextCursor,
                          previousCursors: [
                            ...memberState.previousCursors,
                            memberState.currentCursor,
                          ],
                          preserve: true,
                        })
                      }}
                      size="sm"
                      variant="outline"
                    >
                      {intl.formatMessage({ id: "channelsBiz.members.next" })}
                    </Button>
                  </div>
                ) : null}
              </section>
            )}
          </div>
        ) : null}
      </DetailSheet>

      {canWrite ? (
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
              aria-label={intl.formatMessage({ id: "channelsBiz.form.channelId" })}
              className="mt-1 h-9 w-full rounded-md border border-border bg-background px-3"
              defaultValue={upsertInitial?.channel_id ?? ""}
              disabled={Boolean(upsertInitial)}
              name="channel_id"
            />
          </label>
          <label className="block text-sm">
            {intl.formatMessage({ id: "channelsBiz.form.channelType" })}
            <select
              aria-label={intl.formatMessage({ id: "channelsBiz.form.channelType" })}
              className="mt-1 h-9 w-full rounded-md border border-border bg-background px-2"
              defaultValue={upsertInitial?.channel_type ?? 2}
              disabled={Boolean(upsertInitial)}
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
      ) : null}

      {canWrite ? (
        <>
          <ActionFormDialog
            description={intl.formatMessage(
              { id: "channelsBiz.members.addDescription" },
              { list: memberKindLabel(intl, activeMemberKind) },
            )}
            error={addError}
            onOpenChange={(open) => {
              setAddOpen(open)
              if (!open) {
                setAddUIDsInput("")
              }
            }}
            onSubmit={submitAddMembers}
            open={addOpen}
            pending={addPending}
            submitLabel={intl.formatMessage({ id: "channelsBiz.members.add" })}
            title={intl.formatMessage({ id: "channelsBiz.members.add" })}
          >
            <label className="block text-sm">
              {intl.formatMessage({ id: "channelsBiz.members.uids" })}
              <textarea
                aria-label={intl.formatMessage({ id: "channelsBiz.members.uids" })}
                className="mt-1 min-h-28 w-full rounded-md border border-border bg-background px-3 py-2 text-sm"
                name="uids"
                onChange={(event) => setAddUIDsInput(event.target.value)}
                placeholder={intl.formatMessage({ id: "channelsBiz.members.uidsPlaceholder" })}
                value={addUIDsInput}
              />
            </label>
          </ActionFormDialog>

          <ActionFormDialog
            description={intl.formatMessage(
              { id: "channelsBiz.members.removeManyDescription" },
              { list: memberKindLabel(intl, activeMemberKind) },
            )}
            error={removeDraftError}
            onOpenChange={setRemoveDraftOpen}
            onSubmit={submitRemoveDraft}
            open={removeDraftOpen}
            submitLabel={intl.formatMessage({ id: "channelsBiz.members.reviewRemove" })}
            title={intl.formatMessage({ id: "channelsBiz.members.removeMany" })}
          >
            <label className="block text-sm">
              {intl.formatMessage({ id: "channelsBiz.members.uids" })}
              <textarea
                aria-label={intl.formatMessage({ id: "channelsBiz.members.removeUIDs" })}
                className="mt-1 min-h-28 w-full rounded-md border border-border bg-background px-3 py-2 text-sm"
                name="uids"
                placeholder={intl.formatMessage({ id: "channelsBiz.members.uidsPlaceholder" })}
              />
            </label>
          </ActionFormDialog>

          <ConfirmDialog
            confirmLabel={intl.formatMessage({ id: "channelsBiz.members.confirmRemove" })}
            description={
              selectedChannel && removeUIDs.length > 0
                ? intl.formatMessage(
                    { id: "channelsBiz.members.removeDescription" },
                    {
                      channel: `${selectedChannel.channelId} (${selectedChannel.channelType})`,
                      list: memberKindLabel(intl, activeMemberKind),
                      count: removeUIDs.length,
                      preview: `${removeUIDs.slice(0, 3).join(", ")}${removeUIDs.length > 3 ? "…" : ""}`,
                    },
                  )
                : undefined
            }
            error={removeError}
            onConfirm={() => void confirmRemoveMembers()}
            onOpenChange={(open) => {
              if (!open) {
                setRemoveUIDs([])
                setRemoveError("")
              }
            }}
            open={removeUIDs.length > 0}
            pending={removePending}
            title={intl.formatMessage({ id: "channelsBiz.members.remove" })}
          />
        </>
      ) : null}
    </PageContainer>
  )
}
