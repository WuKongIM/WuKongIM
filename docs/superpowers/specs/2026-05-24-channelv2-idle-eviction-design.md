# channelv2 Idle Eviction Design

## Goal

`pkg/channelv2` keeps channel runtime state in each reactor after metadata is applied or an append loads the channel. With many channels, this makes memory grow and keeps maintenance paths scanning cold channels.

The goal is to evict idle channel runtime state safely while preserving replication correctness:

- Append is the only activity signal that keeps a channel hot.
- The leader controls follower pull speed and lifecycle.
- Followers stop before the leader evicts itself.
- A new Append reactivates an evicted channel through the existing metadata resolver and store load path.

## Non-Goals

- No control-plane metadata deletion.
- No bypass of cluster semantics; a single node is still a single-node cluster.
- No capacity-based LRU in the first version.
- No fetch-driven lazy activation. Fetch for an evicted channel may keep the existing unloaded-channel behavior.

## Current Context

`pkg/channelv2/reactor` owns a `map[ChannelKey]*runtimeChannel` per reactor. The map currently only grows. Maintenance paths such as append flushing, replication ticks, and idle waits scan this map, so many inactive channels create CPU and memory pressure.

Append already has a lazy load path:

```text
Append -> HasChannelState -> MetaResolver.ResolveChannelMeta -> ApplyMeta -> store.Load -> EventAppend
```

This path is the reactivation mechanism after eviction.

## Terms

- **PullHint**: best-effort leader-to-follower signal that tells a follower to pull now instead of waiting for its previous delay.
- **Parked follower**: a follower that is caught up and waiting for `NextPullAfter`.
- **Stopped follower**: a follower that checkpointed, removed local runtime state, and acknowledged `Stopped=true`.
- **Activity version**: a leader-owned monotonic version that changes on Append and fences stale stop acknowledgements.

## Protocol Changes

Rename the current generic notify path to PullHint semantics.

```go
type PullHintRequest struct {
    ChannelKey      ChannelKey
    ChannelID       ChannelID
    Epoch           uint64
    LeaderEpoch     uint64
    Leader          NodeID
    LeaderLEO       uint64
    ActivityVersion uint64
    Reason          PullHintReason
}

type PullHintReason uint8

const (
    PullHintReasonAppend PullHintReason = iota + 1
    PullHintReasonResume
)
```

Extend pull response so the leader controls follower pacing.

```go
type PullControl uint8

const (
    PullControlContinue PullControl = iota + 1
    PullControlStop
)

type PullResponse struct {
    ChannelKey      ChannelKey
    Epoch           uint64
    LeaderEpoch     uint64
    LeaderHW        uint64
    LeaderLEO       uint64
    Records         []Record
    ActivityVersion uint64
    NextPullAfter   time.Duration
    Control         PullControl
}
```

Extend follower ACK so stopped followers can be tracked by the leader.

```go
type AckRequest struct {
    ChannelKey      ChannelKey
    Epoch           uint64
    LeaderEpoch     uint64
    Follower        NodeID
    MatchOffset     uint64
    ActivityVersion uint64
    Stopped         bool
}
```

## Runtime State

Leader channels track lightweight lifecycle and follower pacing state:

```go
type channelLifecycle struct {
    LastAppendAt     time.Time
    ActivityVersion  uint64
    Phase            lifecyclePhase
    EvictEligibleAt  time.Time
}

type followerRuntimeProgress struct {
    Match              uint64
    LastPullAt         time.Time
    NextExpectedPullAt time.Time
    LastHintVersion    uint64
    LastAckVersion     uint64
    Parked             bool
    Stopped            bool
}
```

Followers track the latest accepted activity version and the leader-provided next pull time. A follower may enter a local parked state, but it must not permanently evict itself unless the leader returns `PullControlStop`.

## Leader Behavior

### ApplyMeta

`ApplyMeta` updates authoritative metadata but does not count as activity.

- If the channel is already loaded, apply metadata and fence stale in-flight work as today.
- If the channel is unloaded and callers explicitly invoke `ApplyMeta`, the reactor may load runtime state to preserve the current public API, but the channel starts cold and remains eligible for slowdown or eviction.
- Follower `ApplyMeta` must not be the long-term replication driver. Pull work should be driven by PullHint and leader-provided pull pacing.

### Append

When the leader accepts an Append request into the channel runtime:

1. Refresh `LastAppendAt`.
2. Increment `ActivityVersion`.
3. Move the channel back to hot if it was cooling or draining.
4. Clear stale stopped state for followers that need the new records.
5. Send `PullHint` only to followers that need an immediate pull.

The leader should send PullHint when a follower is:

- stopped,
- never pulled,
- parked with a future `NextExpectedPullAt`,
- lagging without an active pull/apply/ack cycle known to the leader.

The leader should not send repeated PullHint messages for the same follower and activity version.

### Pull Handling

A follower Pull updates leader-side follower state:

- `LastPullAt = now`
- `Stopped = false`
- `Parked = false`
- `LastAckVersion` remains controlled by ACKs

The leader returns records if `NextOffset <= LEO`. If no records are available, the leader returns pacing instructions:

```text
lagging follower
  -> records, NextPullAfter=0, Control=Continue

caught up and recently appended
  -> no records, NextPullAfter=small delay, Control=Continue

caught up and idle
  -> no records, gradually larger NextPullAfter, Control=Continue

caught up and eviction-safe
  -> no records, Control=Stop
```

The stop decision must consider all channel replicas, not only ISR members.

### Leader Eviction

The leader can evict itself only after:

- there are no pending append/fetch/pull waiters,
- no append batch is queued or in flight,
- local `HW >= LEO`,
- all follower replicas have `Match >= LEO`,
- all follower replicas have acknowledged `Stopped=true` for the current `ActivityVersion`.

After those checks pass, the leader stores a checkpoint at the safe HW and deletes its local `runtimeChannel`.

For a single-node cluster, there are no follower stop acknowledgements to wait for. The leader may evict after the local safety checks and checkpoint pass.

## Follower Behavior

### PullHint

On PullHint:

1. If the channel runtime is not loaded, resolve metadata and apply it.
2. Validate `ChannelKey`, `Epoch`, `LeaderEpoch`, and `Leader`.
3. Ignore stale activity versions.
4. Cancel any parked wait.
5. Submit an immediate Pull.

PullHint does not carry records and does not change durable state.

### PullResponse

When the follower receives records:

1. Apply records through the store apply worker.
2. Advance local LEO/HW.
3. ACK the new match offset.
4. Pull again immediately if more leader progress is expected.

When the follower receives `Control=Continue` with no records:

1. ACK match progress if needed.
2. Park until `NextPullAfter`, unless a newer PullHint arrives.

When the follower receives `Control=Stop`:

1. Verify local `LEO >= LeaderLEO` and `HW >= LeaderHW`.
2. Verify no pending pull/apply/ack work remains.
3. Store a checkpoint.
4. Delete local runtime state.
5. ACK with `Stopped=true` and the response `ActivityVersion`.

## Safety Rules

- Append cancels any local draining or evicting phase.
- Stale PullHint, PullResponse stop, or stopped ACK is ignored by activity version and metadata fence.
- A leader never evicts while any follower is behind leader LEO.
- A follower never stops until it is locally caught up to the leader stop response.
- Store checkpoint must happen before deleting runtime state.
- New Append after follower stop reactivates replication by sending PullHint to stopped followers.

## Scheduling

The first implementation should avoid full map scans on every reactor idle turn. Add a small due scheduler per reactor for:

- append flush due time,
- follower next pull due time,
- lifecycle slowdown or eviction due time.

The scheduler can be a min-heap or a coarse timing wheel. A min-heap is simpler and sufficient for the first version. Stale heap entries are ignored by comparing the stored due time or activity version with current channel state.

## Configuration

Add channelv2 service/reactor config fields with conservative defaults:

- `IdleSlowdownAfter`: duration after the last Append before follower pull intervals begin increasing.
- `IdleEvictAfter`: duration after the last Append before the leader may return `PullControlStop`.
- `IdlePullMinInterval`: minimum no-record pull delay.
- `IdlePullMaxInterval`: maximum parked pull delay.
- `IdleEvictCheckInterval`: retry interval while waiting for lagging or unstopped followers.

Configuration fields must have detailed English comments when implemented.

## Observability

Extend the reactor observer with lifecycle events or counters:

- channel runtime loaded,
- channel runtime evicted,
- follower parked,
- follower stopped,
- PullHint sent,
- PullHint dropped/backpressured,
- stop control returned,
- stop ACK received,
- eviction blocked by lagging follower or pending work.

These metrics are important for validating that eviction reduces channel count without increasing append-to-replicate latency too much.

## Tests

Unit tests should cover:

- Append refreshes activity version and cancels draining.
- PullResponse pacing increases while a channel stays idle.
- Append sends PullHint to parked followers and does not wait for the old long delay.
- Append sends PullHint to stopped or never-pulled followers.
- Append does not spam PullHint for already active followers.
- Leader refuses eviction while any replica follower match is behind LEO.
- Follower ignores stale PullHint or stale stop control.
- Follower checkpoints and deletes runtime before ACKing stopped.
- Leader evicts only after all followers ACK stopped for the current activity version.
- New Append after follower stop reloads follower through PullHint and lazy metadata.
- Reactor maintenance no longer scans every loaded channel on every idle turn.

Integration tests should use the existing channelv2 testkit with a three-node cluster and local transport.

## Rollout Plan

1. Add protocol fields while preserving current behavior.
2. Rename Notify internals to PullHint, keeping compatibility inside tests as needed.
3. Add leader-side activity and follower pacing state.
4. Add follower parking and PullHint interruption.
5. Add `PullControlStop` and stopped ACK.
6. Add leader-local eviction after all followers stop.
7. Replace broad reactor scans with due scheduling.
8. Update `pkg/channelv2/FLOW.md`.

## Acceptance Criteria

- Cold channel runtime state is eventually removed from all replicas.
- Leader runtime state is removed only after follower runtime state has stopped.
- A new Append to a parked follower triggers immediate PullHint and does not wait for the previous long delay.
- A new Append to an evicted channel reloads leader state and reactivates followers.
- Existing append/fetch/replication tests continue to pass.
- New tests cover stale activity-version races and lagging follower eviction blocks.
