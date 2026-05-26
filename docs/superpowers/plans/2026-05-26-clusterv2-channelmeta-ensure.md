# ClusterV2 ChannelMeta Ensure Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** Make first channel append create or retrieve authoritative ChannelV2 metadata through the channel's Slot leader before local append or channel-leader forwarding.

**Architecture:** `channelv2` remains a strict local runtime that only appends to pre-applied channel state. `pkg/clusterv2/channels` gets an append-only `ChannelMetaEnsurer` path: append calls use `EnsureChannelMeta`, read/follower paths keep using `ResolveChannelMeta`. Slot-backed ensure is idempotent: read existing meta, create if missing through the authoritative Slot proposal path, then read/return final meta.

**Tech Stack:** Go, `pkg/channelv2`, `pkg/clusterv2/channels`, `pkg/clusterv2/net`, `pkg/db/meta`, existing `go test` suites.

---

### Task 1: Append Path Uses Ensure Instead Of Resolve

**Files:**
- Modify: `pkg/clusterv2/channels/resolver.go`
- Modify: `pkg/clusterv2/channels/service.go`
- Test: `pkg/clusterv2/channels/channels_test.go`

- [x] **Step 1: Write failing service tests**

Add tests proving `Append`/`AppendBatch` call `EnsureChannelMeta` when available, and do not require a pre-existing `ResolveChannelMeta` hit.

- [x] **Step 2: Run red tests**

Run: `go test ./pkg/clusterv2/channels -run 'TestServiceEnsuresMetaBeforeLocalAppend|TestServiceEnsuresMetaBeforeForwardedAppend' -count=1`

Expected: FAIL because `ChannelMetaEnsurer` does not exist or append still calls resolve.

- [x] **Step 3: Implement interfaces and service helper**

Add `ChannelMetaEnsurer` with `EnsureChannelMeta(context.Context, channelv2.ChannelID) (channelv2.Meta, error)`. Change append helper to prefer `EnsureChannelMeta`; fallback to `ResolveChannelMeta` for existing tests/runtime compatibility.

- [x] **Step 4: Run green tests**

Run: `go test ./pkg/clusterv2/channels -run 'TestServiceEnsuresMetaBeforeLocalAppend|TestServiceEnsuresMetaBeforeForwardedAppend|TestServiceAppliesResolvedMetaBeforeLocalAppend|TestServiceForwardsAppendToResolvedLeader' -count=1`

Expected: PASS.

### Task 2: Static Source Supports Ensure For Tests

**Files:**
- Modify: `pkg/clusterv2/channels/resolver.go`
- Test: `pkg/clusterv2/channels/channels_test.go`

- [x] **Step 1: Write failing static ensure test**

Test `StaticMetaSource.EnsureChannelMeta` returns a cloned meta and maps missing to `ErrChannelNotFound`.

- [x] **Step 2: Run red test**

Run: `go test ./pkg/clusterv2/channels -run TestStaticMetaSourceEnsuresExistingMeta -count=1`

Expected: FAIL because method is missing.

- [x] **Step 3: Implement minimal method**

Delegate to `ResolveChannelMeta` and return cloned data.

- [x] **Step 4: Run green test**

Run: `go test ./pkg/clusterv2/channels -run TestStaticMetaSourceEnsuresExistingMeta -count=1`

Expected: PASS.

### Task 3: Slot Source Ensures Missing Runtime Meta

**Files:**
- Modify: `pkg/clusterv2/channels/resolver.go`
- Test: `pkg/clusterv2/channels/channels_test.go`

- [x] **Step 1: Write failing SlotMetaSource tests**

Add tests that `EnsureChannelMeta` returns existing runtime meta without creating, and creates default active meta when reader reports `metadb.ErrNotFound`.

- [x] **Step 2: Run red tests**

Run: `go test ./pkg/clusterv2/channels -run 'TestSlotMetaSourceEnsuresExistingRuntimeMeta|TestSlotMetaSourceCreatesMissingRuntimeMeta' -count=1`

Expected: FAIL because creation port is not implemented.

- [x] **Step 3: Implement creation port and planner**

Add a narrow `RuntimeMetaEnsurer`/writer dependency used only by `SlotMetaSource`. First version generates `Epoch=1`, `LeaderEpoch=1`, `StatusActive`, `Replicas/ISR` from configured placement peers, `MinISR=1`, and deterministic leader as first replica unless tests configure otherwise.

- [x] **Step 4: Run green tests**

Run: `go test ./pkg/clusterv2/channels -run 'TestSlotMetaSourceEnsuresExistingRuntimeMeta|TestSlotMetaSourceCreatesMissingRuntimeMeta' -count=1`

Expected: PASS.

### Task 4: Integration And Flow Docs

**Files:**
- Modify: `pkg/clusterv2/FLOW.md`
- Modify: `pkg/clusterv2/integration_test.go`

- [x] **Step 1: Add first append integration/smoke test**

Where current harness can support it, add a test that a new channel can be appended without pre-seeded static meta by using an ensurer-backed source.

- [x] **Step 2: Update flow docs**

Document `Append -> EnsureChannelMeta -> local ApplyMeta or channel-leader forward`.

- [x] **Step 3: Run package verification**

Run: `go test ./pkg/clusterv2/channels ./pkg/clusterv2 ./pkg/channelv2/service -count=1`

Expected: PASS.
