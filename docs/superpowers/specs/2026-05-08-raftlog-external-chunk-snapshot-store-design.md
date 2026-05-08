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

Use a sibling snapshot directory next to the Raft Pebble DB by default:

```text
<WK_NODE_DATA_DIR>/
  raft/                         # scoped raftlog Pebble DB
  raft-snapshots/               # external snapshot chunk store
    slot-9/
      snap-0000000000001234-0000000000000007/
        chunk-000000
        chunk-000001
        chunk-000002
    controller-1/
      snap-0000000000000456-0000000000000003/
        chunk-000000
```

Path naming rules:

- Scope directory is derived from `raftlog.Scope`:
  - `slot-<slotID>` for Slot scopes.
  - `controller-<controllerID>` for Controller scopes.
- Snapshot directory name is deterministic for the committed snapshot boundary:
  - `snap-%016x-%016x` using snapshot index and term.
- Temporary directories include a nonce:
  - `.tmp-snap-%016x-%016x-<nonce>`.
- Chunk files use fixed-width sequence numbers:
  - `chunk-000000`, `chunk-000001`, ...

The chunk store stores only snapshot payload bytes. Raft metadata remains in Pebble.

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
- `SnapshotID` selects the external chunk directory.
- `ChunkSize`, `ChunkCount`, and `TotalSize` validate chunk shape.
- `WholeChecksum` validates the assembled payload.
- `ChunkChecksums` identify corrupt chunks.
- `ChecksumType` is `crc32c` in the first version.

The manifest is the only authoritative snapshot descriptor. A debug JSON file may be useful during development, but recovery must not depend on any file-system manifest copy.

## Write Protocol

The write protocol must guarantee that a visible Pebble manifest never points to incomplete chunks.

For `Save(PersistentState{Snapshot: &snap})`:

```text
1. Validate snapshot metadata and scope.
2. Generate snapshot ID and tmp directory.
3. Split snap.Data into fixed-size chunks.
4. Write chunk files under the tmp directory.
5. fsync every chunk file.
6. fsync the tmp directory.
7. Rename tmp directory to final snapshot directory.
8. fsync the scope snapshot parent directory.
9. Submit a Pebble write request that atomically:
   - writes the snapshot manifest,
   - updates log metadata,
   - updates hard state commit if needed,
   - deletes entries <= snapshot index.
10. Signal asynchronous GC.
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

## Write Path Placement

Keep large file IO outside the serialized raftlog write worker.

Recommended first-phase flow:

```go
func (s *pebbleStore) Save(ctx context.Context, st multiraft.PersistentState) error {
    var manifest *SnapshotManifest
    if st.Snapshot != nil {
        m, err := s.db.snapshotStore.Write(ctx, s.scope, *st.Snapshot)
        if err != nil {
            return err
        }
        manifest = &m
    }

    return s.db.submitWrite(&writeRequest{
        scope: s.scope,
        op: saveOp{
            state: st,
            snapshotManifest: manifest,
        },
        done: make(chan error, 1),
    })
}
```

The write worker remains responsible for Pebble atomicity only. It must never marshal `Snapshot.Data` into Pebble.

## Read Protocol

`Storage.Snapshot(ctx)` reconstructs a standard `raftpb.Snapshot` from manifest plus external chunks:

```text
1. Read scoped snapshot manifest from Pebble.
2. If no manifest exists, return an empty snapshot.
3. Validate manifest scope and format version.
4. Read chunk files in order.
5. Validate each chunk checksum.
6. Validate total size and whole checksum.
7. Return raftpb.Snapshot{
     Metadata: {Index, Term, ConfState},
     Data: assembled chunks,
   }.
```

If the manifest exists but any referenced chunk is missing or corrupt, return an error. Do not return an empty snapshot and do not fall back to log replay, because entries covered by the snapshot may already be compacted.

First phase still assembles `Snapshot.Data` into memory to satisfy the current `Storage` and state-machine APIs. That is acceptable because the design goal for phase one is to remove snapshot payloads from Pebble, not to make restore fully streaming.

## Other Storage Operations

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

Internal snapshot loading should be context-aware because external file reads may be large. If a small refactor is acceptable, change internal helpers from `loadSnapshot()` to `loadSnapshot(ctx)`.

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

## Configuration

Add storage-level configuration:

```conf
# Directory for external Raft snapshot chunk files.
WK_STORAGE_RAFT_SNAPSHOT_PATH=./raft-snapshots

# Size of each external Raft snapshot chunk.
WK_STORAGE_RAFT_SNAPSHOT_CHUNK_SIZE=8MiB

# Grace period before orphan snapshot directories can be deleted.
WK_STORAGE_RAFT_SNAPSHOT_GC_GRACE=30m
```

Recommended defaults in `internal/app.Config`:

- `RaftSnapshotPath = filepath.Join(Node.DataDir, "raft-snapshots")`
- `RaftSnapshotChunkSize = 8 MiB`
- `RaftSnapshotGCGrace = 30 minutes`

Validation rules:

- `RaftSnapshotPath` must be non-empty after defaults.
- `RaftSnapshotPath` must not equal the Pebble raft path, metadata DB path, controller metadata path, controller raft path, or channel log path.
- `RaftSnapshotChunkSize` must be greater than zero.
- `RaftSnapshotGCGrace` must be non-negative.

The snapshot path is a storage implementation parameter, not a Raft compaction policy. Keep it in `StorageConfig` rather than the log compaction config.

## Garbage Collection

GC deletes only unreferenced data. The current Pebble manifests are authoritative.

Trigger points:

- once after `raftlog.Open`;
- asynchronously after successful snapshot save.

GC algorithm:

```text
1. Scan Pebble for all scoped snapshot manifest keys.
2. Build a referenced set of scope/snapshot ID directories.
3. Scan the snapshot root.
4. Delete tmp directories older than GC grace.
5. Delete final snap-* directories that are not referenced and older than GC grace.
6. Keep referenced directories even if they are older than grace.
```

GC must be best-effort. Failure to delete orphan data should be logged or returned to test hooks, but must not invalidate the current Raft storage state.

## Failure Semantics

- Missing manifest means no snapshot.
- Malformed manifest is storage corruption and should return an error.
- Manifest scope mismatch is storage corruption and should return an error.
- Missing chunk for an existing manifest is storage corruption and should return an error.
- Chunk checksum mismatch is storage corruption and should return an error.
- Whole checksum mismatch is storage corruption and should return an error.
- Old inline `raftpb.Snapshot` values in Pebble are unsupported. Because the format has not shipped, they may be treated as malformed manifests.

## Testing Strategy

### Raftlog tests

Required tests:

```text
TestPebbleSaveSnapshotWritesExternalChunksOnly
TestPebbleSnapshotRoundTripFromExternalChunks
TestPebbleTermAtSnapshotIndexUsesManifest
TestPebbleSaveSnapshotTrimsCoveredEntriesWithManifest
TestPebbleInitialStateUsesSnapshotManifestConfState
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
```

### Crash and GC tests

Required tests:

```text
TestSnapshotGCRemovesTmpDirsWithoutManifest
TestSnapshotGCRemovesUnreferencedFinalDirs
TestSnapshotGCKeepsManifestReferencedSnapshot
TestSnapshotManifestWithMissingChunkFailsSnapshotRead
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
```

Flow:

1. open real `raftlog` DB with a snapshot path;
2. run Slot Raft compaction;
3. close runtime and DB;
4. reopen;
5. restore snapshot and replay post-snapshot entries;
6. verify snapshot payload was stored only in chunk files.

### Config tests

Add tests in `internal/app` and `cmd/wukongim` for:

- default snapshot path;
- configured snapshot path;
- configured chunk size;
- configured GC grace;
- environment variable precedence;
- invalid zero chunk size;
- invalid negative GC grace;
- path collision validation.

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
