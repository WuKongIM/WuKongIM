# Chat Lifecycle Soak Phase 1: Product Evidence Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Expose the minimum bounded product evidence needed to prove exactly-once channel metadata creation and all-node runtime lifecycle for selected real person channels.

**Architecture:** Make initial channel runtime metadata creation a result-bearing Slot FSM operation so the Slot leader can classify `created`, `already_existing`, and `error` authoritatively. Forward that result through the existing channel observer aggregation into one Prometheus counter. Separately extend the restricted bench runtime probe with a mutually exclusive explicit-channel selector capped at 1,200 and preserve detailed LEO/HW/checkpoint/role/status evidence.

**Tech Stack:** Go, existing Slot FSM/DB batch abstractions, MultiRaft `ProposeResult`, existing channel observer composition, Prometheus client, existing restricted bench HTTP API and target client.

---

## Task 1: Add a result-bearing create-only runtime metadata command

**Files:**

- Modify: `pkg/db/meta/batch.go`
- Modify: `pkg/db/meta/table_runtime_meta.go`
- Modify: `pkg/db/meta/channel_runtime_meta_integration_test.go`
- Modify: `pkg/slot/fsm/command.go`
- Modify: `pkg/slot/fsm/statemachine.go`
- Create: `pkg/slot/fsm/channel_runtime_meta_cmds.go`
- Create: `pkg/slot/fsm/channel_runtime_meta_cmds_test.go`
- Modify: `pkg/slot/FLOW.md`

- [ ] **Step 1: Write the DB first-create integration test**

Add a test that submits the same `ChannelRuntimeMeta` twice through the same public meta batch boundary and proves the first commit reports `created=true`, the second reports `created=false`, and the original row remains byte-for-byte equivalent.

Run:

```bash
GOWORK=off go test ./pkg/db/meta -run 'Test.*CreateChannelRuntimeMeta' -count=1
```

Expected: FAIL because no create-only result exists.

- [ ] **Step 2: Add the atomic create-only batch operation**

Add a DB operation whose existence check and insert execute inside the same committed batch. An existing row is a successful `already existing` outcome, not a Slot FSM fatal error. Keep ordinary upsert behavior for migration and repair paths.

- [ ] **Step 3: Write the Slot FSM result test**

Reserve the next command type after the current channel-runtime-meta commands and test these decoded result bytes:

```go
type CreateChannelRuntimeMetaResult struct {
    Created bool `json:"created"`
}
```

The test must apply the same command twice and assert `true` then `false` without modifying the stored row.

- [ ] **Step 4: Implement command encoding, inspection, and apply-result handling**

Add the command to both the FSM apply switch and command inspection. Document that rolling mixed-version use is unsupported, matching the existing command 50/51 upgrade constraint.

- [ ] **Step 5: Run the focused storage/FSM suite**

```bash
GOWORK=off go test ./pkg/db/meta ./pkg/slot/fsm -run 'ChannelRuntimeMeta|CommandInspection' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/db/meta pkg/slot/fsm pkg/slot/FLOW.md
git commit -m "feat(slot): expose authoritative channel meta create result"
```

## Task 2: Observe authoritative creation on the Slot leader

**Files:**

- Modify: `pkg/cluster/channels/meta.go`
- Modify: `pkg/cluster/channels/slot_meta.go`
- Modify: `pkg/cluster/channels/channels_test.go`
- Modify: `pkg/cluster/default_slot_proposer.go`
- Modify: `pkg/cluster/default_slot_proposer_test.go`
- Modify: `pkg/cluster/default_slots.go`
- Modify: `pkg/cluster/node_defaults.go`
- Modify: `pkg/metrics/channel_runtime.go`
- Modify: `pkg/metrics/registry_test.go`
- Modify: `internal/app/observability.go`
- Modify: `internal/app/observability_test.go`
- Modify: `pkg/cluster/FLOW.md`
- Modify: `internal/app/FLOW.md`

- [ ] **Step 1: Write the metadata-source creation tests**

Define a narrow `RuntimeMetaCreator` used only by `SlotMetaSource.EnsureChannelMeta`. Tests must prove:

- an existing authoritative row does not invoke create;
- a missing row invokes create once and re-reads the authoritative row;
- a concurrent loser receives `already existing` as success;
- ordinary upsert remains available to migration paths.

Run:

```bash
GOWORK=off go test ./pkg/cluster/channels -run 'TestSlotMetaSource.*Create' -count=1
```

Expected: FAIL before the creator interface exists.

- [ ] **Step 2: Use `ProposeResult` for the initial create**

Implement the production creator in `defaultChannelRuntimeMetaStore`. Decode `CreateChannelRuntimeMetaResult` returned by the leader. Do not increment metrics in `SlotMetaSource`, because that caller can run away from the authoritative physical Slot leader.

- [ ] **Step 3: Write proposer observer tests**

Add this narrow observer at the channel boundary:

```go
type MetaCreateObserver interface {
    ObserveChannelMetaCreate(slotID uint32, result MetaCreateResult)
}

type MetaCreateResult string

const (
    MetaCreateCreated         MetaCreateResult = "created"
    MetaCreateAlreadyExisting MetaCreateResult = "already_existing"
    MetaCreateError           MetaCreateResult = "error"
)
```

Tests must prove the proposer reports the physical Slot ID once after the authoritative future resolves, including a forwarded proposal, and reports `error` once when the future fails. Replica apply paths must not observe the metric.

- [ ] **Step 4: Wire the observer through existing composition**

Type-assert `n.cfg.Channel.Observer` to `channels.MetaCreateObserver` when constructing both local and forwarding Slot proposers. Extend the existing multi-channel observer and `channelMetricsObserver`; do not add another global service object.

- [ ] **Step 5: Write the bounded metric registry test**

Register exactly:

```text
wukongim_channelv2_meta_created_total{slot_id="<physical-slot>",result="created|already_existing|error"}
```

Assert only `slot_id` and `result` labels exist and the registry's promoted compatibility alias remains valid. No channel ID, UID, node address, or run ID label is allowed.

- [ ] **Step 6: Implement the counter and observer forwarding**

Add the counter to `pkg/metrics.ChannelRuntimeMetrics`. Increment only from the proposer observer after authoritative completion. Reheating existing metadata must not produce a successful creation observation.

- [ ] **Step 7: Run focused cluster, metrics, and wiring tests**

```bash
GOWORK=off go test ./pkg/cluster/channels ./pkg/cluster ./pkg/metrics ./internal/app -run 'MetaCreate|ChannelRuntime|Observability' -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add pkg/cluster pkg/metrics internal/app pkg/cluster/FLOW.md internal/app/FLOW.md
git commit -m "feat(metrics): count authoritative channel meta creation"
```

## Task 3: Add the explicit bounded runtime-probe contract

**Files:**

- Modify: `pkg/bench/model/bench_api.go`
- Modify: `pkg/bench/model/FLOW.md`
- Modify: `internal/access/api/bench_runtime.go`
- Modify: `internal/access/api/bench_runtime_test.go`
- Modify: `internal/access/api/FLOW.md`
- Modify: `internal/infra/cluster/bench_runtime.go`
- Create: `internal/infra/cluster/bench_runtime_test.go`
- Modify: `internal/infra/cluster/FLOW.md`
- Modify: `internal/bench/target/client.go`
- Modify: `internal/bench/target/client_test.go`

- [ ] **Step 1: Write DTO round-trip and validation tests**

Extend the request with concrete identities and the response with detailed per-node facts:

```go
type ChannelRuntimeChannelIdentity struct {
    ChannelID   string `json:"channel_id"`
    ChannelType uint8  `json:"channel_type"`
}

type ChannelRuntimeProbeRequest struct {
    // Existing generated selector fields remain for compatibility.
    Channels []ChannelRuntimeChannelIdentity `json:"channels,omitempty"`
}

type ChannelRuntimeProbeChannel struct {
    ChannelID    string `json:"channel_id"`
    ChannelType  uint8  `json:"channel_type"`
    Role         string `json:"role"`
    Status       string `json:"status"`
    LEO          uint64 `json:"leo"`
    HW           uint64 `json:"hw"`
    CheckpointHW uint64 `json:"checkpoint_hw"`
    LeaderEpoch  uint32 `json:"leader_epoch"`
    ChannelEpoch uint32 `json:"channel_epoch"`
}
```

Validation cases: exactly one selector mode, 1–1,200 explicit channels, unique `(channel_id, channel_type)`, non-empty ID, non-zero type, and rejection of explicit eviction requests.

- [ ] **Step 2: Run the handler tests to observe failure**

```bash
GOWORK=off go test ./internal/access/api -run 'TestBenchRuntimeProbe.*Explicit' -count=1
```

Expected: FAIL because the handler accepts only generated selectors.

- [ ] **Step 3: Implement protocol-only handler validation**

Keep generated selector validation unchanged. Map explicit identities into the internal query without normalizing or inventing product facts in the HTTP adapter.

- [ ] **Step 4: Write the infra mapping test**

Use a fake `pkg/channel.RuntimeProbe` and assert explicit identities are passed unchanged and that role/status/LEO/HW/checkpoint/epochs are returned for every requested channel. Missing channels must stay explicit in the result rather than disappearing.

- [ ] **Step 5: Implement the explicit selector and detailed result mapping**

In `internal/infra/cluster`, select generated IDs only when `Channels` is empty. Never expand a range for explicit probes. Preserve the existing generated mode for all older clients.

- [ ] **Step 6: Extend the target client test**

Make the fake server inspect the exact JSON request and return two channel details. Assert the client decodes all fields and sends the configured bearer token without logging it.

- [ ] **Step 7: Run focused bench API tests**

```bash
GOWORK=off go test ./pkg/bench/model ./internal/access/api ./internal/infra/cluster ./internal/bench/target -run 'RuntimeProbe|ProbeAll' -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add pkg/bench/model internal/access/api internal/infra/cluster internal/bench/target
git commit -m "feat(bench): probe explicit channel runtimes"
```

## Task 4: Phase verification

- [ ] **Step 1: Run all directly affected packages without a name filter**

```bash
GOWORK=off go test ./pkg/db/meta ./pkg/slot/fsm ./pkg/cluster/channels ./pkg/cluster ./pkg/metrics ./pkg/bench/model ./internal/access/api ./internal/infra/cluster ./internal/app ./internal/bench/target -count=1
```

Expected: PASS.

- [ ] **Step 2: Verify production metric cardinality and probe privacy**

```bash
rg -n 'meta_created_total|ChannelRuntimeProbeChannel' pkg internal
```

Expected: metric labels are limited to physical Slot and closed result; explicit channel IDs appear only in the restricted request/response and are not Prometheus labels.

- [ ] **Step 3: Inspect the diff**

```bash
git diff --check
git status --short
```

Expected: no whitespace errors; only phase files are staged or modified.
