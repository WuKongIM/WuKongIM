import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import {
  configureManagerAuth,
  getChannelRuntimeMeta,
  getChannelRuntimeMetaDetail,
  getConnection,
  getConnections,
  getMessages,
  getNode,
  getNodes,
  getOverview,
  getSlot,
  getSlots,
  getTask,
  getTasks,
  loginManager,
  managerFetch,
  ManagerApiError,
  markNodeDraining,
  rebalanceSlots,
  recoverSlot,
  resetManagerAuthConfig,
  resumeNode,
  transferSlotLeader,
} from "@/lib/manager-api"

describe("manager api client", () => {
  const fetchMock = vi.fn()

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

  it("fetches node list and detail data from manager endpoints", async () => {
    const nodesResponse = {
      total: 1,
      items: [{
        node_id: 1,
        addr: "127.0.0.1:7000",
        status: "alive",
        last_heartbeat_at: "2026-04-23T08:00:00Z",
        is_local: true,
        capacity_weight: 1,
        controller: { role: "leader" },
        slot_stats: { count: 3, leader_count: 2 },
      }],
    }
    const nodeDetail = {
      ...nodesResponse.items[0],
      slots: {
        hosted_ids: [1, 2, 3],
        leader_ids: [1, 2],
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
      addr: "127.0.0.1:7000",
      status: "draining",
      last_heartbeat_at: "2026-04-23T08:00:00Z",
      is_local: false,
      capacity_weight: 1,
      controller: { role: "follower" },
      slot_stats: { count: 3, leader_count: 0 },
      slots: { hosted_ids: [1, 2], leader_ids: [] },
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

  it("fetches connection list and detail data from manager endpoints", async () => {
    const connectionsResponse = {
      total: 1,
      items: [{
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

    await expect(getConnections()).resolves.toEqual(connectionsResponse)
    await expect(getConnection(101)).resolves.toEqual(connectionDetail)
    expect(fetchMock).toHaveBeenNthCalledWith(
      1,
      "/manager/connections",
      expect.objectContaining({ headers: expect.any(Headers) }),
    )
    expect(fetchMock).toHaveBeenNthCalledWith(
      2,
      "/manager/connections/101",
      expect.objectContaining({ headers: expect.any(Headers) }),
    )
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

    await expect(getChannelRuntimeMeta({ limit: 100, cursor: "cursor-1" })).resolves.toEqual(listResponse)
    await expect(getChannelRuntimeMetaDetail(1, "u1@u2")).resolves.toEqual(detailResponse)
    expect(fetchMock.mock.calls[0]?.[0]).toBe("/manager/channel-runtime-meta?limit=100&cursor=cursor-1")
    expect(fetchMock.mock.calls[1]?.[0]).toBe("/manager/channel-runtime-meta/1/u1%40u2")
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
