import { Cpu } from "lucide-react"
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
      className="flex w-full shrink-0 flex-col border-b border-sidebar-border bg-sidebar px-3 py-3 lg:w-[244px] lg:border-b-0 lg:border-r lg:px-4 lg:py-5"
    >
      <div className="border-b border-sidebar-border pb-4">
        <div className="font-mono text-[10px] font-semibold uppercase tracking-[0.22em] text-muted-foreground">
          {intl.formatMessage({ id: activeSection.titleMessageId })}
        </div>
        <div className="mt-2 text-sm font-medium text-sidebar-foreground">WuKongIM</div>
        <p className="mt-1 text-xs leading-5 text-muted-foreground">
          {intl.formatMessage({ id: "shell.runtimeConsoleDescription" })}
        </p>
      </div>

      <div className="mt-3 flex gap-1 overflow-x-auto pb-1 lg:mt-4 lg:flex-col lg:overflow-visible lg:pb-0">
        {activeSection.items.map((item) => (
          <NavLink
            key={item.href}
            aria-label={intl.formatMessage({ id: item.titleMessageId })}
            className={({ isActive }) =>
              cn(
                "flex shrink-0 items-center gap-2 rounded-md px-3 py-2 text-sm transition-colors lg:w-full",
                isActive
                  ? "bg-accent text-accent-foreground"
                  : "text-muted-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground",
              )
            }
            to={item.href}
          >
            {({ isActive }) => (
              <>
                <item.icon
                  aria-hidden
                  className={cn("size-4 shrink-0", isActive ? "text-current" : "text-muted-foreground")}
                />
                <span className="font-medium">
                  {intl.formatMessage({ id: item.titleMessageId })}
                </span>
              </>
            )}
          </NavLink>
        ))}
      </div>

      <div className="mt-3 border-t border-sidebar-border pt-4 lg:mt-auto">
        <div className="font-mono text-[10px] font-semibold uppercase tracking-[0.22em] text-muted-foreground">
          {intl.formatMessage({ id: "shell.clusterStatus" })}
        </div>
        <div className="mt-3 space-y-2 text-xs text-muted-foreground">
          <div className="flex items-center justify-between gap-2 border-b border-border pb-2">
            <span className="inline-flex items-center gap-2 text-sidebar-foreground">
              <span className="size-1.5 rounded-full bg-[var(--status-healthy)]" />
              {intl.formatMessage({ id: "shell.singleNodeCluster" })}
            </span>
            <span>{intl.formatMessage({ id: "shell.ready" })}</span>
          </div>
          <div className="flex items-center justify-between gap-2">
            <span className="inline-flex items-center gap-2 text-sidebar-foreground">
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
