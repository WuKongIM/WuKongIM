import { Bell, ChevronRight, CircleHelp, Search } from "lucide-react"

import { SidebarNav } from "@/components/layout/sidebar-nav"
import { GoroutinesPage } from "@/pages/monitor/goroutines-page"

export function AppShell() {
  return (
    <div className="flex h-screen overflow-hidden bg-[var(--app-background)] text-foreground" data-testid="app-shell">
      <SidebarNav />
      <main aria-label="Content workspace" className="min-w-0 flex-1 overflow-y-auto" role="main">
        <div className="mx-auto flex min-h-full w-full max-w-[1440px] flex-col px-6 py-5 lg:px-10">
          <header className="flex min-h-12 items-center justify-between gap-4 border-b border-border/80 pb-5" role="banner">
            <div className="min-w-0">
              <div className="flex items-center gap-2 text-xs font-medium text-muted-foreground">
                <span>Web V2</span>
                <ChevronRight className="size-3.5" aria-hidden />
                <span>Monitor</span>
                <ChevronRight className="size-3.5" aria-hidden />
                <span>goroutines</span>
              </div>
              <h1 className="mt-1 truncate text-2xl font-semibold text-foreground" id="goroutines-title">
                Goroutines
              </h1>
              <p className="mt-1 text-sm text-muted-foreground">System goroutine distribution</p>
            </div>
            <div className="flex shrink-0 items-center gap-2">
              <button className="icon-button" type="button" aria-label="Search">
                <Search className="size-4" aria-hidden />
              </button>
              <button className="icon-button" type="button" aria-label="Notifications">
                <Bell className="size-4" aria-hidden />
              </button>
              <button className="icon-button" type="button" aria-label="Help">
                <CircleHelp className="size-4" aria-hidden />
              </button>
            </div>
          </header>

          <GoroutinesPage />
        </div>
      </main>
    </div>
  )
}
