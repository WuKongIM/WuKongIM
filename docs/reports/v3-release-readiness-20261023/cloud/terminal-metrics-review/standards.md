## Standards

No actionable findings: 0 documented-standard violations and 0 Fowler smell findings (highest severity: none).

Scope: working-copy diff against `79a7177032a6ae1ec7015cefe0e6f94eb0f19f06`; all four intended files reviewed, including staged/unstaged scope and an empty untracked-file inventory. No applicable ancestor FLOW or more-specific AGENTS exists for these paths.

- `terminal-evidence.py`: the change is three fixed metric selectors plus their explanatory comment. All helper-function ASTs, the first five partitions, eight-request limit, 4 MiB response cap, 24 MiB shared cap and 60-second deadline remain unchanged. No runtime business path, arbitrary selector input, cloud command, workflow, policy or Skill changes were introduced. This follows AGENTS.md “Code Quality” and “Bug Diagnosis” requirements for a current, bounded observability repair.
- `chat_lifecycle_terminal_evidence_test.py`: the pre-implementation failure inventory and hashed test-first freeze retain the original helper; static and process RED receipts precede GREEN. The real collector fixture remains invoked by `chat_lifecycle_terminal_evidence_integration_test.go` with `//go:build integration`, satisfying AGENTS.md “Test Policy”. Existing negative cases remain. Independent expected metric tables are purposeful fixture expectations, rather than a production abstraction requiring extraction.
- `CHANGELOG.md`: the concise bilingual operator-facing fix is under Unreleased, satisfying AGENTS.md “Release Notes”.
- `PROJECT_KNOWLEDGE.md`: documents registered names, independent alias series, unchanged budgets and missing-coverage semantics, satisfying AGENTS.md “Documentation and Knowledge”.

All twelve supplied Fowler heuristics were considered; none warrants a finding within this small change. Repository standards take precedence over those judgement calls.

Coverage limit: read-only review and verification of supplied immutable evidence; no heavy tests or cloud operations performed by this reviewer. Recorded GREEN is 23 fixture tests / 80 cases, not a fresh Linux cloud acceptance result. This collector repair does not establish or fix the product message-loss root cause.
