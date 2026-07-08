package cluster

import (
	"context"
	"testing"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
)

func TestManagementNodeConfigReaderUsesLocalProviderForLocalNode(t *testing.T) {
	local := &localNodeConfigReaderStub{snapshot: managementusecase.NodeConfigSnapshot{NodeID: 1}}
	node := &fakeManagementNodeConfigNode{nodeID: 1}
	reader := NewManagementNodeConfigReader(node, local)

	got, err := reader.NodeConfigSnapshot(context.Background(), 1)
	if err != nil {
		t.Fatalf("NodeConfigSnapshot(local) error = %v", err)
	}
	if got.NodeID != 1 || local.nodeID != 1 || node.called {
		t.Fatalf("local=%d got=%#v remoteCalled=%v", local.nodeID, got, node.called)
	}
}

func TestManagementNodeConfigReaderRoutesRemoteNode(t *testing.T) {
	expected := managementusecase.NodeConfigSnapshot{NodeID: 2}
	adapter := accessnode.New(accessnode.Options{ManagerNodeConfig: &localNodeConfigReaderStub{snapshot: expected}})
	node := &fakeManagementNodeConfigNode{nodeID: 1, handler: adapter.HandleManagerNodeConfigRPC}
	reader := NewManagementNodeConfigReader(node, &localNodeConfigReaderStub{snapshot: managementusecase.NodeConfigSnapshot{NodeID: 1}})

	got, err := reader.NodeConfigSnapshot(context.Background(), 2)
	if err != nil {
		t.Fatalf("NodeConfigSnapshot(remote) error = %v", err)
	}
	if got.NodeID != 2 || node.calledNodeID != 2 || node.calledServiceID != accessnode.ManagerNodeConfigRPCServiceID {
		t.Fatalf("remote got=%#v call=node:%d service:%d", got, node.calledNodeID, node.calledServiceID)
	}
}

type localNodeConfigReaderStub struct {
	nodeID   uint64
	snapshot managementusecase.NodeConfigSnapshot
}

func (r *localNodeConfigReaderStub) NodeConfigSnapshot(_ context.Context, nodeID uint64) (managementusecase.NodeConfigSnapshot, error) {
	r.nodeID = nodeID
	return r.snapshot, nil
}

type fakeManagementNodeConfigNode struct {
	nodeID          uint64
	called          bool
	calledNodeID    uint64
	calledServiceID uint8
	handler         func(context.Context, []byte) ([]byte, error)
}

func (f *fakeManagementNodeConfigNode) NodeID() uint64 { return f.nodeID }

func (f *fakeManagementNodeConfigNode) CallRPC(ctx context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	f.called = true
	f.calledNodeID = nodeID
	f.calledServiceID = serviceID
	return f.handler(ctx, payload)
}
