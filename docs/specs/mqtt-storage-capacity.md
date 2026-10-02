# MQTT aggregate storage capacity

Status: implemented and qualified within the finite recorded acceptance bounds.
Evidence: [capacity acceptance](../reports/mqtt-storage-capacity/README.md).
Source revision: `1aff256b2bd9bc71a69cf5e365553357a69c3bc9`.

## Authorized scope and test seams

The operator approved node and cluster aggregate shared-storage admission,
preservation of accepted responsibilities, safe release and restart recovery.
Reuse the approved process-level Paho, WKProto, public HTTP, public fixed
metrics and joined process-restart seams. No storage inspection belongs in E2E.
Start with two individually compliant offline Sessions on distinct sources.

The proposed cluster meter counts retained replica content. Reserve the
source original and its future canonical replay representation before new
durable publication; transferring between them must not require additional
unreserved capacity. One source body shared by many Sessions consumes one
reservation on each replica, not one per subscriber. Physical WAL, compaction,
ordinary history and metadata amplification require separate disk headroom.

## Failure inventory (before implementation)

1. Individually compliant Sessions on different sources jointly exceed a
   node or cluster budget; a new durable send must not succeed.
2. Concurrent sources or nodes observe free space simultaneously; aggregate
   reservations must serialize at their authoritative durable boundary.
3. MQTT, WKProto, Product HTTP, Will and forwarded sends must share admission;
   checks only during asynchronous replay copy are insufficient.
4. Duplicate publication/proposal/copy charges twice, or a retry with different
   content consumes another responsibility; retain exact idempotency semantics.
5. Caller cancellation, lost allocation reply or unknown physical commit
   incorrectly returns space; uncertain reservations remain charged.
6. Exhaustion prevents copy, anchor, ACK or retirement of already accepted
   content; fund its replay representation before acceptance and retain
   bounded maintenance admission independently of new business publications.
7. ACK, Session expiry or unsubscribe releases shared content while another
   consumer still needs it; only existing proved retirement grants release.
8. A replica is full while a quorum accepts new content; do not fabricate
   that replica's capacity or erase accepted debt. Qualify admission and repair
   against the complete storage placement.
9. Restart resets node use, duplicates a cluster grant, or refunds an unknown
   allocation; reconstruct use and recover the exact durable grant.
10. Backup/restore, replica transfer, suffix replacement or retention changes
    bypass accounting. Preserve or rebuild canonical accounting before reopening.
11. Existing protected data predates counters, exceeds a lowered limit, or has
    corrupt/missing evidence; preserve data and close new admission until
    bounded recovery proves its charge.
12. Stale placement, unavailable Slot authority, mismatched budget configuration,
    arithmetic overflow or malformed replies grant space; all fail closed.
13. Global allocation becomes one synchronous replicated command per message,
    or memory grows per Session/message; use bounded node grants and durable
    source accounting, with low-cardinality capacity observations.

## Acceptance

First reproduce the two-source exhaustion through the public send receipt.
Then qualify independent node and cluster ceilings, concurrent sources,
accepted replay after joined restart, completion and reopened admission, and
one shared body across multiple clients in single-node and three-node clusters
with 256 hash Slots. Keep failed prerequisites distinct from product RED.

## Placement prebooking protocol (design before implementation)

The three-node RED showed that per-store checking leaves one partly written
original when the cluster budget cannot cover all replicas. Merely requiring
all replicas before returning HW would retain that business proposal and block
same-source anchors/retirement, preventing existing debt from freeing space.

Use a separate storage-funding prepare/cancel operation on the existing bounded
peer exchange path before dispatching any original mutation. It carries the
exact sealed manifest/content and captures all voters plus non-voting learners.
Funding acknowledgements are separate from durability votes. Each Channel keeps
one bounded durable ticket with a monotonic nonce and exact manifest; charge
receipts are written atomically with preparation. Cancellation persists the
nonce floor before returning any credit, so delayed preparation cannot resurrect
it. Original writes consume only an exact active ticket and never charge twice.
A failed/unknown preparation does not occupy the original sequencer's pending
proposal; existing maintenance may progress. Unknown cancellation retains debt.
New funding cannot replace unresolved tickets without exact cancellation or
canonical proof that their original range is occupied by another proposal.
Restart preserves and validates active tickets, and reconstructs their charges
alongside originals/replay. Node escrow remains chunked rather than issuing
one cluster allocation per publication. Tests must prove all-replica funding,
no partial original on capacity refusal, cancellation fencing, restart and the
same-source maintenance progress cycle.

## Definite Will funding refusal (design before repair)

A full-capacity Will currently returns before original dispatch but its origin
journal has already issued submission permission. The process E2E verifies that
this prevents recovery after ACK/retirement releases capacity. Introduce a
closed non-submission error only at the fresh sealed proposal's funding failure,
never at retained/pending retries or any original durability outcome. Preserve
that typed evidence through the local cluster adapter and the closed node-RPC
error code; transport loss and malformed replies grant no evidence. After the
synchronous publication call returns this proof, the exact same-boot origin
journal can durably seal its issued attempt. Existing receipt-first recovery,
permission reread and successor CAS then own retry. Generic pressure, deadlines,
missing receipts and error text must never grant sealing. The origin router
retains uncertainty monotonically across the whole invocation, including route
refresh retries and storage sibling retries, and suppresses later negative
capability after any unresolved earlier submission.

## Restart maintenance classification (failure before repair)

The legacy-over-limit E2E resumes and ACKs the accepted body but the connection
then closes with EOF; replay accounting has advanced to 4 while completion
remains at 2. Bounded probes show the recovery barrier is admitted as consumer
content. Its deterministic timestamp may exceed the live outbound clock and
closes delivery, stranding retirement. Give newly written authority barriers an
explicit single-record native format 7, validated independently from business
flags/payload spelling. Consumer reads classify only this committed format as
internal maintenance. Ordinary SyncOnce business records remain ordinary and
must be charged, whereas explicit source/anchor/retirement/barrier controls do
not acquire body capacity. Existing untyped format-1 historical barriers cannot
be relabeled from payload spelling alone; preserve their evidence fail-closed.

### Duplicate and cancellation completion evidence

A pre-dispatch funding refusal must still enter the existing positive committed
idempotency lookup. The negative capability applies only to this invocation's
new original dispatch; it cannot invalidate an earlier committed duplicate.
Both batch recovery paths therefore admit `ErrAppendNotSubmitted` for positive
lookup, and retain the refusal on a miss.

The physical cancellation fault reproduces a leaked volatile debit even though
the durable canceled floor was written. Move this uncertainty into the existing
one-per-node pending refund owner. Under its mutex, an exact canceled ticket and
absence of every charge in that proposal range prove the atomic cancellation.
No timeout, absent original alone, newer ticket, or process eviction refunds it.
Different unresolved physical refunds block replacement of that one proof.

Unknown preparation debt also belongs to the node: store handles may close and
be reclaimed before the funding owner's cancellation opens another handle.
Serialize the short physical preparation under the same bounded proof owner.
An exact durable prepared/consumed ticket with matching charge witnesses transfers
uncertainty to durable ownership without refund; an exact canceled floor plus
charge absence refunds once. No absence alone or handle cache grants credit.

The closed escrow row supports at most 1,024 historical nodes. Its symmetric
256 KiB encoded row limit covers maximum-width uint64 grant/member fields plus
the envelope; encoding checks the limit before committing. The former 128 KiB
reader-only bound could reject an otherwise valid writer output.

The three-node refusal initially lost its proof at strict RPC decode because
an empty error result had the zero reason, which equals Success. Encode that
empty negative result with the system-error reason and no identity; retain and
reject any actual contradictory success identity. The closed negative codes
also preserve bounded pressure versus retryable dependency readiness, so a
post-restore follower gap remains HTTP 503 retry-required. No text supplies proof.

A public same-ClientID reconnect fails after definite capacity refusal because
MQTT marks every Send error as unresolved owner work. Consume only the typed
whole-invocation capability with no message identity to avoid adding that
uncertainty. Keep closure/error mapping and all generic/ambiguous errors intact;
the scope must never erase uncertainty from another operation.

### Periodic cancellation lock failure inventory (before repair)

An unresolved prepare/cancel can make the node health owner acquire a Channel
whose foreground append or checkpoint mutation is still running. Context expiry
does not interrupt a Mutex.Lock, so waiting there can stall later health reports
and hold restore ownership. A competing node-budget physical commit can cause
the same wait after Channel admission. Periodic retries must skip each busy lock,
retain the exact charged proof and preserve append -> checkpoint -> budget lock
order. Cancellation identity/original checks and definite atomic refunds remain
unchanged. A focused integration test may hold these actual locks directly:
this isolates owner admission, which public process receipts cannot deterministically
synchronize; process capacity fault tests continue to qualify settlement.
Slow fsync is also not interrupted by a caller context. Transfer the bounded
exact cancellation to the existing managed commit coordinator with canonical
Channel pins/locks retained through its finalizer. The health caller may time
out, but debt remains until the canceled floor and charge deletion are proved.
Queue/build refusal and unknown physical completion retain the original proof;
there must be no direct synchronous-commit fallback in this periodic path.
