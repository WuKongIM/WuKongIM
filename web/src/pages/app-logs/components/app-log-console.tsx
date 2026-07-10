import { Terminal } from "lucide-react"
import { useIntl } from "react-intl"

import { Button } from "@/components/ui/button"
import type {
  ManagerApplicationLogEntry,
  ManagerApplicationLogSource,
} from "@/lib/manager-api.types"
import { basenameForLogSourceFile } from "@/pages/app-logs/log-format"
import { AppLogRow } from "@/pages/app-logs/components/app-log-row"

type AppLogConsoleProps = {
  title: string
  entries: ManagerApplicationLogEntry[]
  lineCount: string
  activeSource: ManagerApplicationLogSource | null
  source: string
  followTail: boolean
  newLiveLineCount: number
  refreshing: boolean
  rotated: boolean
  selectedNodeId: number | null
  atMaxTail: boolean
  onAcknowledgeLiveLines: () => void
  onLoadMore: () => void
}

export function AppLogConsole(props: AppLogConsoleProps) {
  const intl = useIntl()
  const activeSourceLabel = props.activeSource
    ? basenameForLogSourceFile(props.activeSource.file)
    : props.source
  const rotatedLabel = props.rotated
    ? intl.formatMessage({ id: "appLogs.status.rotated" })
    : ""
  const liveLinesLabel = props.newLiveLineCount > 0
    ? intl.formatMessage({ id: "appLogs.liveLines" }, { count: props.newLiveLineCount })
    : ""
  const bannerLabel = rotatedLabel && liveLinesLabel
    ? `${rotatedLabel} · ${liveLinesLabel}`
    : rotatedLabel || liveLinesLabel

  return (
    <section
      aria-label={props.title}
      className="overflow-hidden rounded-lg border border-[#242833] bg-[#0f1115] font-mono text-slate-100 shadow-[0_18px_40px_rgba(15,17,21,0.18)]"
      data-system-log-console="terminal"
    >
      <div className="flex flex-wrap items-center justify-between gap-2 border-b border-white/10 bg-[#151923] px-3 py-2 text-xs">
        <div className="flex min-w-0 items-center gap-2">
          <Terminal aria-hidden className="size-3.5 shrink-0 text-emerald-300" />
          <span className="font-semibold text-slate-100">
            {intl.formatMessage({ id: "appLogs.console.title" })}
          </span>
          <span className="truncate text-slate-400">{activeSourceLabel}</span>
        </div>
        <div className="flex items-center gap-3 text-[11px] text-slate-400">
          <span>{props.lineCount}</span>
          <span>
            {props.followTail
              ? intl.formatMessage({ id: "appLogs.status.following" })
              : intl.formatMessage({ id: "appLogs.status.paused" })}
          </span>
        </div>
      </div>
      {props.rotated || props.newLiveLineCount > 0 ? (
        <div className="flex flex-wrap items-center justify-between gap-2 border-b border-white/10 bg-[#111827] px-3 py-2 text-xs text-slate-300">
          <span>{bannerLabel}</span>
          {props.newLiveLineCount > 0 ? (
            <Button
              aria-label={intl.formatMessage({ id: "appLogs.jumpLatestAria" })}
              onClick={props.onAcknowledgeLiveLines}
              size="sm"
              type="button"
              variant="outline"
            >
              {intl.formatMessage({ id: "appLogs.jumpLatest" })}
            </Button>
          ) : null}
        </div>
      ) : null}
      <div className="max-h-[min(64vh,720px)] overflow-auto text-xs" role="log">
        {props.entries.map((entry) => (
          <AppLogRow entry={entry} key={`${entry.seq}-${entry.offset}-${entry.raw}`} />
        ))}
      </div>
      <div className="border-t border-white/10 bg-[#151923] p-3">
        <Button
          disabled={props.refreshing || props.selectedNodeId === null || props.atMaxTail}
          onClick={props.onLoadMore}
          size="sm"
          type="button"
          variant="outline"
        >
          {props.refreshing
            ? intl.formatMessage({ id: "common.refreshing" })
            : intl.formatMessage({ id: "common.loadMore" })}
        </Button>
      </div>
    </section>
  )
}
