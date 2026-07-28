# pkg/backup Flow

## Responsibility

`pkg/backup` owns the portable, entry-independent format for one scheduled
full backup. It defines repository operations, strict manifests, compressed
chunks, publication markers, and whole-archive verification. Scheduling,
cluster routing, credentials, and restore orchestration stay outside this
package.

## Repository layout

```text
repository.json
backups/<backup-id>/
  manifest.json
  COMPLETE
  HOLD                         optional retention hold
  slots/<hash-slot>/
    manifest.json
    attempts/<attempt-id>/
      meta-<sequence>.zst
      messages-<sequence>.zst
      message-index.json
```

`repository.json` binds an empty repository to one cluster identity. A backup
is discoverable only when `COMPLETE` exists and authenticates the exact
canonical top-level manifest.

## Artifact flow

1. Each logical Hash Slot captures one stable physical Slot authority cut.
2. Metadata is written first, followed by zero or more portable message
   streams.
3. Logical streams are split at 64 MiB, compressed with Zstandard, and bound
   to both stored and logical SHA-256 digests.
4. A strict, size-bounded message index lets a remote producer return a
   fixed-size receipt instead of a chunk list over RPC.
5. A strict Slot manifest covers ordered chunk kinds, streams, parts, totals,
   and the maximum message ID.
6. The top-level manifest covers exactly 256 unique Slot manifests and their
   aggregate totals.
7. Publication verifies all Slot manifests and chunks before writing
   `COMPLETE`.

Decoders reject unknown fields, trailing JSON, non-canonical manifests, unsafe
keys, unsupported versions, missing Slots, invalid stream order, size
mismatches, and digest mismatches. Verification always re-reads every
compressed chunk. Message indexes and Slot manifests have explicit byte and
entry limits, so malformed repositories cannot force unbounded decode memory.

## Repository seam

`ArchiveStore` provides bounded `Put`, `Open`, `List`, `Delete`, and
`DeletePrefix` operations. Implementations must preserve exact keys and object
sizes. File and S3 adapters live in `internal/infra/backup`.

Retention operates only on published archives. It keeps the configured newest
healthy archives plus every archive with a `HOLD` marker. Held archives cannot
be deleted until the marker is removed.
