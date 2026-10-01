# Standards review

Scope: working-copy `git diff HEAD` from
`68eb337c50267fb2e0114130ef2b028760d17555`, plus the three untracked files
listed in `review-scope.json`; no branch commits.

Standards: user-supplied working agreements and root `AGENTS.md`, the E2E/MQTT
and scenario instructions, applicable valid package FLOW documents, and the
Fowler smell baseline. Repository rules take precedence; tooling-enforced checks
are left to the root task.

No actionable documented-rule violations or baseline smell findings.

The change preserves the architecture boundaries: the message usecase carries
a narrow callback, the authority router consumes it before local or remote
submission, and the journal adapter owns exact durable transitions. The callback
does not enter publication metadata or accepted append work. Single and batch
paths retain item alignment and existing concurrency bounds. Version-1 Admitted
records remain distinct from sealable version-2 admission; issued permission is
not treated as proof that an append stopped.

The process tests use independent Paho clients, public provisioning, 256 hash
Slots and exact harness-owned crashes. They add no internal/storage imports or
post-implementation unit tests. The recorded failure inventory and frozen
cut-only baseline identify the tests-first tracer. Changelog, stable knowledge
and affected FLOW documents describe the proof limit and conservative migration.

This was read-only code review; no process tests were run here. Full capacity
stress and terminal recovery of unknown issued effects remain explicitly outside
this round's qualification.

Findings: **0**. Worst priority: **none**.

## Supplement

Reviewed the original scope plus the three post-review files frozen in
`review-supplement-scope.json`; product Go hashes remain unchanged. The startup
counter uses a permitted non-config gofail control through `NodeSpec.Env`.
The legacy upgrade atomically changes only a case-owned temporary symlink,
then restarts the exact stopped process through the existing harness. Running
peers and external binaries are untouched. The FLOW whitespace adjustment and
regenerated index add no semantic change.

Additional findings: **0** documented-rule violations; **0** baseline smells.
The corrected 14-case rerun was still running at review time; this supplement
does not claim its completion. No tests or source mutations were performed here.
