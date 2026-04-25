# `pkg/` Package Naming Design

**Date:** 2026-04-03
**Status:** Implemented on branch `pkg-package-naming`

## Overview

Redesign `pkg/` package names so they match the code's actual responsibility instead of preserving historical names such as `controller`, `wkdb`, `wkstore`, `wkcluster`, `proto`, and `msgstore`.

The target style is intentionally non-compatible. This design optimizes for semantic accuracy, long-term readability, and stable package boundaries. It does not optimize for minimal import churn.

## Goals

- Make package names describe runtime role and technical responsibility
- Remove historical naming layers that no longer match the code
- Use top-level directories as capability domains, not source-history buckets
- Keep mechanism words such as `raft`, `isr`, `codec`, `fsm`, and `jsonrpc` explicit
- Reserve project-specific prefixes for truly project-specific protocols only
- Identify packages that should be split because their names are hard to make accurate

## Non-Goals

- Preserving existing import paths
- Avoiding all repository-wide rename churn
- Renaming `internal/*` in this task
- Reworking business APIs or behavior
- Designing the full implementation plan for the rename

## Problem Statement

The current `pkg/` tree mixes at least four naming strategies:

- historical grouping: `controller`, `proto`, `msgstore`
- project-prefix placeholders: `wkdb`, `wkstore`, `wkcluster`, `wktransport`, `wkutil`
- mechanism terms: `multiraft`, `multiisr`, `jsonrpc`
- domain terms: `channelcluster`

That mixture causes several problems:

1. many package names describe origin rather than responsibility
2. some package names are too project-specific to be informative
3. some packages expose one role but are named after another
4. some packages are difficult to name because they mix multiple responsibilities

Examples:

- `pkg/controller/wkcluster` is not a controller layer; it is a raft-backed cluster runtime and forwarding facade
- `pkg/controller/raftstore` is a `multiraft.Storage` implementation, not a generic controller package
- `pkg/controller/wkstore` is not one coherent store; it mixes distributed metadata APIs with raft state machine concerns
- `pkg/proto/wkproto` is primarily a binary protocol codec, not the protocol object model
- `pkg/msgstore/channelcluster` exposes channel-oriented append/fetch/status semantics on top of replicated logs, not a generic cluster abstraction

## Naming Principles

### 1. Top-level directories describe system role

Top-level directories should answer:

> What kind of subsystem is this?

Preferred examples:

- `replication`
- `cluster`
- `storage`
- `protocol`
- `transport`
- `util`

Rejected examples:

- `controller`
- `proto`
- `msgstore`

### 2. Leaf package names describe concrete capability

Leaf package names should answer:

> What does this package provide?

Preferred examples:

- `raftcluster`
- `metadb`
- `raftstorage`
- `metastore`
- `metafsm`
- `wkcodec`
- `wkframe`
- `nodetransport`

Rejected examples:

- `wkdb`
- `wkstore`
- `wkcluster`
- `wktransport`

### 3. Mechanism words are allowed and preferred

Mechanism terms are part of the design language of this repository and should be explicit when they reflect the real implementation:

- `raft`
- `isr`
- `jsonrpc`
- `codec`
- `fsm`
- `log`

Using these words makes package boundaries more precise than generic names such as `service`, `manager`, or `store`.

### 4. Project-specific prefixes are reserved for project-specific protocols

Project-specific prefixes such as `wk` should only remain when they communicate a genuinely project-specific wire protocol or schema.

Acceptable:

- `wkframe`
- `wkcodec`
- `wkjsonrpc`

Not acceptable:

- `wkdb`
- `wkstore`
- `wkcluster`

### 5. If a package is hard to name, assume it is mixing responsibilities

Package naming difficulty is usually a design signal, not a naming problem.

If a package needs a name that mixes:

- domain object
- runtime mechanism
- storage role
- orchestration role

then the package probably needs to be split before it can be named accurately.

`pkg/controller/wkstore` is the clearest example in the current tree.

## Recommended `pkg/` Structure

```text
pkg/
  replication/
    isr/
    isrnode/
    multiraft/

  cluster/
    raftcluster/

  storage/
    metadb/
    metastore/
    metafsm/
    raftstorage/
    channellog/

  protocol/
    wkframe/
    wkcodec/
    wkjsonrpc/

  transport/
    nodetransport/

  util/
    uuidutil/
```

## Package Mapping

### Replication

- `pkg/consensus/isr` -> `pkg/replication/isr`
  - `isr` is already precise
  - the inaccurate part is the historical top-level `consensus`

- `pkg/consensus/multiisr` -> `pkg/replication/isrnode`
  - this package is not just "many ISR groups"
  - it is the node-local runtime that manages many ISR groups, sessions, throttling, generations, and scheduling
  - `isrnode` communicates that it is a node-scoped ISR runtime

- `pkg/consensus/multiraft` -> `pkg/replication/multiraft`
  - `multiraft` is already accurate and stable
  - only the top-level namespace changes

### Cluster

- `pkg/controller/wkcluster` -> `pkg/cluster/raftcluster`
  - this package starts node transport, opens raft groups, routes keys to groups, tracks leaders, and forwards proposals
  - it is a raft-backed cluster runtime facade, not a controller package

### Storage

- `pkg/controller/wkdb` -> `pkg/storage/metadb`
  - this package stores user/channel metadata and slot snapshots
  - `db` is too generic; `metadb` is closer to the actual data role

- `pkg/controller/raftstore` -> `pkg/storage/raftstorage`
  - this package implements `multiraft.Storage` over memory and Pebble
  - the name should reflect that it is raft persistent storage infrastructure

- `pkg/controller/wkstore` -> split into:
  - `pkg/storage/metastore`
    - distributed metadata API facade used by upper layers
    - owns operations such as create/update/delete channel and user upsert entry points
  - `pkg/storage/metafsm`
    - raft state machine adapter and command codec for metadata replication
    - owns `NewStateMachine`, `NewStateMachineFactory`, command encode/decode, and apply logic

- `pkg/msgstore/channelcluster` -> `pkg/storage/channellog`
  - this package exposes channel-oriented append, fetch, idempotency, and status behavior on top of replicated logs
  - the public API is log-oriented and channel-oriented, not a generic cluster API
  - `channellog` is more truthful to the package surface than `channelcluster`

### Protocol

- `pkg/proto/wkpacket` -> `pkg/protocol/wkframe`
  - the core abstraction is `Frame`, `Framer`, and frame types
  - `packet` is close but less precise than `frame` for the actual API language

- `pkg/proto/wkproto` -> `pkg/protocol/wkcodec`
  - the package primarily implements binary encoding and decoding
  - the name should reflect codec responsibility instead of acting as a vague protocol umbrella

- `pkg/proto/jsonrpc` -> `pkg/protocol/wkjsonrpc`
  - this is not a reusable generic JSON-RPC library
  - it is the WuKong JSON-RPC schema and protocol bridge for `wkframe`

### Transport

- `pkg/wktransport` -> `pkg/transport/nodetransport`
  - this package is node-to-node TCP/RPC transport infrastructure
  - `nodetransport` communicates scope and role more clearly than the project-prefixed placeholder

### Utility

- `pkg/wkutil` -> `pkg/util/uuidutil`
  - current contents are UUID-specific
  - a broad `util` bucket is not justified by the current code
  - if the package stays one-function wide, removing it entirely and using `google/uuid` directly is also a valid future simplification

## Required Package Split

### Split `wkstore` into `metastore` and `metafsm`

Current `wkstore` mixes two separate responsibilities:

1. a distributed metadata facade used by application code
2. a raft state machine adapter with command encoding and apply logic

These concerns change for different reasons and should not share a package boundary.

`metastore` should own:

- `Store`
- metadata-oriented public methods such as channel and user operations
- dependency on `raftcluster` and `metadb`

`metafsm` should own:

- command binary format
- typed command decoding
- apply logic
- `NewStateMachine`
- `NewStateMachineFactory`

This split makes both names accurate and keeps raft-FSM internals out of the public metadata facade.

## Names Explicitly Rejected

The following names are not recommended even if they appear intuitive at first glance.

- `cluster`
  - too broad for the current `wkcluster` leaf package

- `storage`
  - too broad for `wkdb` or `wkstore` leaf packages

- `messagecluster`
  - suggests cluster orchestration more than log semantics

- `channelreplication`
  - technically plausible, but overstates the public surface of `channelcluster`

- `packet`
  - weaker than `frame` for the current protocol object model

- `binaryproto`
  - too generic and does not signal WuKong-specific semantics

- `net` or `rpc`
  - too broad for `wktransport`; the package handles node transport, pooling, and request/response framing together

- any future `service` package under `pkg/`
  - the package tree should stay mechanism-oriented and capability-oriented, not service-layer-oriented

## Migration Strategy

### Phase 1: Rename top-level namespaces

Replace historical grouping directories first:

- `consensus` -> `replication`
- `controller` -> remove in favor of `cluster` and `storage`
- `proto` -> `protocol`
- `msgstore` -> remove in favor of `storage`

This step fixes the broadest structural mismatch.

### Phase 2: Rename leaf packages with clear one-to-one mappings

Apply straightforward renames next:

- `wkcluster` -> `raftcluster`
- `wkdb` -> `metadb`
- `raftstore` -> `raftstorage`
- `wktransport` -> `nodetransport`
- `wkpacket` -> `wkframe`
- `wkproto` -> `wkcodec`
- `jsonrpc` -> `wkjsonrpc`
- `wkutil` -> `uuidutil`

### Phase 3: Split mixed-responsibility packages

Perform package splits where naming alone is insufficient:

- `wkstore` -> `metastore` + `metafsm`

This step should happen after the top-level naming scheme is already in place so the new packages land in their final namespace directly.

### Phase 4: Clean repository vocabulary

After package moves are complete:

- update import aliases that still use historical names
- update package comments and README references
- update tests and docs to use the new vocabulary consistently

## Success Criteria

This naming redesign is successful when:

- every top-level `pkg/` directory describes a system role
- every leaf package name describes a concrete technical capability
- project-prefixed placeholder names are removed except for genuinely project-specific protocols
- obvious mixed-responsibility packages are split instead of being hidden behind vague names
- a reader can infer package role from imports without needing repository history
