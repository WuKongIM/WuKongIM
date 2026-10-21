# Replication saturation CI failure diagnosis

Source: `5f3ecdf93e762a1b1551b44977a44f2e63c97802` in the `v3-release-recovery` worktree. Scope: `pkg/channel/replication` only. No commit, push, dispatch or product-process workload was performed by this task.

## Finding

**Confirmed test-fixture defect, not demonstrated repair-ledger loss.** The test queued one rejection per intended node-2 replication, but `runtimeTestRouter.exchange` rejected only batches whose length was exactly one. Legal coalescing therefore allowed two or more requests to reach the real follower store without consuming their rejection tokens. Those successfully durable requests correctly created no follower-repair obligation, invalidating the test's assumption that all 66 channels required repairs.

The original CI symptom was `runtime_test.go:415: retained repairs = 64, want 66` after 2.08 s. Its raw log is retained under `ci/final/artifact-12322262068/stdout-stderr.log`; it does not contain per-request batch observations. Local reproduction establishes the concrete fixture mechanism responsible for the same symptom, without claiming an additional unobserved product defect.

## Hypotheses and evidence

The pre-edit failure inventory and three falsifiable hypotheses are in `failure-inventory.md`; applicable source/rule digests are in `rules.json`.

- H1, faulty batch-sensitive injection: confirmed. Unmodified test initially passed 10/10, demonstrating why rerunning to obtain green is insufficient. A failure-only diagnostic patch plus `-count=50 -cpu=1,2,4` reproduced 6 failures in 150 runs: five 64/66 and one 62/66. All 14 missing repair keys already had follower LEO=1 with the original command manifest; each failing run had exactly as many unconsumed rejection tokens as missing repairs. See `instrumentation.patch`, `instrumented-count50-cpu124.log`, and `reproduction-summary.json`.
- H2, delayed completion beyond the two-second deadline: does not explain the reproduced observations. The missing channels were already durable, so merely waiting longer cannot make their successful requests consume skipped injection tokens or create the expected gap evidence.
- H3, loss in the runtime repair ledger: not supported. The missing keys never had the intended failure injected. No product change is justified by this evidence.

A deterministic minimal regression queues two real deferred replica requests through `peerBatcher`, uses the manual executor to force one coalesced exchange, and reaches the existing router and in-memory follower store. Before the fix both callbacks reported success, both follower records became durable, and repairs were **0/2**. A mixed-batch control independently showed that the requested rejection was absent while the probe and other replication completed. Both behavioral failures are preserved in `coalesced-contract-red02.log`; the earlier `coalesced-contract-red.log` is a test-authoring compile error and is not counted as behavioral red evidence.

## Minimal repair

Only test files change:

- `runtime_test.go`: consume each node-specific injected fault per replication item, regardless of batch partitioning; forward non-rejected items with the original version/priority and restore result alignment. The original count 66, two-second observation deadline, blocked repair worker and eventual follower durability assertions remain. Add an exact channel/follower-key assertion so unrelated ledger entries cannot satisfy the intended coverage.
- `runtime_router_test.go`: deterministic coalescing regression, one-shot fault consumption/retry durability, and mixed probe/replication result alignment plus persisted-state controls. These tests use no sleeps or real external processes.

The temporary failure-only diagnostic patch has been removed; it remains archived separately. `fix.patch` and a copy of the new test file preserve the delivered change.

## Validation and remaining scope

Exact commands, dedicated GOCACHE and exit codes are retained in `commands.json`; every test command uses `GOWORK=off`, `-p=2`, and a bounded timeout.

- Deterministic coalescing and mixed-item regressions: red before fixture change, green after.
- Original saturation test, unchanged count/deadline, `-count=50 -cpu=1,2,4`: **150/150 pass** after repair, 7.349 s package runtime.
- Entire replication default package: **pass**, 1.130 s.
- Focused race verification (`-count=5`, coalescing/mixed-item fixture tests, saturation, gap repair and authority-owner tests): **pass**, 1.808 s; no race report. The retained log includes a macOS linker LC_DYSYMTAB warning; the command exited 0.
- `git diff --check`: pass. No `[DEBUG-repair-saturation]` instrumentation remains in the package.

Initial disk availability was approximately 9.4 GiB; after normal and race-cache compilation it remained approximately 7.9 GiB. Only a task-specific cache was created; no shared cache or unrelated artifacts were deleted.

This is local macOS evidence. It does not turn the original Linux CI attempt into success or replace CI on the final combined source. The root task retains the failed attempt and handles subsequent candidate verification.

`pkg/channel/FLOW.md` remains accurate because runtime ownership, queue bounds, protocol behavior and product code are unchanged. No new user/operator-visible behavior requires an additional Unreleased entry for this fixture-only repair. The root task owns any combined PR changelog decision; no changelog or FLOW file was modified here.
