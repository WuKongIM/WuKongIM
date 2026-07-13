import type { FormEvent } from "react"
import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { useIntl } from "react-intl"
import { Link, useSearchParams } from "react-router-dom"

import { ResourceState } from "@/components/manager/resource-state"
import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
import { SectionCard } from "@/components/shell/section-card"
import { Button } from "@/components/ui/button"
import { ManagerApiError, getRecentConversations } from "@/lib/manager-api"
import type { ManagerRecentConversationsResponse } from "@/lib/manager-api.types"
import { decodeManagerMessagePayload } from "@/lib/manager-message-payload"

type ConversationQuery = {
  uid: string
  limit: string
  msgCount: string
  onlyUnread: boolean
}

type SubmittedConversationQuery = {
  uid: string
  limit: number
  msgCount: number
  onlyUnread: boolean
}

type ConversationsState = {
  data: ManagerRecentConversationsResponse | null
  loading: boolean
  refreshing: boolean
  queried: boolean
  error: Error | null
}

const defaultQuery: ConversationQuery = {
  uid: "",
  limit: "50",
  msgCount: "1",
  onlyUnread: false,
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

function firstPreview(page: ManagerRecentConversationsResponse["items"][number]) {
  const message = page.recent_messages[0]
  return message ? decodeManagerMessagePayload(message.payload) || "-" : "-"
}

function messagesHref(channelId: string, channelType: number) {
  return `/business/messages?channel_id=${encodeURIComponent(channelId)}&channel_type=${channelType}`
}

export function ConversationsPage() {
  const intl = useIntl()
  const [searchParams] = useSearchParams()
  const initialUID = searchParams.get("uid")?.trim() ?? ""
  const initialOnlyUnread = searchParams.get("only_unread") === "true" || searchParams.get("only_unread") === "1"
  const [query, setQuery] = useState<ConversationQuery>(() => ({
    ...defaultQuery,
    uid: initialUID,
    onlyUnread: initialOnlyUnread,
  }))
  const [submitted, setSubmitted] = useState<SubmittedConversationQuery | null>(null)
  const [validationError, setValidationError] = useState<string | null>(null)
  const autoQueryStartedRef = useRef(false)
  const [state, setState] = useState<ConversationsState>({
    data: null,
    loading: false,
    refreshing: false,
    queried: false,
    error: null,
  })

  const runQuery = useCallback(async (nextQuery: SubmittedConversationQuery, refreshing = false) => {
    setState((current) => ({
      ...current,
      loading: !refreshing,
      refreshing,
      queried: true,
      error: null,
    }))
    try {
      const data = await getRecentConversations({
        uid: nextQuery.uid,
        limit: nextQuery.limit,
        msgCount: nextQuery.msgCount,
        onlyUnread: nextQuery.onlyUnread,
      })
      setState({ data, loading: false, refreshing: false, queried: true, error: null })
    } catch (error) {
      setState({
        data: null,
        loading: false,
        refreshing: false,
        queried: true,
        error: error instanceof Error ? error : new Error("recent conversations request failed"),
      })
    }
  }, [])

  const parseQuery = useCallback((): SubmittedConversationQuery | null => {
    const uid = query.uid.trim()
    if (!uid) {
      setValidationError(intl.formatMessage({ id: "conversations.validation.uid" }))
      return null
    }
    const limit = Number(query.limit)
    if (!Number.isInteger(limit) || limit <= 0 || limit > 200) {
      setValidationError(intl.formatMessage({ id: "conversations.validation.limit" }))
      return null
    }
    const msgCount = Number(query.msgCount)
    if (!Number.isInteger(msgCount) || msgCount < 0 || msgCount > 10) {
      setValidationError(intl.formatMessage({ id: "conversations.validation.msgCount" }))
      return null
    }
    setValidationError(null)
    return { uid, limit, msgCount, onlyUnread: query.onlyUnread }
  }, [intl, query])

  useEffect(() => {
    if (autoQueryStartedRef.current) {
      return
    }
    autoQueryStartedRef.current = true
    if (!initialUID) {
      return
    }
    const nextQuery = { uid: initialUID, limit: 50, msgCount: 1, onlyUnread: initialOnlyUnread }
    setSubmitted(nextQuery)
    void runQuery(nextQuery)
  }, [initialOnlyUnread, initialUID, runQuery])

  const submitSearch = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const nextQuery = parseQuery()
    if (!nextQuery) {
      return
    }
    setSubmitted(nextQuery)
    void runQuery(nextQuery)
  }

  const refresh = () => {
    if (!submitted) {
      return
    }
    void runQuery(submitted, true)
  }

  const summary = useMemo(() => {
    if (!submitted) {
      return intl.formatMessage({ id: "conversations.summary.scopePending" })
    }
    return intl.formatMessage({ id: "conversations.summary.scopeValue" }, { uid: submitted.uid })
  }, [intl, submitted])

  return (
    <PageContainer>
      <PageHeader
        description={intl.formatMessage({ id: "conversations.description" })}
        title={intl.formatMessage({ id: "conversations.title" })}
      />

      <SectionCard className="overflow-hidden" description={summary} title={intl.formatMessage({ id: "conversations.title" })}>
        <form
          className="grid gap-3 border-b border-border pb-4 md:grid-cols-2 xl:grid-cols-[minmax(0,1fr)_8rem_8rem_auto_auto] xl:items-end"
          data-testid="conversations-query-toolbar"
          onSubmit={submitSearch}
        >
          <label className="grid gap-2 text-sm text-foreground">
            <span>{intl.formatMessage({ id: "conversations.form.uid" })}</span>
            <input
              className="h-9 rounded-md border border-border bg-background px-3 text-sm outline-none focus:ring-2 focus:ring-ring"
              onChange={(event) => setQuery((current) => ({ ...current, uid: event.target.value }))}
              placeholder={intl.formatMessage({ id: "conversations.form.uidPlaceholder" })}
              value={query.uid}
            />
          </label>
          <label className="grid gap-2 text-sm text-foreground">
            <span>{intl.formatMessage({ id: "conversations.form.limit" })}</span>
            <input
              aria-label={intl.formatMessage({ id: "conversations.form.limit" })}
              className="h-9 rounded-md border border-border bg-background px-3 text-sm outline-none focus:ring-2 focus:ring-ring"
              onChange={(event) => setQuery((current) => ({ ...current, limit: event.target.value }))}
              type="number"
              value={query.limit}
            />
          </label>
          <label className="grid gap-2 text-sm text-foreground">
            <span>{intl.formatMessage({ id: "conversations.form.msgCount" })}</span>
            <input
              aria-label={intl.formatMessage({ id: "conversations.form.msgCount" })}
              className="h-9 rounded-md border border-border bg-background px-3 text-sm outline-none focus:ring-2 focus:ring-ring"
              onChange={(event) => setQuery((current) => ({ ...current, msgCount: event.target.value }))}
              type="number"
              value={query.msgCount}
            />
          </label>
          <label className="flex h-9 items-center gap-2 text-sm text-foreground md:mb-0">
            <input
              checked={query.onlyUnread}
              onChange={(event) => setQuery((current) => ({ ...current, onlyUnread: event.target.checked }))}
              type="checkbox"
            />
            {intl.formatMessage({ id: "conversations.form.onlyUnread" })}
          </label>
          <div className="flex flex-wrap items-center gap-2">
            <Button disabled={state.loading || state.refreshing} type="submit">
              {state.loading ? intl.formatMessage({ id: "common.loading" }) : intl.formatMessage({ id: "common.search" })}
            </Button>
            {submitted ? (
              <Button disabled={state.loading || state.refreshing} onClick={refresh} type="button" variant="outline">
                {state.refreshing ? intl.formatMessage({ id: "common.refreshing" }) : intl.formatMessage({ id: "common.refresh" })}
              </Button>
            ) : null}
          </div>
        </form>

        {validationError ? <p className="mb-3 text-sm text-destructive">{validationError}</p> : null}
        {state.data ? (
          <div
            className="flex flex-wrap items-center justify-between gap-2 border-b border-border pb-3 text-sm text-muted-foreground"
            data-testid="conversations-metadata-row"
          >
            <span>{intl.formatMessage({ id: "conversations.summary.loadedValue" }, { count: state.data.items.length })}</span>
            {state.data.truncated ? (
              <span>{intl.formatMessage({ id: "conversations.summary.truncated" })}</span>
            ) : null}
          </div>
        ) : null}

        {state.loading ? <ResourceState kind="loading" title={intl.formatMessage({ id: "conversations.empty.title" })} /> : null}
        {!state.loading && state.error ? (
          <ResourceState kind={mapErrorKind(state.error)} onRetry={submitted ? refresh : undefined} title={intl.formatMessage({ id: "conversations.empty.title" })} />
        ) : null}
        {!state.loading && !state.error && !state.queried ? (
          <ResourceState kind="empty" title={intl.formatMessage({ id: "conversations.empty.title" })} />
        ) : null}
        {!state.loading && !state.error && state.data ? (
          state.data.items.length > 0 ? (
            <div className="overflow-x-auto rounded-md border border-border" data-conversations-surface="results">
              <table
                aria-label={intl.formatMessage({ id: "conversations.title" })}
                className="w-full min-w-[760px] border-collapse text-sm"
              >
                <thead className="bg-muted/40 text-left text-xs uppercase tracking-[0.14em] text-muted-foreground">
                  <tr>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "conversations.table.channel" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "conversations.table.type" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "conversations.table.unread" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "conversations.table.lastSeq" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "conversations.table.clientMsgNo" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "conversations.table.timestamp" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "conversations.table.preview" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "conversations.table.actions" })}</th>
                  </tr>
                </thead>
                <tbody>
                  {state.data.items.map((item) => (
                    <tr className="border-t border-border" key={`${item.channel_type}-${item.channel_id}`}>
                      <td className="px-3 py-3 text-sm font-medium text-foreground">{item.channel_id}</td>
                      <td className="px-3 py-3 text-sm text-muted-foreground">{item.channel_type}</td>
                      <td className="px-3 py-3 text-sm text-muted-foreground">{item.unread}</td>
                      <td className="px-3 py-3 text-sm text-muted-foreground">{item.last_msg_seq}</td>
                      <td className="px-3 py-3 text-sm text-muted-foreground">{item.last_client_msg_no || "-"}</td>
                      <td className="px-3 py-3 text-sm text-muted-foreground">{formatTimestamp(item.timestamp)}</td>
                      <td className="max-w-[24rem] px-3 py-3 text-sm text-foreground">{firstPreview(item)}</td>
                      <td className="px-3 py-3 text-sm">
                        <Button asChild size="sm" variant="outline">
                          <Link aria-label={intl.formatMessage({ id: "conversations.viewMessagesFor" }, { channel: item.channel_id })} to={messagesHref(item.channel_id, item.channel_type)}>
                            {intl.formatMessage({ id: "conversations.viewMessages" })}
                          </Link>
                        </Button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ) : (
            <ResourceState kind="empty" title={intl.formatMessage({ id: "conversations.empty.title" })} />
          )
        ) : null}
      </SectionCard>
    </PageContainer>
  )
}
