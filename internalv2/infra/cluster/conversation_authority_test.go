package cluster

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	accessnode "github.com/WuKongIM/WuKongIM/internalv2/access/node"
	conversationusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/conversation"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestConversationAuthorityClientUsesLocalAuthority(t *testing.T) {
	local := &fakeConversationAuthorityLocal{}
	node := &fakeConversationAuthorityNode{nodeID: 1, route: clusterv2.Route{HashSlot: 7, SlotID: 2, Leader: 1, Revision: 3, AuthorityEpoch: 4}}
	client := NewConversationAuthorityClient(node, local)
	patch := conversationusecase.ActivePatch{UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAt: 100, MessageSeq: 1}
	if err := client.AdmitPatches(context.Background(), []conversationusecase.ActivePatch{patch}); err != nil {
		t.Fatalf("AdmitPatches() error = %v", err)
	}
	if len(local.patches) != 1 || !reflect.DeepEqual(local.patches[0], patch) {
		t.Fatalf("local patches = %#v, want %#v", local.patches, patch)
	}
}

func TestConversationAuthorityClientRoutesRemoteList(t *testing.T) {
	remoteAuthority := &fakeConversationAuthorityLocal{page: conversationusecase.ActiveViewPage{
		Rows: []metadb.UserConversationState{{UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAt: 100}},
		Done: true,
	}}
	adapter := accessnode.New(accessnode.Options{ConversationAuthority: remoteAuthority})
	node := &fakeConversationAuthorityNode{
		nodeID: 1,
		route:  clusterv2.Route{HashSlot: 7, SlotID: 2, Leader: 2, Revision: 3, AuthorityEpoch: 4},
		handler: nodeRPCHandlerFunc(func(ctx context.Context, payload []byte) ([]byte, error) {
			return adapter.HandleConversationAuthorityRPC(ctx, payload)
		}),
	}
	client := NewConversationAuthorityClient(node, nil)
	page, err := client.ListUserConversationActiveView(context.Background(), "u1", metadb.UserConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListUserConversationActiveView() error = %v", err)
	}
	if len(page.Rows) != 1 || page.Rows[0].ChannelID != "g1" {
		t.Fatalf("page = %#v, want remote row", page)
	}
}

func TestConversationAuthorityClientGroupsByExactTarget(t *testing.T) {
	local := &fakeConversationAuthorityLocal{}
	node := &fakeConversationAuthorityNode{
		nodeID: 1,
		routesByUID: map[string]clusterv2.Route{
			"u1": {HashSlot: 1, SlotID: 2, Leader: 1, Revision: 10, AuthorityEpoch: 20},
			"u2": {HashSlot: 2, SlotID: 2, Leader: 1, Revision: 11, AuthorityEpoch: 21},
		},
	}
	client := NewConversationAuthorityClient(node, local)
	err := client.AdmitPatches(context.Background(), []conversationusecase.ActivePatch{
		{UID: "u1", ChannelID: "g", ChannelType: 2, ActiveAt: 10},
		{UID: "u2", ChannelID: "g", ChannelType: 2, ActiveAt: 20},
	})
	if err != nil {
		t.Fatalf("AdmitPatches() error = %v", err)
	}
	if len(local.targets) != 2 {
		t.Fatalf("targets = %#v, want two exact route targets", local.targets)
	}
}

func TestConversationAuthorityClientGroupsByFullTargetFields(t *testing.T) {
	local := &fakeConversationAuthorityLocal{}
	node := &fakeConversationAuthorityNode{
		nodeID: 1,
		routesByUID: map[string]clusterv2.Route{
			"u1": {HashSlot: 1, SlotID: 2, Leader: 1, Revision: 10, AuthorityEpoch: 20},
			"u2": {HashSlot: 1, SlotID: 2, Leader: 1, Revision: 11, AuthorityEpoch: 20},
			"u3": {HashSlot: 1, SlotID: 2, Leader: 1, Revision: 10, AuthorityEpoch: 21},
		},
	}
	client := NewConversationAuthorityClient(node, local)

	err := client.AdmitPatches(context.Background(), []conversationusecase.ActivePatch{
		{UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAt: 10},
		{UID: "u2", ChannelID: "g2", ChannelType: 2, ActiveAt: 20},
		{UID: "u3", ChannelID: "g3", ChannelType: 2, ActiveAt: 30},
	})
	if err != nil {
		t.Fatalf("AdmitPatches() error = %v", err)
	}
	if len(local.targets) != 3 {
		t.Fatalf("targets = %#v, want revision/epoch to split exact targets", local.targets)
	}
}

func TestConversationAuthorityClientGroupByTargetUsesEveryFencingField(t *testing.T) {
	base := clusterv2.Route{HashSlot: 1, SlotID: 2, Leader: 1, Revision: 10, AuthorityEpoch: 20}
	tests := []struct {
		name  string
		other clusterv2.Route
	}{
		{name: "hash slot", other: clusterv2.Route{HashSlot: 2, SlotID: 2, Leader: 1, Revision: 10, AuthorityEpoch: 20}},
		{name: "slot id", other: clusterv2.Route{HashSlot: 1, SlotID: 3, Leader: 1, Revision: 10, AuthorityEpoch: 20}},
		{name: "leader node", other: clusterv2.Route{HashSlot: 1, SlotID: 2, Leader: 2, Revision: 10, AuthorityEpoch: 20}},
		{name: "route revision", other: clusterv2.Route{HashSlot: 1, SlotID: 2, Leader: 1, Revision: 11, AuthorityEpoch: 20}},
		{name: "authority epoch", other: clusterv2.Route{HashSlot: 1, SlotID: 2, Leader: 1, Revision: 10, AuthorityEpoch: 21}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := &fakeConversationAuthorityNode{
				nodeID: 1,
				routesByUID: map[string]clusterv2.Route{
					"u1": base,
					"u2": tt.other,
				},
			}
			client := NewConversationAuthorityClient(node, &fakeConversationAuthorityLocal{})

			groups, err := client.groupByTarget([]conversationusecase.ActivePatch{
				{UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAt: 10},
				{UID: "u2", ChannelID: "g2", ChannelType: 2, ActiveAt: 20},
			})
			if err != nil {
				t.Fatalf("groupByTarget() error = %v", err)
			}
			if len(groups) != 2 {
				t.Fatalf("groups = %#v, want two groups when %s differs", groups, tt.name)
			}
		})
	}
}

func TestConversationAuthorityClientCoalescesIdenticalExactTargets(t *testing.T) {
	local := &fakeConversationAuthorityLocal{}
	node := &fakeConversationAuthorityNode{
		nodeID: 1,
		routesByUID: map[string]clusterv2.Route{
			"u1": {HashSlot: 1, SlotID: 2, Leader: 1, Revision: 10, AuthorityEpoch: 20},
			"u2": {HashSlot: 1, SlotID: 2, Leader: 1, Revision: 10, AuthorityEpoch: 20},
		},
	}
	client := NewConversationAuthorityClient(node, local)

	err := client.AdmitPatches(context.Background(), []conversationusecase.ActivePatch{
		{UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAt: 10},
		{UID: "u2", ChannelID: "g2", ChannelType: 2, ActiveAt: 20},
	})
	if err != nil {
		t.Fatalf("AdmitPatches() error = %v", err)
	}
	if len(local.targets) != 1 {
		t.Fatalf("targets = %#v, want one coalesced exact target", local.targets)
	}
	if got := deliveredConversationPatchUIDCounts(local.deliveredPatches); got["u1"] != 1 || got["u2"] != 1 {
		t.Fatalf("delivered patch counts = %#v, want both patches in one group", got)
	}
}

func TestConversationAuthorityClientAdmitPatchesDoesNotRetryStaleRoute(t *testing.T) {
	local := &fakeConversationAuthorityLocal{admitErrs: []error{conversationusecase.ErrStaleRoute, nil}}
	node := &fakeConversationAuthorityNode{
		nodeID: 1,
		routes: []clusterv2.Route{
			{HashSlot: 1, SlotID: 2, Leader: 1, Revision: 10, AuthorityEpoch: 20},
			{HashSlot: 1, SlotID: 2, Leader: 1, Revision: 11, AuthorityEpoch: 21},
		},
	}
	client := NewConversationAuthorityClient(node, local)
	client.routeRetrySleep = func(context.Context, time.Duration) error {
		t.Fatal("AdmitPatches should not sleep between retries")
		return nil
	}

	err := client.AdmitPatches(context.Background(), []conversationusecase.ActivePatch{{UID: "u1", ChannelID: "g", ChannelType: 2, ActiveAt: 10}})
	if !errors.Is(err, conversationusecase.ErrStaleRoute) {
		t.Fatalf("AdmitPatches() error = %v, want ErrStaleRoute", err)
	}
	if node.routeKeyCalls != 1 {
		t.Fatalf("RouteKey calls = %d, want 1", node.routeKeyCalls)
	}
	if len(local.targets) != 1 || local.targets[0].RouteRevision != 10 {
		t.Fatalf("targets = %#v, want one admit with original route revision 10", local.targets)
	}
}

func TestConversationAuthorityClientAdmitPatchesDoesNotRetryRouteNotReady(t *testing.T) {
	local := &fakeConversationAuthorityLocal{admitErrs: []error{conversationusecase.ErrRouteNotReady, nil}}
	node := &fakeConversationAuthorityNode{
		nodeID: 1,
		routes: []clusterv2.Route{
			{HashSlot: 1, SlotID: 2, Leader: 1, Revision: 10, AuthorityEpoch: 20},
			{HashSlot: 1, SlotID: 2, Leader: 1, Revision: 11, AuthorityEpoch: 21},
		},
	}
	client := NewConversationAuthorityClient(node, local)
	client.routeRetrySleep = func(context.Context, time.Duration) error {
		t.Fatal("AdmitPatches should not sleep between retries")
		return nil
	}

	err := client.AdmitPatches(context.Background(), []conversationusecase.ActivePatch{{UID: "u1", ChannelID: "g", ChannelType: 2, ActiveAt: 10}})
	if !errors.Is(err, conversationusecase.ErrRouteNotReady) {
		t.Fatalf("AdmitPatches() error = %v, want ErrRouteNotReady", err)
	}
	if node.routeKeyCalls != 1 {
		t.Fatalf("RouteKey calls = %d, want 1", node.routeKeyCalls)
	}
	if len(local.targets) != 1 || local.targets[0].RouteRevision != 10 {
		t.Fatalf("targets = %#v, want one admit with original route revision 10", local.targets)
	}
}

func TestConversationAuthorityClientRetriesRouteNotReadyListWithFreshRoute(t *testing.T) {
	local := &fakeConversationAuthorityLocal{
		listErrs: []error{conversationusecase.ErrRouteNotReady, nil},
		page: conversationusecase.ActiveViewPage{
			Rows: []metadb.UserConversationState{{UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAt: 100}},
			Done: true,
		},
	}
	node := &fakeConversationAuthorityNode{
		nodeID: 1,
		routes: []clusterv2.Route{
			{HashSlot: 1, SlotID: 2, Leader: 1, Revision: 10, AuthorityEpoch: 20},
			{HashSlot: 1, SlotID: 2, Leader: 1, Revision: 11, AuthorityEpoch: 21},
		},
	}
	client := NewConversationAuthorityClient(node, local)
	client.routeRetrySleep = func(context.Context, time.Duration) error { return nil }

	page, err := client.ListUserConversationActiveView(context.Background(), "u1", metadb.UserConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListUserConversationActiveView() error = %v", err)
	}
	if node.routeKeyCalls != 2 {
		t.Fatalf("RouteKey calls = %d, want 2", node.routeKeyCalls)
	}
	if len(local.targets) != 2 || local.targets[1].RouteRevision != 11 {
		t.Fatalf("targets = %#v, want retry with fresh route revision 11", local.targets)
	}
	if len(page.Rows) != 1 || page.Rows[0].ChannelID != "g1" {
		t.Fatalf("page = %#v, want retried row", page)
	}
}

func TestConversationAuthorityClientRetriesRawRemoteRouteErrorWithFreshRoute(t *testing.T) {
	remoteAuthority := &fakeConversationAuthorityLocal{page: conversationusecase.ActiveViewPage{
		Rows: []metadb.UserConversationState{{UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAt: 100}},
		Done: true,
	}}
	adapter := accessnode.New(accessnode.Options{ConversationAuthority: remoteAuthority})
	node := &fakeConversationAuthorityNode{
		nodeID: 1,
		routes: []clusterv2.Route{
			{HashSlot: 1, SlotID: 2, Leader: 2, Revision: 10, AuthorityEpoch: 20},
			{HashSlot: 1, SlotID: 2, Leader: 2, Revision: 11, AuthorityEpoch: 21},
		},
		rpcErrs: []error{clusterv2.ErrNotLeader, nil},
		handler: nodeRPCHandlerFunc(func(ctx context.Context, payload []byte) ([]byte, error) {
			return adapter.HandleConversationAuthorityRPC(ctx, payload)
		}),
	}
	client := NewConversationAuthorityClient(node, nil)
	client.routeRetrySleep = func(context.Context, time.Duration) error { return nil }

	page, err := client.ListUserConversationActiveView(context.Background(), "u1", metadb.UserConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListUserConversationActiveView() error = %v", err)
	}
	if node.routeKeyCalls != 2 || len(node.calls) != 2 {
		t.Fatalf("route/call counts = %d/%d, want retry through fresh route", node.routeKeyCalls, len(node.calls))
	}
	if len(page.Rows) != 1 || page.Rows[0].ChannelID != "g1" {
		t.Fatalf("page = %#v, want retried remote row", page)
	}
}

func TestConversationAuthorityClientAdmitPatchesDoesNotExhaustBoundedRetries(t *testing.T) {
	local := &fakeConversationAuthorityLocal{admitAlwaysErr: conversationusecase.ErrNotLeader}
	node := &fakeConversationAuthorityNode{
		nodeID: 1,
		route:  clusterv2.Route{HashSlot: 1, SlotID: 2, Leader: 1, Revision: 10, AuthorityEpoch: 20},
	}
	client := NewConversationAuthorityClient(node, local)
	client.routeRetrySleep = func(context.Context, time.Duration) error { return nil }

	err := client.AdmitPatches(context.Background(), []conversationusecase.ActivePatch{{UID: "u1", ChannelID: "g", ChannelType: 2, ActiveAt: 10}})
	if !errors.Is(err, conversationusecase.ErrNotLeader) {
		t.Fatalf("AdmitPatches() error = %v, want ErrNotLeader", err)
	}
	if len(local.targets) != 1 {
		t.Fatalf("admit attempts = %d, want 1", len(local.targets))
	}
}

func TestConversationAuthorityClientAdmitPatchesReturnsContextCancellationBeforeRouting(t *testing.T) {
	local := &fakeConversationAuthorityLocal{}
	node := &fakeConversationAuthorityNode{
		nodeID: 1,
		route:  clusterv2.Route{HashSlot: 1, SlotID: 2, Leader: 1, Revision: 10, AuthorityEpoch: 20},
	}
	client := NewConversationAuthorityClient(node, local)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := client.AdmitPatches(ctx, []conversationusecase.ActivePatch{{UID: "u1", ChannelID: "g", ChannelType: 2, ActiveAt: 10}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("AdmitPatches() error = %v, want context.Canceled", err)
	}
	if node.routeKeyCalls != 0 {
		t.Fatalf("RouteKey calls = %d, want 0", node.routeKeyCalls)
	}
}

func TestConversationAuthorityClientAdmitPatchesDoesNotRetryNotLeader(t *testing.T) {
	local := &fakeConversationAuthorityLocal{admitErrs: []error{conversationusecase.ErrNotLeader, nil}}
	node := &fakeConversationAuthorityNode{
		nodeID: 1,
		routes: []clusterv2.Route{
			{HashSlot: 1, SlotID: 2, Leader: 1, Revision: 10, AuthorityEpoch: 20},
			{HashSlot: 1, SlotID: 2, Leader: 1, Revision: 11, AuthorityEpoch: 21},
		},
	}
	client := NewConversationAuthorityClient(node, local)
	client.routeRetrySleep = func(context.Context, time.Duration) error {
		t.Fatal("AdmitPatches should not sleep between retries")
		return nil
	}

	err := client.AdmitPatches(context.Background(), []conversationusecase.ActivePatch{{UID: "u1", ChannelID: "g", ChannelType: 2, ActiveAt: 10}})
	if !errors.Is(err, conversationusecase.ErrNotLeader) {
		t.Fatalf("AdmitPatches() error = %v, want ErrNotLeader", err)
	}
	if node.routeKeyCalls != 1 {
		t.Fatalf("RouteKey calls = %d, want 1", node.routeKeyCalls)
	}
	if len(local.targets) != 1 || local.targets[0].RouteRevision != 10 {
		t.Fatalf("targets = %#v, want one admit with original route revision 10", local.targets)
	}
}

func TestConversationAuthorityClientAdmitPatchesStopsAtRetryableGroupError(t *testing.T) {
	local := &fakeConversationAuthorityLocal{admitErrs: []error{conversationusecase.ErrStaleRoute, nil, nil, nil}}
	node := &fakeConversationAuthorityNode{
		nodeID: 1,
		routesByUID: map[string]clusterv2.Route{
			"u1": {HashSlot: 1, SlotID: 2, Leader: 1, Revision: 10, AuthorityEpoch: 20},
			"u2": {HashSlot: 2, SlotID: 2, Leader: 1, Revision: 11, AuthorityEpoch: 21},
			"u3": {HashSlot: 3, SlotID: 2, Leader: 1, Revision: 12, AuthorityEpoch: 22},
		},
	}
	client := NewConversationAuthorityClient(node, local)
	client.routeRetrySleep = func(context.Context, time.Duration) error {
		t.Fatal("AdmitPatches should not sleep between retries")
		return nil
	}
	patches := []conversationusecase.ActivePatch{
		{UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAt: 10},
		{UID: "u2", ChannelID: "g2", ChannelType: 2, ActiveAt: 20},
		{UID: "u3", ChannelID: "g3", ChannelType: 2, ActiveAt: 30},
	}

	err := client.AdmitPatches(context.Background(), patches)
	if !errors.Is(err, conversationusecase.ErrStaleRoute) {
		t.Fatalf("AdmitPatches() error = %v, want ErrStaleRoute", err)
	}
	if len(local.targets) != 1 {
		t.Fatalf("targets = %#v, want only first group attempted", local.targets)
	}
	if len(local.deliveredPatches) != 0 {
		t.Fatalf("delivered patches = %#v, want none after first group failure", local.deliveredPatches)
	}
}

func TestConversationAuthorityClientDrainAuthority(t *testing.T) {
	local := &fakeConversationAuthorityLocal{drainResult: "drained"}
	node := &fakeConversationAuthorityNode{nodeID: 1}
	client := NewConversationAuthorityClient(node, local)
	target := conversationusecase.RouteTarget{HashSlot: 7, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}

	got, err := client.DrainAuthority(context.Background(), target)
	if err != nil {
		t.Fatalf("DrainAuthority() error = %v", err)
	}
	if got != "drained" {
		t.Fatalf("DrainAuthority() = %q, want drained", got)
	}
	if len(local.drainTargets) != 1 || local.drainTargets[0] != target {
		t.Fatalf("drain targets = %#v, want %#v", local.drainTargets, target)
	}
}

func TestConversationAuthorityClientDrainAuthorityNilNodeRemoteTargetReturnsRouteNotReady(t *testing.T) {
	client := NewConversationAuthorityClient(nil, &fakeConversationAuthorityLocal{})
	target := conversationusecase.RouteTarget{HashSlot: 7, SlotID: 2, LeaderNodeID: 2, RouteRevision: 3, AuthorityEpoch: 4}

	_, err := client.DrainAuthority(context.Background(), target)
	if !errors.Is(err, conversationusecase.ErrRouteNotReady) {
		t.Fatalf("DrainAuthority() error = %v, want ErrRouteNotReady", err)
	}
}

func TestConversationAuthorityClientMapsRouteErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want error
	}{
		{name: "route not ready", err: clusterv2.ErrRouteNotReady, want: conversationusecase.ErrRouteNotReady},
		{name: "no slot leader", err: clusterv2.ErrNoSlotLeader, want: conversationusecase.ErrRouteNotReady},
		{name: "not leader", err: clusterv2.ErrNotLeader, want: conversationusecase.ErrNotLeader},
		{name: "context canceled", err: context.Canceled, want: context.Canceled},
		{name: "context deadline", err: context.DeadlineExceeded, want: context.DeadlineExceeded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := &fakeConversationAuthorityNode{nodeID: 1, routeErr: tt.err}
			client := NewConversationAuthorityClient(node, &fakeConversationAuthorityLocal{})
			client.routeRetrySleep = func(context.Context, time.Duration) error { return nil }

			_, err := client.ListUserConversationActiveView(context.Background(), "u1", metadb.UserConversationActiveCursor{}, 10)
			if !errors.Is(err, tt.want) {
				t.Fatalf("ListUserConversationActiveView() error = %v, want %v", err, tt.want)
			}
		})
	}
}

type nodeRPCHandlerFunc func(context.Context, []byte) ([]byte, error)

func (f nodeRPCHandlerFunc) HandleRPC(ctx context.Context, payload []byte) ([]byte, error) {
	return f(ctx, payload)
}

type fakeConversationAuthorityNode struct {
	nodeID        uint64
	route         clusterv2.Route
	routesByUID   map[string]clusterv2.Route
	routes        []clusterv2.Route
	routeErr      error
	rpcErrs       []error
	handler       clusterv2.NodeRPCHandler
	calls         []rpcCall
	routeKeyCalls int
	registered    map[uint8]clusterv2.NodeRPCHandler
	watch         chan clusterv2.RouteAuthorityEvent
}

func (f *fakeConversationAuthorityNode) NodeID() uint64 {
	return f.nodeID
}

func (f *fakeConversationAuthorityNode) RouteKey(uid string) (clusterv2.Route, error) {
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

func (f *fakeConversationAuthorityNode) CallRPC(ctx context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	f.calls = append(f.calls, rpcCall{nodeID: nodeID, serviceID: serviceID, payload: append([]byte(nil), payload...)})
	if len(f.rpcErrs) > 0 {
		err := f.rpcErrs[0]
		f.rpcErrs = f.rpcErrs[1:]
		if err != nil {
			return nil, err
		}
	}
	if f.handler != nil {
		return f.handler.HandleRPC(ctx, payload)
	}
	if handler := f.registered[serviceID]; handler != nil {
		return handler.HandleRPC(ctx, payload)
	}
	return nil, errors.New("missing rpc handler")
}

func (f *fakeConversationAuthorityNode) RegisterRPC(serviceID uint8, handler clusterv2.NodeRPCHandler) {
	if f.registered == nil {
		f.registered = make(map[uint8]clusterv2.NodeRPCHandler)
	}
	f.registered[serviceID] = handler
}

func (f *fakeConversationAuthorityNode) WatchRouteAuthorities() <-chan clusterv2.RouteAuthorityEvent {
	if f.watch == nil {
		f.watch = make(chan clusterv2.RouteAuthorityEvent)
	}
	return f.watch
}

type fakeConversationAuthorityLocal struct {
	patches          []conversationusecase.ActivePatch
	deliveredPatches []conversationusecase.ActivePatch
	targets          []conversationusecase.RouteTarget
	page             conversationusecase.ActiveViewPage
	admitErrs        []error
	admitAlwaysErr   error
	listErrs         []error
	drainResult      string
	drainTargets     []conversationusecase.RouteTarget
}

func (f *fakeConversationAuthorityLocal) AdmitPatches(_ context.Context, target conversationusecase.RouteTarget, patches []conversationusecase.ActivePatch) error {
	f.targets = append(f.targets, target)
	f.patches = append(f.patches, patches...)
	if f.admitAlwaysErr != nil {
		return f.admitAlwaysErr
	}
	if len(f.admitErrs) > 0 {
		err := f.admitErrs[0]
		f.admitErrs = f.admitErrs[1:]
		if err != nil {
			return err
		}
	}
	f.deliveredPatches = append(f.deliveredPatches, patches...)
	return nil
}

func (f *fakeConversationAuthorityLocal) ListUserConversationActiveViewForTarget(_ context.Context, target conversationusecase.RouteTarget, _ string, _ metadb.UserConversationActiveCursor, _ int) (conversationusecase.ActiveViewPage, error) {
	f.targets = append(f.targets, target)
	if len(f.listErrs) > 0 {
		err := f.listErrs[0]
		f.listErrs = f.listErrs[1:]
		if err != nil {
			return conversationusecase.ActiveViewPage{}, err
		}
	}
	return f.page, nil
}

func (f *fakeConversationAuthorityLocal) DrainAuthority(_ context.Context, target conversationusecase.RouteTarget) (string, error) {
	f.drainTargets = append(f.drainTargets, target)
	return f.drainResult, nil
}

func deliveredConversationPatchUIDCounts(patches []conversationusecase.ActivePatch) map[string]int {
	out := make(map[string]int, len(patches))
	for _, patch := range patches {
		out[patch.UID]++
	}
	return out
}
