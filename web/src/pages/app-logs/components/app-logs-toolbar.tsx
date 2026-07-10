import { Pause, Play, RefreshCw, Search } from "lucide-react"
import { useIntl } from "react-intl"

import { NodeFilter } from "@/components/manager/node-filter"
import { Button } from "@/components/ui/button"
import type { ManagerApplicationLogSource, ManagerNodesResponse } from "@/lib/manager-api.types"
import {
  appLogSeverityOptions,
  basenameForLogSourceFile,
  formatBytes,
  formatLogSourceLabel,
  type AppLogSeverityFilter,
} from "@/pages/app-logs/log-format"

type AppLogsToolbarProps = {
  nodes: ManagerNodesResponse | null
  selectedNodeId: number | null
  onNodeChange: (nodeId: number | null) => void
  sources: ManagerApplicationLogSource[]
  source: string
  onSourceChange: (source: string) => void
  severity: AppLogSeverityFilter
  onSeverityChange: (severity: AppLogSeverityFilter) => void
  keyword: string
  onKeywordChange: (keyword: string) => void
  followTail: boolean
  onFollowTailChange: (follow: boolean) => void
  lineCount: string
  activeSource: ManagerApplicationLogSource | null
  liveMessage: string
  canLoad: boolean
  loading: boolean
  refreshing: boolean
  onRefresh: () => void
  onSearch: () => void
}

export function AppLogsToolbar(props: AppLogsToolbarProps) {
  const intl = useIntl()
  const activeNode = props.nodes?.items.find((node) => node.node_id === props.selectedNodeId) ?? null
  const unavailableLabel = intl.formatMessage({ id: "appLogs.source.unavailable" })
  const activeSourceLabel = props.activeSource ? basenameForLogSourceFile(props.activeSource.file) : props.source

  return (
    <section className="space-y-3 border-b border-border pb-3" data-app-logs-surface="toolbar">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2 text-sm font-medium text-foreground">
            <span>{activeNode?.name || intl.formatMessage({ id: "common.nodeValue" }, { id: props.selectedNodeId ?? "-" })}</span>
            {activeNode?.is_local ? (
              <span className="rounded-sm bg-accent px-1.5 py-0.5 text-xs">
                {intl.formatMessage({ id: "appLogs.nodeLocal" })}
              </span>
            ) : null}
            {activeNode?.status ? <span className="text-xs text-muted-foreground">{activeNode.status}</span> : null}
          </div>
          <div className="mt-1 flex flex-wrap gap-x-3 gap-y-1 text-xs text-muted-foreground">
            <span>{activeNode?.addr ?? "-"}</span>
            <span>{activeSourceLabel}</span>
            <span>{props.activeSource ? formatBytes(props.activeSource.size_bytes) : "-"}</span>
            <span>{props.lineCount}</span>
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Button
            disabled={!props.canLoad || props.refreshing}
            onClick={props.onRefresh}
            size="sm"
            type="button"
            variant="outline"
          >
            <RefreshCw />
            {props.refreshing ? intl.formatMessage({ id: "common.refreshing" }) : intl.formatMessage({ id: "common.refresh" })}
          </Button>
          <Button
            aria-label={intl.formatMessage({ id: "appLogs.followTail" })}
            aria-pressed={props.followTail}
            disabled={!props.canLoad}
            onClick={() => props.onFollowTailChange(!props.followTail)}
            size="sm"
            type="button"
            variant={props.followTail ? "default" : "outline"}
          >
            {props.followTail ? <Play /> : <Pause />}
            {props.followTail ? intl.formatMessage({ id: "appLogs.status.following" }) : intl.formatMessage({ id: "appLogs.status.paused" })}
          </Button>
        </div>
      </div>

      <div className="grid gap-2 md:grid-cols-[minmax(12rem,1fr)_minmax(9rem,0.8fr)_minmax(9rem,0.8fr)_minmax(12rem,1fr)_auto] md:items-end">
        <NodeFilter nodes={props.nodes} selectedNodeId={props.selectedNodeId} onNodeChange={props.onNodeChange} />
        <label className="text-xs font-medium text-muted-foreground">
          {intl.formatMessage({ id: "appLogs.source" })}
          <select
            aria-label={intl.formatMessage({ id: "appLogs.source" })}
            className="mt-1 h-8 w-full rounded-md border border-border bg-background px-2 text-sm text-foreground"
            disabled={props.sources.length === 0}
            onChange={(event) => props.onSourceChange(event.target.value)}
            value={props.source}
          >
            {props.sources.map((item) => (
              <option disabled={!item.available} key={item.name} value={item.name}>
                {formatLogSourceLabel(item, unavailableLabel)}
              </option>
            ))}
          </select>
        </label>
        <label className="text-xs font-medium text-muted-foreground">
          {intl.formatMessage({ id: "appLogs.severity" })}
          <select
            aria-label={intl.formatMessage({ id: "appLogs.severity" })}
            className="mt-1 h-8 w-full rounded-md border border-border bg-background px-2 text-sm text-foreground"
            onChange={(event) => props.onSeverityChange(event.target.value as AppLogSeverityFilter)}
            value={props.severity}
          >
            {appLogSeverityOptions.map((option) => (
              <option key={option || "all"} value={option}>
                {intl.formatMessage({ id: `appLogs.severity.${option || "all"}` })}
              </option>
            ))}
          </select>
        </label>
        <label className="text-xs font-medium text-muted-foreground">
          {intl.formatMessage({ id: "appLogs.keyword" })}
          <input
            aria-label={intl.formatMessage({ id: "appLogs.keyword" })}
            className="mt-1 h-8 w-full rounded-md border border-border bg-background px-2 text-sm text-foreground"
            onChange={(event) => props.onKeywordChange(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Enter") {
                props.onSearch()
              }
            }}
            placeholder={intl.formatMessage({ id: "appLogs.keyword.placeholder" })}
            value={props.keyword}
          />
        </label>
        <Button disabled={!props.canLoad || props.loading} onClick={props.onSearch} size="sm" type="button">
          <Search />
          {intl.formatMessage({ id: "common.search" })}
        </Button>
      </div>

      <div className="text-xs text-muted-foreground" role="status">
        {props.liveMessage || (props.followTail ? intl.formatMessage({ id: "appLogs.status.following" }) : intl.formatMessage({ id: "appLogs.status.paused" }))}
      </div>
    </section>
  )
}
