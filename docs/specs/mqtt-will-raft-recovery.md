# Will CAS proposal, commit and apply recovery

Source revision: `dcaaaa8b315b1805ffef42d8bfbfcb1719edc4ca`.
The operator approved true uncommitted CAS crash/recovery acceptance after the
applied-reply qualification. Reuse real Paho MQTT 5, public HTTP provisioning,
Manager observations, owned process crash/restart and temporary loopback gofail.
No storage reads, fabricated CAS outcomes or imports of product internals.

## Failure inventory before edits

1. A queued Started proposal expires before entering RawNode but later runs;
   the original caller mistakes its old grant for publication authority.
2. A persisted leader entry without quorum commit is confused with an applied
   claim, or a missing receipt/expired grant permits another unsafe effect.
3. A committed Started entry has not applied to the FSM when the executor dies;
   restart skips the committed suffix or loses its exact reservation evidence.
4. An old queued/uncommitted proposal applies after a new execution decision;
   stale CAS or original grant revives a fenced business publication.
5. Pressure removes older/current/unknown reservation evidence before fresh
   terminal or strictly newer execution proof.
6. Executor crash, authority change or persistent reconnect duplicates original
   business identity, relies on SUBSCRIBE, or hides a closed recipient as quiet.
7. Broad faults stall unrelated Raft groups, change persisted input/consensus,
   or claim single-node uncommitted quorum replication that cannot occur.
8. Fixture workers/processes are not joined before bounded evidence emission.

## States and first tracer

Start with one queued-before-RawNode Started proposal. Its reservation already
exists; the accepted future waits independently of the caller's five-second
turn. Delay only the matched command beyond its ten-second grant. Observe the
healthy recipient for 21 seconds. The old unknown claim grants no publication.
Surviving quorum may elect a new leader and definitely commit a newer Started
execution while the old proposal remains queued. Only its real applied successor
CAS evidence permits one original publication in that interval. Preserve that
unfinished exchange through reconnect with the original PacketID/identity and DUP.
Without a definite successor, require zero actual publication attempts.
Restore the ordinary path or kill/restart the exact cut process, then require
one original business receipt without another SUBSCRIBE and a final 15-second
healthy quiet interval. Reconnect preserves the admitted persistent Session.
Use the same assertions for committed-before-FSM application in both topologies.

The pressure extension first publishes one separate terminal Will through the
same ClientID and observes exact cleanup failure. Cap two then retains that
terminal and the current reservation on the captured executor. Counter epochs
reset only after the first receipt and its cleanup proof. Every case requires
two distinct original business receipts in total. Late committed/unapplied,
persisted/uncommitted and single-node queued cases require an actual
same-executor capacity refusal after delayed FSM resolution, fresh terminal
reclamation and the retained current original. A three-node queued takeover
instead proves fresh successor authority and fences the old proposal;
all-node public convergence requires that the old paused worker resumes and
observes the new leader. Its log may be discarded without an FSM result.
A crash may change placement;
it cannot promise that a successor uses the old node's full journal.

Use 12 logical Slot groups, matching the root configuration example, and still
256 hash Slots. Verify the group count through public Manager convergence.
The first one-group pressure fixture closed the recipient during its quiet
window and is failed evidence. Multiple groups keep the selected proposal cut
from deliberately stopping the entire metadata runtime; healthy quiet remains
mandatory, with no renewal/packet/grant timeout change.

Committed-before-apply evidence concerns the captured authority node. Other
replicas may already have applied the same entry; do not aggregate their counts
as that node's application. Public leader commit/apply gaps must corroborate
the paused node. Preserve killed-process publication counter prefixes across
restart so one business publication cannot be hidden by resetting counters.

Three-node-only true-uncommitted cases drop outgoing MsgApp batches containing
the selected Started command while preserving normal Raft input, log persistence
and heartbeats. A marker fires only after successful persistence and only when
the entry index exceeds the current commit index. It is not a synthetic result.
Disable the bounded loss or kill/restart the captured executor and let ordinary
Raft and Will recovery decide the surviving command. This is exact command
replication loss, not general partition or asymmetric-link qualification.
A single-node cluster commits its own accepted proposal immediately; queued and
committed-before-apply cases are reported separately, never as uncommitted quorum.

Run one tracer before extending to the two-topology crash/late completion matrix.
Pressure retention needs its own reached refusal/read proof; quiet alone cannot
prove reclamation occurred. Pressure is qualified after FSM resolution, rather
than asserted concurrently with the still-unapplied command. Reclamation against
an older row while another independent workload pressures an in-flight proposal
remains open. A private negative control must fail an actual business assertion.
Emit bounded body-free JSON after joined clients and owned process cleanup.
Keep 256 hash Slots, fixed publication counts and original content identity.
This slice does not complete issued-effect terminal recovery or shared storage
admission, direct successor-CAS fault acceptance or failed-read fairness, and
qualifies neither latency SLO nor general network partitions.
