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

CPU evidence failures still retain completed ACKs/window timestamps and cancel+
join ownership sampling. Timeline query/validation failures retain all safe
partial replies, explicit failed-node markers and continue the fixed policy/
history controls. A decoded-key check rejects sender/payload/token fields even
with JSON whitespace or escaped keys; unsafe replies are never retained.
`WK_E2E_PERMISSION_TIMELINE_FAILURE_PROBE=1` selects
`TestPermissionTimelinePartialFailureReceipt/same-slot-remote` with timeline and
report options above. It intentionally injects HTTP 503 on the second diagnostics
query and exits failed; its outer receipt verifier must require all 64 ACKs,
64 three-node outcomes, one explicit failure and exact completed history.

`TestPermissionSequentialDiagnostics/same-slot-remote` requires exactly one of
`WK_E2E_PERMISSION_TIMELINE_SEQUENTIAL=1` or
`WK_E2E_PERMISSION_SEQUENTIAL_PROFILES=1`, a frozen binary and the baseline or
cohort report path above. Run with `-count=1 -timeout=4m -p=1 -v`.
The timeline mode queries the 64 sequential SENDs instead of the burst; all
existing identity, stage, response-size and history checks still apply.
The profile mode uses a separate 256-SEND sequential phase and joins four-second
CPU/allocation captures at all three owned nodes. Each HTTP request is bounded
to eight seconds and 8 MiB. It records request/return bounds, safe failures and
all 417 committed messages. `GODEBUG=memprofilerate=4096` applies throughout this
diagnostic fixture. Endpoint request time does not certify sampling start or
complete traffic overlap. Use identical settings for old/new diagnosis; neither
profiled nor traced windows qualify performance. Preserve outer coverage-test
failures separately from the original experiment's functional verdict.

`WK_E2E_PERMISSION_SEQUENTIAL_NO_WAIT=1` adds a minimized diagnostic assertion
to sequential timeline mode: all 64 ingress permission spans must average less
than 800 microseconds, detecting the former forced 1-ms collection floor. This
assertion is separate from the three unprofiled old/new p99/CPU pairs.

The Issue #977 fixed-arrival diagnostic is separate and opt-in:
`WK_E2E_PERMISSION_FIXED_LOAD=1 WK_E2E_BINARY=/absolute/frozen/wukongim WK_E2E_PERMISSION_CPU_PROBE=/absolute/calibrated/permission-cpu-darwin WK_E2E_PERMISSION_FIXED_REPORT=/absolute/fixed.json GOWORK=off go test -tags=e2e ./test/e2e/message/send_ban -run '^TestPermissionCallerFixedLoad$/two-slots-one-remote-leader$' -count=1 -timeout=8m -p=1 -v`.
Set `WK_E2E_PERMISSION_FIXED_COHORTS=1` only for candidate count assertions;
`WK_E2E_PERMISSION_FIXED_RUN_LABEL` records the predeclared A1/B1/B2/A2/A3/B3
run label. Initially measure only the same-remote-leader placement; run the
original four-layout functional modes separately. Keep tracing/profiles disabled.

Each window offers arrivals for 30 seconds and has a fixed two-second drain:
750 sequential arrivals at 25/s, or 468 bursts of 32 at 64 ms intervals, exactly
14,976 arrivals and 499.2/s with floor rounding. One persistent unqueued worker
per connection retains every ordinal. Busy or at least one interval late work
is a recorded drop; unfinished/error/drop/late work fails the protocol. Never
retry, catch up, add workers or extend the drain to manufacture success.

Keep the original full before/after node cuts and native Darwin cumulative CPU
probe. Record exactly 32 full ingress scrapes at offsets 0.5s through 31.5s,
with identity encoding and a 250 ms deadline from each scheduled offset; preserve
response byte count/hash, status, start/finish, misses and errors. Raw CPU cuts
include background and observer work without subtraction. Query/window drift
of 250 ms invalidates the fixed protocol. Preserve ownership bounds, all joins,
750/14,976 raw ACKs, 32 ban and 32 unban controls, and complete history with a
predeclared 158-page bound and hard 200-page cap. Retain all failures and source,
binary, config and process identities. This diagnostic supplements spec section
11 and never changes historical 64-SEND receipts or their acceptance verdict.

The separate v2 fixed-arrival diagnostic is opt-in:
`GOMAXPROCS=4 GOGC=100 WK_E2E_PERMISSION_FIXED_LOAD_V2=1 WK_E2E_BINARY=/absolute/frozen/wukongim WK_E2E_PERMISSION_CPU_PROBE=/absolute/calibrated/permission-cpu-darwin WK_E2E_PERMISSION_FIXED_V2_REPORT=/absolute/new-v2.json GOWORK=off go test -tags=e2e ./test/e2e/message/send_ban -run '^TestPermissionCallerFixedLoadV2$/two-slots-one-remote-leader$' -count=1 -timeout=8m -p=1 -v`.
Use a clean inherited product environment (no ambient `WK_*` settings), fixed
Go runtime defaults and the same external product/probe pins for each pair.
Candidate count assertions still require `WK_E2E_PERMISSION_FIXED_COHORTS=1`.

V2 preserves the exact 750/14,976 arrivals and fixed 32-second CPU windows, but
each connection has one worker and an eight-entry queued FIFO. Scheduler lateness
of one interval, queue saturation, scheduled-to-worker delay reaching 400ms,
failed ACKs or unfinished work fails the whole window. No retry/catch-up or
window extension is allowed. Keep every ordinal and monotonic offered/worker/
completion offsets. The full-population scheduled-to-completion P99 includes
queue residence; successful-subset P99 cannot qualify a failed population.

Keep actual monotonic native-query boundaries strictly below 20ms and native
inner/outer spans in [32s,32.04s). Full-node CPU includes all background/observer
work. Exactly 32 full identity responses retain raw bytes plus monotonic network
offsets; full parsing, hashing and exclusive file writes occur after CPUAfter.
Retain actual process TOML bytes and canonical copies; normalize only explicit,
exact fixture coordinates, preserving every substantive setting. Calibration,
ownership/fresh-read counts, ban/unban, complete history, joins and process
cleanup remain required. Same-binary AA controls precede candidate isolation;
failed controls prohibit causal qualification. Historical V1 and 64-SEND
failures and the existing 500 SEND/s qualification remain independent.
