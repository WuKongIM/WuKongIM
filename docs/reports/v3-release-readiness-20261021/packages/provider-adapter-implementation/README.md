# D2 optional provider library — local review handoff

Status: eight-file uncommitted implementation, frozen for independent review. Base/head `d824fe37918ee4ef22b1b1745bc02518812563e3`, base tree `50e099e7adcda9e205c9862dad7e1071022d2a3c`, branch `codex/stable-object-provider`. No commit, push, PR, workflow dispatch or provider request was made. No real cloud credentials or production signing keys were accessed.

## Exact scope

- `scripts/aliyun-object-storage-provider.py`: explicit-resource, explicit-temporary-credential official SDK library; create-only OSS and streamed GET, full CDN observations, bounded configuration primitive, exact refresh task binding. Per-instance SDK transport, retries/proxies/redirects disabled. Static literal rewrite candidates only; `activate()` always refuses.
- `scripts/verify-object-storage-delivery-inputs.py`: read-only original v1/v2 planner/helper wiring, fresh GitHub/source/audit/control identities and repeat-observation fence; clean exact executor checkout required. Historical candidate/seal and current executor identities remain distinct.
- `tests/provider_sdk/test_aliyun_object_storage_provider.py`: 17 mandatory real-SDK serialization/response tests with injected HTTP adapter and no provider sockets.
- `tests/test_object_storage_delivery_inputs.py`: 10 stdlib tests for input/helper wiring and CI contract.
- `requirements-object-storage-provider.lock`: four exact official SDK versions and all transitive pins/hashes, wheel-only installation.
- `.github/workflows/object-storage-provider-contract.yml`: new independent contents-read PR/main safety CI, installs lock and rejects dependency/import failure, zero SDK tests or skips. It has not been run on GitHub.
- `docs/object-storage-delivery.md` and `CHANGELOG.md`: operator boundary and remaining unsupported work.

`source-freeze.json` binds exact source copies and complete tracked/untracked `implementation.patch`. `unchanged-tracked-files.json` verifies all other 116 tracked files unchanged, including original D materializer/simulator, producer controls and production client validator. Applicable rule/plan digests are in `initial-binding.json`; packages has no applicable FLOW file.

## Validation and real process order

Initial 17 SDK and 8 input/CI tests were written before the two implementation files; both commands failed and their hashes/absence checks are in `first-red.json`. Original red logs and all correction attempts are preserved.

**Process violation:** the two `FreshPreviewWire` HTTP wiring tests were written after the corresponding input wiring implementation and first ran green (`fresh-boundary-first-run.log`). This violated the explicit project tests-first requirement. They are retained because they add useful coverage; there is no retrospective first-red claim, and the complete suite is not claimed to have been tests-first. Later dirty-executor and distinct-but-wrong ConfigId regressions each failed before their corresponding fix, then passed. The earlier ConfigId experiment accidentally reused one duplicate fixture ID and was green; that log is also retained before the distinct-ID first red.

Clean hash-locked wheel installation passed (`locked-install.json`). Final frozen SDK suite: 17 tests, zero errors/failures/skips (`sdk-contract-final-receipt.json`, `sdk-final-frozen.json`). Final focused input suite: 10 passed (`inputs-final.json`). Final complete default suite: 567 tests, zero failures/errors, one existing skip because real RPM build tools are unavailable (`all-default-final.json/.log`). The final full run and SDK run bind source hashes and confirm no changes during execution. Four Python files parse; `git diff --check` passes. No new CI or live provider test ran. Validation timeouts were 120 seconds focused/SDK, 360 seconds default suite; all commands reached terminal state.

## Explicit limits and remaining work

Production activation is unsupported as approved. Literal rewrite serialization is not proof of cloud PCRE, percent-encoding/query normalization, parameter bounds or POP behavior. SDK primitive receipts keep production/publication/cloud/wire-equivalence false. There is no production writer composing fresh input fence with materialization/resource/write phases, no production origin/private-access/TLS/public-client acceptance, no authorization discovery or resource management.

The v1 positive wiring fixture exercises original planner and actual GitHub/source/audit resolvers at the HTTP boundary, but substitutes the existing native helper subprocess boundary. It verifies helper invocation/failure, not native cryptographic success. A complete production-shaped v2/native-signature positive path is not demonstrated by new fixtures; existing helper tests remain relevant but do not replace that missing integration evidence. See `failure-inventory.md` and `sdk-selection.md` for exact coverage and SDK choice.

Local installation used Python 3.13.5; the Ubuntu CI environment remains unexecuted. The two rejected/exploratory virtual environments were removed. The hash-locked environment is retained outside the public evidence allowlist for review/reproduction. No background task remains. The parent is separately resolving #75; its current draft/red base must be replaced by the finally accepted merged head before publication/rebase. This is not complete original-D delivery or release acceptance.
