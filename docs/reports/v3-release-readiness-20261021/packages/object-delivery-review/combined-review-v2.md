# Offline delivery v2 review

## Standards

# Standards review — object delivery v2

No actionable documented-standard violations or baseline smell findings in the five-file frozen change, including the two repairs since v1.

Base/head remains `983e567f155a982c45ca066e4dc67d33851f0d24`; tree `7a498c42b4f5b0e6f86a524878fde651d7301371`. Frozen source copies and working-copy hashes/sizes match. The complete reconstructed patch matches SHA-256 `758c319723a6ae83169ae29cab015296ba13a6f160dea46f7223b8ca978d0029`; the v1-to-v2 implementation diff also matches. Applicable standards remain unchanged, with no package-specific AGENTS/FLOW.

- `scripts/prepare-object-storage-delivery.py:298–317` adds a documented, narrowly scoped current-control check and exact stable receipt bindings. The two observations at lines 356 and 392 share that validator. They address present behavior without speculative abstraction, consistent with root AGENTS Code Quality. The twelve supplied smell heuristics yielded no actionable judgment-call findings.
- The independent fake state and six new regression methods existed in the preserved first-red test bytes while the implementation hash was still v1. The failure inventory precedes both. Comparing first-red with final tests shows only the documented correction at `tests/test_object_storage_delivery.py:503`: inspect historical control fields instead of substring-matching a 64-character snapshot digest. No new test method was added afterward. This supports AGENTS “FIRST write all the ways it could fail” and the recorded user instruction to write all related tests first.
- `CHANGELOG.md:5` records the operator-visible behavior under Unreleased, satisfying the release-note rule. The operator documentation preserves offline-only operation, independent identity boundaries, unchanged original receipts and disabled production/publication claims.

Recorded evidence shows 75 related storage tests and four contract checks passing against these exact bytes. The subsequently completed first full-suite run reports 554 tests: 553 passed and one existing RPM-tool skip. Its receipt binds the same implementation/test digests. This reviewer reran no tests and changed no implementation, cloud, CI, key or network state.

Standards: 0 findings; no worst issue. This report does not replace the independent Spec review.

## Spec

# Spec review v2 — original D offline core

**Both v1 P2 findings are closed. No remaining actionable Spec findings or scope creep found.** Review covers exactly the five frozen files at HEAD/base `983e567f155a982c45ca066e4dc67d33851f0d24`, tree `7a498c42b4f5b0e6f86a524878fde651d7301371`. Working-copy and frozen-copy hashes match; full patch SHA-256 is `758c319723a6ae83169ae29cab015296ba13a6f160dea46f7223b8ca978d0029`.

1. **Current activation-control fence — closed.** Approved `after-native-delivery-gap-review/failure-inventory.md:19` requires “激活前source/audit/control/current-config漂移 | 不执行新的激活动作”; README:11 preserves separate historical/candidate/seal/current-control identities. `prepare-object-storage-delivery.py:298–304,356–357,392–393` validates independently observed fake current control before upload and immediately before activation. Unknown, malformed or changed control stops simulated writes/activation; observations and already-confirmed objects remain in the incomplete receipt. Tests 17–20 cover rejection, isolated current-control drift, and historical identity preservation.

2. **Stable verifier transport binding — closed.** Approved README:23 requires “绑定输入/plan、source/audit/control” and “原客户端 receipt 摘要和 exact target”. `prepare-object-storage-delivery.py:314–317,419` compares both original stable receipt transport digests with this preparation. Tests 21–22 cover missing/invalid/mismatched digests and a different preview/retained composite with the identical stable candidate. Receipts are retained and hashed unchanged, including their false provenance/publication fields.

The final scope retains strict original v1/v2 planning, source-bound materialization, exact ordered routes, create-only GET confirmation, partial-effect receipts, and disabled production. Existing planners, verifier interface, signing/control files and publication workflows are unchanged. Real provider/GitHub execution remains outside this slice.

Static review only: no tests, source edits, Git mutations, CI or cloud calls performed. Supplied completion evidence records 75 related passes, four contract tests, and 553 full-suite passes with one existing missing-RPM-tools skip; these results were not rerun by this reviewer.

Standards: 0 findings; Spec: 0 findings.
