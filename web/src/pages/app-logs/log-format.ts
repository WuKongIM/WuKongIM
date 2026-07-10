import type { ManagerApplicationLogEntry, ManagerApplicationLogSource } from "@/lib/manager-api.types"

export type AppLogSeverityFilter = "" | "DEBUG" | "INFO" | "WARN" | "ERROR" | "WARN_ERROR"

export const appLogSeverityOptions: AppLogSeverityFilter[] = ["", "WARN_ERROR", "ERROR", "WARN", "INFO", "DEBUG"]

export function levelsForSeverityFilter(filter: AppLogSeverityFilter): string[] {
  if (filter === "WARN_ERROR") {
    return ["WARN", "ERROR"]
  }
  return filter ? [filter] : []
}

export function normalizedLogLevel(level: string) {
  const normalized = level.toUpperCase().trim()
  switch (normalized) {
    case "DEBUG":
    case "INFO":
    case "WARN":
    case "WARNING":
    case "ERROR":
    case "FATAL":
      return normalized
    default:
      return ""
  }
}

export function displayLogLevel(level: string) {
  const normalized = normalizedLogLevel(level)
  if (normalized === "WARNING") {
    return "WARN"
  }
  return normalized || "UNKNOWN"
}

export function logLevelClassName(level: string) {
  switch (normalizedLogLevel(level)) {
    case "ERROR":
    case "FATAL":
      return "border-red-400/30 bg-red-500/10 text-red-300"
    case "WARN":
    case "WARNING":
      return "border-amber-400/30 bg-amber-500/10 text-amber-300"
    case "INFO":
      return "border-emerald-400/30 bg-emerald-500/10 text-emerald-300"
    case "DEBUG":
      return "border-sky-400/30 bg-sky-500/10 text-sky-300"
    default:
      return "border-slate-500/30 bg-slate-500/10 text-slate-300"
  }
}

export function formatFields(fields: Record<string, unknown> | null) {
  if (!fields || Object.keys(fields).length === 0) {
    return ""
  }
  return JSON.stringify(fields)
}

export function compactFieldLabel(key: string, value: unknown) {
  return `${key}=${String(value)}`
}

export function formatBytes(value: number) {
  if (value >= 1024 * 1024) {
    return `${(value / (1024 * 1024)).toFixed(1)} MiB`
  }
  if (value >= 1024) {
    return `${(value / 1024).toFixed(1)} KiB`
  }
  return `${value} B`
}

export function basenameForLogSourceFile(file: string) {
  const normalized = file.trim().replace(/[/\\]+$/, "")
  if (!normalized) {
    return file
  }
  const segments = normalized.split(/[/\\]/)
  return segments[segments.length - 1] || file
}

export function formatLogSourceLabel(source: ManagerApplicationLogSource, unavailableLabel: string) {
  return source.available
    ? `${source.name} · ${basenameForLogSourceFile(source.file)}`
    : `${source.name} · ${unavailableLabel}`
}

export function importantLogFields(entry: ManagerApplicationLogEntry) {
  const fields = entry.fields ?? {}
  return ["node_id", "slot_id", "request_id", "trace_id"]
    .map((key) => [key, fields[key]] as const)
    .filter(([, value]) => value !== undefined && value !== null && value !== "")
}
