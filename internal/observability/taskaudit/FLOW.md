---
scope: package
summary: Persists and projects bounded Controller task audit history for manager queries.
---

# Controller Task Audit Flow

## Responsibility

This package stores bounded Controller task audit events in JSONL and maintains
an in-memory projection for manager list and timeline queries.
It does not execute Controller tasks or own Manager transport and authorization.

## Boundaries

- It owns persistence, replay, projection, ordering, and retention only.
- Manager HTTP DTOs, authorization, and Controller task execution live outside
  this package.
- It does not import cluster runtimes or use cases.

## Main Flows

1. A Controller transition is appended to JSONL and applied to the task
   snapshot and per-task event timeline.
2. Retention drops old tasks or events by applied Raft index and rewrites only
   the retained JSONL projection.
3. Startup replay scans line by line, skips corrupt records, rebuilds bounded
   projections, and reopens the store for append.

## Invariants and Failure Semantics

- Ordering and retention use `AppliedRaftIndex`, never wall-clock time.
- Append and compaction serialize so accepted events are not lost.
- Corrupt replay lines are skipped without making later valid records unreadable.
- In-memory and rewritten JSONL projections contain the same retained events.

## Read First

- [Store](store.go)
- [JSONL persistence](jsonl.go)
- [Retention](retention.go)
- [Audit model](model.go)

## Update Triggers

Update this file when event fields, replay, ordering, retention bounds,
compaction, or query projections change.
