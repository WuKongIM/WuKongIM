import type { IntlShape } from "react-intl"

export function compactNumber(intl: IntlShape, value: number) {
  return new Intl.NumberFormat(intl.locale).format(value)
}

export function formatBytes(intl: IntlShape, value: number) {
  if (value >= 1024 * 1024) return `${new Intl.NumberFormat(intl.locale, { maximumFractionDigits: 1 }).format(value / 1024 / 1024)} MB`
  if (value >= 1024) return `${new Intl.NumberFormat(intl.locale, { maximumFractionDigits: 1 }).format(value / 1024)} KB`
  return `${compactNumber(intl, value)} B`
}

export function formatBps(intl: IntlShape, value: number) {
  return `${compactNumber(intl, value)} bps`
}

export function formatLatency(intl: IntlShape, value: number) {
  if (value <= 0) return intl.formatMessage({ id: "network.latency.insufficientSamples" })
  return `${compactNumber(intl, value)} ms`
}

export function formatPercent(intl: IntlShape, value: number | null) {
  if (value === null || Number.isNaN(value)) return intl.formatMessage({ id: "network.latency.insufficientSamples" })
  return new Intl.NumberFormat(intl.locale, { maximumFractionDigits: 1, style: "percent" }).format(value)
}

export function formatTimestamp(intl: IntlShape, value: string) {
  const date = new Date(value)
  if (!value || value.startsWith("0001-") || Number.isNaN(date.getTime())) return intl.formatMessage({ id: "network.latency.insufficientSamples" })
  return new Intl.DateTimeFormat(intl.locale, {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  }).format(date)
}

export function formatShortTime(intl: IntlShape, value: string) {
  const date = new Date(value)
  if (!value || value.startsWith("0001-") || Number.isNaN(date.getTime())) return intl.formatMessage({ id: "network.latency.insufficientSamples" })
  return new Intl.DateTimeFormat(intl.locale, { hour: "2-digit", minute: "2-digit" }).format(date)
}
