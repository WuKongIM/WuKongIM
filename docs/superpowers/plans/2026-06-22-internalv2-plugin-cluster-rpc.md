# Internalv2 Plugin Cluster RPC Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add PDK-compatible `/cluster/config` and `/cluster/channels/belongNode` host RPC support to `internalv2`.

**Architecture:** `internalv2/access/plugin` registers and decodes the cluster host RPC paths, then delegates to `internalv2/usecase/plugin`. The plugin usecase owns legacy proto mapping and depends on narrow `ClusterReader` and `ChannelOwnerReader` ports. `internalv2/infra/cluster` adapts clusterv2 control snapshots and ChannelV2 authority metadata, with `internalv2/app` wiring the adapters when available.

**Tech Stack:** Go, `internal/usecase/plugin/pluginproto`, `pkg/clusterv2/control`, `pkg/channelv2`, `wkrpc`, standard `go test` benchmarks.

---

## Files

- Create: `internalv2/usecase/plugin/host_rpc_cluster.go`
- Create: `internalv2/usecase/plugin/host_rpc_cluster_test.go`
- Create: `internalv2/infra/cluster/plugin_cluster.go`
- Create: `internalv2/infra/cluster/plugin_cluster_test.go`
- Modify: `internalv2/usecase/plugin/types.go`
- Modify: `internalv2/usecase/plugin/app.go`
- Modify: `internalv2/usecase/plugin/mapping.go`
- Modify: `internalv2/usecase/plugin/benchmark_test.go`
- Modify: `internalv2/access/plugin/server.go`
- Modify: `internalv2/access/plugin/handlers_message.go`
- Modify: `internalv2/access/plugin/server_test.go`
- Modify: `internalv2/app/plugin.go`
- Modify: `internalv2/app/plugin_test.go`
- Modify: `internalv2/usecase/plugin/FLOW.md`
- Modify: `internalv2/app/FLOW.md`

### Task 1: Usecase Cluster RPC Mapping

**Files:**
- Create: `internalv2/usecase/plugin/host_rpc_cluster.go`
- Create: `internalv2/usecase/plugin/host_rpc_cluster_test.go`
- Modify: `internalv2/usecase/plugin/types.go`
- Modify: `internalv2/usecase/plugin/app.go`
- Modify: `internalv2/usecase/plugin/mapping.go`

- [ ] **Step 1: Write failing usecase tests**

Add tests for `ClusterConfig` deterministic mapping, missing cluster reader,
`ClusterChannelsBelongNode` owner grouping, missing owner reader, zero owner,
lookup errors, empty requests, and blank channel IDs.

- [ ] **Step 2: Run usecase tests and verify RED**

Run: `go test ./internalv2/usecase/plugin -run 'TestCluster' -count=1`

Expected: fail because cluster reader ports and methods do not exist in v2.

- [ ] **Step 3: Implement minimal usecase support**

Add `ClusterSnapshot`, `ClusterNode`, `ClusterSlot`, `ClusterReader`,
`ChannelOwnerReader`, required errors, options, app fields, mapping helpers,
`App.ClusterConfig`, and `App.ClusterChannelsBelongNode`.

- [ ] **Step 4: Run usecase tests and verify GREEN**

Run: `go test ./internalv2/usecase/plugin -run 'TestCluster' -count=1`

Expected: pass.

### Task 2: Access Host RPC Routes

**Files:**
- Modify: `internalv2/access/plugin/server.go`
- Modify: `internalv2/access/plugin/handlers_message.go`
- Modify: `internalv2/access/plugin/server_test.go`

- [ ] **Step 1: Write failing access tests**

Extend `recordingUsecase`, assert route registration includes both cluster
paths, and add handler/deadline tests for `/cluster/config` and
`/cluster/channels/belongNode`.

- [ ] **Step 2: Run access tests and verify RED**

Run: `go test ./internalv2/access/plugin -run 'TestServerRegisters|TestHandleCluster|TestClusterHostRPC' -count=1`

Expected: fail because cluster paths are not registered in v2.

- [ ] **Step 3: Implement access handler support**

Extend the access usecase interface, register the two paths, dispatch them, and
add handlers using existing body-limit, decode, timeout, and write helpers.

- [ ] **Step 4: Run access tests and verify GREEN**

Run: `go test ./internalv2/access/plugin -run 'TestServerRegisters|TestHandleCluster|TestClusterHostRPC' -count=1`

Expected: pass.

### Task 3: Infra Adapters And App Wiring

**Files:**
- Create: `internalv2/infra/cluster/plugin_cluster.go`
- Create: `internalv2/infra/cluster/plugin_cluster_test.go`
- Modify: `internalv2/app/plugin.go`
- Modify: `internalv2/app/plugin_test.go`

- [ ] **Step 1: Write failing infra and app tests**

Add infra tests for snapshot mapping and owner leader mapping. Add app tests
proving the plugin usecase can serve both host RPCs when the fake cluster
exposes `LocalControlSnapshot` and `ResolveChannelAppendAuthority`.

- [ ] **Step 2: Run tests and verify RED**

Run: `go test ./internalv2/infra/cluster ./internalv2/app -run 'TestPlugin|TestNewWiresPluginUsecaseAsCluster|TestNewWiresPluginUsecaseAsChannelOwner' -count=1`

Expected: fail because adapters and wiring are missing.

- [ ] **Step 3: Implement infra adapters and app wiring**

Add `PluginClusterNode`, `PluginChannelOwnerNode`, `PluginClusterReader`, and
`PluginChannelOwnerReader`. Wire `ClusterReader` and `ChannelOwners` in
`wirePluginSubsystem` when the cluster implements the adapter interfaces.

- [ ] **Step 4: Run tests and verify GREEN**

Run: `go test ./internalv2/infra/cluster ./internalv2/app -run 'TestPlugin|TestNewWiresPluginUsecaseAsCluster|TestNewWiresPluginUsecaseAsChannelOwner' -count=1`

Expected: pass.

### Task 4: Benchmarks And Flow Docs

**Files:**
- Modify: `internalv2/usecase/plugin/benchmark_test.go`
- Modify: `internalv2/access/plugin/server_test.go`
- Modify: `internalv2/usecase/plugin/FLOW.md`
- Modify: `internalv2/app/FLOW.md`

- [ ] **Step 1: Add benchmark tests**

Add benchmark coverage for cluster config mapping, belong-node grouping, and
access handler overhead for both cluster host RPCs.

- [ ] **Step 2: Run benchmarks**

Run: `go test ./internalv2/usecase/plugin ./internalv2/access/plugin -run '^$' -bench 'Benchmark(ClusterConfigFromSnapshot|ClusterChannelsBelongNode|ClusterHostRPCHandlers)' -benchmem`

Expected: benchmark output with allocation counts and no failures.

- [ ] **Step 3: Update FLOW docs**

Document both cluster host RPC flows in `internalv2/usecase/plugin/FLOW.md` and
update `internalv2/app/FLOW.md` plugin subsystem wiring.

- [ ] **Step 4: Run focused package tests**

Run: `go test ./internalv2/usecase/plugin ./internalv2/access/plugin ./internalv2/infra/cluster ./internalv2/app -count=1`

Expected: pass.

### Task 5: Final Verification And Commit

**Files:**
- All modified files.

- [ ] **Step 1: Run focused migration verification**

Run: `go test ./internalv2/usecase/plugin ./internalv2/access/plugin ./internalv2/infra/cluster ./internalv2/app -count=1`

Expected: pass.

- [ ] **Step 2: Run benchmark verification**

Run: `go test ./internalv2/usecase/plugin ./internalv2/access/plugin -run '^$' -bench 'Benchmark(ClusterConfigFromSnapshot|ClusterChannelsBelongNode|ClusterHostRPCHandlers|ChannelMessagesFromPluginReq|ChannelMessagesHostRPCHandler)' -benchmem`

Expected: benchmark output with no failures.

- [ ] **Step 3: Inspect diff**

Run: `git diff --stat && git diff --check`

Expected: changed files match this plan and `git diff --check` exits 0.

- [ ] **Step 4: Commit**

Run: `git add ... && git commit -m "feat: support internalv2 plugin cluster rpc"`

Expected: one commit containing the migration.

## Self Review

- Spec coverage: usecase, access, infra, app wiring, docs, and benchmark
  requirements all have tasks.
- Placeholder scan: no TBD or open-ended implementation steps.
- Type consistency: usecase ports are independent of clusterv2; infra adapters
  own clusterv2 imports.
