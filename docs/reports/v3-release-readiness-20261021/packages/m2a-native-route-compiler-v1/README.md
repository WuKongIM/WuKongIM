# M2a finite native route compiler — local frozen delivery

This five-file implementation extends only the offline SDK route compiler. It does not implement M2b, a publisher, cloud resources, public acceptance, or production activation. The fixed native A/B maps each retain all 178 routes and 153 object facts and compile to 17 finite rules (maximum expression 1017 bytes). Seventeen is the result of the approved fixed grouping and packing, not a global optimum.

## Exact source

Worktree: `/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/packages-native-route-compiler`; branch `codex/native-route-compiler`. Base/head `bbcfb377c7d5bb23742ebf28394a0cbc4f45cc17`, tree `0c843c1a30df5a75eac5e8ac0b966b5b84e20036`. The original `packages-native-preview-fixture` worktree remains clean and retained.

`source-freeze.json` SHA-256: `34d2361fc94b979366503e36f25fe8e9ab4ed8e256364ad05d2ab9191276f87b`.
`implementation.patch` SHA-256: `21095c12dd8928e31f3894280dbd188806d6a68b5da7c84040a68f917f7d8e18`.
The full patch includes four tracked modifications and the new public JSON fixture; `source/` holds exact copies.

Changed files:

- `scripts/aliyun-object-storage-provider.py`: compile_rewrites only; all other module AST is identical.
- `tests/provider_sdk/test_aliyun_object_storage_provider.py`: twelve new test methods and finite-map decoding helpers; all original 22 test methods remain unchanged.
- `tests/provider_sdk/fixtures/native-composite-routes.json`: bounded public source data from the original native readback.
- `docs/object-storage-delivery.md`.
- `CHANGELOG.md`.

No workflow, lock, producer, key, channel, planner, materializer, fresh verifier, or client validator changes. `protected-source-binding.json` binds 33 relevant unchanged files including existing SDK/native workflow commands and original M1/31/49 drivers. Applicable root rules, absence of package AGENTS/FLOW/CONTEXT, contract, skills and approved plans are frozen in `initial-binding.json`.

## Semantics and public fixture

Validation and exact URI fact deduplication run first. At most 50 unique routes return the original literal bytes and order, including 51 inputs with one duplicate. Larger maps group only exact snapshot identity, `public` plus URI aliases, and exact fixed snapshot/site target-prefix relationships. Root, generated mirrorlist and unproved mappings stay literal; 51 arbitrary `/pN` paths sharing a key still fail. There is no wildcard or generic same-target coalescing.

Each group enumerates escaped full suffixes within one capture, ending in absolute `\z`; target is the fixed prefix plus `$1`. Lexicographic chunks respect the 1024-byte bound, retain earliest-original-route sequence order, and must total at most 50 rules. Source_url remains the first argument and configuration identity. Any required deletion, including literal-to-group transition, changed chunks, or removed source, remains rejected before writes by the unchanged SDK apply primitive.

The fixture embeds original canonical delivery/plan/inputs/resolution JSON, all route rows and object/source/cache facts, external archive/receipt and separate control/source anchors, original manifest text and source binding. Tests reserialize each original document to its archived SHA, verify the original routes SHA and fixture byte anchor, and independently inverse-expand every finite rule to the exact 178 URI→target map. Local Python regex tests reject adjacent strings. The original archived public material had actual native verification; this task did not regenerate signing keys, rerun those signatures or mock a verifier. Historical temporary paths stay historical data and are not production inputs. TEST ONLY and false authorization fields remain explicit.

Real SDK HTTP contracts serialize all 17 functions at the existing injected transport boundary, verify full configuration readback, and demonstrate A→B reuses its source IDs with exactly two changed targets. No object PUT, refresh or activation is introduced. Grouped ConfigId ambiguity, partial execution and timeout preserve incomplete receipts with a single attempted batch; configuration tampering, resource/domain mismatch, drift, unmanaged state, deletion and overcapacity reject before writes.

## Tests-first chronology

`failure-matrix.md` was written before tests and code. The approved seams are compile_rewrites and the existing official SDK HTTP adapter. Root's all-failure-tests-first rule takes precedence over the tdd skill's vertical-slice default.

1. `first-red-tests.py` and `first-red.*`: unchanged provider, original 22 pass, total 34 methods, 15 error subcases from the expected existing 50-rule rejection, exit 1, zero skips.
2. Before implementation, static review found two synthetic boundary tests counted the raw prefix and omitted the escaped hyphen byte. `test-boundary-review.md` records the correction. Original RED is preserved; `preimplementation-tests.py` and `preimplementation-red.*` record the corrected complete test set against unchanged provider: same expected 15 errors, exit 1.
3. `sdk-implementation-first.*`: first implementation run passes all 34 methods, zero errors/failures/skips, 0.357 test seconds. Test bytes are identical to preimplementation RED. No tests were added or weakened after implementation.
4. `inputs-first.*`: all 10 fresh-input/CI contracts pass, zero skips, including TEST ONLY rejection.
5. `contract-first.*`: publication/repository contracts and four embedded tests pass.
6. `default-first.*`: 581 tests run = 580 passed + 1 existing local skip, 134.737 test seconds; exit 0 within the 600-second limit. All validation commands are terminal.

The existing locked SDK environment was reused; no dependencies or provider endpoints were fetched. Its actual versions and unchanged lock digest appear in SDK receipts. Existing CI already triggers for scripts/tests and runs the entire mandatory SDK entry; it rejects zero tests or skips. No workflow change or dispatch was required here. Existing 29/31/49 commands remain in their original CI paths; these were not rerun locally and are not claimed green by this delivery.

## Limits and handoff

No live Alibaba PCRE, query/URI normalization, cache behavior, propagation or actual cloud side effects are proven. Exact finite representation and SDK serialization are separate from live routing equivalence. Full153-object create-only/GET integration remains M2b; unchanged per-object SDK tests continue to cover their current contract. Fresh production authentication and activate remain fail-closed. Earlier D2 tests-after-code history and all original failures remain in their original evidence directories.

No commit, push, PR, external CI, provider call, production credential access or cloud action was performed. Source is frozen for independent Spec/Standards review. `summary.json` records final process status and test counts; `public-allowlist.json` and `manifest.json` delimit bounded public evidence and exact hashes. No venv, large package tree, signing secret or credential file is included.
