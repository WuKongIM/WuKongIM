# Legacy metrics diagnostic JSON compatibility correction

PR: https://github.com/WuKongIM/WuKongIM/pull/1031 (remains draft)
Head: `2662fe93b754d614422f2936185cf781a33b831a`
Tree: `1182b14d5b659eb60f2e7b694ff91b6641189106`
Parent candidate: `11debae7337dc0f0497ff732600d5ebf03949544`
Original release base: `8dfcd35b7d604fb5d3da3d75e36ab267e2f4b1bf`

Independent Spec review found a P3 wire-compatibility regression: decoding a partial object into an existing MetricsFetchFailure reset absent old mask/kind fields. A repeated outer metrics_fetch object containing only a malformed optional vector could therefore remove a previously valid diagnostic. The accepted review was read and its complete sealed manifest verified before production changes.

The correction adds exactly one assignment seeding the two old fields and two explanatory comments. The optional field still starts absent for each object. Omitted/null legacy scalars and arrays, explicit zero values, short arrays and null cells retain their prior default encoding/json behavior. Wrong optional data remains independently discarded; invalid whole JSON and old field errors remain rejected.

## Tests and preservation

A separate supplement2 file froze before the correction: 18 reused-receiver cases, 14 repeated-outer-object cases, and six legacy-field/whole-JSON error controls. An independent old DTO with default decoding is the oracle. The existing implementation produced a real RED (26 failing subcases plus two parent failures; 4.724 seconds), followed by identical-hash GREEN for all 38 subcases plus three parents (3.669 seconds). Nine source-test files are pinned: the original seven, the first three-null-position supplement and this new file. The original and first supplement bytes remain unchanged.

Final normal passed target, chatlifecycle and all cmd/wkcli packages (43.416 seconds; 2,605 parent/subtest pass events; 11 tested packages plus three packages without tests). Final complete race passed the three related packages (118.276 seconds; 2,321 parent/subtest pass events). Policy-named go-format and flow-doc-contracts passed with their original policy arguments. Existing FLOW/knowledge/CHANGELOG text remains accurate; no contract or canonical index change was needed.

The complete parser client still normalizes byte-for-byte to the original release base after only its 30 fixed constructor substitutions across 20 families. It is unchanged from the previous candidate. The entire wire file is unchanged from the prior candidate after removing only the seed and two comments. Every other tracked path remains unchanged from that candidate. All original v1 evidence and the failed Spec round remain separately sealed and untouched.

## Limits

The prior head's unfinished CI generation is not reused. Updating the branch triggers automatic checks for the new head; the creation snapshot is not CI acceptance. No workflow dispatch, enable, rerun, ready, merge, runtime workload, DB access or cloud operation occurred. The original missing scrape cannot be reconstructed, and the retained presence timeout/message_loss remains unresolved; v3 GA is still NO-GO. The clean unmerged task worktree/branch is retained for exact-head independent review.

Raw test/check output logs are excluded from the public payload allowlist and represented only by finite size/SHA metadata. The prior verified named-check helper was reused read-only; no product executable was built or started.
