# internalv2 Connection Routing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement internalv2 connection addressing where any owner node can accept a client connection and the UID Slot leader owns the authoritative virtual route.

**Architecture:** Add the missing platform seams first: clusterv2 node RPC / route-authority observation and gateway activation rollback / physical close. Then build internalv2 runtime registries, presence usecase orchestration, node RPC adapters, cluster infra adapters, gateway activation mapping, app wiring, and bounded authority-change rehydrate.

**Tech Stack:** Go, `pkg/gateway`, `pkg/clusterv2`, `internalv2`, WKProto frames, table-driven unit tests, package import-boundary tests.

---

## Scope Notes

This plan covers the complete first implementation of the approved design in [2026-06-01-internalv2-connection-routing-design.md](/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/docs/superpowers/specs/2026-06-01-internalv2-connection-routing-design.md). Realtime owner-write delivery remains out of scope; the plan only defines its result contract and lazy-prune entry points.

The first two tasks are platform foundations. Do not start internalv2 presence work before they land, because otherwise later tasks will depend on private clusterv2 fields or incomplete gateway lifecycle semantics.

## File Structure

Platform foundation:

- Modify `pkg/clusterv2/api.go`: add public node RPC and route-authority DTOs.
- Modify `pkg/clusterv2/node.go`: add pending RPC handler storage and route-authority watcher fields.
- Modify `pkg/clusterv2/node_defaults.go`: register pending RPC handlers once default transport exists.
- Modify `pkg/clusterv2/node_snapshot.go`: publish route-authority events when route tables or Slot leaders change.
- Modify `pkg/clusterv2/default_slot_leaders.go`: publish route-authority events after leader refresh.
- Modify `pkg/clusterv2/node_lifecycle.go`: close watcher channels on stop.
- Modify `pkg/clusterv2/net/ids.go`: reserve presence RPC service IDs.
- Test `pkg/clusterv2/node_rpc_test.go`, `pkg/clusterv2/route_authority_test.go`, and `pkg/clusterv2/net/ids_test.go`.

Gateway foundation:

- Modify `pkg/gateway/types/event.go`: add activation rollback interface and physical close function on `Context`.
- Modify `pkg/gateway/event.go`: export the new interface alias.
- Modify `pkg/gateway/core/dispatcher.go`: populate the physical close function in gateway contexts.
- Modify `pkg/gateway/core/server.go`: call rollback when CONNACK write fails after successful activation.
- Test `pkg/gateway/core/server_test.go`.
- Update `pkg/gateway/FLOW.md`.

internalv2 runtime and usecase:

- Create `internalv2/runtime/online/`: owner-local sharded session registry.
- Create `internalv2/runtime/presence/`: authority sharded route directory with epochs, pending route commit, and owner sequence fencing.
- Create `internalv2/usecase/presence/`: entry-agnostic activation/deactivation/query/rehydrate orchestration and ports.
- Test `internalv2/runtime/online`, `internalv2/runtime/presence`, and `internalv2/usecase/presence`.

internalv2 adapters and wiring:

- Create `internalv2/access/node/`: presence RPC codec and adapter.
- Extend `internalv2/infra/cluster/`: presence authority client and route target resolver.
- Extend `internalv2/access/gateway/`: presence activation/deactivation mapping.
- Extend `internalv2/app/`: owner boot ID, presence wiring, node RPC registration, and rehydrate worker lifecycle.
- Update internalv2 FLOW docs and `AGENTS.md` directory structure.

## Task 1: clusterv2 Node RPC and Route-Authority Surface

**Files:**
- Modify: `pkg/clusterv2/api.go`
- Modify: `pkg/clusterv2/node.go`
- Modify: `pkg/clusterv2/node_defaults.go`
- Modify: `pkg/clusterv2/node_snapshot.go`
- Modify: `pkg/clusterv2/default_slot_leaders.go`
- Modify: `pkg/clusterv2/node_lifecycle.go`
- Modify: `pkg/clusterv2/net/ids.go`
- Create: `pkg/clusterv2/node_rpc_test.go`
- Create: `pkg/clusterv2/route_authority_test.go`
- Create: `pkg/clusterv2/net/ids_test.go`

- [ ] **Step 1: Write failing RPC service ID uniqueness test**

Create `pkg/clusterv2/net/ids_test.go`:

```go
package clusternet

import "testing"

func TestRPCServiceIDsAreUniqueAndNonZero(t *testing.T) {
	ids := map[string]uint8{
		"slot_forward_propose": RPCSlotForwardPropose,
		"channel_pull":         RPCChannelPull,
		"channel_ack":          RPCChannelAck,
		"channel_pull_hint":    RPCChannelPullHint,
		"channel_notify":       RPCChannelNotify,
		"control_state_sync":   RPCControlStateSync,
		"control_report_node":  RPCControlReportNode,
		"control_report_slots": RPCControlReportSlots,
		"channel_append":       RPCChannelAppend,
		"channel_append_batch": RPCChannelAppendBatch,
		"control_raft":         RPCControlRaft,
		"presence_authority":   RPCPresenceAuthority,
		"presence_owner":       RPCPresenceOwner,
	}
	seen := make(map[uint8]string, len(ids))
	for name, id := range ids {
		if id == 0 {
			t.Fatalf("%s service id is zero", name)
		}
		if prev, ok := seen[id]; ok {
			t.Fatalf("%s and %s share service id %d", prev, name, id)
		}
		seen[id] = name
	}
}
```

- [ ] **Step 2: Run service ID test and verify it fails**

Run:

```bash
go test ./pkg/clusterv2/net -run TestRPCServiceIDsAreUniqueAndNonZero -count=1
```

Expected: FAIL with `undefined: RPCPresenceAuthority`.

- [ ] **Step 3: Reserve presence RPC IDs**

Modify `pkg/clusterv2/net/ids.go` after `RPCControlRaft`:

```go
	// RPCPresenceAuthority serves internalv2 UID connection authority requests.
	RPCPresenceAuthority
	// RPCPresenceOwner serves internalv2 owner-node connection actions.
	RPCPresenceOwner
```

- [ ] **Step 4: Run service ID test and verify it passes**

Run:

```bash
go test ./pkg/clusterv2/net -run TestRPCServiceIDsAreUniqueAndNonZero -count=1
```

Expected: PASS.

- [ ] **Step 5: Write failing node RPC public surface test**

Create `pkg/clusterv2/node_rpc_test.go`:

```go
package clusterv2

import (
	"context"
	"testing"

	clusternet "github.com/WuKongIM/WuKongIM/pkg/clusterv2/net"
)

func TestNodeRegisterRPCStoresHandlerBeforeDefaultTransportExists(t *testing.T) {
	node, err := New(Config{NodeID: 1, ListenAddr: "127.0.0.1:0", DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	handler := clusternet.HandlerFunc(func(context.Context, []byte) ([]byte, error) {
		return []byte("ok"), nil
	})
	node.RegisterRPC(clusternet.RPCPresenceAuthority, handler)
	if got := len(node.pendingRPCHandlers); got != 1 {
		t.Fatalf("pendingRPCHandlers len = %d, want 1", got)
	}
}

func TestNodeCallRPCRequiresStartedTransport(t *testing.T) {
	node, err := New(Config{NodeID: 1, ListenAddr: "127.0.0.1:0", DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = node.CallRPC(context.Background(), 2, clusternet.RPCPresenceAuthority, []byte("ping"))
	if err == nil {
		t.Fatal("CallRPC() error = nil, want not started")
	}
}
```

- [ ] **Step 6: Run node RPC tests and verify they fail**

Run:

```bash
go test ./pkg/clusterv2 -run 'TestNode(RegisterRPCStoresHandlerBeforeDefaultTransportExists|CallRPCRequiresStartedTransport)' -count=1
```

Expected: FAIL with `node.RegisterRPC undefined` and `node.CallRPC undefined`.

- [ ] **Step 7: Add public node RPC surface**

Modify `pkg/clusterv2/api.go`:

```go
// NodeRPCHandler handles one clusterv2 node RPC payload.
type NodeRPCHandler interface {
	HandleRPC(context.Context, []byte) ([]byte, error)
}
```

Modify imports in `pkg/clusterv2/api.go` to include `context`.

Modify `pkg/clusterv2/node.go`:

```go
	pendingRPCHandlers map[uint8]clusternet.Handler
```

Add methods in `pkg/clusterv2/node.go`:

```go
// RegisterRPC registers a node RPC handler on the default clusterv2 transport.
func (n *Node) RegisterRPC(serviceID uint8, handler NodeRPCHandler) {
	if n == nil || serviceID == 0 || handler == nil {
		return
	}
	wrapped := clusternet.HandlerFunc(handler.HandleRPC)
	n.mu.Lock()
	if n.pendingRPCHandlers == nil {
		n.pendingRPCHandlers = make(map[uint8]clusternet.Handler)
	}
	n.pendingRPCHandlers[serviceID] = wrapped
	server := n.transportServer
	n.mu.Unlock()
	if server != nil {
		server.Register(serviceID, wrapped)
	}
}

// CallRPC invokes a node RPC service on a peer node.
func (n *Node) CallRPC(ctx context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	if err := n.ensureForeground(); err != nil {
		return nil, err
	}
	n.mu.RLock()
	client := n.transportClient
	n.mu.RUnlock()
	if client == nil {
		return nil, ErrNotStarted
	}
	return client.Call(ctx, nodeID, serviceID, payload)
}
```

Modify `pkg/clusterv2/node_defaults.go` after `ensureDefaultTransport()` creates the server:

```go
func (n *Node) registerPendingRPCHandlers() {
	if n == nil || n.transportServer == nil {
		return
	}
	n.mu.RLock()
	handlers := make(map[uint8]clusternet.Handler, len(n.pendingRPCHandlers))
	for serviceID, handler := range n.pendingRPCHandlers {
		handlers[serviceID] = handler
	}
	n.mu.RUnlock()
	for serviceID, handler := range handlers {
		n.transportServer.Register(serviceID, handler)
	}
}
```

Call `n.registerPendingRPCHandlers()` from `ensureDefaultRuntime()` immediately after `ensureDefaultTransport()` succeeds.

- [ ] **Step 8: Run node RPC tests and verify they pass**

Run:

```bash
go test ./pkg/clusterv2 -run 'TestNode(RegisterRPCStoresHandlerBeforeDefaultTransportExists|CallRPCRequiresStartedTransport)' -count=1
```

Expected: PASS.

- [ ] **Step 9: Write failing route authority watcher test**

Create `pkg/clusterv2/route_authority_test.go`:

```go
package clusterv2

import "testing"

func TestWatchRouteAuthoritiesReceivesLeadershipChange(t *testing.T) {
	node := &Node{cfg: Config{NodeID: 1}}
	watch := node.WatchRouteAuthorities()
	node.publishRouteAuthority(RouteAuthority{
		HashSlot:       7,
		SlotID:         2,
		LeaderNodeID:   1,
		RouteRevision:  11,
		AuthorityEpoch: 1,
	})
	select {
	case ev := <-watch:
		if len(ev.Authorities) != 1 {
			t.Fatalf("authorities len = %d, want 1", len(ev.Authorities))
		}
		got := ev.Authorities[0]
		if got.HashSlot != 7 || got.SlotID != 2 || got.LeaderNodeID != 1 || got.RouteRevision != 11 || got.AuthorityEpoch != 1 {
			t.Fatalf("authority = %#v", got)
		}
	default:
		t.Fatal("expected route authority event")
	}
}
```

- [ ] **Step 10: Run route authority watcher test and verify it fails**

Run:

```bash
go test ./pkg/clusterv2 -run TestWatchRouteAuthoritiesReceivesLeadershipChange -count=1
```

Expected: FAIL with undefined `WatchRouteAuthorities`, `publishRouteAuthority`, or `RouteAuthority`.

- [ ] **Step 11: Add route authority DTOs and watcher**

Modify `pkg/clusterv2/api.go`:

```go
// RouteAuthority identifies the current authority for one logical hash slot.
type RouteAuthority struct {
	HashSlot       uint16
	SlotID         uint32
	LeaderNodeID   uint64
	RouteRevision  uint64
	AuthorityEpoch uint64
}

// RouteAuthorityEvent reports route authority changes.
type RouteAuthorityEvent struct {
	Authorities []RouteAuthority
}
```

Modify `pkg/clusterv2/node.go` fields:

```go
	routeAuthorityWatchers []chan RouteAuthorityEvent
	routeAuthorityEpochs   map[uint16]uint64
```

Add methods in `pkg/clusterv2/node.go`:

```go
// WatchRouteAuthorities returns a buffered stream of route authority changes.
func (n *Node) WatchRouteAuthorities() <-chan RouteAuthorityEvent {
	ch := make(chan RouteAuthorityEvent, 16)
	if n == nil {
		close(ch)
		return ch
	}
	n.mu.Lock()
	n.routeAuthorityWatchers = append(n.routeAuthorityWatchers, ch)
	n.mu.Unlock()
	return ch
}

func (n *Node) publishRouteAuthority(authorities ...RouteAuthority) {
	if n == nil || len(authorities) == 0 {
		return
	}
	event := RouteAuthorityEvent{Authorities: append([]RouteAuthority(nil), authorities...)}
	n.mu.RLock()
	watchers := append([]chan RouteAuthorityEvent(nil), n.routeAuthorityWatchers...)
	n.mu.RUnlock()
	for _, watcher := range watchers {
		select {
		case watcher <- event:
		default:
		}
	}
}
```

- [ ] **Step 12: Run route authority watcher test and verify it passes**

Run:

```bash
go test ./pkg/clusterv2 -run TestWatchRouteAuthoritiesReceivesLeadershipChange -count=1
```

Expected: PASS.

- [ ] **Step 13: Wire authority event publishing into route updates**

Modify `pkg/clusterv2/node_snapshot.go` after router updates to compute authorities for changed hash slots. Use the current route table and publish one `RouteAuthority` per hash slot whose `(SlotID, LeaderNodeID, Revision)` changed. Increment `AuthorityEpoch` whenever local `NodeID` becomes leader for that hash slot.

Modify `pkg/clusterv2/default_slot_leaders.go` so `refreshDefaultSlotLeaders()` publishes authority events after `UpdateSlotLeaders` changes local leadership.

Add a focused test in `pkg/clusterv2/route_authority_test.go`:

```go
func TestRouteAuthorityEpochIncrementsWhenLocalNodeBecomesAuthorityAgain(t *testing.T) {
	node := &Node{cfg: Config{NodeID: 1}, routeAuthorityEpochs: map[uint16]uint64{}}
	first := node.nextAuthorityEpoch(3, 1)
	second := node.nextAuthorityEpoch(3, 1)
	if first != 1 || second != 2 {
		t.Fatalf("epochs = %d,%d want 1,2", first, second)
	}
}
```

- [ ] **Step 14: Run clusterv2 focused tests**

Run:

```bash
go test ./pkg/clusterv2 ./pkg/clusterv2/net -run 'Test(Node|RouteAuthority|RPCServiceIDs)' -count=1
```

Expected: PASS.

- [ ] **Step 15: Commit clusterv2 foundation**

Run:

```bash
git add pkg/clusterv2 pkg/clusterv2/net
git commit -m "feat: expose clusterv2 node rpc authority surface"
```

## Task 2: Gateway Activation Rollback and Physical Close Capability

**Files:**
- Modify: `pkg/gateway/types/event.go`
- Modify: `pkg/gateway/event.go`
- Modify: `pkg/gateway/core/dispatcher.go`
- Modify: `pkg/gateway/core/server.go`
- Modify: `pkg/gateway/core/server_test.go`
- Modify: `pkg/gateway/FLOW.md`

- [ ] **Step 1: Write failing rollback test**

Add to the WKProto auth tests in `pkg/gateway/core/server_test.go`:

```go
func TestWKProtoActivationRollbackRunsWhenSuccessConnackWriteFails(t *testing.T) {
	handler := newTestHandler()
	handler.onActivate = func(*gateway.Context) (*frame.ConnackPacket, error) {
		return nil, nil
	}
	var rollbackCalled bool
	handler.onActivateRollback = func(ctx gateway.Context, err error) {
		rollbackCalled = true
		if ctx.Session == nil {
			t.Fatal("rollback context missing session")
		}
		if err == nil {
			t.Fatal("rollback err = nil, want connack write error")
		}
	}
	proto := newScriptedProtocol("wkproto")
	proto.encodeFn = func(session.Session, frame.Frame, session.OutboundMeta) ([]byte, error) {
		return nil, errors.New("encode connack failed")
	}
	proto.pushDecode(decodeResult{
		frames: []frame.Frame{&frame.ConnectPacket{UID: "u1", DeviceID: "d1", DeviceFlag: frame.APP}},
		consumed: 1,
	})
	srv, transportFactory := newTestServerWithAuthenticator(t, handler, proto, gateway.SessionOptions{}, gateway.NewWKProtoAuthenticator(gateway.WKProtoAuthOptions{DisableEncryption: true}))
	if err := srv.Start(); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop() })
	conn := transportFactory.MustOpen("listener-a", 1)
	transportFactory.MustData("listener-a", 1, []byte("c"))
	waitFor(t, func() bool { return connClosed(conn) && rollbackCalled })
	if got := handler.callOrder(); !reflect.DeepEqual(got, []string{"activate", "activate_rollback"}) {
		t.Fatalf("unexpected call order: %v", got)
	}
}
```

Extend `testHandler`:

```go
	onActivateRollback func(gateway.Context, error)
```

and add:

```go
func (h *testHandler) OnSessionActivateRollback(ctx gateway.Context, err error) {
	h.mu.Lock()
	h.order = append(h.order, "activate_rollback")
	h.mu.Unlock()
	if h.onActivateRollback != nil {
		h.onActivateRollback(ctx, err)
	}
}
```

- [ ] **Step 2: Run rollback test and verify it fails**

Run:

```bash
go test ./pkg/gateway/core -run TestWKProtoActivationRollbackRunsWhenSuccessConnackWriteFails -count=1
```

Expected: FAIL because `OnSessionActivateRollback` is not called or interface is undefined.

- [ ] **Step 3: Add rollback interface and context physical close**

Modify `pkg/gateway/types/event.go`:

```go
type SessionActivationRollbacker interface {
	OnSessionActivateRollback(ctx Context, err error)
}

type Context struct {
	Session        session.Session
	Listener       string
	Network        string
	Transport      string
	Protocol       string
	CloseReason    CloseReason
	ReplyToken     string
	RequestContext context.Context
	CloseSessionFn func(CloseReason, error)
}

func (ctx *Context) CloseSession(reason CloseReason, err error) error {
	if ctx == nil {
		return session.ErrSessionClosed
	}
	if ctx.CloseSessionFn != nil {
		ctx.CloseSessionFn(reason, err)
		return nil
	}
	if ctx.Session == nil {
		return session.ErrSessionClosed
	}
	return ctx.Session.Close()
}
```

Modify `pkg/gateway/event.go`:

```go
type SessionActivationRollbacker = gatewaytypes.SessionActivationRollbacker
```

Modify `pkg/gateway/core/dispatcher.go` in `context(...)` to set `CloseSessionFn`:

```go
CloseSessionFn: func(closeReason gatewaytypes.CloseReason, closeErr error) {
	state.close(closeReason, closeErr)
},
```

- [ ] **Step 4: Call rollback on post-activation CONNACK write failure**

Modify `pkg/gateway/core/server.go` in the successful CONNACK write failure branch after activation:

```go
if writeErr := s.writeImmediateFrame(state, connack); writeErr != nil {
	if rollbacker, ok := s.options.Handler.(gatewaytypes.SessionActivationRollbacker); ok {
		rollbacker.OnSessionActivateRollback(ctx, writeErr)
	}
	state.close(closeReasonForError(writeErr, gatewaytypes.CloseReasonPeerClosed), writeErr)
	return true, nil
}
```

Do not call rollback when authentication fails before activation, or when activation itself returns an error before any route was accepted.

- [ ] **Step 5: Run gateway rollback test**

Run:

```bash
go test ./pkg/gateway/core -run TestWKProtoActivationRollbackRunsWhenSuccessConnackWriteFails -count=1
```

Expected: PASS.

- [ ] **Step 6: Add physical close context test**

Add to `pkg/gateway/core/dispatcher_test.go`:

```go
func TestDispatcherContextCloseSessionPhysicallyClosesState(t *testing.T) {
	state := &sessionState{closedCh: make(chan struct{})}
	srv := &Server{}
	state.server = srv
	ctx := dispatcher{}.context(state, "", gatewaytypes.CloseReasonPolicyViolation, context.Background())
	if err := ctx.CloseSession(gatewaytypes.CloseReasonPolicyViolation, errors.New("close requested")); err != nil {
		t.Fatalf("CloseSession() error = %v", err)
	}
	select {
	case <-state.closedCh:
	default:
		t.Fatal("CloseSession did not close session state")
	}
}
```

- [ ] **Step 7: Run gateway core tests**

Run:

```bash
go test ./pkg/gateway/core -count=1
```

Expected: PASS.

- [ ] **Step 8: Update gateway FLOW**

Modify `pkg/gateway/FLOW.md` to document:

- `SessionActivationRollbacker`
- CONNACK write failure after activation calls rollback
- `Context.CloseSession` is the physical close path for owner route actions

- [ ] **Step 9: Commit gateway foundation**

Run:

```bash
git add pkg/gateway
git commit -m "feat: add gateway activation rollback"
```

## Task 3: internalv2 Owner-Local Online Registry

**Files:**
- Create: `internalv2/runtime/online/types.go`
- Create: `internalv2/runtime/online/registry.go`
- Create: `internalv2/runtime/online/FLOW.md`
- Create: `internalv2/runtime/online/registry_test.go`

- [ ] **Step 1: Write registry tests**

Create `internalv2/runtime/online/registry_test.go` with these tests:

```go
package online

import "testing"

func TestRegistryPendingActiveClosingLifecycle(t *testing.T) {
	reg := NewRegistry(RegistryOptions{ShardCount: 4})
	conn := OnlineConn{UID: "u1", HashSlot: 3, OwnerBootID: 9, SessionID: 11}
	if err := reg.RegisterPending(conn); err != nil {
		t.Fatalf("RegisterPending() error = %v", err)
	}
	if got, ok := reg.Connection(11); !ok || got.State != RouteStatePending {
		t.Fatalf("pending connection = %#v,%v", got, ok)
	}
	if err := reg.MarkActive(11); err != nil {
		t.Fatalf("MarkActive() error = %v", err)
	}
	if got, ok := reg.Connection(11); !ok || got.State != RouteStateActive {
		t.Fatalf("active connection = %#v,%v", got, ok)
	}
	if got, ok := reg.MarkClosingAndUnregister(11); !ok || got.State != RouteStateClosing {
		t.Fatalf("closing connection = %#v,%v", got, ok)
	}
	if _, ok := reg.Connection(11); ok {
		t.Fatal("connection still indexed after unregister")
	}
}

func TestVisitActiveByHashSlotPagesWithoutSortingWholeBucket(t *testing.T) {
	reg := NewRegistry(RegistryOptions{ShardCount: 4})
	for i := uint64(1); i <= 5; i++ {
		if err := reg.RegisterPending(OnlineConn{UID: "u", HashSlot: 2, OwnerBootID: 1, SessionID: i}); err != nil {
			t.Fatalf("RegisterPending(%d): %v", i, err)
		}
		if err := reg.MarkActive(i); err != nil {
			t.Fatalf("MarkActive(%d): %v", i, err)
		}
	}
	var first []uint64
	cursor, more := reg.VisitActiveByHashSlot(2, RouteCursor{}, 2, func(conn OnlineConn) bool {
		first = append(first, conn.SessionID)
		return true
	})
	if !more || len(first) != 2 {
		t.Fatalf("first page ids=%v more=%v, want len 2 and more", first, more)
	}
	var second []uint64
	_, _ = reg.VisitActiveByHashSlot(2, cursor, 10, func(conn OnlineConn) bool {
		second = append(second, conn.SessionID)
		return true
	})
	if len(second) != 3 {
		t.Fatalf("second page len = %d, want 3", len(second))
	}
}
```

- [ ] **Step 2: Run owner registry tests and verify they fail**

Run:

```bash
go test ./internalv2/runtime/online -count=1
```

Expected: FAIL because the package does not exist.

- [ ] **Step 3: Add owner registry types**

Create `internalv2/runtime/online/types.go`:

```go
package online

import (
	"errors"
	"time"

	coregateway "github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

var ErrInvalidConnection = errors.New("internalv2/runtime/online: invalid connection")
var ErrConnectionNotFound = errors.New("internalv2/runtime/online: connection not found")

type RouteState uint8

const (
	RouteStatePending RouteState = iota
	RouteStateActive
	RouteStateClosing
)

type OnlineConn struct {
	UID          string
	HashSlot     uint16
	OwnerBootID  uint64
	OwnerSeq     uint64
	SessionID    uint64
	DeviceID     string
	DeviceFlag   frame.DeviceFlag
	DeviceLevel  frame.DeviceLevel
	Listener     string
	ConnectedAt  time.Time
	State        RouteState
	Session      coregateway.Context
}

type RouteCursor struct {
	LastSessionID uint64
}

type RegistryOptions struct {
	ShardCount int
}
```

- [ ] **Step 4: Implement sharded registry**

Create `internalv2/runtime/online/registry.go` with a sharded map by session ID and hash slot. Implement:

```go
type Registry struct {
	shards []registryShard
}

func NewRegistry(opts RegistryOptions) *Registry
func (r *Registry) RegisterPending(conn OnlineConn) error
func (r *Registry) MarkActive(sessionID uint64) error
func (r *Registry) MarkClosingAndUnregister(sessionID uint64) (OnlineConn, bool)
func (r *Registry) Connection(sessionID uint64) (OnlineConn, bool)
func (r *Registry) VisitActiveByHashSlot(hashSlot uint16, cursor RouteCursor, limit int, fn func(OnlineConn) bool) (RouteCursor, bool)
```

Validation rules:

- reject empty UID
- reject zero session ID
- default shard count to 32
- `VisitActiveByHashSlot` skips pending and closing connections
- `VisitActiveByHashSlot` returns at most `limit` routes and does not sort the entire bucket

- [ ] **Step 5: Run owner registry tests**

Run:

```bash
go test ./internalv2/runtime/online -count=1
```

Expected: PASS.

- [ ] **Step 6: Add FLOW**

Create `internalv2/runtime/online/FLOW.md`:

```markdown
# internalv2/runtime/online Flow

## Responsibility

`internalv2/runtime/online` owns node-local real gateway sessions for internalv2 connection routing. It is not a distributed directory.

## Lifecycle

`RegisterPending` is used before CONNACK succeeds. `MarkActive` promotes a session after authority registration and local active re-check. `MarkClosingAndUnregister` removes local indexes before authority unregister tombstones are queued.

## Rehydrate

`VisitActiveByHashSlot` pages active routes for bounded authority-change rehydrate without copying or sorting an entire hash-slot bucket.
```

- [ ] **Step 7: Commit owner registry**

Run:

```bash
git add internalv2/runtime/online
git commit -m "feat: add internalv2 online registry"
```

## Task 4: internalv2 Authority Presence Directory

**Files:**
- Create: `internalv2/runtime/presence/types.go`
- Create: `internalv2/runtime/presence/directory.go`
- Create: `internalv2/runtime/presence/FLOW.md`
- Create: `internalv2/runtime/presence/directory_test.go`

- [ ] **Step 1: Write authority directory tests**

Create `internalv2/runtime/presence/directory_test.go` with tests for:

```go
func TestDirectoryRejectsStaleTarget(t *testing.T)
func TestDirectoryUnregisterWithNewerOwnerSeqPreventsStaleRehydrate(t *testing.T)
func TestDirectoryConflictActionFailureLeavesExistingRouteActive(t *testing.T)
func TestDirectoryLeadershipEpochClearsOldRoutes(t *testing.T)
```

Use this core assertion in the conflict test:

```go
existing := Route{UID: "u1", OwnerNodeID: 1, OwnerBootID: 1, OwnerSeq: 1, SessionID: 10, DeviceID: "old", DeviceFlag: 1, DeviceLevel: 1}
incoming := Route{UID: "u1", OwnerNodeID: 2, OwnerBootID: 1, OwnerSeq: 1, SessionID: 20, DeviceID: "new", DeviceFlag: 1, DeviceLevel: 1}
dir := NewDirectory(DirectoryOptions{ShardCount: 4})
target := RouteTarget{HashSlot: 1, SlotID: 1, LeaderNodeID: 1, RouteRevision: 1, AuthorityEpoch: 1}
dir.BecomeAuthority(target)
if _, err := dir.RegisterRoute(target, existing); err != nil {
	t.Fatalf("RegisterRoute(existing) error = %v", err)
}
res, err := dir.RegisterRoute(target, incoming)
if err != nil {
	t.Fatalf("RegisterRoute(incoming) error = %v", err)
}
if len(res.Actions) != 1 || res.PendingToken == "" {
	t.Fatalf("register result = %#v, want one action and pending token", res)
}
if err := dir.AbortRoute(target, res.PendingToken); err != nil {
	t.Fatalf("AbortRoute() error = %v", err)
}
routes, err := dir.EndpointsByUID(target, "u1")
if err != nil {
	t.Fatalf("EndpointsByUID() error = %v", err)
}
if len(routes) != 1 || routes[0].SessionID != existing.SessionID {
	t.Fatalf("routes = %#v, want existing active route", routes)
}
```

- [ ] **Step 2: Run authority directory tests and verify they fail**

Run:

```bash
go test ./internalv2/runtime/presence -count=1
```

Expected: FAIL because the package does not exist.

- [ ] **Step 3: Add authority DTOs**

Create `internalv2/runtime/presence/types.go` with:

```go
package presence

import "errors"

var ErrNotLeader = errors.New("internalv2/runtime/presence: not leader")
var ErrStaleRoute = errors.New("internalv2/runtime/presence: stale route")
var ErrRouteNotReady = errors.New("internalv2/runtime/presence: route not ready")

type RouteTarget struct {
	HashSlot       uint16
	SlotID         uint32
	LeaderNodeID   uint64
	RouteRevision  uint64
	AuthorityEpoch uint64
}

type Route struct {
	UID           string
	OwnerNodeID   uint64
	OwnerBootID   uint64
	OwnerSeq      uint64
	SessionID     uint64
	DeviceID      string
	DeviceFlag    uint8
	DeviceLevel   uint8
	Listener      string
	ConnectedUnix int64
}

type RouteAction struct {
	UID         string
	OwnerNodeID uint64
	OwnerBootID uint64
	SessionID   uint64
	Kind        string
	Reason      string
	DelayMS     int64
}

type RouteIdentity struct {
	UID         string
	OwnerNodeID uint64
	OwnerBootID uint64
	SessionID   uint64
}

type PendingRouteToken string

type RegisterResult struct {
	PendingToken PendingRouteToken
	Actions      []RouteAction
}

type RehydrateResult struct {
	Route    RouteIdentity
	Accepted bool
	Actions  []RouteAction
	Error    string
}
```

- [ ] **Step 4: Implement sharded authority directory**

Create `internalv2/runtime/presence/directory.go`. Implement:

```go
type Directory struct {
	localNodeID uint64
	shards      []directoryShard
}

func NewDirectory(opts DirectoryOptions) *Directory
func (d *Directory) BecomeAuthority(target RouteTarget)
func (d *Directory) LoseAuthority(hashSlot uint16)
func (d *Directory) RegisterRoute(target RouteTarget, route Route) (RegisterResult, error)
func (d *Directory) CommitRoute(target RouteTarget, token PendingRouteToken) error
func (d *Directory) AbortRoute(target RouteTarget, token PendingRouteToken) error
func (d *Directory) UnregisterRoute(target RouteTarget, identity RouteIdentity, ownerSeq uint64) error
func (d *Directory) EndpointsByUID(target RouteTarget, uid string) ([]Route, error)
func (d *Directory) RehydrateRoutes(target RouteTarget, routes []Route) ([]RehydrateResult, error)
```

Implementation rules:

- validate `RouteTarget` before every operation
- store active and pending routes separately
- store last owner sequence per route identity
- reject any route whose `OwnerSeq` is older than the last tombstone
- when conflicts exist, return a pending token and do not remove active conflicting routes until `CommitRoute`
- `AbortRoute` deletes only the pending route
- `LoseAuthority` clears or quarantines the hash-slot shard for the old epoch
- `RehydrateRoutes` uses the same conflict path as `RegisterRoute`

- [ ] **Step 5: Run authority directory tests**

Run:

```bash
go test ./internalv2/runtime/presence -count=1
```

Expected: PASS.

- [ ] **Step 6: Add FLOW**

Create `internalv2/runtime/presence/FLOW.md` documenting authority epochs, target fencing, pending conflict routes, owner sequence tombstones, and no lease/digest/replay.

- [ ] **Step 7: Commit authority directory**

Run:

```bash
git add internalv2/runtime/presence
git commit -m "feat: add internalv2 presence authority directory"
```

## Task 5: internalv2 Presence Usecase

**Files:**
- Create: `internalv2/usecase/presence/types.go`
- Create: `internalv2/usecase/presence/ports.go`
- Create: `internalv2/usecase/presence/app.go`
- Create: `internalv2/usecase/presence/activate.go`
- Create: `internalv2/usecase/presence/deactivate.go`
- Create: `internalv2/usecase/presence/lookup.go`
- Create: `internalv2/usecase/presence/import_boundary_test.go`
- Create: `internalv2/usecase/presence/app_test.go`
- Create: `internalv2/usecase/presence/FLOW.md`

- [ ] **Step 1: Write import boundary test**

Create `internalv2/usecase/presence/import_boundary_test.go` using the same parser pattern as `internalv2/usecase/message/import_boundary_test.go`, with forbidden imports:

```go
forbidden := []string{
	"github.com/WuKongIM/WuKongIM/pkg/gateway",
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame",
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2",
	"github.com/WuKongIM/WuKongIM/internalv2/access",
	"github.com/WuKongIM/WuKongIM/internalv2/app",
}
```

- [ ] **Step 2: Write usecase tests**

Create `internalv2/usecase/presence/app_test.go` with tests:

```go
func TestActivateRegistersPendingLocalRouteThenAuthorityThenMarksActive(t *testing.T)
func TestActivateRollsBackLocalRouteWhenAuthorityRegisterFails(t *testing.T)
func TestActivateUnregistersAuthorityWhenSessionClosedDuringActivation(t *testing.T)
func TestDeactivateRemovesLocalRouteAndQueuesAuthorityTombstone(t *testing.T)
func TestEndpointsByUIDUsesAuthorityClient(t *testing.T)
```

Use fake ports. The activation success test should assert call order:

```go
want := []string{"local.register_pending", "authority.register", "local.mark_active"}
```

- [ ] **Step 3: Run usecase tests and verify they fail**

Run:

```bash
go test ./internalv2/usecase/presence -count=1
```

Expected: FAIL because package or methods are missing.

- [ ] **Step 4: Add usecase DTOs and ports**

Create `types.go` and `ports.go` with entry-agnostic types:

```go
// In types.go, import internalv2/runtime/presence as authority.
type ActivateCommand struct {
	UID          string
	DeviceID     string
	DeviceFlag   uint8
	DeviceLevel  uint8
	Listener     string
	ConnectedUnix int64
	SessionID     uint64
	Session       SessionHandle
}

type DeactivateCommand struct {
	UID       string
	SessionID uint64
}

type SessionHandle interface {
	CloseSession(reason string) error
}

type RouteTarget = authority.RouteTarget
type Route = authority.Route
type RouteIdentity = authority.RouteIdentity
type RouteAction = authority.RouteAction
type PendingRouteToken = authority.PendingRouteToken
type RegisterResult = authority.RegisterResult
type RehydrateResult = authority.RehydrateResult

type LocalRegistry interface {
	RegisterPending(OnlineConn) error
	MarkActive(sessionID uint64) error
	MarkClosingAndUnregister(sessionID uint64) (OnlineConn, bool)
	Connection(sessionID uint64) (OnlineConn, bool)
}

type AuthorityClient interface {
	RegisterRoute(context.Context, Route) (RegisterResult, error)
	CommitRoute(context.Context, PendingRouteToken) error
	AbortRoute(context.Context, PendingRouteToken) error
	EnqueueUnregister(RouteIdentity, uint64)
	EndpointsByUID(context.Context, string) ([]Route, error)
}
```

- [ ] **Step 5: Implement `App.Activate`, `Deactivate`, and lookup**

Implement:

```go
func New(opts Options) *App
func (a *App) Activate(ctx context.Context, cmd ActivateCommand) error
func (a *App) Deactivate(ctx context.Context, cmd DeactivateCommand) error
func (a *App) EndpointsByUID(ctx context.Context, uid string) ([]Route, error)
```

Rules:

- activation registers local pending first
- authority registration errors rollback local state
- pending conflict actions are applied before `CommitRoute`
- local `MarkActive` happens only after authority success and local active re-check
- if active re-check fails, unregister exact authority route
- deactivation removes local route first and enqueues unregister tombstone

- [ ] **Step 6: Run usecase tests**

Run:

```bash
go test ./internalv2/usecase/presence -count=1
```

Expected: PASS.

- [ ] **Step 7: Add FLOW and commit usecase**

Create `internalv2/usecase/presence/FLOW.md` with activate/deactivate/query flows and import-boundary rules.

Run:

```bash
git add internalv2/usecase/presence
git commit -m "feat: add internalv2 presence usecase"
```

## Task 6: internalv2 Node RPC Adapter and Codec

**Files:**
- Create: `internalv2/access/node/FLOW.md`
- Create: `internalv2/access/node/presence_rpc.go`
- Create: `internalv2/access/node/presence_codec.go`
- Create: `internalv2/access/node/presence_rpc_test.go`
- Create: `internalv2/access/node/presence_codec_test.go`

- [ ] **Step 1: Write codec round-trip tests**

Create tests for binary round trip of:

- `RegisterRoute(RouteTarget, Route)`
- `CommitRoute(RouteTarget, PendingRouteToken)`
- `UnregisterRoute(RouteTarget, RouteIdentity, ownerSeq)`
- `EndpointsByUID(RouteTarget, uid)`
- `RehydrateRoutes(RouteTarget, []Route)`

Use magic headers:

```go
var presenceRPCRequestMagic = [...]byte{'W', 'K', 'V', 'P', 1}
var presenceRPCResponseMagic = [...]byte{'W', 'K', 'V', 'R', 1}
```

- [ ] **Step 2: Run codec tests and verify they fail**

Run:

```bash
go test ./internalv2/access/node -run Presence -count=1
```

Expected: FAIL because package files are missing.

- [ ] **Step 3: Add RPC adapter interfaces**

Create `presence_rpc.go` with:

```go
type PresenceAuthority interface {
	RegisterRoute(context.Context, presence.RouteTarget, presence.Route) (presence.RegisterResult, error)
	CommitRoute(context.Context, presence.RouteTarget, string) error
	AbortRoute(context.Context, presence.RouteTarget, string) error
	UnregisterRoute(context.Context, presence.RouteTarget, presence.RouteIdentity, uint64) error
	EndpointsByUID(context.Context, presence.RouteTarget, string) ([]presence.Route, error)
	RehydrateRoutes(context.Context, presence.RouteTarget, []presence.Route) ([]presence.RehydrateResult, error)
}

type Adapter struct {
	authority PresenceAuthority
}
```

Expose:

```go
func New(opts Options) *Adapter
func (a *Adapter) HandlePresenceAuthorityRPC(ctx context.Context, payload []byte) ([]byte, error)
```

- [ ] **Step 4: Implement binary codec and handler**

Implement deterministic binary encoding with length-delimited strings and varints. Reject malformed, truncated, and trailing bytes.

Map authority errors to response status:

- `ok`
- `not_leader`
- `stale_route`
- `route_not_ready`
- `rejected`

- [ ] **Step 5: Run node adapter tests**

Run:

```bash
go test ./internalv2/access/node -count=1
```

Expected: PASS.

- [ ] **Step 6: Add FLOW and commit node adapter**

Create `internalv2/access/node/FLOW.md` documenting codec ownership and no business policy.

Run:

```bash
git add internalv2/access/node
git commit -m "feat: add internalv2 presence node rpc"
```

## Task 7: internalv2 Cluster Presence Infra Adapter

**Files:**
- Modify: `internalv2/infra/cluster/FLOW.md`
- Create: `internalv2/infra/cluster/presence.go`
- Create: `internalv2/infra/cluster/presence_test.go`

- [ ] **Step 1: Write presence infra tests**

Create tests:

```go
func TestPresenceClientUsesLocalAuthorityWhenTargetLeaderIsLocal(t *testing.T)
func TestPresenceClientCallsRemoteAuthorityWhenTargetLeaderIsRemote(t *testing.T)
func TestPresenceClientRetriesOnceAfterStaleRoute(t *testing.T)
func TestPresenceClientReturnsRouteNotReadyWhenRouteKeyFails(t *testing.T)
```

Fake cluster interface:

```go
type fakePresenceCluster struct {
	nodeID uint64
	route  clusterv2.Route
	calls  []rpcCall
}
```

- [ ] **Step 2: Run infra tests and verify they fail**

Run:

```bash
go test ./internalv2/infra/cluster -run Presence -count=1
```

Expected: FAIL because presence adapter is missing.

- [ ] **Step 3: Add presence cluster adapter**

Create `presence.go` with:

```go
type PresenceNode interface {
	NodeID() uint64
	RouteKey(string) (clusterv2.Route, error)
	RouteHashSlot(uint16) (clusterv2.Route, error)
	CallRPC(context.Context, uint64, uint8, []byte) ([]byte, error)
	RegisterRPC(uint8, clusterv2.NodeRPCHandler)
	WatchRouteAuthorities() <-chan clusterv2.RouteAuthorityEvent
}

type PresenceAuthorityClient struct {
	node      PresenceNode
	local     accessnode.PresenceAuthority
	remote    *accessnode.Client
}
```

Implement `ResolveRouteTarget(uid string)` and authority methods by routing through `clusterv2.Route`.

- [ ] **Step 4: Run infra tests**

Run:

```bash
go test ./internalv2/infra/cluster -run Presence -count=1
```

Expected: PASS.

- [ ] **Step 5: Update FLOW and commit infra adapter**

Run:

```bash
git add internalv2/infra/cluster
git commit -m "feat: add internalv2 presence cluster adapter"
```

## Task 8: Gateway Presence Activation Adapter

**Files:**
- Modify: `internalv2/access/gateway/handler.go`
- Create: `internalv2/access/gateway/presence.go`
- Modify: `internalv2/access/gateway/handler_test.go`
- Modify: `internalv2/access/gateway/FLOW.md`

- [ ] **Step 1: Write gateway activation tests**

Add tests:

```go
func TestHandlerOnSessionActivateCallsPresenceActivate(t *testing.T)
func TestHandlerOnSessionActivateRejectsMissingUID(t *testing.T)
func TestHandlerOnSessionCloseCallsPresenceDeactivate(t *testing.T)
func TestHandlerOnSessionActivateRollbackCallsPresenceDeactivate(t *testing.T)
```

Use session values:

```go
sess.SetValue(coregateway.SessionValueUID, "u1")
sess.SetValue(coregateway.SessionValueDeviceID, "d1")
sess.SetValue(coregateway.SessionValueDeviceFlag, frame.APP)
sess.SetValue(coregateway.SessionValueDeviceLevel, frame.DeviceLevelMaster)
```

- [ ] **Step 2: Run gateway access tests and verify they fail**

Run:

```bash
go test ./internalv2/access/gateway -run 'Presence|Activate|SessionClose' -count=1
```

Expected: FAIL because handler has no presence usecase.

- [ ] **Step 3: Add presence option and lifecycle methods**

Modify `Options`:

```go
type PresenceUsecase interface {
	Activate(context.Context, presence.ActivateCommand) error
	Deactivate(context.Context, presence.DeactivateCommand) error
}

type Options struct {
	Messages MessageUsecase
	Presence PresenceUsecase
	SendTimeout time.Duration
}
```

Implement:

```go
func (h *Handler) OnSessionActivate(ctx *coregateway.Context) (*frame.ConnackPacket, error)
func (h *Handler) OnSessionClose(ctx coregateway.Context) error
func (h *Handler) OnSessionActivateRollback(ctx coregateway.Context, err error)
```

Map activation errors to a retryable `ConnackPacket{ReasonCode: frame.ReasonSystemError}` by returning the error to gateway core.

- [ ] **Step 4: Run gateway access tests**

Run:

```bash
go test ./internalv2/access/gateway -count=1
```

Expected: PASS.

- [ ] **Step 5: Update FLOW and commit gateway adapter**

Run:

```bash
git add internalv2/access/gateway
git commit -m "feat: wire internalv2 gateway presence activation"
```

## Task 9: App Wiring and Authority Rehydrate Worker

**Files:**
- Modify: `internalv2/app/app.go`
- Modify: `internalv2/app/config.go`
- Modify: `internalv2/app/lifecycle.go`
- Create: `internalv2/app/presence_rehydrate.go`
- Modify: `internalv2/app/app_test.go`
- Modify: `internalv2/app/FLOW.md`
- Modify: `internalv2/FLOW.md`

- [ ] **Step 1: Write app wiring tests**

Add tests:

```go
func TestNewWiresPresenceWhenGatewayEnabled(t *testing.T)
func TestStartOrderStartsClusterThenPresenceWorkerThenGateway(t *testing.T)
func TestPresenceRehydrateWorkerPagesAffectedHashSlot(t *testing.T)
```

The rehydrate test should use fake registry pages and fake authority changes:

```go
events := make(chan clusterv2.RouteAuthorityEvent, 1)
events <- clusterv2.RouteAuthorityEvent{Authorities: []clusterv2.RouteAuthority{{HashSlot: 9, SlotID: 1, LeaderNodeID: 1, AuthorityEpoch: 2}}}
```

- [ ] **Step 2: Run app tests and verify they fail**

Run:

```bash
go test ./internalv2/app -run 'Presence|Rehydrate|StartOrder' -count=1
```

Expected: FAIL because app has no presence wiring.

- [ ] **Step 3: Add config**

Modify `internalv2/app/config.go`:

```go
type PresenceConfig struct {
	ActivationTimeout time.Duration
	ActivationQueueSize int
	ActivationPerTargetLimit int
	RehydrateBatchSize int
	RehydrateMaxInflightPerTarget int
	UnregisterQueueSize int
}
```

Add `Presence PresenceConfig` to `Config` with English comments. Defaults:

- activation timeout: `3 * time.Second`
- activation queue: `1024`
- per target limit: `64`
- rehydrate batch: `512`
- max in-flight per target: `1`
- unregister queue: `4096`

- [ ] **Step 4: Wire presence components**

Modify `internalv2/app/app.go`:

- generate `ownerBootID` during `New`
- create `online.Registry`
- create `runtime/presence.Directory`
- create presence usecase
- create access/node adapter
- register presence RPC handlers on clusterv2 node when it supports `RegisterRPC`
- pass presence usecase to `access/gateway.New`

Add options for tests:

```go
func WithPresence(presence *presence.App) Option
func WithOnlineRegistry(reg *online.Registry) Option
```

- [ ] **Step 5: Add rehydrate worker**

Create `internalv2/app/presence_rehydrate.go` with worker that:

- watches `WatchRouteAuthorities`
- coalesces events by hash slot
- pages owner registry via `VisitActiveByHashSlot`
- calls authority `RehydrateRoutes`
- cancels old epoch work when newer authority event arrives

- [ ] **Step 6: Run app tests**

Run:

```bash
go test ./internalv2/app -run 'Presence|Rehydrate|StartOrder' -count=1
```

Expected: PASS.

- [ ] **Step 7: Update FLOW docs and commit app wiring**

Update `internalv2/FLOW.md` and `internalv2/app/FLOW.md` with presence routing flow.

Run:

```bash
git add internalv2
git commit -m "feat: wire internalv2 connection routing"
```

## Task 10: Documentation, AGENTS, and Focused Verification

**Files:**
- Modify: `AGENTS.md`
- Modify: `pkg/clusterv2/FLOW.md`
- Modify: `pkg/gateway/FLOW.md`
- Modify: `internalv2/FLOW.md`
- Modify: `internalv2/app/FLOW.md`
- Modify: `internalv2/access/gateway/FLOW.md`
- Modify: `internalv2/infra/cluster/FLOW.md`
- Create or update all new internalv2 package `FLOW.md` files from prior tasks.

- [ ] **Step 1: Update AGENTS directory structure**

Add these directories under `internalv2/`:

```text
runtime/
  online/               新架构节点内真实 gateway session 注册、状态、按 UID hash slot 分页 rehydrate
  presence/             新架构 Slot leader 内存权威连接目录、authority epoch、OwnerSeq fencing
access/
  node/                 新架构节点间 presence RPC codec/handler/client
usecase/
  presence/             新架构入口无关连接寻址编排、激活/注销/查询、冲突动作调度
```

- [ ] **Step 2: Run package verification**

Run:

```bash
go test ./pkg/clusterv2/... ./pkg/gateway/... ./internalv2/... -count=1
```

Expected: PASS.

- [ ] **Step 3: Run full unit tests if focused verification passes**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 4: Run diff check**

Run:

```bash
git diff --check
```

Expected: no output.

- [ ] **Step 5: Commit docs and verification fixes**

Run:

```bash
git add AGENTS.md pkg/clusterv2/FLOW.md pkg/gateway/FLOW.md internalv2
git commit -m "docs: document internalv2 connection routing"
```

## Self-Review Checklist

- [ ] Spec requirement: no owner lease heartbeat, route digest, or periodic mismatch replay. Covered by Tasks 3, 4, and 9.
- [ ] Spec requirement: authority directory belongs in runtime, not usecase. Covered by Task 4.
- [ ] Spec requirement: `RouteTarget` and `AuthorityEpoch` fence authority operations. Covered by Tasks 1, 4, 6, and 7.
- [ ] Spec requirement: gateway activation rollback and physical close. Covered by Task 2.
- [ ] Spec requirement: high-connection-count bounded paths. Covered by Tasks 3, 5, and 9.
- [ ] Spec requirement: device conflict pending commit/abort. Covered by Task 4 and Task 5.
- [ ] Spec requirement: rehydrate only on authority changes and with bounded pages. Covered by Task 1 and Task 9.
- [ ] Spec requirement: access/usecase/infra/app boundaries. Covered by Tasks 5, 6, 7, and 8.
