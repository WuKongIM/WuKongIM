import { Outlet } from "react-router-dom"

import { SidebarNav } from "@/app/layout/sidebar-nav"
import { Topbar } from "@/app/layout/topbar"

export function AppShell() {
  return (
    <div className="relative flex h-screen flex-col overflow-hidden bg-background text-foreground">
      <div
        aria-hidden
        className="pointer-events-none fixed inset-0 bg-[radial-gradient(circle_at_18%_0%,rgba(101,216,138,0.10),transparent_28rem),radial-gradient(circle_at_90%_12%,rgba(93,168,255,0.08),transparent_24rem)]"
      />
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
