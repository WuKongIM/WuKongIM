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
