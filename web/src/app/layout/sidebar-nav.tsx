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
      className="flex w-full shrink-0 flex-col border-b border-sidebar-border bg-sidebar/80 px-3 py-3 backdrop-blur lg:w-[248px] lg:border-b-0 lg:border-r lg:px-4 lg:py-5"
    >
      <div className="rounded-2xl border border-border/80 bg-card/80 px-3 py-3 shadow-[inset_0_1px_0_rgba(255,255,255,0.04)]">
        <div className="font-mono text-[10px] font-semibold uppercase tracking-[0.22em] text-muted-foreground">
          {intl.formatMessage({ id: activeSection.titleMessageId })}
        </div>
        <div className="mt-2 text-sm font-semibold text-foreground">WuKongIM</div>
        <p className="mt-1 text-xs leading-5 text-muted-foreground">
          {intl.formatMessage({ id: "shell.runtimeConsoleDescription" })}
        </p>
      </div>

      <div className="mt-3 flex gap-2 overflow-x-auto pb-1 lg:mt-5 lg:flex-col lg:space-y-1 lg:overflow-visible lg:pb-0">
        {activeSection.items.map((item) => (
          <NavLink
            key={item.href}
            aria-label={intl.formatMessage({ id: item.titleMessageId })}
            className={({ isActive }) =>
              cn(
                "flex shrink-0 items-center gap-2 rounded-xl border px-3 py-2 text-sm transition-colors lg:w-full",
                isActive
                  ? "border-primary/35 bg-primary/10 text-foreground shadow-[0_0_20px_rgba(101,216,138,0.08)]"
                  : "border-transparent text-muted-foreground hover:border-border hover:bg-card hover:text-foreground",
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

      <div className="mt-3 rounded-2xl border border-border/80 bg-card/80 px-3 py-3 lg:mt-auto">
        <div className="flex items-center justify-between gap-3">
          <div>
            <div className="font-mono text-[10px] font-semibold uppercase tracking-[0.22em] text-muted-foreground">
              {intl.formatMessage({ id: "shell.clusterStatus" })}
            </div>
            <div className="mt-2 text-sm font-medium text-foreground">
              {intl.formatMessage({ id: "shell.singleNodeCluster" })}
            </div>
          </div>
          <div className="rounded-xl border border-primary/25 bg-primary/10 p-2 text-primary">
            <ShieldCheck className="size-4" />
          </div>
        </div>
        <div className="mt-4 grid gap-2 text-xs text-muted-foreground sm:grid-cols-2 lg:grid-cols-1">
          <div className="flex items-center justify-between gap-2 rounded-xl border border-border/80 bg-muted/45 px-2 py-2">
            <span className="inline-flex items-center gap-2 text-foreground">
              <span className="size-1.5 rounded-full bg-[var(--status-healthy)]" />
              {intl.formatMessage({ id: "shell.stableShell" })}
            </span>
            <span>{intl.formatMessage({ id: "shell.ready" })}</span>
          </div>
          <div className="flex items-center justify-between gap-2 rounded-xl border border-border/80 bg-muted/45 px-2 py-2">
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
