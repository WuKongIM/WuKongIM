import { useCallback, useEffect, useState } from "react"
import { useIntl } from "react-intl"
import { Link } from "react-router-dom"

import { ResourceState } from "@/components/manager/resource-state"
import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
import { SectionCard } from "@/components/shell/section-card"
import { Button } from "@/components/ui/button"
import { ManagerApiError, getChannelClusterSummary } from "@/lib/manager-api"
import type { ManagerChannelClusterSummaryResponse } from "@/lib/manager-api.types"

type ChannelClusterState = {
  summary: ManagerChannelClusterSummaryResponse | null
  loading: boolean
  refreshing: boolean
  error: Error | null
}

type ChannelClusterOverviewPanelProps = {
  unhealthyHref?: string
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

function formatAverage(value: number) {
  return new Intl.NumberFormat(undefined, {
    minimumFractionDigits: 1,
    maximumFractionDigits: 2,
  }).format(value)
}

export function ChannelClusterOverviewPanel({
  unhealthyHref = "/channel-cluster/unhealthy",
}: ChannelClusterOverviewPanelProps = {}) {
  const intl = useIntl()
  const [state, setState] = useState<ChannelClusterState>({
    summary: null,
    loading: true,
    refreshing: false,
    error: null,
  })

  const loadSummary = useCallback(async (refreshing: boolean) => {
    setState((current) => ({
      ...current,
      loading: refreshing ? current.loading : true,
      refreshing,
      error: null,
    }))

    try {
      const summary = await getChannelClusterSummary()
      setState({ summary, loading: false, refreshing: false, error: null })
    } catch (error) {
      setState({
        summary: null,
        loading: false,
        refreshing: false,
        error: error instanceof Error ? error : new Error("channel cluster summary request failed"),
      })
    }
  }, [])

  useEffect(() => {
    void loadSummary(false)
  }, [loadSummary])

  const summary = state.summary
  const unhealthyCount = summary ? summary.isr_insufficient + summary.no_leader : 0

  return (
    <>
      <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div>
          <h2 className="text-xl font-semibold tracking-tight text-foreground">
            {intl.formatMessage({ id: "channelCluster.overview.title" })}
          </h2>
          <p className="mt-1 max-w-3xl text-sm leading-6 text-muted-foreground">
            {intl.formatMessage({ id: "channelCluster.description" })}
          </p>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button asChild size="sm" variant="outline">
            <Link to={unhealthyHref}>
              {intl.formatMessage({ id: "channelCluster.unhealthyLink" })}
            </Link>
          </Button>
          <Button
            onClick={() => {
              void loadSummary(true)
            }}
            size="sm"
            variant="outline"
          >
            {state.refreshing
              ? intl.formatMessage({ id: "common.refreshing" })
              : intl.formatMessage({ id: "common.refresh" })}
          </Button>
        </div>
      </div>
      {state.loading ? (
        <ResourceState kind="loading" title={intl.formatMessage({ id: "channelCluster.title" })} />
      ) : null}
      {!state.loading && state.error ? (
        <ResourceState
          kind={mapErrorKind(state.error)}
          onRetry={() => {
            void loadSummary(false)
          }}
          title={intl.formatMessage({ id: "channelCluster.title" })}
        />
      ) : null}
      {!state.loading && !state.error && summary && summary.total === 0 ? (
        <ResourceState
          description={intl.formatMessage({ id: "channelCluster.emptySummary" })}
          kind="empty"
          title={intl.formatMessage({ id: "channelCluster.title" })}
        />
      ) : null}
      {!state.loading && !state.error && summary && summary.total > 0 ? (
        <>
          <section className="grid gap-3 md:grid-cols-2 xl:grid-cols-6">
            <SectionCard title={intl.formatMessage({ id: "channelCluster.metric.total" })}>
              <div className="text-3xl font-semibold text-foreground">{summary.total}</div>
            </SectionCard>
            <SectionCard title={intl.formatMessage({ id: "channelCluster.metric.healthy" })}>
              <div className="text-3xl font-semibold text-foreground">{summary.healthy}</div>
            </SectionCard>
            <SectionCard title={intl.formatMessage({ id: "channelCluster.metric.isrInsufficient" })}>
              <div className="text-3xl font-semibold text-foreground">
                {intl.formatMessage(
                  { id: "channelCluster.metric.channelCount" },
                  { count: summary.isr_insufficient },
                )}
              </div>
            </SectionCard>
            <SectionCard title={intl.formatMessage({ id: "channelCluster.metric.noLeader" })}>
              <div className="text-3xl font-semibold text-foreground">
                {intl.formatMessage({ id: "channelCluster.metric.channelCount" }, { count: summary.no_leader })}
              </div>
            </SectionCard>
            <SectionCard title={intl.formatMessage({ id: "channelCluster.metric.avgReplicas" })}>
              <div className="text-3xl font-semibold text-foreground">{formatAverage(summary.avg_replicas)}</div>
            </SectionCard>
            <SectionCard title={intl.formatMessage({ id: "channelCluster.metric.avgIsr" })}>
              <div className="text-3xl font-semibold text-foreground">{formatAverage(summary.avg_isr)}</div>
            </SectionCard>
          </section>

          <section className="grid gap-4 xl:grid-cols-[0.8fr_1.2fr]">
            <SectionCard
              description={intl.formatMessage(
                { id: "channelCluster.unhealthySummary" },
                { count: unhealthyCount },
              )}
              title={intl.formatMessage({ id: "channelCluster.unhealthy.title" })}
            >
              <div className="rounded-lg border border-border bg-muted/30 px-3 py-3 text-sm leading-6 text-muted-foreground">
                {intl.formatMessage(
                  { id: "channelCluster.unhealthyBreakdown" },
                  {
                    isr: summary.isr_insufficient,
                    noLeader: summary.no_leader,
                  },
                )}
              </div>
            </SectionCard>

            <SectionCard title={intl.formatMessage({ id: "channelCluster.leaderDistribution.title" })}>
              {summary.leader_distribution.length > 0 ? (
                <div className="overflow-x-auto rounded-lg border border-border">
                  <table className="w-full border-collapse">
                    <thead className="bg-muted/40 text-left text-xs uppercase tracking-[0.14em] text-muted-foreground">
                      <tr>
                        <th className="px-3 py-3">{intl.formatMessage({ id: "channelCluster.table.node" })}</th>
                        <th className="px-3 py-3">{intl.formatMessage({ id: "channelCluster.table.channels" })}</th>
                      </tr>
                    </thead>
                    <tbody>
                      {summary.leader_distribution.map((item) => (
                        <tr className="border-t border-border" key={item.node_id}>
                          <td className="px-3 py-3 text-sm font-medium text-foreground">
                            {intl.formatMessage({ id: "channelCluster.nodeValue" }, { id: item.node_id })}
                          </td>
                          <td className="px-3 py-3 text-sm text-muted-foreground">
                            {intl.formatMessage(
                              { id: "channelCluster.leaderDistribution.channelCount" },
                              { count: item.count },
                            )}
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              ) : (
                <ResourceState
                  kind="empty"
                  title={intl.formatMessage({ id: "channelCluster.leaderDistribution.title" })}
                  description={intl.formatMessage({ id: "channelCluster.leaderDistribution.empty" })}
                />
              )}
            </SectionCard>
          </section>
        </>
      ) : null}
    </>
  )
}

export function ChannelClusterPage() {
  const intl = useIntl()

  return (
    <PageContainer>
      <PageHeader
        eyebrow={intl.formatMessage({ id: "nav.path.cluster.channels" })}
        title={intl.formatMessage({ id: "channelCluster.title" })}
        description={intl.formatMessage({ id: "channelCluster.description" })}
      />
      <ChannelClusterOverviewPanel />
    </PageContainer>
  )
}
