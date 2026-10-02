# Fresh-reference busy: heartbeat phase hypothesis

The repaired per-Slot reference again failed in comparison attempt 02 after
28.490 seconds with four busy responses. The original handoff accounting race
is proven and repaired separately, but cannot explain every busy result. Keep
this failed attempt and the earlier one-off passing rerun; neither may replace
three complete fresh comparisons.

The harness starts 30-second heartbeat tickers before capturing pre-window
counters/latencies and establishing measuredStart. Across several reference
failures, SEND elapsed times cluster around 28.5 seconds. This could align with
the first heartbeat burst, but no actual heartbeat enqueue timestamp is in the
existing evidence. It is a timing hypothesis, not a causal finding. Presence
Touch only marks owner-local activity; code does not justify claiming that each
PING synchronously performs an authority RPC.

## Discriminating experiment

Two isolated harness variants retain the exact frozen product binary
`/lab/bin/wukongim-handoff-slot`, the 5000-channel/1200-per-second/60-second
SEND workload, and all product limits. Both add one pre-window timing anchor
for heartbeat initialization and measuredStart. One retains 30-second heartbeats;
the other changes only the heartbeat constant to 45 seconds. This changed
control schedule is diagnostic and cannot qualify R6 or replace any campaign
failure.

Prediction: if the first heartbeat burst triggers the busy window, the failure
should move about 15 seconds later in the 45-second variant, subject to the
recorded initialization-to-window offset. If it does not move or no longer
reproduces reliably, retain that result and do not label the heartbeat a cause.
The anchor brackets initialization; ticker creation occurs in goroutines, so
it is not proof of exact PING enqueue or peer receipt time.

The patches in `assets/send-ban-heartbeat-phase-20260927/preparation.json`
were generated only after the local harness file matched the frozen Linux
source byte-for-byte. After all nine comparison attempts became terminal, both variants were
materialized and built. Both existing heartbeat helper tests passed, and the
builder verified that the variants differ only in their interval constant.
Both diagnostics are terminal; results follow below.
No production source or current comparison parameters change.

This is a narrower first experiment for the repeatable fresh busy timing.
The separate sustained EOF remains unresolved and may still require the
runtime-trace plan; do not assume both symptoms have one cause.

The prepared `build.py` refuses to run unless the owned container contains only
its idle sleep. It rechecks the frozen source hash, materializes two single-file
Go overlays, asserts that variants differ only by the interval constant, runs
the existing heartbeat helper contract, and builds separately named harnesses.
Each external build step has a 300-second command bound. `run.py` selects the
30/45-second harness while retaining the same product and original workload.
Both scripts passed Python syntax validation. Build/test outputs and binary
hashes are retained in this directory; the original product and harness hashes
remain unchanged. This is a diagnostic harness, not a production repair.

## Admission evidence before the phase experiment

Cumulative post-failure permission metrics narrow the failure exit. Attempt
02-slot has four node-2 busy admissions totaling 5.082 microseconds; attempt
06-slot has one node-1 admission totaling 1.875 microseconds and sixteen node-2
admissions totaling 9.626 microseconds. These events cannot be 100-ms wait
expiry. They select the immediate full-waiter-queue path in the repaired gate.
The later inflight gauges are zero and do not describe occupancy at rejection.
The extract is retained in the comparison's `admission-failure-extract.json`.
This establishes the rejection mechanism, not the reason execution permits
and waiter positions filled at that moment.

The ranked alternatives are (1) synchronized heartbeat/control work creates a
burst, (2) a ReadIndex/apply/storage dependency delays permit release, and (3)
scheduler/GC delay lets pending work resume together. The interval experiment
distinguishes the first timing hypothesis. A shifted failure alone still needs
an execution/dependency trace to explain the saturation mechanism. The harness
uses explicit PINGs; inspection of `pkg/client` found no automatic heartbeat
loop that would keep a separate 30-second schedule in the 45-second variant.


## First control result

`phase-30-01` completed 72,000 messages with busy=0 and SENDACK P99=16 ms;
total runner duration was 248.829 s. Its measured-start anchor places the first
30-second ticker at +29.907409416 s, not +28.5 s. The initial assumed roughly
1.5-second pre-window offset is therefore unsupported for this control.
This passing control does not prove a repair or establish a heartbeat cause.
The 45-second variant's anchor places its first tick at +44.91770625 s.
No actual PING receipt timestamp is claimed by either initialization anchor.


## 45-second result: first heartbeat is not necessary for failure

`phase-45-01` failed at SEND 34.0073606 s with four permission busy responses
and `ReasonNodeNotMatch` (first observed failed message 40,662). This is about
10.91 seconds before the earliest first heartbeat from the recorded +44.9177 s
anchor. Ticker creation occurs after the initialization anchor, so asynchronous
initialization can delay the first tick, not move it ten seconds earlier.
`pkg/client` has no independent automatic heartbeat loop. This reproduction
therefore falsifies the proposed first-heartbeat trigger as a necessary cause
of this busy failure. It does not prove every historical failure had the same
trigger, and the passing 30-second control remains a passing diagnostic only.

The runner ended after 224.287 s without a resource guard; only idle sleep
remained. Lossless logs, actual commands, resource samples, admission extract
and file hashes are retained for both runs. The product binary and every
admission/durability/load limit were unchanged. R2/R6 are still unsatisfied.

Next investigate permission-holding dependencies and synchronized arrival,
using a bounded execution trace that includes the pre-failure window and is
joined before product cleanup. The fresh busy reproduction can use public
pprof runtime tracing in an isolated harness; the long-soak EOF may require
the separate rolling flight-recorder design. Distinguish a blocking ReadIndex,
apply/storage wait, runnable/GC delay, and a load-generator catch-up burst.
Do not change queue capacity, wait limits or permission freshness without
first establishing which chain caused the saturation.
