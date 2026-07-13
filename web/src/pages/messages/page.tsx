import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { useIntl } from "react-intl"
import { useSearchParams } from "react-router-dom"

import { ConfirmDialog } from "@/components/manager/confirm-dialog"
import { DetailSheet } from "@/components/manager/detail-sheet"
import { KeyValueList } from "@/components/manager/key-value-list"
import { ResourceState } from "@/components/manager/resource-state"
import { PageContainer } from "@/components/shell/page-container"
import { Button } from "@/components/ui/button"
import { ManagerApiError, advanceMessageRetention, getChannelRuntimeMeta, getMessages } from "@/lib/manager-api"
import type {
  AdvanceMessageRetentionResponse,
  ManagerChannelRuntimeMeta,
  ManagerMessage,
  ManagerMessagesResponse,
} from "@/lib/manager-api.types"
import { decodeManagerMessagePayload } from "@/lib/manager-message-payload"

type QueryForm = {
  channelId: string
  channelType: string
  messageId: string
  clientMsgNo: string
}

type SubmittedQuery = {
  channelId: string
  channelType: number
  messageId?: string
  clientMsgNo: string
}

type MessagesState = {
  messages: ManagerMessagesResponse | null
  loading: boolean
  refreshing: boolean
  loadingMore: boolean
  queried: boolean
  error: Error | null
}

type PayloadFormat = "text" | "base64"

type ChannelSuggestionState = {
  items: ManagerChannelRuntimeMeta[]
  loading: boolean
  error: boolean
}

type RetentionActionState = {
  message: ManagerMessage | null
  pending: boolean
  error: string | null
  result: AdvanceMessageRetentionResponse | null
}

const channelSuggestionLimit = 15
const maxUint64Decimal = "18446744073709551615"

const defaultQuery: QueryForm = {
  channelId: "",
  channelType: "1",
  messageId: "",
  clientMsgNo: "",
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

function formatTimestamp(timestamp: number) {
  if (!timestamp) {
    return "-"
  }
  return new Date(timestamp * 1000).toLocaleString()
}

function normalizeUint64Decimal(value: string) {
  if (!/^\d+$/.test(value)) {
    return null
  }
  const normalized = value.replace(/^0+/, "")
  if (!normalized) {
    return null
  }
  if (
    normalized.length > maxUint64Decimal.length
    || (normalized.length === maxUint64Decimal.length && normalized > maxUint64Decimal)
  ) {
    return null
  }
  return normalized
}

export function MessagesPage() {
  const intl = useIntl()
  const [searchParams] = useSearchParams()
  const initialChannelId = searchParams.get("channel_id")?.trim() ?? ""
  const initialChannelType = searchParams.get("channel_type")?.trim() || defaultQuery.channelType
  const [query, setQuery] = useState<QueryForm>(() => ({
    ...defaultQuery,
    channelId: initialChannelId,
    channelType: initialChannelType,
  }))
  const [submitted, setSubmitted] = useState<SubmittedQuery | null>(null)
  const [validationError, setValidationError] = useState<string | null>(null)
  const [selectedMessage, setSelectedMessage] = useState<ManagerMessage | null>(null)
  const [retention, setRetention] = useState<RetentionActionState>({
    message: null,
    pending: false,
    error: null,
    result: null,
  })
  const [payloadFormat, setPayloadFormat] = useState<PayloadFormat>("text")
  const [channelSuggestionsOpen, setChannelSuggestionsOpen] = useState(false)
  const [channelSuggestions, setChannelSuggestions] = useState<ChannelSuggestionState>({
    items: [],
    loading: false,
    error: false,
  })
  const autoQueryStartedRef = useRef(false)
  const messageRequestRef = useRef(0)
  const channelSuggestionRequestRef = useRef(0)
  const [state, setState] = useState<MessagesState>({
    messages: null,
    loading: false,
    refreshing: false,
    loadingMore: false,
    queried: false,
    error: null,
  })

  const runQuery = useCallback(async (nextQuery: SubmittedQuery, refreshing: boolean, cursor?: string) => {
    const requestID = messageRequestRef.current + 1
    messageRequestRef.current = requestID
    setState((current) => ({
      ...current,
      loading: !refreshing && !cursor,
      refreshing: refreshing && !cursor,
      loadingMore: Boolean(cursor),
      queried: true,
      error: null,
    }))

    try {
      const page = await getMessages({
        ...(nextQuery.channelId ? { channelId: nextQuery.channelId, channelType: nextQuery.channelType } : {}),
        limit: 50,
        cursor,
        messageId: nextQuery.messageId,
        clientMsgNo: nextQuery.clientMsgNo,
      })
      if (messageRequestRef.current !== requestID) {
        return
      }
      setState((current) => ({
        messages: cursor && current.messages
          ? {
              items: [...current.messages.items, ...page.items],
              has_more: page.has_more,
              next_cursor: page.next_cursor,
            }
          : page,
        loading: false,
        refreshing: false,
        loadingMore: false,
        queried: true,
        error: null,
      }))
    } catch (error) {
      if (messageRequestRef.current !== requestID) {
        return
      }
      setState((current) => ({
        ...current,
        messages: cursor ? current.messages : null,
        loading: false,
        refreshing: false,
        loadingMore: false,
        queried: true,
        error: error instanceof Error ? error : new Error("message query failed"),
      }))
    }
  }, [])

  useEffect(() => {
    if (autoQueryStartedRef.current) {
      return
    }
    autoQueryStartedRef.current = true

    const channelId = initialChannelId.trim()
    const channelType = Number(initialChannelType)

    if (!channelId) {
      const nextQuery: SubmittedQuery = { channelId: "", channelType: 0, clientMsgNo: "" }
      setValidationError(null)
      setSubmitted(nextQuery)
      void runQuery(nextQuery, false)
      return
    }
    if (!Number.isInteger(channelType) || channelType <= 0) {
      return
    }

    const nextQuery: SubmittedQuery = {
      channelId,
      channelType,
      clientMsgNo: "",
    }
    setValidationError(null)
    setSubmitted(nextQuery)
    void runQuery(nextQuery, false)
  }, [initialChannelId, initialChannelType, runQuery])

  useEffect(() => {
    const channelId = query.channelId.trim()
    const requestID = channelSuggestionRequestRef.current + 1
    channelSuggestionRequestRef.current = requestID

    if (!channelSuggestionsOpen || !channelId) {
      setChannelSuggestions({ items: [], loading: false, error: false })
      return
    }

    setChannelSuggestions((current) => ({ ...current, loading: true, error: false }))
    const timeoutID = window.setTimeout(() => {
      void getChannelRuntimeMeta({ channelId, limit: channelSuggestionLimit })
        .then((page) => {
          if (channelSuggestionRequestRef.current !== requestID) {
            return
          }
          setChannelSuggestions({
            items: page.items.slice(0, channelSuggestionLimit),
            loading: false,
            error: false,
          })
        })
        .catch(() => {
          if (channelSuggestionRequestRef.current !== requestID) {
            return
          }
          setChannelSuggestions({ items: [], loading: false, error: true })
        })
    }, 200)
    return () => {
      window.clearTimeout(timeoutID)
    }
  }, [channelSuggestionsOpen, query.channelId])

  const openDetail = useCallback((message: ManagerMessage) => {
    setSelectedMessage(message)
    setPayloadFormat("text")
  }, [])

  const closeDetail = useCallback((open: boolean) => {
    if (!open) {
      setSelectedMessage(null)
      setPayloadFormat("text")
    }
  }, [])

  const openRetentionDialog = useCallback((message: ManagerMessage) => {
    setRetention((current) => ({
      ...current,
      message,
      pending: false,
      error: null,
    }))
  }, [])

  const closeRetentionDialog = useCallback((open: boolean) => {
    if (open) {
      return
    }
    setRetention((current) => {
      if (current.pending) {
        return current
      }
      return {
        ...current,
        message: null,
        error: null,
      }
    })
  }, [])

  const submit = useCallback(async () => {
    const channelId = query.channelId.trim()
    if (!channelId) {
      setValidationError(intl.formatMessage({ id: "messages.validation.channelId" }))
      return
    }
    const channelType = Number(query.channelType)
    if (!Number.isInteger(channelType) || channelType <= 0) {
      setValidationError(intl.formatMessage({ id: "messages.validation.channelType" }))
      return
    }
    const trimmedMessageId = query.messageId.trim()
    let messageId: string | undefined
    if (trimmedMessageId) {
      messageId = normalizeUint64Decimal(trimmedMessageId) ?? undefined
      if (!messageId) {
        setValidationError(intl.formatMessage({ id: "messages.validation.messageId" }))
        return
      }
    }

    const nextQuery: SubmittedQuery = {
      channelId,
      channelType,
      clientMsgNo: query.clientMsgNo.trim(),
      ...(messageId ? { messageId } : {}),
    }
    setValidationError(null)
    setChannelSuggestionsOpen(false)
    setSubmitted(nextQuery)
    await runQuery(nextQuery, false)
  }, [intl, query, runQuery])

  const loadMore = useCallback(async () => {
    if (!submitted || !state.messages?.next_cursor) {
      return
    }
    await runQuery(submitted, false, state.messages.next_cursor)
  }, [runQuery, state.messages?.next_cursor, submitted])

  const refresh = useCallback(async () => {
    if (!submitted) {
      return
    }
    await runQuery(submitted, true)
  }, [runQuery, submitted])

  const confirmRetentionDelete = useCallback(async () => {
    const message = retention.message
    if (!message || retention.pending) {
      return
    }

    setRetention((current) => ({ ...current, pending: true, error: null }))
    try {
      const result = await advanceMessageRetention({
        channelId: message.channel_id,
        channelType: message.channel_type,
        throughSeq: message.message_seq,
      })

      if (result.status === "blocked") {
        setRetention((current) => ({
          ...current,
          pending: false,
          error: intl.formatMessage(
            { id: "messages.retentionBlocked" },
            { reason: result.blocked_reason || result.status },
          ),
          result,
        }))
        return
      }

      setRetention((current) => ({
        ...current,
        message: null,
        pending: false,
        error: null,
        result,
      }))
      await refresh()
    } catch (error) {
      setRetention((current) => ({
        ...current,
        pending: false,
        error: error instanceof Error ? error.message : "message retention failed",
      }))
    }
  }, [intl, refresh, retention.message, retention.pending])

  const visiblePayload = useMemo(() => {
    if (!selectedMessage) {
      return ""
    }
    return payloadFormat === "base64"
      ? selectedMessage.payload
      : decodeManagerMessagePayload(selectedMessage.payload)
  }, [payloadFormat, selectedMessage])

  return (
    <PageContainer>
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <div className="font-mono text-[11px] font-semibold uppercase tracking-[0.22em] text-muted-foreground">
            {intl.formatMessage({ id: "nav.path.business.messages" })}
          </div>
          <h1 className="text-xl font-semibold tracking-tight text-foreground">
            {intl.formatMessage({ id: "nav.messages.title" })}
          </h1>
          <div className="mt-1 flex flex-wrap gap-3 text-sm text-muted-foreground">
            <span>
              {submitted
                ? submitted.channelId
                  ? intl.formatMessage({ id: "messages.scopeValue" }, { id: submitted.channelId })
                  : intl.formatMessage({ id: "messages.scopeLatest" })
                : intl.formatMessage({ id: "messages.scopePending" })}
            </span>
            <span>
              {state.messages
                ? intl.formatMessage({ id: "messages.loadedValue" }, { count: state.messages.items.length })
                : intl.formatMessage({ id: "messages.loadedPending" })}
            </span>
          </div>
        </div>
        {submitted ? (
          <Button
            onClick={() => {
              void refresh()
            }}
            size="sm"
            variant="outline"
          >
            {state.refreshing
              ? intl.formatMessage({ id: "common.refreshing" })
              : intl.formatMessage({ id: "common.refresh" })}
          </Button>
        ) : null}
      </div>

      <div
        className="rounded-lg border border-border bg-card p-3 shadow-none"
        data-messages-surface="query"
        data-testid="messages-query-surface"
      >
        <div
          className="grid gap-3 md:grid-cols-2 xl:grid-cols-[minmax(0,1.2fr)_10rem_10rem_minmax(0,1fr)_auto] xl:items-end"
          data-testid="messages-query-toolbar"
        >
          <label className="relative grid gap-2 text-sm text-foreground">
            <span>{intl.formatMessage({ id: "messages.form.channelId" })}</span>
            <input
              aria-autocomplete="list"
              aria-controls="message-channel-suggestions"
              aria-expanded={channelSuggestionsOpen && query.channelId.trim() !== ""}
              aria-label={intl.formatMessage({ id: "messages.form.channelId" })}
              autoComplete="off"
              className="min-h-10 rounded-md border border-input bg-background px-3 py-2 text-sm"
              onChange={(event) => {
                setQuery((current) => ({ ...current, channelId: event.target.value }))
              }}
              onBlur={() => {
                window.setTimeout(() => {
                  setChannelSuggestionsOpen(false)
                }, 100)
              }}
              onFocus={() => {
                setChannelSuggestionsOpen(true)
              }}
              value={query.channelId}
            />
            {channelSuggestionsOpen && query.channelId.trim() ? (
              <div
                className="absolute top-full right-0 left-0 z-20 mt-1 overflow-hidden rounded-lg border border-border bg-popover text-popover-foreground shadow-lg"
                id="message-channel-suggestions"
                role="listbox"
              >
                {channelSuggestions.loading ? (
                  <div className="px-3 py-2 text-xs text-muted-foreground">
                    {intl.formatMessage({ id: "messages.channelSuggestions.loading" })}
                  </div>
                ) : null}
                {!channelSuggestions.loading && channelSuggestions.error ? (
                  <div className="px-3 py-2 text-xs text-muted-foreground">
                    {intl.formatMessage({ id: "messages.channelSuggestions.error" })}
                  </div>
                ) : null}
                {!channelSuggestions.loading && !channelSuggestions.error && channelSuggestions.items.length === 0 ? (
                  <div className="px-3 py-2 text-xs text-muted-foreground">
                    {intl.formatMessage({ id: "messages.channelSuggestions.empty" })}
                  </div>
                ) : null}
                {!channelSuggestions.loading && !channelSuggestions.error
                  ? channelSuggestions.items.map((channel) => (
                      <button
                        aria-selected={false}
                        className="flex w-full items-center justify-between gap-3 px-3 py-2 text-left text-sm hover:bg-muted focus:bg-muted focus:outline-none"
                        key={`${channel.channel_type}-${channel.channel_id}`}
                        onClick={() => {
                          setQuery((current) => ({
                            ...current,
                            channelId: channel.channel_id,
                            channelType: String(channel.channel_type),
                          }))
                          setChannelSuggestionsOpen(false)
                        }}
                        onMouseDown={(event) => {
                          event.preventDefault()
                        }}
                        role="option"
                        type="button"
                      >
                        <span className="truncate font-medium text-foreground">{channel.channel_id}</span>
                        <span className="shrink-0 text-xs text-muted-foreground">
                          {intl.formatMessage(
                            { id: "messages.channelSuggestions.type" },
                            { type: channel.channel_type },
                          )}
                        </span>
                      </button>
                    ))
                  : null}
              </div>
            ) : null}
          </label>
          <label className="grid gap-2 text-sm text-foreground">
            <span>{intl.formatMessage({ id: "messages.form.channelType" })}</span>
            <input
              aria-label={intl.formatMessage({ id: "messages.form.channelType" })}
              className="min-h-10 rounded-md border border-input bg-background px-3 py-2 text-sm"
              onChange={(event) => {
                setQuery((current) => ({ ...current, channelType: event.target.value }))
              }}
              type="number"
              value={query.channelType}
            />
          </label>
          <label className="grid gap-2 text-sm text-foreground">
            <span>{intl.formatMessage({ id: "messages.form.messageId" })}</span>
            <input
              aria-label={intl.formatMessage({ id: "messages.form.messageId" })}
              className="min-h-10 rounded-md border border-input bg-background px-3 py-2 text-sm"
              onChange={(event) => {
                setQuery((current) => ({ ...current, messageId: event.target.value }))
              }}
              inputMode="numeric"
              pattern="[0-9]*"
              type="text"
              value={query.messageId}
            />
          </label>
          <label className="grid gap-2 text-sm text-foreground">
            <span>{intl.formatMessage({ id: "messages.form.clientMsgNo" })}</span>
            <input
              aria-label={intl.formatMessage({ id: "messages.form.clientMsgNo" })}
              className="min-h-10 rounded-md border border-input bg-background px-3 py-2 text-sm"
              onChange={(event) => {
                setQuery((current) => ({ ...current, clientMsgNo: event.target.value }))
              }}
              value={query.clientMsgNo}
            />
          </label>
          <div className="flex flex-wrap items-center gap-3">
            <Button
              onClick={() => {
                void submit()
              }}
            >
              {intl.formatMessage({ id: "common.search" })}
            </Button>
          </div>
        </div>
        {validationError ? <p className="mt-3 text-sm text-destructive">{validationError}</p> : null}
      </div>

      <div className="rounded-lg border border-border bg-card p-3 shadow-none" data-messages-surface="inventory">
        {retention.result && retention.result.status !== "blocked" ? (
          <div className="mb-3 rounded-md border border-border bg-muted/30 px-3 py-2 text-sm text-foreground">
            {intl.formatMessage(
              { id: "messages.retentionSuccess" },
              { seq: retention.result.min_available_seq },
            )}
          </div>
        ) : null}
        {state.loading ? <ResourceState kind="loading" title={intl.formatMessage({ id: "nav.messages.title" })} /> : null}
        {!state.loading && state.error ? (
          <ResourceState
            kind={mapErrorKind(state.error)}
            onRetry={submitted ? () => {
              void refresh()
            } : undefined}
            title={intl.formatMessage({ id: "nav.messages.title" })}
          />
        ) : null}
        {!state.loading && !state.error && !state.queried ? (
          <ResourceState kind="empty" title={intl.formatMessage({ id: "nav.messages.title" })} />
        ) : null}
        {!state.loading && !state.error && state.messages ? (
          state.messages.items.length > 0 ? (
            <>
              <div className="overflow-x-auto rounded-md border border-border">
                <table
                  aria-label={intl.formatMessage({ id: "nav.messages.title" })}
                  className="w-full min-w-[100.5rem] table-fixed border-collapse text-sm"
                >
                  <colgroup>
                    <col className="w-[4.5rem]" />
                    <col className="w-[14rem]" />
                    <col className="w-[12rem]" />
                    <col className="w-[22rem]" />
                    <col className="w-[9rem]" />
                    <col className="w-[10rem]" />
                    <col className="w-[20rem]" />
                    <col className="w-[9rem]" />
                  </colgroup>
                  <thead className="bg-muted/40 text-left text-xs uppercase tracking-[0.14em] text-muted-foreground">
                    <tr>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "messages.table.seq" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "messages.table.channel" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "messages.table.messageId" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "messages.table.clientMsgNo" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "messages.table.fromUid" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "messages.table.timestamp" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "messages.table.payload" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "messages.table.actions" })}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {state.messages.items.map((message) => (
                      <tr className="border-t border-border" key={`${message.channel_type}-${message.channel_id}-${message.message_seq}-${message.message_id}`}>
                        <td className="px-3 py-3 text-sm text-muted-foreground">{message.message_seq}</td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">
                          <span className="font-medium text-foreground">{message.channel_id}</span>
                          <span className="ml-2 text-xs">· {message.channel_type}</span>
                        </td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">{message.message_id}</td>
                        <td className="px-3 py-3 text-sm font-medium text-foreground">{message.client_msg_no || "-"}</td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">{message.from_uid || "-"}</td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">{formatTimestamp(message.timestamp)}</td>
                        <td className="px-3 py-3 text-sm text-foreground">
                          <div className="truncate">{decodeManagerMessagePayload(message.payload) || "-"}</div>
                        </td>
                        <td className="px-3 py-3 text-sm text-foreground">
                          <div className="flex flex-wrap gap-2">
                            <Button
                              aria-label={intl.formatMessage(
                                { id: "messages.deleteThroughSeqAction" },
                                { seq: message.message_seq },
                              )}
                              onClick={() => {
                                openRetentionDialog(message)
                              }}
                              size="sm"
                              variant="destructive"
                            >
                              {intl.formatMessage({ id: "messages.deleteThroughSeq" })}
                            </Button>
                            <Button
                              aria-label={intl.formatMessage(
                                { id: "messages.inspectMessage" },
                                { id: message.message_id },
                              )}
                              onClick={() => {
                                openDetail(message)
                              }}
                              size="sm"
                              variant="outline"
                            >
                              {intl.formatMessage({ id: "common.inspect" })}
                            </Button>
                          </div>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
              {state.messages.has_more ? (
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
            <ResourceState kind="empty" title={intl.formatMessage({ id: "nav.messages.title" })} />
          )
        ) : null}
      </div>

      <DetailSheet
        description={
          selectedMessage
            ? intl.formatMessage({ id: "messages.detailDescriptionValue" }, { id: selectedMessage.channel_id })
            : intl.formatMessage({ id: "messages.detailDescriptionFallback" })
        }
        onOpenChange={closeDetail}
        open={selectedMessage !== null}
        title={
          selectedMessage
            ? intl.formatMessage({ id: "messages.detailTitleValue" }, { id: selectedMessage.message_id })
            : intl.formatMessage({ id: "messages.detailTitleFallback" })
        }
      >
        {selectedMessage ? (
          <div className="space-y-4">
            <KeyValueList
              items={[
                {
                  label: intl.formatMessage({ id: "messages.detail.messageId" }),
                  value: selectedMessage.message_id,
                },
                {
                  label: intl.formatMessage({ id: "messages.detail.messageSeq" }),
                  value: selectedMessage.message_seq,
                },
                {
                  label: intl.formatMessage({ id: "messages.detail.channelId" }),
                  value: selectedMessage.channel_id,
                },
                {
                  label: intl.formatMessage({ id: "messages.detail.channelType" }),
                  value: selectedMessage.channel_type,
                },
                {
                  label: intl.formatMessage({ id: "messages.detail.fromUid" }),
                  value: selectedMessage.from_uid || "-",
                },
                {
                  label: intl.formatMessage({ id: "messages.detail.clientMsgNo" }),
                  value: selectedMessage.client_msg_no || "-",
                },
                {
                  label: intl.formatMessage({ id: "messages.detail.timestamp" }),
                  value: formatTimestamp(selectedMessage.timestamp),
                },
              ]}
            />

            <section className="rounded-md border border-border bg-card">
              <div className="flex flex-wrap items-start justify-between gap-3 border-b border-border px-4 py-4">
                <div>
                  <h3 className="text-sm font-medium text-foreground">
                    {intl.formatMessage({ id: "messages.payloadTitle" })}
                  </h3>
                  <p className="mt-1 text-sm text-muted-foreground">
                    {intl.formatMessage({ id: "messages.payloadDescription" })}
                  </p>
                </div>
                <div className="flex flex-wrap items-center gap-2">
                  <Button
                    onClick={() => {
                      setPayloadFormat("text")
                    }}
                    size="sm"
                    variant={payloadFormat === "text" ? "default" : "outline"}
                  >
                    {intl.formatMessage({ id: "messages.payloadText" })}
                  </Button>
                  <Button
                    onClick={() => {
                      setPayloadFormat("base64")
                    }}
                    size="sm"
                    variant={payloadFormat === "base64" ? "default" : "outline"}
                  >
                    {intl.formatMessage({ id: "messages.payloadBase64" })}
                  </Button>
                  <Button
                    onClick={() => {
                      const clipboard = window.navigator.clipboard
                      if (typeof clipboard?.writeText === "function") {
                        void clipboard.writeText(visiblePayload)
                      }
                    }}
                    size="sm"
                    variant="outline"
                  >
                    {intl.formatMessage({ id: "messages.copyPayload" })}
                  </Button>
                </div>
              </div>
              <pre className="overflow-x-auto px-4 py-4 text-sm whitespace-pre-wrap text-foreground">
                {visiblePayload || "-"}
              </pre>
            </section>
          </div>
        ) : null}
      </DetailSheet>
      <ConfirmDialog
        confirmLabel={intl.formatMessage({ id: "common.confirm" })}
        description={retention.message
          ? intl.formatMessage(
              { id: "messages.deleteConfirmDescription" },
              {
                channel: retention.message.channel_id,
                type: retention.message.channel_type,
                seq: retention.message.message_seq,
              },
            )
          : undefined}
        error={retention.error ?? undefined}
        onConfirm={() => {
          void confirmRetentionDelete()
        }}
        onOpenChange={closeRetentionDialog}
        open={retention.message !== null}
        pending={retention.pending}
        title={intl.formatMessage({ id: "messages.deleteConfirmTitle" })}
      />
    </PageContainer>
  )
}
