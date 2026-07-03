package cluster

import (
	"context"
	"testing"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
)

func TestNodeLifecycleClientForwardsJoinRPC(t *testing.T) {
	service := &fakeNodeLifecycleManager{
		response: managementusecase.JoinNodeResponse{
			Created:   true,
			NodeID:    4,
			Addr:      "10.0.0.4:11110",
			JoinState: "joining",
			Revision:  12,
		},
	}
	adapter := accessnode.New(accessnode.Options{
		NodeLifecycle:          service,
		NodeLifecycleClusterID: "cluster-a",
		NodeLifecycleJoinToken: "join-secret",
	})
	node := &fakeNodeLifecycleNode{handler: adapter.HandleNodeLifecycleRPC}
	client := NewNodeLifecycleClient(node)

	req := accessnode.NodeJoinRequest{
		NodeID:         4,
		AdvertiseAddr:  "10.0.0.4:11110",
		ClusterID:      "cluster-a",
		JoinToken:      "join-secret",
		CapacityWeight: 7,
	}
	got, err := client.JoinNode(context.Background(), 1, req)
	if err != nil {
		t.Fatalf("JoinNode() error = %v", err)
	}

	if got != service.response {
		t.Fatalf("JoinNode() = %#v, want %#v", got, service.response)
	}
	if node.nodeID != 1 || node.serviceID != accessnode.NodeLifecycleRPCServiceID {
		t.Fatalf("rpc target = node:%d service:%d, want node 1 service %d", node.nodeID, node.serviceID, accessnode.NodeLifecycleRPCServiceID)
	}
}

func TestNodeLifecycleClientForwardsReadinessRPC(t *testing.T) {
	readiness := &fakeNodeLifecycleReadiness{
		response: accessnode.NodeReadinessResponse{
			NodeID:            4,
			ExpectedClusterID: "cluster-a",
			MirrorClusterID:   "cluster-a",
			MirrorRevision:    22,
			Reachable:         true,
			TransportReady:    true,
			ControlReady:      true,
			RuntimeReady:      true,
			Ready:             true,
		},
	}
	adapter := accessnode.New(accessnode.Options{
		NodeReadiness:          readiness,
		NodeLifecycleClusterID: "cluster-a",
	})
	node := &fakeNodeLifecycleNode{handler: adapter.HandleNodeLifecycleRPC}
	client := NewNodeLifecycleClient(node, "cluster-a")

	got, err := client.NodeReadiness(context.Background(), 4)
	if err != nil {
		t.Fatalf("NodeReadiness() error = %v", err)
	}

	want := managementusecase.NodeReadiness{
		NodeID:            4,
		ExpectedClusterID: "cluster-a",
		MirrorClusterID:   "cluster-a",
		MirrorRevision:    22,
		Reachable:         true,
		TransportReady:    true,
		ControlReady:      true,
		RuntimeReady:      true,
	}
	if got != want {
		t.Fatalf("NodeReadiness() = %#v, want %#v", got, want)
	}
	if readiness.request != (accessnode.NodeReadinessRequest{NodeID: 4, ClusterID: "cluster-a"}) {
		t.Fatalf("readiness request = %#v, want node 4 cluster-a", readiness.request)
	}
	if node.nodeID != 4 || node.serviceID != accessnode.NodeLifecycleRPCServiceID {
		t.Fatalf("rpc target = node:%d service:%d, want node 4 service %d", node.nodeID, node.serviceID, accessnode.NodeLifecycleRPCServiceID)
	}
}

func TestNodeLifecycleClientForwardsControllerVoterReadinessRPC(t *testing.T) {
	readiness := &fakeNodeLifecycleControllerVoterReadiness{
		response: accessnode.ControllerVoterReadinessResponse{
			NodeID:          4,
			ClusterID:       "cluster-a",
			Reachable:       true,
			TransportReady:  true,
			ControlReady:    true,
			RuntimeReady:    true,
			CanPrepare:      true,
			MirrorRevision:  22,
			IsVoter:         true,
			ControlLeaderID: 1,
			ConfigIndex:     77,
			Voters:          []uint64{1, 2, 4},
		},
	}
	adapter := accessnode.New(accessnode.Options{
		ControllerVoterReadiness: readiness,
		NodeLifecycleClusterID:   "cluster-a",
	})
	node := &fakeNodeLifecycleNode{handler: adapter.HandleNodeLifecycleRPC}
	client := NewNodeLifecycleClient(node, "cluster-a")

	got, err := client.ControllerVoterReadiness(context.Background(), 4)
	if err != nil {
		t.Fatalf("ControllerVoterReadiness() error = %v", err)
	}

	if got.NodeID != 4 || got.ClusterID != "cluster-a" || !got.Reachable || !got.TransportReady ||
		!got.ControlReady || !got.RuntimeReady || !got.CanPrepare || got.MirrorRevision != 22 ||
		!got.IsVoter || got.ControlLeaderID != 1 || got.ConfigIndex != 77 ||
		!sameNodeLifecycleUint64s(got.Voters, []uint64{1, 2, 4}) {
		t.Fatalf("ControllerVoterReadiness() = %#v, want readiness and Controller Raft fields preserved", got)
	}
	got.Voters[0] = 99
	if readiness.response.Voters[0] != 1 {
		t.Fatalf("ControllerVoterReadiness() aliased wire voters: wire=%v", readiness.response.Voters)
	}
	if readiness.request != (accessnode.ControllerVoterReadinessRequest{NodeID: 4, ClusterID: "cluster-a"}) {
		t.Fatalf("readiness request = %#v, want node 4 cluster-a", readiness.request)
	}
	if node.nodeID != 4 || node.serviceID != accessnode.NodeLifecycleRPCServiceID {
		t.Fatalf("rpc target = node:%d service:%d, want node 4 service %d", node.nodeID, node.serviceID, accessnode.NodeLifecycleRPCServiceID)
	}
}

func TestNodeLifecycleClientForwardsPrepareControllerVoterRPC(t *testing.T) {
	preparer := &fakeNodeLifecycleControllerVoterPreparer{
		response: accessnode.PrepareControllerVoterResponse{
			NodeID:              4,
			Prepared:            true,
			StateRevision:       22,
			ObservedConfigIndex: 77,
			ObservedVoters:      []uint64{1, 2, 4},
		},
	}
	adapter := accessnode.New(accessnode.Options{
		ControllerVoterPreparer: preparer,
		NodeLifecycleClusterID:  "cluster-a",
	})
	node := &fakeNodeLifecycleNode{handler: adapter.HandleNodeLifecycleRPC}
	client := NewNodeLifecycleClient(node, "cluster-a")

	got, err := client.PrepareControllerVoter(context.Background(), managementusecase.PrepareControllerVoterRequest{
		NodeID:           4,
		ClusterID:        "cluster-a",
		ExpectedRevision: 22,
		NextVoters: []managementusecase.ControllerVoterEndpoint{
			{NodeID: 1, Addr: "10.0.0.1:11110"},
			{NodeID: 4, Addr: "10.0.0.4:11110"},
		},
	})
	if err != nil {
		t.Fatalf("PrepareControllerVoter() error = %v", err)
	}

	if got.NodeID != 4 || !got.Prepared || got.StateRevision != 22 ||
		got.ObservedConfigIndex != 77 || !sameNodeLifecycleUint64s(got.ObservedVoters, []uint64{1, 2, 4}) {
		t.Fatalf("PrepareControllerVoter() = %#v, want proof fields preserved", got)
	}
	if preparer.request.NodeID != 4 || preparer.request.ClusterID != "cluster-a" ||
		preparer.request.ExpectedRevision != 22 ||
		!sameAccessControllerVoters(preparer.request.NextVoters, []accessnode.ControllerVoter{{NodeID: 1, Addr: "10.0.0.1:11110"}, {NodeID: 4, Addr: "10.0.0.4:11110"}}) {
		t.Fatalf("prepare request = %#v, want converted voters", preparer.request)
	}
	got.ObservedVoters[0] = 99
	if preparer.response.ObservedVoters[0] != 1 {
		t.Fatalf("ObservedVoters was not cloned from wire response")
	}
}

type fakeNodeLifecycleManager struct {
	request  managementusecase.JoinNodeRequest
	response managementusecase.JoinNodeResponse
}

func (f *fakeNodeLifecycleManager) JoinNode(_ context.Context, req managementusecase.JoinNodeRequest) (managementusecase.JoinNodeResponse, error) {
	f.request = req
	return f.response, nil
}

type fakeNodeLifecycleReadiness struct {
	request  accessnode.NodeReadinessRequest
	response accessnode.NodeReadinessResponse
}

func (f *fakeNodeLifecycleReadiness) NodeReadiness(_ context.Context, req accessnode.NodeReadinessRequest) (accessnode.NodeReadinessResponse, error) {
	f.request = req
	return f.response, nil
}

type fakeNodeLifecycleControllerVoterReadiness struct {
	request  accessnode.ControllerVoterReadinessRequest
	response accessnode.ControllerVoterReadinessResponse
}

func (f *fakeNodeLifecycleControllerVoterReadiness) ControllerVoterReadiness(_ context.Context, req accessnode.ControllerVoterReadinessRequest) (accessnode.ControllerVoterReadinessResponse, error) {
	f.request = req
	return f.response, nil
}

type fakeNodeLifecycleControllerVoterPreparer struct {
	request  accessnode.PrepareControllerVoterRequest
	response accessnode.PrepareControllerVoterResponse
}

func (f *fakeNodeLifecycleControllerVoterPreparer) PrepareControllerVoter(_ context.Context, req accessnode.PrepareControllerVoterRequest) (accessnode.PrepareControllerVoterResponse, error) {
	f.request = req
	return f.response, nil
}

type fakeNodeLifecycleNode struct {
	handler   func(context.Context, []byte) ([]byte, error)
	nodeID    uint64
	serviceID uint8
}

func (f *fakeNodeLifecycleNode) NodeID() uint64 { return 99 }

func (f *fakeNodeLifecycleNode) CallRPC(ctx context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	f.nodeID = nodeID
	f.serviceID = serviceID
	return f.handler(ctx, payload)
}

func sameAccessControllerVoters(left, right []accessnode.ControllerVoter) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func sameNodeLifecycleUint64s(left, right []uint64) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
