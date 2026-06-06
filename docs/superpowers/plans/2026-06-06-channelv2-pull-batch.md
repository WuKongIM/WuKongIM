# ChannelV2 PullBatch Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reduce ChannelV2 cross-node replication RPC amplification by batching follower Pull and PullHint calls per target node while preserving per-channel quorum and fencing semantics.

**Architecture:** Keep reactor ownership unchanged: every channel still has its own fence, future, and completion path. Add batch DTOs at the transport boundary, batch service handlers in clusterv2, and worker-level batch flushers that fan one network RPC result back into ordinary per-channel worker results. Single Pull/PullHint APIs remain as compatibility fallbacks.

**Tech Stack:** Go, ChannelV2 reactor/worker packages, clusterv2 typed RPC codec, existing Prometheus runtime pool metrics, wkbench real-QPS scripts.

---

### Task 1: Transport DTO And Codec

**Files:**
- Modify: `pkg/channelv2/transport/types.go`
- Modify: `pkg/clusterv2/net/ids.go`
- Modify: `pkg/clusterv2/channels/codec.go`
- Test: `pkg/clusterv2/channels/channels_test.go`

- [ ] **Step 1: Write failing codec tests**

Add tests for `PullBatchRequest/PullBatchResponse` and `PullHintBatchRequest/PullHintBatchResponse` round trips. Each response item must carry either a payload or a sentinel error.

- [ ] **Step 2: Run codec tests and verify RED**

Run: `go test ./pkg/clusterv2/channels -run 'Test.*Batch.*Codec' -count=1`
Expected: FAIL because batch DTOs and codec helpers do not exist.

- [ ] **Step 3: Implement DTOs, service ids, and codec helpers**

Add optional `BatchClient` and `BatchServer` interfaces with:

```go
PullBatch(context.Context, ch.NodeID, transport.PullBatchRequest) (transport.PullBatchResponse, error)
PullHintBatch(context.Context, ch.NodeID, transport.PullHintBatchRequest) (transport.PullHintBatchResponse, error)
HandlePullBatch(context.Context, transport.PullBatchRequest) (transport.PullBatchResponse, error)
HandlePullHintBatch(context.Context, transport.PullHintBatchRequest) (transport.PullHintBatchResponse, error)
```

The batch response item count must match the request count. Per-item application errors use the existing compact sentinel encoding.

- [ ] **Step 4: Run codec tests and verify GREEN**

Run: `go test ./pkg/clusterv2/channels -run 'Test.*Batch.*Codec' -count=1`
Expected: PASS.

### Task 2: Batch RPC Client And Server

**Files:**
- Modify: `pkg/clusterv2/channels/transport.go`
- Modify: `pkg/channelv2/service/replication.go`
- Test: `pkg/clusterv2/channels/channels_test.go`
- Test: `pkg/channelv2/service/service_test.go`

- [ ] **Step 1: Write failing RPC handler tests**

Add tests proving `TransportClient.PullBatch` and `PullHintBatch` call the new clusterv2 service IDs, and `RegisterHandlersOn` dispatches batch payloads to a batch-capable server.

- [ ] **Step 2: Run and verify RED**

Run: `go test ./pkg/clusterv2/channels ./pkg/channelv2/service -run 'Test.*Batch' -count=1`
Expected: FAIL because handlers and service methods do not exist.

- [ ] **Step 3: Implement batch RPC path**

Register new `RPCChannelPullBatch` and `RPCChannelPullHintBatch` handlers. In service handlers, submit all items first, then await all futures, so server-side batch handling does not serialize reactor admission.

- [ ] **Step 4: Run and verify GREEN**

Run: `go test ./pkg/clusterv2/channels ./pkg/channelv2/service -run 'Test.*Batch' -count=1`
Expected: PASS.

### Task 3: Worker Batch Tasks

**Files:**
- Modify: `pkg/channelv2/worker/task.go`
- Modify: `pkg/channelv2/worker/result.go`
- Modify: `pkg/channelv2/worker/pools.go`
- Modify: `pkg/channelv2/worker/pool.go`
- Test: `pkg/channelv2/worker/task_test.go`
- Test: `pkg/channelv2/worker/pool_test.go`

- [ ] **Step 1: Write failing worker tests**

Add tests proving batch-capable transports execute one PullBatch call for multiple pull tasks and fan out one result per original fence. Also test fallback to single Pull calls when the transport does not implement the batch interface.

- [ ] **Step 2: Run and verify RED**

Run: `go test ./pkg/channelv2/worker -run 'Test.*Batch' -count=1`
Expected: FAIL because batch task kinds and fan-out are missing.

- [ ] **Step 3: Implement batch tasks and pool fan-out**

Introduce batch task payloads for pull and pull hint. The pool must deliver one completion per item to the existing `CompletionSink`, preserving each original `Fence`, `Kind`, and per-item error.

- [ ] **Step 4: Run and verify GREEN**

Run: `go test ./pkg/channelv2/worker -run 'Test.*Batch' -count=1`
Expected: PASS.

### Task 4: Reactor Submission Batching

**Files:**
- Modify: `pkg/channelv2/reactor/effect.go`
- Modify: `pkg/channelv2/reactor/follower_replication.go`
- Modify: `pkg/channelv2/reactor/lifecycle_runtime.go`
- Modify: `pkg/channelv2/reactor/group.go`
- Test: `pkg/channelv2/reactor/replication_state_test.go`

- [ ] **Step 1: Write failing reactor tests**

Add tests proving multiple follower runtimes targeting the same leader submit through the batch path without changing per-channel result handling.

- [ ] **Step 2: Run and verify RED**

Run: `go test ./pkg/channelv2/reactor -run 'Test.*Batch' -count=1`
Expected: FAIL because reactor submission still calls single RPC tasks.

- [ ] **Step 3: Implement bounded batch submission**

Add worker-level flush controls with conservative defaults: max wait `250us`, max items `64`, max bytes bounded by encoded request estimates. NeedMeta pull requests may use the same batch path, but each item still validates and returns independently.

- [ ] **Step 4: Run and verify GREEN**

Run: `go test ./pkg/channelv2/reactor -run 'Test.*Batch' -count=1`
Expected: PASS.

### Task 5: Metrics, Scripts, And Verification

**Files:**
- Modify: `internalv2/app/observability.go`
- Modify: `scripts/channelv2-metrics-summary.awk`
- Modify: `scripts/runtime-pool-pressure-summary.awk`
- Modify: `scripts/bench-wukongimv2-three-nodes-real-qps.sh`
- Test: `scripts/wukongimv2_three_node_bench_script_test.go`

- [ ] **Step 1: Add metrics and script expectations**

Expose batch task counts/durations under stable low-cardinality labels and ensure the real-QPS summary reports batch RPC totals beside `rpc_pull/s`.

- [ ] **Step 2: Run targeted tests**

Run: `go test ./internalv2/app ./scripts ./pkg/channelv2/... ./pkg/clusterv2/channels -count=1`
Expected: PASS.

- [ ] **Step 3: Run benchmark verification**

Run: `./scripts/bench-wukongimv2-three-nodes-real-qps.sh --qps 14000`
Expected: `send_errors=0`, `p99 <= 400ms`, `actual_ratio >= 0.95`, and no runtime pool `admission_full` in the batch RPC pools.
