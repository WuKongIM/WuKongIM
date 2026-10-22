# Failure matrix recorded before the diagnostic test

Scope: one test-only replay on exact d349a7bdd2ca8f6bd4683fbccbf83010d3f9cd3e. No production implementation, server binary, workload, network request or report-policy change.

1. Each of the six retained first/terminal node scrapes must traverse the real bounded `target.Client.Metrics` parser through an in-memory RoundTripper. A parsing failure is recorded only as a closed category, never raw source/error text.
2. Exact result totals must equal the12 per-slot integer counters, with all three result kinds present. Counts remain uint64; unknown, partial or malformed metrics never become zeros/success.
3. The three parsed snapshots per retained cut must pass the actual MetaCreateAccounting structural path using a newly constructed valid initial256/12 assignment and explicitly synthetic empty expected vectors. This is a structural counter/fold check only; it cannot replay the missing actual worker vectors or the exact failed scrape round.
4. The real ProductionMetaController must accept valid explicitly synthetic final worker shapes with those same parsed metrics, preserving its normal expectation/collection/accounting chain.
5. A deliberately missing result kind must remain `ErrLifecycleHarnessInvalid` with accounting unchanged; an invalid synthetic worker phase must fail before any metrics source call.
6. All retained files must retain identical size/SHA/mtime before/after replay. Raw scrapes stay private. Public receipts contain only closed stages, node ordinals, fixed aggregate counts and input hashes.
7. No actual terminal WorkerSnapshot is available. The replay must not infer its vector from SENDACK totals or claim this fixture reproduces the exact finalize failure. If every retained-cut structural check passes, actual leaf attribution stays unknown.

One command only, Go timeout90s and process timeout120s; no automatic rerun. A timeout/build failure is retained as incomplete evidence.
