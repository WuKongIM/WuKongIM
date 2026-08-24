---
scope: package
summary: Defines the portable full-backup repository format, strict manifests, compressed chunks, publication markers, and verification.
---

# Portable Backup Format Flow

## Responsibility

This package owns the entry-independent repository contract for one scheduled
full backup. Scheduling, credentials, cluster routing, concrete stores, and
restore orchestration remain outside it.

## Boundaries

- `ArchiveStore` exposes bounded exact-key put, open, list, delete, and prefix
  delete operations; file and object-store adapters live in internal infra.
- `repository.json` binds an empty repository to one cluster identity.
- A backup is discoverable only when `COMPLETE` authenticates the canonical
  top-level manifest.

## Main Flows

1. Capture one stable physical Slot authority cut per Hash Slot; write metadata
   first, followed by portable message streams split at 64 MiB logical bytes.
2. Bind every Zstandard chunk to stored and logical SHA-256, assemble a bounded
   message index and strict Slot manifest, then cover exactly 256 unique Slot
   manifests in the top-level manifest.
3. Verify every manifest and compressed chunk before publication; retention
   keeps configured newest healthy archives and every archive carrying `HOLD`.

## Invariants and Failure Semantics

- Attempt paths are immutable and stream/part order is canonical.
- Decoders reject unknown fields, trailing JSON, unsafe keys, unsupported
  versions, missing Slots, non-canonical data, size mismatch, and digest mismatch.
- Message indexes and manifests have explicit byte and entry bounds.
- Verification rereads every stored compressed chunk.
- Held archives cannot be deleted until `HOLD` is removed.

## Read First

- [Core contracts](core.go)
- [Archive manifest](archive_v1.go)
- [Slot manifest](slot_manifest_v1.go)
- [Chunk format](chunk_v1.go)
- [Archive verification](archive_verify.go)

## Update Triggers

Update this file when repository layout, format versions, chunking, canonical
encoding, digest coverage, publication, verification, or retention changes.
