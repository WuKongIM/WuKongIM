package node

import (
	"testing"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
)

func TestManagerControllerRaftRPCRoundTripStatus(t *testing.T) {
	resp := managerControllerRaftRPCResponse{
		Status: rpcStatusOK,
		RaftStatus: managementusecase.ControllerRaftStatus{
			NodeID:        1,
			Role:          "leader",
			LeaderID:      1,
			Term:          4,
			FirstIndex:    2,
			LastIndex:     10,
			SnapshotIndex: 2,
		},
	}

	encoded, err := encodeManagerControllerRaftResponse(resp)
	if err != nil {
		t.Fatalf("encodeManagerControllerRaftResponse() error = %v", err)
	}
	decoded, err := decodeManagerControllerRaftResponse(encoded)
	if err != nil {
		t.Fatalf("decodeManagerControllerRaftResponse() error = %v", err)
	}

	if decoded.Status != resp.Status || decoded.RaftStatus.NodeID != 1 || decoded.RaftStatus.LastIndex != 10 {
		t.Fatalf("decoded response = %#v, want status round trip", decoded)
	}
}

func TestManagerControllerRaftRPCRoundTripCompact(t *testing.T) {
	req := managerControllerRaftRPCRequest{Op: managerControllerRaftOpCompact, NodeID: 2}

	encoded, err := encodeManagerControllerRaftRequest(req)
	if err != nil {
		t.Fatalf("encodeManagerControllerRaftRequest() error = %v", err)
	}
	decoded, err := decodeManagerControllerRaftRequest(encoded)
	if err != nil {
		t.Fatalf("decodeManagerControllerRaftRequest() error = %v", err)
	}

	if decoded != req {
		t.Fatalf("decoded request = %#v, want %#v", decoded, req)
	}
}
