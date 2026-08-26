import { useState } from "react"
import { useIntl } from "react-intl"
import { Outlet, useLocation } from "react-router-dom"

import { DocumentTitle } from "@/app/document-title"
import { SidebarNav } from "@/app/layout/sidebar-nav"
import { Topbar } from "@/app/layout/topbar"
import { getActiveNavigationItem } from "@/lib/navigation"

export function AppShell() {
  const intl = useIntl()
  const location = useLocation()
  const page = getActiveNavigationItem(location.pathname)
  const [mobileNavigationOpen, setMobileNavigationOpen] = useState(false)

  return (
    <div className="relative flex h-svh flex-col overflow-hidden bg-background text-foreground">
      <DocumentTitle titleMessageId={page?.titleMessageId ?? "route.notFound.title"} />
      <a
        className="absolute left-3 top-2 z-[60] -translate-y-20 rounded-md bg-primary px-3 py-2 text-sm font-medium text-primary-foreground transition-transform focus:translate-y-0"
        href="#main-content"
      >
        {intl.formatMessage({ id: "nav.skipToContent" })}
      </a>
      <Topbar onOpenNavigation={() => setMobileNavigationOpen(true)} />
      <div className="relative flex min-h-0 flex-1 flex-col lg:flex-row">
        <SidebarNav
          mobileOpen={mobileNavigationOpen}
          onMobileOpenChange={setMobileNavigationOpen}
        />
        <main className="min-h-0 min-w-0 flex-1 overflow-y-auto" id="main-content" role="main" tabIndex={-1}>
          <Outlet />
        </main>
      </div>
    </div>
  )
}
