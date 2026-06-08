package cluster

import (
	"context"
	"errors"
	"testing"

	accessnode "github.com/WuKongIM/WuKongIM/internalv2/access/node"
	"github.com/WuKongIM/WuKongIM/internalv2/contracts/authority"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
)

func TestSenderAuthorityResolverMapsRoute(t *testing.T) {
	node := &fakeSenderAuthorityNode{
		route: clusterv2.Route{
			HashSlot:       7,
			SlotID:         3,
			Leader:         42,
			Revision:       1001,
			AuthorityEpoch: 55,
		},
	}
	client := NewSenderAuthorityClient(node, nil)

	got, err := client.ResolveUIDAuthority(context.Background(), "u1")
	if err != nil {
		t.Fatalf("ResolveUIDAuthority() error = %v", err)
	}

	want := authority.Target{HashSlot: 7, SlotID: 3, LeaderNodeID: 42, RouteRevision: 1001, AuthorityEpoch: 55}
	if got != want {
		t.Fatalf("ResolveUIDAuthority() = %#v, want %#v", got, want)
	}
	if node.routeKey != "u1" {
		t.Fatalf("RouteKey key = %q, want u1", node.routeKey)
	}
}

func TestSenderAuthorityResolverMapsRouteErrors(t *testing.T) {
	unknown := errors.New("boom")
	tests := []struct {
		name      string
		routeErr  error
		want      error
		unchanged bool
	}{
		{name: "route not ready", routeErr: clusterv2.ErrRouteNotReady, want: message.ErrRouteNotReady},
		{name: "no slot leader", routeErr: clusterv2.ErrNoSlotLeader, want: message.ErrRouteNotReady},
		{name: "not leader", routeErr: clusterv2.ErrNotLeader, want: message.ErrNotLeader},
		{name: "context canceled", routeErr: context.Canceled, want: context.Canceled, unchanged: true},
		{name: "context deadline", routeErr: context.DeadlineExceeded, want: context.DeadlineExceeded, unchanged: true},
		{name: "unknown", routeErr: unknown, want: unknown, unchanged: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := NewSenderAuthorityClient(&fakeSenderAuthorityNode{routeErr: tt.routeErr}, nil)

			_, err := client.ResolveUIDAuthority(context.Background(), "u1")
			if !errors.Is(err, tt.want) {
				t.Fatalf("ResolveUIDAuthority() error = %v, want %v", err, tt.want)
			}
			if tt.unchanged && err != tt.routeErr {
				t.Fatalf("ResolveUIDAuthority() error = %v, want unchanged %v", err, tt.routeErr)
			}
		})
	}
}

func TestSenderAuthorityResolverRejectsMissingLeader(t *testing.T) {
	client := NewSenderAuthorityClient(&fakeSenderAuthorityNode{route: clusterv2.Route{HashSlot: 7, SlotID: 3}}, nil)

	_, err := client.ResolveUIDAuthority(context.Background(), "u1")
	if !errors.Is(err, message.ErrRouteNotReady) {
		t.Fatalf("ResolveUIDAuthority() error = %v, want ErrRouteNotReady", err)
	}
}

func TestSenderAuthorityClientDelegatesRemoteSend(t *testing.T) {
	target := authority.Target{HashSlot: 7, SlotID: 3, LeaderNodeID: 42, RouteRevision: 1001, AuthorityEpoch: 55}
	item := message.SendBatchItem{Context: context.Background(), Command: senderAuthorityTestCommand("u1", "client-1")}
	result := message.SendBatchItemResult{Result: message.SendResult{MessageID: 100, MessageSeq: 9, Reason: message.ReasonSuccess}}
	node := &fakeSenderAuthorityNode{nodeID: 1}
	local := &fakeSenderAuthorityLocal{results: []message.SendBatchItemResult{result}}
	adapter := accessnode.New(accessnode.Options{SenderAuthority: local})
	node.RegisterRPC(accessnode.SenderAuthorityRPCServiceID, senderAuthorityRPCHandlerFunc(adapter.HandleSenderAuthorityRPC))
	client := NewSenderAuthorityClient(node, nil)

	got := client.SendBatchToAuthority(context.Background(), target, []message.SendBatchItem{item})

	if len(got) != 1 {
		t.Fatalf("SendBatchToAuthority() len = %d, want 1", len(got))
	}
	if got[0].Result != result.Result || got[0].Err != nil {
		t.Fatalf("SendBatchToAuthority()[0] = %#v, want %#v", got[0], result)
	}
	if len(node.calls) != 1 {
		t.Fatalf("CallRPC calls = %d, want 1", len(node.calls))
	}
	if node.calls[0].nodeID != target.LeaderNodeID {
		t.Fatalf("CallRPC nodeID = %d, want %d", node.calls[0].nodeID, target.LeaderNodeID)
	}
	if node.calls[0].serviceID != accessnode.SenderAuthorityRPCServiceID {
		t.Fatalf("CallRPC serviceID = %d, want %d", node.calls[0].serviceID, accessnode.SenderAuthorityRPCServiceID)
	}
	if local.target != target {
		t.Fatalf("local target = %#v, want %#v", local.target, target)
	}
	if len(local.items) != 1 || local.items[0].Command.ClientMsgNo != "client-1" {
		t.Fatalf("local items = %#v, want forwarded client-1 item", local.items)
	}
}

func TestSenderAuthorityClientMissingRemoteReturnsItemErrors(t *testing.T) {
	items := []message.SendBatchItem{
		{Command: senderAuthorityTestCommand("u1", "client-1")},
		{Command: senderAuthorityTestCommand("u2", "client-2")},
	}
	target := authority.Target{LeaderNodeID: 42}

	for _, client := range []*SenderAuthorityClient{nil, &SenderAuthorityClient{}} {
		got := client.SendBatchToAuthority(context.Background(), target, items)
		if len(got) != len(items) {
			t.Fatalf("SendBatchToAuthority() len = %d, want %d", len(got), len(items))
		}
		for i := range got {
			if !errors.Is(got[i].Err, message.ErrRouteNotReady) {
				t.Fatalf("result[%d].Err = %v, want ErrRouteNotReady", i, got[i].Err)
			}
		}
	}
}

type fakeSenderAuthorityNode struct {
	nodeID     uint64
	route      clusterv2.Route
	routeErr   error
	routeKey   string
	registered map[uint8]clusterv2.NodeRPCHandler
	calls      []senderAuthorityRPCCall
}

func (f *fakeSenderAuthorityNode) NodeID() uint64 {
	return f.nodeID
}

func (f *fakeSenderAuthorityNode) RouteKey(key string) (clusterv2.Route, error) {
	f.routeKey = key
	if f.routeErr != nil {
		return clusterv2.Route{}, f.routeErr
	}
	return f.route, nil
}

func (f *fakeSenderAuthorityNode) CallRPC(ctx context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	f.calls = append(f.calls, senderAuthorityRPCCall{nodeID: nodeID, serviceID: serviceID, payload: append([]byte(nil), payload...)})
	handler := f.registered[serviceID]
	if handler == nil {
		return nil, errors.New("missing sender authority rpc handler")
	}
	return handler.HandleRPC(ctx, payload)
}

func (f *fakeSenderAuthorityNode) RegisterRPC(serviceID uint8, handler clusterv2.NodeRPCHandler) {
	if f.registered == nil {
		f.registered = make(map[uint8]clusterv2.NodeRPCHandler)
	}
	f.registered[serviceID] = handler
}

type senderAuthorityRPCCall struct {
	nodeID    uint64
	serviceID uint8
	payload   []byte
}

type senderAuthorityRPCHandlerFunc func(context.Context, []byte) ([]byte, error)

func (f senderAuthorityRPCHandlerFunc) HandleRPC(ctx context.Context, payload []byte) ([]byte, error) {
	return f(ctx, payload)
}

type fakeSenderAuthorityLocal struct {
	target  authority.Target
	items   []message.SendBatchItem
	results []message.SendBatchItemResult
}

func (f *fakeSenderAuthorityLocal) SendBatchForAuthority(_ context.Context, target authority.Target, items []message.SendBatchItem) []message.SendBatchItemResult {
	f.target = target
	f.items = append([]message.SendBatchItem(nil), items...)
	return f.results
}

func senderAuthorityTestCommand(uid, clientMsgNo string) message.SendCommand {
	return message.SendCommand{
		FromUID:     uid,
		ClientMsgNo: clientMsgNo,
		ChannelID:   "room",
		ChannelType: 2,
		Payload:     []byte("hello"),
	}
}
