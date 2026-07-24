import type { LucideIcon } from "lucide-react"
import {
  Activity,
  Archive,
  Cable,
  ClipboardList,
  Database,
  FileText,
  Gauge,
  LayoutDashboard,
  MessageSquare,
  Network,
  Puzzle,
  Radar,
  Server,
  Settings,
  Shield,
  SlidersHorizontal,
  Users,
  Webhook,
} from "lucide-react"

export type NavigationSectionId = "cluster" | "business" | "system"

export type NavigationItem = {
  href: string
  titleMessageId: string
  descriptionMessageId: string
  pathLabelMessageId: string
  icon: LucideIcon
  aliases?: string[]
}

export type NavigationSection = {
  id: NavigationSectionId
  href: string
  titleMessageId: string
  items: NavigationItem[]
}

export const defaultAppPath = "/cluster/monitor"

export const navigationSections: NavigationSection[] = [
  {
    id: "cluster",
    href: defaultAppPath,
    titleMessageId: "nav.section.cluster",
    items: [
      {
        href: "/cluster/monitor",
        titleMessageId: "nav.clusterMonitor.title",
        descriptionMessageId: "nav.clusterMonitor.description",
        pathLabelMessageId: "nav.path.cluster.monitor",
        icon: Activity,
      },
      {
        href: "/cluster/nodes",
        titleMessageId: "nav.nodes.title",
        descriptionMessageId: "nav.nodes.description",
        pathLabelMessageId: "nav.path.cluster.nodes",
        icon: Server,
        aliases: ["/nodes", "/onboarding"],
      },
      {
        href: "/cluster/slots",
        titleMessageId: "nav.slots.title",
        descriptionMessageId: "nav.slots.description",
        pathLabelMessageId: "nav.path.cluster.slots",
        icon: Database,
        aliases: ["/slots"],
      },
      {
        href: "/cluster/channels",
        titleMessageId: "nav.channelCluster.title",
        descriptionMessageId: "nav.channelCluster.description",
        pathLabelMessageId: "nav.path.cluster.channels",
        icon: Network,
        aliases: ["/channel-cluster", "/channel-cluster/list", "/channel-cluster/unhealthy", "/channels"],
      },
      {
        href: "/cluster/plugins",
        titleMessageId: "nav.plugins.title",
        descriptionMessageId: "nav.plugins.description",
        pathLabelMessageId: "nav.path.cluster.plugins",
        icon: Puzzle,
      },
      {
        href: "/cluster/tasks",
        titleMessageId: "nav.tasks.title",
        descriptionMessageId: "nav.tasks.description",
        pathLabelMessageId: "nav.path.cluster.tasks",
        icon: ClipboardList,
        aliases: ["/tasks"],
      },
      {
        href: "/cluster/workqueues",
        titleMessageId: "nav.workqueues.title",
        descriptionMessageId: "nav.workqueues.description",
        pathLabelMessageId: "nav.path.cluster.workqueues",
        icon: Gauge,
        aliases: ["/workqueues"],
      },
      {
        href: "/cluster/node-config",
        titleMessageId: "nav.nodeConfig.title",
        descriptionMessageId: "nav.nodeConfig.description",
        pathLabelMessageId: "nav.path.cluster.nodeConfig",
        icon: SlidersHorizontal,
      },
      {
        href: "/cluster/system-logs",
        titleMessageId: "nav.systemLogs.title",
        descriptionMessageId: "nav.systemLogs.description",
        pathLabelMessageId: "nav.path.cluster.systemLogs",
        icon: FileText,
        aliases: ["/app-logs"],
      },
      {
        href: "/cluster/diagnostics",
        titleMessageId: "nav.diagnostics.title",
        descriptionMessageId: "nav.diagnostics.description",
        pathLabelMessageId: "nav.path.cluster.diagnostics",
        icon: Radar,
        aliases: ["/diagnostics", "/network", "/controller", "/slot-logs"],
      },
      {
        href: "/cluster/backups",
        titleMessageId: "nav.backups.title",
        descriptionMessageId: "nav.backups.description",
        pathLabelMessageId: "nav.path.cluster.backups",
        icon: Archive,
      },
    ],
  },
  {
    id: "business",
    href: "/business/connections",
    titleMessageId: "nav.section.business",
    items: [
      {
        href: "/business/connections",
        titleMessageId: "nav.connections.title",
        descriptionMessageId: "nav.connections.description",
        pathLabelMessageId: "nav.path.business.connections",
        icon: Cable,
        aliases: ["/connections"],
      },
      {
        href: "/business/users",
        titleMessageId: "nav.users.title",
        descriptionMessageId: "nav.users.description",
        pathLabelMessageId: "nav.path.business.users",
        icon: Users,
        aliases: ["/users"],
      },
      {
        href: "/business/channels",
        titleMessageId: "nav.channelsBiz.title",
        descriptionMessageId: "nav.channelsBiz.description",
        pathLabelMessageId: "nav.path.business.channels",
        icon: MessageSquare,
        aliases: ["/channels-biz"],
      },
      {
        href: "/business/conversations",
        titleMessageId: "nav.conversations.title",
        descriptionMessageId: "nav.conversations.description",
        pathLabelMessageId: "nav.path.business.conversations",
        icon: MessageSquare,
        aliases: ["/conversations"],
      },
      {
        href: "/business/messages",
        titleMessageId: "nav.messages.title",
        descriptionMessageId: "nav.messages.description",
        pathLabelMessageId: "nav.path.business.messages",
        icon: MessageSquare,
        aliases: ["/messages"],
      },
      {
        href: "/business/system-users",
        titleMessageId: "nav.systemUsers.title",
        descriptionMessageId: "nav.systemUsers.description",
        pathLabelMessageId: "nav.path.business.systemUsers",
        icon: Shield,
        aliases: ["/system-users"],
      },
    ],
  },
  {
    id: "system",
    href: "/system/permissions",
    titleMessageId: "nav.section.system",
    items: [
      {
        href: "/system/permissions",
        titleMessageId: "nav.permissions.title",
        descriptionMessageId: "nav.permissions.description",
        pathLabelMessageId: "nav.path.system.permissions",
        icon: Settings,
        aliases: ["/settings/permissions"],
      },
      {
        href: "/system/db",
        titleMessageId: "nav.dbInspect.title",
        descriptionMessageId: "nav.dbInspect.description",
        pathLabelMessageId: "nav.path.system.dbInspect",
        icon: Database,
        aliases: ["/db-inspect"],
      },
      {
        href: "/system/webhooks",
        titleMessageId: "nav.webhooks.title",
        descriptionMessageId: "nav.webhooks.description",
        pathLabelMessageId: "nav.path.system.webhooks",
        icon: Webhook,
        aliases: ["/settings/webhooks"],
      },
    ],
  },
]

export const navigationItems = navigationSections.flatMap((section) => section.items)

const pageOnlyNavigationItems: NavigationItem[] = [
  {
    href: "/cluster/dashboard",
    titleMessageId: "nav.clusterDashboard.title",
    descriptionMessageId: "nav.clusterDashboard.description",
    pathLabelMessageId: "nav.path.cluster.dashboard",
    icon: LayoutDashboard,
    aliases: ["/dashboard"],
  },
  {
    href: "/business/dashboard",
    titleMessageId: "nav.businessDashboard.title",
    descriptionMessageId: "nav.businessDashboard.description",
    pathLabelMessageId: "nav.path.business.dashboard",
    icon: LayoutDashboard,
  },
]

export const pageMetadata = new Map(
  [...navigationItems, ...pageOnlyNavigationItems].map((item) => [item.href, item] as const),
)

export const legacyRouteRedirects: Record<string, string> = {
  "/dashboard": "/cluster/dashboard",
  "/nodes": "/cluster/nodes",
  "/onboarding": "/cluster/nodes",
  "/slots": "/cluster/slots",
  "/tasks": "/cluster/tasks",
  "/workqueues": "/cluster/workqueues",
  "/channel-cluster": "/cluster/channels",
  "/channel-cluster/list": "/cluster/channels",
  "/channel-cluster/unhealthy": "/cluster/channels",
  "/channels": "/cluster/channels",
  "/diagnostics": "/cluster/diagnostics?tab=trace",
  "/network": "/cluster/diagnostics?tab=trace",
  "/controller": "/cluster/diagnostics?tab=trace",
  "/slot-logs": "/cluster/diagnostics?tab=trace",
  "/app-logs": "/cluster/system-logs",
  "/users": "/business/users",
  "/channels-biz": "/business/channels",
  "/messages": "/business/messages",
  "/conversations": "/business/conversations",
  "/system-users": "/business/system-users",
  "/db-inspect": "/system/db",
  "/settings/permissions": "/system/permissions",
  "/settings/webhooks": "/system/webhooks",
  "/connections": "/business/connections",
}

function matchesItem(pathname: string, item: NavigationItem) {
  return pathname === item.href || pathname.startsWith(`${item.href}/`) || Boolean(item.aliases?.includes(pathname))
}

export function getActiveNavigationItem(pathname: string) {
  return (
    navigationItems.find((item) => matchesItem(pathname, item)) ??
    pageOnlyNavigationItems.find((item) => matchesItem(pathname, item)) ??
    pageMetadata.get("/cluster/monitor")
  )
}

export function getActiveNavigationSection(pathname: string) {
  const activeItem = navigationItems.find((item) => matchesItem(pathname, item))
  if (activeItem) {
    return navigationSections.find((section) => section.items.some((item) => item.href === activeItem.href)) ?? navigationSections[0]
  }

  if (pathname === "/dashboard" || pathname.startsWith("/cluster/")) {
    return navigationSections.find((section) => section.id === "cluster") ?? navigationSections[0]
  }
  if (pathname.startsWith("/business/")) {
    return navigationSections.find((section) => section.id === "business") ?? navigationSections[0]
  }
  if (pathname.startsWith("/system/")) {
    return navigationSections.find((section) => section.id === "system") ?? navigationSections[0]
  }

  return navigationSections[0]
}

// Compatibility for older imports while the shell is being migrated.
export const navigationGroups = navigationSections.map((section) => ({
  labelMessageId: section.titleMessageId,
  items: section.items,
}))
