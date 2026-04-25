# Cluster Idle CPU Optimization Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reduce steady-state cluster idle CPU by removing per-slot `get_task` reconciliation reads and controller leader self-RPC for local controller reads.

**Architecture:** Keep controller read semantics unchanged while tightening the hot path. Reconciler switches from per-slot task misses to one bulk task snapshot plus execution-time confirmation, and controller client short-circuits local leader reads directly into the local controller handler instead of round-tripping through transport.

**Tech Stack:** Go, `testing`, `pkg/cluster`, `pkg/transport`, controller metadata snapshots, Markdown specs/plans.

---

## References

- Spec: `docs/superpowers/specs/2026-04-22-cluster-idle-cpu-optimization-design.md`
- Reconciler: `pkg/cluster/reconciler.go`, `pkg/cluster/agent.go`
- Controller read path: `pkg/cluster/controller_client.go`, `pkg/cluster/controller_handler.go`, `pkg/cluster/cluster.go`
- Existing tests: `pkg/cluster/reconciler_test.go`, `pkg/cluster/cluster_test.go`
- Follow `@superpowers:test-driven-development` for the behavior changes.
- Run `@superpowers:verification-before-completion` before claiming success.

## File Structure

- Modify: `pkg/cluster/agent.go` — add bulk task listing helper alongside existing controller read helpers.
- Modify: `pkg/cluster/reconciler.go` — replace per-slot task loading with bulk task loading plus pending-report merge.
- Modify: `pkg/cluster/controller_client.go` — add local controller fast path inside controller RPC call flow.
- Modify: `pkg/cluster/reconciler_test.go` — cover steady-state task loading behavior.
- Modify: `pkg/cluster/cluster_test.go` — cover local controller fast path behavior.
- Modify: `pkg/cluster/FLOW.md` if the flow text becomes inconsistent.

### Task 1: Replace per-slot reconciliation task misses with bulk task reads

**Files:**
- Modify: `pkg/cluster/agent.go`
- Modify: `pkg/cluster/reconciler.go`
- Modify: `pkg/cluster/reconciler_test.go`

- [ ] **Step 1: Write the failing reconciler test**

Add a focused test proving that when no tasks exist, `Tick()` uses one `ListTasks()` read and does not issue per-slot `GetTask()` calls.

- [ ] **Step 2: Run the focused reconciler test to verify it fails**

Run: `go test ./pkg/cluster -run 'TestReconcilerTickLoadsTasksViaListTasksBeforePerSlotConfirmation' -count=1`
Expected: FAIL because the current reconciler still loads tasks via `GetTask()`.

- [ ] **Step 3: Implement the minimal task-loading change**

- Add `slotAgent.listTasks(ctx)` with the same snapshot / controller client / fallback pattern as the other read helpers.
- Update `reconciler.loadTasks()` to build the slot map from one bulk task read plus pending task reports.
- Keep execution-time fresh confirmation logic intact.

- [ ] **Step 4: Re-run the focused reconciler test to verify it passes**

Run: `go test ./pkg/cluster -run 'TestReconcilerTickLoadsTasksViaListTasksBeforePerSlotConfirmation' -count=1`
Expected: PASS.

### Task 2: Add controller local fast path to avoid self-RPC

**Files:**
- Modify: `pkg/cluster/controller_client.go`
- Modify: `pkg/cluster/cluster_test.go`

- [ ] **Step 1: Write the failing local-fast-path test**

Add a focused test proving a local controller leader read succeeds even when transport RPC would fail, demonstrating the read no longer depends on self-RPC.

- [ ] **Step 2: Run the focused local-fast-path test to verify it fails**

Run: `go test ./pkg/cluster -run 'TestListTasksUsesLocalControllerFastPathWithoutSelfRPC' -count=1`
Expected: FAIL because the current controller client still routes local leader reads through `RPCService()`.

- [ ] **Step 3: Implement the minimal local fast path**

Inside `controllerClient.call()`:
- detect when the selected target is the local node
- route the encoded request body directly to `cluster.handleControllerRPC(ctx, body)`
- keep response decoding and metrics behavior unchanged
- only use `cluster.RPCService()` for remote targets

- [ ] **Step 4: Re-run the focused local-fast-path test to verify it passes**

Run: `go test ./pkg/cluster -run 'TestListTasksUsesLocalControllerFastPathWithoutSelfRPC' -count=1`
Expected: PASS.

### Task 3: Run focused verification for the cluster package

**Files:**
- Modify: `pkg/cluster/FLOW.md` only if the updated code path no longer matches the documented flow.

- [ ] **Step 1: Run the targeted optimization tests together**

Run: `go test ./pkg/cluster -run 'Test(ReconcilerTickLoadsTasksViaListTasksBeforePerSlotConfirmation|ListTasksUsesLocalControllerFastPathWithoutSelfRPC)' -count=1`
Expected: PASS.

- [ ] **Step 2: Run the full package verification**

Run: `go test ./pkg/cluster -count=1`
Expected: PASS.

- [ ] **Step 3: Update `pkg/cluster/FLOW.md` if needed and rerun `go test ./pkg/cluster -count=1`**

Only touch the flow doc if the documented read/reconcile flow is now inaccurate.
