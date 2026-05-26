package control

import (
	"context"
	"time"

	clusternet "github.com/WuKongIM/WuKongIM/pkg/clusterv2/net"
	cv2sync "github.com/WuKongIM/WuKongIM/pkg/controllerv2/sync"
	"go.etcd.io/raft/v3/raftpb"
)

const defaultControlRPCTimeout = 200 * time.Millisecond

type raftStepper interface {
	Step(context.Context, raftpb.Message) error
}

// RaftTransport sends ControllerV2 Raft messages over clusterv2 typed RPC.
type RaftTransport struct {
	caller  clusternet.Caller
	timeout time.Duration
}

// NewRaftTransport creates a ControllerV2 Raft transport backed by caller.
func NewRaftTransport(caller clusternet.Caller) *RaftTransport {
	return &RaftTransport{caller: caller, timeout: defaultControlRPCTimeout}
}

// Send sends messages grouped by destination node without blocking indefinitely.
func (t *RaftTransport) Send(messages []raftpb.Message) {
	if t == nil || t.caller == nil {
		return
	}
	byNode := make(map[uint64][]raftpb.Message)
	for _, msg := range messages {
		if msg.To == 0 {
			continue
		}
		byNode[msg.To] = append(byNode[msg.To], msg)
	}
	for nodeID, batch := range byNode {
		payload, err := EncodeRaftBatch(batch)
		if err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), t.timeout)
		_, _ = t.caller.Call(ctx, nodeID, clusternet.RPCControlRaft, payload)
		cancel()
	}
}

// NewRaftHandler creates an RPC handler that steps decoded ControllerV2 Raft messages.
func NewRaftHandler(stepper raftStepper) clusternet.Handler {
	return clusternet.HandlerFunc(func(ctx context.Context, payload []byte) ([]byte, error) {
		messages, err := DecodeRaftBatch(payload)
		if err != nil {
			return nil, err
		}
		for _, msg := range messages {
			if stepper == nil {
				continue
			}
			if err := stepper.Step(ctx, msg); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
}

// StateSyncEndpoint adapts clusterv2 RPC to controllerv2/sync.Endpoint.
type StateSyncEndpoint struct {
	caller clusternet.Caller
	nodeID uint64
}

// NewStateSyncEndpoint creates an endpoint for one remote ControllerV2 state peer.
func NewStateSyncEndpoint(caller clusternet.Caller, nodeID uint64) *StateSyncEndpoint {
	return &StateSyncEndpoint{caller: caller, nodeID: nodeID}
}

// GetState sends a ControllerV2 state sync request to the remote node.
func (e *StateSyncEndpoint) GetState(ctx context.Context, req cv2sync.GetStateRequest) (cv2sync.GetStateResponse, error) {
	payload, err := EncodeStateSyncRequest(req)
	if err != nil {
		return cv2sync.GetStateResponse{}, err
	}
	resp, err := e.caller.Call(ctx, e.nodeID, clusternet.RPCControlStateSync, payload)
	if err != nil {
		return cv2sync.GetStateResponse{}, err
	}
	return DecodeStateSyncResponse(resp)
}

// NewStateSyncHandler creates an RPC handler for a ControllerV2 state sync endpoint.
func NewStateSyncHandler(endpoint cv2sync.Endpoint) clusternet.Handler {
	return clusternet.HandlerFunc(func(ctx context.Context, payload []byte) ([]byte, error) {
		req, err := DecodeStateSyncRequest(payload)
		if err != nil {
			return nil, err
		}
		resp, err := endpoint.GetState(ctx, req)
		if err != nil {
			return nil, err
		}
		return EncodeStateSyncResponse(resp)
	})
}
