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

The independent Issue #977 characterization is opt-in:
`WK_E2E_PERMISSION_BASELINE=1 WK_E2E_BINARY=/absolute/frozen/wukongim WK_E2E_PERMISSION_BASELINE_REPORT=/absolute/baseline.json GOWORK=off go test -tags=e2e ./test/e2e/message/send_ban -run '^TestPermissionCallerBaseline$' -count=1 -timeout=4m -p=1 -v`.
Use 32 separate system-UID devices, 64 sequential SENDs and two waves of 32
independent callers per placement. One-voter metadata Slots prove non-replica
routing only; they do not qualify quorum cost or HA. Keep actual all-node
placement, topology fingerprints, binary/build and harness identities, per-node
metric cuts, exact envelope/Slot/barrier counts, ACKs, ban/unban controls and
complete More/sequence-paginated Product HTTP sync history for an offline
subscriber. Pin three message-data replicas independently of the one-voter
metadata fixture; a Slot owner is not proof of complete node-local message data.
Resource cuts describe whole nodes; missing
samples and queue bytes stay unknown. Do not infer a CPU bottleneck from counts.
`WK_E2E_PERMISSION_BASELINE_PROFILES=1` captures bounded CPU/heap profiles in a
separate 256-SEND phase, excluded from unprofiled latency. This baseline neither
implements cohorts nor changes/replaces the 500 SEND/s qualification gates.

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
owner/non-replica placement, stops the other voter, and paces 192 HTTP SENDs 5 ms apart beyond the cohort collection window.
Require remote envelopes, positive receiver or ingress-cohort admission-busy evidence, at most 64 sampled
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

The same fixed experiment has an opt-in candidate mode:
`WK_E2E_PERMISSION_COHORTS=1 WK_E2E_BINARY=/absolute/frozen/candidate WK_E2E_PERMISSION_COHORT_REPORT=/absolute/cohorts.json GOWORK=off go test -tags=e2e ./test/e2e/message/send_ban -run '^TestPermissionCallerCohorts$' -count=1 -timeout=4m -p=1 -v`.
It requires lower burst RPC/local-envelope and fresh-barrier counts, unchanged
sequential counts, independently drained public cohort ownership metrics,
completed ban/unban controls and exact full history. Historical baseline mode
remains executable with its frozen old binary. Source/build identity, whole-node
resource cuts and diagnostic limits remain the same; capacity gates are unchanged.

Fixed old/new comparison windows also sample the ingress ownership gauges every
20 ms (at most 200 samples, canceled/joined). Missing old metrics remain absent;
sampled peaks are lower bounds, and scrape overhead belongs to both comparisons.

The quorum-loss admission companion keeps 192 failed-closed sends but separates
arrivals by 5 ms so sealed cohorts actually accumulate. Require positive receiver
or ingress-cohort busy, record both scopes separately, sample both hard ownership
bounds and drain to zero, then verify exact complete recovery history. This fault
fixture never changes the 500 SEND/s performance gates.

`WK_E2E_PERMISSION_STAGES=1` optionally retains count/sum/bucket samples for six
fixed public permission, append/wait, replication and storage-commit histogram
families in the existing before/after node scrapes. It adds no scrape or product
hook. Keep full label identities and absent series; histogram bucket bounds
describe scoped completed populations, not per-SEND spans or time spent waiting
before stage entry. Use the flag identically for old/new comparisons.

`WK_E2E_PERMISSION_TIMELINE=1` is a separate diagnostic mode. Select only the
`same-slot-remote` subtest with the baseline or cohort command above. It enables
the existing 8192-event diagnostics ring and fixed 100% ordinary/deep sampling
(16 detail items per batch), preserving pkg/client pending/write-start/decode
and harness bridge-return times. Write-start is not socket completion or server
receipt. After the window, query exactly 64 requests at each of three nodes,
with a two-second/64-KiB/32-event bound per query. Reject truncation, foreign
request identities, exposed sender/payload fields and missing ingress queue,
permission, admitted-append wait, message or ACK stages. Node-local absent events stay explicit;
nested/shared spans are not added, and process wall-clock gaps are diagnostic.
Original products without `message.permission` intentionally fail stage coverage
while retaining functional ACK/policy/full-history results. An identically
instrumented clean old/new pair may diagnose that gap; traced latency does not
qualify p99/CPU or change the unprofiled gates.

On Darwin, prebuild `fixtures/permission-cpu-darwin.c` with
`cc -O2 -Wall -Wextra -Werror fixtures/permission-cpu-darwin.c -lproc -o /absolute/permission-cpu-darwin`.
Set `WK_E2E_PERMISSION_CPU_PROBE` to that path, run
`TestPermissionCPUProbeCalibration` first, then use the same binary for all fixed
old/new windows. It observes only three owned node PIDs via public OS counters,
preserves start identity, raw CPU ticks, timebase, cut times and source/binary
hashes, and fails unavailable/reused/regressing samples. The whole-node user+
system integral encloses the SEND window and bounded snapshot/scrape scheduling
overhead; compilation and profiles remain outside it. Retain all three pairs;
do not replace failed observations or present periodic gauges as CPU integrals.
