# Pkg Restructure Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restructure `pkg/` to match the approved controller/group/channel layering proposal while preserving the already-started `transport` and `protocol` moves on the current branch.

**Architecture:** Treat the proposal in `docs/raw/pkg-restructure-proposal.md` as the source of truth, but start from the branch's actual state: `pkg/transport` and `pkg/protocol/{codec,frame,jsonrpc}` already exist, so the remaining work is to move the controller/group/channel/cluster/storage packages and then retarget imports. Execute in small compile-safe batches, using package-scoped `go test` runs as the red/green signal for each move.

**Tech Stack:** Go 1.23, `go test`, `rg`, `git mv`, existing package tests under `pkg/` and `internal/`.

---

### Task 1: Map remaining package moves and import fallout

**Files:**
- Review: `docs/raw/pkg-restructure-proposal.md`
- Review: `pkg/cluster/*`, `pkg/replication/*`, `pkg/storage/*`, `internal/app/*`, `internal/usecase/*`, `internal/access/*`
- Output: `docs/superpowers/plans/2026-04-10-pkg-restructure-implementation.md`

- [ ] Inventory packages that still live at old paths.
- [ ] Confirm `pkg/transport` and `pkg/protocol/*` are already partly migrated and should be preserved.
- [ ] List the remaining import path rewrites needed for controller/group/channel/cluster/raftlog.

### Task 2: Move `storage/raftstorage` to `raftlog`

**Files:**
- Move: `pkg/storage/raftstorage/*` -> `pkg/raftlog/*`
- Modify: all imports of `pkg/storage/raftstorage`
- Test: packages that consume raft log storage, especially controller/group/cluster tests

- [ ] Run a focused `go test` that proves old/new path mismatch exists or that the package boundary will be exercised after the move.
- [ ] Move the files, keep package name `raftstorage`, and update imports to `pkg/raftlog`.
- [ ] Re-run focused tests.

### Task 3: Move controller-layer packages under `pkg/controller`

**Files:**
- Move: `pkg/cluster/controllerraft/*` -> `pkg/controller/raft/*`
- Move: `pkg/cluster/groupcontroller/*` -> `pkg/controller/plane/*`
- Move: `pkg/storage/controllermeta/*` -> `pkg/controller/meta/*`
- Modify: imports in `pkg/cluster/*`, `internal/app/*`, and related tests

- [ ] Run focused tests around controller and cluster packages.
- [ ] Move directories and rename package declarations where needed (`groupcontroller` -> `plane`).
- [ ] Update imports and symbol aliases to the new controller-layer paths.
- [ ] Re-run focused tests.

### Task 4: Move group-layer packages under `pkg/group`

**Files:**
- Move: `pkg/replication/multiraft/*` -> `pkg/group/multiraft/*`
- Move: `pkg/storage/metadb/*` -> `pkg/group/meta/*`
- Move: `pkg/storage/metafsm/*` -> `pkg/group/fsm/*`
- Move: `pkg/storage/metastore/*` -> `pkg/group/proxy/*`
- Modify: imports in `pkg/cluster/*`, `internal/app/*`, `internal/usecase/*`, `internal/access/*`

- [ ] Run focused tests around metadata storage/FSM/proxy consumers.
- [ ] Move directories and rename package declarations where needed (`metadb` -> `meta`, `metafsm` -> `fsm`, `metastore` -> `proxy`).
- [ ] Update imports and any local aliases that would now collide.
- [ ] Re-run focused tests.

### Task 5: Move channel-layer packages under `pkg/channel`

**Files:**
- Move: `pkg/replication/isr/*` -> `pkg/channel/isr/*`
- Move: `pkg/replication/isrnode/*` -> `pkg/channel/node/*`
- Move: `pkg/replication/isrnodetransport/*` -> `pkg/channel/transport/*`
- Move: `pkg/storage/channellog/*` -> `pkg/channel/log/*`
- Modify: imports in `pkg/`, `internal/app/*`, `internal/usecase/*`, `internal/access/*`

- [ ] Run focused tests around ISR/channel log integration.
- [ ] Move directories and rename package declarations where needed (`isrnode` -> `node`, `isrnodetransport` -> `transport`, `channellog` -> `log`).
- [ ] Update imports and explicit aliases where package names would conflict with stdlib or top-level transport.
- [ ] Re-run focused tests.

### Task 6: Flatten `pkg/cluster/raftcluster` into `pkg/cluster`

**Files:**
- Move: `pkg/cluster/raftcluster/*` -> `pkg/cluster/*`
- Modify: imports across `internal/` and `pkg/`
- Test: `pkg/cluster`, `internal/app`, `internal/access/node`

- [ ] Run focused cluster tests.
- [ ] Move files and rename package declarations from `raftcluster` to `cluster`.
- [ ] Update imports and aliases.
- [ ] Re-run focused tests.

### Task 7: Final verification and docs touch-up

**Files:**
- Modify: any docs or READMEs that still point at removed package paths if they are part of touched code/docs
- Verify: whole repo

- [ ] Run `go test` on the moved `pkg/...` and affected `internal/...` packages.
- [ ] Run `go test ./...` if the focused suite is green and time permits.
- [ ] Summarize any remaining stale docs or follow-up cleanup if full replacement is intentionally deferred.
