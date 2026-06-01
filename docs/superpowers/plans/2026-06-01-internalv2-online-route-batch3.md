# Internalv2 Online Route Batch 3 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Establish the RPC foundation needed for cross-node online routing.

**Architecture:** Add a small clusterv2 extension surface so app-level modules can register typed RPC handlers and call peer services over the existing node transport. Add an online wire contract package with service IDs, DTOs, and codecs that are independent of `internalv2/usecase/online`.

**Tech Stack:** Go 1.23, `pkg/clusterv2`, `pkg/clusterv2/net`, JSON wire codecs, `testing`.

**Spec:** `docs/superpowers/specs/2026-06-01-internalv2-online-route-design.md`

---

## Batch Scope

This batch adds only the RPC foundation:

- `pkg/clusterv2.Node.RegisterRPC` for app-owned typed RPC handlers.
- `pkg/clusterv2.Node.CallRPC` for app-owned typed RPC calls.
- `internalv2/contracts/onlinerpc` service IDs, DTOs, and codecs.

This batch does not add `internalv2/access/node`, `internalv2/infra/cluster`
online clients, app wiring, or integration tests. Those stay in later batches so
each batch remains reviewable.

## File Map

| Path | Responsibility |
|------|----------------|
| `pkg/clusterv2/api.go` | Public typed RPC extension comments and interface. |
| `pkg/clusterv2/node.go` | `RegisterRPC` and `CallRPC` methods on `Node`. |
| `pkg/clusterv2/node_defaults.go` | Replay stored extension handlers when default transport is created. |
| `pkg/clusterv2/node_rpc_test.go` | Tests for handler storage, replay, and peer calls. |
| `pkg/clusterv2/FLOW.md` | Document app-owned typed RPC extension surface. |
| `internalv2/contracts/onlinerpc/FLOW.md` | Wire contract boundary notes. |
| `internalv2/contracts/onlinerpc/ids.go` | Online RPC service IDs. |
| `internalv2/contracts/onlinerpc/types.go` | Wire DTOs independent of usecase packages. |
| `internalv2/contracts/onlinerpc/codec.go` | JSON codec helpers with version/kind prefix. |
| `internalv2/contracts/onlinerpc/codec_test.go` | Round-trip and remote-error tests. |
| `internalv2/FLOW.md` | Add `contracts/onlinerpc` to package boundaries. |

## Task 1: Clusterv2 Typed RPC Extension Surface

**Files:**
- Modify: `pkg/clusterv2/api.go`
- Modify: `pkg/clusterv2/node.go`
- Modify: `pkg/clusterv2/node_defaults.go`
- Create: `pkg/clusterv2/node_rpc_test.go`
- Modify: `pkg/clusterv2/FLOW.md`

- [ ] **Step 1: Write failing clusterv2 extension tests**

Create `pkg/clusterv2/node_rpc_test.go` with:

```go
func TestNodeRegisterRPCStoresHandlerBeforeDefaultTransportExists(t *testing.T)
func TestNodeRegisterRPCRegistersImmediatelyAfterDefaultTransportExists(t *testing.T)
func TestNodeCallRPCUsesDefaultTransportClient(t *testing.T)
func TestNodeCallRPCReturnsNotStartedWithoutTransportClient(t *testing.T)
```

Test behavior:

- Registering a handler before default transport exists stores it on `Node`.
- After `ensureDefaultTransport`, stored handlers are registered on the created
  transport server.
- Registering after default transport exists registers immediately.
- `CallRPC` delegates to the default transport client when present.
- `CallRPC` returns `clusterv2.ErrNotStarted` when no transport client exists.

Use a package-local fake handler:

```go
type echoRPCHandler struct{}

func (echoRPCHandler) HandleRPC(context.Context, []byte) ([]byte, error) {
    return []byte("ok"), nil
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run:

```bash
go test ./pkg/clusterv2 -run 'TestNodeRegisterRPC|TestNodeCallRPC' -count=1
```

Expected: FAIL because `RegisterRPC` and `CallRPC` do not exist.

- [ ] **Step 3: Add public extension API**

Add to `pkg/clusterv2/api.go`:

```go
// RPCHandler handles one app-owned typed RPC payload.
type RPCHandler interface {
    HandleRPC(context.Context, []byte) ([]byte, error)
}
```

Add to `Node` in `pkg/clusterv2/node.go`:

```go
externalRPCHandlers map[uint8]clusternet.Handler
```

Add methods:

```go
// RegisterRPC registers an app-owned typed RPC handler.
func (n *Node) RegisterRPC(serviceID uint8, handler clusternet.Handler) error

// CallRPC invokes an app-owned typed RPC service on a peer node.
func (n *Node) CallRPC(ctx context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error)
```

Required behavior:

- `RegisterRPC` returns `ErrNotStarted` for nil `Node`.
- `RegisterRPC` returns `ErrInvalidConfig` when `handler` is nil.
- `RegisterRPC` stores the handler under `n.mu`.
- If `n.transportServer` already exists, `RegisterRPC` also registers the
  handler immediately.
- `CallRPC` returns `ErrNotStarted` for nil `Node` or nil `transportClient`.
- `CallRPC` delegates to `n.transportClient.Call`.

In `pkg/clusterv2/node_defaults.go`, after creating `n.transportServer`, replay
stored `externalRPCHandlers` into the transport server.

- [ ] **Step 4: Run clusterv2 tests**

Run:

```bash
go test ./pkg/clusterv2 -run 'TestNodeRegisterRPC|TestNodeCallRPC' -count=1
```

Expected: PASS.

- [ ] **Step 5: Update clusterv2 FLOW**

Add to `pkg/clusterv2/FLOW.md`:

```text
Composition roots may register app-owned typed RPC handlers through
Node.RegisterRPC. The root stores handlers before default transport creation and
replays them when the transport server exists. Node.CallRPC exposes the default
typed RPC client for app-level adapters.
```

- [ ] **Step 6: Commit**

```bash
git add pkg/clusterv2/api.go pkg/clusterv2/node.go pkg/clusterv2/node_defaults.go pkg/clusterv2/node_rpc_test.go pkg/clusterv2/FLOW.md
git commit -m "feat(clusterv2): expose app typed rpc extension"
```

## Task 2: Online RPC Wire Contract

**Files:**
- Create: `internalv2/contracts/onlinerpc/FLOW.md`
- Create: `internalv2/contracts/onlinerpc/ids.go`
- Create: `internalv2/contracts/onlinerpc/types.go`
- Create: `internalv2/contracts/onlinerpc/codec.go`
- Create: `internalv2/contracts/onlinerpc/codec_test.go`
- Modify: `internalv2/FLOW.md`

- [ ] **Step 1: Write failing codec tests**

Create `internalv2/contracts/onlinerpc/codec_test.go` with:

```go
func TestRegisterRequestRoundTrip(t *testing.T)
func TestUnregisterRequestRoundTrip(t *testing.T)
func TestOnlineBatchResponseRoundTrip(t *testing.T)
func TestWriteBatchResponseRoundTrip(t *testing.T)
func TestWriteOwnerBatchResponseRoundTrip(t *testing.T)
func TestDecodeRejectsWrongKind(t *testing.T)
func TestRPCErrorRoundTrip(t *testing.T)
```

Each round-trip test should encode a non-empty request or response, decode it,
and compare every exported field including payload bytes and route fencing
fields.

- [ ] **Step 2: Run codec tests and verify they fail**

Run:

```bash
go test ./internalv2/contracts/onlinerpc -count=1
```

Expected: FAIL because the package does not exist.

- [ ] **Step 3: Add service IDs and DTOs**

Create `ids.go`:

```go
const (
    RPCOnlineRegisterAuthoritative uint8 = 64 + iota
    RPCOnlineUnregisterAuthoritative
    RPCOnlineBatch
    RPCOnlineWriteBatch
    RPCOnlineWriteOwnerBatch
)
```

Create DTOs in `types.go` without importing `internalv2/usecase/online`:

```go
type RouteRef struct {
    UID string
    OwnerNodeID uint64
    OwnerBootID uint64
    SessionID uint64
    Version uint64
    DeviceID string
    DeviceFlag uint8
    DeviceLevel uint8
    Listener string
    ConnectedAt time.Time
    ExpiresAt time.Time
}

type OutboundMessage struct {
    Kind uint8
    Payload []byte
}

type RegisterRequest struct { Route RouteRef }
type UnregisterRequest struct { Route RouteRef }
type OnlineBatchRequest struct { UIDs []string }
type OnlineBatchResponse struct { States map[string]OnlineState }
type WriteBatchRequest struct { Items []WriteCommand }
type WriteBatchResponse struct { Results []WriteResult }
type WriteOwnerBatchRequest struct { Items []WriteOwnerCommand }
type WriteOwnerBatchResponse struct { Results []WriteOwnerResult }
```

Also define `OnlineState`, `WriteCommand`, `WriteOwnerCommand`, `WriteResult`,
and `WriteOwnerResult` using only primitive fields, wire-local DTOs, and string
error fields.

- [ ] **Step 4: Add codec helpers**

Use the existing ChannelV2 pattern: first byte version, second byte kind, JSON
payload after that.

Implement:

```go
func EncodeRegisterRequest(RegisterRequest) ([]byte, error)
func DecodeRegisterRequest([]byte) (RegisterRequest, error)
func EncodeRegisterResponse(error) ([]byte, error)
func DecodeRegisterResponse([]byte) error
```

Repeat the same request/response helper shape for unregister, online batch,
write batch, and owner write batch.

Remote errors should round-trip as an application error string. `Decode*Response`
should return `nil` when the remote error string is empty and `errors.New(msg)`
when it is not empty.

- [ ] **Step 5: Run codec tests**

Run:

```bash
go test ./internalv2/contracts/onlinerpc -count=1
```

Expected: PASS.

- [ ] **Step 6: Update FLOW docs**

Add `internalv2/contracts/onlinerpc/FLOW.md`:

```text
onlinerpc defines wire DTOs and codecs for internalv2 online node RPC. It does
not import usecase, access, infra, gateway, clusterv2, or protocol frame
packages.
```

Update `internalv2/FLOW.md` package table with:

```text
contracts/onlinerpc | Online node RPC DTOs, service IDs, and codecs shared by access/node and infra/cluster.
```

- [ ] **Step 7: Commit**

```bash
git add internalv2/FLOW.md internalv2/contracts/onlinerpc
git commit -m "feat(internalv2): add online rpc contract"
```

## Batch 3 Verification

Run:

```bash
go test ./pkg/clusterv2 ./internalv2/contracts/onlinerpc -count=1
```

Expected: PASS.

## Batch 3 Acceptance

- `pkg/clusterv2.Node` exposes app-owned typed RPC registration and calls.
- Handlers registered before default transport creation are replayed.
- Online RPC service IDs start at 64 and do not collide with current clusterv2
  built-in service IDs.
- `internalv2/contracts/onlinerpc` does not import `internalv2/usecase/online`.
- Default `internalv2/app.New` wiring remains unchanged.
