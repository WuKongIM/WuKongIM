import { describe, expect, test } from "vitest"

import {
  getActiveNavigationItem,
  getActiveNavigationSection,
  legacyRouteRedirects,
  navigationSections,
  pageMetadata,
} from "@/lib/navigation"

describe("navigationSections", () => {
  test("defines the four redesigned top-level sections", () => {
    expect(navigationSections.map((section) => section.id)).toEqual([
      "overview",
      "cluster",
      "business",
      "system",
    ])
  })

  test("selects the active section from new nested routes", () => {
    expect(getActiveNavigationSection("/cluster/nodes")?.id).toBe("cluster")
    expect(getActiveNavigationSection("/business/messages")?.id).toBe("business")
    expect(getActiveNavigationSection("/business/conversations")?.id).toBe("business")
    expect(getActiveNavigationSection("/system/connections")?.id).toBe("system")
  })

  test("exposes metadata and path label message ids for page headers", () => {
    expect(pageMetadata.get("/cluster/nodes")?.titleMessageId).toBe("nav.nodes.title")
    expect(getActiveNavigationItem("/cluster/nodes")?.pathLabelMessageId).toBe("nav.path.cluster.nodes")
  })

  test("maps legacy routes to new routes", () => {
    expect(legacyRouteRedirects["/channel-cluster/list"]).toBe("/cluster/channels?tab=list")
    expect(legacyRouteRedirects["/connections"]).toBe("/system/connections")
    expect(legacyRouteRedirects["/conversations"]).toBe("/business/conversations")
  })
})
