# Standards review — PR #75 persisted-expiry repair

**No findings.** No hard-rule violation or actionable heuristic smell was found in this four-file delta.

Scope: working-copy changes against `d824fe37918ee4ef22b1b1745bc02518812563e3`. Four file hashes and patch `ae81b89b…8960e0` match the frozen author copies; independently calculated tree is `2119196cef38a159f23a8f5c47f8878e4fde8399`. No untracked source exists. All other 114 tracked files, including production validator, workflows, controls and keys, match HEAD exactly.

Hard standards: applied the user-supplied working agreements/AGENTS Code Quality, test-first and release-note rules, plus package contracts. No local AGENTS/FLOW/CONTRIBUTING file was found. The changed helper retains an English responsibility comment (`tests/native_stable_clients_drill.py:444`) and confines the change to TEST ONLY `rpm-switch`. It preserves first-phase `--refresh`, the existing strict boundary checks and the original production builder. This respects the test/production separation in `docs/release-contract.md:751` and the exact-A, signatures, serial/order and response-completion requirements in `docs/stable-audit-transport.md:149`. The concise `CHANGELOG.md:5` entry and updated operator explanation describe test behavior without claiming publication.

Judgment: all twelve supplied smell heuristics were considered; none warrants a finding. Two explicit replacements describe two fixed shell phases. A generic command-rewriting framework or new domain type would add complexity without a present need. The test's expected command is deliberately explicit and supplemented by independent native evidence; it does not introduce duplicated production behavior.

Read-only evidence review confirmed the frozen test file preceded implementation, its initial 44-test run had one failure, and the related run subsequently passed. Existing final receipts record 557 Python tests (one Mac skip), contracts exit 0, and pinned Rocky/Alma controlled native comparisons with cleanup verified. The original command selects B under injected persistence delay; the candidate selects A, while fresh-B still selects B.

Limits: no tests, CI or provider calls were run by this reviewer. Controlled probes establish a reproducible command-sequence defect, not unique retrospective CI timing. New-head complete 49-case Linux acceptance remains unverified by this review. Tool-enforced formatting results were not duplicated. All reviewer commands are terminal.
