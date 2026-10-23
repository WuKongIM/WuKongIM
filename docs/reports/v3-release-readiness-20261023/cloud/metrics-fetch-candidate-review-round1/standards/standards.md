# Standards review

Fixed scope: `27fa6d63cf3b2b6c3a9a28575f917d60cc054ded...56c287493a5e9fc3df65ea0631b99612672cd678`; head tree `582e5bc024147b32aa68074d04c041391af89b96`. Worktree clean before and after review.

**Findings: 0 documented-standard violations; 0 Fowler-smell findings.**

The diagnostic stays within the public-target benchmark boundary required by `internal/bench/FLOW.md` and introduces no server dependency. Closed target error classes use fixed enums/bitsets; the existing HTTP acquisition loop preserves fallback, causal-context checks, response limits and body cleanup (`target/client.go:306-319,745-838`; `metrics_failure.go:8-130`). This fits `AGENTS.md`'s bounded-observability and resource requirements.

The three-source aggregate retains exactly three private references and joins the existing three calls before deciding. Its public chain exposes only the prior harness-invalid sentinel, keeping child context/product errors from changing classification (`production_meta.go:277-319`; `metrics_fetch_failure.go:31-43`). Parent-context precedence remains outside that aggregate. The optional projection validates mask/slot consistency, constructs a fresh closed copy and survives only the exact metrics-fetch leaf (`metrics_fetch_failure.go:14-28`; `production_controller.go:69-73`), consistent with both benchmark FLOW privacy invariants.

All changed production code and 920 added test lines were inspected. The synthetic transport/context/channel tests introduce no listener, sleep or elapsed-time simulation. Supplied tests-first v2 blobs match the candidate; the initial fixture error remains explicitly recorded. Changelog, knowledge and affected FLOW/index changes meet `AGENTS.md`'s operator-note and navigation-update rules.

All twelve Fowler heuristics were considered as judgement calls, with repository rules taking precedence. No speculative framework or material smell was found. Supplied execution receipts and finite manifests were inspected/hash-verified; formatting, tests, race and named gates were not rerun. No source, GitHub or cloud mutation occurred. This observability change does not establish the remaining message-loss root cause, repair, complete workload report or Linux acceptance; NO-GO remains separate.
