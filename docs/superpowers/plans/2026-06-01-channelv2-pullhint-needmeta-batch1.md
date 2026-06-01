# ChannelV2 PullHint NeedMeta Batch 1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the first production slice of the PullHint NeedMeta design: slim PullHint protocol, typed ChannelV2 RPC errors, leader-side NeedMeta responses, and the reactor-owned PendingMeta bootstrap path.

**Architecture:** Service remains a synchronous RPC adapter and submits PullHint/Pull/Ack events into the owning reactor. The reactor owns loaded runtime and PendingMeta state, including store handles, fences, retries, and worker completion. clusterv2 ChannelV2 transport wraps application-level ChannelV2 errors in a channel RPC envelope so typed sentinel errors survive node RPC boundaries.

**Tech Stack:** Go, `pkg/channelv2`, `pkg/channelv2/reactor`, `pkg/channelv2/service`, `pkg/clusterv2/channels`, existing `store.ChannelStore`, worker pools, JSON channel RPC codec.

---

## Source Spec

- `docs/superpowers/specs/2026-06-01-channelv2-pullhint-needmeta-design.md`

## File Map

- Modify `pkg/channelv2/errors.go`: add `ErrNotReplica`.
- Modify `pkg/channelv2/transport/types.go`: remove full metadata from `PullHintRequest`; add `PullRequest.NeedMeta`; add `PullResponse.Meta`.
- Modify `pkg/clusterv2/channels/codec.go`: round-trip new protocol fields and add channel application-error envelope helpers.
- Modify `pkg/clusterv2/channels/transport.go`: encode/decode ChannelV2 application errors for Pull, Ack, PullHint, Notify, Append, and AppendBatch handlers/clients.
- Modify `pkg/channelv2/reactor/leader_replication.go`: leader Pull validates NeedMeta, returns cloned active metadata, and uses `ErrNotReplica` for matched-fence membership failures.
- Modify `pkg/channelv2/reactor/follower_replication.go`: reject normal Pull responses carrying Meta; apply metadata before records for PendingMeta.
- Modify `pkg/channelv2/reactor/reactor.go`, `event.go`, `replication_state.go`, and focused helpers: add PendingMeta state, creation, release, ApplyMeta conversion, HasChannelState behavior, and non-active runtime guards.
- Modify `pkg/channelv2/service/replication.go`: remove service-side Slot meta resolve/apply from PullHint; only validate the PullHint envelope, observe stages, submit `EventPullHint`, and await.
- Modify `pkg/channelv2/FLOW.md`, `pkg/channelv2/reactor/FLOW.md`, `pkg/clusterv2/FLOW.md`: document slim PullHint and NeedMeta/PendingMeta flow.
- Tests: update and add focused tests in `pkg/clusterv2/channels/channels_test.go`, `pkg/channelv2/service/service_test.go`, `pkg/channelv2/reactor/replication_state_test.go`, `pkg/channelv2/reactor/group_test.go`, and `pkg/channelv2/transport/local_test.go`.

---

### Task 1: Protocol And Channel RPC Error Envelope

**Files:**
- Modify: `pkg/channelv2/errors.go`
- Modify: `pkg/channelv2/transport/types.go`
- Modify: `pkg/clusterv2/channels/codec.go`
- Modify: `pkg/clusterv2/channels/transport.go`
- Test: `pkg/clusterv2/channels/channels_test.go`

- [ ] **Step 1: Write failing codec and RPC error tests**

Add tests that assert:

```go
// PullRequest round trip preserves NeedMeta.
req := channeltransport.PullRequest{
    ChannelKey: "1:room",
    ChannelID: ch.ChannelID{ID: "room", Type: 1},
    Epoch: 1, LeaderEpoch: 2, Follower: 3,
    NextOffset: 4, AckOffset: 3, MaxBytes: 1024,
    NeedMeta: true,
}

// PullResponse round trip preserves Meta.
resp := channeltransport.PullResponse{
    ChannelKey: "1:room", Epoch: 1, LeaderEpoch: 2,
    Meta: &ch.Meta{Key: "1:room", ID: ch.ChannelID{ID: "room", Type: 1}, Epoch: 1, LeaderEpoch: 2, Leader: 1, Replicas: []ch.NodeID{1, 3}, ISR: []ch.NodeID{1, 3}, MinISR: 2, Status: ch.StatusActive},
}

// PullHint round trip no longer references Replicas, ISR, MinISR, or Status.
hint := channeltransport.PullHintRequest{
    ChannelKey: "1:room", ChannelID: ch.ChannelID{ID: "room", Type: 1},
    Epoch: 1, LeaderEpoch: 2, Leader: 1,
    LeaderLEO: 4, ActivityVersion: 4,
    Reason: channeltransport.PullHintReasonAppend,
}
```

Add an RPC test that registers a fake ChannelV2 server returning each sentinel error and verifies `errors.Is(client.Pull/PullHint/Ack(...), sentinel)` for:

```go
[]error{
    ch.ErrInvalidConfig, ch.ErrBackpressured, ch.ErrNotLeader,
    ch.ErrNotReady, ch.ErrStaleMeta, ch.ErrChannelNotFound,
    ch.ErrNotReplica, ch.ErrClosed, ch.ErrTooManyChannels,
}
```

- [ ] **Step 2: Run failing tests**

Run:

```bash
GOWORK=off go test -count=1 ./pkg/clusterv2/channels
```

Expected before implementation: compile failures for `NeedMeta`, `Meta`, and `ErrNotReplica`, plus old PullHint metadata expectations.

- [ ] **Step 3: Implement protocol and error envelope**

Implementation constraints:

- `PullHintRequest` keeps only fence/wakeup fields from the spec.
- `PullRequest` adds `NeedMeta bool`.
- `PullResponse` adds `Meta *ch.Meta`.
- `ErrNotReplica` is a package sentinel in `pkg/channelv2/errors.go` with an English comment.
- Add unexported channel RPC application-error helpers in `pkg/clusterv2/channels/codec.go`, for example `encodeRPCResult(kind, payload, err)` and `decodeRPCResult(data, kind, &payload) error`.
- Channel application errors are encoded in normal RPC response bodies with nil transport error; invalid request decode and malformed frames still return normal RPC errors.
- Client-side decode maps stable error codes back to the local sentinel errors with `errors.Is` support.

- [ ] **Step 4: Verify Task 1**

Run:

```bash
GOWORK=off go test -count=1 ./pkg/channelv2/transport ./pkg/clusterv2/channels
```

- [ ] **Step 5: Commit Task 1**

```bash
git add pkg/channelv2/errors.go pkg/channelv2/transport/types.go pkg/clusterv2/channels/codec.go pkg/clusterv2/channels/transport.go pkg/clusterv2/channels/channels_test.go
git commit -m "feat(channelv2): preserve channel rpc errors"
```

---

### Task 2: Leader NeedMeta Pull Semantics

**Files:**
- Modify: `pkg/channelv2/reactor/leader_replication.go`
- Test: `pkg/channelv2/reactor/replication_state_test.go`

- [ ] **Step 1: Write failing leader tests**

Add tests for:

- `NeedMeta=true` successful Pull returns a non-nil cloned `Meta`.
- `NeedMeta=true` from a matched fence but non-replica follower returns `ErrNotReplica`.
- `NeedMeta=true` on non-active runtime returns `ErrNotReady` and no records.
- `NeedMeta=false` behavior remains unchanged.

- [ ] **Step 2: Run failing tests**

```bash
GOWORK=off go test -count=1 ./pkg/channelv2/reactor
```

- [ ] **Step 3: Implement leader Pull rules**

Implementation constraints:

- Validate invalid request shape first.
- Validate role/fence before membership.
- Return `ErrNotReplica` only when key, channel ID, epoch, leader epoch, and leader match but follower is absent from current replicas.
- For `NeedMeta=true`, clone current active runtime metadata into `PullResponse.Meta`.
- If runtime status is not active, return `ErrNotReady` without records.

- [ ] **Step 4: Verify Task 2**

```bash
GOWORK=off go test -count=1 ./pkg/channelv2/reactor
```

- [ ] **Step 5: Commit Task 2**

```bash
git add pkg/channelv2/reactor/leader_replication.go pkg/channelv2/reactor/replication_state_test.go
git commit -m "feat(channelv2): serve needmeta pulls from leader runtime"
```

---

### Task 3: Reactor-Owned PendingMeta Follower Bootstrap

**Files:**
- Modify: `pkg/channelv2/reactor/event.go`
- Modify: `pkg/channelv2/reactor/reactor.go`
- Modify: `pkg/channelv2/reactor/replication_state.go`
- Modify: `pkg/channelv2/reactor/follower_replication.go`
- Test: `pkg/channelv2/reactor/replication_state_test.go`
- Test: `pkg/channelv2/reactor/group_test.go`

- [ ] **Step 1: Write failing PendingMeta tests**

Cover these cases:

- Unloaded PullHint creates PendingMeta, opens/loads store, sends `Pull{NeedMeta=true}` with `NextOffset=LEO+1` and `AckOffset=LEO`.
- `HasChannelState` reports false while PendingMeta exists.
- Same-fence PullHint coalesces and does not submit a duplicate NeedMeta Pull while inflight.
- Newer-fence PullHint releases old pending shell and creates a fresh one.
- Older PullHint and older `EventApplyMeta` return `ErrStaleMeta` and keep pending unchanged.
- Matching NeedMeta PullResponse applies Meta before records and becomes active follower.
- `NeedMeta=false` PullResponse containing Meta returns `ErrInvalidConfig` and does not apply records.
- Decoded application errors `ErrNotReady`, `ErrNotLeader`, and `ErrStaleMeta` release pending without retry.
- Transport-level backpressure/timeout retries once by default.
- Context cancellation or pending deadline releases pending immediately.

- [ ] **Step 2: Run failing reactor tests**

```bash
GOWORK=off go test -count=1 ./pkg/channelv2/reactor
```

- [ ] **Step 3: Implement PendingMeta**

Implementation constraints:

- PendingMeta is represented explicitly, not inferred from zero role/status.
- PendingMeta counts against `MaxChannels` but is not active runtime.
- `EventCheckState` returns not loaded for PendingMeta.
- Ordinary Append, leader Pull, Ack, Notify, snapshot, and bench active counts must not treat PendingMeta as active.
- Release closes store and removes the shell from `channels`.
- Fenced worker completions for released pending state are ignored.
- Only matching `NeedMeta=true` responses can apply `Meta`; normal responses carrying `Meta` are protocol errors.

- [ ] **Step 4: Verify Task 3**

```bash
GOWORK=off go test -count=1 ./pkg/channelv2/reactor
```

- [ ] **Step 5: Commit Task 3**

```bash
git add pkg/channelv2/reactor/event.go pkg/channelv2/reactor/reactor.go pkg/channelv2/reactor/replication_state.go pkg/channelv2/reactor/follower_replication.go pkg/channelv2/reactor/replication_state_test.go pkg/channelv2/reactor/group_test.go
git commit -m "feat(channelv2): bootstrap followers with pending meta"
```

---

### Task 4: Service Boundary, Docs, And Focused Verification

**Files:**
- Modify: `pkg/channelv2/service/replication.go`
- Modify: `pkg/channelv2/service/service.go`
- Modify: `pkg/channelv2/service/service_test.go`
- Modify: `pkg/channelv2/FLOW.md`
- Modify: `pkg/channelv2/reactor/FLOW.md`
- Modify: `pkg/clusterv2/FLOW.md`

- [ ] **Step 1: Write failing service/docs tests**

Cover:

- `HandlePullHint` no longer calls `MetaResolver`.
- Missing follower metadata is accepted as PendingMeta activation, not reported as service-side `ErrChannelNotFound`.
- Invalid PullHint envelope still returns `ErrInvalidConfig`.
- `HandleNotify` preserves legacy no-op behavior for missing/stale hints.

- [ ] **Step 2: Run failing service tests**

```bash
GOWORK=off go test -count=1 ./pkg/channelv2/service
```

- [ ] **Step 3: Implement service boundary and update FLOW docs**

Implementation constraints:

- Remove PullHint service-side meta resolve/apply helpers.
- Keep observer stages low-cardinality and aligned with the spec.
- Update FLOW docs anywhere they still describe PullHint as carrying `Replicas`, `ISR`, `MinISR`, or `Status`.

- [ ] **Step 4: Run focused verification**

```bash
GOWORK=off go test -count=1 ./pkg/channelv2/transport ./pkg/channelv2/service ./pkg/channelv2/reactor ./pkg/clusterv2/channels
GOWORK=off go test -count=1 ./pkg/clusterv2/routing ./pkg/clusterv2/channels ./pkg/clusterv2
```

- [ ] **Step 5: Commit Task 4**

```bash
git add pkg/channelv2/service/replication.go pkg/channelv2/service/service.go pkg/channelv2/service/service_test.go pkg/channelv2/FLOW.md pkg/channelv2/reactor/FLOW.md pkg/clusterv2/FLOW.md
git commit -m "feat(channelv2): route pullhint through pending meta"
```
