# Raftlog External Chunk Snapshot Store Design

**Date:** 2026-05-08
**Status:** Design approved

## Overview

Move Raft snapshot payload storage out of Pebble for the scoped `pkg/raftlog` storage.

The current `raftlog` implementation writes a full `raftpb.Snapshot` protobuf into the scoped Pebble DB. This keeps the first implementation simple, but it makes large state-machine snapshots large Pebble values and couples snapshot payload IO to the Raft log LSM tree.

The approved design changes the invariant:

```text
New Raft Snapshot.Data is never written to Pebble.
Pebble stores only Raft/log metadata and a snapshot manifest.
Snapshot payload bytes are stored only in external chunk files.
```

This design applies to every durable `pkg/raftlog` scope, including Slot Raft logs and Controller Raft logs. `pkg/slot/multiraft` and controller Raft code should continue to use the existing `Storage` abstraction; the storage implementation owns manifest and chunk details.

## Goals

- Store all newly generated `raftpb.Snapshot.Data` outside Pebble, regardless of snapshot size.
- Store external snapshot data as chunk files with per-chunk and whole-snapshot checksums.
- Keep Pebble as the authoritative source for snapshot metadata and log metadata.
- Preserve Raft storage semantics:
  - `Snapshot()` returns the latest snapshot.
  - `FirstIndex()` reflects entries trimmed by the snapshot.
  - `Term(snapshotIndex)` returns the snapshot term.
  - recovery restores the snapshot and replays post-snapshot entries.
- Keep `multiraft.Storage` unchanged in the first implementation phase.
- Avoid old inline snapshot compatibility because the system has not shipped with the inline snapshot format.
- Add safe crash recovery and garbage collection for temporary and orphan snapshot chunk directories.

## Non-Goals

- End-to-end streaming snapshots in the first phase.
- Incremental snapshots.
- Object storage as the primary snapshot store.
- Keeping multiple historical snapshots as a supported recovery feature.
- Preserving old inline snapshot data stored in Pebble.
- Changing state-machine snapshot payload formats in `pkg/slot/meta` or `pkg/controller/meta`.

## Current State

`pkg/slot/multiraft` builds a Raft snapshot after applying entries:

```text
stateMachine.Snapshot()
  -> raftpb.Snapshot{Metadata: Index/Term/ConfState, Data: state bytes}
  -> Storage.Save(PersistentState{Snapshot: &snapshot})
```

`pkg/raftlog` currently persists that snapshot through `pebbleStore.Save` by marshaling the full `raftpb.Snapshot` into the scoped snapshot key. The same write path also deletes entries covered by the snapshot and updates scoped log metadata.

This is Raft-correct, but it stores the state-machine snapshot payload in Pebble. The new storage design keeps the Raft correctness properties while moving the payload bytes into external chunk files.

## Storage Layout

Use a separate snapshot directory for each durable `raftlog.DB`.

This repository currently opens Slot Raft logs and Controller Raft logs as separate Pebble DBs. Each DB must own exactly one external snapshot root and must only garbage collect directories under that root. Sharing one snapshot root across independent raftlog DBs is not allowed because one DB cannot see manifests stored in the other DB and could delete live chunks as unreferenced.

Default layout:

```text
<WK_NODE_DATA_DIR>/
  raft/                         # scoped raftlog Pebble DB
  raft-snapshots/               # snapshot chunks owned by the Slot raftlog DB
    slot-9/
      snap-0000000000001234-0000000000000007-9f3a1c2d4b5e6f70/
        chunk-000000
        chunk-000001
        chunk-000002
  controller-raft/               # Controller raftlog Pebble DB
  controller-raft-snapshots/     # snapshot chunks owned by the Controller raftlog DB
    controller-1/
      snap-0000000000000456-0000000000000003-27d4e900a1b2c3d4/
        chunk-000000
```

Path naming rules:

- Scope directory is derived from `raftlog.Scope`:
  - `slot-<slotID>` for Slot scopes.
  - `controller-<controllerID>` for Controller scopes.
- Snapshot directory name is unique and human-readable:
  - `snap-%016x-%016x-<nonce>` using snapshot index, term, and a random nonce.
- Temporary directories include a nonce:
  - `.tmp-snap-%016x-%016x-<nonce>`.
- Chunk files use fixed-width sequence numbers:
  - `chunk-000000`, `chunk-000001`, ...

The chunk store stores only snapshot payload bytes. Raft metadata remains in Pebble. The nonce avoids retry collisions when a previous attempt created a final directory but failed before the Pebble manifest commit.

Nonce rules:

- Generate the nonce with `crypto/rand`.
- Encode it as exactly 16 lowercase hex characters (64 bits).
- Validate `SnapshotID` against `^snap-[0-9a-f]{16}-[0-9a-f]{16}-[0-9a-f]{16}$`.

## Snapshot Manifest

Pebble `recordTypeSnapshot` keeps the same scoped key role but changes value semantics from full `raftpb.Snapshot` to a custom manifest. Because the inline format has not shipped, no compatibility reader is required.

Proposed manifest fields:

```go
type SnapshotManifest struct {
    Version uint16

    ScopeKind uint8
    ScopeID   uint64

    Index uint64
    Term  uint64

    ConfState raftpb.ConfState

    SnapshotID string

    ChunkSize  uint64
    ChunkCount uint32
    TotalSize  uint64

    ChecksumType string
    WholeChecksum []byte
    ChunkChecksums [][]byte

    CreatedAtUnixNano int64
}
```

Manifest requirements:

- `Version` supports future format changes.
- `ScopeKind` and `ScopeID` prevent using a manifest with the wrong scope.
- `Index`, `Term`, and `ConfState` rebuild `raftpb.Snapshot.Metadata`.
- `SnapshotID` selects the external chunk directory. It must be a generated basename, not a caller-controlled path, and it must not contain path separators.
- `ChunkSize`, `ChunkCount`, and `TotalSize` validate chunk shape.
- `WholeChecksum` validates the assembled payload.
- `ChunkChecksums` identify corrupt chunks.
- `ChecksumType` is `crc32c` in the first version. Use the Castagnoli polynomial and encode each checksum as a fixed 4-byte big-endian value.

Manifest shape validation must be strict:

- `Index` and `Term` must be non-zero for any persisted manifest.
- `SnapshotID` must match the generated `snap-*` basename regex and must not be `.`, `..`, absolute, or contain a path separator.
- `ChunkSize` must be non-zero.
- `ChunkCount` must equal `ceil(TotalSize / ChunkSize)`. For `TotalSize=0`, `ChunkCount` must be zero. The computed chunk count must fit in `uint32`; otherwise the manifest is malformed.
- `len(ChunkChecksums)` must equal `ChunkCount`.
- `WholeChecksum` and every chunk checksum must be exactly 4 bytes for `crc32c`.
- Phase one must reject manifests whose `TotalSize` or chunk offset arithmetic cannot fit in Go `int` or overflows `uint64`; it must not allocate memory only because a manifest declares a large size.
- Every non-final chunk must be exactly `ChunkSize` bytes, and the final chunk must match the remaining payload size.
- A zero-length payload uses `crc32c(empty)` as `WholeChecksum` and has no chunk files.

The manifest is the only authoritative snapshot descriptor. A debug JSON file may be useful during development, but recovery must not depend on any file-system manifest copy.

## Write Protocol

The write protocol must guarantee that a visible Pebble manifest never points to incomplete chunks.

For `Save(PersistentState{Snapshot: &snap})`:

```text
1. Acquire the per-scope mutation gate. `Save` and `MarkApplied` for the same scope use this gate from method entry until their Pebble commit finishes.
2. Read current manifest/meta with metadata-only helpers and reject stale snapshots before chunk IO when possible.
3. Construct a manifest whose scope matches `pebbleStore.scope`.
4. Generate snapshot ID and tmp directory, then register the tmp directory as in-flight before creating files under it.
5. Split snap.Data into fixed-size chunks.
6. Write chunk files under the tmp directory.
7. fsync every chunk file.
8. fsync the tmp directory.
9. Acquire the DB snapshot lifecycle lock shared with GC.
10. Rename tmp directory to final snapshot directory.
11. fsync the scope snapshot parent directory.
12. Submit a Pebble write request that atomically:
    - revalidates stale/idempotent snapshot rules against the serialized write state,
    - writes the snapshot manifest,
    - updates log metadata,
    - updates hard state commit if needed,
    - deletes entries <= snapshot index,
    - persists only entries whose index is greater than the snapshot index when `Snapshot` and `Entries` are in the same `PersistentState`.
13. Unregister the in-flight tmp/final path.
14. Release the DB snapshot lifecycle lock and per-scope mutation gate.
15. Signal asynchronous GC.
```

Important ordering rule:

```text
External chunks become durable before Pebble manifest commit.
```

Crash behavior:

- Crash before Pebble commit leaves only tmp or unreferenced final directories; GC removes them.
- Crash after Pebble commit leaves a manifest pointing to already durable chunks; recovery succeeds.
- Pebble batch atomicity prevents a visible manifest without matching log metadata or a trimmed log without a visible snapshot manifest.

If chunk writing fails, do not submit the Pebble write request. Delete the tmp directory best-effort and return the error.

If the Pebble batch fails after the final snapshot directory was created, return the error. The unreferenced final directory is safe to delete by GC.

Snapshot freshness rules:

- `snapshot.Metadata.Index < current SnapshotIndex` must return `raft.ErrSnapOutOfDate` or an equivalent stale-snapshot error and must not mutate Pebble.
- `snapshot.Metadata.Index == current SnapshotIndex` is idempotent only when term, conf state, payload size, and whole checksum match the existing manifest. In that case, skip replacing the manifest/chunks but still persist any valid hard state and post-snapshot entries in the same request.
- `snapshot.Metadata.Index == current SnapshotIndex` with different term, conf state, size, or checksum is storage corruption or a conflicting caller bug and must fail.
- `snapshot.Metadata.Index > current SnapshotIndex` is the normal compaction path.
- If the write worker rejects a stale or conflicting snapshot after chunks were already written, the external directory is an orphan and must be left for GC; it must never be attached to Pebble.

Same-request `Snapshot` and `Entries` rules:

- Compute the effective snapshot boundary before writing entries.
- Drop the compacted prefix `entry.Index <= snapshot.Metadata.Index` from the entries being persisted.
- Persist contiguous post-snapshot entries normally.
- Compute `logMeta` from the snapshot manifest plus the retained post-snapshot entries.
- A test must cover a request containing both a snapshot and overlapping entries.

Final directory collision rules:

- Snapshot IDs include a random nonce; collision should be practically impossible.
- If the generated final directory already exists before rename, generate a new snapshot ID and retry the directory creation path.
- If a manifest already references the same snapshot ID, treat the directory as live and never overwrite it.
- If an unreferenced final directory from an earlier failed write exists for the same index and term, leave it for GC; do not reuse or overwrite it in the normal path.
- `PublishFinal` must use no-overwrite semantics. A rename that would replace or merge with an existing final directory is forbidden and must be handled as a collision.
- Tests must cover retry before GC when a previous Pebble commit failed after final directory creation.

## Write Path Placement

Keep large file IO outside the serialized raftlog write worker.

Recommended first-phase flow:

```go
func (s *pebbleStore) Save(ctx context.Context, st multiraft.PersistentState) error {
    unlock := s.db.lockScopeMutation(s.scope)
    defer unlock()

    var staged *stagedSnapshot
    var manifest *SnapshotManifest
    if st.Snapshot != nil {
        plan, err := s.db.planSnapshotSave(ctx, s.scope, *st.Snapshot)
        if err != nil {
            return err
        }
        manifest = plan.Manifest
        if plan.NeedsExternalWrite {
            staged, err = s.db.snapshotStore.Stage(ctx, s.scope, *st.Snapshot, *manifest)
            if err != nil {
                return err
            }
            defer staged.CleanupTmpBestEffort()
        }
    }

    if staged != nil {
        s.db.snapshotLifecycleMu.Lock()
        defer s.db.snapshotLifecycleMu.Unlock()
        if err := staged.PublishFinal(ctx); err != nil {
            return err
        }
    }

    reqState := st.WithoutSnapshotData()
    err := s.db.submitWrite(&writeRequest{
        scope: s.scope,
        op: saveOp{
            state: reqState,
            snapshotManifest: manifest,
        },
        done: make(chan error, 1),
    })
    if err != nil {
        return err
    }
    return nil
}
```

The per-scope mutation gate preserves call ordering for one Raft group without moving chunk file IO into the global write worker. The write worker remains responsible for Pebble atomicity only. It must never marshal `Snapshot.Data` into Pebble.

The write request must be metadata-only for snapshots. Before submitting to the write worker, copy the request state so `saveOp` carries only hard state, retained entries, `raftpb.SnapshotMetadata`, and the manifest. The copied request must not contain the snapshot payload byte slice. This makes the "never write Snapshot.Data to Pebble" invariant structural instead of relying on every worker code path to remember not to marshal it.

`MarkApplied` and entry-only `Save` must also acquire the same per-scope mutation gate. This prevents a later entry-only write from overtaking a slow snapshot write for the same scope.

The DB snapshot lifecycle lock is separate from the per-scope mutation gate. It is held only around final directory rename, parent-directory fsync, and the Pebble batch commit. GC must take the same lifecycle lock for its manifest scan and delete pass, so GC cannot observe a stale manifest set while a final snapshot directory is being attached to Pebble.

## Read Protocol

`Storage.Snapshot(ctx)` reconstructs a standard `raftpb.Snapshot` from manifest plus external chunks:

```text
1. Read scoped snapshot manifest from Pebble.
2. If no manifest exists and metadata has no snapshot boundary, return an empty snapshot.
3. If metadata records a snapshot boundary but the manifest is missing or mismatched, return storage corruption.
4. Validate manifest scope, format version, and shape.
5. Read chunk files in order.
6. Validate each chunk checksum.
7. Validate total size and whole checksum.
8. Return raftpb.Snapshot{
     Metadata: {Index, Term, ConfState},
     Data: assembled chunks,
   }.
```

If the manifest exists but any referenced chunk is missing or corrupt, return an error. Do not return an empty snapshot and do not fall back to log replay, because entries covered by the snapshot may already be compacted.

First phase still assembles `Snapshot.Data` into memory to satisfy the current `Storage` and state-machine APIs. That is acceptable because the design goal for phase one is to remove snapshot payloads from Pebble, not to make restore fully streaming.

## Other Storage Operations

### Manifest-only metadata helpers

Ordinary metadata paths must never reconstruct snapshot payloads.

Add or refactor internal helpers so these paths read only the manifest:

```text
loadSnapshotManifest(ctx, scope)
currentMeta(ctx)
loadScopeWriteState(ctx)
Term(ctx, index)
InitialState(ctx)
FirstIndex(ctx)
LastIndex(ctx)
```

Only the public `Snapshot(ctx)` path and startup restore path should assemble chunk payload bytes. Entry-only `Save` calls and `MarkApplied` must not fail merely because snapshot chunks are missing; chunk corruption is only observed when code actually needs `Snapshot.Data`.

Existing metadata-only callers must stop using `Storage.Snapshot(ctx)` when they only need index/term:

- `pkg/slot/multiraft.compactLogManually` should derive the current snapshot boundary from `FirstIndex()` and, only when `firstIndex > 1`, `Term(firstIndex-1)`.
- `pkg/controller/raft.Service.compactControllerLogManually` should do the same.
- `pkg/cluster.controller_raft_status` should report snapshot index/term from `FirstIndex()` and, only when `firstIndex > 1`, `Term(firstIndex-1)` instead of assembling snapshot payload bytes.

These changes keep `multiraft.Storage` unchanged while avoiding large chunk reads and avoiding false status/compaction failures when payload chunks are corrupt but metadata is still readable.

Manifest/meta consistency rules:

- The scoped `logMeta` key is mandatory once a snapshot manifest exists.
- If a snapshot manifest exists but scoped `logMeta` is absent, return storage corruption. Do not reconstruct it silently.
- Only the empty-bootstrap state may have both manifest and `logMeta` absent.
- Once scoped `logMeta` exists, `meta.SnapshotIndex > 0` requires a manifest with matching index, term, and conf state.
- A manifest with no matching `logMeta` snapshot boundary is storage corruption.
- Metadata-only helpers must return corruption for manifest/meta mismatches; they must not silently treat the group as having no snapshot.

### `Term(index)`

`Term(snapshotIndex)` must read the term from the manifest without reading chunks. This avoids loading snapshot payload data for normal Raft term lookups.

### `InitialState()`

Initial state should derive the latest committed `ConfState` from:

1. snapshot manifest `ConfState`, if present;
2. committed post-snapshot config-change entries;
3. empty conf state when no snapshot or committed config entries exist.

It should not read chunk files.

### `FirstIndex()` and `LastIndex()`

These methods should use scoped log metadata. They should not read chunk files.

After snapshot compaction:

```text
FirstIndex = snapshotIndex + 1 when no post-snapshot entries begin earlier
LastIndex  = max(snapshotIndex, last persisted entry index)
```

### `loadSnapshot` internals

Internal snapshot loading should be context-aware because external file reads may be large. Replace the old full-snapshot helper split with:

```text
loadSnapshotManifest(ctx)  # metadata only
loadSnapshot(ctx)          # manifest + chunk payload, used only by public Snapshot/restore
```

## Package Design

### `pkg/raftlog`

Add files:

```text
snapshot_manifest.go
snapshot_codec.go
snapshot_store.go
snapshot_gc.go
```

Responsibilities:

- manifest data model and validation;
- binary manifest encode/decode;
- chunk store write/read/checksum/path operations;
- snapshot GC;
- storage integration in `pebbleStore.Save`, `Snapshot`, `Term`, and metadata loading.

Add DB-level coordination fields:

```go
type DB struct {
    // ...
    scopeMutationLocksMu sync.Mutex
    scopeMutationLocks map[Scope]*sync.Mutex
    snapshotLifecycleMu sync.Mutex
    activeSnapshotPaths map[string]struct{}
    gcCtx context.Context
    gcCancel context.CancelFunc
    gcWG sync.WaitGroup
}
```

`scopeMutationLocks` preserves per-scope mutation order across chunk staging and Pebble commit. `scopeMutationLocksMu` or an equivalent `sync.Map` must make lock lookup and first creation race-free.

`activeSnapshotPaths` records tmp and final directories that belong to in-flight snapshot saves. It must be updated under `snapshotLifecycleMu`, and GC must skip active paths even when their mtime is older than the grace window.

Once a tmp path is registered as active, unregister it with `defer` on every success and error path so failed saves do not leak active entries and block future GC.

`snapshotLifecycleMu` serializes GC with final snapshot directory publication and manifest commit. `gcCtx`/`gcWG` make asynchronous GC a managed DB lifecycle task.

Update in-memory write state:

```go
type scopeWriteState struct {
    hardState raftpb.HardState
    snapshotManifest SnapshotManifest
    hasSnapshot bool
    entries []raftpb.Entry
    meta logMeta
}
```

`logMeta` can continue storing:

```text
FirstIndex
LastIndex
AppliedIndex
SnapshotIndex
SnapshotTerm
ConfState
```

### `pkg/slot/multiraft`

No public storage interface change in phase one.

`multiraft` keeps building standard `raftpb.Snapshot` values and calling `Storage.Save`. It does not know whether the durable storage uses chunks.

### `pkg/slot/fsm` and `pkg/slot/meta`

No snapshot payload format change in phase one.

`StateMachine.Snapshot` may continue returning `[]byte` payloads. Future streaming work can introduce a separate design.

### `pkg/controller/raft`

No storage API change in phase one. Controller snapshots also use external chunk storage because the durable `pkg/raftlog` implementation applies to all scopes.

### `pkg/cluster` and `internal/app`

`raftlog.Open` must take explicit options:

```go
type Options struct {
    SnapshotPath string
    SnapshotChunkSize uint64
    SnapshotGCGrace time.Duration
}

func Open(path string, opts Options) (*DB, error)
```

`Options` applies defaults and validation inside `pkg/raftlog` for package-level tests, while app config remains the user-facing source of defaults.

`Options{}` behavior:

- `SnapshotPath == ""` derives an external sibling root from the Pebble DB path, equivalent to `<raftlog path>-snapshots`.
- `SnapshotChunkSize == 0` uses the package default of 8 MiB.
- `SnapshotGCGrace == 0` is valid and means immediate orphan eligibility; app-level config should still default to 30 minutes.
- Negative `SnapshotGCGrace` is invalid.
- Empty options must never select inline Pebble snapshot payload storage.

Plumbing requirements:

- `internal/app/build.go` opens the Slot raftlog DB with `Options{SnapshotPath: storage.RaftSnapshotPath, SnapshotChunkSize: storage.RaftSnapshotChunkSize, SnapshotGCGrace: storage.RaftSnapshotGCGrace}`.
- `internal/app.ClusterConfig.runtimeConfig` passes Controller snapshot storage settings into `pkg/cluster.Config`.
- `pkg/cluster.Config` carries `ControllerRaftSnapshotPath`, `RaftSnapshotChunkSize`, and `RaftSnapshotGCGrace`.
- `pkg/cluster/controller_host.go` opens the Controller raftlog DB with `Options{SnapshotPath: cfg.ControllerRaftSnapshotPath, SnapshotChunkSize: cfg.RaftSnapshotChunkSize, SnapshotGCGrace: cfg.RaftSnapshotGCGrace}`.
- Tests must verify Slot and Controller raftlog DBs receive distinct snapshot roots by default and receive the configured chunk size and GC grace values.

## Configuration

Add storage-level configuration:

```conf
# Directory for external Slot Raft snapshot chunk files.
WK_STORAGE_RAFT_SNAPSHOT_PATH=./raft-snapshots

# Directory for external Controller Raft snapshot chunk files.
WK_STORAGE_CONTROLLER_RAFT_SNAPSHOT_PATH=./controller-raft-snapshots

# Size of each external Raft snapshot chunk.
WK_STORAGE_RAFT_SNAPSHOT_CHUNK_SIZE=8MiB

# Grace period before orphan snapshot directories can be deleted.
WK_STORAGE_RAFT_SNAPSHOT_GC_GRACE=30m
```

Recommended defaults in `internal/app.Config`:

- `RaftSnapshotPath = filepath.Join(Node.DataDir, "raft-snapshots")`
- `ControllerRaftSnapshotPath = filepath.Join(Node.DataDir, "controller-raft-snapshots")`
- `RaftSnapshotChunkSize = 8 MiB`
- `RaftSnapshotGCGrace = 30 minutes`

Validation rules:

- `RaftSnapshotPath` and `ControllerRaftSnapshotPath` must be non-empty after defaults.
- Snapshot paths must not equal, contain, or be contained by `DBPath`, `RaftPath`, `ControllerMetaPath`, `ControllerRaftPath`, `ChannelLogPath`, or each other after normalization.
- `RaftSnapshotChunkSize` must be greater than zero.
- `RaftSnapshotGCGrace` must be non-negative.
- `WK_STORAGE_RAFT_SNAPSHOT_CHUNK_SIZE` accepts either a decimal byte count or binary-size units: `B`, `KiB`, `MiB`, and `GiB`. Invalid units are rejected.

Normalize paths with `filepath.Abs` and `filepath.Clean`, then compare by path segment boundaries rather than string prefixes. When an existing path can be resolved, use `filepath.EvalSymlinks` for collision validation so a symlink cannot make the snapshot root overlap a Pebble DB or channel log path.

The snapshot path is a storage implementation parameter, not a Raft compaction policy. Keep it in `StorageConfig` rather than the log compaction config.

Because these are user-facing config keys, update `wukongim.conf.example` in the implementation and keep its comments aligned with the defaults and units above.

## Garbage Collection

GC deletes only unreferenced data under the current `raftlog.DB` snapshot root. The current Pebble manifests in that same DB are authoritative for that root.

Trigger points:

- once after `raftlog.Open`;
- asynchronously after successful snapshot save.

GC algorithm:

```text
1. Acquire the DB snapshot lifecycle lock.
2. Scan Pebble for all scoped snapshot manifest keys.
3. Build a referenced set of scope/snapshot ID directories.
4. Scan only the snapshot root owned by this DB.
5. Snapshot the active in-flight tmp/final path set.
6. Delete tmp directories older than GC grace unless they are active.
7. Delete final snap-* directories that are not referenced, not active, and older than GC grace.
8. Keep referenced directories even if they are older than grace.
9. Release the DB snapshot lifecycle lock.
```

GC must be best-effort. Failure to delete orphan data should be logged or returned to test hooks, but must not invalidate the current Raft storage state.

GC must not scan a shared parent and infer ownership from directory names. Cross-DB deletion is forbidden. A test must open independent Slot and Controller raftlog DBs with separate snapshot roots and verify each GC leaves the other root untouched.

GC lifecycle rules:

- `raftlog.Open` may start a best-effort GC pass after the DB and manifest reader are ready.
- Asynchronous GC must be tracked by `DB` with a context and wait group.
- `DB.Close` must stop accepting new writes, cancel pending GC, wait for the write worker and GC workers to exit, and only then close Pebble.
- GC must not hold a Pebble iterator or delete files after `DB.Close` closes the underlying Pebble DB.
- A test must cover closing the DB while GC is running.

## Failure Semantics

- Missing manifest means no snapshot only when scoped log metadata also has no snapshot boundary.
- Missing manifest with `logMeta.SnapshotIndex > 0` is storage corruption.
- Manifest/logMeta index, term, or conf state mismatch is storage corruption.
- Malformed manifest is storage corruption and should return an error.
- Manifest scope mismatch is storage corruption and should return an error.
- Missing chunk for an existing manifest is storage corruption and should return an error.
- Chunk checksum mismatch is storage corruption and should return an error.
- Whole checksum mismatch is storage corruption and should return an error.
- Old inline `raftpb.Snapshot` values in Pebble are unsupported. Because the format has not shipped, they may be treated as malformed manifests.
- Zero-length `Snapshot.Data` is valid. It creates a manifest with `TotalSize=0`, `ChunkCount=0`, and no chunk files, while still preserving snapshot metadata.
- Invalid snapshot IDs, path traversal attempts, chunk-count mismatches, and checksum-count mismatches are storage corruption and should return errors.

## Testing Strategy

### Raftlog tests

Required tests:

```text
TestPebbleSaveSnapshotWritesExternalChunksOnly
TestPebbleSnapshotRoundTripFromExternalChunks
TestPebbleTermAtSnapshotIndexUsesManifest
TestPebbleSaveSnapshotTrimsCoveredEntriesWithManifest
TestPebbleInitialStateUsesSnapshotManifestConfState
TestPebbleEntryOnlySaveDoesNotReadSnapshotChunks
TestPebbleMarkAppliedDoesNotReadSnapshotChunks
TestPebbleSaveSnapshotWorkerRequestDoesNotCarrySnapshotData
TestPebbleRejectsStaleSnapshotWithoutMutatingState
TestPebbleSameIndexSnapshotIsIdempotentOnlyWhenManifestMatches
TestPebbleSnapshotAndEntriesSameSaveDropsCompactedEntryPrefix
TestPebbleManifestAndMetaMismatchIsCorruption
TestPebbleOpenOptionsDefaultExternalSnapshotRoot
```

Assertions:

- snapshot payload bytes do not appear in the Pebble snapshot value;
- manifest decodes from the scoped snapshot key;
- chunk files contain the payload bytes;
- `Snapshot()` reconstructs the original `raftpb.Snapshot`;
- `FirstIndex()` advances after compaction;
- `Term(snapshotIndex)` works when chunks are unreadable only if the manifest is intact.

### Chunk store tests

Required tests:

```text
TestSnapshotStoreWriteSplitsChunks
TestSnapshotStoreReadReassemblesChunks
TestSnapshotStoreReadRejectsMissingChunk
TestSnapshotStoreReadRejectsCorruptChunk
TestSnapshotStoreWriteUsesTmpThenFinalDirectory
TestSnapshotStoreWriteHandlesFinalDirCollisionByRegeneratingID
TestSnapshotStoreReadRejectsPathTraversalSnapshotID
TestSnapshotStoreAllowsZeroLengthPayload
TestSnapshotStoreUsesCRC32CCastagnoliBigEndian
TestSnapshotStoreRejectsMalformedShapeAndOverflowingSizes
TestSnapshotStoreRejectsSnapshotIDWithInvalidNonce
TestSnapshotStorePublishFinalDoesNotOverwriteExistingDirectory
```

### Crash and GC tests

Required tests:

```text
TestSnapshotGCRemovesTmpDirsWithoutManifest
TestSnapshotGCRemovesUnreferencedFinalDirs
TestSnapshotGCKeepsManifestReferencedSnapshot
TestSnapshotManifestWithMissingChunkFailsSnapshotRead
TestSnapshotSaveFailureDoesNotWriteManifestOrTrimEntries
TestSnapshotPebbleCommitFailureLeavesRetryableOrphanDir
TestSnapshotRetryBeforeGCSucceedsAfterCommitFailure
TestSnapshotGCDoesNotCrossIndependentRaftLogDBRoots
TestSnapshotGCSkipsInFlightSnapshotFinalization
TestSnapshotGCSkipsInFlightTmpStaging
TestSnapshotCloseWaitsForRunningGC
```

Use zero or very small GC grace in tests to avoid flaky sleeps. Prefer controlling file modification times directly where possible.

### Multiraft integration tests

Existing compaction behavior tests should continue to pass:

```text
TestRuntimeLogCompactionSnapshotsAndCompactsAppliedEntries
TestOpenSlotRestoresSnapshotThenReplaysPostSnapshotEntries
TestRuntimeCompactedLeaderSendsSnapshotToNewLearner
```

Add at least one real-storage integration test:

```text
TestRuntimeCompactionWithPebbleExternalSnapshotRestoresAfterRestart
TestRuntimeConcurrentSnapshotSavePreservesPerScopeWriteOrder
```

Flow:

1. open real `raftlog` DB with a snapshot path;
2. run Slot Raft compaction;
3. close runtime and DB;
4. reopen;
5. restore snapshot and replay post-snapshot entries;
6. verify snapshot payload was stored only in chunk files.

Add metadata-only behavior tests:

```text
TestRuntimeManualCompactionReadsSnapshotMetadataWithoutPayload
TestControllerManualCompactionReadsSnapshotMetadataWithoutPayload
TestControllerRaftStatusReadsSnapshotMetadataWithoutPayload
```

### Config tests

Add tests in `internal/app` and `cmd/wukongim` for:

- default snapshot path;
- default controller snapshot path;
- configured snapshot path;
- configured controller snapshot path;
- configured chunk size;
- byte-size parser units and invalid units;
- configured GC grace;
- Slot and Controller `raftlog.Open` receive configured chunk size and GC grace;
- environment variable precedence;
- invalid zero chunk size;
- invalid negative GC grace;
- path collision validation, including ancestor/descendant overlaps and path segment boundaries;
- existing-path symlink collision validation where the platform supports symlinks;
- `wukongim.conf.example` includes the new snapshot keys with the documented units and defaults.

## Verification Targets

Minimum targeted verification before merging implementation:

```text
go test ./pkg/raftlog ./pkg/slot/multiraft ./pkg/cluster ./internal/app ./cmd/wukongim
```

If implementation touches Controller snapshot behavior, also run relevant Controller packages:

```text
go test ./pkg/controller/... ./internal/usecase/management ./internal/access/manager
```

Full repository unit tests remain the final pre-merge check:

```text
go test ./...
```

## Future Work

A later design can make snapshots end-to-end streaming:

- state machines write snapshots to `io.Writer`;
- state machines restore snapshots from `io.Reader`;
- raftlog reads chunk files without assembling a full `[]byte`;
- transport sends chunk files directly;
- restore streams chunk data into the state machine.

That future work is intentionally deferred. Phase one keeps the public storage API stable while enforcing the main invariant: snapshot payload bytes are never persisted in Pebble.
