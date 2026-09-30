# Send ban E2E

Prove independent user and source-Channel send restrictions through real processes,
HTTP and WKProto in 256-Hash-Slot single-node and three-node clusters with
12 initial physical Slots. A separate three-node, one-replica scenario proves
non-replica ingress never reads a missing local policy as an allow.

Run: `GOWORK=off go test -tags=e2e ./test/e2e/message/send_ban -count=1 -timeout=8m -p=1 -v`.
Set `WK_E2E_SEND_BAN_REPORT` to a JSON output path to retain the verdict and observations.

Keep assertions black-box. Cover cached admission, cross-ingress changes, both
person directions, group isolation, trusted identities, atomic metadata updates,
CAS and rejected-message absence. Never retry a ban rejection until it succeeds.

Verify actual leader placement through Manager HTTP before asserting RPC counts:
two distinct Slots on the same remote leader must use one node envelope; local
leader access uses zero RPCs while both Slots retain fresh reads. For quorum
failure, keep the current UID Slot leader alive and stop its two peers. Check
HTTP 503 and rejected request absence in complete committed history after recovery,
using a successful message as a positive history control; do not infer absence
from sequence density across leader changes.

The JSON artifact records timestamps, latency, topology and RPC observations.
Optional WK_E2E_SOURCE_REVISION and WK_E2E_SOURCE_FINGERPRINT identify the tested
source; the non-replica scenario writes a .non-replica.json companion artifact.
These functional timings are not a qualified throughput/latency benchmark.

Run the independent 100,000-member proof with
`WK_E2E_SEND_BAN_100K=1 GOWORK=off go test -tags=e2e ./test/e2e/message/send_ban -run TestHundredKGroupSendBan -count=1 -timeout=6m -p=1 -v`.
It requires the full public-API group setup, a successful fanout positive control,
fixed permission-fact counts and zero recipient processing for both ban scopes,
then exact committed-history checks after unban. Its `.100k.json` artifact must
remain failed if setup exceeds its five-minute budget; do not reduce cardinality
or treat incomplete setup as permission-scale evidence.
Its artifact retains the requested setup cardinality, observed successful
recipient-processing control and exact complete history. Initial three-node
readiness failures include bounded process diagnostics.

The main scenario also keeps two distinct devices of one UID connected before
ban and through unban, covering every additional declared source Channel type
3 through 12. Each type has HTTP and device positive controls before/after the
ban and an exact complete committed-history set excluding all rejected sends.

The independent-ban matrix records all four policy states and both unban orders
through every ingress, with writes rotating across nodes. Check both person
directions, another person target, both group senders and a second group. Its
`independent_ban_combinations` evidence includes policy versions, request IDs,
decisions and exact full histories with successful-message positive controls.

The `.priority.json` companion uses three nodes and one replica per physical
Slot. Public Manager placement must prove UID and Channel Slots have different
owners before stopping the Channel Slot's sole voter. The UID owner and a third
ingress must return the known user ban; clearing that ban must return HTTP 503
while the Channel authority remains unavailable. Recovery history contains only
the successful warm and recovery controls. This is a policy-priority fault
proof, not a claim that one-replica Slots remain available after voter loss.

The `.write-recovery.json` companion withholds the responses of real committed
policy POSTs through a bounded harness proxy. After caller timeout, verify both
policies through another ingress and reject retries carrying the old CAS
version. Eight rounds of concurrent Token, partial Channel metadata, member and
idempotent policy writers must preserve values and versions after each round.
Verify that the retained Token authenticates while banned and that complete
history after independent unbans contains only successful control requests.

The non-replica companion also warms a missing Channel, creates it with a ban,
and records missing/ban/allow transitions with a 1h auxiliary TTL. A trusted
device must first observe the Channel ban before testing unban in the same
device context; this isolates mandatory policy reads from auxiliary membership
caching. Complete history must contain exactly the two successful controls.

The `.delivery.json` companion keeps the receiver connected across both ban
scopes and loss/recovery of UID Slot quorum. Exercise HTTP and WKProto person
and group rejections, including NoPersist/SyncOnce, bounded empty reads, and
successful controls on both sides. Complete history must equal successful IDs.

The `.gateway-distributions.json` companion exercises one/many UIDs against
one/many Channels through actual WKProto bursts. Public gateway histograms must
prove multi-record batches, with per-request ACK decisions and exact histories.
Counter deltas include all related work in their public scope; do not interpret
aggregate Slot groups as only the mandatory policy reads or claim timing gates.

The `.network-admission.json` companion uses two voters per Slot, verifies
owner/non-replica placement, stops the other voter, and gates 192 HTTP SENDs.
Require remote envelopes, owner admission-busy evidence, at most 64 sampled
executing envelopes, drain to zero, all 503/zero identifiers and exact recovery
history. This topology is a controlled quorum-loss fault, not an HA claim.

The `.leader-transfer.json` companion keeps user and Channel bans closed
through separate public Manager Slot leader transfers. Record concurrent
three-ingress SEND outcomes, actual non-source leadership after task completion,
post-write unban/re-ban decisions at all ingresses, and exact complete history.
Preferred targets are not proof of actual Raft leadership. This test does not
claim to corrupt RPC replies or observe the internal retry instant.

The `.parallel.json` companion uses five processes and two disjoint two-voter
policy Slots, selected from actual public leadership. Stop each non-leader
voter and issue one SEND through the fifth, non-replica node. Prove overlap
with positive owner A / owner B / owner A inflight observations followed by
separate, unchanged direct admission-counter checks; potentially buffered
transport counters cannot establish continuity. Require two remote envelopes,
HTTP 503, drain, restored quorum and exact complete history with successful
controls. This proves real network overlap, not a throughput speedup or HA.

The management-audit companion uses a real authenticated Manager and backend
HTTP in a single-node cluster. Verify atomic previous/current values, no-op and
CAS conflict outcomes, explicit versus omitted legacy fields, verified Manager
actor and credential-free structured logs. Its `.audit.json` artifact contains
only selected audit events. The eight-minute package timeout covers all sequential
scenarios; individual scenario deadlines and assertions remain unchanged.
