import type { LucideIcon } from "lucide-react"
import { Activity, LayoutDashboard, Route } from "lucide-react"

export type NavigationChildItem = {
  /** Stable identifier used by the skeleton shell before routes are wired. */
  id: string
  /** URL placeholder for the future route target. */
  href: string
  /** Visible submenu label. */
  label: string
  /** Short text for future route metadata. */
  description: string
  /** Lucide icon rendered next to the submenu label. */
  icon: LucideIcon
}

export type NavigationItem = {
  /** Stable identifier used by the skeleton shell before routes are wired. */
  id: string
  /** URL placeholder for the future route target. */
  href: string
  /** Visible menu label. */
  label: string
  /** Short text for future route metadata. */
  description: string
  /** Lucide icon rendered next to the menu label. */
  icon: LucideIcon
  /** Optional submenu entries displayed under a top-level item. */
  children?: NavigationChildItem[]
}

export const navigationItems: NavigationItem[] = [
  {
    id: "overview",
    href: "#overview",
    label: "Overview",
    description: "Cluster and business runtime summary.",
    icon: LayoutDashboard,
  },
  {
    id: "monitor",
    href: "#monitor",
    label: "Monitor",
    description: "Runtime diagnostics and process-level signals.",
    icon: Activity,
    children: [
      {
        id: "monitor-goroutines",
        href: "#monitor-goroutines",
        label: "goroutines",
        description: "System goroutine distribution by subsystem, state, and stack group.",
        icon: Route,
      },
    ],
  },
]

export const activeNavigationItemId = "monitor"
export const activeNavigationChildId = "monitor-goroutines"
