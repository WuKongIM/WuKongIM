# `slot` Naming Unification Design

**Date:** 2026-04-10
**Status:** Proposed

## Overview

Unify the repository's duplicated `group`/`slot` terminology so the domain concept is expressed only as `slot`.

This is an intentional non-compatible rename. The goal is not to preserve existing import paths or exported identifiers. The goal is to make the slot domain model readable and stable across package paths, APIs, file names, tests, comments, and error text.

The rename scope is limited to places where `group` already means the same thing as `slot`. Generic grouping concepts that are not part of the slot domain remain unchanged.

## Goals

- Express the replicated metadata and Multi-Raft partition concept consistently as `slot`
- Remove mixed terminology from package paths, exported APIs, internal helpers, tests, and comments
- Rename `pkg/group/...` to `pkg/slot/...` and update all repository imports in one cut
- Preserve mechanism-oriented package names such as `multiraft`, `meta`, and `fsm`
- Avoid touching unrelated `group` usages that mean listener groups, runtime groups, or ordinary collections

## Non-Goals

- Preserving backward-compatible import paths or exported aliases
- Renaming every `group` token in the repository
- Reworking runtime behavior, storage format, or network protocols
- Redesigning the Raft runtime architecture while performing the rename
- Changing generic implementation package names when they already describe mechanism accurately

## Problem Statement

The current codebase uses `group` and `slot` for the same domain concept.

Examples from the current tree:

- the package root is `pkg/group/...`
- `pkg/group/meta` already uses `slot` in APIs such as `ForSlot`, `DeleteSlotData`, and `SlotSnapshot`
- `pkg/group/fsm` uses `slot` in state machine construction and snapshot import/export
- `pkg/group/multiraft` still exposes `GroupID`, `GroupOptions`, `BootstrapGroupRequest`, `Envelope.GroupID`, `Command.GroupID`, `Status.GroupID`, and `ErrGroup*`

This produces several problems:

1. readers cannot tell whether `group` and `slot` are different concepts or historical synonyms
2. package paths and API names point in different directions, increasing mental overhead
3. tests and comments continue to reinforce the mixed terminology even where runtime code already settled on `slot`
4. repository-wide changes become harder because every call site must remember the translation layer

The system should present one concept name for one concept. In this codebase, that name should be `slot`.

## Naming Principles

### 1. Domain concept names must be unique

Where the code models one partitioned replication domain concept, it must have one name only. `slot` becomes the canonical term across paths, types, fields, parameters, helper names, and comments.

### 2. Mechanism package names stay mechanism-oriented

`multiraft`, `meta`, and `fsm` describe implementation roles, not the domain noun. These package names remain valid and do not need slot-specific replacements.

### 3. Generic grouping words stay untouched when they are not the slot domain

Names such as listener groups, runtime groups, transport groups, or loop groups are not part of this rename unless they clearly refer to the replicated slot concept.

### 4. The rename is repository-internal and hard-cut

No compatibility aliases, wrapper packages, or duplicate exported names are introduced. After the change, the old `group` identifiers for this concept no longer exist.

### 5. File names follow concept names only when they carry the concept

Files like `group.go` should become `slot.go` when the file is centered on the slot abstraction. Files like `runtime.go` or `scheduler.go` stay unchanged if they describe behavior rather than the concept noun.

## Rename Boundary

### In scope

- `pkg/group` root path and all imports that reference it
- exported and internal identifiers where `group` means the replicated slot concept
- file names dedicated to that concept, such as `group.go`
- comments, test names, benchmark names, and error text that describe the slot concept using `group`
- direct consumers of these APIs in `pkg/...` and `internal/...`

### Out of scope

- generic `group` wording in gateway or transport code that does not refer to the slot domain
- historical string values or fixtures where changing the token would add churn without clarifying semantics, unless the string itself is asserting the slot concept
- unrelated package renames outside the slot domain

## Recommended Structure

```text
pkg/
  slot/
    multiraft/
    meta/
    fsm/
    proxy/
```

The root namespace changes from `pkg/group` to `pkg/slot`, but the mechanism-oriented subpackages remain as they are.

## Core Rename Mapping

### Package paths

- `pkg/group` -> `pkg/slot`
- `github.com/WuKongIM/WuKongIM/pkg/group/...` -> `github.com/WuKongIM/WuKongIM/pkg/slot/...`

### Multi-Raft domain types

Representative API renames:

- `GroupID` -> `SlotID`
- `GroupOptions` -> `SlotOptions`
- `BootstrapGroupRequest` -> `BootstrapSlotRequest`
- `Envelope.GroupID` -> `Envelope.SlotID`
- `Command.GroupID` -> `Command.SlotID`
- `Status.GroupID` -> `Status.SlotID`
- `ErrGroupExists` -> `ErrSlotExists`
- `ErrGroupNotFound` -> `ErrSlotNotFound`
- `ErrGroupClosed` -> `ErrSlotClosed`

### Internal implementation names

Representative internal renames:

- `type group struct` -> `type slot struct`
- `newGroup(...)` -> `newSlot(...)`
- `groups map[...]...` -> `slots map[...]...`
- `group.go` -> `slot.go`
- parameter names such as `groupID`, `group`, `groups` -> `slotID`, `slot`, `slots`

### FSM and metadata edge cleanup

These packages already lean toward `slot`, so the rename here focuses on edge consistency:

- callback and factory parameters named `groupID` become `slotID`
- cross-package references to `multiraft.GroupID` become `multiraft.SlotID`
- tests, fixtures, and comments that still describe the slot domain as `group` are updated

## Module-by-Module Design

### `pkg/slot/multiraft`

This is the primary rename center because it currently exposes the mixed terminology to the rest of the repository.

Design requirements:

- rename all public slot-domain API types to `Slot*`
- rename internal `group` runtime structures and helper methods to `slot`
- update scheduler/runtime/storage adapter code to use `slot` collections and variable names
- update tests and benchmarks so their terminology matches the runtime API
- keep the package name `multiraft` unchanged because it describes the mechanism correctly

### `pkg/slot/meta`

This package already models slot storage correctly. The work here is mostly to eliminate leftover mixed edges.

Design requirements:

- keep package name `meta`
- keep existing slot-oriented APIs such as `ForSlot`
- update imports to the new `pkg/slot/...` root
- align remaining comments, helper names, and test fixtures with `slot`

### `pkg/slot/fsm`

This package already constructs state machines by slot, but still references Multi-Raft `GroupID` names at the boundary.

Design requirements:

- update imports to `pkg/slot/...`
- rename factory signatures and local variables to `slotID`
- keep state machine behavior unchanged
- keep package name `fsm`

### `pkg/slot/proxy`

This package follows the root path rename and any API signature changes from `meta` or `multiraft`.

Design requirements:

- update import paths and field/type references
- rename any slot-domain parameters or helper names still using `group`
- leave unrelated RPC concepts unchanged

### Downstream consumers

Likely consumers include `pkg/channel/node/...` and selected `internal/usecase/...` packages.

Design requirements:

- update imports to `pkg/slot/...`
- replace `multiraft.Group*` references with the `Slot*` names
- only rename local variables to `slot` where they represent the same domain concept
- keep unrelated generic `group` terms untouched

## Implementation Order

### 1. Move the package root

Rename `pkg/group` to `pkg/slot` and update repository imports immediately. This establishes the new namespace first and avoids a prolonged mixed-path state.

### 2. Rename `multiraft` public APIs and internals

Perform the conceptual rename inside the core package next so downstream compile errors point directly to the remaining migration sites.

### 3. Clean up `fsm`, `meta`, and `proxy`

Update boundary names, imports, and comments in packages that already lean toward `slot`.

### 4. Update downstream consumers

Resolve compile fallout in `pkg/channel/node`, `internal/usecase`, and any other callers using the old package path or `Group*` symbols.

### 5. Sweep files, comments, and tests

Finish the migration by renaming concept-bearing files and removing residual slot-domain `group` terminology from tests, benchmarks, and comments.

## Error Handling and Compatibility

This design intentionally accepts repository-wide breakage during the migration window.

Expected compatibility effects:

- old import paths under `pkg/group/...` disappear
- old exported identifiers such as `GroupID` disappear
- code must compile only against the new `slot` naming after the cut

No compatibility layer is added because that would preserve the conceptual duplication the change is trying to remove.

## Risks

### 1. Over-renaming unrelated `group` usages

Some packages use `group` in the ordinary English sense rather than the slot-domain sense. A blind repository-wide replace would break naming clarity in gateway and transport code.

Mitigation:

- constrain edits by package boundary and semantic call chain
- review each remaining `group` match under touched packages after the bulk rename
- avoid changing unrelated packages unless a compile error proves they depend on the slot-domain API

### 2. API churn radiating from `multiraft`

Renaming `GroupID` and related APIs will fan out through multiple packages.

Mitigation:

- rename the core types first
- use compile errors as the migration checklist for consumers
- keep behavior unchanged so failures are naming-only, not semantic

### 3. Partial terminology cleanup

It is easy to rename paths and exported types but leave comments, file names, and tests behind.

Mitigation:

- finish with a repository grep for `pkg/group`, `GroupID`, `GroupOptions`, `BootstrapGroupRequest`, and slot-domain `ErrGroup` names
- explicitly review touched tests and benchmarks for language consistency

## Testing Strategy

### Targeted verification

Run affected package tests first:

```bash
go test ./pkg/slot/... ./pkg/channel/node/... ./internal/usecase/...
```

This provides a fast signal for the renamed API surface and its direct consumers.

### Full regression

Then run the repository test suite:

```bash
go test ./...
```

### Naming sweep

Use `rg` to confirm the old slot-domain names are gone from the migrated surface, while unrelated generic `group` names remain where they should.

Representative checks:

```bash
rg -n 'pkg/group|\bGroupID\b|\bGroupOptions\b|\bBootstrapGroupRequest\b|\bErrGroup(Exists|NotFound|Closed)\b' pkg internal
rg -n '\bgroup\b' pkg/slot internal pkg/channel/node
```

The first query should return no slot-domain matches after the migration. The second query is a manual review pass to make sure remaining `group` occurrences are genuinely non-slot concepts.

## Success Criteria

The design is complete when all of the following are true:

- the repository uses `pkg/slot/...` instead of `pkg/group/...`
- the replicated partition concept is named `slot` consistently across public and internal APIs
- `multiraft` no longer exposes slot-domain `Group*` identifiers
- file names, comments, tests, and errors no longer mix `group` and `slot` for the same concept
- unrelated generic `group` terminology remains untouched
- targeted and full test suites pass
