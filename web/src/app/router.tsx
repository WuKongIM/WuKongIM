import { Navigate, createBrowserRouter, useLocation, type RouteObject } from "react-router-dom"

import { AppShell } from "@/app/layout/app-shell"
import { ProtectedRoute, PublicOnlyRoute } from "@/auth/protected-route"
import { BusinessDashboardPage } from "@/pages/business-dashboard/page"
import { ChannelsBizPage } from "@/pages/channels-biz/page"
import { ClusterChannelsPage } from "@/pages/cluster/channels/page"
import { ClusterDiagnosticsPage } from "@/pages/cluster/diagnostics/page"
import { ConnectionsPage } from "@/pages/connections/page"
import { ConversationsPage } from "@/pages/conversations/page"
import { ClusterDashboardPage } from "@/pages/cluster-dashboard/page"
import { DBInspectPage } from "@/pages/db-inspect/page"
import { LoginPage } from "@/pages/login/page"
import { MessagesPage } from "@/pages/messages/page"
import { MonitorPage } from "@/pages/monitor/page"
import { NodesPage } from "@/pages/nodes/page"
import { PermissionsPage } from "@/pages/settings/permissions/page"
import { PluginsPage } from "@/pages/plugins/page"
import { SlotsPage } from "@/pages/slots/page"
import { SystemUsersPage } from "@/pages/system-users/page"
import { TasksPage } from "@/pages/tasks/page"
import { TopologyPage } from "@/pages/topology/page"
import { UsersPage } from "@/pages/users/page"
import { WebhooksPage } from "@/pages/settings/webhooks/page"
import { WorkqueuesPage } from "@/pages/workqueues/page"

function RedirectWithSearch({ tab, to }: { tab?: string; to: string }) {
  const location = useLocation()
  const params = new URLSearchParams(location.search)
  if (tab && !params.has("tab")) {
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
        <LoginPage />
      </PublicOnlyRoute>
    ),
  },
  {
    path: "/",
    element: (
      <ProtectedRoute>
        <AppShell />
      </ProtectedRoute>
    ),
    children: [
      { index: true, element: <Navigate replace to="/cluster/dashboard" /> },
      // Cluster operations
      { path: "cluster/dashboard", element: <ClusterDashboardPage /> },
      { path: "cluster/nodes", element: <NodesPage /> },
      { path: "cluster/slots", element: <SlotsPage /> },
      { path: "cluster/channels", element: <ClusterChannelsPage /> },
      { path: "cluster/plugins", element: <PluginsPage /> },
      { path: "cluster/tasks", element: <TasksPage /> },
      { path: "cluster/workqueues", element: <WorkqueuesPage /> },
      { path: "cluster/topology", element: <TopologyPage /> },
      { path: "cluster/diagnostics", element: <ClusterDiagnosticsPage /> },
      // Business management
      { path: "business/dashboard", element: <BusinessDashboardPage /> },
      { path: "business/monitor", element: <MonitorPage /> },
      { path: "business/users", element: <UsersPage /> },
      { path: "business/channels", element: <ChannelsBizPage /> },
      { path: "business/messages", element: <MessagesPage /> },
      { path: "business/conversations", element: <ConversationsPage /> },
      { path: "business/system-users", element: <SystemUsersPage /> },
      { path: "business/connections", element: <ConnectionsPage /> },
      // System
      { path: "system/permissions", element: <PermissionsPage /> },
      { path: "system/db", element: <DBInspectPage /> },
      { path: "system/webhooks", element: <WebhooksPage /> },
      { path: "system/connections", element: <RedirectWithSearch to="/business/connections" /> },
      // Legacy redirects
      { path: "dashboard", element: <Navigate replace to="/cluster/dashboard" /> },
      { path: "monitor", element: <Navigate replace to="/business/monitor" /> },
      { path: "nodes", element: <Navigate replace to="/cluster/nodes" /> },
      { path: "onboarding", element: <Navigate replace to="/cluster/nodes?panel=onboarding" /> },
      { path: "slots", element: <Navigate replace to="/cluster/slots" /> },
      { path: "tasks", element: <Navigate replace to="/cluster/tasks" /> },
      { path: "workqueues", element: <Navigate replace to="/cluster/workqueues" /> },
      { path: "topology", element: <Navigate replace to="/cluster/topology" /> },
      { path: "channel-cluster", element: <Navigate replace to="/cluster/channels" /> },
      { path: "channel-cluster/list", element: <Navigate replace to="/cluster/channels" /> },
      { path: "channel-cluster/unhealthy", element: <Navigate replace to="/cluster/channels" /> },
      { path: "channels", element: <Navigate replace to="/cluster/channels" /> },
      { path: "diagnostics", element: <Navigate replace to="/cluster/diagnostics?tab=trace" /> },
      { path: "network", element: <Navigate replace to="/cluster/diagnostics?tab=network" /> },
      { path: "controller", element: <RedirectWithSearch tab="controller-logs" to="/cluster/diagnostics" /> },
      { path: "slot-logs", element: <RedirectWithSearch tab="slot-logs" to="/cluster/diagnostics" /> },
      { path: "users", element: <Navigate replace to="/business/users" /> },
      { path: "channels-biz", element: <Navigate replace to="/business/channels" /> },
      { path: "messages", element: <Navigate replace to="/business/messages" /> },
      { path: "conversations", element: <Navigate replace to="/business/conversations" /> },
      { path: "system-users", element: <Navigate replace to="/business/system-users" /> },
      { path: "db-inspect", element: <Navigate replace to="/system/db" /> },
      { path: "settings/permissions", element: <Navigate replace to="/system/permissions" /> },
      { path: "settings/webhooks", element: <Navigate replace to="/system/webhooks" /> },
      { path: "connections", element: <RedirectWithSearch to="/business/connections" /> },
    ],
  },
]

export const router = createBrowserRouter(routes)
