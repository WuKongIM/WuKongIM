# PR #76 Linux CI generation 1 — failed, fully preserved

PR: https://github.com/WuKongIM/packages/pull/76 (draft, unmerged).
Reviewed head `dded26afa12188d5f1c036af4b9c06c3cbddb46d`, base `1829c3813d6b21dcfeb1238a640604a4691815bd`.
Actual checkout `3e4b7945206faffc18d6023f2b50311d93bd4348` is the exact two-parent PR merge preview; tree `e319ee72e8d30f012fd5e6206d131e2b75c47a09` equals the reviewed head. All runs were pull_request, attempt 1.

- [Pages 39644526518](https://github.com/WuKongIM/packages/actions/runs/39644526518): 567 Linux tests, zero skips; four existing contract tests passed. PR deployment job skipped as intended.
- [SDK 39644526769](https://github.com/WuKongIM/packages/actions/runs/39644526769): locked dependency install succeeded; SDK 22/22, inputs 10/10, zero skips. The uploaded SDK receipt binds exact checkout/tree/lock and records zero failures/errors/skips.
- [Native 39644526993](https://github.com/WuKongIM/packages/actions/runs/39644526993): signed APT/RPM job passed. Public metadata 7, signing 10, bootstrap 6, candidate 31 plus eight transport cases passed. Stable job 119376463184 failed.

First stable failure: `baseline-A-four-download-and-installed-cli`; `stable client verification failed: real installed client failed`. AlmaLinux container's `docker start --attach` exited 124 after 600.218 seconds; the command contains the existing inner `timeout --kill-after=10s 600s`. Package hash/signature checks passed, then the last output is Alma AppStream 30 MB / 05:59. No dependency-resolution-complete, transaction or CLI acceptance output was observed. The coordinator's 650-second deadline did not fire (`timed_out:false`). Cause remains unknown.

Only one of 49 cases was recorded: zero passed, one failed, 48 not reached. Ubuntu, Debian and Rocky installed subcommands succeeded within the unfinished baseline; they are not completed scenario passes. Request-barrier and cache scenarios were not reached. Reader cleanup and final cleanup both verified, no errors, no remaining containers or networks.

`first-failure.json` preserves the exact failed command, preceding container creation, assertions and cleanup. `pr75-comparison.json` binds 97 unchanged existing control files and matching four product binary hashes, images and host tools. #75 generation 3 had 49/49; that previous result does not pass this attempt. Same controls do not uniquely prove an environmental cause. `diagnosis-and-rerun-boundary.md` preserves the contract and bounded distinguishing-experiment recommendation.

All job logs, API snapshots and six artifact archives are local; archive digests match GitHub. `summary.json`, `source-binding.json` and `final-manifest.json` bind the final evidence. The earlier five-minute checkpoint is untouched and verified. All collection commands terminated. Only GitHub GETs and local evidence writes occurred; no source edits, tests, rerun, dispatch, cancellation, push or merge by this collector.

The historical Standards process P2 remains: two original HTTP wiring unit tests were written after implementation. No production activation/publication, live provider success or full new D2 native closure is claimed. Root will decide and dispatch the separately recorded single same-SHA failed-job-only attempt after archive readiness; this first failure remains permanent evidence.
