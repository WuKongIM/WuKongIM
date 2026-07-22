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
   returning trusted metadata.
5. `ReplicatedPublisher.Publish` uploads and verifies every immutable object in
   both explicit repositories before it signs and writes the discoverable
   restore-point manifest. Failed copies leave only undiscoverable orphan
   objects for later garbage collection.
6. `LoadRestorePoint` verifies the repository copy of the signed manifest and
   proves that every referenced immutable object still has the expected size
   and ciphertext checksum before restore may consume it.
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
