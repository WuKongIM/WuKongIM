# Specification review

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
