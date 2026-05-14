import { Cpu, ShieldCheck } from "lucide-react"
import { useIntl } from "react-intl"
import { NavLink, useLocation } from "react-router-dom"

import { cn } from "@/lib/utils"
import { getActiveNavigationSection } from "@/lib/navigation"

export function SidebarNav() {
  const intl = useIntl()
  const location = useLocation()
  const activeSection = getActiveNavigationSection(location.pathname)

  return (
    <nav
      aria-label="Primary navigation"
      className="flex w-[200px] shrink-0 flex-col border-r border-sidebar-border bg-sidebar px-3 py-4"
    >
      <div className="rounded-lg border border-border bg-background px-3 py-3">
        <div className="font-mono text-[10px] font-semibold uppercase tracking-[0.22em] text-muted-foreground">
          {intl.formatMessage({ id: activeSection.titleMessageId })}
        </div>
        <div className="mt-2 text-sm font-semibold text-foreground">WuKongIM</div>
        <p className="mt-1 text-xs leading-5 text-muted-foreground">
          {intl.formatMessage({ id: "shell.runtimeConsoleDescription" })}
        </p>
      </div>

      <div className="mt-5 space-y-1">
        {activeSection.items.map((item) => (
          <NavLink
            key={item.href}
            aria-label={intl.formatMessage({ id: item.titleMessageId })}
            className={({ isActive }) =>
              cn(
                "flex items-center gap-2 rounded-md border border-l-2 px-3 py-2 text-sm transition-colors",
                isActive
                  ? "border-border border-l-[var(--status-healthy)] bg-background text-foreground"
                  : "border-transparent text-muted-foreground hover:border-border hover:bg-background hover:text-foreground",
              )
            }
            to={item.href}
          >
            {({ isActive }) => (
              <>
                <item.icon
                  aria-hidden
                  className={cn("size-4 shrink-0", isActive ? "text-foreground" : "text-muted-foreground")}
                />
                <span className="font-medium tracking-[0.01em]">
                  {intl.formatMessage({ id: item.titleMessageId })}
                </span>
              </>
            )}
          </NavLink>
        ))}
      </div>

      <div className="mt-auto rounded-lg border border-border bg-background px-3 py-3">
        <div className="flex items-center justify-between gap-3">
          <div>
            <div className="font-mono text-[10px] font-semibold uppercase tracking-[0.22em] text-muted-foreground">
              {intl.formatMessage({ id: "shell.clusterStatus" })}
            </div>
            <div className="mt-2 text-sm font-medium text-foreground">
              {intl.formatMessage({ id: "shell.singleNodeCluster" })}
            </div>
          </div>
          <div className="rounded-md border border-border bg-muted/60 p-2 text-foreground">
            <ShieldCheck className="size-4" />
          </div>
        </div>
        <div className="mt-4 space-y-2 text-xs text-muted-foreground">
          <div className="flex items-center justify-between gap-2 rounded-md border border-border bg-muted/40 px-2 py-2">
            <span className="inline-flex items-center gap-2 text-foreground">
              <span className="size-1.5 rounded-full bg-[var(--status-healthy)]" />
              {intl.formatMessage({ id: "shell.stableShell" })}
            </span>
            <span>{intl.formatMessage({ id: "shell.ready" })}</span>
          </div>
          <div className="flex items-center justify-between gap-2 rounded-md border border-border bg-muted/40 px-2 py-2">
            <span className="inline-flex items-center gap-2 text-foreground">
              <Cpu className="size-3.5" />
              {intl.formatMessage({ id: "shell.noLiveFeedYet" })}
            </span>
            <span>{intl.formatMessage({ id: "shell.static" })}</span>
          </div>
        </div>
      </div>
    </nav>
  )
}
