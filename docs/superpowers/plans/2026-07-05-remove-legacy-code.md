# Remove Legacy Code Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove the former v1 runtime trees now that the promoted v2 code lives on canonical paths.

**Architecture:** Treat `cmd/wukongim`, `internal`, `pkg/channel`, `pkg/cluster`, `pkg/controller`, and `pkg/transport` as canonical. First remove remaining non-production test references to legacy packages, then update boundary documentation and guard tests, then delete `internal/legacy`, `pkg/legacy`, and `test/legacy`.

**Tech Stack:** Go, `go test`, repository FLOW docs, AGENTS.md, package import-boundary tests.

---

## Current Evidence

- `cmd/wukongim/main.go` imports `internal/app`, not `internal/legacy`.
- Focused preflight passed:
  - `GOWORK=off GOCACHE=/private/tmp/wukongim-go-build-cache go test ./cmd/wukongim -run 'TestImportBoundaryUsesCanonicalInternalApp|TestDependencyBoundaryUsesCanonical|TestDependencyBoundaryDoesNotReachLegacyBenchInternal' -count=1`
  - `GOWORK=off GOCACHE=/private/tmp/wukongim-go-build-cache go test ./scripts -run TestPromotedProductionCodeDoesNotImportLegacyPackages -count=1`
- Non-test production files outside `internal/legacy`, `pkg/legacy`, and `test/legacy` do not import `github.com/WuKongIM/WuKongIM/internal/legacy` or `github.com/WuKongIM/WuKongIM/pkg/legacy`.
- The remaining hard references are mostly tests and docs:
  - `cmd/wkdb/main_test.go` imports `pkg/legacy/cluster` only for `HashSlotForKey`.
  - `pkg/slot/proxy` tests use `pkg/legacy/cluster` and `pkg/legacy/transport` as a test harness.
  - `scripts/package_promotion_docs_test.go` still expects legacy directories and slot-proxy fallback docs to exist.
  - `AGENTS.md`, `internal/FLOW.md`, `pkg/slot/FLOW.md`, `pkg/cluster/FLOW.md`, `pkg/client/FLOW.md`, and `docs/development/PROJECT_KNOWLEDGE.md` still describe legacy retention.

## Scope

Delete:
- `internal/legacy/`
- `pkg/legacy/`
- `test/legacy/`

Do not delete or rename in this pass:
- Legacy-compatible public HTTP/API contracts that are implemented by promoted code.
- Historical reports under `docs/superpowers/reports/`.
- Prometheus metric names such as `wukongim_channelv2_*`; they are external observability contracts, not old Go source trees.
- Existing untracked user draft `docs/superpowers/plans/2026-07-05-legacy-app-slot-store-adapter.md`.

## Files To Modify

- Modify `cmd/wkdb/main_test.go`: replace the legacy hash-slot helper import.
- Modify `pkg/slot/proxy/*_test.go` and `pkg/slot/proxy/testutil_test.go`: remove legacy cluster/transport imports from tests.
- Modify `scripts/package_promotion_docs_test.go`: stop requiring legacy directories and legacy fallback documentation.
- Modify `AGENTS.md`: remove deleted directories from the directory tree and layer rules.
- Modify `internal/FLOW.md`: describe `internal` as the canonical product kernel without saying old code is retained.
- Modify `pkg/slot/FLOW.md`: remove the legacy RPC mux fallback note.
- Modify `pkg/cluster/FLOW.md`: describe the slot-proxy port as canonical support, not a migration bridge from `pkg/legacy/cluster`.
- Modify `pkg/client/FLOW.md`: remove `test/legacy/e2e` references.
- Modify `docs/development/PROJECT_KNOWLEDGE.md`: record that old v1 trees were removed and keep the promoted package invariant.
- Delete all files under `internal/legacy`, `pkg/legacy`, and `test/legacy`.

## Task 1: Remove Simple Legacy Test Imports

**Files:**
- Modify: `cmd/wkdb/main_test.go`
- Inspect only: `cmd/wkbench/import_boundary_test.go`, `cmd/wukongim/main_test.go`, `pkg/db/message/import_boundary_test.go`, `pkg/db/transfer/import_boundary_test.go`, `pkg/slot/fsm/import_boundary_test.go`

- [ ] **Step 1: Replace the wkdb hash-slot helper**

In `cmd/wkdb/main_test.go`, replace:

```go
"github.com/WuKongIM/WuKongIM/pkg/legacy/cluster"
```

with:

```go
"github.com/WuKongIM/WuKongIM/pkg/hashslot"
```

Then replace:

```go
cluster.HashSlotForKey("u1", 16)
```

with:

```go
hashslot.HashSlotForKey("u1", 16)
```

- [ ] **Step 2: Verify wkdb**

Run:

```bash
GOWORK=off GOCACHE=/private/tmp/wukongim-go-build-cache go test ./cmd/wkdb -count=1
```

Expected: PASS.

- [ ] **Step 3: Keep string-only import-boundary guards unless they break**

Do not remove legacy path strings from boundary tests merely because the packages no longer exist. They still prevent reintroducing those imports. Only update tests that assert the legacy directories must exist.

## Task 2: Remove Legacy Harness From pkg/slot/proxy Tests

**Files:**
- Modify: `pkg/slot/proxy/testutil_test.go`
- Modify: `pkg/slot/proxy/integration_test.go`
- Modify: `pkg/slot/proxy/authoritative_rpc_test.go`
- Modify: `pkg/slot/proxy/channel_migration_rpc_test.go`
- Modify: `pkg/slot/proxy/plugin_binding_rpc_test.go`
- Modify: `pkg/slot/proxy/channel_runtime_meta_page_integration_test.go`
- Modify: `pkg/slot/proxy/channel_page_integration_test.go`
- Modify: `pkg/slot/proxy/user_page_integration_test.go`
- Modify: `pkg/slot/proxy/rpc_hashslot_fallback_test.go`
- Modify: `pkg/slot/proxy/cmd_conversation_state_rpc_test.go`

- [ ] **Step 1: Introduce a deterministic proxy test cluster**

In `pkg/slot/proxy/testutil_test.go`, replace the `raftcluster.Cluster` based harness with a local test type that implements `Cluster` plus the optional proposer interfaces used by `hashslot_compat.go`.

Required behavior:
- `SlotIDs()` returns the configured physical slots.
- `SlotForKey(key)` maps through `HashSlotForKey(key)` and a local `hashSlotToSlot` table.
- `HashSlotForKey(key)` uses `pkg/hashslot.HashSlotForKey(key, hashSlotCount)`.
- `HashSlotsOf(slotID)` returns the logical hash slots assigned to that slot.
- `HashSlotTableVersion()` returns a non-zero test revision.
- `LeaderOf(slotID)` returns the configured leader or `ErrNoLeader`.
- `IsLocal(nodeID)` compares against the local node ID.
- `PeersForSlot(slotID)` returns configured peers.
- `RPCService(ctx, nodeID, slotID, serviceID, payload)` dispatches to the target test node's registered `Store` RPC handler.
- `ProposeWithHashSlot(ctx, slotID, hashSlot, cmd)` applies the Slot FSM command to the authoritative test node DB for that hash slot.
- `ProposeLocalWithHashSlot(ctx, slotID, hashSlot, cmd)` applies only when the local node is the configured leader; otherwise return `ErrNotLeader`.
- `ProposeWithHashSlotResult(ctx, slotID, hashSlot, cmd)` returns the Slot FSM apply result when tests require command output.

This is intentionally a `pkg/slot/proxy` harness, not a promoted `pkg/cluster.Node` harness. `pkg/cluster.Node` already has its own slot-proxy port tests, while `pkg/slot/proxy` tests need direct access to the `metadb.DB` shards they seed and assert against.

- [ ] **Step 2: Replace two-node helpers**

Keep the existing helper names if practical:
- `startTwoNodeShardedStores(t)` should create two test nodes, two DBs, physical slots `1` and `2`, and leaders `1 -> node1`, `2 -> node2`.
- `startTwoNodeHashSlotStores(t, hashSlotCount)` should create the same two test nodes with a deterministic logical hash-slot table split across slots.
- `testStoreNode.cluster` should use the new local cluster type or an interface accepted by helper functions, not `*raftcluster.Cluster`.
- `newLegacyTestStore` should become `newProxyTestStore` and call `Store.RegisterRPCHandlers` into the local test cluster's handler map.

- [ ] **Step 3: Replace legacy error assertions**

In proxy tests, replace checks against:

```go
raftcluster.ErrNoLeader
raftcluster.ErrNotLeader
raftcluster.ErrSlotNotFound
```

with:

```go
ErrNoLeader
ErrNotLeader
ErrSlotNotFound
```

or `errors.Is(err, ErrNoLeader)` style assertions.

- [ ] **Step 4: Replace legacy hash-slot helpers**

In proxy tests, replace:

```go
raftcluster.HashSlotForKey(uid, count)
```

with:

```go
hashslot.HashSlotForKey(uid, count)
```

- [ ] **Step 5: Remove legacy imports**

Run:

```bash
rg -n 'github\.com/WuKongIM/WuKongIM/pkg/legacy|raftcluster|legacytransport' pkg/slot/proxy
```

Expected: no matches, except allowed text inside comments only if the comment explains removed history. Prefer no matches.

- [ ] **Step 6: Verify proxy package**

Run:

```bash
GOWORK=off GOCACHE=/private/tmp/wukongim-go-build-cache go test ./pkg/slot/proxy -count=1
```

Expected: PASS.

If modified files have `//go:build integration`, run only the focused integration package after the unit package is green:

```bash
GOWORK=off GOCACHE=/private/tmp/wukongim-go-build-cache go test -tags=integration ./pkg/slot/proxy -count=1
```

Expected: PASS. Skip this only if no integration-tagged proxy file changed.

## Task 3: Update Boundary Tests And Documentation

**Files:**
- Modify: `scripts/package_promotion_docs_test.go`
- Modify: `AGENTS.md`
- Modify: `internal/FLOW.md`
- Modify: `pkg/slot/FLOW.md`
- Modify: `pkg/cluster/FLOW.md`
- Modify: `pkg/client/FLOW.md`
- Modify: `docs/development/PROJECT_KNOWLEDGE.md`

- [ ] **Step 1: Update package promotion docs tests**

In `scripts/package_promotion_docs_test.go`:
- Remove `pkg/legacy/channel`, `pkg/legacy/cluster`, `pkg/legacy/controller`, and `pkg/legacy/transport` from the list of directories that must exist.
- Add `internal/legacy`, `pkg/legacy`, and `test/legacy` to stale directories that must not exist.
- Replace `TestSlotProxyLegacyFallbackIsDocumented` with a promoted-only check that verifies `pkg/slot/FLOW.md` and `docs/development/PROJECT_KNOWLEDGE.md` mention `pkg/cluster.Node.RegisterRPC` as the slot-proxy RPC registration path.

- [ ] **Step 2: Update AGENTS.md**

Remove these directory entries:
- `internal/legacy/`
- `pkg/legacy/cluster/`
- `pkg/legacy/channel/`
- `pkg/legacy/controller/`
- `pkg/legacy/transport/`
- `test/legacy/e2e/`

Remove the layer rule:

```text
`internal/legacy/*` 是旧 v1 server runtime 保留区；新增或迁移能力不要依赖 legacy。
```

Add a concise rule:

```text
旧 v1 runtime 已删除；新增代码不得重新引入 `internal/legacy` 或 `pkg/legacy/*`。
```

- [ ] **Step 3: Update FLOW docs**

In `internal/FLOW.md`:
- Replace statements saying the former v1 runtime remains under `internal/legacy`.
- Keep `legacy-compatible` API wording where it describes public HTTP compatibility, not old implementation code.

In `pkg/slot/FLOW.md`:
- Remove the `internal/legacy/app/slot_proxy_rpc.go` fallback sentence.
- Keep the promoted `pkg/cluster.Node.RegisterRPC` bridge as the only registration path.

In `pkg/cluster/FLOW.md`:
- Replace "transition bridge for moving Slot metadata proxy callers off `pkg/legacy/cluster`" with "canonical compatibility port used by `pkg/slot/proxy`".

In `pkg/client/FLOW.md`:
- Remove `test/legacy/e2e` from helper-user descriptions.

- [ ] **Step 4: Update project knowledge**

In `docs/development/PROJECT_KNOWLEDGE.md`:
- Change "the former v1 server runtime lives under `internal/legacy`" to "the former v1 server runtime has been removed".
- Change "old implementations live under `pkg/legacy/*`" to "old `pkg/legacy/*` implementations have been removed".
- Remove references to `internal/legacy/app/slot_proxy_rpc.go`.
- Keep the note that raw Prometheus names such as `wukongim_channelv2_*` remain external compatibility aliases.

- [ ] **Step 5: Verify docs tests**

Run:

```bash
GOWORK=off GOCACHE=/private/tmp/wukongim-go-build-cache go test ./scripts -count=1
```

Expected: PASS.

## Task 4: Delete Legacy Trees

**Files:**
- Delete: `internal/legacy/`
- Delete: `pkg/legacy/`
- Delete: `test/legacy/`

- [ ] **Step 1: Check tracked deletion scope**

Run:

```bash
git ls-files internal/legacy pkg/legacy test/legacy | wc -l
```

Current evidence before implementation: 933 tracked files.

- [ ] **Step 2: Delete tracked legacy files**

Run:

```bash
git rm -r -- internal/legacy pkg/legacy test/legacy
```

Expected: Git stages deletions for the old runtime trees.

- [ ] **Step 3: Inspect untracked leftovers under deleted trees**

Run:

```bash
git status --short internal/legacy pkg/legacy test/legacy
```

If untracked `.DS_Store`, `.log`, `.test`, or temporary files remain only inside the deleted trees, remove them as part of the deletion cleanup. Do not remove unrelated untracked files outside these trees.

## Task 5: Stale Reference Sweep

**Files:**
- Modify any file found by the strict scans below, unless it is an intentional historical artifact under `docs/superpowers/reports/`.

- [ ] **Step 1: Strict Go import scan**

Run:

```bash
rg -n 'github\.com/WuKongIM/WuKongIM/(internal/legacy|pkg/legacy)' --glob '*.go'
```

Expected: no matches.

- [ ] **Step 2: Strict active-doc path scan**

Run:

```bash
rg -n '\binternal/legacy\b|\bpkg/legacy\b|\btest/legacy\b|legacy_e2e' AGENTS.md internal pkg cmd scripts docs/development docs/wiki docker test
```

Expected:
- No references to deleted source/test directories.
- `legacy-compatible` wording may remain where it describes public API compatibility.
- `wukongim_channelv2_*` metric names may remain; they are not part of this deletion.

- [ ] **Step 3: Directory absence check**

Run:

```bash
test ! -e internal/legacy && test ! -e pkg/legacy && test ! -e test/legacy
```

Expected: exit code 0.

## Task 6: Verification Matrix

Run the narrow checks first:

```bash
GOWORK=off GOCACHE=/private/tmp/wukongim-go-build-cache go test ./cmd/wukongim ./cmd/wkdb ./cmd/wkbench ./scripts -count=1
```

```bash
GOWORK=off GOCACHE=/private/tmp/wukongim-go-build-cache go test ./pkg/slot/proxy ./pkg/cluster ./pkg/db/message ./pkg/db/transfer ./pkg/slot/fsm -count=1
```

```bash
GOWORK=off GOCACHE=/private/tmp/wukongim-go-build-cache go test ./internal/... -count=1
```

Then run the full unit suite:

```bash
GOWORK=off GOCACHE=/private/tmp/wukongim-go-build-cache go test ./... -count=1
```

Expected: PASS.

Do not run full integration by default. Only run focused integration if a touched integration-tagged proxy test needs proof:

```bash
GOWORK=off GOCACHE=/private/tmp/wukongim-go-build-cache go test -tags=integration ./pkg/slot/proxy -count=1
```

## Task 7: Commit Strategy

Use three commits to keep review and rollback clean:

1. `test: remove legacy package dependencies from active tests`
   - `cmd/wkdb/main_test.go`
   - `pkg/slot/proxy/*_test.go`

2. `docs: mark legacy runtime removed`
   - `AGENTS.md`
   - FLOW docs
   - `docs/development/PROJECT_KNOWLEDGE.md`
   - `scripts/package_promotion_docs_test.go`

3. `chore: delete legacy runtime trees`
   - `internal/legacy`
   - `pkg/legacy`
   - `test/legacy`

Before each commit:

```bash
git diff --check
```

Expected: no whitespace errors.

## Risks And Controls

- `pkg/slot/proxy` coverage could accidentally become too fake. Control this by keeping all existing behavior assertions and using `pkg/cluster`'s existing `node_slot_proxy_port` and default cluster tests as the real `Node` coverage.
- Removing docs too aggressively could erase useful history. Control this by updating active docs only and leaving historical `docs/superpowers/reports/` alone.
- Removing the word `legacy` everywhere would be wrong. Public API compatibility wording can stay; deleted implementation path references must go.
- Full `go test ./...` may uncover unrelated flakes. If that happens, rerun the failing package focused before changing code.
