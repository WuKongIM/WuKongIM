# Internalv2 Slot Raft Compaction Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add manual node-local Slot Raft log compaction to the `internalv2` manager API so the existing web Slot logs page works against `wukongimv2`.

**Architecture:** Mirror the legacy manager contract while keeping internalv2 layering: HTTP adapter -> management usecase -> infra cluster adapter -> local clusterv2 facade or node RPC -> target node local Slot runtime. Use a dedicated manager Slot Raft RPC service because compaction is an operator write, while the existing manager log RPC is read-only.

**Tech Stack:** Go, Gin, clusterv2 typed node RPC, Slot Multi-Raft, Vitest/React Testing Library for existing web contract tests.

---

### Task 1: Management Usecase Contract

**Files:**
- Create: `internalv2/usecase/management/slot_raft_compaction.go`
- Modify: `internalv2/usecase/management/nodes.go`
- Test: `internalv2/usecase/management/slot_raft_compaction_test.go`

- [x] Write failing tests for `CompactSlotRaftLog` delegating to a `SlotRaftOperator`, preserving generated time, target node/slot IDs, success counts, and per-item errors.
- [x] Run `go test ./internalv2/usecase/management -run 'TestSlotRaft' -count=1` and confirm the new API is missing.
- [x] Add `SlotRaftOperator`, unavailable error, result DTOs, and `App.CompactSlotRaftLog`.
- [x] Run the same package test and confirm it passes.

### Task 2: Clusterv2 Local Facade

**Files:**
- Modify: `pkg/clusterv2/node_logs.go`
- Test: `pkg/clusterv2/node_logs_test.go`

- [x] Write a failing test for `LocalCompactSlotRaftLog` using a fake Slot compactor/runtime seam or the existing default runtime fixture.
- [x] Run `go test ./pkg/clusterv2 -run TestLocalSlot -count=1` and confirm the method is missing.
- [x] Add `SlotRaftCompactionResult` and `LocalCompactSlotRaftLog` backed by `defaultSlotRuntime.CompactLog`.
- [x] Run the clusterv2 targeted test.

### Task 3: Infra Local/Remote Operator

**Files:**
- Create: `internalv2/infra/cluster/management_slot_raft.go`
- Test: `internalv2/infra/cluster/management_slot_raft_test.go`

- [x] Write failing tests for local compaction using `LocalCompactSlotRaftLog` and remote compaction through a node RPC client.
- [x] Run `go test ./internalv2/infra/cluster -run 'TestManagementSlotRaft' -count=1` and confirm the operator is missing.
- [x] Implement `ManagementSlotRaftOperator` and mapping from clusterv2 results to management results.
- [x] Run the infra targeted test.

### Task 4: Node RPC

**Files:**
- Modify: `pkg/clusterv2/net/ids.go`
- Modify: `pkg/clusterv2/net/ids_test.go`
- Create: `internalv2/access/node/manager_slot_raft_codec.go`
- Create: `internalv2/access/node/manager_slot_raft_rpc.go`
- Modify: `internalv2/access/node/presence_rpc.go`
- Test: `internalv2/access/node/manager_slot_raft_rpc_test.go`

- [x] Write failing codec and RPC tests for compacting node 2 slot 9.
- [x] Run `go test ./internalv2/access/node ./pkg/clusterv2/net -run 'TestManagerSlotRaft|TestRPCServiceIDs' -count=1` and confirm missing service/code.
- [x] Add `RPCManagerSlotRaft`, alias, adapter option/interface, codec, handler, and client method.
- [x] Run the targeted tests.

### Task 5: Manager HTTP Route and App Wiring

**Files:**
- Modify: `internalv2/access/manager/server.go`
- Create: `internalv2/access/manager/slot_raft_compaction.go`
- Modify: `internalv2/access/manager/server_test.go`
- Modify: `internalv2/app/wiring.go`
- Modify: `internalv2/app/app_test.go`
- Modify: `internalv2/access/manager/FLOW.md`
- Modify: `internalv2/usecase/management/FLOW.md`
- Modify: `internalv2/app/FLOW.md`
- Modify: `internalv2/infra/cluster/FLOW.md`
- Modify: `pkg/clusterv2/FLOW.md`

- [x] Write failing internalv2 manager HTTP tests for success and insufficient permission.
- [x] Write failing app wiring tests for registering the Slot Raft RPC handler and serving local compaction through manager.
- [x] Run the targeted internalv2 tests and confirm missing route/wiring.
- [x] Add management interface method, route with `cluster.slot:w`, DTO mapping, app RPC registration, and management option wiring.
- [x] Update affected `FLOW.md` files to describe the new route and operator path.
- [x] Run targeted internalv2 tests.

### Task 6: Verification

**Files:**
- Existing web contract files under `web/src/pages/slot-logs` and `web/src/lib`.

- [x] Run `go test ./internalv2/usecase/management ./pkg/clusterv2 ./internalv2/access/manager ./internalv2/access/node ./internalv2/infra/cluster ./pkg/clusterv2/net ./internalv2/app -count=1`.
- [x] Run `cd web && yarn test src/pages/slot-logs/page.test.tsx src/lib/manager-api.test.ts`.
- [x] Run `gofmt` on touched Go files and repeat failing/targeted tests.
