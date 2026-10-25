# V3 gate-v2 readiness

The current candidate is not ready for a complete v3 stable release. Gate-v2
repairs evidence collection and interpretation; it does not turn prior local
pressure observations into formal Linux qualification. Numerical latency,
explicit first-attempt failure, and message-correctness thresholds are retained.

## Evidence contract

Chat-lifecycle reports advance to `report/v7` and `thresholds/v2`; worker protocol
advances to v11 so an older worker cannot silently supply zero deadline counters.
Old reports remain archived, and the new reader rejects them as current capacity
continuation inputs. Original R30/R31 outcomes remain `operator_stop`.

| Evidence | Applicable conclusion |
| --- | --- |
| Local 10,000-online, 2,000-SEND/s pressure diagnosis | Explains this workstation's queues, storage waits, latency and correctness; not a cloud hardware or Linux capacity certificate |
| Existing Linux 500-SEND/s PR regression | Protects that fixed workload and its unchanged 400ms floor; does not certify the 2,000-SEND/s long workload |
| Rehearsal report | Requires measured latency evidence before `rehearsal_pass`; does not prove 72-hour resource stability |
| Formal-protocol report | Requires exact thresholds and 12/256/3/3 topology; still needs matching external Linux host/disk, source, Run/Fence and Lease evidence |
| Complete stable release | Also requires exact-tag publisher gates and signed stable APT/RPM publication with public exact-version installation verification |

Latency warmup is ten minutes, independently of the formal/rehearsal two-hour
resource warmup. The first valid cumulative latency cut becomes a baseline.
Positive evidence requires ten measured minutes, at least 1,000 observations in
each hot SENDACK, cold SENDACK and full-sync-attempt class, and a latency cut no
older than five seconds. The count floor resolves the p99.9 rank and is not a
statistical confidence assertion. Valid cumulative deltas belong to their end
cut; gaps longer than ten minutes restart positive evidence. Resource-only
observations, stale cuts and counter regressions cannot create qualification.
The numerical sustained-breach rule still uses five minutes. Short diagnostics
may execute while remaining insufficient; elapsed rehearsal time cannot pass.

Report v7 records baseline/start/end elapsed positions from one process-start
monotonic clock. Duration is the exact elapsed-position difference; ordering and
freshness also use elapsed positions. Wall timestamps remain audit fields after
JSON serialization. No tolerance, wall-clock reconstruction, old-report upgrade
or threshold change supplies missing evidence.

First-attempt reporting exposes known-deadline registrations, resolved original
results and confirmed observed-late timeouts. The three sets satisfy
`timeouts <= resolved <= deadlines <= first_attempts`. An original attempt's own
clocked ACK/error resolves coverage; a retry success does not replace it. Local
admission errors do not count as confirmed timeouts. Unknown clocks, missing
responses, cancellation and receipt expiry retain unresolved coverage. Always
show `deadlines - resolved`; a zero lower bound is not an all-attempts-on-time
claim. Explicit failures and retries retain their existing definitions and are
not added to the timeout count. No new timeout-rate release threshold is chosen.

## Release conditions after this change

1. Finish exact-source validation and review of the gate-v2 candidate, preserving
   first failed attempts and repeatable output artifacts.
2. Run the reviewed four-host Linux rehearsal with these effective latency
   windows. A local run, a profile label, or a fresh report schema is insufficient.
3. Complete the fresh 72-hour Soak and same-dataset aged capacity/recovery chain
   with correctness gates, resource trends, budget limits and zero-inventory
   cleanup evidence intact. No existing short diagnostic fills this requirement.
4. Before tagging, place release notes under the exact version and pass the
   unchanged exact-tag publisher gates. Complete immutable binary/image/docs
   publication and the signed stable native channel, followed by public clients
   selecting and installing that exact target version.

The live packages control checked during this task still has stable disabled
with `status=object_storage_required`. The public repository status remains
`target_version=3.0.0-beta.24`, audit Release `402342684`; it is not stable v3.
Public bootstrap/signing/client preparation merged earlier does not establish
that production stable publication is enabled or complete. See
[package control](https://github.com/WuKongIM/packages/blob/main/manifests/channels.json),
[public status](https://packages.githubim.com/status.json), and the repository
[release completion contract](../development/RELEASING.md).

No new paid Lease, production package publication, or release tag is started by
this evidence-contract change. Their existing explicit authorization and
protected publication requirements remain in force.

## First gate-v2 candidate observation

Commit `3e539c6db4c9082e4f0339bb9133c5bab034fcdf` passed the complete local bench
suite, related CLI/materializer suites, focused race, FLOW contracts and the
terminal collector's 24 process-fixture cases. Its Linux candidate
[default/scripts/migration and messaging run](https://github.com/WuKongIM/WuKongIM/actions/runs/40188426079)
passed. The three fixed Linux 500-SEND/s performance cases, Manager and native
package preview passed; the independent three-node correctness smoke failed and
is retained in [its original run](https://github.com/WuKongIM/WuKongIM/actions/runs/40188425986).
That launcher's exact-line time replacement still matched the old YAML, so the
new latency-warmup field prevented its requested 60-second timeline from being
materialized. It waited for the default 20-minute checkpoint. This integration
regression requires a config-artifact regression and repair; the original
failure is not a passing result or a reason to loosen thresholds.
Its worker evidence separately retains two same-logical-SEND generator-invariant
observations (worker1, SEND stage, values 1 then 0). The script timing mismatch
does not establish the cause of those runtime observations; that boundary still
requires a deterministic reproduction rather than ignoring it after shortening
the smoke back to its original contract.

A fresh native Mac original-scale diagnosis requested 1,220 measured seconds
with 10,000 target online users, three service/worker processes, 2,000 groups
including the 100,000-member group, original seed, payloads and numerical gates.
Its 58 complete periodic intervals covered 290.0032235 seconds at 2,000.019148
logical SEND/s. The driver terminated early with the native
`harness_invalid / worker_harness_invalid`, before the ten-minute latency
baseline; latency remained `not_evaluated`, not pass or a formal Linux failure.

Final drain completed all 592,006 logical SEND/ACKs and all 5,329 sampled delivery
correlations. Terminal/loss/duplicate/corruption/sequence and explicit product
first-attempt failures were zero. All 592,006 first deadlines were resolved;
19,691 original results were confirmed late and unknown coverage was zero.
These deadlines are hot P99.9=1s / cold P99.9=5s, not the 400ms hot P99 limit.
The 12,060 registered retries are a different quantity; do not replace the
explicit-failure gate's numerator or sum these overlapping counters.

Worker0 retained one CONNECT failure. Node1's sole pre-stop auth failure matched
its device and time, with `activation_route_not_ready` and 790.33825ms duration.
The UID maps to HashSlot 193 / physical Slot 10; sealed inventories report
node2 leadership without a measured leader change, strongly supporting a
remote-authority path. The failing request's exact target and source phase were
not recorded. The underlying recovery/register/commit cause and any RPC-queue
causal claim remain unconfirmed. No token-check failure or ordinary five-second
CONNECT timeout is established by this evidence. Post-stop cancellation/refused
connections are cleanup events and cannot explain the earlier rejection.

The run's recorded cleanup and an independent fresh process/listener census
both confirmed zero retained workload processes and zero listeners on all 64
reserved ports. The original failed output remains archived; phase observation
is added before the next deliberate candidate diagnosis, without retrying or
ignoring this CONNECT failure.

## Repairs before the next diagnosis

The smoke fixture now renders the real repository template and compares the
complete typed output configuration for the default 60s/90s and custom 30s/45s
windows. A missing expanded timeline fails explicitly. Its original failure-rate
handling, numerical limits and zero-tolerance correctness gates are unchanged.

A real SessionPool/Engine interleaving reproduced the Linux evidence sequence:
a positive ACK is directly verified while the Engine owner is paused before its
completion callback. Retrying then emitted the same two generator-invariant
observations. Related final-timeout, async-error, non-retry NACK and invalid-ACK
interleavings also reproduced incorrect terminal decisions. Retry registration
and all live owner terminal decisions now atomically select an exact retained,
registered positive completion receipt or record the original terminal outcome
under the same verifier lock. The receipt uses ordinary owner ACK settlement;
it does not verify the ACK again, replay latency/correlation counters or send an
extra retry. Missing state and malformed proofs remain strict errors, and real
rejection/invalid-frame/admission evidence remains visible. Joined Stop cleanup
retains its existing strict public path. Full Verifier/Engine/Session tests and
20 focused race iterations passed; these regressions do not qualify Linux load.

Fixed presence activation phases preserve error-family mapping, protocol status,
retry policy and rollback. The authority emits an identity-free phase event only
for route-not-ready Register/Commit failures; endpoint lookup fanout adds no such
event. Remote wire status stays unchanged. This supplies the missing recovery /
directory registration / commit boundary for a subsequent deliberate run rather
than guessing the first run's underlying cause.

## Second candidate and clock repair

Commit `43642e56b2f22534921bc5f811b296ad9ce6e5f1` passed the
[candidate default/scripts/migration suites](https://github.com/WuKongIM/WuKongIM/actions/runs/40193574219),
the three fixed Linux performance cases, messaging race, Manager and native
package preview. Its [three-node correctness run](https://github.com/WuKongIM/WuKongIM/actions/runs/40193574202)
still failed. The repaired launcher produced the intended short timeline and
the native driver retained `operator_stop` with 75,002 ACKs, without the prior
generator invariant. The report consumer rejected its serialized v6 window:
monotonic duration was 89,930,033,567ns but the wall timestamp difference was
89,930,033,577ns. That 10ns clock-representation mismatch explains the consumer's
missing-evidence classification. The original failed CI/report remain archived;
they are not reclassified as a v7 pass.

V7 reproduces that mismatch and wall shifts of ±10ns and ±200µs through actual
checkpoint/write/read boundaries. Persisted elapsed positions remove the wall
subtraction; truncated, future, stale and missing positions still fail. Fresh
exact-source Linux regression evidence is required after this repair.

The second original-scale Mac diagnosis also terminated early with native
`harness_invalid / worker_harness_invalid`, before the ten-minute baseline.
Fifty complete intervals covered 250.002423292s at 1,999.972614 logical SEND/s.
Final drain completed all 512,005 SEND/ACKs and all 4,692 sampled correlations.
Terminal/loss/duplicate/corruption/sequence and explicit product first-attempt
failures were zero. All original deadlines were resolved; 17,735 observed late
results and 11,459 retry registrations remain separate counters, with unknown
deadline coverage zero. None of these counts qualifies the missing window.

Worker2 retained two CONNECT failures. Matching node3 authentication evidence
and node2's redacted authority event place failures in activation registration
and recovery; the remote event names HashSlot 79 / physical Slot 4 / leader2.
This narrows the source boundary but does not prove provider timeout, RPC queue
saturation, or any one recovery race caused these two failures. New subphase
evidence and deterministic failure-path tests are required before the next run.
Recorded cleanup and an independent fresh census confirmed no retained workload
processes and no listeners on the 64 reserved ports.

Deterministic public RecoveryDirectory-seam tests with the real Directory now
reproduce a separate race: proof can be installed between an initial not-ready
read and the missing-proof query, leaving an empty missing set but returning the
stale initial error. The repair reads back the same exact authority target once;
empty input alone proves no route result. Confirmed absence, live proof,
eviction, changed fence and cancellation remain distinct. A failed readback with
no joined flight retains its own error and phase even when a cold sibling queues,
finishes or cancels. There is no new RPC, provider retry, deadline or admission
budget. Five fixed recovery subphases preserve the original error normalization.
These deterministic failures establish a real bug; they do not establish that
either earlier native CONNECT rejection hit it.

The complete report package also exposed a pre-existing test synchronization
assumption: a goroutine-start notification followed by `Gosched` did not prove
Tick had joined the generation's Stop wait set. Stop could legally seal admission
first. The regression now observes that exact Tick blocked at its admitted mutex
boundary before starting Stop; production lifecycle code remains unchanged.

The combined local bench, presence, node/gateway/cluster adapter and related CLI
suites pass with pinned Go 1.25.11. Presence integration and its seven new boundary
cases pass, including 20 race iterations. The Stop regression fails when its
production join is deliberately removed, then passes 20 normal and 20 race
iterations with the original production bytes restored. FLOW contracts and the
24 terminal-collector process fixtures pass. These checks validate the repairs;
they do not supply a new original-scale latency window or formal Linux evidence.

## Third candidate: original-scale correctness stop

Candidate `e90ae2596eb3b881acbfd0e91b1ac3129157de3d` (tree
`cdaf4c3d0935edc2542eb8ac73341e861f39312c`) passed all exact-source
[candidate suites](https://github.com/WuKongIM/WuKongIM/actions/runs/40198607907),
[three-node regression](https://github.com/WuKongIM/WuKongIM/actions/runs/40198607773),
[Manager smoke](https://github.com/WuKongIM/WuKongIM/actions/runs/40198607788), and
[four-distribution native preview](https://github.com/WuKongIM/WuKongIM/actions/runs/40198607756).
The CI merge tree was verified equal to the source tree. The Linux correctness
smoke retained all 75,502 SEND/ACKs and its report was read successfully. Those
fixed 500-SEND/s cases remain separate from formal original-scale qualification.

A third fresh Mac run retained the same 10,000-online / 2,000-SEND/s workload,
seed, node/worker limits, disk sharing, numerical gates and bounded cleanup.
Its measured first grant was 21:49:11.787026 +08 on 2026-10-25. It naturally
terminated after 97.736997750 monotonic seconds with native
`product_failure / terminal_send`; no supervisor measurement stop was requested.
All 19 periodic cuts are preserved. Eighteen complete intervals span
90.000835375s, with 180,003 SENDs averaging 2,000.014769/s; individual intervals
range from 1,992.981366/s to 2,008.381339/s.

The first bad cut at 21:50:46.834764208 still had 10,000 online, zero generation
stops and zero harness errors, but one retry-exhausted terminal triggered by an
attempt timeout. The preceding 21:50:41.834717667 cut had none. This proves an
original failure before Stop; it is not created by final cleanup. The final cut
contains 192,003 SENDs, 191,487 ACKs, two retry-exhausted timeouts and 514
session-closed terminals. The second exhaustion cannot be placed before Stop
from the captured cuts. The final terminal total must not be described as 516
independent failures during steady traffic.

All 192,003 first attempts registered deadlines; 191,491 original results resolved
coverage, 9,579 were observed late and 512 remain unknown. The known-timeout lower
bound is 4.988985% of registered deadlines, not an all-attempt success rate.
Of 2,048 sampled delivery correlations, 2,041 completed and seven remained
outstanding; zero loss/duplicate/corruption/sequence fields do not prove complete
delivery for this failed run. It never reached the ten-minute latency baseline:
all three v7 elapsed positions remain zero and status is `not_evaluated`.

The earliest server SEND timeout was node2 at 21:50:41.818: permission 543ms,
submitter 4,466ms and remote forwarding 3,592ms. Two node1 requests independently
reported local Future waits of 4,501/4,663ms with queue wait zero. No presence
activation rejection, authentication failure or permission-admission-busy event
was retained. Completed physical message commits were all at most 500ms in the
captured histograms, with means around 37–42ms; they omit unfinished work and
cannot explain a particular Future's 4.5s. The capture lacks the failed logical
SEND's complete attempt/ACK clocks and corresponding backend phases. RPC queue
saturation, pure fsync and a particular storage queue are not established causes.

Two follow-up defects are independently testable. Stop cleanup interpreted the
strict Verifier's successfully recorded product TerminalSend error as a failed
mutation and added GeneratorInvariant noise. Only that exact recorded error may
release its pending state; unexpected errors stay strict. Separately, Channel
append filtering used a nil active slice as a first-filter sentinel. Two leading
ineligible inputs could copy the first failed input back into the active batch,
producing four completions from three inputs, an extra Appender/post-commit call
and aggregate inflight -1 despite deduplicated Future completion. The terminal
node1 metric was -1, but no captured batch identity proves this path caused the
first timeout. Fixing either defect cannot reclassify the original run as passing.

Native cleanup and a fresh independent census confirmed zero remaining workload
processes and zero listeners on all 64 reserved ports. The release decision stays
**not ready**: effective positive latency evidence and the same-source four-host
Linux rehearsal/72-hour/aged-capacity chain remain missing, and production stable
signed-package publication remains disabled. A repaired candidate needs fresh
evidence; these failed runs remain archived under their original identities.

## Fourth candidate and the remaining observation gap

The repaired `14dabb614b72e87627a7740c1c2212a376463e27` candidate passed its
[Linux candidate suite](https://github.com/WuKongIM/WuKongIM/actions/runs/40201680089),
[fixed three-node regression](https://github.com/WuKongIM/WuKongIM/actions/runs/40201680082),
[Manager smoke](https://github.com/WuKongIM/WuKongIM/actions/runs/40201680158), and
[four-distribution native preview](https://github.com/WuKongIM/WuKongIM/actions/runs/40201680078).
The CI merge tree again matched its source. This is fixed Linux regression
evidence, not the formal four-host original-scale acceptance chain.

Its fresh original-scale Mac run ended after 297.657593750 monotonic seconds
with the original `harness_invalid / worker_harness_invalid`. All 592,006 SENDs
were acknowledged and all 5,337 sampled deliveries completed. SEND terminal,
loss, duplicate, corruption, sequence-regression and explicit first-attempt
failure counts were zero. All registered original deadlines resolved; 27,149
original responses were observed late, with zero unknown coverage. The 58 full
periodic intervals averaged approximately 1,999.993 SEND/s. It stopped before
the ten-minute latency baseline; its v7 offsets remain zero and latency is
`not_evaluated`. This run does not erase the third run's earlier terminal error.

Three node2 Register failures named `recovery_provider`, corresponding to the
three subsequent authentication failures. Only the stage was captured; it did
not distinguish snapshot, admission, owner read, response validation or final
recheck. The transport label `presence owner action` covers action opcode 7 and
reads 9/10/11, so its 812 timeout delta cannot identify those three failures.
The current CONNECT Code30 path intentionally combines target rejection with
dial/EOF/other transport failures and stops the worker. Its native classification
is retained; the label alone does not prove a driver cause. An online occupancy
snapshot is not an independent CONNECT error budget.

The next observation therefore adds failure-only immutable provider evidence:
closed reasons, executed-stage masks, local monotonic durations, owner
scope/page progress, and distinct configured budget, effective port-entry budget
and observed elapsed fields. Scope is the shared physical round, never a
fabricated original-caller trace. Unexecuted stages remain unknown rather than
zero; context normalization keeps the old error family and may mark an existing
capture as overridden without extending its clock. Successful prefixes and the
existing result ledger, wire replies, deadlines, retries and gates are retained.
Owner intervals include page encoding, RPC, decoding and merge; entering a port
does not prove a wire request was dispatched. Global RPC sampling remains
disabled for this first diagnostic, avoiding a
simultaneous census/capability-negotiation change. No performance repair or
successful original-scale window is claimed by adding these observations.
