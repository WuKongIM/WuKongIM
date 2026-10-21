# Pprof request-registration cancellation repair

Frozen base: `2cc562fa39f7b430c61a4494636f6ee0ec0d631a`. Four files change: helper, integration test, CHANGELOG, PROJECT_KNOWLEDGE. No policy/workflow/threshold/parallelism changes, full-suite rerun, CI rerun, commit or push occurred in this implementation round.

The helper marks background curl launch through complete PID/tmp/metadata ownership registration as a short critical section. HUP/INT/TERM received there only sets a pending flag, leaving `$!` intact; the original termination/join/metadata cleanup runs immediately after all ownership records are present. Cleanup also handles zero-child/zero-temporary arrays safely under Bash3 nounset. Process selection remains exactly registered child PIDs; timeout values are unchanged.

Tests were written before the helper change. Six real curl/HTTP cases first failed on the original source: first-child and node2 registration windows × HUP/INT/TERM. Node1 exposed the Bash3 empty-array error plus leaked request; node2 reproduced normal helper exit with interrupted metadata and leaked request. Three additional prelaunch HUP/INT/TERM cases first failed the empty-array cleanup path before any request. Both first RED artifacts/logs remain immutable. The original probabilistic interrupt regression and every other pre-existing integration test body remain byte-identical.

After repair, one full related run passed: 15 top-level tests / 38 PASS records, package 2.256 seconds. This includes all static contracts, existing profiling/context/auth/idempotency/bounds tests, the original interrupt test (0.06 seconds), and all nine new deterministic subcases. Every new case reports successful helper exit, partial/interrupted metadata, zero active requests, no surviving child, and no temporary files. `green-first-artifact.json` is the repeatable result artifact; `.jsonl` retains the exact first Go output.

Commands used (all with `GOWORK=off`, `-count=1`, `-parallel=2`):

- Initial RED: integration tag, `./scripts`, `-run '^TestLocalThresholdPprofInterruptDuringRequestRegistration$'`, `-timeout=90s`.
- Additional prelaunch RED: same integration package, `-run '^TestLocalThresholdPprofInterruptDuringRequestRegistration$/^node0_'`, `-timeout=30s`.
- First related GREEN: integration package, `-run '^TestLocalThresholdPprof'`, `-timeout=2m`.

Every run sets `LOCAL_THRESHOLD_PPROF_SIGNAL_TEST_ARTIFACT` to its distinct JSON path and records Go JSON output. This fixture variable is outside the product WK configuration namespace. `bash -n` and `git diff --check` also passed.

`review-source-freeze.json` freezes the exact four-file candidate and applicable AGENTS digest for dual review. Its SHA-256 is `b7c7cc4c1ea841a8f97b0998760c739291f85d5414eabefa38756fcc2d54a351`. The original tests' text invariance is verified after removing only the newly added function and its imports. No applicable FLOW covers scripts root.

This local focused result does not certify the original Linux 600-second named check or v3 release. The prior original CI failure and read-only causal probes remain sealed in `pr1024-pprof-interrupt-diagnosis-public.zip`. The reviewed next head still needs its first original full Linux CI. Cleanup and final source identity are recorded in delivery-receipt.json; the finite public ZIP includes only the allowlisted repair evidence.
