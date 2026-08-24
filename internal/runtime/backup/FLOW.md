---
scope: package
summary: Owns node-local backup archive streaming, publication verification, and the Controller-leader scheduled supervisor.
---

# Backup Runtime Flow

## Responsibility

This package owns reusable node-local archive streaming and the scheduled
Controller-leader supervisor. Repository adapters, cluster routing, and backup
business policy remain outside the runtime.

## Boundaries

- `FullStreamWriter` chunks a closeable metadata or message snapshot into at
  most 64 MiB of logical bytes per Zstandard-compressed part.
- Leaders stream large payloads directly to the shared repository. RPC carries
  only bounded keys, digests, counts, and totals.
- The runtime publishes artifacts but does not select schedules, repositories,
  retention policy, or operator actions.

## Main Flows

1. Full export writes deterministic streams, chunk digests, and counts;
   metadata comes first and the Slot manifest is written last.
2. `PublishArchive` verifies all 256 Slot artifacts, writes and rereads the
   canonical manifest, then writes the manifest-bound `COMPLETE` marker.
3. The scheduled singleton advances restore first, then a due schedule, then
   the active full-backup job only while this node is Controller leader.

## Invariants and Failure Semantics

- Every retry uses immutable attempt-scoped paths; late workers cannot replace
  accepted artifacts.
- `FullExporter` never publishes the archive-level completion marker.
- Verification repeats marker, manifest, and chunk checks without mutation.
- Leadership loss stops new advancement; the next leader resumes recorded
  Controller state instead of creating a duplicate job.
- Hash Slot and archive identifiers never become metric labels.

## Read First

- [Full export](full_export.go)
- [Archive publication](full_publish.go)
- [Scheduled runtime](scheduled_runtime.go)

## Update Triggers

Update this file when chunking, compression, manifests, publication fencing,
verification, leader supervision, or runtime observability changes.
