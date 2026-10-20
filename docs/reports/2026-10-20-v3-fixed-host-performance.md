# V3 fixed-host Linux performance qualification

Status: experiment declared; measurements pending. This document does not
qualify a candidate until the exact run and complete receipts are recorded.

## Question and frozen experiment

The earlier comparison changed CPU models between jobs and reversed its latency
ordering. The original EPYC 7763 failure retained sustained driver queue drops,
high CPU pressure and zero unexpected request errors. Those observations do not
by themselves establish a version regression or identify the physical host
cause. See [the preceding evidence](2026-10-20-v3-performance-followup.md).

Use one bounded `conversation-qps-diagnose.yml` dispatch with
`fixed_host_validation=true`. Bind `product_sha` to the exact clean Workflow
commit, containing the migration-recovery repair `98c25091b`. It builds three
products once from the same canonical source directory:

- A: beta.24, `4d44b39dda6fdaa7dafb637ca6cfb718e9530f77`.
- B: preceding candidate, `b469c6e5e2b4ff65611a8fe3389fb5034de3f152`.
- C: exact new candidate/Workflow commit recorded at dispatch.

Reuse one compiled driver in the fixed order **B, B, A, B, B, A, C**. Every
block starts a fresh three-node cluster and the original fixture. Require
preparation to finish before the earliest product process reaches 180 seconds;
release the unchanged warmup at that age, retaining actual measurement ages.
Run three original 60-second mixed windows per block, then separate profiles.
CPU affinity, Go 1.25.11, driver/node GOMAXPROCS 4/2, configuration, profile and
visible ancestor cgroup limits remain fixed. This is the same hosted Linux VM,
not a claim of exclusive physical hardware or visible hypervisor constraints.

All windows, including numerical rejections, remain in the evidence. Identity,
unexpected request errors, invalid controls and process leaks abort. A Linux
preflight exercises cleanup of timed-out processes in separate process groups.
No measurement retries or threshold changes are allowed.

## Predeclared interpretation

Ranked hypotheses:

1. Shared product/driver CPU contention explains the original sustained queue
   overload. CPU pressure and driver waiting should co-vary without additional
   request work; correlation alone cannot identify its historical cause.
2. Preparation age and sequential host evolution contribute to the version
   gap. Same-binary repeats and balanced order can expose this variability;
   this experiment has no unstandardized-age arm and cannot prove age causal.
3. Changed shared read paths add repeatable version-specific cost. Both B
   blocks in A/B/B/A should remain slower than both A blocks beyond B's own
   observed variation, with consistent server resource evidence.

For each B/B pair (blocks 1/2 and 4/5), require the same passing window verdicts,
zero errors/drops, at most 10% difference in server CPU per completed request,
and at most 25% difference in each endpoint's median of its three window P99s.
Relative difference is `abs(x-y)/min(x,y)`. Server CPU is the three-node sum
counted once, divided by list plus sync completions including drain.
These are diagnostic repeatability criteria, not release floors or a
statistical significance claim.

Retain individual windows and expose the spread across all four B blocks and
both A blocks. Compare B blocks 4/5 against A blocks 3/6 only after the A/A
controls pass. A median or mean of window P99s is not a pooled P99. ABBA balances
linear order effects, not every nonlinear trend or carryover effect.

Candidate C independently needs three passing mixed windows followed by the
unchanged complete release matrix: 12 base results and eight endpoint results
in seven stress windows. Use the existing v2 receipt validator and additionally
match the gate binary digest to frozen C. Profiled windows never qualify it.
Passing this matrix does not erase earlier failures or complete a formal
release, long soak, or signed native package publication.

## Evidence and reproducibility

The Workflow retains the frozen plan, host/cgroup identity, command/session
cleanup receipts, product and driver build identities, every block report,
process-age/preparation/resource cuts, separate profiles, gate report and
validation log. The orchestration has a 114-minute deadline within a
120-minute job; each diagnostic/gate keeps its existing 12/18-minute timeout.

Before dispatch, the scenario's pure contract suite and E2E compilation passed,
as did the scripts `Workflow|ConversationFixedHost|ReviewAgent` checks,
seven response-budget proxy tests, Actionlint v1.7.9 and `git diff --check`.
The cleanup integration test skips on macOS; the Linux Workflow must execute it
and retain an explicit pass before measurement. Independent implementation
review found no remaining execution blocker. Product source under `cmd`,
`internal` and `pkg` is unchanged from migration repair `98c25091b`.

Measurement results and remaining causal uncertainty will be added after
inspecting the retained Linux evidence.
