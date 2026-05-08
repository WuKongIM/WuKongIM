# Raftlog External Chunk Snapshot Store Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move all newly generated Raft `Snapshot.Data` payload bytes out of Pebble and into per-DB external chunk directories while preserving Raft storage semantics.

**Architecture:** `pkg/raftlog` stores only a snapshot manifest plus Raft/log metadata in Pebble; payload bytes live in checksum-verified chunk files under a snapshot root owned by the durable raftlog DB. Slot and Controller keep using the existing `multiraft.Storage` interface in phase one, so public `Snapshot(ctx)` reconstructs `[]byte`, while metadata-only paths read only manifest/log metadata.

**Tech Stack:** Go, Pebble v2, etcd/raft v3, crc32c Castagnoli checksums, filesystem atomic rename/fsync, `testing` + `testify`.

---

## References And Constraints

- Spec: `docs/superpowers/specs/2026-05-08-raftlog-external-chunk-snapshot-store-design.md`.
- Hard invariant: newly generated `Snapshot.Data` is never written to Pebble, regardless of size.
- Pebble stores only Raft/log metadata and `SnapshotManifest` values.
- Snapshot payload bytes are stored only as external chunk files.
- No compatibility reader for old inline Pebble snapshots is required; the program has not shipped.
- Keep `pkg/slot/multiraft.Storage` unchanged in phase one.
- Follow `AGENTS.md`: key structs/methods need concise English comments; config changes must update `wukongim.conf.example`; read `FLOW.md` for packages before changing them. `pkg/cluster/FLOW.md` exists.
- Use TDD. Keep tests targeted and fast; do not run integration-tag tests unless explicitly requested.

## File Map

### `pkg/raftlog`

- Create `pkg/raftlog/snapshot_manifest.go`: manifest model, validation, metadata conversion, checksum constants.
- Create `pkg/raftlog/snapshot_codec.go`: binary manifest encoding/decoding for Pebble `recordTypeSnapshot` values.
- Create `pkg/raftlog/snapshot_store.go`: chunk staging, checksum calculation, path validation, final publication, chunk read/assembly.
- Create `pkg/raftlog/snapshot_gc.go`: best-effort GC with active path protection and DB lifecycle integration.
- Modify `pkg/raftlog/pebble_db.go`: `Options`, defaults, snapshot store wiring, per-scope mutation locks, GC context/waitgroup, `Open(path, opts)` signature.
- Modify `pkg/raftlog/pebble_store.go`: context-aware metadata-only reads, `Save`/`MarkApplied` per-scope gate, public `Snapshot` chunk assembly.
- Modify `pkg/raftlog/pebble_reader.go`: `loadSnapshotManifest(ctx)`, `loadSnapshot(ctx)`, manifest/meta consistency checks.
- Modify `pkg/raftlog/pebble_writer.go`: metadata-only `saveOp`, stale/idempotent snapshot rules, same-request snapshot+entries trimming, no payload in worker requests.
- Modify `pkg/raftlog/meta.go`: helpers that derive metadata from `SnapshotManifest` rather than full `raftpb.Snapshot`.
- Modify `pkg/raftlog/pebble_test_helpers_test.go`: open helpers, manifest/chunk assertions, failure injection hooks.
- Modify/create tests in `pkg/raftlog/pebble_test.go`, `pkg/raftlog/snapshot_store_test.go`, `pkg/raftlog/snapshot_gc_test.go`.

### Runtime Callers

- Modify `pkg/slot/multiraft/compaction.go`: manual compaction reads snapshot boundary via `FirstIndex`/`Term`, not `Snapshot`.
- Modify `pkg/controller/raft/service.go`: manual compaction reads snapshot boundary via `FirstIndex`/`Term`, not `Snapshot`.
- Modify `pkg/cluster/controller_raft_status.go`: status reports snapshot index/term without assembling payload.
- Modify/add tests in `pkg/slot/multiraft/compaction_test.go`, `pkg/controller/raft/service_test.go`, `pkg/cluster/controller_raft_status_test.go`.

### Config And Plumbing

- Modify `internal/app/config.go`: storage snapshot config fields/defaults/validation/path collision checks/byte-size parser support if placed here.
- Modify `cmd/wukongim/config.go`: parse new `WK_STORAGE_*` keys.
- Modify `internal/app/build.go`: open Slot raftlog DB with explicit `raftlog.Options`; pass Controller options to cluster config.
- Modify `pkg/cluster/config.go`: add Controller snapshot path/chunk/grace fields and validation/default handling if needed.
- Modify `pkg/cluster/controller_host.go`: open Controller raftlog DB with explicit `raftlog.Options`.
- Modify `wukongim.conf.example`: document new keys and units.
- Modify tests in `internal/app/config_test.go`, `cmd/wukongim/config_test.go`, `pkg/cluster/config_test.go`, and cluster helper tests that call `raftlog.Open`.

---

## Task 1: Update `raftlog.Open` API And DB Options

**Files:**
- Modify: `pkg/raftlog/pebble_db.go`
- Modify: `pkg/raftlog/pebble_test_helpers_test.go`
- Modify: all current `raftlog.Open(path)` call sites under `pkg/raftlog`, `pkg/cluster`, `internal/app`, `pkg/controller/raft` tests as needed
- Test: `pkg/raftlog/pebble_test.go`

- [ ] **Step 0: Read cluster flow documentation before touching cluster call sites**

Run: `sed -n '1,220p' pkg/cluster/FLOW.md`

Expected: understand Controller startup/opening flow before updating any `pkg/cluster` `raftlog.Open` call sites.

- [ ] **Step 1: Write failing options/default tests**

Add tests to `pkg/raftlog/pebble_test.go`:

```go
func TestPebbleOpenOptionsDefaultExternalSnapshotRoot(t *testing.T) {
    path := filepath.Join(t.TempDir(), "raft")
    db, err := Open(path, Options{})
    require.NoError(t, err)
    t.Cleanup(func() { require.NoError(t, db.Close()) })

    require.Equal(t, path+"-snapshots", db.options.SnapshotPath)
    require.Equal(t, uint64(8<<20), db.options.SnapshotChunkSize)
}

func TestPebbleOpenRejectsNegativeSnapshotGCGrace(t *testing.T) {
    _, err := Open(filepath.Join(t.TempDir(), "raft"), Options{SnapshotGCGrace: -time.Second})
    require.Error(t, err)
}
```

- [ ] **Step 2: Run the failing tests**

Run: `go test ./pkg/raftlog -run 'TestPebbleOpenOptions'`

Expected: FAIL because `Options` and `Open(path, opts)` do not exist.

- [ ] **Step 3: Implement `Options` and defaults**

In `pkg/raftlog/pebble_db.go`, introduce:

```go
const defaultSnapshotChunkSize uint64 = 8 << 20

type Options struct {
    // SnapshotPath is the external root for snapshot chunk directories.
    SnapshotPath string
    // SnapshotChunkSize controls the maximum bytes written to each snapshot chunk file.
    SnapshotChunkSize uint64
    // SnapshotGCGrace is how long orphan snapshot directories remain before GC can remove them.
    SnapshotGCGrace time.Duration
}
```

Add an internal normalized options helper:

```go
func normalizeOptions(path string, opts Options) (Options, error) {
    if opts.SnapshotPath == "" {
        opts.SnapshotPath = path + "-snapshots"
    }
    if opts.SnapshotChunkSize == 0 {
        opts.SnapshotChunkSize = defaultSnapshotChunkSize
    }
    if opts.SnapshotGCGrace < 0 {
        return Options{}, errors.New("raftstorage: snapshot GC grace must be non-negative")
    }
    return opts, nil
}
```

Change `func Open(path string) (*DB, error)` to `func Open(path string, opts Options) (*DB, error)` and store the normalized options on `DB` (for example `db.options`). Update every compile failure by passing `Options{}` first; later tasks replace app/cluster paths with explicit options and initialize the concrete chunk store.

- [ ] **Step 4: Run package tests**

Run: `go test ./pkg/raftlog`

Expected: PASS after all call sites in this package are updated.

- [ ] **Step 5: Run broader compile check for Open callers**

Run: `go test ./pkg/cluster ./internal/app ./cmd/wukongim ./pkg/controller/raft`

Expected: compile failures only where `raftlog.Open` call sites still need `Options{}`; fix those call sites, then PASS or existing unrelated failures.

- [ ] **Step 6: Commit**

```bash
git add pkg/raftlog pkg/cluster internal/app cmd/wukongim pkg/controller/raft
git commit -m "feat: add raftlog snapshot storage options"
```

---

## Task 2: Implement Snapshot Manifest Model And Codec

**Files:**
- Create: `pkg/raftlog/snapshot_manifest.go`
- Create: `pkg/raftlog/snapshot_codec.go`
- Test: `pkg/raftlog/snapshot_manifest_test.go`

- [ ] **Step 1: Write failing manifest tests**

Create tests for:

```go
func TestSnapshotManifestRoundTrip(t *testing.T) { /* encode then decode */ }
func TestSnapshotManifestRejectsInvalidSnapshotID(t *testing.T) { /* ../x, absolute, short nonce */ }
func TestSnapshotManifestRejectsMalformedShapeAndOverflowingSizes(t *testing.T) { /* bad checksum count, chunk count overflow */ }
func TestSnapshotManifestUsesCRC32CCastagnoliBigEndian(t *testing.T) { /* checksum bytes match binary.BigEndian crc32.Checksum(data, crc32.MakeTable(crc32.Castagnoli)) */ }
```

- [ ] **Step 2: Run failing tests**

Run: `go test ./pkg/raftlog -run 'TestSnapshotManifest'`

Expected: FAIL because manifest types and codec do not exist.

- [ ] **Step 3: Implement manifest type and validation**

Create `SnapshotManifest` with English comments. Use an internal checksum type constant:

```go
const snapshotChecksumCRC32C = "crc32c"

var snapshotIDPattern = regexp.MustCompile(`^snap-[0-9a-f]{16}-[0-9a-f]{16}-[0-9a-f]{16}$`)
```

Validation must enforce:

- non-zero `Index` and `Term`;
- manifest scope matches requested `Scope`;
- `SnapshotID` is basename-only and matches regex;
- non-zero chunk size;
- `ChunkCount == ceil(TotalSize / ChunkSize)` and computed count fits `uint32`;
- checksum count and 4-byte checksum lengths;
- no integer overflow or `TotalSize > uint64(math.MaxInt)` in phase one.

- [ ] **Step 4: Implement binary codec**

Use a versioned custom binary format, not JSON. Include enough framing to reject trailing/short data. Keep conf state encoded using `raftpb.ConfState.Marshal()`.

- [ ] **Step 5: Run manifest tests**

Run: `go test ./pkg/raftlog -run 'TestSnapshotManifest|TestSnapshotCodec'`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/raftlog/snapshot_manifest.go pkg/raftlog/snapshot_codec.go pkg/raftlog/snapshot_manifest_test.go
git commit -m "feat: add raft snapshot manifest codec"
```

---

## Task 3: Implement External Snapshot Chunk Store

**Files:**
- Create: `pkg/raftlog/snapshot_store.go`
- Test: `pkg/raftlog/snapshot_store_test.go`

- [ ] **Step 1: Write failing chunk store tests**

Cover the required spec cases:

```go
func TestSnapshotStoreWriteSplitsChunks(t *testing.T) {}
func TestSnapshotStoreReadReassemblesChunks(t *testing.T) {}
func TestSnapshotStoreReadRejectsMissingChunk(t *testing.T) {}
func TestSnapshotStoreReadRejectsCorruptChunk(t *testing.T) {}
func TestSnapshotStoreWriteUsesTmpThenFinalDirectory(t *testing.T) {}
func TestSnapshotStoreWriteHandlesFinalDirCollisionByRegeneratingID(t *testing.T) {}
func TestSnapshotStoreReadRejectsPathTraversalSnapshotID(t *testing.T) {}
func TestSnapshotStoreAllowsZeroLengthPayload(t *testing.T) {}
func TestSnapshotStoreUsesCRC32CCastagnoliBigEndian(t *testing.T) {}
func TestSnapshotStoreRejectsMalformedShapeAndOverflowingSizes(t *testing.T) {}
func TestSnapshotStoreRejectsSnapshotIDWithInvalidNonce(t *testing.T) {}
func TestSnapshotStorePublishFinalDoesNotOverwriteExistingDirectory(t *testing.T) {}
```

- [ ] **Step 2: Run failing tests**

Run: `go test ./pkg/raftlog -run 'TestSnapshotStore'`

Expected: FAIL because `snapshotStore` does not exist.

- [ ] **Step 3: Implement staging and checksums**

Implement a store shaped like:

```go
type snapshotStore struct {
    root      string
    chunkSize uint64
    nonce     func() (string, error) // injectable for tests
}

type stagedSnapshot struct {
    manifest SnapshotManifest
    tmpDir   string
    finalDir string
}
```

Responsibilities:

- Generate `snap-%016x-%016x-<nonce>` IDs. Production nonce generation must use `crypto/rand`, 64 bits, encoded as exactly 16 lowercase hex characters; keep the nonce function injectable for deterministic tests.
- Provide a prepare step that computes tmp/final paths without creating files, so `DB` can register the tmp path as active before any filesystem writes.
- Write `.tmp-snap-*` directories under `<root>/<scopeDir>/` only after the caller has registered the tmp path as in-flight.
- Write `chunk-%06d` files, fsync files and parent directories.
- Compute per-chunk and whole crc32c checksums as 4-byte big-endian and store the final values on `staged.manifest`.
- Publish final with no-overwrite semantics.
- For zero-length data, create the final directory but no chunks, and set `WholeChecksum=crc32c(empty)`.
- Read validates shape, path, chunk sizes, checksums, and returns `raftpb.Snapshot{Metadata, Data}`.

- [ ] **Step 4: Run chunk store tests**

Run: `go test ./pkg/raftlog -run 'TestSnapshotStore'`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/raftlog/snapshot_store.go pkg/raftlog/snapshot_store_test.go
git commit -m "feat: add external snapshot chunk store"
```

---

## Task 4: Add DB Coordination, Active Paths, And GC Lifecycle

**Files:**
- Modify: `pkg/raftlog/pebble_db.go`
- Create: `pkg/raftlog/snapshot_gc.go`
- Test: `pkg/raftlog/snapshot_gc_test.go`

- [ ] **Step 1: Write failing GC/coordination tests**

Add tests:

```go
func TestSnapshotGCRemovesTmpDirsWithoutManifest(t *testing.T) {}
func TestSnapshotGCRemovesUnreferencedFinalDirs(t *testing.T) {}
func TestSnapshotGCKeepsManifestReferencedSnapshot(t *testing.T) {}
func TestSnapshotGCDoesNotCrossIndependentRaftLogDBRoots(t *testing.T) {}
func TestSnapshotGCSkipsInFlightSnapshotFinalization(t *testing.T) {}
func TestSnapshotGCSkipsInFlightTmpStaging(t *testing.T) {}
func TestSnapshotCloseWaitsForRunningGC(t *testing.T) {}
```

- [ ] **Step 2: Run failing tests**

Run: `go test ./pkg/raftlog -run 'TestSnapshotGC|TestSnapshotClose'`

Expected: FAIL because GC and active path coordination do not exist.

- [ ] **Step 3: Add DB coordination fields**

In `DB`, add:

```go
scopeMutationLocksMu sync.Mutex
scopeMutationLocks   map[Scope]*sync.Mutex
snapshotLifecycleMu  sync.Mutex
activeSnapshotPaths   map[string]struct{}
gcCtx                 context.Context
gcCancel              context.CancelFunc
gcWG                  sync.WaitGroup
```

Implement `lockScopeMutation(scope Scope) func()` with race-free lock creation.

Implement active path helpers that must be called with `snapshotLifecycleMu` held or internally lock it:

```go
func (db *DB) registerActiveSnapshotPath(path string) func()
func (db *DB) isActiveSnapshotPath(path string) bool
```

Registration must return a `defer`-safe unregister function.

- [ ] **Step 4: Implement GC**

`runSnapshotGC(ctx)` should:

- lock `snapshotLifecycleMu`;
- scan Pebble snapshot manifest keys for this DB only;
- scan `snapshotStore.root` only;
- remove old inactive tmp dirs;
- remove old inactive unreferenced final dirs;
- never scan a shared parent;
- tolerate filesystem delete errors as best-effort.

- [ ] **Step 5: Wire DB lifecycle**

`Open` creates `gcCtx` and may run one async best-effort GC pass after manifest setup. `Close` stops accepting writes, cancels GC, waits for write worker and `gcWG`, then closes Pebble.

- [ ] **Step 6: Run GC tests**

Run: `go test ./pkg/raftlog -run 'TestSnapshotGC|TestSnapshotClose'`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add pkg/raftlog/pebble_db.go pkg/raftlog/snapshot_gc.go pkg/raftlog/snapshot_gc_test.go
git commit -m "feat: add snapshot chunk GC lifecycle"
```

---

## Task 5: Replace Pebble Inline Snapshot Persistence With Manifest Writes

**Files:**
- Modify: `pkg/raftlog/pebble_store.go`
- Modify: `pkg/raftlog/pebble_reader.go`
- Modify: `pkg/raftlog/pebble_writer.go`
- Modify: `pkg/raftlog/meta.go`
- Test: `pkg/raftlog/pebble_test.go`

- [ ] **Step 1: Write failing raftlog integration tests**

Add tests:

```go
func TestPebbleSaveSnapshotWritesExternalChunksOnly(t *testing.T) {}
func TestPebbleSnapshotRoundTripFromExternalChunks(t *testing.T) {}
func TestPebbleTermAtSnapshotIndexUsesManifest(t *testing.T) {}
func TestPebbleSaveSnapshotTrimsCoveredEntriesWithManifest(t *testing.T) {}
func TestPebbleInitialStateUsesSnapshotManifestConfState(t *testing.T) {}
func TestPebbleEntryOnlySaveDoesNotReadSnapshotChunks(t *testing.T) {}
func TestPebbleMarkAppliedDoesNotReadSnapshotChunks(t *testing.T) {}
func TestPebbleSaveSnapshotWorkerRequestDoesNotCarrySnapshotData(t *testing.T) {}
func TestPebbleRejectsStaleSnapshotWithoutMutatingState(t *testing.T) {}
func TestPebbleSameIndexSnapshotIsIdempotentOnlyWhenManifestMatches(t *testing.T) {}
func TestPebbleSnapshotAndEntriesSameSaveDropsCompactedEntryPrefix(t *testing.T) {}
func TestPebbleManifestAndMetaMismatchIsCorruption(t *testing.T) {}
func TestPebbleMissingManifestWithSnapshotMetaIsCorruption(t *testing.T) {}
func TestPebbleOldInlineSnapshotValueIsMalformedManifest(t *testing.T) {}
```

Critical assertion for the first test:

```go
value := mustGetRawPebbleValue(t, db.db, encodeSnapshotKey(SlotScope(9)))
require.NotContains(t, string(value), "large-snapshot-payload-marker")
```

- [ ] **Step 2: Run failing tests**

Run: `go test ./pkg/raftlog -run 'TestPebble.*Snapshot|TestPebbleEntryOnly|TestPebbleMarkApplied|TestPebbleManifest'`

Expected: FAIL because Pebble still stores full `raftpb.Snapshot`.

- [ ] **Step 3: Implement metadata-only snapshot plan path**

Add helpers with this contract:

```go
type snapshotSavePlan struct {
    Metadata           raftpb.SnapshotMetadata
    ExistingManifest   *SnapshotManifest
    NeedsExternalWrite bool
}

func (db *DB) planSnapshotSave(ctx context.Context, scope Scope, snap raftpb.Snapshot) (snapshotSavePlan, error)
func withoutSnapshotData(st multiraft.PersistentState) persistentWriteState
```

`planSnapshotSave` reads only current manifest/meta, performs stale prechecks, and does not write chunk files. For a same-index snapshot, compute the incoming payload size and whole crc32c checksum from `snap.Data` in memory and compare them with the existing manifest plus term/conf state. If they match, return `NeedsExternalWrite=false` and `ExistingManifest`; if they differ, return an error. For a new snapshot index, return `NeedsExternalWrite=true` with metadata only; the active chunk write phase (`staged.WriteChunks`) is responsible for producing the final manifest containing `ChunkChecksums`, `WholeChecksum`, `ChunkCount`, `TotalSize`, and `SnapshotID`.

Do not pass `Snapshot.Data` to the write worker. `saveOp` must carry only hard state, retained entries, snapshot metadata, and the final manifest. The final manifest passed to `saveOp` must be either `staged.manifest` for a new snapshot or the existing manifest for an idempotent same-index snapshot.

- [ ] **Step 4: Implement full `Save`/`MarkApplied` linearization flow**

`pebbleStore.Save` must follow this shape:

```go
func (s *pebbleStore) Save(ctx context.Context, st multiraft.PersistentState) error {
    unlockScope := s.db.lockScopeMutation(s.scope)
    defer unlockScope()

    var (
        plan snapshotSavePlan
        staged *stagedSnapshot
        finalManifest *SnapshotManifest
        unregisterActive = func() {}
    )
    defer func() { unregisterActive() }()

    if st.Snapshot != nil {
        plan, err := s.db.planSnapshotSave(ctx, s.scope, *st.Snapshot)
        if err != nil { return err }
        if plan.NeedsExternalWrite {
            staged, err = s.db.snapshotStore.Prepare(ctx, s.scope, plan.Metadata)
            if err != nil { return err }
            unregisterActive = s.db.registerActiveSnapshotPath(staged.tmpDir)
            defer staged.CleanupTmpBestEffort()

            if err := staged.WriteChunks(ctx, st.Snapshot.Data); err != nil {
                return err
            }
            finalManifest = &staged.manifest
        } else {
            finalManifest = plan.ExistingManifest
        }
    }

    if staged != nil {
        s.db.snapshotLifecycleMu.Lock()
        defer s.db.snapshotLifecycleMu.Unlock()
        if err := staged.PublishFinal(ctx); err != nil { return err }

        // GC cannot run while lifecycleMu is held, so switching active protection
        // from tmp to final is atomic with respect to GC.
        unregisterActive()
        unregisterActive = s.db.registerActiveSnapshotPath(staged.finalDir)
    }

    reqState := withoutSnapshotData(st)
    err := s.db.submitWrite(&writeRequest{
        scope: s.scope,
        op: saveOp{state: reqState, snapshotManifest: finalManifest},
        done: make(chan error, 1),
    })
    if err == nil && finalManifest != nil {
        s.db.signalSnapshotGC()
    }
    return err
}
```

The exact helper names may differ, but these invariants are mandatory: per-scope gate is held from method entry through Pebble commit; chunk staging runs outside the global write worker; the tmp path is registered as active before any tmp directory or chunk file is created; active protection is switched from tmp to final while `snapshotLifecycleMu` is held; final active protection is unregistered with `defer` after the Pebble commit returns; `snapshotLifecycleMu` covers final publish, parent fsync, and Pebble commit; `MarkApplied` and entry-only `Save` also take the same per-scope gate.

- [ ] **Step 5: Implement manifest readers**

Replace `loadSnapshot()` internals with:

```go
func (s *pebbleStore) loadSnapshotManifest(ctx context.Context) (SnapshotManifest, bool, error)
func (s *pebbleStore) loadSnapshot(ctx context.Context) (raftpb.Snapshot, error)
```

`loadSnapshotManifest` reads only Pebble. `loadSnapshot` calls `snapshotStore.Read(ctx, scope, manifest)`.

- [ ] **Step 6: Update `Term`, `InitialState`, indexes, and meta**

Use manifest/log metadata only for:

- `Term(snapshotIndex)`;
- `InitialState(ctx)`;
- `FirstIndex(ctx)`;
- `LastIndex(ctx)`;
- `loadScopeWriteState`.

Manifest exists + missing/mismatched `logMeta` is corruption. `logMeta.SnapshotIndex > 0` with a missing manifest is also corruption. Empty manifest + empty `logMeta` is allowed only for bootstrap. Old inline `raftpb.Snapshot` bytes at `encodeSnapshotKey(scope)` must be treated as malformed manifest data; do not add an inline snapshot fallback reader.

- [ ] **Step 7: Implement write semantics**

In `saveOp.apply`:

- reject stale snapshot index with `raft.ErrSnapOutOfDate` or equivalent;
- allow same-index idempotency only if term/conf state/size/checksum match;
- drop entries with `Index <= snapshotIndex` when snapshot and entries are in the same request;
- update hard state commit to at least snapshot index;
- write encoded manifest to `encodeSnapshotKey(scope)`;
- never call `st.Snapshot.Marshal()`.

- [ ] **Step 8: Run raftlog tests**

Run: `go test ./pkg/raftlog`

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add pkg/raftlog
git commit -m "feat: persist raft snapshots as external chunks"
```

---

## Task 6: Harden Failure Semantics And Crash-Like Cases

**Files:**
- Modify: `pkg/raftlog/snapshot_store.go`
- Modify: `pkg/raftlog/pebble_writer.go`
- Test: `pkg/raftlog/pebble_test.go`
- Test: `pkg/raftlog/snapshot_gc_test.go`

- [ ] **Step 1: Write failure tests**

Add tests:

```go
func TestSnapshotManifestWithMissingChunkFailsSnapshotRead(t *testing.T) {}
func TestSnapshotSaveFailureDoesNotWriteManifestOrTrimEntries(t *testing.T) {}
func TestSnapshotPebbleCommitFailureLeavesRetryableOrphanDir(t *testing.T) {}
func TestSnapshotRetryBeforeGCSucceedsAfterCommitFailure(t *testing.T) {}
```

Use small injectable test hooks rather than sleeps: fail chunk write, fail publish, fail batch commit, or close Pebble at controlled points.

- [ ] **Step 2: Run failing tests**

Run: `go test ./pkg/raftlog -run 'TestSnapshot.*Failure|TestSnapshot.*MissingChunk|TestSnapshotRetry'`

Expected: FAIL until hooks and cleanup semantics are complete.

- [ ] **Step 3: Add minimal failure injection hooks**

Keep hooks unexported and test-only where possible. Do not add production config for failure injection.

- [ ] **Step 4: Implement cleanup and retry rules**

Ensure:

- chunk write failure never submits Pebble write;
- tmp cleanup is best effort;
- Pebble commit failure after final publish leaves orphan final dir;
- retry before GC generates a new snapshot ID and succeeds;
- active path unregister happens with `defer` on all paths.

- [ ] **Step 5: Run failure tests and full raftlog package**

Run: `go test ./pkg/raftlog`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/raftlog
git commit -m "test: cover snapshot chunk failure semantics"
```

---

## Task 7: Convert Metadata-Only Runtime Callers Away From `Snapshot()`

**Files:**
- Modify: `pkg/slot/multiraft/compaction.go`
- Modify: `pkg/controller/raft/service.go`
- Modify: `pkg/cluster/controller_raft_status.go`
- Test: `pkg/slot/multiraft/compaction_test.go`
- Test: `pkg/controller/raft/service_test.go`
- Test: `pkg/cluster/controller_raft_status_test.go`

- [ ] **Step 0: Read slot/controller flow documentation before edits**

Run: `sed -n '1,220p' pkg/slot/FLOW.md && sed -n '1,220p' pkg/controller/FLOW.md`

Expected: understand package flow constraints before changing `pkg/slot/multiraft` or `pkg/controller/raft`. If code changes make those docs inaccurate, update the relevant `FLOW.md` in the same task.

- [ ] **Step 1: Write failing tests with a storage that fails on `Snapshot()`**

For each caller, provide a fake storage where `Snapshot(ctx)` returns an error but `FirstIndex()` and `Term(firstIndex-1)` return valid metadata.

Required tests:

```go
func TestRuntimeManualCompactionReadsSnapshotMetadataWithoutPayload(t *testing.T) {}
func TestControllerManualCompactionReadsSnapshotMetadataWithoutPayload(t *testing.T) {}
func TestControllerRaftStatusReadsSnapshotMetadataWithoutPayload(t *testing.T) {}
```

- [ ] **Step 2: Run failing tests**

Run: `go test ./pkg/slot/multiraft ./pkg/controller/raft ./pkg/cluster -run 'MetadataWithoutPayload|ControllerRaftStatus'`

Expected: FAIL because callers still call `Snapshot()`.

- [ ] **Step 3: Implement snapshot boundary helper logic**

Use:

```go
first, err := storage.FirstIndex(ctx)
if err != nil { return err }
if first > 1 {
    snapshotIndex := first - 1
    snapshotTerm, err := storage.Term(ctx, snapshotIndex)
    if err != nil { return err }
    // treat snapshotIndex/snapshotTerm as current snapshot boundary
}
```

Do not change `multiraft.Storage`.

- [ ] **Step 4: Run targeted caller tests**

Run: `go test ./pkg/slot/multiraft ./pkg/controller/raft ./pkg/cluster -run 'MetadataWithoutPayload|ControllerRaftStatus|Compaction'`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/slot/multiraft pkg/controller/raft pkg/cluster
git commit -m "fix: avoid snapshot payload reads for raft metadata"
```

---

## Task 8: Add App And Cluster Configuration Plumbing

**Files:**
- Modify: `internal/app/config.go`
- Modify: `cmd/wukongim/config.go`
- Modify: `internal/app/build.go`
- Modify: `pkg/cluster/config.go`
- Modify: `pkg/cluster/controller_host.go`
- Test: `internal/app/config_test.go`
- Test: `cmd/wukongim/config_test.go`
- Test: `pkg/cluster/config_test.go`

- [ ] **Step 1: Write failing config tests**

Add tests for:

```go
func TestConfigApplyDefaultsDerivesRaftSnapshotPathsFromDataDir(t *testing.T) {}
func TestConfigUsesConfiguredRaftSnapshotPaths(t *testing.T) {}
func TestConfigValidateRejectsSnapshotPathOverlappingStoragePaths(t *testing.T) {}
func TestConfigValidateRejectsSnapshotPathAncestorDescendantOverlap(t *testing.T) {}
func TestConfigValidateRejectsSymlinkSnapshotPathOverlap(t *testing.T) {}
func TestConfigValidateAcceptsRaftSnapshotChunkSizeUnits(t *testing.T) {}
func TestConfigValidateRejectsInvalidRaftSnapshotChunkSizeUnits(t *testing.T) {}
func TestConfigValidateRejectsZeroRaftSnapshotChunkSize(t *testing.T) {}
func TestConfigValidateRejectsNegativeRaftSnapshotGCGrace(t *testing.T) {}
func TestClusterRuntimeConfigIncludesControllerSnapshotStorage(t *testing.T) {}
func TestBuildPassesSnapshotOptionsToSlotAndControllerRaftlog(t *testing.T) {}
```

In `cmd/wukongim/config_test.go`, add parse/env precedence tests for:

- `WK_STORAGE_RAFT_SNAPSHOT_PATH`;
- `WK_STORAGE_CONTROLLER_RAFT_SNAPSHOT_PATH`;
- `WK_STORAGE_RAFT_SNAPSHOT_CHUNK_SIZE` as `8MiB` and decimal bytes;
- invalid `WK_STORAGE_RAFT_SNAPSHOT_CHUNK_SIZE=0` and invalid units;
- `WK_STORAGE_RAFT_SNAPSHOT_GC_GRACE`;
- invalid negative `WK_STORAGE_RAFT_SNAPSHOT_GC_GRACE`;
- environment variable precedence over config file values.

- [ ] **Step 2: Run failing config tests**

Run: `go test ./internal/app ./cmd/wukongim ./pkg/cluster -run 'Snapshot|Config'`

Expected: FAIL because fields/parser/plumbing do not exist.

- [ ] **Step 3: Add storage config fields**

In `internal/app/config.go`:

```go
type StorageConfig struct {
    DBPath string
    RaftPath string
    ChannelLogPath string
    ControllerMetaPath string
    ControllerRaftPath string
    // RaftSnapshotPath stores external Slot Raft snapshot chunks.
    RaftSnapshotPath string
    // ControllerRaftSnapshotPath stores external Controller Raft snapshot chunks.
    ControllerRaftSnapshotPath string
    // RaftSnapshotChunkSize is the maximum external snapshot chunk size in bytes.
    RaftSnapshotChunkSize uint64
    // RaftSnapshotGCGrace controls when orphan snapshot directories become GC-eligible.
    RaftSnapshotGCGrace time.Duration
}
```

Defaults:

- `<dataDir>/raft-snapshots`;
- `<dataDir>/controller-raft-snapshots`;
- `8 << 20`;
- `30 * time.Minute`.

- [ ] **Step 4: Add byte-size parser**

Implement a strict parser for decimal bytes or `B`, `KiB`, `MiB`, `GiB`. Reject invalid units and overflow. Put it where it best fits existing parser ownership; if added in `cmd/wukongim/config.go`, keep app config receiving bytes.

- [ ] **Step 5: Implement path collision validation**

Normalize with `filepath.Abs` + `filepath.Clean`; where paths exist, use `filepath.EvalSymlinks`. Compare by path segments, not string prefix. Reject equality, ancestor, and descendant overlaps among snapshot paths and `DBPath`, `RaftPath`, `ControllerMetaPath`, `ControllerRaftPath`, `ChannelLogPath`, and each other.

- [ ] **Step 6: Wire app and cluster**

- `internal/app/build.go`: open Slot raftlog with explicit `raftstorage.Options{SnapshotPath, SnapshotChunkSize, SnapshotGCGrace}` from `StorageConfig`.
- `internal/app.ClusterConfig.runtimeConfig`: pass Controller snapshot options to `raftcluster.Config`.
- `pkg/cluster.Config`: add `ControllerRaftSnapshotPath`, `RaftSnapshotChunkSize`, `RaftSnapshotGCGrace` with English comments.
- `pkg/cluster/controller_host.go`: open Controller raftlog with explicit `raftstorage.Options{SnapshotPath: cfg.ControllerRaftSnapshotPath, SnapshotChunkSize: cfg.RaftSnapshotChunkSize, SnapshotGCGrace: cfg.RaftSnapshotGCGrace}`.

- [ ] **Step 7: Run config tests**

Run: `go test ./internal/app ./cmd/wukongim ./pkg/cluster -run 'Snapshot|Config'`

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/app cmd/wukongim pkg/cluster
git commit -m "feat: configure external raft snapshot storage"
```

---

## Task 9: Update Example Config And Any Needed Flow Docs

**Files:**
- Modify: `wukongim.conf.example`
- Inspect/possibly modify: `pkg/cluster/FLOW.md`
- Test: `cmd/wukongim/config_test.go` or an existing example-config test if present

- [ ] **Step 1: Write/extend example config assertion**

Add a test that reads `wukongim.conf.example` and checks it contains:

```text
WK_STORAGE_RAFT_SNAPSHOT_PATH
WK_STORAGE_CONTROLLER_RAFT_SNAPSHOT_PATH
WK_STORAGE_RAFT_SNAPSHOT_CHUNK_SIZE=8MiB
WK_STORAGE_RAFT_SNAPSHOT_GC_GRACE=30m
```

- [ ] **Step 2: Run failing test**

Run: `go test ./cmd/wukongim -run 'Example|Snapshot'`

Expected: FAIL until example config is updated.

- [ ] **Step 3: Update `wukongim.conf.example`**

Add the new storage keys near the other storage keys. Comments must explain that these directories store external Raft snapshot chunks and that chunk size accepts bytes, `B`, `KiB`, `MiB`, or `GiB`.

- [ ] **Step 4: Re-check `pkg/cluster/FLOW.md` for documentation drift**

`pkg/cluster/FLOW.md` should already have been read before the first cluster edit. Re-check whether Controller startup/opening description should mention Controller raft snapshot options. Update only if the current description becomes misleading.

- [ ] **Step 5: Run test**

Run: `go test ./cmd/wukongim ./pkg/cluster -run 'Example|Snapshot|Config'`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add wukongim.conf.example cmd/wukongim pkg/cluster/FLOW.md
git commit -m "docs: document external raft snapshot config"
```

---

## Task 10: Add Real Storage Restart Integration Coverage

**Files:**
- Modify: `pkg/slot/multiraft/compaction_test.go` or create focused test in `pkg/slot/multiraft/recovery_test.go`
- Modify: `pkg/controller/raft/service_test.go` if Controller restart coverage needs updating
- Test: affected packages

- [ ] **Step 1: Write failing real-storage test**

Add:

```go
func TestRuntimeCompactionWithPebbleExternalSnapshotRestoresAfterRestart(t *testing.T) {}
```

Flow:

1. open real `raftlog.DB` with `SnapshotPath=filepath.Join(dir, "raft-snapshots")`;
2. run Slot Raft compaction to create a snapshot;
3. close runtime and DB;
4. reopen DB/runtime;
5. restore snapshot and replay post-snapshot entries;
6. assert payload marker is absent from Pebble raw snapshot value and present in chunk files.

- [ ] **Step 2: Add ordering test**

Add:

```go
func TestRuntimeConcurrentSnapshotSavePreservesPerScopeWriteOrder(t *testing.T) {}
```

Use a test hook to block chunk staging and attempt a later entry-only save/mark-applied for the same scope. Assert the later write cannot overtake the snapshot save.

- [ ] **Step 3: Run failing integration tests**

Run: `go test ./pkg/slot/multiraft -run 'ExternalSnapshot|ConcurrentSnapshotSave'`

Expected: FAIL until hooks/behavior are correct.

- [ ] **Step 4: Implement missing test hooks or fix behavior**

Prefer unexported test hooks in `pkg/raftlog` if needed. Do not change public `multiraft.Storage`.

- [ ] **Step 5: Run targeted runtime tests**

Run: `go test ./pkg/slot/multiraft -run 'Compaction|Recovery|ExternalSnapshot|ConcurrentSnapshotSave'`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/slot/multiraft pkg/raftlog
git commit -m "test: cover external snapshot restore through multiraft"
```

---

## Task 11: Final Verification And Cleanup

**Files:**
- Any files touched above
- Optional: `docs/development/PROJECT_KNOWLEDGE.md` if a durable project rule was discovered during implementation

- [ ] **Step 1: Run targeted verification**

Run:

```bash
go test ./pkg/raftlog ./pkg/slot/multiraft ./pkg/cluster ./internal/app ./cmd/wukongim
```

Expected: PASS.

- [ ] **Step 2: Run Controller-related verification**

Run:

```bash
go test ./pkg/controller/... ./internal/usecase/management ./internal/access/manager
```

Expected: PASS.

- [ ] **Step 3: Run full unit tests**

Run:

```bash
go test ./...
```

Expected: PASS. If this is too slow or fails for unrelated pre-existing reasons, capture exact output and discuss before claiming completion.

- [ ] **Step 4: Inspect Pebble payload invariant manually**

Use targeted tests or a tiny local assertion to confirm raw Pebble snapshot values contain manifest bytes only and do not contain a unique payload marker.

- [ ] **Step 5: Review git diff**

Run:

```bash
git status --short
git diff --stat
git diff --check
```

Expected: only intended files changed, no whitespace errors.

- [ ] **Step 6: Request code review**

Use `superpowers:requesting-code-review` with the implementation commit range and the spec path.

- [ ] **Step 7: Commit final cleanup if needed**

```bash
git add <files>
git commit -m "chore: finalize external raft snapshot storage"
```

