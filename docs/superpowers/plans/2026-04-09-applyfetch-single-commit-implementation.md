# ApplyFetch Single Commit Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** Reduce follower-side send-path latency by collapsing append + checkpoint/idempotency persistence in `ApplyFetch` into a single durable Pebble commit on the hot append-only path.

**Architecture:** Keep ISR semantics unchanged in `pkg/replication/isr`, but add an optional apply-fetch persistence hook that production `pkg/storage/channellog` can satisfy with one DB batch. The replica falls back to the existing generic path when the optimization is unavailable or when truncation still requires the old flow.

**Tech Stack:** Go, Pebble, WuKongIM ISR runtime, channellog storage bridges, Go tests.

---

### Task 1: Add a failing ISR regression test for single-commit apply-fetch

**Files:**
- Modify: `pkg/replication/isr/replication_test.go`
- Modify: `pkg/replication/isr/testenv_test.go`

- [x] **Step 1: Write the failing test**

Add a focused test proving append-only `ApplyFetch` with HW advance uses one durable persistence hook instead of separate `log.Append` + `checkpoint.Store` sync paths.

- [x] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/replication/isr -run '^TestApplyFetchUsesAtomicStoreOnAppendOnlyHotPath$' -count=1`
Expected: FAIL because the new hook is not wired.

- [x] **Step 3: Write minimal test scaffolding**

Extend the ISR test doubles with an optional atomic apply store recorder so the replica test can observe whether the fast path is used.

- [x] **Step 4: Run test to verify the failure is still the intended one**

Run: `go test ./pkg/replication/isr -run '^TestApplyFetchUsesAtomicStoreOnAppendOnlyHotPath$' -count=1`
Expected: FAIL on missing production behavior, not on broken test setup.

### Task 2: Implement the optional apply-fetch persistence hook

**Files:**
- Modify: `pkg/replication/isr/types.go`
- Modify: `pkg/replication/isr/replica.go`
- Modify: `pkg/replication/isr/replication.go`

- [x] **Step 1: Add the interface to ISR config/types**

Introduce an optional apply-fetch persistence interface and config field that can atomically persist appended records and an optional checkpoint update, while keeping the existing fallback path intact.

- [x] **Step 2: Wire the replica to the new hook**

Use the hook only on the append-only hot path; preserve current generic behavior for truncation or when no optimized store is available.

- [x] **Step 3: Run the focused ISR test**

Run: `go test ./pkg/replication/isr -run '^TestApplyFetchUsesAtomicStoreOnAppendOnlyHotPath$' -count=1`
Expected: PASS.

### Task 3: Implement the production channellog single-commit path

**Files:**
- Modify: `pkg/storage/channellog/apply.go`
- Modify: `pkg/storage/channellog/replica.go`
- Modify: `pkg/storage/channellog/log_store.go`
- Modify: `pkg/storage/channellog/storage_integration_test.go`
- Modify: `pkg/storage/channellog/isr_bridge_test.go`

- [x] **Step 1: Write/extend a failing storage integration test**

Add a regression test showing follower `ApplyFetch` on a real `Store` persists the fetched log records and checkpoint/idempotency updates with one durable DB batch.

- [x] **Step 2: Run the storage test to verify it fails**

Run: `go test ./pkg/storage/channellog -run '^TestStoreReplicaApplyFetchUsesSingleDurableCommit$' -count=1`
Expected: FAIL before the implementation lands.

- [x] **Step 3: Implement the single-commit batch write**

Teach the checkpoint bridge to materialize committed idempotency entries from existing/new records and commit log append + optional checkpoint/idempotency updates in one Pebble batch when backed by the real `stateStore`.

- [x] **Step 4: Run focused storage tests**

Run: `go test ./pkg/storage/channellog -run '^(TestStoreReplicaApplyFetchUsesSingleDurableCommit|TestStoreBridgesReplicaApplyFetchAndRecoverFollowerState|TestClusterAppendDoesNotFailAfterAtomicCheckpointIdempotencyCommit)$' -count=1`
Expected: PASS.

### Task 4: Run regression verification

**Files:**
- Modify: `docs/superpowers/plans/2026-04-09-applyfetch-single-commit-implementation.md`

- [x] **Step 1: Run targeted package verification**

Run: `go test ./pkg/replication/isr ./pkg/storage/channellog -count=1`
Expected: PASS.

- [x] **Step 2: Run app-level regression slice**

Run: `go test ./internal/app ./pkg/replication/isr ./pkg/replication/isrnode ./pkg/replication/isrnodetransport -count=1`
Expected: PASS.

- [x] **Step 3: Re-run fresh send stress**

Run: `WK_SEND_STRESS=1 WK_SEND_STRESS_DURATION=30s WK_SEND_STRESS_MESSAGES_PER_WORKER=100000 go test ./internal/app -run '^TestSendStressThreeNode$' -count=1 -v`
Expected: No failures and follower-latency/QPS no worse than the current baseline, ideally improved.
