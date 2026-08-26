import { Cpu, X } from "lucide-react"
import { useIntl } from "react-intl"
import { Link, NavLink, useLocation } from "react-router-dom"

import { LocaleSwitcher } from "@/components/i18n/locale-switcher"
import { ThemeSwitcher } from "@/components/theme/theme-switcher"
import { Button } from "@/components/ui/button"
import {
  Sheet,
  SheetClose,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"
import { cn } from "@/lib/utils"
import { getActiveNavigationSection, navigationSections } from "@/lib/navigation"
import { clusterHealthPresentation, useClusterStatus } from "@/app/layout/cluster-status-store"

type SidebarNavProps = {
  mobileOpen: boolean
  onMobileOpenChange: (open: boolean) => void
}

export function SidebarNav({ mobileOpen, onMobileOpenChange }: SidebarNavProps) {
  const intl = useIntl()
  const location = useLocation()
  const activeSection = getActiveNavigationSection(location.pathname)

  return (
    <>
      <nav
        aria-label={intl.formatMessage({ id: "nav.primary" })}
        className="hidden w-[244px] shrink-0 flex-col border-r border-sidebar-border bg-sidebar px-4 py-5 lg:flex"
      >
        <NavigationContent />
      </nav>
      <Sheet onOpenChange={onMobileOpenChange} open={mobileOpen}>
        <SheetContent
          className="w-[min(88vw,360px)] gap-0 bg-sidebar p-0 text-sidebar-foreground lg:hidden"
          showCloseButton={false}
          side="left"
        >
          <SheetHeader className="relative border-b border-sidebar-border pr-14">
            <SheetTitle>{intl.formatMessage({ id: "nav.navigation" })}</SheetTitle>
            <SheetDescription>
              {intl.formatMessage({ id: "shell.runtimeConsoleDescription" })}
            </SheetDescription>
            <SheetClose asChild>
              <Button
                aria-label={intl.formatMessage({ id: "common.close" })}
                className="absolute right-3 top-3"
                size="icon-sm"
                variant="ghost"
              >
                <X aria-hidden />
              </Button>
            </SheetClose>
          </SheetHeader>
          <nav
            aria-label={intl.formatMessage({ id: "nav.mobile" })}
            className="flex min-h-0 flex-1 flex-col overflow-y-auto px-4 py-4"
          >
            <div className="grid grid-cols-3 gap-1 border-b border-sidebar-border pb-4">
              {navigationSections.map((section) => (
                <Link
                  aria-current={activeSection.id === section.id ? "page" : undefined}
                  className={cn(
                    "rounded-md px-2 py-2 text-center text-xs font-medium",
                    activeSection.id === section.id
                      ? "bg-accent text-accent-foreground"
                      : "text-muted-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground",
                  )}
                  key={section.id}
                  onClick={() => onMobileOpenChange(false)}
                  to={section.href}
                >
                  {intl.formatMessage({ id: section.titleMessageId })}
                </Link>
              ))}
            </div>
            <NavigationContent onNavigate={() => onMobileOpenChange(false)} />
            <div className="mt-4 space-y-3 border-t border-sidebar-border pt-4">
              <ThemeSwitcher />
              <LocaleSwitcher />
            </div>
          </nav>
        </SheetContent>
      </Sheet>
    </>
  )
}

function NavigationContent({ onNavigate }: { onNavigate?: () => void }) {
  const intl = useIntl()
  const location = useLocation()
  const activeSection = getActiveNavigationSection(location.pathname)
  const clusterStatus = useClusterStatus()
  const statusPresentation = clusterHealthPresentation[clusterStatus.health]
  const clusterLabelId = clusterStatus.total === null
    ? "shell.clusterUnknown"
    : "shell.clusterNodeCount"
  const healthLabelId = clusterStatus.loading
    ? "shell.loading"
    : statusPresentation.stateMessageId

  return (
    <>
      <div className="border-b border-sidebar-border pb-4">
        <div className="font-mono text-[10px] font-semibold uppercase tracking-[0.22em] text-muted-foreground">
          {intl.formatMessage({ id: activeSection.titleMessageId })}
        </div>
        <div className="mt-2 text-sm font-medium text-sidebar-foreground">WuKongIM</div>
        <p className="mt-1 text-xs leading-5 text-muted-foreground">
          {intl.formatMessage({ id: "shell.runtimeConsoleDescription" })}
        </p>
      </div>

      <div className="mt-4 flex flex-col gap-1">
        {activeSection.items.map((item) => (
          <NavLink
            key={item.href}
            aria-label={intl.formatMessage({ id: item.titleMessageId })}
            className={({ isActive }) =>
              cn(
                "flex w-full items-center gap-2 rounded-md px-3 py-2 text-sm transition-colors",
                isActive
                  ? "bg-accent text-accent-foreground"
                  : "text-muted-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground",
              )
            }
            onClick={onNavigate}
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

      <div className="mt-auto border-t border-sidebar-border pt-4">
        <div className="font-mono text-[10px] font-semibold uppercase tracking-[0.22em] text-muted-foreground">
          {intl.formatMessage({ id: "shell.clusterStatus" })}
        </div>
        <div className="mt-3 space-y-2 text-xs text-muted-foreground">
          <div className="flex items-center justify-between gap-2 border-b border-border pb-2">
            <span className="inline-flex items-center gap-2 text-sidebar-foreground">
              <span
                className={cn("size-1.5 rounded-full", statusPresentation.dotClassName)}
              />
              {intl.formatMessage(
                { id: clusterLabelId },
                { count: clusterStatus.total ?? 0 },
              )}
            </span>
            <span>{intl.formatMessage({ id: healthLabelId })}</span>
          </div>
          <div className="flex items-center justify-between gap-2">
            <span className="inline-flex items-center gap-2 text-sidebar-foreground">
              <Cpu className="size-3.5" />
              {clusterStatus.alive === null || clusterStatus.total === null
                ? intl.formatMessage({ id: "shell.liveStatusUnavailable" })
                : intl.formatMessage(
                  { id: "shell.aliveNodeCount" },
                  { alive: clusterStatus.alive, total: clusterStatus.total },
                )}
            </span>
            <span>{intl.formatMessage({ id: "shell.live" })}</span>
          </div>
        </div>
      </div>
    </>
  )
}
