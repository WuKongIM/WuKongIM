import { Activity, CircleHelp, Menu, ShieldAlert, ShieldCheck } from "lucide-react"
import { useIntl } from "react-intl"
import { Link, useLocation } from "react-router-dom"

import { useAuthStore } from "@/auth/auth-store"
import { clusterHealthPresentation, useClusterStatus } from "@/app/layout/cluster-status-store"
import { LocaleSwitcher } from "@/components/i18n/locale-switcher"
import { ThemeSwitcher } from "@/components/theme/theme-switcher"
import { Button } from "@/components/ui/button"
import { cn } from "@/lib/utils"
import {
  getActiveNavigationItem,
  getActiveNavigationSection,
  navigationSections,
} from "@/lib/navigation"

export function Topbar({ onOpenNavigation }: { onOpenNavigation: () => void }) {
  const intl = useIntl()
  const location = useLocation()
  const activeSection = getActiveNavigationSection(location.pathname)
  const page = getActiveNavigationItem(location.pathname)
  const username = useAuthStore((state) => state.username)
  const authStatus = useAuthStore((state) => state.status)
  const logout = useAuthStore((state) => state.logout)
  const clusterStatus = useClusterStatus()
  const statusPresentation = clusterHealthPresentation[clusterStatus.health]
  const statusMessageId = clusterStatus.loading
    ? "shell.clusterSummaryLoading"
    : statusPresentation.summaryMessageId
  const StatusIcon = clusterStatus.health === "healthy"
    ? ShieldCheck
    : clusterStatus.health === "unknown"
      ? CircleHelp
      : ShieldAlert

  return (
    <header
      className="sticky top-0 z-30 border-b border-border bg-background px-3 py-2 sm:px-4"
      role="banner"
    >
      <div className="flex min-h-10 items-center justify-between gap-3">
        <div className="flex min-w-0 items-center gap-3 xl:gap-5">
          <Button
            aria-label={intl.formatMessage({ id: "nav.openNavigation" })}
            className="lg:hidden"
            onClick={onOpenNavigation}
            size="icon"
            variant="outline"
          >
            <Menu aria-hidden />
          </Button>
          <div className="flex shrink-0 items-center gap-2">
            <div
              aria-hidden
              className="size-7 rounded-sm border border-foreground bg-foreground dark:bg-primary"
              data-brand-mark
            />
            <div className="hidden sm:block">
              <div className="font-mono text-[12px] font-semibold tracking-[0.22em] text-foreground">WUKONGIM</div>
              <div className="text-[10px] font-medium uppercase tracking-[0.16em] text-muted-foreground">
                {intl.formatMessage({ id: "shell.operationsCockpit" })}
              </div>
            </div>
          </div>
          <nav
            aria-label={intl.formatMessage({ id: "nav.topSections" })}
            className="hidden min-w-0 items-center gap-1 border-l border-border pl-3 lg:flex"
          >
            {navigationSections.map((section) => {
              const active = section.id === activeSection.id
              return (
                <Link
                  aria-current={active ? "page" : undefined}
                  className={cn(
                    "shrink-0 rounded-full px-3 py-1.5 text-xs font-medium transition-colors sm:text-sm",
                    active
                      ? "top-section-link-active"
                      : "text-muted-foreground hover:bg-muted hover:text-foreground",
                  )}
                  key={section.id}
                  to={section.href}
                >
                  {intl.formatMessage({ id: section.titleMessageId })}
                </Link>
              )
            })}
          </nav>
          <div className="hidden min-w-0 border-l border-border pl-4 xl:block">
            <div className="truncate text-sm font-medium text-foreground">
              {page ? intl.formatMessage({ id: page.titleMessageId }) : null}
            </div>
            <p className="truncate text-xs text-muted-foreground">
              {page ? intl.formatMessage({ id: page.descriptionMessageId }) : null}
            </p>
          </div>
        </div>
        <div className="flex shrink-0 items-center gap-2 sm:gap-3">
          <div className="hidden items-center gap-2 rounded-full border border-border bg-background px-3 py-1.5 text-xs font-medium text-muted-foreground xl:flex">
            <StatusIcon
              className={cn("size-3.5", statusPresentation.iconClassName)}
            />
            {intl.formatMessage(
              { id: statusMessageId },
              { count: clusterStatus.total ?? 0 },
            )}
          </div>
          <div className="hidden lg:block">
            <ThemeSwitcher />
          </div>
          <div className="hidden lg:block">
            <LocaleSwitcher />
          </div>
          <div className="flex items-center gap-2 border-l border-border pl-2 sm:pl-3">
            <span className="hidden text-xs text-muted-foreground sm:inline">
              {authStatus === "readonly" ? intl.formatMessage({ id: "tasks.readOnly" }) : username}
            </span>
            {authStatus === "authenticated" ? (
              <Button
                aria-label={intl.formatMessage({ id: "common.logout" })}
                className="px-2 sm:px-3"
                onClick={logout}
                size="sm"
                variant="outline"
              >
                <Activity className="size-3.5" />
                <span className="hidden sm:inline">{intl.formatMessage({ id: "common.logout" })}</span>
              </Button>
            ) : null}
          </div>
        </div>
      </div>
    </header>
  )
}
