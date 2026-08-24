---
scope: package
summary: Adapts backup repositories, cluster routing, Controller state, export, and restore ports without owning backup policy.
---

# Backup Infrastructure Flow

## Responsibility

This package implements backup infrastructure ports for repository storage,
Controller-owned state, cluster routing, node RPC, full export, and restore.
Backup schedules, lifecycle policy, and operator decisions belong to the
backup use case.

## Boundaries

- Repository providers may use files, OSS, COS, or S3; credentials stay
  encrypted outside active provider calls.
- File repositories anchor paths under their configured root and reject
  symlinks. Object adapters stream artifacts rather than placing payloads in
  RPC messages.
- Controller state is bounded and revision-CAS protected. It must not contain
  manifests, channel identifiers, repository listings, or plaintext secrets.
- Cluster adapters resolve physical Slot authority; they do not invent backup
  or restore policy.

## Main Flows

1. A shared-repository probe writes a unique marker, verifies receipts from
   every node, and returns a stable secret-safe failure result.
2. Full export resolves Slot leadership and term, fences a stable snapshot,
   streams metadata and grouped message artifacts directly to the repository,
   and records all 256 Slot results before archive publication.
3. Restore enters maintenance, stages and verifies data, rechecks topology,
   switches atomically, and rolls back on failure before cleanup.

## Invariants and Failure Semantics

- Every export artifact is attempt-scoped; late workers cannot overwrite the
  accepted attempt.
- Restore work is idempotently fenced by job, Hash Slot, and attempt.
- Large archive payloads never cross node RPC.
- Publication is incomplete until the manifest-bound `COMPLETE` marker exists.
- Ambiguous repository, authority, or topology state fails closed.

## Read First

- [Repository providers](repository_provider.go)
- [Full export service](full_export_service.go)
- [Archive finalization](archive_finalizer.go)
- [Distributed restore](distributed_restore.go)

## Update Triggers

Update this file when provider safety, Controller state, export fencing,
publication, restore phases, or RPC payload boundaries change.
