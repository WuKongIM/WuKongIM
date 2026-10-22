# Standards review

Scope: `d349a7bdd2ca8f6bd4683fbccbf83010d3f9cd3e...3e668ac4733ae403e57242ddec559e6d937b0d3c`; head tree `f70fc869cf65bb03ac42fd43d19e85c32a0fe114`. The worktree was clean before and after this static review.

**Findings: 0 documented-standard violations; 0 Fowler-smell findings.**

The UID-flight change keeps orchestration in the use case and retains Directory as proof/fence authority, consistent with `AGENTS.md` dependency directions and `internal/runtime/presence/FLOW.md`. Its admission lock does not span owner I/O or waits; four caller-executed cohorts, 2,048 keys and explicit UID-copy budgets bound retained admission state (`recovery_flights.go:11-19,124-194`). It adds no per-UID goroutine. Owned subsets complete before joining earlier flights; followers reread current Directory proof and keep cancellation independent (`recovery_flights.go:58-87,198-271`). Existing 512-UID/256-target packing remains intact (`recovery.go:59-102`). These address the performance/resource requirements in `AGENTS.md` without introducing a general-purpose framework.

The metadata change reuses the existing closed diagnostic envelope, preserves private error chains, and leaves product-deficit classification and checkpoint advancement ordering intact (`production_meta.go:108-136`; `production_controller.go:47-52`). This follows the bounded, identity-free reporting requirements in both benchmark FLOW files. Changelog, knowledge and the affected presence FLOW describe the new admission limit and proof semantics.

All twelve Fowler heuristics were considered as judgement calls, with repository rules taking precedence. Formatting, generated-index validity and test execution were not independently rerun; they are tooling checks outside this static review. No source, workflow, cloud resource or GitHub state was changed.

This review does not establish the original cloud message-loss root cause, Linux performance acceptance or process-level candidate E2E success; those remain separate validation work.
