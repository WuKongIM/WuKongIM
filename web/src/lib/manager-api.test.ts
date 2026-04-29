import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import {
  configureManagerAuth,
  getChannelRuntimeMeta,
  getChannelRuntimeMetaDetail,
  getConnection,
  getConnections,
  getMessages,
  createNodeOnboardingPlan,
  getNode,
  getNodeOnboardingCandidates,
  getNodeOnboardingJob,
  getNodeOnboardingJobs,
  getNodes,
  getNodeScaleInStatus,
  getOverview,
  getSlot,
  getSlots,
  getTask,
  getTasks,
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
  retryNodeOnboardingJob,
  planNodeScaleIn,
  startNodeScaleIn,
  advanceNodeScaleIn,
  cancelNodeScaleIn,
  startNodeOnboardingJob,
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
      maxLeaderTransfers: 8,
      forceCloseConnections: true,
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
      max_leader_transfers: 8,
      force_close_connections: true,
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
