# internalv2/observability/taskaudit Flow

## Responsibility

`taskaudit` stores bounded ControllerV2 task audit history for manager reads. It
persists JSONL under the node data directory and keeps a bounded in-memory
projection for list and timeline queries.

## Flow

```text
ControllerV2 task transition
  -> internalv2 app observer
  -> Store.Append
  -> JSONL append
  -> in-memory task snapshot and event timeline update
  -> retention by AppliedRaftIndex
  -> JSONL rewrite when retention drops tasks or events
  -> manager/usecase reads through Store.List and Store.Events
```

Replay on startup scans JSONL line by line, skips corrupt lines, rebuilds the
same bounded projections, and then reopens the file for append. Compaction
serializes with append and rewrites only retained events, including automatic
compaction when retention removes old tasks or per-task events.

## Boundaries

- This package does not own manager HTTP DTOs or permissions.
- This package does not import `pkg/cluster` or `internalv2/usecase`.
- Ordering and retention use `AppliedRaftIndex`, not wall-clock timestamps.
