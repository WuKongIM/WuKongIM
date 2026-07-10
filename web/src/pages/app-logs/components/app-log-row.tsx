import { ChevronDown, ChevronRight, Copy } from "lucide-react"
import { useState } from "react"
import { useIntl } from "react-intl"
import { Link } from "react-router-dom"

import { Button } from "@/components/ui/button"
import type { ManagerApplicationLogEntry } from "@/lib/manager-api.types"
import {
  compactFieldLabel,
  displayLogLevel,
  formatFields,
  importantLogFields,
  logLevelClassName,
} from "@/pages/app-logs/log-format"

type AppLogRowProps = {
  entry: ManagerApplicationLogEntry
}

function copyText(value: string) {
  if (!navigator.clipboard) {
    return
  }
  void navigator.clipboard.writeText(value)
}

export function AppLogRow({ entry }: AppLogRowProps) {
  const intl = useIntl()
  const [expanded, setExpanded] = useState(false)
  const fields = importantLogFields(entry)
  const details = formatFields(entry.fields)
  const slot = entry.fields?.slot_id
  const slotLabel = slot === undefined || slot === null ? null : String(slot)
  const seq = entry.seq

  return (
    <article
      className="grid gap-2 border-b border-white/5 px-3 py-2 last:border-b-0 md:grid-cols-[9.5rem_4.5rem_minmax(0,1fr)_auto]"
      data-app-log-row="compact"
    >
      <div className="whitespace-nowrap text-slate-500">{entry.time || "-"}</div>
      <div
        className={`h-fit max-w-[4.5rem] overflow-hidden truncate rounded border px-1.5 py-0.5 text-[11px] font-semibold leading-none ${logLevelClassName(entry.level)}`}
        title={entry.level || displayLogLevel(entry.level)}
      >
        {displayLogLevel(entry.level)}
      </div>
      <div className="min-w-0">
        <div className="flex flex-wrap items-center gap-2">
          {entry.module ? <span className="text-slate-400">{entry.module}</span> : null}
          <span className="break-all text-slate-100">{entry.message || entry.raw}</span>
        </div>
        <div
          className="mt-1 flex flex-wrap gap-1.5 break-all text-[11px] text-slate-400"
          data-system-log-entry="metadata"
        >
          {entry.caller ? <span className="break-all">{entry.caller}</span> : null}
          {fields.map(([key, value]) => (
            <span
              className="rounded-sm border border-white/10 px-1.5 py-0.5"
              key={`${entry.seq}-${key}`}
            >
              {compactFieldLabel(key, value)}
            </span>
          ))}
          {slotLabel ? (
            <Link
              className="rounded-sm border border-white/10 px-1.5 py-0.5 text-sky-300 hover:text-sky-200"
              to={`/cluster/slots?tab=logs&slot_id=${encodeURIComponent(slotLabel)}`}
            >
              {intl.formatMessage({ id: "appLogs.openSlot" }, { slot: slotLabel })}
            </Link>
          ) : null}
        </div>
        {expanded ? (
          <div className="mt-2 space-y-1 text-slate-500" data-app-log-row="details">
            <pre
              className="whitespace-pre-wrap break-all text-slate-500"
              data-system-log-entry="raw"
            >
              {entry.raw}
            </pre>
            {details ? (
              <pre className="whitespace-pre-wrap break-words text-slate-400">{details}</pre>
            ) : null}
            <Button
              aria-label={intl.formatMessage({ id: "appLogs.copyRawAria" }, { seq })}
              onClick={() => copyText(entry.raw)}
              size="sm"
              type="button"
              variant="outline"
            >
              <Copy />
              {intl.formatMessage({ id: "appLogs.copyRaw" })}
            </Button>
          </div>
        ) : null}
      </div>
      <div className="flex items-start gap-1">
        <Button
          aria-label={intl.formatMessage({ id: "appLogs.copyMessageAria" }, { seq })}
          onClick={() => copyText(entry.message || entry.raw)}
          size="icon"
          type="button"
          variant="ghost"
        >
          <Copy />
        </Button>
        <Button
          aria-label={intl.formatMessage(
            { id: expanded ? "appLogs.hideDetailsAria" : "appLogs.showDetailsAria" },
            { seq },
          )}
          onClick={() => setExpanded((value) => !value)}
          size="icon"
          type="button"
          variant="ghost"
        >
          {expanded ? <ChevronDown /> : <ChevronRight />}
        </Button>
      </div>
    </article>
  )
}
