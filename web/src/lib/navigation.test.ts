import { describe, expect, test } from "vitest"

import {
  getActiveNavigationItem,
  getActiveNavigationSection,
  legacyRouteRedirects,
  navigationSections,
  pageMetadata,
} from "@/lib/navigation"

describe("navigationSections", () => {
  test("defines the three section-specific top-level sections", () => {
    expect(navigationSections.map((section) => section.id)).toEqual([
      "cluster",
      "business",
      "system",
    ])
  })

  test("selects the active section from new nested routes", () => {
    expect(getActiveNavigationSection("/cluster/dashboard")?.id).toBe("cluster")
    expect(getActiveNavigationSection("/cluster/monitor")?.id).toBe("cluster")
    expect(getActiveNavigationSection("/cluster/nodes")?.id).toBe("cluster")
    expect(getActiveNavigationSection("/cluster/plugins")?.id).toBe("cluster")
    expect(getActiveNavigationSection("/cluster/workqueues")?.id).toBe("cluster")
    expect(getActiveNavigationSection("/business/dashboard")?.id).toBe("business")
    expect(getActiveNavigationSection("/business/monitor")?.id).toBe("business")
    expect(getActiveNavigationSection("/business/connections")?.id).toBe("business")
    expect(getActiveNavigationSection("/business/messages")?.id).toBe("business")
    expect(getActiveNavigationSection("/business/conversations")?.id).toBe("business")
  })

  test("exposes metadata and path label message ids for page headers", () => {
    expect(pageMetadata.get("/cluster/dashboard")?.titleMessageId).toBe("nav.clusterDashboard.title")
    expect(pageMetadata.get("/cluster/dashboard")?.pathLabelMessageId).toBe("nav.path.cluster.dashboard")
    expect(pageMetadata.get("/cluster/monitor")?.titleMessageId).toBe("nav.clusterMonitor.title")
    expect(pageMetadata.get("/cluster/monitor")?.pathLabelMessageId).toBe("nav.path.cluster.monitor")
    expect(pageMetadata.get("/business/dashboard")?.titleMessageId).toBe("nav.businessDashboard.title")
    expect(pageMetadata.get("/business/monitor")?.titleMessageId).toBe("nav.monitor.title")
    expect(pageMetadata.get("/business/connections")?.pathLabelMessageId).toBe("nav.path.business.connections")
    expect(pageMetadata.get("/cluster/nodes")?.titleMessageId).toBe("nav.nodes.title")
    expect(pageMetadata.get("/cluster/plugins")?.pathLabelMessageId).toBe("nav.path.cluster.plugins")
    expect(pageMetadata.get("/cluster/workqueues")?.titleMessageId).toBe("nav.workqueues.title")
    expect(pageMetadata.get("/cluster/workqueues")?.pathLabelMessageId).toBe("nav.path.cluster.workqueues")
    expect(getActiveNavigationItem("/cluster/nodes")?.pathLabelMessageId).toBe("nav.path.cluster.nodes")
  })

  test("maps legacy routes to new routes", () => {
    expect(legacyRouteRedirects["/dashboard"]).toBe("/cluster/dashboard")
    expect(legacyRouteRedirects["/monitor"]).toBe("/business/monitor")
    expect(legacyRouteRedirects["/channel-cluster/list"]).toBe("/cluster/channels")
    expect(legacyRouteRedirects["/workqueues"]).toBe("/cluster/workqueues")
    expect(legacyRouteRedirects["/app-logs"]).toBe("/cluster/diagnostics?tab=trace")
    expect(legacyRouteRedirects["/connections"]).toBe("/business/connections")
    expect(legacyRouteRedirects["/conversations"]).toBe("/business/conversations")
  })

  test("includes DB inspect in system navigation", () => {
    const item = pageMetadata.get("/system/db")
    expect(item?.titleMessageId).toBe("nav.dbInspect.title")
    expect(legacyRouteRedirects["/db-inspect"]).toBe("/system/db")
  })
})
