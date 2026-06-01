package cluster

import (
	"context"
	"errors"
	"reflect"
	"testing"

	accessnode "github.com/WuKongIM/WuKongIM/internalv2/access/node"
	authoritypresence "github.com/WuKongIM/WuKongIM/internalv2/runtime/presence"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
)

func TestPresenceClientUsesLocalAuthorityWhenTargetLeaderIsLocal(t *testing.T) {
	cluster := &fakePresenceCluster{
		nodeID: 1,
		route:  clusterv2.Route{HashSlot: 7, SlotID: 11, Leader: 1, Revision: 17, AuthorityEpoch: 19},
	}
	local := &fakePresenceAuthority{registerResult: presence.RegisterResult{PendingToken: "pending-1"}}
	client := NewPresenceAuthorityClient(cluster, local)
	route := testInfraPresenceRoute("u1", 101)

	result, err := client.RegisterRoute(context.Background(), route)
	if err != nil {
		t.Fatalf("RegisterRoute() error = %v", err)
	}
	if result.PendingToken == "" || result.PendingToken == "pending-1" {
		t.Fatalf("PendingToken = %q, want wrapped token", result.PendingToken)
	}
	if len(local.registerCalls) != 1 {
		t.Fatalf("local register calls = %d, want 1", len(local.registerCalls))
	}
	wantTarget := presence.RouteTarget{HashSlot: 7, SlotID: 11, LeaderNodeID: 1, RouteRevision: 17, AuthorityEpoch: 19}
	if !reflect.DeepEqual(local.registerCalls[0].target, wantTarget) {
		t.Fatalf("local target = %#v, want %#v", local.registerCalls[0].target, wantTarget)
	}
	if len(cluster.calls) != 0 {
		t.Fatalf("remote rpc calls = %d, want 0", len(cluster.calls))
	}
}

func TestPresenceClientCallsRemoteAuthorityWhenTargetLeaderIsRemote(t *testing.T) {
	remoteAuthority := &fakePresenceAuthority{registerResult: presence.RegisterResult{PendingToken: "remote-pending"}}
	cluster := &fakePresenceCluster{
		nodeID: 1,
		route:  clusterv2.Route{HashSlot: 9, SlotID: 12, Leader: 2, Revision: 18, AuthorityEpoch: 20},
		rpc:    presenceRPCHandler{adapter: accessnode.New(accessnode.Options{Authority: remoteAuthority})},
	}
	local := &fakePresenceAuthority{}
	client := NewPresenceAuthorityClient(cluster, local)

	result, err := client.RegisterRoute(context.Background(), testInfraPresenceRoute("u2", 201))
	if err != nil {
		t.Fatalf("RegisterRoute() error = %v", err)
	}
	if result.PendingToken == "" || result.PendingToken == "remote-pending" {
		t.Fatalf("PendingToken = %q, want wrapped token", result.PendingToken)
	}
	if len(local.registerCalls) != 0 {
		t.Fatalf("local register calls = %d, want 0", len(local.registerCalls))
	}
	if len(remoteAuthority.registerCalls) != 1 {
		t.Fatalf("remote register calls = %d, want 1", len(remoteAuthority.registerCalls))
	}
	if len(cluster.calls) != 1 {
		t.Fatalf("remote rpc calls = %d, want 1", len(cluster.calls))
	}
	if cluster.calls[0].nodeID != 2 || cluster.calls[0].serviceID != accessnode.PresenceAuthorityRPCServiceID {
		t.Fatalf("remote rpc call = %#v", cluster.calls[0])
	}
	wantTarget := presence.RouteTarget{HashSlot: 9, SlotID: 12, LeaderNodeID: 2, RouteRevision: 18, AuthorityEpoch: 20}
	if !reflect.DeepEqual(remoteAuthority.registerCalls[0].target, wantTarget) {
		t.Fatalf("remote target = %#v, want %#v", remoteAuthority.registerCalls[0].target, wantTarget)
	}
}

func TestPresenceClientRetriesOnceAfterStaleRoute(t *testing.T) {
	cluster := &fakePresenceCluster{
		nodeID: 1,
		routes: []clusterv2.Route{
			{HashSlot: 7, SlotID: 11, Leader: 1, Revision: 17, AuthorityEpoch: 1},
			{HashSlot: 7, SlotID: 11, Leader: 1, Revision: 18, AuthorityEpoch: 2},
		},
	}
	local := &fakePresenceAuthority{
		registerErrs: []error{authoritypresence.ErrStaleRoute, nil},
	}
	client := NewPresenceAuthorityClient(cluster, local)

	if _, err := client.RegisterRoute(context.Background(), testInfraPresenceRoute("u1", 101)); err != nil {
		t.Fatalf("RegisterRoute() error = %v", err)
	}
	if cluster.routeKeyCalls != 2 {
		t.Fatalf("RouteKey calls = %d, want 2", cluster.routeKeyCalls)
	}
	if len(local.registerCalls) != 2 {
		t.Fatalf("local register calls = %d, want 2", len(local.registerCalls))
	}
	if got := local.registerCalls[1].target.RouteRevision; got != 18 {
		t.Fatalf("retry RouteRevision = %d, want 18", got)
	}
}

func TestPresenceClientPendingTokensAreNamespacedByUID(t *testing.T) {
	cluster := &fakePresenceCluster{
		nodeID: 1,
		routesByUID: map[string]clusterv2.Route{
			"u1": {HashSlot: 7, SlotID: 11, Leader: 1, Revision: 17, AuthorityEpoch: 1},
			"u2": {HashSlot: 8, SlotID: 12, Leader: 1, Revision: 18, AuthorityEpoch: 1},
		},
	}
	local := &fakePresenceAuthority{registerResult: presence.RegisterResult{PendingToken: "1"}}
	client := NewPresenceAuthorityClient(cluster, local)

	first, err := client.RegisterRoute(context.Background(), testInfraPresenceRoute("u1", 101))
	if err != nil {
		t.Fatalf("RegisterRoute(u1) error = %v", err)
	}
	second, err := client.RegisterRoute(context.Background(), testInfraPresenceRoute("u2", 201))
	if err != nil {
		t.Fatalf("RegisterRoute(u2) error = %v", err)
	}
	if first.PendingToken == second.PendingToken || first.PendingToken == "1" || second.PendingToken == "1" {
		t.Fatalf("wrapped tokens = %q,%q, want distinct non-raw tokens", first.PendingToken, second.PendingToken)
	}
	if err := client.CommitRoute(context.Background(), first.PendingToken); err != nil {
		t.Fatalf("CommitRoute(first) error = %v", err)
	}
	if err := client.CommitRoute(context.Background(), second.PendingToken); err != nil {
		t.Fatalf("CommitRoute(second) error = %v", err)
	}
	if len(local.commitCalls) != 2 {
		t.Fatalf("commit calls = %d, want 2", len(local.commitCalls))
	}
	if local.commitCalls[0].target.HashSlot != 7 || local.commitCalls[1].target.HashSlot != 8 {
		t.Fatalf("commit targets = %#v", local.commitCalls)
	}
	if local.commitCalls[0].token != "1" || local.commitCalls[1].token != "1" {
		t.Fatalf("raw commit tokens = %#v, want raw token 1 for both", local.commitCalls)
	}
}

func TestPresenceClientRejectsMixedRehydrateHashSlots(t *testing.T) {
	cluster := &fakePresenceCluster{
		nodeID: 1,
		routesByUID: map[string]clusterv2.Route{
			"u1": {HashSlot: 7, SlotID: 11, Leader: 1, Revision: 17, AuthorityEpoch: 1},
			"u2": {HashSlot: 8, SlotID: 12, Leader: 1, Revision: 18, AuthorityEpoch: 1},
		},
	}
	local := &fakePresenceAuthority{}
	client := NewPresenceAuthorityClient(cluster, local)

	_, err := client.RehydrateRoutes(context.Background(), []presence.Route{
		testInfraPresenceRoute("u1", 101),
		testInfraPresenceRoute("u2", 201),
	})
	if !errors.Is(err, authoritypresence.ErrRouteNotReady) {
		t.Fatalf("RehydrateRoutes() error = %v, want ErrRouteNotReady", err)
	}
	if len(local.rehydrateCalls) != 0 {
		t.Fatalf("rehydrate calls = %d, want 0", len(local.rehydrateCalls))
	}
}

func TestPresenceClientReturnsRouteNotReadyWhenRouteKeyFails(t *testing.T) {
	cluster := &fakePresenceCluster{
		nodeID:   1,
		routeErr: clusterv2.ErrRouteNotReady,
	}
	client := NewPresenceAuthorityClient(cluster, &fakePresenceAuthority{})

	_, err := client.EndpointsByUID(context.Background(), "u1")
	if !errors.Is(err, authoritypresence.ErrRouteNotReady) {
		t.Fatalf("EndpointsByUID() error = %v, want %v", err, authoritypresence.ErrRouteNotReady)
	}
}

func TestPresenceClientReturnsRouteNotReadyWhenRouteHasNoLeader(t *testing.T) {
	cluster := &fakePresenceCluster{
		nodeID:   1,
		routeErr: clusterv2.ErrNoSlotLeader,
	}
	client := NewPresenceAuthorityClient(cluster, &fakePresenceAuthority{})

	_, err := client.EndpointsByUID(context.Background(), "u1")
	if !errors.Is(err, authoritypresence.ErrRouteNotReady) {
		t.Fatalf("EndpointsByUID() error = %v, want %v", err, authoritypresence.ErrRouteNotReady)
	}
}

type rpcCall struct {
	nodeID    uint64
	serviceID uint8
	payload   []byte
}

type fakePresenceCluster struct {
	nodeID        uint64
	route         clusterv2.Route
	routesByUID   map[string]clusterv2.Route
	routes        []clusterv2.Route
	routeErr      error
	rpc           clusterv2.NodeRPCHandler
	calls         []rpcCall
	routeKeyCalls int
	registered    map[uint8]clusterv2.NodeRPCHandler
	watch         chan clusterv2.RouteAuthorityEvent
}

type presenceRPCHandler struct {
	adapter *accessnode.Adapter
}

func (h presenceRPCHandler) HandleRPC(ctx context.Context, payload []byte) ([]byte, error) {
	return h.adapter.HandlePresenceAuthorityRPC(ctx, payload)
}

func (f *fakePresenceCluster) NodeID() uint64 {
	return f.nodeID
}

func (f *fakePresenceCluster) RouteKey(uid string) (clusterv2.Route, error) {
	f.routeKeyCalls++
	if f.routeErr != nil {
		return clusterv2.Route{}, f.routeErr
	}
	if route, ok := f.routesByUID[uid]; ok {
		return route, nil
	}
	if len(f.routes) > 0 {
		idx := f.routeKeyCalls - 1
		if idx >= len(f.routes) {
			idx = len(f.routes) - 1
		}
		return f.routes[idx], nil
	}
	return f.route, nil
}

func (f *fakePresenceCluster) RouteHashSlot(uint16) (clusterv2.Route, error) {
	if f.routeErr != nil {
		return clusterv2.Route{}, f.routeErr
	}
	return f.route, nil
}

func (f *fakePresenceCluster) CallRPC(ctx context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	f.calls = append(f.calls, rpcCall{nodeID: nodeID, serviceID: serviceID, payload: append([]byte(nil), payload...)})
	if f.rpc != nil {
		return f.rpc.HandleRPC(ctx, payload)
	}
	if handler := f.registered[serviceID]; handler != nil {
		return handler.HandleRPC(ctx, payload)
	}
	return nil, errors.New("missing rpc handler")
}

func (f *fakePresenceCluster) RegisterRPC(serviceID uint8, handler clusterv2.NodeRPCHandler) {
	if f.registered == nil {
		f.registered = make(map[uint8]clusterv2.NodeRPCHandler)
	}
	f.registered[serviceID] = handler
}

func (f *fakePresenceCluster) WatchRouteAuthorities() <-chan clusterv2.RouteAuthorityEvent {
	if f.watch == nil {
		f.watch = make(chan clusterv2.RouteAuthorityEvent)
	}
	return f.watch
}

type fakePresenceAuthority struct {
	registerResult presence.RegisterResult
	registerErrs   []error
	registerCalls  []presenceRegisterCall
	commitCalls    []presenceTokenCall
	abortCalls     []presenceTokenCall
	unregCalls     []presenceUnregisterCall
	endpointCalls  []presenceEndpointCall
	rehydrateCalls []presenceRehydrateCall
}

func (f *fakePresenceAuthority) RegisterRoute(_ context.Context, target presence.RouteTarget, route presence.Route) (presence.RegisterResult, error) {
	f.registerCalls = append(f.registerCalls, presenceRegisterCall{target: target, route: route})
	if len(f.registerErrs) > 0 {
		err := f.registerErrs[0]
		f.registerErrs = f.registerErrs[1:]
		if err != nil {
			return presence.RegisterResult{}, err
		}
	}
	return f.registerResult, nil
}

func (f *fakePresenceAuthority) CommitRoute(_ context.Context, target presence.RouteTarget, token string) error {
	f.commitCalls = append(f.commitCalls, presenceTokenCall{target: target, token: token})
	return nil
}

func (f *fakePresenceAuthority) AbortRoute(_ context.Context, target presence.RouteTarget, token string) error {
	f.abortCalls = append(f.abortCalls, presenceTokenCall{target: target, token: token})
	return nil
}

func (f *fakePresenceAuthority) UnregisterRoute(_ context.Context, target presence.RouteTarget, identity presence.RouteIdentity, ownerSeq uint64) error {
	f.unregCalls = append(f.unregCalls, presenceUnregisterCall{target: target, identity: identity, ownerSeq: ownerSeq})
	return nil
}

func (f *fakePresenceAuthority) EndpointsByUID(_ context.Context, target presence.RouteTarget, uid string) ([]presence.Route, error) {
	f.endpointCalls = append(f.endpointCalls, presenceEndpointCall{target: target, uid: uid})
	return []presence.Route{testInfraPresenceRoute(uid, 301)}, nil
}

func (f *fakePresenceAuthority) RehydrateRoutes(_ context.Context, target presence.RouteTarget, routes []presence.Route) ([]presence.RehydrateResult, error) {
	f.rehydrateCalls = append(f.rehydrateCalls, presenceRehydrateCall{target: target, routes: append([]presence.Route(nil), routes...)})
	results := make([]presence.RehydrateResult, 0, len(routes))
	for _, route := range routes {
		results = append(results, presence.RehydrateResult{Route: route.Identity(), Accepted: true})
	}
	return results, nil
}

type presenceRegisterCall struct {
	target presence.RouteTarget
	route  presence.Route
}

type presenceTokenCall struct {
	target presence.RouteTarget
	token  string
}

type presenceUnregisterCall struct {
	target   presence.RouteTarget
	identity presence.RouteIdentity
	ownerSeq uint64
}

type presenceEndpointCall struct {
	target presence.RouteTarget
	uid    string
}

type presenceRehydrateCall struct {
	target presence.RouteTarget
	routes []presence.Route
}

func testInfraPresenceRoute(uid string, sessionID uint64) presence.Route {
	return presence.Route{
		UID:           uid,
		OwnerNodeID:   1,
		OwnerBootID:   23,
		OwnerSeq:      sessionID + 1000,
		SessionID:     sessionID,
		DeviceID:      "device",
		DeviceFlag:    1,
		DeviceLevel:   2,
		Listener:      "tcp",
		ConnectedUnix: 1777777777,
	}
}
