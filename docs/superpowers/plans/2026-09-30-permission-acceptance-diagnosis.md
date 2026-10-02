# Permission cohort acceptance follow-up

Authorized scope: local tail diagnosis, bounded native-package bootstrap
diagnostics and a separate documentation contract repair. Start from frozen
cohort head e8981154a857667a04696d42df430ac2dd5f7c4c. Preserve previous receipts.

## Predictions and failure cases (before probe/repair code)

1. Cohort-induced arrival synchronization could shift delay into append or
   replication. Permission plan/barrier buckets would drain early while append
   waits cover the observed tail. Existing Linux trace is a different workload
   from the Darwin 527 ms observation; aggregate profile delay is not per-SEND
   latency and cannot establish its cause.
2. Scheduler, GC or disk delay could dominate; corresponding runtime/storage
   evidence must bracket a breach. Existing flight snapshots omit permission
   metric families, and counter completeness is false. Missing signals remain
   unknown; no inferred zero or population-percentile addition.
3. Cohort lock/wakeup could delay permission completion; public permission
   stage counts/sums/buckets would show that delay in the same caller window.
   Capture only fixed metric families in the existing boundary scrapes, outside
   measurement, for both old/new. Keep actual inputs, sampler cadence, clean
   binaries and one preselected old-then-new pair, no profiles or window retry.
4. Native bootstrap can stall during package installation, before PID 1 exec,
   or during systemd initialization. Failure must retain the current stage,
   bounded installer output, PID 1 state, systemd jobs/status and journal before
   removing only its own container. Probe failure/timeouts must not replace the
   original verdict or stop cleanup. Preserve 300/900 s readiness/total bounds.
5. Missing contract operations must be added from current route/DTO semantics,
   including strict binary flags, decimal CAS versions, failure envelopes,
   completed-write freshness and the existing trusted-backend boundary. Keep
   exact runtime route equality and RPC numeric parity assertions.

Controlled fake-Docker integration exercises failure cleanup/output ownership
before changing the launcher; actual distro diagnosis stays unverified unless
a real run supplies evidence. Documentation repair retains the already-red
runtime-parity tests and adds any necessary schema failure cases before changes.

The one extra process comparison is diagnostic, not a repeated qualification
or a replacement for the old failed tail observation. CPU/5% non-regression and
4500 SEND/s remain unqualified without direct evidence. Do not change production
cohorts based on an unconfirmed attribution. Native/documentation changes use
separate commits/PR scope; no protected workflow/policy changes or manual rerun.
