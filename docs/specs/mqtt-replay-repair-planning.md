# Bounded MQTT repair interval selection

A replica selects recovery work from one pinned view of its local shared-content
frontier and committed anchor journals, capped by one exact committed target
anchor. It never accepts caller-provided HW or treats journal presence as content
coverage. The target anchor may be older than the latest journal; completion is
relative to that exact target, not permission to promote a learner or release data.

Planning returns the current prefix, verified target, and exactly one of: a next
anchor interval, completion, or an explicit scan continuation. At most 64 journals
are examined per call. The next interval starts after actual local coverage and
must reach a committed anchor within 256 rows / 16 MiB. Covered journals are checked
against local cumulative meters before skipping. A continuation is only a hint:
the replica verifies that its anchor is already locally covered before seeking
past it, so a supplied cursor cannot skip missing data or claim completion.

The planner uses existing System 14 journals and replay meters. A bounded tail
point check validates the local frontier; history is never materialized. Planning
writes nothing and cannot advance HW, source release or the replay frontier.
Changes between planning and import remain fenced by the existing atomic import
contract: concurrent progress may require replanning, never overwriting a prefix.
No table, index or stored format changes are required.

## Failure inventory before implementation

1. Absent, pending, foreign or corrupt target proof is accepted; a requested
   anchor position raises HW or a later journal changes the requested endpoint.
2. Missing replay content is inferred from source copied-through or a journal;
   a corrupt local tail/meter or different covered anchor digest claims completion.
3. A continuation skips an uncovered interval, a business position masquerades
   as a cursor, or an exhausted scan reports completion instead of resumable work.
4. Planning starts before/after actual local coverage, returns an unbounded or
   partially authenticated interval, overflows offsets/counters, or crosses sources.
5. Original-body cleanup or restart prevents ordered recovery. Repair retries,
   partial local copy and local copy-ahead reset progress or lose the target bound.
6. Cancellation/closed leases leak snapshots; invalid budgets escape validation;
   Channel adaptation drops proof/cursor fields or exposes message-domain DTOs.

Tests precede implementation at the approved durable-store/Channel adapter seams.
The existing cross-node repair port consumes selected intervals; automatic runtime
scheduling and source-release/readiness orchestration remain required.

## Frozen context

Source `cb65b5e72`; SHA-256:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `pkg/db/FLOW.md`: `8b723e6f1402c2361a872e3a6e4021ac4146782ebc9b416cb7b9cc9787bb9d75`
- `pkg/db/message/FLOW.md`: `cf2f6c23a0554a262841ae5892e4fe302662445b22f7e40537b31c5263f3c4e7`
- `pkg/channel/FLOW.md`: `9d41093ad0224071722b4d8b6e2aeb6e84d79371e1ca3fdc36a1bdd28c57e0fa`
- `pkg/cluster/FLOW.md`: `bcba83eddef61a9072a8e8584e774d8900f48d1519449be671622b79c77be336`
