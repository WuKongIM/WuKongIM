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
- `cloud/`: bounded read-only preparation audit, exact public configuration,
  frozen control sources, historical cleanup payloads and their limitations.
  This does not establish today's provider inventory or authorize procurement.
  `local-shakeout-preconditions.json` records the initial host below the existing
  5-percent disk reserve; no local 30-minute run had then been attempted.
  `disk-followup/` measures the same reserve after merge and inventories only
  previously recorded task output paths. Even all those outputs are too small
  to recover the missing capacity; no files were deleted or workload attempted
  during that audit. Later free-space recovery and the first workload are
  separately recorded below.
- `ci/session-cleanup-probes/`, `ci/candidate-workflow-validation.json`,
  `review.md`: original cross-PGID cleanup failure, real-process macOS probes,
  static validation and independent review. Linux results are separate CI evidence.
- `audit-archive-manifest.json`: original and archived byte hashes for the cloud
  and cleanup audit; `.log` files are retained as deterministic gzip streams.
- `mqtt-diagnosis/`, `replication-diagnosis/`, `diagnosis-archive-manifest.json`:
  first-candidate CI failures, deterministic red/green investigations and focused
  regression receipts. MQTT changes product cancellation; replication changes
  only test fault injection, retaining the original 66-item obligation.
- `candidate-v2-rules.json`, `candidate-v2-dispatch.json`, `followup-checks/`:
  updated candidate identity, exact rule digests, first qualification dispatches
  and the named FLOW check (including the initial stale-index failure).
- `ci/final/first-generation-summary.md`: all six first-generation CI outcomes,
  actual executed/skipped steps, source-tree identity and workload numbers.
  Its allowlist retains bounded reports and logs; archive-level digest claims
  distinguish full downloads from CRC-checked selected ZIP members.

- `ci/final-v2/second-generation-summary.md`: seven updated-candidate CI runs, all
  first-attempt successes, exact source/binary identity, full matrix and native
  lifecycle results; original first-generation failures remain preserved.
- `packages/preflight-followup/`, `packages/preflight-ci/`: test-first source
  preflight implementation, independent review, and both new CI results including
  seven actual native metadata cases. No production deployment occurred.
- `packages/public-signing-observation.json`, `local-backup-classification.json`
  (under `packages/`): public fingerprint comparisons only. The latest user
  clarification confirms independent stable keys are not yet created.
- `packages/storage-design/`: source-frozen OSS/CDN design and official sources;
  not an implemented transport or cloud authorization.
- `review-merge/`: product PR #1016 merge evidence. The disabled Review Agent
  observation remains preserved; the resolution records the user-authorized
  normal administrator merge, matching source trees and verified cleanup.
- `packages/merge/`: PR #67 rebase merge, unchanged branch protection, matching
  source trees, and successful main validation with Pages deployment skipped.
  The original clean checkout is retained because its pre-rebase tip is not an
  ancestor of main; no unmerged local branch was force-deleted.
- `packages/stable-signing-user-confirmation.json`: evolving user statements.
  NAS availability supplies a potential encrypted-backup location, not an
  established offline/hardware signing environment or generated stable keys.
- `packages/object-storage-plan/`, `packages/transport-plan-review/`:
  failure-first implementation, independent resource-limit finding and fix,
  then ordinary-name RPM metadata support with complete closure validation.
  The two generations and their rejection evidence remain distinct.
- `packages/transport-plan-real/`: complete public beta.24 archive digest and
  historical identity anchors; first unsupported-metadata rejection and the
  subsequent successful 44-file plan. The large tar is excluded. This is byte
  and structural consistency evidence, not cryptographic or client acceptance.
- `packages/transport-plan-delivery.json`: historical draft PR #68 identity;
  `packages/transport-plan-archive.json` records the bounded allowlist copies.
- `packages/transport-plan-ci/`: both first-attempt #68 PR checks, 373 Python
  tests and the TEST ONLY native drill; actual merge/head trees match.
- `packages/transport-plan-merge/`: separately user-approved #68 normal rebase
  merge to `45619df14`, identical approved tree, unchanged protection, and
  373 passing postmerge tests. All Pages publication steps were skipped.
- `cloud/bundle-ed705/`: first credential-free offline bundle build and full
  artifact verification against exact ed705 source/control; no cloud resources
  or deployment. The archive excludes the large raw artifact.
- `cloud/local-shakeout-ed705/`: frozen first 30-minute precheck attempt after
  disk recovery. It failed early with grant `offered_load_under_delivery` and
  produced no final report. Running cuts do not establish full correctness;
  all 12 owned processes and 64 reserved ports were independently cleaned.
- `postmerge-preparation-archive.json`,
  `planner-merge-and-shakeout-archive.json`: verified allowlist copies and
  original-file hashes for these new bounded receipts.

Logs and JSONL may be gzip-compressed to retain exact bytes without bloating the
working copy. Decompress with `gzip -dc FILE.gz`; no rejected attempt is removed.
`SHA256SUMS` covers every archived file except itself. Paths and times inside the
receipts describe the original execution environment; replay commands need fresh
output directories and the recorded product revision/binary identity.

The source evidence branch remains separate from the small product repair PR to
respect the repository's review-size limit. This does not change or hide the
candidate's original failures. A final tag still requires its own release gates,
immutable publication and signed native-package public-client acceptance.
