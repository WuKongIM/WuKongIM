import { Search } from "lucide-react"
import { useIntl } from "react-intl"
import { NavLink, useLocation } from "react-router-dom"

import { useAuthStore } from "@/auth/auth-store"
import { LocaleSwitcher } from "@/components/i18n/locale-switcher"
import { Button } from "@/components/ui/button"
import { cn } from "@/lib/utils"
import {
  getActiveNavigationItem,
  getActiveNavigationSection,
  navigationSections,
} from "@/lib/navigation"

export function Topbar() {
  const intl = useIntl()
  const location = useLocation()
  const activeSection = getActiveNavigationSection(location.pathname)
  const page = getActiveNavigationItem(location.pathname)
  const username = useAuthStore((state) => state.username)
  const logout = useAuthStore((state) => state.logout)

  return (
    <header className="h-12 border-b border-border bg-background px-4" role="banner">
      <div className="flex h-full items-center justify-between gap-4">
        <div className="flex min-w-0 items-center gap-5">
          <div className="font-mono text-sm font-semibold tracking-[0.22em] text-foreground">WUKONGIM</div>
          <nav
            aria-label={intl.formatMessage({ id: "nav.topSections" })}
            className="flex items-center gap-1"
          >
            {navigationSections.map((section) => {
              const active = section.id === activeSection.id
              return (
                <NavLink
                  aria-current={active ? "page" : undefined}
                  className={cn(
                    "rounded-md px-3 py-1.5 text-sm font-medium transition-colors",
                    active
                      ? "bg-accent text-foreground"
                      : "text-muted-foreground hover:bg-muted/60 hover:text-foreground",
                  )}
                  key={section.id}
                  to={section.href}
                >
                  {intl.formatMessage({ id: section.titleMessageId })}
                </NavLink>
              )
            })}
          </nav>
          <div className="hidden min-w-0 border-l border-border pl-4 lg:block">
            <div className="truncate text-sm font-semibold text-foreground">
              {page ? intl.formatMessage({ id: page.titleMessageId }) : null}
            </div>
            <p className="truncate text-xs text-muted-foreground">
              {page ? intl.formatMessage({ id: page.descriptionMessageId }) : null}
            </p>
          </div>
        </div>
        <div className="flex shrink-0 items-center gap-3">
          <div className="flex items-center gap-2">
            <LocaleSwitcher />
            <Button size="sm" variant="outline">
              {intl.formatMessage({ id: "common.refresh" })}
            </Button>
            <Button size="sm" variant="outline">
              <Search className="size-3.5" />
              {intl.formatMessage({ id: "common.search" })}
            </Button>
          </div>
          <div className="flex items-center gap-2 border-l border-border pl-3">
            <span className="text-xs text-muted-foreground">{username}</span>
            <Button onClick={logout} size="sm" variant="outline">
              {intl.formatMessage({ id: "common.logout" })}
            </Button>
          </div>
        </div>
      </div>
    </header>
  )
}
