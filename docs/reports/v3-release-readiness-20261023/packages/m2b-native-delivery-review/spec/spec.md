# Spec

Reviewed the seven frozen working-copy files at base/HEAD `7255102de81b45c23d52e908ccdde1b7799ee3f5`, including the untracked driver; commit list is empty. The later authorized aggregate-refresh contract supersedes historical “unsupported” wording.

- **P2 — Required public evidence is discarded** (`native_object_delivery_sdk_drill.py:360–363`). The plan requires “每个object及观测PUT/GET、全部config before/after、URL完整集合” (`provider-m2b-plan/README.md:48`). Execution assertions cover these, but the successful public receipt retains only 354 write rows with method/action/object_key plus counts. Full GET facts, configuration before/after and exact refresh coverage disappear when scratch is removed; CI does not upload the bundle. Retain bounded, sanitized facts and partial results.

- **P2 — A forbidden socket attempt can satisfy an expected refusal** (`native_object_delivery_sdk_drill.py:397–399`, classifier:222–225). The failure matrix requires socket attempts to produce a nonzero driver and finite receipt (row 28). A bounded probe intercepted one connect, yet `refused()` returned normally because the guard raises an accepted `RuntimeError`. Provider wrapping can also translate that error. Count guard attempts outside exception classification; after the explicit self-check, require zero unexpected attempts across the SDK stage. No real connection occurred.

- **P2 — Cleanup failure prevents the incomplete receipt** (`native_object_delivery_sdk_drill.py:410–414`). The same matrix row requires “cleanup失败 … driver非零、always有限收据”. Injecting a stage error followed by `rmtree` failure raised `PermissionError` before any receipt was written. Preserve the original error/attempts, record failed cleanup separately, and still write the bounded incomplete receipt.

Existing author receipts show native 20/20, SDK 46, input 13, contracts 4 and default suite passing; these do not close the three gaps. No additional aggregate-refresh or scope-creep finding. Seven source hashes matched before/after. No source changes, full-suite reruns, or cloud calls.
