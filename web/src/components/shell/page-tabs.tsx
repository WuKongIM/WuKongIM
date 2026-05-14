import { cn } from "@/lib/utils"

export type PageTab = {
  id: string
  label: string
}

type PageTabsProps = {
  activeTab: string
  tabs: PageTab[]
  onTabChange: (tab: string) => void
  className?: string
}

export function PageTabs({ activeTab, tabs, onTabChange, className }: PageTabsProps) {
  return (
    <div
      aria-label="Page tabs"
      className={cn("flex flex-wrap gap-2 border-b border-border px-5 pt-4", className)}
      role="tablist"
    >
      {tabs.map((tab) => {
        const active = tab.id === activeTab
        return (
          <button
            aria-selected={active}
            className={cn(
              "border-b-2 px-3 py-2 text-sm font-medium text-muted-foreground transition-colors",
              active
                ? "border-[var(--status-healthy)] text-foreground"
                : "border-transparent hover:text-foreground",
            )}
            key={tab.id}
            onClick={() => onTabChange(tab.id)}
            role="tab"
            type="button"
          >
            {tab.label}
          </button>
        )
      })}
    </div>
  )
}
