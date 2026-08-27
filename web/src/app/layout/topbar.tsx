import { CircleHelp, LogOut, Menu, ShieldAlert, ShieldCheck, UserRound } from "lucide-react"
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
  defaultAppPath,
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
  const PageIcon = page?.icon
  const StatusIcon = clusterStatus.health === "healthy"
    ? ShieldCheck
    : clusterStatus.health === "unknown"
      ? CircleHelp
      : ShieldAlert

  return (
    <header
      className="sticky top-0 z-30 border-b border-border/80 bg-background/95 px-3 backdrop-blur-xl supports-[backdrop-filter]:bg-background/85 sm:px-4"
      role="banner"
    >
      <div className="flex h-16 items-center justify-between gap-3">
        <div className="flex min-w-0 items-center gap-3 xl:gap-4">
          <Button
            aria-label={intl.formatMessage({ id: "nav.openNavigation" })}
            className="rounded-xl lg:hidden"
            onClick={onOpenNavigation}
            size="icon"
            variant="ghost"
          >
            <Menu aria-hidden />
          </Button>
          <Link
            aria-label="WuKongIM"
            className="group flex shrink-0 items-center gap-2.5 rounded-xl outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
            to={defaultAppPath}
          >
            <span
              aria-hidden
              className="grid size-9 place-items-center overflow-hidden rounded-[10px] shadow-sm ring-1 ring-black/8 transition-transform group-hover:-translate-y-0.5 dark:ring-white/15"
              data-brand-mark
            >
              <img alt="" className="size-full object-cover" src="/logo.png" />
            </span>
            <div className="hidden sm:block">
              <div className="text-sm font-semibold tracking-[-0.02em] text-foreground">WuKongIM</div>
              <div className="mt-0.5 font-mono text-[9px] font-medium uppercase tracking-[0.18em] text-muted-foreground">
                {intl.formatMessage({ id: "shell.operationsCockpit" })}
              </div>
            </div>
          </Link>
          <nav
            aria-label={intl.formatMessage({ id: "nav.topSections" })}
            className="hidden min-w-0 items-center gap-1 rounded-full border border-border/70 bg-muted/70 p-1 lg:flex"
          >
            {navigationSections.map((section) => {
              const active = section.id === activeSection.id
              return (
                <Link
                  aria-current={active ? "page" : undefined}
                  className={cn(
                    "shrink-0 rounded-full px-3.5 py-1.5 text-sm font-medium transition-all",
                    active
                      ? "top-section-link-active"
                      : "text-muted-foreground hover:bg-background/80 hover:text-foreground",
                  )}
                  key={section.id}
                  to={section.href}
                >
                  {intl.formatMessage({ id: section.titleMessageId })}
                </Link>
              )
            })}
          </nav>
          {page && PageIcon ? (
            <div className="hidden min-w-0 items-center gap-2.5 border-l border-border/80 pl-4 xl:flex">
              <span className="grid size-8 shrink-0 place-items-center rounded-lg bg-accent text-accent-foreground">
                <PageIcon aria-hidden className="size-4" />
              </span>
              <div className="min-w-0 max-w-[210px] 2xl:max-w-[280px]">
                <div className="truncate text-sm font-medium text-foreground">
                  {intl.formatMessage({ id: page.titleMessageId })}
                </div>
                <p className="hidden truncate text-xs text-muted-foreground 2xl:block">
                  {intl.formatMessage({ id: page.descriptionMessageId })}
                </p>
              </div>
            </div>
          ) : null}
        </div>
        <div className="flex shrink-0 items-center gap-1.5 sm:gap-2">
          <div
            aria-live="polite"
            className="hidden items-center gap-2 rounded-full border border-border/70 bg-muted/60 px-3 py-2 text-xs font-medium text-muted-foreground 2xl:flex"
          >
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
          <div className="flex items-center gap-1.5 border-l border-border/80 pl-2 sm:pl-3">
            <span
              aria-hidden
              className="hidden size-8 place-items-center rounded-full bg-secondary text-secondary-foreground sm:grid"
            >
              <UserRound className="size-3.5" />
            </span>
            <span className="hidden max-w-28 truncate text-xs font-medium text-foreground 2xl:inline">
              {authStatus === "readonly" ? intl.formatMessage({ id: "tasks.readOnly" }) : username}
            </span>
            {authStatus === "authenticated" ? (
              <Button
                aria-label={intl.formatMessage({ id: "common.logout" })}
                className="rounded-full text-muted-foreground hover:text-foreground"
                onClick={logout}
                size="icon"
                title={intl.formatMessage({ id: "common.logout" })}
                variant="ghost"
              >
                <LogOut className="size-3.5" />
              </Button>
            ) : null}
          </div>
        </div>
      </div>
    </header>
  )
}
