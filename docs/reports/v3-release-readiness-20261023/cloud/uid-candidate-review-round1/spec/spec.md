# Spec review — round 1

Fixed scope: d349a7bdd2ca8f6bd4683fbccbf83010d3f9cd3e → 3e668ac4733ae403e57242ddec559e6d937b0d3c; tree f70fc869cf65bb03ac42fd43d19e85c32a0fe114. Static review only; no tests, service, cloud or GitHub operation was executed.

**One P3 documentation finding; no implementation correctness finding.**

- **P3 — State the failed-flight exception explicitly.** `docs/development/PROJECT_KNOWLEDGE.md:1081` says “Every waiter rereads Directory”, but `recovery_flights.go:69–87` directly returns a failed flight status. The earlier approved `cold-lane-uid-flight-design-v1/design.md:15` likewise says “every caller rereads Directory”; final `approved-spec.md:15` narrows this to “each successful caller”. This needs an explicit design clarification and accurate knowledge wording, rather than an implicit post-implementation weakening. The pre-code matrix #9 expressly requires a live follower of a canceled leader to receive typed not-ready without another owner query, and #18 requires a current Directory read for every success. Therefore direct failure propagation is consistent with that specific failure contract and cannot fabricate offline or stale success. Its availability limit is that a late waiter may retain one retryable failure even if another cohort has subsequently installed valid proof. A runtime change is not required on this evidence.

No missing/partial functional requirement or scope creep was found against the final 19-case matrix and metadata leaf mapping. UID+five-field identity, original-target forwarding, C4/K2048/32-MiB/128-MiB limits, cloned ownership, owned completion before joining, current-proof reads after successful completion, cancellation isolation, original packing and owner gate remain intact. Metadata additions wrap existing returns with the 13 closed leaves plus invalid, preserve error identity, three-source joins, settle limits, baselines and incomplete-report verdicts.

Existing frozen RED/GREEN/race receipts were inspected, not rerun. Exact-candidate process E2E, Linux performance and cloud causal equivalence remain unverified; the candidate does not establish a message-loss fix.
