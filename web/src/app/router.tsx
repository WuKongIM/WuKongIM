import { lazy, Suspense, type ComponentType } from "react"
import { Navigate, createBrowserRouter, useLocation, type RouteObject } from "react-router-dom"

import { AppShell } from "@/app/layout/app-shell"
import { ProtectedRoute, PublicOnlyRoute } from "@/auth/protected-route"
import { NotFoundPage, RouteErrorPage, RouteLoadingPage, RouteLoadingState } from "@/pages/system/route-state-page"
import { defaultAppPath } from "@/lib/navigation"

function lazyRouteElement<TModule, TExport extends keyof TModule>(
  load: () => Promise<TModule>,
  exportName: TExport,
  fullPage = false,
) {
  const LazyPage = lazy(async () => ({ default: (await load())[exportName] as ComponentType }))
  return (
    <Suspense fallback={fullPage ? <RouteLoadingPage /> : <RouteLoadingState />}>
      <LazyPage />
    </Suspense>
  )
}

function RedirectWithSearch({ tab, to }: { tab?: string; to: string }) {
  const location = useLocation()
  const params = new URLSearchParams(location.search)
  if (tab) {
    params.set("tab", tab)
  }
  const search = params.toString()
  return <Navigate replace to={`${to}${search ? `?${search}` : ""}`} />
}

export const routes: RouteObject[] = [
  {
    path: "/login",
    element: (
      <PublicOnlyRoute>
        {lazyRouteElement(() => import("@/pages/login/page"), "LoginPage", true)}
      </PublicOnlyRoute>
    ),
  },
  {
    path: "/",
    errorElement: <RouteErrorPage />,
    element: (
      <ProtectedRoute>
        <AppShell />
      </ProtectedRoute>
    ),
    children: [
      { index: true, element: <Navigate replace to={defaultAppPath} /> },
      // Cluster operations
      { path: "cluster/dashboard", element: lazyRouteElement(() => import("@/pages/cluster-dashboard/page"), "ClusterDashboardPage") },
      { path: "cluster/monitor", element: lazyRouteElement(() => import("@/pages/cluster-monitor/page"), "ClusterMonitorPage") },
      { path: "cluster/nodes", element: lazyRouteElement(() => import("@/pages/nodes/page"), "NodesPage") },
      { path: "cluster/node-config", element: lazyRouteElement(() => import("@/pages/node-config/page"), "NodeConfigPage") },
      { path: "cluster/slots", element: lazyRouteElement(() => import("@/pages/slots/page"), "SlotsPage") },
      { path: "cluster/channels", element: lazyRouteElement(() => import("@/pages/cluster/channels/page"), "ClusterChannelsPage") },
      { path: "cluster/plugins", element: lazyRouteElement(() => import("@/pages/plugins/page"), "PluginsPage") },
      { path: "cluster/tasks", element: lazyRouteElement(() => import("@/pages/tasks/page"), "TasksPage") },
      { path: "cluster/workqueues", element: lazyRouteElement(() => import("@/pages/workqueues/page"), "WorkqueuesPage") },
      { path: "cluster/system-logs", element: lazyRouteElement(() => import("@/pages/app-logs/page"), "AppLogsPage") },
      { path: "cluster/diagnostics", element: lazyRouteElement(() => import("@/pages/cluster/diagnostics/page"), "ClusterDiagnosticsPage") },
      { path: "cluster/backups", element: lazyRouteElement(() => import("@/pages/backups/page"), "BackupsPage") },
      // Business management
      { path: "business/dashboard", element: lazyRouteElement(() => import("@/pages/business-dashboard/page"), "BusinessDashboardPage") },
      { path: "business/users", element: lazyRouteElement(() => import("@/pages/users/page"), "UsersPage") },
      { path: "business/channels", element: lazyRouteElement(() => import("@/pages/channels-biz/page"), "ChannelsBizPage") },
      { path: "business/messages", element: lazyRouteElement(() => import("@/pages/messages/page"), "MessagesPage") },
      { path: "business/conversations", element: lazyRouteElement(() => import("@/pages/conversations/page"), "ConversationsPage") },
      { path: "business/system-users", element: lazyRouteElement(() => import("@/pages/system-users/page"), "SystemUsersPage") },
      { path: "business/connections", element: lazyRouteElement(() => import("@/pages/connections/page"), "ConnectionsPage") },
      // System
      { path: "system/permissions", element: lazyRouteElement(() => import("@/pages/settings/permissions/page"), "PermissionsPage") },
      { path: "system/mcp", element: lazyRouteElement(() => import("@/pages/settings/mcp/page"), "MCPSettingsPage") },
      { path: "system/db", element: lazyRouteElement(() => import("@/pages/db-inspect/page"), "DBInspectPage") },
      { path: "system/webhooks", element: lazyRouteElement(() => import("@/pages/settings/webhooks/page"), "WebhooksPage") },
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
