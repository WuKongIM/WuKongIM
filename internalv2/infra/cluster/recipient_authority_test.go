package cluster

import (
	"context"
	"errors"
	"reflect"
	"testing"

	accessnode "github.com/WuKongIM/WuKongIM/internalv2/access/node"
	"github.com/WuKongIM/WuKongIM/internalv2/contracts/authority"
	"github.com/WuKongIM/WuKongIM/internalv2/contracts/messageevents"
	recipientusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/recipient"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
)

func TestRecipientAuthorityResolverMapsRoute(t *testing.T) {
	node := &fakeRecipientAuthorityNode{
		route: clusterv2.Route{
			HashSlot:       7,
			SlotID:         3,
			Leader:         42,
			Revision:       1001,
			AuthorityEpoch: 55,
		},
	}
	client := NewRecipientAuthorityClient(node, nil)

	got, err := client.ResolveRecipientAuthority(context.Background(), "u1")
	if err != nil {
		t.Fatalf("ResolveRecipientAuthority() error = %v", err)
	}

	want := authority.Target{HashSlot: 7, SlotID: 3, LeaderNodeID: 42, RouteRevision: 1001, AuthorityEpoch: 55}
	if got != want {
		t.Fatalf("ResolveRecipientAuthority() = %#v, want %#v", got, want)
	}
	if node.routeKey != "u1" {
		t.Fatalf("RouteKey key = %q, want u1", node.routeKey)
	}
}

func TestRecipientAuthorityResolverMapsRouteErrors(t *testing.T) {
	unknown := errors.New("boom")
	tests := []struct {
		name      string
		routeErr  error
		want      error
		unchanged bool
	}{
		{name: "route not ready", routeErr: clusterv2.ErrRouteNotReady, want: recipientusecase.ErrRouteNotReady},
		{name: "no slot leader", routeErr: clusterv2.ErrNoSlotLeader, want: recipientusecase.ErrRouteNotReady},
		{name: "not leader", routeErr: clusterv2.ErrNotLeader, want: recipientusecase.ErrNotLeader},
		{name: "context canceled", routeErr: context.Canceled, want: context.Canceled, unchanged: true},
		{name: "context deadline", routeErr: context.DeadlineExceeded, want: context.DeadlineExceeded, unchanged: true},
		{name: "unknown", routeErr: unknown, want: unknown, unchanged: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := NewRecipientAuthorityClient(&fakeRecipientAuthorityNode{routeErr: tt.routeErr}, nil)

			_, err := client.ResolveRecipientAuthority(context.Background(), "u1")
			if !errors.Is(err, tt.want) {
				t.Fatalf("ResolveRecipientAuthority() error = %v, want %v", err, tt.want)
			}
			if tt.unchanged && err != tt.routeErr {
				t.Fatalf("ResolveRecipientAuthority() error = %v, want unchanged %v", err, tt.routeErr)
			}
		})
	}
}

func TestRecipientAuthorityResolverRejectsMissingLeader(t *testing.T) {
	client := NewRecipientAuthorityClient(&fakeRecipientAuthorityNode{route: clusterv2.Route{HashSlot: 7, SlotID: 3}}, nil)

	_, err := client.ResolveRecipientAuthority(context.Background(), "u1")
	if !errors.Is(err, recipientusecase.ErrRouteNotReady) {
		t.Fatalf("ResolveRecipientAuthority() error = %v, want ErrRouteNotReady", err)
	}
}

func TestRecipientAuthorityClientDelegatesRemoteProcess(t *testing.T) {
	target := authority.Target{HashSlot: 7, SlotID: 3, LeaderNodeID: 42, RouteRevision: 1001, AuthorityEpoch: 55}
	req := recipientAuthorityProcessRequest(target)
	node := &fakeRecipientAuthorityNode{nodeID: 1}
	local := &fakeRecipientAuthorityLocal{}
	adapter := accessnode.New(accessnode.Options{RecipientAuthority: local})
	node.RegisterRPC(accessnode.RecipientAuthorityRPCServiceID, recipientAuthorityRPCHandlerFunc(adapter.HandleRecipientAuthorityRPC))
	client := NewRecipientAuthorityClient(node, nil)

	err := client.ProcessRemote(context.Background(), req)
	if err != nil {
		t.Fatalf("ProcessRemote() error = %v", err)
	}

	if len(node.calls) != 1 {
		t.Fatalf("CallRPC calls = %d, want 1", len(node.calls))
	}
	if node.calls[0].nodeID != target.LeaderNodeID {
		t.Fatalf("CallRPC nodeID = %d, want %d", node.calls[0].nodeID, target.LeaderNodeID)
	}
	if node.calls[0].serviceID != accessnode.RecipientAuthorityRPCServiceID {
		t.Fatalf("CallRPC serviceID = %d, want %d", node.calls[0].serviceID, accessnode.RecipientAuthorityRPCServiceID)
	}
	if !reflect.DeepEqual(local.req, req) {
		t.Fatalf("local request = %#v, want %#v", local.req, req)
	}
}

func TestRecipientAuthorityClientMissingRemoteReturnsRouteNotReady(t *testing.T) {
	target := authority.Target{HashSlot: 7, SlotID: 3, LeaderNodeID: 42, RouteRevision: 1001, AuthorityEpoch: 55}
	for _, client := range []*RecipientAuthorityClient{nil, &RecipientAuthorityClient{}} {
		err := client.ProcessRemote(context.Background(), recipientAuthorityProcessRequest(target))
		if !errors.Is(err, recipientusecase.ErrRouteNotReady) {
			t.Fatalf("ProcessRemote() error = %v, want ErrRouteNotReady", err)
		}
	}
}

func TestRecipientAuthorityValidatorAcceptsExactCurrentTarget(t *testing.T) {
	target := authority.Target{HashSlot: 7, SlotID: 3, LeaderNodeID: 1, RouteRevision: 1001, AuthorityEpoch: 55}
	node := &fakeRecipientAuthorityNode{nodeID: 1, hashSlotRoute: clusterv2.Route{
		HashSlot:       7,
		SlotID:         3,
		Leader:         1,
		Revision:       1001,
		AuthorityEpoch: 55,
	}}
	client := NewRecipientAuthorityClient(node, nil)

	if err := client.ValidateRecipientAuthority(context.Background(), target); err != nil {
		t.Fatalf("ValidateRecipientAuthority() error = %v", err)
	}
	if node.hashSlot != target.HashSlot {
		t.Fatalf("RouteHashSlot hashSlot = %d, want %d", node.hashSlot, target.HashSlot)
	}
}

func TestRecipientAuthorityValidatorRejectsStaleTarget(t *testing.T) {
	tests := []struct {
		name  string
		route clusterv2.Route
		want  error
	}{
		{
			name: "leader changed",
			route: clusterv2.Route{
				HashSlot:       7,
				SlotID:         3,
				Leader:         2,
				Revision:       1001,
				AuthorityEpoch: 56,
			},
			want: recipientusecase.ErrNotLeader,
		},
		{
			name: "epoch changed same leader",
			route: clusterv2.Route{
				HashSlot:       7,
				SlotID:         3,
				Leader:         1,
				Revision:       1001,
				AuthorityEpoch: 56,
			},
			want: recipientusecase.ErrStaleRoute,
		},
		{
			name: "revision changed same leader",
			route: clusterv2.Route{
				HashSlot:       7,
				SlotID:         3,
				Leader:         1,
				Revision:       1002,
				AuthorityEpoch: 55,
			},
			want: recipientusecase.ErrStaleRoute,
		},
	}
	target := authority.Target{HashSlot: 7, SlotID: 3, LeaderNodeID: 1, RouteRevision: 1001, AuthorityEpoch: 55}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := &fakeRecipientAuthorityNode{nodeID: 1, hashSlotRoute: tt.route}
			client := NewRecipientAuthorityClient(node, nil)

			err := client.ValidateRecipientAuthority(context.Background(), target)
			if !errors.Is(err, tt.want) {
				t.Fatalf("ValidateRecipientAuthority() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestRecipientAuthorityValidatorMapsRouteErrors(t *testing.T) {
	tests := []struct {
		name     string
		routeErr error
		want     error
	}{
		{name: "route not ready", routeErr: clusterv2.ErrRouteNotReady, want: recipientusecase.ErrRouteNotReady},
		{name: "no slot leader", routeErr: clusterv2.ErrNoSlotLeader, want: recipientusecase.ErrRouteNotReady},
		{name: "not leader", routeErr: clusterv2.ErrNotLeader, want: recipientusecase.ErrNotLeader},
		{name: "context canceled", routeErr: context.Canceled, want: context.Canceled},
	}
	target := authority.Target{HashSlot: 7, SlotID: 3, LeaderNodeID: 1, RouteRevision: 1001, AuthorityEpoch: 55}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := &fakeRecipientAuthorityNode{nodeID: 1, hashSlotErr: tt.routeErr}
			client := NewRecipientAuthorityClient(node, nil)

			err := client.ValidateRecipientAuthority(context.Background(), target)
			if !errors.Is(err, tt.want) {
				t.Fatalf("ValidateRecipientAuthority() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func recipientAuthorityProcessRequest(target authority.Target) recipientusecase.ProcessRequest {
	return recipientusecase.ProcessRequest{
		Target: target,
		Event: messageevents.MessageCommitted{
			MessageID:         100,
			MessageSeq:        9,
			ChannelID:         "g1",
			ChannelType:       2,
			FromUID:           "u1",
			SenderNodeID:      1,
			SenderSessionID:   2,
			ClientMsgNo:       "client-1",
			ServerTimestampMS: 1234,
			Payload:           []byte("hello"),
			RedDot:            true,
			MessageScopedUIDs: []string{"u2"},
		},
		Recipients: []recipientusecase.Recipient{{UID: "u2", JoinSeq: 10}},
	}
}

type fakeRecipientAuthorityNode struct {
	nodeID        uint64
	route         clusterv2.Route
	routeErr      error
	routeKey      string
	hashSlotRoute clusterv2.Route
	hashSlotErr   error
	hashSlot      uint16
	registered    map[uint8]clusterv2.NodeRPCHandler
	calls         []recipientAuthorityRPCCall
}

func (f *fakeRecipientAuthorityNode) NodeID() uint64 {
	return f.nodeID
}

func (f *fakeRecipientAuthorityNode) RouteKey(key string) (clusterv2.Route, error) {
	f.routeKey = key
	if f.routeErr != nil {
		return clusterv2.Route{}, f.routeErr
	}
	return f.route, nil
}

func (f *fakeRecipientAuthorityNode) RouteHashSlot(hashSlot uint16) (clusterv2.Route, error) {
	f.hashSlot = hashSlot
	if f.hashSlotErr != nil {
		return clusterv2.Route{}, f.hashSlotErr
	}
	return f.hashSlotRoute, nil
}

func (f *fakeRecipientAuthorityNode) CallRPC(ctx context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	f.calls = append(f.calls, recipientAuthorityRPCCall{nodeID: nodeID, serviceID: serviceID, payload: append([]byte(nil), payload...)})
	handler := f.registered[serviceID]
	if handler == nil {
		return nil, errors.New("missing recipient authority rpc handler")
	}
	return handler.HandleRPC(ctx, payload)
}

func (f *fakeRecipientAuthorityNode) RegisterRPC(serviceID uint8, handler clusterv2.NodeRPCHandler) {
	if f.registered == nil {
		f.registered = make(map[uint8]clusterv2.NodeRPCHandler)
	}
	f.registered[serviceID] = handler
}

type recipientAuthorityRPCCall struct {
	nodeID    uint64
	serviceID uint8
	payload   []byte
}

type recipientAuthorityRPCHandlerFunc func(context.Context, []byte) ([]byte, error)

func (f recipientAuthorityRPCHandlerFunc) HandleRPC(ctx context.Context, payload []byte) ([]byte, error) {
	return f(ctx, payload)
}

type fakeRecipientAuthorityLocal struct {
	req recipientusecase.ProcessRequest
	err error
}

func (f *fakeRecipientAuthorityLocal) Process(_ context.Context, req recipientusecase.ProcessRequest) error {
	req.Event = req.Event.Clone()
	req.Recipients = append([]recipientusecase.Recipient(nil), req.Recipients...)
	f.req = req
	return f.err
}
