# Send-permission admission handoff investigation

The fresh reference run observed four immediate busy responses (aggregate
admission time 1.916 microseconds), distinct from the sustained run's async
session queue overflow. Neither symptom is established as having the same cause.

Ranked, falsifiable hypotheses for the immediate busy responses:

1. A channel permit transfer keeps the assigned goroutine in `permissionWaiting`
   until it resumes; an arrival in that gap sees a falsely full waiting budget.
   A one-P controlled handoff should reproduce busy with a free waiting position.
2. All sixteen real waiting positions were occupied because fresh barriers
   stalled. A correct atomic handoff would still produce busy in the original
   scenario; the captured pre-failure stacks do not capture the failing peak.
3. Producer pauses caused a catch-up burst. Captured last-window driver timing
   does not show a long pause (see the prime-metrics report), but 100 ms bins
   do not exclude shorter bursts or server-side grouping.

Before any production repair, two integration tests exercise the actual gate:
permit transfer followed by another arrival, and cancellation after transfer
followed by reuse of all sixteen permits. Existing receiver integration tests
cover pre-decode overflow, the 100 ms budget, caller cancellation, and fresh ban
visibility after waiting. No concurrent tests or builds run during the active
30-minute sync diagnostic. Results will be appended after actual execution.

## Reproduction and repair

The original gate failed the controlled handoff arrival in all 20 repetitions:
`permission read admission busy` despite an available waiting position. The
canceled-handoff reuse control did not fail. The exact Linux invocation and
output are retained in `assets/send-ban-admission-handoff-20260927/red.json`.
This confirms an admission accounting race, not the causal explanation of the
previous pressure-run failures.

The repair uses one short mutex to assign a permit and remove its queue entry
before waking its owner. Uncontended calls allocate no waiter. The contention
queue is bounded to sixteen pointers; removal copies at most fifteen pointers
and clears the removed tail. Assigned-but-not-yet-resumed callers occupy the
sixteen execution positions. Cancellation or timeout removes an unassigned entry
or returns its reserved permit, preserving the shared local/remote gate and
pre-decode/fresh-barrier ordering. No capacity, wait limit or RPC retry changes.

Tests were written before this implementation. The old unit test's manually
forged waiting counter was replaced with its malformed-envelope permit-release
check; actual queue saturation remains covered by concurrent receiver integration
tests. All `TestSendPermission*` integration checks passed twenty repetitions
(12.643 s), the same checks passed three race-enabled repetitions (4.296 s),
and the full proxy default test tier passed (1.324 s). Linux build succeeded.
The product candidate SHA-256 is
`39afc971f70a84c48582ca3b2cb5f4355c062e2e441a0c6c7022743055b9e879`.

The original `/lab/src` only received the pre-fix regression test; production
edits and build use `/lab/handoff-src`. The original product and reference
binaries remain unchanged. Source patch and governing context hashes are retained
in `source.json`. Existing Slot FLOW semantics remain accurate: bounded waiting,
sixteen executing envelopes, cancellation, and fresh reads are unchanged.

## Real-process functional regression

All four targeted black-box packages passed using the repaired Linux product:
`send_ban` (243.837 s), `send_permission` (2.730 s), `no_persist` (12.395 s),
and `terminal_disband` (4.005 s). The send-ban package completed its nine
non-opt-in cases, including real network overload, five-process read overlap,
leader transfer, delivery absence, policy combinations, non-replica ingress,
known-ban priority and concurrent metadata writes. The independent 100k opt-in
was not rerun by this invocation. Nine JSON artifacts and the lossless test
output are retained; this functional result does not establish the performance
comparison or explain the previous EOF.

## Original fresh-reference workload recheck

`fresh-recheck-01` used the repaired gate in the otherwise identical per-Slot
fresh reference, 5000 Channels, 1200 SEND/s, 60 seconds and the existing batch
cap 32. No sync overlay or optional runtime/driver/storage diagnostics were
enabled. It passed all 72,000 messages with busy=0, pending=0, SENDACK
P50/P95/P99 7/10/16 ms and RECV P99 16 ms. Mean aggregate node CPU was
190.444% (one CPU=100%), and measured permission RPC calls were 222,163.
Cold prime took 168.290 s; the complete runner exited zero in 249.346 s with
no resource-guard stop and no remaining product processes.

This verifies the original unminimized workload once after the proven accounting
repair. It is not three paired comparisons, does not erase prior failed runs,
and does not establish the earlier sustained EOF's cause. The repaired node
aggregation binary then started a separate uninstrumented 5000/4500/30m run;
its terminal result must be evaluated independently. Raw output and checksums
are retained in `assets/send-ban-admission-handoff-20260927/fresh-recheck-01`.

## Uninstrumented sustained workload: failed, not qualified

`clean-sustained-01` kept 5000 Channels, 4500 SEND/s and the requested 30-minute
window. It failed with sender EOF after 1701.108 seconds (28m21s) and 7,654,988
SEND calls; 6480 messages were pending. Partial-window SENDACK P50/P95/P99 was
8/53/147 ms and RECV P99 137 ms. These partial quantiles cannot be compared as
though the full window passed. Node 1 recorded one
`async_dispatch_queue_full` connection close. Permission busy, transport RPC
rejection and Channel RPC admission-full were zero.

The runner exited 1 in 1891.859 s, without a resource-guard stop; all product
processes were reaped and only the container's sleep remained. Minimum sampled
host free space was 10,678,878,208 bytes (about 9.95 GiB). Maximum cgroup memory
was 5,620,740,096 bytes; memory max/oom/oom_kill events were zero. The cgroup
reported 3,407,753 additional throttled microseconds over the whole run; this
aggregate is not a single observed pause. All resource samples succeeded.

At 08:31:15 UTC, while this run was active, the task's unused `/lab/gocache`
was reclaimed with `go clean -cache` after verifying there was no Go build/test
process. This freed about 2.4 GiB of rebuildable cache in 0.318 s without
stopping the binary/harness or changing databases, limits or the offered load.
The exact command, times and space are retained in `cache-reclamation.json`.
This additional filesystem activity is explicit; the run was never a CPU/P99
comparison pair. It had no product diagnostic overlay or optional pressure probe.

All three post-failure goroutine inputs were untruncated (97,790 / 116,464 /
95,635 bytes). They include four Fdatasync stacks and no memTableWriteStall;
they do not establish operation durations or the cause of the stall. The
accounting repair is proven separately and its original fresh workload passed,
but the sustained EOF remains unresolved. R2 remains failed. The full log,
metrics and resource evidence are hash-verified in this run's manifest.
