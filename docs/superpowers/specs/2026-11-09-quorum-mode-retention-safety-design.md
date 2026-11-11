# Message retention safety under durable-quorum replication

Status: approved 2026-11-09 (option B). Tracking: #1113.

## Problem

Since durable-quorum Channel replication became the cluster path
(`e5a8f7caa`, 2026-08-16, first released in v3.0.0-beta.1), message retention
cannot advance on any Channel that has more than one replica:

- **Logical retention:** `POST /manager/...` retention advance always returns
  `blocked: min_isr_match_offset` with `advanced_through_seq=0`.
- **Physical cleanup:** with `channel.message_retention_physical_gc_enable = true`,
  the leader never deletes anything, because `retentionTrimDecision` returns
  `min_isr_lag`. Followers may trim locally.

Single-replica Channels are unaffected. Physical GC is off by default, so
the visible symptom today is the Manager retention API. Enabling GC on a
replicated cluster would leave leader disk growth unbounded.

Evidence: `TestThreeNodeManagerRetentionForwardsAndSurvivesLeaderRestart`
fails in every nightly run. The leader's channel runtime meta was `ISR=[1 2 3]`,
`MinISR=2`, and every replica already served all three messages. Retention
still reported `{Status:blocked AdvancedThroughSeq:0 BlockedReason:min_isr_match_offset}`.

## Root cause

`minISRMatchOffset` (`pkg/channel/reactor/retention.go`) takes the minimum
`state.Progress[node].Match` across ISR members. A member without an entry
counts as `RetentionThroughSeq`, which is 0 initially.

- **Legacy pull replication:** `machine.applyFollowerAck`
  (`pkg/channel/machine/append.go`) fills `Progress[follower]` from follower ACKs.
- **Durable-quorum mode:** `state.Progress` only ever holds the local node
  (`quorum_runtime.go` install, local append). Follower durability lives inside
  `pkg/channel/replication`, whose public `DurableQuorumLog` deliberately
  exposes only `Install/Commit/Release`, and `Receipt` carries no per-voter
  data.

It is not only missing bookkeeping. A `durableRound` dispatches to just enough
followers to reach quorum and hedges to others only on delay. By design the
leader therefore does not learn every ISR member's position. Lagging
replicas catch up through bounded repair workers from peers.

## Safety constraints

1. **Logical retention** (`RetentionThroughSeq` in Slot metadata) hides
   sequences at or below the floor from committed reads. It deletes nothing.
2. **Physical trim** deletes local records. A record must not be deleted from
   a replica that is the only remaining source another replica still needs
   for contiguous catch-up (repair requires a matching exact tail).
3. HW and the durable checkpoint already bound both. No change there.

## Options

### A. Logical retention by HW only; physical guard unchanged

Drop `MinISRMatchOffset` from the logical boundary in durable-quorum mode, and
keep the leader physical guard.

- **Pros:** tiny change, and the Manager API works. No data is ever deleted
  sooner than today.
- **Cons:** the leader still never reclaims disk. The E2E test, which expects
  physical trim on the leader, still fails. Followers keep trimming by local
  HW while the leader holds the full log. This is safe but asymmetric.

### B. Expose proven per-replica durable progress (recommended)

Track, inside the quorum log, the highest sequence each voter has proven
durable, and expose a read-only accessor, for example
`ReplicaProgress(key) []VoterProgress`. The reactor's `minISRMatchOffset`
uses it in durable-quorum mode.

The progress has three sources, all of which already exist as proofs:

- durable-round completions (`completed[voter]`);
- repair-worker page acknowledgements;
- peer tails gathered during `Install` recovery, which seed progress after a
  restart or leader change.

Rules:

- A voter without a proof counts as 0, so retention waits.
- Progress is process-local and bounded by voters (at most 256) per loaded
  Channel. Nothing is persisted.

Assessment:

- **Pros:** keeps the documented semantics ("every ISR member reached the
  boundary"). Logical and physical trim are both correct. No protocol, wire or
  persisted-data change, so rollback is direct.
- **Cons:**
  - Moderate change in `pkg/channel/replication` and the reactor.
  - An idle Channel whose followers were never proven since a restart stays
    blocked until its next write or repair round. This is acceptable, because
    retention of idle data can wait, and can be verified by E2E.

### C. Floor-aware catch-up

Let a replica whose log is below the retention floor restart its log at the
floor (snapshot-from-floor), so trim never needs every ISR member.

- **Pros:** removes the coupling entirely.
- **Cons:** a replication protocol change. Needs its own design and approval.
  Out of scope.

## Recommendation

Implement **B**. Keep A's behavior as the fallback when no progress accessor
is available, which matches legacy mode.

## Compatibility, rollback, risk

- **No incompatible change:** no persisted-data, wire, API or configuration
  change. The retention responses keep their shape, and blocked reasons stay
  the same set.
- **Rollback:** direct. The previous version simply blocks again.
- **Data-loss risk:** B only enables trims that every ISR member has proven
  durable, which is stricter than HW. The risk to review is an over-reported
  proof. Progress is taken only from exact durability proofs and recovery
  tails, never from desired placement.
- **Downtime:** none.

## Verification plan

1. Write failure cases first: unproven voter → blocked; lagging voter →
   blocked at its proof; restart → seeded from recovery tails; leader change →
   old leader's progress discarded with its authority.
2. Unit tests in `pkg/channel/replication` and `pkg/channel/reactor` for the
   cases above.
3. E2E: `TestThreeNodeManagerRetentionForwardsAndSurvivesLeaderRestart`
   passes on Linux nightly, including physical trim on the restarted leader.

## Open questions for implementation

- Confirm that `Install` recovery can return each responding peer's durable
  tail without an extra round trip.
- Confirm that repair-worker acknowledgements carry an exact durable position.
