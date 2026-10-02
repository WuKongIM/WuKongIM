# Will proposal/commit/apply review

Fixed base: `dcaaaa8b315b1805ffef42d8bfbfcb1719edc4ca`; current approved slice only, commits `[]`.
[Final evidence scope](review-final-scope.json); earlier scope files retain their historical snapshots.

## Standards

Reviewed base/HEAD: `dcaaaa8b315b1805ffef42d8bfbfcb1719edc4ca`; `commits=[]`.
Scope: `review-source-scope.json` plus `review-final-scope.json`, current working-copy
hunks and frozen untracked scenario/spec/report files. Prior branch commits are
excluded. All 50 final-scope hashes match; product/test/spec hashes are unchanged.
The zero-context patch-format follow-up preserves both verified single-line
negative mutations and generated-source hashes.

Sources: root `AGENTS.md`; `test/e2e/AGENTS.md`; MQTT domain/scenario `AGENTS.md`;
`CONTEXT.md`; subtree-scoped `pkg/slot/FLOW.md` as advisory navigation. No separate
CODING_STANDARDS or CONTRIBUTING source was found. Fowler smells are heuristics,
subordinate to repository rules; tooling-enforced matters are excluded.

## Documented standards

No actionable finding or hard standards breach remains. The Multi-Raft
selectors remain opaque and empty in ordinary builds; FSM observations preserve
commit/apply ordering; proxy instrumentation captures the originating executor.
The scenario retains real protocols, 256 hash Slots, healthy DeadlineExceeded
quiet, persistent subscription replay, and cleanup-before-artifact ordering.
New controls have English responsibility/constraint comments. FLOW and the E2E
catalog describe the added cuts. The report explains the absence of user-visible
behavior changes; no Changelog entry is required for this slice.

The earlier delivery follow-up is resolved: `PROJECT_KNOWLEDGE.md:780` now records
the qualification, definite queued-successor authority and remaining limits,
satisfying root `AGENTS.md`, Documentation and Knowledge. The implementation
progress report and final evidence preserve those boundaries.

## Fowler baseline

Judgment only: **Duplicated Code**. The new predicate
`Stage == MQTTWillExecuting && DispatchStage == MQTTWillDispatchStarted &&
ExecutionGeneration == 1` repeats in `fsm/mqtt_will_cmds.go:34`,
`fsm/statemachine.go:317`, and `proxy/mqtt_write.go:250`. Independent tracing cuts
at submission, pre-mutation and durable resolution justify the small duplication;
a shared cross-package API solely for temporary instrumentation would add
coupling. No actionable smell in the remaining reviewed hunks.

## Validation boundary

This reviewer ran no tests, network or external actions. Audited receipts and
matching saved logs show final matrix **9/10**, package **758.215s**; one startup
readiness failure occurs before fault setup with zero control counts. Its unchanged
repeat passes: **85.454s** package, **84.67s** case. Combined coverage reaches all
ten cases; the retained failure prevents a clean whole-matrix claim.

The corrected private negative fails the intended original-receipt deadline after
actual capacity refusal (**153.682s** package). Ordinary Will regression passes
both topologies (**50.474s**); all four focused race packages pass. The named
`flow-doc-contracts` check records **88 compliant, 0 invalid, 11 existing warnings**.
No latency, general-partition or complete-MQTT qualification follows from this
slice.

## Spec

Reviewed base/HEAD: `dcaaaa8b315b1805ffef42d8bfbfcb1719edc4ca`; commits: `[]`.
Scope: approved working-copy source slice frozen by `review-source-scope.json`
and validation/documentation follow-up frozen by `review-final-scope.json`, using
their specified working-copy diffs and untracked files. Earlier branch commits
are excluded. Applicable root/E2E/MQTT/scenario instructions and Slot, suite,
cluster, MQTT session and Will-journal FLOW files were read. This reviewer
performed static review only; no tests, network calls or external actions.

## Findings

- Missing or partial requirements: no actionable finding within the approved
  bounded specification.
- Behavior outside the approved request: no actionable finding. The new seams
  are temporary-copy fault controls and empty ordinary selectors.
- Requirements apparently implemented incorrectly: no actionable finding.
  The persisted marker follows successful persistence and requires index above
  commit; replication loss selects actual MsgApp command batches. Pre-FSM and
  durable-resolution markers surround actual mutation/commit. The scenario
  requires captured-executor evidence, healthy expired-grant quiet, real journal
  refusal where promised, exact original identity, persistent reconnect,
  PacketID/DUP replay when unfinished, and publication counts across killed
  process epochs. Cleanup precedes body-free JSON emission.

## Evidence boundary

All 50 final-scope file hashes match, and product/test/spec hashes remain
unchanged. Reviewed receipts, source-context, validation-results, README,
implementation-progress and PROJECT_KNOWLEDGE agree with the saved evidence;
the six final validation-log hashes also match.
Reformatted zero-context patches retain the same one-line private mutations;
candidate/negative source hashes match and historical patch hashes remain saved.

The final matrix passes 9/10 in 758.215s. The retained failure is startup-only
readyz connection refusal at the unchanged 30-second bound, with every control
counter zero. Its identical source/binary/budget repeat passes in 85.454s
(84.67s case), giving combined ten-case coverage, not one clean matrix run.
The delivered private negative fails the original-receipt assertion in
153.682s after real capacity refusal and persistent reconnect; diagnostics
still report ready. Ordinary Will passes both topologies in 50.474s; four
focused race packages and FLOW validation pass (88 compliant, zero invalid,
eleven existing warnings). Failed fixtures and the ineffective initial
negative remain explicitly disclosed rather than substituted for acceptance.

Spec lines 83–90 defer concurrent independent pressure, successor-CAS cuts,
failed-read fairness, issued-effect terminal recovery, shared-storage admission,
latency and general partitions. These are not credited as implemented.

Standards: 0 unresolved documented violations, 0 actionable smells (1 justified heuristic); Spec: 0 actionable findings. Worst actionable issue: none within either axis.
