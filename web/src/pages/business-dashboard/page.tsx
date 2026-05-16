import { useCallback, useEffect, useMemo, useState } from "react"
import { useIntl } from "react-intl"

import { ResourceState } from "@/components/manager/resource-state"
import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
import {
  ManagerApiError,
  getBusinessChannels,
  getDashboardMetrics,
  getSystemUsers,
  getUsers,
  type DashboardMetricsResponse,
} from "@/lib/manager-api"
import type {
  ManagerBusinessChannelsResponse,
  ManagerSystemUsersResponse,
  ManagerUsersResponse,
} from "@/lib/manager-api.types"

import { BusinessEntryCards } from "./components/business-entry-cards"
import { BusinessHealthHero } from "./components/business-health-hero"
import { BusinessMessageTrends } from "./components/business-message-trends"
import { BusinessMetricStrip } from "./components/business-metric-strip"
import { BusinessRiskList } from "./components/business-risk-list"
import {
  buildBusinessEntryCards,
  buildBusinessMetricStrip,
  buildBusinessRisks,
  buildBusinessTrendSeries,
  computeBusinessVerdict,
} from "./view-model"

type BusinessDashboardState = {
  metrics: DashboardMetricsResponse | null
  users: ManagerUsersResponse | null
  channels: ManagerBusinessChannelsResponse | null
  systemUsers: ManagerSystemUsersResponse | null
  loading: boolean
  refreshing: boolean
  error: Error | null
}

function formatErrorKind(error: Error | null) {
  if (!(error instanceof ManagerApiError)) return "error" as const
  if (error.status === 403) return "forbidden" as const
  if (error.status === 503) return "unavailable" as const
  return "error" as const
}

export function BusinessDashboardPage() {
  const intl = useIntl()
  const [state, setState] = useState<BusinessDashboardState>({
    metrics: null,
    users: null,
    channels: null,
    systemUsers: null,
    loading: true,
    refreshing: false,
    error: null,
  })

  const loadDashboard = useCallback(async (refreshing: boolean) => {
    setState((current) => ({
      ...current,
      loading: refreshing ? current.loading : true,
      refreshing,
      error: null,
    }))

    try {
      const metrics = await getDashboardMetrics({ window: "30m", step: "30s" })
      const [users, channels, systemUsers] = await Promise.all([
        getUsers({ limit: 1 }).catch(() => null),
        getBusinessChannels({ limit: 1 }).catch(() => null),
        getSystemUsers().catch(() => null),
      ])

      setState({ metrics, users, channels, systemUsers, loading: false, refreshing: false, error: null })
    } catch (error) {
      setState({
        metrics: null,
        users: null,
        channels: null,
        systemUsers: null,
        loading: false,
        refreshing: false,
        error: error instanceof Error ? error : new Error("business dashboard request failed"),
      })
    }
  }, [])

  useEffect(() => {
    void loadDashboard(false)
  }, [loadDashboard])

  const title = intl.formatMessage({ id: "businessDashboard.title" })
  const model = useMemo(() => {
    if (!state.metrics) return null
    return {
      verdict: computeBusinessVerdict(state.metrics),
      strip: buildBusinessMetricStrip(state.metrics),
      trends: buildBusinessTrendSeries(state.metrics),
      risks: buildBusinessRisks(state.metrics),
      entryCards: buildBusinessEntryCards({
        users: state.users,
        channels: state.channels,
        systemUsers: state.systemUsers,
      }),
    }
  }, [state.channels, state.metrics, state.systemUsers, state.users])

  let content = null

  if (state.loading) {
    content = <ResourceState kind="loading" title={title} />
  } else if (state.error) {
    content = <ResourceState kind={formatErrorKind(state.error)} onRetry={() => { void loadDashboard(false) }} title={title} />
  } else if (state.metrics && model) {
    content = (
      <>
        <BusinessHealthHero
          generatedAt={state.metrics.generated_at}
          metrics={state.metrics}
          onRefresh={() => { void loadDashboard(true) }}
          refreshing={state.refreshing}
          verdict={model.verdict}
        />
        <BusinessMetricStrip metrics={model.strip} />
        <section className="grid gap-4 xl:grid-cols-[3fr_2fr]">
          <BusinessMessageTrends trends={model.trends} />
          <BusinessRiskList risks={model.risks} />
        </section>
        <BusinessEntryCards cards={model.entryCards} />
      </>
    )
  }

  return (
    <PageContainer>
      <PageHeader description={intl.formatMessage({ id: "businessDashboard.description" })} title={title} />
      {content}
    </PageContainer>
  )
}
