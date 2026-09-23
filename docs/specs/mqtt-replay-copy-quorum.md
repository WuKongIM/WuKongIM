# Current-membership MQTT replay copy confirmation

The cluster copier prepares one bounded page on the current recovered leader,
then asks current ISR voters to independently derive the same immutable shared
prefix from their own committed protected originals. RPC 95 carries no message
bodies: exact leader/epochs/route, a membership digest, and before/after prefixes
bind each confirmation. A follower reads its existing durable checkpoint; the
request never advances HW. Already copied pages can be read without originals.
Partial existing coverage is continued within the original 256-row/16-MiB budget.

A successful receipt requires the current leader and MinISR distinct voters,
with MinISR a strict majority of the current ISR. Fresh Slot quorum/apply reads
bracket coordination and each receiver's storage work. The membership digest
includes exact Channel identity, epochs/route, leader, ordered replicas/ISR,
MinISR and status; writes under a fence are rejected. Lease refresh and logical
retention do not change the immutable copy identity.

The receipt is transient evidence of durable copies, not a replicated release
decision. No source frontier, original retention, Session cursor or subscription
readiness changes. Accepted anchors still need log replication before post-GC
imports, learner readiness or safe release can depend on them.

Each service admits four coordinators and four receivers with no waiting queue.
Each coordinator uses at most four registered, joined workers, with a five-second
deadline. Quorum completion cancels outstanding requests and joins the cohort.
The receiver bounds all local copying and closes its store lease on every exit.

## Failure inventory before implementation

1. Duplicate/zero/learner votes, a non-majority MinISR, unavailable leader, changed
   membership or stale authority yields a receipt; cache-only reads hide isolation.
2. Receiver mistakes requested coverage for committed HW, copies uncommitted or
   foreign-incarnation content, accepts a different full-content digest, or leaks
   its lease on failure/panic.
3. A partially copied prefix loops forever or prevents progress; malformed empty,
   gapped or excessive pages escape the total row/byte bound.
4. Canceled work, saturated admission, failed/foreign acknowledgments or changed
   post-work metadata produces success. Slow peers hold a proven quorum hostage,
   or unjoined workers retain memory after return.
5. RPC accepts changed request echoes, wrong serving nodes, unknown versions or
   statuses, truncation/trailing bytes or oversized frames. Gateway replacement
   and Node maintenance accidentally bypass fencing.
6. Real followers differ in content, lose copied rows across restart, or originals
   are released by a copy-only operation. Tests must distinguish this from full
   product MQTT acceptance and post-GC replica import.

Tests precede implementation: deterministic service/codec failures plus real
three-node TCP/disk integration with 256 hash Slots. Product listener remains
unavailable until the remaining approved delivery and recovery paths are wired.

## Frozen context

Source `736e46d7d`; SHA-256:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `pkg/cluster/FLOW.md`: `64a7a708e782daf6cd36bc140f8b5f8df1f3a8e10b8c2573ed9acd78a92fb08e`
- `pkg/channel/FLOW.md`: `13a3c08d3e13d386b656ec3a89ede3863de0b89be703f22446c713c8f4a312d7`
- `pkg/goroutine/FLOW.md`: `49006c50ddf7890114aa7048ea8a555318c8cf8185cd418ae302f6b55c7e3fbb`

## Commit propagation found by the three-node acceptance test

The last quorum append leaves followers durable at LEO 4 but checkpoint HW 3;
without another append they cannot confirm shared coverage through 4. The copy
receiver must keep rejecting that state. A leader-only native replication hint
will resubmit its own committed tail proposal through the existing bounded repair
owner, with HW taken from its installed quorum sequencer. It accepts exact full
Authority, never a caller-supplied committed position. This is on-demand work for
copy confirmation, not another broadcast added to every native append.

Additional failure inventory before this fix: missing/unready/released or changed
native authority, a write fence, cancellation, a tail that does not cover the
sequencer's HW, duplicate/self targets, or a requested frontier mistaken for
committed truth must never enqueue a commit refresh. Admission queues remain
owned by the existing repair runtime; scheduling itself is not durability proof.
