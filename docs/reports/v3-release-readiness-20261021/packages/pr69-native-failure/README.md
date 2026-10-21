# PR #69 native TEST ONLY failure and narrow fixture repair

Original d049073 run 39564810498 attempt 1 / job119104436724 failed at 2026-10-21T07:50:58Z. Original API, logs and public JSON are retained unchanged. It passed the preceding seven metadata cases; local signing failed before independent output verification and later native stages were skipped.

The old harness discarded the signer stderr, so the original log only narrows failure to a positive RPM invocation. A separate lightweight regression exercises actual prepare_inputs (APT native command output mocked; no native process) and the unchanged actual signer loaders. All three allowlists reject with `must use canonical JSON encoding`. New/signed/active now use sorted compact JSON plus newline; the signer canonical rejection remains tested. This is a confirmed fixture defect, not a loosened signing check.

Also retain exact case/family/input mode/exit code plus only the signer's bounded top-level diagnostic in the existing always-written public failure JSON. Exact TEST ONLY secret fixture values are redacted; arbitrary stderr is not copied. Negative no-output/no-stage requirements are unchanged.

Tests-first: diagnostic 5 tests initially 3 failures/1 error then pass; canonical loader 7 tests initially 3 failing subcases then 7 pass (1.280s). Workflow static contracts 3 pass. No local GPG, Docker, Keychain, native tests or workload executed; no CI retry/dispatch. Full native recovery remains unverified until a new exact-source run.

Only two test files changed; production signer, validator and workflow bytes remain unchanged. Frozen source facts are in repair-summary.json and complete patch in repair.patch.
