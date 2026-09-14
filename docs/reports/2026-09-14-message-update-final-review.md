# Message update final review and validation

Date: 2026-09-14. Reviewed the complete working-copy feature against
`0160c1f7d068b555c872df79ab6ab57f2cd4bc76`, including untracked source, tests,
specification and reports. Review sources were repository AGENTS/FLOW rules,
Issue #957, the accepted ordinary-message-only scope, and the API contract.

The final feature was rebased onto remote main
`0fc7da51e52d6bdbebe6db81dce1851569285d89`. The unrelated readiness probe
optimization at `0160c1f7d` is excluded. Its original local branch/workspace
was preserved. Final Go code candidate: `01189eba9bb8f7c3813f60be1cf6379397e91579`.
Public contract follow-ups were also reviewed and verified through
`0d43eb56dd259d4fbe8a4c2925eb1b77fce22839`; their Go source is identical.
Earlier performance and recovery reports describe their recorded binaries;
they are not measurements of this final candidate.

## Standards

- **P2, resolved:** raw identities could produce an edit cursor larger than its
  own decoder limit. Format 2 hashes the canonical UID/channel/type JSON tuple;
  long and escaped identities, actual pagination, context isolation and format-1
  reset migration now have regression coverage.
- **P2, resolved:** overlay read authority changes used the generic database
  conflict alias, losing retryability. A dedicated stale-read-route cause now
  survives local and RPC paths; database/CAS conflicts remain non-retryable.
- **Possible Middle Man, nonblocking heuristic:** the narrow cluster update
  adapter forwards three store methods. Retained as the existing explicit
  infrastructure-to-usecase port seam; it contains no business policy and
  preserves composition-root wiring. This is not a documented hard violation.

No additional architecture, cluster-bypass or test-tier violation was reported.
A follow-up review found no actionable defect in the response fence: maintenance
publishes its transition stamp before restore replacement is admitted and keeps
it active through runtime resume. Synchronous JSON handlers assemble detached
results before committing the response. Embeddings supporting restore must
supply the transition-fence port; the epoch-only fallback cannot detect a
completed failed restore cycle.

- **P2, resolved in documentation follow-up:** batch history also passes through
  the restore middleware, but its contract omitted the content epoch header and
  possible 503 envelope. Both locales now publish them, retaining per-item
  business error semantics.

Standards: 3 actionable findings (all P2, resolved), 1 retained nonblocking
heuristic; worst original actionable severity P2.

## Spec

- **P1, resolved:** a request body delayed across restore could return new content
  under an old `X-WK-Content-Epoch`. A node-local transition fence checks before
  epoch lookup and before successful response commitment, rejecting overlap as
  503 with no content/epoch header. It covers successful and failed restore
  cycles without a second production Controller read, RPC or response copy.
- **P2, resolved:** stale overlay routes did not consistently return retryable
  errors. Local/RPC authority-change tests and all four public read error maps
  now distinguish temporary routes from database conflicts.
- **P2, resolved:** long supported identities yielded unusable cursors. Format-2
  fixed-size bindings solve this; matching valid format-1 cursors reset to a new
  baseline. Oversized old cursors require empty-cursor initialization.

- **P2, resolved in documentation follow-up:** a reset field description told
  clients to save the baseline before loading history. It now requires holding
  the baseline and committing it together with successfully reloaded history.

Spec: 4 findings (all resolved); worst original severity P1.

## Validation

- Focused full-package unit tests passed for API, app, message usecase, Slot
  proxy and read-availability classification.
- Focused integration tests passed for single-node cluster HTTP behavior,
  three-node quorum/leader transfer, and quorum ReadIndex without log writes.
- Targeted race tests passed for API/app epoch fences, message edits/cursors,
  Slot proxy and Multi-Raft read barriers. The two runtime packages selected by
  that name filter had no matching tests; their normal unit suites passed.
- Named `go-vet`, `go-format`, `go-mod-tidy`, and `flow-doc-contracts` passed.
- Full named `go-unit`: 210 packages passed. Its only failure was
  `TestDashboardAssetsCoverAllExportedMetrics`; an independent clean worktree
  at exact remote-main base reproduced the same six missing conversation
  persisted-read metrics. The temporary baseline worktree was removed after
  validation. This gate is **not green**; no unrelated dashboard change was made.
- Named `docs-integration` passed at `0d43eb56dd25`: 208 document tests,
  bilingual static export/output checks, and the real Chromium JavaScript
  quickstart E2E (15.977 seconds). Verified receipt runtime was Node 22.12.0 /
  Chromium 151.0.7922.34. This proves the existing basic integration still works,
  not that an official SDK implements edit synchronization.
- Initial documentation checks identified stale HTTP/RPC inventories; they now
  cover 44 Product HTTP operations and 60 shared transport IDs. The public
  contract includes both edit routes, latest content/version fields, epoch
  headers and restore/temporary-error envelopes. Intermediate failures are not
  counted as passing checks. An intermediate receipt attempt also correctly
  rejected an untracked report; the successful attempt used a clean checkout.

The strict three-node recovery test passed once on the final Go candidate in
257.22 seconds, with 256 Hash Slots, 12 physical Slots and three replicas.
Three writers each completed 1,178 edits (3,534 total); six read/idempotency
streams recorded 7,057 successes. There were 349 temporary attempts during
fault injection and none in the healthy phase. Observed Channel and Slot
leaders both moved 3→1 after abrupt termination; every worker progressed before
node 3 restarted. All three ingress nodes converged across all four read APIs,
twelve message versions ended at 296–297, and final incremental sync was empty
without a restore reset. The test container exited and was removed.

Machine-readable evidence: [final validation](2026-09-14-message-update-final-review.json).
The server/harness hashes identify the code measured; subsequent documentation
commits do not claim a new performance measurement. Raw local logs remain in
ignored `tmp/message-final-review/`.

Missing baseline dashboard series:

- `wukongim_conversation_persisted_admission_total`
- `wukongim_conversation_persisted_batch_items`
- `wukongim_conversation_persisted_hold_seconds`
- `wukongim_conversation_persisted_inflight`
- `wukongim_conversation_persisted_limit`
- `wukongim_conversation_persisted_occupancy`

## Delivery boundaries

This is a server feature and API contract. Official SDK integration, real
100,000-member online delivery, production capacity limits and rollout remain
separate work. All nodes require compatible binaries; CMD and stream messages
remain non-editable. No release, deployment, auto-merge or cloud procurement is
part of this delivery.
