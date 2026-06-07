# Message DB Commit Shards Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add configurable sharded message DB commit coordinators to reduce commit request tail queueing at 16k real QPS.

**Architecture:** Introduce a small router in `pkg/db/internal/commit` that owns one or more existing coordinators and routes requests by partition hash. Wire the shard count through `pkg/db/message`, `pkg/channelv2/store`, clusterv2 config, scripts, and example config while preserving default one-shard behavior.

**Tech Stack:** Go, Pebble-backed message DB, existing Prometheus metrics, shell bench scripts.

---

### Task 1: Commit Coordinator Router

**Files:**
- Modify: `pkg/db/internal/commit/coordinator.go`
- Test: `pkg/db/internal/commit/coordinator_test.go`

- [x] Add failing tests for routing same partition to one shard and different partitions to different shards.
- [x] Implement `Shards` in `commit.Config` and route `Submit` through a shard set when `Shards > 1`.
- [x] Aggregate queue depth across shard observers.
- [x] Verify with `go test ./pkg/db/internal/commit -count=1`.

### Task 2: Message DB Wiring

**Files:**
- Modify: `pkg/db/message/compat.go`
- Test: `pkg/db/message/compat_test.go`

- [x] Add failing tests for `CommitCoordinatorConfig.Shards` effective config and batch partition distribution.
- [x] Pass `Shards` through to `commit.Config`.
- [x] Use a representative channel key for batched request partition.
- [x] Verify with `go test ./pkg/db/message -count=1`.

### Task 3: Runtime Config Wiring

**Files:**
- Modify: `pkg/channelv2/store/channel_adapter.go`
- Modify: `pkg/clusterv2/config.go`
- Modify: `pkg/clusterv2/channels/service.go`
- Modify: `pkg/clusterv2/node_defaults.go`
- Modify: `internalv2/app/config.go`
- Modify: `wukongim.conf.example`
- Test: relevant package config tests

- [x] Add `CommitShards`/`CommitCoordinatorShards` fields with English comments.
- [x] Wire `WK_CLUSTER_COMMIT_COORDINATOR_SHARDS` from config to the message DB factory.
- [x] Verify targeted config tests.

### Task 4: Bench Script Defaults

**Files:**
- Modify: `scripts/bench-wukongimv2-three-nodes-real-qps.sh`
- Modify: `scripts/bench-wukongimv2-three-nodes-1000ch.sh`
- Modify: `scripts/wukongimv2_three_node_bench_script_test.go`

- [x] Add env propagation and metadata for `WK_CLUSTER_COMMIT_COORDINATOR_SHARDS`.
- [x] Keep the production default unchanged, but set bench-tuned defaults where the script already owns high-QPS settings.
- [x] Verify with `go test ./scripts -count=1`.

### Task 5: Performance Verification

**Files:**
- No source changes unless evidence requires a targeted adjustment.

- [x] Run targeted unit tests.
- [x] Run `./scripts/bench-wukongimv2-three-nodes-real-qps.sh --qps 16000` multiple times with the selected shard setting.
- [x] Compare against prior baseline and report pass rate, actual ratio, p99, DB request p99, and pool pressure.

Result: `WK_CLUSTER_COMMIT_COORDINATOR_SHARDS=2` and `4` were not selected as defaults. Both reduced queue fill but increased physical commit count and commit-request p99 on the local three-node 16k benchmark. The current bench default keeps one commit coordinator and caps ChannelV2 store append/apply workers at `64`.
