# Rejected Issue #977 performance candidate

This worktree/branch is an experimental repair probe, not a merge-ready product change.
The measured source is `38d53d3dc555361e1b72bde6ea49a6dd34586f17`, based on `c03fa6cc1712e886d9359822e23be716fdb4534e`.
Its single variable is fastest gzip compression of complete production metrics.

All 24 placement/control cases (48 individual SEND windows) and all 12 burst comparisons passed. Nine of twelve sequential p99/whole-node CPU comparisons failed the unchanged +5% limits. The prior six original failed windows remain preserved. Do not treat the faster HTTP scrape benchmark as a verified repair, or weaken/subtract the original acceptance scope.

The complete selected report and replayable failed experiment are delivered in Draft PR #995. The native measured binary and all evidence are retained at `/Users/tt/.codex/artifacts/issue-977-metrics-gzip-20261001`.
