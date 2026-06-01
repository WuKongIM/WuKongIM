# ChannelV2 PullHint NeedMeta Design

## Context

ChannelV2 PullHint currently carries the leader's metadata summary:
`Replicas`, `ISR`, `MinISR`, and `Status`, in addition to the fence fields
needed to wake a follower. This solved an important cold-activation problem:
followers could lazily activate even when their local Slot metadata view had not
observed the newly created `ChannelRuntimeMeta`.

After ChannelV2 data replicas were decoupled from Slot metadata replicas, a
follower may be a Channel replica without hosting the channel's Slot metadata
shard. That means local Slot metadata lookup is not a reliable fallback for
PullHint metadata misses.

The next step is to reduce repeated PullHint metadata payloads without bringing
back the previous `channel_not_found` failure mode.

## Goal

Make PullHint a lightweight wakeup message and move full metadata transfer to
the follower Pull path only when the follower lacks matching local metadata.

After this change:

- PullHint does not carry full ChannelV2 metadata.
- PullHint still carries enough fence information to identify the leader state
  that the follower pulls.
- A follower with matching local metadata follows the existing fast path.
- A follower without matching local metadata enters a short-lived `PendingMeta`
  state and sends `Pull{NeedMeta=true}` to the channel leader.
- A successful `NeedMeta` Pull response must include full metadata from the
  current channel leader runtime state.
- The follower applies metadata before processing any records in that response.
- Failed `PendingMeta` activation releases the temporary runtime state.
- The service layer remains an RPC adapter; `PendingMeta` is owned by the
  reactor so worker completion, fences, and lifecycle state stay in one place.

## Non-Goals

This phase excludes:

- retaining older PullHint full-metadata message shapes,
- a separate `GetChannelMeta` RPC,
- remote Slot metadata reads,
- `MetaHash`, `ReplicasHash`, or `ConfigVersion`,
- leader-side per-follower MetaRef confidence or TTL state,
- batching PullHint or Pull metadata requests,
- changing ChannelV2 replica placement.

This repository has not been released with the current PullHint full-metadata
shape. The implementation must remove those fields directly rather than
carrying dual protocol branches.

## Protocol Shape

### PullHint

PullHint becomes a fence and wakeup message:

```go
type PullHintRequest struct {
    ChannelKey      ch.ChannelKey
    ChannelID       ch.ChannelID
    Epoch           uint64
    LeaderEpoch     uint64
    Leader          ch.NodeID
    LeaderLEO       uint64
    ActivityVersion uint64
    Reason          PullHintReason
}
```

It no longer carries `Replicas`, `ISR`, `MinISR`, or `Status`.

### Pull Request

Pull requests gain a metadata demand bit:

```go
type PullRequest struct {
    ChannelKey  ch.ChannelKey
    ChannelID   ch.ChannelID
    Epoch       uint64
    LeaderEpoch uint64
    Follower    ch.NodeID
    NextOffset  uint64
    AckOffset   uint64
    MaxBytes    int
    NeedMeta    bool
}
```

`NeedMeta=true` means the follower does not have matching metadata and cannot
safely process records until the leader returns full metadata.

### Pull Response

Pull responses gain optional metadata:

```go
type PullResponse struct {
    ChannelKey      ch.ChannelKey
    Epoch           uint64
    LeaderEpoch     uint64
    LeaderHW        uint64
    LeaderLEO       uint64
    ActivityVersion uint64
    NextPullAfter   time.Duration
    Control         PullControl
    Meta            *ch.Meta
    Records         []ch.Record
}
```

Success rule:

```text
PullRequest.NeedMeta == true => PullResponse.Meta must be present on success.
```

If the leader cannot provide metadata, it must return an error and must not
return records.

### Error Surface

Add `channelv2.ErrNotReplica` as the typed error for requests from nodes that
are not members of the current channel replica set. Metrics must aggregate it
under a `not_replica` result label. Stale fences continue to use
`ErrStaleMeta`; membership failure must not be hidden as stale metadata.

ChannelV2 errors must survive clusterv2 node RPC boundaries. The clusterv2
ChannelV2 transport must not return typed ChannelV2 application errors through
the generic node RPC error path, because that path serializes them as string
remote errors. Instead, ChannelV2 RPC handlers encode an application result
envelope and return a nil transport error when the server reached ChannelV2
logic. The client decodes the envelope and maps stable error codes back to the
local sentinel errors.

Required error codes:

- `invalid_config` -> `ErrInvalidConfig`
- `backpressured` -> `ErrBackpressured`
- `not_leader` -> `ErrNotLeader`
- `not_ready` -> `ErrNotReady`
- `stale_meta` -> `ErrStaleMeta`
- `channel_not_found` -> `ErrChannelNotFound`
- `not_replica` -> `ErrNotReplica`
- `closed` -> `ErrClosed`
- `too_many_channels` -> `ErrTooManyChannels`

Decode failures, invalid frames, node-discovery failures, closed connections,
and other errors that happen before a ChannelV2 handler runs remain transport
errors. They are not mapped to ChannelV2 sentinels.

Error priority is fixed for both leader Pull and follower PullHint handling:

1. Invalid envelope -> `ErrInvalidConfig`.
2. Missing local/runtime channel for a non-pending operation -> `ErrChannelNotFound`.
3. Runtime role is not the required role -> `ErrNotLeader` for leader Pull,
   `ErrStaleMeta` for follower PullHint.
4. `ChannelKey`, `ChannelID`, `Epoch`, `LeaderEpoch`, or `Leader` fence mismatch
   -> `ErrStaleMeta`.
5. Fence matches but the request node is not in `Replicas` -> `ErrNotReplica`.
6. Runtime status is not active or commit-ready -> `ErrNotReady`.

## Local Metadata Matching

A follower can use local runtime/cache metadata only when all of these are true:

- `meta.Key == PullHint.ChannelKey`
- `meta.ID == PullHint.ChannelID`
- `meta.Epoch == PullHint.Epoch`
- `meta.LeaderEpoch == PullHint.LeaderEpoch`
- `meta.Leader == PullHint.Leader`
- `meta.Status == active`
- local node is in `meta.Replicas`

The first phase does not compare `ISR`, `MinISR`, `LeaseUntil`, or a full meta
hash. Any change that affects replica admission must advance the Channel epoch.
Leader changes must advance `LeaderEpoch`.

## Data Flow

### Hot Path

```text
leader sends PullHint(fence)
  -> follower finds matching local meta
  -> follower submits normal PullHint event
  -> follower pulls records without NeedMeta
```

### Cold Or Newer-Fence Follower Path

```text
leader sends PullHint(fence)
  -> follower has no matching local meta
  -> follower creates PendingMeta(fence)
  -> follower sends Pull{NeedMeta=true, fence, NextOffset, AckOffset}
  -> leader validates request against current runtime state
  -> leader returns PullResponse{Meta, Records}
  -> follower validates Meta against fence
  -> follower applies Meta
  -> follower processes Records
  -> PendingMeta becomes normal active follower runtime
```

A successful response can include both `Meta` and `Records`. This avoids a
separate metadata round trip. The follower must apply metadata before records.
If metadata validation or application fails, the follower must discard the
records.

## Metadata Source

For `NeedMeta=true`, the leader returns metadata only from the current channel
leader runtime state.

The leader must not use `NeedMeta` Pull as a remote Slot metadata query. If the
channel leader runtime is not loaded or cannot prove current metadata, the Pull
returns a typed error such as not leader, stale meta, channel not found, or not
ready.

The leader must return metadata as a clone of the current runtime state. It
must reject non-active runtime state with `ErrNotReady` and must not include
records in that response.

This keeps boundaries clear:

- Slot metadata storage creates and stores authoritative `ChannelRuntimeMeta`.
- Channel leader runtime serves the active data-plane metadata needed by
  followers that are already on the replication path.

## Ownership And Component Boundaries

The service layer validates only the PullHint transport envelope, observes the
receive stage, and submits `EventPullHint` to the owning reactor. It must not
resolve Slot metadata, apply metadata, or hold `PendingMeta` state.

The reactor owns `PendingMeta`. A PullHint for an unloaded channel creates a
reactor-owned pending channel shell keyed by `ChannelKey`. The shell owns the
PullHint fence, the NeedMeta Pull operation ID, the retry budget, and the
deadline. The shell uses the existing worker pool and fenced completion path for
`TaskRPCPull`; it does not introduce a separate RPC path or service-owned
goroutine.

Service-to-reactor entry uses the existing
`Group.Submit(... EventPullHint ...)` path. Any helper added for state checks
must be read-only; metadata application, `PendingMeta` creation, and runtime
release remain reactor transitions. This keeps stale worker completions fenced
by `Generation`, `Epoch`, `LeaderEpoch`, and operation ID.

The clusterv2 ChannelV2 transport owns application error envelopes. Reactor and
service code continue to return normal Go sentinel errors; transport wrappers
perform encode/decode mapping at RPC boundaries.

## PendingMeta State

`PendingMeta` is a temporary follower activation state created only after a
PullHint metadata miss or a newer incoming fence that the loaded follower
runtime cannot safely serve.

Creating `PendingMeta` opens the normal channel store through
`Store.ChannelStore(ChannelKey, ChannelID)` and calls `Load()` before sending the
NeedMeta Pull. The pending shell uses the loaded local offsets:

- `NextOffset = local LEO + 1`
- `AckOffset = local LEO`
- local `HW` and `CheckpointHW` stay local until full metadata is applied

The pending shell is inserted in the same reactor `channels` map as normal
runtimes and counts against `MaxChannels`. A metadata miss must not bypass
capacity limits. Releasing `PendingMeta` closes the channel store handle,
removes the shell from the map, clears due-scheduler entries for that key, and
fences any inflight worker result by operation ID.

Although it lives in the `channels` map, `PendingMeta` is not an active runtime.
All non-PendingMeta entry points must preserve that distinction:

- `Group.HasChannelState` / `EventCheckState`: report not loaded for
  `PendingMeta`.
- `EventAppend`: return `ErrChannelNotFound`, matching unloaded behavior, so
  append admission must resolve authoritative metadata instead of using a
  pending shell.
- Leader-side `EventPull` and `EventAck`: return `ErrChannelNotFound`; a pending
  follower shell cannot serve leader responsibilities.
- `EventNotify`: treat pending as unloaded and complete without activating the
  normal follower lifecycle.
- `EventApplyMeta`: apply the pending fence-ordering rules below. Matching or
  newer valid metadata can convert the shell to a normal runtime; older or stale
  metadata cannot tear down pending state.
- Runtime snapshot and benchmark active leader/follower counts must not include
  `PendingMeta`. They can expose a separate pending count.
- Runtime eviction and `Close` can release `PendingMeta` directly without the
  normal active-runtime safe-eviction guard.

It holds only the activation data needed to complete or abandon the metadata
bootstrap:

- the PullHint fence,
- the follower's next offset and ack offset,
- a bounded `Pull{NeedMeta=true}` attempt,
- a deadline,
- the matching Pull response fence.

It must not:

- enter normal parked, stopped, or replicating follower lifecycle,
- write records before metadata is applied,
- stay resident indefinitely,
- participate in normal recovery probe scheduling.

State transitions:

```text
Unloaded + PullHint miss -> PendingMeta
LoadedFollower + matching PullHint -> ActiveFollower
LoadedFollower + older PullHint fence -> ActiveFollower plus ErrStaleMeta
LoadedFollower + newer PullHint fence -> release old runtime, then PendingMeta
LoadedFollower + same epoch/leaderEpoch but different leader/channel ID -> ActiveFollower plus ErrStaleMeta
PendingMeta + Meta validated/applied -> ActiveFollower
PendingMeta + not leader/stale/not found/not replica/validate fail/apply fail -> Unloaded
PendingMeta + timeout or retry exhaustion -> Unloaded
```

A PullHint fence is newer when it has the same `ChannelKey` and `ChannelID` as
the loaded runtime and either a higher `Epoch`, or the same `Epoch` with a
higher `LeaderEpoch`. When a loaded follower sees a newer fence, it must not
process that hint with the old runtime state. It releases the old runtime state
and creates a new `PendingMeta` attempt for the incoming fence.

An older fence returns `ErrStaleMeta` and leaves the loaded runtime unchanged.
A same-epoch/same-leader-epoch fence with a different leader or channel ID is
treated as stale/invalid for that runtime and does not create pending state.

PullHint received while `PendingMeta` exists:

- Same `ChannelKey`, `ChannelID`, `Epoch`, `LeaderEpoch`, and `Leader`: coalesce
  with the existing pending shell, set `LeaderLEO` and `ActivityVersion` to the
  max observed values, complete the PullHint successfully, and do not submit a
  second NeedMeta Pull while one is inflight.
- Same fence and no inflight NeedMeta Pull: submit the next bounded NeedMeta
  Pull immediately when the retry budget and deadline allow it.
- Older fence: return `ErrStaleMeta` and keep the pending shell unchanged.
- Newer fence for the same channel identity: release the old pending shell and
  create a fresh `PendingMeta` for the incoming fence.
- Same epoch and leader epoch but different leader or channel ID: return
  `ErrStaleMeta` and keep the pending shell unchanged. Empty or zero envelope
  fields are rejected as `ErrInvalidConfig` before pending-state lookup.

`EventApplyMeta` received while `PendingMeta` exists uses the same fence
ordering:

- Matching `ChannelKey`, `ChannelID`, `Epoch`, `LeaderEpoch`, and `Leader`:
  validate full metadata, require active status and local replica membership,
  convert the shell to a normal runtime, cancel the pending NeedMeta operation,
  and schedule follower replication.
- Newer fence for the same channel identity: validate full metadata, replace the
  pending shell with that metadata, cancel the old NeedMeta operation, and
  continue according to the new local role.
- Older fence: return `ErrStaleMeta` and keep the pending shell unchanged.
- Same epoch and leader epoch but different leader or channel ID: return
  `ErrStaleMeta` and keep the pending shell unchanged.
- Invalid full metadata for a matching or newer fence releases the pending shell
  and returns the typed validation error. Older or stale metadata never reaches
  this validation step and cannot tear down a newer pending activation.

Temporary transport errors can retry at most two total NeedMeta Pull attempts
per `PendingMeta` activation by default: the initial attempt plus one retry.
The attempts must also be bounded by a deadline. After the attempt or deadline
limit, the pending runtime is released and a subsequent leader PullHint retry can
create a fresh pending attempt.

Retry classification:

- Retryable until budget or deadline: transport-level timeout, temporary network
  error, or local RPC worker backpressure before a ChannelV2 application
  response is decoded.
- Terminal for the current pending activation: `ErrInvalidConfig`,
  `ErrNotLeader`, `ErrStaleMeta`, `ErrChannelNotFound`, `ErrNotReplica`,
  `ErrNotReady`, metadata validation failure, metadata application failure, or a
  successful `NeedMeta` response without `Meta`.
- `context.Canceled` and caller deadline cancellation release pending state
  immediately and do not wait for another retry tick.

`ErrNotReady` returned as a decoded ChannelV2 application error means the
channel leader runtime is loaded but not active enough to serve metadata; it is
terminal for the current activation. A later leader PullHint can create a new
pending attempt.

## Error Semantics

### PullHint Handling

- Invalid PullHint envelope, such as empty channel key, empty channel ID, zero
  leader, zero epoch, or zero leader epoch: return invalid before creating
  reactor state.
- Matching loaded local metadata: submit the normal PullHint event and return
  success.
- Missing local metadata: create `PendingMeta`, submit NeedMeta Pull, and
  return success.
- Loaded local metadata with an older incoming fence: return stale and keep the
  loaded runtime unchanged.
- Loaded local metadata with a newer incoming fence: release the loaded
  runtime, create `PendingMeta`, submit NeedMeta Pull, and return success.
- Loaded local metadata with the same epoch and leader epoch but a different
  leader or channel ID: return `ErrStaleMeta` and keep the loaded runtime
  unchanged. Empty or zero envelope fields are `ErrInvalidConfig`.
- Existing local metadata proves this node is not a replica for the same fence:
  return `ErrNotReplica` and do not create pending state.
- Existing `PendingMeta`: follow the coalescing, retry, stale, or replacement
  rules from `PendingMeta State`.

Metadata miss is not a PullHint receive error. It is an expected branch that
must be counted as pending metadata activation.

### Pull Handling

For `NeedMeta=true`, the leader must:

- verify it is still the channel leader for the request fence,
- verify the request follower is in current replicas,
- verify current runtime status is active,
- include `Meta` on success,
- omit records and return an error when the fence does not match.

Failure classes must remain typed and observable:

- not leader,
- stale meta,
- channel not found,
- not replica,
- not ready,
- timeout or canceled.

### PullResponse Handling

`PullResponse.Meta` is valid only for the matching `PendingMeta`
`PullRequest.NeedMeta=true` operation. A normal loaded follower Pull with
`NeedMeta=false` must treat a non-nil `Meta` as a protocol error
(`ErrInvalidConfig`), must not apply it, and must not process records from that
response.

When `Meta` is present for a matching `NeedMeta=true` pending operation:

1. Validate `Meta` against the request fence.
2. Validate local node membership in `Meta.Replicas`.
3. Validate `Meta.Status == active`.
4. Apply metadata.
5. Process records only after metadata application succeeds.

If any step fails, discard records and release `PendingMeta`.

## Metrics

The implementation must expose enough counters and gauges to prove payload
reduction and safety without creating a large metric surface:

- PullHint receive total by stage and result.
- PendingMeta current gauge.
- PendingMeta result count by reason.
- Pull NeedMeta result count by reason.
- PullResponse meta validate error count.
- PullResponse meta apply error count.

Existing PullHint receive error counters must no longer increase for ordinary
metadata misses. They remain reserved for invalid fences, not-replica
decisions, and unexpected failures.

## Testing

Focused unit tests must cover:

- PullHint no longer encodes full metadata fields.
- Follower with matching local metadata handles PullHint without `NeedMeta`.
- Follower with missing metadata creates `PendingMeta` and submits
  `Pull{NeedMeta=true}`.
- Loaded follower with a newer PullHint fence releases old runtime and creates
  `PendingMeta`.
- Loaded follower receiving an older hint returns stale and keeps the loaded
  runtime unchanged.
- `NeedMeta=true` successful Pull returns metadata from leader runtime state.
- `NeedMeta=true` leader response rejects non-active runtime state without
  records.
- `NeedMeta=true` from a non-replica follower returns `ErrNotReplica`.
- `NeedMeta=true` success without metadata is rejected as a protocol error.
- Follower validates and applies metadata before records.
- Metadata validation failure discards records and releases `PendingMeta`.
- Not leader, stale, not found, not replica, timeout, and retry exhaustion
  release `PendingMeta`.
- PullHint received while `PendingMeta` is inflight coalesces same-fence hints
  without duplicate NeedMeta Pulls.
- PullHint with a newer fence while `PendingMeta` exists releases the old pending
  shell and creates a fresh one.
- PendingMeta opens and loads the channel store, uses local LEO for
  `NextOffset/AckOffset`, counts against `MaxChannels`, and closes the store on
  release.
- PendingMeta is not reported as loaded by `HasChannelState`, is not counted as
  an active runtime in benchmark snapshots, and returns unloaded/not-ready
  semantics for ordinary Append, leader Pull, Ack, Notify, and eviction paths.
- `EventApplyMeta` can convert matching `PendingMeta` to an active follower and
  cancels the pending NeedMeta operation.
- Older or same-fence-different `EventApplyMeta` returns `ErrStaleMeta` and
  keeps existing `PendingMeta` unchanged.
- PendingMeta does not enter parked or replicating lifecycle before metadata is
  applied.
- Normal `NeedMeta=false` Pull responses carrying `Meta` are rejected as protocol
  errors without applying metadata or records.
- A transport timeout/backpressure before a ChannelV2 application response
  triggers exactly one retry by default.
- Decoded ChannelV2 application errors `ErrNotReady`, `ErrNotLeader`, and
  `ErrStaleMeta` do not retry and release `PendingMeta`.
- Context cancellation or pending deadline expiry releases `PendingMeta`
  immediately.
- `PullRequest.NeedMeta`, `PullResponse.Meta`, and the slim `PullHintRequest`
  round trip through the clusterv2 channel codec.
- ChannelV2 application errors round trip through clusterv2 ChannelV2 RPC
  envelopes and preserve `errors.Is` for `ErrNotReplica`, `ErrStaleMeta`,
  `ErrNotReady`, `ErrNotLeader`, and `ErrChannelNotFound`.
- `pkg/channelv2/FLOW.md` and `pkg/clusterv2/FLOW.md` no longer describe
  PullHint as carrying the leader metadata summary.

Focused commands:

```bash
GOWORK=off go test -count=1 ./pkg/channelv2/transport ./pkg/channelv2/service ./pkg/channelv2/reactor ./pkg/clusterv2/channels
GOWORK=off go test -count=1 ./pkg/clusterv2/routing ./pkg/clusterv2/channels ./pkg/clusterv2
```

Target benchmark:

```bash
GOWORK=off scripts/bench-wukongimv2-three-nodes-10kch.sh
```

Expected benchmark result:

- 10,000 channels activated,
- activation errors remain zero,
- backlog remains zero,
- active leader node count remains three,
- PullHint receive errors do not regress,
- PendingMeta success/fail metrics explain cold follower metadata misses.

## Rollout Criteria

The implementation is complete when:

- PullHint full metadata fields are removed from the active protocol.
- Missing follower metadata no longer counts as a PullHint receive error.
- `NeedMeta=true` is the only data-plane path for a follower to obtain metadata
  after a PullHint miss.
- PendingMeta has bounded residency and cannot leak unloaded channels.
- ChannelV2 typed errors remain typed after clusterv2 ChannelV2 RPC round trips.
- Loaded stale follower handling is explicit and covered by tests.
- The three-node 10k activation benchmark still passes.
- Metrics show how many PullHints used local metadata versus PendingMeta.
