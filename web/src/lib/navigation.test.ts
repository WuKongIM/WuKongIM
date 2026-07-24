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

  test("omits dashboard entries from visible section menus", () => {
    const clusterSection = navigationSections.find((section) => section.id === "cluster")
    const businessSection = navigationSections.find((section) => section.id === "business")

    expect(clusterSection?.href).toBe("/cluster/monitor")
    expect(clusterSection?.items.map((item) => item.href)).not.toContain("/cluster/dashboard")
    expect(clusterSection?.items[0]?.href).toBe("/cluster/monitor")

    expect(businessSection?.href).toBe("/business/connections")
    expect(businessSection?.items.map((item) => item.href)).not.toContain("/business/dashboard")
    expect(businessSection?.items.map((item) => item.href)).not.toContain("/business/monitor")
    expect(businessSection?.items[0]?.href).toBe("/business/connections")
  })

  test("exposes metadata and path label message ids for page headers", () => {
    expect(pageMetadata.get("/cluster/dashboard")?.titleMessageId).toBe("nav.clusterDashboard.title")
    expect(pageMetadata.get("/cluster/dashboard")?.pathLabelMessageId).toBe("nav.path.cluster.dashboard")
    expect(pageMetadata.get("/cluster/monitor")?.titleMessageId).toBe("nav.clusterMonitor.title")
    expect(pageMetadata.get("/cluster/monitor")?.pathLabelMessageId).toBe("nav.path.cluster.monitor")
    expect(pageMetadata.get("/business/dashboard")?.titleMessageId).toBe("nav.businessDashboard.title")
    expect(pageMetadata.has("/business/monitor")).toBe(false)
    expect(pageMetadata.get("/business/connections")?.pathLabelMessageId).toBe("nav.path.business.connections")
    expect(pageMetadata.get("/cluster/nodes")?.titleMessageId).toBe("nav.nodes.title")
    expect(pageMetadata.get("/cluster/plugins")?.pathLabelMessageId).toBe("nav.path.cluster.plugins")
    expect(pageMetadata.get("/cluster/workqueues")?.titleMessageId).toBe("nav.workqueues.title")
    expect(pageMetadata.get("/cluster/workqueues")?.pathLabelMessageId).toBe("nav.path.cluster.workqueues")
    expect(pageMetadata.get("/cluster/system-logs")?.titleMessageId).toBe("nav.systemLogs.title")
    expect(pageMetadata.get("/cluster/system-logs")?.pathLabelMessageId).toBe("nav.path.cluster.systemLogs")
    expect(getActiveNavigationItem("/cluster/nodes")?.pathLabelMessageId).toBe("nav.path.cluster.nodes")
  })

  test("maps legacy routes to new routes", () => {
    expect(legacyRouteRedirects["/dashboard"]).toBe("/cluster/dashboard")
    expect(legacyRouteRedirects["/monitor"]).toBeUndefined()
    expect(legacyRouteRedirects["/business/monitor"]).toBeUndefined()
    expect(legacyRouteRedirects["/channel-cluster/list"]).toBe("/cluster/channels")
    expect(legacyRouteRedirects["/workqueues"]).toBe("/cluster/workqueues")
    expect(legacyRouteRedirects["/app-logs"]).toBe("/cluster/system-logs")
    expect(legacyRouteRedirects["/connections"]).toBe("/business/connections")
    expect(legacyRouteRedirects["/conversations"]).toBe("/business/conversations")
  })

  test("includes system logs in cluster operations navigation", () => {
    const clusterSection = navigationSections.find((section) => section.id === "cluster")
    const hrefs = clusterSection?.items.map((item) => item.href)

    expect(hrefs).toContain("/cluster/system-logs")
    expect(hrefs?.slice(-4)).toEqual([
      "/cluster/node-config",
      "/cluster/system-logs",
      "/cluster/diagnostics",
      "/cluster/backups",
    ])
    expect(getActiveNavigationItem("/app-logs")?.titleMessageId).toBe("nav.systemLogs.title")
  })

  test("includes DB inspect in system navigation", () => {
    const item = pageMetadata.get("/system/db")
    expect(item?.titleMessageId).toBe("nav.dbInspect.title")
    expect(legacyRouteRedirects["/db-inspect"]).toBe("/system/db")
  })
})
