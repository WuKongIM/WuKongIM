import { useCallback, useMemo, useState } from "react"
import { useIntl } from "react-intl"

import { DetailSheet } from "@/components/manager/detail-sheet"
import { KeyValueList } from "@/components/manager/key-value-list"
import { ResourceState } from "@/components/manager/resource-state"
import { TableToolbar } from "@/components/manager/table-toolbar"
import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
import { SectionCard } from "@/components/shell/section-card"
import { Button } from "@/components/ui/button"
import { ManagerApiError, getMessages } from "@/lib/manager-api"
import type { ManagerMessage, ManagerMessagesResponse } from "@/lib/manager-api.types"

type QueryForm = {
  channelId: string
  channelType: string
  messageId: string
  clientMsgNo: string
}

type SubmittedQuery = {
  channelId: string
  channelType: number
  messageId?: number
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

function decodePayload(value: string) {
  if (!value) {
    return ""
  }
  try {
    const binary = atob(value)
    const bytes = Uint8Array.from(binary, (char) => char.charCodeAt(0))
    const decoded = new TextDecoder().decode(bytes)
    const printable = /^[\x09\x0A\x0D\x20-\x7E]*$/.test(decoded)
    return printable ? decoded : value
  } catch {
    return value
  }
}

function formatTimestamp(timestamp: number) {
  if (!timestamp) {
    return "-"
  }
  return new Date(timestamp * 1000).toLocaleString()
}

export function MessagesPage() {
  const intl = useIntl()
  const [query, setQuery] = useState<QueryForm>(defaultQuery)
  const [submitted, setSubmitted] = useState<SubmittedQuery | null>(null)
  const [validationError, setValidationError] = useState<string | null>(null)
  const [selectedMessage, setSelectedMessage] = useState<ManagerMessage | null>(null)
  const [payloadFormat, setPayloadFormat] = useState<PayloadFormat>("text")
  const [state, setState] = useState<MessagesState>({
    messages: null,
    loading: false,
    refreshing: false,
    loadingMore: false,
    queried: false,
    error: null,
  })

  const runQuery = useCallback(async (nextQuery: SubmittedQuery, refreshing: boolean, cursor?: string) => {
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
        channelId: nextQuery.channelId,
        channelType: nextQuery.channelType,
        limit: 50,
        cursor,
        messageId: nextQuery.messageId,
        clientMsgNo: nextQuery.clientMsgNo,
      })
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
    let messageId: number | undefined
    if (trimmedMessageId) {
      messageId = Number(trimmedMessageId)
      if (!Number.isInteger(messageId) || messageId <= 0) {
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

  const summary = useMemo(() => {
    const items = state.messages?.items ?? []
    return {
      total: items.length,
      senders: new Set(items.map((item) => item.from_uid)).size,
    }
  }, [state.messages])

  const visiblePayload = useMemo(() => {
    if (!selectedMessage) {
      return ""
    }
    return payloadFormat === "base64"
      ? selectedMessage.payload
      : decodePayload(selectedMessage.payload)
  }, [payloadFormat, selectedMessage])

  return (
    <PageContainer>
      <PageHeader
        title={intl.formatMessage({ id: "nav.messages.title" })}
        description={intl.formatMessage({ id: "nav.messages.description" })}
      >
        <div className="flex flex-wrap gap-2 text-xs text-muted-foreground">
          <div className="rounded-md border border-border bg-background px-3 py-2">
            {submitted
              ? intl.formatMessage({ id: "messages.scopeValue" }, { id: submitted.channelId })
              : intl.formatMessage({ id: "messages.scopePending" })}
          </div>
          <div className="rounded-md border border-border bg-background px-3 py-2">
            {state.messages
              ? intl.formatMessage({ id: "messages.loadedValue" }, { count: state.messages.items.length })
              : intl.formatMessage({ id: "messages.loadedPending" })}
          </div>
        </div>
      </PageHeader>

      <SectionCard
        description={intl.formatMessage({ id: "messages.queryDescription" })}
        title={intl.formatMessage({ id: "messages.queryTitle" })}
      >
        <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-4">
          <label className="grid gap-2 text-sm text-foreground">
            <span>{intl.formatMessage({ id: "messages.form.channelId" })}</span>
            <input
              aria-label={intl.formatMessage({ id: "messages.form.channelId" })}
              className="min-h-10 rounded-md border border-input bg-background px-3 py-2 text-sm"
              onChange={(event) => {
                setQuery((current) => ({ ...current, channelId: event.target.value }))
              }}
              value={query.channelId}
            />
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
              type="number"
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
        </div>
        <div className="mt-4 flex flex-wrap items-center gap-3">
          <Button onClick={() => { void submit() }}>
            {intl.formatMessage({ id: "common.search" })}
          </Button>
          {validationError ? <p className="text-sm text-destructive">{validationError}</p> : null}
        </div>
      </SectionCard>

      {state.messages ? (
        <section className="grid gap-3 md:grid-cols-2 xl:grid-cols-2">
          <SectionCard
            description={intl.formatMessage({ id: "messages.cards.loaded.description" })}
            title={intl.formatMessage({ id: "messages.cards.loaded.title" })}
          >
            <div className="text-3xl font-semibold text-foreground">{summary.total}</div>
          </SectionCard>
          <SectionCard
            description={intl.formatMessage({ id: "messages.cards.senders.description" })}
            title={intl.formatMessage({ id: "messages.cards.senders.title" })}
          >
            <div className="text-3xl font-semibold text-foreground">{summary.senders}</div>
          </SectionCard>
        </section>
      ) : null}

      <SectionCard
        description={intl.formatMessage({ id: "messages.resultsDescription" })}
        title={intl.formatMessage({ id: "messages.resultsTitle" })}
      >
        <TableToolbar
          actions={
            state.messages?.has_more ? (
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
            ) : null
          }
          description={intl.formatMessage({ id: "messages.toolbarDescription" })}
          onRefresh={submitted ? () => { void refresh() } : undefined}
          refreshing={state.refreshing}
          title={intl.formatMessage({ id: "messages.toolbarTitle" })}
        />

        {state.loading ? <ResourceState kind="loading" title={intl.formatMessage({ id: "messages.toolbarTitle" })} /> : null}
        {!state.loading && state.error ? (
          <ResourceState
            kind={mapErrorKind(state.error)}
            onRetry={submitted ? () => { void refresh() } : undefined}
            title={intl.formatMessage({ id: "messages.toolbarTitle" })}
          />
        ) : null}
        {!state.loading && !state.error && !state.queried ? (
          <ResourceState kind="empty" title={intl.formatMessage({ id: "messages.toolbarTitle" })} />
        ) : null}
        {!state.loading && !state.error && state.messages ? (
          state.messages.items.length > 0 ? (
            <div className="overflow-x-auto rounded-lg border border-border">
              <table className="w-full border-collapse">
                <thead className="bg-muted/40 text-left text-xs uppercase tracking-[0.14em] text-muted-foreground">
                  <tr>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "messages.table.seq" })}</th>
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
                    <tr className="border-t border-border" key={`${message.message_seq}-${message.message_id}`}>
                      <td className="px-3 py-3 text-sm text-muted-foreground">{message.message_seq}</td>
                      <td className="px-3 py-3 text-sm text-muted-foreground">{message.message_id}</td>
                      <td className="px-3 py-3 text-sm font-medium text-foreground">{message.client_msg_no || "-"}</td>
                      <td className="px-3 py-3 text-sm text-muted-foreground">{message.from_uid || "-"}</td>
                      <td className="px-3 py-3 text-sm text-muted-foreground">{formatTimestamp(message.timestamp)}</td>
                      <td className="max-w-[24rem] px-3 py-3 text-sm text-foreground">{decodePayload(message.payload) || "-"}</td>
                      <td className="px-3 py-3 text-sm text-foreground">
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
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ) : (
            <ResourceState kind="empty" title={intl.formatMessage({ id: "messages.toolbarTitle" })} />
          )
        ) : null}
      </SectionCard>

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

            <section className="rounded-lg border border-border bg-card">
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
    </PageContainer>
  )
}
