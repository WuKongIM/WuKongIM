# PR75 persisted-expiry repair review

## Standards

# Standards review — PR #75 persisted-expiry repair

**No findings.** No hard-rule violation or actionable heuristic smell was found in this four-file delta.

Scope: working-copy changes against `d824fe37918ee4ef22b1b1745bc02518812563e3`. Four file hashes and patch `ae81b89b…8960e0` match the frozen author copies; independently calculated tree is `2119196cef38a159f23a8f5c47f8878e4fde8399`. No untracked source exists. All other 114 tracked files, including production validator, workflows, controls and keys, match HEAD exactly.

Hard standards: applied the user-supplied working agreements/AGENTS Code Quality, test-first and release-note rules, plus package contracts. No local AGENTS/FLOW/CONTRIBUTING file was found. The changed helper retains an English responsibility comment (`tests/native_stable_clients_drill.py:444`) and confines the change to TEST ONLY `rpm-switch`. It preserves first-phase `--refresh`, the existing strict boundary checks and the original production builder. This respects the test/production separation in `docs/release-contract.md:751` and the exact-A, signatures, serial/order and response-completion requirements in `docs/stable-audit-transport.md:149`. The concise `CHANGELOG.md:5` entry and updated operator explanation describe test behavior without claiming publication.

Judgment: all twelve supplied smell heuristics were considered; none warrants a finding. Two explicit replacements describe two fixed shell phases. A generic command-rewriting framework or new domain type would add complexity without a present need. The test's expected command is deliberately explicit and supplemented by independent native evidence; it does not introduce duplicated production behavior.

Read-only evidence review confirmed the frozen test file preceded implementation, its initial 44-test run had one failure, and the related run subsequently passed. Existing final receipts record 557 Python tests (one Mac skip), contracts exit 0, and pinned Rocky/Alma controlled native comparisons with cleanup verified. The original command selects B under injected persistence delay; the candidate selects A, while fresh-B still selects B.

Limits: no tests, CI or provider calls were run by this reviewer. Controlled probes establish a reproducible command-sequence defect, not unique retrospective CI timing. New-head complete 49-case Linux acceptance remains unverified by this review. Tool-enforced formatting results were not duplicated. All reviewer commands are terminal.

## Spec

# Spec review — no findings

Reviewed working-copy delta against `d824fe37918ee4ef22b1b1745bc02518812563e3`; candidate tree `2119196cef38a159f23a8f5c47f8878e4fde8399`. All four hashes and patch `ae81b89b…60e0` match the freeze; no branch commits or untracked files. The Standards report was not consulted.

- **Two-phase policy is complete.** Scope line 5 requires “only TEST ONLY rpm-switch” and “first makecache MUST retain --refresh”. Driver lines 444–461 restrict the override to RPM/switch, require unique ordered phase boundaries, and add the repository-scoped option to both commands while retaining initial `makecache --refresh`. No cache-only flag, state deletion, timestamp mutation or retry was added.
- **Acceptance remains strict.** `docs/stable-audit-transport.md:156–166` requires the actual B mirrorlist, then B-prefixed exact-A bytes, authenticated A metadata and completed responses. Unchanged driver lines 132–220 retain explicit B identity, same positive serial, response completion, ordered B-mirror-before-payload, and original-member/hash/size checks. Lines 908–921 still enforce exact-A downloaded identity. Production validator, other modes, fresh-B and persistent-cache paths are unchanged, meeting scope line 5 and documentation lines 159–160.
- **Evidence boundaries are accurate.** Scope line 7 requires persisted-expiry reproduction without claiming unique historical causation. Existing Rocky/Alma receipts show old-command explicit expiry selecting B, the candidate selecting A through B's prefix, and fresh-B selecting B. Both candidate bodies bind to the frozen shipping body. Diagnostic delay and data-only TEST ONLY payloads are expressly limited; shipping commands contain no delay.

Tests-before-code evidence records one assertion failure against the unchanged driver; the final test hash is identical. Existing results report 557 Python tests with one existing skip and passing contracts. I ran no tests, containers or external requests. **Complete exact-head Linux 49-case acceptance is still required**; these receipts do not establish that result or the original CI's unique cause.

Spec findings: **0**.

Standards: 0 findings. Spec: 0 findings. Each axis was reviewed independently; scheduling was sequential because available slots were occupied by live paid-run monitoring and the separate provider implementation. Full new-head Linux acceptance remains required.
