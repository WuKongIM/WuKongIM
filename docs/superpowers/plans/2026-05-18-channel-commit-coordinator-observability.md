# Channel Commit Coordinator Observability Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add safe observability and tuning hooks for channel durable commit latency without changing the existing local/quorum durability semantics.

**Architecture:** Keep the durable append path synchronous for `CommitModeLocal`, but expose the storage commit coordinator batching knobs and record low-cost sendtrace events around commit queue wait, batch collection, Pebble sync, publish, durable mutex wait, and append store work. Configuration flows from `wukongim.conf`/environment into `internal/app` and then into `pkg/channel/store.Engine` before replicas start.

**Tech Stack:** Go, Pebble, existing `pkg/observability/sendtrace`, existing app configuration loader, existing channel store/replica tests.

---

### Task 1: Add Sendtrace Fields And Stages

**Files:**
- Modify: `pkg/observability/sendtrace/sendtrace.go`
- Test: `pkg/observability/sendtrace/sendtrace_test.go`

- [ ] Add tests proving sendtrace events carry batch request count, record count, and byte count.
- [ ] Add stable stages for `replica.leader.durable_mutex_wait`, `replica.leader.durable_append_store`, `store.commit.queue_wait`, `store.commit.batch_collect`, `store.commit.pebble_sync`, and `store.commit.publish`.
- [ ] Add numeric event fields for `RequestCount`, `RecordCount`, `ByteCount`, and `QueueDepth` with English comments.
- [ ] Run `go test ./pkg/observability/sendtrace -run 'TestRecordCarries' -count=1`.

### Task 2: Parameterize Commit Coordinator

**Files:**
- Modify: `pkg/channel/store/commit.go`
- Modify: `pkg/channel/store/engine.go`
- Test: `pkg/channel/store/commit_test.go`

- [ ] Add failing tests for configured flush window, max requests, max records, and max bytes.
- [ ] Add `CommitCoordinatorConfig` with English field comments and defaults preserving current behavior where limits are unset.
- [ ] Add `OpenWithOptions` / engine options and `ConfigureCommitCoordinator` so app startup can set the coordinator before first use.
- [ ] Update `collectBatch` to stop when configured request/record/byte limits are reached while preserving shutdown behavior.
- [ ] Run `go test ./pkg/channel/store -run 'TestCommitCoordinator' -count=1`.

### Task 3: Emit Commit Coordinator Trace Events

**Files:**
- Modify: `pkg/channel/store/commit.go`
- Modify: `pkg/channel/store/message_log.go`
- Modify: `pkg/channel/store/checkpoint.go`
- Modify: `pkg/channel/store/durability.go`
- Test: `pkg/channel/store/commit_test.go`

- [ ] Extend `commitRequest` with `recordCount` and `byteCount` populated by append/apply/follower/checkpoint paths.
- [ ] Add tests for commit queue wait and Pebble sync trace events containing batch counts and bytes.
- [ ] Emit sendtrace events from coordinator with `ChannelKey`, `RequestCount`, `RecordCount`, `ByteCount`, `QueueDepth`, and result/error data.
- [ ] Run `go test ./pkg/channel/store -run 'TestCommitCoordinator.*Trace|TestCommitCoordinator' -count=1`.

### Task 4: Emit Replica Durable Split Trace Events

**Files:**
- Modify: `pkg/channel/replica/append_pipeline.go`
- Test: `pkg/channel/replica/sendtrace_event_test.go`

- [ ] Add failing test that `runAppendEffect` records durable mutex wait and append store duration.
- [ ] Record `replica.leader.durable_mutex_wait` around `lockDurableMu`.
- [ ] Record `replica.leader.durable_append_store` around `AppendLeaderBatch` with record/byte counts.
- [ ] Run `go test ./pkg/channel/replica -run 'TestLeaderAppend.*Trace|TestAppendDurable' -count=1`.

### Task 5: Wire App Config

**Files:**
- Modify: `internal/app/config.go`
- Modify: `internal/app/build.go`
- Modify: `cmd/wukongim/config.go`
- Modify: `cmd/wukongim/config_test.go`
- Modify: `internal/app/config_test.go`
- Modify: `wukongim.conf.example`

- [ ] Add config tests for default and explicit `WK_CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW`, `WK_CLUSTER_COMMIT_COORDINATOR_MAX_REQUESTS`, `WK_CLUSTER_COMMIT_COORDINATOR_MAX_RECORDS`, and `WK_CLUSTER_COMMIT_COORDINATOR_MAX_BYTES`.
- [ ] Add fields with English comments and validation to `ClusterConfig`.
- [ ] Parse the new `WK_` keys in `cmd/wukongim/config.go` and document them in `wukongim.conf.example`.
- [ ] Configure `app.channelLogDB` after opening and before replica factory/runtime creation.
- [ ] Run targeted config/build tests.

### Task 6: Verify And Benchmark

**Files:**
- No planned production changes.

- [ ] Run `go test ./pkg/observability/sendtrace ./pkg/channel/store ./pkg/channel/replica -count=1`.
- [ ] Run `go test ./internal/app ./cmd/wukongim -run 'Test(Config|LoadConfig|Build|NewBuilds|App)' -count=1` with targeted patterns if the full set is too broad.
- [ ] Run one short local stress/trace command when practical and report QPS/p95 and trace stage summaries.
