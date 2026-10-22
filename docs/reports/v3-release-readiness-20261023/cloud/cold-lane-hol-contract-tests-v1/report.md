# Cold-lane HOL: tests-only diagnostic result

Fixed source: `2ab3e1a8bd95670603bc38314036c797a5124682`, branch `codex/v3-cold-lane-hol-contract`. The pre-test failure matrix and source/rule digests were recorded before writing the new test. Only `internal/usecase/presence/cold_uid_isolation_integration_test.go` is added; production, rules and FLOW files remain byte-identical. No commit, push, PR, main-service build, cloud operation or load experiment was performed.

One integration command completed in **4.597447916 s** including compilation (test package 0.826 s): **one intentional RED, eight PASS**. The exact executable, argv, environment overrides and 120-second outer / 90-second Go bounds are in `test-invocation.json`. The test process exited 1, did not hit either timeout, was joined, and its process group had zero remaining processes at cleanup.

| Case | Observed |
| --- | --- |
| New proposed independent cold-UID isolation | RED: A requested one cold UID at each of 32 distinct hash-slot targets, then paused inside the controllable owner port. B used a different UID under A's first exact target. B returned `context deadline exceeded` at 250 ms before A release; owner calls and peak concurrent owner calls both remained 1. |
| New non-conflicting-lane control | PASS: with A covering only hash slot 0, independently cold B at hash slot 1 completed while A remained paused; A completed after release. |
| New same-UID proof sharing | PASS: no route was returned before the owner proof existed; after A release both calls returned identical valid routes with one owner read. This supports coalescing or hot-proof reuse, but the bounded scheduling guard cannot prove the precise instant B entered the private lane wait. |
| New canceled waiter isolation | PASS: canceling B returned `context.Canceled`, did not cancel A's held owner context, and A completed after release. |
| Five existing AuthorityRecovery contracts | PASS: proven-route bypass, complete/negative proofs and old completion rejection, cancellation, batch sibling alignment, and large-page result preservation. |

All eight new public lookup calls logged `cleanup joined=true`, including the intentional failure. The owner fake holds only the selected external read, returns complete valid positive owner routes, and can immediately serve B; the real Directory performs readiness and completion validation. Tests access no private lane state and use channel gates and bounded watchdog timers without `time.Sleep`.

The source explains this mechanism directly: `recovery.go:115–143` derives admission lanes from cold hash slots and acquires every required lane, while the deferred release at `:122–127` covers the owner call at `:165–174` and proof installation at `:175–188`. A can therefore hold all 32 lanes during owner I/O and prevent an unrelated cold UID from reaching that otherwise available owner port. Proven lookups still bypass this path.

**Boundary:** current source comments (`recovery.go:23–25,53–55,99–100`) explicitly describe fixed admission lanes, coalescing and a page-shared unavailable-owner timeout. The public interface does not currently promise independent cold-UID latency. This RED establishes a reproducible coarse-admission head-of-line mechanism and a proposed isolation contract; it does not alone establish a correctness defect, the duration/frequency in production, or the cloud message-loss root cause. No gate threshold, timeout, retry or capacity changed. This deliberately RED test branch is retained for design review and is not merge-ready.

Files in this finite evidence scope contain synthetic fixture identifiers only. `PUBLIC_ALLOWLIST` and `PUBLIC_SHA256SUMS` bind the exact inputs, test source, patch, complete output and cleanup receipts; no raw production identifiers, profiles, config or credentials are included.
