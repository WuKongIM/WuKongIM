# `pkg/raftlog` Scoped Storage Redesign

**Date:** 2026-04-10
**Status:** Proposed

## Overview

Redesign `pkg/raftlog` around explicit raft log scopes instead of implicit slot-only storage.

This redesign is intentionally non-compatible with the current on-disk layout. It optimizes for semantic accuracy, future extensibility, cleaner Pebble key layout, and maintainable code structure.

## Goals

- Make `pkg/raftlog` a generic raft log backend instead of a slot-specialized store
- Model controller raft and slot raft as parallel first-class storage scopes
- Remove the current fake assumption that controller raft is stored as a special slot
- Introduce a versioned key schema that supports future upgrades cleanly
- Split the Pebble implementation into focused files with clear responsibilities
- Preserve existing `multiraft.Storage` behavior for read, write, snapshot, and applied-index semantics

## Non-Goals

- Preserving compatibility with the current `group-only` Pebble key layout
- Adding automatic migration from the old layout in this redesign
- Changing `multiraft.Storage` itself
- Changing controller raft semantics or slot raft semantics outside storage identity
- Introducing a temporary compatibility facade for the old fake-controller-slot model

## Problem Statement

The current package stores both slot raft state and controller raft state under one `group uint64` abstraction.

That creates three design problems:

1. the package abstraction is inaccurate because controller raft is not a slot
2. the key schema is underspecified because it only encodes one `groupID` dimension
3. the Pebble implementation is too coupled because key encoding, metadata derivation, read paths, write batching, and database lifecycle live together in one large file

The result is a package that works mechanically but has unclear semantics and poor extension points.

## Design Summary

The redesign introduces an explicit `Scope` abstraction:

```go
type ScopeKind uint8

const (
    ScopeSlot ScopeKind = 1
    ScopeController ScopeKind = 2
)

type Scope struct {
    Kind ScopeKind
    ID   uint64
}
```

`pkg/raftlog` becomes a scoped raft storage database:

- `slot/<id>` identifies one slot raft log
- `controller/<id>` identifies one controller raft log

The controller storage default remains a single logical scope, but it is modeled as a controller scope, not as slot `1` or any other reserved slot.

## Public API

The database API should expose one generic constructor and two convenience helpers:

```go
func Open(path string) (*DB, error)
func (db *DB) Close() error
func (db *DB) For(scope Scope) multiraft.Storage
func (db *DB) ForSlot(slotID uint64) multiraft.Storage
func (db *DB) ForController() multiraft.Storage
```

Recommended helper constructors:

```go
func SlotScope(slotID uint64) Scope
func ControllerScope() Scope
```

Recommended convenience behavior:

- `For(scope)` is the only fundamental lookup API
- `ForSlot` is a convenience wrapper around `SlotScope`
- `ForController` is a convenience wrapper around `ControllerScope`
- `Scope.String()` should emit stable debug strings such as `slot/7` and `controller/1`

No public constant similar to `controllerGroupStorageID` should remain. The old fake-slot identity must not leak outside `pkg/raftlog`.

## Key Schema

The Pebble key schema must encode version, scope, and record type explicitly:

```text
[formatVersion:1]
[scopeKind:1]
[scopeID:8]
[recordType:1]
[optional index:8]
```

Recommended record classes:

- manifest
- hard state
- applied index
- snapshot
- metadata
- entry

Properties of this layout:

- `formatVersion` at the front supports future schema coexistence and migration logic
- `scopeKind` separates slot and controller namespaces cleanly
- `scopeID` preserves stable per-scope identity
- `recordType` keeps metadata and entries distinct
- `entry` keys remain index-ordered by appending the big-endian log index

Examples:

- `v1 + slot + 7 + hardState`
- `v1 + slot + 7 + entry + 42`
- `v1 + controller + 1 + snapshot`

### Manifest

The redesign should also add one global manifest key outside any specific scope. The manifest stores:

- current schema version
- creation metadata
- optional feature flags for future formats

Version validation should happen when the database opens. v1 only needs single-version read/write support. Automatic migration is intentionally deferred, but the schema must make future migration possible without another redesign.

## Internal Structure

The package should be split by responsibility instead of keeping one large Pebble implementation file.

Recommended layout:

```text
pkg/raftlog/
  scope.go           scope kinds, scope constructors, validation, debug string
  types.go           log metadata types
  codec.go           Pebble key encoding and manifest encoding helpers
  meta.go            pure metadata derivation helpers
  memory.go          in-memory storage implementation
  pebble_db.go       DB lifecycle, manifest validation, store factory
  pebble_store.go    multiraft.Storage implementation for one scope
  pebble_reader.go   scope read helpers
  pebble_writer.go   write queue, batching, scope write state, apply logic
```

Responsibility boundaries:

- `DB` owns Pebble lifecycle, manifest handling, global write queue, and cached scope write state
- `pebbleStore` owns one scope-local `multiraft.Storage` view
- `codec.go` only understands encoding, not storage behavior
- `meta.go` only derives metadata and conflict-trimming behavior, not Pebble I/O
- `pebble_reader.go` performs low-level reads for one scope
- `pebble_writer.go` performs ordered batched writes for one scope

## Metadata Model

The current `groupMeta` should be replaced by a scope-neutral metadata type:

```go
type logMeta struct {
    FirstIndex    uint64
    LastIndex     uint64
    AppliedIndex  uint64
    SnapshotIndex uint64
    SnapshotTerm  uint64
    ConfState     raftpb.ConfState
}
```

Why `logMeta` instead of `groupMeta`:

- it describes one raft log's derived boundaries
- it does not encode slot-specific language
- it stays correct for controller and slot scopes alike

All internal cache keys should move from `uint64` to `Scope`.

## Write Path

The redesign should preserve one DB-level write worker and batch aggregation because that remains a good fit for Pebble.

However, write representation should become explicit. Instead of a request with multiple optional fields, use one operation interface:

```go
type writeRequest struct {
    scope Scope
    op    writeOp
    done  chan error
}

type writeOp interface {
    apply(batch *pebble.Batch, state *scopeWriteState, codec scopeCodec) error
}
```

Recommended concrete operations:

- `saveOp`
- `markAppliedOp`

Recommended write state:

```go
type scopeWriteState struct {
    hardState raftpb.HardState
    snapshot  raftpb.Snapshot
    entries   []raftpb.Entry
    meta      logMeta
}
```

This structure is preferable to the current union-by-nil-fields approach because it:

- gives each write action one explicit type
- keeps future write actions extensible
- makes flush ordering and apply logic easier to understand
- removes the need to interpret partially-populated request structs

## Read Path

The read path should be factored into scope-aware helpers:

- load hard state
- load snapshot
- load applied index
- load metadata
- load entries by range
- lazily backfill metadata when needed

`InitialState`, `Entries`, `Term`, `FirstIndex`, `LastIndex`, and `Snapshot` remain on the `multiraft.Storage` implementation, but delegate low-level Pebble work to reader helpers.

## Naming Cleanup

The redesign should delete historical names that leak the old model:

- `group` -> `scope`
- `groupMeta` -> `logMeta`
- `groupWriteState` -> `scopeWriteState`
- `encodeGroupMetaKey` -> `encodeScopeMetaKey`
- `encodeGroupStateKey` -> `encodeMetaKey`
- `loadGroupMeta/currentGroupMeta/ensureGroupMeta` -> `loadMeta/currentMeta/ensureMeta`

Tests should also use precise names such as "scope isolation" instead of "group independence" where the behavior under test is storage namespacing, not Raft membership.

## Controller Integration

Controller raft callers should move from:

```go
store := s.cfg.LogDB.ForSlot(controllerGroupStorageID)
```

to:

```go
store := s.cfg.LogDB.ForController()
```

This is a semantics fix, not just an API cleanup. The storage identity should express that controller raft is its own independent raft space.

Slot callers can keep using `ForSlot(slotID)`.

## Testing Strategy

Testing should cover four levels.

### 1. Codec tests

Verify:

- same-scope entry keys sort by index
- different scope kinds do not collide
- slot and controller prefixes remain isolated
- version prefix participates correctly in ordering

### 2. Memory storage behavior tests

Preserve current behavior coverage for:

- empty initial state
- save/read round trips
- tail replacement
- snapshot trimming
- applied index tracking
- derived conf state

### 3. Pebble single-scope behavior tests

Verify both:

- `ForSlot(7)`
- `ForController()`

behave as valid `multiraft.Storage` implementations.

### 4. Pebble multi-scope isolation tests

Add explicit coverage for:

- slot scope vs slot scope isolation
- slot scope vs controller scope isolation
- reopen durability across different scope kinds
- concurrent writes to different scopes preserving durability and separation

Recommended examples:

- `TestPebbleForControllerReturnsStorage`
- `TestPebbleSlotAndControllerAreIndependent`
- `TestScopeEntryKeysSortByVersionScopeAndIndex`
- `TestPebbleControllerStateRoundTripAcrossReopen`

## Rollout Notes

This redesign does not need old-data compatibility, so implementation can directly replace the old schema and tests. The rollout should still be staged carefully:

1. introduce `Scope` and the new public API
2. replace key codec and manifest handling
3. split Pebble implementation into focused files
4. update controller callers to `ForController()`
5. update tests from group-centric language to scope-centric language

## Decision

Adopt a scoped `pkg/raftlog` design with:

- explicit `Scope` identity
- `For(scope)` as the fundamental API
- `ForSlot` and `ForController` as convenience APIs
- versioned Pebble keys
- a manifest for schema validation
- file and type boundaries that separate DB lifecycle, read paths, write paths, metadata derivation, and encoding

This is the cleanest design because it makes controller raft and slot raft equal citizens of the storage model instead of forcing one to masquerade as the other.
