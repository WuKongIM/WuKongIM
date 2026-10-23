# Spec review — metrics-fetch diagnostics, round 1

Fixed scope: 27fa6d63cf3b2b6c3a9a28575f917d60cc054ded → 56c287493a5e9fc3df65ea0631b99612672cd678; tree582e5bc024147b32aa68074d04c041391af89b96.

**0 findings.** No missing/partial requirement, unrequested behavior or incorrect implementation was found against approved Option A and its54-case matrix.

- Design lines15–16,25–37: the shared HTTP loop retains address order, fallback, causal-context decisions, auth/limit early exits,512-byte drains and closes. The private typed Metrics wrapper preserves the former cause chain while exposing only closed text/kinds. Earlier failures clear on success; selected-body parser errors do not retry. Empty/partial bodies retain the existing parser behavior. Parser/helper suffix and numerical limits were independently verified byte-equal to base.
- Design lines17,39–43: collection still joins exactly3 launched sources, places results by declared source position, and gives parent context precedence after joining. The fixed private cause array unwraps only HarnessInvalid, preventing child context/product errors from changing finalization semantics. Composition still maps sources in observation.service_nodes order.
- Design lines18–21: the optional mask/vector is accepted only at meta_checkpoint/metrics_fetch. Fresh bounded copies validate mask values1–7 and slot consistency; forged kind text becomes unknown. Other stages/reasons and parent-context failures omit the object. Frozen observation projection and report validity remain unchanged.
- Design lines49–53: test-first v2 source hashes match the committed tests. Frozen behavioral RED, GREEN, race, full-suite and named-check receipts were inspected; the earlier fixture mistake remains preserved. Settle policy/loop, Finalize verdict/report gate, coordinator, CLI composition and check policy were independently verified byte-equal to base.

All13 changed files match exact-head Git blobs. Both supplied manifests and every payload hash passed verification. Checkout stayed clean and fixed at the reviewed head before/after. This static review ran no test, build, service, workload, network, cloud or GitHub operation. The missing historical scrape and remaining message-loss cause remain unresolved; this diagnostic patch does not establish Linux acceptance or a product fix.
