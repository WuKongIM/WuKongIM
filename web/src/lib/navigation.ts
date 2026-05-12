import type { LucideIcon } from "lucide-react"
import {
  Activity,
  Cable,
  Database,
  GitPullRequestArrow,
  LayoutDashboard,
  MessageSquare,
  Radar,
  Radio,
  ScrollText,
  SearchCode,
  Server,
  Settings,
  Shield,
  Users,
  Waypoints,
  Webhook,
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
      {
        href: "/monitor",
        titleMessageId: "nav.monitor.title",
        descriptionMessageId: "nav.monitor.description",
        icon: Activity,
      },
    ],
  },
  {
    labelMessageId: "nav.group.globalCluster",
    items: [
      {
        href: "/nodes",
        titleMessageId: "nav.nodes.title",
        descriptionMessageId: "nav.nodes.description",
        icon: Server,
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
      {
        href: "/controller",
        titleMessageId: "nav.controller.title",
        descriptionMessageId: "nav.controller.description",
        icon: ScrollText,
      },
      {
        href: "/topology",
        titleMessageId: "nav.topology.title",
        descriptionMessageId: "nav.topology.description",
        icon: Waypoints,
      },
    ],
  },
  {
    labelMessageId: "nav.group.channelCluster",
    items: [
      {
        href: "/channel-cluster",
        titleMessageId: "nav.channelCluster.title",
        descriptionMessageId: "nav.channelCluster.description",
        icon: Radio,
      },
      {
        href: "/channel-cluster/list",
        titleMessageId: "nav.channelClusterList.title",
        descriptionMessageId: "nav.channelClusterList.description",
        icon: Radio,
      },
      {
        href: "/channel-cluster/unhealthy",
        titleMessageId: "nav.channelClusterUnhealthy.title",
        descriptionMessageId: "nav.channelClusterUnhealthy.description",
        icon: Radio,
      },
    ],
  },
  {
    labelMessageId: "nav.group.business",
    items: [
      {
        href: "/users",
        titleMessageId: "nav.users.title",
        descriptionMessageId: "nav.users.description",
        icon: Users,
      },
      {
        href: "/channels-biz",
        titleMessageId: "nav.channelsBiz.title",
        descriptionMessageId: "nav.channelsBiz.description",
        icon: MessageSquare,
      },
      {
        href: "/messages",
        titleMessageId: "nav.messages.title",
        descriptionMessageId: "nav.messages.description",
        icon: MessageSquare,
      },
      {
        href: "/system-users",
        titleMessageId: "nav.systemUsers.title",
        descriptionMessageId: "nav.systemUsers.description",
        icon: Shield,
      },
    ],
  },
  {
    labelMessageId: "nav.group.diagnostics",
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
        href: "/connections",
        titleMessageId: "nav.connections.title",
        descriptionMessageId: "nav.connections.description",
        icon: Cable,
      },
      {
        href: "/slot-logs",
        titleMessageId: "nav.slotLogs.title",
        descriptionMessageId: "nav.slotLogs.description",
        icon: Database,
      },
    ],
  },
  {
    labelMessageId: "nav.group.settings",
    items: [
      {
        href: "/settings/permissions",
        titleMessageId: "nav.permissions.title",
        descriptionMessageId: "nav.permissions.description",
        icon: Settings,
      },
      {
        href: "/settings/webhooks",
        titleMessageId: "nav.webhooks.title",
        descriptionMessageId: "nav.webhooks.description",
        icon: Webhook,
      },
    ],
  },
]

export const navigationItems = navigationGroups.flatMap((group) => group.items)

export const pageMetadata = new Map(
  navigationItems.map((item) => [item.href, item] as const),
)
