package cluster

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	authoritypresence "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/stretchr/testify/require"
)

func TestPresenceClientUsesLocalAuthorityWhenTargetLeaderIsLocal(t *testing.T) {
	cluster := &fakePresenceCluster{
		nodeID: 1,
		route:  cluster.Route{HashSlot: 7, SlotID: 11, Leader: 1, LeaderTerm: 23, ConfigEpoch: 29, Revision: 17, AuthorityEpoch: 19},
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
		route:  cluster.Route{HashSlot: 9, SlotID: 12, Leader: 2, LeaderTerm: 24, ConfigEpoch: 30, Revision: 18, AuthorityEpoch: 20},
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
		routes: []cluster.Route{
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
		routes: []cluster.Route{
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
		route:  cluster.Route{HashSlot: 7, SlotID: 11, Leader: 1, Revision: 17, AuthorityEpoch: 1},
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
		route:  cluster.Route{HashSlot: 7, SlotID: 11, Leader: 1, Revision: 17, AuthorityEpoch: 1},
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
		route:  cluster.Route{HashSlot: 7, SlotID: 11, Leader: 1, Revision: 17, AuthorityEpoch: 1},
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
		routesByUID: map[string]cluster.Route{
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
		route:  cluster.Route{HashSlot: 7, SlotID: 11, Leader: 1, Revision: 17, AuthorityEpoch: 1},
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
		routes: []cluster.Route{
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

func TestPresenceAuthorityClientResolveRouteTargetsPreservesAlignment(t *testing.T) {
	node := &fakePresenceCluster{
		routeKeyResults: []cluster.RouteKeyResult{
			{Route: cluster.Route{HashSlot: 7, SlotID: 11, Leader: 1, LeaderTerm: 23, ConfigEpoch: 29, Revision: 17, AuthorityEpoch: 19}},
			{Err: cluster.ErrNoSlotLeader},
			{Route: cluster.Route{HashSlot: 9, SlotID: 13, Leader: 2, LeaderTerm: 31, ConfigEpoch: 37, Revision: 41, AuthorityEpoch: 43}},
		},
	}
	client := NewPresenceAuthorityClient(node, &fakePresenceAuthority{})
	uids := []string{"u1", "u2", "u3"}

	results := client.ResolveRouteTargets(context.Background(), uids)

	require.Len(t, results, len(uids))
	require.NoError(t, results[0].Err)
	require.Equal(t, presence.RouteTarget{
		HashSlot:       7,
		SlotID:         11,
		LeaderNodeID:   1,
		LeaderTerm:     23,
		ConfigEpoch:    29,
		RouteRevision:  17,
		AuthorityEpoch: 19,
	}, results[0].Target)
	require.ErrorIs(t, results[1].Err, authoritypresence.ErrRouteNotReady)
	require.Equal(t, presence.RouteTarget{}, results[1].Target)
	require.NoError(t, results[2].Err)
	require.Equal(t, presence.RouteTarget{
		HashSlot:       9,
		SlotID:         13,
		LeaderNodeID:   2,
		LeaderTerm:     31,
		ConfigEpoch:    37,
		RouteRevision:  41,
		AuthorityEpoch: 43,
	}, results[2].Target)
	require.Equal(t, [][]string{uids}, node.routeKeysCalls)
	require.Zero(t, node.routeKeyCalls, "batch resolution must not fall back to RouteKey")
}

func TestPresenceAuthorityClientResolveRouteTargetsFansOutBatchError(t *testing.T) {
	node := &fakePresenceCluster{routeKeysErr: cluster.ErrRouteNotReady}
	client := NewPresenceAuthorityClient(node, &fakePresenceAuthority{})
	uids := []string{"u1", "u2", "u3"}

	results := client.ResolveRouteTargets(context.Background(), uids)

	require.Len(t, results, len(uids))
	for i := range results {
		require.ErrorIs(t, results[i].Err, authoritypresence.ErrRouteNotReady, "result index %d", i)
		require.Equal(t, presence.RouteTarget{}, results[i].Target)
	}
	require.Equal(t, [][]string{uids}, node.routeKeysCalls)
	require.Zero(t, node.routeKeyCalls, "batch resolution must not fall back to RouteKey")
}

func TestPresenceAuthorityClientResolveRouteTargetsFansOutCanceledContext(t *testing.T) {
	node := &fakePresenceCluster{}
	client := NewPresenceAuthorityClient(node, &fakePresenceAuthority{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	uids := []string{"u1", "u2", "u3"}

	results := client.ResolveRouteTargets(ctx, uids)

	require.Len(t, results, len(uids))
	for i := range results {
		require.ErrorIs(t, results[i].Err, context.Canceled, "result index %d", i)
		require.Equal(t, presence.RouteTarget{}, results[i].Target)
	}
	require.Empty(t, node.routeKeysCalls)
	require.Zero(t, node.routeKeyCalls)
}

func TestPresenceAuthorityClientEndpointsByTargetsBatchesRemoteRPCsByLeader(t *testing.T) {
	local := &fakePresenceAuthority{}
	remoteTwo := &fakePresenceAuthority{}
	remoteThree := &fakePresenceAuthority{}
	node := &fakePresenceCluster{
		nodeID: 1,
		rpcByNode: map[uint64]cluster.NodeRPCHandler{
			2: presenceRPCHandler{adapter: accessnode.New(accessnode.Options{Authority: remoteTwo})},
			3: presenceRPCHandler{adapter: accessnode.New(accessnode.Options{Authority: remoteThree})},
		},
	}
	client := NewPresenceAuthorityClient(node, local)

	groups := make([]presence.EndpointLookupGroup, 0, 256)
	for i := 0; i < 256; i++ {
		leader := uint64(i%3 + 1)
		groups = append(groups, presence.EndpointLookupGroup{
			Target: presence.RouteTarget{
				HashSlot:       uint16(i),
				SlotID:         uint32(i%10 + 1),
				LeaderNodeID:   leader,
				LeaderTerm:     100 + leader,
				ConfigEpoch:    200 + leader,
				RouteRevision:  uint64(300 + i),
				AuthorityEpoch: uint64(400 + i),
			},
			UIDs: []string{fmt.Sprintf("u-%03d-a", i), fmt.Sprintf("u-%03d-b", i)},
		})
	}

	results := client.EndpointsByTargets(context.Background(), groups)

	require.Len(t, results, len(groups))
	for i, result := range results {
		require.NoError(t, result.Err, "result index %d", i)
		require.Equal(t, []string{groups[i].UIDs[0], groups[i].UIDs[1]}, []string{result.Routes[0].UID, result.Routes[1].UID})
	}
	require.Len(t, node.calls, 2, "remote RPC count must scale with remote leaders, not UIDs or hash slots")
	require.ElementsMatch(t, []uint64{2, 3}, []uint64{node.calls[0].nodeID, node.calls[1].nodeID})
	require.Zero(t, node.routeKeyCalls, "exact-target lookup must not resolve RouteKey again")
	require.Empty(t, node.routeKeysCalls, "exact-target lookup must not resolve RouteKeysPartial again")

	gotGroups := make(map[presence.RouteTarget][]string, len(groups))
	for _, authority := range []*fakePresenceAuthority{local, remoteTwo, remoteThree} {
		for _, call := range authority.endpointBatchCalls {
			gotGroups[call.target] = call.uids
		}
	}
	require.Len(t, gotGroups, len(groups))
	for _, group := range groups {
		require.Equal(t, group.UIDs, gotGroups[group.Target], "full authority fence must survive local and remote batching")
	}
}

func TestPresenceAuthorityClientEndpointsByTargetsKeepsGroupErrorsPartialAndAligned(t *testing.T) {
	local := &fakePresenceAuthority{}
	remoteTwo := &fakePresenceAuthority{endpointBatchErrs: map[uint16]error{22: context.Canceled}}
	remoteThree := &fakePresenceAuthority{}
	node := &fakePresenceCluster{
		nodeID: 1,
		rpcByNode: map[uint64]cluster.NodeRPCHandler{
			2: presenceRPCHandler{adapter: accessnode.New(accessnode.Options{Authority: remoteTwo})},
			3: presenceRPCHandler{adapter: accessnode.New(accessnode.Options{Authority: remoteThree})},
		},
	}
	client := NewPresenceAuthorityClient(node, local)
	groups := []presence.EndpointLookupGroup{
		{Target: testEndpointLookupTarget(11, 1), UIDs: []string{"local"}},
		{Target: testEndpointLookupTarget(22, 2), UIDs: []string{"stale"}},
		{Target: testEndpointLookupTarget(23, 2), UIDs: []string{"remote-two"}},
		{Target: testEndpointLookupTarget(33, 3), UIDs: []string{"remote-three"}},
	}

	results := client.EndpointsByTargets(context.Background(), groups)

	require.Len(t, results, len(groups))
	require.Equal(t, "local", results[0].Routes[0].UID)
	require.NoError(t, results[0].Err)
	require.Empty(t, results[1].Routes)
	require.ErrorIs(t, results[1].Err, context.Canceled)
	require.Equal(t, "remote-two", results[2].Routes[0].UID)
	require.NoError(t, results[2].Err)
	require.Equal(t, "remote-three", results[3].Routes[0].UID)
	require.NoError(t, results[3].Err)
	require.Len(t, node.calls, 2)
	require.Zero(t, node.routeKeyCalls)
	require.Empty(t, node.routeKeysCalls)
}

func TestPresenceAuthorityClientEndpointsByTargetsReroutesOnlyStaleGroupOnce(t *testing.T) {
	local := &fakePresenceAuthority{}
	remoteTwo := &fakePresenceAuthority{endpointBatchErrs: map[uint16]error{22: authoritypresence.ErrStaleRoute}}
	remoteThree := &fakePresenceAuthority{}
	fresh := cluster.Route{
		HashSlot:       22,
		SlotID:         3,
		Leader:         3,
		LeaderTerm:     303,
		ConfigEpoch:    403,
		Revision:       503,
		AuthorityEpoch: 603,
	}
	node := &fakePresenceCluster{
		nodeID:          1,
		routeKeyResults: []cluster.RouteKeyResult{{Route: fresh}},
		rpcByNode: map[uint64]cluster.NodeRPCHandler{
			2: presenceRPCHandler{adapter: accessnode.New(accessnode.Options{Authority: remoteTwo})},
			3: presenceRPCHandler{adapter: accessnode.New(accessnode.Options{Authority: remoteThree})},
		},
	}
	client := NewPresenceAuthorityClient(node, local)
	groups := []presence.EndpointLookupGroup{
		{Target: testEndpointLookupTarget(11, 1), UIDs: []string{"local"}},
		{Target: testEndpointLookupTarget(22, 2), UIDs: []string{"stale"}},
		{Target: testEndpointLookupTarget(23, 2), UIDs: []string{"remote-two"}},
		{Target: testEndpointLookupTarget(33, 3), UIDs: []string{"remote-three"}},
	}

	results := client.EndpointsByTargets(context.Background(), groups)

	require.Len(t, results, len(groups))
	for i, result := range results {
		require.NoError(t, result.Err, "result index %d", i)
		require.Len(t, result.Routes, 1, "result index %d", i)
	}
	require.Equal(t, [][]string{{"stale"}}, node.routeKeysCalls, "only the stale group may be rerouted")
	require.Zero(t, node.routeKeyCalls)
	require.Len(t, node.calls, 3, "two initial leaders plus one bounded stale-group retry")
	require.Equal(t, []presenceEndpointBatchCall{
		{target: groups[1].Target, uids: []string{"stale"}},
		{target: groups[2].Target, uids: []string{"remote-two"}},
	}, remoteTwo.endpointBatchCalls)
	require.Equal(t, []presenceEndpointBatchCall{
		{target: groups[3].Target, uids: []string{"remote-three"}},
		{target: routeTargetFromClusterRoute(fresh), uids: []string{"stale"}},
	}, remoteThree.endpointBatchCalls)
}

func testEndpointLookupTarget(hashSlot uint16, leader uint64) presence.RouteTarget {
	return presence.RouteTarget{
		HashSlot:       hashSlot,
		SlotID:         uint32(hashSlot%10 + 1),
		LeaderNodeID:   leader,
		LeaderTerm:     101 + leader,
		ConfigEpoch:    201 + leader,
		RouteRevision:  301 + uint64(hashSlot),
		AuthorityEpoch: 401 + uint64(hashSlot),
	}
}

func TestPresenceClientReturnsRouteNotReadyWhenRouteKeyFails(t *testing.T) {
	cluster := &fakePresenceCluster{
		nodeID:   1,
		routeErr: cluster.ErrRouteNotReady,
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
		routeErr: cluster.ErrNoSlotLeader,
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
	nodeID          uint64
	route           cluster.Route
	routesByUID     map[string]cluster.Route
	routes          []cluster.Route
	routeErr        error
	routeKeyResults []cluster.RouteKeyResult
	routeKeysErr    error
	rpc             cluster.NodeRPCHandler
	rpcByNode       map[uint64]cluster.NodeRPCHandler
	calls           []rpcCall
	routeKeyCalls   int
	routeKeysCalls  [][]string
	registered      map[uint8]cluster.NodeRPCHandler
	watch           chan cluster.RouteAuthorityEvent
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

func (f *fakePresenceCluster) RouteKey(uid string) (cluster.Route, error) {
	f.routeKeyCalls++
	if f.routeErr != nil {
		return cluster.Route{}, f.routeErr
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

func (f *fakePresenceCluster) RouteKeysPartial(uids []string) ([]cluster.RouteKeyResult, error) {
	f.routeKeysCalls = append(f.routeKeysCalls, append([]string(nil), uids...))
	if f.routeKeysErr != nil {
		return nil, f.routeKeysErr
	}
	return append([]cluster.RouteKeyResult(nil), f.routeKeyResults...), nil
}

func (f *fakePresenceCluster) RouteHashSlot(uint16) (cluster.Route, error) {
	if f.routeErr != nil {
		return cluster.Route{}, f.routeErr
	}
	return f.route, nil
}

func (f *fakePresenceCluster) CallRPC(ctx context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	f.calls = append(f.calls, rpcCall{nodeID: nodeID, serviceID: serviceID, payload: append([]byte(nil), payload...)})
	if handler := f.rpcByNode[nodeID]; handler != nil {
		return handler.HandleRPC(ctx, payload)
	}
	if f.rpc != nil {
		return f.rpc.HandleRPC(ctx, payload)
	}
	if handler := f.registered[serviceID]; handler != nil {
		return handler.HandleRPC(ctx, payload)
	}
	return nil, errors.New("missing rpc handler")
}

func (f *fakePresenceCluster) RegisterRPC(serviceID uint8, handler cluster.NodeRPCHandler) {
	if f.registered == nil {
		f.registered = make(map[uint8]cluster.NodeRPCHandler)
	}
	f.registered[serviceID] = handler
}

func (f *fakePresenceCluster) WatchRouteAuthorities() <-chan cluster.RouteAuthorityEvent {
	if f.watch == nil {
		f.watch = make(chan cluster.RouteAuthorityEvent)
	}
	return f.watch
}

type fakePresenceAuthority struct {
	registerResult     presence.RegisterResult
	registerErrs       []error
	registerHook       func() error
	commitErrs         []error
	abortErrs          []error
	registerCalls      []presenceRegisterCall
	commitCalls        []presenceTokenCall
	abortCalls         []presenceTokenCall
	unregCalls         []presenceUnregisterCall
	endpointCalls      []presenceEndpointCall
	endpointBatchCalls []presenceEndpointBatchCall
	endpointBatchErrs  map[uint16]error
	touchCalls         []presenceTouchCall
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

func (f *fakePresenceAuthority) EndpointsByUIDs(_ context.Context, target presence.RouteTarget, uids []string) ([]presence.Route, error) {
	f.endpointBatchCalls = append(f.endpointBatchCalls, presenceEndpointBatchCall{
		target: target,
		uids:   append([]string(nil), uids...),
	})
	if err := f.endpointBatchErrs[target.HashSlot]; err != nil {
		return nil, err
	}
	routes := make([]presence.Route, 0, len(uids))
	for i, uid := range uids {
		routes = append(routes, testInfraPresenceRoute(uid, uint64(i+1)))
	}
	return routes, nil
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

type presenceEndpointBatchCall struct {
	target presence.RouteTarget
	uids   []string
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
