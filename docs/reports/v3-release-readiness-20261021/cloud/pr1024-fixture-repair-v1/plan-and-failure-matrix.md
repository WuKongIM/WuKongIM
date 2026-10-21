# Existing-test-first fixture optimization

Only `scripts/testdata/chat_lifecycle_terminal_evidence_test.py` may change. The exact c91 source, all 20 existing test-method text hashes, original first Linux timeout, and original completed V2 79-case artifact are frozen in before-freeze.json. The existing first Linux policy failure is the RED feedback for performance; no repeat of that suite or invented implementation-coupled unit test is required. The unchanged 79 real collector subprocess scenarios are the behavior specification written before this optimization.

The accepted seam is unchanged: the real Bash collector, real Python terminal helper, local fixture SSH process boundary, manifest/call/retained-file artifact. Keep all Go entrypoints and collector production code unchanged. No protected paths, workflows, policies, limit/parallelism changes, or new test tiers are needed. This change is test-only and does not alter user-visible behavior; the existing PR's operator-facing Changelog already describes the collector behavior. Do not add a new release-note claim of Linux performance recovery before the gate actually passes.

## Failure matrix preserved by existing scenarios

| Boundary | Existing failures or success variants that must survive unchanged |
|---|---|
| Terminal anchor | wrong schema, nonfinal, missing end, future end, backward end; invalid anchor sends no metric requests |
| Formal/capacity | valid terminal outcomes, success that still requires capacity, unsupported outcome/cause pairs, missing phase proof, recovery/attempt/completion/rate inconsistencies, missing capacity report, submillisecond stage ordering |
| Worker/status transport | missing, empty, malformed, exact byte cap, cap+1, oversized, unavailable, partial stdout with nonzero exit, timeout |
| Worker/status identity | schema, run ID, timestamp, duplicate worker, generation, sequence, unsafe fields or diagnostic codes |
| Metric transport/data | unavailable, partial, timeout, malformed, wrong result type/family, API error, empty family, large response, oversized response; valid sibling partitions remain |
| Actual query | selector and order, actual start/end/step, millisecond start/end inclusion and next-point exclusion, invalid submillisecond sample; raw report precision remains |
| Budgets | eight partitions, original selector union/disjointness, exact new low-cardinality families, shared transfer budget, shared deadline, partial successful retention |
| Artifacts | exact exit/complete/states/query/window/budget, request sequence, all file names/bytes/SHA; no canary disclosure or temporary leftovers |

## Implementation constraints

1. Prepare each fixture response body and its exit/stderr disposition once in the existing Python driver. Large response files remain files.
2. Shell SSH adapter logs each actual command in NUL-delimited form, dispatches the actual stage/document or metric family, clips with the actual `head -c` limit, then applies the original transport exit. Preserve unavailable's stderr canary and partial stdout.
3. Parse actual command records once after the collector completes using the same shlex/query interpretation as the original per-SSH Python. Build the exact original `calls` artifact shape/order from these records, never from planned calls.
4. For parameter-dependent boundary responses, prepare from independent fixture report time and explicitly verify actual query start/end match those bounds before serving that prepared response. An incorrect query must fail the fixture instead of receiving a silently convenient expected response. No response or call is derived from the collector manifest.
5. All test_* methods and 79 run_case inputs/assertions remain byte-identical. `run_case` setup may change to prepare and parse the adapter. No background daemon or parallel case execution.

## Validation and evidence

Run the original two focused Go integration tests once and preserve the first output plus original-format case artifact, regardless of outcome. Compare all 79 cases against the exact existing V2 artifact; labels must match, only `manifest.observed_at` and the derived manifest-file SHA may normalize after verifying both original raw hashes/byte counts. Preserve all other fields exactly. Record source hashes and method invariance. If failure reveals an adapter defect, save each generation before repairing it. Do not repeat a 12-minute Mac full suite; a new reviewed head's first original Linux named check is the only evidence that can settle the policy timeout. No commit or push before the parent has both independent reviews.
