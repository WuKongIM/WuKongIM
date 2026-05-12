import { Navigate, createBrowserRouter, type RouteObject } from "react-router-dom"

import { AppShell } from "@/app/layout/app-shell"
import { ProtectedRoute, PublicOnlyRoute } from "@/auth/protected-route"
import { ChannelClusterPage } from "@/pages/channel-cluster/page"
import { ChannelClusterListPage } from "@/pages/channel-cluster/list/page"
import { ChannelClusterUnhealthyPage } from "@/pages/channel-cluster/unhealthy/page"
import { ChannelsBizPage } from "@/pages/channels-biz/page"
import { ConnectionsPage } from "@/pages/connections/page"
import { ControllerPage } from "@/pages/controller/page"
import { DashboardPage } from "@/pages/dashboard/page"
import { DiagnosticsPage } from "@/pages/diagnostics/page"
import { LoginPage } from "@/pages/login/page"
import { MessagesPage } from "@/pages/messages/page"
import { MonitorPage } from "@/pages/monitor/page"
import { NetworkPage } from "@/pages/network/page"
import { NodesPage } from "@/pages/nodes/page"
import { OnboardingPage } from "@/pages/onboarding/page"
import { PermissionsPage } from "@/pages/settings/permissions/page"
import { SlotLogsPage } from "@/pages/slot-logs/page"
import { SlotsPage } from "@/pages/slots/page"
import { SystemUsersPage } from "@/pages/system-users/page"
import { TopologyPage } from "@/pages/topology/page"
import { UsersPage } from "@/pages/users/page"
import { WebhooksPage } from "@/pages/settings/webhooks/page"

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
      { index: true, element: <Navigate replace to="/dashboard" /> },
      // Overview
      { path: "dashboard", element: <DashboardPage /> },
      { path: "monitor", element: <MonitorPage /> },
      // Global Cluster
      { path: "nodes", element: <NodesPage /> },
      { path: "slots", element: <SlotsPage /> },
      { path: "onboarding", element: <OnboardingPage /> },
      { path: "controller", element: <ControllerPage /> },
      { path: "topology", element: <TopologyPage /> },
      // Channel Cluster
      { path: "channel-cluster", element: <ChannelClusterPage /> },
      { path: "channel-cluster/list", element: <ChannelClusterListPage /> },
      { path: "channel-cluster/unhealthy", element: <ChannelClusterUnhealthyPage /> },
      // Business
      { path: "users", element: <UsersPage /> },
      { path: "channels-biz", element: <ChannelsBizPage /> },
      { path: "messages", element: <MessagesPage /> },
      { path: "system-users", element: <SystemUsersPage /> },
      // Diagnostics
      { path: "diagnostics", element: <DiagnosticsPage /> },
      { path: "network", element: <NetworkPage /> },
      { path: "connections", element: <ConnectionsPage /> },
      { path: "slot-logs", element: <SlotLogsPage /> },
      // Settings
      { path: "settings/permissions", element: <PermissionsPage /> },
      { path: "settings/webhooks", element: <WebhooksPage /> },
      // Legacy redirect
      { path: "channels", element: <Navigate replace to="/channel-cluster/list" /> },
    ],
  },
]

export const router = createBrowserRouter(routes)
