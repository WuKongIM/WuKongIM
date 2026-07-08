import { useCallback, useEffect, useState } from "react"
import { useIntl } from "react-intl"

import { ResourceState } from "@/components/manager/resource-state"
import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
import { SectionCard } from "@/components/shell/section-card"
import { getWebhookConfig, ManagerApiError } from "@/lib/manager-api"
import type { ManagerWebhookConfigResponse } from "@/lib/manager-api.types"

type WebhookConfigState = {
  config: ManagerWebhookConfigResponse | null
  loading: boolean
  error: Error | null
}

function emptyWebhookConfigState(): WebhookConfigState {
  return {
    config: null,
    loading: true,
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

type SummaryCellProps = {
  label: string
  value: string
  tone?: "default" | "success" | "muted" | "warning"
}

function SummaryCell({ label, value, tone = "default" }: SummaryCellProps) {
  const valueClass =
    tone === "success"
      ? "text-success"
      : tone === "warning"
        ? "text-warning"
        : tone === "muted"
          ? "text-muted-foreground"
          : "text-foreground"

  return (
    <div className="border-b border-border px-3 py-3 text-sm last:border-b-0 md:border-r md:border-b-0 md:last:border-r-0">
      <div className="text-muted-foreground">{label}</div>
      <div className={`mt-1 break-words font-semibold ${valueClass}`}>{value}</div>
    </div>
  )
}

type TagListProps = {
  items: string[]
  emptyText?: string
}

function TagList({ items, emptyText }: TagListProps) {
  if (items.length === 0 && emptyText) {
    return <span className="text-sm font-medium text-muted-foreground">{emptyText}</span>
  }

  return (
    <div className="flex flex-wrap gap-1">
      {items.map((item) => (
        <span
          className="rounded-full border border-border bg-muted/40 px-2 py-1 text-xs font-medium text-foreground"
          key={item}
        >
          {item}
        </span>
      ))}
    </div>
  )
}

export function WebhooksPage() {
  const intl = useIntl()
  const [state, setState] = useState<WebhookConfigState>(emptyWebhookConfigState)

  const runQuery = useCallback(async () => {
    setState((current) => ({ ...current, loading: true, error: null }))
    try {
      const config = await getWebhookConfig()
      setState({ config, loading: false, error: null })
    } catch (error) {
      setState({
        config: null,
        loading: false,
        error: error instanceof Error ? error : new Error("webhook config request failed"),
      })
    }
  }, [])

  useEffect(() => {
    void runQuery()
  }, [runQuery])

  const config = state.config

  return (
    <PageContainer>
      <PageHeader
        title={intl.formatMessage({ id: "webhooks.title" })}
        description={intl.formatMessage({ id: "webhooks.description" })}
      />

      {state.loading ? <ResourceState kind="loading" title={intl.formatMessage({ id: "webhooks.title" })} /> : null}
      {!state.loading && state.error ? (
        <ResourceState
          kind={mapErrorKind(state.error)}
          onRetry={() => {
            void runQuery()
          }}
          title={intl.formatMessage({ id: "webhooks.title" })}
        />
      ) : null}

      {!state.loading && !state.error && config ? (
        <>
          <SectionCard
            description={intl.formatMessage({ id: "webhooks.status.description" })}
            title={intl.formatMessage({ id: "webhooks.status.title" })}
          >
            <div className="grid overflow-hidden rounded-md border border-border bg-card md:grid-cols-4">
              <SummaryCell
                label={intl.formatMessage({ id: "webhooks.summary.status" })}
                tone={config.enabled ? "success" : "muted"}
                value={
                  config.enabled
                    ? intl.formatMessage({ id: "webhooks.status.enabled" })
                    : intl.formatMessage({ id: "webhooks.status.disabled" })
                }
              />
              <SummaryCell
                label={intl.formatMessage({ id: "webhooks.summary.endpoint" })}
                tone={config.http_addr ? "default" : "muted"}
                value={config.http_addr || intl.formatMessage({ id: "webhooks.endpoint.empty" })}
              />
              <SummaryCell
                label={intl.formatMessage({ id: "webhooks.summary.focusEvents" })}
                tone={config.focus_events.length > 0 ? "default" : "muted"}
                value={
                  config.focus_events.length > 0
                    ? intl.formatMessage({ id: "webhooks.focus.selected" }, { count: config.focus_events.length })
                    : intl.formatMessage({ id: "webhooks.focus.all" })
                }
              />
              <SummaryCell
                label={intl.formatMessage({ id: "webhooks.summary.restart" })}
                tone={config.requires_restart ? "warning" : "muted"}
                value={
                  config.requires_restart
                    ? intl.formatMessage({ id: "webhooks.restart.required" })
                    : intl.formatMessage({ id: "webhooks.restart.notRequired" })
                }
              />
            </div>
            <dl className="mt-4 grid gap-3 border-t border-border pt-3 text-sm sm:grid-cols-2">
              <div className="min-w-0">
                <dt className="text-muted-foreground">{intl.formatMessage({ id: "webhooks.source" })}</dt>
                <dd className="mt-1 break-words font-medium text-foreground">{config.source}</dd>
              </div>
              <div className="min-w-0">
                <dt className="text-muted-foreground">{intl.formatMessage({ id: "webhooks.requiresRestart" })}</dt>
                <dd className="mt-1 font-medium text-foreground">
                  {config.requires_restart
                    ? intl.formatMessage({ id: "webhooks.requiresRestart.yes" })
                    : intl.formatMessage({ id: "webhooks.requiresRestart.no" })}
                </dd>
              </div>
            </dl>
          </SectionCard>

          <SectionCard
            description={intl.formatMessage({ id: "webhooks.events.description" })}
            title={intl.formatMessage({ id: "webhooks.events.title" })}
          >
            <div className="grid gap-4 md:grid-cols-2">
              <div className="min-w-0 rounded-md border border-border p-3">
                <div className="mb-2 text-xs font-semibold uppercase tracking-[0.14em] text-muted-foreground">
                  {intl.formatMessage({ id: "webhooks.focusEvents" })}
                </div>
                <TagList
                  emptyText={intl.formatMessage({ id: "webhooks.focus.all" })}
                  items={config.focus_events}
                />
              </div>
              <div className="min-w-0 rounded-md border border-border p-3">
                <div className="mb-2 text-xs font-semibold uppercase tracking-[0.14em] text-muted-foreground">
                  {intl.formatMessage({ id: "webhooks.supportedEvents" })}
                </div>
                <TagList items={config.supported_events} />
              </div>
            </div>
          </SectionCard>

          <SectionCard
            description={intl.formatMessage({ id: "webhooks.delivery.description" })}
            title={intl.formatMessage({ id: "webhooks.delivery.title" })}
          >
            <div className="overflow-x-auto rounded-md border border-border">
              <table
                aria-label={intl.formatMessage({ id: "webhooks.delivery.title" })}
                className="w-full border-collapse text-sm"
              >
                <tbody>
                  {[
                    ["webhooks.queueSize", intl.formatNumber(config.queue_size)],
                    ["webhooks.workers", intl.formatNumber(config.workers)],
                    ["webhooks.messageBatchItems", intl.formatNumber(config.msg_notify_batch_max_items)],
                    ["webhooks.messageBatchWait", config.msg_notify_batch_max_wait],
                    ["webhooks.onlineStatusBatchItems", intl.formatNumber(config.online_status_batch_max_items)],
                    ["webhooks.onlineStatusBatchWait", config.online_status_batch_max_wait],
                    ["webhooks.offlineUidBatchSize", intl.formatNumber(config.offline_uid_batch_size)],
                    ["webhooks.requestTimeout", config.request_timeout],
                    ["webhooks.retryAttempts", intl.formatNumber(config.retry_max_attempts)],
                  ].map(([labelId, value]) => (
                    <tr className="border-t border-border first:border-t-0" key={labelId}>
                      <th className="w-64 px-3 py-3 text-left font-medium text-muted-foreground">
                        {intl.formatMessage({ id: labelId })}
                      </th>
                      <td className="px-3 py-3 font-medium text-foreground">{value}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </SectionCard>
        </>
      ) : null}
    </PageContainer>
  )
}
