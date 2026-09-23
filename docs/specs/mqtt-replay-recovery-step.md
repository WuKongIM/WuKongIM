# Target-owned bounded MQTT replay recovery steps

A foreground recovery step binds one exact target replica, source incarnation,
Channel/leader/route fences and committed target anchor. The receiver runs its
bounded local planner, then either returns verified completion/scan continuation,
or tries at most four current donor replicas for the selected interval. Donor
attempts have 750 ms sub-deadlines inside the five-second step. Leader and ISR
replicas precede learners; a returned donor cursor resumes rotation after a failed
bounded round. A missing/removed cursor only restarts candidate selection and
cannot skip content. Nodes without another donor return not ready if content is
missing; no source or checkpoint authority is fabricated.

Results preserve the full planner evidence and have four closed outcomes:
complete target coverage; scan continuation; one imported interval; or an explicit
next-donor cursor for the still-missing interval. A successful import does not
itself label the target complete; the next planning read verifies completion.
Malformed donor pages are skipped, while local import failures are surfaced.
Current metadata is checked before planning, before each fetch/import, and before
returning. Stable migration write fences permit immutable recovery; changed fences
withhold results. Existing receiver/donor admission is reused without nested
receiver reservations, new queues or per-Channel goroutines.

Node/RPC 99 exposes this body-free step with a 4 KiB closed versioned codec and
full request echo. The donor body uses existing RPC 98. Higher-level scheduling
must retain returned continuation hints and use a current target anchor; the step
alone is neither source release, migration readiness nor a background scheduler.
Version 2 adds explicit source release; [version 3](mqtt-retirement-recovery.md)
adds explicit application of locally committed retirement before planning. A
separate flag reports pending bounded cleanup even when logical coverage is
complete. Original version-1/2 bytes and behavior remain unchanged. No new table,
log format, configuration key or product MQTT listener is introduced.

## Failure inventory before implementation

1. A wrong target, stale authority, weak/changed membership or changed write fence
   runs work or publishes completion; a future proof passes historical validation.
2. A scan continuation becomes completion, import is mislabeled complete, or retry
   cursors skip missing content. Bad/removed donor hints prevent useful rotation.
3. One unavailable/malformed donor prevents trying other current copies; a round
   exceeds four attempts, a sub-deadline or global admission. Local corruption is
   hidden as harmless donor unavailability.
4. Cancellation, panic, missing capabilities or closed services leak leases,
   admission slots, work or borrowed bytes; nested receiver admission deadlocks.
5. RPC framing/version/echo/outcome/size handling accepts invalid state or a
   donor-cursor success with no missing interval. Gateway replacement bypasses gates.
6. Cross-node multi-interval recovery, restart or already-complete retries lose
   progress; absence of Slot quorum becomes success from locally cached content.

Tests precede implementation at approved Channel/cluster/RPC/Node seams. Full
product MQTT process acceptance and background lifecycle wiring remain required.

## Frozen context

Source `3f965b6b5`; SHA-256:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `pkg/channel/FLOW.md`: `dc9e860b178bf61fa9b125cd79c16a692871e87c39262ce711b9145f0a0270b8`
- `pkg/cluster/FLOW.md`: `bcba83eddef61a9072a8e8584e774d8900f48d1519449be671622b79c77be336`
