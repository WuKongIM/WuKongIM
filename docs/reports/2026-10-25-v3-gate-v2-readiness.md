# V3 gate-v2 readiness

The current candidate is not ready for a complete v3 stable release. Gate-v2
repairs evidence collection and interpretation; it does not turn prior local
pressure observations into formal Linux qualification. Numerical latency,
explicit first-attempt failure, and message-correctness thresholds are retained.

## Evidence contract

Chat-lifecycle reports advance to `report/v6` and `thresholds/v2`; worker protocol
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
