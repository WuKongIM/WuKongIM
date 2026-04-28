# Distributed Risk Remediation Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove confirmed distributed-safety risks around HashSlot migration, Raft log growth, leader routing, internal transport security, strict reads, channel runtime metadata CAS, and channel long-poll fencing.

**Architecture:** Deliver this as independent, reviewable PRs. Start with low-risk guardrails and correctness bug fixes, then implement protocol-level changes behind tests and explicit rollout gates. Preserve the project rule that a single-node deployment is a single-node cluster; do not introduce standalone bypass paths.

**Tech Stack:** Go, etcd/raft RawNode, Pebble-backed raftlog/meta stores, WuKongIM cluster/slot/channel packages, `go test` unit suites plus selected integration tests.

---

## Scope Split

This plan intentionally decomposes the remediation into separate tracks. Do **not** implement all tracks in one PR.

1. Immediate guardrails: `ProposeWithHashSlot` not-leader mapping and HashSlot migration feature gate.
2. HashSlot migration protocol hardening: durable outbox, persisted target dedup, source fence, safe cutover.
3. Raft snapshot and compaction: controller + managed Slot groups.
4. Transport security and inbound resource limits.
5. Linearizable strict reads with ReadIndex / read fence.
6. ChannelRuntimeMeta compare-and-upsert.
7. Channel long-poll leader-epoch / session fencing.
8. Repository hygiene and slow-test cleanup.

## Global Rules

- Follow `AGENTS.md`: no standalone semantics; use "single-node cluster" in docs/tests where deployment shape is mentioned.
- Use TDD for every behavior change: write failing test, verify failure, implement minimal code, verify pass.
- Keep unit tests fast; if a test needs real wall-clock multi-node behavior, put it behind `integration` tag.
- If config changes, update `wukongim.conf.example` and add detailed English comments for config fields.
- If package behavior diverges from a `FLOW.md`, update that `FLOW.md` in the same PR.

---

## Track 1: Immediate Guardrails

### Task 1.1: Map local multiraft not-leader errors in ProposeWithHashSlot

**Files:**
- Modify: `pkg/cluster/cluster.go`
- Test: `pkg/cluster/cluster_test.go`

- [ ] **Step 1: Add failing test for local enqueue not-leader retry**

Add a unit test near existing propose/forward tests. The test should build a fake runtime/router where the first local `runtime.Propose` returns `multiraft.ErrNotLeader`, the router is refreshed or consulted again, and the second attempt forwards to a remote leader successfully.

Expected behavior:

```go
require.NoError(t, c.ProposeWithHashSlot(ctx, 1, 3, []byte("cmd")))
require.GreaterOrEqual(t, attempts, 2)
```

- [ ] **Step 2: Run the focused test and verify RED**

Run:

```bash
go test ./pkg/cluster -run 'Test.*ProposeWithHashSlot.*NotLeader' -count=1
```

Expected: FAIL because `multiraft.ErrNotLeader` is returned directly and is not retryable.

- [ ] **Step 3: Add minimal error normalization**

In `pkg/cluster/cluster.go`, add a small helper such as:

```go
func normalizeProposeError(err error) error {
	if errors.Is(err, multiraft.ErrNotLeader) {
		return ErrNotLeader
	}
	return err
}
```

Use it in `ProposeWithHashSlot` for both `c.runtime.Propose(...)` and `future.Wait(...)` error returns. Keep `ProposeLocalWithHashSlot` behavior unchanged except for reusing the helper if it simplifies code.

- [ ] **Step 4: Verify GREEN**

Run:

```bash
go test ./pkg/cluster -run 'Test.*ProposeWithHashSlot.*NotLeader' -count=1
```

Expected: PASS.

- [ ] **Step 5: Run related cluster unit tests**

Run:

```bash
go test ./pkg/cluster -run 'Test.*Propose|Test.*Forward|Test.*Retry' -count=1
```

Expected: PASS.

### Task 1.2: Add a default-off HashSlot migration feature gate

**Files:**
- Modify: `pkg/cluster/config.go`
- Modify: `pkg/cluster/hashslot_migration.go`
- Modify: `internal/app/config` files that map cluster config, if present after search
- Modify: `wukongim.conf.example`, if the gate is exposed through user config
- Test: `pkg/cluster/cluster_test.go`
- Docs: `docs/development/PROJECT_KNOWLEDGE.md` if this is a durable project rule

- [ ] **Step 1: Locate config mapping**

Run:

```bash
rg -n "HashSlot|Migration|WK_CLUSTER|Cluster" internal/app cmd wukongim.conf.example pkg/cluster
```

Record the exact files that translate `WK_` config into `cluster.Config`.

- [ ] **Step 2: Add failing test for migration disabled by default**

Add a test that constructs a `Cluster` with a router table where `StartHashSlotMigration(ctx, hashSlot, target)` would otherwise be valid, but the new default gate is false.

Expected behavior:

```go
err := c.StartHashSlotMigration(ctx, 3, 2)
require.ErrorIs(t, err, ErrInvalidConfig)
```

Name the test with "single-node cluster" only if deployment shape is mentioned.

- [ ] **Step 3: Run focused test and verify RED**

Run:

```bash
go test ./pkg/cluster -run 'Test.*HashSlotMigration.*Disabled' -count=1
```

Expected: FAIL because there is no gate yet.

- [ ] **Step 4: Implement the gate**

Add a field to `pkg/cluster.Config` with an English comment:

```go
// EnableHashSlotMigration allows experimental hash-slot migration workflows.
// Keep this disabled unless durable delta forwarding, source fencing, and
// recoverable cutover semantics are explicitly accepted by the operator.
EnableHashSlotMigration bool
```

In `StartHashSlotMigration`, `AddSlot`, and `RemoveSlot` entry points that create migrations, return `ErrInvalidConfig` when the gate is false. Keep internal observation of already-existing migrations running so a node can continue to reconcile legacy/test state when explicitly constructed.

- [ ] **Step 5: Wire config only if operator-visible**

If exposing the gate through config, use a `WK_` key and update `wukongim.conf.example` with a detailed English comment. If not exposing yet, leave it as programmatic/test-only and document that production HashSlot migration remains disabled.

- [ ] **Step 6: Verify GREEN**

Run:

```bash
go test ./pkg/cluster -run 'Test.*HashSlotMigration.*Disabled|Test.*AddSlot|Test.*RemoveSlot' -count=1
```

Expected: PASS.

### Task 1.3: Add explicit experimental enable test coverage

**Files:**
- Modify: `pkg/cluster/cluster_test.go`

- [ ] **Step 1: Add test that enabled gate preserves existing behavior**

Use the same fixture as Task 1.2, but set `EnableHashSlotMigration: true`.

Expected:

```go
require.NoError(t, c.StartHashSlotMigration(ctx, 3, 2))
```

- [ ] **Step 2: Run focused tests**

Run:

```bash
go test ./pkg/cluster -run 'Test.*HashSlotMigration.*' -count=1
```

Expected: PASS.

- [ ] **Step 3: Run broader validation for Track 1**

Run:

```bash
go test ./pkg/cluster ./pkg/slot/fsm ./pkg/slot/proxy -count=1
```

Expected: PASS.

---

## Track 2: HashSlot Migration Protocol Hardening

### Task 2.1: Persist migration metadata and target dedup state

**Files:**
- Modify/Create: `pkg/slot/meta/*migration*.go`
- Modify: `pkg/slot/meta/snapshot.go`
- Modify: `pkg/slot/meta/snapshot_codec.go`
- Modify: `pkg/slot/fsm/statemachine.go`
- Test: `pkg/slot/meta/*migration*_test.go`
- Test: `pkg/slot/fsm/state_machine_test.go`

- [ ] **Step 1: Write meta tests for migration state and applied delta records**

Test CRUD for:

```go
type HashSlotMigrationState struct {
	HashSlot uint16
	SourceSlot uint64
	TargetSlot uint64
	Phase uint8
	FenceIndex uint64
	LastOutboxIndex uint64
	LastAckedIndex uint64
}

type AppliedHashSlotDelta struct {
	HashSlot uint16
	SourceSlot uint64
	SourceIndex uint64
}
```

Also test snapshot export/import preserves both state and applied delta records.

- [ ] **Step 2: Verify RED**

Run:

```bash
go test ./pkg/slot/meta ./pkg/slot/fsm -run 'Test.*Migration|Test.*Delta' -count=1
```

Expected: FAIL because the storage APIs do not exist or snapshot omits them.

- [ ] **Step 3: Implement minimal storage APIs**

Add Pebble key families for migration state and applied delta dedup. Keep names and comments in English. Ensure all writes go through `WriteBatch` when used by FSM.

- [ ] **Step 4: Verify GREEN**

Run the same focused tests. Expected: PASS.

### Task 2.2: Replace in-memory apply_delta dedup with persisted dedup

**Files:**
- Modify: `pkg/slot/fsm/statemachine.go`
- Modify: `pkg/slot/fsm/migration_cmds.go`
- Test: `pkg/slot/fsm/state_machine_test.go`

- [ ] **Step 1: Add failing restart dedup test**

Apply an `ApplyDelta` command, close the state machine, create a new state machine over the same DB, then reapply the same `ApplyDelta`. Assert the second apply is skipped using persisted dedup state.

- [ ] **Step 2: Verify RED**

Run:

```bash
go test ./pkg/slot/fsm -run 'Test.*ApplyDelta.*Restart.*Dedup' -count=1
```

Expected: FAIL because current dedup map starts empty after restart.

- [ ] **Step 3: Persist dedup in the same batch as apply_delta**

On `applyDeltaCmd`, check the persisted dedup record before applying the original command. If absent, apply original command and write `AppliedHashSlotDelta` in the same batch.

- [ ] **Step 4: Verify GREEN**

Run focused test. Expected: PASS.

### Task 2.3: Implement durable source delta outbox

**Files:**
- Modify/Create: `pkg/slot/meta/*migration_outbox*.go`
- Modify: `pkg/slot/fsm/statemachine.go`
- Modify: `pkg/cluster/hashslot_migration.go`
- Test: `pkg/slot/fsm/state_machine_test.go`
- Test: `pkg/cluster/cluster_test.go`

- [ ] **Step 1: Add failing test for crash before forward**

Simulate source apply while delta forwarding callback is absent or fails. Reopen state and assert the delta is visible in a durable outbox for replay.

- [ ] **Step 2: Verify RED**

Run:

```bash
go test ./pkg/slot/fsm -run 'Test.*DeltaOutbox.*Crash|Test.*Migration.*Outbox' -count=1
```

Expected: FAIL because no outbox exists.

- [ ] **Step 3: Write outbox after source command in same batch**

For non-`ApplyDelta` commands in `PhaseDelta`, write `DeltaOutbox{SourceIndex, HashSlot, Data}` in the same commit as source state mutation. Do not call remote forwarding before local commit.

- [ ] **Step 4: Add replay worker**

Change `makeHashSlotDeltaForwarder` / `forwardHashSlotDelta` into a scanner-driven worker that reads unacked outbox rows in order and proposes `ApplyDelta` to target. After success, propose or locally apply an ack command on the source slot to mark the outbox row acked.

- [ ] **Step 5: Verify GREEN**

Run:

```bash
go test ./pkg/slot/fsm ./pkg/cluster -run 'Test.*DeltaOutbox|Test.*HashSlot.*Delta' -count=1
```

Expected: PASS.

### Task 2.4: Enforce source fence before switching/finalize

**Files:**
- Modify: `pkg/slot/fsm/migration_cmds.go`
- Modify: `pkg/slot/fsm/statemachine.go`
- Modify: `pkg/cluster/hashslot_migration.go`
- Modify: `pkg/cluster/hashslot/hashslottable.go` only if phase semantics need stronger validation
- Test: `pkg/slot/fsm/state_machine_test.go`
- Test: `pkg/cluster/cluster_test.go`

- [ ] **Step 1: Add failing FSM fence test**

Apply `EnterFence(hashSlot)`, then try a normal business command for that hash slot. Expected: a retryable migration/fenced error, not silent apply.

- [ ] **Step 2: Add failing cluster cutover test**

When migration transitions to switching, assert source fence is proposed and finalize waits until all outbox rows up to `FenceIndex` are acked.

- [ ] **Step 3: Implement fence persistence and write rejection**

Make `enterFenceCmd.apply` persist the fenced state and fence index. `resolveHashSlot` or command apply path should reject normal writes for fenced hash slots while allowing `ApplyDelta`, outbox ack, and migration maintenance commands.

- [ ] **Step 4: Gate finalize on outbox ack**

Before `finalizeHashSlotMigration`, verify `LastAckedIndex >= FenceIndex`; otherwise keep migration in switching and retry later.

- [ ] **Step 5: Verify Track 2**

Run:

```bash
go test ./pkg/slot/meta ./pkg/slot/fsm ./pkg/cluster -run 'Test.*Migration|Test.*HashSlot|Test.*Delta|Test.*Fence' -count=1
```

Expected: PASS.

---

## Track 3: Raft Snapshot and Compaction

### Task 3.1: Controller snapshot/restore

**Files:**
- Modify: `pkg/controller/raft/service.go`
- Modify/Create: `pkg/controller/plane/snapshot.go`
- Modify: `pkg/controller/meta/*`
- Test: `pkg/controller/raft/service_test.go`
- Test: `pkg/controller/plane/*snapshot*_test.go`

- [ ] Write failing tests for controller service restart from non-empty snapshot.
- [ ] Add `StateMachine.Snapshot` / `Restore` support for controller metadata.
- [ ] Remove controller `ErrSnapshotUnsupported` startup failure.
- [ ] Add snapshot trigger after configurable applied-entry threshold.
- [ ] Compact persistent raft log after snapshot is durable.
- [ ] Verify with `go test ./pkg/controller/raft ./pkg/controller/plane ./pkg/controller/meta -run 'Test.*Snapshot|Test.*Restart' -count=1`.

### Task 3.2: Slot multiraft snapshot/compact trigger

**Files:**
- Modify: `pkg/slot/multiraft/slot.go`
- Modify: `pkg/slot/multiraft/ready.go`
- Modify: `pkg/slot/multiraft/types.go` if options are needed
- Modify: `pkg/raftlog/*` if a public compact API is missing
- Test: `pkg/slot/multiraft/recovery_test.go`
- Test: `pkg/slot/multiraft/e2e_test.go`

- [ ] Write failing test showing startup should not replay compacted entries before snapshot index.
- [ ] Trigger `stateMachine.Snapshot(ctx)` after threshold.
- [ ] Persist raftpb snapshot with index/term/conf state.
- [ ] Compact storage and MemoryStorage after snapshot.
- [ ] Verify follower catch-up from snapshot.
- [ ] Run `go test ./pkg/slot/multiraft ./pkg/raftlog -run 'Test.*Snapshot|Test.*Recovery|Test.*Compact' -count=1`.

---

## Track 4: Transport Security and Inbound Limits

### Task 4.1: Add inbound RPC resource limits

**Files:**
- Modify: `pkg/transport/server.go`
- Modify: `pkg/transport/conn.go`
- Modify: `pkg/transport/frame.go`
- Modify: `pkg/transport/types.go`
- Test: `pkg/transport/server_test.go`
- Test: `pkg/transport/conn_test.go`

- [ ] Add failing tests for max RPC concurrency, max frame by service, and slow read timeout.
- [ ] Add `ServerConfig` fields with English comments.
- [ ] Replace unbounded goroutine dispatch with bounded semaphore admission.
- [ ] Use connection-level cancellation instead of per-request cancellation goroutine when possible.
- [ ] Run `go test ./pkg/transport -run 'Test.*RPC.*Limit|Test.*Frame|Test.*Deadline' -count=1`.

### Task 4.2: Add node identity authentication

**Files:**
- Modify: `pkg/transport/server.go`
- Modify: `pkg/transport/pool.go`
- Modify: `pkg/cluster/config.go`
- Modify: cluster transport wiring files found by `rg -n "NewServerWithConfig|NewPool|Transport" pkg/cluster internal/app`
- Modify: `wukongim.conf.example`
- Test: `pkg/transport/*tls*_test.go`
- Test: `pkg/cluster/*transport*_test.go`

- [ ] Add failing mTLS tests: valid peer accepted, wrong node identity rejected, unknown CA rejected.
- [ ] Add TLS config fields and map `WK_` config keys.
- [ ] Bind node identity to certificate SAN URI or equivalent explicit verifier.
- [ ] Verify controller/slot/channel RPC still work in tests with generated certs.
- [ ] Run `go test ./pkg/transport ./pkg/cluster -run 'Test.*TLS|Test.*Auth|Test.*Transport' -count=1`.

---

## Track 5: ReadIndex Strict Reads

### Task 5.1: Controller read fence

**Files:**
- Modify: `pkg/controller/raft/service.go`
- Modify: `pkg/cluster/cluster.go`
- Modify: `pkg/cluster/controller_handler.go`
- Modify: `pkg/cluster/operator.go`
- Test: `pkg/controller/raft/service_test.go`
- Test: `pkg/cluster/controller_handler_test.go`

- [ ] Add failing test where local stale leader cannot serve strict read without quorum-confirmed fence.
- [ ] Implement `Service.ReadFence(ctx)` using `RawNode.ReadIndex` and wait for applied index.
- [ ] Call read fence before strict local reads and controller handler leader reads.
- [ ] Run `go test ./pkg/controller/raft ./pkg/cluster -run 'Test.*ReadFence|Test.*Strict|Test.*List.*Leader' -count=1`.

### Task 5.2: Slot authoritative read fence

**Files:**
- Modify: `pkg/slot/multiraft/api.go`
- Modify: `pkg/slot/multiraft/slot.go`
- Modify: `pkg/slot/proxy/authoritative_rpc.go`
- Test: `pkg/slot/multiraft/*read*_test.go`
- Test: `pkg/slot/proxy/*authoritative*_test.go`

- [ ] Add failing stale local slot leader read test.
- [ ] Implement per-slot read fence.
- [ ] Use it before local authoritative RPC reads.
- [ ] Run `go test ./pkg/slot/multiraft ./pkg/slot/proxy -run 'Test.*ReadFence|Test.*Authoritative' -count=1`.

---

## Track 6: ChannelRuntimeMeta Compare-And-Upsert

### Task 6.1: Add conditional runtime-meta command

**Files:**
- Modify: `pkg/slot/fsm/command.go`
- Modify: `pkg/slot/fsm/statemachine.go`
- Modify: `pkg/slot/meta/channel_runtime_meta.go`
- Modify: `pkg/slot/proxy/store.go`
- Modify: `internal/runtime/channelmeta/repair.go`
- Test: `pkg/slot/fsm/state_machine_test.go`
- Test: `internal/runtime/channelmeta/repair_test.go`

- [ ] Write failing test where stale repair command must not overwrite newer leader epoch.
- [ ] Add command fields for expected channel epoch and expected leader epoch, or introduce a durable revision.
- [ ] At FSM apply time, read current meta and treat mismatch as business conflict/no-op, not fatal Raft apply error.
- [ ] Change repair path to use compare-and-upsert.
- [ ] Run `go test ./pkg/slot/fsm ./pkg/slot/proxy ./internal/runtime/channelmeta -run 'Test.*RuntimeMeta|Test.*Repair|Test.*CAS|Test.*Compare' -count=1`.

---

## Track 7: Channel Long-Poll Fencing

### Task 7.1: Propagate and validate leader epoch

**Files:**
- Modify: `pkg/channel/runtime/types.go`
- Modify: `pkg/channel/runtime/longpoll.go`
- Modify: `pkg/channel/runtime/backpressure.go`
- Modify: `pkg/channel/transport/longpoll_codec.go`
- Modify: `pkg/channel/transport/session.go`
- Modify: `pkg/channel/types.go`
- Modify: `pkg/channel/replica/follower_apply.go`
- Test: `pkg/channel/runtime/longpoll_test.go`
- Test: `pkg/channel/replica/follower_apply_test.go` if present or create focused test in existing replica tests
- Test: `pkg/channel/transport/longpoll_codec_test.go`

- [ ] Write failing test that old leader epoch response from same node is rejected.
- [ ] Fill `LaneResponseItem.LeaderEpoch` from channel meta leader epoch, not channel epoch.
- [ ] Add `LeaderEpoch` to `FetchResponseEnvelope` and `ReplicaApplyFetchRequest`.
- [ ] Validate leader epoch in follower apply.
- [ ] Update long-poll codec round-trip tests.
- [ ] Run `go test ./pkg/channel/runtime ./pkg/channel/replica ./pkg/channel/transport -run 'Test.*LeaderEpoch|Test.*LongPoll|Test.*ApplyFetch' -count=1`.

### Task 7.2: Add lane response request/session fence

**Files:**
- Modify: `pkg/channel/runtime/lanes.go`
- Modify: `pkg/channel/runtime/backpressure.go`
- Modify: `pkg/channel/runtime/lane_dispatcher.go`
- Test: `pkg/channel/runtime/lanes_test.go`
- Test: `pkg/channel/runtime/session_test.go`

- [ ] Write failing test where a lane response after reset is ignored.
- [ ] Track current inflight request/session tuple per lane.
- [ ] Require response to match current inflight tuple before `ApplyResponse` mutates manager state.
- [ ] Ensure tombstone/generation drops also cover lane item application.
- [ ] Run `go test ./pkg/channel/runtime -run 'Test.*Lane.*Stale|Test.*Lane.*Reset|Test.*LongPoll' -count=1`.

---

## Track 8: Hygiene and Test Runtime

### Task 8.1: Stop Go package discovery from entering `web/node_modules`

**Files:**
- Modify: workspace/module config after inspecting `go env GOWORK` and repo layout
- Modify: CI or scripts if they call `go test ./...`
- Test: command output validation

- [ ] Run `go list ./... | rg 'node_modules|web/'` and verify current failure.
- [ ] Choose the smallest repo-compatible exclusion: remove checked-in `node_modules`, move it outside module, or adjust workspace/scripts to target `./cmd/... ./internal/... ./pkg/... ./test/...`.
- [ ] Verify `go list ./... | rg 'node_modules|web/'` returns no output, or document why target-scoped test commands are required.

### Task 8.2: Move slow cluster tests behind integration tag or fake time

**Files:**
- Modify: `pkg/cluster/*_test.go`
- Modify: helpers used by slow multi-node tests

- [ ] Identify slow tests with `go test ./pkg/cluster -run TestName -count=1 -v` or `go test -json` timing.
- [ ] Convert real multi-node long-wait tests to `//go:build integration` when they validate real runtime behavior.
- [ ] Replace wall-clock sleeps with fake clock where the test is unit-level.
- [ ] Verify `go test ./pkg/cluster -count=1` completes within an agreed unit-test budget.
- [ ] Verify `go test -tags=integration ./pkg/cluster -count=1` still covers moved tests.

---

## Final Verification Matrix

Run after each track, scoped to touched packages:

```bash
go test ./pkg/cluster ./pkg/controller/raft ./pkg/slot/multiraft ./pkg/slot/fsm ./pkg/slot/proxy ./pkg/transport ./pkg/channel/replica ./pkg/channel/runtime ./pkg/channel/transport ./internal/runtime/channelmeta -count=1
```

Run before merging major protocol tracks:

```bash
go test ./internal/... ./pkg/... -count=1
```

Run only for explicit release/integration validation:

```bash
go test -tags=integration ./...
```

