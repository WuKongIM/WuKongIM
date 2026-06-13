import { ChevronDown, ChevronRight, PanelLeft } from "lucide-react"

import { activeNavigationChildId, activeNavigationItemId, navigationItems } from "@/lib/navigation"

export function SidebarNav() {
  return (
    <nav
      aria-label="Primary navigation"
      className="flex w-[284px] shrink-0 flex-col border-r border-sidebar-border bg-sidebar px-3 py-4 text-sidebar-foreground"
    >
      <div className="flex items-center gap-3 px-2">
        <div className="grid size-9 place-items-center rounded-lg bg-sidebar-primary text-sidebar-primary-foreground">
          <PanelLeft className="size-4" aria-hidden />
        </div>
        <div className="min-w-0">
          <div className="truncate text-sm font-semibold">WuKongIM</div>
          <div className="truncate text-xs text-sidebar-muted">Web console v2</div>
        </div>
      </div>

      <div className="mt-6 flex min-h-0 flex-1 flex-col gap-1 overflow-y-auto">
        {navigationItems.map((item) => {
          const active = item.id === activeNavigationItemId
          const expanded = active && item.children
          return (
            <div className="space-y-1" key={item.id}>
              <a
                className={active ? "sidebar-link sidebar-link-active" : "sidebar-link"}
                href={item.href}
                aria-expanded={expanded ? true : undefined}
              >
                <item.icon className="size-4 shrink-0" aria-hidden />
                <span className="min-w-0 flex-1 truncate">{item.label}</span>
                {expanded ? (
                  <ChevronDown className="size-4 shrink-0" aria-hidden />
                ) : (
                  <ChevronRight className="size-4 shrink-0 text-sidebar-muted/60" aria-hidden />
                )}
              </a>
              {expanded ? (
                <div className="ml-4 border-l border-sidebar-border pl-3">
                  {item.children?.map((child) => {
                    const childActive = child.id === activeNavigationChildId
                    return (
                      <a
                        aria-current={childActive ? "page" : undefined}
                        className={childActive ? "sidebar-child-link sidebar-child-link-active" : "sidebar-child-link"}
                        href={child.href}
                        key={child.id}
                      >
                        <child.icon className="size-3.5 shrink-0" aria-hidden />
                        <span className="min-w-0 flex-1 truncate">{child.label}</span>
                      </a>
                    )
                  })}
                </div>
              ) : null}
            </div>
          )
        })}
      </div>

      <div className="mt-4 rounded-lg border border-sidebar-border bg-white/70 p-3">
        <div className="text-xs font-medium uppercase text-sidebar-muted">Environment</div>
        <div className="mt-2 flex items-center justify-between gap-3 text-sm">
          <span className="font-medium">Local shell</span>
          <span className="rounded-md bg-primary/10 px-2 py-1 text-xs font-medium text-primary">Ready</span>
        </div>
      </div>
    </nav>
  )
}
