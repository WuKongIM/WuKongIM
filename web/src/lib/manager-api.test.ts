import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import {
  configureManagerAuth,
  createPluginBinding,
  deletePluginBinding,
  getChannelClusterSummary,
  getChannelClusterUnhealthy,
  getChannelClusterReplicas,
  getChannelRuntimeMeta,
  getChannelRuntimeMetaDetail,
  getConnection,
  getConnections,
  getDBInspectTable,
  getDBInspectTables,
  getControllerLogs,
  getControllerRaftStatus,
  compactControllerRaftLogOnNode,
  compactControllerRaftLogs,
  compactSlotRaftLogOnNode,
  getDiagnosticsEvents,
  getDiagnosticsMessage,
  getDiagnosticsTrace,
  getMessages,
  getRecentConversations,
  getRuntimeWorkqueues,
  getApplicationLogEntries,
  getApplicationLogSources,
  streamApplicationLogEntries,
  getClusterRealtimeMonitor,
  getRealtimeMonitor,
  getNetworkSummary,
  createNodeOnboardingPlan,
  getNode,
  getNodeOnboardingCandidates,
  getNodeOnboardingJob,
  getNodeOnboardingJobs,
  getNodes,
  getNodePlugin,
  getNodePlugins,
  getNodeScaleInStatus,
  getOverview,
  getPermissions,
  getPluginBindings,
  getSlot,
  getSlotLogs,
  getSlots,
  getTask,
  getTasks,
  getDistributedTask,
  getDistributedTasks,
  getDistributedTasksSummary,
  getUsers,
  getUser,
  getSystemUsers,
  kickUser,
  loginManager,
  managerFetch,
  ManagerApiError,
  addSlot,
  markNodeDraining,
  removeSlot,
  rebalanceSlots,
  recoverSlot,
  resetManagerAuthConfig,
  resumeNode,
  addSystemUsers,
  removeSystemUsers,
  restartNodePlugin,
  retryNodeOnboardingJob,
  planNodeScaleIn,
  startNodeScaleIn,
  advanceNodeScaleIn,
  cancelNodeScaleIn,
  startNodeOnboardingJob,
  transferSlotLeader,
  advanceMessageRetention,
  addBusinessChannelMembers,
  createDiagnosticsTrackingRule,
  deleteDiagnosticsTrackingRule,
  repairChannelClusterLeader,
  getBusinessChannel,
  getBusinessChannelMembers,
  getBusinessChannels,
  listDiagnosticsTrackingRules,
  resetUserToken,
  removeBusinessChannelMembers,
  transferChannelClusterLeader,
  queryDBInspect,
  updateNodePluginConfig,
  upsertBusinessChannel,
} from "@/lib/manager-api"
import type { ClusterRealtimeMonitorResponse } from "@/lib/manager-api.types"

describe("manager api client", () => {
  const fetchMock = vi.fn()

  function emptyDiagnosticsResponse() {
    return {
      scope: "cluster",
      status: "not_found",
      generated_at: "2026-05-06T00:00:00Z",
      query: {},
      summary: {
        involved_nodes: [],
        peer_nodes: [],
        event_count: 0,
      },
      nodes: [],
      events: [],
      notes: [],
    }
  }

  beforeEach(() => {
    fetchMock.mockReset()
    vi.stubGlobal("fetch", fetchMock)
    resetManagerAuthConfig()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    vi.unstubAllEnvs()
  })

  it("prefixes requests with VITE_API_BASE_URL and trims trailing slashes", async () => {
    vi.stubEnv("VITE_API_BASE_URL", "http://127.0.0.1:5301/")
    fetchMock.mockResolvedValue(new Response("{}", { status: 200 }))

    await managerFetch("/manager/nodes")

    expect(fetchMock).toHaveBeenCalledWith(
      "http://127.0.0.1:5301/manager/nodes",
      expect.objectContaining({
        headers: expect.any(Headers),
      }),
    )
  })

  it("adds the bearer token when auth is configured", async () => {
    configureManagerAuth({
      getAccessToken: () => "token-1",
      onUnauthorized: vi.fn(),
    })
    fetchMock.mockResolvedValue(new Response("{}", { status: 200 }))

    await managerFetch("/manager/nodes")

    const requestInit = fetchMock.mock.calls[0]?.[1] as { headers: Headers }
    expect(requestInit.headers.get("Authorization")).toBe("Bearer token-1")
  })

  it("preserves a caller-provided Accept header for non-JSON responses", async () => {
    fetchMock.mockResolvedValue(new Response("", { status: 200 }))

    await managerFetch("/manager/app-logs/stream?node_id=1", {
      headers: { Accept: "application/x-ndjson" },
    })

    const requestInit = fetchMock.mock.calls[0]?.[1] as { headers: Headers }
    expect(requestInit.headers.get("Accept")).toBe("application/x-ndjson")
  })

  it("calls onUnauthorized on 401 responses", async () => {
    const onUnauthorized = vi.fn()
    configureManagerAuth({ getAccessToken: () => "token-1", onUnauthorized })
    fetchMock.mockResolvedValue(
      new Response('{"error":"unauthorized","message":"unauthorized"}', { status: 401 }),
    )

    await expect(managerFetch("/manager/nodes")).rejects.toMatchObject({
      status: 401,
      error: "unauthorized",
    })
    expect(onUnauthorized).toHaveBeenCalledTimes(1)
  })

  it("maps the login response using current backend fields", async () => {
    fetchMock.mockResolvedValue(
      new Response(
        JSON.stringify({
          username: "admin",
          token_type: "Bearer",
          access_token: "token-1",
          expires_in: 3600,
          expires_at: "2099-04-22T12:00:00Z",
          permissions: [{ resource: "cluster.node", actions: ["r"] }],
        }),
        { status: 200 },
      ),
    )

    await expect(
      loginManager({ username: "admin", password: "secret" }),
    ).resolves.toEqual({
      username: "admin",
      tokenType: "Bearer",
      accessToken: "token-1",
      expiresAt: "2099-04-22T12:00:00Z",
      permissions: [{ resource: "cluster.node", actions: ["r"] }],
    })
  })

  it("fetches overview data from the manager overview endpoint", async () => {
    const overview = {
      generated_at: "2026-04-23T08:00:00Z",
      cluster: { controller_leader_id: 1 },
      nodes: { total: 3, alive: 3, suspect: 0, dead: 0, draining: 0 },
      slots: {
        total: 64,
        ready: 63,
        quorum_lost: 1,
        leader_missing: 0,
        unreported: 0,
        peer_mismatch: 0,
        epoch_lag: 0,
      },
      tasks: { total: 2, pending: 1, retrying: 1, failed: 0 },
      anomalies: {
        slots: {
          quorum_lost: { count: 0, items: [] },
          leader_missing: { count: 0, items: [] },
          sync_mismatch: { count: 0, items: [] },
        },
        tasks: {
          failed: { count: 0, items: [] },
          retrying: { count: 0, items: [] },
        },
      },
    }
    fetchMock.mockResolvedValue(new Response(JSON.stringify(overview), { status: 200 }))

    await expect(getOverview()).resolves.toEqual(overview)
    expect(fetchMock).toHaveBeenCalledWith(
      "/manager/overview",
      expect.objectContaining({ headers: expect.any(Headers) }),
    )
  })

  it("fetches manager permissions", async () => {
    const payload = {
      auth_enabled: true,
      current_user: "admin",
      users: [{ username: "admin", permissions: [{ resource: "*", actions: ["*"] }] }],
      resources: [{ resource: "cluster.permission", actions: ["r"], description: "Read manager permissions." }],
    }
    fetchMock.mockResolvedValue(new Response(JSON.stringify(payload), { status: 200 }))

    await expect(getPermissions()).resolves.toEqual(payload)

    expect(fetchMock).toHaveBeenCalledWith(
      "/manager/permissions",
      expect.objectContaining({ headers: expect.any(Headers) }),
    )
  })

  it("builds DB inspect table and query requests", async () => {
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({ rows: [], stats: {} }), { status: 200 }))
    await getDBInspectTables({ nodeId: 2 })
    expect(fetchMock).toHaveBeenLastCalledWith(
      "/manager/db/inspect/tables?node_id=2",
      expect.objectContaining({ headers: expect.any(Headers) }),
    )

    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({ rows: [], stats: {} }), { status: 200 }))
    await getDBInspectTable("meta", "user", { nodeId: 2 })
    expect(fetchMock).toHaveBeenLastCalledWith(
      "/manager/db/inspect/tables/meta/user?node_id=2",
      expect.objectContaining({ headers: expect.any(Headers) }),
    )

    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({ rows: [], stats: {} }), { status: 200 }))
    await queryDBInspect({ node_id: 2, query: "show tables" })
    expect(fetchMock).toHaveBeenLastCalledWith(
      "/manager/db/inspect/query",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify({ node_id: 2, query: "show tables" }),
      }),
    )
  })

  it("fetches manager users with search params", async () => {
    fetchMock.mockResolvedValue(new Response(JSON.stringify({ items: [], has_more: false }), { status: 200 }))

    await expect(getUsers({ keyword: "u1", limit: 25, cursor: "abc" })).resolves.toEqual({ items: [], has_more: false })

    expect(fetchMock).toHaveBeenCalledWith(
      "/manager/users?keyword=u1&limit=25&cursor=abc",
      expect.objectContaining({ headers: expect.any(Headers) }),
    )
  })

  it("fetches manager user detail with encoded uid", async () => {
    const detail = { uid: "u/1", slot_id: 1, hash_slot: 7, online: false, devices: [], connections: [] }
    fetchMock.mockResolvedValue(new Response(JSON.stringify(detail), { status: 200 }))

    await expect(getUser("u/1")).resolves.toEqual(detail)

    expect(fetchMock.mock.calls[0]?.[0]).toBe("/manager/users/u%2F1")
  })

  it("kicks a manager user", async () => {
    fetchMock.mockResolvedValue(new Response(JSON.stringify({ uid: "u1", device_flag: "all", changed: true }), { status: 200 }))

    await kickUser("u1", { deviceFlag: "all" })

    expect(fetchMock.mock.calls[0]?.[0]).toBe("/manager/users/u1/kick")
    expect(JSON.parse((fetchMock.mock.calls[0]?.[1] as RequestInit).body as string)).toEqual({
      device_flag: "all",
    })
  })

  it("resets a manager user token", async () => {
    fetchMock.mockResolvedValue(new Response(JSON.stringify({ uid: "u1", device_flag: "app", device_level: "master", token: "next" }), { status: 200 }))

    await resetUserToken("u1", { deviceFlag: "app", deviceLevel: "master" })

    expect(fetchMock.mock.calls[0]?.[0]).toBe("/manager/users/u1/token/reset")
    expect(JSON.parse((fetchMock.mock.calls[0]?.[1] as RequestInit).body as string)).toEqual({
      device_flag: "app",
      device_level: "master",
    })
  })

  it("fetches manager system users", async () => {
    const payload = { items: [{ uid: "sys-a" }], total: 1 }
    fetchMock.mockResolvedValue(new Response(JSON.stringify(payload), { status: 200 }))

    await expect(getSystemUsers()).resolves.toEqual(payload)

    expect(fetchMock).toHaveBeenCalledWith(
      "/manager/system-users",
      expect.objectContaining({ headers: expect.any(Headers) }),
    )
  })

  it("fetches and mutates node plugins", async () => {
    const list = { node_id: 2, total: 1, items: [{ node_id: 2, plugin_no: "wk.echo", name: "Echo" }] }
    const detail = { node_id: 2, plugin_no: "wk.echo", name: "Echo", status: "running", enabled: true }
    const mutation = { node_id: 2, plugin_no: "wk.echo", changed: true, plugin: detail }
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(list), { status: 200 }))
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(detail), { status: 200 }))
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(mutation), { status: 200 }))
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(mutation), { status: 200 }))

    await expect(getNodePlugins(2)).resolves.toEqual(list)
    await expect(getNodePlugin(2, "wk.echo")).resolves.toEqual(detail)
    await expect(updateNodePluginConfig(2, "wk.echo", { api_key: "******" })).resolves.toEqual(mutation)
    await expect(restartNodePlugin(2, "wk.echo")).resolves.toEqual(mutation)

    expect(fetchMock).toHaveBeenNthCalledWith(
      1,
      "/manager/nodes/2/plugins",
      expect.objectContaining({ headers: expect.any(Headers) }),
    )
    expect(fetchMock).toHaveBeenNthCalledWith(
      2,
      "/manager/nodes/2/plugins/wk.echo",
      expect.objectContaining({ headers: expect.any(Headers) }),
    )
    expect(fetchMock).toHaveBeenNthCalledWith(
      3,
      "/manager/nodes/2/plugins/wk.echo/config",
      expect.objectContaining({
        method: "PUT",
        body: JSON.stringify({ api_key: "******" }),
        headers: expect.any(Headers),
      }),
    )
    expect(fetchMock).toHaveBeenNthCalledWith(
      4,
      "/manager/nodes/2/plugins/wk.echo/restart",
      expect.objectContaining({ method: "POST", headers: expect.any(Headers) }),
    )
  })

  it("fetches and mutates plugin bindings", async () => {
    const byUID = { items: [{ uid: "u1", plugin_no: "wk.echo", warnings: [] }], total: 1, has_more: false }
    const byPlugin = { items: [], total: 0, next_cursor: "next", has_more: true }
    const mutation = { binding: { uid: "u1", plugin_no: "wk.echo", warnings: [] }, changed: true }
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(byUID), { status: 200 }))
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(byPlugin), { status: 200 }))
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(mutation), { status: 200 }))
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(mutation), { status: 200 }))

    await expect(getPluginBindings({ uid: "u1" })).resolves.toEqual(byUID)
    await expect(getPluginBindings({ pluginNo: "wk.echo", limit: 25, cursor: "abc" })).resolves.toEqual(byPlugin)
    await expect(createPluginBinding({ uid: "u1", pluginNo: "wk.echo" })).resolves.toEqual(mutation)
    await expect(deletePluginBinding({ uid: "u1", pluginNo: "wk.echo" })).resolves.toEqual(mutation)

    expect(fetchMock).toHaveBeenNthCalledWith(
      1,
      "/manager/plugin-bindings?uid=u1",
      expect.objectContaining({ headers: expect.any(Headers) }),
    )
    expect(fetchMock).toHaveBeenNthCalledWith(
      2,
      "/manager/plugin-bindings?plugin_no=wk.echo&limit=25&cursor=abc",
      expect.objectContaining({ headers: expect.any(Headers) }),
    )
    expect(fetchMock).toHaveBeenNthCalledWith(
      3,
      "/manager/plugin-bindings",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify({ uid: "u1", plugin_no: "wk.echo" }),
        headers: expect.any(Headers),
      }),
    )
    expect(fetchMock).toHaveBeenNthCalledWith(
      4,
      "/manager/plugin-bindings",
      expect.objectContaining({
        method: "DELETE",
        body: JSON.stringify({ uid: "u1", plugin_no: "wk.echo" }),
        headers: expect.any(Headers),
      }),
    )
  })

  it("adds manager system users", async () => {
    const payload = { uids: ["sys-a", "sys-b"], changed: true }
    fetchMock.mockResolvedValue(new Response(JSON.stringify(payload), { status: 200 }))

    await expect(addSystemUsers({ uids: ["sys-a", "sys-b"] })).resolves.toEqual(payload)

    expect(fetchMock).toHaveBeenCalledWith(
      "/manager/system-users/add",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify({ uids: ["sys-a", "sys-b"] }),
        headers: expect.any(Headers),
      }),
    )
  })

  it("removes manager system users", async () => {
    const payload = { uids: ["sys-a"], changed: true }
    fetchMock.mockResolvedValue(new Response(JSON.stringify(payload), { status: 200 }))

    await expect(removeSystemUsers({ uids: ["sys-a"] })).resolves.toEqual(payload)

    expect(fetchMock).toHaveBeenCalledWith(
      "/manager/system-users/remove",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify({ uids: ["sys-a"] }),
        headers: expect.any(Headers),
      }),
    )
  })

  it("fetches business channels with search params", async () => {
    fetchMock.mockResolvedValue(new Response(JSON.stringify({ items: [], has_more: false }), { status: 200 }))

    await getBusinessChannels({ nodeId: 2, type: 2, keyword: "g1", limit: 25, cursor: "abc" })

    expect(fetchMock).toHaveBeenCalledWith(
      "/manager/channels?node_id=2&type=2&keyword=g1&limit=25&cursor=abc",
      expect.objectContaining({ headers: expect.any(Headers) }),
    )
  })

  it("fetches business channel detail with encoded channel id", async () => {
    fetchMock.mockResolvedValue(new Response(JSON.stringify({ channel_id: "g/1", channel_type: 2 }), { status: 200 }))

    await getBusinessChannel(2, "g/1")

    expect(fetchMock.mock.calls[0]?.[0]).toBe("/manager/channels/2/g%2F1")
  })

  it("upserts a business channel with backend field names", async () => {
    fetchMock.mockResolvedValue(new Response(JSON.stringify({ channel_id: "g1", channel_type: 2 }), { status: 200 }))

    await upsertBusinessChannel({ channelId: "g1", channelType: 2, ban: true, disband: false, sendBan: true })

    expect(fetchMock.mock.calls[0]?.[0]).toBe("/manager/channels")
    expect(JSON.parse((fetchMock.mock.calls[0]?.[1] as RequestInit).body as string)).toEqual({
      channel_id: "g1",
      channel_type: 2,
      ban: true,
      disband: false,
      send_ban: true,
    })
  })

  it("fetches business channel members by list kind", async () => {
    fetchMock.mockResolvedValue(new Response(JSON.stringify({ items: [], has_more: false }), { status: 200 }))

    await getBusinessChannelMembers(2, "g/1", "allowlist", { limit: 100, cursor: "next" })

    expect(fetchMock.mock.calls[0]?.[0]).toBe("/manager/channels/2/g%2F1/allowlist?limit=100&cursor=next")
  })

  it("adds and removes business channel members with uid bodies", async () => {
    fetchMock
      .mockResolvedValueOnce(new Response(JSON.stringify({ changed: true }), { status: 200 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ changed: true }), { status: 200 }))

    await addBusinessChannelMembers(2, "g1", "subscribers", { uids: ["u1", "u2"] })
    await removeBusinessChannelMembers(2, "g1", "denylist", { uids: ["u3"] })

    expect(fetchMock.mock.calls[0]?.[0]).toBe("/manager/channels/2/g1/subscribers/add")
    expect(JSON.parse((fetchMock.mock.calls[0]?.[1] as RequestInit).body as string)).toEqual({ uids: ["u1", "u2"] })
    expect(fetchMock.mock.calls[1]?.[0]).toBe("/manager/channels/2/g1/denylist/remove")
    expect(JSON.parse((fetchMock.mock.calls[1]?.[1] as RequestInit).body as string)).toEqual({ uids: ["u3"] })
  })

  it("fetches network summary from the manager network endpoint", async () => {
    const summary = {
      generated_at: "2026-04-29T12:00:00Z",
      scope: { view: "local_node", local_node_id: 1, controller_leader_id: 1 },
      source_status: { local_collector: "ok", controller_context: "ok", runtime_views: "ok", errors: {} },
      headline: {
        remote_peers: 1,
        alive_nodes: 2,
        suspect_nodes: 0,
        dead_nodes: 0,
        draining_nodes: 0,
        pool_active: 3,
        pool_idle: 4,
        rpc_inflight: 1,
        dial_errors_1m: 0,
        queue_full_1m: 0,
        timeouts_1m: 1,
        stale_observations: 0,
      },
      traffic: {
        scope: "local_total_by_msg_type",
        tx_bytes_1m: 1024,
        rx_bytes_1m: 512,
        tx_bps: 17.06,
        rx_bps: 8.53,
        peer_breakdown_available: false,
        by_message_type: [{
          direction: "tx",
          message_type: "rpc",
          bytes_1m: 1024,
          bps: 17.06,
        }],
      },
      peers: [{
        node_id: 2,
        name: "node-2",
        addr: "127.0.0.1:7002",
        health: "alive",
        last_heartbeat_at: "2026-04-29T11:59:55Z",
        pools: {
          cluster: { active: 1, idle: 1 },
          data_plane: { active: 1, idle: 2 },
        },
        rpc: { inflight: 1, calls_1m: 10, p95_ms: 12.5, success_rate: 0.9 },
        errors: { dial_error_1m: 0, queue_full_1m: 0, timeout_1m: 1, remote_error_1m: 0 },
      }],
      services: [{
        service_id: 35,
        service: "channel_long_poll_fetch",
        group: "channel_replication",
        target_node: 2,
        inflight: 1,
        calls_1m: 10,
        success_1m: 9,
        expected_timeout_1m: 1,
        timeout_1m: 0,
        queue_full_1m: 0,
        remote_error_1m: 0,
        other_error_1m: 0,
        p50_ms: 2.1,
        p95_ms: 12.5,
        p99_ms: 20,
        last_seen_at: "2026-04-29T11:59:58Z",
      }],
      channel_replication: {
        pool: { active: 1, idle: 2 },
        services: [{
          service_id: 35,
          service: "channel_long_poll_fetch",
          group: "channel_replication",
          target_node: 2,
          inflight: 1,
          calls_1m: 10,
          success_1m: 9,
          expected_timeout_1m: 1,
          timeout_1m: 0,
          queue_full_1m: 0,
          remote_error_1m: 0,
          other_error_1m: 0,
          p50_ms: 2.1,
          p95_ms: 12.5,
          p99_ms: 20,
          last_seen_at: "2026-04-29T11:59:58Z",
        }],
        long_poll: { lane_count: 8, max_wait_ms: 200, max_bytes: 65536, max_channels: 64 },
        long_poll_timeouts_1m: 1,
        data_plane_rpc_timeout_ms: 1000,
      },
      discovery: {
        listen_addr: "0.0.0.0:7000",
        advertise_addr: "127.0.0.1:7000",
        seeds: [],
        static_nodes: [{ node_id: 1, addr: "127.0.0.1:7000" }],
        pool_size: 4,
        data_plane_pool_size: 4,
        dial_timeout_ms: 5000,
        controller_observation_interval_ms: 200,
      },
      history: {
        window_seconds: 60,
        step_seconds: 5,
        traffic: [{
          at: "2026-04-29T11:59:00Z",
          tx_bytes: 1024,
          rx_bytes: 512,
        }],
        rpc: [{
          at: "2026-04-29T11:59:00Z",
          calls: 10,
          success: 9,
          errors: 0,
          expected_timeouts: 1,
        }],
        errors: [{
          at: "2026-04-29T11:59:00Z",
          dial_errors: 0,
          queue_full: 0,
          timeouts: 1,
          remote_errors: 0,
        }],
      },
      events: [{
        at: "2026-04-29T11:59:59Z",
        severity: "warn",
        kind: "rpc_timeout",
        target_node: 2,
        service: "cluster_ping",
        message: "rpc timeout",
      }],
    }
    fetchMock.mockResolvedValue(new Response(JSON.stringify(summary), { status: 200 }))

    await expect(getNetworkSummary()).resolves.toEqual(summary)
    expect(fetchMock).toHaveBeenCalledWith(
      "/manager/network/summary",
      expect.objectContaining({ headers: expect.any(Headers) }),
    )
  })

  it("fetches realtime monitor data with window and step params", async () => {
    const response = {
      status: "prometheus_disabled",
      generated_at: "2026-05-15T08:30:00Z",
      window_seconds: 900,
      step_seconds: 20,
      scope: { view: "prometheus" },
      sources: {
        prometheus: {
          enabled: false,
          base_url: "",
          query_ms: 0,
          error: "prometheus is disabled",
        },
      },
      snapshot: [],
      cards: [],
    }
    fetchMock.mockResolvedValue(new Response(JSON.stringify(response), { status: 200 }))

    await expect(getRealtimeMonitor({ window: "15m", step: "20s" })).resolves.toEqual(response)

    expect(fetchMock).toHaveBeenCalledWith(
      "/manager/monitor/realtime?window=15m&step=20s",
      expect.objectContaining({ headers: expect.any(Headers) }),
    )
  })

  it("fetches cluster realtime monitor data with window and step params", async () => {
    const response: ClusterRealtimeMonitorResponse = {
      status: "ready",
      generated_at: "2026-06-18T10:00:00Z",
      window_seconds: 900,
      step_seconds: 20,
      scope: { view: "cluster" },
      sources: {
        prometheus: { enabled: true, base_url: "http://127.0.0.1:9090", query_ms: 12, error: "" },
        control_snapshot: { enabled: true, query_ms: 2, error: "" },
      },
      snapshot: [],
      cards: [],
    }
    fetchMock.mockResolvedValue(new Response(JSON.stringify(response), { status: 200 }))

    const payload = await getClusterRealtimeMonitor({ window: "15m", step: "20s" })
    expect(payload).toEqual(response)
    expect(payload.scope).toEqual({ view: "cluster" })

    expect(fetchMock).toHaveBeenCalledWith(
      "/manager/cluster-monitor/realtime?window=15m&step=20s",
      expect.objectContaining({ headers: expect.any(Headers) }),
    )
  })

  it("fetches node list and detail data from manager endpoints", async () => {
    const nodesResponse = {
      generated_at: "2026-04-23T08:00:01Z",
      controller_leader_id: 1,
      total: 1,
      items: [{
        node_id: 1,
        name: "node-1",
        addr: "127.0.0.1:7000",
        status: "alive",
        last_heartbeat_at: "2026-04-23T08:00:00Z",
        is_local: true,
        capacity_weight: 1,
        membership: { role: "data", join_state: "active", schedulable: true },
        health: { status: "alive", last_heartbeat_at: "2026-04-23T08:00:00Z" },
        controller: { role: "leader", voter: true, leader_id: 1 },
        slot_stats: { count: 3, leader_count: 2 },
        slots: {
          replica_count: 3,
          leader_count: 2,
          follower_count: 1,
          quorum_lost_count: 0,
          unreported_count: 0,
        },
        runtime: {
          node_id: 1,
          active_online: 4,
          closing_online: 0,
          total_online: 4,
          gateway_sessions: 5,
          sessions_by_listener: {},
          accepting_new_sessions: true,
          draining: false,
          unknown: false,
        },
        actions: {
          can_drain: true,
          can_resume: false,
          can_scale_in: true,
          can_onboard: false,
        },
      }],
    }
    const nodeDetail = {
      ...nodesResponse.items[0],
      slots: {
        hosted_ids: [1, 2, 3],
        leader_ids: [1, 2],
        replica_count: 3,
        leader_count: 2,
        follower_count: 1,
        quorum_lost_count: 0,
        unreported_count: 0,
      },
    }

    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(nodesResponse), { status: 200 }))
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(nodeDetail), { status: 200 }))

    await expect(getNodes()).resolves.toEqual(nodesResponse)
    await expect(getNode(1)).resolves.toEqual(nodeDetail)
    expect(fetchMock).toHaveBeenNthCalledWith(
      1,
      "/manager/nodes",
      expect.objectContaining({ headers: expect.any(Headers) }),
    )
    expect(fetchMock).toHaveBeenNthCalledWith(
      2,
      "/manager/nodes/1",
      expect.objectContaining({ headers: expect.any(Headers) }),
    )
  })

  it("posts node draining and resume actions", async () => {
    const actionResult = {
      node_id: 1,
      name: "node-1",
      addr: "127.0.0.1:7000",
      status: "draining",
      last_heartbeat_at: "2026-04-23T08:00:00Z",
      is_local: false,
      capacity_weight: 1,
      membership: { role: "data", join_state: "active", schedulable: false },
      health: { status: "draining", last_heartbeat_at: "2026-04-23T08:00:00Z" },
      controller: { role: "follower", voter: true, leader_id: 1 },
      slot_stats: { count: 3, leader_count: 0 },
      slots: {
        hosted_ids: [1, 2],
        leader_ids: [],
        replica_count: 3,
        leader_count: 0,
        follower_count: 3,
        quorum_lost_count: 0,
        unreported_count: 0,
      },
      runtime: {
        node_id: 1,
        active_online: 0,
        closing_online: 0,
        total_online: 0,
        gateway_sessions: 0,
        sessions_by_listener: {},
        accepting_new_sessions: false,
        draining: true,
        unknown: false,
      },
      actions: {
        can_drain: false,
        can_resume: true,
        can_scale_in: false,
        can_onboard: false,
      },
    }
    fetchMock.mockResolvedValue(new Response(JSON.stringify(actionResult), { status: 200 }))

    await expect(markNodeDraining(1)).resolves.toEqual(actionResult)

    let requestInit = fetchMock.mock.calls[0]?.[1] as { method: string; headers: Headers }
    expect(fetchMock.mock.calls[0]?.[0]).toBe("/manager/nodes/1/draining")
    expect(requestInit.method).toBe("POST")
    expect(requestInit.headers.get("Content-Type")).toBeNull()

    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(actionResult), { status: 200 }))
    await expect(resumeNode(1)).resolves.toEqual(actionResult)

    requestInit = fetchMock.mock.calls[1]?.[1] as { method: string }
    expect(fetchMock.mock.calls[1]?.[0]).toBe("/manager/nodes/1/resume")
    expect(requestInit.method).toBe("POST")
  })

  it("fetches slot list and detail data from manager endpoints", async () => {
    const slotsResponse = {
      total: 1,
      items: [{
        slot_id: 9,
        state: { quorum: "ready", sync: "in_sync" },
        assignment: { desired_peers: [1, 2, 3], config_epoch: 7, balance_version: 4 },
        runtime: {
          current_peers: [1, 2, 3],
          leader_id: 2,
          healthy_voters: 3,
          has_quorum: true,
          observed_config_epoch: 7,
          last_report_at: "2026-04-23T08:00:00Z",
        },
      }],
    }
    const slotDetail = {
      ...slotsResponse.items[0],
      task: {
        kind: "rebalance",
        step: "plan",
        status: "retrying",
        source_node: 1,
        target_node: 2,
        attempt: 2,
        next_run_at: null,
        last_error: "",
      },
    }

    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(slotsResponse), { status: 200 }))
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(slotDetail), { status: 200 }))

    await expect(getSlots()).resolves.toEqual(slotsResponse)
    await expect(getSlot(9)).resolves.toEqual(slotDetail)
    expect(fetchMock).toHaveBeenNthCalledWith(1, "/manager/slots", expect.anything())
    expect(fetchMock).toHaveBeenNthCalledWith(2, "/manager/slots/9", expect.anything())
  })

  it("fetches node-scoped slot list data from the manager endpoint", async () => {
    const slotsResponse = {
      total: 1,
      items: [{
        slot_id: 9,
        state: { quorum: "ready", sync: "in_sync" },
        assignment: { desired_peers: [1, 2, 3], config_epoch: 7, balance_version: 4 },
        runtime: {
          current_peers: [1, 2, 3],
          leader_id: 2,
          healthy_voters: 3,
          has_quorum: true,
          observed_config_epoch: 7,
          last_report_at: "2026-04-23T08:00:00Z",
        },
        node_log: { node_id: 2, leader_id: 2, commit_index: 93, applied_index: 91 },
      }],
    }
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(slotsResponse), { status: 200 }))

    await expect(getSlots({ nodeId: 2 })).resolves.toEqual(slotsResponse)
    expect(fetchMock).toHaveBeenNthCalledWith(1, "/manager/slots?node_id=2", expect.anything())
  })

  it("fetches node-scoped controller log entries from the manager endpoint", async () => {
    const logsResponse = {
      node_id: 2,
      first_index: 1,
      last_index: 4,
      commit_index: 4,
      applied_index: 3,
      next_cursor: 3,
      items: [
        {
          index: 4,
          term: 2,
          type: "normal",
          data_size: 12,
          decode_status: "ok",
          decoded_type: "add_slot",
          decoded: { command: "add_slot", new_slot_id: 9 },
        },
      ],
    }
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(logsResponse), { status: 200 }))

    await expect(getControllerLogs({ nodeId: 2, limit: 2, cursor: 5 })).resolves.toEqual(logsResponse)
    expect(fetchMock).toHaveBeenNthCalledWith(
      1,
      "/manager/controller/logs?node_id=2&limit=2&cursor=5",
      expect.anything(),
    )
  })

  it("fetches node-scoped controller raft status from the manager endpoint", async () => {
    const statusResponse = {
      node_id: 2,
      role: "leader",
      leader_id: 2,
      term: 7,
      health: "snapshot_transferring",
      first_index: 10,
      last_index: 42,
      commit_index: 40,
      applied_index: 39,
      snapshot_index: 9,
      snapshot_term: 3,
      compaction: {
        enabled: true,
        trigger_entries: 100,
        check_interval_ms: 2000,
        last_snapshot_index: 9,
        last_snapshot_at: "2026-05-06T08:01:00Z",
        last_check_at: "2026-05-06T08:02:00Z",
        last_error: "",
        last_error_at: "0001-01-01T00:00:00Z",
        degraded: false,
      },
      restore: {
        last_snapshot_index: 8,
        last_snapshot_term: 2,
        last_restored_at: "2026-05-06T08:04:00Z",
        last_error: "",
        last_error_at: "0001-01-01T00:00:00Z",
        failed: false,
      },
      peers: [{
        node_id: 3,
        match: 21,
        next: 22,
        state: "snapshot",
        pending_snapshot: 9,
        recent_active: true,
        needs_snapshot: true,
        snapshot_transferring: true,
      }],
    }
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(statusResponse), { status: 200 }))

    await expect(getControllerRaftStatus(2)).resolves.toEqual(statusResponse)
    expect(fetchMock).toHaveBeenNthCalledWith(
      1,
      "/manager/nodes/2/controller-raft",
      expect.anything(),
    )
  })

  it("posts control-plane-wide controller raft compaction", async () => {
    const compactResponse = {
      generated_at: "2026-05-07T10:00:00Z",
      total: 2,
      succeeded: 1,
      failed: 1,
      items: [
        {
          node_id: 1,
          success: true,
          applied_index: 42,
          before_snapshot_index: 30,
          after_snapshot_index: 42,
          compacted: true,
          skipped_reason: "",
          error: "",
        },
        {
          node_id: 2,
          success: false,
          applied_index: 0,
          before_snapshot_index: 0,
          after_snapshot_index: 0,
          compacted: false,
          skipped_reason: "",
          error: "node stopped",
        },
      ],
    }
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(compactResponse), { status: 200 }))

    await expect(compactControllerRaftLogs()).resolves.toEqual(compactResponse)
    expect(fetchMock).toHaveBeenNthCalledWith(
      1,
      "/manager/controller-raft/compact",
      expect.objectContaining({ method: "POST" }),
    )
  })

  it("posts node-scoped controller raft compaction", async () => {
    const compactResponse = {
      generated_at: "2026-05-07T10:03:00Z",
      total: 1,
      succeeded: 1,
      failed: 0,
      items: [{
        node_id: 2,
        success: true,
        applied_index: 50,
        before_snapshot_index: 40,
        after_snapshot_index: 50,
        compacted: true,
        skipped_reason: "",
        error: "",
      }],
    }
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(compactResponse), { status: 200 }))

    await expect(compactControllerRaftLogOnNode(2)).resolves.toEqual(compactResponse)
    expect(fetchMock).toHaveBeenNthCalledWith(
      1,
      "/manager/nodes/2/controller-raft/compact",
      expect.objectContaining({ method: "POST" }),
    )
  })

  it("fetches node-scoped slot log entries from the manager endpoint", async () => {
    const logsResponse = {
      node_id: 2,
      slot_id: 9,
      first_index: 1,
      last_index: 4,
      commit_index: 4,
      applied_index: 3,
      next_cursor: 3,
      items: [
        {
          index: 4,
          term: 2,
          type: "normal",
          data_size: 12,
          decode_status: "ok",
          decoded_type: "upsert_user",
          decoded: { command: "upsert_user", uid: "u1", token: "***" },
        },
        { index: 3, term: 2, type: "conf_change", data_size: 8 },
      ],
    }
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(logsResponse), { status: 200 }))

    await expect(getSlotLogs(9, { nodeId: 2, limit: 2, cursor: 5 })).resolves.toEqual(logsResponse)
    expect(fetchMock).toHaveBeenNthCalledWith(
      1,
      "/manager/slots/9/logs?node_id=2&limit=2&cursor=5",
      expect.anything(),
    )
  })

  it("posts node-scoped slot raft compaction", async () => {
    const compactResponse = {
      generated_at: "2026-05-08T10:03:00Z",
      total: 1,
      succeeded: 1,
      failed: 0,
      items: [{
        node_id: 2,
        slot_id: 9,
        success: true,
        applied_index: 50,
        before_snapshot_index: 40,
        after_snapshot_index: 50,
        compacted: true,
        skipped_reason: "",
        error: "",
      }],
    }
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(compactResponse), { status: 200 }))

    await expect(compactSlotRaftLogOnNode(2, 9)).resolves.toEqual(compactResponse)
    expect(fetchMock).toHaveBeenNthCalledWith(
      1,
      "/manager/nodes/2/slots/9/compact",
      expect.objectContaining({ method: "POST" }),
    )
  })

  it("fetches recent conversations with query params", async () => {
    fetchMock.mockResolvedValue(new Response(JSON.stringify({ uid: "u1", items: [] }), { status: 200 }))

    await getRecentConversations({ uid: "u1", limit: 25, msgCount: 2, onlyUnread: true })

    expect(fetchMock).toHaveBeenCalledWith(
      "/manager/conversations?uid=u1&limit=25&msg_count=2&only_unread=true",
      expect.objectContaining({ headers: expect.any(Headers) }),
    )
  })

  it("fetches message list data from the manager endpoint", async () => {
    const messagesResponse = {
      items: [{
        message_id: 101,
        message_seq: 9,
        client_msg_no: "c-101",
        channel_id: "room-1",
        channel_type: 2,
        from_uid: "u1",
        timestamp: 1713859200,
        payload: "aGVsbG8=",
      }],
      has_more: true,
      next_cursor: "cursor-2",
    }
    fetchMock.mockResolvedValue(new Response(JSON.stringify(messagesResponse), { status: 200 }))

    await expect(getMessages({ channelId: "room-1", channelType: 2, clientMsgNo: "dup-1", limit: 20, cursor: "cursor-1" })).resolves.toEqual(messagesResponse)
    expect(fetchMock).toHaveBeenCalledWith(
      "/manager/messages?channel_id=room-1&channel_type=2&limit=20&cursor=cursor-1&client_msg_no=dup-1",
      expect.objectContaining({ headers: expect.any(Headers) }),
    )
  })


  it("advances message retention through the manager endpoint", async () => {
    const response = {
      channel_id: "room-1",
      channel_type: 2,
      requested_through_seq: 10,
      advanced_through_seq: 8,
      min_available_seq: 9,
      status: "advanced",
    }
    fetchMock.mockResolvedValue(new Response(JSON.stringify(response), { status: 200 }))

    await expect(advanceMessageRetention({ channelId: "room-1", channelType: 2, throughSeq: 10, dryRun: true })).resolves.toEqual(response)

    expect(fetchMock).toHaveBeenCalledWith(
      "/manager/messages/retention",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify({ channel_id: "room-1", channel_type: 2, through_seq: 10, dry_run: true }),
      }),
    )
  })

  it("builds diagnostics trace query URLs", async () => {
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(emptyDiagnosticsResponse()), { status: 200 }))

    await getDiagnosticsTrace("tr 1", { nodeId: 2, limit: 50 })

    expect(fetchMock).toHaveBeenCalledWith(
      "/manager/diagnostics/trace/tr%201?node_id=2&limit=50",
      expect.any(Object),
    )
  })

  it("builds diagnostics message query URLs", async () => {
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(emptyDiagnosticsResponse()), { status: 200 }))

    await getDiagnosticsMessage({ clientMsgNo: "c-1", limit: 25 })

    expect(fetchMock).toHaveBeenCalledWith(
      "/manager/diagnostics/message?client_msg_no=c-1&limit=25",
      expect.any(Object),
    )
  })

  it("rejects invalid diagnostics message selectors before fetching", async () => {
    await expect(getDiagnosticsMessage({} as never)).rejects.toThrow("diagnostics message selector")
    await expect(getDiagnosticsMessage({ channelKey: "2:g1" } as never)).rejects.toThrow("diagnostics message selector")
    await expect(
      getDiagnosticsMessage({ clientMsgNo: "c-1", channelKey: "2:g1", messageSeq: 9 } as never),
    ).rejects.toThrow("diagnostics message selector")

    expect(fetchMock).not.toHaveBeenCalled()
  })

  it("builds diagnostics events query URLs", async () => {
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(emptyDiagnosticsResponse()), { status: 200 }))

    await getDiagnosticsEvents({ stage: "channel_append", result: "error", nodeId: 3 })

    expect(fetchMock).toHaveBeenCalledWith(
      "/manager/diagnostics/events?node_id=3&stage=channel_append&result=error",
      expect.any(Object),
    )
  })

  it("builds diagnostics events query with uid and channel key", async () => {
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(emptyDiagnosticsResponse()), { status: 200 }))

    await getDiagnosticsEvents({ uid: "u1", channelKey: "channel/2/ZzE", limit: 50 })

    expect(fetchMock).toHaveBeenCalledWith(
      "/manager/diagnostics/events?limit=50&uid=u1&channel_key=channel%2F2%2FZzE",
      expect.any(Object),
    )
  })

  it("lists diagnostics tracking rules", async () => {
    const payload = { status: "ok", rules: [], nodes: [], notes: [] }
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(payload), { status: 200 }))

    await expect(listDiagnosticsTrackingRules()).resolves.toEqual(payload)

    expect(fetchMock).toHaveBeenCalledWith(
      "/manager/diagnostics/tracking-rules",
      expect.any(Object),
    )
  })

  it("creates sender uid diagnostics tracking rule", async () => {
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({
      status: "ok",
      rule: { rule_id: "rule-1", target: "sender_uid", uid: "u1", sample_rate: 1 },
      nodes: [],
      notes: [],
    }), { status: 200 }))

    await createDiagnosticsTrackingRule({ target: "sender_uid", uid: "u1", ttlSeconds: 3600, sampleRate: 1 })

    expect(fetchMock).toHaveBeenCalledWith(
      "/manager/diagnostics/tracking-rules",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify({ target: "sender_uid", uid: "u1", ttl_seconds: 3600, sample_rate: 1 }),
      }),
    )
  })

  it("creates channel diagnostics tracking rule", async () => {
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({
      status: "ok",
      rule: { rule_id: "rule-2", target: "channel", channel_key: "channel/2/ZzE", sample_rate: 1 },
      nodes: [],
      notes: [],
    }), { status: 200 }))

    await createDiagnosticsTrackingRule({ target: "channel", channelId: "g1", channelType: 2, ttlSeconds: 600, sampleRate: 1 })

    expect(fetchMock).toHaveBeenCalledWith(
      "/manager/diagnostics/tracking-rules",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify({ target: "channel", channel_id: "g1", channel_type: 2, ttl_seconds: 600, sample_rate: 1 }),
      }),
    )
  })

  it("deletes diagnostics tracking rule", async () => {
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({ status: "ok", rule_id: "rule-1", nodes: [], notes: [] }), { status: 200 }))

    await deleteDiagnosticsTrackingRule("rule-1")

    expect(fetchMock).toHaveBeenCalledWith(
      "/manager/diagnostics/tracking-rules/rule-1",
      expect.objectContaining({ method: "DELETE" }),
    )
  })

  it("fetches connection list and detail data from manager endpoints", async () => {
    const connectionsResponse = {
      total: 1,
      items: [{
        node_id: 2,
        session_id: 101,
        uid: "u1",
        device_id: "device-a",
        device_flag: "app",
        device_level: "master",
        slot_id: 9,
        state: "active",
        listener: "tcp",
        connected_at: "2026-04-23T08:00:00Z",
        remote_addr: "10.0.0.1:5000",
        local_addr: "127.0.0.1:7000",
      }],
    }
    const connectionDetail = {
      ...connectionsResponse.items[0],
      state: "closing",
    }

    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(connectionsResponse), { status: 200 }))
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(connectionDetail), { status: 200 }))

    await expect(getConnections({ nodeId: 2 })).resolves.toEqual(connectionsResponse)
    await expect(getConnection(101, { nodeId: 2 })).resolves.toEqual(connectionDetail)
    expect(fetchMock).toHaveBeenNthCalledWith(
      1,
      "/manager/connections?node_id=2",
      expect.objectContaining({ headers: expect.any(Headers) }),
    )
    expect(fetchMock).toHaveBeenNthCalledWith(
      2,
      "/manager/connections/101?node_id=2",
      expect.objectContaining({ headers: expect.any(Headers) }),
    )
  })

  it("posts slot add and remove actions using backend endpoints", async () => {
    const slotDetail = {
      slot_id: 11,
      state: { quorum: "ready", sync: "matched" },
      assignment: { desired_peers: [1, 2, 3], config_epoch: 1, balance_version: 0 },
      runtime: {
        current_peers: [1, 2, 3],
        leader_id: 1,
        healthy_voters: 3,
        has_quorum: true,
        observed_config_epoch: 1,
        last_report_at: "2026-04-23T08:00:00Z",
      },
      task: null,
    }
    const removeResult = { slot_id: 11, result: "removal_started" }

    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(slotDetail), { status: 200 }))
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(removeResult), { status: 200 }))

    await expect(addSlot()).resolves.toEqual(slotDetail)
    let requestInit = fetchMock.mock.calls[0]?.[1] as { method: string; body?: string; headers: Headers }
    expect(fetchMock.mock.calls[0]?.[0]).toBe("/manager/slots")
    expect(requestInit.method).toBe("POST")
    expect(requestInit.body).toBeUndefined()
    expect(requestInit.headers.get("Content-Type")).toBeNull()

    await expect(removeSlot(11)).resolves.toEqual(removeResult)
    requestInit = fetchMock.mock.calls[1]?.[1] as { method: string }
    expect(fetchMock.mock.calls[1]?.[0]).toBe("/manager/slots/11")
    expect(requestInit.method).toBe("DELETE")
  })

  it("posts slot operator actions using backend request field names", async () => {
    const slotDetail = {
      slot_id: 9,
      state: { quorum: "ready", sync: "in_sync" },
      assignment: { desired_peers: [1, 2, 3], config_epoch: 7, balance_version: 4 },
      runtime: {
        current_peers: [1, 2, 3],
        leader_id: 2,
        healthy_voters: 3,
        has_quorum: true,
        observed_config_epoch: 7,
        last_report_at: "2026-04-23T08:00:00Z",
      },
      task: null,
    }
    const recoverResult = {
      strategy: "latest_live_replica",
      result: "scheduled",
      slot: slotDetail,
    }
    const rebalanceResult = {
      total: 1,
      items: [{ hash_slot: 3, from_slot_id: 9, to_slot_id: 11 }],
    }

    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(slotDetail), { status: 200 }))
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(recoverResult), { status: 200 }))
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(rebalanceResult), { status: 200 }))

    await expect(transferSlotLeader(9, { targetNodeId: 2 })).resolves.toEqual(slotDetail)
    let requestInit = fetchMock.mock.calls[0]?.[1] as { method: string; body: string; headers: Headers }
    expect(fetchMock.mock.calls[0]?.[0]).toBe("/manager/slots/9/leader/transfer")
    expect(requestInit.method).toBe("POST")
    expect(JSON.parse(requestInit.body)).toEqual({ target_node_id: 2 })
    expect(requestInit.headers.get("Content-Type")).toBe("application/json")

    await expect(recoverSlot(9, { strategy: "latest_live_replica" })).resolves.toEqual(recoverResult)
    requestInit = fetchMock.mock.calls[1]?.[1] as { method: string; body: string }
    expect(fetchMock.mock.calls[1]?.[0]).toBe("/manager/slots/9/recover")
    expect(JSON.parse(requestInit.body)).toEqual({ strategy: "latest_live_replica" })

    await expect(rebalanceSlots()).resolves.toEqual(rebalanceResult)
    requestInit = fetchMock.mock.calls[2]?.[1] as { method: string }
    expect(fetchMock.mock.calls[2]?.[0]).toBe("/manager/slots/rebalance")
    expect(requestInit.method).toBe("POST")
  })

  it("fetches tasks list and detail data", async () => {
    const tasksResponse = {
      total: 1,
      items: [{
        slot_id: 9,
        kind: "rebalance",
        step: "plan",
        status: "retrying",
        source_node: 1,
        target_node: 2,
        attempt: 2,
        next_run_at: null,
        last_error: "",
      }],
    }
    const taskDetail = {
      ...tasksResponse.items[0],
      slot: {
        state: { quorum: "ready", sync: "in_sync" },
        assignment: { desired_peers: [1, 2, 3], config_epoch: 7, balance_version: 4 },
        runtime: {
          current_peers: [1, 2, 3],
          leader_id: 2,
          healthy_voters: 3,
          has_quorum: true,
          observed_config_epoch: 7,
          last_report_at: "2026-04-23T08:00:00Z",
        },
      },
    }

    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(tasksResponse), { status: 200 }))
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(taskDetail), { status: 200 }))

    await expect(getTasks()).resolves.toEqual(tasksResponse)
    await expect(getTask(9)).resolves.toEqual(taskDetail)
    expect(fetchMock).toHaveBeenNthCalledWith(1, "/manager/tasks", expect.anything())
    expect(fetchMock).toHaveBeenNthCalledWith(2, "/manager/tasks/9", expect.anything())
  })

  it("fetches distributed task summary, list, and detail data", async () => {
    const summary = {
      total: 1,
      by_status: { pending: 0, running: 0, retrying: 1, blocked: 0, failed: 0, completed: 0, cancelled: 0, unknown: 0 },
      by_domain: { slot_reconcile: 1, node_onboarding: 0, node_scale_in: 0, channel_migration: 0 },
      partial: false,
      warnings: [],
    }
    const list = {
      total: 1,
      items: [{
        id: "slot-reconcile:1",
        domain: "slot_reconcile",
        kind: "repair",
        status: "retrying",
        phase: "catch_up",
        scope: { type: "slot", id: "1", slot_id: 1, channel_id: "", channel_type: 0, node_id: 0 },
        source_node: 0,
        target_node: 3,
        owner_node: 0,
        attempt: 1,
        next_run_at: null,
        created_at: null,
        updated_at: "2026-05-14T10:00:00Z",
        last_error: "",
        summary: "Slot 1 repair is retrying.",
        links: { slot: "/slots?slot_id=1" },
      }],
      next_cursor: "",
      has_more: false,
      partial: false,
      warnings: [],
    }
    const detail = { task: list.items[0], detail: { domain: "slot_reconcile", raw_status: "retrying", slot: null } }

    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(summary), { status: 200 }))
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(list), { status: 200 }))
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(detail), { status: 200 }))

    await expect(getDistributedTasksSummary()).resolves.toEqual(summary)
    await expect(getDistributedTasks({
      domain: "slot_reconcile",
      status: "retrying",
      nodeId: 3,
      scope: "slot",
      keyword: "repair",
      limit: 25,
      cursor: "abc",
    })).resolves.toEqual(list)
    await expect(getDistributedTask("slot_reconcile", "slot-reconcile:1")).resolves.toEqual(detail)

    expect(fetchMock).toHaveBeenNthCalledWith(1, "/manager/distributed-tasks/summary", expect.anything())
    expect(fetchMock).toHaveBeenNthCalledWith(2, "/manager/distributed-tasks?domain=slot_reconcile&status=retrying&node_id=3&scope=slot&keyword=repair&limit=25&cursor=abc", expect.anything())
    expect(fetchMock).toHaveBeenNthCalledWith(3, "/manager/distributed-tasks/slot_reconcile/slot-reconcile%3A1", expect.anything())
  })

  it("fetches channel runtime metadata list and detail data", async () => {
    const listResponse = {
      items: [{
        channel_id: "u1@u2",
        channel_type: 1,
        slot_id: 9,
        channel_epoch: 7,
        leader_epoch: 3,
        leader: 2,
        replicas: [1, 2, 3],
        isr: [2, 3],
        min_isr: 2,
        status: "active",
      }],
      has_more: true,
      next_cursor: "cursor-2",
    }
    const detailResponse = {
      ...listResponse.items[0],
      hash_slot: 18,
      features: 3,
      lease_until_ms: 123456789,
    }

    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(listResponse), { status: 200 }))
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(detailResponse), { status: 200 }))

    await expect(
      getChannelRuntimeMeta({ nodeId: 2, limit: 100, cursor: "cursor-1", includeMaxMessageSeq: true }),
    ).resolves.toEqual(listResponse)
    await expect(getChannelRuntimeMetaDetail(1, "u1@u2")).resolves.toEqual(detailResponse)
    expect(fetchMock.mock.calls[0]?.[0]).toBe(
      "/manager/channel-runtime-meta?node_id=2&limit=100&cursor=cursor-1&include_max_message_seq=true",
    )
    expect(fetchMock.mock.calls[1]?.[0]).toBe("/manager/channel-runtime-meta/1/u1%40u2")
  })

  it("passes the channel ID fuzzy filter to channel runtime meta list requests", async () => {
    const listResponse = {
      items: [],
      has_more: false,
    }

    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(listResponse), { status: 200 }))

    await expect(getChannelRuntimeMeta({ channelId: "room", limit: 15 })).resolves.toEqual(listResponse)
    expect(fetchMock.mock.calls[0]?.[0]).toBe("/manager/channel-runtime-meta?limit=15&channel_id=room")
  })

  it("fetches local runtime workqueue pressure", async () => {
    const response = {
      generated_at: "2026-06-17T10:00:00Z",
      window_seconds: 10,
      scope: { view: "local_node", node_id: 1, node_name: "node-1", ready: true },
      summary: {
        overall_level: "degraded",
        total: 1,
        ok: 0,
        busy: 0,
        degraded: 1,
        critical: 0,
        hottest: {
          component: "gateway",
          pool: "async_send",
          queue: "send",
          priority: "none",
          level: "degraded",
          score: 0.82,
        },
      },
      items: [
        {
          component: "gateway",
          pool: "async_send",
          queue: "send",
          priority: "none",
          level: "degraded",
          score: 0.82,
          depth: 82,
          capacity: 100,
          inflight: 0,
          workers: 0,
          wait_p99_ms: 12.4,
          task_p99_ms: 20.5,
          admission_error_per_sec: 0.3,
          hint: "queue depth is approaching capacity",
        },
      ],
      sources: {
        collector: { available: true, sample_count: 10 },
        metrics: { enabled: false, required: false },
        notes: [],
      },
    }
    fetchMock.mockResolvedValue(new Response(JSON.stringify(response), { status: 200 }))

    await expect(getRuntimeWorkqueues({ window: "10s", limit: 100 })).resolves.toEqual(response)
    expect(fetchMock).toHaveBeenCalledWith(
      "/manager/runtime/workqueues?window=10s&limit=100",
      expect.objectContaining({ headers: expect.any(Headers) }),
    )
  })

  it("fetches application log sources, entries, and stream responses", async () => {
    const sources = {
      node_id: 2,
      sources: [{ name: "app", file: "app.log", available: true, size_bytes: 128, modified_at: "2026-06-17T10:00:00Z" }],
    }
    const entries = {
      node_id: 2,
      source: "warn",
      cursor: "next-cursor",
      rotated: false,
      items: [{
        seq: 7,
        offset: 42,
        time: "2026-06-17T10:00:01Z",
        level: "WARN",
        module: "cluster",
        caller: "server.go:10",
        message: "slow append",
        fields: { channel: "g1" },
        raw: "raw line",
        truncated: false,
      }],
    }

    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(sources), { status: 200 }))
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(entries), { status: 200 }))
    fetchMock.mockResolvedValueOnce(new Response("{}", { status: 200 }))

    await expect(getApplicationLogSources(2)).resolves.toEqual(sources)
    await expect(
      getApplicationLogEntries({
        nodeId: 2,
        source: "warn",
        limit: 100,
        cursor: "opaque cursor",
        keyword: "slow",
        levels: ["WARN", "ERROR"],
      }),
    ).resolves.toEqual(entries)
    await streamApplicationLogEntries({ nodeId: 2, source: "error", cursor: "c1", levels: ["ERROR"] })

    expect(fetchMock).toHaveBeenNthCalledWith(
      1,
      "/manager/app-logs/sources?node_id=2",
      expect.objectContaining({ headers: expect.any(Headers) }),
    )
    expect(fetchMock).toHaveBeenNthCalledWith(
      2,
      "/manager/app-logs?node_id=2&source=warn&limit=100&cursor=opaque+cursor&keyword=slow&levels=WARN%2CERROR",
      expect.objectContaining({ headers: expect.any(Headers) }),
    )
    const streamInit = fetchMock.mock.calls[2]?.[1] as { headers: Headers }
    expect(fetchMock.mock.calls[2]?.[0]).toBe("/manager/app-logs/stream?node_id=2&source=error&cursor=c1&levels=ERROR")
    expect(streamInit.headers.get("Accept")).toBe("application/x-ndjson")
  })

  it("fetches channel cluster summary from the manager endpoint", async () => {
    const summaryResponse = {
      total: 4,
      healthy: 1,
      isr_insufficient: 2,
      no_leader: 1,
      avg_replicas: 2,
      avg_isr: 1.5,
      leader_distribution: [
        { node_id: 1, count: 3 },
        { node_id: 2, count: 1 },
      ],
    }
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(summaryResponse), { status: 200 }))

    await expect(getChannelClusterSummary()).resolves.toEqual(summaryResponse)
    expect(fetchMock.mock.calls[0]?.[0]).toBe("/manager/channel-cluster/summary")
  })

  it("fetches unhealthy channel cluster rows with pagination query", async () => {
    const unhealthyResponse = {
      items: [{
        channel_id: "room-1",
        channel_type: 2,
        slot_id: 9,
        channel_epoch: 7,
        leader_epoch: 3,
        leader: 0,
        replicas: [1, 2, 3],
        isr: [2],
        min_isr: 2,
        max_message_seq: 42,
        status: "active",
        reasons: ["isr_insufficient", "no_leader"],
      }],
      has_more: true,
      next_cursor: "cursor-2",
    }
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(unhealthyResponse), { status: 200 }))

    await expect(getChannelClusterUnhealthy({ limit: 50, cursor: "cursor-1" })).resolves.toEqual(unhealthyResponse)
    expect(fetchMock.mock.calls[0]?.[0]).toBe("/manager/channel-cluster/unhealthy?limit=50&cursor=cursor-1")
  })


  it("fetches channel cluster replica detail", async () => {
    const detailResponse = {
      channel: {
        channel_id: "room-1",
        channel_type: 2,
        slot_id: 9,
        hash_slot: 123,
        channel_epoch: 7,
        leader_epoch: 3,
        leader: 1,
        replicas: [1, 2],
        isr: [1],
        min_isr: 1,
        max_message_seq: 42,
        status: "active",
        features: 0,
        lease_until_ms: 0,
      },
      runtime_reported: true,
      commit_seq: 42,
      min_available_seq: 1,
      retention_through_seq: 0,
      replicas: [
        { node_id: 1, role: "leader", is_leader: true, in_isr: true, reported: true, commit_seq: 42, leo: null, checkpoint_hw: null, lag: 0 },
        { node_id: 2, role: "follower", is_leader: false, in_isr: false, reported: false, commit_seq: null, leo: null, checkpoint_hw: null, lag: null },
      ],
    }
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(detailResponse), { status: 200 }))

    await expect(getChannelClusterReplicas(2, "room-1")).resolves.toEqual(detailResponse)
    expect(fetchMock.mock.calls[0]?.[0]).toBe("/manager/channel-cluster/2/room-1/replicas")
  })

  it("repairs a channel cluster leader and maps conflict errors", async () => {
    const repairResponse = {
      changed: true,
      channel: {
        channel_id: "room-1",
        channel_type: 2,
        slot_id: 9,
        hash_slot: 123,
        channel_epoch: 7,
        leader_epoch: 4,
        leader: 2,
        replicas: [1, 2],
        isr: [2],
        min_isr: 1,
        max_message_seq: 42,
        status: "active",
        features: 0,
        lease_until_ms: 0,
      },
    }
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(repairResponse), { status: 200 }))
    fetchMock.mockResolvedValueOnce(
      new Response(JSON.stringify({ error: "conflict", message: "no safe channel leader candidate" }), { status: 409 }),
    )

    await expect(repairChannelClusterLeader(2, "room-1", { reason: "no_leader" })).resolves.toEqual(repairResponse)
    expect(fetchMock.mock.calls[0]?.[0]).toBe("/manager/channel-cluster/2/room-1/repair")
    expect(fetchMock.mock.calls[0]?.[1]).toEqual(expect.objectContaining({
      method: "POST",
      body: JSON.stringify({ reason: "no_leader" }),
    }))
    await expect(repairChannelClusterLeader(2, "room-1", { reason: "no_leader" })).rejects.toMatchObject({
      status: 409,
      error: "conflict",
    })
  })

  it("transfers a channel cluster leader", async () => {
    const transferResponse = {
      changed: true,
      channel: {
        channel_id: "room-1",
        channel_type: 2,
        slot_id: 9,
        hash_slot: 123,
        channel_epoch: 7,
        leader_epoch: 4,
        leader: 3,
        replicas: [1, 2, 3],
        isr: [1, 2, 3],
        min_isr: 2,
        max_message_seq: 42,
        status: "active",
        features: 0,
        lease_until_ms: 0,
      },
    }
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(transferResponse), { status: 200 }))

    await expect(transferChannelClusterLeader(2, "room-1", { target_node_id: 3 })).resolves.toEqual(transferResponse)
    expect(fetchMock.mock.calls[0]?.[0]).toBe("/manager/channel-cluster/2/room-1/leader/transfer")
    expect(fetchMock.mock.calls[0]?.[1]).toEqual(expect.objectContaining({
      method: "POST",
      body: JSON.stringify({ target_node_id: 3 }),
    }))
  })


  it("calls node onboarding manager endpoints", async () => {
    const candidates = {
      total: 1,
      items: [{
        node_id: 4,
        name: "node-4",
        addr: "127.0.0.1:7004",
        role: "data",
        join_state: "active",
        status: "alive",
        slot_count: 0,
        leader_count: 0,
        recommended: true,
      }],
    }
    const job = {
      job_id: "onboard-1",
      target_node_id: 4,
      retry_of_job_id: "",
      status: "planned",
      created_at: "2026-04-26T12:00:00Z",
      updated_at: "2026-04-26T12:00:00Z",
      started_at: "0001-01-01T00:00:00Z",
      completed_at: "0001-01-01T00:00:00Z",
      plan_version: 1,
      plan_fingerprint: "fp-1",
      plan: {
        target_node_id: 4,
        summary: {
          current_target_slot_count: 0,
          planned_target_slot_count: 1,
          current_target_leader_count: 0,
          planned_leader_gain: 1,
        },
        moves: [{
          slot_id: 2,
          source_node_id: 1,
          target_node_id: 4,
          reason: "underloaded_target",
          desired_peers_before: [1, 2, 3],
          desired_peers_after: [2, 3, 4],
          current_leader_id: 1,
          leader_transfer_required: true,
        }],
        blocked_reasons: [],
      },
      moves: [{
        slot_id: 2,
        source_node_id: 1,
        target_node_id: 4,
        status: "pending",
        task_kind: "rebalance",
        task_slot_id: 2,
        started_at: "0001-01-01T00:00:00Z",
        completed_at: "0001-01-01T00:00:00Z",
        last_error: "",
        desired_peers_before: [1, 2, 3],
        desired_peers_after: [2, 3, 4],
        leader_before: 1,
        leader_after: 0,
        leader_transfer_required: true,
      }],
      current_move_index: -1,
      result_counts: { pending: 1, running: 0, completed: 0, failed: 0, skipped: 0 },
      last_error: "",
    }
    const jobs = { items: [job], next_cursor: "cursor-2", has_more: true }

    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(candidates), { status: 200 }))
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(job), { status: 200 }))
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(job), { status: 200 }))
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(jobs), { status: 200 }))
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(job), { status: 200 }))
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(job), { status: 200 }))

    await expect(getNodeOnboardingCandidates()).resolves.toEqual(candidates)
    await expect(createNodeOnboardingPlan({ targetNodeId: 4 })).resolves.toEqual(job)
    await expect(startNodeOnboardingJob("onboard-1")).resolves.toEqual(job)
    await expect(getNodeOnboardingJobs({ limit: 25, cursor: "cursor-1" })).resolves.toEqual(jobs)
    await expect(getNodeOnboardingJob("onboard-1")).resolves.toEqual(job)
    await expect(retryNodeOnboardingJob("onboard-1")).resolves.toEqual(job)

    expect(fetchMock.mock.calls[0]?.[0]).toBe("/manager/node-onboarding/candidates")
    expect(fetchMock.mock.calls[1]?.[0]).toBe("/manager/node-onboarding/plan")
    expect(JSON.parse((fetchMock.mock.calls[1]?.[1] as { body: string }).body)).toEqual({ target_node_id: 4 })
    expect(fetchMock.mock.calls[2]?.[0]).toBe("/manager/node-onboarding/jobs/onboard-1/start")
    expect(fetchMock.mock.calls[3]?.[0]).toBe("/manager/node-onboarding/jobs?limit=25&cursor=cursor-1")
    expect(fetchMock.mock.calls[4]?.[0]).toBe("/manager/node-onboarding/jobs/onboard-1")
    expect(fetchMock.mock.calls[5]?.[0]).toBe("/manager/node-onboarding/jobs/onboard-1/retry")
  })

  it("calls node scale-in manager endpoints using backend request field names", async () => {
    const report = {
      node_id: 3,
      status: "migrating_replicas",
      safe_to_remove: false,
      can_start: false,
      can_advance: true,
      can_cancel: true,
      connection_safety_verified: true,
      blocked_reasons: [],
      checks: {
        target_exists: true,
        target_is_data_node: true,
        target_is_active_or_draining: true,
        target_is_not_controller_voter: true,
        tail_node_mapping_verified: true,
        remaining_data_nodes_enough: true,
        controller_leader_available: true,
        slot_replica_count_known: true,
        no_other_draining_node: true,
        no_active_hashslot_migrations: true,
        no_running_onboarding: true,
        no_active_reconcile_tasks_involving_target: true,
        no_failed_reconcile_tasks: true,
        runtime_views_complete_and_fresh: true,
        all_slots_have_quorum: true,
        target_not_unique_healthy_replica: true,
      },
      progress: {
        assigned_slot_replicas: 4,
        observed_slot_replicas: 4,
        slot_leaders: 1,
        active_tasks_involving_node: 2,
        active_migrations_involving_node: 0,
        active_connections: 128,
        closing_connections: 0,
        gateway_sessions: 128,
        active_connections_unknown: false,
      },
      runtime: {
        node_id: 3,
        active_online: 128,
        closing_online: 0,
        total_online: 128,
        gateway_sessions: 128,
        sessions_by_listener: { tcp: 128 },
        accepting_new_sessions: false,
        draining: true,
        unknown: false,
      },
      leaders: [],
      next_action: "wait_reconcile_tasks",
    }

    fetchMock.mockImplementation(() => Promise.resolve(new Response(JSON.stringify(report), { status: 200 })))

    await expect(planNodeScaleIn(3, {
      confirmStatefulSetTail: true,
      expectedTailNodeId: 3,
    })).resolves.toEqual(report)
    await expect(startNodeScaleIn(3, {
      confirmStatefulSetTail: true,
      expectedTailNodeId: 3,
    })).resolves.toEqual(report)
    await expect(getNodeScaleInStatus(3)).resolves.toEqual(report)
    await expect(advanceNodeScaleIn(3, {
      maxLeaderTransfers: 2,
      maxChannelMigrations: 4,
    })).resolves.toEqual(report)
    await expect(cancelNodeScaleIn(3)).resolves.toEqual(report)

    expect(fetchMock.mock.calls[0]?.[0]).toBe("/manager/nodes/3/scale-in/plan")
    expect(JSON.parse((fetchMock.mock.calls[0]?.[1] as { body: string }).body)).toEqual({
      confirm_statefulset_tail: true,
      expected_tail_node_id: 3,
    })
    expect(fetchMock.mock.calls[1]?.[0]).toBe("/manager/nodes/3/scale-in/start")
    expect(JSON.parse((fetchMock.mock.calls[1]?.[1] as { body: string }).body)).toEqual({
      confirm_statefulset_tail: true,
      expected_tail_node_id: 3,
    })
    expect(fetchMock.mock.calls[2]?.[0]).toBe("/manager/nodes/3/scale-in/status")
    expect((fetchMock.mock.calls[2]?.[1] as { method?: string }).method).toBeUndefined()
    expect(fetchMock.mock.calls[3]?.[0]).toBe("/manager/nodes/3/scale-in/advance")
    expect(JSON.parse((fetchMock.mock.calls[3]?.[1] as { body: string }).body)).toEqual({
      max_leader_transfers: 2,
      max_channel_migrations: 4,
      force_close_connections: false,
    })
    expect(fetchMock.mock.calls[4]?.[0]).toBe("/manager/nodes/3/scale-in/cancel")
    expect((fetchMock.mock.calls[4]?.[1] as { method: string }).method).toBe("POST")
  })

  it("keeps a scale-in block report on ManagerApiError", async () => {
    const report = {
      node_id: 3,
      status: "blocked",
      safe_to_remove: false,
      can_start: false,
      can_advance: false,
      can_cancel: false,
      connection_safety_verified: false,
      blocked_reasons: [{ code: "controller_voter", message: "controller voter cannot be removed", count: 0, slot_id: 0, node_id: 3 }],
      checks: {},
      progress: {},
      runtime: null,
      leaders: [],
      next_action: "fix_blockers",
    }
    fetchMock.mockResolvedValue(
      new Response(JSON.stringify({ error: "scale_in_blocked", message: "blocked", report }), { status: 409 }),
    )

    await expect(startNodeScaleIn(3, {
      confirmStatefulSetTail: true,
      expectedTailNodeId: 3,
    })).rejects.toMatchObject({
      status: 409,
      error: "scale_in_blocked",
      message: "blocked",
      report,
    })
  })

  it.each([
    [403, "forbidden", "forbidden"],
    [404, "not_found", "node not found"],
    [409, "conflict", "slot migrations already in progress"],
    [503, "service_unavailable", "controller leader unavailable"],
  ])("maps %i manager errors into ManagerApiError", async (status, error, message) => {
    fetchMock.mockResolvedValue(
      new Response(JSON.stringify({ error, message }), { status }),
    )

    await expect(getNodes()).rejects.toEqual(new ManagerApiError(status, error, message))
  })
})
