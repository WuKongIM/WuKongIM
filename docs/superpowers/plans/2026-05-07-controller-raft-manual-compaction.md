# Controller Raft Manual Compaction Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a manager UI action that manually triggers Controller Raft log compaction across all controller voter nodes and reports per-node results.

**Architecture:** The manager page exposes a control-plane-wide action, while the node selector remains only for status/log inspection. `internal/access/manager` adapts the HTTP request; `internal/usecase/management` fans out over configured controller peers; `pkg/cluster` routes each node-local request locally or through controller RPC; `pkg/controller/raft.Service` performs forced compaction inside its run loop.

**Tech Stack:** Go HTTP handlers with gin, Controller RPC binary codec, etcd raft service loop, React/Vitest manager UI.

---

### Task 1: Controller Raft Service Manual Compaction

**Files:**
- Modify: `pkg/controller/raft/service.go`
- Test: `pkg/controller/raft/service_test.go`

- [ ] Write failing tests for `Service.CompactLog(ctx)` forcing a snapshot below the automatic threshold and returning before/after indexes.
- [ ] Run `go test ./pkg/controller/raft -run 'TestServiceManualCompaction'` and verify it fails because the API is missing.
- [ ] Add `LogCompactionResult`, a run-loop `compactCh`, and `CompactLog(ctx)`.
- [ ] Handle disabled/no-applied/no-new-snapshot as explicit skipped results; record success/failure in status.
- [ ] Re-run the targeted tests.

### Task 2: Cluster Routing and Controller RPC

**Files:**
- Create: `pkg/cluster/controller_raft_compaction.go`
- Modify: `pkg/cluster/codec_control.go`
- Modify: `pkg/cluster/controller_handler.go`
- Test: `pkg/cluster/controller_raft_compaction_test.go`
- Test: `pkg/cluster/codec_control_test.go`

- [ ] Write failing tests for local compaction routing, remote RPC decode/encode, and handler behavior without leader redirect.
- [ ] Run targeted cluster tests and verify missing symbols fail.
- [ ] Add a `controller_raft_compact` RPC kind with empty request and compaction-result response payload.
- [ ] Implement `Cluster.CompactControllerRaftLogOnNode(ctx, nodeID)` local/remote routing.
- [ ] Re-run targeted cluster tests.

### Task 3: Management Usecase and Manager HTTP API

**Files:**
- Create: `internal/usecase/management/controller_raft_compaction.go`
- Modify: `internal/usecase/management/app.go`
- Test: `internal/usecase/management/controller_raft_compaction_test.go`
- Create: `internal/access/manager/controller_raft_compaction.go`
- Modify: `internal/access/manager/server.go`
- Modify: `internal/access/manager/routes.go`
- Test: `internal/access/manager/server_test.go`

- [ ] Write failing usecase tests for fan-out over all controller peer IDs with partial failures preserved.
- [ ] Write failing HTTP tests for `POST /manager/controller-raft/compact`, permission checks, and JSON response shape.
- [ ] Run targeted usecase/manager tests and verify missing APIs fail.
- [ ] Add response DTOs with English comments and route requiring `cluster.controller` write permission.
- [ ] Re-run targeted tests.

### Task 4: Web Manager UI

**Files:**
- Modify: `web/src/lib/manager-api.types.ts`
- Modify: `web/src/lib/manager-api.ts`
- Modify: `web/src/pages/controller/page.tsx`
- Modify: `web/src/pages/controller/page.test.tsx`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`

- [ ] Write failing Vitest coverage for the control-plane-wide compaction button, API call, result display, and selected-node refresh.
- [ ] Run `cd web && bun test src/pages/controller/page.test.tsx` and verify it fails.
- [ ] Add `compactControllerRaftLogs()` and response types.
- [ ] Add button/result panel that does not depend on selected node for compaction scope.
- [ ] Re-run targeted web tests.

### Task 5: Verification

**Files:**
- Verify backend and web targeted suites.

- [ ] Run `go test ./pkg/controller/raft ./pkg/cluster ./internal/usecase/management ./internal/access/manager`.
- [ ] Run `cd web && bun test src/pages/controller/page.test.tsx src/lib/manager-api.test.ts`.
- [ ] Inspect `git diff --stat` and ensure no generated `web/dist` files changed.
