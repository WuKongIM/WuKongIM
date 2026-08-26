import type { ComponentType } from "react"
import { Navigate, createBrowserRouter, useLocation, type RouteObject } from "react-router-dom"

import { PublicOnlyRoute } from "@/auth/protected-route"
import { LoginPage } from "@/pages/login/page"
import { NotFoundPage, RouteErrorPage, RouteLoadingPage } from "@/pages/system/route-state-page"
import { defaultAppPath } from "@/lib/navigation"

function RedirectWithSearch({ tab, to }: { tab?: string; to: string }) {
  const location = useLocation()
  const params = new URLSearchParams(location.search)
  if (tab) {
    params.set("tab", tab)
  }
  const search = params.toString()
  return <Navigate replace to={`${to}${search ? `?${search}` : ""}`} />
}

function lazyComponent(load: () => Promise<ComponentType>): NonNullable<RouteObject["lazy"]> {
  return async () => ({ Component: await load() })
}

export const routes: RouteObject[] = [
  {
    path: "/login",
    element: (
      <PublicOnlyRoute>
        <LoginPage />
      </PublicOnlyRoute>
    ),
  },
  {
    path: "/",
    errorElement: <RouteErrorPage />,
    HydrateFallback: RouteLoadingPage,
    lazy: lazyComponent(async () => (await import("@/app/protected-app-shell")).ProtectedAppShell),
    children: [
      { index: true, element: <Navigate replace to={defaultAppPath} /> },
      // Cluster operations
      {
        path: "cluster/dashboard",
        lazy: lazyComponent(async () => (await import("@/pages/cluster-dashboard/page")).ClusterDashboardPage),
      },
      {
        path: "cluster/monitor",
        lazy: lazyComponent(async () => (await import("@/pages/cluster-monitor/page")).ClusterMonitorPage),
      },
      {
        path: "cluster/nodes",
        lazy: lazyComponent(async () => (await import("@/pages/nodes/page")).NodesPage),
      },
      {
        path: "cluster/node-config",
        lazy: lazyComponent(async () => (await import("@/pages/node-config/page")).NodeConfigPage),
      },
      {
        path: "cluster/slots",
        lazy: lazyComponent(async () => (await import("@/pages/slots/page")).SlotsPage),
      },
      {
        path: "cluster/channels",
        lazy: lazyComponent(async () => (await import("@/pages/cluster/channels/page")).ClusterChannelsPage),
      },
      {
        path: "cluster/plugins",
        lazy: lazyComponent(async () => (await import("@/pages/plugins/page")).PluginsPage),
      },
      {
        path: "cluster/tasks",
        lazy: lazyComponent(async () => (await import("@/pages/tasks/page")).TasksPage),
      },
      {
        path: "cluster/workqueues",
        lazy: lazyComponent(async () => (await import("@/pages/workqueues/page")).WorkqueuesPage),
      },
      {
        path: "cluster/system-logs",
        lazy: lazyComponent(async () => (await import("@/pages/app-logs/page")).AppLogsPage),
      },
      {
        path: "cluster/diagnostics",
        lazy: lazyComponent(async () => (await import("@/pages/cluster/diagnostics/page")).ClusterDiagnosticsPage),
      },
      {
        path: "cluster/backups",
        lazy: lazyComponent(async () => (await import("@/pages/backups/page")).BackupsPage),
      },
      // Business management
      { path: "business/dashboard", element: <Navigate replace to="/business/connections" /> },
      {
        path: "business/users",
        lazy: lazyComponent(async () => (await import("@/pages/users/page")).UsersPage),
      },
      {
        path: "business/channels",
        lazy: lazyComponent(async () => (await import("@/pages/channels-biz/page")).ChannelsBizPage),
      },
      {
        path: "business/messages",
        lazy: lazyComponent(async () => (await import("@/pages/messages/page")).MessagesPage),
      },
      {
        path: "business/conversations",
        lazy: lazyComponent(async () => (await import("@/pages/conversations/page")).ConversationsPage),
      },
      {
        path: "business/system-users",
        lazy: lazyComponent(async () => (await import("@/pages/system-users/page")).SystemUsersPage),
      },
      {
        path: "business/connections",
        lazy: lazyComponent(async () => (await import("@/pages/connections/page")).ConnectionsPage),
      },
      // System
      {
        path: "system/permissions",
        lazy: lazyComponent(async () => (await import("@/pages/settings/permissions/page")).PermissionsPage),
      },
      {
        path: "system/mcp",
        lazy: lazyComponent(async () => (await import("@/pages/settings/mcp/page")).MCPSettingsPage),
      },
      {
        path: "system/db",
        lazy: lazyComponent(async () => (await import("@/pages/db-inspect/page")).DBInspectPage),
      },
      {
        path: "system/webhooks",
        lazy: lazyComponent(async () => (await import("@/pages/settings/webhooks/page")).WebhooksPage),
      },
      { path: "system/connections", element: <RedirectWithSearch to="/business/connections" /> },
      // Legacy redirects
      { path: "dashboard", element: <Navigate replace to="/cluster/dashboard" /> },
      { path: "nodes", element: <Navigate replace to="/cluster/nodes" /> },
      { path: "onboarding", element: <Navigate replace to="/cluster/nodes" /> },
      { path: "slots", element: <Navigate replace to="/cluster/slots" /> },
      { path: "tasks", element: <Navigate replace to="/cluster/tasks" /> },
      { path: "workqueues", element: <Navigate replace to="/cluster/workqueues" /> },
      { path: "channel-cluster", element: <Navigate replace to="/cluster/channels" /> },
      { path: "channel-cluster/list", element: <Navigate replace to="/cluster/channels" /> },
      { path: "channel-cluster/unhealthy", element: <Navigate replace to="/cluster/channels" /> },
      { path: "channels", element: <Navigate replace to="/cluster/channels" /> },
      { path: "diagnostics", element: <Navigate replace to="/cluster/diagnostics?tab=trace" /> },
      { path: "network", element: <Navigate replace to="/cluster/diagnostics?tab=trace" /> },
      { path: "controller", element: <RedirectWithSearch tab="trace" to="/cluster/diagnostics" /> },
      { path: "slot-logs", element: <RedirectWithSearch tab="trace" to="/cluster/diagnostics" /> },
      { path: "app-logs", element: <Navigate replace to="/cluster/system-logs" /> },
      { path: "users", element: <Navigate replace to="/business/users" /> },
      { path: "channels-biz", element: <Navigate replace to="/business/channels" /> },
      { path: "messages", element: <Navigate replace to="/business/messages" /> },
      { path: "conversations", element: <Navigate replace to="/business/conversations" /> },
      { path: "system-users", element: <Navigate replace to="/business/system-users" /> },
      { path: "db-inspect", element: <Navigate replace to="/system/db" /> },
      { path: "settings/permissions", element: <Navigate replace to="/system/permissions" /> },
      { path: "settings/mcp", element: <Navigate replace to="/system/mcp" /> },
      { path: "settings/webhooks", element: <Navigate replace to="/system/webhooks" /> },
      { path: "connections", element: <RedirectWithSearch to="/business/connections" /> },
      { path: "*", element: <NotFoundPage /> },
    ],
  },
]

export const router = createBrowserRouter(routes)
