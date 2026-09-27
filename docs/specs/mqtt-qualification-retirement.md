# MQTT ended-lifetime qualification retirement

## Failure inventory before implementation

1. Ended or replaced Session lifetimes retain UID inbox qualification forever in
   candidate/recovery indexes. Discover UID keys through the existing bounded
   consumer cohort, then project explicit termination and retain a tombstone.
2. Offline time, an expired-looking lease, absence, RPC failure or normal pending
   unsubscribe is mistaken for lifetime ending. Only a fresh authoritative ended
   parent or strictly newer Session generation permits this path.
3. Progress/removal accepts unrelated or partial point replies, wrong identity,
   UID, regressed revisions, future generation, or fabricated release proof.
   Read envelopes and exact keys must be checked before a source-owned CAS.
4. A UID row is treated as a Channel consumer: it gets cursor/accounting work,
   fabricated source protection or completion, or releases Channel/shared content.
   UID termination preserves discovery/drain fields and keeps protection zero.
5. A lost close/removal reply erases responsibility or causes resurrection. Use
   existing Preparing/Active -> Removing -> Removed transitions, reread authority
   on every turn and retain tombstones; CAS conflict yields without following keys.
6. Clean Start races with old cleanup and removes the new lifetime. Every queued
   hint, read and CAS retains the original Session/subscription generations.
7. Existing normal unsubscribe drains unfinished sources, but this new path
   silently declares their drain done. Explicit lifetime ending may supersede
   unfinished draining; it must not erase or invent its persisted checkpoint.
8. Runtime skips UID work, unbounds discovery or labels identities. Reuse the
   current cohort and recovery pages; add one fixed qualification-removal outcome.
9. Product acceptance observes only socket behavior or a mocked qualification.
   Real single-node/three-node clusters must observe background retirement after
   zero expiry, positive expiry or Clean Start, then verify new inbox delivery.

This path does not claim normal pending-unsubscribe recovery, source/cursorless
cleanup, metadata reclamation, owner isolation or content GC. Those obligations
remain separate; UID candidate removal alone cannot discharge Channel bindings.
No durable table/index/command/RPC format change is required.

## Implementation

The existing consumer scheduler now dispatches both Channel and UID recovery
keys through the same bounded cohort. UID turns call SourceProgress and
SourceRemoval; they never call Accounting, create a cursor, acquire a socket
owner scope, or send a message.

SourceProgress reads the exact UID binding and current Session through fresh
Slot authority. Identity, UID and nonregressing revisions must match. The old
lifetime is closed only if the parent is explicitly Ended or has a strictly newer
Session generation. A first CAS changes Preparing/Active/Removing to Removing
with `SessionEnded` and the observed parent revision. An independent removal turn
rereads both authorities and commits Removed. UID qualification carries no
Channel protection acknowledgement; its zero protection field remains zero.
Discovery, unfinished drain checkpoints and the original exact key are preserved.

Every turn has bounded point reads and at most one CAS per component. A consumer
turn may compose both commits; neither is a cross-Slot transaction. Monotonic
lifetime ending and exact source revision fencing make a concurrent new lifetime
safe. Lost replies resume from durable state, and Removed tombstones prevent a
late preparation from resurrecting old qualification. The parent, subscriptions,
Channel bindings, cursors, exchanges and shared content are not deleted here.

The fixed `qualification_removed` event counts confirmed Applied UID removals;
it is separate from Channel `removed` and ACK `projected`. A committed removal
whose reply is lost may not increment the counter. Metrics are observations,
never completion authority or a unique-lifetime census.

## Verification

The preimplementation process case waited for old qualification retirement after
zero-expiry disconnect and failed. Runtime discovery tests observed skipped UID
keys, and fixed-metric tests observed the missing qualification outcome. New
lifecycle contracts cover Preparing/Active/incomplete drain, explicit ending,
new lifetimes, lost progress/removal replies, preserved Channel debt, offline and
past-lease nontermination, and failed/malformed/cancelled reads and writes.

The six real-process cases passed (116.705 s) in 256-hash-Slot single-node and
three-node clusters: zero-expiry disconnect, positive expiry and Clean Start.
Each observes old retirement, establishes or preserves a new inbox lifetime,
then receives the first message from a newly created person source.
The full usecase/runtime/metrics/app race suites passed (120.039/1.848/3.238/5.265 s)
and the bounded-cohort joined-stop integration race passed (1.532 s).
`flow-doc-contracts` retains 87 compliant files, zero invalid and nine existing
length warnings. Reproduction commands, frozen inputs and embedded assertions
are recorded in the [evidence report](../reports/mqtt-qualification-retirement.json).

The strengthened quota/completion process regression passed (209.090 s), waiting
for both Channel and old UID removal before reconnect. Persistent Session,
cross-node takeover, graceful restart and TERM regression passed (122.136 s).
These process suites observe public protocols/metrics only; independent Channel
obligation preservation and malformed/lost evidence are covered at real metadata
usecase seams, not claimed as injected process failures.
