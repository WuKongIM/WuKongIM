# Standards review — PR #1024 fixture optimization

No actionable findings: 0 documented-rule violations and 0 actionable smell judgments.

Scope is only `scripts/testdata/chat_lifecycle_terminal_evidence_test.py` relative to `c91e37b4e35e1009c9c5cb55ff351a38b870014f` (+110/−67). HEAD, status, untracked discovery, supplied freeze, and every frozen file digest match. The earlier seven-file implementation is outside this review.

The change retains the real collector and helper process boundary (lines 179–183), serial execution, actual NUL-delimited SSH command observations (lines 61–77, 129), requested byte clipping, nonzero partial-output behavior, and stdout/stderr/manifest secret-canary assertions. Paths remain quoted; commands are matched and parsed rather than evaluated. Boundary fixtures derive timestamps independently and require the actual query bounds before returning bytes (lines 91–124).

Applicable rules are root `AGENTS.md` (test-first, directly related validation, process-test integration tier, bounded design), `CONTEXT.md`, and `.github/workflows/README.md` (V3 candidate regression evidence and unchanged original limits). No applicable ancestor FLOW or child AGENTS exists. The failure matrix and unchanged pre-existing tests support the test-first requirement; all 20 test methods and the remaining test/assertion tail are byte-identical. No production configuration, policy, workflow, or concurrency changes occur.

All 12 supplied smell heuristics were assessed as judgments, with repository rules taking precedence. The small shell response adapter and Python actual-command decoder serve distinct responsibilities; this bounded seam does not justify additional abstraction.

Evidence inspected, without execution: the first focused JSONL records both related tests passing; the retained comparison records 79/79 complete case receipts equal, with both input digests verified and only observation timestamps/derived manifest hashes normalized. The original Linux receipt still records a 600.108-second timeout. This local focused result does **not** establish recovery of the original full Linux named check. No tests, CI, collector probes, source edits, or cloud actions were performed during this review.
