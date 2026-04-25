import { Cpu, ShieldCheck } from "lucide-react"
import { useIntl } from "react-intl"
import { NavLink } from "react-router-dom"

import { cn } from "@/lib/utils"
import { navigationGroups } from "@/lib/navigation"

export function SidebarNav() {
  const intl = useIntl()

  return (
    <nav
      aria-label="Primary navigation"
      className="flex w-72 shrink-0 flex-col border-r border-sidebar-border bg-sidebar px-4 py-5"
    >
      <div className="rounded-lg border border-border bg-background px-4 py-4">
        <div className="text-[11px] font-semibold uppercase tracking-[0.24em] text-muted-foreground">
          {intl.formatMessage({ id: "shell.managementConsole" })}
        </div>
        <div className="mt-2 text-lg font-semibold text-foreground">WuKongIM</div>
        <p className="mt-1 text-sm leading-6 text-muted-foreground">
          {intl.formatMessage({ id: "shell.runtimeConsoleDescription" })}
        </p>
      </div>

      <div className="mt-6 space-y-5">
        {navigationGroups.map((group) => (
          <section key={group.labelMessageId} className="space-y-2">
            <div className="px-3 text-[11px] font-semibold uppercase tracking-[0.24em] text-muted-foreground">
              {intl.formatMessage({ id: group.labelMessageId })}
            </div>
            <div className="space-y-1">
              {group.items.map((item) => (
                <NavLink
                  key={item.href}
                  aria-label={intl.formatMessage({ id: item.titleMessageId })}
                  className={({ isActive }) =>
                    cn(
                      "flex items-center gap-3 rounded-md border px-3 py-2.5 text-sm transition-colors",
                      isActive
                        ? "border-border bg-background text-foreground"
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
          </section>
        ))}
      </div>

      <div className="mt-auto rounded-lg border border-border bg-background px-4 py-4">
        <div className="flex items-center justify-between gap-3">
          <div>
            <div className="text-[11px] font-semibold uppercase tracking-[0.24em] text-muted-foreground">
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
          <div className="flex items-center justify-between gap-3 rounded-md border border-border bg-muted/40 px-3 py-2">
            <span className="inline-flex items-center gap-2 text-foreground">
              <span className="size-2 rounded-full bg-foreground" />
              {intl.formatMessage({ id: "shell.stableShell" })}
            </span>
            <span>{intl.formatMessage({ id: "shell.ready" })}</span>
          </div>
          <div className="flex items-center justify-between gap-3 rounded-md border border-border bg-muted/40 px-3 py-2">
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
