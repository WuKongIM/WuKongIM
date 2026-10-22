# Metrics-fetch diagnostic proposal

**Awaiting root approval; no implementation or new tests.**

`design.md` recommends one typed Metrics boundary plus a fixed three-source diagnostic projection. `failure-matrix.json` and `failure-matrix.md` freeze 54 failure/control cases before code. `proposed-validation.json` describes tests-first synthetic validation. `source-bindings.json` fixes exact c495 source/tree, rule/FLOW hashes and the check policy. `prepare_matrix.py` only regenerates this documentation table; it is not production/test implementation.

The failed c495 round itself was not retained. A successful replay of other retained scrapes does not identify its failure. The proposal preserves harness-only aggregate Unwrap, all three-source joining, parent context precedence, parser behavior, every bound, timeout, retry, accounting baseline and release gate. It does not claim a fix or Linux acceptance. No live workload, HTTP request, database query, build, test, cloud operation or source mutation occurred while preparing this proposal.

Implementation, if approved, belongs in a fresh task worktree from merged main 27fa6d63cf3b2b6c3a9a28575f917d60cc054ded (same production tree 98fead03ba0c727dd733f742790e7737327f3942), after confirming that equality. Do not modify the retained independent exact-source clone. Root approval of this concrete frozen design is required before writing tests/code; a later workload needs its own finite review.
