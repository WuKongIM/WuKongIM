# Internalv2 Online Route Batch 4 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Expose local online authority and owner operations through inbound node RPC handlers.

**Architecture:** Add adapter-facing methods on `online.App` that operate only on the local authority directory or owner registry and never reroute by UID. Then add `internalv2/access/node` handlers that decode `contracts/onlinerpc` payloads, call those local endpoints, and encode responses.

**Tech Stack:** Go 1.23, `internalv2/usecase/online`, `internalv2/contracts/onlinerpc`, `internalv2/access/node`, `pkg/clusterv2/net`, `testing`.

**Spec:** `docs/superpowers/specs/2026-06-01-internalv2-online-route-design.md`

---

## Batch Scope

This batch adds:

- Local-only RPC endpoint methods on `online.App`.
- `internalv2/access/node` online RPC handler registration.
- DTO conversion between `onlinerpc` and `online` types.

This batch does not add outbound `infra/cluster` clients, app wiring, or
integration tests. Those remain separate batches.

## File Map

| Path | Responsibility |
|------|----------------|
| `internalv2/usecase/online/rpc_endpoints.go` | Local-only adapter-facing methods on `online.App`. |
| `internalv2/usecase/online/rpc_endpoints_test.go` | Endpoint behavior tests against the online core. |
| `internalv2/usecase/online/FLOW.md` | Document local-only RPC endpoint boundary. |
| `internalv2/access/node/FLOW.md` | Node RPC adapter flow and package boundaries. |
| `internalv2/access/node/online.go` | Register online RPC handlers and convert DTOs. |
| `internalv2/access/node/online_test.go` | Handler registration and round-trip tests. |
| `internalv2/FLOW.md` | Add `access/node` to package boundaries. |

## Task 1: Online App Local RPC Endpoints

**Files:**
- Create: `internalv2/usecase/online/rpc_endpoints.go`
- Create: `internalv2/usecase/online/rpc_endpoints_test.go`
- Modify: `internalv2/usecase/online/FLOW.md`

- [ ] **Step 1: Write failing endpoint tests**

Create `internalv2/usecase/online/rpc_endpoints_test.go` with:

```go
func TestRegisterAuthoritativeStoresRouteOnLocalAuthority(t *testing.T)
func TestUnregisterAuthoritativeRemovesExactRoute(t *testing.T)
func TestOnlineAuthoritativeBatchReadsLocalAuthorityOnly(t *testing.T)
func TestWriteAuthoritativeBatchUsesLocalAuthorityRoutes(t *testing.T)
func TestWriteOwnerBatchUsesLocalOwnerRegistry(t *testing.T)
```

Test setup:

- Use `online.New` with `LocalNodeID=1` and `OwnerBootID=9001`.
- Use fake router and fake peer clients only to satisfy constructor
  dependencies.
- Register test routes by calling the new endpoint methods, not public
  `Activate`, when testing authority-only behavior.
- For owner write tests, use `Activate` once to create a local owner route and
  then call `WriteOwnerBatch`.

- [ ] **Step 2: Run endpoint tests and verify they fail**

Run:

```bash
go test ./internalv2/usecase/online -run 'TestRegisterAuthoritative|TestUnregisterAuthoritative|TestOnlineAuthoritative|TestWriteAuthoritative|TestWriteOwnerBatch' -count=1
```

Expected: FAIL because adapter-facing endpoint methods do not exist.

- [ ] **Step 3: Implement local-only endpoint methods**

Create `internalv2/usecase/online/rpc_endpoints.go`:

```go
// RegisterAuthoritative stores a route on the local UID authority.
func (a *App) RegisterAuthoritative(ctx context.Context, cmd RegisterCommand) error

// UnregisterAuthoritative removes a route from the local UID authority.
func (a *App) UnregisterAuthoritative(ctx context.Context, cmd UnregisterCommand) error

// OnlineAuthoritativeBatch reads online state from the local UID authority.
func (a *App) OnlineAuthoritativeBatch(ctx context.Context, uids []string) (map[string]OnlineState, error)

// WriteAuthoritativeBatch writes through routes stored on the local UID authority.
func (a *App) WriteAuthoritativeBatch(ctx context.Context, items []WriteCommand) []WriteResult

// WriteOwnerBatch writes concrete route-addressed messages to local owner sessions.
func (a *App) WriteOwnerBatch(ctx context.Context, items []WriteOwnerCommand) []WriteOwnerResult
```

Required behavior:

- Return `context.Canceled` or `context.DeadlineExceeded` when `ctx.Err()` is
  set before work starts.
- Never call `Router.RouteUID`.
- Never call `AuthorityPeerClient`.
- `WriteAuthoritativeBatch` delegates to the same local authoritative write
  path used when the public `WriteBatch` is already on the local UID leader.
- `WriteOwnerBatch` delegates to the local owner registry only.

- [ ] **Step 4: Run online package tests**

Run:

```bash
go test ./internalv2/usecase/online -count=1
```

Expected: PASS.

- [ ] **Step 5: Update online FLOW**

Add:

```text
Adapter-facing RPC methods are local-only. They are used by access/node after a
remote caller has already selected this node as UID leader or owner. These
methods must not reroute by UID.
```

- [ ] **Step 6: Commit**

```bash
git add internalv2/usecase/online
git commit -m "feat(internalv2): expose local online rpc endpoints"
```

## Task 2: Inbound Online Node RPC Handlers

**Files:**
- Create: `internalv2/access/node/FLOW.md`
- Create: `internalv2/access/node/online.go`
- Create: `internalv2/access/node/online_test.go`
- Modify: `internalv2/FLOW.md`

- [ ] **Step 1: Write failing handler tests**

Create `internalv2/access/node/online_test.go` with:

```go
func TestRegisterOnlineHandlersRegistersAllServiceIDs(t *testing.T)
func TestOnlineHandlerRegisterAuthoritative(t *testing.T)
func TestOnlineHandlerUnregisterAuthoritative(t *testing.T)
func TestOnlineHandlerOnlineBatch(t *testing.T)
func TestOnlineHandlerWriteBatch(t *testing.T)
func TestOnlineHandlerWriteOwnerBatch(t *testing.T)
func TestOnlineHandlerEncodesEndpointError(t *testing.T)
```

Use a fake registrar:

```go
type recordingRegistrar struct {
    handlers map[uint8]clusternet.Handler
}

func (r *recordingRegistrar) RegisterRPC(serviceID uint8, handler clusternet.Handler) error {
    if r.handlers == nil {
        r.handlers = make(map[uint8]clusternet.Handler)
    }
    r.handlers[serviceID] = handler
    return nil
}
```

Use a fake endpoint:

```go
type recordingOnlineEndpoints struct {
    registerCalls []online.RegisterCommand
    unregisterCalls []online.UnregisterCommand
    onlineUIDs []string
    writeItems []online.WriteCommand
    ownerItems []online.WriteOwnerCommand
    err error
}
```

Each handler test should encode a request with `onlinerpc`, call the registered
handler, decode the response, and assert the fake endpoint received converted
online types.

- [ ] **Step 2: Run access/node tests and verify they fail**

Run:

```bash
go test ./internalv2/access/node -count=1
```

Expected: FAIL because `internalv2/access/node` does not exist.

- [ ] **Step 3: Implement handler registration**

Create `internalv2/access/node/online.go`:

```go
// RPCRegistrar registers clusterv2 typed RPC handlers.
type RPCRegistrar interface {
    RegisterRPC(serviceID uint8, handler clusternet.Handler) error
}

// OnlineEndpoints is the local online surface exposed over node RPC.
type OnlineEndpoints interface {
    RegisterAuthoritative(context.Context, online.RegisterCommand) error
    UnregisterAuthoritative(context.Context, online.UnregisterCommand) error
    OnlineAuthoritativeBatch(context.Context, []string) (map[string]online.OnlineState, error)
    WriteAuthoritativeBatch(context.Context, []online.WriteCommand) []online.WriteResult
    WriteOwnerBatch(context.Context, []online.WriteOwnerCommand) []online.WriteOwnerResult
}

// RegisterOnlineHandlers registers online node RPC handlers.
func RegisterOnlineHandlers(reg RPCRegistrar, endpoints OnlineEndpoints) error
```

Required behavior:

- Return an error when `reg` or `endpoints` is nil.
- Register exactly five handlers using the service IDs from
  `internalv2/contracts/onlinerpc`.
- Decode request payloads with `onlinerpc`.
- Convert every route, message, state, write command, and result explicitly.
- Encode endpoint errors into the response with the matching `Encode*Response`
  helper.
- Do not import `pkg/gateway`, `pkg/protocol/frame`, or `pkg/clusterv2`.

- [ ] **Step 4: Run access/node tests**

Run:

```bash
go test ./internalv2/access/node -count=1
```

Expected: PASS.

- [ ] **Step 5: Update FLOW docs**

Create `internalv2/access/node/FLOW.md`:

```text
internalv2/access/node adapts clusterv2 typed RPC payloads to internalv2
usecases. It owns wire DTO conversion and must not contain online business
rules.
```

Update `internalv2/FLOW.md` to add:

```text
access/node | Node-to-node RPC adapter for online and later internalv2 usecases.
```

- [ ] **Step 6: Run focused tests**

Run:

```bash
go test ./internalv2/usecase/online ./internalv2/contracts/onlinerpc ./internalv2/access/node -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internalv2/FLOW.md internalv2/usecase/online internalv2/access/node
git commit -m "feat(internalv2): add online node rpc handlers"
```

## Batch 4 Verification

Run:

```bash
go test ./internalv2/usecase/online ./internalv2/contracts/onlinerpc ./internalv2/access/node -count=1
```

Expected: PASS.

## Batch 4 Acceptance

- `online.App` exposes local-only endpoint methods for node RPC adapters.
- Endpoint methods never call `Router.RouteUID` or `AuthorityPeerClient`.
- `internalv2/access/node` registers all five online service IDs.
- `internalv2/access/node` owns DTO conversion but no online business rules.
- Default `internalv2/app.New` wiring remains unchanged.
