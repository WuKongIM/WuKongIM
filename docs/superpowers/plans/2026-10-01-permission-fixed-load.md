# Issue #977: fixed arrival and observation diagnostic

These failure contracts precede the new driver implementation. This supplements
specification section 11; the original 64-SEND reports and failed verifier stay
unchanged. It is not the existing 500 SEND/s qualification or a capacity claim.

1. The historical elapsed-length sampler is not the fixed protocol: it emits
   variable observations, only 64 SENDs, and no 32-second observation clock.
   First run the new opt-in real-process receipt assertion against that original
   scaffold, preserving its expected failure and complete functional receipt.
2. Each CPU window contains 30 seconds of predeclared arrivals and a fixed
   two-second drain tail. Sequential work has 750 arrivals at 40 ms intervals;
   concurrent work has 468 waves of 32 arrivals at 64 ms intervals, 14,976
   messages and 499.2 offered messages/s over the full arrival window. Report
   floor rounding, exact offsets, counts and rate rather than calling it 500/s.
3. Use one persistent worker per selected independent connection and no extra
   queued request. A busy connection drops that scheduled arrival. Scheduling
   delay of at least its arrival interval also drops the arrival. Every positive
   delay is recorded; late/drop/error/uncompleted work fails the protocol. Never
   catch up or retry a SEND. Cancel and join all bounded workers on termination.
4. Exactly 32 full ingress metric requests are scheduled at absolute offsets
   0.5 through 31.5 seconds, each with a 250 ms deadline and no retry. Preserve
   scheduled/start/finish bounds, full-response identity, ownership values,
   timeout/error and missed-slot evidence. Late sampling cannot trigger a burst.
5. Keep the original complete before/after three-node metric cuts and native
   Darwin CPU helper. CPU raw cuts enclose the fixed window and helper scheduling
   overhead, including background and observation work; never subtract either.
   Calibration runs first. Missing, reused or regressing process identity fails.
6. At the fixed drain deadline cancel unfinished calls, preserve all scheduled
   arrival and ACK outcomes, and join workers. An unfinished call cannot grow
   the CPU comparison window or silently vanish from the count. Exact histories
   contain only successful controls and measured ACKs, with a precomputed page
   limit and a hard 200-page cap. Reject truncation, duplicates and foreign IDs.
7. All original placements, bans/unbans and fresh-read/count assertions remain.
   Diagnostic execution initially selects only two-slots-one-remote-leader; the
   original four-layout functional path is validated separately. Source, binary,
   configuration and driver fingerprints remain bound to every receipt.
8. Predeclare same-binary AA1/AA2 controls first, followed by
   A1/B1/B2/A2/A3/B3 before measurement. Preserve all runs and every
   failure. Same-binary repeatability precedes a candidate-isolation comparison;
   this new protocol must not rewrite the old strict acceptance verdict.

Run `WK_E2E_PERMISSION_FIXED_LOAD=1 WK_E2E_PERMISSION_FIXED_REPORT=/absolute/report.json`
with `TestPermissionCallerFixedLoad/two-slots-one-remote-leader`, a frozen product
binary and the original calibrated CPU probe. Candidate count mode additionally
sets `WK_E2E_PERMISSION_FIXED_COHORTS=1`. No product configuration is introduced.
