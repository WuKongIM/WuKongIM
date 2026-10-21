# PR #75 request-barrier repair review

## Standards

# Standards review — packages PR #75 request barrier

**No findings.** No documented-standard violation or actionable heuristic smell was found in this four-file delta.

Scope: uncommitted HEAD diff at `eac9d252f13ea201b405825e6303de703af08c16`, verified candidate tree `50e099e7adcda9e205c9862dad7e1071022d2a3c`. All four live files match the supplied frozen copies and patch; there are no nonignored untracked files. The other 114 tracked files match HEAD byte-for-byte, including production validation, publisher, manifests, workflows and keys. Tree verification was calculated in memory without writing Git objects.

Applied rules: user-supplied working agreements and AGENTS quality, test-first and changelog requirements; package README and release contracts. No package-local AGENTS.md, FLOW.md or CONTRIBUTING.md exists in the discovered scope.

The added helper has an English responsibility comment and only changes the explicitly named TEST ONLY RPM switch phase (`tests/native_stable_clients_drill.py:444`). It rejects ambiguous command boundaries and retains the original production command builder. This narrow adapter avoids extending production interfaces for fixture-only behavior. Exact selected bytes, completed responses, same-case serial and ordered B mirror/payload evidence remain explicit (`tests/native_stable_clients_drill.py:132`). This is consistent with `docs/stable-audit-transport.md:145` and preserves the production/TEST ONLY boundary in `docs/release-contract.md:751`.

`CHANGELOG.md:5` and the operator contract explain the changed test behavior without claiming stable publication. Frozen tests-before-code bytes match the final test file; existing red/green receipts show 44 targeted tests, with the initial missing-seam errors retained. Existing final receipts also record 557 Python tests (one skip) and contract exit 0; I did not rerun them.

All twelve supplied smell heuristics were considered. None warrants a finding: no new speculative abstraction, production coupling, scattered configuration or duplicated business rule was introduced. These are review judgments, not additional mandatory rules; formatter/tool checks were not duplicated.

Limits: this is Standards review only. It does not certify the pending new-head complete native 49-case execution, public-origin acceptance, or a precise retrospective CI cache-age timestamp. All reviewer commands terminated; no repository edits, API calls, test runs or cloud operations were performed.

## Spec

# Spec review — PR75 request-barrier repair

**No actionable omissions, partial requirements, incorrect implementation, or scope creep found.** Reviewed exactly four working-copy files at HEAD/base `eac9d252f13ea201b405825e6303de703af08c16`; no branch commits or untracked sources. All frozen/source-copy hashes and full patch bytes match. Candidate tree: `50e099e7adcda9e205c9862dad7e1071022d2a3c`; patch SHA-256: `31b799475c7dbf3c88de48d017ff21a1e4f94caa649015166b912533be2d560a`.

The approved phase contract says “only that case's second DNF invocation” uses `wukongim-stable.metadata_expire=-1` (`docs/stable-audit-transport.md:149–158`). `download_client_command` (`tests/native_stable_clients_drill.py:444–459`) limits the override to RPM request-barrier mode, verifies unique ordered phase boundaries, copies the command, and changes only the second install invocation. Signed `makecache --refresh`, production/package configuration, fresh-B, persistent-cache, negative and APT cases retain their existing behavior. No finite freshness window or global cache-only mode is added.

The contract requires B's exact mirrorlist “before an exact-A payload response under B's planned prefix, within the same case” (:154–164). The caller passes the actual B audit ID (:917–919); the proof requires one positive integer serial and ordered B mirror/payload evidence (:204–219). Existing validation still verifies A metadata, exact planned source/member, SHA/size/cache facts, status and completed response writes before the order predicate can pass (:151–203). A-only, unrelated retained-C, mixed/missing serial, reversed order and incomplete/corrupt responses have explicit rejection coverage. Exact downloaded A acceptance remains unchanged (:903–910).

The original D files, production validator and workflow are byte-identical to base; the 49-case inventory is unchanged. The CI evidence records signed B selected where A was expected, without establishing a unique cache-age cause. The signed diagnostic mechanism does not substitute for the full 49-case native suite.

Static review only; no tests/CI/cloud/source changes. Supplied terminal evidence records 44 related passes, 557 full Python tests with one skip, and four contract passes. No full repaired native-49 success is asserted.

Standards: 0 findings. Spec: 0 findings. Native Linux acceptance remains separate.
