# pkg/backup Flow

`pkg/backup` owns the portable cluster-backup artifact contract. It does not
read WuKongIM storage, schedule jobs, call cluster APIs, or know a concrete
object-storage/KMS provider.

Current flow:

1. A caller builds one `Manifest` containing every logical hash-slot cut and
   the immutable encrypted objects needed by the restore point.
2. `SignManifest` validates the unsigned contract, encodes its canonical JSON,
   and asks the injected `ManifestSigner` to sign those exact bytes.
3. `MarshalManifest` validates and serializes the signed manifest.
4. `LoadManifest` strictly decodes JSON, validates the complete contract,
   rebuilds the same unsigned canonical bytes, and verifies the signature before
   returning trusted metadata. Production composition wraps the provider signer
   with `NewKeyPinnedManifestSigner`, so only the active signing key and an
   explicit operator-managed retained-key allowlist are trusted even when
   provider IAM can verify other keys.
5. `ReplicatedPublisher.Publish` uploads and verifies every immutable object in
   both explicit repositories before it signs and stages the identical
   restore-point manifest in both repositories. Partition references are
   verified recursively through their complete base chain in both repositories,
   so a new incremental point cannot hide missing historical objects. Only
   after both manifests verify does it write a separately signed publication
   marker; failed manifest copies leave only undiscoverable orphan objects for
   later garbage collection.
6. `LoadRestorePoint` requires and authenticates the publication marker, binds
   it to the staged signed manifest checksum, then proves that every referenced
   immutable object still has the expected size and ciphertext checksum before
   restore may consume it.
7. `LoadRestorePointGraph` authenticates the complete top-level and recursive
   partition-manifest graph and returns the exact reachable key set used by
   retention mark-and-sweep.

The effective restore-point time is the oldest partition watermark. Manifests
must describe every hash slot exactly once and must use safe immutable object
keys.

Partition manifests may point to one prior partition layer for incremental
message deltas. Channel-index objects carry the latest per-channel epoch,
retention start, and committed HW without placing channel identities in
Controller state. Object plaintext is zstd-compressed before AES-256-GCM
encryption; each object has a fresh envelope data key and nonce.

Format v2 requires explicit versioned partition evidence. Each signed top-level
partition reference repeats the authenticated tip's latest metadata-record
count, cumulative base-to-tip message-record count, and cumulative maximum
message ID. A missing evidence version is not an empty partition. Base
references cannot regress cumulative counts or the allocator fence.

Permanent message erasure uses a separate portable append-only artifact chain.
The Channel identity and deletion boundary live only in a freshly encrypted
event object. A signed record binds that object to its hash slot and stable
event ID, and a signed, contiguous sequence commit makes the record visible.
The same signed commit bytes are also stored at a deterministic per-event
receipt key, which preserves idempotency after later events advance the
contiguous sequence without growing Controller state.
Restore plans pin an exact versioned ledger prefix by boundary and SHA-256;
boundary zero is represented by the explicit digest of the empty prefix, never
by missing evidence.

## Continuous Segment Foundation

The replacement continuous-capture path starts with a content-addressed
segment contract. It is not wired into capture or restore scheduling yet.

1. `SegmentCodec` hashes a canonical logical header containing repository,
   source-generation, Slot-generation, stream, sequence, record-count, and
   plaintext evidence. That digest is the stable Segment ID.
2. Compression, a fresh envelope data key, AES-256-GCM nonce, and ciphertext
   checksum are intentionally outside the logical identity. A retry may create
   a different encrypted representation without changing the Segment ID.
3. Ciphertext is stored below
   `segments/<segment-id>/payloads/<ciphertext-sha256>.bin`. A signed
   `segments/<segment-id>/commit.json` binds one representation to the two
   explicit repository identities.
4. `ReplicatedSegmentStore.Commit` writes and verifies both payload copies,
   then the secondary and primary commit proofs. A failed call returns the
   stable attempt reference, but `Load` rejects it until identical commit and
   payload copies exist in both repositories. The reference repeats the
   authenticated plaintext size, allowing callers to reserve memory before
   opening it; `Load` verifies that size against the signed commit header.
   `VerifyCommit` authenticates the exact current proof in both repositories
   without opening payload bytes or following predecessor links.
5. If either repository already has a valid signed commit, a retry verifies
   that its logical header matches the requested plaintext, repairs the missing
   payload and commit copy from the healthy repository, and does not request a
   new KMS data key.
6. Segment commit decoding is strict and bounded. Unknown fields, trailing
   data, unsupported format/version values, unsafe identities, invalid sizes,
   checksum mismatches, and invalid signatures fail closed.
7. Seal and open reuse compressed/ciphertext backing storage where safe. Each
   operation reserves three times its logical payload size plus 16 MiB for
   codec overhead before hashing or transforming data. One replicated store
   therefore admits at most 784 MiB of estimated segment working set, which is
   equivalent to one maximum 256 MiB logical segment or several smaller
   segments. The later capture runtime must apply its smaller rolling target
   and node-level concurrency budget before materializing plaintext and calling
   this boundary.
8. `SegmentBatch` is the strict portable plaintext inside continuous metadata
   and message segments. It binds the source page cursor interval, observed
   high watermark, prior committed Segment reference, ordered record frames,
   and independent record/cursor checksums. Message batches embed a bounded
   sorted `ChannelBoundary` index; metadata batches reject that index. The
   cursor list therefore remains in immutable repository artifacts instead of
   bounded Controller coordination state.
9. Each metadata record inside a metadata `SegmentBatch` uses the strict binary
   `MetadataLogRecord` envelope. It preserves the logical Hash Slot, physical
   Slot Raft index/term, proposer timestamp, and exact FSM command bytes needed
   for ordered replay; malformed lengths, empty commands, and trailing bytes
   fail closed.
10. `MessageLogRecord` is the strict portable committed Channel row. It carries
    Channel identity/epoch/retention cut plus durable message sequence, message
    ID, idempotency/display fields, flags, timestamp, and payload. A distinct
    boundary-only kind represents epoch or retention movement without
    fabricating a message.
11. `MessageCursorBatch` is a cursor-only immutable sidecar stored under the
    `message_cursor` stream. Ordinary batches contain sorted cursor deltas and
    link only to the previous cursor sidecar, never to payload segments. A full
    checkpoint has no predecessor and bounds chain reconstruction to 1024
    sidecars.
12. `Checkpoint` freezes exactly one sorted frontier for every configured Hash
    Slot. Empty but fully reconciled streams are explicit zero-sequence heads,
    and the checkpoint effective time is the oldest Slot watermark. Repository,
    source cluster, source generation, Slot generation, stream, and sequence
    bind its current references to the authenticated segment commit headers.
13. Each signed `CatalogPage` authenticates its checkpoint references and the
    exact previous page reference. Checkpoints use deterministic immutable keys;
    catalog pages use sequence-plus-checkpoint keys so an unpublished orphan
    cannot collide with a later retry. The Controller stores only the newest
    page reference and never stores catalog history.
14. A materialized Slot rebase uses partition-manifest version 3. An
    independent root may bind one committed `message_baseline_cursor`
    `SegmentReference`, containing the complete Channel index used to resume
    continuous message capture without replay. Incremental manifests reject
    that field. Its cut also records the physical Slot ID that owns the Raft
    index space, so a retry after routing remap cannot reuse an old baseline.
    Checkpoint version 2 optionally binds the materialized
    partition and cursor proof beside the current stream heads.
15. Retained-graph traversal authenticates a baseline cursor's signed segment
    commit and marks both the commit and encrypted payload key, so generation
    or restore-point GC cannot delete a live cursor representation.
