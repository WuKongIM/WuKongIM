package node

import (
	"context"
	"errors"
	"testing"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	cv2 "github.com/WuKongIM/WuKongIM/pkg/controller"
)

func TestNodeLifecycleRPCJoinForwardsTokenAndClusterID(t *testing.T) {
	service := &fakeNodeLifecycleService{
		joinResponse: managementusecase.JoinNodeResponse{
			Created:   true,
			NodeID:    4,
			Addr:      "10.0.0.4:11110",
			JoinState: "joining",
			Revision:  12,
		},
	}
	adapter := New(Options{
		NodeLifecycle:          service,
		NodeLifecycleClusterID: "cluster-a",
		NodeLifecycleJoinToken: "join-secret",
	})
	node := &fakeNodeLifecycleRPCNode{handler: adapter.HandleNodeLifecycleRPC}
	client := NewClient(node)

	req := NodeJoinRequest{
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

	if got != service.joinResponse {
		t.Fatalf("JoinNode() = %#v, want %#v", got, service.joinResponse)
	}
	if node.nodeID != 1 || node.serviceID != NodeLifecycleRPCServiceID {
		t.Fatalf("rpc target = node:%d service:%d, want node 1 service %d", node.nodeID, node.serviceID, NodeLifecycleRPCServiceID)
	}
	wireReq, err := decodeNodeLifecycleRequest(node.payload)
	if err != nil {
		t.Fatalf("decodeNodeLifecycleRequest() error = %v", err)
	}
	if wireReq.Op != nodeLifecycleOpJoin || wireReq.Join != req {
		t.Fatalf("wire request = %#v, want join %#v", wireReq, req)
	}
	wantServiceReq := managementusecase.JoinNodeRequest{
		NodeID:         4,
		Addr:           "10.0.0.4:11110",
		CapacityWeight: 7,
	}
	if service.joinRequest != wantServiceReq {
		t.Fatalf("service request = %#v, want %#v", service.joinRequest, wantServiceReq)
	}
}

func TestNodeLifecycleRPCJoinRequiresConfiguredToken(t *testing.T) {
	service := &fakeNodeLifecycleService{}
	adapter := New(Options{
		NodeLifecycle:          service,
		NodeLifecycleClusterID: "cluster-a",
	})
	node := &fakeNodeLifecycleRPCNode{handler: adapter.HandleNodeLifecycleRPC}
	client := NewClient(node)

	_, err := client.JoinNode(context.Background(), 1, NodeJoinRequest{
		NodeID:        4,
		AdvertiseAddr: "10.0.0.4:11110",
		ClusterID:     "cluster-a",
		JoinToken:     "join-secret",
	})

	if err != managementusecase.ErrNodeLifecycleUnavailable {
		t.Fatalf("JoinNode() error = %v, want ErrNodeLifecycleUnavailable", err)
	}
	if service.joinRequest.NodeID != 0 {
		t.Fatalf("service join request = %#v, want no delegate when token is not configured", service.joinRequest)
	}
}

func TestNodeLifecycleRPCReadinessPreservesActivationFields(t *testing.T) {
	readiness := &fakeNodeReadinessProvider{
		response: NodeReadinessResponse{
			NodeID:            4,
			ClusterID:         "cluster-a",
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
	adapter := New(Options{
		NodeReadiness:          readiness,
		NodeLifecycleClusterID: "cluster-a",
	})
	node := &fakeNodeLifecycleRPCNode{handler: adapter.HandleNodeLifecycleRPC}
	client := NewClient(node)

	req := NodeReadinessRequest{NodeID: 4, ClusterID: "cluster-a"}
	got, err := client.NodeReadiness(context.Background(), 4, req)
	if err != nil {
		t.Fatalf("NodeReadiness() error = %v", err)
	}

	if got != readiness.response {
		t.Fatalf("NodeReadiness() = %#v, want %#v", got, readiness.response)
	}
	if readiness.request != req {
		t.Fatalf("readiness request = %#v, want %#v", readiness.request, req)
	}
	wireReq, err := decodeNodeLifecycleRequest(node.payload)
	if err != nil {
		t.Fatalf("decodeNodeLifecycleRequest() error = %v", err)
	}
	if wireReq.Op != nodeLifecycleOpReadiness || wireReq.Readiness != req {
		t.Fatalf("wire request = %#v, want readiness %#v", wireReq, req)
	}
}

func TestNodeLifecycleRPCControllerVoterReadinessPreservesFields(t *testing.T) {
	readiness := &fakeControllerVoterReadinessProvider{
		response: ControllerVoterReadinessResponse{
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
	adapter := New(Options{
		ControllerVoterReadiness: readiness,
		NodeLifecycleClusterID:   "cluster-a",
	})
	node := &fakeNodeLifecycleRPCNode{handler: adapter.HandleNodeLifecycleRPC}
	client := NewClient(node)

	req := ControllerVoterReadinessRequest{NodeID: 4, ClusterID: "cluster-a"}
	got, err := client.ControllerVoterReadiness(context.Background(), 4, req)
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
		t.Fatalf("ControllerVoterReadiness() aliased provider voters: provider=%v", readiness.response.Voters)
	}
	if readiness.request != req {
		t.Fatalf("readiness request = %#v, want %#v", readiness.request, req)
	}
	wireReq, err := decodeNodeLifecycleRequest(node.payload)
	if err != nil {
		t.Fatalf("decodeNodeLifecycleRequest() error = %v", err)
	}
	if wireReq.Op != nodeLifecycleOpControllerVoterReadiness || wireReq.ControllerVoterReadiness != req {
		t.Fatalf("wire request = %#v, want controller voter readiness %#v", wireReq, req)
	}
}

func TestNodeLifecycleRPCPrepareControllerVoterPreservesProofFields(t *testing.T) {
	preparer := &fakeControllerVoterPreparer{
		response: PrepareControllerVoterResponse{
			NodeID:              4,
			Prepared:            true,
			StateRevision:       22,
			ObservedConfigIndex: 77,
			ObservedVoters:      []uint64{1, 2, 4},
		},
	}
	adapter := New(Options{
		ControllerVoterPreparer: preparer,
		NodeLifecycleClusterID:  "cluster-a",
	})
	node := &fakeNodeLifecycleRPCNode{handler: adapter.HandleNodeLifecycleRPC}
	client := NewClient(node)

	req := PrepareControllerVoterRequest{
		NodeID:           4,
		ClusterID:        "cluster-a",
		ExpectedRevision: 22,
		NextVoters: []ControllerVoter{
			{NodeID: 1, Addr: "10.0.0.1:11110"},
			{NodeID: 4, Addr: "10.0.0.4:11110"},
		},
	}
	got, err := client.PrepareControllerVoter(context.Background(), 4, req)
	if err != nil {
		t.Fatalf("PrepareControllerVoter() error = %v", err)
	}

	if got.NodeID != 4 || !got.Prepared || got.StateRevision != 22 ||
		got.ObservedConfigIndex != 77 || !sameNodeLifecycleUint64s(got.ObservedVoters, []uint64{1, 2, 4}) {
		t.Fatalf("PrepareControllerVoter() = %#v, want proof fields preserved", got)
	}
	if preparer.request.NodeID != req.NodeID || preparer.request.ClusterID != req.ClusterID ||
		preparer.request.ExpectedRevision != req.ExpectedRevision ||
		!sameControllerVoters(preparer.request.NextVoters, req.NextVoters) {
		t.Fatalf("prepare request = %#v, want %#v", preparer.request, req)
	}
	wireReq, err := decodeNodeLifecycleRequest(node.payload)
	if err != nil {
		t.Fatalf("decodeNodeLifecycleRequest() error = %v", err)
	}
	if wireReq.Op != nodeLifecycleOpPrepareControllerVoter || !sameControllerVoters(wireReq.PrepareControllerVoter.NextVoters, req.NextVoters) {
		t.Fatalf("wire request = %#v, want prepare controller voter %#v", wireReq, req)
	}
}

func TestNodeLifecycleRPCPrepareControllerVoterConflictMapsToPromotionBlocked(t *testing.T) {
	for _, errFromProvider := range []error{
		managementusecase.ErrControllerVoterPromotionBlocked,
		cv2.ErrExpectedRevisionMismatch,
	} {
		preparer := &fakeControllerVoterPreparer{err: errFromProvider}
		adapter := New(Options{
			ControllerVoterPreparer: preparer,
			NodeLifecycleClusterID:  "cluster-a",
		})
		node := &fakeNodeLifecycleRPCNode{handler: adapter.HandleNodeLifecycleRPC}
		client := NewClient(node)

		_, err := client.PrepareControllerVoter(context.Background(), 4, PrepareControllerVoterRequest{
			NodeID:           4,
			ClusterID:        "cluster-a",
			ExpectedRevision: 22,
			NextVoters:       []ControllerVoter{{NodeID: 1, Addr: "n1"}, {NodeID: 4, Addr: "n4"}},
		})
		if !errors.Is(err, managementusecase.ErrControllerVoterPromotionBlocked) {
			t.Fatalf("PrepareControllerVoter(%v) error = %v, want promotion blocked", errFromProvider, err)
		}
	}
}

type fakeNodeLifecycleService struct {
	joinRequest  managementusecase.JoinNodeRequest
	joinResponse managementusecase.JoinNodeResponse
}

func (f *fakeNodeLifecycleService) JoinNode(_ context.Context, req managementusecase.JoinNodeRequest) (managementusecase.JoinNodeResponse, error) {
	f.joinRequest = req
	return f.joinResponse, nil
}

type fakeNodeReadinessProvider struct {
	request  NodeReadinessRequest
	response NodeReadinessResponse
}

func (f *fakeNodeReadinessProvider) NodeReadiness(_ context.Context, req NodeReadinessRequest) (NodeReadinessResponse, error) {
	f.request = req
	return f.response, nil
}

type fakeControllerVoterReadinessProvider struct {
	request  ControllerVoterReadinessRequest
	response ControllerVoterReadinessResponse
	err      error
}

func (f *fakeControllerVoterReadinessProvider) ControllerVoterReadiness(_ context.Context, req ControllerVoterReadinessRequest) (ControllerVoterReadinessResponse, error) {
	f.request = req
	return f.response, f.err
}

type fakeControllerVoterPreparer struct {
	request  PrepareControllerVoterRequest
	response PrepareControllerVoterResponse
	err      error
}

func (f *fakeControllerVoterPreparer) PrepareControllerVoter(_ context.Context, req PrepareControllerVoterRequest) (PrepareControllerVoterResponse, error) {
	f.request = req
	return f.response, f.err
}

type fakeNodeLifecycleRPCNode struct {
	handler   func(context.Context, []byte) ([]byte, error)
	nodeID    uint64
	serviceID uint8
	payload   []byte
}

func (f *fakeNodeLifecycleRPCNode) CallRPC(ctx context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	f.nodeID = nodeID
	f.serviceID = serviceID
	f.payload = append([]byte(nil), payload...)
	return f.handler(ctx, payload)
}

func sameControllerVoters(left, right []ControllerVoter) bool {
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
