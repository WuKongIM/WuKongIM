import type { LucideIcon } from "lucide-react"
import {
  Cable,
  Database,
  GitPullRequestArrow,
  LayoutDashboard,
  MessageSquare,
  Radar,
  SearchCode,
  ScrollText,
  Server,
  Waypoints,
} from "lucide-react"

export type NavigationItem = {
  href: string
  titleMessageId: string
  descriptionMessageId: string
  icon: LucideIcon
}

export type NavigationGroup = {
  labelMessageId: string
  items: NavigationItem[]
}

export const navigationGroups: NavigationGroup[] = [
  {
    labelMessageId: "nav.group.overview",
    items: [
      {
        href: "/dashboard",
        titleMessageId: "nav.dashboard.title",
        descriptionMessageId: "nav.dashboard.description",
        icon: LayoutDashboard,
      },
    ],
  },
  {
    labelMessageId: "nav.group.runtime",
    items: [
      {
        href: "/nodes",
        titleMessageId: "nav.nodes.title",
        descriptionMessageId: "nav.nodes.description",
        icon: Server,
      },
      {
        href: "/channels",
        titleMessageId: "nav.channels.title",
        descriptionMessageId: "nav.channels.description",
        icon: MessageSquare,
      },
      {
        href: "/messages",
        titleMessageId: "nav.messages.title",
        descriptionMessageId: "nav.messages.description",
        icon: MessageSquare,
      },
      {
        href: "/connections",
        titleMessageId: "nav.connections.title",
        descriptionMessageId: "nav.connections.description",
        icon: Cable,
      },
      {
        href: "/slots",
        titleMessageId: "nav.slots.title",
        descriptionMessageId: "nav.slots.description",
        icon: Database,
      },
      {
        href: "/onboarding",
        titleMessageId: "nav.onboarding.title",
        descriptionMessageId: "nav.onboarding.description",
        icon: GitPullRequestArrow,
      },
    ],
  },
  {
    labelMessageId: "nav.group.observability",
    items: [
      {
        href: "/diagnostics",
        titleMessageId: "nav.diagnostics.title",
        descriptionMessageId: "nav.diagnostics.description",
        icon: SearchCode,
      },
      {
        href: "/network",
        titleMessageId: "nav.network.title",
        descriptionMessageId: "nav.network.description",
        icon: Radar,
      },
      {
        href: "/controller",
        titleMessageId: "nav.controller.title",
        descriptionMessageId: "nav.controller.description",
        icon: ScrollText,
      },
      {
        href: "/slot-logs",
        titleMessageId: "nav.slotLogs.title",
        descriptionMessageId: "nav.slotLogs.description",
        icon: Database,
      },
      {
        href: "/topology",
        titleMessageId: "nav.topology.title",
        descriptionMessageId: "nav.topology.description",
        icon: Waypoints,
      },
    ],
  },
]

export const navigationItems = navigationGroups.flatMap((group) => group.items)

export const pageMetadata = new Map(
  navigationItems.map((item) => [item.href, item] as const),
)
