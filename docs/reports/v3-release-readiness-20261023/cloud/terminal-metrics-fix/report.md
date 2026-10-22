# Terminal metrics selector correction, frozen for independent review

Candidate worktree: `/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-rc-terminal-metrics`
Branch: `codex/v3-rc-terminal-metrics`
Base: `79a7177032a6ae1ec7015cefe0e6f94eb0f19f06`, tree `a13367b2dc4658e6c27a1992f240544636e5178d`.
No commit, push or PR was performed.

The terminal collector used invented root-level metric names for Go and Channel counters, producing empty families even when registered exporter names were available. The three corrected patterns collect standard `go_*`, standard `process_*` together with the existing independent `wukongim_process_*` family, and exact `wukongim_channel(v2)?_(meta_created|activation_rejected)_total` counters. The other five partition patterns are byte-identical to the base. Capture/validation/helper function ASTs are unchanged.

Four changed files are frozen in `final-source-binding.json` and `final-diff.patch`:

- `scripts/chat-lifecycle/terminal-evidence.py`: three metric patterns and one stale explanatory comment.
- `scripts/testdata/chat_lifecycle_terminal_evidence_test.py`: actual exporter fixture names; transformation, registered alias and real collector regressions; original negative cases retained.
- `CHANGELOG.md`: concise operator-facing Unreleased fix.
- `docs/development/PROJECT_KNOWLEDGE.md`: exact exporter families, no alias double-counting, and empty coverage as unknown values.

The full failure matrix and approved seams were written before tests or production changes. `source-freeze-before-tests.json` records root AGENTS/CONTEXT and source hashes. No applicable FLOW exists in these four paths; package-scoped `scripts/cloud-deployment/FLOW.md` does not apply. No workflow, policy, skill or protected control path changed.

## Tests first and observed results

1. Static regression was written before changing the helper. `static-first-red.log`: 6 methods, 20 failure subcases, including all real Go/Channel/standard-process names being omitted. The original helper SHA was independently checked against the source freeze.
2. Fixture regression and actual exporter names were also written before the helper change. `tests-first.patch` and `tests-first-freeze.json` bind those bytes. The final test file hash still equals that pre-implementation freeze.
3. With an explicit quiet window after the local reproduction's owned PID/port cleanup, `process-first-red.log` runs the actual collector CLI using fixture SSH only: 1 method failed, approximately 0.990 seconds, collector exit 1. The full emitted manifest/call/file hashes remain in `process-first-red-artifact.json`.
4. Minimal selector repair: `static-first-green.log` records 6/6 methods passing; `process-first-green.log` records the same CLI case passing in 0.615 seconds. Its exact raw retained matrices and actual query parameters are summarized in `registered-resource-green-summary.json`.
5. Existing full fixture suite, first run after repair: 23 methods and 80 process cases, zero failures/errors/skips, unittest time 50.736 seconds (wrapper elapsed 50.812 seconds). Full log and original artifact are retained in `integration-first-green.*`. This is the Python fixture executed by the unchanged integration-tagged `TestChatLifecycleTerminalEvidenceRetention` Go entry; no Go compilation was started in this subtask.
6. `git diff --check` passes. `verify-source-and-results.py` independently verifies exactly the four intended paths, unchanged function ASTs, unchanged first five patterns, and unchanged 8 partitions / 60 seconds / 24 MiB shared budget / 4 MiB individual cap. Existing cases retain 15-second steps, millisecond bounds and raw nanosecond anchors, explicit empty coverage, invalid-family rejection, sibling retention and original failure verdict semantics.

The static transformation and process fixture tests inspect independent registered-name inputs and the actual emitted query. They do not start a product, a real Prometheus exporter or a cloud service. They therefore establish collector compatibility and preserved budgets, not a live server's availability or a repair for the original message loss. Standard resource series may consume additional bytes inside the existing caps; exhaustion remains explicit and does not relax the budgets. All process invocations completed and fixture-owned temporary directories were removed by the existing harness. No claim of an operating-system-wide process inventory is made.

The previous RC evidence and failure classification remain unchanged. Cloud resources had already been released by the parent task before implementation; this subtask made no cloud, SSH, MCP, IAM, credential or paid operation.
