# Channel Durable Quorum Log Design

**Date:** 2026-08-15
**Status:** Approved for implementation
**Scope:** Replace the Channel hot replication path with leader-planned,
parallel, quorum-durable log commits.

## Decision

Channel appends will move behind one deep `pkg/channel/replication` module.
The module assigns the exact log range before physical I/O, starts the local
synchronous write and data-bearing follower writes concurrently, and returns a
receipt only after the identical proposal is durable on the leader and a
metadata-defined write quorum.

The ordinary append API has no local-only commit mode. A single-node cluster
uses the same algorithm with a one-voter quorum.

The existing hot sequence is replaced, not wrapped:

```text
leader durable append
  -> PullHint
  -> follower Pull
  -> follower durable apply
  -> follower Pull with AckOffset
  -> leader HW / SENDACK
```

becomes:

```text
reserve exact range
  -> leader durable append || data-bearing follower append
  -> local durable + matching durable quorum
  -> contiguous HW / SENDACK
```

Pull/fetch remains only for bounded repair and catch-up. `PullHint` and
offset-only hot acknowledgements are deleted after the new path is wired.

## Evidence

The 2026-08-15 local three-node 250 SEND/s run proved exact traffic and zero
correctness failures but still failed hot latency. Increasing the shared DB
commit collection window from 200 microseconds to 500 microseconds improved
average hot latency by about 6.25 percent and reduced samples above two seconds
from 310 to 65, but left p99 in the two-second bucket. Physical commits fell by
only about 0.9 percent.

The remaining critical path contains two serial durable waits:

- leader store append: about 43--50 ms;
- follower apply/quorum: about 42--47 ms;
- Pull transport itself: about 2 ms.

Therefore another collection-window or Pull pacing change cannot remove the
dominant dependency. The protocol must overlap the two durable legs.

## External Interface

The operational surface is intentionally small:

```go
type DurableQuorumLog interface {
	Install(context.Context, Authority) (Installed, error)
	Commit(context.Context, Proposal) (Receipt, error)
}
```

`Install` applies an authoritative Channel fence and hides activation,
recovery, suffix repair, and the current-term barrier. `Commit` hides sequence
reservation, local persistence, peer selection, quorum math, retry,
reconciliation, batching, and receipt publication.

```go
type AuthorityID struct {
	ChannelEpoch uint64
	LeaderTerm   uint64
	FenceVersion uint64
}

type Authority struct {
	Key        channel.ChannelKey
	ChannelID  channel.ChannelID
	ID         AuthorityID
	Leader     channel.NodeID
	Voters     []channel.NodeID
	WriteQuorum int
	WriteFence channel.WriteFence
}

type Proposal struct {
	Key       channel.ChannelKey
	Expected  AuthorityID
	CommandID CommandID
	Records   []channel.Record
}

type Receipt struct {
	Authority AuthorityID
	CommandID CommandID
	First     uint64
	Last      uint64
	HW        uint64
}
```

An exact retry of the same `CommandID` and content returns the same receipt.
Reusing a command identity with different content fails as a conflict.

## Durable Entry Identity

Before I/O the channel sequencer assigns a contiguous range and creates an
immutable proposal. Every durable entry carries:

- Channel epoch;
- leader term;
- log index;
- previous-entry term and hash;
- command identity;
- content hash;
- the existing message record.

Follower durability is certified by exact `(epoch, term, index, hash)` facts,
not by an offset alone. Primary rows, secondary indexes, entry metadata, and
the proposal manifest are one atomic synchronous storage mutation.

## Commit Invariants

1. Capacity is reserved before any index is assigned. A pre-admission rejection
   writes nothing and consumes no sequence.
2. One sequencer owns each Channel. Accepted proposals receive non-overlapping,
   contiguous ranges in admission order.
3. The same proposal starts local synchronous storage and bounded follower
   pushes in the same scheduling turn.
4. A receipt requires the exact proposal to be durable locally and on at least
   `WriteQuorum` distinct current voters, including the local leader.
5. A later proposal may finish physical I/O first but cannot advance HW or
   publish a receipt before every earlier proposal is committed.
6. Visibility is prefix-only. Durable entries above HW are uncommitted and are
   never returned by committed reads.
7. Every completion is fenced by Channel, authority, command, range, and hash.
   A stale completion cannot mutate the current term.
8. Caller cancellation stops waiting, not admitted durability ownership.
9. Queue items, bytes, I/O concurrency, peer batches, repair pages, and retained
   proposals are bounded. There is no goroutine or timer per loaded Channel.
10. Replica work is proportional to the replica count, never group membership;
    a 100,000-member group still commits one Channel log proposal.

## Storage Seam

The local-substitutable store adapter exposes synchronous exact mutations,
inspection, and recovery-only suffix replacement. It does not expose a
caller-selectable `Sync` flag.

```go
type ReplicaStore interface {
	Load(context.Context, LoadBatch) (LoadBatchResult, error)
	Sync(context.Context, MutationBatch) (MutationBatchResult, error)
}
```

Each item carries the exact expected previous entry and range. Results classify
`Durable`, `AlreadyDurable`, `DefinitelyNotWritten`, `Conflict`, or
`OutcomeUnknown`. Exact replay is successful without creating another row.
Same range or command identity with another hash is a conflict. Adjacent
same-Channel items and different Channels may share one physical group commit,
while retaining per-item results.

The production adapter uses MessageDB/Pebble. Deterministic tests use a crash
store that can reopen at every atomic boundary.

## Peer Protocol

The remote-owned seam is one bounded, versioned exchange protocol. Normal
traffic uses data-bearing `Replicate`; recovery uses `Probe`, `Fetch`,
`CommitFrontier`, and `Control` items. Items from multiple Channels to the same
node may share one transport batch.

Follower rules:

- accept only the current persisted authority;
- accept an exact next range or exact idempotent replay;
- return `NeedFrom` for a gap;
- return conflict for a different entry at the same term/index;
- acknowledge only after synchronous durability.

Unknown Channel metadata is never accepted from a peer as authority. It only
triggers a bounded authoritative Slot metadata resolution.

## Failure And Recovery

Once any write may have occurred, a raw error is not a definite failure.
Admitted timeouts and ambiguous store/RPC results return typed
`OutcomeUnknown`; an exact retry converges on the proposal manifest.

- Local success, no quorum: retain the uncommitted proposal and retry peers.
- Follower success, local failure: do not acknowledge; retry/reconcile the
  exact local proposal while the authority remains current.
- Lost response: exact replay proves follower durability.
- Quorum durability before a lost client response: retry returns the same
  receipt.
- New authority: fence old admission, persist the higher term on a quorum,
  probe an election quorum, and recover before becoming writable.

Recovery selects the greatest identical prefix proven by an intersecting
quorum. It never selects a tail merely because one replica reports the highest
LEO. The new leader repairs its local copy, writes a quorum-durable current-term
barrier, and only then becomes ready. Minority-only uncommitted suffixes may be
truncated. A conflict at or below a quorum-certified committed cut is corruption
and blocks readiness.

Recovery streams fixed-size identity pages under the caller's bounded recovery
deadline. Page memory, voter fanout, and local/remote execution are bounded,
but recoverable log distance has no static page-count ceiling; retries therefore
make progress for an arbitrarily long durable prefix instead of restarting at a
permanent limit. They resume from a compact continuation bound to the unchanged
frontier, proven prefix identity, and its exact supporter set; any available
supporter quorum can resume, failed voters leave subsequent page fanout, and a
later incomplete page preserves the preceding proof. Every peer probe response
is bound to its exact Channel, participants, and requested positions before it
contributes recovery evidence.

Membership changes require an intersecting joint quorum or an explicit durable
cutover proof. Arbitrary non-intersecting `MinISR` configurations are invalid.

## Performance And Boundedness

For three voters and quorum two, the critical path becomes approximately:

```text
max(leader synchronous commit,
    fastest eligible follower synchronous commit + transport)
```

The third follower catches up under a bounded lag/age budget. Follower choice
may adapt to health, but adaptation may not reduce quorum, skip sync, weaken
fences, or reorder a Channel.

The existing 500-microsecond MessageDB commit coordinator remains the only
timed storage collection window. Peer batching may drain work already ready for
the same target but adds no second latency timer.

Parallel fsync cannot defeat one physical disk's lower bound. Local Mac results
must therefore be followed by the same Scenario Profile on independent node
storage before a formal capacity claim.

## Implementation Slices

1. Add the deterministic commit-round test: local and follower durability both
   start before either is released; follower-only completion cannot return a
   receipt; local plus matching quorum can.
2. Add exact-offset MessageDB mutations and crash-store conformance tests.
3. Add persisted term/hash manifests, exact replay, and typed ambiguous outcomes.
4. Add the data-bearing follower push and bounded per-target batching.
5. Wire the reactor through `DurableQuorumLog.Commit` and remove hot PullHint,
   Pull/AckOffset, and displaced quorum waiter logic.
6. Add authority installation, quorum probing, suffix repair/truncation, and
   current-term barrier crash matrices.
7. Repeat focused/race/integration gates, then the exact local three-node
   diagnostic and an independent-storage Simulation Run.

## First Deterministic Test

The first test uses a manual executor and gated store/peer adapters; it has no
sleep or wall-clock threshold:

1. install a ready three-voter, quorum-two authority;
2. start one commit;
3. observe both local and follower durable operations start before releasing
   either gate;
4. release the follower and prove the commit remains incomplete;
5. release local durability and prove one receipt with `HW == Last`;
6. replay the same command and prove the same range with no duplicate rows.

The current architecture deterministically fails step 3 because it does not
expose indexed records to followers until local store completion.
