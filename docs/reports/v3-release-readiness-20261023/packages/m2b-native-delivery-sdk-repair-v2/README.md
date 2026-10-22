# M2b review repair v2 — TEST ONLY

Seven-file source is frozen in `source-freeze.json` against packages `7255102de81b45c23d52e908ccdde1b7799ee3f5` / tree `cd49a22102235d60bda1a703a5b1145ab112c40e`. `implementation.patch` is the complete tracked plus untracked patch; `repair-implementation.diff` compares the preserved v1 source with v2. There are no commits, pushes, provider calls, new signing keys, or publication. Provider and safety-workflow bytes are identical to v1; production verifier, producers, control, lock, and original native/client suites are unchanged.

The three Spec findings are addressed by bounded actual SDK-stream observations and bound operation receipts; an independent post-self-check unexpected-connect counter; and separate primary-stage/cleanup errors with finite incomplete receipts. Object observations retain consumed-byte SHA/size/EOF/cache and source/plan/resource identities. Configuration receipts retain before/result/actual readback. Refresh receipts retain every actual submission and SDK-consumed allowlisted response rows, including missing/duplicate/partial/timeout outcomes. The fixture backend `RefreshWire.rows` is not used to reconstruct received responses. Signed URLs, authorization headers, tokens, and payload contents are excluded. Detailed receipt overflow produces a finite explicit incomplete receipt, never success.

## Tests-first chronology

1. `first-red-tests/`, SDK and input first RED records bind the v1 implementation before edits.
2. `complete-red-tests/` adds the remaining read(0)/close, interrupted-stream, timeout-attempt and overflow boundaries before implementation. Both complete RED runs failed as expected against unchanged v1.
3. `sdk-implementation-first` failed: the observer initially omitted iterable OSS PUT bodies, and a new duplicate-response expectation incorrectly expected eight rows where the existing fault returns five. The observer was corrected to tee the actual iterable; the expectation was corrected to the unchanged fixture's actual response. Neither the fixture nor old assertions were weakened. The failed run remains.
4. SDK 49/0 skips, input 16/0 skips, default 587 = 586 passing + one existing local RPM-tool skip, and four shell contracts completed green. New failure-composition tests deliberately isolate `main` failure seams; they are not positive mocks of native verification.

All v1 first failures and its 93 payload files remain byte-identical, including the earlier SDK/driver failures and the historical D2 two tests-after-code process deviation. This v2 work does not rewrite that history.

## Native first failure and bounded diagnosis

`native-first` exited 1 at materialize-B after preview, A/B seal/resolve, and materialize-A succeeded. The unchanged verifier reported `RPM signature must cryptographically verify with exactly one isolated candidate`; SDK execution had not started. All 120 public M1 input-file hashes match v1. The original failing tool output and temporary path were not captured, so the original cause remains **unknown**.

Root authorized one transparent replay of that exact inputs-B materializer with the same frozen source and unchanged container limits, using scratch PATH wrappers that forward real rpm/rpmkeys output. It passed in 15.265 seconds: 60 tool calls, eight checksig calls, all exit 0; observed memory/pids limit events were zero. This does not prove why the first run failed.

A separately authorized good/bad-path pair used the same already-signed public RPM (SHA `c929628919a768d5a9ad111e3e48ab3d2f0b556bb3a8b05a5ea8b513a1d4043c`) and public certificate. Both rpmkeys calls returned 0 with valid signatures/digests, but the original verifier function returned true for `/good/fixture.rpm` and false for `/bad/fixture.rpm`. This proves an independent path-substring false rejection in the existing verifier, **not** the cause of the unobserved original failure. No verifier change is included. Exact probe, actual outputs, input bindings, resource events and cleanup are in `native-diagnostic/`.

Both diagnostic containers were confirmed absent and their owned materialized trees were removed. Native/runtime completion, if subsequently authorized, is recorded independently in `validation-summary.json`; the failed first attempt is never counted as passing.

## Boundaries

TEST ONLY fixture evidence does not establish production source authentication, provider/cloud execution, actual CDN PCRE/query/propagation, complete installed public-client acceptance, or stable activation. Fresh production authentication and activation remain refused. Existing CI 29/31/49 suites and their original commands/deadlines remain required; local SDK/default results do not replace them. No actual provider credentials, private signing material, package payload trees, or SDK dependency environment are included in this finite archive.

## Final v2 validation

After the bounded diagnosis, root authorized one fresh, uninstrumented native→SDK run with the exact frozen seven files and unchanged resources/deadlines. Native preparation passed in 38.042 seconds; SDK completed 20/20 with no skips in 4.633 seconds. `native-validation/native-sdk.json` is 2,174,051 bytes: 768 actual SDK exchanges, 345 bound operations, and 306 positive object PUT/full-GET receipts with matching SHA/size/cache facts. All attempted writes, including faults, total 315 PUT and 30 refresh calls. Actual missing/duplicate refresh responses retain 177/179 rows respectively. Network self-check = 1, unexpected connects = 0; scratch and container cleanup are verified. The validation does not explain or erase the first failure.

The default first-green log also retains three `ResourceWarning: unclosed socket` observations from the deliberate network self-check/refusal probes. This is disclosed for review; frozen source was not changed to hide the warning. No commands remain running.
