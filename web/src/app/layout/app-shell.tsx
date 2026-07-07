import { Outlet } from "react-router-dom"

import { SidebarNav } from "@/app/layout/sidebar-nav"
import { Topbar } from "@/app/layout/topbar"

export function AppShell() {
  return (
    <div className="relative flex h-screen flex-col overflow-hidden bg-background text-foreground">
      <Topbar />
      <div className="relative flex min-h-0 flex-1 flex-col lg:flex-row">
        <SidebarNav />
        <main className="min-h-0 min-w-0 flex-1 overflow-y-auto" role="main">
          <Outlet />
        </main>
      </div>
    </div>
  )
}
