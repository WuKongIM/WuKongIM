# Recovery barrier and fixture final review

## Standards

# Standards review — recovery barrier core v1

No manually identified documented-standard violations or actionable smell findings in the frozen four-file patch. One tool-enforced required gate failed and remains recorded below.

Reviewed base `9a4900dc67ac674791edba11ea9a719cd91ec570`, patch SHA-256 `0be01dacb235a457cdfb54ff9bec46a2c272337008afb8c09c56b00fa4d49ed7`. All four file hashes and the complete working-copy patch reconstruction matched the freeze. Applicable root AGENTS and the baseline/current `pkg/channel/FLOW.md` were read; no subordinate AGENTS applies.

- `pkg/channel/replication/recovery_barrier.go:56–67` documents and implements one bounded error classification. It consumes existing distinct-voter round evidence, requires local durability and complete vote/repair accounting, checks each NeedFrom against the barrier, and retains the original error through `errors.Join`. It adds no worker, queue, I/O or business-write bypass. This fits root AGENTS cluster semantics, dependency and code-quality requirements. The twelve smell heuristics yielded no actionable judgment-call finding.
- All 19 cases in `recovery_barrier_pending_test.go` existed before the production edit; the preserved red test bytes match the final test exactly. The failure inventory and red evidence show two intended pending cases failing and 17 controls passing. Green evidence shows all 19 passing; the subsequent first replication-package run passed. This supports the mandatory test-first/direct-test rules; the recorded finite fault seam explains the isolation choice.
- The Unreleased changelog and applicable FLOW explain the changed operator-visible classification and explicitly avoid claiming additional repair scheduling.

Required validation gap: `flow-doc-contracts/result.json` is failed because `pkg/channel/FLOW.md` has 152 lines, exceeding its 150-line limit. Root AGENTS requires this named check to pass for FLOW changes. This is a tool-reported gate failure, not a duplicated manual style finding; frozen v1 is not fully validated.

No tests were rerun or sources changed by this reviewer. The original Mac failure cause remains unconfirmed; these controlled tests do not establish that causal claim.

Standards: 0 manual findings; required FLOW gate: failed.

# Standards delta review — combined repair v2

No new standards violations or actionable smell findings in the final limited delta.

Reviewed base `9a4900dc67ac674791edba11ea9a719cd91ec570`, seven-file patch SHA-256 `38519d247817780b633401280ffebba3433bf0f8b0bf5ee1e6bbc059a38c03b0`. All seven frozen file hashes and the full working-copy patch reconstruction match. Removing the generated-index hunk reproduces the previously reviewed combined-v1 patch exactly: all six prior files, including core implementation/tests and migration fixture files, are unchanged.

The sole new change is `docs/development/FLOW_INDEX.md:81`, updating the generated `pkg/channel/FLOW.md` line count from 148 to 150. It matches the actual file and retains its existing warning classification. No content, behavior, abstraction or test was added by this delta; the twelve smell heuristics identify no actionable new concern.

Actual named-check receipts now report `flow-doc-contracts` passed with 88 compliant FLOW files, zero invalid files and 20 warnings; `go-format` also passed. This resolves the previously recorded required FLOW gate gap under root AGENTS. Both earlier failed source generations and their review reports remain preserved, rather than being relabeled as passing.

This review does not claim the in-progress full five-topology E2E result or establish the original Mac failure's cause. No check was rerun and no source was modified.

Standards delta: 0 findings; reviewed FLOW and formatting gates: passed.

## Spec

# Spec review — recovery-barrier NeedFrom-only patch

**No actionable Spec findings or scope expansion found in the frozen four-file patch.** Base: `9a4900dc67ac674791edba11ea9a719cd91ec570`; patch SHA-256: `0be01dacb235a457cdfb54ff9bec46a2c272337008afb8c09c56b00fa4d49ed7`. All four frozen file hashes were verified by reconstructing the patch against the immutable base in memory. The live worktree's CHANGELOG/FLOW subsequently changed; those later edits are outside this report.

The approved addendum (`independent-error-taxonomy/needfrom-only-addendum/review.md:5`) requires “localDurable && durableVotes < writeQuorum && durableVotes + len(repairs) == len(authority.Voters)” only for the original quorum-unavailable sentinel. Lines 18–22 additionally require bounded NeedFrom, unchanged cancellation/mixed-failure semantics, and no published Ready/HW/frontier.

`recovery_barrier.go:59–67` implements exactly that conjunction, checks every repair offset is positive and no greater than the barrier's last offset, and joins `ErrNotReady` while preserving the original sentinel. Existing round validation binds each repair to one remote voter, enforces DefinitelyNotWritten plus the repair cause, and rejects duplicate/late callbacks; durable votes and repairs are disjoint. A mixed unknown/conflict/backpressure/submission failure cannot satisfy the all-voter count. Other errors and successful quorum outcomes retain their existing paths.

The error still exits Install before frontier, HW or readiness assignment (`quorum_log.go:191–201`), leaving business admission closed. The nineteen-case Install table covers both new positives, healthy quorum, mixed/local/malformed failures, cancellation, unchanged original committed history, and recovery after removing the injected fault. No wire6, generic round, HTTP retry, repair scheduling or readiness-budget changes appear in the patch.

Static review only; no tests or mutations performed. Supplied focused and package results passed; the original frozen FLOW check failed because 152 lines exceeded its 150-line limit, so this is not an all-gates-pass statement. The actual voter cause of the earlier Mac HTTP 400 and original Linux refusal remains unknown; combined real-topology validation is separate.

# Combined v2 — limited Spec integration review

**0 omissions, implementation mismatches, or scope expansions found.** The final seven-file scope at base `9a4900dc67ac674791edba11ea9a719cd91ec570` matches the frozen file hashes and full patch SHA-256 `38519d247817780b633401280ffebba3433bf0f8b0bf5ee1e6bbc059a38c03b0`. The assembled tracked/untracked working-copy patch also matches byte-for-byte.

The recovery-barrier production Go file and nineteen-case test retain their reviewed core hashes. The migration scenario Go file and scenario AGENTS retain their reviewed fixture-v2 hashes. Their earlier Spec conclusions therefore remain applicable; no unchanged implementation was re-reviewed or retested.

The integration differences are exactly the authorized documentation changes:

- FLOW compresses the four added lines into two equivalent lines: local durability plus a bounded NeedFrom receipt from every missing voter; other failures remain closed and no repair is added. The file is now 150 lines.
- The generated FLOW index changes only the channel FLOW count from 148 to 150; its existing warning status remains.
- CHANGELOG contains the two prior reviewed entries verbatim, with no additional behavior claim.

The approved NeedFrom-only addendum and fixture ordering/retry constraints remain intact. No wire, generic quorum round, HTTP retry, timeout, credential assertion or business-readiness changes were introduced by integration.

Supplied named-check evidence records passing `flow-doc-contracts` and `go-format`; earlier documentation failures remain retained. This review ran no tests, builds or gates and changed no source. Real combined E2E results are separate and not asserted here; the original Mac HTTP 400 voter cause and Linux connection-refusal cause remain unknown.

Standards: 0 findings; Spec: 0 findings. E2E results are recorded in their separate execution receipts.
