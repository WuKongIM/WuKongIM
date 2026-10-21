# PR #1024 fixture optimization: first implementation receipt

Only `scripts/testdata/chat_lifecycle_terminal_evidence_test.py` is modified on top of `c91e37b4e35e1009c9c5cb55ff351a38b870014f`. Collector/helper, Go integration entrypoints, policy/workflows, limits, concurrency, and all 20 existing test-method bodies are unchanged. The optimization prepares synthetic SSH response files once per case and uses a shell adapter for the unchanged actual process boundary. Actual SSH commands are recorded and decoded after collection; parameter-dependent boundary responses also check actual query bounds against independently prepared fixture time.

The original RED is the first Linux scripts-integration timeout at 600.108 seconds, preserved separately under `cloud/pr1024-ci-generation-1/candidate`. The existing 20-method/79-case behavior tests and their archived artifact preceded this optimization. `plan-and-failure-matrix.md` and `before-freeze.json` record the prior specification, failures, and unchanged test-method hashes. No implementation-coupled process-count unit test was introduced.

The first related Go integration run passed both tests with exit 0:

- `TestChatLifecycleTerminalEvidenceRetention`: 20 methods / 79 collector subprocess cases, zero failures/errors, 60.60 seconds.
- `TestChatLifecycleTerminalEvidenceCollectorRequiresTerminalAnchor`: passed, 1.20 seconds.
- Package: passed, 61.141 seconds, `-count=1`, `-parallel=2`.

`focused-first.jsonl` and `focused-first-results.json` preserve the first output and original-format artifact. All 79 complete case receipts compare equal to the sealed original V2 artifact in `semantic-comparison-first.json`. Only `manifest.observed_at` and its derived manifest-file SHA are canonicalized; each original manifest byte count/SHA is verified first. Case labels, exit codes, every state/window/query/budget, ordered calls, and every other retained file byte count/SHA remain exact. The different source SHA headers are recorded separately as true provenance, not falsified or canonicalized.

The original 20 method bodies have identical SHA-256 hashes before and after. `review-source-freeze.json` freezes the single modified file at `3db0a4151b25d851d2594eb452308f6fe2ec4192ee98406f0a5fb5904becc2b1` for dual review. No additional source edit, commit, push, CI rerun, full-suite rerun, or cloud operation is part of this receipt.

The 60.60-second local focused observation is not a controlled Linux comparison and does not prove the original 600-second policy check is repaired. The reviewed next head must pass its first original full Linux named check. The earlier bounded profile, including its preserved failed scratch prototype and corrected five-case proof, remains separately sealed under `cloud/pr1024-fixture-profiling`.

Reproduce the exact focused command from a source checkout with these frozen file contents:

```sh
GOWORK=off CHAT_LIFECYCLE_TERMINAL_EVIDENCE_TEST_ARTIFACT=/tmp/terminal-evidence-results-new.json go test -tags=integration ./scripts -run '^(TestChatLifecycleTerminalEvidenceRetention|TestChatLifecycleTerminalEvidenceCollectorRequiresTerminalAnchor)$' -count=1 -timeout=4m -parallel=2 -json
```

This command launches fixture processes only. It is a focused integration check, not the protected `scripts-integration` named check. Use a new artifact path to preserve prior receipts.
