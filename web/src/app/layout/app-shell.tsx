import { Outlet } from "react-router-dom"

import { SidebarNav } from "@/app/layout/sidebar-nav"
import { Topbar } from "@/app/layout/topbar"

export function AppShell() {
  return (
    <div className="relative min-h-screen overflow-x-hidden bg-background text-foreground">
      <div
        aria-hidden
        className="pointer-events-none fixed inset-0 bg-[radial-gradient(circle_at_18%_0%,rgba(101,216,138,0.10),transparent_28rem),radial-gradient(circle_at_90%_12%,rgba(93,168,255,0.08),transparent_24rem)]"
      />
      <Topbar />
      <div className="relative flex min-h-[calc(100vh-3.5rem)] flex-col lg:flex-row">
        <SidebarNav />
        <main className="min-w-0 flex-1" role="main">
          <Outlet />
        </main>
      </div>
    </div>
  )
}
