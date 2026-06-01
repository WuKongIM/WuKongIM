# Internalv2 Online Route Batch 6 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Wire the online route module into `internalv2/app` when the cluster runtime supports online RPC.

**Architecture:** The app composition root builds `online.App` from `infra/cluster` adapters, registers inbound online RPC handlers on clusterv2, and injects online lifecycle into the gateway handler. Wiring is conditional for test and harness compatibility: clusters that do not expose route, RPC call, and RPC registration surfaces keep the current SEND-only behavior.

**Tech Stack:** Go 1.23, `internalv2/app`, `internalv2/access/node`, `internalv2/access/gateway`, `internalv2/infra/cluster`, `internalv2/usecase/online`, `testing`.

**Spec:** `docs/superpowers/specs/2026-06-01-internalv2-online-route-design.md`

---

## Batch Scope

This batch adds:

- Process-unique `OwnerBootID` generation.
- Conditional online app construction in `internalv2/app.New`.
- Registration of inbound online node RPC handlers.
- Injection of online lifecycle into `internalv2/access/gateway`.
- App-level tests for local and remote UID leader activation.

This batch does not add black-box multi-process e2e tests. Those should be a
separate acceptance batch after this wiring is implemented.

## File Map

| Path | Responsibility |
|------|----------------|
| `internalv2/app/online.go` | Online wiring helpers, owner boot ID generation, optional cluster interface. |
| `internalv2/app/app.go` | Store `online.App`, call online wiring, inject online into gateway handler. |
| `internalv2/app/app_test.go` | App-level online wiring and activation tests. |
| `internalv2/app/FLOW.md` | Document online construction and lifecycle wiring. |
| `internalv2/FLOW.md` | Update package boundary and phase flow notes for online route wiring. |

## Task 1: App Online Wiring Helper

**Files:**
- Create: `internalv2/app/online.go`
- Modify: `internalv2/app/app.go`
- Modify: `internalv2/app/app_test.go`

- [ ] **Step 1: Write failing wiring tests**

Add tests to `internalv2/app/app_test.go`:

```go
func TestNewLeavesOnlineNilWhenClusterDoesNotSupportOnlineRPC(t *testing.T)
func TestNewWiresOnlineWhenClusterSupportsOnlineRPC(t *testing.T)
func TestNewRegistersOnlineNodeRPCHandlers(t *testing.T)
```

Use a fake cluster:

```go
type fakeOnlineCluster struct {
    fakeCluster
    routes map[string]clusterv2.Route
    handlers map[uint8]clusternet.Handler
    calls []onlineRPCCall
}

func (f *fakeOnlineCluster) RouteKey(key string) (clusterv2.Route, error)
func (f *fakeOnlineCluster) RegisterRPC(serviceID uint8, handler clusternet.Handler) error
func (f *fakeOnlineCluster) CallRPC(ctx context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error)
```

Expected behavior:

- `New(Config{}, WithCluster(&fakeCluster{}))` keeps `app.online == nil`.
- `New(Config{NodeID: 1}, WithCluster(fakeOnlineCluster))` creates non-nil
  `app.online`.
- The fake cluster records exactly the five service IDs from
  `internalv2/contracts/onlinerpc`.

- [ ] **Step 2: Run tests and verify they fail**

Run:

```bash
go test ./internalv2/app -run 'TestNewLeavesOnlineNil|TestNewWiresOnline|TestNewRegistersOnline' -count=1
```

Expected: FAIL because app online wiring does not exist.

- [ ] **Step 3: Add online app field and helper**

In `internalv2/app/app.go`, add:

```go
online *online.App
```

Add an accessor:

```go
// Online returns the online route usecase app.
func (a *App) Online() *online.App
```

Create `internalv2/app/online.go`:

```go
type onlineClusterRuntime interface {
    clusterinfra.OnlineRouteNode
    clusterinfra.OnlineRPCCaller
    accessnode.RPCRegistrar
}

func buildOnlineApp(nodeID uint64, cluster ClusterRuntime) (*online.App, error)
func newOwnerBootID() (uint64, error)
```

Required behavior:

- `buildOnlineApp` returns nil when `cluster` does not implement
  `onlineClusterRuntime`.
- `newOwnerBootID` uses `crypto/rand` to generate a non-zero uint64.
- `buildOnlineApp` creates:
  - `clusterinfra.NewOnlineRouter(cluster)`
  - `clusterinfra.NewOnlineAuthorityPeerClient(cluster)`
  - `clusterinfra.NewOnlineOwnerClient(cluster)`
  - `online.New(online.Options{LocalNodeID: nodeID, OwnerBootID: bootID, Router: router, AuthorityPeerClient: authorityClient, OwnerClient: ownerClient})`
- `buildOnlineApp` calls `accessnode.RegisterOnlineHandlers(cluster, onlineApp)`
  after creating the app.

In `New`, call `buildOnlineApp` after `app.cluster` is finalized and before the
gateway handler is created. Keep nil online valid.

- [ ] **Step 4: Run wiring tests**

Run:

```bash
go test ./internalv2/app -run 'TestNewLeavesOnlineNil|TestNewWiresOnline|TestNewRegistersOnline' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internalv2/app/app.go internalv2/app/online.go internalv2/app/app_test.go
git commit -m "feat(internalv2): wire online app in composition root"
```

## Task 2: Gateway Lifecycle Injection Through App

**Files:**
- Modify: `internalv2/app/app.go`
- Modify: `internalv2/app/app_test.go`

- [ ] **Step 1: Write failing gateway lifecycle tests**

Add tests:

```go
func TestAppGatewayActivationRegistersLocalLeaderOnline(t *testing.T)
func TestAppGatewayActivationCallsRemoteUIDLeader(t *testing.T)
func TestAppGatewayCloseDeactivatesOnline(t *testing.T)
```

Test setup:

- Use `fakeOnlineCluster` with `NodeID=1`.
- For local leader, return `clusterv2.Route{SlotID: 1, Leader: 1, Revision: 1}`.
- For remote leader, return `clusterv2.Route{SlotID: 1, Leader: 2, Revision: 1}`
  and make `CallRPC` return an `onlinerpc.EncodeRegisterResponse(nil)` payload
  for register RPC.
- Build a gateway session with values:

```text
gateway.uid = "u1"
gateway.device_id = "d1"
gateway.device_flag = uint8(1)
gateway.device_level = uint8(1)
```

Assertions:

- Local leader activation makes `app.Online().Online(ctx, "u1")` return
  `Online=true`.
- Remote leader activation records one RPC call to node `2` using
  `RPCOnlineRegisterAuthoritative`.
- Close calls `Deactivate`; local leader online query returns offline after
  close.

- [ ] **Step 2: Run tests and verify they fail**

Run:

```bash
go test ./internalv2/app -run 'TestAppGatewayActivation|TestAppGatewayClose' -count=1
```

Expected: FAIL because gateway handler is not yet receiving the online app.

- [ ] **Step 3: Inject online into gateway handler**

In `internalv2/app/app.go`, change handler creation to:

```go
app.handler = accessgateway.New(accessgateway.Options{
    Messages:    app.messages,
    Online:      app.online,
    SendTimeout: cfg.Gateway.SendTimeout,
})
```

Do not change message usecase wiring. If `app.online` is nil, Batch 2 gateway
adapter keeps lifecycle no-op behavior.

- [ ] **Step 4: Run gateway lifecycle tests**

Run:

```bash
go test ./internalv2/app -run 'TestAppGatewayActivation|TestAppGatewayClose' -count=1
```

Expected: PASS.

- [ ] **Step 5: Run broader app tests**

Run:

```bash
go test ./internalv2/app -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internalv2/app/app.go internalv2/app/app_test.go
git commit -m "feat(internalv2): inject online lifecycle into gateway"
```

## Task 3: Flow Docs And Focused Verification

**Files:**
- Modify: `internalv2/app/FLOW.md`
- Modify: `internalv2/FLOW.md`

- [ ] **Step 1: Update app FLOW**

Update `internalv2/app/FLOW.md` construction flow:

```text
New(Config)
  -> create or receive clusterv2 runtime
  -> create message.App
  -> when the cluster exposes RouteKey, CallRPC, and RegisterRPC:
       create online.App with infra/cluster online adapters
       register access/node online RPC handlers
  -> create access/gateway.Handler with message and optional online lifecycle
```

Update lifecycle notes:

```text
Online has no background worker in phase 1. Its state is memory-only and follows
gateway lifecycle plus node RPC calls.
```

- [ ] **Step 2: Update internalv2 FLOW**

Update `internalv2/FLOW.md`:

- Add `access/node` to the package boundary table.
- Add `usecase/online` to the package boundary table.
- Add `contracts/onlinerpc` to the package boundary table.
- Add an online route flow:

```text
pkg/gateway CONNECT
  -> access/gateway.OnSessionActivate
  -> usecase/online.Activate
  -> infra/cluster OnlineRouter
  -> local authorityDir or access/node online RPC on UID leader
```

- [ ] **Step 3: Run focused verification**

Run:

```bash
go test ./internalv2/app ./internalv2/access/gateway ./internalv2/access/node ./internalv2/infra/cluster ./internalv2/usecase/online -count=1
```

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internalv2/app/FLOW.md internalv2/FLOW.md
git commit -m "docs(internalv2): document online route wiring"
```

## Batch 6 Verification

Run:

```bash
go test ./internalv2/app ./internalv2/access/gateway ./internalv2/access/node ./internalv2/infra/cluster ./internalv2/usecase/online -count=1
```

Expected: PASS.

## Batch 6 Acceptance

- `internalv2/app.New` wires online when the cluster runtime exposes route,
  RPC call, and RPC registration surfaces.
- Existing tests using simple fake clusters keep SEND-only behavior.
- Gateway activation registers local UID leader routes in-process.
- Gateway activation to a remote UID leader uses online register RPC.
- Gateway close deactivates online state.
- `internalv2/app` docs describe online wiring and phase-1 memory semantics.
