# Send Path Main Batch Migration Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Migrate the highest-impact send-path performance work from the dirty `send-path-3000qps-exec` worktree back onto local `main` in three verifiable batches, rerunning the three-node send stress test after each batch.

**Architecture:** Treat the dirty worktree as the source of truth and migrate only cohesive slices that compile and benchmark independently. Batch 1 establishes the dual-watermark / async-checkpoint core, Batch 2 layers runtime transport and backpressure behavior on top, and Batch 3 brings over compatibility handling plus final verification-only test changes.

**Tech Stack:** Go, Pebble, WuKongIM three-node app harness, `go test`, send stress benchmark in `internal/app/send_stress_test.go`

---

### Task 1: Migrate dual-watermark and async checkpoint core

**Files:**
- Modify: `pkg/channel/types.go`
- Modify: `pkg/channel/replica/types.go`
- Modify: `pkg/channel/replica/replica.go`
- Modify: `pkg/channel/replica/progress.go`
- Modify: `pkg/channel/replica/fetch.go`
- Modify: `pkg/channel/replica/recovery.go`
- Modify: `pkg/channel/replica/replication.go`
- Create: `pkg/channel/replica/reconcile.go`
- Modify: `pkg/channel/store/commit.go`
- Modify: `pkg/channel/store/checkpoint.go`
- Modify: `pkg/channel/store/channel_store.go`
- Modify: `pkg/channel/store/engine.go`
- Modify: `pkg/channel/runtime/channel.go`
- Test: `pkg/channel/replica/progress_test.go`
- Test: `pkg/channel/replica/recovery_test.go`
- Test: `pkg/channel/replica/lifecycle_test.go`
- Test: `pkg/channel/replica/testenv_test.go`
- Test: `pkg/channel/store/apply_fetch_test.go`
- Test: `pkg/channel/store/engine_test.go`

- [ ] **Step 1: Copy the Batch 1 file set from the dirty worktree into `main`**

Run: `git -C .worktrees/send-path-3000qps-exec diff --name-only main -- <files...>`
Expected: only the Batch 1 files above are selected.

- [ ] **Step 2: Run targeted unit tests for the new dual-watermark behavior**

Run: `go test ./pkg/channel/replica ./pkg/channel/store -run 'Test(NewReplica|Status|BecomeLeader|CommitCommitted|DefaultPebbleOptions)' -count=1`
Expected: PASS.

- [ ] **Step 3: Run a compile-only sweep for impacted packages**

Run: `go test ./pkg/channel/... ./internal/app -run '^$' -count=1`
Expected: PASS.

- [ ] **Step 4: Run the three-node send stress test on `main`**

Run: `WK_SEND_STRESS=1 go test ./internal/app -run '^TestSendStressThreeNode$' -count=1 -v`
Expected: PASS and QPS materially above the current `main` baseline (`1653.84`).

- [ ] **Step 5: Commit Batch 1**

Run:
```bash
git add pkg/channel/types.go \
  pkg/channel/replica/types.go pkg/channel/replica/replica.go pkg/channel/replica/progress.go \
  pkg/channel/replica/fetch.go pkg/channel/replica/recovery.go pkg/channel/replica/replication.go \
  pkg/channel/replica/reconcile.go pkg/channel/store/commit.go pkg/channel/store/checkpoint.go \
  pkg/channel/store/channel_store.go pkg/channel/store/engine.go pkg/channel/runtime/channel.go \
  pkg/channel/replica/progress_test.go pkg/channel/replica/recovery_test.go \
  pkg/channel/replica/lifecycle_test.go pkg/channel/replica/testenv_test.go \
  pkg/channel/store/apply_fetch_test.go pkg/channel/store/engine_test.go

git commit -m "feat: add dual-watermark async checkpoint core"
```
Expected: one commit on `main`.

### Task 2: Migrate runtime transport, reconcile probe, and deferred batching behavior

**Files:**
- Modify: `pkg/channel/runtime/runtime.go`
- Modify: `pkg/channel/runtime/backpressure.go`
- Modify: `pkg/channel/runtime/replicator.go`
- Modify: `pkg/channel/runtime/testenv_test.go`
- Modify: `pkg/channel/runtime/session_test.go`
- Modify: `pkg/channel/transport/transport.go`
- Modify: `pkg/channel/transport/codec.go`
- Modify: `pkg/channel/handler/fetch.go`
- Modify: `pkg/channel/replica/fetch_test.go`
- Modify: `pkg/channel/replica/append.go`
- Modify: `pkg/channel/replica/append_test.go`
- Modify: `pkg/channel/replica/meta.go`
- Modify: `pkg/channel/replica/meta_test.go`
- Modify: `pkg/channel/replica/progress_test.go`
- Modify: `pkg/channel/replica/recovery_test.go`
- Modify: `pkg/channel/replica/testenv_test.go`
- Modify: `pkg/channel/runtime/pressure_testutil_test.go`
- Modify: `pkg/channel/transport/progress_ack_rpc_test.go`

- [ ] **Step 1: Copy the Batch 2 runtime/transport file set from the dirty worktree into `main`**
- [ ] **Step 2: Run targeted runtime + transport tests**

Run: `go test ./pkg/channel/runtime ./pkg/channel/transport ./pkg/channel/replica -run 'Test(Session|Runtime|Fetch|Append|Meta|ProgressAck)' -count=1`
Expected: PASS.

- [ ] **Step 3: Re-run the three-node send stress test**

Run: `WK_SEND_STRESS=1 go test ./internal/app -run '^TestSendStressThreeNode$' -count=1 -v`
Expected: PASS and QPS materially above the Batch 1 result.

- [ ] **Step 4: Commit Batch 2**

Run: `git add <Batch 2 files> && git commit -m "feat: optimize send path runtime transport batching"`
Expected: one commit on `main`.

### Task 3: Migrate compatibility handling, app harness alignment, and final verification changes

**Files:**
- Modify: `internal/access/node/conversation_facts_rpc.go`
- Modify: `internal/access/node/conversation_facts_rpc_test.go`
- Modify: `internal/app/build.go`
- Modify: `internal/app/build_test.go`
- Modify: `internal/app/comm_test.go`
- Modify: `internal/app/multinode_integration_test.go`
- Modify: `pkg/channel/errors.go`
- Modify: `pkg/channel/api_test.go`
- Modify: `pkg/channel/handler/apply_test.go`
- Modify: `pkg/channel/handler/fetch_test.go`
- Modify: `pkg/channel/handler/meta.go`
- Modify: `pkg/channel/handler/meta_test.go`
- Modify: `pkg/channel/handler/seq_read.go`
- Modify: `pkg/channel/handler/seq_read_test.go`
- Modify: `pkg/channel/store/apply_fetch_test.go`
- Modify: `internal/FLOW.md`

- [ ] **Step 1: Copy only the remaining compatibility and test-only files that still make sense after Batches 1-2**
- [ ] **Step 2: Run targeted app and handler tests**

Run: `go test ./internal/access/node ./internal/app ./pkg/channel/handler ./pkg/channel -run 'Test(Conversation|Build|Multinode|API|Fetch|SeqRead|Meta)' -count=1`
Expected: PASS.

- [ ] **Step 3: Run the final send stress benchmark plus a compile sweep**

Run: `WK_SEND_STRESS=1 go test ./internal/app -run '^TestSendStressThreeNode$' -count=1 -v && go test ./... -run '^$' -count=1`
Expected: PASS and final metrics recorded.

- [ ] **Step 4: Commit Batch 3**

Run: `git add <Batch 3 files> && git commit -m "test: align send path compatibility and verification"`
Expected: final batch commit on `main`.
