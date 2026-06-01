# Internalv2 Online Route Batch 5 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement outbound cluster adapters for online route lookup and remote online RPC calls.

**Architecture:** `internalv2/infra/cluster` adapts `online.Router`, `online.AuthorityPeerClient`, and `online.OwnerClient` to the clusterv2 public route/RPC surface. The authority peer client always uses the explicit target leader passed by `online.App`; it never reroutes or rehashes UID internally.

**Tech Stack:** Go 1.23, `internalv2/usecase/online`, `internalv2/contracts/onlinerpc`, `pkg/clusterv2`, `testing`.

**Spec:** `docs/superpowers/specs/2026-06-01-internalv2-online-route-design.md`

---

## Batch Scope

This batch adds:

- `OnlineRouter` adapter from `clusterv2.RouteKey` to `online.Router`.
- `OnlineAuthorityPeerClient` adapter over `CallRPC`.
- `OnlineOwnerClient` adapter over `CallRPC`.
- Conversion between `online` DTOs and `contracts/onlinerpc` DTOs.

This batch does not add app wiring or integration tests. Those stay in the next
batch so this one remains a focused adapter slice.

## File Map

| Path | Responsibility |
|------|----------------|
| `internalv2/infra/cluster/online.go` | Router, authority peer client, owner client, conversions, and error mapping. |
| `internalv2/infra/cluster/online_test.go` | Adapter tests with fake clusterv2 route and RPC surfaces. |
| `internalv2/infra/cluster/FLOW.md` | Document online adapter flow. |

## Task 1: Online Router Adapter

**Files:**
- Create: `internalv2/infra/cluster/online.go`
- Create: `internalv2/infra/cluster/online_test.go`
- Modify: `internalv2/infra/cluster/FLOW.md`

- [ ] **Step 1: Write failing router adapter tests**

Create `internalv2/infra/cluster/online_test.go` with:

```go
func TestOnlineRouterMapsClusterv2RouteKey(t *testing.T)
func TestOnlineRouterMapsRouteNotReadyErrors(t *testing.T)
func TestOnlineRouterReturnsRouteNotReadyWhenNodeMissing(t *testing.T)
```

Use:

```go
type fakeOnlineRouteNode struct {
    route clusterv2.Route
    err   error
    keys  []string
}

func (n *fakeOnlineRouteNode) RouteKey(key string) (clusterv2.Route, error) {
    n.keys = append(n.keys, key)
    return n.route, n.err
}
```

Expected mapping:

- `RouteKey("u1")` is called exactly once.
- `clusterv2.Route{HashSlot: 3, SlotID: 7, Leader: 2, Revision: 11}` maps to
  `online.UIDRoute{UID: "u1", SlotID: 7, LeaderNodeID: 2, Revision: 11}`.
- `clusterv2.ErrRouteNotReady` and `clusterv2.ErrNoSlotLeader` map to
  `online.ErrRouteNotReady`.

- [ ] **Step 2: Run router tests and verify they fail**

Run:

```bash
go test ./internalv2/infra/cluster -run 'TestOnlineRouter' -count=1
```

Expected: FAIL because `OnlineRouter` does not exist.

- [ ] **Step 3: Implement `OnlineRouter`**

Create:

```go
// OnlineRouteNode is the clusterv2 route surface used by the online router.
type OnlineRouteNode interface {
    RouteKey(string) (clusterv2.Route, error)
}

// OnlineRouter adapts clusterv2 key routing to online UID routing.
type OnlineRouter struct {
    node OnlineRouteNode
}

// NewOnlineRouter creates an OnlineRouter.
func NewOnlineRouter(node OnlineRouteNode) *OnlineRouter

// RouteUID routes a UID through clusterv2's current route snapshot.
func (r *OnlineRouter) RouteUID(uid string) (online.UIDRoute, error)
```

`RouteUID` must:

- Return `online.ErrRouteNotReady` when `node` is nil.
- Preserve the input UID in the result.
- Use `route.SlotID` as `UIDRoute.SlotID`.
- Use `route.Leader` as `UIDRoute.LeaderNodeID`.
- Preserve `route.Revision`.
- Map route-not-ready/no-leader errors to `online.ErrRouteNotReady`.

- [ ] **Step 4: Run router tests**

Run:

```bash
go test ./internalv2/infra/cluster -run 'TestOnlineRouter' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internalv2/infra/cluster/online.go internalv2/infra/cluster/online_test.go
git commit -m "feat(internalv2): add online route adapter"
```

## Task 2: Authority Peer Client Adapter

**Files:**
- Modify: `internalv2/infra/cluster/online.go`
- Modify: `internalv2/infra/cluster/online_test.go`

- [ ] **Step 1: Write failing authority client tests**

Add tests:

```go
func TestOnlineAuthorityPeerClientRegisterCallsTargetLeader(t *testing.T)
func TestOnlineAuthorityPeerClientUnregisterCallsTargetLeader(t *testing.T)
func TestOnlineAuthorityPeerClientOnlineBatchDecodesResponse(t *testing.T)
func TestOnlineAuthorityPeerClientWriteBatchPreservesResultOrder(t *testing.T)
func TestOnlineAuthorityPeerClientReturnsTemporaryUnavailableOnRPCError(t *testing.T)
```

Use:

```go
type fakeOnlineRPCCaller struct {
    calls []onlineRPCCall
    responses map[uint8][]byte
    err error
}

type onlineRPCCall struct {
    nodeID uint64
    serviceID uint8
    payload []byte
}

func (c *fakeOnlineRPCCaller) CallRPC(ctx context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
    c.calls = append(c.calls, onlineRPCCall{nodeID: nodeID, serviceID: serviceID, payload: append([]byte(nil), payload...)})
    if c.err != nil {
        return nil, c.err
    }
    return c.responses[serviceID], nil
}
```

Assertions:

- `Register(ctx, 2, cmd)` calls node `2` with
  `onlinerpc.RPCOnlineRegisterAuthoritative`.
- `Unregister(ctx, 3, cmd)` calls node `3` with
  `onlinerpc.RPCOnlineUnregisterAuthoritative`.
- `OnlineBatch(ctx, 4, []string{"u1"})` decodes returned online state.
- `WriteBatch(ctx, 5, items)` returns results in the same order as the response.
- RPC error for `WriteBatch` returns one `WriteStatusTemporaryUnavailable`
  result per input item.

- [ ] **Step 2: Run authority tests and verify they fail**

Run:

```bash
go test ./internalv2/infra/cluster -run 'TestOnlineAuthorityPeerClient' -count=1
```

Expected: FAIL because `OnlineAuthorityPeerClient` does not exist.

- [ ] **Step 3: Implement authority peer client**

Create:

```go
// OnlineRPCCaller is the clusterv2 app RPC call surface used by online clients.
type OnlineRPCCaller interface {
    CallRPC(context.Context, uint64, uint8, []byte) ([]byte, error)
}

// OnlineAuthorityPeerClient adapts online authority peer calls to clusterv2 RPC.
type OnlineAuthorityPeerClient struct {
    caller OnlineRPCCaller
}

// NewOnlineAuthorityPeerClient creates an authority peer client.
func NewOnlineAuthorityPeerClient(caller OnlineRPCCaller) *OnlineAuthorityPeerClient
```

Implement the four `online.AuthorityPeerClient` methods:

```go
func (c *OnlineAuthorityPeerClient) Register(ctx context.Context, leaderNodeID uint64, cmd online.RegisterCommand) error
func (c *OnlineAuthorityPeerClient) Unregister(ctx context.Context, leaderNodeID uint64, cmd online.UnregisterCommand) error
func (c *OnlineAuthorityPeerClient) OnlineBatch(ctx context.Context, leaderNodeID uint64, uids []string) (map[string]online.OnlineState, error)
func (c *OnlineAuthorityPeerClient) WriteBatch(ctx context.Context, leaderNodeID uint64, items []online.WriteCommand) []online.WriteResult
```

Rules:

- Always call the `leaderNodeID` argument exactly.
- Never call `RouteKey`.
- Never derive a leader from UID.
- Use `onlinerpc` codecs for payloads and responses.
- Clone outbound payload bytes when converting `online.OutboundMessage` to wire
  DTOs.
- For `WriteBatch` RPC or decode error, return
  `WriteStatusTemporaryUnavailable` for every input item.

- [ ] **Step 4: Run authority tests**

Run:

```bash
go test ./internalv2/infra/cluster -run 'TestOnlineAuthorityPeerClient' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internalv2/infra/cluster/online.go internalv2/infra/cluster/online_test.go
git commit -m "feat(internalv2): add online authority rpc client"
```

## Task 3: Owner Client Adapter

**Files:**
- Modify: `internalv2/infra/cluster/online.go`
- Modify: `internalv2/infra/cluster/online_test.go`
- Modify: `internalv2/infra/cluster/FLOW.md`

- [ ] **Step 1: Write failing owner client tests**

Add tests:

```go
func TestOnlineOwnerClientCallsOwnerNode(t *testing.T)
func TestOnlineOwnerClientDecodesOwnerResults(t *testing.T)
func TestOnlineOwnerClientReturnsTemporaryUnavailableOnRPCError(t *testing.T)
```

Assertions:

- `WriteOwnerBatch(ctx, 9, items)` calls node `9` with
  `onlinerpc.RPCOnlineWriteOwnerBatch`.
- The response status and route fencing fields are converted back to
  `online.WriteOwnerResult`.
- RPC error returns one owner result per input item with the original route and
  temporary-unavailable status.

- [ ] **Step 2: Run owner tests and verify they fail**

Run:

```bash
go test ./internalv2/infra/cluster -run 'TestOnlineOwnerClient' -count=1
```

Expected: FAIL because `OnlineOwnerClient` does not exist.

- [ ] **Step 3: Implement owner client**

Create:

```go
// OnlineOwnerClient adapts online owner writes to clusterv2 RPC.
type OnlineOwnerClient struct {
    caller OnlineRPCCaller
}

// NewOnlineOwnerClient creates an owner client.
func NewOnlineOwnerClient(caller OnlineRPCCaller) *OnlineOwnerClient

func (c *OnlineOwnerClient) WriteOwnerBatch(ctx context.Context, ownerNodeID uint64, items []online.WriteOwnerCommand) []online.WriteOwnerResult
```

Rules:

- Always call the `ownerNodeID` argument exactly.
- Use `onlinerpc.RPCOnlineWriteOwnerBatch`.
- Clone outbound payload bytes during conversion.
- For RPC or decode error, return one temporary-unavailable owner result per
  input item and preserve each input route.

- [ ] **Step 4: Run owner tests**

Run:

```bash
go test ./internalv2/infra/cluster -run 'TestOnlineOwnerClient' -count=1
```

Expected: PASS.

- [ ] **Step 5: Update infra FLOW**

Update `internalv2/infra/cluster/FLOW.md` with:

```text
online.App
  -> online.Router / AuthorityPeerClient / OwnerClient ports
  -> internalv2/infra/cluster OnlineRouter / OnlineAuthorityPeerClient / OnlineOwnerClient
  -> pkg/clusterv2 RouteKey / CallRPC
```

Mention that the authority peer client receives an explicit `leaderNodeID` and
must not reroute internally.

- [ ] **Step 6: Run focused tests**

Run:

```bash
go test ./internalv2/infra/cluster ./internalv2/contracts/onlinerpc -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internalv2/infra/cluster/online.go internalv2/infra/cluster/online_test.go internalv2/infra/cluster/FLOW.md
git commit -m "feat(internalv2): add online owner rpc client"
```

## Batch 5 Verification

Run:

```bash
go test ./internalv2/infra/cluster ./internalv2/contracts/onlinerpc -count=1
```

Expected: PASS.

## Batch 5 Acceptance

- `OnlineRouter` maps clusterv2 `RouteKey` results into `online.UIDRoute`.
- `OnlineAuthorityPeerClient` calls only the explicit `leaderNodeID`.
- `OnlineOwnerClient` calls only the explicit `ownerNodeID`.
- RPC/decode failures are converted to temporary-unavailable write results.
- Default `internalv2/app.New` wiring remains unchanged.
