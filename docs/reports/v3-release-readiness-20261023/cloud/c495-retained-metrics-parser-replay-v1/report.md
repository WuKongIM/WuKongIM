# c495 nearby retained metrics parser replay

Exact c495 source, one focused invocation of the real target.Client.Metrics using six existing first-measured/terminal scrapes and an in-memory HTTP RoundTripper; no DNS, TCP, listener, service, workload or DB. All six parse successfully, and each input size/SHA256/mtime is unchanged. Total invocation 1.810 seconds, no repeat. The recorded fixed aggregates are not raw endpoint bodies or identities.

The actual failed finalization scrape round was not retained. Thus these nearby successful responses neither reproduce nor refute its metrics_fetch failure. They do not identify a network/status/body/parser failure, reconstruct worker vectors, validate final accounting, or repair the incomplete final report. No production implementation changed. The one-off test is preserved verbatim here and removed from the temporary source worktree after hash comparison.
