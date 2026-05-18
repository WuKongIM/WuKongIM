# Send Stress QPS Recovery Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restore `TestSendStressThreeNode` acceptance-preserving throughput to about 2000 QPS, at least half of the pinned 4210 QPS baseline.

**Architecture:** Keep cluster/durable semantics intact and tune only the leader durable batching path proven by trace data to dominate latency. Iterate with one hypothesis at a time: add/adjust focused tests, make a minimal change, run store benchmarks and send-stress verification, then commit if the change improves or preserves correctness.

**Tech Stack:** Go, Pebble, `pkg/channel/store`, `pkg/channel/replica`, integration test `internal/app/TestSendStressThreeNode`.

---

### Task 1: Remove redundant same-channel wait when cross-channel coordinator already batches

**Files:**
- Modify: `pkg/channel/store/message_log.go`
- Test: `pkg/channel/store/message_log_commit_test.go`
- Benchmark: `pkg/channel/store/message_log_benchmark_test.go`

- [ ] Write a failing test/benchmark assertion that same-channel appends do not wait for the full coordinator flush window before entering the commit coordinator.
- [ ] Run the focused store test/benchmark and confirm the current wait behavior is visible.
- [ ] Change same-channel append batching so it only coalesces immediately available same-channel waiters and does not add a second timer on top of `commitCoordinator.collectBatch`.
- [ ] Run `go test ./pkg/channel/store -run 'SameChannel|Commit|Append' -count=1`.
- [ ] Run `go test ./pkg/channel/store -run '^$' -bench '^BenchmarkChannelStoreAppend/(records=1|records=32|records=256)$|^BenchmarkChannelStoreAppendParallelChannels/channels=64$' -benchmem -benchtime=3s -count=1`.
- [ ] Run local send stress with trace: `WK_SEND_STRESS=1 WK_SEND_TRACE=1 WK_SEND_STRESS_COMMIT_MODE=local WK_CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW=200us go test -tags=integration ./internal/app -run '^TestSendStressThreeNode$' -count=1 -timeout=240s -v`.
- [ ] If QPS improves and tests pass, commit.

### Task 2: Tune coordinator defaults to favor throughput without adding queue latency

**Files:**
- Modify: `internal/app/comm_test.go`
- Modify: `internal/app/config.go`
- Modify: `wukongim.conf.example`
- Test: `internal/app/config_test.go`, `internal/app/send_stress_test.go`

- [ ] Write failing config/preset tests for a lower acceptance flush window if Task 1 is insufficient.
- [ ] Set acceptance/default coordinator flush window to the best measured value.
- [ ] Run focused config tests and local send stress.
- [ ] Commit only if the change materially improves QPS.

### Task 3: Investigate remaining durable bottleneck if QPS < 2000

**Files:**
- Read/modify as needed: `pkg/channel/store/commit.go`, `pkg/channel/store/message_table.go`, `pkg/channel/replica/append_pipeline.go`

- [ ] Add the smallest missing phase metric needed to distinguish build time, sync time, and durable mutex wait.
- [ ] Run store benchmarks and local send stress with trace.
- [ ] Form one new hypothesis from data.
- [ ] Write a focused failing test before production changes.
- [ ] Implement one minimal fix, verify, then commit if it improves.

### Task 4: Final acceptance verification

**Files:**
- No production changes unless a bug is found.

- [ ] Run local commit send stress and confirm QPS is near or above 2000.
- [ ] Run quorum send stress and capture stage summary.
- [ ] Run relevant unit tests for changed packages.
- [ ] Update `docs/development/PROJECT_KNOWLEDGE.md` if new durable-path knowledge is discovered.
- [ ] Final commit with the verified tuning changes.
