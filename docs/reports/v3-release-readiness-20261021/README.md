# V3 readiness evidence — 2026-10-21

Read [the current assessment](../2026-10-21-v3-release-readiness.md) for the
release decision. This directory preserves bounded audit results and local
regression attempts; it is not a published release or a long-run qualification.

- `context.json`, `recovery-rules.json`: exact source identities and frozen
  AGENTS/FLOW digests for the evidence and recovery worktrees.
- `ci/summary.json`, `ci/readiness-matrix.md`: initial read-only GitHub audit.
  The empty PR list is the state at that audit time, before delivery PR creation.
- `packages/summary.json`, `packages/readiness.md`: live public status, immutable
  audit identity and protected-state observations; not a fresh public-client run.
- `packages/implementation-validation.json`: PR #67 source-classification scope,
  test-first evidence, 38 focused tests and static contract results. Production
  stable remains disabled. Initial CI states are timestamped observations.
- `local/coverage-summary.json`: package-by-package default-test coverage across
  infrastructure recovery attempts. 219 pass, 15 no-test packages; no product
  assertion failure. Both disk-exhaustion and missing-cache runs remain retained.
- `spec/self-chat/`: failed fixture attempts, corrections, final source and JSON.
  Final result covers 12 ACKs, 12 complete-history observations and two restarts.
  Repeated real-time frames are distinct from duplicate durable messages.
- `recovery-checks/`: named local checks, exact commands, source/diff identity and
  logs. CI on the final PR source is recorded independently.

Logs and JSONL may be gzip-compressed to retain exact bytes without bloating the
working copy. Decompress with `gzip -dc FILE.gz`; no rejected attempt is removed.
`SHA256SUMS` covers every archived file except itself. Paths and times inside the
receipts describe the original execution environment; replay commands need fresh
output directories and the recorded product revision/binary identity.

The source evidence branch remains separate from the small product repair PR to
respect the repository's review-size limit. This does not change or hide the
candidate's original failures. A final tag still requires its own release gates,
immutable publication and signed native-package public-client acceptance.
