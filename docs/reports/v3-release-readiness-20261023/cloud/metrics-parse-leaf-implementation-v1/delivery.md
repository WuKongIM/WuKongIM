# Typed metrics parser reasons: implementation delivery

Draft PR: https://github.com/WuKongIM/WuKongIM/pull/1031
Head: `11debae7337dc0f0497ff732600d5ebf03949544`
Tree: `5ac5aa9cbbedcc75696ef45811245a205ad8c471`
Base: `8dfcd35b7d604fb5d3da3d75e36ab267e2f4b1bf`

The 20 fixed parser guard families now retain their original private error text in typed causes. The optional three-position parse_reasons projection exists only for parse-kind slots; historic missing fields stay missing, and malformed optional fields are dropped independently. Entire JSON syntax and old-field type errors remain rejected. No runtime product fix or release qualification is claimed.

## Test sequence and limits

- The approved 52-case design matrix and original seven test files were frozen before production changes. The original test-pin document remains `8c0d7d4e066f82318778cb801693812edb14add539351d97d37d837205902910`.
- The first compile RED is not behavior evidence. An initial malformed minimal DTO shell was retained separately and is not counted as a complete behavior RED. After correcting that shell alone, all three packages compiled and produced a complete behavior RED (6.234 seconds); the same frozen tests then passed (6.670 seconds).
- WIP review found JSON null elements accepted by Decode(&string). Three cases in a separate supplemental test file froze before correction and each failed, at parse/success/transport positions. The decoder changed to Token plus string assertion; the identical three cases passed. Supplemental pins: `e687376c70b6d4f598f8b11c71cda6dcf179f95d28f657b4f8235d987c19121b`. Original seven file bytes did not change.
- Final normal: target, chatlifecycle and all cmd/wkcli packages; 2,564 test/subtest pass events, 11 passed packages plus three packages with no tests, 61.059 seconds. Final race: three related packages, 2,280 test/subtest pass events, 127.319 seconds. These counts include parent tests and subtests, not 2,564 independent scenarios.
- Policy-named go-format and flow-doc-contracts passed. Canonical FLOW index regenerated; existing length warning categories remain.

## Preservation proofs

The complete target/client.go becomes byte-identical to the exact base after replacing only 30 constructors with their original fixed errors.New literals. Every guard, branch, HTTP acquisition/fallback, limit and order remains byte-identical under this normalization. The entire metadata collector likewise becomes byte-identical after removing only the fixed three-reason array and parse-kind projection. Seventy-eight other relevant production Go files are independently byte-bound unchanged, including observation, report validity and CLI behavior; the final Git tree proves all paths outside the fourteen-file allowlist unchanged.

## Review and runtime boundary

The PR remains draft. First automatic checks were observed in progress, with the wake-up success and two optional checks skipped; that snapshot is not CI acceptance. No dispatch, enable, rerun, ready, merge, runtime workload, database access or cloud operation occurred. The retained presence timeout/message_loss and incomplete report still block v3 GA. The clean unmerged task worktree and branch remain available for review and must be removed only after the target contains this tip.

Raw test/compile output logs and the helper executable are excluded from the public payload allowlist; their finite byte counts and hashes are metadata only. The current failed scrape was not retained and cannot be reconstructed by this patch or by the earlier six-cut replay.
