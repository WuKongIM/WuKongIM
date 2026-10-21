Cloud chat-lifecycle startup previously reached paid procurement without checking that its exact tracking Issue existed. A later pre-clock preparation exit could then fail to publish repair state and release the Lease, while the shared stage_readiness_timeout label did not distinguish a stopped unit from SSH/classifier failure or an elapsed deadline. The first observed request is documented in #1018; its earlier preparation cause remains unknown.

This change checks one exact open, unlocked Issue before deployment-bundle construction, key generation, Quote or Acquire. It retains a separate best-effort preflight artifact and records bounded per-generation exit evidence before terminating the unit: command/classifier statuses, safe systemd fields, closed summary codes, byte/hash metadata and observed deadline instants. Original control classifiers, workload thresholds, retry/repair rules and cleanup decisions remain in place. Diagnostic writes and the independent preflight upload cannot block cleanup or authorize procurement.

Validation:

- Test-first CLI/stub regressions cover missing/ambiguous/unusable Issues, complete-stage rejection before any build/key/cloud action, exact command exits, actual readiness-loop branches, redaction and diagnostic write failures.
- Combined local focused run passed 78 test/subtest events with zero failures. Three nested summary SSH cases are explicitly skipped on Darwin because its regex rejects the unchanged cursor bound; Linux CI must execute them before that coverage is claimed.
- Bash 3 and Bash 5 focused checks, the frozen go-format/workflow-contracts named checks, two-workflow actionlint, shell syntax and diff checks passed.
- Independent review found no remaining blocker. The new Issue preflight also passed a real read-only GitHub invocation against #1018.

No paid cloud workflow was dispatched for this change. The original request was already released with exact zero-inventory evidence, and this PR does not claim cloud rehearsal or v3 qualification success. Automatic Linux PR checks are pending.
