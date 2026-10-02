# Will successor CAS and in-flight pressure recovery

Source revision: `423187b33b2784c2d79ffaff5621ee4c8629dabe`.
The operator approved continued implementation of successor-CAS fault acceptance
and independent journal pressure. Reuse Paho MQTT 5, public Product provisioning,
public Manager Slot observations, exact owned SIGKILL/restart and temporary-copy
loopback gofail. Do not read journal/Slot storage, fabricate CAS results or change
command bytes. Keep 256 hash Slots and 12 logical Slot groups.

## Failure inventory before edits

1. A second-generation reservation exists before its CAS is submitted; timeout
   or grant expiry is mistaken for a definite claim or permits its deletion.
2. A queued successor proposal executes late after pressure has erased its new
   reservation, making the original unrecoverable or reviving an old executor.
3. A persisted successor above the quorum commit index is reported as applied,
   or crash loses the exact reservation needed by later ordinary recovery.
4. A committed successor dies before FSM application and restart skips the
   committed suffix, miscounts follower applies or bypasses sealing/permission.
5. Independent ClientIDs fill the same node-wide journal while the proposal is
   in flight; absent/error/current/future or equal-generation foreign authority
   is interpreted as permission to reclaim an unresolved attempt.
6. A legitimate newer or different definite executor is rejected by an absolute
   quiet assertion, or old unknown authority is hidden by another node's claim.
7. Killing a process resets effect counters, hides a second business publication,
   or persistent reconnect changes original PacketID, message identity or DUP.
8. Pressure is claimed without reached refusal and an actual reclamation page
   on the captured executor before the selected proposal resolves.
9. A blocked original Slot prevents the pressure workload from starting; another
   node's spare capacity or pressure after FSM completion is credited as in-flight
   pressure on the old journal.
10. Fixture failures, ineffective negatives or unjoined workers are presented as
    passing product acceptance; bodies/credentials leak into retained artifacts.

## Acceptance slices

Start with a single-node cluster queued-successor tracer. A real first-generation
Started CAS is applied, and the existing pre-dispatch cut returns before effects.
Its normal ten-second grant expires. Capture the second-generation caller after
its exact reservation and before its real queued proposal; hold beyond its
five-second call and ten-second grant. Observe a healthy recipient for 21 seconds.
The unknown captured caller grants no publication. Restore the actual path,
then require one original business publication and a persistent unfinished QoS 1
replay with the same message identity, PacketID and DUP, without SUBSCRIBE.

Extend one verified cut at a time to queued and committed-before-FSM cuts in both
topologies, plus three-node-only persisted/uncommitted replication loss, each
with late completion and exact captured-executor crash/restart (ten cases).
Only second-generation Started commands are selected. Later definite claims may
publish, including a different executor's same-generation claim after leadership
change; require real validated applied evidence and retain the old unknown tuple.
Keep captured cap-two admission through the killed-process counter snapshot and
joined exit, then restore surviving nodes and let the replacement boot use
ordinary 1,024-record admission. Restoring captured admission before the snapshot
would allow pressure effects between the prefix and SIGKILL. A
full journal of unresolved records must fail closed rather than delete evidence
to fit the temporary cap-two experiment. No progress guarantee at a full
unreclaimable journal is claimed. Captured-node FSM counts and killed-process publication prefixes remain separate
from follower applies and restarted counters. Public commit/apply gaps corroborate
committed-before-FSM cuts; post-persistence markers require index above commit.

For independent pressure, pre-admit a bounded set of twelve different ClientIDs
before the cut, then abort their transports while the successor remains in
flight. They use the ordinary executor and the captured node's cap-two journal,
which contains the older sealed record and the successor reservation. Require
actual refusal and a reached real reclamation page on that node before resolution.
Fresh reads may retain the current/unknown successor; read errors retain it too.
Use observations at actual read/decision points, never synthetic authority.
Queued pressure cuts use a 90-second selected pause; persisted/uncommitted cuts
hold matching Append replication until explicit recovery or joined kill.
A separate 110-second public
completion observation allows that already-selected sleep to finish after its
control is disabled. Ordinary startup/restart readiness uses a separate
60-second observation: a prior 30-second setup expired with every fault counter
zero while a node completed startup at 29.256 seconds. No retry is added; this
qualifies no startup latency SLO, and the original
execution/grant/healthy observation bounds remain unchanged. Restore ordinary
admission after the in-flight pressure window, retaining every uncertain record.
Pressure work on another node may publish using spare capacity, and fresh strictly
newer/terminal evidence may retire eligible tuples. Neither grants authority to
erase the selected unknown reservation. Test single-node queued and three-node
persisted/uncommitted late/crash paths (four additional cases). Finish all pressure
receipts, then prove the deliberately unfinished original reconnects exactly once.
Paho manual acknowledgements flush in receive order: withholding the original
also holds pressure ACKs received after it. Track that exact unfinished pressure
set, keep a healthy three-second pre-close window for preceding ACKs, and require
every held exchange to replay with its original identity, PacketID and DUP on
the second reconnect. ACK the original and all held exchanges before the strict
15-second healthy quiet assertion. Known QoS replay cannot count as a second
business publication; the process-epoch effect total must still be thirteen.
Before the first reconnect, independent nodes may still send pressure originals.
That ACK observation accepts only the admitted pressure set, requires a healthy
three-second gap within a fifteen-second total bound, and still rejects the
unresolved original. This completion stage grants no authority to the captured
unknown caller and does not relax final quiet.

A private unsafe-reclamation control must fail a real business assertion after
reached pressure. Keep initial instrumentation gaps and failed fixtures distinct
from product RED. Emit bounded body-free JSON after joined clients/processes,
retain source/binary/instruction hashes and repeat commands, run relevant existing
race/regression/FLOW gates, review Standards and Spec, and commit locally.

This does not qualify failed authority-read fairness, lost/corrupt journals,
full issued-effect terminal recovery, general/asymmetric partitions, aggregate
shared-storage admission, latency SLO, sustained throughput or complete MQTT.
