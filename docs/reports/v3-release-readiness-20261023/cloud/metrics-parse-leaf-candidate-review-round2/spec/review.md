# Spec review — parser leaf diagnostics, round2

Full scope:8dfcd35b7d604fb5d3da3d75e36ab267e2f4b1bf →2662fe93b754d614422f2936185cf781a33b831a; tree1182b14d5b659eb60f2e7b694ff91b6641189106. This round focuses on the delta from11debae7337dc0f0497ff732600d5ebf03949544 and retains the prior review.

**Round1 P3 fully closed; remaining findings:0.**

`metrics_fetch_failure.go:30–32` now seeds the temporary legacy fields from the receiver before default JSON decoding. This satisfies design.md:17,19 (“Retain … source_mask and kinds exactly”; malformed optional vectors leave valid old fields intact). Omitted/null scalar and array values, null array cells, short/empty arrays, partial updates and repeated outer metrics_fetch objects now follow the previous default decoder. Explicit zero/empty updates and outer null retain their existing clearing behavior. The new optional field independently starts absent, so missing/malformed data cannot retain a previous parser-reason vector or invalidate valid old fields. Old type/overflow errors and invalid whole JSON remain rejected.

The129-line supplemental test file uses the independent old DTO's default JSON decoding as an oracle:18 reused-receiver cases,14 repeated-outer-object cases and6 rejection controls. Its pre-fix hash is unchanged through the retained behavioral RED/GREEN; original seven tests plus the three-null-position supplement remain byte-identical. Frozen normal/race receipts report2605/2321 passing test events and both named checks passed; these were inspected, not rerun.

Only the legacy-field seed and two comments change production. Removing those three lines reproduces the entire previously reviewed wire file byte-for-byte; the other13 original changed paths and governing rule/context/FLOW hashes are unchanged. Thus the prior parser/provenance/projection/join/verdict assessment still applies without claiming a new full review execution. The round1 finding and its proof remain immutable.

The v2 manifest and all19 public payloads39,907B were independently verified. Source/head/tree remained clean and fixed before/after. No test, build, service, workload, network, cloud, GitHub or repository mutation was performed. Missing historical response, message_loss root cause and Linux qualification remain unresolved by this diagnostic patch.
