import { Outlet } from "react-router-dom"

import { SidebarNav } from "@/app/layout/sidebar-nav"
import { Topbar } from "@/app/layout/topbar"

export function AppShell() {
  return (
    <div className="flex min-h-screen bg-background text-foreground">
      <SidebarNav />
      <div className="flex min-w-0 flex-1 flex-col">
        <Topbar />
        <main className="flex-1" role="main">
          <Outlet />
        </main>
      </div>
    </div>
  )
}
