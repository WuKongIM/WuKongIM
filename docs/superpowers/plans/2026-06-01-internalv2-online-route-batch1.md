# Internalv2 Online Route Batch 1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the in-process `internalv2/usecase/online` core so UID online state and UID-addressed writes work behind clean ports.

**Architecture:** Add an entry-agnostic online usecase with an owner registry for local sessions, an authority directory for UID-leader routes, and gateway components that group by `Router.RouteUID`. This batch uses fake peer/owner clients in tests and stops before real node RPC, gateway rollback hooks, and app composition.

**Tech Stack:** Go 1.23, `testing`, `sync`, `sync/atomic`, `context`, `internalv2/usecase/online`.

**Spec:** `docs/superpowers/specs/2026-06-01-internalv2-online-route-design.md`

---

## Batch Scope

This batch creates a complete, unit-tested online core package:

- `Activate`, `Deactivate`, `Online`, `OnlineBatch`, `Write`, and `WriteBatch`.
- `OwnerBootID + SessionID + Version` fencing.
- `RouteUID` grouping with local leader/owner short-circuiting.
- Opaque `OutboundMessage`; no gateway frame imports.
- Import-boundary tests for `internalv2/usecase/online`.

This batch does not add real clusterv2 RPC service IDs, `internalv2/access/node`,
`internalv2/app` wiring, or gateway CONNACK rollback. Those are separate batches.

Before executing this plan, create an isolated worktree because the current main
checkout has unrelated user changes.

## File Map

| Path | Responsibility |
|------|----------------|
| `internalv2/usecase/online/FLOW.md` | Package flow, boundaries, lifecycle summary. |
| `internalv2/usecase/online/errors.go` | Online sentinel errors. |
| `internalv2/usecase/online/types.go` | Commands, results, route refs, outbound DTOs. |
| `internalv2/usecase/online/ports.go` | Router, peer client, owner client, session writer, observer ports. |
| `internalv2/usecase/online/app.go` | Options validation, app construction, public wrappers. |
| `internalv2/usecase/online/owner_registry.go` | Local session registry and owner write fencing. |
| `internalv2/usecase/online/authority_dir.go` | UID-leader route index and stale route removal. |
| `internalv2/usecase/online/authority_gateway.go` | RouteUID grouping and authority peer short-circuiting. |
| `internalv2/usecase/online/owner_gateway.go` | Owner-node grouping and owner peer short-circuiting. |
| `internalv2/usecase/online/lifecycle.go` | Activate and Deactivate flows. |
| `internalv2/usecase/online/query.go` | Online and OnlineBatch flows. |
| `internalv2/usecase/online/write.go` | Write and WriteBatch flows. |
| `internalv2/usecase/online/import_boundary_test.go` | Enforce usecase import boundary. |
| `internalv2/usecase/online/owner_registry_test.go` | Owner lifecycle and fencing tests. |
| `internalv2/usecase/online/authority_dir_test.go` | Authority directory and version-fence tests. |
| `internalv2/usecase/online/app_test.go` | Activate/query/write facade tests with fake ports. |
| `internalv2/FLOW.md` | Add `usecase/online` to the package boundary table. |

## Task 1: Package API And Boundary Skeleton

**Files:**
- Create: `internalv2/usecase/online/FLOW.md`
- Create: `internalv2/usecase/online/errors.go`
- Create: `internalv2/usecase/online/types.go`
- Create: `internalv2/usecase/online/ports.go`
- Create: `internalv2/usecase/online/app.go`
- Create: `internalv2/usecase/online/import_boundary_test.go`
- Modify: `internalv2/FLOW.md`

- [ ] **Step 1: Write failing constructor and boundary tests**

Create `internalv2/usecase/online/import_boundary_test.go` with a test named
`TestOnlineUsecaseImportBoundary`. It should parse non-test Go files in the
package and fail if any import path starts with:

```text
github.com/WuKongIM/WuKongIM/pkg/gateway
github.com/WuKongIM/WuKongIM/pkg/protocol/frame
github.com/WuKongIM/WuKongIM/pkg/clusterv2
github.com/WuKongIM/WuKongIM/pkg/channelv2
github.com/WuKongIM/WuKongIM/internalv2/access
github.com/WuKongIM/WuKongIM/internalv2/app
```

Create `internalv2/usecase/online/app_test.go` with:

```go
func TestNewRejectsMissingRouter(t *testing.T)
func TestNewRejectsMissingAuthorityPeerClient(t *testing.T)
func TestNewRejectsMissingOwnerClient(t *testing.T)
func TestNewRejectsZeroLocalNodeID(t *testing.T)
func TestNewRejectsZeroOwnerBootID(t *testing.T)
```

Each test calls `New` with an `Options` value that omits exactly one required
dependency and expects the matching sentinel error.

- [ ] **Step 2: Run tests and verify they fail**

Run:

```bash
go test ./internalv2/usecase/online -run 'TestOnlineUsecaseImportBoundary|TestNewRejects' -count=1
```

Expected: FAIL because the online package API does not exist yet.

- [ ] **Step 3: Implement minimal API skeleton**

Add `errors.go`:

```go
var (
    ErrInvalidOptions              = errors.New("internalv2/online: invalid options")
    ErrRouterRequired              = errors.New("internalv2/online: router required")
    ErrAuthorityPeerClientRequired = errors.New("internalv2/online: authority peer client required")
    ErrOwnerClientRequired         = errors.New("internalv2/online: owner client required")
    ErrSessionRequired             = errors.New("internalv2/online: session required")
    ErrSessionClosed               = errors.New("internalv2/online: session closed")
    ErrRouteNotReady               = errors.New("internalv2/online: route not ready")
    ErrNotLeader                   = errors.New("internalv2/online: not leader")
)
```

Add `Options`, `App`, and `New` in `app.go`. `New` must require non-zero
`LocalNodeID`, non-zero `OwnerBootID`, `Router`, `AuthorityPeerClient`, and
`OwnerClient`. Use English comments on exported structs, fields, and methods.

Add public types and ports matching the design:

```go
type OutboundKind uint8
type OutboundMessage struct { Kind OutboundKind; Payload []byte }
type WriteStatus uint8
type WriteResult struct { UID string; Status WriteStatus; Err error }
type UIDRoute struct { UID string; SlotID uint64; LeaderNodeID uint64; Revision uint64 }
type Router interface { RouteUID(uid string) (UIDRoute, error) }
type AuthorityPeerClient interface {
    Register(context.Context, uint64, RegisterCommand) error
    Unregister(context.Context, uint64, UnregisterCommand) error
    OnlineBatch(context.Context, uint64, []string) (map[string]OnlineState, error)
    WriteBatch(context.Context, uint64, []WriteCommand) []WriteResult
}
type OwnerClient interface { WriteOwnerBatch(context.Context, uint64, []WriteOwnerCommand) []WriteOwnerResult }
type SessionWriter interface { ID() uint64; WriteOnline(OutboundMessage) error }
```

Public methods may initially return offline or validation errors. Task 4 replaces
that skeleton behavior with full routing behavior.

- [ ] **Step 4: Run tests and verify they pass**

Run:

```bash
go test ./internalv2/usecase/online -run 'TestOnlineUsecaseImportBoundary|TestNewRejects' -count=1
```

Expected: PASS.

- [ ] **Step 5: Update FLOW docs**

Add `internalv2/usecase/online/FLOW.md` describing:

```text
access/gateway or delivery caller
  -> online.App
  -> ownerRegistry / authorityDir
  -> Router + AuthorityPeerClient + OwnerClient ports
```

Update `internalv2/FLOW.md` to add `usecase/online` as entry-agnostic online
route orchestration. Keep the existing SEND flow unchanged.

- [ ] **Step 6: Commit**

```bash
git add internalv2/FLOW.md internalv2/usecase/online
git commit -m "feat(internalv2): add online route usecase skeleton"
```

## Task 2: Owner Registry Lifecycle And Fencing

**Files:**
- Create: `internalv2/usecase/online/owner_registry.go`
- Create: `internalv2/usecase/online/owner_registry_test.go`
- Modify: `internalv2/usecase/online/types.go`
- Modify: `internalv2/usecase/online/errors.go`

- [ ] **Step 1: Write failing owner registry tests**

Create tests:

```go
func TestOwnerRegistryRegisterPendingAllocatesVersionAndActivates(t *testing.T)
func TestOwnerRegistryUnregisterIsIdempotent(t *testing.T)
func TestOwnerRegistryWriteRejectsBootMismatch(t *testing.T)
func TestOwnerRegistryWriteRejectsVersionMismatch(t *testing.T)
func TestOwnerRegistryPendingWriteIsTemporaryUnavailable(t *testing.T)
func TestOwnerRegistryWriteCopiesOutboundPayload(t *testing.T)
```

Use a fake session writer that records `OutboundMessage` values and can return
`ErrSessionClosed`.

- [ ] **Step 2: Run tests and verify they fail**

Run:

```bash
go test ./internalv2/usecase/online -run 'TestOwnerRegistry' -count=1
```

Expected: FAIL because `ownerRegistry` does not exist.

- [ ] **Step 3: Implement owner registry**

Implement:

```go
type ownerRegistry struct {
    localNodeID uint64
    ownerBootID uint64
    nextVersion atomic.Uint64
    shards []ownerShard
}

type localRouteState uint8

const (
    localRoutePending localRouteState = iota + 1
    localRouteActive
    localRouteClosing
)
```

Required behavior:

- `RegisterPending(cmd ActivateCommand) (RouteRef, error)` allocates a strictly
  increasing `Version`, stores a pending route by session ID, and clones session
  metadata into `RouteRef`.
- `MarkActive(sessionID, version)` only activates the matching version.
- `Unregister(sessionID)` is idempotent and returns the removed `RouteRef` plus
  a boolean.
- `WriteOwnerBatch(items)` rejects boot mismatch, missing session, inactive
  session, UID mismatch, and version mismatch.
- Pending routes return `ownerWriteTemporaryUnavailable`.
- Successful writes release locks before calling `SessionWriter.WriteOnline`.
- Payload bytes are cloned before writing so callers cannot mutate in-flight
  data.

- [ ] **Step 4: Run tests and verify they pass**

Run:

```bash
go test ./internalv2/usecase/online -run 'TestOwnerRegistry' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internalv2/usecase/online
git commit -m "feat(internalv2): add online owner registry"
```

## Task 3: Authority Directory

**Files:**
- Create: `internalv2/usecase/online/authority_dir.go`
- Create: `internalv2/usecase/online/authority_dir_test.go`
- Modify: `internalv2/usecase/online/types.go`

- [ ] **Step 1: Write failing authority directory tests**

Create tests:

```go
func TestAuthorityDirRegisterUpsertsByRouteKeyWithVersionFence(t *testing.T)
func TestAuthorityDirUnregisterRequiresVersionFence(t *testing.T)
func TestAuthorityDirOnlineBatchReportsAuthoritativeState(t *testing.T)
func TestAuthorityDirRemovesExpiredRoutesForTouchedUID(t *testing.T)
func TestAuthorityDirRemoveStaleRouteRequiresExactFence(t *testing.T)
```

- [ ] **Step 2: Run tests and verify they fail**

Run:

```bash
go test ./internalv2/usecase/online -run 'TestAuthorityDir' -count=1
```

Expected: FAIL because `authorityDir` does not exist.

- [ ] **Step 3: Implement authority directory**

Implement sharded maps keyed by UID:

```text
routesByUID: uid -> map[routeKey]RouteRef
routesByKey: routeKey -> uid
```

`routeKey` is `OwnerNodeID + OwnerBootID + SessionID`.

Required behavior:

- Register is an upsert by route key.
- Older `Version` values cannot overwrite newer routes.
- Unregister removes only when UID, owner node, boot ID, session ID, and version
  match.
- `OnlineBatch` returns `Online=true` and route count for touched UIDs that have
  at least one non-expired route.
- Lazy TTL cleanup only runs for touched UIDs.
- Stale removal from write results requires exact fencing.

- [ ] **Step 4: Run tests and verify they pass**

Run:

```bash
go test ./internalv2/usecase/online -run 'TestAuthorityDir' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internalv2/usecase/online
git commit -m "feat(internalv2): add online authority directory"
```

## Task 4: App Facade Routing Flows

**Files:**
- Create: `internalv2/usecase/online/authority_gateway.go`
- Create: `internalv2/usecase/online/owner_gateway.go`
- Create: `internalv2/usecase/online/lifecycle.go`
- Create: `internalv2/usecase/online/query.go`
- Create: `internalv2/usecase/online/write.go`
- Modify: `internalv2/usecase/online/app.go`
- Modify: `internalv2/usecase/online/app_test.go`

- [ ] **Step 1: Write failing app facade tests**

Add tests with fake router, fake authority peer client, fake owner client, and
fake session writer:

```go
func TestActivateRegistersLocalPendingThenAuthorityLeader(t *testing.T)
func TestActivateRollsBackPendingWhenAuthorityRegisterFails(t *testing.T)
func TestDeactivateBestEffortUnregistersAuthority(t *testing.T)
func TestOnlineBatchGroupsByRouteUIDLeader(t *testing.T)
func TestWriteBatchRoutesThroughLeaderThenOwnerAndPreservesOrder(t *testing.T)
func TestWriteBatchRemovesStaleOwnerRoutesAndReturnsOffline(t *testing.T)
```

Use `LocalNodeID=1`. Cover both local leader/owner and remote leader/owner in
the same fake setup.

- [ ] **Step 2: Run tests and verify they fail**

Run:

```bash
go test ./internalv2/usecase/online -run 'TestActivate|TestDeactivate|TestOnlineBatch|TestWriteBatch' -count=1
```

Expected: FAIL because the facade methods still have skeleton behavior.

- [ ] **Step 3: Implement lifecycle and routing**

Implement:

- `Activate`: validate, register pending locally, call authority gateway
  register, mark active, rollback on authority or mark-active failure.
- `Deactivate`: unregister local route, best-effort authority unregister.
- `Online` and `Write`: batch-of-one wrappers.
- `OnlineBatch`: call `Router.RouteUID`, group by leader, local
  short-circuit to `authorityDir`, remote call through `AuthorityPeerClient`.
- `WriteBatch`: group by UID leader, run authoritative write on local leader or
  through `AuthorityPeerClient`, and preserve input order.
- `writeAuthoritativeBatch`: look up authority routes, group by owner node,
  call `ownerRegistry` for local owner, call `OwnerClient` for remote owner,
  remove stale owner routes, and summarize `OK > TemporaryUnavailable > Offline`.

- [ ] **Step 4: Run package tests**

Run:

```bash
go test ./internalv2/usecase/online -count=1
```

Expected: PASS.

- [ ] **Step 5: Run broader internalv2 tests**

Run:

```bash
go test ./internalv2/... -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internalv2/FLOW.md internalv2/usecase/online
git commit -m "feat(internalv2): implement online route core"
```

## Batch 1 Acceptance

- `go test ./internalv2/usecase/online -count=1` passes.
- `go test ./internalv2/... -count=1` passes.
- `internalv2/usecase/online` does not import `pkg/gateway`,
  `pkg/protocol/frame`, `pkg/clusterv2`, `pkg/channelv2`,
  `internalv2/access`, or `internalv2/app`.
- `Activate` success means the owner route is active and the UID leader route is
  registered in the in-process/fake-peer model.
- `WriteBatch` callers do not need to know the UID leader or owner node.
