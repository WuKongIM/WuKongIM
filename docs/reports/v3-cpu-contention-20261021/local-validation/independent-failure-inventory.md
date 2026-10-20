# CPU placement experiment: independent failure inventory

Prepared before implementation, 2026-10-21. This is a design review and proposed
regression inventory, not test execution or authorization to dispatch.

Accepted refinement before implementation: every role completes preparation,
eviction and 180-second release on the same four-CPU masks and driver/node
GOMAXPROCS 4/2. Only then, before the original warmup, a bounded all-TID
application changes S/O/D masks; the caller subsequently changes driver
GOMAXPROCS to 4/1/1. The helper does not change GOMAXPROCS. At most eight rounds
and 500 ms are allowed, with original and applied all-thread receipts.
This supersedes the earlier preferred launch-mask suggestion below.

Product C is frozen at `ef798352c40a94e57f768f76b2f4713712f827fe`.
Its previously qualified Linux binary SHA-256 is
`8c3c79f2853b76be7f83dbd606404bf6ab1fdf34ce9370e82c056b389d03c57d`.
The new harness/workflow SHA H must be recorded separately. No product changes.
All six blocks must use identical actual product bytes and one compiled H driver.
If a clean rebuild does not match the qualified binary digest, reject a claim
that these are the same qualified bytes; do not silently replace that assertion
with source equality. Freeze the intended identity contract before dispatch.

## What this design can identify

Order: **S, O, D, D, O, S**, fresh three-node cluster per block.
The existing fixture, list/sync 200/60 offered QPS, page 100, eight workers per
endpoint, bounded queues, exact response checks, three 60-second windows and
180-second preparation release remain unchanged.

For O/D, nodes share a fixed set N of three logical CPUs across two visible
cores; driver GOMAXPROCS is 1, node GOMAXPROCS is 2. Select one visible SMT pair
`{o,d}` and require `o in N`, `d not in N`. O pins all driver threads to `o`;
D pins them to `d`. The remaining visible SMT pair belongs entirely to N.
O's driver CPU must be the retained sibling of D's excluded CPU, not an arbitrary
CPU in N. Record actual sysfs package/core/sibling identities; never infer them
from adjacent CPU numbers. D removes logical-CPU overlap with owned nodes; it
does **not** give the driver a private physical core or exclude runner agents.

S uses all four CPUs for all processes, driver/node GOMAXPROCS 4/2. It is only
an initial/final drift sentinel: comparing S with O or D changes both affinity
and driver parallelism. It cannot isolate either factor.

O/D have the same average sequence position and reversed adjacent contrasts
O2→D3 and D4→O5. This balances a linear position trend, not nonlinear host drift,
carryover, unequal wall-clock spacing, hidden scheduling or all preparation
state. Equal age is not proof of equal GC/compaction/cache state.

Even a repeatable D benefit supports only the effect of this placement
intervention on this VM under these settings. It cannot confirm the historical
EPYC failure's exclusive cause, a version regression, or a new release gate.
There is no product-version intervention in this experiment.

## Ranked falsifiable hypotheses

1. Logical-CPU overlap adds driver runnable delay and queueing. Predict D improves
   both paired endpoint timing/drop outcomes and driver runnable-wait evidence,
   with unchanged node masks, workload and product. A repeatable absence of that
   direction weakens this explanation for this setup.
2. Driver GOMAXPROCS=1 or application work is limiting independent of overlap.
   Predict O and D both retain comparable driver execution cost/queueing and no
   repeatable placement benefit. S is descriptive support only because it also
   changes node eligibility.
3. Host evolution or fixture/background state dominates. Predict within-O/D or
   S sentinels drift, pair directions disagree, or concurrent host/state signals
   vary more than the O/D effect. Such results are inconclusive for attribution.

## Pure parser and contract tests to write first

These use fixed text/JSON or no-background dry-run inputs; no real sleeps,
processes, sockets or real retry loops. Existing pure policy seams are preferred.

| ID | Failure input / trigger | Required assertion |
| --- | --- | --- |
| P01 | Non-contiguous CPU IDs, shuffled topology rows, valid two SMT pairs | Deterministic valid N/o/d mapping; CPU-number adjacency is irrelevant. |
| P02 | Missing/duplicate/offline CPU, fewer/more eligible CPUs, one/four visible cores, incomplete/asymmetric sibling sets, sysfs/lscpu disagreement | Reject unsupported or unproven topology; never choose a fallback mask. |
| P03 | O CPU not in N; D CPU in N; O/D are not siblings; changed N between blocks | Control invalid, before measurement. |
| P04 | Requested mask silently intersected with cpuset; cgroup moved; allowed mask changes; visible ancestor quota or memory control missing/changed | Preserve actual/requested masks and error; reject control instead of trusting taskset exit zero. Root-not-applicable controls remain explicitly unknown. |
| P05 | Empty/descending/overlapping CPU ranges, duplicate IDs, oversized IDs/text, malformed integer | Bounded parse failure; do not accept a partial mask. |
| P06 | `/proc/.../stat` comm includes spaces or closing parentheses; truncated stat, overflow or wrong PID/starttime | Correct stable task identity or explicit failure, never split blindly on spaces. |
| P07 | TGID has expected affinity but one non-leader TID has the old/wrong/wider mask | Reject the sample/control. A leader-only receipt must not pass. |
| P08 | New TID with correct/wrong mask; TID reused with different starttime | New identity gets a fresh baseline; wrong mask fails; no deltas across reuse. |
| P09 | Task disappears mid-read | Preserve the race; only a bounded re-enumeration confirming task exit may classify exited. EACCES, parse errors or still-live missing data must fail, not become zero. |
| P10 | Task directory omitted/truncated or thread cap exceeded | Incomplete inventory is explicit and invalid; never silently truncate and claim all-thread coverage. |
| P11 | Valid schedstat three unsigned fields; empty/two/four fields, negative, float, overflow, unsupported schema | Interpret CPU/wait as nanoseconds and timeslices as a count; reject malformed input. |
| P12 | schedstat unavailable/malformed, or every counter remains zero despite a known runnable preflight | Reject unavailable evidence, not a zero-wait claim. Linux 7.0 uses CONFIG_SCHED_INFO/sched_info_on for this file; `kernel.sched_schedstats=0` alone must not be treated as disabling its independent counters. Retain that switch as context, without changing it. |
| P13 | Counter decreases/reset; PID/starttime mismatch; zero/negative interval | Invalid delta, not `max(0,delta)` or absolute value. |
| P14 | Thread appears/exits between cuts; short-lived thread absent from both samples | Preserve births/exits/coverage limits. Sampled per-thread totals are not exact whole-process totals; do not sum only survivors and label complete. |
| P15 | UTC jumps, duplicate/out-of-order sequence, late or missing sample, cutoff outside actual window | Use monotonic offsets and actual start/end receipt. Retain lag and explicit gaps; no interpolation or catch-up samples labeled as past observations. |
| P16 | Sampler read/parse/write failure, oversized file/frame, exceeded thread/frame/byte/time cap | Keep bounded partial artifact with `complete=false`/error, not an empty success. |
| P17 | Arrival buckets indexed by scheduled second but samples indexed by actual observation time | Keep these clocks distinct. Driver wait includes schedule lateness and queue wait; per-second correlation is not an exact per-request span. |
| P18 | Add per-process enclosing cgroup counters four times; add request P99 and driver-wait P99; treat missing PSI/CPU as zero | Reject/avoid these aggregations. Shared cgroup counted once; percentiles are non-additive; availability is explicit. |
| P19 | Sampler wraps the whole `measureMixed` helper rather than its actual shared request start/end | Receipt identifies warmup/drain bounds separately; it cannot claim exact 60-second alignment from helper boundaries. |
| P20 | H differs from C, product bytes match qualified C, correct driver/profile | Accept separate exact identities; never require H=C for this diagnostic mode or mislabel H as product. |
| P21 | Wrong C SHA/digest, dirty source, substituted binary, changed profile, driver compiled twice, rebuild between blocks | Fail provenance. Required profile digest remains `1d0fb74bf51a6e747b56d1b43fece4670b40143a69a1601cc2f4be6f784a37a1`. |
| P22 | Any block missing, duplicated, reordered, overlapping or retried; role not one of S/O/D | Reject experiment completeness, retaining all prior outcomes. Exactly six blocks and three windows each. |
| P23 | New mode absent; conflicting old fixed-host/new intervention flags | Old four-CPU/all-process fixed-host behavior and release gate remain unchanged; conflicting flags fail before process start. |
| P24 | O/D driver GOMAXPROCS not 1, S not 4, node not 2; `runtime.NumCPU()` equals launch affinity rather than host four | Require exact runtime settings and independently proven host topology; never weaken host proof just to bypass the existing guard. |
| P25 | Changed offered load, page, workers, queues, slot count, fixture, HTTP pool limits, response checks, allocation/P99 thresholds or retries | Reject drift; expected overload is retained numerical evidence, not a reason to tune the experiment mid-run. |
| P26 | Failed preparation exceeds 180 seconds; altered age origin or stale data reuse | Reject control; no extension, cluster restart, or replacement block. Actual first-window age still follows original warmup. |
| P27 | Authentic numerical rejection versus forged `verdict=pass`; malformed response, unexpected error, runtime load/membership mutation | Preserve the genuine rejected window; corruption or unexpected behavior invalidates diagnosis. Control validity and workload pass are separate. |
| P28 | O2/O5 or D3/D4 inconsistent; S1/S6 drift; paired O/D effects opposite; one selected best window | Analyzer refuses stable causal attribution, preserves all 18 windows/36 endpoint outcomes, and never converts window-median P99 into pooled P99. |
| P29 | Missing/not-pass/skipped Linux integration preflight; default workflow selected alongside new mode | New Linux job must require explicit exact-test pass events, reject skip, and run no other load job concurrently. |
| P30 | Timeout/exception/no final write, stale prior-success summary, leftover sampler/child session | New result remains incomplete; always-upload includes partial logs/samples; no next block while owned processes remain. |

## Real Linux integration preflight required before measurement

These are process/kernel behavior tests, not pure unit tests. If under `scripts/`,
place them in `*_integration_test.go` with `//go:build integration`.
Use bounded synthetic owned children, not the 600-group performance workload.
macOS skip cannot be the only coverage. The new Linux workflow must run them
before product measurement, preserve JSONL/stderr, and require explicit pass
for each named test with no skip. Names below are proposed, not existing tests.

1. **All-thread launch and late-thread inheritance**:
   `TestConversationCPUCausalAllThreadAffinity`. Start a multithreaded Go helper
   via the actual launch/affinity path; keep several OS threads alive, create
   another after the first inventory, and prove all old/new TIDs have the
   requested role mask. Prove S/O/D masks and the node/driver difference.
   A negative arm intentionally changes only TGID while a non-leader retains the
   old mask; the real observer must reject it. Verify post-exec and post-thread
   creation, not merely command construction.
2. **Actual scheduler-wait availability**:
   `TestConversationCPUCausalSchedstatEnabled`. Record availability/config;
   two bounded owned runnable threads pinned to one CPU must produce nonzero
   CPU and wait deltas with intact identities. Unavailable/unreadable counters
   reject this evidence path; do not fabricate zero. On Linux 7.0 do not infer
   CONFIG_SCHED_INFO availability from the separate sched_schedstats switch.
   Do not change sysctl during the experiment.
3. **Owned thread churn and read failure**:
   `TestConversationCPUCausalThreadSampling`. Exercise a live task exit/birth
   while reading, plus injected reader failure at the OS adapter boundary.
   Verify bounded classification of confirmed exit, fresh baseline, explicit
   missing fields and no deltas across task identity changes.
4. **Timing and cancellation**:
   `TestConversationCPUCausalSamplerLifecycle`. Short real monotonic schedule,
   deliberate sampler delay, controlled I/O failure and cancellation; confirm
   actual timestamps/gaps, bounded artifact, termination/join, no goroutine or
   subprocess left behind and no stale complete receipt.
5. **Timeout session cleanup**:
   Reuse `TestConversationFixedHostTimeoutCleansChildProcessGroups` and extend
   its actual owned-session path only if the sampler or launcher introduces
   another process. Kill a timed-out driver with independently grouped children
   and sampler, prove complete cleanup without touching the caller session.

These tests should fail against a TGID-only/missing-as-zero implementation.
Write and record that red before implementing the fix. Do not write a parser
unit test as a substitute for the actual Linux multithreaded proof.

## Decisions that must be frozen before dispatch

- All-thread application strategy: retain common preparation, then apply the
  role masks to every existing task and perform bounded complete rechecks,
  allowing new tasks only after their inherited masks are verified.
  Avoid changing an already multithreaded driver solely via TGID.
- How task scheduler counters are validated; no inference from file existence
  or zero counts, nor from the separate dynamic sched_schedstats switch alone.
- Observer placement and overhead. A goroutine on the one-CPU driver competes
  with load generation; a separate observer is not free either. Same mechanism
  and mask in all roles, with elapsed/cpu/bytes counts and explicit budget.
  Do not introduce a fifth uncontrolled CPU placement.
- Exact sampling contract: fixed 1 Hz, finite frame/task/byte/read-time bounds,
  monotonic request-window origin, interval begin/end/lag, initial/final cuts,
  and missing-sample policy. Predeclare lag/gap/coverage/overhead limits before
  results. A late sample is neither zero nor an observation of its scheduled
  instant. Thread sampling cannot prove the absence of an unsampled short thread.
- Measurement integration seam. `measureMixed` currently warms up and scrapes
  before selecting a shared future start (`stress_test.go:146–175`). To promise
  same-window sampling, use that exact shared monotonic start, or label a
  surrounding-helper trace and filter only fully observed intervals. Keep the
  existing release/default behavior unchanged.
- Numeric rejection and repeatability rule. The previous all-pass A/A rule was
  for **version attribution**. An intervention may intentionally make O reject.
  Either retain the strict all-pass rule and accept that such a result cannot
  pass the causal criterion, or predeclare a separate reproducible-overload
  criterion now. Never relax it after seeing a red O/green D result.
- O2/O5 and D3/D4 repeatability limits, S1/S6 drift limits, minimum paired effect,
  mechanism corroboration and scope of an inconclusive result. Endpoint results
  alone cannot establish that scheduling rather than another resource changed.

## Repository seams and instructions consulted

- `AGENTS.md`
- `test/e2e/AGENTS.md`, `test/e2e/message/AGENTS.md`,
  `test/e2e/message/conversation_qps/AGENTS.md`
- `test/e2e/suite/FLOW.md` (valid `scope: package`)
- `.github/workflows/README.md` (diagnosis/fixed-host/tool contracts)
- `test/e2e/message/conversation_qps/fixed_environment.go`
- `test/e2e/message/conversation_qps/fixed_environment_e2e_test.go:144–281`
- `test/e2e/message/conversation_qps/mixed_attribution_test.go:31–149`
- `test/e2e/message/conversation_qps/stress_test.go:146–175`
- `test/e2e/message/conversation_qps/conversation_qps_test.go:312–411`
- `scripts/conversation_fixed_host_integration_test.go`

The existing scenario instructions explicitly require all four processes to
share four CPUs and no in-window sampling in **fixed-host mode**. Introduce a
distinct opt-in/receipt and update its instructions; do not silently weaken the
existing contract. No applicable FLOW exists in the scenario or scripts root.

Primary API references checked for this review:

- Linux man-pages [sched_setaffinity](https://man7.org/linux/man-pages/man2/sched_setaffinity.2.html):
  affinity is per-thread; the kernel can intersect masks with cpuset constraints;
  children inherit masks and exec preserves them.
- Linux [scheduler statistics](https://www.kernel.org/doc/html/latest/scheduler/sched-stats.html):
  runtime, runqueue wait and timeslice counters need baseline/delta accounting.
- Linux [sched_schedstats](https://www.kernel.org/doc/html/latest/admin-guide/sysctl/kernel.html#sched-schedstats):
  scheduler statistics can be disabled; enabling has measurement overhead.
- Linux 7.0 [proc task implementation](https://github.com/torvalds/linux/blob/v7.0/fs/proc/base.c#L478-L495):
  task schedstat is guarded by CONFIG_SCHED_INFO and sched_info_on, rather than
  being equivalent to the independent dynamic sched_schedstats control.

No tests, product processes, workflow dispatches, repository edits or sysctl
changes were executed in preparing the initial inventory. Subsequent authorized
placement-helper implementation and validation are recorded separately.
