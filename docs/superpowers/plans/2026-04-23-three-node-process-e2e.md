# Three-Node Process E2E Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extend the existing `test/e2e` black-box harness to start a real three-node cluster and add one stable cross-node WKProto send/receive closure test that uses `/readyz` and `/manager/slots/1` instead of fixed sleeps.

**Architecture:** Keep the phase-1 external boundary intact: build the production binary once, generate real `WK_` config files, start real child processes, and assert behavior only through WKProto and existing HTTP endpoints. Add a small manager/health probe layer in `test/e2e/suite`, extend startup from one node to three nodes, and keep diagnostics node-scoped so later leader-crash and rolling-restart tests can reuse the same `StartedCluster` foundation.

**Tech Stack:** Go 1.23, `testing`, `testify/require`, `net/http`, `encoding/json`, `os/exec`, `pkg/protocol/frame`, existing `test/e2e/suite` helpers, `//go:build e2e`.

---

## References

- Spec: `docs/superpowers/specs/2026-04-23-three-node-process-e2e-design.md`
- Existing baseline: `docs/superpowers/plans/2026-04-23-process-e2e-foundation.md`
- Existing process e2e entrypoint: `test/e2e/e2e_test.go`
- Existing single-node harness: `test/e2e/suite/runtime.go`
- Existing readiness helper: `test/e2e/suite/readiness.go`
- Manager slot DTO contract: `internal/access/manager/slots.go`
- Manager connection DTO contract: `internal/access/manager/connections.go`
- Follow `@superpowers:test-driven-development` for every behavior-bearing slice.
- Run `@superpowers:verification-before-completion` before claiming execution is done.
- In Codex CLI, only use subagents if the user explicitly asks for delegation; otherwise execute this plan locally with `@superpowers:executing-plans`.
- Re-check `FLOW.md` before editing each touched package; as of plan writing, there is no `FLOW.md` under `test/e2e`, `internal/access/manager`, or `internal/usecase/management`.

## Critical Review Notes Before Starting

- Keep `test/e2e` black-box. Do not import `internal/app` or reuse the gray-box multinode harness.
- Do not use fixed `time.Sleep` windows as readiness or topology correctness conditions. Poll `/readyz` and `/manager/slots/1` instead.
- Keep manager probing loopback-only and test-only: `WK_MANAGER_LISTEN_ADDR=127.0.0.1:<port>` and `WK_MANAGER_AUTH_ON=false`.
- Preserve the existing single-node-cluster e2e test as the baseline; phase 2 adds a three-node path, it does not replace phase 1.
- Force all process artifacts under `t.TempDir()` and explicitly set `WK_LOG_DIR` so repo-local `./logs` is never used during e2e runs.
- Add English comments for new important structs, fields, and non-obvious helpers to satisfy `AGENTS.md`.
- Prefer adding small, focused helper files in `test/e2e/suite` instead of making `runtime.go` or `readiness.go` absorb every new concern.

## File Structure

- Modify: `test/e2e/README.md` — document the new three-node scope, manager probing, and diagnostics expectations.
- Modify: `test/e2e/e2e_test.go` — add the new three-node cluster scenario while preserving the single-node baseline.
- Modify: `test/e2e/suite/config.go` — extend `NodeSpec`, render loopback manager config, explicit log dirs, and a reusable three-node config renderer.
- Modify: `test/e2e/suite/config_test.go` — lock manager/listener/log rendering semantics for one-node and three-node configs.
- Modify: `test/e2e/suite/ports.go` — reserve manager ports in addition to cluster/gateway/API ports.
- Modify: `test/e2e/suite/ports_test.go` — assert the expanded port set stays distinct and loopback-bound.
- Modify: `test/e2e/suite/process.go` — include app-log paths and bounded log-tail diagnostics instead of dumping full logs.
- Modify: `test/e2e/suite/process_test.go` — lock diagnostic formatting and log-tail behavior.
- Modify: `test/e2e/suite/readiness.go` — add `/readyz` polling and composed node/cluster readiness helpers.
- Modify: `test/e2e/suite/readiness_test.go` — add HTTP-ready tests and node-ready composition tests.
- Modify: `test/e2e/suite/runtime.go` — add `StartedCluster`, multi-node workspace helpers, `StartThreeNodeCluster`, and cleanup wiring.
- Modify: `test/e2e/suite/runtime_test.go` — lock node-scoped log paths, three-node workspace layout, and cluster handle lookup behavior.
- Create: `test/e2e/suite/manager_client.go` — HTTP helpers for `/manager/slots/:slot_id`, `/manager/connections`, and topology resolution.
- Create: `test/e2e/suite/manager_client_test.go` — assert DTO decoding, topology validation, and connection lookup helpers.

### Task 1: Extend static suite primitives for three-node configs, manager ports, and log isolation

**Files:**
- Modify: `test/e2e/suite/config.go`
- Modify: `test/e2e/suite/config_test.go`
- Modify: `test/e2e/suite/ports.go`
- Modify: `test/e2e/suite/ports_test.go`
- Modify: `test/e2e/suite/runtime.go`
- Modify: `test/e2e/suite/runtime_test.go`

- [ ] **Step 1: Write the failing static tests first**

Add focused tests for the new static contract:

- `TestRenderSingleNodeConfigIncludesManagerLoopbackAndLogDir`
- `TestRenderThreeNodeClusterConfigIncludesAllNodesAndReplicaCounts`
- `TestReserveLoopbackPortsReturnsDistinctManagerAddress`
- `TestNewWorkspaceCreatesNodeScopedLogDirPaths`

Use concrete assertions like:

```go
func TestRenderThreeNodeClusterConfigIncludesAllNodesAndReplicaCounts(t *testing.T) {
    specs := []NodeSpec{
        {ID: 1, Name: "node-1", DataDir: "/tmp/node-1/data", ClusterAddr: "127.0.0.1:17001", GatewayAddr: "127.0.0.1:15101", APIAddr: "127.0.0.1:18081", ManagerAddr: "127.0.0.1:19081", LogDir: "/tmp/node-1/logs"},
        {ID: 2, Name: "node-2", DataDir: "/tmp/node-2/data", ClusterAddr: "127.0.0.1:17002", GatewayAddr: "127.0.0.1:15102", APIAddr: "127.0.0.1:18082", ManagerAddr: "127.0.0.1:19082", LogDir: "/tmp/node-2/logs"},
        {ID: 3, Name: "node-3", DataDir: "/tmp/node-3/data", ClusterAddr: "127.0.0.1:17003", GatewayAddr: "127.0.0.1:15103", APIAddr: "127.0.0.1:18083", ManagerAddr: "127.0.0.1:19083", LogDir: "/tmp/node-3/logs"},
    }

    cfg := RenderClusterConfig(specs[0], specs)
    require.Contains(t, cfg, "WK_CLUSTER_CONTROLLER_REPLICA_N=3")
    require.Contains(t, cfg, "WK_CLUSTER_SLOT_REPLICA_N=3")
    require.Contains(t, cfg, "WK_CLUSTER_INITIAL_SLOT_COUNT=1")
    require.Contains(t, cfg, `WK_MANAGER_LISTEN_ADDR=127.0.0.1:19081`)
    require.Contains(t, cfg, "WK_MANAGER_AUTH_ON=false")
    require.Contains(t, cfg, "WK_LOG_DIR=/tmp/node-1/logs")
    require.Contains(t, cfg, `{"id":2,"addr":"127.0.0.1:17002"}`)
    require.Contains(t, cfg, `{"id":3,"addr":"127.0.0.1:17003"}`)
}
```

- [ ] **Step 2: Run the focused static suite tests and confirm they fail**

Run:

```bash
go test -tags=e2e ./test/e2e/suite -run 'Test(RenderSingleNodeConfigIncludesManagerLoopbackAndLogDir|RenderThreeNodeClusterConfigIncludesAllNodesAndReplicaCounts|ReserveLoopbackPortsReturnsDistinctManagerAddress|NewWorkspaceCreatesNodeScopedLogDirPaths)$' -count=1
```

Expected: FAIL because `ManagerAddr`, `LogDir`, the three-node renderer, and the new workspace helpers do not exist yet.

- [ ] **Step 3: Implement the minimal static primitives**

Make the smallest focused changes that satisfy the tests:

- extend `NodeSpec` with `ManagerAddr` and `LogDir`
- extend `PortSet` with `ManagerAddr`
- add `Workspace.NodeLogDir(nodeID)`
- keep `RenderSingleNodeConfig` working for the phase-1 test
- add a reusable `RenderClusterConfig(local NodeSpec, all []NodeSpec)` helper for three-node use

Use a simple renderer shape like:

```go
func RenderClusterConfig(local NodeSpec, all []NodeSpec) string {
    lines := []string{
        fmt.Sprintf("WK_NODE_ID=%d", local.ID),
        fmt.Sprintf("WK_NODE_NAME=%s", local.Name),
        fmt.Sprintf("WK_NODE_DATA_DIR=%s", local.DataDir),
        fmt.Sprintf("WK_CLUSTER_LISTEN_ADDR=%s", local.ClusterAddr),
        "WK_CLUSTER_SLOT_COUNT=1",
        "WK_CLUSTER_INITIAL_SLOT_COUNT=1",
        "WK_CLUSTER_CONTROLLER_REPLICA_N=3",
        "WK_CLUSTER_SLOT_REPLICA_N=3",
        fmt.Sprintf("WK_API_LISTEN_ADDR=%s", local.APIAddr),
        fmt.Sprintf("WK_MANAGER_LISTEN_ADDR=%s", local.ManagerAddr),
        "WK_MANAGER_AUTH_ON=false",
        fmt.Sprintf("WK_LOG_DIR=%s", local.LogDir),
        fmt.Sprintf("WK_CLUSTER_NODES=%s", marshalClusterNodes(all)),
        fmt.Sprintf(`WK_GATEWAY_LISTENERS=[{"name":"tcp-wkproto","network":"tcp","address":"%s","transport":"stdnet","protocol":"wkproto"}]`, local.GatewayAddr),
    }
    return strings.Join(lines, "\n") + "\n"
}
```

- [ ] **Step 4: Re-run the focused static tests and confirm they pass**

Run:

```bash
go test -tags=e2e ./test/e2e/suite -run 'Test(RenderSingleNodeConfigIncludesManagerLoopbackAndLogDir|RenderThreeNodeClusterConfigIncludesAllNodesAndReplicaCounts|ReserveLoopbackPortsReturnsDistinctManagerAddress|NewWorkspaceCreatesNodeScopedLogDirPaths)$' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the static three-node slice**

Run:

```bash
git add test/e2e/suite/config.go test/e2e/suite/config_test.go test/e2e/suite/ports.go test/e2e/suite/ports_test.go test/e2e/suite/runtime.go test/e2e/suite/runtime_test.go
git commit -m "test: extend e2e suite for three-node config"
```

### Task 2: Add manager and health probe helpers with topology validation

**Files:**
- Modify: `test/e2e/suite/readiness.go`
- Modify: `test/e2e/suite/readiness_test.go`
- Create: `test/e2e/suite/manager_client.go`
- Create: `test/e2e/suite/manager_client_test.go`

- [ ] **Step 1: Write the failing probe and topology tests**

Add focused tests for:

- `TestWaitHTTPReadyAcceptsReadyz200`
- `TestWaitHTTPReadyRejects503`
- `TestWaitNodeReadyRequiresReadyzAndWKProtoHandshake`
- `TestResolveSlotTopologyRejectsMissingLeader`
- `TestResolveSlotTopologyRejectsPeerMismatch`
- `TestResolveSlotTopologyReturnsLeaderAndFollowers`
- `TestConnectionsContainUID`

Example topology assertion:

```go
func TestResolveSlotTopologyReturnsLeaderAndFollowers(t *testing.T) {
    body := `{
        "slot_id":1,
        "state":{"quorum":"ready","sync":"matched"},
        "runtime":{"leader_id":2,"current_peers":[1,2,3],"has_quorum":true}
    }`

    got, err := parseSlotTopology(1, []uint64{1,2,3}, []byte(body))
    require.NoError(t, err)
    require.Equal(t, uint64(2), got.LeaderNodeID)
    require.Equal(t, []uint64{1,3}, got.FollowerNodeIDs)
}
```

- [ ] **Step 2: Run the focused probe tests and confirm they fail**

Run:

```bash
go test -tags=e2e ./test/e2e/suite -run 'Test(WaitHTTPReadyAcceptsReadyz200|WaitHTTPReadyRejects503|WaitNodeReadyRequiresReadyzAndWKProtoHandshake|ResolveSlotTopologyRejectsMissingLeader|ResolveSlotTopologyRejectsPeerMismatch|ResolveSlotTopologyReturnsLeaderAndFollowers|ConnectionsContainUID)$' -count=1
```

Expected: FAIL because the HTTP probe helpers, topology parser, and connection lookup helpers do not exist yet.

- [ ] **Step 3: Implement the minimal probe layer**

Add a dedicated manager/health helper file instead of stuffing everything into `readiness.go`.

Implement:

- `WaitHTTPReady(ctx, baseURL, path)`
- `WaitNodeReady(ctx, node StartedNode)` composed from `/readyz` plus `WaitWKProtoReady`
- manager DTOs that mirror the JSON shape from `internal/access/manager/slots.go` and `internal/access/manager/connections.go`
- `FetchSlotDetail(ctx, node, slotID)`
- `FetchConnections(ctx, node)`
- `parseSlotTopology(slotID, expectedNodeIDs, body)` or equivalent validation helper

Keep the topology structure explicit:

```go
type SlotTopology struct {
    SlotID          uint32
    LeaderNodeID    uint64
    FollowerNodeIDs []uint64
    RawBody         string
}
```

Do not add sleeps. Use retry loops with ticker polling and context deadlines.

- [ ] **Step 4: Re-run the focused probe tests and confirm they pass**

Run:

```bash
go test -tags=e2e ./test/e2e/suite -run 'Test(WaitHTTPReadyAcceptsReadyz200|WaitHTTPReadyRejects503|WaitNodeReadyRequiresReadyzAndWKProtoHandshake|ResolveSlotTopologyRejectsMissingLeader|ResolveSlotTopologyRejectsPeerMismatch|ResolveSlotTopologyReturnsLeaderAndFollowers|ConnectionsContainUID)$' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the probe slice**

Run:

```bash
git add test/e2e/suite/readiness.go test/e2e/suite/readiness_test.go test/e2e/suite/manager_client.go test/e2e/suite/manager_client_test.go
git commit -m "test: add e2e cluster probes"
```

### Task 3: Add `StartedCluster`, three-node startup, and bounded diagnostics

**Files:**
- Modify: `test/e2e/suite/process.go`
- Modify: `test/e2e/suite/process_test.go`
- Modify: `test/e2e/suite/runtime.go`
- Modify: `test/e2e/suite/runtime_test.go`
- Modify: `test/e2e/suite/manager_client.go`

- [ ] **Step 1: Write the failing cluster-lifecycle and diagnostics tests**

Add focused tests for:

- `TestStartedClusterNodeLookupByID`
- `TestStartedClusterDumpDiagnosticsIncludesReadyzAndSlotBodies`
- `TestNodeProcessDumpDiagnosticsTailsAppLogs`
- `TestStartThreeNodeClusterWritesThreeNodeScopedConfigs`

Use a diagnostics assertion shape like:

```go
func TestStartedClusterDumpDiagnosticsIncludesReadyzAndSlotBodies(t *testing.T) {
    cluster := StartedCluster{
        Nodes: []StartedNode{{Spec: NodeSpec{ID: 1, ConfigPath: "/tmp/node-1/wukongim.conf", StdoutPath: "/tmp/node-1/stdout.log", StderrPath: "/tmp/node-1/stderr.log"}}},
        lastReadyz: map[uint64]HTTPObservation{1: {StatusCode: 503, Body: `{"status":"not_ready"}`}},
        lastSlotBodies: map[uint32]string{1: `{"runtime":{"leader_id":2}}`},
    }

    dump := cluster.DumpDiagnostics()
    require.Contains(t, dump, `{"status":"not_ready"}`)
    require.Contains(t, dump, `leader_id`)
}
```

- [ ] **Step 2: Run the focused cluster-lifecycle tests and confirm they fail**

Run:

```bash
go test -tags=e2e ./test/e2e/suite -run 'Test(StartedClusterNodeLookupByID|StartedClusterDumpDiagnosticsIncludesReadyzAndSlotBodies|NodeProcessDumpDiagnosticsTailsAppLogs|StartThreeNodeClusterWritesThreeNodeScopedConfigs)$' -count=1
```

Expected: FAIL because `StartedCluster`, observation tracking, app-log tail support, and three-node startup do not exist yet.

- [ ] **Step 3: Implement the minimal three-node runtime layer**

Implement the runtime extension in small pieces:

- add `Suite.StartThreeNodeCluster()`
- add `StartedCluster` with `Node`, `MustNode`, `WaitClusterReady`, `ResolveSlotTopology`, and `DumpDiagnostics`
- have `WaitClusterReady` call `WaitNodeReady` for all three nodes
- track last `/readyz` and slot-body observations on the cluster handle for failure reporting
- extend `NodeProcess.DumpDiagnostics()` to include bounded tails of `stdout.log`, `stderr.log`, and `<node-root>/logs/app.log` / `<node-root>/logs/error.log`

Keep the API small, for example:

```go
type StartedCluster struct {
    Nodes          []StartedNode
    lastReadyz     map[uint64]HTTPObservation
    lastSlotBodies map[uint32]string
}

func (c *StartedCluster) WaitClusterReady(ctx context.Context) error {
    for _, node := range c.Nodes {
        if err := WaitNodeReady(ctx, node); err != nil {
            return fmt.Errorf("node %d not ready: %w", node.Spec.ID, err)
        }
    }
    return nil
}
```

Make `StartThreeNodeCluster()` write all three configs before starting processes so the full temp-tree is available for debugging even when node 2 or node 3 fails during boot.

- [ ] **Step 4: Re-run the focused cluster-lifecycle tests and confirm they pass**

Run:

```bash
go test -tags=e2e ./test/e2e/suite -run 'Test(StartedClusterNodeLookupByID|StartedClusterDumpDiagnosticsIncludesReadyzAndSlotBodies|NodeProcessDumpDiagnosticsTailsAppLogs|StartThreeNodeClusterWritesThreeNodeScopedConfigs)$' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the cluster-lifecycle slice**

Run:

```bash
git add test/e2e/suite/process.go test/e2e/suite/process_test.go test/e2e/suite/runtime.go test/e2e/suite/runtime_test.go test/e2e/suite/manager_client.go
git commit -m "test: add three-node e2e runtime"
```

### Task 4: Add the three-node cross-node WKProto e2e scenario and document the new contract

**Files:**
- Modify: `test/e2e/e2e_test.go`
- Modify: `test/e2e/README.md`
- Modify: `test/e2e/suite/manager_client.go`
- Modify: `test/e2e/suite/manager_client_test.go`

- [ ] **Step 1: Write the failing three-node e2e test first**

Add `TestE2E_WKProtoSendMessage_ThreeNodeCluster_CrossNodeClosure` next to the existing single-node baseline.

Use the scenario shape below:

```go
func TestE2E_WKProtoSendMessage_ThreeNodeCluster_CrossNodeClosure(t *testing.T) {
    s := suite.New(t, testBinaryPath)
    cluster := s.StartThreeNodeCluster()

    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()

    require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())

    topology, err := cluster.ResolveSlotTopology(ctx, 1)
    require.NoError(t, err, cluster.DumpDiagnostics())
    require.Len(t, topology.FollowerNodeIDs, 2)

    senderNode := cluster.MustNode(topology.FollowerNodeIDs[0])
    recipientNode := cluster.MustNode(topology.FollowerNodeIDs[1])

    sender, _ := suite.NewWKProtoClient()
    recipient, _ := suite.NewWKProtoClient()
    defer sender.Close()
    defer recipient.Close()

    require.NoError(t, sender.Connect(senderNode.GatewayAddr(), "u1", "u1-device"))
    require.NoError(t, recipient.Connect(recipientNode.GatewayAddr(), "u2", "u2-device"))

    require.True(t, suite.ConnectionsContainUID(t, cluster, senderNode.Spec.ID, "u1"))
    require.True(t, suite.ConnectionsContainUID(t, cluster, recipientNode.Spec.ID, "u2"))

    // send, assert sendack/recv payload/message ids, then RecvAck
}
```

Keep this first three-node scenario strictly on the happy path. Do not add crash or restart logic here.

- [ ] **Step 2: Run the new e2e test alone and confirm it fails**

Run:

```bash
go test -tags=e2e ./test/e2e -run 'TestE2E_WKProtoSendMessage_ThreeNodeCluster_CrossNodeClosure$' -count=1 -timeout 10m
```

Expected: FAIL because the three-node startup and topology helpers do not yet fully support the end-to-end scenario.

- [ ] **Step 3: Implement the minimal scenario glue and docs**

Finish any missing helper behavior needed by the e2e test, then update the README so future workers understand the new contract.

README changes should explicitly mention:

- phase 1 baseline: one-node-cluster send path
- phase 2 baseline: three-node cross-node send path
- no fixed sleeps; readiness goes through `/readyz` plus WKProto handshake
- role discovery goes through `/manager/slots/1`
- failures should be debugged via temp configs, stdout/stderr, and node log tails

- [ ] **Step 4: Run targeted and broad verification**

Run:

```bash
go test -tags=e2e ./test/e2e/suite -count=1
go test -tags=e2e ./test/e2e -run 'TestE2E_WKProtoSendMessage_(SingleNodeCluster|ThreeNodeCluster_CrossNodeClosure)$' -count=1 -timeout 10m
go test -tags=e2e ./test/e2e/... -count=1 -timeout 10m
go test ./... -count=1
```

Expected:

- all tagged suite tests PASS
- both single-node and three-node process e2e tests PASS
- full tagged e2e package PASS
- untagged repository tests PASS

- [ ] **Step 5: Commit the scenario and docs slice**

Run:

```bash
git add test/e2e/e2e_test.go test/e2e/README.md test/e2e/suite/manager_client.go test/e2e/suite/manager_client_test.go
git commit -m "test: add three-node process e2e send path"
```

## Final Verification Checklist

- [ ] `test/e2e` still imports only black-box-safe packages and does not import `internal/app`.
- [ ] The existing `TestE2E_WKProtoSendMessage_SingleNodeCluster` still passes unchanged or with only minimal helper-call adjustments.
- [ ] The new three-node path uses `/readyz` and manager APIs instead of hard-coded sleeps.
- [ ] Every e2e artifact stays under `t.TempDir()`, including `app.log` and `error.log`.
- [ ] Failure output includes config paths, stdout/stderr paths, last `/readyz` body, and last slot-topology body.
- [ ] The plan’s commits can be squashed later if desired, but each slice is independently reviewable.
