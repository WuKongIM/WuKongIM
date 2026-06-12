# Send Path Performance Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reduce internalv2 SEND hot-path allocations and unbounded writer retention without changing SENDACK correctness.

**Architecture:** Treat SEND payloads and message-scoped UID slices as immutable inside the send path, and keep deep copies only at concrete ownership boundaries such as durable storage adapters and external async queues. Add narrow fast paths and shard-local pressure accounting inside `internalv2/runtime/channelappend`, keeping gateway/access thin and clusterv2 details in `infra/cluster`.

**Tech Stack:** Go, `testing`, existing `internalv2` channelappend runtime tests.

---

### Task 1: Payload Ownership Contract

**Files:**
- Modify: `internalv2/access/gateway/mapper.go`
- Modify: `internalv2/access/gateway/handler.go`
- Modify: `internalv2/contracts/channelappend/types.go`
- Modify: `internalv2/runtime/channelappend/group.go`
- Modify: `internalv2/runtime/channelappend/append.go`
- Modify: `internalv2/runtime/channelappend/state.go`
- Modify: `internalv2/runtime/channelappend/recipient.go`
- Modify: `internalv2/infra/cluster/appender.go`
- Test: `internalv2/access/gateway/handler_test.go`
- Test: `internalv2/runtime/channelappend/append_test.go`
- Test: `internalv2/runtime/channelappend/commit_test.go`
- Test: `internalv2/runtime/channelappend/recipient_test.go`
- Test: `internalv2/infra/cluster/appender_test.go`

- [ ] Add failing tests that single SEND mapping shares frame payload, `SubmitLocal`/append/post-commit do not clone immutable payloads, and `infra/cluster` remains the durable boundary clone.
- [ ] Run targeted tests and confirm the new ownership tests fail against current clone-heavy behavior.
- [ ] Replace hot-path payload clones with shallow command/envelope copies while keeping slice-header copies for item/result containers.
- [ ] Keep deep payload copies at the `channelv2` adapter/storage-facing boundary and when tests intentionally store requests beyond the call.
- [ ] Update comments and FLOW docs to say send-path payloads are immutable and clusterv2/storage owns the durable copy.
- [ ] Run targeted gateway, channelappend, and infra cluster tests.

### Task 2: Router Single-Item Fast Path and Deadline Context

**Files:**
- Modify: `internalv2/runtime/channelappend/router.go`
- Test: `internalv2/runtime/channelappend/router_test.go`
- Test: `internalv2/runtime/channelappend/benchmark_test.go`

- [ ] Add failing tests showing single-item router submission does not create watcher goroutines for a deadline-only item and preserves retry/result semantics.
- [ ] Run the targeted router tests and confirm the new tests fail.
- [ ] Add `SendBatch` fast path for `len(items)==1`, avoiding grouped map/slice work.
- [ ] Make `routerAllItemsContext` return a `context.WithDeadline`/`context.WithCancel` context for the single-item common case without per-item watcher goroutines.
- [ ] Keep the existing multi-item watcher behavior for mixed cancellation/deadline batches.
- [ ] Run targeted router tests and benchmarks compile check.

### Task 3: Shard-Local Admission

**Files:**
- Modify: `internalv2/runtime/channelappend/group.go`
- Modify: `internalv2/runtime/channelappend/shard.go`
- Modify: `internalv2/runtime/channelappend/metrics.go`
- Test: `internalv2/runtime/channelappend/group_test.go`
- Test: `internalv2/runtime/channelappend/pressure_test.go`

- [ ] Add failing tests that one saturated shard does not consume another shard's admission capacity and aggregate pressure still reports total admission depth/capacity.
- [ ] Run targeted group/pressure tests and confirm the new tests fail.
- [ ] Move admission counters to `shard`, acquire/release on the target shard, and keep `AdmissionCapacityPerShard` semantics.
- [ ] Adjust pressure metrics to aggregate shard-local admission counters.
- [ ] Run targeted group/pressure tests.

### Task 4: Idle Writer Reclamation

**Files:**
- Modify: `internalv2/runtime/channelappend/options.go`
- Modify: `internalv2/runtime/channelappend/group.go`
- Modify: `internalv2/runtime/channelappend/shard.go`
- Modify: `internalv2/runtime/channelappend/writer.go`
- Test: `internalv2/runtime/channelappend/group_test.go`
- Test: `internalv2/runtime/channelappend/shard_test.go`

- [ ] Add failing tests that an idle writer past the configured idle age is removed and active/pending writers are retained.
- [ ] Run targeted tests and confirm the new tests fail.
- [ ] Track writer last-idle/last-active timestamps with a conservative default idle retention.
- [ ] Reclaim idle writers during drain/idle scans and after writer deactivation, only when no inbox, pending append, in-flight append, or post-commit backlog exists.
- [ ] Add English comments for the new option and update FLOW docs.
- [ ] Run targeted group/shard tests.

### Task 5: Verification

**Files:**
- Modify: `internalv2/runtime/channelappend/FLOW.md`
- Modify: `internalv2/contracts/channelappend/FLOW.md`
- Modify: `internalv2/access/gateway/FLOW.md`
- Modify: `internalv2/infra/cluster/FLOW.md`

- [ ] Run `gofmt` on changed Go files.
- [ ] Run `go test ./internalv2/access/gateway ./internalv2/runtime/channelappend ./internalv2/infra/cluster`.
- [ ] Run broader `go test ./internalv2/...` if the targeted tests pass in reasonable time.
- [ ] Review `git diff` for unrelated churn and stale clone-oriented comments.
