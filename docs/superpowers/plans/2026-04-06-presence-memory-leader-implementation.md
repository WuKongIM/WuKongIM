# Presence Memory Leader Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement memory-only slot-leader-authoritative presence so cross-node person-channel realtime delivery can find online sessions, preserve legacy `device_id` / `device_level` conflict semantics, and recover leader memory loss through aggregated lease replay.

**Architecture:** Keep local session truth in `internal/runtime/online`, add a new `internal/usecase/presence` package for authoritative in-memory routing and gateway-side repair orchestration, and expose node-to-node presence RPCs through `internal/access/node`. Gateway connect success must move behind a pre-ack activation hook so a successful `CONNACK` means the current authority already knows the route and conflicting replaced routes are already non-routable.

**Tech Stack:** Go 1.23, `internal/gateway`, `internal/access/gateway`, `internal/access/node`, `internal/runtime/online`, `internal/usecase/presence`, `internal/usecase/message`, `pkg/cluster/raftcluster`, `pkg/transport/nodetransport`, `testing`, `testify`.

**Spec:** `docs/superpowers/specs/2026-04-06-presence-memory-leader-design.md`

---

## Execution Notes

- Use `@superpowers/test-driven-development` on every task: failing test first, confirm failure, then add the minimum code to pass.
- Keep the AGENTS layering intact: `access -> usecase/runtime`, `usecase -> runtime/pkg`, `app -> all`.
- Do not add replicated presence storage, metadb tables, metafsm commands, or reconnect catch-up in this plan.
- Do not reintroduce local-only shortcuts for multi-node behavior. Single-node deployment still means single-node cluster semantics.
- Connect success must remain fail-closed: if authoritative register or required route-action ack fails, roll back the new route and reject connect.
- Heartbeats must stay aggregated by occupied group bucket. Do not add periodic per-route refresh.
- Preserve the legacy connect semantics from `learn_project/WuKongIM/internal/user/handler/event_connect.go`:
  - same `uid + device_flag` conflict scope
  - `master` replaces all same-flag routes
  - `slave` replaces only same `uid + device_flag + device_id`
- Reserve new cluster RPC service ids instead of reusing existing metastore ids `3` and `4`.
- Before final handoff, run `@superpowers/verification-before-completion`.

## File Map

| Path | Responsibility |
|------|----------------|
| `internal/gateway/types/event.go` | define optional pre-ack `SessionActivator` contract |
| `internal/gateway/event.go` | re-export the activator interface at the package edge |
| `internal/gateway/types/session_values.go` | add `SessionValueDeviceID` |
| `internal/gateway/auth.go` | persist `device_id` into authenticated session values |
| `internal/gateway/auth_test.go` | lock `device_id` and negotiated protocol session values |
| `internal/gateway/core/server.go` | run session activation after auth and before success `CONNACK` |
| `internal/gateway/core/server_test.go` | prove activation ordering, rollback path, and error behavior |
| `internal/gateway/gateway_test.go` | end-to-end wkproto auth + activation coverage |
| `internal/runtime/online/types.go` | local route state, `DeviceID`, `GroupID`, and bucket snapshot types |
| `internal/runtime/online/registry.go` | active/closing route lifecycle, UID lookup, group buckets, digest maintenance |
| `internal/runtime/online/registry_test.go` | registry lifecycle, bucket, and digest coverage |
| `internal/runtime/online/delivery.go` | local delivery skips non-active routes |
| `internal/runtime/online/delivery_test.go` | local delivery respects active-only routing |
| `internal/usecase/presence/app.go` | presence app construction, defaults, boot identity, worker lifecycle |
| `internal/usecase/presence/deps.go` | router, RPC client, action dispatcher, and clock interfaces |
| `internal/usecase/presence/types.go` | route, endpoint, lease, replay, action, and command types |
| `internal/usecase/presence/directory.go` | authoritative in-memory route indexes and owner-set bookkeeping |
| `internal/usecase/presence/authority.go` | authoritative register, unregister, heartbeat, replay, and endpoint query methods |
| `internal/usecase/presence/authority_test.go` | device conflict, digest mismatch, lease expiry, and replay semantics |
| `internal/usecase/presence/gateway.go` | gateway-side activate, deactivate, and route-action apply orchestration |
| `internal/usecase/presence/gateway_test.go` | rollback, action ack, and local closing-state coverage |
| `internal/usecase/presence/worker.go` | aggregated heartbeat / replay repair loop |
| `internal/usecase/presence/worker_test.go` | one-heartbeat-per-bucket and replay repair tests |
| `internal/usecase/presence/benchmark_test.go` | benchmark aggregated heartbeat cost against large route counts |
| `internal/access/node/options.go` | node RPC adapter collaborators |
| `internal/access/node/service_ids.go` | presence and remote-delivery RPC service ids |
| `internal/access/node/client.go` | cluster-backed presence RPC client with not-leader retry handling |
| `internal/access/node/presence_rpc.go` | authority/query/action RPC handlers and request/response codecs |
| `internal/access/node/presence_rpc_test.go` | handler/client round-trip, leader redirect, and replay coverage |
| `internal/access/node/delivery_rpc.go` | remote delivery RPC handler with `bootID + sessionID` fencing |
| `internal/access/node/delivery_rpc_test.go` | delivery fencing and active-route filtering tests |
| `internal/access/gateway/handler.go` | gateway handler depends on presence activation instead of direct registry writes |
| `internal/access/gateway/lifecycle.go` | map authenticated session values to presence commands including `device_id` |
| `internal/access/gateway/handler_test.go` | activation, close, and send-path integration coverage |
| `internal/usecase/message/app.go` | inject presence resolver and remote delivery collaborators |
| `internal/usecase/message/deps.go` | define endpoint resolver and remote delivery interfaces |
| `internal/usecase/message/send.go` | resolve authoritative endpoints and fan out by node |
| `internal/usecase/message/send_test.go` | local + remote post-commit fanout coverage |
| `internal/app/app.go` | add presence and node access components to the composition root |
| `internal/app/build.go` | create boot id, wire online registry, presence app, node RPC access, and gateway handler |
| `internal/app/lifecycle.go` | start/stop presence worker around gateway lifecycle |
| `internal/app/lifecycle_test.go` | lock worker start/stop ordering |
| `internal/app/multinode_integration_test.go` | cross-node connect, kick, replay repair, and delivery integration tests |

## Task 1: Add Pre-Ack Gateway Activation And `device_id` Session Propagation

**Files:**
- Modify: `internal/gateway/types/event.go`
- Modify: `internal/gateway/event.go`
- Modify: `internal/gateway/types/session_values.go`
- Modify: `internal/gateway/auth.go`
- Modify: `internal/gateway/auth_test.go`
- Modify: `internal/gateway/core/server.go`
- Modify: `internal/gateway/core/server_test.go`
- Modify: `internal/gateway/gateway_test.go`

- [ ] **Step 1: Write the failing gateway tests**

Add focused coverage:

- `TestAuthenticatorStoresDeviceIDSessionValue`
- `TestServerSuccessfulWKProtoActivationRunsBeforeSuccessConnack`
- `TestServerWKProtoActivationFailureWritesRetryableConnackAndCloses`
- `TestGatewayWKProtoActivationSeesDeviceIDBeforeConnectSucceeds`

Test sketch:

```go
func TestServerSuccessfulWKProtoActivationRunsBeforeSuccessConnack(t *testing.T) {
	handler := newTestHandler()
	handler.onActivate = func(ctx *gateway.Context) (*wkframe.ConnackPacket, error) {
		if got := ctx.Session.Value(gateway.SessionValueDeviceID); got != "d-1" {
			t.Fatalf("expected device id before activation, got %#v", got)
		}
		handler.record("activate")
		return nil, nil
	}

	proto := newScriptedProtocol("wkproto")
	proto.encodedBytes = []byte("connack-success")
	proto.pushDecode(decodeResult{
		frames: []wkframe.Frame{&wkframe.ConnectPacket{
			UID:        "u1",
			DeviceID:   "d-1",
			DeviceFlag: wkframe.APP,
		}},
		consumed: 1,
	})

	srv, factory := newTestServerWithAuthenticator(t, handler, proto, gateway.SessionOptions{}, gateway.NewWKProtoAuthenticator(gateway.WKProtoAuthOptions{}))
	require.NoError(t, srv.Start())
	defer srv.Stop()

	conn := factory.MustOpen("listener-a", 1)
	factory.MustData("listener-a", 1, []byte("c"))

	waitFor(t, func() bool { return len(conn.Writes()) == 1 })
	require.Equal(t, []string{"activate", "open"}, handler.callOrder())
}
```

- [ ] **Step 2: Run the gateway tests to verify they fail**

Run: `go test ./internal/gateway/... -run "TestAuthenticator|TestServer|TestGatewayWKProto" -count=1`

Expected: FAIL because `SessionValueDeviceID` and pre-ack activation do not exist yet.

- [ ] **Step 3: Write the minimal gateway implementation**

Add the new session value and activator contract:

```go
const (
	SessionValueUID             = "gateway.uid"
	SessionValueDeviceID        = "gateway.device_id"
	SessionValueDeviceFlag      = "gateway.device_flag"
	SessionValueDeviceLevel     = "gateway.device_level"
	SessionValueProtocolVersion = "gateway.protocol_version"
)

type SessionActivator interface {
	OnSessionActivate(ctx *Context) (*wkframe.ConnackPacket, error)
}
```

Persist `device_id` during auth and move success `CONNACK` behind activation:

```go
result.SessionValues[SessionValueDeviceID] = connect.DeviceID

for key, value := range result.SessionValues {
	state.session.SetValue(key, value)
}

if activator, ok := s.options.Handler.(gatewaytypes.SessionActivator); ok {
	override, err := activator.OnSessionActivate(ctx)
	if err != nil {
		_ = s.writeImmediateFrame(state, &wkframe.ConnackPacket{ReasonCode: wkframe.ReasonSystemError})
		state.close(gatewaytypes.CloseReasonPolicyViolation, err)
		return true, nil
	}
	if override != nil {
		connack = override
	}
}

if writeErr := s.writeImmediateFrame(state, connack); writeErr != nil {
	state.close(closeReasonForError(writeErr, gatewaytypes.CloseReasonPeerClosed), writeErr)
	return true, nil
}
```

- [ ] **Step 4: Run the gateway tests to verify they pass**

Run: `go test ./internal/gateway/... -run "TestAuthenticator|TestServer|TestGatewayWKProto" -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/gateway/types/event.go internal/gateway/event.go internal/gateway/types/session_values.go internal/gateway/auth.go internal/gateway/auth_test.go internal/gateway/core/server.go internal/gateway/core/server_test.go internal/gateway/gateway_test.go
git commit -m "feat(gateway): activate authenticated sessions before connack"
```

## Task 2: Extend `internal/runtime/online` With Local Route State And Group Buckets

**Files:**
- Modify: `internal/runtime/online/types.go`
- Modify: `internal/runtime/online/registry.go`
- Modify: `internal/runtime/online/registry_test.go`
- Modify: `internal/runtime/online/delivery.go`
- Modify: `internal/runtime/online/delivery_test.go`

- [ ] **Step 1: Write the failing online runtime tests**

Add coverage for:

- `TestRegistryRegisterStoresDeviceIDGroupAndActiveState`
- `TestRegistryMarkClosingRemovesRouteFromUIDDeliveryAndBucketDigest`
- `TestRegistryActiveConnectionsByGroupReturnsOnlyActiveRoutes`
- `TestLocalDeliverySkipsClosingRoutes`

Test sketch:

```go
func TestRegistryMarkClosingRemovesRouteFromUIDDeliveryAndBucketDigest(t *testing.T) {
	reg := NewRegistry()
	require.NoError(t, reg.Register(OnlineConn{
		SessionID:   11,
		UID:         "u1",
		DeviceID:    "d1",
		GroupID:     1,
		State:       LocalRouteStateActive,
		DeviceFlag:  wkframe.APP,
		DeviceLevel: wkframe.DeviceLevelMaster,
		Session:     session.New(session.Config{ID: 11, Listener: "tcp"}),
	}))

	before := reg.ActiveGroups()
	conn, ok := reg.MarkClosing(11)
	require.True(t, ok)
	require.Equal(t, LocalRouteStateClosing, conn.State)
	require.Empty(t, reg.ConnectionsByUID("u1"))
	require.NotEqual(t, before[0].Digest, reg.ActiveGroups()[0].Digest)
}
```

- [ ] **Step 2: Run the online runtime tests to verify they fail**

Run: `go test ./internal/runtime/online -run "TestRegistry|TestLocalDelivery" -count=1`

Expected: FAIL because route state, `DeviceID`, and bucket helpers do not exist yet.

- [ ] **Step 3: Write the minimal online runtime implementation**

Extend the route model and registry API:

```go
type LocalRouteState uint8

const (
	LocalRouteStateActive LocalRouteState = iota
	LocalRouteStateClosing
)

type Conn struct {
	SessionID   uint64
	UID         string
	DeviceID    string
	DeviceFlag  wkframe.DeviceFlag
	DeviceLevel wkframe.DeviceLevel
	GroupID     uint64
	State       LocalRouteState
	Listener    string
	ConnectedAt time.Time
	Session     session.Session
}

type GroupSnapshot struct {
	GroupID uint64
	Count   int
	Digest  uint64
}

type Registry interface {
	Register(conn OnlineConn) error
	Unregister(sessionID uint64)
	MarkClosing(sessionID uint64) (OnlineConn, bool)
	Connection(sessionID uint64) (OnlineConn, bool)
	ConnectionsByUID(uid string) []OnlineConn
	ActiveConnectionsByGroup(groupID uint64) []OnlineConn
	ActiveGroups() []GroupSnapshot
}
```

Update the registry so `closing` routes stay lookupable by `sessionID` but are excluded from:

- `ConnectionsByUID`
- `ActiveConnectionsByGroup`
- group `Count` / `Digest`
- `LocalDelivery.Deliver`

- [ ] **Step 4: Run the online runtime tests to verify they pass**

Run: `go test ./internal/runtime/online -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/runtime/online/types.go internal/runtime/online/registry.go internal/runtime/online/registry_test.go internal/runtime/online/delivery.go internal/runtime/online/delivery_test.go
git commit -m "feat(online): track active routes and group buckets"
```

## Task 3: Build The Authoritative Presence Directory And Device Conflict Rules

**Files:**
- Create: `internal/usecase/presence/app.go`
- Create: `internal/usecase/presence/deps.go`
- Create: `internal/usecase/presence/types.go`
- Create: `internal/usecase/presence/directory.go`
- Create: `internal/usecase/presence/authority.go`
- Create: `internal/usecase/presence/authority_test.go`

- [ ] **Step 1: Write the failing authority tests**

Add coverage for:

- `TestAuthorityRegisterMasterDifferentDeviceReturnsKickThenCloseActions`
- `TestAuthorityRegisterMasterSameDeviceReturnsCloseActions`
- `TestAuthorityRegisterSlaveOnlyReplacesSameDeviceID`
- `TestAuthorityHeartbeatDetectsDigestMismatchWhenCountMatches`
- `TestAuthorityReplayReplacesOwnerSetWithActiveRoutesOnly`
- `TestAuthorityEndpointsByUIDReturnsCurrentRoutes`

Test sketch:

```go
func TestAuthorityRegisterMasterDifferentDeviceReturnsKickThenCloseActions(t *testing.T) {
	app := New(Options{})

	_, err := app.RegisterAuthoritative(context.Background(), RegisterAuthoritativeCommand{
		GroupID: 1,
		Route: Route{
			UID:         "u1",
			NodeID:      1,
			BootID:      10,
			SessionID:   100,
			DeviceID:    "old-device",
			DeviceFlag:  uint8(wkframe.APP),
			DeviceLevel: uint8(wkframe.DeviceLevelMaster),
		},
	})
	require.NoError(t, err)

	result, err := app.RegisterAuthoritative(context.Background(), RegisterAuthoritativeCommand{
		GroupID: 1,
		Route: Route{
			UID:         "u1",
			NodeID:      2,
			BootID:      20,
			SessionID:   200,
			DeviceID:    "new-device",
			DeviceFlag:  uint8(wkframe.APP),
			DeviceLevel: uint8(wkframe.DeviceLevelMaster),
		},
	})

	require.NoError(t, err)
	require.Len(t, result.Actions, 1)
	require.Equal(t, "kick_then_close", result.Actions[0].Kind)
	require.Equal(t, uint64(200), app.EndpointsByUID(context.Background(), "u1")[0].SessionID)
}
```

- [ ] **Step 2: Run the authority tests to verify they fail**

Run: `go test ./internal/usecase/presence -run "TestAuthority" -count=1`

Expected: FAIL because the `presence` package does not exist yet.

- [ ] **Step 3: Write the minimal authoritative presence implementation**

Create focused authority types:

```go
type Route struct {
	UID         string
	NodeID      uint64
	BootID      uint64
	SessionID   uint64
	DeviceID    string
	DeviceFlag  uint8
	DeviceLevel uint8
	Listener    string
}

type RouteAction struct {
	UID       string
	NodeID    uint64
	BootID    uint64
	SessionID uint64
	Kind      string
	Reason    string
	DelayMS   int64
}

type GatewayLease struct {
	GroupID        uint64
	GatewayNodeID  uint64
	GatewayBootID  uint64
	RouteCount     int
	RouteDigest    uint64
	LeaseUntilUnix int64
}
```

In `directory.go`, keep three in-memory indexes:

```go
type directory struct {
	mu       sync.RWMutex
	byUID    map[string]map[routeKey]Route
	leases   map[leaseKey]GatewayLease
	ownerSet map[leaseKey]map[routeKey]Route
}
```

In `authority.go`, implement:

- `RegisterAuthoritative`
- `UnregisterAuthoritative`
- `HeartbeatAuthoritative`
- `ReplayAuthoritative`
- `EndpointsByUID`

Conflict rules must exactly match the spec:

```go
switch newLevel {
case uint8(wkframe.DeviceLevelMaster):
	// replace all same uid + device_flag
	// same device_id => close
	// different device_id => kick_then_close
case uint8(wkframe.DeviceLevelSlave):
	// replace only same uid + device_flag + device_id
}
```

Lease verification must compare both `RouteCount` and `RouteDigest`.

- [ ] **Step 4: Run the authority tests to verify they pass**

Run: `go test ./internal/usecase/presence -run "TestAuthority" -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/usecase/presence/app.go internal/usecase/presence/deps.go internal/usecase/presence/types.go internal/usecase/presence/directory.go internal/usecase/presence/authority.go internal/usecase/presence/authority_test.go
git commit -m "feat(presence): add authoritative in-memory route directory"
```

## Task 4: Add Gateway-Side Presence Activation, Action Ack, And Lease Repair

**Files:**
- Modify: `internal/usecase/presence/app.go`
- Modify: `internal/usecase/presence/deps.go`
- Modify: `internal/usecase/presence/types.go`
- Create: `internal/usecase/presence/gateway.go`
- Create: `internal/usecase/presence/gateway_test.go`
- Create: `internal/usecase/presence/worker.go`
- Create: `internal/usecase/presence/worker_test.go`
- Create: `internal/usecase/presence/benchmark_test.go`

- [ ] **Step 1: Write the failing gateway-side presence tests**

Add coverage for:

- `TestActivateRegistersLocalRouteAndRollsBackWhenAuthorityRegisterFails`
- `TestActivateRollsBackNewRouteWhenRouteActionAckFails`
- `TestDeactivateRemovesLocalRouteAndBestEffortUnregistersAuthority`
- `TestApplyRouteActionMarksRouteClosingBeforeAck`
- `TestApplyRouteActionRejectsMismatchedActiveRoute`
- `TestWorkerSendsOneHeartbeatPerNonEmptyGroupBucket`
- `TestWorkerReplayUsesOnlyActiveRoutes`

Test sketch:

```go
func TestActivateRollsBackNewRouteWhenRouteActionAckFails(t *testing.T) {
	onlineReg := online.NewRegistry()
	authority := &fakeAuthorityClient{
		registerResult: RegisterRouteResult{
			Actions: []RouteAction{{
				NodeID:    9,
				BootID:    88,
				SessionID: 77,
				Kind:      "close",
			}},
		},
	}
	app := New(Options{
		LocalNodeID:     1,
		GatewayBootID:   101,
		Online:          onlineReg,
		Router:          fakeRouter{groupID: 1, leaderID: 2},
		AuthorityClient: authority,
		ActionDispatcher: fakeActionDispatcher{
			applyErr: errors.New("ack timeout"),
		},
	})

	err := app.Activate(context.Background(), ActivateCommand{
		UID:         "u1",
		DeviceID:    "d1",
		DeviceFlag:  wkframe.APP,
		DeviceLevel: wkframe.DeviceLevelMaster,
		Session:     session.New(session.Config{ID: 11, Listener: "tcp"}),
	})

	require.Error(t, err)
	_, ok := onlineReg.Connection(11)
	require.False(t, ok)
	require.Len(t, authority.unregisterCalls, 1)
}
```

- [ ] **Step 2: Run the gateway-side presence tests to verify they fail**

Run: `go test ./internal/usecase/presence -run "TestActivate|TestApplyRouteAction|TestWorker" -count=1`

Expected: FAIL because activation, action apply, and worker logic do not exist yet.

- [ ] **Step 3: Write the minimal gateway-side presence implementation**

Add the gateway orchestration API:

```go
type ActivateCommand struct {
	UID         string
	DeviceID    string
	DeviceFlag  wkframe.DeviceFlag
	DeviceLevel wkframe.DeviceLevel
	Listener    string
	ConnectedAt time.Time
	Session     session.Session
}

type DeactivateCommand struct {
	UID       string
	SessionID uint64
}

func (a *App) Activate(ctx context.Context, cmd ActivateCommand) error {
	groupID := a.router.SlotForKey(cmd.UID)
	conn := online.OnlineConn{
		SessionID:   cmd.Session.ID(),
		UID:         cmd.UID,
		DeviceID:    cmd.DeviceID,
		DeviceFlag:  cmd.DeviceFlag,
		DeviceLevel: cmd.DeviceLevel,
		GroupID:     uint64(groupID),
		State:       online.LocalRouteStateActive,
		Listener:    cmd.Listener,
		ConnectedAt: cmd.ConnectedAt,
		Session:     cmd.Session,
	}
	if err := a.online.Register(conn); err != nil {
		return err
	}

	result, err := a.registerRoute(ctx, groupID, conn)
	if err != nil {
		a.online.Unregister(conn.SessionID)
		return err
	}
	if err := a.dispatchActions(ctx, groupID, result.Actions); err != nil {
		a.bestEffortUnregister(ctx, groupID, conn)
		a.online.Unregister(conn.SessionID)
		return err
	}
	return nil
}

func (a *App) Deactivate(ctx context.Context, cmd DeactivateCommand) error {
	conn, ok := a.online.Connection(cmd.SessionID)
	if !ok {
		return nil
	}

	a.online.Unregister(cmd.SessionID)

	groupID := a.router.SlotForKey(cmd.UID)
	_ = a.unregisterRoute(ctx, UnregisterRouteCommand{
		GroupID:   uint64(groupID),
		UID:       conn.UID,
		NodeID:    a.localNodeID,
		BootID:    a.gatewayBootID,
		SessionID: conn.SessionID,
	})
	return nil
}
```

`ApplyRouteAction` must treat `absent` and `already closing` as idempotent success, but it must reject a mismatched active route. A success ack is allowed only when the route is already non-routable or this call makes it non-routable:

```go
func (a *App) ApplyRouteAction(ctx context.Context, action RouteAction) error {
	if action.BootID != a.gatewayBootID {
		return nil
	}

	conn, ok := a.online.Connection(action.SessionID)
	if !ok {
		return nil
	}
	if conn.State == online.LocalRouteStateClosing {
		return nil
	}
	if conn.UID != action.UID {
		return fmt.Errorf("presence: fenced route mismatch for session %d", action.SessionID)
	}
	conn, ok = a.online.MarkClosing(action.SessionID)
	if !ok || conn.State != online.LocalRouteStateClosing {
		return fmt.Errorf("presence: failed to move session %d to closing", action.SessionID)
	}
	go a.finishRouteAction(conn, action)
	return nil
}
```

`worker.go` should:

- snapshot `online.ActiveGroups()`
- send one heartbeat per bucket
- trigger `ReplayRoutes` on `replay_required` or `not_leader`
- exclude `closing` routes from replay payloads

Also add a benchmark:

```go
func BenchmarkHeartbeatBuckets100kRoutes(b *testing.B) {
	// 100k routes in a few groups should produce O(group buckets) heartbeats.
}
```

- [ ] **Step 4: Run the gateway-side presence tests and benchmark**

Run: `go test ./internal/usecase/presence -run "TestActivate|TestApplyRouteAction|TestWorker" -count=1`

Expected: PASS

Run: `go test ./internal/usecase/presence -run '^$' -bench 'BenchmarkHeartbeatBuckets' -benchmem`

Expected: benchmark completes without showing per-route heartbeat scaling.

- [ ] **Step 5: Commit**

```bash
git add internal/usecase/presence/app.go internal/usecase/presence/deps.go internal/usecase/presence/types.go internal/usecase/presence/gateway.go internal/usecase/presence/gateway_test.go internal/usecase/presence/worker.go internal/usecase/presence/worker_test.go internal/usecase/presence/benchmark_test.go
git commit -m "feat(presence): add gateway activation and lease repair"
```

## Task 5: Add Node RPC Adapters For Presence And Remote Delivery

**Files:**
- Create: `internal/access/node/options.go`
- Create: `internal/access/node/service_ids.go`
- Create: `internal/access/node/client.go`
- Create: `internal/access/node/presence_rpc.go`
- Create: `internal/access/node/presence_rpc_test.go`
- Create: `internal/access/node/delivery_rpc.go`
- Create: `internal/access/node/delivery_rpc_test.go`

- [ ] **Step 1: Write the failing node RPC tests**

Add coverage for:

- `TestPresenceRPCClientFollowsNotLeaderRedirect`
- `TestPresenceRPCRegisterRoundTrip`
- `TestPresenceRPCUnregisterRoundTrip`
- `TestPresenceRPCReplayRoundTrip`
- `TestDeliveryRPCDropsWhenBootIDDoesNotMatch`
- `TestDeliveryRPCDropsClosingRouteTargets`

Test sketch:

```go
func TestDeliveryRPCDropsWhenBootIDDoesNotMatch(t *testing.T) {
	reg := online.NewRegistry()
	sess := newRecordingSession(11, "tcp")
	require.NoError(t, reg.Register(online.OnlineConn{
		SessionID: 11,
		UID:       "u1",
		GroupID:   1,
		State:     online.LocalRouteStateActive,
		Session:   sess,
	}))

	adapter := New(Options{
		Presence:     &fakePresenceAuthority{},
		LocalNodeID:   1,
		GatewayBootID: 99,
		Online:        reg,
	})

	_, err := adapter.handleDeliveryRPC(context.Background(), mustMarshal(deliveryRequest{
		UID:        "u1",
		GroupID:    1,
		BootID:     100,
		SessionIDs: []uint64{11},
		Frame:      &wkframe.RecvPacket{MessageID: 1, MessageSeq: 1},
	}))

	require.NoError(t, err)
	require.Empty(t, sess.WrittenFrames())
}
```

- [ ] **Step 2: Run the node RPC tests to verify they fail**

Run: `go test ./internal/access/node -count=1`

Expected: FAIL because the package does not exist yet.

- [ ] **Step 3: Write the minimal node RPC implementation**

Reserve service ids and register handlers on the cluster RPC mux:

```go
const (
	presenceRPCServiceID uint8 = 5
	deliveryRPCServiceID uint8 = 6
)

func NewClient(cluster *raftcluster.Cluster) *Client {
	return &Client{cluster: cluster}
}

func New(opts Options) *Adapter {
	a := &Adapter{
		cluster:       opts.Cluster,
		presence:      opts.Presence,
		online:        opts.Online,
		gatewayBootID: opts.GatewayBootID,
	}
	if opts.Cluster != nil && opts.Cluster.RPCMux() != nil {
		opts.Cluster.RPCMux().Handle(presenceRPCServiceID, a.handlePresenceRPC)
		opts.Cluster.RPCMux().Handle(deliveryRPCServiceID, a.handleDeliveryRPC)
	}
	return a
}
```

`presence_rpc.go` must implement one explicit switch for all disconnect and repair operations, not an ad hoc subset:

```go
switch req.Op {
case "register":
	return a.handleRegister(ctx, req)
case "unregister":
	return a.handleUnregister(ctx, req)
case "heartbeat":
	return a.handleHeartbeat(ctx, req)
case "replay":
	return a.handleReplay(ctx, req)
case "endpoints":
	return a.handleEndpoints(ctx, req)
case "apply_action":
	return a.handleApplyAction(ctx, req)
default:
	return nil, fmt.Errorf("access/node: unknown presence op %q", req.Op)
}
```

Keep request/response codecs scoped to the access layer:

```go
type presenceRPCRequest struct {
	Op            string                `json:"op"`
	GroupID       uint64                `json:"group_id"`
	UID           string                `json:"uid,omitempty"`
	NodeID        uint64                `json:"node_id,omitempty"`
	BootID        uint64                `json:"boot_id,omitempty"`
	SessionID     uint64                `json:"session_id,omitempty"`
	Route         *presence.Route       `json:"route,omitempty"`
	Routes        []presence.Route      `json:"routes,omitempty"`
	Action        *presence.RouteAction `json:"action,omitempty"`
	GatewayNodeID uint64                `json:"gateway_node_id,omitempty"`
	GatewayBootID uint64                `json:"gateway_boot_id,omitempty"`
	RouteCount    int                   `json:"route_count,omitempty"`
	RouteDigest   uint64                `json:"route_digest,omitempty"`
}

type presenceRPCResponse struct {
	Status    string                      `json:"status"`
	LeaderID  uint64                      `json:"leader_id,omitempty"`
	Register  *presence.RegisterRouteResult `json:"register,omitempty"`
	Heartbeat *presence.HeartbeatResult   `json:"heartbeat,omitempty"`
	Endpoints []presence.Endpoint         `json:"endpoints,omitempty"`
}
```

In `client.go`, copy the same not-leader retry pattern used by metastore, but keep it private to `internal/access/node` instead of coupling to `pkg/storage/metastore`.

Make the disconnect cleanup path explicit in the client and handler set:

```go
func (c *Client) UnregisterRoute(ctx context.Context, req presence.UnregisterRouteCommand) error {
	_, err := c.callPresenceRPC(ctx, presenceRPCRequest{
		Op:        "unregister",
		GroupID:   req.GroupID,
		UID:       req.UID,
		NodeID:    req.NodeID,
		BootID:    req.BootID,
		SessionID: req.SessionID,
	})
	return err
}
```

`handleUnregister(...)` must route `"unregister"` to `Presence.UnregisterAuthoritative(...)` and return a normal status response. `presence.Deactivate(...)` is allowed to ignore the returned error, but the RPC path itself must exist, have request/response codecs, and be covered by `TestPresenceRPCUnregisterRoundTrip`.

In `delivery_rpc.go`, enforce target fencing:

```go
if req.BootID != a.gatewayBootID {
	return encodeDeliveryResponse(deliveryResponse{Status: "ok"})
}
for _, sessionID := range req.SessionIDs {
	conn, ok := a.online.Connection(sessionID)
	if !ok || conn.UID != req.UID || conn.State != online.LocalRouteStateActive {
		continue
	}
	_ = conn.Session.WriteFrame(req.Frame)
}
```

- [ ] **Step 4: Run the node RPC tests to verify they pass**

Run: `go test ./internal/access/node -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/access/node/options.go internal/access/node/service_ids.go internal/access/node/client.go internal/access/node/presence_rpc.go internal/access/node/presence_rpc_test.go internal/access/node/delivery_rpc.go internal/access/node/delivery_rpc_test.go
git commit -m "feat(access/node): add presence and delivery rpc adapters"
```

## Task 6: Integrate Presence Into Gateway Access And App Lifecycle

**Files:**
- Modify: `internal/access/gateway/handler.go`
- Modify: `internal/access/gateway/lifecycle.go`
- Modify: `internal/access/gateway/handler_test.go`
- Modify: `internal/app/app.go`
- Modify: `internal/app/build.go`
- Modify: `internal/app/lifecycle.go`
- Modify: `internal/app/lifecycle_test.go`

- [ ] **Step 1: Write the failing integration wiring tests**

Add coverage for:

- `TestHandlerOnSessionActivateCallsPresenceActivate`
- `TestHandlerOnSessionCloseCallsPresenceDeactivate`
- `TestBuildCreatesPresenceAppAndNodeAccess`
- `TestAppLifecycleStartsPresenceWorkerBeforeGateway`
- `TestAppLifecycleStopsPresenceWorkerAfterGateway`

Test sketch:

```go
func TestHandlerOnSessionActivateCallsPresenceActivate(t *testing.T) {
	presenceApp := &fakePresenceUsecase{}
	handler := New(Options{
		Presence: presenceApp,
		Messages: &fakeMessageUsecase{},
		Now:      func() time.Time { return fixedSendNow },
	})

	ctx := newAuthedContext(t, 1, "u1")
	ctx.Session.SetValue(coregateway.SessionValueDeviceID, "d1")

	_, err := handler.OnSessionActivate(ctx)
	require.NoError(t, err)
	require.Len(t, presenceApp.activateCalls, 1)
	require.Equal(t, "d1", presenceApp.activateCalls[0].DeviceID)
}
```

- [ ] **Step 2: Run the handler and app tests to verify they fail**

Run: `go test ./internal/access/gateway ./internal/app -run "TestHandler|TestBuild|TestAppLifecycle" -count=1`

Expected: FAIL because the handler still registers directly into `online.Registry` and the app does not wire presence yet.

- [ ] **Step 3: Write the minimal integration implementation**

Update the gateway handler dependencies:

```go
type PresenceUsecase interface {
	Activate(ctx context.Context, cmd presence.ActivateCommand) error
	Deactivate(ctx context.Context, cmd presence.DeactivateCommand) error
}

func (h *Handler) OnSessionActivate(ctx *coregateway.Context) (*wkframe.ConnackPacket, error) {
	cmd, err := activateCommandFromContext(ctx, h.now())
	if err != nil {
		return nil, err
	}
	return nil, h.presence.Activate(requestContextOrBackground(ctx), cmd)
}

func (h *Handler) OnSessionOpen(*coregateway.Context) error { return nil }

func (h *Handler) OnSessionClose(ctx *coregateway.Context) error {
	return h.presence.Deactivate(requestContextOrBackground(ctx), deactivateCommandFromContext(ctx))
}
```

In `build.go`, wire the composition root once:

```go
bootID := rand.Uint64()
onlineRegistry := online.NewRegistry()
nodeClient := accessnode.NewClient(app.cluster)
app.presenceApp = presence.New(presence.Options{
	LocalNodeID:      cfg.Node.ID,
	GatewayBootID:    bootID,
	Router:           app.cluster,
	Online:           onlineRegistry,
	AuthorityClient:  nodeClient,
	ActionDispatcher: nodeClient,
	QueryClient:      nodeClient,
})
app.nodeAccess = accessnode.New(accessnode.Options{
	Cluster:       app.cluster,
	Presence:      app.presenceApp,
	LocalNodeID:   cfg.Node.ID,
	GatewayBootID: bootID,
	Online:        onlineRegistry,
})
app.gatewayHandler = accessgateway.New(accessgateway.Options{
	Presence: app.presenceApp,
	Messages: app.messageApp,
})
```

In `lifecycle.go`, start presence after cluster start and before gateway start; stop it after gateway stop and before cluster stop.

Make the close-path contract explicit in the handler and lifecycle tests:

- local route removal happens immediately on session close
- authority unregister is best effort only
- if unregister RPC fails, the closed route stays offline locally and authority cleanup is left to later lease expiry or replay correction

- [ ] **Step 4: Run the handler and app tests to verify they pass**

Run: `go test ./internal/access/gateway ./internal/app -run "TestHandler|TestBuild|TestAppLifecycle" -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/access/gateway/handler.go internal/access/gateway/lifecycle.go internal/access/gateway/handler_test.go internal/app/app.go internal/app/build.go internal/app/lifecycle.go internal/app/lifecycle_test.go
git commit -m "feat(app): wire presence into gateway lifecycle"
```

## Task 7: Move Person-Channel Fanout To Presence Endpoints And Prove Multi-Node Behavior

**Files:**
- Modify: `internal/usecase/message/app.go`
- Modify: `internal/usecase/message/deps.go`
- Modify: `internal/usecase/message/send.go`
- Modify: `internal/usecase/message/send_test.go`
- Modify: `internal/app/build.go`
- Modify: `internal/app/multinode_integration_test.go`

- [ ] **Step 1: Write the failing message and multinode tests**

Add unit coverage for:

- `TestSendDeliversDurablePersonMessageToLocalAndRemoteEndpoints`
- `TestSendSkipsUnknownLocalEndpoint`
- `TestSendReturnsSuccessWhenRemotePostCommitDeliveryFails`

Add multinode coverage for:

- `TestThreeNodeAppPresenceQueryFindsUserAcrossNodes`
- `TestThreeNodeAppMasterConnectKicksOldDifferentDeviceRouteOnOtherNode`
- `TestThreeNodeAppSlaveConnectClosesOnlySameDeviceRoute`
- `TestThreeNodeAppLeaderMemoryLossRepairsViaReplayAndDeliveryResumes`
- `TestThreeNodeAppDigestMismatchTriggersReplayWithoutPerRouteRefresh`

Test sketch:

```go
func TestSendDeliversDurablePersonMessageToLocalAndRemoteEndpoints(t *testing.T) {
	cluster := &fakeChannelCluster{
		sendReplies: []fakeChannelClusterSendReply{{result: channellog.SendResult{MessageID: 99, MessageSeq: 7}}},
	}
	presenceResolver := &fakePresenceResolver{
		endpoints: []presence.Endpoint{
			{NodeID: 1, BootID: 10, SessionID: 2},
			{NodeID: 2, BootID: 20, SessionID: 3},
		},
	}
	reg := &fakeRegistry{bySession: map[uint64]online.OnlineConn{
		2: {SessionID: 2, UID: "u2", State: online.LocalRouteStateActive},
	}}
	remote := &fakeRemoteDelivery{}
	app := New(Options{
		LocalNodeID:    1,
		Cluster:        cluster,
		Online:         reg,
		Presence:       presenceResolver,
		Delivery:       &recordingDelivery{},
		RemoteDelivery: remote,
		Now:            fixedNowFn,
	})

	_, err := app.Send(context.Background(), SendCommand{SenderUID: "u1", ChannelID: "u2", ChannelType: wkframe.ChannelTypePerson})
	require.NoError(t, err)
	require.Len(t, remote.calls, 1)
}
```

- [ ] **Step 2: Run the message and multinode tests to verify they fail**

Run: `go test ./internal/usecase/message ./internal/app -run "TestSend|TestThreeNodeApp" -count=1`

Expected: FAIL because person-channel fanout still reads only local `ConnectionsByUID`.

- [ ] **Step 3: Write the minimal message + integration implementation**

Update `message.Options` and `message.App`:

```go
type PresenceResolver interface {
	EndpointsByUID(ctx context.Context, uid string) ([]presence.Endpoint, error)
}

type RemoteDelivery interface {
	DeliverPerson(ctx context.Context, nodeID uint64, uid string, endpoints []presence.Endpoint, frame *wkframe.RecvPacket) error
}

type Options struct {
	IdentityStore   IdentityStore
	ChannelStore    ChannelStore
	Cluster         ChannelCluster
	MetaRefresher   MetaRefresher
	LocalNodeID    uint64
	Presence       PresenceResolver
	Online         online.Registry
	Delivery       online.Delivery
	RemoteDelivery RemoteDelivery
	Now            func() time.Time
}
```

Replace local-only fanout:

```go
func (a *App) deliverPerson(ctx context.Context, cmd SendCommand, msgID int64, msgSeq uint64) error {
	endpoints, err := a.presence.EndpointsByUID(ctx, cmd.ChannelID)
	if err != nil || len(endpoints) == 0 {
		return err
	}

	frame := buildPersonRecvPacket(cmd, msgID, msgSeq, a.now())
	localRecipients, remoteTargets := splitEndpoints(endpoints, a.localNodeID, a.online)
	_ = a.delivery.Deliver(localRecipients, frame)
	for nodeID, targetEndpoints := range remoteTargets {
		_ = a.remoteDelivery.DeliverPerson(ctx, nodeID, cmd.ChannelID, targetEndpoints, frame)
	}
	return nil
}
```

In `multinode_integration_test.go`, reuse the existing three-node harness and add:

- second-device connects across different gateway nodes
- leader-memory clear by reaching into `app.presenceApp` from the same package test
- replay repair assertions before checking resumed realtime delivery

Update `internal/app/build.go` again in this task so the new message dependencies are actually wired and built in the correct order:

- build `nodeClient` first
- build `app.presenceApp` second
- build `app.nodeAccess` third
- move `message.New(...)` below those steps so `Presence` and `RemoteDelivery` are non-nil
- keep `app.gatewayHandler` last so it receives the fully wired `messageApp`

```go
bootID := rand.Uint64()
onlineRegistry := online.NewRegistry()
nodeClient := accessnode.NewClient(app.cluster)

app.presenceApp = presence.New(presence.Options{
	LocalNodeID:      cfg.Node.ID,
	GatewayBootID:    bootID,
	Router:           app.cluster,
	Online:           onlineRegistry,
	AuthorityClient:  nodeClient,
	ActionDispatcher: nodeClient,
	QueryClient:      nodeClient,
})
app.nodeAccess = accessnode.New(accessnode.Options{
	Cluster:       app.cluster,
	Presence:      app.presenceApp,
	LocalNodeID:   cfg.Node.ID,
	GatewayBootID: bootID,
	Online:        onlineRegistry,
})
app.messageApp = message.New(message.Options{
	IdentityStore:   app.store,
	ChannelStore:    app.store,
	Cluster:         app.channelLog,
	MetaRefresher:   app.channelMetaSync,
	LocalNodeID:     cfg.Node.ID,
	Presence:        app.presenceApp,
	Online:          onlineRegistry,
	Delivery:        online.LocalDelivery{},
	RemoteDelivery:  nodeClient,
})
app.gatewayHandler = accessgateway.New(accessgateway.Options{
	Presence: app.presenceApp,
	Messages: app.messageApp,
})
```

- [ ] **Step 4: Run the message, multinode, and focused verification suite**

Run: `go test ./internal/usecase/message ./internal/access/node ./internal/usecase/presence ./internal/access/gateway ./internal/app -count=1`

Expected: PASS

Run: `go test ./...`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/usecase/message/app.go internal/usecase/message/deps.go internal/usecase/message/send.go internal/usecase/message/send_test.go internal/app/build.go internal/app/multinode_integration_test.go
git commit -m "feat(message): route online delivery through presence authority"
```

## Final Verification

- [ ] Run `go test ./internal/gateway/... ./internal/runtime/online ./internal/usecase/presence ./internal/access/node ./internal/access/gateway ./internal/usecase/message ./internal/app -count=1`
- [ ] Run `go test ./...`
- [ ] Run `go test ./internal/usecase/presence -run '^$' -bench 'BenchmarkHeartbeatBuckets' -benchmem`
- [ ] Review `git diff --stat` to confirm no scope creep into replicated presence storage or reconnect catch-up
- [ ] Prepare execution handoff using `@superpowers/subagent-driven-development`
