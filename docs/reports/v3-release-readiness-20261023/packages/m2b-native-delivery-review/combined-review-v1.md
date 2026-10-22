# M2b review — frozen seven-file candidate v1

Source freeze `123aa28a8aa8e38e8f3f3a53298b4201825db6e2f578dfbb984770375f21d831`; base `7255102de81b45c23d52e908ccdde1b7799ee3f5`. The reports below are independent axes; the original sources remain unchanged.

## Standards

Standards review: **0 documented-rule violations; 0 new actionable heuristic findings** in the seven-file frozen delta at `7255102de81b45c23d52e908ccdde1b7799ee3f5`. HEAD/tree, empty commit/staged lists, the untracked driver, all source hashes and complete patch match the supplied freeze.

Root `AGENTS.md:129–132` test-first requirements are supported by the prewritten matrices, saved case assertions and retained RED receipts. The deliberate scaffold exit is not counted as native verification. The first SDK implementation labeled “green” actually failed; its failure, the first native fixture KeyError, and later bounds/side-effect/cleanup REDs remain preserved. Final logs show SDK46, input13, contract4, default583 plus one existing skip, and native20 passing. No all-tests-green claim is inferred for the pending Linux 29/31/49 suites.

`Provider.refresh` documents exact File coverage, possible effects and the caller-owned deadline (`scripts/aliyun-object-storage-provider.py:403–410`; root `AGENTS.md:88–91`). It retains bounded submissions/queries and original poll limits. Provider AST outside that method and 121 protected source paths are unchanged. Removing the added workflow block reproduces the original workflow bytes. The new steps retain credential-free execution, bounded native resources, locked SDK installation and always-run owned cleanup; no production authority is introduced (root `.github/workflows/README.md:1143–1147`; packages `docs/release-contract.md:16–37`).

The driver preserves native verification, disabled resource intent, explicit dummy SDK identity, false production flags and actual refusal entrypoints. Updated documentation separates TEST ONLY SDK completion from live/public acceptance (`docs/object-storage-delivery.md:306–335`), and CHANGELOG has an Unreleased entry.

All twelve smell heuristics were assessed as judgment calls with repository rules taking precedence; no new actionable design issue was found. No package-specific AGENTS/FLOW/CONTEXT applies. Read-only container queries independently found both exact owned containers absent, and both scratch paths are absent; this is not an OS-wide inventory claim. No source edit, test rerun, CI dispatch or provider call occurred during review. Hash bindings, finite evidence inventory and terminal receipts accompany this report.

## Spec

Reviewed the seven frozen working-copy files at base/HEAD `7255102de81b45c23d52e908ccdde1b7799ee3f5`, including the untracked driver; commit list is empty. The later authorized aggregate-refresh contract supersedes historical “unsupported” wording.

- **P2 — Required public evidence is discarded** (`native_object_delivery_sdk_drill.py:360–363`). The plan requires “每个object及观测PUT/GET、全部config before/after、URL完整集合” (`provider-m2b-plan/README.md:48`). Execution assertions cover these, but the successful public receipt retains only 354 write rows with method/action/object_key plus counts. Full GET facts, configuration before/after and exact refresh coverage disappear when scratch is removed; CI does not upload the bundle. Retain bounded, sanitized facts and partial results.

- **P2 — A forbidden socket attempt can satisfy an expected refusal** (`native_object_delivery_sdk_drill.py:397–399`, classifier:222–225). The failure matrix requires socket attempts to produce a nonzero driver and finite receipt (row 28). A bounded probe intercepted one connect, yet `refused()` returned normally because the guard raises an accepted `RuntimeError`. Provider wrapping can also translate that error. Count guard attempts outside exception classification; after the explicit self-check, require zero unexpected attempts across the SDK stage. No real connection occurred.

- **P2 — Cleanup failure prevents the incomplete receipt** (`native_object_delivery_sdk_drill.py:410–414`). The same matrix row requires “cleanup失败 … driver非零、always有限收据”. Injecting a stage error followed by `rmtree` failure raised `PermissionError` before any receipt was written. Preserve the original error/attempts, record failed cleanup separately, and still write the bounded incomplete receipt.

Existing author receipts show native 20/20, SDK 46, input 13, contracts 4 and default suite passing; these do not close the three gaps. No additional aggregate-refresh or scope-creep finding. Seven source hashes matched before/after. No source changes, full-suite reruns, or cloud calls.

Standards: 0 hard violations, 0 new actionable heuristics. Spec: 3 findings, worst P2; repair required before acceptance.
