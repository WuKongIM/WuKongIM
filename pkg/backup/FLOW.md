# pkg/backup Flow

`pkg/backup` owns the portable cluster-backup artifact contract. It does not
read WuKongIM storage, schedule jobs, call cluster APIs, or know a concrete
object-storage/KMS provider.

Current flow:

1. Continuous metadata, message, and cursor batches are sealed as immutable
   signed segments and committed to both explicit repository copies.
2. Each logical Hash Slot owns one independently replaceable Generation. Its
   durable frontier contains only current stream heads and source watermarks;
   Channel cursor detail remains in immutable sidecars.
3. A materialized rebase publishes one self-contained partition manifest plus
   one complete message-cursor baseline. The manifest has no predecessor
   layering.
4. A checkpoint freezes exactly one healthy frontier for every configured Hash
   Slot and the current permanent-erasure heads. Its effective time is the
   oldest included Slot watermark.
5. Signed hash-linked catalog pages publish checkpoint history. Controller
   stores only the current `CatalogPageReference`; complete history is rebuilt
   and authenticated from the repositories.
6. Production composition wraps `ManifestSigner` with
   `NewKeyPinnedManifestSigner`, so verification trusts only the active key and
   the explicit retained-key allowlist. `ManifestSignature.KeyVersionID`
   additionally pins provider versions, such as Alibaba KMS asymmetric-key
   versions, when the provider requires them for historical verification.

Object plaintext is zstd-compressed before AES-256-GCM encryption; every
representation has a fresh envelope data key and nonce. Strict decoding,
portable size limits, signed logical identities, and immutable safe object keys
fail closed before restore consumes bytes.

Permanent message erasure uses a separate portable append-only artifact chain.
The Channel identity and deletion boundary live only in a freshly encrypted
event object. A signed record binds that object to its Hash Slot and stable
event ID. Every Hash Slot has an independent contiguous commit sequence whose
signed commits link to the preceding commit digest. Commit paths are nested
under a stable SHA-256 namespace derived from repository ID, source cluster,
and source generation, so a successor generation can safely reuse the same
physical repositories and restart each Slot at sequence one. The same signed
commit bytes are also stored at a deterministic per-event receipt key, which
preserves idempotency after later events advance that Slot without growing
Controller state. Writers and readers share a one-million-event snapshot limit;
Controller admission counts committed and pending events so it rejects before
accepting an unrecoverable deletion. Restore plans pin sorted per-Slot heads,
their total event count, and the SHA-256 of that exact versioned snapshot. An
empty head set uses the explicit empty-snapshot digest, never missing evidence.

## Continuous Segment Foundation

The replacement continuous-capture and checkpoint-restore paths share one
content-addressed segment contract.

1. `SegmentCodec` hashes a canonical logical header containing repository,
   source-generation, Slot-generation, stream, sequence, record-count,
   predecessor/checkpoint link, source watermark, and plaintext evidence. That
   digest is the stable Segment ID. The signed predecessor envelope lets
   restore admission walk the complete graph without payload download or KMS.
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
   `VerifyEnvelopeCopies` authenticates the exact current proof and payload
   provider metadata in both repositories without opening payload bytes, and
   returns the signed predecessor link.
5. `InspectSegmentCopies` independently GETs each commit and payload, verifies
   stored checksums and the signed proof, decrypts/decompresses the payload, and
   verifies the plaintext digest under the store's bounded memory semaphore.
   It classifies missing, checksum, ciphertext, and commit-proof corruption.
   `RepairSegmentCopy` copies only damaged commit/payload objects from the
   authenticated healthy peer and then repeats full validation. A repository
   exposes replacement/version publication only through the narrower
   `RepairRepository` capability; ordinary `PutImmutable` remains create-only.
   Independently decoded headers and predecessor references are compared by
   value; JSON pointer allocation identity is never corruption evidence.
   `NewReplicatedSegmentStoreWithRepair` requires both repair capabilities
   explicitly; the ordinary constructor cannot repair existing repository
   keys. Inspection also extracts the authenticated predecessor carried by
   continuous portable plaintext so the catalog auditor can resume a segment
   chain without another full object read. The same explicit repair boundary
   validates materialized partition manifests and each encrypted payload with
   full KMS decrypt and plaintext-digest checks, then repairs only the damaged
   graph node from its authenticated peer.
   Authenticated partition manifests are reused only within one explicit audit
   cycle through a 64 MiB byte-bounded cache; a new cycle or repaired manifest
   invalidates the relevant entries. The manifest object list is copied once
   when cached and remains internal and immutable; each audit result exposes
   only the format, Hash Slot, object count, and predecessor needed for
   navigation, avoiding work proportional to the full list on every object.
   Payload validation accounts for the
   ciphertext buffer, decrypted compressed bytes, plaintext, and decoder
   workspace under the same store semaphore. Repair reopens the healthy
   immutable source as a stream instead of retaining ciphertext after releasing
   that budget.
6. If either repository already has a valid signed commit, a retry verifies
   that its logical header matches the requested plaintext, repairs the missing
   payload and commit copy from the healthy repository, and does not request a
   new KMS data key.
7. Segment commit decoding is strict and bounded. Unknown fields, trailing
   data, unsupported format/version values, unsafe identities, invalid sizes,
   checksum mismatches, and invalid signatures fail closed.
8. Seal and open reuse compressed/ciphertext backing storage where safe.
   Segment operations reserve three times logical payload size plus 16 MiB.
   Partition audit reserves twice ciphertext plus twice plaintext plus 16 MiB,
   with ciphertext capped at the portable 272 MiB bound. One replicated store
   therefore exposes a 1,072 MiB semaphore capacity: enough for one legal
   worst-case partition audit while still admitting several smaller segment
   operations. The capture runtime applies its smaller rolling target and
   node-level concurrency budget before materializing plaintext and calling
   this boundary.
9. `SegmentBatch` is the strict portable plaintext inside continuous metadata
   and message segments. It binds the source page cursor interval, observed
   high watermark, prior committed Segment reference, ordered record frames,
   and independent record/cursor checksums. Message batches embed a bounded
   sorted `ChannelBoundary` index; metadata batches reject that index. The
   cursor list therefore remains in immutable repository artifacts instead of
   bounded Controller coordination state.
10. Each metadata record inside a metadata `SegmentBatch` uses the strict binary
   `MetadataLogRecord` envelope. It preserves the logical Hash Slot, physical
   Slot Raft index/term, proposer timestamp, and exact FSM command bytes needed
   for ordered replay; malformed lengths, empty commands, and trailing bytes
   fail closed.
11. `MessageLogRecord` is the strict portable committed Channel row. It carries
    Channel identity/epoch/retention cut plus durable message sequence, message
    ID, idempotency/display fields, flags, timestamp, and payload. A distinct
    boundary-only kind represents epoch or retention movement without
    fabricating a message.
12. `MessageCursorBatch` is a cursor-only immutable sidecar stored under the
    `message_cursor` stream. Ordinary batches contain sorted cursor deltas and
    link only to the previous cursor sidecar, never to payload segments. A full
    checkpoint has no predecessor and bounds chain reconstruction to 1024
    sidecars.
13. `Checkpoint` freezes exactly one sorted frontier for every configured Hash
    Slot. Empty but fully reconciled streams are explicit zero-sequence heads,
    and the checkpoint effective time is the oldest Slot watermark. Repository,
    source cluster, source generation, Slot generation, stream, and sequence
    bind its current references to the authenticated segment commit headers.
14. Each signed `CatalogPage` authenticates its checkpoint references and the
    exact previous page reference. Checkpoints use deterministic immutable keys;
    catalog pages use sequence-plus-checkpoint keys so an unpublished orphan
    cannot collide with a later retry. The Controller stores only the newest
    page reference and never stores catalog history. Each checkpoint reference
    points to a signed content-addressed `GenerationVector` rather than copying
    all Slot Generation strings into every historical index row. Identical
    vectors are reused across checkpoints, while vector ID, representation
    checksum, byte size, and Slot count remain authenticated by the catalog
    page.
15. A materialized Slot rebase uses the single-layer partition-manifest
    schema. It binds the target Generation and rebase epoch plus one committed
    `message_baseline_cursor`
    `SegmentReference`, containing a checkpoint-form `MessageCursorBatch` with
    the complete Channel index plus generation, source cut, watermark, and
    predecessor-termination proof. An empty Slot uses the same explicit
    checkpoint envelope with an empty index. Its cut records the physical Slot
    ID that owns the Raft
    index space, so a retry after routing remap cannot reuse an old baseline.
    Checkpoint version 3 optionally binds the materialized partition and cursor
    proof beside the current capture heads, and freezes the current sorted
    permanent-erasure stream heads.
16. Restore pins the exact catalog head, containing page and checkpoint
    reference, resolves membership through both repository copies, and audits
    the complete selected graph before a plan is admitted. The selected
    repository copy is used only for the later Leader download.
    `RestoreEvidenceAccumulator` validates the
    chronological portable record stream once while computing exact typed
    counts, Channel boundaries, a domain-separated content digest, and an
    order-sensitive message Merkle root without retaining message payloads.
17. Retained-graph traversal authenticates a baseline cursor's signed segment
    commit and marks both the commit and encrypted payload key, so Generation
    GC cannot delete a live cursor representation.
18. Delete-capable repositories map provider Object Lock rejection to
    `ErrObjectLocked`. Generation GC treats it as deferred work for that
    repository, not as permission to advance past the protected version.

## Restore Activation Evidence

`SourceFenceRecord` is the portable immutable binding from one source cluster
generation to one exact restore plan, checkpoint digest, and successor
generation. A receipt is valid only after the record carries the Controller
revision observed by all active data nodes and a convergence time. Signing and
verification use canonical bytes and the same injected `ManifestSigner`
boundary as other KMS-backed artifacts; changing any binding invalidates the
signature.

`RestoreActivationEvidence` is exactly one of a verified source-fence receipt
or an explicit break-glass audit. Break glass binds a generated audit ID,
operator, reason, plan, and authorization time into a deterministic digest.
The evidence is immutable across `activating` retries and contains no repository
credential or plaintext staging path.
