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

## Local Metadata Matching

A follower may use local runtime/cache metadata only when all of these are true:

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

### Cold Or Stale Follower Path

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

The response may include both `Meta` and `Records`. This avoids a separate
metadata round trip. The follower must apply metadata before records. If
metadata validation or application fails, the follower must discard the records.

## Metadata Source

For `NeedMeta=true`, the leader returns metadata only from the current channel
leader runtime state.

The leader must not use `NeedMeta` Pull as a remote Slot metadata query. If the
channel leader runtime is not loaded or cannot prove current metadata, the Pull
returns a typed error such as not leader, stale meta, channel not found, or not
ready.

This keeps boundaries clear:

- Slot metadata storage creates and stores authoritative `ChannelRuntimeMeta`.
- Channel leader runtime serves the active data-plane metadata needed by
  followers that are already on the replication path.

## PendingMeta State

`PendingMeta` is a temporary follower activation state created only after a
PullHint metadata miss or stale local metadata.

It may:

- hold the PullHint fence,
- remember the follower's next offset and ack offset,
- submit a bounded `Pull{NeedMeta=true}`,
- wait for the matching Pull response.

It must not:

- enter normal parked, stopped, or replicating follower lifecycle,
- write records before metadata is applied,
- stay resident indefinitely,
- participate in normal recovery probe scheduling.

State transitions:

```text
Unloaded + PullHint miss -> PendingMeta
PendingMeta + Meta validated/applied -> ActiveFollower
PendingMeta + not leader/stale/not found/not replica/validate fail/apply fail -> Unloaded
PendingMeta + timeout or retry exhaustion -> Unloaded
```

Temporary transport errors may retry at most two total NeedMeta Pull attempts
per `PendingMeta` activation by default: the initial attempt plus one retry.
The attempts must also be bounded by a deadline. After the attempt or deadline
limit, the pending runtime is released and a subsequent leader PullHint retry can
create a fresh pending attempt.

## Error Semantics

### PullHint Handling

- Matching local metadata: submit normal PullHint event and return success.
- Missing or stale local metadata: create `PendingMeta`, submit NeedMeta Pull,
  and return success.
- Invalid PullHint fence, such as zero leader or zero epoch: return invalid.
- Existing local metadata proves this node is not a replica for the same fence:
  return not replica and do not create pending state.

Metadata miss is not a PullHint receive error. It is an expected branch that
must be counted as pending metadata activation.

### Pull Handling

For `NeedMeta=true`, the leader must:

- verify it is still the channel leader for the request fence,
- verify the request follower is in current replicas,
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

When `Meta` is present:

1. Validate `Meta` against the request fence.
2. Validate local node membership in `Meta.Replicas`.
3. Validate `Meta.Status == active`.
4. Apply metadata.
5. Process records only after metadata application succeeds.

If any step fails, discard records and release `PendingMeta`.

## Metrics

The implementation must expose enough counters and gauges to prove both payload
reduction and safety:

- PullHint total count.
- PullHint local meta hit count.
- PullHint pending meta pull submitted count.
- PullHint invalid fence count.
- PendingMeta current gauge.
- PendingMeta success count.
- PendingMeta fail count by reason.
- PendingMeta timeout count.
- Pull NeedMeta request count.
- Pull NeedMeta response meta included count.
- Pull NeedMeta response error count by reason.
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
- Follower with stale metadata creates `PendingMeta`.
- `NeedMeta=true` successful Pull returns metadata from leader runtime state.
- `NeedMeta=true` success without metadata is rejected as a protocol error.
- Follower validates and applies metadata before records.
- Metadata validation failure discards records and releases `PendingMeta`.
- Not leader, stale, not found, not replica, timeout, and retry exhaustion
  release `PendingMeta`.
- PendingMeta does not enter parked or replicating lifecycle before metadata is
  applied.

Focused commands:

```bash
GOWORK=off go test -count=1 ./pkg/channelv2/transport ./pkg/channelv2/reactor ./pkg/clusterv2/channels
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
- The three-node 10k activation benchmark still passes.
- Metrics show how many PullHints used local metadata versus PendingMeta.
