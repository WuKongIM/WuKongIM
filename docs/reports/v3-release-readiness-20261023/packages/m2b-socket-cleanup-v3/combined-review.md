## Standards

## Standards — v3 socket-cleanup delta

No new mandatory violation or actionable baseline heuristic. The reviewed delta changes only `tests/provider_sdk/native_object_delivery_sdk_drill.py:543` and the guard-failure probe in `tests/test_object_storage_delivery_inputs.py:259`. The remaining five files retain their accepted v2 hashes.

Explicit `with socket.socket()` ownership closes the self-check and injected failure sockets even when the unchanged guard raises `RuntimeError`. The independent attempt counter, refusal classifier, primary-error handling and all previous assertions remain intact. This is a narrowly scoped resource-lifetime fix, consistent with root `AGENTS.md`’s code-quality/testing rules and the documented fixture cleanup boundary.

The zero-`ResourceWarning` assertion and forced collection existed in the frozen RED tests before the socket fix. RED binds the v2 driver and records four failing modes; the supplied 16-case GREEN binds v3 and records no warning. This reviewer inspected those receipts without rerunning tests. The v2 native 20-case result is not relabeled as v3 validation: final-source Linux CI remains pending.

All twelve Fowler heuristics were reconsidered for this delta, with repository rules taking priority. The small test helper centralizes deliberate connection attempts; no unnecessary abstraction or actionable duplication is introduced. No package-local `AGENTS.md`, `FLOW.md` or `CONTEXT.md` was found. All seven source hashes, exact two-file delta and complete tracked-plus-untracked patch were verified. No source changes, cloud calls or Spec-report reads occurred.

Summary: 0 new mandatory violations; 0 new heuristic findings. The earlier socket warning is resolved in focused v3 evidence; original v2 logs and historical limitations remain preserved.

## Spec

# Spec — v3 socket-cleanup increment

**No findings. The earlier non-blocking socket-cleanup observation is resolved.**

The failure matrix requires four existing clean/refused/converted/primary-error scenarios to emit zero ResourceWarning after collection while retaining independent attempt counts, failure/cleanup receipts and the primary error. The two-file delta satisfies this: the self-check and deliberate test connections now own sockets in context managers, including exception paths. `RuntimeError`, refusal classification, the independent counter and all existing receipt assertions are unchanged. The one-attempt self-check remains inside the connect guard.

Tests-first evidence binds the first RED to the frozen v2 driver: 16 methods, four failing subcases. The new warning assertion was present before the implementation changed. The v3 GREEN records 16 methods, zero failures/skips and no ResourceWarning. These supplied results were inspected, not rerun.

All seven frozen source hashes matched before/after; only the intended two test files differ from v2. Provider, workflow, SDK contracts, documentation and Changelog hashes remain unchanged. The previously reviewed v2 evidence remains applicable to unchanged behavior, but the final 20-case native run on v3 still awaits Linux CI. This increment neither substitutes that validation nor makes a production/cloud acceptance claim.

No source edits, SDK/native execution, containers, cloud calls or network probes occurred during this review.

Standards: 0 new mandatory or heuristic findings. Spec: 0 open findings.
