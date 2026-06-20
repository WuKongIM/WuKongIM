package cluster

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	accessnode "github.com/WuKongIM/WuKongIM/internalv2/access/node"
	authoritypresence "github.com/WuKongIM/WuKongIM/internalv2/runtime/presence"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
	"github.com/stretchr/testify/require"
)

func TestPresenceClientUsesLocalAuthorityWhenTargetLeaderIsLocal(t *testing.T) {
	cluster := &fakePresenceCluster{
		nodeID: 1,
		route:  clusterv2.Route{HashSlot: 7, SlotID: 11, Leader: 1, LeaderTerm: 23, ConfigEpoch: 29, Revision: 17, AuthorityEpoch: 19},
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
	wantTarget := presence.RouteTarget{HashSlot: 7, SlotID: 11, LeaderNodeID: 1, LeaderTerm: 23, ConfigEpoch: 29, RouteRevision: 17, AuthorityEpoch: 19}
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
		route:  clusterv2.Route{HashSlot: 9, SlotID: 12, Leader: 2, LeaderTerm: 24, ConfigEpoch: 30, Revision: 18, AuthorityEpoch: 20},
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
	wantTarget := presence.RouteTarget{HashSlot: 9, SlotID: 12, LeaderNodeID: 2, LeaderTerm: 24, ConfigEpoch: 30, RouteRevision: 18, AuthorityEpoch: 20}
	if !reflect.DeepEqual(remoteAuthority.registerCalls[0].target, wantTarget) {
		t.Fatalf("remote target = %#v, want %#v", remoteAuthority.registerCalls[0].target, wantTarget)
	}
}

func TestPresenceClientRoutesOwnerActionToRemoteOwnerNode(t *testing.T) {
	remoteOwner := &fakePresenceOwner{}
	cluster := &fakePresenceCluster{
		nodeID: 1,
		rpc:    presenceOwnerRPCHandler{adapter: accessnode.New(accessnode.Options{Owner: remoteOwner})},
	}
	client := NewPresenceAuthorityClient(cluster, &fakePresenceAuthority{})
	action := presence.RouteAction{UID: "u1", OwnerNodeID: 2, OwnerBootID: 23, SessionID: 101, Kind: "close", Reason: "presence_conflict"}

	if err := client.ApplyRouteAction(context.Background(), action); err != nil {
		t.Fatalf("ApplyRouteAction() error = %v", err)
	}
	if len(remoteOwner.actions) != 1 || !reflect.DeepEqual(remoteOwner.actions[0], action) {
		t.Fatalf("remote owner actions = %#v, want %#v", remoteOwner.actions, action)
	}
	if len(cluster.calls) != 1 {
		t.Fatalf("remote rpc calls = %d, want 1", len(cluster.calls))
	}
	if cluster.calls[0].nodeID != 2 || cluster.calls[0].serviceID != accessnode.PresenceOwnerRPCServiceID {
		t.Fatalf("remote rpc call = %#v", cluster.calls[0])
	}
}

func TestPresenceClientRoutesOwnerActionToLocalOwner(t *testing.T) {
	localOwner := &fakePresenceOwner{}
	cluster := &fakePresenceCluster{nodeID: 1}
	client := NewPresenceAuthorityClient(cluster, &fakePresenceAuthority{})
	client.SetLocalOwner(localOwner)
	action := presence.RouteAction{UID: "u1", OwnerNodeID: 1, OwnerBootID: 23, SessionID: 101, Kind: "close", Reason: "presence_conflict"}

	if err := client.ApplyRouteAction(context.Background(), action); err != nil {
		t.Fatalf("ApplyRouteAction() error = %v", err)
	}
	if len(localOwner.actions) != 1 || !reflect.DeepEqual(localOwner.actions[0], action) {
		t.Fatalf("local owner actions = %#v, want %#v", localOwner.actions, action)
	}
	if len(cluster.calls) != 0 {
		t.Fatalf("remote rpc calls = %d, want 0", len(cluster.calls))
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

func TestPresenceClientRetriesRouteResolutionWhenRouteNotReady(t *testing.T) {
	cluster := &fakePresenceCluster{
		nodeID: 1,
		routes: []clusterv2.Route{
			{HashSlot: 7, SlotID: 11, Leader: 0, Revision: 17, AuthorityEpoch: 1},
			{HashSlot: 7, SlotID: 11, Leader: 1, Revision: 18, AuthorityEpoch: 2},
		},
	}
	local := &fakePresenceAuthority{}
	client := NewPresenceAuthorityClient(cluster, local)

	if _, err := client.RegisterRoute(context.Background(), testInfraPresenceRoute("u1", 101)); err != nil {
		t.Fatalf("RegisterRoute() error = %v", err)
	}
	if cluster.routeKeyCalls != 2 {
		t.Fatalf("RouteKey calls = %d, want 2", cluster.routeKeyCalls)
	}
	if len(local.registerCalls) != 1 {
		t.Fatalf("local register calls = %d, want 1", len(local.registerCalls))
	}
	if got := local.registerCalls[0].target.RouteRevision; got != 18 {
		t.Fatalf("retry RouteRevision = %d, want 18", got)
	}
}

func TestPresenceClientBacksOffBeforeRetryingTransientNotLeader(t *testing.T) {
	cluster := &fakePresenceCluster{
		nodeID: 1,
		route:  clusterv2.Route{HashSlot: 7, SlotID: 11, Leader: 1, Revision: 17, AuthorityEpoch: 1},
	}
	allowRetry := false
	local := &fakePresenceAuthority{
		registerHook: func() error {
			if !allowRetry {
				return authoritypresence.ErrNotLeader
			}
			return nil
		},
	}
	client := NewPresenceAuthorityClient(cluster, local)
	client.routeRetryBackoff = time.Millisecond
	sleepCalls := 0
	client.routeRetrySleep = func(ctx context.Context, d time.Duration) error {
		sleepCalls++
		if d != time.Millisecond {
			t.Fatalf("retry backoff = %s, want 1ms", d)
		}
		allowRetry = true
		return nil
	}

	if _, err := client.RegisterRoute(context.Background(), testInfraPresenceRoute("u1", 101)); err != nil {
		t.Fatalf("RegisterRoute() error = %v", err)
	}
	if sleepCalls != 1 {
		t.Fatalf("retry sleeps = %d, want 1", sleepCalls)
	}
	if len(local.registerCalls) != 2 {
		t.Fatalf("local register calls = %d, want 2", len(local.registerCalls))
	}
}

func TestPresenceClientRidesOutShortNotLeaderBurst(t *testing.T) {
	cluster := &fakePresenceCluster{
		nodeID: 1,
		route:  clusterv2.Route{HashSlot: 7, SlotID: 11, Leader: 1, Revision: 17, AuthorityEpoch: 1},
	}
	local := &fakePresenceAuthority{
		registerErrs: []error{
			authoritypresence.ErrNotLeader,
			authoritypresence.ErrNotLeader,
			authoritypresence.ErrNotLeader,
			authoritypresence.ErrNotLeader,
			authoritypresence.ErrNotLeader,
			authoritypresence.ErrNotLeader,
			nil,
		},
	}
	client := NewPresenceAuthorityClient(cluster, local)
	sleepCalls := 0
	client.routeRetrySleep = func(context.Context, time.Duration) error {
		sleepCalls++
		return nil
	}

	if _, err := client.RegisterRoute(context.Background(), testInfraPresenceRoute("u1", 101)); err != nil {
		t.Fatalf("RegisterRoute() error = %v", err)
	}
	if sleepCalls != 6 {
		t.Fatalf("retry sleeps = %d, want 6", sleepCalls)
	}
	if len(local.registerCalls) != 7 {
		t.Fatalf("local register calls = %d, want 7", len(local.registerCalls))
	}
}

func TestPresenceClientRidesOutLongerNotLeaderBurstWithinActivationWindow(t *testing.T) {
	cluster := &fakePresenceCluster{
		nodeID: 1,
		route:  clusterv2.Route{HashSlot: 7, SlotID: 11, Leader: 1, Revision: 17, AuthorityEpoch: 1},
	}
	const notLeaderCount = 30
	local := &fakePresenceAuthority{
		registerErrs: append(repeatedPresenceErrors(authoritypresence.ErrNotLeader, notLeaderCount), nil),
	}
	client := NewPresenceAuthorityClient(cluster, local)
	sleepCalls := 0
	client.routeRetrySleep = func(context.Context, time.Duration) error {
		sleepCalls++
		return nil
	}

	if _, err := client.RegisterRoute(context.Background(), testInfraPresenceRoute("u1", 101)); err != nil {
		t.Fatalf("RegisterRoute() error = %v", err)
	}
	if sleepCalls != notLeaderCount {
		t.Fatalf("retry sleeps = %d, want %d", sleepCalls, notLeaderCount)
	}
	if len(local.registerCalls) != notLeaderCount+1 {
		t.Fatalf("local register calls = %d, want %d", len(local.registerCalls), notLeaderCount+1)
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

func TestPresenceClientKeepsPendingTokenWhenCommitRouteNotReady(t *testing.T) {
	cluster := &fakePresenceCluster{
		nodeID: 1,
		route:  clusterv2.Route{HashSlot: 7, SlotID: 11, Leader: 1, Revision: 17, AuthorityEpoch: 1},
	}
	local := &fakePresenceAuthority{
		registerResult: presence.RegisterResult{PendingToken: "pending-1"},
		commitErrs:     []error{authoritypresence.ErrRouteNotReady},
	}
	client := NewPresenceAuthorityClient(cluster, local)

	result, err := client.RegisterRoute(context.Background(), testInfraPresenceRoute("u1", 101))
	if err != nil {
		t.Fatalf("RegisterRoute() error = %v", err)
	}
	if err := client.CommitRoute(context.Background(), result.PendingToken); !errors.Is(err, authoritypresence.ErrRouteNotReady) {
		t.Fatalf("CommitRoute() error = %v, want ErrRouteNotReady", err)
	}
	if err := client.AbortRoute(context.Background(), result.PendingToken); err != nil {
		t.Fatalf("AbortRoute() after commit ErrRouteNotReady error = %v", err)
	}
	if len(local.abortCalls) != 1 {
		t.Fatalf("abort calls = %d, want 1", len(local.abortCalls))
	}
	if local.abortCalls[0].token != "pending-1" {
		t.Fatalf("abort raw token = %q, want pending-1", local.abortCalls[0].token)
	}
}

func TestPresenceClientForgetsPendingTokenWhenAbortReachedAuthorityBeforeRouteLoss(t *testing.T) {
	cluster := &fakePresenceCluster{
		nodeID: 1,
		routes: []clusterv2.Route{
			{HashSlot: 7, SlotID: 11, Leader: 1, Revision: 17, AuthorityEpoch: 1},
			{HashSlot: 7, SlotID: 11, Leader: 1, Revision: 18, AuthorityEpoch: 2},
			{HashSlot: 7, SlotID: 11, Leader: 0, Revision: 19, AuthorityEpoch: 3},
		},
	}
	local := &fakePresenceAuthority{
		registerResult: presence.RegisterResult{PendingToken: "pending-1"},
		abortErrs:      []error{authoritypresence.ErrNotLeader},
	}
	client := NewPresenceAuthorityClient(cluster, local)

	result, err := client.RegisterRoute(context.Background(), testInfraPresenceRoute("u1", 101))
	if err != nil {
		t.Fatalf("RegisterRoute() error = %v", err)
	}
	if err := client.AbortRoute(context.Background(), result.PendingToken); !errors.Is(err, authoritypresence.ErrRouteNotReady) {
		t.Fatalf("AbortRoute() error = %v, want ErrRouteNotReady", err)
	}
	if _, ok := client.pendingRef(result.PendingToken); ok {
		t.Fatal("pending token remains after abort reached authority before route loss")
	}
	if len(local.abortCalls) != 1 {
		t.Fatalf("abort calls = %d, want 1", len(local.abortCalls))
	}
}

func TestPresenceAuthorityClientTouchRoutesToLocal(t *testing.T) {
	node := &fakePresenceCluster{nodeID: 1}
	local := &fakePresenceAuthority{}
	client := NewPresenceAuthorityClient(node, local)
	target := presence.RouteTarget{HashSlot: 7, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	routes := []presence.Route{{UID: "u1", OwnerNodeID: 1, OwnerBootID: 2, OwnerSeq: 3, SessionID: 4, LastSeenUnix: 50}}

	require.NoError(t, client.TouchRoutesTo(context.Background(), target, routes))
	require.Len(t, local.touchCalls, 1)
	require.Equal(t, target, local.touchCalls[0].target)
	require.Equal(t, routes, local.touchCalls[0].routes)
}

func TestPresenceAuthorityClientTouchRoutesToRemote(t *testing.T) {
	remoteAuthority := &fakePresenceAuthority{}
	node := &fakePresenceCluster{
		nodeID: 1,
		rpc:    presenceRPCHandler{adapter: accessnode.New(accessnode.Options{Authority: remoteAuthority})},
	}
	client := NewPresenceAuthorityClient(node, &fakePresenceAuthority{})
	target := presence.RouteTarget{HashSlot: 7, SlotID: 2, LeaderNodeID: 2, RouteRevision: 3, AuthorityEpoch: 4}
	routes := []presence.Route{{UID: "u1", OwnerNodeID: 1, OwnerBootID: 2, OwnerSeq: 3, SessionID: 4, LastSeenUnix: 50}}

	require.NoError(t, client.TouchRoutesTo(context.Background(), target, routes))
	require.Len(t, remoteAuthority.touchCalls, 1)
	require.Equal(t, target, remoteAuthority.touchCalls[0].target)
	require.Equal(t, routes, remoteAuthority.touchCalls[0].routes)
	require.Len(t, node.calls, 1)
	require.Equal(t, uint64(2), node.calls[0].nodeID)
	require.Equal(t, accessnode.PresenceAuthorityRPCServiceID, node.calls[0].serviceID)
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

type presenceOwnerRPCHandler struct {
	adapter *accessnode.Adapter
}

func (h presenceOwnerRPCHandler) HandleRPC(ctx context.Context, payload []byte) ([]byte, error) {
	return h.adapter.HandlePresenceOwnerRPC(ctx, payload)
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
	registerHook   func() error
	commitErrs     []error
	abortErrs      []error
	registerCalls  []presenceRegisterCall
	commitCalls    []presenceTokenCall
	abortCalls     []presenceTokenCall
	unregCalls     []presenceUnregisterCall
	endpointCalls  []presenceEndpointCall
	touchCalls     []presenceTouchCall
}

type fakePresenceOwner struct {
	actions []presence.RouteAction
}

func (f *fakePresenceOwner) ApplyRouteAction(_ context.Context, action presence.RouteAction) error {
	f.actions = append(f.actions, action)
	return nil
}

func (f *fakePresenceAuthority) RegisterRoute(_ context.Context, target presence.RouteTarget, route presence.Route) (presence.RegisterResult, error) {
	f.registerCalls = append(f.registerCalls, presenceRegisterCall{target: target, route: route})
	if f.registerHook != nil {
		if err := f.registerHook(); err != nil {
			return presence.RegisterResult{}, err
		}
	}
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
	if len(f.commitErrs) > 0 {
		err := f.commitErrs[0]
		f.commitErrs = f.commitErrs[1:]
		return err
	}
	return nil
}

func (f *fakePresenceAuthority) AbortRoute(_ context.Context, target presence.RouteTarget, token string) error {
	f.abortCalls = append(f.abortCalls, presenceTokenCall{target: target, token: token})
	if len(f.abortErrs) > 0 {
		err := f.abortErrs[0]
		f.abortErrs = f.abortErrs[1:]
		return err
	}
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

func (f *fakePresenceAuthority) TouchRoutes(_ context.Context, target presence.RouteTarget, routes []presence.Route) error {
	f.touchCalls = append(f.touchCalls, presenceTouchCall{target: target, routes: append([]presence.Route(nil), routes...)})
	return nil
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

type presenceTouchCall struct {
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

func repeatedPresenceErrors(err error, count int) []error {
	errs := make([]error, count)
	for i := range errs {
		errs[i] = err
	}
	return errs
}
