import type { DashboardMetricsResponse, DashboardMetricsSeriesDTO } from "@/lib/manager-api"
import type {
  ManagerBusinessChannelsResponse,
  ManagerSystemUsersResponse,
  ManagerUsersResponse,
} from "@/lib/manager-api.types"

export type BusinessVerdict = "normal" | "degraded" | "critical"
export type MetricSource = "real" | "derived" | "sample"

export type BusinessMetricValue = {
  key: string
  labelId: string
  value: number
  formatted: string
  detail: string
  tone: "default" | "warning" | "danger"
  source: MetricSource
}

export type BusinessRisk = {
  key: string
  severity: "warning" | "critical"
  titleId: string
  detail: string
  href: string
}

export type BusinessEntryCard = {
  key: "users" | "channels" | "messages" | "systemUsers" | "monitor"
  titleId: string
  descriptionId: string
  href: string
  value: string
  source: MetricSource
}

export type BusinessTrendSeries = {
  throughput: Array<{ index: number; send: number; deliver: number }>
  latency: Array<{ index: number; send: number; delivery: number }>
  failures: Array<{ index: number; send: number; delivery: number }>
}

export function computeBusinessVerdict(metrics: DashboardMetricsResponse): BusinessVerdict {
  const m = metrics.metrics
  if (
    m.delivery_fail_rate_percent.latest >= 5 ||
    m.send_fail_rate_percent.latest >= 5 ||
    m.delivery_latency_p99_ms.latest >= 3000
  ) {
    return "critical"
  }
  if (
    m.delivery_fail_rate_percent.latest >= 1 ||
    m.send_fail_rate_percent.latest >= 1 ||
    m.retry_queue_depth.latest >= 100 ||
    m.delivery_latency_p99_ms.latest >= 1000
  ) {
    return "degraded"
  }
  return "normal"
}

export function buildBusinessMetricStrip(metrics: DashboardMetricsResponse): BusinessMetricValue[] {
  const m = metrics.metrics
  return [
    countMetric("sendRate", "businessDashboard.metric.sendRate", m.send_per_sec.latest, "/s"),
    countMetric("deliverRate", "businessDashboard.metric.deliverRate", m.deliver_per_sec.latest, "/s"),
    latencyMetric("sendLatency", "businessDashboard.metric.sendLatency", m.send_latency_p99_ms.latest),
    latencyMetric("deliveryLatency", "businessDashboard.metric.deliveryLatency", m.delivery_latency_p99_ms.latest),
    percentMetric("sendFailRate", "businessDashboard.metric.sendFailRate", m.send_fail_rate_percent.latest),
    percentMetric("deliveryFailRate", "businessDashboard.metric.deliveryFailRate", m.delivery_fail_rate_percent.latest),
    countMetric("connections", "businessDashboard.metric.connections", m.connections.latest),
    countMetric("activeChannels", "businessDashboard.metric.activeChannels", m.active_channels.latest),
    countMetric("retryQueue", "businessDashboard.metric.retryQueue", m.retry_queue_depth.latest),
    countMetric("fanOut", "businessDashboard.metric.fanOut", m.fan_out_rate.latest, "x"),
  ]
}

export function buildBusinessTrendSeries(metrics: DashboardMetricsResponse): BusinessTrendSeries {
  return {
    throughput: metrics.metrics.send_per_sec.series.map((value, index) => ({
      index,
      send: value,
      deliver: valueAt(metrics.metrics.deliver_per_sec, index),
    })),
    latency: metrics.metrics.send_latency_p99_ms.series.map((value, index) => ({
      index,
      send: value,
      delivery: valueAt(metrics.metrics.delivery_latency_p99_ms, index),
    })),
    failures: metrics.metrics.send_fail_rate_percent.series.map((value, index) => ({
      index,
      send: value,
      delivery: valueAt(metrics.metrics.delivery_fail_rate_percent, index),
    })),
  }
}

export function buildBusinessRisks(metrics: DashboardMetricsResponse): BusinessRisk[] {
  const m = metrics.metrics
  const risks: BusinessRisk[] = []

  if (m.retry_queue_depth.latest >= 100) {
    risks.push({
      key: "retryQueue",
      severity: "warning",
      titleId: "businessDashboard.risks.retryQueue",
      detail: `${m.retry_queue_depth.latest.toLocaleString()} queued retries`,
      href: "/business/monitor",
    })
  }

  const maxFailure = Math.max(m.send_fail_rate_percent.latest, m.delivery_fail_rate_percent.latest)
  if (maxFailure >= 1) {
    risks.push({
      key: "failureRate",
      severity: maxFailure >= 5 ? "critical" : "warning",
      titleId: "businessDashboard.risks.failureRate",
      detail: `${maxFailure.toFixed(2)}% failure rate`,
      href: "/business/messages",
    })
  }

  if (m.delivery_latency_p99_ms.latest >= 1000) {
    risks.push({
      key: "deliveryLatency",
      severity: m.delivery_latency_p99_ms.latest >= 3000 ? "critical" : "warning",
      titleId: "businessDashboard.risks.deliveryLatency",
      detail: `${Math.round(m.delivery_latency_p99_ms.latest).toLocaleString()} ms delivery p99`,
      href: "/business/monitor",
    })
  }

  if (m.send_per_sec.latest > 0 && m.deliver_per_sec.latest < m.send_per_sec.latest * 0.8) {
    risks.push({
      key: "deliveryGap",
      severity: "warning",
      titleId: "businessDashboard.risks.deliveryGap",
      detail: `${m.deliver_per_sec.latest.toLocaleString()} deliver/s below ${m.send_per_sec.latest.toLocaleString()} send/s`,
      href: "/business/monitor",
    })
  }

  return risks
}

export function buildBusinessEntryCards(input: {
  users?: ManagerUsersResponse | null
  channels?: ManagerBusinessChannelsResponse | null
  systemUsers?: ManagerSystemUsersResponse | null
} = {}): BusinessEntryCard[] {
  return [
    {
      key: "users",
      titleId: "businessDashboard.entries.users",
      descriptionId: "businessDashboard.entries.usersDescription",
      href: "/business/users",
      value: input.users ? String(input.users.items.length) : "12.1k",
      source: input.users ? "real" : "sample",
    },
    {
      key: "channels",
      titleId: "businessDashboard.entries.channels",
      descriptionId: "businessDashboard.entries.channelsDescription",
      href: "/business/channels",
      value: input.channels ? String(input.channels.items.length) : "2.1k",
      source: input.channels ? "real" : "sample",
    },
    {
      key: "messages",
      titleId: "businessDashboard.entries.messages",
      descriptionId: "businessDashboard.entries.messagesDescription",
      href: "/business/messages",
      value: "Search",
      source: "real",
    },
    {
      key: "systemUsers",
      titleId: "businessDashboard.entries.systemUsers",
      descriptionId: "businessDashboard.entries.systemUsersDescription",
      href: "/business/system-users",
      value: input.systemUsers ? String(input.systemUsers.total) : "24",
      source: input.systemUsers ? "real" : "sample",
    },
    {
      key: "monitor",
      titleId: "businessDashboard.entries.monitor",
      descriptionId: "businessDashboard.entries.monitorDescription",
      href: "/business/monitor",
      value: "Live",
      source: "real",
    },
  ]
}

function valueAt(series: DashboardMetricsSeriesDTO, index: number) {
  return series.series[index] ?? series.latest
}

function countMetric(key: string, labelId: string, value: number, suffix = ""): BusinessMetricValue {
  return {
    key,
    labelId,
    value,
    formatted: `${formatNumber(value)}${suffix}`,
    detail: "latest",
    tone: "default",
    source: "real",
  }
}

function latencyMetric(key: string, labelId: string, value: number): BusinessMetricValue {
  return {
    key,
    labelId,
    value,
    formatted: `${Math.round(value).toLocaleString()} ms`,
    detail: "p99",
    tone: value >= 3000 ? "danger" : value >= 1000 ? "warning" : "default",
    source: "real",
  }
}

function percentMetric(key: string, labelId: string, value: number): BusinessMetricValue {
  return {
    key,
    labelId,
    value,
    formatted: `${value.toFixed(2)}%`,
    detail: "latest",
    tone: value >= 5 ? "danger" : value >= 1 ? "warning" : "default",
    source: "real",
  }
}

function formatNumber(value: number) {
  return Number.isInteger(value) ? value.toLocaleString() : value.toFixed(1)
}
